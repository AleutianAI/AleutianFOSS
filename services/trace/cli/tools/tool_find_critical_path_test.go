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

// =============================================================================
// find_critical_path Tool Tests (GR-18a)
// =============================================================================

// createTestGraphForCriticalPath creates a graph with known structure.
// Structure:
//
//	main (entry)
//	  |
//	 init
//	  |
//	  A
//	 / \
//	B   C
//	|
//	D
//
// Critical paths:
// - main → D: main → init → A → B → D
// - main → C: main → init → A → C
// - main → init: main → init
func createTestGraphForCriticalPath(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
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
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s: %v", sym.Name, err)
		}
	}

	// Build call edges: main -> init -> A -> B -> D
	//                                  A -> C
	g.AddEdge("cmd/main.go:10:main", "pkg/init.go:10:init", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	g.AddEdge("pkg/init.go:10:init", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/init.go", StartLine: 15})
	g.AddEdge("pkg/a.go:10:A", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/a.go:10:A", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 16})
	g.AddEdge("pkg/b.go:10:B", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})

	g.Freeze()

	return g, idx
}

func TestFindCriticalPathTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCriticalPath(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCriticalPathTool(analytics, idx)

	t.Run("finds critical path from entry to deep target", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "D",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCriticalPathOutput)
		if !ok {
			t.Fatalf("Output is not FindCriticalPathOutput: %T", result.Output)
		}

		// Critical path: main → init → A → B → D
		expectedLength := 5
		if output.PathLength != expectedLength {
			t.Errorf("Expected path length %d, got %d", expectedLength, output.PathLength)
		}

		if len(output.CriticalPath) != expectedLength {
			t.Fatalf("Expected %d path nodes, got %d", expectedLength, len(output.CriticalPath))
		}

		// Verify path order
		expectedNames := []string{"main", "init", "A", "B", "D"}
		for i, node := range output.CriticalPath {
			if node.Name != expectedNames[i] {
				t.Errorf("Path[%d]: expected name %q, got %q", i, expectedNames[i], node.Name)
			}
			if node.Depth != i {
				t.Errorf("Path[%d]: expected depth %d, got %d", i, i, node.Depth)
			}
			if !node.IsMandatory {
				t.Errorf("Path[%d]: expected IsMandatory=true", i)
			}
		}

		// Verify reasons
		if output.CriticalPath[0].Reason != "Entry point" {
			t.Errorf("First node should have reason 'Entry point', got %q", output.CriticalPath[0].Reason)
		}
		if output.CriticalPath[len(output.CriticalPath)-1].Reason != "Target" {
			t.Errorf("Last node should have reason 'Target', got %q", output.CriticalPath[len(output.CriticalPath)-1].Reason)
		}
	})

	t.Run("finds shorter critical path to different target", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "C",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCriticalPathOutput)
		if !ok {
			t.Fatalf("Output is not FindCriticalPathOutput")
		}

		// Critical path: main → init → A → C
		expectedLength := 4
		if output.PathLength != expectedLength {
			t.Errorf("Expected path length %d, got %d", expectedLength, output.PathLength)
		}

		expectedNames := []string{"main", "init", "A", "C"}
		for i, node := range output.CriticalPath {
			if node.Name != expectedNames[i] {
				t.Errorf("Path[%d]: expected name %q, got %q", i, expectedNames[i], node.Name)
			}
		}
	})

	t.Run("auto-detects entry point", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "D",
			// entry not provided - should auto-detect
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCriticalPathOutput)
		if !ok {
			t.Fatalf("Output is not FindCriticalPathOutput")
		}

		// Should auto-detect "main" as entry
		if output.Entry != "cmd/main.go:10:main" {
			t.Errorf("Expected entry 'cmd/main.go:10:main', got %q", output.Entry)
		}

		// Path should still be correct
		if output.PathLength != 5 {
			t.Errorf("Expected path length 5, got %d", output.PathLength)
		}
	})

	t.Run("handles direct path (entry to immediate child)", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "init",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCriticalPathOutput)
		if !ok {
			t.Fatalf("Output is not FindCriticalPathOutput")
		}

		// Critical path: main → init
		expectedLength := 2
		if output.PathLength != expectedLength {
			t.Errorf("Expected path length %d, got %d", expectedLength, output.PathLength)
		}

		expectedNames := []string{"main", "init"}
		for i, node := range output.CriticalPath {
			if node.Name != expectedNames[i] {
				t.Errorf("Path[%d]: expected name %q, got %q", i, expectedNames[i], node.Name)
			}
		}
	})

	t.Run("handles unreachable target", func(t *testing.T) {
		// Add an isolated node
		isolated := &ast.Symbol{
			ID:        "pkg/isolated.go:10:Isolated",
			Name:      "Isolated",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/isolated.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "pkg",
			Exported:  true,
			Language:  "go",
		}
		g.AddNode(isolated)
		if err := idx.Add(isolated); err != nil {
			t.Fatalf("Failed to add isolated symbol: %v", err)
		}
		g.Freeze()

		// Re-wrap graph
		hg, err := graph.WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph failed: %v", err)
		}
		analytics := graph.NewGraphAnalytics(hg)
		tool := NewFindCriticalPathTool(analytics, idx)

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Isolated",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed even with unreachable target")
		}

		output, ok := result.Output.(FindCriticalPathOutput)
		if !ok {
			t.Fatalf("Output is not FindCriticalPathOutput")
		}

		// Path should be empty for unreachable target
		if output.PathLength != 0 {
			t.Errorf("Expected path length 0 for unreachable target, got %d", output.PathLength)
		}

		if len(output.CriticalPath) != 0 {
			t.Errorf("Expected empty critical path for unreachable target, got %d nodes", len(output.CriticalPath))
		}

		if !strings.Contains(output.Explanation, "not reachable") {
			t.Errorf("Explanation should mention 'not reachable', got: %q", output.Explanation)
		}
	})
}

