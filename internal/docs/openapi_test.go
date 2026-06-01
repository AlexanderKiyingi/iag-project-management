package docs

import (
	"encoding/json"
	"testing"
)

func TestSpecMarshalsToJSON(t *testing.T) {
	b, err := SpecMarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) < 2000 {
		t.Fatalf("spec suspiciously small: %d bytes", len(b))
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("re-parse json: %v", err)
	}
	if openapi, _ := doc["openapi"].(string); openapi == "" {
		t.Fatalf("missing openapi version field")
	}
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) < 20 {
		t.Fatalf("expected at least 20 paths, got %d", len(paths))
	}
	// Spot-check that Phase 1 additions are present.
	mustHave := []string{
		"/api/v1/messages/{id}/reactions",
		"/api/v1/tasks/{id}/subtasks",
		"/api/v1/tasks/bulk",
		"/api/v1/tasks/{id}/projects",
		"/api/v1/projects/{id}/status",
		"/api/v1/goals/{id}/key-results",
		"/api/v1/projects/{id}/sections",
		"/api/v1/workspace/workload",
		"/api/v1/entity-comments/{id}",
		"/api/v1/custom-fields/{id}",
	}
	for _, p := range mustHave {
		if _, ok := paths[p]; !ok {
			t.Errorf("spec missing path %q", p)
		}
	}
}
