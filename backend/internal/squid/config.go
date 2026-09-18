package squid

import (
	"fmt"
	"os"
	"os/exec"
)

type Manager struct {
	ConfPath string
	Bin      string
}

func NewManager(confPath, bin string) *Manager {
	return &Manager{ConfPath: confPath, Bin: bin}
}

func (m *Manager) ReadConfig() (string, error) {
	data, err := os.ReadFile(m.ConfPath)
	if err != nil {
		return "", fmt.Errorf("read squid.conf: %w", err)
	}
	return string(data), nil
}

// WriteConfig validates the new config against a temp file before
// overwriting the real one, so a bad edit never takes squid down.
func (m *Manager) WriteConfig(content string) error {
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

	if err := m.validate(tmp.Name()); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	if err := os.WriteFile(m.ConfPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write squid.conf: %w", err)
	}

	return nil
}

func (m *Manager) validate(path string) error {
	cmd := exec.Command(m.Bin, "-k", "parse", "-f", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", string(out))
	}
	return nil
}

func (m *Manager) Reconfigure() error {
	cmd := exec.Command(m.Bin, "-k", "reconfigure")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reconfigure failed: %s", string(out))
	}
	return nil
}
