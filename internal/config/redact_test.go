package config

import (
	"strings"
	"testing"
)

const leakedSecret = "s3cr3t-pw"

func TestRedactURL(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{"parsable with password", "wss://user:" + leakedSecret + "@host:28112/ws", "wss://user:xxxxx@host:28112/ws"},
		{"parsable without userinfo", "wss://host/ws", "wss://host/ws"},
		{"unparsable keeps no userinfo", "wss://user:" + leakedSecret + "@host:bad/ws", "wss://***@host:bad/ws"},
		{"unparsable password containing at", "wss://user:p@ss@host:bad", "wss://***@host:bad"},
		{"unparsable without userinfo is unchanged", "tcp://[::1", "tcp://[::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURL(tt.raw)
			if got != tt.want {
				t.Errorf("redactURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if strings.Contains(got, leakedSecret) {
				t.Errorf("secret leaked: %q", got)
			}
		})
	}
}

// TestValidate_NeverEchoesURLCredentials covers every message that echoes a URL.
func TestValidate_NeverEchoesURLCredentials(t *testing.T) {
	userinfo := "user:" + leakedSecret + "@"
	cases := []struct {
		name   string
		mutate func(*Config)
		base   func() Config
	}{
		{"connect.endpoint bad scheme", func(c *Config) { c.Connect.Endpoint = "https://" + userinfo + "h/ws" }, validAgent},
		{"connect.endpoint without path", func(c *Config) { c.Connect.Endpoint = "wss://" + userinfo + "h:28112" }, validAgent},
		{"connect.endpoint unparsable", func(c *Config) { c.Connect.Endpoint = "wss://" + userinfo + "h:bad/ws" }, validAgent},
		{"connect_to bad scheme", func(c *Config) {
			c.Agent.Agents = map[string]AgentEntry{"web": {Token: agentToken, ConnectTo: "http://" + userinfo + "h/ws"}}
		}, validMain},
		{"connect_to unparsable", func(c *Config) {
			c.Agent.Agents = map[string]AgentEntry{"web": {Token: agentToken, ConnectTo: "wss://" + userinfo + "h:bad/ws"}}
		}, validMain},
		{"docker.endpoint unsupported scheme", func(c *Config) { c.Docker.Endpoint = "http://" + userinfo + "h:2375" }, validMain},
		{"docker.endpoint unparsable", func(c *Config) { c.Docker.Endpoint = "tcp://" + userinfo + "[::1" }, validMain},
		{"docker.endpoint tcp without port", func(c *Config) { c.Docker.Endpoint = "tcp://" + userinfo + "host" }, validMain},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.base()
			tc.mutate(&cfg)
			err := Validate(cfg)
			if err == nil {
				t.Fatal("want a validation error")
			}
			if strings.Contains(err.Error(), leakedSecret) {
				t.Errorf("secret leaked: %v", err)
			}
		})
	}
}

func TestValidate_NeverEchoesTokens(t *testing.T) {
	cfg := validMain()
	cfg.API.Token = leakedSecret // too short
	cfg.Agent.AcceptToken = leakedSecret
	cfg.Agent.Agents = map[string]AgentEntry{"web": {Token: leakedSecret}}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("want validation errors")
	}
	if strings.Contains(err.Error(), leakedSecret) {
		t.Errorf("token leaked: %v", err)
	}
	agent := validAgent()
	agent.Connect.Token = leakedSecret
	if err := Validate(agent); err == nil || strings.Contains(err.Error(), leakedSecret) {
		t.Errorf("connect.token: err=%v", err)
	}
}
