// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package trace provides the Trace HTTP service for code analysis.
//
// The service exposes endpoints for:
//   - Initializing and caching code graphs
//   - Querying symbols and relationships
//   - Assembling context for LLM prompts
//   - Seeding library documentation
package trace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	cbcontext "github.com/AleutianAI/AleutianFOSS/services/trace/context"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/lsp"
)

// ServiceConfig configures the Code Buddy service.
type ServiceConfig struct {
	// MaxInitDuration is the maximum time allowed for init operations.
	// Default: 30s
	MaxInitDuration time.Duration

	// MaxProjectFiles is the maximum number of files to parse.
	// Default: 10000
	MaxProjectFiles int

	// MaxProjectSize is the maximum total size of files in bytes.
	// Default: 100MB
	MaxProjectSize int64

	// MaxCachedGraphs is the maximum number of graphs to cache.
	// Default: 5
	MaxCachedGraphs int

	// GraphTTL is how long graphs are cached before expiry.
	// Default: 0 (no expiry)
	GraphTTL time.Duration

	// AllowedRoots is an optional list of allowed project root prefixes.
	// If empty, all paths are allowed. Security feature.
	AllowedRoots []string

	// LSPIdleTimeout is how long an LSP server can be idle before shutdown.
	// Default: 10 minutes
	LSPIdleTimeout time.Duration

	// LSPStartupTimeout is the maximum time to wait for LSP server startup.
	// Default: 30 seconds
	LSPStartupTimeout time.Duration

	// LSPRequestTimeout is the default timeout for LSP requests.
	// Default: 10 seconds
	LSPRequestTimeout time.Duration
}

// DefaultServiceConfig returns sensible defaults.
func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		MaxInitDuration:   30 * time.Second,
		MaxProjectFiles:   10000,
		MaxProjectSize:    100 * 1024 * 1024, // 100MB
		MaxCachedGraphs:   5,
		GraphTTL:          0, // No expiry
		LSPIdleTimeout:    10 * time.Minute,
		LSPStartupTimeout: 30 * time.Second,
		LSPRequestTimeout: 10 * time.Second,
	}
}

// Service is the Code Buddy service.
//
// Thread Safety:
//
//	Service is safe for concurrent use. Multiple goroutines can call
//	any combination of methods simultaneously.
type Service struct {
	config    ServiceConfig
	graphs    map[string]*CachedGraph
	mu        sync.RWMutex
	initLocks sync.Map // projectRoot -> *sync.Mutex

	// registry holds parser instances
	registry *ast.ParserRegistry

	// libDocProvider is optional library documentation provider
	libDocProvider cbcontext.LibraryDocProvider

	// plans holds cached change plans for validation and preview
	plans   map[string]*CachedPlan
	plansMu sync.RWMutex

	// lspManagers holds LSP managers per graph (graphID -> manager)
	lspManagers map[string]*lsp.Manager
	lspMu       sync.RWMutex
}

// CachedPlan holds a change plan and its associated graph ID.
type CachedPlan struct {
	// GraphID is the graph this plan was created for.
	GraphID string

	// Plan is the change plan.
	Plan interface{} // *coordinate.ChangePlan

	// CreatedAt is when the plan was created.
	CreatedAt time.Time
}

// NewService creates a new Code Buddy service.
//
// Description:
//
//	Creates a service with the given configuration. The service starts
//	with no cached graphs and a default parser registry.
//
// Inputs:
//
//	config - Service configuration
//
// Outputs:
//
//	*Service - The configured service
func NewService(config ServiceConfig) *Service {
	svc := &Service{
		config:      config,
		graphs:      make(map[string]*CachedGraph),
		registry:    ast.NewParserRegistry(),
		plans:       make(map[string]*CachedPlan),
		lspManagers: make(map[string]*lsp.Manager),
	}

	// Register default parsers
	svc.registry.Register(ast.NewGoParser())
	svc.registry.Register(ast.NewPythonParser())
	svc.registry.Register(ast.NewTypeScriptParser())
	svc.registry.Register(ast.NewJavaScriptParser())

	return svc
}

// SetLibraryDocProvider sets the library documentation provider.
func (s *Service) SetLibraryDocProvider(p cbcontext.LibraryDocProvider) {
	s.libDocProvider = p
}

