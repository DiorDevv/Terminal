package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/auth"
)

// RequireAuth accepts the token either as an "Authorization: Bearer <token>"
// header (normal API calls) or a "token" query param (browser WebSocket
// connections, which can't set custom headers).
func RequireAuth(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")

		if token == "" {
			header := c.GetHeader("Authorization")
			parts := strings.SplitN(header, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token = parts[1]
			}
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		claims, err := svc.Verify(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}
