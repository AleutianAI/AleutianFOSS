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
// find_important Tool (GR-13) - Typed Implementation
// =============================================================================

var findImportantTracer = otel.Tracer("tools.find_important")

// FindImportantParams contains the validated input parameters.
type FindImportantParams struct {
	// Top is the number of important symbols to return.
	// Default: 10, Max: 100
	Top int

	// Kind filters results by symbol kind.
	// Values: "function", "type", "all"
	// Default: "all"
	Kind string

	// Package filters results to a specific package or module scope.
	// Uses matchesPackageScope() with 3 strategies: Package field match,
	// FilePath segment match, and file stem match.
	// IT-07: Added to match find_hotspots package filter capability.
	// Default: "" (no filter)
	Package string

	// ExcludeTests filters out symbols from test and documentation files.
	// Default: true
	ExcludeTests bool

	// Reverse returns lowest-ranked symbols first (peripheral functions).
	// IT-R2c Fix E: Supports "lowest PageRank" / "peripheral" queries.
	// Default: false
	Reverse bool
}

// ToolName returns the tool name for TypedParams interface.
func (p FindImportantParams) ToolName() string { return "find_important" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindImportantParams) ToMap() map[string]any {
	return map[string]any{
		"top":           p.Top,
		"kind":          p.Kind,
		"package":       p.Package,
		"exclude_tests": p.ExcludeTests,
		"reverse":       p.Reverse,
	}
}

// FindImportantOutput contains the structured result.
type FindImportantOutput struct {
	// ResultCount is the number of results returned.
	ResultCount int `json:"result_count"`

	// Results is the list of important symbols.
	Results []ImportantSymbol `json:"results"`

	// Algorithm is the algorithm used (PageRank).
	Algorithm string `json:"algorithm"`
}

// ImportantSymbol holds information about an important symbol.
type ImportantSymbol struct {
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

	// PageRank is the PageRank score.
	PageRank float64 `json:"pagerank"`

	// DegreeScore is the degree-based score for comparison.
	DegreeScore int `json:"degree_score"`
}

// findImportantTool finds the most important symbols using PageRank.
type findImportantTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindImportantTool creates the find_important tool.
//
// Description:
//
//	Creates a tool that finds the most important symbols using PageRank
//	algorithm. PageRank provides better importance ranking than simple
//	degree counting by considering the importance of callers transitively.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance for PageRank computation. Must not be nil.
//   - idx: The symbol index for name lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_important tool implementation.
//
// Limitations:
//
//   - PageRank is more expensive than degree counting O(k × E) vs O(V)
//   - Maximum 100 results per query to prevent excessive output
//   - When filtering by kind, results may be fewer than requested
//
// Assumptions:
//
//   - Graph is frozen and indexed before tool creation
//   - Analytics wraps a HierarchicalGraph
func NewFindImportantTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findImportantTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findImportantTool) Name() string {
	return "find_important"
}

func (t *findImportantTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findImportantTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_important",
		Description: "Find the most important symbols using PageRank algorithm. " +
			"Unlike find_hotspots (which counts connections), this considers the " +
			"importance of callers. A function called by one critical function " +
			"may rank higher than one called by many trivial helpers.",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of important symbols to return (1-100)",
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
			"package": {
				Type:        ParamTypeString,
				Description: "Filter results to a specific package or module (e.g., 'helpers', 'router'). Leave empty for project-wide results.",
				Required:    false,
				Default:     "",
			},
			"exclude_tests": {
				Type:        ParamTypeBool,
				Description: "Exclude symbols from test and documentation files (default: true)",
				Required:    false,
				Default:     true,
			},
			"reverse": {
				Type:        ParamTypeBool,
				Description: "Return lowest-ranked symbols first (for finding peripheral/least important functions). Default: false (highest first).",
				Required:    false,
				Default:     false,
			},
		},
		Category:    CategoryExploration,
		Priority:    89,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
	}
}

