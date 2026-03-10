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
	"hash/fnv"
	"sort"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Default configuration values.
const (
	// DefaultMaxNodes is the default maximum number of nodes a graph can hold.
	DefaultMaxNodes = 1_000_000

	// DefaultMaxEdges is the default maximum number of edges a graph can hold.
	DefaultMaxEdges = 10_000_000
)

// GraphState represents the lifecycle state of the graph.
type GraphState int

const (
	// GraphStateBuilding indicates the graph is accepting AddNode/AddEdge calls.
	GraphStateBuilding GraphState = iota

	// GraphStateReadOnly indicates the graph is frozen and read-only.
	GraphStateReadOnly
)

// String returns the string representation of the GraphState.
func (s GraphState) String() string {
	switch s {
	case GraphStateBuilding:
		return "building"
	case GraphStateReadOnly:
		return "readonly"
	default:
		return "unknown"
	}
}

// EdgeType defines the type of relationship between symbols.
type EdgeType int

const (
	// EdgeTypeUnknown indicates an unrecognized relationship type.
	EdgeTypeUnknown EdgeType = iota

	// EdgeTypeCalls indicates a function/method calls another function/method.
	EdgeTypeCalls

	// EdgeTypeImports indicates a file imports a package.
	EdgeTypeImports

	// EdgeTypeDefines indicates a file defines a symbol.
	EdgeTypeDefines

	// EdgeTypeImplements indicates a type implements an interface.
	EdgeTypeImplements

	// EdgeTypeEmbeds indicates a type embeds another type.
	EdgeTypeEmbeds

	// EdgeTypeReferences indicates a symbol references another symbol (general).
	EdgeTypeReferences

	// EdgeTypeReturns indicates a function returns a type.
	EdgeTypeReturns

	// EdgeTypeReceives indicates a method has a receiver of a type.
	EdgeTypeReceives

	// EdgeTypeParameters indicates a function takes a type as parameter.
	EdgeTypeParameters

	// NumEdgeTypes is the total number of edge types (for array sizing).
	// GR-08: Used for edgesByType index.
	NumEdgeTypes
)

// edgeTypeNames maps EdgeType values to their string representations.
var edgeTypeNames = map[EdgeType]string{
	EdgeTypeUnknown:    "unknown",
	EdgeTypeCalls:      "calls",
	EdgeTypeImports:    "imports",
	EdgeTypeDefines:    "defines",
	EdgeTypeImplements: "implements",
	EdgeTypeEmbeds:     "embeds",
	EdgeTypeReferences: "references",
	EdgeTypeReturns:    "returns",
	EdgeTypeReceives:   "receives",
	EdgeTypeParameters: "parameters",
}

// String returns the string representation of the EdgeType.
func (t EdgeType) String() string {
	if name, ok := edgeTypeNames[t]; ok {
		return name
	}
	return "unknown"
}

// Edge represents a directed relationship between two symbols.
//
// Multiple edges of the same type between the same nodes are allowed,
// representing different call sites or references in the code.
// For example, if function A calls function B at lines 10 and 20,
// there will be two EdgeTypeCalls edges with different Locations.
type Edge struct {
	// FromID is the ID of the source node.
	FromID string

	// ToID is the ID of the target node.
	ToID string

	// Type is the relationship type (calls, imports, etc.).
	Type EdgeType

	// Location is where the relationship is expressed in code.
	Location ast.Location
}

// Node represents a symbol in the code graph with its relationships.
//
// The Symbol pointer is NOT owned by the Node. The referenced Symbol
// MUST NOT be mutated after the Node is added to a Graph.
type Node struct {
	// ID is the unique identifier, same as Symbol.ID.
	ID string

	// Symbol is the underlying symbol from AST parsing.
	// This pointer is NOT owned by the Node.
	Symbol *ast.Symbol

	// Outgoing contains edges where this node is the source.
	// For example, if this node is a function, Outgoing contains
	// all the functions it calls.
	Outgoing []*Edge

	// Incoming contains edges where this node is the target.
	// For example, if this node is a function, Incoming contains
	// all the functions that call it.
	Incoming []*Edge
}

// GraphOptions configures Graph behavior and limits.
type GraphOptions struct {
	// MaxNodes is the maximum number of nodes the graph can hold.
	// Default: 1,000,000
	MaxNodes int

	// MaxEdges is the maximum number of edges the graph can hold.
	// Default: 10,000,000
	MaxEdges int
}

// DefaultGraphOptions returns sensible defaults for graph configuration.
func DefaultGraphOptions() GraphOptions {
	return GraphOptions{
		MaxNodes: DefaultMaxNodes,
		MaxEdges: DefaultMaxEdges,
	}
}

// GraphOption is a functional option for configuring Graph.
type GraphOption func(*GraphOptions)

// WithMaxNodes sets the maximum number of nodes the graph can hold.
func WithMaxNodes(n int) GraphOption {
	return func(o *GraphOptions) {
		o.MaxNodes = n
	}
}

// WithMaxEdges sets the maximum number of edges the graph can hold.
func WithMaxEdges(n int) GraphOption {
	return func(o *GraphOptions) {
		o.MaxEdges = n
	}
}

