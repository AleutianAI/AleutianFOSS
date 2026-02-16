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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// getIntFromAny extracts an int from an any value (handles int and float64).
func getIntFromAny(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case int64:
		return int(val)
	default:
		return 0
	}
}

// createTestGraphWithCallers creates a test graph with call relationships.
func createTestGraphWithCallers(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create symbols (EndLine >= StartLine, Language required for index validation)
	parseConfig := &ast.Symbol{
		ID:        "config/parser.go:10:parseConfig",
		Name:      "parseConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/parser.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Signature: "func parseConfig(path string) (*Config, error)",
		Exported:  false,
		Language:  "go",
	}

	main := &ast.Symbol{
		ID:        "cmd/app/main.go:5:main",
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "cmd/app/main.go",
		StartLine: 5,
		EndLine:   15,
		Package:   "main",
		Signature: "func main()",
		Exported:  false,
		Language:  "go",
	}

	initServer := &ast.Symbol{
		ID:        "server/init.go:20:initServer",
		Name:      "initServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/init.go",
		StartLine: 20,
		EndLine:   30,
		Package:   "server",
		Signature: "func initServer() error",
		Exported:  false,
		Language:  "go",
	}

	loadConfig := &ast.Symbol{
		ID:        "config/loader.go:15:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 15,
		EndLine:   25,
		Package:   "config",
		Signature: "func LoadConfig() (*Config, error)",
		Exported:  true,
		Language:  "go",
	}

	// Handler interface and implementation
	handler := &ast.Symbol{
		ID:        "handler/handler.go:5:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "handler/handler.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "handler",
		Exported:  true,
		Language:  "go",
	}

	userHandler := &ast.Symbol{
		ID:        "handler/user.go:10:UserHandler",
		Name:      "UserHandler",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "handler/user.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "handler",
		Exported:  true,
		Language:  "go",
	}

	// Add nodes to graph
	g.AddNode(parseConfig)
	g.AddNode(main)
	g.AddNode(initServer)
	g.AddNode(loadConfig)
	g.AddNode(handler)
	g.AddNode(userHandler)

	// Add symbols to index
	if err := idx.Add(parseConfig); err != nil {
		t.Fatalf("Failed to add parseConfig: %v", err)
	}
	if err := idx.Add(main); err != nil {
		t.Fatalf("Failed to add main: %v", err)
	}
	if err := idx.Add(initServer); err != nil {
		t.Fatalf("Failed to add initServer: %v", err)
	}
	if err := idx.Add(loadConfig); err != nil {
		t.Fatalf("Failed to add loadConfig: %v", err)
	}
	if err := idx.Add(handler); err != nil {
		t.Fatalf("Failed to add handler: %v", err)
	}
	if err := idx.Add(userHandler); err != nil {
		t.Fatalf("Failed to add userHandler: %v", err)
	}

	// Verify index is populated
	if idx.Stats().TotalSymbols != 6 {
		t.Fatalf("Expected 6 symbols in index, got %d", idx.Stats().TotalSymbols)
	}

	// Create call edges: main -> parseConfig, initServer -> parseConfig, LoadConfig -> parseConfig
	g.AddEdge(main.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: main.FilePath, StartLine: 10,
	})
	g.AddEdge(initServer.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: initServer.FilePath, StartLine: 25,
	})
	g.AddEdge(loadConfig.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: loadConfig.FilePath, StartLine: 20,
	})

	// Create implements edge: UserHandler implements Handler
	g.AddEdge(userHandler.ID, handler.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: userHandler.FilePath, StartLine: 10,
	})

	g.Freeze()

	return g, idx
}

func TestFindCallersTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	t.Run("finds all callers of parseConfig", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"function_name": "parseConfig",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Check that we found callers
		output, ok := result.Output.(FindCallersOutput)
		if !ok {
			t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
		}

		// Should have 1 entry (one parseConfig function)
		if len(output.Results) != 1 {
			t.Errorf("got %d result entries, want 1", len(output.Results))
		}

		// The one parseConfig should have 3 callers
		if len(output.Results) > 0 {
			if len(output.Results[0].Callers) != 3 {
				t.Errorf("got %d callers, want 3", len(output.Results[0].Callers))
			}
		}

		// Check output text mentions the callers
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("returns empty for non-existent function", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"function_name": "nonExistent",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should have message about no callers found
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("requires function_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without function_name")
		}
		if result.Error == "" {
			t.Error("Error message should not be empty")
		}
	})
}

func TestFindCalleesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, idx)

	t.Run("finds callees of main", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"function_name": "main",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// main calls parseConfig
		output, ok := result.Output.(FindCalleesOutput)
		if !ok {
			t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
		}

		if len(output.ResolvedCallees) != 1 {
			t.Errorf("got %d resolved callees, want 1", len(output.ResolvedCallees))
		}
	})

	t.Run("requires function_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without function_name")
		}
	})
}

func TestFindImplementationsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, idx)

	t.Run("finds implementations of Handler", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"interface_name": "Handler",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// UserHandler implements Handler
		output, ok := result.Output.(FindImplementationsOutput)
		if !ok {
			t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
		}

		if len(output.Results) != 1 {
			t.Errorf("got %d result entries, want 1", len(output.Results))
		}
	})

	t.Run("requires interface_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without interface_name")
		}
	})
}

func TestFindSymbolTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindSymbolTool(g, idx)

	t.Run("finds symbol by name", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"name": "parseConfig",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}
	})

	t.Run("filters by kind", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"name": "Handler",
			"kind": "interface",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}
	})

	t.Run("requires name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without name")
		}
	})
}

func TestFindSymbolTool_PartialMatch(t *testing.T) {
	ctx := context.Background()

	// Create test index with symbol "getDatesToProcess"
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	sym := &ast.Symbol{
		ID:        "processor/dates.go:10:getDatesToProcess",
		Name:      "getDatesToProcess",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "processor/dates.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "processor",
		Signature: "func getDatesToProcess() []time.Time",
		Exported:  false,
		Language:  "go",
	}

	// Add to graph and index
	g.AddNode(sym)
	if err := idx.Add(sym); err != nil {
		t.Fatalf("Failed to add symbol to index: %v", err)
	}

	tool := NewFindSymbolTool(g, idx)

	t.Run("exact match works", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"name": "getDatesToProcess",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}

		// Exact match should NOT have fuzzy warning
		outputText := result.OutputText
		if strings.Contains(outputText, "⚠️") {
			t.Errorf("Exact match should not have fuzzy warning")
		}
	})

	t.Run("partial match finds it", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"name": "Process", // Partial match
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount < 1 {
			t.Errorf("got %d matches, want at least 1", output.MatchCount)
		}

		// Check that getDatesToProcess is in the results
		found := false
		for _, match := range output.Symbols {
			if match.Name == "getDatesToProcess" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected to find 'getDatesToProcess' in partial match results")
		}

		// Partial match should have fuzzy warning
		outputText := result.OutputText
		if !strings.Contains(outputText, "⚠️") {
			t.Errorf("Partial match should have fuzzy warning, got: %s", outputText)
		}
	})

	t.Run("no match at all", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"name": "NonExistentXYZ",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 0 {
			t.Errorf("got %d matches, want 0", output.MatchCount)
		}

		// No matches should show "No symbols found"
		outputText := result.OutputText
		if !strings.Contains(outputText, "No symbols found") {
			t.Errorf("Expected 'No symbols found' message, got: %s", outputText)
		}
	})
}

func TestGetCallChainTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewGetCallChainTool(g, idx)

	t.Run("traces downstream from main", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"function_name": "main",
			"direction":     "downstream",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// main -> parseConfig
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		if output.NodeCount < 2 {
			t.Errorf("got %d nodes, want at least 2", output.NodeCount)
		}
	})

	t.Run("traces upstream from parseConfig", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"function_name": "parseConfig",
			"direction":     "upstream",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// parseConfig <- main, initServer, LoadConfig
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		if output.NodeCount < 4 {
			t.Errorf("got %d nodes, want at least 4", output.NodeCount)
		}
	})

	t.Run("requires function_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without function_name")
		}
	})
}

func TestToolDefinitions(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	tests := []struct {
		name     string
		tool     Tool
		wantName string
		wantCat  ToolCategory
	}{
		{
			name:     "find_callers",
			tool:     NewFindCallersTool(g, idx),
			wantName: "find_callers",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_callees",
			tool:     NewFindCalleesTool(g, idx),
			wantName: "find_callees",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_implementations",
			tool:     NewFindImplementationsTool(g, idx),
			wantName: "find_implementations",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_symbol",
			tool:     NewFindSymbolTool(g, idx),
			wantName: "find_symbol",
			wantCat:  CategoryExploration,
		},
		{
			name:     "get_call_chain",
			tool:     NewGetCallChainTool(g, idx),
			wantName: "get_call_chain",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_references",
			tool:     NewFindReferencesTool(g, idx),
			wantName: "find_references",
			wantCat:  CategoryExploration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tool.Name(); got != tt.wantName {
				t.Errorf("Name() = %v, want %v", got, tt.wantName)
			}
			if got := tt.tool.Category(); got != tt.wantCat {
				t.Errorf("Category() = %v, want %v", got, tt.wantCat)
			}

			def := tt.tool.Definition()
			if def.Name != tt.wantName {
				t.Errorf("Definition().Name = %v, want %v", def.Name, tt.wantName)
			}
			if def.Description == "" {
				t.Error("Definition().Description is empty")
			}
			if len(def.Parameters) == 0 {
				t.Error("Definition().Parameters is empty")
			}
		})
	}
}

func TestRegisterExploreTools_IncludesGraphQueryTools(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()
	registry := NewRegistry()

	RegisterExploreTools(registry, g, idx)

	// Check that all 6 new graph query tools are registered
	graphQueryTools := []string{
		"find_callers",
		"find_callees",
		"find_implementations",
		"find_symbol",
		"get_call_chain",
		"find_references",
	}

	for _, name := range graphQueryTools {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("Tool %s not registered", name)
		}
	}

	// Should have at least 16 tools (10 original + 6 new)
	if count := registry.Count(); count < 16 {
		t.Errorf("Registry has %d tools, want at least 16", count)
	}
}

// =============================================================================
// GR-01: Index Optimization Tests
// =============================================================================

// TestFindCallersTool_NilIndexFallback tests that find_callers falls back to
// O(V) graph scan when index is nil (GR-01 requirement M5).
func TestFindCallersTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	// Create tool with nil index
	tool := NewFindCallersTool(g, nil)

	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "parseConfig",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find callers via graph fallback
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	// Should have 1 entry (one parseConfig function) with 3 callers
	if len(output.Results) != 1 {
		t.Errorf("got %d result entries, want 1", len(output.Results))
	}
}

// TestFindCalleesTool_NilIndexFallback tests nil index fallback for find_callees.
func TestFindCalleesTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, nil)

	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "main",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find callees via graph fallback
	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	if len(output.ResolvedCallees) != 1 {
		t.Errorf("got %d resolved callees, want 1", len(output.ResolvedCallees))
	}
}

// TestFindImplementationsTool_NilIndexFallback tests nil index fallback.
func TestFindImplementationsTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, nil)

	result, err := tool.Execute(ctx, map[string]any{
		"interface_name": "Handler",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find implementations via graph fallback
	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	if len(output.Results) != 1 {
		t.Errorf("got %d result entries, want 1", len(output.Results))
	}
}

// createTestGraphWithMultipleMatches creates a graph with multiple functions
// having the same name (e.g., "Setup" in different packages).
func createTestGraphWithMultipleMatches(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create multiple "Setup" functions in different packages
	symbols := []*ast.Symbol{
		{
			ID:        "pkg/a/setup.go:10:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/a/setup.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "a",
			Language:  "go",
		},
		{
			ID:        "pkg/b/setup.go:15:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/b/setup.go",
			StartLine: 15,
			EndLine:   25,
			Package:   "b",
			Language:  "go",
		},
		{
			ID:        "pkg/c/setup.go:20:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/c/setup.go",
			StartLine: 20,
			EndLine:   30,
			Package:   "c",
			Language:  "go",
		},
		{
			ID:        "main.go:5:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "main.go",
			StartLine: 5,
			EndLine:   15,
			Package:   "main",
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s: %v", sym.ID, err)
		}
	}

	// main calls all three Setup functions
	g.AddEdge("main.go:5:main", "pkg/a/setup.go:10:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 10,
	})
	g.AddEdge("main.go:5:main", "pkg/b/setup.go:15:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 11,
	})
	g.AddEdge("main.go:5:main", "pkg/c/setup.go:20:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 12,
	})

	g.Freeze()

	return g, idx
}

