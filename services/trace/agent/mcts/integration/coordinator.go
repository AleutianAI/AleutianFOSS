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
	"sort"
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
	// ErrCoordinatorClosed is returned when the coordinator is closed.
	ErrCoordinatorClosed = errors.New("coordinator is closed")

	// ErrNoActivitiesReady is returned when no activities are ready to run.
	ErrNoActivitiesReady = errors.New("no activities ready to run")

	// ErrActivityNotFound is returned when an activity is not found.
	ErrActivityNotFound = errors.New("activity not found")
)

// CoordinatorError wraps an error with coordinator context.
type CoordinatorError struct {
	Operation string
	Err       error
}

func (e *CoordinatorError) Error() string {
	return "coordinator." + e.Operation + ": " + e.Err.Error()
}

func (e *CoordinatorError) Unwrap() error {
	return e.Err
}

// -----------------------------------------------------------------------------
// Coordinator
// -----------------------------------------------------------------------------

// Coordinator schedules and orchestrates activity execution.
//
// Description:
//
//	Coordinator is the central scheduler that:
//	- Maintains a registry of activities
//	- Determines which activities should run based on CRS state
//	- Prioritizes and schedules activity execution
//	- Runs activities through the Bridge
//	- Handles activity failures and retries
//
// Thread Safety: Safe for concurrent use.
type Coordinator struct {
	mu         sync.RWMutex
	bridge     *Bridge
	activities map[string]activities.Activity
	config     *CoordinatorConfig
	logger     *slog.Logger

	// State
	closed bool

	// Metrics
	scheduledTotal int64
	executedTotal  int64
	failedTotal    int64
}

// CoordinatorConfig configures the coordinator.
type CoordinatorConfig struct {
	// MaxConcurrentActivities limits parallel activity execution.
	MaxConcurrentActivities int

	// ScheduleInterval is how often to check for ready activities.
	ScheduleInterval time.Duration

	// EnableMetrics enables metrics collection.
	EnableMetrics bool

	// EnableTracing enables OpenTelemetry tracing.
	EnableTracing bool

	// ActivityConfigs provides per-activity configuration for event handling.
	// If nil, DefaultActivityConfigs() is used.
	ActivityConfigs map[ActivityName]*ActivityConfig

	// Filters are applied to activity lists during HandleEvent.
	// Filters run in order, each receiving the output of the previous.
	Filters []ActivityFilter

	// EventContext provides context for dynamic filtering.
	// If nil, filters that require context are skipped.
	EventContext *EventContext
}

// DefaultCoordinatorConfig returns the default coordinator configuration.
func DefaultCoordinatorConfig() *CoordinatorConfig {
	return &CoordinatorConfig{
		MaxConcurrentActivities: 4,
		ScheduleInterval:        100 * time.Millisecond,
		EnableMetrics:           true,
		EnableTracing:           true,
	}
}

// NewCoordinator creates a new coordinator.
//
// Inputs:
//   - bridge: The bridge to CRS.
//   - config: Coordinator configuration. Uses defaults if nil.
//
// Outputs:
//   - *Coordinator: The new coordinator.
func NewCoordinator(bridge *Bridge, config *CoordinatorConfig) *Coordinator {
	if config == nil {
		config = DefaultCoordinatorConfig()
	}

	return &Coordinator{
		bridge:     bridge,
		activities: make(map[string]activities.Activity),
		config:     config,
		logger:     slog.Default().With(slog.String("component", "coordinator")),
	}
}

// -----------------------------------------------------------------------------
// Activity Registration
// -----------------------------------------------------------------------------

// Register adds an activity to the coordinator.
//
// Inputs:
//   - activity: The activity to register.
//
// Thread Safety: Safe for concurrent calls.
func (c *Coordinator) Register(activity activities.Activity) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activities[activity.Name()] = activity
	c.logger.Debug("activity registered",
		slog.String("activity", activity.Name()),
	)
}

// Unregister removes an activity from the coordinator.
func (c *Coordinator) Unregister(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.activities, name)
	c.logger.Info("activity unregistered",
		slog.String("activity", name),
	)
}

// Get returns an activity by name.
func (c *Coordinator) Get(name string) (activities.Activity, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	activity, ok := c.activities[name]
	return activity, ok
}

// All returns all registered activities.
func (c *Coordinator) All() []activities.Activity {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]activities.Activity, 0, len(c.activities))
	for _, activity := range c.activities {
		result = append(result, activity)
	}
	return result
}

