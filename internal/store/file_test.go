package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

func TestFileStoreSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}

	task := &types.Task{
		ID:        "task-1",
		Name:      "test_task",
		Status:    types.TaskStatusCompleted,
		CreatedAt: time.Now(),
		Steps: []types.Step{
			{ID: "step_1", Tool: "filesystem/read_file", Status: types.StepStatusCompleted},
		},
	}

	if err := store.Save(task); err != nil {
		t.Fatalf("save task: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected store file to exist: %v", err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}

	loadedTask, err := reloaded.Get(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if loadedTask.Name != task.Name {
		t.Fatalf("unexpected task name: %s", loadedTask.Name)
	}

	if err := reloaded.Delete(task.ID); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if _, err := reloaded.Get(task.ID); err == nil {
		t.Fatalf("expected not found after delete")
	}
}
