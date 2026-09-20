// Package session holds what the api middleware and the handlers share about
// the signed-in user, so neither has to import the other.
package session

import (
	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/auth"
)

// Cookie holds the opaque session token. It is HttpOnly, so page scripts (and
// therefore XSS) can never read it.
const Cookie = "squidadmin_session"

const (
	ctxUser  = "auth.user"
	ctxToken = "auth.token"
)

// Set stores the authenticated user and their token on the request context.
func Set(c *gin.Context, u auth.User, token string) {
	c.Set(ctxUser, u)
	c.Set(ctxToken, token)
}

// User returns the authenticated user; only valid behind the auth middleware.
func User(c *gin.Context) auth.User { return c.MustGet(ctxUser).(auth.User) }

// Token returns the raw session token of the current request.
func Token(c *gin.Context) string { return c.MustGet(ctxToken).(string) }

// Username returns the user's name, or "" when the request is unauthenticated.
func Username(c *gin.Context) string {
	if v, ok := c.Get(ctxUser); ok {
		return v.(auth.User).Username
	}
	return ""
}
