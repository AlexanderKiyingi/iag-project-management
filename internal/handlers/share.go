package handlers

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/iag/project-management/backend/internal/models"
	"github.com/iag/project-management/backend/internal/store"
)

// PublicShare serves read-only views protected by a public share
// token (issued by iag-authentication's /v1/share-tokens). The token's
// `resource` claim identifies the object the caller may view, e.g.
// "task:42" or "project:design". PM verifies the JWT against auth's
// JWKS (audience iag.share, distinct from the PM verifier's audience),
// then matches the resource against the requested URL.
type PublicShare struct {
	Repo     *store.Repository
	JWKSURL  string
	Issuer   string
	Audience string
	// CheckURL is iag-authentication's POST /v1/share-tokens/check; derived
	// from JWKSURL when empty. A signature says who issued a link, not
	// whether it was since revoked or has expired — that is asked here.
	CheckURL string

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	refresh time.Time

	statusMu sync.Mutex
	statuses map[string]cachedStatus
}

type shareStatus struct {
	Active    bool   `json:"active"`
	Reason    string `json:"reason"`
	Resource  string `json:"resource"`
	CreatedBy string `json:"created_by"`
}

type cachedStatus struct {
	status shareStatus
	at     time.Time
}

// statusTTL bounds how long a revoked link can still be served.
const statusTTL = time.Minute

// Register attaches the share endpoints at the engine level so they
// bypass PlatformAuth.AttachPrincipal (these calls don't carry a
// platform Bearer token; the URL path is the credential).
func (h *PublicShare) Register(r *gin.Engine) {
	r.GET("/shared/:token/task/:id", h.getTask)
	r.GET("/shared/:token/project/:id", h.getProject)
}

func (h *PublicShare) getTask(c *gin.Context) {
	tokenStr := c.Param("token")
	claims, err := h.verifyToken(c.Request.Context(), tokenStr)
	if err != nil {
		apierr.JSONStatus(c, http.StatusUnauthorized, err.Error())
		return
	}
	idStr := c.Param("id")
	resource := "task:" + idStr
	if !shareTokenAllows(claims, resource) {
		apierr.JSONStatus(c, http.StatusForbidden, "token does not grant access to this resource")
		return
	}
	id, err := strconv.Atoi(idStr)
	if err != nil {
		apierr.JSONStatus(c, http.StatusBadRequest, "invalid id")
		return
	}
	doc, ws, err := h.findDocumentByResource(c.Request.Context(), "task", idStr)
	if err != nil {
		apierr.JSONStatus(c, http.StatusNotFound, "task not found")
		return
	}
	if !h.honoured(c, claims, resource, doc, ws) {
		return
	}
	for _, t := range doc.Tasks {
		if t.ID != id || t.DeletedAt != "" {
			continue
		}
		if taskInMembersOnlyProject(doc, t) {
			apierr.JSONStatus(c, http.StatusForbidden, "task is not publicly shareable")
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"task":    t,
			"version": ws.Version,
		})
		return
	}
	apierr.JSONStatus(c, http.StatusNotFound, "task not found")
}

func (h *PublicShare) getProject(c *gin.Context) {
	tokenStr := c.Param("token")
	claims, err := h.verifyToken(c.Request.Context(), tokenStr)
	if err != nil {
		apierr.JSONStatus(c, http.StatusUnauthorized, err.Error())
		return
	}
	idStr := c.Param("id")
	resource := "project:" + idStr
	if !shareTokenAllows(claims, resource) {
		apierr.JSONStatus(c, http.StatusForbidden, "token does not grant access to this resource")
		return
	}
	doc, ws, err := h.findDocumentByResource(c.Request.Context(), "project", idStr)
	if err != nil {
		apierr.JSONStatus(c, http.StatusNotFound, "project not found")
		return
	}
	if !h.honoured(c, claims, resource, doc, ws) {
		return
	}
	project, ok := doc.Projects[idStr]
	if !ok {
		apierr.JSONStatus(c, http.StatusNotFound, "project not found")
		return
	}
	if project.Visibility == models.ProjectVisibilityMembersOnly {
		apierr.JSONStatus(c, http.StatusForbidden, "project is not publicly shareable")
		return
	}
	tasks := []models.Task{}
	for _, t := range doc.Tasks {
		if t.DeletedAt != "" {
			continue
		}
		if taskHomedIn(t, idStr) {
			tasks = append(tasks, t)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"project": project,
		"tasks":   tasks,
		"version": ws.Version,
	})
}

func taskInMembersOnlyProject(doc models.Document, t models.Task) bool {
	projects := t.Projects
	if len(projects) == 0 && t.Project != "" {
		projects = []string{t.Project}
	}
	for _, pid := range projects {
		if p, ok := doc.Projects[pid]; ok && p.Visibility == models.ProjectVisibilityMembersOnly {
			return true
		}
	}
	return false
}

// findDocumentByResource locates the workspace that owns the named
// resource. Slow path (linear scan); acceptable because share-link
// hits are infrequent compared to the authenticated API.
func (h *PublicShare) findDocumentByResource(ctx context.Context, kind, id string) (models.Document, store.Workspace, error) {
	workspaces, err := h.Repo.ListWorkspaces(ctx)
	if err != nil {
		return models.Document{}, store.Workspace{}, err
	}
	for _, ws := range workspaces {
		var doc models.Document
		if err := json.Unmarshal(ws.Document, &doc); err != nil {
			continue
		}
		if resourceLivesIn(doc, kind, id) {
			return doc, ws, nil
		}
	}
	return models.Document{}, store.Workspace{}, errors.New("not found")
}

