package squid

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"squidadmin/backend/internal/db"
)

type upRig struct {
	t      *testing.T
	up     *UserPolicy
	mgr    *Manager
	conf   string
	dir    string // holds the fake squid; touching fail_parse there breaks parsing
	passwd string
	clock  time.Time
	usage  map[string]int64
	since  int64 // the "since" the usage function was last asked for
}

func newUPRig(t *testing.T, users ...string) *upRig {
	t.Helper()
	mgr, conf, dir := settingsManager(t, stockConf)

	passwd := filepath.Join(t.TempDir(), "passwd")
	var b strings.Builder
	for _, u := range users {
		b.WriteString(u + ":$apr1$abcdefgh$notarealhash\n")
	}
	os.WriteFile(passwd, []byte(b.String()), 0o644)

	um := NewUserManager(passwd, "authenticated_users", "/usr/lib/squid/basic_ncsa_auth", fakeHtpasswd(t), mgr)
	if len(users) > 0 {
		if err := um.EnsureAuthDirectives(); err != nil {
			t.Fatalf("EnsureAuthDirectives: %v", err)
		}
	}

	conn, err := db.Open(filepath.Join(t.TempDir(), "up.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	r := &upRig{t: t, mgr: mgr, conf: conf, dir: dir, passwd: passwd,
		clock: time.Date(2026, 9, 19, 14, 30, 0, 0, time.Local), usage: map[string]int64{}}
	r.up = NewUserPolicy(conn, mgr, um, func(since int64) (map[string]int64, error) {
		r.since = since
		out := map[string]int64{}
		for k, v := range r.usage {
			out[k] = v
		}
		return out, nil
	})
	r.up.now = func() time.Time { return r.clock }
	return r
}

func (r *upRig) text() string {
	r.t.Helper()
	b, err := os.ReadFile(r.conf)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(b)
}

func (r *upRig) sync() bool {
	r.t.Helper()
	changed, err := r.up.Sync()
	if err != nil {
		r.t.Fatalf("Sync: %v", err)
	}
	return changed
}

func lineIndex(text, needle string) int {
	for i, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == needle {
			return i
		}
	}
	return -1
}

func has(text, sub string) bool { return strings.Contains(text, sub) }

func bptr(b bool) *bool     { return &b }
func iptr(i int) *int       { return &i }
func i64ptr(i int64) *int64 { return &i }

// ------------------------------------------------------------ account state

