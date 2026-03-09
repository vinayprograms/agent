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
	agentID     string
	displayName string // swarm agent name (or Agentfile NAME if standalone)
	capability  registry.CapabilitySchema
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
	bus        bus.MessageBus
	heartbeat  *heartbeat.BusSender
	reg        registry.Registry
	taskSubs   []bus.Subscription // work.<cap>.* subscriptions
	discussSub bus.Subscription   // discuss.* subscription
	controlSub bus.Subscription   // control.<id>.shutdown subscription
	queueGroup string
	embedder   embedding.Embedder // for discuss pre-filter

	// Track tasks this agent has executed (context for revisions)
	executedTasks map[string]*taskExecution

	// Swarm collaboration context (nil in non-swarm mode)
	swarmContext *executor.SwarmContext
}

// taskExecution tracks an agent's prior work on a task for discuss revisions.
type taskExecution struct {
	rounds int    // how many times this agent has executed this task
	output string // agent's last output (for revision context)
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

	// Create service agent
	agent := &serviceAgent{
		wf:             wf,
		creds:          creds,
		agentID:        agentID,
		displayName:    displayName,
		capability:     capability,
		serviceRuntime: serviceRt,
		status:         "idle",
		taskDone:       make(chan struct{}),
		drainTimeout:   drainTimeout,
		executedTasks:  make(map[string]*taskExecution),
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

	// Generate resume from Agentfile (required for collaboration)
	// Retry with backoff — LLM calls can timeout under rate limiting or cold starts
	var resumeErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resumeCtx, resumeCancel := context.WithTimeout(ctx, 30*time.Second)
		resumeErr = a.generateResume(resumeCtx)
		resumeCancel()
		if resumeErr == nil {
			break
		}
		if attempt < 3 {
			backoff := time.Duration(attempt) * 2 * time.Second
			fmt.Fprintf(os.Stderr, "⚠️  Resume generation attempt %d/3 failed: %v (retrying in %s)\n", attempt, resumeErr, backoff)
			time.Sleep(backoff)
		}
	}
	if resumeErr != nil {
		return fmt.Errorf("resume generation failed after 3 attempts (required for swarm collaboration): %w", resumeErr)
	}

	// Register with NATS KV registry (required for discovery)
	if err := a.registerWithRegistry(natsBus); err != nil {
		return fmt.Errorf("registry registration failed (required for swarm discovery): %w", err)
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
		InitialStatus: "monitoring",
	})
	if err != nil {
		return fmt.Errorf("creating heartbeat sender: %w", err)
	}
	a.heartbeat = hbSender
	hbSender.SetMetadata("name", a.displayName)
	hbSender.SetMetadata("session_id", a.serviceRuntime.sess.ID)
	hbSender.SetMetadata("capability", a.capability.Name)
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

	// Determine queue group
	a.queueGroup = a.wf.cfg.Service.QueueGroup
	if a.queueGroup == "" {
		a.queueGroup = a.capability.Name + "-workers"
	}

	// Subscribe to work.<capability>.* (single capability per agent)
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

	// === REPLAY PHASE ===
	// Ensure JetStream stream exists, then replay history to build swarm context.
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("replay")
	}
	js, err := swarm.EnsureStream(natsBus.Conn())
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  JetStream unavailable: %v (continuing without replay)\n", err)
		a.swarmContext = executor.NewSwarmContext()
	} else {
		catchupSeq, err := swarm.LastSequence(js)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Failed to get stream sequence: %v\n", err)
			a.swarmContext = executor.NewSwarmContext()
		} else {
			result, err := swarm.Replay(js, catchupSeq, a.agentID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  Replay failed: %v (starting with empty context)\n", err)
				a.swarmContext = executor.NewSwarmContext()
			} else {
				a.swarmContext = result.SwarmContext
				if result.MessagesRead > 0 {
					fmt.Fprintf(os.Stderr, "📼 Replay complete: %d messages, caught up to seq %d\n",
						result.MessagesRead, result.CatchupSeq)
				}
			}
		}
	}
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("monitoring")
	}

	// Subscribe to discuss.* (regular sub — all agents see all messages)
	discussSub, err := natsBus.Subscribe("discuss.*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to subscribe to discuss.*: %v\n", err)
	} else {
		a.discussSub = discussSub
		defer discussSub.Unsubscribe()
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

	fmt.Fprintf(os.Stderr, "Service agent: %s (ID: %s, capability: %s)\n", a.wf.wf.Name, a.agentID, a.capability.Name)
	fmt.Fprintf(os.Stderr, "Connected to bus: %s\n", a.wf.cfg.Service.BusURL)
	fmt.Fprintf(os.Stderr, "Listening on: work.%s.* (queue: %s)\n", cap, a.queueGroup)
	fmt.Fprintf(os.Stderr, "Listening on: discuss.* (collaborative)\n")
	fmt.Fprintf(os.Stderr, "Heartbeat interval: %s\n", heartbeatInterval)

	// === POST-REPLAY DELIBERATION ===
	// After replay, the agent has full swarm context. Evaluate if there's
	// unclaimed work or open questions this agent should address.
	a.postReplayDeliberation(ctx)

	// Main loop — multiplex work and discuss channels
	a.runMainLoop(ctx, sigCh)
	return nil
}

