// jetstream_pump.go is the half of §3.4's consumption chain that packages/bus does not do itself.
//
// Why this file exists (all three points are verified against the shipped底座, not assumed):
//
//  1. bus.JetStreamWrapper's real implementation, packages/bus/jetstream.go:102 Subscribe, creates
//     the durable consumer and then returns -- its own comment says 「Actual message consumption
//     would be handled by the consumer framework in consumer.go」, but consumer.go's handleMessage is
//     only reachable from the callback handed to Subscribe. With the底座 wrapper nothing ever calls
//     that callback, so a DurableConsumer built on it registers a consumer that receives nothing.
//     packages/ is frozen for this card, so the missing pump lives here: pump.Subscribe does the same
//     CreateOrUpdateConsumer and then Consume, handing底座's callback the messages it never gets.
//     This is the shape packages/authz/cache.go:181-224 already uses for the same reason.
//  2. bus.DurableConsumer.Start:104 builds the durable name as `{Code}-{EventType}`, i.e.
//     "homeos-finance.due.registered". JetStream rejects that: the client validates consumer names
//     against the set that excludes '.', '>', '*', '=' and ' ' (probe against the dev nats-server:
//     `nats: invalid consumer name: "homeos-finance.due.registered"`). §3.4's own example
//     (`homeos-finance-transaction-created`) is the dashed form, so durableName below normalises the
//     base's name to the documented shape instead of inventing a third one.
//  3. bus.ConsumerConfig's AckWait / MaxDeliver / BackOff are defaulted by NewDurableConsumer
//     (consumer.go:81-89) and never passed to js.Subscribe, so the numbers §3.4 pins can only reach
//     the server from the side that creates the consumer -- here. The constants below are §3.4's, and
//     they are the same numbers the底座's own Subscribe hardcodes (jetstream.go:106-108).
package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// §3.1 流表 for the two P1 rows: max_age=90d, R=1 (「单节点、R=1，不做集群」).
const (
	streamMaxAge   = 90 * 24 * time.Hour
	streamReplicas = 1
)

// §3.4's consumer knobs: ack_wait=30s, backoff=[1s,10s,60s], max_deliver=4（对应 10.4 的至多重试 3 次）.
var (
	consumerAckWait    = 30 * time.Second
	consumerMaxDeliver = 4
	consumerBackOff    = []time.Duration{1 * time.Second, 10 * time.Second, 60 * time.Second}
)

// Due-trigger scanner configuration: how often to scan for upcoming dues and the time window
// around now() within which items are considered "due". These defaults match the PRD intent
// of 「到点下发」 without overwhelming the database or producing too many events at once.
const (
	scannerScanInterval     = 1 * time.Minute  // scan every minute
	scannerReminderLeadTime = 5 * time.Minute  // fire reminders for items due within ±5 minutes
)

// pump decorates a bus.JetStreamWrapper. CreateStream and Publish stay the底座's (so the outbox
// deliverer keeps talking to packages/bus), while Subscribe -- the one method the底座's real
// implementation leaves unimplemented -- gets a working pump with its own connection.
type pump struct {
	bus.JetStreamWrapper

	conn   *nats.Conn
	js     jetstream.JetStream
	owns   string
	mu     sync.Mutex
	pumps  []jetstream.ConsumeContext
	closed bool
}

