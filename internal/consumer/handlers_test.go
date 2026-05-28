package consumer

import (
	"context"
	"testing"

	platformevents "github.com/alvor-technologies/iag-platform-go/events"

	pmevents "github.com/iag/project-management/backend/internal/events"
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

func TestHandlerSkipsPMSelfPublishedEvents(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	// An event PM emits — the consumer would otherwise route requisition events
	// through h.handleRequisition, which would fail validation. The source
	// filter must short-circuit before that.
	err := h.Handle(context.Background(), platformevents.Envelope{
		Source: pmevents.Source,
		Type:   TypeRequisitionApproved,
		Data:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("PM-sourced event should be skipped, got %v", err)
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

func TestApplyUserDeactivationClearsAssignmentsAndMarksMember(t *testing.T) {
	t.Parallel()
	d := &models.Document{
		Members: []models.Member{
			{Initials: "AK", Name: "Alex K", Email: "alex@example.com"},
			{Initials: "JD", Name: "Jane Doe", Email: "jane@example.com"},
		},
		Tasks: []models.Task{
			{ID: 1, Name: "open", Assignee: "alex@example.com", Done: false},
			{ID: 2, Name: "done", Assignee: "alex@example.com", Done: true},
			{ID: 3, Name: "other", Assignee: "jane@example.com", Done: false},
		},
	}
	applyUserDeactivation(d, "", "alex@example.com", "Alex K", "admin@iag.local")

	if d.Members[0].Active == nil || *d.Members[0].Active {
		t.Errorf("member 0 should be marked Active=false")
	}
	if d.Members[1].Active != nil {
		t.Errorf("member 1 should not be touched, got Active=%v", d.Members[1].Active)
	}
	if d.Tasks[0].Assignee != "" {
		t.Errorf("open task should be unassigned, got %q", d.Tasks[0].Assignee)
	}
	if d.Tasks[1].Assignee != "alex@example.com" {
		t.Errorf("done task should keep assignee, got %q", d.Tasks[1].Assignee)
	}
	if d.Tasks[2].Assignee != "jane@example.com" {
		t.Errorf("other task should keep assignee, got %q", d.Tasks[2].Assignee)
	}
	if len(d.Audit) != 1 || d.Audit[0].Type != "auth.user.deactivated" {
		t.Errorf("expected one audit entry of type auth.user.deactivated, got %+v", d.Audit)
	}
}

func TestApplyUserDeactivationIdempotent(t *testing.T) {
	t.Parallel()
	off := false
	d := &models.Document{
		Members: []models.Member{
			{Email: "alex@example.com", Active: &off},
		},
	}
	applyUserDeactivation(d, "", "alex@example.com", "", "actor")
	if len(d.Audit) != 0 {
		t.Errorf("expected no audit entry when nothing changed, got %d", len(d.Audit))
	}
}

func TestApplyUserDeactivationNoMatchNoOp(t *testing.T) {
	t.Parallel()
	d := &models.Document{
		Members: []models.Member{{Email: "other@example.com"}},
		Tasks:   []models.Task{{ID: 1, Assignee: "other@example.com"}},
	}
	applyUserDeactivation(d, "", "absent@example.com", "Absent", "actor")
	if len(d.Audit) != 0 {
		t.Errorf("no match should produce no audit, got %d", len(d.Audit))
	}
	if d.Members[0].Active != nil {
		t.Errorf("unrelated member should be untouched")
	}
}

func TestApplyUserDeactivationMatchesByUserIDFirst(t *testing.T) {
	t.Parallel()
	d := &models.Document{
		Members: []models.Member{
			// Same email shared by two members — UserID must disambiguate.
			{Initials: "AK", Name: "Alex K", Email: "shared@example.com", UserID: "user-1"},
			{Initials: "AL", Name: "Alex L", Email: "shared@example.com", UserID: "user-2"},
		},
		Tasks: []models.Task{
			{ID: 1, Assignee: "user-1", Done: false},
			{ID: 2, Assignee: "user-2", Done: false},
		},
	}
	applyUserDeactivation(d, "user-1", "", "", "actor")

	if d.Members[0].Active == nil || *d.Members[0].Active {
		t.Error("user-1 member should be Active=false")
	}
	if d.Members[1].Active != nil {
		t.Error("user-2 member should be untouched")
	}
	if d.Tasks[0].Assignee != "" {
		t.Errorf("user-1 task should be unassigned, got %q", d.Tasks[0].Assignee)
	}
	if d.Tasks[1].Assignee != "user-2" {
		t.Errorf("user-2 task should keep assignee, got %q", d.Tasks[1].Assignee)
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
