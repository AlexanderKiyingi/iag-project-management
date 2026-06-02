package models

// Document is the persisted workspace blob (matches pm frontend Persisted type).
type Document struct {
	Tasks                []Task                 `json:"tasks"`
	Projects             map[string]Project     `json:"projects"`
	Members              []Member               `json:"members"`
	Goals                []Goal                 `json:"goals"`
	Sprints              []Sprint               `json:"sprints"`
	Requisitions         []Requisition          `json:"requisitions"`
	Chats                []Chat                 `json:"chats"`
	Messages             []Message              `json:"messages"`
	Notifications        []WorkspaceNotification `json:"notifications"`
	SavedViews           []SavedView            `json:"savedViews"`
	Audit                []AuditEntry           `json:"audit"`
	Files                []WorkspaceFile        `json:"files"`
	TaskComments         []TaskComment          `json:"taskComments"`
	Subtasks             []Subtask              `json:"subtaskEntities,omitempty"`
	Sections             []Section              `json:"sectionEntities,omitempty"`
	EntityComments       []EntityComment        `json:"entityComments,omitempty"`
	Templates            []Template             `json:"templates,omitempty"`
	Rules                []Rule                 `json:"rules,omitempty"`
	TimeEntries          []TimeEntry            `json:"timeEntries,omitempty"`
	Portfolios           []Portfolio            `json:"portfolios,omitempty"`
	Forms                []Form                 `json:"forms,omitempty"`
	Webhooks             []WebhookSubscription  `json:"webhooks,omitempty"`
	TaskCustomFieldDefs  []TaskCustomFieldDef   `json:"taskCustomFieldDefs"`
	TaskListColumns      map[string]bool        `json:"taskListColumns"`
	SidebarCollapsed     bool                   `json:"sidebarCollapsed"`
	SidebarProjectsOpen  bool                   `json:"sidebarProjectsOpen"`
	SidebarSavedViewsOpen bool                  `json:"sidebarSavedViewsOpen"`
	DesktopNotificationsEnabled bool            `json:"desktopNotificationsEnabled"`
	Theme                string                 `json:"theme"`
	OrgID                string                 `json:"orgId,omitempty"`
}

type Project struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Color         string                `json:"color"`
	Icon          string                `json:"icon"`
	Status        string                `json:"status"`
	Code          string                `json:"code"`
	StatusHistory []ProjectStatusUpdate `json:"statusHistory,omitempty"`
	// Visibility controls which workspace members can see the project
	// in the document. Empty string defaults to ProjectVisibilityWorkspace
	// (everyone in the workspace, the legacy behaviour). When
	// ProjectVisibilityMembersOnly, only users whose userId appears in
	// MemberIDs see the project; tasks anchored to a hidden project are
	// filtered out of GET /workspace responses too.
	Visibility    string                `json:"visibility,omitempty"`
	MemberIDs     []string              `json:"memberIds,omitempty"`
}

const (
	ProjectVisibilityWorkspace   = "workspace"
	ProjectVisibilityMembersOnly = "members_only"
)

// ProjectStatusUpdate is a single point-in-time status report posted
// against a project. Newest entries are at the tail of StatusHistory so
// the legacy Project.Status mirror always reflects the most recent
// update.
type ProjectStatusUpdate struct {
	ID      int    `json:"id"`
	Author  string `json:"author"`
	Status  string `json:"status"` // on_track | at_risk | off_track
	Summary string `json:"summary"`
	Time    string `json:"time"`
}

const (
	ProjectStatusOnTrack  = "on_track"
	ProjectStatusAtRisk   = "at_risk"
	ProjectStatusOffTrack = "off_track"
)

