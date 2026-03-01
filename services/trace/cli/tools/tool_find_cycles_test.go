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

// createTestGraphForCycles builds a graph with known cycles for testing.
// It creates:
//   - A 2-node cycle: funcA <-> funcB (pkg "core")
//   - A 3-node cycle: funcC -> funcD -> funcE -> funcC (pkg "util")
//   - A 4-node cross-package cycle: funcF -> funcG -> funcH -> funcI -> funcF (pkgs "net", "http")
//   - An isolated node: funcZ (no cycles)
func createTestGraphForCycles(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		// 2-node cycle in "core"
		{ID: "core/a.go:10:funcA", Name: "funcA", Kind: ast.SymbolKindFunction, FilePath: "core/a.go", StartLine: 10, EndLine: 20, Package: "core", Language: "go"},
		{ID: "core/b.go:10:funcB", Name: "funcB", Kind: ast.SymbolKindFunction, FilePath: "core/b.go", StartLine: 10, EndLine: 20, Package: "core", Language: "go"},
		// 3-node cycle in "util"
		{ID: "util/c.go:10:funcC", Name: "funcC", Kind: ast.SymbolKindFunction, FilePath: "util/c.go", StartLine: 10, EndLine: 20, Package: "util", Language: "go"},
		{ID: "util/d.go:10:funcD", Name: "funcD", Kind: ast.SymbolKindFunction, FilePath: "util/d.go", StartLine: 10, EndLine: 20, Package: "util", Language: "go"},
		{ID: "util/e.go:10:funcE", Name: "funcE", Kind: ast.SymbolKindFunction, FilePath: "util/e.go", StartLine: 10, EndLine: 20, Package: "util", Language: "go"},
		// 4-node cross-package cycle in "net" and "http"
		{ID: "net/f.go:10:funcF", Name: "funcF", Kind: ast.SymbolKindFunction, FilePath: "net/f.go", StartLine: 10, EndLine: 20, Package: "net", Language: "go"},
		{ID: "net/g.go:10:funcG", Name: "funcG", Kind: ast.SymbolKindFunction, FilePath: "net/g.go", StartLine: 10, EndLine: 20, Package: "net", Language: "go"},
		{ID: "http/h.go:10:funcH", Name: "funcH", Kind: ast.SymbolKindFunction, FilePath: "http/h.go", StartLine: 10, EndLine: 20, Package: "http", Language: "go"},
		{ID: "http/i.go:10:funcI", Name: "funcI", Kind: ast.SymbolKindFunction, FilePath: "http/i.go", StartLine: 10, EndLine: 20, Package: "http", Language: "go"},
		// Isolated node (no cycle)
		{ID: "main.go:10:funcZ", Name: "funcZ", Kind: ast.SymbolKindFunction, FilePath: "main.go", StartLine: 10, EndLine: 20, Package: "main", Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol: %v", err)
		}
	}

	loc := ast.Location{FilePath: "test", StartLine: 1}

	// 2-node cycle: A <-> B
	g.AddEdge("core/a.go:10:funcA", "core/b.go:10:funcB", graph.EdgeTypeCalls, loc)
	g.AddEdge("core/b.go:10:funcB", "core/a.go:10:funcA", graph.EdgeTypeCalls, loc)

	// 3-node cycle: C -> D -> E -> C
	g.AddEdge("util/c.go:10:funcC", "util/d.go:10:funcD", graph.EdgeTypeCalls, loc)
	g.AddEdge("util/d.go:10:funcD", "util/e.go:10:funcE", graph.EdgeTypeCalls, loc)
	g.AddEdge("util/e.go:10:funcE", "util/c.go:10:funcC", graph.EdgeTypeCalls, loc)

	// 4-node cross-package cycle: F -> G -> H -> I -> F
	g.AddEdge("net/f.go:10:funcF", "net/g.go:10:funcG", graph.EdgeTypeCalls, loc)
	g.AddEdge("net/g.go:10:funcG", "http/h.go:10:funcH", graph.EdgeTypeCalls, loc)
	g.AddEdge("http/h.go:10:funcH", "http/i.go:10:funcI", graph.EdgeTypeCalls, loc)
	g.AddEdge("http/i.go:10:funcI", "net/f.go:10:funcF", graph.EdgeTypeCalls, loc)

	// funcZ calls funcA but is not in any cycle
	g.AddEdge("main.go:10:funcZ", "core/a.go:10:funcA", graph.EdgeTypeCalls, loc)

	g.Freeze()
	return g, idx
}

