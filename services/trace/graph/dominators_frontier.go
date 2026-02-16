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
	"log/slog"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// =============================================================================
// Dominance Frontier (GR-16d)
// =============================================================================

var dominanceFrontierTracer = otel.Tracer("graph.dominance_frontier")

// frontierContextCheckInterval is how often to check for context cancellation.
const frontierContextCheckInterval = 500

// DominanceFrontier contains the computed dominance frontiers.
//
// The dominance frontier DF(n) for a node n contains all nodes y such that:
//   - n dominates a predecessor of y, but
//   - n does NOT strictly dominate y
//
// In plain terms: DF(n) = "where n's control ends"
//
// Key concepts:
//   - Frontier: Maps each node to its dominance frontier nodes
//   - MergePoints: Nodes appearing in 2+ frontiers (control flow convergence)
//   - MergePointDegree: How many paths converge at a merge point
//
// Usage:
//
//	domTree, _ := analytics.Dominators(ctx, "main")
//	df, _ := analytics.ComputeDominanceFrontier(ctx, domTree)
//
//	// Find where node's control ends
//	frontier := df.GetFrontier("nodeID")
//
//	// Check if a node is a merge point
//	if df.IsMergePoint("nodeID") {
//	    degree := df.MergePointDegree("nodeID")
//	}
//
// Thread Safety: Safe for concurrent use after construction.
type DominanceFrontier struct {
	// Frontier maps each node to its dominance frontier.
	// DF(n) contains nodes where n's dominance ends.
	Frontier map[string][]string

	// MergePoints contains nodes that appear in multiple frontiers.
	// These are control flow convergence points.
	MergePoints []string

	// MergePointCount maps merge point nodeID to count of frontiers it appears in.
	// Useful for ranking merge points by convergence degree.
	MergePointCount map[string]int

	// DomTree is the underlying dominator tree used.
	DomTree *DominatorTree

	// NodeCount is the number of nodes analyzed.
	NodeCount int

	// NodesWithFrontiers is the count of nodes with non-empty frontiers.
	NodesWithFrontiers int

	// TotalFrontierEntries is the sum of all frontier sizes.
	TotalFrontierEntries int

	// MaxFrontierSize is the largest individual frontier.
	MaxFrontierSize int
}

// GetFrontier returns the dominance frontier for a node.
//
// Description:
//
//	Returns the set of nodes where the specified node's dominance ends.
//	A node Y is in DF(X) if X dominates a predecessor of Y but does not
//	strictly dominate Y.
//
// Inputs:
//
//   - nodeID: The node to query the frontier for. Must be a valid node ID.
//
// Outputs:
//
//   - []string: The dominance frontier of the node, or nil if the node
//     has no frontier or the receiver is nil.
//
// Limitations:
//
//   - Returns nil for nodes not in the dominator tree.
//   - The returned slice should not be modified by the caller.
//
// Thread Safety: Safe for concurrent use.
//
// Complexity: O(1) map lookup.
func (df *DominanceFrontier) GetFrontier(nodeID string) []string {
	if df == nil || df.Frontier == nil {
		return nil
	}
	return df.Frontier[nodeID]
}

// IsMergePoint returns true if the node is a merge point.
//
// Description:
//
//	A merge point is a node that appears in the dominance frontier of
//	two or more nodes, indicating it is a control flow convergence point
//	where execution from different paths meets.
//
// Inputs:
//
//   - nodeID: The node to check. Must be a valid node ID.
//
// Outputs:
//
//   - bool: True if the node is in 2+ frontiers, false otherwise.
//
// Limitations:
//
//   - Returns false if receiver is nil or nodeID is not in any frontier.
//
// Thread Safety: Safe for concurrent use.
//
// Complexity: O(1) map lookup.
func (df *DominanceFrontier) IsMergePoint(nodeID string) bool {
	if df == nil || df.MergePointCount == nil {
		return false
	}
	return df.MergePointCount[nodeID] >= 2
}

