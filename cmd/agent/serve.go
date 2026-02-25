package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agentkit/bus"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/heartbeat"
	"github.com/vinayprograms/agentkit/registry"
	"github.com/vinayprograms/agentkit/tasks"
)

// serviceAgent holds the state for a running service agent.
type serviceAgent struct {
	// Reuse workflow loading infrastructure
	wf    *workflow
	creds *credentials.Credentials

	// Agent identity
	agentID    string
	capability registry.CapabilitySchema

	// Runtime state
	status       string // "idle", "busy", "draining"
	currentTask  *tasks.TaskMessage
	taskDone     chan struct{}
	drainTimeout time.Duration

	// HTTP server (for local mode)
	httpServer *http.Server

	// Bus mode components
	bus         bus.MessageBus
	heartbeat   *heartbeat.BusSender
	taskSub     bus.Subscription
	queueGroup  string
}

// Run executes the serve command.
func (cmd *ServeCmd) Run() error {
	// Create workflow struct (reuses existing loading infrastructure)
	wf := &workflow{
		agentfilePath: cmd.File,
		configPath:    cmd.Config,
		policyPath:    cmd.Policy,
		workspacePath: cmd.Workspace,
		inputs:        make(map[string]string), // Will be set per-task
		debug:         false,
	}

	// Load config, policy, and agentfile
	if err := wf.load(); err != nil {
		return err
	}

	// Apply command-line overrides for service config
	if cmd.HTTP != "" {
		wf.cfg.Service.HTTPAddr = cmd.HTTP
	}
	if cmd.Bus != "" {
		wf.cfg.Service.BusURL = cmd.Bus
	}
	if cmd.QueueGroup != "" {
		wf.cfg.Service.QueueGroup = cmd.QueueGroup
	}
	if cmd.Capability != "" {
		wf.cfg.Service.Capability = cmd.Capability
	}

	// Determine capability name
	capabilityName := wf.cfg.Service.Capability
	if capabilityName == "" {
		capabilityName = wf.wf.Name
	}

	// Extract capability schema from Agentfile
	capability := extractCapabilitySchema(wf.wf, capabilityName)

	// Parse drain timeout
	drainTimeout := 30 * time.Second
	if wf.cfg.Service.DrainTimeout != "" {
		if d, err := time.ParseDuration(wf.cfg.Service.DrainTimeout); err == nil {
			drainTimeout = d
		}
	}

	// Load credentials (same as run mode)
	creds, _, err := credentials.Load()
	if err != nil {
		// Credentials are optional, continue with nil
		creds = nil
	}

	// Determine agent ID
	agentID := wf.cfg.Agent.ID
	if agentID == "" {
		// Auto-generate: <capability>-<random-suffix>
		agentID = fmt.Sprintf("%s-%s", capabilityName, generateShortID())
	}

	// Create service agent
	agent := &serviceAgent{
		wf:           wf,
		creds:        creds,
		agentID:      agentID,
		capability:   capability,
		status:       "idle",
		taskDone:     make(chan struct{}),
		drainTimeout: drainTimeout,
	}

	// Determine mode and run
	if wf.cfg.Service.BusURL != "" {
		return agent.runBusMode()
	} else if wf.cfg.Service.HTTPAddr != "" {
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
		Addr:    a.wf.cfg.Service.HTTPAddr,
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

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s)\n", a.capability.Name, a.agentID)
	fmt.Fprintf(os.Stderr, "HTTP server listening on %s\n", a.wf.cfg.Service.HTTPAddr)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to NATS
	cfg := bus.NATSConfig{
		URL:  a.wf.cfg.Service.BusURL,
		Name: fmt.Sprintf("agent-%s", a.agentID),
	}
	natsBus, err := bus.NewNATSBus(cfg)
	if err != nil {
		return fmt.Errorf("connecting to bus: %w", err)
	}
	a.bus = natsBus
	defer natsBus.Close()

	// Parse heartbeat interval
	heartbeatInterval := 5 * time.Second
	if a.wf.cfg.Service.HeartbeatInterval != "" {
		if d, err := time.ParseDuration(a.wf.cfg.Service.HeartbeatInterval); err == nil {
			heartbeatInterval = d
		}
	}

	// Start heartbeat sender
	hbSender, err := heartbeat.NewBusSender(heartbeat.SenderConfig{
		Bus:           natsBus,
		AgentID:       a.agentID,
		Interval:      heartbeatInterval,
		InitialStatus: "idle",
	})
	if err != nil {
		return fmt.Errorf("creating heartbeat sender: %w", err)
	}
	a.heartbeat = hbSender
	hbSender.SetMetadata("capability", a.capability.Name)
	hbSender.SetMetadata("version", version)
	if err := hbSender.Start(ctx); err != nil {
		return fmt.Errorf("starting heartbeat: %w", err)
	}
	defer hbSender.Stop()

	// Determine queue group
	a.queueGroup = a.wf.cfg.Service.QueueGroup
	if a.queueGroup == "" {
		a.queueGroup = a.capability.Name + "-workers"
	}

	// Subscribe to task queue
	taskSubject := fmt.Sprintf("tasks.%s", a.capability.Name)
	sub, err := natsBus.QueueSubscribe(taskSubject, a.queueGroup)
	if err != nil {
		return fmt.Errorf("subscribing to %s: %w", taskSubject, err)
	}
	a.taskSub = sub
	defer sub.Unsubscribe()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s)\n", a.capability.Name, a.agentID)
	fmt.Fprintf(os.Stderr, "Connected to bus: %s\n", a.wf.cfg.Service.BusURL)
	fmt.Fprintf(os.Stderr, "Listening on: %s (queue: %s)\n", taskSubject, a.queueGroup)
	fmt.Fprintf(os.Stderr, "Heartbeat interval: %s\n", heartbeatInterval)

	// Main loop
	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nReceived shutdown signal, draining...\n")
			a.initiateBusShutdown(ctx)
			return nil

		case msg, ok := <-sub.Messages():
			if !ok {
				// Subscription closed
				return nil
			}
			a.handleBusTask(ctx, msg)
		}
	}
}