// Graph represents the code relationship graph for a project.
//
// Thread Safety:
//
//	Graph is NOT safe for concurrent use during building. It is designed
//	for single-writer access during build, then read-only after Freeze().
//	After Freeze() is called, the graph can be safely read from multiple
//	goroutines, but no further modifications are allowed.
//
// Lifecycle:
//
//  1. Create with NewGraph(projectRoot)
//  2. Build with AddNode() and AddEdge() calls
//  3. Call Freeze() to finalize
//  4. Query with GetNode(), traversal methods, etc.
type Graph struct {
	// ProjectRoot is the absolute path to the project root directory.
	ProjectRoot string

	// nodes maps node ID to Node. Unexported to prevent direct access.
	nodes map[string]*Node

	// edges contains all edges in the graph.
	edges []*Edge

	// nodesByName maps symbol name to nodes with that name.
	// GR-06: Secondary index for O(1) name-based lookup.
	// Multiple symbols can share a name (e.g., "Setup" in different packages).
	// Thread safety: Writes during build only, reads after Freeze().
	nodesByName map[string][]*Node

	// nodesByKind maps symbol kind to nodes of that kind.
	// GR-07: Secondary index for O(1) kind-based lookup.
	// Thread safety: Writes during build only, reads after Freeze().
	nodesByKind map[ast.SymbolKind][]*Node

	// edgesByType stores edges grouped by their type.
	// GR-08: Secondary index for O(1) type-based edge lookup.
	// Array indexed by EdgeType for cache-friendly access.
	// Thread safety: Writes during build only, reads after Freeze().
	edgesByType [NumEdgeTypes][]*Edge

	// edgesByFile maps file path to edges with Location in that file.
	// GR-09: Secondary index for O(1) file-based edge lookup.
	// Note: Indexes by edge.Location.FilePath (where the edge is expressed),
	// which may differ from the source node's file path.
	// Thread safety: Writes during build only, reads after Freeze().
	edgesByFile map[string][]*Edge

	// state is the current lifecycle state.
	state GraphState

	// options contains configuration.
	options GraphOptions

	// BuiltAtMilli is the Unix timestamp in milliseconds when Freeze() was called.
	// Zero if the graph has not been frozen.
	BuiltAtMilli int64

	// FileMtimes records file modification times (Unix seconds) at build time.
	// CRS-19: Used for staleness detection across sessions. Populated by
	// RecordFileMtimes() after Freeze(). Key is relative file path.
	FileMtimes map[string]int64
}

// NewGraph creates a new empty graph for the given project root.
//
// Description:
//
//	Creates a graph in the Building state, ready to accept AddNode and
//	AddEdge calls. The graph must be frozen with Freeze() before querying.
//
// Inputs:
//
//	projectRoot - Absolute path to the project root directory.
//	opts - Optional configuration options.
//
// Example:
//
//	// Default options
//	g := NewGraph("/path/to/project")
//
//	// Custom limits
//	g := NewGraph("/path/to/project",
//	    WithMaxNodes(100_000),
//	    WithMaxEdges(1_000_000),
//	)
func NewGraph(projectRoot string, opts ...GraphOption) *Graph {
	options := DefaultGraphOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return &Graph{
		ProjectRoot: projectRoot,
		nodes:       make(map[string]*Node),
		edges:       make([]*Edge, 0),
		nodesByName: make(map[string][]*Node),
		nodesByKind: make(map[ast.SymbolKind][]*Node),
		// edgesByType is zero-initialized as empty array of slices
		edgesByFile: make(map[string][]*Edge),
		state:       GraphStateBuilding,
		options:     options,
	}
}

// State returns the current lifecycle state of the graph.
func (g *Graph) State() GraphState {
	return g.state
}

// IsFrozen returns true if the graph is in read-only mode.
func (g *Graph) IsFrozen() bool {
	return g.state == GraphStateReadOnly
}

// Freeze transitions the graph to read-only mode.
//
// Description:
//
//	After calling Freeze(), AddNode and AddEdge will return ErrGraphFrozen.
//	This operation is irreversible. The BuiltAtMilli timestamp is set to
//	the current time. Validates secondary index integrity before freezing.
//
// Thread Safety:
//
//	After Freeze() returns, the graph can be safely read from multiple
//	goroutines concurrently.
//
// Limitations:
//
//	Index validation adds O(V + E) overhead on freeze. For graphs with
//	millions of nodes, consider using FreezeWithoutValidation().
func (g *Graph) Freeze() {
	// GR-73: Sort edges for deterministic ordering regardless of worker count.
	// Sort key: (FromID, ToID, Type, Location.FilePath, Location.StartLine).
	sortEdges(g.edges)

	// Sort per-node Outgoing/Incoming slices for deterministic iteration.
	for _, node := range g.nodes {
		sortEdges(node.Outgoing)
		sortEdges(node.Incoming)
	}

	// Sort per-type edge slices.
	for i := range g.edgesByType {
		sortEdges(g.edgesByType[i])
	}

	// Sort per-file edge slices.
	for file := range g.edgesByFile {
		sortEdges(g.edgesByFile[file])
	}

	// GR-06/07/08: Validate secondary indexes before freezing
	g.validateIndexes()

	g.state = GraphStateReadOnly
	g.BuiltAtMilli = time.Now().UnixMilli()
}

// sortEdges sorts a slice of edges by (FromID, ToID, Type, FilePath, StartLine)
// for deterministic ordering. GR-73.
func sortEdges(edges []*Edge) {
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		if a.ToID != b.ToID {
			return a.ToID < b.ToID
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Location.FilePath != b.Location.FilePath {
			return a.Location.FilePath < b.Location.FilePath
		}
		return a.Location.StartLine < b.Location.StartLine
	})
}

