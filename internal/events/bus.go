// Package events publishes project-management domain events to iag.commercial
// on the IAG bus. Post-cutover this is the ONLY topic PM writes to —
// notifications subscribes to iag.commercial and decides which events
// translate to dispatch.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

const (
	SpecVersion = "1.0"
	Source      = "iag.project-management"

	TopicCommercial = "iag.commercial"

	// Domain event types emitted by PM on iag.commercial.
	//
	// pm.requisition.submitted carries workspaceOwnerUserId so downstream
	// services (procurement) can echo it back on approval/rejection, letting
	// PM find the originating workspace without a global requisition index.
	TypePMAlertRaised        = "pm.alert.raised"
	TypeRequisitionSubmitted = "pm.requisition.submitted"
	TypeTaskAssigned         = "pm.task.assigned"
	TypeMentionCreated       = "pm.mention.created"
)

// Bus publishes PM domain events to iag.commercial.
//
// Two delivery modes share the same envelope generation:
//
//   - Direct write: the legacy path. WriteMessages is invoked
//     synchronously inside the handler. Fast but loses events whenever
//     Kafka is unavailable.
//   - Outbox-backed: when an outbox.Store is attached via SetOutbox,
//     PublishCommercial / PublishPMAlert insert into pm_event_outbox
//     instead. A background outbox.Publisher pulls rows and calls
//     DispatchOutbox to do the actual Kafka write. Atomic with the
//     workspace document mutation when the caller passes the active
//     pgx.Tx via outbox.WithTx.
type Bus struct {
	commercialWriter *kafka.Writer
	enabled          bool
	store            outboxEnqueuer
}

// outboxEnqueuer is satisfied by *outbox.Store; declared as an interface
// to avoid an import cycle (outbox imports events for the dispatcher
// interface).
type outboxEnqueuer interface {
	Enqueue(ctx context.Context, eventType, key string, payload any) error
}

// Config for optional Kafka publishing.
type Config struct {
	Brokers []string
	Enabled bool
}

// NewFromEnv builds a bus from EVENT_BUS_ENABLED and KAFKA_BROKERS.
func NewFromEnv() *Bus {
	return New(Config{
		Brokers: ParseBrokers(os.Getenv("KAFKA_BROKERS")),
		Enabled: strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
	})
}

// New constructs a Bus. Disabled bus is a safe no-op.
func New(cfg Config) *Bus {
	if !cfg.Enabled || len(cfg.Brokers) == 0 {
		return &Bus{enabled: false}
	}
	transport := &kafka.Transport{ClientID: Source}
	return &Bus{
		enabled: true,
		commercialWriter: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicCommercial,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireAll,
			Transport:    transport,
		},
	}
}

// Close shuts down the underlying writer.
func (b *Bus) Close() error {
	if b == nil || !b.enabled {
		return nil
	}
	return b.commercialWriter.Close()
}

// Enabled reports whether Kafka publishing is active.
func (b *Bus) Enabled() bool { return b != nil && b.enabled }

// SetOutbox switches the bus into outbox-backed mode. Subsequent
// PublishCommercial / PublishPMAlert calls enqueue rows; the background
// publisher (outbox.Publisher) drives DispatchOutbox to send them to
// Kafka. Pass nil to fall back to the direct-write path.
func (b *Bus) SetOutbox(store outboxEnqueuer) {
	if b == nil {
		return
	}
	b.store = store
}

// UsesOutbox reports whether publishes are buffered through the outbox.
func (b *Bus) UsesOutbox() bool { return b != nil && b.store != nil }

// PlatformEvent is the canonical IAG envelope (mirrors @iag/events).
type PlatformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	Data          map[string]any `json:"data"`
}

// finalizeEvent stamps the missing CloudEvents fields on an event so
// the envelope is fully formed before either Kafka serialization or
// outbox persistence.
func finalizeEvent(evt PlatformEvent) PlatformEvent {
	if evt.ID == "" {
		evt.ID = uuid.NewString()
	}
	if evt.Time == "" {
		evt.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if evt.Source == "" {
		evt.Source = Source
	}
	if evt.SpecVersion == "" {
		evt.SpecVersion = SpecVersion
	}
	return evt
}

// eventKey returns the Kafka partition key for an event: the caller's
// explicit key falls back to the event ID.
func eventKey(explicit string, evt PlatformEvent) string {
	if explicit != "" {
		return explicit
	}
	return evt.ID
}

func (b *Bus) publish(ctx context.Context, evt PlatformEvent, key string) error {
	if !b.enabled || b.commercialWriter == nil {
		return nil
	}
	evt = finalizeEvent(evt)
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	return b.commercialWriter.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(eventKey(key, evt)),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

// DispatchOutbox writes a pre-finalized outbox row to Kafka. Called by
// outbox.Publisher when draining pending events. Returns an error so
// the publisher can apply its retry/backoff.
func (b *Bus) DispatchOutbox(ctx context.Context, eventType string, eventKey string, payload []byte) error {
	if !b.enabled || b.commercialWriter == nil {
		return nil
	}
	var evt PlatformEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}
	if evt.Type == "" {
		evt.Type = eventType
	}
	evt = finalizeEvent(evt)
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	key := eventKey
	if key == "" {
		key = evt.ID
	}
	return b.commercialWriter.WriteMessages(ctx, kafka.Message{
		Topic: TopicCommercial,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

// PublishCommercial emits a domain event on iag.commercial. With an
// outbox attached the event is durably enqueued; without one it falls
// back to the direct Kafka write.
func (b *Bus) PublishCommercial(ctx context.Context, eventType string, data map[string]any, key string) {
	if !b.enabled {
		return
	}
	evt := finalizeEvent(PlatformEvent{Type: eventType, Data: data})
	if b.store != nil {
		if err := b.store.Enqueue(ctx, eventType, eventKey(key, evt), evt); err != nil {
			slog.Warn("commercial event enqueue failed", "type", eventType, "err", err)
		}
		return
	}
	if err := b.publish(ctx, evt, key); err != nil {
		slog.Warn("commercial event publish failed", "type", eventType, "err", err)
	}
}

// PublishPMAlert emits pm.alert.raised on iag.commercial. The notifications
// service subscribes and converts these alerts into dispatch calls — PM no
// longer writes directly to iag.notifications.
func (b *Bus) PublishPMAlert(ctx context.Context, channel, recipient, templateID string, variables map[string]string) {
	if !b.enabled || recipient == "" || templateID == "" {
		return
	}
	vars := map[string]any{}
	for k, v := range variables {
		vars[k] = v
	}
	evt := finalizeEvent(PlatformEvent{
		Type: TypePMAlertRaised,
		Data: map[string]any{
			"channel":    channel,
			"recipient":  recipient,
			"templateId": templateID,
			"variables":  vars,
		},
	})
	if b.store != nil {
		if err := b.store.Enqueue(ctx, evt.Type, eventKey(recipient, evt), evt); err != nil {
			slog.Warn("pm.alert.raised enqueue failed", "recipient", recipient, "err", err)
		}
		return
	}
	if err := b.publish(ctx, evt, recipient); err != nil {
		slog.Warn("pm.alert.raised publish failed", "recipient", recipient, "err", err)
	}
}

// ParseBrokers splits a comma-separated KAFKA_BROKERS value.
func ParseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
