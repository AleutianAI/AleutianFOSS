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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
)

// -----------------------------------------------------------------------------
// Errors
// -----------------------------------------------------------------------------

var (
	// ErrNilContext is returned when context is nil.
	ErrNilContext = errors.New("context must not be nil")

	// ErrNilDelta is returned when delta is nil.
	ErrNilDelta = errors.New("delta must not be nil")

	// ErrDeltaValidation is returned when delta validation fails.
	ErrDeltaValidation = errors.New("delta validation failed")

	// ErrDeltaConflict is returned when deltas conflict.
	ErrDeltaConflict = errors.New("delta conflict detected")

	// ErrSnapshotStale is returned when snapshot is too old.
	ErrSnapshotStale = errors.New("snapshot is stale")

	// ErrIndexNotFound is returned when an index doesn't exist.
	ErrIndexNotFound = errors.New("index not found")

	// ErrHardSoftBoundaryViolation is returned when soft signal attempts hard action.
	ErrHardSoftBoundaryViolation = errors.New("soft signal cannot perform hard action")

	// ErrApplyRollback is returned when apply partially failed and rolled back.
	ErrApplyRollback = errors.New("apply failed, changes rolled back")
)

// -----------------------------------------------------------------------------
// CRS Interface
// -----------------------------------------------------------------------------

// CRS is the central mutable state container for the Aleutian Hybrid MCTS system.
//
// Description:
//
//	CRS manages 6 indexes that algorithms read from and write to. It provides
//	immutable snapshots for reading and delta-based mutations for writing.
//
// Thread Safety: Safe for concurrent use. Uses RWMutex internally.
type CRS interface {
	eval.Evaluable

	// Snapshot returns an immutable view of the current state.
	//
	// Description:
	//
	//   Creates a copy-on-write snapshot that algorithms can read from safely.
	//   The snapshot is immutable and will not change even if Apply() is called.
	//
	// Outputs:
	//   - Snapshot: The immutable snapshot. Never nil.
	//
	// Thread Safety: Safe for concurrent use.
	Snapshot() Snapshot

	// Apply atomically applies a delta to the state.
	//
	// Description:
	//
	//   Validates the delta, then applies all changes atomically. If any
	//   index update fails, all changes are rolled back.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - delta: The delta to apply. Must not be nil.
	//
	// Outputs:
	//   - ApplyMetrics: Metrics about the apply operation.
	//   - error: Non-nil if apply failed.
	//
	// Thread Safety: Safe for concurrent use. Acquires write lock.
	Apply(ctx context.Context, delta Delta) (ApplyMetrics, error)

	// Generation returns the current state version.
	//
	// Description:
	//
	//   Generation increments with each successful Apply(). Use for cache
	//   invalidation and ordering.
	//
	// Outputs:
	//   - int64: The current generation. Always >= 0.
	//
	// Thread Safety: Safe for concurrent use.
	Generation() int64

	// Checkpoint creates a restorable checkpoint for chaos testing.
	//
	// Description:
	//
	//   Creates a checkpoint that can be restored later. Used for chaos
	//   testing and debugging.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//
	// Outputs:
	//   - Checkpoint: The checkpoint. Never nil on success.
	//   - error: Non-nil if checkpoint creation failed.
	//
	// Thread Safety: Safe for concurrent use.
	Checkpoint(ctx context.Context) (Checkpoint, error)

	// Restore returns to a previous checkpoint.
	//
	// Description:
	//
	//   Restores CRS to the state at the checkpoint. All changes since
	//   the checkpoint are discarded.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - cp: The checkpoint to restore. Must not be nil.
	//
	// Outputs:
	//   - error: Non-nil if restore failed.
	//
	// Thread Safety: Safe for concurrent use. Acquires write lock.
	Restore(ctx context.Context, cp Checkpoint) error

	// -------------------------------------------------------------------------
	// StepRecord Methods (CRS-01)
	// -------------------------------------------------------------------------

	// RecordStep adds a step to the CRS step history.
	//
	// Description:
	//
	//   Validates the step, then atomically appends to the session's history.
	//   Recording is non-blocking - steps are added directly to the index.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - step: The step to record. Must pass Validate().
	//
	// Outputs:
	//   - error: Non-nil if validation fails or context cancelled.
	//
	// Thread Safety: Safe for concurrent use.
	RecordStep(ctx context.Context, step StepRecord) error

	// GetStepHistory returns all steps for a session, ordered by step number.
	//
	// Inputs:
	//   - sessionID: The session to query. Must not be empty.
	//
	// Outputs:
	//   - []StepRecord: Steps in order. Empty slice if session not found.
	//
	// Thread Safety: Safe for concurrent use.
	GetStepHistory(sessionID string) []StepRecord

	// GetLastStep returns the most recent step for a session.
	//
	// Inputs:
	//   - sessionID: The session to query. Must not be empty.
	//
	// Outputs:
	//   - *StepRecord: The last step, or nil if session not found/empty.
	//
	// Thread Safety: Safe for concurrent use.
	GetLastStep(sessionID string) *StepRecord

	// CountToolExecutions returns how many times a tool was EXECUTED in a session.
	//
	// Description:
	//
	//   Counts steps where Decision == DecisionExecuteTool, not DecisionSelectTool.
	//   For circuit breaker, we care about actual executions, not selection attempts.
	//
	// Inputs:
	//   - sessionID: The session to query. Must not be empty.
	//   - tool: The tool name to count. Must not be empty.
	//
	// Outputs:
	//   - int: Number of executions. 0 if session not found or tool not used.
	//
	// Thread Safety: Safe for concurrent use.
	CountToolExecutions(sessionID string, tool string) int

	// GetStepsByActor returns steps filtered by actor.
	//
	// Inputs:
	//   - sessionID: The session to query. Must not be empty.
	//   - actor: The actor to filter by.
	//
	// Outputs:
	//   - []StepRecord: Matching steps in order. Empty slice if none found.
	//
	// Thread Safety: Safe for concurrent use.
	GetStepsByActor(sessionID string, actor Actor) []StepRecord

	// GetStepsByOutcome returns steps filtered by outcome.
	//
	// Inputs:
	//   - sessionID: The session to query. Must not be empty.
	//   - outcome: The outcome to filter by.
	//
	// Outputs:
	//   - []StepRecord: Matching steps in order. Empty slice if none found.
	//
	// Thread Safety: Safe for concurrent use.
	GetStepsByOutcome(sessionID string, outcome Outcome) []StepRecord

	// ClearStepHistory removes all steps for a session.
	//
	// Description:
	//
	//   Use when starting a new query or when session is complete.
	//
	// Inputs:
	//   - sessionID: The session to clear. Must not be empty.
	//
	// Thread Safety: Safe for concurrent use.
	ClearStepHistory(sessionID string)

	// -------------------------------------------------------------------------
	// Proof Index Methods (CRS-02)
	// -------------------------------------------------------------------------

	// UpdateProofNumber applies a proof update to a node.
	//
	// Description:
	//
	//   Updates the proof number for a node based on the update type:
	//   - Increment: Increases proof number (failure = path harder to prove)
	//   - Decrement: Decreases proof number (success = path easier to prove)
	//   - Disproven: Marks path as disproven (infinite cost)
	//   - Proven: Marks path as proven (solution found)
	//
	//   IMPORTANT: Proof number represents COST TO PROVE. Lower = better.
	//   This is counterintuitive but matches PN-MCTS semantics.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - update: The proof update to apply. Must pass Validate().
	//
	// Outputs:
	//   - error: Non-nil if validation fails or context cancelled.
	//
	// Thread Safety: Safe for concurrent use.
	UpdateProofNumber(ctx context.Context, update ProofUpdate) error

	// GetProofStatus returns the current proof status for a node.
	//
	// Description:
	//
	//   Returns the full ProofNumber struct for a node, including proof number,
	//   disproof number, status, and last update time.
	//
	// Inputs:
	//   - nodeID: The node to query. Must not be empty.
	//
	// Outputs:
	//   - ProofNumber: The current proof status. Zero value if node not found.
	//   - bool: True if node was found, false otherwise.
	//
	// Thread Safety: Safe for concurrent use.
	GetProofStatus(nodeID string) (ProofNumber, bool)

	// CheckCircuitBreaker checks if the circuit breaker should fire for a tool.
	//
	// Description:
	//
	//   Checks if a tool path is disproven or has exhausted proof number.
	//   This replaces ad-hoc counting in execute.go with proof-based logic.
	//
	//   Circuit breaker fires when:
	//   - Node status is ProofStatusDisproven
	//   - Proof number >= ProofNumberInfinite (exhausted)
	//
	// Inputs:
	//   - sessionID: The session to check. Must not be empty.
	//   - tool: The tool name to check. Must not be empty.
	//
	// Outputs:
	//   - CircuitBreakerResult: Contains ShouldFire bool and Reason.
	//
	// Thread Safety: Safe for concurrent use.
	CheckCircuitBreaker(sessionID string, tool string) CircuitBreakerResult

	// PropagateDisproof propagates disproof to parent decisions.
	//
	// Description:
	//
	//   When a node is disproven, parent decisions that depended on it have
	//   their proof numbers increased (harder to prove). Uses BFS with depth
	//   limit to prevent stack overflow.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - nodeID: The disproven node. Must not be empty.
	//
	// Outputs:
	//   - int: Number of nodes affected by propagation.
	//
	// Thread Safety: Safe for concurrent use.
	PropagateDisproof(ctx context.Context, nodeID string) int

	// -------------------------------------------------------------------------
	// Clause Index Methods (CRS-04)
	// -------------------------------------------------------------------------

	// AddClause adds a learned clause to the constraint index.
	//
	// Description:
	//
	//   Adds a clause learned from CDCL analysis. Checks for semantic
	//   duplicates and handles LRU eviction when MaxClauses is reached.
	//
	// Inputs:
	//   - ctx: Context for cancellation. Must not be nil.
	//   - clause: The clause to add. Must pass Validate().
	//
	// Outputs:
	//   - error: Non-nil if validation fails or context cancelled.
	//
	// Thread Safety: Safe for concurrent use.
	AddClause(ctx context.Context, clause *Clause) error

	// CheckDecisionAllowed checks if a proposed decision violates learned clauses.
	//
	// Description:
	//
	//   Builds an assignment from the step history and the proposed decision,
	//   then checks against all learned clauses using watched literals.
	//
	// Inputs:
	//   - sessionID: The session to check. Must not be empty.
	//   - tool: The proposed tool selection.
	//
	// Outputs:
	//   - bool: True if the decision is allowed.
	//   - string: Reason if the decision is blocked.
	//
	// Thread Safety: Safe for concurrent use.
	CheckDecisionAllowed(sessionID string, tool string) (bool, string)

	// GarbageCollectClauses removes expired clauses based on TTL.
	//
	// Description:
	//
	//   Removes clauses older than their TTL. Call periodically or after
	//   session ends to clean up stale clauses.
	//
	// Outputs:
	//   - int: Number of clauses removed.
	//
	// Thread Safety: Safe for concurrent use.
	GarbageCollectClauses() int

	// -------------------------------------------------------------------------
	// Delta History Methods (GR-35)
	// -------------------------------------------------------------------------

	// SetSessionID sets the current session ID for delta history tracking.
	//
	// Description:
	//
	//   Deltas recorded via Apply() will be associated with this session ID.
	//   Call this at the start of each agent session.
	//
	// Inputs:
	//   - sessionID: The session identifier. Can be empty to clear.
	//
	// Thread Safety: Safe for concurrent use.
	SetSessionID(sessionID string)

	// ApplyWithSource applies a delta with explicit source and metadata tracking.
	//
	// Description:
	//
	//   Like Apply(), but allows specifying a custom source string and metadata
	//   for delta history recording. Use this when you want to track which
	//   activity or component caused the delta.
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
	ApplyWithSource(ctx context.Context, delta Delta, source string, metadata map[string]string) (ApplyMetrics, error)

	// DeltaHistory returns the delta history for querying.
	//
	// Description:
	//
	//   Returns the delta history worker which provides methods to query
	//   what deltas were applied, when, and by whom. Use this to understand
	//   causality and build reasoning traces.
	//
	// Outputs:
	//   - DeltaHistoryView: Read-only view of delta history. May be nil if not initialized.
	//
	// Thread Safety: Safe for concurrent use.
	DeltaHistory() DeltaHistoryView

	// Close releases resources held by the CRS.
	//
	// Description:
	//
	//   Stops background workers (like delta history). Should be called when
	//   the CRS is no longer needed to prevent goroutine leaks.
	//
	// Thread Safety: Safe for concurrent use. Idempotent.
	Close()

	// -------------------------------------------------------------------------
	// Graph Integration Methods (GR-28)
	// -------------------------------------------------------------------------

	// SetGraphProvider sets the graph query provider.
	//
	// Description:
	//
	//   Registers a GraphQuery implementation that will be included in all
	//   future snapshots. Activities can then call snapshot.GraphQuery() to
	//   access the actual code graph.
	//
	//   Call this after the graph is initialized and after graph refreshes
	//   to update the adapter with the new graph state.
	//
	// Inputs:
	//   - provider: The graph query implementation. May be nil to clear.
	//
	// Thread Safety: Safe for concurrent use.
	SetGraphProvider(provider GraphQuery)

	// InvalidateGraphCache invalidates graph-backed dependency index caches.
	//
	// Description:
	//
	//   Called after the graph is refreshed (GR-29) to ensure subsequent queries
	//   see fresh data. Invalidates the Size() cache in GraphBackedDependencyIndex,
	//   which in turn invalidates the CRSGraphAdapter analytics cache (PageRank,
	//   communities, edge count).
	//
	//   This is a no-op if graph-backed index is not in use (legacy mode).
	//
	// Thread Safety: Safe for concurrent use.
	InvalidateGraphCache()

	// -------------------------------------------------------------------------
	// Analytics Methods (GR-31)
	// -------------------------------------------------------------------------

	// GetAnalyticsHistory returns all analytics records.
	//
	// Description:
	//
	//   Returns a copy of all analytics records in chronological order.
	//
	// Outputs:
	//   - []*AnalyticsRecord: Copy of all records.
	//
	// Thread Safety: Safe for concurrent use.
	GetAnalyticsHistory() []*AnalyticsRecord

	// GetLastAnalytics returns the most recent analytics of a given type.
	//
	// Description:
	//
	//   Searches analytics history for the most recent record of the
	//   specified query type.
	//
	// Inputs:
	//   - queryType: The type of analytics query to find.
	//
	// Outputs:
	//   - *AnalyticsRecord: The most recent matching record, or nil if not found.
	//
	// Thread Safety: Safe for concurrent use.
	GetLastAnalytics(queryType AnalyticsQueryType) *AnalyticsRecord

	// HasRunAnalytics checks if a specific analytics type has been run.
	//
	// Description:
	//
	//   Returns true if an analytics query of the given type has been
	//   recorded in history.
	//
	// Inputs:
	//   - queryType: The type of analytics query to check.
	//
	// Outputs:
	//   - bool: True if the query type has been run.
	//
	// Thread Safety: Safe for concurrent use.
	HasRunAnalytics(queryType AnalyticsQueryType) bool
}

