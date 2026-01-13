package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/coordinator"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/planner"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

func TestListTasksHandlerFiltersByStatus(t *testing.T) {
	coord := newTestCoordinator(t)

	if _, err := coord.SubmitDAG(context.Background(), newTestTask("pending_task")); err != nil {
		t.Fatalf("submit pending task: %v", err)
	}

	failedTask := newTestTask("failed_task")
	submittedFailed, err := coord.SubmitDAG(context.Background(), failedTask)
	if err != nil {
		t.Fatalf("submit failed task: %v", err)
	}
	submittedFailed.Status = types.TaskStatusFailed

	req := httptest.NewRequest(http.MethodGet, "/v1/tasks?status=failed", nil)
	recorder := httptest.NewRecorder()

	listTasksHandler(coord).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}

	var tasks []types.Task
	if err := json.NewDecoder(recorder.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Name != failedTask.Name {
		t.Fatalf("expected failed task, got %s", tasks[0].Name)
	}

}

func TestListTasksHandlerRejectsInvalidStatus(t *testing.T) {
	coord := newTestCoordinator(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks?status=unknown", nil)
	recorder := httptest.NewRecorder()

	listTasksHandler(coord).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}

func newTestCoordinator(t *testing.T) *coordinator.Coordinator {
	t.Helper()

	reg, err := registry.LoadFromFile("testdata/registry.yaml")
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	return coordinator.New(coordinator.Config{
		Registry: reg,
		Planner:  planner.NewStaticPlanner(),
	})
}

func newTestTask(name string) *types.Task {
	return &types.Task{
		Name: name,
		Steps: []types.Step{
			{
				ID:   "read_file",
				Tool: "filesystem/read_file",
				Args: map[string]interface{}{
					"path": "/tmp/config.yaml",
				},
			},
		},
	}
}
