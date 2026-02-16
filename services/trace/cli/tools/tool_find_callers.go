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
// find_callers Tool - Typed Implementation
// =============================================================================

var findCallersTracer = otel.Tracer("tools.find_callers")

// FindCallersParams contains the validated input parameters.
type FindCallersParams struct {
	// FunctionName is the name of the function to find callers for.
	FunctionName string

	// Limit is the maximum number of callers to return.
	// Default: 50, Max: 1000
	Limit int
}

// FindCallersOutput contains the structured result.
type FindCallersOutput struct {
	// FunctionName is the function that was searched for.
	FunctionName string `json:"function_name"`

	// MatchCount is the number of matching symbol IDs.
	MatchCount int `json:"match_count"`

	// TotalCallers is the total number of callers found.
	TotalCallers int `json:"total_callers"`

	// Results contains the callers grouped by target symbol ID.
	Results []CallerResult `json:"results"`
}

// CallerResult represents callers for a specific target symbol.
type CallerResult struct {
	// TargetID is the symbol ID being called.
	TargetID string `json:"target_id"`

	// CallerCount is the number of callers for this target.
	CallerCount int `json:"caller_count"`

	// Callers is the list of caller symbols.
	Callers []CallerInfo `json:"callers"`
}

// CallerInfo holds information about a caller.
type CallerInfo struct {
	// Name is the caller function name.
	Name string `json:"name"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Package is the package name.
	Package string `json:"package"`

	// Signature is the function signature.
	Signature string `json:"signature,omitempty"`
}

// findCallersTool wraps graph.FindCallersByName.
//
// Description:
//
//	Finds all functions that call a given function by name. This is essential
//	for understanding code dependencies and answering questions like
//	"Find all functions that call parseConfig".
//
// GR-01 Optimization:
//
//	Uses O(1) SymbolIndex lookup before falling back to O(V) graph scan.
//	When multiple symbols share the same name (e.g., "Setup" in different
//	packages), the limit parameter applies per symbol, not as a global ceiling.
//	Total results may be up to limit × number_of_matching_symbols.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findCallersTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindCallersTool creates the find_callers tool.
//
// Description:
//
//	Creates a tool that finds all functions that call a given function.
//	Uses O(1) index lookup when available, falls back to O(V) graph scan.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_callers tool implementation.
//
// Limitations:
//
//   - Symbol names must match exactly (no fuzzy matching)
//   - When multiple symbols share a name, limit applies per symbol
//   - Maximum 1000 callers per query
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Index is populated with all symbols
func NewFindCallersTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findCallersTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findCallersTool) Name() string {
	return "find_callers"
}

func (t *findCallersTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findCallersTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_callers",
		Description: "Find all functions that CALL a given function (upstream dependencies). " +
			"Use when asked 'who calls X?' or 'what calls X?' or 'find usages of X'. " +
			"NOT for 'what does X call' - use find_callees for that instead. " +
			"Returns the list of callers with file locations and signatures.",
		Parameters: map[string]ParamDef{
			"function_name": {
				Type:        ParamTypeString,
				Description: "Name of the function to find callers for (e.g., 'parseConfig', 'HandleRequest')",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of callers to return",
				Required:    false,
				Default:     50,
			},
		},
		Category:    CategoryExploration,
		Priority:    95, // High priority - direct answer to common questions
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"who calls", "what calls", "find callers", "callers of",
				"usages of", "incoming calls", "upstream", "called from",
				"references to", "uses of", "invocations of",
			},
			UseWhen: "User asks WHO or WHAT calls a specific function. " +
				"Questions like 'who calls X?', 'what calls X?', 'find usages of X', 'callers of X'.",
			AvoidWhen: "User asks what a function CALLS or what it depends on. " +
				"Questions like 'what does X call?', 'what functions does X call?'. " +
				"Use find_callees instead for those.",
		},
	}
}

// Execute runs the find_callers tool.
func (t *findCallersTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
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
	ctx, span := findCallersTracer.Start(ctx, "findCallersTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_callers"),
			attribute.String("function_name", p.FunctionName),
			attribute.Int("limit", p.Limit),
			attribute.Bool("index_available", t.index != nil),
		),
	)
	defer span.End()

	// GR-01: Use index first for O(1) lookup instead of O(V) graph scan
	var results map[string]*graph.QueryResult
	var queryErrors int

	if t.index != nil {
		// O(1) index lookup
		symbols := t.index.GetByName(p.FunctionName)

		// P1: If no exact match, try fuzzy search (Feb 14, 2026)
		if len(symbols) == 0 {
			symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, t.logger)
			if err == nil && fuzzy {
				t.logger.Info("P1: Using fuzzy match for function",
					slog.String("tool", "find_callers"),
					slog.String("query", p.FunctionName),
					slog.String("matched", symbol.Name),
				)
				symbols = []*ast.Symbol{symbol}
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
				result, qErr := t.graph.FindCallersByID(ctx, sym.ID, graph.WithLimit(p.Limit))
				if qErr != nil {
					queryErrors++
					t.logger.Warn("graph query failed",
						slog.String("tool", "find_callers"),
						slog.String("operation", "FindCallersByID"),
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
		// Fallback to O(V) graph scan
		t.logger.Warn("graph query fallback",
			slog.String("tool", "find_callers"),
			slog.String("reason", "index_unavailable"),
			slog.String("function_name", p.FunctionName),
		)
		span.SetAttributes(attribute.Bool("index_used", false))
		var gErr error
		results, gErr = t.graph.FindCallersByName(ctx, p.FunctionName, graph.WithLimit(p.Limit))
		if gErr != nil {
			span.RecordError(gErr)
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("find callers for '%s': %v", p.FunctionName, gErr),
			}, nil
		}
	}

	// Build typed output
	output := t.buildOutput(p.FunctionName, results)

	// Format text output
	outputText := t.formatText(p.FunctionName, results)

	span.SetAttributes(
		attribute.Int("match_count", len(results)),
		attribute.Int("total_callers", output.TotalCallers),
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
func (t *findCallersTool) parseParams(params map[string]any) (FindCallersParams, error) {
	p := FindCallersParams{
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
					slog.String("tool", "find_callers"),
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
func (t *findCallersTool) buildOutput(functionName string, results map[string]*graph.QueryResult) FindCallersOutput {
	output := FindCallersOutput{
		FunctionName: functionName,
		MatchCount:   len(results),
		Results:      make([]CallerResult, 0, len(results)),
	}

	for symbolID, result := range results {
		if result == nil {
			continue
		}

		cr := CallerResult{
			TargetID:    symbolID,
			CallerCount: len(result.Symbols),
			Callers:     make([]CallerInfo, 0, len(result.Symbols)),
		}

		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			cr.Callers = append(cr.Callers, CallerInfo{
				Name:      sym.Name,
				File:      sym.FilePath,
				Line:      sym.StartLine,
				Package:   sym.Package,
				Signature: sym.Signature,
			})
			output.TotalCallers++
		}
		output.Results = append(output.Results, cr)
	}

	return output
}

// formatText creates a human-readable text summary.
func (t *findCallersTool) formatText(functionName string, results map[string]*graph.QueryResult) string {
	var sb strings.Builder

	totalCallers := 0
	for _, r := range results {
		if r != nil {
			totalCallers += len(r.Symbols)
		}
	}

	if totalCallers == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: No callers of '%s'\n\n", functionName))
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("No function named '%s' exists in this codebase.\n", functionName))
		} else {
			sb.WriteString(fmt.Sprintf("The function '%s' is not called anywhere (dead code or entry point).\n", functionName))
		}
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d callers of '%s':\n\n", totalCallers, functionName))

	for symbolID, result := range results {
		if result == nil || len(result.Symbols) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("Target: %s\n", symbolID))
		for _, sym := range result.Symbols {
			if sym == nil {
				continue
			}
			sb.WriteString(fmt.Sprintf("  • %s() in %s:%d\n", sym.Name, sym.FilePath, sym.StartLine))
			if sym.Package != "" {
				sb.WriteString(fmt.Sprintf("    Package: %s\n", sym.Package))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
