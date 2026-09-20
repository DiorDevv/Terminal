package squid

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squidadmin/backend/internal/db"
)

func newAccessDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

const accessConf = `acl localnet src 10.0.0.0/8
http_access deny !Safe_ports
http_access deny blocked_sites
http_access allow localhost
http_access allow localnet
http_access deny all
`

func newAccess(t *testing.T) (*AccessManager, *Manager, string, string) {
	t.Helper()
	bin, dir := writeFakeSquid(t)
	path := newConf(t, accessConf)
	mgr := NewManager(path, bin)
	mgr.SetHistory(newTestHistory(t))
	return NewAccessManager(newAccessDB(t), mgr, "/etc/squid/blocklists"), mgr, path, dir
}

func mustACL(t *testing.T, m *AccessManager, name, typ string, values ...string) ACLObject {
	t.Helper()
	a, err := m.CreateACL(name, typ, values, true, "")
	if err != nil {
		t.Fatalf("CreateACL(%s): %v", name, err)
	}
	return a
}

func term(a ACLObject, negate bool) RuleTerm { return RuleTerm{ACLID: a.ID, Negate: negate} }

func mustRule(t *testing.T, m *AccessManager, action string, comment string, terms ...RuleTerm) AccessRule {
	t.Helper()
	r, err := m.CreateRule(action, terms, comment, 0, true)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return r
}

func readConf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func idx(s, sub string) int { return strings.Index(s, sub) }

func TestNormalizeACLValues(t *testing.T) {
	good := []struct {
		typ  string
		in   []string
		want string
	}{
		{"src", []string{"10.0.0.0/8", "192.168.1.5", "10.1.1.1-10.1.1.9"}, "10.0.0.0/8,192.168.1.5,10.1.1.1-10.1.1.9"},
		{"dst", []string{"::1", "fe80::/10"}, "::1,fe80::/10"},
		{"dstdomain", []string{"YouTube.com", "https://www.Facebook.com/x", ".tiktok.com"}, ".youtube.com,.facebook.com,.tiktok.com"},
		{"url_regex", []string{"casino", "\\.exe$"}, "casino,\\.exe$"},
		{"port", []string{"80", "8000-8100", "80"}, "80,8000-8100"},
		{"method", []string{"post", "Connect"}, "POST,CONNECT"},
		{"time", []string{"mtwhf   09:00-18:00", "SA 10:00-14:00"}, "MTWHF 09:00-18:00,SA 10:00-14:00"},
		{"proxy_auth", []string{"REQUIRED"}, "REQUIRED"},
		{"proxy_auth", []string{"alice", "bob.smith"}, "alice,bob.smith"},
	}
	for _, c := range good {
		got, err := normalizeACLValues(c.typ, c.in)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%s %v -> %v, %v; want %s", c.typ, c.in, got, err, c.want)
		}
	}

	bad := []struct {
		typ string
		in  []string
	}{
		{"src", []string{"not-an-ip"}}, {"src", []string{"10.0.0.0/99"}}, {"src", []string{"10.0.0.1-::1"}},
		{"dstdomain", []string{"nodots"}}, {"dstdomain", []string{"bad_host.com"}},
		{"url_regex", []string{"(?i)x"}}, {"url_regex", []string{"a b"}}, {"url_regex", []string{"[unclosed"}},
		{"url_regex", []string{`\d+`}}, {"url_regex", []string{"it's"}},
		{"port", []string{"0"}}, {"port", []string{"70000"}}, {"port", []string{"90-80"}}, {"port", []string{"http"}},
		{"method", []string{"FETCH"}},
		{"time", []string{"MTWHF 18:00-09:00"}}, {"time", []string{"MMT 09:00-10:00"}}, {"time", []string{"X 09:00-10:00"}}, {"time", []string{"M 25:00-26:00"}},
		{"proxy_auth", []string{"REQUIRED", "alice"}}, {"proxy_auth", []string{"bad name"}},
		{"blocklist", []string{"1"}}, {"nonsense", []string{"x"}},
		{"src", []string{}}, {"src", []string{"  "}},
	}
	for _, c := range bad {
		if got, err := normalizeACLValues(c.typ, c.in); err == nil {
			t.Errorf("%s %v -> %v, want an error", c.typ, c.in, got)
		}
	}
}

