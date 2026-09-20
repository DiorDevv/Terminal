package squid

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stockConf mimics the parts of Debian's squid.conf that matter here: long
// documentation comments that mention the directives, one active http_port,
// a commented-out cache_dir, and http_access rules.
const stockConf = `#  TAG: http_port
#	Usage:	port [mode] [options]
#Default:
# http_port 3128

#  TAG: cache_mem	(bytes)
#Default:
# cache_mem 256 MB

#  TAG: cache_dir
#Default:
# No disk cache. Store cache objects only in memory.
#cache_dir ufs /var/spool/squid 100 16 256

acl localnet src 10.0.0.0/8
http_access deny !Safe_ports
http_access allow localhost
http_access deny all
http_port 3128
coredump_dir /var/spool/squid
refresh_pattern . 0 20% 4320
`

// stockConfWithDiskCache is stockConf with an active cache_dir and dns line,
// to exercise taking over and clearing real stock directives.
const stockConfWithDiskCache = stockConf +
	"cache_dir ufs /var/spool/squid 100 16 256\n" +
	"dns_nameservers 9.9.9.9 8.8.4.4\n"

func mustApply(t *testing.T, content string, u SettingsUpdate) (string, SettingsResult) {
	t.Helper()
	out, res, err := applySettings(content, u)
	if err != nil {
		t.Fatalf("applySettings(%+v): %v", u, err)
	}
	return out, res
}

func valuesOf(content, key string) ([]string, string) {
	for _, sv := range effectiveSettings(content) {
		if sv.Key == key {
			return sv.Values, sv.Source
		}
	}
	return nil, ""
}

func set(kv ...any) SettingsUpdate {
	u := SettingsUpdate{Values: map[string][]string{}}
	for i := 0; i < len(kv); i += 2 {
		switch v := kv[i+1].(type) {
		case string:
			u.Values[kv[i].(string)] = []string{v}
		case []string:
			u.Values[kv[i].(string)] = v
		}
	}
	return u
}

func TestEffectiveSettingsReadsStockAndIgnoresComments(t *testing.T) {
	vals, src := valuesOf(stockConf, "http_port")
	if src != "squid.conf" || len(vals) != 1 || vals[0] != "3128" {
		t.Fatalf("http_port = %v (%s), want [3128] from squid.conf", vals, src)
	}
	// "# cache_mem 256 MB" and "#cache_dir ..." are documentation, not settings.
	if vals, src := valuesOf(stockConf, "cache_mem"); src != "default" || len(vals) != 0 {
		t.Fatalf("cache_mem = %v (%s): a commented-out line must not count", vals, src)
	}
	if vals, src := valuesOf(stockConf, "cache_dir"); src != "default" || len(vals) != 0 {
		t.Fatalf("cache_dir = %v (%s): a commented-out line must not count", vals, src)
	}

	vals, src = valuesOf(stockConfWithDiskCache, "dns_nameservers")
	if src != "squid.conf" || strings.Join(vals, ",") != "9.9.9.9,8.8.4.4" {
		t.Fatalf("dns_nameservers = %v (%s), want both servers split out", vals, src)
	}
}

