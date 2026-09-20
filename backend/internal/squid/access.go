package squid

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// User-defined access rules: an ordered list of "allow/deny when these ACLs
// match" that becomes one managed http_access block in squid.conf.

// RuleTerm is one ACL in a rule, optionally negated ("everyone except ...").
type RuleTerm struct {
	ACLID   int64  `json:"acl_id"`
	Negate  bool   `json:"negate"`
	ACLName string `json:"acl_name,omitempty"` // filled in by ListRules
	ACLType string `json:"acl_type,omitempty"`
}

// AccessRule is one http_access line the administrator defined.
type AccessRule struct {
	ID       int64      `json:"id"`
	Position int        `json:"position"`
	Action   string     `json:"action"` // "allow" or "deny"
	Terms    []RuleTerm `json:"terms"`
	Enabled  bool       `json:"enabled"`
	Comment  string     `json:"comment"`
}

var (
	ErrACLInUse  = errors.New("this ACL is used by access rules; remove it from them first")
	ErrNotFound  = errors.New("not found")
	commentBadRe = regexp.MustCompile(`[\r\n]`)
)

type AccessManager struct {
	db           *sql.DB
	confMgr      *Manager
	blocklistDir string
}

// NewAccessManager creates the manager. blocklistDir is where downloaded block
// lists live; squid reads them by path from the generated ACLs.
func NewAccessManager(db *sql.DB, confMgr *Manager, blocklistDir string) *AccessManager {
	return &AccessManager{db: db, confMgr: confMgr, blocklistDir: blocklistDir}
}

func (m *AccessManager) blocklistPath(id string) string {
	return filepath.Join(m.blocklistDir, id+".txt")
}

// ------------------------------------------------------------------- ACLs

func scanACL(s interface{ Scan(...any) error }) (ACLObject, error) {
	var a ACLObject
	var vals string
	var ci int
	if err := s.Scan(&a.ID, &a.Name, &a.Type, &vals, &ci, &a.Description); err != nil {
		return ACLObject{}, err
	}
	a.CaseInsensitive = ci != 0
	if err := json.Unmarshal([]byte(vals), &a.Values); err != nil || a.Values == nil {
		a.Values = []string{}
	}
	return a, nil
}

const aclCols = `id, name, type, acl_values, case_insensitive, description`

// ListACLs returns every ACL with the number of rules that use it.
func (m *AccessManager) ListACLs() ([]ACLObject, error) {
	rows, err := m.db.Query(`SELECT ` + aclCols + ` FROM acl_objects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ACLObject{}
	for rows.Next() {
		a, err := scanACL(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rules, err := m.rawRules(false)
	if err != nil {
		return nil, err
	}
	use := map[int64]int{}
	for _, r := range rules {
		for _, t := range r.Terms {
			use[t.ACLID]++
		}
	}
	for i := range out {
		out[i].UsedBy = use[out[i].ID]
	}
	return out, nil
}

func (m *AccessManager) GetACL(id int64) (ACLObject, error) {
	a, err := scanACL(m.db.QueryRow(`SELECT `+aclCols+` FROM acl_objects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ACLObject{}, ErrNotFound
	}
	return a, err
}

func validateACLName(name string) error {
	if !aclNamePattern.MatchString(name) {
		return fmt.Errorf("invalid name: lowercase letters, digits, '_' and '-' only, 3-40 characters")
	}
	return nil
}

func validateDescription(s string) error {
	if len(s) > 200 || commentBadRe.MatchString(s) {
		return fmt.Errorf("description must be one line of at most 200 characters")
	}
	return nil
}

// CreateACL adds a user-defined ACL. It is not written to squid.conf until a
// rule uses it.
func (m *AccessManager) CreateACL(name, typ string, values []string, caseInsensitive bool, description string) (ACLObject, error) {
	if err := validateACLName(name); err != nil {
		return ACLObject{}, err
	}
	if err := validateDescription(description); err != nil {
		return ACLObject{}, err
	}
	vals, err := normalizeACLValues(typ, values)
	if err != nil {
		return ACLObject{}, err
	}
	return m.insertACL(name, typ, vals, caseInsensitive, description)
}

// insertACL is the shared insert; it does not validate (callers did).
func (m *AccessManager) insertACL(name, typ string, vals []string, ci bool, description string) (ACLObject, error) {
	j, _ := json.Marshal(vals)
	res, err := m.db.Exec(
		`INSERT INTO acl_objects (name, type, acl_values, case_insensitive, description) VALUES (?, ?, ?, ?, ?)`,
		name, typ, string(j), boolToInt(ci), description)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ACLObject{}, fmt.Errorf("an ACL named %q already exists", name)
		}
		return ACLObject{}, err
	}
	id, _ := res.LastInsertId()
	return m.GetACL(id)
}

