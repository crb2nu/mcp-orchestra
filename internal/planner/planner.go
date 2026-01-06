// Package planner handles task decomposition and DAG generation.
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/llm"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
)

// Planner decomposes tasks into executable DAGs.
type Planner interface {
	// Plan converts a natural language prompt into a task DAG.
	Plan(ctx context.Context, prompt string, tools types.ToolInventory) (*types.Task, error)

	// Validate checks a task DAG for cycles and missing dependencies.
	Validate(task *types.Task, tools types.ToolInventory) error
}

// StaticPlanner creates tasks from explicit DAG definitions (no LLM).
type StaticPlanner struct{}

// NewStaticPlanner creates a planner that only accepts pre-defined DAGs.
func NewStaticPlanner() *StaticPlanner {
	return &StaticPlanner{}
}

// Plan returns an error for static planner (requires explicit DAG).
func (p *StaticPlanner) Plan(ctx context.Context, prompt string, tools types.ToolInventory) (*types.Task, error) {
	return nil, fmt.Errorf("static planner requires explicit DAG definition, not prompt")
}

// Validate checks a task DAG for structural issues.
func (p *StaticPlanner) Validate(task *types.Task, tools types.ToolInventory) error {
	if len(task.Steps) == 0 {
		return fmt.Errorf("task has no steps")
	}

	// Build step index
	stepIndex := make(map[string]int)
	for i, step := range task.Steps {
		if step.ID == "" {
			return fmt.Errorf("step %d has no ID", i)
		}
		if _, exists := stepIndex[step.ID]; exists {
			return fmt.Errorf("duplicate step ID: %s", step.ID)
		}
		stepIndex[step.ID] = i
	}

	// Check dependencies exist
	for _, step := range task.Steps {
		for _, dep := range step.DependsOn {
			if _, exists := stepIndex[dep]; !exists {
				return fmt.Errorf("step %s depends on unknown step %s", step.ID, dep)
			}
		}
	}

	// Check for cycles using DFS
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var hasCycle func(stepID string) bool
	hasCycle = func(stepID string) bool {
		visited[stepID] = true
		inStack[stepID] = true

		idx := stepIndex[stepID]
		for _, dep := range task.Steps[idx].DependsOn {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if inStack[dep] {
				return true
			}
		}

		inStack[stepID] = false
		return false
	}

	for _, step := range task.Steps {
		if !visited[step.ID] {
			if hasCycle(step.ID) {
				return fmt.Errorf("cycle detected in task DAG involving step %s", step.ID)
			}
		}
	}

	// Validate tool references
	for _, step := range task.Steps {
		ref := types.ParseToolRef(step.Tool)
		if _, exists := tools[ref.String()]; !exists {
			// Also try just the tool name
			if _, exists := tools[ref.Tool]; !exists {
				return fmt.Errorf("step %s references unknown tool: %s", step.ID, step.Tool)
			}
		}
	}

	return nil
}

// BuildExecutionPlan creates an execution plan with parallel waves.
func BuildExecutionPlan(task *types.Task) (*types.ExecutionPlan, error) {
	plan := &types.ExecutionPlan{
		TaskID: task.ID,
		Waves:  [][]string{},
		Graph:  make(map[string][]string),
	}

	// Build dependency graph
	for _, step := range task.Steps {
		plan.Graph[step.ID] = step.DependsOn
	}

	// Compute in-degrees
	inDegree := make(map[string]int)
	for _, step := range task.Steps {
		inDegree[step.ID] = len(step.DependsOn)
	}

	// Topological sort with wave grouping (Kahn's algorithm)
	remaining := len(task.Steps)
	for remaining > 0 {
		var wave []string
		for stepID, degree := range inDegree {
			if degree == 0 {
				wave = append(wave, stepID)
			}
		}

		if len(wave) == 0 {
			return nil, fmt.Errorf("cycle detected in task DAG")
		}

		// Remove wave steps from inDegree and update dependents
		for _, stepID := range wave {
			delete(inDegree, stepID)
			for otherID := range inDegree {
				for _, dep := range plan.Graph[otherID] {
					if dep == stepID {
						inDegree[otherID]--
					}
				}
			}
		}

		plan.Waves = append(plan.Waves, wave)
		remaining -= len(wave)
	}

	return plan, nil
}

// LLMPlannerConfig configures the LLM planner.
type LLMPlannerConfig struct {
	Endpoint string // LLM API endpoint
	Model    string // Model name
	APIKey   string // API key (optional, can use env var)
}

