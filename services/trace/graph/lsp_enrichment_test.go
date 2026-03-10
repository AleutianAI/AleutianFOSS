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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/lsp"
)

// =============================================================================
// MOCK LSP QUERIER
// =============================================================================

// mockLSPQuerier implements LSPQuerier for testing.
type mockLSPQuerier struct {
	// definitions maps "file:line:col" to LSP locations
	definitions map[string][]lsp.Location

	// openErr if set, OpenDocument returns this error
	openErr error

	// defErr if set, Definition returns this error
	defErr error

	// openedFiles tracks which files were opened
	openedFiles []string

	// closedFiles tracks which files were closed
	closedFiles []string

	// queriedPositions tracks all definition queries
	queriedPositions []string
}

func newMockLSPQuerier() *mockLSPQuerier {
	return &mockLSPQuerier{
		definitions: make(map[string][]lsp.Location),
	}
}

func (m *mockLSPQuerier) OpenDocument(_ context.Context, filePath, _ string) error {
	m.openedFiles = append(m.openedFiles, filePath)
	return m.openErr
}

func (m *mockLSPQuerier) CloseDocument(_ context.Context, filePath string) error {
	m.closedFiles = append(m.closedFiles, filePath)
	return nil
}

func (m *mockLSPQuerier) Definition(_ context.Context, filePath string, line, col int) ([]lsp.Location, error) {
	key := fmt.Sprintf("%s:%d:%d", filePath, line, col)
	m.queriedPositions = append(m.queriedPositions, key)
	if m.defErr != nil {
		return nil, m.defErr
	}
	if locs, ok := m.definitions[key]; ok {
		return locs, nil
	}
	return nil, nil
}

// addDefinition registers a mock definition response.
func (m *mockLSPQuerier) addDefinition(sourceFile string, sourceLine, sourceCol int, targetURI string, targetLine int) {
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

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// buildWithLSPEnrichment creates a graph where the caller calls a function by a qualified
// name that the builder can't statically resolve (creating a placeholder), plus the actual
// target symbol. The LSP mock can then resolve the placeholder to the real target.
func buildWithLSPEnrichment(t *testing.T, querier LSPQuerier, projectRoot string, setupFn func(*mockLSPQuerier)) *BuildResult {
	t.Helper()

	mock := querier.(*mockLSPQuerier)
	if setupFn != nil {
		setupFn(mock)
	}

	// Create a Python caller that calls "external_api_call" — a name with
	// NO matching symbol in the project. This forces a placeholder to be created.
	// The LSP enrichment phase can then resolve this placeholder via definition lookup.
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

	// The real target function — name is "validate_input" NOT "do_validation".
	// The builder's static resolution won't find this. But the LSP definition
	// lookup should resolve to this node's location.
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

	results := []*ast.ParseResult{
		{
			FilePath: "handlers/api.py",
			Language: "python",
			Symbols:  []*ast.Symbol{callerSym},
			Package:  "handlers",
		},
		{
			FilePath: "utils/validator.py",
			Language: "python",
			Symbols:  []*ast.Symbol{targetSym},
			Package:  "utils",
		},
	}

	builder := NewBuilder(
		WithProjectRoot(projectRoot),
		WithLSPEnrichment(&LSPEnrichmentConfig{
			Querier:            querier,
			MaxConcurrentFiles: 2,
			PerFileTimeout:     5 * time.Second,
			TotalTimeout:       10 * time.Second,
			Languages:          []string{"python", "typescript"},
			FileReader: func(path string) ([]byte, error) {
				// Mock file reader — return dummy content for any file
				return []byte("# mock file content\n"), nil
			},
		}),
	)

	result, err := builder.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return result
}

// =============================================================================
// TESTS
// =============================================================================

func TestBuilder_LSPEnrichment_ResolvesPlaceholder(t *testing.T) {
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	// Mock: definition query at api.py:15:4 returns validator.py:4 (0-indexed → line 5 1-indexed)
	mock.addDefinition(
		projectRoot+"/handlers/api.py", 15, 4,
		"file:///test/project/utils/validator.py", 4, // 0-indexed line 4 = 1-indexed line 5
	)

	result := buildWithLSPEnrichment(t, mock, projectRoot, nil)

	stats := result.Stats.LSPEnrichment
	if stats.PlaceholdersQueried == 0 {
		t.Fatal("expected at least one placeholder queried")
	}
	if stats.PlaceholdersResolved == 0 {
		t.Error("expected at least one placeholder resolved")
	}

	// Verify the edge now points to the real target, not a placeholder
	callerID := ast.GenerateID("handlers/api.py", 10, "handle_request")
	targetID := ast.GenerateID("utils/validator.py", 5, "validate_input")

	callerNode, ok := result.Graph.GetNode(callerID)
	if !ok {
		t.Fatal("caller node not found")
	}

	found := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == targetID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected call edge from caller to %s", targetID)
		for _, edge := range callerNode.Outgoing {
			t.Logf("  edge: type=%s to=%s", edge.Type, edge.ToID)
		}
	}
}

