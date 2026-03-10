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
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// list_symbols_in_file Tool — CB-63 Tier 1
// =============================================================================

var listSymbolsInFileTracer = otel.Tracer("tools.list_symbols_in_file")

// ListSymbolsInFileParams contains the validated input parameters.
type ListSymbolsInFileParams struct {
	// Path is the file path relative to the project root.
	Path string
}

// ToolName returns the tool name for TypedParams interface.
func (p ListSymbolsInFileParams) ToolName() string { return "list_symbols_in_file" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p ListSymbolsInFileParams) ToMap() map[string]any {
	return map[string]any{
		"path": p.Path,
	}
}

// ListSymbolsInFileOutput contains the structured result.
type ListSymbolsInFileOutput struct {
	// Path is the file path.
	Path string `json:"path"`

	// SymbolCount is the number of symbols found.
	SymbolCount int `json:"symbol_count"`

	// Symbols contains the symbols in the file.
	Symbols []FileSymbolInfo `json:"symbols"`
}

// FileSymbolInfo contains information about a symbol in a file.
type FileSymbolInfo struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind.
	Kind string `json:"kind"`

	// Line is the starting line number (1-indexed).
	Line int `json:"line"`

	// EndLine is the ending line number.
	EndLine int `json:"end_line"`

	// Exported indicates if the symbol is publicly visible.
	Exported bool `json:"exported"`

	// Signature is the type signature (if available).
	Signature string `json:"signature,omitempty"`

	// Receiver is the receiver type (for methods).
	Receiver string `json:"receiver,omitempty"`
}

// listSymbolsInFileTool lists all symbols defined in a given file.
//
// Description:
//
//	Uses the symbol index's GetByFile method for O(1) lookup to find
//	all symbols in a file. Returns them sorted by line number.
//	Essential for answering "what's in this file?" navigation queries.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type listSymbolsInFileTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewListSymbolsInFileTool creates the list_symbols_in_file tool.
//
// Description:
//
//	Creates a tool that lists all symbols defined in a given file.
//	Uses the symbol index's GetByFile for O(1) file-based lookup.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The list_symbols_in_file tool implementation.
//
// Limitations:
//
//   - Only shows symbols the parser extracted (may miss some constructs)
//
// Assumptions:
//
//   - Index is populated with all symbols
func NewListSymbolsInFileTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &listSymbolsInFileTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *listSymbolsInFileTool) Name() string {
	return "list_symbols_in_file"
}

func (t *listSymbolsInFileTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *listSymbolsInFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "list_symbols_in_file",
		Description: "List all symbols (functions, classes, methods, types, etc.) defined in a given file. " +
			"Returns symbols sorted by line number with their kind and export status. " +
			"Use for 'what's in this file?', 'list functions in X', 'what symbols are defined in X'.",
		Parameters: map[string]ParamDef{
			"path": {
				Type:        ParamTypeString,
				Description: "File path relative to project root (e.g., 'src/main.go', 'pandas/core/frame.py')",
				Required:    true,
			},
		},
		Category:    CategoryExploration,
		Priority:    90,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"symbols in file", "functions in file", "what's in",
				"list symbols", "list functions", "file outline",
				"table of contents", "what does file contain",
			},
			UseWhen: "User asks what symbols are defined in a specific file. " +
				"Questions like 'what's in X?', 'list functions in X', " +
				"'what symbols are in X?', 'show me the outline of X'.",
			AvoidWhen: "User wants to read the actual source code — use read_file or read_symbol. " +
				"User wants to find a symbol by name — use find_symbol.",
		},
	}
}

// Execute runs the list_symbols_in_file tool.
func (t *listSymbolsInFileTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_list_symbols_in_file").
			WithTool("list_symbols_in_file").
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
	_, span := listSymbolsInFileTracer.Start(ctx, "listSymbolsInFileTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "list_symbols_in_file"),
			attribute.String("path", p.Path),
		),
	)
	defer span.End()

	// Look up symbols by file path using O(1) index lookup
	symbols := t.index.GetByFile(p.Path)

	span.SetAttributes(attribute.Int("symbol_count", len(symbols)))

	if len(symbols) == 0 {
		notFoundStep := crs.NewTraceStepBuilder().
			WithAction("tool_list_symbols_in_file").
			WithTarget(p.Path).
			WithTool("list_symbols_in_file").
			WithDuration(time.Since(start)).
			WithMetadata("symbol_count", "0").
			Build()

		outputText := fmt.Sprintf("## GRAPH RESULT: No symbols found in '%s'\n\n"+
			"No symbols were indexed for this file. The file may not exist, "+
			"may not be a recognized source file, or may be outside the indexed project.\n"+
			"The graph has been fully indexed — this is the definitive answer.\n", p.Path)

		return &Result{
			Success:    true,
			Output:     ListSymbolsInFileOutput{Path: p.Path, SymbolCount: 0},
			OutputText: outputText,
			TraceStep:  &notFoundStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Sort symbols by line number
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].StartLine < symbols[j].StartLine
	})

	// Build output
	output := ListSymbolsInFileOutput{
		Path:        p.Path,
		SymbolCount: len(symbols),
		Symbols:     make([]FileSymbolInfo, 0, len(symbols)),
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Symbols in `%s` (%d symbols)\n\n", p.Path, len(symbols)))
	sb.WriteString("| Line | Kind | Name | Exported |\n")
	sb.WriteString("|------|------|------|----------|\n")

	for _, sym := range symbols {
		if sym == nil {
			continue
		}

		info := FileSymbolInfo{
			Name:      sym.Name,
			Kind:      sym.Kind.String(),
			Line:      sym.StartLine,
			EndLine:   sym.EndLine,
			Exported:  sym.Exported,
			Signature: sym.Signature,
			Receiver:  sym.Receiver,
		}
		output.Symbols = append(output.Symbols, info)

		exportedStr := "no"
		if sym.Exported {
			exportedStr = "yes"
		}
		displayName := sym.Name
		if sym.Receiver != "" {
			displayName = sym.Receiver + "." + sym.Name
		}
		sb.WriteString(fmt.Sprintf("| %d | %s | %s | %s |\n",
			sym.StartLine, sym.Kind, displayName, exportedStr))
	}

	sb.WriteString("\n")

	duration := time.Since(start)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_list_symbols_in_file").
		WithTarget(p.Path).
		WithTool("list_symbols_in_file").
		WithDuration(duration).
		WithMetadata("symbol_count", fmt.Sprintf("%d", len(symbols))).
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
func (t *listSymbolsInFileTool) parseParams(params map[string]any) (ListSymbolsInFileParams, error) {
	var p ListSymbolsInFileParams

	if pathRaw, ok := params["path"]; ok {
		if path, ok := parseStringParam(pathRaw); ok && path != "" {
			p.Path = path
		}
	}
	if p.Path == "" {
		return p, fmt.Errorf("'path' parameter is required")
	}

	// Reject traversal attempts
	if strings.Contains(p.Path, "..") {
		return p, fmt.Errorf("path must not contain '..': %s", p.Path)
	}

	return p, nil
}
