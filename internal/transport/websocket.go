package transport

import (
	"context"
	"fmt"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	// DefaultConnectTimeout is the default WebSocket connection timeout.
	DefaultConnectTimeout = 10 * time.Second
)

// Version is set at build time.
var Version = "dev"

// DialWebSocket creates an initialized WebSocket transport to an MCP server.
//
// It performs the full MCP initialization handshake before returning.
func DialWebSocket(ctx context.Context, url string) (mcp.Transport, error) {
	cfg := mcp.WebSocketConfig{
		URL:            url,
		ConnectTimeout: DefaultConnectTimeout,
		ClientInfo: mcp.ClientInfo{
			Name:    "mcp-orchestra",
			Version: Version,
		},
	}

	transport, err := mcp.NewWebSocketTransport(ctx, cfg, "")
	if err != nil {
		return nil, fmt.Errorf("websocket connect: %w", err)
	}

	// Perform MCP initialization handshake
	if err := transport.Initialize(ctx); err != nil {
		transport.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	return transport, nil
}

// DialWebSocketWithConfig creates a WebSocket transport with custom configuration.
func DialWebSocketWithConfig(ctx context.Context, cfg mcp.WebSocketConfig) (mcp.Transport, error) {
	if cfg.ClientInfo.Name == "" {
		cfg.ClientInfo.Name = "mcp-orchestra"
		cfg.ClientInfo.Version = Version
	}

	transport, err := mcp.NewWebSocketTransport(ctx, cfg, "")
	if err != nil {
		return nil, fmt.Errorf("websocket connect: %w", err)
	}

	if err := transport.Initialize(ctx); err != nil {
		transport.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	return transport, nil
}
