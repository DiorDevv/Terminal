package squid

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The effective access policy: every acl and http_access line of the running
// configuration (stock lines, includes, and every block the panel generated),
// in the order squid evaluates them. It powers two things — the read-only
// "what really happens" view, and a tester that answers "why was this request
// allowed or blocked?" by walking the rules exactly like squid does.

// PolicyACL is one ACL as declared in squid.conf.
type PolicyACL struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Args            []string `json:"args"`
	CaseInsensitive bool     `json:"case_insensitive"`
	Line            int      `json:"line"`
}

type PolicyTerm struct {
	ACL    string `json:"acl"`
	Negate bool   `json:"negate"`
}

// PolicyRule is one http_access line.
type PolicyRule struct {
	Index  int          `json:"index"` // evaluation order, 1-based
	Line   int          `json:"line"`  // line number in squid.conf (0 for included files)
	Action string       `json:"action"`
	Terms  []PolicyTerm `json:"terms"`
	Raw    string       `json:"raw"`
	// Source says who owns the rule: stock, blacklist, time_restriction,
	// rule (the panel's access rules), proxy_auth, user_policy, lan.
	Source  string `json:"source"`
	Comment string `json:"comment,omitempty"`
}

type Policy struct {
	ACLs  map[string]*PolicyACL `json:"acls"`
	Rules []PolicyRule          `json:"rules"`
}

// PolicyEnv carries the outside world the parser and evaluator need, so tests
// can replace it.
type PolicyEnv struct {
	ReadFile func(string) ([]byte, error)
	Glob     func(string) ([]string, error)
	LookupIP func(host string) ([]net.IP, error)
	// BlacklistACL is the name of the manual blacklist ACL, for labelling.
	BlacklistACL string
}

func NewPolicyEnv(blacklistACL string) PolicyEnv {
	return PolicyEnv{
		ReadFile: os.ReadFile,
		Glob:     filepath.Glob,
		LookupIP: func(host string) ([]net.IP, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			var out []net.IP
			for _, a := range addrs {
				out = append(out, a.IP)
			}
			return out, err
		},
		BlacklistACL: blacklistACL,
	}
}

// builtinACLs are defined by squid itself, so squid.conf never declares them.
func builtinACLs() map[string]*PolicyACL {
	mk := func(name, typ string, args ...string) *PolicyACL {
		return &PolicyACL{Name: name, Type: typ, Args: args}
	}
	list := []*PolicyACL{
		mk("all", "all"),
		mk("manager", "proto", "cache_object"),
		mk("localhost", "src", "127.0.0.1/32", "::1"),
		mk("to_localhost", "dst", "127.0.0.0/8", "0.0.0.0/32", "::1"),
		mk("to_linklocal", "dst", "169.254.0.0/16", "fe80::/10"),
		mk("CONNECT", "method", "CONNECT"),
	}
	out := map[string]*PolicyACL{}
	for _, a := range list {
		out[a.Name] = a
	}
	return out
}

// stripInlineComment cuts a trailing "# comment" (a # that starts a token).
func stripInlineComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
}

// ParsePolicy reads the configuration text into ACLs and ordered rules.
func ParsePolicy(content string, env PolicyEnv) Policy {
	p := Policy{ACLs: builtinACLs(), Rules: []PolicyRule{}}
	parsePolicyInto(&p, content, env, 0, true)
	for i := range p.Rules {
		p.Rules[i].Index = i + 1
	}
	return p
}

