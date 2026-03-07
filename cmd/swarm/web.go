package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/vinayprograms/agentkit/tasks"
	"golang.org/x/net/websocket"
	tsclient "tailscale.com/client/local"
	"tailscale.com/ipn"
)

//go:embed static
var staticFiles embed.FS

// wsMessage is sent from server to browser.
type wsMessage struct {
	Type    string          `json:"type"`
	Subject string          `json:"subject"`
	Data    json.RawMessage `json:"data"`
}

// wsCommand is received from browser.
type wsCommand struct {
	Command string `json:"command"`
}

// webServer bridges NATS to WebSocket clients.
type webServer struct {
	nc          *nats.Conn
	natsURL     string
	clients     map[*websocket.Conn]bool
	mu          sync.RWMutex
	dataDir     string
	storageRoot string // agent session storage root (from manifest)
	pricing     map[string]modelPricing
	db          *taskDB

	// Cached state for reconnecting clients
	cacheMu        sync.RWMutex
	lastHeartbeats map[string][]byte // agent_id → last heartbeat JSON (wsMessage)
	recentLogs     [][]byte          // ring buffer of recent log wsMessages
	activeTasks    map[string][]byte // task_id → last task-related wsMessage (work/discuss)
}

type modelPricing struct {
	Input     float64 `yaml:"input"`
	Output    float64 `yaml:"output"`
	CacheRead float64 `yaml:"cache_read"`
}

func newWebServer(natsURL, dataDir, storageRoot string, pricing map[string]modelPricing, db *taskDB) *webServer {
	return &webServer{
		natsURL:        natsURL,
		clients:        make(map[*websocket.Conn]bool),
		dataDir:        dataDir,
		storageRoot:    storageRoot,
		pricing:        pricing,
		db:             db,
		lastHeartbeats: make(map[string][]byte),
		recentLogs:     make([][]byte, 0, 500),
		activeTasks:    make(map[string][]byte),
	}
}

func (s *webServer) start(ctx context.Context, addr string) error {
	// Connect to NATS
	nc, err := nats.Connect(s.natsURL,
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("NATS connect: %w", err)
	}
	s.nc = nc

	// Subscribe to all relevant subjects
	subjects := []string{"heartbeat.>", "work.>", "done.>", "discuss.>", "control.>", "log.>"}
	for _, subj := range subjects {
		sub := subj
		_, err := nc.Subscribe(sub, func(msg *nats.Msg) {
			s.broadcast(sub, msg)
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", sub, err)
		}
	}

	// Serve static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("static files: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", websocket.Handler(s.handleWS))
	mux.HandleFunc("/api/sessions/", s.handleSessionLogs)
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	// API for task details and discuss threads
	mux.HandleFunc("/api/task/", s.handleTaskDetail)
	mux.HandleFunc("/api/thread/", s.handleThreadAPI)
	mux.HandleFunc("/api/reply/", s.handleHumanReply)

	// Bind to localhost only (secure default)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	fmt.Printf("Mission Control: http://%s\n", addr)

	server := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutCtx)
	}()

	return server.Serve(listener)
}

func (s *webServer) broadcast(subject string, msg *nats.Msg) {
	// Determine message type from subject prefix
	msgType := classifySubject(subject)

	wsMsg := wsMessage{
		Type:    msgType,
		Subject: msg.Subject,
		Data:    msg.Data,
	}

	data, err := json.Marshal(wsMsg)
	if err != nil {
		return
	}

	// Persist incoming NATS data to task DB
	s.persistNATSMessage(msgType, subject, msg.Data)

	// Cache for reconnecting clients
	s.cacheMessage(msgType, msg.Subject, data)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for conn := range s.clients {
		go func(c *websocket.Conn) {
			c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			websocket.Message.Send(c, string(data))
		}(conn)
	}
}

