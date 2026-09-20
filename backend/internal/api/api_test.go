package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/api/handlers"
	"squidadmin/backend/internal/audit"
	"squidadmin/backend/internal/auth"
	"squidadmin/backend/internal/db"
	"squidadmin/backend/internal/monitor"
	"squidadmin/backend/internal/squid"
	"squidadmin/backend/internal/ws"
)

const (
	frontend = "http://localhost:5173"
	pw       = "a-long-enough-password"
)

const testConf = "http_access allow localhost\nhttp_access deny all\nhttp_port 3128\n"

type env struct {
	t        *testing.T
	router   *gin.Engine
	conn     *sql.DB
	auth     *auth.Service
	confPath string
	passwd   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "api.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	authSvc := auth.NewService(conn)
	store := audit.NewStore(conn)

	// "true" stands in for squid: every parse/reload succeeds.
	confPath := filepath.Join(dir, "squid.conf")
	if err := os.WriteFile(confPath, []byte(testConf), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := squid.NewManager(confPath, "true")
	blacklist := squid.NewBlacklistManager(filepath.Join(dir, "blocked.txt"), "blocked_sites", mgr)
	access := squid.NewAccessManager(conn, mgr, filepath.Join(dir, "lists"))
	blocklists := squid.NewBlocklistService(conn, mgr, access, filepath.Join(dir, "lists"), false)

	logDir := filepath.Join(dir, "log")
	os.MkdirAll(logDir, 0o755)
	ingest := monitor.NewIngestor(conn, filepath.Join(logDir, "access.log"))
	alerts := monitor.NewAlerts(conn, apiProbe{}, true)
	passwd := filepath.Join(dir, "passwd")
	os.WriteFile(passwd, []byte("alice:fake-hash\nbob:fake-hash\n"), 0o644)
	userMgr := squid.NewUserManager(passwd, "authenticated_users", "/usr/lib/squid/basic_ncsa_auth", fakeHtpasswd(t, dir), mgr)
	if err := userMgr.EnsureAuthDirectives(); err != nil {
		t.Fatalf("EnsureAuthDirectives: %v", err)
	}
	policy := squid.NewUserPolicy(conn, mgr, userMgr, nil)
	monitorH := handlers.NewMonitorHandler(monitor.NewStore(conn), ingest, alerts, mgr, logDir, "http://127.0.0.1:1/info", []string{dir})

	r := NewRouter(Deps{
		MonitorHandler:    monitorH,
		UserPolicyHandler: handlers.NewUserPolicyHandler(policy, mgr),
		UsersHandler:      handlers.NewUsersHandler(userMgr, mgr).WithPolicy(policy),
		SquidHandler:      handlers.NewSquidHandler(mgr, filepath.Join(logDir, "access.log"), ws.NewUpgrader([]string{frontend})),
		AuthSvc:           authSvc,
		Audit:             store,
		AuthHandler:       handlers.NewAuthHandler(authSvc, store, false),
		PanelUsersHandler: handlers.NewPanelUsersHandler(authSvc, store),
		AuditHandler:      handlers.NewAuditHandler(store),
		BlacklistHandler:  handlers.NewBlacklistHandler(blacklist, mgr),
		ServiceHandler:    handlers.NewServiceHandler(mgr),
		SettingsHandler:   handlers.NewSettingsHandler(mgr),
		AccessHandler:     handlers.NewAccessHandler(access, mgr, squid.PolicyEnv{BlacklistACL: "blocked_sites"}),
		BlocklistsHandler: handlers.NewBlocklistsHandler(blocklists, mgr),
		AllowedOrigins:    []string{frontend},
		TrustedProxies:    []string{"127.0.0.1"},
	})
	return &env{t: t, router: r, conn: conn, auth: authSvc, confPath: confPath, passwd: passwd}
}

type req struct {
	method, path string
	body         any
	cookie       string
	noCSRF       bool
	headers      map[string]string
	remote       string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *strings.Reader
	if r.body != nil {
		b, _ := json.Marshal(r.body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	hr := httptest.NewRequest(r.method, r.path, rd)
	hr.Header.Set("Content-Type", "application/json")
	if !r.noCSRF {
		hr.Header.Set(CSRFHeader, CSRFValue)
	}
	if r.cookie != "" {
		hr.AddCookie(&http.Cookie{Name: "squidadmin_session", Value: r.cookie})
	}
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	if r.remote != "" {
		hr.RemoteAddr = r.remote
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, hr)
	return w
}

// user creates an account with the given role, clears the forced password
// change, logs in and returns the session cookie value.
func (e *env) user(name string, role auth.Role) string {
	e.t.Helper()
	if _, err := e.auth.CreateUser(name, pw, role); err != nil {
		e.t.Fatalf("CreateUser(%s): %v", name, err)
	}
	e.conn.Exec(`UPDATE users SET must_change_password = 0 WHERE username = ?`, name)

	w := e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": name, "password": pw}})
	if w.Code != 200 {
		e.t.Fatalf("login %s: %d %s", name, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "squidadmin_session" {
			return c.Value
		}
	}
	e.t.Fatalf("login %s set no session cookie", name)
	return ""
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/auth/me", "/api/squid/blacklist", "/api/panel/users", "/api/audit"} {
		if w := e.do(req{method: "GET", path: path}); w.Code != 401 {
			t.Errorf("GET %s without a session: got %d, want 401", path, w.Code)
		}
	}
	if w := e.do(req{method: "GET", path: "/api/health"}); w.Code != 200 {
		t.Errorf("health must stay public, got %d", w.Code)
	}
}

func TestLoginCookieIsHardenedAndTokenNotInBody(t *testing.T) {
	e := newEnv(t)
	e.auth.CreateUser("ana", pw, auth.RoleAdmin)

	w := e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": "ana", "password": pw}})
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "squidadmin_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie must be SameSite=Strict, got %v", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie path = %q", cookie.Path)
	}
	if strings.Contains(w.Body.String(), cookie.Value) {
		t.Error("the session token must never appear in the response body")
	}

	// Behind TLS the Secure flag is set automatically.
	w = e.do(req{method: "POST", path: "/api/auth/login",
		body:    map[string]string{"username": "ana", "password": pw},
		headers: map[string]string{"X-Forwarded-For": "1.2.3.4"}})
	_ = w
	w2 := e.do(req{method: "POST", path: "/api/auth/login",
		body:    map[string]string{"username": "ana", "password": pw},
		headers: map[string]string{"X-Forwarded-Proto": "https"}})
	for _, c := range w2.Result().Cookies() {
		if c.Name == "squidadmin_session" && !c.Secure {
			t.Error("cookie must be Secure when the request arrived over HTTPS")
		}
	}
}

func TestSessionCookieAuthenticatesButBearerDoesNot(t *testing.T) {
	e := newEnv(t)
	tok := e.user("ana", auth.RoleAdmin)

	w := e.do(req{method: "GET", path: "/api/auth/me", cookie: tok})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"username":"ana"`) {
		t.Fatalf("/me with cookie: %d %s", w.Code, w.Body.String())
	}

	w = e.do(req{method: "GET", path: "/api/auth/me", headers: map[string]string{"Authorization": "Bearer " + tok}})
	if w.Code != 401 {
		t.Errorf("a token in the Authorization header must not authenticate, got %d", w.Code)
	}
	// And never from the URL — that's how tokens used to leak into logs.
	w = e.do(req{method: "GET", path: "/api/auth/me?token=" + tok})
	if w.Code != 401 {
		t.Errorf("a token in the query string must not authenticate, got %d", w.Code)
	}
}

func TestCSRFProtection(t *testing.T) {
	e := newEnv(t)
	tok := e.user("ana", auth.RoleAdmin)

	body := map[string]string{"domain": "example.com"}

	if w := e.do(req{method: "POST", path: "/api/squid/blacklist", body: body, cookie: tok, noCSRF: true}); w.Code != 403 {
		t.Errorf("state-changing request without the CSRF header: got %d, want 403", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/api/squid/blacklist", body: body, cookie: tok,
		headers: map[string]string{"Origin": "https://evil.example"}}); w.Code != 403 {
		t.Errorf("request from a foreign Origin: got %d, want 403", w.Code)
	}
	if w := e.do(req{method: "POST", path: "/api/squid/blacklist", body: body, cookie: tok,
		headers: map[string]string{"Origin": frontend}}); w.Code != 200 {
		t.Errorf("request from the allowed Origin: got %d, want 200", w.Code)
	}
	// Login is state-changing too.
	if w := e.do(req{method: "POST", path: "/api/auth/login", noCSRF: true,
		body: map[string]string{"username": "ana", "password": pw}}); w.Code != 403 {
		t.Errorf("login without the CSRF header: got %d, want 403", w.Code)
	}
	// Reads never need it.
	if w := e.do(req{method: "GET", path: "/api/auth/me", cookie: tok, noCSRF: true}); w.Code != 200 {
		t.Errorf("GET without the CSRF header: got %d, want 200", w.Code)
	}
}

func TestCORSOnlyForAllowedOrigins(t *testing.T) {
	e := newEnv(t)

	w := e.do(req{method: "OPTIONS", path: "/api/squid/blacklist/x", noCSRF: true,
		headers: map[string]string{"Origin": frontend, "Access-Control-Request-Method": "DELETE"}})
	if w.Code != 204 {
		t.Fatalf("preflight: %d", w.Code)
	}
	h := w.Header()
	if h.Get("Access-Control-Allow-Origin") != frontend || h.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("allowed origin must be echoed with credentials, got %v", h)
	}
	if !strings.Contains(h.Get("Access-Control-Allow-Methods"), "DELETE") {
		t.Errorf("DELETE must be allowed, got %q", h.Get("Access-Control-Allow-Methods"))
	}

	w = e.do(req{method: "OPTIONS", path: "/api/squid/blacklist/x", noCSRF: true,
		headers: map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "DELETE"}})
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a foreign origin must get no CORS headers, got %q", got)
	}
}

func TestRoleBasedAccess(t *testing.T) {
	e := newEnv(t)
	viewer := e.user("vera", auth.RoleViewer)
	operator := e.user("olga", auth.RoleOperator)
	admin := e.user("adam", auth.RoleAdmin)

	type c struct {
		who    string
		cookie string
		method string
		path   string
		body   any
		want   int
	}
	dom := map[string]string{"domain": "example.org"}
	cases := []c{
		// viewer: read-only overview
		{"viewer", viewer, "GET", "/api/squid/blacklist", nil, 200},
		{"viewer", viewer, "POST", "/api/squid/blacklist", dom, 403},
		{"viewer", viewer, "GET", "/api/squid/logs/stream", nil, 403},
		{"viewer", viewer, "GET", "/api/squid/config", nil, 403},
		{"viewer", viewer, "GET", "/api/panel/users", nil, 403},
		{"viewer", viewer, "GET", "/api/audit", nil, 403},
		{"viewer", viewer, "POST", "/api/squid/service/start", nil, 403},
		// operator: everyday management, but not the admin surface
		{"operator", operator, "POST", "/api/squid/blacklist", dom, 200},
		{"operator", operator, "GET", "/api/squid/config", nil, 403},
		{"operator", operator, "PUT", "/api/squid/config", map[string]string{"content": "x"}, 403},
		{"operator", operator, "GET", "/api/squid/history", nil, 403},
		{"operator", operator, "GET", "/api/panel/users", nil, 403},
		{"operator", operator, "POST", "/api/panel/users", map[string]string{"username": "zed", "password": pw, "role": "admin"}, 403},
		{"operator", operator, "GET", "/api/audit", nil, 403},
		// operators may start/reload squid but not take it down
		{"operator", operator, "POST", "/api/squid/service/stop", nil, 403},
		{"operator", operator, "POST", "/api/squid/service/restart", nil, 403},
		{"operator", operator, "POST", "/api/squid/service/disable", nil, 403},
		// settings: operators may look, only admins may change
		{"viewer", viewer, "GET", "/api/squid/settings", nil, 403},
		{"operator", operator, "GET", "/api/squid/settings", nil, 200},
		{"operator", operator, "PUT", "/api/squid/settings", map[string]any{"values": map[string][]string{"via": {"off"}}}, 403},
		{"operator", operator, "POST", "/api/squid/settings/restart", nil, 403},
		{"viewer", viewer, "PUT", "/api/squid/settings", map[string]any{"values": map[string][]string{"via": {"off"}}}, 403},
		// access rules: everyone may look, operators change, viewers cannot
		{"viewer", viewer, "GET", "/api/squid/access/rules", nil, 200},
		{"viewer", viewer, "GET", "/api/squid/access/acls", nil, 200},
		{"viewer", viewer, "GET", "/api/squid/access/policy", nil, 200},
		{"viewer", viewer, "GET", "/api/squid/blocklists", nil, 200},
		{"viewer", viewer, "POST", "/api/squid/access/acls", map[string]any{"name": "abc", "type": "port", "values": []string{"80"}}, 403},
		{"viewer", viewer, "POST", "/api/squid/access/test", map[string]any{"url": "http://a.example/"}, 403},
		{"viewer", viewer, "PUT", "/api/squid/access/order", map[string]any{"ids": []int{1}}, 403},
		{"viewer", viewer, "POST", "/api/squid/blocklists", map[string]any{"name": "abc", "url": "http://a.example/x"}, 403},
		{"operator", operator, "POST", "/api/squid/access/acls", map[string]any{"name": "opacl", "type": "port", "values": []string{"8080"}}, 200},
		{"operator", operator, "POST", "/api/squid/access/test", map[string]any{"url": "http://a.example/", "src_ip": "10.0.0.1"}, 200},
		// admin
		{"admin", admin, "GET", "/api/squid/settings", nil, 200},
		{"admin", admin, "GET", "/api/panel/users", nil, 200},
		{"admin", admin, "GET", "/api/audit", nil, 200},
		// every role may use the self-service endpoints
		{"viewer", viewer, "GET", "/api/auth/me", nil, 200},
		{"viewer", viewer, "GET", "/api/auth/sessions", nil, 200},
	}
	for _, tc := range cases {
		w := e.do(req{method: tc.method, path: tc.path, body: tc.body, cookie: tc.cookie})
		if w.Code != tc.want {
			t.Errorf("%s %s %s: got %d, want %d (%s)", tc.who, tc.method, tc.path, w.Code, tc.want, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestPasswordChangeRequiredBlocksEverythingElse(t *testing.T) {
	e := newEnv(t)
	if _, err := e.auth.CreateUser("newbie", pw, auth.RoleOperator); err != nil {
		t.Fatal(err)
	}
	w := e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": "newbie", "password": pw}})
	var tok string
	for _, c := range w.Result().Cookies() {
		tok = c.Value
	}

	// The user can see who they are (so the UI knows to show the change form)...
	if w := e.do(req{method: "GET", path: "/api/auth/me", cookie: tok}); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"must_change_password":true`) {
		t.Fatalf("/me: %d %s", w.Code, w.Body.String())
	}
	// ...but nothing else works until they change it.
	for _, p := range []string{"/api/squid/blacklist", "/api/auth/sessions"} {
		w := e.do(req{method: "GET", path: p, cookie: tok})
		if w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") {
			t.Errorf("GET %s while a change is required: %d %s", p, w.Code, w.Body.String())
		}
	}

	// A wrong current password is a 422 — a 401 would make the UI sign out.
	w = e.do(req{method: "PUT", path: "/api/auth/password", cookie: tok,
		body: map[string]string{"old_password": "nope-nope-nope", "new_password": "a-brand-new-password"}})
	if w.Code != 422 {
		t.Errorf("wrong current password: got %d, want 422", w.Code)
	}
	w = e.do(req{method: "PUT", path: "/api/auth/password", cookie: tok,
		body: map[string]string{"old_password": pw, "new_password": "short"}})
	if w.Code != 422 {
		t.Errorf("weak new password: got %d, want 422", w.Code)
	}

	w = e.do(req{method: "PUT", path: "/api/auth/password", cookie: tok,
		body: map[string]string{"old_password": pw, "new_password": "a-brand-new-password"}})
	if w.Code != 200 {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(req{method: "GET", path: "/api/squid/blacklist", cookie: tok}); w.Code != 200 {
		t.Errorf("after changing the password the API must open up, got %d", w.Code)
	}
}

func TestLogoutKillsTheSession(t *testing.T) {
	e := newEnv(t)
	tok := e.user("ana", auth.RoleAdmin)

	if w := e.do(req{method: "POST", path: "/api/auth/logout", cookie: tok}); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := e.do(req{method: "GET", path: "/api/auth/me", cookie: tok}); w.Code != 401 {
		t.Errorf("the token must be dead after logout, got %d", w.Code)
	}
}

func TestAuditLog(t *testing.T) {
	e := newEnv(t)
	operator := e.user("olga", auth.RoleOperator)
	admin := e.user("adam", auth.RoleAdmin)

	e.do(req{method: "POST", path: "/api/auth/login", body: map[string]string{"username": "olga", "password": "wrong-password-1"}})
	e.do(req{method: "POST", path: "/api/squid/blacklist", cookie: operator, body: map[string]string{"domain": "audited.example"}})
	e.do(req{method: "PUT", path: "/api/squid/config", cookie: operator, body: map[string]string{"content": "x"}}) // 403
	e.do(req{method: "GET", path: "/api/squid/blacklist", cookie: operator})                                       // reads aren't logged

	w := e.do(req{method: "GET", path: "/api/audit", cookie: admin})
	if w.Code != 200 {
		t.Fatalf("audit: %d", w.Code)
	}
	var out struct{ Entries []audit.Entry }
	json.Unmarshal(w.Body.Bytes(), &out)

	find := func(action string, status int) *audit.Entry {
		for i := range out.Entries {
			if out.Entries[i].Action == action && out.Entries[i].Status == status {
				return &out.Entries[i]
			}
		}
		return nil
	}

	if e := find("LOGIN", 401); e == nil || e.Username != "olga" {
		t.Errorf("failed login must be audited, entries: %+v", out.Entries)
	}
	if e := find("LOGIN", 200); e == nil {
		t.Errorf("successful login must be audited")
	}
	if e := find("POST /squid/blacklist", 200); e == nil || e.Username != "olga" {
		t.Errorf("blacklist add must be audited with the user, entries: %+v", out.Entries)
	}
	if e := find("PUT /squid/config", 403); e == nil || e.Username != "olga" {
		t.Errorf("a refused (403) attempt must be audited too, entries: %+v", out.Entries)
	}
	for _, en := range out.Entries {
		if strings.HasPrefix(en.Action, "GET") {
			t.Errorf("reads must not be audited: %+v", en)
		}
		if strings.Contains(en.Detail+en.Target, "wrong-password-1") || strings.Contains(en.Detail+en.Target, pw) {
			t.Errorf("passwords must never be stored in the audit log: %+v", en)
		}
	}
}

// gin trusts X-Forwarded-For from anyone by default, which lets a client pick
// the IP that lands in the audit log and the rate limiter.
func TestClientIPCannotBeSpoofed(t *testing.T) {
	e := newEnv(t)
	adminTok := e.user("adam", auth.RoleAdmin)

	e.do(req{method: "POST", path: "/api/auth/login",
		body:    map[string]string{"username": "adam", "password": "wrong-password-1"},
		remote:  "203.0.113.9:4444",
		headers: map[string]string{"X-Forwarded-For": "10.10.10.10"}})

	w := e.do(req{method: "GET", path: "/api/audit", cookie: adminTok})
	var out struct{ Entries []audit.Entry }
	json.Unmarshal(w.Body.Bytes(), &out)
	for _, en := range out.Entries {
		if en.Action == "LOGIN" && en.Status == 401 {
			if en.IP != "203.0.113.9" {
				t.Errorf("audited IP = %q, want the real peer 203.0.113.9 (X-Forwarded-For must be ignored from untrusted peers)", en.IP)
			}
			return
		}
	}
	t.Fatal("failed login not found in audit log")
}

func TestPanelUserManagementGuards(t *testing.T) {
	e := newEnv(t)
	admin := e.user("adam", auth.RoleAdmin)
	users, _ := e.auth.ListUsers()
	self := users[0].ID

	// Create
	w := e.do(req{method: "POST", path: "/api/panel/users", cookie: admin,
		body: map[string]string{"username": "newop", "password": pw, "role": "operator"}})
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	w = e.do(req{method: "POST", path: "/api/panel/users", cookie: admin,
		body: map[string]string{"username": "newop", "password": pw, "role": "operator"}})
	if w.Code != 409 {
		t.Errorf("duplicate username: got %d, want 409", w.Code)
	}
	w = e.do(req{method: "POST", path: "/api/panel/users", cookie: admin,
		body: map[string]string{"username": "weakling", "password": "123", "role": "viewer"}})
	if w.Code != 422 {
		t.Errorf("weak password: got %d, want 422", w.Code)
	}

	// Self-protection
	selfPath := "/api/panel/users/" + itoa(self)
	if w := e.do(req{method: "DELETE", path: selfPath, cookie: admin}); w.Code != 409 {
		t.Errorf("deleting yourself: got %d, want 409", w.Code)
	}
	if w := e.do(req{method: "PUT", path: selfPath, cookie: admin, body: map[string]any{"disabled": true}}); w.Code != 409 {
		t.Errorf("disabling yourself: got %d, want 409", w.Code)
	}
	// Last-admin guard (demoting yourself while you are the only admin).
	if w := e.do(req{method: "PUT", path: selfPath, cookie: admin, body: map[string]string{"role": "viewer"}}); w.Code != 409 {
		t.Errorf("demoting the last admin: got %d, want 409", w.Code)
	}

	if w := e.do(req{method: "PUT", path: "/api/panel/users/999", cookie: admin, body: map[string]string{"role": "viewer"}}); w.Code != 404 {
		t.Errorf("unknown user: got %d, want 404", w.Code)
	}
	if w := e.do(req{method: "PUT", path: selfPath, cookie: admin, body: map[string]string{"role": "root"}}); w.Code != 422 {
		t.Errorf("invalid role: got %d, want 422", w.Code)
	}
}

func TestAPIRateLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := NewAPIRateLimiter(3, time.Hour)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Stand-in for SessionAuth: two different users.
		id := int64(1)
		if c.GetHeader("X-User") == "2" {
			id = 2
		}
		c.Set("auth.user", auth.User{ID: id})
		c.Set("auth.token", "t")
	})
	r.Use(l.Middleware())
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	hit := func(user string) int {
		w := httptest.NewRecorder()
		rq := httptest.NewRequest("GET", "/x", nil)
		rq.Header.Set("X-User", user)
		r.ServeHTTP(w, rq)
		return w.Code
	}

	for i := 1; i <= 3; i++ {
		if c := hit("1"); c != 200 {
			t.Fatalf("request %d: %d", i, c)
		}
	}
	if c := hit("1"); c != 429 {
		t.Errorf("4th request must be rate limited, got %d", c)
	}
	if c := hit("2"); c != 200 {
		t.Errorf("limits are per user; user 2 got %d", c)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestSettingsAPI(t *testing.T) {
	e := newEnv(t)
	admin := e.user("adam", auth.RoleAdmin)
	put := func(path string, body any) (int, map[string]any) {
		w := e.do(req{method: "PUT", path: path, cookie: admin, body: body})
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	// GET returns the schema and the current values.
	w := e.do(req{method: "GET", path: "/api/squid/settings", cookie: admin})
	var got struct {
		Settings []struct {
			Key    string   `json:"key"`
			Values []string `json:"values"`
			Source string   `json:"source"`
		} `json:"settings"`
		Groups []string `json:"groups"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Groups) != 5 || len(got.Settings) < 10 {
		t.Fatalf("unexpected payload: %s", w.Body.String())
	}
	for _, s := range got.Settings {
		if s.Values == nil {
			t.Errorf("%s: values must be [] not null", s.Key)
		}
		if s.Key == "http_port" && (s.Source != "squid.conf" || len(s.Values) != 1 || s.Values[0] != "3128") {
			t.Errorf("http_port = %+v", s)
		}
	}

	// Validation errors come back per field, and nothing is written.
	before, _ := os.ReadFile(e.confPath)
	code, body := put("/api/squid/settings", map[string]any{"values": map[string][]string{"cache_mem": {"lots"}, "via": {"maybe"}, "connect_timeout": {"2 minutes"}}})
	fields, _ := body["fields"].(map[string]any)
	if code != 422 || len(fields) != 2 || fields["cache_mem"] == nil || fields["via"] == nil {
		t.Fatalf("bad values: %d %v", code, body)
	}
	if after, _ := os.ReadFile(e.confPath); string(after) != string(before) {
		t.Fatal("a rejected update must not touch squid.conf")
	}
	if code, _ := put("/api/squid/settings", map[string]any{}); code != 400 {
		t.Errorf("an empty update is a 400, got %d", code)
	}

	// Dry run: reports the change, writes nothing.
	code, body = put("/api/squid/settings?dry_run=1", map[string]any{"values": map[string][]string{"via": {"off"}}})
	changes, _ := body["changes"].([]any)
	if code != 200 || body["dry_run"] != true || len(changes) != 1 {
		t.Fatalf("dry run: %d %v", code, body)
	}
	if after, _ := os.ReadFile(e.confPath); string(after) != string(before) {
		t.Fatal("a dry run must not touch squid.conf")
	}

	// A reload-only change is applied and reloaded.
	code, body = put("/api/squid/settings", map[string]any{"values": map[string][]string{"via": {"off"}, "cache_mem": {"64mb"}}})
	if code != 200 || body["reloaded"] != true || body["restart_required"] != false {
		t.Fatalf("apply: %d %v", code, body)
	}
	if conf, _ := os.ReadFile(e.confPath); !strings.Contains(string(conf), "via off") || !strings.Contains(string(conf), "cache_mem 64 MB") {
		t.Fatalf("not written:\n%s", conf)
	}

	// A cache_dir change is saved but deliberately NOT reloaded: it needs a restart.
	code, body = put("/api/squid/settings", map[string]any{"values": map[string][]string{"cache_dir": {"ufs /var/spool/squid/x 100 16 256"}}})
	if code != 200 || body["restart_required"] != true {
		t.Fatalf("cache_dir: %d %v", code, body)
	}
	if _, attempted := body["reloaded"]; attempted {
		t.Errorf("a restart-only change must not be reloaded, got %v", body)
	}
	w = e.do(req{method: "GET", path: "/api/squid/service", cookie: admin})
	if !strings.Contains(w.Body.String(), `"restart_pending":true`) {
		t.Errorf("the service info must flag the pending restart: %s", w.Body.String())
	}

	// Every change shows up in the audit log.
	w = e.do(req{method: "GET", path: "/api/audit", cookie: admin})
	if !strings.Contains(w.Body.String(), "PUT /squid/settings") {
		t.Errorf("settings changes must be audited: %s", w.Body.String())
	}
}

func TestAccessAPIEndToEnd(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	call := func(method, path string, body any) (int, map[string]any) {
		w := e.do(req{method: method, path: path, cookie: op, body: body})
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	id := func(m map[string]any, key string) int {
		return int(m[key].(map[string]any)["id"].(float64))
	}

	// ACLs: created, validated, listed.
	code, out := call("POST", "/api/squid/access/acls", map[string]any{"name": "social", "type": "dstdomain", "values": []string{"facebook.com", "tiktok.com"}})
	if code != 200 {
		t.Fatalf("create acl: %d %v", code, out)
	}
	social := id(out, "acl")
	if code, _ := call("POST", "/api/squid/access/acls", map[string]any{"name": "bad", "type": "dstdomain", "values": []string{"nodots"}}); code != 422 {
		t.Errorf("an invalid value must be 422, got %d", code)
	}
	_, out = call("POST", "/api/squid/access/acls", map[string]any{"name": "it_dept", "type": "src", "values": []string{"10.0.0.5"}})
	it := id(out, "acl")

	// Rules: created in order, written to squid.conf, reordered, evaluated.
	code, out = call("POST", "/api/squid/access/rules", map[string]any{"action": "deny", "comment": "no social",
		"terms": []map[string]any{{"acl_id": social}, {"acl_id": it, "negate": true}}})
	if code != 200 || out["reloaded"] != true {
		t.Fatalf("create rule: %d %v", code, out)
	}
	r1 := id(out, "rule")
	_, out = call("POST", "/api/squid/access/rules", map[string]any{"action": "allow", "terms": []map[string]any{{"acl_id": it}}})
	r2 := id(out, "rule")

	conf, _ := os.ReadFile(e.confPath)
	if !strings.Contains(string(conf), "http_access deny sqa_social !sqa_it_dept") {
		t.Fatalf("rule not written:\n%s", conf)
	}

	// An ACL used by a rule cannot be deleted (409), and an unknown id is 404.
	if code, _ := call("DELETE", "/api/squid/access/acls/"+itoa(int64(social)), nil); code != 409 {
		t.Errorf("deleting an ACL in use: got %d, want 409", code)
	}
	if code, _ := call("PUT", "/api/squid/access/rules/999", map[string]any{"enabled": false}); code != 404 {
		t.Errorf("unknown rule: got %d, want 404", code)
	}

	if code, out := call("PUT", "/api/squid/access/order", map[string]any{"ids": []int{r2, r1}}); code != 200 || out["reloaded"] != true {
		t.Fatalf("reorder: %d %v", code, out)
	}
	if code, _ := call("PUT", "/api/squid/access/order", map[string]any{"ids": []int{r2}}); code != 422 {
		t.Errorf("an incomplete order must be refused, got %d", code)
	}

	// The tester sees the panel's own rules (the test config has no stock ACLs
	// beyond what squid.conf defines, so use a request that reaches them).
	code, out = call("POST", "/api/squid/access/test", map[string]any{"url": "http://www.facebook.com/", "src_ip": "10.0.0.9"})
	if code != 200 {
		t.Fatalf("test: %d %v", code, out)
	}
	if _, ok := out["decision"]; !ok || out["trace"] == nil {
		t.Errorf("verdict: %v", out)
	}
	if code, _ := call("POST", "/api/squid/access/test", map[string]any{"url": ""}); code != 422 {
		t.Errorf("a request without a URL is 422, got %d", code)
	}
	if code, _ := call("POST", "/api/squid/access/test", map[string]any{"url": "http://a.example/", "time": "yesterday"}); code != 400 {
		t.Errorf("a malformed time is 400, got %d", code)
	}

	// The effective policy lists the rules in evaluation order.
	code, out = call("GET", "/api/squid/access/policy", nil)
	rules, _ := out["rules"].([]any)
	if code != 200 || len(rules) < 2 {
		t.Fatalf("policy: %d %v", code, out)
	}

	// Everything is audited.
	admin := e.user("adam", auth.RoleAdmin)
	w := e.do(req{method: "GET", path: "/api/audit", cookie: admin})
	if !strings.Contains(w.Body.String(), "POST /squid/access/rules") || !strings.Contains(w.Body.String(), "PUT /squid/access/order") {
		t.Errorf("rule changes must be audited: %s", w.Body.String())
	}
}

// apiProbe is a quiet system for the alert engine.
type apiProbe struct{}

func (apiProbe) SquidRunning() bool                   { return true }
func (apiProbe) Disk() (string, float64, error)       { return "/", 10, nil }
func (apiProbe) Recent(time.Duration) (int, int, int) { return 0, 0, 0 }

// fakeHtpasswd stands in for htpasswd: it keeps "user:hash" lines.
func fakeHtpasswd(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "htpasswd")
	script := `#!/bin/sh
if [ "$1" = "-D" ]; then
  grep -v "^$3:" "$2" > "$2.tmp"; mv "$2.tmp" "$2"; exit 0
fi
shift
if [ "$1" = "-c" ]; then shift; : > "$1"; fi
file=$1; user=$2; read pw
grep -v "^$user:" "$file" > "$file.tmp" 2>/dev/null
echo "$user:fake-hash-of-$pw" >> "$file.tmp"
mv "$file.tmp" "$file"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
