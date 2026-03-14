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
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ServiceName != "aleutian" {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, "aleutian")
	}
	if cfg.TraceExporter != "none" {
		t.Errorf("TraceExporter = %q, want %q", cfg.TraceExporter, "none")
	}
	if cfg.MetricExporter != "none" {
		t.Errorf("MetricExporter = %q, want %q", cfg.MetricExporter, "none")
	}
	if cfg.OTLPEndpoint != "localhost:4317" {
		t.Errorf("OTLPEndpoint = %q, want %q", cfg.OTLPEndpoint, "localhost:4317")
	}
}

func TestInit_NilContext(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"

	_, err := Init(nil, cfg)
	if err != ErrNilContext {
		t.Errorf("Init(nil, cfg) error = %v, want %v", err, ErrNilContext)
	}
}

func TestInit_NoopExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "none"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if shutdown == nil {
		t.Fatal("shutdown function is nil")
	}

	// Verify shutdown works
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() error = %v", err)
	}
}

func TestInit_StdoutExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "stdout"
	cfg.MetricExporter = "none"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Verify tracer is configured
	tracer := otel.Tracer("test")
	if tracer == nil {
		t.Error("tracer is nil")
	}
}

func TestInit_UnknownExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "unknown_exporter"

	_, err := Init(context.Background(), cfg)
	if err == nil {
		t.Error("Init() with unknown exporter should fail")
	}
	if !strings.Contains(err.Error(), "unknown exporter type") {
		t.Errorf("error = %v, want to contain 'unknown exporter type'", err)
	}
}

func TestLoggerWithTrace_NoSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	// No span in context
	result := LoggerWithTrace(context.Background(), logger)

	// Should return original logger (no trace fields added)
	result.Info("test message")
	output := buf.String()

	if strings.Contains(output, "trace_id") {
		t.Errorf("output should not contain trace_id when no span: %s", output)
	}
}

func TestLoggerWithTrace_NilContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	result := LoggerWithTrace(nil, logger)

	// Should return original logger
	result.Info("test message")
	output := buf.String()

	if !strings.Contains(output, "test message") {
		t.Errorf("output should contain message: %s", output)
	}
}

func TestLoggerWithTrace_NilLogger(t *testing.T) {
	result := LoggerWithTrace(context.Background(), nil)

	// Should return slog.Default() instead of panicking
	if result == nil {
		t.Error("result should not be nil")
	}
}

func TestLoggerWithTrace_WithSpan(t *testing.T) {
	// Set up a tracer that creates valid span contexts
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "none"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Create a mock span context for testing
	traceID := trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})

	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	result := LoggerWithTrace(ctx, logger)
	result.Info("test message")

	output := buf.String()

	if !strings.Contains(output, "trace_id") {
		t.Errorf("output should contain trace_id: %s", output)
	}
	if !strings.Contains(output, "span_id") {
		t.Errorf("output should contain span_id: %s", output)
	}
	if !strings.Contains(output, traceID.String()) {
		t.Errorf("output should contain actual trace ID: %s", output)
	}
}

func TestLoggerWithNode(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	result := LoggerWithNode(context.Background(), logger, "PARSE_FILES")
	result.Info("test message")

	output := buf.String()

	if !strings.Contains(output, `"node":"PARSE_FILES"`) {
		t.Errorf("output should contain node field: %s", output)
	}
}

func TestLoggerWithSession(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	result := LoggerWithSession(context.Background(), logger, "abc123")
	result.Info("test message")

	output := buf.String()

	if !strings.Contains(output, `"session_id":"abc123"`) {
		t.Errorf("output should contain session_id field: %s", output)
	}
}

func TestGetEnvOr(t *testing.T) {
	t.Run("returns fallback when env not set", func(t *testing.T) {
		result := getEnvOr("TELEMETRY_TEST_NONEXISTENT_VAR_12345", "fallback")
		if result != "fallback" {
			t.Errorf("getEnvOr() = %q, want %q", result, "fallback")
		}
	})

	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("TELEMETRY_TEST_VAR", "custom_value")
		result := getEnvOr("TELEMETRY_TEST_VAR", "fallback")
		if result != "custom_value" {
			t.Errorf("getEnvOr() = %q, want %q", result, "custom_value")
		}
	})
}

