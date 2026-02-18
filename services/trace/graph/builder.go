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
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Default builder configuration values.
const (
	// DefaultMaxMemoryMB is the default memory limit for building (512MB).
	DefaultMaxMemoryMB = 512

	// DefaultWorkerCount is the default number of parallel workers.
	// Set to 0 to use runtime.NumCPU().
	DefaultWorkerCount = 0

	// maxEmbedResolutionDepth is the maximum recursion depth for resolvePromotedMethods
	// and resolveInterfaceEmbedsRecursive. Prevents stack overflow on pathological
	// interface/struct embedding chains. 20 is generous for any realistic codebase
	// (Hugo's Page interface is 4 levels deep, the deepest known real-world case).
	maxEmbedResolutionDepth = 20
)

// ProgressPhase indicates which phase of building is in progress.
type ProgressPhase int

const (
	// ProgressPhaseCollecting indicates symbols are being collected as nodes.
	ProgressPhaseCollecting ProgressPhase = iota

	// ProgressPhaseExtractingEdges indicates edges are being extracted.
	ProgressPhaseExtractingEdges

	// ProgressPhaseFinalizing indicates the graph is being finalized.
	ProgressPhaseFinalizing
)

// String returns the string representation of the ProgressPhase.
func (p ProgressPhase) String() string {
	switch p {
	case ProgressPhaseCollecting:
		return "collecting"
	case ProgressPhaseExtractingEdges:
		return "extracting_edges"
	case ProgressPhaseFinalizing:
		return "finalizing"
	default:
		return "unknown"
	}
}

// BuildProgress contains progress information during a build.
type BuildProgress struct {
	// Phase is the current build phase.
	Phase ProgressPhase

	// FilesTotal is the total number of files to process.
	FilesTotal int

	// FilesProcessed is the number of files processed so far.
	FilesProcessed int

	// NodesCreated is the number of nodes created so far.
	NodesCreated int

	// EdgesCreated is the number of edges created so far.
	EdgesCreated int
}

// ProgressFunc is a callback function for build progress updates.
type ProgressFunc func(progress BuildProgress)

// BuilderOptions configures Builder behavior.
type BuilderOptions struct {
	// ProjectRoot is the absolute path to the project root directory.
	ProjectRoot string

	// MaxMemoryMB is the maximum memory usage in megabytes.
	// Build will stop with partial results if exceeded.
	// Default: 512
	MaxMemoryMB int

	// WorkerCount is the number of parallel workers for edge extraction.
	// Default: runtime.NumCPU()
	WorkerCount int

	// ProgressCallback is called periodically with build progress.
	// May be nil.
	ProgressCallback ProgressFunc

	// MaxNodes is the maximum number of nodes (passed to Graph).
	MaxNodes int

	// MaxEdges is the maximum number of edges (passed to Graph).
	MaxEdges int
}

// DefaultBuilderOptions returns sensible defaults.
func DefaultBuilderOptions() BuilderOptions {
	return BuilderOptions{
		MaxMemoryMB: DefaultMaxMemoryMB,
		WorkerCount: runtime.NumCPU(),
		MaxNodes:    DefaultMaxNodes,
		MaxEdges:    DefaultMaxEdges,
	}
}

// BuilderOption is a functional option for configuring Builder.
type BuilderOption func(*BuilderOptions)

// WithProjectRoot sets the project root path.
func WithProjectRoot(root string) BuilderOption {
	return func(o *BuilderOptions) {
		o.ProjectRoot = root
	}
}

// WithMaxMemoryMB sets the maximum memory usage in megabytes.
func WithMaxMemoryMB(mb int) BuilderOption {
	return func(o *BuilderOptions) {
		o.MaxMemoryMB = mb
	}
}

// WithWorkerCount sets the number of parallel workers.
func WithWorkerCount(n int) BuilderOption {
	return func(o *BuilderOptions) {
		o.WorkerCount = n
	}
}

// WithProgressCallback sets the progress callback function.
func WithProgressCallback(fn ProgressFunc) BuilderOption {
	return func(o *BuilderOptions) {
		o.ProgressCallback = fn
	}
}

// WithBuilderMaxNodes sets the maximum number of nodes.
func WithBuilderMaxNodes(n int) BuilderOption {
	return func(o *BuilderOptions) {
		o.MaxNodes = n
	}
}

// WithBuilderMaxEdges sets the maximum number of edges.
func WithBuilderMaxEdges(n int) BuilderOption {
	return func(o *BuilderOptions) {
		o.MaxEdges = n
	}
}

// Builder constructs code graphs from parsed AST results.
//
// The builder is stateless and can be reused across multiple builds.
// Each Build() call creates a new graph.
//
// Thread Safety:
//
//	Builder is safe for concurrent use. Each Build() call operates
//	independently with its own internal state.
type Builder struct {
	options BuilderOptions
}

// NewBuilder creates a new Builder with the given options.
//
// Example:
//
//	builder := NewBuilder(
//	    WithProjectRoot("/path/to/project"),
//	    WithMaxMemoryMB(1024),
//	)
func NewBuilder(opts ...BuilderOption) *Builder {
	options := DefaultBuilderOptions()
	for _, opt := range opts {
		opt(&options)
	}

	if options.WorkerCount <= 0 {
		options.WorkerCount = runtime.NumCPU()
	}

	return &Builder{
		options: options,
	}
}

// buildState holds mutable state during a single build operation.
type buildState struct {
	graph         *Graph
	result        *BuildResult
	symbolsByID   map[string]*ast.Symbol
	symbolsByName map[string][]*ast.Symbol
	fileImports   map[string][]ast.Import // filePath -> imports
	placeholders  map[string]*Node        // external ID -> placeholder node
	mu            sync.Mutex              // protects placeholders
	startTime     time.Time

	// symbolParent maps a child symbol ID to its parent symbol ID.
	// Built during collectPhase to enable O(1) method → owning class lookup.
	// Used by resolveCallTarget for this/self receiver resolution.
	symbolParent map[string]string

	// classExtends maps a class/struct name to its parent class name.
	// Built from Metadata.Extends during collectPhase.
	// Used by resolveCallTarget for inheritance-aware method resolution.
	classExtends map[string]string

	// importNameMap maps filePath → localName → importEntry.
	// R3-P2b: Built from fileImports during collectPhase. Enables import-aware
	// call resolution: when "merge" is called in frame.py and the file imports
	// "merge" from "pandas.core.reshape.merge", we can resolve to the right symbol.
	importNameMap map[string]map[string]importEntry
}

// importEntry represents a single imported name with its source module path
// and original name (for aliased imports).
//
// Description:
//
//	R3-P2b: For `from pandas.core.reshape.merge import merge as pd_merge`:
//	  - ModulePath: "pandas.core.reshape.merge"
//	  - OriginalName: "merge" (the name in the source module)
//	The importNameMap key would be "pd_merge" (the local name used in code).
//
// Thread Safety: Immutable after construction.
type importEntry struct {
	ModulePath   string // "pandas.core.reshape.merge"
	OriginalName string // "merge" (the name in the source module)
}

// Build constructs a graph from the given parse results.
//
// Description:
//
//	Processes all parse results, creating nodes for symbols and edges
//	for their relationships. The build is resilient to individual file
//	failures - partial results are returned even on errors.
//
// Inputs:
//
//	ctx - Context for cancellation. Build checks context periodically.
//	results - Parse results from AST parsing. Nil entries are skipped with error.
//
// Outputs:
//
//	*BuildResult - Contains the graph, any errors, and build statistics.
//	error - Non-nil only for fatal errors (context cancelled returns partial result).
//
// Build Phases:
//
//  1. COLLECT: Validate and add all symbols as nodes
//  2. EXTRACT EDGES: Create edges for imports, calls, implements, etc.
//  3. FINALIZE: Freeze graph and compute statistics
func (b *Builder) Build(ctx context.Context, results []*ast.ParseResult) (*BuildResult, error) {
	// Start tracing span
	ctx, span := startBuildSpan(ctx, len(results))
	defer span.End()

	state := &buildState{
		graph: NewGraph(b.options.ProjectRoot,
			WithMaxNodes(b.options.MaxNodes),
			WithMaxEdges(b.options.MaxEdges),
		),
		result: &BuildResult{
			FileErrors: make([]FileError, 0),
			EdgeErrors: make([]EdgeError, 0),
		},
		symbolsByID:   make(map[string]*ast.Symbol),
		symbolsByName: make(map[string][]*ast.Symbol),
		fileImports:   make(map[string][]ast.Import),
		placeholders:  make(map[string]*Node),
		symbolParent:  make(map[string]string),
		classExtends:  make(map[string]string),
		importNameMap: make(map[string]map[string]importEntry),
		startTime:     time.Now(),
	}
	state.result.Graph = state.graph

	// Phase 1: Collect symbols as nodes
	if err := b.collectPhase(ctx, state, results); err != nil {
		state.result.Incomplete = true
		duration := time.Since(state.startTime)
		state.result.Stats.DurationMilli = duration.Milliseconds()
		state.result.Stats.DurationMicro = duration.Microseconds()
		setBuildSpanResult(span, state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, true)
		recordBuildMetrics(ctx, time.Since(state.startTime), state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, false)
		return state.result, nil
	}

	// R3-P2b: Build import name map from fileImports (populated during collectPhase).
	b.buildImportNameMap(state)

	// Phase 2: Extract edges
	if err := b.extractEdgesPhase(ctx, state, results); err != nil {
		state.result.Incomplete = true
		duration := time.Since(state.startTime)
		state.result.Stats.DurationMilli = duration.Milliseconds()
		state.result.Stats.DurationMicro = duration.Microseconds()
		setBuildSpanResult(span, state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, true)
		recordBuildMetrics(ctx, time.Since(state.startTime), state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, false)
		return state.result, nil
	}

	// Phase 3: Finalize
	state.graph.Freeze()
	duration := time.Since(state.startTime)
	state.result.Stats.DurationMilli = duration.Milliseconds()
	state.result.Stats.DurationMicro = duration.Microseconds()

	b.reportProgress(state, ProgressPhaseFinalizing, len(results), len(results))

	// Record success metrics
	setBuildSpanResult(span, state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, false)
	recordBuildMetrics(ctx, time.Since(state.startTime), state.result.Stats.NodesCreated, state.result.Stats.EdgesCreated, true)

	return state.result, nil
}

