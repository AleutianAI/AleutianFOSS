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
	"context"
	"fmt"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Query configuration limits.
const (
	// DefaultQueryLimit is the default maximum number of results.
	DefaultQueryLimit = 1000

	// MaxQueryLimit is the maximum allowed limit.
	MaxQueryLimit = 10000

	// DefaultMaxDepth is the default maximum traversal depth.
	DefaultMaxDepth = 10

	// MaxTraversalDepth is the maximum allowed traversal depth.
	MaxTraversalDepth = 100

	// contextCheckInterval is how often to check context during traversal.
	contextCheckInterval = 100
)

// QueryResult wraps query results with metadata.
type QueryResult struct {
	// Symbols contains the matching symbols.
	Symbols []*ast.Symbol

	// Truncated is true if limit was reached or context was cancelled.
	Truncated bool

	// Duration is the query execution time.
	Duration time.Duration
}

// InheritanceQueryResult separates direct callers from inherited callers.
//
// Description:
//
//	Returned by FindCallersWithInheritance to allow the caller to distinguish
//	between direct callers of the queried method and callers that reach it
//	through parent class methods in the inheritance chain.
//
// Example:
//
//	result, err := g.FindCallersWithInheritance(ctx, plotRenderID, parentIDs)
//	// result.DirectCallers has callers of Plot.render
//	// result.InheritedCallers["Component.render ID"] has callers of Component.render
type InheritanceQueryResult struct {
	// DirectCallers are callers of the primary (queried) method.
	DirectCallers *QueryResult

	// InheritedCallers maps parent method ID to callers of that parent method.
	// Only includes callers NOT already in DirectCallers (deduplicated).
	InheritedCallers map[string]*QueryResult

	// Truncated is true if limit was reached or context was cancelled.
	Truncated bool

	// Duration is the total query execution time.
	Duration time.Duration
}

// AllCallers returns a flat deduplicated list of all callers (direct + inherited).
//
// Description:
//
//	Convenience method that merges DirectCallers and InheritedCallers into a
//	single QueryResult. Useful when the caller doesn't need to distinguish
//	between direct and inherited callers.
func (r *InheritanceQueryResult) AllCallers() *QueryResult {
	if r == nil {
		return &QueryResult{}
	}
	merged := &QueryResult{
		Truncated: r.Truncated,
		Duration:  r.Duration,
	}
	if r.DirectCallers != nil {
		merged.Symbols = append(merged.Symbols, r.DirectCallers.Symbols...)
		merged.Truncated = merged.Truncated || r.DirectCallers.Truncated
	}
	for _, qr := range r.InheritedCallers {
		if qr != nil {
			merged.Symbols = append(merged.Symbols, qr.Symbols...)
			merged.Truncated = merged.Truncated || qr.Truncated
		}
	}
	return merged
}

// TotalCallerCount returns the total number of callers across all levels.
func (r *InheritanceQueryResult) TotalCallerCount() int {
	if r == nil {
		return 0
	}
	count := 0
	if r.DirectCallers != nil {
		count += len(r.DirectCallers.Symbols)
	}
	for _, qr := range r.InheritedCallers {
		if qr != nil {
			count += len(qr.Symbols)
		}
	}
	return count
}

// QueryOptions configures query behavior.
type QueryOptions struct {
	// Limit is the maximum number of results (default: 1000, max: 10000).
	Limit int

	// MaxDepth is the maximum traversal depth (default: 10, max: 100).
	MaxDepth int

	// Timeout is the per-query timeout (0 = use context deadline).
	Timeout time.Duration
}

// DefaultQueryOptions returns sensible defaults for queries.
func DefaultQueryOptions() QueryOptions {
	return QueryOptions{
		Limit:    DefaultQueryLimit,
		MaxDepth: DefaultMaxDepth,
	}
}

// QueryOption is a functional option for configuring queries.
type QueryOption func(*QueryOptions)

