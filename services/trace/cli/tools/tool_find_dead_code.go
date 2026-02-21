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
// find_dead_code Tool - Typed Implementation
// =============================================================================

var findDeadCodeTracer = otel.Tracer("tools.find_dead_code")

// FindDeadCodeParams contains the validated input parameters.
type FindDeadCodeParams struct {
	// IncludeExported includes exported symbols (default: false).
	IncludeExported bool

	// Package filters results to a specific package path.
	Package string

	// Limit is the maximum number of results to return.
	// Default: 50, Max: 500
	Limit int

	// ExcludeTests filters out symbols from test files (default: true).
	ExcludeTests bool
}

// ToolName returns the tool name for TypedParams interface.
func (p FindDeadCodeParams) ToolName() string { return "find_dead_code" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindDeadCodeParams) ToMap() map[string]any {
	m := map[string]any{
		"include_exported": p.IncludeExported,
		"limit":            p.Limit,
		"exclude_tests":    p.ExcludeTests,
	}
	if p.Package != "" {
		m["package"] = p.Package
	}
	return m
}

// FindDeadCodeOutput contains the structured result.
type FindDeadCodeOutput struct {
	// DeadCodeCount is the number of dead code symbols found.
	DeadCodeCount int `json:"dead_code_count"`

	// DeadCode is the list of potentially unused symbols.
	DeadCode []DeadCodeSymbol `json:"dead_code"`
}

// DeadCodeSymbol holds information about a potentially dead symbol.
type DeadCodeSymbol struct {
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

	// Exported indicates if the symbol is exported.
	Exported bool `json:"exported"`

	// Reason explains why the symbol is considered dead.
	Reason string `json:"reason"`
}

// findDeadCodeTool finds potentially unused code.
type findDeadCodeTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindDeadCodeTool creates the find_dead_code tool.
//
// Description:
//
//	Creates a tool that finds potentially unused code (symbols with no callers).
//	Automatically excludes entry points (main, init, Test*) and interface methods.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance for dead code detection. Must not be nil.
//   - idx: The symbol index for name lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_dead_code tool implementation.
//
// Limitations:
//
//   - Cannot detect usage via reflection or dynamic calls
//   - Exported symbols may be used by external packages (use include_exported=true carefully)
//   - Maximum 500 results per query
//   - Package filter matches exact package name or file path substring
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Entry points (main, init, Test*) are pre-filtered by analytics
func NewFindDeadCodeTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findDeadCodeTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findDeadCodeTool) Name() string {
	return "find_dead_code"
}

func (t *findDeadCodeTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findDeadCodeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_dead_code",
		Description: "Find potentially unused code (symbols with no callers). " +
			"Excludes entry points (main, init, Test*) and interface methods. " +
			"By default only shows unexported symbols; use include_exported=true for all.",
		Parameters: map[string]ParamDef{
			"include_exported": {
				Type:        ParamTypeBool,
				Description: "Include exported symbols (by default only unexported are shown)",
				Required:    false,
				Default:     false,
			},
			"package": {
				Type:        ParamTypeString,
				Description: "Filter to a specific package path",
				Required:    false,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of results to return",
				Required:    false,
				Default:     50,
			},
			"exclude_tests": {
				Type:        ParamTypeBool,
				Description: "Exclude symbols from test files",
				Required:    false,
				Default:     true,
			},
		},
		Category:    CategoryExploration,
		Priority:    84,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"dead code", "unused code", "unreferenced", "orphan code",
				"unused functions", "not called", "no callers", "no references",
				"no incoming calls", "no internal callers", "never called",
				"zero callers", "not referenced",
			},
			UseWhen: "User asks about dead code, unused functions, unreferenced symbols, " +
				"or wants to find code that is never called. IMPORTANT: Negated caller queries " +
				"like 'functions with no callers', 'no incoming calls', or 'never called' mean " +
				"dead code — use this tool, NOT find_callers.",
			AvoidWhen: "User asks about most connected or heavily used functions " +
				"(use find_hotspots). User asks about code complexity or structure " +
				"(use find_communities). User asks WHO calls a specific function " +
				"(use find_callers — but 'no callers' means dead code, not find_callers).",
		},
	}
}