// persistNATSMessage saves task inputs and results to the DB as they flow through NATS.
// This ensures the UI can always show task details, even for tasks submitted by other processes.
func (s *webServer) persistNATSMessage(msgType, subject string, data []byte) {
	if s.db == nil {
		return
	}

	switch msgType {
	case "work":
		// Persist task input: work.<cap>.<task_id>
		tm, err := tasks.UnmarshalTaskMessage(data)
		if err != nil {
			return
		}
		// Idempotent — InsertTask appends but GetTask uses file lookup
		s.db.InsertTask(tm, "pending")

		// Also record as thread topic so the task detail modal shows the full conversation
		taskContent := ""
		if task, ok := tm.Inputs["task"]; ok {
			taskContent = task
		}
		if taskContent != "" {
			s.db.AppendThread(tm.TaskID, threadEntry{
				AgentID:   tm.SubmittedBy,
				Type:      "topic",
				Content:   taskContent,
				Timestamp: tm.SubmittedAt,
			})
		}

	case "done":
		// Persist task result: done.<cap>.<task_id>
		var result tasks.TaskResult
		if err := json.Unmarshal(data, &result); err != nil {
			return
		}
		s.db.UpdateResult(&result)

	case "discuss":
		// Persist discuss contributions to thread
		s.persistDiscussContribution(subject, data)
	}
}

// persistDiscussContribution records a discuss message into the thread JSONL.
func (s *webServer) persistDiscussContribution(subject string, data []byte) {
	parts := strings.Split(subject, ".")
	if len(parts) < 2 {
		return
	}
	taskID := parts[len(parts)-1]

	// Try as TaskResult first (agent comments/results)
	var result tasks.TaskResult
	if err := json.Unmarshal(data, &result); err == nil && result.AgentID != "" {
		entryType := "execute"
		if result.Metadata != nil && result.Metadata["type"] == "comment" {
			entryType = "comment"
		}
		content := ""
		if s, ok := result.Outputs.(string); ok {
			content = s
		} else {
			b, _ := json.Marshal(result.Outputs)
			content = string(b)
		}
		cap := ""
		if result.Metadata != nil {
			cap = result.Metadata["capability"]
		}
		s.db.AppendThread(taskID, threadEntry{
			AgentID:    result.AgentID,
			Capability: cap,
			Type:       entryType,
			Content:    content,
			Timestamp:  result.CompletedAt,
		})
		// Also persist as task input for the task detail view
		return
	}

	// Try as TaskMessage (initial topic or follow-up with prior_output)
	tm, err := tasks.UnmarshalTaskMessage(data)
	if err != nil {
		return
	}

	// If it has prior_output metadata, it's an agent follow-up (already captured via result)
	if tm.Metadata != nil && tm.Metadata["prior_output"] != "" {
		return
	}

	// Initial discuss topic from human
	content := ""
	if task, ok := tm.Inputs["task"]; ok {
		content = task
	} else {
		for _, v := range tm.Inputs {
			content = v
			break
		}
	}

	s.db.InsertTask(tm, "discuss")
	s.db.AppendThread(taskID, threadEntry{
		AgentID:   tm.SubmittedBy,
		Type:      "topic",
		Content:   content,
		Timestamp: tm.SubmittedAt,
	})
}

func (s *webServer) handleWS(conn *websocket.Conn) {
	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
	}()

	// Send initial state (existing tasks from disk)
	s.sendInitialState(conn)

	// Read commands from browser
	for {
		var raw string
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			break
		}

		var cmd wsCommand
		if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
			continue
		}

		s.handleCommand(cmd.Command, conn)
	}
}

const maxCachedLogs = 500

func (s *webServer) cacheMessage(msgType, subject string, data []byte) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	switch msgType {
	case "heartbeat":
		// Extract agent_id from subject: heartbeat.<agent_id>
		parts := strings.SplitN(subject, ".", 2)
		if len(parts) == 2 {
			s.lastHeartbeats[parts[1]] = data
		}
	case "log":
		s.recentLogs = append(s.recentLogs, data)
		if len(s.recentLogs) > maxCachedLogs {
			s.recentLogs = s.recentLogs[len(s.recentLogs)-maxCachedLogs:]
		}
	case "work", "discuss":
		// Extract task_id from subject: work.<cap>.<task_id> or discuss.<task_id>
		parts := strings.Split(subject, ".")
		if len(parts) >= 2 {
			taskID := parts[len(parts)-1]
			s.activeTasks[taskID] = data
		}
	case "done":
		// Remove from active when done: done.<cap>.<task_id>
		parts := strings.Split(subject, ".")
		if len(parts) >= 2 {
			taskID := parts[len(parts)-1]
			delete(s.activeTasks, taskID)
		}
	}
}

