// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package rag

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
	"go.opentelemetry.io/otel/attribute"
)

// SymbolStore handles batch indexing and deletion of code symbols in Weaviate.
//
// This is the write side of the semantic resolution layer. Symbols are indexed
// during graph init and refreshed when files change.
//
// CRS-26i: Supports pre-computed vectors via EmbedClient. When embedder is set,
// vectors are computed by the orchestrator before Weaviate insertion. When nil,
// symbols are inserted without vectors (graceful degradation — search won't work
// but indexing doesn't crash).
//
// Thread Safety: Safe for concurrent use.
type SymbolStore struct {
	client    *weaviate.Client
	dataSpace string
	embedder  *EmbedClient
}

// NewSymbolStore creates a new symbol store.
//
// Inputs:
//
//	client - Weaviate client. Must not be nil.
//	dataSpace - Project isolation key. Must not be empty.
//	embedder - Embedding client for pre-computing vectors. May be nil (graceful degradation).
//
// Outputs:
//
//	*SymbolStore - The configured store.
//	error - Non-nil if client is nil or dataSpace is empty.
//
// Thread Safety: Safe for concurrent use after construction.
func NewSymbolStore(client *weaviate.Client, dataSpace string, embedder *EmbedClient) (*SymbolStore, error) {
	if client == nil {
		return nil, errors.New("client must not be nil")
	}
	if dataSpace == "" {
		return nil, errors.New("dataSpace must not be empty")
	}
	return &SymbolStore{
		client:    client,
		dataSpace: dataSpace,
		embedder:  embedder,
	}, nil
}

// IndexProgressFn is a callback for reporting indexing progress.
//
// Description:
//
//	Called at key phases during IndexSymbols: after collecting symbols,
//	after embedding, and after each Weaviate batch insert.
//
// Inputs:
//
//	phase          - Current phase: "collecting", "embedding", "inserting".
//	batchesCompleted - Number of batches completed (inserting phase).
//	batchesTotal     - Total number of batches (inserting phase).
//	symbolsTotal     - Total number of symbols being indexed.
type IndexProgressFn func(phase string, batchesCompleted, batchesTotal, symbolsTotal int)

