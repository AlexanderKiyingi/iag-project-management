// Package webhooks delivers outbound HTTP notifications for matching
// WebhookSubscriptions configured per workspace. Payload signing uses
// HMAC-SHA256 with the subscription's stored secret so the receiver
// can verify the call originated from PM.
//
// The package follows the same shape as internal/outbox: an Enqueue
// path that handlers call inside their mutation context, and a
// Publisher goroutine that drains the table with exponential backoff.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/project-management/backend/internal/models"
)

// Event is the payload a subscription receives.
type Event struct {
	Type      string `json:"type"`
	Workspace string `json:"workspace"`
	Time      string `json:"time"`
	Data      any    `json:"data"`
}

// Store wraps pm_webhook_deliveries.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Enqueue records a pending delivery for every matching subscription.
// Called by the workspace service after a successful mutation so the
// delivery row only lands if the document version actually committed.
func (s *Store) Enqueue(ctx context.Context, ownerUserID string, evt Event, subs []models.WebhookSubscription) error {
	if s == nil || s.pool == nil || len(subs) == 0 {
		return nil
	}
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		if !sub.Active || !subscriptionMatches(sub, evt.Type) {
			continue
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO pm_webhook_deliveries
				(workspace_owner_user_id, subscription_id, url, secret,
				 event_type, payload)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb)
		`, ownerUserID, sub.ID, sub.URL, sub.Secret, evt.Type, body); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionMatches(sub models.WebhookSubscription, eventType string) bool {
	if len(sub.Events) == 0 {
		return true // wildcard
	}
	for _, want := range sub.Events {
		w := strings.TrimSpace(want)
		if w == "" || w == "*" {
			return true
		}
		if w == eventType {
			return true
		}
		// glob suffix: "task.*" matches "task.created", etc.
		if strings.HasSuffix(w, ".*") && strings.HasPrefix(eventType, strings.TrimSuffix(w, "*")) {
			return true
		}
	}
	return false
}

// ----- delivery worker -----

type pendingRow struct {
	id             int64
	subscriptionID int
	url            string
	secret         string
	eventType      string
	payload        []byte
	attempts       int
}

// Publisher drains pm_webhook_deliveries.
type Publisher struct {
	pool       *pgxpool.Pool
	client     *http.Client
	tick       time.Duration
	batch      int
	maxBackoff time.Duration
}

func NewPublisher(pool *pgxpool.Pool) *Publisher {
	return &Publisher{
		pool:       pool,
		client:     &http.Client{Timeout: 15 * time.Second},
		tick:       3 * time.Second,
		batch:      16,
		maxBackoff: 10 * time.Minute,
	}
}

func (p *Publisher) Run(ctx context.Context) {
	if p == nil || p.pool == nil {
		return
	}
	t := time.NewTicker(p.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := p.drainOnce(ctx); err != nil {
				slog.Warn("webhook drain", "err", err)
			}
		}
	}
}

func (p *Publisher) drainOnce(ctx context.Context) (int, error) {
	rows, err := p.pool.Query(ctx, `
		WITH due AS (
			SELECT id FROM pm_webhook_deliveries
			WHERE delivered_at IS NULL AND available_at <= NOW()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE pm_webhook_deliveries d
		SET attempts = d.attempts + 1,
		    available_at = NOW() + INTERVAL '1 second'
		FROM due
		WHERE d.id = due.id
		RETURNING d.id, d.subscription_id, d.url, d.secret,
		          d.event_type, d.payload, d.attempts
	`, p.batch)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	pending := []pendingRow{}
	for rows.Next() {
		var r pendingRow
		if err := rows.Scan(&r.id, &r.subscriptionID, &r.url, &r.secret,
			&r.eventType, &r.payload, &r.attempts); err != nil {
			return 0, err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, row := range pending {
		p.deliver(ctx, row)
	}
	return len(pending), nil
}

func (p *Publisher) deliver(ctx context.Context, row pendingRow) {
	signature := hmac.New(sha256.New, []byte(row.secret))
	signature.Write(row.payload)
	sig := hex.EncodeToString(signature.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.url, bytes.NewReader(row.payload))
	if err != nil {
		p.markFailed(ctx, row, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-IAG-Event", row.eventType)
	req.Header.Set("X-IAG-Signature", "sha256="+sig)
	resp, err := p.client.Do(req)
	if err != nil {
		p.markFailed(ctx, row, 0, err.Error())
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if _, err := p.pool.Exec(ctx, `
			UPDATE pm_webhook_deliveries
			SET delivered_at = NOW(), last_status = $1, last_error = NULL
			WHERE id = $2
		`, resp.StatusCode, row.id); err != nil {
			slog.Warn("webhook mark delivered", "id", row.id, "err", err)
		}
		return
	}
	p.markFailed(ctx, row, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
}

func (p *Publisher) markFailed(ctx context.Context, row pendingRow, status int, errMsg string) {
	backoff := time.Duration(math.Pow(2, float64(row.attempts))) * time.Second
	if backoff > p.maxBackoff {
		backoff = p.maxBackoff
	}
	if _, err := p.pool.Exec(ctx, `
		UPDATE pm_webhook_deliveries
		SET last_status = $1, last_error = $2,
		    available_at = NOW() + ($3 || ' milliseconds')::interval
		WHERE id = $4
	`, nullStatus(status), errMsg, fmt.Sprintf("%d", backoff.Milliseconds()), row.id); err != nil {
		slog.Warn("webhook mark-failed", "id", row.id, "err", err)
	}
}

func nullStatus(s int) any {
	if s == 0 {
		return nil
	}
	return s
}

// ErrNotConfigured is returned by helpers when no store is wired.
var ErrNotConfigured = errors.New("webhooks not configured")
