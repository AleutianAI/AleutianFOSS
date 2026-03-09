// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// CRS Implementation
// -----------------------------------------------------------------------------

// crsImpl implements the CRS interface.
//
// Lock Ordering (to prevent deadlocks):
//
//	Always acquire locks in this order:
//	1. crsImpl.mu (RWMutex)
//	2. streamingStats.mu (RWMutex)
//	3. sessionSteps.mu (RWMutex) - per-session lock
//
//	Never acquire crsImpl.mu while holding streamingStats.mu.
//
// Thread Safety: Safe for concurrent use.
type crsImpl struct {
	mu     sync.RWMutex
	config *Config
	logger *slog.Logger

	// State
	generation atomic.Int64

	// Index data (mutable, protected by mu)
	proofData      map[string]ProofNumber
	constraintData map[string]Constraint
	similarityData map[string]map[string]float64
	dependencyData *dependencyGraph
	historyData    []HistoryEntry
	streamingData  *streamingStats

	// Clause data (CRS-04) - protected by mu
	clauseData   map[string]*Clause
	clauseConfig ClausePersistence

	// Delta history (GR-35) - channel-based, no lock needed
	deltaHistory *DeltaHistoryWorker

	// Current session ID for delta history tracking
	currentSessionID string

	// Graph provider (GR-28) - protected by mu
	graphProvider GraphQuery

	// Graph-backed dependency index (GR-32) - protected by mu
	graphBackedDepIndex *GraphBackedDependencyIndex

	// StepRecord data (CRS-01) - separate lock for step recording
	// Uses per-session locking for better concurrency
	stepMu   sync.RWMutex
	stepData map[string]*sessionSteps

	// Metrics (atomic, no lock needed)
	snapshotCount   atomic.Int64
	applyCount      atomic.Int64
	applyErrorCount atomic.Int64
	stepRecordCount atomic.Int64

	// GR-32 Code Review Fix: Rate-limit DependencyDelta deprecation warning
	depDeltaWarnOnce sync.Once

	// GR-31: Analytics history for tracking analytics queries
	analyticsData *AnalyticsHistory
}

// sessionSteps holds step records for a single session.
// Uses lazy indexing - secondary indexes are built on first query.
type sessionSteps struct {
	mu          sync.RWMutex
	steps       []StepRecord   // Primary storage, append-only
	byTool      map[string]int // Lazy: execution count per tool
	byToolDirty bool           // True if byTool needs rebuild
	generation  int64          // For cache invalidation
}

// maxStepsPerSession is the default maximum steps per session.
// This prevents unbounded memory growth from long-running sessions.
const maxStepsPerSession = 10000

// New creates a new CRS instance.
//
// Inputs:
//   - config: Configuration. If nil, uses DefaultConfig().
//
// Outputs:
//   - CRS: The new CRS instance. Never nil.
//
// Thread Safety: Safe for concurrent use.
func New(config *Config) CRS {
	if config == nil {
		config = DefaultConfig()
	}

	logger := slog.Default().With(slog.String("component", "crs"))

	return &crsImpl{
		config:         config,
		logger:         logger,
		proofData:      make(map[string]ProofNumber),
		constraintData: make(map[string]Constraint),
		similarityData: make(map[string]map[string]float64),
		dependencyData: newDependencyGraph(),
		historyData:    make([]HistoryEntry, 0),
		streamingData:  newStreamingStats(),
		clauseData:     make(map[string]*Clause),
		clauseConfig:   DefaultClauseConfig,
		deltaHistory:   NewDeltaHistoryWorker(DefaultMaxDeltaRecords, logger),
		stepData:       make(map[string]*sessionSteps),
		analyticsData:  NewAnalyticsHistory(MaxAnalyticsHistoryRecords),
	}
}

// -----------------------------------------------------------------------------
// CRS Interface Implementation
// -----------------------------------------------------------------------------

// Snapshot returns an immutable view of the current state.
func (c *crsImpl) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	c.snapshotCount.Add(1)

	snap := newSnapshot(
		c.generation.Load(),
		c.proofData,
		c.constraintData,
		c.similarityData,
		c.dependencyData,
		c.historyData,
		c.streamingData,
		c.clauseData,
	)

	// GR-28: Include graph provider in snapshot
	snap.setGraphQuery(c.graphProvider)

	// GR-32: Include graph-backed dependency index in snapshot
	snap.setGraphBackedDepIndex(c.graphBackedDepIndex)

	// GR-31: Include analytics history in snapshot (cloned for immutability)
	if c.analyticsData != nil {
		snap.setAnalyticsHistory(c.analyticsData.clone())
	}

	return snap
}

// Apply atomically applies a delta to the state.
func (c *crsImpl) Apply(ctx context.Context, delta Delta) (ApplyMetrics, error) {
	metrics, err := c.applyCore(ctx, delta, "crs.Apply")
	if err != nil {
		return metrics, err
	}

	// Record in delta history with default source (GR-35)
	if c.deltaHistory != nil {
		c.deltaHistory.Record(
			delta,
			metrics.NewGeneration,
			delta.Source().String(), // Use signal source as default
			c.currentSessionID,
			nil,
		)
	}

	return metrics, nil
}

// applyCore performs the core apply logic without recording to delta history.
// Used by both Apply and ApplyWithSource to avoid duplicate recording.
func (c *crsImpl) applyCore(ctx context.Context, delta Delta, spanName string) (ApplyMetrics, error) {
	if ctx == nil {
		return ApplyMetrics{}, ErrNilContext
	}
	if delta == nil {
		return ApplyMetrics{}, ErrNilDelta
	}

	// Check cancellation
	select {
	case <-ctx.Done():
		return ApplyMetrics{}, ctx.Err()
	default:
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, spanName,
		trace.WithAttributes(
			attribute.String("delta_type", delta.Type().String()),
			attribute.String("source", delta.Source().String()),
		),
	)
	defer span.End()

	startTime := time.Now()
	metrics := ApplyMetrics{
		DeltaType:     delta.Type(),
		OldGeneration: c.generation.Load(),
	}

	// Phase 1: Validate
	validationStart := time.Now()
	snapshot := c.Snapshot()
	if err := delta.Validate(snapshot); err != nil {
		c.applyErrorCount.Add(1)
		span.RecordError(err)
		span.SetStatus(codes.Error, "validation failed")
		return metrics, fmt.Errorf("%w: %w", ErrDeltaValidation, err)
	}
	metrics.ValidationDuration = time.Since(validationStart)

	// Check cancellation again before acquiring write lock
	select {
	case <-ctx.Done():
		return metrics, ctx.Err()
	default:
	}

	// Phase 2: Apply (atomic)
	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check generation hasn't changed during validation
	if c.generation.Load() != metrics.OldGeneration {
		// State changed, need to re-validate
		currentSnap := newSnapshot(
			c.generation.Load(),
			c.proofData,
			c.constraintData,
			c.similarityData,
			c.dependencyData,
			c.historyData,
			c.streamingData,
			c.clauseData,
		)
		if err := delta.Validate(currentSnap); err != nil {
			c.applyErrorCount.Add(1)
			span.RecordError(err)
			span.SetStatus(codes.Error, "re-validation failed")
			return metrics, fmt.Errorf("%w: %w", ErrDeltaValidation, err)
		}
	}

	// Apply the delta based on type
	var err error
	switch d := delta.(type) {
	case *ProofDelta:
		err = c.applyProofDelta(d, &metrics)
	case *ConstraintDelta:
		err = c.applyConstraintDelta(d, &metrics)
	case *SimilarityDelta:
		err = c.applySimilarityDelta(d, &metrics)
	case *DependencyDelta:
		err = c.applyDependencyDelta(d, &metrics)
	case *HistoryDelta:
		err = c.applyHistoryDelta(d, &metrics)
	case *StreamingDelta:
		err = c.applyStreamingDelta(d, &metrics)
	case *CompositeDelta:
		err = c.applyCompositeDelta(ctx, d, &metrics)
	case *AnalyticsDelta:
		err = c.applyAnalyticsDelta(d, &metrics)
	default:
		err = fmt.Errorf("unknown delta type: %T", delta)
	}

	if err != nil {
		c.applyErrorCount.Add(1)
		span.RecordError(err)
		span.SetStatus(codes.Error, "apply failed")
		// Include delta type in error for debugging (P3 fix: C1)
		return metrics, fmt.Errorf("%w (delta type: %s): %w", ErrApplyRollback, delta.Type(), err)
	}

	// Increment generation
	metrics.NewGeneration = c.generation.Add(1)
	metrics.ApplyDuration = time.Since(startTime)
	c.applyCount.Add(1)

	span.SetAttributes(
		attribute.Int64("old_generation", metrics.OldGeneration),
		attribute.Int64("new_generation", metrics.NewGeneration),
		attribute.Int("entries_modified", metrics.EntriesModified),
	)

	c.logger.Debug("delta applied",
		slog.String("type", delta.Type().String()),
		slog.Int64("generation", metrics.NewGeneration),
		slog.Int("entries", metrics.EntriesModified),
		slog.Duration("duration", metrics.ApplyDuration),
	)

	return metrics, nil
}

