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
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// check_reducibility Tool (GR-17h) - Typed Implementation
// =============================================================================

var checkReducibilityTracer = otel.Tracer("tools.check_reducibility")

// CheckReducibilityParams contains the validated input parameters.
type CheckReducibilityParams struct {
	// ShowIrreducible indicates whether to list specific irreducible regions.
	// Default: true
	ShowIrreducible bool
}

// CheckReducibilityOutput contains the structured result.
type CheckReducibilityOutput struct {
	// IsReducible is true if the entire graph is reducible.
	IsReducible bool `json:"is_reducible"`

	// Score is the percentage of nodes NOT in irreducible regions (0.0 to 1.0).
	Score float64 `json:"score"`

	// QualityLabel is a human-readable quality label.
	QualityLabel string `json:"quality_label"`

	// IrreducibleRegions contains detected irreducible subgraphs.
	IrreducibleRegions []IrreducibleRegionInfo `json:"irreducible_regions,omitempty"`

	// Summary contains aggregate statistics.
	Summary CheckReducibilitySummary `json:"summary"`

	// Recommendation provides actionable guidance.
	Recommendation string `json:"recommendation"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// IrreducibleRegionInfo holds information about an irreducible region.
type IrreducibleRegionInfo struct {
	// ID is the region identifier.
	ID int `json:"id"`

	// EntryNodes are the multiple entry points.
	EntryNodes []string `json:"entry_nodes"`

	// Size is the number of nodes in the region.
	Size int `json:"size"`

	// CrossEdgeCount is the number of cross edges creating this region.
	CrossEdgeCount int `json:"cross_edge_count"`

	// Reason explains why this region is irreducible.
	Reason string `json:"reason,omitempty"`
}

// CheckReducibilitySummary contains aggregate statistics.
type CheckReducibilitySummary struct {
	// TotalNodes is the total nodes analyzed.
	TotalNodes int `json:"total_nodes"`

	// TotalEdges is the total edges analyzed.
	TotalEdges int `json:"total_edges"`

	// IrreducibleNodeCount is nodes in irreducible regions.
	IrreducibleNodeCount int `json:"irreducible_node_count"`

	// IrreducibleRegionCount is the number of irreducible regions.
	IrreducibleRegionCount int `json:"irreducible_region_count"`

	// CrossEdgeCount is the number of cross edges found.
	CrossEdgeCount int `json:"cross_edge_count"`

	// WellStructuredPercent is the percentage of well-structured code.
	WellStructuredPercent float64 `json:"well_structured_percent"`
}

// checkReducibilityTool analyzes graph reducibility for code quality.
type checkReducibilityTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewCheckReducibilityTool creates a new check_reducibility tool.
//
// Description:
//
//	Creates a tool for checking graph reducibility - whether the call graph
//	is well-structured with clean loop structures. Non-reducible regions
//	indicate complex or potentially problematic code.
//
// Inputs:
//   - analytics: GraphAnalytics instance for reducibility checking. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured check_reducibility tool.
//
// Limitations:
//   - Requires dominator tree computation
//   - Maximum 100 irreducible regions enumerated
//
// Thread Safety: Safe for concurrent use after construction.
func NewCheckReducibilityTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &checkReducibilityTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *checkReducibilityTool) Name() string {
	return "check_reducibility"
}

func (t *checkReducibilityTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *checkReducibilityTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "check_reducibility",
		Description: "Check if the call graph is reducible (well-structured). " +
			"Reducible graphs have clean loop structures without 'goto spaghetti'. " +
			"Non-reducible regions indicate complex or potentially problematic code.",
		Parameters: map[string]ParamDef{
			"show_irreducible": {
				Type:        ParamTypeBool,
				Description: "List specific irreducible regions (default: true)",
				Required:    false,
				Default:     true,
			},
		},
		Category:    CategoryExploration,
		Priority:    79,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"reducible", "well-structured", "code quality",
				"spaghetti code", "irreducible", "complex control flow",
				"goto", "structure", "clean code",
			},
			UseWhen: "User asks about code quality, control flow structure, " +
				"or wants to identify poorly structured code regions.",
			AvoidWhen: "User asks about cycles (use find_cycles) or " +
				"extractable regions (use find_extractable_regions).",
		},
	}
}

// Execute runs the check_reducibility tool.
func (t *checkReducibilityTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	// Parse and validate parameters
	p, err := t.parseParams(params)
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

	// GR-17b Fix: Check graph readiness with retry logic
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if t.analytics.IsGraphReady() {
			break
		}

		if attempt < maxRetries-1 {
			t.logger.Info("graph not ready, retrying after delay",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", maxRetries),
				slog.Duration("retry_delay", retryDelay),
			)
			time.Sleep(retryDelay)
		} else {
			return &Result{
				Success: false,
				Error: "graph not ready - indexing still in progress. " +
					"Please wait a few seconds for graph initialization to complete and try again.",
			}, nil
		}
	}

	// Continue with existing logic
	return t.executeOnce(ctx, p)
}

// executeOnce performs a single execution attempt (extracted for retry logic).
func (t *checkReducibilityTool) executeOnce(ctx context.Context, p CheckReducibilityParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := checkReducibilityTracer.Start(ctx, "checkReducibilityTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "check_reducibility"),
			attribute.Bool("show_irreducible", p.ShowIrreducible),
		),
	)
	defer span.End()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect entry point for dominator tree
	entry, err := DetectEntryPoint(ctx, t.index, t.analytics)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to detect entry point: %v", err),
		}, nil
	}

	span.SetAttributes(attribute.String("entry", entry))

	// Compute dominator tree
	domTree, err := t.analytics.Dominators(ctx, entry)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to compute dominators: %v", err),
		}, nil
	}

	// Check context again
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Check reducibility
	result, traceStep := t.analytics.CheckReducibilityWithCRS(ctx, domTree)
	if result == nil {
		outputText := "Reducibility Analysis:\n\nStatus: REDUCIBLE (Well-structured)\nScore: 100.0% (Excellent)\nGraph structure is well-formed.\n"
		return &Result{
			Success: true,
			Output: CheckReducibilityOutput{
				IsReducible:  true,
				Score:        1.0,
				QualityLabel: "Excellent",
				Summary: CheckReducibilitySummary{
					TotalNodes:            domTree.NodeCount,
					WellStructuredPercent: 100.0,
				},
				Recommendation: "Graph structure is well-formed.",
				Message:        "Reducibility check completed with no issues",
			},
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Build output
	output := t.buildOutput(result, p.ShowIrreducible)

	// Format text output
	outputText := t.formatText(result, output)

	span.SetAttributes(
		attribute.Bool("is_reducible", result.IsReducible),
		attribute.Float64("score", result.Score),
		attribute.Int("irreducible_regions", len(result.IrreducibleRegions)),
		attribute.String("trace_action", traceStep.Action),
	)

	t.logger.Debug("check_reducibility completed",
		slog.String("tool", "check_reducibility"),
		slog.Bool("is_reducible", result.IsReducible),
		slog.Float64("score", result.Score),
		slog.Int("irreducible_regions", len(result.IrreducibleRegions)),
	)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &traceStep,
		Duration:   time.Since(start),
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *checkReducibilityTool) parseParams(params map[string]any) (CheckReducibilityParams, error) {
	p := CheckReducibilityParams{
		ShowIrreducible: true,
	}

	// Extract show_irreducible (optional)
	if showRaw, ok := params["show_irreducible"]; ok {
		if show, ok := parseBoolParam(showRaw); ok {
			p.ShowIrreducible = show
		}
	}

	return p, nil
}

// getQualityLabel returns a human-readable quality label based on score.
func (t *checkReducibilityTool) getQualityLabel(score float64) string {
	switch {
	case score >= 0.99:
		return "Excellent"
	case score >= 0.95:
		return "Very Good"
	case score >= 0.90:
		return "Good"
	case score >= 0.80:
		return "Acceptable"
	case score >= 0.70:
		return "Fair"
	case score >= 0.50:
		return "Poor"
	default:
		return "Critical"
	}
}

// getRecommendation returns actionable guidance based on the result.
func (t *checkReducibilityTool) getRecommendation(result *graph.ReducibilityResult) string {
	if result.IsReducible {
		return "Graph is fully reducible. Control flow is well-structured."
	}

	if result.Score >= 0.95 {
		return "Nearly reducible. Consider reviewing the few irreducible regions for potential simplification."
	}

	if result.Score >= 0.80 {
		return "Mostly reducible. The irreducible regions may benefit from refactoring to improve maintainability."
	}

	if result.Score >= 0.50 {
		return "Significant irreducible structure. Consider restructuring control flow to reduce complexity."
	}

	return "Highly irreducible structure detected. Major refactoring recommended to improve code maintainability."
}

// buildOutput creates the typed output struct.
func (t *checkReducibilityTool) buildOutput(
	result *graph.ReducibilityResult,
	showIrreducible bool,
) CheckReducibilityOutput {
	output := CheckReducibilityOutput{
		IsReducible:    result.IsReducible,
		Score:          result.Score,
		QualityLabel:   t.getQualityLabel(result.Score),
		Recommendation: t.getRecommendation(result),
	}

	// Build summary
	output.Summary = CheckReducibilitySummary{
		TotalNodes:             result.NodeCount,
		TotalEdges:             result.EdgeCount,
		IrreducibleNodeCount:   result.Summary.IrreducibleNodeCount,
		IrreducibleRegionCount: len(result.IrreducibleRegions),
		CrossEdgeCount:         result.CrossEdgeCount,
		WellStructuredPercent:  result.Score * 100,
	}

	// Build irreducible regions if requested
	if showIrreducible && len(result.IrreducibleRegions) > 0 {
		regions := make([]IrreducibleRegionInfo, 0, len(result.IrreducibleRegions))
		for _, region := range result.IrreducibleRegions {
			info := IrreducibleRegionInfo{
				ID:             region.ID,
				EntryNodes:     region.EntryNodes,
				Size:           region.Size,
				CrossEdgeCount: len(region.CrossEdges),
				Reason:         region.Reason,
			}
			regions = append(regions, info)
		}
		output.IrreducibleRegions = regions
	}

	return output
}

// formatText creates a human-readable text summary.
func (t *checkReducibilityTool) formatText(
	result *graph.ReducibilityResult,
	output CheckReducibilityOutput,
) string {
	// Pre-size buffer: ~200 base + 100/irreducible_region
	estimatedSize := 200 + len(output.IrreducibleRegions)*100
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString("Reducibility Analysis:\n\n")

	if result.IsReducible {
		sb.WriteString("Status: REDUCIBLE (Well-structured)\n")
	} else {
		sb.WriteString("Status: NON-REDUCIBLE (Contains complex control flow)\n")
	}

	sb.WriteString(fmt.Sprintf("Score: %.1f%% (%s)\n", result.Score*100, output.QualityLabel))
	sb.WriteString(fmt.Sprintf("Nodes analyzed: %d\n", result.NodeCount))
	sb.WriteString(fmt.Sprintf("Edges analyzed: %d\n\n", result.EdgeCount))

	if !result.IsReducible {
		sb.WriteString(fmt.Sprintf("Irreducible regions: %d\n", len(result.IrreducibleRegions)))
		sb.WriteString(fmt.Sprintf("Nodes in irreducible regions: %d (%.1f%%)\n",
			result.Summary.IrreducibleNodeCount,
			100-result.Score*100))
		sb.WriteString(fmt.Sprintf("Cross edges: %d\n\n", result.CrossEdgeCount))

		if len(output.IrreducibleRegions) > 0 {
			sb.WriteString("Irreducible Regions:\n")
			for _, region := range output.IrreducibleRegions {
				sb.WriteString(fmt.Sprintf("  Region %d: %d nodes, %d entry points\n",
					region.ID, region.Size, len(region.EntryNodes)))
				if region.Reason != "" {
					sb.WriteString(fmt.Sprintf("    Reason: %s\n", region.Reason))
				}
				if len(region.EntryNodes) > 0 && len(region.EntryNodes) <= 5 {
					sb.WriteString(fmt.Sprintf("    Entries: %v\n", region.EntryNodes))
				}
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("Recommendation: %s\n", output.Recommendation))

	return sb.String()
}
