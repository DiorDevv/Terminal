package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"squidadmin/backend/internal/auth"
)

func (e *env) conf() string {
	b, _ := os.ReadFile(e.confPath)
	return string(b)
}

func (e *env) csv(cookie, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	hr := httptest.NewRequest("POST", path, strings.NewReader(body))
	hr.Header.Set("Content-Type", "text/csv")
	hr.Header.Set(CSRFHeader, CSRFValue)
	hr.AddCookie(&http.Cookie{Name: "squidadmin_session", Value: cookie})
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, hr)
	return w
}

func TestUserPolicyRoles(t *testing.T) {
	e := newEnv(t)
	viewer := e.user("vera", auth.RoleViewer)
	op := e.user("olga", auth.RoleOperator)

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/squid/proxy-users", nil},
		{"PUT", "/api/squid/proxy-users/alice", map[string]any{"note": "x"}},
		{"GET", "/api/squid/proxy-users-export", nil},
		{"GET", "/api/squid/user-groups", nil},
		{"POST", "/api/squid/user-groups", map[string]any{"name": "staff"}},
		{"GET", "/api/squid/limits", nil},
		{"POST", "/api/squid/limits", map[string]any{}},
	}
	for _, r := range routes {
		if w := e.do(req{method: r.method, path: r.path, body: r.body, cookie: viewer}); w.Code != 403 {
			t.Errorf("viewer %s %s: %d, want 403", r.method, r.path, w.Code)
		}
		if w := e.do(req{method: r.method, path: r.path, body: r.body}); w.Code != 401 {
			t.Errorf("no session %s %s: %d, want 401", r.method, r.path, w.Code)
		}
	}
	for _, path := range []string{"/api/squid/proxy-users", "/api/squid/user-groups", "/api/squid/limits"} {
		if w := e.do(req{method: "GET", path: path, cookie: op}); w.Code != 200 {
			t.Errorf("operator GET %s: %d", path, w.Code)
		}
	}
}

func TestDisablingAnAccountThroughTheAPI(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)

	w := e.do(req{method: "PUT", path: "/api/squid/proxy-users/alice", cookie: op, body: map[string]any{"disabled": true, "daily_quota_mb": 50, "note": "  trial  "}})
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out["reloaded"] != true {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(e.conf(), "acl sqa_up_blocked proxy_auth alice") {
		t.Errorf("squid.conf does not block alice:\n%s", e.conf())
	}

	var list struct {
		Users []struct {
			Username, Status, Note string
			DailyQuotaMB           int `json:"daily_quota_mb"`
		}
	}
	w = e.do(req{method: "GET", path: "/api/squid/proxy-users", cookie: op})
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Users) != 2 || list.Users[0].Username != "alice" || list.Users[0].Status != "disabled" ||
		list.Users[0].Note != "trial" || list.Users[0].DailyQuotaMB != 50 || list.Users[1].Status != "active" {
		t.Errorf("list: %s", w.Body.String())
	}

	// Changing only the note does not touch squid.conf, so squid is not reloaded.
	w = e.do(req{method: "PUT", path: "/api/squid/proxy-users/alice", cookie: op, body: map[string]any{"note": "again"}})
	out = nil
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out["reloaded"] != nil {
		t.Errorf("a change that leaves squid.conf alone must not reload: %s", w.Body.String())
	}

	// The change is in the audit trail.
	admin := e.user("adam", auth.RoleAdmin)
	if a := e.do(req{method: "GET", path: "/api/audit", cookie: admin}).Body.String(); !strings.Contains(a, "PUT /squid/proxy-users/:username") {
		t.Errorf("not audited: %s", a)
	}
}

