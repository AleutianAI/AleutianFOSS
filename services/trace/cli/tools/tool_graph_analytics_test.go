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
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 2,
		}})

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
		result, err := nilTool.Execute(ctx, MapParams{Params: map[string]any{}})

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

		_, err := tool.Execute(cancelCtx, MapParams{Params: map[string]any{}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_exported": true,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "util",
		}})

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
		result, err := nilTool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 3, // Filter out 2-node cycles
		}})

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
		result, err := nilTool.Execute(ctx, MapParams{Params: map[string]any{}})

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

		_, err := tool.Execute(cancelCtx, MapParams{Params: map[string]any{}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "funcB",
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "funcD", // Dead code, not connected
			"to":   "main",
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "nonExistent",
			"to":   "main",
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "nonExistent",
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"to": "main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Expected failure without from parameter")
		}
	})

	t.Run("requires to parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
		}})

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

		_, err := tool.Execute(cancelCtx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "funcB",
		}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 2,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 1000,
		}})

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
		result, err := nilTool.Execute(ctx, MapParams{Params: map[string]any{}})

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

		_, err := tool.Execute(cancelCtx, MapParams{Params: map[string]any{}})
		if err == nil {
			t.Error("Expected context.Canceled error")
		}
	})

	t.Run("returns result metadata", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
	importantResult, err := importantTool.Execute(ctx, MapParams{Params: map[string]any{"top": 6}})
	if err != nil {
		t.Fatalf("find_important Execute() error = %v", err)
	}

	hotspotsResult, err := hotspotsTool.Execute(ctx, MapParams{Params: map[string]any{"top": 6}})
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

// TestFindImportantTool_MatchesKind verifies kind filter consistency (IT-08c).
func TestFindImportantTool_MatchesKind(t *testing.T) {
	tool := &findImportantTool{}

	t.Run("function filter includes Property", func(t *testing.T) {
		if !tool.matchesKind(ast.SymbolKindProperty, "function") {
			t.Error("function filter should include Property (Python @property)")
		}
	})

	t.Run("function filter includes Function and Method", func(t *testing.T) {
		if !tool.matchesKind(ast.SymbolKindFunction, "function") {
			t.Error("function filter should include Function")
		}
		if !tool.matchesKind(ast.SymbolKindMethod, "function") {
			t.Error("function filter should include Method")
		}
	})

	t.Run("function filter rejects Class", func(t *testing.T) {
		if tool.matchesKind(ast.SymbolKindClass, "function") {
			t.Error("function filter should reject Class")
		}
	})

	t.Run("type filter includes Class", func(t *testing.T) {
		if !tool.matchesKind(ast.SymbolKindClass, "type") {
			t.Error("type filter should include Class (JS/TS/Python)")
		}
	})

	t.Run("type filter includes Interface Struct Type", func(t *testing.T) {
		for _, kind := range []ast.SymbolKind{
			ast.SymbolKindInterface, ast.SymbolKindStruct, ast.SymbolKindType,
		} {
			if !tool.matchesKind(kind, "type") {
				t.Errorf("type filter should include %s", kind)
			}
		}
	})

	t.Run("type filter rejects Function", func(t *testing.T) {
		if tool.matchesKind(ast.SymbolKindFunction, "type") {
			t.Error("type filter should reject Function")
		}
	})

	t.Run("all filter accepts everything", func(t *testing.T) {
		for _, kind := range []ast.SymbolKind{
			ast.SymbolKindFunction, ast.SymbolKindMethod, ast.SymbolKindProperty,
			ast.SymbolKindClass, ast.SymbolKindStruct, ast.SymbolKindInterface,
			ast.SymbolKindVariable, ast.SymbolKindEnum,
		} {
			if !tool.matchesKind(kind, "all") {
				t.Errorf("all filter should accept %s", kind)
			}
		}
	})
}

// TestIsDocFile verifies documentation file detection.
func TestIsDocFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Documentation directories
		{"doc/source/conf.py", true},
		{"docs/api/README.md", true},
		{"documentation/guide.rst", true},
		{"examples/basic.py", true},
		{"example/demo.go", true},
		{"src/docs/internal.py", true},

		// Documentation file extensions
		{"README.md", true},
		{"CHANGELOG.rst", true},
		{"notes.txt", true},
		{"index.html", true},
		{"style.css", true},
		{"config.json", true},
		{"settings.yaml", true},
		{"data.csv", true},
		{"logo.svg", true},
		{"icon.png", true},

		// Source files (should NOT be doc files)
		{"main.go", false},
		{"app.py", false},
		{"index.js", false},
		{"component.tsx", false},
		{"core/handler.go", false},
		{"src/utils.ts", false},
		{"pandas/core/frame.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isDocFile(tt.path)
			if got != tt.want {
				t.Errorf("isDocFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestFindImportantTool_ExcludeTests verifies test and doc file filtering in find_important.
func TestFindImportantTool_ExcludeTests(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create graph with source, test, and doc file symbols
	symbols := []*ast.Symbol{
		{
			ID: "core/handler.go:10:Handle", Name: "Handle",
			Kind: ast.SymbolKindFunction, FilePath: "core/handler.go",
			StartLine: 10, EndLine: 30, Package: "core", Exported: true, Language: "go",
		},
		{
			ID: "core/handler_test.go:10:TestHandle", Name: "TestHandle",
			Kind: ast.SymbolKindFunction, FilePath: "core/handler_test.go",
			StartLine: 10, EndLine: 30, Package: "core", Exported: true, Language: "go",
		},
		{
			ID: "doc/source/conf.py:10:setup", Name: "setup",
			Kind: ast.SymbolKindFunction, FilePath: "doc/source/conf.py",
			StartLine: 10, EndLine: 20, Package: "conf", Exported: false, Language: "python",
		},
		{
			ID: "core/router.go:10:Route", Name: "Route",
			Kind: ast.SymbolKindFunction, FilePath: "core/router.go",
			StartLine: 10, EndLine: 40, Package: "core", Exported: true, Language: "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// TestHandle calls Handle (making Handle important via test dependency)
	g.AddEdge("core/handler_test.go:10:TestHandle", "core/handler.go:10:Handle", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/handler_test.go", StartLine: 15,
	})
	// Route calls Handle
	g.AddEdge("core/router.go:10:Route", "core/handler.go:10:Handle", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/router.go", StartLine: 20,
	})
	// setup calls Handle (doc file dependency)
	g.AddEdge("doc/source/conf.py:10:setup", "core/handler.go:10:Handle", graph.EdgeTypeCalls, ast.Location{
		FilePath: "doc/source/conf.py", StartLine: 15,
	})

	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)

	t.Run("exclude_tests true filters test and doc files", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": true,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(FindImportantOutput)
		for _, sym := range output.Results {
			if isTestFile(sym.File) {
				t.Errorf("test file %q should be filtered out", sym.File)
			}
			if isDocFile(sym.File) {
				t.Errorf("doc file %q should be filtered out", sym.File)
			}
		}
	})

	t.Run("exclude_tests false includes everything", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(FindImportantOutput)
		// Should include all 4 symbols (test, doc, and source)
		if output.ResultCount < 3 {
			t.Errorf("with exclude_tests=false, expected at least 3 results, got %d", output.ResultCount)
		}
	})

	t.Run("ranks are sequential after filtering", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": true,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		output := result.Output.(FindImportantOutput)
		for i, sym := range output.Results {
			expectedRank := i + 1
			if sym.Rank != expectedRank {
				t.Errorf("result[%d].Rank = %d, want %d (sequential after filtering)", i, sym.Rank, expectedRank)
			}
		}
	})
}