// collectPhase validates parse results and adds symbols as nodes.
func (b *Builder) collectPhase(ctx context.Context, state *buildState, results []*ast.ParseResult) error {
	for i, r := range results {
		// Check context
		if err := ctx.Err(); err != nil {
			return err
		}

		// Validate parse result
		if err := b.validateParseResult(r); err != nil {
			filePath := ""
			if r != nil {
				filePath = r.FilePath
			} else {
				filePath = fmt.Sprintf("result[%d]", i)
			}
			state.result.FileErrors = append(state.result.FileErrors, FileError{
				FilePath: filePath,
				Err:      err,
			})
			state.result.Stats.FilesFailed++
			continue
		}

		// Store imports for edge extraction
		state.fileImports[r.FilePath] = r.Imports

		// Add symbols as nodes
		for _, sym := range r.Symbols {
			if sym == nil {
				continue
			}

			// Add to graph
			_, err := state.graph.AddNode(sym)
			if err != nil {
				state.result.FileErrors = append(state.result.FileErrors, FileError{
					FilePath: r.FilePath,
					Err:      fmt.Errorf("add node %s: %w", sym.ID, err),
				})
				continue
			}

			// Index for resolution
			state.symbolsByID[sym.ID] = sym
			state.symbolsByName[sym.Name] = append(state.symbolsByName[sym.Name], sym)
			state.result.Stats.NodesCreated++

			// Track class inheritance from Metadata.Extends
			if sym.Metadata != nil && sym.Metadata.Extends != "" {
				state.classExtends[sym.Name] = sym.Metadata.Extends
			}

			// Recursively add children with parent tracking
			b.addChildSymbols(state, sym.Children, sym.ID)
		}

		state.result.Stats.FilesProcessed++
		b.reportProgress(state, ProgressPhaseCollecting, len(results), i+1)
	}

	return nil
}

// addChildSymbols recursively adds child symbols to the graph.
// parentID tracks the owning symbol for reverse parent lookup.
func (b *Builder) addChildSymbols(state *buildState, children []*ast.Symbol, parentID string) {
	for _, child := range children {
		if child == nil {
			continue
		}

		_, err := state.graph.AddNode(child)
		if err != nil {
			// Log but don't fail - child nodes are optional
			continue
		}

		state.symbolsByID[child.ID] = child
		state.symbolsByName[child.Name] = append(state.symbolsByName[child.Name], child)
		state.result.Stats.NodesCreated++

		// Track parent-child relationship for receiver resolution
		if parentID != "" {
			state.symbolParent[child.ID] = parentID
		}

		// Track class inheritance from Metadata.Extends
		if child.Metadata != nil && child.Metadata.Extends != "" {
			state.classExtends[child.Name] = child.Metadata.Extends
		}

		// Recurse
		b.addChildSymbols(state, child.Children, child.ID)
	}
}

// extractEdgesPhase creates edges for symbol relationships.
func (b *Builder) extractEdgesPhase(ctx context.Context, state *buildState, results []*ast.ParseResult) error {
	for i, r := range results {
		// Check context
		if err := ctx.Err(); err != nil {
			return err
		}

		if r == nil {
			continue
		}

		// Extract edges for this file (GR-41: pass ctx for call edge tracing)
		b.extractFileEdges(ctx, state, r)

		b.reportProgress(state, ProgressPhaseExtractingEdges, len(results), i+1)
	}

	// GR-40 FIX (C-3): Associate methods with types across all files
	// This must run before interface detection because methods may be defined
	// in different files than their receiver types.
	b.associateMethodsWithTypesCrossFile(ctx, state)

	// GR-40/GR-40a: Compute interface implementations via method-set matching
	// This runs after all per-file edges are extracted because it needs
	// the complete set of interfaces and types across the entire project.
	// Supports Go interfaces and Python Protocols.
	if err := b.computeInterfaceImplementations(ctx, state); err != nil {
		// Non-fatal: interface detection failure shouldn't fail the build
		state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
			FromID:   "interface_detection",
			ToID:     "all",
			EdgeType: EdgeTypeImplements,
			Err:      err,
		})
	}

	// GR-41: Record call edge metrics after all edges extracted
	recordCallEdgeMetrics(ctx,
		state.result.Stats.CallEdgesResolved,
		state.result.Stats.CallEdgesUnresolved,
		state.result.Stats.CallEdgesResolved+state.result.Stats.CallEdgesUnresolved,
	)

	return nil
}

// extractFileEdges extracts all edge types from a single file's parse result.
// GR-41: Now accepts context for call edge tracing.
// GR-41c: Passes context to extractImportEdges.
func (b *Builder) extractFileEdges(ctx context.Context, state *buildState, r *ast.ParseResult) {
	// Extract import edges (GR-41c: now accepts context)
	b.extractImportEdges(ctx, state, r)

	// Extract edges from symbols
	for _, sym := range r.Symbols {
		if sym == nil {
			continue
		}

		b.extractSymbolEdges(ctx, state, sym, r)

		// Process children
		b.extractChildEdges(ctx, state, sym.Children, r)
	}
}

// extractChildEdges recursively extracts edges from child symbols.
// GR-41: Now accepts context for call edge tracing.
func (b *Builder) extractChildEdges(ctx context.Context, state *buildState, children []*ast.Symbol, r *ast.ParseResult) {
	for _, child := range children {
		if child == nil {
			continue
		}
		b.extractSymbolEdges(ctx, state, child, r)
		b.extractChildEdges(ctx, state, child.Children, r)
	}
}

// extractImportEdges creates IMPORTS edges from import statements.
//
// Description:
//
//	Creates EdgeTypeImports edges from the package symbol to imported packages.
//	GR-41c: Fixed to use actual package symbol instead of fabricated fileSymbolID.
//
// Inputs:
//   - ctx: Context for tracing and cancellation.
//   - state: The build state containing graph and symbol indexes.
//   - r: The ParseResult containing imports and symbols.
//
// Outputs:
//   - None. Edges are added to state.graph, errors to state.result.EdgeErrors.
//
// Thread Safety:
//
//	This method modifies state.graph and state.result. Not safe for concurrent
//	use on the same buildState, but the builder serializes calls appropriately.
func (b *Builder) extractImportEdges(ctx context.Context, state *buildState, r *ast.ParseResult) {
	if r == nil || len(r.Imports) == 0 {
		return
	}

	// GR-41c: OTel tracing for observability
	_, span := tracer.Start(ctx, "GraphBuilder.extractImportEdges",
		trace.WithAttributes(
			attribute.String("file", r.FilePath),
			attribute.Int("import_count", len(r.Imports)),
		),
	)
	defer span.End()

	// GR-41c: Find actual package symbol instead of fabricating fileSymbolID
	sourceID := findPackageSymbolID(r)
	if sourceID == "" {
		slog.Warn("GR-41c: No package symbol found for import edges",
			slog.String("file", r.FilePath),
			slog.Int("import_count", len(r.Imports)),
		)
		span.SetAttributes(attribute.Bool("no_source_symbol", true))
		return
	}

	// GR-41c: Verify sourceID exists in graph before using (I-1: use GetNode method)
	if _, exists := state.graph.GetNode(sourceID); !exists {
		slog.Warn("GR-41c: Package symbol not in graph",
			slog.String("file", r.FilePath),
			slog.String("source_id", sourceID),
		)
		span.SetAttributes(attribute.Bool("source_not_in_graph", true))
		return
	}

	edgesCreated := 0
	edgesFailed := 0

	for i, imp := range r.Imports {
		// R-1: Check context cancellation every 10 imports for responsiveness
		if i > 0 && i%10 == 0 {
			if ctx.Err() != nil {
				slog.Debug("GR-41c: Context cancelled during import edge extraction",
					slog.String("file", r.FilePath),
					slog.Int("processed", i),
					slog.Int("total", len(r.Imports)),
				)
				span.SetAttributes(
					attribute.Bool("cancelled", true),
					attribute.Int("processed_before_cancel", i),
				)
				recordImportEdgeMetrics(ctx, edgesCreated, edgesFailed)
				return
			}
		}
		// Create placeholder for imported package
		pkgID := b.getOrCreatePlaceholder(state, imp.Path, imp.Path)

		// Create edge from package symbol to imported package
		err := state.graph.AddEdge(sourceID, pkgID, EdgeTypeImports, imp.Location)
		if err != nil {
			// Check if it's a duplicate edge error (not fatal)
			if !strings.Contains(err.Error(), "already exists") {
				state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
					FromID:   sourceID,
					ToID:     pkgID,
					EdgeType: EdgeTypeImports,
					Err:      err,
				})
				edgesFailed++
				slog.Debug("GR-41c: Failed to create import edge",
					slog.String("file", r.FilePath),
					slog.String("from", sourceID),
					slog.String("to", pkgID),
					slog.String("error", err.Error()),
				)
			}
			continue
		}

		state.result.Stats.EdgesCreated++
		edgesCreated++

		slog.Debug("GR-41c: Created import edge",
			slog.String("file", r.FilePath),
			slog.String("from", sourceID),
			slog.String("import", imp.Path),
		)
	}

	// GR-41c: Record span attributes and metrics
	span.SetAttributes(
		attribute.Int("edges_created", edgesCreated),
		attribute.Int("edges_failed", edgesFailed),
	)
	recordImportEdgeMetrics(ctx, edgesCreated, edgesFailed)
}

// findPackageSymbolID finds the package symbol ID from a ParseResult.
//
// Description:
//
//	Searches the symbols for a SymbolKindPackage and returns its ID.
//	Falls back to the first symbol if no package symbol is found.
//	This is used by extractImportEdges to find the source node for
//	import edges.
//
// Inputs:
//   - r: The ParseResult to search. Must not be nil.
//
// Outputs:
//   - string: The symbol ID to use as import source, or empty if no symbols.
//
// Thread Safety: This function is safe for concurrent use.
func findPackageSymbolID(r *ast.ParseResult) string {
	if r == nil || len(r.Symbols) == 0 {
		return ""
	}

	// Strategy 1: Find explicit package symbol
	for _, sym := range r.Symbols {
		if sym != nil && sym.Kind == ast.SymbolKindPackage {
			return sym.ID
		}
	}

	// Strategy 2: Use first non-nil symbol as fallback
	for _, sym := range r.Symbols {
		if sym != nil {
			return sym.ID
		}
	}

	return ""
}

