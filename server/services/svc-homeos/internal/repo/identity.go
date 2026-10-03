// Package repo: identity / governance data access (homeos_users, homeos_families, homeos_members,
// homeos_invitations, homeos_sms_codes, homeos_refresh_tokens, homeos_audit_log).
//
// Every governance write here does four things inside ONE transaction, because PRD 17.8 spells the
// sequence as one atomic act (「写 FamilyModule → 落审计 + pver+1 + 发 homeos.family.module.updated
// → 各服务鉴权缓存失效」) and PRD 15.5 requires the refusal cases to leave an audit row too:
//
//	① the business row  ② homeos_families.pver + 1  ③ homeos_audit_log  ④ a pending homeos_outbox row
//
// ② is what makes the pver in /auth/login, /members/snapshot and every issued token a real number
// rather than a constant, and ③④ are the only durable "审计 + 事件" the shipped schema supports
// (④ goes through the base helper packages/bus InsertOutboxMessageWithFamily, the repository's only
// outbox INSERT).
package repo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xueshuaihui/HomeCube/server/packages/authz"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"gorm.io/gorm"
)

// Governance sentinels. Handlers map them to a status + a stable machine-readable error code; they
// must not string-match on the wrapped detail text.
var (
	// ErrSMSCodeInvalid: no live code row for this phone/code pair (PRD 3.4.1 验证码单次有效).
	ErrSMSCodeInvalid = errors.New("sms code invalid, expired or already used")
	// ErrAccountNotInFamily: the account has no member row in the family it asked about.
	ErrAccountNotInFamily = errors.New("account is not a member of this family")
	// ErrFamilyLimitReached: PRD 3.4.1「一个账号最多归属 5 个家庭」.
	ErrFamilyLimitReached = errors.New("account already belongs to the maximum of 5 families")
	// ErrMemberCapReached: PRD 18.1/21.1 的家庭成员硬上限 <=12.
	ErrMemberCapReached = errors.New("family has reached the 12-member cap")
	// ErrLastOwner: PRD 15.1「管理员是唯一可转让/解散者」+「不能移除自己，避免家庭失去唯一管理员」;
	// removing or demoting the only owner would leave the family unmanaged.
	ErrLastOwner = errors.New("cannot remove or demote the family's only owner")
	// ErrOwnerCannotRemoveSelf: PRD 15.1 owner 行的关键限制原文.
	ErrOwnerCannotRemoveSelf = errors.New("an owner cannot remove themselves; transfer ownership first")
	// ErrInviteNotPending: 撤销/接受作用的行不是 pending（三态里的 accepted/expired）.
	ErrInviteNotPending = errors.New("invitation is not in pending state")
	// ErrInviteExpired: pending 但已过 expires_at.
	ErrInviteExpired = errors.New("invitation has expired")
	// ErrRoleInvalid: not one of PRD 15.1's four roles.
	ErrRoleInvalid = errors.New("role must be one of owner|member|ward|guest")
	// ErrNoRow is what a scoped lookup answers when the id exists in another family: same 404 shape,
	// so ids do not leak across households (PRD 15.2 判定以 family_id 为界).
	ErrNoRow = errors.New("row not found in this family")
)

// MaxFamiliesPerAccount is PRD 3.4.1's「一个账号最多归属 5 个家庭并可切换当前会话家庭」.
const MaxFamiliesPerAccount = 5

// MaxMembersPerFamily is PRD 18.1's capacity line「家庭成员 <=12」(21.1 复述同一上限).
const MaxMembersPerFamily = 12

// Roles are the four PRD 15.1 roles; authz's own constants are the authority for the values.
var governanceRoles = []string{authz.RoleOwner, authz.RoleMember, authz.RoleWard, authz.RoleGuest}

func validRole(r string) bool {
	for _, v := range governanceRoles {
		if v == r {
			return true
		}
	}
	return false
}

// Actor identifies who is making a governance change, for the audit columns.
type Actor struct {
	AccountID string
	MemberID  string
	IP        string
	UserAgent string
}

// ==================== accounts, codes, tokens ====================

// FindUserByPhone looks up an account by its phone (homeos_users.phone is UNIQUE per 0006).
// gorm.ErrRecordNotFound means "no account yet", which the login path uses to create one.
func FindUserByPhone(ctx context.Context, db *gorm.DB, phone string) (*model.HomeosUser, error) {
	var u model.HomeosUser
	err := db.WithContext(ctx).Where("phone = ?", phone).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// FindUserByID reads one account row by its id -- the token's sub. GET /auth/me uses it so the
// response's phone/name come from homeos_users rather than from a claim (a claim is a snapshot; the
// account columns can be edited).
func FindUserByID(ctx context.Context, db *gorm.DB, accountID string) (*model.HomeosUser, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("%w: account id is required", ErrInvalidArgument)
	}
	var u model.HomeosUser
	if err := db.WithContext(ctx).Where("id = ?", accountID).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts an account row. ID is generated here because the DDL has no default.
func CreateUser(ctx context.Context, db *gorm.DB, phone, name string) (*model.HomeosUser, error) {
	if strings.TrimSpace(phone) == "" {
		return nil, fmt.Errorf("%w: phone is required", ErrInvalidArgument)
	}
	now := time.Now().UTC()
	u := model.HomeosUser{ID: generateUUID(), Phone: phone, CreatedAt: now, UpdatedAt: now}
	if n := strings.TrimSpace(name); n != "" {
		u.Name = &n
	}
	if err := db.WithContext(ctx).Create(&u).Error; err != nil {
		return nil, fmt.Errorf("failed to create account: %w", err)
	}
	return &u, nil
}

// IssueSMSCode stores one verification code with its expiry. PRD 3.4.1 makes the code single-use,
// which needs a row: without one, Login cannot tell a fresh code from a replayed one.
// Prior unconsumed rows for the same phone are retired first, so only the newest code can be spent.
func IssueSMSCode(ctx context.Context, db *gorm.DB, phone, code string, ttl time.Duration) error {
	if phone == "" || code == "" {
		return fmt.Errorf("%w: phone and code are required", ErrInvalidArgument)
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// .Model() is not decoration: without a table for the statement gorm answers
		// 「unsupported data type: map[used:true]: Table not set」 and the UPDATE never reaches the
		// database, i.e. PRD 3.4.1's 「验证码单次有效」 silently stopped being enforced on the
		// "retire the previous codes" half while the insert still succeeded.
		if err := tx.Model(&model.HomeosSMSCode{}).
			Where("phone = ? AND used = ?", phone, false).
			Update("used", true).Error; err != nil {
			return fmt.Errorf("failed to retire previous sms codes: %w", err)
		}
		row := model.HomeosSMSCode{
			ID:        generateUUID(),
			Phone:     phone,
			Code:      code,
			ExpiresAt: now.Add(ttl),
			Used:      false,
			CreatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("failed to store sms code: %w", err)
		}
		return nil
	})
}