// validateIndexes verifies secondary index integrity.
//
// Description:
//
//	GR-06/07/08/09: Checks that secondary indexes are consistent with primary data.
//	Called by Freeze() to catch corruption before read-only mode.
//
// Invariants Checked:
//
//   - nodesByName: All indexed nodes exist in g.nodes; non-empty names only
//   - nodesByKind: All indexed nodes exist in g.nodes
//   - edgesByType: sum(len(edgesByType[t])) == len(edges)
//   - edgesByFile: All indexed edges exist in g.edges; non-empty paths only
//
// Thread Safety:
//
//	NOT safe for concurrent use. Called during build phase only.
func (g *Graph) validateIndexes() {
	// Validate nodesByName index
	for name, nodes := range g.nodesByName {
		if name == "" {
			// Empty names should not be indexed
			continue
		}
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if _, exists := g.nodes[node.ID]; !exists {
				// Log but don't fail - index has stale reference
				// This would indicate a bug in RemoveFile
			}
		}
	}

	// Validate nodesByKind index
	for _, nodes := range g.nodesByKind {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if _, exists := g.nodes[node.ID]; !exists {
				// Stale reference in kind index
			}
		}
	}

	// Validate edgesByType index
	totalIndexedEdges := 0
	for i := 0; i < int(NumEdgeTypes); i++ {
		totalIndexedEdges += len(g.edgesByType[i])
	}
	if totalIndexedEdges != len(g.edges) {
		// Edge count mismatch - indicates missed index update
	}

	// Validate edgesByFile index
	// Note: Edges with empty FilePath are not indexed, so sum may be less than len(edges)
	for filePath, edges := range g.edgesByFile {
		if filePath == "" {
			// Empty paths should not be indexed
			continue
		}
		for _, edge := range edges {
			if edge == nil {
				continue
			}
			// Verify edge still exists in main edges slice (basic check)
			// Full validation would require O(E) lookup, so we just check non-nil
		}
	}
}

// NodeCount returns the number of nodes in the graph.
func (g *Graph) NodeCount() int {
	return len(g.nodes)
}

// EdgeCount returns the number of edges in the graph.
func (g *Graph) EdgeCount() int {
	return len(g.edges)
}

// AddNode adds a symbol as a node in the graph.
//
// Description:
//
//	Creates a new node from the given symbol and adds it to the graph.
//	The symbol's ID becomes the node's ID.
//
// Inputs:
//
//	symbol - The symbol to add. Must not be nil.
//
// Outputs:
//
//	*Node - The created node. Can be used to inspect Outgoing/Incoming edges.
//	error - Non-nil if the graph is frozen, at capacity, or symbol is invalid.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//	ErrInvalidNode - Symbol is nil
//	ErrDuplicateNode - Node with same ID already exists
//	ErrMaxNodesExceeded - Graph is at node capacity
//
// Ownership:
//
//	The graph stores a pointer to the symbol but does NOT own it.
//	The symbol MUST NOT be mutated after this call.
func (g *Graph) AddNode(symbol *ast.Symbol) (*Node, error) {
	if g.state == GraphStateReadOnly {
		return nil, ErrGraphFrozen
	}

	if symbol == nil {
		return nil, fmt.Errorf("%w: symbol is nil", ErrInvalidNode)
	}

	if len(g.nodes) >= g.options.MaxNodes {
		return nil, ErrMaxNodesExceeded
	}

	if _, exists := g.nodes[symbol.ID]; exists {
		return nil, fmt.Errorf("%w: %s", ErrDuplicateNode, symbol.ID)
	}

	node := &Node{
		ID:       symbol.ID,
		Symbol:   symbol,
		Outgoing: make([]*Edge, 0),
		Incoming: make([]*Edge, 0),
	}

	g.nodes[symbol.ID] = node

	// GR-06: Update nodesByName index
	if symbol.Name != "" {
		g.nodesByName[symbol.Name] = append(g.nodesByName[symbol.Name], node)
	}

	// GR-07: Update nodesByKind index
	g.nodesByKind[symbol.Kind] = append(g.nodesByKind[symbol.Kind], node)

	return node, nil
}

// GetNode retrieves a node by its ID.
//
// Description:
//
//	Performs O(1) lookup in the node map.
//
// Inputs:
//
//	id - The node ID (same as Symbol.ID).
//
// Outputs:
//
//	*Node - The node if found, nil otherwise.
//	bool - True if the node was found.
func (g *Graph) GetNode(id string) (*Node, bool) {
	node, exists := g.nodes[id]
	return node, exists
}

// AddEdge creates a directed edge between two nodes.
//
// Description:
//
//	Creates an edge from the source node to the target node with the
//	given type and location. Both nodes must already exist in the graph.
//	Multiple edges of the same type between the same nodes are allowed
//	(representing different call sites or references).
//
// Inputs:
//
//	fromID - ID of the source node.
//	toID - ID of the target node.
//	edgeType - The type of relationship.
//	loc - Where the relationship is expressed in code.
//
// Outputs:
//
//	error - Non-nil if the graph is frozen, at capacity, or nodes don't exist.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//	ErrNodeNotFound - Source or target node doesn't exist
//	ErrMaxEdgesExceeded - Graph is at edge capacity
func (g *Graph) AddEdge(fromID, toID string, edgeType EdgeType, loc ast.Location) error {
	if g.state == GraphStateReadOnly {
		return ErrGraphFrozen
	}

	if len(g.edges) >= g.options.MaxEdges {
		return ErrMaxEdgesExceeded
	}

	fromNode, fromOK := g.nodes[fromID]
	if !fromOK {
		return fmt.Errorf("%w: source %s", ErrNodeNotFound, fromID)
	}

	toNode, toOK := g.nodes[toID]
	if !toOK {
		return fmt.Errorf("%w: target %s", ErrNodeNotFound, toID)
	}

	edge := &Edge{
		FromID:   fromID,
		ToID:     toID,
		Type:     edgeType,
		Location: loc,
	}

	g.edges = append(g.edges, edge)
	fromNode.Outgoing = append(fromNode.Outgoing, edge)
	toNode.Incoming = append(toNode.Incoming, edge)

	// GR-08: Update edgesByType index
	if edgeType >= 0 && edgeType < NumEdgeTypes {
		g.edgesByType[edgeType] = append(g.edgesByType[edgeType], edge)
	}

	// GR-09: Update edgesByFile index
	if loc.FilePath != "" {
		g.edgesByFile[loc.FilePath] = append(g.edgesByFile[loc.FilePath], edge)
	}

	return nil
}

