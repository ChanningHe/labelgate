// Package docker provides Docker container provider implementation.
package docker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/provider"
	"github.com/channinghe/labelgate/internal/types"
)

// ensure DockerProvider implements Provider interface
var _ provider.Provider = (*DockerProvider)(nil)

// DockerProvider implements the Provider interface for Docker.
type DockerProvider struct {
	client *client.Client
	config *config.DockerConfig
}

// NewDockerProvider creates a new Docker provider.
func NewDockerProvider(cfg *config.DockerConfig) *DockerProvider {
	return &DockerProvider{
		config: cfg,
	}
}

// Name returns the provider name.
func (p *DockerProvider) Name() string {
	return "docker"
}

// Connect establishes connection to Docker.
func (p *DockerProvider) Connect(ctx context.Context) error {
	endpoint := p.config.Endpoint
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}

	var opts []client.Opt

	// Parse endpoint and configure connection
	if strings.HasPrefix(endpoint, "unix://") {
		// Unix socket connection
		opts = append(opts,
			client.WithHost(endpoint),
			client.WithAPIVersionNegotiation(),
		)
	} else if strings.HasPrefix(endpoint, "tcp://") {
		// TCP connection (with optional TLS)
		httpClient, err := p.createHTTPClient()
		if err != nil {
			return fmt.Errorf("failed to create HTTP client: %w", err)
		}
		opts = append(opts,
			client.WithHost(endpoint),
			client.WithHTTPClient(httpClient),
			client.WithAPIVersionNegotiation(),
		)
	} else if strings.HasPrefix(endpoint, "ssh://") {
		// SSH connection
		httpClient, err := p.createSSHClient(ctx)
		if err != nil {
			return fmt.Errorf("failed to create SSH client: %w", err)
		}
		// For SSH, we need to use a custom dialer
		opts = append(opts,
			client.WithHTTPClient(httpClient),
			client.WithHost("http://docker"), // Placeholder, actual connection via SSH
			client.WithAPIVersionNegotiation(),
		)
	} else {
		return fmt.Errorf("unsupported endpoint scheme: %s", endpoint)
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Test connection
	_, err = cli.Ping(ctx)
	if err != nil {
		cli.Close()
		return fmt.Errorf("failed to connect to Docker: %w", err)
	}

	p.client = cli
	log.Info().Str("endpoint", endpoint).Msg("Connected to Docker")
	return nil
}