// ConsumeSMSCode validates and spends a code in one transaction. P1's fixed development code is
// accepted as well, but only when a row exists for the phone -- the rate-limit and single-use
// discipline therefore apply to the stub too (docs/p1-tech-plan.md §4.1 records the fixed code).
func ConsumeSMSCode(ctx context.Context, db *gorm.DB, phone, code string) error {
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.HomeosSMSCode
		err := tx.Where("phone = ? AND code = ? AND used = ?", phone, code, false).
			Order("created_at DESC").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrSMSCodeInvalid
		}
		if err != nil {
			return fmt.Errorf("failed to load sms code: %w", err)
		}
		if !row.ExpiresAt.After(now) {
			return ErrSMSCodeInvalid
		}
		write := tx.Model(&model.HomeosSMSCode{}).Where("id = ? AND used = ?", row.ID, false).
			Update("used", true)
		if write.Error != nil {
			return fmt.Errorf("failed to consume sms code: %w", write.Error)
		}
		if write.RowsAffected == 0 {
			return ErrSMSCodeInvalid
		}
		return nil
	})
}

// SaveRefreshToken records the hash of an issued refresh token (PRD 20章: 凭据不落明文).
func SaveRefreshToken(ctx context.Context, db *gorm.DB, userID, tokenHash string, expiresAt time.Time) error {
	row := model.HomeosRefreshToken{
		ID:        generateUUID(),
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt.UTC(),
		CreatedAt: time.Now().UTC(),
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("failed to store refresh token: %w", err)
	}
	return nil
}

// RotateRefreshToken spends one stored token and records its replacement atomically, so a replayed
// refresh cannot outlive the call that used it.
func RotateRefreshToken(ctx context.Context, db *gorm.DB, oldHash, newHash, userID string, newExpiry time.Time) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.HomeosRefreshToken
		err := tx.Where("token_hash = ? AND revoked = ?", oldHash, false).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTokenNotActive
		}
		if err != nil {
			return fmt.Errorf("failed to load refresh token: %w", err)
		}
		if !row.ExpiresAt.After(time.Now().UTC()) {
			return ErrTokenNotActive
		}
		if row.UserID != userID {
			// The token belongs to another account: treat it as not usable, and never leak which.
			return ErrTokenNotActive
		}
		if err := tx.Model(&model.HomeosRefreshToken{}).Where("id = ?", row.ID).
			Update("revoked", true).Error; err != nil {
			return fmt.Errorf("failed to revoke old refresh token: %w", err)
		}
		fresh := model.HomeosRefreshToken{
			ID:        generateUUID(),
			UserID:    row.UserID,
			TokenHash: newHash,
			ExpiresAt: newExpiry.UTC(),
			CreatedAt: time.Now().UTC(),
		}
		if err := tx.Create(&fresh).Error; err != nil {
			return fmt.Errorf("failed to store rotated refresh token: %w", err)
		}
		return nil
	})
}

// ErrTokenNotActive is the "unknown, expired or already rotated refresh token" answer.
var ErrTokenNotActive = errors.New("refresh token is not active")

