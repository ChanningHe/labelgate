package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/provider"
)

// Client is the WebSocket client for agent outbound mode.
// Agent dials Main's WebSocket server.
type Client struct {
	agentCore
}

// NewClient creates a new agent client for outbound mode.
func NewClient(cfg *config.Config, prov provider.Provider) *Client {
	return &Client{
		agentCore: newAgentCore(cfg, prov),
	}
}

// Run starts the agent client with reconnection loop.
// Retry behaviour is driven by cfg.Retry:
//   - Attempts: max connection attempts (0 = infinite)
//   - Delay:    initial retry delay
//   - MaxDelay: cap after exponential backoff
//   - Backoff:  delay multiplier per attempt
func (c *Client) Run(ctx context.Context) error {
	// Connect to Docker provider
	if err := c.provider.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to provider: %w", err)
	}
	defer c.provider.Close()

	retryCount := 0
	currentDelay := c.config.Retry.Delay

	// Connection loop with configurable retry
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := c.connect(ctx); err != nil {
			c.lastError = err.Error()
			retryCount++

			// Check max attempts (0 = infinite)
			if c.config.Retry.Attempts > 0 && retryCount >= c.config.Retry.Attempts {
				return fmt.Errorf("max retry attempts (%d) reached: %w", c.config.Retry.Attempts, err)
			}

			log.Error().Err(err).
				Int("attempt", retryCount).
				Dur("next_delay", currentDelay).
				Msg("Connection failed, retrying...")

			time.Sleep(currentDelay)

			// Apply exponential backoff capped at MaxDelay
			nextDelay := time.Duration(float64(currentDelay) * c.config.Retry.Backoff)
			if c.config.Retry.MaxDelay > 0 && nextDelay > c.config.Retry.MaxDelay {
				nextDelay = c.config.Retry.MaxDelay
			}
			currentDelay = nextDelay
			continue
		}

		// Connection successful — reset retry state
		retryCount = 0
		currentDelay = c.config.Retry.Delay

		// Run communication loop (blocks until disconnect)
		c.runLoop(ctx)
	}
}

// connect establishes WebSocket connection to main instance.
func (c *Client) connect(ctx context.Context) error {
	endpoint := c.config.Connect.Endpoint
	if endpoint == "" {
		return fmt.Errorf("no endpoint configured")
	}

	log.Info().Str("endpoint", endpoint).Msg("Connecting to main instance")

	dialer := websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // TODO: make configurable
	}

	// Dial with timeout
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(dialCtx, endpoint, http.Header{})
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Authenticate
	if err := c.authenticate(conn); err != nil {
		conn.Close()
		return err
	}

	c.setConn(conn)
	return nil
}

// IsConnected returns whether the client is connected.
func (c *Client) IsConnected() bool {
	return c.isConnected()
}
