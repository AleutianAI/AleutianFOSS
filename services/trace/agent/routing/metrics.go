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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// =============================================================================
// Prometheus Metrics for Tool Routing
// =============================================================================

var (
	// routingLatency measures the time taken for tool routing decisions.
	// Labels: model (the router model used), status (success, error, low_confidence)
	routingLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "latency_seconds",
		Help:      "Tool routing decision latency in seconds",
		Buckets:   []float64{0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.5, 1.0},
	}, []string{"model", "status"})

	// routingConfidence tracks the distribution of confidence scores.
	// Labels: model
	routingConfidence = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "confidence",
		Help:      "Distribution of routing confidence scores",
		Buckets:   []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0},
	}, []string{"model"})

	// routingSelections counts tool selections by the router.
	// Labels: model, tool (selected tool name)
	routingSelections = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "selections_total",
		Help:      "Total tool selections by router",
	}, []string{"model", "tool"})

	// routingFallbacks counts when routing falls back to main LLM.
	// Labels: model, reason (error, low_confidence, disabled)
	routingFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "fallbacks_total",
		Help:      "Total routing fallbacks to main LLM",
	}, []string{"model", "reason"})

	// routingErrors counts routing errors by type.
	// Labels: model, error_type (timeout, parse_error, model_unavailable)
	routingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "errors_total",
		Help:      "Total routing errors by type",
	}, []string{"model", "error_type"})

	// modelWarmupDuration measures time to warm up router models.
	// Labels: model
	modelWarmupDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "warmup_duration_seconds",
		Help:      "Time to warm up router model",
		Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
	}, []string{"model"})

	// modelWarmupStatus tracks warmup success/failure.
	// Labels: model, status (success, error)
	modelWarmupStatus = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "warmup_total",
		Help:      "Model warmup attempts by status",
	}, []string{"model", "status"})

	// routerInitStatus tracks router initialization success/failure.
	// Labels: model, status (success, error), reason (if error)
	routerInitStatus = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "routing",
		Name:      "init_total",
		Help:      "Router initialization attempts by status",
	}, []string{"model", "status", "reason"})
)

// =============================================================================
// Param Extraction Metrics (IT-08b)
// =============================================================================

var (
	// paramExtractionLatency measures the time taken for LLM parameter extraction.
	// Labels: model (the extractor model used), status (success, timeout, error, parse_error)
	paramExtractionLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "param_extraction",
		Name:      "latency_seconds",
		Help:      "LLM parameter extraction latency in seconds",
		Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1.0, 2.0},
	}, []string{"model", "status"})

	// paramExtractionTotal counts parameter extraction attempts by status.
	// Labels: model, status (success, timeout, error, parse_error, fallback)
	paramExtractionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "param_extraction",
		Name:      "total",
		Help:      "Total LLM parameter extraction attempts by status",
	}, []string{"model", "status"})
)

// RecordParamExtractionLatency records the latency of a parameter extraction call.
//
// Inputs:
//
//	model - The extractor model name.
//	status - "success", "timeout", "error", or "parse_error".
//	durationSec - Duration in seconds.
func RecordParamExtractionLatency(model, status string, durationSec float64) {
	paramExtractionLatency.WithLabelValues(model, status).Observe(durationSec)
}

// RecordParamExtractionTotal records a parameter extraction attempt.
//
// Inputs:
//
//	model - The extractor model name.
//	status - "success", "timeout", "error", "parse_error", or "fallback".
func RecordParamExtractionTotal(model, status string) {
	paramExtractionTotal.WithLabelValues(model, status).Inc()
}

// =============================================================================
// Metrics Recording Functions
// =============================================================================

// RecordRoutingLatency records the latency of a routing decision.
//
// Inputs:
//
//	model - The router model name.
//	status - "success", "error", or "low_confidence".
//	durationSec - Duration in seconds.
func RecordRoutingLatency(model, status string, durationSec float64) {
	routingLatency.WithLabelValues(model, status).Observe(durationSec)
}

// RecordRoutingConfidence records a confidence score.
//
// Inputs:
//
//	model - The router model name.
//	confidence - The confidence score (0.0-1.0).
func RecordRoutingConfidence(model string, confidence float64) {
	routingConfidence.WithLabelValues(model).Observe(confidence)
}

// RecordRoutingSelection records a successful tool selection.
//
// Inputs:
//
//	model - The router model name.
//	tool - The selected tool name.
func RecordRoutingSelection(model, tool string) {
	routingSelections.WithLabelValues(model, tool).Inc()
}

// RecordRoutingFallback records a fallback to main LLM.
//
// Inputs:
//
//	model - The router model name (empty if router disabled).
//	reason - "error", "low_confidence", or "disabled".
func RecordRoutingFallback(model, reason string) {
	routingFallbacks.WithLabelValues(model, reason).Inc()
}

// RecordRoutingError records a routing error.
//
// Inputs:
//
//	model - The router model name.
//	errorType - Error type (e.g., "timeout", "parse_error").
func RecordRoutingError(model, errorType string) {
	routingErrors.WithLabelValues(model, errorType).Inc()
}

// RecordModelWarmup records model warmup metrics.
//
// Inputs:
//
//	model - The model name.
//	durationSec - Warmup duration in seconds.
//	success - Whether warmup succeeded.
func RecordModelWarmup(model string, durationSec float64, success bool) {
	modelWarmupDuration.WithLabelValues(model).Observe(durationSec)
	status := "success"
	if !success {
		status = "error"
	}
	modelWarmupStatus.WithLabelValues(model, status).Inc()
}

