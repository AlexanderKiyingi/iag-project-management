package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/google/uuid"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/chat"
	"github.com/iag/project-management/backend/internal/consumer"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/mentions"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/usersclient"
	"github.com/iag/project-management/backend/internal/workspace"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

type Entities struct {
	Svc   *workspace.Service
	Files *files.Store
	Users *usersclient.Client
	Chat  *chat.Service
}

func (h *Entities) Register(rg *gin.RouterGroup) {
	authz := auth.RequireWorkspaceWrite()
	rg.DELETE("/audit", authz, h.deleteAudit)
	rg.PATCH("/settings", authz, h.patchSettings)

	rg.GET("/tasks", auth.RequireWorkspaceRead(), h.listTasksPaged)
	rg.GET("/messages", auth.RequireWorkspaceRead(), h.listMessagesPaged)
	rg.POST("/tasks", authz, h.createTask)
	rg.PATCH("/tasks/:id", authz, h.patchTask)
	rg.POST("/tasks/bulk", authz, h.createTasksBulk)
	rg.POST("/tasks/bulk-patch", authz, h.patchTasksBulk)
	rg.POST("/tasks/delete-batch", authz, h.deleteTasksBatch)
	rg.DELETE("/tasks/:id", authz, h.deleteTask)
	rg.POST("/tasks/:id/restore", authz, h.restoreTask)
	rg.POST("/tasks/:id/projects", authz, h.addTaskProject)
	rg.DELETE("/tasks/:id/projects/:projectId", authz, h.removeTaskProject)
	rg.POST("/tasks/:id/approve", authz, h.approveTask)
	rg.POST("/tasks/:id/reject", authz, h.rejectTask)
	rg.PATCH("/tasks/:id/approvers", authz, h.setApprovers)

	rg.GET("/templates", auth.RequireWorkspaceRead(), h.listTemplates)
	rg.GET("/templates/:id", auth.RequireWorkspaceRead(), h.getTemplate)
	rg.POST("/templates", authz, h.createTemplate)
	rg.DELETE("/templates/:id", authz, h.deleteTemplate)
	rg.POST("/templates/:id/apply", authz, h.applyTemplate)

	rg.GET("/rules", auth.RequireWorkspaceRead(), h.listRules)
	rg.POST("/rules", authz, h.createRule)
	rg.PATCH("/rules/:id", authz, h.patchRule)
	rg.DELETE("/rules/:id", authz, h.deleteRule)

	rg.POST("/tasks/:id/time/start", authz, h.startTimer)
	rg.POST("/tasks/:id/time/stop", authz, h.stopTimer)
	rg.GET("/tasks/:id/time", auth.RequireWorkspaceRead(), h.listTaskTime)
	rg.GET("/users/:id/time", auth.RequireWorkspaceRead(), h.listUserTime)

	rg.GET("/portfolios", auth.RequireWorkspaceRead(), h.listPortfolios)
	rg.GET("/portfolios/:id", auth.RequireWorkspaceRead(), h.getPortfolio)
	rg.POST("/portfolios", authz, h.createPortfolio)
	rg.PATCH("/portfolios/:id", authz, h.patchPortfolio)
	rg.DELETE("/portfolios/:id", authz, h.deletePortfolio)

	rg.GET("/forms", auth.RequireWorkspaceRead(), h.listForms)
	rg.POST("/forms", authz, h.createForm)
	rg.PATCH("/forms/:id", authz, h.patchForm)
	rg.DELETE("/forms/:id", authz, h.deleteForm)

	rg.GET("/reports/workload", auth.RequireWorkspaceRead(), h.reportWorkload)
	rg.GET("/reports/throughput", auth.RequireWorkspaceRead(), h.reportThroughput)
	rg.GET("/reports/status-rollup", auth.RequireWorkspaceRead(), h.reportStatusRollup)
	rg.GET("/reports/burndown/:id", auth.RequireWorkspaceRead(), h.reportBurndown)

	rg.GET("/webhooks", auth.RequireWorkspaceRead(), h.listWebhooks)
	rg.POST("/webhooks", authz, h.createWebhook)
	rg.PATCH("/webhooks/:id", authz, h.patchWebhook)
	rg.DELETE("/webhooks/:id", authz, h.deleteWebhook)
	rg.POST("/tasks/:id/tags", authz, h.addTaskTag)
	rg.DELETE("/tasks/:id/tags/:tag", authz, h.removeTaskTag)
	rg.PATCH("/tasks/:id/custom/:field", authz, h.patchTaskCustom)
	rg.PUT("/custom-fields/:id", authz, h.putCustomField)
	rg.DELETE("/custom-fields/:id", authz, h.deleteCustomField)
	rg.POST("/tasks/:id/deps", authz, h.addTaskDep)
	rg.DELETE("/tasks/:id/deps/:depId", authz, h.removeTaskDep)
	rg.POST("/tasks/:id/comments", authz, h.addTaskComment)
	rg.DELETE("/comments/:id", authz, h.deleteTaskComment)
	rg.POST("/projects/:id/comments", authz, h.addEntityCommentProject)
	rg.POST("/goals/:id/comments", authz, h.addEntityCommentGoal)
	rg.POST("/sprints/:id/comments", authz, h.addEntityCommentSprint)
	rg.DELETE("/entity-comments/:id", authz, h.deleteEntityComment)
	rg.POST("/tasks/:id/subtasks", authz, h.createSubtask)
	rg.POST("/tasks/:id/subtasks/reorder", authz, h.reorderSubtasks)
	rg.PATCH("/subtasks/:id", authz, h.patchSubtask)
	rg.DELETE("/subtasks/:id", authz, h.deleteSubtask)

	rg.POST("/goals", authz, h.createGoal)
	rg.PATCH("/goals/:id", authz, h.patchGoal)
	rg.DELETE("/goals/:id", authz, h.deleteGoal)
	rg.POST("/goals/:id/progress", authz, h.goalProgress)
	rg.POST("/goals/:id/key-results", authz, h.addKeyResult)
	rg.PATCH("/goals/:id/key-results/:krId", authz, h.patchKeyResult)
	rg.DELETE("/goals/:id/key-results/:krId", authz, h.deleteKeyResult)

	rg.POST("/sprints", authz, h.createSprint)
	rg.PATCH("/sprints/:id", authz, h.patchSprint)
	rg.DELETE("/sprints/:id", authz, h.deleteSprint)

	rg.POST("/chats", authz, h.createChat)
	rg.POST("/chats/:id/read", authz, h.chatRead)
	rg.POST("/chats/:id/mute", authz, h.chatMute)
	rg.POST("/messages", authz, h.postMessage)
	rg.POST("/messages/:id/reactions", authz, h.addMessageReaction)
	rg.DELETE("/messages/:id/reactions/:emoji", authz, h.removeMessageReaction)

	rg.POST("/files", authz, h.createFile)
	rg.GET("/files/:id", auth.RequireWorkspaceRead(), h.getFile)
	rg.PUT("/projects/:id", authz, h.putProject)
	rg.GET("/projects", auth.RequireWorkspaceRead(), h.listProjects)
	rg.DELETE("/projects/:id", authz, h.deleteProject)
	rg.POST("/projects/:id/status", authz, h.postProjectStatus)
	rg.GET("/projects/:id/status", auth.RequireWorkspaceRead(), h.listProjectStatus)
	rg.POST("/projects/:id/sections", authz, h.createSection)
	rg.POST("/projects/:id/sections/reorder", authz, h.reorderSections)
	rg.PATCH("/sections/:id", authz, h.patchSection)
	rg.DELETE("/sections/:id", authz, h.deleteSection)
	rg.POST("/requisitions", authz, h.createRequisition)
	rg.GET("/requisitions", auth.RequireWorkspaceRead(), h.listRequisitions)

	rg.POST("/workspace/members", authz, auth.RequirePerm("pm.admin"), h.addMember)
	rg.PATCH("/workspace/org", authz, auth.RequirePerm("pm.admin"), h.setOrg)
	rg.GET("/workspace/org", auth.RequireWorkspaceRead(), h.getWorkspaceOrg)
	rg.GET("/orgs", auth.RequireWorkspaceRead(), h.listOrgs)
	rg.GET("/orgs/:orgId/members", auth.RequireWorkspaceRead(), h.listOrgMembers)
	rg.GET("/workspace/workload", auth.RequireWorkspaceRead(), h.workspaceWorkload)

	h.registerProcurementRoutes(rg, authz)
}

