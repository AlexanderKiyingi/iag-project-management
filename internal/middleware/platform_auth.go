// Package middleware implements Bearer+aud authentication for inbound
// requests. The gateway-header trust path (X-IAG-* + GATEWAY_INTERNAL_SECRET)
// has been removed — every request must carry a verifiable JWT with
// aud=iag.project-management.
package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/iag/project-management/backend/internal/ctxkeys"
)

type PlatformAuth struct {
	verifier *authclient.Verifier
}

type PlatformAuthOptions struct {
	Verifier *authclient.Verifier
}

func NewPlatformAuth(opts PlatformAuthOptions) *PlatformAuth {
	return &PlatformAuth{verifier: opts.Verifier}
}

func isPublicProbePath(path string) bool {
	switch path {
	case "/health", "/healthz", "/ready":
		return true
	default:
		return false
	}
}

// AttachPrincipal validates a Bearer token (if present) and pins the claims +
// user UUID onto the Gin context. It does NOT enforce authentication — pair
// with RequireAuth on routes that must reject unauthenticated callers.
func (m *PlatformAuth) AttachPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isPublicProbePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		if m.verifier == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "jwt verifier not configured"})
			return
		}
		tokenStr := bearerToken(c)
		if tokenStr == "" {
			c.Next()
			return
		}
		claims, userID, err := m.verifier.Verify(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		setPrincipal(c, userID, claims, claims.Permissions)
		c.Next()
	}
}

func (m *PlatformAuth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := UserID(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Next()
	}
}

// VerifyBearerToken validates a raw JWT (e.g. from a WebSocket query param).
func (m *PlatformAuth) VerifyBearerToken(tokenStr string) (uuid.UUID, *authclient.Claims, error) {
	if m.verifier == nil {
		return uuid.Nil, nil, fmt.Errorf("jwt verifier not configured")
	}
	claims, userID, err := m.verifier.Verify(tokenStr)
	if err != nil {
		return uuid.Nil, nil, err
	}
	return userID, claims, nil
}

func bearerToken(c *gin.Context) string {
	if q := strings.TrimSpace(c.Query("token")); q != "" {
		return q
	}
	header := c.GetHeader("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

func setPrincipal(c *gin.Context, userID uuid.UUID, claims *authclient.Claims, perms []string) {
	c.Set(ctxkeys.UserID, userID)
	c.Set(ctxkeys.Claims, claims)
	c.Set(ctxkeys.Permissions, perms)
}

func UserID(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxkeys.UserID)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok
}

func PlatformClaims(c *gin.Context) (*authclient.Claims, bool) {
	v, ok := c.Get(ctxkeys.Claims)
	if !ok {
		return nil, false
	}
	cl, ok := v.(*authclient.Claims)
	return cl, ok
}

func Permissions(c *gin.Context) []string {
	v, ok := c.Get(ctxkeys.Permissions)
	if !ok {
		return nil
	}
	list, _ := v.([]string)
	return list
}
