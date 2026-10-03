// bus_runtime.go is svc-homeos's event-side wiring: the outbox 投递器 for the events this service
// publishes, and the durable consumers for the two events it subscribes to in P1 (§3.6
// finance.due.registered + contracts/events/finance.yaml:93 finance.due.revoked). It exists because
// PRD 3.4.6 judges the whole chain (发布 / 订阅 / 幂等 / 重试 / 死信) as one deliverable, and the chain
// has two ends: before this file nothing in
// svc-homeos ever moved a homeos_outbox row into JetStream, and nothing had registered a consumer at
// all -- the 「今日到期」 cell could therefore only ever be empty (§3.6「svc-homeos: 消费并按
// (source_system, source_id, kind) upsert homeos_due_registration」).
//
// Both 到期 events, not just the first: PRD 卷首第 3 条's registration has a reverse半边, and
// homeos_0008:43-44 names finance.due.revoked as homeos_due_registration 唯一的删除路径. A wiring that
// subscribes to registered only leaves every 已付账单's 到期行 in 首页 B 区 for good -- data a user can
// see, which is why the second consumer belongs in the same startup gate as the first.
//
// Every step here is fail-fast and returns an error: §10.1's startup gate plus the card's rule that a
// service must not come up 带病. 「broker 不可用就跳过订阅」 is exactly the degradation that would put
// the empty B 区 back, and it would be a silent one.
package consumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
	"github.com/xueshuaihui/HomeCube/server/packages/obs"
	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// DueRegisteredEventType is the event this service consumes, named by contracts/events/finance.yaml
// (「到期对象注册到HomeOS日历/提醒中心」, publisher svc-finance, consumers svc-homeos) and carried on the
// subject of the same name (§3.1「subject 命名即事件名」).
const DueRegisteredEventType = "finance.due.registered"

// BusConfig names the two registry rows and the two endpoints this wiring needs. Stream and subject
// names are taken from registry.Domain (§1.3「一张域表是唯一真源」), never spelled out here: HC_HOMEOS /
// homeos.> and HC_FINANCE / finance.> are §3.1 流表's two P1 rows, and registry_test.go:542 pins that
// StreamName()/StreamSubjectPattern() produce exactly those.
type BusConfig struct {
	// Logger receives the deliverer's alerts (§10.3「outbox 积压」 is an operational fact, not a log line
	// to drop on the floor).
	Logger *slog.Logger
	// Own is this service's domain row (homeos): its stream is created, its tables receive the writes.
	Own registry.Domain
	// Source is the domain whose events this service consumes (finance). P1 has one such row; §3.4
	// 「去重是消费方自己的状态，不能共享别人的表」 is why the consumer's Code stays Own.Code.
	Source registry.Domain
	// DSN is the validated §2.1 DSN this process already uses (cfg.DSN), so the bus side gets the same
	// schema boundary as the request side.
	DSN string
	// NATSURL is §3.1's endpoint (cfg.NATSURL, from NATS_URL).
	NATSURL string
}

// BusRuntime owns the handles the wiring opened, in the order they must come down: consumer, then
// deliverer, then the JetStream wrapper, then the database handle.
//
// The two consumers are two fields, not one: bus.DurableConsumer.Stop() closes its own stopCh, so a
// slice that is stopped twice -- or a single field overwritten by the second Start -- either panics or
// leaves the first subscription pumping after Stop().
type BusRuntime struct {
	log             *slog.Logger
	pump            *pump
	deliverer       *bus.OutboxDeliverer
	consumer        *bus.DurableConsumer // finance.due.registered
	revokedConsumer *bus.DurableConsumer // finance.due.revoked
	db              *gorm.DB
}

