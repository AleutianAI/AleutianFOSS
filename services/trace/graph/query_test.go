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
)

// createTestGraph creates a graph for testing with the following structure:
//
//	main --CALLS--> setup --CALLS--> handleAgent
//	main --CALLS--> run --CALLS--> helper
//	run --CALLS--> helper (second edge)
//	FileReader --IMPLEMENTS--> Reader
//	Server --EMBEDS--> Config
func createTestGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph("/test/project")

	// Create symbols
	mainSym := &ast.Symbol{
		ID:        "main.go:10:main",
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		Package:   "main",
		FilePath:  "main.go",
		StartLine: 10,
		EndLine:   10,
		Language:  "go",
	}
	setupSym := &ast.Symbol{
		ID:        "main.go:20:setup",
		Name:      "setup",
		Kind:      ast.SymbolKindFunction,
		Package:   "main",
		FilePath:  "main.go",
		StartLine: 20,
		EndLine:   20,
		Language:  "go",
	}
	handleAgentSym := &ast.Symbol{
		ID:        "handlers.go:30:handleAgent",
		Name:      "handleAgent",
		Kind:      ast.SymbolKindFunction,
		Package:   "handlers",
		FilePath:  "handlers.go",
		StartLine: 30,
		EndLine:   30,
		Language:  "go",
	}
	runSym := &ast.Symbol{
		ID:        "main.go:40:run",
		Name:      "run",
		Kind:      ast.SymbolKindFunction,
		Package:   "main",
		FilePath:  "main.go",
		StartLine: 40,
		EndLine:   40,
		Language:  "go",
	}
	helperSym := &ast.Symbol{
		ID:        "utils.go:50:helper",
		Name:      "helper",
		Kind:      ast.SymbolKindFunction,
		Package:   "utils",
		FilePath:  "utils.go",
		StartLine: 50,
		EndLine:   50,
		Language:  "go",
	}
	readerSym := &ast.Symbol{
		ID:        "types.go:60:Reader",
		Name:      "Reader",
		Kind:      ast.SymbolKindInterface,
		Package:   "main",
		FilePath:  "types.go",
		StartLine: 60,
		EndLine:   60,
		Language:  "go",
	}
	fileReaderSym := &ast.Symbol{
		ID:        "types.go:70:FileReader",
		Name:      "FileReader",
		Kind:      ast.SymbolKindStruct,
		Package:   "main",
		FilePath:  "types.go",
		StartLine: 70,
		EndLine:   70,
		Language:  "go",
	}
	configSym := &ast.Symbol{
		ID:        "config.go:80:Config",
		Name:      "Config",
		Kind:      ast.SymbolKindStruct,
		Package:   "main",
		FilePath:  "config.go",
		StartLine: 80,
		EndLine:   80,
		Language:  "go",
	}
	serverSym := &ast.Symbol{
		ID:        "server.go:90:Server",
		Name:      "Server",
		Kind:      ast.SymbolKindStruct,
		Package:   "main",
		FilePath:  "server.go",
		StartLine: 90,
		EndLine:   90,
		Language:  "go",
	}

	// Add nodes
	symbols := []*ast.Symbol{mainSym, setupSym, handleAgentSym, runSym, helperSym, readerSym, fileReaderSym, configSym, serverSym}
	for _, sym := range symbols {
		if _, err := g.AddNode(sym); err != nil {
			t.Fatalf("AddNode failed: %v", err)
		}
	}

	// Add edges
	edges := []struct {
		from, to string
		eType    EdgeType
	}{
		{mainSym.ID, setupSym.ID, EdgeTypeCalls},
		{mainSym.ID, runSym.ID, EdgeTypeCalls},
		{setupSym.ID, handleAgentSym.ID, EdgeTypeCalls},
		{runSym.ID, helperSym.ID, EdgeTypeCalls},
		{fileReaderSym.ID, readerSym.ID, EdgeTypeImplements},
		{serverSym.ID, configSym.ID, EdgeTypeEmbeds},
	}
	for _, e := range edges {
		if err := g.AddEdge(e.from, e.to, e.eType, ast.Location{}); err != nil {
			t.Fatalf("AddEdge failed: %v", err)
		}
	}

	g.Freeze()
	return g
}

