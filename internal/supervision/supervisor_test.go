package supervision

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vinayprograms/agent/internal/checkpoint"
	"github.com/vinayprograms/agent/internal/testutil/llmmock"
	"github.com/vinayprograms/agentkit/llm"
)

// newTestSupervisor wires a supervisor to a mock model and a buffered logger
// so tests can assert both the verdict and what was logged.
func newTestSupervisor(t *testing.T, cfg Config) (*LLMSupervisor, *llmmock.Model, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	model := llmmock.New()
	cfg.Model = model
	cfg.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewLLMSupervisor(cfg), model, &buf
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name     string
		pre      *checkpoint.PreCheckpoint
		post     *checkpoint.PostCheckpoint
		triggers []string
	}{
		{
			name: "no triggers",
			pre:  &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high"},
			post: &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true},
		},
		{
			name:     "concerns raised",
			pre:      &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high"},
			post:     &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true, Concerns: []string{"Data quality is questionable"}},
			triggers: []string{string(TriggerConcernsRaised)},
		},
		{
			name:     "commitment not met with deviation",
			pre:      &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high"},
			post:     &checkpoint.PostCheckpoint{StepID: "goal-001", Deviations: []string{"Could not complete due to API error"}},
			triggers: []string{string(TriggerCommitmentNotMet), string(TriggerScopeDeviation)},
		},
		{
			name:     "unexpected results",
			pre:      &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high"},
			post:     &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true, Unexpected: []string{"found extra data"}},
			triggers: []string{string(TriggerUnexpectedResults)},
		},
		{
			name:     "low confidence",
			pre:      &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "low"},
			post:     &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true},
			triggers: []string{string(TriggerLowConfidence)},
		},
		{
			name:     "excess assumptions",
			pre:      &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high", Assumptions: []string{"1", "2", "3", "4"}},
			post:     &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true},
			triggers: []string{string(TriggerExcessAssumptions)},
		},
		{
			name: "all triggers",
			pre:  &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "low", Assumptions: []string{"1", "2", "3", "4", "5"}},
			post: &checkpoint.PostCheckpoint{
				StepID:     "goal-001",
				Concerns:   []string{"Multiple issues found"},
				Deviations: []string{"Changed scope"},
				Unexpected: []string{"Found unrelated data"},
			},
			triggers: []string{
				string(TriggerConcernsRaised), string(TriggerCommitmentNotMet), string(TriggerScopeDeviation),
				string(TriggerUnexpectedResults), string(TriggerLowConfidence), string(TriggerExcessAssumptions),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup, _, buf := newTestSupervisor(t, Config{})
			result := sup.Reconcile(tt.pre, tt.post)

			if result.StepID != tt.pre.StepID {
				t.Errorf("StepID = %q, want %q", result.StepID, tt.pre.StepID)
			}
			if want := len(tt.triggers) > 0; result.Supervise != want {
				t.Errorf("Supervise = %v, want %v", result.Supervise, want)
			}
			if !slices.Equal(result.Triggers, tt.triggers) {
				t.Errorf("Triggers = %v, want %v", result.Triggers, tt.triggers)
			}
			for _, want := range []string{"msg=reconcile_phase", "component=supervisor", "step=goal-001", "msg=phase_complete", "phase=RECONCILE"} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log missing %q:\n%s", want, buf.String())
				}
			}
		})
	}
}

func TestParseSupervisionResponse(t *testing.T) {
	tests := []struct {
		input      string
		verdict    Verdict
		correction string
		question   string
	}{
		{input: "CONTINUE", verdict: VerdictContinue},
		{input: "continue", verdict: VerdictContinue},
		{input: "CONTINUE: All good", verdict: VerdictContinue},
		{input: "  CONTINUE  ", verdict: VerdictContinue},
		{input: "REORIENT: Focus on consumer EVs only", verdict: VerdictReorient, correction: "Focus on consumer EVs only"},
		{input: `REORIENT: "Include more sources"`, verdict: VerdictReorient, correction: "Include more sources"},
		{input: "REORIENT", verdict: VerdictReorient},
		{input: "PAUSE: Should we include commercial vehicles?", verdict: VerdictPause, question: "Should we include commercial vehicles?"},
		{input: "PAUSE", verdict: VerdictPause},
		{input: "Thinking...\npause: really?", verdict: VerdictPause, question: "really?"},
		{input: "The agent seems to be doing fine.", verdict: VerdictContinue},
		{input: "", verdict: VerdictContinue},
	}

	sup := NewLLMSupervisor(Config{})
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			verdict, correction, question := sup.parseSupervisionResponse(tt.input)
			if verdict != tt.verdict || correction != tt.correction || question != tt.question {
				t.Errorf("got (%s, %q, %q), want (%s, %q, %q)", verdict, correction, question, tt.verdict, tt.correction, tt.question)
			}
		})
	}
}

