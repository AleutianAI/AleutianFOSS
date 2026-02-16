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
//	Finds all types that implement a given interface by name. Essential for
//	understanding polymorphism and interface usage.
//
// GR-01 Optimization:
//
//	Uses O(1) SymbolIndex lookup before falling back to O(V) graph scan.
//	Only symbols with Kind=SymbolKindInterface are queried; other matching
//	names are filtered out with debug logging.
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
//	Creates a tool that finds all types implementing a given interface.
//	Filters to only interface symbols before querying.
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
//   - Only searches for interface symbols (non-interfaces filtered)
//   - Maximum 1000 implementations per query
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Interface→Implements edges are properly indexed
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
		Description: "Find all types that implement a given interface. " +
			"Essential for understanding polymorphism and interface usage. " +
			"Use this to find concrete implementations of an interface.",
		Parameters: map[string]ParamDef{
			"interface_name": {
				Type:        ParamTypeString,
				Description: "Name of the interface to find implementations for (e.g., 'Reader', 'Handler')",
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
		return &Result{
			Success: false,
			Error:   err.Error(),
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

	if t.index != nil {
		symbols := t.index.GetByName(p.InterfaceName)

		// Filter to only interface symbols
		var interfaces []*ast.Symbol
		var nonInterfaces int
		for _, sym := range symbols {
			if sym == nil {
				continue
			}
			if sym.Kind == ast.SymbolKindInterface {
				interfaces = append(interfaces, sym)
			} else {
				nonInterfaces++
			}
		}

		if nonInterfaces > 0 {
			t.logger.Debug("filtered non-interface symbols",
				slog.String("tool", "find_implementations"),
				slog.String("interface_name", p.InterfaceName),
				slog.Int("filtered_count", nonInterfaces),
				slog.Int("interfaces_found", len(interfaces)),
			)
		}

		span.SetAttributes(
			attribute.Bool("index_used", true),
			attribute.Int("index_matches", len(symbols)),
			attribute.Int("interfaces_found", len(interfaces)),
			attribute.Int("non_interfaces_filtered", nonInterfaces),
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
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("find implementations for '%s': %v", p.InterfaceName, gErr),
			}, nil
		}
	}

	// Build typed output
	output := t.buildOutput(p.InterfaceName, results)

	// Format text output
	outputText := t.formatText(p.InterfaceName, results)

	span.SetAttributes(
		attribute.Int("interface_count", len(results)),
		attribute.Int("total_implementations", output.TotalImplementations),
	)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		Duration:   time.Since(start),
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
	if p.InterfaceName == "" {
		return p, fmt.Errorf("interface_name is required")
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
func (t *findImplementationsTool) formatText(interfaceName string, results map[string]*graph.QueryResult) string {
	var sb strings.Builder

	totalImpls := 0
	for _, r := range results {
		if r != nil {
			totalImpls += len(r.Symbols)
		}
	}

	if totalImpls == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: No implementations of '%s'\n\n", interfaceName))
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("No interface named '%s' exists in this codebase.\n", interfaceName))
			sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
			sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		} else {
			sb.WriteString(fmt.Sprintf("The interface '%s' exists but has no implementing types.\n", interfaceName))
			sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
			sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		}
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d implementations of interface '%s':\n\n", totalImpls, interfaceName))

	for interfaceID, result := range results {
		if result == nil || len(result.Symbols) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("Interface: %s\n", interfaceID))
		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("  • %s (%s) in %s:%d\n", sym.Name, sym.Kind, sym.FilePath, sym.StartLine))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