// runMainLoop multiplexes work and discuss subscriptions.
// postReplayDeliberation evaluates the swarm state after replay and
// determines if there is unclaimed work matching this agent's capability.
// This is the REPLAY → DELIBERATING transition from §2.1.
func (a *serviceAgent) postReplayDeliberation(ctx context.Context) {
	if a.swarmContext == nil {
		return
	}

	// Check if there are any open task discussions
	states := a.swarmContext.GetAgentStates()
	completed := a.swarmContext.GetCompleted()

	// If no other agents are visible and nothing is completed,
	// the swarm is either fresh or this agent is the first to join.
	if len(states) == 0 && len(completed) == 0 {
		fmt.Fprintf(os.Stderr, "🔍 Post-replay: no swarm activity found, entering MONITORING\n")
		return
	}

	fmt.Fprintf(os.Stderr, "🔍 Post-replay: %d agents visible, %d tasks completed — checking for unclaimed work\n",
		len(states), len(completed))

	// Use the small LLM to evaluate the swarm state against this agent's capability.
	if a.serviceRuntime == nil || a.serviceRuntime.smallLLM == nil {
		return
	}

	swarmState := a.swarmContext.FormatForLLM("")
	resumeSummary := a.capability.Name
	if a.agentResume != nil {
		resumeSummary = a.agentResume.ToText()
	}

	prompt := fmt.Sprintf(`You just joined an active swarm. Review the current state and determine if there is unclaimed work that matches your capability.

YOUR PROFILE:
%s

CURRENT SWARM STATE:
%s

Is there unclaimed work you should take on? Reply with:
CLAIM: <what you will work on>
SKIP (if nothing needs your attention)`, resumeSummary, swarmState)

	llmAdapter := &smallLLMAdapter{provider: a.serviceRuntime.smallLLM}
	resp, err := llmAdapter.Complete(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Post-replay deliberation failed: %v\n", err)
		return
	}

	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(strings.ToUpper(resp), "CLAIM") {
		msg := strings.TrimPrefix(resp, "CLAIM:")
		msg = strings.TrimPrefix(msg, "CLAIM")
		msg = strings.TrimSpace(msg)
		fmt.Fprintf(os.Stderr, "  💬 Post-replay CLAIM: %s\n", msg)
		// TODO: construct a TaskMessage from the CLAIM and trigger execution
		// For now, log the intent — full task construction requires knowing
		// which task ID to claim against, which needs more context.
	} else {
		fmt.Fprintf(os.Stderr, "  🔍 Post-replay: nothing to claim, entering MONITORING\n")
	}
}

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
		fmt.Fprintf(os.Stderr, "   Resume capabilities: %v (internal, for triage)\n", r.Capabilities)
		fmt.Fprintf(os.Stderr, "   Announced capability: %s\n", a.getCapabilities()[0])

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

// getCapabilities returns the single announced capability for NATS subjects and registry.
// Priority: --capability CLI > capability: in config > Agentfile NAME.
func (a *serviceAgent) getCapabilities() []string {
	if a.capability.Name != "" {
		return []string{a.capability.Name}
	}
	return []string{a.wf.wf.Name}
}

// Minimum cosine similarity for a discuss message to pass embedding pre-filter.
const discussSimilarityThreshold = 0.3

