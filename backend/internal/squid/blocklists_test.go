package squid

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBlocklistFormats(t *testing.T) {
	input := strings.Join([]string{
		"# a comment",
		"! adblock comment",
		"[Adblock Plus 2.0]",
		"// another comment",
		"",
		"127.0.0.1 localhost",
		"127.0.0.1 localhost.localdomain",
		"255.255.255.255 broadcasthost",
		"::1 ip6-loopback",
		"::1 ip6-localhost",
		"0.0.0.0 ads.tracker.example   # hosts entry",
		"0.0.0.0 Metrics.Tracker.example",
		"||banner.example.org^",
		"||with-options.example.org^$third-party",
		"||path.example.org/ads^",
		"*.wild.example.net",
		".leading.example.net",
		"plain.example.io.",
		"just-a-word",
		"192.168.1.1",
		"bad_underscore.example.com",
		"sub.plain.example.io",      // covered by plain.example.io
		"deep.sub.plain.example.io", // covered too
		"tracker.example",           // parent of ads.tracker.example and metrics.tracker.example
		"in valid line here",
	}, "\n")

	got, skipped, err := ParseBlocklist(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".banner.example.org",
		".leading.example.net",
		".plain.example.io",
		".tracker.example",
		".wild.example.net",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("domains:\n got  %v\n want %v", got, want)
	}
	// Lines that looked like entries but were not usable domains are counted.
	if skipped < 5 {
		t.Errorf("skipped = %d, want the unusable lines counted", skipped)
	}
}

func TestParseBlocklistDropsEntriesCoveredByAParent(t *testing.T) {
	got, _, _ := ParseBlocklist(strings.NewReader("a.b.c.example.com\nb.c.example.com\nother.example.com\nc.example.com\nexample.org\nwww.example.org\n"))
	if strings.Join(got, ",") != ".c.example.com,.example.org,.other.example.com" {
		t.Fatalf("got %v", got)
	}
}

func TestParseBlocklistRejectsHugeLists(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxBlocklistEntries+10; i++ {
		b.WriteString("d")
		b.WriteString(strings.Repeat("x", i%5))
		b.WriteString(itoa(i))
		b.WriteString(".example.com\n")
	}
	if _, _, err := ParseBlocklist(strings.NewReader(b.String())); err == nil {
		t.Fatal("a list over the entry cap must be refused")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// listServer serves a mutable body and counts requests.
type listServer struct {
	*httptest.Server
	body   string
	status int
	hits   int
}

func newListServer(t *testing.T, body string) *listServer {
	t.Helper()
	ls := &listServer{body: body, status: 200}
	ls.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.hits++
		w.WriteHeader(ls.status)
		w.Write([]byte(ls.body))
	}))
	t.Cleanup(ls.Close)
	return ls
}

