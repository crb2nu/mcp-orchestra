// Package main provides the CLI entrypoint for MCP Orchestra.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/coordinator"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/executor"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/planner"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/registry"
	"gitlab.flexinfer.ai/services/mcp-orchestra/internal/store"
	"gitlab.flexinfer.ai/services/mcp-orchestra/pkg/types"
	"gopkg.in/yaml.v3"
)

var (
	version = "dev"

	// Global flags
	registryPath string
	logLevel     string
	target       string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "orchestra",
		Short:   "MCP Orchestra - Multi-agent coordinator for MCP servers",
		Version: version,
	}

	rootCmd.PersistentFlags().StringVar(&registryPath, "registry", "", "Path to MCP registry YAML")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&target, "target", envOrDefault("ORCHESTRA_TARGET", "codex"), "Registry target profile (for fi-mcp/loom registries)")

	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(toolsCmd())
	rootCmd.AddCommand(validateCmd())
	rootCmd.AddCommand(planCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	var port int
	var metricsPort int
	var maxIdle int
	var maxOpen int
	var idleTimeout time.Duration
	var timeout time.Duration
	var storePath string

	// LLM configuration
	var llmEndpoint string
	var llmModel string
	var llmAPIKey string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Orchestra HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}

			// Create the appropriate planner
			var p planner.Planner
			if llmEndpoint != "" {
				p = planner.NewLLMPlanner(planner.LLMPlannerConfig{
					Endpoint: llmEndpoint,
					Model:    llmModel,
					APIKey:   llmAPIKey,
				})
				fmt.Printf("LLM planner enabled (endpoint: %s, model: %s)\n", llmEndpoint, llmModel)
			} else {
				p = planner.NewStaticPlanner()
			}

			var taskStore store.TaskStore
			if storePath != "" {
				taskStore, err = store.NewFileStore(storePath)
				if err != nil {
					return fmt.Errorf("load task store: %w", err)
				}
			}

			coord := coordinator.New(coordinator.Config{
				Registry: reg,
				Planner:  p,
				ExecutorCfg: executor.Config{
					MaxIdle:     maxIdle,
					MaxOpen:     maxOpen,
					IdleTimeout: idleTimeout,
					Timeout:     timeout,
				},
				Store: taskStore,
			})
			defer coord.Close()

			// Setup HTTP routes
			mux := http.NewServeMux()
			mux.HandleFunc("GET /health", healthHandler)
			mux.HandleFunc("GET /ready", readyHandler)
			mux.HandleFunc("GET /v1/tools", toolsHandler(coord))
			mux.HandleFunc("GET /v1/servers", serversHandler(coord))
			mux.HandleFunc("GET /v1/tasks", listTasksHandler(coord))
			mux.HandleFunc("POST /v1/tasks", submitTaskHandler(coord, llmEndpoint != ""))
			mux.HandleFunc("GET /v1/tasks/{id}", getTaskHandler(coord))
			mux.HandleFunc("POST /v1/tasks/{id}/cancel", cancelTaskHandler(coord))

			server := &http.Server{
				Addr:         fmt.Sprintf(":%d", port),
				Handler:      mux,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: timeout + 10*time.Second,
			}

			// Graceful shutdown
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			go func() {
				fmt.Printf("Orchestra server listening on :%d\n", port)
				if err := server.ListenAndServe(); err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
				}
			}()

			<-ctx.Done()
			fmt.Println("Shutting down...")

			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()

			return server.Shutdown(shutdownCtx)
		},
	}

	cmd.Flags().IntVar(&port, "port", 8090, "HTTP API port")
	cmd.Flags().IntVar(&metricsPort, "metrics-port", 9090, "Prometheus metrics port")
	cmd.Flags().IntVar(&maxIdle, "pool-max-idle", 2, "Max idle connections per MCP server")
	cmd.Flags().IntVar(&maxOpen, "pool-max-open", 10, "Max open connections per MCP server")
	cmd.Flags().DurationVar(&idleTimeout, "pool-idle-timeout", 5*time.Minute, "Connection idle timeout")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Default task timeout")
	cmd.Flags().StringVar(&storePath, "store-path", "", "Path to task store file")

	// LLM flags
	cmd.Flags().StringVar(&llmEndpoint, "llm-endpoint", "", "LLM API endpoint (e.g., https://api.openai.com/v1/chat/completions)")
	cmd.Flags().StringVar(&llmModel, "llm-model", "gpt-4o", "LLM model name")
	cmd.Flags().StringVar(&llmAPIKey, "llm-api-key", "", "LLM API key (or use ORCHESTRA_LLM_API_KEY / OPENAI_API_KEY env)")

	return cmd
}

