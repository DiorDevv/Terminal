package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/monitor"
	"squidadmin/backend/internal/squid"
)

// MonitorHandler serves statistics, live squid data, log files and the alert
// settings. Statistics and logs are for operators and above (they show which
// user visited what); alert settings hold channel secrets, so they are
// admin-only — the router enforces both.
type MonitorHandler struct {
	store   *monitor.Store
	ingest  *monitor.Ingestor
	alerts  *monitor.Alerts
	mgr     *squid.Manager
	logDir  string
	infoURL string
	// disks are the paths whose free space is reported.
	disks []string
}

func NewMonitorHandler(store *monitor.Store, ingest *monitor.Ingestor, alerts *monitor.Alerts, mgr *squid.Manager, logDir, infoURL string, disks []string) *MonitorHandler {
	return &MonitorHandler{store: store, ingest: ingest, alerts: alerts, mgr: mgr, logDir: logDir, infoURL: infoURL, disks: disks}
}

func rangeParam(c *gin.Context) string {
	if r := c.Query("range"); r != "" {
		return r
	}
	return "24h"
}

func limitParam(c *gin.Context, def, max int) int {
	n, err := strconv.Atoi(c.Query("limit"))
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (h *MonitorHandler) Summary(c *gin.Context) {
	s, err := h.store.Summary(rangeParam(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}

func (h *MonitorHandler) Top(c *gin.Context) {
	by, sortBy := c.DefaultQuery("by", "domain"), c.DefaultQuery("sort", "requests")
	rows, err := h.store.Top(rangeParam(c), by, sortBy, limitParam(c, 10, 100))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

func (h *MonitorHandler) Denied(c *gin.Context) {
	rows, err := h.store.Denied(limitParam(c, 50, 500))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

func (h *MonitorHandler) disksUsage() []monitor.Usage {
	out := []monitor.Usage{}
	for _, p := range h.disks {
		if u, err := monitor.DiskUsage(p); err == nil {
			u.Path = p
			out = append(out, u)
		}
	}
	return out
}

// Live is what the statistics page polls: squid's own numbers, whether it is
// running, and whether the log reader is keeping up.
func (h *MonitorHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"squid":  h.mgr.GetStatus(),
		"info":   monitor.FetchInfo(h.infoURL),
		"reader": h.ingest.Status(),
		"disks":  h.disksUsage(),
	})
}

func (h *MonitorHandler) Logs(c *gin.Context) {
	files, err := monitor.ListLogFiles(h.logDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"dir": h.logDir, "files": files, "disks": h.disksUsage()})
}

func (h *MonitorHandler) LogTail(c *gin.Context) {
	path, err := monitor.SafeLogPath(h.logDir, c.Param("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	lines, err := monitor.TailLines(path, limitParam(c, 200, 1000))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "the log file cannot be read"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": c.Param("name"), "lines": lines})
}

func (h *MonitorHandler) Rotate(c *gin.Context) {
	if err := h.mgr.RotateLogs(); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "rotated"})
}

// ------------------------------------------------------------------- alerts

func (h *MonitorHandler) Alerts(c *gin.Context) {
	cfg, err := h.alerts.Config()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	status, err := h.alerts.Status()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	events, err := h.alerts.Events(limitParam(c, 50, 200))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": monitor.View(cfg), "status": status, "events": events})
}

func (h *MonitorHandler) SetAlertConfig(c *gin.Context) {
	var in monitor.Config
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	saved, err := h.alerts.SetConfig(in)
	if err != nil {
		var ce *monitor.ConfigError
		if errors.As(err, &ce) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error(), "fields": ce.Fields})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "saved", "config": monitor.View(saved)})
}

// TestAlert sends a test message through every enabled channel.
func (h *MonitorHandler) TestAlert(c *gin.Context) {
	results, err := h.alerts.Test()
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// CheckAlerts runs one round of checks now instead of waiting for the timer.
func (h *MonitorHandler) CheckAlerts(c *gin.Context) {
	events, err := h.alerts.Check()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	status, _ := h.alerts.Status()
	if events == nil {
		events = []monitor.Event{}
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "status": status})
}
