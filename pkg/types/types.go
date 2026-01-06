// Package types defines the core data structures for MCP Orchestra.
package types

import (
	"time"
)

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// StepStatus represents the current state of a step within a task.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
)

// Task represents a multi-step orchestration task.
type Task struct {
	ID          string            `json:"id" yaml:"id"`
	Name        string            `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt      string            `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Steps       []Step            `json:"steps" yaml:"steps"`
	Status      TaskStatus        `json:"status" yaml:"status"`
	Result      interface{}       `json:"result,omitempty" yaml:"result,omitempty"`
	Error       string            `json:"error,omitempty" yaml:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at" yaml:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
}

// Step represents a single step in a task DAG.
type Step struct {
	ID        string                 `json:"id" yaml:"id"`
	Tool      string                 `json:"tool" yaml:"tool"`
	Args      map[string]interface{} `json:"args,omitempty" yaml:"args,omitempty"`
	DependsOn []string               `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Status    StepStatus             `json:"status" yaml:"status"`
	Output    interface{}            `json:"output,omitempty" yaml:"output,omitempty"`
	Error     string                 `json:"error,omitempty" yaml:"error,omitempty"`
	StartedAt *time.Time             `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	Duration  time.Duration          `json:"duration,omitempty" yaml:"duration,omitempty"`
}

// ToolRef identifies a tool on an MCP server.
type ToolRef struct {
	Server string `json:"server" yaml:"server"` // MCP server name
	Tool   string `json:"tool" yaml:"tool"`     // Tool name within server
}

// ParseToolRef parses a tool reference string like "filesystem/read_file".
func ParseToolRef(s string) ToolRef {
	for i, c := range s {
		if c == '/' {
			return ToolRef{Server: s[:i], Tool: s[i+1:]}
		}
	}
	return ToolRef{Tool: s}
}

// String returns the tool reference as "server/tool".
func (t ToolRef) String() string {
	if t.Server == "" {
		return t.Tool
	}
	return t.Server + "/" + t.Tool
}

// TaskEvent represents a streaming event during task execution.
type TaskEvent struct {
	Type      string      `json:"type"`                 // step_start, step_complete, step_error, task_complete
	TaskID    string      `json:"task_id"`              // Parent task ID
	StepID    string      `json:"step_id,omitempty"`    // Step ID (for step events)
	Tool      string      `json:"tool,omitempty"`       // Tool being executed
	Output    interface{} `json:"output,omitempty"`     // Step output
	Error     string      `json:"error,omitempty"`      // Error message
	Timestamp time.Time   `json:"timestamp"`            // Event timestamp
}

// ExecutionPlan represents a planned execution strategy for a task.
type ExecutionPlan struct {
	TaskID string           `json:"task_id"`
	Waves  [][]string       `json:"waves"`  // Steps grouped by execution wave (parallel within wave)
	Graph  map[string][]string `json:"graph"` // Dependency graph (step -> dependencies)
}

// ServerInfo represents an MCP server in the registry.
type ServerInfo struct {
	Name     string   `json:"name" yaml:"name"`
	Endpoint string   `json:"endpoint" yaml:"endpoint"`
	Tools    []string `json:"tools,omitempty" yaml:"tools,omitempty"`
	Healthy  bool     `json:"healthy" yaml:"healthy"`
}

// Registry represents the collection of available MCP servers.
type Registry struct {
	Servers []ServerInfo `json:"servers" yaml:"servers"`
}

// ToolInventory maps tool names to their server locations.
type ToolInventory map[string]ToolRef
