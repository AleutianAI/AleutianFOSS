// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Config controls telemetry behavior.
//
// All fields have sensible defaults via DefaultConfig().
type Config struct {
	// ServiceName identifies this service in traces and metrics.
	ServiceName string `json:"service_name"`

	// ServiceVersion is the version string for this service.
	ServiceVersion string `json:"service_version"`

	// Environment identifies the deployment environment (development, production).
	Environment string `json:"environment"`

	// TraceExporter selects the trace exporter: "otlp", "stdout", or "none".
	TraceExporter string `json:"trace_exporter"`

	// MetricExporter selects the metric exporter: "prometheus", "otlp", "stdout", or "none".
	MetricExporter string `json:"metric_exporter"`

	// OTLPEndpoint is the OTLP receiver endpoint for traces.
	OTLPEndpoint string `json:"otlp_endpoint"`

	// OTLPInsecure disables TLS verification for OTLP connections.
	OTLPInsecure bool `json:"otlp_insecure"`

	// PrometheusPort is the port for the /metrics endpoint (default: 9090).
	PrometheusPort int `json:"prometheus_port"`

	// SampleRate is the percentage of traces to sample (0.0-1.0).
	// Default: 1.0 (100%) for development, recommend 0.1 (10%) for production.
	// When set to 1.0, AlwaysSample() is used.
	// When set to 0.0, NeverSample() is used.
	// Otherwise, TraceIDRatioBased() is used.
	SampleRate float64 `json:"sample_rate"`

	// AllowDegraded allows the service to start even if telemetry backends are unavailable.
	// When true, Init() logs a warning but returns success with noop providers.
	// Default: true (server starts cleanly without OTel backends).
	AllowDegraded bool `json:"allow_degraded"`
}

// DefaultConfig returns opinionated defaults for development.
//
// Environment variables override defaults where applicable:
//   - ALEUTIAN_ENV: environment name
//   - OTEL_TRACES_EXPORTER: trace exporter type
//   - OTEL_METRICS_EXPORTER: metric exporter type
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint
func DefaultConfig() Config {
	return Config{
		ServiceName:    "aleutian",
		ServiceVersion: "1.0.0",
		Environment:    getEnvOr("ALEUTIAN_ENV", "development"),
		TraceExporter:  getEnvOr("OTEL_TRACES_EXPORTER", "none"),
		MetricExporter: getEnvOr("OTEL_METRICS_EXPORTER", "none"),
		OTLPEndpoint:   getEnvOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTLPInsecure:   true,
		PrometheusPort: 9090,
		SampleRate:     1.0, // 100% sampling for development
		AllowDegraded:  true,
	}
}

// Init initializes the telemetry stack with the given configuration.
//
// Description:
//
//	Sets up OpenTelemetry TracerProvider and MeterProvider based on the
//	configuration. After Init returns successfully, otel.Tracer() and
//	otel.Meter() can be used throughout the application.
//
// Inputs:
//
//	ctx - Context for initialization (used for exporter connections).
//	cfg - Telemetry configuration. Use DefaultConfig() for sensible defaults.
//
// Outputs:
//
//	shutdown - Function to call on application exit for cleanup. Must be called.
//	error - Non-nil if initialization fails.
//
// Example:
//
//	shutdown, err := telemetry.Init(ctx, telemetry.DefaultConfig())
//	if err != nil {
//	    return fmt.Errorf("init telemetry: %w", err)
//	}
//	defer shutdown(context.Background())
//
// Thread Safety: Call once at application startup.
func Init(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	var shutdownFuncs []func(context.Context) error

	// Compose shutdown function that calls all registered cleanups
	shutdown = func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if err := fn(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("shutdown errors: %v", errs)
		}
		return nil
	}

	// Build resource (service identity) using standard attribute keys
	res := resource.NewWithAttributes(
		"",
		attribute.String("service.name", cfg.ServiceName),
		attribute.String("service.version", cfg.ServiceVersion),
		attribute.String("deployment.environment", cfg.Environment),
	)

	// Set up W3C TraceContext propagator for distributed tracing
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// --- TRACES ---
	if cfg.TraceExporter != "none" {
		tp, err := initTracer(ctx, cfg, res)
		if err != nil {
			return nil, fmt.Errorf("init tracer: %w", err)
		}
		otel.SetTracerProvider(tp)
		shutdownFuncs = append(shutdownFuncs, tp.Shutdown)
	}

	// --- METRICS ---
	if cfg.MetricExporter != "none" {
		mp, err := initMeter(ctx, cfg, res)
		if err != nil {
			return nil, fmt.Errorf("init meter: %w", err)
		}
		otel.SetMeterProvider(mp)
		shutdownFuncs = append(shutdownFuncs, mp.Shutdown)
	}

	return shutdown, nil
}

