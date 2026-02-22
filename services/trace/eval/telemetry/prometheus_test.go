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
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// -----------------------------------------------------------------------------
// Configuration Tests
// -----------------------------------------------------------------------------

func TestDefaultPrometheusConfig(t *testing.T) {
	config := DefaultPrometheusConfig()

	if config.Namespace != "trace" {
		t.Errorf("Namespace = %s, want trace", config.Namespace)
	}
	if config.Subsystem != "eval" {
		t.Errorf("Subsystem = %s, want eval", config.Subsystem)
	}
	if len(config.LatencyBuckets) == 0 {
		t.Error("LatencyBuckets should not be empty")
	}
	if len(config.ThroughputBuckets) == 0 {
		t.Error("ThroughputBuckets should not be empty")
	}
	if len(config.MemoryBuckets) == 0 {
		t.Error("MemoryBuckets should not be empty")
	}
}

func TestPrometheusConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		config := DefaultPrometheusConfig()
		if err := config.Validate(); err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
	})

	t.Run("empty namespace", func(t *testing.T) {
		config := DefaultPrometheusConfig()
		config.Namespace = ""
		if err := config.Validate(); err == nil {
			t.Error("Validate() should fail for empty namespace")
		}
	})

	t.Run("empty subsystem", func(t *testing.T) {
		config := DefaultPrometheusConfig()
		config.Subsystem = ""
		if err := config.Validate(); err == nil {
			t.Error("Validate() should fail for empty subsystem")
		}
	})
}

// -----------------------------------------------------------------------------
// NewPrometheusSink Tests
// -----------------------------------------------------------------------------

func TestNewPrometheusSink(t *testing.T) {
	t.Run("creates with valid config", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg

		sink, err := NewPrometheusSink(config)
		if err != nil {
			t.Fatalf("NewPrometheusSink failed: %v", err)
		}
		if sink == nil {
			t.Fatal("Expected non-nil sink")
		}
		sink.Close()
	})

	t.Run("rejects nil config", func(t *testing.T) {
		_, err := NewPrometheusSink(nil)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("Expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("rejects invalid config", func(t *testing.T) {
		config := &PrometheusConfig{
			Namespace: "", // Invalid
			Subsystem: "test",
		}
		_, err := NewPrometheusSink(config)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("Expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("applies default buckets", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := &PrometheusConfig{
			Namespace: "test",
			Subsystem: "test",
			Registry:  reg,
			// Leave bucket slices nil
		}

		sink, err := NewPrometheusSink(config)
		if err != nil {
			t.Fatalf("NewPrometheusSink failed: %v", err)
		}
		sink.Close()
	})
}

// -----------------------------------------------------------------------------
// RecordBenchmark Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_RecordBenchmark(t *testing.T) {
	t.Run("records benchmark metrics", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestBenchmarkData()
		err := sink.RecordBenchmark(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordBenchmark failed: %v", err)
		}

		// Verify metrics were recorded
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("Gather failed: %v", err)
		}

		// Check that expected metrics exist
		metricNames := make(map[string]bool)
		for _, mf := range mfs {
			metricNames[mf.GetName()] = true
		}

		expectedMetrics := []string{
			"trace_eval_benchmark_duration_seconds",
			"trace_eval_benchmark_iterations_total",
			"trace_eval_benchmark_latency_seconds",
			"trace_eval_benchmark_throughput_ops_per_second",
			"trace_eval_benchmark_memory_bytes",
			"trace_eval_benchmark_gc_pauses_total",
			"trace_eval_benchmark_errors_total",
		}

		for _, name := range expectedMetrics {
			if !metricNames[name] {
				t.Errorf("Expected metric %s not found", name)
			}
		}
	})

	t.Run("records benchmark without memory", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestBenchmarkData()
		data.Memory = nil
		data.ErrorCount = 0

		err := sink.RecordBenchmark(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordBenchmark failed: %v", err)
		}
	})

	t.Run("handles empty name", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestBenchmarkData()
		data.Name = ""

		err := sink.RecordBenchmark(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordBenchmark failed: %v", err)
		}
	})

	t.Run("rejects nil context", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordBenchmark(nil, createTestBenchmarkData())
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("Expected ErrNilContext, got %v", err)
		}
	})

	t.Run("rejects nil data", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordBenchmark(context.Background(), nil)
		if !errors.Is(err, ErrNilData) {
			t.Errorf("Expected ErrNilData, got %v", err)
		}
	})

	t.Run("returns error after close", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		sink.Close()

		err := sink.RecordBenchmark(context.Background(), createTestBenchmarkData())
		if !errors.Is(err, ErrSinkClosed) {
			t.Errorf("Expected ErrSinkClosed, got %v", err)
		}
	})

	t.Run("verifies metric values", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := &BenchmarkData{
			Name:       "test_metric_values",
			Duration:   2 * time.Second,
			Iterations: 500,
			Latency: LatencyData{
				Mean: 100 * time.Millisecond,
			},
			Throughput: ThroughputData{
				OpsPerSecond: 250.0,
			},
			ErrorCount: 10,
		}

		err := sink.RecordBenchmark(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordBenchmark failed: %v", err)
		}

		// Verify counter value
		iterationsCount := testutil.ToFloat64(sink.benchmarkIterations.WithLabelValues("test_metric_values"))
		if iterationsCount != 500 {
			t.Errorf("iterations count = %f, want 500", iterationsCount)
		}

		errorsCount := testutil.ToFloat64(sink.benchmarkErrors.WithLabelValues("test_metric_values"))
		if errorsCount != 10 {
			t.Errorf("errors count = %f, want 10", errorsCount)
		}
	})
}

