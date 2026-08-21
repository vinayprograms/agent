package llmmock

import (
	"context"
	"errors"
	"testing"

	"github.com/vinayprograms/agentkit/llm"
)

func TestChat(t *testing.T) {
	user := []llm.Message{{Role: "user", Content: "hi"}}
	withToolResult := []llm.Message{
		{Role: "user", Content: "read"},
		{Role: "assistant", ToolCalls: []llm.ToolCallResponse{{ID: "tc1", Name: "read"}}},
		{Role: "tool", ToolCallID: "tc1", Content: "hello world"},
	}
	calls := []llm.ToolCallResponse{
		{ID: "1", Name: "read", Args: map[string]any{"path": "/a"}},
		{ID: "2", Name: "read", Args: map[string]any{"path": "/b"}},
	}

	tests := []struct {
		name  string
		setup func(*Model)
		msgs  []llm.Message
		want  llm.ChatResponse
	}{
		{
			name:  "defaults",
			setup: func(*Model) {},
			msgs:  user,
			want:  llm.ChatResponse{StopReason: "end_turn"},
		},
		{
			name: "response tokens and stop reason",
			setup: func(m *Model) {
				m.SetResponse("done")
				m.SetTokenCounts(100, 50)
				m.SetStopReason("max_tokens")
			},
			msgs: user,
			want: llm.ChatResponse{Content: "done", StopReason: "max_tokens", InputTokens: 100, OutputTokens: 50},
		},
		{
			name:  "single tool call",
			setup: func(m *Model) { m.SetToolCall("read", map[string]any{"path": "/t"}) },
			msgs:  user,
			want: llm.ChatResponse{
				StopReason: "end_turn",
				ToolCalls:  []llm.ToolCallResponse{{ID: "tc-1", Name: "read", Args: map[string]any{"path": "/t"}}},
			},
		},
		{
			name:  "multiple tool calls",
			setup: func(m *Model) { m.SetToolCalls(calls) },
			msgs:  user,
			want:  llm.ChatResponse{StopReason: "end_turn", ToolCalls: calls},
		},
		{
			name: "tool result present suppresses tool calls",
			setup: func(m *Model) {
				m.SetToolCalls(calls)
				m.SetResponse("File contains: hello world")
				m.SetTokenCounts(3, 4)
			},
			msgs: withToolResult,
			want: llm.ChatResponse{Content: "File contains: hello world", StopReason: "end_turn", InputTokens: 3, OutputTokens: 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			tt.setup(m)
			got, err := m.Chat(context.Background(), llm.ChatRequest{Messages: tt.msgs})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if got.Content != tt.want.Content || got.StopReason != tt.want.StopReason ||
				got.InputTokens != tt.want.InputTokens || got.OutputTokens != tt.want.OutputTokens {
				t.Errorf("got %+v, want %+v", *got, tt.want)
			}
			if len(got.ToolCalls) != len(tt.want.ToolCalls) {
				t.Fatalf("tool calls: got %d, want %d", len(got.ToolCalls), len(tt.want.ToolCalls))
			}
			for i := range got.ToolCalls {
				g, w := got.ToolCalls[i], tt.want.ToolCalls[i]
				if g.ID != w.ID || g.Name != w.Name || len(g.Args) != len(w.Args) {
					t.Errorf("tool call %d: got %+v, want %+v", i, g, w)
				}
				for k, v := range w.Args {
					if g.Args[k] != v {
						t.Errorf("tool call %d arg %q: got %v, want %v", i, k, g.Args[k], v)
					}
				}
			}
		})
	}
}

func TestChatError(t *testing.T) {
	m := New()
	want := errors.New("boom")
	m.SetError(want)
	resp, err := m.Chat(context.Background(), llm.ChatRequest{})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if resp != nil {
		t.Errorf("resp = %+v, want nil", resp)
	}
}

func TestChatFunc(t *testing.T) {
	m := New()
	m.SetError(errors.New("ignored when ChatFunc set"))
	m.ChatFunc = func(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
		return &llm.ChatResponse{Content: "custom:" + req.Messages[0].Content}, nil
	}
	resp, err := m.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "x"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "custom:x" {
		t.Errorf("Content = %q, want %q", resp.Content, "custom:x")
	}
}

func TestCallCountLastRequestReset(t *testing.T) {
	m := New()
	if m.LastRequest() != nil {
		t.Fatal("LastRequest before any call should be nil")
	}
	if m.CallCount() != 0 {
		t.Fatalf("CallCount = %d, want 0", m.CallCount())
	}
	for i := 1; i <= 2; i++ {
		if _, err := m.Chat(context.Background(), llm.ChatRequest{MaxTokens: i}); err != nil {
			t.Fatalf("Chat: %v", err)
		}
	}
	if m.CallCount() != 2 {
		t.Errorf("CallCount = %d, want 2", m.CallCount())
	}
	if got := m.LastRequest().MaxTokens; got != 2 {
		t.Errorf("LastRequest().MaxTokens = %d, want 2", got)
	}
	m.Reset()
	if m.CallCount() != 0 {
		t.Errorf("CallCount after Reset = %d, want 0", m.CallCount())
	}
	if m.LastRequest() == nil {
		t.Error("Reset should not clear LastRequest (old behaviour)")
	}
}
