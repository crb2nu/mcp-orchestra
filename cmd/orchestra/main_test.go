package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/coordinator"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/executor"
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

func TestSubmitTaskHandlerStreamsEvents(t *testing.T) {
	coord := newTestCoordinatorWithDialer(t, func(ctx context.Context, serverName string) (mcp.Transport, error) {
		return newSuccessTransport("ok"), nil
	})

	task := newTestTask("stream_task")
	payload := map[string]interface{}{
		"task":   task,
		"stream": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader(body))
	recorder := httptest.NewRecorder()

	submitTaskHandler(coord, false).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}

	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("expected text/event-stream content type, got %q", contentType)
	}

	bodyText := recorder.Body.String()
	if !bytes.Contains([]byte(bodyText), []byte(`"type":"task_complete"`)) {
		t.Fatalf("expected task_complete event")
	}

	tasks := coord.ListTasks("")
	if !hasTaskStatus(tasks, types.TaskStatusCompleted) {
		t.Fatalf("expected completed task status")
	}
}

func newTestCoordinator(t *testing.T) *coordinator.Coordinator {
	t.Helper()

	reg, err := registry.LoadFromFile(filepath.Join("..", "..", "testdata", "registry.yaml"))
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	return coordinator.New(coordinator.Config{
		Registry: reg,
		Planner:  planner.NewStaticPlanner(),
	})
}

func newTestCoordinatorWithDialer(t *testing.T, dialer func(ctx context.Context, serverName string) (mcp.Transport, error)) *coordinator.Coordinator {
	t.Helper()

	reg, err := registry.LoadFromFile(filepath.Join("..", "..", "testdata", "registry.yaml"))
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}

	return coordinator.New(coordinator.Config{
		Registry: reg,
		Planner:  planner.NewStaticPlanner(),
		ExecutorCfg: executor.Config{
			DialFunc: dialer,
			Timeout:  2 * time.Second,
		},
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

func hasTaskStatus(tasks []*types.Task, status types.TaskStatus) bool {
	for _, task := range tasks {
		if task.Status == status {
			return true
		}
	}
	return false
}

type fakeTransport struct {
	recvFn func(ctx context.Context) (*mcp.Message, error)
}

func newSuccessTransport(text string) mcp.Transport {
	result := mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "text", Text: text},
		},
	}
	payload, _ := json.Marshal(result)

	return &fakeTransport{
		recvFn: func(ctx context.Context) (*mcp.Message, error) {
			return &mcp.Message{
				JSONRPC: mcp.JSONRPCVersion,
				Result:  payload,
			}, nil
		},
	}
}

func (f *fakeTransport) Send(ctx context.Context, msg *mcp.Message) error {
	return nil
}

func (f *fakeTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	return f.recvFn(ctx)
}

func (f *fakeTransport) Close() error {
	return nil
}
