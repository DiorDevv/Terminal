package auth

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return db
}

func TestEnsureUserBootstrapsOnce(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, "test-secret")

	if err := svc.EnsureUser("admin", "admin123"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 user after first EnsureUser, got %d", count)
	}

	// Calling again with a different password must NOT overwrite the
	// existing account, so an admin's changed password survives restarts.
	if err := svc.EnsureUser("admin", "different-password"); err != nil {
		t.Fatalf("EnsureUser (second call): %v", err)
	}

	if _, err := svc.Login("admin", "admin123"); err != nil {
		t.Fatalf("original password should still work, got: %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, "test-secret")

	if err := svc.EnsureUser("admin", "correct-password"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if _, err := svc.Login("admin", "wrong-password"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials, got: %v", err)
	}

	if _, err := svc.Login("nobody", "whatever"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for unknown user, got: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, "test-secret")

	if err := svc.EnsureUser("admin", "old-password"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	if err := svc.ChangePassword("admin", "wrong-old-password", "new-password"); err != ErrInvalidCredentials {
		t.Fatalf("expected ErrInvalidCredentials for wrong old password, got: %v", err)
	}

	if err := svc.ChangePassword("admin", "old-password", "new-password"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := svc.Login("admin", "old-password"); err != ErrInvalidCredentials {
		t.Fatal("old password should no longer work")
	}
	if _, err := svc.Login("admin", "new-password"); err != nil {
		t.Fatalf("new password should work, got: %v", err)
	}
}

func TestTokenRoundTrip(t *testing.T) {
	db := newTestDB(t)
	svc := NewService(db, "test-secret")

	if err := svc.EnsureUser("admin", "admin123"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	token, err := svc.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	claims, err := svc.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["username"] != "admin" {
		t.Fatalf("expected username claim 'admin', got %v", claims["username"])
	}

	otherSvc := NewService(db, "different-secret")
	if _, err := otherSvc.Verify(token); err == nil {
		t.Fatal("expected token signed with a different secret to fail verification")
	}
}
