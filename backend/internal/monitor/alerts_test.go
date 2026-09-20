package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ------------------------------------------------------------------ fixtures

type fakeProbe struct {
	running      bool
	diskPct      float64
	diskErr      error
	denied, errs int
	lastWindow   time.Duration
}

func (p *fakeProbe) SquidRunning() bool { return p.running }
func (p *fakeProbe) Disk() (string, float64, error) {
	return "/var/log/squid", p.diskPct, p.diskErr
}
func (p *fakeProbe) Recent(w time.Duration) (int, int, int) {
	p.lastWindow = w
	return 0, p.denied, p.errs
}

type sink struct {
	mu   sync.Mutex
	sent []Message
	fail error
}

func (s *sink) Name() string { return "sink" }
func (s *sink) Send(_ context.Context, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.sent = append(s.sent, m)
	return nil
}
func (s *sink) kinds() string {
	var k []string
	for _, m := range s.sent {
		k = append(k, m.Key+":"+m.Kind)
	}
	return strings.Join(k, ",")
}

type alertRig struct {
	a     *Alerts
	probe *fakeProbe
	out   *sink
	clock time.Time
}

func newAlertRig(t *testing.T) *alertRig {
	t.Helper()
	r := &alertRig{
		probe: &fakeProbe{running: true},
		out:   &sink{},
		clock: time.Unix(base, 0),
	}
	r.a = NewAlerts(newDB(t), r.probe, true)
	r.a.now = func() time.Time { return r.clock }
	r.a.build = func(Config) []Notifier { return []Notifier{r.out} }
	return r
}

func (r *alertRig) check(t *testing.T) []Event {
	t.Helper()
	ev, err := r.a.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return ev
}

func (r *alertRig) advance(d time.Duration) { r.clock = r.clock.Add(d) }

// ------------------------------------------------------------- configuration

func TestSecretsAreNeverShownAndSurviveAnEmptyResave(t *testing.T) {
	r := newAlertRig(t)
	cfg := DefaultConfig()
	cfg.Telegram = TelegramConfig{Enabled: true, Token: "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", ChatID: "-1001234567890"}
	cfg.Webhook = WebhookConfig{Enabled: true, URL: "https://hooks.example.com/services/T000/B000/SECRETSECRET"}
	cfg.Email = EmailConfig{Enabled: true, Host: "smtp.example.com", Port: 587, Username: "bot", Password: "hunter2-password", From: "squid@example.com", To: []string{"admin@example.com"}}
	if _, err := r.a.SetConfig(cfg); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	stored, _ := r.a.Config()
	shown, _ := json.Marshal(View(stored))
	for _, secret := range []string{"AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", "SECRETSECRET", "hunter2-password", "services/T000"} {
		if strings.Contains(string(shown), secret) {
			t.Errorf("the client view must not contain %q:\n%s", secret, shown)
		}
	}
	v := View(stored)
	if !v.Telegram.TokenSet || !v.Webhook.URLSet || !v.Email.PasswordSet || v.Webhook.Host != "hooks.example.com" {
		t.Errorf("the view must say which secrets are set: %+v", v)
	}

	// The UI sends the form back with the secret fields empty.
	again := cfg
	again.Telegram.Token, again.Webhook.URL, again.Email.Password = "", "", ""
	again.DiskFull.Percent = 80
	if _, err := r.a.SetConfig(again); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _ := r.a.Config()
	if got.Telegram.Token != cfg.Telegram.Token || got.Webhook.URL != cfg.Webhook.URL || got.Email.Password != cfg.Email.Password {
		t.Errorf("empty secret fields must keep the stored values: %+v", got)
	}
	if got.DiskFull.Percent != 80 {
		t.Error("the other changes must still be saved")
	}
}

