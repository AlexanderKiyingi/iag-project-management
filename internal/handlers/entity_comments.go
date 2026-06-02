package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/mentions"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) addEntityCommentProject(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid project id")
		return
	}
	h.addEntityComment(c, models.EntityCommentProject, id, func(d *models.Document) bool {
		_, ok := d.Projects[id]
		return ok
	})
}

func (h *Entities) addEntityCommentGoal(c *gin.Context) {
	idInt, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid goal id")
		return
	}
	id := strconv.Itoa(idInt)
	h.addEntityComment(c, models.EntityCommentGoal, id, func(d *models.Document) bool {
		return findGoalIndex(d, idInt) >= 0
	})
}

func (h *Entities) addEntityCommentSprint(c *gin.Context) {
	idInt, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid sprint id")
		return
	}
	id := strconv.Itoa(idInt)
	h.addEntityComment(c, models.EntityCommentSprint, id, func(d *models.Document) bool {
		for _, s := range d.Sprints {
			if s.ID == idInt {
				return true
			}
		}
		return false
	})
}

func (h *Entities) addEntityComment(c *gin.Context, entityType, entityID string, exists func(*models.Document) bool) {
	var body struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	actor := c.GetHeader("X-Workspace-User")
	parsed := mentions.Parse(body.Text)
	var created models.EntityComment
	var membersSnapshot []models.Member
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		if !exists(d) {
			return fmt.Errorf("%s not found", entityType)
		}
		created = models.EntityComment{
			ID:         models.NextEntityCommentID(d),
			EntityType: entityType,
			EntityID:   entityID,
			Author:     actor,
			Text:       body.Text,
			Mentions:   parsed,
			Time:       models.ISONow(),
		}
		d.EntityComments = append(d.EntityComments, created)
		membersSnapshot = append(membersSnapshot[:0], d.Members...)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if len(parsed) > 0 && h.Svc.Events != nil && h.Svc.Events.Enabled() {
		publishMentions(c.Request.Context(), h.Svc.Events, parsed, actor, body.Text,
			entityType+"_comment", entityID, membersSnapshot)
	}
	c.JSON(http.StatusOK, gin.H{"comment": created, "version": ws.Version})
}

func (h *Entities) deleteEntityComment(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.EntityComments[:0]
		removed := false
		for _, cm := range d.EntityComments {
			if cm.ID == id {
				removed = true
				continue
			}
			out = append(out, cm)
		}
		if !removed {
			return fmt.Errorf("comment not found")
		}
		d.EntityComments = out
		return nil
	})
}
