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

// startTimer opens a running TimeEntry for the actor against a task.
// Returns 409 if the same user already has an open timer on the same
// task (clients can call stop first or list to see what's running).
func (h *Entities) startTimer(c *gin.Context) {
	taskID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid task id")
		return
	}
	actor := strings.TrimSpace(c.GetHeader("X-Workspace-User"))
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.TimeEntry
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		// Task must exist.
		if findTaskIndex(d, taskID) < 0 {
			return fmt.Errorf("task not found")
		}
		for _, te := range d.TimeEntries {
			if te.TaskID == taskID && te.UserID == actor && te.EndedAt == "" {
				return fmt.Errorf("timer already running for this user on this task")
			}
		}
		created = models.TimeEntry{
			ID:        models.NextTimeEntryID(d),
			TaskID:    taskID,
			UserID:    actor,
			StartedAt: models.ISONow(),
			Note:      strings.TrimSpace(body.Note),
		}
		d.TimeEntries = append(d.TimeEntries, created)
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "timer already running") {
			apierr.JSONStatus(c, http.StatusConflict, err.Error())
			return
		}
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"timeEntry": created, "version": ws.Version})
}

// stopTimer closes the actor's running entry on the task, computing
// DurationSec server-side. Idempotent on a missing/already-stopped
// entry (returns 404).
func (h *Entities) stopTimer(c *gin.Context) {
	taskID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid task id")
		return
	}
	actor := strings.TrimSpace(c.GetHeader("X-Workspace-User"))
	if actor == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "missing actor")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.TimeEntries {
			te := &d.TimeEntries[i]
			if te.TaskID != taskID || te.UserID != actor || te.EndedAt != "" {
				continue
			}
			now := time.Now().UTC()
			if started, err := time.Parse(time.RFC3339, te.StartedAt); err == nil {
				te.DurationSec = int64(now.Sub(started.UTC()).Seconds())
				if te.DurationSec < 0 {
					te.DurationSec = 0
				}
			}
			te.EndedAt = now.Format(time.RFC3339)
			if note := strings.TrimSpace(body.Note); note != "" {
				te.Note = note
			}
			return nil
		}
		return fmt.Errorf("no running timer for this user on this task")
	})
}

// listTaskTime returns every TimeEntry on a task.
func (h *Entities) listTaskTime(c *gin.Context) {
	taskID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid task id")
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
	entries := make([]models.TimeEntry, 0)
	totalSec := int64(0)
	for _, te := range doc.TimeEntries {
		if te.TaskID == taskID {
			entries = append(entries, te)
			totalSec += te.DurationSec
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": entries, "totalSec": totalSec})
}

// listUserTime returns time entries filtered by user + optional date range.
func (h *Entities) listUserTime(c *gin.Context) {
	userParam := strings.TrimSpace(c.Param("id"))
	if userParam == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid user id")
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
	from, hasFrom := parseQueryTime(c, "from")
	to, hasTo := parseQueryTime(c, "to")

	entries := make([]models.TimeEntry, 0)
	totalSec := int64(0)
	for _, te := range doc.TimeEntries {
		if te.UserID != userParam {
			continue
		}
		if !inWindow(te.StartedAt, hasFrom, from, hasTo, to) {
			continue
		}
		entries = append(entries, te)
		totalSec += te.DurationSec
	}
	c.JSON(http.StatusOK, gin.H{"items": entries, "totalSec": totalSec})
}

func parseQueryTime(c *gin.Context, key string) (time.Time, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func inWindow(startedAt string, hasFrom bool, from time.Time, hasTo bool, to time.Time) bool {
	if !hasFrom && !hasTo {
		return true
	}
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return false
	}
	started = started.UTC()
	if hasFrom && started.Before(from) {
		return false
	}
	if hasTo && started.After(to) {
		return false
	}
	return true
}
