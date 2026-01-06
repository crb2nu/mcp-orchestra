// Package registry manages MCP server discovery and tool inventory.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	kitgen "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/generator"
	kitreg "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"
	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/transport"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
	"gopkg.in/yaml.v3"
)

// HealthCheckTimeout is the timeout for individual server health checks.
const HealthCheckTimeout = 5 * time.Second

// Registry manages available MCP servers and their tools.
type Registry struct {
	servers  map[string]*types.ServerInfo
	tools    types.ToolInventory
	mu       sync.RWMutex
	filePath string
}

// New creates a new empty registry.
func New() *Registry {
	return &Registry{
		servers: make(map[string]*types.ServerInfo),
		tools:   make(types.ToolInventory),
	}
}

type LoadOptions struct {
	Target string
}

// LoadFromFile loads registry configuration from a YAML file.
func LoadFromFile(path string) (*Registry, error) {
	return LoadFromFileWithOptions(path, LoadOptions{})
}

// LoadFromFileWithOptions loads either the legacy orchestra registry schema or the
// fi-mcp/loom registry schema (versioned).
func LoadFromFileWithOptions(path string, opts LoadOptions) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read registry file: %w", err)
	}

	if opts.Target == "" {
		opts.Target = "codex"
	}

	var sniff struct {
		Version *int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &sniff); err != nil {
		return nil, fmt.Errorf("failed to parse registry YAML: %w", err)
	}

	// If a version is present, treat as fi-mcp/loom registry schema.
	if sniff.Version != nil {
		return loadFromFiRegistry(path, data, opts)
	}

	return loadFromOrchestraRegistry(path, data)
}

func loadFromOrchestraRegistry(path string, data []byte) (*Registry, error) {
	var config struct {
		Servers []types.ServerInfo `yaml:"servers"`
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse registry YAML: %w", err)
	}

	r := New()
	r.filePath = path

	for i := range config.Servers {
		server := &config.Servers[i]
		r.servers[server.Name] = server

		// Index tools by their full reference
		for _, tool := range server.Tools {
			ref := types.ToolRef{Server: server.Name, Tool: tool}
			r.tools[ref.String()] = ref
			// Also index by tool name alone (for single-server setups)
			if _, exists := r.tools[tool]; !exists {
				r.tools[tool] = ref
			}
		}
	}

	return r, nil
}

func loadFromFiRegistry(path string, data []byte, opts LoadOptions) (*Registry, error) {
	reg, err := kitreg.Load(path)
	if err != nil {
		return nil, err
	}
	reg.MergeDefaultAliases()

	repoRoot := kitreg.GetRepoRoot(path)

	r := New()
	r.filePath = path

	for _, srv := range reg.Servers {
		if srv == nil {
			continue
		}

		spec, err := reg.GetServerSpec(srv.Name, opts.Target)
		if err != nil || spec == nil {
			continue
		}

		endpoint, tools := fiSpecToEndpointAndTools(repoRoot, spec)
		if endpoint == "" {
			continue
		}

		server := types.ServerInfo{
			Name:     srv.Name,
			Endpoint: endpoint,
			Tools:    tools,
			Healthy:  true,
		}

		r.servers[server.Name] = &server

		for _, tool := range server.Tools {
			ref := types.ToolRef{Server: server.Name, Tool: tool}
			r.tools[ref.String()] = ref
			if _, exists := r.tools[tool]; !exists {
				r.tools[tool] = ref
			}
		}
	}

	return r, nil
}

func fiSpecToEndpointAndTools(repoRoot string, spec *kitreg.TargetSpec) (string, []string) {
	var tools []string
	for _, tool := range spec.Tools {
		if strings.TrimSpace(tool.Name) != "" {
			tools = append(tools, tool.Name)
		}
	}

	cmd := kitgen.ResolveCommand(spec.Command, repoRoot, "local")

	// If the spec explicitly declares a websocket transport (or looks like one),
	// treat Command as the endpoint URL.
	if strings.EqualFold(spec.Type, "websocket") ||
		strings.HasPrefix(cmd, "ws://") ||
		strings.HasPrefix(cmd, "wss://") {
		return cmd, tools
	}

	args := kitgen.ResolveArgs(spec.Args, repoRoot, "local")

	var env []string
	if len(spec.Env) > 0 {
		keys := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, fmt.Sprintf("%s=%s", k, kitgen.ResolveTokens(spec.Env[k], repoRoot, "local")))
		}
	}

	if cmd == "" {
		return "", nil
	}

	return transport.BuildStdioEndpoint(cmd, args, env, repoRoot), tools
}

// GetServer returns server info by name.
func (r *Registry) GetServer(name string) (*types.ServerInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	server, exists := r.servers[name]
	if !exists {
		return nil, fmt.Errorf("server not found: %s", name)
	}
	return server, nil
}

