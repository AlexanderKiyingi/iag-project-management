package handlers

import (
	"testing"

	"github.com/iag/project-management/backend/internal/models"
)

// Every value the frontend can send must be accepted. The sets are spelled to
// match manager-entities.ts exactly, so a drift on either side is a 400 the
// user sees as "save failed" with no explanation.
func TestStatusSetsMatchWhatTheFrontendSends(t *testing.T) {
	cases := []struct {
		level string
		set   []string
		sends []string
	}{
		{"phase", models.PhaseStatuses,
			[]string{"Planned", "In progress", "Completed", "On hold", "Cancelled"}},
		{"activity", models.ActivityStatuses,
			[]string{"Todo", "In progress", "Blocked", "Done", "Cancelled"}},
		{"work program", models.WorkProgramStatuses,
			[]string{"Planned", "Scheduled", "In progress", "Completed", "Cancelled"}},
	}
	for _, c := range cases {
		for _, s := range c.sends {
			if !models.ValidStatus(s, c.set) {
				t.Errorf("%s: %q is offered by the UI but rejected by the service", c.level, s)
			}
		}
		// Empty is accepted and defaulted by the handler.
		if !models.ValidStatus("", c.set) {
			t.Errorf("%s: empty status rejected; the handler defaults it", c.level)
		}
	}
}

// The sets are per level for a reason: a phase is never Blocked, an activity is
// never On hold. A shared set would quietly accept both.
func TestStatusSetsDoNotLeakBetweenLevels(t *testing.T) {
	if models.ValidStatus("Blocked", models.PhaseStatuses) {
		t.Error("a phase accepted Blocked, which is an activity state")
	}
	if models.ValidStatus("On hold", models.ActivityStatuses) {
		t.Error("an activity accepted On hold, which is a phase state")
	}
	if models.ValidStatus("Todo", models.WorkProgramStatuses) {
		t.Error("a work program accepted Todo, which is an activity state")
	}
}

func TestStatusMatchingIsCaseSensitive(t *testing.T) {
	for _, s := range []string{"in progress", "PLANNED", "cancelled"} {
		if models.ValidStatus(s, models.PhaseStatuses) {
			t.Errorf("%q accepted; the set is case-sensitive", s)
		}
	}
}

