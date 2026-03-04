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
	"errors"
	"fmt"
	"time"
)

// -----------------------------------------------------------------------------
// Package-level Index Slices (GR-37: Zero-allocation optimization)
// -----------------------------------------------------------------------------
//
// These slices are returned by IndexesAffected() methods to avoid allocating
// a new []string on every call. They are READ-ONLY - callers must not modify.
var (
	indexesProof      = []string{"proof"}
	indexesConstraint = []string{"constraint"}
	indexesSimilarity = []string{"similarity"}
	indexesDependency = []string{"dependency"}
	indexesHistory    = []string{"history"}
	indexesStreaming  = []string{"streaming"}
)

// -----------------------------------------------------------------------------
// Base Delta Implementation
// -----------------------------------------------------------------------------

// baseDelta provides common delta functionality.
type baseDelta struct {
	source    SignalSource
	timestamp int64 // Unix milliseconds UTC
}

func newBaseDelta(source SignalSource) baseDelta {
	return baseDelta{
		source:    source,
		timestamp: time.Now().UnixMilli(),
	}
}

func (d *baseDelta) Source() SignalSource {
	return d.source
}

func (d *baseDelta) Timestamp() int64 {
	return d.timestamp
}

// -----------------------------------------------------------------------------
// Proof Delta
// -----------------------------------------------------------------------------

// ProofDelta represents changes to proof numbers.
type ProofDelta struct {
	baseDelta

	// Updates maps node ID to new proof number.
	Updates map[string]ProofNumber
}

// NewProofDelta creates a new proof delta.
//
// Inputs:
//   - source: The signal source (hard or soft).
//   - updates: Map of node ID to proof number updates.
//
// Outputs:
//   - *ProofDelta: The new delta.
func NewProofDelta(source SignalSource, updates map[string]ProofNumber) *ProofDelta {
	return &ProofDelta{
		baseDelta: newBaseDelta(source),
		Updates:   updates,
	}
}

// Type returns the delta type.
func (d *ProofDelta) Type() DeltaType {
	return DeltaTypeProof
}

// Validate checks if this delta can be applied.
//
// Description:
//
//	Validates the hard/soft signal boundary. Soft signals cannot mark
//	nodes as DISPROVEN.
func (d *ProofDelta) Validate(snapshot Snapshot) error {
	for nodeID, proof := range d.Updates {
		// Hard/soft signal boundary check
		if proof.Status == ProofStatusDisproven && !d.source.IsHard() {
			return fmt.Errorf("%w: node %s cannot be marked DISPROVEN by soft signal",
				ErrHardSoftBoundaryViolation, nodeID)
		}
	}
	return nil
}

// Merge combines this delta with another delta.
func (d *ProofDelta) Merge(other Delta) (Delta, error) {
	otherProof, ok := other.(*ProofDelta)
	if !ok {
		// Different types - return composite
		return NewCompositeDelta(d, other), nil
	}

	// Merge proof deltas - later timestamp wins for conflicts
	merged := &ProofDelta{
		baseDelta: newBaseDelta(d.source),
		Updates:   make(map[string]ProofNumber, len(d.Updates)+len(otherProof.Updates)),
	}

	// Copy all from d
	for k, v := range d.Updates {
		merged.Updates[k] = v
	}

	// Merge from other, later timestamp wins
	for k, v := range otherProof.Updates {
		if existing, ok := merged.Updates[k]; ok {
			// Conflict - later timestamp wins (higher Unix millis = more recent)
			if v.UpdatedAt > existing.UpdatedAt {
				merged.Updates[k] = v
			}
		} else {
			merged.Updates[k] = v
		}
	}

	// Upgrade source to hard if either is hard
	if otherProof.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *ProofDelta) ConflictsWith(other Delta) bool {
	otherProof, ok := other.(*ProofDelta)
	if !ok {
		return false
	}

	for nodeID := range d.Updates {
		if _, ok := otherProof.Updates[nodeID]; ok {
			return true
		}
	}
	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *ProofDelta) IndexesAffected() []string {
	return indexesProof
}

// -----------------------------------------------------------------------------
// Constraint Delta
// -----------------------------------------------------------------------------

// ConstraintDelta represents changes to constraints.
type ConstraintDelta struct {
	baseDelta

	// Add contains constraints to add.
	Add []Constraint

	// Remove contains constraint IDs to remove.
	Remove []string

	// Update contains constraints to update (by ID).
	Update map[string]Constraint
}