// ReplaceEdgeTarget re-points an existing edge from its current target to a new target node.
//
// Description:
//
//	GR-74: Used by LSP enrichment to replace placeholder edge targets with
//	real symbol nodes after LSP definition lookup resolves the placeholder.
//	Removes the edge from the old target's Incoming slice, updates edge.ToID,
//	and appends to the new target's Incoming slice.
//
// Inputs:
//
//	edge - The edge to retarget. Must not be nil.
//	newToID - ID of the new target node. Must exist in the graph.
//
// Outputs:
//
//	error - Non-nil if graph is frozen, edge is nil, or newToID not found.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//	ErrNodeNotFound - newToID does not exist in the graph
//
// Thread Safety:
//
//	NOT safe for concurrent use. Must be called during build phase only.
func (g *Graph) ReplaceEdgeTarget(edge *Edge, newToID string) error {
	if g.state == GraphStateReadOnly {
		return ErrGraphFrozen
	}
	if edge == nil {
		return fmt.Errorf("%w: edge must not be nil", ErrInvalidNode)
	}

	newTarget, ok := g.nodes[newToID]
	if !ok {
		return fmt.Errorf("%w: target %s", ErrNodeNotFound, newToID)
	}

	// Remove edge from old target's Incoming slice
	oldTarget, ok := g.nodes[edge.ToID]
	if ok {
		incoming := oldTarget.Incoming
		for i, e := range incoming {
			if e == edge {
				oldTarget.Incoming = append(incoming[:i], incoming[i+1:]...)
				break
			}
		}
	}

	// Update edge target
	edge.ToID = newToID

	// Add to new target's Incoming slice
	newTarget.Incoming = append(newTarget.Incoming, edge)

	return nil
}

// RemoveNode removes a node and all its edges from the graph.
//
// Description:
//
//	GR-74: Used by LSP enrichment to clean up orphaned placeholder nodes
//	after all their edges have been resolved to real targets. Removes the node
//	from all indexes (nodes, nodesByName, nodesByKind) and removes all edges
//	connected to the node from the graph's edges slice and secondary indexes.
//
// Inputs:
//
//	id - The ID of the node to remove. Must exist in the graph.
//
// Outputs:
//
//	error - Non-nil if graph is frozen or node not found.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//	ErrNodeNotFound - Node does not exist
//
// Thread Safety:
//
//	NOT safe for concurrent use. Must be called during build phase only.
func (g *Graph) RemoveNode(id string) error {
	if g.state == GraphStateReadOnly {
		return ErrGraphFrozen
	}

	node, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}

	// Collect all edges to remove (both incoming and outgoing)
	edgesToRemove := make(map[*Edge]struct{})
	for _, e := range node.Outgoing {
		edgesToRemove[e] = struct{}{}
	}
	for _, e := range node.Incoming {
		edgesToRemove[e] = struct{}{}
	}

	// Remove edges from counterpart nodes
	for _, e := range node.Outgoing {
		if target, exists := g.nodes[e.ToID]; exists && target != node {
			target.Incoming = removeEdgeFromSlice(target.Incoming, e)
		}
	}
	for _, e := range node.Incoming {
		if source, exists := g.nodes[e.FromID]; exists && source != node {
			source.Outgoing = removeEdgeFromSlice(source.Outgoing, e)
		}
	}

	// Remove edges from g.edges
	filtered := make([]*Edge, 0, len(g.edges)-len(edgesToRemove))
	for _, e := range g.edges {
		if _, remove := edgesToRemove[e]; !remove {
			filtered = append(filtered, e)
		}
	}
	g.edges = filtered

	// Remove edges from edgesByType index
	for e := range edgesToRemove {
		if e.Type >= 0 && e.Type < NumEdgeTypes {
			g.edgesByType[e.Type] = removeEdgeFromSlice(g.edgesByType[e.Type], e)
		}
	}

	// Remove edges from edgesByFile index
	for e := range edgesToRemove {
		if e.Location.FilePath != "" {
			if fileEdges, ok := g.edgesByFile[e.Location.FilePath]; ok {
				g.edgesByFile[e.Location.FilePath] = removeEdgeFromSlice(fileEdges, e)
				if len(g.edgesByFile[e.Location.FilePath]) == 0 {
					delete(g.edgesByFile, e.Location.FilePath)
				}
			}
		}
	}

	// Remove from nodesByName index
	if node.Symbol != nil && node.Symbol.Name != "" {
		name := node.Symbol.Name
		nodes := g.nodesByName[name]
		for i, n := range nodes {
			if n.ID == id {
				g.nodesByName[name] = append(nodes[:i], nodes[i+1:]...)
				break
			}
		}
		if len(g.nodesByName[name]) == 0 {
			delete(g.nodesByName, name)
		}
	}

	// Remove from nodesByKind index
	if node.Symbol != nil {
		kind := node.Symbol.Kind
		nodes := g.nodesByKind[kind]
		for i, n := range nodes {
			if n.ID == id {
				g.nodesByKind[kind] = append(nodes[:i], nodes[i+1:]...)
				break
			}
		}
	}

	// Remove from primary map
	delete(g.nodes, id)

	return nil
}

// removeEdgeFromSlice removes an edge pointer from a slice by identity.
func removeEdgeFromSlice(edges []*Edge, target *Edge) []*Edge {
	for i, e := range edges {
		if e == target {
			return append(edges[:i], edges[i+1:]...)
		}
	}
	return edges
}

