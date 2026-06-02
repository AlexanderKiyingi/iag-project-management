package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
	"github.com/iag/project-management/backend/internal/workspace"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) listForms(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": doc.Forms})
}

func (h *Entities) createForm(c *gin.Context) {
	var body struct {
		Name        string             `json:"name"`
		Description string             `json:"description"`
		Slug        string             `json:"slug"`
		Fields      []models.FormField `json:"fields"`
		ProjectID   string             `json:"projectId"`
		Public      bool               `json:"public"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" {
		// Auto-generate a globally-unique slug so public lookup
		// across workspaces stays safe.
		slug = "f-" + strings.Split(uuid.NewString(), "-")[0]
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	now := models.ISONow()
	var created models.Form
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		for _, f := range d.Forms {
			if f.Slug == slug {
				return fmt.Errorf("slug already in use")
			}
		}
		created = models.Form{
			ID:          models.NextFormID(d),
			Slug:        slug,
			Name:        strings.TrimSpace(body.Name),
			Description: strings.TrimSpace(body.Description),
			Fields:      body.Fields,
			ProjectID:   strings.TrimSpace(body.ProjectID),
			Public:      body.Public,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		d.Forms = append(d.Forms, created)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"form": created, "version": ws.Version})
}

func (h *Entities) patchForm(c *gin.Context) {
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
		for i := range d.Forms {
			if d.Forms[i].ID != id {
				continue
			}
			if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
				d.Forms[i].Name = strings.TrimSpace(v)
			}
			if v, ok := patch["description"].(string); ok {
				d.Forms[i].Description = strings.TrimSpace(v)
			}
			if v, ok := patch["public"].(bool); ok {
				d.Forms[i].Public = v
			}
			if v, ok := patch["projectId"].(string); ok {
				d.Forms[i].ProjectID = strings.TrimSpace(v)
			}
			if raw, ok := patch["fields"].([]any); ok {
				b, err := json.Marshal(raw)
				if err == nil {
					var fields []models.FormField
					if json.Unmarshal(b, &fields) == nil {
						d.Forms[i].Fields = fields
					}
				}
			}
			d.Forms[i].UpdatedAt = models.ISONow()
			return nil
		}
		return fmt.Errorf("form not found")
	})
}

func (h *Entities) deleteForm(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Forms[:0]
		removed := false
		for _, f := range d.Forms {
			if f.ID == id {
				removed = true
				continue
			}
			out = append(out, f)
		}
		if !removed {
			return fmt.Errorf("form not found")
		}
		d.Forms = out
		return nil
	})
}

// PublicForms registers the unauthenticated POST /public/forms/:slug
// endpoint. It is registered at the engine level (not under the
// authenticated /api/v1 group) because submissions come from external
// stakeholders who don't have platform credentials.
type PublicForms struct {
	Repo *store.Repository
	Svc  *workspace.Service
}

func (h *PublicForms) Register(r *gin.Engine) {
	r.POST("/public/forms/:slug", h.submit)
	r.GET("/public/forms/:slug", h.describe)
}

// describe returns enough metadata about the form for the public
// frontend to render the input shape. Hides everything except the
// fields list, the name, and the description.
func (h *PublicForms) describe(c *gin.Context) {
	slug := strings.TrimSpace(c.Param("slug"))
	if slug == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid slug")
		return
	}
	_, form, err := findFormBySlug(c.Request.Context(), h.Repo, slug)
	if err != nil {
		apierr.JSONStatus(c, http.StatusNotFound, "form not found")
		return
	}
	if !form.Public {
		apierr.JSONStatus(c, http.StatusForbidden, "form is not public")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"form": gin.H{
			"slug":        form.Slug,
			"name":        form.Name,
			"description": form.Description,
			"fields":      form.Fields,
		},
	})
}

func (h *PublicForms) submit(c *gin.Context) {
	slug := strings.TrimSpace(c.Param("slug"))
	if slug == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid slug")
		return
	}
	owner, form, err := findFormBySlug(c.Request.Context(), h.Repo, slug)
	if err != nil {
		apierr.JSONStatus(c, http.StatusNotFound, "form not found")
		return
	}
	if !form.Public {
		apierr.JSONStatus(c, http.StatusForbidden, "form is not public")
		return
	}
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	// Enforce required fields.
	for _, f := range form.Fields {
		if f.Required && strings.TrimSpace(body.Values[f.ID]) == "" {
			apierr.JSONStatus(c, http.StatusBadRequest, "missing required field: "+f.ID)
			return
		}
	}
	title := strings.TrimSpace(body.Values["title"])
	if title == "" {
		title = form.Name + " submission"
	}
	descParts := make([]string, 0, len(form.Fields))
	for _, f := range form.Fields {
		v := strings.TrimSpace(body.Values[f.ID])
		if v == "" {
			continue
		}
		descParts = append(descParts, f.Label+": "+v)
	}
	created := models.Task{}
	ws, err := h.Svc.Mutate(c.Request.Context(), owner, func(d *models.Document) error {
		created = models.Task{
			ID:           models.NextTaskID(d),
			Name:         title,
			Desc:         strings.Join(descParts, "\n"),
			Project:      form.ProjectID,
			Status:       "on_track",
			Tags:         []string{"intake"},
			DependsOn:    []int{},
			CustomValues: map[string]string{},
			Activity:     []models.ActivityEntry{},
		}
		normalizeTaskProjects(&created)
		d.Tasks = append(d.Tasks, created)
		tid := created.ID
		models.AppendAudit(d, "public:"+form.Slug, "task.created",
			fmt.Sprintf("created task %q via public form %q", created.Name, form.Slug), &tid)
		return nil
	})
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "submission failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"task":    gin.H{"id": created.ID, "name": created.Name},
		"version": ws.Version,
	})
}

// findFormBySlug walks every workspace looking for the slug. Acceptable
// because slug uniqueness is enforced at form creation and PM
// workspaces are bounded; if the platform grows we can index slugs in
// a dedicated table later without changing this interface.
func findFormBySlug(ctx context.Context, repo *store.Repository, slug string) (string, models.Form, error) {
	workspaces, err := repo.ListWorkspaces(ctx)
	if err != nil {
		return "", models.Form{}, err
	}
	for _, ws := range workspaces {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			continue
		}
		for _, f := range doc.Forms {
			if f.Slug == slug {
				return ws.OwnerUserID, f, nil
			}
		}
	}
	return "", models.Form{}, fmt.Errorf("form not found")
}
