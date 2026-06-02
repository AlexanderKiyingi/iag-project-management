package auth

import (
	"net/http"
	"strings"

	"github.com/alvor-technologies/iag-authclient"
	"github.com/gin-gonic/gin"

	"github.com/iag/project-management/backend/internal/middleware"
)

func RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.UserID(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		c.Next()
	}
}

func RequireStaff() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := middleware.PlatformClaims(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if !claims.IsStaff && !isPlatformSuperuser(claims) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "staff access required"})
			return
		}
		c.Next()
	}
}

func RequirePerm(codename string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := middleware.UserID(c); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if !HasPerm(c, codename) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission denied", "permission": codename})
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		for _, codename := range codenames {
			if HasPerm(c, codename) {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permission denied"})
	}
}

func HasPerm(c *gin.Context, codename string) bool {
	claims, ok := middleware.PlatformClaims(c)
	if ok && isPlatformSuperuser(claims) {
		return true
	}
	for _, p := range middleware.Permissions(c) {
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
