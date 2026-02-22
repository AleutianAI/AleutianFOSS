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
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/dgraph-io/badger/v4"
)

// =============================================================================
// Dominator Tree Cache
// =============================================================================

// DominatorTreeCache provides session-level caching for dominator trees.
//
// Description:
//
//	Caches a single dominator tree to avoid recomputation when the same
//	entry point is requested multiple times. The cache is automatically
//	invalidated when:
//	- A different entry point is requested
//	- The underlying graph changes (detected via BuiltAtMilli)
//
// Thread Safety:
//
//	DominatorTreeCache is safe for concurrent use via RWMutex.
type DominatorTreeCache struct {
	mu           sync.RWMutex
	entry        string         // Entry point used for computation
	tree         *DominatorTree // Cached dominator tree
	graphVersion int64          // Graph BuiltAtMilli when computed
	computedAt   int64          // Unix milliseconds when cache was populated
}

// =============================================================================
// GraphAnalytics Options
// =============================================================================

// GraphAnalyticsOptions configures GraphAnalytics behavior.
type GraphAnalyticsOptions struct {
	// AutoSession enables automatic session management.
	// When true, CRS-aware queries automatically start a session on first use.
	// User must still call EndSession() to finalize and free resources.
	// Default: false (manual session management required)
	AutoSession bool

	// SessionTimeout defines the maximum inactivity duration before a session expires.
	// When set, if a CRS-aware query is called and the session has been inactive
	// for longer than SessionTimeout, the old session is automatically ended and
	// a new session is started.
	// A value of 0 disables timeout (sessions never expire from inactivity).
	// Default: 0 (no timeout)
	SessionTimeout time.Duration

	// EnableBadgerQueryCache enables cross-session path query caching via BadgerDB.
	// When true, path query results are persisted to BadgerDB and reused across sessions
	// if the graph hash matches (graph unchanged).
	// Cache invalidation: automatic on graph hash change.
	// Default: false (no persistent caching)
	EnableBadgerQueryCache bool

	// BadgerDB is the BadgerDB instance for persistent caching.
	// Required if EnableBadgerQueryCache is true, otherwise ignored.
	// Must be opened and managed by caller.
	BadgerDB *badger.DB
}

// =============================================================================
// Session State
// =============================================================================

// sessionState holds the state of a CRS session for stacking.
type sessionState struct {
	// SessionID is the unique session identifier.
	SessionID string

	// StepCount is the number of steps recorded in this session so far.
	StepCount int64

	// LastActivityTime is when the session was last active (Unix milliseconds).
	LastActivityTime int64
}

// =============================================================================
// GraphAnalytics
// =============================================================================

// GraphAnalytics provides analytical queries over the code graph.
//
// Description:
//
//	GraphAnalytics operates on a HierarchicalGraph to provide insights about
//	code structure, including:
//	- Hotspot detection (most-connected nodes)
//	- Dead code detection (unreachable symbols)
//	- Cyclic dependency detection (using Tarjan's SCC)
//	- Package coupling metrics
//	- HLD query wrappers with CRS integration
//
// Thread Safety:
//
//	GraphAnalytics is safe for concurrent use (read-only queries).
//	Session management is protected by RWMutex.
//
// Performance:
//
//	| Operation | Complexity |
//	|-----------|------------|
//	| HotSpots | O(V log k) for top-k |
//	| DeadCode | O(V) |
//	| CyclicDependencies | O(V + E) |
//	| PackageCoupling | O(P) where P = packages |
//	| HLD Queries | O(log V) |
type GraphAnalytics struct {
	graph *HierarchicalGraph

	// domTreeCache caches the dominator tree for a given entry point.
	// Avoids recomputation when multiple tools request the same dominator tree.
	domTreeCache *DominatorTreeCache

	// postDomTreeCache caches the post-dominator tree for a given exit point.
	// Avoids recomputation when multiple tools request the same post-dominator tree.
	postDomTreeCache *DominatorTreeCache

	// CRS integration (optional) - for HLD query recording
	crs              crs.CRS
	hld              *HLDecomposition
	currentSessionID string
	stepCounter      atomic.Int64
	autoSessionSeq   atomic.Int64   // Sequence number for auto-generated session IDs
	lastActivityTime atomic.Int64   // Unix milliseconds of last CRS query activity
	sessionStack     []sessionState // Stack of suspended sessions (for nested sessions)
	autoSession      bool           // If true, auto-start session on first CRS query
	sessionTimeout   time.Duration  // Max inactivity before session expires (0 = no timeout)
	mu               sync.RWMutex   // Protects session fields

	// Path query engines (GR-19d) - one per aggregation function
	pathQueryEngines map[AggregateFunc]*PathQueryEngine
	pqeMu            sync.RWMutex // Protects pathQueryEngines map

	// BadgerDB integration (C-IMPL-2)
	db                *badger.DB // Optional BadgerDB for cross-session caching
	enableBadgerCache bool       // If true, use BadgerDB for path query caching
	badgerCacheHits   atomic.Int64
	badgerCacheMisses atomic.Int64
	badgerCacheErrors atomic.Int64

	logger *slog.Logger
}

// NewGraphAnalytics creates a new analytics instance for the given graph.
//
// Inputs:
//
//	graph - The hierarchical graph to analyze. Must not be nil.
//
// Outputs:
//
//	*GraphAnalytics - The analytics instance. Never nil if graph is valid.
func NewGraphAnalytics(graph *HierarchicalGraph) *GraphAnalytics {
	if graph == nil {
		return nil
	}
	return &GraphAnalytics{
		graph:            graph,
		domTreeCache:     &DominatorTreeCache{},
		postDomTreeCache: &DominatorTreeCache{},
		logger:           slog.Default().With(slog.String("component", "graph_analytics")),
	}
}

// NewGraphAnalyticsWithCRS creates an analytics instance with CRS integration.
//
// Description:
//
//	Creates a GraphAnalytics instance that records HLD query operations to CRS.
//	Use this when you want query tracing and replay capabilities.
//
// Inputs:
//   - graph: The hierarchical graph to analyze. Must not be nil.
//   - hld: The HLD instance for LCA queries. Must not be nil.
//   - crsInstance: The CRS instance for recording. Must not be nil.
//   - opts: Configuration options. If nil, uses defaults (no auto-session).
//
// Outputs:
//   - *GraphAnalytics: The analytics instance with CRS enabled. Never nil if inputs valid.
//
// Thread Safety: Safe for concurrent use.
func NewGraphAnalyticsWithCRS(graph *HierarchicalGraph, hld *HLDecomposition, crsInstance crs.CRS, opts *GraphAnalyticsOptions) *GraphAnalytics {
	if graph == nil || hld == nil {
		return nil
	}

	// Default options if not provided
	if opts == nil {
		opts = &GraphAnalyticsOptions{AutoSession: false}
	}

	return &GraphAnalytics{
		graph:             graph,
		hld:               hld,
		crs:               crsInstance, // Can be nil
		autoSession:       opts.AutoSession,
		sessionTimeout:    opts.SessionTimeout,
		domTreeCache:      &DominatorTreeCache{},
		postDomTreeCache:  &DominatorTreeCache{},
		pathQueryEngines:  make(map[AggregateFunc]*PathQueryEngine),
		db:                opts.BadgerDB, // Can be nil
		enableBadgerCache: opts.EnableBadgerQueryCache,
		logger:            slog.Default().With(slog.String("component", "graph_analytics")),
	}
}

// =============================================================================
// Graph Readiness Check (GR-17b Fix)
// =============================================================================

// IsGraphReady returns true if the graph has been fully indexed and is ready for queries.
//
// Description:
//
//	Checks if the underlying graph has been populated with nodes and edges.
//	This prevents race conditions where dominator-based tools execute before
//	graph indexing completes, returning incorrect empty results.
//
// Outputs:
//   - bool: true if graph is ready for queries, false if still indexing.
//
// Thread Safety: Safe for concurrent use (read-only check).
//
// Example:
//
//	if !analytics.IsGraphReady() {
//	    return &Result{Success: false, Error: "graph not ready - indexing in progress"}, nil
//	}
//
// Limitations:
//
//	This only checks if nodes/edges exist and graph is frozen. It doesn't
//	validate that all files have been processed or that the graph is complete.
func (a *GraphAnalytics) IsGraphReady() bool {
	if a == nil || a.graph == nil || a.graph.Graph == nil {
		return false
	}

	// Check if graph is frozen (BuiltAtMilli > 0)
	if a.graph.BuiltAtMilli == 0 {
		return false
	}

	// Check if graph has nodes and edges
	nodeCount := a.graph.NodeCount()
	edgeCount := a.graph.EdgeCount()

	// Graph must have at least 1 node to be considered ready
	// (Even a minimal project has a main function)
	return nodeCount > 0 && edgeCount >= 0
}

// GetNode retrieves a node from the underlying graph by its ID.
//
// Description:
//
//	Delegates to the underlying Graph.GetNode method to look up a node
//	by its symbol ID. This is useful for checking node properties such as
//	outgoing/incoming edge counts without exposing the internal graph.
//
// Inputs:
//
//	id - The node ID (same as Symbol.ID).
//
// Outputs:
//
//	*Node - The node if found, nil otherwise.
//	bool - True if the node was found.
//
// Thread Safety: Safe for concurrent use (graph is read-only after freeze).
func (a *GraphAnalytics) GetNode(id string) (*Node, bool) {
	if a == nil || a.graph == nil || a.graph.Graph == nil {
		return nil, false
	}
	return a.graph.GetNode(id)
}

