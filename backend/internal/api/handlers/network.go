package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type NetworkHandler struct {
	mgr *squid.Manager
}

func NewNetworkHandler(mgr *squid.Manager) *NetworkHandler {
	return &NetworkHandler{mgr: mgr}
}

func (h *NetworkHandler) GetLANAccess(c *gin.Context) {
	allowed, err := h.mgr.IsLANAllowed()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"allowed": allowed})
}

type setLANAccessRequest struct {
	Allowed bool `json:"allowed"`
}

func (h *NetworkHandler) SetLANAccess(c *gin.Context) {
	var req setLANAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "allowed (bool) is required"})
		return
	}

	if err := h.mgr.SetLANAllowed(req.Allowed); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"status": "saved", "allowed": req.Allowed, "reloaded": true}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}
