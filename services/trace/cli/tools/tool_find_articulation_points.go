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
// find_articulation_points Tool (GR-17a) - Typed Implementation
// =============================================================================

var findArticulationPointsTracer = otel.Tracer("tools.find_articulation_points")

// FindArticulationPointsParams contains the validated input parameters.
type FindArticulationPointsParams struct {
	// Top is the number of articulation points to return.
	// Default: 20, Max: 100
	Top int

	// IncludeBridges indicates whether to include critical edges (bridges).
	// Default: true
	IncludeBridges bool
}

// FindArticulationPointsOutput contains the structured result.
type FindArticulationPointsOutput struct {
	// ArticulationPoints is the list of detected articulation points.
	ArticulationPoints []ArticulationPointInfo `json:"articulation_points"`

	// Bridges is the list of critical edges.
	Bridges []BridgeInfo `json:"bridges,omitempty"`

	// TotalComponents is the number of connected components.
	TotalComponents int `json:"total_components"`

	// FragilityScore is the ratio of articulation points to total nodes.
	FragilityScore float64 `json:"fragility_score"`

	// FragilityLevel is a human-readable fragility assessment.
	FragilityLevel string `json:"fragility_level"`

	// NodeCount is the total number of nodes analyzed.
	NodeCount int `json:"node_count"`

	// EdgeCount is the total number of edges analyzed.
	EdgeCount int `json:"edge_count"`
}

// ArticulationPointInfo holds information about a single articulation point.
type ArticulationPointInfo struct {
	// ID is the full node ID of the articulation point.
	ID string `json:"id"`

	// Name is the function name of the articulation point.
	Name string `json:"name"`

	// File is the source file containing this articulation point.
	File string `json:"file,omitempty"`

	// Line is the line number in the source file.
	Line int `json:"line,omitempty"`

	// Kind is the symbol kind (function, method, etc.).
	Kind string `json:"kind,omitempty"`
}

// BridgeInfo holds information about a critical edge.
type BridgeInfo struct {
	// From is the source node ID.
	From string `json:"from"`

	// To is the target node ID.
	To string `json:"to"`

	// FromName is the source function name.
	FromName string `json:"from_name"`

	// ToName is the target function name.
	ToName string `json:"to_name"`
}

// findArticulationPointsTool finds single points of failure in the call graph.
type findArticulationPointsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindArticulationPointsTool creates the find_articulation_points tool.
//
// Description:
//
//	Creates a tool that finds single points of failure in the call graph
//	using Tarjan's articulation point algorithm. These are functions whose
//	removal would disconnect parts of the codebase.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance. Must not be nil.
//   - idx: Symbol index for name lookups. May be nil for degraded operation.
//
// Outputs:
//
//   - Tool: The find_articulation_points tool implementation.
//
// Limitations:
//
//   - Treats directed call graph as undirected for connectivity
//   - Maximum 100 articulation points reported
//   - Impact scoring is approximate (based on degree, not actual removal)
//
// Assumptions:
//
//   - Graph is frozen and indexed before tool creation
//   - Analytics wraps a HierarchicalGraph
func NewFindArticulationPointsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findArticulationPointsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findArticulationPointsTool) Name() string {
	return "find_articulation_points"
}

func (t *findArticulationPointsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findArticulationPointsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_articulation_points",
		Description: "Find single points of failure in the call graph. " +
			"These are functions whose removal would disconnect parts of the codebase. " +
			"Critical for identifying fragile architecture and bus-factor risks.",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of articulation points to return (default: 20, max: 100)",
				Required:    false,
				Default:     20,
			},
			"include_bridges": {
				Type:        ParamTypeBool,
				Description: "Include critical edges (bridges) in output (default: true)",
				Required:    false,
				Default:     true,
			},
		},
		Category:    CategoryExploration,
		Priority:    84,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"articulation points", "cut vertices", "single point of failure",
				"bottleneck", "fragile", "bus factor", "critical node",
				"disconnection", "fragility", "bridge",
			},
			UseWhen: "User asks about single points of failure, fragile architecture, " +
				"or wants to identify functions that are critical to connectivity.",
			AvoidWhen: "User asks about dominators or control flow (use find_dominators). " +
				"Use find_hotspots for highly connected nodes that aren't necessarily cut vertices.",
		},
	}
}