func (s *webServer) sendInitialState(conn *websocket.Conn) {
	// 1. Replay last heartbeat per agent (restores agent cards)
	s.cacheMu.RLock()
	for _, data := range s.lastHeartbeats {
		websocket.Message.Send(conn, string(data))
	}
	// 2. Replay active tasks
	for _, data := range s.activeTasks {
		websocket.Message.Send(conn, string(data))
	}
	// 3. Replay recent logs (restores event log)
	for _, data := range s.recentLogs {
		websocket.Message.Send(conn, string(data))
	}
	s.cacheMu.RUnlock()

	// 3. Load completed tasks from DB (restores history table)
	if s.db == nil {
		return
	}
	records, err := s.db.ListTasks("", "", 50)
	if err != nil {
		return
	}

	for _, t := range records {
		data, _ := json.Marshal(t)
		msg := wsMessage{Type: "history", Subject: "", Data: data}
		out, _ := json.Marshal(msg)
		websocket.Message.Send(conn, string(out))
	}
}

func (s *webServer) handleCommand(cmd string, conn *websocket.Conn) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	// Strip leading /
	action := strings.TrimPrefix(parts[0], "/")

	switch action {
	case "task":
		s.handleTaskCommand(parts[1:])
	case "discuss":
		s.handleDiscussCommand(parts[1:])
	case "retry":
		s.handleRetryCommand(parts[1:])
	case "result":
		s.handleResultCommand(parts[1:], conn)
	case "shutdown":
		s.handleShutdownCommand()
	case "clear":
		// Client-side only, no server action needed
	default:
		log.Printf("Unknown command: %s", action)
	}
}

func (s *webServer) handleTaskCommand(args []string) {
	if len(args) < 2 {
		return
	}
	capability := args[0]
	task := strings.Trim(strings.Join(args[1:], " "), "\"")

	taskID := fmt.Sprintf("t-%d", time.Now().UnixNano()/1e6)
	subject := fmt.Sprintf("work.%s.%s", capability, taskID)

	// Subscribe for result before publishing
	doneSub := fmt.Sprintf("done.%s.%s", capability, taskID)
	sub, err := s.nc.SubscribeSync(doneSub)
	if err == nil {
		go func() {
			defer sub.Unsubscribe()
			msg, err := sub.NextMsg(10 * time.Minute)
			if err != nil {
				return
			}
			// Broadcast result to all WS clients
			s.broadcast(doneSub, msg)
		}()
	}

	taskMsg := &tasks.TaskMessage{
		TaskID:      taskID,
		Capability:  capability,
		Inputs:      map[string]string{"task": task},
		Attempt:     1,
		SubmittedAt: time.Now(),
	}
	data, _ := taskMsg.Marshal()

	if s.db != nil {
		s.db.InsertTask(taskMsg, "pending")
	}

	if err := s.nc.Publish(subject, data); err != nil {
		log.Printf("Publish error: %v", err)
	}
}

func (s *webServer) handleDiscussCommand(args []string) {
	if len(args) < 2 {
		return
	}
	capability := args[0]
	topic := strings.Trim(strings.Join(args[1:], " "), "\"")

	taskID := fmt.Sprintf("d-%d", time.Now().UnixNano()/1e6)
	subject := fmt.Sprintf("discuss.%s", taskID)

	taskMsg := &tasks.TaskMessage{
		TaskID:      taskID,
		Capability:  capability,
		Inputs:      map[string]string{"task": topic},
		Attempt:     1,
		SubmittedAt: time.Now(),
	}
	data, _ := taskMsg.Marshal()

	if s.db != nil {
		s.db.InsertTask(taskMsg, "discuss")
	}

	if err := s.nc.Publish(subject, data); err != nil {
		log.Printf("Publish error: %v", err)
	}
}

func (s *webServer) handleRetryCommand(args []string) {
	if len(args) < 1 || s.db == nil {
		return
	}
	taskID := args[0]

	// Look up capability from task records
	records, err := s.db.ListTasks("", "", 100)
	if err != nil {
		log.Printf("Retry: cannot load tasks: %v", err)
		return
	}
	var cap string
	for _, r := range records {
		if r.TaskID == taskID {
			cap = r.Capability
			break
		}
	}
	if cap == "" {
		log.Printf("Retry: task not found: %s", taskID)
		return
	}

	newID := fmt.Sprintf("t-%d", time.Now().UnixNano()/1e6)
	subject := fmt.Sprintf("work.%s.%s", cap, newID)

	payload := map[string]interface{}{
		"task_id":    newID,
		"capability": cap,
		"retry_of":   taskID,
	}
	data, _ := json.Marshal(payload)

	if err := s.nc.Publish(subject, data); err != nil {
		log.Printf("Publish error: %v", err)
	}
}

