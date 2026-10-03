// Package model: identity-side rows of migration 0006 (+0009). These are the tables the
// auth / family / member / invitation handlers write. Before this file the handlers addressed
// them as db.Table("homeos_x").Create(map[string]interface{}{...}), which cannot express the
// soft-delete columns (deleted_by) or the NOT NULL / uuid typing the DDL asserts.
//
// Column sets below are read off server/migrations/homeos/homeos_0006_auth_family.up.sql and
// homeos_0009_member_governance.up.sql; nothing here adds a column the sequence does not create.
package model

import (
	"time"

	"gorm.io/gorm"
)

// HomeosUser is the account row (PRD 3.6「User | 账号」, 3.4.1 手机号验证码登录).
// An account may belong to up to 5 families through homeos_members (3.4.1).
//
// No DeletedAt: homeos_users has no deleted_at column in 0006's DDL and no later migration adds one.
// Declaring one made GORM append `deleted_at IS NULL` to every query through this model, so
// repo.FindUserByPhone answered 42703 undefined_column -- which is what pushed the login handler into
// writing homeos_users SQL by hand. 账号注销 is not in P1's scope (PRD 3.4.1 lists only 登录/家庭),
// so the honest shape is a hard row with no soft-delete column; when 注销 lands it comes as a migration
// first and this field second.
type HomeosUser struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	Phone     string    `gorm:"column:phone;type:varchar(20);uniqueIndex" json:"phone"`
	Name      *string   `gorm:"column:name;type:varchar(100)" json:"name"`
	Avatar    *string   `gorm:"column:avatar;type:varchar(500)" json:"avatar"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the table name for HomeosUser.
func (HomeosUser) TableName() string { return setTableName("homeos_users") }

// HomeosFamily is the household row (PRD 3.6「Family | id、name、owner_id、timezone、currency、
// feature_flags」). pver is the permission version of migration 0009: PRD 15.6 makes it the
// cache-invalidation input every service's authz SDK reads, and /auth/login plus
// /members/snapshot both carry it, so it needs a stored source rather than a constant.
//
// No DeletedAt/DeletedBy either: 0006 builds homeos_families without them and 0009 only adds pver.
// PRD 3.4.1 defines 解散家庭 as owner-only but P1's shipped migration sequence has no column for it,
// which is reported as a migration gap -- not patched by inventing a column GORM would then filter on.
type HomeosFamily struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	Name      string    `gorm:"column:name;type:varchar(100);not null" json:"name"`
	OwnerID   string    `gorm:"column:owner_id;type:uuid;not null" json:"owner_id"`
	Timezone  string    `gorm:"column:timezone;type:varchar(50);not null;default:Asia/Shanghai" json:"timezone"`
	Currency  string    `gorm:"column:currency;type:varchar(10);not null;default:CNY" json:"currency"`
	Avatar    *string   `gorm:"column:avatar;type:varchar(500)" json:"avatar"`
	PVersion  int64     `gorm:"column:pver;not null;default:1" json:"pver"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName returns the table name for HomeosFamily.
func (HomeosFamily) TableName() string { return setTableName("homeos_families") }

// HomeosMember is one (account, family) link with a role, or one 无账号被记录成员 when UserID
// is NULL (PRD 3.6「Member | id、family_id、user_id、role、relation、avatar、guardian_id」,
// 15.1 儿童/老人行). has_account, which the /members contract exposes, is derived from
// UserID != NULL: the DDL has no separate column and a second flag would be a second truth.
type HomeosMember struct {
	ID       string `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	FamilyID string `gorm:"column:family_id;type:uuid;not null;index:idx_homeos_members_family;uniqueIndex:homeos_members_family_user_uidx,priority:1" json:"family_id"`
	// NULL user_id = 无账号被记录成员。0006 的 UNIQUE(family_id, user_id) 在 Postgres 下视 NULL 互不
	// 相等，所以一个家庭可以有任意多条无账号行，而同一账号在一个家庭里只有一行。
	UserID     *string        `gorm:"column:user_id;type:uuid;index:idx_homeos_members_user;uniqueIndex:homeos_members_family_user_uidx,priority:2" json:"user_id"`
	Role       string         `gorm:"column:role;type:varchar(20);not null" json:"role"`
	Relation   *string        `gorm:"column:relation;type:varchar(50)" json:"relation"`
	Name       *string        `gorm:"column:name;type:varchar(100)" json:"name"`
	Avatar     *string        `gorm:"column:avatar;type:varchar(500)" json:"avatar"`
	GuardianID *string        `gorm:"column:guardian_id;type:uuid" json:"guardian_id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"-"`
	DeletedBy  *string        `gorm:"column:deleted_by;type:uuid" json:"-"`
}

