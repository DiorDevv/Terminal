// Package origin decides whether a browser Origin may talk to the API. HTTP
// middleware (CORS/CSRF) and the WebSocket upgrader share the same rule.
package origin

import (
	"net/url"
	"strings"
)

// Allowed reports whether origin is one of the configured front-end origins,
// or (same-origin deployment) has the same host the request was sent to.
func Allowed(origin string, allowed []string, requestHost string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, requestHost)
}

// ParseList splits a comma-separated list of origins, trimming blanks and
// trailing slashes (browsers send origins without one).
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimRight(strings.TrimSpace(part), "/")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
