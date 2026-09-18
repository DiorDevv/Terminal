package squid

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

type Status struct {
	Running bool   `json:"running"`
	Detail  string `json:"detail"`
}

// GetStatus checks whether squid is running via `squid -k check`, which
// asks the running process to verify itself rather than grepping ps output.
func (m *Manager) GetStatus() Status {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.Bin, "-k", "check")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Status{Running: false, Detail: strings.TrimSpace(string(out))}
	}
	return Status{Running: true, Detail: "squid is running"}
}
