package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) putCustomField(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		Options []string `json:"options"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	ftype := strings.ToLower(strings.TrimSpace(body.Type))
	if !models.ValidCustomFieldType(ftype) {
		apierr.JSONStatus(c, http.StatusBadRequest, "unsupported custom field type")
		return
	}
	if (ftype == models.CustomFieldTypeSelect || ftype == models.CustomFieldTypeMultiSelect) && len(body.Options) == 0 {
		apierr.JSONStatus(c, http.StatusBadRequest, "select fields require options")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		def := models.TaskCustomFieldDef{
			ID:      id,
			Name:    strings.TrimSpace(body.Name),
			Type:    ftype,
			Options: body.Options,
		}
		for i := range d.TaskCustomFieldDefs {
			if d.TaskCustomFieldDefs[i].ID == id {
				d.TaskCustomFieldDefs[i] = def
				return nil
			}
		}
		d.TaskCustomFieldDefs = append(d.TaskCustomFieldDefs, def)
		return nil
	})
}

func (h *Entities) deleteCustomField(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.TaskCustomFieldDefs[:0]
		removed := false
		for _, def := range d.TaskCustomFieldDefs {
			if def.ID == id {
				removed = true
				continue
			}
			out = append(out, def)
		}
		if !removed {
			return fmt.Errorf("custom field not found")
		}
		d.TaskCustomFieldDefs = out
		// Clear stored values for the removed field across all tasks so
		// historical map entries don't ghost in the UI.
		for ti := range d.Tasks {
			if _, ok := d.Tasks[ti].CustomValues[id]; ok {
				delete(d.Tasks[ti].CustomValues, id)
			}
		}
		return nil
	})
}

// validateCustomFieldValue returns an empty string if value matches the
// definition's type, otherwise a human-readable error suitable for the
// 400 response. Unknown/untyped fields default to text (no validation).
func validateCustomFieldValue(def models.TaskCustomFieldDef, value string) string {
	t := def.Type
	if t == "" {
		t = models.CustomFieldTypeText
	}
	v := strings.TrimSpace(value)
	switch t {
	case models.CustomFieldTypeText, models.CustomFieldTypePeople:
		return ""
	case models.CustomFieldTypeNumber:
		if v == "" {
			return ""
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return "value must be a number"
		}
	case models.CustomFieldTypeDate:
		if v == "" {
			return ""
		}
		// Accept either ISO date (2026-06-01) or full RFC3339.
		if _, err := time.Parse("2006-01-02", v); err == nil {
			return ""
		}
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return "value must be an ISO date or RFC3339 timestamp"
		}
	case models.CustomFieldTypeSelect:
		if v == "" {
			return ""
		}
		if !contains(def.Options, v) {
			return "value not in options"
		}
	case models.CustomFieldTypeMultiSelect:
		if v == "" {
			return ""
		}
		for _, candidate := range strings.Split(v, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if !contains(def.Options, candidate) {
				return "value contains entries not in options"
			}
		}
	}
	return ""
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