func TestBuilder_LSPEnrichment_ExternalDefinition(t *testing.T) {
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	// Mock: definition returns outside project (stdlib)
	mock.addDefinition(
		projectRoot+"/handlers/api.py", 15, 4,
		"file:///usr/lib/python3/json/__init__.py", 100,
	)

	result := buildWithLSPEnrichment(t, mock, projectRoot, nil)

	stats := result.Stats.LSPEnrichment
	// Placeholder should be queried but NOT resolved (external definition)
	if stats.PlaceholdersQueried == 0 {
		t.Error("expected at least one placeholder queried")
	}
	if stats.PlaceholdersResolved != 0 {
		t.Errorf("expected 0 resolved (external definition), got %d", stats.PlaceholdersResolved)
	}
}

func TestBuilder_LSPEnrichment_ServerUnavailable(t *testing.T) {
	// Build without LSP enrichment config — should succeed with zero enrichment
	callerSym := &ast.Symbol{
		ID:        ast.GenerateID("main.py", 1, "main"),
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "main.py",
		StartLine: 1,
		EndLine:   10,
		StartCol:  0,
		EndCol:    50,
		Language:  "python",
	}

	results := []*ast.ParseResult{
		{
			FilePath: "main.py",
			Language: "python",
			Symbols:  []*ast.Symbol{callerSym},
			Package:  "main",
		},
	}

	builder := NewBuilder(WithProjectRoot("/test/project"))
	result, err := builder.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	stats := result.Stats.LSPEnrichment
	if stats.PlaceholdersQueried != 0 || stats.PlaceholdersResolved != 0 {
		t.Errorf("expected zero enrichment stats, got queried=%d resolved=%d",
			stats.PlaceholdersQueried, stats.PlaceholdersResolved)
	}
}

func TestBuilder_LSPEnrichment_Timeout(t *testing.T) {
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	// Caller with an unresolvable qualified call → placeholder created
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
				Target:   "ext_lib.process",
				Location: ast.Location{FilePath: projectRoot + "/handlers/api.py", StartLine: 15, EndLine: 15, StartCol: 4, EndCol: 20},
			},
		},
	}

	results := []*ast.ParseResult{
		{
			FilePath: "handlers/api.py",
			Language: "python",
			Symbols:  []*ast.Symbol{callerSym},
			Package:  "handlers",
		},
	}

	builder := NewBuilder(
		WithProjectRoot(projectRoot),
		WithLSPEnrichment(&LSPEnrichmentConfig{
			Querier:            mock,
			MaxConcurrentFiles: 1,
			PerFileTimeout:     1 * time.Nanosecond,
			TotalTimeout:       1 * time.Nanosecond,
			Languages:          []string{"python"},
			FileReader: func(path string) ([]byte, error) {
				return []byte("# mock\n"), nil
			},
		}),
	)

	// Build should still succeed (enrichment failure is non-fatal)
	result, err := builder.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Graph should still be valid
	if result.Graph == nil {
		t.Fatal("expected non-nil graph")
	}
}

