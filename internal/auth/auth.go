package auth

import (
	"net/http"
	"strings"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/middleware"
	"github.com/alvor-technologies/iag-platform-go/apierr"
)

func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.UserID(c); !ok {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		c.Next()
	}
}

func RequireStaff() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := middleware.PlatformClaims(c)
		if !ok {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		if !claims.IsStaff && !isPlatformSuperuser(claims) {
			apierr.Forbidden(c, "staff access required")
			return
		}
		c.Next()
	}
}

func RequirePerm(codename string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.UserID(c); !ok {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		if !HasPerm(c, codename) {
			apierr.WriteWith(c, http.StatusForbidden, apierr.CodeForbidden, "permission denied: "+codename, gin.H{"required_permission": codename})
			return
		}
		c.Next()
	}
}

func RequireWorkspaceRead() gin.HandlerFunc {
	return requireAnyPerm("pm.view_workspace", "pm.mutate_workspace")
}

func RequireWorkspaceWrite() gin.HandlerFunc {
	return requireAnyPerm("pm.mutate_workspace")
}

func requireAnyPerm(codenames ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.UserID(c); !ok {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		for _, codename := range codenames {
			if HasPerm(c, codename) {
				c.Next()
				return
			}
		}
		apierr.Forbidden(c, "permission denied")
	}
}

func HasPerm(c *gin.Context, codename string) bool {
	claims, ok := middleware.PlatformClaims(c)
	if !ok {
		return false
	}
	return HasPermClaims(claims, middleware.Permissions(c), codename)
}

// HasPermClaims evaluates a codename against JWT claims (WebSocket upgrade, tests).
func HasPermClaims(claims *authclient.Claims, granted []string, codename string) bool {
	if claims != nil && isPlatformSuperuser(claims) {
		return true
	}
	for _, p := range granted {
		if p == "*" || p == codename {
			return true
		}
		if strings.HasPrefix(codename, "pm.") && p == strings.TrimPrefix(codename, "pm.") {
			return true
		}
	}
	return false
}

func isPlatformSuperuser(claims *authclient.Claims) bool {
	if claims == nil {
		return false
	}
	if claims.IsSuperuser {
		return true
	}
	for _, g := range claims.Groups {
		if g == "superadmin" {
			return true
		}
	}
	return false
}
