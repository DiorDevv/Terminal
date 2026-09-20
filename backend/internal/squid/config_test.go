package squid

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// writeFakeSquid installs a stand-in "squid" binary whose behaviour is driven
// by marker files next to it, so tests can make parse/reconfigure/check fail
// on demand.
func writeFakeSquid(t *testing.T) (bin, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake squid is a shell script")
	}

	dir = t.TempDir()
	bin = filepath.Join(dir, "squid")
	script := `#!/bin/sh
dir=$(dirname "$0")
case "$2" in
  parse)       [ -f "$dir/fail_parse" ] && { echo "bad config"; exit 1; }; exit 0;;
  reconfigure) [ -f "$dir/fail_reconfigure" ] && { rm "$dir/fail_reconfigure"; echo boom; exit 1; }; exit 0;;
  check)       [ -f "$dir/stopped" ] && exit 1; exit 0;;
  rotate)      [ -f "$dir/fail_rotate" ] && { echo "no permission"; exit 1; }; touch "$dir/rotated"; exit 0;;
esac
exit 0
`
	// fail_reconfigure is one-shot: it models "the new config can't be
	// loaded" while the restored old config loads fine.
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake squid: %v", err)
	}
	return bin, dir
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

func newTestHistory(t *testing.T) *SQLHistory {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE config_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		comment TEXT NOT NULL,
		content TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	return NewSQLHistory(db)
}

func newConf(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "squid.conf")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	return path
}

func TestUpdateNoopWhenUnchanged(t *testing.T) {
	bin, dir := writeFakeSquid(t)
	// If Update tried to validate an unchanged config this would fail it.
	touch(t, filepath.Join(dir, "fail_parse"))

	conf := newConf(t, "http_access deny all\n")
	m := NewManager(conf, bin)

	err := m.Update("noop", func(c string) (string, error) { return c, nil })
	if err != nil {
		t.Fatalf("Update with no change should not validate or fail: %v", err)
	}
}

func TestUpdateRejectsInvalidConfigAndKeepsOriginal(t *testing.T) {
	bin, dir := writeFakeSquid(t)
	touch(t, filepath.Join(dir, "fail_parse"))

	original := "http_access deny all\n"
	conf := newConf(t, original)
	m := NewManager(conf, bin)

	err := m.WriteConfig("garbage\n")
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation error, got %v", err)
	}

	got, _ := os.ReadFile(conf)
	if string(got) != original {
		t.Fatalf("invalid config must not be written, file is now %q", got)
	}
}

func TestConcurrentUpdatesDoNotLoseWrites(t *testing.T) {
	bin, _ := writeFakeSquid(t)
	conf := newConf(t, "http_access deny all\n")
	m := NewManager(conf, bin)

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			line := fmt.Sprintf("# change %d", i)
			if err := m.Update("concurrent", func(c string) (string, error) {
				return c + line + "\n", nil
			}); err != nil {
				t.Errorf("Update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, _ := os.ReadFile(conf)
	for i := 0; i < n; i++ {
		if !strings.Contains(string(got), fmt.Sprintf("# change %d\n", i)) {
			t.Errorf("change %d was lost:\n%s", i, got)
		}
	}
}

func TestReconfigureRollsBackWhenReloadFails(t *testing.T) {
	bin, dir := writeFakeSquid(t)
	original := "http_access allow localhost\nhttp_access deny all\n"
	conf := newConf(t, original)

	m := NewManager(conf, bin)
	hist := newTestHistory(t)
	m.SetHistory(hist)

	if err := m.WriteConfig(original + "# risky change\n"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	touch(t, filepath.Join(dir, "fail_reconfigure"))
	err := m.Reconfigure()
	if err == nil || !strings.Contains(err.Error(), "restored automatically") {
		t.Fatalf("expected an automatic-rollback error, got %v", err)
	}

	got, _ := os.ReadFile(conf)
	if string(got) != original {
		t.Fatalf("config should be rolled back to the original, got %q", got)
	}

	versions, _ := hist.List(10)
	if len(versions) == 0 || !strings.Contains(versions[0].Comment, "rollback") {
		t.Fatalf("rollback should be recorded in history, got %+v", versions)
	}
}

func TestReconfigureRollsBackWhenSquidDiesAfterReload(t *testing.T) {
	bin, _ := writeFakeSquid(t)

	// Reconfigure "succeeds" but squid is gone afterwards (e.g. the new
	// config makes it fail to bind its port): reloading drops the "stopped"
	// marker, and a rollback must bring squid back (start = no args).
	script := `#!/bin/sh
dir=$(dirname "$0")
case "$2" in
  parse) exit 0;;
  reconfigure) touch "$dir/stopped"; exit 0;;
  check) [ -f "$dir/stopped" ] && exit 1; exit 0;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	original := "http_access deny all\n"
	conf := newConf(t, original)

	m := NewManager(conf, bin)
	m.VerifyDelay = 10 * time.Millisecond

	if err := m.WriteConfig(original + "# bad runtime change\n"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	err := m.Reconfigure()
	if err == nil || !strings.Contains(err.Error(), "squid stopped after reload") {
		t.Fatalf("expected 'squid stopped after reload', got %v", err)
	}

	got, _ := os.ReadFile(conf)
	if string(got) != original {
		t.Fatalf("config should be rolled back, got %q", got)
	}
}

func TestReconfigureFailureWithoutRunningSquidDoesNotRollBack(t *testing.T) {
	bin, dir := writeFakeSquid(t)
	conf := newConf(t, "http_access deny all\n")
	m := NewManager(conf, bin)

	changed := "http_access deny all\n# wanted change\n"
	if err := m.WriteConfig(changed); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	touch(t, filepath.Join(dir, "stopped"))
	touch(t, filepath.Join(dir, "fail_reconfigure"))
	if err := m.Reconfigure(); err == nil {
		t.Fatal("expected reconfigure error")
	}

	got, _ := os.ReadFile(conf)
	if string(got) != changed {
		t.Fatalf("a stopped squid must not trigger rollback, got %q", got)
	}
}

func TestHistorySaveDedupesAndRestore(t *testing.T) {
	bin, _ := writeFakeSquid(t)
	conf := newConf(t, "v1\n")
	m := NewManager(conf, bin)
	hist := newTestHistory(t)
	m.SetHistory(hist)

	if err := m.EnsureBaseline(); err != nil {
		t.Fatalf("EnsureBaseline: %v", err)
	}
	if err := m.EnsureBaseline(); err != nil {
		t.Fatalf("EnsureBaseline (second): %v", err)
	}
	if err := m.WriteConfig("v2\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig("v2\n"); err != nil { // identical: no new version
		t.Fatal(err)
	}

	versions, err := hist.List(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected baseline + one change (2 versions), got %d: %+v", len(versions), versions)
	}

	baseline := versions[len(versions)-1]
	if err := m.Restore(baseline.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, _ := os.ReadFile(conf)
	if string(got) != "v1\n" {
		t.Fatalf("restore should bring back v1, got %q", got)
	}
}

func TestPolicyAnchorSkipsManagedBlocks(t *testing.T) {
	lines := strings.Split(strings.Join([]string{
		"http_access deny !Safe_ports",
		"# --- squidadmin: proxy authentication ---",
		"http_access allow authenticated_users",
		"# --- end squidadmin ---",
		"http_access allow localhost",
		"http_access deny all",
	}, "\n"), "\n")

	if got := policyAnchor(lines); got != 4 {
		t.Fatalf("anchor should skip the managed auth block and land on 'allow localhost' (4), got %d", got)
	}
}