func TestConfigValidation(t *testing.T) {
	r := newAlertRig(t)
	good := DefaultConfig()

	mut := func(f func(c *Config)) Config { c := good; f(&c); return c }
	bad := map[string]Config{
		"disk percent too low":   mut(func(c *Config) { c.DiskFull.Percent = 10 }),
		"disk percent 100":       mut(func(c *Config) { c.DiskFull.Percent = 100 }),
		"spike threshold zero":   mut(func(c *Config) { c.DeniedSpike.Threshold = 0 }),
		"spike window too big":   mut(func(c *Config) { c.ErrorSpike.WindowMinutes = 500 }),
		"telegram without token": mut(func(c *Config) { c.Telegram = TelegramConfig{Enabled: true, ChatID: "123"} }),
		"telegram bad token":     mut(func(c *Config) { c.Telegram = TelegramConfig{Enabled: true, Token: "nope", ChatID: "123"} }),
		"telegram bad chat": mut(func(c *Config) {
			c.Telegram = TelegramConfig{Enabled: true, Token: "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw", ChatID: "not a chat"}
		}),
		"webhook ftp":         mut(func(c *Config) { c.Webhook = WebhookConfig{Enabled: true, URL: "ftp://x.example/hook"} }),
		"webhook credentials": mut(func(c *Config) { c.Webhook = WebhookConfig{Enabled: true, URL: "https://u:p@x.example/hook"} }),
		"email without hosts": mut(func(c *Config) { c.Email = EmailConfig{Enabled: true, Port: 587} }),
		"email bad recipient": mut(func(c *Config) {
			c.Email = EmailConfig{Enabled: true, Host: "smtp.example.com", Port: 587, From: "a@example.com", To: []string{"not-an-address"}}
		}),
		"email header injection": mut(func(c *Config) {
			c.Email = EmailConfig{Enabled: true, Host: "smtp.example.com", Port: 587, From: "a@example.com", To: []string{"x@example.com>\r\nBcc: victim@example.com"}}
		}),
		"email too many": mut(func(c *Config) {
			c.Email = EmailConfig{Enabled: true, Host: "smtp.example.com", Port: 587, From: "a@example.com", To: make([]string, 11)}
		}),
	}
	for name, c := range bad {
		if _, err := r.a.SetConfig(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// A disabled channel is not validated (it may be half filled in).
	if _, err := r.a.SetConfig(mut(func(c *Config) { c.Telegram = TelegramConfig{Enabled: false, Token: "garbage"} })); err != nil {
		t.Errorf("a disabled channel must not block saving: %v", err)
	}

	var ce *ConfigError
	_, err := r.a.SetConfig(mut(func(c *Config) { c.DiskFull.Percent = 1; c.DeniedSpike.Threshold = -5 }))
	if !errors.As(err, &ce) || len(ce.Fields) != 2 {
		t.Errorf("every bad field must be reported at once: %v", err)
	}
}

// ------------------------------------------------------------ state machine

func TestSquidDownNeedsTwoFailedChecksThenNotifiesOnceAndResolves(t *testing.T) {
	r := newAlertRig(t)
	r.probe.running = false

	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("one failed check is not an outage: %+v", ev)
	}
	r.advance(time.Minute)
	ev := r.check(t)
	if len(ev) != 1 || ev[0].Kind != "firing" || ev[0].Key != CondSquidDown {
		t.Fatalf("the second failed check must alert: %+v", ev)
	}
	for i := 0; i < 5; i++ {
		r.advance(time.Minute)
		if ev := r.check(t); len(ev) != 0 {
			t.Fatalf("a condition that keeps firing must not repeat every minute: %+v", ev)
		}
	}

	r.probe.running = true
	r.advance(time.Minute)
	ev = r.check(t)
	if len(ev) != 1 || ev[0].Kind != "resolved" {
		t.Fatalf("recovery must be announced once: %+v", ev)
	}
	r.advance(time.Minute)
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("nothing more to say once resolved: %+v", ev)
	}
	if got := r.out.kinds(); got != "squid_down:firing,squid_down:resolved" {
		t.Errorf("messages sent: %s", got)
	}
	if !strings.Contains(r.out.sent[1].Body, "daqiqa") {
		t.Errorf("the recovery message should say how long it lasted: %q", r.out.sent[1].Body)
	}
}

