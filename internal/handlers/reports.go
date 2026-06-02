package handlers

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

// workloadReport returns task counts by assignee for the workspace.
// Distinct from /workspace/workload (Phase 2.8) which is a flat list;
// this one supports a `by=` axis (assignee | project | priority).
func (h *Entities) reportWorkload(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	by := strings.ToLower(strings.TrimSpace(c.DefaultQuery("by", "assignee")))
	counts := map[string]int{}
	completed := map[string]int{}
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		key := bucketTask(t, by)
		if key == "" {
			key = "(unassigned)"
		}
		counts[key]++
		if t.Done {
			completed[key]++
		}
	}
	rows := flattenCounts(counts, completed)
	c.JSON(http.StatusOK, gin.H{"by": by, "items": rows})
}

// reportThroughput counts completed tasks per week/day in the
// (from, to) window using AuditEntry.Type == "task.completed".
func (h *Entities) reportThroughput(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	from, _ := parseQueryTime(c, "from")
	to, hasTo := parseQueryTime(c, "to")
	if !hasTo {
		to = time.Now().UTC()
	}
	groupBy := strings.ToLower(strings.TrimSpace(c.DefaultQuery("groupBy", "week")))
	buckets := map[string]int{}
	for _, a := range doc.Audit {
		if a.Type != "task.completed" {
			continue
		}
		t, err := time.Parse(time.RFC3339, a.Time)
		if err != nil {
			continue
		}
		t = t.UTC()
		if !from.IsZero() && t.Before(from) {
			continue
		}
		if t.After(to) {
			continue
		}
		buckets[truncateBucket(t, groupBy)]++
	}
	items := flattenSimple(buckets)
	c.JSON(http.StatusOK, gin.H{"groupBy": groupBy, "items": items})
}

// reportStatusRollup groups tasks by status within an optional project.
func (h *Entities) reportStatusRollup(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	projectFilter := strings.TrimSpace(c.Query("projectId"))
	counts := map[string]int{}
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		if projectFilter != "" && !taskHomedIn(t, projectFilter) {
			continue
		}
		status := t.Status
		if status == "" {
			status = "unset"
		}
		counts[status]++
	}
	c.JSON(http.StatusOK, gin.H{"items": flattenSimple(counts)})
}

// reportBurndown returns the remaining-open count per day across a
// sprint's window, computed from task due dates + task.completed audit
// entries. Used to render a sprint burndown chart on the frontend.
func (h *Entities) reportBurndown(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	sprintID := strings.TrimSpace(c.Param("id"))
	if sprintID == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid sprint id")
		return
	}
	// Find sprint window.
	var (
		start, end   time.Time
		foundSprint  bool
	)
	for _, s := range doc.Sprints {
		idStr := strings.TrimSpace(c.Param("id"))
		if formatSprintID(s.ID) != idStr {
			continue
		}
		foundSprint = true
		start, _ = time.Parse("2006-01-02", s.StartDate)
		end, _ = time.Parse("2006-01-02", s.EndDate)
		break
	}
	if !foundSprint || start.IsZero() || end.IsZero() {
		apierr.JSONStatus(c, http.StatusNotFound, "sprint not found or missing dates")
		return
	}
	// Tasks attached to this sprint.
	sprintTasks := []models.Task{}
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		if formatSprintID(t.SprintID) == sprintID {
			sprintTasks = append(sprintTasks, t)
		}
	}
	total := len(sprintTasks)
	// Map taskID → completion time (if any) from the audit log.
	completedAt := map[int]time.Time{}
	for _, a := range doc.Audit {
		if a.Type != "task.completed" || a.TaskID == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, a.Time)
		if err == nil {
			completedAt[*a.TaskID] = t.UTC()
		}
	}
	// Build the day-by-day series.
	type point struct {
		Date      string `json:"date"`
		Remaining int    `json:"remaining"`
	}
	series := []point{}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		dayKey := day.Format("2006-01-02")
		remaining := total
		for _, t := range sprintTasks {
			if c, ok := completedAt[t.ID]; ok && !c.After(day.Add(24*time.Hour)) {
				remaining--
			}
		}
		series = append(series, point{Date: dayKey, Remaining: remaining})
	}
	c.JSON(http.StatusOK, gin.H{
		"sprintId":    sprintID,
		"total":       total,
		"series":      series,
	})
}

func bucketTask(t models.Task, by string) string {
	switch by {
	case "project":
		if t.Project != "" {
			return t.Project
		}
		if len(t.Projects) > 0 {
			return t.Projects[0]
		}
		return ""
	case "priority":
		return t.Priority
	default:
		return t.Assignee
	}
}

func truncateBucket(t time.Time, groupBy string) string {
	switch groupBy {
	case "day":
		return t.Format("2006-01-02")
	case "month":
		return t.Format("2006-01")
	default:
		// Week starts on Monday for predictability.
		weekday := int(t.Weekday()) - 1
		if weekday < 0 {
			weekday = 6
		}
		monday := t.AddDate(0, 0, -weekday)
		return monday.Format("2006-01-02")
	}
}

type countRow struct {
	Key       string `json:"key"`
	Count     int    `json:"count"`
	Completed int    `json:"completed,omitempty"`
}

func flattenCounts(counts, completed map[string]int) []countRow {
	rows := make([]countRow, 0, len(counts))
	for k, c := range counts {
		rows = append(rows, countRow{Key: k, Count: c, Completed: completed[k]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
	return rows
}

func flattenSimple(buckets map[string]int) []countRow {
	rows := make([]countRow, 0, len(buckets))
	for k, c := range buckets {
		rows = append(rows, countRow{Key: k, Count: c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

func formatSprintID(id int) string {
	if id == 0 {
		return "0"
	}
	return strings.TrimSpace(fmtInt(id))
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if negative {
		digits = "-" + digits
	}
	return digits
}
