package squid

import "strings"

const lanAccessDirective = "http_access allow localnet"

// IsLANAllowed reports whether squid.conf has an active (uncommented)
// "http_access allow localnet" rule, which lets other machines on the LAN
// use this proxy rather than only localhost.
func (m *Manager) IsLANAllowed() (bool, error) {
	content, err := m.ReadConfig()
	if err != nil {
		return false, err
	}

	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == lanAccessDirective {
			return true, nil
		}
	}
	return false, nil
}

// SetLANAllowed enables or disables LAN access by commenting/uncommenting
// the existing "# http_access allow localnet" line (present but commented
// out by default in stock squid.conf), or appending it before the final
// deny-all if it's missing entirely.
func (m *Manager) SetLANAllowed(allowed bool) error {
	comment := "LAN access disabled"
	if allowed {
		comment = "LAN access enabled"
	}

	return m.Update(comment, func(content string) (string, error) {
		lines := strings.Split(content, "\n")
		found := false

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == lanAccessDirective {
				found = true
				if !allowed {
					lines[i] = "# " + lanAccessDirective
				}
			} else if trimmed == "# "+lanAccessDirective || trimmed == "#"+lanAccessDirective {
				found = true
				if allowed {
					lines[i] = lanAccessDirective
				}
			}
		}

		if !found {
			if !allowed {
				return content, nil // nothing to do, already effectively disabled
			}
			lines = insertAt(lines, beforeFinalDenyAll(lines), []string{lanAccessDirective})
		}

		return strings.Join(lines, "\n"), nil
	})
}