// extractSymbolEdges extracts edges for a single symbol.
// GR-41: Now accepts context for call edge tracing.
func (b *Builder) extractSymbolEdges(ctx context.Context, state *buildState, sym *ast.Symbol, r *ast.ParseResult) {
	switch sym.Kind {
	case ast.SymbolKindMethod:
		// Method -> Receiver type (RECEIVES edge)
		if sym.Receiver != "" {
			b.extractReceiverEdge(state, sym)
		}
		fallthrough // Methods can also have calls, returns, etc.

	case ast.SymbolKindFunction, ast.SymbolKindProperty:
		// GR-41: Extract call edges from function/method/property body.
		// R3-P1d: @property methods in Python have bodies with calls that need edges.
		if len(sym.Calls) > 0 {
			b.extractCallEdges(ctx, state, sym)
		}
		b.extractReturnTypeEdges(state, sym)
		b.extractParameterEdges(state, sym)

	case ast.SymbolKindStruct, ast.SymbolKindClass:
		// Extract implements edges if metadata available
		b.extractImplementsEdges(state, sym)
		// Extract embeds edges from fields
		b.extractEmbedsEdges(state, sym)

	case ast.SymbolKindInterface:
		// Phase 18: Interfaces can embed other interfaces — create EMBEDS edges
		// for transitive method set resolution (e.g., ReadWriter embeds Reader + Writer).
		b.extractInterfaceEmbedsEdges(state, sym)
	}

	// IT-03a A-3: Extract decorator argument edges for any symbol kind
	b.extractDecoratorArgEdges(state, sym)

	// IT-03a C-2: Extract type argument reference edges
	b.extractTypeArgEdges(state, sym)

	// IT-03a C-3: Extract type narrowing reference edges
	b.extractTypeNarrowingEdges(state, sym)
}

// extractReceiverEdge creates a RECEIVES edge from method to receiver type.
func (b *Builder) extractReceiverEdge(state *buildState, sym *ast.Symbol) {
	// Find receiver type symbol
	receiverName := strings.TrimPrefix(sym.Receiver, "*")
	targets := b.resolveSymbolByName(state, receiverName, sym.FilePath)

	if len(targets) == 0 {
		// Create placeholder
		targetID := b.getOrCreatePlaceholder(state, sym.Package, receiverName)
		targets = []string{targetID}
	}

	for _, targetID := range targets {
		err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReceives, sym.Location())
		if err != nil {
			state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
				FromID:   sym.ID,
				ToID:     targetID,
				EdgeType: EdgeTypeReceives,
				Err:      err,
			})
			continue
		}
		state.result.Stats.EdgesCreated++
	}

	if len(targets) > 1 {
		state.result.Stats.AmbiguousResolves++
	}
}

// extractReturnTypeEdges creates RETURNS edges from function to return types.
func (b *Builder) extractReturnTypeEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || sym.Metadata.ReturnType == "" {
		return
	}

	// Parse return type (simplified - just use the type name)
	returnType := extractTypeName(sym.Metadata.ReturnType)
	if returnType == "" {
		return
	}

	targets := b.resolveSymbolByName(state, returnType, sym.FilePath)
	if len(targets) == 0 {
		targetID := b.getOrCreatePlaceholder(state, "", returnType)
		targets = []string{targetID}
	}

	for _, targetID := range targets {
		err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReturns, sym.Location())
		if err != nil {
			state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
				FromID:   sym.ID,
				ToID:     targetID,
				EdgeType: EdgeTypeReturns,
				Err:      err,
			})
			continue
		}
		state.result.Stats.EdgesCreated++
	}

	if len(targets) > 1 {
		state.result.Stats.AmbiguousResolves++
	}
}

// extractParameterEdges creates PARAMETERS edges from function to parameter types.
func (b *Builder) extractParameterEdges(state *buildState, sym *ast.Symbol) {
	// Extract parameter types from signature (simplified)
	// Full implementation would parse the signature properly
	if sym.Signature == "" {
		return
	}

	// For now, skip complex signature parsing
	// This would be enhanced in a future iteration
}

// extractImplementsEdges creates IMPLEMENTS edges from type to interfaces.
func (b *Builder) extractImplementsEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || len(sym.Metadata.Implements) == 0 {
		return
	}

	for _, ifaceName := range sym.Metadata.Implements {
		targets := b.resolveSymbolByName(state, ifaceName, sym.FilePath)
		if len(targets) == 0 {
			targetID := b.getOrCreatePlaceholder(state, "", ifaceName)
			targets = []string{targetID}
		}

		for _, targetID := range targets {
			// Validate edge type - target should be interface
			if !b.validateEdgeType(state, sym.ID, targetID, EdgeTypeImplements) {
				continue
			}

			err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeImplements, sym.Location())
			if err != nil {
				state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
					FromID:   sym.ID,
					ToID:     targetID,
					EdgeType: EdgeTypeImplements,
					Err:      err,
				})
				continue
			}
			state.result.Stats.EdgesCreated++
		}

		if len(targets) > 1 {
			state.result.Stats.AmbiguousResolves++
		}
	}
}

// extractEmbedsEdges creates EMBEDS edges from struct to embedded types.
func (b *Builder) extractEmbedsEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || sym.Metadata.Extends == "" {
		return
	}

	// Extends represents embedding in Go context
	embeddedName := sym.Metadata.Extends
	targets := b.resolveSymbolByName(state, embeddedName, sym.FilePath)
	if len(targets) == 0 {
		targetID := b.getOrCreatePlaceholder(state, "", embeddedName)
		targets = []string{targetID}
	}

	for _, targetID := range targets {
		err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeEmbeds, sym.Location())
		if err != nil {
			state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
				FromID:   sym.ID,
				ToID:     targetID,
				EdgeType: EdgeTypeEmbeds,
				Err:      err,
			})
			continue
		}
		state.result.Stats.EdgesCreated++
	}

	if len(targets) > 1 {
		state.result.Stats.AmbiguousResolves++
	}

	// IT-03a Phase 16 G-1: Additional embeds stored in Implements for Go structs.
	// For Go structs, Metadata.Implements holds additional embedded types (not interface
	// implementations). We create EMBEDS edges here. extractImplementsEdges also reads
	// Metadata.Implements, but validateEdgeType prevents IMPLEMENTS edges when the target
	// is not a SymbolKindInterface. When the target IS an interface (e.g., embedding io.Writer),
	// both EMBEDS and IMPLEMENTS edges are created, which is semantically correct — embedding
	// an interface in Go satisfies that interface.
	if sym.Kind == ast.SymbolKindStruct && len(sym.Metadata.Implements) > 0 {
		for _, additionalEmbed := range sym.Metadata.Implements {
			addlTargets := b.resolveSymbolByName(state, additionalEmbed, sym.FilePath)
			if len(addlTargets) == 0 {
				targetID := b.getOrCreatePlaceholder(state, "", additionalEmbed)
				addlTargets = []string{targetID}
			}
			for _, targetID := range addlTargets {
				err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeEmbeds, sym.Location())
				if err != nil {
					state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
						FromID:   sym.ID,
						ToID:     targetID,
						EdgeType: EdgeTypeEmbeds,
						Err:      err,
					})
					continue
				}
				state.result.Stats.EdgesCreated++
			}
		}
	}
}

// extractInterfaceEmbedsEdges creates EMBEDS edges from an interface to its embedded interfaces.
//
// Description:
//
//	In Go, interfaces can embed other interfaces (e.g., ReadWriter embeds Reader and Writer).
//	The parser stores embedded interface names in Metadata.Extends (first embed) and
//	Metadata.Implements (additional embeds), mirroring the struct embedding storage pattern.
//	This function creates EMBEDS edges so that resolveInterfaceMethodSets can walk them
//	to build transitive method sets.
//
// Inputs:
//   - state: Build state containing graph and resolution data.
//   - sym: An interface symbol that may embed other interfaces.
//
// Outputs:
//   - None. Modifies state.graph by adding EMBEDS edges.
//
// Thread Safety: Not safe for concurrent use on the same buildState.
func (b *Builder) extractInterfaceEmbedsEdges(state *buildState, sym *ast.Symbol) {
	// CR-20-9: Defense in depth — validate precondition even though caller already checks.
	if sym.Kind != ast.SymbolKindInterface {
		return
	}
	if sym.Metadata == nil || sym.Metadata.Extends == "" {
		return
	}

	// First embedded interface (stored in Extends)
	embeddedName := sym.Metadata.Extends
	targets := b.resolveSymbolByName(state, embeddedName, sym.FilePath)
	if len(targets) == 0 {
		targetID := b.getOrCreatePlaceholder(state, "", embeddedName)
		targets = []string{targetID}
	}

	for _, targetID := range targets {
		err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeEmbeds, sym.Location())
		if err != nil {
			state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
				FromID:   sym.ID,
				ToID:     targetID,
				EdgeType: EdgeTypeEmbeds,
				Err:      err,
			})
			continue
		}
		state.result.Stats.EdgesCreated++
	}

	// Additional embedded interfaces (stored in Implements for Go interfaces)
	if len(sym.Metadata.Implements) > 0 {
		for _, additionalEmbed := range sym.Metadata.Implements {
			addlTargets := b.resolveSymbolByName(state, additionalEmbed, sym.FilePath)
			if len(addlTargets) == 0 {
				targetID := b.getOrCreatePlaceholder(state, "", additionalEmbed)
				addlTargets = []string{targetID}
			}
			for _, targetID := range addlTargets {
				err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeEmbeds, sym.Location())
				if err != nil {
					state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
						FromID:   sym.ID,
						ToID:     targetID,
						EdgeType: EdgeTypeEmbeds,
						Err:      err,
					})
					continue
				}
				state.result.Stats.EdgesCreated++
			}
		}
	}
}

// extractDecoratorArgEdges creates REFERENCES edges from a decorated symbol to its decorator arguments.
//
// Description:
//
//	When a decorator has arguments that are identifiers (references to other symbols),
//	this creates EdgeTypeReferences edges from the decorated symbol to those arguments.
//	Example: @UseInterceptors(LoggingInterceptor) creates a REFERENCES edge from
//	the decorated class to LoggingInterceptor.
//
// IT-03a A-3: Enables graph-based discovery of decorator argument relationships.
func (b *Builder) extractDecoratorArgEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || len(sym.Metadata.DecoratorArgs) == 0 {
		return
	}

	for _, argNames := range sym.Metadata.DecoratorArgs {
		for _, argName := range argNames {
			targets := b.resolveSymbolByName(state, argName, sym.FilePath)
			if len(targets) == 0 {
				// Only create placeholder for reasonable identifier names
				if len(argName) > 0 && argName[0] >= 'A' && argName[0] <= 'Z' {
					targetID := b.getOrCreatePlaceholder(state, "", argName)
					targets = []string{targetID}
				} else {
					continue
				}
			}

			for _, targetID := range targets {
				err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReferences, sym.Location())
				if err != nil {
					state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
						FromID:   sym.ID,
						ToID:     targetID,
						EdgeType: EdgeTypeReferences,
						Err:      err,
					})
					continue
				}
				state.result.Stats.EdgesCreated++
			}

			if len(targets) > 1 {
				state.result.Stats.AmbiguousResolves++
			}
		}
	}
}

