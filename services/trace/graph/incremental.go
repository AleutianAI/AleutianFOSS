// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// incrementalTracer is the OTel tracer for incremental graph operations.
var incrementalTracer = otel.Tracer("graph.incremental")

// IncrementalRefreshThreshold is the maximum ratio of changed files to total files
// before falling back to a full rebuild. If changedFiles/totalFiles > 0.30,
// incremental overhead exceeds full build cost.
const IncrementalRefreshThreshold = 0.30

// IncrementalResult contains the outcome of an incremental graph refresh.
//
// Thread Safety: Immutable after construction.
type IncrementalResult struct {
	// Graph is the updated graph (frozen, read-only).
	Graph *Graph

	// Strategy is "incremental" or "full_rebuild".
	Strategy string

	// ChangedFiles is the list of files that were refreshed.
	ChangedFiles []string

	// NodesRemoved is the count of nodes removed from changed files.
	NodesRemoved int

	// NodesAdded is the count of nodes added from re-parsed files.
	NodesAdded int

	// EdgesCreated is the count of edges created during re-extraction.
	EdgesCreated int

	// DurationMilli is the total time for the incremental refresh.
	DurationMilli int64

	// FallbackReason is non-empty if a full rebuild was used instead.
	FallbackReason string
}

// ShouldDoIncrementalUpdate determines whether an incremental update is appropriate.
//
// Description:
//
//	Returns true if the ratio of changed files to total files is at or below
//	the IncrementalRefreshThreshold (30%). Returns false if totalFiles is 0
//	to avoid division by zero.
//
// Inputs:
//   - changedCount: Number of files that changed since last snapshot.
//   - totalCount: Total number of files in the project.
//
// Outputs:
//   - bool: true if incremental update should be used.
//
// Thread Safety: Safe for concurrent use (stateless).
func ShouldDoIncrementalUpdate(changedCount, totalCount int) bool {
	if totalCount <= 0 {
		return false
	}
	if changedCount == 0 {
		return true // No changes — load snapshot as-is
	}
	if changedCount < 0 {
		return false // Invalid input
	}
	ratio := float64(changedCount) / float64(totalCount)
	return ratio <= IncrementalRefreshThreshold
}

