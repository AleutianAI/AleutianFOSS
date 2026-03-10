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
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/lsp"
)

// =============================================================================
// LSP ENRICHMENT TYPES
// =============================================================================

// LSPQuerier abstracts LSP operations for testability.
//
// Description:
//
//	GR-74: The graph builder uses this interface instead of calling lsp.Operations
//	directly. This allows tests to provide mock implementations that return
//	deterministic results without requiring actual LSP servers.
//
// Thread Safety:
//
//	Implementations must be safe for concurrent use from multiple goroutines.
type LSPQuerier interface {
	// OpenDocument notifies the LSP server that a document was opened.
	//
	// Inputs:
	//
	//	ctx - Context for cancellation and timeout
	//	filePath - Absolute path to the file
	//	content - The file content
	//
	// Outputs:
	//
	//	error - Non-nil on failure
	OpenDocument(ctx context.Context, filePath, content string) error

	// CloseDocument notifies the LSP server that a document was closed.
	//
	// Inputs:
	//
	//	ctx - Context for cancellation and timeout
	//	filePath - Absolute path to the file
	//
	// Outputs:
	//
	//	error - Non-nil on failure
	CloseDocument(ctx context.Context, filePath string) error

	// Definition returns the definition location(s) for a symbol.
	//
	// Inputs:
	//
	//	ctx - Context for cancellation and timeout
	//	filePath - Absolute path to the file
	//	line - 1-indexed line number
	//	col - 0-indexed column number
	//
	// Outputs:
	//
	//	[]lsp.Location - Definition location(s)
	//	error - Non-nil on failure
	Definition(ctx context.Context, filePath string, line, col int) ([]lsp.Location, error)
}

// LSPEnrichmentConfig configures the LSP enrichment phase.
//
// Description:
//
//	GR-74: Controls which LSP server to query, timeouts, parallelism, and
//	which languages to enrich. When added to BuilderOptions via
//	WithLSPEnrichment, the builder runs an enrichment phase after edge extraction.
//
// Thread Safety:
//
//	Immutable after construction.
type LSPEnrichmentConfig struct {
	// Querier provides LSP operations. Required — enrichment is skipped if nil.
	Querier LSPQuerier

	// Manager provides IsAvailable checks. Optional — if nil, all languages are attempted.
	Manager *lsp.Manager

	// MaxConcurrentFiles is the maximum number of files processed in parallel.
	// Default: 4
	MaxConcurrentFiles int

	// PerFileTimeout is the maximum time to process a single file's enrichment queries.
	// Default: 30s
	PerFileTimeout time.Duration

	// TotalTimeout is the maximum time for the entire enrichment phase.
	// Default: 120s
	TotalTimeout time.Duration

	// Languages is the list of languages to enrich. Placeholders from other languages
	// are skipped. Default: ["python", "typescript", "javascript"]
	// Note: JavaScript uses the same typescript-language-server binary as TypeScript.
	Languages []string

	// FileReader overrides the default os.ReadFile for reading source files.
	// If nil, os.ReadFile is used. Provided for testability.
	FileReader func(path string) ([]byte, error)
}

// enrichableQuery represents a single placeholder that can be resolved via LSP.
type enrichableQuery struct {
	edge        *Edge
	placeholder *Node
	sourceFile  string
	sourceLang  string
}

// enrichmentResult represents the outcome of a single LSP definition query.
type enrichmentResult struct {
	query          *enrichableQuery
	resolvedNodeID string
	err            error
}

// =============================================================================
// LSP ENRICHMENT PHASE
// =============================================================================

// lspEnrichmentPhase queries LSP servers to resolve placeholder edge targets.
//
// Description:
//
//	GR-74: Iterates over placeholder nodes, groups their edges by source file,
//	opens each file in the LSP server, queries definitions for each call site,
//	and replaces placeholder targets with real nodes when the definition is found
//	in the project. Processes files in parallel up to MaxConcurrentFiles.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	state - The current build state (graph, symbolsByLocation, placeholders)
//
// Outputs:
//
//	EnrichmentStats - Statistics about the enrichment phase
//	error - Non-nil only on context cancellation; partial results are preserved
//
// Thread Safety:
//
//	NOT safe for concurrent use. Called from Build() in single-threaded context.
func (b *Builder) lspEnrichmentPhase(ctx context.Context, state *buildState) (EnrichmentStats, error) {
	return b.lspEnrichmentPhaseInternal(ctx, state, nil, "graph.Builder.lspEnrichmentPhase")
}