func TestNewLLMSupervisor(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		sup := NewLLMSupervisor(Config{})
		if sup.humanInputTimeout != 5*time.Minute {
			t.Errorf("timeout = %v, want 5m", sup.humanInputTimeout)
		}
		if sup.logger == nil {
			t.Error("nil Logger should fall back to slog.Default()")
		}
		if sup.humanAvailable {
			t.Error("expected human not available")
		}
		sup.SetHumanAvailable(true)
		if !sup.humanAvailable {
			t.Error("expected human available after set")
		}
	})
	t.Run("custom timeout", func(t *testing.T) {
		sup := NewLLMSupervisor(Config{HumanInputTimeout: 10 * time.Second})
		if sup.humanInputTimeout != 10*time.Second {
			t.Errorf("timeout = %v, want 10s", sup.humanInputTimeout)
		}
	})
}

func TestBuildSupervisionPrompt(t *testing.T) {
	sup := NewLLMSupervisor(Config{})
	pre := &checkpoint.PreCheckpoint{
		StepID:          "s1",
		Interpretation:  "interp",
		Approach:        "approach",
		PredictedOutput: "out",
		Confidence:      "medium",
		ScopeOut:        []string{"x", "y"},
		Assumptions:     []string{"a1", "a2"},
	}
	post := &checkpoint.PostCheckpoint{
		MetCommitment: false,
		Deviations:    []string{"d1"},
		Concerns:      []string{"c1"},
		Unexpected:    []string{"u1"},
		ToolsUsed:     []string{"bash", "read"},
	}
	trail := []checkpoint.Checkpoint{
		{Pre: &checkpoint.PreCheckpoint{StepID: "s0", Interpretation: "earlier"}},
		{Pre: nil},
	}

	got := sup.buildSupervisionPrompt("the goal", pre, post, []string{"t1", "t2"}, trail)

	for _, want := range []string{
		"ORIGINAL GOAL: the goal",
		"- Interpretation: interp",
		"- Excluded from scope: x, y",
		"- Assumptions: a1; a2",
		"- Met commitment: false",
		"- Deviations: d1",
		"- Concerns: c1",
		"- Unexpected: u1",
		"- Tools used: bash, read",
		"TRIGGERED BY: t1, t2",
		"DECISION TRAIL:\n- s0: earlier\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}

	minimal := sup.buildSupervisionPrompt("g", &checkpoint.PreCheckpoint{}, &checkpoint.PostCheckpoint{}, nil, nil)
	for _, absent := range []string{"Excluded from scope", "Assumptions:", "Deviations:", "Concerns:", "Unexpected:", "DECISION TRAIL"} {
		if strings.Contains(minimal, absent) {
			t.Errorf("minimal prompt should not contain %q", absent)
		}
	}
}

func TestSupervise(t *testing.T) {
	pre := &checkpoint.PreCheckpoint{StepID: "goal-001", Confidence: "high"}
	post := &checkpoint.PostCheckpoint{StepID: "goal-001", MetCommitment: true}
	baseReq := SuperviseRequest{OriginalGoal: "goal", Pre: pre, Post: post, Triggers: []string{"concerns_raised"}}

	tests := []struct {
		name           string
		cfg            Config
		humanRequired  bool
		responses      []string // successive model replies
		modelErr       error
		humanInput     string // sent on the channel once the model has answered
		cancelOnPause  bool
		wantErr        string
		wantVerdict    Verdict
		wantCorrection string
		wantQuestion   string
		wantCalls      int
		wantLog        []string
	}{
		{
			name:        "continue",
			responses:   []string{"CONTINUE"},
			wantVerdict: VerdictContinue,
			wantCalls:   1,
			wantLog:     []string{"msg=phase_start", "phase=SUPERVISE", "msg=supervise_phase", "msg=supervisor_verdict", "verdict=CONTINUE", "human_required=false", "msg=phase_complete", "result=CONTINUE"},
		},
		{
			name:           "reorient",
			responses:      []string{`REORIENT: "narrow the scope"`},
			wantVerdict:    VerdictReorient,
			wantCorrection: "narrow the scope",
			wantCalls:      1,
			wantLog:        []string{"verdict=REORIENT", `guidance="narrow the scope"`},
		},
		{
			name:      "model error",
			modelErr:  errors.New("boom"),
			wantErr:   "supervisor LLM error: boom",
			wantCalls: 1,
			wantLog:   []string{"msg=supervisor_llm_error", "error=boom"},
		},
		{
			name:          "pause human required but unavailable",
			humanRequired: true,
			responses:     []string{"PAUSE: which one?"},
			wantErr:       "supervision requires human input but no human is available",
			wantCalls:     1,
			wantLog:       []string{"verdict=PAUSE_FAILED", "human_required=true"},
		},
		{
			name:           "pause human answers",
			cfg:            Config{HumanAvailable: true, HumanInputChan: make(chan string, 1)},
			responses:      []string{"PAUSE: which one?"},
			humanInput:     "use the second",
			wantVerdict:    VerdictReorient,
			wantCorrection: "use the second",
			wantQuestion:   "which one?",
			wantCalls:      1,
			wantLog:        []string{"msg=\"waiting for human input\"", "question=\"which one?\"", "verdict=REORIENT", "guidance=\"human provided input\""},
		},
		{
			name:          "pause timeout human required",
			cfg:           Config{HumanAvailable: true, HumanInputChan: make(chan string), HumanInputTimeout: 5 * time.Millisecond},
			humanRequired: true,
			responses:     []string{"PAUSE: which one?"},
			wantErr:       "human input timeout - workflow requires human approval",
			wantCalls:     1,
			wantLog:       []string{"verdict=PAUSE_TIMEOUT"},
		},
		{
			name:           "pause timeout falls back to continue",
			cfg:            Config{HumanAvailable: true, HumanInputChan: make(chan string), HumanInputTimeout: 5 * time.Millisecond},
			responses:      []string{"PAUSE: which one?"},
			wantVerdict:    VerdictContinue,
			wantCorrection: "Proceeding without human input (timeout). Review output carefully.",
			wantQuestion:   "which one?",
			wantCalls:      1,
			wantLog:        []string{"level=WARN", "msg=\"human input timeout, supervisor will decide\"", "guidance=\"timeout fallback\""},
		},
		{
			name:          "pause context cancelled while waiting",
			cfg:           Config{HumanAvailable: true, HumanInputChan: make(chan string)},
			responses:     []string{"PAUSE: which one?"},
			cancelOnPause: true,
			wantErr:       context.Canceled.Error(),
			wantCalls:     1,
		},
		{
			name:           "pause autonomous reorient",
			responses:      []string{"PAUSE: which one?", "VERDICT: REORIENT\nCORRECTION: pick the safe path"},
			wantVerdict:    VerdictReorient,
			wantCorrection: "pick the safe path",
			wantQuestion:   "which one?",
			wantCalls:      2,
			wantLog:        []string{"msg=\"no human available, supervisor deciding autonomously\"", "guidance=\"autonomous decision\"", "verdict=REORIENT"},
		},
		{
			name:         "pause autonomous continue by default",
			responses:    []string{"PAUSE: which one?", "I think it's fine"},
			wantVerdict:  VerdictContinue,
			wantQuestion: "which one?",
			wantCalls:    2,
		},
		{
			name:         "pause autonomous model error",
			responses:    []string{"PAUSE: which one?"},
			modelErr:     errors.New("second call failed"),
			wantErr:      "second call failed",
			wantQuestion: "which one?",
			wantCalls:    2,
		},
		{
			name:          "pause human available without channel and required stays paused",
			cfg:           Config{HumanAvailable: true},
			humanRequired: true,
			responses:     []string{"PAUSE: which one?"},
			wantVerdict:   VerdictPause,
			wantQuestion:  "which one?",
			wantCalls:     1,
			wantLog:       []string{"result=PAUSE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup, model, buf := newTestSupervisor(t, tt.cfg)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			model.ChatFunc = func(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
				n := model.CallCount() - 1
				if n >= len(tt.responses) {
					return nil, tt.modelErr
				}
				if n == 0 {
					if tt.humanInput != "" {
						tt.cfg.HumanInputChan <- tt.humanInput
					}
					if tt.cancelOnPause {
						cancel()
					}
				}
				return &llm.ChatResponse{Content: tt.responses[n]}, nil
			}

			req := baseReq
			req.HumanRequired = tt.humanRequired
			result, err := sup.Supervise(ctx, req)

			if model.CallCount() != tt.wantCalls {
				t.Errorf("model calls = %d, want %d", model.CallCount(), tt.wantCalls)
			}
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if result != nil {
					t.Errorf("result should be nil on error, got %+v", result)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result.StepID != pre.StepID {
					t.Errorf("StepID = %q, want %q", result.StepID, pre.StepID)
				}
				if Verdict(result.Verdict) != tt.wantVerdict {
					t.Errorf("Verdict = %q, want %q", result.Verdict, tt.wantVerdict)
				}
				if result.Correction != tt.wantCorrection {
					t.Errorf("Correction = %q, want %q", result.Correction, tt.wantCorrection)
				}
				if result.Question != tt.wantQuestion {
					t.Errorf("Question = %q, want %q", result.Question, tt.wantQuestion)
				}
			}
			for _, want := range tt.wantLog {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("log missing %q:\n%s", want, buf.String())
				}
			}
		})
	}
}

func TestSupervise_PromptContents(t *testing.T) {
	sup, model, _ := newTestSupervisor(t, Config{})
	model.SetResponse("CONTINUE")

	_, err := sup.Supervise(t.Context(), SuperviseRequest{
		OriginalGoal: "write the report",
		Pre:          &checkpoint.PreCheckpoint{StepID: "s1", Interpretation: "draft it"},
		Post:         &checkpoint.PostCheckpoint{StepID: "s1", MetCommitment: true},
		Triggers:     []string{"low_confidence"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := model.LastRequest()
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[0].Content != supervisorSystemPrompt {
		t.Fatalf("unexpected messages: %+v", req.Messages)
	}
	user := req.Messages[1].Content
	for _, want := range []string{"ORIGINAL GOAL: write the report", "- Interpretation: draft it", "TRIGGERED BY: low_confidence"} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