// extractTypeArgEdges creates REFERENCES edges from a symbol to its type argument types.
//
// Description:
//
//	When a symbol has TypeArguments in its metadata (e.g., Promise<User> → User),
//	this creates EdgeTypeReferences edges from the symbol to the referenced types.
//
// IT-03a C-2: Enables graph-based discovery of generic type relationships.
func (b *Builder) extractTypeArgEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || len(sym.Metadata.TypeArguments) == 0 {
		return
	}

	for _, typeArg := range sym.Metadata.TypeArguments {
		targets := b.resolveSymbolByName(state, typeArg, sym.FilePath)
		if len(targets) == 0 {
			continue // Don't create placeholders for type args
		}
		for _, targetID := range targets {
			err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReferences, sym.Location())
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
					FromID:   sym.ID,
					ToID:     targetID,
					EdgeType: EdgeTypeReferences,
					Err:      err,
				})
			} else if err == nil {
				state.result.Stats.EdgesCreated++
			}
		}
	}
}

// extractTypeNarrowingEdges creates REFERENCES edges from a symbol to types used in instanceof checks.
//
// IT-03a C-3: Enables graph-based discovery of type narrowing relationships.
func (b *Builder) extractTypeNarrowingEdges(state *buildState, sym *ast.Symbol) {
	if sym.Metadata == nil || len(sym.Metadata.TypeNarrowings) == 0 {
		return
	}

	for _, typeName := range sym.Metadata.TypeNarrowings {
		targets := b.resolveSymbolByName(state, typeName, sym.FilePath)
		if len(targets) == 0 {
			continue
		}
		for _, targetID := range targets {
			err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReferences, sym.Location())
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
					FromID:   sym.ID,
					ToID:     targetID,
					EdgeType: EdgeTypeReferences,
					Err:      err,
				})
			} else if err == nil {
				state.result.Stats.EdgesCreated++
			}
		}
	}
}

// extractCallEdges creates CALLS edges from a function/method to its callees.
//
// Description:
//
//	For each call site in the symbol's Calls slice, this function attempts
//	to resolve the call target to a symbol ID and creates an EdgeTypeCalls
//	edge. Unresolved calls create placeholder nodes with SymbolKindExternal.
//
// Inputs:
//   - ctx: Context for tracing and cancellation.
//   - state: The build state containing symbol indexes.
//   - sym: The function or method symbol containing call sites.
//
// Outputs:
//   - None. Edges are added to state.graph, errors to state.result.EdgeErrors.
//
// Thread Safety:
//
//	This method modifies state.graph and state.result. Not safe for concurrent
//	use on the same buildState, but the builder serializes calls appropriately.
//
// See GR-41: Call Edge Extraction for find_callers/find_callees.
func (b *Builder) extractCallEdges(ctx context.Context, state *buildState, sym *ast.Symbol) {
	if sym == nil || len(sym.Calls) == 0 {
		return
	}

	// GR-41: OTel tracing for observability
	_, span := tracer.Start(ctx, "GraphBuilder.extractCallEdges",
		trace.WithAttributes(
			attribute.String("symbol.id", sym.ID),
			attribute.String("symbol.name", sym.Name),
			attribute.Int("call_sites.count", len(sym.Calls)),
		),
	)
	defer span.End()

	callsResolved := 0
	callsUnresolved := 0

	for _, call := range sym.Calls {
		// Validate call target
		if call.Target == "" {
			continue
		}

		// Try to resolve the target to a symbol ID
		targetID := b.resolveCallTarget(state, call, sym)
		if targetID == "" {
			// Create placeholder for unresolved external call
			targetID = b.getOrCreatePlaceholder(state, "", call.Target)
			callsUnresolved++
		} else {
			callsResolved++
		}

		// Skip self-referential calls (recursive calls are valid but don't need edges)
		if targetID == sym.ID {
			slog.Debug("GR-41: Skipping self-referential call",
				slog.String("symbol", sym.Name),
				slog.String("target", call.Target),
			)
			continue
		}

		// Validate edge type
		if !b.validateEdgeType(state, sym.ID, targetID, EdgeTypeCalls) {
			continue
		}

		// Create the edge
		err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeCalls, call.Location)
		if err != nil {
			// Check if it's a duplicate edge error (not fatal)
			if !strings.Contains(err.Error(), "already exists") {
				state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
					FromID:   sym.ID,
					ToID:     targetID,
					EdgeType: EdgeTypeCalls,
					Err:      err,
				})
			}
			continue
		}
		state.result.Stats.EdgesCreated++
	}

	// IT-03a C-1: Create REFERENCES edges for callback/HOF arguments
	for _, call := range sym.Calls {
		for _, argName := range call.FunctionArgs {
			targets := b.resolveSymbolByName(state, argName, sym.FilePath)
			if len(targets) == 0 {
				continue // Don't create placeholders for callback args
			}
			for _, targetID := range targets {
				if targetID == sym.ID {
					continue
				}
				err := state.graph.AddEdge(sym.ID, targetID, EdgeTypeReferences, call.Location)
				if err != nil && !strings.Contains(err.Error(), "already exists") {
					state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
						FromID:   sym.ID,
						ToID:     targetID,
						EdgeType: EdgeTypeReferences,
						Err:      err,
					})
				} else if err == nil {
					state.result.Stats.EdgesCreated++
				}
			}
		}
	}

	// Track call edge stats for observability
	state.result.Stats.CallEdgesResolved += callsResolved
	state.result.Stats.CallEdgesUnresolved += callsUnresolved

	// GR-41: Record span attributes
	span.SetAttributes(
		attribute.Int("calls.resolved", callsResolved),
		attribute.Int("calls.unresolved", callsUnresolved),
	)
}

// resolveCallTarget attempts to find the symbol ID for a call target.
//
// Description:
//
//	Uses multiple resolution strategies to find the symbol being called:
//	1. Direct name match in same package
//	2. Qualified name (package.Function) using import mappings
//	3. Method call resolution using receiver type
//
// Inputs:
//   - state: The build state containing symbol indexes.
//   - call: The call site to resolve.
//   - caller: The calling function/method (for context).
//
// Outputs:
//   - string: The resolved symbol ID, or empty string if unresolved.
//
// Thread Safety: This function is safe for concurrent use.
func (b *Builder) resolveCallTarget(state *buildState, call ast.CallSite, caller *ast.Symbol) string {
	target := call.Target

	// Strategy 1: Direct name match in same package
	// For simple calls like "DoWork()"
	if !strings.Contains(target, ".") && !call.IsMethod {
		candidates := b.resolveSymbolByName(state, target, caller.FilePath)
		// R3-P2b-Self: Filter out the caller itself to prevent self-referential
		// false matches. When DataFrame.merge calls bare merge(), same-file priority
		// returns DataFrame.merge first, which then gets skipped as self-referential
		// at line 879 and resolution gives up. By filtering here, we fall through
		// to cross-file candidates.
		candidates = filterOutID(candidates, caller.ID)

		// R3-P2b-Self: If same-file candidates were all self-referential, try all files.
		if len(candidates) == 0 {
			allCandidates := b.resolveAllSymbolsByName(state, target)
			candidates = filterOutID(allCandidates, caller.ID)
		}

		// R3-P2b-ImportMap: Try import-aware resolution first to disambiguate
		// among cross-file candidates.
		if len(candidates) > 0 {
			if resolved := b.resolveViaImportMap(state, target, caller.FilePath, candidates); resolved != "" {
				return resolved
			}
			// Prefer functions/methods, not types
			for _, id := range candidates {
				if sym, ok := state.symbolsByID[id]; ok {
					if sym.Kind == ast.SymbolKindFunction || sym.Kind == ast.SymbolKindMethod {
						return id
					}
				}
			}
			// Fall back to first match
			return candidates[0]
		}

		// R3-P2b-ImportMap: Even with no candidates, try import map
		// (for aliased imports where the local name doesn't match any symbol name).
		if resolved := b.resolveViaImportMap(state, target, caller.FilePath, nil); resolved != "" {
			return resolved
		}
	}

	// Strategy 2: Qualified name (package.Function)
	// For calls like "config.Load()" or "http.Get()"
	if strings.Contains(target, ".") && !call.IsMethod {
		parts := strings.SplitN(target, ".", 2)
		if len(parts) == 2 {
			funcName := parts[1]
			candidates := b.resolveSymbolByName(state, funcName, caller.FilePath)
			if len(candidates) > 0 {
				return candidates[0]
			}
		}
	}

	// Strategy 3: Method call using receiver
	// For calls like "obj.Method()" where call.Receiver = "obj", call.Target = "Method"
	if call.IsMethod && call.Receiver != "" {
		candidates := b.resolveSymbolByName(state, target, caller.FilePath)

		// Sub-strategy 3a: this/self receiver → resolve to caller's owning class
		if call.Receiver == "this" || call.Receiver == "self" {
			if resolved := b.resolveThisSelfCall(state, candidates, caller); resolved != "" {
				return resolved
			}
		}

		// Sub-strategy 3b: Go-style receiver matching (case-insensitive)
		// Handles: txn.Get() → Txn.Get, ctx.Done() → Context.Done
		if call.Receiver != "this" && call.Receiver != "self" {
			if resolved := b.resolveReceiverCaseInsensitive(state, candidates, call.Receiver); resolved != "" {
				return resolved
			}

			// Sub-strategy 3b2: Receiver matching failed on same-file candidates.
			// Try ALL candidates across all files. This handles cross-file method
			// calls like Application.handle calling router.handle() where Router.handle
			// is in a different file. resolveSymbolByName prefers same-file matches,
			// which may not include the correct receiver-matched target.
			allCandidates := b.resolveAllSymbolsByName(state, target)
			if len(allCandidates) > len(candidates) {
				if resolved := b.resolveReceiverCaseInsensitive(state, allCandidates, call.Receiver); resolved != "" {
					return resolved
				}
			}
		}

		// Sub-strategy 3c: Fallback — first method/property match (original behavior)
		// R3-P1d: Include Property symbols so self.some_property resolves correctly.
		for _, id := range candidates {
			if sym, ok := state.symbolsByID[id]; ok {
				if sym.Kind == ast.SymbolKindMethod || sym.Kind == ast.SymbolKindProperty {
					return id
				}
			}
		}
	}

	// Unresolved - caller will create placeholder
	return ""
}

