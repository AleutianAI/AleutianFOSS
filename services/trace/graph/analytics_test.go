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
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/eval"
)

// =============================================================================
// Analytics Test Fixtures
// =============================================================================

// buildHotspotGraph creates a graph where some nodes are more connected.
func buildHotspotGraph(t *testing.T) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test/project")

	// Hub function - called by many
	hub := &ast.Symbol{
		ID:       "pkg/hub.go:10:Hub",
		Name:     "Hub",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/hub.go",
		Exported: true,
	}

	// Multiple callers
	caller1 := &ast.Symbol{
		ID:       "pkg/caller.go:10:Caller1",
		Name:     "Caller1",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/caller.go",
	}
	caller2 := &ast.Symbol{
		ID:       "pkg/caller.go:20:Caller2",
		Name:     "Caller2",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/caller.go",
	}
	caller3 := &ast.Symbol{
		ID:       "pkg/caller.go:30:Caller3",
		Name:     "Caller3",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/caller.go",
	}

	// Isolated function
	isolated := &ast.Symbol{
		ID:       "pkg/isolated.go:10:Isolated",
		Name:     "Isolated",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/isolated.go",
	}

	mustAddNode(t, g, hub)
	mustAddNode(t, g, caller1)
	mustAddNode(t, g, caller2)
	mustAddNode(t, g, caller3)
	mustAddNode(t, g, isolated)

	// All callers call hub
	mustAddEdge(t, g, caller1.ID, hub.ID, EdgeTypeCalls)
	mustAddEdge(t, g, caller2.ID, hub.ID, EdgeTypeCalls)
	mustAddEdge(t, g, caller3.ID, hub.ID, EdgeTypeCalls)

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}

	return hg
}

// buildDeadCodeGraph creates a graph with some unreferenced symbols.
func buildDeadCodeGraph(t *testing.T) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test/project")

	// Entry point
	main := &ast.Symbol{
		ID:       "cmd/main.go:10:main",
		Name:     "main",
		Kind:     ast.SymbolKindFunction,
		Package:  "cmd",
		FilePath: "cmd/main.go",
	}

	// Called function
	used := &ast.Symbol{
		ID:       "pkg/used.go:10:UsedFunc",
		Name:     "UsedFunc",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/used.go",
		Exported: true,
	}

	// Unused function (dead code)
	unused := &ast.Symbol{
		ID:       "pkg/unused.go:10:UnusedFunc",
		Name:     "UnusedFunc",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/unused.go",
		Exported: true,
	}

	// Test function (should not be flagged)
	testFunc := &ast.Symbol{
		ID:       "pkg/used_test.go:10:TestUsed",
		Name:     "TestUsed",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg",
		FilePath: "pkg/used_test.go",
	}

	mustAddNode(t, g, main)
	mustAddNode(t, g, used)
	mustAddNode(t, g, unused)
	mustAddNode(t, g, testFunc)

	// main calls used
	mustAddEdge(t, g, main.ID, used.ID, EdgeTypeCalls)

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}

	return hg
}

// buildCouplingGraph creates a graph with known coupling characteristics.
func buildCouplingGraph(t *testing.T) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test/project")

	// Stable package (many dependents, no dependencies)
	stableInterface := &ast.Symbol{
		ID:       "pkg/stable/interface.go:10:Core",
		Name:     "Core",
		Kind:     ast.SymbolKindInterface,
		Package:  "pkg/stable",
		FilePath: "pkg/stable/interface.go",
		Exported: true,
	}

	// Unstable package (no dependents, many dependencies)
	unstableFunc := &ast.Symbol{
		ID:       "pkg/unstable/impl.go:10:Impl",
		Name:     "Impl",
		Kind:     ast.SymbolKindStruct,
		Package:  "pkg/unstable",
		FilePath: "pkg/unstable/impl.go",
		Exported: true,
	}

	// Middle package (some of both)
	middleFunc := &ast.Symbol{
		ID:       "pkg/middle/service.go:10:Service",
		Name:     "Service",
		Kind:     ast.SymbolKindFunction,
		Package:  "pkg/middle",
		FilePath: "pkg/middle/service.go",
		Exported: true,
	}

	mustAddNode(t, g, stableInterface)
	mustAddNode(t, g, unstableFunc)
	mustAddNode(t, g, middleFunc)

	// unstable depends on stable
	mustAddEdge(t, g, unstableFunc.ID, stableInterface.ID, EdgeTypeImplements)
	// middle depends on stable
	mustAddEdge(t, g, middleFunc.ID, stableInterface.ID, EdgeTypeReferences)
	// unstable depends on middle
	mustAddEdge(t, g, unstableFunc.ID, middleFunc.ID, EdgeTypeCalls)

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}

	return hg
}

// =============================================================================
// GraphAnalytics Constructor Tests
// =============================================================================

func TestNewGraphAnalytics(t *testing.T) {
	t.Run("valid graph", func(t *testing.T) {
		hg := buildTestGraph(t)
		analytics := NewGraphAnalytics(hg)
		if analytics == nil {
			t.Fatal("expected non-nil analytics")
		}
	})

	t.Run("nil graph", func(t *testing.T) {
		analytics := NewGraphAnalytics(nil)
		if analytics != nil {
			t.Error("expected nil for nil graph")
		}
	})
}

// =============================================================================
// HotSpots Tests
// =============================================================================

func TestHotSpots(t *testing.T) {
	hg := buildHotspotGraph(t)
	analytics := NewGraphAnalytics(hg)

	t.Run("returns top N", func(t *testing.T) {
		hotspots := analytics.HotSpots(2)
		if len(hotspots) != 2 {
			t.Errorf("expected 2 hotspots, got %d", len(hotspots))
		}
	})

	t.Run("hub is highest", func(t *testing.T) {
		hotspots := analytics.HotSpots(5)
		if len(hotspots) == 0 {
			t.Fatal("expected at least 1 hotspot")
		}

		// Hub has 3 incoming edges, so should have highest score
		top := hotspots[0]
		if top.Node.Symbol.Name != "Hub" {
			t.Errorf("expected Hub as top hotspot, got %s", top.Node.Symbol.Name)
		}
		if top.InDegree != 3 {
			t.Errorf("expected InDegree=3, got %d", top.InDegree)
		}
	})

	t.Run("zero top returns empty", func(t *testing.T) {
		hotspots := analytics.HotSpots(0)
		if len(hotspots) != 0 {
			t.Errorf("expected empty result, got %d", len(hotspots))
		}
	})

	t.Run("negative top returns empty", func(t *testing.T) {
		hotspots := analytics.HotSpots(-1)
		if len(hotspots) != 0 {
			t.Errorf("expected empty result, got %d", len(hotspots))
		}
	})
}