func TestRenderACL(t *testing.T) {
	path := func(id string) string { return "/lists/" + id + ".txt" }

	got := renderACL(ACLObject{Name: "social", Type: "dstdomain", Values: []string{".a.com", ".b.com"}}, path)
	if strings.Join(got, "|") != "acl sqa_social dstdomain .a.com .b.com" {
		t.Errorf("dstdomain: %v", got)
	}
	got = renderACL(ACLObject{Name: "kw", Type: "url_regex", CaseInsensitive: true, Values: []string{"casino"}}, path)
	if strings.Join(got, "|") != "acl sqa_kw url_regex -i casino" {
		t.Errorf("case-insensitive regex must carry -i: %v", got)
	}
	got = renderACL(ACLObject{Name: "kw", Type: "url_regex", CaseInsensitive: false, Values: []string{"casino"}}, path)
	if strings.Join(got, "|") != "acl sqa_kw url_regex casino" {
		t.Errorf("case-sensitive regex must not: %v", got)
	}
	got = renderACL(ACLObject{Name: "work", Type: "time", Values: []string{"MTWHF 09:00-18:00", "SA 10:00-14:00"}}, path)
	if strings.Join(got, "|") != "acl sqa_work time MTWHF 09:00-18:00|acl sqa_work time SA 10:00-14:00" {
		t.Errorf("time needs one window per line: %v", got)
	}
	got = renderACL(ACLObject{Name: "list", Type: "blocklist", Values: []string{"7"}}, path)
	if strings.Join(got, "|") != `acl sqa_list dstdomain "/lists/7.txt"` {
		t.Errorf("blocklist: %v", got)
	}

	var many []string
	for i := 0; i < 60; i++ {
		many = append(many, "10.0.0."+string(rune('0'+i%10)))
	}
	if n := len(renderACL(ACLObject{Name: "big", Type: "src", Values: many}, path)); n != 3 {
		t.Errorf("60 values should be split over 3 lines of 25, got %d", n)
	}
}

func TestRulesBecomeOneOrderedBlockBeforeTheStockAllows(t *testing.T) {
	m, _, path, _ := newAccess(t)
	social := mustACL(t, m, "social", "dstdomain", "facebook.com", "tiktok.com")
	it := mustACL(t, m, "it_dept", "src", "10.0.0.5", "10.0.0.6")

	mustRule(t, m, "deny", "no social media except IT", term(social, false), term(it, true))
	mustRule(t, m, "allow", "", term(it, false))

	c := readConf(t, path)
	want := accessBlockStart + "\n" +
		"acl sqa_social dstdomain .facebook.com .tiktok.com\n" +
		"acl sqa_it_dept src 10.0.0.5 10.0.0.6\n" +
		"# rule 1: no social media except IT\n" +
		"http_access deny sqa_social !sqa_it_dept\n" +
		"http_access allow sqa_it_dept\n" +
		accessBlockEnd + "\n"
	if !strings.Contains(c, want) {
		t.Fatalf("unexpected block:\n%s", c)
	}
	if !(idx(c, accessBlockStart) < idx(c, "http_access allow localhost")) {
		t.Fatal("the user's rules must be evaluated before the stock allow rules")
	}
	if !(idx(c, "http_access deny !Safe_ports") < idx(c, accessBlockStart)) {
		t.Fatal("stock safety denies must stay first")
	}
}

