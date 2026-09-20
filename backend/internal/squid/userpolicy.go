package squid

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// UserPolicy is everything the panel knows about proxy accounts beyond their
// passwords: switched off, expired, over the daily quota, group membership,
// and the speed / download limits that apply to them. The passwd file stays
// the only place credentials live; this turns the rest into squid.conf.
//
// Two managed blocks are written, both regenerated from the database in one
// squid.conf update (one history entry, one reload):
//
//	user-policy  a deny rule for the accounts that may not use the proxy right now
//	limits       delay pools (speed) and reply_body_max_size (download size)
type UserPolicy struct {
	db    *sql.DB
	conf  *Manager
	users *UserManager

	// usage returns the bytes each user has transferred since the given unix
	// time. It comes from the statistics tables (nil disables daily quotas).
	usage func(since int64) (map[string]int64, error)
	// now is replaced in tests.
	now func() time.Time

	mu sync.Mutex
}

func NewUserPolicy(db *sql.DB, conf *Manager, users *UserManager, usage func(since int64) (map[string]int64, error)) *UserPolicy {
	return &UserPolicy{db: db, conf: conf, users: users, usage: usage, now: time.Now}
}

const (
	userPolicyBlockStart = "# --- squidadmin: user-policy (managed, do not edit by hand) ---"
	userPolicyBlockEnd   = "# --- end squidadmin: user-policy ---"
	limitsBlockStart     = "# --- squidadmin: limits (managed, do not edit by hand) ---"
	limitsBlockEnd       = "# --- end squidadmin: limits ---"
)

// Account states shown in the UI.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusExpired  = "expired"
	StatusQuota    = "quota_exceeded"
)

type GroupRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ProxyUser is one proxy account as the panel shows it.
type ProxyUser struct {
	Username     string     `json:"username"`
	Disabled     bool       `json:"disabled"`
	ExpiresAt    int64      `json:"expires_at"` // unix seconds, 0 = never
	DailyQuotaMB int        `json:"daily_quota_mb"`
	Note         string     `json:"note"`
	CreatedAt    int64      `json:"created_at"`
	Groups       []GroupRef `json:"groups"`
	UsedToday    int64      `json:"used_today"` // bytes
	Status       string     `json:"status"`
}

// UserPatch changes some of an account's settings; nil fields stay as they are.
type UserPatch struct {
	Disabled     *bool   `json:"disabled"`
	ExpiresAt    *int64  `json:"expires_at"`
	DailyQuotaMB *int    `json:"daily_quota_mb"`
	Note         *string `json:"note"`
}

// PolicyError reports a rejected value.
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &PolicyError{fmt.Sprintf(format, a...)} }

// dayStart is local midnight of the day containing t.
func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

type meta struct {
	disabled  bool
	expiresAt int64
	quotaMB   int
	note      string
	createdAt int64
}