// Init initializes a code graph for a project.
//
// Description:
//
//	Parses the project, builds the code graph and symbol index, and
//	caches the result. If a graph already exists for the project, it
//	is replaced.
//
// Inputs:
//
//	ctx - Context for cancellation
//	projectRoot - Absolute path to the project root
//	languages - Languages to parse (default: ["go"])
//	excludes - Glob patterns to exclude (default: ["vendor/*", "*_test.go"])
//
// Outputs:
//
//	*InitResponse - Graph statistics and metadata
//	error - Non-nil if validation fails or parsing fails
//
// Errors:
//
//	ErrRelativePath - Project root is not absolute
//	ErrPathTraversal - Project root contains .. sequences
//	ErrProjectTooLarge - Project exceeds configured limits
//	ErrInitInProgress - Another init is running for this project
//	ErrInitTimeout - Init took too long
func (s *Service) Init(ctx context.Context, projectRoot string, languages, excludes []string) (*InitResponse, error) {
	// Validate project root
	if err := s.validateProjectRoot(projectRoot); err != nil {
		return nil, err
	}

	// Apply defaults
	if len(languages) == 0 {
		languages = []string{"go"}
	}
	if len(excludes) == 0 {
		excludes = []string{"vendor/*", "*_test.go"}
	}

	// Get init lock for this project to prevent concurrent inits
	lock := s.getInitLock(projectRoot)
	if !lock.TryLock() {
		return nil, ErrInitInProgress
	}
	defer lock.Unlock()

	// Apply timeout
	if s.config.MaxInitDuration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.config.MaxInitDuration)
		defer cancel()
	}

	start := time.Now()

	// Generate graph ID
	graphID := s.generateGraphID(projectRoot)

	// Check if we're replacing an existing graph
	s.mu.RLock()
	existing, isRefresh := s.graphs[graphID]
	var previousID string
	if isRefresh && existing != nil {
		previousID = graphID
	}
	s.mu.RUnlock()

	// Create index
	idx := index.NewSymbolIndex()

	// Parse files into ParseResults
	parseResults, result, err := s.parseProjectToResults(ctx, projectRoot, languages, excludes)
	if err != nil {
		return nil, err
	}

	// Build graph with edges using the Builder
	// GR-41c: This ensures edge extraction (imports, calls, etc.) runs properly
	builder := graph.NewBuilder(graph.WithProjectRoot(projectRoot))
	buildResult, err := builder.Build(ctx, parseResults)
	if err != nil {
		return nil, fmt.Errorf("building graph: %w", err)
	}

	// R-1: Handle incomplete builds (context cancelled or memory limits)
	if buildResult.Incomplete {
		slog.Warn("GR-41c: Graph build incomplete",
			slog.String("project_root", projectRoot),
			slog.Int("nodes_created", buildResult.Stats.NodesCreated),
			slog.Int("edges_created", buildResult.Stats.EdgesCreated),
		)
	}

	// R-2: Merge builder errors into result.Errors
	for _, fe := range buildResult.FileErrors {
		result.Errors = append(result.Errors, fe.Error())
	}
	for _, ee := range buildResult.EdgeErrors {
		result.Errors = append(result.Errors, ee.Error())
	}

	g := buildResult.Graph

	// I-1: Add symbols to index recursively (including child symbols)
	// IT-04: Observable pipeline — log all index add failures for diagnostics.
	var totalAdded, totalDropped, totalRetried int
	for _, pr := range parseResults {
		if pr == nil {
			continue
		}
		added, dropped, retried := addSymbolsToIndexRecursive(idx, pr.Symbols, slog.Default())
		totalAdded += added
		totalDropped += dropped
		totalRetried += retried
	}
	if totalDropped > 0 || totalRetried > 0 {
		slog.Default().Warn("index population completed with issues",
			slog.Int("added", totalAdded),
			slog.Int("dropped", totalDropped),
			slog.Int("retried", totalRetried),
		)
	}

	result.SymbolsExtracted = idx.Stats().TotalSymbols

	// O-1: Log build statistics for observability
	logAttrs := []any{
		slog.String("project_root", projectRoot),
		slog.Int("nodes", buildResult.Stats.NodesCreated),
		slog.Int("edges", buildResult.Stats.EdgesCreated),
		slog.Int("placeholders", buildResult.Stats.PlaceholderNodes),
		slog.Int("call_edges_resolved", buildResult.Stats.CallEdgesResolved),
		slog.Int("call_edges_unresolved", buildResult.Stats.CallEdgesUnresolved),
		slog.Int("interface_edges", buildResult.Stats.GoInterfaceEdges),
		slog.Int64("build_duration_ms", buildResult.Stats.DurationMilli),
	}
	// Add microsecond precision for sub-millisecond builds
	if buildResult.Stats.DurationMilli == 0 && buildResult.Stats.DurationMicro > 0 {
		logAttrs = append(logAttrs, slog.Int64("build_duration_us", buildResult.Stats.DurationMicro))
	}
	logAttrs = append(logAttrs, slog.Bool("incomplete", buildResult.Incomplete))
	slog.Info("GR-41c: Graph built", logAttrs...)

	// Create assembler
	assembler := cbcontext.NewAssembler(g, idx)
	if s.libDocProvider != nil {
		assembler = assembler.WithLibraryDocProvider(s.libDocProvider)
	}

	// GR-10: Create CRSGraphAdapter for query caching
	builtAtMilli := time.Now().UnixMilli()
	var adapter *graph.CRSGraphAdapter
	hg, err := graph.WrapGraph(g)
	if err != nil {
		slog.Warn("GR-10: Failed to create hierarchical graph for adapter",
			slog.String("project_root", projectRoot),
			slog.String("error", err.Error()),
		)
	} else {
		adapter, err = graph.NewCRSGraphAdapter(hg, idx, 1, builtAtMilli, nil)
		if err != nil {
			slog.Warn("GR-10: Failed to create CRS graph adapter",
				slog.String("project_root", projectRoot),
				slog.String("error", err.Error()),
			)
		} else {
			slog.Info("GR-10: CRS graph adapter created successfully",
				slog.String("project_root", projectRoot),
				slog.Int("nodes", g.NodeCount()),
				slog.Int("edges", g.EdgeCount()),
			)

			// P3: Log explicit graph readiness for easier debugging
			slog.Info("graph ready for queries",
				slog.String("project_root", projectRoot),
				slog.Int("nodes", g.NodeCount()),
				slog.Int("edges", g.EdgeCount()),
				slog.Int64("build_time_us", buildResult.Stats.DurationMicro),
				slog.Bool("complete", !buildResult.Incomplete),
			)
		}
	}

	// Cache the graph
	cached := &CachedGraph{
		Graph:        g,
		Index:        idx,
		Assembler:    assembler,
		Adapter:      adapter,
		BuiltAtMilli: builtAtMilli,
		ProjectRoot:  projectRoot,
	}

	if s.config.GraphTTL > 0 {
		cached.ExpiresAtMilli = time.Now().Add(s.config.GraphTTL).UnixMilli()
	}

	s.mu.Lock()
	s.graphs[graphID] = cached
	s.evictIfNeeded()
	s.mu.Unlock()

	return &InitResponse{
		GraphID:          graphID,
		IsRefresh:        isRefresh,
		PreviousID:       previousID,
		FilesParsed:      result.FilesParsed,
		SymbolsExtracted: result.SymbolsExtracted,
		EdgesBuilt:       g.EdgeCount(),
		ParseTimeMs:      time.Since(start).Milliseconds(),
		Errors:           result.Errors,
	}, nil
}