func TestGraph_Validate(t *testing.T) {
	t.Run("valid graph returns nil", func(t *testing.T) {
		g := createTestGraph(t)
		if err := g.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("corrupt graph with dangling edge returns error", func(t *testing.T) {
		g := NewGraph("/test")
		sym := &ast.Symbol{ID: "test:1:func", Name: "func", Kind: ast.SymbolKindFunction}
		g.AddNode(sym)

		// Manually inject a dangling edge
		g.edges = append(g.edges, &Edge{
			FromID: "test:1:func",
			ToID:   "nonexistent:node",
			Type:   EdgeTypeCalls,
		})

		err := g.Validate()
		if err == nil {
			t.Error("Validate() = nil, want error for dangling edge")
		}
	})
}

func TestGraph_FindCallersByID(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("function with callers returns all callers", func(t *testing.T) {
		result, err := g.FindCallersByID(ctx, "main.go:20:setup")
		if err != nil {
			t.Fatalf("FindCallersByID() error = %v", err)
		}
		if len(result.Symbols) != 1 {
			t.Errorf("got %d callers, want 1", len(result.Symbols))
		}
		if result.Symbols[0].Name != "main" {
			t.Errorf("caller name = %s, want main", result.Symbols[0].Name)
		}
	})

	t.Run("function with no callers returns empty slice", func(t *testing.T) {
		result, err := g.FindCallersByID(ctx, "main.go:10:main")
		if err != nil {
			t.Fatalf("FindCallersByID() error = %v", err)
		}
		if result.Symbols == nil {
			t.Error("Symbols is nil, want empty slice")
		}
		if len(result.Symbols) != 0 {
			t.Errorf("got %d callers, want 0", len(result.Symbols))
		}
	})

	t.Run("non-existent ID returns empty result not error", func(t *testing.T) {
		result, err := g.FindCallersByID(ctx, "nonexistent:id")
		if err != nil {
			t.Fatalf("FindCallersByID() error = %v, want nil", err)
		}
		if len(result.Symbols) != 0 {
			t.Errorf("got %d callers, want 0", len(result.Symbols))
		}
	})
}

func TestGraph_FindCalleesByID(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("function with callees returns all callees", func(t *testing.T) {
		result, err := g.FindCalleesByID(ctx, "main.go:10:main")
		if err != nil {
			t.Fatalf("FindCalleesByID() error = %v", err)
		}
		if len(result.Symbols) != 2 {
			t.Errorf("got %d callees, want 2", len(result.Symbols))
		}
	})

	t.Run("leaf function returns empty slice", func(t *testing.T) {
		result, err := g.FindCalleesByID(ctx, "utils.go:50:helper")
		if err != nil {
			t.Fatalf("FindCalleesByID() error = %v", err)
		}
		if result.Symbols == nil {
			t.Error("Symbols is nil, want empty slice")
		}
		if len(result.Symbols) != 0 {
			t.Errorf("got %d callees, want 0", len(result.Symbols))
		}
	})

	t.Run("duplicate edges to same callee are deduplicated", func(t *testing.T) {
		g2 := NewGraph("/test")
		caller := &ast.Symbol{
			ID: "a.go:1:caller", Name: "caller", Kind: ast.SymbolKindFunction,
		}
		callee := &ast.Symbol{
			ID: "b.go:1:callee", Name: "callee", Kind: ast.SymbolKindFunction,
		}
		g2.AddNode(caller)
		g2.AddNode(callee)
		// Add multiple edges to the same callee (e.g., multiple call sites)
		for i := 0; i < 5; i++ {
			g2.AddEdge(caller.ID, callee.ID, EdgeTypeCalls, ast.Location{StartLine: i + 1})
		}
		g2.Freeze()

		result, err := g2.FindCalleesByID(ctx, caller.ID)
		if err != nil {
			t.Fatalf("FindCalleesByID() error = %v", err)
		}
		if len(result.Symbols) != 1 {
			t.Errorf("got %d callees, want 1 (should deduplicate)", len(result.Symbols))
		}
		if result.Truncated {
			t.Error("Truncated = true, want false (duplicates should not fill limit)")
		}
	})

	t.Run("duplicate edges do not consume limit budget", func(t *testing.T) {
		g2 := NewGraph("/test")
		caller := &ast.Symbol{
			ID: "a.go:1:caller", Name: "caller", Kind: ast.SymbolKindFunction,
		}
		calleeA := &ast.Symbol{
			ID: "b.go:1:calleeA", Name: "calleeA", Kind: ast.SymbolKindFunction,
		}
		calleeB := &ast.Symbol{
			ID: "c.go:1:calleeB", Name: "calleeB", Kind: ast.SymbolKindFunction,
		}
		g2.AddNode(caller)
		g2.AddNode(calleeA)
		g2.AddNode(calleeB)
		// 50 edges to calleeA, then 1 edge to calleeB
		for i := 0; i < 50; i++ {
			g2.AddEdge(caller.ID, calleeA.ID, EdgeTypeCalls, ast.Location{StartLine: i + 1})
		}
		g2.AddEdge(caller.ID, calleeB.ID, EdgeTypeCalls, ast.Location{StartLine: 100})
		g2.Freeze()

		result, err := g2.FindCalleesByID(ctx, caller.ID, WithLimit(10))
		if err != nil {
			t.Fatalf("FindCalleesByID() error = %v", err)
		}
		if len(result.Symbols) != 2 {
			t.Errorf("got %d callees, want 2 (duplicates should not consume limit)", len(result.Symbols))
		}
		if result.Truncated {
			t.Error("Truncated = true, want false")
		}
	})
}

func TestGraph_FindImplementationsByID(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("interface with implementers returns all", func(t *testing.T) {
		result, err := g.FindImplementationsByID(ctx, "types.go:60:Reader")
		if err != nil {
			t.Fatalf("FindImplementationsByID() error = %v", err)
		}
		if len(result.Symbols) != 1 {
			t.Errorf("got %d implementers, want 1", len(result.Symbols))
		}
		if result.Symbols[0].Name != "FileReader" {
			t.Errorf("implementer = %s, want FileReader", result.Symbols[0].Name)
		}
	})

	t.Run("interface with no implementers returns empty slice", func(t *testing.T) {
		// Add an interface with no implementers
		g2 := NewGraph("/test")
		lonely := &ast.Symbol{ID: "lonely:1:Lonely", Name: "Lonely", Kind: ast.SymbolKindInterface}
		g2.AddNode(lonely)
		g2.Freeze()

		result, err := g2.FindImplementationsByID(ctx, "lonely:1:Lonely")
		if err != nil {
			t.Fatalf("FindImplementationsByID() error = %v", err)
		}
		if result.Symbols == nil {
			t.Error("Symbols is nil, want empty slice")
		}
		if len(result.Symbols) != 0 {
			t.Errorf("got %d implementers, want 0", len(result.Symbols))
		}
	})
}

func TestGraph_FindCallersByName(t *testing.T) {
	ctx := context.Background()

	t.Run("ambiguous name returns map with all matches", func(t *testing.T) {
		g := NewGraph("/test")

		// Create two functions named "setup" in different packages
		setup1 := &ast.Symbol{ID: "pkg1.go:1:setup", Name: "setup", Kind: ast.SymbolKindFunction, Package: "pkg1"}
		setup2 := &ast.Symbol{ID: "pkg2.go:1:setup", Name: "setup", Kind: ast.SymbolKindFunction, Package: "pkg2"}
		caller1 := &ast.Symbol{ID: "main1.go:1:main1", Name: "main1", Kind: ast.SymbolKindFunction}
		caller2 := &ast.Symbol{ID: "main2.go:1:main2", Name: "main2", Kind: ast.SymbolKindFunction}

		g.AddNode(setup1)
		g.AddNode(setup2)
		g.AddNode(caller1)
		g.AddNode(caller2)
		g.AddEdge(caller1.ID, setup1.ID, EdgeTypeCalls, ast.Location{})
		g.AddEdge(caller2.ID, setup2.ID, EdgeTypeCalls, ast.Location{})
		g.Freeze()

		results, err := g.FindCallersByName(ctx, "setup")
		if err != nil {
			t.Fatalf("FindCallersByName() error = %v", err)
		}
		if len(results) != 2 {
			t.Errorf("got %d result entries, want 2", len(results))
		}

		// Check each setup has its caller
		if r, ok := results["pkg1.go:1:setup"]; !ok || len(r.Symbols) != 1 {
			t.Error("expected callers for setup in pkg1")
		}
		if r, ok := results["pkg2.go:1:setup"]; !ok || len(r.Symbols) != 1 {
			t.Error("expected callers for setup in pkg2")
		}
	})
}

func TestGraph_QueryLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("limit is respected and truncated flag set", func(t *testing.T) {
		g := NewGraph("/test")

		// Create a function with many callers
		target := &ast.Symbol{ID: "target:1:target", Name: "target", Kind: ast.SymbolKindFunction}
		g.AddNode(target)

		for i := 0; i < 10; i++ {
			caller := &ast.Symbol{
				ID:   fmt.Sprintf("caller%d:1:caller%d", i, i),
				Name: fmt.Sprintf("caller%d", i),
				Kind: ast.SymbolKindFunction,
			}
			g.AddNode(caller)
			g.AddEdge(caller.ID, target.ID, EdgeTypeCalls, ast.Location{})
		}
		g.Freeze()

		result, err := g.FindCallersByID(ctx, "target:1:target", WithLimit(5))
		if err != nil {
			t.Fatalf("FindCallersByID() error = %v", err)
		}
		if len(result.Symbols) != 5 {
			t.Errorf("got %d callers, want 5", len(result.Symbols))
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true")
		}
	})

	t.Run("negative limit uses default", func(t *testing.T) {
		opts := applyOptions([]QueryOption{WithLimit(-1)})
		if opts.Limit != DefaultQueryLimit {
			t.Errorf("Limit = %d, want %d", opts.Limit, DefaultQueryLimit)
		}
	})

	t.Run("limit over max is clamped", func(t *testing.T) {
		opts := applyOptions([]QueryOption{WithLimit(99999)})
		if opts.Limit != MaxQueryLimit {
			t.Errorf("Limit = %d, want %d", opts.Limit, MaxQueryLimit)
		}
	})
}

func TestGraph_DepthLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("maxDepth negative uses default", func(t *testing.T) {
		opts := applyOptions([]QueryOption{WithMaxDepth(-1)})
		if opts.MaxDepth != DefaultMaxDepth {
			t.Errorf("MaxDepth = %d, want %d", opts.MaxDepth, DefaultMaxDepth)
		}
	})

	t.Run("maxDepth over 100 is clamped", func(t *testing.T) {
		opts := applyOptions([]QueryOption{WithMaxDepth(200)})
		if opts.MaxDepth != MaxTraversalDepth {
			t.Errorf("MaxDepth = %d, want %d", opts.MaxDepth, MaxTraversalDepth)
		}
	})

	t.Run("traversal respects depth limit", func(t *testing.T) {
		g := NewGraph("/test")

		// Create a chain: a -> b -> c -> d -> e
		funcs := []string{"a", "b", "c", "d", "e"}
		for _, name := range funcs {
			sym := &ast.Symbol{ID: name + ":1:" + name, Name: name, Kind: ast.SymbolKindFunction}
			g.AddNode(sym)
		}
		for i := 0; i < len(funcs)-1; i++ {
			g.AddEdge(funcs[i]+":1:"+funcs[i], funcs[i+1]+":1:"+funcs[i+1], EdgeTypeCalls, ast.Location{})
		}
		g.Freeze()

		result, err := g.GetCallGraph(ctx, "a:1:a", WithMaxDepth(2))
		if err != nil {
			t.Fatalf("GetCallGraph() error = %v", err)
		}

		// Should visit a (depth 0), b (depth 1), c (depth 2), but not explore beyond c
		// So we get a, b, c
		if len(result.VisitedNodes) != 3 {
			t.Errorf("got %d visited nodes, want 3", len(result.VisitedNodes))
		}
		if result.Depth != 2 {
			t.Errorf("Depth = %d, want 2", result.Depth)
		}
	})
}

