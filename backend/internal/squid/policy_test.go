package squid

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// policyConf resembles a real installation: stock ACLs with inline comments,
// an include, the manual blacklist (file based), the panel's blocks, and the
// LAN / proxy-auth allows.
const policyConf = `acl localnet src 10.0.0.0/8		# RFC 1918
acl localnet src 192.168.0.0/16	# RFC 1918
acl SSL_ports port 443
acl Safe_ports port 80		# http
acl Safe_ports port 443
acl Safe_ports port 1025-65535
http_access deny !Safe_ports
http_access deny CONNECT !SSL_ports
acl blocked_sites dstdomain "/etc/squid/blocked_sites.txt"
http_access deny blocked_sites
# --- squidadmin: time-restrictions (managed, do not edit by hand) ---
acl sqa_r1_domains dstdomain .youtube.com
acl sqa_r1_time time MTWHF 09:00-18:00
acl sqa_r1_exempt src 10.0.0.5
http_access deny sqa_r1_domains sqa_r1_time !sqa_r1_exempt
# --- end squidadmin: time-restrictions ---
# --- squidadmin: access rules (managed, do not edit by hand) ---
acl sqa_kw url_regex -i casino
acl sqa_vip proxy_auth alice bob
acl sqa_media dstdomain .netflix.com
# rule 4: vip may stream
http_access allow sqa_media sqa_vip
# rule 5: no gambling
http_access deny sqa_kw
# --- end squidadmin: access rules ---
http_access allow localhost manager
http_access deny manager
http_access allow localhost
http_access deny to_localhost
http_access allow localnet
include /etc/squid/conf.d/*.conf
# --- squidadmin: proxy authentication ---
acl authenticated_users proxy_auth REQUIRED
http_access allow authenticated_users
# --- end squidadmin ---
http_access deny all
`

func testEnv() PolicyEnv {
	files := map[string]string{
		"/etc/squid/blocked_sites.txt": ".badsite.example\n# comment\n.evil.example\n",
		"/etc/squid/conf.d/extra.conf": "acl office_ports port 8080\nhttp_access deny office_ports\n",
	}
	return PolicyEnv{
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
		Glob: func(pat string) ([]string, error) {
			if pat == "/etc/squid/conf.d/*.conf" {
				return []string{"/etc/squid/conf.d/extra.conf"}, nil
			}
			return nil, nil
		},
		LookupIP: func(host string) ([]net.IP, error) {
			switch host {
			case "internal.corp":
				return []net.IP{net.ParseIP("10.9.9.9")}, nil
			case "public.example":
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return nil, errors.New("no such host")
		},
		BlacklistACL: "blocked_sites",
	}
}

func mondayAt(h, m int) time.Time { return time.Date(2026, 9, 21, h, m, 0, 0, time.UTC) } // a Monday
func sundayAt(h, m int) time.Time { return time.Date(2026, 9, 20, h, m, 0, 0, time.UTC) } // a Sunday

func TestParsePolicyReadsEverythingInOrder(t *testing.T) {
	p := ParsePolicy(policyConf, testEnv())

	// ACLs: built-ins, multi-line values merged, inline comments cut, files read.
	if a := p.ACLs["localnet"]; a == nil || strings.Join(a.Args, ",") != "10.0.0.0/8,192.168.0.0/16" {
		t.Errorf("localnet = %+v", a)
	}
	if a := p.ACLs["Safe_ports"]; a == nil || strings.Join(a.Args, ",") != "80,443,1025-65535" {
		t.Errorf("Safe_ports = %+v", a)
	}
	if a := p.ACLs["blocked_sites"]; a == nil || strings.Join(a.Args, ",") != ".badsite.example,.evil.example" {
		t.Errorf("a quoted value is a file with one value per line: %+v", a)
	}
	if a := p.ACLs["sqa_kw"]; a == nil || !a.CaseInsensitive || a.Args[0] != "casino" {
		t.Errorf("the -i flag must be recognised: %+v", a)
	}
	if a := p.ACLs["sqa_r1_time"]; a == nil || strings.Join(a.Args, ",") != "MTWHF 09:00-18:00" {
		t.Errorf("time spec must be paired: %+v", a)
	}
	for _, builtin := range []string{"all", "localhost", "manager", "to_localhost", "CONNECT"} {
		if p.ACLs[builtin] == nil {
			t.Errorf("built-in ACL %q missing", builtin)
		}
	}

	// Rules: in file order, with the included file's rule at its position.
	var order []string
	for _, r := range p.Rules {
		order = append(order, r.Source)
	}
	want := "stock,stock,blacklist,time_restriction,rule,rule,stock,stock,stock,stock,lan,stock,proxy_auth,stock"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("rule sources:\n got  %s\n want %s", got, want)
	}
	if p.Rules[4].Comment != "vip may stream" || p.Rules[5].Comment != "no gambling" {
		t.Errorf("rule comments: %q / %q", p.Rules[4].Comment, p.Rules[5].Comment)
	}
	if last := p.Rules[len(p.Rules)-1]; last.Raw != "http_access deny all" {
		t.Errorf("last rule = %q", last.Raw)
	}
	for i, r := range p.Rules {
		if r.Index != i+1 {
			t.Errorf("rule %d has Index %d", i+1, r.Index)
		}
	}
}

