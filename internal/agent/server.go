package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/reconciler"
	"github.com/channinghe/labelgate/internal/storage"
	"github.com/channinghe/labelgate/internal/types"
	"github.com/channinghe/labelgate/pkg/labels"
)

// AgentConnection represents a connected agent.
type AgentConnection struct {
	ID            string
	Name          string
	Conn          *websocket.Conn
	Connected     bool
	LastSeen      time.Time
	PublicIP      string
	DefaultTunnel string
	send          chan *Message
	done          chan struct{}
}

// Server is the WebSocket server for agent connections.
type Server struct {
	config      *config.AgentServerConfig
	agents      map[string]*AgentConfigEntry // agentID -> config (from main config)
	connections map[string]*AgentConnection  // agentID -> connection
	reconciler  *reconciler.Reconciler
	storage     storage.Storage
	parser      *labels.Parser
	mu          sync.RWMutex
	upgrader    websocket.Upgrader
}

// NewServer creates a new agent server.
func NewServer(cfg *config.AgentServerConfig, agentConfigs map[string]*AgentConfigEntry, rec *reconciler.Reconciler, store storage.Storage, labelPrefix string) *Server {
	agents := make(map[string]*AgentConfigEntry)
	for id, ac := range agentConfigs {
		agents[id] = ac
	}

	return &Server{
		config:      cfg,
		agents:      agents,
		connections: make(map[string]*AgentConnection),
		reconciler:  rec,
		storage:     store,
		parser:      labels.NewParser(labelPrefix),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Accept all origins for now
			},
		},
	}
}

// AgentConfigEntry holds agent configuration entry.
type AgentConfigEntry struct {
	Token         string
	DefaultTunnel string
	ConnectTo     string
}

// Start starts the WebSocket server.
func (s *Server) Start(ctx context.Context) error {
	if !s.config.Enabled {
		log.Info().Msg("Agent server disabled")
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)

	server := &http.Server{
		Addr:    s.config.Listen,
		Handler: mux,
	}

	// Configure TLS if provided
	if s.config.TLS.Cert != "" && s.config.TLS.Key != "" {
		cert, err := tls.LoadX509KeyPair(s.config.TLS.Cert, s.config.TLS.Key)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	// Start server in goroutine
	go func() {
		log.Info().Str("address", s.config.Listen).Msg("Starting agent server")
		var err error
		if server.TLSConfig != nil {
			err = server.ListenAndServeTLS("", "")
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Agent server error")
		}
	}()

	// Wait for shutdown
	<-ctx.Done()

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Close all connections
	s.mu.Lock()
	for _, conn := range s.connections {
		close(conn.done)
		conn.Conn.Close()
	}
	s.mu.Unlock()

	return server.Shutdown(shutdownCtx)
}

// handleWebSocket handles WebSocket connection upgrade.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade connection")
		return
	}

	// Wait for auth message
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		log.Error().Err(err).Msg("Failed to read auth message")
		conn.Close()
		return
	}

	var msg Message
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		log.Error().Err(err).Msg("Failed to parse auth message")
		conn.Close()
		return
	}

	if msg.Type != MessageTypeAuth {
		log.Error().Str("type", string(msg.Type)).Msg("Expected auth message")
		conn.Close()
		return
	}

	var auth AuthPayload
	if err := msg.ParsePayload(&auth); err != nil {
		log.Error().Err(err).Msg("Failed to parse auth payload")
		conn.Close()
		return
	}

	// Lock for both agent config lookup/write and connection registration.
	// s.agents and s.connections are both guarded by s.mu.
	s.mu.Lock()

	// Validate token: check named agents first, then accept_token
	agentConfig, ok := s.agents[auth.AgentID]
	if ok {
		// Named agent: validate specific token
		if agentConfig.Token != auth.Token {
			s.mu.Unlock()
			log.Warn().Str("agent_id", auth.AgentID).Msg("Invalid token for named agent")
			s.sendError(conn, "auth_failed", "Invalid token")
			conn.Close()
			return
		}
	} else if s.config.AcceptToken != "" && auth.Token == s.config.AcceptToken {
		// Dynamic agent: accept_token matches, register on the fly
		agentConfig = &AgentConfigEntry{
			Token:         auth.Token,
			DefaultTunnel: "default",
		}
		s.agents[auth.AgentID] = agentConfig
		log.Info().Str("agent_id", auth.AgentID).Msg("Dynamically registered agent via accept_token")
	} else {
		s.mu.Unlock()
		log.Warn().Str("agent_id", auth.AgentID).Msg("Unknown agent and no accept_token match")
		s.sendError(conn, "auth_failed", "Unknown agent")
		conn.Close()
		return
	}

	// Authentication successful — register connection while still holding the lock
	conn.SetReadDeadline(time.Time{}) // Remove deadline

	agentConn := &AgentConnection{
		ID:            auth.AgentID,
		Conn:          conn,
		Connected:     true,
		LastSeen:      time.Now(),
		DefaultTunnel: agentConfig.DefaultTunnel,
		send:          make(chan *Message, 100),
		done:          make(chan struct{}),
	}

	if existing, ok := s.connections[auth.AgentID]; ok {
		close(existing.done)
		existing.Conn.Close()
	}
	s.connections[auth.AgentID] = agentConn
	s.mu.Unlock()

	log.Info().
		Str("agent_id", auth.AgentID).
		Str("remote", conn.RemoteAddr().String()).
		Msg("Agent connected")

	// Persist agent to storage
	if s.storage != nil {
		now := time.Now()
		if err := s.storage.SaveAgent(context.Background(), &storage.Agent{
			ID:            auth.AgentID,
			Name:          auth.AgentID,
			Connected:     true,
			LastSeen:      &now,
			DefaultTunnel: agentConfig.DefaultTunnel,
			Status:        storage.AgentStatusActive,
			CreatedAt:     now,
			UpdatedAt:     now,
		}); err != nil {
			log.Error().Err(err).Str("agent_id", auth.AgentID).Msg("Failed to persist agent to storage")
		}
	}

	// Send ack
	s.sendAck(conn, msg.RequestID)

	// Start read/write goroutines
	go s.readPump(agentConn)
	go s.writePump(agentConn)
}

