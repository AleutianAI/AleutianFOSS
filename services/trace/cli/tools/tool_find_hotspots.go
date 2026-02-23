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
	"path/filepath"
	"sort"
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
	// Values: "function", "method", "type", "class", "struct", "interface", "enum", "variable", "constant", "all"
	// Default: "all"
	Kind string

	// Package filters results to a specific package or module path.
	// When set, only hotspots in the matching package (or file path containing
	// the package string) are returned.
	Package string

	// ExcludeTests filters out symbols from test files.
	// When true (default), symbols in files matching test patterns
	// (*_test.go, test_*.py, *.test.ts, *.spec.js, etc.) are excluded.
	ExcludeTests bool

	// SortBy controls the ranking dimension.
	// Values: "score" (default, inDegree*2+outDegree), "in" (InDegree desc), "out" (OutDegree desc)
	SortBy string
}

// ToolName returns the tool name for TypedParams interface.
func (p FindHotspotsParams) ToolName() string { return "find_hotspots" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindHotspotsParams) ToMap() map[string]any {
	m := map[string]any{
		"top":           p.Top,
		"kind":          p.Kind,
		"exclude_tests": p.ExcludeTests,
		"sort_by":       p.SortBy,
	}
	if p.Package != "" {
		m["package"] = p.Package
	}
	return m
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
				Description: "Filter by symbol kind",
				Required:    false,
				Default:     "all",
				Enum:        []any{"function", "method", "type", "class", "struct", "interface", "enum", "variable", "constant", "all"},
			},
			"package": {
				Type:        ParamTypeString,
				Description: "Filter to a specific package or module path",
				Required:    false,
			},
			"exclude_tests": {
				Type:        ParamTypeBool,
				Description: "Exclude symbols from test files (default: true)",
				Required:    false,
				Default:     true,
			},
			"sort_by": {
				Type:        ParamTypeString,
				Description: "Sort dimension: score (default), in (by InDegree), out (by OutDegree)",
				Required:    false,
				Default:     "score",
				Enum:        []any{"score", "in", "out"},
			},
		},
		Category:    CategoryExploration,
		Priority:    86,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"hotspots", "most connected", "high coupling",
				"central functions", "heavily used", "most called",
				"connectivity", "hub functions", "fan-in", "fan-out",
			},
			UseWhen: "User asks about the most connected, heavily used, or highest-coupling " +
				"functions in the codebase. Use for connectivity-based ranking (inDegree + outDegree).",
			AvoidWhen: "User asks about the most critical or risky functions considering structural " +
				"importance (use find_weighted_criticality). User asks about PageRank importance " +
				"only (use find_important).",
		},
	}
}