// NewConstraintDelta creates a new constraint delta.
func NewConstraintDelta(source SignalSource) *ConstraintDelta {
	return &ConstraintDelta{
		baseDelta: newBaseDelta(source),
		Add:       make([]Constraint, 0),
		Remove:    make([]string, 0),
		Update:    make(map[string]Constraint),
	}
}

// Type returns the delta type.
func (d *ConstraintDelta) Type() DeltaType {
	return DeltaTypeConstraint
}

// Validate checks if this delta can be applied.
func (d *ConstraintDelta) Validate(snapshot Snapshot) error {
	ci := snapshot.ConstraintIndex()

	// Check that constraints to remove exist
	for _, id := range d.Remove {
		if _, ok := ci.Get(id); !ok {
			return fmt.Errorf("%w: constraint %s", ErrIndexNotFound, id)
		}
	}

	// Check that constraints to update exist
	for id := range d.Update {
		if _, ok := ci.Get(id); !ok {
			return fmt.Errorf("%w: constraint %s", ErrIndexNotFound, id)
		}
	}

	// Check for duplicate IDs in Add
	seen := make(map[string]bool)
	for _, c := range d.Add {
		if seen[c.ID] {
			return fmt.Errorf("%w: duplicate constraint ID %s", ErrDeltaValidation, c.ID)
		}
		seen[c.ID] = true

		// Check constraint doesn't already exist
		if _, ok := ci.Get(c.ID); ok {
			return fmt.Errorf("%w: constraint %s already exists", ErrDeltaValidation, c.ID)
		}
	}

	return nil
}

// Merge combines this delta with another delta.
func (d *ConstraintDelta) Merge(other Delta) (Delta, error) {
	otherConstraint, ok := other.(*ConstraintDelta)
	if !ok {
		return NewCompositeDelta(d, other), nil
	}

	merged := &ConstraintDelta{
		baseDelta: newBaseDelta(d.source),
		Add:       make([]Constraint, 0, len(d.Add)+len(otherConstraint.Add)),
		Remove:    make([]string, 0, len(d.Remove)+len(otherConstraint.Remove)),
		Update:    make(map[string]Constraint, len(d.Update)+len(otherConstraint.Update)),
	}

	// Merge adds
	merged.Add = append(merged.Add, d.Add...)
	merged.Add = append(merged.Add, otherConstraint.Add...)

	// Merge removes (deduplicate)
	removeSet := make(map[string]bool)
	for _, id := range d.Remove {
		removeSet[id] = true
	}
	for _, id := range otherConstraint.Remove {
		removeSet[id] = true
	}
	for id := range removeSet {
		merged.Remove = append(merged.Remove, id)
	}

	// Merge updates (later wins)
	for k, v := range d.Update {
		merged.Update[k] = v
	}
	for k, v := range otherConstraint.Update {
		merged.Update[k] = v
	}

	if otherConstraint.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *ConstraintDelta) ConflictsWith(other Delta) bool {
	otherConstraint, ok := other.(*ConstraintDelta)
	if !ok {
		return false
	}

	// Check if same constraint being added/removed/updated
	for _, c := range d.Add {
		for _, oc := range otherConstraint.Add {
			if c.ID == oc.ID {
				return true
			}
		}
	}

	for _, id := range d.Remove {
		for _, oid := range otherConstraint.Remove {
			if id == oid {
				return true
			}
		}
		if _, ok := otherConstraint.Update[id]; ok {
			return true
		}
	}

	for id := range d.Update {
		if _, ok := otherConstraint.Update[id]; ok {
			return true
		}
	}

	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *ConstraintDelta) IndexesAffected() []string {
	return indexesConstraint
}

// -----------------------------------------------------------------------------
// Dependency Delta
// -----------------------------------------------------------------------------

// DependencyDelta represents changes to dependencies.
type DependencyDelta struct {
	baseDelta

	// AddEdges contains edges to add (from -> to).
	AddEdges [][2]string

	// RemoveEdges contains edges to remove (from -> to).
	RemoveEdges [][2]string
}

// NewDependencyDelta creates a new dependency delta.
func NewDependencyDelta(source SignalSource) *DependencyDelta {
	return &DependencyDelta{
		baseDelta:   newBaseDelta(source),
		AddEdges:    make([][2]string, 0),
		RemoveEdges: make([][2]string, 0),
	}
}

