package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/auth"
	"github.com/iag/project-management/backend/internal/events"
	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func (h *Entities) registerProcurementRoutes(rg *gin.RouterGroup, authz gin.HandlerFunc) {
	pg := rg.Group("/procurement")
	pg.GET("/vendors", auth.RequireWorkspaceRead(), h.listProcurementVendors)
	pg.POST("/vendors", authz, h.createProcurementVendor)
	pg.PATCH("/vendors/:id", authz, h.patchProcurementVendor)
	pg.DELETE("/vendors/:id", authz, h.deleteProcurementVendor)

	pg.GET("/purchase-requisitions", auth.RequireWorkspaceRead(), h.listProcurementPurchaseReqs)
	pg.POST("/purchase-requisitions", authz, h.createProcurementPurchaseReq)
	pg.PATCH("/purchase-requisitions/:id", authz, h.patchProcurementPurchaseReq)
	pg.DELETE("/purchase-requisitions/:id", authz, h.deleteProcurementPurchaseReq)
	pg.POST("/purchase-requisitions/:id/submit", authz, h.submitProcurementPurchaseReq)
}

func (h *Entities) listProcurementVendors(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"vendors": doc.Vendors})
}

func (h *Entities) createProcurementVendor(c *gin.Context) {
	var v models.ProcurementVendor
	if err := c.ShouldBindJSON(&v); err != nil || strings.TrimSpace(v.Name) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.ProcurementVendor
	mutate(c, h.Svc, func(d *models.Document) error {
		if v.ID == "" {
			v.ID = nextVendorID(d)
		}
		created = v
		d.Vendors = append([]models.ProcurementVendor{v}, d.Vendors...)
		return nil
	})
	_ = created
}

func (h *Entities) patchProcurementVendor(c *gin.Context) {
	id := c.Param("id")
	var patch models.ProcurementVendor
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.Vendors {
			if d.Vendors[i].ID != id {
				continue
			}
			if patch.Name != "" {
				d.Vendors[i].Name = patch.Name
			}
			if patch.Category != "" {
				d.Vendors[i].Category = patch.Category
			}
			if patch.Email != "" {
				d.Vendors[i].Email = patch.Email
			}
			if patch.Phone != "" {
				d.Vendors[i].Phone = patch.Phone
			}
			if patch.Status != "" {
				d.Vendors[i].Status = patch.Status
			}
			return nil
		}
		return fmt.Errorf("vendor not found")
	})
}

func (h *Entities) deleteProcurementVendor(c *gin.Context) {
	id := c.Param("id")
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.Vendors[:0]
		found := false
		for _, v := range d.Vendors {
			if v.ID == id {
				found = true
				continue
			}
			out = append(out, v)
		}
		if !found {
			return fmt.Errorf("vendor not found")
		}
		d.Vendors = out
		return nil
	})
}

func (h *Entities) listProcurementPurchaseReqs(c *gin.Context) {
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	doc, _, err := h.Svc.LoadDocument(c.Request.Context(), uid)
	if err != nil {
		apierr.JSONStatus(c, http.StatusInternalServerError, "load workspace")
		return
	}
	c.JSON(http.StatusOK, gin.H{"purchaseRequisitions": doc.PurchaseRequisitions})
}

func (h *Entities) createProcurementPurchaseReq(c *gin.Context) {
	var req models.ProcurementRequisition
	if err := bindJSONCoerced(c, &req); err != nil || strings.TrimSpace(req.Title) == "" {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	var created models.ProcurementRequisition
	mutate(c, h.Svc, func(d *models.Document) error {
		if req.ID == "" {
			req.ID = nextPurchaseReqID(d)
		}
		if req.Status == "" {
			req.Status = "Draft"
		}
		if req.CreatedAt == "" {
			req.CreatedAt = time.Now().UTC().Format("2006-01-02")
		}
		created = req
		d.PurchaseRequisitions = append([]models.ProcurementRequisition{req}, d.PurchaseRequisitions...)
		return nil
	})
	_ = created
}

func (h *Entities) patchProcurementPurchaseReq(c *gin.Context) {
	id := c.Param("id")
	var patch map[string]any
	if err := c.ShouldBindJSON(&patch); err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid body")
		return
	}
	mutate(c, h.Svc, func(d *models.Document) error {
		for i := range d.PurchaseRequisitions {
			if d.PurchaseRequisitions[i].ID != id {
				continue
			}
			applyPurchaseReqPatch(&d.PurchaseRequisitions[i], patch)
			return nil
		}
		return fmt.Errorf("purchase requisition not found")
	})
}

