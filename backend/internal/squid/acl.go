package squid

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// ACL objects: a named set of values of one type (domains, IPs, ports, ...).
// Access rules combine them; squid.conf is generated from both (access.go).

// ACLTypeInfo describes one supported ACL type for the UI.
type ACLTypeInfo struct {
	Type string `json:"type"`
	// Regex types match a pattern; they honour the case-insensitive flag.
	Regex bool `json:"regex"`
	// Managed types are created by the panel itself (blocklist sources).
	Managed bool `json:"managed"`
}

var aclTypeOrder = []ACLTypeInfo{
	{Type: "src"}, {Type: "dst"}, {Type: "dstdomain"},
	{Type: "dstdom_regex", Regex: true}, {Type: "url_regex", Regex: true},
	{Type: "urlpath_regex", Regex: true}, {Type: "browser", Regex: true},
	{Type: "port"}, {Type: "method"}, {Type: "time"}, {Type: "proxy_auth"},
	{Type: "blocklist", Managed: true},
}

func ACLTypes() []ACLTypeInfo { return aclTypeOrder }

func aclTypeInfo(t string) (ACLTypeInfo, bool) {
	for _, i := range aclTypeOrder {
		if i.Type == t {
			return i, true
		}
	}
	return ACLTypeInfo{}, false
}

// ACLObject is one named ACL.
type ACLObject struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Values          []string `json:"values"`
	CaseInsensitive bool     `json:"case_insensitive"`
	Description     string   `json:"description"`
	// UsedBy counts the rules that reference it (filled in by List).
	UsedBy int `json:"used_by"`
}

var (
	aclNamePattern = regexp.MustCompile(`^[a-z0-9_-]{3,40}$`)
	portSpecRe     = regexp.MustCompile(`^(\d{1,5})(?:-(\d{1,5}))?$`)
	timeSpecRe     = regexp.MustCompile(`^([SMTWHFA]{1,7}) ([01]\d|2[0-3]):([0-5]\d)-([01]\d|2[0-3]):([0-5]\d)$`)
	noSpaceRe      = regexp.MustCompile(`^\S+$`)
)

var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "HEAD": true, "CONNECT": true,
	"OPTIONS": true, "PATCH": true, "TRACE": true, "PROPFIND": true, "PROPPATCH": true,
	"MKCOL": true, "COPY": true, "MOVE": true, "LOCK": true, "UNLOCK": true,
}

func normalizeIPSpec(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.Contains(s, "/"):
		if _, _, err := net.ParseCIDR(s); err != nil {
			return "", fmt.Errorf("invalid network %q (use address/prefix, e.g. 10.0.0.0/8)", s)
		}
		return s, nil
	case strings.Contains(s, "-"):
		a, b, _ := strings.Cut(s, "-")
		ia, ib := net.ParseIP(strings.TrimSpace(a)), net.ParseIP(strings.TrimSpace(b))
		if ia == nil || ib == nil || (ia.To4() == nil) != (ib.To4() == nil) {
			return "", fmt.Errorf("invalid address range %q", s)
		}
		return ia.String() + "-" + ib.String(), nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", fmt.Errorf("%q is not an IP address, network or range", s)
	}
	return ip.String(), nil
}

func normalizeACLDomain(s string) (string, error) {
	d, err := normalizeDomain(s)
	if err != nil {
		return "", err
	}
	if err := validHost(strings.TrimPrefix(d, ".")); err != nil {
		return "", err
	}
	return d, nil
}

func normalizePortSpec(s string) (string, error) {
	m := portSpecRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", fmt.Errorf("invalid port %q (use 80 or 8000-8100)", s)
	}
	lo, _ := strconv.Atoi(m[1])
	if lo < 1 || lo > 65535 {
		return "", fmt.Errorf("port %d out of range", lo)
	}
	if m[2] == "" {
		return m[1], nil
	}
	hi, _ := strconv.Atoi(m[2])
	if hi < lo || hi > 65535 {
		return "", fmt.Errorf("invalid port range %q", s)
	}
	return m[1] + "-" + m[2], nil
}

func normalizeMethod(s string) (string, error) {
	m := strings.ToUpper(strings.TrimSpace(s))
	if !httpMethods[m] {
		return "", fmt.Errorf("unknown HTTP method %q", s)
	}
	return m, nil
}

