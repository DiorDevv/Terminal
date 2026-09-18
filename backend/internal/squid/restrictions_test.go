package squid

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestRestrictionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`
		CREATE TABLE ip_groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			members TEXT NOT NULL
		);
		CREATE TABLE time_restrictions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			domains TEXT NOT NULL,
			days TEXT NOT NULL,
			start_time TEXT NOT NULL,
			end_time TEXT NOT NULL,
			exempt_cidrs TEXT NOT NULL DEFAULT '[]',
			exempt_group_id INTEGER REFERENCES ip_groups(id) ON DELETE SET NULL
		);
	`)
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func TestRestrictionCreateListDelete(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "squid.conf")
	initial := "acl Safe_ports port 80\nhttp_access allow localhost\nhttp_access deny all\n"
	if err := os.WriteFile(confPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial conf: %v", err)
	}

	confMgr := NewManager(confPath, "true")
	db := newTestRestrictionDB(t)
	rm := NewRestrictionManager(db, confMgr)

	r := Restriction{
		Name:        "social_media_worktime",
		Domains:     []string{"youtube.com", "facebook.com"},
		Days:        []string{"M", "T", "W", "H", "F"},
		StartTime:   "09:00",
		EndTime:     "18:00",
		ExemptCIDRs: []string{"127.0.0.1"},
	}

	created, err := rm.Create(r)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	list, err := rm.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 restriction, got %d", len(list))
	}

	content, err := confMgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !strings.Contains(content, "acl sqa_r1_domains dstdomain .youtube.com .facebook.com") {
		t.Fatalf("expected domains acl in config, got:\n%s", content)
	}
	if !strings.Contains(content, "acl sqa_r1_time time MTWHF 09:00-18:00") {
		t.Fatalf("expected time acl in config, got:\n%s", content)
	}
	if !strings.Contains(content, "http_access deny sqa_r1_domains sqa_r1_time !sqa_r1_exempt") {
		t.Fatalf("expected http_access deny line with exempt, got:\n%s", content)
	}

	// The generated block must sit before the final catch-all deny.
	blockIdx := strings.Index(content, restrictionBlockStart)
	denyAllIdx := strings.LastIndex(content, "http_access deny all")
	if blockIdx == -1 || denyAllIdx == -1 || blockIdx > denyAllIdx {
		t.Fatalf("expected restriction block before deny all, got:\n%s", content)
	}

	if err := rm.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	list, err = rm.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 restrictions after delete, got %d", len(list))
	}

	content, err = confMgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig after delete: %v", err)
	}
	if strings.Contains(content, restrictionBlockStart) {
		t.Fatalf("expected managed block to be removed after delete, got:\n%s", content)
	}
}

func TestRestrictionValidation(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "squid.conf")
	os.WriteFile(confPath, []byte("http_access deny all\n"), 0o644)
	confMgr := NewManager(confPath, "true")
	db := newTestRestrictionDB(t)
	rm := NewRestrictionManager(db, confMgr)

	cases := []Restriction{
		{Name: "ab", Domains: []string{"x.com"}, Days: []string{"M"}, StartTime: "09:00", EndTime: "10:00"},
		{Name: "valid_name", Domains: []string{}, Days: []string{"M"}, StartTime: "09:00", EndTime: "10:00"},
		{Name: "valid_name2", Domains: []string{"x.com"}, Days: []string{}, StartTime: "09:00", EndTime: "10:00"},
		{Name: "valid_name3", Domains: []string{"x.com"}, Days: []string{"Q"}, StartTime: "09:00", EndTime: "10:00"},
		{Name: "valid_name4", Domains: []string{"x.com"}, Days: []string{"M"}, StartTime: "9:00", EndTime: "10:00"},
	}
	for _, c := range cases {
		if _, err := rm.Create(c); err == nil {
			t.Errorf("Create(%+v) expected error, got none", c)
		}
	}
}
