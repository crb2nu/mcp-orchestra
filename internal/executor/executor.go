// Package executor handles parallel execution of task steps.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"gitlab.flexinfer.ai/libs/mcp-go/pool"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/planner"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/transport"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// requestIDCounter provides unique request IDs for MCP calls.
var requestIDCounter uint64

// Executor runs task DAGs with parallel step execution.
type Executor struct {
	registry *registry.Registry
	pool     *pool.Pool
	timeout  time.Duration
}

// Config holds executor configuration.
type Config struct {
	Registry    *registry.Registry
	MaxIdle     int           // Maximum idle connections per server (default: 2)
	MaxOpen     int           // Maximum open connections per server (default: 10)
	IdleTimeout time.Duration // Idle connection timeout (default: 5m)
	Timeout     time.Duration // Task execution timeout (default: 5m)
}

// New creates a new executor.
func New(cfg Config) *Executor {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = 2
	}
	if cfg.MaxOpen == 0 {
		cfg.MaxOpen = 10
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}

	reg := cfg.Registry

	// Create the connection pool with a dial function that looks up
	// server endpoints from the registry and creates transports.
	p := pool.New(pool.Config{
		MaxIdle:     cfg.MaxIdle,
		MaxOpen:     cfg.MaxOpen,
		IdleTimeout: cfg.IdleTimeout,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			server, err := reg.GetServer(serverName)
			if err != nil {
				return nil, fmt.Errorf("server not found: %s", serverName)
			}
			return transport.Dial(ctx, server.Endpoint)
		},
	})

	return &Executor{
		registry: reg,
		pool:     p,
		timeout:  cfg.Timeout,
	}
}

// Close shuts down the executor and closes all connections.
func (e *Executor) Close() error {
	return e.pool.Close()
}

// Execute runs a task and returns events on the provided channel.
func (e *Executor) Execute(ctx context.Context, task *types.Task, events chan<- types.TaskEvent) error {
	defer close(events)

	// Build execution plan
	plan, err := planner.BuildExecutionPlan(task)
	if err != nil {
		return fmt.Errorf("failed to build execution plan: %w", err)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// Track step outputs for interpolation
	outputs := &sync.Map{}

	// Execute waves sequentially, steps within wave in parallel
	for waveIdx, wave := range plan.Waves {
		if err := e.executeWave(ctx, task, wave, waveIdx, outputs, events); err != nil {
			task.Status = types.TaskStatusFailed
			task.Error = err.Error()
			now := time.Now()
			task.CompletedAt = &now
			events <- types.TaskEvent{
				Type:      "task_error",
				TaskID:    task.ID,
				Error:     err.Error(),
				Timestamp: time.Now(),
			}
			return err
		}
	}

	// Collect final result
	task.Status = types.TaskStatusCompleted
	now := time.Now()
	task.CompletedAt = &now

	// Aggregate outputs
	result := make(map[string]interface{})
	outputs.Range(func(key, value interface{}) bool {
		result[key.(string)] = value
		return true
	})
	task.Result = result

	events <- types.TaskEvent{
		Type:      "task_complete",
		TaskID:    task.ID,
		Output:    result,
		Timestamp: time.Now(),
	}

	return nil
}

// executeWave runs all steps in a wave concurrently.
func (e *Executor) executeWave(
	ctx context.Context,
	task *types.Task,
	wave []string,
	waveIdx int,
	outputs *sync.Map,
	events chan<- types.TaskEvent,
) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(wave))

	// Build step index
	stepIndex := make(map[string]*types.Step)
	for i := range task.Steps {
		stepIndex[task.Steps[i].ID] = &task.Steps[i]
	}

	for _, stepID := range wave {
		step := stepIndex[stepID]
		wg.Add(1)

		go func(s *types.Step) {
			defer wg.Done()

			// Emit step start event
			events <- types.TaskEvent{
				Type:      "step_start",
				TaskID:    task.ID,
				StepID:    s.ID,
				Tool:      s.Tool,
				Timestamp: time.Now(),
			}

			now := time.Now()
			s.StartedAt = &now
			s.Status = types.StepStatusRunning

			// Interpolate arguments from previous outputs
			args, err := e.interpolateArgs(s.Args, outputs)
			if err != nil {
				s.Status = types.StepStatusFailed
				s.Error = err.Error()
				errChan <- fmt.Errorf("step %s: %w", s.ID, err)
				events <- types.TaskEvent{
					Type:      "step_error",
					TaskID:    task.ID,
					StepID:    s.ID,
					Error:     err.Error(),
					Timestamp: time.Now(),
				}
				return
			}

			// Execute the step
			output, err := e.executeStep(ctx, s.Tool, args)
			s.Duration = time.Since(now)

			if err != nil {
				s.Status = types.StepStatusFailed
				s.Error = err.Error()
				errChan <- fmt.Errorf("step %s: %w", s.ID, err)
				events <- types.TaskEvent{
					Type:      "step_error",
					TaskID:    task.ID,
					StepID:    s.ID,
					Error:     err.Error(),
					Timestamp: time.Now(),
				}
				return
			}

			s.Status = types.StepStatusCompleted
			s.Output = output
			outputs.Store(s.ID, output)

			events <- types.TaskEvent{
				Type:      "step_complete",
				TaskID:    task.ID,
				StepID:    s.ID,
				Tool:      s.Tool,
				Output:    output,
				Timestamp: time.Now(),
			}
		}(step)
	}

	wg.Wait()
	close(errChan)

	// Collect any errors
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("wave %d failed with %d errors: %v", waveIdx, len(errs), errs[0])
	}

	return nil
}