// =============================================================================
// Hotspot Detection
// =============================================================================

// HotspotNode represents a node with its connectivity score.
type HotspotNode struct {
	// Node is the graph node.
	Node *Node

	// Score is the connectivity score (higher = more connected).
	// Computed as: inDegree*2 + outDegree*1
	Score int

	// InDegree is the number of incoming edges.
	InDegree int

	// OutDegree is the number of outgoing edges.
	OutDegree int
}

// HotSpots returns the top N most-connected nodes in the graph.
//
// Description:
//
//	Finds nodes with the highest connectivity score, where:
//	score = inDegree * 2 + outDegree * 1
//
//	Incoming edges are weighted higher because being called frequently
//	indicates higher impact on the codebase.
//
// Inputs:
//
//	top - Maximum number of hotspots to return. Must be > 0.
//
// Outputs:
//
//	[]HotspotNode - Top N nodes sorted by score descending.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) HotSpots(top int) []HotspotNode {
	if top <= 0 {
		return []HotspotNode{}
	}

	// Collect all nodes with scores
	hotspots := make([]HotspotNode, 0, a.graph.NodeCount())
	for _, node := range a.graph.Nodes() {
		if node.Symbol == nil {
			continue
		}

		// Skip external/placeholder nodes
		if node.Symbol.Kind == ast.SymbolKindExternal {
			continue
		}

		hs := HotspotNode{
			Node:      node,
			InDegree:  len(node.Incoming),
			OutDegree: len(node.Outgoing),
		}
		hs.Score = hs.InDegree*2 + hs.OutDegree

		hotspots = append(hotspots, hs)
	}

	// Sort by score descending
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].Score != hotspots[j].Score {
			return hotspots[i].Score > hotspots[j].Score
		}
		// Tie-breaker: sort by ID for stability
		return hotspots[i].Node.ID < hotspots[j].Node.ID
	})

	// Return top N
	if top > len(hotspots) {
		top = len(hotspots)
	}
	return hotspots[:top]
}

// HotSpotsWithCRS returns hotspots with a TraceStep for CRS recording.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) HotSpotsWithCRS(ctx context.Context, top int) ([]HotspotNode, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_hotspots").
			WithTarget("project").
			WithTool("HotSpots").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []HotspotNode{}, step
	}

	hotspots := a.HotSpots(top)

	step := crs.NewTraceStepBuilder().
		WithAction("analytics_hotspots").
		WithTarget("project").
		WithTool("HotSpots").
		WithDuration(time.Since(start)).
		WithMetadata("requested", itoa(top)).
		WithMetadata("returned", itoa(len(hotspots))).
		WithMetadata("total_nodes", itoa(a.graph.NodeCount())).
		Build()

	return hotspots, step
}

// =============================================================================
// Dead Code Detection
// =============================================================================

// DeadCodeNode represents a potentially unused symbol.
type DeadCodeNode struct {
	// Node is the graph node.
	Node *Node

	// Reason explains why this was flagged as dead code.
	Reason string
}

// DeadCode finds symbols with no incoming edges (potential unused code).
//
// Description:
//
//	Returns nodes that have no callers/references, excluding:
//	- Entry points (main, init, Test*, Benchmark*)
//	- Exported symbols in main package
//	- External/placeholder nodes
//	- Interface methods (implementations satisfy interfaces)
//
// Outputs:
//
//	[]DeadCodeNode - Potentially unused symbols with reasons.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) DeadCode() []DeadCodeNode {
	result := make([]DeadCodeNode, 0)

	for _, node := range a.graph.Nodes() {
		if node.Symbol == nil {
			continue
		}

		sym := node.Symbol

		// Skip external/placeholder nodes
		if sym.Kind == ast.SymbolKindExternal {
			continue
		}

		// Skip nodes with incoming edges (they're used)
		if len(node.Incoming) > 0 {
			continue
		}

		// Skip entry points
		if isEntryPoint(sym) {
			continue
		}

		// Skip interface methods
		if sym.Kind == ast.SymbolKindMethod {
			// Methods might implement interfaces - harder to detect
			// Be conservative and skip all methods
			continue
		}

		// This node has no incoming edges and isn't an entry point
		reason := "no callers or references"
		if sym.Exported {
			reason = "exported but not referenced internally"
		}

		result = append(result, DeadCodeNode{
			Node:   node,
			Reason: reason,
		})
	}

	// Sort by file path and name for consistent output
	sort.Slice(result, func(i, j int) bool {
		ni, nj := result[i].Node.Symbol, result[j].Node.Symbol
		if ni.FilePath != nj.FilePath {
			return ni.FilePath < nj.FilePath
		}
		return ni.Name < nj.Name
	})

	return result
}

// DeadCodeWithCRS returns dead code analysis with a TraceStep.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) DeadCodeWithCRS(ctx context.Context) ([]DeadCodeNode, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_dead_code").
			WithTarget("project").
			WithTool("DeadCode").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []DeadCodeNode{}, step
	}

	deadCode := a.DeadCode()

	step := crs.NewTraceStepBuilder().
		WithAction("analytics_dead_code").
		WithTarget("project").
		WithTool("DeadCode").
		WithDuration(time.Since(start)).
		WithMetadata("dead_code_count", itoa(len(deadCode))).
		WithMetadata("total_nodes", itoa(a.graph.NodeCount())).
		Build()

	return deadCode, step
}

// isEntryPoint checks if a symbol is an entry point across all supported languages.
//
// Description:
//
//	Detects entry points for Go, Python, JavaScript, and TypeScript using
//	language-specific patterns. Entry points are functions/methods that are
//	invoked by the runtime, framework, or test runner rather than by other
//	application code. These should not be reported as dead code.
//
// IT-08 H-1: Extended from Go-only to cross-language detection.
func isEntryPoint(sym *ast.Symbol) bool {
	if sym == nil {
		return false
	}

	name := sym.Name
	lang := strings.ToLower(sym.Language)

	// === Cross-language entry points ===

	// main is an entry point in all languages
	if name == "main" {
		return true
	}

	// === Go entry points ===

	if lang == "" || lang == "go" {
		if isGoEntryPoint(sym) {
			return true
		}
	}

	// === Python entry points ===

	if lang == "python" {
		if isPythonEntryPoint(sym) {
			return true
		}
	}

	// === JavaScript / TypeScript entry points ===

	if lang == "javascript" || lang == "typescript" {
		if isJSTSEntryPoint(sym) {
			return true
		}
	}

	return false
}

// isGoEntryPoint checks Go-specific entry point patterns.
func isGoEntryPoint(sym *ast.Symbol) bool {
	name := sym.Name

	// init functions (package initialization)
	if name == "init" {
		return true
	}

	// Test, Benchmark, Fuzz, Example functions (go test runner)
	if len(name) > 4 && (name[:4] == "Test" || name[:4] == "Fuzz") {
		return true
	}
	if len(name) > 9 && name[:9] == "Benchmark" {
		return true
	}
	if len(name) > 7 && name[:7] == "Example" {
		return true
	}

	// HTTP handler interface implementation
	if sym.Kind == ast.SymbolKindMethod && name == "ServeHTTP" {
		return true
	}

	return false
}

// isPythonEntryPoint checks Python-specific entry point patterns.
func isPythonEntryPoint(sym *ast.Symbol) bool {
	name := sym.Name

	// __main__ block
	if name == "__main__" {
		return true
	}

	// __main__.py file — all top-level functions are entry points
	if sym.FilePath != "" && filepath.Base(sym.FilePath) == "__main__.py" {
		return true
	}

	// pytest test functions (test_*)
	if strings.HasPrefix(name, "test_") {
		return true
	}

	// unittest setUp/tearDown methods
	if name == "setUp" || name == "tearDown" || name == "setUpClass" || name == "tearDownClass" {
		return true
	}

	// Decorator-based entry points (Flask routes, FastAPI, Click CLI, etc.)
	if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
		if hasPythonEntryPointDecorator(sym.Metadata.Decorators) {
			return true
		}
	}

	// Classes extending TestCase are entry points (unittest)
	if sym.Kind == ast.SymbolKindClass && sym.Metadata != nil && sym.Metadata.Extends != "" {
		if strings.HasSuffix(sym.Metadata.Extends, "TestCase") {
			return true
		}
	}

	return false
}

// hasPythonEntryPointDecorator checks if any decorator marks this as a
// framework entry point (route handler, CLI command, etc.).
func hasPythonEntryPointDecorator(decorators []string) bool {
	for _, dec := range decorators {
		d := strings.ToLower(dec)
		// Flask/FastAPI route decorators
		if strings.Contains(d, "route") || strings.Contains(d, ".get") ||
			strings.Contains(d, ".post") || strings.Contains(d, ".put") ||
			strings.Contains(d, ".delete") || strings.Contains(d, ".patch") {
			return true
		}
		// Click/Typer CLI decorators
		if strings.Contains(d, "click.command") || strings.Contains(d, "click.group") ||
			strings.Contains(d, "app.command") {
			return true
		}
		// Celery tasks
		if strings.Contains(d, ".task") {
			return true
		}
		// pytest fixtures
		if strings.Contains(d, "fixture") {
			return true
		}
	}
	return false
}