// resolveThisSelfCall resolves method calls on this/self to the caller's owning class.
//
// Description:
//
//	When a method calls this.foo() or self.foo(), the target should resolve to
//	a method on the same class (or a parent class via inheritance). This function
//	finds the caller's owning class and matches the target method.
//
// Inputs:
//
//	state - Build state with symbol indexes and parent maps.
//	candidates - Symbol IDs that match the target method name.
//	caller - The calling function/method symbol.
//
// Outputs:
//
//	string - Resolved symbol ID, or empty if no match found.
func (b *Builder) resolveThisSelfCall(state *buildState, candidates []string, caller *ast.Symbol) string {
	// Find the caller's owning class
	ownerClassName := b.findOwnerClassName(state, caller)
	if ownerClassName == "" {
		return ""
	}

	// Try to find a candidate method that belongs to this class or a parent class
	classChain := b.buildInheritanceChain(state, ownerClassName)

	for _, className := range classChain {
		for _, id := range candidates {
			sym, ok := state.symbolsByID[id]
			if !ok {
				continue
			}
			// R3-P1d: Include Property symbols (Python @property, TS get/set accessors).
			if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction && sym.Kind != ast.SymbolKindProperty {
				continue
			}

			// Check 1: sym.Receiver matches the class name (Go, JS)
			if sym.Receiver == className {
				return id
			}

			// Check 2: sym is a child of the class (Python, TS — Receiver often empty)
			parentID, hasParent := state.symbolParent[id]
			if hasParent {
				if parentSym, ok := state.symbolsByID[parentID]; ok {
					if parentSym.Name == className {
						return id
					}
				}
			}
		}
	}

	return ""
}

// resolveReceiverCaseInsensitive matches a call receiver to a method's declared receiver type
// using case-insensitive comparison.
//
// Description:
//
//	In Go, variable names are conventionally lowercase abbreviations of their types:
//	txn → Txn, ctx → Context, r → Router. This function exploits that convention
//	by doing case-insensitive matching between call.Receiver and sym.Receiver.
//
// Inputs:
//
//	state - Build state with symbol indexes.
//	candidates - Symbol IDs that match the target method name.
//	callReceiver - The receiver expression from the call site (e.g., "txn").
//
// Outputs:
//
//	string - Resolved symbol ID, or empty if no match found.
func (b *Builder) resolveReceiverCaseInsensitive(state *buildState, candidates []string, callReceiver string) string {
	for _, id := range candidates {
		sym, ok := state.symbolsByID[id]
		if !ok {
			continue
		}
		if sym.Kind != ast.SymbolKindMethod {
			continue
		}
		if sym.Receiver != "" && strings.EqualFold(callReceiver, sym.Receiver) {
			return id
		}
	}
	return ""
}

// findOwnerClassName finds the class/struct name that owns the given method.
//
// Description:
//
//	Uses multiple strategies to find the owning class:
//	1. sym.Receiver field (Go, JS — directly set by parser)
//	2. symbolParent reverse index (Python, TS — method is a child of class)
//
// Inputs:
//
//	state - Build state with symbol and parent indexes.
//	method - The method symbol to find the owner for.
//
// Outputs:
//
//	string - The owning class/struct name, or empty if not found.
func (b *Builder) findOwnerClassName(state *buildState, method *ast.Symbol) string {
	// Strategy 1: Receiver field (Go, JS)
	if method.Receiver != "" {
		return method.Receiver
	}

	// Strategy 2: Parent symbol lookup (Python, TS)
	parentID, ok := state.symbolParent[method.ID]
	if ok {
		if parent, exists := state.symbolsByID[parentID]; exists {
			if parent.Kind == ast.SymbolKindClass || parent.Kind == ast.SymbolKindStruct {
				return parent.Name
			}
		}
	}

	return ""
}

// buildInheritanceChain builds the chain of class names from the given class up through its parents.
//
// Description:
//
//	Walks the classExtends map to build [className, parentName, grandparentName, ...].
//	Stops at a maximum depth of 10 to prevent infinite loops from circular inheritance.
//
// Inputs:
//
//	state - Build state with classExtends map.
//	className - Starting class name.
//
// Outputs:
//
//	[]string - Class names from child to root, including className itself.
func (b *Builder) buildInheritanceChain(state *buildState, className string) []string {
	chain := []string{className}
	current := className
	for i := 0; i < 10; i++ {
		parent, ok := state.classExtends[current]
		if !ok || parent == "" {
			break
		}
		chain = append(chain, parent)
		current = parent
	}
	return chain
}

// resolveSymbolByName finds symbols matching the given name.
// Prefers symbols in the same file, then same package.
func (b *Builder) resolveSymbolByName(state *buildState, name string, currentFile string) []string {
	candidates := state.symbolsByName[name]
	if len(candidates) == 0 {
		return nil
	}

	// Prefer same file
	var sameFile []string
	var samePackage []string
	var other []string

	for _, sym := range candidates {
		if sym.FilePath == currentFile {
			sameFile = append(sameFile, sym.ID)
		} else if b.samePackage(sym.FilePath, currentFile) {
			samePackage = append(samePackage, sym.ID)
		} else {
			other = append(other, sym.ID)
		}
	}

	// Return in priority order
	if len(sameFile) > 0 {
		return sameFile
	}
	if len(samePackage) > 0 {
		return samePackage
	}
	return other
}

// resolveAllSymbolsByName returns ALL symbol IDs matching a name across all files.
//
// Description:
//
//	Unlike resolveSymbolByName which prefers same-file/same-package matches,
//	this returns every symbol with the given name. Used as a fallback when
//	receiver-based disambiguation fails on the filtered candidate set—the
//	correct target may be in a different file.
//
// Inputs:
//
//	state - Build state with symbol indexes.
//	name - The symbol name to look up.
//
// Outputs:
//
//	[]string - All symbol IDs matching the name, or nil if none found.
func (b *Builder) resolveAllSymbolsByName(state *buildState, name string) []string {
	candidates := state.symbolsByName[name]
	if len(candidates) == 0 {
		return nil
	}
	ids := make([]string, 0, len(candidates))
	for _, sym := range candidates {
		ids = append(ids, sym.ID)
	}
	return ids
}

// samePackage checks if two files are in the same package.
// This is a simple heuristic based on directory.
func (b *Builder) samePackage(file1, file2 string) bool {
	dir1 := extractDir(file1)
	dir2 := extractDir(file2)
	return dir1 == dir2
}

// extractDir extracts the directory from a file path.
func extractDir(path string) string {
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return ""
	}
	return path[:lastSlash]
}

// filterOutID returns a copy of ids with the specified id removed.
//
// Description:
//
//	R3-P2b-Self: Used to remove the caller's own ID from resolution candidates,
//	preventing self-referential false matches where DataFrame.merge resolves
//	bare merge() to itself.
//
// Thread Safety: This function is safe for concurrent use.
func filterOutID(ids []string, exclude string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != exclude {
			result = append(result, id)
		}
	}
	return result
}

// buildImportNameMap builds the import name lookup table from fileImports.
//
// Description:
//
//	R3-P2b: Processes all file imports collected during collectPhase and builds
//	a fast lookup map: importNameMap[filePath][localName] → importEntry.
//	Handles aliased imports (e.g., "merge as pd_merge"), skips wildcards
//	and bare module imports (no Names).
//
// Thread Safety: Must be called before edge extraction (single-threaded phase).
func (b *Builder) buildImportNameMap(state *buildState) {
	entries := 0
	for filePath, imports := range state.fileImports {
		for _, imp := range imports {
			if imp.IsWildcard || len(imp.Names) == 0 {
				continue
			}

			if state.importNameMap[filePath] == nil {
				state.importNameMap[filePath] = make(map[string]importEntry)
			}

			for _, name := range imp.Names {
				localName, originalName := parseAliasedName(name)
				state.importNameMap[filePath][localName] = importEntry{
					ModulePath:   imp.Path,
					OriginalName: originalName,
				}
				entries++
			}
		}
	}

	if entries > 0 {
		slog.Debug("R3-P2b: import name map built",
			slog.Int("entries", entries),
			slog.Int("files", len(state.importNameMap)),
		)
	}
}

// resolveViaImportMap attempts to resolve a call target using import information.
//
// Description:
//
//	R3-P2b: When a file imports "merge" from "pandas.core.reshape.merge", and code
//	calls bare merge(), this function finds the correct cross-file target by matching
//	the candidate's file path against the import's module path.
//
// Inputs:
//
//	state - Build state with importNameMap populated.
//	target - The call target name (e.g., "merge").
//	callerFile - The calling file's path.
//	candidates - Pre-resolved candidate IDs (self-filtered).
//
// Outputs:
//
//	string - Resolved symbol ID, or empty string if no import match found.
//
// Thread Safety: This function is safe for concurrent use.
func (b *Builder) resolveViaImportMap(state *buildState, target string, callerFile string, candidates []string) string {
	fileMap := state.importNameMap[callerFile]
	if fileMap == nil {
		return ""
	}

	entry, ok := fileMap[target]
	if !ok {
		return ""
	}

	// Look through ALL symbols named originalName (not just candidates,
	// which may be filtered to same-file).
	allCandidates := b.resolveAllSymbolsByName(state, entry.OriginalName)
	for _, id := range allCandidates {
		sym := state.symbolsByID[id]
		if sym != nil && matchesImportPath(sym.FilePath, entry.ModulePath) {
			slog.Debug("R3-P2b: import-aware resolution succeeded",
				slog.String("target", target),
				slog.String("import_path", entry.ModulePath),
				slog.String("original_name", entry.OriginalName),
				slog.String("resolved_id", id),
			)
			return id
		}
	}

	return ""
}

// matchesImportPath checks if a symbol's file path corresponds to an import module path.
//
// Description:
//
//	R3-P2b: Converts a Python module path like "pandas.core.reshape.merge" to a file
//	path fragment "pandas/core/reshape/merge" and checks if the symbol's file path
//	ends with it. Uses HasSuffix (not Contains) to prevent false positives from
//	prefix pollution (e.g., "my_pandas/core/reshape/merge.py").
//
// Thread Safety: This function is safe for concurrent use.
func matchesImportPath(filePath string, importPath string) bool {
	pathFragment := strings.ReplaceAll(importPath, ".", "/")
	// Handle both "merge.py" and "merge/__init__.py" (Python package)
	normalized := strings.TrimSuffix(filePath, ".py")
	normalized = strings.TrimSuffix(normalized, "/__init__")
	// Must match at path boundary: either the entire path, or preceded by "/"
	if normalized == pathFragment {
		return true
	}
	return strings.HasSuffix(normalized, "/"+pathFragment)
}