type Task struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Project      string            `json:"project"`
	// Projects holds every project the task is multi-homed in. The
	// legacy `Project` field stays in sync with Projects[0] on
	// every mutation so older frontends keep working unchanged.
	Projects     []string          `json:"projects,omitempty"`
	Section      string            `json:"section"`
	SectionID    int               `json:"sectionId,omitempty"`
	Assignee     string            `json:"assignee"`
	Due          string            `json:"due"`
	StartDate    string            `json:"startDate,omitempty"`
	EndDate      string            `json:"endDate,omitempty"`
	Priority     string            `json:"priority"`
	Status       string            `json:"status"`
	Done         bool              `json:"done"`
	Desc         string            `json:"desc"`
	// Type discriminates rendering. Empty string and "task" both mean a
	// normal task; "milestone" is a point-in-time marker (renders without
	// a duration in timeline/Gantt views even if startDate is set).
	Type         string            `json:"type,omitempty"`
	Subtasks     []string          `json:"subtasks"`
	Tags         []string          `json:"tags"`
	DependsOn    []int             `json:"dependsOn"`
	SprintID     int               `json:"sprintId"`
	CustomValues map[string]string `json:"customValues"`
	Activity     []ActivityEntry   `json:"activity"`
	DeletedAt    string            `json:"deletedAt,omitempty"`
	// Approval workflow (Task.Type == TaskTypeApproval). Approvers
	// names them; ApprovedBy records who has signed off so far. The
	// task only flips Done=true when len(ApprovedBy) >= len(Approvers).
	Approvers    []string          `json:"approvers,omitempty"`
	ApprovedBy   []string          `json:"approvedBy,omitempty"`
	// Recurrence is the optional rrule subset for Phase 4.2; when set
	// the recurrence job clones the task on schedule.
	Recurrence   *TaskRecurrence   `json:"recurrence,omitempty"`
}

// TaskRecurrence is a minimal recurrence spec. Pattern is one of
// "daily" | "weekly" | "monthly". Interval is the every-N step;
// EndDate stops the chain when set. NextDueAt is the next instance
// the recurrence job is expected to create (server-managed; clients
// should not set this).
type TaskRecurrence struct {
	Pattern   string `json:"pattern"`
	Interval  int    `json:"interval"`
	EndDate   string `json:"endDate,omitempty"`
	NextDueAt string `json:"nextDueAt,omitempty"`
}

const (
	TaskTypeTask      = "task"
	TaskTypeMilestone = "milestone"
	TaskTypeApproval  = "approval"
)

type ActivityEntry struct {
	Text string `json:"text"`
	Time string `json:"time"`
}

type Goal struct {
	ID          int          `json:"id"`
	Name        string       `json:"name"`
	Progress    int          `json:"progress"`
	Status      string       `json:"status"`
	Period      string       `json:"period"`
	Team        string       `json:"team"`
	KeyResults  []KeyResult  `json:"keyResults,omitempty"`
}

// KeyResult is an OKR-style measurable outcome attached to a Goal.
// When a Goal has any KeyResults, the parent Goal.Progress is recomputed
// as the average of each KR's percentage (current / target).
type KeyResult struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Metric  string  `json:"metric,omitempty"`
	Current float64 `json:"current"`
	Target  float64 `json:"target"`
	Unit    string  `json:"unit,omitempty"`
}

type Sprint struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Goal      string `json:"goal"`
	Status    string `json:"status"`
}

type Requisition struct {
	ID            int                 `json:"id"`
	Title         string              `json:"title"`
	Amount        float64             `json:"amount"`
	Currency      string              `json:"currency"`
	Payee         string              `json:"payee"`
	Justification string              `json:"justification"`
	RequestedBy   string              `json:"requestedBy"`
	ForDept       string              `json:"forDept"`
	Urgency       string              `json:"urgency"`
	Status        string              `json:"status"`
	ApprovedBy    *string             `json:"approvedBy"`
	PaidAt        *string             `json:"paidAt"`
	Attachments   []int               `json:"attachments"`
	Comments      []RequisitionComment `json:"comments"`
	CreatedAt     string              `json:"createdAt"`
	SubmittedAt   *string             `json:"submittedAt,omitempty"`
	ApprovedAt    *string             `json:"approvedAt,omitempty"`
	RejectedAt    *string             `json:"rejectedAt,omitempty"`
}

type RequisitionComment struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	Time   string `json:"time"`
}

type Chat struct {
	ID        int      `json:"id"`
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Members   []string `json:"members"`
	CreatedBy string   `json:"createdBy"`
	CreatedAt string   `json:"createdAt"`
	Muted     bool     `json:"muted"`
	Desc      string   `json:"desc,omitempty"`
}

type Message struct {
	ID          int                    `json:"id"`
	ChatID      int                    `json:"chatId"`
	Author      string                 `json:"author"`
	Text        string                 `json:"text"`
	ReplyTo     *int                   `json:"replyTo"`
	Time        string                 `json:"time"`
	Reactions   map[string][]string    `json:"reactions"`
	Edited      bool                   `json:"edited"`
	Deleted     bool                   `json:"deleted"`
	ReadBy      []string               `json:"readBy"`
	Attachments []MessageAttachmentRef `json:"attachments"`
	ReminderAt  *string                `json:"reminderAt"`
}

