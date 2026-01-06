// Package coordinator manages task lifecycle and state.
package coordinator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/executor"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/planner"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// Coordinator manages task submission, execution, and state tracking.
type Coordinator struct {
	registry *registry.Registry
	planner  planner.Planner
	executor *executor.Executor

	tasks      map[string]*types.Task
	tasksMu    sync.RWMutex
	maxHistory int
}

// Config holds coordinator configuration.
type Config struct {
	Registry      *registry.Registry
	Planner       planner.Planner
	ExecutorCfg   executor.Config
	MaxHistory    int // Maximum number of completed tasks to retain
}

// New creates a new coordinator.
func New(cfg Config) *Coordinator {
	if cfg.MaxHistory == 0 {
		cfg.MaxHistory = 100
	}
	if cfg.Planner == nil {
		cfg.Planner = planner.NewStaticPlanner()
	}

	cfg.ExecutorCfg.Registry = cfg.Registry

	return &Coordinator{
		registry:   cfg.Registry,
		planner:    cfg.Planner,
		executor:   executor.New(cfg.ExecutorCfg),
		tasks:      make(map[string]*types.Task),
		maxHistory: cfg.MaxHistory,
	}
}

// SubmitPrompt creates a task from a natural language prompt.
func (c *Coordinator) SubmitPrompt(ctx context.Context, prompt string) (*types.Task, error) {
	tools := c.registry.ListTools()

	task, err := c.planner.Plan(ctx, prompt, tools)
	if err != nil {
		return nil, fmt.Errorf("planning failed: %w", err)
	}

	return c.submit(task)
}

// SubmitDAG creates a task from an explicit DAG definition.
func (c *Coordinator) SubmitDAG(ctx context.Context, task *types.Task) (*types.Task, error) {
	tools := c.registry.ListTools()

	if err := c.planner.Validate(task, tools); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return c.submit(task)
}

// submit initializes and stores a task.
func (c *Coordinator) submit(task *types.Task) (*types.Task, error) {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}

	task.Status = types.TaskStatusPending
	task.CreatedAt = time.Now()

	for i := range task.Steps {
		task.Steps[i].Status = types.StepStatusPending
	}

	c.tasksMu.Lock()
	c.tasks[task.ID] = task
	c.pruneHistory()
	c.tasksMu.Unlock()

	return task, nil
}

// Execute runs a submitted task asynchronously.
func (c *Coordinator) Execute(ctx context.Context, taskID string) (<-chan types.TaskEvent, error) {
	c.tasksMu.RLock()
	task, exists := c.tasks[taskID]
	c.tasksMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != types.TaskStatusPending {
		return nil, fmt.Errorf("task already started: %s", task.Status)
	}

	task.Status = types.TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now

	events := make(chan types.TaskEvent, 100)

	go func() {
		err := c.executor.Execute(ctx, task, events)
		if err != nil {
			task.Status = types.TaskStatusFailed
			task.Error = err.Error()
		}
	}()

	return events, nil
}

// SubmitAndExecute combines submission and execution for convenience.
func (c *Coordinator) SubmitAndExecute(ctx context.Context, task *types.Task) (<-chan types.TaskEvent, error) {
	submitted, err := c.SubmitDAG(ctx, task)
	if err != nil {
		return nil, err
	}

	return c.Execute(ctx, submitted.ID)
}

// GetTask retrieves a task by ID.
func (c *Coordinator) GetTask(taskID string) (*types.Task, error) {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return task, nil
}

// ListTasks returns all tasks matching the filter.
func (c *Coordinator) ListTasks(status types.TaskStatus) []*types.Task {
	c.tasksMu.RLock()
	defer c.tasksMu.RUnlock()

	var tasks []*types.Task
	for _, task := range c.tasks {
		if status == "" || task.Status == status {
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// CancelTask attempts to cancel a running task.
func (c *Coordinator) CancelTask(taskID string) error {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != types.TaskStatusRunning && task.Status != types.TaskStatusPending {
		return fmt.Errorf("task cannot be cancelled: %s", task.Status)
	}

	task.Status = types.TaskStatusCancelled
	now := time.Now()
	task.CompletedAt = &now

	return nil
}

// pruneHistory removes old completed tasks to prevent memory growth.
func (c *Coordinator) pruneHistory() {
	if len(c.tasks) <= c.maxHistory {
		return
	}

	var oldest *types.Task
	for _, task := range c.tasks {
		if task.Status == types.TaskStatusCompleted || task.Status == types.TaskStatusFailed || task.Status == types.TaskStatusCancelled {
			if oldest == nil || task.CreatedAt.Before(oldest.CreatedAt) {
				oldest = task
			}
		}
	}

	if oldest != nil {
		delete(c.tasks, oldest.ID)
	}
}

// ListTools returns all available tools.
func (c *Coordinator) ListTools() types.ToolInventory {
	return c.registry.ListTools()
}

// ListServers returns all registered MCP servers.
func (c *Coordinator) ListServers() []types.ServerInfo {
	return c.registry.ListServers()
}

// RefreshRegistry reloads the registry from file.
func (c *Coordinator) RefreshRegistry() error {
	return c.registry.Refresh()
}

// Close shuts down the coordinator and releases resources.
func (c *Coordinator) Close() error {
	return c.executor.Close()
}
