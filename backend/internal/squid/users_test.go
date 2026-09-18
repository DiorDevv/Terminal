package squid

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	valid := []string{"admin", "jamshid", "user.name", "user-name", "user_1"}
	for _, u := range valid {
		if err := validateUsername(u); err != nil {
			t.Errorf("validateUsername(%q) unexpected error: %v", u, err)
		}
	}

	invalid := []string{"", "ab", strings.Repeat("a", 33), "user name", "user:name", "user/name"}
	for _, u := range invalid {
		if err := validateUsername(u); err == nil {
			t.Errorf("validateUsername(%q) expected error, got none", u)
		}
	}
}

func hasHtpasswd(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("htpasswd"); err != nil {
		t.Skip("htpasswd not available in test environment")
	}
}

func TestUserAddListRemove(t *testing.T) {
	hasHtpasswd(t)

	dir := t.TempDir()
	passwdPath := filepath.Join(dir, "passwd")
	um := NewUserManager(passwdPath, "authenticated_users", "/usr/lib/squid/basic_ncsa_auth", "htpasswd", nil)

	if err := um.AddUser("alice", "hunter22"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := um.AddUser("bob", "hunter33"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	users, err := um.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %v", users)
	}

	if err := um.RemoveUser("alice"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}

	users, err = um.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0] != "bob" {
		t.Fatalf("expected only bob to remain, got %v", users)
	}
}

func TestEnsureAuthDirectivesIdempotent(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "squid.conf")
	passwdPath := filepath.Join(dir, "passwd")

	initial := "acl Safe_ports port 80\nhttp_access deny !Safe_ports\nhttp_access allow localhost\nhttp_access deny all\n"
	if err := os.WriteFile(confPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial conf: %v", err)
	}

	mgr := NewManager(confPath, "true")
	um := NewUserManager(passwdPath, "authenticated_users", "/usr/lib/squid/basic_ncsa_auth", "htpasswd", mgr)

	if err := um.EnsureAuthDirectives(); err != nil {
		t.Fatalf("EnsureAuthDirectives (first call): %v", err)
	}

	content, err := mgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !strings.Contains(content, "acl authenticated_users proxy_auth REQUIRED") {
		t.Fatalf("expected acl line to be inserted, got:\n%s", content)
	}

	denyAllIdx := strings.LastIndex(content, "http_access deny all")
	allowAuthIdx := strings.Index(content, "http_access allow authenticated_users")
	if allowAuthIdx == -1 || denyAllIdx == -1 || allowAuthIdx > denyAllIdx {
		t.Fatalf("expected allow authenticated_users before deny all, got:\n%s", content)
	}

	firstPassContent := content

	if err := um.EnsureAuthDirectives(); err != nil {
		t.Fatalf("EnsureAuthDirectives (second call): %v", err)
	}
	content, err = mgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if content != firstPassContent {
		t.Fatalf("EnsureAuthDirectives should be a no-op on second call, config changed:\nbefore:\n%s\nafter:\n%s", firstPassContent, content)
	}
}
