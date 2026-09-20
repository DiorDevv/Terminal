package monitor

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Info is what squid's cache manager reports about the running process.
type Info struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`

	Version       string  `json:"version"`
	Clients       int     `json:"clients"`
	RequestsTotal int64   `json:"requests_total"`
	AvgPerMinute  float64 `json:"avg_per_minute"`
	FailureRatio  float64 `json:"failure_ratio"`

	HitRatio5     float64 `json:"hit_ratio_5m"`
	HitRatio60    float64 `json:"hit_ratio_60m"`
	ByteHitRatio5 float64 `json:"byte_hit_ratio_5m"`
	ByteHit60     float64 `json:"byte_hit_ratio_60m"`

	SwapKB      int64   `json:"swap_kb"`
	SwapUsedPct float64 `json:"swap_used_pct"`
	MemKB       int64   `json:"mem_kb"`
	MemUsedPct  float64 `json:"mem_used_pct"`

	UptimeSeconds float64 `json:"uptime_seconds"`
	CPUPct        float64 `json:"cpu_pct"`
	CPU5Pct       float64 `json:"cpu_5m_pct"`
	CPU60Pct      float64 `json:"cpu_60m_pct"`
	RSSKB         int64   `json:"rss_kb"`

	FDUsed int `json:"fd_used"`
	FDMax  int `json:"fd_max"`
}

var (
	numberRe = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
	min5Re   = regexp.MustCompile(`5min:\s*(-?\d+(?:\.\d+)?)%`)
	min60Re  = regexp.MustCompile(`60min:\s*(-?\d+(?:\.\d+)?)%`)
	usedRe   = regexp.MustCompile(`(-?\d+(?:\.\d+)?)%\s*used`)
)

func firstFloat(s string) float64 {
	f, _ := strconv.ParseFloat(numberRe.FindString(s), 64)
	return f
}

func captured(re *regexp.Regexp, s string) float64 {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	return f
}

// ParseInfo reads the text of the cache manager's "info" page: lines of the
// form "Key:<tab>value".
func ParseInfo(text string) Info {
	in := Info{Available: true}
	for _, raw := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)

		switch key {
		case "Squid Object Cache":
			in.Version = strings.TrimPrefix(val, "Version ")
		case "Number of clients accessing cache":
			in.Clients = int(firstFloat(val))
		case "Number of HTTP requests received":
			in.RequestsTotal = int64(firstFloat(val))
		case "Average HTTP requests per minute since start":
			in.AvgPerMinute = firstFloat(val)
		case "Request failure ratio":
			in.FailureRatio = firstFloat(val)
		case "Hits as % of all requests":
			in.HitRatio5, in.HitRatio60 = captured(min5Re, val), captured(min60Re, val)
		case "Hits as % of bytes sent":
			in.ByteHitRatio5, in.ByteHit60 = captured(min5Re, val), captured(min60Re, val)
		case "Storage Swap size":
			in.SwapKB = int64(firstFloat(val))
		case "Storage Swap capacity":
			in.SwapUsedPct = captured(usedRe, val)
		case "Storage Mem size":
			in.MemKB = int64(firstFloat(val))
		case "Storage Mem capacity":
			in.MemUsedPct = captured(usedRe, val)
		case "UP Time":
			in.UptimeSeconds = firstFloat(val)
		case "CPU Usage":
			in.CPUPct = firstFloat(val)
		case "CPU Usage, 5 minute avg":
			in.CPU5Pct = firstFloat(val)
		case "CPU Usage, 60 minute avg":
			in.CPU60Pct = firstFloat(val)
		case "Maximum Resident Size":
			in.RSSKB = int64(firstFloat(val))
		case "Maximum number of file descriptors":
			in.FDMax = int(firstFloat(val))
		case "Number of file desc currently in use":
			in.FDUsed = int(firstFloat(val))
		}
	}
	return in
}

// FetchInfo asks squid's cache manager for its info page. url is normally
// http://127.0.0.1:3128/squid-internal-mgr/info; squid only answers the
// manager from localhost (its stock "manager" rules). If an access rule
// blocks it the result says so instead of failing the whole page.
func FetchInfo(url string) Info {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return Info{Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Info{Error: err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		return Info{Error: fmt.Sprintf("squid answered %s (the cache manager is limited to localhost by squid's access rules)", resp.Status)}
	}
	return ParseInfo(string(body))
}

// Usage is the state of a filesystem, in the style of `df`.
type Usage struct {
	Path        string  `json:"path"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// LogFile is one file in squid's log directory.
type LogFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // unix seconds
}

// ListLogFiles lists the regular files of a directory, newest name last.
func ListLogFiles(dir string) ([]LogFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []LogFile{}
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, LogFile{Name: e.Name(), Size: fi.Size(), Modified: fi.ModTime().Unix()})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// TailLines returns the last n lines of a text file (n is capped at 1000). It
// reads at most the final megabyte, so it is safe on huge logs.
func TailLines(path string, n int) ([]string, error) {
	if n < 1 {
		n = 100
	}
	if n > 1000 {
		n = 1000
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const window = 1 << 20
	start := int64(0)
	if fi.Size() > window {
		start = fi.Size() - window
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, err
	}

	text := string(buf)
	if start > 0 {
		// The window began mid-line: drop the fragment.
		if i := strings.IndexByte(text, '\n'); i != -1 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// SafeLogPath resolves name inside dir, refusing anything that is not a plain
// file name (no path separators, no ..).
func SafeLogPath(dir, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid file name")
	}
	return filepath.Join(dir, name), nil
}
