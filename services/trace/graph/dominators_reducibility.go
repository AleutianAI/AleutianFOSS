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
	"math"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// =============================================================================
// Check Reducibility Algorithm (GR-16i)
// =============================================================================

var reducibilityTracer = otel.Tracer("graph.reducibility")

// reducibilityContextCheckInterval is how often to check for context cancellation.
const reducibilityContextCheckInterval = 1000

// maxIrreducibleRegions caps the number of irreducible regions to enumerate.
const maxIrreducibleRegions = 100

// ReducibilityResult contains the reducibility analysis output.
//
// A graph is reducible if all back edges go to dominators (natural loops only).
// Irreducible graphs have cross edges that create multi-entry regions.
//
// Thread Safety: Safe for concurrent use after construction.
type ReducibilityResult struct {
	// IsReducible is true if the entire graph is reducible.
	IsReducible bool

	// Score is the percentage of nodes NOT in irreducible regions.
	// Range: 0.0 to 1.0, where 1.0 means fully reducible.
	// Rounded to 4 decimal places for consistency.
	Score float64

	// IrreducibleRegions contains detected irreducible subgraphs.
	IrreducibleRegions []*IrreducibleRegion

	// Summary contains aggregate statistics.
	Summary ReducibilitySummary

	// Recommendation provides actionable guidance.
	Recommendation string

	// DomTree is the dominator tree used for analysis.
	DomTree *DominatorTree

	// NodeCount is total nodes analyzed.
	NodeCount int

	// EdgeCount is total edges analyzed.
	EdgeCount int

	// CrossEdgeCount is the number of cross edges found.
	CrossEdgeCount int
}

// IrreducibleRegion represents a detected irreducible subgraph.
//
// An irreducible region has multiple entry points - nodes that can be
// reached from outside the region via different paths.
//
// Thread Safety: Safe for concurrent use after construction.
type IrreducibleRegion struct {
	// ID is a unique 0-indexed identifier for this region.
	ID int

	// EntryNodes are the multiple entry points to this region.
	// An irreducible region has >= 2 entry points.
	EntryNodes []string

	// Nodes contains all nodes in this irreducible region.
	Nodes []string

	// Size is the number of nodes in the region.
	Size int

	// CrossEdges are the edges that form this irreducible pattern.
	CrossEdges [][2]string

	// Reason explains why this region is irreducible.
	Reason string
}

// ReducibilitySummary contains aggregate statistics.
type ReducibilitySummary struct {
	TotalNodes             int
	IrreducibleNodeCount   int
	IrreducibleRegionCount int
	WellStructuredPercent  float64
}

// reducibilityEdgeType represents the classification of an edge.
type reducibilityEdgeType int

const (
	edgeTypeUnknown reducibilityEdgeType = iota
	edgeTypeTree                         // Tree edge (forward in DFS)
	edgeTypeForward                      // Forward edge (to descendant)
	edgeTypeBack                         // Back edge (to ancestor/dominator)
	edgeTypeCross                        // Cross edge (neither dominates)
)