func (h *Entities) deleteAudit(c *gin.Context) {
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		d.Audit = nil
		if actor != "" {
			models.AppendAudit(d, actor, "audit.cleared", "cleared audit log", nil)
		}
		return nil
	})
}

func (h *Entities) patchSettings(c *gin.Context) {
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		if v, ok := patch["theme"].(string); ok && (v == "light" || v == "dark") {
			d.Theme = v
		}
		if v, ok := patch["sidebarCollapsed"].(bool); ok {
			d.SidebarCollapsed = v
		}
		if v, ok := patch["sidebarProjectsOpen"].(bool); ok {
			d.SidebarProjectsOpen = v
		}
		if v, ok := patch["sidebarSavedViewsOpen"].(bool); ok {
			d.SidebarSavedViewsOpen = v
		}
		if v, ok := patch["desktopNotificationsEnabled"].(bool); ok {
			d.DesktopNotificationsEnabled = v
		}
		if raw, ok := patch["savedViews"]; ok {
			b, err := json.Marshal(raw)
			if err == nil {
				var views []models.SavedView
				if err := json.Unmarshal(b, &views); err == nil {
					d.SavedViews = views
				}
			}
		}
		return nil
	})
}

func (h *Entities) createTask(c *gin.Context) {
	var in struct {
		Name      string `json:"name"`
		Desc      string `json:"desc"`
		Project   string `json:"project"`
		Section   string `json:"section"`
		Assignee  string `json:"assignee"`
		Priority  string `json:"priority"`
		Due       string `json:"due"`
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
		Type      string `json:"type"`
		SprintID  *int   `json:"sprintId"`
	}
	if err := bindJSONCoerced(c, &in); err != nil || in.Name == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	var created models.Task
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		sprintID := 0
		if in.SprintID != nil {
			sprintID = *in.SprintID
		}
		created = models.Task{
			ID:        models.NextTaskID(d),
			Name:      in.Name,
			Project:   in.Project,
			Section:   in.Section,
			Assignee:  in.Assignee,
			Priority:  normalizeTaskPriority(in.Priority),
			Due:       in.Due,
			StartDate: in.StartDate,
			EndDate:   in.EndDate,
			Type:      normalizeTaskType(in.Type),
			Status:    "on_track",
			Tags:      []string{},
			DependsOn: []int{},
			SprintID:  sprintID,
			CustomValues: map[string]string{},
			Activity:  []models.ActivityEntry{},
		}
		normalizeTaskProjects(&created)
		if in.Desc != "" {
			created.Desc = in.Desc
		}
		d.Tasks = append(d.Tasks, created)
		tid := created.ID
		models.AppendAudit(d, actor, "task.created", fmt.Sprintf("created task %q", in.Name), &tid)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() && in.Assignee != "" {
		claims, _ := middleware.PlatformClaims(c)
		email := ""
		if claims != nil {
			email = claims.Email
		}
		publishTaskAssigned(c.Request.Context(), h.Svc.Events, h.Svc.Repo, created, actor, email)
	}
	c.JSON(http.StatusOK, gin.H{"task": created, "version": ws.Version})
}

