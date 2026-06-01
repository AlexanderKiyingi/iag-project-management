package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Entities) listRules(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": doc.Rules})
}

type ruleInput struct {
	Name       string                 `json:"name"`
	Enabled    *bool                  `json:"enabled"`
	Trigger    string                 `json:"trigger"`
	Conditions []models.RuleCondition `json:"conditions"`
	Actions    []models.RuleAction    `json:"actions"`
}

func (h *Entities) createRule(c *gin.Context) {
	var in ruleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if msg := validateRuleInput(in); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := models.ISONow()
	var created models.Rule
	mutate(c, h.Svc, func(d *models.Document) error {
		created = models.Rule{
			ID:         models.NextRuleID(d),
			Name:       strings.TrimSpace(in.Name),
			Enabled:    enabled,
			Trigger:    in.Trigger,
			Conditions: in.Conditions,
			Actions:    in.Actions,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		d.Rules = append(d.Rules, created)
		return nil
	})
	// mutate writes the workspace envelope; the rule is in d.Rules now.
	_ = created
}

func (h *Entities) patchRule(c *gin.Context) {
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
		for i := range d.Rules {
			if d.Rules[i].ID != id {
				continue
			}
			if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
				d.Rules[i].Name = strings.TrimSpace(v)
			}
			if v, ok := patch["enabled"].(bool); ok {
				d.Rules[i].Enabled = v
			}
			if v, ok := patch["trigger"].(string); ok && isValidTrigger(v) {
				d.Rules[i].Trigger = v
			}
			d.Rules[i].UpdatedAt = models.ISONow()
			return nil
		}
		return fmt.Errorf("rule not found")
	})
}

func (h *Entities) deleteRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Rules[:0]
		removed := false
		for _, r := range d.Rules {
			if r.ID == id {
				removed = true
				continue
			}
			out = append(out, r)
		}
		if !removed {
			return fmt.Errorf("rule not found")
		}
		d.Rules = out
		return nil
	})
}

func validateRuleInput(in ruleInput) string {
	if strings.TrimSpace(in.Name) == "" {
		return "name is required"
	}
	if !isValidTrigger(in.Trigger) {
		return "trigger must be one of task.created, task.status_changed, task.assignee_changed, comment.created"
	}
	if len(in.Actions) == 0 {
		return "at least one action is required"
	}
	for _, a := range in.Actions {
		if !isValidAction(a.Type) {
			return "unsupported action type: " + a.Type
		}
	}
	return ""
}

func isValidTrigger(t string) bool {
	switch t {
	case models.TriggerTaskCreated,
		models.TriggerTaskStatusChanged,
		models.TriggerTaskAssigneeChanged,
		models.TriggerCommentCreated:
		return true
	default:
		return false
	}
}

func isValidAction(t string) bool {
	switch t {
	case models.ActionAssignTo,
		models.ActionSetStatus,
		models.ActionSetDueOffset,
		models.ActionAddTag,
		models.ActionCreateSubtask,
		models.ActionPostComment,
		models.ActionNotify:
		return true
	default:
		return false
	}
}
