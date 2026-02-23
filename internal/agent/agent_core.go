// Package agent provides Agent communication functionality.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/provider"
	"github.com/channinghe/labelgate/internal/version"
)

// agentCore contains shared state and logic for both outbound and inbound agent modes.
// Both Client (outbound) and InboundListener (inbound) embed this struct.
type agentCore struct {
	config    *config.Config
	provider  provider.Provider
	conn      *websocket.Conn
	send      chan *Message
	done      chan struct{}
	connected bool
	mu        sync.RWMutex
	startTime time.Time
	lastError string
}

// newAgentCore creates a new agent core.
func newAgentCore(cfg *config.Config, prov provider.Provider) agentCore {
	return agentCore{
		config:    cfg,
		provider:  prov,
		send:      make(chan *Message, 100),
		done:      make(chan struct{}),
		startTime: time.Now(),
	}
}

// setConn sets the WebSocket connection and marks connected.
func (a *agentCore) setConn(conn *websocket.Conn) {
	a.conn = conn
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
}

// authenticate sends auth message and waits for ack from Main.
func (a *agentCore) authenticate(conn *websocket.Conn) error {
	agentID := a.getAgentID()
	auth := &AuthPayload{
		AgentID: agentID,
		Token:   a.config.Connect.Token,
		Version: version.Version,
	}

	authMsg, _ := NewMessageWithID(MessageTypeAuth, uuid.New().String(), auth)
	if err := conn.WriteJSON(authMsg); err != nil {
		return fmt.Errorf("failed to send auth: %w", err)
	}

	// Wait for ack
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read auth response: %w", err)
	}

	var response Message
	if err := json.Unmarshal(msgBytes, &response); err != nil {
		return fmt.Errorf("failed to parse auth response: %w", err)
	}

	if response.Type == MessageTypeError {
		var errPayload ErrorPayload
		response.ParsePayload(&errPayload)
		return fmt.Errorf("auth failed: %s", errPayload.Message)
	}

	if response.Type != MessageTypeAck {
		return fmt.Errorf("unexpected response type: %s", response.Type)
	}

	conn.SetReadDeadline(time.Time{}) // Clear deadline
	log.Info().Str("agent_id", agentID).Msg("Authenticated with main instance")
	return nil
}

// runLoop runs the main communication loop after connection is established.
func (a *agentCore) runLoop(ctx context.Context) {
	// Create new done channel
	a.done = make(chan struct{})

	// Start read/write goroutines
	go a.readPump()
	go a.writePump()

	// Start periodic report
	reportTicker := time.NewTicker(a.config.Connect.HeartbeatInterval)
	defer reportTicker.Stop()

	// Send initial report
	a.sendReport()

	// Watch for container events
	eventsChan := make(chan struct{}, 10)
	go a.watchContainers(ctx, eventsChan)

	for {
		select {
		case <-ctx.Done():
			a.disconnect()
			return

		case <-a.done:
			log.Warn().Msg("Connection lost")
			return

		case <-reportTicker.C:
			a.sendReport()

		case <-eventsChan:
			a.sendReport()
		}
	}
}

// readPump reads messages from the main instance.
func (a *agentCore) readPump() {
	defer func() {
		a.disconnect()
	}()

	a.conn.SetReadLimit(1024 * 1024)
	a.conn.SetPongHandler(func(string) error {
		return nil
	})

	for {
		_, msgBytes, err := a.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Error().Err(err).Msg("Read error")
			}
			return
		}

		var msg Message
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			log.Error().Err(err).Msg("Failed to parse message")
			continue
		}

		a.handleMessage(&msg)
	}
}

// writePump writes messages to the main instance.
func (a *agentCore) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return

		case msg := <-a.send:
			a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := a.conn.WriteJSON(msg); err != nil {
				log.Error().Err(err).Msg("Write error")
				return
			}

		case <-ticker.C:
			a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := a.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles incoming messages from main instance.
func (a *agentCore) handleMessage(msg *Message) {
	switch msg.Type {
	case MessageTypeQuery:
		a.handleQuery(msg)
	case MessageTypeCommand:
		a.handleCommand(msg)
	case MessageTypeAck:
		log.Debug().Str("request_id", msg.RequestID).Msg("Received ack")
	default:
		log.Warn().Str("type", string(msg.Type)).Msg("Unknown message type")
	}
}