func TestEvaluateAgainstARealisticPolicy(t *testing.T) {
	p := ParsePolicy(policyConf, testEnv())
	env := testEnv()

	cases := []struct {
		name     string
		req      Request
		decision string
		source   string // source of the deciding rule
		reason   string // substring expected somewhere in the trace
	}{
		{"local user, ordinary site", Request{SrcIP: "127.0.0.1", URL: "http://example.com/"}, "allow", "stock", ""},
		{"blacklisted domain beats allow localhost", Request{SrcIP: "127.0.0.1", URL: "http://www.badsite.example/x"}, "deny", "blacklist", ".badsite.example"},
		{"unsafe port", Request{SrcIP: "10.1.1.1", URL: "http://example.com:25/"}, "deny", "stock", "port 25"},
		{"CONNECT to a non-SSL port", Request{SrcIP: "10.1.1.1", Method: "CONNECT", URL: "example.com:8443"}, "deny", "stock", ""},
		{"CONNECT on 443 is fine", Request{SrcIP: "127.0.0.1", Method: "CONNECT", URL: "example.com:443"}, "allow", "stock", ""},

		{"working hours: youtube denied", Request{SrcIP: "10.2.2.2", URL: "https://www.youtube.com/watch", Time: mondayAt(10, 30)}, "deny", "time_restriction", "within MTWHF"},
		{"evening: youtube allowed", Request{SrcIP: "10.2.2.2", URL: "https://www.youtube.com/", Time: mondayAt(20, 0)}, "allow", "lan", ""},
		{"weekend: youtube allowed", Request{SrcIP: "10.2.2.2", URL: "https://www.youtube.com/", Time: sundayAt(11, 0)}, "allow", "lan", ""},
		{"the exempt host is not restricted", Request{SrcIP: "10.0.0.5", URL: "https://www.youtube.com/", Time: mondayAt(10, 30)}, "allow", "lan", ""},

		{"keyword rule blocks, case-insensitively", Request{SrcIP: "10.2.2.2", URL: "http://shop.example/CASINO-bonus"}, "deny", "rule", "matches"},
		{"vip streams (rule 4 before rule 5)", Request{SrcIP: "10.2.2.2", User: "alice", URL: "http://www.netflix.com/browse"}, "allow", "rule", "alice"},
		{"non-vip on the media site falls through to the LAN allow", Request{SrcIP: "10.2.2.2", User: "carol", URL: "http://www.netflix.com/"}, "allow", "lan", ""},
		{"a rule with proxy_auth and no user asks for a login", Request{SrcIP: "10.2.2.2", URL: "http://www.netflix.com/"}, "auth_required", "rule", "407"},

		{"a LAN client is allowed before the included deny is reached", Request{SrcIP: "10.2.2.2", URL: "http://intranet.example:8080/"}, "allow", "lan", ""},
		{"the included file's rule applies further down", Request{SrcIP: "203.0.113.7", User: "dave", URL: "http://intranet.example:8080/"}, "deny", "stock", "port 8080"},
		{"outside the LAN, no auth: the login is requested", Request{SrcIP: "203.0.113.7", URL: "http://example.com/"}, "auth_required", "proxy_auth", ""},
		{"outside the LAN, signed-in user", Request{SrcIP: "203.0.113.7", User: "dave", URL: "http://example.com/"}, "allow", "proxy_auth", "dave"},
		{"to_localhost is denied for a non-local source", Request{SrcIP: "203.0.113.7", User: "dave", URL: "http://127.0.0.1:8080/"}, "deny", "stock", ""},
		{"a destination resolved to an internal address is fine here", Request{SrcIP: "10.2.2.2", URL: "http://internal.corp/"}, "allow", "lan", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, err := p.Evaluate(c.req, env)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if v.Decision != c.decision {
				t.Fatalf("decision = %s (rule %d), want %s\n%s", v.Decision, v.RuleIndex, c.decision, dump(v))
			}
			if v.RuleIndex == 0 {
				t.Fatalf("a decision must name its rule\n%s", dump(v))
			}
			if got := p.Rules[v.RuleIndex-1].Source; got != c.source {
				t.Errorf("decided by a %q rule, want %q\n%s", got, c.source, dump(v))
			}
			if c.reason != "" && !strings.Contains(dump(v), c.reason) {
				t.Errorf("trace should mention %q\n%s", c.reason, dump(v))
			}
			// Everything after the deciding rule is reported as not reached.
			for _, tr := range v.Trace[v.RuleIndex:] {
				if tr.Result != "not-reached" {
					t.Errorf("rule %d after the decision must be not-reached, got %s", tr.Rule.Index, tr.Result)
				}
			}
		})
	}
}

