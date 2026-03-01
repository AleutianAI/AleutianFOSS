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
	"fmt"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// getIntFromAny extracts an int from an any value (handles int and float64).
func getIntFromAny(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case int64:
		return int(val)
	default:
		return 0
	}
}

// createTestGraphWithCallers creates a test graph with call relationships.
func createTestGraphWithCallers(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create symbols (EndLine >= StartLine, Language required for index validation)
	parseConfig := &ast.Symbol{
		ID:        "config/parser.go:10:parseConfig",
		Name:      "parseConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/parser.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Signature: "func parseConfig(path string) (*Config, error)",
		Exported:  false,
		Language:  "go",
	}

	main := &ast.Symbol{
		ID:        "cmd/app/main.go:5:main",
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "cmd/app/main.go",
		StartLine: 5,
		EndLine:   15,
		Package:   "main",
		Signature: "func main()",
		Exported:  false,
		Language:  "go",
	}

	initServer := &ast.Symbol{
		ID:        "server/init.go:20:initServer",
		Name:      "initServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/init.go",
		StartLine: 20,
		EndLine:   30,
		Package:   "server",
		Signature: "func initServer() error",
		Exported:  false,
		Language:  "go",
	}

	loadConfig := &ast.Symbol{
		ID:        "config/loader.go:15:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 15,
		EndLine:   25,
		Package:   "config",
		Signature: "func LoadConfig() (*Config, error)",
		Exported:  true,
		Language:  "go",
	}

	// Handler interface and implementation
	handler := &ast.Symbol{
		ID:        "handler/handler.go:5:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "handler/handler.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "handler",
		Exported:  true,
		Language:  "go",
	}

	userHandler := &ast.Symbol{
		ID:        "handler/user.go:10:UserHandler",
		Name:      "UserHandler",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "handler/user.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "handler",
		Exported:  true,
		Language:  "go",
	}

	// Add nodes to graph
	g.AddNode(parseConfig)
	g.AddNode(main)
	g.AddNode(initServer)
	g.AddNode(loadConfig)
	g.AddNode(handler)
	g.AddNode(userHandler)

	// Add symbols to index
	if err := idx.Add(parseConfig); err != nil {
		t.Fatalf("Failed to add parseConfig: %v", err)
	}
	if err := idx.Add(main); err != nil {
		t.Fatalf("Failed to add main: %v", err)
	}
	if err := idx.Add(initServer); err != nil {
		t.Fatalf("Failed to add initServer: %v", err)
	}
	if err := idx.Add(loadConfig); err != nil {
		t.Fatalf("Failed to add loadConfig: %v", err)
	}
	if err := idx.Add(handler); err != nil {
		t.Fatalf("Failed to add handler: %v", err)
	}
	if err := idx.Add(userHandler); err != nil {
		t.Fatalf("Failed to add userHandler: %v", err)
	}

	// Verify index is populated
	if idx.Stats().TotalSymbols != 6 {
		t.Fatalf("Expected 6 symbols in index, got %d", idx.Stats().TotalSymbols)
	}

	// Create call edges: main -> parseConfig, initServer -> parseConfig, LoadConfig -> parseConfig
	g.AddEdge(main.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: main.FilePath, StartLine: 10,
	})
	g.AddEdge(initServer.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: initServer.FilePath, StartLine: 25,
	})
	g.AddEdge(loadConfig.ID, parseConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: loadConfig.FilePath, StartLine: 20,
	})

	// Create implements edge: UserHandler implements Handler
	g.AddEdge(userHandler.ID, handler.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: userHandler.FilePath, StartLine: 10,
	})

	g.Freeze()

	return g, idx
}

// createTestGraphWithMultipleMatches creates a graph with multiple functions
// having the same name (e.g., "Setup" in different packages).
func createTestGraphWithMultipleMatches(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create multiple "Setup" functions in different packages
	symbols := []*ast.Symbol{
		{
			ID:        "pkg/a/setup.go:10:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/a/setup.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "a",
			Language:  "go",
		},
		{
			ID:        "pkg/b/setup.go:15:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/b/setup.go",
			StartLine: 15,
			EndLine:   25,
			Package:   "b",
			Language:  "go",
		},
		{
			ID:        "pkg/c/setup.go:20:Setup",
			Name:      "Setup",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/c/setup.go",
			StartLine: 20,
			EndLine:   30,
			Package:   "c",
			Language:  "go",
		},
		{
			ID:        "main.go:5:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "main.go",
			StartLine: 5,
			EndLine:   15,
			Package:   "main",
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol %s: %v", sym.ID, err)
		}
	}

	// main calls all three Setup functions
	g.AddEdge("main.go:5:main", "pkg/a/setup.go:10:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 10,
	})
	g.AddEdge("main.go:5:main", "pkg/b/setup.go:15:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 11,
	})
	g.AddEdge("main.go:5:main", "pkg/c/setup.go:20:Setup", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 12,
	})

	g.Freeze()

	return g, idx
}