// Two panel blocks that both insert "before the first allow" would end up in
// whatever order they were last regenerated. The order must be fixed.
func TestBlockOrderIsFixedWhateverTheRegenerationOrder(t *testing.T) {
	m, mgr, path, _ := newAccess(t)
	rm := NewRestrictionManager(newTestRestrictionDB(t), mgr)

	a := mustACL(t, m, "social", "dstdomain", "facebook.com")
	mustRule(t, m, "deny", "", term(a, false))

	for round := 0; round < 3; round++ {
		if round%2 == 0 {
			if _, err := rm.Create(Restriction{Name: "work_" + string(rune('a'+round)), Domains: []string{"youtube.com"},
				Days: []string{"M"}, StartTime: "09:00", EndTime: "18:00"}); err != nil {
				t.Fatal(err)
			}
		}
		// Regenerate in both orders; the result must not depend on it.
		for _, first := range []string{"rules", "restrictions", "rules"} {
			if first == "rules" {
				if err := m.Regenerate(); err != nil {
					t.Fatal(err)
				}
			} else if err := rm.Regenerate(); err != nil {
				t.Fatal(err)
			}
			c := readConf(t, path)
			r, x, allow := idx(c, restrictionBlockStart), idx(c, accessBlockStart), idx(c, "http_access allow localhost")
			if !(r != -1 && r < x && x < allow) {
				t.Fatalf("round %d after %s: want restrictions(%d) < access rules(%d) < stock allow(%d):\n%s", round, first, r, x, allow, c)
			}
			if strings.Count(c, accessBlockStart) != 1 || strings.Count(c, restrictionBlockStart) != 1 {
				t.Fatalf("each block must appear exactly once:\n%s", c)
			}
		}
	}
}

func TestReorderChangesTheOrderInSquidConf(t *testing.T) {
	m, _, path, _ := newAccess(t)
	a := mustACL(t, m, "aaa", "port", "8080")
	b := mustACL(t, m, "bbb", "port", "9090")
	r1 := mustRule(t, m, "allow", "", term(a, false))
	r2 := mustRule(t, m, "deny", "", term(b, false))

	c := readConf(t, path)
	if !(idx(c, "http_access allow sqa_aaa") < idx(c, "http_access deny sqa_bbb")) {
		t.Fatal("initial order wrong")
	}
	if err := m.ReorderRules([]int64{r2.ID, r1.ID}); err != nil {
		t.Fatal(err)
	}
	c = readConf(t, path)
	if !(idx(c, "http_access deny sqa_bbb") < idx(c, "http_access allow sqa_aaa")) {
		t.Fatalf("reordered rules must be written in the new order:\n%s", c)
	}

	rules, _ := m.ListRules()
	if rules[0].ID != r2.ID || rules[0].Position != 1 || rules[1].Position != 2 {
		t.Fatalf("positions: %+v", rules)
	}
	for name, ids := range map[string][]int64{"missing": {r2.ID}, "duplicate": {r2.ID, r2.ID}, "unknown": {r2.ID, 999}} {
		if err := m.ReorderRules(ids); err == nil {
			t.Errorf("%s: an incomplete or invalid order must be refused", name)
		}
	}
}

func TestInsertRuleAtPositionAndDeleteRenumbers(t *testing.T) {
	m, _, _, _ := newAccess(t)
	a := mustACL(t, m, "aaa", "port", "1000")
	r1 := mustRule(t, m, "allow", "first", term(a, false))
	r2 := mustRule(t, m, "deny", "second", term(a, false))
	top, err := m.CreateRule("deny", []RuleTerm{term(a, false)}, "top", 1, true)
	if err != nil {
		t.Fatal(err)
	}

	rules, _ := m.ListRules()
	if got := []int64{rules[0].ID, rules[1].ID, rules[2].ID}; got[0] != top.ID || got[1] != r1.ID || got[2] != r2.ID {
		t.Fatalf("insert at position 1: %v", got)
	}
	if err := m.DeleteRule(r1.ID); err != nil {
		t.Fatal(err)
	}
	rules, _ = m.ListRules()
	if len(rules) != 2 || rules[0].Position != 1 || rules[1].Position != 2 {
		t.Fatalf("positions must be contiguous after a delete: %+v", rules)
	}
}

