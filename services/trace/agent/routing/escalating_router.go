// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// =============================================================================
// Prometheus Metrics (CB-62)
// =============================================================================

var (
	routerEscalationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "router",
		Name:      "escalation_total",
		Help:      "Escalation events by outcome: success, timeout, error, skipped",
	}, []string{"outcome"})

	routerEscalationLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "router",
		Name:      "escalation_latency_seconds",
		Help:      "Latency of escalation model calls",
		Buckets:   []float64{0.1, 0.5, 1.0, 2.0, 3.0, 5.0},
	})

	// CB-62 Rev 2: Prefilter miss recovery metrics.
	routerPrefilterMissTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "router",
		Name:      "prefilter_miss_total",
		Help:      "Prefilter miss recovery outcomes: direct_use, escalated, no_escalation, hallucination",
	}, []string{"outcome"})
)

// =============================================================================
// OTel Tracer
// =============================================================================

var escalatingRouterTracer = otel.Tracer("aleutian.agent.routing.escalating")

// =============================================================================
// EscalatingRouter
// =============================================================================

// EscalatingRouter wraps a primary ToolRouter and escalates to a larger model
// when the primary router's confidence is below a threshold.
//
// Description:
//
//	Delegates to the primary router first. If the primary returns a selection
//	with confidence below the threshold AND an escalation router is configured,
//	re-routes the query through the escalation router with the full tool set
//	(bypassing the prefilter). If escalation fails, falls back to the primary
//	result (best effort).
//
// Inputs:
//
//	primary   - The fast primary router (e.g., granite4:micro-h). Must not be nil.
//	escalation - The larger escalation router. Nil disables escalation.
//	threshold  - Minimum primary confidence to skip escalation. Default: 0.7.
//	allSpecs   - Full tool set for escalation (bypasses prefilter).
//	timeout    - Maximum time for escalation call. Default: 3s.
//	logger     - Logger instance. Must not be nil.
//
// Thread Safety: Safe for concurrent use (delegates to thread-safe routers).
type EscalatingRouter struct {
	primary           ToolRouter
	escalation        ToolRouter
	threshold         float64
	allSpecs          []ToolSpec
	escalationTimeout time.Duration
	logger            *slog.Logger
}

// NewEscalatingRouter creates a new EscalatingRouter.
//
// Description:
//
//	Creates a router that delegates to primary first, then escalates to
//	a larger model when confidence is low. If escalation is nil, behaves
//	identically to the primary router (zero overhead).
//
// Inputs:
//
//	primary    - The fast primary router. Must not be nil.
//	escalation - The larger escalation router. Nil disables escalation.
//	threshold  - Minimum primary confidence to skip escalation.
//	allSpecs   - Full tool set for escalation calls.
//	timeout    - Maximum time for escalation. Zero uses default (3s).
//	logger     - Logger instance. Must not be nil.
//
// Outputs:
//
//	*EscalatingRouter - The constructed router. Never nil.
//
// Limitations:
//
//	allSpecs must be set at construction time. If the tool set changes,
//	a new EscalatingRouter must be created.
//
// Assumptions:
//
//	Both primary and escalation routers are already warmed.
func NewEscalatingRouter(primary ToolRouter, escalation ToolRouter, threshold float64, allSpecs []ToolSpec, timeout time.Duration, logger *slog.Logger) *EscalatingRouter {
	if logger == nil {
		logger = slog.Default()
	}
	if threshold <= 0 {
		threshold = 0.7
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &EscalatingRouter{
		primary:           primary,
		escalation:        escalation,
		threshold:         threshold,
		allSpecs:          allSpecs,
		escalationTimeout: timeout,
		logger:            logger,
	}
}

// SelectTool chooses the best tool, escalating to a larger model if needed.
//
// Description:
//
//  1. Calls the primary router with the prefiltered candidate set.
//  2. If the primary's confidence >= threshold, returns immediately.
//  3. If confidence is low and escalation is configured, calls the escalation
//     router with the FULL tool set (all 55 tools).
//  4. If escalation fails or times out, returns the primary result (best effort).
//
// Inputs:
//
//	ctx            - Context for cancellation/timeout. Must not be nil.
//	query          - The user's question or request.
//	availableTools - Prefiltered tools for the primary router.
//	codeCtx        - Optional code context (symbols, files loaded, etc.)
//
// Outputs:
//
//	*ToolSelection - The selected tool and confidence.
//	error          - Non-nil only if the primary router fails.
//
// Thread Safety: Safe for concurrent use.
func (r *EscalatingRouter) SelectTool(ctx context.Context, query string, availableTools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
	ctx, span := escalatingRouterTracer.Start(ctx, "routing.EscalatingRouter.SelectTool",
		trace.WithAttributes(
			attribute.String("query_preview", truncateForLog(query, 80)),
			attribute.Int("available_tools", len(availableTools)),
			attribute.Bool("escalation_configured", r.escalation != nil),
		),
	)
	defer span.End()

	// 1. Call primary router.
	sel, err := r.primary.SelectTool(ctx, query, availableTools, codeCtx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "primary router failed")
		return nil, err
	}

	span.SetAttributes(
		attribute.String("primary_tool", sel.Tool),
		attribute.Float64("primary_confidence", sel.Confidence),
	)

	// 2. CB-62 Rev 2: Handle prefilter miss.
	if sel.PrefilterMiss {
		return r.handlePrefilterMiss(ctx, query, sel, codeCtx)
	}

	// 3. If confident or no escalation configured, return primary result.
	if r.escalation == nil || sel.Confidence >= r.threshold {
		routerEscalationTotal.WithLabelValues("skipped").Inc()
		span.SetAttributes(attribute.Bool("escalated", false))
		return sel, nil
	}

	// 4. Low confidence escalation (existing CB-62 behavior).
	return r.escalate(ctx, query, sel, codeCtx)
}