// lspEnrichmentPhaseScoped runs LSP enrichment only for edges originating
// from files in the scopeFiles set.
//
// Description:
//
//	GR-76: Used by IncrementalRefresh to avoid re-querying unchanged files.
//	Delegates to the same enrichment logic as lspEnrichmentPhase but passes
//	scopeFiles to collectEnrichablePlaceholders for filtering.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	state - The current build state
//	scopeFiles - Set of file paths to scope enrichment to
//
// Outputs:
//
//	EnrichmentStats - Statistics for scoped enrichment
//	error - Non-nil only on context cancellation
//
// Thread Safety: NOT safe for concurrent use.
func (b *Builder) lspEnrichmentPhaseScoped(
	ctx context.Context,
	state *buildState,
	scopeFiles map[string]struct{},
) (EnrichmentStats, error) {
	return b.lspEnrichmentPhaseInternal(ctx, state, scopeFiles, "graph.IncrementalRefresh.lspEnrichment")
}

// lspEnrichmentPhaseInternal is the shared implementation for both full and
// scoped LSP enrichment.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout
//	state - The current build state (graph, symbolsByLocation, placeholders)
//	scopeFiles - If non-nil, only enrich edges from these files. Nil = all files.
//	spanName - OTel span name to distinguish full vs incremental enrichment
//
// Outputs:
//
//	EnrichmentStats - Statistics about the enrichment phase
//	error - Non-nil only on context cancellation; partial results are preserved
//
// Thread Safety:
//
//	NOT safe for concurrent use.
func (b *Builder) lspEnrichmentPhaseInternal(
	ctx context.Context,
	state *buildState,
	scopeFiles map[string]struct{},
	spanName string,
) (EnrichmentStats, error) {
	config := b.options.LSPEnrichment
	stats := EnrichmentStats{}
	start := time.Now()

	ctx, span := tracer.Start(ctx, spanName)
	defer span.End()

	// Apply total timeout
	totalTimeout := config.TotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()

	// Apply defaults
	maxConcurrent := config.MaxConcurrentFiles
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	perFileTimeout := config.PerFileTimeout
	if perFileTimeout <= 0 {
		perFileTimeout = 30 * time.Second
	}
	languages := config.Languages
	if len(languages) == 0 {
		languages = []string{"python", "typescript", "javascript"}
	}

	// Only report progress for full builds (not incremental)
	if scopeFiles == nil {
		b.reportProgress(state, ProgressPhaseLSPEnrichment, 0, 0)
	}

	// Collect enrichable placeholders
	queries := collectEnrichablePlaceholders(state, config, languages, scopeFiles)
	stats.PlaceholdersSkipped = len(state.placeholders) - len(queries)

	if len(queries) == 0 {
		stats.DurationMicro = time.Since(start).Microseconds()
		span.SetAttributes(attribute.Int("enrichment.queries", 0))
		return stats, nil
	}

	// Group queries by source file
	byFile := make(map[string][]*enrichableQuery)
	for i := range queries {
		q := &queries[i]
		byFile[q.sourceFile] = append(byFile[q.sourceFile], q)
	}
	stats.FilesQueried = len(byFile)

	// Process files in parallel, collect results
	var (
		mu      sync.Mutex
		results []enrichmentResult
		wg      sync.WaitGroup
		sem     = make(chan struct{}, maxConcurrent)
	)

	for filePath, fileQueries := range byFile {
		if err := ctx.Err(); err != nil {
			stats.DurationMicro = time.Since(start).Microseconds()
			return stats, fmt.Errorf("lsp enrichment interrupted: %w", err)
		}

		wg.Add(1)
		sem <- struct{}{} // acquire semaphore

		go func(fp string, fq []*enrichableQuery) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore

			fileCtx, fileCancel := context.WithTimeout(ctx, perFileTimeout)
			defer fileCancel()

			fileResults := processFileEnrichment(fileCtx, state, config, fp, fq)

			mu.Lock()
			results = append(results, fileResults...)
			mu.Unlock()
		}(filePath, fileQueries)
	}

	wg.Wait()

	// Apply results single-threaded
	for _, r := range results {
		stats.PlaceholdersQueried++
		if r.err != nil {
			stats.PlaceholdersFailed++
			stats.LSPErrors++
			continue
		}
		if r.resolvedNodeID == "" {
			stats.PlaceholdersFailed++
			continue
		}

		err := state.graph.ReplaceEdgeTarget(r.query.edge, r.resolvedNodeID)
		if err != nil {
			slog.Debug("GR-74: failed to replace edge target",
				slog.String("edge_from", r.query.edge.FromID),
				slog.String("old_to", r.query.placeholder.ID),
				slog.String("new_to", r.resolvedNodeID),
				slog.String("error", err.Error()),
			)
			stats.PlaceholdersFailed++
			continue
		}
		stats.PlaceholdersResolved++
	}

	// Clean up orphaned placeholders
	stats.OrphanedRemoved = removeOrphanedPlaceholders(state)

	stats.DurationMicro = time.Since(start).Microseconds()

	span.SetAttributes(
		attribute.Int("enrichment.queried", stats.PlaceholdersQueried),
		attribute.Int("enrichment.resolved", stats.PlaceholdersResolved),
		attribute.Int("enrichment.failed", stats.PlaceholdersFailed),
		attribute.Int("enrichment.skipped", stats.PlaceholdersSkipped),
		attribute.Int("enrichment.orphans_removed", stats.OrphanedRemoved),
		attribute.Int("enrichment.files", stats.FilesQueried),
	)

	return stats, nil
}

