package handlers

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) listProjects(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	projects := make([]models.Project, 0, len(doc.Projects))
	for _, p := range doc.Projects {
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (h *Entities) deleteProject(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		if d.Projects == nil {
			return fmt.Errorf("project not found")
		}
		if _, ok := d.Projects[id]; !ok {
			return fmt.Errorf("project not found")
		}
		delete(d.Projects, id)
		// Detach tasks from the removed project.
		for i := range d.Tasks {
			normalizeTaskProjects(&d.Tasks[i])
			out := d.Tasks[i].Projects[:0]
			for _, p := range d.Tasks[i].Projects {
				if p != id {
					out = append(out, p)
				}
			}
			d.Tasks[i].Projects = out
			if d.Tasks[i].Project == id {
				d.Tasks[i].Project = ""
			}
			normalizeTaskProjects(&d.Tasks[i])
		}
		return nil
	})
}

func (h *Entities) listRequisitions(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"requisitions": doc.Requisitions})
}