// -----------------------------------------------------------------------------
// Snapshot Interface
// -----------------------------------------------------------------------------

// Snapshot is an immutable view of CRS at a point in time.
//
// Description:
//
//	Snapshots are created by CRS.Snapshot() and provide read-only access
//	to all indexes. Snapshots are thread-safe and can be shared across
//	goroutines.
//
// Thread Safety: Safe for concurrent use (immutable).
type Snapshot interface {
	// Generation returns the generation when this snapshot was created.
	Generation() int64

	// CreatedAt returns when this snapshot was created (Unix milliseconds UTC).
	CreatedAt() int64

	// ProofIndex returns the proof numbers index view.
	ProofIndex() ProofIndexView

	// ConstraintIndex returns the constraint index view.
	ConstraintIndex() ConstraintIndexView

	// SimilarityIndex returns the similarity index view.
	SimilarityIndex() SimilarityIndexView

	// DependencyIndex returns the dependency index view.
	DependencyIndex() DependencyIndexView

	// HistoryIndex returns the history index view.
	HistoryIndex() HistoryIndexView

	// StreamingIndex returns the streaming statistics index view.
	StreamingIndex() StreamingIndexView

	// Query returns the cross-index query API.
	//
	// Description:
	//
	//   Query provides methods that span multiple indexes, such as finding
	//   proven nodes that satisfy constraints or computing dependency chains.
	//
	// Outputs:
	//   - QueryAPI: The query API. Never nil.
	Query() QueryAPI

	// GraphQuery returns read-only access to the code graph (GR-28).
	//
	// Description:
	//
	//   Returns the graph query interface for activities to query the actual
	//   code graph structure. This enables activities to use graph algorithms
	//   (PageRank, community detection, etc.) rather than relying solely on
	//   CRS's internal DependencyIndex.
	//
	// Outputs:
	//   - GraphQuery: The graph query interface, or nil if unavailable.
	//
	// Thread Safety: Safe for concurrent use (snapshot is immutable).
	GraphQuery() GraphQuery

	// AnalyticsHistory returns recent analytics records (GR-31).
	//
	// Description:
	//
	//   Returns a copy of analytics records in chronological order.
	//   Activities can use this to check what analytics have been run.
	//
	// Outputs:
	//   - []*AnalyticsRecord: Copy of analytics records.
	//
	// Thread Safety: Safe for concurrent use (snapshot is immutable).
	AnalyticsHistory() []*AnalyticsRecord

	// LastAnalytics returns the most recent analytics of a given type (GR-31).
	//
	// Description:
	//
	//   Searches analytics history for the most recent record of the
	//   specified query type.
	//
	// Inputs:
	//   - queryType: The type of analytics query to find.
	//
	// Outputs:
	//   - *AnalyticsRecord: The most recent matching record, or nil if not found.
	//
	// Thread Safety: Safe for concurrent use (snapshot is immutable).
	LastAnalytics(queryType AnalyticsQueryType) *AnalyticsRecord

	// HasRunAnalytics checks if a specific analytics type has been run (GR-31).
	//
	// Description:
	//
	//   Returns true if an analytics query of the given type has been
	//   recorded in history.
	//
	// Inputs:
	//   - queryType: The type of analytics query to check.
	//
	// Outputs:
	//   - bool: True if the query type has been run.
	//
	// Thread Safety: Safe for concurrent use (snapshot is immutable).
	HasRunAnalytics(queryType AnalyticsQueryType) bool
}