func TestUserPolicyValidationAnswers422(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	before := e.conf()

	cases := []struct {
		name, method, path string
		body               any
	}{
		{"negative quota", "PUT", "/api/squid/proxy-users/alice", map[string]any{"daily_quota_mb": -1}},
		{"unknown account", "PUT", "/api/squid/proxy-users/ghost", map[string]any{"disabled": true}},
		{"bad group name", "POST", "/api/squid/user-groups", map[string]any{"name": "x"}},
		{"limit without a target", "POST", "/api/squid/limits", map[string]any{"name": "speed", "kind": "speed", "enabled": true, "spec": map[string]any{"scope": "shared", "rate_kbps": 10}}},
		{"limit with an unknown user", "POST", "/api/squid/limits", map[string]any{"name": "speed", "kind": "download", "enabled": true, "spec": map[string]any{"users": []string{"ghost"}, "max_mb": 5}}},
		{"missing group", "PUT", "/api/squid/user-groups/99/members", map[string]any{"members": []string{"alice"}}},
	}
	for _, c := range cases {
		w := e.do(req{method: c.method, path: c.path, body: c.body, cookie: op})
		if w.Code != 422 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if w := e.do(req{method: "PUT", path: "/api/squid/proxy-users/alice", cookie: op, body: "not json"}); w.Code != 400 {
		t.Errorf("malformed body: %d", w.Code)
	}
	if w := e.do(req{method: "DELETE", path: "/api/squid/limits/abc", cookie: op}); w.Code != 400 {
		t.Errorf("a bad id: %d", w.Code)
	}
	if e.conf() != before {
		t.Error("rejected requests must not touch squid.conf")
	}
}

func TestUserGroupsAndLimitsThroughTheAPI(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)

	var g struct{ Group struct{ ID int64 } }
	w := e.do(req{method: "POST", path: "/api/squid/user-groups", cookie: op, body: map[string]any{"name": "staff"}})
	json.Unmarshal(w.Body.Bytes(), &g)
	if w.Code != 200 || g.Group.ID == 0 {
		t.Fatalf("create group: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(req{method: "PUT", path: "/api/squid/user-groups/" + itoa(g.Group.ID) + "/members", cookie: op, body: map[string]any{"members": []string{"alice", "bob"}}}); w.Code != 200 {
		t.Fatalf("members: %d %s", w.Code, w.Body.String())
	}

	limit := map[string]any{"name": "staff speed", "kind": "speed", "enabled": true,
		"spec": map[string]any{"user_groups": []int64{g.Group.ID}, "scope": "each_user", "rate_kbps": 64}}
	var l struct{ Limit struct{ ID int64 } }
	w = e.do(req{method: "POST", path: "/api/squid/limits", cookie: op, body: limit})
	json.Unmarshal(w.Body.Bytes(), &l)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reloaded":true`) {
		t.Fatalf("create limit: %d %s", w.Code, w.Body.String())
	}
	for _, want := range []string{"delay_pools 1", "acl sqa_lim1_users proxy_auth alice bob", "delay_parameters 1 -1/-1 -1/-1 -1/-1 65536/131072"} {
		if !strings.Contains(e.conf(), want) {
			t.Errorf("squid.conf misses %q:\n%s", want, e.conf())
		}
	}

	limit["spec"].(map[string]any)["rate_kbps"] = 128
	if w := e.do(req{method: "PUT", path: "/api/squid/limits/" + itoa(l.Limit.ID), cookie: op, body: limit}); w.Code != 200 {
		t.Fatalf("update limit: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(e.conf(), "131072/262144") {
		t.Errorf("the update must reach squid.conf:\n%s", e.conf())
	}

	// Deleting a group takes its members out of the limit.
	if w := e.do(req{method: "DELETE", path: "/api/squid/user-groups/" + itoa(g.Group.ID), cookie: op}); w.Code != 200 {
		t.Fatalf("delete group: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(e.conf(), "sqa_lim1_users") {
		t.Errorf("a limit whose group is gone matches nobody:\n%s", e.conf())
	}
	if w := e.do(req{method: "DELETE", path: "/api/squid/limits/" + itoa(l.Limit.ID), cookie: op}); w.Code != 200 {
		t.Fatalf("delete limit: %d", w.Code)
	}
	if strings.Contains(e.conf(), "delay_pools") || strings.Contains(e.conf(), limitsMarker) {
		t.Errorf("everything of the limits block must be gone:\n%s", e.conf())
	}
}

const limitsMarker = "squidadmin: limits"

func TestDeletingAnAccountCleansUpItsSettings(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)

	e.do(req{method: "PUT", path: "/api/squid/proxy-users/alice", cookie: op, body: map[string]any{"disabled": true}})
	if !strings.Contains(e.conf(), "proxy_auth alice") {
		t.Fatal("setup: alice must be blocked")
	}
	if w := e.do(req{method: "DELETE", path: "/api/squid/users/alice", cookie: op}); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(e.conf(), "proxy_auth alice") {
		t.Errorf("a deleted account must not stay in the blocked list:\n%s", e.conf())
	}
	var n int
	e.conn.QueryRow(`SELECT COUNT(*) FROM proxy_users WHERE username = 'alice'`).Scan(&n)
	if n != 0 {
		t.Error("the account's settings row must be removed")
	}
}

func TestCSVThroughTheAPI(t *testing.T) {
	e := newEnv(t)
	op := e.user("olga", auth.RoleOperator)
	e.do(req{method: "PUT", path: "/api/squid/proxy-users/alice", cookie: op, body: map[string]any{"note": "=1+1"}})

	w := e.do(req{method: "GET", path: "/api/squid/proxy-users-export", cookie: op})
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export headers: %d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "username,status,") || !strings.Contains(body, "'=1+1") || strings.Contains(body, "fake-hash") {
		t.Errorf("export body:\n%s", body)
	}

	// Dry run: reports, writes nothing.
	before := e.conf()
	w = e.csv(op, "/api/squid/proxy-users-import?dry_run=1", "username,disabled\nalice,true\nbob,true\n")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"dry_run":true`) || !strings.Contains(w.Body.String(), `"updated":2`) {
		t.Fatalf("dry run: %d %s", w.Code, w.Body.String())
	}
	if e.conf() != before {
		t.Error("a dry run must not touch squid.conf")
	}

	// A bad row: 422, the report says why, nothing applied.
	w = e.csv(op, "/api/squid/proxy-users-import", "username,disabled\nalice,true\nbob,perhaps\n")
	if w.Code != 422 || !strings.Contains(w.Body.String(), "not true/false") || !strings.Contains(w.Body.String(), `"line":3`) {
		t.Fatalf("bad row: %d %s", w.Code, w.Body.String())
	}
	if e.conf() != before {
		t.Error("nothing may be applied when a row is wrong")
	}

	// A good file: applied and squid reloaded.
	w = e.csv(op, "/api/squid/proxy-users-import", "username,disabled\nalice,true\nbob,true\n")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reloaded":true`) || !strings.Contains(e.conf(), "proxy_auth alice bob") {
		t.Fatalf("import: %d %s\n%s", w.Code, w.Body.String(), e.conf())
	}

	// Files that are not CSV at all.
	if w := e.csv(op, "/api/squid/proxy-users-import", ""); w.Code != 422 {
		t.Errorf("empty file: %d", w.Code)
	}
	if w := e.csv(op, "/api/squid/proxy-users-import", strings.Repeat("x", 3<<20)); w.Code != 413 && w.Code != 422 {
		t.Errorf("a huge file must be refused, got %d", w.Code)
	}
}
