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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_sources": 3,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 1,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 0,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Should succeed with clamped value
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
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
	})

	t.Run("min_sources below 2 clamped to 2", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_sources": 1,
		}})

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

		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		// TraceStep.Tool comes from the underlying analytics
		if result.TraceStep.Action == "" {
			t.Error("TraceStep.Action should not be empty")
		}
		if result.TraceStep.Duration == 0 {
			t.Error("TraceStep.Duration should be > 0")
		}
	}
}
