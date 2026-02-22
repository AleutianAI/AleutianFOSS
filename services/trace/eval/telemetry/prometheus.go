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
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	// ErrInvalidConfig is returned when the Prometheus configuration is invalid.
	ErrInvalidConfig = errors.New("invalid prometheus configuration")

	// ErrRegistrationFailed is returned when metric registration fails.
	ErrRegistrationFailed = errors.New("metric registration failed")
)

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

// PrometheusConfig configures the Prometheus sink.
//
// Description:
//
//	PrometheusConfig specifies namespace, subsystem, and bucket configuration
//	for Prometheus metrics.
//
// Thread Safety: Immutable after creation; safe for concurrent read access.
type PrometheusConfig struct {
	// Namespace is the metrics namespace (e.g., "trace").
	// Required.
	Namespace string

	// Subsystem is the metrics subsystem (e.g., "eval").
	// Required.
	Subsystem string

	// Registry is the Prometheus registry to use.
	// If nil, uses prometheus.DefaultRegisterer.
	Registry prometheus.Registerer

	// LatencyBuckets defines histogram buckets for latency metrics (seconds).
	// If nil, uses default buckets.
	LatencyBuckets []float64

	// ThroughputBuckets defines histogram buckets for throughput metrics (ops/sec).
	// If nil, uses default buckets.
	ThroughputBuckets []float64

	// MemoryBuckets defines histogram buckets for memory metrics (bytes).
	// If nil, uses default buckets.
	MemoryBuckets []float64

	// MaxLabelCardinality is the maximum number of unique label values to track.
	// When exceeded, new label values are mapped to "_other".
	// Default: 1000
	MaxLabelCardinality int
}

// DefaultPrometheusConfig returns a configuration with sensible defaults.
//
// Description:
//
//	Returns a PrometheusConfig with default namespace, subsystem, and buckets.
//
// Outputs:
//   - *PrometheusConfig: Configuration with defaults applied.
//
// Thread Safety: Stateless function; safe for concurrent use.
//
// Example:
//
//	config := telemetry.DefaultPrometheusConfig()
//	config.Namespace = "my_service"
//	sink, err := telemetry.NewPrometheusSink(config)
func DefaultPrometheusConfig() *PrometheusConfig {
	return &PrometheusConfig{
		Namespace: "trace",
		Subsystem: "eval",
		LatencyBuckets: []float64{
			0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0,
		},
		ThroughputBuckets: []float64{
			1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000,
		},
		MemoryBuckets: []float64{
			1024, 10240, 102400, 1048576, 10485760, 104857600, 1073741824,
		},
		MaxLabelCardinality: 1000,
	}
}