func (h *Entities) patchTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	var prevAssignee string
	var updated models.Task
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			prevAssignee = d.Tasks[i].Assignee
			applyTaskPatch(&d.Tasks[i], patch)
			updated = d.Tasks[i]
			return nil
		}
		return fmt.Errorf("task not found")
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() &&
		strings.TrimSpace(updated.Assignee) != "" &&
		updated.Assignee != prevAssignee {
		claims, _ := middleware.PlatformClaims(c)
		email := ""
		if claims != nil {
			email = claims.Email
		}
		publishTaskAssigned(c.Request.Context(), h.Svc.Events, h.Svc.Repo, updated, actor, email)
	}
	respondWorkspace(c, ws)
}

func applyTaskPatch(t *models.Task, patch map[string]any) {
	if v, ok := patch["name"].(string); ok {
		t.Name = v
	}
	if v, ok := patch["desc"].(string); ok {
		t.Desc = v
	}
	if v, ok := patch["assignee"].(string); ok {
		t.Assignee = v
	}
	if v, ok := patch["due"].(string); ok {
		t.Due = v
	}
	if v, ok := patch["startDate"].(string); ok {
		t.StartDate = v
	}
	if v, ok := patch["endDate"].(string); ok {
		t.EndDate = v
	}
	if v, ok := patch["priority"].(string); ok {
		t.Priority = normalizeTaskPriority(v)
	}
	if v, ok := patch["project"].(string); ok {
		t.Project = v
		t.Projects = nil
		normalizeTaskProjects(t)
	}
	if raw, ok := patch["projects"]; ok {
		if arr, ok := raw.([]any); ok {
			ps := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					ps = append(ps, s)
				}
			}
			t.Projects = ps
			normalizeTaskProjects(t)
		}
	}
	if v, ok := patch["section"].(string); ok {
		t.Section = v
	}
	if v, ok := patch["sectionId"].(float64); ok {
		t.SectionID = int(v)
	}
	if v, ok := patch["status"].(string); ok {
		t.Status = v
	}
	if v, ok := patch["done"].(bool); ok {
		t.Done = v
	}
	if v, ok := patch["sprintId"].(float64); ok {
		t.SprintID = int(v)
	}
	if v, ok := patch["type"].(string); ok {
		t.Type = normalizeTaskType(v)
	}
	if raw, ok := patch["recurrence"]; ok {
		applyRecurrencePatch(t, raw)
	}
}

// applyRecurrencePatch accepts either a nil to clear the recurrence
// spec or a map carrying {pattern, interval, endDate}. Server-managed
// fields (nextDueAt) are recomputed from the task's due date when
// missing so callers don't have to supply it.
func applyRecurrencePatch(t *models.Task, raw any) {
	if raw == nil {
		t.Recurrence = nil
		return
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	pattern, _ := m["pattern"].(string)
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	switch pattern {
	case "daily", "weekly", "monthly":
	default:
		t.Recurrence = nil
		return
	}
	interval := 1
	if v, ok := m["interval"].(float64); ok && int(v) > 0 {
		interval = int(v)
	}
	rec := &models.TaskRecurrence{Pattern: pattern, Interval: interval}
	if v, ok := m["endDate"].(string); ok {
		rec.EndDate = strings.TrimSpace(v)
	}
	if v, ok := m["nextDueAt"].(string); ok && v != "" {
		rec.NextDueAt = v
	} else {
		rec.NextDueAt = firstNonEmptyString(t.EndDate, t.Due, t.StartDate)
	}
	t.Recurrence = rec
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// normalizeTaskType accepts the supported task-type literals and falls
// back to an empty string (treated as TaskTypeTask by callers) for
// anything else. Empty input stays empty so that absence isn't coerced
// into a value when patching.
func normalizeTaskType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "":
		return ""
	case models.TaskTypeMilestone:
		return models.TaskTypeMilestone
	case models.TaskTypeApproval:
		return models.TaskTypeApproval
	default:
		return models.TaskTypeTask
	}
}

// normalizeTaskPriority coerces free-form priority input to the lowercase set
// the task board/report rollups group on (low/medium/high), so mixed-casing or
// junk from the client can't fragment those groupings. Empty is preserved
// (unprioritised); any other unknown value defaults to medium.
func normalizeTaskPriority(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "":
		return ""
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	default:
		return "medium"
	}
}

