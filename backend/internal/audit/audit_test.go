package audit

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"squidadmin/backend/internal/db"
)

func TestRecordCapsAttackerControlledFields(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := NewStore(conn)

	// A username made of multi-byte characters, far longer than any limit: the
	// cut must not split a character.
	long := strings.Repeat("ж", 100000)
	s.Record(Entry{Username: long, IP: strings.Repeat("1", 500), Action: "LOGIN", Target: strings.Repeat("t", 9999), Detail: strings.Repeat("d", 9999), Status: 401})

	got, err := s.List(Filter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("List: %v %v", got, err)
	}
	e := got[0]
	if len(e.Username) > 64 || len(e.IP) > 64 || len(e.Target) > 512 || len(e.Detail) > 512 {
		t.Errorf("fields were not capped: %d %d %d %d", len(e.Username), len(e.IP), len(e.Target), len(e.Detail))
	}
	if !utf8.ValidString(e.Username) || e.Username == "" {
		t.Errorf("the username must stay valid UTF-8 and non-empty: %q", e.Username)
	}

	// Short values are untouched.
	s.Record(Entry{Username: "alice", Action: "PUT /x", Status: 200})
	got, _ = s.List(Filter{Username: "alice"})
	if len(got) != 1 || got[0].Username != "alice" || got[0].Action != "PUT /x" {
		t.Errorf("normal entries must be stored as they are: %+v", got)
	}
}