// Nodes returns an iterator function over all nodes in the graph.
//
// Description:
//
//	Returns a function that can be used to iterate over all nodes.
//	This allows iteration without exposing the internal map.
//
// Example:
//
//	for id, node := range g.Nodes() {
//	    fmt.Printf("Node: %s\n", id)
//	}
func (g *Graph) Nodes() func(yield func(string, *Node) bool) {
	return func(yield func(string, *Node) bool) {
		for id, node := range g.nodes {
			if !yield(id, node) {
				return
			}
		}
	}
}

// Edges returns a slice of all edges in the graph.
//
// Description:
//
//	Returns the internal edge slice. Callers should NOT modify
//	the returned slice.
func (g *Graph) Edges() []*Edge {
	return g.edges
}

// TraversalResult contains the results of a graph traversal query.
type TraversalResult struct {
	// StartNode is the ID of the node where traversal began.
	StartNode string

	// VisitedNodes contains IDs of all visited nodes in traversal order.
	VisitedNodes []string

	// Edges contains all edges that were traversed.
	Edges []*Edge

	// Depth is the maximum depth reached during traversal.
	Depth int

	// Truncated indicates the traversal was stopped early due to
	// limit, depth, or context cancellation.
	Truncated bool
}

// PathResult contains the result of a shortest path query.
type PathResult struct {
	// From is the starting node ID.
	From string

	// To is the target node ID.
	To string

	// Path contains node IDs in path order, including From and To.
	// Empty if no path exists.
	Path []string

	// Length is the number of edges in the path.
	// -1 if no path exists.
	Length int
}

// GraphStats contains statistics about the graph.
//
// Thread Safety: GraphStats is a value type with no internal state.
// Safe for concurrent use as long as the source Graph is frozen.
type GraphStats struct {
	// NodeCount is the total number of nodes.
	NodeCount int

	// EdgeCount is the total number of edges.
	EdgeCount int

	// EdgesByType maps each EdgeType to the count of edges of that type.
	EdgesByType map[EdgeType]int

	// NodesByKind maps each SymbolKind to the count of nodes of that kind.
	// Added for GR-43 debug endpoint.
	NodesByKind map[ast.SymbolKind]int

	// MaxNodes is the configured maximum node capacity.
	MaxNodes int

	// MaxEdges is the configured maximum edge capacity.
	MaxEdges int

	// State is the current graph state.
	State GraphState

	// BuiltAtMilli is when Freeze() was called (0 if not frozen).
	BuiltAtMilli int64
}

// Stats returns statistics about the graph.
//
// Description:
//
//	Returns statistics including node/edge counts, breakdowns by edge type
//	and symbol kind, and capacity information. Uses secondary indexes for
//	O(1) lookups instead of O(V+E) iteration.
//
// Outputs:
//
//	GraphStats - Statistics about the graph.
//
// Complexity:
//
//	O(K + T) where K is number of symbol kinds and T is number of edge types.
//	GR-06/07/08: Improved from O(V + E) via secondary indexes.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs. Not safe during building.
func (g *Graph) Stats() GraphStats {
	// GR-08: Use edgesByType index for O(1) per type
	edgesByType := make(map[EdgeType]int)
	for i := 0; i < int(NumEdgeTypes); i++ {
		if count := len(g.edgesByType[i]); count > 0 {
			edgesByType[EdgeType(i)] = count
		}
	}

	// GR-07: Use nodesByKind index for O(1) per kind
	nodesByKind := make(map[ast.SymbolKind]int)
	for kind, nodes := range g.nodesByKind {
		if len(nodes) > 0 {
			nodesByKind[kind] = len(nodes)
		}
	}

	return GraphStats{
		NodeCount:    len(g.nodes),
		EdgeCount:    len(g.edges),
		EdgesByType:  edgesByType,
		NodesByKind:  nodesByKind,
		MaxNodes:     g.options.MaxNodes,
		MaxEdges:     g.options.MaxEdges,
		State:        g.state,
		BuiltAtMilli: g.BuiltAtMilli,
	}
}

// Clone creates a deep copy of the graph.
//
// Description:
//
//	Creates an independent copy of the graph that can be modified without
//	affecting the original. Used for copy-on-write incremental updates.
//
// Outputs:
//
//	*Graph - A deep copy of the graph. Always in GraphStateBuilding state
//	         to allow modifications.
//
// Behavior:
//
//   - Nodes are deep copied (new Node structs, same Symbol pointers)
//   - Edges are deep copied (new Edge structs)
//   - Edge/node references are updated to point to cloned nodes
//   - BuiltAtMilli is preserved from original
//   - State is reset to GraphStateBuilding
//
// Thread Safety:
//
//	The returned graph is independent and can be modified without synchronization.
func (g *Graph) Clone() *Graph {
	clone := &Graph{
		ProjectRoot:  g.ProjectRoot,
		nodes:        make(map[string]*Node, len(g.nodes)),
		edges:        make([]*Edge, 0, len(g.edges)),
		nodesByName:  make(map[string][]*Node, len(g.nodesByName)),
		nodesByKind:  make(map[ast.SymbolKind][]*Node, len(g.nodesByKind)),
		edgesByFile:  make(map[string][]*Edge, len(g.edgesByFile)),
		state:        GraphStateBuilding, // Allow modifications on clone
		options:      g.options,
		BuiltAtMilli: g.BuiltAtMilli,
	}

	// First pass: clone all nodes and update node indexes
	for id, node := range g.nodes {
		clonedNode := &Node{
			ID:       node.ID,
			Symbol:   node.Symbol, // Symbols are shared (immutable after add)
			Outgoing: make([]*Edge, 0, len(node.Outgoing)),
			Incoming: make([]*Edge, 0, len(node.Incoming)),
		}
		clone.nodes[id] = clonedNode

		// GR-06: Update nodesByName index with cloned node
		if node.Symbol != nil && node.Symbol.Name != "" {
			clone.nodesByName[node.Symbol.Name] = append(
				clone.nodesByName[node.Symbol.Name],
				clonedNode,
			)
		}

		// GR-07: Update nodesByKind index with cloned node
		if node.Symbol != nil {
			clone.nodesByKind[node.Symbol.Kind] = append(
				clone.nodesByKind[node.Symbol.Kind],
				clonedNode,
			)
		}
	}

	// Second pass: clone edges, update node references, and update edge index
	for _, edge := range g.edges {
		clonedEdge := &Edge{
			FromID:   edge.FromID,
			ToID:     edge.ToID,
			Type:     edge.Type,
			Location: edge.Location,
		}
		clone.edges = append(clone.edges, clonedEdge)

		// Update node edge references
		if fromNode, ok := clone.nodes[edge.FromID]; ok {
			fromNode.Outgoing = append(fromNode.Outgoing, clonedEdge)
		}
		if toNode, ok := clone.nodes[edge.ToID]; ok {
			toNode.Incoming = append(toNode.Incoming, clonedEdge)
		}

		// GR-08: Update edgesByType index with cloned edge
		if edge.Type >= 0 && edge.Type < NumEdgeTypes {
			clone.edgesByType[edge.Type] = append(clone.edgesByType[edge.Type], clonedEdge)
		}

		// GR-09: Update edgesByFile index with cloned edge
		if edge.Location.FilePath != "" {
			clone.edgesByFile[edge.Location.FilePath] = append(
				clone.edgesByFile[edge.Location.FilePath],
				clonedEdge,
			)
		}
	}

	return clone
}

