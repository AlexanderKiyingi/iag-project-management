package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/google/uuid"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/files"
	"github.com/iag/project-management/backend/internal/mentions"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/workspace"
)

type Entities struct {
	Svc   *workspace.Service
	Files *files.Store
}

func (h *Entities) Register(rg *gin.RouterGroup) {
	authz := auth.RequireWorkspaceWrite()
	rg.DELETE("/audit", authz, h.deleteAudit)
	rg.PATCH("/settings", authz, h.patchSettings)

	rg.POST("/tasks", authz, h.createTask)
	rg.PATCH("/tasks/:id", authz, h.patchTask)
	rg.POST("/tasks/delete-batch", authz, h.deleteTasksBatch)
	rg.POST("/tasks/:id/tags", authz, h.addTaskTag)
	rg.DELETE("/tasks/:id/tags/:tag", authz, h.removeTaskTag)
	rg.PATCH("/tasks/:id/custom/:field", authz, h.patchTaskCustom)
	rg.POST("/tasks/:id/deps", authz, h.addTaskDep)
	rg.DELETE("/tasks/:id/deps/:depId", authz, h.removeTaskDep)
	rg.POST("/tasks/:id/comments", authz, h.addTaskComment)
	rg.DELETE("/comments/:id", authz, h.deleteTaskComment)

	rg.POST("/goals", authz, h.createGoal)
	rg.PATCH("/goals/:id", authz, h.patchGoal)
	rg.DELETE("/goals/:id", authz, h.deleteGoal)
	rg.POST("/goals/:id/progress", authz, h.goalProgress)

	rg.POST("/sprints", authz, h.createSprint)
	rg.PATCH("/sprints/:id", authz, h.patchSprint)
	rg.DELETE("/sprints/:id", authz, h.deleteSprint)

	rg.POST("/chats", authz, h.createChat)
	rg.POST("/chats/:id/read", authz, h.chatRead)
	rg.POST("/chats/:id/mute", authz, h.chatMute)
	rg.POST("/messages", authz, h.postMessage)

	rg.POST("/files", authz, h.createFile)
	rg.GET("/files/:id", auth.RequireWorkspaceRead(), h.getFile)
	rg.PUT("/projects/:id", authz, h.putProject)
	rg.POST("/requisitions", authz, h.createRequisition)

	rg.POST("/workspace/members", authz, auth.RequirePerm("pm.admin"), h.addMember)
	rg.PATCH("/workspace/org", authz, auth.RequirePerm("pm.admin"), h.setOrg)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
		Name     string `json:"name"`
		Desc     string `json:"desc"`
		Project  string `json:"project"`
		Section  string `json:"section"`
		Assignee string `json:"assignee"`
		Priority string `json:"priority"`
		Due      string `json:"due"`
		SprintID *int   `json:"sprintId"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	var created models.Task
	uid, ok := userID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		sprintID := 0
		if in.SprintID != nil {
			sprintID = *in.SprintID
		}
		created = models.Task{
			ID:       models.NextTaskID(d),
			Name:     in.Name,
			Project:  in.Project,
			Section:  in.Section,
			Assignee: in.Assignee,
			Priority: in.Priority,
			Due:      in.Due,
			Status:   "on_track",
			Tags:     []string{},
			DependsOn: []int{},
			SprintID: sprintID,
			CustomValues: map[string]string{},
			Activity: []models.ActivityEntry{},
		}
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
		h.Svc.Events.PublishCommercial(c.Request.Context(), events.TypeTaskAssigned, map[string]any{
			"taskId":   strconv.Itoa(created.ID),
			"assignee": in.Assignee,
			"actor":    actor,
			"email":    email,
		}, strconv.Itoa(created.ID))
	}
	c.JSON(http.StatusOK, gin.H{"task": created, "version": ws.Version})
}

func (h *Entities) patchTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			applyTaskPatch(&d.Tasks[i], patch)
			return nil
		}
		return fmt.Errorf("task not found")
	})
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
	if v, ok := patch["priority"].(string); ok {
		t.Priority = v
	}
	if v, ok := patch["project"].(string); ok {
		t.Project = v
	}
	if v, ok := patch["section"].(string); ok {
		t.Section = v
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
}

func (h *Entities) deleteTasksBatch(c *gin.Context) {
	var body struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	remove := map[int]bool{}
	for _, id := range body.IDs {
		remove[id] = true
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Tasks[:0]
		for _, t := range d.Tasks {
			if !remove[t.ID] {
				out = append(out, t)
			}
		}
		d.Tasks = out
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		cmt := models.TaskComment{
			TaskID: taskID, Author: actor, Text: body.Text,
			Mentions: mentions.Parse(body.Text), Time: models.ISONow(),
		}
		cmt.ID = models.NextCommentID(d)
		d.TaskComments = append(d.TaskComments, cmt)
		return nil
	})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
	_ = c.ShouldBindJSON(&body)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
		for _, s := range d.Sprints {
			if s.ID != id {
				out = append(out, s)
			}
		}
		d.Sprints = out
		return nil
	})
}

func (h *Entities) createChat(c *gin.Context) {
	var ch models.Chat
	if err := c.ShouldBindJSON(&ch); err != nil || ch.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		msg := models.Message{
			ChatID: body.ChatID, Author: actor, Text: body.Text,
			ReplyTo: body.ReplyTo, Edited: body.Edited, Deleted: body.Deleted,
			Reactions: map[string][]string{}, ReadBy: []string{actor},
			Attachments: body.Attachments, ReminderAt: body.ReminderAt, Time: models.ISONow(),
		}
		msg.ID = models.NextMessageID(d)
		d.Messages = append(d.Messages, msg)
		return nil
	})
}

func (h *Entities) createFile(c *gin.Context) {
	var f models.WorkspaceFile
	if err := c.ShouldBindJSON(&f); err != nil || f.N == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	_, wsMeta, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
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
		c.JSON(http.StatusNotFound, gin.H{"error": "file storage disabled"})
		return
	}
	rawID := c.Param("id")
	blobID, err := uuid.Parse(strings.TrimPrefix(rawID, "blob:"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	rec, err := h.Files.GetBlob(c.Request.Context(), blobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	if rec.WorkspaceID != ws.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "file not in workspace"})
		return
	}
	payload, err := h.Files.ReadBlob(rec)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file failed"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	p.ID = id
	mutate(c, h.Svc, func(d *models.Document) error {
		if d.Projects == nil {
			d.Projects = map[string]models.Project{}
		}
		d.Projects[id] = p
		return nil
	})
}

func (h *Entities) createRequisition(c *gin.Context) {
	var req models.Requisition
	if err := c.ShouldBindJSON(&req); err != nil || req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
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
			"requisitionId": strconv.Itoa(created.ID),
			"title":         created.Title,
			"amount":        fmt.Sprintf("%.2f", created.Amount),
			"currency":      created.Currency,
			"status":        created.Status,
			"requestedBy":   created.RequestedBy,
		}, strconv.Itoa(created.ID))
		claims, _ := middleware.PlatformClaims(c)
		if claims != nil && claims.Email != "" {
			h.Svc.Events.PublishNotificationRequested(c.Request.Context(), "email", claims.Email, "pm.requisition.submitted", map[string]string{
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	wsMeta, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	role := body.Role
	if role == "" {
		role = "member"
	}
	if err := h.Svc.Repo.AddMember(c.Request.Context(), wsMeta.ID, body.UserID, role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "add member failed"})
		return
	}
	if body.Member != nil && body.Member.Initials != "" {
		member := *body.Member
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	ws, err := h.Svc.Repo.ResolveForUser(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	if err := h.Svc.Repo.SetOrgID(c.Request.Context(), ws.OwnerUserID, body.OrgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "set org failed"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		d.OrgID = body.OrgID
		return nil
	})
}