func TestHotSpotsWithCRS(t *testing.T) {
	hg := buildHotspotGraph(t)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	hotspots, traceStep := analytics.HotSpotsWithCRS(ctx, 3)

	if len(hotspots) != 3 {
		t.Errorf("expected 3 hotspots, got %d", len(hotspots))
	}

	if traceStep.Action != "analytics_hotspots" {
		t.Errorf("expected Action='analytics_hotspots', got '%s'", traceStep.Action)
	}
	if traceStep.Metadata["requested"] != "3" {
		t.Errorf("expected requested='3', got '%s'", traceStep.Metadata["requested"])
	}
}

// =============================================================================
// DeadCode Tests
// =============================================================================

func TestDeadCode(t *testing.T) {
	hg := buildDeadCodeGraph(t)
	analytics := NewGraphAnalytics(hg)

	deadCode := analytics.DeadCode()

	t.Run("finds unused function", func(t *testing.T) {
		found := false
		for _, dc := range deadCode {
			if dc.Node.Symbol.Name == "UnusedFunc" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected UnusedFunc to be flagged as dead code")
		}
	})

	t.Run("excludes entry points", func(t *testing.T) {
		for _, dc := range deadCode {
			if dc.Node.Symbol.Name == "main" {
				t.Error("main should not be flagged as dead code")
			}
			if dc.Node.Symbol.Name == "TestUsed" {
				t.Error("test functions should not be flagged as dead code")
			}
		}
	})

	t.Run("excludes used functions", func(t *testing.T) {
		for _, dc := range deadCode {
			if dc.Node.Symbol.Name == "UsedFunc" {
				t.Error("UsedFunc has callers, should not be flagged")
			}
		}
	})
}

func TestDeadCodeWithCRS(t *testing.T) {
	hg := buildDeadCodeGraph(t)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	deadCode, traceStep := analytics.DeadCodeWithCRS(ctx)

	if traceStep.Action != "analytics_dead_code" {
		t.Errorf("expected Action='analytics_dead_code', got '%s'", traceStep.Action)
	}
	if traceStep.Metadata["dead_code_count"] == "" {
		t.Error("expected dead_code_count in metadata")
	}

	// Verify we found at least the unused function
	if len(deadCode) == 0 {
		t.Error("expected at least one dead code item")
	}
}

func TestIsEntryPoint(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"main", true},
		{"init", true},
		{"TestFoo", true},
		{"BenchmarkBar", true},
		{"FuzzBaz", true},
		{"ExampleQux", true},
		{"ServeHTTP", false}, // Only method with this name is entry point
		{"RegularFunc", false},
		{"Test", false},      // Exactly "Test" is not an entry point
		{"Benchmark", false}, // Exactly "Benchmark" is not an entry point
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sym := &ast.Symbol{
				ID:   "test.go:1:" + tc.name,
				Name: tc.name,
				Kind: ast.SymbolKindFunction,
			}
			result := isEntryPoint(sym)
			if result != tc.expected {
				t.Errorf("isEntryPoint(%s) = %v, expected %v", tc.name, result, tc.expected)
			}
		})
	}

	t.Run("ServeHTTP method", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:   "test.go:1:ServeHTTP",
			Name: "ServeHTTP",
			Kind: ast.SymbolKindMethod,
		}
		if !isEntryPoint(sym) {
			t.Error("ServeHTTP method should be entry point")
		}
	})

	t.Run("nil symbol", func(t *testing.T) {
		if isEntryPoint(nil) {
			t.Error("nil symbol should not be entry point")
		}
	})

	// IT-08 H-1: Python entry point tests
	t.Run("Python test_function", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "test_helpers.py:1:test_parse_input",
			Name:     "test_parse_input",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
		}
		if !isEntryPoint(sym) {
			t.Error("Python test_ function should be entry point")
		}
	})

	t.Run("Python __main__", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "app.py:1:__main__",
			Name:     "__main__",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
		}
		if !isEntryPoint(sym) {
			t.Error("Python __main__ should be entry point")
		}
	})

	t.Run("Python __main__.py file", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "__main__.py:1:run",
			Name:     "run",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
			FilePath: "src/myapp/__main__.py",
		}
		if !isEntryPoint(sym) {
			t.Error("function in __main__.py should be entry point")
		}
	})

	t.Run("Python setUp/tearDown", func(t *testing.T) {
		for _, name := range []string{"setUp", "tearDown", "setUpClass", "tearDownClass"} {
			sym := &ast.Symbol{
				ID:       "test.py:1:" + name,
				Name:     name,
				Kind:     ast.SymbolKindMethod,
				Language: "python",
			}
			if !isEntryPoint(sym) {
				t.Errorf("Python %s should be entry point", name)
			}
		}
	})

	t.Run("Python Flask route decorator", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "views.py:1:get_users",
			Name:     "get_users",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"app.route"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("Flask route handler should be entry point")
		}
	})

	t.Run("Python FastAPI decorator", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "api.py:1:create_item",
			Name:     "create_item",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"app.post"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("FastAPI POST handler should be entry point")
		}
	})

	t.Run("Python click command", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "cli.py:1:deploy",
			Name:     "deploy",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"click.command"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("Click command should be entry point")
		}
	})

	t.Run("Python pytest fixture", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "conftest.py:1:db_session",
			Name:     "db_session",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"pytest.fixture"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("pytest fixture should be entry point")
		}
	})

	t.Run("Python TestCase class", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "test_views.py:1:TestUserAPI",
			Name:     "TestUserAPI",
			Kind:     ast.SymbolKindClass,
			Language: "python",
			Metadata: &ast.SymbolMetadata{
				Extends: "unittest.TestCase",
			},
		}
		if !isEntryPoint(sym) {
			t.Error("unittest TestCase class should be entry point")
		}
	})

	t.Run("Python regular function not entry point", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "helpers.py:1:parse_config",
			Name:     "parse_config",
			Kind:     ast.SymbolKindFunction,
			Language: "python",
		}
		if isEntryPoint(sym) {
			t.Error("regular Python function should not be entry point")
		}
	})

	// IT-08 H-1: JavaScript/TypeScript entry point tests
	t.Run("JS test framework functions", func(t *testing.T) {
		for _, name := range []string{"it", "test", "describe", "beforeEach", "afterEach", "beforeAll", "afterAll"} {
			sym := &ast.Symbol{
				ID:       "test.js:1:" + name,
				Name:     name,
				Kind:     ast.SymbolKindFunction,
				Language: "javascript",
			}
			if !isEntryPoint(sym) {
				t.Errorf("JS %s should be entry point", name)
			}
		}
	})

	t.Run("TS NestJS Controller decorator", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "users.controller.ts:1:UsersController",
			Name:     "UsersController",
			Kind:     ast.SymbolKindClass,
			Language: "typescript",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"Controller"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("NestJS Controller should be entry point")
		}
	})

	t.Run("TS NestJS route decorators", func(t *testing.T) {
		for _, dec := range []string{"Get", "Post", "Put", "Delete", "Patch"} {
			sym := &ast.Symbol{
				ID:       "users.controller.ts:1:find",
				Name:     "find",
				Kind:     ast.SymbolKindMethod,
				Language: "typescript",
				Metadata: &ast.SymbolMetadata{
					Decorators: []string{dec},
				},
			}
			if !isEntryPoint(sym) {
				t.Errorf("NestJS @%s handler should be entry point", dec)
			}
		}
	})

	t.Run("TS NestJS Injectable", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "users.service.ts:1:UsersService",
			Name:     "UsersService",
			Kind:     ast.SymbolKindClass,
			Language: "typescript",
			Metadata: &ast.SymbolMetadata{
				Decorators: []string{"Injectable"},
			},
		}
		if !isEntryPoint(sym) {
			t.Error("NestJS Injectable should be entry point")
		}
	})

	t.Run("JS exported function", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "utils.js:1:formatDate",
			Name:     "formatDate",
			Kind:     ast.SymbolKindFunction,
			Language: "javascript",
			Exported: true,
		}
		if !isEntryPoint(sym) {
			t.Error("exported JS function should be entry point")
		}
	})

	t.Run("JS non-exported internal function not entry point", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:       "utils.js:1:_helper",
			Name:     "_helper",
			Kind:     ast.SymbolKindFunction,
			Language: "javascript",
			Exported: false,
		}
		if isEntryPoint(sym) {
			t.Error("non-exported JS internal function should not be entry point")
		}
	})

	t.Run("TS Next.js special exports", func(t *testing.T) {
		for _, name := range []string{"getServerSideProps", "getStaticProps", "getStaticPaths"} {
			sym := &ast.Symbol{
				ID:       "page.tsx:1:" + name,
				Name:     name,
				Kind:     ast.SymbolKindFunction,
				Language: "typescript",
			}
			if !isEntryPoint(sym) {
				t.Errorf("Next.js %s should be entry point", name)
			}
		}
	})
}

