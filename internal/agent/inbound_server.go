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

// InboundListener is the WebSocket server for agent inbound mode.
// Agent listens and waits for Main to connect.
type InboundListener struct {
	agentCore
	upgrader websocket.Upgrader
}

// NewInboundListener creates a new agent inbound listener.
func NewInboundListener(cfg *config.Config, prov provider.Provider) *InboundListener {
	return &InboundListener{
		agentCore: newAgentCore(cfg, prov),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// Run starts the inbound listener with reconnection support.
func (l *InboundListener) Run(ctx context.Context) error {
	// Connect to Docker provider
	if err := l.provider.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to provider: %w", err)
	}
	defer l.provider.Close()

	listenAddr := l.config.Connect.Listen
	if listenAddr == "" {
		return fmt.Errorf("no listen address configured for inbound mode")
	}

	// Channel to receive WebSocket connections from the HTTP handler
	connChan := make(chan *websocket.Conn, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := l.upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Error().Err(err).Msg("Failed to upgrade inbound connection")
			return
		}
		// Send to channel; drop if already have a pending connection
		select {
		case connChan <- conn:
		default:
			log.Warn().Msg("Already have a pending connection, rejecting")
			conn.Close()
		}
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","agent_id":"%s","connected":%t}`, l.getAgentID(), l.isConnected())
	})

	server := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	// Configure TLS if provided
	if l.config.Connect.TLS.Cert != "" && l.config.Connect.TLS.Key != "" {
		cert, err := tls.LoadX509KeyPair(l.config.Connect.TLS.Cert, l.config.Connect.TLS.Key)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
	}

	// Start HTTP server in background
	go func() {
		log.Info().
			Str("address", listenAddr).
			Str("agent_id", l.getAgentID()).
			Msg("Agent inbound listener started, waiting for main to connect")

		var err error
		if server.TLSConfig != nil {
			err = server.ListenAndServeTLS("", "")
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("Inbound listener server error")
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	// Main loop: accept connections and run communication
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case conn := <-connChan:
			log.Info().
				Str("remote", conn.RemoteAddr().String()).
				Msg("Main instance connected")

			// Authenticate with Main
			if err := l.authenticate(conn); err != nil {
				log.Error().Err(err).Msg("Authentication failed")
				conn.Close()
				continue
			}

			l.setConn(conn)

			// Run communication loop (blocks until disconnect)
			l.runLoop(ctx)

			log.Info().Msg("Connection to main lost, waiting for reconnect...")
		}
	}
}

// IsConnected returns whether the listener has an active connection.
func (l *InboundListener) IsConnected() bool {
	return l.isConnected()
}
