package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/iag/project-management/backend/internal/financeclient"
	"github.com/iag/project-management/backend/internal/models"
)

func requisitionDocumentRef(reqID int) string {
	return fmt.Sprintf("PM-REQ-%d", reqID)
}

func (h *Handler) bookApprovedRequisitionAP(ctx context.Context, owner string, reqID int) {
	if h.Finance == nil || !h.Finance.Enabled() {
		return
	}
	ws, err := h.Repo.GetByOwner(ctx, owner)
	if err != nil {
		slog.Warn("finance ap: load workspace failed", "owner", owner, "err", err)
		return
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		slog.Warn("finance ap: decode workspace failed", "owner", owner, "err", err)
		return
	}
	var req *models.Requisition
	for i := range doc.Requisitions {
		if doc.Requisitions[i].ID == reqID {
			req = &doc.Requisitions[i]
			break
		}
	}
	if req == nil || req.Status != "approved" {
		return
	}
	if req.FinanceApRef != nil && *req.FinanceApRef != "" {
		return
	}
	currency := req.Currency
	if currency == "" {
		currency = "UGX"
	}
	docRef := requisitionDocumentRef(reqID)
	vendor := req.Payee
	if vendor == "" {
		vendor = req.RequestedBy
	}
	if vendor == "" {
		vendor = "pm-requisition"
	}
	apRef, err := h.Finance.CreateAPItem(ctx, financeclient.CreateAPInput{
		VendorRef:   vendor,
		DocumentRef: docRef,
		Description: firstNonEmpty(req.Title, fmt.Sprintf("PM requisition #%d", reqID)),
		Amount:      fmt.Sprintf("%.2f", req.Amount),
		Currency:    currency,
	})
	if err != nil {
		slog.Warn("finance ap: create failed", "owner", owner, "reqId", reqID, "err", err)
		return
	}
	_, err = h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
		for i := range d.Requisitions {
			if d.Requisitions[i].ID != reqID {
				continue
			}
			d.Requisitions[i].FinanceApRef = strPtr(apRef)
			models.AppendAudit(d, "finance", "finance.ap.created",
				fmt.Sprintf("AP %s booked for requisition #%d", apRef, reqID), nil)
			return nil
		}
		return nil
	})
	if err != nil {
		slog.Warn("finance ap: persist ref failed", "owner", owner, "reqId", reqID, "err", err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
