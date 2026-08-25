package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/models"
)

// Work breakdown structure endpoints: phases, activities and work programs.
//
// Three near-identical CRUD trios. They are written out rather than generated
// because each level has its own parent to validate and its own cascade on
// delete, and a generic version hid both.
//
// Creates and patches echo the written row (`{phase: ...}`), not the workspace
// document. Every other mutation here answers with the whole document via
// `mutate()`, which means a client cannot tell which row it just wrote — see
// the write-resolution note in the frontend adapter.

func (h *Entities) registerWBSRoutes(rg *gin.RouterGroup, authz gin.HandlerFunc) {
	read := auth.RequireWorkspaceRead()

	rg.GET("/phases", read, h.listPhases)
	rg.POST("/phases", authz, h.createPhase)
	rg.PATCH("/phases/:id", authz, h.patchPhase)
	rg.DELETE("/phases/:id", authz, h.deletePhase)

	rg.GET("/activities", read, h.listActivities)
	rg.POST("/activities", authz, h.createActivity)
	rg.PATCH("/activities/:id", authz, h.patchActivity)
	rg.DELETE("/activities/:id", authz, h.deleteActivity)

	rg.GET("/work-programs", read, h.listWorkPrograms)
	rg.POST("/work-programs", authz, h.createWorkProgram)
	rg.PATCH("/work-programs/:id", authz, h.patchWorkProgram)
	rg.DELETE("/work-programs/:id", authz, h.deleteWorkProgram)
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// validWBS rejects a status outside that level's closed set, naming the set in
// the message so the caller can fix it without reading the source. Progress is
// clamped rather than rejected — see models.ClampProgress.
func validWBS(c *gin.Context, status string, set []string) bool {
	if models.ValidStatus(status, set) {
		return true
	}
	apierr.JSONStatus(c, http.StatusBadRequest,
		fmt.Sprintf("invalid status; expected one of %s", strings.Join(set, ", ")))
	return false
}

func defaultStatus(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

/* ───────────────────────────── phases ───────────────────────────── */

func (h *Entities) listPhases(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	rows := doc.Phases
	if project := strings.TrimSpace(c.Query("project")); project != "" {
		filtered := make([]models.Phase, 0, len(rows))
		for _, row := range rows {
			if row.ProjectID == project {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if rows == nil {
		rows = []models.Phase{}
	}
	c.JSON(http.StatusOK, gin.H{"phases": rows})
}

func (h *Entities) createPhase(c *gin.Context) {
	var in models.Phase
	if err := bindJSONCoerced(c, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if !validWBS(c, in.Status, models.PhaseStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.Phase
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if in.ProjectID != "" {
			if _, ok := d.Projects[in.ProjectID]; !ok {
				return fmt.Errorf("project not found")
			}
		}
		if in.ID == "" {
			in.ID = nextStringID("PH", d.Phases, func(p models.Phase) string { return p.ID })
		}
		in.Status = defaultStatus(in.Status, models.PhaseStatusDefault)
		in.Progress = models.ClampProgress(in.Progress)
		in.CreatedAt = today()
		in.UpdatedAt = in.CreatedAt
		created = in
		d.Phases = append(d.Phases, in)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"phase": created, "version": ws.Version})
}

func (h *Entities) patchPhase(c *gin.Context) {
	id := c.Param("id")
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if v, ok := patch["status"].(string); ok && !validWBS(c, v, models.PhaseStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var updated models.Phase
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		for i := range d.Phases {
			if d.Phases[i].ID != id {
				continue
			}
			applyPhasePatch(&d.Phases[i], patch)
			d.Phases[i].UpdatedAt = today()
			updated = d.Phases[i]
			return nil
		}
		return fmt.Errorf("phase not found")
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"phase": updated, "version": ws.Version})
}

// deletePhase detaches the activities under it rather than deleting them: the
// work is still real when the grouping is removed, and a cascade would silently
// destroy rows the caller never named.
func (h *Entities) deletePhase(c *gin.Context) {
	id := c.Param("id")
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Phases[:0]
		found := false
		for _, row := range d.Phases {
			if row.ID == id {
				found = true
				continue
			}
			out = append(out, row)
		}
		if !found {
			return fmt.Errorf("phase not found")
		}
		d.Phases = out
		for i := range d.Activities {
			if d.Activities[i].PhaseID == id {
				d.Activities[i].PhaseID = ""
			}
		}
		for i := range d.WorkPrograms {
			if d.WorkPrograms[i].PhaseID == id {
				d.WorkPrograms[i].PhaseID = ""
			}
		}
		return nil
	})
}

func applyPhasePatch(p *models.Phase, patch map[string]any) {
	if v, ok := patch["name"].(string); ok && v != "" {
		p.Name = v
	}
	if v, ok := patch["projectId"].(string); ok {
		p.ProjectID = v
	}
	if v, ok := patch["code"].(string); ok {
		p.Code = v
	}
	if v, ok := patch["startDate"].(string); ok {
		p.StartDate = v
	}
	if v, ok := patch["dueDate"].(string); ok {
		p.DueDate = v
	}
	if v, ok := patch["status"].(string); ok && v != "" {
		p.Status = v
	}
	if v, ok := patch["desc"].(string); ok {
		p.Desc = v
	}
	if v, ok := patch["progress"].(float64); ok {
		p.Progress = models.ClampProgress(int(v))
	}
	if v, ok := patch["sortOrder"].(float64); ok {
		p.SortOrder = int(v)
	}
	if v, ok := patch["budget"].(float64); ok {
		p.Budget = int64(v)
	}
	if v, ok := patch["currency"].(string); ok {
		p.Currency = v
	}
}

/* ─────────────────────────── activities ─────────────────────────── */

func (h *Entities) listActivities(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	project := strings.TrimSpace(c.Query("project"))
	phase := strings.TrimSpace(c.Query("phase"))
	rows := make([]models.Activity, 0, len(doc.Activities))
	for _, row := range doc.Activities {
		if project != "" && row.ProjectID != project {
			continue
		}
		if phase != "" && row.PhaseID != phase {
			continue
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, gin.H{"activities": rows})
}

func (h *Entities) createActivity(c *gin.Context) {
	var in models.Activity
	if err := bindJSONCoerced(c, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if !validWBS(c, in.Status, models.ActivityStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.Activity
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if in.ProjectID != "" {
			if _, ok := d.Projects[in.ProjectID]; !ok {
				return fmt.Errorf("project not found")
			}
		}
		if in.PhaseID != "" && !hasPhase(d, in.PhaseID) {
			return fmt.Errorf("phase not found")
		}
		if in.ID == "" {
			in.ID = nextStringID("ACT", d.Activities, func(a models.Activity) string { return a.ID })
		}
		in.Status = defaultStatus(in.Status, models.ActivityStatusDefault)
		in.Progress = models.ClampProgress(in.Progress)
		in.CreatedAt = today()
		in.UpdatedAt = in.CreatedAt
		created = in
		d.Activities = append(d.Activities, in)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"activity": created, "version": ws.Version})
}

func (h *Entities) patchActivity(c *gin.Context) {
	id := c.Param("id")
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if v, ok := patch["status"].(string); ok && !validWBS(c, v, models.ActivityStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var updated models.Activity
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if v, ok := patch["phaseId"].(string); ok && v != "" && !hasPhase(d, v) {
			return fmt.Errorf("phase not found")
		}
		for i := range d.Activities {
			if d.Activities[i].ID != id {
				continue
			}
			applyActivityPatch(&d.Activities[i], patch)
			d.Activities[i].UpdatedAt = today()
			updated = d.Activities[i]
			return nil
		}
		return fmt.Errorf("activity not found")
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"activity": updated, "version": ws.Version})
}

func (h *Entities) deleteActivity(c *gin.Context) {
	id := c.Param("id")
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Activities[:0]
		found := false
		for _, row := range d.Activities {
			if row.ID == id {
				found = true
				continue
			}
			out = append(out, row)
		}
		if !found {
			return fmt.Errorf("activity not found")
		}
		d.Activities = out
		for i := range d.WorkPrograms {
			if d.WorkPrograms[i].ActivityID == id {
				d.WorkPrograms[i].ActivityID = ""
			}
		}
		return nil
	})
}