func (h *Entities) deleteProcurementPurchaseReq(c *gin.Context) {
	id := c.Param("id")
	mutate(c, h.Svc, func(d *models.Document) error {
		out := d.PurchaseRequisitions[:0]
		found := false
		for _, r := range d.PurchaseRequisitions {
			if r.ID == id {
				found = true
				continue
			}
			out = append(out, r)
		}
		if !found {
			return fmt.Errorf("purchase requisition not found")
		}
		d.PurchaseRequisitions = out
		return nil
	})
}

func (h *Entities) submitProcurementPurchaseReq(c *gin.Context) {
	id := c.Param("id")
	uid, ok := requireUserID(c)
	if !ok {
		return
	}
	var submitted models.ProcurementRequisition
	ws, err := h.Svc.Mutate(c.Request.Context(), uid, func(d *models.Document) error {
		for i := range d.PurchaseRequisitions {
			if d.PurchaseRequisitions[i].ID != id {
				continue
			}
			d.PurchaseRequisitions[i].Status = "Pending Approval"
			submitted = d.PurchaseRequisitions[i]
			return nil
		}
		return fmt.Errorf("purchase requisition not found")
	})
	if err != nil {
		writeMutationError(c, err)
		return
	}
	if h.Svc.Events != nil && h.Svc.Events.Enabled() {
		h.Svc.Events.PublishCommercial(c.Request.Context(), events.TypePurchaseRequisitionSubmitted, map[string]any{
			"purchaseRequisitionId": submitted.ID,
			"workspaceOwnerUserId":  uid,
			"title":                 submitted.Title,
			"total":                 fmt.Sprintf("%.2f", submitted.Total),
			"currency":              submitted.Currency,
			"status":                submitted.Status,
			"requester":             submitted.Requester,
			"dept":                  submitted.Dept,
			"priority":              submitted.Priority,
		}, submitted.ID)
		claims, _ := middleware.PlatformClaims(c)
		if claims != nil && claims.Email != "" {
			h.Svc.Events.PublishPMAlert(c.Request.Context(), "email", claims.Email, "pm.purchase_requisition.submitted", map[string]string{
				"title": submitted.Title,
				"id":    submitted.ID,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"purchaseRequisition": submitted, "version": ws.Version})
}

func applyPurchaseReqPatch(r *models.ProcurementRequisition, patch map[string]any) {
	if v, ok := patch["title"].(string); ok && v != "" {
		r.Title = v
	}
	if v, ok := patch["status"].(string); ok && v != "" {
		r.Status = v
	}
	if v, ok := patch["priority"].(string); ok {
		r.Priority = v
	}
	if v, ok := patch["notes"].(string); ok {
		r.Notes = v
	}
	if v, ok := patch["total"].(float64); ok {
		r.Total = v
	}
}

func nextVendorID(d *models.Document) string {
	return nextStringID("V", d.Vendors, func(v models.ProcurementVendor) string { return v.ID })
}

func nextPurchaseReqID(d *models.Document) string {
	return nextStringID("PR", d.PurchaseRequisitions, func(r models.ProcurementRequisition) string { return r.ID })
}

func nextStringID[T any](prefix string, rows []T, idFn func(T) string) string {
	max := 0
	year := time.Now().UTC().Format("2006")
	pat := prefix + "-" + year + "-"
	for _, row := range rows {
		id := idFn(row)
		if !strings.HasPrefix(id, pat) {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(id, prefix+"-"+year+"-%d", &n); err == nil && n > max {
			max = n
		}
	}
	return fmt.Sprintf("%s-%s-%04d", prefix, year, max+1)
}
