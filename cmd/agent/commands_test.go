package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/agentfile"
	"github.com/vinayprograms/agent/internal/config"
	"github.com/vinayprograms/agent/internal/executor"
	"github.com/vinayprograms/agent/internal/swarm"
	"github.com/vinayprograms/agentkit/credentials"
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
	src := writeAgentDir(t)
	keys := filepath.Join(t.TempDir(), "k")
	if err := runKeygen(keys); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if err := runKeygen(keys); err == nil {
		t.Error("second keygen on the same prefix should fail")
	}

	out := filepath.Join(t.TempDir(), "pkg.agent")
	err := runPack(&PackCmd{Dir: src, Output: out, Sign: keys + ".pem", Author: "a", Email: "a@b", License: "MIT"})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if err := runPack(&PackCmd{Dir: src, Output: out, Sign: "/nonexistent.pem"}); err == nil {
		t.Error("pack with missing key should fail")
	}
	if err := runPack(&PackCmd{Dir: t.TempDir(), Output: out}); err == nil {
		t.Error("pack without Agentfile should fail")
	}

	if err := runVerify(out, keys+".pub"); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := runVerify(out, ""); err != nil {
		t.Errorf("verify without key: %v", err)
	}
	if err := runVerify(out, "/nonexistent.pub"); err == nil {
		t.Error("verify with missing key should fail")
	}
	if err := runVerify("/nonexistent.agent", ""); err == nil {
		t.Error("verify of missing package should fail")
	}

	target := t.TempDir()
	if err := runInstall(&InstallCmd{Package: out, Target: target, Key: keys + ".pub", DryRun: true}); err != nil {
		t.Errorf("install dry-run: %v", err)
	}
	if err := runInstall(&InstallCmd{Package: out, Target: target, NoDeps: true}); err != nil {
		t.Errorf("install: %v", err)
	}
	if err := runInstall(&InstallCmd{Package: out, Target: target, Key: "/nonexistent.pub"}); err == nil {
		t.Error("install with missing key should fail")
	}
	if err := runInstall(&InstallCmd{Package: "/nonexistent.agent", Target: target}); err == nil {
		t.Error("install of missing package should fail")
	}

	// inspect both forms
	if err := runInspectWorkflow(filepath.Join(src, "Agentfile")); err != nil {
		t.Errorf("inspect workflow: %v", err)
	}
	if err := runInspectPackage(out); err != nil {
		t.Errorf("inspect package: %v", err)
	}
	if !isPackageFile(out) {
		t.Error("packed file should be detected as a package")
	}
	if err := (&InspectCmd{Path: out}).Run(); err != nil {
		t.Errorf("InspectCmd package: %v", err)
	}
	if err := (&InspectCmd{Path: filepath.Join(src, "Agentfile")}).Run(); err != nil {
		t.Errorf("InspectCmd workflow: %v", err)
	}
}

func TestCommandRuns_Simple(t *testing.T) {
	src := writeAgentDir(t)
	if err := (&ValidateCmd{File: filepath.Join(src, "Agentfile")}).Run(); err != nil {
		t.Errorf("validate: %v", err)
	}
	if err := (&ValidateCmd{File: filepath.Join(src, "missing")}).Run(); err == nil {
		t.Error("validate missing file should fail")
	}
	if err := (&VersionCmd{}).Run(); err != nil {
		t.Errorf("version: %v", err)
	}
	if err := (&RunCmd{File: filepath.Join(src, "missing")}).Run(); err == nil {
		t.Error("run with missing Agentfile should fail")
	}
	if err := (&KeygenCmd{Output: filepath.Join(t.TempDir(), "k")}).Run(); err != nil {
		t.Errorf("keygen cmd: %v", err)
	}

	root, cli := newRootCmd()
	if root == nil || cli == nil || len(root.Commands()) != 11 {
		t.Errorf("root command tree: %v", root)
	}
}

