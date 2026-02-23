// Package agent provides Agent communication functionality.
package agent

import (
	"encoding/json"
	"time"

	"github.com/channinghe/labelgate/internal/types"
)

// MessageType defines the type of WebSocket message.
type MessageType string

const (
	// MessageTypeAuth is sent by agent to authenticate.
	MessageTypeAuth MessageType = "auth"
	// MessageTypeReport is sent by agent to report data.
	MessageTypeReport MessageType = "report"
	// MessageTypeQuery is sent by main to query agent.
	MessageTypeQuery MessageType = "query"
	// MessageTypeCommand is sent by main to send commands.
	MessageTypeCommand MessageType = "command"
	// MessageTypeResponse is a response to query/command.
	MessageTypeResponse MessageType = "response"
	// MessageTypeAck is an acknowledgment message.
	MessageTypeAck MessageType = "ack"
	// MessageTypeError is an error message.
	MessageTypeError MessageType = "error"
	// MessageTypeHeartbeat is a heartbeat/ping message.
	MessageTypeHeartbeat MessageType = "heartbeat"
)

// Message is the WebSocket message envelope.
type Message struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// AuthPayload is the authentication message payload.
type AuthPayload struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
	Version string `json:"version,omitempty"`
}

// ReportPayload is the data report message payload.
type ReportPayload struct {
	AgentID    string           `json:"agent_id"`
	Timestamp  time.Time        `json:"timestamp"`
	PublicIP   string           `json:"public_ip,omitempty"`
	Containers []*ContainerData `json:"containers"`
	Health     *AgentHealth     `json:"health"`
}

// ContainerData represents container data sent by agent.
type ContainerData struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	Status   string            `json:"status"`
	Labels   map[string]string `json:"labels"`
	Networks map[string]string `json:"networks,omitempty"`
	Created  time.Time         `json:"created"`
	Started  time.Time         `json:"started,omitempty"`
}

// AgentHealth represents agent health status.
type AgentHealth struct {
	DockerConnected bool   `json:"docker_connected"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	LastError       string `json:"last_error,omitempty"`
}

// QueryPayload is the query message payload.
type QueryPayload struct {
	Action string `json:"action"`
}

// QueryAction defines query actions.
const (
	QueryActionGetContainers = "get_containers"
	QueryActionGetIP         = "get_ip"
	QueryActionGetHealth     = "get_health"
)

// CommandPayload is the command message payload.
type CommandPayload struct {
	Action string `json:"action"`
}

// CommandAction defines command actions.
const (
	CommandActionRefresh   = "refresh"
	CommandActionReconnect = "reconnect"
)

// ResponsePayload is a generic response payload.
type ResponsePayload struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// ErrorPayload is an error message payload.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewMessage creates a new message with the given type and payload.
func NewMessage(msgType MessageType, payload interface{}) (*Message, error) {
	var payloadBytes json.RawMessage
	if payload != nil {
		var err error
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}

	return &Message{
		Type:    msgType,
		Payload: payloadBytes,
	}, nil
}

// NewMessageWithID creates a new message with request ID.
func NewMessageWithID(msgType MessageType, requestID string, payload interface{}) (*Message, error) {
	msg, err := NewMessage(msgType, payload)
	if err != nil {
		return nil, err
	}
	msg.RequestID = requestID
	return msg, nil
}

// ParsePayload parses the payload into the given type.
func (m *Message) ParsePayload(v interface{}) error {
	if m.Payload == nil {
		return nil
	}
	return json.Unmarshal(m.Payload, v)
}

// ConvertToContainerInfo converts ContainerData to ContainerInfo.
func (c *ContainerData) ConvertToContainerInfo() *types.ContainerInfo {
	return &types.ContainerInfo{
		ID:       c.ID,
		Name:     c.Name,
		Image:    c.Image,
		Labels:   c.Labels,
		Networks: c.Networks,
		State:    c.Status,
		Created:  c.Created,
		Started:  c.Started,
	}
}

// ContainerDataFromInfo converts ContainerInfo to ContainerData.
func ContainerDataFromInfo(info *types.ContainerInfo) *ContainerData {
	return &ContainerData{
		ID:       info.ID,
		Name:     info.Name,
		Image:    info.Image,
		Status:   info.State,
		Labels:   info.Labels,
		Networks: info.Networks,
		Created:  info.Created,
		Started:  info.Started,
	}
}
