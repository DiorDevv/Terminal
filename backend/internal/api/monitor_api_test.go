package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squidadmin/backend/internal/auth"
)

func TestMonitorRoles(t *testing.T) {
	e := newEnv(t)
	viewer := e.user("vera", auth.RoleViewer)
	operator := e.user("olga", auth.RoleOperator)
	admin := e.user("adam", auth.RoleAdmin)

	cases := []struct {
		who    string
		cookie string
		method string
		path   string
		body   any
		want   int
	}{
		// Statistics and logs reveal who visited what: operator and up.
		{"viewer", viewer, "GET", "/api/monitor/summary", nil, 403},
		{"viewer", viewer, "GET", "/api/monitor/top", nil, 403},
		{"viewer", viewer, "GET", "/api/monitor/denied", nil, 403},
		{"viewer", viewer, "GET", "/api/monitor/live", nil, 403},
		{"viewer", viewer, "GET", "/api/monitor/logs", nil, 403},
		{"viewer", viewer, "GET", "/api/monitor/logs/access.log", nil, 403},
		{"operator", operator, "GET", "/api/monitor/summary", nil, 200},
		{"operator", operator, "GET", "/api/monitor/top", nil, 200},
		{"operator", operator, "GET", "/api/monitor/denied", nil, 200},
		{"operator", operator, "GET", "/api/monitor/live", nil, 200},
		{"operator", operator, "GET", "/api/monitor/logs", nil, 200},

		// Alert settings hold channel secrets; rotation touches squid: admin only.
		{"operator", operator, "GET", "/api/monitor/alerts", nil, 403},
		{"operator", operator, "PUT", "/api/monitor/alerts", map[string]any{}, 403},
		{"operator", operator, "POST", "/api/monitor/alerts/test", nil, 403},
		{"operator", operator, "POST", "/api/monitor/alerts/check", nil, 403},
		{"operator", operator, "POST", "/api/monitor/logs/rotate", nil, 403},
		{"admin", admin, "GET", "/api/monitor/alerts", nil, 200},
		{"admin", admin, "POST", "/api/monitor/alerts/check", nil, 200},
		{"admin", admin, "POST", "/api/monitor/logs/rotate", nil, 200},
	}
	for _, c := range cases {
		w := e.do(req{method: c.method, path: c.path, body: c.body, cookie: c.cookie})
		if w.Code != c.want {
			t.Errorf("%s %s %s: got %d, want %d (%s)", c.who, c.method, c.path, w.Code, c.want, w.Body.String())
		}
	}

	if w := e.do(req{method: "GET", path: "/api/monitor/summary"}); w.Code != 401 {
		t.Errorf("no session: %d", w.Code)
	}
}

func TestMonitorInputIsValidated(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	get := func(path string) int { return e.do(req{method: "GET", path: path, cookie: op}).Code }

	if c := get("/api/monitor/summary?range=24h"); c != 200 {
		t.Errorf("24h: %d", c)
	}
	for _, bad := range []string{
		"/api/monitor/summary?range=1y",
		"/api/monitor/top?by=password",
		"/api/monitor/top?sort=drop",
		"/api/monitor/top?by=domain&sort=requests%3BDROP%20TABLE%20users",
	} {
		if c := get(bad); c != 400 {
			t.Errorf("%s: got %d, want 400", bad, c)
		}
	}
	// A huge limit is capped, not an error and not a giant response.
	if c := get("/api/monitor/denied?limit=999999999"); c != 200 {
		t.Errorf("limit cap: %d", c)
	}
}

