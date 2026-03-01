// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_weighted_criticality Tool (GR-18c) - Typed Implementation
// =============================================================================

var findWeightedCriticalityTracer = otel.Tracer("tools.find_weighted_criticality")

// FindWeightedCriticalityParams contains the validated input parameters.
type FindWeightedCriticalityParams struct {
	// Top is the number of critical functions to return.
	// Default: 20, Range: [1, 100].
	Top int

	// Entry is the entry point for dominator analysis.
	// Empty string triggers auto-detection.
	Entry string

	// ShowQuadrant enables quadrant classification in output.
	ShowQuadrant bool
}

// ToolName returns the tool name for TypedParams interface.
func (p FindWeightedCriticalityParams) ToolName() string { return "find_weighted_criticality" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindWeightedCriticalityParams) ToMap() map[string]any {
	m := map[string]any{
		"top":           p.Top,
		"show_quadrant": p.ShowQuadrant,
	}
	if p.Entry != "" {
		m["entry"] = p.Entry
	}
	return m
}

// WeightedCriticalityOutput contains the structured result.
type WeightedCriticalityOutput struct {
	CriticalFunctions []CriticalFunction `json:"critical_functions"`
	QuadrantSummary   map[string]int     `json:"quadrant_summary"`
	Summary           CriticalitySummary `json:"summary"`
}

// CriticalFunction represents one critical function with combined scores.
type CriticalFunction struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	File             string  `json:"file"`
	Line             int     `json:"line"`
	CriticalityScore float64 `json:"criticality_score"`
	DominatorScore   float64 `json:"dominator_score"`
	PageRankScore    float64 `json:"pagerank_score"`
	DominatedCount   int     `json:"dominated_count"`
	Quadrant         string  `json:"quadrant"`
	RiskLevel        string  `json:"risk_level"`
	Recommendation   string  `json:"recommendation"`
}

// CriticalitySummary contains aggregate statistics.
type CriticalitySummary struct {
	TotalAnalyzed   int     `json:"total_analyzed"`
	HighRiskCount   int     `json:"high_risk_count"`
	MediumRiskCount int     `json:"medium_risk_count"`
	AvgCriticality  float64 `json:"avg_criticality"`
}

// findWeightedCriticalityTool finds critical functions by combining dominance with PageRank.
type findWeightedCriticalityTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindWeightedCriticalityTool creates a new find_weighted_criticality tool.
//
// Description:
//
//	Creates a tool for finding the most critical functions by combining dominator
//	analysis with PageRank. A function scores high if it's both a mandatory
//	dependency (high dominator score) AND architecturally important (high PageRank).
//	Criticality = normalize(DominatorScore) × normalize(PageRank).
//
// Inputs:
//   - analytics: GraphAnalytics instance for dominators and PageRank. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_weighted_criticality tool.
//
// Thread Safety: Safe for concurrent use after construction.
//
// Limitations:
//   - Requires both dominator tree and PageRank computation (can be expensive)
//   - Results are sensitive to entry point selection for dominators
//   - Quadrant classification uses fixed threshold (0.5) for high/low
//
// Assumptions:
//   - Graph is frozen and immutable
//   - PageRank converges (or uses last iteration on timeout)
//   - Dominator tree exists (entry point is reachable)
func NewFindWeightedCriticalityTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findWeightedCriticalityTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findWeightedCriticalityTool) Name() string {
	return "find_weighted_criticality"
}

func (t *findWeightedCriticalityTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findWeightedCriticalityTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_weighted_criticality",
		Description: "Find the most critical functions by combining dominance with importance. " +
			"A function scores high if it's both a mandatory dependency AND highly connected. " +
			"Criticality = DominatorScore × PageRank",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of critical functions to return (default: 20, max: 100)",
				Required:    false,
			},
			"entry": {
				Type:        ParamTypeString,
				Description: "Entry point for dominator analysis (default: auto-detect)",
				Required:    false,
			},
			"show_quadrant": {
				Type:        ParamTypeBool,
				Description: "Show quadrant classification (default: true)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    88,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     45 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"critical functions", "most important", "highest risk",
				"combined importance", "weighted criticality",
				"risk assessment", "key functions",
			},
			UseWhen: "User asks about the most critical or risky functions " +
				"considering both structural importance and dependency relationships.",
			AvoidWhen: "User asks about the most connected, highest-coupling, highest-degree, " +
				"or hub functions based on connectivity (use find_hotspots for degree-based ranking). " +
				"User asks about fan-in or fan-out counts (use find_hotspots). " +
				"User asks only about dominators or only about PageRank " +
				"(use find_dominators or find_important for single-dimension analysis).",
		},
	}
}