// CheckReducibility analyzes graph reducibility using dominator tree.
//
// Description:
//
//	Determines if the graph is reducible by classifying edges relative
//	to the dominator tree. A graph is reducible if and only if all
//	back edges go to dominators (natural loops only).
//
//	Identifies and enumerates irreducible regions - subgraphs with
//	multiple entry points that indicate complex control flow.
//
// Inputs:
//
//   - ctx: Context for cancellation. Must not be nil.
//   - domTree: Pre-computed dominator tree. Must not be nil.
//
// Outputs:
//
//   - *ReducibilityResult: Analysis results. Never nil on success.
//   - error: Non-nil on failure with context-wrapped message.
//
// Algorithm:
//
//  1. For each edge (u, v) in the graph:
//     - If domTree.Dominates(v, u): back edge (OK)
//     - If domTree.Dominates(u, v): forward edge (OK)
//     - Otherwise: cross edge (potentially irreducible)
//  2. For each cross edge, find the irreducible region it creates
//  3. Deduplicate overlapping regions
//  4. Compute reducibility score
//
// Complexity:
//
//   - Time: O(E × depth) for edge classification via dominator queries
//   - Time: O(V + E) for region enumeration per irreducible region
//   - Space: O(V) for tracking node membership
//
// Limitations:
//
//   - Maximum 100 irreducible regions enumerated
//   - Disconnected nodes (unreachable from entry) are not analyzed
//
// Assumptions:
//
//   - domTree was computed from the same graph
//   - Graph is frozen before analysis
//
// Thread Safety: Safe for concurrent use (read-only on graph/domTree).
func (a *GraphAnalytics) CheckReducibility(ctx context.Context, domTree *DominatorTree) (*ReducibilityResult, error) {
	// Input validation
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if domTree == nil {
		return nil, fmt.Errorf("domTree must not be nil")
	}

	// Check context early
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reducibility check cancelled: %w", err)
	}

	// Get graph metrics
	nodeCount := a.graph.NodeCount()
	edgeCount := a.graph.EdgeCount()

	// Allow empty entry only for empty graphs
	if domTree.Entry == "" && nodeCount > 0 {
		return nil, fmt.Errorf("domTree has no entry point for non-empty graph")
	}

	// Track start time for duration logging
	startTime := time.Now()

	// Start OTel span
	ctx, span := reducibilityTracer.Start(ctx, "CheckReducibility",
		trace.WithAttributes(
			attribute.String("entry", domTree.Entry),
			attribute.Int("node_count", nodeCount),
			attribute.Int("edge_count", edgeCount),
		),
	)
	defer span.End()

	// Get logger with trace context
	logger := telemetry.LoggerWithTrace(ctx, slog.Default())

	logger.Debug("starting reducibility check",
		slog.Int("node_count", nodeCount),
		slog.Int("edge_count", edgeCount),
		slog.String("entry", domTree.Entry),
	)

	// Handle empty graph
	if nodeCount == 0 {
		result := &ReducibilityResult{
			IsReducible:        true,
			Score:              1.0,
			IrreducibleRegions: []*IrreducibleRegion{},
			Summary: ReducibilitySummary{
				TotalNodes:             0,
				IrreducibleNodeCount:   0,
				IrreducibleRegionCount: 0,
				WellStructuredPercent:  100.0,
			},
			Recommendation: "Empty graph is trivially reducible.",
			DomTree:        domTree,
			NodeCount:      0,
			EdgeCount:      0,
			CrossEdgeCount: 0,
		}

		span.SetAttributes(
			attribute.Bool("is_reducible", true),
			attribute.Float64("score", 1.0),
			attribute.Int("cross_edges", 0),
		)

		return result, nil
	}

	// Phase 1: Edge classification - find all cross edges
	crossEdges := make([][2]string, 0, 50)
	edgesProcessed := 0

	for _, node := range a.graph.Nodes() {
		// Context check every N edges
		if edgesProcessed%reducibilityContextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("reducibility check cancelled during edge classification: %w", err)
			}
		}

		for _, edge := range node.Outgoing {
			edgesProcessed++
			edgeType := a.classifyReducibilityEdge(node.ID, edge.ToID, domTree)

			if edgeType == edgeTypeCross {
				crossEdges = append(crossEdges, [2]string{node.ID, edge.ToID})
			}
		}
	}

	// Handle fully reducible case (no cross edges)
	if len(crossEdges) == 0 {
		result := &ReducibilityResult{
			IsReducible:        true,
			Score:              1.0,
			IrreducibleRegions: []*IrreducibleRegion{},
			Summary: ReducibilitySummary{
				TotalNodes:             nodeCount,
				IrreducibleNodeCount:   0,
				IrreducibleRegionCount: 0,
				WellStructuredPercent:  100.0,
			},
			Recommendation: "Graph is fully reducible - well-structured control flow. No action needed.",
			DomTree:        domTree,
			NodeCount:      nodeCount,
			EdgeCount:      edgeCount,
			CrossEdgeCount: 0,
		}

		span.SetAttributes(
			attribute.Bool("is_reducible", true),
			attribute.Float64("score", 1.0),
			attribute.Int("cross_edges", 0),
			attribute.Int("edges_processed", edgesProcessed),
		)

		logger.Debug("reducibility check completed - fully reducible",
			slog.Bool("is_reducible", true),
			slog.Float64("score", 1.0),
			slog.Int("edges_processed", edgesProcessed),
			slog.Int64("duration_ms", time.Since(startTime).Milliseconds()),
		)

		return result, nil
	}

	// Log cross edges detection
	logger.Info("cross edges detected",
		slog.Int("count", len(crossEdges)),
		slog.Bool("is_reducible", false),
	)

	// Phase 2: Find irreducible regions from cross edges
	regions := a.findIrreducibleRegions(ctx, crossEdges, domTree, logger)

	// Phase 3: Compute statistics
	inIrreducible := make(map[string]bool)
	for _, region := range regions {
		for _, nodeID := range region.Nodes {
			inIrreducible[nodeID] = true
		}
	}

	irreducibleNodeCount := len(inIrreducible)
	score := 1.0
	if nodeCount > 0 {
		score = float64(nodeCount-irreducibleNodeCount) / float64(nodeCount)
		// Round to 4 decimal places
		score = math.Round(score*10000) / 10000
	}

	wellStructuredPercent := score * 100.0

	// Generate recommendation
	recommendation := generateReducibilityRecommendation(score, len(regions))

	result := &ReducibilityResult{
		IsReducible:        false,
		Score:              score,
		IrreducibleRegions: regions,
		Summary: ReducibilitySummary{
			TotalNodes:             nodeCount,
			IrreducibleNodeCount:   irreducibleNodeCount,
			IrreducibleRegionCount: len(regions),
			WellStructuredPercent:  wellStructuredPercent,
		},
		Recommendation: recommendation,
		DomTree:        domTree,
		NodeCount:      nodeCount,
		EdgeCount:      edgeCount,
		CrossEdgeCount: len(crossEdges),
	}

	// Set span attributes
	span.SetAttributes(
		attribute.Bool("is_reducible", false),
		attribute.Float64("score", score),
		attribute.Int("cross_edges", len(crossEdges)),
		attribute.Int("regions", len(regions)),
		attribute.Int("edges_processed", edgesProcessed),
	)

	span.AddEvent("irreducible_graph_detected",
		trace.WithAttributes(
			attribute.Int("region_count", len(regions)),
			attribute.Float64("well_structured_percent", wellStructuredPercent),
		),
	)

	logger.Debug("reducibility check completed",
		slog.Bool("is_reducible", false),
		slog.Float64("score", score),
		slog.Int("cross_edges", len(crossEdges)),
		slog.Int("regions", len(regions)),
		slog.Int("edges_processed", edgesProcessed),
		slog.Int64("duration_ms", time.Since(startTime).Milliseconds()),
	)

	return result, nil
}

