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
	"fmt"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// createTestGraphForHotspots builds a graph with diverse symbol kinds and packages
// for testing hotspot detection with kind and package filtering.
func createTestGraphForHotspots(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
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
			ID:        "core/server.go:10:Server",
			Name:      "Server",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "core/server.go",
			StartLine: 10,
			EndLine:   30,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/server.go:35:handleRequest",
			Name:      "handleRequest",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "core/server.go",
			StartLine: 35,
			EndLine:   60,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/config.go:10:parseConfig",
			Name:      "parseConfig",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "core/config.go",
			StartLine: 10,
			EndLine:   30,
			Package:   "core",
			Exported:  true,
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
		{
			ID:        "types/user.go:10:UserType",
			Name:      "UserType",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "types/user.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "types",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "types/role.go:10:Role",
			Name:      "Role",
			Kind:      ast.SymbolKindEnum,
			FilePath:  "types/role.go",
			StartLine: 10,
			EndLine:   15,
			Package:   "types",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "core/constants.go:5:MaxRetries",
			Name:      "MaxRetries",
			Kind:      ast.SymbolKindConstant,
			FilePath:  "core/constants.go",
			StartLine: 5,
			EndLine:   5,
			Package:   "core",
			Exported:  true,
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Edges: make parseConfig and handleRequest high-connectivity hotspots
	// main -> parseConfig
	g.AddEdge("main.go:10:main", "core/config.go:10:parseConfig", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 12,
	})
	// main -> handleRequest
	g.AddEdge("main.go:10:main", "core/server.go:35:handleRequest", graph.EdgeTypeCalls, ast.Location{
		FilePath: "main.go", StartLine: 14,
	})
	// handleRequest -> parseConfig
	g.AddEdge("core/server.go:35:handleRequest", "core/config.go:10:parseConfig", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/server.go", StartLine: 40,
	})
	// handleRequest -> helper
	g.AddEdge("core/server.go:35:handleRequest", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/server.go", StartLine: 45,
	})
	// parseConfig -> helper
	g.AddEdge("core/config.go:10:parseConfig", "util/helper.go:5:helper", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/config.go", StartLine: 20,
	})
	// Server implements UserType
	g.AddEdge("core/server.go:10:Server", "types/user.go:10:UserType", graph.EdgeTypeImplements, ast.Location{
		FilePath: "core/server.go", StartLine: 10,
	})
	// handleRequest -> MaxRetries (reads constant)
	g.AddEdge("core/server.go:35:handleRequest", "core/constants.go:5:MaxRetries", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/server.go", StartLine: 50,
	})
	// handleRequest -> Role (uses enum)
	g.AddEdge("core/server.go:35:handleRequest", "types/role.go:10:Role", graph.EdgeTypeCalls, ast.Location{
		FilePath: "core/server.go", StartLine: 55,
	})

	g.Freeze()
	return g, idx
}

// TestFindHotspots_GraphMarkers verifies that IT-07 Bug 1 graph markers are present
// in both zero-result and positive-result paths.
func TestFindHotspots_GraphMarkers(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForHotspots(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	t.Run("positive result has Found prefix and exhaustive footer", func(t *testing.T) {
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
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected 'these results are exhaustive' in positive output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read to verify' in positive output")
		}
	})

	t.Run("nonexistent package returns empty results", func(t *testing.T) {
		// CR-11 restored: When package filter matches nothing, that IS the correct
		// answer. Do NOT fall back to global results — that gives wrong-scope data.
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "nonexistent_package_xyz",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}
		if output.HotspotCount != 0 {
			t.Errorf("expected 0 results for nonexistent package, got %d", output.HotspotCount)
		}
	})
}

