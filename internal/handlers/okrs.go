package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/models"
)

func (h *Entities) addKeyResult(c *gin.Context) {
	goalID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var body struct {
		Name    string  `json:"name"`
		Metric  string  `json:"metric"`
		Current float64 `json:"current"`
		Target  float64 `json:"target"`
		Unit    string  `json:"unit"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" || body.Target <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body (need name and positive target)"})
		return
	}
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var created models.KeyResult
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		gi := findGoalIndex(d, goalID)
		if gi < 0 {
			return fmt.Errorf("goal not found")
		}
		created = models.KeyResult{
			ID:      nextKeyResultID(d.Goals[gi]),
			Name:    body.Name,
			Metric:  body.Metric,
			Current: body.Current,
			Target:  body.Target,
			Unit:    body.Unit,
		}
		d.Goals[gi].KeyResults = append(d.Goals[gi].KeyResults, created)
		d.Goals[gi].Progress = computeGoalProgress(d.Goals[gi])
		return nil
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"keyResult": created, "version": ws.Version})
}

func (h *Entities) patchKeyResult(c *gin.Context) {
	goalID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid goal id"})
		return
	}
	krID, err := strconv.Atoi(c.Param("krId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key result id"})
		return
	}
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		gi := findGoalIndex(d, goalID)
		if gi < 0 {
			return fmt.Errorf("goal not found")
		}
		for k := range d.Goals[gi].KeyResults {
			if d.Goals[gi].KeyResults[k].ID != krID {
				continue
			}
			applyKeyResultPatch(&d.Goals[gi].KeyResults[k], patch)
			d.Goals[gi].Progress = computeGoalProgress(d.Goals[gi])
			return nil
		}
		return fmt.Errorf("key result not found")
	})
}

func (h *Entities) deleteKeyResult(c *gin.Context) {
	goalID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid goal id"})
		return
	}
	krID, err := strconv.Atoi(c.Param("krId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key result id"})
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		gi := findGoalIndex(d, goalID)
		if gi < 0 {
			return fmt.Errorf("goal not found")
		}
		out := d.Goals[gi].KeyResults[:0]
		removed := false
		for _, kr := range d.Goals[gi].KeyResults {
			if kr.ID == krID {
				removed = true
				continue
			}
			out = append(out, kr)
		}
		if !removed {
			return fmt.Errorf("key result not found")
		}
		d.Goals[gi].KeyResults = out
		d.Goals[gi].Progress = computeGoalProgress(d.Goals[gi])
		return nil
	})
}

func applyKeyResultPatch(kr *models.KeyResult, patch map[string]any) {
	if v, ok := patch["name"].(string); ok {
		kr.Name = v
	}
	if v, ok := patch["metric"].(string); ok {
		kr.Metric = v
	}
	if v, ok := patch["current"].(float64); ok {
		kr.Current = v
	}
	if v, ok := patch["target"].(float64); ok && v > 0 {
		kr.Target = v
	}
	if v, ok := patch["unit"].(string); ok {
		kr.Unit = v
	}
}

// computeGoalProgress averages each KR's current/target ratio (capped at
// 100). With no KRs the previously-stored Progress is preserved so
// manual progress mutations (POST /goals/:id/progress) keep working.
func computeGoalProgress(g models.Goal) int {
	if len(g.KeyResults) == 0 {
		return g.Progress
	}
	sum := 0.0
	for _, kr := range g.KeyResults {
		if kr.Target <= 0 {
			continue
		}
		pct := math.Min(100, math.Max(0, (kr.Current/kr.Target)*100))
		sum += pct
	}
	avg := sum / float64(len(g.KeyResults))
	return int(math.Round(avg))
}

func nextKeyResultID(g models.Goal) int {
	max := 0
	for _, kr := range g.KeyResults {
		if kr.ID > max {
			max = kr.ID
		}
	}
	return max + 1
}

func findGoalIndex(d *models.Document, goalID int) int {
	for i := range d.Goals {
		if d.Goals[i].ID == goalID {
			return i
		}
	}
	return -1
}
