// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/weaviate/weaviate/entities/models"
)

func TestFindSimilarSymbolsTool_Name(t *testing.T) {
	tool := NewFindSimilarSymbolsTool(nil, "test", nil, nil)
	if tool.Name() != "find_similar_symbols" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "find_similar_symbols")
	}
}

func TestFindSimilarSymbolsTool_Category(t *testing.T) {
	tool := NewFindSimilarSymbolsTool(nil, "test", nil, nil)
	if tool.Category() != CategorySemantic {
		t.Errorf("Category() = %s, want %s", tool.Category(), CategorySemantic)
	}
}

func TestFindSimilarSymbolsTool_Definition(t *testing.T) {
	tool := NewFindSimilarSymbolsTool(nil, "test", nil, nil)
	def := tool.Definition()

	t.Run("has required parameters", func(t *testing.T) {
		if _, ok := def.Parameters["symbol_name"]; !ok {
			t.Error("missing 'symbol_name' parameter")
		}
		if !def.Parameters["symbol_name"].Required {
			t.Error("'symbol_name' parameter should be required")
		}
	})

	t.Run("requires graph_initialized", func(t *testing.T) {
		found := false
		for _, req := range def.Requires {
			if req == "graph_initialized" {
				found = true
				break
			}
		}
		if !found {
			t.Error("definition should require 'graph_initialized'")
		}
	})
}

func TestFindSimilarSymbolsTool_Execute_NilClient(t *testing.T) {
	t.Run("nil dependencies", func(t *testing.T) {
		tool := NewFindSimilarSymbolsTool(nil, "test", nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "parseConfig",
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure when dependencies are nil")
		}
		if result.Error == "" {
			t.Error("expected error message")
		}
	})
}

func TestFindSimilarSymbolsTool_Execute_SymbolNotFound(t *testing.T) {
	// Create an empty index
	idx := index.NewSymbolIndex()

	tool := NewFindSimilarSymbolsTool(nil, "test", nil, idx)
	ctx := context.Background()

	// Even with nil wvClient, the tool should check index first
	// and return "not found" before hitting weaviate
	// But since wvClient is nil, it will return graceful degradation first
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "nonexistent",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With nil wvClient, it will return the degradation error
	if result.Success {
		t.Error("expected failure when weaviate client is nil")
	}
}

func TestFindSimilarSymbolsTool_ParseParams(t *testing.T) {
	tool := &findSimilarSymbolsTool{}

	t.Run("defaults", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"symbol_name": "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 10 {
			t.Errorf("default limit = %d, want 10", p.Limit)
		}
	})

	t.Run("custom limit", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{
			"symbol_name": "parseConfig",
			"limit":       25,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.SymbolName != "parseConfig" {
			t.Errorf("symbol_name = %q, want %q", p.SymbolName, "parseConfig")
		}
		if p.Limit != 25 {
			t.Errorf("limit = %d, want 25", p.Limit)
		}
	})

	t.Run("limit clamped to 50", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"symbol_name": "test", "limit": 100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 50 {
			t.Errorf("limit = %d, want 50 (clamped)", p.Limit)
		}
	})

	t.Run("missing symbol_name returns error", func(t *testing.T) {
		_, err := tool.parseParams(map[string]any{})
		if err == nil {
			t.Error("expected error for missing symbol_name")
		}
	})
}

func TestFindSimilarSymbolsTool_ParseResults(t *testing.T) {
	tool := &findSimilarSymbolsTool{}

	sourceSym := &ast.Symbol{
		ID:       "test.go:1:parseConfig",
		Name:     "parseConfig",
		Kind:     ast.SymbolKindFunction,
		FilePath: "test.go",
	}

	t.Run("nil response", func(t *testing.T) {
		output := tool.parseResults(sourceSym, nil)
		if output.ResultCount != 0 {
			t.Errorf("expected 0 results, got %d", output.ResultCount)
		}
		if output.SourceSymbol != "parseConfig" {
			t.Errorf("source_symbol = %q, want %q", output.SourceSymbol, "parseConfig")
		}
	})

	t.Run("valid response", func(t *testing.T) {
		resp := &models.GraphQLResponse{
			Data: makeGraphQLData(map[string]interface{}{
				"CodeSymbol": []interface{}{
					map[string]interface{}{
						"name":        "loadConfig",
						"kind":        "function",
						"packagePath": "config",
						"filePath":    "config/loader.go",
						"_additional": map[string]interface{}{
							"distance": 0.1,
						},
					},
				},
			}),
		}

		output := tool.parseResults(sourceSym, resp)
		if output.ResultCount != 1 {
			t.Errorf("expected 1 result, got %d", output.ResultCount)
		}
		if output.Results[0].Name != "loadConfig" {
			t.Errorf("result name = %q, want %q", output.Results[0].Name, "loadConfig")
		}
		if output.Results[0].Score != 0.9 {
			t.Errorf("result score = %f, want 0.9", output.Results[0].Score)
		}
	})
}

// TestBuildSymbolSearchText removed — rag.BuildSearchText is tested in
// services/trace/rag/symbol_store_test.go with 7 comprehensive test cases.