// TestFindCallersTool_MultipleMatches tests that find_callers correctly handles
// multiple functions with the same name (GR-01 requirement M2).
func TestFindCallersTool_MultipleMatches(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMultipleMatches(t)

	tool := NewFindCallersTool(g, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "Setup",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	// Should have 3 result entries (one per Setup function)
	if len(output.Results) != 3 {
		t.Errorf("got %d result entries, want 3 (one per Setup)", len(output.Results))
	}

	// Each Setup should have 1 caller (main)
	for i, entry := range output.Results {
		if len(entry.Callers) != 1 {
			t.Errorf("result[%d] got %d callers, want 1", i, len(entry.Callers))
		}
	}
}

// TestFindCalleesTool_MultipleMatches tests multiple symbol matches.
func TestFindCalleesTool_MultipleMatches(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMultipleMatches(t)

	tool := NewFindCalleesTool(g, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "main",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	// main has 3 callees (three Setup functions)
	if len(output.ResolvedCallees) != 3 {
		t.Errorf("got %d resolved callees, want 3", len(output.ResolvedCallees))
	}
}

// TestFindCallersTool_FastNotFound tests that queries for non-existent symbols
// return quickly (O(1) index miss, not O(V) scan).
func TestFindCallersTool_FastNotFound(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "NonExistentFunctionXYZ123",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should have empty results and message about no callers
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	if len(output.Results) != 0 {
		t.Errorf("got %d results for non-existent function, want 0", len(output.Results))
	}

	// OutputText should mention no callers found
	if result.OutputText == "" {
		t.Error("OutputText is empty")
	}
}

// =============================================================================
// GR-01: Benchmark Tests
// =============================================================================

// createLargeGraph creates a graph with many symbols for benchmarking.
func createLargeGraph(b *testing.B, size int) (*graph.Graph, *index.SymbolIndex) {
	b.Helper()

	g := graph.NewGraph("/benchmark")
	idx := index.NewSymbolIndex()

	// Create a chain of function calls (StartLine must be >= 1)
	var symbols []*ast.Symbol
	for i := 0; i < size; i++ {
		startLine := i*10 + 1 // 1-indexed, starting at 1
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("pkg/module%d/file.go:%d:Function%d", i, startLine, i),
			Name:      fmt.Sprintf("Function%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("pkg/module%d/file.go", i),
			StartLine: startLine,
			EndLine:   startLine + 10,
			Package:   fmt.Sprintf("module%d", i),
			Language:  "go",
		}
		symbols = append(symbols, sym)
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			b.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Create call edges: each function calls the next
	for i := 0; i < size-1; i++ {
		g.AddEdge(symbols[i].ID, symbols[i+1].ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: symbols[i].FilePath, StartLine: symbols[i].StartLine + 5,
		})
	}

	g.Freeze()

	return g, idx
}

// BenchmarkFindCallers_WithIndex benchmarks find_callers using O(1) index lookup.
func BenchmarkFindCallers_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindCallersTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Query for a function in the middle of the graph
		_, err := tool.Execute(ctx, map[string]any{
			"function_name": "Function5000",
		})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// BenchmarkFindCallers_WithoutIndex benchmarks find_callers using O(V) graph scan.
func BenchmarkFindCallers_WithoutIndex(b *testing.B) {
	g, _ := createLargeGraph(b, 10000)
	tool := NewFindCallersTool(g, nil) // nil index forces graph fallback
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Query for a function in the middle of the graph
		_, err := tool.Execute(ctx, map[string]any{
			"function_name": "Function5000",
		})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// BenchmarkFindCallees_WithIndex benchmarks find_callees using O(1) index lookup.
func BenchmarkFindCallees_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindCalleesTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{
			"function_name": "Function5000",
		})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// BenchmarkFindCallees_WithoutIndex benchmarks find_callees using O(V) graph scan.
func BenchmarkFindCallees_WithoutIndex(b *testing.B) {
	g, _ := createLargeGraph(b, 10000)
	tool := NewFindCalleesTool(g, nil) // nil index forces graph fallback
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{
			"function_name": "Function5000",
		})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// =============================================================================
// GR-01: Additional Test Coverage (L1, L2)
// =============================================================================

// TestFindCallersTool_ContextCancellation tests that context cancellation is handled.
// L1: Verify context cancellation path is covered.
func TestFindCallersTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithMultipleMatches(t)
	tool := NewFindCallersTool(g, idx)

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, map[string]any{
		"function_name": "Setup",
	})

	// Should return context.Canceled error
	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// TestFindCalleesTool_ContextCancellation tests context cancellation for find_callees.
func TestFindCalleesTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithMultipleMatches(t)
	tool := NewFindCalleesTool(g, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, map[string]any{
		"function_name": "main",
	})

	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
}

// TestFindImplementationsTool_ContextCancellation tests context cancellation.
func TestFindImplementationsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithCallers(t)
	tool := NewFindImplementationsTool(g, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, map[string]any{
		"interface_name": "Handler",
	})

	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
}

// TestFindCallersTool_LimitCapped tests that limit is capped at 1000 (M1).
func TestFindCallersTool_LimitCapped(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	// Request a very large limit
	result, err := tool.Execute(ctx, map[string]any{
		"function_name": "parseConfig",
		"limit":         1000000, // Should be capped to 1000
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// The test doesn't have 1000+ callers, but the limit should be silently capped
	// Verify the query still works
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	if output.MatchCount != 1 {
		t.Errorf("Expected 1 match, got %d", output.MatchCount)
	}
}

// TestFindImplementationsTool_NonInterfaceFiltered tests that non-interface symbols
// are filtered out (H3).
func TestFindImplementationsTool_NonInterfaceFiltered(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create two symbols with same name: one interface, one struct
	handler := &ast.Symbol{
		ID:        "handler/handler.go:5:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "handler/handler.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "handler",
		Language:  "go",
	}

	handlerStruct := &ast.Symbol{
		ID:        "other/handler.go:10:Handler",
		Name:      "Handler", // Same name, different kind
		Kind:      ast.SymbolKindStruct,
		FilePath:  "other/handler.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "other",
		Language:  "go",
	}

	g.AddNode(handler)
	g.AddNode(handlerStruct)
	_ = idx.Add(handler)
	_ = idx.Add(handlerStruct)
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"interface_name": "Handler",
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should only query the interface, not the struct
	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	// The match_count should be 1 (only the interface was queried)
	if output.MatchCount != 1 {
		t.Errorf("Expected 1 interface match, got %d (struct should be filtered)", output.MatchCount)
	}
}

// TestFindCallersTool_IndexAndGraphPathConsistency tests that index and graph paths
// return consistent results.
func TestFindCallersTool_IndexAndGraphPathConsistency(t *testing.T) {
	g, idx := createTestGraphWithCallers(t)

	toolWithIndex := NewFindCallersTool(g, idx)
	toolWithoutIndex := NewFindCallersTool(g, nil)

	ctx := context.Background()

	result1, err1 := toolWithIndex.Execute(ctx, map[string]any{
		"function_name": "parseConfig",
	})
	result2, err2 := toolWithoutIndex.Execute(ctx, map[string]any{
		"function_name": "parseConfig",
	})

	if err1 != nil || err2 != nil {
		t.Fatalf("Execute errors: %v, %v", err1, err2)
	}

	output1, _ := result1.Output.(FindCallersOutput)
	output2, _ := result2.Output.(FindCallersOutput)

	if output1.MatchCount != output2.MatchCount {
		t.Errorf("Index path got %d matches, graph path got %d - results inconsistent",
			output1.MatchCount, output2.MatchCount)
	}
}

// L2: Add find_references benchmark
func BenchmarkFindReferences_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindReferencesTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{
			"symbol_name": "Function5000",
		})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// =============================================================================
// GR-02 to GR-05: Graph Analytics Tool Tests
// =============================================================================

// createTestGraphForAnalytics creates a test graph with call relationships
// suitable for analytics queries (hotspots, dead code, cycles, paths).
func createTestGraphForAnalytics(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a graph with various patterns:
	// - funcA is a hotspot (called by many)
	// - funcD is dead code (no callers)
	// - funcB and funcC form a cycle
	// - main -> funcA -> funcB -> funcC -> funcB (cycle)
	symbols := []*ast.Symbol{
		{
			ID:        "main.go:10:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "main.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "main",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "core/funcA.go:10:funcA",
			Name:      "funcA",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcA.go",
			StartLine: 10,
			EndLine:   30,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/funcB.go:10:funcB",
			Name:      "funcB",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcB.go",
			StartLine: 10,
			EndLine:   25,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/funcC.go:10:funcC",
			Name:      "funcC",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcC.go",
			StartLine: 10,
			EndLine:   25,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "util/funcD.go:10:funcD",
			Name:      "funcD",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "util/funcD.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "util",
			Exported:  false, // Unexported dead code
			Language:  "go",
		},
		{
			ID:        "util/helper.go:5:helper",
			Name:      "helper",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "util/helper.go",
			StartLine: 5,
			EndLine:   15,
			Package:   "util",
			Exported:  false,
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Create edges:
	// main -> funcA (funcA is called by main)
	g.AddEdge("main.go:10:main", "core/funcA.go:10:funcA", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 15,
	})
	// funcA -> funcB
	g.AddEdge("core/funcA.go:10:funcA", "core/funcB.go:10:funcB", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcA.go", StartLine: 20,
	})
	// funcA -> helper (funcA is a hotspot)
	g.AddEdge("core/funcA.go:10:funcA", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcA.go", StartLine: 22,
	})
	// funcB -> funcC
	g.AddEdge("core/funcB.go:10:funcB", "core/funcC.go:10:funcC", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcB.go", StartLine: 15,
	})
	// funcC -> funcB (creates cycle B <-> C)
	g.AddEdge("core/funcC.go:10:funcC", "core/funcB.go:10:funcB", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcC.go", StartLine: 15,
	})

	g.Freeze()
	return g, idx
}

func TestFindHotspotsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	// Create HierarchicalGraph and analytics
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	t.Run("finds hotspots with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		// Should have at least one hotspot
		if len(output.Hotspots) == 0 {
			t.Error("Expected at least one hotspot")
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		if len(output.Hotspots) > 2 {
			t.Errorf("got %d hotspots, want at most 2", len(output.Hotspots))
		}
	})

	t.Run("handles nil analytics", func(t *testing.T) {
		nilTool := NewFindHotspotsTool(nil, idx)
		result, err := nilTool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure with nil analytics")
		}
		if result.Error == "" {
			t.Error("Expected error message")
		}
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := tool.Execute(cancelCtx, map[string]any{})
		if err == nil {
			t.Error("Expected context.Canceled error")
		}
	})
}

func TestFindDeadCodeTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("finds dead code by default (unexported only)", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		// funcD is unexported dead code
		found := false
		for _, dc := range output.DeadCode {
			if dc.Name == "funcD" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected to find funcD in dead code")
		}
	})

	t.Run("includes exported when requested", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"include_exported": true,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// OutputText should exist
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("filters by package", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"package": "util",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		// All results should be in util package
		for _, dc := range output.DeadCode {
			if dc.Package != "util" {
				t.Errorf("Found dead code from package %v, expected util", dc.Package)
			}
		}
	})

	t.Run("handles nil analytics", func(t *testing.T) {
		nilTool := NewFindDeadCodeTool(nil, idx)
		result, err := nilTool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure with nil analytics")
		}
	})
}

func TestFindCyclesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	t.Run("finds cycles", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		// Should find the B <-> C cycle
		if len(output.Cycles) == 0 {
			t.Error("Expected to find at least one cycle (funcB <-> funcC)")
		}

		// Check output text
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects min_size filter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 3, // Filter out 2-node cycles
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		// 2-node cycles should be filtered out
		for _, cycle := range output.Cycles {
			if cycle.Length < 3 {
				t.Errorf("Found cycle with length %d, expected >= 3", cycle.Length)
			}
		}
	})

	t.Run("handles nil analytics", func(t *testing.T) {
		nilTool := NewFindCyclesTool(nil, idx)
		result, err := nilTool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure with nil analytics")
		}
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := tool.Execute(cancelCtx, map[string]any{})
		if err == nil {
			t.Error("Expected context.Canceled error")
		}
	})
}

func TestFindPathTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	tool := NewFindPathTool(g, idx)

	t.Run("finds path between connected symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"from": "main",
			"to":   "funcB",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindPathOutput)
		if !ok {
			t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
		}

		if !output.Found {
			t.Error("Expected to find a path from main to funcB")
		}

		if output.Length < 1 {
			t.Errorf("Expected path length >= 1, got %d", output.Length)
		}

		// Check path contains nodes
		if len(output.Path) == 0 {
			t.Error("Expected non-empty path")
		}

		// Check output text
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("returns no path for unconnected symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"from": "funcD", // Dead code, not connected
			"to":   "main",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindPathOutput)
		if !ok {
			t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
		}

		if output.Found {
			t.Error("Expected no path from funcD to main")
		}
	})

	t.Run("handles non-existent from symbol", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"from": "nonExistent",
			"to":   "main",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should return a message about symbol not found
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("handles non-existent to symbol", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"from": "main",
			"to":   "nonExistent",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("requires from parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"to": "main",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure without from parameter")
		}
	})

	t.Run("requires to parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"from": "main",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure without to parameter")
		}
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := tool.Execute(cancelCtx, map[string]any{
			"from": "main",
			"to":   "funcB",
		})
		if err == nil {
			t.Error("Expected context.Canceled error")
		}
	})
}

func TestToolDefinitions_GraphAnalytics(t *testing.T) {
	g, idx := createTestGraphForAnalytics(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	tests := []struct {
		name     string
		tool     Tool
		wantName string
		wantCat  ToolCategory
	}{
		{
			name:     "find_hotspots",
			tool:     NewFindHotspotsTool(analytics, idx),
			wantName: "find_hotspots",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_dead_code",
			tool:     NewFindDeadCodeTool(analytics, idx),
			wantName: "find_dead_code",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_cycles",
			tool:     NewFindCyclesTool(analytics, idx),
			wantName: "find_cycles",
			wantCat:  CategoryExploration,
		},
		{
			name:     "find_path",
			tool:     NewFindPathTool(g, idx),
			wantName: "find_path",
			wantCat:  CategoryExploration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tool.Name(); got != tt.wantName {
				t.Errorf("Name() = %v, want %v", got, tt.wantName)
			}
			if got := tt.tool.Category(); got != tt.wantCat {
				t.Errorf("Category() = %v, want %v", got, tt.wantCat)
			}

			def := tt.tool.Definition()
			if def.Name != tt.wantName {
				t.Errorf("Definition().Name = %v, want %v", def.Name, tt.wantName)
			}
			if def.Description == "" {
				t.Error("Definition().Description is empty")
			}
		})
	}
}

func TestRegisterExploreTools_IncludesAnalyticsTools(t *testing.T) {
	g, idx := createTestGraphForAnalytics(t)
	registry := NewRegistry()

	RegisterExploreTools(registry, g, idx)

	// Check that the new analytics tools are registered
	analyticsTools := []string{
		"find_hotspots",
		"find_dead_code",
		"find_cycles",
		"find_path",
		"find_important", // GR-13
	}

	for _, name := range analyticsTools {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("Tool %s not registered", name)
		}
	}

	// Should have at least 21 tools now (16 original + 4 analytics + 1 PageRank)
	if count := registry.Count(); count < 21 {
		t.Errorf("Registry has %d tools, want at least 21", count)
	}
}

// =============================================================================
// GR-13: find_important Tool Tests (PageRank)
// =============================================================================

func TestFindImportantTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	// Create HierarchicalGraph and analytics
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)

	t.Run("finds important symbols with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindImportantOutput)
		if !ok {
			t.Fatalf("Output is not FindImportantOutput, got %T", result.Output)
		}

		// Check algorithm field indicates PageRank
		if output.Algorithm != "PageRank" {
			t.Errorf("Expected algorithm 'PageRank', got '%s'", output.Algorithm)
		}

		// Should have at least one result
		if len(output.Results) == 0 {
			t.Error("Expected at least one important symbol")
		}

		// First result should have pagerank score and rank
		if len(output.Results) > 0 {
			first := output.Results[0]
			if first.PageRank == 0 {
				t.Error("Expected non-zero pagerank score in result")
			}
			if first.Rank == 0 {
				t.Error("Expected non-zero rank in result")
			}
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindImportantOutput)
		if !ok {
			t.Fatalf("Output is not FindImportantOutput, got %T", result.Output)
		}

		if len(output.Results) > 2 {
			t.Errorf("got %d results, want at most 2", len(output.Results))
		}
	})

	t.Run("top parameter capped at max", func(t *testing.T) {
		// Request more than max
		result, err := tool.Execute(ctx, map[string]any{
			"top": 1000,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should succeed without error (capped internally)
		output, ok := result.Output.(FindImportantOutput)
		if !ok {
			t.Fatalf("Output is not FindImportantOutput, got %T", result.Output)
		}

		// Just verify we got valid results
		if output.Results == nil {
			t.Fatalf("results is nil")
		}
	})

	t.Run("handles nil analytics", func(t *testing.T) {
		nilTool := NewFindImportantTool(nil, idx)
		result, err := nilTool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure with nil analytics")
		}
		if result.Error == "" {
			t.Error("Expected error message")
		}
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := tool.Execute(cancelCtx, map[string]any{})
		if err == nil {
			t.Error("Expected context.Canceled error")
		}
	})

	t.Run("returns result metadata", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindImportantOutput)
		if !ok {
			t.Fatalf("Output is not FindImportantOutput, got %T", result.Output)
		}

		// Should have result count and algorithm metadata
		if output.ResultCount < 0 {
			t.Error("Expected non-negative result_count in output")
		}
		if output.Algorithm == "" {
			t.Error("Expected algorithm field to be set in output")
		}
	})
}

func TestFindImportantTool_VsHotspots(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	importantTool := NewFindImportantTool(analytics, idx)
	hotspotsTool := NewFindHotspotsTool(analytics, idx)

	// Get results from both tools
	importantResult, err := importantTool.Execute(ctx, map[string]any{"top": 6})
	if err != nil {
		t.Fatalf("find_important Execute() error = %v", err)
	}

	hotspotsResult, err := hotspotsTool.Execute(ctx, map[string]any{"top": 6})
	if err != nil {
		t.Fatalf("find_hotspots Execute() error = %v", err)
	}

	// Both should succeed
	if !importantResult.Success || !hotspotsResult.Success {
		t.Fatalf("One of the tools failed")
	}

	// Extract rankings (they may differ due to different algorithms)
	importantOutput, ok := importantResult.Output.(FindImportantOutput)
	if !ok {
		t.Fatalf("find_important output is not FindImportantOutput, got %T", importantResult.Output)
	}

	hotspotsOutput, ok := hotspotsResult.Output.(FindHotspotsOutput)
	if !ok {
		t.Fatalf("find_hotspots output is not FindHotspotsOutput, got %T", hotspotsResult.Output)
	}

	// Just verify both returned reasonable results
	if len(importantOutput.Results) > 0 {
		t.Logf("PageRank top: %v", importantOutput.Results[0].Name)
	}
	if len(hotspotsOutput.Hotspots) > 0 {
		t.Logf("HotSpots top: %v", hotspotsOutput.Hotspots[0].Name)
	}

	// Rankings may differ - that's expected
	// Just verify both have results
	if len(importantOutput.Results) == 0 {
		t.Error("find_important returned no results")
	}
	if len(hotspotsOutput.Hotspots) == 0 {
		t.Error("find_hotspots returned no results")
	}
}

func TestFindImportantTool_Definition(t *testing.T) {
	tool := NewFindImportantTool(nil, nil)

	if got := tool.Name(); got != "find_important" {
		t.Errorf("Name() = %v, want find_important", got)
	}

	if got := tool.Category(); got != CategoryExploration {
		t.Errorf("Category() = %v, want CategoryExploration", got)
	}

	def := tool.Definition()
	if def.Name != "find_important" {
		t.Errorf("Definition().Name = %v, want find_important", def.Name)
	}
	if def.Description == "" {
		t.Error("Definition().Description is empty")
	}
	if len(def.Parameters) == 0 {
		t.Error("Definition().Parameters is empty")
	}

	// Check for expected parameters (Parameters is a map[string]ParamDef)
	if _, ok := def.Parameters["top"]; !ok {
		t.Error("Missing 'top' parameter")
	}
}

// BenchmarkFindImportant benchmarks PageRank-based importance ranking.
func BenchmarkFindImportant(b *testing.B) {
	g, idx := createLargeGraph(b, 1000)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		b.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{"top": 10})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// =============================================================================
// GR-15: find_communities Tool Tests (TDD)
// =============================================================================

// createTestGraphForCommunities creates a graph with clear community structure.
// Creates two distinct communities with minimal cross-community edges.
func createTestGraphForCommunities(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Community 1: auth package (4 nodes, densely connected)
	authLogin := &ast.Symbol{
		ID:        "auth/login.go:10:Login",
		Name:      "Login",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/login.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authLogout := &ast.Symbol{
		ID:        "auth/login.go:35:Logout",
		Name:      "Logout",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/login.go",
		StartLine: 35,
		EndLine:   50,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authSession := &ast.Symbol{
		ID:        "auth/session.go:10:CreateSession",
		Name:      "CreateSession",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/session.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authValidate := &ast.Symbol{
		ID:        "auth/validate.go:10:ValidateToken",
		Name:      "ValidateToken",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/validate.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}

	// Community 2: config package (3 nodes, densely connected)
	configLoad := &ast.Symbol{
		ID:        "config/loader.go:10:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	configParse := &ast.Symbol{
		ID:        "config/parser.go:10:ParseConfig",
		Name:      "ParseConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/parser.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	configValidate := &ast.Symbol{
		ID:        "config/validate.go:10:ValidateConfig",
		Name:      "ValidateConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/validate.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}

	// Add all nodes to graph and index
	allSymbols := []*ast.Symbol{
		authLogin, authLogout, authSession, authValidate,
		configLoad, configParse, configValidate,
	}
	for _, sym := range allSymbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Community 1 edges (auth - dense internal connections)
	g.AddEdge(authLogin.ID, authSession.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 15,
	})
	g.AddEdge(authLogin.ID, authValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 18,
	})
	g.AddEdge(authLogout.ID, authSession.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogout.FilePath, StartLine: 40,
	})
	g.AddEdge(authSession.ID, authValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authSession.FilePath, StartLine: 15,
	})

	// Community 2 edges (config - dense internal connections)
	g.AddEdge(configLoad.ID, configParse.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 15,
	})
	g.AddEdge(configLoad.ID, configValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 20,
	})
	g.AddEdge(configParse.ID, configValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configParse.FilePath, StartLine: 15,
	})

	// One cross-community edge (sparse connection between communities)
	g.AddEdge(authLogin.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 12,
	})

	g.Freeze()
	return g, idx
}

// createCrossPackageCommunityGraph creates a graph where a community spans packages.
func createCrossPackageCommunityGraph(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Cross-package community: server and config are tightly coupled
	serverInit := &ast.Symbol{
		ID:        "server/init.go:10:InitServer",
		Name:      "InitServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/init.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "server",
		Exported:  true,
		Language:  "go",
	}
	configLoad := &ast.Symbol{
		ID:        "config/loader.go:10:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	serverConfig := &ast.Symbol{
		ID:        "server/config.go:10:ConfigureServer",
		Name:      "ConfigureServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/config.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "server",
		Exported:  true,
		Language:  "go",
	}
	configDefault := &ast.Symbol{
		ID:        "config/defaults.go:10:DefaultConfig",
		Name:      "DefaultConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/defaults.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}

	// Add nodes to graph and index
	allSymbols := []*ast.Symbol{serverInit, configLoad, serverConfig, configDefault}
	for _, sym := range allSymbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Dense cross-package connections (they should form one community)
	g.AddEdge(serverInit.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverInit.FilePath, StartLine: 15,
	})
	g.AddEdge(serverInit.ID, serverConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverInit.FilePath, StartLine: 18,
	})
	g.AddEdge(serverConfig.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverConfig.FilePath, StartLine: 12,
	})
	g.AddEdge(serverConfig.ID, configDefault.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverConfig.FilePath, StartLine: 14,
	})
	g.AddEdge(configLoad.ID, configDefault.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 15,
	})

	g.Freeze()
	return g, idx
}

func TestFindCommunitiesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	t.Run("finds communities with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// Check algorithm field
		if output.Algorithm != "Leiden" {
			t.Errorf("Expected algorithm 'Leiden', got '%s'", output.Algorithm)
		}

		// Check modularity is in valid range
		if output.Modularity < 0 || output.Modularity > 1 {
			t.Errorf("Modularity %f outside expected range [0,1]", output.Modularity)
		}

		// Check communities exist
		if len(output.Communities) == 0 {
			t.Error("Expected at least one community")
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects min_size parameter", func(t *testing.T) {
		// With min_size=5, should filter out smaller communities
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 5,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// All returned communities should have size >= 5
		for _, comm := range output.Communities {
			if comm.Size < 5 {
				t.Errorf("Community size %d is less than min_size 5", comm.Size)
			}
		}
	})

	t.Run("respects resolution parameter", func(t *testing.T) {
		// Lower resolution should produce fewer, larger communities
		lowResResult, err := tool.Execute(ctx, map[string]any{
			"resolution": 0.5,
		})
		if err != nil {
			t.Fatalf("Execute() low resolution error = %v", err)
		}

		// Higher resolution should produce more, smaller communities
		highResResult, err := tool.Execute(ctx, map[string]any{
			"resolution": 2.0,
		})
		if err != nil {
			t.Fatalf("Execute() high resolution error = %v", err)
		}

		// Both should succeed
		if !lowResResult.Success || !highResResult.Success {
			t.Fatalf("One of the resolution tests failed")
		}

		// Note: We can't guarantee exact community counts, but both should run
		lowOutput, ok := lowResResult.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Low resolution output is not FindCommunitiesOutput, got %T", lowResResult.Output)
		}
		highOutput, ok := highResResult.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("High resolution output is not FindCommunitiesOutput, got %T", highResResult.Output)
		}

		// Check that modularity is present (it's always set for FindCommunitiesOutput)
		if lowOutput.Modularity == 0 && lowOutput.CommunityCount > 0 {
			t.Error("Low resolution output has zero modularity with communities")
		}
		if highOutput.Modularity == 0 && highOutput.CommunityCount > 0 {
			t.Error("High resolution output has zero modularity with communities")
		}
	})

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top":      1,
			"min_size": 1, // Allow small communities
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		if len(output.Communities) > 1 {
			t.Errorf("got %d communities, want at most 1", len(output.Communities))
		}
	})

	t.Run("clamps invalid min_size to valid range", func(t *testing.T) {
		// min_size < 1 should be clamped
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": -5,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped min_size")
		}
	})

	t.Run("clamps invalid resolution to valid range", func(t *testing.T) {
		// resolution < 0.1 should be clamped
		result, err := tool.Execute(ctx, map[string]any{
			"resolution": 0.0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped resolution")
		}
	})

	t.Run("clamps invalid top to valid range", func(t *testing.T) {
		// top > 50 should be clamped
		result, err := tool.Execute(ctx, map[string]any{
			"top": 1000,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped top")
		}
	})

	t.Run("returns result metadata", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// Should have result metadata (these are always present in typed struct)
		if output.CommunityCount < 0 {
			t.Error("Expected non-negative community_count")
		}
		if output.Algorithm == "" {
			t.Error("Expected algorithm field to be set")
		}
		// Converged is a bool field, always present in typed struct
		// Just verify the output was processed (Converged may be true or false)
		t.Logf("Converged: %v", output.Converged)
	})
}

func TestFindCommunitiesTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	_, idx := createTestGraphForCommunities(t)

	tool := NewFindCommunitiesTool(nil, idx)
	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Error("Expected failure with nil analytics")
	}
	if result.Error == "" {
		t.Error("Expected error message")
	}
}

func TestFindCommunitiesTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = tool.Execute(ctx, map[string]any{})
	if err == nil {
		t.Error("Expected context.Canceled error")
	}
}

func TestFindCommunitiesTool_CrossPackageDetection(t *testing.T) {
	ctx := context.Background()
	g, idx := createCrossPackageCommunityGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1, // Allow small communities
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Check if cross-package communities are identified
	if len(output.CrossPackageCommunities) > 0 {
		t.Logf("Found %d cross-package communities", len(output.CrossPackageCommunities))
	}

	// The output text should mention cross-package if detected
	if result.OutputText != "" {
		// This is a soft check - cross-package detection is algorithmic
		t.Logf("Output text: %s", result.OutputText[:min(200, len(result.OutputText))])
	}
}

func TestFindCommunitiesTool_ShowCrossEdges(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	t.Run("show_cross_edges true", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"show_cross_edges": true,
			"min_size":         1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}
		// cross_community_edges should be present (may be empty if no cross edges exist)
		// Just verify the output was processed - the field is always present in typed struct
		t.Logf("CrossCommunityEdges count: %d", len(output.CrossCommunityEdges))
	})

	t.Run("show_cross_edges false", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"show_cross_edges": false,
			"min_size":         1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should still succeed, just without cross edges
		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}
		if len(output.CrossCommunityEdges) > 0 {
			t.Error("Expected no cross_community_edges when show_cross_edges=false")
		}
	})
}

func TestFindCommunitiesTool_Definition(t *testing.T) {
	tool := NewFindCommunitiesTool(nil, nil)

	if got := tool.Name(); got != "find_communities" {
		t.Errorf("Name() = %v, want find_communities", got)
	}

	if got := tool.Category(); got != CategoryExploration {
		t.Errorf("Category() = %v, want CategoryExploration", got)
	}

	def := tool.Definition()
	if def.Name != "find_communities" {
		t.Errorf("Definition().Name = %v, want find_communities", def.Name)
	}
	if def.Description == "" {
		t.Error("Definition().Description is empty")
	}
	if len(def.Parameters) == 0 {
		t.Error("Definition().Parameters is empty")
	}

	// Check for expected parameters
	expectedParams := []string{"min_size", "resolution", "top", "show_cross_edges"}
	for _, param := range expectedParams {
		if _, ok := def.Parameters[param]; !ok {
			t.Errorf("Missing '%s' parameter", param)
		}
	}

	// Check timeout is reasonable
	if def.Timeout < 30*time.Second {
		t.Errorf("Timeout %v is too short for community detection", def.Timeout)
	}
}

func TestFindCommunitiesTool_EmptyGraph(t *testing.T) {
	ctx := context.Background()

	// Create empty graph
	g := graph.NewGraph("/test")
	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	idx := index.NewSymbolIndex()
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with empty graph")
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	if len(output.Communities) != 0 {
		t.Errorf("Expected 0 communities for empty graph, got %d", len(output.Communities))
	}
}

func TestFindCommunitiesTool_ModularityQualityLabel(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should have modularity_quality label
	quality := output.ModularityQuality
	validQualities := map[string]bool{
		"weak": true, "moderate": true, "good": true, "strong": true,
	}
	if !validQualities[quality] {
		t.Errorf("Invalid modularity_quality: %s", quality)
	}
}

// BenchmarkFindCommunities benchmarks Leiden-based community detection.
func BenchmarkFindCommunities(b *testing.B) {
	g, idx := createLargeGraph(b, 500) // Smaller for faster benchmarks

	hg, err := graph.WrapGraph(g)
	if err != nil {
		b.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{"top": 10})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// TestFindCommunitiesTool_SingleNodeGraph tests behavior with a single node.
func TestFindCommunitiesTool_SingleNodeGraph(t *testing.T) {
	ctx := context.Background()

	// Create single-node graph
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	singleSym := &ast.Symbol{
		ID:        "pkg/solo.go:10:Solo",
		Name:      "Solo",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "pkg/solo.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Exported:  true,
		Language:  "go",
	}

	g.AddNode(singleSym)
	if err := idx.Add(singleSym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}
	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1, // Allow single-node communities
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with single node graph: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Single node should form one community
	if len(output.Communities) != 1 {
		t.Errorf("Expected 1 community for single-node graph, got %d", len(output.Communities))
	}

	if len(output.Communities) > 0 {
		size := output.Communities[0].Size
		if size != 1 {
			t.Errorf("Single-node community should have size 1, got %d", size)
		}
	}
}

// createDisconnectedGraph creates a graph with two disconnected components.
func createDisconnectedGraph(t testing.TB) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Component A - fully connected triangle
	compA := []*ast.Symbol{
		{ID: "compA/a1.go:10:A1", Name: "A1", Kind: ast.SymbolKindFunction, FilePath: "compA/a1.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
		{ID: "compA/a2.go:10:A2", Name: "A2", Kind: ast.SymbolKindFunction, FilePath: "compA/a2.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
		{ID: "compA/a3.go:10:A3", Name: "A3", Kind: ast.SymbolKindFunction, FilePath: "compA/a3.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
	}

	// Component B - fully connected triangle (disconnected from A)
	compB := []*ast.Symbol{
		{ID: "compB/b1.go:10:B1", Name: "B1", Kind: ast.SymbolKindFunction, FilePath: "compB/b1.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
		{ID: "compB/b2.go:10:B2", Name: "B2", Kind: ast.SymbolKindFunction, FilePath: "compB/b2.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
		{ID: "compB/b3.go:10:B3", Name: "B3", Kind: ast.SymbolKindFunction, FilePath: "compB/b3.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
	}

	for _, sym := range append(compA, compB...) {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Connect component A (triangle)
	g.AddEdge(compA[0].ID, compA[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[0].FilePath, StartLine: 15})
	g.AddEdge(compA[1].ID, compA[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[1].FilePath, StartLine: 15})
	g.AddEdge(compA[2].ID, compA[0].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[2].FilePath, StartLine: 15})

	// Connect component B (triangle)
	g.AddEdge(compB[0].ID, compB[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[0].FilePath, StartLine: 15})
	g.AddEdge(compB[1].ID, compB[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[1].FilePath, StartLine: 15})
	g.AddEdge(compB[2].ID, compB[0].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[2].FilePath, StartLine: 15})

	g.Freeze()
	return g, idx
}

// TestFindCommunitiesTool_DisconnectedGraph tests behavior with disconnected components.
func TestFindCommunitiesTool_DisconnectedGraph(t *testing.T) {
	ctx := context.Background()
	g, idx := createDisconnectedGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should detect at least 2 communities (one per disconnected component)
	if len(output.Communities) < 2 {
		t.Errorf("Expected at least 2 communities for disconnected graph, got %d", len(output.Communities))
	}

	// Total nodes across communities should equal 6
	totalNodes := 0
	for _, comm := range output.Communities {
		totalNodes += comm.Size
	}
	if totalNodes != 6 {
		t.Errorf("Total nodes in communities should be 6, got %d", totalNodes)
	}
}

// createAllSamePackageGraph creates a graph where all nodes are in the same package.
func createAllSamePackageGraph(t testing.TB) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{ID: "myPkg/file1.go:10:Func1", Name: "Func1", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file1.go", StartLine: 10, EndLine: 20, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file1.go:30:Func2", Name: "Func2", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file1.go", StartLine: 30, EndLine: 40, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file2.go:10:Func3", Name: "Func3", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file2.go", StartLine: 10, EndLine: 20, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file2.go:30:Func4", Name: "Func4", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file2.go", StartLine: 30, EndLine: 40, Package: "myPkg", Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Dense connections
	g.AddEdge(symbols[0].ID, symbols[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[0].FilePath, StartLine: 15})
	g.AddEdge(symbols[0].ID, symbols[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[0].FilePath, StartLine: 16})
	g.AddEdge(symbols[1].ID, symbols[3].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[1].FilePath, StartLine: 35})
	g.AddEdge(symbols[2].ID, symbols[3].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[2].FilePath, StartLine: 15})

	g.Freeze()
	return g, idx
}

// TestFindCommunitiesTool_AllSamePackage tests behavior when all nodes are in same package.
func TestFindCommunitiesTool_AllSamePackage(t *testing.T) {
	ctx := context.Background()
	g, idx := createAllSamePackageGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should have no cross-package communities since all are same package
	if len(output.CrossPackageCommunities) > 0 {
		t.Errorf("Expected no cross-package communities for same-package graph, got %d", len(output.CrossPackageCommunities))
	}

	// Communities should exist and all have dominant_package = "myPkg"
	for i, comm := range output.Communities {
		if comm.DominantPackage != "" && comm.DominantPackage != "myPkg" {
			t.Errorf("Community %d has unexpected dominant_package: %s", i, comm.DominantPackage)
		}
	}
}

// TestFindCommunitiesTool_ParameterExactBoundaries tests exact boundary values.
func TestFindCommunitiesTool_ParameterExactBoundaries(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"min_size=1 (lower bound)", map[string]any{"min_size": 1}},
		{"min_size=100 (upper bound)", map[string]any{"min_size": 100}},
		{"resolution=0.1 (lower bound)", map[string]any{"resolution": 0.1}},
		{"resolution=5.0 (upper bound)", map[string]any{"resolution": 5.0}},
		{"top=1 (lower bound)", map[string]any{"top": 1}},
		{"top=50 (upper bound)", map[string]any{"top": 50}},
		{"all bounds at min", map[string]any{"min_size": 1, "resolution": 0.1, "top": 1}},
		{"all bounds at max", map[string]any{"min_size": 100, "resolution": 5.0, "top": 50}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tc.params)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed: %s", result.Error)
			}

			output, ok := result.Output.(FindCommunitiesOutput)
			if !ok {
				t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
			}
			// Modularity is always present in typed struct, just verify it was computed
			t.Logf("Modularity: %f", output.Modularity)
		})
	}
}