// handleDiscussMessage processes a message from the discuss.* topic.
// This is the DELIBERATING state: the agent evaluates the message and
// decides whether to CLAIM (→ EXECUTING), respond with NEED_INFO, or stay silent.
// Round 1: Embedding pre-filter (fast, cheap — skip irrelevant messages).
// Round 2: Small LLM deliberation (decide: CLAIM, NEED_INFO, or SKIP).
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

	// Agent addressing: if target_agent is set, skip unless it matches us
	if task.Metadata != nil && task.Metadata["target_agent"] != "" {
		target := task.Metadata["target_agent"]
		if target != a.agentID && target != a.capability.Name && target != a.wf.wf.Name && target != a.displayName {
			return
		}
		fmt.Fprintf(os.Stderr, "  💬 Addressed to me: %s\n", task.TaskID)
	}

	fmt.Fprintf(os.Stderr, "  💬 Discussion: %s\n", task.TaskID)

	// Update swarm context with the incoming message
	if a.swarmContext != nil {
		signal := ""
		if task.Metadata != nil {
			signal = task.Metadata["signal"]
		}
		a.swarmContext.AddDiscussMessage(task.TaskID, executor.DiscussMessage{
			From:      task.SubmittedBy,
			Timestamp: time.Now(),
			Content:   buildTaskText(task),
			Signal:    signal,
		})
	}

	// Enter DELIBERATING
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("deliberating")
		a.heartbeat.SetMetadata("current_task", task.TaskID)
	}
	defer func() {
		if a.heartbeat != nil {
			a.heartbeat.SetStatus("monitoring")
			a.heartbeat.SetMetadata("current_task", "")
		}
	}()

	// --- Round 1: Embedding pre-filter ---
	if !a.discussEmbeddingFilter(ctx, task) {
		return
	}

	// --- Round 2: Small LLM deliberation ---
	decision, response := a.deliberate(ctx, task)
	switch decision {
	case "CLAIM":
		fmt.Fprintf(os.Stderr, "  💬 Decision: CLAIM — taking task %s\n", task.TaskID)
		// Publish CLAIM to discuss.* so other agents see it
		a.publishDeliberationResponse(ctx, task, "CLAIM", response)
		// Transition to EXECUTING
		a.handleBusTask(ctx, msg)
	case "NEED_INFO":
		fmt.Fprintf(os.Stderr, "  💬 Decision: NEED_INFO — responding to %s\n", task.TaskID)
		a.publishDeliberationResponse(ctx, task, "NEED_INFO", response)
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

// deliberate evaluates a discuss message and decides the agent's response.
// Returns (decision, response_text) where decision is CLAIM, NEED_INFO, or SKIP.
func (a *serviceAgent) deliberate(ctx context.Context, task *tasks.TaskMessage) (string, string) {
	if a.serviceRuntime == nil || a.serviceRuntime.smallLLM == nil {
		return "CLAIM", "" // No LLM available — default to claim
	}

	taskText := buildTaskText(task)

	// Build resume summary
	resumeSummary := a.capability.Name
	if a.agentResume != nil {
		resumeSummary = a.agentResume.ToText()
	} else {
		// Fallback: use goals from Agentfile
		var goals []string
		for _, g := range a.wf.wf.Goals {
			goals = append(goals, fmt.Sprintf("- %s: %s", g.Name, g.Outcome))
		}
		if len(goals) > 0 {
			resumeSummary = fmt.Sprintf("%s\n\nGoals:\n%s", a.capability.Name, strings.Join(goals, "\n"))
		}
	}

	// Build swarm context for deliberation
	swarmCtx := ""
	if a.swarmContext != nil {
		swarmCtx = "\n\nSWARM STATE:\n" + a.swarmContext.FormatForLLM(task.TaskID)
	}

	prompt := fmt.Sprintf(`You are deciding whether to participate in a collaborative task.

AGENT PROFILE:
%s

TASK:
%s%s

Evaluate the task and the current swarm state. Decide your action:
- CLAIM — this task needs your capabilities and you are ready to take responsibility for a specific portion
- NEED_INFO — you have a question, insight, or need information before you can commit
- SKIP — the task doesn't need your skills

Reply format — first line is the decision, remaining lines are your response to the swarm:
CLAIM: <what specific portion of work you are taking responsibility for>
NEED_INFO: <your question, insight, or dependency that needs resolution>
SKIP

Examples:
CLAIM: I'll handle the REST API routes, request validation, and database queries.
NEED_INFO: What email service are we using? I need this before I can implement the verification flow.
SKIP`, resumeSummary, taskText, swarmCtx)

	llmAdapter := &smallLLMAdapter{provider: a.serviceRuntime.smallLLM}
	resp, err := llmAdapter.Complete(ctx, prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠️  Deliberation LLM failed: %v (defaulting to CLAIM)\n", err)
		return "CLAIM", ""
	}

	// Parse response — first line contains decision, rest is the message.
	// LLMs may wrap keywords in markdown (e.g., **CLAIM:**) — strip formatting.
	resp = strings.TrimSpace(resp)
	lines := strings.SplitN(resp, "\n", 2)
	firstLine := strings.TrimSpace(lines[0])
	firstLine = strings.ReplaceAll(firstLine, "*", "") // Strip markdown bold/italic
	firstLine = strings.TrimSpace(firstLine)
	responseText := ""
	if len(lines) > 1 {
		responseText = strings.TrimSpace(lines[1])
	}

	if strings.HasPrefix(strings.ToUpper(firstLine), "CLAIM") {
		msg := strings.TrimPrefix(firstLine, "CLAIM:")
		msg = strings.TrimPrefix(msg, "CLAIM")
		msg = strings.TrimSpace(msg)
		if responseText != "" {
			msg = msg + "\n" + responseText
		}
		return "CLAIM", msg
	}
	if strings.HasPrefix(strings.ToUpper(firstLine), "NEED_INFO") {
		msg := strings.TrimPrefix(firstLine, "NEED_INFO:")
		msg = strings.TrimPrefix(msg, "NEED_INFO")
		msg = strings.TrimSpace(msg)
		if responseText != "" {
			msg = msg + "\n" + responseText
		}
		return "NEED_INFO", msg
	}
	if strings.HasPrefix(strings.ToUpper(firstLine), "SKIP") {
		return "SKIP", ""
	}

	fmt.Fprintf(os.Stderr, "  ⚠️  Unclear deliberation response: %q (defaulting to SKIP)\n", firstLine)
	return "SKIP", ""
}

// publishDeliberationResponse publishes a CLAIM or NEED_INFO message to discuss.*.
func (a *serviceAgent) publishDeliberationResponse(ctx context.Context, task *tasks.TaskMessage, signal, message string) {
	result := &tasks.TaskResult{
		TaskID:      task.TaskID,
		AgentID:     a.agentID,
		Status:      tasks.ResultSuccess,
		Outputs:     message,
		CompletedAt: time.Now(),
		Metadata: map[string]string{
			"type":       "deliberation",
			"signal":     signal,
			"capability": a.capability.Name,
			"name":       a.displayName,
		},
	}

	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to marshal deliberation response: %v\n", err)
		return
	}

	subject := fmt.Sprintf("discuss.%s", task.TaskID)
	if err := a.bus.Publish(subject, resultData); err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to publish deliberation response: %v\n", err)
		return
	}

	// Update swarm context with our own response
	if a.swarmContext != nil {
		a.swarmContext.AddDiscussMessage(task.TaskID, executor.DiscussMessage{
			From:      a.agentID,
			Timestamp: time.Now(),
			Content:   message,
			Signal:    signal,
		})
	}

	fmt.Fprintf(os.Stderr, "  💬 Published %s: %s\n", signal, message)
}

