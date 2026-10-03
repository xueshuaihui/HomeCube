// due_revoked_handler.go is the reverse half of the 到期 chain, and the reason it exists is PRD 卷首
// 第 3 条: a time-bearing business object is registered into HomeOS' 日历/待办/提醒 -- and a
// registration that can never be undone leaves 已付账单 in 首页 B 区 and in 到期中心 for good.
//
// The event is contracts/events/finance.yaml:93-105 (FROZEN, v1.0.0):
//
//	event_type:  finance.due.revoked          「到期对象撤销（业务对象删除或完成）」
//	business_id: "{source_id}:{due_at}"       (:95)
//	payload:     source_system: finance / source_id: uuid / family_id: uuid /
//	             revoked_at: timestamp / reason: enum(completed|deleted|expired)  (:101-105)
//	consumers:   svc-homeos                   (:98-99)
//
// Its effect on this service is one column: homeos_0008:43-44 names finance.due.revoked as
// homeos_due_registration 唯一的删除路径 and the soft-delete semantics of the DDL are「打 deleted_at，
// 不删行」 -- 0008:52-54's unique index is PARTIAL (WHERE deleted_at IS NULL) precisely so a revoked
// row can stay in the 注册历史 while the same object registers a fresh live row.
//
// Why the anchor is (source_system, source_id, family_id, due_at) and not the registration triple: the
// frozen revoked payload declares no `kind`, and inventing one from the reason or the source would be
// guessing at a contract field. It does, however, put due_at in business_id (:95「"{source_id}:{due_at}"」)
// -- 与注册事件写下的同一段文本，所以「撤销哪一期」在事件里本来就有答案，不必借 kind 也能收到唯一一行。
// Kind is exactly what a two-column anchor could not do: 0008:52-54 make
// (source_system, source_id, kind) WHERE deleted_at IS NULL unique, so one source_id can hold several
// live registrations at once, one per kind, and the dev库 showed that happening (a bill and a budget
// sharing an id) -- a (source_system, source_id) UPDATE revoked every one of them, i.e. it deleted rows
// the event never named. That is data loss, not a difference to report.
//
// The two new columns are read from the event, never inferred from the table: family_id is the payload
// field the contract itself declares (:103), due_at is business_id's suffix after checking its source_id
// half against payload.source_id. Both are also what PRD 22.2 第 10 条 / 技术方案 §10.3「指标最小集
// （全部带 family_id 与 code）」 ask of every query in this service.
//
// A delivery that cannot supply them -- empty family_id, envelope and payload disagreeing, a business_id
// without the contract's colon, a business_id whose prefix is not the payload's source_id, an unparsable
// due_at -- is refused, which sends it to the dead letter (§3.4). There is deliberately no fallback to a
// wider WHERE: the wide anchor WAS the defect, and 宁可进死信也不误删 is the judgement this file exists to
// make.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// DueRevokedEventType is the event this handler subscribes to, named by the contract and carried on
// the subject of the same name (§3.1「subject 命名即事件名」). It sits next to
// bus_runtime.go's DueRegisteredEventType so the two ends of the 到期 chain are readable together.
const DueRevokedEventType = "finance.due.revoked"

// ErrNoRevokedPayload is the refusal a revoked delivery whose envelope carries no payload object gets,
// the same judgement due_registered_handler.go's ErrNoPayload records. It is returned rather than
// defaulted because an empty payload has no source_id, and 「撤销一切」 is not a fallback anyone wants.
var ErrNoRevokedPayload = errors.New("finance.due.revoked 信封缺少 payload")

// ErrNoRevokedFamilyID is the refusal a revoked payload without family_id gets. family_id is a declared
// payload field (finance.yaml:103), so a missing one is a producer bug, and the alternative -- dropping
// the family boundary -- is a cross-family UPDATE, which §2.2「索引第一列固定 family_id」/15.2 refuse outright.
var ErrNoRevokedFamilyID = errors.New("finance.due.revoked 缺 family_id")

// ErrRevokedFamilyMismatch is the refusal an envelope whose family_id and whose payload family_id
// disagree gets. Both are the publisher's own words, so exactly one of them is wrong and this consumer has
// no standing to pick: guessing would revoke a different household's registration.
var ErrRevokedFamilyMismatch = errors.New("finance.due.revoked 的信封 family_id 与 payload family_id 不一致")

// ErrRevokedBusinessIDShape is the refusal a business_id that is not contracts/events/finance.yaml:95's
// 「"{source_id}:{due_at}"」 gets: no colon (so no 期 is named), or a prefix that is not the payload's
// source_id (so the key and the body point at different objects). Both are refusals rather than「那就按
// 宽锚点来」 precisely because the wide anchor is the defect this file replaced.
var ErrRevokedBusinessIDShape = errors.New("finance.due.revoked 的 business_id 不符合 \"{source_id}:{due_at}\" 形态")

