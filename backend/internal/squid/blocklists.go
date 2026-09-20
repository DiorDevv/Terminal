package squid

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"squidadmin/backend/internal/netguard"
)

// External block lists: a URL that serves a list of domains (a hosts file, an
// adblock-style list or plain domains). The panel downloads it on a schedule,
// converts it to squid's dstdomain format and points an ACL at the file, so a
// rule such as "deny <this list>" always uses the latest copy.

const (
	maxBlocklistBytes   = 50 << 20 // download size cap
	maxBlocklistEntries = 500_000
)

// BlocklistSource is one downloadable list.
type BlocklistSource struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"interval_hours"`
	LastFetched   int64  `json:"last_fetched"` // unix seconds, 0 = never
	LastStatus    string `json:"last_status"`
	EntryCount    int    `json:"entry_count"`
	ACLID         int64  `json:"acl_id"`
	ACLName       string `json:"acl_name"`
}

type BlocklistService struct {
	db      *sql.DB
	confMgr *Manager
	access  *AccessManager
	dir     string
	client  *http.Client
	now     func() time.Time
	// maxBytes caps one download (tests lower it).
	maxBytes int64

	mu sync.Mutex // one refresh at a time
}

// NewBlocklistService creates the service. allowLoopback lets the panel fetch
// from 127.0.0.1 (tests, or a list served on the same host); link-local
// addresses such as cloud metadata endpoints stay blocked either way.
func NewBlocklistService(db *sql.DB, confMgr *Manager, access *AccessManager, dir string, allowLoopback bool) *BlocklistService {
	return &BlocklistService{
		db: db, confMgr: confMgr, access: access, dir: dir, now: time.Now, maxBytes: maxBlocklistBytes,
		client: netguard.NewClient(allowLoopback, 90*time.Second),
	}
}

func (s *BlocklistService) filePath(id int64) string {
	return s.access.blocklistPath(fmt.Sprint(id))
}

func validateBlocklistURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(raw) > 500 {
		return fmt.Errorf("the URL must be a valid http:// or https:// address")
	}
	if u.User != nil {
		return fmt.Errorf("credentials in the URL are not supported")
	}
	return nil
}

const sourceCols = `s.id, s.name, s.url, s.enabled, s.interval_hours, s.last_fetched, s.last_status, s.entry_count, COALESCE(a.id, 0), COALESCE(a.name, '')`

func scanSource(sc interface{ Scan(...any) error }) (BlocklistSource, error) {
	var b BlocklistSource
	var en int
	if err := sc.Scan(&b.ID, &b.Name, &b.URL, &en, &b.IntervalHours, &b.LastFetched, &b.LastStatus, &b.EntryCount, &b.ACLID, &b.ACLName); err != nil {
		return BlocklistSource{}, err
	}
	b.Enabled = en != 0
	return b, nil
}

func (s *BlocklistService) query(where string, args ...any) ([]BlocklistSource, error) {
	rows, err := s.db.Query(`SELECT `+sourceCols+` FROM blocklist_sources s
		LEFT JOIN acl_objects a ON a.type = 'blocklist' AND a.acl_values = '["' || s.id || '"]' `+where+` ORDER BY s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlocklistSource{}
	for rows.Next() {
		b, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *BlocklistService) List() ([]BlocklistSource, error) { return s.query("") }

func (s *BlocklistService) Get(id int64) (BlocklistSource, error) {
	l, err := s.query("WHERE s.id = ?", id)
	if err != nil {
		return BlocklistSource{}, err
	}
	if len(l) == 0 {
		return BlocklistSource{}, ErrNotFound
	}
	return l[0], nil
}

