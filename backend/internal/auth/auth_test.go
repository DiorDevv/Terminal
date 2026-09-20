package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"squidadmin/backend/internal/db"
)

const goodPass = "correct-horse-battery"

func newSvc(t *testing.T) *Service {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewService(conn)
}

// newSvcWithAdmin returns a service with one admin ("boss") who has already
// changed away from any temporary password.
func newSvcWithAdmin(t *testing.T) *Service {
	t.Helper()
	svc := newSvc(t)
	if _, err := svc.Bootstrap("boss", goodPass, false); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return svc
}

func login(t *testing.T, svc *Service, user, pass string) string {
	t.Helper()
	tok, _, err := svc.Login(user, pass, "10.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Login(%s): %v", user, err)
	}
	return tok
}

func TestBootstrapOnlyWhenEmpty(t *testing.T) {
	svc := newSvc(t)

	created, err := svc.Bootstrap("admin", goodPass, true)
	if err != nil || !created {
		t.Fatalf("first Bootstrap: created=%v err=%v", created, err)
	}
	// A restart with a different password must not touch the existing account.
	created, err = svc.Bootstrap("admin", "another-password-1", false)
	if err != nil || created {
		t.Fatalf("second Bootstrap must be a no-op: created=%v err=%v", created, err)
	}
	// Deleting the default account must not resurrect it on restart either.
	users, _ := svc.ListUsers()
	if len(users) != 1 || users[0].Role != RoleAdmin || !users[0].MustChangePassword {
		t.Fatalf("unexpected bootstrap user: %+v", users)
	}
}

func TestLoginAndAuthenticate(t *testing.T) {
	svc := newSvcWithAdmin(t)
	tok := login(t, svc, "boss", goodPass)

	u, sess, err := svc.Authenticate(tok)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u.Username != "boss" || u.Role != RoleAdmin || !sess.Current {
		t.Fatalf("unexpected identity: %+v %+v", u, sess)
	}
	if u.CreatedAt.IsZero() || u.CreatedAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("created_at must be the real account creation time, got %v", u.CreatedAt)
	}

	// Only a hash of the token may be stored.
	var n int
	svc.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, tok).Scan(&n)
	if n != 0 {
		t.Fatal("raw session token must never be stored")
	}

	if _, _, err := svc.Authenticate("not-a-real-token"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("bogus token: want ErrSessionInvalid, got %v", err)
	}
	if _, _, err := svc.Authenticate(""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("empty token: want ErrSessionInvalid, got %v", err)
	}
}

func TestWrongPasswordAndUnknownUserLookAlike(t *testing.T) {
	svc := newSvcWithAdmin(t)

	_, _, e1 := svc.Login("boss", "wrong-password", "", "")
	_, _, e2 := svc.Login("nobody", "whatever-123", "", "")
	if !errors.Is(e1, ErrInvalidCredentials) || !errors.Is(e2, ErrInvalidCredentials) {
		t.Fatalf("both must be ErrInvalidCredentials, got %v / %v", e1, e2)
	}
}

func TestAccountLocksAfterRepeatedFailures(t *testing.T) {
	svc := newSvcWithAdmin(t)
	now := time.Now()
	svc.now = func() time.Time { return now }

	for i := 0; i < maxFailedLogins; i++ {
		if _, _, err := svc.Login("boss", "wrong-password", "", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}

	// Even the right password is refused while locked.
	if _, _, err := svc.Login("boss", goodPass, "", ""); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked, got %v", err)
	}

	now = now.Add(lockDuration + time.Second)
	if _, _, err := svc.Login("boss", goodPass, "", ""); err != nil {
		t.Fatalf("lock should have expired: %v", err)
	}
}