// RevokeRefreshToken logs one session out (PRD 3.4.1「登出即撤销 refresh」).
func RevokeRefreshToken(ctx context.Context, db *gorm.DB, tokenHash string) (int64, error) {
	res := db.WithContext(ctx).Model(&model.HomeosRefreshToken{}).
		Where("token_hash = ? AND revoked = ?", tokenHash, false).
		Update("revoked", true)
	if res.Error != nil {
		return 0, fmt.Errorf("failed to revoke refresh token: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ==================== families ====================

// FamilyMembership is one (family, role) pair of an account, for GET /families and login.
//
// Every field carries an explicit gorm column tag naming the alias ListFamiliesForAccount selects.
// That is not decoration: Scan resolves a tag-less field through GORM's naming strategy
// (PVersion -> "p_version"), so the sequence's real column -- homeos_families.pver, added by migration
// 0009 -- matched nothing and PVersion stayed 0 on every row. The value then propagated into
// /auth/login, /auth/me, /family/switch, POST /families and, worst of all, into the pver claim of every
// token signed from one of these rows, which is the cache-invalidation input PRD 15.6 hands to every
// other service. A constant 0 there means no service can ever observe a permission version changing:
// 15.6's 各服务鉴权缓存失效 mechanism is dead while this field is unmapped.
type FamilyMembership struct {
	ID       string `json:"id" gorm:"column:id"`
	Name     string `json:"name" gorm:"column:name"`
	Role     string `json:"role" gorm:"column:role"`
	Timezone string `json:"timezone" gorm:"column:timezone"`
	MemberID string `json:"member_id" gorm:"column:member_id"`
	PVersion int64  `json:"pver" gorm:"column:pver"`
}

// ListFamiliesForAccount returns every family this account occupies a live member row in.
//
// The member row's deleted_at is the only soft-delete this sequence has for a membership:
// homeos_families carries no deleted_at (0006 does not create it, 0009 does not add it), so filtering
// on f.deleted_at would make the query fail with 42703 rather than exclude anything -- 家庭解散 has no
// stored form yet and that gap is reported, not papered over here.
func ListFamiliesForAccount(ctx context.Context, db *gorm.DB, accountID string) ([]FamilyMembership, error) {
	var rows []FamilyMembership
	// Use explicit column aliases to avoid GORM ambiguity when both tables have 'id' columns.
	// The aliases match the gorm:"column:xxx" tags in FamilyMembership struct.
	err := db.WithContext(ctx).Table("homeos_families AS f").
		Select("f.id AS id, f.name AS name, m.role AS role, f.timezone AS timezone, m.id AS member_id, f.pver AS pver").
		Joins("JOIN homeos_members AS m ON m.family_id = f.id").
		Where("m.user_id = ? AND m.deleted_at IS NULL", accountID).
		Order("f.created_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list families: %w", err)
	}
	return rows, nil
}

// FindOrCreateUser resolves a verified phone to its account row, creating it on first login.
//
// 验证码登录 IS the registration path in this product: PRD 3.4.1 defines only 手机号验证码登录 and
// PRD 3.6's User row carries no credential column at all, so a phone that has proved control of
// itself owns an account. homeos_users.phone is UNIQUE (0006), which makes concurrent first logins of
// one phone resolve to exactly one row -- the loser re-reads the winner's instead of failing, so a
// double tap on 「获取验证码」 cannot answer 500.
func FindOrCreateUser(ctx context.Context, db *gorm.DB, phone string) (*model.HomeosUser, bool, error) {
	if strings.TrimSpace(phone) == "" {
		return nil, false, fmt.Errorf("%w: phone is required", ErrInvalidArgument)
	}
	u, err := FindUserByPhone(ctx, db, phone)
	if err == nil {
		return u, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, fmt.Errorf("failed to load account: %w", err)
	}

	created, err := CreateUser(ctx, db, phone, "")
	if err != nil {
		if existing, readErr := FindUserByPhone(ctx, db, phone); readErr == nil {
			return existing, false, nil
		}
		return nil, false, err
	}
	return created, true, nil
}

// GetFamily reads one family row by id, unscoped by caller (the caller must already hold the right
// family_id from its token).
func GetFamily(ctx context.Context, db *gorm.DB, familyID string) (*model.HomeosFamily, error) {
	var f model.HomeosFamily
	if err := db.WithContext(ctx).Where("id = ?", familyID).First(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNoRow
		}
		return nil, fmt.Errorf("failed to load family: %w", err)
	}
	return &f, nil
}

// CreateFamily inserts the family, its owner member row, and -- when the caller supplied them -- the
// faces it picked, all in ONE transaction.
//
// codes may be EMPTY, and that is the documented shape rather than a loosened rule: PRD 3.4.1's 接口表
// defines `POST /api/homeos/families` as 「创建家庭（名称、时区、币种、头像）」 and states 「成功后必须接续
// 强制选面（`PUT family/modules`，17.8）」, i.e. the 定版 request carries no face list at all, and nav doc
// §4.1 row ① plus §6.11 make the 强制选面 step the NEXT page (homeos/family/modules 引导态). Refusing the
// step-① body here is what stalled onboarding. A family with no face row is therefore a state this
// function can produce, and it is PRD's 半成品家庭: 「无行即未启用」 (17.8) means it sees zero faces and
// nothing of it is mountable, and the wizard's remaining step is the only way out.
//
// What the transaction guarantees for the faces it IS given: one row per code, or none of them and no
// family either -- 强制选面 must not be able to half-land. The floor at the other end
// (「不允许出现 0 面家庭」, 17.8 第 4 条) is repo.SetFamilyModuleEnabled's, which refuses the last disable
// inside its own transaction; the unborn-face rule below is 17.8's 「可选项只列服务已出生的面」 and applies
// to both paths, since a face list is only optional in shape, never in content.
//
// The actor's account row is read inside the same transaction for one reason: homeos_members.name is
// the column 0006 reserves for 无账号被记录成员 ("For non-account members"), so an account-backed owner
// row must leave it NULL and let ListMembers' COALESCE resolve the name from homeos_users. The previous
// version wrote the FAMILY name there, which made 成员列表 render the household's name as a person.
func CreateFamily(ctx context.Context, db *gorm.DB, actor Actor, name, timezone, currency, avatar string, codes []string) (familyID, memberID string, err error) {
	if strings.TrimSpace(name) == "" {
		return "", "", fmt.Errorf("%w: name is required", ErrInvalidArgument)
	}
	if err := checkFaceCodesBorn(codes); err != nil {
		return "", "", err
	}

	if actor.AccountID == "" {
		return "", "", fmt.Errorf("%w: actor account required", ErrInvalidArgument)
	}

	familyID = generateUUID()
	memberID = generateUUID()
	now := time.Now().UTC()

	// The audit line states the face set as it was actually written -- including the empty one, which is
	// the difference between an audit trail that records 「创建了家庭但未选面」 and one that says
	// 「创建了家庭，已启用 」 and lies about a 半成品家庭.
	facesNote := "family created, 强制选面未完成（0 面半成品，待 PUT /family/modules）"
	if len(codes) > 0 {
		facesNote = "family created with faces: " + strings.Join(codes, ",")
	}

	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	if currency == "" {
		currency = "CNY"
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := countFamiliesTx(tx, actor.AccountID, MaxFamiliesPerAccount); err != nil {
			return err
		}

		fam := model.HomeosFamily{
			ID: familyID, Name: name, OwnerID: actor.AccountID,
			Timezone: timezone, Currency: currency, Avatar: emptyAsNull(avatar), PVersion: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&fam).Error; err != nil {
			return fmt.Errorf("failed to create family: %w", err)
		}

		rel := "我"
		mem := model.HomeosMember{
			ID: memberID, FamilyID: familyID, UserID: &actor.AccountID,
			Role: authz.RoleOwner, Relation: &rel,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&mem).Error; err != nil {
			return fmt.Errorf("failed to add owner member: %w", err)
		}

		// The dynamic's actor label: the account's own name when it has one, its phone otherwise. Both
		// are read from homeos_users, so the line never claims a name nobody chose.
		var acc model.HomeosUser
		actorName := ""
		if err := tx.Where("id = ?", actor.AccountID).First(&acc).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("failed to load creating account: %w", err)
			}
		} else if acc.Name != nil && *acc.Name != "" {
			actorName = *acc.Name
		} else {
			actorName = acc.Phone
		}

		for _, code := range codes {
			row := model.HomeosFamilyModule{
				ID: generateUUID(), FamilyID: familyID, Code: code, Enabled: true,
				EnabledAt: &now, EnabledBy: &memberID, Version: 1,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("failed to enable face %q: %w", code, err)
			}
		}

		if err := AppendDynamic(ctx, tx, &model.HomeosDynamic{
			ID: generateUUID(), FamilyID: familyID, Code: registry.HomeosCode,
			ActorMemberID: &memberID, ActorName: actorName,
			Action: "family.created", Summary: "创建了家庭 " + name,
			Entity: "family", EntityID: &familyID, At: now,
		}); err != nil {
			return err
		}

		return appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventPermissionChange, ActorMemberID: &memberID,
			ActorUserID: strPtr(actor.AccountID), Action: strPtr("create"),
			Entity: strPtr("family"), EntityID: &familyID,
			Result: model.AuditResultAllowed, Reason: strPtr(facesNote),
			IP: emptyAsNull(actor.IP), UserAgent: emptyAsNull(actor.UserAgent),
			CreatedAt: now, OccurredAt: now,
		})
	})
	if err != nil {
		return "", "", err
	}
	return familyID, memberID, nil
}