// Execute runs the find_important tool.
func (t *findImportantTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
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
	ctx, span := findImportantTracer.Start(ctx, "findImportantTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_important"),
			attribute.Int("top", p.Top),
			attribute.String("kind", p.Kind),
			attribute.String("package", p.Package),
			attribute.Bool("exclude_tests", p.ExcludeTests),
			attribute.Bool("reverse", p.Reverse),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Adaptive request size based on filters.
	// F-5: When filtering by kind or excluding tests/docs, request 10x to ensure
	// enough results survive. PageRank computes scores for ALL nodes anyway —
	// the only cost of requesting more is iterating a larger slice in the filter loop.
	// Previous 5x multiplier was insufficient for projects like NestJS where
	// Phase 4 reclassifies 576 integration/ files as non-production.
	hasFilters := p.Kind != "all" || p.ExcludeTests || p.Package != ""
	requestCount := p.Top
	if hasFilters {
		requestCount = p.Top * 10 // Request 10x when filtering (test+doc+kind)
		t.logger.Debug("pagerank request adjusted for filtering",
			slog.String("tool", "find_important"),
			slog.String("kind_filter", p.Kind),
			slog.Bool("exclude_tests", p.ExcludeTests),
			slog.Int("top_requested", p.Top),
			slog.Int("request_count", requestCount),
		)
	}

	// Get PageRank results using CRS-enabled method for tracing
	pageRankNodes, traceStep := t.analytics.PageRankTopWithCRS(ctx, requestCount, nil)

	span.SetAttributes(
		attribute.Int("raw_results", len(pageRankNodes)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Phase 1: Filter out test and documentation files
	var sourceOnly []graph.PageRankNode
	if p.ExcludeTests {
		filteredCount := 0
		for _, prn := range pageRankNodes {
			if prn.Node == nil || prn.Node.Symbol == nil {
				continue
			}
			filePath := prn.Node.Symbol.FilePath
			// GR-60: Use graph-based file classification instead of heuristics
			isProd := t.analytics.IsProductionFile(filePath)
			// F-3: Safety net — _test.go is NEVER production regardless of classification.
			// Defense-in-depth for cases where graph classification misses a test file
			// (e.g., file path normalization issues).
			if isProd && strings.HasSuffix(filepath.Base(filePath), "_test.go") {
				isProd = false
			}
			if !isProd {
				filteredCount++
				// GR-60c: Debug log for classification decisions on top results
				if filteredCount <= 10 {
					t.logger.Info("GR-60c: filtered non-production file from find_important",
						slog.String("file", filePath),
						slog.String("symbol", prn.Node.Symbol.Name),
						slog.Float64("pagerank", prn.Score),
					)
				}
				continue
			}
			sourceOnly = append(sourceOnly, prn)
		}
		if filteredCount > 0 {
			t.logger.Info("GR-60c: find_important filter summary",
				slog.Int("raw_results", len(pageRankNodes)),
				slog.Int("filtered_out", filteredCount),
				slog.Int("kept", len(sourceOnly)),
			)
		}
		if len(sourceOnly) > 0 {
			pageRankNodes = sourceOnly
		}
		// If ALL results are test/doc files, keep original to avoid empty results.
		span.SetAttributes(attribute.Int("after_test_doc_filter", len(sourceOnly)))
	}

	// Phase 2: Filter by package scope if specified.
	// IT-07: Uses matchesPackageScope() with 3 strategies (Package field, FilePath
	// segment, file stem) to match find_hotspots package filter capability.
	//
	// HISTORY (read before modifying):
	// - FIX-6 (original): Added this filter phase. Package param added to struct,
	//   Definition(), parseParams(). BUT never wired into execute_execution.go
	//   extractToolParameters() or convertMapToTypedParams(). So p.Package was
	//   ALWAYS "" and this block never executed. FIX-6 was incorrectly marked DONE.
	// - CR-11: Said "no matches = correct answer, return empty." Correct principle.
	// - FIX-B: Overrode CR-11 with fallback to global results, arguing the param
	//   extractor sends conceptual names ("materials") that can't match. But the
	//   real bug was upstream: Package was never populated. FIX-B was solving a
	//   phantom problem and broke CR-11's correct behavior.
	// - IT-Summary Round 2: Discovered Package was never wired. Fixed upstream in
	//   execute_execution.go. Restored CR-11 behavior here (no fallback).
	//
	// RULE: If the package filter returns 0 results, that IS the correct answer.
	// Do NOT fall back to global results — that silently gives wrong-scope data
	// to the LLM. If conceptual scope names ("materials", "write path") don't
	// match, the fix belongs in extractPackageContextFromQuery(), not here.
	if p.Package != "" {
		var packageFiltered []graph.PageRankNode
		for _, prn := range pageRankNodes {
			if prn.Node == nil || prn.Node.Symbol == nil {
				continue
			}
			if matchesPackageScope(prn.Node.Symbol, p.Package) {
				packageFiltered = append(packageFiltered, prn)
			}
		}
		t.logger.Info("IT-07: find_important package filter applied",
			slog.String("package", p.Package),
			slog.Int("before", len(pageRankNodes)),
			slog.Int("after", len(packageFiltered)),
		)
		span.SetAttributes(attribute.Int("after_package_filter", len(packageFiltered)))
		// CR-11: Unconditionally apply filter. Empty result = "no important
		// symbols found in that scope." Do NOT fall back to global results.
		pageRankNodes = packageFiltered
	}

	// Phase 3: Filter by kind if needed
	var filtered []graph.PageRankNode
	if p.Kind == "all" {
		filtered = pageRankNodes
	} else {
		for _, prn := range pageRankNodes {
			if prn.Node == nil || prn.Node.Symbol == nil {
				continue
			}
			if t.matchesKind(prn.Node.Symbol.Kind, p.Kind) {
				filtered = append(filtered, prn)
			}
		}
	}

	// IT-R2c Fix E: Reverse order for "lowest PageRank" / "peripheral" queries.
	// PageRank results arrive sorted descending. Reverse to ascending before trim.
	if p.Reverse && len(filtered) > 0 {
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
	}

	// Trim to requested count
	if len(filtered) > p.Top {
		filtered = filtered[:p.Top]
	}

	// Phase 4: Re-assign ranks sequentially after filtering (XC-4 fix).
	// Without this, kind="function" results show ranks 12, 15, 18... instead of 1, 2, 3...
	for i := range filtered {
		filtered[i].Rank = i + 1
	}

	span.SetAttributes(attribute.Int("filtered_results", len(filtered)))

	// F-5: Warn when filtering removes too many results — indicates the project
	// has a high test-to-production ratio and the multiplier may need further tuning.
	if len(pageRankNodes) > 0 && len(filtered) == 0 {
		t.logger.Warn("F-5: all PageRank results filtered — project may have very few production symbols",
			slog.String("tool", "find_important"),
			slog.Int("raw_count", len(pageRankNodes)),
			slog.String("kind_filter", p.Kind),
			slog.Bool("exclude_tests", p.ExcludeTests),
		)
	} else if len(filtered) < p.Top && hasFilters {
		t.logger.Warn("F-5: fewer results than requested after filtering",
			slog.String("tool", "find_important"),
			slog.Int("requested", p.Top),
			slog.Int("returned", len(filtered)),
			slog.Int("raw_count", len(pageRankNodes)),
			slog.String("kind_filter", p.Kind),
			slog.Bool("exclude_tests", p.ExcludeTests),
		)
	}

	// Build typed output
	output := t.buildOutput(filtered)

	// Format text output
	outputText := t.formatText(filtered, p.Package)

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
func (t *findImportantTool) parseParams(params map[string]any) (FindImportantParams, error) {
	p := FindImportantParams{
		Top:          10,
		Kind:         "all",
		ExcludeTests: true,
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_important"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_important"),
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
					slog.String("tool", "find_important"),
					slog.String("invalid_kind", kind),
				)
				kind = "all"
			}
			p.Kind = kind
		}
	}

	// Extract package (optional, default: "")
	// IT-07: Package scope filter for find_important.
	if pkgRaw, ok := params["package"]; ok {
		if pkg, ok := parseStringParam(pkgRaw); ok {
			p.Package = pkg
		}
	}

	// Extract exclude_tests (optional, default: true)
	if etRaw, ok := params["exclude_tests"]; ok {
		if et, ok := etRaw.(bool); ok {
			p.ExcludeTests = et
		}
	}

	// Extract reverse (optional, default: false)
	// IT-R2c Fix E: Support "lowest PageRank" / "peripheral" queries.
	if revRaw, ok := params["reverse"]; ok {
		if rev, ok := revRaw.(bool); ok {
			p.Reverse = rev
		}
	}

	return p, nil
}