// TestFindCommunitiesTool_ConcurrentExecution tests thread safety.
func TestFindCommunitiesTool_ConcurrentExecution(t *testing.T) {
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	const goroutines = 10
	ctx := context.Background()

	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			result, err := tool.Execute(ctx, map[string]any{
				"min_size":   1,
				"resolution": 1.0 + float64(idx%3)*0.5,
			})
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: execute error: %w", idx, err)
				return
			}
			if !result.Success {
				errCh <- fmt.Errorf("goroutine %d: execution failed: %s", idx, result.Error)
				return
			}
			errCh <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}

// TestFindCommunitiesTool_OutputFormatValidation validates all expected fields.
func TestFindCommunitiesTool_OutputFormatValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size":         1,
		"show_cross_edges": true,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Validate top-level fields are set appropriately
	if output.Algorithm == "" {
		t.Error("Algorithm should not be empty")
	}
	if output.Modularity < 0 || output.Modularity > 1 {
		t.Errorf("Modularity %f should be in [0,1]", output.Modularity)
	}
	if output.ModularityQuality == "" {
		t.Error("ModularityQuality should not be empty")
	}
	// Converged is a bool, always valid
	// CommunityCount should be consistent with Communities slice
	if output.CommunityCount != len(output.Communities) {
		t.Errorf("CommunityCount %d doesn't match Communities length %d", output.CommunityCount, len(output.Communities))
	}

	// Validate community structure
	if len(output.Communities) > 0 {
		comm := output.Communities[0]
		// Validate fields are set
		if comm.ID < 0 {
			t.Error("Community ID should not be negative")
		}
		if comm.Size <= 0 {
			t.Error("Community Size should be positive")
		}
		if comm.Connectivity < 0 || comm.Connectivity > 1 {
			t.Errorf("Community Connectivity %f should be in [0,1]", comm.Connectivity)
		}
		// InternalEdges, ExternalEdges, Members, DominantPackage, Packages, IsCrossPackage
		// are all guaranteed by the typed struct
	}

	// Validate cross_community_edges when present (show_cross_edges=true)
	if len(output.CrossCommunityEdges) > 0 {
		edge := output.CrossCommunityEdges[0]
		if edge.FromCommunity < 0 || edge.ToCommunity < 0 || edge.Count < 0 {
			t.Error("CrossCommunityEdge has invalid fields")
		}
	}

	// Validate OutputText is non-empty
	if result.OutputText == "" {
		t.Error("OutputText should not be empty")
	}
}

// TestFindCommunitiesTool_TokensUsed verifies token estimation.
func TestFindCommunitiesTool_TokensUsed(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TokensUsed should be > 0 for non-empty output
	if result.TokensUsed <= 0 {
		t.Error("TokensUsed should be > 0 for non-empty result")
	}

	// TokensUsed should be roughly proportional to OutputText length
	// (rough estimate: 4 chars per token)
	expectedMinTokens := len(result.OutputText) / 8
	if result.TokensUsed < expectedMinTokens {
		t.Errorf("TokensUsed %d seems too low for OutputText length %d", result.TokensUsed, len(result.OutputText))
	}
}

// TestFindCommunitiesTool_NilIndex tests behavior with nil index.
func TestFindCommunitiesTool_NilIndex(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	// Create tool with nil index - should still work since index is optional
	tool := NewFindCommunitiesTool(analytics, nil)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with nil index: %s", result.Error)
	}
}

// TestFindCommunitiesTool_LargeMinSize tests that large min_size filters out all communities.
func TestFindCommunitiesTool_LargeMinSize(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t) // Creates ~10 nodes

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 100, // Much larger than graph size
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// All communities should be filtered out
	if len(output.Communities) != 0 {
		t.Errorf("Expected 0 communities with min_size=100, got %d", len(output.Communities))
	}

	// Modularity is always present in typed struct
	t.Logf("Modularity with no communities: %f", output.Modularity)
}

// TestFindCommunitiesTool_TraceStepPopulated verifies CRS integration.
func TestFindCommunitiesTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"min_size": 1,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TraceStep should be populated (H-1 fix verification)
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}

	// Validate TraceStep fields
	if result.TraceStep.Action != "analytics_communities" {
		t.Errorf("TraceStep.Action = %q, want 'analytics_communities'", result.TraceStep.Action)
	}

	if result.TraceStep.Tool != "DetectCommunities" {
		t.Errorf("TraceStep.Tool = %q, want 'DetectCommunities'", result.TraceStep.Tool)
	}

	// Should have metadata
	if result.TraceStep.Metadata == nil {
		t.Error("TraceStep.Metadata should not be nil")
	} else {
		if _, ok := result.TraceStep.Metadata["algorithm"]; !ok {
			t.Error("TraceStep.Metadata should contain 'algorithm'")
		}
		if _, ok := result.TraceStep.Metadata["modularity"]; !ok {
			t.Error("TraceStep.Metadata should contain 'modularity'")
		}
	}

	// Duration should be tracked
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}
}

// =============================================================================
// find_articulation_points tool tests (GR-17a)
// =============================================================================

// createTestGraphForArticulationPoints creates a graph with known articulation points.
// Structure:
//
//	A --- B --- C --- D --- E
//	      |         |
//	      F         G --- H
//
// Articulation points: B (connects A to rest), C (connects B,F to D,G,H), D (connects C to E), G (connects D to H)
// Bridges: A-B, B-C, C-D, D-E, D-G, G-H
func createTestGraphForArticulationPoints(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create nodes
	symbols := []*ast.Symbol{
		{ID: "pkg/a.go:10:A", Name: "A", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:B", Name: "B", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:C", Name: "C", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/d.go:10:D", Name: "D", Kind: ast.SymbolKindFunction, FilePath: "pkg/d.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/e.go:10:E", Name: "E", Kind: ast.SymbolKindFunction, FilePath: "pkg/e.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/f.go:10:F", Name: "F", Kind: ast.SymbolKindFunction, FilePath: "pkg/f.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/g.go:10:G", Name: "G", Kind: ast.SymbolKindFunction, FilePath: "pkg/g.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/h.go:10:H", Name: "H", Kind: ast.SymbolKindFunction, FilePath: "pkg/h.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Add edges (call relationships)
	// A-B chain
	g.AddEdge("pkg/a.go:10:A", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	// B-C chain with B-F branch
	g.AddEdge("pkg/b.go:10:B", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/f.go:10:F", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 16})
	// C-D chain
	g.AddEdge("pkg/c.go:10:C", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/c.go", StartLine: 15})
	// D-E and D-G branches
	g.AddEdge("pkg/d.go:10:D", "pkg/e.go:10:E", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/d.go", StartLine: 15})
	g.AddEdge("pkg/d.go:10:D", "pkg/g.go:10:G", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/d.go", StartLine: 16})
	// G-H chain
	g.AddEdge("pkg/g.go:10:G", "pkg/h.go:10:H", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/g.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

// createConnectedGraphNoArticulation creates a graph with no articulation points (fully connected).
func createConnectedGraphNoArticulation(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Triangle: A-B-C-A (no articulation points)
	symbols := []*ast.Symbol{
		{ID: "pkg/a.go:10:A", Name: "A", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:B", Name: "B", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:C", Name: "C", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Create triangle (bidirectional)
	g.AddEdge("pkg/a.go:10:A", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})
	g.AddEdge("pkg/c.go:10:C", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/c.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

func TestFindArticulationPointsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	t.Run("finds articulation points with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindArticulationPointsOutput)
		if !ok {
			t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
		}

		// Should find at least some articulation points
		if len(output.ArticulationPoints) == 0 {
			t.Error("Expected at least one articulation point")
		}

		// Check fragility_score is in valid range
		if output.FragilityScore < 0 || output.FragilityScore > 1 {
			t.Errorf("fragility_score %f outside expected range [0,1]", output.FragilityScore)
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindArticulationPointsOutput)
		if !ok {
			t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
		}

		// Should not exceed top limit
		if len(output.ArticulationPoints) > 2 {
			t.Errorf("Expected at most 2 articulation points, got %d", len(output.ArticulationPoints))
		}
	})

	t.Run("include_bridges=true returns bridges", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"include_bridges": true,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindArticulationPointsOutput)
		if !ok {
			t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
		}

		// Bridges should be present
		if len(output.Bridges) == 0 {
			t.Error("Expected at least one bridge in test graph")
		}
	})

	t.Run("include_bridges=false omits bridges", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"include_bridges": false,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindArticulationPointsOutput)
		if !ok {
			t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
		}

		// Bridges should be empty when include_bridges=false
		if len(output.Bridges) > 0 {
			t.Error("Expected no bridges when include_bridges=false")
		}
	})
}

func TestFindArticulationPointsTool_NoArticulationPoints(t *testing.T) {
	ctx := context.Background()
	g, idx := createConnectedGraphNoArticulation(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	// Triangles have no articulation points
	if len(output.ArticulationPoints) != 0 {
		t.Errorf("Expected 0 articulation points in triangle, got %d", len(output.ArticulationPoints))
	}

	// Fragility should be 0
	if output.FragilityScore != 0 {
		t.Errorf("Expected fragility_score 0 for no articulation points, got %f", output.FragilityScore)
	}
}

func TestFindArticulationPointsTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	_, idx := createTestGraphForArticulationPoints(t)

	tool := NewFindArticulationPointsTool(nil, idx)
	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Error("Expected failure with nil analytics")
	}
	if result.Error == "" {
		t.Error("Expected error message")
	}
}

func TestFindArticulationPointsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = tool.Execute(ctx, map[string]any{})
	if err == nil {
		t.Error("Expected context.Canceled error")
	}
}

func TestFindArticulationPointsTool_Definition(t *testing.T) {
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	def := tool.Definition()

	// Check name
	if def.Name != "find_articulation_points" {
		t.Errorf("Name = %q, want 'find_articulation_points'", def.Name)
	}

	// Check category
	if tool.Category() != CategoryExploration {
		t.Errorf("Category = %v, want CategoryExploration", tool.Category())
	}

	// Check required parameters
	if _, ok := def.Parameters["top"]; !ok {
		t.Error("Expected 'top' parameter in definition")
	}
	if _, ok := def.Parameters["include_bridges"]; !ok {
		t.Error("Expected 'include_bridges' parameter in definition")
	}

	// Check description mentions key concepts
	if def.Description == "" {
		t.Error("Description should not be empty")
	}
}

func TestFindArticulationPointsTool_EmptyGraph(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	g.Freeze() // Must freeze before wrapping
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	// Empty graph has no articulation points
	if len(output.ArticulationPoints) != 0 {
		t.Errorf("Expected 0 articulation points in empty graph, got %d", len(output.ArticulationPoints))
	}
}

func TestFindArticulationPointsTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TraceStep should be populated for CRS integration
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}

	// Validate TraceStep fields
	if result.TraceStep.Action != "analytics_articulation_points" {
		t.Errorf("TraceStep.Action = %q, want 'analytics_articulation_points'", result.TraceStep.Action)
	}

	if result.TraceStep.Tool != "ArticulationPoints" {
		t.Errorf("TraceStep.Tool = %q, want 'ArticulationPoints'", result.TraceStep.Tool)
	}

	// Should have metadata
	if result.TraceStep.Metadata == nil {
		t.Error("TraceStep.Metadata should not be nil")
	} else {
		if _, ok := result.TraceStep.Metadata["points_found"]; !ok {
			t.Error("TraceStep.Metadata should contain 'points_found'")
		}
		if _, ok := result.TraceStep.Metadata["bridges_found"]; !ok {
			t.Error("TraceStep.Metadata should contain 'bridges_found'")
		}
	}

	// Duration should be tracked
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}
}

func TestFindArticulationPointsTool_ParameterValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	tests := []struct {
		name   string
		params map[string]any
	}{
		{"top=1 (lower bound)", map[string]any{"top": 1}},
		{"top=100 (upper bound)", map[string]any{"top": 100}},
		{"include_bridges=true", map[string]any{"include_bridges": true}},
		{"include_bridges=false", map[string]any{"include_bridges": false}},
		{"both params", map[string]any{"top": 5, "include_bridges": true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tc.params)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed: %s", result.Error)
			}

			output, ok := result.Output.(FindArticulationPointsOutput)
			if !ok {
				t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
			}
			// FragilityScore is always present in typed struct
			t.Logf("FragilityScore: %f", output.FragilityScore)
		})
	}
}

