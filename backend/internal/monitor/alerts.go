package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"squidadmin/backend/internal/netguard"
)

// Conditions the panel watches. Each is either "firing" or not; a notification
// goes out when it starts firing, again as a reminder while it keeps firing,
// and when it stops.
const (
	CondSquidDown   = "squid_down"
	CondDiskFull    = "disk_full"
	CondDeniedSpike = "denied_spike"
	CondErrorSpike  = "error_spike"
)

var conditionOrder = []string{CondSquidDown, CondDiskFull, CondDeniedSpike, CondErrorSpike}

// Config is the whole alert configuration. The same shape is used when saving;
// secrets (token, URL, password) left empty on save keep their stored value.
type Config struct {
	SquidDown   Toggle `json:"squid_down"`
	DiskFull    Disk   `json:"disk_full"`
	DeniedSpike Spike  `json:"denied_spike"`
	ErrorSpike  Spike  `json:"error_spike"`

	Telegram TelegramConfig `json:"telegram"`
	Webhook  WebhookConfig  `json:"webhook"`
	Email    EmailConfig    `json:"email"`
}

type Toggle struct {
	Enabled bool `json:"enabled"`
}

type Disk struct {
	Enabled bool `json:"enabled"`
	Percent int  `json:"percent"`
}

type Spike struct {
	Enabled       bool `json:"enabled"`
	Threshold     int  `json:"threshold"`
	WindowMinutes int  `json:"window_minutes"`
}

type TelegramConfig struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
	ChatID  string `json:"chat_id"`
}

type WebhookConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