// initTracer creates and returns a configured TracerProvider.
func initTracer(ctx context.Context, cfg Config, res *resource.Resource) (*trace.TracerProvider, error) {
	var exporter trace.SpanExporter
	var err error

	switch cfg.TraceExporter {
	case "otlp", "jaeger":
		// Jaeger now supports OTLP natively (recommended since Jaeger 1.35)
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exporter, err = otlptracegrpc.New(ctx, opts...)
		if err != nil {
			if cfg.AllowDegraded {
				slog.Warn("trace exporter unavailable, using noop provider",
					slog.String("endpoint", cfg.OTLPEndpoint),
					slog.String("error", err.Error()))
				return trace.NewTracerProvider(trace.WithResource(res)), nil
			}
			return nil, fmt.Errorf("create OTLP exporter: %w", err)
		}

	case "stdout":
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("create stdout exporter: %w", err)
		}

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownExporter, cfg.TraceExporter)
	}

	// Select sampler based on SampleRate configuration
	sampler := getSampler(cfg.SampleRate)

	// Create TracerProvider with batcher (batches spans before export)
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
		trace.WithSampler(sampler),
	)

	return tp, nil
}

// getSampler returns the appropriate sampler based on the sample rate.
//
// - SampleRate >= 1.0: AlwaysSample (100% sampling)
// - SampleRate <= 0.0: NeverSample (0% sampling)
// - Otherwise: TraceIDRatioBased (percentage sampling)
func getSampler(rate float64) trace.Sampler {
	if rate >= 1.0 {
		return trace.AlwaysSample()
	}
	if rate <= 0.0 {
		return trace.NeverSample()
	}
	return trace.TraceIDRatioBased(rate)
}

// getEnvOr returns the environment variable value or the fallback.
func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// prometheusHandler stores the Prometheus exporter's HTTP handler.
// Access via MetricsHandler().
var (
	prometheusHandler   http.Handler
	prometheusHandlerMu sync.RWMutex
)

// MetricsHandler returns the HTTP handler for the /metrics endpoint.
//
// Description:
//
//	Returns the Prometheus metrics handler if Prometheus exporter is enabled.
//	Returns nil if metrics are disabled or a different exporter is used.
//
// Outputs:
//
//	http.Handler - The metrics handler, or nil if unavailable.
//
// Example:
//
//	handler := telemetry.MetricsHandler()
//	if handler != nil {
//	    http.Handle("/metrics", handler)
//	}
//
// Thread Safety: Safe for concurrent use.
func MetricsHandler() http.Handler {
	prometheusHandlerMu.RLock()
	defer prometheusHandlerMu.RUnlock()
	return prometheusHandler
}

// initMeter creates and returns a configured MeterProvider.
func initMeter(_ context.Context, cfg Config, res *resource.Resource) (*metric.MeterProvider, error) {
	switch cfg.MetricExporter {
	case "prometheus":
		// Create Prometheus exporter which registers with default prometheus registry
		exporter, err := promexporter.New()
		if err != nil {
			return nil, fmt.Errorf("create prometheus exporter: %w", err)
		}

		// Store the promhttp handler for later retrieval via MetricsHandler()
		// The OTel prometheus exporter registers as a collector with the default
		// prometheus registry, so promhttp.Handler() will include our metrics.
		prometheusHandlerMu.Lock()
		prometheusHandler = promhttp.Handler()
		prometheusHandlerMu.Unlock()

		return metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(exporter),
		), nil

	case "stdout":
		exporter, err := stdoutmetric.New(stdoutmetric.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("create stdout metric exporter: %w", err)
		}

		return metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(metric.NewPeriodicReader(exporter)),
		), nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownExporter, cfg.MetricExporter)
	}
}
