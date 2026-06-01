package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

// MemberWorkload reports the live workload of one workspace member
// computed from open task assignments. It is intentionally NOT cached
// in the document — every call recomputes from the current Task slice
// so Member.wl (legacy display int) can drift without affecting this
// endpoint.
type MemberWorkload struct {
	UserID    string `json:"userId,omitempty"`
	Initials  string `json:"initials,omitempty"`
	Name      string `json:"name,omitempty"`
	OpenTasks int    `json:"openTasks"`
	DueInWin  int    `json:"dueInWindow"`
	Overdue   int    `json:"overdue"`
}

func (h *Entities) workspaceWorkload(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	windowDays := 7
	if raw := strings.TrimSpace(c.Query("period")); raw != "" {
		switch strings.ToLower(raw) {
		case "day":
			windowDays = 1
		case "week":
			windowDays = 7
		case "month":
			windowDays = 30
		}
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"workload": computeWorkload(doc, windowDays), "windowDays": windowDays})
}

// computeWorkload groups tasks by their Assignee match key — preferring
// initials when the Task.Assignee value matches a Member's initials, and
// otherwise grouping by the raw Assignee string so externally-assigned
// tasks still surface. Closed tasks (Done=true) are excluded.
func computeWorkload(doc models.Document, windowDays int) []MemberWorkload {
	now := time.Now().UTC()
	windowEnd := now.Add(time.Duration(windowDays) * 24 * time.Hour)

	byInitials := map[string]models.Member{}
	for _, m := range doc.Members {
		if k := strings.TrimSpace(m.Initials); k != "" {
			byInitials[strings.ToLower(k)] = m
		}
	}

	type bucket struct {
		member  models.Member
		hasUser bool
		raw     string
		stats   MemberWorkload
	}
	buckets := map[string]*bucket{}

	for _, t := range doc.Tasks {
		if t.Done {
			continue
		}
		assignee := strings.TrimSpace(t.Assignee)
		if assignee == "" {
			continue
		}
		key := strings.ToLower(assignee)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{raw: assignee}
			if m, found := byInitials[key]; found {
				b.member = m
				b.hasUser = true
			}
			buckets[key] = b
		}
		b.stats.OpenTasks++
		due := taskDueTime(t)
		if due.IsZero() {
			continue
		}
		if due.Before(now) {
			b.stats.Overdue++
		} else if !due.After(windowEnd) {
			b.stats.DueInWin++
		}
	}

	out := make([]MemberWorkload, 0, len(buckets))
	for _, b := range buckets {
		row := b.stats
		if b.hasUser {
			row.UserID = b.member.UserID
			row.Initials = b.member.Initials
			row.Name = b.member.Name
		} else {
			row.Initials = b.raw
		}
		out = append(out, row)
	}
	return out
}

func taskDueTime(t models.Task) time.Time {
	candidates := []string{t.EndDate, t.Due}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if v, err := time.Parse("2006-01-02", raw); err == nil {
			return v
		}
		if v, err := time.Parse(time.RFC3339, raw); err == nil {
			return v
		}
	}
	return time.Time{}
}
