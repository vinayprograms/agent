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
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
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
	nc      *nats.Conn
	natsURL string
	clients map[*websocket.Conn]bool
	mu      sync.RWMutex
	dataDir string
	pricing map[string]modelPricing
	db      *taskDB
}

type modelPricing struct {
	Input     float64 `yaml:"input"`
	Output    float64 `yaml:"output"`
	CacheRead float64 `yaml:"cache_read"`
}

func newWebServer(natsURL, dataDir string, pricing map[string]modelPricing, db *taskDB) *webServer {
	return &webServer{
		natsURL: natsURL,
		clients: make(map[*websocket.Conn]bool),
		dataDir: dataDir,
		pricing: pricing,
		db:      db,
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
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

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

	s.mu.RLock()
	defer s.mu.RUnlock()

	for conn := range s.clients {
		go func(c *websocket.Conn) {
			c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			websocket.Message.Send(c, string(data))
		}(conn)
	}
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

func (s *webServer) sendInitialState(conn *websocket.Conn) {
	if s.db == nil {
		return
	}
	// Load completed tasks from DB
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

	payload := map[string]interface{}{
		"task_id":    taskID,
		"task":       task,
		"capability": capability,
	}
	data, _ := json.Marshal(payload)

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

	payload := map[string]interface{}{
		"task_id":    taskID,
		"task":       topic,
		"capability": capability,
	}
	data, _ := json.Marshal(payload)

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
