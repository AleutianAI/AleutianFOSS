// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package providers

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// chatTracerName is the shared OTel tracer name for all ChatClient adapters.
const chatTracerName = "trace.providers"

// Package-level Prometheus metrics for ChatClient adapter operations.
// Auto-registered via promauto so no explicit registry wiring is needed.
var (
	// chatCallDuration measures the duration of ChatClient API calls.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - status: "success" or "error"
	chatCallDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "trace",
			Subsystem: "chat",
			Name:      "call_duration_seconds",
			Help:      "Duration of ChatClient API calls in seconds.",
			Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"provider", "status"},
	)

	// chatCallsTotal counts the total number of ChatClient API calls.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - status: "success" or "error"
	chatCallsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "trace",
			Subsystem: "chat",
			Name:      "calls_total",
			Help:      "Total number of ChatClient API calls.",
		},
		[]string{"provider", "status"},
	)

	// chatErrorsTotal counts the total ChatClient errors by type.
	//
	// Labels:
	//   - provider: "anthropic", "openai", "gemini", "ollama"
	//   - error_type: "timeout", "auth", "rate_limit", "server", "nil_client", "unknown"
	chatErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "trace",
			Subsystem: "chat",
			Name:      "errors_total",
			Help:      "Total ChatClient errors by type.",
		},
		[]string{"provider", "error_type"},
	)
)

// classifyChatError maps an error to a label-safe error type string.
//
// Description:
//
//	Inspects the error message to categorize it into one of the predefined
//	error types. Used for Prometheus labels to avoid high cardinality.
//
// Inputs:
//
//	err - The error to classify. May be nil.
//
// Outputs:
//
//	string - One of: "timeout", "auth", "rate_limit", "server",
//	         "nil_client", "unknown". Returns empty string for nil error.
//
// Thread Safety: Safe for concurrent use.
func classifyChatError(err error) string {
	if err == nil {
		return ""
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "client is nil") ||
		strings.Contains(msg, "manager is nil"):
		return "nil_client"
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

// recordChatMetrics records Prometheus metrics for a completed ChatClient call.
//
// Description:
//
//	One-shot metric recording for both success and error paths.
//
// Inputs:
//
//	provider - Provider name ("anthropic", "openai", "gemini", "ollama").
//	duration - How long the call took.
//	err - The error, if any. Nil means success.
//
// Thread Safety: Safe for concurrent use.
func recordChatMetrics(provider string, duration time.Duration, err error) {
	status := "success"
	if err != nil {
		status = "error"
		errType := classifyChatError(err)
		chatErrorsTotal.WithLabelValues(provider, errType).Inc()
	}

	chatCallDuration.WithLabelValues(provider, status).Observe(duration.Seconds())
	chatCallsTotal.WithLabelValues(provider, status).Inc()
}