// isJSTSEntryPoint checks JavaScript and TypeScript entry point patterns.
func isJSTSEntryPoint(sym *ast.Symbol) bool {
	name := sym.Name

	// Jest/Mocha/Vitest test framework entry points
	switch name {
	case "it", "test", "describe", "beforeEach", "afterEach",
		"beforeAll", "afterAll", "before", "after":
		return true
	}

	// NestJS and other decorator-based frameworks
	if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
		if hasJSTSEntryPointDecorator(sym.Metadata.Decorators) {
			return true
		}
	}

	// module.exports target functions are consumed externally
	if sym.Exported {
		return true
	}

	// Next.js special exports
	switch name {
	case "getServerSideProps", "getStaticProps", "getStaticPaths",
		"generateStaticParams", "generateMetadata":
		return true
	}

	return false
}

// hasJSTSEntryPointDecorator checks if any decorator marks this as a
// framework entry point (NestJS controller, route handler, etc.).
func hasJSTSEntryPointDecorator(decorators []string) bool {
	for _, dec := range decorators {
		// NestJS decorators (case-sensitive — they're PascalCase)
		switch dec {
		case "Controller", "Get", "Post", "Put", "Delete", "Patch",
			"Injectable", "Module", "Guard", "Interceptor", "Pipe",
			"ExceptionFilter", "UseGuards", "UseInterceptors",
			"WebSocketGateway", "SubscribeMessage":
			return true
		}
	}
	return false
}

// =============================================================================
// Cyclic Dependency Detection (Tarjan's SCC)
// =============================================================================

// CyclicDependency represents a cycle in the dependency graph.
type CyclicDependency struct {
	// Nodes contains the node IDs in the cycle (in cycle order).
	Nodes []string

	// Packages contains the unique packages involved in the cycle.
	Packages []string

	// Length is the number of nodes in the cycle.
	Length int
}

// CyclicDependencies finds all cycles in the graph using Tarjan's SCC algorithm.
//
// Description:
//
//	Uses Tarjan's strongly connected components algorithm to find cycles.
//	Only returns components with more than one node (actual cycles).
//
//	Time complexity: O(V + E)
//	Space complexity: O(V)
//
//	Implementation uses an explicit call stack to avoid stack overflow on
//	deep graphs (CR-2 fix).
//
// Outputs:
//
//	[]CyclicDependency - All cycles found, sorted by length descending.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) CyclicDependencies() []CyclicDependency {
	// Tarjan's SCC state
	index := 0
	nodeIndex := make(map[string]int)
	nodeLowLink := make(map[string]int)
	onStack := make(map[string]bool)
	sccStack := make([]string, 0)
	sccs := make([][]string, 0)

	// callFrame represents a stack frame in the iterative Tarjan's algorithm.
	// This replaces the recursive call stack to avoid stack overflow on deep graphs.
	type callFrame struct {
		nodeID    string
		edgeIndex int    // Current index into Outgoing edges
		phase     int    // 0=init, 1=process edges, 2=post-child
		childID   string // ID of child we just returned from (for phase 2)
	}

	// strongConnectIterative performs Tarjan's algorithm iteratively.
	strongConnectIterative := func(startNodeID string) {
		callStack := []callFrame{{nodeID: startNodeID, edgeIndex: 0, phase: 0}}

		for len(callStack) > 0 {
			frame := &callStack[len(callStack)-1]

			switch frame.phase {
			case 0:
				// Initialize node (equivalent to function entry)
				nodeIndex[frame.nodeID] = index
				nodeLowLink[frame.nodeID] = index
				index++
				sccStack = append(sccStack, frame.nodeID)
				onStack[frame.nodeID] = true
				frame.phase = 1

			case 1:
				// Process outgoing edges
				node, ok := a.graph.GetNode(frame.nodeID)
				if !ok {
					frame.phase = 3 // Skip to finalization
					continue
				}

				// Find next unprocessed edge
				for frame.edgeIndex < len(node.Outgoing) {
					edge := node.Outgoing[frame.edgeIndex]
					frame.edgeIndex++

					if _, visited := nodeIndex[edge.ToID]; !visited {
						// Push new frame for unvisited child
						frame.phase = 2
						frame.childID = edge.ToID
						callStack = append(callStack, callFrame{
							nodeID:    edge.ToID,
							edgeIndex: 0,
							phase:     0,
						})
						goto continueLoop
					} else if onStack[edge.ToID] {
						// Back edge to node on stack
						if nodeIndex[edge.ToID] < nodeLowLink[frame.nodeID] {
							nodeLowLink[frame.nodeID] = nodeIndex[edge.ToID]
						}
					}
				}
				// All edges processed, move to finalization
				frame.phase = 3

			case 2:
				// Post-child processing (after returning from recursive call)
				if nodeLowLink[frame.childID] < nodeLowLink[frame.nodeID] {
					nodeLowLink[frame.nodeID] = nodeLowLink[frame.childID]
				}
				frame.phase = 1 // Continue processing edges

			case 3:
				// Finalization: check if this is SCC root
				if nodeLowLink[frame.nodeID] == nodeIndex[frame.nodeID] {
					scc := make([]string, 0)
					for {
						w := sccStack[len(sccStack)-1]
						sccStack = sccStack[:len(sccStack)-1]
						onStack[w] = false
						scc = append(scc, w)
						if w == frame.nodeID {
							break
						}
					}
					if len(scc) > 1 {
						// Only record cycles (components with >1 node)
						sccs = append(sccs, scc)
					}
				}
				// Pop this frame
				callStack = callStack[:len(callStack)-1]
			}
		continueLoop:
		}
	}

	// Run Tarjan's on all nodes
	for _, node := range a.graph.Nodes() {
		if _, visited := nodeIndex[node.ID]; !visited {
			strongConnectIterative(node.ID)
		}
	}

	// Convert to CyclicDependency results
	result := make([]CyclicDependency, 0, len(sccs))
	for _, scc := range sccs {
		pkgSet := make(map[string]bool)
		for _, nodeID := range scc {
			if node, ok := a.graph.GetNode(nodeID); ok {
				pkg := getNodePackage(node)
				if pkg != "" {
					pkgSet[pkg] = true
				}
			}
		}

		packages := make([]string, 0, len(pkgSet))
		for pkg := range pkgSet {
			packages = append(packages, pkg)
		}
		sort.Strings(packages)

		result = append(result, CyclicDependency{
			Nodes:    scc,
			Packages: packages,
			Length:   len(scc),
		})
	}

	// Sort by length descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Length > result[j].Length
	})

	return result
}

// CyclicDependenciesWithCRS returns cycles with a TraceStep.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) CyclicDependenciesWithCRS(ctx context.Context) ([]CyclicDependency, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_cycles").
			WithTarget("project").
			WithTool("CyclicDependencies").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []CyclicDependency{}, step
	}

	cycles := a.CyclicDependencies()

	totalNodes := 0
	for _, c := range cycles {
		totalNodes += c.Length
	}

	step := crs.NewTraceStepBuilder().
		WithAction("analytics_cycles").
		WithTarget("project").
		WithTool("CyclicDependencies").
		WithDuration(time.Since(start)).
		WithMetadata("cycles_found", itoa(len(cycles))).
		WithMetadata("nodes_in_cycles", itoa(totalNodes)).
		WithMetadata("graph_nodes", itoa(a.graph.NodeCount())).
		WithMetadata("graph_edges", itoa(a.graph.EdgeCount())).
		Build()

	return cycles, step
}

// =============================================================================
// Package Coupling Metrics
// =============================================================================

// PackageCoupling computes coupling metrics for all packages.
//
// Description:
//
//	Computes Robert C. Martin's coupling metrics for each package:
//
//	- Afferent Coupling (Ca): packages that depend ON this package
//	- Efferent Coupling (Ce): packages this package depends ON
//	- Instability (I): Ce / (Ca + Ce), range [0, 1]
//	  - I=0: maximally stable (many dependents, no dependencies)
//	  - I=1: maximally unstable (no dependents, all dependencies)
//	- Abstractness (A): abstract types / total types
//
// Outputs:
//
//	map[string]CouplingMetrics - Metrics keyed by package path.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) PackageCoupling() map[string]CouplingMetrics {
	result := make(map[string]CouplingMetrics)

	for _, pkgInfo := range a.graph.GetPackages() {
		deps := a.graph.GetPackageDependencies(pkgInfo.Name)
		dependents := a.graph.GetPackageDependents(pkgInfo.Name)

		ca := len(dependents) // Afferent (incoming)
		ce := len(deps)       // Efferent (outgoing)

		// Compute instability
		var instability float64
		if ca+ce > 0 {
			instability = float64(ce) / float64(ca+ce)
		}

		// Count abstract vs concrete types
		abstractCount := 0
		concreteCount := 0
		for _, node := range a.graph.GetNodesInPackage(pkgInfo.Name) {
			if node.Symbol == nil {
				continue
			}
			switch node.Symbol.Kind {
			case ast.SymbolKindInterface:
				abstractCount++
			case ast.SymbolKindStruct, ast.SymbolKindClass:
				concreteCount++
			}
		}

		// Compute abstractness
		var abstractness float64
		if abstractCount+concreteCount > 0 {
			abstractness = float64(abstractCount) / float64(abstractCount+concreteCount)
		}

		result[pkgInfo.Name] = CouplingMetrics{
			Package:       pkgInfo.Name,
			Afferent:      ca,
			Efferent:      ce,
			Instability:   instability,
			AbstractTypes: abstractCount,
			ConcreteTypes: concreteCount,
			Abstractness:  abstractness,
		}
	}

	return result
}