func (p *UserPolicy) loadMeta() (map[string]meta, error) {
	rows, err := p.db.Query(`SELECT username, disabled, expires_at, daily_quota_mb, note, created_at FROM proxy_users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]meta{}
	for rows.Next() {
		var name string
		var m meta
		var dis int
		if err := rows.Scan(&name, &dis, &m.expiresAt, &m.quotaMB, &m.note, &m.createdAt); err != nil {
			return nil, err
		}
		m.disabled = dis != 0
		out[name] = m
	}
	return out, rows.Err()
}

// usageToday returns today's bytes per user; a missing statistics source means
// "unknown", which never blocks anybody.
func (p *UserPolicy) usageToday() map[string]int64 {
	if p.usage == nil {
		return map[string]int64{}
	}
	u, err := p.usage(dayStart(p.now()).Unix())
	if err != nil || u == nil {
		return map[string]int64{}
	}
	return u
}

func statusOf(m meta, used int64, now time.Time) string {
	switch {
	case m.disabled:
		return StatusDisabled
	case m.expiresAt > 0 && now.Unix() >= m.expiresAt:
		return StatusExpired
	case m.quotaMB > 0 && used >= int64(m.quotaMB)*1024*1024:
		return StatusQuota
	}
	return StatusActive
}

// Users lists every account in the passwd file with its settings and state.
func (p *UserPolicy) Users() ([]ProxyUser, error) {
	names, err := p.users.ListUsers()
	if err != nil {
		return nil, err
	}
	metas, err := p.loadMeta()
	if err != nil {
		return nil, err
	}
	groups, err := p.groupsOf()
	if err != nil {
		return nil, err
	}
	used := p.usageToday()
	now := p.now()

	sort.Strings(names)
	out := make([]ProxyUser, 0, len(names))
	for _, n := range names {
		m := metas[n]
		u := ProxyUser{
			Username: n, Disabled: m.disabled, ExpiresAt: m.expiresAt, DailyQuotaMB: m.quotaMB,
			Note: m.note, CreatedAt: m.createdAt, Groups: groups[n], UsedToday: used[n],
			Status: statusOf(m, used[n], now),
		}
		if u.Groups == nil {
			u.Groups = []GroupRef{}
		}
		out = append(out, u)
	}
	return out, nil
}

func (p *UserPolicy) groupsOf() (map[string][]GroupRef, error) {
	rows, err := p.db.Query(`SELECT m.username, g.id, g.name FROM user_group_members m JOIN user_groups g ON g.id = m.group_id ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]GroupRef{}
	for rows.Next() {
		var u string
		var g GroupRef
		if err := rows.Scan(&u, &g.ID, &g.Name); err != nil {
			return nil, err
		}
		out[u] = append(out[u], g)
	}
	return out, rows.Err()
}

func (p *UserPolicy) exists(username string) (bool, error) {
	names, err := p.users.ListUsers()
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == username {
			return true, nil
		}
	}
	return false, nil
}