// parseResult holds intermediate parsing results.
type parseResult struct {
	FilesParsed      int
	SymbolsExtracted int
	Errors           []string
}

// parseProjectToResults parses project files and returns ParseResults for builder.
//
// Description:
//
//	GR-41c: Changed to return []*ast.ParseResult for use with graph.Builder
//	to ensure proper edge extraction (imports, calls, implements, etc.).
//
// Inputs:
//   - ctx: Context for cancellation
//   - projectRoot: Absolute path to project root
//   - languages: Language filters
//   - excludes: Exclusion patterns
//
// Outputs:
//   - []*ast.ParseResult: Parse results for all files
//   - *parseResult: Stats (FilesParsed, Errors)
//   - error: Non-nil on fatal errors
func (s *Service) parseProjectToResults(ctx context.Context, projectRoot string, languages, excludes []string) ([]*ast.ParseResult, *parseResult, error) {
	result := &parseResult{
		Errors: make([]string, 0),
	}
	var parseResults []*ast.ParseResult

	// Walk the project directory
	fileCount := 0
	var totalSize int64

	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Check context
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip directories
		if d.IsDir() {
			// Skip excluded directories
			relPath, _ := filepath.Rel(projectRoot, path)
			for _, pattern := range excludes {
				if matched, _ := filepath.Match(pattern, relPath); matched {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}

		// Check exclusions
		for _, pattern := range excludes {
			if matched, _ := filepath.Match(pattern, relPath); matched {
				return nil
			}
		}

		// Check file extension matches languages
		ext := filepath.Ext(path)
		if !s.isLanguageFile(ext, languages) {
			return nil
		}

		// Check limits
		info, err := d.Info()
		if err != nil {
			return nil
		}
		totalSize += info.Size()
		if totalSize > s.config.MaxProjectSize {
			return ErrProjectTooLarge
		}

		fileCount++
		if fileCount > s.config.MaxProjectFiles {
			return ErrProjectTooLarge
		}

		// Parse file
		pr, parseErr := s.parseFileToResult(ctx, path, relPath)
		if parseErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", relPath, parseErr))
		} else {
			parseResults = append(parseResults, pr)
			result.FilesParsed++
		}

		return nil
	})

	if err != nil && err != ErrProjectTooLarge {
		return nil, nil, fmt.Errorf("walking project: %w", err)
	}
	if err == ErrProjectTooLarge {
		return nil, nil, err
	}

	return parseResults, result, nil
}

// parseFileToResult parses a single file and returns the ParseResult.
//
// Description:
//
//	GR-41c: Changed to return *ast.ParseResult instead of adding directly
//	to graph/index, enabling proper edge extraction via graph.Builder.
//	Reads the file content, selects the appropriate parser based on file
//	extension, and returns the parsed symbols and imports.
//
// Inputs:
//   - ctx: Context for cancellation. Passed to parser.Parse().
//   - absPath: Absolute path to the file on disk. Must exist and be readable.
//   - relPath: Relative path from project root. Used for symbol ID generation.
//
// Outputs:
//   - *ast.ParseResult: Contains symbols, imports, and file metadata. Never nil on success.
//   - error: Non-nil if file cannot be read or no parser exists for extension.
//
// Limitations:
//   - Only parses files with registered parser extensions.
//   - Does not validate file content encoding (assumes UTF-8).
//
// Assumptions:
//   - absPath points to a regular file (not directory or symlink).
//   - relPath uses forward slashes and is within project boundary.
//   - s.registry is initialized with at least one parser.
//
// Thread Safety: Safe for concurrent use (reads only).
func (s *Service) parseFileToResult(ctx context.Context, absPath, relPath string) (*ast.ParseResult, error) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	// Determine language from extension
	ext := filepath.Ext(relPath)

	// Get parser for file extension
	parser, ok := s.registry.GetByExtension(ext)
	if !ok {
		return nil, fmt.Errorf("no parser for extension: %s", ext)
	}

	// Parse the file
	return parser.Parse(ctx, content, relPath)
}

// isLanguageFile checks if a file extension matches any of the specified languages.
func (s *Service) isLanguageFile(ext string, languages []string) bool {
	// Check if we have a parser for this extension
	parser, ok := s.registry.GetByExtension(ext)
	if !ok {
		return false
	}

	// Check if the parser's language matches any of the requested languages
	parserLang := parser.Language()
	for _, lang := range languages {
		if parserLang == lang {
			return true
		}
	}

	return false
}