// classifyReducibilityEdge determines the type of edge relative to domTree.
//
// Uses depth comparison as a fast path when depths are equal.
func (a *GraphAnalytics) classifyReducibilityEdge(from, to string, domTree *DominatorTree) reducibilityEdgeType {
	// Self-loop: A dominates itself, so it's a back edge
	if from == to {
		return edgeTypeBack
	}

	// Fast path: if neither node is in the dominator tree (unreachable),
	// we can't classify the edge
	fromDepth, fromHasDepth := domTree.Depth[from]
	toDepth, toHasDepth := domTree.Depth[to]

	if !fromHasDepth || !toHasDepth {
		// One or both nodes are unreachable from entry
		// Treat as cross edge (doesn't affect reducibility of reachable subgraph)
		return edgeTypeCross
	}

	// Fast path: if depths are equal and nodes differ, neither dominates
	if fromDepth == toDepth {
		return edgeTypeCross
	}

	// Back edge: target dominates source (target is ancestor)
	if domTree.Dominates(to, from) {
		return edgeTypeBack
	}

	// Forward/tree edge: source dominates target (target is descendant)
	if domTree.Dominates(from, to) {
		return edgeTypeForward
	}

	// Cross edge: neither dominates the other
	return edgeTypeCross
}

// findIrreducibleRegions identifies all irreducible regions from cross edges.
func (a *GraphAnalytics) findIrreducibleRegions(
	ctx context.Context,
	crossEdges [][2]string,
	domTree *DominatorTree,
	logger *slog.Logger,
) []*IrreducibleRegion {
	regions := make([]*IrreducibleRegion, 0, 10)
	visitedNodes := make(map[string]bool) // Track nodes already in a region

	for i, crossEdge := range crossEdges {
		// Check context
		if ctx.Err() != nil {
			break
		}

		// Cap regions
		if len(regions) >= maxIrreducibleRegions {
			logger.Debug("maximum irreducible regions reached",
				slog.Int("max", maxIrreducibleRegions),
				slog.Int("remaining_cross_edges", len(crossEdges)-i),
			)
			break
		}

		from, to := crossEdge[0], crossEdge[1]

		// Skip if both nodes are already in a region (avoid duplicates)
		if visitedNodes[from] && visitedNodes[to] {
			continue
		}

		// Find the region created by this cross edge
		region := a.findSingleIrreducibleRegion(crossEdge, domTree, visitedNodes)
		if region != nil && region.Size > 0 {
			region.ID = len(regions)
			regions = append(regions, region)

			// Mark all nodes in this region as visited
			for _, nodeID := range region.Nodes {
				visitedNodes[nodeID] = true
			}

			logger.Debug("irreducible region detected",
				slog.Int("region_id", region.ID),
				slog.Int("size", region.Size),
				slog.Int("entry_count", len(region.EntryNodes)),
				slog.String("reason", region.Reason),
			)
		}
	}

	return regions
}