// -----------------------------------------------------------------------------
// RecordComparison Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_RecordComparison(t *testing.T) {
	t.Run("records comparison metrics", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestComparisonData()
		err := sink.RecordComparison(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordComparison failed: %v", err)
		}

		// Verify metrics were recorded
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("Gather failed: %v", err)
		}

		metricNames := make(map[string]bool)
		for _, mf := range mfs {
			metricNames[mf.GetName()] = true
		}

		expectedMetrics := []string{
			"trace_eval_comparison_speedup_ratio",
			"trace_eval_comparison_p_value",
			"trace_eval_comparison_effect_size",
			"trace_eval_comparisons_total",
		}

		for _, name := range expectedMetrics {
			if !metricNames[name] {
				t.Errorf("Expected metric %s not found", name)
			}
		}
	})

	t.Run("handles no winner", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestComparisonData()
		data.Winner = ""
		data.Significant = false

		err := sink.RecordComparison(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordComparison failed: %v", err)
		}

		// Verify "none" label is used
		speedup := testutil.ToFloat64(sink.comparisonSpeedup.WithLabelValues("none"))
		if speedup != data.Speedup {
			t.Errorf("speedup = %f, want %f", speedup, data.Speedup)
		}
	})

	t.Run("verifies metric values", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := &ComparisonData{
			Winner:             "fast_impl",
			Speedup:            5.5,
			PValue:             0.01,
			EffectSize:         1.2,
			EffectSizeCategory: "large",
			Significant:        true,
		}

		err := sink.RecordComparison(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordComparison failed: %v", err)
		}

		speedup := testutil.ToFloat64(sink.comparisonSpeedup.WithLabelValues("fast_impl"))
		if speedup != 5.5 {
			t.Errorf("speedup = %f, want 5.5", speedup)
		}

		pvalue := testutil.ToFloat64(sink.comparisonPValue.WithLabelValues("fast_impl"))
		if pvalue != 0.01 {
			t.Errorf("p-value = %f, want 0.01", pvalue)
		}

		effectSize := testutil.ToFloat64(sink.comparisonEffectSize.WithLabelValues("fast_impl", "large"))
		if effectSize != 1.2 {
			t.Errorf("effect size = %f, want 1.2", effectSize)
		}

		total := testutil.ToFloat64(sink.comparisonTotal.WithLabelValues("true"))
		if total != 1 {
			t.Errorf("total = %f, want 1", total)
		}
	})

	t.Run("rejects nil context", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordComparison(nil, createTestComparisonData())
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("Expected ErrNilContext, got %v", err)
		}
	})

	t.Run("rejects nil data", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordComparison(context.Background(), nil)
		if !errors.Is(err, ErrNilData) {
			t.Errorf("Expected ErrNilData, got %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// RecordError Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_RecordError(t *testing.T) {
	t.Run("records error metrics", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := createTestErrorData()
		err := sink.RecordError(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordError failed: %v", err)
		}

		// Verify error counter was incremented
		count := testutil.ToFloat64(sink.errorsTotal.WithLabelValues(
			data.Component,
			data.Operation,
			data.ErrorType,
		))
		if count != 1 {
			t.Errorf("error count = %f, want 1", count)
		}
	})

	t.Run("handles empty labels", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		data := &ErrorData{
			Timestamp: time.Now(),
			// Empty component, operation, errorType
		}

		err := sink.RecordError(context.Background(), data)
		if err != nil {
			t.Fatalf("RecordError failed: %v", err)
		}

		// Should use "unknown" labels
		count := testutil.ToFloat64(sink.errorsTotal.WithLabelValues("unknown", "unknown", "unknown"))
		if count != 1 {
			t.Errorf("error count = %f, want 1", count)
		}
	})

	t.Run("rejects nil context", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordError(nil, createTestErrorData())
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("Expected ErrNilContext, got %v", err)
		}
	})

	t.Run("rejects nil data", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.RecordError(context.Background(), nil)
		if !errors.Is(err, ErrNilData) {
			t.Errorf("Expected ErrNilData, got %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// Flush and Close Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_Flush(t *testing.T) {
	t.Run("flush succeeds", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.Flush(context.Background())
		if err != nil {
			t.Errorf("Flush failed: %v", err)
		}
	})

	t.Run("rejects nil context", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		err := sink.Flush(nil)
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("Expected ErrNilContext, got %v", err)
		}
	})

	t.Run("returns error after close", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		sink.Close()

		err := sink.Flush(context.Background())
		if !errors.Is(err, ErrSinkClosed) {
			t.Errorf("Expected ErrSinkClosed, got %v", err)
		}
	})
}

