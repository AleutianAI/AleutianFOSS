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

// createTestGraphForImportant builds a graph suitable for PageRank analysis.
func createTestGraphForImportant(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{ID: "main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 20, Package: "main", Exported: false, Language: "go"},
		{ID: "core/server.go:10:Server", Name: "Server", Kind: ast.SymbolKindStruct, FilePath: "core/server.go", StartLine: 10, EndLine: 30, Package: "core", Exported: true, Language: "go"},
		{ID: "core/server.go:35:handleRequest", Name: "handleRequest", Kind: ast.SymbolKindMethod, FilePath: "core/server.go", StartLine: 35, EndLine: 60, Package: "core", Exported: true, Language: "go"},
		{ID: "core/config.go:10:parseConfig", Name: "parseConfig", Kind: ast.SymbolKindFunction, FilePath: "core/config.go", StartLine: 10, EndLine: 30, Package: "core", Exported: true, Language: "go"},
		{ID: "util/helper.go:5:helper", Name: "helper", Kind: ast.SymbolKindFunction, FilePath: "util/helper.go", StartLine: 5, EndLine: 15, Package: "util", Exported: false, Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Build edges for PageRank to produce meaningful scores
	g.AddEdge("main.go:10:main", "core/config.go:10:parseConfig", graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 12})
	g.AddEdge("main.go:10:main", "core/server.go:35:handleRequest", graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 14})
	g.AddEdge("core/server.go:35:handleRequest", "core/config.go:10:parseConfig", graph.EdgeTypeCalls, ast.Location{FilePath: "core/server.go", StartLine: 40})
	g.AddEdge("core/server.go:35:handleRequest", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{FilePath: "core/server.go", StartLine: 45})
	g.AddEdge("core/config.go:10:parseConfig", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{FilePath: "core/config.go", StartLine: 20})
	g.AddEdge("core/server.go:10:Server", "core/server.go:35:handleRequest", graph.EdgeTypeCalls, ast.Location{FilePath: "core/server.go", StartLine: 10})

	g.Freeze()
	return g, idx
}

func TestFindImportant_CRS_Metadata(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForImportant(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated")
	}

	meta := result.TraceStep.Metadata
	requiredKeys := []string{
		"top", "kind", "package", "exclude_tests",
		"reverse", "raw_count", "final_count",
		"grc_files_reclassified", // IT_CRS_03 AC-4
	}
	for _, key := range requiredKeys {
		t.Run("has_"+key, func(t *testing.T) {
			if _, ok := meta[key]; !ok {
				t.Errorf("TraceStep.Metadata missing '%s'", key)
			}
		})
	}
}

// IT_CRS_03 AC-4: Verify grc_files_reclassified is populated.
func TestFindImportant_GRCFilesReclassified(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForImportant(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindImportantTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"exclude_tests": true,
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated")
	}
	val, ok := result.TraceStep.Metadata["grc_files_reclassified"]
	if !ok {
		t.Fatal("grc_files_reclassified metadata missing")
	}
	// Value should be a valid integer string (may be "0" if no test files were filtered)
	if val == "" {
		t.Error("grc_files_reclassified should not be empty")
	}
}