// matchesKind checks if a symbol kind matches a filter string.
//
// IT-08c: Aligned with matchesKindFilter in symbol_resolution.go and
// matchesHotspotKind in tool_find_hotspots.go for cross-language consistency.
func (t *findImportantTool) matchesKind(kind ast.SymbolKind, filter string) bool {
	switch filter {
	case "function":
		return kind == ast.SymbolKindFunction ||
			kind == ast.SymbolKindMethod ||
			kind == ast.SymbolKindProperty // IT-08c: Python @property has callable body
	case "type":
		return kind == ast.SymbolKindType ||
			kind == ast.SymbolKindStruct ||
			kind == ast.SymbolKindInterface ||
			kind == ast.SymbolKindClass // IT-08c: JS/TS/Python classes
	default:
		return true
	}
}

// buildOutput creates the typed output struct.
func (t *findImportantTool) buildOutput(nodes []graph.PageRankNode) FindImportantOutput {
	results := make([]ImportantSymbol, 0, len(nodes))

	for _, prn := range nodes {
		if prn.Node == nil || prn.Node.Symbol == nil {
			continue
		}
		sym := prn.Node.Symbol
		results = append(results, ImportantSymbol{
			Rank:        prn.Rank,
			Name:        sym.Name,
			Kind:        sym.Kind.String(),
			File:        sym.FilePath,
			Line:        sym.StartLine,
			Package:     sym.Package,
			PageRank:    prn.Score,
			DegreeScore: prn.DegreeScore,
		})
	}

	return FindImportantOutput{
		ResultCount: len(results),
		Results:     results,
		Algorithm:   "PageRank",
	}
}

