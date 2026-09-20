package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"squidadmin/backend/internal/db"
)

const base = int64(1789800000) // 2026-09-19, a fixed clock for the tests

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// logLine builds a native-format access.log line.
func logLine(ts int64, client, code string, status int, bytes int, method, url, user string) string {
	if user == "" {
		user = "-"
	}
	return fmt.Sprintf("%d.123 %6d %s %s/%d %d %s %s %s HIER_NONE/- text/html\n", ts, 5, client, code, status, bytes, method, url, user)
}

func appendTo(t *testing.T, path string, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

type rig struct {
	conn *sql.DB
	path string
	ing  *Ingestor
	st   *Store
}

func newRig(t *testing.T) *rig {
	t.Helper()
	conn := newDB(t)
	path := filepath.Join(t.TempDir(), "access.log")
	ing := NewIngestor(conn, path)
	ing.now = func() time.Time { return time.Unix(base+7200, 0) }
	st := NewStore(conn)
	st.now = ing.now
	return &rig{conn: conn, path: path, ing: ing, st: st}
}

func (r *rig) poll(t *testing.T) int {
	t.Helper()
	n, err := r.ing.Poll()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return n
}

func (r *rig) totalRequests(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := r.conn.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM stats_hourly`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestParseLine(t *testing.T) {
	cases := []struct {
		name                 string
		line                 string
		ok                   bool
		client, domain, user string
		status               int
		hit, denied, failed  bool
	}{
		{"plain hit", "1789820492.486      0 10.0.0.5 TCP_MEM_HIT/200 3410 GET http://Example.COM/a?b=1 - HIER_NONE/- text/html", true, "10.0.0.5", "example.com", "", 200, true, false, false},
		{"miss with user", "1789820492.486    120 10.0.0.5 TCP_MISS/200 51200 GET https://www.site.org:8443/x alice HIER_DIRECT/1.2.3.4 text/html", true, "10.0.0.5", "www.site.org", "alice", 200, false, false, false},
		{"CONNECT tunnel", "1789820492.486   3000 10.0.0.6 TCP_TUNNEL/200 9000 CONNECT secure.example.net:443 bob HIER_DIRECT/1.2.3.4 -", true, "10.0.0.6", "secure.example.net", "bob", 200, false, false, false},
		{"denied", "1789820492.486      0 127.0.0.1 TCP_DENIED/403 3410 GET http://blocked.example/ - HIER_NONE/- text/html", true, "127.0.0.1", "blocked.example", "", 403, false, true, false},
		{"upstream failure", "1789820492.486      7 127.0.0.1 TCP_MISS_ABORTED/503 3576 GET http://down.example/ - HIER_NONE/- text/html", true, "127.0.0.1", "down.example", "", 503, false, false, true},
		{"ipv6 host", "1789820492.486      1 ::1 TCP_MISS/200 10 GET http://[2001:db8::1]:8080/p - HIER_DIRECT/x -", true, "::1", "2001:db8::1", "", 200, false, false, false},
		{"userinfo in the URL", "1789820492.486      1 10.0.0.1 TCP_MISS/200 10 GET http://user:pw@host.example/p - HIER_DIRECT/x -", true, "10.0.0.1", "host.example", "", 200, false, false, false},
		{"refresh hit", "1789820492.486      1 10.0.0.1 TCP_REFRESH_UNMODIFIED/304 10 GET http://a.example/ - HIER_DIRECT/x -", true, "10.0.0.1", "a.example", "", 304, false, false, false},
		{"too few fields", "1789820492.486 0 10.0.0.1 TCP_MISS/200", false, "", "", "", 0, false, false, false},
		{"bad timestamp", "notatime 0 10.0.0.1 TCP_MISS/200 1 GET http://a.example/ - H/- -", false, "", "", "", 0, false, false, false},
		{"no slash in the result", "1789820492.486 0 10.0.0.1 TCPMISS200 1 GET http://a.example/ - H/- -", false, "", "", "", 0, false, false, false},
		{"bytes not a number", "1789820492.486 0 10.0.0.1 TCP_MISS/200 lots GET http://a.example/ - H/- -", false, "", "", "", 0, false, false, false},
		{"blank", "", false, "", "", "", 0, false, false, false},
	}
	for _, c := range cases {
		e, ok := ParseLine(c.line)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if e.Client != c.client || e.Domain != c.domain || e.User != c.user || e.Status != c.status {
			t.Errorf("%s: got client=%q domain=%q user=%q status=%d", c.name, e.Client, e.Domain, e.User, e.Status)
		}
		if e.Hit() != c.hit || e.Denied() != c.denied || e.Failed() != c.failed {
			t.Errorf("%s: hit=%v denied=%v failed=%v, want %v %v %v", c.name, e.Hit(), e.Denied(), e.Failed(), c.hit, c.denied, c.failed)
		}
	}

	// A 407 is a login challenge, not a request of its own.
	e, _ := ParseLine("1789820492.486 0 10.0.0.1 TCP_DENIED/407 3900 GET http://a.example/ - HIER_NONE/- text/html")
	if !e.Challenge() || e.Denied() {
		t.Error("407 must be a challenge and not a denial")
	}
}

func TestIngestAggregatesPerHourClientUserAndDomain(t *testing.T) {
	r := newRig(t)
	h1, h2 := base, base+3600
	appendTo(t, r.path,
		logLine(h1, "10.0.0.1", "TCP_MISS", 200, 1000, "GET", "http://a.example/1", "alice")+
			logLine(h1+5, "10.0.0.1", "TCP_MEM_HIT", 200, 500, "GET", "http://a.example/2", "alice")+
			logLine(h1+9, "10.0.0.2", "TCP_MISS", 200, 2000, "GET", "http://b.example/", "")+
			logLine(h1+10, "10.0.0.2", "TCP_DENIED", 403, 3400, "GET", "http://bad.example/x", "")+
			logLine(h1+11, "10.0.0.2", "TCP_MISS_ABORTED", 503, 3500, "GET", "http://down.example/", "")+
			logLine(h1+12, "10.0.0.1", "TCP_DENIED", 407, 3900, "GET", "http://a.example/3", "")+ // challenge: ignored
			logLine(h2, "10.0.0.1", "TCP_MISS", 200, 100, "GET", "http://a.example/4", "alice")+
			"garbage that is not a log line\n")

	if n := r.poll(t); n != 6 {
		t.Fatalf("counted %d request lines, want 6 (the 407 challenge and the garbage line are not requests)", n)
	}

	type row struct{ req, bytes, hits, denied, errs int64 }
	get := func(hour int64, client, user, domain string) row {
		var x row
		err := r.conn.QueryRow(`SELECT requests, bytes, hits, denied, errors FROM stats_hourly WHERE hour=? AND client=? AND user=? AND domain=?`,
			hour/3600*3600, client, user, domain).Scan(&x.req, &x.bytes, &x.hits, &x.denied, &x.errs)
		if err != nil {
			t.Fatalf("row %d/%s/%s/%s: %v", hour, client, user, domain, err)
		}
		return x
	}
	if got := get(h1, "10.0.0.1", "alice", "a.example"); got != (row{2, 1500, 1, 0, 0}) {
		t.Errorf("alice@a.example: %+v", got)
	}
	if got := get(h1, "10.0.0.2", "", "bad.example"); got != (row{1, 3400, 0, 1, 0}) {
		t.Errorf("denied row: %+v", got)
	}
	if got := get(h1, "10.0.0.2", "", "down.example"); got != (row{1, 3500, 0, 0, 1}) {
		t.Errorf("failed row: %+v", got)
	}
	if got := get(h2, "10.0.0.1", "alice", "a.example"); got.req != 1 {
		t.Errorf("the next hour is a separate row: %+v", got)
	}

	denied, err := r.st.Denied(10)
	if err != nil || len(denied) != 1 || denied[0].URL != "http://bad.example/x" || denied[0].Client != "10.0.0.2" {
		t.Errorf("denied list: %+v %v", denied, err)
	}
}

func TestPartialLineWaitsForItsNewlineAndIsCountedOnce(t *testing.T) {
	r := newRig(t)
	full := logLine(base, "10.0.0.1", "TCP_MISS", 200, 10, "GET", "http://a.example/", "")
	appendTo(t, r.path, full[:len(full)-20]) // squid is mid-write

	if n := r.poll(t); n != 0 {
		t.Fatalf("an unfinished line must not be counted, got %d", n)
	}
	appendTo(t, r.path, full[len(full)-20:])
	if n := r.poll(t); n != 1 {
		t.Fatalf("the completed line must be counted once, got %d", n)
	}
	for i := 0; i < 3; i++ {
		if n := r.poll(t); n != 0 {
			t.Fatalf("polling again must not recount, got %d", n)
		}
	}
	if got := r.totalRequests(t); got != 1 {
		t.Errorf("total = %d, want 1", got)
	}
}

// squid renames access.log and starts a new one; lines written to the old file
// before it reopens must not be lost, and nothing may be counted twice.
func TestRotationLosesAndDuplicatesNothing(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/1", "")+
		logLine(base+1, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/2", ""))
	r.poll(t)

	// Late writes into the old file, the rename, then a new file.
	appendTo(t, r.path, logLine(base+2, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/3", ""))
	if err := os.Rename(r.path, r.path+".0"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, r.path, logLine(base+3, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/4", "")+
		logLine(base+4, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/5", ""))

	if n := r.poll(t); n != 3 {
		t.Fatalf("one late line from the old file and two from the new one: got %d", n)
	}
	if got := r.totalRequests(t); got != 5 {
		t.Fatalf("total = %d, want 5", got)
	}

	// The new file keeps being followed.
	appendTo(t, r.path, logLine(base+5, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/6", ""))
	if n := r.poll(t); n != 1 {
		t.Fatalf("the new file must be followed, got %d", n)
	}
	if got := r.totalRequests(t); got != 6 {
		t.Fatalf("total = %d, want 6", got)
	}
}

func TestTruncatedLogIsReadFromTheStart(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, strings.Repeat(logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""), 5))
	r.poll(t)

	if err := os.Truncate(r.path, 0); err != nil {
		t.Fatal(err)
	}
	r.poll(t) // notices the shrink
	appendTo(t, r.path, logLine(base+9, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://b.example/", ""))
	if n := r.poll(t); n != 1 {
		t.Fatalf("after a truncation the new content must be read, got %d", n)
	}
}

func TestRestartResumesWithoutDoubleCountingOrSkipping(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, strings.Repeat(logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""), 4))
	r.poll(t)

	again := NewIngestor(r.conn, r.path) // the panel restarted
	again.now = r.ing.now
	if n, err := again.Poll(); err != nil || n != 0 {
		t.Fatalf("a restart must not recount old lines: n=%d err=%v", n, err)
	}
	appendTo(t, r.path, logLine(base+5, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""))
	if n, _ := again.Poll(); n != 1 {
		t.Fatalf("only the new line must be counted after a restart, got %d", n)
	}
	if got := r.totalRequests(t); got != 5 {
		t.Fatalf("total = %d, want 5", got)
	}
}

// A file that was empty when the panel first opened it has no fingerprint; the
// panel must still recognise it after a restart.
func TestFileThatStartedEmptyIsRecognisedAfterARestart(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, "")
	r.poll(t) // empty
	appendTo(t, r.path, strings.Repeat(logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""), 3))
	r.poll(t)

	again := NewIngestor(r.conn, r.path)
	again.now = r.ing.now
	if n, _ := again.Poll(); n != 0 {
		t.Fatalf("recounted %d lines after a restart", n)
	}
	if got := r.totalRequests(t); got != 3 {
		t.Fatalf("total = %d, want 3", got)
	}
}

func TestADifferentFileAfterARestartIsReadInFull(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://old.example/", ""))
	r.poll(t)

	// While the panel was down the log was rotated: same path, new content.
	os.Remove(r.path)
	appendTo(t, r.path, logLine(base+100, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://new1.example/", "")+
		logLine(base+101, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://new2.example/", ""))

	again := NewIngestor(r.conn, r.path)
	again.now = r.ing.now
	if n, _ := again.Poll(); n != 2 {
		t.Fatalf("all of the new file is new: got %d, want 2", n)
	}
}

func TestFirstSightOfABigLogImportsOnlyTheTailOnLineBoundaries(t *testing.T) {
	r := newRig(t)
	line := logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", "")
	appendTo(t, r.path, strings.Repeat(line, 100))
	r.ing.backfill = int64(len(line))*10 + 7 // ends in the middle of a line

	n := r.poll(t)
	if n != 9 && n != 10 {
		t.Fatalf("expected the last ~10 lines, got %d", n)
	}
	var bad int
	r.conn.QueryRow(`SELECT COUNT(*) FROM stats_hourly WHERE domain NOT IN ('a.example')`).Scan(&bad)
	if bad != 0 {
		t.Fatalf("a half line was parsed as data: %d stray rows", bad)
	}
}

func TestRecentCountsAreWindowed(t *testing.T) {
	r := newRig(t) // now = base + 7200
	nowTs := base + 7200
	appendTo(t, r.path,
		logLine(nowTs-30, "10.0.0.1", "TCP_DENIED", 403, 1, "GET", "http://a.example/", "")+
			logLine(nowTs-90, "10.0.0.1", "TCP_MISS_ABORTED", 503, 1, "GET", "http://b.example/", "")+
			logLine(nowTs-400, "10.0.0.1", "TCP_DENIED", 403, 1, "GET", "http://c.example/", "")+
			logLine(nowTs-4000, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://d.example/", ""))
	r.poll(t)

	req, denied, errs := r.ing.Recent(2 * time.Minute)
	if req != 2 || denied != 1 || errs != 1 {
		t.Errorf("last 2 minutes: req=%d denied=%d errors=%d", req, denied, errs)
	}
	_, denied, _ = r.ing.Recent(10 * time.Minute)
	if denied != 2 {
		t.Errorf("last 10 minutes: denied=%d, want 2", denied)
	}
}

func TestPollsFromSeveralGoroutinesDoNotDoubleCount(t *testing.T) {
	r := newRig(t)
	appendTo(t, r.path, strings.Repeat(logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""), 50))

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ing.Poll()
		}()
	}
	wg.Wait()
	if got := r.totalRequests(t); got != 50 {
		t.Fatalf("total = %d, want exactly 50", got)
	}
}

// ---------------------------------------------------------------- statistics

func seed(t *testing.T, r *rig) {
	t.Helper()
	now := base + 7200
	var b strings.Builder
	add := func(ts int64, client, code string, status, bytes int, url, user string) {
		b.WriteString(logLine(ts, client, code, status, bytes, "GET", url, user))
	}
	// last 24h
	add(now-600, "10.0.0.1", "TCP_MISS", 200, 4000, "http://a.example/", "alice")
	add(now-500, "10.0.0.1", "TCP_MEM_HIT", 200, 1000, "http://a.example/", "alice")
	add(now-400, "10.0.0.1", "TCP_MISS", 200, 300, "http://b.example/", "alice")
	add(now-300, "10.0.0.2", "TCP_MISS", 200, 100, "http://a.example/", "bob")
	add(now-200, "10.0.0.2", "TCP_DENIED", 403, 3400, "http://bad.example/", "bob")
	add(now-100, "10.0.0.3", "TCP_DENIED", 403, 3400, "http://bad.example/", "")
	add(now-50, "10.0.0.3", "TCP_DENIED", 403, 3400, "http://worse.example/", "")
	// 3 days ago: only in the 7d and 30d ranges
	add(now-3*86400, "10.0.0.9", "TCP_MISS", 200, 50, "http://old.example/", "carol")
	// 20 days ago: only in 30d
	add(now-20*86400, "10.0.0.8", "TCP_MISS", 200, 70, "http://ancient.example/", "")
	appendTo(t, r.path, b.String())
	r.poll(t)
}

func TestSummaryTotalsAndRanges(t *testing.T) {
	r := newRig(t)
	seed(t, r)

	s, err := r.st.Summary("24h")
	if err != nil {
		t.Fatal(err)
	}
	if s.Requests != 7 || s.Bytes != 4000+1000+300+100+3*3400 || s.Hits != 1 || s.Denied != 3 {
		t.Errorf("24h totals: %+v", s)
	}
	if s.Clients != 3 || s.Users != 2 || s.Domains != 4 {
		t.Errorf("24h distinct counts: clients=%d users=%d domains=%d", s.Clients, s.Users, s.Domains)
	}
	if s7, _ := r.st.Summary("7d"); s7.Requests != 8 {
		t.Errorf("7d requests = %d, want 8", s7.Requests)
	}
	if s30, _ := r.st.Summary("30d"); s30.Requests != 9 {
		t.Errorf("30d requests = %d, want 9", s30.Requests)
	}
	if _, err := r.st.Summary("1y"); err == nil {
		t.Error("an unknown range must be rejected")
	}
}

func TestTimelineHasNoGapsAndSumsToTheTotal(t *testing.T) {
	r := newRig(t)
	seed(t, r)

	for _, rng := range []string{"24h", "7d", "30d"} {
		s, _ := r.st.Summary(rng)
		ri, _ := lookupRange(rng)
		var sum int64
		for i, b := range s.Timeline {
			sum += b.Requests
			if i > 0 && b.T-s.Timeline[i-1].T != int64(ri.Bucket/time.Second) {
				t.Fatalf("%s: gap between buckets %d and %d", rng, s.Timeline[i-1].T, b.T)
			}
		}
		if sum != s.Requests {
			t.Errorf("%s: timeline sums to %d, total is %d", rng, sum, s.Requests)
		}
		if len(s.Timeline) < 2 {
			t.Errorf("%s: timeline too short: %d", rng, len(s.Timeline))
		}
	}
}

func TestTopLists(t *testing.T) {
	r := newRig(t)
	seed(t, r)

	keys := func(rows []TopRow) string {
		var k []string
		for _, x := range rows {
			k = append(k, fmt.Sprintf("%s=%d", x.Key, x.Requests))
		}
		return strings.Join(k, ",")
	}

	rows, _ := r.st.Top("24h", "domain", "requests", 10)
	if got := keys(rows); got != "a.example=3,bad.example=2,b.example=1,worse.example=1" {
		t.Errorf("top domains: %s", got)
	}
	rows, _ = r.st.Top("24h", "domain", "bytes", 2)
	if len(rows) != 2 || rows[0].Key != "bad.example" {
		t.Errorf("top by bytes, limit 2: %+v", rows)
	}
	rows, _ = r.st.Top("24h", "client", "requests", 10)
	if got := keys(rows); got != "10.0.0.1=3,10.0.0.2=2,10.0.0.3=2" {
		t.Errorf("top clients: %s", got)
	}
	rows, _ = r.st.Top("24h", "user", "requests", 10)
	if got := keys(rows); got != "alice=3,bob=2" {
		t.Errorf("top users must skip anonymous traffic: %s", got)
	}
	rows, _ = r.st.Top("24h", "domain", "denied", 10)
	if got := keys(rows); got != "bad.example=2,worse.example=1" || rows[0].Denied != 2 {
		t.Errorf("most blocked domains: %s", got)
	}
	rows, _ = r.st.Top("24h", "client", "denied", 10)
	if len(rows) != 2 {
		t.Errorf("only clients with denials belong here: %+v", rows)
	}

	for _, bad := range [][3]string{{"24h", "sql", "requests"}, {"24h", "domain", "1; DROP TABLE x"}, {"9y", "domain", "requests"}} {
		if _, err := r.st.Top(bad[0], bad[1], bad[2], 10); err == nil {
			t.Errorf("Top(%v) must be rejected", bad)
		}
	}
	var n int
	r.conn.QueryRow(`SELECT COUNT(*) FROM stats_hourly`).Scan(&n)
	if n == 0 {
		t.Fatal("the statistics table must still exist")
	}
}

func TestDeniedListIsNewestFirstAndLimited(t *testing.T) {
	r := newRig(t)
	seed(t, r)
	rows, err := r.st.Denied(2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("Denied: %+v %v", rows, err)
	}
	if rows[0].URL != "http://worse.example/" || rows[1].URL != "http://bad.example/" {
		t.Errorf("newest first: %+v", rows)
	}
}

func TestPruneRemovesOldStatisticsAndTrimsTheDeniedList(t *testing.T) {
	r := newRig(t)
	seed(t, r)

	if err := r.ing.Prune(10 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.st.Summary("30d"); s.Requests != 8 {
		t.Errorf("the 20-day-old row must be pruned, requests = %d", s.Requests)
	}

	for i := 0; i < maxDeniedRows+50; i++ {
		r.conn.Exec(`INSERT INTO denied_log (ts, client, user, method, url) VALUES (?, 'c', '', 'GET', 'u')`, base+7200)
	}
	if err := r.ing.Prune(30 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	var n int
	r.conn.QueryRow(`SELECT COUNT(*) FROM denied_log`).Scan(&n)
	if n != maxDeniedRows {
		t.Errorf("the denied list must be capped at %d, has %d", maxDeniedRows, n)
	}
}

func TestStatusReportsHealth(t *testing.T) {
	r := newRig(t)
	if st := r.ing.Status(); st.Readable {
		t.Error("a missing log is not readable")
	}
	if _, err := r.ing.Poll(); err == nil {
		t.Error("polling a missing log must report an error")
	}
	if r.ing.Status().LastError == "" {
		t.Error("the error must be visible in Status")
	}

	appendTo(t, r.path, logLine(base, "10.0.0.1", "TCP_MISS", 200, 1, "GET", "http://a.example/", ""))
	r.poll(t)
	st := r.ing.Status()
	if !st.Readable || st.Lines != 1 || st.LastEntry != base || st.LastError != "" {
		t.Errorf("status: %+v", st)
	}
}

func TestUserBytesSince(t *testing.T) {
	r := newRig(t)
	seed(t, r)
	hour := (base + 7200) / 3600 * 3600

	got, err := r.st.UserBytesSince(hour)
	if err != nil {
		t.Fatal(err)
	}
	if got["alice"] != 5300 || got["bob"] != 100+3400 || len(got) != 2 {
		t.Errorf("today's bytes: %v (users without a login must not appear, older days must not count)", got)
	}
	if got, _ := r.st.UserBytesSince(hour + 3600); len(got) != 0 {
		t.Errorf("nothing was transferred after that hour: %v", got)
	}
	if got, _ := r.st.UserBytesSince(hour - 1); got["alice"] != 5300 {
		t.Errorf("the hour containing the start time is included whole: %v", got)
	}
	if got, _ := r.st.UserBytesSince(base + 7200 - 4*86400); got["carol"] != 50 {
		t.Errorf("a wider window includes older users: %v", got)
	}
}

// The quota day starts at local midnight, which is not always on an hour
// boundary of the statistics: the hour that contains it must count.
func TestUserBytesSinceCountsTheHourContainingTheStart(t *testing.T) {
	r := newRig(t)
	seed(t, r)
	hour := (base + 7200) / 3600 * 3600

	got, _ := r.st.UserBytesSince(hour + 1800) // half way into the hour the traffic is in
	if got["alice"] != 5300 || got["bob"] != 3500 {
		t.Errorf("a start inside the hour must still include that whole hour: %v", got)
	}
	if got, _ := r.st.UserBytesSince(hour + 3600); len(got) != 0 {
		t.Errorf("but not the hour before it: %v", got)
	}
}