// -----------------------------------------------------------------------------
// Index View Interfaces (Read-Only)
// -----------------------------------------------------------------------------

// ProofIndexView provides read-only access to proof numbers.
//
// Thread Safety: Safe for concurrent use (immutable).
type ProofIndexView interface {
	// Get returns the proof number for a node.
	Get(nodeID string) (ProofNumber, bool)

	// All returns all proof numbers.
	All() map[string]ProofNumber

	// Size returns the number of entries.
	Size() int
}

// ConstraintIndexView provides read-only access to constraints.
//
// Thread Safety: Safe for concurrent use (immutable).
type ConstraintIndexView interface {
	// Get returns a constraint by ID.
	Get(constraintID string) (Constraint, bool)

	// FindByType returns all constraints of a type.
	FindByType(constraintType ConstraintType) []Constraint

	// FindByNode returns all constraints affecting a node.
	FindByNode(nodeID string) []Constraint

	// All returns all constraints.
	All() map[string]Constraint

	// Size returns the number of constraints.
	Size() int

	// --- Clause Methods (CRS-04) ---

	// GetClause returns a learned clause by ID.
	GetClause(clauseID string) (*Clause, bool)

	// AllClauses returns all learned clauses.
	AllClauses() map[string]*Clause

	// ClauseCount returns the number of learned clauses.
	ClauseCount() int

	// CheckAssignment checks if an assignment violates any learned clauses.
	//
	// Description:
	//
	//   Uses watched literals for efficient checking. Returns the first
	//   violated clause if any.
	//
	// Inputs:
	//
	//   assignment - Map of variable names to their boolean values.
	//
	// Outputs:
	//
	//   ClauseCheckResult - Result of the check.
	CheckAssignment(assignment map[string]bool) ClauseCheckResult
}

// SimilarityIndexView provides read-only access to similarity scores.
//
// Thread Safety: Safe for concurrent use (immutable).
type SimilarityIndexView interface {
	// Distance returns the similarity distance between two nodes.
	Distance(node1, node2 string) (float64, bool)

	// NearestNeighbors returns the k nearest neighbors of a node.
	NearestNeighbors(nodeID string, k int) []SimilarityMatch

	// Size returns the number of entries.
	Size() int

	// AllPairs returns all similarity pairs for export.
	//
	// Description:
	//
	//   Returns a deep copy of the similarity matrix for serialization.
	//   Format: map[fromID]map[toID]similarity.
	//
	// Outputs:
	//   - map[string]map[string]float64: Copy of all pairs. Never nil.
	//
	// Thread Safety: Returns deep copy; caller can modify without affecting source.
	AllPairs() map[string]map[string]float64

	// AllPairsFiltered returns similarity pairs in one direction only for efficient export.
	//
	// Description:
	//
	//   Returns pairs where fromID < toID to avoid duplicates. This is more
	//   memory-efficient than AllPairs() for export since similarity is symmetric.
	//   Respects maxPairs limit and returns truncated flag.
	//
	// Inputs:
	//   - maxPairs: Maximum pairs to return. -1 for unlimited.
	//
	// Outputs:
	//   - []SimilarityPairData: Pairs in one direction. Never nil.
	//   - bool: True if truncated due to limit.
	//
	// Thread Safety: Safe for concurrent use.
	AllPairsFiltered(maxPairs int) ([]SimilarityPairData, bool)
}

// SimilarityPairData holds a similarity pair for export.
type SimilarityPairData struct {
	FromID     string
	ToID       string
	Similarity float64
}

// DependencyIndexView provides read-only access to dependencies.
//
// Thread Safety: Safe for concurrent use (immutable).
type DependencyIndexView interface {
	// DependsOn returns all nodes that nodeID depends on.
	DependsOn(nodeID string) []string

	// DependedBy returns all nodes that depend on nodeID.
	DependedBy(nodeID string) []string

	// HasCycle returns true if there's a cycle involving nodeID.
	HasCycle(nodeID string) bool

	// Size returns the number of dependency edges.
	Size() int

	// AllEdges returns all dependency edges for export.
	//
	// Description:
	//
	//   Returns a deep copy of all dependency edges for serialization.
	//   Format: map[fromID][]toIDs.
	//
	//   For GraphBackedDependencyIndex (GR-32), returns nil since edges
	//   live in the graph which has its own persistence mechanism.
	//
	// Outputs:
	//   - map[string][]string: Copy of forward edges, or nil for graph-backed.
	//
	// Thread Safety: Returns deep copy; caller can modify without affecting source.
	AllEdges() map[string][]string

	// IsGraphBacked returns true if this index delegates to the graph.
	//
	// Description:
	//
	//   When true, AllEdges() returns nil and edge export should be skipped.
	//   The graph has its own persistence; use graph backup instead.
	//
	// Outputs:
	//   - bool: True if graph-backed (GR-32), false for legacy implementation.
	IsGraphBacked() bool
}

// HistoryIndexView provides read-only access to decision history.
//
// Thread Safety: Safe for concurrent use (immutable).
type HistoryIndexView interface {
	// Trace returns the decision trace for a node.
	Trace(nodeID string) []HistoryEntry

	// Recent returns the N most recent history entries.
	Recent(n int) []HistoryEntry

	// Size returns the number of history entries.
	Size() int
}

// StreamingIndexView provides read-only access to streaming statistics.
//
// Thread Safety: Safe for concurrent use (immutable).
type StreamingIndexView interface {
	// Estimate returns the frequency estimate for an item.
	Estimate(item string) uint64

	// Cardinality returns the estimated unique item count.
	Cardinality() uint64

	// Size returns the approximate memory usage in bytes.
	Size() int
}

// -----------------------------------------------------------------------------
// Data Types
// -----------------------------------------------------------------------------

// ProofNumber represents proof and disproof numbers for a node.
type ProofNumber struct {
	// Proof is the proof number (cost to prove this node).
	Proof uint64

	// Disproof is the disproof number (cost to disprove this node).
	Disproof uint64

	// Status is the current proof status.
	Status ProofStatus

	// Source indicates where this proof came from.
	Source SignalSource

	// UpdatedAt is when this proof was last updated (Unix milliseconds UTC).
	UpdatedAt int64
}

// ProofStatus represents the proof status of a node.
type ProofStatus int