// IndexSymbols batch-inserts symbols from the SymbolIndex into Weaviate.
//
// Description:
//
//	Iterates all exported symbols from the index, builds a searchText
//	(natural language description), and batch-inserts into the CodeSymbol class.
//	Only indexes exported symbols to keep the collection focused.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	idx - Symbol index to read from.
//	graphHash - Hash identifying this graph version (for skip-on-match optimization).
//	onProgress - Optional callback for progress reporting. May be nil.
//
// Outputs:
//
//	int - Number of symbols indexed.
//	error - Non-nil if batch insert fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) IndexSymbols(ctx context.Context, idx *index.SymbolIndex, graphHash string, onProgress IndexProgressFn) (int, error) {
	ctx, span := tracer.Start(ctx, "rag.SymbolStore.IndexSymbols")
	defer span.End()

	var objects []*models.Object
	seen := make(map[string]bool)

	for _, kind := range allSymbolKinds() {
		for _, sym := range idx.GetByKind(kind) {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if !sym.Exported {
				continue
			}
			if seen[sym.ID] {
				continue
			}
			seen[sym.ID] = true

			searchText := BuildSearchText(sym)
			objects = append(objects, &models.Object{
				Class: CodeSymbolClassName,
				Properties: map[string]interface{}{
					"symbolId":    sym.ID,
					"searchText":  searchText,
					"name":        sym.Name,
					"kind":        sym.Kind.String(),
					"packagePath": sym.Package,
					"filePath":    sym.FilePath,
					"exported":    sym.Exported,
					"dataSpace":   s.dataSpace,
					"graphHash":   graphHash,
				},
			})
		}
	}

	if len(objects) == 0 {
		span.SetAttributes(attribute.Int("rag.indexed", 0))
		return 0, nil
	}

	if onProgress != nil {
		onProgress("collecting", 0, 0, len(objects))
	}

	// CRS-26i: Pre-compute vectors for searchText fields.
	if s.embedder != nil {
		if onProgress != nil {
			onProgress("embedding", 0, 1, len(objects))
		}
		texts := make([]string, len(objects))
		for i, obj := range objects {
			props, _ := obj.Properties.(map[string]interface{})
			texts[i], _ = props["searchText"].(string)
		}
		vectors, err := s.embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			slog.Warn("CRS-26i: Failed to compute vectors, inserting without vectors",
				slog.String("error", err.Error()),
				slog.Int("symbol_count", len(objects)),
			)
		} else if len(vectors) != len(objects) {
			slog.Warn("CRS-26i: Vector count mismatch, inserting without vectors",
				slog.Int("expected", len(objects)),
				slog.Int("got", len(vectors)),
			)
		} else {
			for i, vec := range vectors {
				objects[i].Vector = vec
			}
			slog.Info("CRS-26i: Pre-computed vectors for symbols",
				slog.Int("count", len(vectors)),
			)
		}
		if onProgress != nil {
			onProgress("embedding", 1, 1, len(objects))
		}
	}

	// Batch insert in chunks of 100.
	const batchSize = 100
	totalBatches := (len(objects) + batchSize - 1) / batchSize
	indexed := 0
	batchNum := 0
	for i := 0; i < len(objects); i += batchSize {
		end := i + batchSize
		if end > len(objects) {
			end = len(objects)
		}
		batch := objects[i:end]

		resp, err := s.client.Batch().ObjectsBatcher().
			WithObjects(batch...).
			Do(ctx)
		if err != nil {
			return indexed, fmt.Errorf("batch insert symbols (offset %d): %w", i, err)
		}

		batchErrors := 0
		for _, r := range resp {
			if r.Result != nil && r.Result.Errors != nil && len(r.Result.Errors.Error) > 0 {
				batchErrors++
				for _, e := range r.Result.Errors.Error {
					slog.Warn("CRS-26i: Symbol batch insert error",
						slog.String("symbol", fmt.Sprintf("%v", r.Properties)),
						slog.String("error", e.Message),
					)
				}
			}
		}
		indexed += len(batch) - batchErrors
		batchNum++
		if onProgress != nil {
			onProgress("inserting", batchNum, totalBatches, len(objects))
		}
	}

	span.SetAttributes(attribute.Int("rag.indexed", indexed))
	slog.Info("CRS-26i: Symbols indexed in Weaviate",
		slog.Int("count", indexed),
		slog.String("graph_hash", graphHash),
	)
	return indexed, nil
}

// DeleteByFile removes all symbols for a given file path.
//
// Description:
//
//	Used during incremental graph refresh — when a file changes, delete its
//	old symbols before re-indexing the new ones.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	filePath - The file path whose symbols to delete.
//
// Outputs:
//
//	error - Non-nil if deletion fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) DeleteByFile(ctx context.Context, filePath string) error {
	ctx, span := tracer.Start(ctx, "rag.SymbolStore.DeleteByFile")
	defer span.End()
	span.SetAttributes(attribute.String("rag.file_path", filePath))

	where := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{
			filters.Where().
				WithPath([]string{"dataSpace"}).
				WithOperator(filters.Equal).
				WithValueString(s.dataSpace),
			filters.Where().
				WithPath([]string{"filePath"}).
				WithOperator(filters.Equal).
				WithValueString(filePath),
		})

	_, err := s.client.Batch().ObjectsBatchDeleter().
		WithClassName(CodeSymbolClassName).
		WithWhere(where).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("deleting symbols for file %s: %w", filePath, err)
	}
	return nil
}

// DeleteAll removes all symbols for this data space.
//
// Description:
//
//	Used before full re-index when the graph hash changes.
//
// Inputs:
//
//	ctx - Context for cancellation.
//
// Outputs:
//
//	error - Non-nil if deletion fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) DeleteAll(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "rag.SymbolStore.DeleteAll")
	defer span.End()

	// CRS-26n: Schema drop+recreate in O(1) replaces batch delete which
	// hangs on 300K+ objects. Safe because only one dataSpace is active.
	if err := ResetCodeSymbolSchema(ctx, s.client); err != nil {
		return fmt.Errorf("resetting schema for dataSpace %s: %w", s.dataSpace, err)
	}

	slog.Info("CRS-26n: All symbols cleared via schema reset",
		slog.String("data_space", s.dataSpace),
	)
	return nil
}

