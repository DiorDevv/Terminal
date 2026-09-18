package squid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"example.com":              ".example.com",
		"www.example.com":          ".example.com",
		"https://example.com/path": ".example.com",
		"http://sub.example.com":   ".sub.example.com",
		"  EXAMPLE.com  ":          ".example.com",
		".already.dotted.com":      ".already.dotted.com",
	}
	for input, want := range cases {
		got, err := normalizeDomain(input)
		if err != nil {
			t.Errorf("normalizeDomain(%q) unexpected error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeDomainInvalid(t *testing.T) {
	for _, input := range []string{"", "   ", "notadomain", "https://"} {
		if _, err := normalizeDomain(input); err == nil {
			t.Errorf("normalizeDomain(%q) expected error, got none", input)
		}
	}
}

func TestBlacklistAddListRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked_sites.txt")
	bl := NewBlacklistManager(path, "blocked_sites", nil)

	if err := bl.AddDomain("example.com"); err != nil {
		t.Fatalf("AddDomain: %v", err)
	}
	if err := bl.AddDomain("example.com"); err != nil {
		t.Fatalf("AddDomain (duplicate) should be a no-op, got: %v", err)
	}
	if err := bl.AddDomain("other.com"); err != nil {
		t.Fatalf("AddDomain: %v", err)
	}

	domains, err := bl.ListDomains()
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("expected 2 domains after de-duped add, got %v", domains)
	}

	if err := bl.RemoveDomain("example.com"); err != nil {
		t.Fatalf("RemoveDomain: %v", err)
	}

	domains, err = bl.ListDomains()
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(domains) != 1 || domains[0] != ".other.com" {
		t.Fatalf("expected only .other.com to remain, got %v", domains)
	}
}

func TestEnsureDirectivesIdempotent(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "squid.conf")
	blPath := filepath.Join(dir, "blocked_sites.txt")

	initial := "acl Safe_ports port 80\nhttp_access deny !Safe_ports\nhttp_access allow localhost\nhttp_access deny all\n"
	if err := os.WriteFile(confPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial conf: %v", err)
	}

	mgr := NewManager(confPath, "true") // "true" stands in for squid -k parse, always succeeds
	bl := NewBlacklistManager(blPath, "blocked_sites", mgr)

	if err := bl.EnsureDirectives(); err != nil {
		t.Fatalf("EnsureDirectives (first call): %v", err)
	}

	content, err := mgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !strings.Contains(content, "acl blocked_sites dstdomain") {
		t.Fatalf("expected acl line to be inserted, got:\n%s", content)
	}
	if !strings.Contains(content, "http_access deny blocked_sites") {
		t.Fatalf("expected deny line to be inserted, got:\n%s", content)
	}

	// Deny line must come before the first allow, so blocked domains stay
	// blocked even for localhost.
	denyIdx := strings.Index(content, "http_access deny blocked_sites")
	allowIdx := strings.Index(content, "http_access allow localhost")
	if denyIdx == -1 || allowIdx == -1 || denyIdx > allowIdx {
		t.Fatalf("expected deny blocked_sites before allow localhost, got:\n%s", content)
	}

	firstPassContent := content

	if err := bl.EnsureDirectives(); err != nil {
		t.Fatalf("EnsureDirectives (second call): %v", err)
	}
	content, err = mgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if content != firstPassContent {
		t.Fatalf("EnsureDirectives should be a no-op on second call, config changed:\nbefore:\n%s\nafter:\n%s", firstPassContent, content)
	}
}