// Create registers a list, makes its ACL (named bl_<name>) and, optionally, a
// deny rule for it, then downloads it once. A failed first download is not an
// error: the source keeps its error status and is retried on schedule.
func (s *BlocklistService) Create(name, rawURL string, intervalHours int, addRule bool) (BlocklistSource, error) {
	// The name also becomes the ACL name (bl_<name>, at most 40 characters).
	if !aclNamePattern.MatchString(name) || len(name) > 36 {
		return BlocklistSource{}, fmt.Errorf("invalid name: lowercase letters, digits, '_' and '-' only, 3-36 characters")
	}
	if err := validateBlocklistURL(rawURL); err != nil {
		return BlocklistSource{}, err
	}
	if intervalHours < 1 || intervalHours > 720 {
		return BlocklistSource{}, fmt.Errorf("the refresh interval must be 1-720 hours")
	}
	rawURL = strings.TrimSpace(rawURL)

	res, err := s.db.Exec(`INSERT INTO blocklist_sources (name, url, interval_hours) VALUES (?, ?, ?)`, name, rawURL, intervalHours)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return BlocklistSource{}, fmt.Errorf("a list named %q already exists", name)
		}
		return BlocklistSource{}, err
	}
	id, _ := res.LastInsertId()

	undo := func() {
		s.db.Exec(`DELETE FROM acl_objects WHERE type = 'blocklist' AND acl_values = ?`, fmt.Sprintf(`["%d"]`, id))
		s.db.Exec(`DELETE FROM blocklist_sources WHERE id = ?`, id)
		os.Remove(s.filePath(id))
	}

	// squid refuses an ACL whose file is missing, so the file exists (empty)
	// before anything references it.
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		undo()
		return BlocklistSource{}, fmt.Errorf("create block list directory: %w", err)
	}
	if err := writeFileAtomic(s.filePath(id), []byte("# empty until the first download\n"), 0o644); err != nil {
		undo()
		return BlocklistSource{}, fmt.Errorf("create list file: %w", err)
	}
	acl, err := s.access.insertACL("bl_"+name, "blocklist", []string{fmt.Sprint(id)}, false, "Block list: "+name)
	if err != nil {
		undo()
		return BlocklistSource{}, err
	}
	if addRule {
		if _, err := s.access.CreateRule("deny", []RuleTerm{{ACLID: acl.ID}}, "block list: "+name, 0, true); err != nil {
			undo()
			return BlocklistSource{}, err
		}
	}

	if _, err := s.Refresh(id); err != nil {
		log.Printf("block list %s: first download failed: %v", name, err)
	}
	return s.Get(id)
}

// Update changes the schedule, address or on/off switch.
func (s *BlocklistService) Update(id int64, enabled *bool, intervalHours *int, rawURL *string) (BlocklistSource, error) {
	cur, err := s.Get(id)
	if err != nil {
		return BlocklistSource{}, err
	}
	if enabled != nil {
		cur.Enabled = *enabled
	}
	if intervalHours != nil {
		if *intervalHours < 1 || *intervalHours > 720 {
			return BlocklistSource{}, fmt.Errorf("the refresh interval must be 1-720 hours")
		}
		cur.IntervalHours = *intervalHours
	}
	if rawURL != nil {
		if err := validateBlocklistURL(*rawURL); err != nil {
			return BlocklistSource{}, err
		}
		cur.URL = strings.TrimSpace(*rawURL)
	}
	_, err = s.db.Exec(`UPDATE blocklist_sources SET enabled = ?, interval_hours = ?, url = ? WHERE id = ?`,
		boolToInt(cur.Enabled), cur.IntervalHours, cur.URL, id)
	if err != nil {
		return BlocklistSource{}, err
	}
	return s.Get(id)
}

