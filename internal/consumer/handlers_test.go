package consumer

import (
	"context"
	"testing"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"

	"github.com/iag/project-management/backend/internal/models"
)

func TestHandlerIgnoresUnknownType(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	err := h.Handle(context.Background(), platformevents.Envelope{
		Type: "fleet.vehicle.broke_down", // not a PM-consumed type
		Data: map[string]any{},
	})
	if err != nil {
		t.Fatalf("unknown type should be ignored, got %v", err)
	}
}

func TestHandleRequisitionRejectsMissingFields(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	err := h.Handle(context.Background(), platformevents.Envelope{
		Type: TypeRequisitionApproved,
		Data: map[string]any{"requisitionId": "42"}, // missing workspaceOwnerUserId
	})
	if err == nil {
		t.Fatal("expected permanent error for missing workspaceOwnerUserId")
	}
	var perm *platformevents.PermanentError
	if !errorAs(err, &perm) {
		t.Fatalf("expected PermanentError, got %T", err)
	}
}

func TestHandleRequisitionRejectsNonNumericID(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	err := h.Handle(context.Background(), platformevents.Envelope{
		Type: TypeRequisitionRejected,
		Data: map[string]any{
			"workspaceOwnerUserId": "user-1",
			"requisitionId":        "not-a-number",
		},
	})
	if err == nil {
		t.Fatal("expected permanent error for non-numeric requisitionId")
	}
}

func TestParseIntID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"42", 42, true},
		{"0", 0, true},
		{"", 0, false},
		{"4a", 0, false},
		{"-1", 0, false},
	}
	for _, c := range cases {
		got, ok := parseIntID(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseIntID(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWorkspaceHasMember(t *testing.T) {
	t.Parallel()
	d := &models.Document{Members: []models.Member{
		{Initials: "AK", Name: "Alex K"},
		{Initials: "JD", Name: "Jane Doe"},
	}}
	if !workspaceHasMember(d, "ak") {
		t.Error("should match by initials (case-insensitive)")
	}
	if !workspaceHasMember(d, "jane doe") {
		t.Error("should match by name (case-insensitive)")
	}
	if workspaceHasMember(d, "absent") {
		t.Error("should not match absent owner")
	}
}

func TestNextNotificationID(t *testing.T) {
	t.Parallel()
	d := &models.Document{Notifications: []models.WorkspaceNotification{{ID: 3}, {ID: 7}, {ID: 5}}}
	if got := nextNotificationID(d); got != 8 {
		t.Errorf("nextNotificationID = %d, want 8", got)
	}
	empty := &models.Document{}
	if got := nextNotificationID(empty); got != 1 {
		t.Errorf("empty nextNotificationID = %d, want 1", got)
	}
}

// errorAs is a tiny stand-in for errors.As to avoid the extra import surface.
func errorAs(err error, target **platformevents.PermanentError) bool {
	for err != nil {
		if perm, ok := err.(*platformevents.PermanentError); ok {
			*target = perm
			return true
		}
		type unwrap interface{ Unwrap() error }
		u, ok := err.(unwrap)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