func TestGraph_GetCallGraph(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("traverses call tree", func(t *testing.T) {
		result, err := g.GetCallGraph(ctx, "main.go:10:main", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetCallGraph() error = %v", err)
		}

		// main -> setup -> handleAgent
		// main -> run -> helper
		// So we should visit: main, setup, run, handleAgent, helper (5 nodes)
		if len(result.VisitedNodes) != 5 {
			t.Errorf("got %d visited nodes, want 5", len(result.VisitedNodes))
		}
		if result.VisitedNodes[0] != "main.go:10:main" {
			t.Errorf("first visited = %s, want main", result.VisitedNodes[0])
		}
	})

	t.Run("handles cycles gracefully", func(t *testing.T) {
		g2 := NewGraph("/test")

		// Create a cycle: a -> b -> c -> a
		syms := []*ast.Symbol{
			{ID: "a:1:a", Name: "a", Kind: ast.SymbolKindFunction},
			{ID: "b:1:b", Name: "b", Kind: ast.SymbolKindFunction},
			{ID: "c:1:c", Name: "c", Kind: ast.SymbolKindFunction},
		}
		for _, s := range syms {
			g2.AddNode(s)
		}
		g2.AddEdge("a:1:a", "b:1:b", EdgeTypeCalls, ast.Location{})
		g2.AddEdge("b:1:b", "c:1:c", EdgeTypeCalls, ast.Location{})
		g2.AddEdge("c:1:c", "a:1:a", EdgeTypeCalls, ast.Location{})
		g2.Freeze()

		result, err := g2.GetCallGraph(ctx, "a:1:a", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetCallGraph() error = %v", err)
		}

		// Should visit each node exactly once despite cycle
		if len(result.VisitedNodes) != 3 {
			t.Errorf("got %d visited nodes, want 3 (cycle handled)", len(result.VisitedNodes))
		}
	})

	t.Run("non-existent root returns error", func(t *testing.T) {
		_, err := g.GetCallGraph(ctx, "nonexistent:id")
		if err == nil {
			t.Error("GetCallGraph() error = nil, want error for non-existent root")
		}
	})
}

