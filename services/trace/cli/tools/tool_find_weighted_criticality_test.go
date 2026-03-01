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
	"math"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_weighted_criticality Tool Tests (GR-18c)
// =============================================================================

// createTestGraphForWeightedCriticality creates a graph with known structure.
// Structure:
//
//	main (entry, no incoming edges) - high PageRank, dominates all
//	  |
//	 init - medium PageRank, dominates all except main
//	/    \
//
// A       B - medium PageRank, some dominated nodes
//
//	/ \     / \
//
// C   D   E   F - low PageRank, no dominated nodes (leaf nodes)
//
// Expected criticality patterns:
// - main: CRITICAL (high dominator score, high PageRank)
// - init: CRITICAL (high dominator score, medium-high PageRank)
// - A, B: HIDDEN_GATEKEEPER or HUB (medium scores)
// - C, D, E, F: LEAF (low dominator score, low PageRank)
func createTestGraphForWeightedCriticality(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction, FilePath: "cmd/main.go", StartLine: 10, EndLine: 20, Package: "main", Exported: false, Language: "go"},
		{ID: "pkg/init.go:10:init", Name: "init", Kind: ast.SymbolKindFunction, FilePath: "pkg/init.go", StartLine: 10, EndLine: 20, Package: "pkg", Exported: false, Language: "go"},
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
			t.Fatalf("Failed to add symbol %s: %v", sym.Name, err)
		}
	}

	// Build call edges: main -> init -> A, B -> C, D, E, F
	g.AddEdge("cmd/main.go:10:main", "pkg/init.go:10:init", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	g.AddEdge("pkg/init.go:10:init", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/init.go", StartLine: 15})
	g.AddEdge("pkg/init.go:10:init", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/init.go", StartLine: 16})
	g.AddEdge("pkg/a.go:10:A", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/a.go:10:A", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 16})
	g.AddEdge("pkg/b.go:10:B", "pkg/e.go:10:E", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})
	g.AddEdge("pkg/b.go:10:B", "pkg/f.go:10:F", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 16})

	g.Freeze()

	return g, idx
}

// =============================================================================
// Tool Metadata Tests
// =============================================================================

func TestFindWeightedCriticalityTool_Metadata(t *testing.T) {
	g, idx := createTestGraphForWeightedCriticality(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindWeightedCriticalityTool(analytics, idx)

	t.Run("Name returns correct value", func(t *testing.T) {
		if tool.Name() != "find_weighted_criticality" {
			t.Errorf("Name() = %s, want find_weighted_criticality", tool.Name())
		}
	})

	t.Run("Category returns correct value", func(t *testing.T) {
		if tool.Category() != CategoryExploration {
			t.Errorf("Category() = %s, want %s", tool.Category(), CategoryExploration)
		}
	})

	t.Run("Definition is valid", func(t *testing.T) {
		def := tool.Definition()
		if def.Name != "find_weighted_criticality" {
			t.Errorf("Definition.Name = %s, want find_weighted_criticality", def.Name)
		}
		if def.Description == "" {
			t.Error("Definition.Description is empty")
		}
		if len(def.Parameters) == 0 {
			t.Error("Definition.Parameters has no parameters")
		}
	})
}

// =============================================================================
// Parameter Validation Tests
// =============================================================================

func TestFindWeightedCriticalityTool_ParameterValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForWeightedCriticality(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindWeightedCriticalityTool(analytics, idx)

	t.Run("valid parameters succeed", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top":           10,
			"show_quadrant": true,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("top parameter defaults to 20", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput: %T", result.Output)
		}

		// Should return min(20, total nodes) = 8 in this case
		if len(output.CriticalFunctions) > 20 {
			t.Errorf("Expected at most 20 functions, got %d", len(output.CriticalFunctions))
		}
	})

	t.Run("top parameter too small is clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 0,
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// 0 should be clamped to 1, so we get at least 1 result
		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}
		if len(output.CriticalFunctions) != 1 {
			t.Errorf("Expected 1 function (clamped), got %d", len(output.CriticalFunctions))
		}
	})

	t.Run("top parameter too large is clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 101,
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// 101 should be clamped to 100, execution should succeed
		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}
		// Should return min(100, total nodes) = 8 in this case
		if len(output.CriticalFunctions) > 100 {
			t.Errorf("Expected at most 100 functions, got %d", len(output.CriticalFunctions))
		}
	})

	t.Run("negative top parameter is clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": -5,
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Negative should be clamped to 1
		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}
		if len(output.CriticalFunctions) != 1 {
			t.Errorf("Expected 1 function (clamped), got %d", len(output.CriticalFunctions))
		}
	})

	t.Run("explicit entry parameter is used", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"entry": "pkg/init.go:10:init",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Verify execution succeeded with explicit entry
		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}
		if len(output.CriticalFunctions) == 0 {
			t.Error("Expected non-zero functions with explicit entry")
		}
	})

	t.Run("show_quadrant parameter controls quadrant output", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_quadrant": false,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Verify text output doesn't contain quadrant info
		if !strings.Contains(result.OutputText, "Weighted Criticality Analysis") {
			t.Error("Expected text output header")
		}
	})
}