func applyActivityPatch(a *models.Activity, patch map[string]any) {
	if v, ok := patch["name"].(string); ok && v != "" {
		a.Name = v
	}
	if v, ok := patch["projectId"].(string); ok {
		a.ProjectID = v
	}
	if v, ok := patch["phaseId"].(string); ok {
		a.PhaseID = v
	}
	if v, ok := patch["code"].(string); ok {
		a.Code = v
	}
	if v, ok := patch["startDate"].(string); ok {
		a.StartDate = v
	}
	if v, ok := patch["dueDate"].(string); ok {
		a.DueDate = v
	}
	if v, ok := patch["status"].(string); ok && v != "" {
		a.Status = v
	}
	if v, ok := patch["desc"].(string); ok {
		a.Desc = v
	}
	if v, ok := patch["progress"].(float64); ok {
		a.Progress = models.ClampProgress(int(v))
	}
	if v, ok := patch["assignee"].(string); ok {
		a.Assignee = v
	}
}

/* ───────────────────────── work programs ────────────────────────── */

func (h *Entities) listWorkPrograms(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	project := strings.TrimSpace(c.Query("project"))
	activity := strings.TrimSpace(c.Query("activity"))
	rows := make([]models.WorkProgram, 0, len(doc.WorkPrograms))
	for _, row := range doc.WorkPrograms {
		if project != "" && row.ProjectID != project {
			continue
		}
		if activity != "" && row.ActivityID != activity {
			continue
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, gin.H{"workPrograms": rows})
}

func (h *Entities) createWorkProgram(c *gin.Context) {
	var in models.WorkProgram
	if err := bindJSONCoerced(c, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if !validWBS(c, in.Status, models.WorkProgramStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.WorkProgram
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if in.ProjectID != "" {
			if _, ok := d.Projects[in.ProjectID]; !ok {
				return fmt.Errorf("project not found")
			}
		}
		if in.PhaseID != "" && !hasPhase(d, in.PhaseID) {
			return fmt.Errorf("phase not found")
		}
		if in.ActivityID != "" && !hasActivity(d, in.ActivityID) {
			return fmt.Errorf("activity not found")
		}
		if in.ID == "" {
			in.ID = nextStringID("WP", d.WorkPrograms, func(w models.WorkProgram) string { return w.ID })
		}
		in.Status = defaultStatus(in.Status, models.WorkProgramStatusDefault)
		in.CreatedAt = today()
		in.UpdatedAt = in.CreatedAt
		created = in
		d.WorkPrograms = append(d.WorkPrograms, in)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"workProgram": created, "version": ws.Version})
}

func (h *Entities) patchWorkProgram(c *gin.Context) {
	id := c.Param("id")
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	if v, ok := patch["status"].(string); ok && !validWBS(c, v, models.WorkProgramStatuses) {
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var updated models.WorkProgram
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if v, ok := patch["activityId"].(string); ok && v != "" && !hasActivity(d, v) {
			return fmt.Errorf("activity not found")
		}
		for i := range d.WorkPrograms {
			if d.WorkPrograms[i].ID != id {
				continue
			}
			applyWorkProgramPatch(&d.WorkPrograms[i], patch)
			d.WorkPrograms[i].UpdatedAt = today()
			updated = d.WorkPrograms[i]
			return nil
		}
		return fmt.Errorf("work program not found")
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"workProgram": updated, "version": ws.Version})
}