// WithLimit sets the maximum number of results.
//
// If n <= 0, uses default (1000).
// If n > 10000, clamps to 10000.
func WithLimit(n int) QueryOption {
	return func(o *QueryOptions) {
		if n <= 0 {
			o.Limit = DefaultQueryLimit
		} else if n > MaxQueryLimit {
			o.Limit = MaxQueryLimit
		} else {
			o.Limit = n
		}
	}
}

// WithMaxDepth sets the maximum traversal depth.
//
// If d < 0, uses default (10).
// If d > 100, clamps to 100.
func WithMaxDepth(d int) QueryOption {
	return func(o *QueryOptions) {
		if d < 0 {
			o.MaxDepth = DefaultMaxDepth
		} else if d > MaxTraversalDepth {
			o.MaxDepth = MaxTraversalDepth
		} else {
			o.MaxDepth = d
		}
	}
}

// WithTimeout sets the per-query timeout.
func WithTimeout(d time.Duration) QueryOption {
	return func(o *QueryOptions) {
		o.Timeout = d
	}
}

// applyOptions applies functional options and returns the configured options.
func applyOptions(opts []QueryOption) QueryOptions {
	options := DefaultQueryOptions()
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// Validate checks that the graph is in a consistent state for querying.
//
// Description:
//
//	Verifies all edges reference existing nodes. Should be called once
//	after build, before queries. Queries will return error if validation
//	fails.
//
// Outputs:
//
//	error - Non-nil if graph is corrupt (dangling edges)
//
// Example:
//
//	if err := graph.Validate(); err != nil {
//	    return fmt.Errorf("graph corrupt: %w", err)
//	}
func (g *Graph) Validate() error {
	for i, edge := range g.edges {
		if _, ok := g.nodes[edge.FromID]; !ok {
			return fmt.Errorf("edge[%d]: source node %q not found", i, edge.FromID)
		}
		if _, ok := g.nodes[edge.ToID]; !ok {
			return fmt.Errorf("edge[%d]: target node %q not found", i, edge.ToID)
		}
	}
	return nil
}

// FindCallersByID returns all symbols that call the given function/method.
//
// Description:
//
//	Finds all functions/methods that have a CALLS edge to the target.
//	Uses symbol ID for unambiguous lookup.
//
// Inputs:
//
//	ctx - Context for cancellation
//	symbolID - ID of the function/method to find callers for
//	opts - Query options (Limit, Timeout)
//
// Outputs:
//
//	*QueryResult - Symbols that call the target (empty if none), with metadata
//	error - Non-nil if context error occurs
//
// Limitations:
//
//	Only finds direct callers (not transitive)
//	May miss callers through function pointers/interfaces
func (g *Graph) FindCallersByID(ctx context.Context, symbolID string, opts ...QueryOption) (*QueryResult, error) {
	start := time.Now()
	options := applyOptions(opts)

	result := &QueryResult{
		Symbols: make([]*ast.Symbol, 0),
	}

	node, ok := g.nodes[symbolID]
	if !ok {
		// Node not found - return empty result (not error)
		result.Duration = time.Since(start)
		return result, nil
	}

	for _, edge := range node.Incoming {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.Duration = time.Since(start)
			return result, nil
		}

		if edge.Type != EdgeTypeCalls {
			continue
		}

		if len(result.Symbols) >= options.Limit {
			result.Truncated = true
			break
		}

		callerNode, exists := g.nodes[edge.FromID]
		if exists {
			result.Symbols = append(result.Symbols, callerNode.Symbol)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// FindCallersWithInheritance returns callers of a method AND callers of the same method
// on parent classes in the inheritance chain.
//
// Description:
//
//	When a child class overrides a parent method, calls through the parent class
//	(e.g., this.renderImmediately() inside Component) create edges to the parent's
//	method, not the child's. This function collects callers from the entire
//	inheritance chain so that callers of both Plot.renderImmediately AND
//	Component.renderImmediately are returned.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	symbolID - ID of the target method (e.g., Plot.renderImmediately).
//	parentMethodIDs - IDs of the same-named method on parent classes.
//	opts - Query options (Limit, Timeout).
//
// Outputs:
//
//	*QueryResult - Combined callers from all inheritance levels, deduplicated by ID.
//	error - Non-nil if context error occurs.
func (g *Graph) FindCallersWithInheritance(ctx context.Context, symbolID string, parentMethodIDs []string, opts ...QueryOption) (*InheritanceQueryResult, error) {
	start := time.Now()
	options := applyOptions(opts)

	result := &InheritanceQueryResult{
		DirectCallers:    &QueryResult{Symbols: make([]*ast.Symbol, 0)},
		InheritedCallers: make(map[string]*QueryResult),
	}

	seen := make(map[string]bool)
	totalCount := 0

	// Phase 1: Direct callers of the primary method
	if node, ok := g.nodes[symbolID]; ok {
		for _, edge := range node.Incoming {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				result.Duration = time.Since(start)
				return result, nil
			}
			if edge.Type != EdgeTypeCalls {
				continue
			}
			if seen[edge.FromID] {
				continue
			}
			seen[edge.FromID] = true
			if totalCount >= options.Limit {
				result.Truncated = true
				break
			}
			if callerNode, exists := g.nodes[edge.FromID]; exists {
				result.DirectCallers.Symbols = append(result.DirectCallers.Symbols, callerNode.Symbol)
				totalCount++
			}
		}
	}

	// Phase 2: Inherited callers from parent methods (deduplicated against direct)
	for _, parentID := range parentMethodIDs {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.Duration = time.Since(start)
			return result, nil
		}

		node, ok := g.nodes[parentID]
		if !ok {
			continue
		}

		parentResult := &QueryResult{Symbols: make([]*ast.Symbol, 0)}

		for _, edge := range node.Incoming {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				result.Duration = time.Since(start)
				return result, nil
			}
			if edge.Type != EdgeTypeCalls {
				continue
			}
			if seen[edge.FromID] {
				continue
			}
			seen[edge.FromID] = true
			if totalCount >= options.Limit {
				result.Truncated = true
				break
			}
			if callerNode, exists := g.nodes[edge.FromID]; exists {
				parentResult.Symbols = append(parentResult.Symbols, callerNode.Symbol)
				totalCount++
			}
		}

		if len(parentResult.Symbols) > 0 {
			result.InheritedCallers[parentID] = parentResult
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// FindCalleesByID returns all symbols called by the given function/method.
//
// Description:
//
//	Finds all functions/methods that the source has CALLS edges to.
//	Uses symbol ID for unambiguous lookup.
//
// Inputs:
//
//	ctx - Context for cancellation
//	symbolID - ID of the function/method to find callees for
//	opts - Query options (Limit, Timeout)
//
// Outputs:
//
//	*QueryResult - Symbols called by the source (empty if none), with metadata
//	error - Non-nil if context error occurs
func (g *Graph) FindCalleesByID(ctx context.Context, symbolID string, opts ...QueryOption) (*QueryResult, error) {
	start := time.Now()
	options := applyOptions(opts)

	result := &QueryResult{
		Symbols: make([]*ast.Symbol, 0),
	}

	node, ok := g.nodes[symbolID]
	if !ok {
		result.Duration = time.Since(start)
		return result, nil
	}

	seen := make(map[string]bool)
	for _, edge := range node.Outgoing {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.Duration = time.Since(start)
			return result, nil
		}

		if edge.Type != EdgeTypeCalls {
			continue
		}

		if len(result.Symbols) >= options.Limit {
			result.Truncated = true
			break
		}

		calleeNode, exists := g.nodes[edge.ToID]
		if exists && !seen[calleeNode.Symbol.ID] {
			seen[calleeNode.Symbol.ID] = true
			result.Symbols = append(result.Symbols, calleeNode.Symbol)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// FindImplementationsByID returns all types that implement or extend the given type.
//
// Description:
//
//	Finds all types that have an IMPLEMENTS or EMBEDS edge to the target.
//	IMPLEMENTS edges come from Go interface satisfaction and TS implements clauses.
//	EMBEDS edges come from Python/JS/TS class extends and Go struct embedding.
//	Uses target ID for unambiguous lookup. Deduplicates results when a type
//	has both edge types to the same target.
//
// Inputs:
//
//	ctx - Context for cancellation
//	interfaceID - ID of the interface, class, or struct to find implementers for
//	opts - Query options (Limit, Timeout)
//
// Outputs:
//
//	*QueryResult - Types implementing/extending the target (empty if none)
//	error - Non-nil if context error occurs
func (g *Graph) FindImplementationsByID(ctx context.Context, interfaceID string, opts ...QueryOption) (*QueryResult, error) {
	start := time.Now()
	options := applyOptions(opts)

	result := &QueryResult{
		Symbols: make([]*ast.Symbol, 0),
	}

	node, ok := g.nodes[interfaceID]
	if !ok {
		result.Duration = time.Since(start)
		return result, nil
	}

	// IT-03 C-3b: Check both EdgeTypeImplements (Go interfaces, TS implements)
	// AND EdgeTypeEmbeds (Python/JS/TS class extends, Go struct embedding).
	// Class inheritance creates EdgeTypeEmbeds edges via extractEmbedsEdges
	// in the builder, not EdgeTypeImplements.
	seen := make(map[string]bool)
	for _, edge := range node.Incoming {
		if err := ctx.Err(); err != nil {
			result.Truncated = true
			result.Duration = time.Since(start)
			return result, nil
		}

		if edge.Type != EdgeTypeImplements && edge.Type != EdgeTypeEmbeds {
			continue
		}

		if len(result.Symbols) >= options.Limit {
			result.Truncated = true
			break
		}

		implNode, exists := g.nodes[edge.FromID]
		if exists && implNode.Symbol != nil && !seen[implNode.ID] {
			seen[implNode.ID] = true
			result.Symbols = append(result.Symbols, implNode.Symbol)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// FindReferencesByID returns all locations where the given symbol is referenced.
//
// Description:
//
//	Finds all incoming edges to the symbol and returns their locations.
//	This includes calls, type references, etc.
//
// Inputs:
//
//	ctx - Context for cancellation
//	symbolID - ID of the symbol to find references for
//	opts - Query options (Limit, Timeout)
//
// Outputs:
//
//	[]ast.Location - Locations where the symbol is referenced
//	error - Non-nil if context error occurs
func (g *Graph) FindReferencesByID(ctx context.Context, symbolID string, opts ...QueryOption) ([]ast.Location, error) {
	options := applyOptions(opts)

	node, ok := g.nodes[symbolID]
	if !ok {
		return []ast.Location{}, nil
	}

	locations := make([]ast.Location, 0)

	// IT-06d Bug 10: Two-pass edge collection ensures EdgeTypeReferences and
	// EdgeTypeImplements edges appear before EdgeTypeCalls edges, regardless of
	// graph insertion order. Edges are inserted in builder call order: extractCallEdges
	// runs before extractTypeRefEdges, so for highly-called symbols (e.g., pandas.Series
	// with hundreds of asv_bench EdgeTypeCalls), the call edges fill the fetch window and
	// annotation-based references (core/frame.py etc.) are never returned.
	//
	// Pass 1: annotation-based references (most semantically informative).
	for _, edge := range node.Incoming {
		if err := ctx.Err(); err != nil {
			return locations, nil
		}
		if len(locations) >= options.Limit {
			return locations, nil
		}
		if edge.Type == EdgeTypeReferences || edge.Type == EdgeTypeImplements {
			locations = append(locations, edge.Location)
		}
	}

	// Pass 2: call-based references (constructor calls, direct invocations).
	for _, edge := range node.Incoming {
		if err := ctx.Err(); err != nil {
			return locations, nil
		}
		if len(locations) >= options.Limit {
			return locations, nil
		}
		if edge.Type != EdgeTypeReferences && edge.Type != EdgeTypeImplements {
			locations = append(locations, edge.Location)
		}
	}

	return locations, nil
}

// findSymbolsByName returns all symbols matching the given name.
//
// GR-06: Uses nodesByName secondary index for O(1) lookup instead of O(V) scan.
func (g *Graph) findSymbolsByName(name string) []*Node {
	// Use secondary index for O(1) lookup
	return g.GetNodesByName(name)
}

// FindCallersByName returns callers for all symbols matching the given name.
//
// Description:
//
//	When multiple symbols have the same name (e.g., Setup in different packages),
//	this returns callers for each, keyed by symbol ID.
//
// Inputs:
//
//	ctx - Context for cancellation
//	name - Symbol name to search for
//	opts - Query options (Limit per symbol, Timeout)
//
// Outputs:
//
//	map[string]*QueryResult - Symbol ID → callers of that symbol
//	error - Non-nil if context error occurs
func (g *Graph) FindCallersByName(ctx context.Context, name string, opts ...QueryOption) (map[string]*QueryResult, error) {
	results := make(map[string]*QueryResult)

	matches := g.findSymbolsByName(name)
	for _, node := range matches {
		if err := ctx.Err(); err != nil {
			return results, nil
		}

		result, err := g.FindCallersByID(ctx, node.ID, opts...)
		if err != nil {
			return results, err
		}
		results[node.ID] = result
	}

	return results, nil
}

// FindCalleesByName returns callees for all symbols matching the given name.
//
// Description:
//
//	When multiple symbols have the same name, this returns callees
//	for each, keyed by symbol ID.
//
// Inputs:
//
//	ctx - Context for cancellation
//	name - Symbol name to search for
//	opts - Query options (Limit per symbol, Timeout)
//
// Outputs:
//
//	map[string]*QueryResult - Symbol ID → callees of that symbol
//	error - Non-nil if context error occurs
func (g *Graph) FindCalleesByName(ctx context.Context, name string, opts ...QueryOption) (map[string]*QueryResult, error) {
	results := make(map[string]*QueryResult)

	matches := g.findSymbolsByName(name)
	for _, node := range matches {
		if err := ctx.Err(); err != nil {
			return results, nil
		}

		result, err := g.FindCalleesByID(ctx, node.ID, opts...)
		if err != nil {
			return results, err
		}
		results[node.ID] = result
	}

	return results, nil
}

// FindImplementationsByName returns implementations for all types matching the given name.
//
// Description:
//
//	When multiple types (interfaces, classes, structs) share the same name,
//	this returns implementers/subclasses for each, keyed by type ID.
//	Non-type symbols (functions, variables, etc.) are filtered out.
//
// Inputs:
//
//	ctx - Context for cancellation
//	name - Interface/class/struct name to search for
//	opts - Query options (Limit per target type, Timeout)
//
// Outputs:
//
//	map[string]*QueryResult - Type ID → implementers/subclasses of that type
//	error - Non-nil if context error occurs
func (g *Graph) FindImplementationsByName(ctx context.Context, name string, opts ...QueryOption) (map[string]*QueryResult, error) {
	results := make(map[string]*QueryResult)

	matches := g.findSymbolsByName(name)
	for _, node := range matches {
		if err := ctx.Err(); err != nil {
			return results, nil
		}

		// IT-03 C-3b: Accept interfaces, classes, and structs as valid targets.
		// Interfaces have EdgeTypeImplements edges from concrete types.
		// Classes/structs have EdgeTypeEmbeds edges from subclasses/embedding types.
		if node.Symbol != nil {
			switch node.Symbol.Kind {
			case ast.SymbolKindInterface, ast.SymbolKindClass, ast.SymbolKindStruct:
				result, err := g.FindImplementationsByID(ctx, node.ID, opts...)
				if err != nil {
					return results, err
				}
				results[node.ID] = result
			}
		}
	}

	return results, nil
}

// FindImporters returns all file paths that import the given package.
//
// Description:
//
//	Finds all files that have an IMPORTS edge to nodes in the given package.
//
// Inputs:
//
//	ctx - Context for cancellation
//	packagePath - Package path to find importers for
//	opts - Query options (Limit, Timeout)
//
// Outputs:
//
//	[]string - File paths that import the package
//	error - Non-nil if context error occurs
func (g *Graph) FindImporters(ctx context.Context, packagePath string, opts ...QueryOption) ([]string, error) {
	options := applyOptions(opts)
	filePaths := make([]string, 0)
	seen := make(map[string]bool)

	for _, edge := range g.edges {
		if err := ctx.Err(); err != nil {
			return filePaths, nil
		}

		if edge.Type != EdgeTypeImports {
			continue
		}

		// Check if the target is in the package we're looking for
		targetNode, ok := g.nodes[edge.ToID]
		if !ok {
			continue
		}

		if targetNode.Symbol != nil && targetNode.Symbol.Package == packagePath {
			// Get the file path from the source
			sourceNode, ok := g.nodes[edge.FromID]
			if ok && sourceNode.Symbol != nil {
				filePath := sourceNode.Symbol.FilePath
				if !seen[filePath] {
					seen[filePath] = true
					filePaths = append(filePaths, filePath)

					if len(filePaths) >= options.Limit {
						break
					}
				}
			}
		}
	}

	return filePaths, nil
}

// GetCallGraph returns the call tree rooted at a function.
//
// Description:
//
//	Performs iterative BFS traversal following CALLS edges up to maxDepth.
//	Uses iterative approach (not recursive) to handle deep graphs without
//	stack overflow.
//
// Inputs:
//
//	ctx - Context for cancellation (checked every 100 nodes)
//	symbolID - Root function ID (must be unambiguous)
//	opts - Query options including MaxDepth (default: 10, max: 100)
//
// Outputs:
//
//	*TraversalResult - Visited nodes and edges, with Truncated flag
//	error - Non-nil if root not found
func (g *Graph) GetCallGraph(ctx context.Context, symbolID string, opts ...QueryOption) (*TraversalResult, error) {
	options := applyOptions(opts)

	_, ok := g.nodes[symbolID]
	if !ok {
		return nil, fmt.Errorf("root node not found: %s", symbolID)
	}

	result := &TraversalResult{
		StartNode:    symbolID,
		VisitedNodes: make([]string, 0),
		Edges:        make([]*Edge, 0),
	}

	visited := make(map[string]bool)
	type queueItem struct {
		nodeID string
		depth  int
	}
	queue := []queueItem{{symbolID, 0}}
	visited[symbolID] = true
	checkCounter := 0

	for len(queue) > 0 {
		checkCounter++
		if checkCounter%contextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				return result, nil
			}
		}

		item := queue[0]
		queue = queue[1:]

		result.VisitedNodes = append(result.VisitedNodes, item.nodeID)
		if item.depth > result.Depth {
			result.Depth = item.depth
		}

		if len(result.VisitedNodes) >= options.Limit {
			result.Truncated = true
			break
		}

		if item.depth >= options.MaxDepth {
			continue
		}

		node := g.nodes[item.nodeID]
		for _, edge := range node.Outgoing {
			if edge.Type != EdgeTypeCalls {
				continue
			}
			if visited[edge.ToID] {
				continue // Cycle detection
			}
			visited[edge.ToID] = true
			result.Edges = append(result.Edges, edge)
			queue = append(queue, queueItem{edge.ToID, item.depth + 1})
		}
	}

	return result, nil
}

// GetReverseCallGraph returns the callers tree rooted at a function.
//
// Description:
//
//	Performs iterative BFS traversal following CALLS edges backwards
//	(finding callers) up to maxDepth.
//
// Inputs:
//
//	ctx - Context for cancellation (checked every 100 nodes)
//	symbolID - Root function ID (must be unambiguous)
//	opts - Query options including MaxDepth (default: 10, max: 100)
//
// Outputs:
//
//	*TraversalResult - Visited nodes and edges, with Truncated flag
//	error - Non-nil if root not found
func (g *Graph) GetReverseCallGraph(ctx context.Context, symbolID string, opts ...QueryOption) (*TraversalResult, error) {
	options := applyOptions(opts)

	_, ok := g.nodes[symbolID]
	if !ok {
		return nil, fmt.Errorf("root node not found: %s", symbolID)
	}

	result := &TraversalResult{
		StartNode:    symbolID,
		VisitedNodes: make([]string, 0),
		Edges:        make([]*Edge, 0),
	}

	visited := make(map[string]bool)
	type queueItem struct {
		nodeID string
		depth  int
	}
	queue := []queueItem{{symbolID, 0}}
	visited[symbolID] = true
	checkCounter := 0

	for len(queue) > 0 {
		checkCounter++
		if checkCounter%contextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				return result, nil
			}
		}

		item := queue[0]
		queue = queue[1:]

		result.VisitedNodes = append(result.VisitedNodes, item.nodeID)
		if item.depth > result.Depth {
			result.Depth = item.depth
		}

		if len(result.VisitedNodes) >= options.Limit {
			result.Truncated = true
			break
		}

		if item.depth >= options.MaxDepth {
			continue
		}

		node := g.nodes[item.nodeID]
		for _, edge := range node.Incoming {
			if edge.Type != EdgeTypeCalls {
				continue
			}
			if visited[edge.FromID] {
				continue // Cycle detection
			}
			visited[edge.FromID] = true
			result.Edges = append(result.Edges, edge)
			queue = append(queue, queueItem{edge.FromID, item.depth + 1})
		}
	}

	return result, nil
}

// GetDependencyTree returns the dependency tree for a file.
//
// Description:
//
//	Performs iterative BFS traversal following IMPORTS edges up to maxDepth.
//	Returns all transitive dependencies.
//
// Inputs:
//
//	ctx - Context for cancellation (checked every 100 nodes)
//	filePath - File path to find dependencies for
//	opts - Query options including MaxDepth (default: 10, max: 100)
//
// Outputs:
//
//	*TraversalResult - Visited nodes and edges, with Truncated flag
//	error - Non-nil if file not found in graph
func (g *Graph) GetDependencyTree(ctx context.Context, filePath string, opts ...QueryOption) (*TraversalResult, error) {
	options := applyOptions(opts)

	// Find all nodes in this file
	var startNodes []string
	for id, node := range g.nodes {
		if node.Symbol != nil && node.Symbol.FilePath == filePath {
			startNodes = append(startNodes, id)
		}
	}

	if len(startNodes) == 0 {
		return nil, fmt.Errorf("no nodes found for file: %s", filePath)
	}

	result := &TraversalResult{
		StartNode:    filePath, // Use file path as start identifier
		VisitedNodes: make([]string, 0),
		Edges:        make([]*Edge, 0),
	}

	visited := make(map[string]bool)
	type queueItem struct {
		nodeID string
		depth  int
	}

	// Start BFS from all nodes in the file
	queue := make([]queueItem, 0, len(startNodes))
	for _, id := range startNodes {
		queue = append(queue, queueItem{id, 0})
		visited[id] = true
	}

	checkCounter := 0

	for len(queue) > 0 {
		checkCounter++
		if checkCounter%contextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				return result, nil
			}
		}

		item := queue[0]
		queue = queue[1:]

		result.VisitedNodes = append(result.VisitedNodes, item.nodeID)
		if item.depth > result.Depth {
			result.Depth = item.depth
		}

		if len(result.VisitedNodes) >= options.Limit {
			result.Truncated = true
			break
		}

		if item.depth >= options.MaxDepth {
			continue
		}

		node := g.nodes[item.nodeID]
		for _, edge := range node.Outgoing {
			if edge.Type != EdgeTypeImports {
				continue
			}
			if visited[edge.ToID] {
				continue
			}
			visited[edge.ToID] = true
			result.Edges = append(result.Edges, edge)
			queue = append(queue, queueItem{edge.ToID, item.depth + 1})
		}
	}

	return result, nil
}

// GetTypeHierarchy returns the type hierarchy for a type.
//
// Description:
//
//	Performs iterative BFS traversal following IMPLEMENTS and EMBEDS edges.
//	Returns all interfaces implemented and types embedded.
//
// Inputs:
//
//	ctx - Context for cancellation (checked every 100 nodes)
//	typeID - Type ID to find hierarchy for
//	opts - Query options including MaxDepth (default: 10, max: 100)
//
// Outputs:
//
//	*TraversalResult - Visited nodes and edges, with Truncated flag
//	error - Non-nil if type not found
func (g *Graph) GetTypeHierarchy(ctx context.Context, typeID string, opts ...QueryOption) (*TraversalResult, error) {
	options := applyOptions(opts)

	_, ok := g.nodes[typeID]
	if !ok {
		return nil, fmt.Errorf("type node not found: %s", typeID)
	}

	result := &TraversalResult{
		StartNode:    typeID,
		VisitedNodes: make([]string, 0),
		Edges:        make([]*Edge, 0),
	}

	visited := make(map[string]bool)
	type queueItem struct {
		nodeID string
		depth  int
	}
	queue := []queueItem{{typeID, 0}}
	visited[typeID] = true
	checkCounter := 0

	for len(queue) > 0 {
		checkCounter++
		if checkCounter%contextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				result.Truncated = true
				return result, nil
			}
		}

		item := queue[0]
		queue = queue[1:]

		result.VisitedNodes = append(result.VisitedNodes, item.nodeID)
		if item.depth > result.Depth {
			result.Depth = item.depth
		}

		if len(result.VisitedNodes) >= options.Limit {
			result.Truncated = true
			break
		}

		if item.depth >= options.MaxDepth {
			continue
		}

		node := g.nodes[item.nodeID]

		// Follow IMPLEMENTS edges (outgoing - type implements interface)
		for _, edge := range node.Outgoing {
			if edge.Type != EdgeTypeImplements && edge.Type != EdgeTypeEmbeds {
				continue
			}
			if visited[edge.ToID] {
				continue
			}
			visited[edge.ToID] = true
			result.Edges = append(result.Edges, edge)
			queue = append(queue, queueItem{edge.ToID, item.depth + 1})
		}

		// Also follow incoming IMPLEMENTS edges (for interfaces - find implementers)
		for _, edge := range node.Incoming {
			if edge.Type != EdgeTypeImplements && edge.Type != EdgeTypeEmbeds {
				continue
			}
			if visited[edge.FromID] {
				continue
			}
			visited[edge.FromID] = true
			result.Edges = append(result.Edges, edge)
			queue = append(queue, queueItem{edge.FromID, item.depth + 1})
		}
	}

	return result, nil
}

