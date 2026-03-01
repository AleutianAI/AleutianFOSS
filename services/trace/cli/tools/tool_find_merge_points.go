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
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_merge_points Tool (GR-17d) - Typed Implementation
// =============================================================================

var findMergePointsTracer = otel.Tracer("tools.find_merge_points")

// FindMergePointsParams contains the validated input parameters.
type FindMergePointsParams struct {
	// Top is the number of merge points to return.
	// Default: 20, Max: 100
	Top int

	// MinSources is the minimum incoming paths to qualify as merge point.
	// Default: 2
	MinSources int
}

// ToolName returns the tool name for TypedParams interface.
func (p FindMergePointsParams) ToolName() string { return "find_merge_points" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindMergePointsParams) ToMap() map[string]any {
	return map[string]any{
		"top":         p.Top,
		"min_sources": p.MinSources,
	}
}

// FindMergePointsOutput contains the structured result.
type FindMergePointsOutput struct {
	// MergePoints is the list of detected merge points.
	MergePoints []MergePointInfo `json:"merge_points"`

	// Summary contains aggregate statistics.
	Summary MergePointsSummary `json:"summary"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// MergePointInfo holds information about a single merge point.
type MergePointInfo struct {
	// ID is the full node ID of the merge point.
	ID string `json:"id"`

	// Name is the function name of the merge point.
	Name string `json:"name"`

	// File is the source file containing this merge point.
	File string `json:"file,omitempty"`

	// Line is the line number in the source file.
	Line int `json:"line,omitempty"`

	// ConvergingPaths is the number of code paths that converge at this point.
	ConvergingPaths int `json:"converging_paths"`

	// SourceNodes lists the nodes whose dominance frontier includes this merge point.
	SourceNodes []string `json:"source_nodes,omitempty"`

	// HasMoreSources indicates if there are more source nodes than shown.
	HasMoreSources bool `json:"has_more_sources,omitempty"`
}

// MergePointsSummary contains aggregate statistics about merge points.
type MergePointsSummary struct {
	// TotalMergePoints is the total number of merge points detected.
	TotalMergePoints int `json:"total_merge_points"`

	// Returned is the number of merge points returned in this result.
	Returned int `json:"returned"`

	// MaxConvergence is the maximum convergence count among returned merge points.
	MaxConvergence int `json:"max_convergence"`

	// AvgConvergence is the average convergence count among returned merge points.
	AvgConvergence float64 `json:"avg_convergence"`
}

// findMergePointsTool finds nodes where different code paths converge.
type findMergePointsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindMergePointsTool creates a new find_merge_points tool.
//
// Description:
//
//	Creates a tool for finding merge points - nodes where multiple code paths
//	converge. These are identified via dominance frontier analysis.
//
// Inputs:
//   - analytics: GraphAnalytics instance for frontier computation. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_merge_points tool.
//
// Thread Safety: Safe for concurrent use after construction.
//
// Limitations:
//   - Requires dominator tree and dominance frontier computation
//   - May not detect all merge points in highly disconnected graphs
func NewFindMergePointsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findMergePointsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findMergePointsTool) Name() string {
	return "find_merge_points"
}

func (t *findMergePointsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findMergePointsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_merge_points",
		Description: "Find where different code paths converge. " +
			"These are integration points where multiple call chains meet. " +
			"Useful for identifying coordination points and potential bottlenecks.",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of merge points to return (default: 20, max: 100)",
				Required:    false,
				Default:     20,
			},
			"min_sources": {
				Type:        ParamTypeInt,
				Description: "Minimum incoming paths to qualify as merge point (default: 2)",
				Required:    false,
				Default:     2,
			},
		},
		Category:    CategoryExploration,
		Priority:    82,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"merge point", "convergence", "paths converge",
				"integration point", "coordination point",
				"where paths meet", "common destination",
			},
			UseWhen: "User asks where different code paths converge or meet, " +
				"or wants to find coordination/integration points.",
			AvoidWhen: "User asks about single points of failure. " +
				"Use find_articulation_points for that.",
		},
	}
}

// Execute runs the find_merge_points tool.
func (t *findMergePointsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
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

	// GR-17b Fix: Check graph readiness with retry logic
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check if graph is ready
		if t.analytics.IsGraphReady() {
			break
		}

		// If this is not the last attempt, retry after delay
		if attempt < maxRetries-1 {
			t.logger.Info("graph not ready, retrying after delay",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", maxRetries),
				slog.Duration("retry_delay", retryDelay),
			)
			time.Sleep(retryDelay)
		} else {
			// Last attempt failed - return error
			return &Result{
				Success: false,
				Error: "graph not ready - indexing still in progress. " +
					"Please wait a few seconds for graph initialization to complete and try again.",
			}, nil
		}
	}

	// Continue with existing logic after graph readiness confirmed
	return t.executeOnce(ctx, p)
}

// executeOnce performs a single execution attempt (extracted for retry logic).
func (t *findMergePointsTool) executeOnce(ctx context.Context, p FindMergePointsParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := findMergePointsTracer.Start(ctx, "findMergePointsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_merge_points"),
			attribute.Int("top", p.Top),
			attribute.Int("min_sources", p.MinSources),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect entry point for dominator analysis
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

	// Check context again before frontier computation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Compute dominance frontier
	// GR-17b Fix: After Fix 2, this will return error if dominator tree is empty
	// (indicating graph not ready), which will be caught and returned to user.
	frontier, traceStep := t.analytics.ComputeDominanceFrontierWithCRS(ctx, domTree)
	if frontier == nil {
		// This should not happen after Fix 2, but keep as defensive check
		span.SetAttributes(attribute.Int("merge_points_found", 0))
		return &Result{
			Success:   false,
			Error:     "dominance frontier computation returned nil - graph may not be ready",
			TraceStep: &traceStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Check if frontier computation returned error via TraceStep
	if traceStep.Error != "" {
		span.RecordError(fmt.Errorf("frontier computation failed: %s", traceStep.Error))
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("failed to compute dominance frontier: %s", traceStep.Error),
			TraceStep: &traceStep,
			Duration:  time.Since(start),
		}, nil
	}

	// If frontier is empty (no merge points exist), return success with empty result
	if len(frontier.MergePoints) == 0 {
		span.SetAttributes(attribute.Int("merge_points_found", 0))
		output := FindMergePointsOutput{
			MergePoints: []MergePointInfo{},
			Summary: MergePointsSummary{
				TotalMergePoints: 0,
				Returned:         0,
				MaxConvergence:   0,
				AvgConvergence:   0.0,
			},
			Message: "No merge points found - graph may be a tree or DAG with no convergence",
		}
		outputText := "No merge points found - graph may be a tree or DAG with no convergence.\n"
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Find and rank merge points
	mergePoints := t.findAndRankMergePoints(frontier, p.MinSources, p.Top)

	// Build typed output
	output := t.buildOutput(mergePoints, frontier, p.Top)

	// Format text output
	outputText := t.formatText(mergePoints, frontier, p.Top)

	span.SetAttributes(
		attribute.Int("merge_points_found", len(frontier.MergePoints)),
		attribute.Int("merge_points_returned", len(mergePoints)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Log completion for production debugging
	t.logger.Debug("find_merge_points completed",
		slog.String("tool", "find_merge_points"),
		slog.Int("merge_points_returned", len(mergePoints)),
		slog.Int("total_merge_points", len(frontier.MergePoints)),
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
func (t *findMergePointsTool) parseParams(params map[string]any) (FindMergePointsParams, error) {
	p := FindMergePointsParams{
		Top:        20,
		MinSources: 2,
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_merge_points"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_merge_points"),
					slog.Int("requested", top),
				)
				top = 100
			}
			p.Top = top
		}
	}

	// Extract min_sources (optional)
	if minSourcesRaw, ok := params["min_sources"]; ok {
		if minSources, ok := parseIntParam(minSourcesRaw); ok {
			if minSources < 2 {
				t.logger.Debug("min_sources below minimum, clamping to 2",
					slog.String("tool", "find_merge_points"),
					slog.Int("requested", minSources),
				)
				minSources = 2
			}
			p.MinSources = minSources
		}
	}

	return p, nil
}

// findAndRankMergePoints finds merge points and ranks by convergence count.
func (t *findMergePointsTool) findAndRankMergePoints(
	frontier *graph.DominanceFrontier,
	minSources int,
	top int,
) []MergePointInfo {
	// Get merge points that meet the threshold
	var mergePoints []MergePointInfo

	for _, mpID := range frontier.MergePoints {
		degree := frontier.MergePointDegree(mpID)
		if degree < minSources {
			continue
		}

		mp := MergePointInfo{
			ID:              mpID,
			Name:            extractNameFromNodeID(mpID),
			ConvergingPaths: degree,
			SourceNodes:     t.findSourceNodes(mpID, frontier),
		}

		// Mark if there are more sources
		if len(mp.SourceNodes) >= 10 {
			mp.HasMoreSources = true
		}

		// Try to get file and line info from index
		if t.index != nil {
			if sym, ok := t.index.GetByID(mpID); ok && sym != nil {
				mp.File = sym.FilePath
				mp.Line = sym.StartLine
			}
		}

		mergePoints = append(mergePoints, mp)
	}

	// Sort by converging paths descending, then by name for stability
	sort.Slice(mergePoints, func(i, j int) bool {
		if mergePoints[i].ConvergingPaths != mergePoints[j].ConvergingPaths {
			return mergePoints[i].ConvergingPaths > mergePoints[j].ConvergingPaths
		}
		return mergePoints[i].Name < mergePoints[j].Name
	})

	// Return top N
	if len(mergePoints) > top {
		mergePoints = mergePoints[:top]
	}

	return mergePoints
}

// findSourceNodes finds all nodes whose frontier contains the merge point.
func (t *findMergePointsTool) findSourceNodes(mergePoint string, frontier *graph.DominanceFrontier) []string {
	sources := make([]string, 0)
	maxSources := 10 // Cap to prevent huge outputs

	for nodeID, frontierNodes := range frontier.Frontier {
		for _, f := range frontierNodes {
			if f == mergePoint {
				sources = append(sources, nodeID)
				if len(sources) >= maxSources {
					return sources
				}
				break
			}
		}
	}

	return sources
}

// buildOutput creates the typed output struct.
func (t *findMergePointsTool) buildOutput(
	mergePoints []MergePointInfo,
	frontier *graph.DominanceFrontier,
	top int,
) FindMergePointsOutput {
	// Compute summary statistics
	maxConvergence := 0
	totalConvergence := 0
	for _, mp := range mergePoints {
		if mp.ConvergingPaths > maxConvergence {
			maxConvergence = mp.ConvergingPaths
		}
		totalConvergence += mp.ConvergingPaths
	}

	avgConvergence := 0.0
	if len(mergePoints) > 0 {
		avgConvergence = float64(totalConvergence) / float64(len(mergePoints))
	}

	return FindMergePointsOutput{
		MergePoints: mergePoints,
		Summary: MergePointsSummary{
			TotalMergePoints: len(frontier.MergePoints),
			Returned:         len(mergePoints),
			MaxConvergence:   maxConvergence,
			AvgConvergence:   avgConvergence,
		},
	}
}

// formatText creates a human-readable text summary.
func (t *findMergePointsTool) formatText(
	mergePoints []MergePointInfo,
	frontier *graph.DominanceFrontier,
	top int,
) string {
	// Pre-size buffer: ~200 base + 120/merge_point
	estimatedSize := 200 + len(mergePoints)*120
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString(fmt.Sprintf("Found %d merge points", len(frontier.MergePoints)))
	if len(frontier.MergePoints) > top {
		sb.WriteString(fmt.Sprintf(" (showing top %d)", top))
	}
	sb.WriteString("\n\n")

	// List merge points
	sb.WriteString("Merge Points (sorted by convergence):\n")
	for i, mp := range mergePoints {
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, mp.Name))
		sb.WriteString(fmt.Sprintf("     ID: %s\n", mp.ID))
		sb.WriteString(fmt.Sprintf("     Converging paths: %d\n", mp.ConvergingPaths))
		if mp.File != "" {
			sb.WriteString(fmt.Sprintf("     Location: %s:%d\n", mp.File, mp.Line))
		}
		if len(mp.SourceNodes) > 0 {
			sb.WriteString("     Sources:\n")
			for _, src := range mp.SourceNodes {
				srcName := extractNameFromNodeID(src)
				sb.WriteString(fmt.Sprintf("       - %s\n", srcName))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
