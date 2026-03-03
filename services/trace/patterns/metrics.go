// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Package-level tracer and meter for pattern detection operations.
var (
	tracer = otel.Tracer("aleutian.patterns")
	meter  = otel.Meter("aleutian.patterns")
)

// Metrics for pattern detection operations.
var (
	detectLatency  metric.Float64Histogram
	detectTotal    metric.Int64Counter
	patternsFound  metric.Int64Histogram
	patternsByType metric.Int64Counter

	metricsOnce sync.Once
	metricsErr  error
)

// initMetrics initializes the metrics. Safe to call multiple times.
func initMetrics() error {
	metricsOnce.Do(func() {
		var err error

		detectLatency, err = meter.Float64Histogram(
			"patterns_detect_duration_seconds",
			metric.WithDescription("Duration of pattern detection operations"),
			metric.WithUnit("s"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		detectTotal, err = meter.Int64Counter(
			"patterns_detect_total",
			metric.WithDescription("Total number of pattern detection operations"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		patternsFound, err = meter.Int64Histogram(
			"patterns_found",
			metric.WithDescription("Number of patterns found per detection"),
		)
		if err != nil {
			metricsErr = err
			return
		}

		patternsByType, err = meter.Int64Counter(
			"patterns_by_type_total",
			metric.WithDescription("Total patterns found by type"),
		)
		if err != nil {
			metricsErr = err
			return
		}
	})
	return metricsErr
}

// startDetectSpan creates a span for a pattern detection operation.
func startDetectSpan(ctx context.Context, scope string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "PatternDetector.DetectPatterns",
		trace.WithAttributes(
			attribute.String("patterns.scope", scope),
		),
	)
}

// setDetectSpanResult sets the result attributes on a detection span.
func setDetectSpanResult(span trace.Span, patternCount int, success bool) {
	span.SetAttributes(
		attribute.Int("patterns.count", patternCount),
		attribute.Bool("patterns.success", success),
	)
}

// recordDetectMetrics records metrics for a pattern detection operation.
func recordDetectMetrics(ctx context.Context, duration time.Duration, patternCount int, success bool) {
	if err := initMetrics(); err != nil {
		return
	}

	attrs := metric.WithAttributes(
		attribute.Bool("success", success),
	)

	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(patternCount))
}

// recordPatternByType records a pattern found by type.
func recordPatternByType(ctx context.Context, patternType string) {
	if err := initMetrics(); err != nil {
		return
	}
	patternsByType.Add(ctx, 1, metric.WithAttributes(
		attribute.String("pattern_type", patternType),
	))
}

// ============================================================================
// Smell Finder OTel
// ============================================================================

// startSmellsSpan creates a span for code smell detection.
func startSmellsSpan(ctx context.Context, scope string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "patterns.SmellFinder.FindCodeSmells",
		trace.WithAttributes(
			attribute.String("smells.scope", scope),
		),
	)
}

// setSmellsSpanResult sets result attributes on a smell detection span.
func setSmellsSpanResult(span trace.Span, count int, err error) {
	span.SetAttributes(
		attribute.Int("smells.count", count),
		attribute.Bool("smells.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordSmellsMetrics records metrics for smell detection.
func recordSmellsMetrics(ctx context.Context, duration time.Duration, count int, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("tool", "find_code_smells"),
		attribute.Bool("success", err == nil),
	)
	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(count))
}

// ============================================================================
// Duplication Finder OTel
// ============================================================================

// startDuplicationSpan creates a span for duplication detection.
func startDuplicationSpan(ctx context.Context, scope string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "patterns.DuplicationFinder.FindDuplication",
		trace.WithAttributes(
			attribute.String("duplication.scope", scope),
		),
	)
}

// setDuplicationSpanResult sets result attributes on a duplication detection span.
func setDuplicationSpanResult(span trace.Span, count int, err error) {
	span.SetAttributes(
		attribute.Int("duplication.count", count),
		attribute.Bool("duplication.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordDuplicationMetrics records metrics for duplication detection.
func recordDuplicationMetrics(ctx context.Context, duration time.Duration, count int, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("tool", "find_duplication"),
		attribute.Bool("success", err == nil),
	)
	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(count))
}

// ============================================================================
// Circular Dependency Finder OTel
// ============================================================================

// startCircularDepsSpan creates a span for circular dependency detection.
func startCircularDepsSpan(ctx context.Context, scope string, depType CircularDepType) (context.Context, trace.Span) {
	return tracer.Start(ctx, "patterns.CircularDepFinder.FindCircularDeps",
		trace.WithAttributes(
			attribute.String("circular_deps.scope", scope),
			attribute.String("circular_deps.type", string(depType)),
		),
	)
}

// setCircularDepsSpanResult sets result attributes on a circular dep detection span.
func setCircularDepsSpanResult(span trace.Span, count int, err error) {
	span.SetAttributes(
		attribute.Int("circular_deps.count", count),
		attribute.Bool("circular_deps.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordCircularDepsMetrics records metrics for circular dependency detection.
func recordCircularDepsMetrics(ctx context.Context, duration time.Duration, count int, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("tool", "find_circular_deps"),
		attribute.Bool("success", err == nil),
	)
	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(count))
}

// ============================================================================
// Convention Extractor OTel
// ============================================================================

// startConventionsSpan creates a span for convention extraction.
func startConventionsSpan(ctx context.Context, scope string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "patterns.ConventionExtractor.ExtractConventions",
		trace.WithAttributes(
			attribute.String("conventions.scope", scope),
		),
	)
}

// setConventionsSpanResult sets result attributes on a convention extraction span.
func setConventionsSpanResult(span trace.Span, count int, err error) {
	span.SetAttributes(
		attribute.Int("conventions.count", count),
		attribute.Bool("conventions.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordConventionsMetrics records metrics for convention extraction.
func recordConventionsMetrics(ctx context.Context, duration time.Duration, count int, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("tool", "extract_conventions"),
		attribute.Bool("success", err == nil),
	)
	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(count))
}

// ============================================================================
// Dead Code Finder OTel
// ============================================================================

// startDeadCodeSpan creates a span for dead code detection.
func startDeadCodeSpan(ctx context.Context, scope string) (context.Context, trace.Span) {
	return tracer.Start(ctx, "patterns.DeadCodeFinder.FindDeadCode",
		trace.WithAttributes(
			attribute.String("dead_code.scope", scope),
		),
	)
}

// setDeadCodeSpanResult sets result attributes on a dead code detection span.
func setDeadCodeSpanResult(span trace.Span, count int, err error) {
	span.SetAttributes(
		attribute.Int("dead_code.count", count),
		attribute.Bool("dead_code.success", err == nil),
	)
	if err != nil {
		span.RecordError(err)
	}
}

// recordDeadCodeMetrics records metrics for dead code detection.
func recordDeadCodeMetrics(ctx context.Context, duration time.Duration, count int, err error) {
	if initErr := initMetrics(); initErr != nil {
		return
	}
	attrs := metric.WithAttributes(
		attribute.String("tool", "find_dead_code"),
		attribute.Bool("success", err == nil),
	)
	detectLatency.Record(ctx, duration.Seconds(), attrs)
	detectTotal.Add(ctx, 1, attrs)
	patternsFound.Record(ctx, int64(count))
}