const (
	// ProofStatusUnknown means the node's proof status is not determined.
	ProofStatusUnknown ProofStatus = iota

	// ProofStatusProven means the node is proven (leads to solution).
	ProofStatusProven

	// ProofStatusDisproven means the node is disproven (does not lead to solution).
	ProofStatusDisproven

	// ProofStatusExpanded means the node has been expanded but not proven/disproven.
	ProofStatusExpanded
)

// String returns the string representation of ProofStatus.
func (s ProofStatus) String() string {
	switch s {
	case ProofStatusUnknown:
		return "unknown"
	case ProofStatusProven:
		return "proven"
	case ProofStatusDisproven:
		return "disproven"
	case ProofStatusExpanded:
		return "expanded"
	default:
		return fmt.Sprintf("ProofStatus(%d)", s)
	}
}

// SignalSource indicates where a signal came from (hard vs soft).
type SignalSource int

const (
	// SignalSourceUnknown means the source is not known.
	SignalSourceUnknown SignalSource = iota

	// SignalSourceHard means the signal came from a hard source (compiler, tests).
	SignalSourceHard

	// SignalSourceSoft means the signal came from a soft source (LLM).
	SignalSourceSoft

	// SignalSourceSafety means the signal came from the safety gate.
	//
	// Safety violations are treated as HARD signals for learning purposes.
	// When the safety gate blocks an action, CDCL should learn to avoid
	// the pattern that caused the violation.
	//
	// This addresses the "Safety Blocking Learning Signal" problem:
	// without this, safety-blocked actions would be soft errors that
	// MCTS/CDCL ignores, potentially retrying the same blocked pattern.
	SignalSourceSafety
)

// String returns the string representation of SignalSource.
func (s SignalSource) String() string {
	switch s {
	case SignalSourceUnknown:
		return "unknown"
	case SignalSourceHard:
		return "hard"
	case SignalSourceSoft:
		return "soft"
	case SignalSourceSafety:
		return "safety"
	default:
		return fmt.Sprintf("SignalSource(%d)", s)
	}
}

// IsHard returns true if this is a hard signal source.
//
// Safety violations are treated as hard signals because:
// 1. They represent definitive failures (the action WILL be blocked)
// 2. CDCL should learn to avoid patterns that trigger safety blocks
// 3. Unlike soft signals, safety rules are deterministic
func (s SignalSource) IsHard() bool {
	return s == SignalSourceHard || s == SignalSourceSafety
}

// IsSafety returns true if this signal came from the safety gate.
func (s SignalSource) IsSafety() bool {
	return s == SignalSourceSafety
}

// IsValid returns true if the signal source is a known value (not Unknown).
//
// Description:
//
//	Used for validation to ensure proof updates have explicit signal sources.
//	SignalSourceUnknown is not considered valid as it indicates missing attribution.
func (s SignalSource) IsValid() bool {
	switch s {
	case SignalSourceHard, SignalSourceSoft, SignalSourceSafety:
		return true
	default:
		return false
	}
}

// Constraint represents a constraint on the search space.
type Constraint struct {
	// ID is the unique constraint identifier.
	ID string

	// Type is the constraint type.
	Type ConstraintType

	// Nodes are the nodes this constraint affects.
	Nodes []string

	// Expression is the constraint expression.
	Expression string

	// Active indicates if the constraint is currently active.
	Active bool

	// Source indicates where this constraint came from.
	Source SignalSource

	// CreatedAt is when this constraint was created (Unix milliseconds UTC).
	CreatedAt int64
}

// ConstraintType represents the type of constraint.
type ConstraintType int

const (
	// ConstraintTypeUnknown is an unknown constraint type.
	ConstraintTypeUnknown ConstraintType = iota

	// ConstraintTypeMutualExclusion means nodes cannot be selected together.
	ConstraintTypeMutualExclusion

	// ConstraintTypeImplication means selecting one node implies another.
	ConstraintTypeImplication

	// ConstraintTypeOrdering means nodes must be selected in order.
	ConstraintTypeOrdering

	// ConstraintTypeResource means nodes share a resource limit.
	ConstraintTypeResource
)

// String returns the string representation of ConstraintType.
func (t ConstraintType) String() string {
	switch t {
	case ConstraintTypeUnknown:
		return "unknown"
	case ConstraintTypeMutualExclusion:
		return "mutual_exclusion"
	case ConstraintTypeImplication:
		return "implication"
	case ConstraintTypeOrdering:
		return "ordering"
	case ConstraintTypeResource:
		return "resource"
	default:
		return fmt.Sprintf("ConstraintType(%d)", t)
	}
}

// SimilarityMatch represents a similarity search result.
type SimilarityMatch struct {
	// NodeID is the matching node.
	NodeID string

	// Distance is the similarity distance (lower = more similar).
	Distance float64
}

// HistoryEntry represents a single decision in the history.
type HistoryEntry struct {
	// ID is the unique entry identifier.
	ID string

	// NodeID is the node this decision was about.
	NodeID string

	// Action is the action taken.
	Action string

	// Result is the outcome of the action.
	Result string

	// Source indicates where this decision came from.
	Source SignalSource

	// Timestamp is when this decision was made (Unix milliseconds UTC).
	Timestamp int64

	// Metadata contains additional context.
	Metadata map[string]string
}

// -----------------------------------------------------------------------------
// Delta Types
// -----------------------------------------------------------------------------

// DeltaType identifies the type of delta.
type DeltaType int

const (
	// DeltaTypeUnknown is an unknown delta type.
	DeltaTypeUnknown DeltaType = iota

	// DeltaTypeProof updates proof numbers.
	DeltaTypeProof

	// DeltaTypeConstraint updates constraints.
	DeltaTypeConstraint

	// DeltaTypeSimilarity updates similarity scores.
	DeltaTypeSimilarity

	// DeltaTypeDependency updates dependencies.
	DeltaTypeDependency

	// DeltaTypeHistory adds history entries.
	DeltaTypeHistory

	// DeltaTypeStreaming updates streaming statistics.
	DeltaTypeStreaming

	// DeltaTypeComposite contains multiple deltas.
	DeltaTypeComposite

	// DeltaTypeAnalytics records analytics queries.
	// GR-31: Added for analytics CRS routing.
	DeltaTypeAnalytics
)

// String returns the string representation of DeltaType.
func (t DeltaType) String() string {
	switch t {
	case DeltaTypeUnknown:
		return "unknown"
	case DeltaTypeProof:
		return "proof"
	case DeltaTypeConstraint:
		return "constraint"
	case DeltaTypeSimilarity:
		return "similarity"
	case DeltaTypeDependency:
		return "dependency"
	case DeltaTypeHistory:
		return "history"
	case DeltaTypeStreaming:
		return "streaming"
	case DeltaTypeComposite:
		return "composite"
	case DeltaTypeAnalytics:
		return "analytics"
	default:
		return fmt.Sprintf("DeltaType(%d)", t)
	}
}

// Delta represents an atomic change to CRS state.
//
// Description:
//
//	Algorithms produce deltas that describe state changes. Deltas are
//	validated before application and can be merged with other deltas.
//
// Thread Safety: Implementations must be safe for concurrent use.
type Delta interface {
	// Type returns the delta type.
	Type() DeltaType

	// Validate checks if this delta can be applied to the snapshot.
	//
	// Inputs:
	//   - snapshot: The snapshot to validate against.
	//
	// Outputs:
	//   - error: Non-nil if validation fails.
	Validate(snapshot Snapshot) error

	// Merge combines this delta with another delta.
	//
	// Inputs:
	//   - other: The delta to merge with.
	//
	// Outputs:
	//   - Delta: The merged delta.
	//   - error: Non-nil if deltas cannot be merged.
	Merge(other Delta) (Delta, error)

	// ConflictsWith returns true if this delta conflicts with another.
	//
	// Inputs:
	//   - other: The delta to check for conflicts.
	//
	// Outputs:
	//   - bool: True if deltas conflict.
	ConflictsWith(other Delta) bool

	// Source returns the signal source for this delta.
	Source() SignalSource

	// Timestamp returns when this delta was created (Unix milliseconds UTC).
	Timestamp() int64

	// IndexesAffected returns which indexes this delta will modify.
	IndexesAffected() []string
}

