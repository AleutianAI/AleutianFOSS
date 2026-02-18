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
			"The target function is the OBJECT being called. " +
			"Use when asked 'who calls X?' or 'find usages of X' or 'where is X called from?'. " +
			"NOT for 'what does X call?' - use find_callees for that instead. " +
			"Returns the list of callers with file locations and signatures.",
		Parameters: map[string]ParamDef{
			"function_name": {
				Type:        ParamTypeString,
				Description: "Name of the function to find callers for. Use Type.Method format for methods (e.g., 'Context.JSON', 'DB.Open', 'Txn.Get'). For standalone functions, just the name (e.g., 'parseConfig', 'Publish').",
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
		// IT-02 H-3: Removed ambiguous "what calls" keyword, added grammar hint
		WhenToUse: WhenToUse{
			Keywords: []string{
				"who calls", "find callers", "callers of",
				"usages of", "incoming calls", "upstream", "called from",
				"references to", "uses of", "invocations of",
				"where is it called", "find references",
			},
			UseWhen: "User asks WHO or WHAT calls a specific function. " +
				"The target function is the OBJECT being called. " +
				"Questions like 'who calls parseConfig?', 'find usages of Process', " +
				"'where is Open called from?', 'callers of X'.",
			AvoidWhen: "User asks what a function CALLS or depends on. " +
				"The target function is the SUBJECT doing the calling. " +
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
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_callers").
			WithTool("find_callers").
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
	var inheritanceResults map[string]*graph.InheritanceQueryResult
	var legacyResults map[string]*graph.QueryResult
	var queryErrors int
	usedInheritancePath := false

	if t.index != nil {
		var symbols []*ast.Symbol

		// IT-01 SI-2: If name contains ".", skip exact match (dot-notation names like
		// "Txn.Get" are never stored as-is in the index) and go straight to fuzzy resolution.
		if strings.Contains(p.FunctionName, ".") {
			symbol, _, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, t.logger)
			if err == nil {
				t.logger.Info("dot-notation resolved",
					slog.String("tool", "find_callers"),
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
						slog.String("tool", "find_callers"),
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
			usedInheritancePath = true
			inheritanceResults = make(map[string]*graph.InheritanceQueryResult, len(symbols))
			for _, sym := range symbols {
				if sym == nil {
					continue
				}
				if err := ctx.Err(); err != nil {
					span.RecordError(err)
					return nil, err
				}

				// IT-01 Bug 6: Check for parent class methods with same name
				// (inheritance-aware callers for overridden methods)
				parentMethodIDs := t.findParentMethodIDs(sym)
				if len(parentMethodIDs) > 0 {
					t.logger.Info("inheritance-aware caller search",
						slog.String("tool", "find_callers"),
						slog.String("symbol", sym.Name),
						slog.Int("parent_methods", len(parentMethodIDs)),
					)
				}

				result, qErr := t.graph.FindCallersWithInheritance(ctx, sym.ID, parentMethodIDs, graph.WithLimit(p.Limit))
				if qErr != nil {
					queryErrors++
					t.logger.Warn("graph query failed",
						slog.String("tool", "find_callers"),
						slog.String("operation", "FindCallersWithInheritance"),
						slog.String("symbol_id", sym.ID),
						slog.String("error", qErr.Error()),
					)
					continue
				}
				inheritanceResults[sym.ID] = result
			}
			if queryErrors > 0 {
				span.SetAttributes(attribute.Int("query_errors", queryErrors))
			}
		} else {
			span.SetAttributes(attribute.Bool("fast_not_found", true))
			usedInheritancePath = true
			inheritanceResults = make(map[string]*graph.InheritanceQueryResult)
		}
	} else {
		// Fallback to O(V) graph scan (no index, no inheritance info)
		t.logger.Warn("graph query fallback",
			slog.String("tool", "find_callers"),
			slog.String("reason", "index_unavailable"),
			slog.String("function_name", p.FunctionName),
		)
		span.SetAttributes(attribute.Bool("index_used", false))
		var gErr error
		legacyResults, gErr = t.graph.FindCallersByName(ctx, p.FunctionName, graph.WithLimit(p.Limit))
		if gErr != nil {
			span.RecordError(gErr)
			errStep := crs.NewTraceStepBuilder().
				WithAction("tool_find_callers").
				WithTarget(p.FunctionName).
				WithTool("find_callers").
				WithDuration(time.Since(start)).
				WithError(fmt.Sprintf("find callers for '%s': %v", p.FunctionName, gErr)).
				Build()
			return &Result{
				Success:   false,
				Error:     fmt.Sprintf("find callers for '%s': %v", p.FunctionName, gErr),
				TraceStep: &errStep,
				Duration:  time.Since(start),
			}, nil
		}
	}

	// Build typed output and format text
	var output FindCallersOutput
	var outputText string

	if usedInheritancePath {
		output = t.buildOutputFromInheritance(p.FunctionName, inheritanceResults)
		outputText = t.formatTextWithInheritance(p.FunctionName, inheritanceResults)
	} else {
		output = t.buildOutput(p.FunctionName, legacyResults)
		outputText = t.formatText(p.FunctionName, legacyResults)
	}

	span.SetAttributes(
		attribute.Int("match_count", output.MatchCount),
		attribute.Int("total_callers", output.TotalCallers),
	)

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_callers").
		WithTarget(p.FunctionName).
		WithTool("find_callers").
		WithDuration(duration).
		WithMetadata("match_count", fmt.Sprintf("%d", output.MatchCount)).
		WithMetadata("total_callers", fmt.Sprintf("%d", output.TotalCallers)).
		WithMetadata("used_inheritance_path", fmt.Sprintf("%v", usedInheritancePath)).
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
	if err := ValidateSymbolName(p.FunctionName, "function_name", "'handleRequest', 'Serve', 'Parse'"); err != nil {
		return p, err
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

// findParentMethodIDs finds IDs of same-named methods on parent classes in the inheritance chain.
//
// Description:
//
//	For a method like Plot.renderImmediately, walks up the inheritance chain
//	(Plot → Component → ...) and finds renderImmediately on each parent class.
//	This enables finding callers through parent class references (e.g.,
//	this.renderImmediately() in Component's methods).
//
// Inputs:
//
//	sym - The resolved method symbol.
//
// Outputs:
//
//	[]string - Symbol IDs of same-named methods on parent classes. Empty if no parents.
func (t *findCallersTool) findParentMethodIDs(sym *ast.Symbol) []string {
	if t.index == nil || sym == nil {
		return nil
	}

	// Only relevant for methods
	if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
		return nil
	}

	// Find the owning class name
	ownerClassName := ""
	if sym.Receiver != "" {
		ownerClassName = sym.Receiver
	} else {
		// For Python/TS: look for a class that has this method as a child
		allByName := t.index.GetByName(sym.Name)
		for _, candidate := range allByName {
			if candidate.ID == sym.ID {
				continue
			}
			// This method's owner is found by checking which classes have it as a child
			// We need to find the class that owns our method — check all classes
		}
		// Alternative: search for classes by checking all structs/classes
		// For now, use the method name to find siblings
	}

	if ownerClassName == "" {
		return nil
	}

	// Find the owning class symbol to check Extends
	classSymbols := t.index.GetByName(ownerClassName)
	var ownerClass *ast.Symbol
	for _, cs := range classSymbols {
		if cs.Kind == ast.SymbolKindClass || cs.Kind == ast.SymbolKindStruct {
			ownerClass = cs
			break
		}
	}
	if ownerClass == nil || ownerClass.Metadata == nil || ownerClass.Metadata.Extends == "" {
		return nil
	}

	// Walk the inheritance chain and collect same-named methods
	var parentMethodIDs []string
	currentParentName := ownerClass.Metadata.Extends

	for depth := 0; depth < 10; depth++ {
		if currentParentName == "" {
			break
		}

		// Find the parent class
		parentClasses := t.index.GetByName(currentParentName)
		var parentClass *ast.Symbol
		for _, pc := range parentClasses {
			if pc.Kind == ast.SymbolKindClass || pc.Kind == ast.SymbolKindStruct {
				parentClass = pc
				break
			}
		}
		if parentClass == nil {
			break
		}

		// Find same-named method in parent's children
		for _, child := range parentClass.Children {
			if child != nil && child.Name == sym.Name {
				parentMethodIDs = append(parentMethodIDs, child.ID)
				break
			}
		}

		// Also check by Receiver match (Go-style)
		allMethods := t.index.GetByName(sym.Name)
		for _, m := range allMethods {
			if m.Receiver == currentParentName && m.ID != sym.ID {
				// Avoid duplicates
				alreadyAdded := false
				for _, existing := range parentMethodIDs {
					if existing == m.ID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					parentMethodIDs = append(parentMethodIDs, m.ID)
				}
			}
		}

		// Continue up the chain
		if parentClass.Metadata != nil && parentClass.Metadata.Extends != "" {
			currentParentName = parentClass.Metadata.Extends
		} else {
			break
		}
	}

	return parentMethodIDs
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
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n", functionName))
			sb.WriteString(fmt.Sprintf("No function named '%s' exists in this codebase.\n", functionName))
		} else {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Callers of '%s' not found\n\n", functionName))
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

	// GR-59 Group A: Signal definitiveness on success path.
	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}

// buildOutputFromInheritance creates structured output from inheritance-aware results.
//
// Description:
//
//	Flattens InheritanceQueryResult into FindCallersOutput for JSON serialization.
//	Merges direct and inherited callers into a single flat list per symbol.
//
// Inputs:
//   - functionName: The queried function name.
//   - results: Map of symbol ID to InheritanceQueryResult.
//
// Outputs:
//   - FindCallersOutput: Structured output with all callers merged.
func (t *findCallersTool) buildOutputFromInheritance(functionName string, results map[string]*graph.InheritanceQueryResult) FindCallersOutput {
	output := FindCallersOutput{
		FunctionName: functionName,
		MatchCount:   len(results),
		Results:      make([]CallerResult, 0, len(results)),
	}

	for symbolID, inhResult := range results {
		if inhResult == nil {
			continue
		}

		allCallers := inhResult.AllCallers()
		cr := CallerResult{
			TargetID:    symbolID,
			CallerCount: len(allCallers.Symbols),
			Callers:     make([]CallerInfo, 0, len(allCallers.Symbols)),
		}

		for _, sym := range allCallers.Symbols {
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

// formatTextWithInheritance creates human-readable text that annotates inherited callers.
//
// Description:
//
//	Formats caller results distinguishing between direct callers (of the queried
//	method) and inherited callers (via parent class methods). Inherited callers
//	are annotated with the parent class name for clarity.
//
// Inputs:
//   - functionName: The queried function name.
//   - results: Map of symbol ID to InheritanceQueryResult.
//
// Outputs:
//   - string: Human-readable text with caller annotations.
func (t *findCallersTool) formatTextWithInheritance(functionName string, results map[string]*graph.InheritanceQueryResult) string {
	var sb strings.Builder

	totalCallers := 0
	for _, r := range results {
		if r != nil {
			totalCallers += r.TotalCallerCount()
		}
	}

	if totalCallers == 0 {
		if len(results) == 0 {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n", functionName))
			sb.WriteString(fmt.Sprintf("No function named '%s' exists in this codebase.\n", functionName))
		} else {
			sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Callers of '%s' not found\n\n", functionName))
			sb.WriteString(fmt.Sprintf("The function '%s' is not called anywhere (dead code or entry point).\n", functionName))
		}
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d callers of '%s':\n\n", totalCallers, functionName))

	for symbolID, inhResult := range results {
		if inhResult == nil {
			continue
		}

		// Direct callers
		if inhResult.DirectCallers != nil && len(inhResult.DirectCallers.Symbols) > 0 {
			sb.WriteString(fmt.Sprintf("Target: %s\n", symbolID))
			for _, sym := range inhResult.DirectCallers.Symbols {
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

		// Inherited callers — annotate with parent class name
		if len(inhResult.InheritedCallers) > 0 {
			for parentMethodID, parentResult := range inhResult.InheritedCallers {
				if parentResult == nil || len(parentResult.Symbols) == 0 {
					continue
				}

				// Derive a human-readable parent label from the parent method ID
				parentLabel := t.resolveParentLabel(parentMethodID)

				sb.WriteString(fmt.Sprintf("Inherited callers (via %s):\n", parentLabel))
				for _, sym := range parentResult.Symbols {
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
		}
	}

	// GR-59 Group A: Signal definitiveness on success path.
	sb.WriteString("---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}

// resolveParentLabel converts a parent method ID into a human-readable label.
//
// Description:
//
//	Given a symbol ID like "src/component.ts:50:renderImmediately", produces
//	a label like "Component.renderImmediately" by looking up the symbol in the
//	index. Falls back to the raw ID if the symbol is not found.
//
// Inputs:
//   - parentMethodID: The symbol ID of the parent method.
//
// Outputs:
//   - string: Human-readable label like "Component.renderImmediately".
func (t *findCallersTool) resolveParentLabel(parentMethodID string) string {
	if t.index == nil {
		return parentMethodID
	}

	sym, ok := t.index.GetByID(parentMethodID)
	if !ok || sym == nil {
		return parentMethodID
	}

	if sym.Receiver != "" {
		return sym.Receiver + "." + sym.Name
	}

	return sym.Name
}