func runCmd() *cobra.Command {
	var taskFile string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute a task from a YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}

			coord := coordinator.New(coordinator.Config{
				Registry: reg,
				Planner:  planner.NewStaticPlanner(),
				ExecutorCfg: executor.Config{
					Timeout: timeout,
				},
			})
			defer coord.Close()

			// Load task definition
			data, err := os.ReadFile(taskFile)
			if err != nil {
				return fmt.Errorf("failed to read task file: %w", err)
			}

			var task types.Task
			if err := yaml.Unmarshal(data, &task); err != nil {
				return fmt.Errorf("failed to parse task YAML: %w", err)
			}

			// Execute task
			ctx := context.Background()
			events, err := coord.SubmitAndExecute(ctx, &task)
			if err != nil {
				return fmt.Errorf("failed to execute task: %w", err)
			}

			// Stream events to stdout
			for event := range events {
				data, _ := json.Marshal(event)
				fmt.Println(string(data))
			}

			// Get final task state
			finalTask, _ := coord.GetTask(task.ID)
			if finalTask.Status == types.TaskStatusFailed {
				return fmt.Errorf("task failed: %s", finalTask.Error)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&taskFile, "file", "f", "", "Task definition YAML file")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Task timeout")
	cmd.MarkFlagRequired("file")

	return cmd
}

func planCmd() *cobra.Command {
	var llmEndpoint string
	var llmModel string
	var llmAPIKey string
	var outputFile string

	cmd := &cobra.Command{
		Use:   "plan [prompt]",
		Short: "Generate a task DAG from a natural language prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if llmEndpoint == "" {
				return fmt.Errorf("--llm-endpoint is required")
			}

			reg, err := loadRegistry()
			if err != nil {
				return err
			}

			p := planner.NewLLMPlanner(planner.LLMPlannerConfig{
				Endpoint: llmEndpoint,
				Model:    llmModel,
				APIKey:   llmAPIKey,
			})

			prompt := args[0]
			tools := reg.ListTools()

			ctx := context.Background()
			task, err := p.Plan(ctx, prompt, tools)
			if err != nil {
				return fmt.Errorf("planning failed: %w", err)
			}

			// Output as YAML
			output, err := yaml.Marshal(task)
			if err != nil {
				return fmt.Errorf("marshal task: %w", err)
			}

			if outputFile != "" {
				if err := os.WriteFile(outputFile, output, 0644); err != nil {
					return fmt.Errorf("write file: %w", err)
				}
				fmt.Printf("Task written to %s\n", outputFile)
			} else {
				fmt.Println(string(output))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&llmEndpoint, "llm-endpoint", "", "LLM API endpoint (required)")
	cmd.Flags().StringVar(&llmModel, "llm-model", "gpt-4o", "LLM model name")
	cmd.Flags().StringVar(&llmAPIKey, "llm-api-key", "", "LLM API key")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file (default: stdout)")
	cmd.MarkFlagRequired("llm-endpoint")

	return cmd
}

func toolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "List available tools from the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}

			tools := reg.ListTools()
			for name, ref := range tools {
				fmt.Printf("%s -> %s\n", name, ref.String())
			}

			return nil
		},
	}
}