func TestBuilder_LSPEnrichment_OrphanCleanup(t *testing.T) {
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	// Mock: definition resolves to target within project
	mock.addDefinition(
		projectRoot+"/handlers/api.py", 15, 4,
		"file:///test/project/utils/validator.py", 4,
	)

	result := buildWithLSPEnrichment(t, mock, projectRoot, nil)

	stats := result.Stats.LSPEnrichment
	// When the single incoming edge of a placeholder is retargeted, the placeholder
	// has zero edges and should be removed as an orphan.
	if stats.PlaceholdersResolved > 0 {
		if stats.OrphanedRemoved == 0 {
			t.Error("expected orphaned placeholder to be removed after all edges resolved")
		}
	}

	// Verify no placeholder nodes remain with zero edges
	for id, node := range result.Graph.nodes {
		if len(node.Incoming) == 0 && len(node.Outgoing) == 0 {
			if node.Symbol != nil && node.Symbol.Kind == ast.SymbolKindExternal {
				t.Errorf("orphaned placeholder node %s still in graph", id)
			}
		}
	}
}

func TestBuilder_LSPEnrichment_GoExcluded(t *testing.T) {
	// Go files should not be enriched — Go has excellent static resolution.
	// Even though "fmt.Println" creates a placeholder (unresolved), the edge
	// location is a .go file so LSP enrichment skips it.
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	callerSym := &ast.Symbol{
		ID:        ast.GenerateID("main.go", 10, "main"),
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "main.go",
		StartLine: 10,
		EndLine:   20,
		StartCol:  0,
		EndCol:    50,
		Language:  "go",
		Calls: []ast.CallSite{
			{
				Target:   "fmt.Println",
				Location: ast.Location{FilePath: projectRoot + "/main.go", StartLine: 15, EndLine: 15, StartCol: 4, EndCol: 20},
			},
		},
	}

	results := []*ast.ParseResult{
		{
			FilePath: "main.go",
			Language: "go",
			Symbols:  []*ast.Symbol{callerSym},
			Package:  "main",
		},
	}

	builder := NewBuilder(
		WithProjectRoot(projectRoot),
		WithLSPEnrichment(&LSPEnrichmentConfig{
			Querier:            mock,
			MaxConcurrentFiles: 2,
			PerFileTimeout:     5 * time.Second,
			TotalTimeout:       10 * time.Second,
			Languages:          []string{"python", "typescript"},
		}),
	)

	result, err := builder.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	stats := result.Stats.LSPEnrichment
	if stats.PlaceholdersQueried != 0 {
		t.Errorf("expected 0 Go placeholders queried, got %d", stats.PlaceholdersQueried)
	}

	// Verify the mock was never called for definition
	if len(mock.queriedPositions) != 0 {
		t.Errorf("expected no LSP queries for Go files, got %d", len(mock.queriedPositions))
	}
}

func TestBuilder_LSPEnrichment_Stats(t *testing.T) {
	projectRoot := "/test/project"
	mock := newMockLSPQuerier()

	// Mock: definition resolves to target within project
	mock.addDefinition(
		projectRoot+"/handlers/api.py", 15, 4,
		"file:///test/project/utils/validator.py", 4,
	)

	result := buildWithLSPEnrichment(t, mock, projectRoot, nil)

	stats := result.Stats.LSPEnrichment
	// Verify basic stats are populated
	if stats.DurationMicro <= 0 {
		t.Error("expected positive duration")
	}

	t.Logf("LSP Enrichment Stats: queried=%d resolved=%d failed=%d skipped=%d files=%d orphans=%d errors=%d duration=%dμs",
		stats.PlaceholdersQueried,
		stats.PlaceholdersResolved,
		stats.PlaceholdersFailed,
		stats.PlaceholdersSkipped,
		stats.FilesQueried,
		stats.OrphanedRemoved,
		stats.LSPErrors,
		stats.DurationMicro,
	)

	// Total should add up
	total := stats.PlaceholdersResolved + stats.PlaceholdersFailed
	if stats.PlaceholdersQueried != total {
		t.Errorf("queried (%d) != resolved (%d) + failed (%d)",
			stats.PlaceholdersQueried, stats.PlaceholdersResolved, stats.PlaceholdersFailed)
	}

	// Should have queried at least 1 file
	if stats.FilesQueried == 0 {
		t.Error("expected at least one file queried")
	}
}

// =============================================================================
// GRAPH METHOD UNIT TESTS
// =============================================================================

