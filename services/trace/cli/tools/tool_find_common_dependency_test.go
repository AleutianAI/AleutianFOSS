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
// find_common_dependency Tool Tests (GR-17f)
// =============================================================================

// createTestGraphForCommonDependency creates a graph with known dominator structure.
// Structure:
//
//	main (entry, no incoming edges)
//	  |
//	 init
//	/    \
//
// A       B
//
//	/ \     / \
//
// C   D   E   F (leaf nodes)
//
// Expected LCD:
// - LCD(C, D) = A
// - LCD(E, F) = B
// - LCD(C, E) = init
// - LCD(A, B) = init
// - LCD(C, D, E, F) = init
func createTestGraphForCommonDependency(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
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

func TestFindCommonDependencyTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("finds common dependency of two targets", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "D"},
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommonDependencyOutput)
		if !ok {
			t.Fatalf("Output is not FindCommonDependencyOutput: %T", result.Output)
		}

		// LCD of C and D should be A
		if output.LCD.Name != "A" {
			t.Errorf("Expected LCD name 'A', got '%s'", output.LCD.Name)
		}
	})

	t.Run("finds common dependency across branches", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "E"},
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommonDependencyOutput)
		if !ok {
			t.Fatalf("Output is not FindCommonDependencyOutput")
		}

		// LCD of C (under A) and E (under B) should be init
		if output.LCD.Name != "init" {
			t.Errorf("Expected LCD name 'init', got '%s'", output.LCD.Name)
		}
	})

	t.Run("finds common dependency of multiple targets", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "D", "E", "F"},
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommonDependencyOutput)
		if !ok {
			t.Fatalf("Output is not FindCommonDependencyOutput")
		}

		// LCD of all four should be init
		if output.LCD.Name != "init" {
			t.Errorf("Expected LCD name 'init', got '%s'", output.LCD.Name)
		}
	})
}

func TestFindCommonDependencyTool_MissingTargets(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("returns error when targets missing", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if result.Success {
			t.Fatal("Expected failure when targets missing")
		}
		if !strings.Contains(result.Error, "targets") {
			t.Errorf("Expected error about targets, got: %s", result.Error)
		}
	})

	t.Run("returns error when targets empty", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{},
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if result.Success {
			t.Fatal("Expected failure when targets empty")
		}
		if !strings.Contains(result.Error, "at least 2") {
			t.Errorf("Expected error about needing at least 2 targets, got: %s", result.Error)
		}
	})

	t.Run("returns error when only one target", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C"},
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if result.Success {
			t.Fatal("Expected failure when only one target")
		}
		if !strings.Contains(result.Error, "at least 2") {
			t.Errorf("Expected error about needing at least 2 targets, got: %s", result.Error)
		}
	})
}

func TestFindCommonDependencyTool_NonExistentTarget(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("handles non-existent target gracefully", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "NonExistent"},
		}})

		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		// Phase 11A: Tool returns Success=true with informational text when targets
		// are definitively verified as non-existent (graph is fully indexed).
		if !result.Success {
			t.Fatal("Expected success (informational result for verified non-existence)")
		}
		if !strings.Contains(result.OutputText, "Not found") && !strings.Contains(result.OutputText, "NonExistent") {
			t.Errorf("Expected informational text about non-existent target, got: %s", result.OutputText)
		}
	})
}

func TestFindCommonDependencyTool_WithEntry(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("uses specified entry point", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "D"},
			"entry":   "main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommonDependencyOutput)
		if !ok {
			t.Fatalf("Output is not FindCommonDependencyOutput")
		}

		// Entry should be reported
		if !strings.Contains(output.Entry, "main") {
			t.Errorf("Expected entry to contain 'main', got '%s'", output.Entry)
		}
	})
}

func TestFindCommonDependencyTool_NilAnalytics(t *testing.T) {
	tool := NewFindCommonDependencyTool(nil, nil)

	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"targets": []string{"A", "B"},
	}})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Expected failure with nil analytics")
	}
	if !strings.Contains(result.Error, "analytics") && !strings.Contains(result.Error, "graph") {
		t.Errorf("Expected error about analytics, got: %s", result.Error)
	}
}

func TestFindCommonDependencyTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "D"},
		}})

		// Should handle cancellation gracefully
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		// Either success (if fast enough) or failure due to cancellation
		if !result.Success && !strings.Contains(result.Error, "cancel") {
			// If failed for a different reason, that's unexpected
			t.Logf("Result: %+v", result)
		}
	})
}

func TestFindCommonDependencyTool_TraceStep(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"targets": []string{"C", "D"},
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

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

func TestFindCommonDependencyTool_OutputFormat(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommonDependency(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommonDependencyTool(analytics, idx)

	t.Run("output has expected fields", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"targets": []string{"C", "D"},
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommonDependencyOutput)
		if !ok {
			t.Fatalf("Output is not FindCommonDependencyOutput: %T", result.Output)
		}

		// Check required fields using typed access
		if output.LCD.ID == "" {
			t.Error("Output.LCD.ID should not be empty")
		}
		if output.LCD.Name == "" {
			t.Error("Output.LCD.Name should not be empty")
		}
		if len(output.Targets) == 0 {
			t.Error("Output.Targets should not be empty")
		}
		if output.Entry == "" {
			t.Error("Output.Entry should not be empty")
		}
		if output.TargetsResolved == 0 {
			t.Error("Output.TargetsResolved should be > 0")
		}
		if output.PathsToTargets == nil {
			t.Error("Output.PathsToTargets should not be nil")
		}
	})
}
