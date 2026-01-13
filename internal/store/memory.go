package store

import (
	"sync"

	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// MemoryStore keeps tasks in memory.
type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*types.Task
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks: make(map[string]*types.Task),
	}
}

// Save stores or updates a task.
func (s *MemoryStore) Save(task *types.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
	return nil
}

// Get retrieves a task by ID.
func (s *MemoryStore) Get(id string) (*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return task, nil
}

// List returns tasks, optionally filtered by status.
func (s *MemoryStore) List(status types.TaskStatus) ([]*types.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tasks []*types.Task
	for _, task := range s.tasks {
		if status == "" || task.Status == status {
			tasks = append(tasks, task)
		}
	}

	return tasks, nil
}

// Delete removes a task by ID.
func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return ErrTaskNotFound
	}
	delete(s.tasks, id)
	return nil
}

func (s *MemoryStore) snapshot() map[string]*types.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := make(map[string]*types.Task, len(s.tasks))
	for key, task := range s.tasks {
		snapshot[key] = task
	}
	return snapshot
}

func (s *MemoryStore) restore(tasks map[string]*types.Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = tasks
}
