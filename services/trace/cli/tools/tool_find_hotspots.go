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

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_hotspots Tool - Typed Implementation
// =============================================================================

var findHotspotsTracer = otel.Tracer("tools.find_hotspots")

// FindHotspotsParams contains the validated input parameters.
type FindHotspotsParams struct {
	// Top is the number of hotspots to return.
	// Default: 10, Max: 100
	Top int

	// Kind filters results by symbol kind.
	// Values: "function", "type", "all"
	// Default: "all"
	Kind string
}

// FindHotspotsOutput contains the structured result.
type FindHotspotsOutput struct {
	// HotspotCount is the number of hotspots returned.
	HotspotCount int `json:"hotspot_count"`

	// Hotspots is the list of hotspot symbols.
	Hotspots []HotspotInfo `json:"hotspots"`
}

// HotspotInfo holds information about a hotspot symbol.
type HotspotInfo struct {
	// Rank is the position in the ranking (1-based).
	Rank int `json:"rank"`

	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind (function, type, etc.).
	Kind string `json:"kind"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Package is the package name.
	Package string `json:"package"`

	// Score is the connectivity score.
	Score int `json:"score"`

	// InDegree is the number of incoming edges.
	InDegree int `json:"in_degree"`

	// OutDegree is the number of outgoing edges.
	OutDegree int `json:"out_degree"`
}

// findHotspotsTool wraps graph.GraphAnalytics.HotSpots.
//
// Description:
//
//	Finds the most-connected nodes in the graph (hotspots). Hotspots are
//	symbols with many incoming and outgoing edges, indicating high coupling
//	and potential refactoring targets.
//
// GR-01 Optimization:
//
//	Uses GraphAnalytics which operates on the HierarchicalGraph with O(V log k)
//	complexity for top-k hotspots.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findHotspotsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindHotspotsTool creates the find_hotspots tool.
//
// Description:
//
//	Creates a tool that finds the most-connected symbols in the codebase
//	(hotspots). Hotspots are nodes with high connectivity scores, indicating
//	central points in the code that many other components depend on.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance for hotspot detection. Must not be nil.
//   - idx: The symbol index for name lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_hotspots tool implementation.
//
// Limitations:
//
//   - Connectivity score uses formula: inDegree*2 + outDegree (favors callee-heavy nodes)
//   - Maximum 100 results per query to prevent excessive output
//   - When filtering by kind, results may be fewer than requested if not enough match
//
// Assumptions:
//
//   - Graph is frozen and indexed before tool creation
//   - Analytics wraps a HierarchicalGraph for O(V log k) complexity
func NewFindHotspotsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findHotspotsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findHotspotsTool) Name() string {
	return "find_hotspots"
}

func (t *findHotspotsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findHotspotsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_hotspots",
		Description: "Find the most-connected symbols in the codebase (hotspots). " +
			"Hotspots indicate high coupling and potential refactoring targets. " +
			"Returns symbols ranked by connectivity score (inDegree*2 + outDegree).",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of hotspots to return (1-100)",
				Required:    false,
				Default:     10,
			},
			"kind": {
				Type:        ParamTypeString,
				Description: "Filter by symbol kind: function, type, or all",
				Required:    false,
				Default:     "all",
				Enum:        []any{"function", "type", "all"},
			},
		},
		Category:    CategoryExploration,
		Priority:    86,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// Execute runs the find_hotspots tool.
func (t *findHotspotsTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Validate analytics is available
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Start span with context
	ctx, span := findHotspotsTracer.Start(ctx, "findHotspotsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_hotspots"),
			attribute.Int("top", p.Top),
			attribute.String("kind", p.Kind),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Adaptive request size based on filter
	requestCount := p.Top
	if p.Kind != "all" {
		requestCount = p.Top * 3 // Request 3x when filtering
		t.logger.Debug("hotspot request adjusted for filtering",
			slog.String("tool", "find_hotspots"),
			slog.String("kind_filter", p.Kind),
			slog.Int("top_requested", p.Top),
			slog.Int("request_count", requestCount),
		)
	}

	// Get hotspots using CRS-enabled method for tracing
	hotspots, traceStep := t.analytics.HotSpotsWithCRS(ctx, requestCount)

	span.SetAttributes(
		attribute.Int("raw_hotspots", len(hotspots)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Filter by kind if needed
	var filtered []graph.HotspotNode
	if p.Kind == "all" {
		filtered = hotspots
	} else {
		for _, hs := range hotspots {
			if hs.Node == nil || hs.Node.Symbol == nil {
				continue
			}
			if matchesHotspotKind(hs.Node.Symbol.Kind, p.Kind) {
				filtered = append(filtered, hs)
			}
		}
	}

	// Trim to requested count
	if len(filtered) > p.Top {
		filtered = filtered[:p.Top]
	}

	span.SetAttributes(attribute.Int("filtered_hotspots", len(filtered)))

	// Structured logging for edge cases
	if len(hotspots) > 0 && len(filtered) == 0 {
		t.logger.Debug("all hotspots filtered by kind",
			slog.String("tool", "find_hotspots"),
			slog.Int("raw_count", len(hotspots)),
			slog.String("kind_filter", p.Kind),
		)
	} else if len(filtered) < p.Top && p.Kind != "all" {
		t.logger.Debug("fewer hotspots than requested after filtering",
			slog.String("tool", "find_hotspots"),
			slog.Int("requested", p.Top),
			slog.Int("returned", len(filtered)),
			slog.String("kind_filter", p.Kind),
		)
	}

	// Build typed output
	output := t.buildOutput(filtered)

	// Format text output
	outputText := t.formatText(filtered)

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
func (t *findHotspotsTool) parseParams(params map[string]any) (FindHotspotsParams, error) {
	p := FindHotspotsParams{
		Top:  10,
		Kind: "all",
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_hotspots"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_hotspots"),
					slog.Int("requested", top),
				)
				top = 100
			}
			p.Top = top
		}
	}

	// Extract kind (optional)
	if kindRaw, ok := params["kind"]; ok {
		if kind, ok := parseStringParam(kindRaw); ok {
			validKinds := map[string]bool{"function": true, "type": true, "all": true}
			if !validKinds[kind] {
				t.logger.Warn("invalid kind filter, defaulting to all",
					slog.String("tool", "find_hotspots"),
					slog.String("invalid_kind", kind),
				)
				kind = "all"
			}
			p.Kind = kind
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findHotspotsTool) buildOutput(hotspots []graph.HotspotNode) FindHotspotsOutput {
	results := make([]HotspotInfo, 0, len(hotspots))

	for i, hs := range hotspots {
		if hs.Node == nil || hs.Node.Symbol == nil {
			continue
		}
		sym := hs.Node.Symbol
		results = append(results, HotspotInfo{
			Rank:      i + 1,
			Name:      sym.Name,
			Kind:      sym.Kind.String(),
			File:      sym.FilePath,
			Line:      sym.StartLine,
			Package:   sym.Package,
			Score:     hs.Score,
			InDegree:  hs.InDegree,
			OutDegree: hs.OutDegree,
		})
	}

	return FindHotspotsOutput{
		HotspotCount: len(results),
		Hotspots:     results,
	}
}

// formatText creates a human-readable text summary.
func (t *findHotspotsTool) formatText(hotspots []graph.HotspotNode) string {
	var sb strings.Builder

	if len(hotspots) == 0 {
		sb.WriteString("No hotspots found.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Top %d Hotspots by Connectivity:\n\n", len(hotspots)))

	for i, hs := range hotspots {
		if hs.Node == nil || hs.Node.Symbol == nil {
			continue
		}
		sym := hs.Node.Symbol
		sb.WriteString(fmt.Sprintf("%d. %s (score: %d)\n", i+1, sym.Name, hs.Score))
		sb.WriteString(fmt.Sprintf("   %s:%d\n", sym.FilePath, sym.StartLine))
		sb.WriteString(fmt.Sprintf("   InDegree: %d, OutDegree: %d\n", hs.InDegree, hs.OutDegree))
		if sym.Kind != ast.SymbolKindUnknown {
			sb.WriteString(fmt.Sprintf("   Kind: %s\n", sym.Kind))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// matchesHotspotKind checks if a symbol kind matches a filter string for hotspots.
func matchesHotspotKind(kind ast.SymbolKind, filter string) bool {
	switch filter {
	case "function":
		return kind == ast.SymbolKindFunction || kind == ast.SymbolKindMethod
	case "type":
		return kind == ast.SymbolKindType || kind == ast.SymbolKindStruct || kind == ast.SymbolKindInterface
	default:
		return true
	}
}
