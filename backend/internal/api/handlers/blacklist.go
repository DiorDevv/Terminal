package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type BlacklistHandler struct {
	bl  *squid.BlacklistManager
	mgr *squid.Manager
}

func NewBlacklistHandler(bl *squid.BlacklistManager, mgr *squid.Manager) *BlacklistHandler {
	return &BlacklistHandler{bl: bl, mgr: mgr}
}

func (h *BlacklistHandler) List(c *gin.Context) {
	domains, err := h.bl.ListDomains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

type domainRequest struct {
	Domain string `json:"domain" binding:"required"`
}

func (h *BlacklistHandler) Add(c *gin.Context) {
	var req domainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "domain is required"})
		return
	}

	if err := h.bl.AddDomain(req.Domain); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	h.respondWithReload(c)
}

func (h *BlacklistHandler) Remove(c *gin.Context) {
	domain := c.Param("domain")

	if err := h.bl.RemoveDomain(domain); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	h.respondWithReload(c)
}

// respondWithReload tells squid to pick up the changed blacklist file. A
// reload failure (e.g. squid isn't running) doesn't undo the file change,
// so it's reported as a warning rather than an error.
func (h *BlacklistHandler) respondWithReload(c *gin.Context) {
	resp := gin.H{"status": "saved", "reloaded": true}

	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}

	c.JSON(http.StatusOK, resp)
}
