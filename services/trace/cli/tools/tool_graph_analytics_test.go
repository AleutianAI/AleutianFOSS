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
