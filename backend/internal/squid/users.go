package squid

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// UserManager manages Squid proxy users: an htpasswd-format credentials
// file, plus the squid.conf directives (auth_param + ACL) that make Squid
// require and check that authentication.
type UserManager struct {
	PasswdPath  string
	ACLName     string
	HelperPath  string
	HtpasswdBin string
	confMgr     *Manager
}

func NewUserManager(passwdPath, aclName, helperPath, htpasswdBin string, confMgr *Manager) *UserManager {
	return &UserManager{
		PasswdPath:  passwdPath,
		ACLName:     aclName,
		HelperPath:  helperPath,
		HtpasswdBin: htpasswdBin,
		confMgr:     confMgr,
	}
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return fmt.Errorf("invalid username: only letters, digits, '.', '_', '-' allowed, 3-32 chars")
	}
	return nil
}

func (u *UserManager) ListUsers() ([]string, error) {
	f, err := os.Open(u.PasswdPath)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open passwd file: %w", err)
	}
	defer f.Close()

	var users []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if name, _, ok := strings.Cut(line, ":"); ok {
			users = append(users, name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read passwd file: %w", err)
	}

	return users, nil
}

// AddUser creates or updates a proxy user's password via htpasswd, which
// owns the file's hashing format so we never handle raw password hashing
// ourselves.
func (u *UserManager) AddUser(username, password string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if len(password) < 4 {
		return fmt.Errorf("password must be at least 4 characters")
	}

	args := []string{"-b"}
	if _, err := os.Stat(u.PasswdPath); os.IsNotExist(err) {
		args = append(args, "-c")
	}
	args = append(args, u.PasswdPath, username, password)

	cmd := exec.Command(u.HtpasswdBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("htpasswd failed: %s", string(out))
	}

	return nil
}

func (u *UserManager) RemoveUser(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}

	if _, err := os.Stat(u.PasswdPath); os.IsNotExist(err) {
		return nil
	}

	cmd := exec.Command(u.HtpasswdBin, "-D", u.PasswdPath, username)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("htpasswd failed: %s", string(out))
	}

	return nil
}

// EnsureAuthDirectives makes sure squid.conf requires proxy authentication
// for any traffic that isn't already allowed by an earlier rule (e.g. the
// existing "allow localhost" rules keep working unauthenticated). It's
// idempotent and best-effort, like BlacklistManager.EnsureDirectives.
func (u *UserManager) EnsureAuthDirectives() error {
	content, err := u.confMgr.ReadConfig()
	if err != nil {
		return err
	}

	marker := "# --- squidadmin: proxy authentication ---"
	if strings.Contains(content, marker) {
		return nil
	}

	block := []string{
		marker,
		fmt.Sprintf("auth_param basic program %s %s", u.HelperPath, u.PasswdPath),
		"auth_param basic realm Squid Proxy",
		"auth_param basic credentialsttl 2 hours",
		fmt.Sprintf("acl %s proxy_auth REQUIRED", u.ACLName),
		fmt.Sprintf("http_access allow %s", u.ACLName),
		"# --- end squidadmin ---",
	}

	lines := strings.Split(content, "\n")
	var out []string
	inserted := false
	for _, line := range lines {
		if !inserted && strings.TrimSpace(line) == "http_access deny all" {
			out = append(out, block...)
			inserted = true
		}
		out = append(out, line)
	}

	if !inserted {
		out = append(out, block...)
	}

	return u.confMgr.WriteConfig(strings.Join(out, "\n"))
}