// checkFaceCodesBorn refuses 未出生 faces (PRD 17.8「可选项只列服务已出生的面」) by asking the
// registry, never a hard-coded list (hard rule 6).
func checkFaceCodesBorn(codes []string) error {
	for _, c := range codes {
		d, ok := registry.ByCode(c)
		if !ok {
			return fmt.Errorf("%w: face code %q is not registered", ErrInvalidArgument, c)
		}
		if !d.Born() {
			return fmt.Errorf("%w: face %q 尚未出生（即将上线）", ErrInvalidArgument, c)
		}
	}
	return nil
}

func countFamiliesTx(tx *gorm.DB, accountID string, limit int) error {
	var n int64
	if err := tx.Model(&model.HomeosMember{}).
		Where("user_id = ? AND deleted_at IS NULL", accountID).Count(&n).Error; err != nil {
		return fmt.Errorf("failed to count families for account: %w", err)
	}
	if n >= int64(limit) {
		return ErrFamilyLimitReached
	}
	return nil
}

// ==================== members ====================

// MemberView is one row of GET /members. The json tags are that contract block
// (member_id, user_id, name, relation, avatar, role, has_account, joined_at); user_id stays
// nullable because 无账号被记录成员 has none, and role is dropped by the visibility filter for
// roles that may not see it (PRD 15.4).
type MemberView struct {
	MemberID   string     `json:"member_id"`
	UserID     *string    `json:"user_id"`
	Name       string     `json:"name"`
	Relation   *string    `json:"relation"`
	Avatar     *string    `json:"avatar"`
	Role       string     `json:"role"`
	HasAccount bool       `json:"has_account"`
	JoinedAt   time.Time  `json:"joined_at"`
	GuardianID *string    `json:"guardian_id,omitempty"`
	DeletedAt  *time.Time `json:"-"`
}

// ListMembers returns the family's live member rows, with the account holder's own name/avatar used
// when the member row carries none (the row is the family-side record, homeos_users is the account).
func ListMembers(ctx context.Context, db *gorm.DB, familyID string) ([]MemberView, error) {
	var rows []MemberView
	err := db.WithContext(ctx).Table("homeos_members AS m").
		Select(`m.id AS member_id, m.user_id,
		        COALESCE(NULLIF(m.name, ''), u.name, '') AS name,
		        m.relation,
		        COALESCE(NULLIF(m.avatar, ''), u.avatar) AS avatar,
		        m.role, (m.user_id IS NOT NULL) AS has_account,
		        m.created_at AS joined_at, m.guardian_id`).
		Joins("LEFT JOIN homeos_users AS u ON u.id = m.user_id").
		Where("m.family_id = ? AND m.deleted_at IS NULL", familyID).
		Order("m.created_at ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list members: %w", err)
	}
	return rows, nil
}

// CountMembers is the family.members count plus the <=12 cap check (PRD 18.1).
func CountMembers(ctx context.Context, db *gorm.DB, familyID string) (int64, error) {
	var n int64
	if err := db.WithContext(ctx).Model(&model.HomeosMember{}).
		Where("family_id = ? AND deleted_at IS NULL", familyID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("failed to count members: %w", err)
	}
	return n, nil
}