// RecordRouterInit records router initialization metrics.
//
// # Description
//
// Tracks router initialization attempts, successes, and failures.
// Use this to monitor router health and identify initialization issues.
//
// # Inputs
//
//	model - The router model name.
//	success - Whether initialization succeeded.
//	reason - Failure reason if !success (e.g., "model_manager_nil", "warmup_failed").
//	         Empty string if success=true.
//
// # Thread Safety
//
// Safe for concurrent use.
func RecordRouterInit(model string, success bool, reason string) {
	status := "success"
	if !success {
		status = "error"
	}
	if success {
		reason = "" // No reason on success
	}
	routerInitStatus.WithLabelValues(model, status, reason).Inc()
}

// =============================================================================
// UCB1 Search Activity Metrics (CRS-05)
// =============================================================================

var (
	// ucb1SelectionScore tracks the distribution of UCB1 final scores.
	// Labels: tool (selected tool name)
	ucb1SelectionScore = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "selection_score",
		Help:      "Distribution of UCB1 final scores for selected tools",
		Buckets:   []float64{-1, 0, 0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0},
	}, []string{"tool"})

	// ucb1ProofPenalty tracks proof penalty values.
	// Labels: tool
	ucb1ProofPenalty = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "proof_penalty",
		Help:      "Distribution of proof penalties applied to tools",
		Buckets:   []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	}, []string{"tool"})

	// ucb1ExplorationBonus tracks exploration bonus values.
	// Labels: tool
	ucb1ExplorationBonus = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "exploration_bonus",
		Help:      "Distribution of exploration bonuses for tools",
		Buckets:   []float64{0, 0.5, 1.0, 1.5, 2.0, 2.5, 3.0, 4.0, 5.0},
	}, []string{"tool"})

	// ucb1BlockedSelections counts tool selections blocked by clauses.
	// Labels: tool, reason_type (clause violation category)
	ucb1BlockedSelections = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "blocked_selections_total",
		Help:      "Total tool selections blocked by learned clauses",
	}, []string{"tool", "reason_type"})

	// ucb1ForcedMoves counts forced moves detected by unit propagation.
	// Labels: tool
	ucb1ForcedMoves = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "forced_moves_total",
		Help:      "Total forced moves detected via unit propagation",
	}, []string{"tool"})

	// ucb1CacheHits counts tool selection cache hits.
	ucb1CacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "cache_hits_total",
		Help:      "Total tool selection cache hits",
	})

	// ucb1CacheMisses counts tool selection cache misses.
	ucb1CacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "cache_misses_total",
		Help:      "Total tool selection cache misses",
	})

	// ucb1CacheInvalidations counts cache invalidations.
	// Labels: reason (ttl_expired, generation_changed, evicted)
	ucb1CacheInvalidations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "cache_invalidations_total",
		Help:      "Total cache invalidations by reason",
	}, []string{"reason"})

	// ucb1ScoringLatency measures UCB1 scoring duration.
	ucb1ScoringLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "scoring_latency_seconds",
		Help:      "UCB1 scoring latency in seconds",
		Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
	})

	// ucb1AllBlockedTotal counts when all tools are blocked.
	ucb1AllBlockedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "ucb1",
		Name:      "all_blocked_total",
		Help:      "Total times all tools were blocked by clauses",
	})
)

// RecordUCB1Selection records a UCB1 tool selection.
//
// Inputs:
//
//	tool - The selected tool name.
//	score - The UCB1 final score.
//	proofPenalty - The proof penalty applied.
//	explorationBonus - The exploration bonus.
func RecordUCB1Selection(tool string, score, proofPenalty, explorationBonus float64) {
	ucb1SelectionScore.WithLabelValues(tool).Observe(score)
	ucb1ProofPenalty.WithLabelValues(tool).Observe(proofPenalty)
	ucb1ExplorationBonus.WithLabelValues(tool).Observe(explorationBonus)
}

// RecordUCB1BlockedSelection records a tool blocked by clause.
//
// Inputs:
//
//	tool - The blocked tool name.
//	reasonType - Category of the blocking reason.
func RecordUCB1BlockedSelection(tool, reasonType string) {
	ucb1BlockedSelections.WithLabelValues(tool, reasonType).Inc()
}

// RecordUCB1ForcedMove records a forced move.
//
// Inputs:
//
//	tool - The forced tool name.
func RecordUCB1ForcedMove(tool string) {
	ucb1ForcedMoves.WithLabelValues(tool).Inc()
}

// RecordUCB1CacheHit records a cache hit.
func RecordUCB1CacheHit() {
	ucb1CacheHits.Inc()
}

// RecordUCB1CacheMiss records a cache miss.
func RecordUCB1CacheMiss() {
	ucb1CacheMisses.Inc()
}

// RecordUCB1CacheInvalidation records a cache invalidation.
//
// Inputs:
//
//	reason - "ttl_expired", "generation_changed", or "evicted".
func RecordUCB1CacheInvalidation(reason string) {
	ucb1CacheInvalidations.WithLabelValues(reason).Inc()
}

// RecordUCB1ScoringLatency records UCB1 scoring duration.
//
// Inputs:
//
//	durationSec - Duration in seconds.
func RecordUCB1ScoringLatency(durationSec float64) {
	ucb1ScoringLatency.Observe(durationSec)
}

// RecordUCB1AllBlocked records when all tools are blocked.
func RecordUCB1AllBlocked() {
	ucb1AllBlockedTotal.Inc()
}
