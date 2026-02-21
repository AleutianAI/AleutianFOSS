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
	"sort"
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
// find_references Tool - Typed Implementation
// =============================================================================

var findReferencesTracer = otel.Tracer("tools.find_references")

// FindReferencesParams contains the validated input parameters.
type FindReferencesParams struct {
	// SymbolName is the name of the symbol to find references for.
	SymbolName string

	// Limit is the maximum number of references to return.
	// Default: 100
	Limit int

	// PackageHint is an optional package/module context extracted from the query.
	// IT-06c: Used to disambiguate when multiple symbols share the same name
	// during ResolveFunctionWithFuzzy exact-match phase.
	PackageHint string
}

// ToolName returns the tool name for TypedParams interface.
func (p FindReferencesParams) ToolName() string { return "find_references" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindReferencesParams) ToMap() map[string]any {
	m := map[string]any{
		"symbol_name": p.SymbolName,
		"limit":       p.Limit,
	}
	if p.PackageHint != "" {
		m["package_hint"] = p.PackageHint
	}
	return m
}

// FindReferencesOutput contains the structured result.
type FindReferencesOutput struct {
	// SymbolName is the symbol that was searched for.
	SymbolName string `json:"symbol_name"`

	// ResolvedName is the actual symbol name after resolution (may differ from
	// SymbolName if fuzzy matching was used).
	ResolvedName string `json:"resolved_name,omitempty"`

	// DefinedAt is where the resolved symbol is defined (file:line).
	DefinedAt string `json:"defined_at,omitempty"`

	// SymbolKind is the kind of the resolved symbol (function, struct, interface, etc.).
	SymbolKind string `json:"symbol_kind,omitempty"`

	// ReferenceCount is the number of references found.
	ReferenceCount int `json:"reference_count"`

	// References is the list of reference locations.
	References []ReferenceInfo `json:"references"`
}

