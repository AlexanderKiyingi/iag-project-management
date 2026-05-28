// Package events publishes project-management domain events to iag.commercial
// on the IAG bus. Post-cutover this is the ONLY topic PM writes to —
// notifications subscribes to iag.commercial and decides which events
// translate to dispatch.
package events

import (
	"context"
	"encoding/json"
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
type Bus struct {
	commercialWriter *kafka.Writer
	enabled          bool
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

func (b *Bus) publish(ctx context.Context, evt PlatformEvent, key string) error {
	if !b.enabled || b.commercialWriter == nil {
		return nil
	}
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
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
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

// PublishCommercial emits a domain event on iag.commercial.
func (b *Bus) PublishCommercial(ctx context.Context, eventType string, data map[string]any, key string) {
	if !b.enabled {
		return
	}
	evt := PlatformEvent{Type: eventType, Data: data}
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
	evt := PlatformEvent{
		Type: TypePMAlertRaised,
		Data: map[string]any{
			"channel":    channel,
			"recipient":  recipient,
			"templateId": templateID,
			"variables":  vars,
		},
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