// GetTool returns the server location for a tool.
func (r *Registry) GetTool(name string) (types.ToolRef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ref, exists := r.tools[name]
	return ref, exists
}

// ListServers returns all registered servers.
func (r *Registry) ListServers() []types.ServerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	servers := make([]types.ServerInfo, 0, len(r.servers))
	for _, server := range r.servers {
		servers = append(servers, *server)
	}
	return servers
}

// ListTools returns the complete tool inventory.
func (r *Registry) ListTools() types.ToolInventory {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent mutation
	tools := make(types.ToolInventory, len(r.tools))
	for k, v := range r.tools {
		tools[k] = v
	}
	return tools
}

// RegisterServer adds or updates a server in the registry.
func (r *Registry) RegisterServer(server types.ServerInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.servers[server.Name] = &server

	// Update tool index
	for _, tool := range server.Tools {
		ref := types.ToolRef{Server: server.Name, Tool: tool}
		r.tools[ref.String()] = ref
	}
}

// UnregisterServer removes a server from the registry.
func (r *Registry) UnregisterServer(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	server, exists := r.servers[name]
	if !exists {
		return
	}

	// Remove tool references
	for _, tool := range server.Tools {
		ref := types.ToolRef{Server: name, Tool: tool}
		delete(r.tools, ref.String())
	}

	delete(r.servers, name)
}

// Refresh reloads the registry from file if it was loaded from one.
func (r *Registry) Refresh() error {
	if r.filePath == "" {
		return fmt.Errorf("registry was not loaded from file")
	}

	newReg, err := LoadFromFile(r.filePath)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.servers = newReg.servers
	r.tools = newReg.tools

	return nil
}

// HealthCheck pings all servers and updates their health status.
// It performs checks concurrently and returns a map of server names to health status.
func (r *Registry) HealthCheck(ctx context.Context) map[string]bool {
	// Get snapshot of servers to check
	r.mu.RLock()
	servers := make([]*types.ServerInfo, 0, len(r.servers))
	for _, s := range r.servers {
		servers = append(servers, s)
	}
	r.mu.RUnlock()

	results := make(map[string]bool, len(servers))
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	// Check servers concurrently
	for _, server := range servers {
		wg.Add(1)
		go func(s *types.ServerInfo) {
			defer wg.Done()

			healthy := r.checkServer(ctx, s.Endpoint)

			resultsMu.Lock()
			results[s.Name] = healthy
			resultsMu.Unlock()

			// Update server health status
			r.mu.Lock()
			if srv, ok := r.servers[s.Name]; ok {
				srv.Healthy = healthy
			}
			r.mu.Unlock()
		}(server)
	}

	wg.Wait()
	return results
}

// checkServer attempts to connect to a server and verify it's responsive.
func (r *Registry) checkServer(ctx context.Context, endpoint string) bool {
	ctx, cancel := context.WithTimeout(ctx, HealthCheckTimeout)
	defer cancel()

	// Attempt to dial the server
	t, err := transport.Dial(ctx, endpoint)
	if err != nil {
		return false
	}
	defer t.Close()

	// Send tools/list request to verify the connection works
	req, err := mcp.NewRequest(1, "tools/list", nil)
	if err != nil {
		return false
	}

	if err := t.Send(ctx, req); err != nil {
		return false
	}

	resp, err := t.Recv(ctx)
	if err != nil {
		return false
	}

	return resp.Error == nil
}

// ToolSchema represents a tool's full schema including description.
type ToolSchema struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

// DiscoverTools connects to a server and retrieves its full tool list with schemas.
// This is useful for getting detailed tool information for LLM planning.
func (r *Registry) DiscoverTools(ctx context.Context, serverName string) ([]ToolSchema, error) {
	server, err := r.GetServer(serverName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, HealthCheckTimeout*2)
	defer cancel()

	t, err := transport.Dial(ctx, server.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", serverName, err)
	}
	defer t.Close()

	// Send tools/list request
	req, err := mcp.NewRequest(1, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if err := t.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := t.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("recv response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	// Parse the tools list result
	var result mcp.ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	// Convert to ToolSchema
	schemas := make([]ToolSchema, len(result.Tools))
	for i, t := range result.Tools {
		schemas[i] = ToolSchema{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema.Properties,
		}
	}

	return schemas, nil
}

// DiscoverAllTools retrieves tool schemas from all registered servers.
func (r *Registry) DiscoverAllTools(ctx context.Context) (map[string][]ToolSchema, error) {
	servers := r.ListServers()
	result := make(map[string][]ToolSchema, len(servers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, server := range servers {
		wg.Add(1)
		go func(s types.ServerInfo) {
			defer wg.Done()

			tools, err := r.DiscoverTools(ctx, s.Name)
			if err != nil {
				// Log error but continue with other servers
				return
			}

			mu.Lock()
			result[s.Name] = tools
			mu.Unlock()
		}(server)
	}

	wg.Wait()
	return result, nil
}
