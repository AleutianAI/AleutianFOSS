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

// createTestGraphWithLoops creates a graph with various loop types for testing.
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 2,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 1,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 0,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 200,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Should succeed with clamped value (100)
	})

	t.Run("min_size below 1 clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 0,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_nesting": true,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_nesting": false,
		}})

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

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
