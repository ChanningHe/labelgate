package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/storage"
)

// ConnectToInboundAgents starts goroutines to connect to all agents that have
// ConnectTo configured (inbound mode). Each connection runs in its own goroutine
// with automatic reconnection.
func (s *Server) ConnectToInboundAgents(ctx context.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for agentID, cfg := range s.agents {
		if cfg.ConnectTo == "" {
			continue
		}

		id := agentID
		endpoint := cfg.ConnectTo
		token := cfg.Token

		go s.connectToInboundAgent(ctx, id, endpoint, token)
	}
}

// connectToInboundAgent connects to a single inbound agent with retry loop.
func (s *Server) connectToInboundAgent(ctx context.Context, expectedAgentID, endpoint, expectedToken string) {
	log.Info().
		Str("agent_id", expectedAgentID).
		Str("endpoint", endpoint).
		Msg("Starting inbound connection to agent")

	retryDelay := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		err := s.dialInboundAgent(ctx, expectedAgentID, endpoint, expectedToken)
		if err != nil {
			log.Error().
				Err(err).
				Str("agent_id", expectedAgentID).
				Str("endpoint", endpoint).
				Dur("retry_in", retryDelay).
				Msg("Inbound agent connection failed, retrying")

			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
			}

			// Backoff (cap at 30s)
			retryDelay = retryDelay * 2
			if retryDelay > 30*time.Second {
				retryDelay = 30 * time.Second
			}
			continue
		}

		// Connection succeeded and then closed; reset backoff
		retryDelay = time.Second
	}
}

// dialInboundAgent dials an inbound agent, authenticates, and runs the connection.
// Returns when the connection is closed.
func (s *Server) dialInboundAgent(ctx context.Context, expectedAgentID, endpoint, expectedToken string) error {
	dialer := websocket.DefaultDialer
	dialer.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // TODO: make configurable per agent
	}

	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	conn, _, err := dialer.DialContext(dialCtx, endpoint, http.Header{})
	if err != nil {
		return fmt.Errorf("failed to dial agent: %w", err)
	}

	log.Info().
		Str("agent_id", expectedAgentID).
		Str("endpoint", endpoint).
		Msg("Connected to inbound agent, waiting for auth")

	// Wait for auth message from agent
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to read auth from agent: %w", err)
	}

	var msg Message
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to parse auth message: %w", err)
	}

	if msg.Type != MessageTypeAuth {
		conn.Close()
		return fmt.Errorf("expected auth message, got: %s", msg.Type)
	}

	var auth AuthPayload
	if err := msg.ParsePayload(&auth); err != nil {
		conn.Close()
		return fmt.Errorf("failed to parse auth payload: %w", err)
	}

	// Validate: agent ID must match expected, and token must match
	if auth.AgentID != expectedAgentID {
		sendErrorMsg(conn, "auth_failed", fmt.Sprintf("unexpected agent ID: %s", auth.AgentID))
		conn.Close()
		return fmt.Errorf("agent ID mismatch: expected %s, got %s", expectedAgentID, auth.AgentID)
	}

	if auth.Token != expectedToken {
		sendErrorMsg(conn, "auth_failed", "Invalid token")
		conn.Close()
		return fmt.Errorf("invalid token for agent %s", expectedAgentID)
	}

	// Auth successful, send ack
	conn.SetReadDeadline(time.Time{})

	ackMsg, _ := NewMessageWithID(MessageTypeAck, msg.RequestID, nil)
	if err := conn.WriteJSON(ackMsg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send ack: %w", err)
	}

	// Get default tunnel from config
	s.mu.RLock()
	agentCfg := s.agents[expectedAgentID]
	defaultTunnel := "default"
	if agentCfg != nil && agentCfg.DefaultTunnel != "" {
		defaultTunnel = agentCfg.DefaultTunnel
	}
	s.mu.RUnlock()

	// Register connection
	agentConn := &AgentConnection{
		ID:            auth.AgentID,
		Conn:          conn,
		Connected:     true,
		LastSeen:      time.Now(),
		DefaultTunnel: defaultTunnel,
		send:          make(chan *Message, 100),
		done:          make(chan struct{}),
	}

	s.mu.Lock()
	if existing, ok := s.connections[auth.AgentID]; ok {
		close(existing.done)
		existing.Conn.Close()
	}
	s.connections[auth.AgentID] = agentConn
	s.mu.Unlock()

	log.Info().
		Str("agent_id", auth.AgentID).
		Str("mode", "inbound").
		Msg("Inbound agent authenticated and registered")

	// Persist agent to storage
	if s.storage != nil {
		now := time.Now()
		if err := s.storage.SaveAgent(ctx, &storage.Agent{
			ID:            auth.AgentID,
			Name:          auth.AgentID,
			Connected:     true,
			LastSeen:      &now,
			DefaultTunnel: defaultTunnel,
			Status:        storage.AgentStatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			log.Error().Err(err).Str("agent_id", auth.AgentID).Msg("Failed to persist inbound agent to storage")
		}
	}

	// Run read/write pumps (blocks until disconnect)
	done := make(chan struct{})
	go func() {
		s.readPump(agentConn)
		close(done)
	}()
	go s.writePump(agentConn)

	// Wait for disconnect
	select {
	case <-done:
	case <-ctx.Done():
		agentConn.Conn.Close()
		<-done
	}

	return nil
}

// sendErrorMsg is a helper to send an error message on a raw connection.
func sendErrorMsg(conn *websocket.Conn, code, message string) {
	msg, _ := NewMessage(MessageTypeError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
	conn.WriteJSON(msg)
}