// Update changes an account's settings. The caller reloads squid when the
// returned changed flag is set.
func (p *UserPolicy) Update(username string, patch UserPatch) (changed bool, err error) {
	if ok, err := p.exists(username); err != nil {
		return false, err
	} else if !ok {
		return false, bad("no such proxy user: %s", username)
	}
	if patch.DailyQuotaMB != nil && (*patch.DailyQuotaMB < 0 || *patch.DailyQuotaMB > 10_000_000) {
		return false, bad("daily quota must be between 0 (unlimited) and 10000000 MB")
	}
	if patch.ExpiresAt != nil && *patch.ExpiresAt < 0 {
		return false, bad("expiry must be a unix time, or 0 for never")
	}
	if patch.Note != nil && len(*patch.Note) > 200 {
		return false, bad("note is too long (200 characters at most)")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	cur, err := p.loadMeta()
	if err != nil {
		return false, err
	}
	m, existed := cur[username]
	before := m
	if !existed {
		m.createdAt = p.now().Unix()
	}
	if patch.Disabled != nil {
		m.disabled = *patch.Disabled
	}
	if patch.ExpiresAt != nil {
		m.expiresAt = *patch.ExpiresAt
	}
	if patch.DailyQuotaMB != nil {
		m.quotaMB = *patch.DailyQuotaMB
	}
	if patch.Note != nil {
		m.note = strings.TrimSpace(*patch.Note)
	}
	if _, err := p.db.Exec(`INSERT INTO proxy_users (username, disabled, expires_at, daily_quota_mb, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET disabled = excluded.disabled, expires_at = excluded.expires_at,
			daily_quota_mb = excluded.daily_quota_mb, note = excluded.note`,
		username, boolToInt(m.disabled), m.expiresAt, m.quotaMB, m.note, m.createdAt); err != nil {
		return false, fmt.Errorf("save user settings: %w", err)
	}
	changed, err = p.syncLocked()
	if err != nil {
		// squid.conf could not take the change: do not keep it in the database.
		if existed {
			p.db.Exec(`UPDATE proxy_users SET disabled = ?, expires_at = ?, daily_quota_mb = ?, note = ? WHERE username = ?`,
				boolToInt(before.disabled), before.expiresAt, before.quotaMB, before.note, username)
		} else {
			p.db.Exec(`DELETE FROM proxy_users WHERE username = ?`, username)
		}
	}
	return changed, err
}

// Forget removes what the panel stores about an account that was deleted.
func (p *UserPolicy) Forget(username string) (changed bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.db.Exec(`DELETE FROM proxy_users WHERE username = ?`, username); err != nil {
		return false, err
	}
	if _, err := p.db.Exec(`DELETE FROM user_group_members WHERE username = ?`, username); err != nil {
		return false, err
	}
	return p.syncLocked()
}

// ---------------------------------------------------------------- user groups

var groupNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,32}$`)

type UserGroup struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func (p *UserPolicy) Groups() ([]UserGroup, error) {
	rows, err := p.db.Query(`SELECT id, name FROM user_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserGroup{}
	byID := map[int64]int{}
	for rows.Next() {
		var g UserGroup
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		g.Members = []string{}
		byID[g.ID] = len(out)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	mrows, err := p.db.Query(`SELECT group_id, username FROM user_group_members ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var id int64
		var u string
		if err := mrows.Scan(&id, &u); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Members = append(out[i].Members, u)
		}
	}
	return out, mrows.Err()
}

func (p *UserPolicy) CreateGroup(name string) (UserGroup, error) {
	name = strings.TrimSpace(name)
	if !groupNameRe.MatchString(name) {
		return UserGroup{}, bad("group name: 2-32 letters, digits, '_' or '-'")
	}
	res, err := p.db.Exec(`INSERT INTO user_groups (name) VALUES (?)`, name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return UserGroup{}, bad("a user group named %q already exists", name)
		}
		return UserGroup{}, err
	}
	id, _ := res.LastInsertId()
	return UserGroup{ID: id, Name: name, Members: []string{}}, nil
}

// DeleteGroup removes a group. Limits that referred to it stop matching its
// (former) members on the next sync, which the returned flag reports.
func (p *UserPolicy) DeleteGroup(id int64) (changed bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res, err := p.db.Exec(`DELETE FROM user_groups WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, bad("no such user group")
	}
	return p.syncLocked()
}