// -----------------------------------------------------------------------------
// Index Mask (GR-37: Zero-allocation bitmask)
// -----------------------------------------------------------------------------

// IndexMask identifies which CRS indexes were modified using a bitmask.
//
// Description:
//
//	Uses bitmask for O(1) operations and zero allocation. There are exactly
//	6 CRS indexes, so a uint8 is sufficient.
//
// Thread Safety: IndexMask is immutable; safe for concurrent use.
type IndexMask uint8

const (
	// IndexProof indicates the proof index was modified.
	IndexProof IndexMask = 1 << iota
	// IndexConstraint indicates the constraint index was modified.
	IndexConstraint
	// IndexSimilarity indicates the similarity index was modified.
	IndexSimilarity
	// IndexDependency indicates the dependency index was modified.
	IndexDependency
	// IndexHistory indicates the history index was modified.
	IndexHistory
	// IndexStreaming indicates the streaming index was modified.
	IndexStreaming
)

// Has returns true if the mask contains the given index.
func (m IndexMask) Has(idx IndexMask) bool {
	return m&idx != 0
}

// Add returns a new mask with the given index added.
func (m IndexMask) Add(idx IndexMask) IndexMask {
	return m | idx
}

// Names returns the names of all indexes in the mask.
//
// Thread Safety: Allocates a new slice on each call.
func (m IndexMask) Names() []string {
	var names []string
	if m.Has(IndexProof) {
		names = append(names, "proof")
	}
	if m.Has(IndexConstraint) {
		names = append(names, "constraint")
	}
	if m.Has(IndexSimilarity) {
		names = append(names, "similarity")
	}
	if m.Has(IndexDependency) {
		names = append(names, "dependency")
	}
	if m.Has(IndexHistory) {
		names = append(names, "history")
	}
	if m.Has(IndexStreaming) {
		names = append(names, "streaming")
	}
	return names
}

// String returns a comma-separated list of index names.
func (m IndexMask) String() string {
	return strings.Join(m.Names(), ",")
}

// MarshalJSON implements json.Marshaler for backwards-compatible JSON output.
//
// Outputs a JSON array of index names: ["proof", "constraint"]
func (m IndexMask) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.Names())
}

// UnmarshalJSON implements json.Unmarshaler.
//
// Accepts either a JSON array of strings or a number (bitmask).
func (m *IndexMask) UnmarshalJSON(data []byte) error {
	// Try array of strings first (backwards compatible)
	var names []string
	if err := json.Unmarshal(data, &names); err == nil {
		*m = IndexMaskFromStrings(names)
		return nil
	}
	// Try number (new format)
	var n uint8
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*m = IndexMask(n)
	return nil
}

// IndexMaskFromStrings converts string names to IndexMask.
func IndexMaskFromStrings(names []string) IndexMask {
	var m IndexMask
	for _, name := range names {
		switch name {
		case "proof":
			m |= IndexProof
		case "constraint":
			m |= IndexConstraint
		case "similarity":
			m |= IndexSimilarity
		case "dependency":
			m |= IndexDependency
		case "history":
			m |= IndexHistory
		case "streaming":
			m |= IndexStreaming
		}
	}
	return m
}

// IndexMaskFromDelta converts a delta's IndexesAffected to IndexMask.
func IndexMaskFromDelta(delta Delta) IndexMask {
	return IndexMaskFromStrings(delta.IndexesAffected())
}

// -----------------------------------------------------------------------------
// Apply Metrics
// -----------------------------------------------------------------------------

// ApplyMetrics contains metrics about an Apply operation.
type ApplyMetrics struct {
	// DeltaType is the type of delta applied.
	DeltaType DeltaType

	// ApplyDuration is how long the apply took.
	ApplyDuration time.Duration

	// ValidationDuration is how long validation took.
	ValidationDuration time.Duration

	// IndexesUpdated identifies which indexes were updated (bitmask).
	// Use IndexesUpdated.Has(IndexProof) to check, .Names() for string slice.
	IndexesUpdated IndexMask

	// EntriesModified is the number of entries modified.
	EntriesModified int

	// OldGeneration is the generation before apply.
	OldGeneration int64

	// NewGeneration is the generation after apply.
	NewGeneration int64
}

// -----------------------------------------------------------------------------
// Checkpoint
// -----------------------------------------------------------------------------

// Checkpoint represents a restorable state checkpoint.
type Checkpoint struct {
	// ID is the unique checkpoint identifier.
	ID string

	// Generation is the generation at checkpoint time.
	Generation int64

	// CreatedAt is when the checkpoint was created (Unix milliseconds UTC).
	CreatedAt int64

	// data holds the checkpoint data (opaque to consumers).
	data any
}

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

// Config configures a CRS instance.
type Config struct {
	// MaxGeneration is the maximum generation before wrapping.
	// Default: 0 (no maximum, wraps at int64 max).
	MaxGeneration int64

	// SnapshotEpochLimit is how many generations a snapshot is valid.
	// Default: 1000 (snapshots older than 1000 generations are stale).
	SnapshotEpochLimit int64

	// EnableMetrics enables metrics collection.
	// Default: true.
	EnableMetrics bool

	// EnableTracing enables OpenTelemetry tracing.
	// Default: true.
	EnableTracing bool
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxGeneration:      0,
		SnapshotEpochLimit: 1000,
		EnableMetrics:      true,
		EnableTracing:      true,
	}
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.SnapshotEpochLimit < 0 {
		return errors.New("snapshot epoch limit must be non-negative")
	}
	return nil
}

// -----------------------------------------------------------------------------
// StepRecord v2 Types (CRS-01)
// -----------------------------------------------------------------------------

// Actor identifies who made a decision in the agent reasoning process.
type Actor string

const (
	// ActorRouter is the tool router (e.g., granite4:micro-h).
	ActorRouter Actor = "router"

	// ActorMainAgent is the main LLM (e.g., glm-4.7-flash).
	ActorMainAgent Actor = "main_agent"

	// ActorSystem is the system (circuit breaker, retries, timeouts).
	ActorSystem Actor = "system"
)

// String returns the string representation of Actor.
func (a Actor) String() string {
	return string(a)
}

// IsValid returns true if the actor is a known value.
func (a Actor) IsValid() bool {
	switch a {
	case ActorRouter, ActorMainAgent, ActorSystem:
		return true
	default:
		return false
	}
}

// Decision identifies what type of decision was made.
type Decision string

const (
	// DecisionSelectTool means the router selected a tool.
	DecisionSelectTool Decision = "select_tool"

	// DecisionExecuteTool means a tool was executed.
	DecisionExecuteTool Decision = "execute_tool"

	// DecisionSynthesize means generating a final answer.
	DecisionSynthesize Decision = "synthesize"

	// DecisionCircuitBreaker means the circuit breaker intervened.
	DecisionCircuitBreaker Decision = "circuit_breaker"

	// DecisionRetry means a retry was triggered.
	DecisionRetry Decision = "retry"

	// DecisionComplete means the session completed.
	DecisionComplete Decision = "complete"

	// DecisionError means an error occurred.
	DecisionError Decision = "error"
)

// String returns the string representation of Decision.
func (d Decision) String() string {
	return string(d)
}

// IsValid returns true if the decision is a known value.
func (d Decision) IsValid() bool {
	switch d {
	case DecisionSelectTool, DecisionExecuteTool, DecisionSynthesize,
		DecisionCircuitBreaker, DecisionRetry, DecisionComplete, DecisionError:
		return true
	default:
		return false
	}
}

// Outcome identifies the result of a step.
type Outcome string

const (
	// OutcomeSuccess means the step completed successfully.
	OutcomeSuccess Outcome = "success"

	// OutcomeFailure means the step failed.
	OutcomeFailure Outcome = "failure"

	// OutcomeSkipped means the step was skipped (e.g., low confidence).
	OutcomeSkipped Outcome = "skipped"

	// OutcomeForced means the outcome was forced (e.g., circuit breaker).
	OutcomeForced Outcome = "forced"
)

// String returns the string representation of Outcome.
func (o Outcome) String() string {
	return string(o)
}

// IsValid returns true if the outcome is a known value.
func (o Outcome) IsValid() bool {
	switch o {
	case OutcomeSuccess, OutcomeFailure, OutcomeSkipped, OutcomeForced:
		return true
	default:
		return false
	}
}