type EmailConfig struct {
	Enabled  bool     `json:"enabled"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

func DefaultConfig() Config {
	return Config{
		SquidDown:   Toggle{Enabled: true},
		DiskFull:    Disk{Enabled: true, Percent: 90},
		DeniedSpike: Spike{Enabled: false, Threshold: 500, WindowMinutes: 10},
		ErrorSpike:  Spike{Enabled: true, Threshold: 100, WindowMinutes: 5},
		Email:       EmailConfig{Port: 587, To: []string{}},
	}
}

// ConfigView is Config as shown to clients: secrets are replaced by flags.
type ConfigView struct {
	SquidDown   Toggle `json:"squid_down"`
	DiskFull    Disk   `json:"disk_full"`
	DeniedSpike Spike  `json:"denied_spike"`
	ErrorSpike  Spike  `json:"error_spike"`

	Telegram struct {
		Enabled  bool   `json:"enabled"`
		TokenSet bool   `json:"token_set"`
		ChatID   string `json:"chat_id"`
	} `json:"telegram"`
	Webhook struct {
		Enabled bool   `json:"enabled"`
		URLSet  bool   `json:"url_set"`
		Host    string `json:"host"` // where it points, without the (secret) path
	} `json:"webhook"`
	Email struct {
		Enabled     bool     `json:"enabled"`
		Host        string   `json:"host"`
		Port        int      `json:"port"`
		Username    string   `json:"username"`
		PasswordSet bool     `json:"password_set"`
		From        string   `json:"from"`
		To          []string `json:"to"`
	} `json:"email"`
}

func View(c Config) ConfigView {
	var v ConfigView
	v.SquidDown, v.DiskFull, v.DeniedSpike, v.ErrorSpike = c.SquidDown, c.DiskFull, c.DeniedSpike, c.ErrorSpike
	v.Telegram.Enabled, v.Telegram.TokenSet, v.Telegram.ChatID = c.Telegram.Enabled, c.Telegram.Token != "", c.Telegram.ChatID
	v.Webhook.Enabled, v.Webhook.URLSet = c.Webhook.Enabled, c.Webhook.URL != ""
	if u, err := url.Parse(c.Webhook.URL); err == nil {
		v.Webhook.Host = u.Host
	}
	e := c.Email
	v.Email.Enabled, v.Email.Host, v.Email.Port, v.Email.Username = e.Enabled, e.Host, e.Port, e.Username
	v.Email.PasswordSet, v.Email.From = e.Password != "", e.From
	v.Email.To = e.To
	if v.Email.To == nil {
		v.Email.To = []string{}
	}
	return v
}

// mergeSecrets keeps the stored secrets when the update leaves them empty.
func mergeSecrets(old, in Config) Config {
	if in.Telegram.Token == "" {
		in.Telegram.Token = old.Telegram.Token
	}
	if in.Webhook.URL == "" {
		in.Webhook.URL = old.Webhook.URL
	}
	if in.Email.Password == "" && in.Email.Username == old.Email.Username {
		in.Email.Password = old.Email.Password
	}
	return in
}

// ConfigError carries one message per invalid field.
type ConfigError struct{ Fields map[string]string }

func (e *ConfigError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "invalid alert settings: " + strings.Join(parts, "; ")
}

var (
	tokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{20,80}$`)
	chatRe  = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z0-9_]{4,64})$`)
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// Validate checks a complete configuration.
func Validate(c Config) error {
	f := map[string]string{}

	if c.DiskFull.Percent < 50 || c.DiskFull.Percent > 99 {
		f["disk_full.percent"] = "must be between 50 and 99"
	}
	for name, s := range map[string]Spike{"denied_spike": c.DeniedSpike, "error_spike": c.ErrorSpike} {
		if s.Threshold < 1 || s.Threshold > 1_000_000 {
			f[name+".threshold"] = "must be between 1 and 1000000"
		}
		if s.WindowMinutes < 1 || s.WindowMinutes > 120 {
			f[name+".window_minutes"] = "must be between 1 and 120"
		}
	}

	if c.Telegram.Enabled {
		if !tokenRe.MatchString(c.Telegram.Token) {
			f["telegram.token"] = "not a valid bot token (looks like 123456789:AAE...)"
		}
		if !chatRe.MatchString(c.Telegram.ChatID) {
			f["telegram.chat_id"] = "must be a numeric chat id (e.g. -1001234567890) or @channelname"
		}
	}
	if c.Webhook.Enabled {
		u, err := url.Parse(c.Webhook.URL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || len(c.Webhook.URL) > 500:
			f["webhook.url"] = "must be a valid http:// or https:// address"
		case u.User != nil:
			f["webhook.url"] = "credentials in the URL are not supported"
		}
	}
	if e := c.Email; e.Enabled {
		if !hostRe.MatchString(e.Host) && net.ParseIP(e.Host) == nil {
			f["email.host"] = "not a valid host name"
		}
		if e.Port < 1 || e.Port > 65535 {
			f["email.port"] = "must be between 1 and 65535"
		}
		if _, err := mail.ParseAddress(e.From); err != nil || strings.ContainsAny(e.From, "\r\n") {
			f["email.from"] = "not a valid address"
		}
		if len(e.To) < 1 || len(e.To) > 10 {
			f["email.to"] = "between 1 and 10 recipients"
		}
		for _, to := range e.To {
			if _, err := mail.ParseAddress(to); err != nil || strings.ContainsAny(to, "\r\n") {
				f["email.to"] = fmt.Sprintf("%q is not a valid address", to)
			}
		}
		if len(e.Username) > 100 || strings.ContainsAny(e.Username, "\r\n") || len(e.Password) > 200 {
			f["email.username"] = "too long or contains line breaks"
		}
	}

	if len(f) > 0 {
		return &ConfigError{Fields: f}
	}
	return nil
}

// ------------------------------------------------------------------ service

// Probe supplies the live facts the conditions are judged on.
type Probe interface {
	SquidRunning() bool
	// Disk returns the fullest of the monitored filesystems.
	Disk() (path string, percent float64, err error)
	Recent(window time.Duration) (requests, denied, errors int)
}

type Alerts struct {
	db    *sql.DB
	probe Probe
	now   func() time.Time

	client        *http.Client
	allowLoopback bool
	telegramBase  string
	hostname      string
	// reminder is how often a still-firing condition is repeated.
	reminder time.Duration
	// build returns the channels for a configuration (replaced in tests).
	build func(Config) []Notifier

	mu         sync.Mutex
	downStreak int
}

func NewAlerts(db *sql.DB, probe Probe, allowLoopback bool) *Alerts {
	host, _ := os.Hostname()
	a := &Alerts{
		db: db, probe: probe, now: time.Now, hostname: host,
		client:        netguard.NewClient(allowLoopback, 20*time.Second),
		allowLoopback: allowLoopback,
		reminder:      6 * time.Hour,
	}
	a.build = a.defaultBuild
	return a
}

func (a *Alerts) defaultBuild(c Config) []Notifier {
	var out []Notifier
	if c.Telegram.Enabled {
		out = append(out, &Telegram{Token: c.Telegram.Token, ChatID: c.Telegram.ChatID, Base: a.telegramBase, Client: a.client})
	}
	if c.Webhook.Enabled {
		out = append(out, &Webhook{URL: c.Webhook.URL, Client: a.client})
	}
	if e := c.Email; e.Enabled {
		out = append(out, &Email{Host: e.Host, Port: e.Port, Username: e.Username, Password: e.Password, From: e.From, To: e.To, AllowLoopback: a.allowLoopback})
	}
	return out
}

// Config loads the stored configuration (defaults when nothing was saved).
func (a *Alerts) Config() (Config, error) {
	var raw string
	err := a.db.QueryRow(`SELECT config FROM alert_config WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	c := DefaultConfig()
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return Config{}, fmt.Errorf("stored alert settings are unreadable: %w", err)
	}
	return c, nil
}

