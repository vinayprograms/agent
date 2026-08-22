package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/run"
	"github.com/vinayprograms/agent/internal/session"
	"github.com/vinayprograms/agent/internal/swarm"
	"github.com/vinayprograms/agentkit/credentials"
	"github.com/vinayprograms/agentkit/policy"
	"github.com/vinayprograms/swarmkit/messaging"
)

const minimalAgentfile = `NAME pkg-test
INPUT topic DEFAULT "golang"
GOAL analyze "Analyze $topic" -> summary
RUN main USING analyze
`

func writeAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Agentfile"), []byte(minimalAgentfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPackVerifyInstall_RoundTrip(t *testing.T) {
	var out2 bytes.Buffer
	src := writeAgentDir(t)
	keys := filepath.Join(t.TempDir(), "k")
	if err := runKeygen(&out2, keys); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if got := out2.String(); !strings.Contains(got, "Generated key pair") || !strings.Contains(got, keys+".pem") {
		t.Errorf("keygen output = %q, missing expected fields", got)
	}
	if err := runKeygen(io.Discard, keys); err == nil {
		t.Error("second keygen on the same prefix should fail")
	}

	out := filepath.Join(t.TempDir(), "pkg.agent")
	out2.Reset()
	err := runPack(&out2, &packOptions{Dir: src, Output: out, Sign: keys + ".pem", Author: "a", Email: "a@b", License: "MIT"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if got := out2.String(); !strings.Contains(got, "Created "+out) || !strings.Contains(got, "Signed: yes") {
		t.Errorf("pack output = %q, missing expected fields", got)
	}
	if err := runPack(io.Discard, &packOptions{Dir: src, Output: out, Sign: "/nonexistent.pem"}); err == nil {
		t.Error("pack with missing key should fail")
	}
	if err := runPack(io.Discard, &packOptions{Dir: t.TempDir(), Output: out}); err == nil {
		t.Error("pack without Agentfile should fail")
	}

	out2.Reset()
	if err := runVerify(&out2, out, keys+".pub"); err != nil {
		t.Errorf("verify: %v", err)
	}
	if got := out2.String(); !strings.Contains(got, "Package verified") || !strings.Contains(got, "Signature: valid") {
		t.Errorf("verify output = %q, missing expected fields", got)
	}
	if err := runVerify(io.Discard, out, ""); err != nil {
		t.Errorf("verify without key: %v", err)
	}
	if err := runVerify(io.Discard, out, "/nonexistent.pub"); err == nil {
		t.Error("verify with missing key should fail")
	}
	if err := runVerify(io.Discard, "/nonexistent.agent", ""); err == nil {
		t.Error("verify of missing package should fail")
	}

	target := t.TempDir()
	out2.Reset()
	if err := runInstall(&out2, &installOptions{Package: out, Target: target, Key: keys + ".pub", DryRun: true}); err != nil {
		t.Errorf("install dry-run: %v", err)
	}
	if got := out2.String(); !strings.Contains(got, "Dry run") {
		t.Errorf("install dry-run output = %q, missing expected fields", got)
	}
	if err := runInstall(io.Discard, &installOptions{Package: out, Target: target, NoDeps: true}); err != nil {
		t.Errorf("install: %v", err)
	}
	if err := runInstall(io.Discard, &installOptions{Package: out, Target: target, Key: "/nonexistent.pub"}); err == nil {
		t.Error("install with missing key should fail")
	}
	if err := runInstall(io.Discard, &installOptions{Package: "/nonexistent.agent", Target: target}); err == nil {
		t.Error("install of missing package should fail")
	}

	// inspect both forms
	out2.Reset()
	if err := runInspectWorkflow(&out2, filepath.Join(src, "Agentfile")); err != nil {
		t.Errorf("inspect workflow: %v", err)
	}
	if got := out2.String(); !strings.Contains(got, "Workflow: pkg-test") {
		t.Errorf("inspect workflow output = %q, missing expected fields", got)
	}
	if err := runInspectPackage(io.Discard, out); err != nil {
		t.Errorf("inspect package: %v", err)
	}
	if !isPackageFile(out) {
		t.Error("packed file should be detected as a package")
	}
}

// serveRuntime builds a real runtime over local (ollama) models — no
// credentials or network are needed to construct one — and the service
// agent that wraps it.
func serveRuntime(t *testing.T) (*serviceAgent, *[]session.Event) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.New()
	cfg.Agent.Workspace = dir
	cfg.State.Location = filepath.Join(dir, "state")
	cfg.LLM = config.LLMConfig{Provider: "ollama-local", Model: "llama3"}
	pol := policy.New()
	pol.DefaultDeny = false
	pol.AllowedDirs = []string{dir}
	loaded := &run.Loaded{
		Workflow: &agentfile.Workflow{
			Name:  "serve-test",
			Goals: []agentfile.Goal{{Name: "g", Outcome: "do it"}},
			Steps: []agentfile.Step{{Type: agentfile.StepRUN, Name: "s", UsingGoals: []string{"g"}}},
		},
		Config: cfg,
		Policy: pol,
	}
	var (
		mu     sync.Mutex
		events []session.Event
	)
	rt, err := run.New(t.Context(), loaded, run.Deps{
		Creds: credentials.NewEnvStore(),
		Sink: func(e session.Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		},
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}
	t.Cleanup(rt.Close)
	return &serviceAgent{
		loaded:         loaded,
		serviceRuntime: rt,
		stderr:         io.Discard,
		capability:     capabilitySchema{Name: "cap"},
		status:         "idle",
		taskDone:       make(chan struct{}, 1),
	}, &events
}

func TestServeAgent_IdleHandlers(t *testing.T) {
	a, events := serveRuntime(t)

	// Nil bus / empty content: publishToDiscuss is a no-op.
	a.publishToDiscuss("t", "g", "content")
	// An idle correction is recorded in the session rather than dropped.
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t1", Data: []byte("fix it")})
	last := (*events)[len(*events)-1]
	if last.Type != session.EventWarning || !strings.Contains(last.Content, "fix it") {
		t.Errorf("idle correction not logged to the session: %+v", *events)
	}

	// While executing, corrections land in the interrupt buffer.
	buf := executor.NewInterruptBuffer()
	a.interrupts.Store(buf)
	task := swarm.NewTaskMessage("t2", "cap", map[string]string{"k": "v"})
	task.SubmittedBy = "mgr"
	data, _ := task.Marshal()
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t2", Data: data})
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t3", Data: []byte("raw")})
	a.interrupts.Store(nil)

	// Manager discuss parsing: update, result, and garbage.
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: []byte(`{"instance_id":"i","task_id":"t","goal":"g","content":"c"}`)})
	res := swarm.NewTaskResult("t", "agent", swarm.ResultSuccess)
	rdata, _ := res.Marshal()
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: rdata})
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: []byte("not json")})

	// Shutdown paths with nothing in flight.
	a.initiateShutdown(t.Context())
	a.initiateBusShutdown()
	if a.state() != "draining" {
		t.Errorf("status %q", a.state())
	}
}

