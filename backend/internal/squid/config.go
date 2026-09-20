package squid

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type Manager struct {
	ConfPath string
	Bin      string

	// ServiceName / SystemctlBin identify the systemd unit used by the
	// service-control functions (see service.go).
	ServiceName  string
	SystemctlBin string

	// Systemd selects systemctl for status and control; false drives the
	// squid binary directly (containers, non-systemd hosts).
	Systemd bool
	// Sudo runs the privileged commands through `sudo -n`, letting the panel
	// itself run as an unprivileged user.
	Sudo bool

	// VerifyDelay is how long Reconfigure watches squid after reloading a
	// changed config to make sure it stays up. Zero disables it (tests).
	VerifyDelay time.Duration

	// mu serialises every read-modify-write of squid.conf so two requests
	// can never overwrite each other's change.
	mu      sync.Mutex
	history History

	// restartPendingSince is when a change needing a restart (e.g. cache_dir)
	// was written, as unix nanoseconds; 0 when there is none. Squid counts as
	// up to date once it has been started after that moment.
	restartPendingSince atomic.Int64

	// pending holds squid.conf as it was before the oldest change that has
	// not yet been successfully reloaded. If a reload then fails, Reconfigure
	// restores it. nil means there is nothing to roll back to.
	pending *string
}

func NewManager(confPath, bin string) *Manager {
	return &Manager{
		ConfPath:     confPath,
		Bin:          bin,
		ServiceName:  "squid",
		SystemctlBin: "systemctl",
	}
}

// SetHistory enables config snapshots. Without it changes are still applied
// safely, they just can't be reviewed or restored later.
func (m *Manager) SetHistory(h History) {
	m.history = h
}

func (m *Manager) History() History {
	return m.history
}

// EnsureBaseline stores the current squid.conf as the first history entry if
// the history is empty, so the very first panel change can be rolled back too.
func (m *Manager) EnsureBaseline() error {
	if m.history == nil {
		return nil
	}
	existing, err := m.history.List(1)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	content, err := m.ReadConfig()
	if err != nil {
		return err
	}
	return m.history.Save("initial state (before first panel change)", content)
}

func (m *Manager) ReadConfig() (string, error) {
	data, err := os.ReadFile(m.ConfPath)
	if err != nil {
		return "", fmt.Errorf("read squid.conf: %w", err)
	}
	return string(data), nil
}

// Update is the single entry point for changing squid.conf. Under one lock it
// reads the current content, lets fn produce the new content, validates it
// with `squid -k parse` against a temp file, and only then atomically replaces
// the real file — so a bad edit can never take squid down and concurrent
// edits can't clobber each other. If fn returns the content unchanged nothing
// is written. Changes are snapshotted to the history under comment.
func (m *Manager) Update(comment string, fn func(current string) (string, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.ReadConfig()
	if err != nil {
		return err
	}

	next, err := fn(current)
	if err != nil {
		return err
	}
	if next == current {
		return nil
	}

	if err := m.validateContent(next); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if err := writeFileAtomic(m.ConfPath, []byte(next), 0o644); err != nil {
		return fmt.Errorf("write squid.conf: %w", err)
	}

	if m.pending == nil {
		m.pending = &current
	}
	if m.history != nil {
		if err := m.history.Save(comment, next); err != nil {
			log.Printf("warning: could not save config history: %v", err)
		}
	}
	return nil
}

// WriteConfig replaces squid.conf with content (the raw config editor).
func (m *Manager) WriteConfig(content string) error {
	return m.Update("manual edit in config editor", func(string) (string, error) {
		return content, nil
	})
}

// Restore writes a previously saved version back as the current config.
func (m *Manager) Restore(id int64) error {
	if m.history == nil {
		return errors.New("config history is not enabled")
	}
	v, err := m.history.Get(id)
	if err != nil {
		return fmt.Errorf("version %d not found", id)
	}
	return m.Update(fmt.Sprintf("restored version #%d", id), func(string) (string, error) {
		return v.Content, nil
	})
}

func (m *Manager) validateContent(content string) error {
	tmp, err := os.CreateTemp("", "squid-*.conf")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmp.Close()

	// Parsing needs no privileges, so it never goes through sudo.
	cmd := m.command(nil, false, m.Bin, "-k", "parse", "-f", tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", string(out))
	}
	return nil
}

func (m *Manager) runReconfigure() error {
	var out []byte
	var err error
	if m.Systemd {
		// The unit's ExecReload signals squid; going through systemctl keeps
		// the sudoers rule to one fixed command.
		var s string
		s, err = m.systemctl(true, "reload", m.ServiceName)
		out = []byte(s)
	} else {
		out, err = m.command(nil, true, m.Bin, "-k", "reconfigure").CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf("reconfigure failed: %s", string(out))
	}
	return nil
}

// Reconfigure makes the running squid pick up the current squid.conf and then
// verifies squid is still alive. If squid was running before and the reload
// fails or squid dies, the last known-good config is restored automatically
// (and squid is started again if needed), so a bad change can't leave the
// proxy down. The returned error says whether a rollback happened.
func (m *Manager) Reconfigure() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	wasRunning := m.GetStatus().Running

	err := m.runReconfigure()
	// A config that parses can still be fatal at runtime (unusable cache_dir,
	// port already in use, ...): squid then dies a moment after the reload
	// signal, which `-k reconfigure` doesn't report. Watch for that — but only
	// when squid.conf actually changed, since otherwise there is nothing to
	// roll back to and no reason to make the caller wait.
	if err == nil && wasRunning && m.pending != nil && m.VerifyDelay > 0 {
		if !m.survives(m.VerifyDelay) {
			err = errors.New("squid stopped after reload")
		}
	}

	if err == nil {
		m.pending = nil
		return nil
	}

	// Without a running squid there is nothing to protect: a failed reload
	// just means "not running", not "the new config is bad".
	if !wasRunning || m.pending == nil {
		return err
	}

	if rbErr := m.rollback(); rbErr != nil {
		return fmt.Errorf("%w; automatic rollback also failed: %v", err, rbErr)
	}
	return fmt.Errorf("%w (previous config was restored automatically)", err)
}

// survives reports whether squid stays up for the whole window, returning
// early as soon as it is seen down. On a typical machine squid dies about
// 1.2s after loading a config it cannot run, so the window must comfortably
// exceed that.
func (m *Manager) survives(window time.Duration) bool {
	deadline := time.Now().Add(window)
	for {
		if !m.GetStatus().Running {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// rollback restores the pre-change config and brings squid back up. Caller
// must hold m.mu.
func (m *Manager) rollback() error {
	if err := writeFileAtomic(m.ConfPath, []byte(*m.pending), 0o644); err != nil {
		return fmt.Errorf("restore squid.conf: %w", err)
	}
	if m.history != nil {
		if err := m.history.Save("automatic rollback (reload failed)", *m.pending); err != nil {
			log.Printf("warning: could not save rollback to history: %v", err)
		}
	}
	m.pending = nil

	if m.GetStatus().Running {
		return m.runReconfigure()
	}
	return m.ServiceAction("start")
}
