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
	"strings"
	"syscall"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/swarm"
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

	// Agent identity (uses session ID)
	agentID     string
	instanceID  string // <name>-<session-id> for swarm addressing
	displayName string // swarm agent name (or Agentfile NAME if standalone)
	agentType   string // "worker" (default) or "manager"
	capability  registry.CapabilitySchema


	// Service-level session (shared across all tasks)
	serviceRuntime *runtime

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
	reg         registry.Registry
	taskSubs    []bus.Subscription // work.<cap>.* subscriptions
	instanceSub bus.Subscription   // work.<instance-id>.* for corrections
	discussSub  bus.Subscription   // discuss.* subscription (manager only — read)
	controlSub  bus.Subscription   // control.<id>.shutdown subscription
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
		yolo:          cmd.Yolo,
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
	if cmd.Storage != "" {
		wf.cfg.Storage.Path = cmd.Storage
	}
	if cmd.SessionLabel != "" {
		wf.sessionLabel = cmd.SessionLabel
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

	// Create service-level runtime (one session for entire service lifetime)
	serviceRt := newRuntime(wf, creds)
	if err := serviceRt.setup(); err != nil {
		return fmt.Errorf("setting up service runtime: %w", err)
	}

	// Agent ID uses session ID (or config if specified)
	agentID := wf.cfg.Agent.ID
	if agentID == "" {
		// Auto-generate: <capability>-<session-id>
		agentID = fmt.Sprintf("%s-%s", capabilityName, serviceRt.sess.ID)
	}

	// Display name: swarm agent name if available, otherwise Agentfile NAME
	displayName := wf.wf.Name
	if wf.sessionLabel != "" {
		displayName = wf.sessionLabel
	}

	// Determine agent type from environment (set by swarm up)
	agentType := os.Getenv("AGENT_TYPE")
	if agentType == "" {
		agentType = "worker"
	}

	// Instance ID: <displayName>-<session-id> for swarm addressing
	instanceID := fmt.Sprintf("%s-%s", displayName, serviceRt.sess.ID)

	// Create service agent
	agent := &serviceAgent{
		wf:             wf,
		creds:          creds,
		agentID:        agentID,
		instanceID:     instanceID,
		displayName:    displayName,
		agentType:      agentType,
		capability:     capability,
		serviceRuntime: serviceRt,
		status:         "idle",
		taskDone:       make(chan struct{}),
		drainTimeout:   drainTimeout,
	}

	// Ensure cleanup on exit
	defer serviceRt.cleanup()

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

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s, capability: %s)\n", a.wf.wf.Name, a.agentID, a.capability.Name)
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

	// Register with NATS KV registry (for discovery)
	if err := a.registerWithRegistry(natsBus); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Registry registration failed: %v (continuing without registry)\n", err)
	}

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
	hbSender.SetMetadata("name", a.displayName)
	hbSender.SetMetadata("instance_id", a.instanceID)
	hbSender.SetMetadata("session_id", a.serviceRuntime.sess.ID)
	hbSender.SetMetadata("capability", a.capability.Name)
	hbSender.SetMetadata("type", a.agentType)
	hbSender.SetMetadata("version", version)

	// Wire metrics collector for dashboard reporting
	mc := heartbeat.NewMetricsCollector(hbSender)
	a.serviceRuntime.exec.SetMetricsCollector(mc)

	// Add registry TTL touch to heartbeat callback
	if a.reg != nil {
		hbSender.SetCallback(func() {
			if err := a.reg.Touch(a.agentID); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Registry touch failed: %v\n", err)
			}
		})
	}

	if err := hbSender.Start(ctx); err != nil {
		return fmt.Errorf("starting heartbeat: %w", err)
	}
	fmt.Fprintf(os.Stderr, "📡 Heartbeat started on subject: heartbeat.%s\n", a.agentID)
	defer hbSender.Stop()

	// Ensure JetStream stream exists for durable messaging
	if _, err := swarm.EnsureStream(natsBus.Conn()); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  JetStream unavailable: %v (messages may be lost)\n", err)
	}

	// Determine queue group
	a.queueGroup = a.wf.cfg.Service.QueueGroup
	if a.queueGroup == "" {
		a.queueGroup = a.capability.Name + "-workers"
	}

	// Subscribe to work.<capability>.* (task assignment via queue group)
	cap := a.getCapabilities()[0]
	workSubject := fmt.Sprintf("work.%s.*", cap)
	workSub, err := natsBus.QueueSubscribe(workSubject, a.queueGroup)
	if err != nil {
		return fmt.Errorf("subscribing to %s: %w", workSubject, err)
	}
	a.taskSubs = append(a.taskSubs, workSub)
	defer func() {
		for _, sub := range a.taskSubs {
			sub.Unsubscribe()
		}
	}()

	// Subscribe to work.<instance-id>.* (corrective guidance → interrupt buffer)
	instanceSubject := fmt.Sprintf("work.%s.*", a.instanceID)
	instanceSub, err := natsBus.Subscribe(instanceSubject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to %s: %v\n", instanceSubject, err)
	} else {
		a.instanceSub = instanceSub
		defer instanceSub.Unsubscribe()
	}

	// Manager-specific: subscribe to discuss.* for monitoring worker progress
	if a.agentType == "manager" {
		discussSub, err := natsBus.Subscribe("discuss.*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to discuss.*: %v\n", err)
		} else {
			a.discussSub = discussSub
			defer discussSub.Unsubscribe()
		}
	}

	// Subscribe to control.<agentID>.shutdown for remote shutdown via `swarm down`
	controlSubject := fmt.Sprintf("control.%s.shutdown", a.agentID)
	controlSub, err := natsBus.Subscribe(controlSubject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to %s: %v\n", controlSubject, err)
	} else {
		a.controlSub = controlSub
		defer controlSub.Unsubscribe()
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s, instance: %s, type: %s, capability: %s)\n",
		a.wf.wf.Name, a.agentID, a.instanceID, a.agentType, a.capability.Name)
	fmt.Fprintf(os.Stderr, "Connected to bus: %s\n", a.wf.cfg.Service.BusURL)
	fmt.Fprintf(os.Stderr, "Listening on: work.%s.* (queue: %s)\n", cap, a.queueGroup)
	fmt.Fprintf(os.Stderr, "Listening on: work.%s.* (corrections)\n", a.instanceID)
	if a.agentType == "manager" {
		fmt.Fprintf(os.Stderr, "Listening on: discuss.* (manager — monitoring workers)\n")
	}
	fmt.Fprintf(os.Stderr, "Heartbeat interval: %s\n", heartbeatInterval)

	// Main loop
	a.runMainLoop(ctx, sigCh)
	return nil
}