func TestABlipDoesNotAlert(t *testing.T) {
	r := newAlertRig(t)
	r.probe.running = false
	r.check(t)
	r.probe.running = true // recovered before the second check
	r.advance(time.Minute)
	r.check(t)
	r.probe.running = false
	r.advance(time.Minute)
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("the failure counter must reset on success: %+v", ev)
	}
}

func TestRemindersRepeatOnlyAfterTheInterval(t *testing.T) {
	r := newAlertRig(t)
	r.probe.diskPct = 95

	if ev := r.check(t); len(ev) != 1 || ev[0].Kind != "firing" {
		t.Fatalf("firing: %+v", ev)
	}
	r.advance(5*time.Hour + 59*time.Minute)
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("too early for a reminder: %+v", ev)
	}
	r.advance(2 * time.Minute)
	ev := r.check(t)
	if len(ev) != 1 || ev[0].Kind != "reminder" {
		t.Fatalf("a reminder is due after 6h: %+v", ev)
	}
	r.advance(time.Hour)
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("the reminder timer restarts: %+v", ev)
	}
	if !strings.Contains(r.out.sent[1].Body, "soat") {
		t.Errorf("a reminder should say how long it has lasted: %q", r.out.sent[1].Body)
	}
}

func TestThresholdsAreInclusiveAndUseTheConfiguredWindow(t *testing.T) {
	r := newAlertRig(t)
	cfg := DefaultConfig()
	cfg.DiskFull = Disk{Enabled: true, Percent: 90}
	cfg.DeniedSpike = Spike{Enabled: true, Threshold: 200, WindowMinutes: 15}
	cfg.ErrorSpike = Spike{Enabled: true, Threshold: 50, WindowMinutes: 3}
	if _, err := r.a.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}

	r.probe.diskPct, r.probe.denied, r.probe.errs = 89.9, 199, 49
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("just below every threshold: %+v", ev)
	}
	r.probe.diskPct, r.probe.denied, r.probe.errs = 90.0, 200, 50
	ev := r.check(t)
	if len(ev) != 3 {
		t.Fatalf("exactly at every threshold must fire all three: %+v", ev)
	}
	if r.probe.lastWindow != 3*time.Minute {
		t.Errorf("the error spike must look at its own window, last window = %v", r.probe.lastWindow)
	}
}

func TestDisabledConditionsAreSilentAndClearTheirState(t *testing.T) {
	r := newAlertRig(t)
	r.probe.diskPct = 99
	r.check(t) // firing
	if st, _ := r.a.Status(); !st[1].Firing {
		t.Fatal("disk_full should be firing")
	}

	cfg, _ := r.a.Config()
	cfg.DiskFull.Enabled = false
	r.a.SetConfig(cfg)
	if ev := r.check(t); len(ev) != 0 {
		t.Fatalf("switching a condition off must not send anything: %+v", ev)
	}
	if st, _ := r.a.Status(); st[1].Firing || st[1].Enabled {
		t.Errorf("a disabled condition must not stay 'firing': %+v", st[1])
	}

	cfg.DiskFull.Enabled = true
	r.a.SetConfig(cfg)
	if ev := r.check(t); len(ev) != 1 || ev[0].Kind != "firing" {
		t.Fatalf("switching it back on while still full alerts afresh: %+v", ev)
	}
}

func TestDeliveryOutcomeIsRecordedIncludingFailures(t *testing.T) {
	r := newAlertRig(t)
	r.out.fail = errors.New("connection refused")
	r.probe.diskPct = 95
	ev := r.check(t)
	if len(ev) != 1 || !strings.Contains(ev[0].Delivery, "xato") || !strings.Contains(ev[0].Delivery, "connection refused") {
		t.Fatalf("a failed delivery must be visible in the history: %+v", ev)
	}
	events, _ := r.a.Events(10)
	if len(events) != 1 || events[0].Delivery != ev[0].Delivery {
		t.Errorf("stored events: %+v", events)
	}

	// With no channel at all the event is still recorded, with a clear note.
	r2 := newAlertRig(t)
	r2.a.build = func(Config) []Notifier { return nil }
	r2.probe.diskPct = 95
	ev = r2.check(t)
	if len(ev) != 1 || ev[0].Delivery != "kanal sozlanmagan" {
		t.Errorf("no channel configured: %+v", ev)
	}
}

