// Package docs serves the hand-curated OpenAPI 3.1 spec for the PM
// service. The spec is embedded at build time and exposed at
// /openapi.json and /openapi.yaml. Whenever a route is added in
// internal/handlers, update internal/docs/openapi.yaml in the same PR
// — the audit job and frontend integration doc both rely on this being
// the source of truth for the API contract.
package docs

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

//go:embed openapi.yaml
var openAPIYAML []byte

// Register attaches /openapi.json and /openapi.yaml to the engine.
// These are unauthenticated by design — the contract is public.
func Register(r *gin.Engine) {
	r.GET("/openapi.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", openAPIYAML)
	})
	r.GET("/openapi.json", func(c *gin.Context) {
		var doc any
		if err := yaml.Unmarshal(openAPIYAML, &doc); err != nil {
			apierr.JSONStatus(c, http.StatusInternalServerError, "spec parse failed")
			return
		}
		// yaml.v3 decodes maps as map[string]any when the keys are strings,
		// but nested maps can come back as map[any]any from generic
		// scalars. Normalize before marshalling to JSON.
		c.JSON(http.StatusOK, normalizeMaps(doc))
	})
	r.GET("/docs", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(swaggerHTML))
	})
}

// SpecBytes returns the raw YAML bytes, useful for the OpenAPI audit job.
func SpecBytes() []byte {
	return openAPIYAML
}

// SpecMarshalJSON returns the OpenAPI spec as JSON bytes.
func SpecMarshalJSON() ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal(openAPIYAML, &doc); err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalizeMaps(doc), "", "  ")
}

func normalizeMaps(v any) any {
	switch t := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			out[ks] = normalizeMaps(val)
		}
		return out
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeMaps(val)
		}
		return t
	case []any:
		for i, item := range t {
			t[i] = normalizeMaps(item)
		}
		return t
	default:
		return v
	}
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>IAG Project Management API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
window.onload = function() {
  window.ui = SwaggerUIBundle({ url: "/openapi.json", dom_id: "#swagger" });
};
</script>
</body>
</html>`