// findSingleIrreducibleRegion finds the irreducible region created by a cross edge.
func (a *GraphAnalytics) findSingleIrreducibleRegion(
	crossEdge [2]string,
	domTree *DominatorTree,
	alreadyVisited map[string]bool,
) *IrreducibleRegion {
	from, to := crossEdge[0], crossEdge[1]

	// Find the lowest common dominator of the cross edge endpoints
	lcd := domTree.LowestCommonDominator(from, to)
	if lcd == "" {
		// No common dominator - shouldn't happen for reachable nodes
		return nil
	}

	// BFS to find all nodes in the region between LCD and endpoints
	visited := make(map[string]bool)
	queue := []string{from, to}
	var nodes []string

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		if visited[node] || alreadyVisited[node] {
			continue
		}
		visited[node] = true
		nodes = append(nodes, node)

		// Stop at LCD (it's the boundary)
		if node == lcd {
			continue
		}

		// Walk up the dominator tree to LCD
		idom := domTree.ImmediateDom[node]
		if idom != "" && !visited[idom] && domTree.Dominates(lcd, idom) {
			queue = append(queue, idom)
		}

		// Also include siblings in the dominator tree that are part of the cycle
		// (nodes that share the same immediate dominator)
		if idom != "" {
			for _, sibling := range domTree.Children[idom] {
				if !visited[sibling] && !alreadyVisited[sibling] {
					// Check if sibling is connected to the region
					if a.isConnectedToRegion(sibling, visited) {
						queue = append(queue, sibling)
					}
				}
			}
		}
	}

	if len(nodes) == 0 {
		return nil
	}

	// Find entry nodes - nodes that have predecessors outside the region
	entryNodes := a.findRegionEntryNodes(nodes, visited)

	// Build reason
	reason := fmt.Sprintf("Multiple entries via cross edge %s → %s; entries: %v", from, to, entryNodes)

	return &IrreducibleRegion{
		EntryNodes: entryNodes,
		Nodes:      nodes,
		Size:       len(nodes),
		CrossEdges: [][2]string{crossEdge},
		Reason:     reason,
	}
}

// isConnectedToRegion checks if a node has edges to nodes in the region.
func (a *GraphAnalytics) isConnectedToRegion(nodeID string, regionNodes map[string]bool) bool {
	node, ok := a.graph.GetNode(nodeID)
	if !ok {
		return false
	}

	// Check outgoing edges
	for _, edge := range node.Outgoing {
		if regionNodes[edge.ToID] {
			return true
		}
	}

	// Check incoming edges
	for _, edge := range node.Incoming {
		if regionNodes[edge.FromID] {
			return true
		}
	}

	return false
}

// findRegionEntryNodes finds nodes that have predecessors outside the region.
func (a *GraphAnalytics) findRegionEntryNodes(nodes []string, inRegion map[string]bool) []string {
	entries := make([]string, 0, 2)
	seen := make(map[string]bool)

	for _, nodeID := range nodes {
		if seen[nodeID] {
			continue
		}

		node, ok := a.graph.GetNode(nodeID)
		if !ok {
			continue
		}

		// Check if any predecessor is outside the region
		for _, edge := range node.Incoming {
			if !inRegion[edge.FromID] {
				entries = append(entries, nodeID)
				seen[nodeID] = true
				break
			}
		}
	}

	return entries
}

