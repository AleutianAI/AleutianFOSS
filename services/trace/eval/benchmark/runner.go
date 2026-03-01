// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package benchmark

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "trace.eval.benchmark"

// -----------------------------------------------------------------------------
// Runner Options
// -----------------------------------------------------------------------------

// RunOption configures a benchmark run.
//
// Description:
//
//	RunOption functions modify the benchmark Config. They are applied
//	in order, so later options override earlier ones.
type RunOption func(*Config)

// WithIterations sets the number of benchmark iterations.
//
// Description:
//
//	Configures the number of measured iterations to run. More iterations
//	provide more accurate statistics but take longer.
//
// Inputs:
//   - n: Number of iterations. Must be positive; non-positive values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithIterations(10000))
func WithIterations(n int) RunOption {
	return func(c *Config) {
		if n > 0 {
			c.Iterations = n
		}
	}
}

// WithWarmup sets the number of warmup iterations.
//
// Description:
//
//	Warmup iterations run before measurement to allow JIT, caches, and
//	other runtime optimizations to stabilize. Results are discarded.
//
// Inputs:
//   - n: Number of warmup iterations. Must be non-negative; negative values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithWarmup(1000))
func WithWarmup(n int) RunOption {
	return func(c *Config) {
		if n >= 0 {
			c.Warmup = n
		}
	}
}

// WithCooldown sets the cooldown duration between warmup and measurement.
//
// Description:
//
//	Cooldown allows the system to settle after warmup before measurement
//	begins. Useful for letting GC complete and caches stabilize.
//
// Inputs:
//   - d: Cooldown duration. Must be non-negative; negative values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithCooldown(500*time.Millisecond))
func WithCooldown(d time.Duration) RunOption {
	return func(c *Config) {
		if d >= 0 {
			c.Cooldown = d
		}
	}
}

// WithTimeout sets the total benchmark timeout.
//
// Description:
//
//	Sets the maximum time for the entire benchmark run including warmup,
//	cooldown, and all iterations. The benchmark will stop early if exceeded.
//
// Inputs:
//   - d: Total timeout. Must be positive; non-positive values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithTimeout(10*time.Minute))
func WithTimeout(d time.Duration) RunOption {
	return func(c *Config) {
		if d > 0 {
			c.Timeout = d
		}
	}
}

// WithIterationTimeout sets the per-iteration timeout.
//
// Description:
//
//	Sets the maximum time for a single iteration. Iterations exceeding
//	this timeout are counted as errors.
//
// Inputs:
//   - d: Per-iteration timeout. Must be positive; non-positive values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithIterationTimeout(5*time.Second))
func WithIterationTimeout(d time.Duration) RunOption {
	return func(c *Config) {
		if d > 0 {
			c.IterationTimeout = d
		}
	}
}

// WithMemoryCollection enables or disables memory statistics collection.
//
// Description:
//
//	When enabled, the runner collects heap allocation statistics before
//	and after the benchmark. Disabling can slightly reduce overhead.
//
// Inputs:
//   - enabled: Whether to collect memory statistics.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithMemoryCollection(false))
func WithMemoryCollection(enabled bool) RunOption {
	return func(c *Config) {
		c.CollectMemory = enabled
	}
}

// WithOutlierRemoval enables or disables outlier removal.
//
// Description:
//
//	When enabled, statistical outliers are removed before computing
//	latency statistics. Uses the IQR method with the configured threshold.
//
// Inputs:
//   - enabled: Whether to remove outliers.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithOutlierRemoval(false))
func WithOutlierRemoval(enabled bool) RunOption {
	return func(c *Config) {
		c.RemoveOutliers = enabled
	}
}

// WithOutlierThreshold sets the IQR threshold for outlier detection.
//
// Description:
//
//	Values outside [Q1 - threshold*IQR, Q3 + threshold*IQR] are considered
//	outliers. Common values: 1.5 (mild), 3.0 (extreme).
//
// Inputs:
//   - threshold: IQR multiplier. Must be positive; non-positive values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithOutlierThreshold(3.0))
func WithOutlierThreshold(threshold float64) RunOption {
	return func(c *Config) {
		if threshold > 0 {
			c.OutlierThreshold = threshold
		}
	}
}