// ReferenceInfo holds information about a reference location.
type ReferenceInfo struct {
	// SymbolID is the symbol ID being referenced.
	SymbolID string `json:"symbol_id"`

	// Package is the package containing the reference.
	Package string `json:"package"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Column is the column number.
	Column int `json:"column"`
}

// findReferencesTool wraps graph.FindReferencesByID.
//
// Description:
//
//	Finds all references to a symbol (function, type, variable).
//	Returns all locations where the symbol is used, not just calls.
//	Uses shared ResolveFunctionWithFuzzy for symbol resolution (IT-06, IT-00a).
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findReferencesTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindReferencesTool creates the find_references tool.
//
// Description:
//
//	Creates a tool that finds all references to a symbol.
//	Useful for refactoring and understanding symbol usage.
//	Supports exact match, dot-notation (Type.Method), and fuzzy resolution
//	via the shared ResolveFunctionWithFuzzy pipeline.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_references tool implementation.
//
// Limitations:
//
//   - Resolves to a single best-match symbol (not all symbols of the same name)
//   - References are code locations based on graph edges, not semantic relationships
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Reference locations are indexed
func NewFindReferencesTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findReferencesTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findReferencesTool) Name() string {
	return "find_references"
}

func (t *findReferencesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findReferencesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_references",
		Description: "Find all references to a symbol (function, type, variable). " +
			"Returns all locations where the symbol is used, not just calls. " +
			"Useful for refactoring and understanding symbol usage.",
		Parameters: map[string]ParamDef{
			"symbol_name": {
				Type:        ParamTypeString,
				Description: "Name of the symbol to find references for (e.g., 'Entry', 'Config', 'Router', 'DB.Open')",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of references to return",
				Required:    false,
				Default:     100,
			},
		},
		Category:    CategoryExploration,
		Priority:    87,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// Execute runs the find_references tool.
//
// Description:
//
//	Resolves the symbol name via shared ResolveFunctionWithFuzzy (IT-06 Bug 1 fix),
//	then queries the graph for all incoming reference edges. Produces three distinct
//	output paths (IT-06 Bug 2 fix):
//	  - Symbol not found: informative "not found" message
//	  - Symbol found, 0 references: honest "exists but no edges" message
//	  - Symbol found, N references: reference list with definition location
//
//	All output paths include a definitive footer (IT-06 Bug 3 fix) and use the
//	"not found" pattern compatible with the synthesis gate (IT-06 Bug 4 fix).
//
// Inputs:
//   - ctx: Context for cancellation and timeout. Must not be nil.
//   - params: Tool parameters containing "symbol_name" (required) and "limit" (optional).
//
// Outputs:
//   - *Result: Tool result with OutputText for LLM consumption. Never nil on success.
//   - error: Non-nil only for infrastructure failures (context cancellation, graph errors).
//
// Thread Safety: This method is safe for concurrent use.
func (t *findReferencesTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_references").
			WithTool("find_references").
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
	ctx, span := findReferencesTracer.Start(ctx, "findReferencesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_references"),
			attribute.String("symbol_name", p.SymbolName),
			attribute.Int("limit", p.Limit),
		),
	)
	defer span.End()

	// IT-06 M-2: Use trace-enriched logger for all log entries in this execution
	logger := telemetry.LoggerWithTrace(ctx, t.logger)

	// IT-06 Bug 1: Use shared ResolveFunctionWithFuzzy instead of inline GetByName.
	// KindFilterAny because find_references operates on any symbol kind.
	//
	// IT-06c: When a package hint is available, try package-aware exact match first.
	// This prevents "Config" in a 20-project monorepo from resolving to the wrong
	// package's Config when the query says "in flask".
	var sym *ast.Symbol
	var fuzzy bool
	var resolveErr error

	if p.PackageHint != "" && t.index != nil {
		exactMatches := t.index.GetByName(p.SymbolName)
		if len(exactMatches) > 1 {
			filtered := filterByPackageHint(exactMatches, p.PackageHint, logger, "find_references")
			if len(filtered) < len(exactMatches) {
				// Package hint narrowed the matches — pick the most significant
				sym = pickMostSignificantSymbol(filtered)
				logger.Info("IT-06c: find_references resolved via package hint",
					slog.String("symbol", p.SymbolName),
					slog.String("hint", p.PackageHint),
					slog.String("resolved", sym.Name),
					slog.String("file", sym.FilePath),
					slog.Int("before", len(exactMatches)),
					slog.Int("after", len(filtered)),
				)
			}
		}
	}

	// Fall back to standard resolution if package hint didn't narrow results
	if sym == nil {
		sym, fuzzy, resolveErr = ResolveFunctionWithFuzzy(ctx, t.index, p.SymbolName, logger,
			WithKindFilter(KindFilterAny))
	}

	if resolveErr != nil {
		// Path A: Symbol not found — return informative "not found" message
		span.SetAttributes(
			attribute.Bool("symbol_resolved", false),
			attribute.Int("reference_count", 0),
		)

		outputText := t.formatTextNotFound(p.SymbolName)
		output := FindReferencesOutput{
			SymbolName:     p.SymbolName,
			ReferenceCount: 0,
			References:     []ReferenceInfo{},
		}

		duration := time.Since(start)
		toolStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_references").
			WithTarget(p.SymbolName).
			WithTool("find_references").
			WithDuration(duration).
			WithMetadata("reference_count", "0").
			WithMetadata("symbol_resolved", "false").
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

	if fuzzy {
		logger.Info("find_references: fuzzy match",
			slog.String("query", p.SymbolName),
			slog.String("matched", sym.Name),
		)
	}

	span.SetAttributes(
		attribute.Bool("symbol_resolved", true),
		attribute.String("resolved_name", sym.Name),
		attribute.String("resolved_id", sym.ID),
		attribute.String("symbol_kind", sym.Kind.String()),
		attribute.Bool("fuzzy_match", fuzzy),
	)

	// Find all references to the resolved symbol.
	// IT-06 Bug 6: Fetch extra results to allow sorting by relevance before truncating.
	// Without this, graph insertion order determines results, causing test/benchmark files
	// (e.g., asv_bench/) to dominate when they're indexed before core modules.
	fetchLimit := p.Limit * 3
	if fetchLimit < 100 {
		fetchLimit = 100
	}
	locations, gErr := t.graph.FindReferencesByID(ctx, sym.ID, graph.WithLimit(fetchLimit))
	if gErr != nil {
		return nil, fmt.Errorf("find references for '%s': %w", sym.Name, gErr)
	}

	// Build references from locations, deduplicating by file:line.
	// IT-06c M-9: The graph can return duplicate reference edges for the same location
	// (e.g., from multiple edge types). Dedup ensures clean output.
	var allReferences []ReferenceInfo
	seen := make(map[string]bool, len(locations))
	for _, loc := range locations {
		key := fmt.Sprintf("%s:%d", loc.FilePath, loc.StartLine)
		if seen[key] {
			continue
		}
		seen[key] = true
		allReferences = append(allReferences, ReferenceInfo{
			SymbolID: sym.ID,
			Package:  sym.Package,
			File:     loc.FilePath,
			Line:     loc.StartLine,
			Column:   loc.StartCol,
		})
	}

	// IT-06 Bug 6: Sort references — core source files before test/benchmark files.
	// This ensures the LLM sees the most relevant references first.
	sort.SliceStable(allReferences, func(i, j int) bool {
		iTest := isTestFile(allReferences[i].File)
		jTest := isTestFile(allReferences[j].File)
		if iTest != jTest {
			return !iTest // non-test files sort first
		}
		return false // preserve relative order within same category
	})

	// Truncate to requested limit after sorting
	if len(allReferences) > p.Limit {
		allReferences = allReferences[:p.Limit]
	}

	// IT-06 Change 3: Build output with definition location
	definedAt := fmt.Sprintf("%s:%d", sym.FilePath, sym.StartLine)
	output := FindReferencesOutput{
		SymbolName:     p.SymbolName,
		ResolvedName:   sym.Name,
		DefinedAt:      definedAt,
		SymbolKind:     sym.Kind.String(),
		ReferenceCount: len(allReferences),
		References:     allReferences,
	}

	// Format text output — Path B (0 refs) or Path C (N refs)
	outputText := t.formatText(p.SymbolName, sym, allReferences)

	span.SetAttributes(attribute.Int("reference_count", len(allReferences)))

	duration := time.Since(start)

	// IT-06 Change 4: Enhanced CRS TraceStep metadata
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_references").
		WithTarget(p.SymbolName).
		WithTool("find_references").
		WithDuration(duration).
		WithMetadata("reference_count", fmt.Sprintf("%d", len(allReferences))).
		WithMetadata("symbol_resolved", "true").
		WithMetadata("resolved_name", sym.Name).
		WithMetadata("symbol_kind", sym.Kind.String()).
		WithMetadata("fuzzy_match", fmt.Sprintf("%t", fuzzy)).
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
func (t *findReferencesTool) parseParams(params map[string]any) (FindReferencesParams, error) {
	p := FindReferencesParams{
		Limit: 100,
	}

	// Extract symbol_name (required)
	if nameRaw, ok := params["symbol_name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.SymbolName = name
		}
	}
	if err := ValidateSymbolName(p.SymbolName, "symbol_name", "'Session', 'app', 'Logger', 'DB.Open'"); err != nil {
		return p, err
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok && limit > 0 {
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

// formatTextNotFound creates output for Path A: symbol not found in the codebase.
//
// Description:
//
//	IT-06 Bug 2/4: Produces a distinct message when the symbol cannot be resolved,
//	using the "not found" pattern that the synthesis gate recognizes (GR-59 Rev 4b).
//	Includes a definitive footer (IT-06 Bug 3).
//
// Inputs:
//   - symbolName: The original user query string.
//
// Outputs:
//   - string: Formatted text output for LLM consumption.
//
// Thread Safety: This method is safe for concurrent use.
func (t *findReferencesTool) formatTextNotFound(symbolName string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n", symbolName))
	sb.WriteString(fmt.Sprintf("Symbol '%s' not found in the codebase.\n", symbolName))
	sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
	sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")

	return sb.String()
}

// formatText creates a human-readable text summary for resolved symbols.
//
// Description:
//
//	Handles two output paths:
//	  - Path B: Symbol resolved but 0 references (honest "exists but no edges" message)
//	  - Path C: Symbol resolved with N references (reference list with definition location)
//
//	Both paths include definition location and a definitive footer (IT-06 Bug 3).
//
// Inputs:
//   - symbolName: The original user query string.
//   - sym: The resolved symbol. Must not be nil.
//   - refs: The reference locations (may be empty).
//
// Outputs:
//   - string: Formatted text output for LLM consumption.
//
// Thread Safety: This method is safe for concurrent use.
func (t *findReferencesTool) formatText(symbolName string, sym *ast.Symbol, refs []ReferenceInfo) string {
	var sb strings.Builder

	definedAt := fmt.Sprintf("%s:%d", sym.FilePath, sym.StartLine)

	if len(refs) == 0 {
		// Path B: Symbol found, 0 references
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: References to '%s' not found\n\n", symbolName))
		sb.WriteString(fmt.Sprintf("Symbol defined at: %s (kind: %s, package: %s)\n\n", definedAt, sym.Kind.String(), sym.Package))
		sb.WriteString("The symbol exists in the codebase but has no incoming reference edges in the code graph.\n")
		sb.WriteString("This may mean the symbol is referenced via type annotations, imports, or patterns\n")
		sb.WriteString("that static analysis cannot track as edges.\n\n")
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	// Path C: Symbol found, N references
	if symbolName != sym.Name {
		sb.WriteString(fmt.Sprintf("Found %d references to '%s' (resolved from '%s'):\n", len(refs), sym.Name, symbolName))
	} else {
		sb.WriteString(fmt.Sprintf("Found %d references to '%s':\n", len(refs), sym.Name))
	}
	sb.WriteString(fmt.Sprintf("Defined at: %s (kind: %s, package: %s)\n\n", definedAt, sym.Kind.String(), sym.Package))

	for _, ref := range refs {
		if ref.Package != "" {
			sb.WriteString(fmt.Sprintf("• %s:%d:%d    (package: %s)\n", ref.File, ref.Line, ref.Column, ref.Package))
		} else {
			sb.WriteString(fmt.Sprintf("• %s:%d:%d\n", ref.File, ref.Line, ref.Column))
		}
	}

	// GR-59 Group A: Signal definitiveness on success path.
	sb.WriteString("\n---\n")
	sb.WriteString("The graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}

// referenceLocation is kept for backward compatibility with existing code.
// New code should use ReferenceInfo instead.
type referenceLocation struct {
	SymbolID   string
	SymbolName string
	Package    string
	Location   ast.Location
}
