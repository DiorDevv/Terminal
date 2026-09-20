package squid

import (
	"database/sql"
	"fmt"
	"time"
)

// Version is one saved snapshot of squid.conf.
type Version struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Comment   string    `json:"comment"`
	Size      int       `json:"size"`
	Content   string    `json:"content,omitempty"`
}

// History stores snapshots of squid.conf so any change can be reviewed and
// rolled back.
type History interface {
	// Save records content unless it is identical to the newest snapshot.
	Save(comment, content string) error
	// List returns the newest snapshots first, without their content.
	List(limit int) ([]Version, error)
	Get(id int64) (Version, error)
}

// maxVersions bounds how many snapshots are kept; older ones are pruned.
const maxVersions = 200

type SQLHistory struct {
	db *sql.DB
}

func NewSQLHistory(db *sql.DB) *SQLHistory {
	return &SQLHistory{db: db}
}

func (h *SQLHistory) Save(comment, content string) error {
	var latest string
	err := h.db.QueryRow(`SELECT content FROM config_versions ORDER BY id DESC LIMIT 1`).Scan(&latest)
	if err == nil && latest == content {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if _, err := h.db.Exec(`INSERT INTO config_versions (comment, content) VALUES (?, ?)`, comment, content); err != nil {
		return fmt.Errorf("save config version: %w", err)
	}

	_, err = h.db.Exec(`DELETE FROM config_versions WHERE id NOT IN (SELECT id FROM config_versions ORDER BY id DESC LIMIT ?)`, maxVersions)
	return err
}

func (h *SQLHistory) List(limit int) ([]Version, error) {
	rows, err := h.db.Query(
		`SELECT id, created_at, comment, length(content) FROM config_versions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Version{}
	for rows.Next() {
		var v Version
		var ts int64
		if err := rows.Scan(&v.ID, &ts, &v.Comment, &v.Size); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(ts, 0).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *SQLHistory) Get(id int64) (Version, error) {
	var v Version
	var ts int64
	err := h.db.QueryRow(
		`SELECT id, created_at, comment, length(content), content FROM config_versions WHERE id = ?`, id,
	).Scan(&v.ID, &ts, &v.Comment, &v.Size, &v.Content)
	if err != nil {
		return Version{}, err
	}
	v.CreatedAt = time.Unix(ts, 0).UTC()
	return v, nil
}
