package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/iag/project-management/backend/internal/financeclient"
	"github.com/iag/project-management/backend/internal/models"
)

func requisitionDocumentRef(reqID int) string {
	return fmt.Sprintf("PM-REQ-%d", reqID)
}

// bookApprovedRequisitionAP books the AP open item for an approved requisition.
// It returns an error when the booking should be RETRIED (transient finance
// outage, persist failure): the caller propagates it so the consumer redelivers
// the event instead of marking it processed. A finance 409 (already booked) is
// treated as success — the AP exists, so we record the ref and stop.
func (h *Handler) bookApprovedRequisitionAP(ctx context.Context, owner string, reqID int) error {
	if h.Finance == nil || !h.Finance.Enabled() {
		return nil
	}
	ws, err := h.Repo.GetByOwner(ctx, owner)
	if err != nil {
		return fmt.Errorf("finance ap: load workspace %s: %w", owner, err)
	}
	var doc models.Document
	if err := json.Unmarshal(ws.Document, &doc); err != nil {
		// Corrupt workspace JSON won't fix itself on retry — log and give up.
		slog.Warn("finance ap: decode workspace failed", "owner", owner, "err", err)
		return nil
	}
	var req *models.Requisition
	for i := range doc.Requisitions {
		if doc.Requisitions[i].ID == reqID {
			req = &doc.Requisitions[i]
			break
		}
	}
	if req == nil || req.Status != "approved" {
		return nil
	}
	if req.FinanceApRef != nil && *req.FinanceApRef != "" {
		return nil // already booked
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
	if err != nil && !errors.Is(err, financeclient.ErrAPItemExists) {
		// Transient/unknown failure — retry by surfacing the error.
		return fmt.Errorf("finance ap: create for requisition #%d: %w", reqID, err)
	}
	// On ErrAPItemExists, apRef is the documentRef of the already-booked item.

	if _, err := h.Svc.Mutate(ctx, owner, func(d *models.Document) error {
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
	}); err != nil {
		// AP exists in finance but PM didn't record the ref — retry so the
		// link isn't orphaned (the next attempt hits the 409 idempotent path).
		return fmt.Errorf("finance ap: persist ref for requisition #%d: %w", reqID, err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
