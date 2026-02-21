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

	// DestinationName is an optional endpoint for "from X to Y" queries.
	// IT-05 R5: When set, provides the resolved destination symbol ID for
	// dual-endpoint resolution. Used to enhance output messaging.
	DestinationName string

	// PackageHint is an optional package/module context extracted from the query.
	// IT-06c: Used to disambiguate when multiple symbols share the same name.
	PackageHint string
}

// ToolName returns the tool name for TypedParams interface.
func (p GetCallChainParams) ToolName() string { return "get_call_chain" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p GetCallChainParams) ToMap() map[string]any {
	m := map[string]any{
		"function_name": p.FunctionName,
		"direction":     p.Direction,
		"max_depth":     p.MaxDepth,
	}
	if p.DestinationName != "" {
		m["destination_name"] = p.DestinationName
	}
	if p.PackageHint != "" {
		m["package_hint"] = p.PackageHint
	}
	return m
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

	// DestinationFound indicates whether the destination (from "from X to Y" queries)
	// was found in the traversal result. Only set when DestinationName was provided.
	DestinationFound bool `json:"destination_found,omitempty"`

	// PathToDestination contains only the nodes on the path from source to destination.
	// Only populated when DestinationName was provided and found in the traversal.
	PathToDestination []CallChainNode `json:"path_to_destination,omitempty"`

	// ExternalDependencies lists external library boundaries encountered during traversal.
	// IT-05a: Populated when the call chain reaches nodes outside the indexed project.
	ExternalDependencies []string `json:"external_dependencies,omitempty"`
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

	// Depth is the BFS depth from the root node (0 for root).
	Depth int `json:"depth"`

	// CalledBy is the parent node ID in the call chain.
	CalledBy string `json:"called_by,omitempty"`

	// IsExternal indicates this node is an external dependency boundary.
	// IT-05a: Set when the node represents a call to an external library
	// that is not part of the indexed project.
	IsExternal bool `json:"is_external,omitempty"`

	// ExternalPkg is the inferred external package/module name.
	// Only set when IsExternal is true.
	ExternalPkg string `json:"external_package,omitempty"`
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
func (t *getCallChainTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
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

	// IT-05: Use shared symbol resolution (IT-00a directive)
	// KindFilterAny because get_call_chain should accept classes, structs, etc. —
	// the graph's BFS only follows EdgeTypeCalls edges, so non-callable symbols
	// simply return an empty traversal (harmless).
	var symbolID string
	if t.index != nil {
		sym, _, err := ResolveFunctionWithFuzzy(ctx, t.index, p.FunctionName, t.logger,
			WithKindFilter(KindFilterAny), WithBareMethodFallback())
		if err == nil {
			symbolID = sym.ID
		} else {
			// CR-1: Log resolution failure for debuggability
			t.logger.Debug("get_call_chain: symbol resolution failed",
				slog.String("function_name", p.FunctionName),
				slog.String("error", err.Error()),
			)
		}
	}

	// CR-3: Fallback to graph-based lookup when index is nil or resolution failed.
	// IT-00a-1 Phase 2: Apply disambiguation when multiple nodes match instead of
	// taking nodes[0] (which is effectively random from map iteration order).
	if symbolID == "" && t.graph != nil {
		nodes := t.graph.GetNodesByName(p.FunctionName)
		if len(nodes) == 1 {
			symbolID = nodes[0].Symbol.ID
			t.logger.Debug("get_call_chain: resolved via graph fallback",
				slog.String("function_name", p.FunctionName),
				slog.String("symbol_id", symbolID),
			)
		} else if len(nodes) > 1 {
			syms := make([]*ast.Symbol, len(nodes))
			for i, n := range nodes {
				syms[i] = n.Symbol
			}
			// IT-06c: Try package hint disambiguation first, then fall back
			// to the existing disambiguateGraphNodes heuristic.
			if p.PackageHint != "" {
				filtered := filterByPackageHint(syms, p.PackageHint, t.logger, "get_call_chain")
				if len(filtered) == 1 {
					symbolID = filtered[0].ID
					t.logger.Debug("get_call_chain: resolved via package hint",
						slog.String("function_name", p.FunctionName),
						slog.String("package_hint", p.PackageHint),
						slog.String("symbol_id", symbolID),
						slog.Int("candidates", len(nodes)),
					)
				} else {
					// Multiple still remain after hint — use existing disambiguation
					best := disambiguateGraphNodes(filtered)
					if best != nil {
						symbolID = best.ID
					}
				}
			}
			if symbolID == "" {
				best := disambiguateGraphNodes(syms)
				if best != nil {
					symbolID = best.ID
					t.logger.Debug("get_call_chain: resolved via graph fallback with disambiguation",
						slog.String("function_name", p.FunctionName),
						slog.String("symbol_id", symbolID),
						slog.Int("candidates", len(nodes)),
					)
				}
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
		// CR-2: Inheritance-aware upstream traversal.
		// Collect parent method IDs so that callers of overridden parent methods
		// are included in the upstream chain.
		parentMethodIDs := t.findParentMethodIDs(symbolID)
		if len(parentMethodIDs) > 0 {
			t.logger.Debug("get_call_chain: inheritance-aware upstream",
				slog.String("symbol_id", symbolID),
				slog.Int("parent_methods", len(parentMethodIDs)),
			)
		}
		traversal, gErr = t.graph.GetReverseCallGraph(ctx, symbolID, graph.WithMaxDepth(p.MaxDepth))

		// Merge callers of parent methods into traversal result
		if gErr == nil && len(parentMethodIDs) > 0 {
			for _, parentID := range parentMethodIDs {
				parentTraversal, pErr := t.graph.GetReverseCallGraph(ctx, parentID, graph.WithMaxDepth(p.MaxDepth))
				if pErr != nil {
					continue
				}
				mergeTraversals(traversal, parentTraversal)
			}
		}
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

	// Build depth map once (used by both buildOutput and formatText)
	depths, parents := buildNodeDepths(traversal, p.Direction)

	// Build typed output
	output := t.buildOutputWithDepths(p.FunctionName, p.Direction, traversal, depths, parents)

	// IT-05 R5 review fix: When DestinationName is set, check if the destination
	// was found in the traversal and extract the path from source to destination.
	if p.DestinationName != "" {
		destFound := false
		for _, nodeID := range traversal.VisitedNodes {
			if nodeID == p.DestinationName {
				destFound = true
				break
			}
		}
		output.DestinationFound = destFound
		if destFound {
			// Walk parents map backward from destination to source to extract path
			pathNodes := extractPathToDestination(p.DestinationName, parents, depths, t.index)
			output.PathToDestination = pathNodes
			output.Message = fmt.Sprintf("Found static path from '%s' to destination '%s' (depth %d).",
				p.FunctionName, p.DestinationName, len(pathNodes)-1)
		}
	}

	// IT-05 R5: Depth-0 enrichment — when traversal finds no call edges,
	// add context about the limitation and include destination info if available.
	if output.Depth == 0 || (len(traversal.VisitedNodes) <= 1 && len(traversal.Edges) == 0) {
		msg := fmt.Sprintf(
			"Static analysis shows no further call edges from '%s'. "+
				"This may be due to dynamic dispatch, event-driven patterns, or framework-specific behavior.",
			p.FunctionName)
		if p.DestinationName != "" && !output.DestinationFound {
			msg += fmt.Sprintf(" Destination '%s' was also resolved but no static path connects them.", p.DestinationName)
		}
		output.Message = msg
	}

	// Format text output
	outputText := t.formatTextWithDepths(p.FunctionName, p.Direction, traversal, depths)

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
	// IT-05 Issue 4: Reject generic words like "the", "function", "class"
	if err := ValidateSymbolName(p.FunctionName, "function_name", "'main', 'handleRequest', 'render'"); err != nil {
		return p, err
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

	// IT-05 R5: Extract destination_name (optional, for "from X to Y" queries)
	if destRaw, ok := params["destination_name"]; ok {
		if dest, ok := parseStringParam(destRaw); ok {
			p.DestinationName = dest
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

// buildNodeDepths reconstructs the BFS depth of each node from the traversal edges.
//
// Description:
//
//	For downstream traversal, edges go from parent→child (FromID→ToID).
//	For upstream traversal, edges go from child→parent (FromID→ToID represents
//	a reverse call edge: ToID called FromID).
//	The start node is at depth 0; each edge adds 1 to the depth of the target.
//
// Outputs:
//   - map[string]int: node ID → BFS depth
//   - map[string]string: node ID → parent node ID (the node that led to this one)
func buildNodeDepths(traversal *graph.TraversalResult, direction string) (map[string]int, map[string]string) {
	depths := make(map[string]int, len(traversal.VisitedNodes))
	parents := make(map[string]string, len(traversal.VisitedNodes))

	if len(traversal.VisitedNodes) > 0 {
		depths[traversal.StartNode] = 0
	}

	for _, edge := range traversal.Edges {
		// In BFS traversal, edges go from visited node to new node.
		// For downstream: FromID is the caller (known depth), ToID is the callee.
		// For upstream: FromID is the callee (known depth), ToID is the caller.
		var parentID, childID string
		if direction == "upstream" {
			parentID = edge.ToID
			childID = edge.FromID
		} else {
			parentID = edge.FromID
			childID = edge.ToID
		}

		if parentDepth, ok := depths[parentID]; ok {
			if _, already := depths[childID]; !already {
				depths[childID] = parentDepth + 1
				parents[childID] = parentID
			}
		}
	}

	return depths, parents
}

// extractPathToDestination walks the parents map backward from the destination
// to the root (source) and returns the path as a list of CallChainNodes in
// source-to-destination order.
//
// Description:
//
//	IT-05 R5 review fix: When a "from X to Y" query resolves both endpoints and
//	the BFS traversal visits the destination, this function extracts the specific
//	path connecting them. This filters out unrelated branches from the full BFS tree.
//
// Inputs:
//   - destID: The destination node ID found in the traversal.
//   - parents: BFS parent map (child → parent).
//   - depths: BFS depth map (node → depth).
//   - idx: Symbol index for enriching nodes with names/files.
//
// Outputs:
//   - []CallChainNode: The path from source to destination, in order.
//
// Thread Safety: Safe for concurrent use (read-only operations).
func extractPathToDestination(destID string, parents map[string]string, depths map[string]int, idx *index.SymbolIndex) []CallChainNode {
	// Walk backward from destination to root via parents map
	var reversePath []string
	current := destID
	for i := 0; i < 100; i++ { // Safety limit to prevent infinite loops
		reversePath = append(reversePath, current)
		parent, ok := parents[current]
		if !ok {
			break // Reached root (no parent)
		}
		current = parent
	}

	// Reverse to get source→destination order
	n := len(reversePath)
	path := make([]CallChainNode, n)
	for i := 0; i < n; i++ {
		nodeID := reversePath[n-1-i] // Read from end of reversePath
		node := CallChainNode{
			ID:    nodeID,
			Depth: depths[nodeID],
		}
		if i > 0 {
			node.CalledBy = reversePath[n-i] // Previous in reversed order = parent
		}
		if idx != nil {
			if sym, ok := idx.GetByID(nodeID); ok && sym != nil {
				node.Name = sym.Name
				node.File = sym.FilePath
				node.Line = sym.StartLine
				node.Package = sym.Package
			}
		}
		path[i] = node
	}

	return path
}

// buildOutputWithDepths creates the typed output struct using pre-computed depth and parent maps.
func (t *getCallChainTool) buildOutputWithDepths(functionName, direction string, traversal *graph.TraversalResult, depths map[string]int, parents map[string]string) GetCallChainOutput {
	output := GetCallChainOutput{
		FunctionName: functionName,
		Direction:    direction,
		Depth:        traversal.Depth,
		Truncated:    traversal.Truncated,
		NodeCount:    len(traversal.VisitedNodes),
		EdgeCount:    len(traversal.Edges),
		Nodes:        make([]CallChainNode, 0, len(traversal.VisitedNodes)),
	}

	// IT-05a: Classify external boundary nodes.
	externals := graph.ClassifyExternalNodes(t.graph, traversal)
	externalSet := make(map[string]graph.ExternalDependency, len(externals))
	for _, ext := range externals {
		externalSet[ext.NodeID] = ext
	}

	for _, nodeID := range traversal.VisitedNodes {
		node := CallChainNode{
			ID:       nodeID,
			Depth:    depths[nodeID],
			CalledBy: parents[nodeID],
		}

		// IT-05a: Check if this node is an external boundary.
		if ext, isExternal := externalSet[nodeID]; isExternal {
			node.IsExternal = true
			node.ExternalPkg = ext.Package
			node.Name = ext.Name
		}

		if !node.IsExternal && t.index != nil {
			if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
				node.Name = sym.Name
				node.File = sym.FilePath
				node.Line = sym.StartLine
				node.Package = sym.Package
			}
		}
		output.Nodes = append(output.Nodes, node)
	}

	// IT-05a: Build external dependencies summary.
	if len(externals) > 0 {
		seen := make(map[string]bool, len(externals))
		for _, ext := range externals {
			label := ext.Name
			if ext.Package != "" {
				label = ext.Package + "." + ext.Name
			}
			if !seen[label] {
				output.ExternalDependencies = append(output.ExternalDependencies, label)
				seen[label] = true
			}
		}
	}

	return output
}

// formatTextWithDepths creates a human-readable text summary using pre-computed depths.
func (t *getCallChainTool) formatTextWithDepths(functionName, direction string, traversal *graph.TraversalResult, depths map[string]int) string {
	var sb strings.Builder

	if len(traversal.VisitedNodes) == 0 {
		sb.WriteString(fmt.Sprintf("## GRAPH RESULT: Call chain for '%s' not found\n\n", functionName))
		sb.WriteString(fmt.Sprintf("No call chain found for '%s' (%s).\n", functionName, direction))
		sb.WriteString("\n**Do NOT use Grep to search further** - the graph already analyzed all source files.\n")
		return sb.String()
	}

	dirLabel := "calls"
	if direction == "upstream" {
		dirLabel = "is called by"
	}

	sb.WriteString(fmt.Sprintf("Found %d nodes in call chain for '%s' (%s):\n", len(traversal.VisitedNodes), functionName, direction))
	sb.WriteString(fmt.Sprintf("Depth: %d, Nodes: %d", traversal.Depth, len(traversal.VisitedNodes)))
	if traversal.Truncated {
		sb.WriteString(" (truncated)")
	}
	sb.WriteString("\n\n")

	// IT-05a: Classify external nodes for text formatting.
	externals := graph.ClassifyExternalNodes(t.graph, traversal)
	externalSet := make(map[string]graph.ExternalDependency, len(externals))
	for _, ext := range externals {
		externalSet[ext.NodeID] = ext
	}

	for i, nodeID := range traversal.VisitedNodes {
		depth := depths[nodeID]
		indent := strings.Repeat("  ", depth)
		prefix := "→"
		if direction == "upstream" {
			prefix = "←"
		}

		nodeName := nodeID
		if ext, isExternal := externalSet[nodeID]; isExternal {
			// IT-05a: Format external nodes with package annotation.
			if ext.Package != "" {
				nodeName = fmt.Sprintf("%s() (external: %s)", ext.Name, ext.Package)
			} else {
				nodeName = fmt.Sprintf("%s() (external)", ext.Name)
			}
		} else if t.index != nil {
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

	// IT-05a: Append external dependency summary.
	if len(externals) > 0 {
		sb.WriteString("\n--- External Dependencies ---\n")
		seen := make(map[string]bool, len(externals))
		for _, ext := range externals {
			label := ext.Name
			if ext.Package != "" {
				label = ext.Package + "." + ext.Name
			}
			if !seen[label] {
				sb.WriteString(fmt.Sprintf("  • %s (depth %d)\n", label, ext.Depth))
				seen[label] = true
			}
		}
		sb.WriteString("Note: External dependencies are outside the indexed project. ")
		sb.WriteString("Use search_library_docs for detailed API documentation.\n")
	}

	// IT-06c M-5: Definitive footer for pass-through detection by getSingleFormattedResult().
	sb.WriteString("\nThe graph has been fully indexed — these results are exhaustive.\n")
	sb.WriteString("**Do NOT use Grep or Read to verify** — the graph already analyzed all source files.\n")

	return sb.String()
}

// findParentMethodIDs walks the inheritance chain to find parent methods with
// the same name as the resolved symbol.
//
// Description:
//
//	CR-2: For inheritance-aware upstream traversal. If Dog.speak() overrides
//	Animal.speak(), callers of Animal.speak() should also appear in Dog.speak()'s
//	upstream chain. This method collects the IDs of parent methods with the same
//	name, which are used to merge their caller sets into the traversal result.
//
// Inputs:
//
//	symbolID - The resolved symbol ID to find parents for.
//
// Outputs:
//
//	[]string - IDs of parent methods with the same name. Nil if none found.
//
// Thread Safety: Safe for concurrent use (read-only operations).
func (t *getCallChainTool) findParentMethodIDs(symbolID string) []string {
	if t.index == nil {
		return nil
	}

	// Get the resolved symbol
	sym, ok := t.index.GetByID(symbolID)
	if !ok || sym == nil {
		return nil
	}

	// Only methods can have parent methods
	if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
		return nil
	}

	// Find the owner class via Receiver field
	ownerClassName := sym.Receiver
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

	// Walk the inheritance chain and collect same-named methods.
	// Use map for O(1) dedup instead of O(n) linear search per iteration.
	var parentMethodIDs []string
	seen := make(map[string]bool)
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
			if child != nil && child.Name == sym.Name && !seen[child.ID] {
				parentMethodIDs = append(parentMethodIDs, child.ID)
				seen[child.ID] = true
				break
			}
		}

		// Also check by Receiver match (Go-style)
		allMethods := t.index.GetByName(sym.Name)
		for _, m := range allMethods {
			if m.Receiver == currentParentName && m.ID != sym.ID && !seen[m.ID] {
				parentMethodIDs = append(parentMethodIDs, m.ID)
				seen[m.ID] = true
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

// mergeTraversals merges a secondary traversal into the primary one, deduplicating
// nodes and edges.
//
// Description:
//
//	CR-2: Used by inheritance-aware upstream traversal to combine callers from
//	parent methods into the main traversal result.
//
// Inputs:
//
//	primary - The main traversal result.
//	secondary - The parent method traversal to merge.
//
// Mutation Contract:
//
//	This function mutates primary in-place. It does NOT return a value.
//	The caller's reference to primary already reflects the merged state.
//
// Thread Safety: NOT safe for concurrent use. Caller must ensure exclusive access.
func mergeTraversals(primary, secondary *graph.TraversalResult) {
	if secondary == nil || len(secondary.VisitedNodes) == 0 {
		return
	}

	// Build a set of existing nodes for deduplication
	existingNodes := make(map[string]bool, len(primary.VisitedNodes))
	for _, nodeID := range primary.VisitedNodes {
		existingNodes[nodeID] = true
	}

	// Build a set of existing edges for deduplication
	type edgeKey struct{ from, to string }
	existingEdges := make(map[edgeKey]bool, len(primary.Edges))
	for _, e := range primary.Edges {
		existingEdges[edgeKey{e.FromID, e.ToID}] = true
	}

	// Merge new nodes
	for _, nodeID := range secondary.VisitedNodes {
		if !existingNodes[nodeID] {
			primary.VisitedNodes = append(primary.VisitedNodes, nodeID)
			existingNodes[nodeID] = true
		}
	}

	// Merge new edges
	for _, e := range secondary.Edges {
		key := edgeKey{e.FromID, e.ToID}
		if !existingEdges[key] {
			primary.Edges = append(primary.Edges, e)
			existingEdges[key] = true
		}
	}

	// Update depth if secondary went deeper
	if secondary.Depth > primary.Depth {
		primary.Depth = secondary.Depth
	}

	// Merge truncation flag
	if secondary.Truncated {
		primary.Truncated = true
	}
}

// disambiguateGraphNodes picks the best symbol from multiple graph nodes with the
// same name using multi-signal scoring. Uses the same penalty signals as
// phases.disambiguateMultipleMatches (IT-05 SR1) to maintain scoring consistency.
//
// Description:
//
//	IT-00a-1 Phase 2: When the graph fallback returns multiple nodes matching a
//	function name, this function scores each candidate and returns the best one
//	instead of taking an arbitrary first result.
//
//	Scoring signals (lower is better):
//	  - Test file: +50000
//	  - Unexported: +20000
//	  - Underscore prefix: +10000
//	  - Directory depth beyond 2: +1000 per level
//	  - Kind: function/method = 0, type = +1, other = +2
//
// Inputs:
//
//	syms - Slice of symbols to choose from (len >= 1).
//
// Outputs:
//
//	*ast.Symbol - The best-scoring symbol.
//
// Thread Safety: Safe for concurrent use (stateless function).
func disambiguateGraphNodes(syms []*ast.Symbol) *ast.Symbol {
	if len(syms) == 0 {
		return nil
	}
	if len(syms) == 1 {
		return syms[0]
	}

	best := syms[0]
	bestScore := scoreGraphNode(best)

	for _, s := range syms[1:] {
		sc := scoreGraphNode(s)
		if sc < bestScore {
			best = s
			bestScore = sc
		}
	}

	return best
}

// scoreGraphNode computes a disambiguation score for a graph node symbol.
// Lower scores indicate more relevant symbols. Aligned with
// phases.scoreForDisambiguation to maintain cross-package scoring consistency.
func scoreGraphNode(sym *ast.Symbol) int {
	score := 0

	// Test file penalty
	if isGraphNodeTestFile(sym.FilePath) {
		score += 50000
	}

	// Export penalty
	if !sym.Exported {
		score += 20000
	}

	// Underscore prefix penalty
	if len(sym.Name) > 0 && sym.Name[0] == '_' {
		score += 10000
	}

	// Directory depth penalty
	depth := strings.Count(sym.FilePath, "/")
	if depth > 2 {
		score += (depth - 2) * 1000
	}

	// Kind preference
	switch sym.Kind {
	case ast.SymbolKindFunction, ast.SymbolKindMethod:
		// Best — no penalty
	case ast.SymbolKindClass, ast.SymbolKindStruct, ast.SymbolKindInterface, ast.SymbolKindType:
		score += 1
	default:
		score += 2
	}

	return score
}

// isGraphNodeTestFile checks if a file path indicates a test file.
// Mirrors phases.isTestFilePath and index.isTestFile for cross-package consistency.
func isGraphNodeTestFile(filePath string) bool {
	lower := strings.ToLower(filePath)

	for _, dir := range []string{"test/", "tests/", "__tests__/", "testing/"} {
		if strings.HasPrefix(lower, dir) || strings.Contains(lower, "/"+dir) {
			return true
		}
	}

	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	if strings.HasSuffix(lower, "_test.py") || strings.HasSuffix(lower, "conftest.py") {
		return true
	}
	base := lower
	if idx := strings.LastIndex(lower, "/"); idx >= 0 {
		base = lower[idx+1:]
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	for _, ext := range []string{".test.js", ".spec.js", ".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx", ".test.jsx", ".spec.jsx"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	return false
}
