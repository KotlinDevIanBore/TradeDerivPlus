package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

const (
	adminIDKey   = "admin_id"
	adminRoleKey = "admin_role"
)

// AdminAuth requires a token whose scope claim is "admin". A user token is
// rejected here and an admin token is rejected by Auth, so neither opens the
// other's routes even though both are signed with the same secret.
func AdminAuth(admin *service.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			unauthorized(c, "Sign in to continue")
			return
		}

		claims, err := admin.ParseToken(token)
		if err != nil || claims.Kind != "access" {
			unauthorized(c, "Your session has expired")
			return
		}

		c.Set(adminIDKey, claims.AdminID)
		c.Set(adminRoleKey, claims.Role)
		c.Next()
	}
}

// RequireFullAdmin blocks a session that still has to change its default
// password from doing anything except changing it. The restriction is carried
// in the token itself, so it cannot be sidestepped by calling another route.
func RequireFullAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if AdminRole(c) == service.RoleRestricted {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"message": "Change the default password before continuing",
				"code":    "password_change_required",
			}})
			return
		}
		c.Next()
	}
}

// AdminID returns the authenticated admin. Only call it behind AdminAuth.
func AdminID(c *gin.Context) uuid.UUID {
	v, ok := c.Get(adminIDKey)
	if !ok {
		return uuid.Nil
	}
	id, _ := v.(uuid.UUID)
	return id
}

func AdminRole(c *gin.Context) string {
	v, ok := c.Get(adminRoleKey)
	if !ok {
		return ""
	}
	role, _ := v.(string)
	return role
}