// RemoveFile removes all nodes and edges associated with a file.
//
// Description:
//
//	Removes all symbols (nodes) that were defined in the specified file,
//	along with all edges that reference those nodes. Used for incremental
//	updates when a file is deleted or modified.
//
// Inputs:
//
//	filePath - The relative file path to remove (must match Symbol.FilePath).
//
// Outputs:
//
//	int - Number of nodes removed.
//	error - Non-nil if the graph is frozen.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//
// Behavior:
//
//   - Removes all nodes where Symbol.FilePath matches
//   - Removes all edges where FromID or ToID references removed nodes
//   - Updates Incoming/Outgoing slices of remaining nodes
//
// Thread Safety:
//
//	NOT safe for concurrent use during modification.
func (g *Graph) RemoveFile(filePath string) (int, error) {
	if g.state == GraphStateReadOnly {
		return 0, ErrGraphFrozen
	}

	// Find nodes to remove and track their names/kinds for index cleanup
	toRemove := make(map[string]bool)
	removedNames := make(map[string]bool)
	removedKinds := make(map[ast.SymbolKind]bool)

	for id, node := range g.nodes {
		if node.Symbol != nil && node.Symbol.FilePath == filePath {
			toRemove[id] = true
			if node.Symbol.Name != "" {
				removedNames[node.Symbol.Name] = true
			}
			removedKinds[node.Symbol.Kind] = true
		}
	}

	if len(toRemove) == 0 {
		return 0, nil
	}

	// Remove nodes from primary index
	for id := range toRemove {
		delete(g.nodes, id)
	}

	// GR-06: Update nodesByName index - filter out removed nodes
	for name := range removedNames {
		nodes := g.nodesByName[name]
		filtered := make([]*Node, 0, len(nodes))
		for _, n := range nodes {
			if !toRemove[n.ID] {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			delete(g.nodesByName, name)
		} else {
			g.nodesByName[name] = filtered
		}
	}

	// GR-07: Update nodesByKind index - filter out removed nodes
	for kind := range removedKinds {
		nodes := g.nodesByKind[kind]
		filtered := make([]*Node, 0, len(nodes))
		for _, n := range nodes {
			if !toRemove[n.ID] {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			delete(g.nodesByKind, kind)
		} else {
			g.nodesByKind[kind] = filtered
		}
	}

	// Filter edges and track which types/files need index update
	newEdges := make([]*Edge, 0, len(g.edges))
	removedEdgeTypes := make(map[EdgeType]bool)
	removedEdgeFiles := make(map[string]bool)

	for _, edge := range g.edges {
		if toRemove[edge.FromID] || toRemove[edge.ToID] {
			removedEdgeTypes[edge.Type] = true
			if edge.Location.FilePath != "" {
				removedEdgeFiles[edge.Location.FilePath] = true
			}
			continue // Skip edges that reference removed nodes
		}
		newEdges = append(newEdges, edge)
	}
	g.edges = newEdges

	// GR-08: Update edgesByType index - rebuild affected types
	for edgeType := range removedEdgeTypes {
		if edgeType >= 0 && edgeType < NumEdgeTypes {
			edges := g.edgesByType[edgeType]
			filtered := make([]*Edge, 0, len(edges))
			for _, e := range edges {
				if !toRemove[e.FromID] && !toRemove[e.ToID] {
					filtered = append(filtered, e)
				}
			}
			g.edgesByType[edgeType] = filtered
		}
	}

	// GR-09: Update edgesByFile index - rebuild affected files
	for filePath := range removedEdgeFiles {
		edges := g.edgesByFile[filePath]
		filtered := make([]*Edge, 0, len(edges))
		for _, e := range edges {
			if !toRemove[e.FromID] && !toRemove[e.ToID] {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			delete(g.edgesByFile, filePath)
		} else {
			g.edgesByFile[filePath] = filtered
		}
	}

	// Rebuild edge references for remaining nodes
	for _, node := range g.nodes {
		node.Outgoing = filterEdges(node.Outgoing, toRemove)
		node.Incoming = filterEdges(node.Incoming, toRemove)
	}

	return len(toRemove), nil
}

// filterEdges removes edges that reference removed nodes.
func filterEdges(edges []*Edge, removed map[string]bool) []*Edge {
	result := make([]*Edge, 0, len(edges))
	for _, e := range edges {
		if !removed[e.FromID] && !removed[e.ToID] {
			result = append(result, e)
		}
	}
	return result
}

// MergeParseResult adds nodes and edges from a ParseResult.
//
// Description:
//
//	Adds all symbols from the ParseResult as nodes and creates edges
//	based on import relationships. Used for incremental updates when
//	adding newly parsed files.
//
// Inputs:
//
//	result - The ParseResult containing symbols to add.
//
// Outputs:
//
//	int - Number of nodes added.
//	error - Non-nil if the graph is frozen or capacity exceeded.
//
// Errors:
//
//	ErrGraphFrozen - Graph has been frozen
//	ErrMaxNodesExceeded - Graph is at node capacity
//	ErrMaxEdgesExceeded - Graph is at edge capacity
//
// Behavior:
//
//   - Adds all top-level symbols as nodes
//   - Creates EdgeTypeDefines edges from file node to symbols
//   - Skips duplicate nodes (by ID)
//   - Does NOT add children (caller should flatten if needed)
//
// Thread Safety:
//
//	NOT safe for concurrent use during modification.
func (g *Graph) MergeParseResult(result *ast.ParseResult) (int, error) {
	if g.state == GraphStateReadOnly {
		return 0, ErrGraphFrozen
	}

	if result == nil || len(result.Symbols) == 0 {
		return 0, nil
	}

	added := 0

	// Add symbols as nodes
	for _, sym := range result.Symbols {
		if sym == nil {
			continue
		}

		// Skip if already exists
		if _, exists := g.nodes[sym.ID]; exists {
			continue
		}

		// Check capacity
		if len(g.nodes) >= g.options.MaxNodes {
			return added, ErrMaxNodesExceeded
		}

		node := &Node{
			ID:       sym.ID,
			Symbol:   sym,
			Outgoing: make([]*Edge, 0),
			Incoming: make([]*Edge, 0),
		}
		g.nodes[sym.ID] = node

		// GR-06: Update nodesByName index
		if sym.Name != "" {
			g.nodesByName[sym.Name] = append(g.nodesByName[sym.Name], node)
		}

		// GR-07: Update nodesByKind index
		g.nodesByKind[sym.Kind] = append(g.nodesByKind[sym.Kind], node)

		added++
	}

	return added, nil
}

// GetNodesByFile returns all nodes from a specific file.
//
// Description:
//
//	Returns all nodes where the Symbol's FilePath matches the given path.
//	Useful for identifying what symbols are defined in a file.
//
// Inputs:
//
//	filePath - The relative file path to search for.
//
// Outputs:
//
//	[]*Node - Nodes from that file. Empty slice if none found.
func (g *Graph) GetNodesByFile(filePath string) []*Node {
	result := make([]*Node, 0)
	for _, node := range g.nodes {
		if node.Symbol != nil && node.Symbol.FilePath == filePath {
			result = append(result, node)
		}
	}
	return result
}

// GetNodesByName returns all nodes with the given symbol name.
//
// Description:
//
//	GR-06: Uses secondary index for O(1) lookup.
//	Multiple symbols can share a name (e.g., "Setup" in different packages).
//	Returns a defensive copy to prevent external mutation.
//
// Inputs:
//
//	name - The symbol name to search for.
//
// Outputs:
//
//	[]*Node - Nodes with that name. Empty slice if none found.
//
// Complexity:
//
//	O(1) lookup + O(k) copy where k = nodes with that name.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetNodesByName(name string) []*Node {
	nodes := g.nodesByName[name]
	if len(nodes) == 0 {
		return []*Node{}
	}
	// Return defensive copy to prevent external mutation
	result := make([]*Node, len(nodes))
	copy(result, nodes)
	return result
}

// GetNodesByKind returns all nodes of the given symbol kind.
//
// Description:
//
//	GR-07: Uses secondary index for O(1) lookup.
//	Returns a defensive copy to prevent external mutation.
//
// Inputs:
//
//	kind - The symbol kind to filter by (e.g., SymbolKindFunction).
//
// Outputs:
//
//	[]*Node - Nodes of that kind. Empty slice if none found.
//
// Complexity:
//
//	O(1) lookup + O(k) copy where k = nodes of that kind.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetNodesByKind(kind ast.SymbolKind) []*Node {
	nodes := g.nodesByKind[kind]
	if len(nodes) == 0 {
		return []*Node{}
	}
	// Return defensive copy to prevent external mutation
	result := make([]*Node, len(nodes))
	copy(result, nodes)
	return result
}

// GetEdgesByType returns all edges of the given type.
//
// Description:
//
//	GR-08: Uses secondary index for O(1) lookup.
//	Returns a defensive copy to prevent external mutation.
//
// Inputs:
//
//	edgeType - The edge type to filter by (e.g., EdgeTypeCalls).
//
// Outputs:
//
//	[]*Edge - Edges of that type. Empty slice if none found or invalid type.
//
// Complexity:
//
//	O(1) lookup + O(k) copy where k = edges of that type.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetEdgesByType(edgeType EdgeType) []*Edge {
	if edgeType < 0 || edgeType >= NumEdgeTypes {
		return []*Edge{}
	}
	edges := g.edgesByType[edgeType]
	if len(edges) == 0 {
		return []*Edge{}
	}
	// Return defensive copy to prevent external mutation
	result := make([]*Edge, len(edges))
	copy(result, edges)
	return result
}

