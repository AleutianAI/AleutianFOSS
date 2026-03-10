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
// get_signature Tool — CB-63 Tier 1
// =============================================================================

var getSignatureTracer = otel.Tracer("tools.get_signature")

// GetSignatureParams contains the validated input parameters.
type GetSignatureParams struct {
	// Name is the name of the symbol to get the signature for.
	Name string
}

// ToolName returns the tool name for TypedParams interface.
func (p GetSignatureParams) ToolName() string { return "get_signature" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p GetSignatureParams) ToMap() map[string]any {
	return map[string]any{
		"name": p.Name,
	}
}

// GetSignatureOutput contains the structured result.
type GetSignatureOutput struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Matches contains signature info for each matching symbol.
	Matches []SignatureMatch `json:"matches"`

	// MatchCount is the number of matching symbols.
	MatchCount int `json:"match_count"`
}

// SignatureMatch contains signature info for a single symbol.
type SignatureMatch struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind (function, method, class, etc.).
	Kind string `json:"kind"`

	// FilePath is the file containing the symbol.
	FilePath string `json:"file_path"`

	// StartLine is the first line of the symbol (1-indexed).
	StartLine int `json:"start_line"`

	// Signature is the type signature or declaration.
	Signature string `json:"signature"`

	// DocComment is the extracted documentation comment.
	DocComment string `json:"doc_comment"`

	// Exported indicates if the symbol is publicly visible.
	Exported bool `json:"exported"`

	// Package is the package or module name.
	Package string `json:"package"`

	// Receiver is the receiver type (for Go methods).
	Receiver string `json:"receiver,omitempty"`
}

// getSignatureTool returns the signature and doc comment of a named symbol.
//
// Description:
//
//	Fastest way to understand a function without reading the full source.
//	Retrieves signature and doc comment directly from the symbol index,
//	already stored in ast.Symbol.Signature and ast.Symbol.DocComment.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type getSignatureTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewGetSignatureTool creates the get_signature tool.
//
// Description:
//
//	Creates a tool that returns signatures and doc comments for symbols.
//	Uses O(1) index lookup — no filesystem access needed.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The get_signature tool implementation.
//
// Limitations:
//
//   - Returns empty signature/doc if the parser didn't extract them
//
// Assumptions:
//
//   - Index is populated with all symbols
func NewGetSignatureTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &getSignatureTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *getSignatureTool) Name() string {
	return "get_signature"
}

func (t *getSignatureTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *getSignatureTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "get_signature",
		Description: "Get the type signature and documentation of a named symbol. " +
			"Returns the function signature, doc comment, kind, file location, and export status. " +
			"Fastest way to understand what a function does without reading the full source. " +
			"Use for 'what is the signature of X?', 'how do I call X?', 'what are X's parameters?'.",
		Parameters: map[string]ParamDef{
			"name": {
				Type:        ParamTypeString,
				Description: "Name of the symbol to get the signature for (e.g., 'read_csv', 'TextFileReader', 'parseConfig')",
				Required:    true,
			},
		},
		Category:    CategoryExploration,
		Priority:    91,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"signature", "type signature", "function signature",
				"how to call", "parameters of", "arguments of",
				"doc comment", "documentation of", "what are the parameters",
			},
			UseWhen: "User asks about the signature, parameters, or documentation of a symbol " +
				"without needing the full source. Questions like 'what is the signature of X?', " +
				"'how do I call X?', 'what are X's parameters?'.",
			AvoidWhen: "User wants to read the full source code — use read_symbol. " +
				"User asks about callers or callees — use graph query tools.",
		},
	}
}