func TestFindCycles_ParseParams(t *testing.T) {
	tool := &findCyclesTool{logger: testLogger()}

	t.Run("defaults", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.MinSize != 2 {
			t.Errorf("expected MinSize=2, got %d", p.MinSize)
		}
		if p.Limit != 20 {
			t.Errorf("expected Limit=20, got %d", p.Limit)
		}
		if p.PackageFilter != "" {
			t.Errorf("expected PackageFilter='', got %q", p.PackageFilter)
		}
		if p.SortBy != "length_desc" {
			t.Errorf("expected SortBy='length_desc', got %q", p.SortBy)
		}
	})

	t.Run("min_size clamped to 2 when below minimum", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"min_size": 1})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.MinSize != 2 {
			t.Errorf("expected MinSize=2 (clamped), got %d", p.MinSize)
		}
	})

	t.Run("limit clamped to 1 when below minimum", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"limit": 0})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.Limit != 1 {
			t.Errorf("expected Limit=1 (clamped), got %d", p.Limit)
		}
	})

	t.Run("limit clamped to 100 when above maximum", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"limit": 999})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.Limit != 100 {
			t.Errorf("expected Limit=100 (clamped), got %d", p.Limit)
		}
	})

	t.Run("package_filter set", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"package_filter": "core"})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.PackageFilter != "core" {
			t.Errorf("expected PackageFilter='core', got %q", p.PackageFilter)
		}
	})

	t.Run("sort_by length_asc", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"sort_by": "length_asc"})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.SortBy != "length_asc" {
			t.Errorf("expected SortBy='length_asc', got %q", p.SortBy)
		}
	})

	t.Run("sort_by invalid falls back to default", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"sort_by": "invalid"})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.SortBy != "length_desc" {
			t.Errorf("expected SortBy='length_desc' (default), got %q", p.SortBy)
		}
	})
}

func TestFindCycles_NilAnalytics(t *testing.T) {
	tool := NewFindCyclesTool(nil, nil)
	ctx := context.Background()

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Error("expected Success=false with nil analytics")
	}
	if !strings.Contains(result.Error, "not initialized") {
		t.Errorf("expected 'not initialized' in error, got %q", result.Error)
	}
}

func TestFindCycles_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	t.Run("finds all cycles with defaults", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 3 {
			t.Errorf("expected 3 cycles, got %d", output.CycleCount)
		}
	})

	t.Run("min_size=3 filters 2-node cycle", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"min_size": 3}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 2 {
			t.Errorf("expected 2 cycles (3-node and 4-node), got %d", output.CycleCount)
		}
		for _, cycle := range output.Cycles {
			if cycle.Length < 3 {
				t.Errorf("expected all cycles to have length >= 3, got %d", cycle.Length)
			}
		}
	})

	t.Run("limit=1 caps results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"limit": 1}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 1 {
			t.Errorf("expected 1 cycle with limit=1, got %d", output.CycleCount)
		}
	})
}

func TestFindCycles_PackageFilter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	t.Run("filter by core returns 2-node cycle only", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package_filter": "core",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 1 {
			t.Errorf("expected 1 cycle for 'core' filter, got %d", output.CycleCount)
		}
		if output.CycleCount > 0 && output.Cycles[0].Length != 2 {
			t.Errorf("expected 2-node cycle for 'core', got %d nodes", output.Cycles[0].Length)
		}
	})

	t.Run("filter by http returns cross-package cycle", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package_filter": "http",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 1 {
			t.Errorf("expected 1 cycle for 'http' filter, got %d", output.CycleCount)
		}
		if output.CycleCount > 0 && output.Cycles[0].Length != 4 {
			t.Errorf("expected 4-node cycle for 'http', got %d nodes", output.Cycles[0].Length)
		}
	})

	t.Run("case-insensitive filter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package_filter": "UTIL",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 1 {
			t.Errorf("expected 1 cycle for 'UTIL' filter (case-insensitive), got %d", output.CycleCount)
		}
	})

	t.Run("nonexistent package returns zero", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package_filter": "nonexistent",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount != 0 {
			t.Errorf("expected 0 cycles for nonexistent package, got %d", output.CycleCount)
		}
	})
}