// Delete removes a list. Rules that use it must go too (removeRules), because
// a rule without its ACL cannot exist.
func (s *BlocklistService) Delete(id int64, removeRules bool) error {
	src, err := s.Get(id)
	if err != nil {
		return err
	}
	rules, err := s.access.rawRules(false)
	if err != nil {
		return err
	}
	var using []int64
	for _, r := range rules {
		for _, t := range r.Terms {
			if t.ACLID == src.ACLID {
				using = append(using, r.ID)
			}
		}
	}
	if len(using) > 0 && !removeRules {
		return fmt.Errorf("%w (rule #%d)", ErrACLInUse, using[0])
	}
	for _, rid := range using {
		if err := s.access.DeleteRule(rid); err != nil {
			return err
		}
	}
	s.db.Exec(`DELETE FROM acl_objects WHERE id = ?`, src.ACLID)
	if _, err := s.db.Exec(`DELETE FROM blocklist_sources WHERE id = ?`, id); err != nil {
		return err
	}
	os.Remove(s.filePath(id))
	return nil
}

// RefreshResult reports what a refresh did.
type RefreshResult struct {
	Entries int  `json:"entries"`
	Changed bool `json:"changed"`
}

func (s *BlocklistService) setStatus(id int64, status string, entries int, hash string) {
	if hash != "" {
		s.db.Exec(`UPDATE blocklist_sources SET last_fetched = ?, last_status = ?, entry_count = ?, content_hash = ? WHERE id = ?`,
			s.now().Unix(), status, entries, hash, id)
		return
	}
	s.db.Exec(`UPDATE blocklist_sources SET last_fetched = ?, last_status = ? WHERE id = ?`, s.now().Unix(), status, id)
}

// Refresh downloads one list and, if its content changed, rewrites the list
// file. It does not reload squid; callers do that once for a batch.
func (s *BlocklistService) Refresh(id int64) (RefreshResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src, err := s.Get(id)
	if err != nil {
		return RefreshResult{}, err
	}
	var oldHash string
	s.db.QueryRow(`SELECT content_hash FROM blocklist_sources WHERE id = ?`, id).Scan(&oldHash)

	domains, err := s.download(src.URL)
	if err != nil {
		s.setStatus(id, "error: "+err.Error(), 0, "")
		return RefreshResult{}, err
	}

	var b strings.Builder
	b.WriteString("# managed by squidadmin; source: " + src.URL + "\n")
	for _, d := range domains {
		b.WriteString(d + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	hash := hex.EncodeToString(sum[:])

	res := RefreshResult{Entries: len(domains), Changed: hash != oldHash}
	if res.Changed {
		if err := writeFileAtomic(s.filePath(id), []byte(b.String()), 0o644); err != nil {
			s.setStatus(id, "error: "+err.Error(), 0, "")
			return RefreshResult{}, fmt.Errorf("write list file: %w", err)
		}
	}
	s.setStatus(id, fmt.Sprintf("ok: %d domains", len(domains)), len(domains), hash)
	return res, nil
}

// RefreshNow refreshes one list and reloads squid if it changed.
func (s *BlocklistService) RefreshNow(id int64) (RefreshResult, error) {
	res, err := s.Refresh(id)
	if err != nil {
		return res, err
	}
	if res.Changed {
		if err := s.confMgr.Reconfigure(); err != nil {
			return res, fmt.Errorf("list updated but squid did not reload: %w", err)
		}
	}
	return res, nil
}

// RefreshDue refreshes every enabled list whose interval has elapsed and
// reloads squid once if anything changed.
func (s *BlocklistService) RefreshDue() {
	sources, err := s.List()
	if err != nil {
		log.Printf("block lists: %v", err)
		return
	}
	changed := false
	for _, src := range sources {
		due := s.now().Unix()-src.LastFetched >= int64(src.IntervalHours)*3600
		if !src.Enabled || !due {
			continue
		}
		res, err := s.Refresh(src.ID)
		if err != nil {
			log.Printf("block list %s: %v", src.Name, err)
			continue
		}
		changed = changed || res.Changed
	}
	if changed {
		if err := s.confMgr.Reconfigure(); err != nil {
			log.Printf("block lists: squid did not reload: %v", err)
		}
	}
}

// Run refreshes due lists periodically until ctx ends.
func (s *BlocklistService) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RefreshDue()
		}
	}
}

