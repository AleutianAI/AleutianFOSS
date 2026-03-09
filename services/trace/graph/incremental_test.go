// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestShouldDoIncrementalUpdate(t *testing.T) {
	t.Run("zero total returns false", func(t *testing.T) {
		if ShouldDoIncrementalUpdate(0, 0) {
			t.Error("expected false for zero total")
		}
	})

	t.Run("no changes returns true", func(t *testing.T) {
		if !ShouldDoIncrementalUpdate(0, 100) {
			t.Error("expected true for no changes")
		}
	})

	t.Run("under threshold returns true", func(t *testing.T) {
		// 30 out of 100 = 30% = at threshold
		if !ShouldDoIncrementalUpdate(30, 100) {
			t.Error("expected true for 30%")
		}
	})

	t.Run("over threshold returns false", func(t *testing.T) {
		// 31 out of 100 = 31% > threshold
		if ShouldDoIncrementalUpdate(31, 100) {
			t.Error("expected false for 31%")
		}
	})

	t.Run("all files changed returns false", func(t *testing.T) {
		if ShouldDoIncrementalUpdate(100, 100) {
			t.Error("expected false when all files changed")
		}
	})
}

func TestIncrementalRefresh_NilInputs(t *testing.T) {
	ctx := context.Background()

	t.Run("nil context", func(t *testing.T) {
		g := NewGraph("/test")
		g.Freeze()
		_, err := IncrementalRefresh(nil, g, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil context")
		}
	})

	t.Run("nil base graph", func(t *testing.T) {
		_, err := IncrementalRefresh(ctx, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil base graph")
		}
	})
}

func TestIncrementalRefresh_NoChanges(t *testing.T) {
	ctx := context.Background()

	// Build a base graph with 2 files
	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{
			testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1),
		}, nil),
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB", ast.SymbolKindFunction, "file_b.go", 1),
		}, nil),
	}

	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	baseGraph := buildResult.Graph
	if baseGraph.NodeCount() != 2 {
		t.Fatalf("expected 2 nodes, got %d", baseGraph.NodeCount())
	}

	// Incremental refresh with no changes
	incrResult, err := IncrementalRefresh(ctx, baseGraph, nil, nil)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	if incrResult.Strategy != "incremental" {
		t.Errorf("expected strategy=incremental, got %s", incrResult.Strategy)
	}
	if incrResult.NodesRemoved != 0 {
		t.Errorf("expected 0 nodes removed, got %d", incrResult.NodesRemoved)
	}
	if incrResult.NodesAdded != 0 {
		t.Errorf("expected 0 nodes added, got %d", incrResult.NodesAdded)
	}
	// Graph should be identical to base
	if incrResult.Graph.NodeCount() != baseGraph.NodeCount() {
		t.Errorf("expected %d nodes, got %d", baseGraph.NodeCount(), incrResult.Graph.NodeCount())
	}
}

func TestIncrementalRefresh_FileModified(t *testing.T) {
	ctx := context.Background()

	// Build base graph with 3 files, 3 functions
	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{
			testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1),
		}, nil),
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB", ast.SymbolKindFunction, "file_b.go", 1),
		}, nil),
		testParseResult("file_c.go", []*ast.Symbol{
			testSymbol("FuncC", ast.SymbolKindFunction, "file_c.go", 1),
		}, nil),
	}

	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	baseGraph := buildResult.Graph
	if baseGraph.NodeCount() != 3 {
		t.Fatalf("expected 3 nodes, got %d", baseGraph.NodeCount())
	}

	// Modify file_b: rename FuncB to FuncB2, add FuncB3
	changedFiles := []string{"file_b.go"}
	changedResults := []*ast.ParseResult{
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB2", ast.SymbolKindFunction, "file_b.go", 1),
			testSymbol("FuncB3", ast.SymbolKindFunction, "file_b.go", 15),
		}, nil),
	}

	incrResult, err := IncrementalRefresh(ctx, baseGraph, changedFiles, changedResults)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// Should have removed 1 (FuncB) and added 2 (FuncB2, FuncB3)
	if incrResult.NodesRemoved != 1 {
		t.Errorf("expected 1 node removed, got %d", incrResult.NodesRemoved)
	}
	if incrResult.NodesAdded != 2 {
		t.Errorf("expected 2 nodes added, got %d", incrResult.NodesAdded)
	}

	// Total nodes: 3 - 1 + 2 = 4 (FuncA, FuncB2, FuncB3, FuncC)
	if incrResult.Graph.NodeCount() != 4 {
		t.Errorf("expected 4 nodes, got %d", incrResult.Graph.NodeCount())
	}

	// Verify specific nodes exist
	funcA := incrResult.Graph.GetNodesByName("FuncA")
	if len(funcA) == 0 {
		t.Error("FuncA should still exist")
	}

	funcB := incrResult.Graph.GetNodesByName("FuncB")
	if len(funcB) != 0 {
		t.Error("FuncB should have been removed")
	}

	funcB2 := incrResult.Graph.GetNodesByName("FuncB2")
	if len(funcB2) == 0 {
		t.Error("FuncB2 should exist after refresh")
	}

	funcC := incrResult.Graph.GetNodesByName("FuncC")
	if len(funcC) == 0 {
		t.Error("FuncC should still exist")
	}
}

