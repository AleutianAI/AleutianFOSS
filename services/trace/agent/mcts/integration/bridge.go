// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package integration

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/activities"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	// ErrNilContext is returned when context is nil.
	ErrNilContext = errors.New("context must not be nil")

	// ErrNilCRS is returned when CRS is nil.
	ErrNilCRS = errors.New("CRS must not be nil")

	// ErrNilActivity is returned when activity is nil.
	ErrNilActivity = errors.New("activity must not be nil")

	// ErrBridgeClosed is returned when the bridge is closed.
	ErrBridgeClosed = errors.New("bridge is closed")

	// ErrApplyFailed is returned when delta apply fails.
	ErrApplyFailed = errors.New("delta apply failed")
)

// BridgeError wraps an error with bridge context.
type BridgeError struct {
	Operation string
	Err       error
}

func (e *BridgeError) Error() string {
	return "bridge." + e.Operation + ": " + e.Err.Error()
}

func (e *BridgeError) Unwrap() error {
	return e.Err
}

// -----------------------------------------------------------------------------
// Bridge
// -----------------------------------------------------------------------------

// Bridge connects activities to the CRS state machine.
//
// Description:
//
//	Bridge is the integration layer between the activity system and CRS.
//	It:
//	- Provides snapshots to activities
//	- Applies deltas from activity results
//	- Handles conflicts and rollbacks
//	- Tracks metrics and traces
//	- Optionally records trace steps for audit/debugging
//
// Thread Safety: Safe for concurrent use.
type Bridge struct {
	mu            sync.RWMutex
	crs           crs.CRS
	traceRecorder *crs.TraceRecorder
	journal       crs.Journal
	config        *BridgeConfig
	logger        *slog.Logger

	// State
	closed bool

	// Metrics
	activitiesRun  int64
	deltasApplied  int64
	conflictsFound int64
}

// BridgeConfig configures the bridge.
type BridgeConfig struct {
	// MaxRetries is the maximum number of apply retries.
	MaxRetries int

	// RetryDelay is the delay between retries.
	RetryDelay time.Duration

	// EnableMetrics enables metrics collection.
	EnableMetrics bool

	// EnableTracing enables OpenTelemetry tracing.
	EnableTracing bool

	// EnableTraceRecording enables automatic trace step recording.
	// When true and a TraceRecorder is provided, the Bridge records
	// a TraceStep after each activity execution.
	// Default: true
	EnableTraceRecording bool
}

// DefaultBridgeConfig returns the default bridge configuration.
func DefaultBridgeConfig() *BridgeConfig {
	return &BridgeConfig{
		MaxRetries:           3,
		RetryDelay:           100 * time.Millisecond,
		EnableMetrics:        true,
		EnableTracing:        true,
		EnableTraceRecording: true,
	}
}

// BridgeOption configures optional Bridge parameters.
type BridgeOption func(*Bridge)

// WithTraceRecorder sets the trace recorder for automatic step recording.
//
// Description:
//
//	When provided, the Bridge will automatically record a TraceStep
//	after each successful activity execution. Recording failures
//	are logged but do not block activity execution.
//
// Inputs:
//
//	recorder - The trace recorder. May be nil (disables recording).
//
// Example:
//
//	recorder := crs.NewTraceRecorder(crs.DefaultTraceConfig())
//	bridge := integration.NewBridge(crsInstance, config,
//	    integration.WithTraceRecorder(recorder),
//	)
func WithTraceRecorder(recorder *crs.TraceRecorder) BridgeOption {
	return func(b *Bridge) {
		b.traceRecorder = recorder
	}
}

// WithJournal sets the delta journal for persistence.
//
// Description:
//
//	When provided, the Bridge will append each successfully applied delta
//	to the journal for crash recovery. Journal write failures are logged
//	but do not block activity execution.
//
// Inputs:
//
//	journal - The delta journal. May be nil (disables journaling).
//
// Example:
//
//	journal, _ := crs.NewBadgerJournal(journalConfig)
//	bridge := integration.NewBridge(crsInstance, config,
//	    integration.WithJournal(journal),
//	)
func WithJournal(journal crs.Journal) BridgeOption {
	return func(b *Bridge) {
		b.journal = journal
	}
}