// readPump reads messages from the agent.
func (s *Server) readPump(agent *AgentConnection) {
	defer func() {
		s.handleDisconnect(agent)
	}()

	agent.Conn.SetReadLimit(1024 * 1024) // 1MB max message size
	agent.Conn.SetPongHandler(func(string) error {
		agent.LastSeen = time.Now()
		return nil
	})

	for {
		select {
		case <-agent.done:
			return
		default:
		}

		_, msgBytes, err := agent.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Str("agent", agent.ID).Msg("Read error")
			}
			return
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			log.Error().Err(err).Str("agent", agent.ID).Msg("Failed to parse message")
			continue
		}

		agent.LastSeen = time.Now()
		s.handleMessage(agent, &msg)
	}
}

// writePump writes messages to the agent.
func (s *Server) writePump(agent *AgentConnection) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-agent.done:
			return

		case msg := <-agent.send:
			agent.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := agent.Conn.WriteJSON(msg); err != nil {
				log.Error().Err(err).Str("agent", agent.ID).Msg("Write error")
				return
			}

		case <-ticker.C:
			agent.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := agent.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles incoming messages from agent.
func (s *Server) handleMessage(agent *AgentConnection, msg *Message) {
	switch msg.Type {
	case MessageTypeReport:
		s.handleReport(agent, msg)

	case MessageTypeResponse:
		// Handle query/command responses
		log.Debug().
			Str("agent", agent.ID).
			Str("request_id", msg.RequestID).
			Msg("Received response")

	case MessageTypeHeartbeat:
		s.sendAck(agent.Conn, msg.RequestID)

	default:
		log.Warn().
			Str("agent", agent.ID).
			Str("type", string(msg.Type)).
			Msg("Unknown message type")
	}
}

// handleReport handles agent data report.
func (s *Server) handleReport(agent *AgentConnection, msg *Message) {
	var report ReportPayload
	if err := msg.ParsePayload(&report); err != nil {
		log.Error().Err(err).Str("agent", agent.ID).Msg("Failed to parse report")
		return
	}

	agent.PublicIP = report.PublicIP
	agent.LastSeen = report.Timestamp

	// Parse containers and update reconciler
	var parsedContainers []*types.ParsedContainer
	for _, cd := range report.Containers {
		containerInfo := cd.ConvertToContainerInfo()
		parsed := s.parseContainerLabels(containerInfo, agent.ID)
		if parsed != nil {
			parsedContainers = append(parsedContainers, parsed)
		}
	}

	// Update reconciler with agent data
	if s.reconciler != nil {
		s.reconciler.UpdateAgentData(agent.ID, parsedContainers)
	}

	// Update agent metadata in storage
	if s.storage != nil {
		now := time.Now()
		if err := s.storage.SaveAgent(context.Background(), &storage.Agent{
			ID:        agent.ID,
			Name:      agent.Name,
			Connected: true,
			LastSeen:  &now,
			PublicIP:  report.PublicIP,
			Status:    storage.AgentStatusActive,
			UpdatedAt: now,
		}); err != nil {
			log.Error().Err(err).Str("agent", agent.ID).Msg("Failed to update agent metadata in storage")
		}
	}

	log.Debug().
		Str("agent", agent.ID).
		Int("containers", len(report.Containers)).
		Msg("Received report")

	s.sendAck(agent.Conn, msg.RequestID)
}

