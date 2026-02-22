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
}

// ToolName returns the tool name for TypedParams interface.
func (p FindImportantParams) ToolName() string { return "find_important" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindImportantParams) ToMap() map[string]any {
	return map[string]any{
		"top":  p.Top,
		"kind": p.Kind,
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
		requestCount = p.Top * 3 // Request 3x when filtering to ensure enough results
		t.logger.Debug("pagerank request adjusted for filtering",
			slog.String("tool", "find_important"),
			slog.String("kind_filter", p.Kind),
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

	// Filter by kind if needed
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

	// Trim to requested count
	if len(filtered) > p.Top {
		filtered = filtered[:p.Top]
	}

	span.SetAttributes(attribute.Int("filtered_results", len(filtered)))

	// Structured logging for edge cases
	if len(pageRankNodes) > 0 && len(filtered) == 0 {
		t.logger.Debug("all PageRank results filtered by kind",
			slog.String("tool", "find_important"),
			slog.Int("raw_count", len(pageRankNodes)),
			slog.String("kind_filter", p.Kind),
		)
	} else if len(filtered) < p.Top && p.Kind != "all" {
		t.logger.Debug("fewer results than requested after filtering",
			slog.String("tool", "find_important"),
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
func (t *findImportantTool) parseParams(params map[string]any) (FindImportantParams, error) {
	p := FindImportantParams{
		Top:  10,
		Kind: "all",
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

// formatText creates a human-readable text summary.
func (t *findImportantTool) formatText(nodes []graph.PageRankNode) string {
	var sb strings.Builder

	if len(nodes) == 0 {
		sb.WriteString("No important symbols found.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Top %d Most Important Symbols (PageRank):\n\n", len(nodes)))

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

	sb.WriteString("Note: PageRank considers caller importance, not just caller count.\n")

	return sb.String()
}
