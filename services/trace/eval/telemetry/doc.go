// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package telemetry provides observability infrastructure for the evaluation framework.
//
// # Overview
//
// The telemetry package implements the Three Pillars of Observability:
//   - Traces: Distributed tracing with OpenTelemetry
//   - Metrics: Prometheus-compatible metrics collection
//   - Logs: Structured logging with trace context
//
// # Architecture
//
//	┌─────────────────────────────────────────────────────────────────────────────┐
//	│                           TELEMETRY PACKAGE                                  │
//	├─────────────────────────────────────────────────────────────────────────────┤
//	│                                                                              │
//	│   Application Code                                                           │
//	│         │                                                                    │
//	│         ▼                                                                    │
//	│   ┌───────────────────────────────────────────────────────────────────────┐ │
//	│   │                         Sink Interface                                 │ │
//	│   │   RecordBenchmark() │ RecordComparison() │ RecordError() │ Flush()    │ │
//	│   └───────────────────────────────────────────────────────────────────────┘ │
//	│         │                         │                         │               │
//	│         ▼                         ▼                         ▼               │
//	│   ┌───────────┐           ┌───────────────┐           ┌───────────┐        │
//	│   │ Prometheus│           │ OpenTelemetry │           │  Composite│        │
//	│   │   Sink    │           │     Sink      │           │    Sink   │        │
//	│   └─────┬─────┘           └───────┬───────┘           └─────┬─────┘        │
//	│         │                         │                         │               │
//	│         ▼                         ▼                         ▼               │
//	│   ┌───────────┐           ┌───────────────┐           ┌───────────┐        │
//	│   │ /metrics  │           │    Jaeger     │           │  Multi-   │        │
//	│   │ endpoint  │           │   Exporter    │           │  Backend  │        │
//	│   └───────────┘           └───────────────┘           └───────────┘        │
//	│                                                                              │
//	└─────────────────────────────────────────────────────────────────────────────┘
//
// # Sink Interface
//
// The Sink interface is the primary abstraction for telemetry collection:
//
//	sink := telemetry.NewPrometheusSink(telemetry.PrometheusConfig{
//	    Namespace: "trace",
//	    Subsystem: "eval",
//	})
//
//	// Record benchmark results
//	sink.RecordBenchmark(ctx, result)
//
//	// Record comparison results
//	sink.RecordComparison(ctx, comparison)
//
// # Composite Sink
//
// Multiple sinks can be combined for multi-backend export:
//
//	composite := telemetry.NewCompositeSink(
//	    promSink,
//	    otelSink,
//	)
//
// # OpenTelemetry Integration
//
// The OTel sink provides distributed tracing and metric export:
//
//	otelSink, shutdown, err := telemetry.NewOTelSink(ctx, telemetry.OTelConfig{
//	    ServiceName:    "code-buddy-eval",
//	    OTLPEndpoint:   "localhost:4317",
//	    MetricInterval: 15 * time.Second,
//	})
//	defer shutdown(ctx)
//
// # Thread Safety
//
// All Sink implementations are safe for concurrent use from multiple goroutines.
//
// # Metric Naming Convention
//
// Metrics follow the pattern: <namespace>_<subsystem>_<metric>_<unit>
//
// Examples:
//   - trace_eval_benchmark_duration_seconds
//   - trace_eval_benchmark_iterations_total
//   - trace_eval_comparison_speedup_ratio
//   - trace_eval_errors_total
package telemetry
