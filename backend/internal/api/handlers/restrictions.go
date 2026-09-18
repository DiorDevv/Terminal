package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type RestrictionsHandler struct {
	restr *squid.RestrictionManager
	mgr   *squid.Manager
}

func NewRestrictionsHandler(restr *squid.RestrictionManager, mgr *squid.Manager) *RestrictionsHandler {
	return &RestrictionsHandler{restr: restr, mgr: mgr}
}

func (h *RestrictionsHandler) List(c *gin.Context) {
	list, err := h.restr.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"restrictions": list})
}

func (h *RestrictionsHandler) Create(c *gin.Context) {
	var req squid.Restriction
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	created, err := h.restr.Create(req)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"restriction": created, "reloaded": true}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}

func (h *RestrictionsHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.restr.Delete(id); err != nil {
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
