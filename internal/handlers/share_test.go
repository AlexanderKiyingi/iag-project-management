package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

func TestShareTokenAllowsExactlyOneRecord(t *testing.T) {
	if !shareTokenAllows(jwt.MapClaims{"resource": "task:42"}, "task:42") {
		t.Fatal("exact match")
	}
	// task:* once opened every task in every workspace.
	for _, claim := range []string{"task:*", "task:4*", "", "task:43"} {
		if shareTokenAllows(jwt.MapClaims{"resource": claim}, "task:42") {
			t.Errorf("%q must not grant task:42", claim)
		}
	}
}

func TestHonouredChecksIssuerAndCreator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	statuses := map[string]shareStatus{
		"live":    {Active: true, Resource: "task:42", CreatedBy: "u-member"},
		"revoked": {Active: false, Reason: "revoked", Resource: "task:42"},
		"other":   {Active: true, Resource: "task:99", CreatedBy: "u-member"},
		"legacy":  {Active: true, Resource: "task:42", CreatedBy: "u-owner"},
	}
	var calls int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var body struct{ JTI string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(statuses[body.JTI])
	}))
	defer issuer.Close()
	h := &PublicShare{CheckURL: issuer.URL}
	doc := models.Document{Members: []models.Member{{UserID: "u-member"}}}
	ws := store.Workspace{OwnerUserID: "u-owner"}

	serve := func(claims jwt.MapClaims) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/shared/x/task/42", nil)
		if h.honoured(c, claims, "task:42", doc, ws) {
			return http.StatusOK
		}
		return w.Code
	}
	cases := []struct {
		name   string
		claims jwt.MapClaims
		want   int
	}{
		{"a live link by a member", jwt.MapClaims{"jti": "live", "cre": "u-member"}, http.StatusOK},
		{"a revoked link", jwt.MapClaims{"jti": "revoked", "cre": "u-member"}, http.StatusGone},
		{"the issuer records another resource", jwt.MapClaims{"jti": "other", "cre": "u-member"}, http.StatusForbidden},
		{"a creator who cannot see the workspace", jwt.MapClaims{"jti": "live", "cre": "u-outsider"}, http.StatusForbidden},
		{"a legacy link: the issuer names the owner", jwt.MapClaims{"jti": "legacy"}, http.StatusOK},
		{"an unknown link", jwt.MapClaims{"jti": "nope", "cre": "u-member"}, http.StatusGone},
	}
	for _, tc := range cases {
		if got := serve(tc.claims); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	// The answer is cached a minute: a second view does not ask again.
	before := atomic.LoadInt32(&calls)
	serve(jwt.MapClaims{"jti": "live", "cre": "u-member"})
	if atomic.LoadInt32(&calls) != before {
		t.Fatal("status should be cached")
	}

	// Unable to ask the issuer: refuse.
	down := &PublicShare{CheckURL: "http://127.0.0.1:1/none"}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if down.honoured(c, jwt.MapClaims{"jti": "live", "cre": "u-member"}, "task:42", doc, ws) || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("issuer down must fail closed: %d", w.Code)
	}
}

func TestCheckURLDerivedFromJWKS(t *testing.T) {
	h := &PublicShare{JWKSURL: "http://auth.internal:3001/.well-known/jwks.json"}
	if got := h.checkURL(); got != "http://auth.internal:3001/v1/share-tokens/check" {
		t.Fatalf("derived: %s", got)
	}
	if got := (&PublicShare{JWKSURL: "http://x/keys"}).checkURL(); got != "" {
		t.Fatalf("an unrecognised JWKS URL derives nothing: %s", got)
	}
}
