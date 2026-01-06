# MCP Orchestra

Multi-agent coordinator service for orchestrating MCP servers.

## Quick Commands

```bash
# Build
make build

# Run with example registry
make run

# Validate a task file
make validate-example

# Run tests
make test

# Development mode with debug logging
make dev
```

## Architecture

```
cmd/orchestra/main.go     # CLI + HTTP server entrypoint
internal/
  coordinator/            # Task lifecycle management
  planner/                # DAG generation and validation
  executor/               # Parallel step execution
  registry/               # MCP server discovery
pkg/types/                # Shared type definitions
api/                      # (future) OpenAPI specs
testdata/                 # Example configs for testing
```

## Key Concepts

### Task DAG
Tasks are defined as directed acyclic graphs (DAGs) where each step specifies:
- `id`: Unique step identifier
- `tool`: MCP tool reference (server/tool format)
- `args`: Tool arguments (supports interpolation)
- `depends_on`: List of step IDs that must complete first

### Execution Waves
Steps are grouped into parallel execution waves:
1. Wave 0: Steps with no dependencies (run in parallel)
2. Wave 1: Steps depending only on Wave 0 (run after Wave 0)
3. ...and so on

### Argument Interpolation
Use `${{ steps.STEP_ID.output }}` to reference output from a previous step.

## Development Notes

- Uses `mcp-go` for MCP protocol implementation
- Connection pooling prevents excessive reconnections
- Events are streamed via SSE for real-time feedback
- Static planner requires explicit DAGs; LLM planner (TODO) will convert prompts

## Testing

```bash
# Run all tests
go test ./...

# Run with race detection and coverage
go test -race -cover ./...

# Test specific package
go test ./internal/planner/...
```

## Dependencies

- `gitlab.flexinfer.ai/libs/mcp-go` - MCP protocol SDK
- `github.com/spf13/cobra` - CLI framework
- `github.com/google/uuid` - Task ID generation
- `gopkg.in/yaml.v3` - YAML parsing
