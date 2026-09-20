package monitor

import (
	"database/sql"
	"fmt"
	"time"
)

// Store answers statistics questions from the hourly aggregates.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// RangeInfo describes a supported time range.
type RangeInfo struct {
	Name   string
	Span   time.Duration
	Bucket time.Duration // timeline resolution
}

var ranges = []RangeInfo{
	{"24h", 24 * time.Hour, time.Hour},
	{"7d", 7 * 24 * time.Hour, 6 * time.Hour},
	{"30d", 30 * 24 * time.Hour, 24 * time.Hour},
}

func lookupRange(name string) (RangeInfo, error) {
	for _, r := range ranges {
		if r.Name == name {
			return r, nil
		}
	}
	return RangeInfo{}, fmt.Errorf("range must be one of 24h, 7d, 30d")
}

type Bucket struct {
	T        int64 `json:"t"` // start of the bucket, unix seconds
	Requests int64 `json:"requests"`
	Bytes    int64 `json:"bytes"`
	Hits     int64 `json:"hits"`
	Denied   int64 `json:"denied"`
	Errors   int64 `json:"errors"`
}

type Summary struct {
	Range    string   `json:"range"`
	From     int64    `json:"from"`
	To       int64    `json:"to"`
	Requests int64    `json:"requests"`
	Bytes    int64    `json:"bytes"`
	Hits     int64    `json:"hits"`
	HitBytes int64    `json:"hit_bytes"`
	Denied   int64    `json:"denied"`
	Errors   int64    `json:"errors"`
	Clients  int64    `json:"clients"`
	Users    int64    `json:"users"`
	Domains  int64    `json:"domains"`
	Timeline []Bucket `json:"timeline"`
}

// bucketStart floors t to the bucket grid.
func bucketStart(t int64, size time.Duration) int64 {
	s := int64(size / time.Second)
	return t / s * s
}

// Summary returns totals and a gap-free timeline for the range.
func (s *Store) Summary(rangeName string) (Summary, error) {
	ri, err := lookupRange(rangeName)
	if err != nil {
		return Summary{}, err
	}
	now := s.now().Unix()
	from := bucketStart(now-int64(ri.Span/time.Second), ri.Bucket)

	out := Summary{Range: ri.Name, From: from, To: now, Timeline: []Bucket{}}
	err = s.db.QueryRow(`SELECT COALESCE(SUM(requests),0), COALESCE(SUM(bytes),0), COALESCE(SUM(hits),0),
			COALESCE(SUM(hit_bytes),0), COALESCE(SUM(denied),0), COALESCE(SUM(errors),0),
			COUNT(DISTINCT client), COUNT(DISTINCT CASE WHEN user != '' THEN user END), COUNT(DISTINCT domain)
		FROM stats_hourly WHERE hour >= ?`, from).
		Scan(&out.Requests, &out.Bytes, &out.Hits, &out.HitBytes, &out.Denied, &out.Errors, &out.Clients, &out.Users, &out.Domains)
	if err != nil {
		return Summary{}, err
	}

	rows, err := s.db.Query(`SELECT hour, SUM(requests), SUM(bytes), SUM(hits), SUM(denied), SUM(errors)
		FROM stats_hourly WHERE hour >= ? GROUP BY hour`, from)
	if err != nil {
		return Summary{}, err
	}
	defer rows.Close()

	bySlot := map[int64]*Bucket{}
	for rows.Next() {
		var hour int64
		var r, b, h, d, e int64
		if err := rows.Scan(&hour, &r, &b, &h, &d, &e); err != nil {
			return Summary{}, err
		}
		slot := bucketStart(hour, ri.Bucket)
		bk := bySlot[slot]
		if bk == nil {
			bk = &Bucket{T: slot}
			bySlot[slot] = bk
		}
		bk.Requests += r
		bk.Bytes += b
		bk.Hits += h
		bk.Denied += d
		bk.Errors += e
	}
	if err := rows.Err(); err != nil {
		return Summary{}, err
	}

	step := int64(ri.Bucket / time.Second)
	for t := from; t <= bucketStart(now, ri.Bucket); t += step {
		if bk := bySlot[t]; bk != nil {
			out.Timeline = append(out.Timeline, *bk)
		} else {
			out.Timeline = append(out.Timeline, Bucket{T: t})
		}
	}
	return out, nil
}

type TopRow struct {
	Key      string `json:"key"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
	Hits     int64  `json:"hits"`
	Denied   int64  `json:"denied"`
}

// Top lists the busiest domains, clients or users. by is domain, client or
// user; sortBy is requests, bytes or denied.
func (s *Store) Top(rangeName, by, sortBy string, limit int) ([]TopRow, error) {
	ri, err := lookupRange(rangeName)
	if err != nil {
		return nil, err
	}
	var col, where string
	switch by {
	case "domain":
		col = "domain"
	case "client":
		col = "client"
	case "user":
		col, where = "user", " AND user != ''"
	default:
		return nil, fmt.Errorf("by must be domain, client or user")
	}
	var order string
	switch sortBy {
	case "", "requests":
		order = "SUM(requests)"
	case "bytes":
		order = "SUM(bytes)"
	case "denied":
		order, where = "SUM(denied)", where+" AND denied > 0"
	default:
		return nil, fmt.Errorf("sort must be requests, bytes or denied")
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}

	from := bucketStart(s.now().Unix()-int64(ri.Span/time.Second), ri.Bucket)
	// col/order/where are chosen from the fixed lists above, never from input.
	rows, err := s.db.Query(fmt.Sprintf(`SELECT %s, SUM(requests), SUM(bytes), SUM(hits), SUM(denied)
		FROM stats_hourly WHERE hour >= ?%s GROUP BY %s ORDER BY %s DESC, %s LIMIT ?`,
		col, where, col, order, col), from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TopRow{}
	for rows.Next() {
		var r TopRow
		if err := rows.Scan(&r.Key, &r.Requests, &r.Bytes, &r.Hits, &r.Denied); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type DeniedRow struct {
	Time   int64  `json:"time"`
	Client string `json:"client"`
	User   string `json:"user"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

// Denied lists the most recent requests squid refused.
func (s *Store) Denied(limit int) ([]DeniedRow, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT ts, client, user, method, url FROM denied_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DeniedRow{}
	for rows.Next() {
		var r DeniedRow
		if err := rows.Scan(&r.Time, &r.Client, &r.User, &r.Method, &r.URL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UserBytesSince returns the bytes each signed-in user has transferred since
// the given unix time, for daily quotas. Statistics are kept per hour, so the
// hour containing `since` is included whole: for a local midnight on a whole-hour
// time zone that is exact, otherwise the count may start up to 30 minutes early.
func (s *Store) UserBytesSince(since int64) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT user, SUM(bytes) FROM stats_hourly WHERE hour >= ? AND user != '' GROUP BY user`,
		since/3600*3600)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var u string
		var n int64
		if err := rows.Scan(&u, &n); err != nil {
			return nil, err
		}
		out[u] = n
	}
	return out, rows.Err()
}
