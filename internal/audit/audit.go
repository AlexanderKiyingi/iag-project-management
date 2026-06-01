// Package audit provides HTTP-level audit recording for PM. It captures
// every authenticated mutation as a row in pm_request_audit so admins
// can answer "who hit what, when?" without scrubbing structured logs.
//
// The document-embedded models.AuditEntry is unchanged — it still
// captures entity-level events ("task X created by Y") inside the
// workspace document. This package is the complementary request-level
// view: HTTP method, path, status, latency, IP.
package audit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iag/project-management/backend/internal/middleware"
)

// Recorder writes a single audit row. Implementations should be
// non-blocking from the caller's perspective; the default Postgres
// recorder queues writes onto a buffered channel drained by a
// background goroutine.
type Recorder interface {
	Record(ctx context.Context, row Row) error
}

// Row captures a single HTTP exchange.
type Row struct {
	OccurredAt           time.Time
	WorkspaceOwnerUserID string
	ActorUserID          string
	ActorDisplay         string
	Method               string
	Path                 string
	Status               int
	DurationMs           int
	IP                   string
	UserAgent            string
	RequestID            string
}

// NewMiddleware returns a Gin middleware that records every request
// matching the configured filter (defaults to all mutating verbs on
// /api/v1/*). Read-only GETs and health/probe endpoints are skipped
// to keep the table small.
func NewMiddleware(rec Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if rec == nil {
			return
		}
		if !shouldRecord(c.Request.Method, c.Request.URL.Path) {
			return
		}
		uid, hasUID := middleware.UserID(c)
		actor := c.GetHeader("X-Workspace-User")
		actorUserID := ""
		if hasUID {
			actorUserID = uid.String()
		}
		row := Row{
			OccurredAt:           start,
			WorkspaceOwnerUserID: actorUserID,
			ActorUserID:          actorUserID,
			ActorDisplay:         actor,
			Method:               c.Request.Method,
			Path:                 c.Request.URL.Path,
			Status:               c.Writer.Status(),
			DurationMs:           int(time.Since(start) / time.Millisecond),
			IP:                   clientIP(c),
			UserAgent:            c.Request.UserAgent(),
			RequestID:            c.GetHeader("X-Request-Id"),
		}
		if err := rec.Record(c.Request.Context(), row); err != nil {
			slog.Warn("audit record failed", "err", err, "path", row.Path)
		}
	}
}

// shouldRecord filters down to mutation calls under /api/v1.
// Health, /openapi, and pure-read GET are skipped.
func shouldRecord(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return false
	}
	// /api/v1/ws/* is the websocket upgrade — skip; the connection
	// lifecycle is its own concern and shouldn't bloat the audit.
	if strings.HasPrefix(path, "/api/v1/ws/") {
		return false
	}
	return true
}

func clientIP(c *gin.Context) string {
	if ip := strings.TrimSpace(c.GetHeader("X-Forwarded-For")); ip != "" {
		if comma := strings.Index(ip, ","); comma > 0 {
			return strings.TrimSpace(ip[:comma])
		}
		return ip
	}
	if ip := strings.TrimSpace(c.GetHeader("X-Real-IP")); ip != "" {
		return ip
	}
	return c.ClientIP()
}

// ----- Postgres recorder -----

// PgxRecorder records audit rows via a background goroutine to avoid
// blocking request handlers. Backed by a buffered channel; when the
// channel is full the row is dropped (with a warn log) rather than
// blocking the user.
type PgxRecorder struct {
	pool   *pgxpool.Pool
	queue  chan Row
	closed chan struct{}
}

func NewPgxRecorder(pool *pgxpool.Pool, bufferSize int) *PgxRecorder {
	if bufferSize <= 0 {
		bufferSize = 512
	}
	r := &PgxRecorder{
		pool:   pool,
		queue:  make(chan Row, bufferSize),
		closed: make(chan struct{}),
	}
	go r.drain()
	return r
}

func (r *PgxRecorder) Record(_ context.Context, row Row) error {
	if r == nil || r.pool == nil {
		return errors.New("recorder not initialized")
	}
	select {
	case r.queue <- row:
		return nil
	default:
		return errors.New("audit queue full; row dropped")
	}
}

// Close stops the background drainer after flushing pending rows.
// Safe to call multiple times.
func (r *PgxRecorder) Close() {
	if r == nil {
		return
	}
	select {
	case <-r.closed:
		return
	default:
		close(r.closed)
	}
}

func (r *PgxRecorder) drain() {
	for {
		select {
		case <-r.closed:
			r.flushPending()
			return
		case row := <-r.queue:
			r.insert(row)
		}
	}
}

func (r *PgxRecorder) flushPending() {
	for {
		select {
		case row := <-r.queue:
			r.insert(row)
		default:
			return
		}
	}
}

func (r *PgxRecorder) insert(row Row) {
	if row.OccurredAt.IsZero() {
		row.OccurredAt = time.Now()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO pm_request_audit
			(occurred_at, workspace_owner_user_id, actor_user_id, actor_display,
			 method, path, status, duration_ms, ip, user_agent, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, row.OccurredAt, nullable(row.WorkspaceOwnerUserID), nullable(row.ActorUserID),
		nullable(row.ActorDisplay), row.Method, row.Path, row.Status, row.DurationMs,
		nullable(row.IP), nullable(row.UserAgent), nullable(row.RequestID))
	if err != nil {
		slog.Warn("audit insert failed", "err", err, "path", row.Path)
	}
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// ListFilter narrows a paginated audit query.
type ListFilter struct {
	WorkspaceOwnerUserID string
	ActorUserID          string
	Path                 string
	From                 *time.Time
	To                   *time.Time
	Limit                int
	Offset               int
}

// List returns rows ordered newest first.
func List(ctx context.Context, pool *pgxpool.Pool, f ListFilter) ([]Row, error) {
	if pool == nil {
		return nil, errors.New("pool not initialized")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{}
	clauses := []string{}
	add := func(clause string, v any) {
		args = append(args, v)
		clauses = append(clauses, strings.Replace(clause, "$?", "$"+strconv.Itoa(len(args)), 1))
	}
	if f.WorkspaceOwnerUserID != "" {
		add("workspace_owner_user_id = $?", f.WorkspaceOwnerUserID)
	}
	if f.ActorUserID != "" {
		add("actor_user_id = $?", f.ActorUserID)
	}
	if f.Path != "" {
		add("path = $?", f.Path)
	}
	if f.From != nil {
		add("occurred_at >= $?", *f.From)
	}
	if f.To != nil {
		add("occurred_at <= $?", *f.To)
	}
	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit, f.Offset)
	q := `SELECT occurred_at, workspace_owner_user_id, actor_user_id, actor_display,
	             method, path, status, duration_ms, ip, user_agent, request_id
	      FROM pm_request_audit ` + where +
		` ORDER BY occurred_at DESC LIMIT $` + strconv.Itoa(len(args)-1) +
		` OFFSET $` + strconv.Itoa(len(args))
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var ws, actor, display, ip, ua, rid *string
		if err := rows.Scan(&r.OccurredAt, &ws, &actor, &display, &r.Method, &r.Path,
			&r.Status, &r.DurationMs, &ip, &ua, &rid); err != nil {
			return nil, err
		}
		if ws != nil {
			r.WorkspaceOwnerUserID = *ws
		}
		if actor != nil {
			r.ActorUserID = *actor
		}
		if display != nil {
			r.ActorDisplay = *display
		}
		if ip != nil {
			r.IP = *ip
		}
		if ua != nil {
			r.UserAgent = *ua
		}
		if rid != nil {
			r.RequestID = *rid
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