func parsePolicyInto(p *Policy, content string, env PolicyEnv, depth int, top bool) {
	block := "" // which panel block the current line sits in
	pendingComment := ""

	for n, raw := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(raw)

		switch {
		case trimmed == restrictionBlockStart:
			block = "time_restriction"
		case trimmed == accessBlockStart:
			block = "rule"
		case trimmed == userPolicyBlockStart:
			block = "user_policy"
		case trimmed == "# --- squidadmin: proxy authentication ---":
			block = "proxy_auth"
		case strings.HasPrefix(trimmed, managedBlockPrefix) && !strings.HasPrefix(trimmed, managedBlockEndPrefix):
			block = "other"
		case strings.HasPrefix(trimmed, managedBlockEndPrefix):
			block = ""
		}
		if rest, ok := strings.CutPrefix(trimmed, "# rule "); ok && block == "rule" {
			_, c, _ := strings.Cut(rest, ": ")
			pendingComment = c
			continue
		}

		line := stripInlineComment(trimmed)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)

		switch fields[0] {
		case "include":
			if depth >= 5 || len(fields) < 2 || env.Glob == nil {
				continue
			}
			matches, _ := env.Glob(fields[1])
			for _, f := range matches {
				if data, err := env.ReadFile(f); err == nil {
					parsePolicyInto(p, string(data), env, depth+1, false)
				}
			}

		case "acl":
			if len(fields) < 4 {
				continue
			}
			addACL(p, fields[1], fields[2], fields[3:], n+1, env)

		case "http_access":
			if len(fields) < 3 || (fields[1] != "allow" && fields[1] != "deny") {
				continue
			}
			r := PolicyRule{Action: fields[1], Raw: line, Source: block, Comment: pendingComment}
			if top {
				r.Line = n + 1
			}
			for _, t := range fields[2:] {
				neg := strings.HasPrefix(t, "!")
				r.Terms = append(r.Terms, PolicyTerm{ACL: strings.TrimPrefix(t, "!"), Negate: neg})
			}
			r.Source = classifyRule(r, block, env)
			p.Rules = append(p.Rules, r)
			pendingComment = ""
		}
	}
}

func classifyRule(r PolicyRule, block string, env PolicyEnv) string {
	switch block {
	case "time_restriction", "rule", "proxy_auth", "user_policy":
		return block
	}
	if env.BlacklistACL != "" && len(r.Terms) == 1 && r.Terms[0].ACL == env.BlacklistACL && !r.Terms[0].Negate {
		return "blacklist"
	}
	if r.Action == "allow" && len(r.Terms) == 1 && r.Terms[0].ACL == "localnet" && !r.Terms[0].Negate {
		return "lan"
	}
	return "stock"
}

func addACL(p *Policy, name, typ string, args []string, line int, env PolicyEnv) {
	ci := false
	// Options between the type and the values: -i / +i (case), -n (no lookup).
	for len(args) > 0 && (args[0] == "-i" || args[0] == "+i" || args[0] == "-n") {
		if args[0] == "-i" {
			ci = true
		} else if args[0] == "+i" {
			ci = false
		}
		args = args[1:]
	}

	vals := []string{} // never nil: an empty list file must encode as [] not null
	for _, a := range args {
		if strings.HasPrefix(a, "#") {
			break
		}
		if len(a) >= 2 && strings.HasPrefix(a, `"`) && strings.HasSuffix(a, `"`) {
			// A quoted value is a file with one value per line.
			if env.ReadFile != nil {
				if data, err := env.ReadFile(a[1 : len(a)-1]); err == nil {
					for _, l := range strings.Split(string(data), "\n") {
						if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
							vals = append(vals, l)
						}
					}
				}
			}
			continue
		}
		vals = append(vals, a)
	}

	if typ == "time" {
		vals = pairTimeSpecs(vals)
	}

	// Later acl lines with the same name add values; the built-in of the same
	// name (if any) is replaced by the first user declaration.
	if cur, ok := p.ACLs[name]; ok && cur.Line != 0 {
		cur.Args = append(cur.Args, vals...)
		return
	}
	p.ACLs[name] = &PolicyACL{Name: name, Type: typ, Args: vals, CaseInsensitive: ci, Line: line}
}

var (
	timeDaysRe   = regexp.MustCompile(`^[SMTWHFA]+$`)
	timeWindowRe = regexp.MustCompile(`^\d{1,2}:\d{2}-\d{1,2}:\d{2}$`)
)

// pairTimeSpecs turns the tokens of "time MTWHF 09:00-18:00" into
// "MTWHF 09:00-18:00" entries. A window without days means every day.
func pairTimeSpecs(tokens []string) []string {
	var out []string
	days := ""
	for _, t := range tokens {
		switch {
		case timeDaysRe.MatchString(t):
			days = t
		case timeWindowRe.MatchString(t):
			d := days
			if d == "" {
				d = "SMTWHFA"
			}
			out = append(out, d+" "+t)
			days = ""
		}
	}
	return out
}

// ------------------------------------------------------------- evaluation

// Request is a hypothetical request to evaluate.
type Request struct {
	SrcIP     string    `json:"src_ip"`
	URL       string    `json:"url"`
	Method    string    `json:"method"`
	User      string    `json:"user"`
	UserAgent string    `json:"user_agent"`
	Time      time.Time `json:"-"`
}

type TermResult struct {
	ACL    string `json:"acl"`
	Negate bool   `json:"negate"`
	Result string `json:"result"` // match, no-match, unknown, skipped
	Detail string `json:"detail"`
}

