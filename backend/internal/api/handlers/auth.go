package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/api/session"
	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
)

func currentUser(c *gin.Context) auth.User { return session.User(c) }
func currentToken(c *gin.Context) string   { return session.Token(c) }

type AuthHandler struct {
	svc          *auth.Service
	audit        *audit.Store
	cookieSecure bool
}

func NewAuthHandler(svc *auth.Service, store *audit.Store, cookieSecure bool) *AuthHandler {
	return &AuthHandler{svc: svc, audit: store, cookieSecure: cookieSecure}
}

// secure decides the cookie's Secure flag: always when configured, and
// automatically when the request itself arrived over HTTPS (directly or via a
// TLS-terminating proxy).
func (h *AuthHandler) secure(c *gin.Context) bool {
	return h.cookieSecure || c.Request.TLS != nil ||
		strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

func (h *AuthHandler) setCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(session.Cookie, token, maxAge, "/", "", h.secure(c), true)
}

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	entry := audit.Entry{Username: req.Username, IP: c.ClientIP(), Action: "LOGIN"}

	token, user, err := h.svc.Login(req.Username, req.Password, c.ClientIP(), c.GetHeader("User-Agent"))
	switch {
	case errors.Is(err, auth.ErrLocked):
		entry.Status, entry.Detail = http.StatusTooManyRequests, "account locked"
		h.audit.Record(entry)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		entry.Status, entry.Detail = http.StatusUnauthorized, "wrong username or password"
		h.audit.Record(entry)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "login failed"})
		return
	}

	entry.Status = http.StatusOK
	h.audit.Record(entry)

	h.setCookie(c, token, int((7 * 24 * time.Hour).Seconds()))
	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (h *AuthHandler) Logout(c *gin.Context) {
	_ = h.svc.Logout(currentToken(c))
	h.setCookie(c, "", -1)
	c.JSON(http.StatusOK, gin.H{"status": "signed out"})
}

func (h *AuthHandler) Me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"user": currentUser(c)})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// ChangePassword answers a wrong current password with 422, not 401: the
// front-end treats 401 as "session expired" and would sign the user out.
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "old_password and new_password are required"})
		return
	}

	user := currentUser(c)
	err := h.svc.ChangePassword(user.ID, req.OldPassword, req.NewPassword, currentToken(c))
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "current password is incorrect"})
	case err != nil:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, gin.H{"status": "password changed"})
	}
}

func (h *AuthHandler) Sessions(c *gin.Context) {
	sessions, err := h.svc.Sessions(currentUser(c).ID, currentToken(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (h *AuthHandler) RevokeSession(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.svc.RevokeSession(currentUser(c).ID, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}