type bulkTaskInput struct {
	Name      string `json:"name"`
	Desc      string `json:"desc"`
	Project   string `json:"project"`
	Section   string `json:"section"`
	Assignee  string `json:"assignee"`
	Priority  string `json:"priority"`
	Due       string `json:"due"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Type      string `json:"type"`
	SprintID  *int   `json:"sprintId"`
}

func (h *Entities) createTasksBulk(c *gin.Context) {
	var body struct {
		Tasks []bulkTaskInput `json:"tasks"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Tasks) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	for _, in := range body.Tasks {
		if strings.TrimSpace(in.Name) == "" {
			apierr.JSONStatus(c, http.StatusBadRequest, "every task requires a name")
			return
		}
	}
	actor := c.GetHeader("X-Workspace-User")
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	created := make([]models.Task, 0, len(body.Tasks))
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		created = created[:0]
		for _, in := range body.Tasks {
			sprintID := 0
			if in.SprintID != nil {
				sprintID = *in.SprintID
			}
			t := models.Task{
				ID:           models.NextTaskID(d),
				Name:         in.Name,
				Desc:         in.Desc,
				Project:      in.Project,
				Section:      in.Section,
				Assignee:     in.Assignee,
				Priority:     in.Priority,
				Due:          in.Due,
				StartDate:    in.StartDate,
				EndDate:      in.EndDate,
				Type:         normalizeTaskType(in.Type),
				Status:       "on_track",
				Tags:         []string{},
				DependsOn:    []int{},
				SprintID:     sprintID,
				CustomValues: map[string]string{},
				Activity:     []models.ActivityEntry{},
			}
			normalizeTaskProjects(&t)
			d.Tasks = append(d.Tasks, t)
			tid := t.ID
			models.AppendAudit(d, actor, "task.created", fmt.Sprintf("created task %q (bulk)", in.Name), &tid)
			created = append(created, t)
		}
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() {
		claims, _ := middleware.PlatformClaims(c)
		email := ""
		if claims != nil {
			email = claims.Email
		}
		for _, t := range created {
			if strings.TrimSpace(t.Assignee) == "" {
				continue
			}
			publishTaskAssigned(c.Request.Context(), h.Svc.Events, h.Svc.Repo, t, actor, email)
		}
	}
	c.JSON(http.StatusOK, gin.H{"tasks": created, "version": ws.Version})
}

