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
// find_symbol Tool - Typed Implementation
// =============================================================================

var findSymbolTracer = otel.Tracer("tools.find_symbol")

// FindSymbolParams contains the validated input parameters.
type FindSymbolParams struct {
	// Name is the symbol name to search for.
	Name string

	// Kind filters by symbol kind: function, type, interface, variable, constant, method, or all.
	Kind string

	// Package filters by package path (optional).
	Package string
}

// FindSymbolOutput contains the structured result.
type FindSymbolOutput struct {
	// SearchName is the name that was searched for.
	SearchName string `json:"search_name"`

	// MatchCount is the number of matching symbols.
	MatchCount int `json:"match_count"`

	// Symbols is the list of matching symbols.
	Symbols []SymbolInfo `json:"symbols"`
}

// SymbolInfo holds information about a symbol.
type SymbolInfo struct {
	// ID is the symbol's unique identifier.
	ID string `json:"id"`

	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind.
	Kind string `json:"kind"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number.
	Line int `json:"line"`

	// Package is the package name.
	Package string `json:"package"`

	// Signature is the function/method signature.
	Signature string `json:"signature,omitempty"`

	// Exported indicates if the symbol is exported.
	Exported bool `json:"exported"`
}

// findSymbolTool looks up symbols by name.
//
// Description:
//
//	Finds symbols (functions, types, variables, etc.) by name with optional
//	filtering by kind and package.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findSymbolTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindSymbolTool creates the find_symbol tool.
//
// Description:
//
//	Creates a tool that looks up symbols by name with optional filters.
//	Useful for resolving ambiguous names or finding definitions.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_symbol tool implementation.
//
// Limitations:
//
//   - Symbol names must match exactly (no fuzzy matching)
//   - Returns all matching symbols (may be multiple with same name)
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Index is populated with all symbols
func NewFindSymbolTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findSymbolTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findSymbolTool) Name() string {
	return "find_symbol"
}

func (t *findSymbolTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findSymbolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_symbol",
		Description: "Look up a symbol (function, type, variable) by name. " +
			"Returns symbol details including location, signature, and documentation. " +
			"Use this to resolve ambiguous names or find where something is defined.",
		Parameters: map[string]ParamDef{
			"name": {
				Type:        ParamTypeString,
				Description: "Name of the symbol to find",
				Required:    true,
			},
			"kind": {
				Type:        ParamTypeString,
				Description: "Filter by symbol kind: function, method, class, struct, interface, type, variable, constant, enum, decorator, property, field, component, or all",
				Required:    false,
				Default:     "all",
				Enum:        []any{"function", "method", "class", "struct", "interface", "type", "variable", "constant", "enum", "decorator", "property", "field", "component", "all"},
			},
			"package": {
				Type:        ParamTypeString,
				Description: "Filter by package path (optional)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    92,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// Execute runs the find_symbol tool.
func (t *findSymbolTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_symbol").
			WithTool("find_symbol").
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
	ctx, span := findSymbolTracer.Start(ctx, "findSymbolTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_symbol"),
			attribute.String("name", p.Name),
			attribute.String("kind", p.Kind),
			attribute.String("package", p.Package),
		),
	)
	defer span.End()

	// Use index to find symbols by name
	var matches []*ast.Symbol
	var usedFuzzy bool

	if t.index != nil {
		// Try exact match first (fast path)
		matches = t.index.GetByName(p.Name)

		// If no exact match, try fuzzy search (fallback)
		if len(matches) == 0 {
			searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			fuzzyMatches, err := t.index.Search(searchCtx, p.Name, 10)
			if err == nil && len(fuzzyMatches) > 0 {
				matches = fuzzyMatches
				usedFuzzy = true

				t.logger.Info("find_symbol: exact match failed, using fuzzy search",
					slog.String("query", p.Name),
					slog.Int("fuzzy_matches", len(fuzzyMatches)))
			}
		}
	}

	// Apply filters
	var filtered []*ast.Symbol
	for _, sym := range matches {
		if sym == nil {
			continue
		}

		// Filter by kind
		if p.Kind != "all" && !matchesSymbolKind(sym.Kind, p.Kind) {
			continue
		}

		// Filter by package
		if p.Package != "" && sym.Package != p.Package {
			continue
		}

		filtered = append(filtered, sym)
	}

	// Build typed output
	output := t.buildOutput(p.Name, filtered)

	// Format text output
	outputText := t.formatText(p.Name, filtered)

	// Add fuzzy match indicator if applicable
	if usedFuzzy && len(filtered) > 0 {
		outputText = "⚠️ No exact match found. Showing partial matches:\n\n" + outputText
	}

	span.SetAttributes(
		attribute.Int("match_count", len(filtered)),
		attribute.Bool("used_fuzzy", usedFuzzy),
	)

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_symbol").
		WithTarget(p.Name).
		WithTool("find_symbol").
		WithDuration(duration).
		WithMetadata("match_count", fmt.Sprintf("%d", len(filtered))).
		WithMetadata("used_fuzzy", fmt.Sprintf("%v", usedFuzzy)).
		WithMetadata("kind_filter", p.Kind).
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
func (t *findSymbolTool) parseParams(params map[string]any) (FindSymbolParams, error) {
	p := FindSymbolParams{
		Kind: "all",
	}

	// Extract name (required)
	if nameRaw, ok := params["name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.Name = name
		}
	}
	if err := ValidateSymbolName(p.Name, "name", "'Router', 'handleRequest', 'Config'"); err != nil {
		return p, err
	}

	// Extract kind (optional)
	if kindRaw, ok := params["kind"]; ok {
		if kind, ok := parseStringParam(kindRaw); ok && kind != "" {
			p.Kind = kind
		}
	}

	// Extract package (optional)
	if pkgRaw, ok := params["package"]; ok {
		if pkg, ok := parseStringParam(pkgRaw); ok {
			p.Package = pkg
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findSymbolTool) buildOutput(searchName string, symbols []*ast.Symbol) FindSymbolOutput {
	output := FindSymbolOutput{
		SearchName: searchName,
		MatchCount: len(symbols),
		Symbols:    make([]SymbolInfo, 0, len(symbols)),
	}

	for _, sym := range symbols {
		if sym == nil {
			continue
		}
		output.Symbols = append(output.Symbols, SymbolInfo{
			ID:        sym.ID,
			Name:      sym.Name,
			Kind:      sym.Kind.String(),
			File:      sym.FilePath,
			Line:      sym.StartLine,
			Package:   sym.Package,
			Signature: sym.Signature,
			Exported:  sym.Exported,
		})
	}

	return output
}

// formatText creates a human-readable text summary.
func (t *findSymbolTool) formatText(searchName string, symbols []*ast.Symbol) string {
	var sb strings.Builder

	if len(symbols) == 0 {
		sb.WriteString(fmt.Sprintf("No symbols found matching '%s'.\n", searchName))
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d symbols matching '%s':\n\n", len(symbols), searchName))

	for _, sym := range symbols {
		if sym == nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("• %s (%s)\n", sym.Name, sym.Kind))
		sb.WriteString(fmt.Sprintf("  Location: %s:%d\n", sym.FilePath, sym.StartLine))
		sb.WriteString(fmt.Sprintf("  Package: %s\n", sym.Package))
		if sym.Signature != "" {
			sb.WriteString(fmt.Sprintf("  Signature: %s\n", sym.Signature))
		}
		if sym.Exported {
			sb.WriteString("  Exported: yes\n")
		}
		sb.WriteString(fmt.Sprintf("  ID: %s\n", sym.ID))
		sb.WriteString("\n")
	}

	return sb.String()
}

// matchesSymbolKind checks if a symbol kind matches a filter string.
//
// IT-04 Audit: Comprehensive kind matching across Go, Python, TypeScript, JavaScript.
//   - "type" now matches Type, Struct, Class, AND Interface (all are type-level constructs)
//   - "class" matches Class (Python/JS/TS class definitions)
//   - "struct" matches Struct (Go struct definitions)
//   - Added "decorator", "property", "field", "component" filters
//   - "all" or any unrecognized value matches everything
func matchesSymbolKind(kind ast.SymbolKind, filter string) bool {
	switch filter {
	case "function":
		return kind == ast.SymbolKindFunction
	case "method":
		return kind == ast.SymbolKindMethod
	case "class":
		// IT-04: "class" matches both Class and Struct. The router may send "class"
		// for Go codebases where the actual kind is Struct, or vice versa.
		return kind == ast.SymbolKindClass || kind == ast.SymbolKindStruct
	case "struct":
		// IT-04: "struct" matches both Struct and Class. JS/Python/TS don't have structs;
		// the router (trained on Go) sends "struct" for constructor functions that the
		// JS parser classifies as SymbolKindClass.
		return kind == ast.SymbolKindStruct || kind == ast.SymbolKindClass
	case "type":
		// "type" is the broadest type-level filter: matches all type-defining constructs
		return kind == ast.SymbolKindType || kind == ast.SymbolKindStruct ||
			kind == ast.SymbolKindClass || kind == ast.SymbolKindInterface
	case "interface":
		return kind == ast.SymbolKindInterface
	case "variable":
		return kind == ast.SymbolKindVariable
	case "constant":
		return kind == ast.SymbolKindConstant
	case "enum":
		return kind == ast.SymbolKindEnum
	case "decorator":
		return kind == ast.SymbolKindDecorator
	case "property":
		return kind == ast.SymbolKindProperty
	case "field":
		return kind == ast.SymbolKindField
	case "component":
		return kind == ast.SymbolKindComponent
	case "all":
		return true
	default:
		return true
	}
}
