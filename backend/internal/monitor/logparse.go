// Package monitor turns squid's access.log into statistics and watches the
// proxy's health: it reads the log as it grows, keeps hourly aggregates, reads
// squid's cache manager, reports disk usage, and raises alerts.
package monitor

import (
	"strconv"
	"strings"
)

// Entry is one request line of squid's default ("native") access.log format:
//
//	time.ms  elapsed  client  RESULT/status  bytes  method  URL  user  hierarchy/peer  mime
type Entry struct {
	Time   int64 // unix seconds
	Client string
	Code   string // TCP_MISS, TCP_DENIED, TCP_MEM_HIT, ...
	Status int    // HTTP status squid answered with
	Bytes  int64  // reply size
	Method string
	URL    string
	User   string // "" when the request was anonymous
	Domain string // host part of the URL ("-" if none)
}

// ParseLine parses one native-format line. It returns false for lines that are
// not request lines (blank, truncated, or another log format).
func ParseLine(line string) (Entry, bool) {
	f := strings.Fields(line)
	if len(f) < 10 {
		return Entry{}, false
	}

	secs, _, _ := strings.Cut(f[0], ".")
	ts, err := strconv.ParseInt(secs, 10, 64)
	if err != nil || ts <= 0 {
		return Entry{}, false
	}
	code, statusStr, ok := strings.Cut(f[3], "/")
	if !ok {
		return Entry{}, false
	}
	status, err := strconv.Atoi(statusStr)
	if err != nil {
		return Entry{}, false
	}
	bytes, err := strconv.ParseInt(f[4], 10, 64)
	if err != nil || bytes < 0 {
		return Entry{}, false
	}

	e := Entry{
		Time: ts, Client: f[2], Code: code, Status: status, Bytes: bytes,
		Method: f[5], URL: f[6], User: f[7],
	}
	if e.User == "-" {
		e.User = ""
	}
	e.Domain = domainOf(e.Method, e.URL)
	return e, true
}

// domainOf extracts the host from the request URL. For CONNECT (HTTPS
// tunnels) the URL is just "host:port".
func domainOf(method, rawURL string) string {
	u := rawURL
	if i := strings.Index(u, "://"); i != -1 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?#"); i != -1 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "@"); i != -1 {
		u = u[i+1:]
	}

	switch {
	case strings.HasPrefix(u, "["): // [ipv6]:port
		if i := strings.Index(u, "]"); i != -1 {
			u = u[1:i]
		}
	case strings.Count(u, ":") == 1:
		u, _, _ = strings.Cut(u, ":")
	}
	u = strings.ToLower(strings.TrimSuffix(u, "."))
	if u == "" || u == "-" {
		return "-"
	}
	return u
}

// Hit reports whether the reply came from squid's cache.
func (e Entry) Hit() bool { return strings.Contains(e.Code, "HIT") }

// Denied reports whether squid refused the request by an access rule.
func (e Entry) Denied() bool { return e.Status == 403 && strings.HasPrefix(e.Code, "TCP_DENIED") }

// Challenge reports a 407 login challenge. Squid logs one for the first,
// credential-less attempt of every request; the client then repeats it with
// credentials. Counting both would double every authenticated request.
func (e Entry) Challenge() bool { return e.Status == 407 }

// Failed reports a server-side failure (5xx) that was not an access denial.
func (e Entry) Failed() bool { return e.Status >= 500 && !e.Denied() }
