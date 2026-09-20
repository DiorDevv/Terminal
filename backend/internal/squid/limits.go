package squid

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Limit kinds.
const (
	LimitSpeed    = "speed"    // bandwidth, via squid's delay pools
	LimitDownload = "download" // largest single download, via reply_body_max_size
)

// Speed scopes: what one bucket is shared by.
const (
	ScopeEachUser   = "each_user"   // every proxy user gets their own bucket
	ScopeEachClient = "each_client" // every client IP gets its own bucket
	ScopeShared     = "shared"      // everyone the rule matches shares one bucket
)

// LimitSpec says who a limit applies to and how strict it is. Who is the union
// of the listed users, user groups and IP groups; Everyone overrides them.
type LimitSpec struct {
	Everyone   bool     `json:"everyone"`
	Users      []string `json:"users"`
	UserGroups []int64  `json:"user_groups"`
	IPGroups   []int64  `json:"ip_groups"`

	// speed
	Scope    string `json:"scope,omitempty"`
	RateKBps int    `json:"rate_kbps,omitempty"` // sustained speed, kilobytes per second
	BurstKB  int    `json:"burst_kb,omitempty"`  // bucket size; how much may go at full speed first

	// download
	MaxMB int `json:"max_mb,omitempty"`
}

type LimitRule struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Enabled bool      `json:"enabled"`
	Spec    LimitSpec `json:"spec"`
}

var limitNameRe = regexp.MustCompile(`^[A-Za-z0-9_ -]{2,40}$`)

func (p *UserPolicy) validateLimit(r *LimitRule) error {
	r.Name = strings.TrimSpace(r.Name)
	if !limitNameRe.MatchString(r.Name) {
		return bad("name: 2-40 letters, digits, spaces, '_' or '-'")
	}
	s := &r.Spec
	switch r.Kind {
	case LimitSpeed:
		switch s.Scope {
		case ScopeEachUser, ScopeEachClient, ScopeShared:
		default:
			return bad("scope must be each_user, each_client or shared")
		}
		if s.RateKBps < 1 || s.RateKBps > 10_000_000 {
			return bad("speed must be between 1 and 10000000 KB/s")
		}
		if s.BurstKB == 0 {
			s.BurstKB = s.RateKBps * 2 // two seconds at full speed
		}
		if s.BurstKB < s.RateKBps || s.BurstKB > 100_000_000 {
			return bad("burst must be at least the speed (KB) and at most 100000000 KB")
		}
		s.MaxMB = 0
	case LimitDownload:
		if s.MaxMB < 1 || s.MaxMB > 10_000_000 {
			return bad("largest download must be between 1 and 10000000 MB")
		}
		s.Scope, s.RateKBps, s.BurstKB = "", 0, 0
	default:
		return bad("kind must be speed or download")
	}

	if s.Everyone {
		s.Users, s.UserGroups, s.IPGroups = nil, nil, nil
	} else if len(s.Users) == 0 && len(s.UserGroups) == 0 && len(s.IPGroups) == 0 {
		return bad("choose who the limit applies to: everyone, users, user groups or IP groups")
	}
	if s.Scope == ScopeEachUser && !s.Everyone && len(s.Users) == 0 && len(s.UserGroups) == 0 {
		return bad("a per-user speed limit needs proxy users or user groups (IP groups do not identify users)")
	}

	names, err := p.users.ListUsers()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	for _, u := range s.Users {
		if !known[u] {
			return bad("no such proxy user: %s", u)
		}
	}
	for _, id := range s.UserGroups {
		var n int
		if err := p.db.QueryRow(`SELECT COUNT(*) FROM user_groups WHERE id = ?`, id).Scan(&n); err != nil || n == 0 {
			return bad("no such user group: %d", id)
		}
	}
	for _, id := range s.IPGroups {
		if _, err := NewGroupManager(p.db).Get(id); err != nil {
			return bad("no such IP group: %d", id)
		}
	}
	return nil
}