// Execute runs the find_weighted_criticality tool.
func (t *findWeightedCriticalityTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Check for nil analytics
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Start span with context
	ctx, span := findWeightedCriticalityTracer.Start(ctx, "findWeightedCriticalityTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_weighted_criticality"),
			attribute.String("entry", p.Entry),
			attribute.Int("top", p.Top),
			attribute.Bool("show_quadrant", p.ShowQuadrant),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Auto-detect entry if not provided
	if p.Entry == "" {
		p.Entry = t.detectEntry()
		span.SetAttributes(attribute.String("entry_detected", p.Entry))
	}

	// Step 1: Compute dominator tree
	t.logger.Debug("computing dominator tree",
		slog.String("tool", "find_weighted_criticality"),
		slog.String("entry", p.Entry),
	)

	domTree, err := t.analytics.Dominators(ctx, p.Entry)
	if err != nil || domTree == nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "dominator computation failed")
		span.AddEvent("dominator_computation_failed", trace.WithAttributes(
			attribute.String("error", fmt.Sprintf("%v", err)),
		))
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to compute dominator tree: %v", err),
		}, nil
	}

	span.AddEvent("dominator_tree_computed", trace.WithAttributes(
		attribute.Int("node_count", len(domTree.ImmediateDom)),
	))

	// Step 2: Compute PageRank
	t.logger.Debug("computing PageRank",
		slog.String("tool", "find_weighted_criticality"),
	)

	prOpts := graph.DefaultPageRankOptions()
	prResult := t.analytics.PageRank(ctx, prOpts)
	if prResult == nil {
		err := fmt.Errorf("PageRank returned nil result")
		span.RecordError(err)
		span.SetStatus(codes.Error, "PageRank computation failed")
		span.AddEvent("pagerank_computation_failed", trace.WithAttributes(
			attribute.String("error", "nil result"),
		))
		return &Result{
			Success: false,
			Error:   "failed to compute PageRank: nil result",
		}, nil
	}

	// Warn if PageRank didn't converge
	if !prResult.Converged {
		t.logger.Warn("PageRank did not converge",
			slog.Int("iterations", prResult.Iterations),
			slog.Float64("final_delta", prResult.MaxDiff),
		)
		span.AddEvent("pagerank_convergence_warning", trace.WithAttributes(
			attribute.Int("iterations", prResult.Iterations),
			attribute.Float64("final_delta", prResult.MaxDiff),
		))
	}

	span.AddEvent("pagerank_computed", trace.WithAttributes(
		attribute.Int("node_count", len(prResult.Scores)),
		attribute.Bool("converged", prResult.Converged),
	))

	// Step 3: Compute dominator scores (optimized)
	dominatorScores := t.computeDominatorScores(domTree)

	// Step 4: Normalize both scores to [0, 1]
	normDom := normalize(dominatorScores)
	normPR := normalize(prResult.Scores)

	// Step 5: Compute criticality = domScore × pageRank
	criticality := make(map[string]float64, len(normDom))
	for nodeID := range normDom {
		domScore := normDom[nodeID]
		prScore := normPR[nodeID]
		criticality[nodeID] = domScore * prScore
	}

	span.AddEvent("scores_computed", trace.WithAttributes(
		attribute.Int("node_count", len(criticality)),
	))

	// Step 6: Build results and classify
	functions := make([]CriticalFunction, 0, len(criticality))
	quadrantCounts := map[string]int{
		"CRITICAL":          0,
		"HIDDEN_GATEKEEPER": 0,
		"HUB":               0,
		"LEAF":              0,
	}

	for nodeID, critScore := range criticality {
		domScore := normDom[nodeID]
		prScore := normPR[nodeID]
		rawDomScore := dominatorScores[nodeID]

		// Classify quadrant
		quadrant := classifyQuadrant(domScore, prScore)
		quadrantCounts[quadrant]++

		// Assign risk level
		riskLevel := assignRiskLevel(critScore)

		// Generate recommendation
		recommendation := generateRecommendation(quadrant, riskLevel)

		// Extract metadata from node ID
		name := extractNameFromNodeID(nodeID)
		file := extractFileFromNodeID(nodeID)
		line := extractLineFromNodeID(nodeID)

		functions = append(functions, CriticalFunction{
			ID:               nodeID,
			Name:             name,
			File:             file,
			Line:             line,
			CriticalityScore: critScore,
			DominatorScore:   domScore,
			PageRankScore:    prScore,
			DominatedCount:   int(rawDomScore),
			Quadrant:         quadrant,
			RiskLevel:        riskLevel,
			Recommendation:   recommendation,
		})
	}

	// Step 7: Sort by criticality (deterministic with tertiary sort)
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].CriticalityScore != functions[j].CriticalityScore {
			return functions[i].CriticalityScore > functions[j].CriticalityScore
		}
		if functions[i].DominatedCount != functions[j].DominatedCount {
			return functions[i].DominatedCount > functions[j].DominatedCount
		}
		// Tertiary sort by name for determinism (enables CRS caching)
		return functions[i].Name < functions[j].Name
	})

	// Step 8: Select top N
	if len(functions) > p.Top {
		functions = functions[:p.Top]
	}

	// Step 9: Compute summary statistics
	highRiskCount := 0
	mediumRiskCount := 0
	totalCriticality := 0.0

	for _, fn := range functions {
		totalCriticality += fn.CriticalityScore
		if fn.RiskLevel == "high" {
			highRiskCount++
		} else if fn.RiskLevel == "medium" {
			mediumRiskCount++
		}
	}

	avgCriticality := 0.0
	if len(functions) > 0 {
		avgCriticality = totalCriticality / float64(len(functions))
	}

	summary := CriticalitySummary{
		TotalAnalyzed:   len(criticality),
		HighRiskCount:   highRiskCount,
		MediumRiskCount: mediumRiskCount,
		AvgCriticality:  avgCriticality,
	}

	output := WeightedCriticalityOutput{
		CriticalFunctions: functions,
		QuadrantSummary:   quadrantCounts,
		Summary:           summary,
	}

	// Format text output
	outputText := t.formatText(functions, quadrantCounts, summary, p.ShowQuadrant)

	span.SetAttributes(
		attribute.Int("results_count", len(functions)),
		attribute.Int("high_risk_count", highRiskCount),
		attribute.Int("critical_quadrant", quadrantCounts["CRITICAL"]),
	)

	t.logger.Info("weighted criticality completed",
		slog.String("tool", "find_weighted_criticality"),
		slog.Int("nodes_analyzed", len(criticality)),
		slog.Int("high_risk_count", highRiskCount),
		slog.Int("critical_quadrant", quadrantCounts["CRITICAL"]),
		slog.Duration("duration", time.Since(start)),
	)

	// Build comprehensive TraceStep
	finalTrace := crs.NewTraceStepBuilder().
		WithAction("tool_weighted_criticality").
		WithTool("find_weighted_criticality").
		WithTarget(p.Entry).
		WithDuration(time.Since(start)).
		WithMetadata("top", strconv.Itoa(p.Top)).
		WithMetadata("show_quadrant", strconv.FormatBool(p.ShowQuadrant)).
		WithMetadata("nodes_analyzed", strconv.Itoa(len(criticality))).
		WithMetadata("high_risk_count", strconv.Itoa(highRiskCount)).
		WithMetadata("quadrant_critical", strconv.Itoa(quadrantCounts["CRITICAL"])).
		WithMetadata("quadrant_hidden", strconv.Itoa(quadrantCounts["HIDDEN_GATEKEEPER"])).
		WithMetadata("quadrant_hub", strconv.Itoa(quadrantCounts["HUB"])).
		WithMetadata("quadrant_leaf", strconv.Itoa(quadrantCounts["LEAF"])).
		Build()

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &finalTrace,
		Duration:   time.Since(start),
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findWeightedCriticalityTool) parseParams(params map[string]any) (FindWeightedCriticalityParams, error) {
	p := FindWeightedCriticalityParams{
		Top:          20,   // Default
		Entry:        "",   // Auto-detect
		ShowQuadrant: true, // Default
	}

	// Extract and validate top
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			// Clamp to [1, 100]
			if top < 1 {
				top = 1
			}
			if top > 100 {
				top = 100
			}
			p.Top = top
		}
	}

	// Extract entry
	if entryRaw, ok := params["entry"].(string); ok {
		p.Entry = entryRaw
	}

	// Extract show_quadrant
	if showRaw, ok := params["show_quadrant"].(bool); ok {
		p.ShowQuadrant = showRaw
	}

	return p, nil
}