func dump(v Verdict) string {
	var b strings.Builder
	for _, tr := range v.Trace {
		if tr.Result == "not-reached" {
			continue
		}
		fmt.Fprintf(&b, "  #%d %s [%s] %s\n", tr.Rule.Index, tr.Result, tr.Rule.Source, tr.Rule.Raw)
		for _, t := range tr.Terms {
			fmt.Fprintf(&b, "        %s %s: %s\n", t.ACL, t.Result, t.Detail)
		}
	}
	return b.String()
}

func TestEvaluateStopsWithUnknownRatherThanGuessing(t *testing.T) {
	p := ParsePolicy("acl weird external /usr/bin/helper %SRC\nhttp_access deny weird\nhttp_access allow all\n", testEnv())
	v, err := p.Evaluate(Request{SrcIP: "10.0.0.1", URL: "http://example.com/"}, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	if v.Decision != "unknown" || v.RuleIndex != 1 {
		t.Fatalf("a rule using an ACL type the panel cannot judge must yield unknown, got %s rule %d", v.Decision, v.RuleIndex)
	}
	if v.Trace[1].Result != "not-reached" {
		t.Errorf("later rules must not be consulted after an unknown: %+v", v.Trace[1])
	}
}

func TestEvaluateDefaultIsTheOppositeOfTheLastRule(t *testing.T) {
	deny := ParsePolicy("acl site dstdomain .a.example\nhttp_access allow site\n", testEnv())
	v, _ := deny.Evaluate(Request{SrcIP: "10.0.0.1", URL: "http://b.example/"}, testEnv())
	if v.Decision != "deny" || v.RuleIndex != 0 {
		t.Errorf("last rule is allow, nothing matched: want deny, got %s", v.Decision)
	}
	allow := ParsePolicy("acl site dstdomain .a.example\nhttp_access deny site\n", testEnv())
	v, _ = allow.Evaluate(Request{SrcIP: "10.0.0.1", URL: "http://b.example/"}, testEnv())
	if v.Decision != "allow" {
		t.Errorf("last rule is deny, nothing matched: want allow, got %s", v.Decision)
	}
}

func TestACLTypeMatching(t *testing.T) {
	conf := `acl d dstdomain .example.com exact.org
acl r dstdom_regex -i ^ads[0-9]+\.
acl u url_regex \.exe$
acl up urlpath_regex ^/private/
acl b browser -i curl
acl p port 8000-8100 9999
acl m method POST PUT
acl s src 192.168.1.10-192.168.1.20 172.16.0.0/12
acl tm time SA 10:00-14:00
acl dd dst 93.184.216.0/24
http_access allow all
`
	p := ParsePolicy(conf, testEnv())
	env := testEnv()

	check := func(acl string, req Request, want tri) {
		t.Helper()
		rp, err := parseRequest(req)
		if err != nil {
			t.Fatalf("%s: %v", acl, err)
		}
		if got, detail := env.evalACL(p.ACLs[acl], req, rp); got != want {
			t.Errorf("%s on %+v: got %d (%s), want %d", acl, req, got, detail, want)
		}
	}
	yes, no := triYes, triNo

	check("d", Request{URL: "http://EXAMPLE.com/"}, yes)
	check("d", Request{URL: "http://a.b.example.com/"}, yes)
	check("d", Request{URL: "http://notexample.com/"}, no)
	check("d", Request{URL: "http://exact.org/"}, yes)
	check("d", Request{URL: "http://sub.exact.org/"}, no) // no leading dot: exact host only
	check("r", Request{URL: "http://ADS42.tracker.example/"}, yes)
	check("r", Request{URL: "http://news.example/"}, no)
	check("u", Request{URL: "http://x.example/setup.exe"}, yes)
	check("u", Request{URL: "http://x.example/setup.exe?dl=1"}, no)
	check("up", Request{URL: "http://x.example/private/a"}, yes)
	check("up", Request{URL: "http://x.example/public/a"}, no)
	check("b", Request{URL: "http://x.example/", UserAgent: "CURL/8.0"}, yes)
	check("b", Request{URL: "http://x.example/", UserAgent: "Mozilla"}, no)
	check("p", Request{URL: "http://x.example:8050/"}, yes)
	check("p", Request{URL: "http://x.example:9999/"}, yes)
	check("p", Request{URL: "http://x.example/"}, no)
	check("p", Request{URL: "https://x.example/"}, no)
	check("m", Request{URL: "http://x.example/", Method: "post"}, yes)
	check("m", Request{URL: "http://x.example/"}, no)
	check("s", Request{URL: "http://x.example/", SrcIP: "192.168.1.15"}, yes)
	check("s", Request{URL: "http://x.example/", SrcIP: "192.168.1.21"}, no)
	check("s", Request{URL: "http://x.example/", SrcIP: "172.20.1.1"}, yes)
	check("s", Request{URL: "http://x.example/", SrcIP: "not-an-ip"}, triUnknown)
	check("tm", Request{URL: "http://x.example/", Time: sundayAt(11, 0)}, yes) // S = Sunday, A = Saturday: the window SA covers both
	check("tm", Request{URL: "http://x.example/", Time: mondayAt(11, 0)}, no)
	check("tm", Request{URL: "http://x.example/", Time: time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC)}, yes) // Saturday
	check("tm", Request{URL: "http://x.example/", Time: time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)}, no)
	check("dd", Request{URL: "http://public.example/"}, yes)
	check("dd", Request{URL: "http://internal.corp/"}, no)
	check("dd", Request{URL: "http://unresolvable.invalid/"}, no)
}

