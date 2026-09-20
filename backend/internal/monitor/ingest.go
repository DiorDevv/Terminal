package monitor

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// backfillBytes is how much of an existing log is read the first time the
	// panel sees it, so statistics are not empty on day one.
	backfillBytes = 20 << 20
	// maxDeniedRows bounds the "recently blocked" list.
	maxDeniedRows = 5000
	// deniedKeep is how long blocked requests are listed.
	deniedKeep = 7 * 24 * time.Hour
)

type aggKey struct {
	hour                 int64
	client, user, domain string
}

type agg struct {
	requests, bytes, hits, hitBytes, denied, errors int64
}

type minuteCount struct{ requests, denied, errors int }

// Status describes the health of the log reader.
type Status struct {
	Path      string `json:"path"`
	Offset    int64  `json:"offset"`
	Lines     int64  `json:"lines"`      // request lines counted since the panel started
	LastEntry int64  `json:"last_entry"` // timestamp of the newest request seen (unix), 0 = none
	LastPoll  int64  `json:"last_poll"`  // unix
	LastError string `json:"last_error"`
	FileSize  int64  `json:"file_size"`
	Readable  bool   `json:"readable"`
}

// Ingestor reads squid's access.log as it grows and keeps hourly aggregates in
// the database. It survives log rotation (the rotated file is drained before
// the new one is opened) and restarts (its position is stored with a
// fingerprint of the file, so nothing is counted twice or skipped).
type Ingestor struct {
	db   *sql.DB
	path string
	now  func() time.Time

	mu      sync.Mutex
	f       *os.File
	off     int64
	head    string
	minutes map[int64]*minuteCount
	lines   int64
	last    int64
	polled  time.Time
	lastErr string
	// backfill is how much of a pre-existing log is imported on first sight.
	backfill int64
}

func NewIngestor(db *sql.DB, path string) *Ingestor {
	return &Ingestor{db: db, path: path, now: time.Now, minutes: map[int64]*minuteCount{}, backfill: backfillBytes}
}

// fingerprint identifies a log file by its first line, which never changes
// once written (unlike the size). "" means the file has no complete line yet.
func fingerprint(f *os.File) (string, error) {
	buf := make([]byte, 1024)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	nl := strings.IndexByte(string(buf[:n]), '\n')
	if nl == -1 {
		return "", nil
	}
	sum := sha256.Sum256(buf[:nl])
	return hex.EncodeToString(sum[:8]), nil
}