type RuleTrace struct {
	Rule   PolicyRule   `json:"rule"`
	Result string       `json:"result"` // match, no-match, unknown, auth-required, not-reached
	Terms  []TermResult `json:"terms"`
}

type Verdict struct {
	// Decision: allow, deny, auth_required (squid answers 407) or unknown.
	Decision  string      `json:"decision"`
	RuleIndex int         `json:"rule_index"` // deciding rule, 0 if none
	Trace     []RuleTrace `json:"trace"`
	Notes     []string    `json:"notes"`
}

// reqParts are the pieces of a request the ACL types look at.
type reqParts struct {
	Host, Path, FullURL, Scheme string
	Port                        int
	Method                      string
}

func parseRequest(r Request) (reqParts, error) {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		method = "GET"
	}
	raw := strings.TrimSpace(r.URL)
	if raw == "" {
		return reqParts{}, fmt.Errorf("a URL is required")
	}

	if method == "CONNECT" {
		// A CONNECT target is "host:port"; a full URL is accepted too, since
		// that is what people paste ("https://example.com:8443/x").
		target, defPort := raw, "443"
		if strings.Contains(raw, "://") {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" {
				return reqParts{}, fmt.Errorf("invalid CONNECT target %q (host:port)", raw)
			}
			target = u.Host
			if strings.EqualFold(u.Scheme, "http") {
				defPort = "80"
			}
		} else if i := strings.Index(target, "/"); i != -1 {
			target = target[:i]
		}
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			host, port = strings.Trim(target, "[]"), defPort
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || host == "" {
			return reqParts{}, fmt.Errorf("invalid CONNECT target %q (host:port)", raw)
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		return reqParts{Host: host, Port: n, Method: method, FullURL: fmt.Sprintf("%s:%d", host, n)}, nil
	}

	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return reqParts{}, fmt.Errorf("invalid URL %q", r.URL)
	}
	scheme := strings.ToLower(u.Scheme)
	port := 0
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	} else {
		port = map[string]int{"http": 80, "https": 443, "ftp": 21}[scheme]
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	full := scheme + "://" + host
	if u.Port() != "" {
		full += ":" + u.Port()
	}
	full += path
	if u.RawQuery != "" {
		full += "?" + u.RawQuery
	}
	return reqParts{Host: host, Path: path, FullURL: full, Scheme: scheme, Port: port, Method: method}, nil
}

type tri int

const (
	triNo tri = iota
	triYes
	triUnknown
)

func ipInSpec(spec string, ip net.IP) (matched, understood bool) {
	switch {
	case strings.Contains(spec, "/"):
		_, n, err := net.ParseCIDR(spec)
		if err != nil {
			return false, false
		}
		return n.Contains(ip), true
	case strings.Contains(spec, "-"):
		a, b, _ := strings.Cut(spec, "-")
		lo, hi := net.ParseIP(a), net.ParseIP(b)
		if lo == nil || hi == nil {
			return false, false
		}
		return bytesLE(lo, ip) && bytesLE(ip, hi), true
	}
	s := net.ParseIP(spec)
	if s == nil {
		return false, false // a host name: squid would resolve it
	}
	return s.Equal(ip), true
}

func bytesLE(a, b net.IP) bool {
	if a4, b4 := a.To4(), b.To4(); a4 != nil && b4 != nil {
		a, b = a4, b4
	}
	return strings.Compare(string(a), string(b)) <= 0
}

func compilePolicyRegex(pat string, ci bool) (*regexp.Regexp, error) {
	if ci {
		pat = "(?i)" + pat
	}
	return regexp.Compile(pat)
}

var weekdayLetter = map[time.Weekday]byte{
	time.Sunday: 'S', time.Monday: 'M', time.Tuesday: 'T', time.Wednesday: 'W',
	time.Thursday: 'H', time.Friday: 'F', time.Saturday: 'A',
}