func TestStatusAndEventHistory(t *testing.T) {
	r := newAlertRig(t)
	r.probe.diskPct = 95
	r.check(t)
	st, _ := r.a.Status()
	if len(st) != 4 || st[0].Key != CondSquidDown || !st[1].Firing || st[1].Detail == "" || st[1].Since != base {
		t.Errorf("status: %+v", st)
	}
	for i := 0; i < 600; i++ {
		r.a.record("x", "test", "filler", "")
	}
	if ev, _ := r.a.Events(1000); len(ev) > 200 {
		t.Errorf("the API must cap the history size, got %d", len(ev))
	}
	var n int
	r.a.db.QueryRow(`SELECT COUNT(*) FROM alert_events`).Scan(&n)
	if n > 500 {
		t.Errorf("stored history must be bounded, has %d", n)
	}
}

func TestTestSendReportsEachChannel(t *testing.T) {
	r := newAlertRig(t)
	got, err := r.a.Test()
	if err != nil || got["sink"] != "ok" || len(r.out.sent) != 1 || r.out.sent[0].Kind != "test" {
		t.Fatalf("Test: %v %v", got, err)
	}
	r.out.fail = errors.New("boom")
	if got, _ := r.a.Test(); got["sink"] != "boom" {
		t.Errorf("a failing channel must be reported: %v", got)
	}
	// The history shows the outcome in words, like every other event.
	events, _ := r.a.Events(5)
	if len(events) != 2 || events[0].Delivery != "sink: xato: boom" || events[1].Delivery != "sink: yuborildi" {
		t.Errorf("test sends must be recorded readably: %+v", events)
	}
	r.a.build = func(Config) []Notifier { return nil }
	if _, err := r.a.Test(); err == nil {
		t.Error("testing with no channel enabled is an error")
	}
}

// ---------------------------------------------------------------- channels

func TestTelegram(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		if gotBody["chat_id"] == "bad" {
			w.WriteHeader(400)
			w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	tg := &Telegram{Token: token, ChatID: "-100123", Base: srv.URL, Client: srv.Client()}
	msg := Message{Subject: "Squid ishlamayapti", Body: "server: box"}
	if err := tg.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/bot"+token+"/sendMessage" || gotBody["chat_id"] != "-100123" || !strings.Contains(gotBody["text"].(string), "Squid ishlamayapti") {
		t.Errorf("request: path=%q body=%v", gotPath, gotBody)
	}

	tg.ChatID = "bad"
	if err := tg.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("an API error must be surfaced: %v", err)
	}

	// The token is part of the URL, so a network error would print it.
	dead := &Telegram{Token: token, ChatID: "1", Base: "http://127.0.0.1:1", Client: srv.Client()}
	err := dead.Send(context.Background(), msg)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("the bot token must never appear in an error: %v", err)
	}
}

func TestWebhook(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		if r.URL.Path == "/fail" {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	wh := &Webhook{URL: srv.URL + "/hook", Client: srv.Client()}
	msg := Message{Key: "disk_full", Kind: "firing", Subject: "Disk to'ldi", Body: "95%", Time: time.Unix(base, 0)}
	if err := wh.Send(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"text", "content", "subject", "body", "key", "kind", "time"} {
		if got[k] == nil || got[k] == "" {
			t.Errorf("payload is missing %q: %v", k, got)
		}
	}
	if !strings.Contains(got["text"].(string), "Disk to'ldi") {
		t.Errorf("text: %v", got["text"])
	}

	bad := &Webhook{URL: srv.URL + "/fail", Client: srv.Client()}
	if err := bad.Send(context.Background(), msg); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("a failing endpoint: %v", err)
	}

	secret := &Webhook{URL: "http://127.0.0.1:1/services/SECRETTOKEN", Client: srv.Client()}
	if err := secret.Send(context.Background(), msg); err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Errorf("a secret in the webhook URL must not leak into errors: %v", err)
	}
}