func (p *UserPolicy) Limits() ([]LimitRule, error) {
	rows, err := p.db.Query(`SELECT id, name, kind, enabled, spec FROM limit_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LimitRule{}
	for rows.Next() {
		var r LimitRule
		var en int
		var spec string
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &en, &spec); err != nil {
			return nil, err
		}
		r.Enabled = en != 0
		if err := json.Unmarshal([]byte(spec), &r.Spec); err != nil {
			return nil, fmt.Errorf("limit %d: unreadable spec: %w", r.ID, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *UserPolicy) saveLimit(r LimitRule, create bool) (LimitRule, bool, error) {
	if err := p.validateLimit(&r); err != nil {
		return LimitRule{}, false, err
	}
	spec, _ := json.Marshal(r.Spec)

	p.mu.Lock()
	defer p.mu.Unlock()

	var previous *LimitRule
	if !create {
		all, err := p.Limits()
		if err != nil {
			return LimitRule{}, false, err
		}
		for i := range all {
			if all[i].ID == r.ID {
				previous = &all[i]
			}
		}
		if previous == nil {
			return LimitRule{}, false, bad("no such limit")
		}
	}

	if create {
		res, err := p.db.Exec(`INSERT INTO limit_rules (name, kind, enabled, spec) VALUES (?, ?, ?, ?)`,
			r.Name, r.Kind, boolToInt(r.Enabled), string(spec))
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return LimitRule{}, false, bad("a limit named %q already exists", r.Name)
			}
			return LimitRule{}, false, err
		}
		r.ID, _ = res.LastInsertId()
	} else {
		res, err := p.db.Exec(`UPDATE limit_rules SET name = ?, kind = ?, enabled = ?, spec = ? WHERE id = ?`,
			r.Name, r.Kind, boolToInt(r.Enabled), string(spec), r.ID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return LimitRule{}, false, bad("a limit named %q already exists", r.Name)
			}
			return LimitRule{}, false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return LimitRule{}, false, bad("no such limit")
		}
	}

	changed, err := p.syncLocked()
	if err != nil {
		// Do not leave a limit in the database that squid.conf could not take.
		if create {
			p.db.Exec(`DELETE FROM limit_rules WHERE id = ?`, r.ID)
		} else if previous != nil {
			old, _ := json.Marshal(previous.Spec)
			p.db.Exec(`UPDATE limit_rules SET name = ?, kind = ?, enabled = ?, spec = ? WHERE id = ?`,
				previous.Name, previous.Kind, boolToInt(previous.Enabled), string(old), previous.ID)
		}
		return LimitRule{}, false, err
	}
	return r, changed, nil
}

func (p *UserPolicy) CreateLimit(r LimitRule) (LimitRule, bool, error) { return p.saveLimit(r, true) }
func (p *UserPolicy) UpdateLimit(r LimitRule) (LimitRule, bool, error) { return p.saveLimit(r, false) }

func (p *UserPolicy) DeleteLimit(id int64) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	res, err := p.db.Exec(`DELETE FROM limit_rules WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, bad("no such limit")
	}
	return p.syncLocked()
}

// ------------------------------------------------------------ squid.conf text

type limitOutput struct {
	lines    []string
	usesAuth bool
}

type resolved struct {
	users []string // proxy_auth ACL values
	cidrs []string // src ACL values
}

// resolve turns a selector into concrete values: the named users plus the
// members of the chosen user groups, and the addresses of the chosen IP groups.
func (p *UserPolicy) resolve(s LimitSpec, groups map[int64][]string, exist map[string]bool) resolved {
	seen := map[string]bool{}
	var res resolved
	add := func(u string) {
		if exist[u] && !seen[u] {
			seen[u] = true
			res.users = append(res.users, u)
		}
	}
	for _, u := range s.Users {
		add(u)
	}
	for _, id := range s.UserGroups {
		for _, u := range groups[id] {
			add(u)
		}
	}
	sort.Strings(res.users)

	gm := NewGroupManager(p.db)
	cseen := map[string]bool{}
	for _, id := range s.IPGroups {
		if g, err := gm.Get(id); err == nil {
			for _, c := range g.Members {
				if !cseen[c] {
					cseen[c] = true
					res.cidrs = append(res.cidrs, c)
				}
			}
		}
	}
	return res
}

