// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package coordinate

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Package-level tracer and meter for coordinate operations.
var (
	tracer = otel.Tracer("aleutian.coordinate")
	meter  = otel.Meter("aleutian.coordinate")
)

// Metrics for coordinate operations.
var (
	coordinateLatency metric.Float64Histogram
	coordinateTotal   metric.Int64Counter

	metricsOnce sync.Once
	metricsErr  error
)

// initMetrics initializes the metrics. Safe to call multiple times.
func initMetrics() error {
	metricsOnce.Do(func() {
		var err error

		coordinateLatency, err = meter.Float64Histogram(
			"coordinate_operation_duration_seconds",
			metric.WithDescription("Duration of coordinate operations"),
			metric.WithUnit("s"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		coordinateTotal, err = meter.Int64Counter(
			"coordinate_operation_total",
			metric.WithDescription("Total number of coordinate operations"),
		)
		if err != nil {
			metricsErr = err
			return
		}
	})
	return metricsErr
}

// ============================================================================
// PlanChanges OTel
// ============================================================================

// startPlanSpan creates a span for change planning.
func startPlanSpan(ctx context.Context, targetID string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "coordinate.MultiFileChangeCoordinator.PlanChanges",
		trace.WithAttributes(
			attribute.String("coordinate.operation", "plan_changes"),
			attribute.String("coordinate.target_id", targetID),
		),
	)
}

// setPlanSpanResult sets result attributes on a plan span.
func setPlanSpanResult(span trace.Span, fileCount int, err error) {
	span.SetAttributes(
		attribute.Int("coordinate.file_count", fileCount),
		attribute.Bool("coordinate.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordPlanMetrics records metrics for change planning.
func recordPlanMetrics(ctx context.Context, duration time.Duration, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", "plan_changes"),
		attribute.Bool("success", err == nil),
	)
	coordinateLatency.Record(ctx, duration.Seconds(), attrs)
	coordinateTotal.Add(ctx, 1, attrs)
}

// ============================================================================
// ValidatePlan OTel
// ============================================================================

// startValidatePlanSpan creates a span for plan validation.
func startValidatePlanSpan(ctx context.Context, planID string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "coordinate.MultiFileChangeCoordinator.ValidatePlan",
		trace.WithAttributes(
			attribute.String("coordinate.operation", "validate_plan"),
			attribute.String("coordinate.plan_id", planID),
		),
	)
}

// setValidatePlanSpanResult sets result attributes on a validate plan span.
func setValidatePlanSpanResult(span trace.Span, valid bool, errorCount int, err error) {
	span.SetAttributes(
		attribute.Bool("coordinate.valid", valid),
		attribute.Int("coordinate.error_count", errorCount),
		attribute.Bool("coordinate.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordValidatePlanMetrics records metrics for plan validation.
func recordValidatePlanMetrics(ctx context.Context, duration time.Duration, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("operation", "validate_plan"),
		attribute.Bool("success", err == nil),
	)
	coordinateLatency.Record(ctx, duration.Seconds(), attrs)
	coordinateTotal.Add(ctx, 1, attrs)
}
