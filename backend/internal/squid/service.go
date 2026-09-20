package squid

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ServiceInfo describes the squid process as the service manager sees it.
type ServiceInfo struct {
	// Manager is "systemd" when squid runs as a systemd unit, "direct" when
	// the panel drives the squid binary itself (containers, no systemd).
	Manager string `json:"manager"`
	Running bool   `json:"running"`
	// State is the systemd ActiveState/SubState, e.g. "active (running)".
	State string `json:"state"`
	// Enabled reports whether squid starts at boot; nil when unknown.
	Enabled     *bool  `json:"enabled"`
	PID         int    `json:"pid"`
	MemoryBytes int64  `json:"memory_bytes"`
	StartedAt   int64  `json:"started_at"` // unix seconds, 0 when unknown
	Version     string `json:"version"`
	// RestartPending is true when squid.conf holds a change that only takes
	// effect after a restart, and squid hasn't been restarted since.
	RestartPending bool `json:"restart_pending"`
}

func (m *Manager) version() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := m.command(ctx, false, m.Bin, "-v").Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(strings.TrimPrefix(first, "Squid Cache: Version "))
}

func (m *Manager) ServiceInfo() ServiceInfo {
	info := ServiceInfo{
		Manager: "direct",
		Running: m.GetStatus().Running,
		Version: m.version(),
	}
	if info.Running {
		info.State = "running"
	} else {
		info.State = "stopped"
	}

	if !m.Systemd {
		info.RestartPending = m.restartPending(0)
		return info
	}

	info.Manager = "systemd"
	out, err := m.systemctl(false, "show", m.ServiceName, "--no-pager", "--timestamp=unix",
		"-p", "ActiveState", "-p", "SubState", "-p", "MainPID", "-p", "MemoryCurrent",
		"-p", "ActiveEnterTimestamp", "-p", "UnitFileState")
	if err != nil && out == "" {
		return info
	}

	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}

	if props["ActiveState"] != "" {
		info.State = fmt.Sprintf("%s (%s)", props["ActiveState"], props["SubState"])
	}
	info.PID, _ = strconv.Atoi(props["MainPID"])

	// systemd reports "[not set]" or uint64-max when memory accounting is off.
	if mem, err := strconv.ParseInt(props["MemoryCurrent"], 10, 64); err == nil && mem > 0 {
		info.MemoryBytes = mem
	}
	if ts := strings.TrimPrefix(props["ActiveEnterTimestamp"], "@"); ts != "" {
		info.StartedAt, _ = strconv.ParseInt(ts, 10, 64)
	}
	info.RestartPending = m.restartPending(info.StartedAt)
	switch props["UnitFileState"] {
	case "enabled", "enabled-runtime", "alias":
		t := true
		info.Enabled = &t
	case "disabled":
		f := false
		info.Enabled = &f
	}

	return info
}

// ServiceAction performs start, stop, restart, reload, enable or disable.
func (m *Manager) ServiceAction(action string) error {
	if m.Systemd {
		return m.systemdAction(action)
	}
	return m.directAction(action)
}

// activeState returns the systemd ActiveState (active, activating,
// deactivating, inactive, failed), or "" if it can't be read.
func (m *Manager) activeState() string {
	out, err := m.systemctl(false, "show", m.ServiceName, "--no-pager", "-p", "ActiveState", "--value")
	if err != nil {
		return ""
	}
	return out
}

// waitSettled polls until the unit is no longer mid-transition or the timeout
// passes, so a quick start/stop is already reflected in the response.
func (m *Manager) waitSettled(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := m.activeState(); s != "activating" && s != "deactivating" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (m *Manager) systemdAction(action string) error {
	switch action {
	case "start", "enable", "disable":
		if out, err := m.systemctl(true, action, m.ServiceName); err != nil {
			return fmt.Errorf("systemctl %s %s failed: %s", action, m.ServiceName, out)
		}
		return nil
	case "stop", "restart":
		// squid waits for open connections on shutdown (shutdown_lifetime,
		// 30s by default). Queue the job instead of holding the request open;
		// the UI polls the service state until it settles.
		if out, err := m.systemctl(true, action, "--no-block", m.ServiceName); err != nil {
			return fmt.Errorf("systemctl %s %s failed: %s", action, m.ServiceName, out)
		}
		if action == "restart" {
			m.clearRestartPending() // the restart applies whatever is on disk
		}
		m.waitSettled(3 * time.Second)
		return nil
	case "reload":
		return m.Reconfigure()
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// directAction covers environments without systemd by calling squid itself.
func (m *Manager) directAction(action string) error {
	run := func(args ...string) error {
		out, err := m.command(nil, true, m.Bin, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("squid %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
		return nil
	}

	switch action {
	case "start":
		return run()
	case "stop":
		return run("-k", "shutdown")
	case "restart":
		if m.GetStatus().Running {
			if err := run("-k", "shutdown"); err != nil {
				return err
			}
			// squid finishes open connections before exiting.
			for i := 0; i < 20 && m.GetStatus().Running; i++ {
				time.Sleep(500 * time.Millisecond)
			}
		}
		m.clearRestartPending()
		return run()
	case "reload":
		return m.Reconfigure()
	case "enable", "disable":
		return fmt.Errorf("%s at boot needs systemd, which is not available here", action)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}