func TestGraph_GetReverseCallGraph(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("returns callers tree", func(t *testing.T) {
		result, err := g.GetReverseCallGraph(ctx, "handlers.go:30:handleAgent", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetReverseCallGraph() error = %v", err)
		}

		// handleAgent <- setup <- main
		// So we should visit: handleAgent, setup, main (3 nodes)
		if len(result.VisitedNodes) != 3 {
			t.Errorf("got %d visited nodes, want 3", len(result.VisitedNodes))
		}
	})
}

func TestGraph_GetTypeHierarchy(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("returns implements and embeds relationships", func(t *testing.T) {
		result, err := g.GetTypeHierarchy(ctx, "types.go:70:FileReader", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetTypeHierarchy() error = %v", err)
		}

		// FileReader implements Reader
		// So we should visit at least: FileReader, Reader
		if len(result.VisitedNodes) < 2 {
			t.Errorf("got %d visited nodes, want at least 2", len(result.VisitedNodes))
		}
	})

	t.Run("interface shows implementers", func(t *testing.T) {
		result, err := g.GetTypeHierarchy(ctx, "types.go:60:Reader", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetTypeHierarchy() error = %v", err)
		}

		// Reader is implemented by FileReader
		if len(result.VisitedNodes) < 2 {
			t.Errorf("got %d visited nodes, want at least 2", len(result.VisitedNodes))
		}
	})
}