// MergePointDegree returns how many frontiers contain this node.
//
// Description:
//
//	Returns the number of distinct dominance frontiers that include
//	this node. A higher degree indicates more control flow paths
//	converge at this point.
//
// Inputs:
//
//   - nodeID: The node to query. Must be a valid node ID.
//
// Outputs:
//
//   - int: The number of frontiers containing this node. Returns 0
//     if the node is not in any frontier or the receiver is nil.
//
// Limitations:
//
//   - Returns 0 for nodes not appearing in any frontier.
//
// Thread Safety: Safe for concurrent use.
//
// Complexity: O(1) map lookup.
func (df *DominanceFrontier) MergePointDegree(nodeID string) int {
	if df == nil || df.MergePointCount == nil {
		return 0
	}
	return df.MergePointCount[nodeID]
}

// DominanceFrontierError represents an error in dominance frontier computation.
type DominanceFrontierError struct {
	Message string
}

func (e *DominanceFrontierError) Error() string {
	return e.Message
}

// ComputeDominanceFrontier computes dominance frontiers for all nodes.
//
// Description:
//
//	For each node n, DF(n) contains nodes where n's dominance ends.
//	Uses the standard algorithm from Cytron et al. (1991).
//	A node m is in DF(n) iff:
//	  1. n dominates a predecessor of m
//	  2. n does NOT strictly dominate m
//
// Inputs:
//
//   - ctx: Context for cancellation. Checked every 500 nodes.
//   - domTree: Pre-computed dominator tree. Must not be nil.
//
// Outputs:
//
//   - *DominanceFrontier: Frontiers for all nodes. Never nil.
//   - error: Non-nil if domTree is nil or context cancelled.
//
// Example:
//
//	domTree, _ := analytics.Dominators(ctx, "main")
//	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	mergePoints := df.MergePoints
//
// Limitations:
//
//   - Only computes frontiers for nodes in the dominator tree
//   - Unreachable nodes have no frontier
//   - Memory: O(V) typical, O(V²) worst case
//
// Assumptions:
//
//   - domTree is valid and consistent
//   - Graph is frozen
//
// Thread Safety: Safe for concurrent use (read-only on graph and domTree).
//
// Complexity: O(E × depth), typically O(E) for shallow trees.
func (a *GraphAnalytics) ComputeDominanceFrontier(
	ctx context.Context,
	domTree *DominatorTree,
) (*DominanceFrontier, error) {
	// Initialize result with empty structures
	result := &DominanceFrontier{
		Frontier:        make(map[string][]string),
		MergePoints:     make([]string, 0),
		MergePointCount: make(map[string]int),
	}

	// Nil dominator tree check
	if domTree == nil {
		telemetry.LoggerWithTrace(ctx, slog.Default()).Debug("dominance_frontier: nil dominator tree")
		return result, &DominanceFrontierError{Message: "dominator tree must not be nil"}
	}

	// Validate dominator tree has valid entry
	if domTree.Entry == "" {
		telemetry.LoggerWithTrace(ctx, slog.Default()).Debug("dominance_frontier: dominator tree has empty entry")
		return result, &DominanceFrontierError{Message: "dominator tree has empty entry node"}
	}

	result.DomTree = domTree

	// Empty dominator tree check (GR-17b Fix)
	// An empty dominator tree with a valid entry point indicates the graph
	// is not fully populated yet (initialization race condition).
	// This should be an error, not a silent empty result.
	if len(domTree.ImmediateDom) == 0 {
		telemetry.LoggerWithTrace(ctx, slog.Default()).Warn(
			"dominance_frontier: empty dominator tree - graph may not be ready",
			slog.String("entry", domTree.Entry),
		)
		return result, &DominanceFrontierError{
			Message: "dominator tree has no nodes - graph may not be indexed yet or entry point is isolated",
		}
	}

	// Start timing
	startTime := time.Now()

	// Start OTel span
	ctx, span := dominanceFrontierTracer.Start(ctx, "GraphAnalytics.ComputeDominanceFrontier",
		trace.WithAttributes(
			attribute.String("entry", domTree.Entry),
			attribute.Int("node_count", len(domTree.ImmediateDom)),
		),
	)
	defer span.End()

	// Context check
	if ctx != nil && ctx.Err() != nil {
		span.AddEvent("context_cancelled_early")
		telemetry.LoggerWithTrace(ctx, slog.Default()).Debug("dominance_frontier: context cancelled before start")
		return result, ctx.Err()
	}

	// Nil graph check
	if a == nil || a.graph == nil {
		telemetry.LoggerWithTrace(ctx, slog.Default()).Debug("dominance_frontier: nil analytics or graph")
		return result, nil
	}

	result.NodeCount = len(domTree.ImmediateDom)

	// Use set semantics internally to prevent duplicates
	// frontierSets[node] contains the set of nodes in DF(node)
	frontierSets := make(map[string]map[string]struct{}, len(domTree.ImmediateDom))

	iterationsProcessed := 0

	// Algorithm: For each node n with multiple predecessors
	// Walk up the dominator tree from each predecessor until reaching idom(n)
	// Each node on the path (except idom(n)) has n in its frontier
	allNodes := a.graph.Nodes()

	for _, node := range allNodes {
		iterationsProcessed++

		// Check context periodically
		if iterationsProcessed%frontierContextCheckInterval == 0 {
			if ctx != nil && ctx.Err() != nil {
				span.AddEvent("context_cancelled", trace.WithAttributes(
					attribute.Int("nodes_processed", iterationsProcessed),
				))
				telemetry.LoggerWithTrace(ctx, slog.Default()).Info("dominance_frontier: context cancelled",
					slog.Int("nodes_processed", iterationsProcessed),
				)
				// Convert sets to slices before returning
				a.convertFrontierSetsToSlices(result, frontierSets)
				return result, ctx.Err()
			}
		}

		nodeID := node.ID

		// Skip nodes not in dominator tree (unreachable)
		idomNode, inTree := domTree.ImmediateDom[nodeID]
		if !inTree {
			continue
		}

		// Get predecessors (incoming edges)
		preds := node.Incoming
		if len(preds) < 2 {
			// Nodes with < 2 predecessors can't be join points
			// (but we still process them for completeness in case they're
			// reachable from multiple paths in the dominator tree)
			if len(preds) == 0 {
				continue
			}
		}

		// For each predecessor, walk up the dominator tree
		for _, pred := range preds {
			predID := pred.FromID

			// Skip self-loops
			if predID == nodeID {
				continue
			}

			// Skip predecessors not in dominator tree
			if _, predInTree := domTree.ImmediateDom[predID]; !predInTree {
				continue
			}

			// Walk up from predecessor to idom(node)
			runner := predID
			chainLength := 0
			maxChainLength := len(domTree.ImmediateDom) + 1 // Safety limit

			for runner != idomNode && runner != "" && chainLength < maxChainLength {
				// Add nodeID to DF(runner)
				if frontierSets[runner] == nil {
					frontierSets[runner] = make(map[string]struct{})
				}
				frontierSets[runner][nodeID] = struct{}{}

				// Move up to parent in dominator tree
				nextRunner, ok := domTree.ImmediateDom[runner]
				if !ok || nextRunner == runner {
					// Reached root or cycle - stop
					break
				}
				runner = nextRunner
				chainLength++
			}

			if chainLength >= maxChainLength {
				telemetry.LoggerWithTrace(ctx, slog.Default()).Warn("dominance_frontier: idom chain exceeded max length",
					slog.String("node", nodeID),
					slog.String("pred", predID),
					slog.Int("chain_length", chainLength),
				)
			}
		}
	}

	// Convert sets to slices
	a.convertFrontierSetsToSlices(result, frontierSets)

	// Compute statistics
	for _, frontier := range result.Frontier {
		if len(frontier) > 0 {
			result.NodesWithFrontiers++
			result.TotalFrontierEntries += len(frontier)
			if len(frontier) > result.MaxFrontierSize {
				result.MaxFrontierSize = len(frontier)
			}
		}
	}

	span.AddEvent("computation_complete", trace.WithAttributes(
		attribute.Int("merge_points", len(result.MergePoints)),
		attribute.Int("nodes_with_frontiers", result.NodesWithFrontiers),
		attribute.Int("total_frontier_entries", result.TotalFrontierEntries),
		attribute.Int("max_frontier_size", result.MaxFrontierSize),
	))

	telemetry.LoggerWithTrace(ctx, slog.Default()).Debug("dominance_frontier: computation complete",
		slog.Int("merge_points", len(result.MergePoints)),
		slog.Int("nodes_with_frontiers", result.NodesWithFrontiers),
		slog.Int("total_entries", result.TotalFrontierEntries),
		slog.Duration("duration", time.Since(startTime)),
	)

	return result, nil
}

