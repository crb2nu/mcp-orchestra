package transport

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// StdioConfig configures a stdio subprocess transport.
type StdioConfig struct {
	Command string   // Path to the MCP server binary
	Args    []string // Command-line arguments
	Env     []string // Environment variables (KEY=VALUE format)
	WorkDir string   // Working directory for the subprocess
}

// ParseStdioEndpoint parses a stdio endpoint URL into a StdioConfig.
//
// Format: stdio:///path/to/binary?arg=value&arg=other&env=KEY=VALUE&workdir=/path
//
// Query parameters:
//   - arg: Command-line argument (can be repeated)
//   - env: Environment variable in KEY=VALUE format (can be repeated)
//   - workdir: Working directory for the subprocess
func ParseStdioEndpoint(endpoint string) (*StdioConfig, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse stdio endpoint: %w", err)
	}

	if u.Scheme != "stdio" {
		return nil, fmt.Errorf("expected stdio scheme, got: %s", u.Scheme)
	}

	// The path is the binary location
	binaryPath := u.Path
	if binaryPath == "" {
		return nil, fmt.Errorf("stdio endpoint missing binary path")
	}

	cfg := &StdioConfig{
		Command: binaryPath,
	}

	// Parse query parameters
	query := u.Query()

	// Collect args
	cfg.Args = query["arg"]

	// Collect env vars
	cfg.Env = query["env"]

	// Working directory
	if workdir := query.Get("workdir"); workdir != "" {
		cfg.WorkDir = workdir
	}

	return cfg, nil
}

// StdioTransportWrapper wraps an MCP StdioTransport with subprocess lifecycle management.
type StdioTransportWrapper struct {
	transport *mcp.StdioTransport
	cmd       *exec.Cmd
	mu        sync.Mutex
	closed    bool
}

// Send delegates to the underlying transport.
func (w *StdioTransportWrapper) Send(ctx context.Context, msg *mcp.Message) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("transport closed")
	}
	w.mu.Unlock()
	return w.transport.Send(ctx, msg)
}

// Recv delegates to the underlying transport.
func (w *StdioTransportWrapper) Recv(ctx context.Context) (*mcp.Message, error) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil, fmt.Errorf("transport closed")
	}
	w.mu.Unlock()
	return w.transport.Recv(ctx)
}

// Close terminates the subprocess and cleans up resources.
func (w *StdioTransportWrapper) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	// Try graceful shutdown first
	if w.cmd.Process != nil {
		// Send SIGTERM for graceful shutdown
		w.cmd.Process.Signal(syscall.SIGTERM)

		// Wait a bit for graceful shutdown, then force kill
		done := make(chan error, 1)
		go func() {
			done <- w.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited gracefully
		default:
			// Force kill if still running
			w.cmd.Process.Kill()
			<-done
		}
	}

	return nil
}

// DialStdio spawns a subprocess MCP server and creates a transport to it.
//
// It performs the MCP initialization handshake before returning.
func DialStdio(ctx context.Context, cfg *StdioConfig) (mcp.Transport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("stdio config missing command")
	}

	// Verify the binary exists
	if _, err := os.Stat(cfg.Command); err != nil {
		return nil, fmt.Errorf("binary not found: %w", err)
	}

	// Create the command
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)

	// Set working directory
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}

	// Set environment: inherit current env + add custom vars
	cmd.Env = os.Environ()
	for _, env := range cfg.Env {
		cmd.Env = append(cmd.Env, env)
	}

	// Set up pipes for stdin/stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}

	// Capture stderr for debugging (optional)
	cmd.Stderr = os.Stderr

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start subprocess: %w", err)
	}

	// Create the stdio transport
	transport := mcp.NewStdioTransport(stdout, stdin)

	// Wrap with process management
	wrapper := &StdioTransportWrapper{
		transport: transport,
		cmd:       cmd,
	}

	// Perform MCP initialization handshake
	if err := initializeStdio(ctx, wrapper); err != nil {
		wrapper.Close()
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}

	return wrapper, nil
}

// initializeStdio performs the MCP initialization handshake over stdio.
func initializeStdio(ctx context.Context, t mcp.Transport) error {
	// Send initialize request
	initReq, err := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities:    mcp.Capabilities{},
		ClientInfo: mcp.ClientInfo{
			Name:    "mcp-orchestra",
			Version: Version,
		},
	})
	if err != nil {
		return fmt.Errorf("create init request: %w", err)
	}

	if err := t.Send(ctx, initReq); err != nil {
		return fmt.Errorf("send init: %w", err)
	}

	// Receive init response
	resp, err := t.Recv(ctx)
	if err != nil {
		return fmt.Errorf("recv init: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("init error: %s", resp.Error.Message)
	}

	// Send initialized notification
	initNotif := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		Method:  "notifications/initialized",
	}
	if err := t.Send(ctx, initNotif); err != nil {
		return fmt.Errorf("send initialized: %w", err)
	}

	return nil
}

// BuildStdioEndpoint constructs a stdio endpoint URL from components.
func BuildStdioEndpoint(binary string, args []string, env []string, workdir string) string {
	u := &url.URL{
		Scheme: "stdio",
		Path:   binary,
	}

	query := url.Values{}
	for _, arg := range args {
		query.Add("arg", arg)
	}
	for _, e := range env {
		query.Add("env", e)
	}
	if workdir != "" {
		query.Set("workdir", workdir)
	}

	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	return u.String()
}

// IsStdioEndpoint returns true if the endpoint is a stdio endpoint.
func IsStdioEndpoint(endpoint string) bool {
	return strings.HasPrefix(endpoint, "stdio://")
}