// =============================================================================
// CyclicDependencies Tests
// =============================================================================

func TestCyclicDependencies(t *testing.T) {
	t.Run("finds cycle", func(t *testing.T) {
		hg := buildCyclicGraph(t)
		analytics := NewGraphAnalytics(hg)

		cycles := analytics.CyclicDependencies()

		if len(cycles) != 1 {
			t.Errorf("expected 1 cycle, got %d", len(cycles))
		}

		if len(cycles) > 0 {
			cycle := cycles[0]
			if cycle.Length != 3 {
				t.Errorf("expected cycle length=3, got %d", cycle.Length)
			}
		}
	})

	t.Run("no cycles in acyclic graph", func(t *testing.T) {
		hg := buildTestGraph(t)
		analytics := NewGraphAnalytics(hg)

		cycles := analytics.CyclicDependencies()

		if len(cycles) != 0 {
			t.Errorf("expected 0 cycles, got %d", len(cycles))
		}
	})
}

func TestCyclicDependenciesWithCRS(t *testing.T) {
	hg := buildCyclicGraph(t)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	cycles, traceStep := analytics.CyclicDependenciesWithCRS(ctx)

	if len(cycles) != 1 {
		t.Errorf("expected 1 cycle, got %d", len(cycles))
	}

	if traceStep.Action != "analytics_cycles" {
		t.Errorf("expected Action='analytics_cycles', got '%s'", traceStep.Action)
	}
	if traceStep.Metadata["cycles_found"] != "1" {
		t.Errorf("expected cycles_found='1', got '%s'", traceStep.Metadata["cycles_found"])
	}
}

// =============================================================================
// PackageCoupling Tests
// =============================================================================

func TestPackageCoupling(t *testing.T) {
	hg := buildCouplingGraph(t)
	analytics := NewGraphAnalytics(hg)

	metrics := analytics.PackageCoupling()

	t.Run("has all packages", func(t *testing.T) {
		if len(metrics) != 3 {
			t.Errorf("expected 3 packages, got %d", len(metrics))
		}
	})

	t.Run("stable package metrics", func(t *testing.T) {
		stable, ok := metrics["pkg/stable"]
		if !ok {
			t.Fatal("expected metrics for pkg/stable")
		}

		// Stable has 2 dependents (middle and unstable), 0 dependencies
		if stable.Afferent != 2 {
			t.Errorf("expected Afferent=2, got %d", stable.Afferent)
		}
		if stable.Efferent != 0 {
			t.Errorf("expected Efferent=0, got %d", stable.Efferent)
		}
		if stable.Instability != 0.0 {
			t.Errorf("expected Instability=0.0, got %f", stable.Instability)
		}
	})

	t.Run("unstable package metrics", func(t *testing.T) {
		unstable, ok := metrics["pkg/unstable"]
		if !ok {
			t.Fatal("expected metrics for pkg/unstable")
		}

		// Unstable has 0 dependents, 2 dependencies (stable and middle)
		if unstable.Afferent != 0 {
			t.Errorf("expected Afferent=0, got %d", unstable.Afferent)
		}
		if unstable.Efferent != 2 {
			t.Errorf("expected Efferent=2, got %d", unstable.Efferent)
		}
		if unstable.Instability != 1.0 {
			t.Errorf("expected Instability=1.0, got %f", unstable.Instability)
		}
	})

	t.Run("abstractness calculation", func(t *testing.T) {
		stable, ok := metrics["pkg/stable"]
		if !ok {
			t.Fatal("expected metrics for pkg/stable")
		}

		// stable only has an interface (abstract)
		if stable.AbstractTypes != 1 {
			t.Errorf("expected AbstractTypes=1, got %d", stable.AbstractTypes)
		}
		if stable.Abstractness != 1.0 {
			t.Errorf("expected Abstractness=1.0, got %f", stable.Abstractness)
		}
	})
}

func TestPackageCouplingWithCRS(t *testing.T) {
	hg := buildCouplingGraph(t)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	metrics, traceStep := analytics.PackageCouplingWithCRS(ctx)

	if len(metrics) != 3 {
		t.Errorf("expected 3 packages, got %d", len(metrics))
	}

	if traceStep.Action != "analytics_coupling" {
		t.Errorf("expected Action='analytics_coupling', got '%s'", traceStep.Action)
	}
	if traceStep.Metadata["packages_analyzed"] != "3" {
		t.Errorf("expected packages_analyzed='3', got '%s'", traceStep.Metadata["packages_analyzed"])
	}
}

