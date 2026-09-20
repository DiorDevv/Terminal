// Package audit records who did what in the panel.
package audit

import (
	"database/sql"
	"log"
	"time"
	"unicode/utf8"
)

type Entry struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	Username string    `json:"username"`
	IP       string    `json:"ip"`
	Action   string    `json:"action"`
	Target   string    `json:"target"`
	Status   int       `json:"status"`
	Detail   string    `json:"detail"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Record writes an entry. A failure to audit is logged, never returned: it
// must not turn a successful action into an error for the user.
func (s *Store) Record(e Entry) {
	// The login endpoint records whatever username a stranger typed, so every
	// text field is capped: the log must not be a way to fill the disk.
	e.Username, e.IP = clamp(e.Username, 64), clamp(e.IP, 64)
	e.Action, e.Target, e.Detail = clamp(e.Action, 128), clamp(e.Target, 512), clamp(e.Detail, 512)
	_, err := s.db.Exec(
		`INSERT INTO audit_log (username, ip, action, target, status, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		e.Username, e.IP, e.Action, e.Target, e.Status, e.Detail)
	if err != nil {
		log.Printf("warning: could not write audit log: %v", err)
	}
}

// clamp cuts s to at most n bytes without splitting a UTF-8 character.
func clamp(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type Filter struct {
	Limit    int
	BeforeID int64  // return entries older than this id (pagination)
	Username string // exact match, optional
}

// List returns entries newest first.
func (s *Store) List(f Filter) ([]Entry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}

	q := `SELECT id, ts, username, ip, action, target, status, detail FROM audit_log WHERE 1=1`
	var args []any
	if f.BeforeID > 0 {
		q += ` AND id < ?`
		args = append(args, f.BeforeID)
	}
	if f.Username != "" {
		q += ` AND username = ?`
		args = append(args, f.Username)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Entry{}
	for rows.Next() {
		var e Entry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Username, &e.IP, &e.Action, &e.Target, &e.Status, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Prune deletes entries older than keep, so the table can't grow forever.
func (s *Store) Prune(keep time.Duration) error {
	_, err := s.db.Exec(`DELETE FROM audit_log WHERE ts < ?`, time.Now().Add(-keep).Unix())
	return err
}
