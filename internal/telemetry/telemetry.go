// Package telemetry initialises the process-wide OpenTelemetry tracer
// provider from the repo's telemetry configuration.
//
// It lives in-repo because agentkit v1.2.0 removed its telemetry package:
// the kit now emits spans through otel.Tracer and expects the consumer to
// install a global provider itself. The provider setup below was copied
// verbatim from agentkit v0.2.1 telemetry/provider.go (InitProvider) so that
// endpoint resolution, protocol selection, sampling, resource attributes and
// shutdown behave exactly as before. The legacy event exporter
// (Exporter.LogEvent) was dropped deliberately (migration decision A-G4);
// callers that need a tracer should use otel.Tracer directly.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// Config configures the OpenTelemetry provider.
type Config struct {
	// ServiceName is the name of the service (required).
	ServiceName string

	// ServiceVersion is the version of the service.
	ServiceVersion string

	// Endpoint is the OTLP endpoint (e.g., "localhost:4317").
	// If empty, uses OTEL_EXPORTER_OTLP_ENDPOINT env var.
	Endpoint string

	// Protocol is "grpc" or "http". Default is "grpc".
	Protocol string

	// Insecure disables TLS. Default is false.
	Insecure bool

	// Headers are additional headers to send with requests.
	Headers map[string]string

	// BatchTimeout is the maximum time to wait before sending a batch.
	BatchTimeout time.Duration

	// ExportTimeout is the timeout for exporting spans.
	ExportTimeout time.Duration
}

// Init initialises OpenTelemetry with the given configuration and installs
// the resulting tracer provider and propagator as the process globals.
// The returned shutdown function flushes pending spans and must be called
// when the process is done.
func Init(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	// Resolve endpoint from config or env
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		return nil, fmt.Errorf("telemetry endpoint not configured (set endpoint or OTEL_EXPORTER_OTLP_ENDPOINT)")
	}

	// Strip protocol prefix if present
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	// Resolve service name
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = os.Getenv("OTEL_SERVICE_NAME")
	}
	if serviceName == "" {
		serviceName = "agent"
	}

	// Create resource
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	// Create exporter
	var exporter sdktrace.SpanExporter
	protocol := cfg.Protocol
	if protocol == "" {
		protocol = "grpc"
	}

	switch protocol {
	case "grpc":
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		if cfg.ExportTimeout > 0 {
			opts = append(opts, otlptracegrpc.WithTimeout(cfg.ExportTimeout))
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)

	case "http":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		if cfg.ExportTimeout > 0 {
			opts = append(opts, otlptracehttp.WithTimeout(cfg.ExportTimeout))
		}
		exporter, err = otlptracehttp.New(ctx, opts...)

	default:
		return nil, fmt.Errorf("unknown protocol: %s (use 'grpc' or 'http')", protocol)
	}

	if err != nil {
		return nil, fmt.Errorf("creating exporter: %w", err)
	}

	// Configure batch processor
	batchOpts := []sdktrace.BatchSpanProcessorOption{}
	if cfg.BatchTimeout > 0 {
		batchOpts = append(batchOpts, sdktrace.WithBatchTimeout(cfg.BatchTimeout))
	}

	// Create tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, batchOpts...),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set as global provider
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
