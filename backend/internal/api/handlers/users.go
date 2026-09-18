package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type UsersHandler struct {
	users *squid.UserManager
	mgr   *squid.Manager
}

func NewUsersHandler(users *squid.UserManager, mgr *squid.Manager) *UsersHandler {
	return &UsersHandler{users: users, mgr: mgr}
}

func (h *UsersHandler) List(c *gin.Context) {
	users, err := h.users.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

type createUserRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *UsersHandler) Add(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
		return
	}

	if err := h.users.AddUser(req.Username, req.Password); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	if err := h.users.EnsureAuthDirectives(); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status":  "user saved",
			"warning": "could not enable proxy auth in squid.conf: " + err.Error(),
		})
		return
	}

	resp := gin.H{"status": "saved", "reloaded": true}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}

func (h *UsersHandler) Remove(c *gin.Context) {
	username := c.Param("username")

	if err := h.users.RemoveUser(username); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"status": "saved", "reloaded": true}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}