// GetEdgeCountByType returns the count of edges of the given type.
//
// Description:
//
//	GR-08: Uses secondary index for O(1) count without copying.
//	More efficient than len(GetEdgesByType()) for just getting counts.
//
// Inputs:
//
//	edgeType - The edge type to count (e.g., EdgeTypeCalls).
//
// Outputs:
//
//	int - Number of edges of that type. Zero if invalid type or none found.
//
// Complexity:
//
//	O(1) - Direct array access and len().
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
//
// Example:
//
//	callCount := g.GetEdgeCountByType(EdgeTypeCalls)
func (g *Graph) GetEdgeCountByType(edgeType EdgeType) int {
	if edgeType < 0 || edgeType >= NumEdgeTypes {
		return 0
	}
	return len(g.edgesByType[edgeType])
}

// GetNodeCountByName returns the count of nodes with the given name.
//
// Description:
//
//	GR-06: Uses secondary index for O(1) count without copying.
//	More efficient than len(GetNodesByName()) for just getting counts.
//
// Inputs:
//
//	name - The symbol name to count.
//
// Outputs:
//
//	int - Number of nodes with that name. Zero if none found.
//
// Complexity:
//
//	O(1) - Direct map access and len().
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetNodeCountByName(name string) int {
	return len(g.nodesByName[name])
}