// WithParallelism sets the number of concurrent benchmark goroutines.
//
// Description:
//
//	Runs multiple benchmark iterations concurrently. Useful for testing
//	thread safety and measuring contention. Note that parallel execution
//	may increase variance.
//
// Inputs:
//   - n: Number of concurrent goroutines. Must be positive; non-positive values are ignored.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithParallelism(runtime.NumCPU()))
func WithParallelism(n int) RunOption {
	return func(c *Config) {
		if n > 0 {
			c.Parallelism = n
		}
	}
}

// WithInputGenerator sets a custom input generator.
//
// Description:
//
//	Provides a function that generates input for each benchmark iteration.
//	If not set, the component's property generator is used, or nil input.
//
// Inputs:
//   - gen: Function that generates input. Can return any value.
//
// Example:
//
//	runner.Run(ctx, "algo", benchmark.WithInputGenerator(func() any {
//	    return generateRandomInput(100)
//	}))
func WithInputGenerator(gen func() any) RunOption {
	return func(c *Config) {
		c.InputGenerator = gen
	}
}

// -----------------------------------------------------------------------------
// Runner
// -----------------------------------------------------------------------------

// Runner executes benchmarks against evaluable components.
//
// Description:
//
//	Runner provides the core benchmarking functionality, executing
//	iterations against registered components, collecting statistics,
//	and enabling comparisons between implementations.
//
// Thread Safety: Safe for concurrent use.
type Runner struct {
	registry *eval.Registry
	logger   *slog.Logger
}

// NewRunner creates a new benchmark runner.
//
// Description:
//
//	Creates a runner that benchmarks components from the given registry.
//	The runner uses slog.Default() for logging; use SetLogger to override.
//
// Inputs:
//   - registry: The registry of evaluable components. Must not be nil.
//
// Outputs:
//   - *Runner: The new runner. Never nil.
//
// Example:
//
//	registry := eval.NewRegistry()
//	registry.MustRegister(myAlgorithm)
//	runner := benchmark.NewRunner(registry)
//
// Assumptions:
//   - Registry is initialized and will not be nil.
func NewRunner(registry *eval.Registry) *Runner {
	return &Runner{
		registry: registry,
		logger:   slog.Default(),
	}
}

// SetLogger sets the logger for the runner.
//
// Description:
//
//	Replaces the runner's logger. Nil values are ignored.
//
// Inputs:
//   - logger: The logger to use. If nil, the current logger is retained.
//
// Thread Safety: Safe for concurrent use.
func (r *Runner) SetLogger(logger *slog.Logger) {
	if logger != nil {
		r.logger = logger
	}
}

