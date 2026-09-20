package squid

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Restriction is a time-scoped block rule: deny access to a set of domains
// during certain days/hours, optionally exempting a set of source
// IPs/CIDRs. It generalizes the hand-written
// "social_media + ish_vaqti + !it_bolimi" pattern that already existed in
// this machine's squid.conf into something manageable from the UI.
type Restriction struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Domains         []string `json:"domains"`
	Days            []string `json:"days"` // squid day letters: S M T W H F A
	StartTime       string   `json:"start_time"`
	EndTime         string   `json:"end_time"`
	ExemptCIDRs     []string `json:"exempt_cidrs"`
	ExemptGroupID   *int64   `json:"exempt_group_id,omitempty"`
	ExemptGroupName string   `json:"exempt_group_name,omitempty"`
}

type RestrictionManager struct {
	db      *sql.DB
	confMgr *Manager
}

func NewRestrictionManager(db *sql.DB, confMgr *Manager) *RestrictionManager {
	return &RestrictionManager{db: db, confMgr: confMgr}
}

var (
	restrictionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,40}$`)
	timePattern            = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	cidrPattern            = regexp.MustCompile(`^[0-9a-fA-F:.]+(/\d{1,3})?$`)
	validDays              = map[string]bool{"S": true, "M": true, "T": true, "W": true, "H": true, "F": true, "A": true}
)

func validateRestriction(r Restriction) error {
	if !restrictionNamePattern.MatchString(r.Name) {
		return fmt.Errorf("invalid name: only letters, digits, '_', '-' allowed, 3-40 chars")
	}
	if len(r.Domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	if len(r.Days) == 0 {
		return fmt.Errorf("at least one day is required")
	}
	for _, d := range r.Days {
		if !validDays[d] {
			return fmt.Errorf("invalid day code: %q", d)
		}
	}
	if !timePattern.MatchString(r.StartTime) || !timePattern.MatchString(r.EndTime) {
		return fmt.Errorf("start/end time must be in HH:MM format")
	}
	for _, c := range r.ExemptCIDRs {
		if !cidrPattern.MatchString(c) {
			return fmt.Errorf("invalid exempt IP/CIDR: %q", c)
		}
	}
	return nil
}

func (m *RestrictionManager) List() ([]Restriction, error) {
	rows, err := m.db.Query(`
		SELECT r.id, r.name, r.domains, r.days, r.start_time, r.end_time, r.exempt_cidrs, r.exempt_group_id, g.name
		FROM time_restrictions r
		LEFT JOIN ip_groups g ON g.id = r.exempt_group_id
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Restriction{}
	for rows.Next() {
		var r Restriction
		var domainsJSON, daysJSON, exemptJSON string
		var groupID sql.NullInt64
		var groupName sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &domainsJSON, &daysJSON, &r.StartTime, &r.EndTime, &exemptJSON, &groupID, &groupName); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(domainsJSON), &r.Domains)
		json.Unmarshal([]byte(daysJSON), &r.Days)
		json.Unmarshal([]byte(exemptJSON), &r.ExemptCIDRs)
		if groupID.Valid {
			id := groupID.Int64
			r.ExemptGroupID = &id
			r.ExemptGroupName = groupName.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *RestrictionManager) Create(r Restriction) (Restriction, error) {
	if err := validateRestriction(r); err != nil {
		return Restriction{}, err
	}

	for i, d := range r.Domains {
		norm, err := normalizeDomain(d)
		if err != nil {
			return Restriction{}, err
		}
		r.Domains[i] = norm
	}

	if r.ExemptGroupID != nil {
		if _, err := NewGroupManager(m.db).Get(*r.ExemptGroupID); err != nil {
			return Restriction{}, fmt.Errorf("exempt group not found: %w", err)
		}
		r.ExemptCIDRs = nil
	}

	domainsJSON, _ := json.Marshal(r.Domains)
	daysJSON, _ := json.Marshal(r.Days)
	exemptJSON, _ := json.Marshal(r.ExemptCIDRs)

	res, err := m.db.Exec(
		`INSERT INTO time_restrictions (name, domains, days, start_time, end_time, exempt_cidrs, exempt_group_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Name, string(domainsJSON), string(daysJSON), r.StartTime, r.EndTime, string(exemptJSON), r.ExemptGroupID,
	)
	if err != nil {
		return Restriction{}, fmt.Errorf("save restriction: %w", err)
	}
	r.ID, _ = res.LastInsertId()

	if err := m.Regenerate(); err != nil {
		m.db.Exec(`DELETE FROM time_restrictions WHERE id = ?`, r.ID)
		return Restriction{}, err
	}

	return r, nil
}

func (m *RestrictionManager) Delete(id int64) error {
	if _, err := m.db.Exec(`DELETE FROM time_restrictions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete restriction: %w", err)
	}
	return m.Regenerate()
}

const (
	restrictionBlockStart = "# --- squidadmin: time-restrictions (managed, do not edit by hand) ---"
	restrictionBlockEnd   = "# --- end squidadmin: time-restrictions ---"
)

// Regenerate rebuilds the whole managed block from the current DB state and
// writes it back into squid.conf, replacing any previous version of the
// block. It's called after every create/delete so squid.conf always
// reflects exactly what's in the database.
//
// The block is placed before the first http_access "allow" rule. squid stops
// at the first matching rule, so a deny placed after "allow localhost" or
// "allow <authenticated users>" would never fire for those clients.
func (m *RestrictionManager) Regenerate() error {
	restrictions, err := m.List()
	if err != nil {
		return err
	}

	groups := NewGroupManager(m.db)

	var block []string
	block = append(block, restrictionBlockStart)
	for _, r := range restrictions {
		id := r.ID
		domainsACL := fmt.Sprintf("acl sqa_r%d_domains dstdomain %s", id, strings.Join(r.Domains, " "))
		timeACL := fmt.Sprintf("acl sqa_r%d_time time %s %s-%s", id, strings.Join(r.Days, ""), r.StartTime, r.EndTime)
		block = append(block, domainsACL, timeACL)

		exempt := r.ExemptCIDRs
		if r.ExemptGroupID != nil {
			if g, err := groups.Get(*r.ExemptGroupID); err == nil {
				exempt = g.Members
			}
		}

		access := fmt.Sprintf("http_access deny sqa_r%d_domains sqa_r%d_time", id, id)
		if len(exempt) > 0 {
			exemptACL := fmt.Sprintf("acl sqa_r%d_exempt src %s", id, strings.Join(exempt, " "))
			block = append(block, exemptACL)
			access += fmt.Sprintf(" !sqa_r%d_exempt", id)
		}
		block = append(block, access)
	}
	block = append(block, restrictionBlockEnd)

	return m.confMgr.Update("time restrictions regenerated", func(content string) (string, error) {
		lines := stripManagedBlock(strings.Split(content, "\n"), restrictionBlockStart, restrictionBlockEnd)

		if len(restrictions) > 0 {
			lines = insertAt(lines, denyStageAnchor(lines), block)
		}
		return strings.Join(lines, "\n"), nil
	})
}

// stripManagedBlock removes a previously inserted [start, end] marker block
// (inclusive) so Regenerate can cleanly re-insert a fresh one.
func stripManagedBlock(lines []string, start, end string) []string {
	startIdx, endIdx := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == start {
			startIdx = i
		}
		if trimmed == end {
			endIdx = i
			break
		}
	}
	if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
		return lines
	}
	out := make([]string, 0, len(lines)-(endIdx-startIdx+1))
	out = append(out, lines[:startIdx]...)
	out = append(out, lines[endIdx+1:]...)
	return out
}
