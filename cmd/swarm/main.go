package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agentkit/tasks"
)

type CLI struct {
	NATSURL string `name:"nats" help:"NATS server URL" default:"nats://localhost:4222"`

	Status       StatusCmd       `cmd:"" help:"Show swarm status"`
	Agents       AgentsCmd       `cmd:"" help:"List registered agents"`
	Capabilities CapabilitiesCmd `cmd:"" help:"List capabilities in swarm"`
	Submit       SubmitCmd       `cmd:"" help:"Submit a task to an agent"`
	Result       ResultCmd       `cmd:"" help:"Get result for a task"`
	History      HistoryCmd      `cmd:"" help:"Show recent tasks"`
	Up           UpCmd           `cmd:"" help:"Start swarm from swarm.yaml"`
	Down         DownCmd         `cmd:"" help:"Stop swarm agents"`
	Restart      RestartCmd      `cmd:"" help:"Restart swarm agents"`
	UI           UICmd           `cmd:"" help:"Interactive TUI dashboard"`
	Replay       ReplayCmd       `cmd:"" help:"Replay task execution"`
	Chain        ChainCmd        `cmd:"" help:"Chain tasks through multiple agents"`
}

type StatusCmd struct{}
type AgentsCmd struct{}
type CapabilitiesCmd struct{}
type SubmitCmd struct {
	Capability string   `arg:"" help:"Capability to route task to"`
	Inputs     []string `name:"input" short:"i" help:"Input as name=value (can repeat)"`
	File       string   `name:"file" short:"f" help:"Load inputs from JSON file" type:"existingfile"`
	Task       string   `arg:"" optional:"" help:"Task description (used as 'task' input if no --input specified)"`
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
	File   string   `name:"file" short:"f" help:"Manifest file" type:"existingfile"`
	Agents []string `arg:"" optional:"" help:"Specific agents to start (default: all)"`
}
type DownCmd struct {
	Agents []string `arg:"" optional:"" help:"Specific agents to stop (default: all)"`
}
type RestartCmd struct {
	Agents []string `arg:"" optional:"" help:"Specific agents to restart (default: all)"`
}
type UICmd struct{}
type ReplayCmd struct {
	TaskID string `arg:"" help:"Task ID to replay"`
	Web    bool   `name:"web" short:"w" help:"Generate HTML and open in browser"`
}
type ChainCmd struct {
	Spec string `arg:"" help:"Chain spec: <cap1> \"<task>\" -> <cap2> -> ..."`
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
		TaskID:     taskID,
		Capability: s.Capability,
		Inputs:     inputs,
		Attempt:    1,
	}

	data, err := task.Marshal()
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

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

	// Wait for result on done.* subject
	sub, err := nc.SubscribeSync(fmt.Sprintf("done.*.%s", r.TaskID))
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	msg, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return fmt.Errorf("timeout waiting for result: %w", err)
	}

	var result tasks.TaskResult
	if err := json.Unmarshal(msg.Data, &result); err != nil {
		return fmt.Errorf("parse result: %w", err)
	}

	// Save to DB
	if err := db.UpdateResult(&result); err != nil {
		return err
	}

	return printResult(&result)
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
	fmt.Printf("Storage: %s\n", m.Storage.Root)

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

	// Start each agent
	for _, ag := range agents {
		fmt.Printf("  → %s (%s)\n", ag.Name, ag.Capability)
		args := []string{"serve", "--bus", m.NATS.URL}
		if ag.Agentfile != "" {
			args = append(args, "-f", ag.Agentfile)
		}
		if ag.Config != "" {
			args = append(args, "--config", ag.Config)
		}
		if ag.Policy != "" {
			args = append(args, "--policy", ag.Policy)
		}
		if ag.Capability != "" {
			args = append(args, "--capability", ag.Capability)
		}

		cmd := exec.Command("agent", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("  ✗ Failed to start %s: %v\n", ag.Name, err)
			continue
		}
		fmt.Printf("  ✓ Started %s (pid %d)\n", ag.Name, cmd.Process.Pid)
	}

	fmt.Println("Swarm started. Use 'swarm agents' to verify.")
	return nil
}

func (d *DownCmd) Run(a *app) error {
	// Graceful shutdown via NATS signal
	nc, err := a.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	// Get list of agents from heartbeats
	sub, err := nc.SubscribeSync("heartbeat.>")
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type agentInfo struct {
		id   string
		name string
	}
	agents := []agentInfo{}

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
		name := hb.AgentID
		if n, ok := hb.Metadata["name"]; ok && n != "" {
			name = n
		}
		agents = append(agents, agentInfo{id: hb.AgentID, name: name})
	}

	if len(agents) == 0 {
		fmt.Println("No agents discovered")
		return nil
	}

	// Filter if specific agents requested
	targets := agents
	if len(d.Agents) > 0 {
		agentSet := map[string]struct{}{}
		for _, name := range d.Agents {
			agentSet[name] = struct{}{}
		}
		filtered := make([]agentInfo, 0)
		for _, ag := range agents {
			if _, ok := agentSet[ag.name]; ok {
				filtered = append(filtered, ag)
			}
		}
		targets = filtered
	}

	// Send shutdown signal
	for _, ag := range targets {
		fmt.Printf("  → Stopping %s\n", ag.name)
		// Publish to control.<agent_id>.shutdown
		if err := nc.Publish(fmt.Sprintf("control.%s.shutdown", ag.id), []byte{}); err != nil {
			fmt.Printf("  ✗ Failed to signal %s: %v\n", ag.name, err)
			continue
		}
		fmt.Printf("  ✓ Shutdown signal sent to %s\n", ag.name)
	}

	fmt.Println("Shutdown signals sent. Agents will drain and exit.")
	return nil
}

func (r *RestartCmd) Run(a *app) error {
	// Restart = down + up
	// For now, just warn that this requires a manifest
	fmt.Println("Restart requires a manifest file. Use: swarm down && swarm up")
	return nil
}

func (u *UICmd) Run(a *app) error {
	p := tea.NewProgram(newTUIModel(a.natsURL), tea.WithAltScreen())
	_, err := p.Run()
	return err
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
	// Simple JSON append for now - will migrate to SQLite
	records := d.loadRecords()
	records = append(records, taskRecord{
		TaskID:     task.TaskID,
		Capability: task.Capability,
		Status:     status,
		CreatedAt:  time.Now(),
	})
	return d.saveRecords(records)
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

// getUserHome returns the current user's home directory.
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
