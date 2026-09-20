package monitor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Captured from a real squid 7.6 (Debian) with `curl .../squid-internal-mgr/info`.
const realInfoPage = `Squid Object Cache: Version 7.6
Build Info: Debian GNU/Linux forky/sid
Service Name: squid
Start Time:	Sat, 19 Sep 2026 10:47:56 GMT
Current Time:	Sat, 19 Sep 2026 12:25:02 GMT
Connection information for squid:
	Number of clients accessing cache:	2
	Number of HTTP requests received:	206
	Number of ICP messages received:	0
	Request failure ratio:	 0.00
	Average HTTP requests per minute since start:	2.1
	Select loop called: 8686 times, 670.780 ms avg
Cache information for squid:
	Hits as % of all requests:	5min: 5.9%, 60min: 1.5%
	Hits as % of bytes sent:	5min: 100.0%, 60min: 87.5%
	Memory hits as % of hit requests:	5min: 100.0%, 60min: 100.0%
	Storage Swap size:	5120 KB
	Storage Swap capacity:	 12.5% used, 87.5% free
	Storage Mem size:	220 KB
	Storage Mem capacity:	 0.1% used, 99.9% free
	Mean Object Size:	0.00 KB
Resource usage for squid:
	UP Time:	5826.392 seconds
	CPU Time:	3.954 seconds
	CPU Usage:	0.07%
	CPU Usage, 5 minute avg:	0.21%
	CPU Usage, 60 minute avg:	0.07%
	Maximum Resident Size: 100656 KB
File descriptor usage for squid:
	Maximum number of file descriptors:   1024
	Largest file desc currently in use:     13
	Number of file desc currently in use:    7
`

func TestParseInfoOnARealPage(t *testing.T) {
	in := ParseInfo(realInfoPage)
	if !in.Available || in.Version != "7.6" {
		t.Fatalf("version/available: %+v", in)
	}
	checks := map[string][2]any{
		"clients":        {in.Clients, 2},
		"requests":       {in.RequestsTotal, int64(206)},
		"avg per minute": {in.AvgPerMinute, 2.1},
		"hit 5m":         {in.HitRatio5, 5.9},
		"hit 60m":        {in.HitRatio60, 1.5},
		"byte hit 5m":    {in.ByteHitRatio5, 100.0},
		"byte hit 60m":   {in.ByteHit60, 87.5},
		"swap KB":        {in.SwapKB, int64(5120)},
		"swap used %":    {in.SwapUsedPct, 12.5},
		"mem KB":         {in.MemKB, int64(220)},
		"mem used %":     {in.MemUsedPct, 0.1},
		"uptime":         {in.UptimeSeconds, 5826.392},
		"cpu":            {in.CPUPct, 0.07},
		"cpu 5m":         {in.CPU5Pct, 0.21},
		"cpu 60m":        {in.CPU60Pct, 0.07},
		"rss KB":         {in.RSSKB, int64(100656)},
		"fd used":        {in.FDUsed, 7},
		"fd max":         {in.FDMax, 1024},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %v, want %v", name, c[0], c[1])
		}
	}
}

func TestParseInfoToleratesGarbage(t *testing.T) {
	in := ParseInfo("<html>Internal Error: Missing Template MGR_INDEX</html>\nnot key value\n:\n")
	if in.Clients != 0 || in.Version != "" {
		t.Errorf("garbage must not invent values: %+v", in)
	}
}

func TestFetchInfo(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(realInfoPage)) }))
	defer ok.Close()
	if in := FetchInfo(ok.URL); !in.Available || in.Clients != 2 || in.Error != "" {
		t.Errorf("a healthy manager: %+v", in)
	}

	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "denied", 403) }))
	defer denied.Close()
	in := FetchInfo(denied.URL)
	if in.Available || !strings.Contains(in.Error, "403") {
		t.Errorf("a refusing manager must be reported, not crash: %+v", in)
	}

	if in := FetchInfo("http://127.0.0.1:1/x"); in.Available || in.Error == "" {
		t.Errorf("an unreachable manager: %+v", in)
	}
}

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	short := write("short.log", "one\ntwo\nthree\n")
	got, err := TailLines(short, 2)
	if err != nil || strings.Join(got, "|") != "two|three" {
		t.Errorf("last two lines: %v %v", got, err)
	}
	if got, _ := TailLines(short, 50); strings.Join(got, "|") != "one|two|three" {
		t.Errorf("asking for more than exist: %v", got)
	}
	if got, _ := TailLines(write("empty.log", ""), 5); len(got) != 0 {
		t.Errorf("an empty file has no lines: %v", got)
	}
	if got, _ := TailLines(write("nonl.log", "a\nb"), 5); strings.Join(got, "|") != "a|b" {
		t.Errorf("a file without a final newline: %v", got)
	}

	// A file larger than the read window: only whole lines, from the end.
	var b strings.Builder
	for i := 0; i < 120000; i++ {
		b.WriteString("line-number-")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("-end\n")
	}
	big := write("big.log", b.String())
	got, err = TailLines(big, 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("big file: %v %v", got, err)
	}
	for _, l := range got {
		if !strings.HasPrefix(l, "line-number-") || !strings.HasSuffix(l, "-end") {
			t.Errorf("a partial line leaked out of the read window: %q", l)
		}
	}
	if got, _ := TailLines(big, 100000); len(got) > 1000 {
		t.Errorf("the line count must be capped at 1000, got %d", len(got))
	}
	if _, err := TailLines(filepath.Join(dir, "missing.log"), 5); err == nil {
		t.Error("a missing file is an error")
	}
}

func TestListLogFilesAndSafePath(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cache.log"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "access.log"), []byte("yy"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	files, err := ListLogFiles(dir)
	if err != nil || len(files) != 2 || files[0].Name != "access.log" || files[0].Size != 2 {
		t.Fatalf("files: %+v %v", files, err)
	}

	for _, bad := range []string{"", "../etc/passwd", "a/b.log", ".hidden", "..", "/etc/passwd"} {
		if _, err := SafeLogPath(dir, bad); err == nil {
			t.Errorf("SafeLogPath(%q) must be refused", bad)
		}
	}
	if p, err := SafeLogPath(dir, "cache.log"); err != nil || p != filepath.Join(dir, "cache.log") {
		t.Errorf("a plain name is fine: %q %v", p, err)
	}
}

func TestDiskUsage(t *testing.T) {
	u, err := DiskUsage(t.TempDir())
	if err != nil {
		t.Skip("disk usage unavailable here: ", err)
	}
	if u.TotalBytes == 0 || u.UsedPercent < 0 || u.UsedPercent > 100 || u.UsedBytes+u.FreeBytes > u.TotalBytes+u.TotalBytes/10 {
		t.Errorf("implausible usage: %+v", u)
	}
	if _, err := DiskUsage("/definitely/not/a/path"); err == nil {
		t.Error("a missing path is an error")
	}
}
