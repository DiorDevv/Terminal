package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"squidadmin/backend/internal/auth"
)

func (e *env) logPath() string { return filepath.Join(filepath.Dir(e.confPath), "log", "access.log") }

func dialLogs(srv *httptest.Server, cookie, origin string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	h.Set("Cookie", "squidadmin_session="+cookie)
	if origin != "" {
		h.Set("Origin", origin)
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/squid/logs/stream"
	return websocket.DefaultDialer.Dial(url, h)
}

func TestLiveLogStream(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	viewer := e.user("vera", auth.RoleViewer)
	srv := httptest.NewServer(e.router)
	defer srv.Close()

	if err := os.WriteFile(e.logPath(), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Only operators may watch traffic; a foreign web page may not either.
	if _, resp, err := dialLogs(srv, viewer, frontend); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("a viewer must be refused: %v %v", err, resp)
	}
	if _, resp, err := dialLogs(srv, op, "https://evil.example"); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("a foreign origin must be refused: %v %v", err, resp)
	}
	if _, resp, err := dialLogs(srv, "not-a-session", frontend); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no session must be refused: %v %v", err, resp)
	}

	conn, _, err := dialLogs(srv, op, frontend)
	if err != nil {
		t.Fatalf("operator dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(800 * time.Millisecond) // the tail starts at the end of the file

	f, _ := os.OpenFile(e.logPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("hello from squid\n")
	f.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil || string(msg) != "hello from squid\n" {
		t.Fatalf("got %q, %v", msg, err)
	}
}

func TestLiveLogStreamExplainsAnUnreadableLog(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	srv := httptest.NewServer(e.router)
	defer srv.Close()
	os.Remove(e.logPath()) // there is no access.log

	conn, _, err := dialLogs(srv, op, frontend)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if !ok || ce.Code != websocket.CloseInternalServerErr || !strings.Contains(ce.Text, "cannot be read") {
		t.Errorf("the socket must close with a reason, got %v", err)
	}
}

func TestLiveLogViewersAreCapped(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	srv := httptest.NewServer(e.router)
	defer srv.Close()
	os.WriteFile(e.logPath(), nil, 0o644)

	var open []*websocket.Conn
	defer func() {
		for _, c := range open {
			c.Close()
		}
	}()
	for i := 0; i < 20; i++ {
		c, _, err := dialLogs(srv, op, frontend)
		if err != nil {
			t.Fatalf("viewer %d should be allowed: %v", i+1, err)
		}
		open = append(open, c)
	}
	_, resp, err := dialLogs(srv, op, frontend)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("the 21st viewer must be refused with 503: %v %v", err, resp)
	}

	// Closing one frees a slot.
	open[0].Close()
	open = open[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _, err := dialLogs(srv, op, frontend)
		if err == nil {
			open = append(open, c)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a slot was not released after a viewer left: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestLiveLogSocketRefusesBigClientFrames(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	srv := httptest.NewServer(e.router)
	defer srv.Close()
	os.WriteFile(e.logPath(), nil, 0o644)

	conn, _, err := dialLogs(srv, op, frontend)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// The stream never expects data from the browser, so a large frame is abuse.
	// The server may hang up while the client is still sending the big frame, so
	// the write itself can fail with "connection reset": that is the refusal too.
	writeErr := conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 64<<10)))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	ce, ok := err.(*websocket.CloseError)
	if writeErr == nil && (!ok || ce.Code != websocket.CloseMessageTooBig) {
		t.Errorf("an oversized client frame must close the socket with 1009, got %v", err)
	}
	if writeErr != nil && err == nil {
		t.Errorf("the write failed (%v) yet the socket still delivers messages", writeErr)
	}
}