// UpdateACL changes an ACL's values, case flag and/or description. The type
// and name are fixed: rules refer to them.
func (m *AccessManager) UpdateACL(id int64, values []string, caseInsensitive *bool, description *string) (ACLObject, error) {
	cur, err := m.GetACL(id)
	if err != nil {
		return ACLObject{}, err
	}

	next := cur
	if values != nil {
		if cur.Type == "blocklist" {
			return ACLObject{}, fmt.Errorf("a block list ACL follows its source; edit the source instead")
		}
		if next.Values, err = normalizeACLValues(cur.Type, values); err != nil {
			return ACLObject{}, err
		}
	}
	if caseInsensitive != nil {
		next.CaseInsensitive = *caseInsensitive
	}
	if description != nil {
		if err := validateDescription(*description); err != nil {
			return ACLObject{}, err
		}
		next.Description = *description
	}

	write := func(a ACLObject) error {
		j, _ := json.Marshal(a.Values)
		_, err := m.db.Exec(`UPDATE acl_objects SET acl_values = ?, case_insensitive = ?, description = ? WHERE id = ?`,
			string(j), boolToInt(a.CaseInsensitive), a.Description, id)
		return err
	}
	if err := write(next); err != nil {
		return ACLObject{}, err
	}
	// A change only matters to squid if an enabled rule uses the ACL.
	if err := m.Regenerate(); err != nil {
		write(cur)
		return ACLObject{}, err
	}
	return m.GetACL(id)
}

// DeleteACL removes an ACL that no rule uses.
func (m *AccessManager) DeleteACL(id int64) error {
	a, err := m.GetACL(id)
	if err != nil {
		return err
	}
	rules, err := m.rawRules(false)
	if err != nil {
		return err
	}
	for _, r := range rules {
		for _, t := range r.Terms {
			if t.ACLID == id {
				return fmt.Errorf("%w (rule #%d)", ErrACLInUse, r.ID)
			}
		}
	}
	if a.Type == "blocklist" {
		return fmt.Errorf("a block list ACL is removed together with its source")
	}
	_, err = m.db.Exec(`DELETE FROM acl_objects WHERE id = ?`, id)
	return err
}

// ------------------------------------------------------------------ rules

func scanRule(s interface{ Scan(...any) error }) (AccessRule, error) {
	var r AccessRule
	var terms string
	var enabled int
	if err := s.Scan(&r.ID, &r.Position, &r.Action, &terms, &enabled, &r.Comment); err != nil {
		return AccessRule{}, err
	}
	r.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(terms), &r.Terms); err != nil || r.Terms == nil {
		r.Terms = []RuleTerm{}
	}
	return r, nil
}

const ruleCols = `id, position, action, terms, enabled, comment`