// Close closes the Docker client.
func (p *DockerProvider) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// ListContainers returns all running containers.
func (p *DockerProvider) ListContainers(ctx context.Context) ([]*types.ContainerInfo, error) {
	if p.client == nil {
		return nil, fmt.Errorf("Docker client not connected")
	}

	containers, err := p.client.ContainerList(ctx, container.ListOptions{
		All: false, // Only running containers
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var result []*types.ContainerInfo
	for _, c := range containers {
		// Apply filter if configured
		if p.config.FilterLabel != "" {
			if !matchesFilter(c.Labels, p.config.FilterLabel) {
				continue
			}
		}

		info := &types.ContainerInfo{
			ID:       c.ID,
			Name:     strings.TrimPrefix(c.Names[0], "/"),
			Image:    c.Image,
			Labels:   c.Labels,
			State:    c.State,
			Created:  time.Unix(c.Created, 0),
			Networks: make(map[string]string),
		}

		// Extract network IPs
		if c.NetworkSettings != nil {
			for name, net := range c.NetworkSettings.Networks {
				if net.IPAddress != "" {
					info.Networks[name] = net.IPAddress
				}
			}
		}

		result = append(result, info)
	}

	return result, nil
}

// GetContainer returns a specific container by ID.
func (p *DockerProvider) GetContainer(ctx context.Context, id string) (*types.ContainerInfo, error) {
	if p.client == nil {
		return nil, fmt.Errorf("Docker client not connected")
	}

	c, err := p.client.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	info := &types.ContainerInfo{
		ID:       c.ID,
		Name:     strings.TrimPrefix(c.Name, "/"),
		Image:    c.Config.Image,
		Labels:   c.Config.Labels,
		State:    c.State.Status,
		Created:  time.Time{},
		Networks: make(map[string]string),
	}

	// Parse created time
	if created, err := time.Parse(time.RFC3339Nano, c.Created); err == nil {
		info.Created = created
	}

	// Parse started time
	if c.State.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil {
			info.Started = started
		}
	}

	// Extract network IPs
	if c.NetworkSettings != nil {
		for name, net := range c.NetworkSettings.Networks {
			if net.IPAddress != "" {
				info.Networks[name] = net.IPAddress
			}
		}
	}

	return info, nil
}

// Watch starts watching for container events.
func (p *DockerProvider) Watch(ctx context.Context, events chan<- *types.ContainerEvent) error {
	if p.client == nil {
		return fmt.Errorf("Docker client not connected")
	}

	// Start event stream
	msgChan, errChan := p.client.Events(ctx, dockerevents.ListOptions{})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errChan:
			if err != nil {
				return fmt.Errorf("Docker event error: %w", err)
			}
		case msg := <-msgChan:
			// Only handle container events
			if msg.Type != "container" {
				continue
			}

			// Convert to our event type
			var eventType types.EventType
			switch msg.Action {
			case "start":
				eventType = types.EventStart
			case "stop":
				eventType = types.EventStop
			case "die":
				eventType = types.EventDie
			case "destroy":
				eventType = types.EventDestroy
			default:
				continue // Ignore other events
			}

			event := &types.ContainerEvent{
				Type:          eventType,
				ContainerID:   msg.Actor.ID,
				ContainerName: msg.Actor.Attributes["name"],
				Labels:        msg.Actor.Attributes,
				Timestamp:     time.Unix(msg.Time, msg.TimeNano),
			}

			// Apply filter if configured
			if p.config.FilterLabel != "" {
				if !matchesFilter(event.Labels, p.config.FilterLabel) {
					continue
				}
			}

			select {
			case events <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// createHTTPClient creates an HTTP client for TCP connections.
func (p *DockerProvider) createHTTPClient() (*http.Client, error) {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	// Configure TLS if certificates are provided
	if p.config.TLS.CA != "" || p.config.TLS.Cert != "" {
		tlsConfig := &tls.Config{}

		// Load CA cert
		if p.config.TLS.CA != "" {
			caCert, err := os.ReadFile(p.config.TLS.CA)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA cert: %w", err)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(caCert)
			tlsConfig.RootCAs = caCertPool
		}

		// Load client cert
		if p.config.TLS.Cert != "" && p.config.TLS.Key != "" {
			cert, err := tls.LoadX509KeyPair(p.config.TLS.Cert, p.config.TLS.Key)
			if err != nil {
				return nil, fmt.Errorf("failed to load client cert: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}

		transport.TLSClientConfig = tlsConfig
	}

	return &http.Client{Transport: transport}, nil
}

// createSSHClient creates an HTTP client that tunnels through SSH.
func (p *DockerProvider) createSSHClient(ctx context.Context) (*http.Client, error) {
	endpoint := p.config.Endpoint
	
	// Parse SSH URL: ssh://user@host:port
	sshURL := strings.TrimPrefix(endpoint, "ssh://")
	parts := strings.Split(sshURL, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid SSH URL format: %s", endpoint)
	}

	user := parts[0]
	hostPort := parts[1]
	if !strings.Contains(hostPort, ":") {
		hostPort += ":22"
	}

	// Read SSH key
	keyPath := p.config.SSH.Key
	if keyPath == "" {
		home, _ := os.UserHomeDir()
		keyPath = home + "/.ssh/id_rsa"
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key: %w", err)
	}

	var signer ssh.Signer
	if p.config.SSH.KeyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(p.config.SSH.KeyPassphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH key: %w", err)
	}

	// SSH client config
	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: implement proper host key verification
		Timeout:         30 * time.Second,
	}

	// Connect to SSH server
	sshClient, err := ssh.Dial("tcp", hostPort, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SSH server: %w", err)
	}

	// Create HTTP transport that uses SSH connection
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Connect to Docker socket through SSH
			return sshClient.Dial("unix", "/var/run/docker.sock")
		},
	}

	return &http.Client{Transport: transport}, nil
}

// matchesFilter checks if labels match the filter.
func matchesFilter(labels map[string]string, filter string) bool {
	parts := strings.SplitN(filter, "=", 2)
	if len(parts) != 2 {
		return true // Invalid filter, match all
	}

	key, value := parts[0], parts[1]
	if v, ok := labels[key]; ok {
		if value == "" || v == value {
			return true
		}
	}
	return false
}
