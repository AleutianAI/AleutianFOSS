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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// =============================================================================
// Prometheus Metrics for Egress Control
// =============================================================================

var (
	// egressCallsTotal counts egress call attempts by provider and status.
	// Labels: provider (anthropic, openai, gemini), status (allowed, blocked)
	egressCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "calls_total",
		Help:      "Total egress call attempts by provider and status",
	}, []string{"provider", "status"})

	// egressTokensTotal counts tokens sent/received by provider and direction.
	// Labels: provider, direction (input, output)
	egressTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "tokens_total",
		Help:      "Total tokens by provider and direction",
	}, []string{"provider", "direction"})

	// egressBlockedTotal counts blocked egress attempts by provider and blocker.
	// Labels: provider, blocked_by (kill_switch, policy, consent, sensitive_data, rate_limit, budget, cost)
	egressBlockedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "blocked_total",
		Help:      "Total blocked egress attempts by provider and blocking component",
	}, []string{"provider", "blocked_by"})

	// egressLatencySeconds measures end-to-end egress latency (including guard checks).
	// Labels: provider
	egressLatencySeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "latency_seconds",
		Help:      "End-to-end egress latency including guard checks",
		Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
	}, []string{"provider"})

	// egressCostCentsTotal tracks cumulative cost in US cents by provider.
	// Labels: provider
	egressCostCentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "cost_cents_total",
		Help:      "Cumulative cost in US cents by provider",
	}, []string{"provider"})

	// egressSensitivityTotal counts classifications by provider and sensitivity level.
	// Labels: provider, sensitivity (public, confidential, pii, phi, secret)
	egressSensitivityTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "egress",
		Name:      "sensitivity_total",
		Help:      "Data classification counts by provider and sensitivity level",
	}, []string{"provider", "sensitivity"})
)

// RecordEgressAllowed records a successful egress call.
//
// Inputs:
//   - provider: The provider name.
//   - inputTokens: Number of input tokens sent.
//   - outputTokens: Number of output tokens received.
//   - durationSec: Call duration in seconds.
//   - costCents: Estimated cost in US cents.
func RecordEgressAllowed(provider string, inputTokens, outputTokens int, durationSec, costCents float64) {
	egressCallsTotal.WithLabelValues(provider, "allowed").Inc()
	egressTokensTotal.WithLabelValues(provider, "input").Add(float64(inputTokens))
	egressTokensTotal.WithLabelValues(provider, "output").Add(float64(outputTokens))
	egressLatencySeconds.WithLabelValues(provider).Observe(durationSec)
	if costCents > 0 {
		egressCostCentsTotal.WithLabelValues(provider).Add(costCents)
	}
}

// RecordEgressBlocked records a blocked egress attempt.
//
// Inputs:
//   - provider: The provider name.
//   - blockedBy: The component that blocked the request
//     (e.g., "kill_switch", "policy", "consent", "sensitive_data", "rate_limit", "budget", "cost").
func RecordEgressBlocked(provider, blockedBy string) {
	egressCallsTotal.WithLabelValues(provider, "blocked").Inc()
	egressBlockedTotal.WithLabelValues(provider, blockedBy).Inc()
}

// RecordEgressSensitivity records a data sensitivity classification.
//
// Inputs:
//   - provider: The provider name.
//   - sensitivity: The classification level.
func RecordEgressSensitivity(provider string, sensitivity DataSensitivity) {
	egressSensitivityTotal.WithLabelValues(provider, sensitivity.String()).Inc()
}