// LLMPlanner uses an LLM to decompose natural language into DAGs.
type LLMPlanner struct {
	client *llm.Client
}

// NewLLMPlanner creates a planner that uses an LLM for task decomposition.
func NewLLMPlanner(cfg LLMPlannerConfig) *LLMPlanner {
	return &LLMPlanner{
		client: llm.New(llm.Config{
			Endpoint: cfg.Endpoint,
			Model:    cfg.Model,
			APIKey:   cfg.APIKey,
		}),
	}
}

// Plan uses an LLM to convert a prompt into a task DAG.
func (p *LLMPlanner) Plan(ctx context.Context, prompt string, tools types.ToolInventory) (*types.Task, error) {
	// Format tools as context for the LLM
	toolsContext := formatToolsForLLM(tools)

	// Build the system prompt
	systemPrompt := buildSystemPrompt(toolsContext)

	// Build messages
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	// Call LLM with JSON output format
	result, err := p.client.CompleteJSON(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM completion failed: %w", err)
	}

	// Parse response into task
	var taskResponse struct {
		Name  string `json:"name"`
		Steps []struct {
			ID        string                 `json:"id"`
			Tool      string                 `json:"tool"`
			Args      map[string]interface{} `json:"args,omitempty"`
			DependsOn []string               `json:"depends_on,omitempty"`
		} `json:"steps"`
	}

	if err := json.Unmarshal(result, &taskResponse); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}

	// Convert to Task type
	task := &types.Task{
		Name:   taskResponse.Name,
		Prompt: prompt,
		Steps:  make([]types.Step, len(taskResponse.Steps)),
	}

	for i, step := range taskResponse.Steps {
		task.Steps[i] = types.Step{
			ID:        step.ID,
			Tool:      step.Tool,
			Args:      step.Args,
			DependsOn: step.DependsOn,
		}
	}

	// Validate the generated DAG
	if err := p.Validate(task, tools); err != nil {
		return nil, fmt.Errorf("invalid generated DAG: %w", err)
	}

	return task, nil
}

// Validate delegates to static validation.
func (p *LLMPlanner) Validate(task *types.Task, tools types.ToolInventory) error {
	static := &StaticPlanner{}
	return static.Validate(task, tools)
}

// formatToolsForLLM formats the tool inventory for inclusion in the LLM prompt.
func formatToolsForLLM(tools types.ToolInventory) string {
	// Group tools by server
	serverTools := make(map[string][]string)
	for name, ref := range tools {
		// Only include full references (server/tool) to avoid duplicates
		if strings.Contains(name, "/") {
			serverTools[ref.Server] = append(serverTools[ref.Server], ref.Tool)
		}
	}

	var sb strings.Builder
	for server, toolList := range serverTools {
		sb.WriteString(fmt.Sprintf("\n## Server: %s\n", server))
		for _, tool := range toolList {
			sb.WriteString(fmt.Sprintf("- %s/%s\n", server, tool))
		}
	}

	return sb.String()
}

// buildSystemPrompt creates the system prompt for task planning.
func buildSystemPrompt(toolsContext string) string {
	return `You are a task planning assistant that creates task DAGs (Directed Acyclic Graphs) from natural language requests.

## Available Tools
` + toolsContext + `

## Output Format

You must respond with a JSON object containing:
- "name": A short, descriptive name for the task (snake_case)
- "steps": An array of steps, where each step has:
  - "id": A unique identifier (snake_case, e.g., "read_config", "validate_schema")
  - "tool": The tool reference in "server/tool_name" format
  - "args": A JSON object with the tool's arguments
  - "depends_on": An array of step IDs that must complete before this step (optional)

## Rules

1. Steps with no "depends_on" execute in parallel
2. Use "depends_on" to sequence steps that need outputs from previous steps
3. Use "${{ steps.STEP_ID.output }}" syntax to reference output from a previous step
4. Minimize the number of steps while ensuring correctness
5. Always validate inputs before performing destructive operations
6. Use descriptive, snake_case IDs for steps

## Example

For "Read a file and validate it against a schema":

{
  "name": "validate_file",
  "steps": [
    {
      "id": "read_file",
      "tool": "filesystem/read_file",
      "args": {"path": "/app/config.yaml"}
    },
    {
      "id": "validate",
      "tool": "schema/validate_yaml",
      "args": {
        "content": "${{ steps.read_file.output }}",
        "schema_path": "/app/schema.json"
      },
      "depends_on": ["read_file"]
    }
  ]
}

Now create a task DAG for the user's request. Respond ONLY with the JSON object.`
}
