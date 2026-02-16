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
// find_callees Tool - Typed Implementation
// =============================================================================

var findCalleesTracer = otel.Tracer("tools.find_callees")

// FindCalleesParams contains the validated input parameters.
type FindCalleesParams struct {
	// FunctionName is the name of the function to find callees for.
	FunctionName string

	// Limit is the maximum number of callees to return.
	// Default: 50, Max: 1000
	Limit int
}

// FindCalleesOutput contains the structured result.
type FindCalleesOutput struct {
	// FunctionName is the function that was searched for.
	FunctionName string `json:"function_name"`

	// ResolvedCount is the number of in-codebase callees.
	ResolvedCount int `json:"resolved_count"`

	// ExternalCount is the number of external/stdlib callees.
	ExternalCount int `json:"external_count"`

	// TotalCount is the total number of callees.
	TotalCount int `json:"total_count"`

	// ResolvedCallees are in-codebase callees with file locations.
	ResolvedCallees []CalleeInfo `json:"resolved_callees"`

	// ExternalCallees are external/stdlib callees (names only).
	ExternalCallees []string `json:"external_callees"`
}

// CalleeInfo holds information about an in-codebase callee.
type CalleeInfo struct {
	// Name is the callee function name.
	Name string `json:"name"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Package is the package name.
	Package string `json:"package"`

	// Signature is the function signature.
	Signature string `json:"signature,omitempty"`

	// SourceID is the ID of the caller symbol.
	SourceID string `json:"source_id"`
}

// findCalleesTool wraps graph.FindCalleesByName.
//
// Description:
//
//	Finds all functions called by a given function by name. Essential for
//	understanding dependencies and data flow.
//
// GR-01 Optimization:
//
//	Uses O(1) SymbolIndex lookup before falling back to O(V) graph scan.
//	When multiple symbols share the same name, the limit parameter applies
//	per symbol, not as a global ceiling.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findCalleesTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindCalleesTool creates the find_callees tool.
//
// Description:
//
//	Creates a tool that finds all functions called by a given function.
//	Separates in-codebase callees from external/stdlib callees.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_callees tool implementation.
//
// Limitations:
//
//   - Symbol names must match exactly (no fuzzy matching)
//   - External callees have no file location
//   - Maximum 1000 callees per query
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Index is populated with all symbols
func NewFindCalleesTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findCalleesTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findCalleesTool) Name() string {
	return "find_callees"
}

func (t *findCalleesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findCalleesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_callees",
		Description: "Find all functions that a given function CALLS (downstream dependencies). " +
			"Use when asked 'what does X call?' or 'what functions does X call?' or 'what does X depend on?'. " +
			"NOT for 'who calls X' - use find_callers for that instead. " +
			"Returns the list of called functions with file locations.",
		Parameters: map[string]ParamDef{
			"function_name": {
				Type:        ParamTypeString,
				Description: "Name of the function to find callees for. Use Type.Method format for methods (e.g., 'Context.JSON', 'DB.Open', 'Txn.Get'). For standalone functions, just the name (e.g., 'parseConfig').",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of callees to return",
				Required:    false,
				Default:     50,
			},
		},
		Category:    CategoryExploration,
		Priority:    94,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"what does call", "functions called by", "find callees", "callees of",
				"calls to", "outgoing calls", "downstream", "dependencies of",
				"what X calls", "what functions X calls",
			},
			UseWhen: "User asks what functions a specific function CALLS. " +
				"Questions like 'what does X call?', 'what functions does X call?', 'dependencies of X'.",
			AvoidWhen: "User asks WHO calls a function. " +
				"Questions like 'who calls X?', 'find usages of X', 'callers of X'. " +
				"Use find_callers instead for those.",
		},
	}
}

// Execute runs the find_callees tool.
func (t *findCalleesTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
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
	ctx, span := findCalleesTracer.Start(ctx, "findCalleesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_callees"),
			attribute.String("function_name", p.FunctionName),
			attribute.Int("limit", p.Limit),
			attribute.Bool("index_available", t.index != nil),
		),
	)
	defer span.End()

	// GR-01: Use index first for O(1) lookup
	var results map[string]*graph.QueryResult
	var queryErrors int

	if t.index != nil {
		var symbols []*ast.Symbol

		// IT-01 SI-2: If name contains ".", skip exact match (dot-notation names like
		// "Txn.Get" are never stored as-is in the index) and go straight to fuzzy resolution.
		if strings.Contains(p.FunctionName, ".") {
			symbol, _, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, t.logger)
			if err == nil {
				t.logger.Info("dot-notation resolved",
					slog.String("tool", "find_callees"),
					slog.String("query", p.FunctionName),
					slog.String("matched", symbol.Name),
				)
				symbols = []*ast.Symbol{symbol}
			}
		} else {
			// O(1) index lookup for bare names
			symbols = t.index.GetByName(p.FunctionName)

			// P1: If no exact match, try fuzzy search (Feb 14, 2026)
			if len(symbols) == 0 {
				symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, t.logger)
				if err == nil && fuzzy {
					t.logger.Info("P1: Using fuzzy match for function",
						slog.String("tool", "find_callees"),
						slog.String("query", p.FunctionName),
						slog.String("matched", symbol.Name),
					)
					symbols = []*ast.Symbol{symbol}
				}
			}
		}

		span.SetAttributes(
			attribute.Bool("index_used", true),
			attribute.Int("index_matches", len(symbols)),
		)

		if len(symbols) > 0 {
			results = make(map[string]*graph.QueryResult, len(symbols))
			for _, sym := range symbols {
				if sym == nil {
					continue
				}
				if err := ctx.Err(); err != nil {
					span.RecordError(err)
					return nil, err
				}
				result, qErr := t.graph.FindCalleesByID(ctx, sym.ID, graph.WithLimit(p.Limit))
				if qErr != nil {
					queryErrors++
					t.logger.Warn("graph query failed",
						slog.String("tool", "find_callees"),
						slog.String("operation", "FindCalleesByID"),
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
			slog.String("tool", "find_callees"),
			slog.String("reason", "index_unavailable"),
			slog.String("function_name", p.FunctionName),
		)
		span.SetAttributes(attribute.Bool("index_used", false))
		var gErr error
		results, gErr = t.graph.FindCalleesByName(ctx, p.FunctionName, graph.WithLimit(p.Limit))
		if gErr != nil {
			span.RecordError(gErr)
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("find callees for '%s': %v", p.FunctionName, gErr),
			}, nil
		}
	}

	// Build typed output
	output := t.buildOutput(p.FunctionName, results)

	// Format text output
	outputText := t.formatText(p.FunctionName, results)

	span.SetAttributes(
		attribute.Int("resolved_count", output.ResolvedCount),
		attribute.Int("external_count", output.ExternalCount),
		attribute.Int("total_count", output.TotalCount),
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
func (t *findCalleesTool) parseParams(params map[string]any) (FindCalleesParams, error) {
	p := FindCalleesParams{
		Limit: 50,
	}

	// Extract function_name (required)
	if nameRaw, ok := params["function_name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.FunctionName = name
		}
	}
	if p.FunctionName == "" {
		return p, fmt.Errorf("function_name is required")
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok {
			if limit < 1 {
				limit = 1
			} else if limit > 1000 {
				t.logger.Debug("limit above maximum, clamping to 1000",
					slog.String("tool", "find_callees"),
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
func (t *findCalleesTool) buildOutput(functionName string, results map[string]*graph.QueryResult) FindCalleesOutput {
	var resolvedCallees []CalleeInfo
	var externalCallees []string

	for symbolID, result := range results {
		if result == nil {
			continue
		}

		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			// External/placeholder symbols have empty FilePath or Kind=External
			if sym.FilePath == "" || sym.Kind == ast.SymbolKindExternal {
				externalCallees = append(externalCallees, sym.Name)
			} else {
				resolvedCallees = append(resolvedCallees, CalleeInfo{
					Name:      sym.Name,
					File:      sym.FilePath,
					Line:      sym.StartLine,
					Package:   sym.Package,
					Signature: sym.Signature,
					SourceID:  symbolID,
				})
			}
		}
	}

	// Deduplicate external callees
	seen := make(map[string]bool)
	var uniqueExternal []string
	for _, name := range externalCallees {
		if !seen[name] {
			seen[name] = true
			uniqueExternal = append(uniqueExternal, name)
		}
	}

	return FindCalleesOutput{
		FunctionName:    functionName,
		ResolvedCount:   len(resolvedCallees),
		ExternalCount:   len(uniqueExternal),
		TotalCount:      len(resolvedCallees) + len(uniqueExternal),
		ResolvedCallees: resolvedCallees,
		ExternalCallees: uniqueExternal,
	}
}

// formatText creates a human-readable text summary.
func (t *findCalleesTool) formatText(functionName string, results map[string]*graph.QueryResult) string {
	var sb strings.Builder

	// Separate resolved and external callees
	var resolvedCallees []struct {
		name     string
		file     string
		line     int
		sourceID string
	}
	var externalCallees []string

	for symbolID, result := range results {
		if result == nil {
			continue
		}
		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			if sym.FilePath == "" || sym.Kind == ast.SymbolKindExternal {
				externalCallees = append(externalCallees, sym.Name)
			} else {
				resolvedCallees = append(resolvedCallees, struct {
					name     string
					file     string
					line     int
					sourceID string
				}{
					name:     sym.Name,
					file:     sym.FilePath,
					line:     sym.StartLine,
					sourceID: symbolID,
				})
			}
		}
	}

	totalResolved := len(resolvedCallees)
	totalExternal := len(externalCallees)
	totalCallees := totalResolved + totalExternal

	if totalCallees == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: No callees of '%s'\n\n", functionName))
		sb.WriteString(fmt.Sprintf("The function '%s' does not call any other functions.\n", functionName))
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	// Header with clear breakdown
	sb.WriteString(fmt.Sprintf("Function '%s' calls %d functions", functionName, totalCallees))
	if totalResolved > 0 && totalExternal > 0 {
		sb.WriteString(fmt.Sprintf(" (%d in-codebase, %d external/stdlib)", totalResolved, totalExternal))
	}
	sb.WriteString(":\n\n")

	// Show resolved (in-codebase) callees first
	if totalResolved > 0 {
		sb.WriteString("## In-Codebase Callees (navigable)\n")
		for _, callee := range resolvedCallees {
			sb.WriteString(fmt.Sprintf("  → %s() in %s:%d\n", callee.name, callee.file, callee.line))
		}
		sb.WriteString("\n")
	}

	// Summarize external callees
	if totalExternal > 0 {
		sb.WriteString("## External/Stdlib Callees (not in codebase)\n")
		seen := make(map[string]bool)
		var uniqueExternal []string
		for _, name := range externalCallees {
			if !seen[name] {
				seen[name] = true
				uniqueExternal = append(uniqueExternal, name)
			}
		}
		if len(uniqueExternal) <= 10 {
			for _, name := range uniqueExternal {
				sb.WriteString(fmt.Sprintf("  → %s() (external)\n", name))
			}
		} else {
			for _, name := range uniqueExternal[:10] {
				sb.WriteString(fmt.Sprintf("  → %s() (external)\n", name))
			}
			sb.WriteString(fmt.Sprintf("  ... and %d more external calls\n", len(uniqueExternal)-10))
		}
	}

	return sb.String()
}