// SetConfig validates and stores a configuration, returning what was saved.
func (a *Alerts) SetConfig(in Config) (Config, error) {
	old, err := a.Config()
	if err != nil {
		old = DefaultConfig()
	}
	c := mergeSecrets(old, in)
	if c.Email.To == nil {
		c.Email.To = []string{}
	}
	if err := Validate(c); err != nil {
		return Config{}, err
	}
	raw, _ := json.Marshal(c)
	_, err = a.db.Exec(`INSERT INTO alert_config (id, config) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET config = excluded.config`, string(raw))
	return c, err
}

type check struct {
	key     string
	firing  bool
	subject string
	detail  string
}

func (a *Alerts) evaluate(c Config) []check {
	var out []check

	if c.SquidDown.Enabled {
		if a.probe.SquidRunning() {
			a.downStreak = 0
		} else {
			a.downStreak++
		}
		// Two consecutive failed checks: one bad poll is not an outage.
		out = append(out, check{CondSquidDown, a.downStreak >= 2, "🔴 Squid ishlamayapti",
			fmt.Sprintf("Squid xizmati javob bermayapti (%d ketma-ket tekshiruv).", a.downStreak)})
	} else {
		a.downStreak = 0
	}

	if c.DiskFull.Enabled {
		if path, pct, err := a.probe.Disk(); err == nil {
			out = append(out, check{CondDiskFull, pct >= float64(c.DiskFull.Percent), "🟠 Disk to'lib bormoqda",
				fmt.Sprintf("%s: %.0f%% band (chegara %d%%).", path, pct, c.DiskFull.Percent)})
		}
	}

	for _, s := range []struct {
		key, subject, what string
		cfg                Spike
		pick               func(req, denied, errs int) int
	}{
		{CondDeniedSpike, "🟠 Bloklangan so'rovlar ko'paydi", "bloklangan so'rov", c.DeniedSpike, func(_, d, _ int) int { return d }},
		{CondErrorSpike, "🟠 Server xatolari ko'paydi", "5xx xato", c.ErrorSpike, func(_, _, e int) int { return e }},
	} {
		if !s.cfg.Enabled {
			continue
		}
		req, denied, errs := a.probe.Recent(time.Duration(s.cfg.WindowMinutes) * time.Minute)
		n := s.pick(req, denied, errs)
		out = append(out, check{s.key, n >= s.cfg.Threshold, s.subject,
			fmt.Sprintf("Oxirgi %d daqiqada %d ta %s (chegara %d).", s.cfg.WindowMinutes, n, s.what, s.cfg.Threshold)})
	}
	return out
}

