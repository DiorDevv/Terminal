package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
)

// PanelUsersHandler manages the accounts that can sign in to this panel
// (distinct from the squid proxy users). Admin only.
type PanelUsersHandler struct {
	svc   *auth.Service
	audit *audit.Store
}

func NewPanelUsersHandler(svc *auth.Service, store *audit.Store) *PanelUsersHandler {
	return &PanelUsersHandler{svc: svc, audit: store}
}

func respondUserError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrUserExists), errors.Is(err, auth.ErrLastAdmin):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, auth.ErrUserMissing):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	}
}

func userID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

func (h *PanelUsersHandler) List(c *gin.Context) {
	users, err := h.svc.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

type createPanelUserRequest struct {
	Username string    `json:"username" binding:"required"`
	Password string    `json:"password" binding:"required"`
	Role     auth.Role `json:"role" binding:"required"`
}

func (h *PanelUsersHandler) Create(c *gin.Context) {
	var req createPanelUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username, password and role are required"})
		return
	}
	user, err := h.svc.CreateUser(req.Username, req.Password, req.Role)
	if err != nil {
		respondUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

type updatePanelUserRequest struct {
	Role     *auth.Role `json:"role"`
	Disabled *bool      `json:"disabled"`
}

func (h *PanelUsersHandler) Update(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	var req updatePanelUserRequest
	if err := c.ShouldBindJSON(&req); err != nil || (req.Role == nil && req.Disabled == nil) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role and/or disabled is required"})
		return
	}
	if id == currentUser(c).ID && req.Disabled != nil && *req.Disabled {
		c.JSON(http.StatusConflict, gin.H{"error": "you cannot disable your own account"})
		return
	}

	user, err := h.svc.UpdateUser(id, req.Role, req.Disabled)
	if err != nil {
		respondUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}

type resetPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

// ResetPassword sets a temporary password; the user must change it at their
// next sign-in and is signed out everywhere.
func (h *PanelUsersHandler) ResetPassword(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password is required"})
		return
	}
	if err := h.svc.ResetPassword(id, req.Password); err != nil {
		respondUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "password reset"})
}

func (h *PanelUsersHandler) Delete(c *gin.Context) {
	id, ok := userID(c)
	if !ok {
		return
	}
	if id == currentUser(c).ID {
		c.JSON(http.StatusConflict, gin.H{"error": "you cannot delete your own account"})
		return
	}
	if err := h.svc.DeleteUser(id); err != nil {
		respondUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// AuditHandler exposes the audit log. Admin only.
type AuditHandler struct {
	store *audit.Store
}

func NewAuditHandler(store *audit.Store) *AuditHandler {
	return &AuditHandler{store: store}
}

func (h *AuditHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)

	entries, err := h.store.List(audit.Filter{
		Limit:    limit,
		BeforeID: before,
		Username: c.Query("username"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}
