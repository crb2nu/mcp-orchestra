package executor

import (
	"context"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

func TestExecutorEndToEndWithInterpolation(t *testing.T) {
	dialer, cleanup := newPipeServerDialer(t, "echo", func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		message, _ := args["message"].(string)
		return mcp.TextResult(message), nil
	})
	t.Cleanup(cleanup)

	exec := New(Config{
		Registry: registry.New(),
		DialFunc: dialer,
		Timeout:  2 * time.Second,
	})

	task := &types.Task{
		ID:   "task-echo",
		Name: "echo_task",
		Steps: []types.Step{
			{
				ID:   "first",
				Tool: "test/echo",
				Args: map[string]interface{}{
					"message": "hello",
				},
			},
			{
				ID:        "second",
				Tool:      "test/echo",
				DependsOn: []string{"first"},
				Args: map[string]interface{}{
					"message": "${{ steps.first.output }} world",
				},
			},
		},
	}

	events := make(chan types.TaskEvent, 10)
	if err := exec.Execute(context.Background(), task, events); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if task.Status != types.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s", task.Status)
	}

	result, ok := task.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result map")
	}

	if result["first"] != "hello" {
		t.Fatalf("unexpected first output: %v", result["first"])
	}
	if result["second"] != "hello world" {
		t.Fatalf("unexpected second output: %v", result["second"])
	}
}

func newPipeServerDialer(t *testing.T, toolName string, handler mcp.ToolHandler) (func(ctx context.Context, serverName string) (mcp.Transport, error), func()) {
	t.Helper()

	var cancels []context.CancelFunc
	dialer := func(ctx context.Context, serverName string) (mcp.Transport, error) {
		server := mcp.NewServer("test", "1.0.0")
		server.AddTool(mcp.Tool{
			Name:        toolName,
			Description: "test tool",
			InputSchema: mcp.InputSchema{Type: "object"},
		}, handler)

		clientTransport, serverTransport := mcp.NewPipeTransport()
		serverCtx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)

		go func() {
			_ = server.ServeTransport(serverCtx, serverTransport)
		}()

		return clientTransport, nil
	}

	cleanup := func() {
		for _, cancel := range cancels {
			cancel()
		}
	}

	return dialer, cleanup
}