func TestDisabledRulesAreNotWrittenAndUnusedACLsNeither(t *testing.T) {
	m, _, path, _ := newAccess(t)
	used := mustACL(t, m, "used", "port", "8080")
	mustACL(t, m, "unused", "port", "9090")
	r := mustRule(t, m, "deny", "", term(used, false))

	c := readConf(t, path)
	if !strings.Contains(c, "sqa_used") || strings.Contains(c, "sqa_unused") {
		t.Fatalf("only ACLs used by enabled rules belong in squid.conf:\n%s", c)
	}

	off := false
	if _, err := m.UpdateRule(r.ID, RuleUpdate{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if c := readConf(t, path); strings.Contains(c, accessBlockStart) {
		t.Fatalf("with every rule disabled the block must disappear:\n%s", c)
	}
	on := true
	if _, err := m.UpdateRule(r.ID, RuleUpdate{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConf(t, path), "http_access deny sqa_used") {
		t.Fatal("re-enabling must bring the rule back")
	}
}

func TestACLCannotBeDeletedWhileARuleUsesIt(t *testing.T) {
	m, _, _, _ := newAccess(t)
	a := mustACL(t, m, "aaa", "port", "8080")
	r := mustRule(t, m, "deny", "", term(a, false))

	if err := m.DeleteACL(a.ID); !errors.Is(err, ErrACLInUse) {
		t.Fatalf("expected ErrACLInUse, got %v", err)
	}
	if err := m.DeleteRule(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteACL(a.ID); err != nil {
		t.Fatalf("an unused ACL can go: %v", err)
	}
}

func TestRuleAndACLValidation(t *testing.T) {
	m, _, _, _ := newAccess(t)
	a := mustACL(t, m, "aaa", "port", "8080")

	if _, err := m.CreateACL("Bad Name", "port", []string{"80"}, true, ""); err == nil {
		t.Error("invalid ACL name accepted")
	}
	if _, err := m.CreateACL("aaa", "port", []string{"81"}, true, ""); err == nil {
		t.Error("duplicate ACL name accepted")
	}
	if _, err := m.CreateACL("multi", "port", []string{"80"}, true, "two\nlines"); err == nil {
		t.Error("multi-line description accepted")
	}
	for name, c := range map[string]struct {
		action string
		terms  []RuleTerm
	}{
		"bad action": {"permit", []RuleTerm{term(a, false)}},
		"no terms":   {"deny", nil},
		"unknown":    {"deny", []RuleTerm{{ACLID: 999}}},
		"duplicate":  {"deny", []RuleTerm{term(a, false), term(a, true)}},
	} {
		if _, err := m.CreateRule(c.action, c.terms, "", 0, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := m.CreateRule("deny", []RuleTerm{term(a, false)}, "line\nbreak", 0, true); err == nil {
		t.Error("a comment with a newline could inject config lines")
	}
}

func TestSquidRejectingTheConfigRollsTheDatabaseBack(t *testing.T) {
	m, _, path, dir := newAccess(t)
	a := mustACL(t, m, "aaa", "port", "8080")
	before := readConf(t, path)

	touch(t, filepath.Join(dir, "fail_parse"))
	if _, err := m.CreateRule("deny", []RuleTerm{term(a, false)}, "", 0, true); err == nil {
		t.Fatal("expected squid's rejection to surface")
	}
	if rules, _ := m.ListRules(); len(rules) != 0 {
		t.Fatalf("the rule must not stay in the database when squid refused it: %+v", rules)
	}
	if readConf(t, path) != before {
		t.Fatal("squid.conf must be untouched")
	}
}

func TestUpdatingAnACLRewritesTheRulesUsingIt(t *testing.T) {
	m, _, path, _ := newAccess(t)
	a := mustACL(t, m, "social", "dstdomain", "facebook.com")
	mustRule(t, m, "deny", "", term(a, false))

	if _, err := m.UpdateACL(a.ID, []string{"facebook.com", "instagram.com"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConf(t, path), "acl sqa_social dstdomain .facebook.com .instagram.com") {
		t.Fatalf("the new values must reach squid.conf:\n%s", readConf(t, path))
	}
	if _, err := m.UpdateACL(a.ID, []string{"nodots"}, nil, nil); err == nil {
		t.Fatal("invalid values must be refused")
	}
	got, _ := m.GetACL(a.ID)
	if len(got.Values) != 2 {
		t.Fatalf("a refused update must not change the ACL: %+v", got)
	}
}

func TestExistingBlocksAreUntouched(t *testing.T) {
	m, _, path, _ := newAccess(t)
	before := readConf(t, path)
	a := mustACL(t, m, "aaa", "port", "8080")
	r := mustRule(t, m, "deny", "", term(a, false))
	if err := m.DeleteRule(r.ID); err != nil {
		t.Fatal(err)
	}
	if got := readConf(t, path); got != before {
		t.Fatalf("adding and removing a rule must give back the original file:\n%s", got)
	}
}