func (s *webServer) handleResultCommand(args []string, conn *websocket.Conn) {
	if len(args) < 1 || s.db == nil {
		return
	}
	taskID := args[0]

	result, err := s.db.GetResult(taskID)
	if err != nil {
		return
	}
	data, _ := json.Marshal(result)
	msg := wsMessage{Type: "result_detail", Subject: "", Data: data}
	out, _ := json.Marshal(msg)
	websocket.Message.Send(conn, string(out))
}

// handleTaskDetail returns input+result for a task ID (HTTP endpoint for modal)
func (s *webServer) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/task/")
	if path == "" || path == "/" {
		http.Error(w, "missing task_id", 400)
		return
	}
	taskID := strings.TrimSuffix(path, ".json")

	if s.db == nil {
		http.Error(w, "no task database", 500)
		return
	}

	input, inputErr := s.db.GetTask(taskID)
	result, resultErr := s.db.GetResult(taskID)

	if inputErr != nil {
		log.Printf("GetTask(%s): %v", taskID, inputErr)
	}
	if resultErr != nil {
		log.Printf("GetResult(%s): %v", taskID, resultErr)
	}

	data, _ := json.Marshal(map[string]interface{}{
		"task_id":  taskID,
		"input":    input,
		"result":   result,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleThreadAPI returns the full discuss thread for a task ID.
func (s *webServer) handleThreadAPI(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimPrefix(r.URL.Path, "/api/thread/")
	taskID = strings.TrimSuffix(taskID, "/")
	if taskID == "" {
		http.Error(w, "missing task_id", 400)
		return
	}
	if s.db == nil {
		http.Error(w, "no database", 500)
		return
	}

	thread, err := s.db.GetThread(taskID)
	if err != nil {
		// Return empty thread rather than error
		thread = []threadEntry{}
	}

	data, _ := json.Marshal(thread)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handleHumanReply lets the user inject a message into a discuss thread.
func (s *webServer) handleHumanReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/reply/")
	taskID = strings.TrimSuffix(taskID, "/")
	if taskID == "" {
		http.Error(w, "missing task_id", 400)
		return
	}

	var body struct {
		Message    string `json:"message"`
		Target     string `json:"target,omitempty"`     // @agent addressing
		Synthesize bool   `json:"synthesize,omitempty"` // request synthesis
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
		http.Error(w, "invalid body: need {\"message\": \"...\"}", 400)
		return
	}

	// Record in thread
	if s.db != nil {
		s.db.AppendThread(taskID, threadEntry{
			AgentID:   "human",
			Type:      "human",
			Content:   body.Message,
			Timestamp: time.Now(),
		})
	}

	// Build TaskMessage and publish to discuss.*
	meta := map[string]string{"type": "human_reply"}
	if body.Target != "" {
		meta["target_agent"] = body.Target
	}

	// For synthesis requests, inject full thread context so agents have all contributions
	taskContent := body.Message
	if body.Synthesize && s.db != nil {
		thread, err := s.db.GetThread(taskID)
		if err == nil && len(thread) > 0 {
			var ctx strings.Builder
			ctx.WriteString("THREAD CONTEXT (all contributions so far):\n\n")
			for _, e := range thread {
				label := e.AgentID
				if e.Capability != "" {
					label = e.Capability
				}
				ctx.WriteString(fmt.Sprintf("[%s] (%s):\n%s\n\n", label, e.Type, e.Content))
			}
			ctx.WriteString("---\n\nSYNTHESIS REQUEST:\n")
			ctx.WriteString(body.Message)
			taskContent = ctx.String()
			meta["type"] = "synthesis_request"
		}
	}

	tm := &tasks.TaskMessage{
		TaskID:      taskID,
		Inputs:      map[string]string{"task": taskContent},
		Metadata:    meta,
		Attempt:     1,
		SubmittedBy: "human",
		SubmittedAt: time.Now(),
	}
	data, _ := tm.Marshal()

	subject := fmt.Sprintf("discuss.%s", taskID)
	if err := s.nc.Publish(subject, data); err != nil {
		http.Error(w, "publish failed", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok": true}`))
}

func (s *webServer) handleShutdownCommand() {
	// Publish shutdown to all known agents
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Load PIDs and send control shutdown
	pids := loadPIDRecords(s.dataDir)
	for _, p := range pids {
		subject := fmt.Sprintf("control.%s.shutdown", p.Name)
		s.nc.Publish(subject, []byte(`{"action":"shutdown"}`))
	}
}

// enableTailscaleServe configures Tailscale Serve to proxy HTTPS → localhost.
// Uses the existing machine's Tailscale identity (like OpenClaw gateway does).
// Access at https://<machine>.tail<xxx>.ts.net/ — proper Let's Encrypt certs via Tailscale.
func (s *webServer) enableTailscaleServe(ctx context.Context, localPort string) error {
	lc := &tsclient.Client{}

	// Get machine's DNS name and cert domains
	status, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		return fmt.Errorf("tailscale status: %w (is tailscaled running?)", err)
	}

	if len(status.CertDomains) == 0 {
		return fmt.Errorf("no Tailscale cert domains available — enable HTTPS in Tailscale admin console")
	}

	domain := status.CertDomains[0]

	// Get existing serve config (preserve other entries)
	sc, err := lc.GetServeConfig(ctx)
	if err != nil {
		return fmt.Errorf("get serve config: %w", err)
	}
	if sc == nil {
		sc = &ipn.ServeConfig{}
	}

	// Configure: HTTPS on :443 → proxy to localhost:<port>
	hostPort := ipn.HostPort(domain + ":443")

	if sc.TCP == nil {
		sc.TCP = make(map[uint16]*ipn.TCPPortHandler)
	}
	sc.TCP[443] = &ipn.TCPPortHandler{HTTPS: true}

	if sc.Web == nil {
		sc.Web = make(map[ipn.HostPort]*ipn.WebServerConfig)
	}
	sc.Web[hostPort] = &ipn.WebServerConfig{
		Handlers: map[string]*ipn.HTTPHandler{
			"/": {Proxy: "http://127.0.0.1:" + localPort},
		},
	}

	if err := lc.SetServeConfig(ctx, sc); err != nil {
		return fmt.Errorf("set serve config: %w", err)
	}

	fmt.Printf("Mission Control (Tailscale): https://%s/\n", domain)
	return nil
}

// disableTailscaleServe removes the Tailscale Serve proxy config on shutdown.
func disableTailscaleServe(localPort string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lc := &tsclient.Client{}
	sc, err := lc.GetServeConfig(ctx)
	if err != nil || sc == nil {
		return
	}

	// Remove our port 443 entries
	delete(sc.TCP, 443)
	for hp := range sc.Web {
		if strings.HasSuffix(string(hp), ":443") {
			delete(sc.Web, hp)
		}
	}

	_ = lc.SetServeConfig(ctx, sc)
}

// handleSessionLogs serves JSONL session logs for an agent.
// GET /api/sessions/<agent-name>/<session-id> → returns array of JSONL records.
// Path on disk: <storageRoot>/agents/<name>/sessions/<name>/<session-id>.jsonl
func (s *webServer) handleSessionLogs(w http.ResponseWriter, r *http.Request) {
	if s.storageRoot == "" {
		http.Error(w, "storage root not configured", http.StatusServiceUnavailable)
		return
	}

	// Extract agent name and session ID from URL: /api/sessions/<agent-name>/<session-id>
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	path = strings.TrimSuffix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "usage: /api/sessions/<agent-name>/<session-id>", http.StatusBadRequest)
		return
	}
	agentName := parts[0]
	sessionID := parts[1]

	// Sanitize (prevent path traversal)
	for _, s := range []string{agentName, sessionID} {
		if strings.Contains(s, "..") || strings.Contains(s, "/") || strings.Contains(s, "\\") {
			http.Error(w, "invalid parameter", http.StatusBadRequest)
			return
		}
	}

	// Session JSONL at: <storageRoot>/agents/<name>/sessions/<name>/<session-id>.jsonl
	jsonlPath := filepath.Join(s.storageRoot, "agents", agentName, "sessions", agentName, sessionID+".jsonl")

	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Parse JSONL lines into array
	var records []json.RawMessage
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		records = append(records, json.RawMessage(line))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

func classifySubject(subject string) string {
	switch {
	case strings.HasPrefix(subject, "heartbeat."):
		return "heartbeat"
	case strings.HasPrefix(subject, "work."):
		return "work"
	case strings.HasPrefix(subject, "done."):
		return "done"
	case strings.HasPrefix(subject, "discuss."):
		return "discuss"
	case strings.HasPrefix(subject, "control."):
		return "control"
	case strings.HasPrefix(subject, "log."):
		return "log"
	default:
		return "unknown"
	}
}