// countingFakeSquid counts "-k reconfigure" calls in <dir>/reloads.
func countingFakeSquid(t *testing.T) (bin, dir string) {
	t.Helper()
	bin, dir = writeFakeSquid(t)
	script := `#!/bin/sh
dir=$(dirname "$0")
case "$2" in
  parse) exit 0;;
  reconfigure) echo x >> "$dir/reloads"; exit 0;;
  check) exit 0;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func reloads(dir string) int {
	b, _ := os.ReadFile(filepath.Join(dir, "reloads"))
	return strings.Count(string(b), "x")
}

func newBlocklists(t *testing.T, allowLoopback bool) (*BlocklistService, *AccessManager, string, string, string) {
	t.Helper()
	bin, fakeDir := countingFakeSquid(t)
	confPath := newConf(t, accessConf)
	mgr := NewManager(confPath, bin)
	mgr.SetHistory(newTestHistory(t))
	listDir := filepath.Join(t.TempDir(), "blocklists")
	conn := newAccessDB(t)
	access := NewAccessManager(conn, mgr, listDir)
	return NewBlocklistService(conn, mgr, access, listDir, allowLoopback), access, confPath, listDir, fakeDir
}

const sampleList = "0.0.0.0 ads.example.com\n0.0.0.0 track.example.net\n"

func TestCreateWiresListACLAndRuleIntoSquidConf(t *testing.T) {
	svc, _, confPath, listDir, _ := newBlocklists(t, true)
	srv := newListServer(t, sampleList)

	src, err := svc.Create("ads", srv.URL, 24, true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if src.EntryCount != 2 || !strings.HasPrefix(src.LastStatus, "ok") || src.ACLName != "bl_ads" {
		t.Fatalf("the first download must have run: %+v", src)
	}

	file, _ := os.ReadFile(filepath.Join(listDir, itoa(int(src.ID))+".txt"))
	if !strings.Contains(string(file), "\n.ads.example.com\n") || !strings.Contains(string(file), "\n.track.example.net\n") {
		t.Fatalf("list file:\n%s", file)
	}
	conf := readConf(t, confPath)
	if !strings.Contains(conf, `acl sqa_bl_ads dstdomain "`+filepath.Join(listDir, itoa(int(src.ID))+".txt")+`"`) ||
		!strings.Contains(conf, "http_access deny sqa_bl_ads") {
		t.Fatalf("the ACL and deny rule must be in squid.conf:\n%s", conf)
	}
	if !(idx(conf, accessBlockStart) < idx(conf, "http_access allow localhost")) {
		t.Fatal("the block list rule must be evaluated before the stock allows")
	}
}

func TestRefreshOnlyRewritesAndReloadsWhenTheListChanged(t *testing.T) {
	svc, _, _, listDir, fakeDir := newBlocklists(t, true)
	srv := newListServer(t, sampleList)
	src, err := svc.Create("ads", srv.URL, 24, true)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(listDir, itoa(int(src.ID))+".txt")
	before, _ := os.Stat(path)
	time.Sleep(20 * time.Millisecond)

	res, err := svc.RefreshNow(src.ID)
	if err != nil || res.Changed {
		t.Fatalf("an identical download is not a change: %+v %v", res, err)
	}
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged list must not be rewritten")
	}
	baseline := reloads(fakeDir)

	srv.body = sampleList + "0.0.0.0 new.example.org\n"
	res, err = svc.RefreshNow(src.ID)
	if err != nil || !res.Changed || res.Entries != 3 {
		t.Fatalf("a changed list must be picked up: %+v %v", res, err)
	}
	if reloads(fakeDir) != baseline+1 {
		t.Errorf("a changed list must reload squid exactly once (reloads %d -> %d)", baseline, reloads(fakeDir))
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), ".new.example.org") {
		t.Error("the new entry must be in the file")
	}
}

func TestAFailedOrEmptyDownloadKeepsTheWorkingList(t *testing.T) {
	svc, _, _, listDir, _ := newBlocklists(t, true)
	srv := newListServer(t, sampleList)
	src, _ := svc.Create("ads", srv.URL, 24, false)
	path := filepath.Join(listDir, itoa(int(src.ID))+".txt")
	good, _ := os.ReadFile(path)

	for name, setup := range map[string]func(){
		"server error":       func() { srv.status, srv.body = 500, "oops" },
		"empty body":         func() { srv.status, srv.body = 200, "" },
		"only comments":      func() { srv.status, srv.body = 200, "# nothing here\n! at all\n" },
		"an HTML error page": func() { srv.status, srv.body = 200, "<html><body>Not found</body></html>\n" },
	} {
		setup()
		if _, err := svc.Refresh(src.ID); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		if now, _ := os.ReadFile(path); string(now) != string(good) {
			t.Errorf("%s: the working list must be kept", name)
		}
		got, _ := svc.Get(src.ID)
		if !strings.HasPrefix(got.LastStatus, "error:") {
			t.Errorf("%s: status = %q", name, got.LastStatus)
		}
		if got.EntryCount != 2 {
			t.Errorf("%s: entry count must still describe the list in use, got %d", name, got.EntryCount)
		}
	}
}

func TestFetchingRefusesInternalAddresses(t *testing.T) {
	svc, _, _, _, _ := newBlocklists(t, false)
	srv := newListServer(t, sampleList) // 127.0.0.1

	for name, target := range map[string]string{
		"loopback":        srv.URL,
		"cloud metadata":  "http://169.254.169.254/latest/meta-data/",
		"unspecified":     "http://0.0.0.0:9/",
		"link-local IPv6": "http://[fe80::1]:9/",
	} {
		_, err := svc.download(target)
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s: expected the connection to be refused, got %v", name, err)
		}
	}
	if srv.hits != 0 {
		t.Error("the request must never reach the internal server")
	}

	// A public-looking redirect target is checked too, at connect time.
	redirect := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/", http.StatusFound))
	defer redirect.Close()
	svcLoop, _, _, _, _ := newBlocklists(t, true) // loopback allowed so we can reach the redirector
	if _, err := svcLoop.download(redirect.URL); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("a redirect into a link-local address must be refused, got %v", err)
	}
}

func TestBlocklistSourceValidation(t *testing.T) {
	svc, _, _, _, _ := newBlocklists(t, true)
	for name, c := range map[string]struct {
		name, url string
		hours     int
	}{
		"bad name":      {"Bad Name", "http://a.example/x", 24},
		"too short":     {"ab", "http://a.example/x", 24},
		"no scheme":     {"okname", "a.example/x", 24},
		"ftp":           {"okname", "ftp://a.example/x", 24},
		"credentials":   {"okname", "http://u:p@a.example/x", 24},
		"interval low":  {"okname", "http://a.example/x", 0},
		"interval high": {"okname", "http://a.example/x", 5000},
	} {
		if _, err := svc.Create(c.name, c.url, c.hours, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDeleteRemovesRuleACLFileAndSource(t *testing.T) {
	svc, access, confPath, listDir, _ := newBlocklists(t, true)
	srv := newListServer(t, sampleList)
	src, _ := svc.Create("ads", srv.URL, 24, true)
	path := filepath.Join(listDir, itoa(int(src.ID))+".txt")

	if err := svc.Delete(src.ID, false); err == nil {
		t.Fatal("a list used by a rule must not be deleted silently")
	}
	if err := svc.Delete(src.ID, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the list file must be removed")
	}
	if rules, _ := access.ListRules(); len(rules) != 0 {
		t.Errorf("rules: %+v", rules)
	}
	if acls, _ := access.ListACLs(); len(acls) != 0 {
		t.Errorf("acls: %+v", acls)
	}
	if strings.Contains(readConf(t, confPath), "bl_ads") {
		t.Error("squid.conf must no longer mention the list")
	}
	if l, _ := svc.List(); len(l) != 0 {
		t.Errorf("sources: %+v", l)
	}
}

func TestRefreshDueRespectsIntervalAndTheEnabledSwitchAndReloadsOnce(t *testing.T) {
	svc, _, _, _, fakeDir := newBlocklists(t, true)
	a := newListServer(t, "0.0.0.0 a.example.com\n")
	b := newListServer(t, "0.0.0.0 b.example.com\n")
	sa, _ := svc.Create("list-a", a.URL, 24, false)
	sb, _ := svc.Create("list-b", b.URL, 24, false)
	a.hits, b.hits = 0, 0

	now := time.Now()
	svc.now = func() time.Time { return now }
	svc.RefreshDue()
	if a.hits+b.hits != 0 {
		t.Fatalf("nothing is due right after the first download (hits %d/%d)", a.hits, b.hits)
	}

	now = now.Add(25 * time.Hour)
	a.body, b.body = "0.0.0.0 a2.example.com\n", "0.0.0.0 b2.example.com\n"
	baseline := reloads(fakeDir)
	svc.RefreshDue()
	if a.hits != 1 || b.hits != 1 {
		t.Fatalf("both lists are due: hits %d/%d", a.hits, b.hits)
	}
	if reloads(fakeDir) != baseline+1 {
		t.Errorf("two changed lists must cause ONE reload, got %d", reloads(fakeDir)-baseline)
	}

	off := false
	svc.Update(sb.ID, &off, nil, nil)
	now = now.Add(25 * time.Hour)
	a.hits, b.hits = 0, 0
	svc.RefreshDue()
	if a.hits != 1 || b.hits != 0 {
		t.Errorf("a disabled list must be skipped: hits %d/%d", a.hits, b.hits)
	}
	_ = sa
}

func TestDownloadsOverTheSizeCapAreRefused(t *testing.T) {
	svc, _, _, listDir, _ := newBlocklists(t, true)
	srv := newListServer(t, sampleList)
	src, err := svc.Create("ads", srv.URL, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(listDir, itoa(int(src.ID))+".txt")
	good, _ := os.ReadFile(path)

	svc.maxBytes = 64
	srv.body = strings.Repeat("0.0.0.0 filler.example.com\n", 20)
	_, err = svc.Refresh(src.ID)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized list must be refused, got %v", err)
	}
	if now, _ := os.ReadFile(path); string(now) != string(good) {
		t.Error("the working list must be kept")
	}

	// A list exactly at the cap is fine; one byte more is not.
	srv.body = "0.0.0.0 a.example.com\n"
	svc.maxBytes = int64(len(srv.body))
	if _, err := svc.Refresh(src.ID); err != nil {
		t.Errorf("a list exactly at the cap must be accepted: %v", err)
	}
	svc.maxBytes--
	if _, err := svc.Refresh(src.ID); err == nil {
		t.Error("one byte over the cap must be refused")
	}
}