// HasGraphHash checks if the current Weaviate data matches the given graph hash.
//
// Description:
//
//	Queries for any symbol with this graphHash + dataSpace. If found, the graph
//	hasn't changed and we can skip re-indexing (~5ms check vs ~30s full index).
//
// Inputs:
//
//	ctx - Context for cancellation.
//	graphHash - Hash to check against.
//
// Outputs:
//
//	bool - True if Weaviate already has symbols for this graph hash.
//	error - Non-nil if the query fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) HasGraphHash(ctx context.Context, graphHash string) (bool, error) {
	where := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{
			filters.Where().
				WithPath([]string{"dataSpace"}).
				WithOperator(filters.Equal).
				WithValueString(s.dataSpace),
			filters.Where().
				WithPath([]string{"graphHash"}).
				WithOperator(filters.Equal).
				WithValueString(graphHash),
		})

	result, err := s.client.GraphQL().Aggregate().
		WithClassName(CodeSymbolClassName).
		WithWhere(where).
		WithFields(graphql.Field{Name: "meta", Fields: []graphql.Field{{Name: "count"}}}).
		Do(ctx)
	if err != nil {
		return false, fmt.Errorf("checking graph hash: %w", err)
	}

	// Parse aggregate count.
	if result.Data != nil {
		if agg, ok := result.Data["Aggregate"].(map[string]interface{}); ok {
			if cls, ok := agg[CodeSymbolClassName].([]interface{}); ok && len(cls) > 0 {
				if entry, ok := cls[0].(map[string]interface{}); ok {
					if meta, ok := entry["meta"].(map[string]interface{}); ok {
						if count, ok := meta["count"].(float64); ok {
							return count > 0, nil
						}
					}
				}
			}
		}
	}

	return false, nil
}

// IndexFileSymbols indexes exported symbols from specific files into Weaviate.
//
// Description:
//
//	Used during incremental graph refresh (CRS-25b). After a file changes, its old
//	symbols are deleted via DeleteByFile and new symbols are indexed via this method.
//	Only indexes exported symbols, matching the behavior of IndexSymbols.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	idx - Symbol index to read from (already refreshed).
//	files - File paths whose symbols to index.
//	graphHash - Current graph hash for the symbols.
//
// Outputs:
//
//	int - Number of symbols indexed.
//	error - Non-nil if batch insert fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) IndexFileSymbols(ctx context.Context, idx *index.SymbolIndex, files []string, graphHash string) (int, error) {
	ctx, span := tracer.Start(ctx, "rag.SymbolStore.IndexFileSymbols")
	defer span.End()

	var objects []*models.Object
	seen := make(map[string]bool)

	for _, file := range files {
		for _, sym := range idx.GetByFile(file) {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			if !sym.Exported || seen[sym.ID] {
				continue
			}
			seen[sym.ID] = true

			searchText := BuildSearchText(sym)
			objects = append(objects, &models.Object{
				Class: CodeSymbolClassName,
				Properties: map[string]interface{}{
					"symbolId":    sym.ID,
					"searchText":  searchText,
					"name":        sym.Name,
					"kind":        sym.Kind.String(),
					"packagePath": sym.Package,
					"filePath":    sym.FilePath,
					"exported":    sym.Exported,
					"dataSpace":   s.dataSpace,
					"graphHash":   graphHash,
				},
			})
		}
	}

	if len(objects) == 0 {
		span.SetAttributes(attribute.Int("rag.indexed", 0))
		return 0, nil
	}

	// CRS-26i: Pre-compute vectors for searchText fields.
	if s.embedder != nil {
		texts := make([]string, len(objects))
		for i, obj := range objects {
			props, _ := obj.Properties.(map[string]interface{})
			texts[i], _ = props["searchText"].(string)
		}
		vectors, err := s.embedder.EmbedDocuments(ctx, texts)
		if err != nil {
			slog.Warn("CRS-26i: Failed to compute file symbol vectors, inserting without vectors",
				slog.String("error", err.Error()),
				slog.Int("symbol_count", len(objects)),
			)
		} else if len(vectors) != len(objects) {
			slog.Warn("CRS-26i: File symbol vector count mismatch, inserting without vectors",
				slog.Int("expected", len(objects)),
				slog.Int("got", len(vectors)),
			)
		} else {
			for i, vec := range vectors {
				objects[i].Vector = vec
			}
		}
	}

	// Batch insert in chunks of 100 (same as IndexSymbols).
	const batchSize = 100
	indexed := 0
	for i := 0; i < len(objects); i += batchSize {
		end := i + batchSize
		if end > len(objects) {
			end = len(objects)
		}
		batch := objects[i:end]

		resp, err := s.client.Batch().ObjectsBatcher().
			WithObjects(batch...).
			Do(ctx)
		if err != nil {
			return indexed, fmt.Errorf("batch insert file symbols (offset %d): %w", i, err)
		}

		batchErrors := 0
		for _, r := range resp {
			if r.Result != nil && r.Result.Errors != nil && len(r.Result.Errors.Error) > 0 {
				batchErrors++
				for _, e := range r.Result.Errors.Error {
					slog.Warn("CRS-26i: File symbol batch insert error",
						slog.String("symbol", fmt.Sprintf("%v", r.Properties)),
						slog.String("error", e.Message),
					)
				}
			}
		}
		indexed += len(batch) - batchErrors
	}

	span.SetAttributes(attribute.Int("rag.indexed", indexed))
	return indexed, nil
}