// Execute runs the get_signature tool.
func (t *getSignatureTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_get_signature").
			WithTool("get_signature").
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

	// Start span
	_, span := getSignatureTracer.Start(ctx, "getSignatureTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "get_signature"),
			attribute.String("name", p.Name),
		),
	)
	defer span.End()

	// Look up symbols by name
	var symbols []*ast.Symbol

	if strings.Contains(p.Name, ".") {
		symbol, _, resolveErr := ResolveFunctionWithFuzzy(ctx, t.index, p.Name, t.logger)
		if resolveErr == nil {
			symbols = []*ast.Symbol{symbol}
		}
	} else {
		symbols = t.index.GetByName(p.Name)

		if len(symbols) == 0 {
			symbol, fuzzy, resolveErr := ResolveFunctionWithFuzzy(ctx, t.index, p.Name, t.logger)
			if resolveErr == nil && fuzzy {
				symbols = []*ast.Symbol{symbol}
			}
		}
	}

	span.SetAttributes(attribute.Int("matches", len(symbols)))

	if len(symbols) == 0 {
		notFoundStep := crs.NewTraceStepBuilder().
			WithAction("tool_get_signature").
			WithTarget(p.Name).
			WithTool("get_signature").
			WithDuration(time.Since(start)).
			WithMetadata("match_count", "0").
			Build()

		outputText := fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n"+
			"No symbol named '%s' exists in this codebase.\n"+
			"The graph has been fully indexed — this is the definitive answer.\n", p.Name, p.Name)

		return &Result{
			Success:    true,
			Output:     GetSignatureOutput{Name: p.Name, MatchCount: 0},
			OutputText: outputText,
			TraceStep:  &notFoundStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Build output
	output := GetSignatureOutput{
		Name:       p.Name,
		MatchCount: len(symbols),
		Matches:    make([]SignatureMatch, 0, len(symbols)),
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Signature for '%s' (%d match", p.Name, len(symbols)))
	if len(symbols) != 1 {
		sb.WriteString("es")
	}
	sb.WriteString(")\n\n")

	for _, sym := range symbols {
		if sym == nil {
			continue
		}

		match := SignatureMatch{
			Name:       sym.Name,
			Kind:       sym.Kind.String(),
			FilePath:   sym.FilePath,
			StartLine:  sym.StartLine,
			Signature:  sym.Signature,
			DocComment: sym.DocComment,
			Exported:   sym.Exported,
			Package:    sym.Package,
			Receiver:   sym.Receiver,
		}
		output.Matches = append(output.Matches, match)

		// Format text
		sb.WriteString(fmt.Sprintf("### %s `%s` in `%s:%d`\n", sym.Kind, sym.Name, sym.FilePath, sym.StartLine))
		if sym.Package != "" {
			sb.WriteString(fmt.Sprintf("Package: %s\n", sym.Package))
		}
		if sym.Receiver != "" {
			sb.WriteString(fmt.Sprintf("Receiver: %s\n", sym.Receiver))
		}
		sb.WriteString(fmt.Sprintf("Exported: %t\n", sym.Exported))
		if sym.Signature != "" {
			sb.WriteString(fmt.Sprintf("\n```\n%s\n```\n", sym.Signature))
		} else {
			sb.WriteString("\n(no signature extracted)\n")
		}
		if sym.DocComment != "" {
			sb.WriteString(fmt.Sprintf("\nDoc: %s\n", sym.DocComment))
		}
		sb.WriteString("\n")
	}

	duration := time.Since(start)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_get_signature").
		WithTarget(p.Name).
		WithTool("get_signature").
		WithDuration(duration).
		WithMetadata("match_count", fmt.Sprintf("%d", len(symbols))).
		Build()

	return &Result{
		Success:     true,
		Output:      output,
		OutputText:  sb.String(),
		TokensUsed:  estimateTokens(sb.String()),
		TraceStep:   &toolStep,
		Duration:    duration,
		ResultCount: len(symbols),
	}, nil
}

// parseParams validates and extracts typed parameters.
func (t *getSignatureTool) parseParams(params map[string]any) (GetSignatureParams, error) {
	var p GetSignatureParams

	if nameRaw, ok := params["name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.Name = name
		}
	}
	if err := ValidateSymbolName(p.Name, "name", "'read_csv', 'TextFileReader', 'parseConfig'"); err != nil {
		return p, err
	}

	return p, nil
}