// addSymbolsToIndexRecursive adds symbols and their children to the index.
//
// Description:
//
//	GR-41c I-1 Fix: Recursively adds all symbols including children to ensure
//	the index contains all symbols that exist in the graph. Without this,
//	child symbols (e.g., nested functions, struct fields) would be in the
//	graph but not findable via the index.
//
//	IT-04 Fix: Logs all index add failures for diagnostics instead of silently
//	discarding errors. On ErrInvalidSymbol with children, strips children and
//	retries the parent alone, then adds children independently. This prevents
//	invalid children from poisoning parent symbol indexing.
//
// Inputs:
//   - idx: The symbol index to add to. Must not be nil.
//   - symbols: Slice of symbols to add. Nil entries are skipped.
//   - logger: Logger for diagnostic output. Must not be nil.
//
// Outputs:
//   - added: Number of symbols successfully added to the index.
//   - dropped: Number of symbols that could not be added (logged).
//   - retried: Number of symbols that required child-stripping retry.
//
// Thread Safety: Depends on index.SymbolIndex thread safety.
func addSymbolsToIndexRecursive(idx *index.SymbolIndex, symbols []*ast.Symbol, logger *slog.Logger) (added, dropped, retried int) {
	for i, sym := range symbols {
		if sym == nil {
			continue
		}

		err := idx.Add(sym)
		if err == nil {
			added++
			// Recursively add children
			if len(sym.Children) > 0 {
				ca, cd, cr := addSymbolsToIndexRecursive(idx, sym.Children, logger)
				added += ca
				dropped += cd
				retried += cr
			}
			continue
		}

		switch {
		case errors.Is(err, index.ErrDuplicateSymbol):
			// Expected for some patterns (re-exports, aliases). Log at debug level.
			logger.Debug("index: duplicate symbol skipped",
				slog.String("name", sym.Name),
				slog.String("kind", sym.Kind.String()),
				slog.String("id", sym.ID),
				slog.String("file", sym.FilePath),
			)
			dropped++
			// Still recurse children — they may have unique IDs
			if len(sym.Children) > 0 {
				ca, cd, cr := addSymbolsToIndexRecursive(idx, sym.Children, logger)
				added += ca
				dropped += cd
				retried += cr
			}

		case errors.Is(err, index.ErrInvalidSymbol) && len(sym.Children) > 0:
			// Children may be poisoning parent validation. Strip children, retry parent,
			// then add children independently.
			logger.Warn("index: invalid symbol with children, retrying without children",
				slog.String("name", sym.Name),
				slog.String("kind", sym.Kind.String()),
				slog.String("file", sym.FilePath),
				slog.Int("children", len(sym.Children)),
				slog.String("error", err.Error()),
			)
			retried++
			// Clone parent before stripping children to avoid mutating the original.
			// addSymbolLocked stores the pointer, so mutating the original would leave
			// an invalid-child parent in the index, violating the Validate() invariant.
			parentCopy := *sym
			parentCopy.Children = nil
			retryErr := idx.Add(&parentCopy)
			if retryErr == nil {
				added++
			} else {
				logger.Error("index: symbol still invalid after stripping children",
					slog.String("name", sym.Name),
					slog.String("kind", sym.Kind.String()),
					slog.String("file", sym.FilePath),
					slog.String("error", retryErr.Error()),
				)
				dropped++
			}
			// Add children independently regardless of parent outcome
			ca, cd, cr := addSymbolsToIndexRecursive(idx, sym.Children, logger)
			added += ca
			dropped += cd
			retried += cr

		case errors.Is(err, index.ErrInvalidSymbol):
			logger.Error("index: invalid symbol dropped",
				slog.String("name", sym.Name),
				slog.String("kind", sym.Kind.String()),
				slog.String("file", sym.FilePath),
				slog.String("error", err.Error()),
			)
			dropped++

		case errors.Is(err, index.ErrMaxSymbolsExceeded):
			// Count remaining symbols (including current) as dropped.
			remaining := len(symbols) - i
			logger.Warn("index: max symbols exceeded, stopping",
				slog.String("name", sym.Name),
				slog.String("file", sym.FilePath),
				slog.Int("remaining_unprocessed", remaining),
			)
			dropped += remaining
			return added, dropped, retried

		default:
			logger.Error("index: unexpected error adding symbol",
				slog.String("name", sym.Name),
				slog.String("kind", sym.Kind.String()),
				slog.String("file", sym.FilePath),
				slog.String("error", err.Error()),
			)
			dropped++
		}
	}
	return added, dropped, retried
}

// GetContext assembles context for a query.
//
// Description:
//
//	Uses the cached graph to assemble relevant context for an LLM prompt.
//
// Inputs:
//
//	ctx - Context for cancellation
//	graphID - ID of the graph to query
//	query - Search query or task description
//	budget - Maximum tokens to use
//
// Outputs:
//
//	*ContextResponse - Assembled context with metadata
//	error - Non-nil if graph not found or assembly fails
func (s *Service) GetContext(ctx context.Context, graphID, query string, budget int) (*ContextResponse, error) {
	cached, err := s.GetGraph(graphID)
	if err != nil {
		return nil, err
	}

	result, err := cached.Assembler.Assemble(ctx, query, budget)
	if err != nil {
		return nil, err
	}

	return &ContextResponse{
		Context:             result.Context,
		TokensUsed:          result.TokensUsed,
		SymbolsIncluded:     result.SymbolsIncluded,
		LibraryDocsIncluded: result.LibraryDocsIncluded,
		Suggestions:         result.Suggestions,
	}, nil
}

// FindCallers returns all symbols that call the given function.
//
// Description:
//
//	Searches the graph for functions that call the named function.
//
// Inputs:
//
//	ctx - Context for cancellation
//	graphID - ID of the graph to query
//	functionName - Name of the function to find callers for
//	limit - Maximum number of results (0 = default)
//
// Outputs:
//
//	[]*SymbolInfo - List of caller symbols
//	error - Non-nil if graph not found
func (s *Service) FindCallers(ctx context.Context, graphID, functionName string, limit int) ([]*SymbolInfo, error) {
	cached, err := s.GetGraph(graphID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}

	// GR-10: Use adapter for cached queries when available
	if cached.Adapter != nil {
		// Resolve function name to symbol IDs first using secondary index
		matches := cached.Graph.GetNodesByName(functionName)
		slog.Debug("GR-10: FindCallers using adapter",
			slog.String("function", functionName),
			slog.Int("matches", len(matches)),
			slog.Bool("adapter_available", true),
		)

		var callers []*SymbolInfo
		seen := make(map[string]bool)

		for _, node := range matches {
			if node.Symbol == nil {
				continue
			}
			slog.Debug("GR-10: Querying callers for symbol",
				slog.String("symbol_id", node.ID),
				slog.String("symbol_name", node.Symbol.Name),
			)
			// Use cached FindCallers from adapter
			symbols, err := cached.Adapter.FindCallers(ctx, node.ID)
			if err != nil {
				slog.Debug("GR-10: FindCallers error",
					slog.String("symbol_id", node.ID),
					slog.String("error", err.Error()),
				)
				continue // Skip on error, try other matches
			}
			slog.Debug("GR-10: FindCallers result",
				slog.String("symbol_id", node.ID),
				slog.Int("callers_found", len(symbols)),
			)
			for _, sym := range symbols {
				if !seen[sym.ID] && len(callers) < limit {
					seen[sym.ID] = true
					callers = append(callers, SymbolInfoFromAST(sym))
				}
			}
			if len(callers) >= limit {
				break
			}
		}
		return callers, nil
	} else {
		slog.Debug("GR-10: FindCallers adapter not available, using direct query",
			slog.String("function", functionName),
		)
	}

	// Fallback to direct graph query (no caching)
	results, err := cached.Graph.FindCallersByName(ctx, functionName, graph.WithLimit(limit))
	if err != nil {
		return nil, err
	}

	var callers []*SymbolInfo
	for _, queryResult := range results {
		for _, sym := range queryResult.Symbols {
			callers = append(callers, SymbolInfoFromAST(sym))
		}
	}

	return callers, nil
}