// PackageCouplingWithCRS returns coupling metrics with a TraceStep.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) PackageCouplingWithCRS(ctx context.Context) (map[string]CouplingMetrics, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("analytics_coupling").
			WithTarget("project").
			WithTool("PackageCoupling").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return map[string]CouplingMetrics{}, step
	}

	metrics := a.PackageCoupling()

	// Find most unstable and most stable
	mostUnstable := ""
	mostStable := ""
	highestI := 0.0
	lowestI := 2.0 // Start above max

	for pkg, m := range metrics {
		if m.Instability > highestI {
			highestI = m.Instability
			mostUnstable = pkg
		}
		if m.Instability < lowestI {
			lowestI = m.Instability
			mostStable = pkg
		}
	}

	step := crs.NewTraceStepBuilder().
		WithAction("analytics_coupling").
		WithTarget("project").
		WithTool("PackageCoupling").
		WithDuration(time.Since(start)).
		WithMetadata("packages_analyzed", itoa(len(metrics))).
		WithMetadata("most_unstable_pkg", mostUnstable).
		WithMetadata("most_stable_pkg", mostStable).
		Build()

	return metrics, step
}

// GetCouplingForPackage returns coupling metrics for a specific package.
//
// Inputs:
//
//	pkg - The package path to analyze.
//
// Outputs:
//
//	*CouplingMetrics - Metrics for the package, or nil if not found.
//
// Thread Safety: Safe for concurrent use.
func (a *GraphAnalytics) GetCouplingForPackage(pkg string) *CouplingMetrics {
	deps := a.graph.GetPackageDependencies(pkg)
	dependents := a.graph.GetPackageDependents(pkg)

	ca := len(dependents)
	ce := len(deps)

	var instability float64
	if ca+ce > 0 {
		instability = float64(ce) / float64(ca+ce)
	}

	abstractCount := 0
	concreteCount := 0
	for _, node := range a.graph.GetNodesInPackage(pkg) {
		if node.Symbol == nil {
			continue
		}
		switch node.Symbol.Kind {
		case ast.SymbolKindInterface:
			abstractCount++
		case ast.SymbolKindStruct, ast.SymbolKindClass:
			concreteCount++
		}
	}

	var abstractness float64
	if abstractCount+concreteCount > 0 {
		abstractness = float64(abstractCount) / float64(abstractCount+concreteCount)
	}

	return &CouplingMetrics{
		Package:       pkg,
		Afferent:      ca,
		Efferent:      ce,
		Instability:   instability,
		AbstractTypes: abstractCount,
		ConcreteTypes: concreteCount,
		Abstractness:  abstractness,
	}
}

// =============================================================================
// CRS Session Management (GR-19c)
// =============================================================================

// StartSession initializes a new CRS session for query recording.
//
// Description:
//
//	Sets the session ID in CRS and resets the step counter. All subsequent
//	CRS-aware queries (LCAWithCRS, etc.) will record StepRecords to this session.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - sessionID: Unique session identifier. Must not be empty.
//
// Outputs:
//   - error: Non-nil if CRS is not configured or session ID is empty.
//
// Thread Safety: Safe for concurrent use (protected by mutex).
func (ga *GraphAnalytics) StartSession(ctx context.Context, sessionID string) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	if sessionID == "" {
		return errors.New("sessionID must not be empty")
	}

	ga.mu.Lock()
	defer ga.mu.Unlock()

	if ga.crs == nil {
		return errors.New("CRS not configured")
	}

	// Set session ID in CRS
	ga.crs.SetSessionID(sessionID)

	// Reset step counter and set current session
	ga.stepCounter.Store(0)
	ga.currentSessionID = sessionID

	// Reset last activity time
	ga.lastActivityTime.Store(time.Now().UnixMilli())

	ga.logger.Info("CRS session started",
		slog.String("session_id", sessionID),
	)

	return nil
}

// EndSession finalizes the current CRS session and clears history.
//
// Description:
//
//	Clears the step history for the current session and resets session state.
//	Call this when the session is complete to free memory.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//   - error: Non-nil if CRS is not configured or no active session.
//
// Thread Safety: Safe for concurrent use (protected by mutex).
func (ga *GraphAnalytics) EndSession(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}

	ga.mu.Lock()
	defer ga.mu.Unlock()

	if ga.crs == nil {
		return errors.New("CRS not configured")
	}

	if ga.currentSessionID == "" {
		return errors.New("no active session")
	}

	sessionID := ga.currentSessionID
	stepCount := ga.stepCounter.Load()

	// Clear session history in CRS
	ga.crs.ClearStepHistory(sessionID)

	// Reset session state
	ga.currentSessionID = ""
	ga.stepCounter.Store(0)

	ga.logger.Info("CRS session ended",
		slog.String("session_id", sessionID),
		slog.Int64("steps", stepCount),
	)

	return nil
}

// PushSession suspends the current session and starts a new nested session.
//
// Description:
//
//	Saves the current session state onto a stack and starts a new session with
//	the given sessionID. This allows nested/hierarchical session tracking.
//	Use PopSession() to end the nested session and restore the previous one.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - sessionID: Unique session identifier for the nested session. Must not be empty.
//
// Outputs:
//   - error: Non-nil if CRS is not configured, session ID is empty, or no active session to push.
//
// Thread Safety: Safe for concurrent use (protected by mutex).
//
// Example:
//
//	// Outer operation
//	analytics.StartSession(ctx, "outer-session")
//	_, _ = analytics.LCAWithCRS(ctx, "node1", "node2")
//
//	// Start nested operation
//	analytics.PushSession(ctx, "inner-session")
//	_, _ = analytics.LCAWithCRS(ctx, "node3", "node4")
//	analytics.PopSession(ctx)
//
//	// Back to outer operation
//	_, _ = analytics.LCAWithCRS(ctx, "node5", "node6")
//	analytics.EndSession(ctx)
func (ga *GraphAnalytics) PushSession(ctx context.Context, sessionID string) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	if sessionID == "" {
		return errors.New("sessionID must not be empty")
	}

	ga.mu.Lock()
	defer ga.mu.Unlock()

	if ga.crs == nil {
		return errors.New("CRS not configured")
	}

	if ga.currentSessionID == "" {
		return errors.New("no active session to push - call StartSession first")
	}

	// Save current session state
	currentState := sessionState{
		SessionID:        ga.currentSessionID,
		StepCount:        ga.stepCounter.Load(),
		LastActivityTime: ga.lastActivityTime.Load(),
	}
	ga.sessionStack = append(ga.sessionStack, currentState)

	// Start new nested session
	ga.crs.SetSessionID(sessionID)
	ga.stepCounter.Store(0)
	ga.currentSessionID = sessionID
	ga.lastActivityTime.Store(time.Now().UnixMilli())

	ga.logger.Info("CRS session pushed (nested session started)",
		slog.String("new_session_id", sessionID),
		slog.String("suspended_session_id", currentState.SessionID),
		slog.Int("stack_depth", len(ga.sessionStack)),
	)

	return nil
}

// PopSession ends the current nested session and restores the previous session from the stack.
//
// Description:
//
//	Ends the current session, clears its history, and restores the previous session
//	state from the stack. This is the counterpart to PushSession().
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//   - error: Non-nil if CRS is not configured, no active session, or stack is empty.
//
// Thread Safety: Safe for concurrent use (protected by mutex).
func (ga *GraphAnalytics) PopSession(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}

	ga.mu.Lock()
	defer ga.mu.Unlock()

	if ga.crs == nil {
		return errors.New("CRS not configured")
	}

	if ga.currentSessionID == "" {
		return errors.New("no active session")
	}

	if len(ga.sessionStack) == 0 {
		return errors.New("session stack is empty - no session to pop")
	}

	// End current nested session
	currentSessionID := ga.currentSessionID
	currentStepCount := ga.stepCounter.Load()

	ga.crs.ClearStepHistory(currentSessionID)

	// Pop previous session from stack
	stackLen := len(ga.sessionStack)
	previousState := ga.sessionStack[stackLen-1]
	ga.sessionStack = ga.sessionStack[:stackLen-1]

	// Restore previous session state
	ga.crs.SetSessionID(previousState.SessionID)
	ga.currentSessionID = previousState.SessionID
	ga.stepCounter.Store(previousState.StepCount)
	ga.lastActivityTime.Store(previousState.LastActivityTime)

	ga.logger.Info("CRS session popped (nested session ended)",
		slog.String("ended_session_id", currentSessionID),
		slog.Int64("ended_steps", currentStepCount),
		slog.String("restored_session_id", previousState.SessionID),
		slog.Int("stack_depth", len(ga.sessionStack)),
	)

	return nil
}

