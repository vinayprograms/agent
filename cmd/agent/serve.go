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
	"github.com/vinayprograms/agentkit/bus"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/embedding"
	"github.com/vinayprograms/agentkit/heartbeat"
	"github.com/vinayprograms/agentkit/llm"
	"github.com/vinayprograms/agentkit/registry"
	"github.com/vinayprograms/agentkit/resume"
	"github.com/vinayprograms/agentkit/tasks"
)

// serviceAgent holds the state for a running service agent.
type serviceAgent struct {
	// Reuse workflow loading infrastructure
	wf    *workflow
	creds *credentials.Credentials

	// Agent identity (uses session ID)
	agentID    string
	capability registry.CapabilitySchema
	agentResume *resume.Resume

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
	bus          bus.MessageBus
	heartbeat    *heartbeat.BusSender
	reg          registry.Registry
	taskSubs     []bus.Subscription // work.<cap>.* subscriptions
	discussSub   bus.Subscription   // discuss.* subscription
	queueGroup   string
	embedder     embedding.Embedder // for discuss pre-filter
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

	// Create service agent
	agent := &serviceAgent{
		wf:             wf,
		creds:          creds,
		agentID:        agentID,
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

	// Generate resume from Agentfile (infer capabilities)
	if err := a.generateResume(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Resume generation failed: %v (using fallback capability)\n", err)
	}

	// Register with NATS KV registry
	if err := a.registerWithRegistry(natsBus); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Registry registration failed: %v\n", err)
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
	hbSender.SetMetadata("name", a.capability.Name)
	hbSender.SetMetadata("capability", a.capability.Name)
	hbSender.SetMetadata("version", version)

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

	// Determine queue group
	a.queueGroup = a.wf.cfg.Service.QueueGroup
	if a.queueGroup == "" {
		a.queueGroup = a.capability.Name + "-workers"
	}

	// Subscribe to work topics for each capability (queue sub — one agent gets each task)
	capabilities := a.getCapabilities()
	for _, cap := range capabilities {
		subject := fmt.Sprintf("work.%s.*", cap)
		sub, err := natsBus.QueueSubscribe(subject, a.queueGroup)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to %s: %v\n", subject, err)
			continue
		}
		a.taskSubs = append(a.taskSubs, sub)
	}
	defer func() {
		for _, sub := range a.taskSubs {
			sub.Unsubscribe()
		}
	}()

	// Subscribe to discuss.* (regular sub — all agents see all messages)
	discussSub, err := natsBus.Subscribe("discuss.*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to discuss.*: %v\n", err)
	} else {
		a.discussSub = discussSub
		defer discussSub.Unsubscribe()
	}

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s)\n", a.capability.Name, a.agentID)
	fmt.Fprintf(os.Stderr, "Connected to bus: %s\n", a.wf.cfg.Service.BusURL)
	for _, cap := range capabilities {
		fmt.Fprintf(os.Stderr, "Listening on: work.%s.* (queue: %s)\n", cap, a.queueGroup)
	}
	fmt.Fprintf(os.Stderr, "Listening on: discuss.* (collaborative)\n")
	fmt.Fprintf(os.Stderr, "Heartbeat interval: %s\n", heartbeatInterval)

	// Main loop — multiplex work and discuss channels
	a.runMainLoop(ctx, sigCh)
	return nil
}

// runMainLoop multiplexes work and discuss subscriptions.
func (a *serviceAgent) runMainLoop(ctx context.Context, sigCh chan os.Signal) {
	// Merge all work subscription channels
	workCh := make(chan *bus.Message, 16)
	for _, sub := range a.taskSubs {
		go func(s bus.Subscription) {
			for msg := range s.Messages() {
				workCh <- msg
			}
		}(sub)
	}

	// Discuss channel
	var discussCh <-chan *bus.Message
	if a.discussSub != nil {
		discussCh = a.discussSub.Messages()
	}

	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nReceived shutdown signal, draining...\n")
			a.initiateBusShutdown(ctx)
			return

		case msg, ok := <-workCh:
			if !ok {
				return
			}
			a.handleBusTask(ctx, msg)

		case msg, ok := <-discussCh:
			if !ok {
				discussCh = nil
				continue
			}
			a.handleDiscussMessage(ctx, msg)
		}
	}
}

