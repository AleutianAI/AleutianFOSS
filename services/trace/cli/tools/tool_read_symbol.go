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
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
// read_symbol Tool — CB-63 Tier 1
// =============================================================================

var readSymbolTracer = otel.Tracer("tools.read_symbol")

// ReadSymbolParams contains the validated input parameters.
type ReadSymbolParams struct {
	// Name is the name of the symbol to read source code for.
	Name string

	// Kind is an optional filter for the symbol kind (function, class, etc.).
	Kind string

	// PackageHint is an optional package/module context for disambiguation.
	PackageHint string
}

// ToolName returns the tool name for TypedParams interface.
func (p ReadSymbolParams) ToolName() string { return "read_symbol" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p ReadSymbolParams) ToMap() map[string]any {
	m := map[string]any{
		"name": p.Name,
	}
	if p.Kind != "" {
		m["kind"] = p.Kind
	}
	if p.PackageHint != "" {
		m["package_hint"] = p.PackageHint
	}
	return m
}

// ReadSymbolOutput contains the structured result.
type ReadSymbolOutput struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Matches contains the source code for each matching symbol.
	Matches []ReadSymbolMatch `json:"matches"`

	// MatchCount is the number of matching symbols.
	MatchCount int `json:"match_count"`
}

// ReadSymbolMatch contains source code for a single symbol.
type ReadSymbolMatch struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// FilePath is the file containing the symbol.
	FilePath string `json:"file_path"`

	// StartLine is the first line of the symbol (1-indexed).
	StartLine int `json:"start_line"`

	// EndLine is the last line of the symbol (1-indexed).
	EndLine int `json:"end_line"`

	// Language is the source language.
	Language string `json:"language"`

	// Kind is the symbol kind.
	Kind string `json:"kind"`

	// Source is the source code of the symbol.
	Source string `json:"source"`

	// Truncated indicates if the source was truncated (over 500 lines).
	Truncated bool `json:"truncated"`

	// LineCount is the total number of lines in the symbol.
	LineCount int `json:"line_count"`
}

// readSymbolTool reads the source code of a named symbol.
//
// Description:
//
//	Uses the symbol index for O(1) lookup to find the symbol's file path
//	and line range, then reads the source code from the filesystem.
//	This is the primary tool for "explain X" and "show me X" queries.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type readSymbolTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewReadSymbolTool creates the read_symbol tool.
//
// Description:
//
//	Creates a tool that reads source code for a named symbol.
//	Uses O(1) index lookup to find the symbol, then reads from filesystem.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The read_symbol tool implementation.
//
// Limitations:
//
//   - Symbol names must match exactly (no fuzzy matching initially)
//   - Source files must be accessible on the filesystem
//   - Maximum 500 lines before truncation flag is set
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Index is populated with all symbols
//   - Project files are accessible at graph.ProjectRoot
func NewReadSymbolTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &readSymbolTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *readSymbolTool) Name() string {
	return "read_symbol"
}

func (t *readSymbolTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *readSymbolTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "read_symbol",
		Description: "Read the source code of a named symbol (function, class, method, etc.). " +
			"Uses the symbol index for instant lookup, then reads the exact line range from the project files. " +
			"Use when asked 'explain X', 'show me X', 'what does X do?', or 'read the source of X'. " +
			"Returns the full source code with file path and line numbers.",
		Parameters: map[string]ParamDef{
			"name": {
				Type:        ParamTypeString,
				Description: "Name of the symbol to read source code for (e.g., 'read_csv', 'parseConfig', 'DataFrame')",
				Required:    true,
			},
			"kind": {
				Type:        ParamTypeString,
				Description: "Optional filter by symbol kind",
				Required:    false,
				Default:     "all",
				Enum:        []any{"function", "method", "class", "struct", "interface", "type", "all"},
			},
			"package_hint": {
				Type:        ParamTypeString,
				Description: "Optional package/module hint for disambiguation when multiple symbols share the same name",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    96, // Higher than find_callers — most natural follow-up
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"explain", "show me", "what does", "read source",
				"source code", "show source", "read function",
				"show function", "read code", "show code",
				"how does it work", "implementation of",
			},
			UseWhen: "User asks to explain, show, or read the source code of a specific function, " +
				"class, or method. Questions like 'explain X', 'what does X do?', 'show me X', " +
				"'read the source of X'. This is the primary tool for understanding what code does.",
			AvoidWhen: "User asks about callers, callees, or graph structure — use graph query tools. " +
				"User asks about file contents without a specific symbol — use read_file. " +
				"User asks about just the signature — use get_signature for a lighter response.",
		},
	}
}