func TestGetCouplingForPackage(t *testing.T) {
	hg := buildCouplingGraph(t)
	analytics := NewGraphAnalytics(hg)

	t.Run("existing package", func(t *testing.T) {
		metrics := analytics.GetCouplingForPackage("pkg/stable")
		if metrics == nil {
			t.Fatal("expected metrics for pkg/stable")
		}
		if metrics.Package != "pkg/stable" {
			t.Errorf("expected Package='pkg/stable', got '%s'", metrics.Package)
		}
	})

	t.Run("non-existent package", func(t *testing.T) {
		metrics := analytics.GetCouplingForPackage("pkg/nonexistent")
		// Should return metrics with zero values, not nil
		if metrics == nil {
			t.Fatal("expected non-nil metrics")
		}
		if metrics.Afferent != 0 || metrics.Efferent != 0 {
			t.Error("expected zero coupling for non-existent package")
		}
	})
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkHotSpots(b *testing.B) {
	// Build a larger graph for benchmarking
	g := NewGraph("/bench")
	for i := 0; i < 1000; i++ {
		sym := &ast.Symbol{
			ID:       "pkg/bench.go:" + itoa(i) + ":Func" + itoa(i),
			Name:     "Func" + itoa(i),
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg",
			FilePath: "pkg/bench.go",
		}
		g.AddNode(sym)
	}

	// Add random edges
	for i := 0; i < 500; i++ {
		from := "pkg/bench.go:" + itoa(i) + ":Func" + itoa(i)
		to := "pkg/bench.go:" + itoa((i+1)%1000) + ":Func" + itoa((i+1)%1000)
		g.AddEdge(from, to, EdgeTypeCalls, ast.Location{})
	}

	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analytics.HotSpots(10)
	}
}

func BenchmarkCyclicDependencies(b *testing.B) {
	// Build a graph without cycles for worst-case Tarjan's
	g := NewGraph("/bench")
	for i := 0; i < 1000; i++ {
		sym := &ast.Symbol{
			ID:       "pkg/bench.go:" + itoa(i) + ":Func" + itoa(i),
			Name:     "Func" + itoa(i),
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg",
			FilePath: "pkg/bench.go",
		}
		g.AddNode(sym)
	}

	// Linear chain (no cycles)
	for i := 0; i < 999; i++ {
		from := "pkg/bench.go:" + itoa(i) + ":Func" + itoa(i)
		to := "pkg/bench.go:" + itoa(i+1) + ":Func" + itoa(i+1)
		g.AddEdge(from, to, EdgeTypeCalls, ast.Location{})
	}

	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analytics.CyclicDependencies()
	}
}

// =============================================================================
// CRS Integration Benchmarks
// =============================================================================

// mockCRS is a minimal CRS implementation for benchmarking overhead.
type mockCRS struct {
	sessionID string
}

// CRS interface methods (minimal no-op implementations)
func (m *mockCRS) Name() string           { return "mock_crs" }
func (m *mockCRS) Snapshot() crs.Snapshot { return nil }
func (m *mockCRS) Apply(context.Context, crs.Delta) (crs.ApplyMetrics, error) {
	return crs.ApplyMetrics{}, nil
}
func (m *mockCRS) Generation() int64                                         { return 0 }
func (m *mockCRS) Checkpoint(context.Context) (crs.Checkpoint, error)        { return crs.Checkpoint{}, nil }
func (m *mockCRS) Restore(context.Context, crs.Checkpoint) error             { return nil }
func (m *mockCRS) RecordStep(ctx context.Context, step crs.StepRecord) error { return step.Validate() }
func (m *mockCRS) GetLastStep(string) *crs.StepRecord                        { return nil }
func (m *mockCRS) CountToolExecutions(string, string) int                    { return 0 }
func (m *mockCRS) GetStepsByActor(string, crs.Actor) []crs.StepRecord        { return nil }
func (m *mockCRS) GetStepsByOutcome(string, crs.Outcome) []crs.StepRecord    { return nil }
func (m *mockCRS) ClearStepHistory(string)                                   {}
func (m *mockCRS) UpdateProofNumber(context.Context, crs.ProofUpdate) error  { return nil }
func (m *mockCRS) GetProofStatus(string) (crs.ProofNumber, bool)             { return crs.ProofNumber{}, false }
func (m *mockCRS) CheckCircuitBreaker(string, string) crs.CircuitBreakerResult {
	return crs.CircuitBreakerResult{}
}
func (m *mockCRS) PropagateDisproof(context.Context, string) int      { return 0 }
func (m *mockCRS) HealthCheck(context.Context) error                  { return nil }
func (m *mockCRS) Properties() []eval.Property                        { return nil }
func (m *mockCRS) Metrics() []eval.MetricDefinition                   { return nil }
func (m *mockCRS) AddClause(context.Context, *crs.Clause) error       { return nil }
func (m *mockCRS) CheckDecisionAllowed(string, string) (bool, string) { return true, "" }
func (m *mockCRS) GarbageCollectClauses() int                         { return 0 }
func (m *mockCRS) SetSessionID(id string)                             { m.sessionID = id }
func (m *mockCRS) ApplyWithSource(context.Context, crs.Delta, string, map[string]string) (crs.ApplyMetrics, error) {
	return crs.ApplyMetrics{}, nil
}
func (m *mockCRS) DeltaHistory() crs.DeltaHistoryView                           { return nil }
func (m *mockCRS) Close()                                                       {}
func (m *mockCRS) SetGraphProvider(crs.GraphQuery)                              {}
func (m *mockCRS) InvalidateGraphCache()                                        {}
func (m *mockCRS) GetAnalyticsHistory() []*crs.AnalyticsRecord                  { return nil }
func (m *mockCRS) GetLastAnalytics(crs.AnalyticsQueryType) *crs.AnalyticsRecord { return nil }
func (m *mockCRS) HasRunAnalytics(crs.AnalyticsQueryType) bool                  { return false }
func (m *mockCRS) GetStepHistory(string) []crs.StepRecord                       { return nil }

// BenchmarkLCAWithCRS_Baseline measures HLD.LCA without CRS.
func BenchmarkLCAWithCRS_Baseline(b *testing.B) {
	ctx := context.Background()
	_, hld := buildBenchHLDTree(b)

	// No CRS - just raw HLD
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = hld.LCA(ctx, "node_10", "node_20")
	}
}

// BenchmarkLCAWithCRS_NoCRS measures LCAWithCRS when CRS not configured.
func BenchmarkLCAWithCRS_NoCRS(b *testing.B) {
	ctx := context.Background()
	g, _ := buildBenchHLDTree(b)
	analytics := NewGraphAnalytics(g)

	// No CRS - should fallthrough
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.LCAWithCRS(ctx, "node_10", "node_20")
	}
}

// BenchmarkLCAWithCRS_NoSession measures LCAWithCRS when CRS configured but no session.
func BenchmarkLCAWithCRS_NoSession(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	// CRS configured but no session - should fallthrough
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.LCAWithCRS(ctx, "node_10", "node_20")
	}
}

// BenchmarkLCAWithCRS_WithRecording measures LCAWithCRS with active CRS session.
func BenchmarkLCAWithCRS_WithRecording(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	// Start session
	_ = analytics.StartSession(ctx, "bench-session")

	// Full CRS recording overhead
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.LCAWithCRS(ctx, "node_10", "node_20")
	}

	b.StopTimer()
	_ = analytics.EndSession(ctx)
}

