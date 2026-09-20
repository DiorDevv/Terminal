package squid

import (
	"context"
	"strings"
	"time"
)

type Status struct {
	Running bool   `json:"running"`
	Detail  string `json:"detail"`
}

// GetStatus reports whether squid is running.
//
// Under systemd it asks the service manager, which any user may do, so the
// panel needs no privileges to show status. Otherwise it uses
// `squid -k check`, which signals the running process to verify itself
// rather than grepping ps output.
func (m *Manager) GetStatus() Status {
	if m.Systemd {
		state, _ := m.systemctl(false, "is-active", m.ServiceName)
		// A squid that is still shutting down (open connections) is alive.
		switch state {
		case "active", "reloading", "deactivating":
			return Status{Running: true, Detail: "squid is running"}
		}
		return Status{Running: false, Detail: "squid is " + firstLine(state)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := m.command(ctx, true, m.Bin, "-k", "check").CombinedOutput()
	if err != nil {
		return Status{Running: false, Detail: strings.TrimSpace(string(out))}
	}
	return Status{Running: true, Detail: "squid is running"}
}

func firstLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if first == "" {
		return "not running"
	}
	return first
}
