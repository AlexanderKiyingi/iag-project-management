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

func (h *Entities) createSection(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("id"))
	if projectID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid project id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.Section
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if _, exists := d.Projects[projectID]; !exists {
			return fmt.Errorf("project not found")
		}
		order := 0
		for _, s := range d.Sections {
			if s.ProjectID == projectID && s.Order >= order {
				order = s.Order + 1
			}
		}
		created = models.Section{
			ID:        models.NextSectionID(d),
			ProjectID: projectID,
			Name:      strings.TrimSpace(body.Name),
			Order:     order,
		}
		d.Sections = append(d.Sections, created)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"section": created, "version": ws.Version})
}

func (h *Entities) patchSection(c *gin.Context) {
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
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Sections {
			if d.Sections[i].ID != id {
				continue
			}
			oldName := d.Sections[i].Name
			if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
				d.Sections[i].Name = strings.TrimSpace(v)
			}
			if v, ok := patch["order"].(float64); ok {
				d.Sections[i].Order = int(v)
			}
			// If the name changed, refresh the legacy Task.Section
			// mirror on every task currently anchored by name within
			// this project.
			if newName := d.Sections[i].Name; newName != oldName && oldName != "" {
				for t := range d.Tasks {
					if d.Tasks[t].Project != d.Sections[i].ProjectID {
						continue
					}
					if d.Tasks[t].Section == oldName || d.Tasks[t].SectionID == d.Sections[i].ID {
						d.Tasks[t].Section = newName
					}
				}
			}
			return nil
		}
		return fmt.Errorf("section not found")
	})
}

func (h *Entities) deleteSection(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		var removed *models.Section
		out := d.Sections[:0]
		for i := range d.Sections {
			if d.Sections[i].ID == id {
				s := d.Sections[i]
				removed = &s
				continue
			}
			out = append(out, d.Sections[i])
		}
		if removed == nil {
			return fmt.Errorf("section not found")
		}
		d.Sections = out
		// Detach tasks that referenced this section by id; leave the
		// legacy name in place so the frontend can show them in a
		// fallback bucket until the user reassigns them.
		for ti := range d.Tasks {
			if d.Tasks[ti].SectionID == removed.ID {
				d.Tasks[ti].SectionID = 0
			}
		}
		return nil
	})
}

func (h *Entities) reorderSections(c *gin.Context) {
	projectID := strings.TrimSpace(c.Param("id"))
	if projectID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid project id")
		return
	}
	var body struct {
		Order []int `json:"order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Order) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		index := map[int]int{}
		for pos, sid := range body.Order {
			index[sid] = pos
		}
		matched := 0
		for i := range d.Sections {
			if d.Sections[i].ProjectID != projectID {
				continue
			}
			if pos, ok := index[d.Sections[i].ID]; ok {
				d.Sections[i].Order = pos
				matched++
			}
		}
		if matched == 0 {
			return fmt.Errorf("no sections matched")
		}
		return nil
	})
}
