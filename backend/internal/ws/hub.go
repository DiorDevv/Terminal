package ws

import (
	"net/http"

	"github.com/gorilla/websocket"

	"squidadmin/backend/internal/api/origin"
)

// NewUpgrader returns a WebSocket upgrader that only accepts browser
// connections from the given front-end origins (or the API's own host).
// Without this any web page could open a socket to the panel from a visitor's
// browser and ride their session cookie (cross-site WebSocket hijacking).
func NewUpgrader(allowedOrigins []string) *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			o := r.Header.Get("Origin")
			if o == "" {
				// Not a browser (browsers always send Origin on WebSocket
				// handshakes); it still has to present a valid session.
				return true
			}
			return origin.Allowed(o, allowedOrigins, r.Host)
		},
	}
}
