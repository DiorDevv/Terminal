package api

import (
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"squidadmin/backend/internal/auth"
)

// The API documentation is checked against the real router, so it cannot drift:
// every route is documented, nothing documented is missing, and no operation is
// more open than the role the document promises.

type spec struct {
	Paths map[string]map[string]struct {
		Summary string `yaml:"summary"`
		Role    string `yaml:"x-role"`
	} `yaml:"paths"`
}

type op struct{ method, path, role string }

func loadSpec(t *testing.T) []op {
	t.Helper()
	data, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatalf("read docs/openapi.yaml: %v", err)
	}
	var s spec
	if err := yaml.Unmarshal(data, &s); err != nil {
		t.Fatalf("docs/openapi.yaml is not valid YAML: %v", err)
	}
	var ops []op
	for path, methods := range s.Paths {
		for method, o := range methods {
			method = strings.ToUpper(method)
			switch method {
			case "GET", "POST", "PUT", "DELETE":
			default:
				continue
			}
			if o.Summary == "" {
				t.Errorf("%s %s has no summary", method, path)
			}
			ops = append(ops, op{method, "/api" + braceToColon(path), o.Role})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].path+ops[i].method < ops[j].path+ops[j].method })
	return ops
}

// braceToColon turns /squid/users/{username} into gin's /squid/users/:username.
func braceToColon(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '{':
			b.WriteByte(':')
		case '}':
		default:
			b.WriteByte(p[i])
		}
	}
	return b.String()
}

func TestEveryRouteIsDocumentedAndEveryDocumentedOperationExists(t *testing.T) {
	e := newEnv(t)
	routes := map[string]bool{}
	for _, r := range e.router.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	documented := map[string]bool{}
	for _, o := range loadSpec(t) {
		documented[o.method+" "+o.path] = true
	}
	for r := range routes {
		if !documented[r] {
			t.Errorf("route %s is not in docs/openapi.yaml", r)
		}
	}
	for d := range documented {
		if !routes[d] {
			t.Errorf("docs/openapi.yaml documents %s, which the router does not serve", d)
		}
	}
}

// pathParams are harmless values for the {placeholders}: the handlers refuse or
// ignore them, and the role check happens before any handler runs.
var pathParams = map[string]string{":id": "1", ":username": "alice", ":name": "cache.log", ":action": "start", ":domain": "example.com"}

func fill(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if v, ok := pathParams[s]; ok {
			segs[i] = v
		}
	}
	return strings.Join(segs, "/")
}

var roleRank = map[string]int{"viewer": 1, "operator": 2, "admin": 3}

func TestNoOperationIsMoreOpenThanItsDocumentedRole(t *testing.T) {
	e := newEnv(t)
	sessions := map[string]string{
		"viewer":   e.user("vera", auth.RoleViewer),
		"operator": e.user("olga", auth.RoleOperator),
		"admin":    e.user("adam", auth.RoleAdmin),
	}

	valid := map[string]bool{"public": true, "any": true, "viewer": true, "operator": true, "admin": true}
	checked := 0
	for _, o := range loadSpec(t) {
		if !valid[o.role] {
			t.Errorf("%s %s has x-role %q (want public, any, viewer, operator or admin)", o.method, o.path, o.role)
			continue
		}
		if o.role == "public" {
			continue
		}
		path := fill(o.path)
		var body any
		if o.method == "POST" || o.method == "PUT" {
			body = map[string]any{}
		}

		// Nobody signed in: always refused.
		if w := e.do(req{method: o.method, path: path, body: body}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session answered %d, want 401", o.method, o.path, w.Code)
		}
		if o.role == "any" {
			continue
		}
		// A role below the documented one is refused. (The allowed direction is
		// covered by the feature tests; running every mutating endpoint here
		// would restart squid and delete things.)
		for name, cookie := range sessions {
			if roleRank[name] >= roleRank[o.role] {
				continue
			}
			if w := e.do(req{method: o.method, path: path, body: body, cookie: cookie}); w.Code != http.StatusForbidden {
				t.Errorf("%s %s is documented as %s but a %s got %d instead of 403", o.method, o.path, o.role, name, w.Code)
			}
			checked++
		}
	}
	if checked < 60 { // 79 at the time of writing: one per operator op, two per admin op
		t.Errorf("only %d role checks ran; the spec was probably not read", checked)
	}
}
