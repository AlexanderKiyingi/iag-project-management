package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

// RunReminders scans all workspaces for due message reminders and upcoming task due dates.
func RunReminders(ctx context.Context, repo *store.Repository, bus *events.Bus) (int, error) {
	if repo == nil {
		return 0, nil
	}
	list, err := repo.ListWorkspaces(ctx)
	if err != nil {
		return 0, err
	}

	recipient := strings.TrimSpace(os.Getenv("NOTIFY_DEFAULT_RECIPIENT"))
	channel := strings.TrimSpace(envOr("NOTIFY_CHANNEL", "email"))
	if channel == "" {
		channel = "email"
	}
	withinDays := taskDueReminderDays()
	now := time.Now().UTC()
	sent := 0

	for _, ws := range list {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			slog.Warn("reminder job: skip workspace", "owner", ws.OwnerUserID, "err", err)
			continue
		}

		for _, msg := range doc.Messages {
			if msg.ReminderAt == nil || strings.TrimSpace(*msg.ReminderAt) == "" || msg.Deleted {
				continue
			}
			due, err := parseReminderTime(*msg.ReminderAt)
			if err != nil || due.After(now) {
				continue
			}
			key := messageReminderKey(ws.ID, msg.ID, *msg.ReminderAt)
			already, err := repo.WasReminderSent(ctx, key)
			if err != nil || already {
				continue
			}
			if recipient == "" || bus == nil || !bus.Enabled() {
				continue
			}
			bus.PublishPMAlert(ctx, channel, recipient, "pm.message.reminder", map[string]string{
				"chatId":  strconv.Itoa(msg.ChatID),
				"message": truncate(msg.Text, 200),
				"author":  msg.Author,
			})
			if err := repo.MarkReminderSent(ctx, key); err != nil {
				slog.Warn("message reminder mark failed", "key", key, "err", err)
				continue
			}
			sent++
		}

		for _, task := range doc.Tasks {
			if task.Done || strings.TrimSpace(task.Due) == "" {
				continue
			}
			dueDate, err := time.Parse("2006-01-02", strings.TrimSpace(task.Due))
			if err != nil {
				continue
			}
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			dueDay := time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 0, 0, 0, 0, time.UTC)
			if dueDay.Before(today) || dueDay.Sub(today) > time.Duration(withinDays)*24*time.Hour {
				continue
			}
			key := taskDueReminderKey(ws.ID, task.ID, task.Due)
			already, err := repo.WasReminderSent(ctx, key)
			if err != nil || already {
				continue
			}
			if recipient == "" || bus == nil || !bus.Enabled() {
				continue
			}
			bus.PublishPMAlert(ctx, channel, recipient, "pm.task.due_soon", map[string]string{
				"taskId":   strconv.Itoa(task.ID),
				"taskName": task.Name,
				"assignee": task.Assignee,
				"due":      task.Due,
			})
			if err := repo.MarkReminderSent(ctx, key); err != nil {
				slog.Warn("task due reminder mark failed", "key", key, "err", err)
				continue
			}
			sent++
		}
	}

	slog.Info("pm reminders processed", "workspaces", len(list), "sent", sent, "task_due_within_days", withinDays)
	return sent, nil
}

func messageReminderKey(workspaceID uuid.UUID, messageID int, reminderAt string) string {
	return "msg:" + workspaceID.String() + ":" + strconv.Itoa(messageID) + ":" + reminderAt
}

func taskDueReminderKey(workspaceID uuid.UUID, taskID int, due string) string {
	return "task-due:" + workspaceID.String() + ":" + strconv.Itoa(taskID) + ":" + due
}

func taskDueReminderDays() int {
	raw := os.Getenv("TASK_DUE_REMINDER_DAYS")
	if raw == "" {
		return 7
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 7
	}
	return n
}

func parseReminderTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Parse("2006-01-02T15:04:05", raw)
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