func (a *serviceAgent) runMainLoop(ctx context.Context, sigCh chan os.Signal) {
	// Merge all work subscription channels (capability + instance)
	workCh := make(chan *bus.Message, 16)
	for _, sub := range a.taskSubs {
		go func(s bus.Subscription) {
			for msg := range s.Messages() {
				workCh <- msg
			}
		}(sub)
	}

	// Instance channel (corrections → interrupt buffer during execution)
	var instanceCh <-chan *bus.Message
	if a.instanceSub != nil {
		instanceCh = a.instanceSub.Messages()
	}

	// Discuss channel (manager only — monitoring worker progress)
	var discussCh <-chan *bus.Message
	if a.discussSub != nil {
		discussCh = a.discussSub.Messages()
	}

	// Control channel (remote shutdown via `swarm down`)
	var controlCh <-chan *bus.Message
	if a.controlSub != nil {
		controlCh = a.controlSub.Messages()
	}

	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nReceived shutdown signal, draining...\n")
			a.initiateBusShutdown(ctx)
			return

		case _, ok := <-controlCh:
			if !ok {
				controlCh = nil
				continue
			}
			fmt.Fprintf(os.Stderr, "\nReceived remote shutdown signal, draining...\n")
			a.initiateBusShutdown(ctx)
			return

		case msg, ok := <-workCh:
			if !ok {
				return
			}
			a.handleBusTask(ctx, msg)

		case msg, ok := <-instanceCh:
			if !ok {
				instanceCh = nil
				continue
			}
			// Corrective guidance from manager/human → interrupt buffer
			a.handleInstanceMessage(msg)

		case msg, ok := <-discussCh:
			if !ok {
				discussCh = nil
				continue
			}
			// Manager only: monitor worker progress
			a.handleManagerDiscussMessage(ctx, msg)
		}
	}
}

// handleInstanceMessage processes a corrective guidance message from
// work.<instance-id>.* and pushes it into the interrupt buffer.
func (a *serviceAgent) handleInstanceMessage(msg *bus.Message) {
	buf := a.serviceRuntime.exec.InterruptBuffer()
	if buf == nil {
		// Not currently executing — log and discard
		fmt.Fprintf(os.Stderr, "  ⚠️  Correction received while idle (discarded): %s\n", string(msg.Data))
		return
	}

	// Parse the message — could be a TaskMessage or raw text
	var content string
	var from string

	task, err := tasks.UnmarshalTaskMessage(msg.Data)
	if err == nil {
		content = buildTaskText(task)
		from = task.SubmittedBy
		if from == "" {
			from = "orchestrator"
		}
	} else {
		// Raw text correction
		content = string(msg.Data)
		from = "orchestrator"
	}

	buf.Push(executor.InterruptMessage{
		From:      from,
		Timestamp: time.Now(),
		Content:   content,
		TaskID:    extractTaskIDFromSubject(msg.Subject),
	})
	fmt.Fprintf(os.Stderr, "  📨 Correction received from %s → interrupt buffer\n", from)
}

