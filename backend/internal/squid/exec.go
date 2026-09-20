package squid

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SystemdAvailable reports whether systemd is the running init and the
// systemctl binary is on the PATH.
func SystemdAvailable(systemctlBin string) bool {
	if _, err := exec.LookPath(systemctlBin); err != nil {
		return false
	}
	// /run/systemd/system exists only when systemd is the running init.
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// command builds an exec.Cmd. Privileged commands (signalling squid, starting
// and stopping the service) go through `sudo -n` when Sudo is on, so the panel
// can run as an unprivileged user with a narrow sudoers rule. Everything else
// (parsing a config, reading unit state, `squid -v`) never needs root.
func (m *Manager) command(ctx context.Context, privileged bool, name string, args ...string) *exec.Cmd {
	if privileged && m.Sudo {
		// sudoers rules match the full path, so resolve it first.
		if path, err := exec.LookPath(name); err == nil {
			name = path
		}
		args = append([]string{"-n", name}, args...)
		name = "sudo"
	}
	if ctx == nil {
		return exec.Command(name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

// systemctl runs systemctl with a timeout and returns its trimmed output.
func (m *Manager) systemctl(privileged bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := m.command(ctx, privileged, m.SystemctlBin, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