// FindImplementations returns all types that implement the given interface.
//
// Description:
//
//	Searches the graph for types that implement the named interface.
//
// Inputs:
//
//	ctx - Context for cancellation
//	graphID - ID of the graph to query
//	interfaceName - Name of the interface to find implementations for
//	limit - Maximum number of results (0 = default)
//
// Outputs:
//
//	[]*SymbolInfo - List of implementing types
//	error - Non-nil if graph not found
func (s *Service) FindImplementations(ctx context.Context, graphID, interfaceName string, limit int) ([]*SymbolInfo, error) {
	cached, err := s.GetGraph(graphID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 50
	}

	results, err := cached.Graph.FindImplementationsByName(ctx, interfaceName, graph.WithLimit(limit))
	if err != nil {
		return nil, err
	}

	var implementations []*SymbolInfo
	for _, queryResult := range results {
		for _, sym := range queryResult.Symbols {
			implementations = append(implementations, SymbolInfoFromAST(sym))
		}
	}

	return implementations, nil
}

// GetSymbol retrieves a symbol by its ID.
//
// Description:
//
//	Looks up a symbol in the graph by its unique ID.
//
// Inputs:
//
//	ctx - Context for cancellation
//	graphID - ID of the graph to query
//	symbolID - ID of the symbol to retrieve
//
// Outputs:
//
//	*SymbolInfo - The symbol if found
//	error - Non-nil if graph not found or symbol not found
func (s *Service) GetSymbol(ctx context.Context, graphID, symbolID string) (*SymbolInfo, error) {
	cached, err := s.GetGraph(graphID)
	if err != nil {
		return nil, err
	}

	sym, ok := cached.Index.GetByID(symbolID)
	if !ok {
		return nil, fmt.Errorf("symbol not found: %s", symbolID)
	}

	return SymbolInfoFromAST(sym), nil
}

// GetGraph retrieves a cached graph by ID.
//
// Description:
//
//	Returns the cached graph if it exists and hasn't expired.
//
// Inputs:
//
//	graphID - ID of the graph to retrieve
//
// Outputs:
//
//	*CachedGraph - The cached graph
//	error - ErrGraphNotInitialized if not found, ErrGraphExpired if expired
func (s *Service) GetGraph(graphID string) (*CachedGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cached, ok := s.graphs[graphID]
	if !ok {
		return nil, ErrGraphNotInitialized
	}

	// Check expiry
	if cached.ExpiresAtMilli > 0 && time.Now().UnixMilli() > cached.ExpiresAtMilli {
		return nil, ErrGraphExpired
	}

	return cached, nil
}

// GraphCount returns the number of cached graphs.
func (s *Service) GraphCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.graphs)
}

