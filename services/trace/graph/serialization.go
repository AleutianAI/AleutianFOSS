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
	"fmt"
	"sort"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// GraphSchemaVersion is the version of the serialization schema.
// Increment when the serialization format changes in a breaking way.
const GraphSchemaVersion = "1.0"

// SerializableGraph is the JSON-serializable representation of a Graph.
//
// Description:
//
//	Contains all data needed to reconstruct a Graph from JSON. Nodes are
//	sorted by ID for deterministic output, enabling reliable diffing and
//	content hashing.
//
// Thread Safety: SerializableGraph is a value type with no internal state.
type SerializableGraph struct {
	// SchemaVersion identifies the serialization format version.
	SchemaVersion string `json:"schema_version"`

	// ProjectRoot is the absolute path to the project root directory.
	ProjectRoot string `json:"project_root"`

	// BuiltAtMilli is the Unix timestamp in milliseconds when the graph was frozen.
	BuiltAtMilli int64 `json:"built_at_milli"`

	// GraphHash is the deterministic hash of the graph structure.
	GraphHash string `json:"graph_hash"`

	// Nodes contains all nodes in the graph, sorted by ID.
	Nodes []SerializableNode `json:"nodes"`

	// Edges contains all edges in the graph.
	Edges []SerializableEdge `json:"edges"`
}

// SerializableNode is the JSON-serializable representation of a Node.
type SerializableNode struct {
	// ID is the unique node identifier (same as Symbol.ID).
	ID string `json:"id"`

	// Symbol is the underlying AST symbol. ast.Symbol already has JSON tags.
	Symbol *ast.Symbol `json:"symbol"`
}

// SerializableEdge is the JSON-serializable representation of an Edge.
type SerializableEdge struct {
	// FromID is the ID of the source node.
	FromID string `json:"from_id"`

	// ToID is the ID of the target node.
	ToID string `json:"to_id"`

	// Type is the human-readable edge type string (e.g., "calls", "imports").
	Type string `json:"type"`

	// TypeCode is the integer edge type for exact reconstruction.
	TypeCode EdgeType `json:"type_code"`

	// Location is where the relationship is expressed in code.
	Location ast.Location `json:"location"`
}

// ToSerializable converts a Graph to its JSON-serializable representation.
//
// Description:
//
//	Iterates all nodes (sorted by ID for deterministic output) and all edges
//	to produce a SerializableGraph suitable for JSON encoding. The resulting
//	structure contains all data needed to reconstruct the graph.
//
// Outputs:
//
//	*SerializableGraph - The serializable representation. Never nil.
//
// Complexity:
//
//	O(V log V + E) where V is node count and E is edge count.
//	Sorting nodes by ID dominates.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) ToSerializable() *SerializableGraph {
	if g == nil {
		return &SerializableGraph{
			SchemaVersion: GraphSchemaVersion,
			Nodes:         []SerializableNode{},
			Edges:         []SerializableEdge{},
		}
	}

	// Collect and sort node IDs for deterministic output
	nodeIDs := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	// Build serializable nodes
	nodes := make([]SerializableNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node := g.nodes[id]
		nodes = append(nodes, SerializableNode{
			ID:     id,
			Symbol: node.Symbol,
		})
	}

	// Build serializable edges (sorted for determinism)
	edges := make([]SerializableEdge, 0, len(g.edges))
	for _, edge := range g.edges {
		edges = append(edges, SerializableEdge{
			FromID:   edge.FromID,
			ToID:     edge.ToID,
			Type:     edge.Type.String(),
			TypeCode: edge.Type,
			Location: edge.Location,
		})
	}

	// Sort edges for deterministic output
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromID != edges[j].FromID {
			return edges[i].FromID < edges[j].FromID
		}
		if edges[i].ToID != edges[j].ToID {
			return edges[i].ToID < edges[j].ToID
		}
		return edges[i].TypeCode < edges[j].TypeCode
	})

	return &SerializableGraph{
		SchemaVersion: GraphSchemaVersion,
		ProjectRoot:   g.ProjectRoot,
		BuiltAtMilli:  g.BuiltAtMilli,
		GraphHash:     g.Hash(),
		Nodes:         nodes,
		Edges:         edges,
	}
}

// FromSerializable reconstructs a Graph from its serializable representation.
//
// Description:
//
//	Creates a new Graph in building state, calls AddNode() and AddEdge() for
//	each entry to correctly build all secondary indexes (nodesByName, nodesByKind,
//	edgesByType, edgesByFile), then calls Freeze(). This reuses the existing
//	construction code path to guarantee index consistency.
//
// Inputs:
//
//	sg - The serializable graph to reconstruct. Must not be nil.
//	opts - Optional GraphOption values (e.g., WithMaxNodes).
//
// Outputs:
//
//	*Graph - The reconstructed graph in read-only state.
//	error - Non-nil if sg is nil, contains invalid data, or capacity exceeded.
//
// Errors:
//
//	Returns error if sg is nil, schema version is unsupported, a node has nil
//	symbol, or AddNode/AddEdge fails.
//
// Complexity:
//
//	O(V + E) where V is node count and E is edge count.
//
// Thread Safety:
//
//	The returned graph is independent and safe for concurrent reads after construction.
func FromSerializable(sg *SerializableGraph, opts ...GraphOption) (*Graph, error) {
	if sg == nil {
		return nil, fmt.Errorf("serializable graph must not be nil")
	}
	if sg.SchemaVersion != GraphSchemaVersion {
		return nil, fmt.Errorf("unsupported schema version %q (expected %q)", sg.SchemaVersion, GraphSchemaVersion)
	}

	g := NewGraph(sg.ProjectRoot, opts...)

	// Add all nodes
	for i, sn := range sg.Nodes {
		if sn.Symbol == nil {
			return nil, fmt.Errorf("node at index %d has nil symbol (id=%s)", i, sn.ID)
		}
		if _, err := g.AddNode(sn.Symbol); err != nil {
			return nil, fmt.Errorf("adding node %s: %w", sn.ID, err)
		}
	}

	// Add all edges using TypeCode for exact reconstruction
	for i, se := range sg.Edges {
		if err := g.AddEdge(se.FromID, se.ToID, se.TypeCode, se.Location); err != nil {
			return nil, fmt.Errorf("adding edge %d (%s -> %s): %w", i, se.FromID, se.ToID, err)
		}
	}

	// Freeze the graph and restore the original BuiltAtMilli
	g.Freeze()
	g.BuiltAtMilli = sg.BuiltAtMilli

	return g, nil
}