func (env PolicyEnv) evalACL(a *PolicyACL, req Request, rp reqParts) (tri, string) {
	switch a.Type {
	case "all":
		return triYes, "matches everything"

	case "src":
		ip := net.ParseIP(strings.TrimSpace(req.SrcIP))
		if ip == nil {
			return triUnknown, "no valid source IP was given"
		}
		return matchIPs(a, []net.IP{ip}, "source "+ip.String())

	case "dst":
		if ip := net.ParseIP(rp.Host); ip != nil {
			return matchIPs(a, []net.IP{ip}, "destination "+ip.String())
		}
		if env.LookupIP == nil {
			return triUnknown, "destination lookup is not available"
		}
		ips, err := env.LookupIP(rp.Host)
		if err != nil || len(ips) == 0 {
			return triNo, fmt.Sprintf("%s could not be resolved", rp.Host)
		}
		return matchIPs(a, ips, "destination "+rp.Host)

	case "dstdomain":
		for _, d := range a.Args {
			d = strings.ToLower(d)
			if strings.HasPrefix(d, ".") {
				if rp.Host == d[1:] || strings.HasSuffix(rp.Host, d) {
					return triYes, fmt.Sprintf("%s is within %s", rp.Host, d)
				}
			} else if rp.Host == d {
				return triYes, fmt.Sprintf("%s equals %s", rp.Host, d)
			}
		}
		return triNo, fmt.Sprintf("%s is not in the domain list (%d entries)", rp.Host, len(a.Args))

	case "dstdom_regex":
		return matchRegexes(a, rp.Host, "host")
	case "url_regex":
		return matchRegexes(a, rp.FullURL, "URL")
	case "urlpath_regex":
		return matchRegexes(a, rp.Path, "URL path")
	case "browser":
		if req.UserAgent == "" {
			return triNo, "no User-Agent was given"
		}
		return matchRegexes(a, req.UserAgent, "User-Agent")

	case "port":
		for _, spec := range a.Args {
			lo, hi := spec, spec
			if l, h, ok := strings.Cut(spec, "-"); ok {
				lo, hi = l, h
			}
			l, e1 := strconv.Atoi(lo)
			h, e2 := strconv.Atoi(hi)
			if e1 != nil || e2 != nil {
				return triUnknown, "unreadable port list"
			}
			if rp.Port >= l && rp.Port <= h {
				return triYes, fmt.Sprintf("port %d is in %s", rp.Port, spec)
			}
		}
		return triNo, fmt.Sprintf("port %d is not listed", rp.Port)

	case "method":
		for _, m := range a.Args {
			if strings.EqualFold(m, rp.Method) {
				return triYes, "method is " + rp.Method
			}
		}
		return triNo, "method is " + rp.Method

	case "proto":
		for _, s := range a.Args {
			if strings.EqualFold(s, rp.Scheme) {
				return triYes, "protocol is " + rp.Scheme
			}
		}
		return triNo, "protocol is " + orDash(rp.Scheme)

	case "time":
		t := req.Time
		if t.IsZero() {
			t = time.Now()
		}
		day, minutes := weekdayLetter[t.Weekday()], t.Hour()*60+t.Minute()
		for _, spec := range a.Args {
			days, window, ok := strings.Cut(spec, " ")
			if !ok {
				// "time M 09:00-18:00" arrives as two args; a lone token is a day list.
				continue
			}
			if strings.IndexByte(days, day) < 0 {
				continue
			}
			s, e, ok := strings.Cut(window, "-")
			if !ok {
				continue
			}
			if minutes >= hhmm(s) && minutes <= hhmm(e) {
				return triYes, fmt.Sprintf("%s %s is within %s", t.Format("Mon"), t.Format("15:04"), spec)
			}
		}
		return triNo, fmt.Sprintf("%s %s is outside the time windows", t.Format("Mon"), t.Format("15:04"))

	case "req_header":
		// Only the header the panel's own rules use is modelled: a request has
		// Proxy-Authorization exactly when it carries a login.
		if len(a.Args) > 0 && strings.EqualFold(a.Args[0], "Proxy-Authorization") {
			if strings.TrimSpace(req.User) == "" {
				return triNo, "the request carries no login (no Proxy-Authorization header)"
			}
			return triYes, "the request carries a login"
		}
		return triUnknown, "request headers other than Proxy-Authorization are not known to the tester"

	case "proxy_auth":
		if strings.TrimSpace(req.User) == "" {
			return triUnknown, "auth-required"
		}
		for _, u := range a.Args {
			if u == "REQUIRED" {
				return triYes, "user " + req.User + " is signed in (password not checked here)"
			}
			if u == req.User {
				return triYes, "user " + req.User + " is listed"
			}
		}
		return triNo, "user " + req.User + " is not listed"
	}
	return triUnknown, fmt.Sprintf("ACL type %q cannot be evaluated by the panel", a.Type)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func hhmm(s string) int {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return -1
	}
	hi, _ := strconv.Atoi(h)
	mi, _ := strconv.Atoi(m)
	return hi*60 + mi
}