func resourceLivesIn(doc models.Document, kind, id string) bool {
	switch kind {
	case "task":
		if intID, err := strconv.Atoi(id); err == nil {
			for _, t := range doc.Tasks {
				if t.ID == intID && t.DeletedAt == "" {
					return true
				}
			}
		}
	case "project":
		_, ok := doc.Projects[id]
		return ok
	}
	return false
}

// shareTokenAllows compares the requested resource against the
// `resource` claim in the token: exactly one record. "task:*" wildcards used
// to grant every task in every workspace; the issuer no longer mints them and
// they are refused here for any minted before.
func shareTokenAllows(claims jwt.MapClaims, resource string) bool {
	want, _ := claims["resource"].(string)
	want = strings.TrimSpace(want)
	return want != "" && !strings.Contains(want, "*") && want == resource
}

/*
honoured decides whether a verified token may be served now:

  - the issuer still honours it (not revoked, not expired) and agrees on the
    resource — asked of iag-authentication, cached a minute;
  - its creator could see the record: they own the workspace or are one of
    its members. Links minted before tokens named their creator fall back to
    the creator the issuer recorded.

It writes the refusal itself. Unable to ask the issuer, it refuses: a public
link is the one place to fail closed.
*/
func (h *PublicShare) honoured(c *gin.Context, claims jwt.MapClaims, resource string, doc models.Document, ws store.Workspace) bool {
	jti, _ := claims["jti"].(string)
	st, err := h.status(c.Request.Context(), jti)
	if err != nil {
		apierr.JSONStatus(c, http.StatusServiceUnavailable, "could not confirm the share link; try again shortly")
		return false
	}
	if !st.Active {
		reason := st.Reason
		if reason == "" {
			reason = "inactive"
		}
		apierr.JSONStatus(c, http.StatusGone, "this share link is no longer valid ("+reason+")")
		return false
	}
	if st.Resource != "" && st.Resource != resource {
		apierr.JSONStatus(c, http.StatusForbidden, "token does not grant access to this resource")
		return false
	}
	creator, _ := claims["cre"].(string)
	if creator == "" {
		creator = st.CreatedBy
	}
	if !canSee(creator, doc, ws) {
		apierr.JSONStatus(c, http.StatusForbidden, "the person who shared this cannot see it")
		return false
	}
	return true
}

func canSee(userID string, doc models.Document, ws store.Workspace) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	if ws.OwnerUserID == userID {
		return true
	}
	for _, m := range doc.Members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}

func (h *PublicShare) checkURL() string {
	if h.CheckURL != "" {
		return h.CheckURL
	}
	base := strings.TrimSuffix(strings.TrimSpace(h.JWKSURL), "/.well-known/jwks.json")
	if base == "" || base == strings.TrimSpace(h.JWKSURL) {
		return ""
	}
	return base + "/v1/share-tokens/check"
}

func (h *PublicShare) status(ctx context.Context, jti string) (shareStatus, error) {
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return shareStatus{Reason: "unknown"}, nil
	}
	h.statusMu.Lock()
	if cached, ok := h.statuses[jti]; ok && time.Since(cached.at) < statusTTL {
		h.statusMu.Unlock()
		return cached.status, nil
	}
	h.statusMu.Unlock()
	url := h.checkURL()
	if url == "" {
		return shareStatus{}, errors.New("share check URL not configured")
	}
	body, _ := json.Marshal(map[string]string{"jti": jti})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return shareStatus{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return shareStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return shareStatus{}, fmt.Errorf("share check: %s", resp.Status)
	}
	var st shareStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return shareStatus{}, err
	}
	h.statusMu.Lock()
	if h.statuses == nil {
		h.statuses = map[string]cachedStatus{}
	}
	h.statuses[jti] = cachedStatus{status: st, at: time.Now()}
	h.statusMu.Unlock()
	return st, nil
}

// verifyToken parses the share JWT, fetching auth's JWKS on a 5-minute
// cache. Enforces issuer + audience.
func (h *PublicShare) verifyToken(ctx context.Context, raw string) (jwt.MapClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("missing token")
	}
	if err := h.ensureKeys(ctx); err != nil {
		return nil, fmt.Errorf("jwks unavailable: %w", err)
	}
	h.mu.RLock()
	keys := make(map[string]*rsa.PublicKey, len(h.keys))
	for k, v := range h.keys {
		keys[k] = v
	}
	h.mu.RUnlock()
	parser := jwt.NewParser(
		jwt.WithIssuer(h.Issuer),
		jwt.WithAudience(h.Audience),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	tok, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid != "" {
			if k, ok := keys[kid]; ok {
				return k, nil
			}
		}
		for _, k := range keys {
			return k, nil
		}
		return nil, errors.New("no verification key")
	})
	if err != nil || !tok.Valid {
		return nil, errors.New("invalid token")
	}
	claims, _ := tok.Claims.(jwt.MapClaims)
	return claims, nil
}

func (h *PublicShare) ensureKeys(ctx context.Context) error {
	h.mu.RLock()
	fresh := time.Since(h.refresh) < 5*time.Minute && len(h.keys) > 0
	h.mu.RUnlock()
	if fresh {
		return nil
	}
	if h.JWKSURL == "" {
		return errors.New("JWKS_URL not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.JWKSURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := parseRSAFromJWK(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("no rsa keys in jwks")
	}
	h.mu.Lock()
	h.keys = keys
	h.refresh = time.Now()
	h.mu.Unlock()
	return nil
}

func parseRSAFromJWK(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
