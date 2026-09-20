package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/api/origin"
	"squidadmin/backend/internal/api/session"
	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
)

const (
	// CSRFHeader must be present on every state-changing request. A
	// cross-site form or <img> can't set a custom header, and a cross-site
	// fetch that tries would need a CORS preflight the server refuses.
	CSRFHeader = "X-Requested-With"
	CSRFValue  = "squidadmin"
)

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func originAllowed(o string, allowed []string, host string) bool {
	return origin.Allowed(o, allowed, host)
}

// SecurityHeaders marks every API answer as non-cacheable and non-sniffable.
// The answers hold configuration, statistics and account data that must not
// sit in a browser or proxy cache, and the API is never meant to be framed.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}

// MaxBodyBytes is the largest request body the API reads: a raw squid.conf is
// a few hundred KB, everything else is tiny. Without a cap an unauthenticated
// client could make the login endpoint buffer an arbitrarily large body.
const MaxBodyBytes = 4 << 20

func LimitBody(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

// CORS lets the configured front-end origin(s) call the API with cookies. It
// never answers with "*" — credentialed requests forbid it, and echoing an
// arbitrary origin would defeat the point.
func CORS(allowed []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && originAllowed(origin, allowed, c.Request.Host) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, "+CSRFHeader)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// CSRF rejects state-changing requests that lack the custom header, or that
// carry an Origin that isn't ours.
func CSRF(allowed []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if safeMethod(c.Request.Method) {
			c.Next()
			return
		}
		if c.GetHeader(CSRFHeader) != CSRFValue {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing " + CSRFHeader + " header"})
			return
		}
		if origin := c.GetHeader("Origin"); origin != "" && !originAllowed(origin, allowed, c.Request.Host) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin request refused"})
			return
		}
		c.Next()
	}
}

// SessionAuth authenticates the request from the session cookie.
func SessionAuth(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(session.Cookie)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not signed in"})
			return
		}

		user, _, err := svc.Authenticate(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session expired, sign in again"})
			return
		}

		session.Set(c, user, token)
		c.Next()
	}
}

// CurrentUser returns the authenticated user; only valid behind SessionAuth.
func CurrentUser(c *gin.Context) auth.User { return session.User(c) }

// RequirePasswordChange blocks everything except the endpoints needed to fix
// the situation, while the account still has a temporary password.
func RequirePasswordChange(allowedPaths ...string) gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, p := range allowedPaths {
		allowed[p] = true
	}
	return func(c *gin.Context) {
		if CurrentUser(c).MustChangePassword && !allowed[c.FullPath()] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "you must change your password before continuing",
				"code":  "password_change_required",
			})
			return
		}
		c.Next()
	}
}

// RequireRole allows only users holding at least the given role.
func RequireRole(min auth.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !CurrentUser(c).Role.AtLeast(min) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "your role (" + string(CurrentUser(c).Role) + ") is not allowed to do this",
				"code":  "forbidden",
			})
			return
		}
		c.Next()
	}
}

// Audit records every state-changing request (and its outcome) with the acting
// user. Bodies are deliberately not stored: they can contain passwords.
func Audit(store *audit.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if safeMethod(c.Request.Method) {
			return
		}

		var target []string
		for _, p := range c.Params {
			target = append(target, p.Key+"="+p.Value)
		}

		store.Record(audit.Entry{
			Username: session.Username(c),
			IP:       c.ClientIP(),
			Action:   c.Request.Method + " " + strings.TrimPrefix(c.FullPath(), "/api"),
			Target:   strings.Join(target, " "),
			Status:   c.Writer.Status(),
		})
	}
}