// ErrorCategory categorizes errors for retry and learning logic.
// Using typed enums instead of raw strings enables compile-time checking
// and better analytics.
type ErrorCategory string

const (
	// ErrorCategoryNone means no error occurred.
	ErrorCategoryNone ErrorCategory = ""

	// ErrorCategoryToolNotFound means the tool doesn't exist.
	ErrorCategoryToolNotFound ErrorCategory = "tool_not_found"

	// ErrorCategoryInvalidParams means the tool parameters were invalid.
	ErrorCategoryInvalidParams ErrorCategory = "invalid_params"

	// ErrorCategoryTimeout means the operation timed out.
	ErrorCategoryTimeout ErrorCategory = "timeout"

	// ErrorCategoryRateLimited means the operation was rate limited.
	ErrorCategoryRateLimited ErrorCategory = "rate_limited"

	// ErrorCategoryPermission means permission was denied.
	ErrorCategoryPermission ErrorCategory = "permission"

	// ErrorCategoryNetwork means a network error occurred.
	ErrorCategoryNetwork ErrorCategory = "network"

	// ErrorCategoryInternal means an internal error occurred.
	ErrorCategoryInternal ErrorCategory = "internal"

	// ErrorCategorySafety means the safety gate blocked the operation.
	ErrorCategorySafety ErrorCategory = "safety"
)

// String returns the string representation of ErrorCategory.
func (e ErrorCategory) String() string {
	if e == ErrorCategoryNone {
		return "none"
	}
	return string(e)
}

// IsRetryable returns true if this error category is potentially retryable.
func (e ErrorCategory) IsRetryable() bool {
	switch e {
	case ErrorCategoryTimeout, ErrorCategoryRateLimited, ErrorCategoryNetwork:
		return true
	default:
		return false
	}
}

// KeyValue is a typed key-value pair for extra parameters.
// This provides a bounded, auditable alternative to map[string]any.
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ToolParams captures tool invocation parameters with typed fields.
//
// Description:
//
//	Per CLAUDE.md Section 4.5, we use typed structs instead of map[string]any.
//	This ensures type safety at compile time and proper serialization.
//
// Thread Safety: ToolParams is immutable after creation.
type ToolParams struct {
	// Target is the primary target (file path, symbol name, etc.).
	Target string `json:"target,omitempty"`

	// Query is the search query or pattern.
	Query string `json:"query,omitempty"`

	// Depth limits recursion or traversal depth.
	Depth int `json:"depth,omitempty"`

	// Limit caps the number of results.
	Limit int `json:"limit,omitempty"`

	// Flags are boolean options.
	Flags []string `json:"flags,omitempty"`

	// Extra holds additional string key-value pairs for tool-specific params.
	// This is bounded and auditable unlike map[string]any.
	Extra []KeyValue `json:"extra,omitempty"`
}

// StepRecord captures one step in the agent reasoning process.
//
// Description:
//
//	This is the primary unit of CRS recording. Each step represents a discrete
//	decision point with explicit attribution, outcome, and context for the next step.
//	StepRecord replaces the old TraceStep with typed fields instead of untyped strings.
//
// Thread Safety: StepRecord is immutable after creation.
type StepRecord struct {
	// === Identity ===

	// StepNumber is the 1-indexed step number.
	StepNumber int `json:"step_number"`

	// Timestamp is when this step started (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`

	// SessionID links this step to its session. Must not be empty.
	SessionID string `json:"session_id"`

	// === Actor Attribution ===

	// Actor identifies who made this decision.
	Actor Actor `json:"actor"`

	// Model is the specific model used (e.g., "granite4:micro-h", "glm-4.7-flash").
	Model string `json:"model,omitempty"`

	// === Decision ===

	// Decision is the type of decision made.
	Decision Decision `json:"decision"`

	// Tool is the tool name if applicable.
	Tool string `json:"tool,omitempty"`

	// ToolParams are the parameters passed to the tool (typed struct, not map[string]any).
	ToolParams *ToolParams `json:"tool_params,omitempty"`

	// Confidence is the router confidence if applicable (0.0-1.0).
	Confidence float64 `json:"confidence,omitempty"`

	// Reasoning explains why this decision was made.
	Reasoning string `json:"reasoning,omitempty"`

	// === Outcome ===

	// Outcome is the result of this step.
	Outcome Outcome `json:"outcome"`

	// ErrorMessage contains the error message if outcome is failure.
	ErrorMessage string `json:"error_message,omitempty"`

	// ErrorCategory categorizes the error for retry logic (typed enum, not string).
	ErrorCategory ErrorCategory `json:"error_category,omitempty"`

	// DurationMs is how long this step took in milliseconds.
	// NOTE: Using int64 with explicit _ms suffix for JSON clarity.
	// time.Duration encodes as nanoseconds in JSON which is confusing.
	DurationMs int64 `json:"duration_ms"`

	// === Results ===

	// ResultSummary is a brief summary of what was found/produced.
	ResultSummary string `json:"result_summary,omitempty"`

	// TokensUsed is the number of tokens consumed.
	TokensUsed int `json:"tokens_used,omitempty"`

	// === Context for Next Step ===

	// Propagate indicates if this step's outcome should influence the next step.
	Propagate bool `json:"propagate"`

	// Terminal indicates if this is a final answer (no more steps needed).
	Terminal bool `json:"terminal"`

	// NextHint provides guidance for the next step.
	NextHint string `json:"next_hint,omitempty"`

	// === MCTS Integration (optional) ===

	// ProofUpdates for MCTS proof number changes.
	ProofUpdates []ProofUpdate `json:"proof_updates,omitempty"`

	// ConstraintsAdded for MCTS constraint additions.
	ConstraintsAdded []ConstraintUpdate `json:"constraints_added,omitempty"`

	// DependenciesFound for MCTS dependency edges.
	DependenciesFound []DependencyEdge `json:"dependencies_found,omitempty"`
}

// Validate checks that the StepRecord has required fields and valid values.
//
// Description:
//
//	Call this before recording to catch errors early. Validation is strict:
//	all required fields must be present and values must be in valid ranges.
//
// Outputs:
//
//	error - Non-nil if validation fails with a description of the problem.
//
// Thread Safety: Safe for concurrent use (read-only).
func (s *StepRecord) Validate() error {
	if s.SessionID == "" {
		return errors.New("session_id must not be empty")
	}
	if s.StepNumber < 1 {
		return errors.New("step_number must be >= 1")
	}
	if s.Actor == "" {
		return errors.New("actor must not be empty")
	}
	if !s.Actor.IsValid() {
		return fmt.Errorf("actor %q is not a valid actor", s.Actor)
	}
	if s.Decision == "" {
		return errors.New("decision must not be empty")
	}
	if !s.Decision.IsValid() {
		return fmt.Errorf("decision %q is not a valid decision", s.Decision)
	}
	if s.Outcome == "" {
		return errors.New("outcome must not be empty")
	}
	if !s.Outcome.IsValid() {
		return fmt.Errorf("outcome %q is not a valid outcome", s.Outcome)
	}
	if s.Outcome == OutcomeFailure && s.ErrorCategory == ErrorCategoryNone {
		return errors.New("error_category is required when outcome is failure")
	}
	if s.Confidence < 0 || s.Confidence > 1 {
		return errors.New("confidence must be in range [0.0, 1.0]")
	}
	if s.DurationMs < 0 {
		return errors.New("duration_ms must be non-negative")
	}
	return nil
}

// IsToolExecution returns true if this step represents an actual tool execution.
// Used by CountToolExecutions to count real executions, not selections.
func (s *StepRecord) IsToolExecution() bool {
	return s.Decision == DecisionExecuteTool
}

// IsCircuitBreakerIntervention returns true if this step was a circuit breaker action.
func (s *StepRecord) IsCircuitBreakerIntervention() bool {
	return s.Decision == DecisionCircuitBreaker
}

// IsTerminal returns true if this step marks the end of reasoning.
func (s *StepRecord) IsTerminal() bool {
	return s.Terminal
}

// -----------------------------------------------------------------------------
// Proof Index Types (CRS-02)
// -----------------------------------------------------------------------------

// ProofUpdateType identifies the type of proof number update.
//
// Description:
//
//	In PN-MCTS, proof number represents the COST TO PROVE a node (lower = easier).
//	This is the OPPOSITE of a "success count":
//	  - Success DECREASES proof number (path is viable, easier to prove)
//	  - Failure INCREASES proof number (path is problematic, harder to prove)
//	  - Disproven means infinite cost (path cannot lead to solution)
type ProofUpdateType int