// Execute runs the find_dead_code tool.
func (t *findDeadCodeTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
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
	ctx, span := findDeadCodeTracer.Start(ctx, "findDeadCodeTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_dead_code"),
			attribute.Bool("include_exported", p.IncludeExported),
			attribute.String("package_filter", p.Package),
			attribute.Int("limit", p.Limit),
			attribute.Bool("exclude_tests", p.ExcludeTests),
		),
	)
	defer span.End()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Get dead code
	deadCode, traceStep := t.analytics.DeadCodeWithCRS(ctx)

	span.SetAttributes(
		attribute.Int("raw_dead_code_count", len(deadCode)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Apply filters
	var filtered []graph.DeadCodeNode

	// Hoist lowerPkg outside the loop for efficiency
	lowerPkg := ""
	if p.Package != "" {
		lowerPkg = strings.ToLower(p.Package)
	}

	for _, dc := range deadCode {
		if dc.Node == nil || dc.Node.Symbol == nil {
			continue
		}
		sym := dc.Node.Symbol

		// Filter by exported status
		if !p.IncludeExported && sym.Exported {
			continue
		}

		// Filter by package (exact match OR file path substring)
		if p.Package != "" {
			if !strings.EqualFold(sym.Package, p.Package) &&
				!strings.Contains(strings.ToLower(sym.FilePath), lowerPkg) {
				continue
			}
		}

		filtered = append(filtered, dc)
	}

	// IT-08: Filter out test-file symbols
	if p.ExcludeTests {
		var nonTest []graph.DeadCodeNode
		for _, dc := range filtered {
			if dc.Node != nil && dc.Node.Symbol != nil && !isTestFile(dc.Node.Symbol.FilePath) {
				nonTest = append(nonTest, dc)
			}
		}
		if len(nonTest) > 0 {
			filtered = nonTest
		}
		// If ALL results are from test files, keep them rather than returning empty.
	}

	// Apply limit after all filters
	if len(filtered) > p.Limit {
		filtered = filtered[:p.Limit]
	}

	span.SetAttributes(attribute.Int("filtered_count", len(filtered)))

	// Structured logging for edge cases
	if len(deadCode) > 0 && len(filtered) == 0 {
		t.logger.Debug("all dead code filtered out",
			slog.String("tool", "find_dead_code"),
			slog.Int("raw_count", len(deadCode)),
			slog.Bool("include_exported", p.IncludeExported),
			slog.String("package_filter", p.Package),
			slog.Bool("exclude_tests", p.ExcludeTests),
		)
	} else if len(filtered) >= p.Limit {
		t.logger.Debug("dead code results limited",
			slog.String("tool", "find_dead_code"),
			slog.Int("raw_count", len(deadCode)),
			slog.Int("limit", p.Limit),
			slog.Int("returned", len(filtered)),
		)
	}

	// Build typed output
	output := t.buildOutput(filtered)

	// Format text output
	outputText := t.formatText(filtered, len(deadCode))

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
func (t *findDeadCodeTool) parseParams(params map[string]any) (FindDeadCodeParams, error) {
	p := FindDeadCodeParams{
		IncludeExported: false,
		Package:         "",
		Limit:           50,
		ExcludeTests:    true,
	}

	// Extract include_exported (optional)
	if includeExportedRaw, ok := params["include_exported"]; ok {
		if includeExported, ok := parseBoolParam(includeExportedRaw); ok {
			p.IncludeExported = includeExported
		}
	}

	// Extract package (optional)
	if packageRaw, ok := params["package"]; ok {
		if pkg, ok := parseStringParam(packageRaw); ok {
			p.Package = pkg
		}
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok {
			if limit < 1 {
				t.logger.Debug("limit below minimum, clamping to 1",
					slog.String("tool", "find_dead_code"),
					slog.Int("requested", limit),
				)
				limit = 1
			} else if limit > 500 {
				t.logger.Debug("limit above maximum, clamping to 500",
					slog.String("tool", "find_dead_code"),
					slog.Int("requested", limit),
				)
				limit = 500
			}
			p.Limit = limit
		}
	}

	// Extract exclude_tests (optional, default: true)
	if etRaw, ok := params["exclude_tests"]; ok {
		if et, ok := parseBoolParam(etRaw); ok {
			p.ExcludeTests = et
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findDeadCodeTool) buildOutput(deadCode []graph.DeadCodeNode) FindDeadCodeOutput {
	results := make([]DeadCodeSymbol, 0, len(deadCode))

	for _, dc := range deadCode {
		if dc.Node == nil || dc.Node.Symbol == nil {
			continue
		}
		sym := dc.Node.Symbol
		results = append(results, DeadCodeSymbol{
			Name:     sym.Name,
			Kind:     sym.Kind.String(),
			File:     sym.FilePath,
			Line:     sym.StartLine,
			Package:  sym.Package,
			Exported: sym.Exported,
			Reason:   dc.Reason,
		})
	}

	return FindDeadCodeOutput{
		DeadCodeCount: len(results),
		DeadCode:      results,
	}
}

// formatText creates a human-readable text summary with GR-59 graph markers.
//
// IT-08: Output must include graph markers so that getSingleFormattedResult()
// can identify authoritative results and skip LLM synthesis:
//   - Zero results: "## GRAPH RESULT" header + "Do NOT use Grep" footer
//   - Positive results: "Found N" prefix + exhaustive footer + "Do NOT use Grep" footer
func (t *findDeadCodeTool) formatText(deadCode []graph.DeadCodeNode, totalCount int) string {
	var sb strings.Builder

	if len(deadCode) == 0 {
		sb.WriteString("## GRAPH RESULT: No dead code found\n\n")
		sb.WriteString("No potentially dead code symbols exist in the graph.\n\n")
		sb.WriteString("---\n")
		sb.WriteString("The graph has been fully indexed \u2014 these results are exhaustive.\n")
		sb.WriteString("**Do NOT use Grep or Read to verify** \u2014 the graph already analyzed all source files.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d potentially dead code symbols", len(deadCode)))
	if len(deadCode) < totalCount {
		sb.WriteString(fmt.Sprintf(" (showing %d of %d total)", len(deadCode), totalCount))
	}
	sb.WriteString(":\n\n")

	for i, dc := range deadCode {
		if dc.Node == nil || dc.Node.Symbol == nil {
			continue
		}
		sym := dc.Node.Symbol
		sb.WriteString(fmt.Sprintf("%d. %s (%s)\n", i+1, sym.Name, sym.Kind))
		sb.WriteString(fmt.Sprintf("   %s:%d\n", sym.FilePath, sym.StartLine))
		sb.WriteString(fmt.Sprintf("   Reason: %s\n", dc.Reason))
		sb.WriteString("\n")
	}

	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed \u2014 these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** \u2014 the graph already analyzed all source files.\n")

	return sb.String()
}