func TestDisablingAnAccountWritesAStableDenyRuleAndEnablingRemovesItExactly(t *testing.T) {
	r := newUPRig(t, "alice", "bob")
	before := r.text()

	changed, err := r.up.Update("bob", UserPatch{Disabled: bptr(true)})
	if err != nil || !changed {
		t.Fatalf("Update: changed=%v err=%v", changed, err)
	}
	txt := r.text()
	for _, want := range []string{
		userPolicyBlockStart,
		"acl sqa_up_auth req_header Proxy-Authorization .",
		"acl sqa_up_blocked proxy_auth bob",
		"http_access deny sqa_up_auth sqa_up_blocked",
		"deny_info 403:ERR_ACCESS_DENIED sqa_up_blocked", // a plain 403 instead of squid's 407 login loop
		userPolicyBlockEnd,
	} {
		if !has(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	if has(txt, "proxy_auth alice") {
		t.Error("an active account must not be in the blocked list")
	}
	// squid refuses a proxy_auth ACL before its auth_param: the block carries a copy.
	blockStart := lineIndex(txt, userPolicyBlockStart)
	authCopy := -1
	for i, l := range strings.Split(txt, "\n") {
		if i > blockStart && strings.HasPrefix(strings.TrimSpace(l), "auth_param basic program") {
			authCopy = i
			break
		}
	}
	if authCopy == -1 || authCopy > lineIndex(txt, "acl sqa_up_blocked proxy_auth bob") {
		t.Error("the block must repeat auth_param before it uses a proxy_auth ACL")
	}
	// It must be evaluated before any allow rule, or an allowed client would skip it.
	if lineIndex(txt, "http_access deny sqa_up_auth sqa_up_blocked") > lineIndex(txt, "http_access allow localhost") {
		t.Error("the deny rule must come before the first allow rule")
	}

	if changed, err := r.up.Sync(); err != nil || changed {
		t.Errorf("syncing again must change nothing: changed=%v err=%v", changed, err)
	}

	if changed, err := r.up.Update("bob", UserPatch{Disabled: bptr(false)}); err != nil || !changed {
		t.Fatalf("re-enable: changed=%v err=%v", changed, err)
	}
	if r.text() != before {
		t.Errorf("re-enabling must give back the exact previous squid.conf:\n%s", r.text())
	}
}

func TestExpiryTakesEffectWhenTheClockPassesIt(t *testing.T) {
	r := newUPRig(t, "alice")
	expires := r.clock.Add(2 * time.Hour).Unix()
	if _, err := r.up.Update("alice", UserPatch{ExpiresAt: i64ptr(expires)}); err != nil {
		t.Fatal(err)
	}
	if has(r.text(), "sqa_up_blocked") {
		t.Fatal("not expired yet")
	}
	users, _ := r.up.Users()
	if users[0].Status != StatusActive || users[0].ExpiresAt != expires {
		t.Fatalf("state before expiry: %+v", users[0])
	}

	r.clock = r.clock.Add(2*time.Hour - time.Second)
	if r.sync() {
		t.Fatal("one second before the expiry nothing may change")
	}
	r.clock = r.clock.Add(time.Second) // exactly at the expiry: inclusive
	if !r.sync() || !has(r.text(), "acl sqa_up_blocked proxy_auth alice") {
		t.Fatal("at the expiry time the account must be blocked")
	}
	if users, _ := r.up.Users(); users[0].Status != StatusExpired {
		t.Errorf("status = %s", users[0].Status)
	}

	// Extending the expiry brings the account back.
	if changed, _ := r.up.Update("alice", UserPatch{ExpiresAt: i64ptr(r.clock.Add(24 * time.Hour).Unix())}); !changed || has(r.text(), "sqa_up_blocked") {
		t.Error("a later expiry must unblock the account")
	}
	// 0 means never.
	r.up.Update("alice", UserPatch{ExpiresAt: i64ptr(1)})
	if !has(r.text(), "sqa_up_blocked") {
		t.Fatal("an expiry in the past blocks at once")
	}
	r.up.Update("alice", UserPatch{ExpiresAt: i64ptr(0)})
	if has(r.text(), "sqa_up_blocked") {
		t.Error("expiry 0 must mean never")
	}
}

func TestDailyQuotaBlocksAtTheLimitAndResetsAtMidnight(t *testing.T) {
	r := newUPRig(t, "alice", "bob")
	r.up.Update("alice", UserPatch{DailyQuotaMB: iptr(10)})
	r.up.Update("bob", UserPatch{DailyQuotaMB: iptr(10)})

	r.usage["alice"] = 10*1024*1024 - 1
	r.usage["bob"] = 3 * 1024 * 1024
	if r.sync() {
		t.Fatal("under the quota nobody is blocked")
	}
	wantSince := time.Date(2026, 9, 19, 0, 0, 0, 0, time.Local).Unix()
	if r.since != wantSince {
		t.Errorf("usage must be asked since local midnight (%d), was %d", wantSince, r.since)
	}

	r.usage["alice"] = 10 * 1024 * 1024 // exactly the quota: used up
	if !r.sync() || !has(r.text(), "proxy_auth alice") || has(r.text(), "proxy_auth alice bob") {
		t.Fatalf("alice is at her quota and must be blocked alone:\n%s", r.text())
	}
	users, _ := r.up.Users()
	if users[0].Status != StatusQuota || users[0].UsedToday != 10*1024*1024 || users[1].Status != StatusActive {
		t.Errorf("states: %+v", users)
	}

	// Next morning: the statistics for the new day are empty.
	r.clock = time.Date(2026, 9, 20, 0, 0, 5, 0, time.Local)
	r.usage = map[string]int64{}
	if !r.sync() || has(r.text(), "sqa_up_blocked") {
		t.Error("a new day must lift the quota block")
	}
	if want := time.Date(2026, 9, 20, 0, 0, 0, 0, time.Local).Unix(); r.since != want {
		t.Errorf("the new day starts at %d, usage was asked since %d", want, r.since)
	}

	// Quota 0 = unlimited, however much was used.
	r.usage["alice"] = 1 << 40
	r.up.Update("alice", UserPatch{DailyQuotaMB: iptr(0)})
	if has(r.text(), "sqa_up_blocked") {
		t.Error("no quota, no block")
	}
}

func TestAnUnavailableUsageSourceNeverBlocksAnyone(t *testing.T) {
	r := newUPRig(t, "alice")
	r.up.usage = func(int64) (map[string]int64, error) { return nil, errors.New("statistics are down") }
	r.up.Update("alice", UserPatch{DailyQuotaMB: iptr(1)})
	if has(r.text(), "sqa_up_blocked") {
		t.Error("unknown usage must not be treated as an exceeded quota")
	}
}

func TestPolicyValidationAndUnknownUsers(t *testing.T) {
	r := newUPRig(t, "alice")
	bad := []UserPatch{
		{DailyQuotaMB: iptr(-1)},
		{DailyQuotaMB: iptr(10_000_001)},
		{ExpiresAt: i64ptr(-5)},
		{Note: strPtr(strings.Repeat("x", 201))},
	}
	for _, p := range bad {
		var pe *PolicyError
		if _, err := r.up.Update("alice", p); !errors.As(err, &pe) {
			t.Errorf("%+v must be rejected: %v", p, err)
		}
	}
	var pe *PolicyError
	if _, err := r.up.Update("ghost", UserPatch{Disabled: bptr(true)}); !errors.As(err, &pe) {
		t.Errorf("an unknown account must be refused: %v", err)
	}
	if has(r.text(), "ghost") {
		t.Error("nothing may be written for an unknown account")
	}
	if _, err := r.up.Update("alice", UserPatch{Note: strPtr("  night shift  ")}); err != nil {
		t.Fatal(err)
	}
	if u, _ := r.up.Users(); u[0].Note != "night shift" {
		t.Errorf("the note is trimmed: %q", u[0].Note)
	}
}

func strPtr(s string) *string { return &s }

func TestBlockingNeedsProxyAuthenticationToBeSetUp(t *testing.T) {
	r := newUPRig(t) // no users, so EnsureAuthDirectives never ran
	os.WriteFile(r.passwd, []byte("alice:$apr1$x$y\n"), 0o644)
	before := r.text()
	if _, err := r.up.Update("alice", UserPatch{Disabled: bptr(true)}); err == nil {
		t.Fatal("a proxy_auth ACL without auth_param would make squid refuse the config")
	}
	if r.text() != before {
		t.Error("a refused change must leave squid.conf untouched")
	}
}

func TestForgetRemovesSettingsAndMemberships(t *testing.T) {
	r := newUPRig(t, "alice", "bob")
	g, _ := r.up.CreateGroup("staff")
	r.up.SetMembers(g.ID, []string{"alice", "bob"})
	r.up.Update("alice", UserPatch{Disabled: bptr(true)})

	os.WriteFile(r.passwd, []byte("bob:$apr1$x$y\n"), 0o644) // alice was deleted
	if changed, err := r.up.Forget("alice"); err != nil || !changed {
		t.Fatalf("Forget: %v %v", changed, err)
	}
	if has(r.text(), "sqa_up_blocked") {
		t.Error("a deleted account must not stay in the blocked list")
	}
	groups, _ := r.up.Groups()
	if len(groups[0].Members) != 1 || groups[0].Members[0] != "bob" {
		t.Errorf("members after delete: %v", groups[0].Members)
	}
}

// -------------------------------------------------------------- user groups

func TestUserGroups(t *testing.T) {
	r := newUPRig(t, "alice", "bob")

	for _, name := range []string{"", "a", "has space", strings.Repeat("x", 33), "semi;colon"} {
		var pe *PolicyError
		if _, err := r.up.CreateGroup(name); !errors.As(err, &pe) {
			t.Errorf("group name %q must be refused: %v", name, err)
		}
	}
	g, err := r.up.CreateGroup("staff")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.up.CreateGroup("staff"); err == nil {
		t.Error("duplicate group names must be refused")
	}

	if _, err := r.up.SetMembers(g.ID, []string{"alice", "ghost"}); err == nil {
		t.Error("only existing proxy users may be members")
	}
	if _, err := r.up.SetMembers(g.ID, []string{"alice", "bob", "alice"}); err != nil {
		t.Fatal(err)
	}
	groups, _ := r.up.Groups()
	if len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Errorf("duplicates must collapse: %+v", groups)
	}
	users, _ := r.up.Users()
	if len(users[0].Groups) != 1 || users[0].Groups[0].Name != "staff" {
		t.Errorf("the user list shows memberships: %+v", users[0].Groups)
	}
	if _, err := r.up.SetMembers(9999, nil); err == nil {
		t.Error("an unknown group must be refused")
	}
	if _, err := r.up.SetMembers(g.ID, nil); err != nil {
		t.Fatal(err)
	}
	if groups, _ := r.up.Groups(); len(groups[0].Members) != 0 {
		t.Error("an empty list clears the group")
	}
	if _, err := r.up.DeleteGroup(g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.up.DeleteGroup(g.ID); err == nil {
		t.Error("deleting twice is an error")
	}
}

// ------------------------------------------------------------------- limits

func speedRule(name, scope string, rate int, who LimitSpec) LimitRule {
	who.Scope, who.RateKBps = scope, rate
	return LimitRule{Name: name, Kind: LimitSpeed, Enabled: true, Spec: who}
}

func TestSpeedLimitsBecomeDelayPools(t *testing.T) {
	r := newUPRig(t, "alice", "bob", "carol")
	staff, _ := r.up.CreateGroup("staff")
	r.up.SetMembers(staff.ID, []string{"bob", "carol"})
	ipg, err := NewGroupManager(r.up.db).Create("office", []string{"10.1.0.0/16", "10.2.0.5"})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := r.up.CreateLimit(speedRule("per user", ScopeEachUser, 50, LimitSpec{Users: []string{"alice"}, UserGroups: []int64{staff.ID}})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.up.CreateLimit(speedRule("office wifi", ScopeShared, 200, LimitSpec{IPGroups: []int64{ipg.ID}, BurstKB: 400})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.up.CreateLimit(speedRule("everyone", ScopeEachClient, 1000, LimitSpec{Everyone: true})); err != nil {
		t.Fatal(err)
	}
	txt := r.text()

	for _, want := range []string{
		limitsBlockStart,
		"delay_pools 3",
		"acl sqa_lim1_users proxy_auth alice bob carol", // named user + group members, sorted, once
		"delay_class 1 4",
		"delay_parameters 1 -1/-1 -1/-1 -1/-1 51200/102400", // 50 KB/s, burst defaults to two seconds
		"delay_access 1 allow sqa_lim1_users",
		"delay_access 1 deny all",
		"acl sqa_lim2_net src 10.1.0.0/16 10.2.0.5",
		"delay_class 2 1",
		"delay_parameters 2 204800/409600", // 200 KB/s with a 400 KB burst
		"delay_access 2 allow sqa_lim2_net",
		"delay_class 3 2",
		"delay_parameters 3 -1/-1 1024000/2048000",
		"delay_access 3 allow all",
		limitsBlockEnd,
	} {
		if !has(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	if lineIndex(txt, "delay_pools 3") > lineIndex(txt, "delay_class 1 4") {
		t.Error("delay_pools must precede the delay_class lines")
	}
	// Each ACL is defined before the delay_access line that uses it.
	if lineIndex(txt, "acl sqa_lim1_users proxy_auth alice bob carol") > lineIndex(txt, "delay_access 1 allow sqa_lim1_users") {
		t.Error("an ACL must be defined before it is used")
	}
	if n := strings.Count(txt, "auth_param basic program"); n != 1 {
		t.Errorf("the block is appended after the existing auth_param, which must stay the only copy (found %d)", n)
	}
	// The block goes at the end of the file.
	if lineIndex(txt, limitsBlockStart) < lineIndex(txt, "refresh_pattern . 0 20% 4320") {
		t.Error("the limits block is appended after the stock directives")
	}
}

func TestDownloadLimits(t *testing.T) {
	r := newUPRig(t, "alice", "bob")
	ipg, _ := NewGroupManager(r.up.db).Create("office", []string{"10.1.0.0/16"})

	// Mixed selector: users AND an IP group -> two lines with the same size.
	if _, _, err := r.up.CreateLimit(LimitRule{Name: "small", Kind: LimitDownload, Enabled: true,
		Spec: LimitSpec{Users: []string{"bob"}, IPGroups: []int64{ipg.ID}, MaxMB: 50}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.up.CreateLimit(LimitRule{Name: "everyone", Kind: LimitDownload, Enabled: true,
		Spec: LimitSpec{Everyone: true, MaxMB: 700}}); err != nil {
		t.Fatal(err)
	}
	txt := r.text()
	for _, want := range []string{
		"acl sqa_lim1_users proxy_auth bob",
		"acl sqa_lim1_net src 10.1.0.0/16",
		"reply_body_max_size 50 MB sqa_lim1_users",
		"reply_body_max_size 50 MB sqa_lim1_net",
		"reply_body_max_size 700 MB",
	} {
		if !has(txt, want) {
			t.Errorf("missing %q in\n%s", want, txt)
		}
	}
	// First match wins in squid, so the specific rule (older id) is written first.
	if lineIndex(txt, "reply_body_max_size 50 MB sqa_lim1_users") > lineIndex(txt, "reply_body_max_size 700 MB") {
		t.Error("rules are written in id order")
	}
	if has(txt, "delay_pools") {
		t.Error("download limits do not create delay pools")
	}
}

func TestLimitsThatMatchNobodyOrAreOffWriteNothing(t *testing.T) {
	r := newUPRig(t, "alice")
	before := r.text()
	empty, _ := r.up.CreateGroup("empty")

	if _, _, err := r.up.CreateLimit(LimitRule{Name: "empty group", Kind: LimitDownload, Enabled: true,
		Spec: LimitSpec{UserGroups: []int64{empty.ID}, MaxMB: 10}}); err != nil {
		t.Fatal(err)
	}
	if r.text() != before {
		t.Errorf("a group without members matches nobody: no ACL, no directive, not 'everyone'\n%s", r.text())
	}

	rule, _, _ := r.up.CreateLimit(LimitRule{Name: "switched off", Kind: LimitDownload, Enabled: false,
		Spec: LimitSpec{Everyone: true, MaxMB: 10}})
	if r.text() != before {
		t.Error("a disabled limit writes nothing")
	}
	rule.Enabled = true
	if _, changed, err := r.up.UpdateLimit(rule); err != nil || !changed || !has(r.text(), "reply_body_max_size 10 MB") {
		t.Errorf("enabling: changed=%v err=%v", changed, err)
	}

	// Once the group gets a member the same rule starts to apply.
	r.up.SetMembers(empty.ID, []string{"alice"})
	if !has(r.text(), "reply_body_max_size 10 MB sqa_lim1_users") {
		t.Errorf("a new member must be picked up:\n%s", r.text())
	}
}

func TestLimitValidation(t *testing.T) {
	r := newUPRig(t, "alice")
	ipg, _ := NewGroupManager(r.up.db).Create("office", []string{"10.1.0.0/16"})
	base := func() LimitRule {
		return speedRule("ok rule", ScopeShared, 100, LimitSpec{Users: []string{"alice"}})
	}
	mut := func(f func(*LimitRule)) LimitRule { l := base(); f(&l); return l }

	cases := map[string]LimitRule{
		"empty name":          mut(func(l *LimitRule) { l.Name = "" }),
		"name with a newline": mut(func(l *LimitRule) { l.Name = "a\nhttp_access allow all" }),
		"unknown kind":        mut(func(l *LimitRule) { l.Kind = "fast" }),
		"unknown scope":       mut(func(l *LimitRule) { l.Spec.Scope = "galaxy" }),
		"zero speed":          mut(func(l *LimitRule) { l.Spec.RateKBps = 0 }),
		"absurd speed":        mut(func(l *LimitRule) { l.Spec.RateKBps = 10_000_001 }),
		"burst below speed":   mut(func(l *LimitRule) { l.Spec.BurstKB = 50 }),
		"nobody selected":     mut(func(l *LimitRule) { l.Spec.Users = nil }),
		"unknown user":        mut(func(l *LimitRule) { l.Spec.Users = []string{"ghost"} }),
		"unknown user group":  mut(func(l *LimitRule) { l.Spec.Users = nil; l.Spec.UserGroups = []int64{99} }),
		"unknown ip group":    mut(func(l *LimitRule) { l.Spec.Users = nil; l.Spec.IPGroups = []int64{99} }),
		"per-user by IP group": mut(func(l *LimitRule) {
			l.Spec.Scope = ScopeEachUser
			l.Spec.Users = nil
			l.Spec.IPGroups = []int64{ipg.ID}
		}),
		"download without size": {Name: "dl", Kind: LimitDownload, Enabled: true, Spec: LimitSpec{Everyone: true}},
		"download absurd size":  {Name: "dl", Kind: LimitDownload, Enabled: true, Spec: LimitSpec{Everyone: true, MaxMB: 10_000_001}},
	}
	before := r.text()
	for name, rule := range cases {
		var pe *PolicyError
		if _, _, err := r.up.CreateLimit(rule); !errors.As(err, &pe) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	if r.text() != before {
		t.Error("rejected limits must not touch squid.conf")
	}

	if _, _, err := r.up.CreateLimit(base()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.up.CreateLimit(base()); err == nil {
		t.Error("limit names are unique")
	}
	if _, _, err := r.up.UpdateLimit(LimitRule{ID: 999, Name: "ghost", Kind: LimitSpeed, Spec: base().Spec}); err == nil {
		t.Error("updating a missing limit must fail")
	}
}

func TestAFailingConfigLeavesNoLimitBehind(t *testing.T) {
	r := newUPRig(t, "alice")
	good, _, err := r.up.CreateLimit(speedRule("first", ScopeShared, 100, LimitSpec{Everyone: true}))
	if err != nil {
		t.Fatal(err)
	}
	beforeConf := r.text()

	touch(t, filepath.Join(r.dir, "fail_parse")) // squid now rejects every config

	if _, _, err := r.up.CreateLimit(speedRule("second", ScopeShared, 200, LimitSpec{Everyone: true})); err == nil {
		t.Fatal("squid refused the config, so the create must fail")
	}
	limits, _ := r.up.Limits()
	if len(limits) != 1 {
		t.Errorf("a limit squid could not take must not stay in the database: %+v", limits)
	}

	edited := good
	edited.Spec.RateKBps = 999
	if _, _, err := r.up.UpdateLimit(edited); err == nil {
		t.Fatal("the update must fail too")
	}
	limits, _ = r.up.Limits()
	if limits[0].Spec.RateKBps != 100 {
		t.Errorf("a failed update must be rolled back in the database, speed is %d", limits[0].Spec.RateKBps)
	}
	if r.text() != beforeConf {
		t.Error("squid.conf must be unchanged")
	}
}

func TestDeletingTheLastLimitRestoresTheConfigExactly(t *testing.T) {
	r := newUPRig(t, "alice")
	before := r.text()
	l1, _, _ := r.up.CreateLimit(speedRule("one", ScopeShared, 100, LimitSpec{Everyone: true}))
	l2, _, _ := r.up.CreateLimit(LimitRule{Name: "two", Kind: LimitDownload, Enabled: true, Spec: LimitSpec{Everyone: true, MaxMB: 5}})
	r.up.Update("alice", UserPatch{Disabled: bptr(true)})

	if _, err := r.up.DeleteLimit(l1.ID); err != nil {
		t.Fatal(err)
	}
	if has(r.text(), "delay_pools") || !has(r.text(), "reply_body_max_size 5 MB") {
		t.Errorf("only the deleted limit may go:\n%s", r.text())
	}
	r.up.DeleteLimit(l2.ID)
	r.up.Update("alice", UserPatch{Disabled: bptr(false)})
	if r.text() != before {
		t.Errorf("with everything removed squid.conf must be as it was:\n%s", r.text())
	}
	if _, err := r.up.DeleteLimit(l1.ID); err == nil {
		t.Error("deleting a missing limit is an error")
	}
}

func TestBothBlocksCoexistWithOtherManagedBlocks(t *testing.T) {
	r := newUPRig(t, "alice")
	r.up.Update("alice", UserPatch{Disabled: bptr(true)})
	r.up.CreateLimit(speedRule("one", ScopeShared, 100, LimitSpec{Everyone: true}))

	// A different generator rewrites its own block; ours must survive and
	// regenerating ours must not disturb theirs.
	restr := NewRestrictionManager(r.up.db, r.mgr)
	if _, err := restr.Create(Restriction{Name: "lunch", Domains: []string{"example.org"}, Days: []string{"M"}, StartTime: "12:00", EndTime: "13:00"}); err != nil {
		t.Fatal(err)
	}
	txt := r.text()
	for _, marker := range []string{userPolicyBlockStart, limitsBlockStart, restrictionBlockStart} {
		if strings.Count(txt, marker) != 1 {
			t.Errorf("%q must appear exactly once", marker)
		}
	}
	r.up.Update("alice", UserPatch{Note: strPtr("x")})
	r.sync()
	if after := r.text(); after != txt {
		t.Error("syncing with nothing to change must be a no-op even next to other blocks")
	}
	if lineIndex(txt, "http_access deny sqa_up_auth sqa_up_blocked") > lineIndex(txt, "http_access allow localhost") {
		t.Error("the user deny rule must sit before the allow rules")
	}
}

func TestAFailedSyncLeavesTheAccountSettingsAsTheyWere(t *testing.T) {
	r := newUPRig(t, "alice")
	r.up.Update("alice", UserPatch{Note: strPtr("first")})

	touch(t, filepath.Join(r.dir, "fail_parse")) // squid now rejects every config
	if _, err := r.up.Update("alice", UserPatch{Disabled: bptr(true), Note: strPtr("second")}); err == nil {
		t.Fatal("squid refused the config, so the change must fail")
	}
	users, _ := r.up.Users()
	if users[0].Disabled || users[0].Note != "first" {
		t.Errorf("settings must be rolled back: %+v", users[0])
	}

	// An account without settings goes back to having none.
	r2 := newUPRig(t, "bob")
	touch(t, filepath.Join(r2.dir, "fail_parse"))
	r2.up.Update("bob", UserPatch{Disabled: bptr(true)})
	var n int
	r2.up.db.QueryRow(`SELECT COUNT(*) FROM proxy_users`).Scan(&n)
	if n != 0 {
		t.Errorf("a failed first change must not leave a row behind, found %d", n)
	}
}

// fakeHtpasswd is a stand-in for htpasswd -i: it keeps "user:hash" lines, the
// hash being a marker plus the password so tests can see what was set.
func fakeHtpasswd(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "htpasswd")
	script := `#!/bin/sh
if [ "$1" = "-D" ]; then
  grep -v "^$3:" "$2" > "$2.tmp"; mv "$2.tmp" "$2"; exit 0
fi
shift
if [ "$1" = "-c" ]; then shift; : > "$1"; fi
file=$1; user=$2; read pw
grep -v "^$user:" "$file" > "$file.tmp" 2>/dev/null
echo "$user:fake-hash-of-$pw" >> "$file.tmp"
mv "$file.tmp" "$file"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