func TestLogFilesCannotEscapeTheLogDirectory(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)

	// A secret next to (not inside) the log directory.
	logDir := filepath.Join(filepath.Dir(e.confPath), "log")
	os.WriteFile(filepath.Join(logDir, "cache.log"), []byte("line one\nline two\n"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(logDir), "secret.txt"), []byte("TOPSECRET"), 0o644)

	w := e.do(req{method: "GET", path: "/api/monitor/logs/cache.log", cookie: op})
	var ok struct{ Lines []string }
	json.Unmarshal(w.Body.Bytes(), &ok)
	if w.Code != 200 || len(ok.Lines) != 2 || ok.Lines[1] != "line two" {
		t.Fatalf("tail: %d %s", w.Code, w.Body.String())
	}

	for _, name := range []string{"..%2Fsecret.txt", "%2E%2E%2Fsecret.txt", "..%2F..%2Fetc%2Fpasswd", "%2Fetc%2Fpasswd", ".hidden"} {
		w := e.do(req{method: "GET", path: "/api/monitor/logs/" + name, cookie: op})
		if w.Code == 200 || strings.Contains(w.Body.String(), "TOPSECRET") {
			t.Errorf("%s must not be readable: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := e.do(req{method: "GET", path: "/api/monitor/logs/nothere.log", cookie: op}); w.Code != http.StatusNotFound {
		t.Errorf("a missing file: %d", w.Code)
	}
}

func TestAlertSettingsRoundTripWithoutLeakingSecrets(t *testing.T) {
	e := newEnv(t)
	admin := e.user("adam", auth.RoleAdmin)

	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	const hook = "https://hooks.example.com/services/SECRETPATH"
	put := func(body map[string]any) (int, string) {
		w := e.do(req{method: "PUT", path: "/api/monitor/alerts", body: body, cookie: admin})
		return w.Code, w.Body.String()
	}
	get := func() string {
		return e.do(req{method: "GET", path: "/api/monitor/alerts", cookie: admin}).Body.String()
	}

	code, body := put(map[string]any{
		"squid_down":   map[string]any{"enabled": true},
		"disk_full":    map[string]any{"enabled": true, "percent": 85},
		"denied_spike": map[string]any{"enabled": false, "threshold": 100, "window_minutes": 10},
		"error_spike":  map[string]any{"enabled": false, "threshold": 50, "window_minutes": 10},
		"telegram":     map[string]any{"enabled": true, "token": token, "chat_id": "-100123"},
		"webhook":      map[string]any{"enabled": true, "url": hook},
	})
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	for _, out := range []string{body, get()} {
		for _, secret := range []string{token, "SECRETPATH", "AAHdqTcv"} {
			if strings.Contains(out, secret) {
				t.Errorf("a secret leaked to the client: %q in %s", secret, out)
			}
		}
	}
	if !strings.Contains(get(), `"token_set":true`) {
		t.Errorf("the view must say a token is set: %s", get())
	}

	// Saving the form again with the secret blank keeps it.
	put(map[string]any{
		"squid_down": map[string]any{"enabled": true}, "disk_full": map[string]any{"enabled": true, "percent": 70},
		"denied_spike": map[string]any{"enabled": false, "threshold": 100, "window_minutes": 10},
		"error_spike":  map[string]any{"enabled": false, "threshold": 50, "window_minutes": 10},
		"telegram":     map[string]any{"enabled": true, "token": "", "chat_id": "-100123"},
		"webhook":      map[string]any{"enabled": true, "url": ""},
	})
	if !strings.Contains(get(), `"token_set":true`) || !strings.Contains(get(), `"percent":70`) {
		t.Errorf("blank secret must keep the stored one and other fields must change: %s", get())
	}

	// Bad input: 422 with the offending fields, nothing stored.
	code, body = put(map[string]any{"disk_full": map[string]any{"enabled": true, "percent": 5}})
	if code != 422 || !strings.Contains(body, "fields") {
		t.Errorf("invalid config: %d %s", code, body)
	}
	if w := e.do(req{method: "PUT", path: "/api/monitor/alerts", body: "not an object", cookie: admin}); w.Code != 400 {
		t.Errorf("malformed body: %d", w.Code)
	}

	// Test send with a channel that cannot be reached reports it, per channel.
	w := e.do(req{method: "POST", path: "/api/monitor/alerts/test", cookie: admin})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "telegram") {
		t.Errorf("test send: %d %s", w.Code, w.Body.String())
	}
}

func TestSecretsNeverAppearInTheAuditLog(t *testing.T) {
	e := newEnv(t)
	admin := e.user("adam", auth.RoleAdmin)
	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	saved := e.do(req{method: "PUT", path: "/api/monitor/alerts", cookie: admin, body: map[string]any{
		"squid_down":   map[string]any{"enabled": true},
		"disk_full":    map[string]any{"enabled": true, "percent": 85},
		"denied_spike": map[string]any{"enabled": false, "threshold": 100, "window_minutes": 10},
		"error_spike":  map[string]any{"enabled": false, "threshold": 50, "window_minutes": 10},
		"telegram":     map[string]any{"enabled": true, "token": token, "chat_id": "-100123"},
	}})
	if saved.Code != 200 {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	w := e.do(req{method: "GET", path: "/api/audit", cookie: admin})
	if !strings.Contains(w.Body.String(), "PUT /monitor/alerts") {
		t.Fatalf("the change should be audited: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "AAHdqTcv") {
		t.Errorf("the audit log must not store request bodies with secrets: %s", w.Body.String())
	}
}