// validateProjectRoot validates the project root path.
func (s *Service) validateProjectRoot(projectRoot string) error {
	// Must be absolute
	if !filepath.IsAbs(projectRoot) {
		return ErrRelativePath
	}

	// No path traversal
	if strings.Contains(projectRoot, "..") {
		return ErrPathTraversal
	}

	// Resolve symlinks and verify still within allowed roots
	resolved, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	// Check against allowlist if configured
	if len(s.config.AllowedRoots) > 0 {
		allowed := false
		for _, root := range s.config.AllowedRoots {
			if strings.HasPrefix(resolved, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrPathTraversal
		}
	}

	return nil
}

// generateGraphID creates a deterministic ID for a project.
func (s *Service) generateGraphID(projectRoot string) string {
	hash := sha256.Sum256([]byte(projectRoot))
	return hex.EncodeToString(hash[:])[:16]
}

// getInitLock returns the init lock for a project.
func (s *Service) getInitLock(projectRoot string) *sync.Mutex {
	lock, _ := s.initLocks.LoadOrStore(projectRoot, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// evictIfNeeded removes graphs if over capacity. Caller must hold write lock.
func (s *Service) evictIfNeeded() {
	for len(s.graphs) > s.config.MaxCachedGraphs {
		// Find oldest graph
		var oldestID string
		var oldestTime int64 = time.Now().UnixMilli()
		for id, cached := range s.graphs {
			if cached.BuiltAtMilli < oldestTime {
				oldestTime = cached.BuiltAtMilli
				oldestID = id
			}
		}
		if oldestID != "" {
			delete(s.graphs, oldestID)
		}
	}
}

// getFirstGraph returns the first cached graph, or nil if none exist.
//
// Description:
//
//	Used by debug endpoints when no graph_id is specified.
//	Returns the most recently built graph if multiple exist.
//
// Outputs:
//
//	*CachedGraph - The first cached graph, or nil if none exist.
//
// Thread Safety: Safe for concurrent use.
func (s *Service) getFirstGraph() *CachedGraph {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var newest *CachedGraph
	var newestTime int64

	for _, cached := range s.graphs {
		if cached.BuiltAtMilli > newestTime {
			newestTime = cached.BuiltAtMilli
			newest = cached
		}
	}

	return newest
}

// =============================================================================
// PLAN STORAGE METHODS (CB-22b)
// =============================================================================

// StorePlan stores a change plan for later validation and preview.
//
// Description:
//
//	Stores the plan with its associated graph ID so it can be retrieved
//	for validation and preview. Plans expire after 1 hour.
//
// Inputs:
//
//	plan - The change plan (must have ID field)
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) StorePlan(plan interface{}) {
	s.plansMu.Lock()
	defer s.plansMu.Unlock()

	// Extract plan ID and graph ID via reflection or type assertion
	// For now we use a type switch
	type planWithID interface {
		GetID() string
	}

	var planID string
	var graphID string

	// Type assertion for coordinate.ChangePlan
	if p, ok := plan.(interface{ GetID() string }); ok {
		planID = p.GetID()
	}

	// Try to get GraphID if available
	if p, ok := plan.(interface{ GetGraphID() string }); ok {
		graphID = p.GetGraphID()
	}

	// Fallback: use the plan pointer address as ID
	if planID == "" {
		planID = fmt.Sprintf("plan_%d", time.Now().UnixNano())
	}

	s.plans[planID] = &CachedPlan{
		GraphID:   graphID,
		Plan:      plan,
		CreatedAt: time.Now(),
	}

	// Evict old plans (keep last 100)
	s.evictOldPlans()
}

// GetPlan retrieves a stored change plan by ID.
//
// Description:
//
//	Returns the plan if found and not expired.
//
// Inputs:
//
//	planID - The plan ID
//
// Outputs:
//
//	interface{} - The plan (caller casts to *coordinate.ChangePlan)
//	error - Non-nil if plan not found
func (s *Service) GetPlan(planID string) (interface{}, error) {
	s.plansMu.RLock()
	defer s.plansMu.RUnlock()

	cached, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}

	// Check expiry (1 hour)
	if time.Since(cached.CreatedAt) > time.Hour {
		return nil, fmt.Errorf("plan expired: %s", planID)
	}

	return cached.Plan, nil
}

// GetGraphForPlan returns the graph associated with a plan.
//
// Description:
//
//	Finds the graph that was used to create the plan.
//
// Inputs:
//
//	plan - The change plan
//
// Outputs:
//
//	*CachedGraph - The graph
//	error - Non-nil if graph not found
func (s *Service) GetGraphForPlan(plan interface{}) (*CachedGraph, error) {
	// Try to get GraphID from the plan
	var graphID string
	if p, ok := plan.(interface{ GetGraphID() string }); ok {
		graphID = p.GetGraphID()
	}

	if graphID == "" {
		// Search for the plan in cache to find graph ID
		s.plansMu.RLock()
		for _, cached := range s.plans {
			if cached.Plan == plan {
				graphID = cached.GraphID
				break
			}
		}
		s.plansMu.RUnlock()
	}

	if graphID == "" {
		return nil, fmt.Errorf("could not determine graph ID for plan")
	}

	return s.GetGraph(graphID)
}

// evictOldPlans removes plans older than 1 hour or if over 100 plans.
func (s *Service) evictOldPlans() {
	maxPlans := 100
	maxAge := time.Hour

	// Remove expired plans
	for id, cached := range s.plans {
		if time.Since(cached.CreatedAt) > maxAge {
			delete(s.plans, id)
		}
	}

	// If still over limit, remove oldest
	for len(s.plans) > maxPlans {
		var oldestID string
		var oldestTime time.Time = time.Now()
		for id, cached := range s.plans {
			if cached.CreatedAt.Before(oldestTime) {
				oldestTime = cached.CreatedAt
				oldestID = id
			}
		}
		if oldestID != "" {
			delete(s.plans, oldestID)
		}
	}
}

// =============================================================================
// LSP INTEGRATION METHODS (CB-24)
// =============================================================================

// getOrCreateLSPManager returns or creates an LSP manager for a graph.
//
// Description:
//
//	Returns an existing LSP manager for the graph if one exists, or creates
//	a new one based on the graph's project root. The manager is configured
//	with the service's LSP settings.
//
// Inputs:
//
//	graphID - The graph to get/create a manager for
//
// Outputs:
//
//	*lsp.Manager - The LSP manager
//	error - Non-nil if graph not found
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) getOrCreateLSPManager(graphID string) (*lsp.Manager, error) {
	// Check if manager already exists
	s.lspMu.RLock()
	mgr, ok := s.lspManagers[graphID]
	s.lspMu.RUnlock()

	if ok {
		return mgr, nil
	}

	// Get the graph to find project root
	cached, err := s.GetGraph(graphID)
	if err != nil {
		return nil, err
	}

	// Create new manager
	s.lspMu.Lock()
	defer s.lspMu.Unlock()

	// Double-check after acquiring write lock
	if mgr, ok := s.lspManagers[graphID]; ok {
		return mgr, nil
	}

	config := lsp.ManagerConfig{
		IdleTimeout:    s.config.LSPIdleTimeout,
		StartupTimeout: s.config.LSPStartupTimeout,
		RequestTimeout: s.config.LSPRequestTimeout,
	}

	mgr = lsp.NewManager(cached.ProjectRoot, config)
	mgr.StartIdleMonitor()
	s.lspManagers[graphID] = mgr

	return mgr, nil
}