// Validate checks that the configuration is valid.
//
// Description:
//
//	Validates that required fields are set and bucket arrays are non-empty.
//
// Outputs:
//   - error: Non-nil if configuration is invalid.
//
// Thread Safety: Safe for concurrent use.
func (c *PrometheusConfig) Validate() error {
	if c.Namespace == "" {
		return errors.New("namespace is required")
	}
	if c.Subsystem == "" {
		return errors.New("subsystem is required")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Prometheus Sink
// -----------------------------------------------------------------------------

// PrometheusSink exports telemetry as Prometheus metrics.
//
// Description:
//
//	PrometheusSink collects benchmark, comparison, and error telemetry
//	and exposes them as Prometheus metrics. Metrics are registered on
//	creation and deregistered on Close().
//
// Thread Safety: Safe for concurrent use.
//
// Example:
//
//	sink, err := telemetry.NewPrometheusSink(telemetry.DefaultPrometheusConfig())
//	if err != nil {
//	    return fmt.Errorf("create prometheus sink: %w", err)
//	}
//	defer sink.Close()
//
//	sink.RecordBenchmark(ctx, data)
type PrometheusSink struct {
	config   *PrometheusConfig
	registry prometheus.Registerer

	// Benchmark metrics
	benchmarkDuration   *prometheus.HistogramVec
	benchmarkIterations *prometheus.CounterVec
	benchmarkLatency    *prometheus.HistogramVec
	benchmarkThroughput *prometheus.HistogramVec
	benchmarkMemory     *prometheus.HistogramVec
	benchmarkGCPauses   *prometheus.CounterVec
	benchmarkErrors     *prometheus.CounterVec

	// Comparison metrics
	comparisonSpeedup    *prometheus.GaugeVec
	comparisonPValue     *prometheus.GaugeVec
	comparisonEffectSize *prometheus.GaugeVec
	comparisonTotal      *prometheus.CounterVec

	// Error metrics
	errorsTotal *prometheus.CounterVec

	mu     sync.RWMutex
	closed bool

	// Track registered collectors for cleanup
	collectors []prometheus.Collector

	// Label cardinality protection
	labelMu        sync.RWMutex
	seenLabels     map[string]map[string]struct{} // labelName -> set of seen values
	maxCardinality int
}

// NewPrometheusSink creates a new Prometheus telemetry sink.
//
// Description:
//
//	Creates a sink that exports telemetry as Prometheus metrics.
//	Registers all metrics collectors on creation.
//
// Inputs:
//   - config: Prometheus configuration. Must not be nil.
//
// Outputs:
//   - *PrometheusSink: The created sink. Never nil on success.
//   - error: Non-nil if configuration is invalid or registration fails.
//
// Thread Safety: The returned sink is safe for concurrent use.
//
// Example:
//
//	config := telemetry.DefaultPrometheusConfig()
//	config.Namespace = "my_service"
//	sink, err := telemetry.NewPrometheusSink(config)
//	if err != nil {
//	    return fmt.Errorf("create sink: %w", err)
//	}
//
// Limitations:
//   - Metric names cannot be changed after creation.
//   - Uses global default registry if none specified.
//
// Assumptions:
//   - Registry allows duplicate registration (or collector not previously registered).
//   - Labels do not contain high-cardinality values.
func NewPrometheusSink(config *PrometheusConfig) (*PrometheusSink, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}
	if err := config.Validate(); err != nil {
		return nil, errors.Join(ErrInvalidConfig, err)
	}

	// Apply defaults for nil slices
	cfg := *config // Copy to avoid mutating input
	if cfg.LatencyBuckets == nil {
		cfg.LatencyBuckets = DefaultPrometheusConfig().LatencyBuckets
	}
	if cfg.ThroughputBuckets == nil {
		cfg.ThroughputBuckets = DefaultPrometheusConfig().ThroughputBuckets
	}
	if cfg.MemoryBuckets == nil {
		cfg.MemoryBuckets = DefaultPrometheusConfig().MemoryBuckets
	}

	registry := cfg.Registry
	if registry == nil {
		registry = prometheus.DefaultRegisterer
	}

	maxCard := cfg.MaxLabelCardinality
	if maxCard <= 0 {
		maxCard = 1000
	}

	sink := &PrometheusSink{
		config:         &cfg,
		registry:       registry,
		seenLabels:     make(map[string]map[string]struct{}),
		maxCardinality: maxCard,
	}

	// Initialize benchmark metrics
	sink.benchmarkDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_duration_seconds",
			Help:      "Total benchmark duration in seconds",
			Buckets:   cfg.LatencyBuckets,
		},
		[]string{"name"},
	)

	sink.benchmarkIterations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_iterations_total",
			Help:      "Total benchmark iterations",
		},
		[]string{"name"},
	)

	sink.benchmarkLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_latency_seconds",
			Help:      "Benchmark latency distribution in seconds",
			Buckets:   cfg.LatencyBuckets,
		},
		[]string{"name", "percentile"},
	)

	sink.benchmarkThroughput = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_throughput_ops_per_second",
			Help:      "Benchmark throughput in operations per second",
			Buckets:   cfg.ThroughputBuckets,
		},
		[]string{"name"},
	)

	sink.benchmarkMemory = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_memory_bytes",
			Help:      "Benchmark memory allocation in bytes",
			Buckets:   cfg.MemoryBuckets,
		},
		[]string{"name", "type"},
	)

	sink.benchmarkGCPauses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_gc_pauses_total",
			Help:      "Total GC pauses during benchmarks",
		},
		[]string{"name"},
	)

	sink.benchmarkErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "benchmark_errors_total",
			Help:      "Total errors during benchmarks",
		},
		[]string{"name"},
	)

	// Initialize comparison metrics
	sink.comparisonSpeedup = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "comparison_speedup_ratio",
			Help:      "Comparison speedup ratio (winner vs runner-up)",
		},
		[]string{"winner"},
	)

	sink.comparisonPValue = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "comparison_p_value",
			Help:      "Statistical p-value of comparison",
		},
		[]string{"winner"},
	)

	sink.comparisonEffectSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "comparison_effect_size",
			Help:      "Cohen's d effect size of comparison",
		},
		[]string{"winner", "category"},
	)

	sink.comparisonTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "comparisons_total",
			Help:      "Total comparisons performed",
		},
		[]string{"significant"},
	)

	// Initialize error metrics
	sink.errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: cfg.Namespace,
			Subsystem: cfg.Subsystem,
			Name:      "errors_total",
			Help:      "Total errors by type and component",
		},
		[]string{"component", "operation", "error_type"},
	)

	// Register all collectors
	sink.collectors = []prometheus.Collector{
		sink.benchmarkDuration,
		sink.benchmarkIterations,
		sink.benchmarkLatency,
		sink.benchmarkThroughput,
		sink.benchmarkMemory,
		sink.benchmarkGCPauses,
		sink.benchmarkErrors,
		sink.comparisonSpeedup,
		sink.comparisonPValue,
		sink.comparisonEffectSize,
		sink.comparisonTotal,
		sink.errorsTotal,
	}

	for _, c := range sink.collectors {
		if err := registry.Register(c); err != nil {
			// If already registered, try to continue
			var alreadyErr prometheus.AlreadyRegisteredError
			if !errors.As(err, &alreadyErr) {
				return nil, errors.Join(ErrRegistrationFailed, err)
			}
		}
	}

	return sink, nil
}

