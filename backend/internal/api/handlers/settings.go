package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

// SettingsHandler serves the form-based squid settings.
type SettingsHandler struct {
	mgr *squid.Manager
}

func NewSettingsHandler(mgr *squid.Manager) *SettingsHandler {
	return &SettingsHandler{mgr: mgr}
}

// restartTimeout bounds a verified restart: squid may wait shutdown_lifetime
// for open connections, then run its cache init before it is ready.
const restartTimeout = 150 * time.Second

func (h *SettingsHandler) Get(c *gin.Context) {
	values, err := h.mgr.Settings()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": values, "groups": squid.SettingGroups})
}

func respondSettingsError(c *gin.Context, err error) {
	var se *squid.SettingsError
	if errors.As(err, &se) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "fields": se.Fields})
		return
	}
	c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
}

// Update applies a partial settings update. With ?dry_run=1 it only validates
// and reports the exact config lines that would change.
func (h *SettingsHandler) Update(c *gin.Context) {
	var req squid.SettingsUpdate
	if err := c.ShouldBindJSON(&req); err != nil || (len(req.Values) == 0 && len(req.Reset) == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "values and/or reset is required"})
		return
	}

	if q := c.Query("dry_run"); q == "1" || q == "true" {
		res, err := h.mgr.PreviewSettings(req)
		if err != nil {
			respondSettingsError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"dry_run": true, "changed": res.Changed,
			"restart_required": res.RestartRequired, "changes": res.Changes,
		})
		return
	}

	res, err := h.mgr.ApplySettings(req)
	if err != nil {
		respondSettingsError(c, err)
		return
	}

	resp := gin.H{
		"status": "saved", "changed": res.Changed,
		"restart_required": res.RestartRequired, "changes": res.Changes,
	}
	switch {
	case res.RestartRequired:
		// Reloading a new cache_dir would just make squid die; the change
		// waits for a verified restart instead.
	case len(res.Changed) > 0:
		resp["reloaded"] = true
		if err := h.mgr.Reconfigure(); err != nil {
			resp["reloaded"] = false
			resp["reload_error"] = err.Error()
		}
	}
	c.JSON(http.StatusOK, resp)
}

// Restart restarts squid and verifies it comes back with the current config,
// rolling the config back if it does not.
func (h *SettingsHandler) Restart(c *gin.Context) {
	if err := h.mgr.RestartVerified(restartTimeout); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "service": h.mgr.ServiceInfo()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "restarted", "service": h.mgr.ServiceInfo()})
}
