package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

// BlocklistsHandler manages downloadable domain block lists.
type BlocklistsHandler struct {
	svc *squid.BlocklistService
	mgr *squid.Manager
}

func NewBlocklistsHandler(svc *squid.BlocklistService, mgr *squid.Manager) *BlocklistsHandler {
	return &BlocklistsHandler{svc: svc, mgr: mgr}
}

func (h *BlocklistsHandler) List(c *gin.Context) {
	list, err := h.svc.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sources": list})
}

type createBlocklistRequest struct {
	Name          string `json:"name" binding:"required"`
	URL           string `json:"url" binding:"required"`
	IntervalHours int    `json:"interval_hours"`
	AddRule       *bool  `json:"add_rule"`
}

// Create registers a list, downloads it once and (by default) adds a deny rule
// for it.
func (h *BlocklistsHandler) Create(c *gin.Context) {
	var req createBlocklistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and url are required"})
		return
	}
	if req.IntervalHours == 0 {
		req.IntervalHours = 24
	}
	addRule := true
	if req.AddRule != nil {
		addRule = *req.AddRule
	}

	src, err := h.svc.Create(req.Name, req.URL, req.IntervalHours, addRule)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"source": src}))
}

type updateBlocklistRequest struct {
	Enabled       *bool   `json:"enabled"`
	IntervalHours *int    `json:"interval_hours"`
	URL           *string `json:"url"`
}

func (h *BlocklistsHandler) Update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateBlocklistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	src, err := h.svc.Update(id, req.Enabled, req.IntervalHours, req.URL)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"source": src})
}

func (h *BlocklistsHandler) Delete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	removeRules := c.Query("remove_rules") == "1"
	if err := h.svc.Delete(id, removeRules); err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"status": "deleted"}))
}

// Refresh downloads a list right now and reloads squid if it changed.
func (h *BlocklistsHandler) Refresh(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if _, err := h.svc.Get(id); err != nil {
		respondAccessError(c, err)
		return
	}
	res, err := h.svc.RefreshNow(id)
	if err != nil {
		src, _ := h.svc.Get(id)
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "source": src})
		return
	}
	src, _ := h.svc.Get(id)
	c.JSON(http.StatusOK, gin.H{"source": src, "entries": res.Entries, "changed": res.Changed})
}
