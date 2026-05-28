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
	ID     string `json:"id"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Icon   string `json:"icon"`
	Status string `json:"status"`
	Code   string `json:"code"`
}

type Task struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Project      string            `json:"project"`
	Section      string            `json:"section"`
	Assignee     string            `json:"assignee"`
	Due          string            `json:"due"`
	Priority     string            `json:"priority"`
	Status       string            `json:"status"`
	Done         bool              `json:"done"`
	Desc         string            `json:"desc"`
	Subtasks     []string          `json:"subtasks"`
	Tags         []string          `json:"tags"`
	DependsOn    []int             `json:"dependsOn"`
	SprintID     int               `json:"sprintId"`
	CustomValues map[string]string `json:"customValues"`
	Activity     []ActivityEntry   `json:"activity"`
}

type ActivityEntry struct {
	Text string `json:"text"`
	Time string `json:"time"`
}

type Goal struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Progress int    `json:"progress"`
	Status   string `json:"status"`
	Period   string `json:"period"`
	Team     string `json:"team"`
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

type TaskCustomFieldDef struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
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
