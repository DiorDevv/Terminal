package squid

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Settings returns every managed setting with its current values.
func (m *Manager) Settings() ([]SettingValue, error) {
	content, err := m.ReadConfig()
	if err != nil {
		return nil, err
	}
	return effectiveSettings(content), nil
}

// PreviewSettings validates an update and reports the exact lines it would
// change, without touching squid.conf. squid's own parser has the final say,
// so a value that passes the panel's checks but not squid's is caught here too.
func (m *Manager) PreviewSettings(u SettingsUpdate) (SettingsResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.ReadConfig()
	if err != nil {
		return SettingsResult{}, err
	}
	next, res, err := applySettings(current, u)
	if err != nil {
		return SettingsResult{}, err
	}
	if next != current {
		if err := m.validateContent(next); err != nil {
			return SettingsResult{}, fmt.Errorf("config validation failed: %w", err)
		}
	}
	return res, nil
}

// ApplySettings writes an update into squid.conf (validated, snapshotted to the
// history like every other change). It does not reload squid: the caller does
// that, unless the result says a restart is needed, in which case the change is
// remembered as pending until squid is restarted.
func (m *Manager) ApplySettings(u SettingsUpdate) (SettingsResult, error) {
	var res SettingsResult
	err := m.Update(settingsComment(u), func(current string) (string, error) {
		next, r, err := applySettings(current, u)
		res = r
		return next, err
	})
	if err != nil {
		return SettingsResult{}, err
	}
	if res.RestartRequired {
		m.restartPendingSince.Store(time.Now().UnixNano())
	}
	return res, nil
}

func settingsComment(u SettingsUpdate) string {
	var keys []string
	for k := range u.Values {
		keys = append(keys, k)
	}
	for _, k := range u.Reset {
		keys = append(keys, k+" (reset)")
	}
	sort.Strings(keys)
	return "settings: " + strings.Join(keys, ", ")
}

// restartPending reports whether squid.conf holds a change that needs a
// restart and squid hasn't been restarted since. startedAt is squid's start
// time in unix seconds (0 when unknown).
func (m *Manager) restartPending(startedAt int64) bool {
	since := m.restartPendingSince.Load()
	if since == 0 {
		return false
	}
	// systemd reports whole seconds, so a start in the same second as the
	// change is indistinguishable from one just before it: count it as older.
	// (Restarts that follow a change clear the flag explicitly.)
	return startedAt == 0 || startedAt <= since/int64(time.Second)
}

// clearRestartPending is called when squid is restarted, which applies
// whatever configuration is on disk.
func (m *Manager) clearRestartPending() {
	m.restartPendingSince.Store(0)
}
