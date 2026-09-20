package squid

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Squid settings that the panel edits through forms instead of a text editor.
//
// squid.conf itself is the source of truth: the values the panel manages live
// in one marked block, and the stock line for the same directive (if any) is
// commented out with a reversible marker. Because nothing lives in a database,
// restoring an old config version, or hand-editing the file, can never leave
// the panel showing something different from what squid actually runs.

const (
	settingsBlockStart = "# --- squidadmin: settings (managed, do not edit by hand) ---"
	settingsBlockEnd   = "# --- end squidadmin: settings ---"

	// disabledPrefix marks a stock line the panel has taken over. Removing the
	// prefix restores the line byte for byte.
	disabledPrefix = "# squidadmin-disabled: "
)

type FieldKind string

const (
	KindText   FieldKind = "text"
	KindSelect FieldKind = "select"
	KindList   FieldKind = "list"
	KindToggle FieldKind = "toggle" // on = ["allow all"], off = no value
)

// Setting describes one editable squid directive. Labels and help texts are
// deliberately not here: they belong to the UI (and its language).
type Setting struct {
	Key         string    `json:"key"` // also the squid directive name
	Group       string    `json:"group"`
	Kind        FieldKind `json:"kind"`
	Options     []string  `json:"options,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	// Default is what squid uses when the directive is absent.
	Default  string `json:"default"`
	Required bool   `json:"required"`
	// Multi means the directive may appear more than once (or hold several
	// values); every value is one entry of Values.
	Multi bool `json:"multi"`
	// Restart means a reload is not enough: squid must be restarted.
	Restart bool `json:"restart"`

	// join renders all values on one directive line (dns_nameservers a b c).
	join bool
	// normalize validates one value and returns its canonical spelling.
	normalize func(string) (string, error)
	// check validates the whole value list (duplicates, limits).
	check func([]string) error
}

// SettingGroups lists the group ids in display order; the UI supplies the labels.
var SettingGroups = []string{"network", "cache", "timeouts", "logs", "upstream"}

var (
	hostLabelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	sizeRe      = regexp.MustCompile(`(?i)^(\d{1,9})\s*(KB|MB|GB)$`)
	durationRe  = regexp.MustCompile(`(?i)^(\d{1,7})\s*(seconds?|minutes?|hours?|days?)$`)
	nameRe      = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	cachePathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)
)

func validHost(s string) error {
	if net.ParseIP(s) != nil {
		return nil
	}
	if len(s) == 0 || len(s) > 253 {
		return fmt.Errorf("invalid host name %q", s)
	}
	for _, label := range strings.Split(s, ".") {
		if !hostLabelRe.MatchString(label) {
			return fmt.Errorf("invalid host name %q", s)
		}
	}
	return nil
}

func validPort(s string, min int) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > 65535 {
		return 0, fmt.Errorf("invalid port %q (allowed %d-65535)", s, min)
	}
	return n, nil
}

// http_port  [address:]port [intercept|tproxy] [name=X]
func normalizeHTTPPort(line string) (string, error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "", fmt.Errorf("empty value")
	}

	addr, portStr := "", f[0]
	switch {
	case strings.HasPrefix(f[0], "["):
		end := strings.Index(f[0], "]:")
		if end == -1 {
			return "", fmt.Errorf("invalid address %q (use [ipv6]:port)", f[0])
		}
		addr, portStr = f[0][1:end], f[0][end+2:]
		if net.ParseIP(addr) == nil {
			return "", fmt.Errorf("invalid IPv6 address %q", addr)
		}
		addr = "[" + addr + "]"
	case strings.Count(f[0], ":") > 1:
		return "", fmt.Errorf("IPv6 addresses must be written in brackets, e.g. [::1]:3128")
	case strings.Contains(f[0], ":"):
		i := strings.LastIndex(f[0], ":")
		addr, portStr = f[0][:i], f[0][i+1:]
		if err := validHost(addr); err != nil {
			return "", err
		}
	}
	if _, err := validPort(portStr, 1); err != nil {
		return "", err
	}

	seen := map[string]bool{}
	out := []string{f[0]}
	for _, opt := range f[1:] {
		switch {
		case opt == "intercept" || opt == "tproxy":
			if seen["mode"] {
				return "", fmt.Errorf("intercept and tproxy cannot be combined")
			}
			seen["mode"] = true
		case strings.HasPrefix(opt, "name="):
			if !nameRe.MatchString(strings.TrimPrefix(opt, "name=")) {
				return "", fmt.Errorf("invalid name option %q", opt)
			}
		default:
			return "", fmt.Errorf("unsupported option %q (allowed: intercept, tproxy, name=...)", opt)
		}
		out = append(out, opt)
	}
	return strings.Join(out, " "), nil
}

func checkHTTPPorts(lines []string) error {
	seen := map[string]bool{}
	for _, l := range lines {
		spec := strings.Fields(l)[0]
		if seen[spec] {
			return fmt.Errorf("%s is listed twice", spec)
		}
		seen[spec] = true
	}
	return nil
}

func normalizeHostname(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if net.ParseIP(s) != nil {
		return "", fmt.Errorf("must be a host name, not an IP address")
	}
	return s, validHost(s)
}

func normalizeIP(s string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return "", fmt.Errorf("%q is not an IP address", s)
	}
	return ip.String(), nil
}

// sizeKB converts "256 MB" style values to kilobytes for range checks.
func sizeKB(v string) int64 {
	m := sizeRe.FindStringSubmatch(v)
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch strings.ToUpper(m[2]) {
	case "MB":
		return n * 1024
	case "GB":
		return n * 1024 * 1024
	}
	return n
}

func normalizeSize(minKB, maxKB int64) func(string) (string, error) {
	return func(s string) (string, error) {
		m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
		if m == nil {
			return "", fmt.Errorf("use a number and a unit, e.g. 256 MB (KB, MB or GB)")
		}
		out := m[1] + " " + strings.ToUpper(m[2])
		if kb := sizeKB(out); kb < minKB || kb > maxKB {
			return "", fmt.Errorf("must be between %s and %s", humanKB(minKB), humanKB(maxKB))
		}
		return out, nil
	}
}

// normalizeCount accepts a plain whole number within [min, max].
func normalizeCount(min, max int) func(string) (string, error) {
	return func(s string) (string, error) {
		s = strings.TrimSpace(s)
		n, err := strconv.Atoi(s)
		if err != nil || strconv.Itoa(n) != s || n < min || n > max {
			return "", fmt.Errorf("must be a whole number between %d and %d", min, max)
		}
		return s, nil
	}
}

func humanKB(kb int64) string {
	switch {
	case kb >= 1024*1024 && kb%(1024*1024) == 0:
		return fmt.Sprintf("%d GB", kb/(1024*1024))
	case kb >= 1024 && kb%1024 == 0:
		return fmt.Sprintf("%d MB", kb/1024)
	}
	return fmt.Sprintf("%d KB", kb)
}

func durationSeconds(v string) int64 {
	m := durationRe.FindStringSubmatch(v)
	n, _ := strconv.ParseInt(m[1], 10, 64)
	switch strings.ToLower(m[2])[0] {
	case 'm':
		return n * 60
	case 'h':
		return n * 3600
	case 'd':
		return n * 86400
	}
	return n
}

func normalizeDuration(minSec, maxSec int64) func(string) (string, error) {
	return func(s string) (string, error) {
		m := durationRe.FindStringSubmatch(strings.TrimSpace(s))
		if m == nil {
			return "", fmt.Errorf("use a number and a unit, e.g. 30 seconds (seconds, minutes, hours or days)")
		}
		unit := strings.ToLower(m[2])
		unit = strings.TrimSuffix(unit, "s")
		if n, _ := strconv.Atoi(m[1]); n != 1 {
			unit += "s"
		}
		out := m[1] + " " + unit
		if sec := durationSeconds(out); sec < minSec || sec > maxSec {
			return "", fmt.Errorf("must be between %s and %s", humanSec(minSec), humanSec(maxSec))
		}
		return out, nil
	}
}

func humanSec(sec int64) string {
	switch {
	case sec >= 86400 && sec%86400 == 0:
		return fmt.Sprintf("%d days", sec/86400)
	case sec >= 3600 && sec%3600 == 0:
		return fmt.Sprintf("%d hours", sec/3600)
	case sec >= 60 && sec%60 == 0:
		return fmt.Sprintf("%d minutes", sec/60)
	}
	return fmt.Sprintf("%d seconds", sec)
}

// cache_dir  ufs|aufs|diskd  /path  size-MB  L1  L2
func normalizeCacheDir(line string) (string, error) {
	f := strings.Fields(line)
	if len(f) != 5 {
		return "", fmt.Errorf("expected: type path size-in-MB L1 L2, e.g. ufs /var/spool/squid 1000 16 256")
	}
	switch f[0] {
	case "ufs", "aufs", "diskd":
	default:
		return "", fmt.Errorf("unsupported store type %q (ufs, aufs or diskd)", f[0])
	}
	p := f[1]
	if !cachePathRe.MatchString(p) || p == "/" || strings.Contains(p, "..") || strings.HasSuffix(p, "/") {
		return "", fmt.Errorf("invalid directory %q: must be an absolute path without .. or a trailing /", p)
	}
	for i, spec := range []struct {
		name     string
		min, max int
	}{{"size", 8, 10_000_000}, {"L1", 1, 256}, {"L2", 1, 256}} {
		n, err := strconv.Atoi(f[2+i])
		if err != nil || n < spec.min || n > spec.max {
			return "", fmt.Errorf("%s must be a number between %d and %d", spec.name, spec.min, spec.max)
		}
	}
	return strings.Join(f, " "), nil
}

// cache_peer  host  parent|sibling  http-port  icp-port  [options]
var peerOptions = map[string]bool{
	"no-query": true, "default": true, "no-digest": true, "proxy-only": true,
	"round-robin": true, "no-netdb-exchange": true,
}

var peerNumericOptions = map[string]bool{"weight": true, "connect-timeout": true, "connect-fail-limit": true}

func normalizeCachePeer(line string) (string, error) {
	f := strings.Fields(line)
	if len(f) < 4 {
		return "", fmt.Errorf("expected: host parent|sibling http-port icp-port [options]")
	}
	if err := validHost(f[0]); err != nil {
		return "", err
	}
	if f[1] != "parent" && f[1] != "sibling" {
		return "", fmt.Errorf("type must be parent or sibling, got %q", f[1])
	}
	if _, err := validPort(f[2], 1); err != nil {
		return "", err
	}
	if _, err := validPort(f[3], 0); err != nil {
		return "", err
	}
	// login=/password style options would put credentials into squid.conf, and
	// the panel exposes that file to every admin: not supported here.
	for _, opt := range f[4:] {
		name, val, hasVal := strings.Cut(opt, "=")
		switch {
		case !hasVal && peerOptions[opt]:
		case hasVal && peerNumericOptions[name]:
			if n, err := strconv.Atoi(val); err != nil || n < 0 || n > 100000 {
				return "", fmt.Errorf("invalid value in %q", opt)
			}
		case hasVal && name == "name" && nameRe.MatchString(val):
		default:
			return "", fmt.Errorf("unsupported option %q", opt)
		}
	}
	return strings.Join(f, " "), nil
}

func normalizeAllowAll(s string) (string, error) {
	if strings.Join(strings.Fields(s), " ") != "allow all" {
		return "", fmt.Errorf(`must be "allow all"`)
	}
	return "allow all", nil
}

func enumNormalizer(options ...string) func(string) (string, error) {
	return func(s string) (string, error) {
		s = strings.ToLower(strings.TrimSpace(s))
		for _, o := range options {
			if s == o {
				return s, nil
			}
		}
		return "", fmt.Errorf("must be one of: %s", strings.Join(options, ", "))
	}
}

// Schema lists every setting the panel can manage, in display order.
var Schema = []Setting{
	{Key: "http_port", Group: "network", Kind: KindList, Multi: true, Required: true, Default: "3128",
		Placeholder: "3128   |   0.0.0.0:3129   |   3130 intercept",
		normalize:   normalizeHTTPPort, check: checkHTTPPorts},
	{Key: "visible_hostname", Group: "network", Kind: KindText, Default: "auto",
		Placeholder: "proxy.example.com", normalize: normalizeHostname},
	{Key: "dns_nameservers", Group: "network", Kind: KindList, Multi: true, join: true, Default: "system",
		Placeholder: "1.1.1.1", normalize: normalizeIP,
		check: func(l []string) error {
			if len(l) > 8 {
				return fmt.Errorf("at most 8 name servers")
			}
			return nil
		}},
	{Key: "forwarded_for", Group: "network", Kind: KindSelect, Default: "on",
		Options:   []string{"on", "off", "transparent", "delete", "truncate"},
		normalize: enumNormalizer("on", "off", "transparent", "delete", "truncate")},
	{Key: "via", Group: "network", Kind: KindSelect, Default: "on",
		Options: []string{"on", "off"}, normalize: enumNormalizer("on", "off")},

	{Key: "cache_mem", Group: "cache", Kind: KindText, Default: "256 MB", Placeholder: "256 MB",
		normalize: normalizeSize(0, 1024*1024*1024)},
	{Key: "maximum_object_size", Group: "cache", Kind: KindText, Default: "4 MB", Placeholder: "4 MB",
		normalize: normalizeSize(1, 1024*1024*1024)},
	{Key: "cache_dir", Group: "cache", Kind: KindText, Restart: true, Default: "none (memory only)",
		Placeholder: "ufs /var/spool/squid 1000 16 256", normalize: normalizeCacheDir},

	{Key: "connect_timeout", Group: "timeouts", Kind: KindText, Default: "1 minute", Placeholder: "1 minute",
		normalize: normalizeDuration(1, 3600)},
	{Key: "read_timeout", Group: "timeouts", Kind: KindText, Default: "15 minutes", Placeholder: "15 minutes",
		normalize: normalizeDuration(1, 86400)},
	{Key: "request_timeout", Group: "timeouts", Kind: KindText, Default: "5 minutes", Placeholder: "5 minutes",
		normalize: normalizeDuration(1, 3600)},
	{Key: "shutdown_lifetime", Group: "timeouts", Kind: KindText, Default: "30 seconds", Placeholder: "30 seconds",
		normalize: normalizeDuration(0, 3600)},

	{Key: "logfile_rotate", Group: "logs", Kind: KindText, Default: "0", Placeholder: "10",
		normalize: normalizeCount(0, 365)},

	{Key: "cache_peer", Group: "upstream", Kind: KindList, Multi: true, Default: "none",
		Placeholder: "proxy.isp.example parent 3128 0 no-query default", normalize: normalizeCachePeer},
	{Key: "never_direct", Group: "upstream", Kind: KindToggle, Default: "off", normalize: normalizeAllowAll},
}

func schemaByKey(key string) (Setting, bool) {
	for _, s := range Schema {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingValue is a setting together with what is configured right now.
type SettingValue struct {
	Setting
	Values []string `json:"values"`
	// Source is "panel" (managed block), "squid.conf" (a stock line) or
	// "default" (nothing configured).
	Source string `json:"source"`
}

// SettingsError carries one message per invalid field.
type SettingsError struct {
	Fields map[string]string `json:"fields"`
}

func (e *SettingsError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, s := range Schema { // stable order
		if msg, ok := e.Fields[s.Key]; ok {
			parts = append(parts, s.Key+": "+msg)
		}
	}
	return "invalid settings: " + strings.Join(parts, "; ")
}

// directiveOf returns the directive name of an active (non-comment) config
// line, or "".
func directiveOf(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	name, _, _ := strings.Cut(t, " ")
	name, _, _ = strings.Cut(name, "\t")
	return name
}

func argsOf(line string) string {
	t := strings.TrimSpace(line)
	i := strings.IndexAny(t, " \t")
	if i == -1 {
		return ""
	}
	return strings.TrimSpace(t[i+1:])
}

// found holds where a setting's values currently come from.
type found struct {
	managed []string
	stock   []string
}

// scanSettings walks the config once and records every schema directive, split
// into panel-managed (inside the settings block) and stock (everywhere else).
func scanSettings(lines []string) map[string]*found {
	res := map[string]*found{}
	inBlock := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case settingsBlockStart:
			inBlock = true
			continue
		case settingsBlockEnd:
			inBlock = false
			continue
		}

		key := directiveOf(line)
		s, ok := schemaByKey(key)
		if !ok {
			continue
		}
		vals := []string{argsOf(line)}
		if s.join {
			vals = strings.Fields(argsOf(line))
		}
		f := res[key]
		if f == nil {
			f = &found{}
			res[key] = f
		}
		if inBlock {
			f.managed = append(f.managed, vals...)
		} else {
			f.stock = append(f.stock, vals...)
		}
	}
	return res
}

// effectiveSettings reports every setting's current values and where they come from.
func effectiveSettings(content string) []SettingValue {
	scanned := scanSettings(strings.Split(content, "\n"))
	out := make([]SettingValue, 0, len(Schema))
	for _, s := range Schema {
		sv := SettingValue{Setting: s, Values: []string{}, Source: "default"}
		if f := scanned[s.Key]; f != nil {
			switch {
			case len(f.managed) > 0:
				sv.Values, sv.Source = f.managed, "panel"
			case len(f.stock) > 0:
				sv.Values, sv.Source = f.stock, "squid.conf"
			}
		}
		out = append(out, sv)
	}
	return out
}

// SettingsUpdate is a partial update: Values sets (or, with an empty list,
// removes) a setting; Reset hands a setting back to whatever the stock
// squid.conf had.
type SettingsUpdate struct {
	Values map[string][]string `json:"values"`
	Reset  []string            `json:"reset"`
}

// SettingsResult describes what an update did (or, for a dry run, would do).
type SettingsResult struct {
	Changed         []string        `json:"changed"`
	RestartRequired bool            `json:"restart_required"`
	Changes         []SettingChange `json:"changes"`
}

// SettingChange is one setting's value before and after an update.
type SettingChange struct {
	Key     string   `json:"key"`
	From    []string `json:"from"`
	To      []string `json:"to"`
	Restart bool     `json:"restart"`
}

func sameValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validateUpdate normalises the requested values and reports every problem at
// once, so the form can flag all bad fields in a single round trip.
func validateUpdate(u SettingsUpdate) (map[string][]string, error) {
	errs := map[string]string{}
	clean := map[string][]string{}

	for key, raw := range u.Values {
		s, ok := schemaByKey(key)
		if !ok {
			errs[key] = "unknown setting"
			continue
		}
		var vals []string
		for _, r := range raw {
			if strings.TrimSpace(r) == "" {
				continue
			}
			n, err := s.normalize(r)
			if err != nil {
				errs[key] = err.Error()
				break
			}
			vals = append(vals, n)
		}
		if errs[key] != "" {
			continue
		}
		if len(vals) == 0 && s.Required {
			errs[key] = "at least one value is required"
			continue
		}
		if len(vals) > 1 && !s.Multi {
			errs[key] = "only one value is allowed"
			continue
		}
		if s.check != nil && len(vals) > 0 {
			if err := s.check(vals); err != nil {
				errs[key] = err.Error()
				continue
			}
		}
		clean[key] = vals
	}
	for _, key := range u.Reset {
		if _, ok := schemaByKey(key); !ok {
			errs[key] = "unknown setting"
		}
		if _, both := u.Values[key]; both {
			errs[key] = "cannot both set and reset"
		}
	}

	if len(errs) > 0 {
		return nil, &SettingsError{Fields: errs}
	}
	return clean, nil
}

// applySettings computes the new squid.conf for an update. It is a pure
// function of the current text, which keeps it easy to test exhaustively.
func applySettings(content string, u SettingsUpdate) (string, SettingsResult, error) {
	clean, err := validateUpdate(u)
	if err != nil {
		return "", SettingsResult{}, err
	}

	lines := strings.Split(content, "\n")
	before := effectiveSettings(content)
	beforeBy := map[string]SettingValue{}
	for _, sv := range before {
		beforeBy[sv.Key] = sv
	}
	scanned := scanSettings(lines)

	// What the panel manages after this update, starting from what it already
	// manages.
	managed := map[string][]string{}
	for key, f := range scanned {
		if len(f.managed) > 0 {
			managed[key] = f.managed
		}
	}
	disable := map[string]bool{} // active stock lines to comment out
	restore := map[string]bool{} // commented-out stock lines to bring back

	for key, vals := range clean {
		if sameValues(beforeBy[key].Values, vals) {
			continue // already so; don't take a line over for no reason
		}
		disable[key] = true
		if len(vals) == 0 {
			delete(managed, key) // removed from the configuration altogether
		} else {
			managed[key] = vals
		}
	}
	for _, key := range u.Reset {
		delete(managed, key)
		delete(disable, key)
		restore[key] = true
	}

	// Rebuild the file.
	lines = stripManagedBlock(lines, settingsBlockStart, settingsBlockEnd)
	out := make([]string, 0, len(lines)+8)
	for _, line := range lines {
		if key := directiveOf(line); key != "" {
			if _, known := schemaByKey(key); known {
				if _, isManaged := managed[key]; isManaged || disable[key] {
					out = append(out, disabledPrefix+line)
					continue
				}
			}
		}
		if key := keyOfDisabled(line); key != "" && restore[key] {
			out = append(out, strings.TrimPrefix(line, disabledPrefix))
			continue
		}
		out = append(out, line)
	}

	if len(managed) > 0 {
		block := []string{settingsBlockStart}
		for _, s := range Schema {
			vals, ok := managed[s.Key]
			if !ok {
				continue
			}
			if s.join {
				block = append(block, s.Key+" "+strings.Join(vals, " "))
				continue
			}
			for _, v := range vals {
				block = append(block, s.Key+" "+v)
			}
		}
		block = append(block, settingsBlockEnd)

		// Append at the end, ahead of the file's final newline, so removing
		// the block later gives back the original text exactly.
		at := len(out)
		if at > 0 && out[at-1] == "" {
			at--
		}
		out = insertAt(out, at, block)
	}
	newContent := strings.Join(out, "\n")

	// Judge the result by re-reading it: what squid will actually see, not
	// what this function meant to write.
	after := effectiveSettings(newContent)
	afterBy := map[string]SettingValue{}
	for _, sv := range after {
		afterBy[sv.Key] = sv
	}

	// Cross-setting rule: never_direct without any upstream proxy would make
	// every request fail, yet squid would parse and start without complaint.
	if len(afterBy["never_direct"].Values) > 0 && len(afterBy["cache_peer"].Values) == 0 {
		return "", SettingsResult{}, &SettingsError{Fields: map[string]string{
			"never_direct": "needs at least one cache_peer: otherwise no request could leave the proxy",
		}}
	}

	changed := []string{}
	changes := []SettingChange{}
	res := SettingsResult{}
	for _, st := range Schema {
		from, to := beforeBy[st.Key].Values, afterBy[st.Key].Values
		if sameValues(from, to) {
			continue
		}
		changed = append(changed, st.Key)
		changes = append(changes, SettingChange{Key: st.Key, From: nonNil(from), To: nonNil(to), Restart: st.Restart})
		if st.Restart {
			res.RestartRequired = true
		}
	}
	res.Changed, res.Changes = changed, changes
	return newContent, res, nil
}

// keyOfDisabled returns the directive of a line the panel commented out, or "".
func keyOfDisabled(line string) string {
	if !strings.HasPrefix(line, disabledPrefix) {
		return ""
	}
	key := directiveOf(strings.TrimPrefix(line, disabledPrefix))
	if _, ok := schemaByKey(key); !ok {
		return ""
	}
	return key
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
