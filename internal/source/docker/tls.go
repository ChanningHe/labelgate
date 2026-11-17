package docker

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/channinghe/labelgate/internal/config"
)

// tlsHTTPClient builds the HTTP client for tcp:// endpoints, or nil when no
// docker.tls.* option is set (plain HTTP). Server certificates are verified
// against docker.tls.ca (else the system roots) unless insecure_skip_verify is
// explicitly enabled.
func tlsHTTPClient(cfg config.TLSConfig, log *slog.Logger) (*http.Client, error) {
	if cfg == (config.TLSConfig{}) {
		return nil, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CA != "" {
		pool, err := loadCAPool(cfg.CA)
		if err != nil {
			return nil, err
		}
		tc.RootCAs = pool
	}
	if cfg.Cert != "" || cfg.Key != "" {
		if cfg.Cert == "" || cfg.Key == "" {
			return nil, errors.New("docker: tls.cert and tls.key must be set together")
		}
		cert, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
		if err != nil {
			return nil, fmt.Errorf("docker: load client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	if cfg.InsecureSkipVerify {
		log.Warn("docker.tls.insecure_skip_verify is enabled: the Docker daemon's certificate is NOT verified")
		tc.InsecureSkipVerify = true // #nosec G402 -- explicit operator opt-in, warned above
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("docker: read tls.ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("docker: tls.ca %q contains no valid certificate", path)
	}
	return pool, nil
}