func TestGraph_ReplaceEdgeTarget(t *testing.T) {
	t.Run("replaces edge target successfully", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		symB := makeSymbol("b", "B", ast.SymbolKindFunction, "b.go")
		symC := makeSymbol("c", "C", ast.SymbolKindFunction, "c.go")

		g.AddNode(symA)
		g.AddNode(symB)
		g.AddNode(symC)
		g.AddEdge("a", "b", EdgeTypeCalls, makeLocation("a.go", 5))

		nodeB, _ := g.GetNode("b")
		nodeC, _ := g.GetNode("c")

		if len(nodeB.Incoming) != 1 {
			t.Fatalf("expected 1 incoming edge on B, got %d", len(nodeB.Incoming))
		}

		edge := nodeB.Incoming[0]
		err := g.ReplaceEdgeTarget(edge, "c")
		if err != nil {
			t.Fatalf("ReplaceEdgeTarget failed: %v", err)
		}

		// Edge should now point to C
		if edge.ToID != "c" {
			t.Errorf("expected edge.ToID = 'c', got %q", edge.ToID)
		}

		// B should have no incoming edges
		if len(nodeB.Incoming) != 0 {
			t.Errorf("expected 0 incoming edges on B, got %d", len(nodeB.Incoming))
		}

		// C should have 1 incoming edge
		if len(nodeC.Incoming) != 1 {
			t.Errorf("expected 1 incoming edge on C, got %d", len(nodeC.Incoming))
		}
	})

	t.Run("returns error when frozen", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		symB := makeSymbol("b", "B", ast.SymbolKindFunction, "b.go")
		g.AddNode(symA)
		g.AddNode(symB)
		g.AddEdge("a", "b", EdgeTypeCalls, makeLocation("a.go", 5))

		g.Freeze()

		nodeB, _ := g.GetNode("b")
		edge := nodeB.Incoming[0]
		err := g.ReplaceEdgeTarget(edge, "a")
		if !errors.Is(err, ErrGraphFrozen) {
			t.Errorf("expected ErrGraphFrozen, got %v", err)
		}
	})

	t.Run("returns error for non-existent target", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		symB := makeSymbol("b", "B", ast.SymbolKindFunction, "b.go")
		g.AddNode(symA)
		g.AddNode(symB)
		g.AddEdge("a", "b", EdgeTypeCalls, makeLocation("a.go", 5))

		nodeB, _ := g.GetNode("b")
		edge := nodeB.Incoming[0]
		err := g.ReplaceEdgeTarget(edge, "nonexistent")
		if !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("expected ErrNodeNotFound, got %v", err)
		}
	})

	t.Run("returns error for nil edge", func(t *testing.T) {
		g := NewGraph("/test")
		err := g.ReplaceEdgeTarget(nil, "a")
		if err == nil {
			t.Error("expected error for nil edge")
		}
	})
}

func TestGraph_RemoveNode(t *testing.T) {
	t.Run("removes node and all edges", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		symB := makeSymbol("b", "B", ast.SymbolKindFunction, "b.go")
		symC := makeSymbol("c", "C", ast.SymbolKindFunction, "c.go")

		g.AddNode(symA)
		g.AddNode(symB)
		g.AddNode(symC)
		g.AddEdge("a", "b", EdgeTypeCalls, makeLocation("a.go", 5))
		g.AddEdge("b", "c", EdgeTypeCalls, makeLocation("b.go", 10))

		err := g.RemoveNode("b")
		if err != nil {
			t.Fatalf("RemoveNode failed: %v", err)
		}

		// B should be gone
		if _, ok := g.GetNode("b"); ok {
			t.Error("node B should have been removed")
		}

		// Edges involving B should be removed
		if g.EdgeCount() != 0 {
			t.Errorf("expected 0 edges, got %d", g.EdgeCount())
		}

		// A's outgoing should be empty
		nodeA, _ := g.GetNode("a")
		if len(nodeA.Outgoing) != 0 {
			t.Errorf("expected 0 outgoing edges on A, got %d", len(nodeA.Outgoing))
		}

		// C's incoming should be empty
		nodeC, _ := g.GetNode("c")
		if len(nodeC.Incoming) != 0 {
			t.Errorf("expected 0 incoming edges on C, got %d", len(nodeC.Incoming))
		}

		// NodeCount should be 2
		if g.NodeCount() != 2 {
			t.Errorf("expected 2 nodes, got %d", g.NodeCount())
		}
	})

	t.Run("removes from secondary indexes", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		g.AddNode(symA)

		// Verify it's in indexes
		nodesByName := g.GetNodesByName("A")
		if len(nodesByName) != 1 {
			t.Fatalf("expected 1 node by name 'A', got %d", len(nodesByName))
		}

		err := g.RemoveNode("a")
		if err != nil {
			t.Fatalf("RemoveNode failed: %v", err)
		}

		// Should be gone from name index
		nodesByName = g.GetNodesByName("A")
		if len(nodesByName) != 0 {
			t.Errorf("expected 0 nodes by name 'A' after removal, got %d", len(nodesByName))
		}
	})

	t.Run("returns error when frozen", func(t *testing.T) {
		g := NewGraph("/test")
		symA := makeSymbol("a", "A", ast.SymbolKindFunction, "a.go")
		g.AddNode(symA)
		g.Freeze()

		err := g.RemoveNode("a")
		if !errors.Is(err, ErrGraphFrozen) {
			t.Errorf("expected ErrGraphFrozen, got %v", err)
		}
	})

	t.Run("returns error for non-existent node", func(t *testing.T) {
		g := NewGraph("/test")
		err := g.RemoveNode("nonexistent")
		if !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("expected ErrNodeNotFound, got %v", err)
		}
	})
}