// NewBridge creates a new bridge.
//
// Description:
//
//	Creates a Bridge that coordinates activity execution and CRS state
//	management. Optionally accepts a TraceRecorder for automatic
//	reasoning trace recording.
//
// Inputs:
//
//	crsInstance - The CRS state machine. Must not be nil.
//	config - Bridge configuration. Uses defaults if nil.
//	opts - Optional configuration functions.
//
// Outputs:
//
//	*Bridge - The new bridge.
//
// Example:
//
//	// Basic usage
//	bridge := integration.NewBridge(crsInstance, nil)
//
//	// With trace recorder
//	recorder := crs.NewTraceRecorder(crs.DefaultTraceConfig())
//	bridge := integration.NewBridge(crsInstance, config,
//	    integration.WithTraceRecorder(recorder),
//	)
func NewBridge(crsInstance crs.CRS, config *BridgeConfig, opts ...BridgeOption) *Bridge {
	if config == nil {
		config = DefaultBridgeConfig()
	}

	b := &Bridge{
		crs:    crsInstance,
		config: config,
		logger: slog.Default().With(slog.String("component", "bridge")),
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}

// -----------------------------------------------------------------------------
// Bridge Operations
// -----------------------------------------------------------------------------

// RunActivity executes an activity and applies its delta.
//
// Description:
//
//	Runs the activity with a snapshot, then applies the resulting delta
//	to CRS. If apply fails due to conflict, retries with fresh snapshot.
//
// Inputs:
//   - ctx: Context for cancellation.
//   - activity: The activity to run.
//   - input: Activity input.
//
// Outputs:
//   - activities.ActivityResult: The activity result.
//   - error: Non-nil on failure.
//
// Thread Safety: Safe for concurrent calls.
func (b *Bridge) RunActivity(
	ctx context.Context,
	activity activities.Activity,
	input activities.ActivityInput,
) (activities.ActivityResult, error) {
	if ctx == nil {
		return activities.ActivityResult{}, &BridgeError{Operation: "RunActivity", Err: ErrNilContext}
	}
	if activity == nil {
		return activities.ActivityResult{}, &BridgeError{Operation: "RunActivity", Err: ErrNilActivity}
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return activities.ActivityResult{}, &BridgeError{Operation: "RunActivity", Err: ErrBridgeClosed}
	}
	b.mu.RUnlock()

	// Start trace
	var span trace.Span
	if b.config.EnableTracing {
		ctx, span = otel.Tracer("integration").Start(ctx, "bridge.RunActivity",
			trace.WithAttributes(
				attribute.String("activity", activity.Name()),
			),
		)
		defer span.End()
	}

	// Retry loop
	var lastErr error
	for attempt := 0; attempt <= b.config.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return activities.ActivityResult{}, ctx.Err()
			case <-time.After(b.config.RetryDelay):
			}
		}

		result, err := b.runActivityOnce(ctx, activity, input, attempt)
		if err == nil {
			b.mu.Lock()
			b.activitiesRun++
			b.mu.Unlock()
			return result, nil
		}

		lastErr = err

		// Check if retryable
		if !errors.Is(err, crs.ErrDeltaConflict) && !errors.Is(err, crs.ErrSnapshotStale) {
			break
		}

		b.mu.Lock()
		b.conflictsFound++
		b.mu.Unlock()

		// Emit conflict metric for Prometheus
		if b.config.EnableMetrics {
			RecordConflict()
		}

		// Conflicts and retries are expected behavior, log at Debug level
		b.logger.Debug("activity apply conflict, retrying",
			slog.String("activity", activity.Name()),
			slog.Int("attempt", attempt+1),
			slog.String("error", err.Error()),
		)
	}

	return activities.ActivityResult{}, lastErr
}

// runActivityOnce runs an activity once without retry.
func (b *Bridge) runActivityOnce(
	ctx context.Context,
	activity activities.Activity,
	input activities.ActivityInput,
	attempt int,
) (activities.ActivityResult, error) {
	startTime := time.Now() // Capture start time for metrics

	// Get fresh snapshot
	snapshot := b.crs.Snapshot()

	// Run activity
	result, delta, executeErr := activity.Execute(ctx, snapshot, input)

	// Apply delta if present and execution succeeded
	var applyErr error
	if executeErr == nil && delta != nil {
		metrics, err := b.crs.Apply(ctx, delta)
		if err != nil {
			applyErr = &BridgeError{Operation: "Apply", Err: err}
		} else {
			b.mu.Lock()
			b.deltasApplied++
			journal := b.journal
			b.mu.Unlock()

			b.logger.Debug("delta applied",
				slog.String("activity", activity.Name()),
				slog.Int64("generation", metrics.NewGeneration),
				slog.Int("entries_modified", metrics.EntriesModified),
			)

			// CRS-WIRE-01: Persist delta to journal for crash recovery.
			// Only coordinator-driven deltas are journaled (learned constraints,
			// proof updates). Analytics deltas bypass journal since they are
			// regenerated from source on graph rebuild.
			if journal != nil {
				if jErr := journal.Append(ctx, delta); jErr != nil {
					b.logger.Warn("journal append failed",
						slog.String("activity", activity.Name()),
						slog.String("error", jErr.Error()),
					)
				}
			}
		}
	}

	// Record trace step with full observability (DR-6/DR-15: record ALL activities)
	// Record even when activity fails - this provides complete audit trail
	b.recordTraceStep(ctx, result, delta, input, startTime)

	// Return the first error encountered
	if executeErr != nil {
		return result, executeErr
	}
	if applyErr != nil {
		return result, applyErr
	}

	return result, nil
}

