package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/auth"
	"squidadmin/backend/internal/squid"
)

type ServiceHandler struct {
	mgr *squid.Manager
}

func NewServiceHandler(mgr *squid.Manager) *ServiceHandler {
	return &ServiceHandler{mgr: mgr}
}

func (h *ServiceHandler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, h.mgr.ServiceInfo())
}

// serviceActionRole is the minimum role for each action. Operators can bring
// squid up and reload it; taking it down (which drops every user's
// connection) or changing its boot behaviour is admin-only.
var serviceActionRole = map[string]auth.Role{
	"start":   auth.RoleOperator,
	"reload":  auth.RoleOperator,
	"stop":    auth.RoleAdmin,
	"restart": auth.RoleAdmin,
	"enable":  auth.RoleAdmin,
	"disable": auth.RoleAdmin,
}

// Action runs start / stop / restart / reload / enable / disable and returns
// the fresh service state so the UI can update without a second request.
func (h *ServiceHandler) Action(c *gin.Context) {
	action := c.Param("action")
	minRole, known := serviceActionRole[action]
	if !known {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action: " + action})
		return
	}
	if !currentUser(c).Role.AtLeast(minRole) {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "only " + string(minRole) + " users can " + action + " squid",
			"code":  "forbidden",
		})
		return
	}

	if err := h.mgr.ServiceAction(action); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   err.Error(),
			"service": h.mgr.ServiceInfo(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": h.mgr.ServiceInfo()})
}