func TestParseRequestHandlesSchemesPortsAndConnect(t *testing.T) {
	cases := []struct {
		req  Request
		host string
		port int
		full string
	}{
		{Request{URL: "example.com"}, "example.com", 80, "http://example.com/"},
		{Request{URL: "https://Example.COM/A?b=1"}, "example.com", 443, "https://example.com/A?b=1"},
		{Request{URL: "http://example.com:3128/x"}, "example.com", 3128, "http://example.com:3128/x"},
		{Request{Method: "CONNECT", URL: "example.com:8443"}, "example.com", 8443, "example.com:8443"},
		{Request{Method: "connect", URL: "example.com"}, "example.com", 443, "example.com:443"},
	}
	for _, c := range cases {
		got, err := parseRequest(c.req)
		if err != nil || got.Host != c.host || got.Port != c.port || got.FullURL != c.full {
			t.Errorf("%+v -> %+v, %v; want %s %d %s", c.req, got, err, c.host, c.port, c.full)
		}
	}
	for _, bad := range []Request{{URL: ""}, {URL: "http://"}, {Method: "CONNECT", URL: "host:99999"}} {
		if _, err := parseRequest(bad); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}

// The tester must agree with what the panel itself generated.
func TestPolicyOfAPanelGeneratedConfigMatchesTheRules(t *testing.T) {
	m, _, path, _ := newAccess(t)
	social := mustACL(t, m, "social", "dstdomain", "facebook.com")
	it := mustACL(t, m, "it_dept", "src", "10.0.0.5")
	mustRule(t, m, "deny", "no social", term(social, false), term(it, true))

	// The fixture config only names Safe_ports (squid's stock config defines it).
	stock := "acl Safe_ports port 80 443 1025-65535\nacl blocked_sites dstdomain .blocked.example\n"
	p := ParsePolicy(stock+readConf(t, path), PolicyEnv{BlacklistACL: "blocked_sites"})
	env := PolicyEnv{}

	v, _ := p.Evaluate(Request{SrcIP: "10.0.0.9", URL: "http://www.facebook.com/"}, env)
	if v.Decision != "deny" || p.Rules[v.RuleIndex-1].Source != "rule" {
		t.Errorf("a normal user is denied by the panel rule: %s\n%s", v.Decision, dump(v))
	}
	v, _ = p.Evaluate(Request{SrcIP: "10.0.0.5", URL: "http://www.facebook.com/"}, env)
	if v.Decision != "allow" {
		t.Errorf("the IT host is exempt (negated ACL): %s\n%s", v.Decision, dump(v))
	}
}

// squid reaches a proxy_auth ACL for a request without credentials and answers
// 407. Written first, that would challenge everyone; the panel therefore writes
// such conditions last.
func TestAProxyAuthConditionWrittenFirstChallengesEveryone(t *testing.T) {
	first := ParsePolicy("acl v proxy_auth alice\nacl media dstdomain .netflix.com\nhttp_access allow v media\nhttp_access allow all\n", testEnv())
	v, _ := first.Evaluate(Request{SrcIP: "10.0.0.1", URL: "http://unrelated.example/"}, testEnv())
	if v.Decision != "auth_required" {
		t.Fatalf("auth first: even an unrelated site is challenged, got %s", v.Decision)
	}

	last := ParsePolicy("acl v proxy_auth alice\nacl media dstdomain .netflix.com\nhttp_access allow media v\nhttp_access allow all\n", testEnv())
	v, _ = last.Evaluate(Request{SrcIP: "10.0.0.1", URL: "http://unrelated.example/"}, testEnv())
	if v.Decision != "allow" {
		t.Fatalf("auth last: the unrelated site must pass without a login, got %s", v.Decision)
	}
}

// authConf is accessConf with proxy authentication configured, as the panel does.
const authConf = accessConf +
	"auth_param basic program /usr/lib/squid/basic_ncsa_auth /etc/squid/passwd\n" +
	"auth_param basic realm Squid Proxy\n"

func TestGeneratedRulesPutProxyAuthConditionsLast(t *testing.T) {
	m, _, path, _ := newAccess(t)
	if err := os.WriteFile(path, []byte(authConf), 0o644); err != nil {
		t.Fatal(err)
	}
	vip := mustACL(t, m, "vip", "proxy_auth", "alice")
	media := mustACL(t, m, "media", "dstdomain", "netflix.com")
	work := mustACL(t, m, "work", "time", "MTWHF 09:00-18:00")

	// The user lists the login condition first...
	mustRule(t, m, "allow", "", term(vip, false), term(media, false), term(work, true))

	// ...the generated rule checks it last.
	if c := readConf(t, path); !strings.Contains(c, "http_access allow sqa_media !sqa_work sqa_vip\n") {
		t.Fatalf("proxy_auth must be last:\n%s", c)
	}
	// The stored rule (what the UI shows and edits) keeps the user's order.
	rules, _ := m.ListRules()
	if rules[0].Terms[0].ACLID != vip.ID {
		t.Fatalf("the stored order must be untouched: %+v", rules[0].Terms)
	}
}

// squid rejects a proxy_auth ACL declared before any auth_param line. The
// stock proxy-authentication block sits later in the file than the user rules,
// so the rules block carries its own copy of the auth_param lines.
func TestProxyAuthACLsGetTheirOwnAuthParamLinesFirst(t *testing.T) {
	m, _, path, _ := newAccess(t)
	os.WriteFile(path, []byte(authConf), 0o644)

	vip := mustACL(t, m, "vip", "proxy_auth", "alice")
	mustRule(t, m, "allow", "", term(vip, false))

	c := readConf(t, path)
	block := c[idx(c, accessBlockStart):idx(c, accessBlockEnd)]
	a, acl := idx(block, "auth_param basic program"), idx(block, "acl sqa_vip proxy_auth")
	if a == -1 || !(a < acl) {
		t.Fatalf("auth_param must precede the proxy_auth ACL inside the block:\n%s", block)
	}
	if !strings.Contains(block, "auth_param basic realm Squid Proxy") {
		t.Errorf("every auth_param line must be carried over:\n%s", block)
	}

	// Without a proxy_auth ACL the block needs no copy.
	other := mustACL(t, m, "site", "dstdomain", "a.example.com")
	m.DeleteRule(1)
	mustRule(t, m, "deny", "", term(other, false))
	c = readConf(t, path)
	if strings.Contains(c[idx(c, accessBlockStart):idx(c, accessBlockEnd)], "auth_param") {
		t.Error("a block without proxy_auth conditions must not repeat auth_param")
	}
}

func TestProxyAuthRuleIsRefusedWhileAuthenticationIsNotSetUp(t *testing.T) {
	m, _, path, _ := newAccess(t) // accessConf has no auth_param
	before := readConf(t, path)
	vip := mustACL(t, m, "vip", "proxy_auth", "alice")

	_, err := m.CreateRule("allow", []RuleTerm{term(vip, false)}, "", 0, true)
	if err == nil || !strings.Contains(err.Error(), "proxy authentication is not set up") {
		t.Fatalf("expected a clear explanation, got %v", err)
	}
	if rules, _ := m.ListRules(); len(rules) != 0 {
		t.Errorf("the rejected rule must not stay in the database: %+v", rules)
	}
	if readConf(t, path) != before {
		t.Error("squid.conf must be untouched")
	}
}

func TestConnectTargetsMayBeGivenAsFullURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com:8443/x": "example.com:8443",
		"https://example.com/":       "example.com:443",
		"http://example.com/":        "example.com:80",
		"example.com:8443/path":      "example.com:8443",
		"Example.COM.":               "example.com:443",
		"[2001:db8::1]:8443":         "2001:db8::1:8443",
	} {
		got, err := parseRequest(Request{Method: "CONNECT", URL: in})
		if err != nil || got.FullURL != want {
			t.Errorf("CONNECT %q -> %q, %v; want %q", in, got.FullURL, err, want)
		}
	}
}