// convertFrontierSetsToSlices converts internal set representation to slices.
// Also computes merge points and their degrees.
func (a *GraphAnalytics) convertFrontierSetsToSlices(
	result *DominanceFrontier,
	frontierSets map[string]map[string]struct{},
) {
	// Count how many frontiers each node appears in
	nodeCount := make(map[string]int)

	for nodeID, frontierSet := range frontierSets {
		frontier := make([]string, 0, len(frontierSet))
		for member := range frontierSet {
			frontier = append(frontier, member)
			nodeCount[member]++
		}
		result.Frontier[nodeID] = frontier
	}

	// Identify merge points (nodes in 2+ frontiers)
	result.MergePointCount = nodeCount
	for nodeID, count := range nodeCount {
		if count >= 2 {
			result.MergePoints = append(result.MergePoints, nodeID)
		}
	}
}

// ComputeDominanceFrontierWithCRS wraps ComputeDominanceFrontier with CRS tracing.
//
// Description:
//
//	Provides the same functionality as ComputeDominanceFrontier but also returns
//	a TraceStep for recording in the Code Reasoning State (CRS).
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) ComputeDominanceFrontierWithCRS(
	ctx context.Context,
	domTree *DominatorTree,
) (*DominanceFrontier, crs.TraceStep) {
	start := time.Now()

	// Handle nil domTree case
	if domTree == nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_dominance_frontier").
			WithTarget("").
			WithTool("ComputeDominanceFrontier").
			WithDuration(time.Since(start)).
			WithError("dominator tree must not be nil").
			Build()
		return &DominanceFrontier{
			Frontier:        make(map[string][]string),
			MergePoints:     []string{},
			MergePointCount: make(map[string]int),
		}, step
	}

	// Early cancellation check
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_dominance_frontier").
			WithTarget(domTree.Entry).
			WithTool("ComputeDominanceFrontier").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return &DominanceFrontier{
			Frontier:        make(map[string][]string),
			MergePoints:     []string{},
			MergePointCount: make(map[string]int),
			DomTree:         domTree,
		}, step
	}

	result, err := a.ComputeDominanceFrontier(ctx, domTree)

	// Build TraceStep
	builder := crs.NewTraceStepBuilder().
		WithAction("analytics_dominance_frontier").
		WithTarget(domTree.Entry).
		WithTool("ComputeDominanceFrontier").
		WithDuration(time.Since(start))

	if err != nil {
		builder = builder.WithError(err.Error())
	} else {
		builder = builder.
			WithMetadata("merge_points_found", itoa(len(result.MergePoints))).
			WithMetadata("nodes_with_frontiers", itoa(result.NodesWithFrontiers)).
			WithMetadata("total_frontier_entries", itoa(result.TotalFrontierEntries)).
			WithMetadata("max_frontier_size", itoa(result.MaxFrontierSize)).
			WithMetadata("entry", domTree.Entry)
	}

	return result, builder.Build()
}
