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
	"fmt"
	"log/slog"

	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

// CodeSymbolClassName is the Weaviate class name for code symbols.
const CodeSymbolClassName = "CodeSymbol"

// GetCodeSymbolSchema returns the Weaviate schema for the CodeSymbol class.
//
// Description:
//
//	Defines the schema for storing code symbols in Weaviate for semantic search.
//	Uses text2vec-ollama to vectorize the searchText field, which contains
//	a natural language description of the symbol (name, kind, package, signature).
//	Other fields are indexed for filtering but not vectorized.
//
// Outputs:
//
//	*models.Class - The Weaviate class definition.
//
// Thread Safety: Stateless, safe for concurrent use.
func GetCodeSymbolSchema() *models.Class {
	indexFilterable := new(bool)
	*indexFilterable = true

	indexSearchable := new(bool)
	*indexSearchable = true

	skipVectorization := new(bool)
	*skipVectorization = true

	skipModule := map[string]interface{}{
		"text2vec-ollama": map[string]interface{}{
			"skip": true,
		},
	}

	return &models.Class{
		Class:       CodeSymbolClassName,
		Description: "Code symbols from AST graph for semantic entity resolution",
		Vectorizer:  "text2vec-ollama",
		ModuleConfig: map[string]interface{}{
			"text2vec-ollama": map[string]interface{}{
				"vectorizeClassName": false,
				"model":              "nomic-embed-text-v2-moe",
			},
		},
		Properties: []*models.Property{
			{
				Name:            "symbolId",
				DataType:        []string{"text"},
				Description:     "Unique symbol ID from the AST graph",
				IndexFilterable: indexFilterable,
				Tokenization:    "field",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "searchText",
				DataType:        []string{"text"},
				Description:     "Natural language description for semantic search",
				IndexSearchable: indexSearchable,
				Tokenization:    "word",
				// searchText IS vectorized — this is the main search field.
			},
			{
				Name:            "name",
				DataType:        []string{"text"},
				Description:     "Symbol name (e.g., FindHotspots)",
				IndexFilterable: indexFilterable,
				IndexSearchable: indexSearchable,
				Tokenization:    "word",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "kind",
				DataType:        []string{"text"},
				Description:     "Symbol kind: function, method, struct, interface, etc.",
				IndexFilterable: indexFilterable,
				Tokenization:    "field",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "packagePath",
				DataType:        []string{"text"},
				Description:     "Package path (e.g., pkg/materials)",
				IndexFilterable: indexFilterable,
				IndexSearchable: indexSearchable,
				Tokenization:    "word",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "filePath",
				DataType:        []string{"text"},
				Description:     "Source file path",
				IndexFilterable: indexFilterable,
				Tokenization:    "field",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "exported",
				DataType:        []string{"boolean"},
				Description:     "Whether the symbol is exported",
				IndexFilterable: indexFilterable,
				ModuleConfig:    skipModule,
			},
			{
				Name:            "dataSpace",
				DataType:        []string{"text"},
				Description:     "Project isolation key",
				IndexFilterable: indexFilterable,
				Tokenization:    "field",
				ModuleConfig:    skipModule,
			},
			{
				Name:            "graphHash",
				DataType:        []string{"text"},
				Description:     "Hash of the graph state when this symbol was indexed",
				IndexFilterable: indexFilterable,
				Tokenization:    "field",
				ModuleConfig:    skipModule,
			},
		},
	}
}

// EnsureCodeSymbolSchema creates the CodeSymbol class if it doesn't exist.
//
// Description:
//
//	Checks if the CodeSymbol class exists in Weaviate and creates it if not.
//	Idempotent — safe to call on every startup.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	client - Weaviate client. Must not be nil.
//
// Outputs:
//
//	error - Non-nil if schema creation fails.
//
// Thread Safety: Safe for concurrent use.
func EnsureCodeSymbolSchema(ctx context.Context, client *weaviate.Client) error {
	existing, err := client.Schema().ClassGetter().WithClassName(CodeSymbolClassName).Do(ctx)
	if err == nil {
		// Class exists — verify the vectorizer matches what we need.
		// If a previous run created the class with "none" or "text2vec-transformers",
		// nearText won't work. Delete and recreate with the correct vectorizer.
		expectedVectorizer := GetCodeSymbolSchema().Vectorizer
		if existing.Vectorizer != expectedVectorizer {
			slog.Warn("CRS-25: CodeSymbol schema has wrong vectorizer, recreating",
				slog.String("current", existing.Vectorizer),
				slog.String("expected", expectedVectorizer))
			if delErr := client.Schema().ClassDeleter().WithClassName(CodeSymbolClassName).Do(ctx); delErr != nil {
				return fmt.Errorf("deleting stale CodeSymbol schema: %w", delErr)
			}
		} else {
			slog.Debug("CRS-25: CodeSymbol schema already exists",
				slog.String("vectorizer", existing.Vectorizer))
			return nil
		}
	}

	schema := GetCodeSymbolSchema()
	slog.Info("CRS-25: Creating CodeSymbol schema",
		slog.String("vectorizer", schema.Vectorizer))
	if err := client.Schema().ClassCreator().WithClass(schema).Do(ctx); err != nil {
		return fmt.Errorf("creating CodeSymbol schema: %w", err)
	}

	slog.Info("CRS-25: CodeSymbol schema created",
		slog.String("vectorizer", schema.Vectorizer))
	return nil
}