// TestServeAgent_HTTPHandler exercises the HTTP surface end to end: the
// task endpoint runs through the shared executor, so a task that needs the
// (absent) model fails rather than hanging.
func TestServeAgent_HTTPHandler(t *testing.T) {
	a, _ := serveRuntime(t)
	srv := httptest.NewServer(a.handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if health["status"] != "idle" || health["capability"] != "cap" {
		t.Errorf("health = %v", health)
	}

	resp, err = http.Get(srv.URL + "/capability")
	if err != nil {
		t.Fatal(err)
	}
	var schema capabilitySchema
	json.NewDecoder(resp.Body).Decode(&schema)
	resp.Body.Close()
	if schema.Name != "cap" {
		t.Errorf("capability = %+v", schema)
	}

	// Wrong method, oversized body and invalid payloads are all rejected.
	resp, err = http.Get(srv.URL + "/task")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /task = %d", resp.StatusCode)
	}
	for _, body := range []string{"not json", `{"task_id":""}`, `{"x":"` + strings.Repeat("y", maxTaskBody) + `"}`} {
		resp, err := http.Post(srv.URL+"/task", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /task %.20q = %d", body, resp.StatusCode)
		}
	}

	// Draining refuses new work.
	a.setStatus("draining")
	resp, err = http.Post(srv.URL+"/task", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("draining POST /task = %d", resp.StatusCode)
	}
}

// TestServeAgent_ExecuteTaskSerialises pins that concurrent submissions do
// not run on the shared executor at the same time.
func TestServeAgent_ExecuteTaskSerialises(t *testing.T) {
	a, _ := serveRuntime(t)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task := swarm.NewTaskMessage(fmt.Sprintf("t%d", i), "cap", map[string]string{"k": "v"})
			if res := a.executeTask(t.Context(), task); res == nil {
				t.Error("nil result")
			}
		}()
	}
	wg.Wait()
	if a.state() != "idle" || a.busy() {
		t.Errorf("state after tasks: %q busy=%v", a.state(), a.busy())
	}
}

