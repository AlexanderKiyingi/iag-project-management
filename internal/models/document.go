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
}

const (
	TaskTypeTask      = "task"
	TaskTypeMilestone = "milestone"
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
	AccessRole string  `json:"accessRole"`
	Color      string  `json:"color"`
	Tasks      int     `json:"tasks"`
	Wl         int     `json:"wl"`
	Team       string  `json:"team"`
	Manager    *string `json:"manager"`
	Active     *bool   `json:"active,omitempty"`
}
