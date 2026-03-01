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

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 2,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_bridges": true,
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_bridges": false,
		}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

	_, err = tool.Execute(ctx, MapParams{Params: map[string]any{}})
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		params MapParams
	}{
		{"top=1 (lower bound)", MapParams{Params: map[string]any{"top": 1}}},
		{"top=100 (upper bound)", MapParams{Params: map[string]any{"top": 100}}},
		{"include_bridges=true", MapParams{Params: map[string]any{"include_bridges": true}}},
		{"include_bridges=false", MapParams{Params: map[string]any{"include_bridges": false}}},
		{"both params", MapParams{Params: map[string]any{"top": 5, "include_bridges": true}}},
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"include_bridges": true,
	}})

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
			result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
				"top":             idx%5 + 1,
				"include_bridges": idx%2 == 0,
			}})
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": 10}})
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
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": 0}})
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
	result2, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": -5}})
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
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": 500}})
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

			result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"include_bridges": true}})
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"include_bridges": true}})
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