const (
	// ProofUpdateTypeUnknown is an unknown update type.
	ProofUpdateTypeUnknown ProofUpdateType = iota

	// ProofUpdateTypeIncrement increases proof number (failure = harder to prove).
	ProofUpdateTypeIncrement

	// ProofUpdateTypeDecrement decreases proof number (success = easier to prove).
	ProofUpdateTypeDecrement

	// ProofUpdateTypeDisproven marks node as disproven (infinite cost).
	ProofUpdateTypeDisproven

	// ProofUpdateTypeProven marks node as proven (solution found via this path).
	ProofUpdateTypeProven

	// ProofUpdateTypeReset resets proof number to initial value.
	ProofUpdateTypeReset
)

// String returns the string representation of ProofUpdateType.
func (t ProofUpdateType) String() string {
	switch t {
	case ProofUpdateTypeUnknown:
		return "unknown"
	case ProofUpdateTypeIncrement:
		return "increment"
	case ProofUpdateTypeDecrement:
		return "decrement"
	case ProofUpdateTypeDisproven:
		return "disproven"
	case ProofUpdateTypeProven:
		return "proven"
	case ProofUpdateTypeReset:
		return "reset"
	default:
		return fmt.Sprintf("ProofUpdateType(%d)", t)
	}
}

// IsValid returns true if the update type is a known value.
func (t ProofUpdateType) IsValid() bool {
	switch t {
	case ProofUpdateTypeIncrement, ProofUpdateTypeDecrement,
		ProofUpdateTypeDisproven, ProofUpdateTypeProven, ProofUpdateTypeReset:
		return true
	default:
		return false
	}
}

// ProofUpdate represents a proof number update operation.
//
// Description:
//
//	Used by RecordStep and UpdateProofNumber to track changes to proof numbers.
//	The update type determines how the proof number is modified:
//	  - Increment: Add Delta to proof number (failure)
//	  - Decrement: Subtract Delta from proof number (success)
//	  - Disproven: Set status to disproven regardless of current number
//	  - Proven: Set status to proven regardless of current number
//
// Thread Safety: ProofUpdate is immutable after creation.
type ProofUpdate struct {
	// NodeID is the node whose proof status changed.
	NodeID string `json:"node_id"`

	// Type is the type of update to apply.
	Type ProofUpdateType `json:"type"`

	// Delta is the amount to increment/decrement (ignored for Disproven/Proven).
	Delta uint64 `json:"delta,omitempty"`

	// Reason explains why this update is being made.
	Reason string `json:"reason,omitempty"`

	// Source indicates where this signal came from (hard, soft, safety).
	Source SignalSource `json:"source"`

	// Status is the new status (used for JSON serialization compatibility).
	// Deprecated: Use Type instead. This field exists for backwards compatibility
	// with trace_recorder.go format.
	Status string `json:"status,omitempty"`
}

// Validate checks that the ProofUpdate has required fields.
func (u *ProofUpdate) Validate() error {
	if u.NodeID == "" {
		return errors.New("node_id must not be empty")
	}
	if !u.Type.IsValid() {
		return fmt.Errorf("type %d is not valid", u.Type)
	}
	// Require explicit signal source attribution
	if !u.Source.IsValid() {
		return fmt.Errorf("source %d is not valid (must be hard, soft, or safety)", u.Source)
	}
	// Increment/Decrement require Delta > 0
	if (u.Type == ProofUpdateTypeIncrement || u.Type == ProofUpdateTypeDecrement) && u.Delta == 0 {
		return errors.New("delta must be > 0 for increment/decrement")
	}
	return nil
}

// ConstraintUpdate represents a constraint being added.
//
// Thread Safety: ConstraintUpdate is immutable after creation.
type ConstraintUpdate struct {
	// ID is the constraint ID.
	ID string `json:"id"`

	// Type is the constraint type (typed enum).
	Type ConstraintType `json:"type"`

	// Nodes are the affected nodes.
	Nodes []string `json:"nodes"`

	// Source indicates where this constraint came from.
	Source SignalSource `json:"source"`
}

// DependencyEdge represents a dependency relationship.
//
// Thread Safety: DependencyEdge is immutable after creation.
type DependencyEdge struct {
	// From is the dependent node.
	From string `json:"from"`

	// To is the dependency target.
	To string `json:"to"`

	// Source indicates where this dependency was discovered.
	Source SignalSource `json:"source"`
}

// CircuitBreakerResult contains the result of a circuit breaker check.
//
// Description:
//
//	Returned by CheckCircuitBreaker to indicate whether the circuit breaker
//	should fire and why.
type CircuitBreakerResult struct {
	// ShouldFire is true if the circuit breaker should intervene.
	ShouldFire bool `json:"should_fire"`

	// Reason explains why the circuit breaker fired (or didn't).
	Reason string `json:"reason,omitempty"`

	// ProofNumber is the current proof number for the checked path.
	ProofNumber uint64 `json:"proof_number,omitempty"`

	// Status is the current proof status.
	Status ProofStatus `json:"status,omitempty"`
}

// DefaultInitialProofNumber is the starting proof number for new nodes.
// Represents moderate cost to prove - not too easy, not too hard.
const DefaultInitialProofNumber uint64 = 10

// ProofNumberInfinite represents a disproven path (infinite cost to prove).
// Using max uint64 value.
const ProofNumberInfinite uint64 = ^uint64(0)

// MaxPropagationDepth prevents runaway propagation in cyclic graphs.
// 100 levels is sufficient for any reasonable decision tree.
const MaxPropagationDepth = 100

// DefaultCircuitBreakerThreshold is the fallback threshold for tool execution count
// when no proof data exists. If a tool has been executed this many times, the
// circuit breaker fires. This is used for backwards compatibility with code that
// hasn't yet integrated proof numbers.
//
// NOTE: This value should match maxRepeatedToolCalls in execute.go.
// Feb 13, 2026: Lowered from 5 to 2 based on integration test evidence.
// Threshold=5 allowed too much wasteful exploration (Test 95: 5 identical calls).
// Threshold=2 prevents tool loops while still allowing legitimate multi-call patterns.
// If specific tools need >2 calls, implement per-tool thresholds in future work.
const DefaultCircuitBreakerThreshold = 2

// -----------------------------------------------------------------------------
// Learning Activity Types (CRS-04)
// -----------------------------------------------------------------------------

// FailureType identifies the type of failure that triggered learning.
type FailureType string

const (
	// FailureTypeToolError means a tool execution failed.
	FailureTypeToolError FailureType = "tool_error"

	// FailureTypeCycleDetected means a reasoning cycle was detected.
	FailureTypeCycleDetected FailureType = "cycle_detected"

	// FailureTypeCircuitBreaker means the circuit breaker intervened.
	FailureTypeCircuitBreaker FailureType = "circuit_breaker"

	// FailureTypeTimeout means the operation timed out.
	FailureTypeTimeout FailureType = "timeout"

	// FailureTypeInvalidOutput means the output was invalid.
	FailureTypeInvalidOutput FailureType = "invalid_output"

	// FailureTypeSafety means the safety gate blocked the operation.
	FailureTypeSafety FailureType = "safety"

	// FailureTypeSemanticRepetition means a semantically similar tool call was detected.
	// CB-30c: Added to prevent repeated similar queries (e.g., Grep("parseConfig") then Grep("parse_config")).
	FailureTypeSemanticRepetition FailureType = "semantic_repetition"

	// FailureTypeBatchFiltered means the router filtered out this tool call as redundant.
	// GR-39a: Added for batch filter learning to inform CDCL clause generation.
	FailureTypeBatchFiltered FailureType = "batch_filtered"
)

// String returns the string representation of FailureType.
func (f FailureType) String() string {
	return string(f)
}

// IsValid returns true if the failure type is a known value.
func (f FailureType) IsValid() bool {
	switch f {
	case FailureTypeToolError, FailureTypeCycleDetected, FailureTypeCircuitBreaker,
		FailureTypeTimeout, FailureTypeInvalidOutput, FailureTypeSafety,
		FailureTypeSemanticRepetition, FailureTypeBatchFiltered:
		return true
	default:
		return false
	}
}