// detectEntry attempts to find a reasonable entry point for the graph.
func (t *findWeightedCriticalityTool) detectEntry() string {
	// Try common entry point names
	candidates := []string{
		"main",
		"Main",
		"init",
		"start",
		"run",
	}

	for _, name := range candidates {
		// Use index to find candidates
		if t.index != nil {
			symbols := t.index.GetByName(name)
			if len(symbols) > 0 {
				return symbols[0].ID
			}
		}
	}

	// Fallback: return empty (will error in Dominators)
	return ""
}

// computeDominatorScores computes dominator scores for all nodes.
//
// Description:
//
//	Counts the number of nodes dominated by each node in the dominator tree.
//	A node's score equals the size of its dominator subtree (including itself).
//	Optimized implementation using single traversal - O(V) instead of O(V × subtree).
//
// Inputs:
//   - domTree: Dominator tree from graph.Dominators(). Must not be nil.
//
// Outputs:
//   - map[string]float64: Node ID -> count of dominated nodes. Never nil.
//
// Thread Safety: Safe for concurrent use (read-only operations).
//
// Performance: O(V) - single traversal with memoization.
//
// Example:
//
//	scores := t.computeDominatorScores(domTree)
//	fmt.Printf("Main dominates %d nodes\n", scores["main"])
//
// Assumptions:
//   - domTree is non-nil and valid
//   - Entry node exists in tree
func (t *findWeightedCriticalityTool) computeDominatorScores(domTree *graph.DominatorTree) map[string]float64 {
	nodeCount := len(domTree.ImmediateDom)
	scores := make(map[string]float64, nodeCount)
	visited := make(map[string]bool, nodeCount)

	// Build children map for efficient traversal
	children := make(map[string][]string)
	for node, idom := range domTree.ImmediateDom {
		if idom != "" && node != idom {
			children[idom] = append(children[idom], node)
		}
	}

	// Recursive function to count descendants
	var countDescendants func(string) int
	countDescendants = func(node string) int {
		if visited[node] {
			return int(scores[node])
		}
		visited[node] = true

		count := 1 // Count self
		for _, child := range children[node] {
			count += countDescendants(child)
		}

		scores[node] = float64(count)
		return count
	}

	// Start from root
	if domTree.Entry != "" {
		countDescendants(domTree.Entry)
	}

	// Handle nodes not reachable from entry
	for node := range domTree.ImmediateDom {
		if !visited[node] {
			countDescendants(node)
		}
	}

	return scores
}