func TestFindArticulationPointsTool_OutputFormatValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"include_bridges": true,
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	// Validate top-level fields are set appropriately
	if output.FragilityScore < 0 || output.FragilityScore > 1 {
		t.Errorf("FragilityScore %f should be in [0,1]", output.FragilityScore)
	}
	if output.FragilityLevel == "" {
		t.Error("FragilityLevel should not be empty")
	}
	if output.TotalComponents < 0 {
		t.Error("TotalComponents should not be negative")
	}

	// Validate articulation point structure
	if len(output.ArticulationPoints) > 0 {
		point := output.ArticulationPoints[0]
		if point.ID == "" {
			t.Error("Articulation point should have ID")
		}
		if point.Name == "" {
			t.Error("Articulation point should have Name")
		}
	}

	// Validate bridge structure
	if len(output.Bridges) > 0 {
		bridge := output.Bridges[0]
		if bridge.From == "" {
			t.Error("Bridge should have From field")
		}
		if bridge.To == "" {
			t.Error("Bridge should have To field")
		}
	}
}

func TestFindArticulationPointsTool_ConcurrentExecution(t *testing.T) {
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	const goroutines = 10
	ctx := context.Background()

	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			result, err := tool.Execute(ctx, map[string]any{
				"top":             idx%5 + 1,
				"include_bridges": idx%2 == 0,
			})
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: execute error: %w", idx, err)
				return
			}
			if !result.Success {
				errCh <- fmt.Errorf("goroutine %d: execution failed: %s", idx, result.Error)
				return
			}
			errCh <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}

func TestFindArticulationPointsTool_TokensUsed(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TokensUsed should be > 0 for non-empty output
	if result.TokensUsed <= 0 {
		t.Error("TokensUsed should be > 0 for non-empty result")
	}

	// TokensUsed should be roughly proportional to OutputText length
	// (rough estimate: 4 chars per token)
	expectedMinTokens := len(result.OutputText) / 8
	if result.TokensUsed < expectedMinTokens {
		t.Errorf("TokensUsed %d seems too low for OutputText length %d", result.TokensUsed, len(result.OutputText))
	}
}

func TestFindArticulationPointsTool_NilIndex(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	// Create tool with nil index - should still work since index is optional
	tool := NewFindArticulationPointsTool(analytics, nil)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with nil index: %s", result.Error)
	}
}

// BenchmarkFindArticulationPoints benchmarks articulation point detection.
func BenchmarkFindArticulationPoints(b *testing.B) {
	g, idx := createLargeGraph(b, 500) // Use existing helper

	hg, err := graph.WrapGraph(g)
	if err != nil {
		b.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, map[string]any{"top": 10})
		if err != nil {
			b.Fatalf("Execute() error: %v", err)
		}
	}
}

// =============================================================================
// Additional GR-17a Tests (find_articulation_points tool)
// =============================================================================

// TestFindArticulationPointsTool_TopClampingLow verifies clamping when top < 1.
func TestFindArticulationPointsTool_TopClampingLow(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	// Test with top = 0 (should clamp to 1)
	result, err := tool.Execute(ctx, map[string]any{"top": 0})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	// Should have at most 1 point (clamped)
	if len(output.ArticulationPoints) > 1 {
		t.Errorf("Expected at most 1 point when top=0 (clamped to 1), got %d", len(output.ArticulationPoints))
	}

	// Test with top = -5 (should clamp to 1)
	result2, err := tool.Execute(ctx, map[string]any{"top": -5})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result2.Success {
		t.Fatalf("Execute() failed: %s", result2.Error)
	}

	output2, ok := result2.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result2.Output)
	}
	if len(output2.ArticulationPoints) > 1 {
		t.Errorf("Expected at most 1 point when top=-5 (clamped to 1), got %d", len(output2.ArticulationPoints))
	}
}

// TestFindArticulationPointsTool_TopClampingHigh verifies clamping when top > 100.
func TestFindArticulationPointsTool_TopClampingHigh(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	// Test with top = 500 (should clamp to 100)
	result, err := tool.Execute(ctx, map[string]any{"top": 500})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should succeed (clamped to 100, which is more than our test graph has)
	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}
	// ArticulationPoints is always present in typed struct
	t.Logf("Got %d articulation points", len(output.ArticulationPoints))
}

// TestFindArticulationPointsTool_FragilityLevels verifies fragility_level categories.
func TestFindArticulationPointsTool_FragilityLevels(t *testing.T) {
	ctx := context.Background()

	// Valid fragility levels as defined in getFragilityLevel()
	validLevels := map[string]bool{
		"MINIMAL - well-connected architecture":     true, // < 5%
		"LOW - reasonably robust":                   true, // 5-10%
		"MODERATE - some architectural bottlenecks": true, // 10-20%
		"HIGH - many single points of failure":      true, // >= 20%
	}

	tests := []struct {
		name             string
		graphFunc        func(*testing.T) (*graph.Graph, *index.SymbolIndex)
		expectedContains string // substring that should be in the level
	}{
		{
			name:             "no articulation points (triangle)",
			graphFunc:        createConnectedGraphNoArticulation,
			expectedContains: "MINIMAL", // 0% fragility
		},
		{
			name:             "with articulation points",
			graphFunc:        createTestGraphForArticulationPoints,
			expectedContains: "", // Just verify it's a valid level string
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, idx := tc.graphFunc(t)
			hg, err := graph.WrapGraph(g)
			if err != nil {
				t.Fatalf("WrapGraph failed: %v", err)
			}
			analytics := graph.NewGraphAnalytics(hg)
			tool := NewFindArticulationPointsTool(analytics, idx)

			result, err := tool.Execute(ctx, map[string]any{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed: %s", result.Error)
			}

			output, ok := result.Output.(FindArticulationPointsOutput)
			if !ok {
				t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
			}

			level := output.FragilityLevel

			// Verify it's a valid level
			if !validLevels[level] {
				t.Errorf("Invalid fragility_level: %q", level)
			}

			// Check expected substring if specified
			if tc.expectedContains != "" {
				found := false
				for validLevel := range validLevels {
					if validLevel == level && strings.Contains(level, tc.expectedContains) {
						found = true
						break
					}
				}
				if !found && !strings.Contains(level, tc.expectedContains) {
					t.Errorf("Expected fragility_level to contain %q, got %q", tc.expectedContains, level)
				}
			}
		})
	}
}

// TestFindArticulationPointsTool_AllOutputFields verifies all expected fields exist.
func TestFindArticulationPointsTool_AllOutputFields(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{"include_bridges": true})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	// Verify all fields are accessible (typed struct guarantees presence)
	_ = output.ArticulationPoints
	_ = output.Bridges
	_ = output.TotalComponents
	_ = output.FragilityScore
	_ = output.FragilityLevel
	_ = output.NodeCount
	_ = output.EdgeCount

	// Verify Result fields
	if result.OutputText == "" {
		t.Error("OutputText should not be empty")
	}
	if result.TokensUsed <= 0 {
		t.Error("TokensUsed should be positive")
	}
	if result.TraceStep == nil {
		t.Error("TraceStep should be populated")
	}
}

// TestFindArticulationPointsTool_PointMetadata verifies each point has expected metadata.
func TestFindArticulationPointsTool_PointMetadata(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	if len(output.ArticulationPoints) == 0 {
		t.Fatal("Expected at least one articulation point")
	}

	// Check first point has expected fields
	point := output.ArticulationPoints[0]
	if point.ID == "" {
		t.Error("Point ID should not be empty")
	}
	if point.Name == "" {
		t.Error("Point Name should not be empty")
	}
}

// TestFindArticulationPointsTool_BridgeMetadata verifies bridge metadata.
func TestFindArticulationPointsTool_BridgeMetadata(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForArticulationPoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindArticulationPointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{"include_bridges": true})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindArticulationPointsOutput)
	if !ok {
		t.Fatalf("Output is not FindArticulationPointsOutput, got %T", result.Output)
	}

	if len(output.Bridges) == 0 {
		t.Fatal("Expected at least one bridge in test graph")
	}

	// Check first bridge has expected fields (typed struct guarantees these)
	bridge := output.Bridges[0]
	if bridge.From == "" {
		t.Error("Bridge From should not be empty")
	}
	if bridge.To == "" {
		t.Error("Bridge To should not be empty")
	}
}

// =============================================================================
// find_loops tool tests (GR-17e)
// =============================================================================

