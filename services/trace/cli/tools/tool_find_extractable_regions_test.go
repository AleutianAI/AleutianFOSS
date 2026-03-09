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

// createTestGraphForExtractableRegions builds a graph with a "main" entry point
// and enough structure for SESE region detection.
func createTestGraphForExtractableRegions(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
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

	// Build a graph with potential SESE regions:
	// main -> init -> A -> B -> D
	//                 A -> C -> D
	g.AddEdge("cmd/main.go:10:main", "pkg/init.go:10:init", graph.EdgeTypeCalls, ast.Location{FilePath: "cmd/main.go", StartLine: 15})
	g.AddEdge("pkg/init.go:10:init", "pkg/a.go:10:A", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/init.go", StartLine: 15})
	g.AddEdge("pkg/a.go:10:A", "pkg/b.go:10:B", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 15})
	g.AddEdge("pkg/a.go:10:A", "pkg/c.go:10:C", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/a.go", StartLine: 16})
	g.AddEdge("pkg/b.go:10:B", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/b.go", StartLine: 15})
	g.AddEdge("pkg/c.go:10:C", "pkg/d.go:10:D", graph.EdgeTypeCalls, ast.Location{FilePath: "pkg/c.go", StartLine: 15})

	g.Freeze()
	return g, idx
}

func TestFindExtractableRegions_CRS_Metadata(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForExtractableRegions(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindExtractableRegionsTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated")
	}

	meta := result.TraceStep.Metadata
	requiredKeys := []string{
		"min_size", "max_size", "top",
		"raw_region_count", "final_count",
	}
	for _, key := range requiredKeys {
		t.Run("has_"+key, func(t *testing.T) {
			if _, ok := meta[key]; !ok {
				t.Errorf("TraceStep.Metadata missing '%s'", key)
			}
		})
	}
}