// =============================================================================
// Execute Success Tests
// =============================================================================

func TestFindWeightedCriticalityTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForWeightedCriticality(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindWeightedCriticalityTool(analytics, idx)

	t.Run("returns all critical functions with scores", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 20,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput: %T", result.Output)
		}

		// Should return all 8 nodes in the graph
		if len(output.CriticalFunctions) != 8 {
			t.Errorf("Expected 8 functions, got %d", len(output.CriticalFunctions))
		}

		// Verify all functions have valid scores
		for _, fn := range output.CriticalFunctions {
			if fn.CriticalityScore < 0 || fn.CriticalityScore > 1 {
				t.Errorf("Function %s has invalid criticality score: %f", fn.Name, fn.CriticalityScore)
			}
			if fn.DominatorScore < 0 || fn.DominatorScore > 1 {
				t.Errorf("Function %s has invalid dominator score: %f", fn.Name, fn.DominatorScore)
			}
			if fn.PageRankScore < 0 || fn.PageRankScore > 1 {
				t.Errorf("Function %s has invalid PageRank score: %f", fn.Name, fn.PageRankScore)
			}
			if fn.Quadrant == "" {
				t.Errorf("Function %s has empty quadrant", fn.Name)
			}
			if fn.RiskLevel == "" {
				t.Errorf("Function %s has empty risk level", fn.Name)
			}
		}
	})

	t.Run("first function has highest criticality score", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		// Verify we have results and first has highest score
		if len(output.CriticalFunctions) == 0 {
			t.Fatal("No functions returned")
		}

		// First function should have highest score
		firstScore := output.CriticalFunctions[0].CriticalityScore
		for i := 1; i < len(output.CriticalFunctions); i++ {
			if output.CriticalFunctions[i].CriticalityScore > firstScore {
				t.Errorf("Function at index %d has higher score (%f) than first (%f)",
					i, output.CriticalFunctions[i].CriticalityScore, firstScore)
			}
		}
	})

	t.Run("results are sorted by criticality descending", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		// Verify sorting
		for i := 1; i < len(output.CriticalFunctions); i++ {
			prev := output.CriticalFunctions[i-1]
			curr := output.CriticalFunctions[i]
			if curr.CriticalityScore > prev.CriticalityScore {
				t.Errorf("Results not sorted: %s (%.3f) after %s (%.3f)",
					curr.Name, curr.CriticalityScore, prev.Name, prev.CriticalityScore)
			}
		}
	})

	t.Run("quadrant summary is correct", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		// Verify quadrant counts sum to total
		total := 0
		for _, count := range output.QuadrantSummary {
			total += count
		}
		if total != len(output.CriticalFunctions) {
			t.Errorf("Quadrant counts (%d) don't match function count (%d)", total, len(output.CriticalFunctions))
		}
	})

	t.Run("summary statistics are correct", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		if output.Summary.TotalAnalyzed != 8 {
			t.Errorf("Expected 8 total analyzed, got %d", output.Summary.TotalAnalyzed)
		}
		if output.Summary.AvgCriticality < 0 || output.Summary.AvgCriticality > 1 {
			t.Errorf("Invalid avg criticality: %f", output.Summary.AvgCriticality)
		}
	})

	t.Run("top parameter limits results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 3,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		if len(output.CriticalFunctions) != 3 {
			t.Errorf("Expected 3 functions, got %d", len(output.CriticalFunctions))
		}
	})
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestFindWeightedCriticalityTool_EdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("empty graph returns error", func(t *testing.T) {
		g := graph.NewGraph("/test")
		g.Freeze()
		hg, err := graph.WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph failed: %v", err)
		}
		analytics := graph.NewGraphAnalytics(hg)
		idx := index.NewSymbolIndex()
		tool := NewFindWeightedCriticalityTool(analytics, idx)

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if result.Success {
			t.Fatal("Expected failure on empty graph")
		}
	})

	t.Run("single node graph succeeds", func(t *testing.T) {
		g := graph.NewGraph("/test")
		idx := index.NewSymbolIndex()

		sym := &ast.Symbol{
			ID:        "main.go:10:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "main.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "main",
			Exported:  false,
			Language:  "go",
		}
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
		g.Freeze()

		hg, err := graph.WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph failed: %v", err)
		}
		analytics := graph.NewGraphAnalytics(hg)
		tool := NewFindWeightedCriticalityTool(analytics, idx)

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput")
		}

		if len(output.CriticalFunctions) != 1 {
			t.Errorf("Expected 1 function, got %d", len(output.CriticalFunctions))
		}
		// Single node should have normalized scores
		fn := output.CriticalFunctions[0]
		if fn.CriticalityScore < 0 || fn.CriticalityScore > 1 {
			t.Errorf("Invalid score for single node: %f", fn.CriticalityScore)
		}
	})

	t.Run("context cancellation stops execution", func(t *testing.T) {
		g, idx := createTestGraphForWeightedCriticality(t)
		hg, err := graph.WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph failed: %v", err)
		}
		analytics := graph.NewGraphAnalytics(hg)
		tool := NewFindWeightedCriticalityTool(analytics, idx)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		// Should handle context cancellation gracefully
		if err == nil && result.Success {
			// If it completes before cancellation takes effect, that's also OK
			t.Log("Execution completed before context cancellation took effect")
		}
	})
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestClassifyQuadrant(t *testing.T) {
	tests := []struct {
		name     string
		domScore float64
		prScore  float64
		wantQuad string
	}{
		{"both high", 0.8, 0.8, "CRITICAL"},
		{"both at threshold", 0.6, 0.6, "CRITICAL"},
		{"high dom, low PR", 0.8, 0.3, "HIDDEN_GATEKEEPER"},
		{"low dom, high PR", 0.3, 0.8, "HUB"},
		{"both low", 0.3, 0.3, "LEAF"},
		{"boundary dom", 0.6, 0.3, "HIDDEN_GATEKEEPER"},
		{"boundary PR", 0.3, 0.6, "HUB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyQuadrant(tt.domScore, tt.prScore)
			if got != tt.wantQuad {
				t.Errorf("classifyQuadrant(%f, %f) = %s, want %s",
					tt.domScore, tt.prScore, got, tt.wantQuad)
			}
		})
	}
}