// handlePrefilterMiss recovers when the router picked a tool not in the
// prefiltered candidate set. Two paths:
//
//   - High confidence (>= 0.85): Use the model's pick directly. The router
//     model KNEW the right tool despite prefilter excluding it. Zero overhead.
//   - Low confidence (< 0.85): Escalate to larger model with ALL tools.
//     The prefilter miss + low confidence suggests genuine ambiguity.
//
// Inputs:
//
//	ctx        - Context for cancellation/timeout.
//	query      - The user's query string.
//	primarySel - The primary router's selection with PrefilterMiss=true.
//	codeCtx    - Optional code context.
//
// Outputs:
//
//	*ToolSelection - The recovered selection.
//	error          - Non-nil if recovery fails (hallucinated tool name).
func (r *EscalatingRouter) handlePrefilterMiss(ctx context.Context, query string,
	primarySel *ToolSelection, codeCtx *CodeContext) (*ToolSelection, error) {

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Bool("prefilter_miss", true),
		attribute.String("raw_model_pick", primarySel.RawModelPick),
		attribute.Float64("primary_confidence", primarySel.Confidence),
	)

	// Validate the raw pick is a known tool name (exists in allSpecs).
	if !r.isKnownTool(primarySel.RawModelPick) {
		// Model hallucinated a tool name. This is NOT a prefilter miss.
		r.logger.Warn("router hallucinated tool name, not a prefilter miss",
			slog.String("hallucinated", primarySel.RawModelPick),
		)
		routerPrefilterMissTotal.WithLabelValues("hallucination").Inc()
		return nil, NewRouterError(ErrCodeParseError,
			fmt.Sprintf("model returned unknown tool: %s", primarySel.RawModelPick), false)
	}

	const directUseThreshold = 0.85

	if primarySel.Confidence >= directUseThreshold {
		// High confidence: trust the model directly.
		r.logger.Info("prefilter miss recovered via direct use",
			slog.String("tool", primarySel.RawModelPick),
			slog.Float64("confidence", primarySel.Confidence),
		)
		routerPrefilterMissTotal.WithLabelValues("direct_use").Inc()
		span.SetAttributes(attribute.String("prefilter_miss_outcome", "direct_use"))
		return primarySel, nil
	}

	// Low confidence: escalate with full tool set.
	if r.escalation == nil || len(r.allSpecs) == 0 {
		// No escalation available — use primary pick (best effort).
		r.logger.Warn("prefilter miss with low confidence, no escalation available",
			slog.String("tool", primarySel.RawModelPick),
			slog.Float64("confidence", primarySel.Confidence),
		)
		routerPrefilterMissTotal.WithLabelValues("no_escalation").Inc()
		span.SetAttributes(attribute.String("prefilter_miss_outcome", "no_escalation"))
		return primarySel, nil
	}

	r.logger.Info("prefilter miss with low confidence, escalating",
		slog.String("primary_tool", primarySel.RawModelPick),
		slog.Float64("primary_confidence", primarySel.Confidence),
	)
	routerPrefilterMissTotal.WithLabelValues("escalated").Inc()
	span.SetAttributes(attribute.String("prefilter_miss_outcome", "escalated"))
	return r.escalate(ctx, query, primarySel, codeCtx)
}