// buildSearchText creates a natural language description of a symbol for embedding.
//
// Description:
//
//	Combines the symbol's name, kind, package, signature, receiver, and doc comment
//	into a sentence that the embedding model can vectorize meaningfully.
//
//	CRS-26b: Enriched with receiver type (for method disambiguation) and doc comment
//	(for semantic matching on conceptual queries). Examples:
//	  "function FindHotspots in package pkg/materials: func FindHotspots(ctx context.Context) ([]Hotspot, error). FindHotspots identifies functions with high PageRank scores."
//	  "method (Router) ServeHTTP in package pkg/server: func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request). ServeHTTP dispatches incoming HTTP requests to registered route handlers."
//	  "struct Material in package pkg/materials"
//	  "interface Renderer in package pkg/render"
//
// Inputs:
//
//	sym - The symbol to describe. Must not be nil.
//
// Outputs:
//
//	string - Natural language description suitable for text embedding.
//
// BuildSearchText constructs a natural language description of a symbol
// suitable for text embedding. Exported for reuse by semantic search tools.
func BuildSearchText(sym *ast.Symbol) string {
	var sb strings.Builder
	sb.WriteString(sym.Kind.String())
	sb.WriteByte(' ')

	// CRS-26b: Include receiver type for methods (e.g., "(Router) ServeHTTP").
	if sym.Receiver != "" {
		sb.WriteByte('(')
		sb.WriteString(sym.Receiver)
		sb.WriteString(") ")
	}

	sb.WriteString(sym.Name)
	if sym.Package != "" {
		sb.WriteString(" in package ")
		sb.WriteString(sym.Package)
	}
	if sym.Signature != "" {
		sb.WriteString(": ")
		sb.WriteString(sym.Signature)
	}

	// CRS-26b: Append doc comment (first sentence, max 200 chars) for semantic richness.
	if doc := truncateDocComment(sym.DocComment); doc != "" {
		sb.WriteString(". ")
		sb.WriteString(doc)
	}

	return sb.String()
}

// truncateDocComment extracts the first sentence from a doc comment, bounded to 200 chars.
//
// Description:
//
//	Long doc comments dilute the embedding signal. Takes the first sentence
//	(period followed by space or end of string) or first 200 characters,
//	whichever is shorter. Strips leading comment markers (// and #).
//
// Inputs:
//
//	doc - The raw doc comment string. May be empty.
//
// Outputs:
//
//	string - The truncated comment, or empty if input is empty/whitespace.
func truncateDocComment(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}

	// Strip common comment prefixes line by line is unnecessary — AST parsers
	// typically strip these. But handle the case where they don't.
	for strings.HasPrefix(doc, "// ") {
		doc = strings.TrimPrefix(doc, "// ")
	}
	for strings.HasPrefix(doc, "# ") {
		doc = strings.TrimPrefix(doc, "# ")
	}
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}

	// Take first sentence (period followed by space or end of string).
	if idx := strings.Index(doc, ". "); idx >= 0 && idx < 200 {
		return doc[:idx+1]
	}

	// No mid-text sentence boundary. Check if the whole thing ends with a period.
	if len(doc) <= 200 {
		return doc
	}

	// Truncate at word boundary before 200 chars.
	if idx := strings.LastIndex(doc[:200], " "); idx > 100 {
		return doc[:idx]
	}
	return doc[:200]
}
