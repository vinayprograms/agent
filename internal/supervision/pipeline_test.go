package supervision

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/vinayprograms/agent/internal/checkpoint"
)

// fakeStore records saves and can fail any of them.
type fakeStore struct {
	failPre, failPost, failReconcile, failSupervise bool
	saved                                           []string
	trail                                           []checkpoint.Checkpoint
}

func (s *fakeStore) save(kind string, fail bool) error {
	s.saved = append(s.saved, kind)
	if fail {
		return errors.New(kind + " failed")
	}
	return nil
}

func (s *fakeStore) SavePre(*checkpoint.PreCheckpoint) error   { return s.save("pre", s.failPre) }
func (s *fakeStore) SavePost(*checkpoint.PostCheckpoint) error { return s.save("post", s.failPost) }
func (s *fakeStore) SaveReconcile(*checkpoint.ReconcileResult) error {
	return s.save("reconcile", s.failReconcile)
}
func (s *fakeStore) SaveSupervise(*checkpoint.SuperviseResult) error {
	return s.save("supervise", s.failSupervise)
}
func (s *fakeStore) Trail() []checkpoint.Checkpoint { return s.trail }

// fakeSupervisor returns canned reconcile/supervise outcomes.
type fakeSupervisor struct {
	triggers    []string
	result      *checkpoint.SuperviseResult
	err         error
	lastRequest SuperviseRequest
}

func (f *fakeSupervisor) Reconcile(pre *checkpoint.PreCheckpoint, _ *checkpoint.PostCheckpoint) *checkpoint.ReconcileResult {
	return &checkpoint.ReconcileResult{StepID: pre.StepID, Triggers: f.triggers, Supervise: len(f.triggers) > 0}
}

func (f *fakeSupervisor) Supervise(_ context.Context, req SuperviseRequest) (*checkpoint.SuperviseResult, error) {
	f.lastRequest = req
	return f.result, f.err
}

// fakePhase records PhaseLogger calls as "kind:arg" strings.
type fakePhase struct{ calls []string }

func (p *fakePhase) LogPhaseReconcile(_, stepID string, _ []string, escalate bool, _ int64) {
	p.calls = append(p.calls, "reconcile:"+stepID)
}
func (p *fakePhase) LogPhaseSupervise(_, stepID, verdict, _ string, _ bool, _ int64) {
	p.calls = append(p.calls, "supervise:"+stepID+":"+verdict)
}
func (p *fakePhase) LogCheckpoint(checkpointType, _, _, _ string) {
	p.calls = append(p.calls, "checkpoint:"+checkpointType)
}

var (
	testPre  = &checkpoint.PreCheckpoint{StepID: "s1", Confidence: "high"}
	testPost = &checkpoint.PostCheckpoint{StepID: "s1", MetCommitment: true}
)

func commitPre(context.Context) *checkpoint.PreCheckpoint { return testPre }
func commitNil(context.Context) *checkpoint.PreCheckpoint { return nil }

func postOK(context.Context, *checkpoint.PreCheckpoint, string, []string) *checkpoint.PostCheckpoint {
	return testPost
}
func postNil(context.Context, *checkpoint.PreCheckpoint, string, []string) *checkpoint.PostCheckpoint {
	return nil
}

func execOK(context.Context) (*ExecuteResult, error) {
	return &ExecuteResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true}, nil
}