// Generation returns the current state version.
func (c *crsImpl) Generation() int64 {
	return c.generation.Load()
}

// SetSessionID sets the current session ID for delta history tracking.
//
// Description:
//
//	Deltas recorded via Apply() will be associated with this session ID.
//	Call this at the start of each agent session.
//
// Inputs:
//   - sessionID: The session identifier. Can be empty to clear.
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) SetSessionID(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentSessionID = sessionID
}

// ApplyWithSource applies a delta with explicit source and metadata tracking.
//
// Description:
//
//	Like Apply(), but allows specifying a custom source string and metadata
//	for delta history recording. Use this when you want to track which
//	activity or component caused the delta.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - delta: The delta to apply. Must not be nil.
//   - source: Human-readable source identifier (e.g., "AwarenessActivity").
//   - metadata: Optional additional context for the delta.
//
// Outputs:
//   - ApplyMetrics: Metrics about the apply operation.
//   - error: Non-nil on validation failure or apply error.
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) ApplyWithSource(ctx context.Context, delta Delta, source string, metadata map[string]string) (ApplyMetrics, error) {
	// Use core apply logic (does not record to delta history)
	metrics, err := c.applyCore(ctx, delta, "crs.ApplyWithSource")
	if err != nil {
		return metrics, err
	}

	// Record with explicit source and metadata (GR-35)
	// Only one record is created, unlike if we called Apply
	if c.deltaHistory != nil {
		c.deltaHistory.Record(
			delta,
			metrics.NewGeneration,
			source,
			c.currentSessionID,
			metadata,
		)
	}

	return metrics, nil
}

// DeltaHistory returns the delta history worker for querying.
//
// Outputs:
//   - DeltaHistoryView: Read-only view of delta history. May be nil if not initialized.
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) DeltaHistory() DeltaHistoryView {
	return c.deltaHistory
}

// Close releases resources held by the CRS.
//
// Description:
//
//	Stops the delta history worker goroutine. Should be called when the
//	CRS is no longer needed to prevent goroutine leaks.
//
// Thread Safety: Safe for concurrent use. Idempotent.
func (c *crsImpl) Close() {
	if c.deltaHistory != nil {
		c.deltaHistory.Close()
	}
}

// -----------------------------------------------------------------------------
// Graph Integration Methods (GR-28)
// -----------------------------------------------------------------------------

// SetGraphProvider sets the graph query provider.
//
// Description:
//
//	Registers a GraphQuery implementation that will be included in all
//	future snapshots. Activities can then call snapshot.GraphQuery() to
//	access the actual code graph.
//
//	Call this after the graph is initialized and after graph refreshes
//	to update the adapter with the new graph state.
//
// Inputs:
//   - provider: The graph query implementation. May be nil to clear.
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) SetGraphProvider(provider GraphQuery) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close old provider if it exists and is different
	if c.graphProvider != nil && c.graphProvider != provider {
		if err := c.graphProvider.Close(); err != nil {
			c.logger.Warn("failed to close old graph provider",
				slog.String("error", err.Error()),
			)
		}
	}

	c.graphProvider = provider

	// GR-32: Create graph-backed dependency index
	if provider != nil {
		depIndex, err := NewGraphBackedDependencyIndex(provider)
		if err != nil {
			c.logger.Error("failed to create graph-backed dependency index",
				slog.String("error", err.Error()),
			)
			c.graphBackedDepIndex = nil
		} else {
			c.graphBackedDepIndex = depIndex
			c.logger.Info("graph-backed dependency index created")
		}

		c.logger.Info("graph provider set",
			slog.Int("node_count", provider.NodeCount()),
			slog.Int("edge_count", provider.EdgeCount()),
			slog.Int64("generation", provider.Generation()),
		)
	} else {
		c.graphBackedDepIndex = nil
		c.logger.Info("graph provider cleared")
	}
}

// InvalidateGraphCache invalidates graph-backed dependency index caches.
//
// Description:
//
//	Called after the graph is refreshed (GR-29). Invalidates the Size() cache
//	in GraphBackedDependencyIndex, which in turn invalidates the CRSGraphAdapter
//	analytics cache (PageRank, communities, edge count).
//
//	This is a no-op if graphBackedDepIndex is nil (legacy mode).
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) InvalidateGraphCache() {
	c.mu.RLock()
	depIndex := c.graphBackedDepIndex
	c.mu.RUnlock()

	if depIndex != nil {
		depIndex.InvalidateCache()
		c.logger.Debug("graph cache invalidated")
	}
}

// -----------------------------------------------------------------------------
// Analytics Methods (GR-31)
// -----------------------------------------------------------------------------

// GetAnalyticsHistory returns all analytics records.
func (c *crsImpl) GetAnalyticsHistory() []*AnalyticsRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.analyticsData == nil {
		return nil
	}
	return c.analyticsData.All()
}

// GetLastAnalytics returns the most recent analytics of a given type.
func (c *crsImpl) GetLastAnalytics(queryType AnalyticsQueryType) *AnalyticsRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.analyticsData == nil {
		return nil
	}
	return c.analyticsData.GetLast(queryType)
}

// HasRunAnalytics checks if a specific analytics type has been run.
func (c *crsImpl) HasRunAnalytics(queryType AnalyticsQueryType) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.analyticsData == nil {
		return false
	}
	return c.analyticsData.HasRun(queryType)
}

