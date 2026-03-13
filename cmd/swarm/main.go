package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agent/internal/swarm"
	"github.com/vinayprograms/agentkit/tasks"
)

type CLI struct {
	NATSURL string `name:"nats" help:"NATS server URL" default:"nats://localhost:4222"`

	Status       StatusCmd       `cmd:"" help:"Show swarm status"`
	Agents       AgentsCmd       `cmd:"" help:"List registered agents"`
	Capabilities CapabilitiesCmd `cmd:"" help:"List capabilities in swarm"`
	Submit       SubmitCmd       `cmd:"" help:"Submit a task to an agent"`
	Discuss      DiscussCmd      `cmd:"" help:"Submit a task for collaborative discussion"`
	Result       ResultCmd       `cmd:"" help:"Get result for a task"`
	History      HistoryCmd      `cmd:"" help:"Show recent tasks"`
	Up           UpCmd           `cmd:"" help:"Start swarm from swarm.yaml"`
	Down         DownCmd         `cmd:"" help:"Stop swarm agents"`
	Restart      RestartCmd      `cmd:"" help:"Restart swarm agents"`
	UI           UICmd           `cmd:"" help:"Interactive TUI dashboard"`
	Replay       ReplayCmd       `cmd:"" help:"Replay task execution"`
	Chain        ChainCmd        `cmd:"" help:"Chain tasks through multiple agents"`
	Purge        PurgeCmd        `cmd:"" help:"Purge NATS stream (clear all replay history)"`
}

type StatusCmd struct{}
type AgentsCmd struct{}
type CapabilitiesCmd struct{}
type SubmitCmd struct {
	Capability string   `arg:"" help:"Capability to route task to"`
	Inputs     []string `name:"input" short:"i" sep:"none" help:"Input as name=value (can repeat)"`
	File       string   `name:"file" short:"f" help:"Load inputs from JSON file" type:"existingfile"`
	Task       string   `arg:"" optional:"" help:"Task description (used as 'task' input if no --input specified)"`
	NoWait     bool     `name:"nowait" help:"Don't wait for result (fire-and-forget)"`
}
type DiscussCmd struct {
	Inputs []string `name:"input" short:"i" sep:"none" help:"Input as name=value (can repeat)"`
	File   string   `name:"file" short:"f" help:"Load inputs from JSON file" type:"existingfile"`
	Task   string   `arg:"" optional:"" help:"Task description"`
}
type ResultCmd struct {
	TaskID string `arg:"" help:"Task ID to fetch result for"`
	Wait   bool   `name:"wait" short:"w" help:"Wait for result if not ready"`
}
type HistoryCmd struct {
	Capability string `name:"capability" short:"c" help:"Filter by capability"`
	Status     string `name:"status" short:"s" help:"Filter by status (pending, running, success, failed)"`
	Limit      int    `name:"limit" short:"l" help:"Max results" default:"20"`
}
type UpCmd struct {
	Agents []string `name:"agent" short:"a" optional:"" help:"Specific agents to start (default: all)"`
	File   string   `arg:"" optional:"" default:"swarm.yaml" help:"Manifest file"`
}
type DownCmd struct {
	Agents []string `arg:"" optional:"" help:"Specific agents to stop (default: all)"`
}
type RestartCmd struct {
	Agents []string `arg:"" optional:"" help:"Specific agents to restart (default: all)"`
}
type UICmd struct {
	Port    int    `name:"port" short:"p" default:"9090" help:"Web UI port"`
	Bind    string `name:"bind" short:"b" default:"127.0.0.1" help:"Bind address (default: localhost only)"`
	TUI     bool   `name:"tui" help:"Use terminal TUI instead of web"`
	Manifest string `name:"manifest" short:"f" default:"" help:"Path to swarm.yaml (default: auto-detect in CWD)"`
}
type ReplayCmd struct {
	TaskID string `arg:"" help:"Task ID to replay"`
	Web    bool   `name:"web" short:"w" help:"Generate HTML and open in browser"`
}
type ChainCmd struct {
	Spec string `arg:"" help:"Chain spec: <cap1> \"<task>\" -> <cap2> -> ..."`
}
type PurgeCmd struct {
	Force bool `name:"force" short:"f" help:"Skip confirmation prompt"`
}

func main() {
	cli := &CLI{}
	ctx := kong.Parse(cli,
		kong.Name("swarm"),
		kong.Description("Personal swarm controller"),
	)

	home, _ := os.UserHomeDir()
	app := &app{
		natsURL:   cli.NATSURL,
		configDir: filepath.Join(home, ".config", "swarm"),
		dataDir:   filepath.Join(home, ".local", "share", "swarm"),
	}

	err := ctx.Run(app)
	ctx.FatalIfErrorf(err)
}

