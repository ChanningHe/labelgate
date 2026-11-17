package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/gorilla/websocket"
)

const (
	readHeaderTimeout = 5 * time.Second // pre-auth: bounded like the handshake
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 5 * time.Second
	healthBody        = `{"status":"ok"}`
)

// validateWSURL accepts absolute ws:// or wss:// URLs with a host.
func validateWSURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return errors.New("URL scheme must be ws or wss")
	}
	if u.Host == "" {
		return errors.New("URL has no host")
	}
	return nil
}

// warnPlaintext logs a warning for ws:// peers: the handshake never reveals
// the token, but reports are neither confidential nor integrity-protected.
func warnPlaintext(log *slog.Logger, raw string) {
	if u, err := url.Parse(raw); err == nil && u.Scheme == "ws" {
		log.Warn("unencrypted ws:// connection: use wss:// so reports are confidential and tamper-proof",
			"url", u.Redacted())
	}
}

// redactURL renders a URL for logs without userinfo secrets.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid url>"
	}
	return u.Redacted()
}

// validateTLSPair requires cert and key to be set together.
func validateTLSPair(c config.TLSConfig) error {
	if (c.Cert == "") != (c.Key == "") {
		return errors.New("tls.cert and tls.key must be set together")
	}
	return nil
}

// clientTLSConfig builds the TLS configuration used to dial a peer:
// verification against the configured CA (or system roots); verification is
// disabled only when explicitly requested, with a loud warning.
func clientTLSConfig(c config.TLSConfig, log *slog.Logger) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.CA != "" {
		pem, err := os.ReadFile(c.CA)
		if err != nil {
			return nil, fmt.Errorf("agent: read tls.ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("agent: tls.ca %s: no PEM certificates found", c.CA)
		}
		cfg.RootCAs = pool
	}
	if c.InsecureSkipVerify {
		log.Warn("TLS CERTIFICATE VERIFICATION IS DISABLED (insecure_skip_verify=true): " +
			"the peer is NOT authenticated at the transport layer; use tls.ca instead")
		cfg.InsecureSkipVerify = true
	}
	return cfg, nil
}

// dial opens a WebSocket with a private Dialer (websocket.DefaultDialer is
// never used or mutated).
func dial(ctx context.Context, rawURL string, tlsCfg *tls.Config, timeout time.Duration) (*websocket.Conn, error) {
	d := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: timeout,
		TLSClientConfig:  tlsCfg.Clone(),
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ws, resp, err := d.DialContext(ctx, rawURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("%w (HTTP %d)", err, resp.StatusCode)
		}
		return nil, err
	}
	return ws, nil
}

// listen opens a TCP listener, TLS-wrapped when cert and key are configured.
func listen(addr string, c config.TLSConfig) (net.Listener, error) {
	if err := validateTLSPair(c); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	var tlsCfg *tls.Config
	if c.Cert != "" {
		cert, err := tls.LoadX509KeyPair(c.Cert, c.Key)
		if err != nil {
			return nil, fmt.Errorf("agent: load listener certificate: %w", err)
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("agent: listen %s: %w", addr, err)
	}
	if tlsCfg != nil {
		return tls.NewListener(ln, tlsCfg), nil
	}
	return ln, nil
}

// serveHTTP serves h on ln until ctx is done, then shuts the server down.
// Hijacked (WebSocket) connections are not tracked by Shutdown; their owners
// watch ctx themselves.
func serveHTTP(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("agent: serve: %w", err)
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(sctx)
	<-errc
	return nil
}

// handleHealth answers the unauthenticated health probe with a fixed body.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(healthBody))
}

// upgrade acquires a pending-handshake slot for the peer and upgrades the
// request. The returned release func is idempotent; ok=false means the
// response was written.
func upgrade(lim *pendingLimiter, up *websocket.Upgrader, w http.ResponseWriter, r *http.Request) (*websocket.Conn, func(), bool) {
	release, ok := lim.acquire(r.RemoteAddr)
	if !ok {
		http.Error(w, "too many pending connections", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	ws, err := up.Upgrade(w, r, nil)
	if err != nil { // Upgrade already replied with an HTTP error
		release()
		return nil, nil, false
	}
	return ws, release, true
}

func newUpgrader(t timings) *websocket.Upgrader {
	return &websocket.Upgrader{HandshakeTimeout: t.handshake}
}