// TestFindHotspots_KindFilterExpanded tests IT-07 Bug 2: all extractKindFromQuery()
// output values are correctly handled by matchesHotspotKind().
func TestFindHotspots_KindFilterExpanded(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForHotspots(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	t.Run("method filter returns functions and methods", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"kind": "method",
			"top":  100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		for _, hs := range output.Hotspots {
			if hs.Kind != "function" && hs.Kind != "method" {
				t.Errorf("kind filter 'method' returned unexpected kind: %s (symbol: %s)", hs.Kind, hs.Name)
			}
		}
	})

	t.Run("struct filter returns type-like symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"kind": "struct",
			"top":  100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		for _, hs := range output.Hotspots {
			validKinds := map[string]bool{"type": true, "struct": true, "interface": true, "class": true}
			if !validKinds[hs.Kind] {
				t.Errorf("kind filter 'struct' returned unexpected kind: %s (symbol: %s)", hs.Kind, hs.Name)
			}
		}
	})

	t.Run("interface filter returns type-like symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"kind": "interface",
			"top":  100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		for _, hs := range output.Hotspots {
			validKinds := map[string]bool{"type": true, "struct": true, "interface": true, "class": true}
			if !validKinds[hs.Kind] {
				t.Errorf("kind filter 'interface' returned unexpected kind: %s (symbol: %s)", hs.Kind, hs.Name)
			}
		}
	})

	t.Run("class filter accepted in parseParams", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"kind": "class",
			"top":  100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Just verify it doesn't default to "all"
		// If it defaulted to "all", it would return functions too
		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}
		for _, hs := range output.Hotspots {
			validKinds := map[string]bool{"type": true, "struct": true, "interface": true, "class": true}
			if !validKinds[hs.Kind] {
				t.Errorf("kind filter 'class' returned unexpected kind: %s (symbol: %s)", hs.Kind, hs.Name)
			}
		}
	})
}

// TestFindHotspots_PackageFilter tests IT-07 Bug 3: package parameter filters results.
func TestFindHotspots_PackageFilter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForHotspots(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	t.Run("filter by exact package name", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "core",
			"top":     100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		if output.HotspotCount == 0 {
			t.Error("expected hotspots in 'core' package, got 0")
		}

		for _, hs := range output.Hotspots {
			if hs.Package != "core" && !strings.Contains(strings.ToLower(hs.File), "core") {
				t.Errorf("package filter 'core' returned symbol from wrong package: %s (package: %s, file: %s)",
					hs.Name, hs.Package, hs.File)
			}
		}

		// Verify package name appears in output text
		if !strings.Contains(result.OutputText, "package 'core'") {
			t.Error("expected package name in output text header")
		}
	})

	t.Run("filter by file path substring", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "util",
			"top":     100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		for _, hs := range output.Hotspots {
			if hs.Package != "util" && !strings.Contains(strings.ToLower(hs.File), "util") {
				t.Errorf("package filter 'util' returned symbol from wrong package: %s (package: %s)",
					hs.Name, hs.Package)
			}
		}
	})

	t.Run("nonexistent package returns empty results", func(t *testing.T) {
		// CR-11 restored: When package filter matches nothing, that IS the correct
		// answer. Do NOT fall back to global results — that gives wrong-scope data.
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "nonexistent",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		if output.HotspotCount != 0 {
			t.Errorf("expected 0 results for nonexistent package, got %d", output.HotspotCount)
		}
	})

	t.Run("combined package and kind filter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package": "core",
			"kind":    "function",
			"top":     100,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		for _, hs := range output.Hotspots {
			if hs.Kind != "function" && hs.Kind != "method" {
				t.Errorf("expected function/method kind, got %s for %s", hs.Kind, hs.Name)
			}
			if hs.Package != "core" && !strings.Contains(strings.ToLower(hs.File), "core") {
				t.Errorf("expected core package, got %s for %s", hs.Package, hs.Name)
			}
		}
	})
}

