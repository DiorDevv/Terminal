package squid

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CSV import and export of proxy accounts.
//
// Export never contains passwords (the passwd file only holds hashes and those
// are not the panel's to hand out). Import accepts a "password" column to
// create accounts or reset passwords.
//
// Import is all or nothing as far as the *data* goes: every row is validated
// first, and if any row is wrong nothing is changed. ?dry_run shows the outcome
// without applying it.

const (
	maxImportRows  = 1000
	maxImportBytes = 1 << 20
)

// csvColumns are the export columns; import accepts any subset (username is
// required), in any order.
var csvColumns = []string{"username", "status", "disabled", "expires", "daily_quota_mb", "groups", "note"}

type ImportResult struct {
	Line     int    `json:"line"`
	Username string `json:"username"`
	Action   string `json:"action"` // create, update, error
	Error    string `json:"error,omitempty"`
}

type ImportReport struct {
	DryRun  bool           `json:"dry_run"`
	Applied bool           `json:"applied"`
	Created int            `json:"created"`
	Updated int            `json:"updated"`
	Errors  int            `json:"errors"`
	Rows    []ImportResult `json:"rows"`
}

type importRow struct {
	line     int
	username string
	password string
	disabled *bool
	expires  *int64
	quotaMB  *int
	groups   []string
	hasGroup bool
	note     *string
}

// csvSafe defuses spreadsheet formulas: a cell that starts with = + - @ (or a
// control character) is executed by Excel and LibreOffice when the file is
// opened, so it gets a leading apostrophe.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// csvUnsafe undoes csvSafe on import so that an exported file round-trips.
func csvUnsafe(s string) string {
	if len(s) > 1 && s[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(s[1])) {
		return s[1:]
	}
	return s
}

// ExportCSV writes every account with its settings.
func (p *UserPolicy) ExportCSV(w io.Writer) error {
	users, err := p.Users()
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(csvColumns); err != nil {
		return err
	}
	for _, u := range users {
		expires := ""
		if u.ExpiresAt > 0 {
			expires = time.Unix(u.ExpiresAt, 0).Format(time.RFC3339)
		}
		groups := make([]string, 0, len(u.Groups))
		for _, g := range u.Groups {
			groups = append(groups, g.Name)
		}
		sort.Strings(groups)
		rec := []string{
			csvSafe(u.Username), u.Status, strconv.FormatBool(u.Disabled), expires,
			strconv.Itoa(u.DailyQuotaMB), csvSafe(strings.Join(groups, ";")), csvSafe(u.Note),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func parseBoolCell(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "ha", "disabled":
		return true, nil
	case "0", "false", "no", "n", "yo'q", "yoq", "active":
		return false, nil
	}
	return false, fmt.Errorf("%q is not true/false", s)
}

// parseExpiry accepts a date (valid through the end of that day, local time) or
// a full RFC 3339 timestamp.
func parseExpiry(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.AddDate(0, 0, 1).Unix(), nil
	}
	return 0, fmt.Errorf("%q is not a date (YYYY-MM-DD) or an RFC 3339 time", s)
}

// parseImport reads and validates the whole file. Problems are returned per
// row; rows that parse cleanly are returned in `rows`.
func parseImport(r io.Reader) (rows []importRow, results []ImportResult, fatal error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxImportBytes {
		return nil, nil, bad("the file is larger than %d KB", maxImportBytes/1024)
	}
	text := strings.TrimPrefix(string(data), "\ufeff") // Excel writes a BOM

	cr := csv.NewReader(strings.NewReader(text))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, nil, bad("not a valid CSV file: %v", err)
	}
	if len(records) == 0 {
		return nil, nil, bad("the file is empty")
	}
	if len(records)-1 > maxImportRows {
		return nil, nil, bad("at most %d accounts per file", maxImportRows)
	}

	col := map[string]int{}
	for i, h := range records[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		if _, dup := col[h]; dup {
			return nil, nil, bad("the column %q appears twice", h)
		}
		col[h] = i
	}
	if _, ok := col["username"]; !ok {
		return nil, nil, bad("the first row must be a header with at least a username column")
	}
	known := map[string]bool{"password": true}
	for _, c := range csvColumns {
		known[c] = true
	}
	for h := range col {
		if !known[h] {
			return nil, nil, bad("unknown column %q (allowed: username, password, disabled, expires, daily_quota_mb, groups, note)", h)
		}
	}

	get := func(rec []string, name string) (string, bool) {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return "", false
		}
		return csvUnsafe(strings.TrimSpace(rec[i])), true
	}

	seen := map[string]int{}
	for n, rec := range records[1:] {
		line := n + 2
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue // a blank line
		}
		row := importRow{line: line}
		var rowErr error
		fail := func(err error) {
			if rowErr == nil {
				rowErr = err
			}
		}

		row.username, _ = get(rec, "username")
		if err := validateUsername(row.username); err != nil {
			fail(err)
		} else if first, dup := seen[row.username]; dup {
			fail(fmt.Errorf("%s already appears on line %d", row.username, first))
		}
		seen[row.username] = line

		if i, ok := col["password"]; ok && i < len(rec) {
			row.password = rec[i] // exactly as written: no trimming, no apostrophe handling
			if row.password != "" && len(row.password) < 4 {
				fail(fmt.Errorf("password must be at least 4 characters"))
			}
		}
		if v, ok := get(rec, "disabled"); ok && v != "" {
			b, err := parseBoolCell(v)
			if err != nil {
				fail(fmt.Errorf("disabled: %v", err))
			}
			row.disabled = &b
		}
		if v, ok := get(rec, "expires"); ok && v != "" {
			e, err := parseExpiry(v)
			if err != nil {
				fail(fmt.Errorf("expires: %v", err))
			}
			row.expires = &e
		}
		if v, ok := get(rec, "daily_quota_mb"); ok && v != "" {
			q, err := strconv.Atoi(v)
			if err != nil || q < 0 || q > 10_000_000 {
				fail(fmt.Errorf("daily_quota_mb must be a whole number from 0 to 10000000"))
			}
			row.quotaMB = &q
		}
		if v, ok := get(rec, "groups"); ok && v != "" {
			row.hasGroup = true
			for _, g := range strings.Split(v, ";") {
				g = strings.TrimSpace(g)
				if g == "" {
					continue
				}
				if !groupNameRe.MatchString(g) {
					fail(fmt.Errorf("group name %q: 2-32 letters, digits, '_' or '-'", g))
				}
				row.groups = append(row.groups, g)
			}
		}
		if v, ok := get(rec, "note"); ok && v != "" {
			if len(v) > 200 {
				fail(fmt.Errorf("note is too long (200 characters at most)"))
			}
			row.note = &v
		}

		if rowErr != nil {
			results = append(results, ImportResult{Line: line, Username: row.username, Action: "error", Error: rowErr.Error()})
			continue
		}
		rows = append(rows, row)
	}
	if len(rows)+len(results) == 0 {
		return nil, nil, bad("the file has a header but no accounts")
	}
	return rows, results, nil
}