// newPump connects the consuming side and decorates inner. A failed connect is returned as an error:
// §10.1's startup gate means the process must not come up 带病.
func newPump(inner bus.JetStreamWrapper, natsURL, clientName string) (*pump, error) {
	conn, err := nats.Connect(natsURL,
		nats.Name(clientName),
		// Reconnect forever, same rule obs.Open applies to the publishing connection: a NATS restart
		// must not take the process down (§10.1, PRD 14.5 第 1 项).
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("连接 JetStream 消费侧（%s）: %w", natsURL, err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("建立 JetStream 上下文: %w", err)
	}

	return &pump{JetStreamWrapper: inner, conn: conn, js: js, owns: clientName}, nil
}

// Subscribe creates the durable consumer AND pumps its messages into handler. handler is the closure
// bus.DurableConsumer.Start passed in, i.e. handleMessage, which owns dedupe / ack / nak / dead letter.
func (p *pump) Subscribe(ctx context.Context, streamName, consumerName, filterSubject string, handler jetstream.MessageHandler) error {
	durable := durableName(consumerName)

	stream, err := p.js.Stream(ctx, streamName)
	if err != nil {
		return fmt.Errorf("打开流 %s（consumer %s）: %w", streamName, durable, err)
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       durable,
		FilterSubject: filterSubject,
		// Explicit ack is what makes handleMessage's Ack/Nak the redelivery control (§3.4).
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    consumerAckWait,
		MaxDeliver: consumerMaxDeliver,
		// MEASURED, not assumed (dev nats-server v2.10.29, probe against HC_FINANCE): once BackOff is
		// set, the server uses backoff[0] as the ack wait and the requested ack_wait is ignored -- a
		// brand-new durable asking for ack_wait=30s + backoff=[1s,10s,60s] comes back with
		// ack_wait=1s, while the identical request WITHOUT backoff comes back with ack_wait=30s. The
		// same is true of an update: CreateOrUpdateConsumer on the existing durable returns no error and
		// does not move ack_wait. §3.4:208 pins both numbers, so on this broker the two cannot both
		// hold; the values below stay §3.4's verbatim and the conflict is escalated rather than
		// resolved by quietly editing one of them.
		BackOff: consumerBackOff,
	})
	if err != nil {
		return fmt.Errorf("创建 durable consumer %s（流 %s、filter %s）: %w", durable, streamName, filterSubject, err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) { handler(msg) })
	if err != nil {
		return fmt.Errorf("启动 durable consumer %s 的投递循环: %w", durable, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		// Stop already ran (a fail-fast path mid-startup): do not leak the pump.
		cc.Stop()
		return fmt.Errorf("consumer %s: 总线已关闭", durable)
	}
	p.pumps = append(p.pumps, cc)
	return nil
}

// ensureSourceStream creates a stream this process only CONSUMES from, and only when it is absent
// (§3.1 marks both P1 rows 「建」, and deploy/docker-compose.yml:74 hands stream creation to the S2
// bus cards rather than to the carrier). An existing stream is left untouched on purpose: the
// publisher owns its config, and CreateOrUpdateStream replaces the whole subject list, so creating
// a neighbour's stream that already exists could silently drop that neighbour's subjects.
func (p *pump) ensureSourceStream(ctx context.Context, name string, subjects []string) error {
	_, err := p.js.Stream(ctx, name)
	if err == nil {
		return nil
	}
	if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("探测来源流 %s: %w", name, err)
	}
	return p.CreateStream(ctx, name, subjects)
}

// Close stops every pump, then closes both connections (this side's own, then the底座's).
func (p *pump) Close() {
	p.mu.Lock()
	pumps := p.pumps
	p.pumps = nil
	p.closed = true
	p.mu.Unlock()

	for _, cc := range pumps {
		cc.Stop()
	}

	if p.conn != nil {
		_ = p.conn.Drain()
		p.conn.Close()
	}
	if p.JetStreamWrapper != nil {
		p.JetStreamWrapper.Close()
	}
}

// durableName turns the name bus.DurableConsumer.Start builds ({code}-{event_type}, therefore full of
// dots) into a name JetStream accepts, matching §3.4's dashed example. JetStream's own rule is
// checked by the client before the request goes out: whitespace, '.', '>', '*', '=' are rejected.
func durableName(name string) string {
	translated := strings.NewReplacer(".", "-", ">", "-", "*", "-", "=", "-", " ", "-").Replace(name)
	// A name that already had no invalid character comes back byte-identical; a trailing dash would
	// only ever come from an event type ending in one, which no registered event does -- kept as the
	// minimal, reversible normalisation rather than a full slugifier.
	return translated
}