// ensureSession automatically starts a session if auto-session is enabled and no session is active.
//
// Description:
//
//	This is an internal helper called by CRS-aware queries. If autoSession is true
//	and no session is active, generates a session ID and starts a session.
//	If sessionTimeout is configured and the current session has been inactive beyond
//	the timeout, automatically ends the old session and starts a new one.
//	If a session is already active and not timed out, does nothing.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//
// Outputs:
//   - error: Non-nil if session auto-start fails (e.g., CRS not configured).
//
// Thread Safety: Safe for concurrent use (protected by mutex).
//
// Assumptions:
//   - Called by CRS-aware queries before recording steps
//   - Session ID format: "auto_<unix_milliseconds>_<sequence>"
func (ga *GraphAnalytics) ensureSession(ctx context.Context) error {
	// Fast path: check session state (read lock)
	ga.mu.RLock()
	hasSession := ga.currentSessionID != ""
	autoEnabled := ga.autoSession
	timeout := ga.sessionTimeout
	lastActivity := ga.lastActivityTime.Load()
	ga.mu.RUnlock()

	// Check if session has timed out
	sessionTimedOut := false
	if hasSession && timeout > 0 && lastActivity > 0 {
		inactivity := time.Duration(time.Now().UnixMilli()-lastActivity) * time.Millisecond
		if inactivity > timeout {
			sessionTimedOut = true
		}
	}

	// If session exists and not timed out, nothing to do
	if hasSession && !sessionTimedOut {
		return nil
	}

	// If auto-session disabled and no timeout, nothing to do
	if !autoEnabled && !sessionTimedOut {
		return nil
	}

	// Need to handle timeout or start session - acquire write lock
	ga.mu.Lock()
	defer ga.mu.Unlock()

	// Double-check after acquiring write lock
	hasSession = ga.currentSessionID != ""
	lastActivity = ga.lastActivityTime.Load()
	sessionTimedOut = false
	if hasSession && timeout > 0 && lastActivity > 0 {
		inactivity := time.Duration(time.Now().UnixMilli()-lastActivity) * time.Millisecond
		if inactivity > timeout {
			sessionTimedOut = true
		}
	}

	// If session exists and timed out, end it first
	if hasSession && sessionTimedOut {
		oldSessionID := ga.currentSessionID
		stepCount := ga.stepCounter.Load()

		// Clear session history in CRS
		if ga.crs != nil {
			ga.crs.ClearStepHistory(oldSessionID)
		}

		// Reset session state
		ga.currentSessionID = ""
		ga.stepCounter.Store(0)

		ga.logger.Info("CRS session timed out and ended",
			slog.String("session_id", oldSessionID),
			slog.Int64("steps", stepCount),
			slog.Duration("timeout", timeout),
		)

		// Fall through to start new session
		hasSession = false
	}

	// If session exists (not timed out) or auto-session disabled, nothing more to do
	if hasSession || !autoEnabled {
		return nil
	}

	// Check CRS is configured
	if ga.crs == nil {
		return errors.New("CRS not configured - cannot auto-start session")
	}

	// Generate auto session ID using timestamp + sequence number for uniqueness
	seq := ga.autoSessionSeq.Add(1)
	sessionID := fmt.Sprintf("auto_%d_%d", time.Now().UnixMilli(), seq)

	// Set session ID in CRS
	ga.crs.SetSessionID(sessionID)

	// Reset step counter and set current session
	ga.stepCounter.Store(0)
	ga.currentSessionID = sessionID

	// Set last activity time
	ga.lastActivityTime.Store(time.Now().UnixMilli())

	ga.logger.Info("CRS session auto-started",
		slog.String("session_id", sessionID),
		slog.Bool("auto_session", true),
	)

	return nil
}

// updateActivity updates the last activity timestamp for session timeout tracking.
//
// Description:
//
//	Sets lastActivityTime to the current time in Unix milliseconds.
//	Called by CRS-aware queries after successful execution to reset the inactivity timer.
//
// Thread Safety: Safe for concurrent use (uses atomic operations).
func (ga *GraphAnalytics) updateActivity() {
	ga.lastActivityTime.Store(time.Now().UnixMilli())
}

// nextStepNumber returns the next step number and increments the counter.
//
// Description:
//
//	Step numbers are 1-indexed and sequential within a session.
//	Thread-safe via atomic operations.
//
// Outputs:
//   - int: The next step number (1-indexed).
//
// Thread Safety: Safe for concurrent use (atomic increment).
func (ga *GraphAnalytics) nextStepNumber() int {
	return int(ga.stepCounter.Add(1))
}

// =============================================================================
// HLD Query Wrappers with CRS Integration (GR-19c)
// =============================================================================

// LCAWithCRS computes LCA and records a StepRecord to CRS.
//
// Description:
//
//	Wraps HLD.LCA() to provide CRS integration. Records the operation
//	as a StepRecord with timing, outcome, and result summary.
//
//	If CRS is not configured or no active session, falls through to raw HLD call.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - u, v: Node IDs. Must not be empty.
//
// Outputs:
//   - string: LCA node ID.
//   - error: Non-nil on failure.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) LCAWithCRS(ctx context.Context, u, v string) (string, error) {
	// Auto-start session if enabled and no session active
	if err := ga.ensureSession(ctx); err != nil {
		return "", fmt.Errorf("auto-start session: %w", err)
	}

	// Check if CRS integration is enabled
	ga.mu.RLock()
	hasCRS := ga.crs != nil && ga.currentSessionID != "" && ga.hld != nil
	sessionID := ga.currentSessionID
	ga.mu.RUnlock()

	if !hasCRS {
		// No CRS or session - fall through to raw HLD call
		if ga.hld == nil {
			return "", errors.New("HLD not initialized")
		}
		return ga.hld.LCA(ctx, u, v)
	}

	// Execute HLD query with timing
	start := time.Now()
	lca, err := ga.hld.LCA(ctx, u, v)
	duration := time.Since(start)

	// Determine outcome
	outcome := crs.OutcomeSuccess
	errorCategory := crs.ErrorCategoryNone
	errorMsg := ""
	if err != nil {
		outcome = crs.OutcomeFailure
		errorCategory = classifyHLDError(err)
		errorMsg = err.Error()
	}

	// Build StepRecord
	step := crs.StepRecord{
		SessionID:  sessionID,
		StepNumber: ga.nextStepNumber(),
		Timestamp:  time.Now().UnixMilli(),
		Actor:      crs.ActorSystem,
		Decision:   crs.DecisionExecuteTool,
		Tool:       "LCA",
		ToolParams: &crs.ToolParams{
			Target: u,
			Query:  v,
		},
		Outcome:       outcome,
		ErrorCategory: errorCategory,
		ErrorMessage:  errorMsg,
		DurationMs:    duration.Milliseconds(),
		ResultSummary: fmt.Sprintf("LCA(%s,%s)=%s", u, v, lca),
		Propagate:     false, // Internal system operation
		Terminal:      false,
	}

	// Validate and record
	if err := step.Validate(); err != nil {
		ga.logger.Error("invalid step record",
			slog.String("error", err.Error()),
			slog.String("tool", "LCA"),
		)
	} else {
		if err := ga.crs.RecordStep(ctx, step); err != nil {
			ga.logger.Warn("failed to record step",
				slog.String("error", err.Error()),
				slog.String("tool", "LCA"),
			)
		}
	}

	// Update activity timestamp for timeout tracking
	ga.updateActivity()

	return lca, err
}

// DistanceWithCRS computes distance and records a StepRecord to CRS.
//
// Description:
//
//	Wraps HLD.Distance() to provide CRS integration. Records the operation
//	as a StepRecord with timing, outcome, and result summary.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - u, v: Node IDs. Must not be empty.
//
// Outputs:
//   - int: Distance between nodes.
//   - error: Non-nil on failure.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) DistanceWithCRS(ctx context.Context, u, v string) (int, error) {
	// Auto-start session if enabled and no session active
	if err := ga.ensureSession(ctx); err != nil {
		return 0, fmt.Errorf("auto-start session: %w", err)
	}

	// Check if CRS integration is enabled
	ga.mu.RLock()
	hasCRS := ga.crs != nil && ga.currentSessionID != "" && ga.hld != nil
	sessionID := ga.currentSessionID
	ga.mu.RUnlock()

	if !hasCRS {
		// No CRS or session - fall through to raw HLD call
		if ga.hld == nil {
			return 0, errors.New("HLD not initialized")
		}
		return ga.hld.Distance(ctx, u, v)
	}

	// Execute HLD query with timing
	start := time.Now()
	dist, err := ga.hld.Distance(ctx, u, v)
	duration := time.Since(start)

	// Determine outcome
	outcome := crs.OutcomeSuccess
	errorCategory := crs.ErrorCategoryNone
	errorMsg := ""
	if err != nil {
		outcome = crs.OutcomeFailure
		errorCategory = classifyHLDError(err)
		errorMsg = err.Error()
	}

	// Build StepRecord
	step := crs.StepRecord{
		SessionID:  sessionID,
		StepNumber: ga.nextStepNumber(),
		Timestamp:  time.Now().UnixMilli(),
		Actor:      crs.ActorSystem,
		Decision:   crs.DecisionExecuteTool,
		Tool:       "Distance",
		ToolParams: &crs.ToolParams{
			Target: u,
			Query:  v,
		},
		Outcome:       outcome,
		ErrorCategory: errorCategory,
		ErrorMessage:  errorMsg,
		DurationMs:    duration.Milliseconds(),
		ResultSummary: fmt.Sprintf("Distance(%s,%s)=%d", u, v, dist),
		Propagate:     false,
		Terminal:      false,
	}

	// Validate and record
	if err := step.Validate(); err != nil {
		ga.logger.Error("invalid step record",
			slog.String("error", err.Error()),
			slog.String("tool", "Distance"),
		)
	} else {
		if err := ga.crs.RecordStep(ctx, step); err != nil {
			ga.logger.Warn("failed to record step",
				slog.String("error", err.Error()),
				slog.String("tool", "Distance"),
			)
		}
	}

	// Update activity timestamp for timeout tracking
	ga.updateActivity()

	return dist, err
}