func TestWorkflowLoad(t *testing.T) {
	src := writeAgentDir(t)
	cfgPath := filepath.Join(src, "agent.toml")
	if err := os.WriteFile(cfgPath, []byte("[agent]\nworkspace = \""+src+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &workflow{agentfilePath: filepath.Join(src, "Agentfile"), configPath: cfgPath, workspacePath: src}
	if err := w.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if w.wf.Name != "pkg-test" || w.cfg.Agent.Workspace != src || w.pol == nil {
		t.Errorf("loaded: wf=%v ws=%q pol=%v", w.wf, w.cfg.Agent.Workspace, w.pol)
	}

	// Conflicting --workspace vs agent.toml is an error.
	w2 := &workflow{agentfilePath: filepath.Join(src, "Agentfile"), configPath: cfgPath, workspacePath: t.TempDir()}
	if err := w2.load(); err == nil || !strings.Contains(err.Error(), "workspace conflict") {
		t.Errorf("expected workspace conflict, got %v", err)
	}

	// Missing config path and missing Agentfile are errors.
	if err := (&workflow{agentfilePath: filepath.Join(src, "Agentfile"), configPath: "/nonexistent.toml"}).load(); err == nil {
		t.Error("expected config error")
	}
	if err := (&workflow{agentfilePath: "/nonexistent/Agentfile"}).load(); err == nil {
		t.Error("expected Agentfile error")
	}

	// Unset workspace defaults to the working directory.
	w3 := &workflow{agentfilePath: filepath.Join(src, "Agentfile")}
	if err := w3.loadConfig(); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	if w3.cfg.Agent.Workspace != expandAbsPath(cwd) {
		t.Errorf("workspace = %q", w3.cfg.Agent.Workspace)
	}
}

func TestExpandAbsPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandAbsPath("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("tilde: %q", got)
	}
	if got := expandAbsPath("rel"); !filepath.IsAbs(got) {
		t.Errorf("relative: %q", got)
	}
	if got := expandAbsPath("/abs"); got != "/abs" {
		t.Errorf("absolute: %q", got)
	}
}

func TestEnsureWorkspaceInAllowedDirs(t *testing.T) {
	ws := "/ws/project"
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, []string{ws}},
		{"exact", []string{ws}, []string{ws}},
		{"parent", []string{"/ws"}, []string{"/ws"}},
		{"placeholder", []string{"$WORKSPACE"}, []string{"$WORKSPACE"}},
		{"other", []string{"/tmp"}, []string{"/tmp", ws}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := testWorkflow(t, func(c *config.Config) { c.Agent.Workspace = ws })
			w.pol.AllowedDirs = tc.in
			w.ensureWorkspaceInAllowedDirs()
			if strings.Join(w.pol.AllowedDirs, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v want %v", w.pol.AllowedDirs, tc.want)
			}
		})
	}
	w := testWorkflow(t, func(c *config.Config) { c.Agent.Workspace = "" })
	w.pol.AllowedDirs = nil
	w.ensureWorkspaceInAllowedDirs()
	if w.pol.AllowedDirs != nil {
		t.Errorf("empty workspace should not add dirs: %v", w.pol.AllowedDirs)
	}
}

func TestServeAgent_IdleHandlers(t *testing.T) {
	w := testWorkflow(t, nil)
	rt := newRuntime(w, credentials.NewEnvStore())
	defer rt.cleanup()
	if err := rt.setup(); err != nil {
		t.Fatal(err)
	}
	a := &serviceAgent{wf: w, serviceRuntime: rt, capability: capabilitySchema{Name: "cap"}, taskDone: make(chan struct{})}

	// Nil bus / empty content: publishToDiscuss is a no-op.
	a.publishToDiscuss("t", "g", "content")
	// Idle correction is discarded without panicking.
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t1", Data: []byte("fix it")})
	// While executing, corrections land in the interrupt buffer.
	buf := executor.NewInterruptBuffer()
	rt.exec.SetInterruptBuffer(buf)
	task := swarm.NewTaskMessage("t2", "cap", map[string]string{"k": "v"})
	task.SubmittedBy = "mgr"
	data, _ := task.Marshal()
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t2", Data: data})
	a.handleInstanceMessage(&messaging.Message{Subject: "work.inst.t3", Data: []byte("raw")})
	rt.exec.SetInterruptBuffer(nil)

	// Manager discuss parsing: update, result, and garbage.
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: []byte(`{"instance_id":"i","task_id":"t","goal":"g","content":"c"}`)})
	res := swarm.NewTaskResult("t", "agent", swarm.ResultSuccess)
	rdata, _ := res.Marshal()
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: rdata})
	a.handleManagerDiscussMessage(t.Context(), &messaging.Message{Data: []byte("not json")})

	// Shutdown paths with nothing in flight.
	a.initiateShutdown(t.Context())
	a.initiateBusShutdown(t.Context())
	if a.status != "draining" {
		t.Errorf("status %q", a.status)
	}
}

func TestGetCapabilities(t *testing.T) {
	a := &serviceAgent{capability: capabilitySchema{Name: "cap"}}
	if got := a.getCapabilities(); len(got) != 1 || got[0] != "cap" {
		t.Errorf("got %v", got)
	}
	a = &serviceAgent{wf: &workflow{wf: &agentfile.Workflow{Name: "wfname"}}}
	if got := a.getCapabilities(); len(got) != 1 || got[0] != "wfname" {
		t.Errorf("got %v", got)
	}
}