func TestPipelineRun(t *testing.T) {
	execErr := errors.New("exec failed")
	supErr := errors.New("llm down")

	tests := []struct {
		name       string
		store      *fakeStore // nil => unsupervised infrastructure
		sup        *fakeSupervisor
		withPhase  bool
		req        PipelineRequest
		work       Work
		wantErr    error
		wantResult PipelineResult
		wantSaved  []string
		wantPhase  []string
		wantEvents []string
		wantLog    []string
	}{
		{
			name:       "unsupervised request executes directly",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{},
			req:        PipelineRequest{StepID: "s1"},
			work:       Work{Execute: execOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
		},
		{
			name:       "supervised without store executes directly",
			sup:        &fakeSupervisor{},
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Execute: execOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
		},
		{
			name:       "execute error without partial result",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{},
			req:        PipelineRequest{StepID: "s1"},
			work:       Work{Execute: func(context.Context) (*ExecuteResult, error) { return nil, execErr }},
			wantErr:    execErr,
			wantResult: PipelineResult{Verdict: VerdictContinue},
		},
		{
			name:  "execute error with partial result",
			store: &fakeStore{},
			sup:   &fakeSupervisor{},
			req:   PipelineRequest{StepID: "s1"},
			work: Work{Execute: func(context.Context) (*ExecuteResult, error) {
				return &ExecuteResult{Output: "partial", ToolsUsed: []string{"x"}, ToolCallsMade: true}, execErr
			}},
			wantErr:    execErr,
			wantResult: PipelineResult{Output: "partial", ToolsUsed: []string{"x"}, ToolCallsMade: true, Verdict: VerdictContinue},
		},
		{
			name:       "commit returns nil skips supervision",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{},
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitNil, Execute: execOK, Post: postOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
		},
		{
			name:       "post-checkpoint nil skips supervision",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{},
			withPhase:  true,
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postNil},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
			wantSaved:  []string{"pre"},
			wantPhase:  []string{"checkpoint:pre"},
			wantEvents: []string{"commit"},
		},
		{
			name:       "save failures are warned not fatal",
			store:      &fakeStore{failPre: true, failPost: true, failReconcile: true, failSupervise: true},
			sup:        &fakeSupervisor{triggers: []string{"concerns_raised"}, result: &checkpoint.SuperviseResult{StepID: "s1", Verdict: "CONTINUE"}},
			withPhase:  true,
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
			wantSaved:  []string{"pre", "post", "reconcile", "supervise"},
			wantPhase:  []string{"reconcile:s1", "supervise:s1:CONTINUE", "checkpoint:supervise"},
			wantEvents: []string{"commit", "execute", "reconcile", "supervise"},
			wantLog: []string{
				"level=WARN", "step=s1",
				`msg="failed to save pre-checkpoint" step=s1 error="pre failed"`,
				`msg="failed to save post-checkpoint" step=s1 error="post failed"`,
				`msg="failed to save reconcile result" step=s1 error="reconcile failed"`,
				`msg="failed to save supervise result" step=s1 error="supervise failed"`,
			},
		},
		{
			name:       "no triggers and no human required stops after reconcile",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{},
			withPhase:  true,
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
			wantSaved:  []string{"pre", "post", "reconcile"},
			wantPhase:  []string{"checkpoint:pre", "checkpoint:post", "reconcile:s1"},
			wantEvents: []string{"commit", "execute", "reconcile"},
		},
		{
			name:       "human required forces supervise without triggers",
			store:      &fakeStore{trail: []checkpoint.Checkpoint{{Pre: testPre}}},
			sup:        &fakeSupervisor{result: &checkpoint.SuperviseResult{StepID: "s1", Verdict: "PAUSE", Question: "ok?"}},
			req:        PipelineRequest{StepID: "s1", GoalName: "the goal", Supervised: true, HumanRequired: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictPause, Question: "ok?"},
			wantSaved:  []string{"pre", "post", "reconcile", "supervise"},
			wantEvents: []string{"commit", "execute", "reconcile", "supervise"},
		},
		{
			name:       "reorient verdict carries correction",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{triggers: []string{"scope_deviation"}, result: &checkpoint.SuperviseResult{StepID: "s1", Verdict: "REORIENT", Correction: "fix it"}},
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postOK},
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictReorient, Correction: "fix it"},
			wantSaved:  []string{"pre", "post", "reconcile", "supervise"},
			wantEvents: []string{"commit", "execute", "reconcile", "supervise"},
		},
		{
			name:       "supervise error returns output with wrapped error",
			store:      &fakeStore{},
			sup:        &fakeSupervisor{triggers: []string{"scope_deviation"}, err: supErr},
			withPhase:  true,
			req:        PipelineRequest{StepID: "s1", Supervised: true},
			work:       Work{Commit: commitPre, Execute: execOK, Post: postOK},
			wantErr:    supErr,
			wantResult: PipelineResult{Output: "done", ToolsUsed: []string{"bash"}, ToolCallsMade: true, Verdict: VerdictContinue},
			wantSaved:  []string{"pre", "post", "reconcile"},
			wantPhase:  []string{"checkpoint:pre", "checkpoint:post", "reconcile:s1"},
			wantEvents: []string{"commit", "execute", "reconcile"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			var events []string
			var phase *fakePhase
			cfg := PipelineConfig{
				Supervisor: tt.sup,
				Logger:     slog.New(slog.NewTextHandler(&buf, nil)),
				Event:      func(_, phase string, _ any) { events = append(events, phase) },
			}
			if tt.store != nil {
				cfg.Store = tt.store
			}
			if tt.withPhase {
				phase = &fakePhase{}
				cfg.Phase = phase
			}

			result, err := NewPipeline(cfg).Run(t.Context(), tt.req, tt.work)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && errors.Is(tt.wantErr, supErr) && !strings.HasPrefix(err.Error(), "supervision failed: ") {
				t.Errorf("supervise error not wrapped: %v", err)
			}
			if result.Output != tt.wantResult.Output || result.ToolCallsMade != tt.wantResult.ToolCallsMade ||
				result.Verdict != tt.wantResult.Verdict || result.Correction != tt.wantResult.Correction ||
				result.Question != tt.wantResult.Question || strings.Join(result.ToolsUsed, ",") != strings.Join(tt.wantResult.ToolsUsed, ",") {
				t.Errorf("result = %+v, want %+v", *result, tt.wantResult)
			}
			if tt.store != nil && strings.Join(tt.store.saved, ",") != strings.Join(tt.wantSaved, ",") {
				t.Errorf("saved = %v, want %v", tt.store.saved, tt.wantSaved)
			}
			if strings.Join(events, ",") != strings.Join(tt.wantEvents, ",") {
				t.Errorf("events = %v, want %v", events, tt.wantEvents)
			}
			if phase != nil && strings.Join(phase.calls, ",") != strings.Join(tt.wantPhase, ",") {
				t.Errorf("phase calls = %v, want %v", phase.calls, tt.wantPhase)
			}
			for _, want := range tt.wantLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log missing %q:\n%s", want, buf.String())
				}
			}
			if len(tt.wantLog) == 0 && buf.Len() != 0 {
				t.Errorf("unexpected log output:\n%s", buf.String())
			}
		})
	}
}