// RecordBenchmark records benchmark metrics.
//
// Description:
//
//	Records duration, iterations, latency percentiles, throughput,
//	memory allocation, GC pauses, and error counts from a benchmark run.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - data: Benchmark data to record. Must not be nil.
//
// Outputs:
//   - error: Non-nil if sink is closed or inputs are invalid.
//
// Thread Safety: Safe for concurrent use.
func (s *PrometheusSink) RecordBenchmark(ctx context.Context, data *BenchmarkData) error {
	if ctx == nil {
		return ErrNilContext
	}
	if data == nil {
		return ErrNilData
	}

	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSinkClosed
	}
	s.mu.RUnlock()

	name := data.Name
	if name == "" {
		name = "unknown"
	}
	name = s.sanitizeLabel("name", name)

	// Record duration
	s.benchmarkDuration.WithLabelValues(name).Observe(data.Duration.Seconds())

	// Record iterations
	s.benchmarkIterations.WithLabelValues(name).Add(float64(data.Iterations))

	// Record latency percentiles
	s.benchmarkLatency.WithLabelValues(name, "min").Observe(data.Latency.Min.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "max").Observe(data.Latency.Max.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "mean").Observe(data.Latency.Mean.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "p50").Observe(data.Latency.P50.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "p90").Observe(data.Latency.P90.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "p95").Observe(data.Latency.P95.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "p99").Observe(data.Latency.P99.Seconds())
	s.benchmarkLatency.WithLabelValues(name, "p999").Observe(data.Latency.P999.Seconds())

	// Record throughput
	s.benchmarkThroughput.WithLabelValues(name).Observe(data.Throughput.OpsPerSecond)

	// Record memory metrics if present
	if data.Memory != nil {
		s.benchmarkMemory.WithLabelValues(name, "heap_before").Observe(float64(data.Memory.HeapAllocBefore))
		s.benchmarkMemory.WithLabelValues(name, "heap_after").Observe(float64(data.Memory.HeapAllocAfter))
		s.benchmarkMemory.WithLabelValues(name, "heap_delta").Observe(float64(data.Memory.HeapAllocDelta))
		s.benchmarkGCPauses.WithLabelValues(name).Add(float64(data.Memory.GCPauses))
	}

	// Record errors
	if data.ErrorCount > 0 {
		s.benchmarkErrors.WithLabelValues(name).Add(float64(data.ErrorCount))
	}

	return nil
}