func TestIncrementalRefresh_FileDeleted(t *testing.T) {
	ctx := context.Background()

	// Build base graph with 2 files
	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{
			testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1),
		}, nil),
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB", ast.SymbolKindFunction, "file_b.go", 1),
		}, nil),
	}

	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Delete file_b: listed in changedFiles but no parse results
	changedFiles := []string{"file_b.go"}
	changedResults := []*ast.ParseResult{} // Empty: file no longer exists

	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	if incrResult.NodesRemoved != 1 {
		t.Errorf("expected 1 node removed, got %d", incrResult.NodesRemoved)
	}
	if incrResult.Graph.NodeCount() != 1 {
		t.Errorf("expected 1 node after deletion, got %d", incrResult.Graph.NodeCount())
	}

	funcA := incrResult.Graph.GetNodesByName("FuncA")
	if len(funcA) == 0 {
		t.Error("FuncA should still exist")
	}
}

func TestIncrementalRefresh_PreservesEdges(t *testing.T) {
	ctx := context.Background()

	// Build base graph with A calling B
	symA := testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1)
	symB := testSymbol("FuncB", ast.SymbolKindFunction, "file_b.go", 1)
	symC := testSymbol("FuncC", ast.SymbolKindFunction, "file_c.go", 1)

	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{symA}, nil),
		testParseResult("file_b.go", []*ast.Symbol{symB}, nil),
		testParseResult("file_c.go", []*ast.Symbol{symC}, nil),
	}

	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	g := buildResult.Graph

	// Manually add an edge A->B in a writable clone for testing
	writable := g.Clone()
	if err := writable.AddEdge(symA.ID, symB.ID, EdgeTypeCalls, ast.Location{
		FilePath:  "file_a.go",
		StartLine: 5,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	writable.Freeze()

	initialEdgeCount := writable.EdgeCount()

	// Modify file_c only — edge A->B should be preserved
	changedFiles := []string{"file_c.go"}
	changedResults := []*ast.ParseResult{
		testParseResult("file_c.go", []*ast.Symbol{
			testSymbol("FuncC2", ast.SymbolKindFunction, "file_c.go", 1),
		}, nil),
	}

	incrResult, err := IncrementalRefresh(ctx, writable, changedFiles, changedResults)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// FuncA and FuncB should still exist with their edge
	funcA := incrResult.Graph.GetNodesByName("FuncA")
	if len(funcA) == 0 {
		t.Error("FuncA should exist")
	}

	funcB := incrResult.Graph.GetNodesByName("FuncB")
	if len(funcB) == 0 {
		t.Error("FuncB should exist")
	}

	// The edge A->B should be preserved (we changed file_c, not file_a or file_b).
	// Edge count should be exactly the same since file_c had no edges.
	if incrResult.Graph.EdgeCount() != initialEdgeCount {
		t.Errorf("expected edge count preserved (had %d, got %d)",
			initialEdgeCount, incrResult.Graph.EdgeCount())
	}
}

func TestIncrementalRefresh_GraphIsFrozen(t *testing.T) {
	ctx := context.Background()

	// Build a base graph
	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{
			testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1),
		}, nil),
	}
	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// After incremental refresh, graph should be frozen (read-only)
	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, nil, nil)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	if !incrResult.Graph.IsFrozen() {
		t.Error("expected graph to be frozen after incremental refresh")
	}
}

func TestIncrementalRefresh_BaseGraphUnchanged(t *testing.T) {
	ctx := context.Background()

	// Build a base graph
	builder := NewBuilder(WithProjectRoot("/test"))
	results := []*ast.ParseResult{
		testParseResult("file_a.go", []*ast.Symbol{
			testSymbol("FuncA", ast.SymbolKindFunction, "file_a.go", 1),
		}, nil),
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB", ast.SymbolKindFunction, "file_b.go", 1),
		}, nil),
	}
	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	baseNodes := buildResult.Graph.NodeCount()
	baseEdges := buildResult.Graph.EdgeCount()

	// Run incremental refresh that modifies file_b
	changedFiles := []string{"file_b.go"}
	changedResults := []*ast.ParseResult{
		testParseResult("file_b.go", []*ast.Symbol{
			testSymbol("FuncB_New", ast.SymbolKindFunction, "file_b.go", 1),
		}, nil),
	}

	_, err = IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// Base graph should be unchanged (Clone creates independent copy)
	if buildResult.Graph.NodeCount() != baseNodes {
		t.Errorf("base graph modified: expected %d nodes, got %d", baseNodes, buildResult.Graph.NodeCount())
	}
	if buildResult.Graph.EdgeCount() != baseEdges {
		t.Errorf("base graph modified: expected %d edges, got %d", baseEdges, buildResult.Graph.EdgeCount())
	}

	// FuncB should still exist in the base graph
	funcB := buildResult.Graph.GetNodesByName("FuncB")
	if len(funcB) == 0 {
		t.Error("base graph should still have FuncB")
	}
}