// executeStep calls the MCP server to execute a tool.
func (e *Executor) executeStep(ctx context.Context, toolRef string, args map[string]interface{}) (interface{}, error) {
	ref := types.ParseToolRef(toolRef)

	// Get connection from pool (uses server name, not endpoint)
	conn, err := e.pool.Get(ctx, ref.Server)
	if err != nil {
		return nil, fmt.Errorf("get connection to %s: %w", ref.Server, err)
	}

	// Track whether we should discard the connection
	var callErr error
	defer func() {
		if callErr != nil {
			e.pool.Discard(conn)
		} else {
			e.pool.Put(conn)
		}
	}()

	// Create tool call request
	params := mcp.CallToolParams{
		Name:      ref.Tool,
		Arguments: convertArgs(args),
	}

	reqID := atomic.AddUint64(&requestIDCounter, 1)
	req, err := mcp.NewRequest(reqID, "tools/call", params)
	if err != nil {
		callErr = err
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Send request
	if err := conn.Transport.Send(ctx, req); err != nil {
		callErr = err
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Receive response
	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		callErr = err
		return nil, fmt.Errorf("recv response: %w", err)
	}

	// Check for JSON-RPC error
	if resp.Error != nil {
		return nil, fmt.Errorf("tool error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	// Parse the result
	var result mcp.CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}

	// Check if the tool returned an error
	if result.IsError {
		return nil, fmt.Errorf("tool returned error: %s", extractErrorText(result.Content))
	}

	return extractOutput(result.Content), nil
}

// extractOutput converts MCP Content array to a usable output value.
func extractOutput(content []mcp.Content) interface{} {
	if len(content) == 0 {
		return nil
	}

	// Single content item: try to extract as the most useful type
	if len(content) == 1 {
		c := content[0]
		switch c.Type {
		case "text":
			// Try to parse as JSON
			var v interface{}
			if json.Unmarshal([]byte(c.Text), &v) == nil {
				return v
			}
			return c.Text
		case "image", "resource":
			return map[string]interface{}{
				"type":     c.Type,
				"mimeType": c.MimeType,
				"data":     c.Data,
			}
		default:
			return c.Text
		}
	}

	// Multiple content items: return as array
	results := make([]interface{}, len(content))
	for i, c := range content {
		switch c.Type {
		case "text":
			var v interface{}
			if json.Unmarshal([]byte(c.Text), &v) == nil {
				results[i] = v
			} else {
				results[i] = c.Text
			}
		default:
			results[i] = map[string]interface{}{
				"type":     c.Type,
				"text":     c.Text,
				"mimeType": c.MimeType,
				"data":     c.Data,
			}
		}
	}
	return results
}

// extractErrorText extracts error text from MCP Content array.
func extractErrorText(content []mcp.Content) string {
	for _, c := range content {
		if c.Text != "" {
			return c.Text
		}
	}
	return "unknown error"
}

// convertArgs converts map[string]interface{} to map[string]any for MCP.
func convertArgs(args map[string]interface{}) map[string]any {
	if args == nil {
		return nil
	}
	result := make(map[string]any, len(args))
	for k, v := range args {
		result[k] = v
	}
	return result
}

// interpolateArgs replaces ${{ steps.X.output }} references with actual values.
func (e *Executor) interpolateArgs(args map[string]interface{}, outputs *sync.Map) (map[string]interface{}, error) {
	if args == nil {
		return nil, nil
	}

	result := make(map[string]interface{})

	for key, value := range args {
		switch v := value.(type) {
		case string:
			interpolated, err := e.interpolateString(v, outputs)
			if err != nil {
				return nil, err
			}
			result[key] = interpolated
		case map[string]interface{}:
			nested, err := e.interpolateArgs(v, outputs)
			if err != nil {
				return nil, err
			}
			result[key] = nested
		case []interface{}:
			arr, err := e.interpolateArray(v, outputs)
			if err != nil {
				return nil, err
			}
			result[key] = arr
		default:
			result[key] = value
		}
	}

	return result, nil
}

// interpolateArray handles arrays that may contain interpolation strings.
func (e *Executor) interpolateArray(arr []interface{}, outputs *sync.Map) ([]interface{}, error) {
	result := make([]interface{}, len(arr))
	for i, item := range arr {
		switch v := item.(type) {
		case string:
			interpolated, err := e.interpolateString(v, outputs)
			if err != nil {
				return nil, err
			}
			result[i] = interpolated
		case map[string]interface{}:
			nested, err := e.interpolateArgs(v, outputs)
			if err != nil {
				return nil, err
			}
			result[i] = nested
		case []interface{}:
			nested, err := e.interpolateArray(v, outputs)
			if err != nil {
				return nil, err
			}
			result[i] = nested
		default:
			result[i] = item
		}
	}
	return result, nil
}

// interpolateString handles ${{ steps.X.output }} syntax.
func (e *Executor) interpolateString(s string, outputs *sync.Map) (interface{}, error) {
	if len(s) < 10 {
		return s, nil
	}

	const prefix = "${{ steps."
	const suffix = ".output }}"

	// Check for exact match pattern
	if len(s) > len(prefix)+len(suffix) && s[:len(prefix)] == prefix && s[len(s)-len(suffix):] == suffix {
		stepID := s[len(prefix) : len(s)-len(suffix)]
		if output, ok := outputs.Load(stepID); ok {
			return output, nil
		}
		return nil, fmt.Errorf("output not found for step: %s", stepID)
	}

	if !strings.Contains(s, prefix) {
		return s, nil
	}

	var builder strings.Builder
	cursor := 0
	for {
		start := strings.Index(s[cursor:], prefix)
		if start == -1 {
			builder.WriteString(s[cursor:])
			break
		}

		start += cursor
		builder.WriteString(s[cursor:start])

		end := strings.Index(s[start+len(prefix):], suffix)
		if end == -1 {
			builder.WriteString(s[start:])
			break
		}

		end += start + len(prefix)
		stepID := s[start+len(prefix) : end]
		output, ok := outputs.Load(stepID)
		if !ok {
			return nil, fmt.Errorf("output not found for step: %s", stepID)
		}

		rendered, err := stringifyOutput(output)
		if err != nil {
			return nil, err
		}

		builder.WriteString(rendered)
		cursor = end + len(suffix)
	}

	return builder.String(), nil
}

func stringifyOutput(output interface{}) (string, error) {
	switch v := output.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v), nil
		}
		return string(encoded), nil
	}
}
