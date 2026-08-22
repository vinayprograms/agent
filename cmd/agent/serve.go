package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/swarm"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/agentkit/tools"
	"github.com/vinayprograms/swarmkit/messaging"
	"github.com/vinayprograms/swarmkit/registry"
)

// maxTaskBody caps an HTTP task submission (1 MiB): task inputs are text,
// and an unbounded body is a denial-of-service invitation.
const maxTaskBody = 1 << 20

// serviceAgent holds the state for a running service agent.
type serviceAgent struct {
	loaded *run.Loaded
	stderr io.Writer

	// Agent identity (uses session ID)
	agentID         string
	instanceID      string // <name>-<session-id> for swarm addressing
	displayName     string // swarm agent name (or Agentfile NAME if standalone)
	agentType       string // "worker" (default) or "manager"
	capabilitiesStr string // "cap1:n,cap2:n" for manager dispatch
	capability      capabilitySchema

	// Service-level session (shared across all tasks)
	serviceRuntime *run.Runtime
	// metrics forwards executor metrics to the heartbeat sender, which
	// only exists once the bus is up.
	metrics *deferredMetrics
	// interrupts is the buffer of the task currently executing (nil when
	// idle) — corrections that arrive between tasks are discarded.
	interrupts   atomic.Pointer[executor.InterruptBuffer]
	publishEvent *atomic.Pointer[session.Sink] // session event sink target; nil until the bus is up

	// Runtime state. mu guards status and currentTask, which the HTTP
	// handlers, the bus loop and the shutdown path all touch; exec
	// serialises task execution onto the single shared executor.
	mu           sync.Mutex
	status       string // "idle", "busy", "draining"
	currentTask  *swarm.TaskMessage
	exec         sync.Mutex
	taskDone     chan struct{}
	drainTimeout time.Duration

	// HTTP server (for local mode)
	httpServer *http.Server

	// Bus mode components. The bus (pub/sub, heartbeat) and the raw NATS
	// connection (JetStream) are separate dials: swarmkit's bus does not
	// expose its connection (A-S3).
	bus         messaging.Bus
	js          nats.JetStreamContext // JetStream context (nil if unavailable)
	heartbeat   *swarm.BusSender
	reg         registry.Registry
	taskSubs    []messaging.Subscription // work.<cap>.* subscriptions (fallback)
	workPullSub *nats.Subscription       // JetStream pull consumer for work (preferred)
	instanceSub messaging.Subscription   // work.<instance-id>.* for corrections
	discussSub  messaging.Subscription   // discuss.* subscription (manager only — read)
	controlSub  messaging.Subscription   // control.<id>.shutdown subscription
	queueGroup  string
}

// capabilitySchema is the document served on GET /capability. It keeps the
// pre-migration JSON shape (the swarmkit registry.Skill has a different one).
type capabilitySchema struct {
	Name        string        `json:"name"`
	Version     string        `json:"version,omitempty"`
	Description string        `json:"description,omitempty"`
	Inputs      []fieldSchema `json:"inputs,omitempty"`
	Outputs     []fieldSchema `json:"outputs,omitempty"`
}

// fieldSchema describes one input or output of a capability.
type fieldSchema struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// skill converts the capability into the swarmkit registry's skill shape.
func (c capabilitySchema) skill() registry.Skill {
	s := registry.Skill{ID: c.Name, Name: c.Name, Description: c.Description}
	for _, f := range c.Inputs {
		s.Inputs = append(s.Inputs, registry.Param(f))
	}
	for _, f := range c.Outputs {
		s.Outputs = append(s.Outputs, registry.Param(f))
	}
	return s
}

// serveOptions are the serve command's flags, plus the positional
// Agentfile and the streams the command runs on.
type serveOptions struct {
	file        string
	stdout      io.Writer
	stderr      io.Writer
	config      string
	policy      string
	credentials string
	workspace   string
	state       string

	// Transport
	http string
	bus  string

	// Service
	queueGroup   string
	capability   string
	sessionLabel string

	// Swarm integration
	agentType    string
	capabilities string
}

