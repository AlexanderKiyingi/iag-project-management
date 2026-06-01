package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Entities) listWebhooks(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	// Redact the secret on list so it isn't accidentally exposed.
	redacted := make([]models.WebhookSubscription, 0, len(doc.Webhooks))
	for _, w := range doc.Webhooks {
		w.Secret = "***"
		redacted = append(redacted, w)
	}
	c.JSON(http.StatusOK, gin.H{"items": redacted})
}

func (h *Entities) createWebhook(c *gin.Context) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.URL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if !strings.HasPrefix(body.URL, "https://") && !strings.HasPrefix(body.URL, "http://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url must be http(s)"})
		return
	}
	secret := strings.TrimSpace(body.Secret)
	if secret == "" {
		secret = generateSecret()
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	now := models.ISONow()
	var created models.WebhookSubscription
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		created = models.WebhookSubscription{
			ID:          models.NextWebhookID(d),
			OwnerUserID: uid,
			URL:         strings.TrimSpace(body.URL),
			Secret:      secret,
			Events:      dedupeNonEmpty(body.Events),
			Active:      true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		d.Webhooks = append(d.Webhooks, created)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	// Return the secret ONCE on create so the operator can save it.
	c.JSON(http.StatusOK, gin.H{"webhook": created, "version": ws.Version})
}

func (h *Entities) patchWebhook(c *gin.Context) {
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
		for i := range d.Webhooks {
			if d.Webhooks[i].ID != id {
				continue
			}
			if v, ok := patch["url"].(string); ok && strings.TrimSpace(v) != "" {
				d.Webhooks[i].URL = strings.TrimSpace(v)
			}
			if v, ok := patch["active"].(bool); ok {
				d.Webhooks[i].Active = v
				if v {
					d.Webhooks[i].FailureCount = 0
				}
			}
			if raw, ok := patch["events"].([]any); ok {
				out := make([]string, 0, len(raw))
				for _, x := range raw {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
				d.Webhooks[i].Events = dedupeNonEmpty(out)
			}
			d.Webhooks[i].UpdatedAt = models.ISONow()
			return nil
		}
		return fmt.Errorf("webhook not found")
	})
}

func (h *Entities) deleteWebhook(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Webhooks[:0]
		removed := false
		for _, w := range d.Webhooks {
			if w.ID == id {
				removed = true
				continue
			}
			out = append(out, w)
		}
		if !removed {
			return fmt.Errorf("webhook not found")
		}
		d.Webhooks = out
		return nil
	})
}

func generateSecret() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
