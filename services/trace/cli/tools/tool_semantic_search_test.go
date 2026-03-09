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

	"github.com/weaviate/weaviate/entities/models"
)

func TestSemanticSearchTool_Name(t *testing.T) {
	tool := NewSemanticSearchTool(nil, "test", nil)
	if tool.Name() != "semantic_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "semantic_search")
	}
}

func TestSemanticSearchTool_Category(t *testing.T) {
	tool := NewSemanticSearchTool(nil, "test", nil)
	if tool.Category() != CategorySemantic {
		t.Errorf("Category() = %s, want %s", tool.Category(), CategorySemantic)
	}
}

func TestSemanticSearchTool_Definition(t *testing.T) {
	tool := NewSemanticSearchTool(nil, "test", nil)
	def := tool.Definition()

	t.Run("has required parameters", func(t *testing.T) {
		if _, ok := def.Parameters["query"]; !ok {
			t.Error("missing 'query' parameter")
		}
		if !def.Parameters["query"].Required {
			t.Error("'query' parameter should be required")
		}
	})

	t.Run("has optional parameters", func(t *testing.T) {
		if _, ok := def.Parameters["limit"]; !ok {
			t.Error("missing 'limit' parameter")
		}
		if _, ok := def.Parameters["min_score"]; !ok {
			t.Error("missing 'min_score' parameter")
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

func TestSemanticSearchTool_Execute_NilClient(t *testing.T) {
	t.Run("nil weaviate client", func(t *testing.T) {
		tool := NewSemanticSearchTool(nil, "test", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"query": "authentication",
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure when weaviate client is nil")
		}
		if result.Error == "" {
			t.Error("expected error message")
		}
	})
}

func TestSemanticSearchTool_Execute_MissingQuery(t *testing.T) {
	tool := NewSemanticSearchTool(nil, "test", nil)
	ctx := context.Background()

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure when query is missing")
	}
}

func TestSemanticSearchTool_ParseParams(t *testing.T) {
	tool := &semanticSearchTool{}

	t.Run("defaults", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"query": "test"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 10 {
			t.Errorf("default limit = %d, want 10", p.Limit)
		}
		if p.MinScore != 0.5 {
			t.Errorf("default min_score = %f, want 0.5", p.MinScore)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{
			"query":     "authentication",
			"limit":     20,
			"min_score": 0.7,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Query != "authentication" {
			t.Errorf("query = %q, want %q", p.Query, "authentication")
		}
		if p.Limit != 20 {
			t.Errorf("limit = %d, want 20", p.Limit)
		}
		if p.MinScore != 0.7 {
			t.Errorf("min_score = %f, want 0.7", p.MinScore)
		}
	})

	t.Run("limit clamped to 50", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"query": "test", "limit": 100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.Limit != 50 {
			t.Errorf("limit = %d, want 50 (clamped)", p.Limit)
		}
	})

	t.Run("min_score clamped to 1.0", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"query": "test", "min_score": 2.0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.MinScore != 1.0 {
			t.Errorf("min_score = %f, want 1.0 (clamped)", p.MinScore)
		}
	})

	t.Run("empty query returns error", func(t *testing.T) {
		_, err := tool.parseParams(map[string]any{"query": ""})
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

func TestSemanticSearchTool_ParseResults(t *testing.T) {
	tool := &semanticSearchTool{}

	t.Run("nil response", func(t *testing.T) {
		output := tool.parseResults("test", nil)
		if output.ResultCount != 0 {
			t.Errorf("expected 0 results, got %d", output.ResultCount)
		}
	})

	t.Run("empty response", func(t *testing.T) {
		resp := &models.GraphQLResponse{
			Data: makeGraphQLData(nil),
		}
		output := tool.parseResults("test", resp)
		if output.ResultCount != 0 {
			t.Errorf("expected 0 results, got %d", output.ResultCount)
		}
	})

	t.Run("valid response", func(t *testing.T) {
		resp := &models.GraphQLResponse{
			Data: makeGraphQLData(map[string]interface{}{
				"CodeSymbol": []interface{}{
					map[string]interface{}{
						"name":        "parseConfig",
						"kind":        "function",
						"packagePath": "config",
						"filePath":    "config/parser.go",
						"_additional": map[string]interface{}{
							"distance": 0.15,
						},
					},
					map[string]interface{}{
						"name":        "loadConfig",
						"kind":        "function",
						"packagePath": "config",
						"filePath":    "config/loader.go",
						"_additional": map[string]interface{}{
							"distance": 0.25,
						},
					},
				},
			}),
		}

		output := tool.parseResults("configuration parsing", resp)
		if output.ResultCount != 2 {
			t.Errorf("expected 2 results, got %d", output.ResultCount)
		}
		if output.Results[0].Name != "parseConfig" {
			t.Errorf("first result name = %q, want %q", output.Results[0].Name, "parseConfig")
		}
		if output.Results[0].Score != 0.85 {
			t.Errorf("first result score = %f, want 0.85", output.Results[0].Score)
		}
	})
}

func TestSemanticSearchTool_FormatText(t *testing.T) {
	tool := &semanticSearchTool{}

	t.Run("empty results", func(t *testing.T) {
		output := SemanticSearchOutput{Query: "test", ResultCount: 0, Results: nil}
		text := tool.formatText(output)
		if text == "" {
			t.Error("expected non-empty text for empty results")
		}
	})

	t.Run("with results", func(t *testing.T) {
		output := SemanticSearchOutput{
			Query:       "authentication",
			ResultCount: 1,
			Results: []SemanticSearchResult{
				{Name: "authenticate", Kind: "function", Score: 0.9},
			},
		}
		text := tool.formatText(output)
		if text == "" {
			t.Error("expected non-empty formatted text")
		}
	})
}

// makeGraphQLData constructs a models.GraphQLResponse.Data map.
// models.JSONObject is interface{}, so we can't use composite literal syntax.
func makeGraphQLData(get map[string]interface{}) map[string]models.JSONObject {
	data := make(map[string]models.JSONObject)
	if get != nil {
		data["Get"] = get
	}
	return data
}
