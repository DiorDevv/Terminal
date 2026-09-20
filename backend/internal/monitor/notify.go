package monitor

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"squidadmin/backend/internal/netguard"
)

// Message is one notification.
type Message struct {
	Key     string // which condition: squid_down, disk_full, ...
	Kind    string // firing, reminder, resolved, test
	Subject string
	Body    string
	Time    time.Time
}

// Notifier delivers a message to one channel.
type Notifier interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// ---------------------------------------------------------------- Telegram

const telegramAPI = "https://api.telegram.org"

type Telegram struct {
	Token, ChatID string
	Base          string // API base URL; tests point it at a local server
	Client        *http.Client
}

func (t *Telegram) Name() string { return "telegram" }

func (t *Telegram) Send(ctx context.Context, m Message) error {
	base := t.Base
	if base == "" {
		base = telegramAPI
	}
	payload, _ := json.Marshal(map[string]any{
		"chat_id":                  t.ChatID,
		"text":                     m.Subject + "\n\n" + m.Body,
		"disable_web_page_preview": true,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/bot"+t.Token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return t.scrub(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.Client.Do(req)
	if err != nil {
		return t.scrub(err)
	}
	defer resp.Body.Close()

	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK || !out.OK {
		if out.Description == "" {
			out.Description = resp.Status
		}
		return fmt.Errorf("telegram: %s", out.Description)
	}
	return nil
}

// scrub keeps the bot token, which is part of the request URL, out of error
// messages (they end up in the alert history and on screen).
func (t *Telegram) scrub(err error) error {
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), t.Token, "***"))
}

// ----------------------------------------------------------------- Webhook

// Webhook posts JSON. The payload carries "text" (Slack and Mattermost) and
// "content" (Discord) as well as structured fields, so it works with most
// incoming-webhook endpoints as they are.
type Webhook struct {
	URL    string
	Client *http.Client
}

func (w *Webhook) Name() string { return "webhook" }

func (w *Webhook) Send(ctx context.Context, m Message) error {
	text := m.Subject + "\n" + m.Body
	payload, _ := json.Marshal(map[string]any{
		"text": text, "content": text,
		"subject": m.Subject, "body": m.Body, "key": m.Key, "kind": m.Kind,
		"time": m.Time.UTC().Format(time.RFC3339),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook: invalid URL")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.Client.Do(req)
	if err != nil {
		// The URL often carries a secret token: report only what failed.
		return fmt.Errorf("webhook: %s", strings.ReplaceAll(err.Error(), w.URL, "<url>"))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook: the server answered %s", resp.Status)
	}
	return nil
}

// ------------------------------------------------------------------- Email

type Email struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	// AllowLoopback lets the panel reach a mail server on this host.
	AllowLoopback bool
}

func (e *Email) Name() string { return "email" }

func header(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	return mime.QEncoding.Encode("utf-8", s)
}

func (e *Email) Send(ctx context.Context, m Message) error {
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	conn, err := netguard.Dialer(e.AllowLoopback).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Port 465 speaks TLS from the first byte; elsewhere STARTTLS is used when
	// the server offers it.
	var c *smtp.Client
	if e.Port == 465 {
		tc := tls.Client(conn, &tls.Config{ServerName: e.Host})
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("email: TLS: %w", err)
		}
		c, err = smtp.NewClient(tc, e.Host)
	} else {
		c, err = smtp.NewClient(conn, e.Host)
	}
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	defer c.Close()

	if e.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: e.Host}); err != nil {
				return fmt.Errorf("email: STARTTLS: %w", err)
			}
		}
	}
	if e.Username != "" {
		// PlainAuth refuses to send the password over an unencrypted connection
		// to anything but localhost, which is what we want.
		if err := c.Auth(smtp.PlainAuth("", e.Username, e.Password, e.Host)); err != nil {
			return fmt.Errorf("email: login: %w", err)
		}
	}
	if err := c.Mail(e.From); err != nil {
		return fmt.Errorf("email: sender: %w", err)
	}
	for _, to := range e.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("email: recipient %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", header(e.From))
	fmt.Fprintf(&msg, "To: %s\r\n", header(strings.Join(e.To, ", ")))
	fmt.Fprintf(&msg, "Subject: %s\r\n", header("[Squid Admin] "+m.Subject))
	fmt.Fprintf(&msg, "Date: %s\r\n", m.Time.Format(time.RFC1123Z))
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	msg.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n") + "\r\n")
	if _, err := w.Write(msg.Bytes()); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return c.Quit()
}