// generateResume infers capabilities from the Agentfile via small LLM.
func (a *serviceAgent) generateResume(ctx context.Context) error {
	// Build AgentfileInfo from parsed workflow
	af := resume.AgentfileInfo{
		Name: a.wf.wf.Name,
	}
	for _, goal := range a.wf.wf.Goals {
		af.Goals = append(af.Goals, resume.GoalInfo{
			Name:        goal.Name,
			Description: goal.Outcome,
			Outputs:     goal.Outputs,
		})
	}
	for _, input := range a.wf.wf.Inputs {
		af.Inputs = append(af.Inputs, resume.InputInfo{
			Name:    input.Name,
			Default: input.Default,
		})
	}

	// Collect tool names from executor
	var tools []string
	// Tools are available from the workflow config, not the executor directly
	// For now, leave empty — the LLM infers capabilities from goals primarily

	// Use small LLM for inference if available
	var resumeLLM resume.LLM
	if a.serviceRuntime != nil && a.serviceRuntime.smallLLM != nil {
		resumeLLM = &smallLLMAdapter{provider: a.serviceRuntime.smallLLM}
	}

	if resumeLLM != nil {
		r, err := resume.GenerateFromAgentfile(ctx, a.agentID, af, tools, resumeLLM)
		if err != nil {
			return err
		}
		a.agentResume = r
		fmt.Fprintf(os.Stderr, "📋 Resume: %s — %s\n", r.Name, r.Description)
		fmt.Fprintf(os.Stderr, "   Capabilities: %v\n", r.Capabilities)

		// Embed the resume if embedding provider is configured
		if err := a.embedResume(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Resume embedding failed: %v\n", err)
		}
	}

	return nil
}

// embedResume generates and attaches an embedding vector to the agent's resume.
func (a *serviceAgent) embedResume(ctx context.Context) error {
	cfg := a.wf.cfg.Embedding
	if cfg.Provider == "" || cfg.Provider == "none" {
		return nil
	}

	// Resolve API key from credentials if not in config
	apiKey := cfg.APIKey
	if apiKey == "" && a.creds != nil {
		apiKey = a.creds.GetAPIKey(cfg.Provider)
	}

	embedder, err := embedding.New(embedding.Config{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		APIKey:   apiKey,
		BaseURL:  cfg.BaseURL,
	})
	if err != nil {
		return fmt.Errorf("creating embedder: %w", err)
	}
	if embedder == nil {
		return nil
	}

	if err := resume.EmbedResume(ctx, a.agentResume, embedder); err != nil {
		return fmt.Errorf("embedding resume: %w", err)
	}

	// Store embedder for discuss pre-filter
	a.embedder = embedder

	fmt.Fprintf(os.Stderr, "🧮 Resume embedded (%d dimensions)\n", len(a.agentResume.Embedding))
	return nil
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
		Metadata:     map[string]string{"version": version},
	}

	// Attach embedding if resume has one
	if a.agentResume != nil && len(a.agentResume.Embedding) > 0 {
		info.Embedding = a.agentResume.Embedding
	}

	if err := natsReg.Register(info); err != nil {
		return fmt.Errorf("registering agent: %w", err)
	}

	fmt.Fprintf(os.Stderr, "📝 Registered with NATS KV registry\n")
	return nil
}

// getCapabilities returns the merged list of inferred + configured capabilities.
func (a *serviceAgent) getCapabilities() []string {
	seen := make(map[string]bool)
	var caps []string

	// Inferred capabilities from resume
	if a.agentResume != nil {
		for _, c := range a.agentResume.Capabilities {
			if !seen[c] {
				seen[c] = true
				caps = append(caps, c)
			}
		}
	}

	// Extra capabilities from config
	for _, c := range a.wf.cfg.Service.Capabilities {
		if !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}

	// Fallback: use capability name if nothing inferred
	if len(caps) == 0 {
		caps = []string{a.capability.Name}
	}

	return caps
}

// Minimum cosine similarity for a discuss message to pass embedding pre-filter.
const discussSimilarityThreshold = 0.3

// handleDiscussMessage processes a message from the discuss.* topic.
// Round 1: Embedding pre-filter (fast, cheap — skip irrelevant messages).
// Round 2: Small LLM triage (decide: execute, comment, or skip).
func (a *serviceAgent) handleDiscussMessage(ctx context.Context, msg *bus.Message) {
	task, err := tasks.UnmarshalTaskMessage(msg.Data)
	if err != nil {
		return
	}

	// Filter own messages
	if task.SubmittedBy == a.agentID {
		return
	}
	if task.Metadata != nil && task.Metadata["agent_id"] == a.agentID {
		return
	}

	fmt.Fprintf(os.Stderr, "  💬 Discussion: %s\n", task.TaskID)

	// --- Round 1: Embedding pre-filter ---
	if !a.discussEmbeddingFilter(ctx, task) {
		return
	}

	// --- Round 2: Small LLM triage ---
	decision := a.discussLLMTriage(ctx, task)
	switch decision {
	case "EXECUTE":
		fmt.Fprintf(os.Stderr, "  💬 Decision: EXECUTE — taking task %s\n", task.TaskID)
		a.handleBusTask(ctx, msg)
	case "COMMENT":
		fmt.Fprintf(os.Stderr, "  💬 Decision: COMMENT — contributing to %s\n", task.TaskID)
		a.publishDiscussComment(ctx, task)
	default:
		fmt.Fprintf(os.Stderr, "  💬 Decision: SKIP — not relevant to %s\n", a.capability.Name)
	}
}