func (s *BlocklistService) download(rawURL string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "squidadmin-blocklist/1")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}

	limited := &io.LimitedReader{R: resp.Body, N: s.maxBytes + 1}
	domains, _, err := ParseBlocklist(limited)
	if err != nil {
		return nil, err
	}
	if limited.N <= 0 {
		return nil, fmt.Errorf("the list is larger than %s", humanKB(s.maxBytes/1024))
	}
	if len(domains) == 0 {
		// An empty result almost always means a wrong URL or a format the
		// parser does not know; replacing a working list with nothing would
		// silently turn the blocking off.
		return nil, errors.New("no domains found in the downloaded list")
	}
	return domains, nil
}

var hostsPlaceholders = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true, "broadcasthost": true,
	"ip6-localhost": true, "ip6-loopback": true, "0.0.0.0": true,
}

// ParseBlocklist reads hosts files, adblock-style lists (||domain^) and plain
// domain lists into squid dstdomain entries (".example.com"). Entries already
// covered by a parent in the list are dropped: squid warns about them, and they
// only slow matching down. skipped counts lines that were not domains.
func ParseBlocklist(r io.Reader) (domains []string, skipped int, err error) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	for sc.Scan() {
		d, ok := blocklistDomain(sc.Text())
		if !ok {
			if t := strings.TrimSpace(sc.Text()); t != "" && !isListComment(t) {
				skipped++
			}
			continue
		}
		seen[d] = true
		if len(seen) > maxBlocklistEntries {
			return nil, 0, fmt.Errorf("the list has more than %d domains", maxBlocklistEntries)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}

	all := make([]string, 0, len(seen))
	for d := range seen {
		all = append(all, d)
	}
	sort.Slice(all, func(i, j int) bool {
		li, lj := strings.Count(all[i], "."), strings.Count(all[j], ".")
		if li != lj {
			return li < lj // parents first
		}
		return all[i] < all[j]
	})

	kept := make(map[string]bool, len(all))
	for _, d := range all {
		if !coveredByParent(d, kept) {
			kept[d] = true
			domains = append(domains, d)
		}
	}
	sort.Strings(domains)
	return domains, skipped, nil
}

func isListComment(t string) bool {
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "//")
}

// coveredByParent reports whether ".a.b.example.com" is already matched by
// ".b.example.com", ".example.com" (any shorter dotted suffix) in kept.
func coveredByParent(d string, kept map[string]bool) bool {
	rest := d[1:]
	for {
		i := strings.Index(rest, ".")
		if i == -1 {
			return false
		}
		rest = rest[i+1:]
		if kept["."+rest] {
			return true
		}
	}
}

// blocklistDomain extracts one domain from a line of any supported format.
func blocklistDomain(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || isListComment(t) {
		return "", false
	}
	if i := strings.Index(t, " #"); i != -1 {
		t = strings.TrimSpace(t[:i])
	}

	var cand string
	fields := strings.Fields(t)
	switch {
	case strings.HasPrefix(t, "||"):
		// ||example.com^ (options after $ are not supported: skip those lines)
		body := strings.TrimPrefix(t, "||")
		if !strings.HasSuffix(body, "^") || strings.ContainsAny(strings.TrimSuffix(body, "^"), "$/*|") {
			return "", false
		}
		cand = strings.TrimSuffix(body, "^")
	case len(fields) >= 2 && net.ParseIP(fields[0]) != nil:
		cand = fields[1] // hosts file: "0.0.0.0 ads.example.com"
	case len(fields) == 1:
		cand = fields[0]
	default:
		return "", false
	}

	cand = strings.ToLower(strings.Trim(strings.TrimPrefix(cand, "*"), "."))
	if cand == "" || hostsPlaceholders[cand] || !strings.Contains(cand, ".") ||
		net.ParseIP(cand) != nil || validHost(cand) != nil {
		return "", false
	}
	return "." + cand, true
}
