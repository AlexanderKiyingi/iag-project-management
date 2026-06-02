package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

// deleteTask soft-deletes a single task by default. Pass ?permanent=true
// to hard-delete (and cascade through comments / dependencies / subtasks).
func (h *Entities) deleteTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	permanent := c.Query("permanent") == "true"
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		idx := -1
		for i := range d.Tasks {
			if d.Tasks[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("task not found")
		}
		if permanent {
			cascadeRemoveTasks(d, map[int]bool{id: true})
			models.AppendAudit(d, actor, "task.deleted", fmt.Sprintf("hard-deleted task #%d", id), &id)
			return nil
		}
		d.Tasks[idx].DeletedAt = models.ISONow()
		models.AppendAudit(d, actor, "task.soft_deleted", fmt.Sprintf("soft-deleted task #%d", id), &id)
		return nil
	})
}

func (h *Entities) restoreTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			if d.Tasks[i].DeletedAt == "" {
				return nil
			}
			d.Tasks[i].DeletedAt = ""
			models.AppendAudit(d, actor, "task.restored", fmt.Sprintf("restored task #%d", id), &id)
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

// cascadeRemoveTasks hard-deletes the given task IDs along with their
// dependent records: comments, subtasks, dependsOn references on other
// tasks, and audit linkage. Sectioning/legacy fields are left in place
// because they're carried as denormalized strings on each task.
func cascadeRemoveTasks(d *models.Document, ids map[int]bool) {
	if len(ids) == 0 {
		return
	}
	// Tasks themselves.
	keptTasks := d.Tasks[:0]
	for _, t := range d.Tasks {
		if ids[t.ID] {
			continue
		}
		// Drop dangling dependsOn references to the removed tasks.
		if len(t.DependsOn) > 0 {
			out := t.DependsOn[:0]
			for _, dep := range t.DependsOn {
				if ids[dep] {
					continue
				}
				out = append(out, dep)
			}
			t.DependsOn = out
		}
		keptTasks = append(keptTasks, t)
	}
	d.Tasks = keptTasks

	// Task comments.
	if len(d.TaskComments) > 0 {
		keptComments := d.TaskComments[:0]
		for _, c := range d.TaskComments {
			if ids[c.TaskID] {
				continue
			}
			keptComments = append(keptComments, c)
		}
		d.TaskComments = keptComments
	}

	// Subtask entities.
	if len(d.Subtasks) > 0 {
		keptSubtasks := d.Subtasks[:0]
		for _, s := range d.Subtasks {
			if ids[s.ParentTaskID] {
				continue
			}
			keptSubtasks = append(keptSubtasks, s)
		}
		d.Subtasks = keptSubtasks
	}
}