// discussEmbeddingFilter checks if a discuss message is semantically relevant
// to this agent's resume. Returns true if relevant (or if embedding is unavailable).
func (a *serviceAgent) discussEmbeddingFilter(ctx context.Context, task *tasks.TaskMessage) bool {
	// Skip filter if no embedder or no resume embedding
	if a.embedder == nil || a.agentResume == nil || len(a.agentResume.Embedding) == 0 {
		return true // Can't filter — pass through to round 2
	}

	// Build task text for embedding
	taskText := buildTaskText(task)

	// Embed the task
	taskVec, err := a.embedder.Embed(ctx, taskText)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Embedding failed: %v (passing to round 2)\n", err)
		return true // Fail open to round 2
	}

	score := resume.CosineSimilarity(a.agentResume.Embedding, taskVec)
	fmt.Fprintf(os.Stderr, "  🧮 Similarity: %.3f (threshold: %.3f)\n", score, discussSimilarityThreshold)

	if score < discussSimilarityThreshold {
		fmt.Fprintf(os.Stderr, "  💬 Filtered: low relevance to %s\n", a.capability.Name)
		return false
	}
	return true
}

// discussLLMTriage asks the small LLM whether this agent should execute,
// comment on, or skip a discuss message.
func (a *serviceAgent) discussLLMTriage(ctx context.Context, task *tasks.TaskMessage) string {
	if a.serviceRuntime == nil || a.serviceRuntime.smallLLM == nil {
		return "EXECUTE" // No LLM available — default to execute
	}

	taskText := buildTaskText(task)

	// Build resume summary
	resumeSummary := a.capability.Name
	if a.agentResume != nil {
		resumeSummary = a.agentResume.ToText()
	}

	prompt := fmt.Sprintf(`You are deciding whether an agent should act on a collaborative task.

AGENT PROFILE:
%s

TASK:
%s

Decide the agent's action. Reply with exactly one word:
- EXECUTE — this task matches the agent's capabilities and the agent should do the work
- COMMENT — the agent has relevant expertise to contribute insights but should not do the full task
- SKIP — this task is outside the agent's expertise

Your answer:`, resumeSummary, taskText)

	llm := &smallLLMAdapter{provider: a.serviceRuntime.smallLLM}
	resp, err := llm.Complete(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Triage LLM failed: %v (defaulting to SKIP)\n", err)
		return "SKIP" // Fail closed — don't execute on LLM error
	}

	// Parse response — first word only
	decision := strings.ToUpper(strings.TrimSpace(strings.SplitN(resp, "\n", 2)[0]))
	switch decision {
	case "EXECUTE", "COMMENT", "SKIP":
		return decision
	default:
		fmt.Fprintf(os.Stderr, "  ⚠️  Unclear triage response: %q (defaulting to SKIP)\n", resp)
		return "SKIP"
	}
}

// publishDiscussComment runs the agent on the task but publishes the result
// as a comment on the discuss topic (not as a done.* result).
func (a *serviceAgent) publishDiscussComment(ctx context.Context, task *tasks.TaskMessage) {
	result := a.executeTask(ctx, task)

	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to marshal comment: %v\n", err)
		return
	}

	// Publish comment back to the discuss topic
	replyTo := task.ReplyTo
	if replyTo == "" {
		replyTo = fmt.Sprintf("discuss.%s", task.TaskID)
	}

	if err := a.bus.Publish(replyTo, resultData); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to publish comment: %v\n", err)
	}
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

	// Determine result subject: done.<capability>.<task_id>
	resultSubject := task.ReplyTo
	if resultSubject == "" {
		resultSubject = fmt.Sprintf("done.%s.%s", a.capability.Name, task.TaskID)
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
	if a.discussSub != nil {
		a.discussSub.Unsubscribe()
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
	execResult, err := a.serviceRuntime.exec.Run(ctx, task.Inputs)
	if err != nil {
		result.Status = tasks.ResultFailed
		result.Error = err.Error()
	} else if execResult.Status != "complete" {
		result.Status = tasks.ResultFailed
		result.Error = fmt.Sprintf("workflow status: %s", execResult.Status)
		result.Outputs = execResult.Outputs
	} else {
		result.Outputs = execResult.Outputs
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

// smallLLMAdapter adapts agentkit's llm.Provider to resume.LLM interface.
type smallLLMAdapter struct {
	provider llm.Provider
}

func (a *smallLLMAdapter) Complete(ctx context.Context, prompt string) (string, error) {
	resp, err := a.provider.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