// IncrementalRefresh applies changes to a previously-saved graph by removing
// nodes/edges for changed files and re-adding them from fresh parse results.
//
// Description:
//
//	Given a base graph (from a prior snapshot) and parse results for changed
//	files, this function:
//	  1. Clones the base graph to get a writable copy
//	  2. Removes all nodes and edges for each changed file
//	  3. Adds new nodes from the parse results
//	  4. Re-extracts per-file edges for the changed files
//	  5. Freezes the updated graph
//
//	Cross-file resolution (interface matching, Python import resolution) is
//	NOT re-run. Existing edges from unchanged files are preserved via Clone.
//	For changed files, per-file edges (imports, calls, returns) are re-extracted
//	against the full symbol set. If structural changes affect cross-file edges
//	(e.g., interface method additions, renamed exports), CRS-19 graph staleness
//	detection catches the discrepancy.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - baseGraph: The previous session's graph (frozen). Must not be nil.
//   - changedFiles: File paths (relative to project root) that changed.
//   - changedResults: Parse results for ONLY the changed files.
//
// Outputs:
//   - *IncrementalResult: The result of the refresh operation.
//   - error: Non-nil if the operation fails.
//
// Limitations:
//   - Cross-file edges FROM unchanged files TO renamed symbols are not updated.
//   - Interface implementation edges are not recomputed (use full rebuild for
//     interface signature changes).
//
// Thread Safety: Safe for concurrent use (creates a new graph via Clone).
func IncrementalRefresh(
	ctx context.Context,
	baseGraph *Graph,
	changedFiles []string,
	changedResults []*ast.ParseResult,
) (*IncrementalResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if baseGraph == nil {
		return nil, fmt.Errorf("base graph must not be nil")
	}

	ctx, span := incrementalTracer.Start(ctx, "graph.IncrementalRefresh",
		trace.WithAttributes(
			attribute.Int("changed_files", len(changedFiles)),
			attribute.Int("base_nodes", baseGraph.NodeCount()),
			attribute.Int("base_edges", baseGraph.EdgeCount()),
		),
	)
	defer span.End()

	start := time.Now()
	logger := slog.Default()

	// Clone the base graph to get a writable copy.
	// Clone sets state to GraphStateBuilding, preserving all nodes and edges.
	working := baseGraph.Clone()

	result := &IncrementalResult{
		Strategy:     "incremental",
		ChangedFiles: changedFiles,
	}

	// Phase 1: Remove old data for changed files.
	// RemoveFile atomically removes all nodes and edges for a file,
	// rebuilding all secondary indexes (nodesByName, nodesByKind,
	// edgesByType, edgesByFile).
	for _, f := range changedFiles {
		removed, err := working.RemoveFile(f)
		if err != nil {
			return nil, fmt.Errorf("removing file %s: %w", f, err)
		}
		result.NodesRemoved += removed
	}

	logger.Info("CRS-18: Removed old data for changed files",
		slog.Int("files", len(changedFiles)),
		slog.Int("nodes_removed", result.NodesRemoved),
	)

	// Phase 2: Reconstruct build state from existing nodes.
	// Edge extraction resolves calls by name, so we need the complete
	// symbolsByName map (unchanged + newly added symbols).
	builder := NewBuilder(WithProjectRoot(baseGraph.ProjectRoot))
	state := &buildState{
		graph: working,
		result: &BuildResult{
			FileErrors: make([]FileError, 0),
			EdgeErrors: make([]EdgeError, 0),
		},
		symbolsByID:            make(map[string]*ast.Symbol),
		symbolsByName:          make(map[string][]*ast.Symbol),
		fileImports:            make(map[string][]ast.Import),
		placeholders:           make(map[string]*Node),
		symbolParent:           make(map[string]string),
		classExtends:           make(map[string]string),
		classAdditionalParents: make(map[string][]string),
		importNameMap:          make(map[string]map[string]importEntry),
		startTime:              time.Now(),
	}
	state.result.Graph = working

	// Populate symbolsByID and symbolsByName from remaining (unchanged) nodes.
	for _, node := range working.Nodes() {
		if node.Symbol != nil {
			state.symbolsByID[node.Symbol.ID] = node.Symbol
			state.symbolsByName[node.Symbol.Name] = append(
				state.symbolsByName[node.Symbol.Name], node.Symbol,
			)
		}
	}

	// Phase 3: Add new symbols from changed files.
	if err := builder.collectPhase(ctx, state, changedResults); err != nil {
		return nil, fmt.Errorf("collecting symbols from changed files: %w", err)
	}
	result.NodesAdded = state.result.Stats.NodesCreated

	// Phase 4: Populate imports for changed files and build import name map.
	for _, r := range changedResults {
		if r != nil {
			state.fileImports[r.FilePath] = r.Imports
		}
	}
	builder.buildImportNameMap(state)

	// Phase 5: Re-extract per-file edges for changed files.
	// This creates import edges, call edges, return/parameter edges for
	// the changed files, resolving against the full symbolsByName map.
	for _, r := range changedResults {
		if r == nil {
			continue
		}
		builder.extractFileEdges(ctx, state, r)
	}
	result.EdgesCreated = state.result.Stats.EdgesCreated

	// Freeze the updated graph.
	working.Freeze()

	result.Graph = working
	result.DurationMilli = time.Since(start).Milliseconds()

	span.SetAttributes(
		attribute.Int("nodes_removed", result.NodesRemoved),
		attribute.Int("nodes_added", result.NodesAdded),
		attribute.Int("edges_created", result.EdgesCreated),
		attribute.Int64("duration_ms", result.DurationMilli),
	)

	logger.Info("CRS-18: Incremental refresh complete",
		slog.Int("changed_files", len(changedFiles)),
		slog.Int("nodes_removed", result.NodesRemoved),
		slog.Int("nodes_added", result.NodesAdded),
		slog.Int("edges_created", result.EdgesCreated),
		slog.Int64("duration_ms", result.DurationMilli),
		slog.Int("total_nodes", working.NodeCount()),
		slog.Int("total_edges", working.EdgeCount()),
	)

	return result, nil
}
