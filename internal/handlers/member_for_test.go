package handlers

import (
	"testing"

	"github.com/iag/project-management/backend/internal/models"
)

func TestMemberForResolvesAssignee(t *testing.T) {
	members := []models.Member{
		{Initials: "AK", Name: "Alex K", Email: "alex@example.com"},
		{Initials: "BN", Name: "Bea N"},
	}
	for _, a := range []string{"AK", "ak", "Alex K", "alex@example.com"} {
		m, ok := memberFor(a, members)
		if !ok || m.Email != "alex@example.com" {
			t.Errorf("%q did not resolve to the assignee", a)
		}
	}
	if _, ok := memberFor("ZZ", members); ok {
		t.Error("an unknown assignee resolved")
	}
}
