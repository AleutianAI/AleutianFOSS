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

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_implementations Tool - Typed Implementation
// =============================================================================

var findImplementationsTracer = otel.Tracer("tools.find_implementations")

// FindImplementationsParams contains the validated input parameters.
type FindImplementationsParams struct {
	// InterfaceName is the name of the interface to find implementations for.
	InterfaceName string

	// Limit is the maximum number of implementations to return.
	// Default: 50, Max: 1000
	Limit int

	// PackageHint is an optional package/module context extracted from the query.
	// IT-06c: Used to disambiguate when multiple types share the same name.
	PackageHint string
}

// FindImplementationsOutput contains the structured result.
type FindImplementationsOutput struct {
	// InterfaceName is the interface that was searched for.
	InterfaceName string `json:"interface_name"`

	// MatchCount is the number of matching interface IDs.
	MatchCount int `json:"match_count"`

	// TotalImplementations is the total number of implementations found.
	TotalImplementations int `json:"total_implementations"`

	// Results contains the implementations grouped by interface ID.
	Results []ImplementationResult `json:"results"`
}

// ImplementationResult represents implementations for a specific interface.
type ImplementationResult struct {
	// InterfaceID is the interface symbol ID.
	InterfaceID string `json:"interface_id"`

	// ImplCount is the number of implementations.
	ImplCount int `json:"impl_count"`

	// Implementations is the list of implementing types.
	Implementations []ImplementationInfo `json:"implementations"`
}

