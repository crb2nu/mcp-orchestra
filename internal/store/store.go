package store

import (
	"errors"

	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// ErrTaskNotFound indicates the task ID does not exist.
var ErrTaskNotFound = errors.New("task not found")

// TaskStore persists task state.
type TaskStore interface {
	Save(task *types.Task) error
	Get(id string) (*types.Task, error)
	List(status types.TaskStatus) ([]*types.Task, error)
	Delete(id string) error
}
