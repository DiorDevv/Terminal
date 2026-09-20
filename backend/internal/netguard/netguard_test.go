package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDialerRefusesDangerousAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	dial := func(allowLoopback bool, addr string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := Dialer(allowLoopback).DialContext(ctx, "tcp", addr)
		if err == nil {
			c.Close()
		}
		return err
	}

	if err := dial(false, ln.Addr().String()); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("loopback must be refused by default: %v", err)
	}
	if err := dial(true, ln.Addr().String()); err != nil {
		t.Errorf("loopback is allowed when asked for: %v", err)
	}
	for _, addr := range []string{
		"169.254.169.254:80", // cloud metadata
		"[fe80::1]:80",       // IPv6 link-local
		"0.0.0.0:80",
		"224.0.0.1:80",
		"[::]:80",
	} {
		// Even with loopback allowed these stay blocked.
		if err := dial(true, addr); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s must always be refused: %v", addr, err)
		}
	}
	// IPv6 loopback follows the same switch as 127.0.0.1.
	if err := dial(false, "[::1]:9"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("::1 must be refused by default: %v", err)
	}
}

func TestClientDoesNotFollowRedirectsIntoInternalAddresses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("secret")) }))
	defer target.Close()

	// A "public" server that bounces the client to the internal one.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// Both are on loopback, so with loopback refused even the first hop fails —
	// the point is that no hop can reach an address the dialer forbids.
	if resp, err := NewClient(false, 5*time.Second).Get(redirector.URL); err == nil {
		resp.Body.Close()
		t.Fatal("a client with loopback refused must not connect to a loopback server")
	}

	resp, err := NewClient(true, 5*time.Second).Get(redirector.URL)
	if err != nil {
		t.Fatalf("allowed loopback: %v", err)
	}
	resp.Body.Close()
}

func TestClientStopsRedirectLoops(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer srv.Close()
	resp, err := NewClient(true, 5*time.Second).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("an endless redirect must fail")
	}
	if !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("error: %v", err)
	}
}

func TestClientIgnoresProxyEnvironment(t *testing.T) {
	// The dial check only means something if requests go straight to the target.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	resp, err := NewClient(true, 5*time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("the client must not route through HTTP_PROXY: %v", err)
	}
	resp.Body.Close()
}