// handleManagerDiscussMessage processes worker updates on discuss.* (manager only).
// For now, this logs the update. Future: feed into manager's reasoning context.
func (a *serviceAgent) handleManagerDiscussMessage(ctx context.Context, msg *bus.Message) {
	// Parse discuss message
	var update struct {
		InstanceID string `json:"instance_id"`
		TaskID     string `json:"task_id"`
		Goal       string `json:"goal"`
		Content    string `json:"content"`
		Timestamp  string `json:"timestamp"`
	}
	if err := json.Unmarshal(msg.Data, &update); err != nil {
		// Try as TaskResult format
		var result tasks.TaskResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "  📢 [%s] %s: %v\n", result.AgentID, result.TaskID, result.Outputs)
		return
	}

	if update.InstanceID != "" {
		fmt.Fprintf(os.Stderr, "  📢 [%s] %s/%s: %s\n",
			update.InstanceID, update.TaskID, update.Goal,
			truncateStr(update.Content, 120))
	}
}

// extractTaskIDFromSubject extracts the task ID from a NATS subject like work.<id>.<task_id>.
func extractTaskIDFromSubject(subject string) string {
	parts := strings.Split(subject, ".")
	if len(parts) >= 3 {
		return parts[len(parts)-1]
	}
	return ""
}

// truncateStr truncates a string to max length with ellipsis.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}



// registerWithRegistry registers the agent's resume with NATS KV.
func (a *serviceAgent) registerWithRegistry(natsBus *bus.NATSBus) error {
	conn := natsBus.Conn()
	if conn == nil {
		return fmt.Errorf("no NATS connection")
	}

	regCfg := registry.DefaultNATSRegistryConfig()
	natsReg, err := registry.NewNATSRegistry(conn, regCfg)
	if err != nil {
		return fmt.Errorf("creating registry: %w", err)
	}
	a.reg = natsReg

	// Build agent info for registry
	info := registry.AgentInfo{
		ID:           a.agentID,
		Name:         a.capability.Name,
		Capabilities: a.getCapabilities(),
		Status:       registry.StatusIdle,
		Load:         0,
		Metadata: map[string]string{
			"version":     version,
			"instance_id": a.instanceID,
			"type":        a.agentType,
		},
	}

	if err := natsReg.Register(info); err != nil {
		return fmt.Errorf("registering agent: %w", err)
	}

	fmt.Fprintf(os.Stderr, "📝 Registered with NATS KV registry\n")
	return nil
}

// getCapabilities returns the single announced capability for NATS subjects and registry.
// Priority: --capability CLI > capability: in config > Agentfile NAME.
func (a *serviceAgent) getCapabilities() []string {
	if a.capability.Name != "" {
		return []string{a.capability.Name}
	}
	return []string{a.wf.wf.Name}
}


// buildTaskText extracts a readable text representation from a task message.
func buildTaskText(task *tasks.TaskMessage) string {
	parts := []string{}
	if task.Capability != "" {
		parts = append(parts, "Capability: "+task.Capability)
	}
	for k, v := range task.Inputs {
		parts = append(parts, k+": "+v)
	}
	if len(parts) == 0 {
		return task.TaskID
	}
	return strings.Join(parts, "\n")
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
		a.heartbeat.SetStatus("executing")
		a.heartbeat.SetLoad(1.0)
		a.heartbeat.SetMetadata("executing_since", fmt.Sprintf("%d", time.Now().UnixMilli()))
		a.heartbeat.SetMetadata("current_task", task.TaskID)
	}

	// Set up interrupt buffer for corrective guidance from work.<instance-id>.*
	interruptBuf := executor.NewInterruptBuffer()
	a.serviceRuntime.exec.SetInterruptBuffer(interruptBuf)
	defer a.serviceRuntime.exec.SetInterruptBuffer(nil)

	// Set up discuss publisher — publishes non-tool-call LLM output to discuss.*
	taskID := task.TaskID
	a.serviceRuntime.exec.SetDiscussPublisher(func(goalName, content string) {
		a.publishToDiscuss(taskID, goalName, content)
	})
	defer a.serviceRuntime.exec.ClearDiscussPublisher()

	// Execute task
	result := a.executeTask(ctx, task)

	// Update heartbeat status
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("idle")
		a.heartbeat.SetLoad(0.0)
		a.heartbeat.SetMetadata("executing_since", "")
		a.heartbeat.SetMetadata("current_task", "")
	}

	// Publish result to done.<capability>.<task_id>
	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to marshal result: %v\n", err)
		return
	}

	resultSubject := task.ReplyTo
	if resultSubject == "" {
		resultSubject = fmt.Sprintf("done.%s.%s", a.capability.Name, task.TaskID)
	}

	if err := a.bus.Publish(resultSubject, resultData); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to publish result: %v\n", err)
		return
	}

	// Publish final completion update to discuss.*
	finalContent := fmt.Sprintf("%v", result.Outputs)
	if result.Status == tasks.ResultFailed {
		finalContent = fmt.Sprintf("FAILED: %s", result.Error)
	}
	a.publishToDiscuss(task.TaskID, "complete", finalContent)

	statusIcon := "✓"
	if result.Status == tasks.ResultFailed {
		statusIcon = "✗"
	}
	fmt.Fprintf(os.Stderr, "  %s Task complete: %s (%s, %dms)\n",
		statusIcon, task.TaskID, result.Status, result.DurationMs)
}