// normalize normalizes scores to [0, 1] range using min-max scaling.
//
// Description:
//
//	Performs min-max normalization: normalized = (value - min) / (max - min).
//	Handles edge cases: empty map, single value, zero range, NaN/Inf values.
//
// Inputs:
//   - scores: Map of node ID -> raw score. Can be empty.
//
// Outputs:
//   - map[string]float64: Node ID -> normalized score [0.0, 1.0]. Never nil.
//
// Thread Safety: Safe for concurrent use (creates new map).
//
// Edge Cases:
//   - Empty map: Returns empty map
//   - Single node: Returns {node: 1.0}
//   - All same score: Returns {node: 0.5} for all
//   - Zero range: Returns {node: 0.5} for all
//   - NaN/Inf: Filtered and assigned 0.0
//
// Example:
//
//	scores := map[string]float64{"A": 10, "B": 20, "C": 30}
//	norm := normalize(scores)
//	// Result: {"A": 0.0, "B": 0.5, "C": 1.0}
//
// Assumptions:
//   - Input scores are non-negative (or handled by caller)
func normalize(scores map[string]float64) map[string]float64 {
	if len(scores) == 0 {
		return make(map[string]float64)
	}

	if len(scores) == 1 {
		// Single node: assign 1.0 (most critical by definition)
		result := make(map[string]float64, 1)
		for k := range scores {
			result[k] = 1.0
		}
		return result
	}

	// Find min/max, filtering NaN/Inf
	minVal, maxVal := math.MaxFloat64, -math.MaxFloat64
	validCount := 0
	for _, v := range scores {
		// Filter NaN/Inf
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
		validCount++
	}

	// All invalid values
	if validCount == 0 {
		result := make(map[string]float64, len(scores))
		for k := range scores {
			result[k] = 0.0
		}
		return result
	}

	range_ := maxVal - minVal
	if range_ == 0 || range_ < 1e-10 {
		// All same score (or very close)
		result := make(map[string]float64, len(scores))
		for k := range scores {
			result[k] = 0.5 // Neutral
		}
		return result
	}

	// Normalize to [0, 1]
	result := make(map[string]float64, len(scores))
	for k, v := range scores {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			result[k] = 0.0
		} else {
			result[k] = (v - minVal) / range_
		}
	}
	return result
}