type app struct {
	natsURL   string
	configDir string
	dataDir   string
}

func (a *app) connect() (*nats.Conn, error) {
	nc, err := nats.Connect(a.natsURL)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return nc, nil
}

func (a *app) db() (*taskDB, error) {
	if err := os.MkdirAll(a.dataDir, 0755); err != nil {
		return nil, err
	}
	return openTaskDB(filepath.Join(a.dataDir, "swarm.db"))
}

func (s *StatusCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	// Check NATS status
	fmt.Printf("NATS: %s\n", a.natsURL)
	if !nc.IsConnected() {
		fmt.Println("Status: disconnected")
		return nil
	}
	fmt.Println("Status: connected")

	// Get task stats
	db, err := a.db()
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := db.Stats()
	if err != nil {
		return err
	}

	fmt.Printf("Tasks: %d total (%d success, %d failed, %d pending, %d running)\n",
		stats.Total, stats.Success, stats.Failed, stats.Pending, stats.Running)
	return nil
}

func (a *AgentsCmd) Run(app *app) error {
	nc, err := app.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	fmt.Fprintf(os.Stderr, "📡 Listening for heartbeats on: heartbeat.>\n")
	sub, err := nc.SubscribeSync("heartbeat.>")
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type agentInfo struct {
		id     string
		status string
		load   float64
		caps   []string
	}
	agents := map[string]*agentInfo{}

	for {
		msg, err := sub.NextMsgWithContext(ctx)
		if err != nil {
			break
		}
		var hb struct {
			AgentID  string            `json:"agent_id"`
			Status   string            `json:"status"`
			Load     float64           `json:"load"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(msg.Data, &hb); err != nil {
			continue
		}
		// Extract capabilities from metadata
		var caps []string
		if cap, ok := hb.Metadata["capability"]; ok && cap != "" {
			caps = []string{cap}
		}
		agents[hb.AgentID] = &agentInfo{
			id:     hb.AgentID,
			status: hb.Status,
			load:   hb.Load,
			caps:   caps,
		}
	}

	if len(agents) == 0 {
		fmt.Println("No agents discovered (wait 2s for heartbeats)")
		return nil
	}

	ids := make([]string, 0, len(agents))
	for id := range agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		ag := agents[id]
		caps := strings.Join(ag.caps, ",")
		fmt.Printf("%s\t%s\tload=%.2f\tcaps=%s\n", id, ag.status, ag.load, caps)
	}
	return nil
}

func (c *CapabilitiesCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("heartbeat.>")
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	capSet := map[string]int{}
	for {
		msg, err := sub.NextMsgWithContext(ctx)
		if err != nil {
			break
		}
		var hb struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(msg.Data, &hb); err != nil {
			continue
		}
		if cap, ok := hb.Metadata["capability"]; ok && cap != "" {
			capSet[cap]++
		}
	}

	if len(capSet) == 0 {
		fmt.Println("No capabilities discovered")
		return nil
	}

	caps := make([]string, 0, len(capSet))
	for cap := range capSet {
		caps = append(caps, cap)
	}
	sort.Strings(caps)
	for _, cap := range caps {
		fmt.Printf("%s\t(%d agents)\n", cap, capSet[cap])
	}
	return nil
}

func (s *SubmitCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	taskID := fmt.Sprintf("t-%s", uuid.New().String()[:8])

	inputs := map[string]string{}

	// 1. Process --input name=value flags
	for _, input := range s.Inputs {
		parts := strings.SplitN(input, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid input format '%s': expected name=value", input)
		}
		inputs[parts[0]] = parts[1]
	}

	// 2. Process JSON file if specified
	if s.File != "" {
		data, err := os.ReadFile(s.File)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse json: %w", err)
		}
		for k, v := range raw {
			switch val := v.(type) {
			case string:
				inputs[k] = val
			default:
				b, _ := json.Marshal(val)
				inputs[k] = string(b)
			}
		}
	}

	// 3. Process positional task argument (only if no inputs yet)
	if s.Task != "" && len(inputs) == 0 {
		// Try parse as JSON, else use as "task" field
		var raw map[string]any
		if err := json.Unmarshal([]byte(s.Task), &raw); err != nil {
			inputs["task"] = s.Task
		} else {
			for k, v := range raw {
				switch val := v.(type) {
				case string:
					inputs[k] = val
				default:
					b, _ := json.Marshal(val)
					inputs[k] = string(b)
				}
			}
		}
	}

	if len(inputs) == 0 {
		return fmt.Errorf("no inputs provided: use --input name=value or positional argument")
	}

	task := tasks.TaskMessage{
		TaskID:      taskID,
		Capability:  s.Capability,
		Inputs:      inputs,
		Attempt:     1,
		SubmittedAt: time.Now(),
	}

	data, err := task.Marshal()
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

	// Subscribe to result BEFORE publishing (NATS drops messages with no subscribers)
	// Default: wait for result. Use --nowait for fire-and-forget.
	var resultSub *nats.Subscription
	if !s.NoWait {
		resultSub, err = nc.SubscribeSync(fmt.Sprintf("done.*.%s", taskID))
		if err != nil {
			return fmt.Errorf("subscribe: %w", err)
		}
		defer resultSub.Unsubscribe()
	}

	// Publish task
	subject := fmt.Sprintf("work.%s.%s", s.Capability, taskID)
	if err := nc.Publish(subject, data); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	// Record in DB
	db, err := a.db()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.InsertTask(&task, "pending"); err != nil {
		return err
	}

	fmt.Println(taskID)

	// Wait for result (default behavior)
	if !s.NoWait {
		result, err := waitForResult(nc, taskID, db)
		if err != nil {
			return err
		}
		return printResult(result)
	}

	return nil
}

func (d *DiscussCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	taskID := fmt.Sprintf("t-%s", uuid.New().String()[:8])

	inputs := map[string]string{}
	for _, input := range d.Inputs {
		parts := strings.SplitN(input, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid input format '%s': expected name=value", input)
		}
		inputs[parts[0]] = parts[1]
	}

	if d.File != "" {
		data, err := os.ReadFile(d.File)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse json: %w", err)
		}
		for k, v := range raw {
			switch val := v.(type) {
			case string:
				inputs[k] = val
			default:
				b, _ := json.Marshal(val)
				inputs[k] = string(b)
			}
		}
	}

	if d.Task != "" && len(inputs) == 0 {
		var raw map[string]any
		if err := json.Unmarshal([]byte(d.Task), &raw); err != nil {
			inputs["task"] = d.Task
		} else {
			for k, v := range raw {
				switch val := v.(type) {
				case string:
					inputs[k] = val
				default:
					b, _ := json.Marshal(val)
					inputs[k] = string(b)
				}
			}
		}
	}

	if len(inputs) == 0 {
		return fmt.Errorf("no inputs provided: use --input name=value or positional argument")
	}

	task := tasks.TaskMessage{
		TaskID:      taskID,
		Inputs:      inputs,
		Attempt:     1,
		SubmittedAt: time.Now(),
	}

	data, err := task.Marshal()
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

	// Publish to discuss.* (all agents see it, triage decides who acts)
	subject := fmt.Sprintf("discuss.%s", taskID)
	if err := nc.Publish(subject, data); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Published discuss.%s — track progress in swarm ui\n", taskID)
	fmt.Println(taskID)
	return nil
}

func (r *ResultCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	db, err := a.db()
	if err != nil {
		return err
	}
	defer db.Close()

	// Check DB first
	res, err := db.GetResult(r.TaskID)
	if err == nil && res != nil {
		return printResult(res)
	}

	if !r.Wait {
		return fmt.Errorf("task %s not found or not complete (use --wait)", r.TaskID)
	}

	// Wait for result (heartbeat-aware, no fixed timeout)
	result, err := waitForResult(nc, r.TaskID, db)
	if err != nil {
		return err
	}
	return printResult(result)
}

func printResult(r *tasks.TaskResult) error {
	output := struct {
		TaskID     string      `json:"task_id"`
		Status     string      `json:"status"`
		Outputs    interface{} `json:"outputs,omitempty"`
		Error      string      `json:"error,omitempty"`
		DurationMs int64       `json:"duration_ms"`
	}{
		TaskID:     r.TaskID,
		Status:     string(r.Status),
		Outputs:    r.Outputs,
		Error:      r.Error,
		DurationMs: r.DurationMs,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func (h *HistoryCmd) Run(a *app) error {
	db, err := a.db()
	if err != nil {
		return err
	}
	defer db.Close()

	tasks, err := db.ListTasks(h.Capability, h.Status, h.Limit)
	if err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks found")
		return nil
	}

	for _, t := range tasks {
		fmt.Printf("%s\t%s\t%s\t%s\n", t.TaskID, t.Capability, t.Status, t.CreatedAt.Format("2006-01-02 15:04"))
	}
	return nil
}

func (u *UpCmd) Run(a *app) error {
	manifestPath := u.File
	if manifestPath == "" {
		var err error
		manifestPath, err = findManifest()
		if err != nil {
			return err
		}
	}

	m, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}

	fmt.Printf("Starting swarm from %s\n", manifestPath)
	fmt.Printf("NATS: %s\n", m.NATS.URL)
	fmt.Printf("State: %s\n", m.State.Location)

	// Connect to NATS for log forwarding to web UI
	var nc *nats.Conn
	if conn, err := nats.Connect(m.NATS.URL); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  NATS log forwarding unavailable: %v\n", err)
	} else {
		nc = conn
		defer nc.Close()
	}

	// Filter agents if specified
	agents := m.Agents
	if len(u.Agents) > 0 {
		agentSet := map[string]struct{}{}
		for _, name := range u.Agents {
			agentSet[name] = struct{}{}
		}
		filtered := make([]AgentSpec, 0)
		for _, ag := range agents {
			if _, ok := agentSet[ag.Name]; ok {
				filtered = append(filtered, ag)
			}
		}
		agents = filtered
	}

	// Load existing PID records and clean stale entries
	existingPIDs := cleanStalePIDs(loadPIDRecords(a.dataDir))

	// Start each agent (with replicas for workers)
	var newPIDs []pidRecord
	for _, ag := range agents {
		replicas := ag.Replicas
		if replicas < 1 {
			replicas = 1
		}
		// Managers always have exactly 1 replica
		if ag.Type == "manager" {
			replicas = 1
		}

		for replica := 0; replica < replicas; replica++ {
			displayName := ag.Name
			if replicas > 1 {
				displayName = fmt.Sprintf("%s-%d", ag.Name, replica+1)
			}

			fmt.Printf("  → %s [%s] (%s)\n", displayName, ag.Type, ag.Capability)
			args := []string{"serve", "--bus", m.NATS.URL}
			if ag.Config != "" {
				args = append(args, "--config", ag.Config)
			}
			if ag.Policy != "" {
				args = append(args, "--policy", ag.Policy)
			}
			if ag.Capability != "" {
				args = append(args, "--capability", ag.Capability)
			}
			// Auto-isolate state per agent instance under swarm state location
			agentState := ag.State
			if agentState == "" {
				agentState = filepath.Join(m.State.Location, "agents", displayName)
			}
			args = append(args, "--state", agentState)
			args = append(args, "--session-label", displayName)
			if ag.Agentfile != "" {
				args = append(args, ag.Agentfile)
			}

			cmd := exec.Command("agent", args...)
			// Pass agent type via environment variable
			cmd.Env = append(os.Environ(), fmt.Sprintf("AGENT_TYPE=%s", ag.Type))
			// For managers: pass worker capabilities so the dispatch tool knows targets
			if ag.Type == "manager" {
				var caps []string
				for _, other := range agents {
					if other.Type == "worker" && other.Capability != "" {
						caps = append(caps, fmt.Sprintf("%s:%d", other.Capability, other.Replicas))
					}
				}
				cmd.Env = append(cmd.Env, fmt.Sprintf("SWARM_CAPABILITIES=%s", strings.Join(caps, ",")))
			}
			// Prefix each agent's output with its name for multi-agent clarity
			stdoutPipe, _ := cmd.StdoutPipe()
			stderrPipe, _ := cmd.StderrPipe()
			if err := cmd.Start(); err != nil {
				fmt.Printf("  ✗ Failed to start %s: %v\n", displayName, err)
				continue
			}
			go prefixLines(displayName, ag.Capability, stdoutPipe, os.Stdout, nc)
			go prefixLines(displayName, ag.Capability, stderrPipe, os.Stderr, nc)

			// Brief pause to catch immediate crashes (missing binary, bad config, etc.)
			doneCh := make(chan error, 1)
			go func() { doneCh <- cmd.Wait() }()
			select {
			case err := <-doneCh:
				// Process already exited — it crashed
				fmt.Printf("  ✗ %s exited immediately: %v\n", displayName, err)
				if nc != nil {
					payload, _ := json.Marshal(map[string]string{
						"agent":      displayName,
						"line":       fmt.Sprintf("FATAL: agent exited immediately: %v", err),
						"capability": ag.Capability,
					})
					nc.Publish(fmt.Sprintf("log.%s", displayName), payload)
				}
				continue
			case <-time.After(500 * time.Millisecond):
				// Still running after 500ms — likely healthy
			}

			fmt.Printf("  ✓ Started %s (pid %d)\n", displayName, cmd.Process.Pid)
			newPIDs = append(newPIDs, pidRecord{
				Name:       displayName,
				PID:        cmd.Process.Pid,
				Capability: ag.Capability,
				StartedAt:  time.Now().Format(time.RFC3339),
			})
		}
	}

	// Save merged PID records
	allPIDs := append(existingPIDs, newPIDs...)
	if err := savePIDRecords(a.dataDir, allPIDs); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to save PID records: %v\n", err)
	}

	fmt.Println("Swarm started. Press Ctrl+C to stop all agents.")

	// Stay alive to pipe agent output and handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	fmt.Println("\nShutting down agents...")
	for _, r := range newPIDs {
		if isProcessAlive(r.PID) {
			proc, err := os.FindProcess(r.PID)
			if err == nil {
				proc.Signal(syscall.SIGTERM)
			}
		}
	}

	// Wait for graceful shutdown, then force kill survivors
	fmt.Println("Waiting 10s for graceful shutdown...")
	time.Sleep(10 * time.Second)

	for _, r := range newPIDs {
		if isProcessAlive(r.PID) {
			proc, err := os.FindProcess(r.PID)
			if err == nil {
				fmt.Printf("  ✗ %s (pid %d) didn't exit — SIGKILL\n", r.Name, r.PID)
				proc.Signal(syscall.SIGKILL)
			}
		}
	}

	return nil
}

func (d *DownCmd) Run(a *app) error {
	// Phase 1: Try NATS discovery + control signal
	natsOK := false
	nc, err := a.connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  NATS unavailable: %v\n", err)
	} else {
		defer nc.Close()
		natsOK = true
	}

	var signaled []string // agent names that received NATS shutdown signal
	if natsOK {
		agents := discoverAgentsViaHeartbeat(nc, 2*time.Second)

		// Filter if specific agents requested
		targets := agents
		if len(d.Agents) > 0 {
			agentSet := map[string]struct{}{}
			for _, name := range d.Agents {
				agentSet[name] = struct{}{}
			}
			filtered := make([]discoveredAgent, 0)
			for _, ag := range agents {
				if _, ok := agentSet[ag.name]; ok {
					filtered = append(filtered, ag)
				}
			}
			targets = filtered
		}

		// Send shutdown signal via NATS
		for _, ag := range targets {
			fmt.Printf("  → Stopping %s (NATS)\n", ag.name)
			if err := nc.Publish(fmt.Sprintf("control.%s.shutdown", ag.id), []byte{}); err != nil {
				fmt.Printf("  ✗ Failed to signal %s: %v\n", ag.name, err)
				continue
			}
			fmt.Printf("  ✓ Shutdown signal sent to %s\n", ag.name)
			signaled = append(signaled, ag.name)
		}
	}

	// Phase 2: PID fallback — wait briefly, then SIGTERM any still-alive processes
	pidRecords := loadPIDRecords(a.dataDir)
	if len(pidRecords) == 0 && len(signaled) == 0 {
		fmt.Println("No agents discovered (NATS) and no saved PIDs")
		return nil
	}

	if len(pidRecords) > 0 {
		// Filter PID records if specific agents requested
		targets := pidRecords
		if len(d.Agents) > 0 {
			agentSet := map[string]struct{}{}
			for _, name := range d.Agents {
				agentSet[name] = struct{}{}
			}
			filtered := make([]pidRecord, 0)
			for _, r := range pidRecords {
				if _, ok := agentSet[r.Name]; ok {
					filtered = append(filtered, r)
				}
			}
			targets = filtered
		}

		// Wait for NATS-signaled agents to exit gracefully
		if len(signaled) > 0 {
			fmt.Println("Waiting 3s for graceful shutdown...")
			time.Sleep(3 * time.Second)
		}

		// SIGTERM any still-alive processes from PID records
		for _, r := range targets {
			if !isProcessAlive(r.PID) {
				continue
			}
			fmt.Printf("  → Stopping %s (pid %d, SIGTERM)\n", r.Name, r.PID)
			proc, err := os.FindProcess(r.PID)
			if err != nil {
				continue
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				fmt.Printf("  ✗ Failed to SIGTERM %s (pid %d): %v\n", r.Name, r.PID, err)
			} else {
				fmt.Printf("  ✓ SIGTERM sent to %s (pid %d)\n", r.Name, r.PID)
			}
		}
	}

	// Notify UI that swarm is shutting down
	if natsOK {
		payload, _ := json.Marshal(map[string]interface{}{
			"event":  "swarm_shutdown",
			"agents": signaled,
		})
		nc.Publish("control.swarm.shutdown", payload)
		nc.Flush()
		// Brief pause to let the message propagate to WebSocket clients
		time.Sleep(200 * time.Millisecond)
	}

	// Clean up PID file
	remaining := cleanStalePIDs(loadPIDRecords(a.dataDir))
	if len(remaining) == 0 {
		os.Remove(pidFilePath(a.dataDir))
	} else {
		savePIDRecords(a.dataDir, remaining)
	}

	fmt.Println("Shutdown complete.")
	return nil
}

func (r *RestartCmd) Run(a *app) error {
	// Restart = down + up
	// For now, just warn that this requires a manifest
	fmt.Println("Restart requires a manifest file. Use: swarm down && swarm up")
	return nil
}

func (u *UICmd) Run(a *app) error {
	if u.TUI {
		p := tea.NewProgram(newTUIModel(a.natsURL), tea.WithAltScreen())
		_, err := p.Run()
		return err
	}

	// Web UI (default)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	db, err := a.db()
	if err != nil {
		return fmt.Errorf("open task DB: %w", err)
	}
	defer db.Close()

	// Discover state location from manifest (for session JSONL access)
	storageRoot := ""
	manifestPath := u.Manifest
	if manifestPath == "" {
		manifestPath, _ = findManifest()
	}
	if manifestPath != "" {
		if m, err := loadManifest(manifestPath); err == nil {
			storageRoot = m.State.Location
			log.Printf("[ui] state location: %s (from %s)", storageRoot, manifestPath)
		} else {
			log.Printf("[ui] failed to load manifest %s: %v", manifestPath, err)
		}
	}
	// Fall back to default state location if manifest not found or had no state path
	if storageRoot == "" {
		home, _ := os.UserHomeDir()
		storageRoot = filepath.Join(home, ".local", "share", "swarm")
		log.Printf("[ui] using default state location: %s", storageRoot)
	}

	srv := newWebServer(a.natsURL, a.dataDir, storageRoot, nil, db)

	// Primary bind address
	addr := fmt.Sprintf("%s:%d", u.Bind, u.Port)

	// Auto-detect Tailscale and expose via Serve (HTTPS with proper certs)
	port := fmt.Sprintf("%d", u.Port)
	if err := srv.enableTailscaleServe(ctx, port); err != nil {
		log.Printf("Tailscale Serve: %v (skipping — local-only mode)", err)
	} else {
		defer disableTailscaleServe(port)
	}

	return srv.start(ctx, addr)
}

func (r *ReplayCmd) Run(a *app) error {
	if r.Web {
		return replayWeb(r.TaskID)
	}
	return replayTask(r.TaskID)
}

func (c *ChainCmd) Run(a *app) error {
	// Parse chain spec: <cap1> "task" -> <cap2> -> <cap3>
	// Split on " -> " to get stages
	parts := strings.Split(c.Spec, " -> ")
	if len(parts) < 2 {
		return fmt.Errorf("chain requires at least 2 stages: <cap1> \"<task>\" -> <cap2>")
	}

	// First stage: capability + task
	first := parts[0]
	// Parse: capability "task"
	firstParts := strings.Fields(first)
	if len(firstParts) < 2 {
		return fmt.Errorf("first stage must be: <capability> \"<task>\"")
	}
	capability := firstParts[0]
	task := strings.Join(firstParts[1:], " ")
	task = strings.Trim(task, "\"")

	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	db, err := a.db()
	if err != nil {
		return err
	}
	defer db.Close()

	prevOutput := task
	for i, stage := range parts {
		var cap string
		if i == 0 {
			cap = capability
		} else {
			cap = strings.TrimSpace(stage)
		}

		fmt.Printf("Stage %d: %s\n", i+1, cap)

		taskID := fmt.Sprintf("t-%s", uuid.New().String()[:8])
		inputs := map[string]string{"task": prevOutput}

		tm := tasks.TaskMessage{
			TaskID:     taskID,
			Capability: cap,
			Inputs:     inputs,
			Attempt:    1,
		}

		data, err := tm.Marshal()
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}

		subject := fmt.Sprintf("work.%s.%s", cap, taskID)
		if err := nc.Publish(subject, data); err != nil {
			return fmt.Errorf("publish: %w", err)
		}

		if err := db.InsertTask(&tm, "pending"); err != nil {
			return err
		}

		// Wait for result
		sub, err := nc.SubscribeSync(fmt.Sprintf("done.%s.%s", cap, taskID))
		if err != nil {
			return fmt.Errorf("subscribe: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		msg, err := sub.NextMsgWithContext(ctx)
		cancel()
		sub.Unsubscribe()

		if err != nil {
			return fmt.Errorf("timeout waiting for stage %d: %w", i+1, err)
		}

		var result tasks.TaskResult
		if err := json.Unmarshal(msg.Data, &result); err != nil {
			return fmt.Errorf("parse result: %w", err)
		}

		if result.Status == tasks.ResultFailed {
			return fmt.Errorf("stage %d failed: %s", i+1, result.Error)
		}

		// Convert outputs to string for next stage
		if result.Outputs != nil {
			switch v := result.Outputs.(type) {
			case string:
				prevOutput = v
			case map[string]interface{}:
				if t, ok := v["task"].(string); ok {
					prevOutput = t
				} else {
					b, _ := json.Marshal(v)
					prevOutput = string(b)
				}
			default:
				b, _ := json.Marshal(v)
				prevOutput = string(b)
			}
		}

		fmt.Printf("  ✓ %s: %s\n", taskID, truncate(prevOutput, 60))
	}

	fmt.Println("\nChain complete.")
	fmt.Println("Final output:")
	fmt.Println(prevOutput)
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// taskDB provides SQLite persistence for task history.
type taskDB struct {
	dbPath string
}

type taskRecord struct {
	TaskID     string
	Capability string
	Status     string
	CreatedAt  time.Time
	DurationMs int64
}

type taskStats struct {
	Total   int
	Success int
	Failed  int
	Pending int
	Running int
}

func openTaskDB(path string) (*taskDB, error) {
	// Use modernc.org/sqlite (CGO-free) if available, else skip persistence
	// For now, use a simple JSON file approach
	return &taskDB{dbPath: path}, nil
}

func (d *taskDB) Close() error { return nil }

func (d *taskDB) InsertTask(task *tasks.TaskMessage, status string) error {
	records := d.loadRecords()

	// Deduplicate — skip if task already recorded
	for _, r := range records {
		if r.TaskID == task.TaskID {
			return nil
		}
	}

	records = append(records, taskRecord{
		TaskID:     task.TaskID,
		Capability: task.Capability,
		Status:     status,
		CreatedAt:  time.Now(),
	})
	if err := d.saveRecords(records); err != nil {
		return err
	}

	// Save full input message for later retrieval
	inputDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		return fmt.Errorf("create task input dir: %w", err)
	}
	data, err := task.Marshal()
	if err != nil {
		return fmt.Errorf("marshal task input: %w", err)
	}
	if err := os.WriteFile(filepath.Join(inputDir, task.TaskID+".input.json"), data, 0644); err != nil {
		return fmt.Errorf("write task input: %w", err)
	}
	return nil
}

func (d *taskDB) GetTask(taskID string) (*tasks.TaskMessage, error) {
	inputDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	data, err := os.ReadFile(filepath.Join(inputDir, taskID+".input.json"))
	if err != nil {
		return nil, err
	}
	return tasks.UnmarshalTaskMessage(data)
}

func (d *taskDB) UpdateResult(result *tasks.TaskResult) error {
	// Update status in records
	records := d.loadRecords()
	for i, r := range records {
		if r.TaskID == result.TaskID {
			records[i].Status = string(result.Status)
			records[i].DurationMs = result.DurationMs
			break
		}
	}
	if err := d.saveRecords(records); err != nil {
		return err
	}

	// Save full result to file for replay
	resultDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		return fmt.Errorf("create result dir: %w", err)
	}
	resultPath := filepath.Join(resultDir, result.TaskID+".json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := os.WriteFile(resultPath, data, 0644); err != nil {
		return fmt.Errorf("write result: %w", err)
	}

	return nil
}

func (d *taskDB) GetResult(taskID string) (*tasks.TaskResult, error) {
	// Check result files
	resultDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	data, err := os.ReadFile(filepath.Join(resultDir, taskID+".json"))
	if err != nil {
		return nil, err
	}
	var res tasks.TaskResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (d *taskDB) ListTasks(capability, status string, limit int) ([]taskRecord, error) {
	records := d.loadRecords()

	// Filter
	filtered := make([]taskRecord, 0)
	for _, r := range records {
		if capability != "" && r.Capability != capability {
			continue
		}
		if status != "" && r.Status != status {
			continue
		}
		filtered = append(filtered, r)
	}

	// Sort by created desc
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (d *taskDB) Stats() (taskStats, error) {
	records := d.loadRecords()
	var s taskStats
	for _, r := range records {
		s.Total++
		switch r.Status {
		case "success":
			s.Success++
		case "failed":
			s.Failed++
		case "pending":
			s.Pending++
		case "running":
			s.Running++
		}
	}
	return s, nil
}

func (d *taskDB) loadRecords() []taskRecord {
	recordsPath := filepath.Join(filepath.Dir(d.dbPath), "tasks.json")
	data, err := os.ReadFile(recordsPath)
	if err != nil {
		return nil
	}
	var records []taskRecord
	json.Unmarshal(data, &records)
	return records
}

func (d *taskDB) saveRecords(records []taskRecord) error {
	dir := filepath.Dir(d.dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	recordsPath := filepath.Join(dir, "tasks.json")
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(recordsPath, data, 0644)
}

// threadEntry represents a single contribution in a discuss thread.
type threadEntry struct {
	AgentID    string    `json:"agent_id"`
	Capability string    `json:"capability,omitempty"`
	Name       string    `json:"name,omitempty"` // swarm agent display name (for @mentions)
	Type       string    `json:"type"`           // "topic", "execute", "comment", "human"
	Content    string    `json:"content"`
	Round      int       `json:"round,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// AppendThread adds an entry to a discuss thread (JSONL file).
func (d *taskDB) AppendThread(taskID string, entry threadEntry) error {
	threadDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	if err := os.MkdirAll(threadDir, 0755); err != nil {
		return fmt.Errorf("create thread dir: %w", err)
	}
	threadPath := filepath.Join(threadDir, taskID+".thread.jsonl")
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal thread entry: %w", err)
	}
	f, err := os.OpenFile(threadPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open thread file: %w", err)
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// GetThread returns all entries in a discuss thread.
func (d *taskDB) GetThread(taskID string) ([]threadEntry, error) {
	threadDir := filepath.Join(filepath.Dir(d.dbPath), "tasks")
	threadPath := filepath.Join(threadDir, taskID+".thread.jsonl")
	data, err := os.ReadFile(threadPath)
	if err != nil {
		return nil, err
	}
	var entries []threadEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var entry threadEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// pidRecord tracks a started agent process.
type pidRecord struct {
	Name       string `json:"name"`
	PID        int    `json:"pid"`
	Capability string `json:"capability"`
	StartedAt  string `json:"started_at"`
}

func pidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "pids.json")
}

func loadPIDRecords(dataDir string) []pidRecord {
	data, err := os.ReadFile(pidFilePath(dataDir))
	if err != nil {
		return nil
	}
	var records []pidRecord
	json.Unmarshal(data, &records)
	return records
}

func savePIDRecords(dataDir string, records []pidRecord) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pidFilePath(dataDir), data, 0644)
}

// isProcessAlive checks if a process exists using kill(pid, 0).
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// cleanStalePIDs removes records for processes that no longer exist.
func cleanStalePIDs(records []pidRecord) []pidRecord {
	alive := make([]pidRecord, 0, len(records))
	for _, r := range records {
		if isProcessAlive(r.PID) {
			alive = append(alive, r)
		}
	}
	return alive
}

// discoveredAgent holds agent identity discovered via heartbeat for shutdown.
type discoveredAgent struct {
	id   string
	name string
}

// discoverAgentsViaHeartbeat listens for heartbeats and returns discovered agents.
func discoverAgentsViaHeartbeat(nc *nats.Conn, timeout time.Duration) []discoveredAgent {
	sub, err := nc.SubscribeSync("heartbeat.>")
	if err != nil {
		return nil
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	seen := map[string]bool{}
	var agents []discoveredAgent

	for {
		msg, err := sub.NextMsgWithContext(ctx)
		if err != nil {
			break
		}
		var hb struct {
			AgentID  string            `json:"agent_id"`
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(msg.Data, &hb); err != nil {
			continue
		}
		if seen[hb.AgentID] {
			continue
		}
		seen[hb.AgentID] = true
		name := hb.AgentID
		if n, ok := hb.Metadata["name"]; ok && n != "" {
			name = n
		}
		agents = append(agents, discoveredAgent{id: hb.AgentID, name: name})
	}
	return agents
}

// getUserHome returns the current user's home directory.
// prefixLines reads from r line-by-line and writes each line to w with a
// [name] prefix. Uses bufio.Reader to stream without line-length limits —
// bufio.Scanner silently dies on lines exceeding its buffer.
// If nc is non-nil, also publishes each line to NATS on log.<name>.
func prefixLines(name, capability string, r io.Reader, w io.Writer, nc *nats.Conn) {
	br := bufio.NewReaderSize(r, 8192)
	prefix := fmt.Sprintf("[%s] ", name)
	natsSubject := fmt.Sprintf("log.%s", name)

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			// Trim trailing newline for consistent formatting
			text := strings.TrimRight(line, "\n\r")
			fmt.Fprintf(w, "%s%s\n", prefix, text)
			if nc != nil {
				payload, _ := json.Marshal(map[string]string{
					"agent":      name,
					"capability": capability,
					"line":       text,
				})
				nc.Publish(natsSubject, payload)
			}
		}
		if err != nil {
			break // EOF or pipe closed
		}
	}
}

func getUserHome() string {
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return os.Getenv("HOME")
}

// execCmd runs a shell command.
func execCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// expandPath expands ~ to home directory.
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(getUserHome(), path[2:])
	}
	return path
}

func (p *PurgeCmd) Run(a *app) error {
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}

	streamName := swarm.StreamName

	// Check if stream exists
	info, err := js.StreamInfo(streamName)
	if err != nil {
		fmt.Printf("No stream '%s' found — nothing to purge.\n", streamName)
		return nil
	}

	if !p.Force {
		fmt.Printf("Stream '%s' has %d messages. Purge all? [y/N] ", streamName, info.State.Msgs)
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	if err := js.PurgeStream(streamName); err != nil {
		return fmt.Errorf("purge stream: %w", err)
	}

	fmt.Printf("Purged %d messages from stream '%s'.\n", info.State.Msgs, streamName)
	return nil
}