// SetupBus builds the event side of PRD 3.4.6 and starts it. Any refusal aborts the process: an
// outbox that is never delivered or a subscription that never exists both mean 「什么都不发生」.
func SetupBus(ctx context.Context, cfg BusConfig) (*BusRuntime, error) {
	switch {
	case cfg.Own.Code == "":
		return nil, errors.New("BusConfig.Own: registry row 未传入（HC_{CODE} 流名与本域表名都从它派生）")
	case cfg.Source.Code == "":
		return nil, errors.New("BusConfig.Source: registry row 未传入（来源流名与 filter subject 从它派生）")
	case cfg.Own.Code == cfg.Source.Code:
		return nil, fmt.Errorf("BusConfig.Source 与 Own 同为 %q：消费自己域的流不是本接线的形状", cfg.Own.Code)
	}

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	inner, err := bus.NewJetStreamWrapper(bus.JetStreamConfig{
		URL:      cfg.NATSURL,
		Code:     cfg.Own.Code,
		MaxAge:   streamMaxAge,
		Replicas: streamReplicas,
	})
	if err != nil {
		return nil, err
	}

	rt := &BusRuntime{log: log}
	fail := func(step string, err error) (*BusRuntime, error) {
		rt.Stop()
		return nil, fmt.Errorf("%s: %w", step, err)
	}

	// Two connections to the same endpoint, on purpose: packages/bus's wrapper is the publishing half
	// obs.Open's health check and §3.3's deliverer use, and the consuming half needs a JetStream
	// context the底座 does not expose (jetstream.go's Subscribe discards the handler). The client
	// identity stays the registry's service name, the one §10.1 末条 registers.
	rt.pump, err = newPump(inner, cfg.NATSURL, cfg.Own.ServiceName)
	if err != nil {
		inner.Close()
		return nil, err
	}

	// Own stream: this process owns HC_HOMEOS, so it re-declares §3.1's row (§3.1 marks it 「建」).
	if err := rt.pump.CreateStream(ctx, cfg.Own.StreamName(), []string{cfg.Own.StreamSubjectPattern()}); err != nil {
		return fail("确保本域流存在（"+cfg.Own.StreamName()+"）", err)
	}
	// Source stream: created only when absent -- see ensureSourceStream.
	if err := rt.pump.ensureSourceStream(ctx, cfg.Source.StreamName(), []string{cfg.Source.StreamSubjectPattern()}); err != nil {
		return fail("确保来源流存在（"+cfg.Source.StreamName()+"）", err)
	}

	// The bus side's own handle: it writes {code}_outbox / {code}_event_dedupe / {code}_dead_letter and
	// homeos_due_registration, none of which the request side's handle is needed for.
	rt.db, err = openBusDB(cfg.DSN, log)
	if err != nil {
		return fail("连接本域数据库（总线侧）", err)
	}

	// 投递器: §3.3「每 500ms 批量 100 行 → JetStream Publish → 成功置 sent，失败 attempts+1」, with
	// §3.3's crash recovery（「重启即扫 pending」) inside Start.
	rt.deliverer = bus.NewOutboxDeliverer(rt.db, inner, bus.OutboxConfig{Code: cfg.Own.Code}, func(msg string) {
		log.Error("outbox 投递告警", "code", cfg.Own.Code, "detail", msg)
	})
	rt.deliverer.Start(ctx)

	// 消费者: one durable consumer per subscribed event, handler落到 the event's own adapter.
	//
	// Two of them, because a durable consumer has ONE FilterSubject (bus.ConsumerConfig:27, and
	// jetstream_pump.go passes it straight into ConsumerConfig.FilterSubject) while the 到期 chain is two
	// events: contracts/events/finance.yaml's finance.due.registered (:77) registers an object into
	// homeos_due_registration and finance.due.revoked (:93) withdraws it. Subscribing only to the first
	// is what left 已付账单 in 首页 B 区 permanently -- the table had a writer and no deletion path, and
	// homeos_0008:43-44 names finance.due.revoked as that table's 唯一的删除路径.
	//
	// The three retry knobs go in explicitly rather than through NewDurableConsumer's defaults: the
	// numbers §3.4 pins reach the server from pump.Subscribe (jetstream_pump.go's reason 3), and the
	//底座 keeps its own copy -- passing the same values here keeps the two from diverging silently.
	for _, sub := range []struct {
		eventType string
		handler   bus.Handler
		assign    func(*BusRuntime, *bus.DurableConsumer)
	}{
		{DueRegisteredEventType, DueRegisteredHandler(rt.db), func(r *BusRuntime, c *bus.DurableConsumer) {
			r.consumer = c
		}},
		{DueRevokedEventType, DueRevokedHandler(rt.db, log), func(r *BusRuntime, c *bus.DurableConsumer) {
			r.revokedConsumer = c
		}},
	} {
		// Assigned before Start so the fail-fast path below still brings the already-running consumer
		// down: rt.Stop() stops whatever the runtime holds, and every consumer it never reached is nil.
		cons := bus.NewDurableConsumer(rt.db, rt.pump, bus.ConsumerConfig{
			Code:          cfg.Own.Code,
			StreamName:    cfg.Source.StreamName(),
			EventType:     sub.eventType,
			FilterSubject: sub.eventType,
			AckWait:       consumerAckWait,
			MaxDeliver:    consumerMaxDeliver,
			BackOff:       consumerBackOff,
		})
		sub.assign(rt, cons)

		if err := cons.Start(ctx, sub.handler); err != nil {
			return fail("注册 durable consumer "+cfg.Own.Code+"-"+sub.eventType, err)
		}
	}

	log.Info("event_side_started",
		"own_stream", cfg.Own.StreamName(),
		"source_stream", cfg.Source.StreamName(),
		"filter_subjects", strings.Join([]string{DueRegisteredEventType, DueRevokedEventType}, ","),
		"consumers", strings.Join([]string{
			durableName(cfg.Own.Code + "-" + DueRegisteredEventType),
			durableName(cfg.Own.Code + "-" + DueRevokedEventType),
		}, ","),
	)

	return rt, nil
}

