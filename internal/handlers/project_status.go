package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Entities) postProjectStatus(c *gin.Context) {
	projectID := c.Param("id")
	if strings.TrimSpace(projectID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}
	var body struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	normalized := normalizeProjectStatus(body.Status)
	if normalized == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "status must be on_track | at_risk | off_track"})
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.ProjectStatusUpdate
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		project, ok := d.Projects[projectID]
		if !ok {
			return fmt.Errorf("project not found")
		}
		created = models.ProjectStatusUpdate{
			ID:      nextProjectStatusID(project),
			Author:  actor,
			Status:  normalized,
			Summary: strings.TrimSpace(body.Summary),
			Time:    models.ISONow(),
		}
		project.StatusHistory = append(project.StatusHistory, created)
		// Project.Status mirrors the latest update so the legacy frontend
		// reading `project.status` keeps showing the current health.
		project.Status = normalized
		d.Projects[projectID] = project
		models.AppendAudit(d, actor, "project.status.posted",
			fmt.Sprintf("project %s set to %s", projectID, normalized), nil)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"statusUpdate": created, "version": ws.Version})
}

func (h *Entities) listProjectStatus(c *gin.Context) {
	projectID := c.Param("id")
	if strings.TrimSpace(projectID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	project, ok := doc.Projects[projectID]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
		return
	}
	history := project.StatusHistory
	// Return newest first; preserve the on-disk order (oldest→newest) by
	// iterating from the tail.
	out := make([]models.ProjectStatusUpdate, 0, len(history))
	for i := len(history) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, history[i])
	}
	c.JSON(http.StatusOK, gin.H{"statusHistory": out})
}

func normalizeProjectStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case models.ProjectStatusOnTrack:
		return models.ProjectStatusOnTrack
	case models.ProjectStatusAtRisk:
		return models.ProjectStatusAtRisk
	case models.ProjectStatusOffTrack:
		return models.ProjectStatusOffTrack
	default:
		return ""
	}
}

func nextProjectStatusID(p models.Project) int {
	max := 0
	for _, u := range p.StatusHistory {
		if u.ID > max {
			max = u.ID
		}
	}
	return max + 1
}