type MessageAttachmentRef struct {
	FileID    int    `json:"fileId"`
	Name      string `json:"name"`
	SizeLabel string `json:"sizeLabel"`
}

type WorkspaceNotification struct {
	ID       int     `json:"id"`
	Icon     string  `json:"icon"`
	Title    string  `json:"title"`
	Meta     string  `json:"meta"`
	Body     string  `json:"body"`
	Read     bool    `json:"read"`
	Archived bool    `json:"archived"`
	Mention  bool    `json:"mention"`
	Target   *string `json:"target"`
}

type SavedView struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Icon    string            `json:"icon"`
	Filters map[string]string `json:"filters"`
	SortBy  string            `json:"sortBy"`
}

type AuditEntry struct {
	ID     int    `json:"id"`
	Type   string `json:"type"`
	Actor  string `json:"actor"`
	TaskID *int   `json:"taskId"`
	Text   string `json:"text"`
	Time   string `json:"time"`
}

type WorkspaceFile struct {
	ID      int    `json:"id"`
	N       string `json:"n"`
	S       string `json:"s"`
	T       string `json:"t"`
	I       string `json:"i"`
	D       string `json:"d"`
	Project string `json:"project"`
	Data    string `json:"data"`
}

type TaskComment struct {
	ID       int      `json:"id"`
	TaskID   int      `json:"taskId"`
	Author   string   `json:"author"`
	Text     string   `json:"text"`
	Mentions []string `json:"mentions"`
	Time     string   `json:"time"`
}

// EntityComment is the polymorphic comment for non-task entities
// (projects, goals, sprints). EntityID is a string to accommodate both
// string-keyed projects and integer-keyed goals/sprints — handlers
// validate the id shape per EntityType before persisting.
type EntityComment struct {
	ID         int      `json:"id"`
	EntityType string   `json:"entityType"`
	EntityID   string   `json:"entityId"`
	Author     string   `json:"author"`
	Text       string   `json:"text"`
	Mentions   []string `json:"mentions,omitempty"`
	Time       string   `json:"time"`
}

const (
	EntityCommentProject = "project"
	EntityCommentGoal    = "goal"
	EntityCommentSprint  = "sprint"
)

// Template stores a reusable snippet of workspace data that can be
// applied to a workspace to materialize the entities it describes.
// Type discriminates the shape of Body: "project" templates carry a
// Project + Sections + Tasks; "task" templates carry a single Task
// (with optional subtasks). Variables holds {{placeholder}} names the
// frontend prompts the user to fill in at apply time; the values are
// substituted into string fields of Body when POST /templates/:id/apply
// is called.
type Template struct {
	ID        int            `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Variables []string       `json:"variables,omitempty"`
	Body      map[string]any `json:"body"`
	CreatedAt string         `json:"createdAt"`
	UpdatedAt string         `json:"updatedAt"`
}

const (
	TemplateTypeProject = "project"
	TemplateTypeTask    = "task"
)

// Rule is a single automation entry: when a Trigger fires and all
// Conditions match, every Action runs. Actions execute in the same
// transaction as the mutation that fired the trigger, so the
// resulting state is atomic.
type Rule struct {
	ID         int             `json:"id"`
	Name       string          `json:"name"`
	Enabled    bool            `json:"enabled"`
	Trigger    string          `json:"trigger"`
	Conditions []RuleCondition `json:"conditions,omitempty"`
	Actions    []RuleAction    `json:"actions"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

// RuleCondition compares one field against one value. Field is a dotted
// path like "task.status" or "task.project". Op is one of: eq, ne,
// contains, in.
type RuleCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// RuleAction is one operation applied when the rule fires. Type is one
// of: assign_to, set_status, set_due_offset, add_tag, create_subtask,
// post_comment, notify. Params holds the action-specific values.
type RuleAction struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params,omitempty"`
}

const (
	TriggerTaskCreated         = "task.created"
	TriggerTaskStatusChanged   = "task.status_changed"
	TriggerTaskAssigneeChanged = "task.assignee_changed"
	TriggerCommentCreated      = "comment.created"

	ActionAssignTo      = "assign_to"
	ActionSetStatus     = "set_status"
	ActionSetDueOffset  = "set_due_offset"
	ActionAddTag        = "add_tag"
	ActionCreateSubtask = "create_subtask"
	ActionPostComment   = "post_comment"
	ActionNotify        = "notify"

	OpEq       = "eq"
	OpNe       = "ne"
	OpContains = "contains"
	OpIn       = "in"
)