// createUnfrozenTestGraph creates a minimal test graph that is NOT frozen.
// Used to test tools that require a frozen graph (e.g., find_similar_code Build).
func createUnfrozenTestGraph(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	sym := &ast.Symbol{
		ID:        "test/func.go:1:testFunc",
		Name:      "testFunc",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test/func.go",
		StartLine: 1,
		EndLine:   10,
		Package:   "test",
		Language:  "go",
	}
	g.AddNode(sym)
	if err := idx.Add(sym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	// Deliberately NOT calling g.Freeze()
	return g, idx
}

// createLargeGraph creates a graph with many symbols for benchmarking.
func createLargeGraph(b *testing.B, size int) (*graph.Graph, *index.SymbolIndex) {
	b.Helper()

	g := graph.NewGraph("/benchmark")
	idx := index.NewSymbolIndex()

	// Create a chain of function calls (StartLine must be >= 1)
	var symbols []*ast.Symbol
	for i := 0; i < size; i++ {
		startLine := i*10 + 1 // 1-indexed, starting at 1
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("pkg/module%d/file.go:%d:Function%d", i, startLine, i),
			Name:      fmt.Sprintf("Function%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("pkg/module%d/file.go", i),
			StartLine: startLine,
			EndLine:   startLine + 10,
			Package:   fmt.Sprintf("module%d", i),
			Language:  "go",
		}
		symbols = append(symbols, sym)
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			b.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Create call edges: each function calls the next
	for i := 0; i < size-1; i++ {
		g.AddEdge(symbols[i].ID, symbols[i+1].ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: symbols[i].FilePath, StartLine: symbols[i].StartLine + 5,
		})
	}

	g.Freeze()

	return g, idx
}

// createTestGraphForAnalytics creates a test graph with call relationships
// suitable for analytics queries (hotspots, dead code, cycles, paths).
func createTestGraphForAnalytics(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a graph with various patterns:
	// - funcA is a hotspot (called by many)
	// - funcD is dead code (no callers)
	// - funcB and funcC form a cycle
	// - main -> funcA -> funcB -> funcC -> funcB (cycle)
	symbols := []*ast.Symbol{
		{
			ID:        "main.go:10:main",
			Name:      "main",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "main.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "main",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "core/funcA.go:10:funcA",
			Name:      "funcA",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcA.go",
			StartLine: 10,
			EndLine:   30,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/funcB.go:10:funcB",
			Name:      "funcB",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcB.go",
			StartLine: 10,
			EndLine:   25,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/funcC.go:10:funcC",
			Name:      "funcC",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/funcC.go",
			StartLine: 10,
			EndLine:   25,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "util/funcD.go:10:funcD",
			Name:      "funcD",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "util/funcD.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "util",
			Exported:  false, // Unexported dead code
			Language:  "go",
		},
		{
			ID:        "util/helper.go:5:helper",
			Name:      "helper",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "util/helper.go",
			StartLine: 5,
			EndLine:   15,
			Package:   "util",
			Exported:  false,
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Create edges:
	// main -> funcA (funcA is called by main)
	g.AddEdge("main.go:10:main", "core/funcA.go:10:funcA", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 15,
	})
	// funcA -> funcB
	g.AddEdge("core/funcA.go:10:funcA", "core/funcB.go:10:funcB", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcA.go", StartLine: 20,
	})
	// funcA -> helper (funcA is a hotspot)
	g.AddEdge("core/funcA.go:10:funcA", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcA.go", StartLine: 22,
	})
	// funcB -> funcC
	g.AddEdge("core/funcB.go:10:funcB", "core/funcC.go:10:funcC", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcB.go", StartLine: 15,
	})
	// funcC -> funcB (creates cycle B <-> C)
	g.AddEdge("core/funcC.go:10:funcC", "core/funcB.go:10:funcB", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/funcC.go", StartLine: 15,
	})

	g.Freeze()
	return g, idx
}