// Run executes a benchmark for a single component.
//
// Description:
//
//	Executes a complete benchmark run including warmup, cooldown, and
//	measured iterations. Computes comprehensive statistics from the results.
//
// Inputs:
//   - ctx: Context for cancellation and timeout. Must not be nil.
//   - name: The name of the component to benchmark. Must be registered.
//   - opts: Optional configuration options.
//
// Outputs:
//   - *Result: The benchmark results with statistics. Never nil on success.
//   - error: Non-nil if benchmark could not be run. Wraps underlying error.
//
// Thread Safety: Safe for concurrent use.
//
// Example:
//
//	result, err := runner.Run(ctx, "cdcl",
//	    benchmark.WithIterations(10000),
//	    benchmark.WithWarmup(1000),
//	)
//	if err != nil {
//	    return fmt.Errorf("benchmark cdcl: %w", err)
//	}
//	fmt.Printf("P99: %v, Ops/sec: %.2f\n", result.Latency.P99, result.Throughput.OpsPerSecond)
//
// Limitations:
//   - Uses component's HealthCheck as the benchmarked operation.
//   - Memory statistics may be affected by concurrent operations.
//
// Assumptions:
//   - Component is properly registered and its HealthCheck is meaningful.
func (r *Runner) Run(ctx context.Context, name string, opts ...RunOption) (*Result, error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}

	// Start trace span
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "benchmark.Runner.Run",
		trace.WithAttributes(
			attribute.String("benchmark.component", name),
		),
	)
	defer span.End()

	component, ok := r.registry.Get(name)
	if !ok {
		span.RecordError(eval.ErrNotFound)
		span.SetStatus(codes.Error, "component not found")
		return nil, fmt.Errorf("getting component %s: %w", name, eval.ErrNotFound)
	}

	config := DefaultConfig()
	for _, opt := range opts {
		opt(config)
	}

	if err := config.Validate(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid config")
		return nil, fmt.Errorf("validating config: %w", ErrInvalidConfig)
	}

	// Add config attributes to span
	span.SetAttributes(
		attribute.Int("benchmark.iterations", config.Iterations),
		attribute.Int("benchmark.warmup", config.Warmup),
		attribute.Int("benchmark.parallelism", config.Parallelism),
	)

	// Set up timeout context
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// Get input generator
	generator := config.InputGenerator
	if generator == nil {
		// Use component's first property generator if available
		properties := component.Properties()
		if len(properties) > 0 && properties[0].Generator != nil {
			generator = properties[0].Generator
		} else {
			generator = func() any { return nil }
		}
	}

	// Collect memory stats before
	var memBefore runtime.MemStats
	if config.CollectMemory {
		runtime.GC()
		runtime.ReadMemStats(&memBefore)
	}

	// Run warmup
	if err := r.runWarmup(ctx, component, generator, config); err != nil {
		return nil, fmt.Errorf("running warmup: %w", err)
	}

	// Cooldown
	if config.Cooldown > 0 {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cooldown interrupted: %w", ctx.Err())
		case <-time.After(config.Cooldown):
		}
	}

	// Run measurement iterations
	samples, errorCount, err := r.runMeasurement(ctx, component, generator, config)
	if err != nil {
		return nil, fmt.Errorf("running measurement: %w", err)
	}

	if len(samples) == 0 {
		return nil, fmt.Errorf("no successful iterations: %w", ErrBenchmarkFailed)
	}

	// Collect memory stats after
	var memAfter runtime.MemStats
	if config.CollectMemory {
		runtime.ReadMemStats(&memAfter)
	}

	// Build result
	result := r.buildResult(name, samples, errorCount, config, &memBefore, &memAfter)

	// Record result in span
	span.SetAttributes(
		attribute.Int("benchmark.result.iterations", result.Iterations),
		attribute.Int("benchmark.result.errors", result.Errors),
		attribute.Float64("benchmark.result.ops_per_second", result.Throughput.OpsPerSecond),
		attribute.Int64("benchmark.result.p99_ns", int64(result.Latency.P99)),
	)
	span.SetStatus(codes.Ok, "benchmark completed")

	return result, nil
}

// runWarmup executes warmup iterations.
func (r *Runner) runWarmup(ctx context.Context, component eval.Evaluable, generator func() any, config *Config) error {
	for i := 0; i < config.Warmup; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		input := generator()
		_, _ = r.runIteration(ctx, component, input, config.IterationTimeout)
	}
	return nil
}

