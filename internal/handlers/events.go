package handlers

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/models"
)

func defaultNotifyRecipient() string {
	return strings.TrimSpace(os.Getenv("NOTIFY_DEFAULT_RECIPIENT"))
}

func publishTaskAssigned(ctx context.Context, bus *events.Bus, task models.Task, actor, actorEmail string) {
	if bus == nil || !bus.Enabled() || strings.TrimSpace(task.Assignee) == "" {
		return
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
