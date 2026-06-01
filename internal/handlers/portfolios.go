package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

// PortfolioRollup is the per-portfolio summary returned by GET
// /portfolios — composed live from the current document so rollup
// numbers always reflect reality without a separate sync job.
type PortfolioRollup struct {
	models.Portfolio
	TaskCount      int                      `json:"taskCount"`
	CompletedCount int                      `json:"completedCount"`
	OverdueCount   int                      `json:"overdueCount"`
	DueThisWeek    int                      `json:"dueThisWeek"`
	StatusRollup   map[string]int           `json:"statusRollup"`
	Projects       []portfolioProjectStat   `json:"projects"`
}

type portfolioProjectStat struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	TaskCount int    `json:"taskCount"`
	Completed int    `json:"completed"`
}

func (h *Entities) listPortfolios(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	out := make([]PortfolioRollup, 0, len(doc.Portfolios))
	now := time.Now().UTC()
	for _, p := range doc.Portfolios {
		out = append(out, buildPortfolioRollup(p, doc, now))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (h *Entities) getPortfolio(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "load workspace"})
		return
	}
	for _, p := range doc.Portfolios {
		if p.ID == id {
			c.JSON(http.StatusOK, gin.H{"portfolio": buildPortfolioRollup(p, doc, time.Now().UTC())})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "portfolio not found"})
}

func (h *Entities) createPortfolio(c *gin.Context) {
	var body struct {
		Name       string   `json:"name"`
		ProjectIDs []string `json:"projectIds"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	now := models.ISONow()
	var created models.Portfolio
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		created = models.Portfolio{
			ID:          models.NextPortfolioID(d),
			OwnerUserID: uid,
			Name:        strings.TrimSpace(body.Name),
			ProjectIDs:  dedupeNonEmpty(body.ProjectIDs),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		d.Portfolios = append(d.Portfolios, created)
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"portfolio": created, "version": ws.Version})
}

func (h *Entities) patchPortfolio(c *gin.Context) {
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
		for i := range d.Portfolios {
			if d.Portfolios[i].ID != id {
				continue
			}
			if v, ok := patch["name"].(string); ok && strings.TrimSpace(v) != "" {
				d.Portfolios[i].Name = strings.TrimSpace(v)
			}
			if raw, ok := patch["projectIds"].([]any); ok {
				ids := make([]string, 0, len(raw))
				for _, x := range raw {
					if s, ok := x.(string); ok {
						ids = append(ids, s)
					}
				}
				d.Portfolios[i].ProjectIDs = dedupeNonEmpty(ids)
			}
			d.Portfolios[i].UpdatedAt = models.ISONow()
			return nil
		}
		return fmt.Errorf("portfolio not found")
	})
}

func (h *Entities) deletePortfolio(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Portfolios[:0]
		removed := false
		for _, p := range d.Portfolios {
			if p.ID == id {
				removed = true
				continue
			}
			out = append(out, p)
		}
		if !removed {
			return fmt.Errorf("portfolio not found")
		}
		d.Portfolios = out
		return nil
	})
}

func buildPortfolioRollup(p models.Portfolio, doc models.Document, now time.Time) PortfolioRollup {
	weekEnd := now.AddDate(0, 0, 7)
	projectSet := map[string]struct{}{}
	for _, id := range p.ProjectIDs {
		projectSet[id] = struct{}{}
	}
	rollup := PortfolioRollup{
		Portfolio:    p,
		StatusRollup: map[string]int{},
	}
	perProject := map[string]*portfolioProjectStat{}
	for _, id := range p.ProjectIDs {
		project, ok := doc.Projects[id]
		row := &portfolioProjectStat{ID: id}
		if ok {
			row.Name = project.Name
			row.Status = project.Status
		}
		perProject[id] = row
	}
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		if !portfolioMatchTask(projectSet, t) {
			continue
		}
		rollup.TaskCount++
		rollup.StatusRollup[t.Status]++
		stat := perProject[firstProjectOnTask(t)]
		if stat != nil {
			stat.TaskCount++
		}
		if t.Done {
			rollup.CompletedCount++
			if stat != nil {
				stat.Completed++
			}
			continue
		}
		due, has := parsePortfolioDate(t)
		if !has {
			continue
		}
		if due.Before(now) {
			rollup.OverdueCount++
		} else if !due.After(weekEnd) {
			rollup.DueThisWeek++
		}
	}
	for _, stat := range perProject {
		rollup.Projects = append(rollup.Projects, *stat)
	}
	return rollup
}

func portfolioMatchTask(projectSet map[string]struct{}, t models.Task) bool {
	if _, ok := projectSet[t.Project]; ok {
		return true
	}
	for _, p := range t.Projects {
		if _, ok := projectSet[p]; ok {
			return true
		}
	}
	return false
}

func firstProjectOnTask(t models.Task) string {
	if t.Project != "" {
		return t.Project
	}
	if len(t.Projects) > 0 {
		return t.Projects[0]
	}
	return ""
}

func parsePortfolioDate(t models.Task) (time.Time, bool) {
	for _, candidate := range []string{t.EndDate, t.Due} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if v, err := time.Parse("2006-01-02", candidate); err == nil {
			return v.UTC(), true
		}
		if v, err := time.Parse(time.RFC3339, candidate); err == nil {
			return v.UTC(), true
		}
	}
	return time.Time{}, false
}