func normalizeTimeSpec(s string) (string, error) {
	s = strings.Join(strings.Fields(strings.ToUpper(s)), " ")
	m := timeSpecRe.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("invalid time %q (days S M T W H F A, then HH:MM-HH:MM, e.g. MTWHF 09:00-18:00)", s)
	}
	seen := map[rune]bool{}
	for _, d := range m[1] {
		if seen[d] {
			return "", fmt.Errorf("day %c is listed twice", d)
		}
		seen[d] = true
	}
	if m[2]+m[3] >= m[4]+m[5] {
		return "", fmt.Errorf("the start time must be before the end time (a window across midnight needs two entries)")
	}
	return s, nil
}

// normalizeRegex checks a pattern squid (POSIX extended) and the panel can both
// digest. squid's own parser has the final word when the config is written.
func normalizeRegex(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || len(s) > 200:
		return "", fmt.Errorf("a pattern must be 1-200 characters")
	case !noSpaceRe.MatchString(s) || strings.ContainsAny(s, `"'`):
		return "", fmt.Errorf("a pattern cannot contain spaces or quotes")
	case strings.Contains(s, "(?"):
		return "", fmt.Errorf("lookaheads and named groups are not supported (POSIX regular expressions)")
	case strings.Contains(s, `\d`) || strings.Contains(s, `\D`):
		return "", fmt.Errorf(`\d is not supported by squid; use [0-9]`)
	}
	if _, err := regexp.Compile(s); err != nil {
		return "", fmt.Errorf("invalid regular expression: %v", err)
	}
	return s, nil
}

func normalizeProxyAuthUser(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "REQUIRED" {
		return s, nil
	}
	if err := validateUsername(s); err != nil {
		return "", err
	}
	return s, nil
}

// normalizeACLValues validates and canonicalises the values of an ACL.
func normalizeACLValues(typ string, in []string) ([]string, error) {
	info, ok := aclTypeInfo(typ)
	if !ok {
		return nil, fmt.Errorf("unknown ACL type %q", typ)
	}
	if info.Managed {
		return nil, fmt.Errorf("%s ACLs are created by the panel", typ)
	}

	var norm func(string) (string, error)
	switch typ {
	case "src", "dst":
		norm = normalizeIPSpec
	case "dstdomain":
		norm = normalizeACLDomain
	case "dstdom_regex", "url_regex", "urlpath_regex", "browser":
		norm = normalizeRegex
	case "port":
		norm = normalizePortSpec
	case "method":
		norm = normalizeMethod
	case "time":
		norm = normalizeTimeSpec
	case "proxy_auth":
		norm = normalizeProxyAuthUser
	}

	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		v, err := norm(raw)
		if err != nil {
			return nil, err
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one value is required")
	}
	if len(out) > 5000 {
		return nil, fmt.Errorf("too many values (max 5000); use a block list source for large lists")
	}
	if typ == "proxy_auth" && seen["REQUIRED"] && len(out) > 1 {
		return nil, fmt.Errorf("REQUIRED (any signed-in user) cannot be combined with user names")
	}
	return out, nil
}

// sqaACLName is the name an ACL object gets in squid.conf. The prefix keeps
// panel ACLs from ever colliding with the stock ones (localnet, Safe_ports...).
func sqaACLName(name string) string { return "sqa_" + name }

// renderACL produces the acl line(s) for one object. blocklistPath resolves
// the list file of a blocklist ACL.
func renderACL(a ACLObject, blocklistPath func(id string) string) []string {
	name := sqaACLName(a.Name)

	if a.Type == "blocklist" {
		return []string{fmt.Sprintf(`acl %s dstdomain "%s"`, name, blocklistPath(a.Values[0]))}
	}

	flag := ""
	if info, _ := aclTypeInfo(a.Type); info.Regex && a.CaseInsensitive {
		flag = " -i"
	}

	// squid's time ACL takes one window per line; the others take many values.
	perLine := 25
	if a.Type == "time" {
		perLine = 1
	}
	var lines []string
	for i := 0; i < len(a.Values); i += perLine {
		end := i + perLine
		if end > len(a.Values) {
			end = len(a.Values)
		}
		lines = append(lines, fmt.Sprintf("acl %s %s%s %s", name, a.Type, flag, strings.Join(a.Values[i:end], " ")))
	}
	return lines
}