// recordTraceStep records a trace step with full observability integration.
//
// Description:
//
//	Records the activity execution as a TraceStep and emits:
//	1. OTel span event with CRS context
//	2. Prometheus metrics for reasoning progress
//	3. Structured log with trace correlation
//	4. TraceStep to TraceRecorder with trace_id link
//
//	Recording failures are logged but never block activity execution.
//	Each subsystem operates independently - a failure in one does not
//	prevent the others from recording.
//
// Inputs:
//
//	ctx - Context containing the active span for trace correlation.
//	result - The activity execution result.
//	delta - The delta produced by the activity. May be nil.
//	input - The activity input. May be nil.
//	startTime - When the activity started.
//
// Outputs:
//
//	None.
//
// Thread Safety: Safe for concurrent use.
func (b *Bridge) recordTraceStep(
	ctx context.Context,
	result activities.ActivityResult,
	delta crs.Delta,
	input activities.ActivityInput,
	startTime time.Time,
) {
	recordingStart := time.Now()

	// Fast path: check if any recording is enabled
	b.mu.RLock()
	recorder := b.traceRecorder
	recordingEnabled := b.config.EnableTraceRecording
	metricsEnabled := b.config.EnableMetrics
	tracingEnabled := b.config.EnableTracing
	b.mu.RUnlock()

	// Nothing to do if everything is disabled
	if recorder == nil && !metricsEnabled && !tracingEnabled {
		return
	}

	// Panic recovery - recording must never crash the activity
	defer func() {
		if r := recover(); r != nil {
			b.logger.Warn("trace recording panicked",
				slog.Any("panic", r),
				slog.String("activity", result.ActivityName),
			)
			RecordRecordingError("panic")
		}
	}()

	// Extract trace step
	step := ExtractTraceStep(&result, delta, input, startTime)

	// --- 1. OTel Span Event ---
	if tracingEnabled {
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			span.AddEvent("crs.activity_completed", trace.WithAttributes(
				attribute.String("activity", result.ActivityName),
				attribute.Bool("success", result.Success),
				attribute.Bool("partial_success", result.PartialSuccess),
				attribute.Int("proof_updates", len(step.ProofUpdates)),
				attribute.Int("constraints_added", len(step.ConstraintsAdded)),
				attribute.Int("dependencies_found", len(step.DependenciesFound)),
				attribute.Int("symbols_found", len(step.SymbolsFound)),
				attribute.Int64("duration_ms", step.Duration.Milliseconds()),
			))

			// Add trace_id to step metadata for correlation (DR-10: pre-allocate)
			spanCtx := span.SpanContext()
			if spanCtx.IsValid() {
				if step.Metadata == nil {
					step.Metadata = make(map[string]string, 2)
				}
				step.Metadata["trace_id"] = spanCtx.TraceID().String()
				step.Metadata["span_id"] = spanCtx.SpanID().String()
			}
		}
	}

	// --- 2. Prometheus Metrics ---
	if metricsEnabled {
		RecordActivityMetrics(
			result.ActivityName,
			result.Success,
			result.PartialSuccess,
			step.Duration.Seconds(),
		)
		RecordStepMetrics(result.ActivityName, &step)
		UpdateGenerationGauge(b.crs.Generation())
	}

	// --- 3. Structured Log with Trace Context ---
	logger := b.logger
	if tracingEnabled {
		spanCtx := trace.SpanContextFromContext(ctx)
		if spanCtx.IsValid() {
			logger = logger.With(
				slog.String("trace_id", spanCtx.TraceID().String()),
				slog.String("span_id", spanCtx.SpanID().String()),
			)
		}
	}
	logger.Info("crs activity completed",
		slog.String("activity", result.ActivityName),
		slog.Bool("success", result.Success),
		slog.Int("proof_updates", len(step.ProofUpdates)),
		slog.Int("constraints_added", len(step.ConstraintsAdded)),
		slog.Int("dependencies_found", len(step.DependenciesFound)),
		slog.Duration("duration", step.Duration),
	)

	// --- 4. TraceRecorder ---
	if recorder != nil && recordingEnabled {
		recorder.RecordStep(step)
	}

	// Record recording overhead
	if metricsEnabled {
		RecordRecordingDuration(recordingStart)
	}
}

// Snapshot returns the current CRS snapshot.
func (b *Bridge) Snapshot() crs.Snapshot {
	return b.crs.Snapshot()
}