// TestMatchesHotspotKind_AllValues tests IT-07 Bug 2: all extractKindFromQuery()
// output values are handled by matchesHotspotKind().
func TestMatchesHotspotKind_AllValues(t *testing.T) {
	tests := []struct {
		name     string
		kind     ast.SymbolKind
		filter   string
		expected bool
	}{
		// function filter
		{"function matches SymbolKindFunction", ast.SymbolKindFunction, "function", true},
		{"function matches SymbolKindMethod", ast.SymbolKindMethod, "function", true},
		{"function rejects SymbolKindStruct", ast.SymbolKindStruct, "function", false},
		{"function rejects SymbolKindInterface", ast.SymbolKindInterface, "function", false},

		// method filter (alias for function)
		{"method matches SymbolKindFunction", ast.SymbolKindFunction, "method", true},
		{"method matches SymbolKindMethod", ast.SymbolKindMethod, "method", true},
		{"method rejects SymbolKindStruct", ast.SymbolKindStruct, "method", false},

		// type filter
		{"type matches SymbolKindType", ast.SymbolKindType, "type", true},
		{"type matches SymbolKindStruct", ast.SymbolKindStruct, "type", true},
		{"type matches SymbolKindInterface", ast.SymbolKindInterface, "type", true},
		{"type matches SymbolKindClass", ast.SymbolKindClass, "type", true},
		{"type rejects SymbolKindFunction", ast.SymbolKindFunction, "type", false},

		// class filter (alias for type)
		{"class matches SymbolKindClass", ast.SymbolKindClass, "class", true},
		{"class matches SymbolKindStruct", ast.SymbolKindStruct, "class", true},
		{"class rejects SymbolKindFunction", ast.SymbolKindFunction, "class", false},

		// struct filter (alias for type)
		{"struct matches SymbolKindStruct", ast.SymbolKindStruct, "struct", true},
		{"struct matches SymbolKindInterface", ast.SymbolKindInterface, "struct", true},
		{"struct rejects SymbolKindFunction", ast.SymbolKindFunction, "struct", false},

		// interface filter (alias for type)
		{"interface matches SymbolKindInterface", ast.SymbolKindInterface, "interface", true},
		{"interface matches SymbolKindType", ast.SymbolKindType, "interface", true},
		{"interface rejects SymbolKindFunction", ast.SymbolKindFunction, "interface", false},

		// enum filter
		{"enum matches SymbolKindEnum", ast.SymbolKindEnum, "enum", true},
		{"enum rejects SymbolKindFunction", ast.SymbolKindFunction, "enum", false},
		{"enum rejects SymbolKindStruct", ast.SymbolKindStruct, "enum", false},

		// variable/constant filter
		{"variable matches SymbolKindVariable", ast.SymbolKindVariable, "variable", true},
		{"variable matches SymbolKindConstant", ast.SymbolKindConstant, "variable", true},
		{"variable rejects SymbolKindFunction", ast.SymbolKindFunction, "variable", false},
		{"constant matches SymbolKindConstant", ast.SymbolKindConstant, "constant", true},
		{"constant matches SymbolKindVariable", ast.SymbolKindVariable, "constant", true},
		{"constant rejects SymbolKindFunction", ast.SymbolKindFunction, "constant", false},

		// all filter
		{"all matches everything", ast.SymbolKindFunction, "all", true},
		{"all matches struct", ast.SymbolKindStruct, "all", true},
		{"all matches enum", ast.SymbolKindEnum, "all", true},

		// unrecognized defaults to all
		{"unknown filter matches everything", ast.SymbolKindFunction, "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesHotspotKind(tt.kind, tt.filter)
			if got != tt.expected {
				t.Errorf("matchesHotspotKind(%v, %q) = %v, want %v",
					tt.kind, tt.filter, got, tt.expected)
			}
		})
	}
}

// TestFindHotspots_OutputIncludesPackageField verifies that the formatted text
// output includes Package information per hotspot entry.
func TestFindHotspots_OutputIncludesPackageField(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForHotspots(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Verify Package field appears in output text
	if !strings.Contains(result.OutputText, "Package:") {
		t.Error("expected 'Package:' in output text entries")
	}
}

// TestFindHotspots_ParseParamsExpandedKinds verifies that parseParams accepts
// all expanded kind values without defaulting to "all".
func TestFindHotspots_ParseParamsExpandedKinds(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForHotspots(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	validKinds := []string{"function", "method", "type", "class", "struct", "interface", "enum", "variable", "constant", "all"}
	for _, kind := range validKinds {
		t.Run("kind_"+kind+"_accepted", func(t *testing.T) {
			result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
				"kind": kind,
			}})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed for kind=%q: %s", kind, result.Error)
			}
		})
	}

	t.Run("invalid kind defaults to all", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"kind": "invalid_kind",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Should succeed (defaulted to "all")
	})
}

// TestFindHotspots_NilAnalytics verifies graceful handling of nil analytics.
func TestFindHotspots_NilAnalytics(t *testing.T) {
	idx := index.NewSymbolIndex()
	tool := NewFindHotspotsTool(nil, idx)

	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Error("expected failure with nil analytics")
	}
	if !strings.Contains(result.Error, "not initialized") {
		t.Errorf("expected 'not initialized' error, got: %s", result.Error)
	}
}