func (m *AccessManager) rawRules(onlyEnabled bool) ([]AccessRule, error) {
	q := `SELECT ` + ruleCols + ` FROM access_rules`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	rows, err := m.db.Query(q + ` ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AccessRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRules returns the rules in evaluation order, with ACL names filled in.
func (m *AccessManager) ListRules() ([]AccessRule, error) {
	rules, err := m.rawRules(false)
	if err != nil {
		return nil, err
	}
	acls, err := m.aclMap()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		for j := range rules[i].Terms {
			if a, ok := acls[rules[i].Terms[j].ACLID]; ok {
				rules[i].Terms[j].ACLName, rules[i].Terms[j].ACLType = a.Name, a.Type
			}
		}
	}
	return rules, nil
}

func (m *AccessManager) aclMap() (map[int64]ACLObject, error) {
	rows, err := m.db.Query(`SELECT ` + aclCols + ` FROM acl_objects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ACLObject{}
	for rows.Next() {
		a, err := scanACL(rows)
		if err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}

func (m *AccessManager) validateRule(action string, terms []RuleTerm, comment string) error {
	if action != "allow" && action != "deny" {
		return fmt.Errorf("action must be allow or deny")
	}
	if len(terms) == 0 || len(terms) > 8 {
		return fmt.Errorf("a rule needs 1-8 conditions")
	}
	if err := validateDescription(comment); err != nil {
		return fmt.Errorf("comment: %w", err)
	}
	acls, err := m.aclMap()
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, t := range terms {
		if _, ok := acls[t.ACLID]; !ok {
			return fmt.Errorf("ACL %d does not exist", t.ACLID)
		}
		if seen[t.ACLID] {
			return fmt.Errorf("an ACL can appear only once in a rule")
		}
		seen[t.ACLID] = true
	}
	return nil
}

func cleanTerms(in []RuleTerm) []RuleTerm {
	out := make([]RuleTerm, len(in))
	for i, t := range in {
		out[i] = RuleTerm{ACLID: t.ACLID, Negate: t.Negate}
	}
	return out
}

// CreateRule adds a rule at the given 1-based position (0 = at the end).
func (m *AccessManager) CreateRule(action string, terms []RuleTerm, comment string, position int, enabled bool) (AccessRule, error) {
	if err := m.validateRule(action, terms, comment); err != nil {
		return AccessRule{}, err
	}
	rules, err := m.rawRules(false)
	if err != nil {
		return AccessRule{}, err
	}
	if position < 1 || position > len(rules)+1 {
		position = len(rules) + 1
	}

	tx, err := m.db.Begin()
	if err != nil {
		return AccessRule{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE access_rules SET position = position + 1 WHERE position >= ?`, position); err != nil {
		return AccessRule{}, err
	}
	j, _ := json.Marshal(cleanTerms(terms))
	res, err := tx.Exec(`INSERT INTO access_rules (position, action, terms, enabled, comment) VALUES (?, ?, ?, ?, ?)`,
		position, action, string(j), boolToInt(enabled), comment)
	if err != nil {
		return AccessRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return AccessRule{}, err
	}
	id, _ := res.LastInsertId()

	if err := m.Regenerate(); err != nil {
		m.db.Exec(`DELETE FROM access_rules WHERE id = ?`, id)
		m.renumber()
		return AccessRule{}, err
	}
	return m.getRule(id)
}

func (m *AccessManager) getRule(id int64) (AccessRule, error) {
	r, err := scanRule(m.db.QueryRow(`SELECT `+ruleCols+` FROM access_rules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AccessRule{}, ErrNotFound
	}
	return r, err
}

// RuleUpdate is a partial change to a rule.
type RuleUpdate struct {
	Action  *string     `json:"action"`
	Terms   *[]RuleTerm `json:"terms"`
	Enabled *bool       `json:"enabled"`
	Comment *string     `json:"comment"`
}

func (m *AccessManager) UpdateRule(id int64, u RuleUpdate) (AccessRule, error) {
	cur, err := m.getRule(id)
	if err != nil {
		return AccessRule{}, err
	}
	next := cur
	if u.Action != nil {
		next.Action = *u.Action
	}
	if u.Terms != nil {
		next.Terms = *u.Terms
	}
	if u.Enabled != nil {
		next.Enabled = *u.Enabled
	}
	if u.Comment != nil {
		next.Comment = *u.Comment
	}
	if err := m.validateRule(next.Action, next.Terms, next.Comment); err != nil {
		return AccessRule{}, err
	}

	write := func(r AccessRule) error {
		j, _ := json.Marshal(cleanTerms(r.Terms))
		_, err := m.db.Exec(`UPDATE access_rules SET action = ?, terms = ?, enabled = ?, comment = ? WHERE id = ?`,
			r.Action, string(j), boolToInt(r.Enabled), r.Comment, id)
		return err
	}
	if err := write(next); err != nil {
		return AccessRule{}, err
	}
	if err := m.Regenerate(); err != nil {
		write(cur)
		return AccessRule{}, err
	}
	return m.getRule(id)
}

func (m *AccessManager) DeleteRule(id int64) error {
	cur, err := m.getRule(id)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec(`DELETE FROM access_rules WHERE id = ?`, id); err != nil {
		return err
	}
	m.renumber()
	if err := m.Regenerate(); err != nil {
		j, _ := json.Marshal(cleanTerms(cur.Terms))
		m.db.Exec(`INSERT INTO access_rules (id, position, action, terms, enabled, comment) VALUES (?, ?, ?, ?, ?, ?)`,
			cur.ID, cur.Position, cur.Action, string(j), boolToInt(cur.Enabled), cur.Comment)
		m.renumber()
		return err
	}
	return nil
}

// renumber makes positions contiguous (1..n) again.
func (m *AccessManager) renumber() {
	rules, err := m.rawRules(false)
	if err != nil {
		return
	}
	for i, r := range rules {
		if r.Position != i+1 {
			m.db.Exec(`UPDATE access_rules SET position = ? WHERE id = ?`, i+1, r.ID)
		}
	}
}

// ReorderRules sets the evaluation order. ids must list every rule exactly once.
func (m *AccessManager) ReorderRules(ids []int64) error {
	rules, err := m.rawRules(false)
	if err != nil {
		return err
	}
	if len(ids) != len(rules) {
		return fmt.Errorf("the new order must list all %d rules", len(rules))
	}
	known := map[int64]bool{}
	for _, r := range rules {
		known[r.ID] = true
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if !known[id] || seen[id] {
			return fmt.Errorf("the new order must list every rule exactly once")
		}
		seen[id] = true
	}

	set := func(order []int64) error {
		tx, err := m.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for i, id := range order {
			if _, err := tx.Exec(`UPDATE access_rules SET position = ? WHERE id = ?`, i+1, id); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	old := make([]int64, len(rules))
	for i, r := range rules {
		old[i] = r.ID
	}
	if err := set(ids); err != nil {
		return err
	}
	if err := m.Regenerate(); err != nil {
		set(old)
		return err
	}
	return nil
}

// --------------------------------------------------------------- squid.conf

// Regenerate rebuilds the managed access-rule block from the database. Only
// ACLs used by an enabled rule are written.
func (m *AccessManager) Regenerate() error {
	rules, err := m.rawRules(true)
	if err != nil {
		return err
	}
	acls, err := m.aclMap()
	if err != nil {
		return err
	}

	var aclLines, ruleLines []string
	usesAuth := false
	written := map[int64]bool{}
	for _, r := range rules {
		var terms []string
		usable := true
		for _, t := range proxyAuthLast(r.Terms, acls) {
			a, ok := acls[t.ACLID]
			if !ok {
				usable = false // dangling reference: skip the whole rule rather than guess
				break
			}
			if !written[a.ID] {
				aclLines = append(aclLines, renderACL(a, m.blocklistPath)...)
				written[a.ID] = true
				usesAuth = usesAuth || a.Type == "proxy_auth"
			}
			term := sqaACLName(a.Name)
			if t.Negate {
				term = "!" + term
			}
			terms = append(terms, term)
		}
		if !usable || len(terms) == 0 {
			continue
		}
		if r.Comment != "" {
			ruleLines = append(ruleLines, fmt.Sprintf("# rule %d: %s", r.ID, r.Comment))
		}
		ruleLines = append(ruleLines, fmt.Sprintf("http_access %s %s", r.Action, strings.Join(terms, " ")))
	}

	return m.confMgr.Update("access rules regenerated", func(content string) (string, error) {
		lines := stripManagedBlock(strings.Split(content, "\n"), accessBlockStart, accessBlockEnd)
		if len(ruleLines) == 0 {
			return strings.Join(lines, "\n"), nil
		}

		block := []string{accessBlockStart}
		if usesAuth {
			// squid refuses a proxy_auth ACL that comes before the auth_param
			// lines, and the stock proxy-authentication block sits later in the
			// file than these rules. Repeating auth_param is harmless (squid
			// accepts it), so the block carries its own copy.
			auth := authParamLines(lines)
			if len(auth) == 0 {
				return "", errors.New("a rule uses a proxy user condition, but proxy authentication is not set up yet: add a proxy user first (Foydalanuvchilar page)")
			}
			block = append(block, auth...)
		}
		block = append(block, aclLines...)
		block = append(block, ruleLines...)
		block = append(block, accessBlockEnd)

		lines = insertAt(lines, policyAnchor(lines), block)
		return strings.Join(lines, "\n"), nil
	})
}

// authParamLines returns the auth_param directives of the configuration.
func authParamLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "auth_param ") {
			out = append(out, t)
		}
	}
	return out
}

// proxyAuthLast puts proxy_auth conditions after all the others. squid checks
// a rule's ACLs left to right and, on reaching a proxy_auth ACL for a request
// without credentials, answers 407 (login required) — so a proxy_auth
// condition placed first would make squid ask *everyone* to log in, even for
// requests the other conditions would have excluded. "All conditions must
// match" does not depend on order, so moving it last only removes needless
// login prompts.
func proxyAuthLast(terms []RuleTerm, acls map[int64]ACLObject) []RuleTerm {
	out := make([]RuleTerm, 0, len(terms))
	var auth []RuleTerm
	for _, t := range terms {
		if acls[t.ACLID].Type == "proxy_auth" {
			auth = append(auth, t)
		} else {
			out = append(out, t)
		}
	}
	return append(out, auth...)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