// newServeCmd runs the agent as a long-running service.
func newServeCmd(d deps) *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve [file]",
		Short: "Run as a service agent (long-running)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.file = argOr(args, "Agentfile")
			opts.stdout, opts.stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			return runServe(cmd.Context(), d, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.config, "config", "", "Config file path")
	f.StringVar(&opts.policy, "policy", "", "Policy file path")
	f.StringVar(&opts.credentials, "credentials", "", "Credentials file path")
	f.StringVar(&opts.workspace, "workspace", "", "Workspace directory")
	f.StringVar(&opts.state, "state", "", "Override state location (isolate per-agent when needed)")
	f.StringVar(&opts.http, "http", "", "Run HTTP server on this address (e.g., :8080)")
	f.StringVar(&opts.bus, "bus", "", "Message bus URL (e.g., nats://localhost:4222)")
	f.StringVar(&opts.queueGroup, "queue-group", "", "Queue group name for load balancing")
	f.StringVar(&opts.capability, "capability", "", "Capability name (default: Agentfile NAME)")
	f.StringVar(&opts.sessionLabel, "session-label", "", "Label for session directory (default: Agentfile NAME)")
	f.StringVar(&opts.agentType, "type", "", "Agent type: worker or manager (default: worker)")
	f.StringVar(&opts.capabilities, "capabilities", "", "Worker capabilities for manager dispatch (format: cap1:n,cap2:n)")
	return cmd
}

// runServe loads the workflow, builds the service agent and serves on the
// configured transport until ctx is cancelled.
func runServe(ctx context.Context, d deps, opts serveOptions) error {
	loaded, err := run.Load(run.LoadOptions{
		AgentfilePath: opts.file,
		ConfigPath:    opts.config,
		PolicyPath:    opts.policy,
		Workspace:     opts.workspace,
		Inputs:        map[string]string{}, // set per task
		SessionLabel:  opts.sessionLabel,
		Home:          d.home,
		Stderr:        opts.stderr,
	})
	if err != nil {
		return err
	}

	// Apply command-line overrides for service config
	svc := &loaded.Config.Service
	if opts.http != "" {
		svc.HTTPAddr = opts.http
	}
	if opts.bus != "" {
		svc.BusURL = opts.bus
	}
	if opts.queueGroup != "" {
		svc.QueueGroup = opts.queueGroup
	}
	if opts.capability != "" {
		svc.Capability = opts.capability
	}
	if opts.state != "" {
		loaded.Config.State.Location = opts.state
	}

	// Determine capability name
	capabilityName := svc.Capability
	if capabilityName == "" {
		capabilityName = loaded.Workflow.Name
	}
	capability := extractCapabilitySchema(loaded.Workflow, capabilityName)

	// Parse drain timeout
	drainTimeout := 30 * time.Second
	if svc.DrainTimeout != "" {
		if dur, err := time.ParseDuration(svc.DrainTimeout); err == nil {
			drainTimeout = dur
		}
	}

	creds, err := d.credentials(opts.credentials)
	if err != nil {
		return err
	}

	// Create service-level runtime (one session for entire service lifetime).
	// Session events stream to NATS once the bus is up (see publishEvent
	// below); until then the sink drops them.
	publishEvent := new(atomic.Pointer[session.Sink])
	metrics := &deferredMetrics{}
	serviceRt, err := run.New(ctx, loaded, run.Deps{
		Creds:   creds,
		Stdout:  opts.stdout,
		Stderr:  opts.stderr,
		Version: version,
		Sink: func(evt session.Event) {
			if p := publishEvent.Load(); p != nil {
				(*p)(evt)
			}
		},
		Metrics:     metrics,
		KeepSession: true, // session persists across tasks
	})
	if err != nil {
		return fmt.Errorf("setting up service runtime: %w", err)
	}
	defer serviceRt.Close()

	sessID := serviceRt.Session().ID

	// Agent ID uses session ID (or config if specified)
	agentID := loaded.Config.Agent.ID
	if agentID == "" {
		agentID = fmt.Sprintf("%s-%s", capabilityName, sessID)
	}

	// Display name: swarm agent name if available, otherwise Agentfile NAME
	displayName := loaded.Workflow.Name
	if opts.sessionLabel != "" {
		displayName = opts.sessionLabel
	}

	// Agent type and capabilities: CLI flag first, env var as fallback.
	agentType := opts.agentType
	if agentType == "" {
		agentType = d.getenv("AGENT_TYPE")
	}
	if agentType == "" {
		agentType = "worker"
	}
	capabilitiesStr := opts.capabilities
	if capabilitiesStr == "" {
		capabilitiesStr = d.getenv("SWARM_CAPABILITIES")
	}

	agent := &serviceAgent{
		loaded:          loaded,
		stderr:          opts.stderr,
		agentID:         agentID,
		instanceID:      fmt.Sprintf("%s-%s", displayName, sessID),
		displayName:     displayName,
		agentType:       agentType,
		capabilitiesStr: capabilitiesStr,
		capability:      capability,
		serviceRuntime:  serviceRt,
		metrics:         metrics,
		publishEvent:    publishEvent,
		status:          "idle",
		taskDone:        make(chan struct{}, 1),
		drainTimeout:    drainTimeout,
	}

	switch {
	case svc.BusURL != "":
		return agent.runBusMode(ctx)
	case svc.HTTPAddr != "":
		return agent.runHTTPMode(ctx)
	default:
		return fmt.Errorf("no transport configured: specify --http or --bus, or set [service].http_addr or [service].bus_url in config")
	}
}