// ErrRevokedDueAtUnparsable is the refusal a business_id whose due_at half is not an RFC3339 instant
// gets -- the same judgement revoked_at's parse below records, on the撤销锚点's other half.
var ErrRevokedDueAtUnparsable = errors.New("finance.due.revoked 的 business_id 里 due_at 不是 RFC3339 时间戳")

// revokedReasonCompleted / Deleted / Expired are contracts/events/finance.yaml:105's
// reason: enum(completed|deleted|expired) -- the three values this handler accepts, and no others.
// The enum is checked rather than passed through because homeos_due_registration has no reason column
// (0008:25-47): an out-of-enum value would otherwise be soft-deleting rows for a reason the contract
// does not allow, with nothing on the table to show it.
var revokedReasons = map[string]bool{
	"completed": true,
	"deleted":   true,
	"expired":   true,
}

// DueRevokedPayload is the revoked event's payload_schema, field-for-field the contract's five keys.
// It is exported so svc-homeos' read side and the wiring test can name the same shape the wire carries.
type DueRevokedPayload struct {
	SourceSystem string `json:"source_system"`
	SourceID     string `json:"source_id"`
	FamilyID     string `json:"family_id"`
	RevokedAt    string `json:"revoked_at"`
	Reason       string `json:"reason"`
}

// DueRevokedHandler is the bus.Handler for finance.due.revoked: it turns one accepted delivery into one
// deleted_at stamp on the one live registration that delivery names (§3.6's B 区 source table). 「哪一条」
// 由 (source_system, source_id, family_id, due_at) 四列回答（头注），缺一列就是拒绝投递，不是放宽 WHERE。
//
// log receives what each accepted delivery did (the 撤销锚点 and the row count); nil means
// slog.Default(). It is a parameter rather than a package-level logger because SetupBus already owns the
// process logger (BusConfig.Logger, i.e. obs's) and the two ends of this chain must not write to
// different sinks.
//
// Idempotency (§3.4「至少一次投递」 means every delivery can arrive twice): the write is an UPDATE over
// the live rows of that object, so a redelivery matches nothing (deleted_at IS NULL no longer holds),
// answers 0 rows and returns nil. It never re-opens a revoked row, never stamps a second 撤销时刻, and
// never errors -- an error here would send a successfully-applied event to the dead-letter table.
func DueRevokedHandler(db *gorm.DB, log *slog.Logger) bus.Handler {
	if log == nil {
		log = slog.Default()
	}

	return func(ctx context.Context, msg bus.Message) error {
		if len(msg.Envelope.Payload) == 0 {
			return ErrNoRevokedPayload
		}

		// The payload arrives as Envelope.Payload (map[string]any) -- re-marshalled and decoded through
		// the contract's struct, the same two-step due_registered_handler.go uses so both handlers read
		// one shape and neither reaches into the map by hand.
		data, err := json.Marshal(msg.Envelope.Payload)
		if err != nil {
			return fmt.Errorf("序列化 %s 的 payload: %w", msg.Envelope.EventType, err)
		}

		var payload DueRevokedPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			return fmt.Errorf("反序列化 %s 的 payload: %w", DueRevokedEventType, err)
		}

		switch {
		case payload.SourceSystem == "":
			return fmt.Errorf("%s 缺 source_system：撤销锚点 (source_system, source_id, family_id, due_at) 不能为空", DueRevokedEventType)
		case payload.SourceID == "":
			return fmt.Errorf("%s 缺 source_id：不知道撤销哪个到期对象", DueRevokedEventType)
		case payload.FamilyID == "":
			return fmt.Errorf("%w：撤销锚点 (source_system, source_id, family_id, due_at) 不能少 family_id（finance.yaml:103 声明该字段）",
				ErrNoRevokedFamilyID)
		case !revokedReasons[payload.Reason]:
			return fmt.Errorf("%s 的 reason=%q 不在契约枚举 enum(completed|deleted|expired) 内",
				DueRevokedEventType, payload.Reason)
		}

		// 信封的 family_id 是底座路由与 outbox 行用的那一份，payload 的是契约声明的那一份。两者不符就是
		// 发布方自相矛盾（§10.3「指标最小集（全部带 family_id 与 code）」要求同一事件的两个 family_id 是同
		// 一个事实），消费方没有立场替它选一个：选错就是撤销别人家的注册。信封留空是允许的——那只是没在信封
		// 上重复这份事实，锚点仍按 payload 走。
		if msg.Envelope.FamilyID != "" && msg.Envelope.FamilyID != payload.FamilyID {
			return fmt.Errorf("%w：信封 %q / payload %q", ErrRevokedFamilyMismatch, msg.Envelope.FamilyID, payload.FamilyID)
		}

		// due_at 取自 business_id，而不是查表猜、也不是拿 revoked_at 顶替：契约把撤销事件的 business_id 定成
		// 「"{source_id}:{due_at}"」(finance.yaml:95)，与注册事件用的是同一段文本，所以「撤销哪一期」这件事
		// 在事件里本来就有答案。uuid 不含冒号，因此按第一个冒号切分就够；切出来的前半段必须等于
		// payload.source_id —— 不等就说明 key 与 body 指的是两个对象，那已经不是一条事件而是一处损坏。
		// 任何一种不符都是拒绝（死信），绝不退回到不带 due_at 的宽锚点：宽锚点正是本卡修的缺陷。
		businessSourceID, businessDueAt, found := strings.Cut(msg.Envelope.BusinessID, ":")
		if !found {
			return fmt.Errorf("%w：got %q，没有 due_at 段就不知道该撤销哪一期", ErrRevokedBusinessIDShape, msg.Envelope.BusinessID)
		}
		if businessSourceID != payload.SourceID {
			return fmt.Errorf("%w：business_id 前段 %q 与 payload.source_id %q 不是同一个对象",
				ErrRevokedBusinessIDShape, businessSourceID, payload.SourceID)
		}
		dueAt, err := time.Parse(time.RFC3339, businessDueAt)
		if err != nil {
			return fmt.Errorf("%w %q: %v", ErrRevokedDueAtUnparsable, businessDueAt, err)
		}

		// revoked_at is the contract's `timestamp` type and the value the DDL's deleted_at column gets:
		// the 撤销时刻 is the publisher's business fact (账单结清的那一刻), not this consumer's clock,
		// which would move on every redelivery. An unparsable stamp is a rejected delivery (死信), not a
		// row deleted at the epoch -- the same judgement HandleDueRegistered makes of due_at.
		revokedAt, err := time.Parse(time.RFC3339, payload.RevokedAt)
		if err != nil {
			return fmt.Errorf("failed to parse revoked_at timestamp: %w", err)
		}

		revoked, err := RevokeDueRegistration(ctx, db, payload.SourceSystem, payload.SourceID, payload.FamilyID, dueAt, revokedAt)
		if err != nil {
			// Back to bus.DurableConsumer.handleMessage, which writes the dead-letter row and Naks the
			// delivery (§3.4); this layer adds no retry of its own.
			return err
		}

		// 0 rows is not an error: the object was either already revoked (redelivery) or never registered
		// here at all (a due object this household removed before HomeOS ran). Both are the end state the
		// event asked for, so the delivery is acked -- §3.4's「重复投递不报错」. The row count goes on the
		// log line because it is the only in-process evidence distinguishing「撤销生效」from「事件被吞了」,
		// and due_at goes on it with it because 0 行现在多了一种新解释：那一期没有注册过。
		log.Info("due_revoked_applied",
			"event_type", DueRevokedEventType,
			"source_system", payload.SourceSystem,
			"source_id", payload.SourceID,
			"family_id", payload.FamilyID,
			"due_at", dueAt.UTC().Format(time.RFC3339),
			"reason", payload.Reason,
			"revoked_at", payload.RevokedAt,
			"rows_revoked", revoked,
		)
		return nil
	}
}