// TimeEntry tracks one work session against a task. EndedAt is empty
// while the timer is running; clients call /tasks/:id/time/stop to
// close the entry. DurationSec is server-computed on stop so the
// client doesn't have to.
type TimeEntry struct {
	ID          int    `json:"id"`
	TaskID      int    `json:"taskId"`
	UserID      string `json:"userId"`
	StartedAt   string `json:"startedAt"`
	EndedAt     string `json:"endedAt,omitempty"`
	DurationSec int64  `json:"durationSec"`
	Note        string `json:"note,omitempty"`
}

// Portfolio is a personal collection of project IDs with rollup stats
// computed on read.
type Portfolio struct {
	ID          int      `json:"id"`
	OwnerUserID string   `json:"ownerUserId"`
	Name        string   `json:"name"`
	ProjectIDs  []string `json:"projectIds"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
}

// Form is an intake form whose submission creates a Task in the
// configured ProjectID. Public forms can be submitted without auth
// via POST /public/forms/:slug.
type Form struct {
	ID          int         `json:"id"`
	Slug        string      `json:"slug"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Fields      []FormField `json:"fields"`
	ProjectID   string      `json:"projectId,omitempty"`
	Public      bool        `json:"public"`
	CreatedAt   string      `json:"createdAt"`
	UpdatedAt   string      `json:"updatedAt"`
}

// FormField describes one form input. Type is one of: text, longtext,
// number, date, select. Options applies to select.
type FormField struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// WebhookSubscription holds a user's request to be POSTed when given
// event types occur. Secret is used to HMAC-sign the payload so the
// receiver can verify authenticity. FailureCount lets the retry job
// disable subscriptions that look dead.
type WebhookSubscription struct {
	ID            int      `json:"id"`
	OwnerUserID   string   `json:"ownerUserId"`
	URL           string   `json:"url"`
	Secret        string   `json:"secret"`
	Events        []string `json:"events"`
	Active        bool     `json:"active"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	LastDelivered string   `json:"lastDelivered,omitempty"`
	FailureCount  int      `json:"failureCount,omitempty"`
}

// Section is an ordered group of tasks within a project. The legacy
// Task.Section string field is kept as a name mirror so existing
// frontends keep working; section CRUD updates both. Future task
// patches that include sectionId override the legacy string.
type Section struct {
	ID        int    `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Order     int    `json:"order"`
}

// Subtask is a first-class entity belonging to a parent Task. The legacy
// Task.Subtasks []string field is kept as a name-only mirror so existing
// frontends keep working; create/delete handlers update both.
type Subtask struct {
	ID           int    `json:"id"`
	ParentTaskID int    `json:"parentTaskId"`
	Name         string `json:"name"`
	Assignee     string `json:"assignee,omitempty"`
	Due          string `json:"due,omitempty"`
	Done         bool   `json:"done"`
	Order        int    `json:"order"`
}

type TaskCustomFieldDef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
}

const (
	CustomFieldTypeText        = "text"
	CustomFieldTypeNumber      = "number"
	CustomFieldTypeDate        = "date"
	CustomFieldTypeSelect      = "select"
	CustomFieldTypeMultiSelect = "multi_select"
	CustomFieldTypePeople      = "people"
)

// ValidCustomFieldType reports whether t is one of the recognised
// CustomFieldType* constants. The empty string is treated as "text" by
// callers (back-compat with legacy untyped fields).
func ValidCustomFieldType(t string) bool {
	switch t {
	case "",
		CustomFieldTypeText,
		CustomFieldTypeNumber,
		CustomFieldTypeDate,
		CustomFieldTypeSelect,
		CustomFieldTypeMultiSelect,
		CustomFieldTypePeople:
		return true
	default:
		return false
	}
}

type Member struct {
	Initials   string  `json:"initials"`
	Name       string  `json:"name"`
	Email      string  `json:"email,omitempty"`
	UserID     string  `json:"userId,omitempty"` // canonical auth identifier; preferred match key for upstream events
	Role       string  `json:"role"`
	// AccessRole (owner|editor|viewer) is UI/sync metadata; API auth uses JWT pm.* permissions.
	AccessRole string  `json:"accessRole"`
	Color      string  `json:"color"`
	Tasks      int     `json:"tasks"`
	Wl         int     `json:"wl"`
	Team       string  `json:"team"`
	Manager    *string `json:"manager"`
	Active     *bool   `json:"active,omitempty"`
}