// Stop brings the event side down in the order §3.4's chain can tolerate: no new deliveries, then no
// new publishes, then the connections. Safe to call on a partially built runtime (the fail-fast
// paths do).
func (r *BusRuntime) Stop() {
	if r == nil {
		return
	}
	if r.consumer != nil {
		r.consumer.Stop()
	}
	if r.revokedConsumer != nil {
		r.revokedConsumer.Stop()
	}
	if r.deliverer != nil {
		r.deliverer.Stop()
	}
	if r.pump != nil {
		r.pump.Close()
	}
	if r.db != nil {
		if sqlDB, err := r.db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// openBusDB opens the bus side's handle with the SAME dialector shape obs.Open uses for the request
// side (packages/obs/serve.go:42 `postgres.Open(cfg.DSN)`), because that is the only shape the shipped
// 底座 writes measure against.
//
// The predecessor of this function carried a `postgres.Config{WithoutReturning: true}` plus a
// ConnPool that answered LastInsertId with (0,nil), for two reasons FB1's packages/bus fix recorded.
// Both were measured on the dev库 against the real homeos_* tables, and both are gone or inverted:
//
//   - 「DedupeRecord 的自增 id 会让去重写入红在 RETURNING "id"（42703）」: true while the field
//     existed. bus.DedupeRecord is now the 0002 column set (event_type, business_id, created_at), it
//     has no primary-key field, so gorm emits no RETURNING for it and the insert goes through --
//     measured: first delivery rows=1, redelivery rows=0, which is exactly §3.4's「已存在即 ack」.
//   - 「map 插入在 pgx 上取不到 LastInsertId，所以要一个去势池」: the other way round. pgx v5's
//     stdlib returns driver.RowsAffected, whose LastInsertId answers
//     「LastInsertId is not supported by this driver」, and gorm only records that answer as an error
//     when the dialector does NOT support RETURNING (gorm@v1.31.2 callbacks/create.go:141-150,
//     `if !supportReturning { db.AddError(err) }`). WithoutReturning is what sets supportReturning to
//     false (driver/postgres@v1.6.3/postgres.go:84), so the workaround pair was itself the defect:
//     under it the map-based upsert into homeos_due_registration and the DeadLetterRecord insert both
//     failed with that message (measured, cases B and C), while under the plain dialector all four
//     writes -- dedupe, dedupe redelivery, dead letter, due registration insert + reschedule --
//     succeed (measured, case A).
//
// BUS-2c re-measured both shapes on the dev PG16 in a throwaway database carrying the shipped DDL
// (homeos_0002 + homeos_0008, i.e. the prefixed homeos.homeos_* relations), and added the two shapes
// the predecessor had not tried: a map insert that supplies no primary key -- the only case in which
// gorm can fall back to driver LastInsertId at all -- and a map insert + ON CONFLICT DO NOTHING.
// Under postgres.Open both answer err=nil (rows 1, then rows 0 on the redelivery, which is §3.4's
// 「已存在即 ack」), and under WithoutReturning both answer 「LastInsertId is not supported by this
// driver」, exactly like the due-registration and dead-letter writes above. So the LastInsertId
// failure mode is a property of WithoutReturning, never of the driver: a neutered ConnPool would be
// a workaround for a problem the shipped code does not have, and conn_pool.go stays deleted. The
// probe that produced this measurement is deleted too -- it addressed the dev库 and is not a
// deliverable test.
//
// The search_path boundary is unchanged: the same §2.1 DSN obs validated, so §1.3 row 2's
// 「连接串 search_path={code}」 still holds and this handle cannot reach another schema.
func openBusDB(dsn string, log *slog.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: obs.NewGormLogger(log),
	})
	if err != nil {
		return nil, fmt.Errorf("连接本域数据库（schema 由 DSN 的 search_path 决定）: %w", err)
	}
	return db, nil
}
