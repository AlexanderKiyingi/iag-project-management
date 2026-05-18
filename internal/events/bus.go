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

	TopicCommercial     = "iag.commercial"
	TopicNotifications  = "iag.notifications"

	TypeNotificationRequested  = "notification.requested"
	TypeRequisitionSubmitted   = "pm.requisition.submitted"
	TypeTaskAssigned           = "pm.task.assigned"
	TypeMentionCreated         = "pm.mention.created"
)

type Bus struct {
	commercialWriter *kafka.Writer
	notifWriter      *kafka.Writer
	enabled          bool
}

type Config struct {
	Brokers []string
	Enabled bool
}

func NewFromEnv() *Bus {
	return New(Config{
		Brokers: ParseBrokers(os.Getenv("KAFKA_BROKERS")),
		Enabled: strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
	})
}

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
			RequiredAcks: kafka.RequireOne,
			Transport:    transport,
		},
		notifWriter: &kafka.Writer{
			Addr:         kafka.TCP(cfg.Brokers...),
			Topic:        TopicNotifications,
			Balancer:     &kafka.LeastBytes{},
			RequiredAcks: kafka.RequireOne,
			Transport:    transport,
		},
	}
}

func (b *Bus) Close() error {
	if b == nil || !b.enabled {
		return nil
	}
	var err error
	if e := b.commercialWriter.Close(); e != nil {
		err = e
	}
	if e := b.notifWriter.Close(); e != nil && err == nil {
		err = e
	}
	return err
}

func (b *Bus) Enabled() bool {
	return b != nil && b.enabled
}

type PlatformEvent struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	Time          string         `json:"time"`
	Source        string         `json:"source"`
	SpecVersion   string         `json:"specversion"`
	CorrelationID string         `json:"correlationId,omitempty"`
	Data          map[string]any `json:"data"`
}

func (b *Bus) publish(ctx context.Context, writer *kafka.Writer, evt PlatformEvent, key string) error {
	if !b.enabled || writer == nil {
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
	return writer.WriteMessages(ctx, kafka.Message{
		Topic: writer.Topic,
		Key:   []byte(key),
		Value: body,
		Headers: []kafka.Header{
			{Key: "ce-type", Value: []byte(evt.Type)},
			{Key: "ce-source", Value: []byte(evt.Source)},
		},
	})
}

func (b *Bus) PublishCommercial(ctx context.Context, eventType string, data map[string]any, key string) {
	if !b.enabled {
		return
	}
	evt := PlatformEvent{Type: eventType, Data: data}
	if err := b.publish(ctx, b.commercialWriter, evt, key); err != nil {
		slog.Warn("commercial event publish failed", "type", eventType, "err", err)
	}
}

func (b *Bus) PublishNotificationRequested(ctx context.Context, channel, recipient, templateID string, variables map[string]string) {
	if !b.enabled || recipient == "" || templateID == "" {
		return
	}
	vars := map[string]any{}
	for k, v := range variables {
		vars[k] = v
	}
	evt := PlatformEvent{
		Type: TypeNotificationRequested,
		Data: map[string]any{
			"channel":    channel,
			"recipient":  recipient,
			"templateId": templateID,
			"variables":  vars,
		},
	}
	if err := b.publish(ctx, b.notifWriter, evt, recipient); err != nil {
		slog.Warn("notification.requested publish failed", "recipient", recipient, "err", err)
	}
}

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
