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
// find_path Tool - Typed Implementation
// =============================================================================

var findPathTracer = otel.Tracer("tools.find_path")

// FindPathParams contains the validated input parameters.
type FindPathParams struct {
	// From is the starting symbol name.
	From string

	// To is the target symbol name.
	To string
}

// ToolName returns the tool name for TypedParams interface.
func (p FindPathParams) ToolName() string { return "find_path" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindPathParams) ToMap() map[string]any {
	return map[string]any{
		"from": p.From,
		"to":   p.To,
	}
}

// FindPathOutput contains the structured result.
type FindPathOutput struct {
	// From is the starting symbol name.
	From string `json:"from"`

	// To is the target symbol name.
	To string `json:"to"`

	// Length is the path length in hops (-1 if no path found).
	Length int `json:"length"`

	// Found indicates if a path was found.
	Found bool `json:"found"`

	// Path is the list of nodes in the path.
	Path []PathNode `json:"path,omitempty"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// PathNode represents a node in the path.
type PathNode struct {
	// Hop is the position in the path (0-indexed).
	Hop int `json:"hop"`

	// ID is the node ID.
	ID string `json:"id"`

	// Name is the symbol name.
	Name string `json:"name,omitempty"`

	// File is the source file path.
	File string `json:"file,omitempty"`

	// Line is the line number.
	Line int `json:"line,omitempty"`

	// Kind is the symbol kind.
	Kind string `json:"kind,omitempty"`
}

// findPathTool finds the shortest path between two symbols.
type findPathTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewFindPathTool creates the find_path tool.
//
// Description:
//
//	Creates a tool that finds the shortest path between two symbols in the
//	code graph. Uses BFS to find the minimum-hop path considering all edge types.
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for name-to-ID resolution. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_path tool implementation.
//
// Limitations:
//
//   - Returns only one path even if multiple shortest paths exist
//   - Path length is measured in hops, not weighted by call frequency
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - BFS runs in O(V+E) time
//   - Caller handles disambiguation via package filter if needed
func NewFindPathTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findPathTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *findPathTool) Name() string {
	return "find_path"
}

func (t *findPathTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findPathTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_path",
		Description: "Find the shortest path between two symbols. " +
			"Uses BFS to find the minimum-hop path. " +
			"Useful for understanding how two pieces of code are connected.",
		Parameters: map[string]ParamDef{
			"from": {
				Type:        ParamTypeString,
				Description: "Starting symbol name (e.g., 'main', 'parseConfig')",
				Required:    true,
			},
			"to": {
				Type:        ParamTypeString,
				Description: "Target symbol name",
				Required:    true,
			},
		},
		Category:    CategoryExploration,
		Priority:    83,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

// Execute runs the find_path tool.
func (t *findPathTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Validate graph is available
	if t.graph == nil {
		return &Result{
			Success: false,
			Error:   "graph not initialized",
		}, nil
	}

	// Start span with context
	ctx, span := findPathTracer.Start(ctx, "findPathTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_path"),
			attribute.String("from", p.From),
			attribute.String("to", p.To),
		),
	)
	defer span.End()

	// IT-Summary FIX-C Step 1: Strip package qualifiers before resolution.
	// ParamExtractor may pass "gin.New" even though the prompt says to strip —
	// the tool must strip as a fallback safety net.
	fromName := stripPackageQualifier(p.From)
	toName := stripPackageQualifier(p.To)

	// IT-12 Rev 3: Use multi-candidate resolution so we can retry with alternate
	// From/To combinations if the first resolution produces no path.
	fromCandidates := t.resolveSymbolCandidates(ctx, fromName, 3)
	toCandidates := t.resolveSymbolCandidates(ctx, toName, 3)

	var fromID, fromPackage string
	if len(fromCandidates) > 0 {
		fromID = fromCandidates[0].ID
		fromPackage = fromCandidates[0].Package
	}
	var toID, toPackage string
	if len(toCandidates) > 0 {
		toID = toCandidates[0].ID
		toPackage = toCandidates[0].Package
	}

	// Handle not found cases
	if fromID == "" {
		output := FindPathOutput{
			From:    p.From,
			To:      p.To,
			Found:   false,
			Length:  -1,
			Message: fmt.Sprintf("Symbol '%s' not found", p.From),
		}
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: fmt.Sprintf("Symbol '%s' not found in the codebase.", p.From),
			TokensUsed: 10,
			Duration:   time.Since(start),
		}, nil
	}

	if toID == "" {
		output := FindPathOutput{
			From:    p.From,
			To:      p.To,
			Found:   false,
			Length:  -1,
			Message: fmt.Sprintf("Symbol '%s' not found", p.To),
		}
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: fmt.Sprintf("Symbol '%s' not found in the codebase.", p.To),
			TokensUsed: 10,
			Duration:   time.Since(start),
		}, nil
	}

	span.SetAttributes(
		attribute.String("from_id", fromID),
		attribute.String("from_package", fromPackage),
		attribute.String("to_id", toID),
		attribute.String("to_package", toPackage),
	)

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Find shortest path
	pathStart := time.Now()
	pathResult, err := t.graph.ShortestPath(ctx, fromID, toID)
	pathDuration := time.Since(pathStart)

	// Create CRS TraceStep
	var traceStep crs.TraceStep
	if err != nil {
		traceStep = crs.NewTraceStepBuilder().
			WithAction("graph_shortest_path").
			WithTarget(fmt.Sprintf("%s->%s", p.From, p.To)).
			WithTool("ShortestPath").
			WithDuration(pathDuration).
			WithError(err.Error()).
			Build()
		span.RecordError(err)
		span.SetAttributes(attribute.String("trace_action", traceStep.Action))
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("path search failed: %v", err),
			TraceStep: &traceStep,
			Duration:  time.Since(start),
		}, nil
	}

	traceStep = crs.NewTraceStepBuilder().
		WithAction("graph_shortest_path").
		WithTarget(fmt.Sprintf("%s->%s", p.From, p.To)).
		WithTool("ShortestPath").
		WithDuration(pathDuration).
		WithMetadata("path_length", fmt.Sprintf("%d", pathResult.Length)).
		WithMetadata("from_id", fromID).
		WithMetadata("to_id", toID).
		Build()

	span.SetAttributes(
		attribute.Int("path_length", pathResult.Length),
		attribute.Int("path_nodes", len(pathResult.Path)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Structured logging for edge cases
	if pathResult.Length < 0 {
		t.logger.Debug("no path found between symbols",
			slog.String("tool", "find_path"),
			slog.String("from", p.From),
			slog.String("to", p.To),
			slog.String("from_id", fromID),
			slog.String("to_id", toID),
		)
	}

	// IT-12 Rev 3: If no path found and we have alternative candidates,
	// try other From/To combinations. Try alt-From with original To first,
	// then original From with alt-To. Max 3 extra attempts total.
	if pathResult.Length < 0 && (len(fromCandidates) > 1 || len(toCandidates) > 1) {
		t.logger.Info("find_path: no path with primary candidates, trying alternatives",
			slog.String("from", p.From),
			slog.String("to", p.To),
			slog.Int("from_candidates", len(fromCandidates)),
			slog.Int("to_candidates", len(toCandidates)),
		)
		attempts := 0
		found := false
		for fi, fc := range fromCandidates {
			for ti, tc := range toCandidates {
				if fi == 0 && ti == 0 {
					continue // Already tried
				}
				if attempts >= 3 {
					break
				}
				attempts++
				altResult, altErr := t.graph.ShortestPath(ctx, fc.ID, tc.ID)
				if altErr == nil && altResult.Length >= 0 {
					t.logger.Info("find_path: alternative candidates found path",
						slog.String("from_id", fc.ID),
						slog.String("to_id", tc.ID),
						slog.Int("length", altResult.Length),
					)
					pathResult = altResult
					fromID = fc.ID
					toID = tc.ID
					found = true
					break
				}
			}
			if found || attempts >= 3 {
				break
			}
		}
	}

	// Build typed output
	output := t.buildOutput(p.From, p.To, pathResult)

	// Format text output
	outputText := t.formatText(p.From, p.To, pathResult)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &traceStep,
		Duration:   time.Since(start),
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findPathTool) parseParams(params map[string]any) (FindPathParams, error) {
	var p FindPathParams

	// Extract from (required)
	if fromRaw, ok := params["from"]; ok {
		if from, ok := parseStringParam(fromRaw); ok && from != "" {
			p.From = from
		}
	}
	if err := ValidateSymbolName(p.From, "from", "'main', 'handleRequest', 'Initialize'"); err != nil {
		return p, err
	}

	// Extract to (required)
	if toRaw, ok := params["to"]; ok {
		if to, ok := parseStringParam(toRaw); ok && to != "" {
			p.To = to
		}
	}
	if err := ValidateSymbolName(p.To, "to", "'Serve', 'Execute', 'render'"); err != nil {
		return p, err
	}

	return p, nil
}

// resolveSymbolCandidates resolves a symbol name to multiple candidates using
// the multi-candidate resolution pipeline.
//
// Description:
//
//	IT-12 Rev 3: Wraps ResolveFunctionCandidates for use by find_path. Returns
//	up to max candidates ranked callable-first, so the tool can retry with
//	alternate From/To combinations when the primary pair produces no path.
//
// Inputs:
//   - ctx: Context for timeout control. Must not be nil.
//   - name: Symbol name to resolve. Must not be empty.
//   - max: Maximum number of candidates to return.
//
// Outputs:
//   - []*ast.Symbol: Ranked candidates, or nil on error.
//
// Thread Safety: Safe for concurrent use (read-only operations).
func (t *findPathTool) resolveSymbolCandidates(ctx context.Context, name string, max int) []*ast.Symbol {
	if t.index == nil {
		return nil
	}
	candidates, err := ResolveFunctionCandidates(ctx, t.index, name,
		t.logger, max, WithKindFilter(KindFilterAny), WithBareMethodFallback())
	if err != nil {
		t.logger.Debug("find_path: candidate resolution failed",
			slog.String("name", name),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return candidates
}

// buildOutput creates the typed output struct.
func (t *findPathTool) buildOutput(fromName, toName string, result *graph.PathResult) FindPathOutput {
	output := FindPathOutput{
		From:   fromName,
		To:     toName,
		Length: result.Length,
		Found:  result.Length >= 0,
	}

	if result.Length < 0 {
		output.Message = fmt.Sprintf("No path found between '%s' and '%s'", fromName, toName)
		return output
	}

	// Build path details
	output.Path = make([]PathNode, 0, len(result.Path))
	for i, nodeID := range result.Path {
		node := PathNode{
			Hop: i,
			ID:  nodeID,
		}
		if t.index != nil {
			if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
				node.Name = sym.Name
				node.File = sym.FilePath
				node.Line = sym.StartLine
				node.Kind = sym.Kind.String()
			}
		}
		output.Path = append(output.Path, node)
	}

	return output
}

// stripPackageQualifier removes known package/project/stdlib prefixes from
// dot-notation symbol names while preserving Type.Method format.
//
// Description:
//
//	IT-Summary FIX-C Step 1: ParamExtractor may pass "gin.New" even though
//	the prompt instructs it to strip package qualifiers. This function acts
//	as a fallback safety net. If the prefix is a known project name or stdlib
//	package (all lowercase), it is stripped. If both parts are PascalCase or
//	the prefix is a type name (capitalized), the name is kept as-is for
//	Type.Method resolution.
//
// Inputs:
//   - name: Symbol name, possibly package-qualified (e.g., "gin.New").
//
// Outputs:
//   - string: The name with package prefix stripped, or unchanged if the
//     prefix is not a known package (e.g., "Engine.ServeHTTP" stays as-is).
//
// Thread Safety: This function is safe for concurrent use (pure function).
func stripPackageQualifier(name string) string {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return name
	}
	// If the prefix is PascalCase (starts with uppercase), it's likely a type name
	// (e.g., "Engine.ServeHTTP", "Context.JSON"), not a package qualifier.
	// Only strip when the prefix matches a known package in lowercase form AND
	// the original prefix is all lowercase (e.g., "gin.New", "http.Get").
	if parts[0] != strings.ToLower(parts[0]) {
		return name // Prefix has uppercase — treat as Type.Method
	}
	prefix := parts[0] // Already lowercase at this point
	knownPrefixes := map[string]bool{
		// Project names
		"gin": true, "flask": true, "express": true, "hugo": true,
		"nestjs": true, "pandas": true, "badger": true, "plottable": true,
		"babylonjs": true, "babylon": true,
		// Go stdlib
		"http": true, "os": true, "fmt": true, "io": true,
		"net": true, "path": true, "strings": true, "context": true,
		"sync": true, "time": true, "math": true, "sort": true,
		// Python stdlib
		"numpy": true, "np": true, "pd": true,
	}
	if knownPrefixes[prefix] {
		return parts[1]
	}
	return name // Keep Type.Method as-is
}

// formatText creates a human-readable text summary.
func (t *findPathTool) formatText(fromName, toName string, result *graph.PathResult) string {
	var sb strings.Builder

	if result.Length < 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: No path between '%s' and '%s'\n\n", fromName, toName))
		sb.WriteString("These symbols are not connected through call relationships.\n")
		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Path from %s to %s (%d hops):\n\n", fromName, toName, result.Length))

	for i, nodeID := range result.Path {
		nodeName := nodeID
		nodeFile := ""

		if t.index != nil {
			if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
				nodeName = fmt.Sprintf("%s()", sym.Name)
				nodeFile = fmt.Sprintf(" (%s:%d)", sym.FilePath, sym.StartLine)
			}
		}

		sb.WriteString(fmt.Sprintf("%d. %s%s\n", i+1, nodeName, nodeFile))

		// Add arrow except for last node
		if i < len(result.Path)-1 {
			sb.WriteString("   -> calls\n")
		}
	}

	return sb.String()
}
