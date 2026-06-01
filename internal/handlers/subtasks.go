package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Entities) createSubtask(c *gin.Context) {
	parentID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Assignee string `json:"assignee"`
		Due      string `json:"due"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.Subtask
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		parent := findTaskIndex(d, parentID)
		if parent < 0 {
			return fmt.Errorf("task not found")
		}
		order := 0
		for _, s := range d.Subtasks {
			if s.ParentTaskID == parentID && s.Order >= order {
				order = s.Order + 1
			}
		}
		created = models.Subtask{
			ID:           models.NextSubtaskID(d),
			ParentTaskID: parentID,
			Name:         body.Name,
			Assignee:     body.Assignee,
			Due:          body.Due,
			Order:        order,
		}
		d.Subtasks = append(d.Subtasks, created)
		// Legacy mirror: keep Task.Subtasks []string in sync so older
		// frontend builds still see subtask names attached to the parent.
		d.Tasks[parent].Subtasks = append(d.Tasks[parent].Subtasks, body.Name)
		stid := created.ID
		models.AppendAudit(d, actor, "subtask.created", fmt.Sprintf("added subtask %q to task #%d", body.Name, parentID), &stid)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"subtask": created, "version": ws.Version})
}

func (h *Entities) patchSubtask(c *gin.Context) {
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
		for i := range d.Subtasks {
			if d.Subtasks[i].ID != id {
				continue
			}
			oldName := d.Subtasks[i].Name
			applySubtaskPatch(&d.Subtasks[i], patch)
			if d.Subtasks[i].Name != oldName {
				// Re-sync the legacy mirror entry on the parent task.
				if pi := findTaskIndex(d, d.Subtasks[i].ParentTaskID); pi >= 0 {
					for j, n := range d.Tasks[pi].Subtasks {
						if n == oldName {
							d.Tasks[pi].Subtasks[j] = d.Subtasks[i].Name
							break
						}
					}
				}
			}
			return nil
		}
		return fmt.Errorf("subtask not found")
	})
}

func (h *Entities) deleteSubtask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		var removed *models.Subtask
		out := d.Subtasks[:0]
		for _, s := range d.Subtasks {
			if s.ID == id {
				s := s
				removed = &s
				continue
			}
			out = append(out, s)
		}
		d.Subtasks = out
		if removed == nil {
			return fmt.Errorf("subtask not found")
		}
		if pi := findTaskIndex(d, removed.ParentTaskID); pi >= 0 {
			kept := d.Tasks[pi].Subtasks[:0]
			for _, n := range d.Tasks[pi].Subtasks {
				if n == removed.Name {
					// drop one occurrence
					removed.Name = ""
					continue
				}
				kept = append(kept, n)
			}
			d.Tasks[pi].Subtasks = kept
		}
		return nil
	})
}

func (h *Entities) reorderSubtasks(c *gin.Context) {
	parentID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body struct {
		Order []int `json:"order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Order) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		index := map[int]int{}
		for pos, id := range body.Order {
			index[id] = pos
		}
		matched := 0
		for i := range d.Subtasks {
			if d.Subtasks[i].ParentTaskID != parentID {
				continue
			}
			if pos, ok := index[d.Subtasks[i].ID]; ok {
				d.Subtasks[i].Order = pos
				matched++
			}
		}
		if matched == 0 {
			return fmt.Errorf("no subtasks matched")
		}
		return nil
	})
}

func applySubtaskPatch(s *models.Subtask, patch map[string]any) {
	if v, ok := patch["name"].(string); ok {
		s.Name = v
	}
	if v, ok := patch["assignee"].(string); ok {
		s.Assignee = v
	}
	if v, ok := patch["due"].(string); ok {
		s.Due = v
	}
	if v, ok := patch["done"].(bool); ok {
		s.Done = v
	}
	if v, ok := patch["order"].(float64); ok {
		s.Order = int(v)
	}
}

func findTaskIndex(d *models.Document, taskID int) int {
	for i := range d.Tasks {
		if d.Tasks[i].ID == taskID {
			return i
		}
	}
	return -1
}