func TestApplyTakesOverStockLineAndLeavesDocsAlone(t *testing.T) {
	out, res := mustApply(t, stockConf, set("http_port", []string{"3128", "8080"}, "cache_mem", "512mb"))

	if strings.Contains(out, "\nhttp_port 3128\ncoredump") {
		t.Fatal("the stock http_port line must be commented out, or squid would listen on it twice")
	}
	for _, want := range []string{
		"# squidadmin-disabled: http_port 3128\n",
		settingsBlockStart + "\nhttp_port 3128\nhttp_port 8080\ncache_mem 512 MB\n" + settingsBlockEnd + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Documentation that merely mentions the directive is untouched.
	for _, doc := range []string{"#  TAG: http_port\n", "# http_port 3128\n", "# cache_mem 256 MB\n"} {
		if !strings.Contains(out, doc) {
			t.Errorf("documentation line %q was modified", doc)
		}
	}

	if got := strings.Join(res.Changed, ","); got != "http_port,cache_mem" {
		t.Errorf("changed = %s, want http_port,cache_mem", got)
	}
	if res.RestartRequired {
		t.Error("http_port / cache_mem changes only need a reload")
	}
	vals, src := valuesOf(out, "http_port")
	if src != "panel" || strings.Join(vals, ",") != "3128,8080" {
		t.Errorf("effective http_port = %v (%s)", vals, src)
	}
	if !strings.HasSuffix(out, settingsBlockEnd+"\n") {
		t.Errorf("the block must sit at the very end, before the final newline:\n...%s", out[len(out)-120:])
	}
}

func TestSettingTheStockValueChangesNothing(t *testing.T) {
	out, res := mustApply(t, stockConf, set("http_port", "3128"))
	if out != stockConf || len(res.Changed) != 0 || len(res.Changes) != 0 {
		t.Fatalf("re-saving an unchanged value must not touch the file: changed=%v", res.Changed)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	u := set("http_port", []string{"3128", "3129"}, "via", "off", "connect_timeout", "45 seconds", "cache_mem", "128 MB")
	first, _ := mustApply(t, stockConf, u)
	second, res := mustApply(t, first, u)
	if second != first || len(res.Changed) != 0 {
		t.Fatalf("applying the same update twice must be a no-op (changed=%v)", res.Changed)
	}
}

func TestResetRestoresTheOriginalFileByteForByte(t *testing.T) {
	for name, orig := range map[string]string{"stock": stockConf, "with disk cache": stockConfWithDiskCache} {
		t.Run(name, func(t *testing.T) {
			modified, _ := mustApply(t, orig, set(
				"http_port", []string{"3128", "3129 name=alt"},
				"cache_mem", "64 MB", "via", "off", "forwarded_for", "delete",
				"visible_hostname", "Proxy.Example.com",
				"dns_nameservers", []string{"1.1.1.1", "8.8.8.8"},
				"cache_dir", "ufs /var/spool/squid 500 16 256",
				"shutdown_lifetime", "3 seconds",
			))
			if modified == orig {
				t.Fatal("update changed nothing")
			}
			var keys []string
			for _, s := range Schema {
				keys = append(keys, s.Key)
			}
			restored, _ := mustApply(t, modified, SettingsUpdate{Reset: keys})
			if restored != orig {
				t.Fatalf("resetting everything must give back the original file exactly.\n--- got\n%s\n--- want\n%s", restored, orig)
			}
		})
	}
}

func TestClearDisablesAStockLineAndResetBringsItBack(t *testing.T) {
	cleared, res := mustApply(t, stockConfWithDiskCache, SettingsUpdate{Values: map[string][]string{"cache_dir": {}}})
	if vals, src := valuesOf(cleared, "cache_dir"); len(vals) != 0 || src != "default" {
		t.Fatalf("cache_dir after clearing = %v (%s), want none", vals, src)
	}
	if !strings.Contains(cleared, "# squidadmin-disabled: cache_dir ufs /var/spool/squid 100 16 256\n") {
		t.Fatal("the stock cache_dir line should be commented out, not deleted")
	}
	if !res.RestartRequired {
		t.Error("removing a cache_dir needs a restart")
	}

	back, _ := mustApply(t, cleared, SettingsUpdate{Reset: []string{"cache_dir"}})
	if back != stockConfWithDiskCache {
		t.Fatalf("reset must restore the stock line:\n%s", back)
	}
}

func TestRestartRequiredOnlyForCacheDir(t *testing.T) {
	_, res := mustApply(t, stockConf, set("cache_dir", "ufs /var/spool/squid/new 100 16 256"))
	if !res.RestartRequired || strings.Join(res.Changed, ",") != "cache_dir" {
		t.Fatalf("cache_dir: %+v", res)
	}
	_, res = mustApply(t, stockConf, set("cache_mem", "1 GB", "via", "off"))
	if res.RestartRequired {
		t.Fatalf("cache_mem/via must not demand a restart: %+v", res)
	}
}

func TestManuallyAddedDuplicateIsNeutralisedOnNextApply(t *testing.T) {
	managed, _ := mustApply(t, stockConf, set("cache_mem", "512 MB"))
	// Someone edits the file by hand and adds their own cache_mem above the block.
	handEdited := strings.Replace(managed, "acl localnet", "cache_mem 128 MB\nacl localnet", 1)

	// The panel's value (later in the file) still wins in what it reports...
	if vals, src := valuesOf(handEdited, "cache_mem"); src != "panel" || vals[0] != "512 MB" {
		t.Fatalf("cache_mem = %v (%s)", vals, src)
	}
	// ...and the next panel save comments the stray line out so there is exactly one.
	healed, _ := mustApply(t, handEdited, set("via", "off"))
	if strings.Count(healed, "\ncache_mem ") != 1 || !strings.Contains(healed, "# squidadmin-disabled: cache_mem 128 MB") {
		t.Fatalf("expected the hand-added duplicate to be disabled:\n%s", healed)
	}
}

func TestSettingsBlockCoexistsWithOtherManagedBlocks(t *testing.T) {
	withAuth := stockConf + "# --- squidadmin: proxy authentication ---\nhttp_access allow authenticated_users\n# --- end squidadmin ---\n"
	out, _ := mustApply(t, withAuth, set("via", "off"))

	if !strings.Contains(out, "# --- squidadmin: proxy authentication ---\nhttp_access allow authenticated_users\n# --- end squidadmin ---\n") {
		t.Fatal("another managed block was disturbed")
	}
	lines := strings.Split(out, "\n")
	if got := policyAnchor(lines); !strings.HasPrefix(lines[got], "http_access allow localhost") {
		t.Fatalf("policyAnchor must still land on the first real allow rule, got line %d %q", got, lines[got])
	}

	// Removing the settings again leaves the other block exactly as it was.
	back, _ := mustApply(t, out, SettingsUpdate{Reset: []string{"via"}})
	if back != withAuth {
		t.Fatalf("round trip with another block present differs:\n%s", back)
	}
}

func TestChangesDescribeEachSettingBeforeAndAfter(t *testing.T) {
	_, res := mustApply(t, stockConf, set("http_port", []string{"3128", "8080"}, "cache_dir", "ufs /var/spool/squid/x 100 16 256"))

	if len(res.Changes) != 2 {
		t.Fatalf("changes = %+v", res.Changes)
	}
	hp, cd := res.Changes[0], res.Changes[1]
	if hp.Key != "http_port" || strings.Join(hp.From, ",") != "3128" || strings.Join(hp.To, ",") != "3128,8080" || hp.Restart {
		t.Errorf("http_port change = %+v", hp)
	}
	if cd.Key != "cache_dir" || len(cd.From) != 0 || len(cd.To) != 1 || !cd.Restart {
		t.Errorf("cache_dir change = %+v", cd)
	}

	// The lists are never null in JSON, even for "was unset".
	if cd.From == nil {
		t.Error("From must be an empty list, not nil")
	}
}

func TestValidation(t *testing.T) {
	good := map[string]map[string]string{ // key -> input -> canonical
		"http_port":           {"3128": "3128", "0.0.0.0:3129": "0.0.0.0:3129", "[::1]:3128": "[::1]:3128", "3130  intercept": "3130 intercept", "proxy.lan:8080 name=main": "proxy.lan:8080 name=main"},
		"visible_hostname":    {"Proxy.Example.COM": "proxy.example.com", "proxy": "proxy"},
		"dns_nameservers":     {"1.1.1.1": "1.1.1.1", "2606:4700:4700::1111": "2606:4700:4700::1111"},
		"forwarded_for":       {"DELETE": "delete", "off": "off"},
		"via":                 {"Off": "off"},
		"cache_mem":           {"256mb": "256 MB", "1 gb": "1 GB", "0 MB": "0 MB", "512   KB": "512 KB"},
		"maximum_object_size": {"4 MB": "4 MB"},
		"cache_dir":           {"ufs /var/spool/squid 1000 16 256": "ufs /var/spool/squid 1000 16 256", "aufs   /srv/cache 50 8 64": "aufs /srv/cache 50 8 64"},
		"connect_timeout":     {"30 seconds": "30 seconds", "1 second": "1 second", "1 seconds": "1 second", "2 MINUTES": "2 minutes", "1 minute": "1 minute"},
		"shutdown_lifetime":   {"0 seconds": "0 seconds", "3seconds": "3 seconds"},
		"cache_peer":          {"up.example.com parent 3128 0 no-query default": "up.example.com parent 3128 0 no-query default", "10.0.0.5 sibling 3128 3130 weight=5 name=a": "10.0.0.5 sibling 3128 3130 weight=5 name=a"},
		"never_direct":        {"allow all": "allow all", " allow   all ": "allow all"},
		"logfile_rotate":      {"0": "0", " 10 ": "10", "365": "365"},
	}
	for key, cases := range good {
		s, _ := schemaByKey(key)
		for in, want := range cases {
			got, err := s.normalize(in)
			if err != nil || got != want {
				t.Errorf("%s: normalize(%q) = %q, %v; want %q", key, in, got, err, want)
			}
		}
	}

	bad := map[string][]string{
		"http_port":           {"", "0", "70000", "abc", "3128 bogus", "3128 intercept tproxy", "::1:3128", "[::1]3128", "bad host:80", "3128 name=has space"},
		"visible_hostname":    {"-bad", "bad-", "has space", "1.2.3.4", "under_score", strings.Repeat("a", 64)},
		"dns_nameservers":     {"not-an-ip", "1.1.1", "dns.google"},
		"forwarded_for":       {"maybe", "1"},
		"via":                 {"transparent", "yes"},
		"cache_mem":           {"256", "MB", "-1 MB", "1.5 GB", "5000000000 GB", "256 TB"},
		"maximum_object_size": {"0 KB", "big"},
		"cache_dir":           {"ufs /var/spool/squid", "rock /var/spool/squid 100 16 256", "ufs relative/path 100 16 256", "ufs /var/../etc 100 16 256", "ufs / 100 16 256", "ufs /var/spool/ 100 16 256", "ufs /var/spool/squid 1 16 256", "ufs /var/spool/squid 100 0 256", "ufs /var/spool/squid 100 16 999", "ufs /var/spool/sq uid 100 16 256", "ufs /a;b 100 16 256"},
		"connect_timeout":     {"0 seconds", "2 weeks", "30", "fast", "2 hours"},
		"read_timeout":        {"25 hours"},
		"shutdown_lifetime":   {"2 hours"},
		"cache_peer":          {"up.example.com", "up.example.com child 3128 0", "up.example.com parent 0 0", "up.example.com parent 3128 70000", "up.example.com parent 3128 0 login=user:pass", "up.example.com parent 3128 0 no-such-option", "up.example.com parent 3128 0 weight=abc", "bad host parent 3128 0"},
		"never_direct":        {"deny all", "allow localhost", "allow"},
		"logfile_rotate":      {"", "-1", "366", "1.5", "010", "ten", "5 days", "+3"},
	}
	for key, inputs := range bad {
		s, _ := schemaByKey(key)
		for _, in := range inputs {
			if got, err := s.normalize(in); err == nil {
				t.Errorf("%s: normalize(%q) = %q, want an error", key, in, got)
			}
		}
	}
}

func TestValidationReportsEveryBadFieldAtOnce(t *testing.T) {
	_, _, err := applySettings(stockConf, set("cache_mem", "lots", "via", "maybe", "connect_timeout", "1 minute"))
	var se *SettingsError
	if !errors.As(err, &se) {
		t.Fatalf("want *SettingsError, got %v", err)
	}
	if len(se.Fields) != 2 || se.Fields["cache_mem"] == "" || se.Fields["via"] == "" {
		t.Fatalf("both bad fields must be reported, and the good one not: %v", se.Fields)
	}
}

func TestListRules(t *testing.T) {
	cases := map[string]SettingsUpdate{
		"unknown key":            set("nonsense", "1"),
		"http_port required":     {Values: map[string][]string{"http_port": {}}},
		"http_port blank only":   set("http_port", []string{"  "}),
		"duplicate port":         set("http_port", []string{"3128", "3128"}),
		"single-valued setting":  set("cache_mem", []string{"1 GB", "2 GB"}),
		"too many nameservers":   set("dns_nameservers", []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "9.9.9.10", "4.4.4.4", "4.4.2.2", "1.2.3.4"}),
		"set and reset together": {Values: map[string][]string{"via": {"off"}}, Reset: []string{"via"}},
		"reset unknown":          {Reset: []string{"nonsense"}},
	}
	for name, u := range cases {
		if _, _, err := applySettings(stockConf, u); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestNeverDirectNeedsAnUpstreamProxy(t *testing.T) {
	// squid would parse and start this happily, then every request would fail.
	if _, _, err := applySettings(stockConf, set("never_direct", "allow all")); err == nil {
		t.Fatal("never_direct without a cache_peer must be rejected")
	}
	withPeer, _ := mustApply(t, stockConf, set("cache_peer", "up.example.com parent 3128 0 no-query", "never_direct", "allow all"))

	// ...and the peer can't be removed while never_direct still depends on it.
	if _, _, err := applySettings(withPeer, SettingsUpdate{Values: map[string][]string{"cache_peer": {}}}); err == nil {
		t.Fatal("removing the last cache_peer while never_direct is on must be rejected")
	}
	if _, _, err := applySettings(withPeer, SettingsUpdate{Values: map[string][]string{"cache_peer": {}, "never_direct": {}}}); err != nil {
		t.Fatalf("removing both together is fine: %v", err)
	}
}

// The bundled tests use a small fixture; the real distribution config is ~3000
// lines of documentation. When it is available, prove the round trip on it.
func TestRoundTripOnTheRealDistributionConfig(t *testing.T) {
	data, err := os.ReadFile("/etc/squid/squid.conf.orig")
	if err != nil {
		t.Skip("no /etc/squid/squid.conf.orig on this machine")
	}
	orig := string(data)

	modified, res := mustApply(t, orig, set(
		"http_port", []string{"3128", "3129"}, "cache_mem", "64 MB", "via", "off",
		"cache_dir", "ufs /var/spool/squid 100 16 256", "shutdown_lifetime", "3 seconds"))
	if len(res.Changes) > 30 {
		t.Errorf("an update should touch a handful of lines, not %d", len(res.Changes))
	}
	if !strings.Contains(modified, "\nhttp_access deny all\n") {
		t.Error("access rules must be untouched")
	}
	var keys []string
	for _, s := range Schema {
		keys = append(keys, s.Key)
	}
	if restored, _ := mustApply(t, modified, SettingsUpdate{Reset: keys}); restored != orig {
		t.Fatal("resetting everything did not give back the real distribution config exactly")
	}
}

// ------------------------------------------------------------------ Manager

func settingsManager(t *testing.T, conf string) (*Manager, string, string) {
	t.Helper()
	bin, dir := writeFakeSquid(t)
	path := newConf(t, conf)
	m := NewManager(path, bin)
	m.SetHistory(newTestHistory(t))
	return m, path, dir
}

func TestApplySettingsWritesAndRecordsHistory(t *testing.T) {
	m, path, _ := settingsManager(t, stockConf)

	res, err := m.ApplySettings(set("via", "off", "cache_mem", "64 MB"))
	if err != nil || strings.Join(res.Changed, ",") != "via,cache_mem" {
		t.Fatalf("ApplySettings: %+v %v", res, err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "via off") {
		t.Fatalf("not written:\n%s", got)
	}
	versions, _ := m.History().List(5)
	if len(versions) == 0 || versions[0].Comment != "settings: cache_mem, via" {
		t.Fatalf("history comment = %+v", versions)
	}
}

func TestPreviewSettingsWritesNothingButStillAsksSquid(t *testing.T) {
	m, path, dir := settingsManager(t, stockConf)

	res, err := m.PreviewSettings(set("via", "off"))
	if err != nil || len(res.Changes) == 0 {
		t.Fatalf("PreviewSettings: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(path); string(got) != stockConf {
		t.Fatal("a dry run must not modify squid.conf")
	}

	// A value that passes the panel's checks but that squid itself rejects is
	// caught by the preview, before anything is saved.
	touch(t, filepath.Join(dir, "fail_parse"))
	if _, err := m.PreviewSettings(set("via", "off")); err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("squid's parser must have the last word, got %v", err)
	}
}

func TestApplySettingsRejectedBySquidLeavesConfigUntouched(t *testing.T) {
	m, path, dir := settingsManager(t, stockConf)
	touch(t, filepath.Join(dir, "fail_parse"))

	if _, err := m.ApplySettings(set("via", "off")); err == nil {
		t.Fatal("expected an error")
	}
	if got, _ := os.ReadFile(path); string(got) != stockConf {
		t.Fatal("a config squid rejects must never be written")
	}
}

// statefulFakeSquid is a stand-in squid whose "-k shutdown" stops it and whose
// bare invocation starts it; a one-shot fail_start marker makes the next start
// fail (a config squid cannot run).
func statefulFakeSquid(t *testing.T) (bin, dir string) {
	t.Helper()
	bin, dir = writeFakeSquid(t)
	script := `#!/bin/sh
dir=$(dirname "$0")
case "$2" in
  parse) exit 0;;
  shutdown) touch "$dir/stopped"; exit 0;;
  check) [ -f "$dir/stopped" ] && exit 1; exit 0;;
  reconfigure) exit 0;;
  "") if [ -f "$dir/fail_start" ]; then rm "$dir/fail_start"; echo "FATAL: cannot create swap directory" >&2; exit 1; fi
      rm -f "$dir/stopped"; exit 0;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func TestRestartPendingLifecycle(t *testing.T) {
	bin, _ := statefulFakeSquid(t)
	path := newConf(t, stockConf)
	m := NewManager(path, bin)
	m.SetHistory(newTestHistory(t))

	if m.ServiceInfo().RestartPending {
		t.Fatal("nothing pending initially")
	}
	if _, err := m.ApplySettings(set("cache_mem", "64 MB")); err != nil {
		t.Fatal(err)
	}
	if m.ServiceInfo().RestartPending {
		t.Fatal("a reload-only change must not ask for a restart")
	}

	if _, err := m.ApplySettings(set("cache_dir", "ufs /var/spool/squid/x 100 16 256")); err != nil {
		t.Fatal(err)
	}
	if !m.ServiceInfo().RestartPending {
		t.Fatal("a cache_dir change must be flagged as needing a restart")
	}
	if err := m.RestartVerified(5 * time.Second); err != nil {
		t.Fatalf("RestartVerified: %v", err)
	}
	if m.ServiceInfo().RestartPending {
		t.Fatal("the flag must clear after a successful restart")
	}
}

func TestRestartVerifiedRollsBackWhenSquidWillNotStart(t *testing.T) {
	bin, dir := statefulFakeSquid(t)
	path := newConf(t, stockConf)
	m := NewManager(path, bin)
	hist := newTestHistory(t)
	m.SetHistory(hist)

	if _, err := m.ApplySettings(set("cache_dir", "ufs /nonexistent/place 100 16 256")); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "fail_start"))

	err := m.RestartVerified(5 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "restored automatically") {
		t.Fatalf("expected a rollback error, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != stockConf {
		t.Fatalf("the previous config must be back:\n%s", got)
	}
	if !m.GetStatus().Running {
		t.Fatal("squid must be started again after the rollback")
	}
	if m.ServiceInfo().RestartPending {
		t.Fatal("the undone change must no longer be pending")
	}
	versions, _ := hist.List(5)
	if len(versions) == 0 || !strings.Contains(versions[0].Comment, "rollback") {
		t.Fatalf("rollback must be recorded in the history: %+v", versions)
	}
}

// systemd reports the start time in whole seconds.
func TestRestartPendingComparesAtSecondGranularity(t *testing.T) {
	m := NewManager("/nonexistent", "true")
	if m.restartPending(1000) {
		t.Fatal("nothing pending initially")
	}
	changedAt := time.Unix(2000, 500_000_000) // 2000.5 s
	m.restartPendingSince.Store(changedAt.UnixNano())

	for started, want := range map[int64]bool{
		1990: true,  // started well before the change
		2000: true,  // same second: cannot tell, so treat as older
		2001: false, // clearly after the change
		0:    true,  // start time unknown (no systemd): stays pending
	} {
		if got := m.restartPending(started); got != want {
			t.Errorf("restartPending(started=%d) = %v, want %v", started, got, want)
		}
	}
}

func TestPlainRestartClearsThePendingFlag(t *testing.T) {
	bin, _ := statefulFakeSquid(t)
	m := NewManager(newConf(t, stockConf), bin)
	m.SetHistory(newTestHistory(t))

	if _, err := m.ApplySettings(set("cache_dir", "ufs /var/spool/squid/x 100 16 256")); err != nil {
		t.Fatal(err)
	}
	if !m.ServiceInfo().RestartPending {
		t.Fatal("expected a pending restart")
	}
	if err := m.ServiceAction("restart"); err != nil {
		t.Fatal(err)
	}
	if m.ServiceInfo().RestartPending {
		t.Fatal("restarting squid (from the dashboard, say) applies the change, so it is no longer pending")
	}
}

func TestRotateLogs(t *testing.T) {
	m, _, dir := settingsManager(t, stockConf)
	if err := m.RotateLogs(); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rotated")); err != nil {
		t.Error("squid -k rotate was not run")
	}

	touch(t, filepath.Join(dir, "fail_rotate"))
	if err := m.RotateLogs(); err == nil || !strings.Contains(err.Error(), "no permission") {
		t.Errorf("a failing rotate must surface squid's message: %v", err)
	}

	os.Remove(filepath.Join(dir, "fail_rotate"))
	os.Remove(filepath.Join(dir, "rotated"))
	touch(t, filepath.Join(dir, "stopped"))
	if err := m.RotateLogs(); err == nil {
		t.Error("rotating while squid is stopped must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "rotated")); err == nil {
		t.Error("squid must not be signalled when it is not running")
	}
}
