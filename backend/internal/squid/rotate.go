package squid

import (
	"fmt"
	"strings"
)

// RotateLogs asks squid to close and reopen its log files (`squid -k rotate`).
// With logfile_rotate above zero squid also renames the old files to
// access.log.0, .1, ...; with zero — Debian's default, where logrotate does the
// renaming — it only reopens them, which is what makes an external rename safe.
func (m *Manager) RotateLogs() error {
	if !m.GetStatus().Running {
		return fmt.Errorf("squid is not running")
	}
	if out, err := m.command(nil, true, m.Bin, "-k", "rotate").CombinedOutput(); err != nil {
		return fmt.Errorf("squid -k rotate failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