// classifyQuadrant classifies a function into one of four quadrants.
//
// Description:
//
//	Classifies based on normalized dominator and PageRank scores using 0.5 threshold.
//	Quadrants: CRITICAL (high/high), HIDDEN_GATEKEEPER (high/low),
//	HUB (low/high), LEAF (low/low).
//
// Inputs:
//   - domScore: Normalized dominator score [0.0, 1.0].
//   - prScore: Normalized PageRank score [0.0, 1.0].
//
// Outputs:
//   - string: Quadrant name (CRITICAL, HIDDEN_GATEKEEPER, HUB, LEAF).
//
// Thread Safety: Safe for concurrent use (pure function).
//
// Example:
//
//	q := classifyQuadrant(0.8, 0.9)  // Returns "CRITICAL"
//	q := classifyQuadrant(0.3, 0.7)  // Returns "HUB"
//
// Assumptions:
//   - Scores are normalized to [0, 1]
//   - Threshold is fixed at 0.5
func classifyQuadrant(domScore, prScore float64) string {
	// Threshold of 0.5 divides the [0,1] normalized score space into balanced quadrants.
	// Rationale: 0.5 represents the median - functions above this threshold dominate/influence
	// more than half the codebase. This creates four equal-area quadrants for balanced classification.
	// Alternative thresholds like 0.6 or 0.7 would skew the distribution and miss mid-range functions.
	highDom := domScore >= 0.5
	highPR := prScore >= 0.5

	switch {
	case highDom && highPR:
		return "CRITICAL" // Maximum risk: both mandatory AND central
	case highDom && !highPR:
		return "HIDDEN_GATEKEEPER" // Mandatory but not central: silent bottleneck
	case !highDom && highPR:
		return "HUB" // Central but bypassable: architectural importance
	default:
		return "LEAF" // Low risk: peripheral function
	}
}

