// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package llm

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// llmTracerName is the shared OTel tracer name for all agent LLM adapters.
const llmTracerName = "trace.llm"

// Package-level Prometheus metrics for LLM adapter operations.
// Auto-registered via promauto so no explicit registry wiring is needed.
var (
	// llmCallDuration measures the duration of LLM API calls.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - status: "success" or "error"
	llmCallDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "trace",
			Subsystem: "llm",
			Name:      "call_duration_seconds",
			Help:      "Duration of LLM API calls in seconds.",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
		},
		[]string{"provider", "status"},
	)

	// llmCallsTotal counts the total number of LLM API calls.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - status: "success" or "error"
	llmCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "trace",
			Subsystem: "llm",
			Name:      "calls_total",
			Help:      "Total number of LLM API calls.",
		},
		[]string{"provider", "status"},
	)

	// llmTokensTotal counts the total tokens consumed by LLM calls.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - direction: "input" or "output"
	llmTokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "trace",
			Subsystem: "llm",
			Name:      "tokens_total",
			Help:      "Total tokens consumed by LLM calls.",
		},
		[]string{"provider", "direction"},
	)

	// llmErrorsTotal counts the total LLM errors by type.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - error_type: "timeout", "auth", "rate_limit", "server", "empty_response", "unknown"
	llmErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "trace",
			Subsystem: "llm",
			Name:      "errors_total",
			Help:      "Total LLM errors by type.",
		},
		[]string{"provider", "error_type"},
	)

	// llmActiveRequests tracks the number of in-flight LLM requests.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	llmActiveRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "trace",
			Subsystem: "llm",
			Name:      "active_requests",
			Help:      "Number of currently active LLM requests.",
		},
		[]string{"provider"},
	)
)

// classifyError maps an error to a label-safe error type string.
//
// Description:
//
//	Inspects the error message to categorize it into one of the predefined
//	error types used as Prometheus label values. This avoids high-cardinality
//	labels from raw error messages.
//
// Inputs:
//
//	err - The error to classify. May be nil.
//
// Outputs:
//
//	string - One of: "timeout", "auth", "rate_limit", "server",
//	         "empty_response", "unknown". Returns empty string for nil error.
//
// Thread Safety: Safe for concurrent use.
func classifyError(err error) string {
	if err == nil {
		return ""
	}

	// Check for EmptyResponseError type first
	if _, ok := err.(*EmptyResponseError); ok {
		return "empty_response"
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "context canceled") ||
		strings.Contains(msg, "timeout"):
		return "timeout"
	case strings.Contains(msg, "returned 401") ||
		strings.Contains(msg, "returned 403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "api key"):
		return "auth"
	case strings.Contains(msg, "returned 429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "too many requests"):
		return "rate_limit"
	case strings.Contains(msg, "returned 500") ||
		strings.Contains(msg, "returned 502") ||
		strings.Contains(msg, "returned 503") ||
		strings.Contains(msg, "server error") ||
		strings.Contains(msg, "internal error"):
		return "server"
	default:
		return "unknown"
	}
}

// recordLLMMetrics records Prometheus metrics for a completed LLM call.
//
// Description:
//
//	One-shot metric recording for both success and error paths. Records
//	duration, call count, token counts (on success), and error type (on failure).
//
// Inputs:
//
//	provider - Provider name ("anthropic", "openai", "gemini", "ollama").
//	duration - How long the call took.
//	inputTokens - Estimated input token count (ignored on error).
//	outputTokens - Estimated output token count (ignored on error).
//	err - The error, if any. Nil means success.
//
// Thread Safety: Safe for concurrent use.
func recordLLMMetrics(provider string, duration time.Duration, inputTokens, outputTokens int, err error) {
	status := "success"
	if err != nil {
		status = "error"
		errType := classifyError(err)
		llmErrorsTotal.WithLabelValues(provider, errType).Inc()
	}

	llmCallDuration.WithLabelValues(provider, status).Observe(duration.Seconds())
	llmCallsTotal.WithLabelValues(provider, status).Inc()

	if err == nil {
		llmTokensTotal.WithLabelValues(provider, "input").Add(float64(inputTokens))
		llmTokensTotal.WithLabelValues(provider, "output").Add(float64(outputTokens))
	}
}

// incActiveRequests increments the active requests gauge for a provider.
//
// Inputs:
//
//	provider - Provider name.
func incActiveRequests(provider string) {
	llmActiveRequests.WithLabelValues(provider).Inc()
}

// decActiveRequests decrements the active requests gauge for a provider.
//
// Inputs:
//
//	provider - Provider name.
func decActiveRequests(provider string) {
	llmActiveRequests.WithLabelValues(provider).Dec()
}