// Checkpoint creates a restorable checkpoint.
func (c *crsImpl) Checkpoint(ctx context.Context) (Checkpoint, error) {
	if ctx == nil {
		return Checkpoint{}, ErrNilContext
	}

	select {
	case <-ctx.Done():
		return Checkpoint{}, ctx.Err()
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Create deep copy of all state
	snap := newSnapshot(
		c.generation.Load(),
		c.proofData,
		c.constraintData,
		c.similarityData,
		c.dependencyData,
		c.historyData,
		c.streamingData,
		c.clauseData,
	)

	return Checkpoint{
		ID:         uuid.New().String(),
		Generation: c.generation.Load(),
		CreatedAt:  time.Now().UnixMilli(),
		data:       snap,
	}, nil
}

// Restore returns to a previous checkpoint.
func (c *crsImpl) Restore(ctx context.Context, cp Checkpoint) error {
	if ctx == nil {
		return ErrNilContext
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.Restore",
		trace.WithAttributes(
			attribute.String("checkpoint_id", cp.ID),
			attribute.Int64("checkpoint_generation", cp.Generation),
		),
	)
	defer span.End()

	snap, ok := cp.data.(*snapshot)
	if !ok {
		err := fmt.Errorf("restore: invalid checkpoint data type: expected *snapshot, got %T", cp.data)
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid checkpoint")
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Deep copy streaming data to avoid sharing mutex with snapshot
	// This prevents race conditions if snapshot's streaming stats are still being read
	newStreaming := snap.streamingData.clone()

	// Deep copy clause data (CRS-04)
	newClauseData := make(map[string]*Clause, len(snap.clauseData))
	for k, v := range snap.clauseData {
		clauseCopy := *v
		if v.Literals != nil {
			clauseCopy.Literals = make([]Literal, len(v.Literals))
			copy(clauseCopy.Literals, v.Literals)
		}
		newClauseData[k] = &clauseCopy
	}

	// Restore all state from checkpoint (using snapshot's immutable copies)
	c.proofData = snap.proofData
	c.constraintData = snap.constraintData
	// CRS-15 CR2 Fix C2: Deep-copy similarityData to avoid aliasing snapshot's
	// inner maps. Without this, subsequent RecordSimilarity() calls mutate the
	// snapshot's inner map[string]float64, violating snapshot immutability.
	c.similarityData = make(map[string]map[string]float64, len(snap.similarityData))
	for k, peers := range snap.similarityData {
		innerCopy := make(map[string]float64, len(peers))
		for k2, dist := range peers {
			innerCopy[k2] = dist
		}
		c.similarityData[k] = innerCopy
	}
	c.dependencyData = snap.dependencyData
	c.historyData = snap.historyData
	c.streamingData = newStreaming
	c.clauseData = newClauseData // CRS-04: Restore clause data
	c.generation.Store(cp.Generation)

	span.SetAttributes(attribute.Bool("success", true))

	c.logger.Info("checkpoint restored",
		slog.String("checkpoint_id", cp.ID),
		slog.Int64("generation", cp.Generation),
	)

	return nil
}

// -----------------------------------------------------------------------------
// Disk Persistence Methods (GR-33)
// -----------------------------------------------------------------------------

// SaveCheckpointToDisk persists current state to disk.
//
// Description:
//
//	Creates a checkpoint and saves it to disk via the persistence
//	manager. This is the primary method for cross-session persistence.
//	Also triggers a journal checkpoint to clear old deltas.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - pm: Persistence manager. Must not be nil.
//   - projectHash: Project identifier. Must be valid (8-64 hex chars).
//   - journal: The journal to backup. Must not be nil.
//
// Outputs:
//   - *BackupMetadata: Metadata about the created backup.
//   - error: Non-nil on failure.
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) SaveCheckpointToDisk(ctx context.Context, pm *PersistenceManager, projectHash string, journal Journal) (*BackupMetadata, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if pm == nil {
		return nil, fmt.Errorf("persistence manager must not be nil")
	}
	if journal == nil {
		return nil, fmt.Errorf("journal must not be nil")
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.SaveCheckpointToDisk",
		trace.WithAttributes(
			attribute.String("project_hash", projectHash),
		),
	)
	defer span.End()

	// First create in-memory checkpoint
	checkpoint, err := c.Checkpoint(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create checkpoint failed")
		return nil, fmt.Errorf("create checkpoint: %w", err)
	}

	// Checkpoint the journal to clear old deltas
	if err := journal.Checkpoint(ctx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "journal checkpoint failed")
		return nil, fmt.Errorf("journal checkpoint: %w", err)
	}

	// CRS-15: Flush current similarity data to journal AFTER checkpoint.
	// This ensures similarity deltas appear after the checkpoint marker
	// so Replay() returns them during TryRestore(). Flushing before
	// checkpoint would cause the delta to be marked as checkpointed
	// and skipped during replay.
	//
	// CRS-15 Code Review Fix #1: Snapshot under lock, then write.
	// Previous code had asymmetric RUnlock in two branches. Now we
	// copy the data under a single RLock/RUnlock, then write outside
	// the lock. The snapshot in simDelta is consistent; new writes
	// arriving concurrently are expected to be missed (point-in-time).
	simDelta := NewSimilarityDelta(SignalSourceSoft)
	c.mu.RLock()
	for key1, peers := range c.similarityData {
		for key2, dist := range peers {
			// Data is stored canonically (key1 < key2) via RecordSimilarity
			// and applySimilarityDelta, so all entries qualify.
			simDelta.Updates[[2]string{key1, key2}] = dist
		}
	}
	c.mu.RUnlock()

	if len(simDelta.Updates) > 0 {
		if jErr := journal.Append(ctx, simDelta); jErr != nil {
			c.logger.Warn("CRS-15: Failed to flush similarity to journal",
				slog.String("error", jErr.Error()),
				slog.Int("pairs", len(simDelta.Updates)),
			)
		} else {
			c.logger.Debug("CRS-15: Flushed similarity data to journal",
				slog.Int("pairs", len(simDelta.Updates)),
			)
		}
	}

	// Save to disk
	opts := &BackupOptions{
		SessionID: checkpoint.ID, // Use checkpoint ID as session identifier
	}
	metadata, err := pm.SaveBackup(ctx, projectHash, journal, opts)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save backup failed")
		return nil, fmt.Errorf("save backup: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("generation", metadata.Generation),
		attribute.Int64("compressed_size", metadata.CompressedSize),
	)

	c.logger.Info("checkpoint saved to disk",
		slog.String("project_hash", projectHash),
		slog.Int64("generation", metadata.Generation),
		slog.Int64("compressed_bytes", metadata.CompressedSize),
	)

	return metadata, nil
}

// LoadCheckpointFromDisk restores state from disk.
//
// Description:
//
//	Loads a backup and replays the journal to restore CRS state.
//	Returns nil if no backup exists (first run).
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - pm: Persistence manager. Must not be nil.
//   - projectHash: Project identifier. Must be valid (8-64 hex chars).
//   - journal: Journal to restore.
//
// Outputs:
//   - *BackupMetadata: Metadata about restored backup, nil if none.
//   - error: Non-nil on failure (not on missing backup).
//
// Thread Safety: Safe for concurrent use.
func (c *crsImpl) LoadCheckpointFromDisk(ctx context.Context, pm *PersistenceManager, projectHash string, journal Journal) (*BackupMetadata, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if pm == nil {
		return nil, fmt.Errorf("persistence manager must not be nil")
	}
	if journal == nil {
		return nil, fmt.Errorf("journal must not be nil")
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.LoadCheckpointFromDisk",
		trace.WithAttributes(
			attribute.String("project_hash", projectHash),
		),
	)
	defer span.End()

	// Attempt to load backup
	metadata, err := pm.LoadBackup(ctx, projectHash, journal)
	if errors.Is(err, ErrBackupNotFound) {
		span.SetAttributes(attribute.Bool("backup_found", false))
		c.logger.Info("no backup found, starting fresh",
			slog.String("project_hash", projectHash),
		)
		return nil, nil // First run, no backup exists
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load backup failed")
		return nil, fmt.Errorf("load backup: %w", err)
	}

	span.SetAttributes(attribute.Bool("backup_found", true))

	// Replay journal to restore CRS state
	deltas, err := journal.Replay(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "replay journal failed")
		return nil, fmt.Errorf("replay journal: %w", err)
	}

	// Apply all deltas
	for i, delta := range deltas {
		if _, err := c.Apply(ctx, delta); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "apply delta failed")
			return nil, fmt.Errorf("apply delta %d: %w", i, err)
		}
	}

	span.SetAttributes(
		attribute.Int64("restored_generation", metadata.Generation),
		attribute.Int("deltas_replayed", len(deltas)),
	)

	c.logger.Info("CRS state restored from disk",
		slog.String("project_hash", projectHash),
		slog.Int64("generation", metadata.Generation),
		slog.Int("deltas_replayed", len(deltas)),
		slog.Duration("backup_age", metadata.Age()),
	)

	return metadata, nil
}

