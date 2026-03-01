// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package mcts

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const mctsTracerName = "trace.mcts"

// MCTSTracer provides OpenTelemetry tracing for MCTS operations.
//
// Thread Safety: Safe for concurrent use.
type MCTSTracer struct {
	tracer  trace.Tracer
	logger  *slog.Logger
	enabled bool
}

// NewMCTSTracer creates a new tracer.
//
// Inputs:
//   - logger: Logger for structured logging (can be nil for no logging).
//   - config: Observability configuration.
//
// Outputs:
//   - *MCTSTracer: Tracer instance.
func NewMCTSTracer(logger *slog.Logger, config ObservabilityConfig) *MCTSTracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &MCTSTracer{
		tracer:  otel.Tracer(mctsTracerName),
		logger:  logger,
		enabled: config.TracingEnabled,
	}
}

// StartMCTSRun starts a span for the entire MCTS run.
//
// Inputs:
//   - ctx: Parent context.
//   - task: Task description.
//   - budget: Budget configuration.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span (nil if tracing disabled).
func (t *MCTSTracer) StartMCTSRun(ctx context.Context, task string, budget *TreeBudget) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	config := budget.Config()
	ctx, span := t.tracer.Start(ctx, "mcts.run",
		trace.WithAttributes(
			attribute.String("mcts.task", truncateForObs(task, 100)),
			attribute.Int("mcts.budget.max_nodes", config.MaxNodes),
			attribute.Int("mcts.budget.max_calls", config.LLMCallLimit),
			attribute.Float64("mcts.budget.max_cost_usd", config.CostLimitUSD),
			attribute.String("mcts.budget.time_limit", config.TimeLimit.String()),
		),
		trace.WithSpanKind(trace.SpanKindInternal),
	)

	t.logger.InfoContext(ctx, "MCTS run started",
		slog.String("task", truncateForObs(task, 100)),
		slog.Int("budget_nodes", config.MaxNodes),
		slog.Int("budget_calls", config.LLMCallLimit),
		slog.Float64("budget_cost_usd", config.CostLimitUSD),
	)

	return ctx, span
}

// EndMCTSRun completes the MCTS run span.
//
// Inputs:
//   - span: The span to end.
//   - tree: The resulting plan tree (can be nil).
//   - budget: Budget tracker with usage.
//   - err: Error if run failed.
func (t *MCTSTracer) EndMCTSRun(span trace.Span, tree *PlanTree, budget *TreeBudget, err error) {
	if span == nil {
		return
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.SetAttributes(
		attribute.Int64("mcts.result.nodes_used", budget.NodesExplored()),
		attribute.Int64("mcts.result.calls_used", budget.LLMCalls()),
		attribute.Float64("mcts.result.cost_used_usd", budget.CostUSD()),
		attribute.String("mcts.result.elapsed", budget.Elapsed().String()),
	)

	if tree != nil {
		span.SetAttributes(
			attribute.Int64("mcts.result.total_nodes", tree.TotalNodes()),
			attribute.Int("mcts.result.max_depth", tree.MaxDepth()),
			attribute.Float64("mcts.result.best_score", tree.BestScore()),
		)
	}

	span.End()

	t.logger.Info("MCTS run completed",
		slog.Int64("nodes_used", budget.NodesExplored()),
		slog.Int64("calls_used", budget.LLMCalls()),
		slog.Float64("cost_usd", budget.CostUSD()),
		slog.Duration("elapsed", budget.Elapsed()),
	)
}

// TraceIteration traces a single MCTS iteration.
//
// Inputs:
//   - ctx: Parent context.
//   - iteration: Iteration number.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span.
func (t *MCTSTracer) TraceIteration(ctx context.Context, iteration int) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	return t.tracer.Start(ctx, "mcts.iteration",
		trace.WithAttributes(
			attribute.Int("mcts.iteration", iteration),
		),
	)
}