// publishToDiscuss publishes a worker's update to discuss.<task_id>.
func (a *serviceAgent) publishToDiscuss(taskID, goalName, content string) {
	if a.bus == nil || content == "" {
		return
	}

	update := map[string]string{
		"instance_id": a.instanceID,
		"task_id":     taskID,
		"goal":        goalName,
		"content":     content,
		"timestamp":   time.Now().Format(time.RFC3339),
	}

	data, err := json.Marshal(update)
	if err != nil {
		return
	}

	subject := fmt.Sprintf("discuss.%s", taskID)
	if err := a.bus.Publish(subject, data); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Failed to publish to discuss: %v\n", err)
	}
}

// initiateBusShutdown handles graceful shutdown in bus mode.
func (a *serviceAgent) initiateBusShutdown(ctx context.Context) {
	a.status = "draining"

	// Update heartbeat to draining
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("draining")
	}

	// Deregister from NATS KV registry
	if a.reg != nil {
		if err := a.reg.Deregister(a.agentID); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Registry deregister failed: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "📝 Deregistered from registry\n")
		}
	}

	// Unsubscribe to stop receiving new tasks
	for _, sub := range a.taskSubs {
		sub.Unsubscribe()
	}
	if a.instanceSub != nil {
		a.instanceSub.Unsubscribe()
	}
	if a.discussSub != nil {
		a.discussSub.Unsubscribe()
	}
	if a.controlSub != nil {
		a.controlSub.Unsubscribe()
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

	// Execute workflow using service runtime's executor
	// All tasks share the same session, provider, tools, etc.
	inputs := task.Inputs
	// Inject revision context if present (discuss follow-up rounds)
	if task.Metadata != nil && task.Metadata["revision_context"] != "" {
		inputs = make(map[string]string, len(task.Inputs))
		for k, v := range task.Inputs {
			inputs[k] = v
		}
		inputs["_revision_context"] = task.Metadata["revision_context"]
	}
	execResult, err := a.serviceRuntime.exec.Run(ctx, inputs)
	if err != nil {
		result.Status = tasks.ResultFailed
		result.Error = err.Error()
		fmt.Fprintf(os.Stderr, "  ✗ Execution error: %v\n", err)
	} else if execResult.Status != "complete" {
		result.Status = tasks.ResultFailed
		result.Error = fmt.Sprintf("workflow status: %s", execResult.Status)
		result.Outputs = execResult.Outputs
		fmt.Fprintf(os.Stderr, "  ✗ Workflow failed with status: %s\n", execResult.Status)
		if execResult.Error != "" {
			fmt.Fprintf(os.Stderr, "     Error: %s\n", execResult.Error)
		}
	} else {
		result.Outputs = execResult.Outputs
	}

	result.DurationMs = time.Since(start).Milliseconds()
	if result.Metadata == nil {
		result.Metadata = make(map[string]string)
	}
	result.Metadata["capability"] = a.capability.Name
	result.Metadata["name"] = a.displayName
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

// stripMarkdownFences removes ```lang ... ``` wrapping from LLM responses.
func stripMarkdownFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```") {
		if idx := strings.Index(trimmed, "\n"); idx != -1 {
			trimmed = trimmed[idx+1:]
		}
		if strings.HasSuffix(trimmed, "```") {
			trimmed = trimmed[:len(trimmed)-3]
		}
		return strings.TrimSpace(trimmed)
	}
	return s
}
