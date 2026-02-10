package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServer_ParseError(t *testing.T) {
	input := "not valid json\n"
	output := &bytes.Buffer{}

	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		return nil, nil
	})

	server := NewServer(strings.NewReader(input), output, handler)
	server.Serve(context.Background())

	var resp Response
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != ParseError {
		t.Errorf("expected ParseError code, got %d", resp.Error.Code)
	}
}

func TestServer_InvalidRequest(t *testing.T) {
	input := `{"jsonrpc":"1.0","method":"test","id":1}` + "\n"
	output := &bytes.Buffer{}

	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		return nil, nil
	})

	server := NewServer(strings.NewReader(input), output, handler)
	server.Serve(context.Background())

	var resp Response
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != InvalidRequest {
		t.Errorf("expected InvalidRequest code, got %d", resp.Error.Code)
	}
}

func TestServer_SuccessfulRequest(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"echo","params":{"msg":"hello"},"id":1}` + "\n"
	output := &bytes.Buffer{}

	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		if method != "echo" {
			t.Errorf("expected method 'echo', got %s", method)
		}
		var p struct{ Msg string }
		json.Unmarshal(params, &p)
		return map[string]string{"echo": p.Msg}, nil
	})

	server := NewServer(strings.NewReader(input), output, handler)
	server.Serve(context.Background())

	var resp Response
	if err := json.Unmarshal(output.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.ID != float64(1) { // JSON numbers are float64
		t.Errorf("expected ID 1, got %v", resp.ID)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if result["echo"] != "hello" {
		t.Errorf("expected echo 'hello', got %v", result["echo"])
	}
}

func TestServer_Notification(t *testing.T) {
	// Notification = no ID, no response
	input := `{"jsonrpc":"2.0","method":"notify","params":{}}` + "\n"
	output := &bytes.Buffer{}

	called := false
	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		called = true
		return nil, nil
	})

	server := NewServer(strings.NewReader(input), output, handler)
	server.Serve(context.Background())

	if !called {
		t.Error("handler was not called")
	}
	if output.Len() != 0 {
		t.Errorf("expected no output for notification, got: %s", output.String())
	}
}

func TestServer_Notify(t *testing.T) {
	output := &bytes.Buffer{}
	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		return nil, nil
	})

	server := NewServer(strings.NewReader(""), output, handler)
	server.Notify("event", map[string]string{"key": "value"})

	var notif Notification
	if err := json.Unmarshal(output.Bytes(), &notif); err != nil {
		t.Fatalf("failed to parse notification: %v", err)
	}

	if notif.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", notif.JSONRPC)
	}
	if notif.Method != "event" {
		t.Errorf("expected method 'event', got %s", notif.Method)
	}
}

func TestServer_MultipleRequests(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"add","params":{"a":1,"b":2},"id":1}
{"jsonrpc":"2.0","method":"add","params":{"a":3,"b":4},"id":2}
`
	output := &bytes.Buffer{}

	handler := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (interface{}, error) {
		var p struct{ A, B int }
		json.Unmarshal(params, &p)
		return p.A + p.B, nil
	})

	server := NewServer(strings.NewReader(input), output, handler)
	server.Serve(context.Background())

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}

	var resp1, resp2 Response
	json.Unmarshal([]byte(lines[0]), &resp1)
	json.Unmarshal([]byte(lines[1]), &resp2)

	if resp1.Result != float64(3) {
		t.Errorf("expected result 3, got %v", resp1.Result)
	}
	if resp2.Result != float64(7) {
		t.Errorf("expected result 7, got %v", resp2.Result)
	}
}