func (p *UserPolicy) limitLines() (limitOutput, error) {
	rules, err := p.Limits()
	if err != nil {
		return limitOutput{}, err
	}
	names, err := p.users.ListUsers()
	if err != nil {
		return limitOutput{}, err
	}
	exist := map[string]bool{}
	for _, n := range names {
		exist[n] = true
	}
	groups := map[int64][]string{}
	ugs, err := p.Groups()
	if err != nil {
		return limitOutput{}, err
	}
	for _, g := range ugs {
		groups[g.ID] = g.Members
	}

	var out limitOutput
	var speedBlock, sizeBlock []string
	pools := 0

	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		// ACL names for this rule; one per selector kind that has members.
		var aclNames []string
		var aclLines []string
		if !r.Spec.Everyone {
			sel := p.resolve(r.Spec, groups, exist)
			if len(sel.users) > 0 {
				n := fmt.Sprintf("sqa_lim%d_users", r.ID)
				aclLines = append(aclLines, fmt.Sprintf("acl %s proxy_auth %s", n, strings.Join(sel.users, " ")))
				aclNames = append(aclNames, n)
				out.usesAuth = true
			}
			if len(sel.cidrs) > 0 {
				n := fmt.Sprintf("sqa_lim%d_net", r.ID)
				aclLines = append(aclLines, fmt.Sprintf("acl %s src %s", n, strings.Join(sel.cidrs, " ")))
				aclNames = append(aclNames, n)
			}
			if len(aclNames) == 0 {
				continue // matches nobody (an empty group): never write an empty ACL
			}
		}
		// A per-user bucket needs to know the user: class 4 keys on the login.
		if r.Kind == LimitSpeed && r.Spec.Scope == ScopeEachUser {
			out.usesAuth = true
		}

		comment := fmt.Sprintf("# limit %d: %s", r.ID, r.Name)
		switch r.Kind {
		case LimitSpeed:
			pools++
			rate, burst := r.Spec.RateKBps*1024, r.Spec.BurstKB*1024
			var class int
			var params string
			switch r.Spec.Scope {
			case ScopeShared:
				class, params = 1, fmt.Sprintf("%d/%d", rate, burst)
			case ScopeEachClient:
				class, params = 2, fmt.Sprintf("-1/-1 %d/%d", rate, burst)
			default: // each user
				class, params = 4, fmt.Sprintf("-1/-1 -1/-1 -1/-1 %d/%d", rate, burst)
			}
			speedBlock = append(speedBlock, comment)
			speedBlock = append(speedBlock, aclLines...)
			speedBlock = append(speedBlock,
				fmt.Sprintf("delay_class %d %d", pools, class),
				fmt.Sprintf("delay_parameters %d %s", pools, params))
			if r.Spec.Everyone {
				speedBlock = append(speedBlock, fmt.Sprintf("delay_access %d allow all", pools))
			} else {
				for _, n := range aclNames {
					speedBlock = append(speedBlock, fmt.Sprintf("delay_access %d allow %s", pools, n))
				}
			}
			speedBlock = append(speedBlock, fmt.Sprintf("delay_access %d deny all", pools))

		case LimitDownload:
			sizeBlock = append(sizeBlock, comment)
			sizeBlock = append(sizeBlock, aclLines...)
			if r.Spec.Everyone {
				sizeBlock = append(sizeBlock, fmt.Sprintf("reply_body_max_size %d MB", r.Spec.MaxMB))
			} else {
				for _, n := range aclNames {
					sizeBlock = append(sizeBlock, fmt.Sprintf("reply_body_max_size %d MB %s", r.Spec.MaxMB, n))
				}
			}
		}
	}

	if pools > 0 {
		// delay_pools must precede the delay_class lines that use it.
		out.lines = append(out.lines, fmt.Sprintf("delay_pools %d", pools))
	}
	out.lines = append(out.lines, speedBlock...)
	out.lines = append(out.lines, sizeBlock...)
	return out, nil
}