func TestPipelineRun_SuperviseRequest(t *testing.T) {
	trail := []checkpoint.Checkpoint{{Pre: testPre}}
	sup := &fakeSupervisor{triggers: []string{"low_confidence"}, result: &checkpoint.SuperviseResult{StepID: "s1", Verdict: "CONTINUE"}}
	p := NewPipeline(PipelineConfig{Store: &fakeStore{trail: trail}, Supervisor: sup})

	_, err := p.Run(t.Context(), PipelineRequest{StepID: "s1", GoalName: "the goal", Supervised: true, HumanRequired: true}, Work{Commit: commitPre, Execute: execOK, Post: postOK})
	if err != nil {
		t.Fatal(err)
	}

	got := sup.lastRequest
	if got.OriginalGoal != "the goal" || got.Pre != testPre || got.Post != testPost || !got.HumanRequired ||
		len(got.DecisionTrail) != 1 || strings.Join(got.Triggers, ",") != "low_confidence" {
		t.Errorf("unexpected SuperviseRequest: %+v", got)
	}
}

func TestNewPipeline_NilLoggerDefaults(t *testing.T) {
	p := NewPipeline(PipelineConfig{})
	if p.cfg.Logger == nil {
		t.Error("nil Logger should fall back to slog.Default()")
	}
	// No OnEvent: fireEvent must be a no-op.
	p.fireEvent("s", "commit", nil)
}