func validateCmd() *cobra.Command {
	var taskFile string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a task definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := loadRegistry()
			if err != nil {
				return err
			}

			data, err := os.ReadFile(taskFile)
			if err != nil {
				return fmt.Errorf("failed to read task file: %w", err)
			}

			var task types.Task
			if err := yaml.Unmarshal(data, &task); err != nil {
				return fmt.Errorf("failed to parse task YAML: %w", err)
			}

			p := planner.NewStaticPlanner()
			if err := p.Validate(&task, reg.ListTools()); err != nil {
				return fmt.Errorf("validation failed: %w", err)
			}

			plan, err := planner.BuildExecutionPlan(&task)
			if err != nil {
				return fmt.Errorf("failed to build execution plan: %w", err)
			}

			fmt.Println("Task is valid!")
			fmt.Printf("Execution waves: %d\n", len(plan.Waves))
			for i, wave := range plan.Waves {
				fmt.Printf("  Wave %d: %v\n", i+1, wave)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&taskFile, "file", "f", "", "Task definition YAML file")
	cmd.MarkFlagRequired("file")

	return cmd
}

func loadRegistry() (*registry.Registry, error) {
	path := registryPath
	if path == "" {
		path = os.Getenv("ORCHESTRA_REGISTRY")
	}
	if path == "" {
		path = "./registry.yaml"
	}

	return registry.LoadFromFileWithOptions(path, registry.LoadOptions{Target: target})
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// HTTP Handlers

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy"}`))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ready"}`))
}

func toolsHandler(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tools := coord.ListTools()
		writeJSON(w, http.StatusOK, tools)
	}
}

func serversHandler(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		servers := coord.ListServers()
		writeJSON(w, http.StatusOK, servers)
	}
}

func submitTaskHandler(coord *coordinator.Coordinator, llmEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Prompt  string      `json:"prompt,omitempty"`
			Task    *types.Task `json:"task,omitempty"`
			Stream  bool        `json:"stream,omitempty"`
			Timeout string      `json:"timeout,omitempty"`
		}

		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}

		ctx := r.Context()
		if req.Timeout != "" {
			timeout, err := time.ParseDuration(req.Timeout)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid timeout format")
				return
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		var events <-chan types.TaskEvent
		var err error

		if req.Task != nil {
			events, err = coord.SubmitAndExecute(ctx, req.Task)
		} else if req.Prompt != "" {
			if !llmEnabled {
				writeJSONError(w, http.StatusNotImplemented, "LLM planner not configured. Start server with --llm-endpoint")
				return
			}
			// Use LLM planner to generate task from prompt
			task, planErr := coord.SubmitPrompt(ctx, req.Prompt)
			if planErr != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("planning failed: %s", planErr.Error()))
				return
			}
			events, err = coord.Execute(ctx, task.ID)
		} else {
			writeJSONError(w, http.StatusBadRequest, "either task or prompt required")
			return
		}

		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		if req.Stream {
			// SSE streaming
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "streaming not supported", http.StatusInternalServerError)
				return
			}

			for event := range events {
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		} else {
			// Collect all events and return final result
			var lastEvent types.TaskEvent
			for event := range events {
				lastEvent = event
			}

			writeJSON(w, http.StatusOK, lastEvent)
		}
	}
}

func getTaskHandler(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		task, err := coord.GetTask(taskID)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSON(w, http.StatusOK, task)
	}
}

func listTasksHandler(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statusParam := r.URL.Query().Get("status")
		if statusParam != "" && !isValidTaskStatus(statusParam) {
			writeJSONError(w, http.StatusBadRequest, "invalid status filter")
			return
		}

		tasks := coord.ListTasks(types.TaskStatus(statusParam))
		writeJSON(w, http.StatusOK, tasks)
	}
}

func isValidTaskStatus(value string) bool {
	switch types.TaskStatus(value) {
	case types.TaskStatusPending,
		types.TaskStatusRunning,
		types.TaskStatusCompleted,
		types.TaskStatusFailed,
		types.TaskStatusCancelled:
		return true
	default:
		return false
	}
}

func cancelTaskHandler(coord *coordinator.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("id")
		if err := coord.CancelTask(taskID); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
