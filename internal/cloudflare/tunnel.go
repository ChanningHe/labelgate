package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/channinghe/labelgate/internal/model"
)

// tunnelConfigWire is the configurations GET result; config stays raw so
// every field (catch-all, warp-routing, unknown keys) survives GET -> PUT.
type tunnelConfigWire struct {
	Config json.RawMessage `json:"config"`
}

func (c *Client) tunnelCred(t Tunnel) (*credEntry, error) {
	if t.ID == "" || t.AccountID == "" {
		return nil, fmt.Errorf("tunnel %q: missing tunnel or account ID: %w", t.Ref, model.ErrInvalid)
	}
	return c.credByName(t.Credential)
}

func tunnelConfigPath(t Tunnel) string {
	return fmt.Sprintf("accounts/%s/cfd_tunnel/%s/configurations", url.PathEscape(t.AccountID), url.PathEscape(t.ID))
}

// GetTunnelConfig returns the live remotely-managed configuration.
func (c *Client) GetTunnelConfig(ctx context.Context, t Tunnel) (TunnelConfig, error) {
	cr, err := c.tunnelCred(t)
	if err != nil {
		return TunnelConfig{}, err
	}
	var env envelope[*tunnelConfigWire]
	if err := c.call(ctx, cr, request{op: "tunnel.config.get", method: http.MethodGet, path: tunnelConfigPath(t), out: &env}); err != nil {
		return TunnelConfig{}, err
	}
	if env.Result == nil {
		return TunnelConfig{}, nil
	}
	raw := bytes.TrimSpace(env.Result.Config)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return TunnelConfig{}, nil
	}
	var cfg TunnelConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return TunnelConfig{}, &APIError{Op: "tunnel.config.get", Status: http.StatusOK, Messages: []string{"decode config: " + err.Error()}, Err: err}
	}
	return cfg, nil
}

// PutTunnelConfig replaces the configuration with cfg exactly as given.
func (c *Client) PutTunnelConfig(ctx context.Context, t Tunnel, cfg TunnelConfig) error {
	cr, err := c.tunnelCred(t)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("tunnel.config.put: encode config: %w", err)
	}
	body := make([]byte, 0, len(raw)+12)
	body = append(append(append(body, `{"config":`...), raw...), '}')
	var env envelope[json.RawMessage]
	return c.call(ctx, cr, request{op: "tunnel.config.put", method: http.MethodPut, path: tunnelConfigPath(t), body: body, out: &env})
}