// Type returns the delta type.
func (d *DependencyDelta) Type() DeltaType {
	return DeltaTypeDependency
}

// Validate checks if this delta can be applied.
func (d *DependencyDelta) Validate(snapshot Snapshot) error {
	di := snapshot.DependencyIndex()

	// Simulate adding edges and check for cycles
	// Build a temporary graph
	tempGraph := newDependencyGraph()

	// Copy existing edges by checking DependsOn for each potential node
	// This is a simplified check - in production you'd iterate all nodes
	for _, edge := range d.AddEdges {
		from, to := edge[0], edge[1]

		// Check if adding this edge would create a cycle
		// by checking if 'from' is reachable from 'to'
		if from == to {
			return fmt.Errorf("%w: self-dependency not allowed: %s", ErrDeltaValidation, from)
		}

		// Check existing reverse path
		deps := di.DependsOn(to)
		for _, dep := range deps {
			if dep == from {
				return fmt.Errorf("%w: cycle detected: %s -> %s -> %s", ErrDeltaValidation, from, to, from)
			}
		}

		tempGraph.addEdge(from, to)
	}

	// Check for cycles in temp graph
	for _, edge := range d.AddEdges {
		if tempGraph.hasCycle(edge[0]) {
			return fmt.Errorf("%w: adding edges would create cycle", ErrDeltaValidation)
		}
	}

	return nil
}

// Merge combines this delta with another delta.
func (d *DependencyDelta) Merge(other Delta) (Delta, error) {
	otherDep, ok := other.(*DependencyDelta)
	if !ok {
		return NewCompositeDelta(d, other), nil
	}

	merged := &DependencyDelta{
		baseDelta:   newBaseDelta(d.source),
		AddEdges:    make([][2]string, 0, len(d.AddEdges)+len(otherDep.AddEdges)),
		RemoveEdges: make([][2]string, 0, len(d.RemoveEdges)+len(otherDep.RemoveEdges)),
	}

	merged.AddEdges = append(merged.AddEdges, d.AddEdges...)
	merged.AddEdges = append(merged.AddEdges, otherDep.AddEdges...)
	merged.RemoveEdges = append(merged.RemoveEdges, d.RemoveEdges...)
	merged.RemoveEdges = append(merged.RemoveEdges, otherDep.RemoveEdges...)

	if otherDep.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *DependencyDelta) ConflictsWith(other Delta) bool {
	otherDep, ok := other.(*DependencyDelta)
	if !ok {
		return false
	}

	// Check for same edge being added and removed
	for _, edge := range d.AddEdges {
		for _, oedge := range otherDep.RemoveEdges {
			if edge[0] == oedge[0] && edge[1] == oedge[1] {
				return true
			}
		}
	}

	for _, edge := range d.RemoveEdges {
		for _, oedge := range otherDep.AddEdges {
			if edge[0] == oedge[0] && edge[1] == oedge[1] {
				return true
			}
		}
	}

	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *DependencyDelta) IndexesAffected() []string {
	return indexesDependency
}

// -----------------------------------------------------------------------------
// History Delta
// -----------------------------------------------------------------------------

// HistoryDelta represents additions to history.
type HistoryDelta struct {
	baseDelta

	// Entries contains history entries to add.
	Entries []HistoryEntry
}

// NewHistoryDelta creates a new history delta.
func NewHistoryDelta(source SignalSource, entries []HistoryEntry) *HistoryDelta {
	return &HistoryDelta{
		baseDelta: newBaseDelta(source),
		Entries:   entries,
	}
}

// Type returns the delta type.
func (d *HistoryDelta) Type() DeltaType {
	return DeltaTypeHistory
}

// Validate checks if this delta can be applied.
func (d *HistoryDelta) Validate(_ Snapshot) error {
	// History is append-only, validate entry structure
	for _, e := range d.Entries {
		if e.ID == "" {
			return fmt.Errorf("%w: history entry ID cannot be empty", ErrDeltaValidation)
		}
		// Validate metadata keys and values for safety
		for k, v := range e.Metadata {
			if k == "" {
				return fmt.Errorf("%w: history entry %s has empty metadata key", ErrDeltaValidation, e.ID)
			}
			if len(k) > 256 {
				return fmt.Errorf("%w: history entry %s metadata key too long (max 256)", ErrDeltaValidation, e.ID)
			}
			if len(v) > 4096 {
				return fmt.Errorf("%w: history entry %s metadata value too long (max 4096)", ErrDeltaValidation, e.ID)
			}
		}
	}
	return nil
}