// DecomposePathWithCRS decomposes path and records a StepRecord to CRS.
//
// Description:
//
//	Wraps HLD.DecomposePath() to provide CRS integration. Records the operation
//	as a StepRecord with timing, outcome, and result summary.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - u, v: Node IDs. Must not be empty.
//
// Outputs:
//   - []PathSegment: Path segments (O(log V) segments).
//   - error: Non-nil on failure.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) DecomposePathWithCRS(ctx context.Context, u, v string) ([]PathSegment, error) {
	// Auto-start session if enabled and no session active
	if err := ga.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("auto-start session: %w", err)
	}

	// Check if CRS integration is enabled
	ga.mu.RLock()
	hasCRS := ga.crs != nil && ga.currentSessionID != "" && ga.hld != nil
	sessionID := ga.currentSessionID
	ga.mu.RUnlock()

	if !hasCRS {
		// No CRS or session - fall through to raw HLD call
		if ga.hld == nil {
			return nil, errors.New("HLD not initialized")
		}
		return ga.hld.DecomposePath(ctx, u, v)
	}

	// Execute HLD query with timing
	start := time.Now()
	segments, err := ga.hld.DecomposePath(ctx, u, v)
	duration := time.Since(start)

	// Determine outcome
	outcome := crs.OutcomeSuccess
	errorCategory := crs.ErrorCategoryNone
	errorMsg := ""
	if err != nil {
		outcome = crs.OutcomeFailure
		errorCategory = classifyHLDError(err)
		errorMsg = err.Error()
	}

	// Count total positions
	totalPositions := 0
	for _, seg := range segments {
		totalPositions += (seg.End - seg.Start + 1)
	}

	// Build StepRecord
	step := crs.StepRecord{
		SessionID:  sessionID,
		StepNumber: ga.nextStepNumber(),
		Timestamp:  time.Now().UnixMilli(),
		Actor:      crs.ActorSystem,
		Decision:   crs.DecisionExecuteTool,
		Tool:       "DecomposePath",
		ToolParams: &crs.ToolParams{
			Target: u,
			Query:  v,
		},
		Outcome:       outcome,
		ErrorCategory: errorCategory,
		ErrorMessage:  errorMsg,
		DurationMs:    duration.Milliseconds(),
		ResultSummary: fmt.Sprintf("DecomposePath(%s,%s) segments=%d positions=%d", u, v, len(segments), totalPositions),
		Propagate:     false,
		Terminal:      false,
	}

	// Validate and record
	if err := step.Validate(); err != nil {
		ga.logger.Error("invalid step record",
			slog.String("error", err.Error()),
			slog.String("tool", "DecomposePath"),
		)
	} else {
		if err := ga.crs.RecordStep(ctx, step); err != nil {
			ga.logger.Warn("failed to record step",
				slog.String("error", err.Error()),
				slog.String("tool", "DecomposePath"),
			)
		}
	}

	// Update activity timestamp for timeout tracking
	ga.updateActivity()

	return segments, err
}

// BatchLCAWithCRS computes batch LCA and records a StepRecord to CRS.
//
// Description:
//
//	Wraps HLD.BatchLCA() to provide CRS integration. Records a single StepRecord
//	for the entire batch operation with aggregate statistics.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - pairs: Array of node ID pairs. Must not be empty.
//
// Outputs:
//   - []string: LCA results (one per pair).
//   - []error: Errors (one per pair, nil on success).
//   - error: Non-nil on total failure.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) BatchLCAWithCRS(ctx context.Context, pairs [][2]string) ([]string, []error, error) {
	// Auto-start session if enabled and no session active
	if err := ga.ensureSession(ctx); err != nil {
		return nil, nil, fmt.Errorf("auto-start session: %w", err)
	}

	// Check if CRS integration is enabled
	ga.mu.RLock()
	hasCRS := ga.crs != nil && ga.currentSessionID != "" && ga.hld != nil
	sessionID := ga.currentSessionID
	ga.mu.RUnlock()

	if !hasCRS {
		// No CRS or session - fall through to raw HLD call
		if ga.hld == nil {
			return nil, nil, errors.New("HLD not initialized")
		}
		return ga.hld.BatchLCA(ctx, pairs)
	}

	// Execute HLD batch query with timing
	start := time.Now()
	results, errs, batchErr := ga.hld.BatchLCA(ctx, pairs)
	duration := time.Since(start)

	// Count successes and errors
	successCount := 0
	errorCount := 0
	for _, err := range errs {
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	// Determine outcome
	// Note: CRS doesn't have OutcomePartial, so use Success with error details in summary
	outcome := crs.OutcomeSuccess
	if errorCount == len(pairs) {
		outcome = crs.OutcomeFailure
	}

	errorCategory := crs.ErrorCategoryNone
	errorMsg := ""
	if batchErr != nil {
		errorCategory = classifyHLDError(batchErr)
		errorMsg = batchErr.Error()
	}

	// Build StepRecord
	step := crs.StepRecord{
		SessionID:  sessionID,
		StepNumber: ga.nextStepNumber(),
		Timestamp:  time.Now().UnixMilli(),
		Actor:      crs.ActorSystem,
		Decision:   crs.DecisionExecuteTool,
		Tool:       "BatchLCA",
		ToolParams: &crs.ToolParams{
			Limit: len(pairs),
		},
		Outcome:       outcome,
		ErrorCategory: errorCategory,
		ErrorMessage:  errorMsg,
		DurationMs:    duration.Milliseconds(),
		ResultSummary: fmt.Sprintf("BatchLCA pairs=%d success=%d errors=%d", len(pairs), successCount, errorCount),
		Propagate:     false,
		Terminal:      false,
	}

	// Validate and record
	if err := step.Validate(); err != nil {
		ga.logger.Error("invalid step record",
			slog.String("error", err.Error()),
			slog.String("tool", "BatchLCA"),
		)
	} else {
		if err := ga.crs.RecordStep(ctx, step); err != nil {
			ga.logger.Warn("failed to record step",
				slog.String("error", err.Error()),
				slog.String("tool", "BatchLCA"),
			)
		}
	}

	// Update activity timestamp for timeout tracking
	ga.updateActivity()

	return results, errs, batchErr
}

// =============================================================================
// Error Classification Helper (GR-19c)
// =============================================================================

// classifyHLDError maps HLD errors to typed ErrorCategory.
//
// Description:
//
//	Converts HLD-specific errors to CRS ErrorCategory enums for consistent
//	error tracking and analytics.
//
// Inputs:
//   - err: The error to classify.
//
// Outputs:
//   - crs.ErrorCategory: Typed error classification.
//
// Thread Safety: Safe for concurrent use (pure function).
func classifyHLDError(err error) crs.ErrorCategory {
	if err == nil {
		return crs.ErrorCategoryNone
	}

	switch {
	case errors.Is(err, ErrNodeNotFound):
		return crs.ErrorCategoryToolNotFound
	case errors.Is(err, ErrHLDNotInitialized):
		return crs.ErrorCategoryInternal
	case errors.Is(err, ErrNodesInDifferentTrees):
		return crs.ErrorCategoryInvalidParams
	case errors.Is(err, context.DeadlineExceeded):
		return crs.ErrorCategoryTimeout
	case errors.Is(err, context.Canceled):
		return crs.ErrorCategoryInternal
	default:
		return crs.ErrorCategoryInternal
	}
}

// =============================================================================
// Path Aggregate Queries (GR-19d)
// =============================================================================

// getOrCreatePathQueryEngine gets or creates a PathQueryEngine for the given aggregation function.
//
// Description:
//
//	Lazily creates and caches PathQueryEngine instances per aggregation function.
//	Builds segment tree on-demand using the provided value function.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) getOrCreatePathQueryEngine(ctx context.Context, aggFunc AggregateFunc, valueFunc func(string) int64) (*PathQueryEngine, error) {
	// Fast path: check if engine exists
	ga.pqeMu.RLock()
	engine, ok := ga.pathQueryEngines[aggFunc]
	ga.pqeMu.RUnlock()

	if ok {
		return engine, nil
	}

	// Slow path: create new engine
	ga.pqeMu.Lock()
	defer ga.pqeMu.Unlock()

	// Double-check after acquiring write lock
	if engine, ok := ga.pathQueryEngines[aggFunc]; ok {
		return engine, nil
	}

	// Build value array in HLD position order
	valueArr := make([]int64, ga.hld.NodeCount())
	for i := 0; i < ga.hld.NodeCount(); i++ {
		nodeIdx := ga.hld.NodeAtPos(i)
		nodeID, err := ga.hld.IdxToNode(nodeIdx)
		if err != nil {
			return nil, fmt.Errorf("getting node ID at position %d: %w", i, err)
		}
		valueArr[i] = valueFunc(nodeID)
	}

	// Build segment tree
	segTree, err := NewSegmentTree(ctx, valueArr, aggFunc)
	if err != nil {
		return nil, fmt.Errorf("building segment tree: %w", err)
	}

	// Create path query engine with CRS context (H-IMPL-3)
	opts := DefaultPathQueryEngineOptions()
	ga.mu.RLock()
	sessionID := ga.currentSessionID
	ga.mu.RUnlock()
	engine, err = NewPathQueryEngineWithCRS(ga.hld, segTree, aggFunc, &opts, ga.crs, sessionID)
	if err != nil {
		return nil, fmt.Errorf("creating path query engine: %w", err)
	}

	// Cache for reuse
	ga.pathQueryEngines[aggFunc] = engine
	return engine, nil
}

// pathQueryCacheKey generates a BadgerDB cache key for path queries.
//
// Format: pathquery:{graphHash}:{u}:{v}:{aggFunc}
//
// Thread Safety: Safe for concurrent use (read-only).
func (ga *GraphAnalytics) pathQueryCacheKey(u, v string, aggFunc AggregateFunc) string {
	graphHash := ga.hld.GraphHash()
	return fmt.Sprintf("pathquery:%s:%s:%s:%s", graphHash, u, v, aggFunc.String())
}

