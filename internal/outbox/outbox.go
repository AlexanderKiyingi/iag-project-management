// Package outbox implements the transactional outbox pattern for PM.
//
// Domain handlers used to publish to Kafka directly inside their HTTP
// handlers. That window — DB committed, Kafka write not yet acknowledged
// — was an event-loss surface every time Kafka, the network, or the
// service itself blipped. With the outbox the handler instead inserts a
// row into pm_event_outbox in the same transaction as the document
// update, and a background Publisher drains the table to Kafka with
// retry/backoff. Worst case is duplicate delivery (idempotent
// consumers handle that already via processed_events dedupe).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is a pending or completed outbox entry.
type Row struct {
	ID          int64
	EventType   string
	EventKey    string
	Payload     json.RawMessage
	CreatedAt   time.Time
	AvailableAt time.Time
	Attempts    int
	LastError   string
}

// Store wraps the pm_event_outbox table.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Enqueue writes a pending row. If a *pgx.Tx is in the context (the
// handler is mutating the workspace document), the insert participates
// in that transaction — so commit/rollback covers both writes
// atomically.
func (s *Store) Enqueue(ctx context.Context, eventType, key string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	exec := txExecOr(ctx, s.pool)
	_, err = exec.Exec(ctx, `
		INSERT INTO pm_event_outbox (event_type, event_key, payload)
		VALUES ($1, $2, $3::jsonb)
	`, eventType, nullable(key), body)
	return err
}

// ClaimBatch reserves up to `limit` due rows for processing by atomically
// bumping their attempts count and pushing available_at out by the
// retry backoff so concurrent publishers don't double-deliver.
func (s *Store) ClaimBatch(ctx context.Context, limit int, backoff time.Duration) ([]Row, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM pm_event_outbox
			WHERE dispatched_at IS NULL AND available_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE pm_event_outbox o
		SET attempts = o.attempts + 1,
		    available_at = NOW() + $2::interval
		FROM due
		WHERE o.id = due.id
		RETURNING o.id, o.event_type, o.event_key, o.payload, o.created_at,
		          o.available_at, o.attempts, COALESCE(o.last_error, '')
	`, limit, fmt.Sprintf("%d milliseconds", backoff.Milliseconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var key *string
		if err := rows.Scan(&r.ID, &r.EventType, &key, &r.Payload, &r.CreatedAt,
			&r.AvailableAt, &r.Attempts, &r.LastError); err != nil {
			return nil, err
		}
		if key != nil {
			r.EventKey = *key
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkDispatched records the successful delivery.
func (s *Store) MarkDispatched(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pm_event_outbox
		SET dispatched_at = NOW(), last_error = NULL
		WHERE id = $1
	`, id)
	return err
}

