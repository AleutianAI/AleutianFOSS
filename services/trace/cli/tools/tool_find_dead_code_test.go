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

// createTestGraphForDeadCode builds a graph with dead code symbols for testing.
// It creates:
//   - main (entry point, has callers via edge chain)
//   - usedFunc (called by main)
//   - deadFunc (unexported, no callers)
//   - DeadExported (exported, no callers)
//   - testHelper (in test file, no callers)
//   - deadInPkg (in "pkg" package, no callers)
//   - deadByPath (in file path containing "pkg", different package name)
func createTestGraphForDeadCode(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

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
			ID:        "core/used.go:10:usedFunc",
			Name:      "usedFunc",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/used.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "core",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "core/dead.go:10:deadFunc",
			Name:      "deadFunc",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/dead.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "core",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "core/dead.go:30:DeadExported",
			Name:      "DeadExported",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/dead.go",
			StartLine: 30,
			EndLine:   40,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/dead_test.go:10:testHelper",
			Name:      "testHelper",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/dead_test.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "core",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "pkg/utils.go:10:deadInPkg",
			Name:      "deadInPkg",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pkg/utils.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "pkg",
			Exported:  false,
			Language:  "go",
		},
		{
			ID:        "lib/pkg/helper.go:10:deadByPath",
			Name:      "deadByPath",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "lib/pkg/helper.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "helper",
			Exported:  false,
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol: %v", err)
		}
	}

	// main -> usedFunc (so usedFunc is NOT dead)
	g.AddEdge("main.go:10:main", "core/used.go:10:usedFunc", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 12,
	})

	g.Freeze()
	return g, idx
}

func TestFindDeadCode_ParseParams(t *testing.T) {
	tool := &findDeadCodeTool{logger: testLogger()}

	t.Run("defaults", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.IncludeExported != false {
			t.Errorf("expected IncludeExported=false, got %v", p.IncludeExported)
		}
		if p.Limit != 50 {
			t.Errorf("expected Limit=50, got %d", p.Limit)
		}
		if p.ExcludeTests != true {
			t.Errorf("expected ExcludeTests=true, got %v", p.ExcludeTests)
		}
		if p.Package != "" {
			t.Errorf("expected Package='', got %q", p.Package)
		}
	})

	t.Run("include_exported true", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"include_exported": true})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if !p.IncludeExported {
			t.Error("expected IncludeExported=true")
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

	t.Run("limit clamped to 500 when above maximum", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"limit": 999})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.Limit != 500 {
			t.Errorf("expected Limit=500 (clamped), got %d", p.Limit)
		}
	})

	t.Run("package filter set", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"package": "core"})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.Package != "core" {
			t.Errorf("expected Package='core', got %q", p.Package)
		}
	})

	t.Run("exclude_tests false", func(t *testing.T) {
		p, err := tool.parseParams(map[string]any{"exclude_tests": false})
		if err != nil {
			t.Fatalf("parseParams() error = %v", err)
		}
		if p.ExcludeTests != false {
			t.Errorf("expected ExcludeTests=false, got %v", p.ExcludeTests)
		}
	})
}

func TestFindDeadCode_NilAnalytics(t *testing.T) {
	tool := NewFindDeadCodeTool(nil, nil)
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

func TestFindDeadCode_GraphMarkers(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForDeadCode(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("positive result has Found prefix and exhaustive footer", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_exported": true,
			"exclude_tests":    false,
		}})
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
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected 'these results are exhaustive' in positive output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read to verify' in positive output")
		}
	})

	t.Run("zero result has GRAPH RESULT header and exhaustive footer", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "nonexistent_package_xyz",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if !strings.Contains(result.OutputText, "## GRAPH RESULT") {
			t.Error("expected '## GRAPH RESULT' header in zero-result output")
		}
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected 'these results are exhaustive' in zero-result output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read to verify' in zero-result output")
		}
	})
}