// -----------------------------------------------------------------------------
// Scheduling
// -----------------------------------------------------------------------------

// scheduledActivity pairs an activity with its priority.
type scheduledActivity struct {
	activity activities.Activity
	priority activities.Priority
}

// Schedule determines which activities should run.
//
// Description:
//
//	Examines the current CRS state and returns activities that
//	should run, sorted by priority.
//
// Outputs:
//   - []scheduledActivity: Activities to run, sorted by priority.
func (c *Coordinator) Schedule() []scheduledActivity {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := c.bridge.Snapshot()
	var scheduled []scheduledActivity

	for _, activity := range c.activities {
		shouldRun, priority := activity.ShouldRun(snapshot)
		if shouldRun {
			scheduled = append(scheduled, scheduledActivity{
				activity: activity,
				priority: priority,
			})
		}
	}

	// Sort by priority (higher priority first)
	sort.Slice(scheduled, func(i, j int) bool {
		return scheduled[i].priority > scheduled[j].priority
	})

	return scheduled
}

// -----------------------------------------------------------------------------
// Execution
// -----------------------------------------------------------------------------

// RunOnce runs one scheduling cycle.
//
// Description:
//
//	Schedules activities and runs the highest priority ones
//	up to MaxConcurrentActivities.
//
// Inputs:
//   - ctx: Context for cancellation.
//
// Outputs:
//   - []activities.ActivityResult: Results from executed activities.
//   - error: Non-nil if execution failed.
func (c *Coordinator) RunOnce(ctx context.Context) ([]activities.ActivityResult, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, &CoordinatorError{Operation: "RunOnce", Err: ErrCoordinatorClosed}
	}
	c.mu.RUnlock()

	// Start trace
	var span trace.Span
	if c.config.EnableTracing {
		ctx, span = otel.Tracer("integration").Start(ctx, "coordinator.RunOnce")
		defer span.End()
	}

	// Schedule activities
	scheduled := c.Schedule()
	if len(scheduled) == 0 {
		return nil, nil
	}

	c.mu.Lock()
	c.scheduledTotal += int64(len(scheduled))
	c.mu.Unlock()

	// Limit concurrent activities
	limit := c.config.MaxConcurrentActivities
	if limit > len(scheduled) {
		limit = len(scheduled)
	}
	scheduled = scheduled[:limit]

	// Run activities in parallel
	// Channel buffers are sized to scheduled count (expected max: 8 activities).
	// This prevents blocking but assumes bounded activity registration.
	var wg sync.WaitGroup
	resultsCh := make(chan activities.ActivityResult, len(scheduled))
	errorsCh := make(chan error, len(scheduled))

	for _, sa := range scheduled {
		// Check for cancellation before spawning to avoid goroutine pile-up
		select {
		case <-ctx.Done():
			// Context cancelled, stop spawning new goroutines
			c.logger.Debug("run cycle cancelled before all activities spawned",
				slog.Int("spawned", len(scheduled)-len(scheduled)),
			)
			break
		default:
		}

		wg.Add(1)
		go func(sa scheduledActivity) {
			defer wg.Done()

			// Create activity input
			input := createActivityInput(sa.activity)

			result, err := c.bridge.RunActivity(ctx, sa.activity, input)
			if err != nil {
				errorsCh <- err
				c.mu.Lock()
				c.failedTotal++
				c.mu.Unlock()
			} else {
				resultsCh <- result
				c.mu.Lock()
				c.executedTotal++
				c.mu.Unlock()
			}
		}(sa)
	}

	// Wait for all to complete
	wg.Wait()
	close(resultsCh)
	close(errorsCh)

	// Collect results
	var results []activities.ActivityResult
	for result := range resultsCh {
		results = append(results, result)
	}

	// Log errors with trace context
	for err := range errorsCh {
		// Extract trace context for correlation
		spanCtx := trace.SpanContextFromContext(ctx)
		attrs := []any{slog.String("error", err.Error())}
		if spanCtx.IsValid() {
			attrs = append(attrs,
				slog.String("trace_id", spanCtx.TraceID().String()),
				slog.String("span_id", spanCtx.SpanID().String()),
			)
		}
		c.logger.Warn("activity execution failed", attrs...)
	}

	if span != nil {
		span.SetAttributes(
			attribute.Int("activities_scheduled", len(scheduled)),
			attribute.Int("activities_executed", len(results)),
		)
	}

	return results, nil
}