// TraceSelect traces the selection phase.
//
// Inputs:
//   - ctx: Parent context.
//   - selectedNode: The node selected for expansion.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span.
func (t *MCTSTracer) TraceSelect(ctx context.Context, selectedNode *PlanNode) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	ctx, span := t.tracer.Start(ctx, "mcts.select",
		trace.WithAttributes(
			attribute.String("mcts.selected_node_id", selectedNode.ID),
			attribute.Int64("mcts.selected_node_visits", selectedNode.Visits()),
			attribute.Float64("mcts.selected_node_score", selectedNode.AvgScore()),
			attribute.String("mcts.selected_node_state", selectedNode.State().String()),
		),
	)

	t.logger.DebugContext(ctx, "MCTS select",
		slog.String("node_id", selectedNode.ID),
		slog.Int64("visits", selectedNode.Visits()),
		slog.Float64("avg_score", selectedNode.AvgScore()),
	)

	return ctx, span
}

// TraceExpand traces the expansion phase.
//
// Inputs:
//   - ctx: Parent context.
//   - parentNode: The node being expanded.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span.
func (t *MCTSTracer) TraceExpand(ctx context.Context, parentNode *PlanNode) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	return t.tracer.Start(ctx, "mcts.expand",
		trace.WithAttributes(
			attribute.String("mcts.parent_node_id", parentNode.ID),
			attribute.Int("mcts.parent_children_count", len(parentNode.Children())),
		),
	)
}

// EndExpand completes the expansion span.
//
// Inputs:
//   - span: The span to end.
//   - children: The created child nodes.
//   - tokens: Tokens used.
//   - cost: Cost in USD.
//   - err: Error if expansion failed.
func (t *MCTSTracer) EndExpand(span trace.Span, children []*PlanNode, tokens int, cost float64, err error) {
	if span == nil {
		return
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.SetAttributes(
		attribute.Int("mcts.expand.children_count", len(children)),
		attribute.Int("mcts.expand.tokens_used", tokens),
		attribute.Float64("mcts.expand.cost_usd", cost),
	)

	span.End()

	t.logger.Debug("MCTS expand completed",
		slog.Int("children", len(children)),
		slog.Int("tokens", tokens),
		slog.Float64("cost_usd", cost),
	)
}

// TraceSimulate traces the simulation phase.
//
// Inputs:
//   - ctx: Parent context.
//   - node: The node being simulated.
//   - tier: Simulation tier.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span.
func (t *MCTSTracer) TraceSimulate(ctx context.Context, node *PlanNode, tier SimulationTier) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	return t.tracer.Start(ctx, "mcts.simulate",
		trace.WithAttributes(
			attribute.String("mcts.node_id", node.ID),
			attribute.String("mcts.simulation_tier", tier.String()),
		),
	)
}

// EndSimulate completes the simulation span.
//
// Inputs:
//   - span: The span to end.
//   - result: Simulation result.
//   - err: Error if simulation failed.
func (t *MCTSTracer) EndSimulate(span trace.Span, result *SimulationResult, err error) {
	if span == nil {
		return
	}

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	if result != nil {
		span.SetAttributes(
			attribute.Float64("mcts.simulate.score", result.Score),
			attribute.String("mcts.simulate.tier", result.Tier),
			attribute.Bool("mcts.simulate.promote_next", result.PromoteToNext),
			attribute.Int("mcts.simulate.error_count", len(result.Errors)),
			attribute.Int("mcts.simulate.warning_count", len(result.Warnings)),
			attribute.String("mcts.simulate.duration", result.Duration.String()),
		)

		// Add signal details as events
		for signal, value := range result.Signals {
			span.AddEvent("simulation_signal",
				trace.WithAttributes(
					attribute.String("signal", signal),
					attribute.Float64("value", value),
				),
			)
		}
	}

	span.End()

	if result != nil {
		t.logger.Debug("MCTS simulate completed",
			slog.Float64("score", result.Score),
			slog.String("tier", result.Tier),
			slog.Duration("duration", result.Duration),
		)
	}
}

// TraceBackpropagate traces the backpropagation phase.
//
// Inputs:
//   - ctx: Parent context.
//   - node: The starting node for backpropagation.
//   - score: The score to propagate.
//
// Outputs:
//   - context.Context: Context with span.
//   - trace.Span: The created span.
func (t *MCTSTracer) TraceBackpropagate(ctx context.Context, node *PlanNode, score float64) (context.Context, trace.Span) {
	if !t.enabled {
		return ctx, noop.Span{}
	}

	return t.tracer.Start(ctx, "mcts.backpropagate",
		trace.WithAttributes(
			attribute.String("mcts.node_id", node.ID),
			attribute.Float64("mcts.backprop.score", score),
		),
	)
}