// getCachedPathQueryResult attempts to retrieve a cached path query result from BadgerDB.
//
// Returns (result, true) if cache hit, (0, false) if cache miss or error.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) getCachedPathQueryResult(ctx context.Context, u, v string, aggFunc AggregateFunc) (int64, bool) {
	if !ga.enableBadgerCache || ga.db == nil {
		return 0, false
	}

	key := ga.pathQueryCacheKey(u, v, aggFunc)

	var result int64
	err := ga.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(key))
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			if len(val) != 8 {
				return fmt.Errorf("invalid cached value length: %d", len(val))
			}
			result = int64(binary.BigEndian.Uint64(val))
			return nil
		})
	})

	if err != nil {
		if err != badger.ErrKeyNotFound {
			// Log unexpected errors
			ga.badgerCacheErrors.Add(1)
			ga.logger.Warn("badger cache read error",
				slog.String("key", key),
				slog.String("error", err.Error()),
			)
		}
		ga.badgerCacheMisses.Add(1)
		return 0, false
	}

	ga.badgerCacheHits.Add(1)
	return result, true
}

// setCachedPathQueryResult stores a path query result to BadgerDB.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) setCachedPathQueryResult(ctx context.Context, u, v string, aggFunc AggregateFunc, result int64) {
	if !ga.enableBadgerCache || ga.db == nil {
		return
	}

	key := ga.pathQueryCacheKey(u, v, aggFunc)
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, uint64(result))

	err := ga.db.Update(func(txn *badger.Txn) error {
		// No TTL - cache persists until graph hash changes
		return txn.Set([]byte(key), value)
	})

	if err != nil {
		ga.badgerCacheErrors.Add(1)
		ga.logger.Warn("badger cache write error",
			slog.String("key", key),
			slog.String("error", err.Error()),
		)
	}
}

// PathSumWithCRS computes sum of values on path from u to v with CRS recording.
//
// Description:
//
//	Computes the sum of node values along the path from u to v using HLD and segment tree.
//	Records operation to CRS for traceability and replay.
//	If BadgerDB caching is enabled, checks cache before computing.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - u: Start node ID. Must exist in graph.
//   - v: End node ID. Must exist in graph.
//   - valueFunc: Function that returns value for each node. Must not be nil.
//
// Outputs:
//   - int64: Sum of values on path u → v.
//   - error: Non-nil if nodes don't exist or query fails.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) PathSumWithCRS(ctx context.Context, u, v string, valueFunc func(string) int64) (int64, error) {
	// Auto-start session if enabled
	if err := ga.ensureSession(ctx); err != nil {
		return 0, fmt.Errorf("auto-start session: %w", err)
	}

	startTime := time.Now()

	// Check BadgerDB cache (C-IMPL-2)
	if cachedResult, found := ga.getCachedPathQueryResult(ctx, u, v, AggregateSUM); found {
		duration := time.Since(startTime)

		// Record CRS trace step for cache hit
		if ga.crs != nil {
			ga.mu.RLock()
			sessionID := ga.currentSessionID
			ga.mu.RUnlock()

			step := crs.StepRecord{
				SessionID:  sessionID,
				StepNumber: ga.nextStepNumber(),
				Timestamp:  time.Now().UnixMilli(),
				Actor:      crs.ActorSystem,
				Decision:   crs.DecisionExecuteTool,
				Tool:       "PathSum",
				ToolParams: &crs.ToolParams{
					Target: u,
					Query:  v,
				},
				Outcome:       crs.OutcomeSuccess,
				ErrorCategory: crs.ErrorCategoryNone,
				DurationMs:    duration.Milliseconds(),
				ResultSummary: fmt.Sprintf("PathSum(%s,%s)=%d [cached]", u, v, cachedResult),
				Propagate:     false,
				Terminal:      false,
			}

			if err := step.Validate(); err != nil {
				ga.logger.Error("invalid step record",
					slog.String("error", err.Error()),
					slog.String("tool", "PathSum"),
				)
			} else {
				if err := ga.crs.RecordStep(ctx, step); err != nil {
					ga.logger.Warn("failed to record step",
						slog.String("error", err.Error()),
						slog.String("tool", "PathSum"),
					)
				}
			}
		}

		ga.updateActivity()
		return cachedResult, nil
	}

	// Get or create path query engine for SUM
	engine, err := ga.getOrCreatePathQueryEngine(ctx, AggregateSUM, valueFunc)
	if err != nil {
		return 0, fmt.Errorf("getting path query engine: %w", err)
	}

	// Execute query
	result, err := engine.PathQuery(ctx, u, v, ga.logger)
	if err != nil {
		return 0, fmt.Errorf("path sum query %s->%s: %w", u, v, err)
	}

	// Store to BadgerDB cache (C-IMPL-2)
	ga.setCachedPathQueryResult(ctx, u, v, AggregateSUM, result)

	duration := time.Since(startTime)

	// Record CRS trace step (cache miss)
	if ga.crs != nil {
		ga.mu.RLock()
		sessionID := ga.currentSessionID
		ga.mu.RUnlock()

		outcome := crs.OutcomeSuccess
		errorCategory := crs.ErrorCategoryNone
		errorMsg := ""
		if err != nil {
			outcome = crs.OutcomeFailure
			errorCategory = classifyHLDError(err)
			errorMsg = err.Error()
		}

		step := crs.StepRecord{
			SessionID:  sessionID,
			StepNumber: ga.nextStepNumber(),
			Timestamp:  time.Now().UnixMilli(),
			Actor:      crs.ActorSystem,
			Decision:   crs.DecisionExecuteTool,
			Tool:       "PathSum",
			ToolParams: &crs.ToolParams{
				Target: u,
				Query:  v,
			},
			Outcome:       outcome,
			ErrorCategory: errorCategory,
			ErrorMessage:  errorMsg,
			DurationMs:    duration.Milliseconds(),
			ResultSummary: fmt.Sprintf("PathSum(%s,%s)=%d", u, v, result),
			Propagate:     false,
			Terminal:      false,
		}

		if err := step.Validate(); err != nil {
			ga.logger.Error("invalid step record",
				slog.String("error", err.Error()),
				slog.String("tool", "PathSum"),
			)
		} else {
			if err := ga.crs.RecordStep(ctx, step); err != nil {
				ga.logger.Warn("failed to record step",
					slog.String("error", err.Error()),
					slog.String("tool", "PathSum"),
				)
			}
		}
	}

	// Update activity timestamp
	ga.updateActivity()

	return result, nil
}

// PathMinWithCRS computes minimum value on path from u to v with CRS recording.
//
// Description:
//
//	Computes the minimum of node values along the path from u to v using HLD and segment tree.
//	If BadgerDB caching is enabled, checks cache before computing.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) PathMinWithCRS(ctx context.Context, u, v string, valueFunc func(string) int64) (int64, error) {
	if err := ga.ensureSession(ctx); err != nil {
		return 0, fmt.Errorf("auto-start session: %w", err)
	}

	startTime := time.Now()

	// Check BadgerDB cache (C-IMPL-2)
	if cachedResult, found := ga.getCachedPathQueryResult(ctx, u, v, AggregateMIN); found {
		duration := time.Since(startTime)

		if ga.crs != nil {
			ga.mu.RLock()
			sessionID := ga.currentSessionID
			ga.mu.RUnlock()

			step := crs.StepRecord{
				SessionID:  sessionID,
				StepNumber: ga.nextStepNumber(),
				Timestamp:  time.Now().UnixMilli(),
				Actor:      crs.ActorSystem,
				Decision:   crs.DecisionExecuteTool,
				Tool:       "PathMin",
				ToolParams: &crs.ToolParams{
					Target: u,
					Query:  v,
				},
				Outcome:       crs.OutcomeSuccess,
				ErrorCategory: crs.ErrorCategoryNone,
				DurationMs:    duration.Milliseconds(),
				ResultSummary: fmt.Sprintf("PathMin(%s,%s)=%d [cached]", u, v, cachedResult),
				Propagate:     false,
				Terminal:      false,
			}

			if err := step.Validate(); err != nil {
				ga.logger.Error("invalid step record",
					slog.String("error", err.Error()),
					slog.String("tool", "PathMin"),
				)
			} else {
				if err := ga.crs.RecordStep(ctx, step); err != nil {
					ga.logger.Warn("failed to record step",
						slog.String("error", err.Error()),
						slog.String("tool", "PathMin"),
					)
				}
			}
		}

		ga.updateActivity()
		return cachedResult, nil
	}

	engine, err := ga.getOrCreatePathQueryEngine(ctx, AggregateMIN, valueFunc)
	if err != nil {
		return 0, fmt.Errorf("getting path query engine: %w", err)
	}

	result, err := engine.PathQuery(ctx, u, v, ga.logger)
	if err != nil {
		return 0, fmt.Errorf("path min query %s->%s: %w", u, v, err)
	}

	// Store to BadgerDB cache (C-IMPL-2)
	ga.setCachedPathQueryResult(ctx, u, v, AggregateMIN, result)

	duration := time.Since(startTime)

	if ga.crs != nil {
		ga.mu.RLock()
		sessionID := ga.currentSessionID
		ga.mu.RUnlock()

		outcome := crs.OutcomeSuccess
		errorCategory := crs.ErrorCategoryNone
		errorMsg := ""
		if err != nil {
			outcome = crs.OutcomeFailure
			errorCategory = classifyHLDError(err)
			errorMsg = err.Error()
		}

		step := crs.StepRecord{
			SessionID:  sessionID,
			StepNumber: ga.nextStepNumber(),
			Timestamp:  time.Now().UnixMilli(),
			Actor:      crs.ActorSystem,
			Decision:   crs.DecisionExecuteTool,
			Tool:       "PathMin",
			ToolParams: &crs.ToolParams{
				Target: u,
				Query:  v,
			},
			Outcome:       outcome,
			ErrorCategory: errorCategory,
			ErrorMessage:  errorMsg,
			DurationMs:    duration.Milliseconds(),
			ResultSummary: fmt.Sprintf("PathMin(%s,%s)=%d", u, v, result),
			Propagate:     false,
			Terminal:      false,
		}

		if err := step.Validate(); err != nil {
			ga.logger.Error("invalid step record",
				slog.String("error", err.Error()),
				slog.String("tool", "PathMin"),
			)
		} else {
			if err := ga.crs.RecordStep(ctx, step); err != nil {
				ga.logger.Warn("failed to record step",
					slog.String("error", err.Error()),
					slog.String("tool", "PathMin"),
				)
			}
		}
	}

	ga.updateActivity()
	return result, nil
}