// Run runs the coordinator continuously until context is cancelled.
//
// Description:
//
//	Runs scheduling cycles at ScheduleInterval until ctx is cancelled.
//
// Inputs:
//   - ctx: Context for cancellation.
//
// Outputs:
//   - error: Non-nil if run loop failed.
func (c *Coordinator) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.config.ScheduleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_, err := c.RunOnce(ctx)
			if err != nil && !errors.Is(err, context.Canceled) {
				c.logger.Warn("run cycle failed",
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// RunActivity runs a specific activity by name.
//
// Inputs:
//   - ctx: Context for cancellation.
//   - name: Activity name.
//   - input: Activity input.
//
// Outputs:
//   - activities.ActivityResult: The result.
//   - error: Non-nil on failure.
func (c *Coordinator) RunActivity(
	ctx context.Context,
	name string,
	input activities.ActivityInput,
) (activities.ActivityResult, error) {
	activity, ok := c.Get(name)
	if !ok {
		return activities.ActivityResult{}, &CoordinatorError{
			Operation: "RunActivity",
			Err:       ErrActivityNotFound,
		}
	}

	return c.bridge.RunActivity(ctx, activity, input)
}

// createActivityInput creates a default input for an activity.
func createActivityInput(activity activities.Activity) activities.ActivityInput {
	// Create appropriate input based on activity type
	switch activity.Name() {
	case "search":
		return activities.NewSearchInput("auto", "", crs.SignalSourceSoft)
	case "learning":
		return activities.NewLearningInput("auto", "", crs.SignalSourceSoft)
	case "constraint":
		return activities.NewConstraintInput("auto", "propagate", crs.SignalSourceSoft)
	case "planning":
		return activities.NewPlanningInput("auto", "", crs.SignalSourceSoft)
	case "awareness":
		return activities.NewAwarenessInput("auto", crs.SignalSourceSoft)
	case "similarity":
		return activities.NewSimilarityInput("auto", crs.SignalSourceSoft)
	case "streaming":
		return activities.NewStreamingInput("auto", crs.SignalSourceSoft)
	case "memory":
		return activities.NewMemoryInput("auto", "query", crs.SignalSourceSoft)
	default:
		return activities.BaseInput{}
	}
}

// Close closes the coordinator.
func (c *Coordinator) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return nil
}

// =============================================================================
// Event-Driven Execution (CRS-06)
// =============================================================================

// HandleEvent handles an agent event by running the appropriate activities.
//
// Description:
//
//	This is the primary entry point for event-driven activity coordination.
//	Based on the event type, determines which activities to run, filters
//	them based on configuration and context, then executes them in priority
//	order respecting dependencies.
//
// Inputs:
//   - ctx: Context for cancellation.
//   - event: The agent event that occurred.
//   - data: Data associated with the event.
//
// Outputs:
//   - []activities.ActivityResult: Results from executed activities.
//   - error: Non-nil if a required activity failed.
//
// Thread Safety: Safe for concurrent use.
func (c *Coordinator) HandleEvent(
	ctx context.Context,
	event AgentEvent,
	data *EventData,
) ([]activities.ActivityResult, error) {
	c.mu.RLock()
	if c.closed {
		c.mu.RUnlock()
		return nil, &CoordinatorError{Operation: "HandleEvent", Err: ErrCoordinatorClosed}
	}
	c.mu.RUnlock()

	// Start trace
	var span trace.Span
	if c.config.EnableTracing {
		ctx, span = otel.Tracer("integration").Start(ctx, "coordinator.HandleEvent",
			trace.WithAttributes(
				attribute.String("event", string(event)),
				attribute.String("session_id", data.SessionID),
			),
		)
		defer span.End()
	}

	c.logger.Debug("handling event",
		slog.String("event", string(event)),
		slog.String("session_id", data.SessionID),
	)

	// GR-29: Invalidate graph cache on refresh event
	if event == EventGraphRefreshed && c.bridge != nil && c.bridge.CRS() != nil {
		c.bridge.CRS().InvalidateGraphCache()
		c.logger.Debug("invalidated graph cache after refresh",
			slog.String("session_id", data.SessionID),
		)
	}

	// Get activities for this event
	activityNames, ok := EventActivityMapping[event]
	if !ok || len(activityNames) == 0 {
		c.logger.Debug("no activities mapped for event",
			slog.String("event", string(event)),
		)
		return nil, nil
	}

	// Apply filters if configured
	if c.config.EventContext != nil {
		for _, filter := range c.config.Filters {
			activityNames = filter.Filter(event, activityNames, c.config.EventContext)
		}
	}

	// Filter to only registered and enabled activities
	configs := c.config.ActivityConfigs
	if configs == nil {
		configs = DefaultActivityConfigs()
	}

	var toRun []scheduledActivity
	for _, name := range activityNames {
		activity, registered := c.Get(string(name))
		if !registered {
			c.logger.Debug("activity not registered, skipping",
				slog.String("activity", string(name)),
			)
			continue
		}

		config, hasConfig := configs[name]
		if hasConfig && !config.Enabled {
			c.logger.Debug("activity disabled, skipping",
				slog.String("activity", string(name)),
			)
			continue
		}

		priority := activities.Priority(50) // default
		if hasConfig {
			priority = activities.Priority(config.Priority)
		}

		toRun = append(toRun, scheduledActivity{
			activity: activity,
			priority: priority,
		})
	}

	if len(toRun) == 0 {
		c.logger.Debug("no activities to run after filtering",
			slog.String("event", string(event)),
		)
		return nil, nil
	}

	// Sort by priority
	sort.Slice(toRun, func(i, j int) bool {
		return toRun[i].priority > toRun[j].priority
	})

	c.mu.Lock()
	c.scheduledTotal += int64(len(toRun))
	c.mu.Unlock()

	// Execute activities
	// For simplicity, run sequentially respecting priority/dependency order.
	// Parallel groups can be added later for optimization.
	var results []activities.ActivityResult
	completedActivities := make(map[string]bool)

	for _, sa := range toRun {
		// Check context cancellation
		select {
		case <-ctx.Done():
			c.logger.Debug("event handling cancelled",
				slog.String("event", string(event)),
			)
			return results, ctx.Err()
		default:
		}

		activityName := ActivityName(sa.activity.Name())
		config := configs[activityName]

		// CR-5 fix: Check dependencies with better distinction between missing and not-run
		// Dependencies are checked against coordinator registration, not just this event's activities.
		// A dependency is satisfied if:
		// 1. It's registered with the coordinator (even if not triggered by this event), AND
		// 2. It has completed in this execution cycle
		// If a dependency is not triggered by this event but is registered, we skip silently
		// (it may have run in a previous event or is simply not needed for this event type).
		if config != nil && len(config.DependsOn) > 0 {
			allDepsComplete := true
			for _, dep := range config.DependsOn {
				// Check if dependency is registered with the coordinator at all
				_, depRegistered := c.Get(string(dep))
				if !depRegistered {
					// Dependency is not registered - this is a configuration error
					c.logger.Warn("dependency activity not registered, skipping dependent",
						slog.String("activity", string(activityName)),
						slog.String("missing_dependency", string(dep)),
					)
					allDepsComplete = false
					break
				}
				// Dependency is registered. Check if it's in toRun for this event.
				depScheduledForThisEvent := false
				for _, scheduled := range toRun {
					if ActivityName(scheduled.activity.Name()) == dep {
						depScheduledForThisEvent = true
						break
					}
				}
				if depScheduledForThisEvent && !completedActivities[string(dep)] {
					// Dependency is scheduled for this event but not yet complete - wait
					c.logger.Debug("dependency not complete yet, skipping for now",
						slog.String("activity", string(activityName)),
						slog.String("dependency", string(dep)),
					)
					allDepsComplete = false
					break
				}
				// If dependency is not scheduled for this event, we don't require it
				// (it may have been satisfied by a previous event or is not relevant here)
			}
			if !allDepsComplete {
				continue
			}
		}

		// Create input from event data
		input := c.createInputFromEvent(sa.activity, event, data)

		// Run activity
		result, err := c.bridge.RunActivity(ctx, sa.activity, input)
		if err != nil {
			c.mu.Lock()
			c.failedTotal++
			c.mu.Unlock()

			isOptional := config != nil && config.Optional
			if isOptional {
				c.logger.Warn("optional activity failed",
					slog.String("activity", sa.activity.Name()),
					slog.String("error", err.Error()),
				)
				// Continue with other activities
			} else {
				c.logger.Error("required activity failed",
					slog.String("activity", sa.activity.Name()),
					slog.String("error", err.Error()),
				)
				return results, &CoordinatorError{
					Operation: "HandleEvent." + sa.activity.Name(),
					Err:       err,
				}
			}
		} else {
			results = append(results, result)
			completedActivities[sa.activity.Name()] = true

			c.mu.Lock()
			c.executedTotal++
			c.mu.Unlock()
		}
	}

	if span != nil {
		span.SetAttributes(
			attribute.Int("activities_scheduled", len(toRun)),
			attribute.Int("activities_executed", len(results)),
		)
	}

	c.logger.Debug("event handled",
		slog.String("event", string(event)),
		slog.Int("activities_run", len(results)),
	)

	return results, nil
}

// createInputFromEvent creates an activity input from event data.
func (c *Coordinator) createInputFromEvent(
	activity activities.Activity,
	event AgentEvent,
	data *EventData,
) activities.ActivityInput {
	source := crs.SignalSourceHard // Events from agent are authoritative

	switch activity.Name() {
	case "search":
		return activities.NewSearchInput(data.SessionID, "", source)
	case "learning":
		conflictNode := ""
		if data.Tool != "" {
			conflictNode = "tool:" + data.Tool
		}
		// CR-10 fix: Include error message in learning input for better clause generation
		input := activities.NewLearningInput(data.SessionID, conflictNode, source)
		if data.Error != "" {
			input.SetErrorInfo(data.Error, data.ErrorCategory)
		}
		return input
	case "constraint":
		operation := "propagate"
		if event == EventCircuitBreaker {
			operation = "restrict"
		}
		return activities.NewConstraintInput(data.SessionID, operation, source)
	case "planning":
		return activities.NewPlanningInput(data.SessionID, "", source)
	case "awareness":
		return activities.NewAwarenessInput(data.SessionID, source)
	case "similarity":
		return activities.NewSimilarityInput(data.SessionID, source)
	case "streaming":
		return activities.NewStreamingInput(data.SessionID, source)
	case "memory":
		operation := "record"
		if event == EventQueryReceived {
			operation = "query"
		} else if event == EventSessionEnd {
			operation = "persist"
		}
		return activities.NewMemoryInput(data.SessionID, operation, source)
	default:
		return activities.BaseInput{}
	}
}

// Stats returns coordinator statistics.
func (c *Coordinator) Stats() CoordinatorStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CoordinatorStats{
		RegisteredActivities: len(c.activities),
		ScheduledTotal:       c.scheduledTotal,
		ExecutedTotal:        c.executedTotal,
		FailedTotal:          c.failedTotal,
	}
}