// isKnownTool checks if a tool name exists in the full tool set.
//
// Inputs:
//
//	name - Tool name to check.
//
// Outputs:
//
//	bool - True if the tool exists in allSpecs.
func (r *EscalatingRouter) isKnownTool(name string) bool {
	for _, spec := range r.allSpecs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

// escalate calls the escalation router with the full tool set.
//
// Description:
//
//	Shared escalation logic used by both low-confidence escalation and
//	prefilter miss recovery. Calls the escalation router with ALL tools
//	(bypassing the prefilter). If escalation fails or times out, falls
//	back to the primary result.
//
// Inputs:
//
//	ctx        - Context for cancellation/timeout.
//	query      - The user's query string.
//	primarySel - The primary router's selection (returned on escalation failure).
//	codeCtx    - Optional code context.
//
// Outputs:
//
//	*ToolSelection - The escalation result, or primarySel on failure.
//	error          - Always nil (escalation failures degrade to primary).
func (r *EscalatingRouter) escalate(ctx context.Context, query string,
	primarySel *ToolSelection, codeCtx *CodeContext) (*ToolSelection, error) {

	span := trace.SpanFromContext(ctx)

	r.logger.Info("router escalation triggered",
		slog.String("primary_tool", primarySel.Tool),
		slog.Float64("primary_confidence", primarySel.Confidence),
		slog.Float64("threshold", r.threshold),
		slog.Int("full_tool_count", len(r.allSpecs)),
	)

	escStart := time.Now()
	escCtx, cancel := context.WithTimeout(ctx, r.escalationTimeout)
	defer cancel()

	escSel, escErr := r.escalation.SelectTool(escCtx, query, r.allSpecs, codeCtx)
	escDuration := time.Since(escStart)
	routerEscalationLatency.Observe(escDuration.Seconds())

	if escErr != nil {
		// Escalation failed — return primary result (best effort).
		r.logger.Warn("router escalation failed, using primary result",
			slog.String("error", escErr.Error()),
			slog.Duration("duration", escDuration),
		)
		routerEscalationTotal.WithLabelValues("error").Inc()
		span.SetAttributes(
			attribute.Bool("escalated", true),
			attribute.String("escalation_outcome", "error"),
		)
		return primarySel, nil
	}

	r.logger.Info("router escalation succeeded",
		slog.String("escalation_tool", escSel.Tool),
		slog.Float64("escalation_confidence", escSel.Confidence),
		slog.Duration("duration", escDuration),
	)
	routerEscalationTotal.WithLabelValues("success").Inc()
	span.SetAttributes(
		attribute.Bool("escalated", true),
		attribute.String("escalation_outcome", "success"),
		attribute.String("escalation_tool", escSel.Tool),
		attribute.Float64("escalation_confidence", escSel.Confidence),
	)

	return escSel, nil
}

// Model returns the primary router's model name.
//
// Outputs:
//
//	string - The model name of the primary router.
func (r *EscalatingRouter) Model() string {
	return r.primary.Model()
}

// SetAllSpecs sets the full tool set used for escalation calls.
//
// Description:
//
//	This is a post-construction setter for cases where the full tool set
//	is not available at EscalatingRouter creation time (e.g., when tool
//	registration happens after router wiring). Must be called before the
//	first query if escalation is enabled.
//
// Inputs:
//
//	specs - The full tool spec set. Nil disables escalation passthrough.
//
// Thread Safety: NOT safe for concurrent use with SelectTool. Call during init only.
func (r *EscalatingRouter) SetAllSpecs(specs []ToolSpec) {
	r.allSpecs = specs
}

// Close releases resources held by both routers.
//
// Outputs:
//
//	error - Non-nil if either router's Close fails.
func (r *EscalatingRouter) Close() error {
	var firstErr error
	if err := r.primary.Close(); err != nil {
		firstErr = err
	}
	if r.escalation != nil {
		if err := r.escalation.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
