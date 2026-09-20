package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type HistoryHandler struct {
	mgr *squid.Manager
}

func NewHistoryHandler(mgr *squid.Manager) *HistoryHandler {
	return &HistoryHandler{mgr: mgr}
}

func (h *HistoryHandler) List(c *gin.Context) {
	hist := h.mgr.History()
	if hist == nil {
		c.JSON(http.StatusOK, gin.H{"versions": []squid.Version{}})
		return
	}

	versions, err := hist.List(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (h *HistoryHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	hist := h.mgr.History()
	if hist == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "config history is not enabled"})
		return
	}

	v, err := hist.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": v})
}

// Restore writes an old version back as the current squid.conf and reloads
// squid. The reload is verified and rolled back automatically on failure, like
// every other change.
func (h *HistoryHandler) Restore(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.mgr.Restore(id); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"status": "restored", "reloaded": true}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}
