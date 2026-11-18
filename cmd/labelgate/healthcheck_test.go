package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/config"
)

func TestLoopbackAddr(t *testing.T) {
	tests := []struct {
		listen  string
		want    string
		wantErr bool
	}{
		{":28111", "127.0.0.1:28111", false},
		{"0.0.0.0:28111", "127.0.0.1:28111", false},
		{"[::]:28111", "127.0.0.1:28111", false},
		{"127.0.0.1:9000", "127.0.0.1:9000", false},
		{"10.1.2.3:28111", "10.1.2.3:28111", false},
		{"[::1]:28111", "[::1]:28111", false},
		{"localhost:28111", "localhost:28111", false},
		{"28111", "", true},
		{"host:", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.listen, func(t *testing.T) {
			got, err := loopbackAddr(tt.listen)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("loopbackAddr(%q) = %q, %v", tt.listen, got, err)
			}
		})
	}
}

// healthServer answers GET {base}/health with status.
func healthServer(t *testing.T, base string, status int) (addr string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != base+"/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestCheckHealthMainMode(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *config.Config, addr string)
		status  int
		base    string
		wantErr bool
	}{
		{"healthy on default base path", func(c *config.Config, a string) { c.API.Address = a }, 200, "/api", false},
		{"healthy on custom base path", func(c *config.Config, a string) { c.API.Address = a; c.API.BasePath = "/lg/v1" }, 200, "/lg/v1", false},
		{"wrong base path is unhealthy", func(c *config.Config, a string) { c.API.Address = a; c.API.BasePath = "/other" }, 200, "/api", true},
		{"non-200 is unhealthy", func(c *config.Config, a string) { c.API.Address = a }, 503, "/api", true},
		{"API disabled is healthy even if nothing listens", func(c *config.Config, _ string) {
			c.API.Enabled = false
			c.API.Address = "127.0.0.1:1"
		}, 200, "/api", false},
		{"missing token (API not started) is healthy", func(c *config.Config, _ string) {
			c.API.Token = ""
			c.API.Address = "127.0.0.1:1"
		}, 200, "/api", false},
		{"bad address is unhealthy", func(c *config.Config, _ string) { c.API.Address = "nonsense" }, 200, "/api", true},
		{"nothing listening is unhealthy", func(c *config.Config, _ string) { c.API.Address = closedAddr(t) }, 200, "/api", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := healthServer(t, tt.base, tt.status)
			cfg := config.Defaults()
			cfg.Mode = config.ModeMain
			cfg.API.Token = "tok"
			tt.mutate(&cfg, addr)
			err := checkHealth(context.Background(), cfg, http.DefaultClient)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckHealthAgentMode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	tests := []struct {
		name    string
		mode    config.ConnectMode
		listen  string
		wantErr bool
	}{
		{"outbound is healthy", config.ConnectOutbound, "127.0.0.1:1", false},
		{"inbound listening", config.ConnectInbound, ln.Addr().String(), false},
		{"inbound not listening", config.ConnectInbound, closedAddr(t), true},
		{"inbound bad address", config.ConnectInbound, "nope", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Mode = config.ModeAgent
			cfg.Connect.Mode = tt.mode
			cfg.Connect.Listen = tt.listen
			err := checkHealth(context.Background(), cfg, http.DefaultClient)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHealthcheckSubcommand(t *testing.T) {
	addr := healthServer(t, "/api", 200)
	h := newHarness(t)
	cfg := config.Defaults()
	cfg.API.Token = "tok"
	cfg.API.Address = addr
	h.app.load = func(string) (config.Config, error) { return cfg, nil }

	var out, errOut bytes.Buffer
	if got := h.app.run([]string{"healthcheck"}, &out, &errOut); got != exitOK {
		t.Fatalf("exit = %d, stderr %q", got, errOut.String())
	}

	cfg.API.Address = closedAddr(t)
	errOut.Reset()
	if got := h.app.run([]string{"-c", "x.yaml", "healthcheck"}, &out, &errOut); got != exitFatal {
		t.Fatalf("exit = %d", got)
	}
	for _, want := range []string{"healthcheck failed", "the API is not answering", "api.address (env LABELGATE_API_ADDRESS)"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("stderr = %q, want %q", errOut.String(), want)
		}
	}
}

// closedAddr returns a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