func TestSuccessfulLoginResetsFailureCounter(t *testing.T) {
	svc := newSvcWithAdmin(t)
	for i := 0; i < maxFailedLogins-1; i++ {
		svc.Login("boss", "wrong-password", "", "")
	}
	login(t, svc, "boss", goodPass)
	// Counter was reset, so four more failures must not lock the account.
	for i := 0; i < maxFailedLogins-1; i++ {
		svc.Login("boss", "wrong-password", "", "")
	}
	if _, _, err := svc.Login("boss", goodPass, "", ""); err != nil {
		t.Fatalf("account should not be locked: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	svc := newSvcWithAdmin(t)
	now := time.Now()
	svc.now = func() time.Time { return now }

	tok := login(t, svc, "boss", goodPass)

	// Activity keeps a session alive far beyond the idle limit...
	start := now
	for now.Sub(start) < sessionMax-6*time.Hour {
		now = now.Add(6 * time.Hour)
		if _, _, err := svc.Authenticate(tok); err != nil {
			t.Fatalf("an active session died early, %v after login: %v", now.Sub(start), err)
		}
	}

	// ...but never past the absolute limit. The last use was under 8h ago, so
	// only the absolute limit can be what ends it.
	now = now.Add(7 * time.Hour)
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("session must expire at the absolute limit even while active, got %v", err)
	}

	// Idle expiry.
	tok = login(t, svc, "boss", goodPass)
	now = now.Add(sessionIdle + time.Minute)
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("idle session must expire, got %v", err)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	svc := newSvcWithAdmin(t)
	tok := login(t, svc, "boss", goodPass)

	if err := svc.Logout(tok); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("token must be dead after logout, got %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	svc := newSvc(t)
	svc.Bootstrap("boss", "Temp-pass-123", true)
	u, _ := svc.ListUsers()
	id := u[0].ID

	mine := login(t, svc, "boss", "Temp-pass-123")
	other := login(t, svc, "boss", "Temp-pass-123")

	if err := svc.ChangePassword(id, "wrong-old-pass", "brand-new-pass-1", mine); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong old password: %v", err)
	}
	if err := svc.ChangePassword(id, "Temp-pass-123", "short", mine); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak new password must be rejected, got %v", err)
	}
	if err := svc.ChangePassword(id, "Temp-pass-123", "Temp-pass-123", mine); err == nil {
		t.Fatal("reusing the same password must be rejected")
	}

	if err := svc.ChangePassword(id, "Temp-pass-123", "brand-new-pass-1", mine); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, _, err := svc.Login("boss", "Temp-pass-123", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password must stop working")
	}
	if user, _, err := svc.Authenticate(mine); err != nil || user.MustChangePassword {
		t.Fatalf("caller's session must survive and the forced-change flag clear: %v %+v", err, user)
	}
	if _, _, err := svc.Authenticate(other); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("the user's other sessions must be ended")
	}
}

func TestDisablingUserEndsSessionsAndBlocksLogin(t *testing.T) {
	svc := newSvcWithAdmin(t)
	op, err := svc.CreateUser("olga", "olgas-password-1", RoleOperator)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	tok := login(t, svc, "olga", "olgas-password-1")

	yes := true
	if _, err := svc.UpdateUser(op.ID, nil, &yes); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("disabling must end existing sessions immediately")
	}
	if _, _, err := svc.Login("olga", "olgas-password-1", "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user must not log in, got %v", err)
	}
}

func TestRoleChangeEndsSessions(t *testing.T) {
	svc := newSvcWithAdmin(t)
	op, _ := svc.CreateUser("olga", "olgas-password-1", RoleOperator)
	tok := login(t, svc, "olga", "olgas-password-1")

	viewer := RoleViewer
	if _, err := svc.UpdateUser(op.ID, &viewer, nil); err != nil {
		t.Fatalf("demote: %v", err)
	}
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("a demoted user must not keep running with the old role")
	}
}

func TestLastAdminIsProtected(t *testing.T) {
	svc := newSvcWithAdmin(t)
	users, _ := svc.ListUsers()
	boss := users[0]

	viewer := RoleViewer
	yes := true
	if _, err := svc.UpdateUser(boss.ID, &viewer, nil); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demoting the last admin: got %v", err)
	}
	if _, err := svc.UpdateUser(boss.ID, nil, &yes); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disabling the last admin: got %v", err)
	}
	if err := svc.DeleteUser(boss.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("deleting the last admin: got %v", err)
	}

	// With a second admin the first can be demoted.
	if _, err := svc.CreateUser("second", "second-admin-pass", RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := svc.UpdateUser(boss.ID, &viewer, nil); err != nil {
		t.Fatalf("demoting a non-last admin should work: %v", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	svc := newSvcWithAdmin(t)

	if _, err := svc.CreateUser("ab", goodPass, RoleViewer); err == nil {
		t.Error("short username must be rejected")
	}
	if _, err := svc.CreateUser("bad name", goodPass, RoleViewer); err == nil {
		t.Error("username with a space must be rejected")
	}
	if _, err := svc.CreateUser("valid_user", "short", RoleViewer); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: got %v", err)
	}
	if _, err := svc.CreateUser("valid_user", goodPass, Role("root")); err == nil {
		t.Error("unknown role must be rejected")
	}
	if _, err := svc.CreateUser("boss", goodPass, RoleViewer); !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate username: got %v", err)
	}

	u, err := svc.CreateUser("newbie", goodPass, RoleViewer)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !u.MustChangePassword {
		t.Error("accounts created by an admin must change their password at first login")
	}
}

