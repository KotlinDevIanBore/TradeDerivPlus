package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

const userIDKey = "user_id"

// Auth validates the bearer access token and puts the user id on the context.
// A refresh token presented here is rejected: only access tokens open routes.
func Auth(auth *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			unauthorized(c, "Sign in to continue")
			return
		}

		claims, err := auth.ParseToken(token)
		if err != nil || claims.Kind != "access" {
			unauthorized(c, "Your session has expired")
			return
		}

		c.Set(userIDKey, claims.UserID)
		c.Next()
	}
}

// UserID returns the authenticated user. Only call it behind Auth.
func UserID(c *gin.Context) uuid.UUID {
	v, ok := c.Get(userIDKey)
	if !ok {
		return uuid.Nil
	}
	id, _ := v.(uuid.UUID)
	return id
}

// bearerToken reads the Authorization header, falling back to a query
// parameter because browsers cannot set headers on a WebSocket handshake.
func bearerToken(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if after, found := strings.CutPrefix(header, "Bearer "); found {
		return strings.TrimSpace(after)
	}
	return c.Query("token")
}

func unauthorized(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{"message": msg, "code": "unauthorized"},
	})
}