// open opens the log and decides where to start reading.
func (i *Ingestor) open() error {
	f, err := os.Open(i.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	head, err := fingerprint(f)
	if err != nil {
		f.Close()
		return err
	}

	var stPath, stHead string
	var stOff int64
	stored := i.db.QueryRow(`SELECT path, head, offset FROM ingest_state WHERE id = 1`).Scan(&stPath, &stHead, &stOff) == nil

	start := int64(0)
	switch {
	case head == "":
		// Empty or just-created file: read from the very start.
	case stored && stPath == i.path && stHead == head && stOff <= fi.Size():
		start = stOff // the same file we were reading: resume
	case stored:
		// A different file than last time (rotated while we were down): all of
		// it is new.
	case fi.Size() > i.backfill:
		start = fi.Size() - i.backfill // first ever sight of a big log
	}

	if start > 0 && !(stored && stPath == i.path && stHead == head) {
		// Land on a line boundary after an arbitrary byte offset.
		if _, err := f.Seek(start, io.SeekStart); err == nil {
			skipped, _ := bufio.NewReader(f).ReadString('\n')
			start += int64(len(skipped))
		}
	}

	i.f, i.off, i.head = f, start, head
	return nil
}

func (i *Ingestor) closeFile() {
	if i.f != nil {
		i.f.Close()
	}
	i.f, i.off, i.head = nil, 0, ""
}

// Poll reads everything new in the log and stores it. It returns the number of
// request lines counted.
func (i *Ingestor) Poll() (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.polled = i.now()
	total := 0
	// Several rounds so that after a rotation the old file is drained and the
	// new one is picked up in the same poll.
	for round := 0; round < 4; round++ {
		n, rotated, err := i.readAvailable()
		total += n
		if err != nil {
			i.lastErr = err.Error()
			return total, err
		}
		i.lastErr = ""
		if !rotated {
			break
		}
	}
	return total, nil
}

func (i *Ingestor) readAvailable() (counted int, rotated bool, err error) {
	if i.f == nil {
		if err := i.open(); err != nil {
			return 0, false, err
		}
	}
	if _, err := i.f.Seek(i.off, io.SeekStart); err != nil {
		return 0, false, err
	}

	batch := map[aggKey]*agg{}
	var denied []Entry
	minutes := map[int64]*minuteCount{}
	off := i.off
	var newest int64

	r := bufio.NewReaderSize(i.f, 256<<10)
	for {
		line, rerr := r.ReadString('\n')
		if rerr != nil {
			// A last line without its newline is still being written; leave
			// it for the next poll.
			if !errors.Is(rerr, io.EOF) {
				return 0, false, rerr
			}
			break
		}
		off += int64(len(line))

		e, ok := ParseLine(line)
		if !ok || e.Challenge() {
			continue
		}
		counted++
		if e.Time > newest {
			newest = e.Time
		}

		k := aggKey{hour: e.Time / 3600 * 3600, client: e.Client, user: e.User, domain: e.Domain}
		a := batch[k]
		if a == nil {
			a = &agg{}
			batch[k] = a
		}
		a.requests++
		a.bytes += e.Bytes
		mc := minutes[e.Time/60]
		if mc == nil {
			mc = &minuteCount{}
			minutes[e.Time/60] = mc
		}
		mc.requests++
		if e.Hit() {
			a.hits++
			a.hitBytes += e.Bytes
		}
		if e.Denied() {
			a.denied++
			mc.denied++
			denied = append(denied, e)
		}
		if e.Failed() {
			a.errors++
			mc.errors++
		}
	}

	if off != i.off || len(batch) > 0 {
		// A file that was empty when opened has no fingerprint yet; take it now
		// that it has a first line, or a restart would mistake it for a new file
		// and count everything twice.
		if i.head == "" {
			if h, err := fingerprint(i.f); err == nil {
				i.head = h
			}
		}
		if err := i.store(batch, denied, off); err != nil {
			return 0, false, fmt.Errorf("store statistics: %w", err)
		}
		i.off = off
		i.lines += int64(counted)
		if newest > i.last {
			i.last = newest
		}
		for m, c := range minutes {
			cur := i.minutes[m]
			if cur == nil {
				cur = &minuteCount{}
				i.minutes[m] = cur
			}
			cur.requests += c.requests
			cur.denied += c.denied
			cur.errors += c.errors
		}
		i.pruneMinutes()
	}

	// Rotation: the path now points to a different file than the one we hold
	// open (which has just been read to its end), or the file was truncated.
	pathInfo, perr := os.Stat(i.path)
	if perr != nil {
		return counted, false, nil // gone for a moment; keep the handle
	}
	held, herr := i.f.Stat()
	if herr != nil {
		return counted, false, herr
	}
	switch {
	case !os.SameFile(pathInfo, held):
		i.closeFile()
		return counted, true, nil
	case pathInfo.Size() < i.off:
		i.off = 0
	}
	return counted, false, nil
}

// store writes one batch and the new read position in a single transaction, so
// a crash can never count a line twice.
func (i *Ingestor) store(batch map[aggKey]*agg, denied []Entry, off int64) error {
	tx, err := i.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if len(batch) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO stats_hourly (hour, client, user, domain, requests, bytes, hits, hit_bytes, denied, errors)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(hour, client, user, domain) DO UPDATE SET
				requests = requests + excluded.requests, bytes = bytes + excluded.bytes,
				hits = hits + excluded.hits, hit_bytes = hit_bytes + excluded.hit_bytes,
				denied = denied + excluded.denied, errors = errors + excluded.errors`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for k, a := range batch {
			if _, err := stmt.Exec(k.hour, k.client, k.user, k.domain, a.requests, a.bytes, a.hits, a.hitBytes, a.denied, a.errors); err != nil {
				return err
			}
		}
	}
	for _, e := range denied {
		if _, err := tx.Exec(`INSERT INTO denied_log (ts, client, user, method, url) VALUES (?, ?, ?, ?, ?)`,
			e.Time, e.Client, e.User, e.Method, truncate(e.URL, 500)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO ingest_state (id, path, head, offset) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET path = excluded.path, head = excluded.head, offset = excluded.offset`,
		i.path, i.head, off); err != nil {
		return err
	}
	return tx.Commit()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (i *Ingestor) pruneMinutes() {
	cutoff := i.now().Unix()/60 - 180
	for m := range i.minutes {
		if m < cutoff {
			delete(i.minutes, m)
		}
	}
}

// Recent returns how many requests, denials and server errors were logged
// within the last window (minute resolution, kept in memory).
func (i *Ingestor) Recent(window time.Duration) (requests, denied, errs int) {
	i.mu.Lock()
	defer i.mu.Unlock()

	from := i.now().Add(-window).Unix() / 60
	for m, c := range i.minutes {
		if m >= from {
			requests += c.requests
			denied += c.denied
			errs += c.errors
		}
	}
	return
}

func (i *Ingestor) Status() Status {
	i.mu.Lock()
	defer i.mu.Unlock()

	st := Status{Path: i.path, Offset: i.off, Lines: i.lines, LastEntry: i.last, LastError: i.lastErr}
	if !i.polled.IsZero() {
		st.LastPoll = i.polled.Unix()
	}
	if fi, err := os.Stat(i.path); err == nil {
		st.FileSize, st.Readable = fi.Size(), true
	}
	return st
}

// Prune deletes statistics older than retention, and old blocked requests.
func (i *Ingestor) Prune(retention time.Duration) error {
	cutoff := i.now().Add(-retention).Unix()
	if _, err := i.db.Exec(`DELETE FROM stats_hourly WHERE hour < ?`, cutoff); err != nil {
		return err
	}
	if _, err := i.db.Exec(`DELETE FROM denied_log WHERE ts < ?`, i.now().Add(-deniedKeep).Unix()); err != nil {
		return err
	}
	_, err := i.db.Exec(`DELETE FROM denied_log WHERE id NOT IN (SELECT id FROM denied_log ORDER BY id DESC LIMIT ?)`, maxDeniedRows)
	return err
}

// Run polls the log every interval and prunes once a day, until stop closes.
func (i *Ingestor) Run(stop <-chan struct{}, every time.Duration, retention time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		if _, err := i.Poll(); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("access log: %v", err)
		}
		if i.now().Sub(lastPrune) > 24*time.Hour {
			if err := i.Prune(retention); err != nil {
				log.Printf("statistics prune: %v", err)
			}
			lastPrune = i.now()
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}