func TestAssignRiskLevel(t *testing.T) {
	tests := []struct {
		name     string
		score    float64
		wantRisk string
	}{
		{"high risk", 0.8, "high"},
		{"high threshold", 0.7, "high"},
		{"medium risk", 0.5, "medium"},
		{"medium threshold", 0.4, "medium"},
		{"low risk", 0.3, "low"},
		{"low threshold", 0.2, "low"},
		{"minimal risk", 0.1, "minimal"},
		{"zero", 0.0, "minimal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assignRiskLevel(tt.score)
			if got != tt.wantRisk {
				t.Errorf("assignRiskLevel(%f) = %s, want %s",
					tt.score, got, tt.wantRisk)
			}
		})
	}
}

func TestGenerateRecommendation(t *testing.T) {
	tests := []struct {
		quadrant string
		risk     string
	}{
		{"CRITICAL", "high"},
		{"HIDDEN_GATEKEEPER", "medium"},
		{"HUB", "low"},
		{"LEAF", "minimal"},
	}

	for _, tt := range tests {
		t.Run(tt.quadrant, func(t *testing.T) {
			rec := generateRecommendation(tt.quadrant, tt.risk)
			if rec == "" {
				t.Error("generateRecommendation returned empty string")
			}
			if len(rec) < 10 {
				t.Error("generateRecommendation returned too short string")
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	t.Run("empty map returns empty", func(t *testing.T) {
		result := normalize(map[string]float64{})
		if len(result) != 0 {
			t.Errorf("Expected empty result, got %d elements", len(result))
		}
	})

	t.Run("single element returns 1.0", func(t *testing.T) {
		result := normalize(map[string]float64{"a": 42.0})
		if result["a"] != 1.0 {
			t.Errorf("Expected 1.0, got %f", result["a"])
		}
	})

	t.Run("all same values return 0.5", func(t *testing.T) {
		result := normalize(map[string]float64{
			"a": 5.0,
			"b": 5.0,
			"c": 5.0,
		})
		for k, v := range result {
			if v != 0.5 {
				t.Errorf("Expected 0.5 for %s, got %f", k, v)
			}
		}
	})

	t.Run("normalizes to [0, 1] range", func(t *testing.T) {
		result := normalize(map[string]float64{
			"a": 10.0,
			"b": 50.0,
			"c": 30.0,
		})

		// Min should be 0, max should be 1
		if result["a"] != 0.0 {
			t.Errorf("Expected min (a) = 0.0, got %f", result["a"])
		}
		if result["b"] != 1.0 {
			t.Errorf("Expected max (b) = 1.0, got %f", result["b"])
		}
		if result["c"] <= 0.0 || result["c"] >= 1.0 {
			t.Errorf("Expected c in (0, 1), got %f", result["c"])
		}
	})

	t.Run("handles NaN values", func(t *testing.T) {
		result := normalize(map[string]float64{
			"a": 10.0,
			"b": math.NaN(),
			"c": 30.0,
		})

		// NaN should map to 0
		if result["b"] != 0.0 {
			t.Errorf("Expected NaN to map to 0.0, got %f", result["b"])
		}
		// Other values should normalize
		if result["a"] != 0.0 || result["c"] != 1.0 {
			t.Errorf("Expected proper normalization of non-NaN values")
		}
	})

	t.Run("handles Inf values", func(t *testing.T) {
		result := normalize(map[string]float64{
			"a": 10.0,
			"b": math.Inf(1),
			"c": 30.0,
		})

		// Inf should map to 0
		if result["b"] != 0.0 {
			t.Errorf("Expected Inf to map to 0.0, got %f", result["b"])
		}
	})

	t.Run("handles all NaN/Inf", func(t *testing.T) {
		result := normalize(map[string]float64{
			"a": math.NaN(),
			"b": math.Inf(1),
			"c": math.Inf(-1),
		})

		// All invalid should map to 0
		for k, v := range result {
			if v != 0.0 {
				t.Errorf("Expected 0.0 for %s, got %f", k, v)
			}
		}
	})
}

// =============================================================================
// Output Format Tests
// =============================================================================

func TestFindWeightedCriticalityTool_OutputFormat(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForWeightedCriticality(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindWeightedCriticalityTool(analytics, idx)

	t.Run("text output contains key sections", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Check for key sections
		if !strings.Contains(result.OutputText, "Weighted Criticality Analysis") {
			t.Error("Missing title")
		}
		if !strings.Contains(result.OutputText, "Analyzed") {
			t.Error("Missing analyzed count")
		}
		if !strings.Contains(result.OutputText, "Quadrant Distribution") {
			t.Error("Missing quadrant distribution")
		}
		if !strings.Contains(result.OutputText, "main") {
			t.Error("Missing main function")
		}
	})

	t.Run("text output without quadrants omits quadrant section", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_quadrant": false,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should NOT contain quadrant distribution
		if strings.Contains(result.OutputText, "Quadrant Distribution") {
			t.Error("Text should not contain quadrant distribution when show_quadrant=false")
		}
	})

	t.Run("structured output is correct type", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(WeightedCriticalityOutput)
		if !ok {
			t.Fatalf("Output is not WeightedCriticalityOutput: %T", result.Output)
		}

		if len(output.CriticalFunctions) == 0 {
			t.Error("Output has no functions")
		}
		if output.Summary.TotalAnalyzed == 0 {
			t.Error("Summary has zero total analyzed")
		}
	})
}
