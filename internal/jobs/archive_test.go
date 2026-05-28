package jobs

import (
	"testing"
	"time"

	"github.com/iag/project-management/backend/internal/models"
)

func TestTrimDocumentDropsOldAuditAndNotifications(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * 24 * time.Hour).Format(time.RFC3339)

	d := &models.Document{
		Audit: []models.AuditEntry{
			{ID: 1, Type: "task.created", Time: old},
			{ID: 2, Type: "task.updated", Time: recent},
		},
		Notifications: []models.WorkspaceNotification{
			{ID: 1, Title: "stale", Meta: old},
			{ID: 2, Title: "fresh", Meta: recent},
		},
	}
	cfg := ArchiveConfig{
		AuditRetention:        90 * 24 * time.Hour,
		NotificationRetention: 30 * 24 * time.Hour,
	}
	audit, notif, msg := trimDocument(d, cfg, now.Add(-90*24*time.Hour), now.Add(-30*24*time.Hour), now)
	if audit != 1 || notif != 1 || msg != 0 {
		t.Fatalf("audit=%d notif=%d msg=%d, want 1/1/0", audit, notif, msg)
	}
	if len(d.Audit) != 1 || d.Audit[0].ID != 2 {
		t.Fatalf("expected only recent audit entry to survive, got %+v", d.Audit)
	}
	if len(d.Notifications) != 1 || d.Notifications[0].ID != 2 {
		t.Fatalf("expected only fresh notification to survive, got %+v", d.Notifications)
	}
}

func TestTrimDocumentDropsMessagesFromInactiveChats(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	stale := now.Add(-200 * 24 * time.Hour).Format(time.RFC3339)
	fresh := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	cutoff := now.Add(-180 * 24 * time.Hour)

	d := &models.Document{
		Chats: []models.Chat{{ID: 1}, {ID: 2}},
		Messages: []models.Message{
			{ID: 1, ChatID: 1, Time: stale},
			{ID: 2, ChatID: 1, Time: stale},
			{ID: 3, ChatID: 2, Time: fresh}, // active chat — keep
		},
	}
	cfg := ArchiveConfig{InactiveChatThreshold: 180 * 24 * time.Hour}

	_, _, msg := trimDocument(d, cfg, time.Time{}, time.Time{}, cutoff)
	if msg != 2 {
		t.Fatalf("dropped %d messages, want 2", msg)
	}
	if len(d.Messages) != 1 || d.Messages[0].ID != 3 {
		t.Fatalf("expected only message 3 to survive, got %+v", d.Messages)
	}
}

func TestTrimDocumentSkipsUndatedEntries(t *testing.T) {
	t.Parallel()
	d := &models.Document{
		Audit: []models.AuditEntry{
			{ID: 1, Time: ""},               // undated — keep
			{ID: 2, Time: "not-a-timestamp"}, // unparseable — keep
		},
	}
	cfg := ArchiveConfig{AuditRetention: 1 * time.Hour}
	audit, _, _ := trimDocument(d, cfg, time.Now(), time.Time{}, time.Time{})
	if audit != 0 {
		t.Fatalf("expected undated entries to survive, dropped %d", audit)
	}
	if len(d.Audit) != 2 {
		t.Fatalf("expected 2 audit entries to remain, got %d", len(d.Audit))
	}
}

func TestArchiveConfigDisabledWhenZero(t *testing.T) {
	t.Parallel()
	d := &models.Document{
		Audit: []models.AuditEntry{{ID: 1, Time: time.Now().Add(-1000 * 24 * time.Hour).Format(time.RFC3339)}},
	}
	audit, _, _ := trimDocument(d, ArchiveConfig{}, time.Time{}, time.Time{}, time.Time{})
	if audit != 0 {
		t.Fatalf("expected zero-config to drop nothing, got %d", audit)
	}
}