// ShortestPath finds the shortest path between two symbols.
//
// Description:
//
//	Uses BFS to find minimum-edge path. Considers all edge types.
//	Returns immediately if fromID == toID (path of length 0).
//
// Inputs:
//
//	ctx - Context for cancellation
//	fromID - Starting node ID
//	toID - Target node ID
//
// Outputs:
//
//	*PathResult - Path details or empty if no path exists
//	error - Non-nil if nodes not found
func (g *Graph) ShortestPath(ctx context.Context, fromID, toID string) (*PathResult, error) {
	result := &PathResult{
		From:   fromID,
		To:     toID,
		Path:   []string{},
		Length: -1,
	}

	if _, ok := g.nodes[fromID]; !ok {
		return nil, fmt.Errorf("source node not found: %s", fromID)
	}
	if _, ok := g.nodes[toID]; !ok {
		return nil, fmt.Errorf("target node not found: %s", toID)
	}

	// Same node case
	if fromID == toID {
		result.Path = []string{fromID}
		result.Length = 0
		return result, nil
	}

	// BFS with parent tracking
	visited := make(map[string]bool)
	parent := make(map[string]string)
	queue := []string{fromID}
	visited[fromID] = true

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return result, nil // No path found before cancellation
		}

		current := queue[0]
		queue = queue[1:]

		node := g.nodes[current]
		for _, edge := range node.Outgoing {
			if visited[edge.ToID] {
				continue
			}
			visited[edge.ToID] = true
			parent[edge.ToID] = current

			if edge.ToID == toID {
				// Reconstruct path
				path := []string{toID}
				for p := parent[toID]; p != ""; p = parent[p] {
					path = append([]string{p}, path...)
					if p == fromID {
						break
					}
				}
				result.Path = path
				result.Length = len(path) - 1
				return result, nil
			}

			queue = append(queue, edge.ToID)
		}
	}

	return result, nil // No path found
}
