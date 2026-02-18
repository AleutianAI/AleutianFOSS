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
// get_call_chain Tool - Typed Implementation
// =============================================================================

var getCallChainTracer = otel.Tracer("tools.get_call_chain")

// GetCallChainParams contains the validated input parameters.
type GetCallChainParams struct {
	// FunctionName is the name of the function to trace.
	FunctionName string

	// Direction is either "downstream" (callees) or "upstream" (callers).
	Direction string

	// MaxDepth is the maximum traversal depth (1-10).
	MaxDepth int
}

// GetCallChainOutput contains the structured result.
type GetCallChainOutput struct {
	// FunctionName is the function that was traced.
	FunctionName string `json:"function_name"`

	// Direction is the traversal direction.
	Direction string `json:"direction"`

	// Depth is the actual depth reached.
	Depth int `json:"depth"`

	// Truncated indicates if traversal was cut short.
	Truncated bool `json:"truncated"`

	// NodeCount is the number of nodes visited.
	NodeCount int `json:"node_count"`

	// EdgeCount is the number of edges traversed.
	EdgeCount int `json:"edge_count"`

	// Nodes is the list of nodes in the call chain.
	Nodes []CallChainNode `json:"nodes"`

	// Message contains optional status message.
	Message string `json:"message,omitempty"`
}

// CallChainNode holds information about a node in the call chain.
type CallChainNode struct {
	// ID is the node ID.
	ID string `json:"id"`

	// Name is the function name.
	Name string `json:"name,omitempty"`

	// File is the source file path.
	File string `json:"file,omitempty"`

	// Line is the line number.
	Line int `json:"line,omitempty"`

	// Package is the package name.
	Package string `json:"package,omitempty"`
}

// getCallChainTool wraps graph.GetCallGraph and GetReverseCallGraph.
//
// Description:
//
//	Gets the transitive call chain for a function, either downstream (callees)
//	or upstream (callers). Useful for impact analysis and understanding full
//	code flow.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type getCallChainTool struct {
	graph  *graph.Graph
	index  *index.SymbolIndex
	logger *slog.Logger
}

// NewGetCallChainTool creates the get_call_chain tool.
//
// Description:
//
//	Creates a tool that traces transitive call chains from a function.
//	Can trace both upstream (callers) and downstream (callees).
//
// Inputs:
//
//   - g: The code graph. Must not be nil.
//   - idx: The symbol index for O(1) lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The get_call_chain tool implementation.
//
// Limitations:
//
//   - Maximum depth of 10 to prevent excessive traversal
//   - May truncate large graphs
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - BFS traversal for call chain
func NewGetCallChainTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &getCallChainTool{
		graph:  g,
		index:  idx,
		logger: slog.Default(),
	}
}

func (t *getCallChainTool) Name() string {
	return "get_call_chain"
}

