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
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/store"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// Coordinator manages task submission, execution, and state tracking.
type Coordinator struct {
	registry *registry.Registry
	planner  planner.Planner
	executor *executor.Executor
	store    store.TaskStore

	maxHistory int

	cancels   map[string]context.CancelFunc
	cancelsMu sync.Mutex
}

// Config holds coordinator configuration.
type Config struct {
	Registry    *registry.Registry
	Planner     planner.Planner
	ExecutorCfg executor.Config
	Store       store.TaskStore
	MaxHistory  int // Maximum number of completed tasks to retain
}

// New creates a new coordinator.
func New(cfg Config) *Coordinator {
	if cfg.MaxHistory == 0 {
		cfg.MaxHistory = 100
	}
	if cfg.Planner == nil {
		cfg.Planner = planner.NewStaticPlanner()
	}
	if cfg.Store == nil {
		cfg.Store = store.NewMemoryStore()
	}

	cfg.ExecutorCfg.Registry = cfg.Registry

	return &Coordinator{
		registry:   cfg.Registry,
		planner:    cfg.Planner,
		executor:   executor.New(cfg.ExecutorCfg),
		store:      cfg.Store,
		maxHistory: cfg.MaxHistory,
		cancels:    make(map[string]context.CancelFunc),
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

	if err := c.store.Save(task); err != nil {
		return nil, err
	}
	c.pruneHistory()

	return task, nil
}

// Execute runs a submitted task asynchronously.
func (c *Coordinator) Execute(ctx context.Context, taskID string) (<-chan types.TaskEvent, error) {
	task, err := c.store.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != types.TaskStatusPending {
		return nil, fmt.Errorf("task already started: %s", task.Status)
	}

	task.Status = types.TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now
	if err := c.store.Save(task); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancelsMu.Lock()
	c.cancels[task.ID] = cancel
	c.cancelsMu.Unlock()

	events := make(chan types.TaskEvent, 100)

	go func() {
		defer func() {
			c.cancelsMu.Lock()
			delete(c.cancels, task.ID)
			c.cancelsMu.Unlock()
		}()

		err := c.executor.Execute(ctx, task, events)
		if err != nil {
			if task.Status != types.TaskStatusCancelled {
				task.Status = types.TaskStatusFailed
				task.Error = err.Error()
			}
		}
		_ = c.store.Save(task)
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
	task, err := c.store.Get(taskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	return task, nil
}

// ListTasks returns all tasks matching the filter.
func (c *Coordinator) ListTasks(status types.TaskStatus) []*types.Task {
	tasks, err := c.store.List(status)
	if err != nil {
		return nil
	}
	return tasks
}

// CancelTask attempts to cancel a running task.
func (c *Coordinator) CancelTask(taskID string) error {
	task, err := c.store.Get(taskID)
	if err != nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != types.TaskStatusRunning && task.Status != types.TaskStatusPending {
		return fmt.Errorf("task cannot be cancelled: %s", task.Status)
	}

	if task.Status == types.TaskStatusPending {
		task.Status = types.TaskStatusCancelled
		now := time.Now()
		task.CompletedAt = &now
		if err := c.store.Save(task); err != nil {
			return err
		}
		return nil
	}

	c.cancelsMu.Lock()
	cancel, exists := c.cancels[taskID]
	c.cancelsMu.Unlock()
	if exists {
		cancel()
	}

	return nil
}

// pruneHistory removes old completed tasks to prevent memory growth.
func (c *Coordinator) pruneHistory() {
	tasks, err := c.store.List("")
	if err != nil {
		return
	}

	if len(tasks) <= c.maxHistory {
		return
	}

	var oldest *types.Task
	for _, task := range tasks {
		if task.Status == types.TaskStatusCompleted || task.Status == types.TaskStatusFailed || task.Status == types.TaskStatusCancelled {
			if oldest == nil || task.CreatedAt.Before(oldest.CreatedAt) {
				oldest = task
			}
		}
	}

	if oldest != nil {
		_ = c.store.Delete(oldest.ID)
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