// Event is one entry of the alert history.
type Event struct {
	ID       int64  `json:"id"`
	Time     int64  `json:"time"`
	Key      string `json:"key"`
	Kind     string `json:"kind"` // firing, reminder, resolved, test
	Message  string `json:"message"`
	Delivery string `json:"delivery"`
}

// deliver sends m to every enabled channel and reports the outcome per channel.
func (a *Alerts) deliver(c Config, m Message) string {
	notifiers := a.build(c)
	if len(notifiers) == 0 {
		return "kanal sozlanmagan"
	}
	var results []string
	for _, n := range notifiers {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		err := n.Send(ctx, m)
		cancel()
		if err != nil {
			results = append(results, fmt.Sprintf("%s: xato: %v", n.Name(), err))
		} else {
			results = append(results, n.Name()+": yuborildi")
		}
	}
	return strings.Join(results, "; ")
}

func (a *Alerts) record(key, kind, message, delivery string) Event {
	now := a.now().Unix()
	res, err := a.db.Exec(`INSERT INTO alert_events (ts, key, kind, message, delivery) VALUES (?, ?, ?, ?, ?)`, now, key, kind, message, delivery)
	if err != nil {
		log.Printf("alerts: could not record event: %v", err)
	}
	id, _ := res.LastInsertId()
	// Keep the history bounded.
	a.db.Exec(`DELETE FROM alert_events WHERE id NOT IN (SELECT id FROM alert_events ORDER BY id DESC LIMIT 500)`)
	return Event{ID: id, Time: now, Key: key, Kind: kind, Message: message, Delivery: delivery}
}

type state struct {
	firing       bool
	since        int64
	lastNotified int64
	detail       string
}

func (a *Alerts) loadState(key string) state {
	var s state
	var firing int
	err := a.db.QueryRow(`SELECT firing, since, last_notified, detail FROM alert_state WHERE key = ?`, key).
		Scan(&firing, &s.since, &s.lastNotified, &s.detail)
	if err == nil {
		s.firing = firing != 0
	}
	return s
}

func (a *Alerts) saveState(key string, s state) {
	f := 0
	if s.firing {
		f = 1
	}
	a.db.Exec(`INSERT INTO alert_state (key, firing, since, last_notified, detail) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET firing = excluded.firing, since = excluded.since,
			last_notified = excluded.last_notified, detail = excluded.detail`, key, f, s.since, s.lastNotified, s.detail)
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d soniya", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d daqiqa", int(d.Minutes()))
	}
	return fmt.Sprintf("%d soat %d daqiqa", int(d.Hours()), int(d.Minutes())%60)
}

