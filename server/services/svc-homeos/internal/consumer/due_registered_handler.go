// due_registered_handler.go is the seam between packages/bus's handler signature and this service's
// already-shipped processor.
//
// bus.Handler is `func(ctx, bus.Message) error` (packages/bus/interface.go:70) and bus.Message carries
// the parsed envelope only -- the jetstream.Msg the delivery came in on is consumed by
// DurableConsumer.handleMessage and never handed onward (consumer.go:123-164). HandleDueRegistered
// takes a jetstream.Msg, because §3.4's chain has it ack its own delivery. payloadMsg below is that
// adapter: the envelope's payload object re-marshalled into the flat form the contract declares
// (contracts/events/finance.yaml finance.due.registered → payload_schema: source_system, source_id,
// family_id, due_at, kind, title, members), and an ack that belongs to the durable consumer.
package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/bus"
)

// ErrNoPayload is the refusal a delivery whose envelope carries no payload object gets. It is returned
// rather than defaulting: an empty payload would reach HandleDueRegistered as zero values, fail on
// due_at, and be recorded as a bad event when the event was never the problem.
var ErrNoPayload = errors.New("finance.due.registered 信封缺少 payload")

// DueRegisteredHandler is the bus.Handler for finance.due.registered. Every message it accepts is
// written by HandleDueRegistered into homeos_due_registration, the B 区's source table (§3.6).
func DueRegisteredHandler(db *gorm.DB) bus.Handler {
	return func(ctx context.Context, msg bus.Message) error {
		if len(msg.Envelope.Payload) == 0 {
			return ErrNoPayload
		}

		data, err := json.Marshal(msg.Envelope.Payload)
		if err != nil {
			return fmt.Errorf("序列化 %s 的 payload: %w", msg.Envelope.EventType, err)
		}

		if err := HandleDueRegistered(ctx, &payloadMsg{
			data:    data,
			subject: msg.Subject,
			headers: toNatsHeader(msg.Headers),
		}, db); err != nil {
			// The error goes back to DurableConsumer.handleMessage, which writes the dead-letter row
			// and Naks the delivery (§3.4). This layer adds no retry of its own.
			return err
		}
		return nil
	}
}

// payloadMsg presents one envelope payload as the jetstream.Msg HandleDueRegistered reads. Only
// Data, Subject, Headers and Ack are on that function's path; the rest of the interface exists to
// make the type complete and answers with an error rather than pretending to act.
type payloadMsg struct {
	data    []byte
	subject string
	headers nats.Header
}

// Data is the flat event body (§3.1「版本不进 subject 而进信封」 means the envelope wrapper is not
// part of the payload, so the handler unmarshals exactly this object).
func (m *payloadMsg) Data() []byte { return m.data }

// Subject is the delivery's subject, i.e. the event name (§3.1「subject 命名即事件名」).
func (m *payloadMsg) Subject() string { return m.subject }

func (m *payloadMsg) Headers() nats.Header { return m.headers }

// Ack is a no-op that reports success. The ack is not this message's to give: DurableConsumer
// .handleMessage calls Ack on the real delivery only after the handler returned nil, and Nak when it
// returned an error (§3.4's retry decision lives with the consumer, not with the payload view).
func (m *payloadMsg) Ack() error { return nil }

func (m *payloadMsg) Reply() string { return "" }

func (m *payloadMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return nil, errNotOwnable("Metadata")
}

func (m *payloadMsg) DoubleAck(context.Context) error { return errNotOwnable("DoubleAck") }

func (m *payloadMsg) Nak() error { return errNotOwnable("Nak") }

func (m *payloadMsg) NakWithDelay(time.Duration) error { return errNotOwnable("NakWithDelay") }

func (m *payloadMsg) InProgress() error { return errNotOwnable("InProgress") }

func (m *payloadMsg) Term() error { return errNotOwnable("Term") }

func (m *payloadMsg) TermWithReason(string) error { return errNotOwnable("TermWithReason") }

// errNotOwnable names the layer that owns redelivery control, so a call that should not happen says
// why it failed instead of failing silently.
func errNotOwnable(method string) error {
	return fmt.Errorf("payloadMsg.%s: ack 语义归 bus.DurableConsumer 所有，payload 视图不重投递也不终止投递", method)
}

// toNatsHeader widens bus.Message's map[string]string back to nats.Header.
func toNatsHeader(headers map[string]string) nats.Header {
	out := make(nats.Header, len(headers))
	for k, v := range headers {
		out.Set(k, v)
	}
	return out
}
