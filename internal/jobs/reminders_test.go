package jobs

import (
	"testing"
	"time"
)

func TestParseReminderTime(t *testing.T) {
	t.Parallel()
	ts, err := parseReminderTime("2026-05-28T10:00:00Z")
	if err != nil {
		t.Fatalf("parse RFC3339: %v", err)
	}
	if ts.Location() != time.UTC {
		t.Fatalf("expected UTC, got %v", ts.Location())
	}
}

func TestTaskDueReminderDaysDefault(t *testing.T) {
	t.Setenv("TASK_DUE_REMINDER_DAYS", "")
	if got := taskDueReminderDays(); got != 7 {
		t.Fatalf("default days = %d, want 7", got)
	}
}