func TestGetCapabilities(t *testing.T) {
	a := &serviceAgent{capability: capabilitySchema{Name: "cap"}}
	if got := a.getCapabilities(); len(got) != 1 || got[0] != "cap" {
		t.Errorf("got %v", got)
	}
	a = &serviceAgent{loaded: &run.Loaded{Workflow: &agentfile.Workflow{Name: "wfname"}}}
	if got := a.getCapabilities(); len(got) != 1 || got[0] != "wfname" {
		t.Errorf("got %v", got)
	}
}

// TestServeAgent_ShutdownWaitsForRunningTask pins the drain contract: a
// completed task must not leave a signal behind that lets a later shutdown
// walk away from a task that is still executing.
func TestServeAgent_ShutdownWaitsForRunningTask(t *testing.T) {
	a, _ := serveRuntime(t)
	a.drainTimeout = 5 * time.Second

	// Two tasks run to completion, then a third is in flight.
	for _, id := range []string{"t1", "t2"} {
		a.executeTask(t.Context(), swarm.NewTaskMessage(id, "cap", map[string]string{"k": "v"}))
	}
	a.exec.Lock()
	a.setState("busy", swarm.NewTaskMessage("t3", "cap", map[string]string{"k": "v"}))

	const held = 250 * time.Millisecond
	go func() {
		time.Sleep(held)
		a.setState("idle", nil)
		a.exec.Unlock()
	}()

	start := time.Now()
	a.initiateShutdown(t.Context())
	if waited := time.Since(start); waited < held {
		t.Errorf("shutdown returned after %s, before the running task finished (%s)", waited, held)
	}
}

// TestServeAgent_BusShutdownWaitsForRunningTask is the bus-mode half of
// the same contract.
func TestServeAgent_BusShutdownWaitsForRunningTask(t *testing.T) {
	a, _ := serveRuntime(t)
	a.drainTimeout = 5 * time.Second

	a.executeTask(t.Context(), swarm.NewTaskMessage("t1", "cap", map[string]string{"k": "v"}))
	a.exec.Lock()
	a.setState("busy", swarm.NewTaskMessage("t2", "cap", map[string]string{"k": "v"}))

	const held = 250 * time.Millisecond
	go func() {
		time.Sleep(held)
		a.setState("idle", nil)
		a.exec.Unlock()
	}()

	start := time.Now()
	a.initiateBusShutdown()
	if waited := time.Since(start); waited < held {
		t.Errorf("bus shutdown returned after %s, before the running task finished (%s)", waited, held)
	}
}

// TestServeAgent_AwaitIdleTimesOut pins the forced-shutdown escape hatch.
func TestServeAgent_AwaitIdleTimesOut(t *testing.T) {
	a, _ := serveRuntime(t)
	a.exec.Lock()
	defer a.exec.Unlock()
	if a.awaitIdle(20 * time.Millisecond) {
		t.Error("awaitIdle should time out while a task holds the executor")
	}
}

// TestServeAgent_AwaitTaskDone pins that a shutdown signal does not make
// the pull loop abandon (and Nak) a task that is still draining.
func TestServeAgent_AwaitTaskDone(t *testing.T) {
	a, _ := serveRuntime(t)
	a.drainTimeout = time.Second

	// Already finished.
	a.taskDone <- struct{}{}
	if !a.awaitTaskDone(t.Context()) {
		t.Error("a completed task should report done")
	}

	// Shutdown signalled while the task is still draining: keep waiting.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.taskDone <- struct{}{}
	}()
	if !a.awaitTaskDone(ctx) {
		t.Error("a draining task must be waited for, not abandoned")
	}

	// Drain deadline expires with no completion: the caller must Nak.
	a.drainTimeout = 20 * time.Millisecond
	if a.awaitTaskDone(ctx) {
		t.Error("expected the drain deadline to expire")
	}
}

// TestTaskContext pins that a shutdown signal drains rather than aborting
// the task in flight.
func TestTaskContext(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	taskCtx, cancelTasks := taskContext(parent)
	defer cancelTasks()

	cancelParent()
	if err := taskCtx.Err(); err != nil {
		t.Errorf("a shutdown signal must not abort the task: %v", err)
	}
	cancelTasks()
	if !errors.Is(taskCtx.Err(), context.Canceled) {
		t.Errorf("task context should be cancellable by its owner: %v", taskCtx.Err())
	}
}
