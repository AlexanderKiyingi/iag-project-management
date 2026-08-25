package handlers

import (
	"testing"

	"github.com/iag/project-management/backend/internal/models"
)

// The patch surface used to be title/status/priority/notes/total only, so a
// client that edited the delivery date, the currency, the department or the
// lines got a 200 and no change — the requisition simply reappeared as it was.
// These pin the whole editable surface, because the failure mode is silent.
func TestApplyPurchaseReqPatchCoversEveryEditableField(t *testing.T) {
	r := models.ProcurementRequisition{
		ID:        "PR-2026-0001",
		Title:     "Cement",
		Dept:      "Works",
		Requester: "asiimwe",
		Priority:  "Low",
		Status:    "Draft",
		CreatedAt: "2026-08-01",
		NeededBy:  "2026-08-10",
		Total:     100,
		Currency:  "UGX",
		BudgetID:  "B-1",
		Notes:     "first",
	}

	applyPurchaseReqPatch(&r, map[string]any{
		"title":     "Cement, 42.5N",
		"status":    "Submitted",
		"priority":  "High",
		"notes":     "revised",
		"total":     float64(250),
		"neededBy":  "2026-09-01",
		"currency":  "USD",
		"dept":      "Civils",
		"requester": "nakato",
		"budgetId":  "B-2",
		"items": []any{
			map[string]any{"itemId": "I-1", "qty": float64(12), "unit": "bags"},
		},
	})

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"Title", r.Title, "Cement, 42.5N"},
		{"Status", r.Status, "Submitted"},
		{"Priority", r.Priority, "High"},
		{"Notes", r.Notes, "revised"},
		{"NeededBy", r.NeededBy, "2026-09-01"},
		{"Currency", r.Currency, "USD"},
		{"Dept", r.Dept, "Civils"},
		{"Requester", r.Requester, "nakato"},
		{"BudgetID", r.BudgetID, "B-2"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if r.Total != 250 {
		t.Errorf("Total = %v, want 250", r.Total)
	}

	if len(r.Items) != 1 {
		t.Fatalf("Items = %d rows, want 1", len(r.Items))
	}
	if r.Items[0].ItemID != "I-1" || r.Items[0].Qty != 12 || r.Items[0].Unit != "bags" {
		t.Errorf("Items[0] = %+v, want {I-1 12 bags}", r.Items[0])
	}

	// Server-owned; a patch must not be able to move them.
	if r.ID != "PR-2026-0001" || r.CreatedAt != "2026-08-01" {
		t.Errorf("server-owned fields moved: id=%q createdAt=%q", r.ID, r.CreatedAt)
	}
}

// A key that is absent must leave the stored value alone — otherwise every
// partial patch blanks the fields it did not mention.
func TestApplyPurchaseReqPatchLeavesAbsentKeysAlone(t *testing.T) {
	r := models.ProcurementRequisition{
		Title:    "Cement",
		NeededBy: "2026-08-10",
		Currency: "UGX",
		BudgetID: "B-1",
		Items:    []models.ProcurementLineItem{{ItemID: "I-1", Qty: 3, Unit: "bags"}},
	}

	applyPurchaseReqPatch(&r, map[string]any{"title": "Cement, 42.5N"})

	if r.NeededBy != "2026-08-10" || r.Currency != "UGX" || r.BudgetID != "B-1" {
		t.Errorf("absent keys were cleared: %+v", r)
	}
	if len(r.Items) != 1 {
		t.Errorf("Items cleared by a patch that did not mention them: %+v", r.Items)
	}
}

// Lines are replaced wholesale, so an explicit empty list is how a caller says
// "no lines" — a merge would have no way to express a removal.
func TestApplyPurchaseReqPatchReplacesLinesWholesale(t *testing.T) {
	r := models.ProcurementRequisition{
		Items: []models.ProcurementLineItem{
			{ItemID: "I-1", Qty: 3},
			{ItemID: "I-2", Qty: 4},
		},
	}

	applyPurchaseReqPatch(&r, map[string]any{"items": []any{}})

	if len(r.Items) != 0 {
		t.Errorf("Items = %+v, want empty", r.Items)
	}
}

// Malformed lines must not take the rest of the patch down with them.
func TestApplyPurchaseReqPatchIgnoresUndecodableLines(t *testing.T) {
	r := models.ProcurementRequisition{
		Title: "Cement",
		Items: []models.ProcurementLineItem{{ItemID: "I-1", Qty: 3}},
	}

	applyPurchaseReqPatch(&r, map[string]any{
		"title": "Sand",
		"items": "not a list",
	})

	if r.Title != "Sand" {
		t.Errorf("Title = %q, want Sand", r.Title)
	}
	if len(r.Items) != 1 || r.Items[0].ItemID != "I-1" {
		t.Errorf("Items changed on an undecodable patch: %+v", r.Items)
	}
}
