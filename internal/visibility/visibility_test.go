package visibility

import (
	"testing"

	"github.com/iag/project-management/backend/internal/models"
)

func TestApplyHidesMembersOnlyProject(t *testing.T) {
	doc := models.Document{
		Projects: map[string]models.Project{
			"secret": {
				ID:         "secret",
				Visibility: models.ProjectVisibilityMembersOnly,
				MemberIDs:  []string{"member-1"},
			},
			"open": {ID: "open"},
		},
		Tasks: []models.Task{
			{ID: 1, Name: "hidden", Project: "secret"},
			{ID: 2, Name: "visible", Project: "open"},
		},
	}
	Apply(&doc, "owner-1", "outsider")
	if _, ok := doc.Projects["secret"]; ok {
		t.Fatal("secret project should be hidden")
	}
	if len(doc.Tasks) != 1 || doc.Tasks[0].ID != 2 {
		t.Fatalf("tasks = %+v, want only visible task", doc.Tasks)
	}
}

func TestOwnerSeesAll(t *testing.T) {
	doc := models.Document{
		Projects: map[string]models.Project{
			"secret": {ID: "secret", Visibility: models.ProjectVisibilityMembersOnly, MemberIDs: []string{"member-1"}},
		},
	}
	Apply(&doc, "owner-1", "owner-1")
	if len(doc.Projects) != 1 {
		t.Fatal("owner should see private project")
	}
}
