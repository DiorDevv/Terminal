package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAPIHardening(t *testing.T) {
	e := newEnv(t)

	// Every answer carries the security headers and is not cacheable.
	for _, path := range []string{"/api/health", "/api/auth/me"} {
		w := e.do(req{method: "GET", path: path})
		h := w.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" ||
			h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: missing security headers: %v", path, h)
		}
	}

	// A huge body is refused instead of buffered, even before signing in.
	huge := strings.Repeat("x", 5<<20)
	w := e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": huge, "password": "p"}})
	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized login body must be rejected, got %d", w.Code)
	}
	// A normal login attempt is unaffected.
	if w := e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": "nobody", "password": "wrong-password"}}); w.Code != 401 {
		t.Errorf("a normal login attempt: %d", w.Code)
	}
}

func TestLoginLimiterForgetsOldVisitors(t *testing.T) {
	l := NewLoginRateLimiter(3, 20*time.Millisecond)
	r := gin.New()
	r.POST("/login", l.Middleware(), func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	fail := func(ip string) {
		hr := httptest.NewRequest("POST", "/login", nil)
		hr.RemoteAddr = ip + ":1234"
		r.ServeHTTP(httptest.NewRecorder(), hr)
	}
	size := func() int {
		l.mu.Lock()
		defer l.mu.Unlock()
		return len(l.attempts)
	}

	// Fifty one-off visitors, none of whom ever comes back.
	for i := 0; i < 50; i++ {
		fail("10.0.0." + strconv.Itoa(i))
	}
	if size() < 50 {
		t.Fatalf("setup: the failures should be tracked while fresh, map holds %d", size())
	}
	time.Sleep(40 * time.Millisecond)

	// Any later request, from anyone, clears out everybody whose window passed.
	fail("192.0.2.1")
	if n := size(); n > 1 {
		t.Errorf("expired visitors must not accumulate, map holds %d addresses", n)
	}
}
