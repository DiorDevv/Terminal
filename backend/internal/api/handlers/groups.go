package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

type GroupsHandler struct {
	groups *squid.GroupManager
	restr  *squid.RestrictionManager
	mgr    *squid.Manager
}

func NewGroupsHandler(groups *squid.GroupManager, restr *squid.RestrictionManager, mgr *squid.Manager) *GroupsHandler {
	return &GroupsHandler{groups: groups, restr: restr, mgr: mgr}
}

func (h *GroupsHandler) List(c *gin.Context) {
	list, err := h.groups.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": list})
}

type createGroupRequest struct {
	Name    string   `json:"name" binding:"required"`
	Members []string `json:"members" binding:"required"`
}

func (h *GroupsHandler) Create(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and members are required"})
		return
	}

	group, err := h.groups.Create(req.Name, req.Members)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": group})
}

// Delete removes a group. Since restrictions reference it, squid.conf is
// regenerated afterward so any restriction that used this group as its
// exemption stops referencing IPs that no longer resolve to anything.
func (h *GroupsHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.groups.Delete(id); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"status": "saved", "reloaded": true}
	if err := h.restr.Regenerate(); err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "saved", "warning": "could not refresh squid.conf: " + err.Error()})
		return
	}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	c.JSON(http.StatusOK, resp)
}