func TestFindDeadCode_PackageFilter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForDeadCode(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("exact package match", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "pkg",
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		// Should find deadInPkg (exact match) and deadByPath (file path match)
		if output.DeadCodeCount == 0 {
			t.Error("expected dead code in 'pkg' filter, got 0")
		}

		foundExact := false
		foundPath := false
		for _, dc := range output.DeadCode {
			if dc.Name == "deadInPkg" {
				foundExact = true
			}
			if dc.Name == "deadByPath" {
				foundPath = true
			}
		}
		if !foundExact {
			t.Error("expected deadInPkg to match via exact package match")
		}
		if !foundPath {
			t.Error("expected deadByPath to match via file path substring")
		}
	})

	t.Run("nonexistent package returns zero results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "nonexistent",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		if output.DeadCodeCount != 0 {
			t.Errorf("expected 0 dead code for nonexistent package, got %d", output.DeadCodeCount)
		}
	})
}

func TestFindDeadCode_ExportedFilter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForDeadCode(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("include_exported=false skips exported symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_exported": false,
			"exclude_tests":    false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		for _, dc := range output.DeadCode {
			if dc.Exported {
				t.Errorf("include_exported=false returned exported symbol: %s", dc.Name)
			}
		}
	})

	t.Run("include_exported=true includes exported symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"include_exported": true,
			"exclude_tests":    false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		foundExported := false
		for _, dc := range output.DeadCode {
			if dc.Exported {
				foundExported = true
				break
			}
		}
		if !foundExported {
			t.Error("include_exported=true should include exported symbols")
		}
	})
}

func TestFindDeadCode_ExcludeTests(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForDeadCode(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("exclude_tests=true filters test file symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": true,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		for _, dc := range output.DeadCode {
			if isTestFile(dc.File) {
				t.Errorf("exclude_tests=true returned test file symbol: %s (%s)", dc.Name, dc.File)
			}
		}
	})

	t.Run("exclude_tests=false includes test file symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		foundTestFile := false
		for _, dc := range output.DeadCode {
			if isTestFile(dc.File) {
				foundTestFile = true
				break
			}
		}
		if !foundTestFile {
			t.Error("exclude_tests=false should include test file symbols")
		}
	})
}

func TestFindDeadCode_Limit(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForDeadCode(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindDeadCodeTool(analytics, idx)

	t.Run("limit caps results", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"limit":            1,
			"include_exported": true,
			"exclude_tests":    false,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindDeadCodeOutput)
		if !ok {
			t.Fatalf("Output is not FindDeadCodeOutput, got %T", result.Output)
		}

		if output.DeadCodeCount > 1 {
			t.Errorf("expected at most 1 result with limit=1, got %d", output.DeadCodeCount)
		}
	})
}

func TestFindDeadCode_WhenToUse(t *testing.T) {
	tool := &findDeadCodeTool{logger: testLogger()}
	def := tool.Definition()

	if len(def.WhenToUse.Keywords) == 0 {
		t.Error("expected WhenToUse.Keywords to be populated")
	}
	if def.WhenToUse.UseWhen == "" {
		t.Error("expected WhenToUse.UseWhen to be non-empty")
	}
	if def.WhenToUse.AvoidWhen == "" {
		t.Error("expected WhenToUse.AvoidWhen to be non-empty")
	}

	// Verify key routing keywords are present
	keywords := strings.Join(def.WhenToUse.Keywords, " ")
	for _, expected := range []string{"dead code", "unused", "unreferenced", "no incoming calls", "never called"} {
		if !strings.Contains(keywords, expected) {
			t.Errorf("expected keyword %q in WhenToUse.Keywords", expected)
		}
	}
}

func TestFindDeadCode_ToMap(t *testing.T) {
	p := FindDeadCodeParams{
		IncludeExported: true,
		Package:         "core",
		Limit:           25,
		ExcludeTests:    false,
	}

	m := p.ToMap()

	if v, ok := m["include_exported"].(bool); !ok || !v {
		t.Error("expected include_exported=true in map")
	}
	if v, ok := m["package"].(string); !ok || v != "core" {
		t.Error("expected package='core' in map")
	}
	if v, ok := m["limit"].(int); !ok || v != 25 {
		t.Error("expected limit=25 in map")
	}
	if v, ok := m["exclude_tests"].(bool); !ok || v != false {
		t.Error("expected exclude_tests=false in map")
	}
}

func TestFindDeadCode_ToMapOmitsEmptyPackage(t *testing.T) {
	p := FindDeadCodeParams{
		Limit:        50,
		ExcludeTests: true,
	}

	m := p.ToMap()

	if _, ok := m["package"]; ok {
		t.Error("expected package to be omitted when empty")
	}
}
