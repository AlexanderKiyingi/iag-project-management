package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

// ArchiveConfig configures retention thresholds. Non-positive durations disable
// the corresponding sweep.
type ArchiveConfig struct {
	AuditRetention        time.Duration
	NotificationRetention time.Duration
	InactiveChatThreshold time.Duration
}

// ArchiveResult summarises what one RunArchive invocation removed.
type ArchiveResult struct {
	Workspaces       int
	AuditDropped     int
	NotifDropped     int
	MessagesDropped  int
	WorkspacesFailed int
}

// DefaultArchiveConfig reads thresholds from env vars (in days):
//   AUDIT_RETENTION_DAYS (default 90), NOTIFICATION_RETENTION_DAYS (default 30),
//   INACTIVE_CHAT_DAYS (default 180). 0 disables a sweep.
func DefaultArchiveConfig() ArchiveConfig {
	return ArchiveConfig{
		AuditRetention:        days(envIntOr("AUDIT_RETENTION_DAYS", 90)),
		NotificationRetention: days(envIntOr("NOTIFICATION_RETENTION_DAYS", 30)),
		InactiveChatThreshold: days(envIntOr("INACTIVE_CHAT_DAYS", 180)),
	}
}

// RunArchive trims old Audit + Notifications + Messages-from-inactive-chats
// across every workspace. The function is idempotent: each pass removes only
// entries that exceed the configured retention. A single audit entry summarising
// the trim is appended so admins can see what happened.
func RunArchive(ctx context.Context, repo *store.Repository, cfg ArchiveConfig) (ArchiveResult, error) {
	var result ArchiveResult
	if repo == nil {
		return result, nil
	}
	list, err := repo.ListWorkspaces(ctx)
	if err != nil {
		return result, err
	}
	result.Workspaces = len(list)

	now := time.Now().UTC()
	auditCutoff := now.Add(-cfg.AuditRetention)
	notifCutoff := now.Add(-cfg.NotificationRetention)
	chatCutoff := now.Add(-cfg.InactiveChatThreshold)

	for _, ws := range list {
		audit, notif, msg, err := archiveWorkspace(ctx, repo, ws, cfg, auditCutoff, notifCutoff, chatCutoff, now)
		if err != nil {
			result.WorkspacesFailed++
			slog.Warn("archive: workspace failed", "owner", ws.OwnerUserID, "err", err)
			continue
		}
		result.AuditDropped += audit
		result.NotifDropped += notif
		result.MessagesDropped += msg
	}
	slog.Info("pm archive run",
		"workspaces", result.Workspaces,
		"audit_dropped", result.AuditDropped,
		"notif_dropped", result.NotifDropped,
		"messages_dropped", result.MessagesDropped,
		"failed", result.WorkspacesFailed,
	)
	return result, nil
}

// archiveWorkspace handles a single workspace with one optimistic-lock retry.
func archiveWorkspace(
	ctx context.Context,
	repo *store.Repository,
	ws store.Workspace,
	cfg ArchiveConfig,
	auditCutoff, notifCutoff, chatCutoff, now time.Time,
) (audit, notif, msg int, err error) {
	const maxAttempts = 2
	for attempt := 0; attempt < maxAttempts; attempt++ {
		current := ws
		if attempt > 0 {
			refreshed, refreshErr := repo.GetOrCreate(ctx, ws.OwnerUserID)
			if refreshErr != nil {
				return 0, 0, 0, refreshErr
			}
			current = refreshed
		}

		var doc models.Document
		if err := json.Unmarshal(current.Document, &doc); err != nil {
			return 0, 0, 0, err
		}

		audit, notif, msg = trimDocument(&doc, cfg, auditCutoff, notifCutoff, chatCutoff)
		if audit == 0 && notif == 0 && msg == 0 {
			return 0, 0, 0, nil
		}

		summary := fmt.Sprintf("archive: dropped audit=%d notifications=%d messages=%d", audit, notif, msg)
		models.AppendAudit(&doc, "system", "system.archive", summary, nil)
		_ = now // keep parameter for future "track last archive time" feature

		raw, err := json.Marshal(doc)
		if err != nil {
			return 0, 0, 0, err
		}
		if _, err := repo.Update(ctx, current.OwnerUserID, raw, current.Version); err != nil {
			if errors.Is(err, store.ErrVersionConflict) && attempt+1 < maxAttempts {
				continue
			}
			return 0, 0, 0, err
		}
		return audit, notif, msg, nil
	}
	return 0, 0, 0, store.ErrVersionConflict
}

// trimDocument applies retention rules in place and returns the counts dropped.
func trimDocument(d *models.Document, cfg ArchiveConfig, auditCutoff, notifCutoff, chatCutoff time.Time) (audit, notif, msg int) {
	if cfg.AuditRetention > 0 {
		kept := d.Audit[:0]
		for _, e := range d.Audit {
			t, ok := parseEntryTime(e.Time)
			if !ok || !t.Before(auditCutoff) {
				kept = append(kept, e)
				continue
			}
			audit++
		}
		d.Audit = kept
	}
	if cfg.NotificationRetention > 0 {
		// Notifications use a separate Meta timestamp shape (RFC3339 string) or
		// none. We treat them as undated if Meta isn't a parseable RFC3339; in
		// that case fall back to keeping them. This avoids trimming dateless
		// notifications.
		kept := d.Notifications[:0]
		for _, n := range d.Notifications {
			t, ok := parseEntryTime(n.Meta)
			if !ok || !t.Before(notifCutoff) {
				kept = append(kept, n)
				continue
			}
			notif++
		}
		d.Notifications = kept
	}
	if cfg.InactiveChatThreshold > 0 && len(d.Chats) > 0 {
		// Group messages by chat and find each chat's last activity timestamp.
		lastSeen := make(map[int]time.Time, len(d.Chats))
		for _, m := range d.Messages {
			t, ok := parseEntryTime(m.Time)
			if !ok {
				continue
			}
			if prev, exists := lastSeen[m.ChatID]; !exists || t.After(prev) {
				lastSeen[m.ChatID] = t
			}
		}
		inactive := make(map[int]bool, len(d.Chats))
		for _, c := range d.Chats {
			last, ok := lastSeen[c.ID]
			if !ok {
				continue // no messages — keep
			}
			if last.Before(chatCutoff) {
				inactive[c.ID] = true
			}
		}
		if len(inactive) > 0 {
			kept := d.Messages[:0]
			for _, m := range d.Messages {
				if inactive[m.ChatID] {
					t, ok := parseEntryTime(m.Time)
					if ok && t.Before(chatCutoff) {
						msg++
						continue
					}
				}
				kept = append(kept, m)
			}
			d.Messages = kept
		}
	}
	return audit, notif, msg
}

func parseEntryTime(raw string) (time.Time, bool) {
	t, err := parseReminderTime(raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func envIntOr(key string, fallback int) int {
	raw := envOr(key, "")
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func days(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * 24 * time.Hour
}