// TableName returns the table name for HomeosMember.
func (HomeosMember) TableName() string { return setTableName("homeos_members") }

// HasAccount reports the /members contract field of the same name: an account-backed member can
// log in, a 无账号被记录成员 cannot and must not appear in the invitation list (PRD 3.4.1).
func (m HomeosMember) HasAccount() bool { return m.UserID != nil && *m.UserID != "" }

// DisplayName resolves the name a viewer sees. Account holders carry their name on homeos_users
// (the handler joins it in); 无账号成员的名字只在本行的 name 列上。
func (m HomeosMember) DisplayName() string {
	if m.Name != nil && *m.Name != "" {
		return *m.Name
	}
	return ""
}

// HomeosInvitation is one invitation (PRD 3.4.1 邀请、3.7「POST /members/invite」「DELETE
// /family/invites/{id} 撤销邀请（三态链接同时失效）」).
//
// Status keeps 0006's three-value CHECK ('pending','accepted','expired'): 三态 is a 定版 statement
// and this card does not widen a shipped constraint. A 撤销 therefore lands as status='expired'
// plus the two revoked_* columns of 0009, which is what lets GET /family/invites still tell
// "withdrawn by hand" apart from "timed out".
type HomeosInvitation struct {
	ID           string     `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	FamilyID     string     `gorm:"column:family_id;type:uuid;not null;index:idx_homeos_invitations_family_status,priority:1" json:"family_id"`
	Code         string     `gorm:"column:code;type:varchar(20);not null;uniqueIndex" json:"code"`
	Role         string     `gorm:"column:role;type:varchar(20);not null" json:"role"`
	InviterID    string     `gorm:"column:inviter_id;type:uuid;not null" json:"inviter_id"`
	InviteePhone *string    `gorm:"column:invitee_phone;type:varchar(20)" json:"invitee_phone"`
	Status       string     `gorm:"column:status;type:varchar(20);not null;default:pending;index:idx_homeos_invitations_family_status,priority:2" json:"status"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null" json:"expires_at"`
	RevokedAt    *time.Time `gorm:"column:revoked_at" json:"revoked_at"`
	RevokedBy    *string    `gorm:"column:revoked_by;type:uuid" json:"revoked_by"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TableName returns the table name for HomeosInvitation.
func (HomeosInvitation) TableName() string { return setTableName("homeos_invitations") }

// HomeosSMSCode is one issued verification code (PRD 3.4.1). Login consumes it: without a stored
// row the code cannot expire, cannot be single-use, and cannot be rate limited.
type HomeosSMSCode struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	Phone     string    `gorm:"column:phone;type:varchar(20);not null;index:idx_homeos_sms_codes_phone" json:"phone"`
	Code      string    `gorm:"column:code;type:varchar(10);not null" json:"code"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null" json:"expires_at"`
	Used      bool      `gorm:"column:used;not null;default:false" json:"used"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName returns the table name for HomeosSMSCode.
func (HomeosSMSCode) TableName() string { return setTableName("homeos_sms_codes") }

// HomeosRefreshToken is one issued refresh token, stored hashed (PRD 3.4.1「refresh 轮换」、
// 20章 密钥不落明文). Logout and rotation revoke rows here, which is the only way a revoked
// session can actually stop working.
type HomeosRefreshToken struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	UserID    string    `gorm:"column:user_id;type:uuid;not null;index:idx_homeos_refresh_tokens_user" json:"user_id"`
	TokenHash string    `gorm:"column:token_hash;type:varchar(255);not null" json:"-"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null" json:"expires_at"`
	Revoked   bool      `gorm:"column:revoked;not null;default:false" json:"revoked"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName returns the table name for HomeosRefreshToken.
func (HomeosRefreshToken) TableName() string { return setTableName("homeos_refresh_tokens") }

// HomeosDueRegistration is one 到期中心 registration (PRD 16.4 注册契约, migration 0008). It is
// written by the bus consumer, not by a handler; this row type exists so the revoke path
// (finance.due.revoked) and the B-zone read use one shape instead of two map literals.
type HomeosDueRegistration struct {
	ID           string         `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	FamilyID     string         `gorm:"column:family_id;type:uuid;not null" json:"family_id"`
	SourceSystem string         `gorm:"column:source_system;type:varchar(20);not null" json:"source_system"`
	SourceID     string         `gorm:"column:source_id;type:uuid;not null" json:"source_id"`
	Kind         string         `gorm:"column:kind;type:varchar(20);not null" json:"kind"`
	Title        string         `gorm:"column:title;type:varchar(200);not null" json:"title"`
	DueAt        time.Time      `gorm:"column:due_at;not null" json:"due_at"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"-"`
	DeletedBy    *string        `gorm:"column:deleted_by;type:uuid" json:"-"`
}

