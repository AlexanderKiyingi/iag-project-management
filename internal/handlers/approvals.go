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

// approveTask records the actor's approval on an approval-type task.
// When every named approver has approved, the task transitions to
// Done=true and Status="completed", surfacing as a finished task in
// every existing filter and view.
func (h *Entities) approveTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	actor := strings.TrimSpace(c.GetHeader("X-Workspace-User"))
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			t := &d.Tasks[i]
			if t.Type != models.TaskTypeApproval {
				return fmt.Errorf("task is not an approval task")
			}
			if !contains(t.Approvers, actor) {
				return fmt.Errorf("actor is not in approver list")
			}
			if contains(t.ApprovedBy, actor) {
				return nil // idempotent
			}
			t.ApprovedBy = append(t.ApprovedBy, actor)
			models.AppendAudit(d, actor, "task.approved",
				fmt.Sprintf("approved task #%d (%d/%d)", id, len(t.ApprovedBy), len(t.Approvers)), &id)
			if len(t.ApprovedBy) >= len(t.Approvers) {
				t.Done = true
				t.Status = "completed"
				models.AppendAudit(d, actor, "task.completed",
					fmt.Sprintf("approval threshold met for task #%d", id), &id)
			}
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

// rejectTask records the actor's rejection. A single rejection is
// fatal: the task is marked off-track and reset so the requester can
// see who blocked it. ApprovedBy is cleared so a re-submit (clearing
// the rejection via setApprovers) starts fresh.
func (h *Entities) rejectTask(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	actor := strings.TrimSpace(c.GetHeader("X-Workspace-User"))
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	reason := strings.TrimSpace(body.Reason)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			t := &d.Tasks[i]
			if t.Type != models.TaskTypeApproval {
				return fmt.Errorf("task is not an approval task")
			}
			if !contains(t.Approvers, actor) {
				return fmt.Errorf("actor is not in approver list")
			}
			t.Status = "off_track"
			t.Done = false
			t.ApprovedBy = nil
			text := fmt.Sprintf("rejected task #%d", id)
			if reason != "" {
				text += ": " + reason
			}
			models.AppendAudit(d, actor, "task.rejected", text, &id)
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

// setApprovers replaces the approver list on a task. Approvers already
// in ApprovedBy are preserved; anyone dropped from the list also gets
// cleared out of ApprovedBy so the completion math stays consistent.
func (h *Entities) setApprovers(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Approvers []string `json:"approvers"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	approvers := dedupeNonEmpty(body.Approvers)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Tasks {
			if d.Tasks[i].ID != id {
				continue
			}
			t := &d.Tasks[i]
			t.Approvers = approvers
			// Drop any prior approvers no longer in the list.
			kept := t.ApprovedBy[:0]
			for _, who := range t.ApprovedBy {
				if contains(approvers, who) {
					kept = append(kept, who)
				}
			}
			t.ApprovedBy = kept
			// If we already meet the new threshold, complete the task.
			if t.Type == models.TaskTypeApproval && len(approvers) > 0 &&
				len(t.ApprovedBy) >= len(approvers) {
				t.Done = true
				t.Status = "completed"
			}
			return nil
		}
		return fmt.Errorf("task not found")
	})
}

func dedupeNonEmpty(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