// BenchmarkLCAWithCRS_ConcurrentReads measures concurrent access with CRS.
func BenchmarkLCAWithCRS_ConcurrentReads(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	// Start session
	_ = analytics.StartSession(ctx, "bench-session")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = analytics.LCAWithCRS(ctx, "node_10", "node_20")
		}
	})

	b.StopTimer()
	_ = analytics.EndSession(ctx)
}

// BenchmarkDistanceWithCRS_WithRecording measures DistanceWithCRS overhead.
func BenchmarkDistanceWithCRS_WithRecording(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	_ = analytics.StartSession(ctx, "bench-session")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DistanceWithCRS(ctx, "node_10", "node_20")
	}

	b.StopTimer()
	_ = analytics.EndSession(ctx)
}

// BenchmarkDecomposePathWithCRS_WithRecording measures DecomposePathWithCRS overhead.
func BenchmarkDecomposePathWithCRS_WithRecording(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	_ = analytics.StartSession(ctx, "bench-session")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DecomposePathWithCRS(ctx, "node_10", "node_20")
	}

	b.StopTimer()
	_ = analytics.EndSession(ctx)
}

// BenchmarkSessionManagement measures session lifecycle overhead.
func BenchmarkSessionManagement(b *testing.B) {
	ctx := context.Background()
	g, hld := buildBenchHLDTree(b)

	mockCRS := &mockCRS{}
	analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analytics.StartSession(ctx, "bench-session")
		_ = analytics.EndSession(ctx)
	}
}

// buildBenchHLDTree creates a balanced tree for HLD benchmarking.
func buildBenchHLDTree(b *testing.B) (*HierarchicalGraph, *HLDecomposition) {
	b.Helper()

	g := NewGraph("/bench")

	// Build a balanced tree with 100 nodes
	for i := 0; i < 100; i++ {
		sym := &ast.Symbol{
			ID:       "bench.go:" + itoa(i) + ":node_" + itoa(i),
			Name:     "node_" + itoa(i),
			Kind:     ast.SymbolKindFunction,
			Package:  "bench",
			FilePath: "bench.go",
		}
		g.AddNode(sym)
	}

	// Create tree structure (binary tree)
	for i := 0; i < 50; i++ {
		parent := "bench.go:" + itoa(i) + ":node_" + itoa(i)
		left := "bench.go:" + itoa(2*i+1) + ":node_" + itoa(2*i+1)
		right := "bench.go:" + itoa(2*i+2) + ":node_" + itoa(2*i+2)

		if 2*i+1 < 100 {
			g.AddEdge(parent, left, EdgeTypeCalls, ast.Location{})
		}
		if 2*i+2 < 100 {
			g.AddEdge(parent, right, EdgeTypeCalls, ast.Location{})
		}
	}

	g.Freeze()
	hg, _ := WrapGraph(g)

	// Build HLD
	ctx := context.Background()
	hld, err := BuildHLDIterative(ctx, hg.Graph, "bench.go:0:node_0")
	if err != nil {
		b.Fatalf("failed to build HLD: %v", err)
	}

	return hg, hld
}

// =============================================================================
// CRS Integration Tests
// =============================================================================

// mockCRSRecorder extends mockCRS to record steps for verification.
type mockCRSRecorder struct {
	mockCRS
	steps []crs.StepRecord
	mu    sync.Mutex
}

func (m *mockCRSRecorder) RecordStep(ctx context.Context, step crs.StepRecord) error {
	if err := step.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.steps = append(m.steps, step)
	m.mu.Unlock()
	return nil
}

func (m *mockCRSRecorder) GetSteps() []crs.StepRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]crs.StepRecord, len(m.steps))
	copy(result, m.steps)
	return result
}

func (m *mockCRSRecorder) ClearSteps() {
	m.mu.Lock()
	m.steps = nil
	m.mu.Unlock()
}

// =============================================================================
// Session Management Tests
// =============================================================================

func TestGraphAnalytics_StartSession(t *testing.T) {
	t.Run("valid session", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		err := analytics.StartSession(context.Background(), "test-session")
		if err != nil {
			t.Fatalf("StartSession failed: %v", err)
		}

		if mockCRS.sessionID != "test-session" {
			t.Errorf("expected session ID 'test-session', got %q", mockCRS.sessionID)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		err := analytics.StartSession(nil, "test-session")
		if err == nil {
			t.Error("expected error for nil context")
		}
	})

	t.Run("empty session ID", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		err := analytics.StartSession(context.Background(), "")
		if err == nil {
			t.Error("expected error for empty session ID")
		}
	})

	t.Run("no CRS configured", func(t *testing.T) {
		g, _, _ := buildTestHLDTree(t)
		analytics := NewGraphAnalytics(g)

		err := analytics.StartSession(context.Background(), "test-session")
		if err == nil {
			t.Error("expected error when CRS not configured")
		}
	})
}

func TestGraphAnalytics_EndSession(t *testing.T) {
	t.Run("ends active session", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")
		err := analytics.EndSession(context.Background())
		if err != nil {
			t.Fatalf("EndSession failed: %v", err)
		}
	})

	t.Run("no active session", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		err := analytics.EndSession(context.Background())
		if err == nil {
			t.Error("expected error when no active session")
		}
	})

	t.Run("nil context", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")
		err := analytics.EndSession(nil)
		if err == nil {
			t.Error("expected error for nil context")
		}
	})
}

func TestGraphAnalytics_SessionStepCounter(t *testing.T) {
	t.Run("step counter increments", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		// Execute multiple queries
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])
		_, _ = analytics.DistanceWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 3 {
			t.Fatalf("expected 3 steps, got %d", len(steps))
		}

		// Verify step numbers are sequential
		for i, step := range steps {
			expectedStep := i + 1
			if step.StepNumber != expectedStep {
				t.Errorf("step %d: expected StepNumber %d, got %d", i, expectedStep, step.StepNumber)
			}
		}
	})

	t.Run("step counter resets on new session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// First session
		_ = analytics.StartSession(context.Background(), "session-1")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])
		_ = analytics.EndSession(context.Background())

		mockCRS.ClearSteps()

		// Second session - counter should reset
		_ = analytics.StartSession(context.Background(), "session-2")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}
		if steps[0].StepNumber != 1 {
			t.Errorf("expected StepNumber 1 after reset, got %d", steps[0].StepNumber)
		}
	})
}