// Merge combines this delta with another delta.
func (d *HistoryDelta) Merge(other Delta) (Delta, error) {
	otherHistory, ok := other.(*HistoryDelta)
	if !ok {
		return NewCompositeDelta(d, other), nil
	}

	merged := &HistoryDelta{
		baseDelta: newBaseDelta(d.source),
		Entries:   make([]HistoryEntry, 0, len(d.Entries)+len(otherHistory.Entries)),
	}
	merged.Entries = append(merged.Entries, d.Entries...)
	merged.Entries = append(merged.Entries, otherHistory.Entries...)

	if otherHistory.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *HistoryDelta) ConflictsWith(_ Delta) bool {
	// History is append-only, never conflicts
	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *HistoryDelta) IndexesAffected() []string {
	return indexesHistory
}

// -----------------------------------------------------------------------------
// Streaming Delta
// -----------------------------------------------------------------------------

// StreamingDelta represents updates to streaming statistics.
type StreamingDelta struct {
	baseDelta

	// Increments maps item to frequency increment.
	Increments map[string]uint64

	// CardinalityItems are items to count for cardinality.
	CardinalityItems []string
}

// NewStreamingDelta creates a new streaming delta.
func NewStreamingDelta(source SignalSource) *StreamingDelta {
	return &StreamingDelta{
		baseDelta:        newBaseDelta(source),
		Increments:       make(map[string]uint64),
		CardinalityItems: make([]string, 0),
	}
}

// Type returns the delta type.
func (d *StreamingDelta) Type() DeltaType {
	return DeltaTypeStreaming
}

// Validate checks if this delta can be applied.
func (d *StreamingDelta) Validate(_ Snapshot) error {
	// Streaming updates are always valid
	return nil
}

// Merge combines this delta with another delta.
func (d *StreamingDelta) Merge(other Delta) (Delta, error) {
	otherStreaming, ok := other.(*StreamingDelta)
	if !ok {
		return NewCompositeDelta(d, other), nil
	}

	merged := &StreamingDelta{
		baseDelta:        newBaseDelta(d.source),
		Increments:       make(map[string]uint64, len(d.Increments)+len(otherStreaming.Increments)),
		CardinalityItems: make([]string, 0, len(d.CardinalityItems)+len(otherStreaming.CardinalityItems)),
	}

	// Sum increments
	for k, v := range d.Increments {
		merged.Increments[k] = v
	}
	for k, v := range otherStreaming.Increments {
		merged.Increments[k] += v
	}

	// Combine cardinality items
	merged.CardinalityItems = append(merged.CardinalityItems, d.CardinalityItems...)
	merged.CardinalityItems = append(merged.CardinalityItems, otherStreaming.CardinalityItems...)

	if otherStreaming.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *StreamingDelta) ConflictsWith(_ Delta) bool {
	// Streaming is commutative, never conflicts
	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *StreamingDelta) IndexesAffected() []string {
	return indexesStreaming
}

// -----------------------------------------------------------------------------
// Similarity Delta
// -----------------------------------------------------------------------------

// SimilarityDelta represents changes to similarity scores.
type SimilarityDelta struct {
	baseDelta

	// Updates maps (node1, node2) to distance.
	Updates map[[2]string]float64
}

// NewSimilarityDelta creates a new similarity delta.
func NewSimilarityDelta(source SignalSource) *SimilarityDelta {
	return &SimilarityDelta{
		baseDelta: newBaseDelta(source),
		Updates:   make(map[[2]string]float64),
	}
}

// Type returns the delta type.
func (d *SimilarityDelta) Type() DeltaType {
	return DeltaTypeSimilarity
}

// Validate checks if this delta can be applied.
func (d *SimilarityDelta) Validate(_ Snapshot) error {
	for pair, dist := range d.Updates {
		// CRS-15 Code Review Fix #5: Reject empty keys.
		if pair[0] == "" || pair[1] == "" {
			return fmt.Errorf("%w: similarity keys must not be empty", ErrDeltaValidation)
		}
		if dist < 0 {
			return fmt.Errorf("%w: similarity distance cannot be negative: %s-%s", ErrDeltaValidation, pair[0], pair[1])
		}
		if pair[0] == pair[1] {
			return fmt.Errorf("%w: self-similarity not allowed: %s", ErrDeltaValidation, pair[0])
		}
	}
	return nil
}