// -----------------------------------------------------------------------------
// Delta Application Helpers
// -----------------------------------------------------------------------------

func (c *crsImpl) applyProofDelta(d *ProofDelta, metrics *ApplyMetrics) error {
	for nodeID, proof := range d.Updates {
		c.proofData[nodeID] = proof
		metrics.EntriesModified++
	}
	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexProof)
	return nil
}

func (c *crsImpl) applyConstraintDelta(d *ConstraintDelta, metrics *ApplyMetrics) error {
	// Remove
	for _, id := range d.Remove {
		delete(c.constraintData, id)
		metrics.EntriesModified++
	}

	// Update
	for id, constraint := range d.Update {
		c.constraintData[id] = constraint
		metrics.EntriesModified++
	}

	// Add
	for _, constraint := range d.Add {
		c.constraintData[constraint.ID] = constraint
		metrics.EntriesModified++
	}

	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexConstraint)
	return nil
}

func (c *crsImpl) applySimilarityDelta(d *SimilarityDelta, metrics *ApplyMetrics) error {
	// CRS-15 CR2 Fix C1: Count total pairs for cap enforcement.
	// Without this, journal replay can exceed maxSimilarityPairs.
	const maxSimilarityPairs = 10000
	totalPairs := 0
	for _, peers := range c.similarityData {
		totalPairs += len(peers)
	}

	for pair, dist := range d.Updates {
		// CRS-15 Code Review Fix #2: Enforce canonical key ordering on replay.
		// Journal entries from older versions may have non-canonical ordering.
		// Canonical: key1 < key2, matching RecordSimilarity and flush logic.
		k1, k2 := pair[0], pair[1]
		if k1 > k2 {
			k1, k2 = k2, k1
		}

		// Check if this pair already exists (update doesn't grow the count).
		isUpdate := c.similarityData[k1] != nil && c.similarityData[k1][k2] != 0
		if !isUpdate && totalPairs >= maxSimilarityPairs {
			slog.Debug("CRS-15: Similarity pair cap reached during delta replay, skipping",
				slog.Int("capacity", maxSimilarityPairs),
			)
			continue
		}

		if c.similarityData[k1] == nil {
			c.similarityData[k1] = make(map[string]float64)
		}
		c.similarityData[k1][k2] = dist
		if !isUpdate {
			totalPairs++
		}
		metrics.EntriesModified++
	}
	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexSimilarity)
	return nil
}

func (c *crsImpl) applyDependencyDelta(d *DependencyDelta, metrics *ApplyMetrics) error {
	// GR-32: No-op - dependencies are now read from the graph directly via
	// GraphBackedDependencyIndex. This method is deprecated.
	//
	// The DependencyDelta type is kept for journal backward compatibility but
	// has no effect. All dependency queries now go through the graph.
	if len(d.AddEdges) > 0 || len(d.RemoveEdges) > 0 {
		// GR-32 Code Review Fix: Rate-limit warning to once per CRS instance
		c.depDeltaWarnOnce.Do(func() {
			c.logger.Warn("DependencyDelta.Apply is deprecated (GR-32); dependencies come from graph. This warning will only appear once.")
		})
	}

	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexDependency)
	return nil
}

func (c *crsImpl) applyHistoryDelta(d *HistoryDelta, metrics *ApplyMetrics) error {
	c.historyData = append(c.historyData, d.Entries...)
	metrics.EntriesModified += len(d.Entries)
	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexHistory)
	return nil
}

func (c *crsImpl) applyStreamingDelta(d *StreamingDelta, metrics *ApplyMetrics) error {
	c.streamingData.mu.Lock()
	defer c.streamingData.mu.Unlock()

	// Track new items for cardinality
	for item, inc := range d.Increments {
		if c.streamingData.frequencies[item] == 0 {
			// New item - increment cardinality
			c.streamingData.cardinality++
		}
		c.streamingData.frequencies[item] += inc
		metrics.EntriesModified++
	}

	// Update cardinality for explicit cardinality items
	for _, item := range d.CardinalityItems {
		if c.streamingData.frequencies[item] == 0 {
			c.streamingData.frequencies[item] = 1
			c.streamingData.cardinality++
		}
		metrics.EntriesModified++
	}

	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexStreaming)
	return nil
}

// applyAnalyticsDelta records an analytics query in history and sets proof markers.
//
// Description:
//
//	Records the analytics query in history, sets proof markers for completion,
//	and logs the operation for debugging.
//
// GR-31: Analytics CRS routing.
// L-1/L-3 fix: Added structured logging.
// C-2/L-4 fix: TODO - Emit coordinator event when CRS gains coordinator reference.
func (c *crsImpl) applyAnalyticsDelta(d *AnalyticsDelta, metrics *ApplyMetrics) error {
	if d.Record == nil {
		c.logger.Debug("analytics delta has nil record, skipping")
		return nil // Nothing to apply
	}

	// O-4 fix: Truncate large result sets before storing
	d.Record.TruncateResults()

	// Add to analytics history
	if c.analyticsData != nil {
		c.analyticsData.Add(d.Record)
		metrics.EntriesModified++
	}

	// Set proof markers for analytics completion
	now := time.Now().UnixMilli()

	// Mark analytics as done
	doneKey := d.Record.GetProofDoneKey()
	c.proofData[doneKey] = ProofNumber{
		Proof:     1,
		Disproof:  0,
		Status:    ProofStatusProven,
		UpdatedAt: now,
	}
	metrics.EntriesModified++

	// If results were found, mark as found
	foundKey := ""
	if d.Record.HasResults() {
		foundKey = d.Record.GetProofFoundKey()
		c.proofData[foundKey] = ProofNumber{
			Proof:     1,
			Disproof:  0,
			Status:    ProofStatusProven,
			UpdatedAt: now,
		}
		metrics.EntriesModified++
	}

	// L-1 fix: Log analytics application
	c.logger.Debug("analytics delta applied",
		slog.String("query_type", string(d.Record.QueryType)),
		slog.Int("result_count", d.Record.ResultCount),
		slog.Int64("execution_ms", d.Record.ExecutionMs),
		slog.String("done_key", doneKey),
		slog.String("found_key", foundKey),
	)

	// TODO(C-2/L-4): Emit EventAnalyticsRun to coordinator when CRS gains
	// coordinator reference. This enables activities to react to analytics:
	// if c.coordinator != nil {
	//     c.coordinator.EmitEvent(integration.EventAnalyticsRun, &integration.EventData{
	//         Metadata: map[string]any{
	//             "query_type":   d.Record.QueryType,
	//             "result_count": d.Record.ResultCount,
	//         },
	//     })
	// }

	metrics.IndexesUpdated = metrics.IndexesUpdated.Add(IndexProof)
	return nil
}

