package squid

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// Group is a named, reusable set of IPs/CIDRs (e.g. "IT bo'limi"). It has
// no direct effect on squid.conf by itself — it's referenced by
// Restriction.ExemptGroupID so a single edit here propagates to every rule
// that uses it on the next Regenerate.
type Group struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type GroupManager struct {
	db *sql.DB
}

func NewGroupManager(db *sql.DB) *GroupManager {
	return &GroupManager{db: db}
}

func (g *GroupManager) List() ([]Group, error) {
	rows, err := g.db.Query(`SELECT id, name, members FROM ip_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Group
	for rows.Next() {
		var group Group
		var membersJSON string
		if err := rows.Scan(&group.ID, &group.Name, &membersJSON); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(membersJSON), &group.Members)
		out = append(out, group)
	}
	return out, rows.Err()
}

func (g *GroupManager) Get(id int64) (Group, error) {
	var group Group
	var membersJSON string
	err := g.db.QueryRow(`SELECT id, name, members FROM ip_groups WHERE id = ?`, id).
		Scan(&group.ID, &group.Name, &membersJSON)
	if err != nil {
		return Group{}, err
	}
	json.Unmarshal([]byte(membersJSON), &group.Members)
	return group, nil
}

func (g *GroupManager) Create(name string, members []string) (Group, error) {
	if !restrictionNamePattern.MatchString(name) {
		return Group{}, fmt.Errorf("invalid name: only letters, digits, '_', '-' allowed, 3-40 chars")
	}
	if len(members) == 0 {
		return Group{}, fmt.Errorf("at least one IP/CIDR is required")
	}
	for _, m := range members {
		if !cidrPattern.MatchString(m) {
			return Group{}, fmt.Errorf("invalid IP/CIDR: %q", m)
		}
	}

	membersJSON, _ := json.Marshal(members)
	res, err := g.db.Exec(`INSERT INTO ip_groups (name, members) VALUES (?, ?)`, name, string(membersJSON))
	if err != nil {
		return Group{}, fmt.Errorf("save group: %w", err)
	}
	id, _ := res.LastInsertId()

	return Group{ID: id, Name: name, Members: members}, nil
}

// Delete removes a group. Any restriction that referenced it keeps its
// row (exempt_group_id becomes NULL via ON DELETE SET NULL) but effectively
// loses its exemption — the caller should regenerate squid.conf afterward.
func (g *GroupManager) Delete(id int64) error {
	if _, err := g.db.Exec(`DELETE FROM ip_groups WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}
