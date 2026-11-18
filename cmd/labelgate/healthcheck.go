package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

const healthcheckTimeout = 2 * time.Second

// healthcheck implements the container HEALTHCHECK. It reads the same
// configuration as the server (address, base path, mode), so it stays correct
// for custom ports or a disabled API.
//
//	main mode, API disabled or token empty: healthy (process liveness is Docker's job)
//	main mode, API enabled: GET http://<loopback>:<port><base_path>/health must return 200
//	agent, inbound: TCP connect to connect.listen
//	agent, outbound: healthy (nothing listens)
func (a *app) healthcheck(cfg config.Config, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	if err := checkHealth(ctx, cfg, http.DefaultClient); err != nil {
		fmt.Fprintf(stderr, "healthcheck failed: %v\n", err)
		return exitFatal
	}
	return exitOK
}

func checkHealth(ctx context.Context, cfg config.Config, client *http.Client) error {
	switch cfg.Mode {
	case config.ModeAgent:
		if cfg.Connect.Mode != config.ConnectInbound {
			return nil
		}
		return dialCheck(ctx, cfg.Connect.Listen)
	default:
		if !cfg.API.Enabled || cfg.API.Token == "" {
			return nil
		}
		return httpCheck(ctx, client, cfg.API.Address, cfg.API.BasePath)
	}
}

func httpCheck(ctx context.Context, client *http.Client, listen, basePath string) error {
	addr, err := loopbackAddr(listen)
	if err != nil {
		return err
	}
	url := "http://" + addr + basePath + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("the API is not answering at %s: %w; check the labelgate logs, and that %s is the address it listens on",
			url, err, config.KeyWithEnv("api.address"))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned HTTP %d, want 200; check %s", url, resp.StatusCode, config.KeyWithEnv("api.base_path"))
	}
	return nil
}

func dialCheck(ctx context.Context, listen string) error {
	addr, err := loopbackAddr(listen)
	if err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("the agent's inbound listener is not accepting connections at %s: %w; check the labelgate logs and %s",
			addr, err, config.KeyWithEnv("connect.listen"))
	}
	return conn.Close()
}

// loopbackAddr turns a listen address into one that can be dialed locally:
// wildcard hosts ("", 0.0.0.0, ::) become the loopback address; specific hosts
// are kept (the server only accepts connections there).
func loopbackAddr(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if port == "" {
		return "", fmt.Errorf("invalid listen address %q: missing port", listen)
	}
	if ip, err := netip.ParseAddr(host); host == "" || (err == nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