// handler builds the service agent's HTTP mux.
func (a *serviceAgent) handler() http.Handler {
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":     a.state(),
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
		if a.state() == "draining" {
			http.Error(w, "agent is draining, not accepting tasks", http.StatusServiceUnavailable)
			return
		}

		// Parse task message
		var task swarm.TaskMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTaskBody)).Decode(&task); err != nil {
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
		if result.Status == swarm.ResultFailed {
			w.WriteHeader(http.StatusInternalServerError)
		}
		json.NewEncoder(w).Encode(result)
	})
	return mux
}

// runHTTPMode runs the agent as an HTTP server until ctx is cancelled.
func (a *serviceAgent) runHTTPMode(ctx context.Context) error {
	a.httpServer = &http.Server{
		Addr:              a.loaded.Config.Service.HTTPAddr,
		Handler:           a.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout on purpose: it caps the whole handler, and
		// POST /task runs a workflow whose duration is the workload's,
		// not the transport's. The read-side timeouts and the body cap
		// are what close the slow-loris door.
	}

	fmt.Fprintf(a.stderr, "Service agent: %s (ID: %s, capability: %s)\n", a.loaded.Workflow.Name, a.agentID, a.capability.Name)
	fmt.Fprintf(a.stderr, "HTTP server listening on %s\n", a.loaded.Config.Service.HTTPAddr)
	fmt.Fprintf(a.stderr, "Endpoints:\n")
	fmt.Fprintf(a.stderr, "  GET  /health     - Health check\n")
	fmt.Fprintf(a.stderr, "  GET  /capability - Capability schema\n")
	fmt.Fprintf(a.stderr, "  POST /task       - Submit task\n")

	serverErr := make(chan error, 1)
	go func() { serverErr <- a.httpServer.ListenAndServe() }()

	select {
	case err := <-serverErr:
		if err != http.ErrServerClosed {
			return fmt.Errorf("HTTP server error: %w", err)
		}
	case <-ctx.Done():
		fmt.Fprintf(a.stderr, "\nReceived shutdown signal, draining...\n")
		a.initiateShutdown(context.WithoutCancel(ctx))
		<-serverErr
	}
	return nil
}