// getLSPOperations returns LSP operations for a graph.
//
// Description:
//
//	Returns an Operations instance for performing LSP queries on the
//	graph's project.
//
// Inputs:
//
//	graphID - The graph ID
//
// Outputs:
//
//	*lsp.Operations - The operations instance
//	error - Non-nil if graph not found
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) getLSPOperations(graphID string) (*lsp.Operations, error) {
	mgr, err := s.getOrCreateLSPManager(graphID)
	if err != nil {
		return nil, err
	}
	return lsp.NewOperations(mgr), nil
}

// LSPDefinition returns the definition location(s) for a symbol.
//
// Description:
//
//	Uses the LSP server to find the definition of the symbol at the
//	given position. More accurate than graph-based lookup for cross-file
//	and type-based resolution.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	graphID - The graph to use for project context
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//
// Outputs:
//
//	*LSPDefinitionResponse - Definition locations with latency
//	error - Non-nil on failure
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPDefinition(ctx context.Context, graphID, filePath string, line, col int) (*LSPDefinitionResponse, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	start := time.Now()

	ops, err := s.getLSPOperations(graphID)
	if err != nil {
		return nil, fmt.Errorf("get lsp operations: %w", err)
	}

	locs, err := ops.Definition(ctx, filePath, line, col)
	if err != nil {
		return nil, fmt.Errorf("lsp definition: %w", err)
	}

	return &LSPDefinitionResponse{
		Locations: lspLocationsToAPI(locs),
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

// LSPReferences returns all references to a symbol.
//
// Description:
//
//	Uses the LSP server to find all references to the symbol at the
//	given position. More accurate than graph-based lookup.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	graphID - The graph to use for project context
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//	includeDecl - Whether to include the declaration in results
//
// Outputs:
//
//	*LSPReferencesResponse - Reference locations with latency
//	error - Non-nil on failure
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPReferences(ctx context.Context, graphID, filePath string, line, col int, includeDecl bool) (*LSPReferencesResponse, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	start := time.Now()

	ops, err := s.getLSPOperations(graphID)
	if err != nil {
		return nil, fmt.Errorf("get lsp operations: %w", err)
	}

	locs, err := ops.References(ctx, filePath, line, col, includeDecl)
	if err != nil {
		return nil, fmt.Errorf("lsp references: %w", err)
	}

	return &LSPReferencesResponse{
		Locations: lspLocationsToAPI(locs),
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

// LSPHover returns type and documentation info for a symbol.
//
// Description:
//
//	Uses the LSP server to get hover information (type, documentation)
//	for the symbol at the given position.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	graphID - The graph to use for project context
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//
// Outputs:
//
//	*LSPHoverResponse - Hover content with latency
//	error - Non-nil on failure
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPHover(ctx context.Context, graphID, filePath string, line, col int) (*LSPHoverResponse, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}

	start := time.Now()

	ops, err := s.getLSPOperations(graphID)
	if err != nil {
		return nil, fmt.Errorf("get lsp operations: %w", err)
	}

	info, err := ops.Hover(ctx, filePath, line, col)
	if err != nil {
		return nil, fmt.Errorf("lsp hover: %w", err)
	}

	resp := &LSPHoverResponse{
		LatencyMs: time.Since(start).Milliseconds(),
	}

	if info != nil {
		resp.Content = info.Content
		resp.Kind = info.Kind
		if info.Range != nil {
			resp.Range = &LSPLocation{
				StartLine:   info.Range.Start.Line + 1, // Convert to 1-indexed
				StartColumn: info.Range.Start.Character,
				EndLine:     info.Range.End.Line + 1,
				EndColumn:   info.Range.End.Character,
			}
		}
	}

	return resp, nil
}

// LSPRename computes edits for renaming a symbol.
//
// Description:
//
//	Uses the LSP server to compute all edits needed to rename the symbol
//	at the given position. Returns the edits but does NOT apply them.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	graphID - The graph to use for project context
//	filePath - Absolute path to the file
//	line - 1-indexed line number
//	col - 0-indexed column number
//	newName - The new name for the symbol
//
// Outputs:
//
//	*LSPRenameResponse - Edits with file count and latency
//	error - Non-nil on failure
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPRename(ctx context.Context, graphID, filePath string, line, col int, newName string) (*LSPRenameResponse, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if newName == "" {
		return nil, fmt.Errorf("newName must not be empty")
	}

	start := time.Now()

	ops, err := s.getLSPOperations(graphID)
	if err != nil {
		return nil, fmt.Errorf("get lsp operations: %w", err)
	}

	edit, err := ops.Rename(ctx, filePath, line, col, newName)
	if err != nil {
		return nil, fmt.Errorf("lsp rename: %w", err)
	}

	edits := lspWorkspaceEditToAPI(edit)
	editCount := 0
	for _, fileEdits := range edits {
		editCount += len(fileEdits)
	}

	return &LSPRenameResponse{
		Edits:     edits,
		FileCount: len(edits),
		EditCount: editCount,
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

// LSPWorkspaceSymbol finds symbols matching a query.
//
// Description:
//
//	Uses the LSP server to find symbols matching the query across
//	the workspace.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	graphID - The graph to use for project context
//	language - The language to search (required)
//	query - The symbol search query
//
// Outputs:
//
//	*LSPWorkspaceSymbolResponse - Matching symbols with latency
//	error - Non-nil on failure
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPWorkspaceSymbol(ctx context.Context, graphID, language, query string) (*LSPWorkspaceSymbolResponse, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if language == "" {
		return nil, fmt.Errorf("language must not be empty")
	}

	start := time.Now()

	ops, err := s.getLSPOperations(graphID)
	if err != nil {
		return nil, fmt.Errorf("get lsp operations: %w", err)
	}

	symbols, err := ops.WorkspaceSymbol(ctx, language, query)
	if err != nil {
		return nil, fmt.Errorf("lsp workspace symbol: %w", err)
	}

	return &LSPWorkspaceSymbolResponse{
		Symbols:   lspSymbolsToAPI(symbols),
		LatencyMs: time.Since(start).Milliseconds(),
	}, nil
}

// LSPStatus returns the status of LSP for a graph.
//
// Description:
//
//	Returns information about LSP availability and running servers
//	for the given graph.
//
// Inputs:
//
//	graphID - The graph to check status for
//
// Outputs:
//
//	*LSPStatusResponse - Status information
//	error - Non-nil if graph not found
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) LSPStatus(graphID string) (*LSPStatusResponse, error) {
	// Check if graph exists
	if _, err := s.GetGraph(graphID); err != nil {
		return nil, err
	}

	// Check if we have a manager
	s.lspMu.RLock()
	mgr, ok := s.lspManagers[graphID]
	s.lspMu.RUnlock()

	resp := &LSPStatusResponse{
		Available:          true,
		RunningServers:     []string{},
		SupportedLanguages: []string{},
	}

	if ok {
		resp.RunningServers = mgr.RunningServers()
		resp.SupportedLanguages = mgr.Configs().Languages()
	} else {
		// No manager yet, get supported languages from a temp registry
		registry := lsp.NewConfigRegistry()
		resp.SupportedLanguages = registry.Languages()
	}

	return resp, nil
}

// Close shuts down all LSP managers and cleans up resources.
//
// Description:
//
//	Gracefully shuts down all running LSP servers. Should be called
//	when the service is being stopped.
//
// Inputs:
//
//	ctx - Context for shutdown timeout
//
// Outputs:
//
//	error - Non-nil if any shutdown encountered errors
//
// Thread Safety:
//
//	Safe for concurrent use.
func (s *Service) Close(ctx context.Context) error {
	s.lspMu.Lock()
	managers := make(map[string]*lsp.Manager)
	for id, mgr := range s.lspManagers {
		managers[id] = mgr
	}
	s.lspManagers = make(map[string]*lsp.Manager)
	s.lspMu.Unlock()

	var lastErr error
	for _, mgr := range managers {
		if err := mgr.ShutdownAll(ctx); err != nil {
			lastErr = err
		}
	}

	return lastErr
}

// =============================================================================
// LSP TYPE CONVERSION HELPERS
// =============================================================================

// lspLocationsToAPI converts LSP locations to API locations.
func lspLocationsToAPI(locs []lsp.Location) []LSPLocation {
	if locs == nil {
		return []LSPLocation{}
	}

	result := make([]LSPLocation, len(locs))
	for i, loc := range locs {
		result[i] = LSPLocation{
			FilePath:    strings.TrimPrefix(loc.URI, "file://"),
			StartLine:   loc.Range.Start.Line + 1, // Convert to 1-indexed
			StartColumn: loc.Range.Start.Character,
			EndLine:     loc.Range.End.Line + 1,
			EndColumn:   loc.Range.End.Character,
		}
	}
	return result
}

// lspWorkspaceEditToAPI converts LSP workspace edit to API format.
func lspWorkspaceEditToAPI(edit *lsp.WorkspaceEdit) map[string][]LSPTextEdit {
	if edit == nil {
		return make(map[string][]LSPTextEdit)
	}

	result := make(map[string][]LSPTextEdit)

	for uri, edits := range edit.Changes {
		filePath := strings.TrimPrefix(uri, "file://")
		apiEdits := make([]LSPTextEdit, len(edits))
		for i, e := range edits {
			apiEdits[i] = LSPTextEdit{
				Range: LSPLocation{
					FilePath:    filePath,
					StartLine:   e.Range.Start.Line + 1,
					StartColumn: e.Range.Start.Character,
					EndLine:     e.Range.End.Line + 1,
					EndColumn:   e.Range.End.Character,
				},
				NewText: e.NewText,
			}
		}
		result[filePath] = apiEdits
	}

	return result
}

// lspSymbolsToAPI converts LSP symbols to API format.
func lspSymbolsToAPI(symbols []lsp.SymbolInformation) []LSPSymbolInfo {
	if symbols == nil {
		return []LSPSymbolInfo{}
	}

	result := make([]LSPSymbolInfo, len(symbols))
	for i, sym := range symbols {
		result[i] = LSPSymbolInfo{
			Name:          sym.Name,
			Kind:          symbolKindToString(sym.Kind),
			ContainerName: sym.ContainerName,
			Location: LSPLocation{
				FilePath:    strings.TrimPrefix(sym.Location.URI, "file://"),
				StartLine:   sym.Location.Range.Start.Line + 1,
				StartColumn: sym.Location.Range.Start.Character,
				EndLine:     sym.Location.Range.End.Line + 1,
				EndColumn:   sym.Location.Range.End.Character,
			},
		}
	}
	return result
}

// symbolKindToString converts LSP symbol kind to string.
func symbolKindToString(kind lsp.SymbolKind) string {
	switch kind {
	case lsp.SymbolKindFile:
		return "file"
	case lsp.SymbolKindModule:
		return "module"
	case lsp.SymbolKindNamespace:
		return "namespace"
	case lsp.SymbolKindPackage:
		return "package"
	case lsp.SymbolKindClass:
		return "class"
	case lsp.SymbolKindMethod:
		return "method"
	case lsp.SymbolKindProperty:
		return "property"
	case lsp.SymbolKindField:
		return "field"
	case lsp.SymbolKindConstructor:
		return "constructor"
	case lsp.SymbolKindEnum:
		return "enum"
	case lsp.SymbolKindInterface:
		return "interface"
	case lsp.SymbolKindFunction:
		return "function"
	case lsp.SymbolKindVariable:
		return "variable"
	case lsp.SymbolKindConstant:
		return "constant"
	case lsp.SymbolKindStruct:
		return "struct"
	default:
		return "unknown"
	}
}