// TableName returns the table name for HomeosDueRegistration.
func (HomeosDueRegistration) TableName() string { return setTableName("homeos_due_registration") }

// HomeosAuditLog is one audit entry (PRD 21.5「审计事件九类，审计日志落 svc-homeos，字段带 code」,
// migration 0009). 21.5 names the nine classes but no column set -- that gap is reported; the
// fields here are the ones the doc's own consumers need: 按家庭查询 (3.7 audit-events 行),
// 「本人可读自己触发的条目」 and 「越权尝试 + 连续 5 次触发通知」 (15.5), which needs result.
type HomeosAuditLog struct {
	ID             string    `gorm:"column:id;type:uuid;primaryKey" json:"id"`
	FamilyID       *string   `gorm:"column:family_id;type:uuid" json:"family_id"`
	TargetFamilyID *string   `gorm:"column:target_family_id;type:uuid" json:"target_family_id"`
	Code           *string   `gorm:"column:code;type:varchar(20)" json:"code"`
	Event          string    `gorm:"column:event;type:varchar(40);not null" json:"event"`
	ActorMemberID  *string   `gorm:"column:actor_member_id;type:uuid" json:"actor_member_id"`
	ActorUserID    *string   `gorm:"column:actor_user_id;type:uuid" json:"actor_user_id"`
	Action         *string   `gorm:"column:action;type:varchar(30)" json:"action"`
	Entity         *string   `gorm:"column:entity;type:varchar(50)" json:"entity"`
	EntityID       *string   `gorm:"column:entity_id;type:uuid" json:"entity_id"`
	Result         string    `gorm:"column:result;type:varchar(10);not null;default:allowed" json:"result"`
	Reason         *string   `gorm:"column:reason;type:text" json:"reason"`
	IP             *string   `gorm:"column:ip;type:varchar(64)" json:"ip"`
	UserAgent      *string   `gorm:"column:user_agent;type:varchar(500)" json:"user_agent"`
	CreatedAt      time.Time `json:"created_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// TableName returns the table name for HomeosAuditLog.
func (HomeosAuditLog) TableName() string { return setTableName("homeos_audit_log") }

// Audit event classes, one per PRD 21.5 列举的九类。The identifiers are this card's naming;
// the nine categories themselves are the document's.
const (
	AuditEventLogin              = "login"
	AuditEventPermissionChange   = "permission_change"
	AuditEventCrossFamilyAttempt = "cross_family_attempt"
	AuditEventExport             = "export"
	AuditEventDelete             = "delete"
	AuditEventL3Read             = "l3_read"
	AuditEventRuleToggle         = "rule_toggle"
	AuditEventDeadLetterReplay   = "dead_letter_replay"
	AuditEventOnBehalfWrite      = "on_behalf_write"
)

// Audit results (PRD 15.5: 越权尝试全部落审计 -> a denied row).
const (
	AuditResultAllowed = "allowed"
	AuditResultDenied  = "denied"
)
