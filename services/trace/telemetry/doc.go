// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package telemetry provides OpenTelemetry-based observability for Trace.
//
// This package initializes the OTel SDK with opinionated defaults for tracing
// and metrics, while allowing backend flexibility through exporter configuration.
//
// # Philosophy
//
// Be opinionated about the API, flexible about the backend. OpenTelemetry IS
// the abstraction layer. We use OTel APIs directly (no custom interfaces), and
// users swap backends by changing exporter configuration, not code.
//
// # Trace Backend (default: Jaeger via OTLP)
//
// Jaeger is the default trace backend. Since Jaeger 1.35+, it supports OTLP
// natively, which is the recommended protocol. Users can swap to Datadog,
// New Relic, or other OTLP-compatible backends via environment variables.
//
// # Metrics Backend (default: Prometheus)
//
// Prometheus is the default metrics backend. Metrics are exposed at /metrics
// endpoint for scraping. Users can swap to OTLP push-based metrics if needed.
//
// # Logging
//
// Uses slog (Go 1.21+ stdlib) for structured logging. LoggerWithTrace injects
// trace_id and span_id into log entries for correlation in Grafana/Loki.
//
// # Usage
//
//	cfg := telemetry.DefaultConfig()
//	shutdown, err := telemetry.Init(ctx, cfg)
//	if err != nil {
//	    return fmt.Errorf("init telemetry: %w", err)
//	}
//	defer shutdown(ctx)
//
//	// Now otel.Tracer() and otel.Meter() are configured
//	tracer := otel.Tracer("mypackage")
//	meter := otel.Meter("mypackage")
//
// # Environment Variables
//
// Standard OTel environment variables are supported:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint (default: localhost:4317)
//   - OTEL_TRACES_EXPORTER: otlp, stdout, or none (default: otlp)
//   - OTEL_METRICS_EXPORTER: prometheus, otlp, stdout, or none (default: prometheus)
//   - ALEUTIAN_ENV: environment name (default: development)
//
// # Thread Safety
//
// All exported functions are safe for concurrent use after Init() returns.
package telemetry