// formatText creates a human-readable text summary with graph markers.
//
// F-4: Output must include graph markers so that getSingleFormattedResult()
// can identify authoritative results and skip LLM synthesis:
//   - Zero results: "## GRAPH RESULT" header + "Do NOT use Grep" footer
//   - Positive results: "Found N" prefix + exhaustive footer + "Do NOT use Grep" footer
//
// This matches the pattern used by find_hotspots and find_dead_code.
func (t *findImportantTool) formatText(nodes []graph.PageRankNode, packageFilter string) string {
	var sb strings.Builder

	// CR-12: Include package scope in output header when filter is active.
	scopeLabel := ""
	if packageFilter != "" {
		scopeLabel = fmt.Sprintf(" in package '%s'", packageFilter)
	}

	if len(nodes) == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: No important symbols found%s\n\n", scopeLabel))
		if packageFilter != "" {
			sb.WriteString(fmt.Sprintf("No symbols matching package '%s' were found in the PageRank results.\n\n", packageFilter))
		} else {
			sb.WriteString("No symbols with PageRank score > 0 exist in the graph.\n\n")
		}
		sb.WriteString("---\n")
		sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
		sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d most important symbols%s (PageRank):\n\n", len(nodes), scopeLabel))

	for _, prn := range nodes {
		if prn.Node == nil || prn.Node.Symbol == nil {
			continue
		}
		sym := prn.Node.Symbol

		sb.WriteString(fmt.Sprintf("%d. %s (PageRank: %.6f)\n", prn.Rank, sym.Name, prn.Score))
		sb.WriteString(fmt.Sprintf("   %s:%d\n", sym.FilePath, sym.StartLine))
		sb.WriteString(fmt.Sprintf("   Degree score: %d (for comparison with find_hotspots)\n", prn.DegreeScore))
		if sym.Kind != ast.SymbolKindUnknown {
			sb.WriteString(fmt.Sprintf("   Kind: %s\n", sym.Kind))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}