// =============================================================================
// BENCHMARK
// =============================================================================

func BenchmarkBuilder_LSPEnrichment(b *testing.B) {
	projectRoot := "/bench/project"
	placeholderCount := 100

	// Create symbols and parse results
	var symbols []*ast.Symbol
	for i := 0; i < placeholderCount; i++ {
		sym := &ast.Symbol{
			ID:        ast.GenerateID(fmt.Sprintf("file_%d.py", i), 10, fmt.Sprintf("func_%d", i)),
			Name:      fmt.Sprintf("func_%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("file_%d.py", i),
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "python",
			Calls: []ast.CallSite{
				{
					Target: fmt.Sprintf("lib.target_%d", i),
					Location: ast.Location{
						FilePath:  projectRoot + fmt.Sprintf("/file_%d.py", i),
						StartLine: 15,
						EndLine:   15,
						StartCol:  4,
						EndCol:    20,
					},
				},
			},
		}
		symbols = append(symbols, sym)
	}

	// Create target symbols
	var targets []*ast.Symbol
	for i := 0; i < placeholderCount; i++ {
		target := &ast.Symbol{
			ID:        ast.GenerateID(fmt.Sprintf("targets/target_%d.py", i), 5, fmt.Sprintf("target_%d", i)),
			Name:      fmt.Sprintf("target_%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("targets/target_%d.py", i),
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "python",
		}
		targets = append(targets, target)
	}

	// Create mock querier
	mock := newMockLSPQuerier()
	for i := 0; i < placeholderCount; i++ {
		mock.addDefinition(
			projectRoot+fmt.Sprintf("/file_%d.py", i), 15, 4,
			fmt.Sprintf("file:///bench/project/targets/target_%d.py", i), 4,
		)
	}

	// Build parse results
	var parseResults []*ast.ParseResult
	for i := 0; i < placeholderCount; i++ {
		parseResults = append(parseResults, &ast.ParseResult{
			FilePath: fmt.Sprintf("file_%d.py", i),
			Language: "python",
			Symbols:  []*ast.Symbol{symbols[i]},
			Package:  "bench",
		})
	}
	for i := 0; i < placeholderCount; i++ {
		parseResults = append(parseResults, &ast.ParseResult{
			FilePath: fmt.Sprintf("targets/target_%d.py", i),
			Language: "python",
			Symbols:  []*ast.Symbol{targets[i]},
			Package:  "targets",
		})
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		builder := NewBuilder(
			WithProjectRoot(projectRoot),
			WithLSPEnrichment(&LSPEnrichmentConfig{
				Querier:            mock,
				MaxConcurrentFiles: 4,
				PerFileTimeout:     5 * time.Second,
				TotalTimeout:       30 * time.Second,
				Languages:          []string{"python"},
				FileReader: func(path string) ([]byte, error) {
					return []byte("# mock\n"), nil
				},
			}),
		)
		_, err := builder.Build(context.Background(), parseResults)
		if err != nil {
			b.Fatalf("Build failed: %v", err)
		}
	}
}