func TestFindCycles_SortBy(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	t.Run("length_desc returns largest first", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"sort_by": "length_desc",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount < 2 {
			t.Fatalf("need at least 2 cycles for sort test, got %d", output.CycleCount)
		}
		if output.Cycles[0].Length < output.Cycles[output.CycleCount-1].Length {
			t.Errorf("length_desc: first cycle (%d) should be >= last cycle (%d)",
				output.Cycles[0].Length, output.Cycles[output.CycleCount-1].Length)
		}
	})

	t.Run("length_asc returns smallest first", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"sort_by": "length_asc",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCyclesOutput)
		if !ok {
			t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
		}

		if output.CycleCount < 2 {
			t.Fatalf("need at least 2 cycles for sort test, got %d", output.CycleCount)
		}
		if output.Cycles[0].Length > output.Cycles[output.CycleCount-1].Length {
			t.Errorf("length_asc: first cycle (%d) should be <= last cycle (%d)",
				output.Cycles[0].Length, output.Cycles[output.CycleCount-1].Length)
		}
	})
}

func TestFindCycles_FormatText(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	t.Run("output starts with Found prefix", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if !strings.HasPrefix(result.OutputText, "Found ") {
			t.Errorf("expected OutputText to start with 'Found ', got: %q",
				result.OutputText[:min(80, len(result.OutputText))])
		}
		if !strings.Contains(result.OutputText, "circular dependencies") {
			t.Error("expected 'circular dependencies' in output")
		}
	})

	t.Run("output contains cycle back marker", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if !strings.Contains(result.OutputText, "(cycle back)") {
			t.Error("expected '(cycle back)' marker in cycle output")
		}
	})

	t.Run("output contains resolved symbol names", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// At least some symbols should be resolved to their names
		if !strings.Contains(result.OutputText, "funcA()") &&
			!strings.Contains(result.OutputText, "funcC()") &&
			!strings.Contains(result.OutputText, "funcF()") {
			t.Error("expected at least one resolved symbol name in output")
		}
	})

	t.Run("zero cycles output", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package_filter": "nonexistent",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if !strings.Contains(result.OutputText, "No circular dependencies found") {
			t.Error("expected 'No circular dependencies found' for zero results")
		}
	})
}

func TestFindCycles_NilIndex(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	// Create tool with nil index — should still work, just no symbol resolution
	tool := NewFindCyclesTool(analytics, nil)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCyclesOutput)
	if !ok {
		t.Fatalf("Output is not FindCyclesOutput, got %T", result.Output)
	}

	// Should still find cycles even without symbol resolution
	if output.CycleCount == 0 {
		t.Error("expected cycles even with nil index")
	}

	// Nodes should have IDs but no resolved names
	for _, cycle := range output.Cycles {
		for _, node := range cycle.Nodes {
			if node.ID == "" {
				t.Error("expected node ID to be set even with nil index")
			}
		}
	}
}

func TestFindCycles_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForCycles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCyclesTool(analytics, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestFindCycles_ToMap(t *testing.T) {
	p := FindCyclesParams{
		MinSize:       3,
		Limit:         10,
		PackageFilter: "core",
		SortBy:        "length_asc",
	}

	m := p.ToMap()

	if v, ok := m["min_size"].(int); !ok || v != 3 {
		t.Error("expected min_size=3 in map")
	}
	if v, ok := m["limit"].(int); !ok || v != 10 {
		t.Error("expected limit=10 in map")
	}
	if v, ok := m["package_filter"].(string); !ok || v != "core" {
		t.Error("expected package_filter='core' in map")
	}
	if v, ok := m["sort_by"].(string); !ok || v != "length_asc" {
		t.Error("expected sort_by='length_asc' in map")
	}
}

func TestFindCycles_ToMapOmitsEmptyPackageFilter(t *testing.T) {
	p := FindCyclesParams{
		MinSize: 2,
		Limit:   20,
		SortBy:  "length_desc",
	}

	m := p.ToMap()

	if _, ok := m["package_filter"]; ok {
		t.Error("expected package_filter to be omitted when empty")
	}
}

func TestFindCycles_Definition(t *testing.T) {
	tool := &findCyclesTool{logger: testLogger()}
	def := tool.Definition()

	if def.Name != "find_cycles" {
		t.Errorf("expected Name='find_cycles', got %q", def.Name)
	}

	// Verify all 4 parameters are defined
	expectedParams := []string{"min_size", "limit", "package_filter", "sort_by"}
	for _, param := range expectedParams {
		if _, ok := def.Parameters[param]; !ok {
			t.Errorf("expected parameter %q in definition", param)
		}
	}

	if def.Category != CategoryExploration {
		t.Errorf("expected Category=CategoryExploration, got %v", def.Category)
	}
}
