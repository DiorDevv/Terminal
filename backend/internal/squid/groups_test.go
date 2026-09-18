package squid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGroupCreateListDelete(t *testing.T) {
	db := newTestRestrictionDB(t)
	gm := NewGroupManager(db)

	g, err := gm.Create("it_bolimi", []string{"127.0.0.1", "192.168.1.0/24"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.ID == 0 {
		t.Fatal("expected non-zero group ID")
	}

	list, err := gm.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "it_bolimi" {
		t.Fatalf("unexpected list: %+v", list)
	}

	if err := gm.Delete(g.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err = gm.List()
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list after delete, got %+v", list)
	}
}

func TestGroupValidation(t *testing.T) {
	db := newTestRestrictionDB(t)
	gm := NewGroupManager(db)

	if _, err := gm.Create("ab", []string{"127.0.0.1"}); err == nil {
		t.Error("expected error for too-short name")
	}
	if _, err := gm.Create("valid_name", nil); err == nil {
		t.Error("expected error for empty members")
	}
	if _, err := gm.Create("valid_name2", []string{"not an ip!"}); err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

func TestRestrictionUsesGroupExemption(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "squid.conf")
	os.WriteFile(confPath, []byte("http_access deny all\n"), 0o644)
	confMgr := NewManager(confPath, "true")

	db := newTestRestrictionDB(t)
	gm := NewGroupManager(db)
	rm := NewRestrictionManager(db, confMgr)

	g, err := gm.Create("it_bolimi", []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("Create group: %v", err)
	}

	_, err = rm.Create(Restriction{
		Name:          "social_worktime",
		Domains:       []string{"youtube.com"},
		Days:          []string{"M"},
		StartTime:     "09:00",
		EndTime:       "18:00",
		ExemptGroupID: &g.ID,
	})
	if err != nil {
		t.Fatalf("Create restriction: %v", err)
	}

	content, err := confMgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	if !strings.Contains(content, "acl sqa_r1_exempt src 127.0.0.1") {
		t.Fatalf("expected exempt acl resolved from group members, got:\n%s", content)
	}

	// Deleting the group must not leave a dangling reference in squid.conf:
	// the caller (handler) regenerates after group deletion.
	if err := gm.Delete(g.ID); err != nil {
		t.Fatalf("Delete group: %v", err)
	}
	if err := rm.Regenerate(); err != nil {
		t.Fatalf("Regenerate after group delete: %v", err)
	}

	content, err = confMgr.ReadConfig()
	if err != nil {
		t.Fatalf("ReadConfig after group delete: %v", err)
	}
	if strings.Contains(content, "sqa_r1_exempt") {
		t.Fatalf("expected exempt acl to disappear once its group is gone, got:\n%s", content)
	}
}
