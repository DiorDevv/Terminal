package squid

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// BlacklistManager manages the plain-text domain list that squid.conf's
// blocked_sites ACL (dstdomain) reads from, plus the acl/http_access
// directives in squid.conf that wire it up.
type BlacklistManager struct {
	Path    string
	ACLName string
	confMgr *Manager

	// mu serialises read-modify-write of the list file.
	mu sync.Mutex
}

func NewBlacklistManager(path, aclName string, confMgr *Manager) *BlacklistManager {
	return &BlacklistManager{Path: path, ACLName: aclName, confMgr: confMgr}
}

// normalizeDomain accepts input like "example.com", "www.example.com" or
// "https://example.com/path" and returns Squid's dstdomain leading-dot form
// (".example.com"), which matches the domain and all its subdomains.
func normalizeDomain(input string) (string, error) {
	d := strings.TrimSpace(input)
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	if i := strings.IndexAny(d, "/?#"); i != -1 {
		d = d[:i]
	}
	d = strings.TrimPrefix(d, "www.")
	d = strings.ToLower(strings.Trim(d, "."))

	if d == "" || !strings.Contains(d, ".") {
		return "", fmt.Errorf("invalid domain: %q", input)
	}

	return "." + d, nil
}

func (b *BlacklistManager) ListDomains() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.listDomains()
}

func (b *BlacklistManager) listDomains() ([]string, error) {
	f, err := os.Open(b.Path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open blacklist: %w", err)
	}
	defer f.Close()

	domains := []string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domains = append(domains, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read blacklist: %w", err)
	}

	return domains, nil
}

func (b *BlacklistManager) AddDomain(input string) error {
	domain, err := normalizeDomain(input)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	existing, err := b.listDomains()
	if err != nil {
		return err
	}
	for _, d := range existing {
		if d == domain {
			return nil // already blocked
		}
	}

	f, err := os.OpenFile(b.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open blacklist for append: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, domain); err != nil {
		return fmt.Errorf("write blacklist: %w", err)
	}

	return nil
}

func (b *BlacklistManager) RemoveDomain(input string) error {
	domain, err := normalizeDomain(input)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	existing, err := b.listDomains()
	if err != nil {
		return err
	}

	kept := existing[:0]
	for _, d := range existing {
		if d != domain {
			kept = append(kept, d)
		}
	}

	content := strings.Join(kept, "\n")
	if len(kept) > 0 {
		content += "\n"
	}

	if err := writeFileAtomic(b.Path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write blacklist: %w", err)
	}

	return nil
}

// EnsureDirectives makes sure squid.conf declares the blocked_sites ACL and
// denies access to it. It's a best-effort call: on a fresh Squid install
// these directives won't exist yet, but on a system where they're already
// present (as on this machine) it's a no-op. Failures (e.g. missing write
// permission) are returned so the caller can log and continue rather than
// crash startup.
func (b *BlacklistManager) EnsureDirectives() error {
	aclLine := fmt.Sprintf(`acl %s dstdomain "%s"`, b.ACLName, b.Path)
	denyLine := fmt.Sprintf("http_access deny %s", b.ACLName)

	return b.confMgr.Update("blacklist directives added", func(content string) (string, error) {
		hasACL := strings.Contains(content, aclLine)
		hasDeny := strings.Contains(content, denyLine)
		if hasACL && hasDeny {
			return content, nil
		}

		var block []string
		if !hasACL {
			block = append(block, aclLine)
		}
		if !hasDeny {
			block = append(block, denyLine)
		}

		lines := strings.Split(content, "\n")
		lines = insertAt(lines, denyStageAnchor(lines), block)
		return strings.Join(lines, "\n"), nil
	})
}