func TestGraph_ShortestPath(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("path exists returns valid path", func(t *testing.T) {
		result, err := g.ShortestPath(ctx, "main.go:10:main", "handlers.go:30:handleAgent")
		if err != nil {
			t.Fatalf("ShortestPath() error = %v", err)
		}

		// main -> setup -> handleAgent
		if result.Length != 2 {
			t.Errorf("Length = %d, want 2", result.Length)
		}
		if len(result.Path) != 3 {
			t.Errorf("got %d path nodes, want 3", len(result.Path))
		}
		if result.Path[0] != "main.go:10:main" {
			t.Errorf("path start = %s, want main", result.Path[0])
		}
		if result.Path[2] != "handlers.go:30:handleAgent" {
			t.Errorf("path end = %s, want handleAgent", result.Path[2])
		}
	})

	t.Run("no path returns length -1", func(t *testing.T) {
		// handleAgent has no path to main (wrong direction)
		result, err := g.ShortestPath(ctx, "handlers.go:30:handleAgent", "main.go:10:main")
		if err != nil {
			t.Fatalf("ShortestPath() error = %v", err)
		}

		if result.Length != -1 {
			t.Errorf("Length = %d, want -1", result.Length)
		}
		if len(result.Path) != 0 {
			t.Errorf("got %d path nodes, want 0", len(result.Path))
		}
	})

	t.Run("same node returns length 0", func(t *testing.T) {
		result, err := g.ShortestPath(ctx, "main.go:10:main", "main.go:10:main")
		if err != nil {
			t.Fatalf("ShortestPath() error = %v", err)
		}

		if result.Length != 0 {
			t.Errorf("Length = %d, want 0", result.Length)
		}
		if len(result.Path) != 1 {
			t.Errorf("got %d path nodes, want 1", len(result.Path))
		}
	})

	t.Run("non-existent source returns error", func(t *testing.T) {
		_, err := g.ShortestPath(ctx, "nonexistent", "main.go:10:main")
		if err == nil {
			t.Error("ShortestPath() error = nil, want error")
		}
	})

	t.Run("non-existent target returns error", func(t *testing.T) {
		_, err := g.ShortestPath(ctx, "main.go:10:main", "nonexistent")
		if err == nil {
			t.Error("ShortestPath() error = nil, want error")
		}
	})
}