func TestGraphAnalytics_AutoSession(t *testing.T) {
	t.Run("auto-session disabled by default", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Call CRS-aware query without starting session - should not record
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		steps := mockCRS.GetSteps()
		if len(steps) != 0 {
			t.Errorf("expected 0 steps (no auto-session), got %d", len(steps))
		}
	})

	t.Run("auto-session enabled", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{AutoSession: true}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Call CRS-aware query without manually starting session - should auto-start
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step (auto-session), got %d", len(steps))
		}

		// Verify session ID was auto-generated (format: auto_<timestamp>_<seq>)
		if steps[0].SessionID == "" {
			t.Error("expected non-empty session ID from auto-session")
		}
		if len(steps[0].SessionID) < 5 || steps[0].SessionID[:5] != "auto_" {
			t.Errorf("expected session ID to start with 'auto_', got %s", steps[0].SessionID)
		}
	})

	t.Run("auto-session only starts once", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{AutoSession: true}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Execute multiple queries - session should only start once
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])
		_, _ = analytics.DistanceWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 3 {
			t.Fatalf("expected 3 steps, got %d", len(steps))
		}

		// All steps should have same session ID
		sessionID := steps[0].SessionID
		for i, step := range steps {
			if step.SessionID != sessionID {
				t.Errorf("step %d: expected session ID %s, got %s", i, sessionID, step.SessionID)
			}
		}
	})

	t.Run("manual session overrides auto-session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{AutoSession: true}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Manually start session before any queries
		_ = analytics.StartSession(context.Background(), "manual-session")

		// Call query - should use manual session, not auto-start
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(steps))
		}

		// Verify session ID is the manual one
		if steps[0].SessionID != "manual-session" {
			t.Errorf("expected session ID 'manual-session', got %s", steps[0].SessionID)
		}
	})

	t.Run("auto-session with nil CRS returns error", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		opts := &GraphAnalyticsOptions{AutoSession: true}
		analytics := NewGraphAnalyticsWithCRS(g, hld, nil, opts)

		// Call query - should fail with CRS not configured error
		_, err := analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err == nil {
			t.Error("expected error when auto-session enabled with nil CRS")
		}
	})

	t.Run("auto-session respects EndSession", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{AutoSession: true}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Execute query - auto-starts session
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		firstSessionID := mockCRS.GetSteps()[0].SessionID

		// End session
		_ = analytics.EndSession(context.Background())

		// Execute another query - should auto-start NEW session
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(steps))
		}

		// Second query should have different session ID
		secondSessionID := steps[1].SessionID
		if secondSessionID == firstSessionID {
			t.Errorf("expected new session ID after EndSession, both are %s", firstSessionID)
		}
	})
}

func TestGraphAnalytics_SessionTimeout(t *testing.T) {
	t.Run("timeout disabled by default", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{AutoSession: true, SessionTimeout: 0}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Execute query - auto-starts session
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		firstSessionID := mockCRS.GetSteps()[0].SessionID

		// Wait longer than any reasonable timeout (but timeout is disabled)
		time.Sleep(10 * time.Millisecond)

		// Execute another query - should use SAME session (no timeout)
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(steps))
		}

		// Both queries should have same session ID (no timeout occurred)
		secondSessionID := steps[1].SessionID
		if secondSessionID != firstSessionID {
			t.Errorf("expected same session ID (no timeout), got %s and %s", firstSessionID, secondSessionID)
		}
	})

	t.Run("timeout expires inactive session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{
			AutoSession:    true,
			SessionTimeout: 50 * time.Millisecond,
		}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Execute query - auto-starts session
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		firstSessionID := mockCRS.GetSteps()[0].SessionID

		// Wait for timeout to expire
		time.Sleep(100 * time.Millisecond)

		// Execute another query - should start NEW session (old one timed out)
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(steps))
		}

		// Second query should have different session ID (timeout occurred)
		secondSessionID := steps[1].SessionID
		if secondSessionID == firstSessionID {
			t.Errorf("expected new session ID after timeout, both are %s", firstSessionID)
		}
	})

	t.Run("activity resets timeout", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{
			AutoSession:    true,
			SessionTimeout: 100 * time.Millisecond,
		}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Execute query - auto-starts session
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		firstSessionID := mockCRS.GetSteps()[0].SessionID

		// Execute queries periodically within timeout window
		for i := 0; i < 3; i++ {
			time.Sleep(60 * time.Millisecond) // Less than timeout
			_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 4 {
			t.Fatalf("expected 4 steps, got %d", len(steps))
		}

		// All queries should have same session ID (activity kept resetting timeout)
		for i, step := range steps {
			if step.SessionID != firstSessionID {
				t.Errorf("step %d: expected session ID %s, got %s", i, firstSessionID, step.SessionID)
			}
		}
	})

	t.Run("timeout with manual session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{
			AutoSession:    true,
			SessionTimeout: 50 * time.Millisecond,
		}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Manually start session
		_ = analytics.StartSession(context.Background(), "manual-session")

		// Execute query
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		// Wait for timeout to expire
		time.Sleep(100 * time.Millisecond)

		// Execute another query - should start NEW session (manual session timed out)
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(steps))
		}

		// First should be manual session
		if steps[0].SessionID != "manual-session" {
			t.Errorf("expected manual-session, got %s", steps[0].SessionID)
		}

		// Second should be auto-generated (manual session timed out)
		if steps[1].SessionID == "manual-session" {
			t.Error("expected new auto-generated session after timeout")
		}
		if len(steps[1].SessionID) < 5 || steps[1].SessionID[:5] != "auto_" {
			t.Errorf("expected auto-generated session ID, got %s", steps[1].SessionID)
		}
	})

	t.Run("timeout without auto-session does nothing", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		opts := &GraphAnalyticsOptions{
			AutoSession:    false, // Auto-session disabled
			SessionTimeout: 50 * time.Millisecond,
		}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, opts)

		// Manually start session
		_ = analytics.StartSession(context.Background(), "manual-session")

		// Execute query
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		// Wait for timeout to expire
		time.Sleep(100 * time.Millisecond)

		// Execute another query - should NOT record (session timed out, but auto-session disabled)
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		// Only first query should be recorded (second query has no session)
		if len(steps) != 1 {
			t.Fatalf("expected 1 step (timeout without auto-restart), got %d", len(steps))
		}
	})
}

