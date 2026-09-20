package ws

import (
	"net/http"
	"testing"
)

func TestCheckOrigin(t *testing.T) {
	up := NewUpgrader([]string{"http://localhost:5173"})

	cases := []struct {
		name, origin, host string
		want               bool
	}{
		{"configured front-end", "http://localhost:5173", "localhost:8080", true},
		{"same host (single-origin deployment)", "https://panel.example.com", "panel.example.com", true},
		{"foreign site", "https://evil.example", "localhost:8080", false},
		{"look-alike host", "http://localhost:5173.evil.example", "localhost:8080", false},
		{"different port", "http://localhost:9999", "localhost:8080", false},
		{"no Origin (non-browser client)", "", "localhost:8080", true},
	}
	for _, c := range cases {
		r := &http.Request{Header: http.Header{}, Host: c.host}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := up.CheckOrigin(r); got != c.want {
			t.Errorf("%s: CheckOrigin(%q) = %v, want %v", c.name, c.origin, got, c.want)
		}
	}
}