// parseContainerLabels parses container labels.
func (s *Server) parseContainerLabels(container *types.ContainerInfo, agentID string) *types.ParsedContainer {
	result := s.parser.Parse(container.Labels)

	// Log any parse errors
	for _, err := range result.Errors {
		log.Warn().
			Err(err).
			Str("container", container.Name).
			Msg("Label parsing error")
	}

	// Check hostname conflicts
	if err := s.parser.CheckHostnameConflict(result); err != nil {
		log.Error().
			Err(err).
			Str("container", container.Name).
			Msg("Hostname conflict detected")
	}

	if len(result.DNSServices) == 0 && len(result.TunnelServices) == 0 && len(result.AccessPolicies) == 0 {
		return nil
	}

	return &types.ParsedContainer{
		Info:           container,
		DNSServices:    result.DNSServices,
		TunnelServices: result.TunnelServices,
		AccessPolicies: result.AccessPolicies,
		AgentID:        agentID,
	}
}

// handleDisconnect handles agent disconnection.
func (s *Server) handleDisconnect(agent *AgentConnection) {
	s.mu.Lock()
	if conn, ok := s.connections[agent.ID]; ok && conn == agent {
		delete(s.connections, agent.ID)
	}
	s.mu.Unlock()

	agent.Connected = false
	agent.Conn.Close()

	// Remove agent data from reconciler
	if s.reconciler != nil {
		s.reconciler.RemoveAgentData(agent.ID)
	}

	// Update agent status in storage
	if s.storage != nil {
		if err := s.storage.UpdateAgentStatus(context.Background(), agent.ID, false, storage.AgentStatusDisconnected); err != nil {
			log.Error().Err(err).Str("agent", agent.ID).Msg("Failed to update agent disconnect status in storage")
		}
	}

	log.Info().Str("agent", agent.ID).Msg("Agent disconnected")
}

// handleHealth handles health check requests.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	s.mu.RLock()
	agents := make([]map[string]interface{}, 0, len(s.connections))
	for _, conn := range s.connections {
		agents = append(agents, map[string]interface{}{
			"id":        conn.ID,
			"connected": conn.Connected,
			"last_seen": conn.LastSeen,
			"public_ip": conn.PublicIP,
		})
	}
	s.mu.RUnlock()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"agents": agents,
	})
}

// sendAck sends an acknowledgment message.
func (s *Server) sendAck(conn *websocket.Conn, requestID string) {
	msg, _ := NewMessageWithID(MessageTypeAck, requestID, nil)
	conn.WriteJSON(msg)
}

// sendError sends an error message.
func (s *Server) sendError(conn *websocket.Conn, code, message string) {
	msg, _ := NewMessage(MessageTypeError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
	conn.WriteJSON(msg)
}

// SendQuery sends a query to an agent.
func (s *Server) SendQuery(agentID string, action string) error {
	s.mu.RLock()
	agent, ok := s.connections[agentID]
	s.mu.RUnlock()

	if !ok || !agent.Connected {
		return fmt.Errorf("agent not connected: %s", agentID)
	}

	msg, err := NewMessageWithID(MessageTypeQuery, uuid.New().String(), &QueryPayload{
		Action: action,
	})
	if err != nil {
		return err
	}

	select {
	case agent.send <- msg:
		return nil
	default:
		return fmt.Errorf("agent send buffer full")
	}
}

// SendCommand sends a command to an agent.
func (s *Server) SendCommand(agentID string, action string) error {
	s.mu.RLock()
	agent, ok := s.connections[agentID]
	s.mu.RUnlock()

	if !ok || !agent.Connected {
		return fmt.Errorf("agent not connected: %s", agentID)
	}

	msg, err := NewMessageWithID(MessageTypeCommand, uuid.New().String(), &CommandPayload{
		Action: action,
	})
	if err != nil {
		return err
	}

	select {
	case agent.send <- msg:
		return nil
	default:
		return fmt.Errorf("agent send buffer full")
	}
}

// GetConnectedAgents returns list of connected agents.
func (s *Server) GetConnectedAgents() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]string, 0, len(s.connections))
	for id, conn := range s.connections {
		if conn.Connected {
			agents = append(agents, id)
		}
	}
	return agents
}

// IsAgentConnected checks if an agent is connected.
func (s *Server) IsAgentConnected(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if conn, ok := s.connections[agentID]; ok {
		return conn.Connected
	}
	return false
}
