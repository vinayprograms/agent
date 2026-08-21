package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// Init installs process globals; tests restore them so other tests in the
// package are unaffected.
func saveGlobals(t *testing.T) {
	t.Helper()
	tp, pr := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(pr)
	})
}

func TestInitErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{"no endpoint", Config{}, "telemetry endpoint not configured"},
		{"unknown protocol", Config{Endpoint: "localhost:4317", Protocol: "file"}, "unknown protocol: file"},
		{"bad grpc endpoint", Config{Endpoint: "%", Protocol: "grpc"}, "creating exporter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			saveGlobals(t)
			shutdown, err := Init(context.Background(), tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Init() err = %v, want containing %q", err, tt.wantErr)
			}
			if shutdown != nil {
				t.Error("Init() returned non-nil shutdown on error")
			}
		})
	}
}

// TestInitHTTP drives the http protocol end to end against a local OTLP
// receiver: a span emitted through the global provider must reach the
// server with the configured header on shutdown.
func TestInitHTTP(t *testing.T) {
	saveGlobals(t)
	var got atomic.Int32
	var gotHeader atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" && r.Method == http.MethodPost {
			got.Add(1)
			gotHeader.Store(r.Header.Get("X-Test"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	shutdown, err := Init(context.Background(), Config{
		ServiceName:    "agent-test",
		ServiceVersion: "v0.0.0",
		Endpoint:       srv.URL, // http:// prefix must be stripped
		Protocol:       "http",
		Insecure:       true,
		Headers:        map[string]string{"X-Test": "yes"},
		BatchTimeout:   time.Second,
		ExportTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	_, span := otel.Tracer("test").Start(context.Background(), "op")
	span.End()

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error = %v", err)
	}
	if got.Load() == 0 {
		t.Fatal("no spans exported to the http receiver")
	}
	if h, _ := gotHeader.Load().(string); h != "yes" {
		t.Errorf("X-Test header = %q, want %q", h, "yes")
	}

	// Propagator must be installed alongside the provider.
	fields := otel.GetTextMapPropagator().Fields()
	want := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}).Fields()
	slices.Sort(fields)
	slices.Sort(want)
	if !slices.Equal(fields, want) {
		t.Errorf("propagator fields = %v, want %v", fields, want)
	}
}

// TestInitGRPC covers the grpc branch and endpoint/service-name resolution
// from the environment. The gRPC client dials lazily, so no listener is
// needed; shutdown with a cancelled context returns without touching the
// network.
func TestInitGRPC(t *testing.T) {
	saveGlobals(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://localhost:1")
	t.Setenv("OTEL_SERVICE_NAME", "from-env")

	shutdown, err := Init(context.Background(), Config{
		Insecure:      true,
		Headers:       map[string]string{"X-Test": "yes"},
		ExportTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := shutdown(ctx); err == nil {
		t.Error("shutdown with cancelled context: want error, got nil")
	}
}

// TestInitDefaults covers the "grpc" protocol default and the "agent"
// service-name fallback when neither config nor env provides one.
func TestInitDefaults(t *testing.T) {
	saveGlobals(t)
	t.Setenv("OTEL_SERVICE_NAME", "")
	shutdown, err := Init(context.Background(), Config{Endpoint: "localhost:1"})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = shutdown(ctx)
}