func matchIPs(a *PolicyACL, ips []net.IP, what string) (tri, string) {
	unread := false
	for _, ip := range ips {
		for _, spec := range a.Args {
			m, ok := ipInSpec(spec, ip)
			if !ok {
				unread = true
				continue
			}
			if m {
				return triYes, fmt.Sprintf("%s is in %s", what, spec)
			}
		}
	}
	if unread {
		return triUnknown, "the list contains host names the panel cannot resolve"
	}
	return triNo, fmt.Sprintf("%s is not in the list (%d entries)", what, len(a.Args))
}

func matchRegexes(a *PolicyACL, subject, what string) (tri, string) {
	for _, pat := range a.Args {
		re, err := compilePolicyRegex(pat, a.CaseInsensitive)
		if err != nil {
			return triUnknown, fmt.Sprintf("pattern %q is not readable by the panel", pat)
		}
		if re.MatchString(subject) {
			return triYes, fmt.Sprintf("%s matches %q", what, pat)
		}
	}
	return triNo, fmt.Sprintf("%s matches none of the %d patterns", what, len(a.Args))
}

// Evaluate walks the rules the way squid does: in order, all terms of a rule
// must match, the first matching rule decides. Rules it cannot judge stop the
// walk with an "unknown" verdict rather than a guess.
func (p Policy) Evaluate(req Request, env PolicyEnv) (Verdict, error) {
	rp, err := parseRequest(req)
	if err != nil {
		return Verdict{}, err
	}

	v := Verdict{Trace: []RuleTrace{}, Notes: []string{}}
	stopped := false

	for _, rule := range p.Rules {
		tr := RuleTrace{Rule: rule, Result: "not-reached", Terms: []TermResult{}}
		if stopped {
			v.Trace = append(v.Trace, tr)
			continue
		}

		tr.Result = "match"
		for i, t := range rule.Terms {
			acl, ok := p.ACLs[t.ACL]
			if !ok {
				tr.Terms = append(tr.Terms, TermResult{ACL: t.ACL, Negate: t.Negate, Result: "unknown", Detail: "this ACL is not defined"})
				tr.Result = "unknown"
				break
			}
			res, detail := env.evalACL(acl, req, rp)

			if res == triUnknown && detail == "auth-required" {
				tr.Terms = append(tr.Terms, TermResult{ACL: t.ACL, Negate: t.Negate, Result: "unknown",
					Detail: "no user was given, so squid would ask for a login (407)"})
				tr.Result = "auth-required"
				break
			}
			if res == triUnknown {
				tr.Terms = append(tr.Terms, TermResult{ACL: t.ACL, Negate: t.Negate, Result: "unknown", Detail: detail})
				tr.Result = "unknown"
				break
			}
			matched := res == triYes
			if t.Negate {
				matched = !matched
			}
			tr2 := TermResult{ACL: t.ACL, Negate: t.Negate, Result: "match", Detail: detail}
			if t.Negate {
				tr2.Detail = "NOT: " + detail
			}
			if !matched {
				tr2.Result = "no-match"
				tr.Terms = append(tr.Terms, tr2)
				tr.Result = "no-match"
				for _, rest := range rule.Terms[i+1:] {
					tr.Terms = append(tr.Terms, TermResult{ACL: rest.ACL, Negate: rest.Negate, Result: "skipped", Detail: "not checked"})
				}
				break
			}
			tr.Terms = append(tr.Terms, tr2)
		}

		v.Trace = append(v.Trace, tr)
		switch tr.Result {
		case "match":
			v.Decision, v.RuleIndex, stopped = rule.Action, rule.Index, true
		case "auth-required":
			v.Decision, v.RuleIndex, stopped = "auth_required", rule.Index, true
		case "unknown":
			v.Decision, v.RuleIndex, stopped = "unknown", rule.Index, true
			v.Notes = append(v.Notes, fmt.Sprintf("rule #%d could not be evaluated, so the outcome is unknown", rule.Index))
		}
	}

	if !stopped {
		// No rule matched: squid does the opposite of the last rule.
		v.Decision = "allow"
		if n := len(p.Rules); n > 0 && p.Rules[n-1].Action == "allow" {
			v.Decision = "deny"
		}
		v.Notes = append(v.Notes, "no rule matched; squid then does the opposite of the last rule")
	}
	if v.Decision == "allow" || v.Decision == "deny" {
		v.Notes = append(v.Notes, "the password of a proxy user is not checked, and DNS-based rules use this server's resolver")
	}
	return v, nil
}
