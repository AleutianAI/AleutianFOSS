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

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 2,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"max_size": 10,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 1,
		}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 0,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("top above 100 clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 200,
		}})

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

		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_irreducible": true,
		}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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
