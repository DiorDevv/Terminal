package db

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The database holds password hashes, session tokens and alert secrets.
func TestDatabaseFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "p.db")

	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec(`INSERT INTO ip_groups (name, members) VALUES ('g', '[]')`) // makes the WAL files exist
	conn.Close()

	for _, p := range []string{dir, path} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is accessible to group/others: %v", p, fi.Mode().Perm())
		}
	}

	// A database left world-readable by an older version is tightened.
	os.Chmod(path, 0o644)
	conn, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("an existing world-readable database must be tightened: %v", fi.Mode().Perm())
	}
}
