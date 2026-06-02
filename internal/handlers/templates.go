package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) listTemplates(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": doc.Templates})
}

func (h *Entities) getTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	for _, t := range doc.Templates {
		if t.ID == id {
			c.JSON(http.StatusOK, gin.H{"template": t})
			return
		}
	}
	apierr.JSONStatus(c, http.StatusNotFound, "template not found")
}

func (h *Entities) createTemplate(c *gin.Context) {
	var body struct {
		Type      string         `json:"type"`
		Name      string         `json:"name"`
		Variables []string       `json:"variables"`
		Body      map[string]any `json:"body"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	tType := strings.ToLower(strings.TrimSpace(body.Type))
	if tType != models.TemplateTypeProject && tType != models.TemplateTypeTask {
		apierr.JSONStatus(c, http.StatusBadRequest, "type must be project or task")
		return
	}
	if body.Body == nil {
		body.Body = map[string]any{}
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	now := models.ISONow()
	var created models.Template
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		created = models.Template{
			ID:        models.NextTemplateID(d),
			Type:      tType,
			Name:      strings.TrimSpace(body.Name),
			Variables: dedupeNonEmpty(body.Variables),
			Body:      body.Body,
			CreatedAt: now,
			UpdatedAt: now,
		}
		d.Templates = append(d.Templates, created)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"template": created, "version": ws.Version})
}

func (h *Entities) deleteTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Templates[:0]
		removed := false
		for _, t := range d.Templates {
			if t.ID == id {
				removed = true
				continue
			}
			out = append(out, t)
		}
		if !removed {
			return fmt.Errorf("template not found")
		}
		d.Templates = out
		return nil
	})
}

// applyTemplate materializes a template into the current workspace.
// The optional context map supplies values for {{placeholder}} tokens
// in the template body's string fields.
func (h *Entities) applyTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Context map[string]string `json:"context"`
	}
	_ = c.ShouldBindJSON(&body)
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	type applied struct {
		CreatedTaskIDs []int  `json:"createdTaskIds"`
		ProjectID      string `json:"projectId,omitempty"`
	}
	var result applied
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		var tpl *models.Template
		for i := range d.Templates {
			if d.Templates[i].ID == id {
				tpl = &d.Templates[i]
				break
			}
		}
		if tpl == nil {
			return fmt.Errorf("template not found")
		}
		// Deep-clone the body to avoid mutating the stored template
		// while substituting placeholders.
		cloned, err := cloneTemplateBody(tpl.Body, body.Context)
		if err != nil {
			return fmt.Errorf("template substitution: %w", err)
		}
		switch tpl.Type {
		case models.TemplateTypeProject:
			pid, taskIDs, err := applyProjectTemplate(d, cloned, actor)
			if err != nil {
				return err
			}
			result.ProjectID = pid
			result.CreatedTaskIDs = taskIDs
		case models.TemplateTypeTask:
			taskID, err := applyTaskTemplate(d, cloned, actor)
			if err != nil {
				return err
			}
			result.CreatedTaskIDs = []int{taskID}
		default:
			return fmt.Errorf("unsupported template type %q", tpl.Type)
		}
		models.AppendAudit(d, actor, "template.applied",
			fmt.Sprintf("applied template %q", tpl.Name), nil)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"applied": result,
		"version": ws.Version,
	})
}

// cloneTemplateBody round-trips through JSON so the stored template's
// map isn't mutated by substitution. Placeholder tokens in string
// values look like {{name}} and are replaced by ctx[name].
func cloneTemplateBody(body map[string]any, ctx map[string]string) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if len(ctx) > 0 {
		out := string(raw)
		for k, v := range ctx {
			out = strings.ReplaceAll(out, "{{"+k+"}}", jsonEscape(v))
		}
		raw = []byte(out)
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// Strip surrounding quotes — we're inserting into an existing
	// quoted context.
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}

func applyProjectTemplate(d *models.Document, body map[string]any, actor string) (string, []int, error) {
	projectRaw, _ := body["project"].(map[string]any)
	if projectRaw == nil {
		return "", nil, fmt.Errorf("project template missing 'project' object")
	}
	pid, _ := projectRaw["id"].(string)
	if pid == "" {
		return "", nil, fmt.Errorf("project template requires project.id (use {{placeholder}} for runtime injection)")
	}
	if _, exists := d.Projects[pid]; exists {
		return "", nil, fmt.Errorf("project %q already exists", pid)
	}
	project := models.Project{ID: pid}
	if v, ok := projectRaw["name"].(string); ok {
		project.Name = v
	}
	if v, ok := projectRaw["color"].(string); ok {
		project.Color = v
	}
	if v, ok := projectRaw["icon"].(string); ok {
		project.Icon = v
	}
	if v, ok := projectRaw["status"].(string); ok {
		project.Status = v
	}
	if v, ok := projectRaw["code"].(string); ok {
		project.Code = v
	}
	if d.Projects == nil {
		d.Projects = map[string]models.Project{}
	}
	d.Projects[pid] = project

	// Sections, if any.
	if rawSecs, ok := body["sections"].([]any); ok {
		for _, raw := range rawSecs {
			m, _ := raw.(map[string]any)
			name, _ := m["name"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			d.Sections = append(d.Sections, models.Section{
				ID:        models.NextSectionID(d),
				ProjectID: pid,
				Name:      name,
				Order:     len(d.Sections),
			})
		}
	}

	// Tasks under this project.
	createdTaskIDs := []int{}
	if rawTasks, ok := body["tasks"].([]any); ok {
		for _, raw := range rawTasks {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			m["project"] = pid
			tid, err := applyTaskTemplate(d, m, actor)
			if err != nil {
				return "", nil, err
			}
			createdTaskIDs = append(createdTaskIDs, tid)
		}
	}
	return pid, createdTaskIDs, nil
}

func applyTaskTemplate(d *models.Document, body map[string]any, actor string) (int, error) {
	name, _ := body["name"].(string)
	if strings.TrimSpace(name) == "" {
		return 0, fmt.Errorf("task template requires a name")
	}
	task := models.Task{
		ID:           models.NextTaskID(d),
		Name:         name,
		Status:       "on_track",
		Tags:         []string{},
		DependsOn:    []int{},
		CustomValues: map[string]string{},
		Activity:     []models.ActivityEntry{},
	}
	if v, ok := body["desc"].(string); ok {
		task.Desc = v
	}
	if v, ok := body["project"].(string); ok {
		task.Project = v
	}
	if v, ok := body["section"].(string); ok {
		task.Section = v
	}
	if v, ok := body["assignee"].(string); ok {
		task.Assignee = v
	}
	if v, ok := body["priority"].(string); ok {
		task.Priority = v
	}
	if v, ok := body["due"].(string); ok {
		task.Due = v
	}
	if v, ok := body["type"].(string); ok {
		task.Type = normalizeTaskType(v)
	}
	if raw, ok := body["tags"].([]any); ok {
		for _, t := range raw {
			if s, ok := t.(string); ok {
				task.Tags = append(task.Tags, s)
			}
		}
	}
	normalizeTaskProjects(&task)
	d.Tasks = append(d.Tasks, task)
	tid := task.ID
	models.AppendAudit(d, actor, "task.created",
		fmt.Sprintf("created task %q (template)", task.Name), &tid)

	// Optional inline subtasks.
	if raw, ok := body["subtasks"].([]any); ok {
		for _, st := range raw {
			m, _ := st.(map[string]any)
			if m == nil {
				continue
			}
			subName, _ := m["name"].(string)
			if strings.TrimSpace(subName) == "" {
				continue
			}
			d.Subtasks = append(d.Subtasks, models.Subtask{
				ID:           models.NextSubtaskID(d),
				ParentTaskID: tid,
				Name:         subName,
				Order:        len(d.Subtasks),
			})
		}
	}
	return tid, nil
}