// Execute runs the read_symbol tool.
func (t *readSymbolTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_symbol").
			WithTool("read_symbol").
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
	ctx, span := readSymbolTracer.Start(ctx, "readSymbolTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "read_symbol"),
			attribute.String("name", p.Name),
			attribute.String("kind", p.Kind),
			attribute.String("package_hint", p.PackageHint),
		),
	)
	defer span.End()

	// Look up symbols by name using O(1) index
	var symbols []*ast.Symbol

	if strings.Contains(p.Name, ".") {
		// Dot-notation: try fuzzy resolution
		symbol, _, resolveErr := ResolveFunctionWithFuzzy(ctx, t.index, p.Name, t.logger)
		if resolveErr == nil {
			symbols = []*ast.Symbol{symbol}
		}
	} else {
		symbols = t.index.GetByName(p.Name)

		// Fuzzy fallback
		if len(symbols) == 0 {
			symbol, fuzzy, resolveErr := ResolveFunctionWithFuzzy(ctx, t.index, p.Name, t.logger)
			if resolveErr == nil && fuzzy {
				symbols = []*ast.Symbol{symbol}
			}
		}
	}

	// Filter by kind if specified
	if p.Kind != "" && p.Kind != "all" {
		symbols = filterByKind(symbols, p.Kind)
	}

	// Filter by package hint if ambiguous
	if len(symbols) > 1 && p.PackageHint != "" {
		symbols = filterByPackageHint(symbols, p.PackageHint, t.logger, "read_symbol")
	}

	span.SetAttributes(attribute.Int("matches", len(symbols)))

	if len(symbols) == 0 {
		notFoundStep := crs.NewTraceStepBuilder().
			WithAction("tool_read_symbol").
			WithTarget(p.Name).
			WithTool("read_symbol").
			WithDuration(time.Since(start)).
			WithMetadata("match_count", "0").
			Build()

		outputText := fmt.Sprintf("## GRAPH RESULT: Symbol '%s' not found\n\n"+
			"No symbol named '%s' exists in this codebase.\n"+
			"The graph has been fully indexed — this is the definitive answer.\n", p.Name, p.Name)

		return &Result{
			Success:    true,
			Output:     ReadSymbolOutput{Name: p.Name, MatchCount: 0},
			OutputText: outputText,
			TraceStep:  &notFoundStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Read source code for each match
	output := ReadSymbolOutput{
		Name:       p.Name,
		MatchCount: len(symbols),
		Matches:    make([]ReadSymbolMatch, 0, len(symbols)),
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Source code for '%s' (%d match", p.Name, len(symbols)))
	if len(symbols) != 1 {
		sb.WriteString("es")
	}
	sb.WriteString(")\n\n")

	for _, sym := range symbols {
		if sym == nil {
			continue
		}

		match := ReadSymbolMatch{
			Name:      sym.Name,
			FilePath:  sym.FilePath,
			StartLine: sym.StartLine,
			EndLine:   sym.EndLine,
			Language:  sym.Language,
			Kind:      sym.Kind.String(),
			LineCount: sym.EndLine - sym.StartLine + 1,
			Truncated: sym.EndLine-sym.StartLine+1 > 500,
		}

		// Read source from filesystem
		source, readErr := t.readSourceLines(sym.FilePath, sym.StartLine, sym.EndLine)
		if readErr != nil {
			t.logger.Warn("failed to read source file",
				slog.String("tool", "read_symbol"),
				slog.String("file", sym.FilePath),
				slog.String("error", readErr.Error()),
			)
			match.Source = fmt.Sprintf("(error reading source: %s)", readErr.Error())
		} else {
			match.Source = source
		}

		output.Matches = append(output.Matches, match)

		// Format text output
		sb.WriteString(fmt.Sprintf("### %s `%s` in `%s:%d-%d`\n",
			sym.Kind, sym.Name, sym.FilePath, sym.StartLine, sym.EndLine))
		if sym.Package != "" {
			sb.WriteString(fmt.Sprintf("Package: %s\n", sym.Package))
		}
		sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", sym.Language, match.Source))
		if match.Truncated {
			sb.WriteString(fmt.Sprintf("⚠ Source is %d lines (truncation flag set)\n\n", match.LineCount))
		}
	}

	duration := time.Since(start)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_read_symbol").
		WithTarget(p.Name).
		WithTool("read_symbol").
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
func (t *readSymbolTool) parseParams(params map[string]any) (ReadSymbolParams, error) {
	p := ReadSymbolParams{Kind: "all"}

	if nameRaw, ok := params["name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.Name = name
		}
	}
	if err := ValidateSymbolName(p.Name, "name", "'read_csv', 'parseConfig', 'DataFrame'"); err != nil {
		return p, err
	}

	if kindRaw, ok := params["kind"]; ok {
		if kind, ok := parseStringParam(kindRaw); ok && kind != "" {
			p.Kind = kind
		}
	}

	if hintRaw, ok := params["package_hint"]; ok {
		if hint, ok := parseStringParam(hintRaw); ok && hint != "" {
			p.PackageHint = hint
		}
	}

	return p, nil
}

// readSourceLines reads specific line ranges from a source file.
//
// Description:
//
//	Reads lines startLine through endLine (1-indexed) from the file at
//	the given path, resolved relative to the graph's ProjectRoot.
//	Validates the path is under the project root to prevent traversal attacks.
//
// Inputs:
//
//   - filePath: Relative path from project root.
//   - startLine: First line to read (1-indexed, inclusive).
//   - endLine: Last line to read (1-indexed, inclusive).
//
// Outputs:
//
//   - string: The source code lines joined with newlines.
//   - error: Non-nil if file cannot be read or path is invalid.
func (t *readSymbolTool) readSourceLines(filePath string, startLine, endLine int) (string, error) {
	absPath := filepath.Join(t.graph.ProjectRoot, filePath)

	// Security: validate path is under project root
	resolved, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("resolving path %s: %w", filePath, err)
	}
	projectRoot, err := filepath.Abs(t.graph.ProjectRoot)
	if err != nil {
		return "", fmt.Errorf("resolving project root: %w", err)
	}
	if !strings.HasPrefix(resolved, projectRoot+string(filepath.Separator)) && resolved != projectRoot {
		return "", fmt.Errorf("path traversal rejected: %s resolves outside project root", filePath)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= startLine && lineNum <= endLine {
			lines = append(lines, scanner.Text())
		}
		if lineNum > endLine {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading %s: %w", filePath, err)
	}

	return strings.Join(lines, "\n"), nil
}

// filterByKind filters symbols to only those matching the specified kind.
func filterByKind(symbols []*ast.Symbol, kind string) []*ast.Symbol {
	targetKind := ast.ParseSymbolKind(kind)
	if targetKind == ast.SymbolKindUnknown {
		return symbols
	}
	filtered := make([]*ast.Symbol, 0, len(symbols))
	for _, sym := range symbols {
		if sym != nil && sym.Kind == targetKind {
			filtered = append(filtered, sym)
		}
	}
	return filtered
}