// Merge combines this delta with another delta.
func (d *SimilarityDelta) Merge(other Delta) (Delta, error) {
	otherSim, ok := other.(*SimilarityDelta)
	if !ok {
		return NewCompositeDelta(d, other), nil
	}

	merged := &SimilarityDelta{
		baseDelta: newBaseDelta(d.source),
		Updates:   make(map[[2]string]float64, len(d.Updates)+len(otherSim.Updates)),
	}

	for k, v := range d.Updates {
		merged.Updates[k] = v
	}
	for k, v := range otherSim.Updates {
		merged.Updates[k] = v // Later wins
	}

	if otherSim.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if this delta conflicts with another.
func (d *SimilarityDelta) ConflictsWith(other Delta) bool {
	otherSim, ok := other.(*SimilarityDelta)
	if !ok {
		return false
	}

	for pair := range d.Updates {
		if _, ok := otherSim.Updates[pair]; ok {
			return true
		}
		// Check reverse pair
		reverse := [2]string{pair[1], pair[0]}
		if _, ok := otherSim.Updates[reverse]; ok {
			return true
		}
	}
	return false
}

// IndexesAffected returns which indexes this delta will modify.
//
// Thread Safety: Returns a shared slice. Callers must not modify.
func (d *SimilarityDelta) IndexesAffected() []string {
	return indexesSimilarity
}

// -----------------------------------------------------------------------------
// Composite Delta
// -----------------------------------------------------------------------------

// CompositeDelta contains multiple deltas for atomic application.
type CompositeDelta struct {
	baseDelta

	// Deltas are the contained deltas.
	Deltas []Delta
}

// NewCompositeDelta creates a composite delta from multiple deltas.
func NewCompositeDelta(deltas ...Delta) *CompositeDelta {
	source := SignalSourceSoft
	for _, d := range deltas {
		if d.Source().IsHard() {
			source = SignalSourceHard
			break
		}
	}

	return &CompositeDelta{
		baseDelta: newBaseDelta(source),
		Deltas:    deltas,
	}
}

// Type returns the delta type.
func (d *CompositeDelta) Type() DeltaType {
	return DeltaTypeComposite
}

// Validate checks if all contained deltas can be applied.
func (d *CompositeDelta) Validate(snapshot Snapshot) error {
	var errs []error
	for _, delta := range d.Deltas {
		if err := delta.Validate(snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Merge combines this delta with another delta.
func (d *CompositeDelta) Merge(other Delta) (Delta, error) {
	otherComposite, ok := other.(*CompositeDelta)
	if !ok {
		merged := &CompositeDelta{
			baseDelta: newBaseDelta(d.source),
			Deltas:    make([]Delta, 0, len(d.Deltas)+1),
		}
		merged.Deltas = append(merged.Deltas, d.Deltas...)
		merged.Deltas = append(merged.Deltas, other)

		if other.Source().IsHard() {
			merged.source = SignalSourceHard
		}
		return merged, nil
	}

	merged := &CompositeDelta{
		baseDelta: newBaseDelta(d.source),
		Deltas:    make([]Delta, 0, len(d.Deltas)+len(otherComposite.Deltas)),
	}
	merged.Deltas = append(merged.Deltas, d.Deltas...)
	merged.Deltas = append(merged.Deltas, otherComposite.Deltas...)

	if otherComposite.source.IsHard() {
		merged.source = SignalSourceHard
	}

	return merged, nil
}

// ConflictsWith returns true if any contained delta conflicts.
func (d *CompositeDelta) ConflictsWith(other Delta) bool {
	for _, delta := range d.Deltas {
		if delta.ConflictsWith(other) {
			return true
		}
	}
	return false
}

// IndexesAffected returns all indexes affected by contained deltas.
func (d *CompositeDelta) IndexesAffected() []string {
	seen := make(map[string]bool)
	var result []string
	for _, delta := range d.Deltas {
		for _, idx := range delta.IndexesAffected() {
			if !seen[idx] {
				seen[idx] = true
				result = append(result, idx)
			}
		}
	}
	return result
}