// Check evaluates every condition once and sends notifications for changes.
// It returns the events it created.
func (a *Alerts) Check() ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	now := a.now()
	var events []Event

	enabled := map[string]bool{}
	for _, ck := range a.evaluate(cfg) {
		enabled[ck.key] = true
		st := a.loadState(ck.key)
		msgBody := ck.detail + "\nServer: " + a.hostname

		switch {
		case ck.firing && !st.firing:
			m := Message{Key: ck.key, Kind: "firing", Subject: ck.subject, Body: msgBody, Time: now}
			events = append(events, a.record(ck.key, "firing", ck.subject+": "+ck.detail, a.deliver(cfg, m)))
			a.saveState(ck.key, state{firing: true, since: now.Unix(), lastNotified: now.Unix(), detail: ck.detail})

		case ck.firing && st.firing:
			st.detail = ck.detail
			if now.Sub(time.Unix(st.lastNotified, 0)) >= a.reminder {
				m := Message{Key: ck.key, Kind: "reminder", Subject: "⏰ Hali davom etmoqda: " + ck.subject,
					Body: msgBody + "\nBoshlanganiga " + humanDuration(now.Sub(time.Unix(st.since, 0))) + " bo'ldi.", Time: now}
				events = append(events, a.record(ck.key, "reminder", ck.subject+": "+ck.detail, a.deliver(cfg, m)))
				st.lastNotified = now.Unix()
			}
			a.saveState(ck.key, st)

		case !ck.firing && st.firing:
			dur := humanDuration(now.Sub(time.Unix(st.since, 0)))
			m := Message{Key: ck.key, Kind: "resolved", Subject: "✅ Muammo bartaraf etildi",
				Body: strings.TrimPrefix(ck.subject, "🔴 ") + " — davomiyligi " + dur + ".\nServer: " + a.hostname, Time: now}
			events = append(events, a.record(ck.key, "resolved", strings.TrimLeft(ck.subject, "🔴🟠 ")+" — bartaraf etildi ("+dur+")", a.deliver(cfg, m)))
			a.saveState(ck.key, state{})
		}
	}

	// A condition that was switched off (or can no longer be judged) must not
	// stay "firing" forever.
	for _, key := range conditionOrder {
		if !enabled[key] {
			if st := a.loadState(key); st.firing {
				a.saveState(key, state{})
			}
		}
	}
	return events, nil
}

// Test sends a test message to every enabled channel and reports each result.
func (a *Alerts) Test() (map[string]string, error) {
	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	notifiers := a.build(cfg)
	out := map[string]string{}
	if len(notifiers) == 0 {
		return out, errors.New("no channel is enabled")
	}
	var delivery []string
	m := Message{Key: "test", Kind: "test", Subject: "🔔 Squid Admin: sinov xabari",
		Body: "Ogohlantirishlar to'g'ri sozlangan.\nServer: " + a.hostname, Time: a.now()}
	for _, n := range notifiers {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		err := n.Send(ctx, m)
		cancel()
		if err != nil {
			out[n.Name()] = err.Error()
			delivery = append(delivery, fmt.Sprintf("%s: xato: %v", n.Name(), err))
		} else {
			out[n.Name()] = "ok"
			delivery = append(delivery, n.Name()+": yuborildi")
		}
	}
	a.record("test", "test", "sinov xabari", strings.Join(delivery, "; "))
	return out, nil
}

// ConditionStatus is the current state of one condition.
type ConditionStatus struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
	Firing  bool   `json:"firing"`
	Since   int64  `json:"since"`
	Detail  string `json:"detail"`
}

func (a *Alerts) Status() ([]ConditionStatus, error) {
	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{
		CondSquidDown: cfg.SquidDown.Enabled, CondDiskFull: cfg.DiskFull.Enabled,
		CondDeniedSpike: cfg.DeniedSpike.Enabled, CondErrorSpike: cfg.ErrorSpike.Enabled,
	}
	out := []ConditionStatus{}
	for _, key := range conditionOrder {
		st := a.loadState(key)
		out = append(out, ConditionStatus{Key: key, Enabled: enabled[key], Firing: st.firing && enabled[key], Since: st.since, Detail: st.detail})
	}
	return out, nil
}

func (a *Alerts) Events(limit int) ([]Event, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := a.db.Query(`SELECT id, ts, key, kind, message, delivery FROM alert_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Time, &e.Key, &e.Kind, &e.Message, &e.Delivery); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Run checks every interval until stop closes.
func (a *Alerts) Run(stop <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if _, err := a.Check(); err != nil {
				log.Printf("alerts: %v", err)
			}
		}
	}
}