// handleBusTask processes a task received from the bus.
func (a *serviceAgent) handleBusTask(ctx context.Context, msg *bus.Message) {
	// Parse task message
	task, err := tasks.UnmarshalTaskMessage(msg.Data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Invalid task message: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "  → Task received: %s\n", task.TaskID)

	// Update heartbeat status
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("busy")
		a.heartbeat.SetLoad(1.0)
	}

	// Execute task
	result := a.executeTask(ctx, task)

	// Update heartbeat status
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("idle")
		a.heartbeat.SetLoad(0.0)
	}

	// Publish result
	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to marshal result: %v\n", err)
		return
	}

	// Determine result subject
	resultSubject := task.ReplyTo
	if resultSubject == "" {
		resultSubject = fmt.Sprintf("results.%s", task.TaskID)
	}

	if err := a.bus.Publish(resultSubject, resultData); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to publish result: %v\n", err)
		return
	}

	statusIcon := "✓"
	if result.Status == tasks.ResultFailed {
		statusIcon = "✗"
	}
	fmt.Fprintf(os.Stderr, "  %s Task complete: %s (%s, %dms)\n",
		statusIcon, task.TaskID, result.Status, result.DurationMs)
}

// initiateBusShutdown handles graceful shutdown in bus mode.
func (a *serviceAgent) initiateBusShutdown(ctx context.Context) {
	a.status = "draining"

	// Update heartbeat to draining
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("draining")
	}

	// Unsubscribe to stop receiving new tasks
	if a.taskSub != nil {
		a.taskSub.Unsubscribe()
	}

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

	// Heartbeat and bus will be closed by deferred calls in runBusMode
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

	result := tasks.NewTaskResult(task.TaskID, a.agentID, tasks.ResultSuccess)
	result.CorrelationID = task.CorrelationID
	result.Attempt = task.Attempt

	// Create a fresh workflow copy with this task's inputs
	taskWf := &workflow{
		agentfilePath: a.wf.agentfilePath,
		configPath:    a.wf.configPath,
		policyPath:    a.wf.policyPath,
		workspacePath: a.wf.workspacePath,
		inputs:        task.Inputs,
		debug:         a.wf.debug,
		wf:            a.wf.wf,
		cfg:           a.wf.cfg,
		pol:           a.wf.pol,
		baseDir:       a.wf.baseDir,
	}

	// Create runtime (reuses all the setup from run mode)
	rt := newRuntime(taskWf, a.creds)

	// Setup runtime (creates provider, registry, executor, etc.)
	if err := rt.setup(); err != nil {
		result.Status = tasks.ResultFailed
		result.Error = fmt.Sprintf("runtime setup failed: %v", err)
		result.DurationMs = time.Since(start).Milliseconds()
		return result
	}

	// Ensure cleanup
	defer rt.cleanup()

	// Execute workflow
	exitCode := rt.run(ctx)
	if exitCode != 0 {
		result.Status = tasks.ResultFailed
		result.Error = "workflow execution failed"
		// Try to get outputs even on failure
		if rt.sess != nil {
			result.Outputs = rt.sess.Outputs
		}
	} else {
		if rt.sess != nil {
			result.Outputs = rt.sess.Outputs
		}
	}

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

// generateShortID generates a short random ID (8 hex chars).
func generateShortID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if crypto/rand fails
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xFFFFFFFF)
	}
	return hex.EncodeToString(b)
}