func TestGraphAnalytics_SessionStacking(t *testing.T) {
	t.Run("push and pop nested session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Start outer session
		_ = analytics.StartSession(context.Background(), "outer-session")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		// Push nested session
		_ = analytics.PushSession(context.Background(), "inner-session")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		// Pop back to outer session
		_ = analytics.PopSession(context.Background())
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 3 {
			t.Fatalf("expected 3 steps, got %d", len(steps))
		}

		// Verify session IDs
		if steps[0].SessionID != "outer-session" {
			t.Errorf("step 0: expected outer-session, got %s", steps[0].SessionID)
		}
		if steps[1].SessionID != "inner-session" {
			t.Errorf("step 1: expected inner-session, got %s", steps[1].SessionID)
		}
		if steps[2].SessionID != "outer-session" {
			t.Errorf("step 2: expected outer-session (restored), got %s", steps[2].SessionID)
		}

		// Verify step numbers reset in nested session
		if steps[0].StepNumber != 1 {
			t.Errorf("step 0: expected StepNumber 1, got %d", steps[0].StepNumber)
		}
		if steps[1].StepNumber != 1 {
			t.Errorf("step 1: expected StepNumber 1 (nested session reset), got %d", steps[1].StepNumber)
		}
		if steps[2].StepNumber != 2 {
			t.Errorf("step 2: expected StepNumber 2 (outer session continued), got %d", steps[2].StepNumber)
		}
	})

	t.Run("multiple nested levels", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Level 1
		_ = analytics.StartSession(context.Background(), "level-1")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		// Level 2
		_ = analytics.PushSession(context.Background(), "level-2")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		// Level 3
		_ = analytics.PushSession(context.Background(), "level-3")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		// Pop back to level 2
		_ = analytics.PopSession(context.Background())
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		// Pop back to level 1
		_ = analytics.PopSession(context.Background())
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		steps := mockCRS.GetSteps()
		if len(steps) != 5 {
			t.Fatalf("expected 5 steps, got %d", len(steps))
		}

		// Verify session progression
		expectedSessions := []string{"level-1", "level-2", "level-3", "level-2", "level-1"}
		for i, expected := range expectedSessions {
			if steps[i].SessionID != expected {
				t.Errorf("step %d: expected %s, got %s", i, expected, steps[i].SessionID)
			}
		}
	})

	t.Run("push without active session returns error", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Try to push without starting a session first
		err := analytics.PushSession(context.Background(), "nested-session")
		if err == nil {
			t.Error("expected error when pushing without active session")
		}
	})

	t.Run("pop without stack returns error", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Start session but don't push anything
		_ = analytics.StartSession(context.Background(), "session")

		// Try to pop - should fail (stack is empty)
		err := analytics.PopSession(context.Background())
		if err == nil {
			t.Error("expected error when popping empty stack")
		}
	})

	t.Run("nested session isolation", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Start outer session and execute multiple queries
		_ = analytics.StartSession(context.Background(), "outer")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_1"], nodes["node_2"])

		// Push nested session
		_ = analytics.PushSession(context.Background(), "inner")
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_2"])

		// Pop back to outer
		_ = analytics.PopSession(context.Background())

		// Continue outer session - step counter should pick up from where it left off
		_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])

		steps := mockCRS.GetSteps()
		if len(steps) != 4 {
			t.Fatalf("expected 4 steps, got %d", len(steps))
		}

		// Outer session steps should be 1, 2, then 3 after pop (not reset)
		// Inner session step should be 1
		if steps[0].StepNumber != 1 || steps[1].StepNumber != 2 {
			t.Errorf("outer session initial steps incorrect: %d, %d", steps[0].StepNumber, steps[1].StepNumber)
		}
		if steps[2].StepNumber != 1 {
			t.Errorf("inner session step should be 1, got %d", steps[2].StepNumber)
		}
		if steps[3].StepNumber != 3 {
			t.Errorf("outer session restored step should be 3, got %d", steps[3].StepNumber)
		}
	})

	t.Run("push with nil context returns error", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "session")

		err := analytics.PushSession(nil, "nested")
		if err == nil {
			t.Error("expected error for nil context")
		}
	})

	t.Run("pop with nil context returns error", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "session")
		_ = analytics.PushSession(context.Background(), "nested")

		err := analytics.PopSession(nil)
		if err == nil {
			t.Error("expected error for nil context")
		}
	})

	t.Run("push with empty session ID returns error", func(t *testing.T) {
		g, hld, _ := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "session")

		err := analytics.PushSession(context.Background(), "")
		if err == nil {
			t.Error("expected error for empty session ID")
		}
	})
}

// =============================================================================
// StepRecord Recording Tests
// =============================================================================

func TestGraphAnalytics_LCAWithCRS_Recording(t *testing.T) {
	t.Run("records step on success", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		lca, err := analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("LCAWithCRS failed: %v", err)
		}
		if lca == "" {
			t.Error("expected non-empty LCA result")
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step recorded, got %d", len(steps))
		}

		step := steps[0]
		if step.SessionID != "test-session" {
			t.Errorf("expected SessionID 'test-session', got %q", step.SessionID)
		}
		if step.Tool != "LCA" {
			t.Errorf("expected Tool 'LCA', got %q", step.Tool)
		}
		if step.Actor != crs.ActorSystem {
			t.Errorf("expected Actor %v, got %v", crs.ActorSystem, step.Actor)
		}
		if step.Decision != crs.DecisionExecuteTool {
			t.Errorf("expected Decision %v, got %v", crs.DecisionExecuteTool, step.Decision)
		}
		if step.Outcome != crs.OutcomeSuccess {
			t.Errorf("expected Outcome %v, got %v", crs.OutcomeSuccess, step.Outcome)
		}
		if step.ErrorCategory != crs.ErrorCategoryNone {
			t.Errorf("expected ErrorCategory %v, got %v", crs.ErrorCategoryNone, step.ErrorCategory)
		}
		if step.DurationMs < 0 {
			t.Errorf("expected non-negative duration, got %d", step.DurationMs)
		}
		if step.ToolParams == nil {
			t.Error("expected ToolParams to be set")
		} else {
			if step.ToolParams.Target != nodes["node_0"] {
				t.Errorf("expected Target 'node_0', got %q", step.ToolParams.Target)
			}
			if step.ToolParams.Query != nodes["node_1"] {
				t.Errorf("expected Query 'node_1', got %q", step.ToolParams.Query)
			}
		}
	})

	t.Run("records step on failure", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		// Query non-existent node
		_, err := analytics.LCAWithCRS(context.Background(), "nonexistent", nodes["node_1"])
		if err == nil {
			t.Error("expected error for non-existent node")
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step recorded, got %d", len(steps))
		}

		step := steps[0]
		if step.Outcome != crs.OutcomeFailure {
			t.Errorf("expected Outcome %v, got %v", crs.OutcomeFailure, step.Outcome)
		}
		if step.ErrorCategory == crs.ErrorCategoryNone {
			t.Error("expected non-None error category")
		}
		if step.ErrorMessage == "" {
			t.Error("expected error message to be set")
		}
	})
}

func TestGraphAnalytics_DistanceWithCRS_Recording(t *testing.T) {
	t.Run("records step with correct tool name", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		dist, err := analytics.DistanceWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("DistanceWithCRS failed: %v", err)
		}
		if dist < 0 {
			t.Errorf("expected non-negative distance, got %d", dist)
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step recorded, got %d", len(steps))
		}

		if steps[0].Tool != "Distance" {
			t.Errorf("expected Tool 'Distance', got %q", steps[0].Tool)
		}
	})
}