// Execute runs the find_hotspots tool.
func (t *findHotspotsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Validate analytics is available
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
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
			attribute.String("package_filter", p.Package),
			attribute.Bool("exclude_tests", p.ExcludeTests),
			attribute.String("sort_by", p.SortBy),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Adaptive request size based on filters.
	// When filtering (kind, package, or test exclusion), we request more
	// candidates than needed since many will be filtered out.
	requestCount := p.Top
	hasFilters := p.Kind != "all" || p.Package != "" || p.ExcludeTests
	if hasFilters {
		requestCount = p.Top * 5 // Request 5x when any filter is active
		t.logger.Debug("hotspot request adjusted for filtering",
			slog.String("tool", "find_hotspots"),
			slog.String("kind_filter", p.Kind),
			slog.String("package_filter", p.Package),
			slog.Bool("exclude_tests", p.ExcludeTests),
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

	// IT-07 Bug 3 / IT-08 Run 3: Filter by package using boundary-aware matching
	if p.Package != "" {
		var pkgFiltered []graph.HotspotNode
		for _, hs := range filtered {
			if hs.Node == nil || hs.Node.Symbol == nil {
				continue
			}
			if matchesPackageScope(hs.Node.Symbol, p.Package) {
				pkgFiltered = append(pkgFiltered, hs)
			}
		}
		filtered = pkgFiltered
	}

	// IT-07 Phase 3: Filter out test and documentation file symbols
	if p.ExcludeTests {
		var nonTestFiltered []graph.HotspotNode
		for _, hs := range filtered {
			if hs.Node == nil || hs.Node.Symbol == nil {
				continue
			}
			filePath := hs.Node.Symbol.FilePath
			// GR-60: Use graph-based file classification instead of heuristics
			isProd := t.analytics.IsProductionFile(filePath)
			// F-3: Safety net — _test.go is NEVER production regardless of classification
			if isProd && strings.HasSuffix(filepath.Base(filePath), "_test.go") {
				isProd = false
			}
			if isProd {
				nonTestFiltered = append(nonTestFiltered, hs)
			}
		}
		if len(nonTestFiltered) > 0 {
			filtered = nonTestFiltered
		}
		// If ALL results are from test/doc files, keep them rather than returning empty.
	}

	// IT-07 Phase 3: Re-sort by requested dimension
	if p.SortBy == "in" || p.SortBy == "out" {
		sort.Slice(filtered, func(i, j int) bool {
			if p.SortBy == "in" {
				if filtered[i].InDegree != filtered[j].InDegree {
					return filtered[i].InDegree > filtered[j].InDegree
				}
				return filtered[i].Score > filtered[j].Score
			}
			// sort_by == "out"
			if filtered[i].OutDegree != filtered[j].OutDegree {
				return filtered[i].OutDegree > filtered[j].OutDegree
			}
			return filtered[i].Score > filtered[j].Score
		})
	}

	// Trim to requested count
	if len(filtered) > p.Top {
		filtered = filtered[:p.Top]
	}

	span.SetAttributes(attribute.Int("filtered_hotspots", len(filtered)))

	// Structured logging for edge cases
	if len(hotspots) > 0 && len(filtered) == 0 {
		t.logger.Debug("all hotspots filtered out",
			slog.String("tool", "find_hotspots"),
			slog.Int("raw_count", len(hotspots)),
			slog.String("kind_filter", p.Kind),
			slog.String("package_filter", p.Package),
		)
	} else if len(filtered) < p.Top && (p.Kind != "all" || p.Package != "") {
		t.logger.Debug("fewer hotspots than requested after filtering",
			slog.String("tool", "find_hotspots"),
			slog.Int("requested", p.Top),
			slog.Int("returned", len(filtered)),
			slog.String("kind_filter", p.Kind),
			slog.String("package_filter", p.Package),
		)
	}

	// Build typed output
	output := t.buildOutput(filtered)

	// Format text output
	outputText := t.formatText(filtered, p.Package, p.SortBy)

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
		Top:          10,
		Kind:         "all",
		ExcludeTests: true,
		SortBy:       "score",
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
			validKinds := map[string]bool{
				"function": true, "method": true,
				"type": true, "class": true, "struct": true, "interface": true,
				"enum": true, "variable": true, "constant": true,
				"all": true,
			}
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

	// IT-07 Bug 3: Extract package (optional)
	if packageRaw, ok := params["package"]; ok {
		if pkg, ok := parseStringParam(packageRaw); ok {
			p.Package = pkg
		}
	}

	// IT-07 Phase 3: Extract exclude_tests (optional, default: true)
	if etRaw, ok := params["exclude_tests"]; ok {
		if et, ok := etRaw.(bool); ok {
			p.ExcludeTests = et
		}
	}

	// IT-07 Phase 3: Extract sort_by (optional, default: "score")
	if sbRaw, ok := params["sort_by"]; ok {
		if sb, ok := parseStringParam(sbRaw); ok {
			validSortBy := map[string]bool{
				"score": true, "in": true, "out": true,
			}
			if !validSortBy[sb] {
				t.logger.Warn("invalid sort_by value, defaulting to score",
					slog.String("tool", "find_hotspots"),
					slog.String("invalid_sort_by", sb),
				)
				sb = "score"
			}
			p.SortBy = sb
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

// formatText creates a human-readable text summary with graph markers.
//
// IT-07 Bug 1: Output must include graph markers so that getSingleFormattedResult()
// can identify authoritative results and skip LLM synthesis:
//   - Zero results: "## GRAPH RESULT" header + "Do NOT use Grep" footer
//   - Positive results: "Found N" prefix + exhaustive footer + "Do NOT use Grep" footer
func (t *findHotspotsTool) formatText(hotspots []graph.HotspotNode, packageFilter, sortBy string) string {
	var sb strings.Builder

	if len(hotspots) == 0 {
		sb.WriteString("## GRAPH RESULT: No hotspots found\n\n")
		sb.WriteString("No symbols with connectivity score > 0 exist in the graph")
		if packageFilter != "" {
			sb.WriteString(fmt.Sprintf(" for package '%s'", packageFilter))
		}
		sb.WriteString(".\n\n")
		sb.WriteString("---\n")
		sb.WriteString("The graph has been fully indexed \u2014 these results are exhaustive.\n")
		sb.WriteString("**Do NOT use Grep or Read to verify** \u2014 the graph already analyzed all source files.\n")
		return sb.String()
	}

	// Describe the sort dimension in the header
	sortLabel := "connectivity"
	switch sortBy {
	case "in":
		sortLabel = "InDegree (fan-in)"
	case "out":
		sortLabel = "OutDegree (fan-out)"
	}

	if packageFilter != "" {
		sb.WriteString(fmt.Sprintf("Found %d hotspots in package '%s' by %s:\n\n", len(hotspots), packageFilter, sortLabel))
	} else {
		sb.WriteString(fmt.Sprintf("Found %d hotspots by %s:\n\n", len(hotspots), sortLabel))
	}

	for i, hs := range hotspots {
		if hs.Node == nil || hs.Node.Symbol == nil {
			continue
		}
		sym := hs.Node.Symbol
		sb.WriteString(fmt.Sprintf("%d. %s (score: %d)\n", i+1, sym.Name, hs.Score))
		sb.WriteString(fmt.Sprintf("   %s:%d\n", sym.FilePath, sym.StartLine))
		sb.WriteString(fmt.Sprintf("   InDegree: %d, OutDegree: %d\n", hs.InDegree, hs.OutDegree))
		if sym.Kind != ast.SymbolKindUnknown {
			sb.WriteString(fmt.Sprintf("   Kind: %s, Package: %s\n", sym.Kind, sym.Package))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed \u2014 these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** \u2014 the graph already analyzed all source files.\n")

	return sb.String()
}

// matchesHotspotKind checks if a symbol kind matches a filter string for hotspots.
//
// IT-07 Bug 2: Expanded to handle all values that extractKindFromQuery() can produce:
// "function", "method", "type", "class", "struct", "interface", "enum", "variable", "constant".
func matchesHotspotKind(kind ast.SymbolKind, filter string) bool {
	switch filter {
	case "function", "method":
		return kind == ast.SymbolKindFunction || kind == ast.SymbolKindMethod ||
			kind == ast.SymbolKindProperty // IT-08c: Python @property has callable body
	case "type", "class", "struct", "interface":
		return kind == ast.SymbolKindType || kind == ast.SymbolKindStruct ||
			kind == ast.SymbolKindInterface || kind == ast.SymbolKindClass
	case "enum":
		return kind == ast.SymbolKindEnum
	case "variable", "constant":
		return kind == ast.SymbolKindVariable || kind == ast.SymbolKindConstant
	default: // "all" or unrecognized
		return true
	}
}
