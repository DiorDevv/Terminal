package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
	"squidadmin/backend/internal/ws"
)

type SquidHandler struct {
	mgr       *squid.Manager
	accessLog string
}

func NewSquidHandler(mgr *squid.Manager, accessLog string) *SquidHandler {
	return &SquidHandler{mgr: mgr, accessLog: accessLog}
}

func (h *SquidHandler) GetConfig(c *gin.Context) {
	content, err := h.mgr.ReadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": content})
}

type updateConfigRequest struct {
	Content string `json:"content" binding:"required"`
}

func (h *SquidHandler) UpdateConfig(c *gin.Context) {
	var req updateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content is required"})
		return
	}

	if err := h.mgr.WriteConfig(req.Content); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "saved"})
}

func (h *SquidHandler) Reconfigure(c *gin.Context) {
	if err := h.mgr.Reconfigure(); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "reconfigured"})
}

func (h *SquidHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, h.mgr.GetStatus())
}

// StreamLogs upgrades to a websocket and pushes new access.log lines as they
// arrive, until the client disconnects.
func (h *SquidHandler) StreamLogs(c *gin.Context) {
	conn, err := ws.Upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	lines := make(chan string, 100)
	go func() {
		_ = squid.TailAccessLog(ctx, h.accessLog, lines)
	}()

	// Detect client-initiated close so the tail goroutine stops promptly.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case line := <-lines:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(1, []byte(line)); err != nil {
				return
			}
		}
	}
}