// Execute runs the find_articulation_points tool.
func (t *findArticulationPointsTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Validate analytics is available
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Start span with context
	ctx, span := findArticulationPointsTracer.Start(ctx, "findArticulationPointsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_articulation_points"),
			attribute.Int("top", p.Top),
			attribute.Bool("include_bridges", p.IncludeBridges),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Call ArticulationPointsWithCRS for tracing
	result, traceStep := t.analytics.ArticulationPointsWithCRS(ctx)

	// Handle nil result (shouldn't happen but defensive)
	if result == nil {
		return &Result{
			Success: false,
			Error:   "articulation point detection failed",
		}, nil
	}

	span.SetAttributes(
		attribute.Int("articulation_points_found", len(result.Points)),
		attribute.Int("bridges_found", len(result.Bridges)),
		attribute.Int("components", result.Components),
		attribute.Int("node_count", result.NodeCount),
		attribute.Int("edge_count", result.EdgeCount),
		attribute.String("trace_action", traceStep.Action),
	)

	// Calculate fragility score
	fragilityScore := 0.0
	if result.NodeCount > 0 {
		fragilityScore = float64(len(result.Points)) / float64(result.NodeCount)
	}

	// Build typed output
	output := t.buildOutput(result, p, fragilityScore)

	// Format text output
	outputText := t.formatText(result, output, fragilityScore)

	// Log completion for production debugging
	t.logger.Debug("find_articulation_points completed",
		slog.String("tool", "find_articulation_points"),
		slog.Int("points_returned", len(output.ArticulationPoints)),
		slog.Int("total_points", len(result.Points)),
		slog.Float64("fragility_score", fragilityScore),
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
func (t *findArticulationPointsTool) parseParams(params map[string]any) (FindArticulationPointsParams, error) {
	p := FindArticulationPointsParams{
		Top:            20,
		IncludeBridges: true,
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_articulation_points"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_articulation_points"),
					slog.Int("requested", top),
				)
				top = 100
			}
			p.Top = top
		}
	}

	// Extract include_bridges (optional)
	if includeBridgesRaw, ok := params["include_bridges"]; ok {
		if includeBridges, ok := parseBoolParam(includeBridgesRaw); ok {
			p.IncludeBridges = includeBridges
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findArticulationPointsTool) buildOutput(
	result *graph.ArticulationResult,
	p FindArticulationPointsParams,
	fragilityScore float64,
) FindArticulationPointsOutput {
	// Format articulation points with metadata
	points := make([]ArticulationPointInfo, 0, minInt(len(result.Points), p.Top))
	for i, pointID := range result.Points {
		if i >= p.Top {
			break
		}

		info := ArticulationPointInfo{
			ID: pointID,
		}

		// Try to resolve name and location from index
		if t.index != nil {
			if sym, ok := t.index.GetByID(pointID); ok && sym != nil {
				info.Name = sym.Name
				info.File = sym.FilePath
				info.Line = sym.StartLine
				info.Kind = sym.Kind.String()
			} else {
				// Fallback: extract name from ID
				info.Name = extractNameFromNodeID(pointID)
			}
		} else {
			info.Name = extractNameFromNodeID(pointID)
		}

		points = append(points, info)
	}

	// Format bridges if requested
	var bridges []BridgeInfo
	if p.IncludeBridges {
		bridges = make([]BridgeInfo, 0, len(result.Bridges))
		for _, bridge := range result.Bridges {
			info := BridgeInfo{
				From: bridge[0],
				To:   bridge[1],
			}

			// Try to resolve names from index
			if t.index != nil {
				if sym, ok := t.index.GetByID(bridge[0]); ok && sym != nil {
					info.FromName = sym.Name
				} else {
					info.FromName = extractNameFromNodeID(bridge[0])
				}
				if sym, ok := t.index.GetByID(bridge[1]); ok && sym != nil {
					info.ToName = sym.Name
				} else {
					info.ToName = extractNameFromNodeID(bridge[1])
				}
			} else {
				info.FromName = extractNameFromNodeID(bridge[0])
				info.ToName = extractNameFromNodeID(bridge[1])
			}

			bridges = append(bridges, info)
		}
	}

	return FindArticulationPointsOutput{
		ArticulationPoints: points,
		Bridges:            bridges,
		TotalComponents:    result.Components,
		FragilityScore:     fragilityScore,
		FragilityLevel:     t.getFragilityLevel(fragilityScore),
		NodeCount:          result.NodeCount,
		EdgeCount:          result.EdgeCount,
	}
}

// getFragilityLevel returns a human-readable fragility assessment.
func (t *findArticulationPointsTool) getFragilityLevel(score float64) string {
	switch {
	case score >= 0.2:
		return "HIGH - many single points of failure"
	case score >= 0.1:
		return "MODERATE - some architectural bottlenecks"
	case score >= 0.05:
		return "LOW - reasonably robust"
	default:
		return "MINIMAL - well-connected architecture"
	}
}

// formatText creates a human-readable text summary.
func (t *findArticulationPointsTool) formatText(
	result *graph.ArticulationResult,
	output FindArticulationPointsOutput,
	fragilityScore float64,
) string {
	// Pre-size buffer: ~200 base + 100/point + 50/bridge
	estimatedSize := 200 + len(output.ArticulationPoints)*100 + len(output.Bridges)*50
	var sb strings.Builder
	sb.Grow(estimatedSize)

	if len(result.Points) == 0 {
		sb.WriteString("No articulation points found. The codebase has no single points of failure.\n")
		return sb.String()
	}

	// Header with fragility assessment
	fragilityLevel := t.getFragilityLevel(fragilityScore)
	sb.WriteString(fmt.Sprintf("Found %d articulation points (fragility: %.1f%% - %s):\n\n",
		len(result.Points), fragilityScore*100, fragilityLevel))

	// List articulation points with full IDs for tool chaining
	for i, point := range output.ArticulationPoints {
		name := point.Name
		if name == "" {
			name = "(unknown)"
		}

		// Always include full ID for use with build_minimal_context
		if point.ID != "" {
			if point.File != "" {
				sb.WriteString(fmt.Sprintf("%d. %s [ID: %s] (%s:%d)\n", i+1, name, point.ID, point.File, point.Line))
			} else {
				sb.WriteString(fmt.Sprintf("%d. %s [ID: %s]\n", i+1, name, point.ID))
			}
		} else if point.File != "" {
			sb.WriteString(fmt.Sprintf("%d. %s (%s:%d)\n", i+1, name, point.File, point.Line))
		} else {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, name))
		}
	}

	if len(result.Points) > len(output.ArticulationPoints) {
		sb.WriteString(fmt.Sprintf("\n... and %d more articulation points\n", len(result.Points)-len(output.ArticulationPoints)))
	}

	// Bridges section
	if len(output.Bridges) > 0 {
		sb.WriteString(fmt.Sprintf("\nCritical edges (bridges): %d\n", len(result.Bridges)))
		limit := 5
		if len(output.Bridges) < limit {
			limit = len(output.Bridges)
		}
		for i := 0; i < limit; i++ {
			bridge := output.Bridges[i]
			fromName := bridge.FromName
			toName := bridge.ToName
			if fromName == "" {
				fromName = "(unknown)"
			}
			if toName == "" {
				toName = "(unknown)"
			}
			sb.WriteString(fmt.Sprintf("  %s -> %s\n", fromName, toName))
		}
		if len(result.Bridges) > limit {
			sb.WriteString(fmt.Sprintf("  ... and %d more bridges\n", len(result.Bridges)-limit))
		}
	}

	// Summary
	sb.WriteString(fmt.Sprintf("\nSummary:\n"))
	sb.WriteString(fmt.Sprintf("  - Fragility score: %.1f%% (%s)\n", fragilityScore*100, fragilityLevel))
	sb.WriteString(fmt.Sprintf("  - Connected components: %d\n", result.Components))
	sb.WriteString(fmt.Sprintf("  - Total nodes analyzed: %d\n", result.NodeCount))

	if fragilityScore > 0.1 {
		sb.WriteString("\nHigh fragility detected. Consider adding redundant paths or decoupling these functions.\n")
	}

	return sb.String()
}