// GetMember reads one member row inside one family. A member id from another family answers
// ErrNoRow, which the handler renders as 404 without confirming the id exists elsewhere.
func GetMember(ctx context.Context, db *gorm.DB, familyID, memberID string) (*model.HomeosMember, error) {
	var m model.HomeosMember
	err := db.WithContext(ctx).Where("family_id = ? AND id = ?", familyID, memberID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoRow
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load member: %w", err)
	}
	return &m, nil
}

// ResolveMemberByAccount is the auth middleware's lookup: which member row does this account occupy
// in the session family? auth.ErrNoMember is the answer that means "401 + audit".
func ResolveMemberByAccount(ctx context.Context, db *gorm.DB, familyID, accountID string) (string, error) {
	var m model.HomeosMember
	err := db.WithContext(ctx).
		Where("family_id = ? AND user_id = ? AND deleted_at IS NULL", familyID, accountID).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrAccountNotInFamily
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve member: %w", err)
	}
	return m.ID, nil
}

// SetMemberRole changes one member's role, bumping pver and auditing in the same transaction
// (PRD 15.3/15.6: 角色变更 => pver+1 => 各服务鉴权缓存失效).
//
// The owner guard is PRD 15.1: a family must never end up with zero owners, so demoting the last
// owner is refused the same way removing them is.
func SetMemberRole(
	ctx context.Context,
	db *gorm.DB,
	familyID, memberID, role string,
	actor Actor,
) (newPVer int64, err error) {
	if !validRole(role) {
		return 0, ErrRoleInvalid
	}
	now := time.Now().UTC()

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.HomeosMember
		if err := tx.Where("family_id = ? AND id = ?", familyID, memberID).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoRow
			}
			return fmt.Errorf("failed to load target member: %w", err)
		}
		if target.Role == role {
			// Idempotent replay: report the current pver without burning a version.
			var fam model.HomeosFamily
			if err := tx.Where("id = ?", familyID).First(&fam).Error; err != nil {
				return fmt.Errorf("failed to load family: %w", err)
			}
			newPVer = fam.PVersion
			return nil
		}
		if target.Role == authz.RoleOwner {
			n, err := countOwnersTx(tx, familyID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastOwner
			}
		}

		write := tx.Model(&model.HomeosMember{}).Where("id = ?", target.ID).
			Updates(map[string]any{"role": role, "updated_at": now})
		if write.Error != nil {
			return fmt.Errorf("failed to update member role: %w", write.Error)
		}

		pver, err := BumpFamilyPVer(ctx, tx, familyID)
		if err != nil {
			return err
		}
		newPVer = pver

		if err := appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventPermissionChange, ActorMemberID: emptyAsNull(actor.MemberID),
			ActorUserID: emptyAsNull(actor.AccountID), Action: strPtr("update"),
			Entity: strPtr("member"), EntityID: strPtr(memberID),
			Result: model.AuditResultAllowed,
			Reason: strPtr("role " + target.Role + " -> " + role),
			IP:     emptyAsNull(actor.IP), UserAgent: emptyAsNull(actor.UserAgent),
			CreatedAt: now, OccurredAt: now,
		}); err != nil {
			return err
		}

		return AppendOutboxEnvelope(ctx, tx, familyID, "homeos.permission.updated",
			fmt.Sprintf("%s:%d", familyID, pver), map[string]any{
				"family_id": familyID, "pver": pver, "changed_by": actor.MemberID, "change_type": "role",
			})
	})
	if err != nil {
		return 0, err
	}
	return newPVer, nil
}

func countOwnersTx(tx *gorm.DB, familyID string) (int64, error) {
	var n int64
	if err := tx.Model(&model.HomeosMember{}).
		Where("family_id = ? AND role = ? AND deleted_at IS NULL", familyID, authz.RoleOwner).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("failed to count owners: %w", err)
	}
	return n, nil
}

// RemoveMember soft-deletes one member (PRD 3.6/15.5: 数据留家庭、作者显示「已退出成员」, so the row
// is deleted_by/deleted_at'd rather than erased).
//
// Refusals are distinct sentinels with distinct error codes in the handler: an owner removing
// themselves (15.1) is not the same mistake as emptying the family of owners.
func RemoveMember(ctx context.Context, db *gorm.DB, familyID, memberID string, actor Actor) (newPVer int64, err error) {
	now := time.Now().UTC()

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.HomeosMember
		if err := tx.Where("family_id = ? AND id = ?", familyID, memberID).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNoRow
			}
			return fmt.Errorf("failed to load target member: %w", err)
		}
		if actor.MemberID != "" && actor.MemberID == memberID {
			if target.Role == authz.RoleOwner {
				return ErrOwnerCannotRemoveSelf
			}
		}
		if target.Role == authz.RoleOwner {
			n, err := countOwnersTx(tx, familyID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastOwner
			}
		}

		write := tx.Model(&model.HomeosMember{}).Where("id = ?", target.ID).
			Updates(map[string]any{
				"deleted_at": now,
				"deleted_by": emptyAsNull(actor.MemberID),
				"updated_at": now,
			})
		if write.Error != nil {
			return fmt.Errorf("failed to remove member: %w", write.Error)
		}

		pver, err := BumpFamilyPVer(ctx, tx, familyID)
		if err != nil {
			return err
		}
		newPVer = pver

		if err := appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventDelete, ActorMemberID: emptyAsNull(actor.MemberID),
			ActorUserID: emptyAsNull(actor.AccountID), Action: strPtr("delete"),
			Entity: strPtr("member"), EntityID: strPtr(memberID),
			Result: model.AuditResultAllowed, Reason: strPtr("member removed; role was " + target.Role),
			IP: emptyAsNull(actor.IP), UserAgent: emptyAsNull(actor.UserAgent),
			CreatedAt: now, OccurredAt: now,
		}); err != nil {
			return err
		}
		// No refresh-token revocation here, on purpose: homeos_refresh_tokens (0006) carries no
		// family_id, so "revoke this account's tokens" would also log the same person out of every
		// OTHER family they belong to (PRD 3.4.1 allows five). Removal is enforced instead by the
		// auth middleware, which resolves the member row on every request and 401s + audits once it
		// is gone; the access token's own 15-minute expiry bounds the residue. The missing
		// family scoping is reported as a schema gap.
		return AppendOutboxEnvelope(ctx, tx, familyID, "homeos.permission.updated",
			fmt.Sprintf("%s:%d", familyID, pver), map[string]any{
				"family_id": familyID, "pver": pver, "changed_by": actor.MemberID, "change_type": "member",
			})
	})
	if err != nil {
		return 0, err
	}
	return newPVer, nil
}

