// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package cancel

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus metrics for the cancellation framework.
//
// Thread Safety: Safe for concurrent use (Prometheus metrics are thread-safe).
type Metrics struct {
	// CancelTotal counts cancellations by type, level, and component.
	CancelTotal *prometheus.CounterVec

	// CancelAllTotal counts emergency cancel-all operations.
	CancelAllTotal prometheus.Counter

	// CancelDurationSeconds measures the time from signal to completion.
	CancelDurationSeconds *prometheus.HistogramVec

	// DeadlockDetectedTotal counts deadlock detections by component.
	DeadlockDetectedTotal *prometheus.CounterVec

	// ResourceLimitExceededTotal counts resource limit violations.
	ResourceLimitExceededTotal *prometheus.CounterVec

	// TimeoutTotal counts algorithm timeouts by component.
	TimeoutTotal *prometheus.CounterVec

	// PartialResultsCollected counts partial results collected during shutdown.
	PartialResultsCollected prometheus.Counter

	// ForceKilledTotal counts contexts that had to be force-killed.
	ForceKilledTotal prometheus.Counter

	// SessionsCreated counts sessions created.
	SessionsCreated prometheus.Counter

	// ActiveContexts is a gauge of currently active (non-terminal) contexts.
	ActiveContexts *prometheus.GaugeVec

	// GracefulShutdownDurationSeconds measures total shutdown duration.
	GracefulShutdownDurationSeconds prometheus.Histogram

	// ProgressReportsTotal counts progress reports by component.
	ProgressReportsTotal *prometheus.CounterVec
}

// NewMetrics creates and registers all cancellation metrics.
//
// Description:
//
//	Creates a new Metrics instance with all Prometheus metrics registered.
//	Uses promauto for automatic registration with the default registerer.
//
// Outputs:
//   - *Metrics: The created metrics. Never nil.
//
// Thread Safety: Safe for concurrent use.
func NewMetrics() *Metrics {
	return &Metrics{
		CancelTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "total",
				Help:      "Total cancellations by type, level, and component",
			},
			[]string{"type", "level", "component"},
		),

		CancelAllTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "all_total",
				Help:      "Total emergency cancel-all operations",
			},
		),

		CancelDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "duration_seconds",
				Help:      "Time from cancel signal to completion",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"level"},
		),

		DeadlockDetectedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "deadlock_detected_total",
				Help:      "Total deadlock detections by component",
			},
			[]string{"component"},
		),

		ResourceLimitExceededTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "resource_limit_exceeded_total",
				Help:      "Total resource limit violations by resource type and component",
			},
			[]string{"resource", "component"},
		),

		TimeoutTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "timeout_total",
				Help:      "Total algorithm timeouts by component",
			},
			[]string{"component"},
		),

		PartialResultsCollected: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "partial_results_collected_total",
				Help:      "Total partial results collected during shutdown",
			},
		),

		ForceKilledTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "force_killed_total",
				Help:      "Total contexts force-killed during shutdown",
			},
		),

		SessionsCreated: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "sessions_created_total",
				Help:      "Total sessions created",
			},
		),

		ActiveContexts: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "active_contexts",
				Help:      "Currently active (non-terminal) contexts by level",
			},
			[]string{"level"},
		),

		GracefulShutdownDurationSeconds: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "graceful_shutdown_duration_seconds",
				Help:      "Total graceful shutdown duration",
				Buckets:   []float64{0.5, 1, 2, 5, 10, 30},
			},
		),

		ProgressReportsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trace",
				Subsystem: "cancel",
				Name:      "progress_reports_total",
				Help:      "Total progress reports by component",
			},
			[]string{"component"},
		),
	}
}

// MetricsConfig configures which metrics to enable.
type MetricsConfig struct {
	// Enabled determines if metrics are collected at all.
	Enabled bool

	// Namespace is the Prometheus namespace prefix.
	// Default: "trace"
	Namespace string

	// Subsystem is the Prometheus subsystem prefix.
	// Default: "cancel"
	Subsystem string
}

// DefaultMetricsConfig returns the default metrics configuration.
func DefaultMetricsConfig() MetricsConfig {
	return MetricsConfig{
		Enabled:   true,
		Namespace: "trace",
		Subsystem: "cancel",
	}
}
