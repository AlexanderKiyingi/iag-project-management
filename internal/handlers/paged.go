package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 500
)

// listTasks returns a paged, filtered view of the workspace's tasks. The
// canonical document GET stays the source of truth for app-load; this
// endpoint exists so very large workspaces can paginate task lists once
// the document grows past comfortable in-browser sizes.
//
// Query params:
//   limit (default 50, max 500)
//   offset (default 0)
//   project (string, exact match)
//   assignee (string, case-insensitive exact match)
//   status (string, exact match)
//   priority (string, exact match)
//   q (string, substring match on name/desc)
//   includeDeleted=true (otherwise soft-deleted tasks are filtered)
func (h *Entities) listTasksPaged(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, ws, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	applyProjectVisibility(&doc, uid)

	limit := parsePositiveQuery(c, "limit", defaultPageLimit, maxPageLimit)
	offset := parseNonNegativeQuery(c, "offset", 0)
	project := strings.TrimSpace(c.Query("project"))
	assignee := strings.ToLower(strings.TrimSpace(c.Query("assignee")))
	status := strings.TrimSpace(c.Query("status"))
	priority := strings.TrimSpace(c.Query("priority"))
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	includeDeleted := c.Query("includeDeleted") == "true"

	matched := make([]models.Task, 0, len(doc.Tasks))
	for _, t := range doc.Tasks {
		if !includeDeleted && t.DeletedAt != "" {
			continue
		}
		if project != "" && !taskHomedIn(t, project) {
			continue
		}
		if assignee != "" && strings.ToLower(t.Assignee) != assignee {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		if priority != "" && t.Priority != priority {
			continue
		}
		if q != "" {
			if !strings.Contains(strings.ToLower(t.Name), q) && !strings.Contains(strings.ToLower(t.Desc), q) {
				continue
			}
		}
		matched = append(matched, t)
	}
	total := len(matched)
	page := pageSlice(matched, offset, limit)

	c.JSON(http.StatusOK, gin.H{
		"items":   page,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
		"version": ws.Version,
	})
}

// listMessagesPaged returns messages for a chat with simple before-cursor
// pagination. before is the ID (exclusive) of the oldest message the
// client has seen; messages are returned newest-first.
func (h *Entities) listMessagesPaged(c *gin.Context) {
	chatRaw := strings.TrimSpace(c.Query("chatId"))
	if chatRaw == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "chatId is required")
		return
	}
	chatID, err := strconv.Atoi(chatRaw)
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid chatId")
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}

	limit := parsePositiveQuery(c, "limit", defaultPageLimit, maxPageLimit)
	before := parseNonNegativeQuery(c, "before", 0)

	filtered := make([]models.Message, 0)
	for _, m := range doc.Messages {
		if m.ChatID != chatID {
			continue
		}
		if before > 0 && m.ID >= before {
			continue
		}
		filtered = append(filtered, m)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID > filtered[j].ID })
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	nextBefore := 0
	if len(filtered) == limit && len(filtered) > 0 {
		nextBefore = filtered[len(filtered)-1].ID
	}
	c.JSON(http.StatusOK, gin.H{
		"items":      filtered,
		"limit":      limit,
		"nextBefore": nextBefore,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	})
}

func parsePositiveQuery(c *gin.Context, key string, def, max int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func parseNonNegativeQuery(c *gin.Context, key string, def int) int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func pageSlice[T any](items []T, offset, limit int) []T {
	if offset >= len(items) {
		return items[:0]
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