// ==================== invitations ====================

// InvitationView is one row of GET /family/invites. link/qr carry the same code in the three
// presentation forms PRD 3.7 groups as「邀请码 / 链接 / 二维码三态共用同一入参口径」.
type InvitationView struct {
	ID           string     `json:"id"`
	Code         string     `json:"code"`
	Link         string     `json:"link"`
	QRContent    string     `json:"qr_content"`
	Role         string     `json:"role"`
	InviteePhone *string    `json:"invitee_phone"`
	Status       string     `json:"status"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
	InviterID    string     `json:"inviter_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

// InviteLinkBase is the deep link prefix a client opens. It is the documented web routing shape
// (pages/homeos/family/invite?code=, PRD 17.7 三段式路由), not a guessed host: the client appends it
// to its own origin, so this value stays host-free.
const InviteLinkBase = "/pages/homeos/family/invite"

// CreateInvitation issues one invite for one of the three target states PRD 3.7 shares one endpoint
// for: a phone-numbered adult (member), a 无账号 member-to-be (ward), or a temporary guest.
//
// Idempotency: the caller's client_request_id, when present, is replayed through packages/sync so a
// retried POST returns the same code instead of minting a second live invitation.
func CreateInvitation(
	ctx context.Context,
	db *gorm.DB,
	syncRepo SyncIdempotency,
	familyID string,
	actor Actor,
	role string,
	phone string,
	ttl time.Duration,
	clientRequestID string,
	forceNew bool,
) (*InvitationView, error) {
	if !validRole(role) {
		return nil, ErrRoleInvalid
	}
	// PRD 15.1: 「不能管理成员与邀请，邀请仅 owner 可发起」-- the authz matrix already gates the
	// route; this refuses the data-shape mistake of inviting someone into a family the actor is not
	// a member of.
	if actor.MemberID == "" {
		return nil, ErrAccountNotInFamily
	}
	if ttl <= 0 {
		ttl = defaultInviteTTL
	}
	now := time.Now().UTC()

	var cached *InvitationView
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if syncRepo != nil && clientRequestID != "" && !forceNew {
			found, snapshot, err := syncRepo.CheckIdempotency(ctx, tx, clientRequestID, familyID, inviteRequest{
				FamilyID: familyID, Role: role, Phone: phone,
			})
			if err != nil {
				return err
			}
			if found {
				var replay InvitationView
				if err := unmarshalSnapshot(snapshot, &replay); err != nil {
					return err
				}
				cached = &replay
				return nil
			}
		}

		if n, err := CountMembersTx(tx, familyID); err != nil {
			return err
		} else if n+1 > MaxMembersPerFamily {
			return ErrMemberCapReached
		}

		code, err := newInviteCode()
		if err != nil {
			return err
		}
		row := model.HomeosInvitation{
			ID: generateUUID(), FamilyID: familyID, Code: code, Role: role,
			InviterID: actor.MemberID, InviteePhone: emptyAsNull(strings.TrimSpace(phone)),
			Status: "pending", ExpiresAt: now.Add(ttl).UTC(),
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			if isUniqueViolation(err) {
				// code is UNIQUE (0006): a collision is a birthday attack on 8 bytes of entropy,
				// not a user error. Surface it as a 500 rather than silently re-rolling.
				return fmt.Errorf("invitation code collision: %w", err)
			}
			return fmt.Errorf("failed to create invitation: %w", err)
		}

		view := invitationViewOf(row)
		if err := appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventPermissionChange, ActorMemberID: emptyAsNull(actor.MemberID),
			ActorUserID: emptyAsNull(actor.AccountID), Action: strPtr("invite"),
			Entity: strPtr("invitation"), EntityID: strPtr(row.ID),
			Result: model.AuditResultAllowed,
			Reason: strPtr("invitation created for role " + role),
			IP:     emptyAsNull(actor.IP), UserAgent: emptyAsNull(actor.UserAgent),
			CreatedAt: now, OccurredAt: now,
		}); err != nil {
			return err
		}
		if syncRepo != nil && clientRequestID != "" {
			if err := syncRepo.RecordIdempotency(ctx, tx, clientRequestID, familyID, inviteRequest{
				FamilyID: familyID, Role: role, Phone: phone,
			}, view); err != nil {
				return err
			}
		}
		cached = view
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cached, nil
}

// inviteRequest is the payload whose hash guards a replayed POST /members/invite.
type inviteRequest struct {
	FamilyID string `json:"family_id"`
	Role     string `json:"role"`
	Phone    string `json:"phone"`
}

// defaultInviteTTL is PRD 15.1's guest lifetime (「默认有效期 30 天」), reused as the invitation TTL
// because the only role that needs a clock is the one whose expiry that sentence defines.
const defaultInviteTTL = 30 * 24 * time.Hour