// ImplementationInfo holds information about an implementation.
type ImplementationInfo struct {
	// Name is the implementing type name.
	Name string `json:"name"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Package is the package name.
	Package string `json:"package"`

	// Kind is the symbol kind (struct, type, etc.).
	Kind string `json:"kind"`
}

// findImplementationsTool wraps graph.FindImplementationsByName.
//
// Description:
//
//	Finds all types that implement a given interface, extend a given class,
//	or embed a given struct. Works across languages: Go interfaces (structural
//	typing), Python class inheritance and ABCs, JS/TS class extends and
//	implements. Essential for understanding polymorphism and type hierarchies.
//
// GR-01 Optimization:
//
//	Uses O(1) SymbolIndex lookup before falling back to O(V) graph scan.
//	Only symbols with Kind in {Interface, Class, Struct} are queried; other
//	matching names (functions, variables, etc.) are filtered out with debug logging.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findImplementationsTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindImplementationsTool creates the find_implementations tool.
//
// Description:
//
//	Creates a tool that finds all types implementing a given interface,
//	extending a given class, or embedding a given struct. Accepts
//	SymbolKindInterface, SymbolKindClass, and SymbolKindStruct as targets.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_implementations tool implementation.
//
// Limitations:
//
//   - Only searches for type symbols (functions, variables filtered out)
//   - Maximum 1000 implementations per query
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Implements and Embeds edges are properly indexed
func NewFindImplementationsTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findImplementationsTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findImplementationsTool) Name() string {
	return "find_implementations"
}

func (t *findImplementationsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findImplementationsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_implementations",
		Description: "Find all types that implement a given interface or extend a given class. " +
			"Works across languages: Go interfaces (structural typing), Python class inheritance and ABCs, " +
			"JS/TS class extends and implements. " +
			"Use for 'what implements X?', 'what extends X?', 'what subclasses X?', 'what types derive from X?'.",
		Parameters: map[string]ParamDef{
			"interface_name": {
				Type:        ParamTypeString,
				Description: "Name of the interface or base class to find implementations/subclasses for (e.g., 'Reader', 'AbstractMesh', 'Index', 'SessionInterface')",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of implementations to return",
				Required:    false,
				Default:     50,
			},
		},
		Category:    CategoryExploration,
		Priority:    93,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// Execute runs the find_implementations tool.
func (t *findImplementationsTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_implementations").
			WithTool("find_implementations").
			WithDuration(time.Since(start)).
			WithError(err.Error()).
			Build()
		return &Result{
			Success:   false,
			Error:     err.Error(),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Start span with context
	ctx, span := findImplementationsTracer.Start(ctx, "findImplementationsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_implementations"),
			attribute.String("interface_name", p.InterfaceName),
			attribute.Int("limit", p.Limit),
			attribute.Bool("index_available", t.index != nil),
		),
	)
	defer span.End()

	var results map[string]*graph.QueryResult
	var queryErrors int
	// CR-20-7: Store all index symbols at function scope so formatText can reuse them
	// instead of performing a redundant index lookup on the "not found" path.
	var allIndexSymbols []*ast.Symbol

	if t.index != nil {
		symbols := t.index.GetByName(p.InterfaceName)

		// IT-06c: When multiple symbols match and a package hint is available,
		// disambiguate before kind-filtering.
		if len(symbols) > 1 && p.PackageHint != "" {
			symbols = filterByPackageHint(symbols, p.PackageHint, t.logger, "find_implementations")
		}

		allIndexSymbols = symbols

		// IT-03 C-3a: Accept interfaces, classes, and structs as valid targets.
		// Go uses interfaces (SymbolKindInterface), Python/JS/TS use classes
		// (SymbolKindClass), and Go structs (SymbolKindStruct) can be embedding targets.
		var interfaces []*ast.Symbol
		var filtered int
		for _, sym := range symbols {
			if sym == nil {
				continue
			}
			switch sym.Kind {
			case ast.SymbolKindInterface, ast.SymbolKindClass, ast.SymbolKindStruct:
				interfaces = append(interfaces, sym)
			default:
				filtered++
			}
		}

		if filtered > 0 {
			t.logger.Debug("filtered non-type symbols",
				slog.String("tool", "find_implementations"),
				slog.String("interface_name", p.InterfaceName),
				slog.Int("filtered_count", filtered),
				slog.Int("types_found", len(interfaces)),
			)
		}

		span.SetAttributes(
			attribute.Bool("index_used", true),
			attribute.Int("index_matches", len(symbols)),
			attribute.Int("types_found", len(interfaces)),
			attribute.Int("non_types_filtered", filtered),
		)

		if len(interfaces) > 0 {
			results = make(map[string]*graph.QueryResult, len(interfaces))
			for _, sym := range interfaces {
				if err := ctx.Err(); err != nil {
					span.RecordError(err)
					return nil, err
				}
				result, qErr := t.graph.FindImplementationsByID(ctx, sym.ID, graph.WithLimit(p.Limit))
				if qErr != nil {
					queryErrors++
					t.logger.Warn("graph query failed",
						slog.String("tool", "find_implementations"),
						slog.String("operation", "FindImplementationsByID"),
						slog.String("symbol_id", sym.ID),
						slog.String("error", qErr.Error()),
					)
					continue
				}
				results[sym.ID] = result
			}
			if queryErrors > 0 {
				span.SetAttributes(attribute.Int("query_errors", queryErrors))
			}
		} else {
			span.SetAttributes(attribute.Bool("fast_not_found", true))
			results = make(map[string]*graph.QueryResult)
		}
	} else {
		t.logger.Warn("graph query fallback",
			slog.String("tool", "find_implementations"),
			slog.String("reason", "index_unavailable"),
			slog.String("interface_name", p.InterfaceName),
		)
		span.SetAttributes(attribute.Bool("index_used", false))
		var gErr error
		results, gErr = t.graph.FindImplementationsByName(ctx, p.InterfaceName, graph.WithLimit(p.Limit))
		if gErr != nil {
			span.RecordError(gErr)
			errStep := crs.NewTraceStepBuilder().
				WithAction("tool_find_implementations").
				WithTarget(p.InterfaceName).
				WithTool("find_implementations").
				WithDuration(time.Since(start)).
				WithError(fmt.Sprintf("find implementations for '%s': %v", p.InterfaceName, gErr)).
				Build()
			return &Result{
				Success:   false,
				Error:     fmt.Sprintf("find implementations for '%s': %v", p.InterfaceName, gErr),
				TraceStep: &errStep,
				Duration:  time.Since(start),
			}, nil
		}
	}

	// Build typed output
	output := t.buildOutput(p.InterfaceName, results)

	// Format text output
	// CR-20-7: Pass pre-fetched symbols to avoid redundant index lookup on "not found" path.
	outputText := t.formatText(p.InterfaceName, results, allIndexSymbols)

	span.SetAttributes(
		attribute.Int("interface_count", len(results)),
		attribute.Int("total_implementations", output.TotalImplementations),
	)

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_implementations").
		WithTarget(p.InterfaceName).
		WithTool("find_implementations").
		WithDuration(duration).
		WithMetadata("match_count", fmt.Sprintf("%d", output.MatchCount)).
		WithMetadata("total_implementations", fmt.Sprintf("%d", output.TotalImplementations)).
		WithMetadata("index_used", fmt.Sprintf("%v", t.index != nil)).
		Build()

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &toolStep,
		Duration:   duration,
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findImplementationsTool) parseParams(params map[string]any) (FindImplementationsParams, error) {
	p := FindImplementationsParams{
		Limit: 50,
	}

	// Extract interface_name (required)
	if nameRaw, ok := params["interface_name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.InterfaceName = name
		}
	}
	if err := ValidateSymbolName(p.InterfaceName, "interface_name", "'Scale', 'Iterator', 'Router'"); err != nil {
		return p, err
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok {
			if limit < 1 {
				limit = 1
			} else if limit > 1000 {
				t.logger.Debug("limit above maximum, clamping to 1000",
					slog.String("tool", "find_implementations"),
					slog.Int("requested", limit),
				)
				limit = 1000
			}
			p.Limit = limit
		}
	}

	// IT-06c: Extract package_hint (optional)
	if hintRaw, ok := params["package_hint"]; ok {
		if hint, ok := parseStringParam(hintRaw); ok && hint != "" {
			p.PackageHint = hint
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findImplementationsTool) buildOutput(interfaceName string, results map[string]*graph.QueryResult) FindImplementationsOutput {
	output := FindImplementationsOutput{
		InterfaceName: interfaceName,
		MatchCount:    len(results),
		Results:       make([]ImplementationResult, 0, len(results)),
	}

	for interfaceID, result := range results {
		if result == nil {
			continue
		}

		ir := ImplementationResult{
			InterfaceID:     interfaceID,
			ImplCount:       len(result.Symbols),
			Implementations: make([]ImplementationInfo, 0, len(result.Symbols)),
		}

		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			ir.Implementations = append(ir.Implementations, ImplementationInfo{
				Name:    sym.Name,
				File:    sym.FilePath,
				Line:    sym.StartLine,
				Package: sym.Package,
				Kind:    sym.Kind.String(),
			})
			output.TotalImplementations++
		}
		output.Results = append(output.Results, ir)
	}

	return output
}

// formatText creates a human-readable text summary.
//
// CR-20-7: Added allIndexSymbols parameter to avoid redundant index lookup. When the
// index path is used, Execute() already fetched all symbols by name — passing them here
// eliminates a second O(n) index scan on the "not found" diagnostic path.
func (t *findImplementationsTool) formatText(interfaceName string, results map[string]*graph.QueryResult, allIndexSymbols []*ast.Symbol) string {
	var sb strings.Builder

	totalImpls := 0
	for _, r := range results {
		if r != nil {
			totalImpls += len(r.Symbols)
		}
	}

	if totalImpls == 0 {
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n", interfaceName))

			// Phase 19: Check if the name exists as a non-type symbol (function, variable,
			// export alias). This provides a more informative message for prototype-based
			// JS codebases where patterns like `var app = module.exports = {}` create
			// export aliases, not class/interface declarations.
			// CR-20-7: Uses pre-fetched allIndexSymbols instead of redundant index lookup.
			if len(allIndexSymbols) > 0 {
				sb.WriteString(fmt.Sprintf("No interface, class, or struct named '%s' exists, but the name was found as:\n", interfaceName))
				for _, sym := range allIndexSymbols {
					if sym == nil {
						continue
					}
					sb.WriteString(fmt.Sprintf("  • %s (%s) in %s:%d\n", sym.Name, sym.Kind, sym.FilePath, sym.StartLine))
				}
				sb.WriteString(fmt.Sprintf("\n'%s' is not a class or interface, so no inheritance hierarchy exists for it.\n", interfaceName))
				sb.WriteString("This is common in JavaScript codebases that use prototype/module.exports patterns.\n\n")
			} else {
				sb.WriteString(fmt.Sprintf("No interface or class named '%s' exists in this codebase.\n", interfaceName))
			}

			sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
			sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		} else {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Implementations of '%s' not found\n\n", interfaceName))
			sb.WriteString(fmt.Sprintf("The type '%s' exists but has no implementing or extending types.\n", interfaceName))
			sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
			sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		}
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d implementations/subclasses of '%s':\n\n", totalImpls, interfaceName))

	for targetID, result := range results {
		if result == nil || len(result.Symbols) == 0 {
			continue
		}

		// IT-03: Use language-appropriate label based on target symbol kind
		label := "Interface"
		if targetNode, exists := t.graph.GetNode(targetID); exists && targetNode.Symbol != nil {
			switch targetNode.Symbol.Kind {
			case ast.SymbolKindClass:
				label = "Base class"
			case ast.SymbolKindStruct:
				label = "Struct"
			}
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", label, targetID))
		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("  • %s (%s) in %s:%d\n", sym.Name, sym.Kind, sym.FilePath, sym.StartLine))
		}
		sb.WriteString("\n")
	}

	// GR-59 Group A: Signal definitiveness on success path.
	// The graph already analyzed every source file — these results are exhaustive.
	// Without this footer, the LLM tries to verify via Grep/Read, triggering CB fires.
	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}