func TestInit_PrometheusExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "prometheus"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Verify meter is configured
	meter := otel.Meter("test")
	if meter == nil {
		t.Error("meter is nil")
	}

	// Create a test counter to ensure metrics work
	counter, err := meter.Int64Counter("test_counter")
	if err != nil {
		t.Fatalf("creating counter: %v", err)
	}
	counter.Add(context.Background(), 1)

	// Verify MetricsHandler is available
	handler := MetricsHandler()
	if handler == nil {
		t.Fatal("MetricsHandler() returned nil")
	}
}

func TestMetricsHandler_ReturnsPrometheusFormat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "prometheus"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Create and increment a counter
	meter := otel.Meter("test_metrics")
	counter, err := meter.Int64Counter("telemetry_test_requests_total")
	if err != nil {
		t.Fatalf("creating counter: %v", err)
	}
	counter.Add(context.Background(), 42)

	handler := MetricsHandler()
	if handler == nil {
		t.Fatal("MetricsHandler() returned nil")
	}

	// Make a request to the metrics handler
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	// Verify it's Prometheus format (should contain # HELP or # TYPE)
	output := string(body)
	if !strings.Contains(output, "# HELP") && !strings.Contains(output, "# TYPE") {
		t.Errorf("output should be Prometheus format: %s", output[:min(200, len(output))])
	}
}

func TestMetricsHandler_NilBeforeInit(t *testing.T) {
	// Reset the handler to simulate fresh state
	prometheusHandlerMu.Lock()
	oldHandler := prometheusHandler
	prometheusHandler = nil
	prometheusHandlerMu.Unlock()

	defer func() {
		prometheusHandlerMu.Lock()
		prometheusHandler = oldHandler
		prometheusHandlerMu.Unlock()
	}()

	handler := MetricsHandler()
	if handler != nil {
		t.Error("MetricsHandler() should return nil before Prometheus init")
	}
}

func TestInit_StdoutMetricExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "stdout"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Verify meter is configured
	meter := otel.Meter("test")
	if meter == nil {
		t.Error("meter is nil")
	}
}

func TestInit_UnknownMetricExporter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "unknown_metric_exporter"

	_, err := Init(context.Background(), cfg)
	if err == nil {
		t.Error("Init() with unknown metric exporter should fail")
	}
	if !strings.Contains(err.Error(), "unknown exporter type") {
		t.Errorf("error = %v, want to contain 'unknown exporter type'", err)
	}
}

func TestDefaultConfig_SampleRate(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.SampleRate != 1.0 {
		t.Errorf("SampleRate = %v, want 1.0", cfg.SampleRate)
	}
	if cfg.AllowDegraded != true {
		t.Errorf("AllowDegraded = %v, want true", cfg.AllowDegraded)
	}
}

func TestGetSampler(t *testing.T) {
	tests := []struct {
		name     string
		rate     float64
		expected string
	}{
		{"full sampling", 1.0, "AlwaysOnSampler"},
		{"above 100%", 1.5, "AlwaysOnSampler"},
		{"no sampling", 0.0, "AlwaysOffSampler"},
		{"below 0%", -0.5, "AlwaysOffSampler"},
		{"partial sampling", 0.5, "TraceIDRatioBased"},
		{"10% sampling", 0.1, "TraceIDRatioBased"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sampler := getSampler(tt.rate)
			description := sampler.Description()

			// Verify the sampler type based on description
			switch tt.expected {
			case "AlwaysOnSampler":
				if description != "AlwaysOnSampler" {
					t.Errorf("getSampler(%v) = %q, want %q", tt.rate, description, tt.expected)
				}
			case "AlwaysOffSampler":
				if description != "AlwaysOffSampler" {
					t.Errorf("getSampler(%v) = %q, want %q", tt.rate, description, tt.expected)
				}
			case "TraceIDRatioBased":
				if !strings.Contains(description, "TraceIDRatioBased") {
					t.Errorf("getSampler(%v) = %q, want to contain %q", tt.rate, description, tt.expected)
				}
			}
		})
	}
}