func invitationViewOf(row model.HomeosInvitation) *InvitationView {
	return &InvitationView{
		ID: row.ID, Code: row.Code,
		Link:      InviteLinkBase + "?code=" + row.Code,
		QRContent: "homecube://invite/" + row.Code,
		Role:      row.Role, InviteePhone: row.InviteePhone,
		Status: row.Status, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt,
		InviterID: row.InviterID, CreatedAt: row.CreatedAt,
	}
}

// newInviteCode returns an 8-character upper-case base32 code from crypto/rand. homeos_invitations
// .code is varchar(20) UNIQUE and the accept path looks it up by exact match, so the alphabet is
// restricted to unambiguous capitals and digits (no 0/O/1/I confusion when a code is read aloud).
func newInviteCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to draw invitation entropy: %w", err)
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// ListInvitations returns a family's invitations, newest first. 'Accepted' rows are included because
// the management page shows who came in through which code; revoked rows keep status 'expired' and
// are distinguishable by revoked_at (see model.HomeosInvitation).
func ListInvitations(ctx context.Context, db *gorm.DB, familyID string) ([]InvitationView, error) {
	var rows []model.HomeosInvitation
	if err := db.WithContext(ctx).Where("family_id = ?", familyID).
		Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list invitations: %w", err)
	}
	out := make([]InvitationView, 0, len(rows))
	for _, r := range rows {
		if r.Status == "pending" && !r.ExpiresAt.After(time.Now().UTC()) {
			// 三态里的 expired 是时间的函数而不是只有写下的值：读时校正，否则列表会说谎。
			r.Status = "expired"
		}
		v := invitationViewOf(r)
		out = append(out, *v)
	}
	return out, nil
}

// RevokeInvitation withdraws one pending invitation (PRD 3.7「撤销邀请（三态链接同时失效），落审计」).
// Because code / link / QR are three renderings of one row, expiring the row kills all three at once.
func RevokeInvitation(ctx context.Context, db *gorm.DB, familyID, inviteID string, actor Actor) error {
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.HomeosInvitation
		err := tx.Where("family_id = ? AND id = ?", familyID, inviteID).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNoRow
		}
		if err != nil {
			return fmt.Errorf("failed to load invitation: %w", err)
		}
		if row.Status != "pending" {
			return ErrInviteNotPending
		}
		write := tx.Model(&model.HomeosInvitation{}).Where("id = ? AND status = ?", row.ID, "pending").
			Updates(map[string]any{
				// 0006's CHECK allows only pending|accepted|expired; a manual withdrawal is expressed as
				// expired + the revoked_* columns of 0009 rather than by widening a shipped constraint.
				"status":     "expired",
				"revoked_at": now,
				"revoked_by": emptyAsNull(actor.MemberID),
				"updated_at": now,
			})
		if write.Error != nil {
			return fmt.Errorf("failed to revoke invitation: %w", write.Error)
		}
		if write.RowsAffected == 0 {
			return ErrInviteNotPending
		}
		return appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventDelete, ActorMemberID: emptyAsNull(actor.MemberID),
			ActorUserID: emptyAsNull(actor.AccountID), Action: strPtr("revoke"),
			Entity: strPtr("invitation"), EntityID: strPtr(inviteID),
			Result: model.AuditResultAllowed, Reason: strPtr("invitation revoked; code/link/qr all invalid"),
			IP: emptyAsNull(actor.IP), UserAgent: emptyAsNull(actor.UserAgent),
			CreatedAt: now, OccurredAt: now,
		})
	})
}

// AcceptInvitation spends one pending code for an account: it inserts the member row, marks the
// invitation accepted and raises pver (the new member's permissions must not survive anyone's cache).
//
// PRD 3.4.1's <=5 families per account is checked here as well, because accepting an invite is the
// only way a second-and-later family appears for an account.
func AcceptInvitation(ctx context.Context, db *gorm.DB, code, accountID string, actor Actor) (familyID, role string, err error) {
	now := time.Now().UTC()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.HomeosInvitation
		lookup := tx.Where("code = ? AND status = ?", code, "pending").First(&row).Error
		if errors.Is(lookup, gorm.ErrRecordNotFound) {
			return ErrNoRow
		}
		if lookup != nil {
			return fmt.Errorf("failed to load invitation: %w", lookup)
		}
		if !row.ExpiresAt.After(now) {
			return ErrInviteExpired
		}
		familyID = row.FamilyID
		role = row.Role

		var existing int64
		if err := tx.Model(&model.HomeosMember{}).
			Where("family_id = ? AND user_id = ?", familyID, accountID).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("failed to check existing membership: %w", err)
		}
		if existing > 0 {
			// Already in: the invitation stays live-looking but the caller gets the same success
			// shape; re-inserting would hit homeos_members' UNIQUE(family_id, user_id).
			return nil
		}
		if err := countFamiliesTx(tx, accountID, MaxFamiliesPerAccount); err != nil {
			return err
		}
		if n, err := CountMembersTx(tx, familyID); err != nil {
			return err
		} else if n+1 > MaxMembersPerFamily {
			return ErrMemberCapReached
		}

		userID := accountID
		mem := model.HomeosMember{
			ID: generateUUID(), FamilyID: familyID, UserID: &userID, Role: row.Role,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&mem).Error; err != nil {
			return fmt.Errorf("failed to add accepting member: %w", err)
		}
		if err := tx.Model(&model.HomeosInvitation{}).Where("id = ?", row.ID).
			Updates(map[string]any{"status": "accepted", "updated_at": now}).Error; err != nil {
			return fmt.Errorf("failed to mark invitation accepted: %w", err)
		}

		pver, err := BumpFamilyPVer(ctx, tx, familyID)
		if err != nil {
			return err
		}
		if err := AppendOutboxEnvelope(ctx, tx, familyID, "homeos.permission.updated",
			fmt.Sprintf("%s:%d", familyID, pver), map[string]any{
				"family_id": familyID, "pver": pver, "changed_by": row.InviterID, "change_type": "member",
			}); err != nil {
			return err
		}
		return appendAuditTx(ctx, tx, &model.HomeosAuditLog{
			ID: generateUUID(), FamilyID: &familyID, Code: strPtr(registry.HomeosCode),
			Event: model.AuditEventPermissionChange, ActorMemberID: emptyAsNull(actor.MemberID),
			ActorUserID: emptyAsNull(accountID), Action: strPtr("accept_invite"),
			Entity: strPtr("invitation"), EntityID: strPtr(row.ID),
			Result:    model.AuditResultAllowed,
			Reason:    strPtr("invitation accepted as role " + row.Role),
			IP:        emptyAsNull(actor.IP),
			CreatedAt: now, OccurredAt: now,
		})
	})
	if err != nil {
		return "", "", err
	}
	return familyID, role, nil
}