func (h *Entities) deleteWorkProgram(c *gin.Context) {
	id := c.Param("id")
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.WorkPrograms[:0]
		found := false
		for _, row := range d.WorkPrograms {
			if row.ID == id {
				found = true
				continue
			}
			out = append(out, row)
		}
		if !found {
			return fmt.Errorf("work program not found")
		}
		d.WorkPrograms = out
		return nil
	})
}

func applyWorkProgramPatch(w *models.WorkProgram, patch map[string]any) {
	if v, ok := patch["name"].(string); ok && v != "" {
		w.Name = v
	}
	if v, ok := patch["projectId"].(string); ok {
		w.ProjectID = v
	}
	if v, ok := patch["phaseId"].(string); ok {
		w.PhaseID = v
	}
	if v, ok := patch["activityId"].(string); ok {
		w.ActivityID = v
	}
	if v, ok := patch["scheduledStart"].(string); ok {
		w.ScheduledStart = v
	}
	if v, ok := patch["scheduledEnd"].(string); ok {
		w.ScheduledEnd = v
	}
	if v, ok := patch["assignedTo"].(string); ok {
		w.AssignedTo = v
	}
	if v, ok := patch["status"].(string); ok && v != "" {
		w.Status = v
	}
	if v, ok := patch["desc"].(string); ok {
		w.Desc = v
	}
}

func hasPhase(d *models.Document, id string) bool {
	for _, row := range d.Phases {
		if row.ID == id {
			return true
		}
	}
	return false
}

func hasActivity(d *models.Document, id string) bool {
	for _, row := range d.Activities {
		if row.ID == id {
			return true
		}
	}
	return false
}
