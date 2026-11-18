// Package docker implements source.Source on the local Docker Engine API.
package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/source"
)

// DefaultEndpoint is used when docker.endpoint is empty.
const DefaultEndpoint = "unix:///var/run/docker.sock"

const shortIDLen = 12

// Supported endpoint schemes. ssh:// was removed in v0.3.
const (
	schemeUnix = "unix"
	schemeTCP  = "tcp"
)

// Source is the Docker implementation of source.Source.
//
//   - Endpoints unix:// and tcp:// only; tcp:// uses TLS when docker.tls.* is
//     set (CA verifies the daemon, cert+key = client certificate). The API
//     version is negotiated by the client library.
//   - Snapshot lists running containers (filtered by docker.filter_label).
//   - Watch subscribes to the event stream first and signals right after each
//     (re)subscription, so nothing between a list and the subscription is lost;
//     failures reconnect with ctx-aware backoff forever.
type Source struct {
	cli      *client.Client
	endpoint string // for error messages
	filter   string
	poll     time.Duration
	retry    config.RetryConfig
	log      *slog.Logger
	now      func() time.Time
}

var _ source.Source = (*Source)(nil)

// New builds a Docker client for cfg. No I/O happens until the first call.
func New(cfg config.DockerConfig, retry config.RetryConfig, log *slog.Logger) (*Source, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	scheme, err := endpointScheme(endpoint)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	var opts []client.Opt
	if scheme == schemeTCP {
		hc, err := tlsHTTPClient(cfg.TLS, log)
		if err != nil {
			return nil, err
		}
		if hc != nil {
			opts = append(opts, client.WithHTTPClient(hc))
		}
	}
	opts = append(opts, client.WithHost(endpoint))
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker: create client for %q: %w", endpoint, err)
	}
	return &Source{
		cli:      cli,
		endpoint: endpoint,
		filter:   cfg.FilterLabel,
		poll:     cfg.PollInterval,
		retry:    retry,
		log:      log,
		now:      time.Now,
	}, nil
}

// endpointScheme accepts unix:// and tcp:// only.
func endpointScheme(endpoint string) (string, error) {
	scheme, rest, ok := strings.Cut(endpoint, "://")
	if !ok || rest == "" {
		return "", fmt.Errorf("docker: invalid endpoint %q: want unix:///path or tcp://host:port; fix %s", endpoint, config.KeyWithEnv("docker.endpoint"))
	}
	switch scheme {
	case schemeUnix, schemeTCP:
		return scheme, nil
	case "ssh":
		return "", errors.New("docker: ssh:// endpoints were removed in v0.3; use a unix socket or tcp:// with TLS")
	default:
		return "", fmt.Errorf("docker: unsupported endpoint scheme %q: want unix or tcp", scheme)
	}
}

// Close releases the Docker client.
func (s *Source) Close() error { return s.cli.Close() }

// Snapshot implements source.Source.
func (s *Source) Snapshot(ctx context.Context) (model.Snapshot, error) {
	res, err := s.cli.ContainerList(ctx, client.ContainerListOptions{Filters: s.labelFilters(nil)})
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("docker: cannot list containers: %w", explain(s.endpoint, err))
	}
	containers := make([]model.Container, 0, len(res.Items))
	for _, c := range res.Items {
		if c.State != container.StateRunning || !matchesFilter(c.Labels, s.filter) {
			continue
		}
		containers = append(containers, toContainer(c))
	}
	slices.SortFunc(containers, func(a, b model.Container) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return model.Snapshot{
		AgentID:    model.LocalAgentID,
		Complete:   true,
		Containers: containers,
		TakenAt:    s.now().UTC(),
	}, nil
}

// labelFilters returns base plus the daemon-side label filter, if configured.
func (s *Source) labelFilters(base client.Filters) client.Filters {
	f := client.Filters{}
	for term, vals := range base {
		for v := range vals {
			f.Add(term, v)
		}
	}
	if s.filter != "" {
		f.Add("label", s.filter)
	}
	return f
}

func toContainer(c container.Summary) model.Container {
	labels := make(map[string]string, len(c.Labels))
	for k, v := range c.Labels {
		labels[k] = v
	}
	v4, v6 := networkAddrs(c)
	return model.Container{
		ID:        c.ID,
		Name:      containerName(c),
		Labels:    labels,
		Networks:  v4,
		Networks6: v6,
		State:     string(c.State),
	}
}

// containerName is the first name without its leading slash (short ID if unnamed).
func containerName(c container.Summary) string {
	if len(c.Names) > 0 {
		if n := strings.TrimPrefix(c.Names[0], "/"); n != "" {
			return n
		}
	}
	if len(c.ID) > shortIDLen {
		return c.ID[:shortIDLen]
	}
	return c.ID
}

// networkAddrs returns the per-network addresses of a container: v4 maps
// network name -> IPv4 address, v6 maps network name -> global IPv6 address.
// Networks without an address of a kind are omitted from that map.
func networkAddrs(c container.Summary) (v4, v6 map[string]string) {
	if c.NetworkSettings == nil {
		return nil, nil
	}
	v4 = make(map[string]string, len(c.NetworkSettings.Networks))
	v6 = make(map[string]string, len(c.NetworkSettings.Networks))
	for name, ep := range c.NetworkSettings.Networks {
		if ep == nil {
			continue
		}
		if ep.GlobalIPv6Address.IsValid() {
			v6[name] = ep.GlobalIPv6Address.String()
		}
		if ep.IPAddress.IsValid() {
			v4[name] = ep.IPAddress.String()
		}
	}
	return v4, v6
}

// matchesFilter implements docker.filter_label: "key" requires the label,
// "key=value" requires that exact value; "" matches everything.
func matchesFilter(labels map[string]string, filter string) bool {
	if filter == "" {
		return true
	}
	key, want, hasValue := strings.Cut(filter, "=")
	got, ok := labels[key]
	if !ok {
		return false
	}
	return !hasValue || got == want
}
