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
}

// FindReferencesOutput contains the structured result.
type FindReferencesOutput struct {
	// SymbolName is the symbol that was searched for.
	SymbolName string `json:"symbol_name"`

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
//   - Symbol names must match exactly (no fuzzy matching)
//   - References are code locations, not semantic relationships
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
				Description: "Name of the symbol to find references for",
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
func (t *findReferencesTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
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

	// Find symbol IDs and their references
	var allReferences []ReferenceInfo
	var symbolMatches int
	if t.index != nil {
		matches := t.index.GetByName(p.SymbolName)
		symbolMatches = len(matches)
		for _, sym := range matches {
			if sym == nil {
				continue
			}

			locations, gErr := t.graph.FindReferencesByID(ctx, sym.ID, graph.WithLimit(p.Limit))
			if gErr != nil {
				continue
			}

			for _, loc := range locations {
				allReferences = append(allReferences, ReferenceInfo{
					SymbolID: sym.ID,
					Package:  sym.Package,
					File:     loc.FilePath,
					Line:     loc.StartLine,
					Column:   loc.StartCol,
				})

				if len(allReferences) >= p.Limit {
					break
				}
			}

			if len(allReferences) >= p.Limit {
				break
			}
		}
	}

	// Build typed output
	output := FindReferencesOutput{
		SymbolName:     p.SymbolName,
		ReferenceCount: len(allReferences),
		References:     allReferences,
	}

	// Format text output
	outputText := t.formatText(p.SymbolName, allReferences)

	span.SetAttributes(attribute.Int("reference_count", len(allReferences)))

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_references").
		WithTarget(p.SymbolName).
		WithTool("find_references").
		WithDuration(duration).
		WithMetadata("reference_count", fmt.Sprintf("%d", len(allReferences))).
		WithMetadata("symbol_matches", fmt.Sprintf("%d", symbolMatches)).
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
	if err := ValidateSymbolName(p.SymbolName, "symbol_name", "'Session', 'app', 'Logger'"); err != nil {
		return p, err
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok && limit > 0 {
			p.Limit = limit
		}
	}

	return p, nil
}

// formatText creates a human-readable text summary.
func (t *findReferencesTool) formatText(symbolName string, refs []ReferenceInfo) string {
	var sb strings.Builder

	if len(refs) == 0 {
		sb.WriteString(fmt.Sprintf("No references found for '%s'.\n", symbolName))
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d references to '%s':\n\n", len(refs), symbolName))

	for _, ref := range refs {
		sb.WriteString(fmt.Sprintf("• %s:%d:%d\n", ref.File, ref.Line, ref.Column))
		if ref.Package != "" {
			sb.WriteString(fmt.Sprintf("  Package: %s\n", ref.Package))
		}
	}

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
