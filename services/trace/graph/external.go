// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// ExternalDependency represents an external library boundary detected during
// graph traversal.
//
// Description:
//
//	IT-05a: When a traversal tool (get_call_chain, find_callees, find_callers, etc.)
//	encounters a node with Kind == SymbolKindExternal, this struct captures the
//	boundary information so tools can annotate their output.
//
// Thread Safety: This type is safe for concurrent use (immutable after creation).
type ExternalDependency struct {
	// NodeID is the graph node ID of the external symbol.
	// Format: "external:<package>:<name>" or "external::<name>".
	NodeID string

	// Name is the symbol name (e.g., "run_simple", "read_csv").
	Name string

	// Package is the inferred external package/module (e.g., "werkzeug.serving", "pandas").
	// Empty string if the package could not be determined.
	Package string

	// CalledFrom is the ID of the internal node that calls this external symbol.
	// Empty string if the caller could not be determined from the traversal edges.
	CalledFrom string

	// Depth is the traversal depth at which this boundary was encountered.
	Depth int
}

// ClassifyExternalNodes identifies external dependency boundaries in a traversal result.
//
// Description:
//
//	IT-05a: Iterates over the visited nodes in a TraversalResult, identifies those with
//	Kind == SymbolKindExternal, and returns structured ExternalDependency entries with
//	package information and caller linkage.
//
//	This function is designed to be called by any traversal tool's buildOutput method.
//	It operates on the already-completed TraversalResult — it does not modify the
//	traversal itself.
//
// Inputs:
//   - g: The graph containing the nodes. Must not be nil.
//   - result: The traversal result to classify. Must not be nil.
//
// Outputs:
//   - []ExternalDependency: External boundaries found, ordered by traversal order.
//     Returns nil if no external nodes are found.
//
// Thread Safety: Safe for concurrent use (reads only).
func ClassifyExternalNodes(g *Graph, result *TraversalResult) []ExternalDependency {
	if g == nil || result == nil || len(result.VisitedNodes) == 0 {
		return nil
	}

	// Build caller map from edges: targetID → sourceID
	callerMap := make(map[string]string, len(result.Edges))
	for _, edge := range result.Edges {
		if _, exists := callerMap[edge.ToID]; !exists {
			callerMap[edge.ToID] = edge.FromID
		}
	}

	// Build depth map from visited nodes + edges
	depthMap := buildDepthMap(result)

	var externals []ExternalDependency

	for _, nodeID := range result.VisitedNodes {
		node, ok := g.GetNode(nodeID)
		if !ok || node.Symbol == nil {
			continue
		}
		if node.Symbol.Kind != ast.SymbolKindExternal {
			continue
		}

		dep := ExternalDependency{
			NodeID:     nodeID,
			Name:       node.Symbol.Name,
			Package:    node.Symbol.Package,
			CalledFrom: callerMap[nodeID],
			Depth:      depthMap[nodeID],
		}

		// If Package is empty, try to infer from the Name's dot-prefix.
		// This handles legacy placeholder nodes created before IT-05a enrichment.
		if dep.Package == "" {
			dep.Package = inferPackageFromName(node.Symbol.Name)
		}

		externals = append(externals, dep)
	}

	return externals
}

// inferPackageFromName extracts a package prefix from a dotted symbol name.
// This is a fallback for placeholder nodes that were created without package info.
//
// Examples:
//
//	"os.MkdirAll" → "os"
//	"werkzeug.run_simple" → "werkzeug"
//	"Connect" → "" (no dot prefix)
//	"os.path.join" → "os.path"
func inferPackageFromName(name string) string {
	lastDot := strings.LastIndexByte(name, '.')
	if lastDot <= 0 {
		return ""
	}
	return name[:lastDot]
}

// buildDepthMap constructs a nodeID → depth map from a TraversalResult.
// Uses the edges to compute BFS depth from the start node.
func buildDepthMap(result *TraversalResult) map[string]int {
	depths := make(map[string]int, len(result.VisitedNodes))
	depths[result.StartNode] = 0

	// Build adjacency from edges
	children := make(map[string][]string, len(result.Edges))
	for _, edge := range result.Edges {
		children[edge.FromID] = append(children[edge.FromID], edge.ToID)
	}

	// BFS to compute depths
	queue := []string{result.StartNode}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		currentDepth := depths[current]

		for _, child := range children[current] {
			if _, exists := depths[child]; !exists {
				depths[child] = currentDepth + 1
				queue = append(queue, child)
			}
		}
	}

	return depths
}
