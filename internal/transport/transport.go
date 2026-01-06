// Package transport provides MCP transport creation and management.
//
// It supports multiple transport types:
//   - WebSocket (ws://, wss://) for remote MCP servers
//   - Stdio (stdio:///path/to/binary) for subprocess-based MCP servers
package transport

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// EndpointType represents the type of MCP server endpoint.
type EndpointType int

const (
	// EndpointWebSocket indicates a WebSocket endpoint (ws:// or wss://).
	EndpointWebSocket EndpointType = iota
	// EndpointStdio indicates a stdio subprocess endpoint (stdio://).
	EndpointStdio
)

// String returns the string representation of the endpoint type.
func (t EndpointType) String() string {
	switch t {
	case EndpointWebSocket:
		return "websocket"
	case EndpointStdio:
		return "stdio"
	default:
		return "unknown"
	}
}

// ParsedEndpoint contains the parsed components of an endpoint URL.
type ParsedEndpoint struct {
	Type     EndpointType
	URL      string       // For WebSocket: the full URL
	StdioCfg *StdioConfig // For Stdio: the subprocess configuration
}

// ParseEndpoint parses an endpoint URL and returns its type and configuration.
//
// Supported formats:
//   - ws://host:port/path - WebSocket
//   - wss://host:port/path - WebSocket with TLS
//   - stdio:///path/to/binary - Stdio subprocess
//   - stdio:///path/to/binary?arg=value - Stdio with arguments
func ParseEndpoint(endpoint string) (*ParsedEndpoint, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("empty endpoint")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	switch strings.ToLower(u.Scheme) {
	case "ws", "wss":
		return &ParsedEndpoint{
			Type: EndpointWebSocket,
			URL:  endpoint,
		}, nil

	case "stdio":
		cfg, err := ParseStdioEndpoint(endpoint)
		if err != nil {
			return nil, err
		}
		return &ParsedEndpoint{
			Type:     EndpointStdio,
			StdioCfg: cfg,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported endpoint scheme: %s (supported: ws, wss, stdio)", u.Scheme)
	}
}

// Dial creates a new MCP transport connection to the given endpoint.
//
// It automatically determines the transport type from the endpoint URL scheme
// and performs the MCP initialization handshake.
func Dial(ctx context.Context, endpoint string) (mcp.Transport, error) {
	parsed, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	switch parsed.Type {
	case EndpointWebSocket:
		return DialWebSocket(ctx, parsed.URL)
	case EndpointStdio:
		return DialStdio(ctx, parsed.StdioCfg)
	default:
		return nil, fmt.Errorf("unsupported endpoint type: %v", parsed.Type)
	}
}

// MustDial is like Dial but panics on error.
// Useful for initialization in main() where failure should be fatal.
func MustDial(ctx context.Context, endpoint string) mcp.Transport {
	t, err := Dial(ctx, endpoint)
	if err != nil {
		panic(fmt.Sprintf("transport.Dial(%q): %v", endpoint, err))
	}
	return t
}
