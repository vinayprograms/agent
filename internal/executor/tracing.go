package executor

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tracer is a no-op until cmd/agent installs a global tracer provider
// (internal/telemetry.Init).
var tracer = otel.Tracer("github.com/vinayprograms/agent/internal/executor")

// startWorkflowSpan starts a span for the workflow execution.
func (e *Executor) startWorkflowSpan(ctx context.Context, workflowName string) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, "workflow.run")
	span.SetAttributes(
		attribute.String("workflow.name", workflowName),
	)
	return ctx, span
}

// endWorkflowSpan ends the workflow span with result info.
func (e *Executor) endWorkflowSpan(span trace.Span, status string, err error) {
	span.SetAttributes(attribute.String("workflow.status", status))
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}

// startGoalSpan starts a span for a goal execution.
func (e *Executor) startGoalSpan(ctx context.Context, goalName string, supervised bool) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, "goal."+goalName)
	span.SetAttributes(
		attribute.String("goal.name", goalName),
		attribute.Bool("goal.supervised", supervised),
	)
	return ctx, span
}

// endGoalSpan ends the goal span with output info.
func (e *Executor) endGoalSpan(span trace.Span, output string, err error) {
	if e.debug && output != "" {
		span.SetAttributes(attribute.String("goal.output", truncateForLog(output, 2000)))
	}
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}

// startPhaseSpan starts a span for a supervision phase.
func (e *Executor) startPhaseSpan(ctx context.Context, phase, goalName string) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, "phase."+phase)
	span.SetAttributes(
		attribute.String("phase.name", phase),
		attribute.String("phase.goal", goalName),
	)
	return ctx, span
}

// endPhaseSpan ends the phase span.
func (e *Executor) endPhaseSpan(span trace.Span, attrs map[string]string, err error) {
	for k, v := range attrs {
		span.SetAttributes(attribute.String(k, v))
	}
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}

// startSubAgentSpan starts a span for a sub-agent execution.
func (e *Executor) startSubAgentSpan(ctx context.Context, role, model string) (context.Context, trace.Span) {
	ctx, span := tracer.Start(ctx, "subagent."+role)
	span.SetAttributes(
		attribute.String("subagent.role", role),
		attribute.String("subagent.model", model),
	)
	return ctx, span
}

// endSubAgentSpan ends the sub-agent span with output info.
func (e *Executor) endSubAgentSpan(span trace.Span, output string, err error) {
	if e.debug && output != "" {
		span.SetAttributes(attribute.String("subagent.output", truncateForLog(output, 2000)))
	}
	if err != nil {
		span.RecordError(err)
	}
	span.End()
}