func TestWebhooksCannotTargetInternalServices(t *testing.T) {
	hit := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer internal.Close()

	a := NewAlerts(newDB(t), &fakeProbe{running: true}, false) // loopback NOT allowed
	wh := &Webhook{URL: internal.URL, Client: a.client}
	err := wh.Send(context.Background(), Message{Subject: "x"})
	if err == nil || !strings.Contains(err.Error(), "refusing") || hit {
		t.Fatalf("a webhook to loopback must be refused (hit=%v): %v", hit, err)
	}
	meta := &Webhook{URL: "http://169.254.169.254/latest/meta-data/", Client: a.client}
	if err := meta.Send(context.Background(), Message{Subject: "x"}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("cloud metadata must be refused: %v", err)
	}
}

// fakeSMTP is a minimal SMTP server that records what it is sent.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	from     string
	rcpt     []string
	data     string
	starttls bool
	rejectTo string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	say := func(l string) { fmt.Fprintf(c, "%s\r\n", l) }
	say("220 fake ESMTP")
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		s.mu.Lock()
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.from = strings.TrimSpace(line[10:])
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to := strings.TrimSpace(line[8:])
			if s.rejectTo != "" && strings.Contains(to, s.rejectTo) {
				say("550 no such user")
			} else {
				s.rcpt = append(s.rcpt, to)
				say("250 ok")
			}
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := rd.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.data = b.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			s.mu.Unlock()
			return
		default:
			say("250 ok")
		}
		s.mu.Unlock()
	}
}

func TestEmail(t *testing.T) {
	srv := newFakeSMTP(t)
	host, port, _ := net.SplitHostPort(srv.ln.Addr().String())
	var p int
	fmt.Sscan(port, &p)

	em := &Email{Host: host, Port: p, From: "squid@example.com", To: []string{"a@example.com", "b@example.com"}, AllowLoopback: true}
	msg := Message{Key: "squid_down", Kind: "firing", Subject: "🔴 Squid ishlamayapti", Body: "Server: box\nikkinchi qator", Time: time.Unix(base, 0)}
	if err := em.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.Contains(srv.from, "squid@example.com") || len(srv.rcpt) != 2 {
		t.Errorf("envelope: from=%q rcpt=%v", srv.from, srv.rcpt)
	}
	for _, want := range []string{"Subject: =?utf-8?", "MIME-Version: 1.0", "Content-Type: text/plain; charset=UTF-8", "To: a@example.com, b@example.com", "ikkinchi qator"} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message is missing %q:\n%s", want, srv.data)
		}
	}
}

func TestEmailReportsARejectedRecipient(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.rejectTo = "bad@"
	host, port, _ := net.SplitHostPort(srv.ln.Addr().String())
	var p int
	fmt.Sscan(port, &p)

	em := &Email{Host: host, Port: p, From: "s@example.com", To: []string{"bad@example.com"}, AllowLoopback: true}
	if err := em.Send(context.Background(), Message{Subject: "x", Body: "y", Time: time.Unix(base, 0)}); err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Errorf("a rejected recipient must be reported: %v", err)
	}

	// The panel must not be usable to port-scan its own host.
	blocked := &Email{Host: "127.0.0.1", Port: p, From: "s@example.com", To: []string{"a@example.com"}, AllowLoopback: false}
	if err := blocked.Send(context.Background(), Message{Subject: "x", Time: time.Unix(base, 0)}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("an SMTP host on loopback must be refused: %v", err)
	}
}

func TestEmailHeadersCannotBeInjected(t *testing.T) {
	got := header("Squid down\r\nBcc: victim@example.com")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("a subject must never carry line breaks: %q", got)
	}
}

var _ = io.Discard