// SetMembers replaces a group's member list; every member must be a proxy user.
func (p *UserPolicy) SetMembers(id int64, members []string) (changed bool, err error) {
	names, err := p.users.ListUsers()
	if err != nil {
		return false, err
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	seen := map[string]bool{}
	var clean []string
	for _, m := range members {
		m = strings.TrimSpace(m)
		if !known[m] {
			return false, bad("no such proxy user: %s", m)
		}
		if !seen[m] {
			seen[m] = true
			clean = append(clean, m)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	var exists int
	if err := p.db.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		return false, bad("no such user group")
	}
	tx, err := p.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM user_group_members WHERE group_id = ?`, id); err != nil {
		return false, err
	}
	for _, m := range clean {
		if _, err := tx.Exec(`INSERT INTO user_group_members (group_id, username) VALUES (?, ?)`, id, m); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return p.syncLocked()
}

// ------------------------------------------------------------------- syncing

// Sync rebuilds both managed blocks from the database and the current usage.
// It reports whether squid.conf changed, in which case squid must be reloaded.
func (p *UserPolicy) Sync() (changed bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.syncLocked()
}

// Blocked returns the accounts that may not use the proxy right now, sorted.
func (p *UserPolicy) Blocked() ([]string, error) {
	names, err := p.users.ListUsers()
	if err != nil {
		return nil, err
	}
	metas, err := p.loadMeta()
	if err != nil {
		return nil, err
	}
	used := p.usageToday()
	now := p.now()

	var out []string
	for _, n := range names {
		if m, ok := metas[n]; ok && statusOf(m, used[n], now) != StatusActive {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (p *UserPolicy) syncLocked() (bool, error) {
	blocked, err := p.Blocked()
	if err != nil {
		return false, err
	}
	limits, err := p.limitLines()
	if err != nil {
		return false, err
	}

	changed := false
	err = p.conf.Update("user policy regenerated", func(content string) (string, error) {
		lines := strings.Split(content, "\n")
		lines = stripManagedBlock(lines, userPolicyBlockStart, userPolicyBlockEnd)
		lines = stripManagedBlock(lines, limitsBlockStart, limitsBlockEnd)
		auth := authParamLines(lines)

		if len(blocked) > 0 {
			if len(auth) == 0 {
				return "", errors.New("proxy authentication is not set up yet: add a proxy user first")
			}
			// The rule only fires for requests that carry credentials, so a
			// blocked account is refused wherever it logs in, while requests
			// without credentials are not made to log in just for this rule.
			//
			// squid answers a request denied by a proxy_auth rule with 407 (login
			// again), which makes a browser ask for the password over and over.
			// deny_info with an explicit status turns that into a plain 403.
			block := []string{userPolicyBlockStart}
			block = append(block, auth...)
			block = append(block,
				"acl sqa_up_auth req_header Proxy-Authorization .",
				"acl sqa_up_blocked proxy_auth "+strings.Join(blocked, " "),
				"http_access deny sqa_up_auth sqa_up_blocked",
				"deny_info 403:ERR_ACCESS_DENIED sqa_up_blocked",
				userPolicyBlockEnd)
			lines = insertAt(lines, userPolicyAnchor(lines), block)
		}

		if len(limits.lines) > 0 {
			// The block goes at the very end, after every auth_param line, so it
			// needs no copy of them; without any, a proxy_auth ACL cannot work.
			if limits.usesAuth && len(auth) == 0 {
				return "", errors.New("a limit applies to proxy users, but proxy authentication is not set up yet: add a proxy user first")
			}
			block := []string{limitsBlockStart}
			block = append(block, limits.lines...)
			block = append(block, limitsBlockEnd)
			lines = appendBlock(lines, block)
		}

		next := strings.Join(lines, "\n")
		changed = next != content
		return next, nil
	})
	return changed, err
}

// appendBlock adds block at the end of the file. The limit directives are
// self-contained: they define the ACLs they use. Removing the block again
// (stripManagedBlock) gives back the previous text exactly.
func appendBlock(lines, block []string) []string {
	// The file ends with an empty last element (its final newline): keep it last.
	end := len(lines)
	if end > 0 && lines[end-1] == "" {
		end--
	}
	out := append([]string{}, lines[:end]...)
	out = append(out, block...)
	return append(out, "")
}

// userPolicyAnchor is where the user-policy block goes. All the panel's deny
// blocks sit before the first allow rule; to keep their relative order fixed no
// matter which one was regenerated last, this one always sits right before the
// time-restriction block when there is one:
//
//	user-policy -> time restrictions -> user access rules -> stock allows
//
// Without a fixed place, two blocks that both insert "before the first allow"
// swap places on every regeneration and the config would change on every sync.
func userPolicyAnchor(lines []string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == restrictionBlockStart {
			return i
		}
	}
	return denyStageAnchor(lines)
}

// Run re-evaluates expiry and quotas every interval until stop closes, and
// reloads squid whenever the set of blocked accounts (or a limit) changed.
func (p *UserPolicy) Run(stop <-chan struct{}, every time.Duration, log func(format string, a ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			changed, err := p.Sync()
			if err != nil {
				log("user policy: %v", err)
				continue
			}
			if changed {
				if err := p.conf.Reconfigure(); err != nil {
					log("user policy: reload failed: %v", err)
				}
			}
		}
	}
}