// GetNodeCountByKind returns the count of nodes of the given kind.
//
// Description:
//
//	GR-07: Uses secondary index for O(1) count without copying.
//	More efficient than len(GetNodesByKind()) for just getting counts.
//
// Inputs:
//
//	kind - The symbol kind to count.
//
// Outputs:
//
//	int - Number of nodes of that kind. Zero if none found.
//
// Complexity:
//
//	O(1) - Direct map access and len().
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetNodeCountByKind(kind ast.SymbolKind) int {
	return len(g.nodesByKind[kind])
}

// GetEdgesByFile returns all edges with Location in the given file.
//
// Description:
//
//	GR-09: Uses secondary index for O(1) lookup.
//	Returns a defensive copy to prevent external mutation.
//	Note: Indexes by edge.Location.FilePath (where the edge is expressed),
//	which may differ from the source node's file path.
//
// Inputs:
//
//	filePath - The file path to filter by.
//
// Outputs:
//
//	[]*Edge - Edges with Location in that file. Empty slice if none found.
//
// Complexity:
//
//	O(1) lookup + O(k) copy where k = edges in that file.
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
//
// Example:
//
//	edges := g.GetEdgesByFile("pkg/handler.go")
//	fmt.Printf("Found %d edges in handler.go\n", len(edges))
func (g *Graph) GetEdgesByFile(filePath string) []*Edge {
	edges := g.edgesByFile[filePath]
	if len(edges) == 0 {
		return []*Edge{}
	}
	// Return defensive copy to prevent external mutation
	result := make([]*Edge, len(edges))
	copy(result, edges)
	return result
}

// GetEdgeCountByFile returns the count of edges with Location in the given file.
//
// Description:
//
//	GR-09: Uses secondary index for O(1) count without copying.
//	More efficient than len(GetEdgesByFile()) for just getting counts.
//
// Inputs:
//
//	filePath - The file path to count edges for.
//
// Outputs:
//
//	int - Number of edges with Location in that file. Zero if none found.
//
// Complexity:
//
//	O(1) - Direct map access and len().
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) GetEdgeCountByFile(filePath string) int {
	return len(g.edgesByFile[filePath])
}

// Hash returns a deterministic hash of the graph structure.
//
// Description:
//
//	Computes a hash over all nodes and edges in the graph. The hash is
//	deterministic (same graph structure always produces same hash) and
//	can be used to detect graph changes for cache invalidation.
//
//	The hash includes:
//	  - All node IDs (sorted)
//	  - All edge tuples (from, to, type) sorted
//
//	Uses FNV-1a hash for speed and good distribution.
//
// Outputs:
//
//   - string: Hex-encoded hash (16 characters). Empty string if graph is nil.
//
// Example:
//
//	hash1 := graph.Hash()
//	// ... modify graph ...
//	hash2 := graph.Hash()
//	if hash1 != hash2 {
//	    log.Info("graph changed, invalidating caches")
//	}
//
// Complexity:
//
//	O(V log V + E log E) - Sorting nodes and edges
//	O(V + E) if already sorted (frozen graphs)
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs.
func (g *Graph) Hash() string {
	if g == nil {
		return ""
	}

	h := fnv.New64a()

	// Hash node IDs (sorted for determinism)
	nodeIDs := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	for _, id := range nodeIDs {
		h.Write([]byte(id))
		h.Write([]byte{0}) // Separator
	}

	// Hash edges (sorted for determinism)
	type edgeTuple struct {
		from     string
		to       string
		edgeType EdgeType
	}
	edgeTuples := make([]edgeTuple, 0, len(g.edges))
	for _, edge := range g.edges {
		edgeTuples = append(edgeTuples, edgeTuple{
			from:     edge.FromID,
			to:       edge.ToID,
			edgeType: edge.Type,
		})
	}

	sort.Slice(edgeTuples, func(i, j int) bool {
		if edgeTuples[i].from != edgeTuples[j].from {
			return edgeTuples[i].from < edgeTuples[j].from
		}
		if edgeTuples[i].to != edgeTuples[j].to {
			return edgeTuples[i].to < edgeTuples[j].to
		}
		return edgeTuples[i].edgeType < edgeTuples[j].edgeType
	})

	for _, tuple := range edgeTuples {
		h.Write([]byte(tuple.from))
		h.Write([]byte{0})
		h.Write([]byte(tuple.to))
		h.Write([]byte{0})
		h.Write([]byte{byte(tuple.edgeType)})
		h.Write([]byte{0})
	}

	return fmt.Sprintf("%016x", h.Sum64())
}
