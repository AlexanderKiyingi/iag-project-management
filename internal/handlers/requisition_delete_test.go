package handlers

import (
	"errors"
	"testing"

	"github.com/iag/project-management/backend/internal/models"
)

// A cash requisition had POST and GET and nothing else, so a request raised
// by mistake sat in the list forever. Deleting one is fine right up to the
// point a desk signs it or finance takes it — after that it is the record of
// money that moved, and the delete is refused.
func TestRemoveRequisition(t *testing.T) {
	ref := "AP-77"
	doc := func() *models.Document {
		return &models.Document{Requisitions: []models.Requisition{
			{ID: 1, Title: "Draft", Status: "Draft"},
			{ID: 2, Title: "Submitted", Status: "submitted"},
			{ID: 3, Title: "Approved", Status: "approved"},
			{ID: 4, Title: "At finance", Status: "submitted", FinanceApRef: &ref},
		}}
	}

	for _, id := range []int{1, 2} {
		d := doc()
		if err := removeRequisition(d, id); err != nil {
			t.Fatalf("delete #%d: %v", id, err)
		}
		if len(d.Requisitions) != 3 {
			t.Fatalf("delete #%d left %d rows, want 3", id, len(d.Requisitions))
		}
		for _, r := range d.Requisitions {
			if r.ID == id {
				t.Fatalf("#%d still present after delete", id)
			}
		}
	}

	for _, id := range []int{3, 4} {
		d := doc()
		err := removeRequisition(d, id)
		if !errors.Is(err, errRequisitionSettled) {
			t.Fatalf("delete #%d: got %v, want errRequisitionSettled", id, err)
		}
		if len(d.Requisitions) != 4 {
			t.Fatalf("a refused delete must not touch the list (%d rows)", len(d.Requisitions))
		}
	}

	if err := removeRequisition(doc(), 99); err == nil || err.Error() != "requisition not found" {
		t.Fatalf("unknown id: got %v", err)
	}
}
