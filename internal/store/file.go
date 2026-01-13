package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// FileStore persists tasks to a JSON file.
type FileStore struct {
	path string
	mem  *MemoryStore
	mu   sync.Mutex
}

// NewFileStore creates a file-backed store and loads existing data if present.
func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{
		path: path,
		mem:  NewMemoryStore(),
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

// Save stores or updates a task and flushes to disk.
func (s *FileStore) Save(task *types.Task) error {
	if err := s.mem.Save(task); err != nil {
		return err
	}
	return s.flush()
}

// Get retrieves a task by ID.
func (s *FileStore) Get(id string) (*types.Task, error) {
	return s.mem.Get(id)
}

// List returns tasks, optionally filtered by status.
func (s *FileStore) List(status types.TaskStatus) ([]*types.Task, error) {
	return s.mem.List(status)
}

// Delete removes a task by ID and flushes to disk.
func (s *FileStore) Delete(id string) error {
	if err := s.mem.Delete(id); err != nil {
		return err
	}
	return s.flush()
}

func (s *FileStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var tasks map[string]*types.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}

	s.mem.restore(tasks)
	return nil
}

func (s *FileStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	snapshot := s.mem.snapshot()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmpPath, s.path)
}