// assignRiskLevel assigns a risk level based on criticality score.
//
// Description:
//
//	Maps criticality score to risk level: high (>= 0.7), medium (>= 0.4),
//	low (>= 0.2), minimal (< 0.2).
//
// Inputs:
//   - criticality: Criticality score [0.0, 1.0].
//
// Outputs:
//   - string: Risk level (high, medium, low, minimal).
//
// Thread Safety: Safe for concurrent use (pure function).
//
// Example:
//
//	risk := assignRiskLevel(0.85)  // Returns "high"
//	risk := assignRiskLevel(0.15)  // Returns "minimal"
//
// Assumptions:
//   - Criticality is in [0, 1] range
func assignRiskLevel(criticality float64) string {
	switch {
	case criticality >= 0.7:
		return "high"
	case criticality >= 0.4:
		return "medium"
	case criticality >= 0.2:
		return "low"
	default:
		return "minimal"
	}
}

// generateRecommendation generates a recommendation based on quadrant and risk.
//
// Description:
//
//	Provides actionable recommendations based on the function's quadrant
//	classification. Recommendations are tailored to the specific risk profile.
//
// Inputs:
//   - quadrant: Quadrant classification (CRITICAL, HIDDEN_GATEKEEPER, HUB, LEAF).
//   - riskLevel: Risk level (high, medium, low, minimal).
//
// Outputs:
//   - string: Human-readable recommendation.
//
// Thread Safety: Safe for concurrent use (pure function).
//
// Example:
//
//	rec := generateRecommendation("CRITICAL", "high")
//	// Returns "High-impact function. Ensure comprehensive testing and monitoring."
//
// Assumptions:
//   - Quadrant is one of the four valid values
//   - Risk level is one of the four valid values
func generateRecommendation(quadrant, riskLevel string) string {
	switch quadrant {
	case "CRITICAL":
		return "High-impact function. Ensure comprehensive testing and monitoring."
	case "HIDDEN_GATEKEEPER":
		return "Gateway function with moderate connectivity. Review for bottlenecks."
	case "HUB":
		return "Central but bypassable. Consider for code review focus."
	default:
		return "Low-risk function. Standard maintenance."
	}
}

// formatText creates a human-readable text summary.
func (t *findWeightedCriticalityTool) formatText(
	functions []CriticalFunction,
	quadrantCounts map[string]int,
	summary CriticalitySummary,
	showQuadrant bool,
) string {
	// Pre-size buffer for efficiency
	estimatedSize := 300 + len(functions)*200
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString("Weighted Criticality Analysis\n\n")

	if len(functions) == 0 {
		sb.WriteString("No critical functions found.\n")
		return sb.String()
	}

	// Summary
	sb.WriteString(fmt.Sprintf("Analyzed %d functions\n", summary.TotalAnalyzed))
	sb.WriteString(fmt.Sprintf("High-risk: %d | Medium-risk: %d | Avg criticality: %.2f\n\n",
		summary.HighRiskCount, summary.MediumRiskCount, summary.AvgCriticality))

	if showQuadrant {
		sb.WriteString("Quadrant Distribution:\n")
		sb.WriteString(fmt.Sprintf("  CRITICAL: %d\n", quadrantCounts["CRITICAL"]))
		sb.WriteString(fmt.Sprintf("  HIDDEN_GATEKEEPER: %d\n", quadrantCounts["HIDDEN_GATEKEEPER"]))
		sb.WriteString(fmt.Sprintf("  HUB: %d\n", quadrantCounts["HUB"]))
		sb.WriteString(fmt.Sprintf("  LEAF: %d\n\n", quadrantCounts["LEAF"]))
	}

	// Top functions
	sb.WriteString("Critical Functions:\n")
	for i, fn := range functions {
		sb.WriteString(fmt.Sprintf("\n%d. %s (%s:%d)\n", i+1, fn.Name, fn.File, fn.Line))
		sb.WriteString(fmt.Sprintf("   Criticality: %.2f | Risk: %s", fn.CriticalityScore, fn.RiskLevel))
		if showQuadrant {
			sb.WriteString(fmt.Sprintf(" | Quadrant: %s", fn.Quadrant))
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("   Dominator: %.2f (%d nodes) | PageRank: %.2f\n",
			fn.DominatorScore, fn.DominatedCount, fn.PageRankScore))
		sb.WriteString(fmt.Sprintf("   → %s\n", fn.Recommendation))
	}

	return sb.String()
}