// TestFindImportantTool_PackageFilter verifies CR-11/CR-12: package scope filtering.
func TestFindImportantTool_PackageFilter(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create symbols in two packages: "core" and "util"
	symbols := []*ast.Symbol{
		{
			ID: "core/handler.go:10:Handle", Name: "Handle",
			Kind: ast.SymbolKindFunction, FilePath: "core/handler.go",
			StartLine: 10, EndLine: 30, Package: "core", Exported: true, Language: "go",
		},
		{
			ID: "core/router.go:10:Route", Name: "Route",
			Kind: ast.SymbolKindFunction, FilePath: "core/router.go",
			StartLine: 10, EndLine: 40, Package: "core", Exported: true, Language: "go",
		},
		{
			ID: "util/helpers.go:10:FormatOutput", Name: "FormatOutput",
			Kind: ast.SymbolKindFunction, FilePath: "util/helpers.go",
			StartLine: 10, EndLine: 25, Package: "util", Exported: true, Language: "go",
		},
		{
			ID: "util/strings.go:5:TrimSafe", Name: "TrimSafe",
			Kind: ast.SymbolKindFunction, FilePath: "util/strings.go",
			StartLine: 5, EndLine: 15, Package: "util", Exported: true, Language: "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Create call edges so PageRank has non-zero scores
	g.AddEdge("core/router.go:10:Route", "core/handler.go:10:Handle", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/router.go", StartLine: 20,
	})
	g.AddEdge("core/handler.go:10:Handle", "util/helpers.go:10:FormatOutput", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/handler.go", StartLine: 15,
	})
	g.AddEdge("util/helpers.go:10:FormatOutput", "util/strings.go:5:TrimSafe", graph.EdgeTypeCalls, ast.Location{
		FilePath: "util/helpers.go", StartLine: 12,
	})

	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)

	t.Run("empty package returns all results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindImportantOutput)
		if output.ResultCount < 3 {
			t.Errorf("empty package filter should return all results, got %d", output.ResultCount)
		}
	})

	t.Run("package filter returns only matching symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "util",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindImportantOutput)
		if output.ResultCount == 0 {
			t.Fatal("expected results for package 'util', got 0")
		}
		for _, sym := range output.Results {
			if sym.Package != "util" {
				t.Errorf("expected package 'util', got %q for symbol %s", sym.Package, sym.Name)
			}
		}
	})

	t.Run("nonexistent package falls back to global results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "nonexistent",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindImportantOutput)
		// IT-Summary FIX-B: when package filter matches nothing but global results
		// exist, the tool drops the filter and returns all results (4 symbols).
		if output.ResultCount == 0 {
			t.Error("expected fallback to global results for nonexistent package, got 0")
		}
	})

	t.Run("package filter scope mentioned in output text", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "core",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// CR-12: output text should mention the package scope
		if !strings.Contains(result.OutputText, "package 'core'") {
			t.Errorf("output text should mention package scope 'core', got:\n%s", result.OutputText)
		}
	})

	t.Run("no-match package fallback output does not mention cleared package", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "nonexistent",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// IT-Summary FIX-B: p.Package is cleared on fallback, so output text
		// should NOT mention "nonexistent" — it returns unscoped global results.
		if strings.Contains(result.OutputText, "nonexistent") {
			t.Errorf("fallback output should not mention cleared package 'nonexistent', got:\n%s", result.OutputText)
		}
		// Should have positive results with "Found" prefix
		if !strings.HasPrefix(result.OutputText, "Found ") {
			t.Errorf("expected 'Found ' prefix after fallback, got: %q",
				result.OutputText[:min(80, len(result.OutputText))])
		}
	})

	t.Run("ranks are sequential after package filtering", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "core",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output := result.Output.(FindImportantOutput)
		for i, sym := range output.Results {
			expectedRank := i + 1
			if sym.Rank != expectedRank {
				t.Errorf("result[%d].Rank = %d, want %d", i, sym.Rank, expectedRank)
			}
		}
	})
}

// TestMatchesHotspotKind_Property verifies hotspot kind filter includes Property (IT-08c).
func TestMatchesHotspotKind_Property(t *testing.T) {
	if !matchesHotspotKind(ast.SymbolKindProperty, "function") {
		t.Error("hotspot function filter should include Property (Python @property)")
	}
	if !matchesHotspotKind(ast.SymbolKindProperty, "method") {
		t.Error("hotspot method filter should include Property (Python @property)")
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
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": 10}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}