// TestFindHotspots_DefinitionEnum verifies that the Definition Enum includes
// all expanded kind values (IT-07 Bug 4).
func TestFindHotspots_DefinitionEnum(t *testing.T) {
	idx := index.NewSymbolIndex()
	tool := NewFindHotspotsTool(nil, idx)

	def := tool.Definition()
	kindParam, ok := def.Parameters["kind"]
	if !ok {
		t.Fatal("expected 'kind' parameter in Definition")
	}

	expectedKinds := []string{"function", "method", "type", "class", "struct", "interface", "enum", "variable", "constant", "all"}
	enumSet := make(map[string]bool)
	for _, v := range kindParam.Enum {
		if s, ok := v.(string); ok {
			enumSet[s] = true
		}
	}

	for _, k := range expectedKinds {
		if !enumSet[k] {
			t.Errorf("expected kind %q in Definition Enum, not found", k)
		}
	}

	// Verify package parameter exists
	_, ok = def.Parameters["package"]
	if !ok {
		t.Error("expected 'package' parameter in Definition")
	}
}

// createTestGraphWithDunders builds a graph containing dunder methods and production
// functions to test IT-43d Fix A (dunder filtering).
func createTestGraphWithDunders(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{
			ID: "io/common.py:142:__enter__", Name: "__enter__",
			Kind: ast.SymbolKindMethod, FilePath: "io/common.py",
			StartLine: 142, EndLine: 150, Package: "io", Language: "python",
		},
		{
			ID: "io/common.py:145:__exit__", Name: "__exit__",
			Kind: ast.SymbolKindMethod, FilePath: "io/common.py",
			StartLine: 145, EndLine: 155, Package: "io", Language: "python",
		},
		{
			ID: "io/common.py:50:__init__", Name: "__init__",
			Kind: ast.SymbolKindMethod, FilePath: "io/common.py",
			StartLine: 50, EndLine: 80, Package: "io", Language: "python",
		},
		{
			ID: "io/parsers.py:100:read_csv", Name: "read_csv",
			Kind: ast.SymbolKindFunction, FilePath: "io/parsers.py",
			StartLine: 100, EndLine: 200, Package: "io", Language: "python",
		},
		{
			ID: "io/parsers.py:300:TextFileReader", Name: "TextFileReader",
			Kind: ast.SymbolKindClass, FilePath: "io/parsers.py",
			StartLine: 300, EndLine: 500, Package: "io", Language: "python",
		},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Give __enter__ high InDegree (simulating Strategy 3c inflation)
	for i := 0; i < 20; i++ {
		callerID := fmt.Sprintf("caller_%d.py:1:func_%d", i, i)
		callerSym := &ast.Symbol{
			ID: callerID, Name: fmt.Sprintf("func_%d", i),
			Kind: ast.SymbolKindFunction, FilePath: fmt.Sprintf("caller_%d.py", i),
			StartLine: 1, EndLine: 10, Package: "callers", Language: "python",
		}
		g.AddNode(callerSym)
		g.AddEdge(callerID, "io/common.py:142:__enter__", graph.EdgeTypeCalls, ast.Location{
			FilePath: callerSym.FilePath, StartLine: 5,
		})
		g.AddEdge(callerID, "io/common.py:145:__exit__", graph.EdgeTypeCalls, ast.Location{
			FilePath: callerSym.FilePath, StartLine: 6,
		})
	}

	// Give read_csv moderate InDegree (real production hotspot)
	for i := 0; i < 10; i++ {
		callerID := fmt.Sprintf("user_%d.py:1:user_func_%d", i, i)
		callerSym := &ast.Symbol{
			ID: callerID, Name: fmt.Sprintf("user_func_%d", i),
			Kind: ast.SymbolKindFunction, FilePath: fmt.Sprintf("user_%d.py", i),
			StartLine: 1, EndLine: 10, Package: "users", Language: "python",
		}
		g.AddNode(callerSym)
		g.AddEdge(callerID, "io/parsers.py:100:read_csv", graph.EdgeTypeCalls, ast.Location{
			FilePath: callerSym.FilePath, StartLine: 3,
		})
	}

	// read_csv -> TextFileReader
	g.AddEdge("io/parsers.py:100:read_csv", "io/parsers.py:300:TextFileReader", graph.EdgeTypeCalls, ast.Location{
		FilePath: "io/parsers.py", StartLine: 150,
	})

	g.Freeze()
	return g, idx
}

