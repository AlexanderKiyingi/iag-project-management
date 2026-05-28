package consumer

import (
	"context"
	"fmt"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CommercialTopic is the IAG bus topic PM subscribes to.
const CommercialTopic = "iag.commercial"

// ProcessedEventsTable is the dedupe table name (created by migration 0004).
const ProcessedEventsTable = "pm_processed_events"

// Options bundles everything needed to construct the PM consumer.
type Options struct {
	Brokers  []string
	GroupID  string
	DLQTopic string
	Pool     *pgxpool.Pool
	Handler  *Handler
}

// New builds a platformevents.Consumer wired to PM's handler, with Postgres
// dedupe and (if DLQTopic + brokers are set) a DLQ producer. Returns the
// consumer and a close func that releases the DLQ producer.
func New(opts Options) (*platformevents.Consumer, func(), error) {
	if opts.Handler == nil {
		return nil, func() {}, fmt.Errorf("consumer: Handler is required")
	}
	if opts.Pool == nil {
		return nil, func() {}, fmt.Errorf("consumer: Pool is required")
	}
	if len(opts.Brokers) == 0 {
		return nil, func() {}, fmt.Errorf("consumer: at least one broker required")
	}
	if opts.GroupID == "" {
		return nil, func() {}, fmt.Errorf("consumer: GroupID is required")
	}

	var dlq *platformevents.Producer
	closeFn := func() {}
	if opts.DLQTopic != "" {
		dlq = platformevents.NewProducer(platformevents.ProducerConfig{
			Brokers:  opts.Brokers,
			ClientID: "iag.project-management.dlq",
		})
		closeFn = func() { _ = dlq.Close() }
	}

	cfg := platformevents.ConsumerConfig{
		Brokers:     opts.Brokers,
		Topic:       CommercialTopic,
		GroupID:     opts.GroupID,
		Handler:     opts.Handler,
		Dedupe:      platformevents.PostgresDedupe(opts.Pool, ProcessedEventsTable),
		DLQProducer: dlq,
		DLQTopic:    opts.DLQTopic,
	}
	c, err := platformevents.NewConsumer(cfg)
	if err != nil {
		closeFn()
		return nil, func() {}, err
	}
	return c, closeFn, nil
}

// Run blocks consuming until ctx is cancelled. Thin wrapper for callers that
// don't want to import platformevents directly.
func Run(ctx context.Context, c *platformevents.Consumer) error {
	if c == nil {
		return nil
	}
	return c.Run(ctx)
}