// runBusMode runs the agent connected to a message bus (swarm mode).
func (a *serviceAgent) runBusMode(ctx context.Context) error {
	// Connect to NATS
	cfg := messaging.NATSDefaults()
	cfg.URL = a.loaded.Config.Service.BusURL
	cfg.Name = fmt.Sprintf("agent-%s", a.agentID)
	natsBus, err := messaging.NATS(cfg)
	if err != nil {
		return fmt.Errorf("connecting to bus: %w", err)
	}
	a.bus = natsBus
	defer natsBus.Close()

	// Manager-only: register dispatch tool so the orchestrator can assign tasks to workers
	if a.agentType == "manager" {
		caps := parseSwarmCapabilities(a.capabilitiesStr)
		dispatchTool := swarm.NewDispatchTool(natsBus, a.displayName, caps)
		if err := a.serviceRuntime.Registry().Register(tools.New(dispatchTool)); err != nil {
			return fmt.Errorf("registering dispatch tool: %w", err)
		}
		enableTool(a.serviceRuntime.Policy(), dispatchTool.Name())
		fmt.Fprintf(a.stderr, "✓ Dispatch tool registered (manager-only)\n")
		for _, c := range caps {
			fmt.Fprintf(a.stderr, "  capability: %s (%d workers)\n", c.Name, c.Replicas)
		}
	}

	// Register with NATS KV registry (for discovery)
	if err := a.registerWithRegistry(cfg); err != nil {
		fmt.Fprintf(a.stderr, "⚠️  Registry registration failed: %v (continuing without registry)\n", err)
	} else {
		defer a.reg.Close()
	}

	// Parse heartbeat interval
	heartbeatInterval := 5 * time.Second
	if a.loaded.Config.Service.HeartbeatInterval != "" {
		if d, err := time.ParseDuration(a.loaded.Config.Service.HeartbeatInterval); err == nil {
			heartbeatInterval = d
		}
	}

	// Start heartbeat sender
	hbSender, err := swarm.NewBusSender(swarm.SenderConfig{
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
	hbSender.SetMetadata("session_id", a.serviceRuntime.Session().ID)
	hbSender.SetMetadata("capability", a.capability.Name)
	hbSender.SetMetadata("type", a.agentType)
	hbSender.SetMetadata("version", version)

	// Wire metrics collector for dashboard reporting
	a.metrics.set(swarm.NewMetricsCollector(hbSender))

	// Wire event publisher — streams structured session events to NATS
	// for the swarm UI's real-time event log.
	evtSubject := fmt.Sprintf("events.%s", a.displayName)
	publish := session.Sink(func(evt session.Event) {
		data, err := json.Marshal(evt)
		if err != nil {
			return
		}
		natsBus.Publish(evtSubject, data)
	})
	a.publishEvent.Store(&publish)
	defer a.publishEvent.Store(nil)

	if err := hbSender.Start(ctx); err != nil {
		return fmt.Errorf("starting heartbeat: %w", err)
	}
	fmt.Fprintf(a.stderr, "📡 Heartbeat started on subject: heartbeat.%s\n", a.agentID)
	defer hbSender.Stop()

	// Ensure JetStream stream exists for durable messaging. JetStream needs
	// a raw connection, which the bus does not expose: dial a second one.
	var js nats.JetStreamContext
	nc, jsErr := nats.Connect(cfg.URL, nats.Name(cfg.Name+"-js"))
	if jsErr == nil {
		defer nc.Close()
		js, jsErr = swarm.EnsureStream(nc)
	}
	if jsErr != nil {
		fmt.Fprintf(a.stderr, "⚠️  JetStream unavailable: %v (falling back to queue groups)\n", jsErr)
	}
	a.js = js

	// Determine queue group (used as fallback if JetStream unavailable)
	a.queueGroup = a.loaded.Config.Service.QueueGroup
	if a.queueGroup == "" {
		a.queueGroup = a.capability.Name + "-workers"
	}

	// Subscribe to work.<capability>.* for task assignment.
	// Prefer JetStream pull consumer (ack-based, guaranteed single delivery)
	// with fallback to NATS queue groups (push-based, best-effort distribution).
	capName := a.getCapabilities()[0]
	if js != nil {
		pullSub, err := swarm.EnsureWorkConsumer(js, capName)
		if err != nil {
			fmt.Fprintf(a.stderr, "⚠️  JetStream pull consumer failed: %v (falling back to queue groups)\n", err)
		} else {
			a.workPullSub = pullSub
			fmt.Fprintf(a.stderr, "✓ JetStream pull consumer: work.%s.* (ack-based delivery)\n", capName)
		}
	}
	if a.workPullSub == nil {
		// Fallback: NATS queue groups (push-based)
		workSubject := fmt.Sprintf("work.%s.*", capName)
		workSub, err := natsBus.Join(a.queueGroup).Subscribe(workSubject)
		if err != nil {
			return fmt.Errorf("subscribing to %s: %w", workSubject, err)
		}
		a.taskSubs = append(a.taskSubs, workSub)
		fmt.Fprintf(a.stderr, "⚠️  Using queue group fallback: work.%s.* (push-based)\n", capName)
	}
	defer func() {
		if a.workPullSub != nil {
			a.workPullSub.Unsubscribe()
		}
		for _, sub := range a.taskSubs {
			sub.Unsubscribe()
		}
	}()

	// Subscribe to work.<instance-id>.* (corrective guidance → interrupt buffer)
	instanceSubject := fmt.Sprintf("work.%s.*", a.instanceID)
	instanceSub, err := natsBus.Subscribe(instanceSubject)
	if err != nil {
		fmt.Fprintf(a.stderr, "⚠️  Failed to subscribe to %s: %v\n", instanceSubject, err)
	} else {
		a.instanceSub = instanceSub
		defer instanceSub.Unsubscribe()
	}

	// Manager-specific: subscribe to discuss.* for monitoring worker progress
	if a.agentType == "manager" {
		discussSub, err := natsBus.Subscribe("discuss.*")
		if err != nil {
			fmt.Fprintf(a.stderr, "⚠️  Failed to subscribe to discuss.*: %v\n", err)
		} else {
			a.discussSub = discussSub
			defer discussSub.Unsubscribe()
		}
	}

	// Subscribe to control.<agentID>.shutdown for remote shutdown via `swarm down`
	controlSubject := fmt.Sprintf("control.%s.shutdown", a.agentID)
	controlSub, err := natsBus.Subscribe(controlSubject)
	if err != nil {
		fmt.Fprintf(a.stderr, "⚠️  Failed to subscribe to %s: %v\n", controlSubject, err)
	} else {
		a.controlSub = controlSub
		defer controlSub.Unsubscribe()
	}

	fmt.Fprintf(a.stderr, "Service agent: %s (ID: %s, instance: %s, type: %s, capability: %s)\n",
		a.loaded.Workflow.Name, a.agentID, a.instanceID, a.agentType, a.capability.Name)
	fmt.Fprintf(a.stderr, "Connected to bus: %s\n", a.loaded.Config.Service.BusURL)
	if a.workPullSub != nil {
		fmt.Fprintf(a.stderr, "Listening on: work.%s.* (JetStream pull consumer)\n", capName)
	} else {
		fmt.Fprintf(a.stderr, "Listening on: work.%s.* (queue: %s)\n", capName, a.queueGroup)
	}
	fmt.Fprintf(a.stderr, "Listening on: work.%s.* (corrections)\n", a.instanceID)
	if a.agentType == "manager" {
		fmt.Fprintf(a.stderr, "Listening on: discuss.* (manager — monitoring workers)\n")
	}
	fmt.Fprintf(a.stderr, "Heartbeat interval: %s\n", heartbeatInterval)

	// Main loop
	a.runMainLoop(ctx)
	return nil
}

func (a *serviceAgent) runMainLoop(ctx context.Context) {
	// ctx carries the shutdown signal; taskCtx carries task execution and
	// outlives it, so a signal drains the task in flight instead of
	// aborting it. It is cancelled on the way out, once the drain is done.
	taskCtx, cancelTasks := taskContext(ctx)
	defer cancelTasks()

	// Work channel — fed by either JetStream pull or queue group push
	workCh := make(chan *messaging.Message, 16)

	if a.workPullSub != nil {
		// JetStream pull consumer: fetch one task at a time, ack after processing
		go a.pullWorkLoop(ctx, workCh)
	} else {
		// Fallback: push-based queue group subscriptions
		for _, sub := range a.taskSubs {
			go func(s messaging.Subscription) {
				for msg := range s.Messages() {
					workCh <- msg
				}
			}(sub)
		}
	}

	// Instance channel (corrections → interrupt buffer during execution)
	var instanceCh <-chan *messaging.Message
	if a.instanceSub != nil {
		instanceCh = a.instanceSub.Messages()
	}

	// Discuss channel (manager only — monitoring worker progress)
	var discussCh <-chan *messaging.Message
	if a.discussSub != nil {
		discussCh = a.discussSub.Messages()
	}

	// Control channel (remote shutdown via `swarm down`)
	var controlCh <-chan *messaging.Message
	if a.controlSub != nil {
		controlCh = a.controlSub.Messages()
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(a.stderr, "\nReceived shutdown signal, draining...\n")
			a.initiateBusShutdown()
			return

		case _, ok := <-controlCh:
			if !ok {
				controlCh = nil
				continue
			}
			fmt.Fprintf(a.stderr, "\nReceived remote shutdown signal, draining...\n")
			a.initiateBusShutdown()
			return

		case msg, ok := <-workCh:
			if !ok {
				return
			}
			a.handleBusTask(taskCtx, msg)

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
func (a *serviceAgent) handleInstanceMessage(msg *messaging.Message) {
	buf := a.interrupts.Load()
	if buf == nil {
		// Not currently executing: there is no run to correct, so record
		// the message in the session log rather than dropping it.
		fmt.Fprintf(a.stderr, "  ⚠️  Correction received while idle (discarded): %s\n", string(msg.Data))
		a.serviceRuntime.Session().AddEvent(session.Event{
			Type:    session.EventWarning,
			Content: "correction received while idle (no task in flight): " + string(msg.Data),
		})
		return
	}

	// Parse the message — could be a TaskMessage or raw text
	var content string
	var from string

	task, err := swarm.UnmarshalTaskMessage(msg.Data)
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
	fmt.Fprintf(a.stderr, "  📨 Correction received from %s → interrupt buffer\n", from)
}

// handleManagerDiscussMessage processes worker updates on discuss.* (manager only).
// For now, this logs the update. Future: feed into manager's reasoning context.
func (a *serviceAgent) handleManagerDiscussMessage(ctx context.Context, msg *messaging.Message) {
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
		var result swarm.TaskResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			return
		}
		fmt.Fprintf(a.stderr, "  📢 [%s] %s: %v\n", result.AgentID, result.TaskID, result.Outputs)
		return
	}

	if update.InstanceID != "" {
		fmt.Fprintf(a.stderr, "  📢 [%s] %s/%s: %s\n",
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

// parseSwarmCapabilities parses the SWARM_CAPABILITIES env var (format: "develop:3,test:1").
func parseSwarmCapabilities(env string) []swarm.WorkerCapability {
	if env == "" {
		return nil
	}
	var caps []swarm.WorkerCapability
	for _, entry := range strings.Split(env, ",") {
		parts := strings.SplitN(entry, ":", 2)
		name := parts[0]
		replicas := 1
		if len(parts) == 2 {
			n, err := strconv.Atoi(parts[1])
			if err != nil || n < 1 {
				n = 1
			}
			replicas = n
		}
		caps = append(caps, swarm.WorkerCapability{Name: name, Replicas: replicas})
	}
	return caps
}

// truncateStr truncates a string to max length with ellipsis.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// registerWithRegistry registers the agent with the NATS KV registry. The
// registry dials its own connection from cfg. Entries persist until
// Deregister (no TTL, so no touch/re-register loop — A-S4).
func (a *serviceAgent) registerWithRegistry(cfg messaging.NATSConfig) error {
	reg, err := registry.New(registry.Config{NATS: cfg})
	if err != nil {
		return fmt.Errorf("creating registry: %w", err)
	}

	if err := reg.Register(a.registryEntry()); err != nil {
		reg.Close()
		return fmt.Errorf("registering agent: %w", err)
	}
	a.reg = reg

	fmt.Fprintf(a.stderr, "📝 Registered with NATS KV registry\n")
	return nil
}

// registryEntry builds the agent's registry record: identity, the single
// announced capability as a skill, and the metadata the swarm UI reads.
func (a *serviceAgent) registryEntry() registry.Agent {
	capName := a.getCapabilities()[0]
	skill := a.capability.skill()
	skill.ID, skill.Name = capName, capName
	return registry.Agent{
		ID:      a.agentID,
		Name:    a.capability.Name,
		Version: version,
		Skills:  []registry.Skill{skill},
		Metadata: map[string]string{
			"version":     version,
			"instance_id": a.instanceID,
			"type":        a.agentType,
		},
	}
}

// enableTool lists a tool in the policy so the executor advertises and runs
// it. Used for tools the serve mode registers itself (dispatch).
func enableTool(pol *policy.Policy, name string) {
	if pol == nil {
		return
	}
	if pol.Tools == nil {
		pol.Tools = make(map[string]*policy.ToolPolicy)
	}
	if _, ok := pol.Tools[name]; !ok {
		pol.Tools[name] = &policy.ToolPolicy{}
	}
}

// getCapabilities returns the single announced capability for NATS subjects and registry.
// Priority: --capability CLI > capability: in config > Agentfile NAME.
func (a *serviceAgent) getCapabilities() []string {
	if a.capability.Name != "" {
		return []string{a.capability.Name}
	}
	return []string{a.loaded.Workflow.Name}
}

// buildTaskText extracts a readable text representation from a task message.
func buildTaskText(task *swarm.TaskMessage) string {
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

// pullWorkLoop fetches tasks from the JetStream pull consumer one at a time.
// Each message is acked only after the worker finishes processing it,
// guaranteeing exactly-once delivery across the worker pool.
func (a *serviceAgent) pullWorkLoop(ctx context.Context, workCh chan<- *messaging.Message) {
	consecutiveErrors := 0
	for {
		// Fetch one message at a time (blocks until available or timeout)
		msgs, err := a.workPullSub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			if err == nats.ErrTimeout {
				// No messages available — normal idle state
				consecutiveErrors = 0
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if err == nats.ErrSubscriptionClosed || ctx.Err() != nil {
				return
			}
			// Check for invalid subscription (connection was reset)
			if !a.workPullSub.IsValid() || err.Error() == "nats: invalid subscription" {
				return
			}
			consecutiveErrors++
			if consecutiveErrors <= 3 {
				fmt.Fprintf(a.stderr, "  ⚠️  JetStream fetch error: %v\n", err)
			} else if consecutiveErrors == 4 {
				fmt.Fprintf(a.stderr, "  ⚠️  JetStream fetch errors suppressed (repeating)\n")
			}
			// Backoff on repeated errors to avoid tight spin loop
			select {
			case <-time.After(time.Duration(consecutiveErrors) * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		consecutiveErrors = 0

		for _, natsMsg := range msgs {
			// Convert to messaging.Message for handleBusTask compatibility
			busMsg := &messaging.Message{
				Subject: natsMsg.Subject,
				Data:    natsMsg.Data,
			}

			// Discard any completion left over from a task this loop did
			// not dispatch, so the signal we wait for below is this
			// task's own.
			select {
			case <-a.taskDone:
			default:
			}

			// Send to work channel (blocks until main loop picks it up)
			select {
			case workCh <- busMsg:
			case <-ctx.Done():
				natsMsg.Nak()
				return
			}

			// Wait for task processing to complete before acking. A
			// shutdown signal drains the task rather than abandoning it;
			// only an expired drain leaves it for another worker.
			if !a.awaitTaskDone(ctx) {
				natsMsg.Nak()
				return
			}

			// Ack the message — NATS won't redeliver to any worker
			if err := natsMsg.Ack(); err != nil {
				fmt.Fprintf(a.stderr, "  ⚠️  JetStream ack error: %v\n", err)
			}
		}
	}
}

// handleBusTask processes a task received from the bus.
func (a *serviceAgent) handleBusTask(ctx context.Context, msg *messaging.Message) {
	// Parse task message
	task, err := swarm.UnmarshalTaskMessage(msg.Data)
	if err != nil {
		fmt.Fprintf(a.stderr, "  ✗ Invalid task message: %v\n", err)
		return
	}

	fmt.Fprintf(a.stderr, "  → Task received: %s\n", task.TaskID)

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
		a.heartbeat.SetStatus("idle")
		a.heartbeat.SetLoad(0.0)
		a.heartbeat.SetMetadata("executing_since", "")
		a.heartbeat.SetMetadata("current_task", "")
	}

	// Publish result to done.<capability>.<task_id>
	resultData, err := result.Marshal()
	if err != nil {
		fmt.Fprintf(a.stderr, "  ✗ Failed to marshal result: %v\n", err)
		return
	}

	resultSubject := task.ReplyTo
	if resultSubject == "" {
		resultSubject = fmt.Sprintf("done.%s.%s", a.capability.Name, task.TaskID)
	}

	if err := a.bus.Publish(resultSubject, resultData); err != nil {
		fmt.Fprintf(a.stderr, "  ✗ Failed to publish result: %v\n", err)
		return
	}

	// Publish final completion update to discuss.*
	finalContent := fmt.Sprintf("%v", result.Outputs)
	if result.Status == swarm.ResultFailed {
		finalContent = fmt.Sprintf("FAILED: %s", result.Error)
	}
	a.publishToDiscuss(task.TaskID, "complete", finalContent)

	statusIcon := "✓"
	if result.Status == swarm.ResultFailed {
		statusIcon = "✗"
	}
	fmt.Fprintf(a.stderr, "  %s Task complete: %s (%s, %dms)\n",
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
		fmt.Fprintf(a.stderr, "  ⚠️  Failed to publish to discuss: %v\n", err)
	}
}

// initiateBusShutdown handles graceful shutdown in bus mode: deregister,
// stop taking work, then drain. It takes no context — every step here is
// either a non-cancellable teardown call or bounded by the drain timeout.
func (a *serviceAgent) initiateBusShutdown() {
	inFlight := a.busy()
	a.setStatus("draining")

	// Update heartbeat to draining
	if a.heartbeat != nil {
		a.heartbeat.SetStatus("draining")
	}

	// Deregister from NATS KV registry
	if a.reg != nil {
		if err := a.reg.Deregister(a.agentID); err != nil {
			fmt.Fprintf(a.stderr, "⚠️  Registry deregister failed: %v\n", err)
		} else {
			fmt.Fprintf(a.stderr, "📝 Deregistered from registry\n")
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
	if inFlight {
		fmt.Fprintf(a.stderr, "Waiting for current task to complete (timeout: %s)...\n", a.drainTimeout)
		if a.awaitIdle(a.drainTimeout) {
			fmt.Fprintf(a.stderr, "Task completed, shutting down.\n")
		} else {
			fmt.Fprintf(a.stderr, "Drain timeout reached, forcing shutdown.\n")
		}
	}

	// Heartbeat and bus will be closed by deferred calls in runBusMode
}

// awaitIdle blocks until no task is executing, or until timeout expires.
// It waits on the execution lock itself rather than on a completion
// signal: a signal outlives the task that sent it, a held lock cannot.
func (a *serviceAgent) awaitIdle(timeout time.Duration) bool {
	idle := make(chan struct{})
	go func() {
		a.exec.Lock()
		a.exec.Unlock()
		close(idle)
	}()
	select {
	case <-idle:
		return true
	case <-time.After(timeout):
		return false
	}
}

// awaitTaskDone waits for the task the caller dispatched to finish, and
// reports whether it did. A shutdown signal does not abandon the task:
// execution is detached from it (see taskContext), so the task drains and
// its result is still published — only the drain deadline gives up, and
// then the caller must Nak so another worker retries.
func (a *serviceAgent) awaitTaskDone(ctx context.Context) bool {
	select {
	case <-a.taskDone:
		return true
	case <-ctx.Done():
	}
	select {
	case <-a.taskDone:
		return true
	case <-time.After(a.drainTimeout):
		return false
	}
}

// deferredMetrics forwards executor metrics to a collector that is wired
// after the executor exists: the heartbeat sender only appears once the
// bus is up. The zero value drops every metric.
type deferredMetrics struct {
	target atomic.Pointer[executor.MetricsCollector]
}

func (d *deferredMetrics) set(mc executor.MetricsCollector) { d.target.Store(&mc) }

func (d *deferredMetrics) collector() executor.MetricsCollector {
	if p := d.target.Load(); p != nil {
		return *p
	}
	return nil
}

func (d *deferredMetrics) RecordLLMCall(in, out, cacheCreation, cacheRead int, latencyMs int64) {
	if c := d.collector(); c != nil {
		c.RecordLLMCall(in, out, cacheCreation, cacheRead, latencyMs)
	}
}

func (d *deferredMetrics) RecordSupervision(approved bool) {
	if c := d.collector(); c != nil {
		c.RecordSupervision(approved)
	}
}

func (d *deferredMetrics) SetSubagents(count int) {
	if c := d.collector(); c != nil {
		c.SetSubagents(count)
	}
}

// taskContext derives the context tasks execute on. It is detached from
// the shutdown signal — SIGINT starts a drain, it does not abort the task
// in flight — and is cancelled by its own cancel func once the drain is
// over.
func taskContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithoutCancel(ctx))
}

// state reports the agent's lifecycle state: "idle", "busy" or "draining".
func (a *serviceAgent) state() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// setState records the lifecycle state and the task in flight (nil when
// there is none).
func (a *serviceAgent) setState(status string, task *swarm.TaskMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
	a.currentTask = task
}

// setStatus records the lifecycle state, leaving the task in flight alone.
func (a *serviceAgent) setStatus(status string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = status
}

// busy reports whether a task is in flight.
func (a *serviceAgent) busy() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentTask != nil
}

// executeTask runs a single task through the workflow. The executor and
// its session are shared, so tasks run one at a time.
func (a *serviceAgent) executeTask(ctx context.Context, task *swarm.TaskMessage) *swarm.TaskResult {
	a.exec.Lock()
	defer a.exec.Unlock()

	start := time.Now()
	a.setState("busy", task)
	defer func() {
		a.setState("idle", nil)
		select {
		case a.taskDone <- struct{}{}:
		default:
		}
	}()

	result := swarm.NewTaskResult(task.TaskID, a.agentID, swarm.ResultSuccess)
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
	// Corrective guidance from work.<instance-id>.* lands in this buffer for
	// the duration of the task; handleInstanceMessage reads it.
	interrupts := executor.NewInterruptBuffer()
	a.interrupts.Store(interrupts)
	defer a.interrupts.Store(nil)

	taskID := task.TaskID
	execResult, err := a.serviceRuntime.Executor().Run(ctx, executor.RunOptions{
		Inputs:     inputs,
		Interrupts: interrupts,
		Discuss:    func(goalName, content string) { a.publishToDiscuss(taskID, goalName, content) },
	})
	if err != nil {
		result.Status = swarm.ResultFailed
		result.Error = err.Error()
		fmt.Fprintf(a.stderr, "  ✗ Execution error: %v\n", err)
	} else if execResult.Status != "complete" {
		result.Status = swarm.ResultFailed
		result.Error = fmt.Sprintf("workflow status: %s", execResult.Status)
		result.Outputs = execResult.Outputs
		fmt.Fprintf(a.stderr, "  ✗ Workflow failed with status: %s\n", execResult.Status)
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
	inFlight := a.busy()
	a.setStatus("draining")

	// Wait for current task to complete (with timeout)
	if inFlight {
		fmt.Fprintf(a.stderr, "Waiting for current task to complete (timeout: %s)...\n", a.drainTimeout)
		if a.awaitIdle(a.drainTimeout) {
			fmt.Fprintf(a.stderr, "Task completed, shutting down.\n")
		} else {
			fmt.Fprintf(a.stderr, "Drain timeout reached, forcing shutdown.\n")
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
func extractCapabilitySchema(wf *agentfile.Workflow, name string) capabilitySchema {
	schema := capabilitySchema{
		Name:        name,
		Description: fmt.Sprintf("Workflow: %s", wf.Name),
	}

	// Extract inputs from Agentfile
	for _, input := range wf.Inputs {
		field := fieldSchema{
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
			field := fieldSchema{
				Name: output,
				Type: "string",
			}
			schema.Outputs = append(schema.Outputs, field)
		}
	}

	return schema
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