// TestFindHotspots_FiltersDunders verifies IT-43d Fix A: dunder methods
// (__enter__, __exit__, __init__, etc.) are excluded from hotspot results.
func TestFindHotspots_FiltersDunders(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithDunders(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"top":           100,
		"exclude_tests": false, // Disable test filter to isolate dunder filtering
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindHotspotsOutput)
	if !ok {
		t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
	}

	// Verify no dunder methods in results
	for _, hs := range output.Hotspots {
		if strings.HasPrefix(hs.Name, "__") && strings.HasSuffix(hs.Name, "__") {
			t.Errorf("IT-43d: dunder method %q should be filtered from hotspot results", hs.Name)
		}
	}

	// Verify production functions are still present
	foundReadCSV := false
	for _, hs := range output.Hotspots {
		if hs.Name == "read_csv" {
			foundReadCSV = true
		}
	}
	if !foundReadCSV {
		t.Error("expected read_csv in hotspot results after dunder filtering")
	}
}

// createTestGraphWithTestFiles builds a graph where all high-scoring symbols
// in a specific package are in test files, to test IT-43d Fix B.
func createTestGraphWithTestFiles(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Test file symbol with high connectivity (like "raises" in test_setitem.py)
	testSym := &ast.Symbol{
		ID: "tests/indexing/test_setitem.py:977:raises", Name: "raises",
		Kind: ast.SymbolKindMethod, FilePath: "tests/indexing/test_setitem.py",
		StartLine: 977, EndLine: 990, Package: "indexing", Language: "python",
	}
	g.AddNode(testSym)
	if err := idx.Add(testSym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	// Production file symbol with lower connectivity
	prodSym := &ast.Symbol{
		ID: "core/other.py:10:process", Name: "process",
		Kind: ast.SymbolKindFunction, FilePath: "core/other.py",
		StartLine: 10, EndLine: 30, Package: "core", Language: "python",
	}
	g.AddNode(prodSym)
	if err := idx.Add(prodSym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	// Give test symbol high InDegree
	for i := 0; i < 15; i++ {
		callerID := fmt.Sprintf("tests/indexing/test_case_%d.py:1:test_%d", i, i)
		callerSym := &ast.Symbol{
			ID: callerID, Name: fmt.Sprintf("test_%d", i),
			Kind: ast.SymbolKindFunction, FilePath: fmt.Sprintf("tests/indexing/test_case_%d.py", i),
			StartLine: 1, EndLine: 10, Package: "indexing", Language: "python",
		}
		g.AddNode(callerSym)
		g.AddEdge(callerID, testSym.ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: callerSym.FilePath, StartLine: 5,
		})
	}

	// Give production symbol moderate connectivity
	for i := 0; i < 3; i++ {
		callerID := fmt.Sprintf("core/caller_%d.py:1:caller_%d", i, i)
		callerSym := &ast.Symbol{
			ID: callerID, Name: fmt.Sprintf("caller_%d", i),
			Kind: ast.SymbolKindFunction, FilePath: fmt.Sprintf("core/caller_%d.py", i),
			StartLine: 1, EndLine: 10, Package: "core", Language: "python",
		}
		g.AddNode(callerSym)
		g.AddEdge(callerID, prodSym.ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: callerSym.FilePath, StartLine: 3,
		})
	}

	g.Freeze()
	return g, idx
}

// TestFindHotspots_FiltersTestFiles verifies IT-43d Fix B: when all results
// matching a package filter are from test files, the tool returns empty results
// instead of leaking test infrastructure into hotspot output.
func TestFindHotspots_FiltersTestFiles(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithTestFiles(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindHotspotsTool(analytics, idx)

	t.Run("package filter with only test results returns empty", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"package":       "indexing",
			"exclude_tests": true,
			"top":           10,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		// IT-43d Fix B: should return 0 results, NOT leak test file "raises"
		if output.HotspotCount != 0 {
			for _, hs := range output.Hotspots {
				t.Errorf("IT-43d: test file symbol %q (%s) leaked into hotspot results",
					hs.Name, hs.File)
			}
		}

		// Should have graph result markers for empty results
		if !strings.Contains(result.OutputText, "No symbols with connectivity") ||
			!strings.Contains(result.OutputText, "## GRAPH RESULT") {
			t.Error("expected zero-result graph markers in output")
		}
	})

	t.Run("without package filter returns production symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"exclude_tests": true,
			"top":           10,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindHotspotsOutput)
		if !ok {
			t.Fatalf("Output is not FindHotspotsOutput, got %T", result.Output)
		}

		// Should have production results (process, caller_*)
		for _, hs := range output.Hotspots {
			if strings.Contains(hs.File, "test") {
				t.Errorf("test file symbol %q leaked without package filter", hs.Name)
			}
		}
	})
}