// runMeasurement executes measurement iterations and returns samples.
func (r *Runner) runMeasurement(ctx context.Context, component eval.Evaluable, generator func() any, config *Config) ([]time.Duration, int, error) {
	samples := make([]time.Duration, 0, config.Iterations)
	var errorCount int
	var mu sync.Mutex

	if config.Parallelism <= 1 {
		// Sequential execution
		for i := 0; i < config.Iterations; i++ {
			select {
			case <-ctx.Done():
				return samples, errorCount, nil
			default:
			}
			input := generator()
			duration, err := r.runIteration(ctx, component, input, config.IterationTimeout)
			if err != nil {
				errorCount++
			} else {
				samples = append(samples, duration)
			}
		}
	} else {
		// Parallel execution
		sem := make(chan struct{}, config.Parallelism)
		var wg sync.WaitGroup

		for i := 0; i < config.Iterations; i++ {
			select {
			case <-ctx.Done():
				break
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				input := generator()
				duration, err := r.runIteration(ctx, component, input, config.IterationTimeout)

				mu.Lock()
				if err != nil {
					errorCount++
				} else {
					samples = append(samples, duration)
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
	}

	return samples, errorCount, nil
}

// runIteration runs a single benchmark iteration.
func (r *Runner) runIteration(ctx context.Context, component eval.Evaluable, input any, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// For benchmarking, we use the component's health check as a proxy
	// In a real implementation, this would call a specific benchmark method
	start := time.Now()

	// Run health check as the benchmark operation
	// Components can override this for more meaningful benchmarks
	err := component.HealthCheck(ctx)

	return time.Since(start), err
}

// buildResult constructs the Result from collected data.
func (r *Runner) buildResult(name string, samples []time.Duration, errorCount int, config *Config, memBefore, memAfter *runtime.MemStats) *Result {
	totalIterations := len(samples) + errorCount
	var errorRate float64
	if totalIterations > 0 {
		errorRate = float64(errorCount) / float64(totalIterations)
	}

	result := &Result{
		Name:       name,
		Iterations: totalIterations,
		Timestamp:  time.Now().UnixMilli(),
		Config:     config,
		RawSamples: samples,
		Errors:     errorCount,
		ErrorRate:  errorRate,
	}

	// Remove outliers if enabled
	if config.RemoveOutliers && len(samples) > 4 {
		result.Samples = RemoveOutliers(samples, config.OutlierThreshold)
		if removed := len(samples) - len(result.Samples); removed > 0 {
			r.logger.Debug("outliers removed from benchmark",
				slog.String("component", name),
				slog.Int("original_count", len(samples)),
				slog.Int("remaining_count", len(result.Samples)),
				slog.Int("removed_count", removed),
				slog.Float64("removal_rate", float64(removed)/float64(len(samples))),
			)
		}
	} else {
		result.Samples = samples
	}

	// Calculate latency statistics
	latencyStats, err := CalculateLatencyStats(result.Samples)
	if err == nil {
		result.Latency = latencyStats
	}

	// Calculate total duration
	for _, s := range result.Samples {
		result.TotalDuration += s
	}

	// Calculate throughput
	if result.TotalDuration > 0 {
		result.Throughput.OpsPerSecond = float64(len(result.Samples)) / result.TotalDuration.Seconds()
	}

	// Calculate memory statistics
	if config.CollectMemory && memBefore != nil && memAfter != nil {
		result.Memory = &MemoryStats{
			HeapAllocBefore: memBefore.HeapAlloc,
			HeapAllocAfter:  memAfter.HeapAlloc,
			HeapAllocDelta:  int64(memAfter.HeapAlloc) - int64(memBefore.HeapAlloc),
			GCPauses:        memAfter.NumGC - memBefore.NumGC,
		}
		if memAfter.PauseTotalNs > memBefore.PauseTotalNs {
			result.Memory.GCPauseTotal = time.Duration(memAfter.PauseTotalNs - memBefore.PauseTotalNs)
		}
	}

	return result
}

// Compare runs benchmarks for multiple components and compares results.
//
// Description:
//
//	Runs benchmarks for each component with the same configuration,
//	then performs statistical comparison to determine if there's a
//	significant difference. Uses Welch's t-test and Cohen's d.
//
// Inputs:
//   - ctx: Context for cancellation and timeout. Must not be nil.
//   - names: Names of components to compare. Must have at least 2.
//   - opts: Optional configuration options (applied to all benchmarks).
//
// Outputs:
//   - *ComparisonResult: The comparison results with statistical analysis.
//   - error: Non-nil if comparison could not be performed. Wraps underlying error.
//
// Thread Safety: Safe for concurrent use.
//
// Example:
//
//	comparison, err := runner.Compare(ctx,
//	    []string{"cdcl_v1", "cdcl_v2"},
//	    benchmark.WithIterations(10000),
//	)
//	if err != nil {
//	    return fmt.Errorf("comparing algorithms: %w", err)
//	}
//	if comparison.Winner != "" {
//	    fmt.Printf("%s is %.2fx faster (p=%.4f)\n",
//	        comparison.Winner, comparison.Speedup, comparison.PValue)
//	}
//
// Assumptions:
//   - All named components are registered and comparable.
func (r *Runner) Compare(ctx context.Context, names []string, opts ...RunOption) (*ComparisonResult, error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}
	if len(names) < 2 {
		return nil, errors.New("comparison requires at least 2 components")
	}

	// Start trace span
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "benchmark.Runner.Compare",
		trace.WithAttributes(
			attribute.StringSlice("benchmark.components", names),
		),
	)
	defer span.End()

	results := make(map[string]*Result)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(names))

	// Run benchmarks in parallel
	for _, name := range names {
		wg.Add(1)
		go func(componentName string) {
			defer wg.Done()

			result, err := r.Run(ctx, componentName, opts...)
			if err != nil {
				errCh <- fmt.Errorf("benchmarking %s: %w", componentName, err)
				return
			}

			mu.Lock()
			results[componentName] = result
			mu.Unlock()
		}(name)
	}

	// Wait for all benchmarks to complete
	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		span.RecordError(err)
		span.SetStatus(codes.Error, "benchmark failed")
		return nil, err
	}

	// Build comparison result
	comparison := r.buildComparison(results)

	span.SetAttributes(
		attribute.String("benchmark.winner", comparison.Winner),
		attribute.Float64("benchmark.speedup", comparison.Speedup),
		attribute.Bool("benchmark.significant", comparison.Significant),
	)
	span.SetStatus(codes.Ok, "comparison completed")

	return comparison, nil
}