// RevokeDueRegistration stamps deleted_at on the live registration row of one 到期对象的其中一期 and
// returns how many rows it moved -- 1 when the anchor hit, 0 when that 期 was never registered here or is
// already revoked.
//
// The WHERE is the whole card: family_id and due_at are not refinements but the difference between「撤销
// 事件指的那一期」 and「这个 source_id 在这个服务里的所有注册」. homeos_due_registration's unique index is
// (source_system, source_id, kind) WHERE deleted_at IS NULL (0008:52-54), so several live rows can share
// one (source_system, source_id) -- one per kind -- and the two-column version of this statement revoked
// all of them, which is why it no longer exists here. due_at is compared as the instant the event named,
// and both ends render that text from the same bill row (svc-finance's registration producer and
// writeDueRevokedEvent both format bill.DueAt with time.RFC3339 in UTC), so the equality is the contract's
// own identity rather than a float comparison.
//
// 四列齐而没有 kind：kind 是注册事件的字段（finance.yaml:89），撤销事件不带它，所以锚点用 due_at 收到唯一
// 一行，而不是替发布方补一个 kind。
//
// updated_at gets now() because the row changed here; deleted_at keeps the event's own moment. deleted_by
// stays NULL -- homeos_0008:44-46 records that a non-null value is a 成员 id, and the actor of a
// cross-service revocation is the owning service, not a family member.
func RevokeDueRegistration(ctx context.Context, db *gorm.DB, sourceSystem, sourceID, familyID string, dueAt, revokedAt time.Time) (int64, error) {
	result := db.WithContext(ctx).
		Table("homeos_due_registration").
		Where("source_system = ? AND source_id = ? AND family_id = ? AND due_at = ? AND deleted_at IS NULL",
			sourceSystem, sourceID, familyID, dueAt).
		Updates(map[string]any{
			"deleted_at": revokedAt,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to revoke due registration of (%s, %s, family %s, due %s): %w",
			sourceSystem, sourceID, familyID, dueAt.UTC().Format(time.RFC3339), result.Error)
	}
	return result.RowsAffected, nil
}