func TestGraph_ContextCancellation(t *testing.T) {
	t.Run("FindCallersByID respects cancellation", func(t *testing.T) {
		g := NewGraph("/test")

		// Create many callers
		target := &ast.Symbol{ID: "target:1:target", Name: "target", Kind: ast.SymbolKindFunction}
		g.AddNode(target)
		for i := 0; i < 200; i++ {
			caller := &ast.Symbol{
				ID:   fmt.Sprintf("caller%d:1:caller%d", i, i),
				Name: fmt.Sprintf("caller%d", i),
				Kind: ast.SymbolKindFunction,
			}
			g.AddNode(caller)
			g.AddEdge(caller.ID, target.ID, EdgeTypeCalls, ast.Location{})
		}
		g.Freeze()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		result, err := g.FindCallersByID(ctx, "target:1:target")
		if err != nil {
			t.Fatalf("FindCallersByID() error = %v", err)
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true for cancelled context")
		}
	})

	t.Run("GetCallGraph respects cancellation", func(t *testing.T) {
		g := NewGraph("/test")

		// Create a long chain
		for i := 0; i < 500; i++ {
			sym := &ast.Symbol{
				ID:   fmt.Sprintf("func%d:1:func%d", i, i),
				Name: fmt.Sprintf("func%d", i),
				Kind: ast.SymbolKindFunction,
			}
			g.AddNode(sym)
			if i > 0 {
				g.AddEdge(
					fmt.Sprintf("func%d:1:func%d", i-1, i-1),
					fmt.Sprintf("func%d:1:func%d", i, i),
					EdgeTypeCalls,
					ast.Location{},
				)
			}
		}
		g.Freeze()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()
		time.Sleep(10 * time.Millisecond) // Ensure timeout

		result, err := g.GetCallGraph(ctx, "func0:1:func0", WithMaxDepth(1000))
		if err != nil {
			t.Fatalf("GetCallGraph() error = %v", err)
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true for cancelled context")
		}
	})
}