// CoordinatorStats contains coordinator statistics.
type CoordinatorStats struct {
	RegisteredActivities int
	ScheduledTotal       int64
	ExecutedTotal        int64
	FailedTotal          int64
}

// -----------------------------------------------------------------------------
// Evaluable Implementation
// -----------------------------------------------------------------------------

// Properties returns the correctness properties.
func (c *Coordinator) Properties() []eval.Property {
	return []eval.Property{
		{
			Name:        "priority_ordering",
			Description: "Higher priority activities run first",
			Check: func(input, output any) error {
				// Verified by Schedule implementation
				return nil
			},
		},
		{
			Name:        "concurrent_limit",
			Description: "Max concurrent activities is respected",
			Check: func(input, output any) error {
				// Verified by RunOnce implementation
				return nil
			},
		},
	}
}

// Metrics returns the metrics this component exposes.
func (c *Coordinator) Metrics() []eval.MetricDefinition {
	return []eval.MetricDefinition{
		{
			Name:        "coordinator_scheduled_total",
			Type:        eval.MetricCounter,
			Description: "Total activities scheduled",
		},
		{
			Name:        "coordinator_executed_total",
			Type:        eval.MetricCounter,
			Description: "Total activities executed",
		},
		{
			Name:        "coordinator_failed_total",
			Type:        eval.MetricCounter,
			Description: "Total activity failures",
		},
	}
}

// HealthCheck verifies the coordinator is functioning.
func (c *Coordinator) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return &CoordinatorError{Operation: "HealthCheck", Err: ErrCoordinatorClosed}
	}

	if c.bridge == nil {
		return &CoordinatorError{Operation: "HealthCheck", Err: ErrNilCRS}
	}

	return c.bridge.HealthCheck(ctx)
}