// MarkFailed records the failure and pushes the next retry out by the
// caller's chosen backoff window.
func (s *Store) MarkFailed(ctx context.Context, id int64, attempts int, errMsg string, retryDelay time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE pm_event_outbox
		SET last_error = $1, available_at = NOW() + $2::interval
		WHERE id = $3
	`, errMsg, fmt.Sprintf("%d milliseconds", retryDelay.Milliseconds()), id)
	return err
}

// Dispatcher is the Kafka-facing side of the outbox. Implemented by the
// events Bus.
type Dispatcher interface {
	DispatchOutbox(ctx context.Context, row Row) error
}

// Publisher periodically drains the outbox.
type Publisher struct {
	store      *Store
	dispatcher Dispatcher
	tick       time.Duration
	batch      int
	maxBackoff time.Duration
}

func NewPublisher(store *Store, d Dispatcher) *Publisher {
	return &Publisher{
		store:      store,
		dispatcher: d,
		tick:       2 * time.Second,
		batch:      32,
		maxBackoff: 5 * time.Minute,
	}
}

// Run drains the outbox until ctx is canceled.
// outboxIdleBackoffMax bounds how far the poll interval stretches when the
// outbox is empty.
//
// Each poll is a write transaction — FOR UPDATE SKIP LOCKED plus
// UPDATE ... RETURNING — issued whether or not there is anything to send. Across
// the services that run one of these, a fixed two-second tick is a constant
// floor of write traffic and WAL against the one shared Postgres, for a table
// that is empty most of the time.
//
// The cost of the backoff is latency on the FIRST event after a quiet spell:
// up to this long. It is kept deliberately short for that reason — a drain that
// finds anything resets to p.tick immediately, so a busy outbox keeps its
// original latency and only genuinely idle periods stretch out.
//
// The real fix is LISTEN/NOTIFY: the enqueue side signals, this side wakes
// immediately, and an idle outbox costs nothing at all. That needs a dedicated
// connection per service and is a larger change than this one.
const outboxIdleBackoffMax = 8 * time.Second

// nextOutboxInterval doubles the poll interval towards outboxIdleBackoffMax.
func nextOutboxInterval(current, base time.Duration) time.Duration {
	if current < base {
		current = base
	}
	if next := current * 2; next < outboxIdleBackoffMax {
		return next
	}
	return outboxIdleBackoffMax
}

func (p *Publisher) Run(ctx context.Context) {
	if p == nil || p.store == nil || p.dispatcher == nil {
		return
	}
	// A timer rather than a ticker, so the interval can adapt — see
	// outboxIdleBackoffMax.
	interval := p.tick
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			n, err := p.drainOnce(ctx)
			switch {
			case err != nil:
				slog.Warn("outbox drain", "err", err)
				// Back off on failure too: retrying a failing drain every two
				// seconds mostly multiplies whatever load is causing it.
				interval = nextOutboxInterval(interval, p.tick)
			case n > 0:
				if n >= p.batch {
					if _, err := p.drainOnce(ctx); err != nil {
						slog.Warn("outbox follow-up drain", "err", err)
					}
				}
				// There was work, so there is probably more: go straight back
				// to the base interval.
				interval = p.tick
			default:
				interval = nextOutboxInterval(interval, p.tick)
			}
			timer.Reset(interval)
		}
	}
}

func (p *Publisher) drainOnce(ctx context.Context) (int, error) {
	// Initial claim sets a short backoff so a transient kafka blip
	// retries quickly; the per-row failure path stretches it out.
	rows, err := p.store.ClaimBatch(ctx, p.batch, time.Second)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if err := p.dispatcher.DispatchOutbox(ctx, r); err != nil {
			delay := backoffFor(r.Attempts, p.maxBackoff)
			if mErr := p.store.MarkFailed(ctx, r.ID, r.Attempts, err.Error(), delay); mErr != nil {
				slog.Warn("outbox mark-failed", "id", r.ID, "err", mErr)
			}
			slog.Warn("outbox dispatch failed", "id", r.ID, "type", r.EventType,
				"attempts", r.Attempts, "err", err, "retryIn", delay)
			continue
		}
		if mErr := p.store.MarkDispatched(ctx, r.ID); mErr != nil {
			slog.Warn("outbox mark-dispatched", "id", r.ID, "err", mErr)
		}
	}
	return len(rows), nil
}

// backoffFor returns an exponential backoff: 2s, 4s, 8s, 16s, ... capped
// at max.
func backoffFor(attempts int, max time.Duration) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Duration(math.Pow(2, float64(attempts))) * time.Second
	if d > max {
		return max
	}
	return d
}

// ----- helpers -----

// txKey is the context value carrying an in-flight pgx.Tx. Mutate
// callers may populate this before invoking outbox.Enqueue so the
// insert participates in the same transaction.
type txKey struct{}

// WithTx attaches a pgx.Tx to ctx so subsequent Enqueue calls run
// against it instead of the pool.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

func txExecOr(ctx context.Context, pool *pgxpool.Pool) execer {
	if v := ctx.Value(txKey{}); v != nil {
		if tx, ok := v.(pgx.Tx); ok && tx != nil {
			return tx
		}
	}
	return pool
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ErrNotEnqueued is returned by helpers that try to enqueue without an
// active publisher.
var ErrNotEnqueued = errors.New("outbox: publisher not configured")