// RecordComparison records comparison metrics.
//
// Description:
//
//	Records speedup ratio, p-value, effect size, and comparison count.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - data: Comparison data to record. Must not be nil.
//
// Outputs:
//   - error: Non-nil if sink is closed or inputs are invalid.
//
// Thread Safety: Safe for concurrent use.
func (s *PrometheusSink) RecordComparison(ctx context.Context, data *ComparisonData) error {
	if ctx == nil {
		return ErrNilContext
	}
	if data == nil {
		return ErrNilData
	}

	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSinkClosed
	}
	s.mu.RUnlock()

	winner := data.Winner
	if winner == "" {
		winner = "none"
	}
	winner = s.sanitizeLabel("winner", winner)

	// Record speedup
	s.comparisonSpeedup.WithLabelValues(winner).Set(data.Speedup)

	// Record p-value
	s.comparisonPValue.WithLabelValues(winner).Set(data.PValue)

	// Record effect size
	category := data.EffectSizeCategory
	if category == "" {
		category = "unknown"
	}
	category = s.sanitizeLabel("category", category)
	s.comparisonEffectSize.WithLabelValues(winner, category).Set(data.EffectSize)

	// Record comparison count
	significant := "false"
	if data.Significant {
		significant = "true"
	}
	s.comparisonTotal.WithLabelValues(significant).Inc()

	return nil
}

// RecordError records error metrics.
//
// Description:
//
//	Increments the error counter with component, operation, and error type labels.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - data: Error data to record. Must not be nil.
//
// Outputs:
//   - error: Non-nil if sink is closed or inputs are invalid.
//
// Thread Safety: Safe for concurrent use.
func (s *PrometheusSink) RecordError(ctx context.Context, data *ErrorData) error {
	if ctx == nil {
		return ErrNilContext
	}
	if data == nil {
		return ErrNilData
	}

	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return ErrSinkClosed
	}
	s.mu.RUnlock()

	component := data.Component
	if component == "" {
		component = "unknown"
	}
	component = s.sanitizeLabel("component", component)

	operation := data.Operation
	if operation == "" {
		operation = "unknown"
	}
	operation = s.sanitizeLabel("operation", operation)

	errorType := data.ErrorType
	if errorType == "" {
		errorType = "unknown"
	}
	errorType = s.sanitizeLabel("error_type", errorType)

	s.errorsTotal.WithLabelValues(component, operation, errorType).Inc()

	return nil
}

// Flush is a no-op for Prometheus sink.
//
// Description:
//
//	Prometheus metrics are available immediately via scraping.
//	This method exists for interface compliance.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//   - error: Non-nil if sink is closed or context is nil.
//
// Thread Safety: Safe for concurrent use.
func (s *PrometheusSink) Flush(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return ErrSinkClosed
	}

	// Prometheus metrics are pull-based, no explicit flush needed
	return nil
}

// Close unregisters all metrics and releases resources.
//
// Description:
//
//	Unregisters all Prometheus collectors from the registry.
//	After Close(), all recording methods return ErrSinkClosed.
//
// Outputs:
//   - error: Non-nil if unregistration fails.
//
// Thread Safety: Safe for concurrent use. Idempotent.
func (s *PrometheusSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	// Unregister collectors if using a registry that supports it
	if unregisterer, ok := s.registry.(prometheus.Registerer); ok {
		// Note: DefaultRegisterer doesn't support Unregister, so we check
		// if it's a custom registry that might support unregistration
		if gatherer, ok := unregisterer.(*prometheus.Registry); ok {
			for _, c := range s.collectors {
				gatherer.Unregister(c)
			}
		}
	}

	return nil
}

// sanitizeLabel protects against label cardinality explosion.
//
// Description:
//
//	Tracks unique label values per label name and replaces values
//	beyond MaxLabelCardinality with "_other".
//
// Thread Safety: Safe for concurrent use.
func (s *PrometheusSink) sanitizeLabel(labelName, labelValue string) string {
	s.labelMu.RLock()
	seen := s.seenLabels[labelName]
	if seen != nil {
		if _, exists := seen[labelValue]; exists {
			s.labelMu.RUnlock()
			return labelValue
		}
		if len(seen) >= s.maxCardinality {
			s.labelMu.RUnlock()
			return "_other"
		}
	}
	s.labelMu.RUnlock()

	// Need to add new value
	s.labelMu.Lock()
	defer s.labelMu.Unlock()

	// Double-check after acquiring write lock
	if s.seenLabels[labelName] == nil {
		s.seenLabels[labelName] = make(map[string]struct{})
	}
	if _, exists := s.seenLabels[labelName][labelValue]; exists {
		return labelValue
	}
	if len(s.seenLabels[labelName]) >= s.maxCardinality {
		return "_other"
	}

	s.seenLabels[labelName][labelValue] = struct{}{}
	return labelValue
}

// Verify interface compliance at compile time.
var _ Sink = (*PrometheusSink)(nil)