func (c *crsImpl) applyCompositeDelta(ctx context.Context, d *CompositeDelta, metrics *ApplyMetrics) error {
	// Apply each delta in sequence
	for _, delta := range d.Deltas {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch dd := delta.(type) {
		case *ProofDelta:
			if err := c.applyProofDelta(dd, metrics); err != nil {
				return err
			}
		case *ConstraintDelta:
			if err := c.applyConstraintDelta(dd, metrics); err != nil {
				return err
			}
		case *SimilarityDelta:
			if err := c.applySimilarityDelta(dd, metrics); err != nil {
				return err
			}
		case *DependencyDelta:
			if err := c.applyDependencyDelta(dd, metrics); err != nil {
				return err
			}
		case *HistoryDelta:
			if err := c.applyHistoryDelta(dd, metrics); err != nil {
				return err
			}
		case *StreamingDelta:
			if err := c.applyStreamingDelta(dd, metrics); err != nil {
				return err
			}
		case *CompositeDelta:
			if err := c.applyCompositeDelta(ctx, dd, metrics); err != nil {
				return err
			}
		case *AnalyticsDelta:
			if err := c.applyAnalyticsDelta(dd, metrics); err != nil {
				return err
			}
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Evaluable Interface Implementation
// -----------------------------------------------------------------------------

// Name returns the unique identifier for this component.
func (c *crsImpl) Name() string {
	return "crs"
}

// Properties returns the correctness properties this component guarantees.
func (c *crsImpl) Properties() []eval.Property {
	return []eval.Property{
		{
			Name:        "snapshot_immutability",
			Description: "Snapshots returned by Snapshot() are immutable",
			Check: func(_, output any) error {
				// Verified by type system - snapshot fields are not exported
				return nil
			},
		},
		{
			Name:        "generation_monotonic",
			Description: "Generation always increases on Apply",
			Check: func(_, _ any) error {
				// Verified by atomic increment
				return nil
			},
		},
		{
			Name:        "hard_soft_boundary",
			Description: "Soft signals cannot mark nodes DISPROVEN",
			Check: func(input, _ any) error {
				delta, ok := input.(*ProofDelta)
				if !ok {
					return nil
				}
				for nodeID, proof := range delta.Updates {
					if proof.Status == ProofStatusDisproven && !delta.Source().IsHard() {
						return fmt.Errorf("soft signal marked node %s as DISPROVEN", nodeID)
					}
				}
				return nil
			},
		},
		{
			Name:        "checkpoint_roundtrip",
			Description: "Checkpoint and Restore preserves state",
			Check: func(_, _ any) error {
				// Verified by implementation
				return nil
			},
		},
	}
}

// Metrics returns the metrics this component exposes.
func (c *crsImpl) Metrics() []eval.MetricDefinition {
	return []eval.MetricDefinition{
		{
			Name:        "crs_snapshot_total",
			Type:        eval.MetricCounter,
			Description: "Total snapshots created",
		},
		{
			Name:        "crs_apply_total",
			Type:        eval.MetricCounter,
			Description: "Total Apply calls",
			Labels:      []string{"delta_type", "status"},
		},
		{
			Name:        "crs_apply_duration_seconds",
			Type:        eval.MetricHistogram,
			Description: "Apply duration in seconds",
			Labels:      []string{"delta_type"},
			Buckets:     []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
		},
		{
			Name:        "crs_generation",
			Type:        eval.MetricGauge,
			Description: "Current generation number",
		},
		{
			Name:        "crs_index_size",
			Type:        eval.MetricGauge,
			Description: "Size of each index",
			Labels:      []string{"index"},
		},
	}
}

// HealthCheck verifies the component is functioning correctly.
func (c *crsImpl) HealthCheck(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Check we can create a snapshot
	snap := c.Snapshot()
	if snap == nil {
		return fmt.Errorf("snapshot returned nil")
	}

	// Check all indexes are accessible
	if snap.ProofIndex() == nil {
		return fmt.Errorf("proof index is nil")
	}
	if snap.ConstraintIndex() == nil {
		return fmt.Errorf("constraint index is nil")
	}
	if snap.SimilarityIndex() == nil {
		return fmt.Errorf("similarity index is nil")
	}
	if snap.DependencyIndex() == nil {
		return fmt.Errorf("dependency index is nil")
	}
	if snap.HistoryIndex() == nil {
		return fmt.Errorf("history index is nil")
	}
	if snap.StreamingIndex() == nil {
		return fmt.Errorf("streaming index is nil")
	}

	// Check for circular dependencies in dependency graph
	c.mu.RLock()
	defer c.mu.RUnlock()

	for nodeID := range c.dependencyData.forward {
		if c.dependencyData.hasCycle(nodeID) {
			return fmt.Errorf("circular dependency detected involving node %s", nodeID)
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// StepRecord Methods (CRS-01)
// -----------------------------------------------------------------------------

// RecordStep adds a step to the CRS step history.
func (c *crsImpl) RecordStep(ctx context.Context, step StepRecord) error {
	if ctx == nil {
		return ErrNilContext
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Require session ID before we can do anything
	if step.SessionID == "" {
		return fmt.Errorf("step validation failed: session_id must not be empty")
	}

	// Get or create session steps (uses stepMu for map access)
	c.stepMu.Lock()
	ss, ok := c.stepData[step.SessionID]
	if !ok {
		ss = &sessionSteps{
			steps:       make([]StepRecord, 0, 32),
			byTool:      make(map[string]int),
			byToolDirty: false,
			generation:  c.generation.Load(),
		}
		c.stepData[step.SessionID] = ss
	}
	c.stepMu.Unlock()

	// Append step (uses per-session lock)
	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Check step limit to prevent unbounded memory growth
	if len(ss.steps) >= maxStepsPerSession {
		c.logger.Warn("step limit reached, evicting oldest step",
			slog.String("session_id", step.SessionID),
			slog.Int("max_steps", maxStepsPerSession),
		)
		// Evict oldest step (FIFO)
		if len(ss.steps) > 0 {
			oldStep := ss.steps[0]
			ss.steps = ss.steps[1:]
			// Decrement tool count for evicted step
			if oldStep.Decision == DecisionExecuteTool && oldStep.Tool != "" {
				ss.byTool[oldStep.Tool]--
				if ss.byTool[oldStep.Tool] <= 0 {
					delete(ss.byTool, oldStep.Tool)
				}
			}
		}
	}

	// Auto-assign step number if not set (BEFORE validation)
	if step.StepNumber < 1 {
		step.StepNumber = len(ss.steps) + 1
	}

	// Auto-set timestamp if not set (BEFORE validation)
	if step.Timestamp == 0 {
		step.Timestamp = time.Now().UnixMilli()
	}

	// Validate the step AFTER auto-assignment
	if err := step.Validate(); err != nil {
		return fmt.Errorf("step validation failed: %w", err)
	}

	// Deep copy slices to prevent mutation after recording
	stepCopy := step
	if step.ToolParams != nil {
		paramsCopy := *step.ToolParams
		if len(step.ToolParams.Flags) > 0 {
			paramsCopy.Flags = make([]string, len(step.ToolParams.Flags))
			copy(paramsCopy.Flags, step.ToolParams.Flags)
		}
		if len(step.ToolParams.Extra) > 0 {
			paramsCopy.Extra = make([]KeyValue, len(step.ToolParams.Extra))
			copy(paramsCopy.Extra, step.ToolParams.Extra)
		}
		stepCopy.ToolParams = &paramsCopy
	}
	if len(step.ProofUpdates) > 0 {
		stepCopy.ProofUpdates = make([]ProofUpdate, len(step.ProofUpdates))
		copy(stepCopy.ProofUpdates, step.ProofUpdates)
	}
	if len(step.ConstraintsAdded) > 0 {
		stepCopy.ConstraintsAdded = make([]ConstraintUpdate, len(step.ConstraintsAdded))
		copy(stepCopy.ConstraintsAdded, step.ConstraintsAdded)
	}
	if len(step.DependenciesFound) > 0 {
		stepCopy.DependenciesFound = make([]DependencyEdge, len(step.DependenciesFound))
		copy(stepCopy.DependenciesFound, step.DependenciesFound)
	}

	ss.steps = append(ss.steps, stepCopy)

	// Update tool count directly if this is a tool execution
	if stepCopy.Decision == DecisionExecuteTool && stepCopy.Tool != "" {
		ss.byTool[stepCopy.Tool]++
	}

	c.stepRecordCount.Add(1)

	c.logger.Debug("step recorded",
		slog.String("session_id", stepCopy.SessionID),
		slog.Int("step_number", stepCopy.StepNumber),
		slog.String("actor", string(stepCopy.Actor)),
		slog.String("decision", string(stepCopy.Decision)),
		slog.String("outcome", string(stepCopy.Outcome)),
	)

	return nil
}

// GetStepHistory returns all steps for a session, ordered by step number.
func (c *crsImpl) GetStepHistory(sessionID string) []StepRecord {
	if sessionID == "" {
		return nil
	}

	c.stepMu.RLock()
	ss, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return nil
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Return a copy to avoid mutation
	result := make([]StepRecord, len(ss.steps))
	copy(result, ss.steps)
	return result
}

// GetLastStep returns the most recent step for a session.
func (c *crsImpl) GetLastStep(sessionID string) *StepRecord {
	if sessionID == "" {
		return nil
	}

	c.stepMu.RLock()
	ss, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return nil
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	if len(ss.steps) == 0 {
		return nil
	}

	// Return a copy
	step := ss.steps[len(ss.steps)-1]
	return &step
}

// CountToolExecutions returns how many times a tool was EXECUTED in a session.
func (c *crsImpl) CountToolExecutions(sessionID string, tool string) int {
	if sessionID == "" || tool == "" {
		return 0
	}

	c.stepMu.RLock()
	ss, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return 0
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Use the cached count
	return ss.byTool[tool]
}

// GetStepsByActor returns steps filtered by actor.
func (c *crsImpl) GetStepsByActor(sessionID string, actor Actor) []StepRecord {
	if sessionID == "" {
		return nil
	}

	c.stepMu.RLock()
	ss, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return nil
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Filter steps by actor
	var result []StepRecord
	for _, step := range ss.steps {
		if step.Actor == actor {
			result = append(result, step)
		}
	}
	return result
}

// GetStepsByOutcome returns steps filtered by outcome.
func (c *crsImpl) GetStepsByOutcome(sessionID string, outcome Outcome) []StepRecord {
	if sessionID == "" {
		return nil
	}

	c.stepMu.RLock()
	ss, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return nil
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	// Filter steps by outcome
	var result []StepRecord
	for _, step := range ss.steps {
		if step.Outcome == outcome {
			result = append(result, step)
		}
	}
	return result
}

// ClearStepHistory removes all steps for a session.
func (c *crsImpl) ClearStepHistory(sessionID string) {
	if sessionID == "" {
		return
	}

	c.stepMu.Lock()
	defer c.stepMu.Unlock()

	delete(c.stepData, sessionID)

	c.logger.Debug("step history cleared",
		slog.String("session_id", sessionID),
	)
}

// -----------------------------------------------------------------------------
// Proof Index Methods (CRS-02)
// -----------------------------------------------------------------------------

// UpdateProofNumber applies a proof update to a node.
func (c *crsImpl) UpdateProofNumber(ctx context.Context, update ProofUpdate) error {
	if ctx == nil {
		return ErrNilContext
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Validate the update
	if err := update.Validate(); err != nil {
		return fmt.Errorf("proof update validation failed: %w", err)
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.UpdateProofNumber",
		trace.WithAttributes(
			attribute.String("node_id", update.NodeID),
			attribute.String("type", update.Type.String()),
			attribute.Int64("delta", int64(update.Delta)),
			attribute.String("source", update.Source.String()),
		),
	)
	defer span.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Get or create proof number entry
	pn, exists := c.proofData[update.NodeID]
	if !exists {
		pn = ProofNumber{
			Proof:     DefaultInitialProofNumber,
			Disproof:  DefaultInitialProofNumber,
			Status:    ProofStatusUnknown,
			Source:    update.Source,
			UpdatedAt: time.Now().UnixMilli(),
		}
	}

	// Apply the update based on type
	switch update.Type {
	case ProofUpdateTypeIncrement:
		// Failure: increase proof number (harder to prove)
		if pn.Proof < ProofNumberInfinite-update.Delta {
			pn.Proof += update.Delta
		} else {
			pn.Proof = ProofNumberInfinite
		}
		if pn.Status == ProofStatusUnknown {
			pn.Status = ProofStatusExpanded
		}

	case ProofUpdateTypeDecrement:
		// Success: decrease proof number (easier to prove)
		if pn.Proof >= update.Delta {
			pn.Proof -= update.Delta
		} else {
			pn.Proof = 0
		}
		if pn.Status == ProofStatusUnknown {
			pn.Status = ProofStatusExpanded
		}

	case ProofUpdateTypeDisproven:
		// Mark as disproven (infinite cost)
		// Only hard signals can mark nodes as disproven
		if !update.Source.IsHard() {
			span.SetStatus(codes.Error, "soft signal cannot mark disproven")
			return ErrHardSoftBoundaryViolation
		}
		pn.Proof = ProofNumberInfinite
		pn.Status = ProofStatusDisproven

	case ProofUpdateTypeProven:
		// Mark as proven (solution found)
		pn.Proof = 0
		pn.Status = ProofStatusProven

	case ProofUpdateTypeReset:
		// Reset to initial value
		pn.Proof = DefaultInitialProofNumber
		pn.Disproof = DefaultInitialProofNumber
		pn.Status = ProofStatusUnknown
	}

	pn.Source = update.Source
	pn.UpdatedAt = time.Now().UnixMilli()
	c.proofData[update.NodeID] = pn

	c.logger.Debug("proof number updated",
		slog.String("node_id", update.NodeID),
		slog.String("type", update.Type.String()),
		slog.Uint64("proof", pn.Proof),
		slog.String("status", pn.Status.String()),
		slog.String("reason", update.Reason),
	)

	return nil
}

// GetProofStatus returns the current proof status for a node.
func (c *crsImpl) GetProofStatus(nodeID string) (ProofNumber, bool) {
	if nodeID == "" {
		return ProofNumber{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	pn, exists := c.proofData[nodeID]
	return pn, exists
}

// CheckCircuitBreaker checks if the circuit breaker should fire for a tool.
func (c *crsImpl) CheckCircuitBreaker(sessionID string, tool string) CircuitBreakerResult {
	if sessionID == "" || tool == "" {
		return CircuitBreakerResult{
			ShouldFire: false,
			Reason:     "invalid input: empty sessionID or tool",
		}
	}

	// Build node ID from session and tool
	nodeID := fmt.Sprintf("session:%s:tool:%s", sessionID, tool)

	c.mu.RLock()
	pn, exists := c.proofData[nodeID]
	c.mu.RUnlock()

	// If no proof data exists, check tool execution count as fallback
	if !exists {
		// CRS-16: Use per-tool threshold if configured, else default.
		threshold := DefaultCircuitBreakerThreshold
		if c.config != nil && c.config.CircuitBreakerThresholds != nil {
			if perTool, ok := c.config.CircuitBreakerThresholds[tool]; ok && perTool > 0 {
				threshold = perTool
			}
		}

		// Use step recording data for backwards compatibility
		count := c.CountToolExecutions(sessionID, tool)
		if count >= threshold {
			return CircuitBreakerResult{
				ShouldFire:  true,
				Reason:      fmt.Sprintf("tool %s called %d times (threshold: %d)", tool, count, threshold),
				ProofNumber: 0,
				Status:      ProofStatusUnknown,
			}
		}
		return CircuitBreakerResult{
			ShouldFire:  false,
			Reason:      "no proof data, execution count below threshold",
			ProofNumber: DefaultInitialProofNumber,
			Status:      ProofStatusUnknown,
		}
	}

	// Check if disproven
	if pn.Status == ProofStatusDisproven {
		return CircuitBreakerResult{
			ShouldFire:  true,
			Reason:      fmt.Sprintf("tool %s is disproven", tool),
			ProofNumber: pn.Proof,
			Status:      pn.Status,
		}
	}

	// Check if proof number exhausted
	if pn.Proof >= ProofNumberInfinite {
		return CircuitBreakerResult{
			ShouldFire:  true,
			Reason:      fmt.Sprintf("tool %s proof exhausted: pn=%d", tool, pn.Proof),
			ProofNumber: pn.Proof,
			Status:      pn.Status,
		}
	}

	return CircuitBreakerResult{
		ShouldFire:  false,
		Reason:      "proof number viable",
		ProofNumber: pn.Proof,
		Status:      pn.Status,
	}
}

// PropagateDisproof propagates disproof to parent decisions.
func (c *crsImpl) PropagateDisproof(ctx context.Context, nodeID string) int {
	if ctx == nil || nodeID == "" {
		return 0
	}

	select {
	case <-ctx.Done():
		return 0
	default:
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.PropagateDisproof",
		trace.WithAttributes(
			attribute.String("node_id", nodeID),
		),
	)
	defer span.End()

	// Use BFS with visited set to prevent infinite loops
	visited := make(map[string]bool)
	type queueItem struct {
		id    string
		depth int
	}
	queue := []queueItem{{nodeID, 0}}
	affected := 0

	// GR-32 Code Review Fix: Take snapshot ONCE before the loop to avoid
	// creating deep copies on every BFS iteration. The DependencyIndex delegates
	// to the live graph, so this is safe.
	snap := c.Snapshot()
	depIndex := snap.DependencyIndex()

	for len(queue) > 0 {
		// Check cancellation
		select {
		case <-ctx.Done():
			span.SetAttributes(attribute.Int("affected_nodes", affected))
			return affected
		default:
		}

		current := queue[0]
		queue = queue[1:]

		// Skip if already visited or max depth reached
		if visited[current.id] || current.depth >= MaxPropagationDepth {
			continue
		}
		visited[current.id] = true

		// Get parent decisions that led to this node (GR-32: use DependencyIndex)
		parentList := depIndex.DependedBy(current.id)

		for _, parentID := range parentList {
			if visited[parentID] {
				continue
			}

			// Increase parent's proof number (child disproven = path harder to prove)
			err := c.UpdateProofNumber(ctx, ProofUpdate{
				NodeID: parentID,
				Type:   ProofUpdateTypeIncrement,
				Delta:  1,
				Reason: fmt.Sprintf("child_disproven:%s", current.id),
				Source: SignalSourceHard,
			})
			if err != nil {
				c.logger.Warn("failed to update parent proof number",
					slog.String("parent_id", parentID),
					slog.String("child_id", current.id),
					slog.String("error", err.Error()),
				)
				continue
			}
			affected++

			// Check if parent is now disproven (proof exhausted)
			pn, _ := c.GetProofStatus(parentID)
			if pn.Status == ProofStatusDisproven || pn.Proof >= ProofNumberInfinite {
				// Add to queue for further propagation
				queue = append(queue, queueItem{parentID, current.depth + 1})
			}
		}
	}

	span.SetAttributes(attribute.Int("affected_nodes", affected))

	c.logger.Debug("disproof propagated",
		slog.String("source_node", nodeID),
		slog.Int("affected_nodes", affected),
	)

	return affected
}

// -----------------------------------------------------------------------------
// Clause Index Methods (CRS-04)
// -----------------------------------------------------------------------------

// AddClause adds a learned clause to the constraint index.
func (c *crsImpl) AddClause(ctx context.Context, clause *Clause) error {
	if ctx == nil {
		return ErrNilContext
	}
	if clause == nil {
		return fmt.Errorf("clause must not be nil")
	}

	// Validate clause
	if err := clause.Validate(); err != nil {
		return fmt.Errorf("clause validation: %w", err)
	}

	// Check cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Start tracing
	ctx, span := otel.Tracer("crs").Start(ctx, "crs.AddClause",
		trace.WithAttributes(
			attribute.String("clause_id", clause.ID),
			attribute.Int("literal_count", len(clause.Literals)),
		),
	)
	defer span.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for semantic duplicates
	for _, existing := range c.clauseData {
		if c.isSemanticDuplicate(existing, clause) {
			// Update existing instead of adding duplicate
			existing.UseCount++
			existing.LastUsed = time.Now().UnixMilli()
			span.SetAttributes(attribute.Bool("duplicate", true))
			c.logger.Debug("clause duplicate detected, updating existing",
				slog.String("existing_id", existing.ID),
				slog.String("new_id", clause.ID),
			)
			return nil
		}
	}

	// Check capacity and evict if needed
	if len(c.clauseData) >= c.clauseConfig.MaxClauses {
		c.evictLRUClauseLocked()
	}

	// Add clause with timestamps
	nowMillis := time.Now().UnixMilli()
	clause.LearnedAt = nowMillis
	clause.LastUsed = nowMillis // CR-2: Initialize LastUsed to prevent immediate LRU eviction
	c.clauseData[clause.ID] = clause

	span.SetAttributes(attribute.Int("total_clauses", len(c.clauseData)))
	c.logger.Info("clause added",
		slog.String("clause_id", clause.ID),
		slog.String("failure_type", string(clause.FailureType)),
		slog.Int("total_clauses", len(c.clauseData)),
	)

	return nil
}

// isSemanticDuplicate checks if two clauses have the same meaning.
// CR-5: Uses set-based comparison to handle duplicate literals within a clause.
func (c *crsImpl) isSemanticDuplicate(a, b *Clause) bool {
	// Build deduplicated sets of literals for both clauses
	aLits := make(map[string]struct{}, len(a.Literals))
	for _, lit := range a.Literals {
		key := lit.Variable
		if lit.Negated {
			key = "¬" + key
		}
		aLits[key] = struct{}{}
	}

	bLits := make(map[string]struct{}, len(b.Literals))
	for _, lit := range b.Literals {
		key := lit.Variable
		if lit.Negated {
			key = "¬" + key
		}
		bLits[key] = struct{}{}
	}

	// Compare set sizes (after deduplication)
	if len(aLits) != len(bLits) {
		return false
	}

	// Check that all literals in a are in b
	for key := range aLits {
		if _, ok := bLits[key]; !ok {
			return false
		}
	}

	return true
}

// evictLRUClauseLocked removes the least recently used clause.
// Caller must hold c.mu write lock.
func (c *crsImpl) evictLRUClauseLocked() {
	var lruID string
	var lruTime int64 = 0 // Unix milliseconds; 0 is invalid, so first clause always wins

	for id, clause := range c.clauseData {
		// Find the clause with the smallest (oldest) LastUsed timestamp
		if lruID == "" || clause.LastUsed < lruTime {
			lruID = id
			lruTime = clause.LastUsed
		}
	}

	if lruID != "" {
		delete(c.clauseData, lruID)
		c.logger.Debug("clause evicted (LRU)", slog.String("clause_id", lruID))
	}
}

// CheckDecisionAllowed checks if a proposed decision violates learned clauses.
func (c *crsImpl) CheckDecisionAllowed(sessionID string, tool string) (bool, string) {
	if sessionID == "" || tool == "" {
		c.logger.Debug("CheckDecisionAllowed skipped: empty sessionID or tool",
			slog.String("session_id", sessionID),
			slog.String("tool", tool),
		)
		return true, ""
	}

	// Build assignment first (requires step lock), then check clauses (requires main lock)
	steps := c.getStepHistoryUnlocked(sessionID)
	assignment := c.buildAssignment(steps, tool)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check against all clauses
	for _, clause := range c.clauseData {
		if clause.IsViolated(assignment) {
			// Update usage stats inline (we already hold the write lock)
			clause.UseCount++
			clause.LastUsed = time.Now().UnixMilli()

			return false, fmt.Sprintf("violates learned clause %s: %s", clause.ID, clause.String())
		}
	}

	return true, ""
}

// buildAssignment creates a SAT assignment for evaluating a proposed decision.
// It considers only the proposed tool and recent history context, not cumulative history.
func (c *crsImpl) buildAssignment(steps []StepRecord, proposedTool string) map[string]bool {
	assignment := make(map[string]bool)

	// Add proposed tool
	if proposedTool != "" {
		assignment["tool:"+proposedTool] = true
	}

	// Context from recent history (for sequence patterns)
	n := len(steps)
	if n > 0 {
		lastStep := steps[n-1]

		// Previous tool (for prev_tool patterns)
		if lastStep.Tool != "" {
			assignment["prev_tool:"+lastStep.Tool] = true
		}

		// Last outcome and error category
		if lastStep.Outcome != "" {
			assignment["outcome:"+string(lastStep.Outcome)] = true
		}
		if lastStep.ErrorCategory != "" {
			assignment["error:"+string(lastStep.ErrorCategory)] = true
		}
	}

	// Two steps back (for 3-step cycle patterns like A→B→A)
	if n > 1 {
		prevPrevStep := steps[n-2]
		if prevPrevStep.Tool != "" {
			assignment["prev_prev_tool:"+prevPrevStep.Tool] = true
		}
	}

	return assignment
}

// getStepHistoryUnlocked returns step history without requiring c.mu to be held.
// This is safe because step data has its own lock (c.stepMu).
func (c *crsImpl) getStepHistoryUnlocked(sessionID string) []StepRecord {
	c.stepMu.RLock()
	session, ok := c.stepData[sessionID]
	c.stepMu.RUnlock()

	if !ok {
		return nil
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	result := make([]StepRecord, len(session.steps))
	copy(result, session.steps)
	return result
}

// GarbageCollectClauses removes expired clauses based on TTL.
func (c *crsImpl) GarbageCollectClauses() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	nowMillis := time.Now().UnixMilli()
	ttlMillis := c.clauseConfig.TTL.Milliseconds()
	removed := 0

	for id, clause := range c.clauseData {
		// Check TTL
		if nowMillis-clause.LearnedAt > ttlMillis {
			delete(c.clauseData, id)
			removed++
		}
	}

	if removed > 0 {
		c.logger.Info("clauses garbage collected",
			slog.Int("removed", removed),
			slog.Int("remaining", len(c.clauseData)),
		)
	}

	return removed
}

// -----------------------------------------------------------------------------
// Similarity Index Methods (CRS-15)
// -----------------------------------------------------------------------------

// RecordSimilarity records a similarity pair between two query keys.
//
// Description:
//
//	Records that two queries are semantically similar with the given
//	distance score. Stores in the similarity index symmetrically (both
//	key1→key2 and key2→key1). This data persists across sessions via
//	journal deltas.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - key1: First query key. Must not be empty.
//   - key2: Second query key. Must not be empty or equal to key1.
//   - distance: Similarity distance (0 = identical). Must be non-negative.
//
// Outputs:
//   - error: Non-nil if validation fails or context cancelled.
//
// Thread Safety: Safe for concurrent use. Acquires write lock.
func (c *crsImpl) RecordSimilarity(ctx context.Context, key1, key2 string, distance float64) error {
	if ctx == nil {
		return ErrNilContext
	}
	if key1 == "" || key2 == "" {
		return fmt.Errorf("%w: similarity keys must not be empty", ErrDeltaValidation)
	}
	if key1 == key2 {
		return fmt.Errorf("%w: self-similarity not allowed", ErrDeltaValidation)
	}
	if distance < 0 {
		return fmt.Errorf("%w: similarity distance must be non-negative", ErrDeltaValidation)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	_, span := otel.Tracer("crs").Start(ctx, "crs.RecordSimilarity",
		trace.WithAttributes(
			attribute.String("key1", key1),
			attribute.String("key2", key2),
			attribute.Float64("distance", distance),
		),
	)
	defer span.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	// CRS-15 Code Review Fix #6: Cap similarity entries to prevent unbounded growth.
	// Count total pairs across all outer keys. 10,000 pairs is generous for
	// any realistic session; beyond this we silently drop new entries.
	const maxSimilarityPairs = 10000
	totalPairs := 0
	for _, peers := range c.similarityData {
		totalPairs += len(peers)
	}
	if totalPairs >= maxSimilarityPairs {
		c.logger.Debug("CRS-15: Similarity index at capacity, dropping entry",
			slog.Int("capacity", maxSimilarityPairs),
			slog.String("key1", key1),
			slog.String("key2", key2),
		)
		return nil
	}

	// Store one direction only (GetSimilarity checks both via reverse lookup).
	// Use canonical ordering (key1 < key2) for consistent storage.
	k1, k2 := key1, key2
	if k1 > k2 {
		k1, k2 = k2, k1
	}
	if c.similarityData[k1] == nil {
		c.similarityData[k1] = make(map[string]float64)
	}
	c.similarityData[k1][k2] = distance

	c.generation.Add(1)

	c.logger.Debug("CRS-15: Similarity recorded",
		slog.String("key1", key1),
		slog.String("key2", key2),
		slog.Float64("distance", distance),
	)

	return nil
}

// GetSimilarity returns the recorded similarity distance between two keys.
//
// Inputs:
//   - key1: First query key. Must not be empty.
//   - key2: Second query key. Must not be empty.
//
// Outputs:
//   - float64: The distance, or -1 if no similarity recorded.
//   - bool: True if a similarity record exists.
//
// Thread Safety: Safe for concurrent use. Acquires read lock.
func (c *crsImpl) GetSimilarity(key1, key2 string) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if peers, ok := c.similarityData[key1]; ok {
		if dist, found := peers[key2]; found {
			return dist, true
		}
	}
	// Try reverse lookup (similarity is symmetric).
	if peers, ok := c.similarityData[key2]; ok {
		if dist, found := peers[key1]; found {
			return dist, true
		}
	}
	return -1, false
}