// parseAliasedName splits a Python import name that may contain "as" alias.
//
// Description:
//
//	R3-P2b: For "concat as pd_concat", returns ("pd_concat", "concat").
//	For "merge" (no alias), returns ("merge", "merge").
//
// Thread Safety: This function is safe for concurrent use.
func parseAliasedName(name string) (localName, originalName string) {
	parts := strings.SplitN(name, " as ", 2)
	originalName = strings.TrimSpace(parts[0])
	localName = originalName
	if len(parts) == 2 {
		localName = strings.TrimSpace(parts[1])
	}
	return localName, originalName
}

// extractTypeName extracts a simple type name from a type expression.
// For example: "*User" -> "User", "[]string" -> "string", "map[string]User" -> "User"
func extractTypeName(typeExpr string) string {
	// Remove pointer prefix
	typeExpr = strings.TrimPrefix(typeExpr, "*")

	// Remove slice prefix
	typeExpr = strings.TrimPrefix(typeExpr, "[]")

	// Handle map - extract value type
	if strings.HasPrefix(typeExpr, "map[") {
		closeBracket := strings.Index(typeExpr, "]")
		if closeBracket > 0 && closeBracket < len(typeExpr)-1 {
			typeExpr = typeExpr[closeBracket+1:]
		}
	}

	// Remove channel prefix
	typeExpr = strings.TrimPrefix(typeExpr, "chan ")
	typeExpr = strings.TrimPrefix(typeExpr, "<-chan ")
	typeExpr = strings.TrimPrefix(typeExpr, "chan<- ")

	// Remove any remaining pointer
	typeExpr = strings.TrimPrefix(typeExpr, "*")

	// Extract just the type name (before any generic brackets)
	bracketIdx := strings.Index(typeExpr, "[")
	if bracketIdx > 0 {
		typeExpr = typeExpr[:bracketIdx]
	}

	// Skip built-in types
	builtins := map[string]bool{
		"string": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"bool": true, "byte": true, "rune": true, "error": true, "any": true,
	}

	if builtins[typeExpr] {
		return ""
	}

	return typeExpr
}

// getOrCreatePlaceholder returns an existing placeholder or creates a new one.
func (b *Builder) getOrCreatePlaceholder(state *buildState, pkg, name string) string {
	var id string
	if pkg != "" {
		id = fmt.Sprintf("external:%s:%s", pkg, name)
	} else {
		id = fmt.Sprintf("external::%s", name)
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if node, exists := state.placeholders[id]; exists {
		return node.ID
	}

	// Create placeholder symbol
	placeholder := &ast.Symbol{
		ID:       id,
		Name:     name,
		Kind:     ast.SymbolKindExternal,
		Package:  pkg,
		Language: "external",
	}

	node, err := state.graph.AddNode(placeholder)
	if err != nil {
		// Node might already exist (race condition) - just return the ID
		return id
	}

	state.placeholders[id] = node
	state.result.Stats.PlaceholderNodes++
	return id
}

// validateEdgeType checks if an edge type is valid for the given nodes.
func (b *Builder) validateEdgeType(state *buildState, fromID, toID string, edgeType EdgeType) bool {
	fromSym := state.symbolsByID[fromID]
	toSym := state.symbolsByID[toID]

	// If we don't have symbol info, allow the edge
	if fromSym == nil || toSym == nil {
		return true
	}

	switch edgeType {
	case EdgeTypeCalls:
		// R3-P1b: The TO side uses isCallTarget which additionally allows Class/Struct
		// (constructor calls like DataFrameFormatter(), new Router()).
		return isCallable(fromSym.Kind) && isCallTarget(toSym.Kind)
	case EdgeTypeImplements:
		return toSym.Kind == ast.SymbolKindInterface
	case EdgeTypeEmbeds:
		return fromSym.Kind == ast.SymbolKindStruct || fromSym.Kind == ast.SymbolKindClass
	default:
		return true
	}
}

// isCallable returns true if the symbol kind can be the source (FROM) of a call edge.
// R3-P1d: Property methods in Python have bodies that make calls.
func isCallable(kind ast.SymbolKind) bool {
	return kind == ast.SymbolKindFunction ||
		kind == ast.SymbolKindMethod ||
		kind == ast.SymbolKindExternal ||
		kind == ast.SymbolKindProperty
}

// isCallTarget returns true if the symbol kind can be the target (TO) of a call edge.
// This is a superset of isCallable: it additionally allows Class and Struct symbols
// because constructor calls (e.g., DataFrameFormatter(), new Router()) target class/struct symbols.
// R3-P1b: Fixes silently dropped constructor call edges.
func isCallTarget(kind ast.SymbolKind) bool {
	return isCallable(kind) ||
		kind == ast.SymbolKindClass ||
		kind == ast.SymbolKindStruct
}

// validateParseResult checks if a ParseResult is valid for building.
// Note: Nil symbols are allowed and will be skipped during processing.
func (b *Builder) validateParseResult(r *ast.ParseResult) error {
	if r == nil {
		return fmt.Errorf("nil ParseResult")
	}

	if r.FilePath == "" {
		return fmt.Errorf("empty FilePath")
	}

	// Check for path traversal
	if strings.Contains(r.FilePath, "..") {
		return fmt.Errorf("FilePath contains path traversal")
	}

	// Validate non-nil symbols only
	// Nil symbols are skipped, not treated as errors
	for i, sym := range r.Symbols {
		if sym == nil {
			continue // Skip nil symbols
		}
		if err := sym.Validate(); err != nil {
			return fmt.Errorf("symbol[%d] (%s): %w", i, sym.Name, err)
		}
	}

	return nil
}

// reportProgress calls the progress callback if configured.
func (b *Builder) reportProgress(state *buildState, phase ProgressPhase, total, processed int) {
	if b.options.ProgressCallback == nil {
		return
	}

	b.options.ProgressCallback(BuildProgress{
		Phase:          phase,
		FilesTotal:     total,
		FilesProcessed: processed,
		NodesCreated:   state.result.Stats.NodesCreated,
		EdgesCreated:   state.result.Stats.EdgesCreated,
	})
}

// === GR-40 FIX (C-3): Cross-File Method Association ===

// associateMethodsWithTypesCrossFile associates methods with their receiver types across all files.
//
// Description:
//
//	In Go, methods can be defined in different files than their receiver types.
//	The parser's associateMethodsWithTypes() only works within a single file.
//	This function operates on the complete symbol set to handle cross-file cases.
//
// Inputs:
//
//	ctx - Context for tracing
//	state - Build state with all symbols from all files
//
// Side Effects:
//
//	Modifies Symbol.Metadata.Methods for types (structs and type aliases)
//
// Thread Safety:
//
//	Not safe for concurrent use on the same buildState. The builder serializes calls.
func (b *Builder) associateMethodsWithTypesCrossFile(ctx context.Context, state *buildState) {
	ctx, span := tracer.Start(ctx, "GraphBuilder.associateMethodsWithTypesCrossFile")
	defer span.End()

	if state == nil || len(state.symbolsByID) == 0 {
		span.AddEvent("no_symbols")
		return
	}

	// Collect all Go methods by receiver type name
	// methodsByReceiverType[receiverTypeName] = []MethodSignature
	methodsByReceiverType := make(map[string][]ast.MethodSignature)
	methodCount := 0
	skippedNoReceiver := 0

	for _, sym := range state.symbolsByID {
		// L-6: Check context periodically for large codebases
		if methodCount%1000 == 0 {
			if err := ctx.Err(); err != nil {
				span.AddEvent("cancelled", trace.WithAttributes(
					attribute.Int("methods_processed", methodCount),
				))
				return
			}
		}

		if sym.Kind != ast.SymbolKindMethod || sym.Language != "go" {
			continue
		}

		// Extract receiver type name from signature
		// Signature format: "func (r *Type) Name(params) returns" or "func (r Type) Name(params) returns"
		receiverType := extractReceiverTypeFromSignature(sym.Signature)
		if receiverType == "" {
			skippedNoReceiver++
			continue
		}

		// Create method signature from symbol
		sig := ast.MethodSignature{
			Name:         sym.Name,
			Params:       extractParamsFromSignature(sym.Signature),
			Returns:      extractReturnsFromSignature(sym.Signature),
			ReceiverType: receiverType,
		}
		sig.ParamCount = countParamString(sig.Params)
		sig.ReturnCount = countReturnString(sig.Returns)

		methodsByReceiverType[receiverType] = append(methodsByReceiverType[receiverType], sig)
		methodCount++
	}

	span.SetAttributes(
		attribute.Int("methods_collected", methodCount),
		attribute.Int("receiver_types", len(methodsByReceiverType)),
		attribute.Int("skipped_no_receiver", skippedNoReceiver),
	)

	// L-5: Log warning if many methods couldn't be parsed
	if skippedNoReceiver > 0 {
		slog.Debug("methods skipped due to unparseable receiver",
			slog.Int("skipped", skippedNoReceiver),
			slog.Int("collected", methodCount),
		)
	}

	if len(methodsByReceiverType) == 0 {
		span.AddEvent("no_methods_with_receivers")
		return
	}

	// Associate methods with their types (cross-file!)
	typesUpdated := 0
	for _, sym := range state.symbolsByID {
		if sym.Language != "go" {
			continue
		}
		if sym.Kind != ast.SymbolKindStruct && sym.Kind != ast.SymbolKindType {
			continue
		}

		methods, ok := methodsByReceiverType[sym.Name]
		if !ok || len(methods) == 0 {
			continue
		}

		// Initialize metadata if needed
		if sym.Metadata == nil {
			sym.Metadata = &ast.SymbolMetadata{}
		}

		// Append cross-file methods (don't overwrite same-file methods)
		existingNames := make(map[string]bool)
		for _, m := range sym.Metadata.Methods {
			existingNames[m.Name] = true
		}

		for _, m := range methods {
			if !existingNames[m.Name] {
				sym.Metadata.Methods = append(sym.Metadata.Methods, m)
			}
		}
		typesUpdated++
	}

	span.SetAttributes(attribute.Int("types_updated", typesUpdated))

	slog.Debug("cross-file method association complete",
		slog.Int("methods_collected", methodCount),
		slog.Int("receiver_types", len(methodsByReceiverType)),
		slog.Int("types_updated", typesUpdated),
	)
}

// extractReceiverTypeFromSignature extracts the receiver type name from a Go method signature.
// Example: "func (h *Handler) Handle()" returns "Handler"
// Example: "func (s Server) Start()" returns "Server"
func extractReceiverTypeFromSignature(sig string) string {
	if sig == "" || !strings.HasPrefix(sig, "func (") {
		return ""
	}

	// Find the closing paren of the receiver
	parenEnd := strings.Index(sig[6:], ")")
	if parenEnd == -1 {
		return ""
	}

	receiver := sig[6 : 6+parenEnd]
	// receiver is like "h *Handler" or "s Server"

	parts := strings.Fields(receiver)
	if len(parts) < 2 {
		return ""
	}

	typePart := parts[len(parts)-1]
	// Remove * prefix if pointer receiver
	return strings.TrimPrefix(typePart, "*")
}

// extractParamsFromSignature extracts the parameter list from a method signature.
func extractParamsFromSignature(sig string) string {
	if sig == "" {
		return ""
	}

	// Find the method name and params: "func (r Type) Name(params) returns"
	// First, skip past the receiver
	start := strings.Index(sig, ") ")
	if start == -1 {
		return ""
	}

	// Find the opening paren of params
	paramStart := strings.Index(sig[start:], "(")
	if paramStart == -1 {
		return ""
	}
	paramStart += start

	// Find the matching closing paren
	depth := 0
	paramEnd := -1
	for i := paramStart; i < len(sig); i++ {
		switch sig[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				paramEnd = i
				break
			}
		}
		if paramEnd != -1 {
			break
		}
	}

	if paramEnd == -1 {
		return ""
	}

	return sig[paramStart+1 : paramEnd]
}

