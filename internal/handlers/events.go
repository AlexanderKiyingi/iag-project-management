package handlers

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

func defaultNotifyRecipient() string {
	return strings.TrimSpace(os.Getenv("NOTIFY_DEFAULT_RECIPIENT"))
}

// taskAssignDedupeKey produces a key that suppresses repeated assignment
// notifications for the same (task, assignee) within a UTC day. A subsequent
// reassignment to a different user (or a different day) generates a new key.
func taskAssignDedupeKey(taskID int, assignee string) string {
	day := time.Now().UTC().Format("2006-01-02")
	return "task-assign:" + strconv.Itoa(taskID) + ":" + strings.ToLower(strings.TrimSpace(assignee)) + ":" + day
}

func publishTaskAssigned(ctx context.Context, bus *events.Bus, repo *store.Repository, task models.Task, actor, actorEmail string) {
	if bus == nil || !bus.Enabled() || strings.TrimSpace(task.Assignee) == "" {
		return
	}
	// Within-day dedupe: if a notification for this (task, assignee) already
	// fired today, skip. A repo failure is logged but does not block the publish —
	// duplicate notifications are preferable to silent dropouts.
	if repo != nil {
		key := taskAssignDedupeKey(task.ID, task.Assignee)
		already, err := repo.WasReminderSent(ctx, key)
		if err != nil {
			slog.Warn("task-assigned dedupe check failed; publishing anyway", "key", key, "err", err)
		} else if already {
			return
		}
		if err := repo.MarkReminderSent(ctx, key); err != nil {
			slog.Warn("task-assigned dedupe mark failed", "key", key, "err", err)
		}
	}
	data := map[string]any{
		"taskId":   strconv.Itoa(task.ID),
		"taskName": task.Name,
		"assignee": task.Assignee,
		"actor":    actor,
	}
	if actorEmail != "" {
		data["actorEmail"] = actorEmail
	}
	if recipient := defaultNotifyRecipient(); recipient != "" {
		data["recipient"] = recipient
	}
	bus.PublishCommercial(ctx, events.TypeTaskAssigned, data, strconv.Itoa(task.ID))
}

func publishMentions(ctx context.Context, bus *events.Bus, mentionees []string, author, text, contextKind, contextID string) {
	if bus == nil || !bus.Enabled() || len(mentionees) == 0 {
		return
	}
	recipient := defaultNotifyRecipient()
	for _, mentionee := range mentionees {
		mentionee = strings.TrimSpace(mentionee)
		if mentionee == "" {
			continue
		}
		data := map[string]any{
			"mentionee": mentionee,
			"author":    author,
			"text":      text,
			"context":   contextKind,
			"contextId": contextID,
		}
		if recipient != "" {
			data["recipient"] = recipient
		}
		bus.PublishCommercial(ctx, events.TypeMentionCreated, data, mentionee+"-"+contextID)
	}
}