func (h *Entities) patchTasksBulk(c *gin.Context) {
	var body struct {
		Patches []struct {
			ID    int            `json:"id"`
			Patch map[string]any `json:"patch"`
		} `json:"patches"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Patches) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	patchByID := make(map[int]map[string]any, len(body.Patches))
	for _, p := range body.Patches {
		if p.ID == 0 || p.Patch == nil {
			continue
		}
		patchByID[p.ID] = p.Patch
	}
	if len(patchByID) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "no valid patches")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	type assignChange struct {
		updated models.Task
		prev    string
	}
	var changes []assignChange
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		changes = changes[:0]
		for i := range d.Tasks {
			patch, ok := patchByID[d.Tasks[i].ID]
			if !ok {
				continue
			}
			prev := d.Tasks[i].Assignee
			applyTaskPatch(&d.Tasks[i], patch)
			changes = append(changes, assignChange{updated: d.Tasks[i], prev: prev})
			tid := d.Tasks[i].ID
			models.AppendAudit(d, actor, "task.updated", fmt.Sprintf("bulk-updated task #%d", tid), &tid)
		}
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() {
		claims, _ := middleware.PlatformClaims(c)
		email := ""
		if claims != nil {
			email = claims.Email
		}
		for _, ch := range changes {
			if strings.TrimSpace(ch.updated.Assignee) == "" || ch.updated.Assignee == ch.prev {
				continue
			}
			publishTaskAssigned(c.Request.Context(), h.Svc.Events, h.Svc.Repo, ch.updated, actor, email)
		}
	}
	respondWorkspace(c, ws)
}

func (h *Entities) deleteTasksBatch(c *gin.Context) {
	var body struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	// Default is hard-delete to preserve the prior contract; soft-delete is
	// opt-in via ?soft=true so callers can move bulk delete onto the new
	// reversible semantics when ready.
	soft := c.Query("soft") == "true"
	remove := map[int]bool{}
	for _, id := range body.IDs {
		remove[id] = true
	}
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		if soft {
			now := models.ISONow()
			for i := range d.Tasks {
				if remove[d.Tasks[i].ID] {
					d.Tasks[i].DeletedAt = now
				}
			}
			models.AppendAudit(d, actor, "task.soft_deleted",
				fmt.Sprintf("soft-deleted %d tasks (bulk)", len(remove)), nil)
			return nil
		}
		cascadeRemoveTasks(d, remove)
		models.AppendAudit(d, actor, "task.deleted",
			fmt.Sprintf("hard-deleted %d tasks (bulk)", len(remove)), nil)
		return nil
	})
}

func (h *Entities) addTaskTag(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	var body struct {
		Tag string `json:"tag"`
	}
	_ = c.ShouldBindJSON(&body)
	tag := strings.TrimSpace(body.Tag)
	if tag == "" {
		tag = strings.TrimSpace(c.Query("tag"))
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != taskID {
				continue
			}
			for _, existing := range d.Tasks[i].Tags {
				if existing == tag {
					return nil
				}
			}
			d.Tasks[i].Tags = append(d.Tasks[i].Tags, tag)
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) removeTaskTag(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	tag := c.Param("tag")
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != taskID {
				continue
			}
			out := d.Tasks[i].Tags[:0]
			for _, t := range d.Tasks[i].Tags {
				if t != tag {
					out = append(out, t)
				}
			}
			d.Tasks[i].Tags = out
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) patchTaskCustom(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	field := c.Param("field")
	var body struct {
		Value string `json:"value"`
	}
	_ = c.ShouldBindJSON(&body)
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	// Type-validate against the field def before locking the workspace
	// to avoid version-bumping for invalid input.
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	for _, def := range doc.TaskCustomFieldDefs {
		if def.ID != field {
			continue
		}
		if msg := validateCustomFieldValue(def, body.Value); msg != "" {
			apierr.JSONStatus(c, http.StatusBadRequest, msg)
			return
		}
		break
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != taskID {
				continue
			}
			if d.Tasks[i].CustomValues == nil {
				d.Tasks[i].CustomValues = map[string]string{}
			}
			d.Tasks[i].CustomValues[field] = body.Value
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) addTaskDep(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	var body struct {
		DepID int `json:"depId"`
	}
	_ = c.ShouldBindJSON(&body)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != taskID {
				continue
			}
			for _, dpid := range d.Tasks[i].DependsOn {
				if dpid == body.DepID {
					return nil
				}
			}
			d.Tasks[i].DependsOn = append(d.Tasks[i].DependsOn, body.DepID)
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) removeTaskDep(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	depID, _ := strconv.Atoi(c.Param("depId"))
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != taskID {
				continue
			}
			out := d.Tasks[i].DependsOn[:0]
			for _, dpid := range d.Tasks[i].DependsOn {
				if dpid != depID {
					out = append(out, dpid)
				}
			}
			d.Tasks[i].DependsOn = out
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) addTaskComment(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))
	var body struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Text == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	parsedMentions := mentions.Parse(body.Text)
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var membersSnapshot []models.Member
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		cmt := models.TaskComment{
			TaskID: taskID, Author: actor, Text: body.Text,
			Mentions: parsedMentions, Time: models.ISONow(),
		}
		cmt.ID = models.NextCommentID(d)
		d.TaskComments = append(d.TaskComments, cmt)
		membersSnapshot = append(membersSnapshot[:0], d.Members...)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if len(parsedMentions) > 0 && h.Svc.Events != nil && h.Svc.Events.Enabled() {
		publishMentions(c.Request.Context(), h.Svc.Events, parsedMentions, actor, body.Text, "task_comment", strconv.Itoa(taskID), membersSnapshot)
	}
	respondWorkspace(c, ws)
}

func (h *Entities) deleteTaskComment(c *gin.Context) {
	commentID, _ := strconv.Atoi(c.Param("id"))
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.TaskComments[:0]
		for _, cmt := range d.TaskComments {
			if cmt.ID != commentID {
				out = append(out, cmt)
			}
		}
		d.TaskComments = out
		return nil
	})
}

func (h *Entities) createGoal(c *gin.Context) {
	var g models.Goal
	if err := c.ShouldBindJSON(&g); err != nil || g.Name == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.Goal
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		g.ID = models.NextGoalID(d)
		created = g
		d.Goals = append(d.Goals, g)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"goal": created, "version": ws.Version})
}

func (h *Entities) patchGoal(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var patch struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Period   string `json:"period"`
		Team     string `json:"team"`
		Progress *int   `json:"progress"`
	}
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Goals {
			if d.Goals[i].ID != id {
				continue
			}
			if patch.Name != "" {
				d.Goals[i].Name = patch.Name
			}
			if patch.Status != "" {
				d.Goals[i].Status = patch.Status
			}
			if patch.Period != "" {
				d.Goals[i].Period = patch.Period
			}
			if patch.Team != "" {
				d.Goals[i].Team = patch.Team
			}
			if patch.Progress != nil {
				d.Goals[i].Progress = *patch.Progress
			}
			return nil
		}
		return fmt.Errorf("goal not found")
	})
}

func (h *Entities) deleteGoal(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Goals[:0]
		for _, g := range d.Goals {
			if g.ID != id {
				out = append(out, g)
			}
		}
		d.Goals = out
		return nil
	})
}

func (h *Entities) goalProgress(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var body struct {
		Delta int `json:"delta"`
	}
	_ = bindJSONCoerced(c, &body)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Goals {
			if d.Goals[i].ID != id {
				continue
			}
			d.Goals[i].Progress += body.Delta
			if d.Goals[i].Progress > 100 {
				d.Goals[i].Progress = 100
			}
			if d.Goals[i].Progress < 0 {
				d.Goals[i].Progress = 0
			}
			return nil
		}
		return fmt.Errorf("goal not found")
	})
}

func (h *Entities) createSprint(c *gin.Context) {
	var sp models.Sprint
	if err := c.ShouldBindJSON(&sp); err != nil || sp.Name == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.Sprint
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		sp.ID = models.NextSprintID(d)
		created = sp
		d.Sprints = append(d.Sprints, sp)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sprint": created, "version": ws.Version})
}

func (h *Entities) patchSprint(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var patch models.Sprint
	_ = c.ShouldBindJSON(&patch)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Sprints {
			if d.Sprints[i].ID != id {
				continue
			}
			if patch.Name != "" {
				d.Sprints[i].Name = patch.Name
			}
			if patch.ProjectID != "" {
				d.Sprints[i].ProjectID = patch.ProjectID
			}
			if patch.StartDate != "" {
				d.Sprints[i].StartDate = patch.StartDate
			}
			if patch.EndDate != "" {
				d.Sprints[i].EndDate = patch.EndDate
			}
			if patch.Goal != "" {
				d.Sprints[i].Goal = patch.Goal
			}
			if patch.Status != "" {
				d.Sprints[i].Status = patch.Status
			}
			return nil
		}
		return fmt.Errorf("sprint not found")
	})
}

func (h *Entities) deleteSprint(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Sprints[:0]
		removed := false
		for _, s := range d.Sprints {
			if s.ID == id {
				removed = true
				continue
			}
			out = append(out, s)
		}
		if !removed {
			return fmt.Errorf("sprint not found")
		}
		d.Sprints = out
		// Detach tasks that referenced this sprint so they don't keep
		// pointing at a dangling sprintId.
		for i := range d.Tasks {
			if d.Tasks[i].SprintID == id {
				d.Tasks[i].SprintID = 0
			}
		}
		return nil
	})
}

func (h *Entities) createChat(c *gin.Context) {
	var ch models.Chat
	if err := c.ShouldBindJSON(&ch); err != nil || ch.Name == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.Chat
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		ch.ID = models.NextChatID(d)
		created = ch
		d.Chats = append(d.Chats, ch)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"chat": created, "version": ws.Version})
}

func (h *Entities) chatRead(c *gin.Context) {
	chatID, _ := strconv.Atoi(c.Param("id"))
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Messages {
			if d.Messages[i].ChatID != chatID {
				continue
			}
			found := false
			for _, r := range d.Messages[i].ReadBy {
				if r == actor {
					found = true
					break
				}
			}
			if !found && actor != "" {
				d.Messages[i].ReadBy = append(d.Messages[i].ReadBy, actor)
			}
		}
		return nil
	})
}

func (h *Entities) chatMute(c *gin.Context) {
	chatID, _ := strconv.Atoi(c.Param("id"))
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Chats {
			if d.Chats[i].ID == chatID {
				d.Chats[i].Muted = !d.Chats[i].Muted
				return nil
			}
		}
		return fmt.Errorf("chat not found")
	})
}

func (h *Entities) postMessage(c *gin.Context) {
	var body struct {
		ChatID      int                           `json:"chatId"`
		Text        string                        `json:"text"`
		ReplyTo     *int                          `json:"replyTo"`
		Edited      bool                          `json:"edited"`
		Deleted     bool                          `json:"deleted"`
		Attachments []models.MessageAttachmentRef `json:"attachments"`
		ReminderAt  *string                       `json:"reminderAt"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	parsedMentions := mentions.Parse(body.Text)
	uid, ok := userID(c)
	if !ok {
		apierr.JSONStatus(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var messageID int
	var membersSnapshot []models.Member
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		msg := models.Message{
			ChatID: body.ChatID, Author: actor, Text: body.Text,
			ReplyTo: body.ReplyTo, Edited: body.Edited, Deleted: body.Deleted,
			Reactions: map[string][]string{}, ReadBy: []string{actor},
			Attachments: body.Attachments, ReminderAt: body.ReminderAt, Time: models.ISONow(),
		}
		msg.ID = models.NextMessageID(d)
		messageID = msg.ID
		d.Messages = append(d.Messages, msg)
		membersSnapshot = append(membersSnapshot[:0], d.Members...)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if len(parsedMentions) > 0 && h.Svc.Events != nil && h.Svc.Events.Enabled() {
		publishMentions(c.Request.Context(), h.Svc.Events, parsedMentions, actor, body.Text, "chat_message", strconv.Itoa(messageID), membersSnapshot)
	}
	respondWorkspace(c, ws)
}

func (h *Entities) addMessageReaction(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Emoji string `json:"emoji"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Emoji) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Messages {
			if d.Messages[i].ID != id {
				continue
			}
			if d.Messages[i].Reactions == nil {
				d.Messages[i].Reactions = map[string][]string{}
			}
			users := d.Messages[i].Reactions[body.Emoji]
			for _, u := range users {
				if u == actor {
					return nil
				}
			}
			d.Messages[i].Reactions[body.Emoji] = append(users, actor)
			return nil
		}
		return fmt.Errorf("message not found")
	})
}

func (h *Entities) removeMessageReaction(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	emoji := c.Param("emoji")
	if emoji == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid emoji")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Messages {
			if d.Messages[i].ID != id {
				continue
			}
			users, ok := d.Messages[i].Reactions[emoji]
			if !ok {
				return nil
			}
			kept := users[:0]
			for _, u := range users {
				if u != actor {
					kept = append(kept, u)
				}
			}
			if len(kept) == 0 {
				delete(d.Messages[i].Reactions, emoji)
			} else {
				d.Messages[i].Reactions[emoji] = kept
			}
			return nil
		}
		return fmt.Errorf("message not found")
	})
}

func (h *Entities) createFile(c *gin.Context) {
	var f models.WorkspaceFile
	if err := c.ShouldBindJSON(&f); err != nil || f.N == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	_, wsMeta, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	if h.Files != nil && f.Data != "" && len(f.Data) > 256 {
		meta, err := h.Files.PersistInline(c.Request.Context(), wsMeta.ID, wsMeta.OwnerUserID, f.N, f.Data)
		if err == nil {
			f.Data = "blob:" + meta.ID.String()
		}
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		f.ID = models.NextFileID(d)
		d.Files = append(d.Files, f)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"file": f, "version": ws.Version})
}

func (h *Entities) getFile(c *gin.Context) {
	if h.Files == nil {
		apierr.JSONStatus(c, http.StatusNotFound, "file storage disabled")
		return
	}
	rawID := c.Param("id")
	blobID, err := uuid.Parse(strings.TrimPrefix(rawID, "blob:"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid file id")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	rec, err := h.Files.GetBlob(c.Request.Context(), blobID)
	if err != nil {
		apierr.JSONStatus(c, http.StatusNotFound, "file not found")
		return
	}
	if rec.WorkspaceID != ws.ID {
		apierr.JSONStatus(c, http.StatusForbidden, "file not in workspace")
		return
	}
	payload, err := h.Files.ReadBlob(rec)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "read file failed")
		return
	}
	ct := rec.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=%q", rec.Filename))
	c.Data(http.StatusOK, ct, payload)
}

func (h *Entities) putProject(c *gin.Context) {
	id := c.Param("id")
	var p models.Project
	if err := c.ShouldBindJSON(&p); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	switch p.Visibility {
	case "", models.ProjectVisibilityWorkspace, models.ProjectVisibilityMembersOnly:
	default:
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid visibility")
		return
	}
	p.ID = id
	isAdmin := auth.HasPerm(c, "pm.admin")
	isNew := false
	statusChanged := false
	prevStatus := ""
	mutate(c, h.Svc, func(d *models.Document) error {
		if d.Projects == nil {
			d.Projects = map[string]models.Project{}
		}
		if existing, ok := d.Projects[id]; ok {
			if existing.Status != "" && p.Status != existing.Status {
				statusChanged = true
				prevStatus = existing.Status
			}
			// Access-control fields (visibility + member list) are the guard for
			// members_only filtering; only a workspace admin may change them.
			// A plain writer's PUT preserves the existing values so it cannot
			// widen a members_only project or tamper with its member list.
			if !isAdmin {
				p.Visibility = existing.Visibility
				p.MemberIDs = existing.MemberIDs
			}
			// Preserve server-managed fields a client PUT typically omits, so a
			// partial round-trip does not wipe them.
			if p.StatusHistory == nil {
				p.StatusHistory = existing.StatusHistory
			}
			if p.LinkedContracts == nil {
				p.LinkedContracts = existing.LinkedContracts
			}
			// ConversationID is server-managed; never let a client PUT set or
			// wipe it.
			p.ConversationID = existing.ConversationID
		} else {
			isNew = true
			p.ConversationID = ""
		}
		d.Projects[id] = p
		return nil
	})
	// A new project gets a chat discussion thread. Done async + best-effort so a
	// chat outage never blocks or fails project creation; the conversation id is
	// written back onto the project when the thread is ready.
	if isNew && h.Chat != nil {
		if uid, ok := userID(c); ok {
			go h.ensureProjectThread(uid, id, p.Name, append([]string(nil), p.MemberIDs...))
		}
	}
	// Status change → post a system line into the project's discussion thread.
	if !isNew && statusChanged && h.Chat != nil {
		go h.postProjectSystem(id, "Status changed: "+prevStatus+" → "+p.Status)
	}
}

// postProjectSystem posts a system line into a project's discussion thread
// (find-or-creating it by link). Runs in the background, best-effort.
func (h *Entities) postProjectSystem(projectID, message string) {
	if h.Chat == nil || projectID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Chat.PostSystem(ctx, projectID, message); err != nil {
		slog.Warn("project chat system post failed", "project", projectID, "err", err)
	}
}

// ensureProjectThread find-or-creates a project's chat thread and persists its
// conversation id back onto the project. Runs in the background off the request.
func (h *Entities) ensureProjectThread(uid, projectID, title string, participants []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	// The creator must be a participant so they can read/post; then the project
	// members. Dedup, drop empties.
	seen := map[string]bool{}
	parts := make([]string, 0, len(participants)+1)
	for _, m := range append([]string{uid}, participants...) {
		if m != "" && !seen[m] {
			seen[m] = true
			parts = append(parts, m)
		}
	}
	convID, err := h.Chat.EnsureProjectThread(ctx, projectID, title, parts)
	if err != nil {
		slog.Warn("project chat thread create failed", "project", projectID, "err", err)
		return
	}
	if convID == "" {
		return
	}
	if _, err := h.Svc.Mutate(ctx, uid, func(d *models.Document) error {
		if pr, ok := d.Projects[projectID]; ok && pr.ConversationID == "" {
			pr.ConversationID = convID
			d.Projects[projectID] = pr
		}
		return nil
	}); err != nil {
		slog.Warn("persist project conversation id failed", "project", projectID, "err", err)
	}
}

func (h *Entities) createRequisition(c *gin.Context) {
	var req models.Requisition
	if err := bindJSONCoerced(c, &req); err != nil || req.Title == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.Requisition
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		req.ID = models.NextRequisitionID(d)
		created = req
		d.Requisitions = append(d.Requisitions, req)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() {
		h.Svc.Events.PublishCommercial(c.Request.Context(), events.TypeRequisitionSubmitted, map[string]any{
			"requisitionId":        strconv.Itoa(created.ID),
			"workspaceOwnerUserId": uid,
			"title":                created.Title,
			"amount":               fmt.Sprintf("%.2f", created.Amount),
			"currency":             created.Currency,
			"status":               created.Status,
			"requestedBy":          created.RequestedBy,
			"forDept":              created.ForDept,
			"urgency":              created.Urgency,
			"payee":                created.Payee,
			"justification":        created.Justification,
		}, strconv.Itoa(created.ID))
		claims, _ := middleware.PlatformClaims(c)
		if claims != nil && claims.Email != "" {
			h.Svc.Events.PublishPMAlert(c.Request.Context(), "email", claims.Email, "pm.requisition.submitted", map[string]string{
				"title": created.Title,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"requisition": created, "version": ws.Version})
}

func (h *Entities) addMember(c *gin.Context) {
	var body struct {
		UserID string         `json:"userId"`
		Role   string         `json:"role"`
		Member *models.Member `json:"member"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.UserID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	wsMeta, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	role := body.Role
	if role == "" {
		role = "member"
	}
	if err := h.Svc.Repo.AddMember(c.Request.Context(), wsMeta.ID, body.UserID, role); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "add member failed")
		return
	}
	if body.Member != nil && body.Member.Initials != "" {
		member := *body.Member
		// Anchor the doc-level member to the canonical auth UserID. Upstream
		// auth events (deactivate/reactivate) match on this first.
		if member.UserID == "" {
			member.UserID = body.UserID
		}
		_, err = h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
			for i := range d.Members {
				if d.Members[i].Initials == member.Initials {
					d.Members[i] = member
					return nil
				}
			}
			d.Members = append(d.Members, member)
			return nil
		})
		if err != nil {
			writeMutationError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Entities) setOrg(c *gin.Context) {
	var body struct {
		OrgID string `json:"orgId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	orgID := strings.TrimSpace(body.OrgID)
	if orgID != "" && h.Users != nil && h.Users.Enabled() {
		bearer := bearerFromRequest(c)
		if _, err := h.Users.GetOrg(c.Request.Context(), bearer, orgID); err != nil {
			if errors.Is(err, usersclient.ErrForbidden) || errors.Is(err, usersclient.ErrNotFound) {
				apierr.JSONStatus(c, http.StatusForbidden, "org_not_accessible")
				return
			}
			apierr.JSONStatus(c, http.StatusBadGateway, "users_service_unavailable")
			return
		}
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	if err := h.Svc.Repo.SetOrgID(c.Request.Context(), ws.OwnerUserID, orgID); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "set org failed")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		d.OrgID = orgID
		return nil
	})
	if orgID != "" {
		h.syncOrgMembersToWorkspace(c, ws, orgID, bearerFromRequest(c))
	}
}

func (h *Entities) syncOrgMembersToWorkspace(c *gin.Context, ws store.Workspace, orgID, bearer string) {
	if h.Users == nil || !h.Users.Enabled() || bearer == "" {
		return
	}
	members, err := h.Users.ListOrgMembers(c.Request.Context(), bearer, orgID)
	if err != nil {
		return
	}
	for _, m := range members {
		if strings.EqualFold(m.UserID, ws.OwnerUserID) {
			continue
		}
		role := m.Role
		if role == "" {
			role = "member"
		}
		_ = h.Svc.Repo.AddMember(c.Request.Context(), ws.ID, m.UserID, role)
		_, _ = h.Svc.Mutate(c.Request.Context(), ws.OwnerUserID, func(d *models.Document) error {
			consumer.ApplyOrgMemberAdded(d, m.UserID, m.Email, m.FullName, role)
			return nil
		})
	}
}

func (h *Entities) getWorkspaceOrg(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "decode workspace")
		return
	}
	out := gin.H{"orgId": doc.OrgID}
	if doc.OrgID != "" && h.Users != nil && h.Users.Enabled() {
		if org, err := h.Users.GetOrg(c.Request.Context(), bearerFromRequest(c), doc.OrgID); err == nil {
			out["org"] = org
		}
	}
	c.JSON(http.StatusOK, out)
}

func (h *Entities) listOrgs(c *gin.Context) {
	if h.Users == nil || !h.Users.Enabled() {
		apierr.JSONStatus(c, http.StatusServiceUnavailable, "users_service_not_configured")
		return
	}
	items, err := h.Users.ListOrgs(c.Request.Context(), bearerFromRequest(c))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadGateway, "users_service_unavailable")
		return
	}
	if items == nil {
		items = []usersclient.Organization{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *Entities) listOrgMembers(c *gin.Context) {
	if h.Users == nil || !h.Users.Enabled() {
		apierr.JSONStatus(c, http.StatusServiceUnavailable, "users_service_not_configured")
		return
	}
	orgID := strings.TrimSpace(c.Param("orgId"))
	if orgID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid orgId")
		return
	}
	items, err := h.Users.ListOrgMembers(c.Request.Context(), bearerFromRequest(c), orgID)
	if err != nil {
		if errors.Is(err, usersclient.ErrForbidden) || errors.Is(err, usersclient.ErrNotFound) {
			apierr.JSONStatus(c, http.StatusForbidden, "org_not_accessible")
			return
		}
		apierr.JSONStatus(c, http.StatusBadGateway, "users_service_unavailable")
		return
	}
	if items == nil {
		items = []usersclient.OrgMember{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func bearerFromRequest(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}