// collectEnrichablePlaceholders filters placeholders to those that can be queried via LSP.
//
// Description:
//
//	Returns queries for placeholder nodes that:
//	1. Have at least one incoming edge with a valid source file location
//	2. The source file language is in the allowed languages list
//	3. The language is not "go" (Go has excellent static resolution already)
//	4. The LSP manager reports the language as available (if manager is set)
//	5. If scopeFiles is non-nil, the edge's source file is in the set (GR-76)
//
// Inputs:
//
//	state - The current build state
//	config - LSP enrichment configuration
//	languages - List of languages to include
//	scopeFiles - If non-nil, only include edges from these files. Nil = all files.
//
// Outputs:
//
//	[]enrichableQuery - Queries ready for LSP resolution
func collectEnrichablePlaceholders(state *buildState, config *LSPEnrichmentConfig, languages []string, scopeFiles map[string]struct{}) []enrichableQuery {
	langSet := make(map[string]struct{}, len(languages))
	for _, l := range languages {
		langSet[l] = struct{}{}
	}

	var queries []enrichableQuery
	for _, placeholder := range state.placeholders {
		for _, edge := range placeholder.Incoming {
			if edge.Location.FilePath == "" || edge.Location.StartLine == 0 {
				continue
			}

			// GR-76: Scope to changed files when doing incremental enrichment
			if scopeFiles != nil {
				if _, inScope := scopeFiles[edge.Location.FilePath]; !inScope {
					continue
				}
			}

			// Infer language from file extension (edge Location doesn't carry language)
			lang := inferLanguageFromPath(edge.Location.FilePath)

			// Skip Go — already has excellent static resolution
			if lang == "go" {
				continue
			}

			if _, ok := langSet[lang]; !ok {
				continue
			}

			// Check LSP availability if manager is set
			if config.Manager != nil && !config.Manager.IsAvailable(lang) {
				continue
			}

			queries = append(queries, enrichableQuery{
				edge:        edge,
				placeholder: placeholder,
				sourceFile:  edge.Location.FilePath,
				sourceLang:  lang,
			})
		}
	}

	return queries
}

