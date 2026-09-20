package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "sub", "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// SQLite ships with foreign keys OFF; without the DSN pragma the
// "ON DELETE SET NULL" on time_restrictions.exempt_group_id would do nothing
// and a deleted group would leave a dangling reference behind.
func TestForeignKeysAreEnforced(t *testing.T) {
	conn := openTemp(t)

	var on int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("foreign_keys pragma = %d (err %v), want 1", on, err)
	}

	res, err := conn.Exec(`INSERT INTO ip_groups (name, members) VALUES ('it_dept', '["10.0.0.1"]')`)
	if err != nil {
		t.Fatalf("insert group: %v", err)
	}
	groupID, _ := res.LastInsertId()

	if _, err := conn.Exec(
		`INSERT INTO time_restrictions (name, domains, days, start_time, end_time, exempt_group_id)
		 VALUES ('r1', '[]', '[]', '09:00', '18:00', ?)`, groupID); err != nil {
		t.Fatalf("insert restriction: %v", err)
	}

	if _, err := conn.Exec(`DELETE FROM ip_groups WHERE id = ?`, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}

	var ref *int64
	if err := conn.QueryRow(`SELECT exempt_group_id FROM time_restrictions WHERE name = 'r1'`).Scan(&ref); err != nil {
		t.Fatalf("select restriction: %v", err)
	}
	if ref != nil {
		t.Fatalf("exempt_group_id should be NULL after the group is deleted, got %d", *ref)
	}

	if _, err := conn.Exec(
		`INSERT INTO time_restrictions (name, domains, days, start_time, end_time, exempt_group_id)
		 VALUES ('r2', '[]', '[]', '09:00', '18:00', 9999)`); err == nil {
		t.Fatal("inserting a restriction that references a missing group must be rejected")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for i := 0; i < 2; i++ {
		conn, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		conn.Close()
	}
}