// FailureEvent represents something the agent should learn from.
//
// Description:
//
//	When a failure occurs (tool error, cycle, circuit breaker), a FailureEvent
//	is created and passed to the Learning Activity for CDCL analysis. The
//	Learning Activity extracts a conflict clause that prevents the same failure.
//
// Thread Safety: FailureEvent is immutable after creation.
type FailureEvent struct {
	// SessionID is the session where the failure occurred.
	SessionID string `json:"session_id"`

	// FailureType classifies the type of failure.
	FailureType FailureType `json:"failure_type"`

	// DecisionPath is the sequence of decisions leading to failure.
	// This is analyzed by CDCL to find the conflict cut.
	DecisionPath []StepRecord `json:"decision_path"`

	// FailedStep is the step that failed.
	FailedStep StepRecord `json:"failed_step"`

	// ErrorMessage is the error message if applicable.
	ErrorMessage string `json:"error_message,omitempty"`

	// ErrorCategory categorizes the error for learning.
	ErrorCategory ErrorCategory `json:"error_category,omitempty"`

	// Tool is the tool that was involved in the failure.
	Tool string `json:"tool,omitempty"`

	// Source indicates signal source (should be hard for learning).
	Source SignalSource `json:"source"`
}

// Validate checks that the FailureEvent has required fields.
func (e *FailureEvent) Validate() error {
	if e.SessionID == "" {
		return errors.New("session_id must not be empty")
	}
	if !e.FailureType.IsValid() {
		return fmt.Errorf("failure_type %q is not valid", e.FailureType)
	}
	if !e.Source.IsValid() {
		return fmt.Errorf("source %d is not valid", e.Source)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Clause Types (CRS-04 - CDCL Learned Clauses)
// -----------------------------------------------------------------------------

// Literal represents a single variable assignment in a clause.
//
// Description:
//
//	A literal is either a variable (positive) or its negation (negative).
//	Variables use the format "<type>:<value>" e.g., "tool:list_packages",
//	"outcome:success", "error:file_not_found".
//
// Thread Safety: Literal is immutable after creation.
type Literal struct {
	// Variable is the decision variable name.
	// Format: "<type>:<value>" e.g., "tool:list_packages", "outcome:success"
	Variable string `json:"variable"`

	// Negated indicates if this literal is negated (NOT).
	Negated bool `json:"negated"`
}

// String returns the string representation of a Literal.
func (l Literal) String() string {
	if l.Negated {
		return "¬" + l.Variable
	}
	return l.Variable
}

// Clause represents a learned constraint in CNF (Conjunctive Normal Form).
//
// Description:
//
//	A clause is a disjunction (OR) of literals that must be satisfied.
//	Learned clauses prevent the agent from repeating the same mistakes.
//
//	Example: (¬tool:list_packages ∨ ¬outcome:success ∨ ¬tool:list_packages)
//	Meaning: "Don't select list_packages after it already succeeded"
//
// Thread Safety: Clause is safe for concurrent read access.
type Clause struct {
	// ID is the unique clause identifier.
	ID string `json:"id"`

	// Literals are the disjuncts (OR'd together).
	Literals []Literal `json:"literals"`

	// Source indicates where this clause was learned.
	// Must be HARD for CDCL-learned clauses.
	Source SignalSource `json:"source"`

	// LearnedAt is when this clause was created (Unix milliseconds UTC).
	LearnedAt int64 `json:"learned_at"`

	// FailureType categorizes what kind of failure created this clause.
	FailureType FailureType `json:"failure_type"`

	// SessionID is the session where this was learned (for debugging).
	SessionID string `json:"session_id,omitempty"`

	// UseCount tracks how often this clause blocks decisions (for GC).
	UseCount int64 `json:"use_count"`

	// LastUsed is when this clause last blocked a decision (Unix milliseconds UTC).
	LastUsed int64 `json:"last_used,omitempty"`
}

// String returns the string representation of a Clause.
func (c *Clause) String() string {
	if len(c.Literals) == 0 {
		return "(empty)"
	}
	lits := make([]string, len(c.Literals))
	for i, lit := range c.Literals {
		lits[i] = lit.String()
	}
	return "(" + strings.Join(lits, " ∨ ") + ")"
}

// IsSatisfied checks if the clause is satisfied by the given assignment.
//
// Description:
//
//	A clause is satisfied if ANY literal is true (disjunction).
//	For a literal to be true:
//	  - If positive: the variable must be true in the assignment
//	  - If negated: the variable must be false in the assignment
//
// Inputs:
//
//	assignment - Map of variable names to their boolean values.
//
// Outputs:
//
//	bool - True if the clause is satisfied.
func (c *Clause) IsSatisfied(assignment map[string]bool) bool {
	// Clause is satisfied if ANY literal is true (disjunction)
	for _, lit := range c.Literals {
		val, exists := assignment[lit.Variable]
		if !exists {
			continue // Unknown variable doesn't satisfy
		}
		if lit.Negated {
			if !val {
				return true // ¬X is true when X is false
			}
		} else {
			if val {
				return true // X is true when X is true
			}
		}
	}
	return false
}

// IsViolated checks if the clause is violated by the given assignment.
//
// Description:
//
//	A clause is violated when ALL literals are false under the assignment.
//	This is the opposite of IsSatisfied, but with explicit handling:
//	if a variable is unassigned, the clause is not yet violated.
//
// Inputs:
//
//	assignment - Map of variable names to their boolean values.
//
// Outputs:
//
//	bool - True if the clause is violated (all literals are false).
func (c *Clause) IsViolated(assignment map[string]bool) bool {
	if len(c.Literals) == 0 {
		return true // Empty clause is always violated
	}

	for _, lit := range c.Literals {
		val, exists := assignment[lit.Variable]
		if !exists {
			// Variable not assigned yet - clause not yet violated
			return false
		}
		// Check if this literal is true
		literalTrue := (lit.Negated && !val) || (!lit.Negated && val)
		if literalTrue {
			return false // At least one literal is true
		}
	}
	return true // All literals are false
}

// Validate checks that the Clause has required fields.
func (c *Clause) Validate() error {
	if c.ID == "" {
		return errors.New("clause ID must not be empty")
	}
	if len(c.Literals) == 0 {
		return errors.New("clause must have at least one literal")
	}
	// CR-4: Validate each literal has a non-empty variable
	for i, lit := range c.Literals {
		if lit.Variable == "" {
			return fmt.Errorf("literal %d has empty variable", i)
		}
	}
	// CDCL clauses must come from hard signals
	if !c.Source.IsHard() {
		return errors.New("clause source must be hard (CDCL rule)")
	}
	return nil
}

// ClauseScope determines clause visibility and persistence.
type ClauseScope string

const (
	// ClauseScopeSession: Clauses only valid for current session.
	// TTL: session duration. GC: session end.
	ClauseScopeSession ClauseScope = "session"

	// ClauseScopeProject: Clauses valid across sessions for same project.
	// TTL: 7 days. GC: LRU eviction when MaxClauses reached.
	ClauseScopeProject ClauseScope = "project"

	// ClauseScopeGlobal: Clauses valid across all projects (rare).
	// TTL: 30 days. GC: Manual review required.
	// Use sparingly - only for universal patterns like "don't infinite loop".
	ClauseScopeGlobal ClauseScope = "global"
)

// ClausePersistence configures clause storage and garbage collection.
type ClausePersistence struct {
	// Scope determines clause visibility.
	Scope ClauseScope

	// TTL is how long clauses remain valid.
	TTL time.Duration

	// MaxClauses is the maximum clauses per scope (LRU eviction).
	MaxClauses int
}

// DefaultClauseConfig is the default clause persistence configuration.
var DefaultClauseConfig = ClausePersistence{
	Scope:      ClauseScopeProject,
	TTL:        7 * 24 * time.Hour, // 7 days
	MaxClauses: 1000,               // LRU eviction after 1000
}

// ClauseCheckResult contains the result of checking an assignment against clauses.
type ClauseCheckResult struct {
	// Conflict is true if a clause is violated by the assignment.
	Conflict bool `json:"conflict"`

	// ViolatedClause is the clause that was violated, if any.
	ViolatedClause *Clause `json:"violated_clause,omitempty"`

	// Reason explains why the conflict occurred.
	Reason string `json:"reason,omitempty"`
}