// ImportCSV validates the file and, unless dryRun, applies it. changed reports
// whether squid must be reloaded (accounts or squid.conf changed).
func (p *UserPolicy) ImportCSV(r io.Reader, dryRun bool) (report ImportReport, changed bool, err error) {
	rows, problems, err := parseImport(r)
	if err != nil {
		return ImportReport{}, false, err
	}
	report.DryRun = dryRun

	names, err := p.users.ListUsers()
	if err != nil {
		return ImportReport{}, false, err
	}
	exists := map[string]bool{}
	for _, n := range names {
		exists[n] = true
	}

	// A new account needs a password to exist at all.
	var valid []importRow
	for _, row := range rows {
		if !exists[row.username] && row.password == "" {
			problems = append(problems, ImportResult{Line: row.line, Username: row.username, Action: "error",
				Error: "a new account needs a password"})
			continue
		}
		valid = append(valid, row)
	}
	for _, row := range valid {
		action := "update"
		if !exists[row.username] {
			action = "create"
		}
		report.Rows = append(report.Rows, ImportResult{Line: row.line, Username: row.username, Action: action})
	}
	report.Rows = append(report.Rows, problems...)
	sort.Slice(report.Rows, func(i, j int) bool { return report.Rows[i].Line < report.Rows[j].Line })
	for _, r := range report.Rows {
		switch r.Action {
		case "create":
			report.Created++
		case "update":
			report.Updated++
		default:
			report.Errors++
		}
	}

	// One bad row and nothing is applied: half an import is hard to reason about.
	if dryRun || report.Errors > 0 {
		return report, false, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	createdAny := false
	for _, row := range valid {
		if row.password != "" {
			if err := p.users.AddUser(row.username, row.password); err != nil {
				return report, createdAny, fmt.Errorf("line %d (%s): %w", row.line, row.username, err)
			}
			if !exists[row.username] {
				createdAny = true
			}
		}
	}
	if createdAny {
		if err := p.users.EnsureAuthDirectives(); err != nil {
			return report, true, fmt.Errorf("accounts were created but proxy authentication could not be enabled: %w", err)
		}
	}

	if err := p.applyImportSettings(valid); err != nil {
		return report, createdAny, err
	}
	report.Applied = true

	syncChanged, err := p.syncLocked()
	return report, createdAny || syncChanged, err
}

// applyImportSettings writes every row's settings in one transaction.
func (p *UserPolicy) applyImportSettings(rows []importRow) error {
	cur, err := p.loadMeta()
	if err != nil {
		return err
	}
	tx, err := p.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := p.now().Unix()
	groupID := map[string]int64{}
	ensureGroup := func(name string) (int64, error) {
		if id, ok := groupID[name]; ok {
			return id, nil
		}
		var id int64
		err := tx.QueryRow(`SELECT id FROM user_groups WHERE name = ?`, name).Scan(&id)
		if err != nil {
			res, err := tx.Exec(`INSERT INTO user_groups (name) VALUES (?)`, name)
			if err != nil {
				return 0, err
			}
			id, _ = res.LastInsertId()
		}
		groupID[name] = id
		return id, nil
	}

	for _, row := range rows {
		m, existed := cur[row.username]
		if !existed {
			m.createdAt = now
		}
		if row.disabled != nil {
			m.disabled = *row.disabled
		}
		if row.expires != nil {
			m.expiresAt = *row.expires
		}
		if row.quotaMB != nil {
			m.quotaMB = *row.quotaMB
		}
		if row.note != nil {
			m.note = *row.note
		}
		if _, err := tx.Exec(`INSERT INTO proxy_users (username, disabled, expires_at, daily_quota_mb, note, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(username) DO UPDATE SET disabled = excluded.disabled, expires_at = excluded.expires_at,
				daily_quota_mb = excluded.daily_quota_mb, note = excluded.note`,
			row.username, boolToInt(m.disabled), m.expiresAt, m.quotaMB, m.note, m.createdAt); err != nil {
			return err
		}
		if row.hasGroup {
			for _, g := range row.groups {
				id, err := ensureGroup(g)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO user_group_members (group_id, username) VALUES (?, ?)`, id, row.username); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}