func TestClampProgress(t *testing.T) {
	cases := map[int]int{-40: 0, 0: 0, 55: 55, 100: 100, 140: 100}
	for in, want := range cases {
		if got := models.ClampProgress(in); got != want {
			t.Errorf("ClampProgress(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestDefaultStatus(t *testing.T) {
	if got := defaultStatus("  ", models.PhaseStatusDefault); got != "Planned" {
		t.Errorf("blank phase status defaulted to %q, want Planned", got)
	}
	if got := defaultStatus("", models.ActivityStatusDefault); got != "Todo" {
		t.Errorf("blank activity status defaulted to %q, want Todo", got)
	}
	if got := defaultStatus("On hold", models.PhaseStatusDefault); got != "On hold" {
		t.Errorf("a set status was overwritten with %q", got)
	}
}

// A patch must move only what it names. The whole point of PATCH here is that
// the schedule screens send one field at a time.
func TestApplyPhasePatchTouchesOnlyNamedFields(t *testing.T) {
	p := models.Phase{
		ID: "PH-2026-0001", ProjectID: "proj-1", Name: "Substructure",
		Code: "P1", StartDate: "2026-01-05", DueDate: "2026-03-30",
		Progress: 40, Status: "In progress", Desc: "footings and slab",
	}

	applyPhasePatch(&p, map[string]any{"progress": float64(65)})

	if p.Progress != 65 {
		t.Errorf("Progress = %d, want 65", p.Progress)
	}
	if p.Name != "Substructure" || p.DueDate != "2026-03-30" || p.Status != "In progress" {
		t.Errorf("unnamed fields moved: %+v", p)
	}
}

func TestApplyPhasePatchClampsProgress(t *testing.T) {
	p := models.Phase{Progress: 10}
	applyPhasePatch(&p, map[string]any{"progress": float64(320)})
	if p.Progress != 100 {
		t.Errorf("Progress = %d, want 100", p.Progress)
	}
}

// Blanking a status would leave a row in a state that is not in the closed set,
// so an empty string is ignored rather than written.
func TestApplyPhasePatchIgnoresBlankStatus(t *testing.T) {
	p := models.Phase{Status: "In progress"}
	applyPhasePatch(&p, map[string]any{"status": ""})
	if p.Status != "In progress" {
		t.Errorf("Status = %q, want In progress", p.Status)
	}
}

// Re-parenting must be possible, including detaching: an activity moved out of
// every phase is a real state, so an explicit empty phaseId clears it.
func TestApplyActivityPatchCanDetachFromItsPhase(t *testing.T) {
	a := models.Activity{PhaseID: "PH-2026-0001", Name: "Excavation"}
	applyActivityPatch(&a, map[string]any{"phaseId": ""})
	if a.PhaseID != "" {
		t.Errorf("PhaseID = %q, want empty", a.PhaseID)
	}
	if a.Name != "Excavation" {
		t.Errorf("Name moved to %q", a.Name)
	}
}

func TestApplyWorkProgramPatchMovesSchedule(t *testing.T) {
	w := models.WorkProgram{
		ID: "WP-2026-0001", Name: "Week 12 blockwork",
		ScheduledStart: "2026-03-16", ScheduledEnd: "2026-03-20",
		AssignedTo: "site-team-a", Status: "Planned",
	}

	applyWorkProgramPatch(&w, map[string]any{
		"scheduledStart": "2026-03-23",
		"scheduledEnd":   "2026-03-27",
		"status":         "In progress",
	})

	if w.ScheduledStart != "2026-03-23" || w.ScheduledEnd != "2026-03-27" {
		t.Errorf("schedule not moved: %+v", w)
	}
	if w.Status != "In progress" {
		t.Errorf("Status = %q", w.Status)
	}
	if w.AssignedTo != "site-team-a" || w.Name != "Week 12 blockwork" {
		t.Errorf("unnamed fields moved: %+v", w)
	}
}

// Deleting a phase detaches its children rather than destroying them. This is
// the behaviour the delete handler relies on; pinning it here keeps the intent
// from being "simplified" into a cascade later.
func TestPhaseDeleteDetachesChildren(t *testing.T) {
	d := &models.Document{
		Phases: []models.Phase{
			{ID: "PH-1", Name: "Substructure"},
			{ID: "PH-2", Name: "Superstructure"},
		},
		Activities: []models.Activity{
			{ID: "ACT-1", PhaseID: "PH-1", Name: "Excavation"},
			{ID: "ACT-2", PhaseID: "PH-2", Name: "Columns"},
		},
		WorkPrograms: []models.WorkProgram{
			{ID: "WP-1", PhaseID: "PH-1", Name: "Week 1"},
		},
	}

	// Mirrors deletePhase's mutation body.
	out := d.Phases[:0]
	for _, row := range d.Phases {
		if row.ID == "PH-1" {
			continue
		}
		out = append(out, row)
	}
	d.Phases = out
	for i := range d.Activities {
		if d.Activities[i].PhaseID == "PH-1" {
			d.Activities[i].PhaseID = ""
		}
	}
	for i := range d.WorkPrograms {
		if d.WorkPrograms[i].PhaseID == "PH-1" {
			d.WorkPrograms[i].PhaseID = ""
		}
	}

	if len(d.Phases) != 1 || d.Phases[0].ID != "PH-2" {
		t.Fatalf("phases = %+v, want only PH-2", d.Phases)
	}
	if len(d.Activities) != 2 {
		t.Errorf("activities were destroyed with the phase: %+v", d.Activities)
	}
	if d.Activities[0].PhaseID != "" {
		t.Errorf("ACT-1 still points at the deleted phase")
	}
	if d.Activities[1].PhaseID != "PH-2" {
		t.Errorf("ACT-2 was detached from a phase that still exists")
	}
	if d.WorkPrograms[0].PhaseID != "" {
		t.Errorf("WP-1 still points at the deleted phase")
	}
}

func TestParentLookups(t *testing.T) {
	d := &models.Document{
		Phases:     []models.Phase{{ID: "PH-1"}},
		Activities: []models.Activity{{ID: "ACT-1"}},
	}
	if !hasPhase(d, "PH-1") || hasPhase(d, "PH-9") {
		t.Error("hasPhase is wrong")
	}
	if !hasActivity(d, "ACT-1") || hasActivity(d, "ACT-9") {
		t.Error("hasActivity is wrong")
	}
}