func (t *getCallChainTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *getCallChainTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "get_call_chain",
		Description: "Get the transitive call chain for a function. " +
			"Can trace either 'downstream' (what does this call, recursively) or " +
			"'upstream' (what calls this, recursively). " +
			"Useful for impact analysis and understanding full code flow.",
		Parameters: map[string]ParamDef{
			"function_name": {
				Type:        ParamTypeString,
				Description: "Name of the function to trace",
				Required:    true,
			},
			"direction": {
				Type:        ParamTypeString,
				Description: "Direction to trace: 'downstream' (callees) or 'upstream' (callers)",
				Required:    false,
				Default:     "downstream",
				Enum:        []any{"downstream", "upstream"},
			},
			"max_depth": {
				Type:        ParamTypeInt,
				Description: "Maximum depth to traverse (1-10)",
				Required:    false,
				Default:     5,
			},
		},
		Category:    CategoryExploration,
		Priority:    88,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

// Execute runs the get_call_chain tool.
func (t *getCallChainTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_get_call_chain").
			WithTool("get_call_chain").
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
	ctx, span := getCallChainTracer.Start(ctx, "getCallChainTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "get_call_chain"),
			attribute.String("function_name", p.FunctionName),
			attribute.String("direction", p.Direction),
			attribute.Int("max_depth", p.MaxDepth),
		),
	)
	defer span.End()

	// Find the symbol ID
	var symbolID string
	if t.index != nil {
		matches := t.index.GetByName(p.FunctionName)
		for _, sym := range matches {
			if sym != nil && (sym.Kind == ast.SymbolKindFunction || sym.Kind == ast.SymbolKindMethod) {
				symbolID = sym.ID
				break
			}
		}
	}

	if symbolID == "" {
		output := GetCallChainOutput{
			FunctionName: p.FunctionName,
			Direction:    p.Direction,
			Message:      fmt.Sprintf("No function named '%s' found", p.FunctionName),
		}
		notFoundStep := crs.NewTraceStepBuilder().
			WithAction("tool_get_call_chain").
			WithTarget(p.FunctionName).
			WithTool("get_call_chain").
			WithDuration(time.Since(start)).
			WithMetadata("direction", p.Direction).
			WithMetadata("chain_length", "0").
			WithMetadata("depth", "0").
			Build()
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: fmt.Sprintf("No function named '%s' found in the codebase.", p.FunctionName),
			TokensUsed: 10,
			TraceStep:  &notFoundStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Execute the appropriate traversal
	var traversal *graph.TraversalResult
	var gErr error

	if p.Direction == "upstream" {
		traversal, gErr = t.graph.GetReverseCallGraph(ctx, symbolID, graph.WithMaxDepth(p.MaxDepth))
	} else {
		traversal, gErr = t.graph.GetCallGraph(ctx, symbolID, graph.WithMaxDepth(p.MaxDepth))
	}

	if gErr != nil {
		span.RecordError(gErr)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_get_call_chain").
			WithTarget(p.FunctionName).
			WithTool("get_call_chain").
			WithDuration(time.Since(start)).
			WithMetadata("direction", p.Direction).
			WithError(fmt.Sprintf("traversal failed: %v", gErr)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("traversal failed: %v", gErr),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Build typed output
	output := t.buildOutput(p.FunctionName, p.Direction, traversal)

	// Format text output
	outputText := t.formatText(p.FunctionName, p.Direction, traversal)

	span.SetAttributes(
		attribute.Int("nodes_visited", len(traversal.VisitedNodes)),
		attribute.Int("depth", traversal.Depth),
		attribute.Bool("truncated", traversal.Truncated),
	)

	duration := time.Since(start)

	// Build CRS TraceStep for reasoning trace continuity
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_get_call_chain").
		WithTarget(p.FunctionName).
		WithTool("get_call_chain").
		WithDuration(duration).
		WithMetadata("chain_length", fmt.Sprintf("%d", len(traversal.VisitedNodes))).
		WithMetadata("depth", fmt.Sprintf("%d", traversal.Depth)).
		WithMetadata("direction", p.Direction).
		WithMetadata("truncated", fmt.Sprintf("%v", traversal.Truncated)).
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
func (t *getCallChainTool) parseParams(params map[string]any) (GetCallChainParams, error) {
	p := GetCallChainParams{
		Direction: "downstream",
		MaxDepth:  5,
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

	// Extract direction (optional)
	if dirRaw, ok := params["direction"]; ok {
		if dir, ok := parseStringParam(dirRaw); ok && dir != "" {
			if dir == "upstream" || dir == "downstream" {
				p.Direction = dir
			}
		}
	}

	// Extract max_depth (optional)
	if depthRaw, ok := params["max_depth"]; ok {
		if depth, ok := parseIntParam(depthRaw); ok {
			if depth < 1 {
				depth = 1
			} else if depth > 10 {
				t.logger.Warn("max_depth above maximum, clamping to 10",
					slog.String("tool", "get_call_chain"),
					slog.Int("requested", depth),
				)
				depth = 10
			}
			p.MaxDepth = depth
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *getCallChainTool) buildOutput(functionName, direction string, traversal *graph.TraversalResult) GetCallChainOutput {
	output := GetCallChainOutput{
		FunctionName: functionName,
		Direction:    direction,
		Depth:        traversal.Depth,
		Truncated:    traversal.Truncated,
		NodeCount:    len(traversal.VisitedNodes),
		EdgeCount:    len(traversal.Edges),
		Nodes:        make([]CallChainNode, 0, len(traversal.VisitedNodes)),
	}

	for _, nodeID := range traversal.VisitedNodes {
		node := CallChainNode{ID: nodeID}

		if t.index != nil {
			if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
				node.Name = sym.Name
				node.File = sym.FilePath
				node.Line = sym.StartLine
				node.Package = sym.Package
			}
		}
		output.Nodes = append(output.Nodes, node)
	}

	return output
}

// formatText creates a human-readable text summary.
func (t *getCallChainTool) formatText(functionName, direction string, traversal *graph.TraversalResult) string {
	var sb strings.Builder

	if len(traversal.VisitedNodes) == 0 {
		sb.WriteString(fmt.Sprintf("No call chain found for '%s' (%s).\n", functionName, direction))
		return sb.String()
	}

	dirLabel := "calls"
	if direction == "upstream" {
		dirLabel = "is called by"
	}

	sb.WriteString(fmt.Sprintf("Call chain for '%s' (%s):\n", functionName, direction))
	sb.WriteString(fmt.Sprintf("Depth: %d, Nodes: %d", traversal.Depth, len(traversal.VisitedNodes)))
	if traversal.Truncated {
		sb.WriteString(" (truncated)")
	}
	sb.WriteString("\n\n")

	for i, nodeID := range traversal.VisitedNodes {
		indent := strings.Repeat("  ", i)
		prefix := "→"
		if direction == "upstream" {
			prefix = "←"
		}

		nodeName := nodeID
		if t.index != nil {
			if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
				nodeName = fmt.Sprintf("%s() [%s:%d]", sym.Name, sym.FilePath, sym.StartLine)
			}
		}

		if i == 0 {
			sb.WriteString(fmt.Sprintf("%s%s (root) %s\n", indent, nodeName, dirLabel))
		} else {
			sb.WriteString(fmt.Sprintf("%s%s %s\n", indent, prefix, nodeName))
		}
	}

	return sb.String()
}