// PathMaxWithCRS computes maximum value on path from u to v with CRS recording.
//
// Description:
//
//	Computes the maximum of node values along the path from u to v using HLD and segment tree.
//	If BadgerDB caching is enabled, checks cache before computing.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) PathMaxWithCRS(ctx context.Context, u, v string, valueFunc func(string) int64) (int64, error) {
	if err := ga.ensureSession(ctx); err != nil {
		return 0, fmt.Errorf("auto-start session: %w", err)
	}

	startTime := time.Now()

	// Check BadgerDB cache (C-IMPL-2)
	if cachedResult, found := ga.getCachedPathQueryResult(ctx, u, v, AggregateMAX); found {
		duration := time.Since(startTime)

		if ga.crs != nil {
			ga.mu.RLock()
			sessionID := ga.currentSessionID
			ga.mu.RUnlock()

			step := crs.StepRecord{
				SessionID:  sessionID,
				StepNumber: ga.nextStepNumber(),
				Timestamp:  time.Now().UnixMilli(),
				Actor:      crs.ActorSystem,
				Decision:   crs.DecisionExecuteTool,
				Tool:       "PathMax",
				ToolParams: &crs.ToolParams{
					Target: u,
					Query:  v,
				},
				Outcome:       crs.OutcomeSuccess,
				ErrorCategory: crs.ErrorCategoryNone,
				DurationMs:    duration.Milliseconds(),
				ResultSummary: fmt.Sprintf("PathMax(%s,%s)=%d [cached]", u, v, cachedResult),
				Propagate:     false,
				Terminal:      false,
			}

			if err := step.Validate(); err != nil {
				ga.logger.Error("invalid step record",
					slog.String("error", err.Error()),
					slog.String("tool", "PathMax"),
				)
			} else {
				if err := ga.crs.RecordStep(ctx, step); err != nil {
					ga.logger.Warn("failed to record step",
						slog.String("error", err.Error()),
						slog.String("tool", "PathMax"),
					)
				}
			}
		}

		ga.updateActivity()
		return cachedResult, nil
	}

	engine, err := ga.getOrCreatePathQueryEngine(ctx, AggregateMAX, valueFunc)
	if err != nil {
		return 0, fmt.Errorf("getting path query engine: %w", err)
	}

	result, err := engine.PathQuery(ctx, u, v, ga.logger)
	if err != nil {
		return 0, fmt.Errorf("path max query %s->%s: %w", u, v, err)
	}

	// Store to BadgerDB cache (C-IMPL-2)
	ga.setCachedPathQueryResult(ctx, u, v, AggregateMAX, result)

	duration := time.Since(startTime)

	if ga.crs != nil {
		ga.mu.RLock()
		sessionID := ga.currentSessionID
		ga.mu.RUnlock()

		outcome := crs.OutcomeSuccess
		errorCategory := crs.ErrorCategoryNone
		errorMsg := ""
		if err != nil {
			outcome = crs.OutcomeFailure
			errorCategory = classifyHLDError(err)
			errorMsg = err.Error()
		}

		step := crs.StepRecord{
			SessionID:  sessionID,
			StepNumber: ga.nextStepNumber(),
			Timestamp:  time.Now().UnixMilli(),
			Actor:      crs.ActorSystem,
			Decision:   crs.DecisionExecuteTool,
			Tool:       "PathMax",
			ToolParams: &crs.ToolParams{
				Target: u,
				Query:  v,
			},
			Outcome:       outcome,
			ErrorCategory: errorCategory,
			ErrorMessage:  errorMsg,
			DurationMs:    duration.Milliseconds(),
			ResultSummary: fmt.Sprintf("PathMax(%s,%s)=%d", u, v, result),
			Propagate:     false,
			Terminal:      false,
		}

		if err := step.Validate(); err != nil {
			ga.logger.Error("invalid step record",
				slog.String("error", err.Error()),
				slog.String("tool", "PathMax"),
			)
		} else {
			if err := ga.crs.RecordStep(ctx, step); err != nil {
				ga.logger.Warn("failed to record step",
					slog.String("error", err.Error()),
					slog.String("tool", "PathMax"),
				)
			}
		}
	}

	ga.updateActivity()
	return result, nil
}

// PathGCDWithCRS computes GCD of values on path from u to v with CRS recording.
//
// Description:
//
//	Computes the GCD of node values along the path from u to v using HLD and segment tree.
//	If BadgerDB caching is enabled, checks cache before computing.
//
// Thread Safety: Safe for concurrent use.
func (ga *GraphAnalytics) PathGCDWithCRS(ctx context.Context, u, v string, valueFunc func(string) int64) (int64, error) {
	if err := ga.ensureSession(ctx); err != nil {
		return 0, fmt.Errorf("auto-start session: %w", err)
	}

	startTime := time.Now()

	// Check BadgerDB cache (C-IMPL-2)
	if cachedResult, found := ga.getCachedPathQueryResult(ctx, u, v, AggregateGCD); found {
		duration := time.Since(startTime)

		if ga.crs != nil {
			ga.mu.RLock()
			sessionID := ga.currentSessionID
			ga.mu.RUnlock()

			step := crs.StepRecord{
				SessionID:  sessionID,
				StepNumber: ga.nextStepNumber(),
				Timestamp:  time.Now().UnixMilli(),
				Actor:      crs.ActorSystem,
				Decision:   crs.DecisionExecuteTool,
				Tool:       "PathGCD",
				ToolParams: &crs.ToolParams{
					Target: u,
					Query:  v,
				},
				Outcome:       crs.OutcomeSuccess,
				ErrorCategory: crs.ErrorCategoryNone,
				DurationMs:    duration.Milliseconds(),
				ResultSummary: fmt.Sprintf("PathGCD(%s,%s)=%d [cached]", u, v, cachedResult),
				Propagate:     false,
				Terminal:      false,
			}

			if err := step.Validate(); err != nil {
				ga.logger.Error("invalid step record",
					slog.String("error", err.Error()),
					slog.String("tool", "PathGCD"),
				)
			} else {
				if err := ga.crs.RecordStep(ctx, step); err != nil {
					ga.logger.Warn("failed to record step",
						slog.String("error", err.Error()),
						slog.String("tool", "PathGCD"),
					)
				}
			}
		}

		ga.updateActivity()
		return cachedResult, nil
	}

	engine, err := ga.getOrCreatePathQueryEngine(ctx, AggregateGCD, valueFunc)
	if err != nil {
		return 0, fmt.Errorf("getting path query engine: %w", err)
	}

	result, err := engine.PathQuery(ctx, u, v, ga.logger)
	if err != nil {
		return 0, fmt.Errorf("path gcd query %s->%s: %w", u, v, err)
	}

	// Store to BadgerDB cache (C-IMPL-2)
	ga.setCachedPathQueryResult(ctx, u, v, AggregateGCD, result)

	duration := time.Since(startTime)

	if ga.crs != nil {
		ga.mu.RLock()
		sessionID := ga.currentSessionID
		ga.mu.RUnlock()

		outcome := crs.OutcomeSuccess
		errorCategory := crs.ErrorCategoryNone
		errorMsg := ""
		if err != nil {
			outcome = crs.OutcomeFailure
			errorCategory = classifyHLDError(err)
			errorMsg = err.Error()
		}

		step := crs.StepRecord{
			SessionID:  sessionID,
			StepNumber: ga.nextStepNumber(),
			Timestamp:  time.Now().UnixMilli(),
			Actor:      crs.ActorSystem,
			Decision:   crs.DecisionExecuteTool,
			Tool:       "PathGCD",
			ToolParams: &crs.ToolParams{
				Target: u,
				Query:  v,
			},
			Outcome:       outcome,
			ErrorCategory: errorCategory,
			ErrorMessage:  errorMsg,
			DurationMs:    duration.Milliseconds(),
			ResultSummary: fmt.Sprintf("PathGCD(%s,%s)=%d", u, v, result),
			Propagate:     false,
			Terminal:      false,
		}

		if err := step.Validate(); err != nil {
			ga.logger.Error("invalid step record",
				slog.String("error", err.Error()),
				slog.String("tool", "PathGCD"),
			)
		} else {
			if err := ga.crs.RecordStep(ctx, step); err != nil {
				ga.logger.Warn("failed to record step",
					slog.String("error", err.Error()),
					slog.String("tool", "PathGCD"),
				)
			}
		}
	}

	ga.updateActivity()
	return result, nil
}