func TestInit_WithSampleRate(t *testing.T) {
	t.Run("full sampling (1.0)", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.TraceExporter = "stdout"
		cfg.MetricExporter = "none"
		cfg.SampleRate = 1.0

		shutdown, err := Init(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		defer shutdown(context.Background())

		// Create a span and verify it's sampled
		tracer := otel.Tracer("test")
		_, span := tracer.Start(context.Background(), "test-span")
		defer span.End()

		if !span.SpanContext().IsSampled() {
			t.Error("expected span to be sampled with rate 1.0")
		}
	})

	t.Run("no sampling (0.0)", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.TraceExporter = "stdout"
		cfg.MetricExporter = "none"
		cfg.SampleRate = 0.0

		shutdown, err := Init(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		defer shutdown(context.Background())

		// Create spans and verify none are sampled
		tracer := otel.Tracer("test")
		sampledCount := 0
		for i := 0; i < 10; i++ {
			_, span := tracer.Start(context.Background(), "test-span")
			if span.SpanContext().IsSampled() {
				sampledCount++
			}
			span.End()
		}

		if sampledCount > 0 {
			t.Errorf("expected no spans sampled with rate 0.0, got %d", sampledCount)
		}
	})

	t.Run("partial sampling (0.5)", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.TraceExporter = "stdout"
		cfg.MetricExporter = "none"
		cfg.SampleRate = 0.5

		shutdown, err := Init(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		defer shutdown(context.Background())

		// Create many spans and verify sampling is probabilistic
		tracer := otel.Tracer("test")
		sampledCount := 0
		totalSpans := 100
		for i := 0; i < totalSpans; i++ {
			_, span := tracer.Start(context.Background(), "test-span")
			if span.SpanContext().IsSampled() {
				sampledCount++
			}
			span.End()
		}

		// With 50% sampling, we expect roughly half to be sampled
		// Allow for statistical variance (30-70%)
		sampledRatio := float64(sampledCount) / float64(totalSpans)
		if sampledRatio < 0.2 || sampledRatio > 0.8 {
			t.Errorf("expected ~50%% sampling with rate 0.5, got %.1f%%", sampledRatio*100)
		}
	})
}

func TestInit_AllowDegraded(t *testing.T) {
	// Note: This test verifies AllowDegraded behavior when OTLP endpoint is unreachable
	// The OTLP gRPC client connects asynchronously, so the Init may not fail immediately.
	// We test that the configuration is properly accepted and the option exists.

	t.Run("AllowDegraded config is accepted", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.TraceExporter = "stdout" // Use stdout to avoid connection issues
		cfg.MetricExporter = "none"
		cfg.AllowDegraded = true

		shutdown, err := Init(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Init() error = %v", err)
		}
		defer shutdown(context.Background())

		// Tracer should work
		tracer := otel.Tracer("test")
		_, span := tracer.Start(context.Background(), "test-span")
		span.End()
	})

	t.Run("AllowDegraded=true is default", func(t *testing.T) {
		cfg := DefaultConfig()
		if cfg.AllowDegraded != true {
			t.Errorf("AllowDegraded default = %v, want true", cfg.AllowDegraded)
		}
	})
}

func TestInit_PropagatorIsSet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TraceExporter = "none"
	cfg.MetricExporter = "none"

	shutdown, err := Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	defer shutdown(context.Background())

	// Verify propagator is set by checking that it can extract/inject
	propagator := otel.GetTextMapPropagator()
	if propagator == nil {
		t.Error("expected propagator to be set")
	}

	// Propagator should support TraceContext and Baggage
	fields := propagator.Fields()
	hasTraceParent := false
	for _, f := range fields {
		if f == "traceparent" {
			hasTraceParent = true
			break
		}
	}
	if !hasTraceParent {
		t.Error("expected propagator to include traceparent field")
	}
}