// publishDiscussComment generates a lightweight comment (single LLM call, no tools)
// and publishes it back to the discuss topic.
func (a *serviceAgent) publishDiscussComment(ctx context.Context, task *tasks.TaskMessage) {
	start := time.Now()

	// COMMENT mode: read-only tools + memory, no writes
	taskText := buildTaskText(task)

	readOnlyNames := []string{
		"read", "glob", "grep", "ls", "head", "tail", "diff", "tree",
		"pwd", "hostname", "whoami", "sysinfo",
		"web_fetch", "web_search",
		"scratchpad_read", "scratchpad_write", "scratchpad_list", "scratchpad_search",
		"remember", "recall",
	}

	registry := a.serviceRuntime.exec.Registry()
	commentRegistry := registry.Subset(readOnlyNames)

	// Build tool definitions for LLM
	var toolDefs []llm.ToolDef
	for _, def := range commentRegistry.Definitions() {
		toolDefs = append(toolDefs, llm.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		})
	}

	commentLLM := a.serviceRuntime.smallLLM
	if commentLLM == nil {
		commentLLM = a.serviceRuntime.provider
	}

	prompt := fmt.Sprintf(`You are %s. A collaborative task is being discussed.

TASK: %s

You are in COMMENT mode — provide brief, actionable insights (2-5 sentences).
You may use tools to read files and gather context. Do NOT attempt the full work.`, a.capability.Name, taskText)

	messages := []llm.Message{{Role: "user", Content: prompt}}

	// Simple tool loop (max 5 rounds to prevent runaway)
	var finalContent string
	for i := 0; i < 5; i++ {
		resp, err := commentLLM.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ Comment LLM failed: %v\n", err)
			return
		}

		// If no tool calls, we have the final response
		if len(resp.ToolCalls) == 0 {
			finalContent = resp.Content
			break
		}

		// Execute tool calls against restricted registry
		messages = append(messages, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			tool := commentRegistry.Get(tc.Name)
			if tool == nil {
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("tool '%s' not available in comment mode", tc.Name),
					ToolCallID: tc.ID,
				})
				continue
			}
			result, toolErr := tool.Execute(ctx, tc.Args)
			content := fmt.Sprintf("%v", result)
			if toolErr != nil {
				content = fmt.Sprintf("error: %v", toolErr)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
		}
	}

	if finalContent == "" {
		return
	}

	result := &tasks.TaskResult{
		TaskID:      task.TaskID,
		AgentID:     a.agentID,
		Status:      tasks.ResultSuccess,
		Outputs:     finalContent,
		DurationMs:  time.Since(start).Milliseconds(),
		CompletedAt: time.Now(),
		Metadata:    map[string]string{"type": "comment", "capability": a.capability.Name, "name": a.displayName},
	}

	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ Failed to marshal comment: %v\n", err)
		return
	}

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

	// Track if this came from a discuss.* topic
	fromDiscuss := strings.HasPrefix(msg.Subject, "discuss.")

	fmt.Fprintf(os.Stderr, "  → Task received: %s\n", task.TaskID)

	// Update heartbeat status
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("executing")
		a.heartbeat.SetLoad(1.0)
		a.heartbeat.SetMetadata("executing_since", fmt.Sprintf("%d", time.Now().UnixMilli()))
		a.heartbeat.SetMetadata("current_task", task.TaskID)
	}

	// Execute task
	result := a.executeTask(ctx, task)

	// Update heartbeat status
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("monitoring")
		a.heartbeat.SetLoad(0.0)
		a.heartbeat.SetMetadata("executing_since", "")
		a.heartbeat.SetMetadata("current_task", "")
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

	// If task came from discuss.*, republish result to discuss channel
	// so other agents can pick up the output (e.g., tester sees coder's code)
	if fromDiscuss {
		followUp := tasks.TaskMessage{
			TaskID:      task.TaskID,
			Inputs:      task.Inputs,
			Attempt:     task.Attempt,
			SubmittedBy: a.agentID,
			Metadata: map[string]string{
				"agent_id":     a.agentID,
				"capability":   a.capability.Name,
				"prior_output": fmt.Sprintf("%v", result.Outputs),
			},
		}
		followUpData, err := followUp.Marshal()
		if err == nil {
			discussSubject := fmt.Sprintf("discuss.%s", task.TaskID)
			if pubErr := a.bus.Publish(discussSubject, followUpData); pubErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  Failed to republish to discuss: %v\n", pubErr)
			} else {
				fmt.Fprintf(os.Stderr, "  📢 Result shared on discuss.%s\n", task.TaskID)
			}
		}
	}

	// Track execution for revision context
	exec := a.executedTasks[task.TaskID]
	if exec == nil {
		exec = &taskExecution{}
		a.executedTasks[task.TaskID] = exec
	}
	exec.rounds++
	exec.output = fmt.Sprintf("%v", result.Outputs)

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
	return stripMarkdownFences(resp.Content), nil
}

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