func TestGraphAnalytics_DecomposePathWithCRS_Recording(t *testing.T) {
	t.Run("records step with segment count", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		segments, err := analytics.DecomposePathWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("DecomposePathWithCRS failed: %v", err)
		}
		if len(segments) == 0 {
			t.Error("expected non-empty segments")
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step recorded, got %d", len(steps))
		}

		step := steps[0]
		if step.Tool != "DecomposePath" {
			t.Errorf("expected Tool 'DecomposePath', got %q", step.Tool)
		}
		// ResultSummary should contain segment count
		if step.ResultSummary == "" {
			t.Error("expected non-empty result summary")
		}
	})
}

func TestGraphAnalytics_BatchLCAWithCRS_Recording(t *testing.T) {
	t.Run("records single step for batch", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		pairs := [][2]string{
			{nodes["node_0"], nodes["node_1"]},
			{nodes["node_1"], nodes["node_2"]},
			{nodes["node_0"], nodes["node_2"]},
		}

		results, errs, err := analytics.BatchLCAWithCRS(context.Background(), pairs)
		if err != nil {
			t.Fatalf("BatchLCAWithCRS failed: %v", err)
		}
		if len(results) != len(pairs) {
			t.Errorf("expected %d results, got %d", len(pairs), len(results))
		}
		if len(errs) != len(pairs) {
			t.Errorf("expected %d error entries, got %d", len(pairs), len(errs))
		}

		steps := mockCRS.GetSteps()
		if len(steps) != 1 {
			t.Fatalf("expected 1 step for batch operation, got %d", len(steps))
		}

		step := steps[0]
		if step.Tool != "BatchLCA" {
			t.Errorf("expected Tool 'BatchLCA', got %q", step.Tool)
		}
		if step.ToolParams == nil || step.ToolParams.Limit != 3 {
			t.Errorf("expected ToolParams.Limit = 3, got %v", step.ToolParams)
		}
	})
}

// =============================================================================
// No-CRS Fallthrough Tests
// =============================================================================

func TestGraphAnalytics_LCAWithCRS_NoCRSFallthrough(t *testing.T) {
	t.Run("falls through when CRS not configured", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		analytics := NewGraphAnalyticsWithCRS(g, hld, nil, nil)

		// Should work even without CRS
		lca, err := analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("LCAWithCRS failed: %v", err)
		}
		if lca == "" {
			t.Error("expected non-empty LCA result")
		}
	})

	t.Run("falls through when no active session", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		// Don't start session - should fallthrough
		lca, err := analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("LCAWithCRS failed: %v", err)
		}
		if lca == "" {
			t.Error("expected non-empty LCA result")
		}

		// Should not record any steps
		steps := mockCRS.GetSteps()
		if len(steps) != 0 {
			t.Errorf("expected 0 steps without session, got %d", len(steps))
		}
	})

	t.Run("returns same result as raw HLD", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		analytics := NewGraphAnalyticsWithCRS(g, hld, nil, nil)

		// Get result from raw HLD
		rawResult, err := hld.LCA(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("HLD.LCA failed: %v", err)
		}

		// Get result from analytics wrapper
		wrapperResult, err := analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
		if err != nil {
			t.Fatalf("LCAWithCRS failed: %v", err)
		}

		if rawResult != wrapperResult {
			t.Errorf("expected same result, got raw=%q wrapper=%q", rawResult, wrapperResult)
		}
	})
}

// =============================================================================
// Error Classification Tests
// =============================================================================

func TestClassifyHLDError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected crs.ErrorCategory
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: crs.ErrorCategoryNone,
		},
		{
			name:     "node not found",
			err:      ErrNodeNotFound,
			expected: crs.ErrorCategoryToolNotFound,
		},
		{
			name:     "HLD not initialized",
			err:      ErrHLDNotInitialized,
			expected: crs.ErrorCategoryInternal,
		},
		{
			name:     "nodes in different trees",
			err:      ErrNodesInDifferentTrees,
			expected: crs.ErrorCategoryInvalidParams,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: crs.ErrorCategoryTimeout,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: crs.ErrorCategoryInternal,
		},
		{
			name:     "unknown error",
			err:      errors.New("unknown error"),
			expected: crs.ErrorCategoryInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyHLDError(tt.err)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestGraphAnalytics_ConcurrentRecording(t *testing.T) {
	t.Run("concurrent queries record correctly", func(t *testing.T) {
		g, hld, nodes := buildTestHLDTree(t)
		mockCRS := &mockCRSRecorder{}
		analytics := NewGraphAnalyticsWithCRS(g, hld, mockCRS, nil)

		_ = analytics.StartSession(context.Background(), "test-session")

		const numGoroutines = 10
		const queriesPerGoroutine = 10

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < queriesPerGoroutine; j++ {
					_, _ = analytics.LCAWithCRS(context.Background(), nodes["node_0"], nodes["node_1"])
				}
			}()
		}

		wg.Wait()

		steps := mockCRS.GetSteps()
		expectedSteps := numGoroutines * queriesPerGoroutine
		if len(steps) != expectedSteps {
			t.Errorf("expected %d steps, got %d", expectedSteps, len(steps))
		}

		// Verify all step numbers are unique and in range
		stepNumbers := make(map[int]bool)
		for _, step := range steps {
			if stepNumbers[step.StepNumber] {
				t.Errorf("duplicate step number: %d", step.StepNumber)
			}
			stepNumbers[step.StepNumber] = true

			if step.StepNumber < 1 || step.StepNumber > expectedSteps {
				t.Errorf("step number %d out of range [1, %d]", step.StepNumber, expectedSteps)
			}
		}
	})
}

// =============================================================================
// Test Helpers
// =============================================================================

// buildTestHLDTree creates a small tree for testing.
// Returns the graph, HLD, and a map of node IDs for easy reference.
func buildTestHLDTree(t *testing.T) (*HierarchicalGraph, *HLDecomposition, map[string]string) {
	t.Helper()

	g := NewGraph("/test")

	// Build a simple tree: node_0 -> node_1 -> node_2
	nodeIDs := make(map[string]string)
	for i := 0; i < 3; i++ {
		id := "test.go:" + itoa(i) + ":node_" + itoa(i)
		sym := &ast.Symbol{
			ID:       id,
			Name:     "node_" + itoa(i),
			Kind:     ast.SymbolKindFunction,
			Package:  "test",
			FilePath: "test.go",
		}
		g.AddNode(sym)
		nodeIDs["node_"+itoa(i)] = id
	}

	g.AddEdge(nodeIDs["node_0"], nodeIDs["node_1"], EdgeTypeCalls, ast.Location{})
	g.AddEdge(nodeIDs["node_1"], nodeIDs["node_2"], EdgeTypeCalls, ast.Location{})

	g.Freeze()
	hg, _ := WrapGraph(g)

	ctx := context.Background()
	hld, err := BuildHLDIterative(ctx, hg.Graph, nodeIDs["node_0"])
	if err != nil {
		t.Fatalf("failed to build HLD: %v", err)
	}

	return hg, hld, nodeIDs
}