// EndBackpropagate completes the backpropagation span.
//
// Inputs:
//   - span: The span to end.
//   - nodesUpdated: Number of nodes updated during backpropagation.
func (t *MCTSTracer) EndBackpropagate(span trace.Span, nodesUpdated int) {
	if span == nil {
		return
	}

	span.SetAttributes(
		attribute.Int("mcts.backprop.nodes_updated", nodesUpdated),
	)
	span.End()
}

// TraceNodeAbandon records a node abandonment event.
//
// Inputs:
//   - ctx: Context with span.
//   - node: The abandoned node.
//   - reason: Reason for abandonment.
func (t *MCTSTracer) TraceNodeAbandon(ctx context.Context, node *PlanNode, reason string) {
	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}

	span.AddEvent("node_abandoned",
		trace.WithAttributes(
			attribute.String("node_id", node.ID),
			attribute.String("reason", reason),
			attribute.Float64("score", node.AvgScore()),
			attribute.Int64("visits", node.Visits()),
		),
	)

	t.logger.Info("MCTS node abandoned",
		slog.String("node_id", node.ID),
		slog.String("reason", reason),
		slog.Float64("score", node.AvgScore()),
	)
}

// TraceDegradation records a degradation event.
//
// Inputs:
//   - ctx: Context with span.
//   - from: Previous degradation level.
//   - to: New degradation level.
//   - reason: Reason for degradation.
func (t *MCTSTracer) TraceDegradation(ctx context.Context, from, to TreeDegradation, reason string) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.AddEvent("degradation",
			trace.WithAttributes(
				attribute.String("from", from.String()),
				attribute.String("to", to.String()),
				attribute.String("reason", reason),
			),
		)
	}

	t.logger.Warn("MCTS degradation",
		slog.String("from", from.String()),
		slog.String("to", to.String()),
		slog.String("reason", reason),
	)
}

// TraceCircuitBreakerStateChange records circuit breaker state changes.
//
// Inputs:
//   - ctx: Context with span.
//   - from: Previous circuit state.
//   - to: New circuit state.
func (t *MCTSTracer) TraceCircuitBreakerStateChange(ctx context.Context, from, to CircuitState) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.AddEvent("circuit_breaker_state_change",
			trace.WithAttributes(
				attribute.String("from", from.String()),
				attribute.String("to", to.String()),
			),
		)
	}

	t.logger.Info("Circuit breaker state change",
		slog.String("from", from.String()),
		slog.String("to", to.String()),
	)
}

// TraceBudgetExhaustion records budget exhaustion.
//
// Inputs:
//   - ctx: Context with span.
//   - reason: The budget limit that was exceeded.
//   - budget: Budget tracker with current usage.
func (t *MCTSTracer) TraceBudgetExhaustion(ctx context.Context, reason string, budget *TreeBudget) {
	span := trace.SpanFromContext(ctx)
	if span != nil {
		span.AddEvent("budget_exhausted",
			trace.WithAttributes(
				attribute.String("reason", reason),
				attribute.Int64("nodes_used", budget.NodesExplored()),
				attribute.Int64("calls_used", budget.LLMCalls()),
				attribute.Float64("cost_used_usd", budget.CostUSD()),
			),
		)
	}

	t.logger.Info("MCTS budget exhausted",
		slog.String("reason", reason),
		slog.Int64("nodes_used", budget.NodesExplored()),
		slog.Int64("calls_used", budget.LLMCalls()),
		slog.Float64("cost_usd", budget.CostUSD()),
	)
}

// truncateForObs truncates a string for use in span attributes.
func truncateForObs(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// LoggerWithTrace returns a logger with trace context.
//
// Inputs:
//   - ctx: Context that may contain trace information.
//   - logger: Base logger.
//
// Outputs:
//   - *slog.Logger: Logger with trace_id and span_id if available.
func LoggerWithTrace(ctx context.Context, logger *slog.Logger) *slog.Logger {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return logger
	}
	return logger.With(
		slog.String("trace_id", spanCtx.TraceID().String()),
		slog.String("span_id", spanCtx.SpanID().String()),
	)
}