func TestFindCriticalPathTool_ParameterValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCriticalPath(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCriticalPathTool(analytics, idx)

	t.Run("missing target parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"entry": "cmd/main.go:10:main",
			// target missing
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Errorf("Execute() should fail when target is missing")
		}
		if !strings.Contains(result.Error, "target") {
			t.Errorf("Error should mention 'target', got: %q", result.Error)
		}
	})

	t.Run("empty target parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Errorf("Execute() should fail when target is empty")
		}
		if !strings.Contains(result.Error, "target") {
			t.Errorf("Error should mention 'target', got: %q", result.Error)
		}
	})

	t.Run("invalid target type", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": 123, // number instead of string
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Errorf("Execute() should fail when target is not a string")
		}
		if !strings.Contains(result.Error, "string") {
			t.Errorf("Error should mention 'string', got: %q", result.Error)
		}
	})

	t.Run("target not found in graph", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "NonExistent",
			"entry":  "cmd/main.go:10:main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Errorf("Execute() should fail when target not found in graph")
		}
		if !strings.Contains(result.Error, "not found") {
			t.Errorf("Error should mention 'not found', got: %q", result.Error)
		}
	})

	t.Run("entry not found in graph", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "D",
			"entry":  "NonExistent",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Errorf("Execute() should fail when entry not found in graph")
		}
		if !strings.Contains(result.Error, "not found") {
			t.Errorf("Error should mention 'not found', got: %q", result.Error)
		}
	})
}

func TestFindCriticalPathTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForCriticalPath(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCriticalPathTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "D",
			"entry":  "cmd/main.go:10:main",
		}})

		// Should return error when context is canceled
		if err == nil && result.Success {
			t.Errorf("Execute() should respect context cancellation")
		}
	})
}

func TestFindCriticalPathTool_TextOutput(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCriticalPath(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCriticalPathTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"target": "D",
		"entry":  "cmd/main.go:10:main",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check text output contains expected elements
	text := result.OutputText
	if !strings.Contains(text, "Critical Path") {
		t.Errorf("OutputText should contain 'Critical Path'")
	}
	if !strings.Contains(text, "main") {
		t.Errorf("OutputText should contain 'main'")
	}
	if !strings.Contains(text, "D") {
		t.Errorf("OutputText should contain target 'D'")
	}
	if !strings.Contains(text, "→") {
		t.Errorf("OutputText should contain path arrows '→'")
	}

	// Check explanation
	output := result.Output.(FindCriticalPathOutput)
	if !strings.Contains(output.Explanation, "MUST call") {
		t.Errorf("Explanation should mention 'MUST call'")
	}
}

func TestFindCriticalPathTool_TraceStepIntegration(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCriticalPath(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCriticalPathTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"target": "D",
		"entry":  "cmd/main.go:10:main",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify TraceStep is included
	if result.TraceStep == nil {
		t.Errorf("Result should include TraceStep for CRS integration")
	}

	// Verify Duration is set
	if result.Duration == 0 {
		t.Errorf("Result should include Duration")
	}

	// Verify TokensUsed is estimated
	if result.TokensUsed == 0 {
		t.Errorf("Result should include TokensUsed estimation")
	}
}
