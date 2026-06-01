package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

// RecurrenceResult summarises one RunRecurrence invocation.
type RecurrenceResult struct {
	WorkspacesScanned int
	TasksCloned       int
	ChainsEnded       int
}

// RunRecurrence walks every workspace and, for each task with a
// Recurrence whose NextDueAt is due, clones the task forward with the
// next computed due date. The original task is left as-is so it
// continues to anchor the chain; only when the chain reaches its end
// (count exhausted or past EndDate) is the recurrence cleared.
//
// Idempotency: NextDueAt is updated on every clone, so reruns inside
// the same time bucket are no-ops. Time-of-day from the parent task's
// due field is preserved when possible.
func RunRecurrence(ctx context.Context, repo *store.Repository) (RecurrenceResult, error) {
	if repo == nil {
		return RecurrenceResult{}, errors.New("repo not initialized")
	}
	workspaces, err := repo.ListWorkspaces(ctx)
	if err != nil {
		return RecurrenceResult{}, fmt.Errorf("list workspaces: %w", err)
	}
	now := time.Now().UTC()
	res := RecurrenceResult{WorkspacesScanned: len(workspaces)}
	for _, ws := range workspaces {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			slog.Warn("recurrence: decode workspace", "owner", ws.OwnerUserID, "err", err)
			continue
		}
		changed := false
		nowTasks := len(doc.Tasks)
		for i := 0; i < nowTasks; i++ {
			t := &doc.Tasks[i]
			if t.Recurrence == nil || t.DeletedAt != "" {
				continue
			}
			next, mutated := advanceRecurrence(*t, now)
			if !mutated {
				continue
			}
			changed = true
			// If the recurrence chain ended, clear the spec on the
			// originating task and move on.
			if next == nil {
				doc.Tasks[i].Recurrence = nil
				res.ChainsEnded++
				continue
			}
			// Otherwise materialize the next instance and update the
			// parent's NextDueAt cursor.
			doc.Tasks[i].Recurrence.NextDueAt = next.Recurrence.NextDueAt
			clone := *next
			clone.ID = models.NextTaskID(&doc)
			clone.Activity = []models.ActivityEntry{}
			doc.Tasks = append(doc.Tasks, clone)
			cid := clone.ID
			models.AppendAudit(&doc, "system", "task.recurrence.cloned",
				fmt.Sprintf("created task #%d from recurring #%d", cid, t.ID), &cid)
			res.TasksCloned++
		}
		if !changed {
			continue
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			slog.Warn("recurrence: marshal workspace", "owner", ws.OwnerUserID, "err", err)
			continue
		}
		if _, err := repo.Update(ctx, ws.OwnerUserID, raw, ws.Version); err != nil {
			slog.Warn("recurrence: persist workspace", "owner", ws.OwnerUserID, "err", err)
		}
	}
	return res, nil
}

// advanceRecurrence returns (nextTask, true) when the recurrence is
// due and a clone should be materialized. The returned task is a
// shallow copy of the parent with Due/EndDate/StartDate advanced and
// Recurrence.NextDueAt rolled forward to the instance after the clone.
// Returns (nil, true) when the chain has terminated. (nil, false)
// means nothing is due yet.
func advanceRecurrence(t models.Task, now time.Time) (*models.Task, bool) {
	rec := t.Recurrence
	if rec == nil {
		return nil, false
	}
	pattern := strings.ToLower(strings.TrimSpace(rec.Pattern))
	interval := rec.Interval
	if interval <= 0 {
		interval = 1
	}
	// Determine the anchor — the most recent due-equivalent timestamp
	// the chain has tracked.
	anchorRaw := rec.NextDueAt
	if anchorRaw == "" {
		anchorRaw = firstNonEmpty(t.EndDate, t.Due, t.StartDate)
	}
	anchor, ok := parseDateOrTime(anchorRaw)
	if !ok {
		return nil, false
	}
	if now.Before(anchor) {
		return nil, false
	}
	// Recurrence has ended.
	if rec.EndDate != "" {
		if endAt, ok := parseDateOrTime(rec.EndDate); ok && anchor.After(endAt) {
			return nil, true
		}
	}
	advance, ok := advanceBy(anchor, pattern, interval)
	if !ok {
		return nil, false
	}
	clone := t
	clone.Done = false
	clone.ApprovedBy = nil
	clone.Status = "on_track"
	clone.DeletedAt = ""
	clone.Due = formatLikely(anchorRaw, advance)
	if t.EndDate != "" {
		clone.EndDate = formatLikely(t.EndDate, advance)
	}
	if t.StartDate != "" {
		// Shift StartDate by the same delta the due advanced.
		clone.StartDate = formatLikely(t.StartDate, advance)
	}
	// Compute the cursor for the NEXT instance so reruns are idempotent.
	nextCursor, _ := advanceBy(advance, pattern, interval)
	clone.Recurrence = &models.TaskRecurrence{
		Pattern:   pattern,
		Interval:  interval,
		EndDate:   rec.EndDate,
		NextDueAt: nextCursor.Format("2006-01-02"),
	}
	return &clone, true
}

func advanceBy(anchor time.Time, pattern string, interval int) (time.Time, bool) {
	switch pattern {
	case "daily":
		return anchor.AddDate(0, 0, interval), true
	case "weekly":
		return anchor.AddDate(0, 0, 7*interval), true
	case "monthly":
		return anchor.AddDate(0, interval, 0), true
	default:
		return time.Time{}, false
	}
}

func parseDateOrTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if v, err := time.Parse("2006-01-02", raw); err == nil {
		return v.UTC(), true
	}
	if v, err := time.Parse(time.RFC3339, raw); err == nil {
		return v.UTC(), true
	}
	return time.Time{}, false
}

// formatLikely mirrors the format of `like` (date-only or RFC3339)
// when serializing the advanced timestamp, so callers don't get an
// upgraded string format unexpectedly.
func formatLikely(like string, t time.Time) string {
	if strings.Contains(strings.TrimSpace(like), "T") {
		return t.UTC().Format(time.RFC3339)
	}
	return t.UTC().Format("2006-01-02")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