func TestPrometheusSink_Close(t *testing.T) {
	t.Run("close succeeds", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)

		err := sink.Close()
		if err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})

	t.Run("close is idempotent", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)

		// Close multiple times
		sink.Close()
		sink.Close()
		err := sink.Close()
		if err != nil {
			t.Errorf("Close failed: %v", err)
		}
	})

	t.Run("unregisters metrics on custom registry", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)

		// Record some data
		sink.RecordBenchmark(context.Background(), createTestBenchmarkData())

		// Verify metrics exist
		mfs, _ := reg.Gather()
		if len(mfs) == 0 {
			t.Error("Expected metrics before close")
		}

		// Close and verify metrics are unregistered
		sink.Close()

		mfs, _ = reg.Gather()
		if len(mfs) != 0 {
			t.Errorf("Expected 0 metrics after close, got %d", len(mfs))
		}
	})
}

// -----------------------------------------------------------------------------
// Concurrent Access Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_Concurrent(t *testing.T) {
	t.Run("handles concurrent recording", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		var wg sync.WaitGroup
		iterations := 100

		for i := 0; i < iterations; i++ {
			wg.Add(3)

			go func() {
				defer wg.Done()
				sink.RecordBenchmark(context.Background(), createTestBenchmarkData())
			}()

			go func() {
				defer wg.Done()
				sink.RecordComparison(context.Background(), createTestComparisonData())
			}()

			go func() {
				defer wg.Done()
				sink.RecordError(context.Background(), createTestErrorData())
			}()
		}

		wg.Wait()

		// Verify all records were processed
		errCount := testutil.ToFloat64(sink.errorsTotal.WithLabelValues(
			"test_component",
			"benchmark",
			"timeout",
		))
		if int(errCount) != iterations {
			t.Errorf("error count = %d, want %d", int(errCount), iterations)
		}
	})

	t.Run("handles concurrent close and record", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)

		var wg sync.WaitGroup

		// Start recording
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// May succeed or fail with ErrSinkClosed
				sink.RecordBenchmark(context.Background(), createTestBenchmarkData())
			}()
		}

		// Close while recording
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.Close()
		}()

		wg.Wait()
	})
}

// -----------------------------------------------------------------------------
// Metric Output Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_MetricOutput(t *testing.T) {
	t.Run("produces valid prometheus output", func(t *testing.T) {
		reg := prometheus.NewRegistry()
		config := DefaultPrometheusConfig()
		config.Registry = reg
		sink, _ := NewPrometheusSink(config)
		defer sink.Close()

		// Record data
		sink.RecordBenchmark(context.Background(), createTestBenchmarkData())
		sink.RecordComparison(context.Background(), createTestComparisonData())
		sink.RecordError(context.Background(), createTestErrorData())

		// Get metric output
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatalf("Gather failed: %v", err)
		}

		// Verify we have multiple metric families
		if len(mfs) < 5 {
			t.Errorf("Expected at least 5 metric families, got %d", len(mfs))
		}

		// Verify metric naming convention
		for _, mf := range mfs {
			name := mf.GetName()
			if !strings.HasPrefix(name, "trace_eval_") {
				t.Errorf("Metric %s should have prefix trace_eval_", name)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Interface Compliance Tests
// -----------------------------------------------------------------------------

func TestPrometheusSink_InterfaceCompliance(t *testing.T) {
	var _ Sink = (*PrometheusSink)(nil)
}