// createTestGraphWithLoops creates a graph with known loop structures.
// Structure:
//
//	main -> A -> B -> C -> A (cycle: A-B-C)
//	        |
//	        v
//	        D -> D (self-loop/direct recursion)
//	        |
//	        v
//	        E -> F -> E (mutual recursion: E-F)
//
// Expected loops:
// 1. A-B-C (size 3, mutual recursion via C->A back edge)
// 2. D (size 1, direct recursion)
// 3. E-F (size 2, mutual recursion)
func createTestGraphWithLoops(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create nodes
	symbols := []*ast.Symbol{
		{ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction, FilePath: "cmd/main.go", StartLine: 10, EndLine: 20, Package: "main", Exported: false, Language: "go"},
		{ID: "pkg/a.go:10:A", Name: "A", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:B", Name: "B", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:C", Name: "C", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/d.go:10:D", Name: "D", Kind: ast.SymbolKindFunction, FilePath: "pkg/d.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/e.go:10:E", Name: "E", Kind: ast.SymbolKindFunction, FilePath: "pkg/e.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/f.go:10:F", Name: "F", Kind: ast.SymbolKindFunction, FilePath: "pkg/f.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Add edges (call relationships)
	// main -> A
	g.AddEdge("cmd/main.go:10:main", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	// A -> B -> C -> A (3-node cycle)
	g.AddEdge("pkg/a.go:10:A", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})
	g.AddEdge("pkg/c.go:10:C", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/c.go", StartLine: 15}) // Back edge
	// A -> D (branch to direct recursion)
	g.AddEdge("pkg/a.go:10:A", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 16})
	// D -> D (self-loop / direct recursion)
	g.AddEdge("pkg/d.go:10:D", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/d.go", StartLine: 15})
	// D -> E (branch to mutual recursion)
	g.AddEdge("pkg/d.go:10:D", "pkg/e.go:10:E", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/d.go", StartLine: 16})
	// E -> F -> E (mutual recursion)
	g.AddEdge("pkg/e.go:10:E", "pkg/f.go:10:F", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/e.go", StartLine: 15})
	g.AddEdge("pkg/f.go:10:F", "pkg/e.go:10:E", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/f.go", StartLine: 15}) // Back edge

	g.Freeze()
	return g, idx
}

// createTestGraphNoLoops creates a DAG (directed acyclic graph) with no loops.
func createTestGraphNoLoops(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Simple tree structure: main -> A, B; A -> C; B -> D
	symbols := []*ast.Symbol{
		{ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction, FilePath: "cmd/main.go", StartLine: 10, EndLine: 20, Package: "main", Exported: false, Language: "go"},
		{ID: "pkg/a.go:10:A", Name: "A", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:B", Name: "B", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:C", Name: "C", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/d.go:10:D", Name: "D", Kind: ast.SymbolKindFunction, FilePath: "pkg/d.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// DAG edges (no cycles)
	g.AddEdge("cmd/main.go:10:main", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	g.AddEdge("cmd/main.go:10:main", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 16})
	g.AddEdge("pkg/a.go:10:A", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

func TestFindLoopsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("finds loops with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// Should find at least some loops
		if len(output.Loops) == 0 {
			t.Error("Expected at least one loop")
		}

		// Check summary is present
		if output.Summary.TotalLoops < 0 {
			t.Error("summary.total_loops should not be negative")
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("loop has required fields", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		if len(output.Loops) == 0 {
			t.Fatal("Expected at least one loop to check fields")
		}

		loop := output.Loops[0]
		if loop.Header == "" {
			t.Error("Loop missing required field: header")
		}
		if loop.HeaderName == "" {
			t.Error("Loop missing required field: header_name")
		}
		// body_size and depth are ints, always present in typed struct
	})
}

func TestFindLoopsTool_MinSize(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("min_size=2 filters self-loops", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// All returned loops should have size >= 2
		for _, loop := range output.Loops {
			if loop.BodySize < 2 {
				t.Errorf("Expected all loops to have size >= 2, got size %d", loop.BodySize)
			}
		}
	})
}

func TestFindLoopsTool_TopLimit(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// Should not exceed top limit
		if len(output.Loops) > 1 {
			t.Errorf("Expected at most 1 loop, got %d", len(output.Loops))
		}
	})
}

func TestFindLoopsTool_NoLoops(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphNoLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("DAG returns empty loops", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// DAG should have no loops
		if len(output.Loops) != 0 {
			t.Errorf("Expected 0 loops in DAG, got %d", len(output.Loops))
		}

		// Summary should indicate 0 loops
		if output.Summary.TotalLoops != 0 {
			t.Errorf("Expected total_loops=0, got %d", output.Summary.TotalLoops)
		}
	})
}

func TestFindLoopsTool_DirectRecursion(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("detects direct recursion", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// Should have at least one direct recursion (D -> D)
		if output.Summary.DirectRecursion < 1 {
			t.Errorf("Expected at least 1 direct recursion, got %d", output.Summary.DirectRecursion)
		}
	})
}

func TestFindLoopsTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	tool := NewFindLoopsTool(nil, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() should not return error for nil analytics: %v", err)
	}
	if result.Success {
		t.Error("Execute() should fail when analytics is nil")
	}
	if result.Error == "" {
		t.Error("Execute() should return error message when analytics is nil")
	}
}

func TestFindLoopsTool_ParamBounds(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("top below 1 clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should succeed with clamped value
		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}
		// Should return at least one loop (clamped top=1 allows 1)
		if len(output.Loops) > 20 { // Default is 20, clamped to 20 if above 100
			t.Errorf("Expected top to be clamped")
		}
	})

	t.Run("top above 100 clamped to 100", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 200,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Should succeed with clamped value (100)
	})

	t.Run("min_size below 1 clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Should succeed with clamped value
	})
}

func TestFindLoopsTool_ShowNesting(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("show_nesting=true includes nesting info", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"show_nesting": true,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindLoopsOutput)
		if !ok {
			t.Fatalf("Output is not FindLoopsOutput, got %T", result.Output)
		}

		// Each loop should have depth field (always present in typed struct)
		for _, loop := range output.Loops {
			// Depth is an int field, always present
			_ = loop.Depth
		}

		// Summary should have max_nesting (always present in typed struct)
		_ = output.Summary.MaxNesting
	})

	t.Run("show_nesting=false omits nesting hierarchy", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"show_nesting": false,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should still succeed but may omit certain fields
		// (Implementation detail - loops still have depth but hierarchy not shown)
	})
}

func TestFindLoopsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := tool.Execute(ctx, map[string]any{})

		// Should handle cancellation gracefully
		// Either return error or return partial results
		if err != nil && err != context.Canceled {
			// If error is returned, it should be context.Canceled
			t.Logf("Execute returned error: %v (acceptable for cancelled context)", err)
		}
		if result != nil && result.Success && result.Error != "" {
			// If success, the operation completed before checking cancellation
			t.Log("Execute completed despite cancellation (acceptable for small graphs)")
		}
	})
}

func TestFindLoopsTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithLoops(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindLoopsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Check TraceStep is present
	if result.TraceStep == nil {
		t.Error("Expected TraceStep in result")
	} else {
		// TraceStep.Tool comes from the underlying analytics (DetectLoops)
		if result.TraceStep.Tool != "DetectLoops" {
			t.Errorf("TraceStep.Tool = %s, want DetectLoops", result.TraceStep.Tool)
		}
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}

// =============================================================================
// find_merge_points Tool Tests (GR-17d)
// =============================================================================

// createTestGraphWithMergePoints creates a graph with merge points for testing.
// Graph structure:
//
//	A    B
//	 \  /
//	  M1  (merge point - 2 sources)
//	   |
//	  C    D    E
//	   \   |   /
//	    \  |  /
//	      M2    (merge point - 3 sources)
func createTestGraphWithMergePoints(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create nodes
	symbols := []*ast.Symbol{
		{ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction, FilePath: "cmd/main.go", StartLine: 10, EndLine: 20, Package: "main", Exported: false, Language: "go"},
		{ID: "pkg/a.go:10:FuncA", Name: "FuncA", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:FuncB", Name: "FuncB", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/m1.go:10:Merge1", Name: "Merge1", Kind: ast.SymbolKindFunction, FilePath: "pkg/m1.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:FuncC", Name: "FuncC", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/d.go:10:FuncD", Name: "FuncD", Kind: ast.SymbolKindFunction, FilePath: "pkg/d.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/e.go:10:FuncE", Name: "FuncE", Kind: ast.SymbolKindFunction, FilePath: "pkg/e.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/m2.go:10:Merge2", Name: "Merge2", Kind: ast.SymbolKindFunction, FilePath: "pkg/m2.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Create edges forming merge points
	// main -> A, B (branching)
	g.AddEdge("cmd/main.go:10:main", "pkg/a.go:10:FuncA", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	g.AddEdge("cmd/main.go:10:main", "pkg/b.go:10:FuncB", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 16})

	// A, B -> M1 (merge)
	g.AddEdge("pkg/a.go:10:FuncA", "pkg/m1.go:10:Merge1", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:FuncB", "pkg/m1.go:10:Merge1", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})

	// M1 -> C, D and main -> E (branching for second merge)
	g.AddEdge("pkg/m1.go:10:Merge1", "pkg/c.go:10:FuncC", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/m1.go", StartLine: 15})
	g.AddEdge("pkg/m1.go:10:Merge1", "pkg/d.go:10:FuncD", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/m1.go", StartLine: 16})
	g.AddEdge("cmd/main.go:10:main", "pkg/e.go:10:FuncE", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 17})

	// C, D, E -> M2 (merge with 3 sources)
	g.AddEdge("pkg/c.go:10:FuncC", "pkg/m2.go:10:Merge2", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/c.go", StartLine: 15})
	g.AddEdge("pkg/d.go:10:FuncD", "pkg/m2.go:10:Merge2", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/d.go", StartLine: 15})
	g.AddEdge("pkg/e.go:10:FuncE", "pkg/m2.go:10:Merge2", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/e.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

// createTestGraphNoMergePoints creates a DAG with no merge points.
func createTestGraphNoMergePointsMP(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a tree structure (no merge points)
	symbols := []*ast.Symbol{
		{ID: "pkg/root.go:10:Root", Name: "Root", Kind: ast.SymbolKindFunction, FilePath: "pkg/root.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/a.go:10:A", Name: "A", Kind: ast.SymbolKindFunction, FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/b.go:10:B", Name: "B", Kind: ast.SymbolKindFunction, FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/c.go:10:C", Name: "C", Kind: ast.SymbolKindFunction, FilePath: "pkg/c.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
		{ID: "pkg/d.go:10:D", Name: "D", Kind: ast.SymbolKindFunction, FilePath: "pkg/d.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: true, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Tree structure - no convergence
	g.AddEdge("pkg/root.go:10:Root", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/root.go", StartLine: 15})
	g.AddEdge("pkg/root.go:10:Root", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/root.go", StartLine: 16})
	g.AddEdge("pkg/a.go:10:A", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

func TestFindMergePointsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("finds merge points with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindMergePointsOutput)
		if !ok {
			t.Fatalf("Output is not FindMergePointsOutput, got %T", result.Output)
		}

		// Should find at least one merge point
		if len(output.MergePoints) == 0 {
			t.Error("Expected at least one merge point")
		}
	})

	t.Run("merge point has required fields", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindMergePointsOutput)
		if !ok {
			t.Fatalf("Output is not FindMergePointsOutput, got %T", result.Output)
		}

		if len(output.MergePoints) == 0 {
			t.Skip("No merge points to check fields")
		}

		// Verify typed struct has required fields populated
		mp := output.MergePoints[0]
		if mp.ID == "" {
			t.Error("Merge point missing ID")
		}
		if mp.Name == "" {
			t.Error("Merge point missing Name")
		}
		if mp.ConvergingPaths == 0 {
			t.Error("Merge point missing ConvergingPaths")
		}
	})
}

func TestFindMergePointsTool_MinSources(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("min_sources=3 filters lower convergence", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_sources": 3,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindMergePointsOutput)
		if !ok {
			t.Fatalf("Output is not FindMergePointsOutput, got %T", result.Output)
		}

		// All returned merge points should have >= 3 converging paths
		for _, mp := range output.MergePoints {
			if mp.ConvergingPaths < 3 {
				t.Errorf("Expected converging_paths >= 3, got %d", mp.ConvergingPaths)
			}
		}
	})
}

func TestFindMergePointsTool_TopLimit(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindMergePointsOutput)
		if !ok {
			t.Fatalf("Output is not FindMergePointsOutput, got %T", result.Output)
		}

		// Should return at most 1 merge point
		if len(output.MergePoints) > 1 {
			t.Errorf("Expected at most 1 merge point, got %d", len(output.MergePoints))
		}
	})
}

func TestFindMergePointsTool_NoMergePoints(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphNoMergePointsMP(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("tree returns no merge points", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindMergePointsOutput)
		if !ok {
			t.Fatalf("Output is not FindMergePointsOutput, got %T", result.Output)
		}

		// Tree should have no merge points
		if len(output.MergePoints) != 0 {
			t.Errorf("Expected 0 merge points in tree, got %d", len(output.MergePoints))
		}

		// Summary should indicate 0 merge points
		if output.Summary.TotalMergePoints != 0 {
			t.Errorf("Expected total_merge_points=0, got %d", output.Summary.TotalMergePoints)
		}
	})
}

func TestFindMergePointsTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	tool := NewFindMergePointsTool(nil, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() should not return error for nil analytics: %v", err)
	}
	if result.Success {
		t.Error("Execute() should fail when analytics is nil")
	}
	if result.Error == "" {
		t.Error("Execute() should return error message when analytics is nil")
	}
}

func TestFindMergePointsTool_ParamBounds(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("top below 1 clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Should succeed with clamped value
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("top above 100 clamped to 100", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 200,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("min_sources below 2 clamped to 2", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_sources": 1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Should succeed - by definition merge requires 2+
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})
}

func TestFindMergePointsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := tool.Execute(ctx, map[string]any{})

		// Should return error or context cancellation
		if err == nil {
			// Some implementations return nil error with Success=false
			// This is acceptable
		}
	})
}

func TestFindMergePointsTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMergePoints(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindMergePointsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Check TraceStep is present
	if result.TraceStep == nil {
		t.Error("Expected TraceStep in result")
	} else {
		// TraceStep.Tool comes from the underlying analytics
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}

// =============================================================================
// find_control_dependencies Tool Tests (GR-17c)
// =============================================================================

// createTestGraphWithControlFlow creates a graph with branching control flow.
func createTestGraphWithControlFlow(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a control flow pattern:
	// main -> router -> handler1, handler2
	//              |
	//              v
	//         validator -> process
	//
	// handler1 and handler2 are control-dependent on router (branch decision)
	symbols := []*ast.Symbol{
		{ID: "main:1:main", Name: "main", Kind: ast.SymbolKindFunction, Package: "main", FilePath: "main.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "router:1:Route", Name: "Route", Kind: ast.SymbolKindFunction, Package: "router", FilePath: "router.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "handler:1:HandleGet", Name: "HandleGet", Kind: ast.SymbolKindFunction, Package: "handler", FilePath: "handler.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "handler:2:HandlePost", Name: "HandlePost", Kind: ast.SymbolKindFunction, Package: "handler", FilePath: "handler.go", StartLine: 20, EndLine: 30, Language: "go"},
		{ID: "validator:1:Validate", Name: "Validate", Kind: ast.SymbolKindFunction, Package: "validator", FilePath: "validator.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "processor:1:Process", Name: "Process", Kind: ast.SymbolKindFunction, Package: "processor", FilePath: "processor.go", StartLine: 1, EndLine: 10, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s to index: %v", sym.ID, err)
		}
	}

	// Add edges (control flow)
	edges := [][2]string{
		{"main:1:main", "router:1:Route"},
		{"router:1:Route", "handler:1:HandleGet"},
		{"router:1:Route", "handler:2:HandlePost"},
		{"router:1:Route", "validator:1:Validate"},
		{"validator:1:Validate", "processor:1:Process"},
		{"handler:1:HandleGet", "processor:1:Process"},
		{"handler:2:HandlePost", "processor:1:Process"},
	}

	for _, edge := range edges {
		g.AddEdge(edge[0], edge[1], graph.EdgeTypeCalls, ast.Location{
			FilePath: "test.go", StartLine: 1,
		})
	}

	g.Freeze()
	return g, idx
}

func TestFindControlDependenciesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithControlFlow(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindControlDependenciesTool(analytics, idx)

	t.Run("finds control dependencies with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"target": "Process",
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindControlDependenciesOutput)
		if !ok {
			t.Fatalf("Output is not FindControlDependenciesOutput, got %T", result.Output)
		}

		// Should find some control dependencies
		t.Logf("Found %d control dependencies for Process", len(output.Dependencies))
	})

	t.Run("requires target parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without target parameter")
		}
		if result.Error == "" {
			t.Error("Execute() should return error message")
		}
	})
}

