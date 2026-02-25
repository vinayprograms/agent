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

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agentkit/registry"
	"github.com/vinayprograms/agentkit/tasks"
)

// serviceAgent holds the state for a running service agent.
type serviceAgent struct {
	cfg        *config.Config
	workflow   *agentfile.Workflow
	capability registry.CapabilitySchema

	// Runtime state
	status       string // "idle", "busy", "draining"
	currentTask  *tasks.TaskMessage
	taskDone     chan struct{}
	drainTimeout time.Duration

	// HTTP server (for local mode)
	httpServer *http.Server
}

// Run executes the serve command.
func (cmd *ServeCmd) Run() error {
	// Load config
	cfg, err := loadServeConfig(cmd.Config)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply command-line overrides
	if cmd.HTTP != "" {
		cfg.Service.HTTPAddr = cmd.HTTP
	}
	if cmd.Bus != "" {
		cfg.Service.BusURL = cmd.Bus
	}
	if cmd.QueueGroup != "" {
		cfg.Service.QueueGroup = cmd.QueueGroup
	}
	if cmd.Capability != "" {
		cfg.Service.Capability = cmd.Capability
	}

	// Set workspace
	workspace := cmd.Workspace
	if workspace == "" {
		workspace = cfg.Agent.Workspace
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	// Load and parse Agentfile
	wf, err := loadAgentfile(cmd.File)
	if err != nil {
		return err
	}

	// Determine capability name
	capabilityName := cfg.Service.Capability
	if capabilityName == "" {
		capabilityName = wf.Name
	}

	// Extract capability schema from Agentfile
	capability := extractCapabilitySchema(wf, capabilityName)

	// Parse drain timeout
	drainTimeout := 30 * time.Second
	if cfg.Service.DrainTimeout != "" {
		if d, err := time.ParseDuration(cfg.Service.DrainTimeout); err == nil {
			drainTimeout = d
		}
	}

	// Create service agent
	agent := &serviceAgent{
		cfg:          cfg,
		workflow:     wf,
		capability:   capability,
		status:       "idle",
		taskDone:     make(chan struct{}),
		drainTimeout: drainTimeout,
	}

	// Determine mode and run
	if cfg.Service.BusURL != "" {
		return agent.runBusMode()
	} else if cfg.Service.HTTPAddr != "" {
		return agent.runHTTPMode()
	} else {
		return fmt.Errorf("no transport configured: specify --http or --bus, or set [service].http_addr or [service].bus_url in config")
	}
}

// runHTTPMode runs the agent as an HTTP server.
func (a *serviceAgent) runHTTPMode() error {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     a.status,
			"capability": a.capability.Name,
		})
	})

	// Capability schema
	mux.HandleFunc("/capability", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.capability)
	})

	// Task submission
	mux.HandleFunc("/task", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Check if draining
		if a.status == "draining" {
			http.Error(w, "agent is draining, not accepting tasks", http.StatusServiceUnavailable)
			return
		}

		// Parse task message
		var task tasks.TaskMessage
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			http.Error(w, fmt.Sprintf("invalid task: %v", err), http.StatusBadRequest)
			return
		}

		// Validate task
		if err := task.Validate(); err != nil {
			http.Error(w, fmt.Sprintf("invalid task: %v", err), http.StatusBadRequest)
			return
		}

		// Execute task
		result := a.executeTask(r.Context(), &task)

		// Return result
		w.Header().Set("Content-Type", "application/json")
		if result.Status == tasks.ResultFailed {
			w.WriteHeader(http.StatusInternalServerError)
		}
		json.NewEncoder(w).Encode(result)
	})

	a.httpServer = &http.Server{
		Addr:    a.cfg.Service.HTTPAddr,
		Handler: mux,
	}

	// Handle shutdown signals
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nReceived shutdown signal, draining...\n")
		a.initiateShutdown(ctx)
	}()

	fmt.Fprintf(os.Stderr, "Service agent running: %s\n", a.capability.Name)
	fmt.Fprintf(os.Stderr, "HTTP server listening on %s\n", a.cfg.Service.HTTPAddr)
	fmt.Fprintf(os.Stderr, "Endpoints:\n")
	fmt.Fprintf(os.Stderr, "  GET  /health     - Health check\n")
	fmt.Fprintf(os.Stderr, "  GET  /capability - Capability schema\n")
	fmt.Fprintf(os.Stderr, "  POST /task       - Submit task\n")

	if err := a.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	return nil
}

