// Package netguard builds network clients for addresses an administrator typed
// in (block list sources, alert webhooks, an SMTP server). Such addresses are
// a server-side request forgery risk: pointed at 127.0.0.1 or a cloud metadata
// address they would let a panel user make this server talk to services that
// trust it.
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Dialer returns a dialer that refuses to connect to loopback (unless
// allowLoopback), unspecified, multicast and link-local addresses — the last
// covers cloud metadata endpoints such as 169.254.169.254. Ordinary private
// LAN addresses are allowed: lists, webhooks and mail servers legitimately
// live there.
//
// The check runs on the resolved address at connect time, so DNS tricks and
// redirects cannot get around it.
func Dialer(allowLoopback bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			switch {
			case ip == nil:
				return fmt.Errorf("refusing to connect to %q", host)
			case ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast():
				return fmt.Errorf("refusing to connect to %s (not a public or LAN address)", ip)
			case ip.IsLoopback() && !allowLoopback:
				return fmt.Errorf("refusing to connect to %s (loopback)", ip)
			}
			return nil
		},
	}
}

// NewClient returns an HTTP client using Dialer. At most three redirects are
// followed.
func NewClient(allowLoopback bool, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: Dialer(allowLoopback).DialContext, Proxy: nil},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}
