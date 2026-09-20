package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"squidadmin/backend/internal/squid"
)

type SquidHandler struct {
	mgr       *squid.Manager
	accessLog string
	upgrader  *websocket.Upgrader
	streams   chan struct{}
}

func NewSquidHandler(mgr *squid.Manager, accessLog string, upgrader *websocket.Upgrader) *SquidHandler {
	return &SquidHandler{mgr: mgr, accessLog: accessLog, upgrader: upgrader, streams: make(chan struct{}, maxLogStreams)}
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

const (
	// maxLogStreams caps concurrent live-log viewers: each one holds a goroutine
	// and an open file until its tab is closed.
	maxLogStreams = 20
	pingEvery     = 30 * time.Second
	// pongWait is how long a silent client is tolerated; a browser answers pings
	// on its own, so a longer silence means the connection is dead.
	pongWait = 70 * time.Second
)

// StreamLogs upgrades to a websocket and pushes new access.log lines as they
// arrive, until the client disconnects. If the log cannot be read the socket is
// closed with a reason instead of staying open and silent.
func (h *SquidHandler) StreamLogs(c *gin.Context) {
	select {
	case h.streams <- struct{}{}:
		defer func() { <-h.streams }()
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many live log viewers; close another tab and try again"})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// The client never sends anything meaningful; a small limit keeps a hostile
	// one from making the server buffer big frames.
	conn.SetReadLimit(1024)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	lines := make(chan string, 100)
	tailErr := make(chan error, 1)
	go func() { tailErr <- squid.TailAccessLog(ctx, h.accessLog, lines) }()

	// Detect a client-initiated close (or a dead connection) so the tail stops.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-tailErr:
			if err != nil {
				conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "the access log cannot be read"),
					time.Now().Add(time.Second))
			}
			return
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		case line := <-lines:
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
				return
			}
		}
	}
}