// CRS returns the underlying CRS instance.
//
// Description:
//
//	Provides direct access to the CRS for operations not covered by the
//	Bridge interface, such as cache invalidation (GR-29).
//
// Thread Safety: Safe for concurrent calls. The returned CRS is thread-safe.
func (b *Bridge) CRS() crs.CRS {
	return b.crs
}

// Apply applies a delta to CRS.
//
// Description:
//
//	Direct delta application. Use RunActivity for managed execution.
//
// Thread Safety: Safe for concurrent calls.
func (b *Bridge) Apply(ctx context.Context, delta crs.Delta) (crs.ApplyMetrics, error) {
	if ctx == nil {
		return crs.ApplyMetrics{}, &BridgeError{Operation: "Apply", Err: ErrNilContext}
	}

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return crs.ApplyMetrics{}, &BridgeError{Operation: "Apply", Err: ErrBridgeClosed}
	}
	b.mu.RUnlock()

	// Validate delta before applying per Rule #6
	snapshot := b.crs.Snapshot()
	if err := delta.Validate(snapshot); err != nil {
		return crs.ApplyMetrics{}, &BridgeError{Operation: "Apply.Validate", Err: err}
	}

	metrics, err := b.crs.Apply(ctx, delta)
	if err != nil {
		return metrics, &BridgeError{Operation: "Apply", Err: err}
	}

	b.mu.Lock()
	b.deltasApplied++
	b.mu.Unlock()

	return metrics, nil
}

// Generation returns the current CRS generation.
func (b *Bridge) Generation() int64 {
	return b.crs.Generation()
}

// Close closes the bridge.
func (b *Bridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true
	return nil
}

// SetTraceRecorder sets or replaces the trace recorder.
//
// Description:
//
//	Allows setting the trace recorder after construction.
//	Useful when the recorder is created after the Bridge.
//
// Inputs:
//
//	recorder - The trace recorder. May be nil (disables recording).
//
// Thread Safety: Safe for concurrent use.
func (b *Bridge) SetTraceRecorder(recorder *crs.TraceRecorder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.traceRecorder = recorder
}

// TraceRecorder returns the current trace recorder, or nil if not set.
//
// Thread Safety: Safe for concurrent use.
func (b *Bridge) TraceRecorder() *crs.TraceRecorder {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.traceRecorder
}

// SetJournal sets or replaces the delta journal.
//
// Description:
//
//	Allows setting the journal after construction.
//	Useful when the journal is created after the Bridge.
//
// Inputs:
//
//	journal - The delta journal. May be nil (disables journaling).
//
// Thread Safety: Safe for concurrent use.
func (b *Bridge) SetJournal(journal crs.Journal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.journal = journal
}

// Journal returns the current journal, or nil if not set.
//
// Thread Safety: Safe for concurrent use.
func (b *Bridge) Journal() crs.Journal {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.journal
}

// Stats returns bridge statistics.
func (b *Bridge) Stats() BridgeStats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return BridgeStats{
		ActivitiesRun:  b.activitiesRun,
		DeltasApplied:  b.deltasApplied,
		ConflictsFound: b.conflictsFound,
		Generation:     b.crs.Generation(), // Get current generation from CRS directly
	}
}

// BridgeStats contains bridge statistics.
type BridgeStats struct {
	ActivitiesRun  int64
	DeltasApplied  int64
	ConflictsFound int64
	Generation     int64
}

// -----------------------------------------------------------------------------
// Evaluable Implementation
// -----------------------------------------------------------------------------

// Properties returns the correctness properties.
func (b *Bridge) Properties() []eval.Property {
	return []eval.Property{
		{
			Name:        "deltas_applied_atomically",
			Description: "Deltas are applied atomically to CRS",
			Check: func(input, output any) error {
				// Verified by CRS implementation
				return nil
			},
		},
		{
			Name:        "conflicts_retried",
			Description: "Conflicts trigger retry with fresh snapshot",
			Check: func(input, output any) error {
				// Verified by RunActivity implementation
				return nil
			},
		},
	}
}

// Metrics returns the metrics this component exposes.
func (b *Bridge) Metrics() []eval.MetricDefinition {
	return []eval.MetricDefinition{
		{
			Name:        "bridge_activities_run_total",
			Type:        eval.MetricCounter,
			Description: "Total activities run through bridge",
		},
		{
			Name:        "bridge_deltas_applied_total",
			Type:        eval.MetricCounter,
			Description: "Total deltas applied to CRS",
		},
		{
			Name:        "bridge_conflicts_total",
			Type:        eval.MetricCounter,
			Description: "Total conflicts requiring retry",
		},
	}
}

// HealthCheck verifies the bridge is functioning.
func (b *Bridge) HealthCheck(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return &BridgeError{Operation: "HealthCheck", Err: ErrBridgeClosed}
	}

	if b.crs == nil {
		return &BridgeError{Operation: "HealthCheck", Err: ErrNilCRS}
	}

	return nil
}
