package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

func TestExecutorEmitsTaskErrorOnFailure(t *testing.T) {
	exec := newTestExecutor(t, &fakeTransport{
		recvFn: func(ctx context.Context) (*mcp.Message, error) {
			return &mcp.Message{
				JSONRPC: mcp.JSONRPCVersion,
				Error: &mcp.Error{
					Code:    -32000,
					Message: "boom",
				},
			}, nil
		},
	})

	task := &types.Task{
		ID:   "task-1",
		Name: "failure_task",
		Steps: []types.Step{
			{ID: "step_1", Tool: "dummy/tool"},
		},
	}

	events := make(chan types.TaskEvent, 10)
	err := exec.Execute(context.Background(), task, events)
	if err == nil {
		t.Fatalf("expected error")
	}

	if task.Status != types.TaskStatusFailed {
		t.Fatalf("expected failed status, got %s", task.Status)
	}
	if task.CompletedAt == nil {
		t.Fatalf("expected CompletedAt to be set")
	}

	if !containsEvent(events, "task_error") {
		t.Fatalf("expected task_error event")
	}
}

func TestExecutorEmitsTaskCancelledOnContextCancel(t *testing.T) {
	exec := newTestExecutor(t, &fakeTransport{
		recvFn: func(ctx context.Context) (*mcp.Message, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	task := &types.Task{
		ID:   "task-2",
		Name: "cancel_task",
		Steps: []types.Step{
			{ID: "step_1", Tool: "dummy/tool"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)

	events := make(chan types.TaskEvent, 10)
	err := exec.Execute(ctx, task, events)
	if err == nil {
		t.Fatalf("expected error")
	}

	if task.Status != types.TaskStatusCancelled {
		t.Fatalf("expected cancelled status, got %s", task.Status)
	}
	if task.CompletedAt == nil {
		t.Fatalf("expected CompletedAt to be set")
	}

	if !containsEvent(events, "task_cancelled") {
		t.Fatalf("expected task_cancelled event")
	}
}

type fakeTransport struct {
	sendErr error
	recvFn  func(ctx context.Context) (*mcp.Message, error)
}

func (f *fakeTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	return nil
}

func (f *fakeTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	if f.recvFn == nil {
		return nil, errors.New("no recvFn configured")
	}
	return f.recvFn(ctx)
}

func (f *fakeTransport) Close() error {
	return nil
}

func newTestExecutor(t *testing.T, transport mcp.Transport) *Executor {
	t.Helper()

	reg := registry.New()
	dialer := func(ctx context.Context, serverName string) (mcp.Transport, error) {
		return transport, nil
	}

	return New(Config{
		Registry: reg,
		DialFunc: dialer,
		Timeout:  2 * time.Second,
	})
}

func containsEvent(events <-chan types.TaskEvent, eventType string) bool {
	for event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
