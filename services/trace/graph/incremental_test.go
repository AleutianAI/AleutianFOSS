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
	"fmt"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/lsp"
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
		_, err := IncrementalRefresh(nil, g, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil context")
		}
	})

	t.Run("nil base graph", func(t *testing.T) {
		_, err := IncrementalRefresh(ctx, nil, nil, nil, nil)
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
	incrResult, err := IncrementalRefresh(ctx, baseGraph, nil, nil, nil)
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

	incrResult, err := IncrementalRefresh(ctx, baseGraph, changedFiles, changedResults, nil)
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

	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults, nil)
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

	incrResult, err := IncrementalRefresh(ctx, writable, changedFiles, changedResults, nil)
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
	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, nil, nil, nil)
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

	_, err = IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults, nil)
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

// =============================================================================
// GR-76: LSP ENRICHMENT IN INCREMENTAL REFRESH
// =============================================================================

// incrementalMockQuerier is a mock LSPQuerier for incremental refresh tests.
type incrementalMockQuerier struct {
	definitions      map[string][]lsp.Location
	openedFiles      []string
	queriedPositions []string
}

func newIncrementalMockQuerier() *incrementalMockQuerier {
	return &incrementalMockQuerier{
		definitions: make(map[string][]lsp.Location),
	}
}

func (m *incrementalMockQuerier) OpenDocument(_ context.Context, filePath, _ string) error {
	m.openedFiles = append(m.openedFiles, filePath)
	return nil
}

func (m *incrementalMockQuerier) CloseDocument(_ context.Context, _ string) error {
	return nil
}

func (m *incrementalMockQuerier) Definition(_ context.Context, filePath string, line, col int) ([]lsp.Location, error) {
	key := fmt.Sprintf("%s:%d:%d", filePath, line, col)
	m.queriedPositions = append(m.queriedPositions, key)
	if locs, ok := m.definitions[key]; ok {
		return locs, nil
	}
	return nil, nil
}

func (m *incrementalMockQuerier) addDefinition(sourceFile string, sourceLine, sourceCol int, targetURI string, targetLine int) {
	key := fmt.Sprintf("%s:%d:%d", sourceFile, sourceLine, sourceCol)
	m.definitions[key] = []lsp.Location{
		{
			URI: targetURI,
			Range: lsp.Range{
				Start: lsp.Position{Line: targetLine, Character: 0},
				End:   lsp.Position{Line: targetLine, Character: 10},
			},
		},
	}
}

func TestIncrementalRefresh_WithLSPEnrichment(t *testing.T) {
	ctx := context.Background()
	projectRoot := "/test/project"

	// Build a base graph with a Python file that has an unresolved call
	callerSym := &ast.Symbol{
		ID:        ast.GenerateID("handlers/api.py", 10, "handle_request"),
		Name:      "handle_request",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "handlers/api.py",
		StartLine: 10,
		EndLine:   20,
		StartCol:  0,
		EndCol:    50,
		Language:  "python",
		Calls: []ast.CallSite{
			{
				Target:   "do_validation",
				Location: ast.Location{FilePath: projectRoot + "/handlers/api.py", StartLine: 15, EndLine: 15, StartCol: 4, EndCol: 30},
			},
		},
	}
	targetSym := &ast.Symbol{
		ID:        ast.GenerateID("utils/validator.py", 5, "validate_input"),
		Name:      "validate_input",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "utils/validator.py",
		StartLine: 5,
		EndLine:   15,
		StartCol:  0,
		EndCol:    50,
		Language:  "python",
	}

	builder := NewBuilder(WithProjectRoot(projectRoot))
	results := []*ast.ParseResult{
		{FilePath: "handlers/api.py", Language: "python", Symbols: []*ast.Symbol{callerSym}, Package: "handlers"},
		{FilePath: "utils/validator.py", Language: "python", Symbols: []*ast.Symbol{targetSym}, Package: "utils"},
	}
	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify placeholder exists in base graph
	placeholderNodes := buildResult.Graph.GetNodesByName("do_validation")
	if len(placeholderNodes) == 0 {
		t.Fatal("expected placeholder node for do_validation in base graph")
	}

	// Now do incremental refresh with LSP that resolves the placeholder
	mock := newIncrementalMockQuerier()
	mock.addDefinition(
		projectRoot+"/handlers/api.py", 15, 4,
		"file:///test/project/utils/validator.py", 4, // 0-indexed → 1-indexed line 5
	)

	lspConfig := &LSPEnrichmentConfig{
		Querier:            mock,
		MaxConcurrentFiles: 2,
		PerFileTimeout:     5 * time.Second,
		TotalTimeout:       10 * time.Second,
		Languages:          []string{"python"},
		FileReader: func(path string) ([]byte, error) {
			return []byte("# mock content\n"), nil
		},
	}

	// Modify handlers/api.py (same content — triggers re-parse + enrichment)
	changedFiles := []string{"handlers/api.py"}
	changedResults := []*ast.ParseResult{
		{FilePath: "handlers/api.py", Language: "python", Symbols: []*ast.Symbol{callerSym}, Package: "handlers"},
	}

	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults, lspConfig)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// Verify enrichment ran
	if incrResult.EnrichmentStats.PlaceholdersQueried == 0 {
		t.Error("expected placeholders to be queried during incremental enrichment")
	}
	if incrResult.EnrichmentStats.PlaceholdersResolved == 0 {
		t.Error("expected at least one placeholder resolved")
	}
}