// The window covers its first and last minute (checked against real squid in
// deploy/e2e/phase4.py), and each weekday has its own letter.
func TestTimeACLBoundariesAndWeekdayLetters(t *testing.T) {
	p := ParsePolicy("acl w time M 09:00-17:30\nacl sun time S 10:00-11:00\nacl sat time A 10:00-11:00\nacl thu time H 10:00-11:00\nacl wed time W 10:00-11:00\nhttp_access allow all\n", testEnv())
	env := testEnv()
	at := func(day time.Weekday, h, m int) time.Time {
		base := time.Date(2026, 9, 20, h, m, 0, 0, time.UTC) // a Sunday
		return base.AddDate(0, 0, int(day))
	}
	eval := func(acl string, tm time.Time) tri {
		req := Request{URL: "http://x.example/", Time: tm}
		rp, _ := parseRequest(req)
		got, _ := env.evalACL(p.ACLs[acl], req, rp)
		return got
	}

	for _, c := range []struct {
		name string
		tm   time.Time
		want tri
	}{
		{"one minute before the start", at(time.Monday, 8, 59), triNo},
		{"the first minute", at(time.Monday, 9, 0), triYes},
		{"the last minute", at(time.Monday, 17, 30), triYes},
		{"one minute after the end", at(time.Monday, 17, 31), triNo},
		{"the same clock time on another day", at(time.Tuesday, 12, 0), triNo},
	} {
		if got := eval("w", c.tm); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}

	// S = Sunday, M = Monday, T = Tuesday, W = Wednesday, H = Thursday, F = Friday, A = Saturday.
	for acl, day := range map[string]time.Weekday{"sun": time.Sunday, "sat": time.Saturday, "thu": time.Thursday, "wed": time.Wednesday} {
		for d := time.Sunday; d <= time.Saturday; d++ {
			want := triNo
			if d == day {
				want = triYes
			}
			if got := eval(acl, at(d, 10, 30)); got != want {
				t.Errorf("acl %s on %s: got %d, want %d", acl, d, got, want)
			}
		}
	}
}