// extractReturnsFromSignature extracts the return types from a method signature.
func extractReturnsFromSignature(sig string) string {
	if sig == "" {
		return ""
	}

	// Find the last ) which ends the params
	lastParen := strings.LastIndex(sig, ")")
	if lastParen == -1 || lastParen >= len(sig)-1 {
		return ""
	}

	// Everything after is the return type(s)
	returns := strings.TrimSpace(sig[lastParen+1:])

	// Remove outer parens from multi-return: "(int, error)" -> "int, error"
	if strings.HasPrefix(returns, "(") && strings.HasSuffix(returns, ")") {
		returns = returns[1 : len(returns)-1]
	}

	return returns
}

// countParamString counts the number of parameters in a parameter string.
// Example: "ctx context.Context, name string" returns 2
// Example: "" returns 0
func countParamString(params string) int {
	params = strings.TrimSpace(params)
	if params == "" {
		return 0
	}

	// Count commas outside of nested types
	count := 1
	depth := 0
	for _, c := range params {
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}

// countReturnString counts the number of return types in a return string.
// Example: "(int, error)" returns 2
// Example: "error" returns 1
// Example: "" returns 0
func countReturnString(returns string) int {
	returns = strings.TrimSpace(returns)
	if returns == "" {
		return 0
	}

	// Remove outer parens if present
	if strings.HasPrefix(returns, "(") && strings.HasSuffix(returns, ")") {
		returns = returns[1 : len(returns)-1]
	}

	if returns == "" {
		return 0
	}

	// Count commas outside of nested types
	count := 1
	depth := 0
	for _, c := range returns {
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	return count
}

// === GR-40/GR-40a: Implicit Interface Implementation Detection ===

// computeInterfaceImplementations detects implicit interface implementations via method-set matching.
//
// Description:
//
//	Go uses implicit interface satisfaction (no "implements" keyword), and Python's
//	typing.Protocol (PEP 544) works similarly. This function detects when a type
//	implements an interface by comparing method sets:
//	  - An interface defines a set of required method signatures
//	  - A type implements an interface if its method set is a superset of the interface's
//
//	Supported languages:
//	  - Go: All interfaces (GR-40)
//	  - Python: typing.Protocol classes (GR-40a)
//
//	This is called after all symbols are collected and their Metadata.Methods populated.
//
// Inputs:
//   - ctx: Context for cancellation and tracing. Must not be nil.
//   - state: Build state with symbols and graph. Must not be nil.
//
// Outputs:
//   - error: Non-nil only on context cancellation. Edge creation errors are
//     recorded in state.result.EdgeErrors and do not cause function failure.
//
// Algorithm:
//
//	For each interface I (in supported languages):
//	  1. Collect method names from I.Metadata.Methods
//
//	For each type T with methods (T.Metadata.Methods):
//	  2. For each interface I in the SAME language:
//	     - If T's method names are a superset of I's method names
//	     - THEN create EdgeTypeImplements from T → I
//
// Thread Safety:
//
//	This method modifies state.graph and state.result. Not safe for concurrent use
//	on the same buildState, but the builder serializes calls appropriately.
func (b *Builder) computeInterfaceImplementations(ctx context.Context, state *buildState) error {
	// Start OTel span for observability (GR-40 post-implementation review fix C-1, C-2)
	ctx, span := tracer.Start(ctx, "GraphBuilder.computeInterfaceImplementations")
	defer span.End()

	// Check context early
	if err := ctx.Err(); err != nil {
		return err
	}

	// Collect interfaces and their method sets, grouped by language
	// interfacesByLang[language][interfaceID] = {methodName: true}
	interfacesByLang := make(map[string]map[string]map[string]bool)

	for _, sym := range state.symbolsByID {
		if sym.Kind != ast.SymbolKindInterface {
			continue
		}
		// GR-40: Go, GR-40a: Python, IT-03a A-1: TypeScript
		if sym.Language != "go" && sym.Language != "python" && sym.Language != "typescript" {
			continue
		}
		// Phase 18: Don't skip interfaces with no direct methods yet — they may gain
		// methods via embedded/extended interfaces (Go: ReadWriter embeds Reader + Writer,
		// TS: interface ReadWriter extends Reader, Writer {}, Python: class Combined(ReadProto, WriteProto, Protocol)).
		// We resolve embedded methods below via EMBEDS edges, then prune still-empty ones.
		if sym.Metadata == nil {
			// No metadata at all — truly empty, safe to skip
			continue
		}
		// Collect even if Methods is empty — will resolve via EMBEDS edges

		if interfacesByLang[sym.Language] == nil {
			interfacesByLang[sym.Language] = make(map[string]map[string]bool)
		}

		methodSet := make(map[string]bool)
		for _, m := range sym.Metadata.Methods {
			methodSet[m.Name] = true
		}
		interfacesByLang[sym.Language][sym.ID] = methodSet
	}

	// Phase 18: Resolve interface method sets by walking EMBEDS edges transitively.
	// This fills in methods for composed interfaces (e.g., ReadWriter gets Read + Write).
	interfaceMethodsAdded := b.resolveInterfaceMethodSets(state, interfacesByLang)
	if interfaceMethodsAdded > 0 {
		span.SetAttributes(attribute.Int("interface_methods_resolved", interfaceMethodsAdded))
	}

	// Phase 18: Prune interfaces that are still empty after resolution.
	// These are purely compositional interfaces whose embedded targets weren't found.
	// An empty method set would match everything via isMethodSuperset, causing false positives.
	for _, ifaces := range interfacesByLang {
		for ifaceID, methods := range ifaces {
			if len(methods) == 0 {
				delete(ifaces, ifaceID)
			}
		}
	}

	// Count interfaces for span attributes
	goInterfaceCount := len(interfacesByLang["go"])
	pythonProtocolCount := len(interfacesByLang["python"])
	tsInterfaceCount := len(interfacesByLang["typescript"])

	span.SetAttributes(
		attribute.Int("interface.go_count", goInterfaceCount),
		attribute.Int("interface.python_count", pythonProtocolCount),
		attribute.Int("interface.typescript_count", tsInterfaceCount),
	)

	if len(interfacesByLang) == 0 {
		span.AddEvent("no_interfaces_found")
		return nil // No interfaces to match
	}

	// Check context periodically for cancellation
	if err := ctx.Err(); err != nil {
		return err
	}

	// Collect types with methods, grouped by language
	// typesByLang[language][typeID] = {methodName: true}
	typesByLang := make(map[string]map[string]map[string]bool)

	for _, sym := range state.symbolsByID {
		if sym.Kind != ast.SymbolKindStruct && sym.Kind != ast.SymbolKindType && sym.Kind != ast.SymbolKindClass {
			continue
		}
		// GR-40: Go, GR-40a: Python, IT-03a A-1: TypeScript
		if sym.Language != "go" && sym.Language != "python" && sym.Language != "typescript" {
			continue
		}
		// IT-03 H-3: Include types that have embeds even if they have no direct methods,
		// because promoted methods from embedded types may satisfy interfaces.
		if sym.Metadata == nil {
			continue
		}
		// CR-20-4: Check Implements slice too — Phase 16 G-1 stores additional Go struct
		// embeds in Implements. A type with only Implements entries (no Extends, no Methods)
		// would be incorrectly skipped without this check.
		if len(sym.Metadata.Methods) == 0 && sym.Metadata.Extends == "" && len(sym.Metadata.Implements) == 0 {
			continue // No methods and no embeds
		}

		if typesByLang[sym.Language] == nil {
			typesByLang[sym.Language] = make(map[string]map[string]bool)
		}

		methodSet := make(map[string]bool)
		for _, m := range sym.Metadata.Methods {
			methodSet[m.Name] = true
		}
		typesByLang[sym.Language][sym.ID] = methodSet
	}

	// IT-03 H-3: Resolve promoted methods from EMBEDS edges.
	// In Go, when struct A embeds struct B, A inherits B's methods (promoted methods).
	// Walk EMBEDS edges recursively and merge embedded type methods into each type's method set.
	promotedCount := 0
	for _, types := range typesByLang {
		for typeID, methodSet := range types {
			added := b.resolvePromotedMethods(state, typeID, methodSet, make(map[string]bool))
			promotedCount += added
		}
	}

	if promotedCount > 0 {
		span.SetAttributes(attribute.Int("promoted_methods_added", promotedCount))
	}

	// Count types for span attributes
	goTypeCount := len(typesByLang["go"])
	pythonClassCount := len(typesByLang["python"])
	tsClassCount := len(typesByLang["typescript"])

	span.SetAttributes(
		attribute.Int("type.go_count", goTypeCount),
		attribute.Int("type.python_count", pythonClassCount),
		attribute.Int("type.typescript_count", tsClassCount),
	)

	if len(typesByLang) == 0 {
		span.AddEvent("no_types_with_methods")
		return nil // No types with methods
	}

	// Match types to interfaces within the same language
	edgesCreated := 0
	matchesChecked := 0

	// GR-40a: Track per-language metrics for observability (C-2 fix)
	edgesByLang := make(map[string]int)
	matchesByLang := make(map[string]int)

	for lang, interfaces := range interfacesByLang {
		typesWithMethods, hasTypes := typesByLang[lang]
		if !hasTypes {
			continue
		}

		langEdges := 0
		langMatches := 0

		for typeID, typeMethods := range typesWithMethods {
			// Check context periodically for responsiveness on large codebases
			if matchesChecked%1000 == 0 {
				if err := ctx.Err(); err != nil {
					span.SetAttributes(
						attribute.Int("edges_created", edgesCreated),
						attribute.Int("matches_checked", matchesChecked),
						attribute.Bool("cancelled", true),
					)
					return err
				}
			}

			typeSym := state.symbolsByID[typeID]
			if typeSym == nil {
				continue
			}

			for ifaceID, ifaceMethods := range interfaces {
				matchesChecked++
				langMatches++

				// Check if type's method set is a superset of interface's method set
				if isMethodSuperset(typeMethods, ifaceMethods) {
					// Create EdgeTypeImplements from type to interface
					err := state.graph.AddEdge(typeID, ifaceID, EdgeTypeImplements, typeSym.Location())
					if err != nil {
						state.result.EdgeErrors = append(state.result.EdgeErrors, EdgeError{
							FromID:   typeID,
							ToID:     ifaceID,
							EdgeType: EdgeTypeImplements,
							Err:      err,
						})
						continue
					}
					edgesCreated++
					langEdges++
					state.result.Stats.EdgesCreated++
				}
			}
		}

		edgesByLang[lang] = langEdges
		matchesByLang[lang] = langMatches
	}

	// Track stats for observability
	state.result.Stats.GoInterfaceEdges = edgesCreated

	// Record metrics with language dimension (GR-40a C-2 fix)
	for lang, edges := range edgesByLang {
		recordInterfaceDetectionMetricsWithLanguage(ctx, lang, edges, matchesByLang[lang])
	}
	// Also record aggregate metrics for backward compatibility
	recordInterfaceDetectionMetrics(ctx, edgesCreated, matchesChecked)

	// Set final span attributes
	span.SetAttributes(
		attribute.Int("edges_created", edgesCreated),
		attribute.Int("matches_checked", matchesChecked),
	)

	span.AddEvent("interface_detection_complete")

	return nil
}

// isMethodSuperset returns true if superset contains all methods in subset.
//
// Description:
//
//	Checks if a type's method set (superset) contains all methods required
//	by an interface (subset). This is the core matching logic for GR-40/GR-40a
//	interface implementation detection.
//
// Inputs:
//   - superset: Method names from a type (struct/class).
//   - subset: Method names required by an interface.
//
// Outputs:
//   - bool: True if all methods in subset exist in superset.
//
// Limitations:
//   - Only checks method names, not parameter/return types (Phase 1).
//   - Phase 2 would add signature matching for higher accuracy.
//
// Thread Safety: This function is safe for concurrent use.
func isMethodSuperset(superset, subset map[string]bool) bool {
	// CR-20-10 note: An empty subset matches any type (mathematical superset definition).
	// This is correct mathematically but would produce false positives for interface
	// matching. Callers MUST prune empty-method interfaces before calling this function.
	// The Phase 18 prune in computeInterfaceImplementations (lines 2262-2268) handles this.
	for methodName := range subset {
		if !superset[methodName] {
			return false
		}
	}
	return true
}

// resolvePromotedMethods follows outgoing EMBEDS edges from a type and merges
// the embedded type's methods into the given method set.
//
// Description:
//
//	In Go, when struct A embeds struct B, all of B's methods are promoted to A.
//	This function walks the EMBEDS edge chain recursively (A embeds B, B embeds C)
//	and merges all discovered methods into the provided methodSet.
//
// Inputs:
//   - state: Build state containing symbolsByID for method lookups.
//   - typeID: The symbol ID of the type to resolve promoted methods for.
//   - methodSet: The type's method set to merge promoted methods into. Modified in place.
//   - visited: Set of already-visited type IDs to prevent infinite cycles.
//
// Outputs:
//   - int: Number of promoted methods added to the method set.
//
// Limitations:
//   - Only follows EMBEDS edges (not IMPLEMENTS or CALLS).
//   - Only merges method names, not full signatures (consistent with isMethodSuperset).
//
// Thread Safety: Not safe for concurrent use on the same methodSet.
//
// CR-20-3: Depth-limited to maxEmbedResolutionDepth (20) to prevent stack overflow.
func (b *Builder) resolvePromotedMethods(state *buildState, typeID string, methodSet map[string]bool, visited map[string]bool) int {
	return b.resolvePromotedMethodsWithDepth(state, typeID, methodSet, visited, 0)
}

// resolvePromotedMethodsWithDepth is the depth-limited implementation of resolvePromotedMethods.
//
// CR-20-3: Added depth parameter to prevent stack overflow on pathological embedding chains.
// Stops at maxEmbedResolutionDepth (20). Cycle detection via visited set is retained.
func (b *Builder) resolvePromotedMethodsWithDepth(state *buildState, typeID string, methodSet map[string]bool, visited map[string]bool, depth int) int {
	if depth > maxEmbedResolutionDepth {
		return 0
	}
	if visited[typeID] {
		return 0
	}
	visited[typeID] = true

	added := 0

	// Look up the node in the graph to find outgoing EMBEDS edges
	node, exists := state.graph.GetNode(typeID)
	if !exists || node == nil {
		return 0
	}

	for _, edge := range node.Outgoing {
		if edge.Type != EdgeTypeEmbeds {
			continue
		}

		// Get the embedded type's symbol to access its methods
		embeddedSym := state.symbolsByID[edge.ToID]
		if embeddedSym == nil || embeddedSym.Metadata == nil {
			continue
		}

		// Merge the embedded type's methods into our method set
		for _, m := range embeddedSym.Metadata.Methods {
			if !methodSet[m.Name] {
				methodSet[m.Name] = true
				added++
			}
		}

		// Recurse: the embedded type may itself embed other types
		added += b.resolvePromotedMethodsWithDepth(state, edge.ToID, methodSet, visited, depth+1)
	}

	return added
}

// resolveInterfaceMethodSets walks EMBEDS edges from interfaces and merges
// embedded interface methods into each interface's method set.
//
// Description:
//
//	In Go, composed interfaces like ReadWriter (embedding Reader + Writer) have no
//	direct methods — their method set is the union of their embedded interfaces' methods.
//	This function walks EMBEDS edges transitively (with cycle detection) and merges
//	discovered methods into each interface's method set in interfacesByLang.
//
//	This is structurally analogous to resolvePromotedMethods for structs, but operates
//	on the interfacesByLang map used by computeInterfaceImplementations.
//
// Inputs:
//   - state: Build state containing graph nodes and symbol data.
//   - interfacesByLang: Map of [language][interfaceID] → method set. Modified in place.
//
// Outputs:
//   - int: Total number of methods added across all interfaces.
//
// Limitations:
//   - Only follows EMBEDS edges (not IMPLEMENTS or CALLS).
//   - Only merges method names, not full signatures (consistent with isMethodSuperset).
//
// Thread Safety: Not safe for concurrent use on the same interfacesByLang.
func (b *Builder) resolveInterfaceMethodSets(state *buildState, interfacesByLang map[string]map[string]map[string]bool) int {
	totalAdded := 0

	for _, interfaces := range interfacesByLang {
		for ifaceID, methodSet := range interfaces {
			added := b.resolveInterfaceEmbedsRecursive(state, ifaceID, methodSet, make(map[string]bool))
			totalAdded += added
		}
	}

	return totalAdded
}

// resolveInterfaceEmbedsRecursive follows outgoing EMBEDS edges from an interface
// and merges the embedded interface's methods into the given method set.
//
// Description:
//
//	Recursively walks EMBEDS edges from an interface, merging method sets from
//	embedded interfaces. Uses a visited set for cycle detection.
//
// Inputs:
//   - state: Build state containing graph nodes and symbol data.
//   - ifaceID: The symbol ID of the interface to resolve.
//   - methodSet: The interface's method set to merge embedded methods into. Modified in place.
//   - visited: Set of already-visited interface IDs to prevent infinite cycles.
//
// Outputs:
//   - int: Number of methods added to the method set.
//
// Thread Safety: Not safe for concurrent use on the same methodSet.
//
// CR-20-3: Depth-limited to maxEmbedResolutionDepth (20) to prevent stack overflow.
func (b *Builder) resolveInterfaceEmbedsRecursive(state *buildState, ifaceID string, methodSet map[string]bool, visited map[string]bool) int {
	return b.resolveInterfaceEmbedsRecursiveWithDepth(state, ifaceID, methodSet, visited, 0)
}

// resolveInterfaceEmbedsRecursiveWithDepth is the depth-limited implementation.
//
// CR-20-3: Added depth parameter to prevent stack overflow on pathological interface
// embedding chains. Stops at maxEmbedResolutionDepth (20). Cycle detection via visited
// set is retained as the primary guard; depth limit is defense-in-depth.
func (b *Builder) resolveInterfaceEmbedsRecursiveWithDepth(state *buildState, ifaceID string, methodSet map[string]bool, visited map[string]bool, depth int) int {
	if depth > maxEmbedResolutionDepth {
		return 0
	}
	if visited[ifaceID] {
		return 0
	}
	visited[ifaceID] = true

	added := 0

	node, exists := state.graph.GetNode(ifaceID)
	if !exists || node == nil {
		return 0
	}

	for _, edge := range node.Outgoing {
		if edge.Type != EdgeTypeEmbeds {
			continue
		}

		// Get the embedded interface's symbol to access its methods
		embeddedSym := state.symbolsByID[edge.ToID]
		if embeddedSym == nil || embeddedSym.Metadata == nil {
			continue
		}

		// Merge the embedded interface's methods into our method set
		for _, m := range embeddedSym.Metadata.Methods {
			if !methodSet[m.Name] {
				methodSet[m.Name] = true
				added++
			}
		}

		// Recurse: the embedded interface may itself embed other interfaces
		added += b.resolveInterfaceEmbedsRecursiveWithDepth(state, edge.ToID, methodSet, visited, depth+1)
	}

	return added
}
