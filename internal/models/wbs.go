package models

// Work breakdown structure: Phase → Activity → Work program.
//
// These are deliberately NOT tasks. `Task` is one flat list with a free-text
// `Section` and a `Type` discriminator, which is the right shape for a
// workspace/Asana project. A construction project has a real three-level
// breakdown where each level is scheduled and progressed in its own right, and
// folding all three into the task list makes them overwrite one another — a
// phase, the activity under it, and the work program under that would all be
// tasks differing only by a string.
//
// Each level carries its own dates, percent progress and status so a phase can
// be reported on without walking its children, which is how the schedule is
// actually read.
//
// Like every other collection here these live in the workspace JSON document,
// so adding them needs no migration.

// Phase is the top level: a stage of the works ("Substructure", "Fit-out").
//
// SortOrder, Budget and Currency are here because the Project Manager phase
// form captures them and its list renders them — a phase budget is a normal
// breakdown of the contract value in construction, not a second source of truth
// for it.
type Phase struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Code      string `json:"code,omitempty"`
	SortOrder int    `json:"sortOrder"`
	StartDate string `json:"startDate,omitempty"`
	DueDate   string `json:"dueDate,omitempty"`
	Budget    int64  `json:"budget"`
	Currency  string `json:"currency,omitempty"`
	Progress  int    `json:"progress"`
	Status    string `json:"status"`
	Desc      string `json:"desc,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// Activity is a unit of work inside a phase ("Excavation", "Blockwork").
type Activity struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	PhaseID   string `json:"phaseId,omitempty"`
	Name      string `json:"name"`
	Code      string `json:"code,omitempty"`
	// Assignee is the person the activity list renders a column for. Free text
	// rather than a user id: the same column already carries names typed on
	// site, and coercing those into ids would drop them.
	Assignee  string `json:"assignee,omitempty"`
	StartDate string `json:"startDate,omitempty"`
	DueDate   string `json:"dueDate,omitempty"`
	Progress  int    `json:"progress"`
	Status    string `json:"status"`
	Desc      string `json:"desc,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// WorkProgram is a scheduled, assigned piece of an activity — the row a site
// team works to for a given week.
type WorkProgram struct {
	ID             string `json:"id"`
	ProjectID      string `json:"projectId"`
	PhaseID        string `json:"phaseId,omitempty"`
	ActivityID     string `json:"activityId,omitempty"`
	Name           string `json:"name"`
	ScheduledStart string `json:"scheduledStart,omitempty"`
	ScheduledEnd   string `json:"scheduledEnd,omitempty"`
	AssignedTo     string `json:"assignedTo,omitempty"`
	Status         string `json:"status"`
	Desc           string `json:"desc,omitempty"`
	CreatedAt      string `json:"createdAt,omitempty"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
}

// Each level has its own closed set, spelled exactly as the Project Manager
// frontend spells it. A shared set was the first shape here and it was wrong
// twice over: a phase is never "Blocked" and an activity is never "On hold", and
// any vocabulary the only client does not send is a 400 waiting to happen.
//
// Closed rather than free-text because these drive the schedule roll-up: one
// row spelling it "in progress" makes a phase's completion count wrong, and
// nothing surfaces that.
var (
	PhaseStatuses       = []string{"Planned", "In progress", "Completed", "On hold", "Cancelled"}
	ActivityStatuses    = []string{"Todo", "In progress", "Blocked", "Done", "Cancelled"}
	WorkProgramStatuses = []string{"Planned", "Scheduled", "In progress", "Completed", "Cancelled"}
)

// Default status for a row created without one.
const (
	PhaseStatusDefault       = "Planned"
	ActivityStatusDefault    = "Todo"
	WorkProgramStatusDefault = "Planned"
)

// ValidStatus reports whether s is in the given set. An empty status is
// accepted and defaulted by the caller, so a client that does not set one is
// not rejected.
func ValidStatus(s string, set []string) bool {
	if s == "" {
		return true
	}
	for _, candidate := range set {
		if candidate == s {
			return true
		}
	}
	return false
}

// ClampProgress keeps percent progress inside 0-100. Out-of-range values come
// from spreadsheets often enough that rejecting the whole write is worse than
// clamping.
func ClampProgress(p int) int {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