func TestIncrementalRefresh_WithoutLSPEnrichment(t *testing.T) {
	ctx := context.Background()

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

	// nil config — should skip enrichment entirely
	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, nil, nil, nil)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// EnrichmentStats should be zero-valued
	if incrResult.EnrichmentStats.PlaceholdersQueried != 0 {
		t.Errorf("expected 0 placeholders queried with nil config, got %d",
			incrResult.EnrichmentStats.PlaceholdersQueried)
	}
}

func TestIncrementalRefresh_LSPScopedToChangedFiles(t *testing.T) {
	ctx := context.Background()
	projectRoot := "/test/project"

	// Create two Python files, each calling an unresolved function
	callerA := &ast.Symbol{
		ID:        ast.GenerateID("file_a.py", 1, "funcA"),
		Name:      "funcA",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "file_a.py",
		StartLine: 1,
		EndLine:   10,
		Language:  "python",
		Calls: []ast.CallSite{
			{Target: "unresolved_a", Location: ast.Location{FilePath: projectRoot + "/file_a.py", StartLine: 5, EndLine: 5, StartCol: 4, EndCol: 20}},
		},
	}
	callerB := &ast.Symbol{
		ID:        ast.GenerateID("file_b.py", 1, "funcB"),
		Name:      "funcB",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "file_b.py",
		StartLine: 1,
		EndLine:   10,
		Language:  "python",
		Calls: []ast.CallSite{
			{Target: "unresolved_b", Location: ast.Location{FilePath: projectRoot + "/file_b.py", StartLine: 5, EndLine: 5, StartCol: 4, EndCol: 20}},
		},
	}

	builder := NewBuilder(WithProjectRoot(projectRoot))
	results := []*ast.ParseResult{
		{FilePath: "file_a.py", Language: "python", Symbols: []*ast.Symbol{callerA}, Package: "pkg"},
		{FilePath: "file_b.py", Language: "python", Symbols: []*ast.Symbol{callerB}, Package: "pkg"},
	}
	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	mock := newIncrementalMockQuerier()
	lspConfig := &LSPEnrichmentConfig{
		Querier:            mock,
		MaxConcurrentFiles: 2,
		PerFileTimeout:     5 * time.Second,
		TotalTimeout:       10 * time.Second,
		Languages:          []string{"python"},
		FileReader: func(path string) ([]byte, error) {
			return []byte("# mock\n"), nil
		},
	}

	// Only change file_a.py — file_b.py should NOT be queried
	changedFiles := []string{"file_a.py"}
	changedResults := []*ast.ParseResult{
		{FilePath: "file_a.py", Language: "python", Symbols: []*ast.Symbol{callerA}, Package: "pkg"},
	}

	_, err = IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults, lspConfig)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// Verify only file_a.py was opened, not file_b.py
	for _, opened := range mock.openedFiles {
		if opened == projectRoot+"/file_b.py" {
			t.Error("file_b.py should NOT have been opened — it was not in changedFiles")
		}
	}

	// Verify no queries were made for file_b.py positions
	for _, pos := range mock.queriedPositions {
		if len(pos) > len(projectRoot+"/file_b.py") && pos[:len(projectRoot+"/file_b.py")] == projectRoot+"/file_b.py" {
			t.Errorf("unexpected query for file_b.py: %s", pos)
		}
	}
}

