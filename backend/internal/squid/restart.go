package squid

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RestartVerified restarts squid and confirms that it really comes back and
// stays up. Some settings (cache_dir) only take effect on a restart, and a bad
// one leaves squid unable to start at all — so unlike a plain restart this
// puts the last known-good squid.conf back, and starts squid again, when the
// new configuration doesn't work. It blocks until the outcome is known.
func (m *Manager) RestartVerified(timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.restartAndWait(timeout)
	if err == nil {
		m.pending = nil
		m.restartPendingSince.Store(0)
		return nil
	}

	if m.pending == nil {
		return err
	}
	// Whatever restart-needing change was pending is being undone.
	m.restartPendingSince.Store(0)
	if rbErr := m.rollback(); rbErr != nil {
		return fmt.Errorf("%w; automatic rollback also failed: %v", err, rbErr)
	}
	return fmt.Errorf("%w (previous config was restored automatically)", err)
}

func (m *Manager) restartAndWait(timeout time.Duration) error {
	t0 := time.Now()

	if m.Systemd {
		// --no-block: squid waits for open connections on shutdown, which we
		// wait out ourselves below rather than inside systemctl's own timeout.
		if out, err := m.systemctl(true, "restart", "--no-block", m.ServiceName); err != nil {
			return fmt.Errorf("systemctl restart %s failed: %s", m.ServiceName, out)
		}
		return m.waitForFreshStart(t0, timeout)
	}

	// Without systemd: ask squid to shut down, wait for it, start it again.
	if m.GetStatus().Running {
		if out, err := m.command(nil, true, m.Bin, "-k", "shutdown").CombinedOutput(); err != nil {
			return fmt.Errorf("squid -k shutdown failed: %s", strings.TrimSpace(string(out)))
		}
		for m.GetStatus().Running {
			if time.Since(t0) > timeout {
				return errors.New("timed out waiting for squid to shut down")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	if out, err := m.command(nil, true, m.Bin).CombinedOutput(); err != nil {
		return fmt.Errorf("squid failed to start with the new configuration: %s", strings.TrimSpace(string(out)))
	}
	if m.VerifyDelay > 0 && !m.survives(m.VerifyDelay) {
		return errors.New("squid stopped right after starting")
	}
	return nil
}

// waitForFreshStart follows the unit through stop -> start after a queued
// restart. "Active" only counts once its start time is newer than the restart
// request; before that it is still the old process waiting to be stopped.
func (m *Manager) waitForFreshStart(t0 time.Time, timeout time.Duration) error {
	var inactiveSince time.Time

	for time.Since(t0) < timeout {
		switch m.activeState() {
		case "active":
			inactiveSince = time.Time{}
			if m.startedAt() >= t0.Unix() {
				if m.VerifyDelay > 0 && !m.survives(m.VerifyDelay) {
					return errors.New("squid stopped right after restarting")
				}
				return nil
			}
		case "failed":
			return errors.New("squid failed to start with the new configuration")
		case "inactive":
			// Briefly normal between the stop and the start; not for long.
			if inactiveSince.IsZero() {
				inactiveSince = time.Now()
			} else if time.Since(inactiveSince) > 8*time.Second {
				return errors.New("squid did not come back up after the restart")
			}
		default: // activating, deactivating, reloading
			inactiveSince = time.Time{}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("timed out waiting for squid to restart")
}

// startedAt is when the unit last became active, in unix seconds (0 unknown).
func (m *Manager) startedAt() int64 {
	out, err := m.systemctl(false, "show", m.ServiceName, "--no-pager", "--timestamp=unix",
		"-p", "ActiveEnterTimestamp", "--value")
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(out), "@"), 10, 64)
	return n
}