// handleQuery handles query messages.
func (a *agentCore) handleQuery(msg *Message) {
	var query QueryPayload
	if err := msg.ParsePayload(&query); err != nil {
		a.sendErrorResponse(msg.RequestID, "invalid_payload", err.Error())
		return
	}

	var data interface{}
	var err error

	switch query.Action {
	case QueryActionGetContainers:
		containers, e := a.provider.ListContainers(context.Background())
		if e != nil {
			err = e
		} else {
			var containerData []*ContainerData
			for _, container := range containers {
				containerData = append(containerData, ContainerDataFromInfo(container))
			}
			data = containerData
		}
	case QueryActionGetIP:
		data = map[string]string{"ip": a.getPublicIP()}
	case QueryActionGetHealth:
		data = a.getHealth()
	default:
		err = fmt.Errorf("unknown action: %s", query.Action)
	}

	if err != nil {
		a.sendErrorResponse(msg.RequestID, "query_failed", err.Error())
		return
	}
	a.sendResponse(msg.RequestID, data)
}

// handleCommand handles command messages.
func (a *agentCore) handleCommand(msg *Message) {
	var cmd CommandPayload
	if err := msg.ParsePayload(&cmd); err != nil {
		a.sendErrorResponse(msg.RequestID, "invalid_payload", err.Error())
		return
	}

	switch cmd.Action {
	case CommandActionRefresh:
		a.sendReport()
		a.sendResponse(msg.RequestID, map[string]bool{"success": true})
	case CommandActionReconnect:
		a.provider.Close()
		if err := a.provider.Connect(context.Background()); err != nil {
			a.sendErrorResponse(msg.RequestID, "reconnect_failed", err.Error())
			return
		}
		a.sendResponse(msg.RequestID, map[string]bool{"success": true})
	default:
		a.sendErrorResponse(msg.RequestID, "unknown_command", fmt.Sprintf("unknown command: %s", cmd.Action))
	}
}

// sendReport sends a data report to the main instance.
func (a *agentCore) sendReport() {
	containers, err := a.provider.ListContainers(context.Background())
	if err != nil {
		a.lastError = err.Error()
		log.Error().Err(err).Msg("Failed to list containers")
		return
	}

	var containerData []*ContainerData
	for _, container := range containers {
		containerData = append(containerData, ContainerDataFromInfo(container))
	}

	report := &ReportPayload{
		AgentID:    a.getAgentID(),
		Timestamp:  time.Now(),
		PublicIP:   a.getPublicIP(),
		Containers: containerData,
		Health:     a.getHealth(),
	}

	msg, _ := NewMessageWithID(MessageTypeReport, uuid.New().String(), report)

	select {
	case a.send <- msg:
	default:
		log.Warn().Msg("Send buffer full, dropping report")
	}
}

// sendResponse sends a response message.
func (a *agentCore) sendResponse(requestID string, data interface{}) {
	dataBytes, _ := json.Marshal(data)
	msg, _ := NewMessageWithID(MessageTypeResponse, requestID, &ResponsePayload{
		Success: true,
		Data:    dataBytes,
	})

	select {
	case a.send <- msg:
	default:
		log.Warn().Msg("Send buffer full, dropping response")
	}
}

// sendErrorResponse sends an error response.
func (a *agentCore) sendErrorResponse(requestID, code, message string) {
	msg, _ := NewMessageWithID(MessageTypeResponse, requestID, &ResponsePayload{
		Success: false,
		Error:   message,
	})

	select {
	case a.send <- msg:
	default:
		log.Warn().Msg("Send buffer full, dropping error response")
	}
}

// watchContainers watches for container changes and signals the channel.
func (a *agentCore) watchContainers(ctx context.Context, notify chan<- struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var lastCount int

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.done:
			return
		case <-ticker.C:
			containers, err := a.provider.ListContainers(ctx)
			if err != nil {
				continue
			}
			if len(containers) != lastCount {
				lastCount = len(containers)
				select {
				case notify <- struct{}{}:
				default:
				}
			}
		}
	}
}

// disconnect closes the connection.
func (a *agentCore) disconnect() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.connected {
		return
	}

	a.connected = false
	close(a.done)
	if a.conn != nil {
		a.conn.Close()
	}
}

// getAgentID returns the agent ID.
func (a *agentCore) getAgentID() string {
	if a.config.Connect.AgentID != "" {
		return a.config.Connect.AgentID
	}

	// Try to read machine-id
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil && len(data) >= 32 {
		return string(data[:32])
	}

	// Fallback to hostname
	hostname, _ := os.Hostname()
	return hostname
}

// getPublicIP returns the public IP address.
func (a *agentCore) getPublicIP() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

// getHealth returns the agent health status.
func (a *agentCore) getHealth() *AgentHealth {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()

	return &AgentHealth{
		DockerConnected: connected,
		UptimeSeconds:   int64(time.Since(a.startTime).Seconds()),
		LastError:       a.lastError,
	}
}

// isConnected returns whether the agent is connected.
func (a *agentCore) isConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}
