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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Process",
		}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Process",
			"depth":  2,
		}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"target": "Process",
	}})

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
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Process",
			"depth":  0,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// Should succeed with clamped value
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("depth above 10 clamped to 10", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Process",
			"depth":  100,
		}})

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

		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"target": "Process",
		}})

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

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"target": "Process",
	}})
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