// generateReducibilityRecommendation creates actionable guidance based on score.
func generateReducibilityRecommendation(score float64, regionCount int) string {
	switch {
	case score >= 0.99:
		return "Graph is fully reducible - well-structured control flow. No action needed."
	case score >= 0.95:
		return fmt.Sprintf("Mostly well-structured. Consider refactoring %d minor irreducible region(s) for better maintainability.", regionCount)
	case score >= 0.80:
		return fmt.Sprintf("Several irreducible regions detected (%d). Review control flow for potential simplification.", regionCount)
	default:
		return fmt.Sprintf("Significant irreducible regions (%d). Consider restructuring control flow for clarity and maintainability.", regionCount)
	}
}

// CheckReducibilityWithCRS wraps CheckReducibility with CRS tracing.
//
// Description:
//
//	Provides the same functionality as CheckReducibility but also returns
//	a TraceStep for recording in the Code Reasoning State (CRS).
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) CheckReducibilityWithCRS(
	ctx context.Context,
	domTree *DominatorTree,
) (*ReducibilityResult, crs.TraceStep) {
	start := time.Now()

	// Handle nil domTree case
	if domTree == nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_reducibility").
			WithTarget("graph").
			WithTool("CheckReducibility").
			WithDuration(time.Since(start)).
			WithError("dominator tree must not be nil").
			Build()
		return &ReducibilityResult{
			IsReducible:        true,
			Score:              1.0,
			IrreducibleRegions: make([]*IrreducibleRegion, 0),
			Summary: ReducibilitySummary{
				WellStructuredPercent: 100.0,
			},
			Recommendation: "Unable to analyze - no dominator tree provided",
		}, step
	}

	// Handle nil/cancelled context
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_reducibility").
			WithTarget(domTree.Entry).
			WithTool("CheckReducibility").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return &ReducibilityResult{
			IsReducible:        true,
			Score:              1.0,
			IrreducibleRegions: make([]*IrreducibleRegion, 0),
			DomTree:            domTree,
			Recommendation:     "Analysis cancelled",
		}, step
	}

	result, err := a.CheckReducibility(ctx, domTree)

	// Build TraceStep
	builder := crs.NewTraceStepBuilder().
		WithAction("analytics_reducibility").
		WithTarget("graph").
		WithTool("CheckReducibility").
		WithDuration(time.Since(start))

	if err != nil {
		builder = builder.WithError(err.Error())
	} else if result != nil {
		builder = builder.
			WithMetadata("is_reducible", fmt.Sprintf("%t", result.IsReducible)).
			WithMetadata("score", fmt.Sprintf("%.4f", result.Score)).
			WithMetadata("irreducible_regions", itoa(len(result.IrreducibleRegions))).
			WithMetadata("cross_edge_count", itoa(result.CrossEdgeCount)).
			WithMetadata("node_count", itoa(result.NodeCount)).
			WithMetadata("edge_count", itoa(result.EdgeCount)).
			WithMetadata("entry", domTree.Entry)
	}

	// Handle nil result case
	if result == nil {
		result = &ReducibilityResult{
			IsReducible:        true,
			Score:              1.0,
			IrreducibleRegions: make([]*IrreducibleRegion, 0),
			DomTree:            domTree,
			Recommendation:     "Analysis failed",
		}
	}

	return result, builder.Build()
}

// IsReducible provides a fast check for reducibility without region enumeration.
//
// Description:
//
//	Returns true if the graph is reducible, false otherwise.
//	This is faster than CheckReducibility when you only need the boolean result
//	and don't need the detailed region information.
//
// Complexity:
//
//   - Time: O(E × depth) worst case, but returns early on first cross edge
//   - Space: O(1)
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) IsReducible(ctx context.Context, domTree *DominatorTree) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("ctx must not be nil")
	}
	if domTree == nil {
		return false, fmt.Errorf("domTree must not be nil")
	}

	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("reducibility check cancelled: %w", err)
	}

	edgesProcessed := 0

	for _, node := range a.graph.Nodes() {
		if edgesProcessed%reducibilityContextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return false, fmt.Errorf("reducibility check cancelled: %w", err)
			}
		}

		for _, edge := range node.Outgoing {
			edgesProcessed++
			edgeType := a.classifyReducibilityEdge(node.ID, edge.ToID, domTree)

			if edgeType == edgeTypeCross {
				return false, nil // Found cross edge → not reducible
			}
		}
	}

	return true, nil
}
