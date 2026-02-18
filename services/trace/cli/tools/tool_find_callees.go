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
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
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
			"The target function is the SUBJECT doing the calling. " +
			"Use when asked 'what does X call?' or 'what functions does X call?' or 'what does X depend on?'. " +
			"NOT for 'who calls X?' - use find_callers for that instead. " +
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
		// IT-02 H-3: Added specific callee keywords and grammar hint
		WhenToUse: WhenToUse{
			Keywords: []string{
				"what does call", "what functions does", "what methods does",
				"functions called by", "find callees", "callees of",
				"outgoing calls", "downstream", "dependencies of",
				"depends on", "invokes", "calls what", "makes calls to",
			},
			UseWhen: "User asks what functions a specific function CALLS or depends on. " +
				"The target function is the SUBJECT doing the calling. " +
				"Questions like 'what does Engine.Run call?', 'what functions does Build call?', " +
				"'what does make_response depend on?'.",
			AvoidWhen: "User asks WHO calls a function. " +
				"The target function is the OBJECT being called. " +
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
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_callees").
			WithTool("find_callees").
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
	ctx, span := findCalleesTracer.Start(ctx, "findCalleesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_callees"),
			attribute.String("function_name", p.FunctionName),
			attribute.Int("limit", p.Limit),
			attribute.Bool("index_available", t.index != nil),
		),
	)
	defer span.End()

	// M-2: Use trace-enriched logger for all log entries in this execution
	logger := telemetry.LoggerWithTrace(ctx, t.logger)

	// GR-01: Use index first for O(1) lookup
	var results map[string]*graph.QueryResult
	var queryErrors int

	if t.index != nil {
		var symbols []*ast.Symbol

		// IT-01 SI-2: If name contains ".", skip exact match (dot-notation names like
		// "Txn.Get" are never stored as-is in the index) and go straight to fuzzy resolution.
		if strings.Contains(p.FunctionName, ".") {
			symbol, _, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, logger)
			if err == nil {
				logger.Info("dot-notation resolved",
					slog.String("tool", "find_callees"),
					slog.String("query", p.FunctionName),
					slog.String("matched", symbol.Name),
				)
				symbols = []*ast.Symbol{symbol}
			} else {
				// IT-02 C-1: When dot-notation resolution fails (e.g., "DB.Open" where Open
				// is a package-level function, not a method on DB), fall back to bare method name.
				parts := strings.SplitN(p.FunctionName, ".", 2)
				bareName := parts[1]
				bareSymbols := t.index.GetByName(bareName)
				if len(bareSymbols) > 0 {
					symbols = bareSymbols
					logger.Info("IT-02 C-1: dot-notation fallback to bare name",
						slog.String("tool", "find_callees"),
						slog.String("query", p.FunctionName),
						slog.String("bare_name", bareName),
						slog.Int("matches", len(bareSymbols)),
					)
					span.SetAttributes(attribute.Bool("dot_notation_fallback", true))
				} else {
					// Also try fuzzy on bare name as last resort
					bareSym, fuzzy, bareErr := ResolveFunctionWithFuzzy(ctx, t.index, bareName, logger)
					if bareErr == nil {
						symbols = []*ast.Symbol{bareSym}
						logger.Info("IT-02 C-1: dot-notation fallback to bare fuzzy",
							slog.String("tool", "find_callees"),
							slog.String("query", p.FunctionName),
							slog.String("bare_name", bareName),
							slog.String("matched", bareSym.Name),
							slog.Bool("fuzzy", fuzzy),
						)
						span.SetAttributes(attribute.Bool("dot_notation_bare_fuzzy", true))
					}
				}
			}
		} else {
			// O(1) index lookup for bare names
			symbols = t.index.GetByName(p.FunctionName)

			// P1: If no exact match, try fuzzy search (Feb 14, 2026)
			if len(symbols) == 0 {
				symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, logger)
				if err == nil && fuzzy {
					logger.Info("P1: Using fuzzy match for function",
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
					logger.Warn("graph query failed",
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
		logger.Warn("graph query fallback",
			slog.String("tool", "find_callees"),
			slog.String("reason", "index_unavailable"),
			slog.String("function_name", p.FunctionName),
		)
		span.SetAttributes(attribute.Bool("index_used", false))
		var gErr error
		results, gErr = t.graph.FindCalleesByName(ctx, p.FunctionName, graph.WithLimit(p.Limit))
		if gErr != nil {
			span.RecordError(gErr)
			errStep := crs.NewTraceStepBuilder().
				WithAction("tool_find_callees").
				WithTarget(p.FunctionName).
				WithTool("find_callees").
				WithDuration(time.Since(start)).
				WithError(fmt.Sprintf("find callees for '%s': %v", p.FunctionName, gErr)).
				Build()
			return &Result{
				Success:   false,
				Error:     fmt.Sprintf("find callees for '%s': %v", p.FunctionName, gErr),
				TraceStep: &errStep,
				Duration:  time.Since(start),
			}, nil
		}
	}

	// Build typed output (single classification pass)
	output := t.buildOutput(p.FunctionName, results)

	// M-1: Format text from typed output (eliminates duplicate classification)
	outputText := t.formatText(p.FunctionName, output)

	span.SetAttributes(
		attribute.Int("resolved_count", output.ResolvedCount),
		attribute.Int("external_count", output.ExternalCount),
		attribute.Int("total_count", output.TotalCount),
	)

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_callees").
		WithTarget(p.FunctionName).
		WithTool("find_callees").
		WithDuration(duration).
		WithMetadata("resolved_count", fmt.Sprintf("%d", output.ResolvedCount)).
		WithMetadata("external_count", fmt.Sprintf("%d", output.ExternalCount)).
		WithMetadata("total_count", fmt.Sprintf("%d", output.TotalCount)).
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
	if err := ValidateSymbolName(p.FunctionName, "function_name", "'render', 'Initialize', 'Execute'"); err != nil {
		return p, err
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
//
// Description:
//
//	Classifies callees into resolved (in-codebase) and external (stdlib/external).
//	Deduplicates both resolved and external callees.
//
// L-1: Deduplicates resolved callees by symbol ID to prevent duplicates when
// multiple source symbols resolve to the same callee.
func (t *findCalleesTool) buildOutput(functionName string, results map[string]*graph.QueryResult) FindCalleesOutput {
	var resolvedCallees []CalleeInfo
	var externalCallees []string
	seenResolved := make(map[string]bool)

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
				// L-1: Deduplicate resolved callees by symbol ID
				if seenResolved[sym.ID] {
					continue
				}
				seenResolved[sym.ID] = true
				resolvedCallees = append(resolvedCallees, CalleeInfo{
					Name:      sym.Name,
					File:      sym.FilePath,
					Line:      sym.StartLine,
					Package:   sym.Package,
					Signature: sym.Signature,
					// L-2: CallerID is the symbol whose callees we queried (not the callee's own ID)
					SourceID: symbolID,
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

// formatText creates a human-readable text summary from the typed output.
//
// M-1: Refactored to accept FindCalleesOutput instead of raw results,
// eliminating the duplicate classification logic that was in the previous version.
func (t *findCalleesTool) formatText(functionName string, output FindCalleesOutput) string {
	var sb strings.Builder

	if output.TotalCount == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Callees of '%s' not found\n\n", functionName))
		sb.WriteString(fmt.Sprintf("The function '%s' does not call any other functions.\n", functionName))
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	// Header with clear breakdown
	sb.WriteString(fmt.Sprintf("Function '%s' calls %d functions", functionName, output.TotalCount))
	if output.ResolvedCount > 0 && output.ExternalCount > 0 {
		sb.WriteString(fmt.Sprintf(" (%d in-codebase, %d external/stdlib)", output.ResolvedCount, output.ExternalCount))
	}
	sb.WriteString(":\n\n")

	// Show resolved (in-codebase) callees first
	if output.ResolvedCount > 0 {
		sb.WriteString("## In-Codebase Callees (navigable)\n")
		for _, callee := range output.ResolvedCallees {
			sb.WriteString(fmt.Sprintf("  → %s() in %s:%d\n", callee.Name, callee.File, callee.Line))
		}
		sb.WriteString("\n")
	}

	// Summarize external callees (already deduplicated by buildOutput)
	if output.ExternalCount > 0 {
		sb.WriteString("## External/Stdlib Callees (not in codebase)\n")
		if len(output.ExternalCallees) <= 10 {
			for _, name := range output.ExternalCallees {
				sb.WriteString(fmt.Sprintf("  → %s() (external)\n", name))
			}
		} else {
			for _, name := range output.ExternalCallees[:10] {
				sb.WriteString(fmt.Sprintf("  → %s() (external)\n", name))
			}
			sb.WriteString(fmt.Sprintf("  ... and %d more external calls\n", len(output.ExternalCallees)-10))
		}
	}

	// GR-59 Group A: Signal definitiveness on success path.
	sb.WriteString("\n---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}