func TestGraph_FindReferencesByID(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	t.Run("returns locations of references", func(t *testing.T) {
		locations, err := g.FindReferencesByID(ctx, "main.go:20:setup")
		if err != nil {
			t.Fatalf("FindReferencesByID() error = %v", err)
		}
		if locations == nil {
			t.Error("locations is nil, want empty slice")
		}
		// setup is called by main, so there should be at least 1 reference
		if len(locations) < 1 {
			t.Errorf("got %d locations, want at least 1", len(locations))
		}
	})

	t.Run("non-existent ID returns empty slice", func(t *testing.T) {
		locations, err := g.FindReferencesByID(ctx, "nonexistent:id")
		if err != nil {
			t.Fatalf("FindReferencesByID() error = %v", err)
		}
		if locations == nil {
			t.Error("locations is nil, want empty slice")
		}
		if len(locations) != 0 {
			t.Errorf("got %d locations, want 0", len(locations))
		}
	})
}

func TestGraph_FindImporters(t *testing.T) {
	ctx := context.Background()

	t.Run("returns files importing package", func(t *testing.T) {
		g := NewGraph("/test")

		// Create some symbols with package info
		fileSym := &ast.Symbol{
			ID:        "main.go:1:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			Package:   "main",
			FilePath:  "main.go",
			StartLine: 1,
			EndLine:   1,
			Language:  "go",
		}
		pkgSym := &ast.Symbol{
			ID:        "fmt:1:Println",
			Name:      "Println",
			Kind:      ast.SymbolKindFunction,
			Package:   "fmt",
			FilePath:  "fmt/print.go",
			StartLine: 1,
			EndLine:   1,
			Language:  "go",
		}
		g.AddNode(fileSym)
		g.AddNode(pkgSym)
		g.AddEdge(fileSym.ID, pkgSym.ID, EdgeTypeImports, ast.Location{})
		g.Freeze()

		files, err := g.FindImporters(ctx, "fmt")
		if err != nil {
			t.Fatalf("FindImporters() error = %v", err)
		}
		if len(files) != 1 {
			t.Errorf("got %d files, want 1", len(files))
		}
		if len(files) > 0 && files[0] != "main.go" {
			t.Errorf("file = %s, want main.go", files[0])
		}
	})
}

func TestGraph_GetDependencyTree(t *testing.T) {
	ctx := context.Background()

	t.Run("returns transitive dependencies", func(t *testing.T) {
		g := NewGraph("/test")

		// main.go -> utils.go -> core.go
		mainSym := &ast.Symbol{
			ID: "main.go:1:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "main.go", StartLine: 1, EndLine: 1, Language: "go",
		}
		utilsSym := &ast.Symbol{
			ID: "utils.go:1:helper", Name: "helper", Kind: ast.SymbolKindFunction,
			FilePath: "utils.go", StartLine: 1, EndLine: 1, Language: "go",
		}
		coreSym := &ast.Symbol{
			ID: "core.go:1:core", Name: "core", Kind: ast.SymbolKindFunction,
			FilePath: "core.go", StartLine: 1, EndLine: 1, Language: "go",
		}
		g.AddNode(mainSym)
		g.AddNode(utilsSym)
		g.AddNode(coreSym)
		g.AddEdge(mainSym.ID, utilsSym.ID, EdgeTypeImports, ast.Location{})
		g.AddEdge(utilsSym.ID, coreSym.ID, EdgeTypeImports, ast.Location{})
		g.Freeze()

		result, err := g.GetDependencyTree(ctx, "main.go", WithMaxDepth(10))
		if err != nil {
			t.Fatalf("GetDependencyTree() error = %v", err)
		}

		// Should include main.go symbol and transitively utils.go and core.go
		if len(result.VisitedNodes) < 3 {
			t.Errorf("got %d visited nodes, want at least 3", len(result.VisitedNodes))
		}
	})

	t.Run("file not in graph returns error", func(t *testing.T) {
		g := NewGraph("/test")
		g.Freeze()

		_, err := g.GetDependencyTree(ctx, "nonexistent.go")
		if err == nil {
			t.Error("GetDependencyTree() error = nil, want error for non-existent file")
		}
	})
}

func TestQueryResult_Duration(t *testing.T) {
	ctx := context.Background()
	g := createTestGraph(t)

	result, err := g.FindCallersByID(ctx, "main.go:10:main")
	if err != nil {
		t.Fatalf("FindCallersByID() error = %v", err)
	}

	// Duration should be set (even if very small)
	if result.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", result.Duration)
	}
}