func TestIncrementalRefresh_SymbolsByLocationPopulated(t *testing.T) {
	ctx := context.Background()
	projectRoot := "/test/project"

	// Build a base graph with known symbol locations
	sym := &ast.Symbol{
		ID:        ast.GenerateID("module.py", 10, "my_func"),
		Name:      "my_func",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "module.py",
		StartLine: 10,
		EndLine:   20,
		Language:  "python",
	}

	builder := NewBuilder(WithProjectRoot(projectRoot))
	results := []*ast.ParseResult{
		{FilePath: "module.py", Language: "python", Symbols: []*ast.Symbol{sym}, Package: "mod"},
	}
	buildResult, err := builder.Build(ctx, results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Create a mock LSP that returns a definition at module.py:9 (0-indexed → line 10 1-indexed).
	// If symbolsByLocation is NOT populated, findNodeByLocation will return "" and
	// the enrichment will fail to resolve.
	mock := newIncrementalMockQuerier()

	// Create a changed file that calls an unresolved function
	callerSym := &ast.Symbol{
		ID:        ast.GenerateID("caller.py", 1, "call_func"),
		Name:      "call_func",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "caller.py",
		StartLine: 1,
		EndLine:   5,
		Language:  "python",
		Calls: []ast.CallSite{
			{Target: "mystery_func", Location: ast.Location{FilePath: projectRoot + "/caller.py", StartLine: 3, EndLine: 3, StartCol: 4, EndCol: 20}},
		},
	}

	// Mock: definition at caller.py:3:4 → module.py line 9 (0-indexed) = line 10 (1-indexed)
	mock.addDefinition(
		projectRoot+"/caller.py", 3, 4,
		"file:///test/project/module.py", 9,
	)

	lspConfig := &LSPEnrichmentConfig{
		Querier:            mock,
		MaxConcurrentFiles: 2,
		PerFileTimeout:     5 * time.Second,
		TotalTimeout:       10 * time.Second,
		Languages:          []string{"python"},
		FileReader: func(path string) ([]byte, error) {
			return []byte("# mock\n"), nil
		},
	}

	changedFiles := []string{"caller.py"}
	changedResults := []*ast.ParseResult{
		{FilePath: "caller.py", Language: "python", Symbols: []*ast.Symbol{callerSym}, Package: "pkg"},
	}

	incrResult, err := IncrementalRefresh(ctx, buildResult.Graph, changedFiles, changedResults, lspConfig)
	if err != nil {
		t.Fatalf("IncrementalRefresh: %v", err)
	}

	// The key assertion: if symbolsByLocation was properly populated from existing nodes,
	// the LSP definition pointing to module.py:10 should resolve to my_func's node ID.
	if incrResult.EnrichmentStats.PlaceholdersResolved == 0 {
		t.Error("expected placeholder resolved via symbolsByLocation lookup — " +
			"symbolsByLocation may not be populated for unchanged nodes")
	}
}

func TestEnrichmentStats_Merge(t *testing.T) {
	t.Run("additive counters", func(t *testing.T) {
		base := EnrichmentStats{
			PlaceholdersQueried:  10,
			PlaceholdersResolved: 7,
			PlaceholdersFailed:   3,
			PlaceholdersSkipped:  5,
			OrphanedRemoved:      2,
			LSPErrors:            1,
			FilesQueried:         8,
			DurationMicro:        5000,
		}
		delta := EnrichmentStats{
			PlaceholdersQueried:  3,
			PlaceholdersResolved: 2,
			PlaceholdersFailed:   1,
			PlaceholdersSkipped:  1,
			OrphanedRemoved:      1,
			LSPErrors:            0,
			FilesQueried:         2,
			DurationMicro:        1000,
		}

		base.Merge(delta)

		if base.PlaceholdersQueried != 13 {
			t.Errorf("PlaceholdersQueried: expected 13, got %d", base.PlaceholdersQueried)
		}
		if base.PlaceholdersResolved != 9 {
			t.Errorf("PlaceholdersResolved: expected 9, got %d", base.PlaceholdersResolved)
		}
		if base.PlaceholdersFailed != 4 {
			t.Errorf("PlaceholdersFailed: expected 4, got %d", base.PlaceholdersFailed)
		}
		if base.PlaceholdersSkipped != 6 {
			t.Errorf("PlaceholdersSkipped: expected 6, got %d", base.PlaceholdersSkipped)
		}
		if base.OrphanedRemoved != 3 {
			t.Errorf("OrphanedRemoved: expected 3, got %d", base.OrphanedRemoved)
		}
		if base.LSPErrors != 1 {
			t.Errorf("LSPErrors: expected 1, got %d", base.LSPErrors)
		}
	})

	t.Run("duration and files reflect latest run", func(t *testing.T) {
		base := EnrichmentStats{
			FilesQueried:  10,
			DurationMicro: 5000,
		}
		delta := EnrichmentStats{
			FilesQueried:  2,
			DurationMicro: 800,
		}

		base.Merge(delta)

		if base.FilesQueried != 2 {
			t.Errorf("FilesQueried should reflect latest run: expected 2, got %d", base.FilesQueried)
		}
		if base.DurationMicro != 800 {
			t.Errorf("DurationMicro should reflect latest run: expected 800, got %d", base.DurationMicro)
		}
	})

	t.Run("merge zero delta is no-op for counters", func(t *testing.T) {
		base := EnrichmentStats{
			PlaceholdersQueried:  10,
			PlaceholdersResolved: 7,
		}
		delta := EnrichmentStats{}

		base.Merge(delta)

		if base.PlaceholdersQueried != 10 {
			t.Errorf("expected 10, got %d", base.PlaceholdersQueried)
		}
		if base.PlaceholdersResolved != 7 {
			t.Errorf("expected 7, got %d", base.PlaceholdersResolved)
		}
	})
}
