// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package bridge

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Package-level tracer and meter for bridge operations.
var (
	tracer = otel.Tracer("aleutian.mcp.bridge")
	meter  = otel.Meter("aleutian.mcp.bridge")
)

// bridgeMetrics holds the OTel metrics for tool bridge operations.
//
// Description:
//
//	All metrics follow the naming convention mcp_tool_<metric>_<unit>.
//	Initialized lazily via sync.Once on first use.
//
// Thread Safety: Safe for concurrent use after initBridgeMetrics returns.
type bridgeMetrics struct {
	// toolCallsTotal counts the total number of tool calls, labeled by tool and status.
	toolCallsTotal metric.Int64Counter

	// toolCallDuration records tool call latency in seconds, labeled by tool and method.
	toolCallDuration metric.Float64Histogram

	// toolResultBytes records response size in bytes before truncation, labeled by tool.
	toolResultBytes metric.Int64Histogram

	// toolTruncationsTotal counts how many times results were truncated, labeled by tool.
	toolTruncationsTotal metric.Int64Counter

	// toolErrorsTotal counts errors by type (4xx/5xx/connection/timeout), labeled by tool.
	toolErrorsTotal metric.Int64Counter
}

var (
	metrics     *bridgeMetrics
	metricsOnce sync.Once
	metricsErr  error
)

// initBridgeMetrics initializes the bridge metrics. Safe to call multiple times.
//
// Description:
//
//	Registers all five bridge metrics with the aleutian.mcp.bridge meter.
//	Uses sync.Once for thread-safe lazy initialization.
//
// Outputs:
//
//	error - Non-nil if any metric registration fails.
//
// Thread Safety: Safe for concurrent use.
func initBridgeMetrics() error {
	metricsOnce.Do(func() {
		m := &bridgeMetrics{}

		var err error
		m.toolCallsTotal, err = meter.Int64Counter(
			"mcp_tool_calls_total",
			metric.WithDescription("Total number of MCP tool calls"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		m.toolCallDuration, err = meter.Float64Histogram(
			"mcp_tool_call_duration_seconds",
			metric.WithDescription("Duration of MCP tool calls"),
			metric.WithUnit("s"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		m.toolResultBytes, err = meter.Int64Histogram(
			"mcp_tool_result_bytes",
			metric.WithDescription("Size of MCP tool results in bytes before truncation"),
			metric.WithUnit("By"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		m.toolTruncationsTotal, err = meter.Int64Counter(
			"mcp_tool_truncations_total",
			metric.WithDescription("Total number of MCP tool result truncations"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		m.toolErrorsTotal, err = meter.Int64Counter(
			"mcp_tool_errors_total",
			metric.WithDescription("Total number of MCP tool call errors by type"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		metrics = m
	})
	return metricsErr
}

// recordToolCall records a successful or failed tool call in metrics.
//
// Description:
//
//	Increments mcp_tool_calls_total with tool name and status labels.
//	Safe to call even if metrics initialization failed (no-op).
//
// Inputs:
//
//	ctx - Context for metric recording.
//	toolName - The MCP tool name.
//	status - "ok" or "error".
//
// Thread Safety: Safe for concurrent use.
func recordToolCall(ctx context.Context, toolName string, status string) {
	if err := initBridgeMetrics(); err != nil || metrics == nil {
		return
	}
	metrics.toolCallsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tool", toolName),
		attribute.String("status", status),
	))
}

// recordToolDuration records a tool call's latency in metrics.
//
// Description:
//
//	Records mcp_tool_call_duration_seconds with tool name and HTTP method labels.
//
// Inputs:
//
//	ctx - Context for metric recording.
//	toolName - The MCP tool name.
//	method - The HTTP method (GET/POST).
//	durationSeconds - Elapsed time in seconds.
//
// Thread Safety: Safe for concurrent use.
func recordToolDuration(ctx context.Context, toolName string, method string, durationSeconds float64) {
	if err := initBridgeMetrics(); err != nil || metrics == nil {
		return
	}
	metrics.toolCallDuration.Record(ctx, durationSeconds, metric.WithAttributes(
		attribute.String("tool", toolName),
		attribute.String("method", method),
	))
}

// recordToolResultSize records the response size before truncation.
//
// Description:
//
//	Records mcp_tool_result_bytes with tool name label.
//
// Inputs:
//
//	ctx - Context for metric recording.
//	toolName - The MCP tool name.
//	sizeBytes - Response size in bytes.
//
// Thread Safety: Safe for concurrent use.
func recordToolResultSize(ctx context.Context, toolName string, sizeBytes int64) {
	if err := initBridgeMetrics(); err != nil || metrics == nil {
		return
	}
	metrics.toolResultBytes.Record(ctx, sizeBytes, metric.WithAttributes(
		attribute.String("tool", toolName),
	))
}

// recordToolTruncation increments the truncation counter.
//
// Description:
//
//	Increments mcp_tool_truncations_total with tool name label.
//
// Inputs:
//
//	ctx - Context for metric recording.
//	toolName - The MCP tool name.
//
// Thread Safety: Safe for concurrent use.
func recordToolTruncation(ctx context.Context, toolName string) {
	if err := initBridgeMetrics(); err != nil || metrics == nil {
		return
	}
	metrics.toolTruncationsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tool", toolName),
	))
}

// recordToolError increments the error counter by type.
//
// Description:
//
//	Increments mcp_tool_errors_total with tool name and error type labels.
//
// Inputs:
//
//	ctx - Context for metric recording.
//	toolName - The MCP tool name.
//	errorType - Error classification (e.g., "4xx", "5xx", "connection", "timeout").
//
// Thread Safety: Safe for concurrent use.
func recordToolError(ctx context.Context, toolName string, errorType string) {
	if err := initBridgeMetrics(); err != nil || metrics == nil {
		return
	}
	metrics.toolErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tool", toolName),
		attribute.String("error_type", errorType),
	))
}

// startBridgeSpan creates a span for a bridge HTTP request.
//
// Description:
//
//	Creates a client span with the tool name, HTTP method, and URL as attributes.
//
// Inputs:
//
//	ctx - Parent context.
//	toolName - The MCP tool name (used in span name).
//	method - HTTP method (GET/POST).
//	url - The full request URL.
//
// Outputs:
//
//	context.Context - Context with span attached.
//	trace.Span - The created span. Caller must call span.End().
//
// Thread Safety: Safe for concurrent use.
func startBridgeSpan(ctx context.Context, toolName string, method string, url string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "bridge.CallTool",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("mcp.tool", toolName),
			attribute.String("http.method", method),
			attribute.String("http.url", url),
		),
	)
}
