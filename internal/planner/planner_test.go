package planner

import (
	"testing"

	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

func TestValidateRejectsDuplicateStepIDs(t *testing.T) {
	task := &types.Task{
		Steps: []types.Step{
			{ID: "dup", Tool: "filesystem/read_file"},
			{ID: "dup", Tool: "filesystem/read_file"},
		},
	}

	planner := NewStaticPlanner()
	err := planner.Validate(task, testTools())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateRejectsMissingDependency(t *testing.T) {
	task := &types.Task{
		Steps: []types.Step{
			{ID: "step_1", Tool: "filesystem/read_file", DependsOn: []string{"missing"}},
		},
	}

	planner := NewStaticPlanner()
	err := planner.Validate(task, testTools())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateRejectsCycles(t *testing.T) {
	task := &types.Task{
		Steps: []types.Step{
			{ID: "a", Tool: "filesystem/read_file", DependsOn: []string{"b"}},
			{ID: "b", Tool: "filesystem/read_file", DependsOn: []string{"a"}},
		},
	}

	planner := NewStaticPlanner()
	err := planner.Validate(task, testTools())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestValidateRejectsUnknownTool(t *testing.T) {
	task := &types.Task{
		Steps: []types.Step{
			{ID: "step_1", Tool: "unknown/tool"},
		},
	}

	planner := NewStaticPlanner()
	err := planner.Validate(task, testTools())
	if err == nil {
		t.Fatalf("expected error")
	}
}

func testTools() types.ToolInventory {
	return types.ToolInventory{
		"filesystem/read_file": {Server: "filesystem", Tool: "read_file"},
		"read_file":            {Server: "filesystem", Tool: "read_file"},
	}
}
