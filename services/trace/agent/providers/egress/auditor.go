// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// EgressAuditor produces structured audit log entries for egress events (P-5/P-6).
//
// Description:
//
//	Logs egress decisions using slog structured logging. Each log entry
//	includes request_id, session_id, trace_id, provider, model, sensitivity,
//	and a SHA256 content hash (if enabled). This provides a compliance-ready
//	audit trail without storing actual message content.
//
// Thread Safety: Safe for concurrent use (slog.Logger is concurrent-safe).
type EgressAuditor struct {
	logger      *slog.Logger
	enabled     bool
	hashContent bool
}

// NewEgressAuditor creates a new auditor.
//
// Inputs:
//   - logger: The structured logger for audit output.
//   - enabled: Whether audit logging is active.
//   - hashContent: Whether to include SHA256 content hashes in log entries.
//
// Outputs:
//   - *EgressAuditor: Configured auditor.
func NewEgressAuditor(logger *slog.Logger, enabled, hashContent bool) *EgressAuditor {
	return &EgressAuditor{
		logger:      logger,
		enabled:     enabled,
		hashContent: hashContent,
	}
}

// LogBefore logs a pre-flight audit entry before making an API call.
//
// Inputs:
//   - ctx: Context containing trace information.
//   - decision: The egress decision record.
func (a *EgressAuditor) LogBefore(ctx context.Context, decision *EgressDecision) {
	if !a.enabled || decision == nil {
		return
	}

	logger := a.loggerWithTrace(ctx)

	attrs := []any{
		slog.String("event", "egress_before"),
		slog.String("request_id", decision.RequestID),
		slog.String("session_id", decision.SessionID),
		slog.String("provider", decision.Provider),
		slog.String("model", decision.Model),
		slog.String("sensitivity", decision.Sensitivity.String()),
		slog.Int("estimated_tokens", decision.EstimatedTokens),
		slog.Float64("estimated_cost_cents", decision.EstimatedCostCents),
		slog.Int64("timestamp", decision.Timestamp),
	}

	if a.hashContent && decision.ContentHash != "" {
		attrs = append(attrs, slog.String("content_hash", decision.ContentHash))
	}

	logger.Info("egress request", attrs...)
}

// LogAfter logs a post-call audit entry after an API call completes.
//
// Inputs:
//   - ctx: Context containing trace information.
//   - requestID: The request ID from the pre-flight decision.
//   - provider: The provider name.
//   - model: The model name.
//   - inputTokens: Actual input tokens used.
//   - outputTokens: Actual output tokens used.
//   - durationMs: Call duration in milliseconds.
//   - costCents: Actual cost in US cents.
//   - callErr: Error from the API call (nil on success).
func (a *EgressAuditor) LogAfter(
	ctx context.Context,
	requestID, provider, model string,
	inputTokens, outputTokens int,
	durationMs int64,
	costCents float64,
	callErr error,
) {
	if !a.enabled {
		return
	}

	logger := a.loggerWithTrace(ctx)

	status := "success"
	if callErr != nil {
		status = "error"
	}

	attrs := []any{
		slog.String("event", "egress_after"),
		slog.String("request_id", requestID),
		slog.String("provider", provider),
		slog.String("model", model),
		slog.String("status", status),
		slog.Int("input_tokens", inputTokens),
		slog.Int("output_tokens", outputTokens),
		slog.Int64("duration_ms", durationMs),
		slog.Float64("cost_cents", costCents),
		slog.Int64("timestamp", time.Now().UnixMilli()),
	}

	if callErr != nil {
		attrs = append(attrs, slog.String("error", callErr.Error()))
	}

	logger.Info("egress response", attrs...)
}

// LogBlocked logs an audit entry for a blocked egress attempt.
//
// Inputs:
//   - ctx: Context containing trace information.
//   - decision: The egress decision record (must have BlockedBy and BlockReason set).
func (a *EgressAuditor) LogBlocked(ctx context.Context, decision *EgressDecision) {
	if !a.enabled || decision == nil {
		return
	}

	logger := a.loggerWithTrace(ctx)

	logger.Warn("egress blocked",
		slog.String("event", "egress_blocked"),
		slog.String("request_id", decision.RequestID),
		slog.String("session_id", decision.SessionID),
		slog.String("provider", decision.Provider),
		slog.String("model", decision.Model),
		slog.String("blocked_by", decision.BlockedBy),
		slog.String("reason", decision.BlockReason),
		slog.Int64("timestamp", decision.Timestamp),
		slog.Int64("duration_ms", decision.DurationMs),
	)
}

// LogMinimization logs an audit entry for request minimization.
//
// Description:
//
//	Records the token reduction achieved by the DataMinimizer, including
//	per-stage deltas (system prompt, tool defs, messages) and counts of
//	truncated results and dropped messages.
//
// Inputs:
//   - ctx: Context containing trace information.
//   - requestID: The request ID from the pre-flight decision.
//   - stats: Minimization statistics from DataMinimizer.Minimize().
func (a *EgressAuditor) LogMinimization(ctx context.Context, requestID string, stats MinimizationStats) {
	if !a.enabled {
		return
	}

	// Skip logging if no minimization occurred
	if stats.OriginalTokens == 0 || stats.OriginalTokens == stats.MinimizedTokens {
		return
	}

	logger := a.loggerWithTrace(ctx)

	logger.Info("egress minimization",
		slog.String("event", "egress_minimization"),
		slog.String("request_id", requestID),
		slog.Int("original_tokens", stats.OriginalTokens),
		slog.Int("minimized_tokens", stats.MinimizedTokens),
		slog.Float64("reduction_pct", stats.Reduction()),
		slog.Int("system_prompt_delta", stats.SystemPromptDelta),
		slog.Int("tool_defs_delta", stats.ToolDefsDelta),
		slog.Int("messages_delta", stats.MessagesDelta),
		slog.Int("truncated_results", stats.TruncatedResults),
		slog.Int("dropped_messages", stats.DroppedMessages),
		slog.Int64("timestamp", time.Now().UnixMilli()),
	)
}

// loggerWithTrace returns a logger enriched with trace context.
func (a *EgressAuditor) loggerWithTrace(ctx context.Context) *slog.Logger {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return a.logger
	}
	return a.logger.With(
		slog.String("trace_id", spanCtx.TraceID().String()),
		slog.String("span_id", spanCtx.SpanID().String()),
	)
}

// HashContent computes the SHA256 hex digest of content for audit purposes.
//
// Description:
//
//	Produces a deterministic hash of the request content for compliance
//	verification without storing the actual content. Returns empty string
//	for nil/empty input.
//
// Inputs:
//   - content: The raw content to hash.
//
// Outputs:
//   - string: The hex-encoded SHA256 digest, or empty string for empty input.
func HashContent(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
