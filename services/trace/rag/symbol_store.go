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
// Thread Safety: Safe for concurrent use.
type SymbolStore struct {
	client    *weaviate.Client
	dataSpace string
}

// NewSymbolStore creates a new symbol store.
//
// Inputs:
//
//	client - Weaviate client. Must not be nil.
//	dataSpace - Project isolation key. Must not be empty.
//
// Outputs:
//
//	*SymbolStore - The configured store.
//	error - Non-nil if client is nil or dataSpace is empty.
//
// Thread Safety: Safe for concurrent use after construction.
func NewSymbolStore(client *weaviate.Client, dataSpace string) (*SymbolStore, error) {
	if client == nil {
		return nil, errors.New("client must not be nil")
	}
	if dataSpace == "" {
		return nil, errors.New("dataSpace must not be empty")
	}
	return &SymbolStore{
		client:    client,
		dataSpace: dataSpace,
	}, nil
}

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
//
// Outputs:
//
//	int - Number of symbols indexed.
//	error - Non-nil if batch insert fails.
//
// Thread Safety: Safe for concurrent use.
func (s *SymbolStore) IndexSymbols(ctx context.Context, idx *index.SymbolIndex, graphHash string) (int, error) {
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

			searchText := buildSearchText(sym)
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

	// Batch insert in chunks of 100.
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
			return indexed, fmt.Errorf("batch insert symbols (offset %d): %w", i, err)
		}

		batchErrors := 0
		for _, r := range resp {
			if r.Result != nil && r.Result.Errors != nil && len(r.Result.Errors.Error) > 0 {
				batchErrors++
				for _, e := range r.Result.Errors.Error {
					slog.Warn("CRS-25: Symbol batch insert error",
						slog.String("symbol", fmt.Sprintf("%v", r.Properties)),
						slog.String("error", e.Message),
					)
				}
			}
		}
		indexed += len(batch) - batchErrors
	}

	span.SetAttributes(attribute.Int("rag.indexed", indexed))
	slog.Info("CRS-25: Symbols indexed in Weaviate",
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

	where := filters.Where().
		WithPath([]string{"dataSpace"}).
		WithOperator(filters.Equal).
		WithValueString(s.dataSpace)

	_, err := s.client.Batch().ObjectsBatchDeleter().
		WithClassName(CodeSymbolClassName).
		WithWhere(where).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("deleting all symbols for dataSpace %s: %w", s.dataSpace, err)
	}

	slog.Info("CRS-25: All symbols deleted for re-index",
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

// buildSearchText creates a natural language description of a symbol for embedding.
//
// Description:
//
//	Combines the symbol's name, kind, package, and signature into a sentence
//	that text2vec-transformers can vectorize meaningfully. Examples:
//	  "function FindHotspots in package pkg/materials: func FindHotspots(ctx context.Context) ([]Hotspot, error)"
//	  "struct Material in package pkg/materials"
//	  "interface Renderer in package pkg/render"
func buildSearchText(sym *ast.Symbol) string {
	var sb strings.Builder
	sb.WriteString(sym.Kind.String())
	sb.WriteByte(' ')
	sb.WriteString(sym.Name)
	if sym.Package != "" {
		sb.WriteString(" in package ")
		sb.WriteString(sym.Package)
	}
	if sym.Signature != "" {
		sb.WriteString(": ")
		sb.WriteString(sym.Signature)
	}
	return sb.String()
}
