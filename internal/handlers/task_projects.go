package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

// taskHomedIn reports whether a task is anchored to the given project,
// considering both the legacy Task.Project string and the new
// Task.Projects []string.
func taskHomedIn(t models.Task, projectID string) bool {
	if projectID == "" {
		return false
	}
	if t.Project == projectID {
		return true
	}
	for _, p := range t.Projects {
		if p == projectID {
			return true
		}
	}
	return false
}

// normalizeTaskProjects keeps the legacy Task.Project string and the new
// Task.Projects []string in sync. It is called from every handler that
// mutates either field. Empty Projects with non-empty Project is
// upgraded to a single-element slice (lazy migration). An update to
// either side rewrites the other.
func normalizeTaskProjects(t *models.Task) {
	if t == nil {
		return
	}
	switch {
	case len(t.Projects) == 0 && t.Project != "":
		t.Projects = []string{t.Project}
	case len(t.Projects) > 0:
		// Dedupe while preserving order.
		seen := map[string]struct{}{}
		out := t.Projects[:0]
		for _, p := range t.Projects {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
		t.Projects = out
		if len(t.Projects) > 0 {
			t.Project = t.Projects[0]
		} else {
			t.Project = ""
		}
	}
}

func (h *Entities) addTaskProject(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid task id")
		return
	}
	var body struct {
		ProjectID string `json:"projectId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.ProjectID) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		if _, ok := d.Projects[body.ProjectID]; !ok {
			return fmt.Errorf("project not found")
		}
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			normalizeTaskProjects(&d.Tasks[i])
			d.Tasks[i].Projects = append(d.Tasks[i].Projects, body.ProjectID)
			normalizeTaskProjects(&d.Tasks[i])
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func (h *Entities) removeTaskProject(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid task id")
		return
	}
	projectID := strings.TrimSpace(c.Param("projectId"))
	if projectID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid project id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			normalizeTaskProjects(&d.Tasks[i])
			if len(d.Tasks[i].Projects) <= 1 {
				return fmt.Errorf("cannot remove a task's only project")
			}
			out := d.Tasks[i].Projects[:0]
			for _, p := range d.Tasks[i].Projects {
				if p == projectID {
					continue
				}
				out = append(out, p)
			}
			d.Tasks[i].Projects = out
			normalizeTaskProjects(&d.Tasks[i])
			return nil
		}
		return fmt.Errorf("task not found")
	})
}