// runBusMode runs the agent connected to a message bus (swarm mode).
func (a *serviceAgent) runBusMode() error {
	// TODO: Implement NATS bus mode
	return fmt.Errorf("bus mode not yet implemented (coming soon)")
}

// executeTask runs a single task through the workflow.
func (a *serviceAgent) executeTask(ctx context.Context, task *tasks.TaskMessage) *tasks.TaskResult {
	start := time.Now()
	a.status = "busy"
	a.currentTask = task
	defer func() {
		a.status = "idle"
		a.currentTask = nil
		select {
		case a.taskDone <- struct{}{}:
		default:
		}
	}()

	result := tasks.NewTaskResult(task.TaskID, a.cfg.Agent.ID, tasks.ResultSuccess)
	result.CorrelationID = task.CorrelationID
	result.Attempt = task.Attempt

	// For now, return a placeholder - full implementation needs executor setup
	// TODO: Wire up executor with LLM provider, tools, etc.
	result.Status = tasks.ResultFailed
	result.Error = "service agent execution not yet implemented - infrastructure in place, executor integration pending"
	result.DurationMs = time.Since(start).Milliseconds()

	return result
}

// initiateShutdown handles graceful shutdown.
func (a *serviceAgent) initiateShutdown(ctx context.Context) {
	a.status = "draining"

	// Wait for current task to complete (with timeout)
	if a.currentTask != nil {
		fmt.Fprintf(os.Stderr, "Waiting for current task to complete (timeout: %s)...\n", a.drainTimeout)
		select {
		case <-a.taskDone:
			fmt.Fprintf(os.Stderr, "Task completed, shutting down.\n")
		case <-time.After(a.drainTimeout):
			fmt.Fprintf(os.Stderr, "Drain timeout reached, forcing shutdown.\n")
		}
	}

	// Shutdown HTTP server
	if a.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		a.httpServer.Shutdown(shutdownCtx)
	}
}

// extractCapabilitySchema extracts capability info from an Agentfile.
func extractCapabilitySchema(wf *agentfile.Workflow, name string) registry.CapabilitySchema {
	schema := registry.CapabilitySchema{
		Name:        name,
		Description: fmt.Sprintf("Workflow: %s", wf.Name),
	}

	// Extract inputs from Agentfile
	for _, input := range wf.Inputs {
		field := registry.FieldSchema{
			Name:     input.Name,
			Required: input.Default == nil,
			Type:     "string",
		}
		if input.Default != nil {
			field.Default = *input.Default
		}
		schema.Inputs = append(schema.Inputs, field)
	}

	// Extract outputs from goals (if declared with ->)
	for _, goal := range wf.Goals {
		for _, output := range goal.Outputs {
			field := registry.FieldSchema{
				Name: output,
				Type: "string",
			}
			schema.Outputs = append(schema.Outputs, field)
		}
	}

	return schema
}

// loadServeConfig loads configuration for serve mode.
func loadServeConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.LoadFile(path)
	}
	return config.LoadDefault()
}

// loadAgentfile loads and parses an Agentfile.
func loadAgentfile(path string) (*agentfile.Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	wf, err := agentfile.ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if err := agentfile.Validate(wf); err != nil {
		return nil, fmt.Errorf("validating %s: %w", path, err)
	}

	return wf, nil
}

// TODO: Implement executor integration for service mode
// This requires wiring up:
// - LLM provider creation
// - Tool registry setup
// - Policy loading
// - Session management
// For now, the HTTP endpoints work but task execution returns a stub error.
