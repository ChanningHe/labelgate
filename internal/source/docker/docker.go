// Package docker implements source.Source on the local Docker Engine API.
package docker

import (
	"context"
	"errors"
	"log/slog"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
	"github.com/moby/moby/client"
)

var errNotImplemented = errors.New("docker source: not implemented")

// Source is the Docker implementation of source.Source.
//
// Required behavior (WP6):
//   - Endpoints unix:// and tcp:// only; tcp:// uses TLS when docker.tls.* is
//     set (CA verifies the daemon, cert+key = client certificate). API version
//     negotiated. No ssh://, no InsecureIgnoreHostKey.
//   - Snapshot lists running containers only; Name without leading "/";
//     Networks = network name -> IPAddress (IPv4, or GlobalIPv6Address when no IPv4).
//   - Watch: subscribe first, then notify; reconnect with config.RetryConfig
//     backoff (ctx-aware sleeps), notify after each reconnect; also notify every
//     docker.poll_interval.
//   - Library: prefer github.com/moby/moby/client (current upstream module);
//     the chosen module must be govulncheck-clean for reachable code.
type Source struct {
	cli *client.Client
	log *slog.Logger
}

var _ source.Source = (*Source)(nil)

// New connects to the Docker daemon described by cfg (no I/O until first call).
func New(cfg config.DockerConfig, retry config.RetryConfig, log *slog.Logger) (*Source, error) {
	return nil, errNotImplemented
}

// Snapshot implements source.Source.
func (s *Source) Snapshot(ctx context.Context) (model.Snapshot, error) {
	return model.Snapshot{}, errNotImplemented
}

// Watch implements source.Source.
func (s *Source) Watch(ctx context.Context, notify func()) error { return errNotImplemented }

// Close releases the Docker client.
func (s *Source) Close() error { return nil }