// processFileEnrichment opens a file in the LSP server and queries definitions for each call site.
//
// Description:
//
//	Reads the file from disk, opens it in the LSP server, queries each call site's
//	definition, and returns results. Each result either has a resolvedNodeID (success),
//	an error, or an empty resolvedNodeID (definition not in project).
//
// Inputs:
//
//	ctx - Context with per-file timeout
//	state - The current build state
//	config - LSP enrichment configuration
//	filePath - Absolute path to the source file
//	queries - Enrichable queries from this file
//
// Outputs:
//
//	[]enrichmentResult - One result per query
//
// Thread Safety:
//
//	Safe for concurrent use (reads state immutably, uses thread-safe LSP querier).
func processFileEnrichment(
	ctx context.Context,
	state *buildState,
	config *LSPEnrichmentConfig,
	filePath string,
	queries []*enrichableQuery,
) []enrichmentResult {
	_, span := tracer.Start(ctx, "graph.Builder.lspEnrichFile",
		trace.WithAttributes(
			attribute.String("file", filePath),
			attribute.Int("queries", len(queries)),
		),
	)
	defer span.End()

	results := make([]enrichmentResult, 0, len(queries))

	// Read file content for OpenDocument
	readFile := config.FileReader
	if readFile == nil {
		readFile = os.ReadFile
	}
	content, err := readFile(filePath)
	if err != nil {
		for _, q := range queries {
			results = append(results, enrichmentResult{
				query: q,
				err:   fmt.Errorf("reading file %s: %w", filePath, err),
			})
		}
		return results
	}

	// Open document in LSP
	if err := config.Querier.OpenDocument(ctx, filePath, string(content)); err != nil {
		for _, q := range queries {
			results = append(results, enrichmentResult{
				query: q,
				err:   fmt.Errorf("opening document %s: %w", filePath, err),
			})
		}
		return results
	}
	defer func() {
		// Best-effort close; ignore errors
		_ = config.Querier.CloseDocument(ctx, filePath)
	}()

	// Query each call site
	for _, q := range queries {
		if err := ctx.Err(); err != nil {
			results = append(results, enrichmentResult{
				query: q,
				err:   fmt.Errorf("enrichment cancelled: %w", err),
			})
			continue
		}

		locs, err := config.Querier.Definition(ctx, filePath, q.edge.Location.StartLine, q.edge.Location.StartCol)
		if err != nil {
			results = append(results, enrichmentResult{
				query: q,
				err:   fmt.Errorf("definition query at %s:%d:%d: %w", filePath, q.edge.Location.StartLine, q.edge.Location.StartCol, err),
			})
			continue
		}

		if len(locs) == 0 {
			results = append(results, enrichmentResult{query: q})
			continue
		}

		// Try to match the first definition location to a node in the project
		loc := locs[0]
		defPath := lsp.URIToPath(loc.URI)

		// Check if definition is within project
		if !strings.HasPrefix(defPath, state.graph.ProjectRoot) {
			results = append(results, enrichmentResult{query: q})
			continue
		}

		// LSP returns 0-indexed lines; our symbolsByLocation uses 1-indexed
		defLine := loc.Range.Start.Line + 1

		nodeID := findNodeByLocation(state, defPath, defLine)
		results = append(results, enrichmentResult{
			query:          q,
			resolvedNodeID: nodeID,
		})
	}

	return results
}

// findNodeByLocation finds a symbol node at or near the given file:line.
//
// Description:
//
//	Tries both absolute and relative paths (symbols are stored with relative
//	file paths, but LSP returns absolute paths). For each path form, first
//	tries exact match on "filePath:line", then fuzzy match within +-2 lines.
//
// Inputs:
//
//	state - The current build state with symbolsByLocation
//	filePath - Absolute path to the definition file
//	line - 1-indexed line number of the definition
//
// Outputs:
//
//	string - The node ID if found, empty string if not found
func findNodeByLocation(state *buildState, filePath string, line int) string {
	// Try both absolute and relative paths
	paths := []string{filePath}
	projectRoot := state.graph.ProjectRoot
	if projectRoot != "" && strings.HasPrefix(filePath, projectRoot) {
		relPath := strings.TrimPrefix(filePath, projectRoot)
		relPath = strings.TrimPrefix(relPath, "/")
		paths = append(paths, relPath)
	}

	for _, p := range paths {
		// Exact match
		key := fmt.Sprintf("%s:%d", p, line)
		if ids, ok := state.symbolsByLocation[key]; ok && len(ids) > 0 {
			return ids[0]
		}
	}

	// Fuzzy match: +-2 lines
	for delta := 1; delta <= 2; delta++ {
		for _, p := range paths {
			keyUp := fmt.Sprintf("%s:%d", p, line-delta)
			if ids, ok := state.symbolsByLocation[keyUp]; ok && len(ids) > 0 {
				return ids[0]
			}
			keyDown := fmt.Sprintf("%s:%d", p, line+delta)
			if ids, ok := state.symbolsByLocation[keyDown]; ok && len(ids) > 0 {
				return ids[0]
			}
		}
	}

	return ""
}

// removeOrphanedPlaceholders removes placeholder nodes that have no remaining edges.
//
// Description:
//
//	After LSP enrichment resolves placeholder targets, some placeholders may
//	have all their incoming edges retargeted to real nodes. These orphaned
//	placeholders are removed from the graph.
//
// Inputs:
//
//	state - The current build state
//
// Outputs:
//
//	int - Number of orphaned placeholders removed
//
// Thread Safety:
//
//	NOT safe for concurrent use. Called single-threaded after enrichment.
func removeOrphanedPlaceholders(state *buildState) int {
	removed := 0
	for id, placeholder := range state.placeholders {
		if len(placeholder.Incoming) == 0 && len(placeholder.Outgoing) == 0 {
			if err := state.graph.RemoveNode(id); err != nil {
				slog.Debug("GR-74: failed to remove orphaned placeholder",
					slog.String("id", id),
					slog.String("error", err.Error()),
				)
				continue
			}
			delete(state.placeholders, id)
			removed++
		}
	}
	return removed
}