func TestPolicyUnderstandsTheUserPolicyBlock(t *testing.T) {
	conf := `acl localnet src 10.0.0.0/8
` + userPolicyBlockStart + `
auth_param basic program /usr/lib/squid/basic_ncsa_auth /etc/squid/passwd
acl sqa_up_auth req_header Proxy-Authorization .
acl sqa_up_blocked proxy_auth bob
http_access deny sqa_up_auth sqa_up_blocked
` + userPolicyBlockEnd + `
http_access allow localhost
http_access allow localnet
http_access deny all
`
	p := ParsePolicy(conf, testEnv())
	if p.Rules[0].Source != "user_policy" {
		t.Fatalf("the block's rule must be attributed to the user policy, got %q", p.Rules[0].Source)
	}

	cases := []struct {
		name     string
		req      Request
		decision string
	}{
		{"an anonymous request is never challenged by this rule", Request{SrcIP: "127.0.0.1", URL: "http://example.com/"}, "allow"},
		{"a blocked account is refused, even from localhost", Request{SrcIP: "127.0.0.1", User: "bob", URL: "http://example.com/"}, "deny"},
		{"a blocked account is refused on the LAN too", Request{SrcIP: "10.1.1.1", User: "bob", URL: "http://example.com/"}, "deny"},
		{"other accounts are not affected", Request{SrcIP: "127.0.0.1", User: "alice", URL: "http://example.com/"}, "allow"},
	}
	for _, c := range cases {
		v, err := p.Evaluate(c.req, testEnv())
		if err != nil || v.Decision != c.decision {
			t.Errorf("%s: decision %q, want %q (%v)\n%s", c.name, v.Decision, c.decision, err, dump(v))
		}
	}
}