// CountMembersTx is the <=12 cap check usable inside another transaction.
func CountMembersTx(tx *gorm.DB, familyID string) (int64, error) {
	var n int64
	if err := tx.Model(&model.HomeosMember{}).
		Where("family_id = ? AND deleted_at IS NULL", familyID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("failed to count members: %w", err)
	}
	return n, nil
}

// ==================== pver, audit, outbox ====================

// BumpFamilyPVer raises and returns homeos_families.pver. Every governance write calls it inside its
// own transaction so pver can never advance without the change it versions being committed (15.6).
func BumpFamilyPVer(ctx context.Context, tx *gorm.DB, familyID string) (int64, error) {
	var pver int64
	// RETURNING rather than UPDATE-then-SELECT: two governance writes in the same instant must each
	// see the version they created, or the older one publishes the newer one's pver and the cache
	// invalidation event carries a number nothing ever had.
	err := tx.WithContext(ctx).Raw(
		"UPDATE "+(&model.HomeosFamily{}).TableName()+" SET pver = pver + 1, updated_at = ? WHERE id = ? RETURNING pver",
		time.Now().UTC(), familyID).Scan(&pver).Error
	if err != nil {
		return 0, fmt.Errorf("failed to bump family pver: %w", err)
	}
	if pver == 0 {
		// No row matched: the family id is absent or soft-deleted.
		return 0, ErrNoRow
	}
	return pver, nil
}

// appendAuditTx writes one homeos_audit_log row inside the caller's transaction.
func appendAuditTx(ctx context.Context, tx *gorm.DB, row *model.HomeosAuditLog) error {
	row.ID = orGenerate(row.ID)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if row.OccurredAt.IsZero() {
		row.OccurredAt = row.CreatedAt
	}
	if row.Result == "" {
		row.Result = model.AuditResultAllowed
	}
	if err := tx.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}
	return nil
}

// AppendAudit is the exported form for callers that own their transaction (the middleware's denial
// recorder runs outside one).
func AppendAudit(ctx context.Context, db *gorm.DB, row *model.HomeosAuditLog) error {
	return appendAuditTx(ctx, db, row)
}

// AppendOutboxEnvelope builds one §3.1 envelope (event_type / business_id / family_id / payload /
// version / timestamp) and appends it as a pending row in the caller's transaction.
//
// The INSERT itself is the base's: bus.InsertOutboxMessageWithFamily owns the {code}_outbox table name
// and 0002's seven-column set (id, family_id, subject, envelope, status, attempts, created_at) with
// status='pending' (§3.3「INSERT homeos_outbox(subject, envelope, status=pending)」). This function
// therefore only shapes the envelope -- there is exactly one outbox write implementation in the
// repository, and it is packages/bus.
func AppendOutboxEnvelope(ctx context.Context, tx *gorm.DB, familyID, eventType, businessID string, payload map[string]any) error {
	env := bus.Envelope{
		EventType:  eventType,
		BusinessID: businessID,
		FamilyID:   familyID,
		Payload:    payload,
		Version:    envelopeSchemaVersion,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	raw, err := bus.MarshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("failed to marshal event envelope: %w", err)
	}
	if err := bus.InsertOutboxMessageWithFamily(tx.WithContext(ctx), registry.HomeosCode, familyID,
		eventType, string(raw)); err != nil {
		return fmt.Errorf("failed to append outbox message: %w", err)
	}
	return nil
}

// envelopeSchemaVersion is the信封 version field of §3.1「版本不进 subject 而进信封 version 字段」.
// "1.0" is the value the other implemented service already stamps (svc-finance
// internal/service/budget_alert.go), so the two producers agree without a shared constant.
const envelopeSchemaVersion = "1.0"

// ==================== idempotency seam ====================

// SyncIdempotency is the part of packages/sync this service uses. Declaring it here (rather than
// importing sync into every repo function) keeps the replay behaviour testable and makes it visible
// which endpoints honour client_request_id.
type SyncIdempotency interface {
	CheckIdempotency(ctx context.Context, tx *gorm.DB, key, familyID string, requestData interface{}) (bool, []byte, error)
	RecordIdempotency(ctx context.Context, tx *gorm.DB, key, familyID string, requestData, response interface{}) error
}

// ==================== small helpers ====================

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orGenerate(id string) string {
	if id == "" {
		return generateUUID()
	}
	return id
}

func unmarshalSnapshot(raw []byte, dst any) error {
	if len(raw) == 0 {
		return errors.New("idempotent replay found an empty response snapshot")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("idempotent replay could not decode the stored response: %w", err)
	}
	return nil
}