func TestResetPassword(t *testing.T) {
	svc := newSvcWithAdmin(t)
	op, _ := svc.CreateUser("olga", "olgas-password-1", RoleOperator)
	tok := login(t, svc, "olga", "olgas-password-1")

	if err := svc.ResetPassword(op.ID, "weak"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak reset password: got %v", err)
	}
	if err := svc.ResetPassword(op.ID, "temporary-pass-9"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, _, err := svc.Authenticate(tok); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("reset must sign the user out everywhere")
	}
	_, u, err := svc.Login("olga", "temporary-pass-9", "", "")
	if err != nil || !u.MustChangePassword {
		t.Fatalf("login with temp password: err=%v must_change=%v", err, u.MustChangePassword)
	}
}

func TestFlagWeakPasswords(t *testing.T) {
	svc := newSvc(t)
	// An installation created by an older version: default password in place.
	if _, err := svc.Bootstrap("admin", "admin123", false); err != nil {
		t.Fatal(err)
	}
	svc.CreateUser("fine", goodPass, RoleViewer)
	svc.db.Exec(`UPDATE users SET must_change_password = 0`)

	n, err := svc.FlagWeakPasswords("admin123")
	if err != nil || n != 1 {
		t.Fatalf("FlagWeakPasswords: n=%d err=%v", n, err)
	}
	users, _ := svc.ListUsers()
	for _, u := range users {
		if want := u.Username == "admin"; u.MustChangePassword != want {
			t.Errorf("%s: must_change=%v, want %v", u.Username, u.MustChangePassword, want)
		}
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	svc := newSvcWithAdmin(t)
	users, _ := svc.ListUsers()
	id := users[0].ID

	a := login(t, svc, "boss", goodPass)
	login(t, svc, "boss", goodPass)

	sessions, err := svc.Sessions(id, a)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("Sessions: n=%d err=%v", len(sessions), err)
	}
	current := 0
	var other Session
	for _, s := range sessions {
		if s.Current {
			current++
		} else {
			other = s
		}
	}
	if current != 1 {
		t.Fatalf("exactly one session must be flagged current, got %d", current)
	}

	// A user can't revoke someone else's session by guessing its id.
	other2, _ := svc.CreateUser("olga", "olgas-password-1", RoleViewer)
	if err := svc.RevokeSession(other2.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := svc.Sessions(id, a); len(s) != 2 {
		t.Fatal("revoking with the wrong owner id must not delete the session")
	}

	if err := svc.RevokeSession(id, other.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := svc.Sessions(id, a); len(s) != 1 {
		t.Fatal("owner must be able to revoke their own session")
	}
}

func TestValidatePassword(t *testing.T) {
	bad := map[string]string{
		"short":                 "",
		"admin123":              "",
		"Password":              "",
		strings.Repeat("x", 73): "",
		"boss":                  "",
		"BOSS":                  "",
		"12345678":              "",
	}
	for pw := range bad {
		if err := ValidatePassword("boss", pw); err == nil {
			t.Errorf("ValidatePassword(%q) should fail", pw)
		}
	}
	for _, pw := range []string{"correct-horse-battery", "Xk9$mQ2vLp", strings.Repeat("y", 72)} {
		if err := ValidatePassword("boss", pw); err != nil {
			t.Errorf("ValidatePassword(%q) unexpected error: %v", pw, err)
		}
	}
}

func TestRoleOrdering(t *testing.T) {
	cases := []struct {
		have, need Role
		want       bool
	}{
		{RoleAdmin, RoleViewer, true}, {RoleAdmin, RoleOperator, true}, {RoleAdmin, RoleAdmin, true},
		{RoleOperator, RoleViewer, true}, {RoleOperator, RoleOperator, true}, {RoleOperator, RoleAdmin, false},
		{RoleViewer, RoleViewer, true}, {RoleViewer, RoleOperator, false}, {RoleViewer, RoleAdmin, false},
		{Role(""), RoleViewer, false}, {Role("root"), RoleViewer, false},
	}
	for _, c := range cases {
		if got := c.have.AtLeast(c.need); got != c.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}