// buildComparison constructs the ComparisonResult from individual results.
func (r *Runner) buildComparison(results map[string]*Result) *ComparisonResult {
	comparison := &ComparisonResult{
		Results:         results,
		ConfidenceLevel: 0.95,
	}

	// Rank by mean latency
	type ranked struct {
		name string
		mean time.Duration
	}
	var rankings []ranked
	for name, result := range results {
		rankings = append(rankings, ranked{name: name, mean: result.Latency.Mean})
	}
	sort.Slice(rankings, func(i, j int) bool {
		return rankings[i].mean < rankings[j].mean
	})

	comparison.Ranking = make([]string, len(rankings))
	for i, ranking := range rankings {
		comparison.Ranking[i] = ranking.name
	}

	// Compare fastest vs slowest
	if len(rankings) >= 2 {
		fastest := rankings[0]
		slowest := rankings[len(rankings)-1]

		fastestResult := results[fastest.name]
		slowestResult := results[slowest.name]

		// Statistical test
		_, pValue := WelchTTest(fastestResult.Samples, slowestResult.Samples)
		comparison.PValue = pValue
		comparison.Significant = pValue < (1 - comparison.ConfidenceLevel)

		// Effect size
		comparison.EffectSize = CalculateCohensD(fastestResult.Samples, slowestResult.Samples)
		comparison.EffectSizeCategory = CategorizeEffectSize(comparison.EffectSize)

		// Speedup
		if fastest.mean > 0 {
			comparison.Speedup = float64(slowest.mean) / float64(fastest.mean)
		}

		// Declare winner if significant
		if comparison.Significant {
			comparison.Winner = fastest.name
		}
	}

	return comparison
}

// RunAll runs benchmarks for all registered components.
//
// Description:
//
//	Runs benchmarks for every component in the registry. Components
//	that fail are logged and skipped; the runner continues with others.
//
// Inputs:
//   - ctx: Context for cancellation and timeout. Must not be nil.
//   - opts: Optional configuration options (applied to all benchmarks).
//
// Outputs:
//   - []*Result: Results for all successfully benchmarked components.
//   - error: Non-nil only if benchmarking could not be started (nil context).
//
// Thread Safety: Safe for concurrent use.
//
// Example:
//
//	results, err := runner.RunAll(ctx, benchmark.WithIterations(1000))
//	if err != nil {
//	    return fmt.Errorf("running all benchmarks: %w", err)
//	}
//	for _, result := range results {
//	    fmt.Printf("%s: P99=%v\n", result.Name, result.Latency.P99)
//	}
func (r *Runner) RunAll(ctx context.Context, opts ...RunOption) ([]*Result, error) {
	if ctx == nil {
		return nil, errors.New("context must not be nil")
	}

	names := r.registry.List()
	results := make([]*Result, 0, len(names))

	for _, name := range names {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		result, err := r.Run(ctx, name, opts...)
		if err != nil {
			r.logger.Warn("benchmark failed",
				slog.String("component", name),
				slog.String("error", err.Error()),
			)
			continue
		}

		results = append(results, result)
	}

	return results, nil
}