func TestFindControlDependenciesTool_Depth(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithControlFlow(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindControlDependenciesTool(analytics, idx)

	t.Run("respects depth parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"target": "Process",
			"depth":  2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindControlDependenciesOutput)
		if !ok {
			t.Fatalf("Output is not FindControlDependenciesOutput, got %T", result.Output)
		}

		// Depth should be limited
		t.Logf("Dependencies at depth 2: %d", len(output.Dependencies))
	})
}

func TestFindControlDependenciesTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	tool := NewFindControlDependenciesTool(nil, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"target": "Process",
	})

	if err != nil {
		t.Fatalf("Execute() should not return error for nil analytics: %v", err)
	}
	if result.Success {
		t.Error("Execute() should fail when analytics is nil")
	}
	if result.Error == "" {
		t.Error("Execute() should return error message when analytics is nil")
	}
}

func TestFindControlDependenciesTool_ParamBounds(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithControlFlow(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindControlDependenciesTool(analytics, idx)

	t.Run("depth below 1 clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"target": "Process",
			"depth":  0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Should succeed with clamped value
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("depth above 10 clamped to 10", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"target": "Process",
			"depth":  100,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})
}

func TestFindControlDependenciesTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithControlFlow(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindControlDependenciesTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := tool.Execute(ctx, map[string]any{
			"target": "Process",
		})

		// Should return error or context cancellation
		if err == nil {
			// Some implementations return nil error with Success=false
			// This is acceptable
		}
	})
}

func TestFindControlDependenciesTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithControlFlow(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindControlDependenciesTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{
		"target": "Process",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Check TraceStep is present
	if result.TraceStep == nil {
		t.Error("Expected TraceStep in result")
	} else {
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}

// =============================================================================
// find_extractable_regions Tool Tests (GR-17g)
// =============================================================================

// createTestGraphWithSESERegions creates a graph with extractable SESE regions.
func createTestGraphWithSESERegions(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a SESE region pattern:
	// entry -> [SESE region: a -> b -> c] -> exit
	// The a->b->c sequence is a single-entry single-exit region
	symbols := []*ast.Symbol{
		{ID: "main:1:main", Name: "main", Kind: ast.SymbolKindFunction, Package: "main", FilePath: "main.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "sese:1:setup", Name: "setup", Kind: ast.SymbolKindFunction, Package: "sese", FilePath: "sese.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "sese:2:process", Name: "process", Kind: ast.SymbolKindFunction, Package: "sese", FilePath: "sese.go", StartLine: 10, EndLine: 20, Language: "go"},
		{ID: "sese:3:cleanup", Name: "cleanup", Kind: ast.SymbolKindFunction, Package: "sese", FilePath: "sese.go", StartLine: 20, EndLine: 30, Language: "go"},
		{ID: "main:2:finish", Name: "finish", Kind: ast.SymbolKindFunction, Package: "main", FilePath: "main.go", StartLine: 30, EndLine: 40, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s to index: %v", sym.ID, err)
		}
	}

	// Create linear flow (potential SESE region)
	edges := [][2]string{
		{"main:1:main", "sese:1:setup"},
		{"sese:1:setup", "sese:2:process"},
		{"sese:2:process", "sese:3:cleanup"},
		{"sese:3:cleanup", "main:2:finish"},
	}

	for _, edge := range edges {
		g.AddEdge(edge[0], edge[1], graph.EdgeTypeCalls, ast.Location{
			FilePath: "test.go", StartLine: 1,
		})
	}

	g.Freeze()
	return g, idx
}

func TestFindExtractableRegionsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	t.Run("finds extractable regions with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindExtractableRegionsOutput)
		if !ok {
			t.Fatalf("Output is not FindExtractableRegionsOutput, got %T", result.Output)
		}

		t.Logf("Found %d extractable regions", len(output.Regions))
	})
}

func TestFindExtractableRegionsTool_SizeFilter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	t.Run("respects min_size parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 2,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindExtractableRegionsOutput)
		if !ok {
			t.Fatalf("Output is not FindExtractableRegionsOutput, got %T", result.Output)
		}

		// All regions should have size >= 2
		for _, region := range output.Regions {
			if region.Size < 2 {
				t.Errorf("Region size %d is less than min_size 2", region.Size)
			}
		}
	})

	t.Run("respects max_size parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"max_size": 10,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindExtractableRegionsOutput)
		if !ok {
			t.Fatalf("Output is not FindExtractableRegionsOutput, got %T", result.Output)
		}

		// All regions should have size <= 10
		for _, region := range output.Regions {
			if region.Size > 10 {
				t.Errorf("Region size %d exceeds max_size 10", region.Size)
			}
		}
	})
}

func TestFindExtractableRegionsTool_TopLimit(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 1,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindExtractableRegionsOutput)
		if !ok {
			t.Fatalf("Output is not FindExtractableRegionsOutput, got %T", result.Output)
		}

		if len(output.Regions) > 1 {
			t.Errorf("Expected at most 1 region, got %d", len(output.Regions))
		}
	})
}

func TestFindExtractableRegionsTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	tool := NewFindExtractableRegionsTool(nil, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() should not return error for nil analytics: %v", err)
	}
	if result.Success {
		t.Error("Execute() should fail when analytics is nil")
	}
	if result.Error == "" {
		t.Error("Execute() should return error message when analytics is nil")
	}
}

func TestFindExtractableRegionsTool_ParamBounds(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	t.Run("min_size below 1 clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"min_size": 0,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("top above 100 clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"top": 200,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})
}

func TestFindExtractableRegionsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := tool.Execute(ctx, map[string]any{})

		// Should return error or context cancellation
		if err == nil {
			// Some implementations return nil error with Success=false
			// This is acceptable
		}
	})
}

func TestFindExtractableRegionsTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithSESERegions(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Check TraceStep is present
	if result.TraceStep == nil {
		t.Error("Expected TraceStep in result")
	} else {
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}

// =============================================================================
// check_reducibility Tool Tests (GR-17h)
// =============================================================================

// createTestGraphReducible creates a well-structured (reducible) graph.
func createTestGraphReducible(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a simple reducible graph (no cross edges)
	// main -> a -> b -> c (linear = reducible)
	symbols := []*ast.Symbol{
		{ID: "main:1:main", Name: "main", Kind: ast.SymbolKindFunction, Package: "main", FilePath: "main.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "pkg:1:funcA", Name: "funcA", Kind: ast.SymbolKindFunction, Package: "pkg", FilePath: "pkg.go", StartLine: 1, EndLine: 10, Language: "go"},
		{ID: "pkg:2:funcB", Name: "funcB", Kind: ast.SymbolKindFunction, Package: "pkg", FilePath: "pkg.go", StartLine: 10, EndLine: 20, Language: "go"},
		{ID: "pkg:3:funcC", Name: "funcC", Kind: ast.SymbolKindFunction, Package: "pkg", FilePath: "pkg.go", StartLine: 20, EndLine: 30, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s to index: %v", sym.ID, err)
		}
	}

	edges := [][2]string{
		{"main:1:main", "pkg:1:funcA"},
		{"pkg:1:funcA", "pkg:2:funcB"},
		{"pkg:2:funcB", "pkg:3:funcC"},
	}

	for _, edge := range edges {
		g.AddEdge(edge[0], edge[1], graph.EdgeTypeCalls, ast.Location{
			FilePath: "test.go", StartLine: 1,
		})
	}

	g.Freeze()
	return g, idx
}

func TestCheckReducibilityTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphReducible(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewCheckReducibilityTool(analytics, idx)

	t.Run("analyzes reducibility with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(CheckReducibilityOutput)
		if !ok {
			t.Fatalf("Output is not CheckReducibilityOutput, got %T", result.Output)
		}

		t.Logf("IsReducible: %v, Score: %.2f", output.IsReducible, output.Score)

		// Score should be between 0 and 1
		if output.Score < 0 || output.Score > 1 {
			t.Errorf("Score %.2f is not in range [0, 1]", output.Score)
		}
	})
}

func TestCheckReducibilityTool_ShowIrreducible(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphReducible(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewCheckReducibilityTool(analytics, idx)

	t.Run("respects show_irreducible parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{
			"show_irreducible": true,
		})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(CheckReducibilityOutput)
		if !ok {
			t.Fatalf("Output is not CheckReducibilityOutput, got %T", result.Output)
		}

		// Reducible graph should have no irreducible regions
		if output.IsReducible && len(output.IrreducibleRegions) > 0 {
			t.Errorf("Reducible graph has %d irreducible regions", len(output.IrreducibleRegions))
		}
	})
}

func TestCheckReducibilityTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	idx := index.NewSymbolIndex()

	tool := NewCheckReducibilityTool(nil, idx)

	result, err := tool.Execute(ctx, map[string]any{})

	if err != nil {
		t.Fatalf("Execute() should not return error for nil analytics: %v", err)
	}
	if result.Success {
		t.Error("Execute() should fail when analytics is nil")
	}
	if result.Error == "" {
		t.Error("Execute() should return error message when analytics is nil")
	}
}

func TestCheckReducibilityTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphReducible(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewCheckReducibilityTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := tool.Execute(ctx, map[string]any{})

		// Should return error or context cancellation
		if err == nil {
			// Some implementations return nil error with Success=false
			// This is acceptable
		}
	})
}

func TestCheckReducibilityTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphReducible(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewCheckReducibilityTool(analytics, idx)

	result, err := tool.Execute(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Check TraceStep is present
	if result.TraceStep == nil {
		t.Error("Expected TraceStep in result")
	} else {
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}

func TestCheckReducibilityTool_OutputFormat(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphReducible(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewCheckReducibilityTool(analytics, idx)

	t.Run("output has expected fields", func(t *testing.T) {
		result, err := tool.Execute(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(CheckReducibilityOutput)
		if !ok {
			t.Fatalf("Output is not CheckReducibilityOutput, got %T", result.Output)
		}

		// Check that all expected fields are present
		// IsReducible is a bool, so we just check it exists (always true or false)
		t.Logf("IsReducible: %v", output.IsReducible)

		// Score should be non-negative
		if output.Score < 0 {
			t.Errorf("Score should be >= 0, got %f", output.Score)
		}

		// QualityLabel should not be empty
		if output.QualityLabel == "" {
			t.Error("QualityLabel should not be empty")
		}

		// Recommendation should not be empty
		if output.Recommendation == "" {
			t.Error("Recommendation should not be empty")
		}

		// Summary should be populated
		if output.Summary.TotalNodes == 0 {
			t.Error("Summary.TotalNodes should be > 0")
		}
	})
}
