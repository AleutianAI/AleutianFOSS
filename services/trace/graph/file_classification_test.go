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
	"os"
	"path/filepath"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// Test Helpers
// =============================================================================

// buildClassificationGraph creates a graph with production files and test files.
// Production files call each other; test files call production but are not called back.
func buildClassificationGraph(t *testing.T) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test")

	// Production file: pkg/server.go
	serverInit := &ast.Symbol{
		ID: "pkg/server.go:1:init", Name: "init", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/server.go", StartLine: 1, Language: "go",
	}
	serverHandle := &ast.Symbol{
		ID: "pkg/server.go:10:Handle", Name: "Handle", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/server.go", StartLine: 10, Language: "go",
	}

	// Production file: pkg/handler.go
	handlerProcess := &ast.Symbol{
		ID: "pkg/handler.go:1:Process", Name: "Process", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/handler.go", StartLine: 1, Language: "go",
	}
	handlerValidate := &ast.Symbol{
		ID: "pkg/handler.go:20:Validate", Name: "Validate", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/handler.go", StartLine: 20, Language: "go",
	}

	// Test file: pkg/server_test.go
	testServerInit := &ast.Symbol{
		ID: "pkg/server_test.go:1:TestInit", Name: "TestInit", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/server_test.go", StartLine: 1, Language: "go",
	}
	testServerHandle := &ast.Symbol{
		ID: "pkg/server_test.go:20:TestHandle", Name: "TestHandle", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/server_test.go", StartLine: 20, Language: "go",
	}

	// Example file: examples/demo.go
	exampleMain := &ast.Symbol{
		ID: "examples/demo.go:1:main", Name: "main", Kind: ast.SymbolKindFunction,
		FilePath: "examples/demo.go", StartLine: 1, Language: "go",
	}
	exampleRun := &ast.Symbol{
		ID: "examples/demo.go:10:run", Name: "run", Kind: ast.SymbolKindFunction,
		FilePath: "examples/demo.go", StartLine: 10, Language: "go",
	}

	mustAddNode(t, g, serverInit)
	mustAddNode(t, g, serverHandle)
	mustAddNode(t, g, handlerProcess)
	mustAddNode(t, g, handlerValidate)
	mustAddNode(t, g, testServerInit)
	mustAddNode(t, g, testServerHandle)
	mustAddNode(t, g, exampleMain)
	mustAddNode(t, g, exampleRun)

	// Production edges: server.go <-> handler.go (bidirectional)
	mustAddEdge(t, g, serverHandle.ID, handlerProcess.ID, EdgeTypeCalls)
	mustAddEdge(t, g, handlerProcess.ID, handlerValidate.ID, EdgeTypeCalls)
	mustAddEdge(t, g, handlerValidate.ID, serverInit.ID, EdgeTypeCalls)

	// Test edges: test calls production, NOT the reverse
	mustAddEdge(t, g, testServerInit.ID, serverInit.ID, EdgeTypeCalls)
	mustAddEdge(t, g, testServerHandle.ID, serverHandle.ID, EdgeTypeCalls)
	mustAddEdge(t, g, testServerHandle.ID, handlerProcess.ID, EdgeTypeCalls)

	// Example edges: example calls production, NOT the reverse
	mustAddEdge(t, g, exampleMain.ID, serverHandle.ID, EdgeTypeCalls)
	mustAddEdge(t, g, exampleRun.ID, handlerProcess.ID, EdgeTypeCalls)

	g.Freeze()
	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	return hg
}

// =============================================================================
// ClassifyFiles Guard Clause Tests
// =============================================================================

func TestClassifyFiles_GuardClauses(t *testing.T) {
	t.Run("nil graph returns error", func(t *testing.T) {
		fc, err := ClassifyFiles(nil, FileClassificationOptions{})
		if err == nil {
			t.Fatal("expected error for nil graph")
		}
		if fc != nil {
			t.Fatal("expected nil result for nil graph")
		}
	})

	t.Run("unfrozen graph returns error", func(t *testing.T) {
		g := NewGraph("/test")
		// Not frozen, so WrapGraph would fail, but we test ClassifyFiles guard directly
		// Create a HierarchicalGraph manually for testing
		hg := &HierarchicalGraph{Graph: g}
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err == nil {
			t.Fatal("expected error for unfrozen graph")
		}
		if fc != nil {
			t.Fatal("expected nil result for unfrozen graph")
		}
	})

	t.Run("empty frozen graph succeeds", func(t *testing.T) {
		g := NewGraph("/test")
		g.Freeze()
		hg, err := WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph failed: %v", err)
		}

		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if fc == nil {
			t.Fatal("expected non-nil result")
		}
		stats := fc.Stats()
		if stats.TotalFiles != 0 {
			t.Errorf("expected 0 total files, got %d", stats.TotalFiles)
		}
	})
}

// =============================================================================
// ClassifyFiles Consumption Ratio Tests
// =============================================================================

func TestClassifyFiles_ConsumptionRatio(t *testing.T) {
	t.Run("isolated file classified as production", func(t *testing.T) {
		g := NewGraph("/test")
		sym := &ast.Symbol{
			ID: "pkg/isolated.go:1:helper", Name: "helper", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/isolated.go", StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, sym)
		g.Freeze()
		hg, _ := WrapGraph(g)

		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !fc.IsProduction("pkg/isolated.go") {
			t.Error("isolated file should be classified as production")
		}
		stats := fc.Stats()
		if stats.IsolatedFiles != 1 {
			t.Errorf("expected 1 isolated file, got %d", stats.IsolatedFiles)
		}
	})

	t.Run("pure consumer classified as non-production", func(t *testing.T) {
		hg := buildClassificationGraph(t)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Test file: only makes outgoing calls, no incoming (ratio ~0)
		if fc.IsProduction("pkg/server_test.go") {
			t.Error("test file should be classified as non-production")
		}

		// Example file: only makes outgoing calls, no incoming
		if fc.IsProduction("examples/demo.go") {
			t.Error("example file should be classified as non-production")
		}
	})

	t.Run("production files classified as production", func(t *testing.T) {
		hg := buildClassificationGraph(t)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !fc.IsProduction("pkg/server.go") {
			t.Error("server.go should be classified as production")
		}
		if !fc.IsProduction("pkg/handler.go") {
			t.Error("handler.go should be classified as production")
		}
	})

	t.Run("mixed ratio file with mostly outgoing classified correctly", func(t *testing.T) {
		g := NewGraph("/test")

		// Production file with many incoming edges
		prod := &ast.Symbol{
			ID: "core/engine.go:1:Run", Name: "Run", Kind: ast.SymbolKindFunction,
			FilePath: "core/engine.go", StartLine: 1, Language: "go",
		}
		// File with ratio = 1 in / (1 in + 20 out) ≈ 0.048 → non-production
		consumer := &ast.Symbol{
			ID: "bench/perf.go:1:BenchmarkRun", Name: "BenchmarkRun", Kind: ast.SymbolKindFunction,
			FilePath: "bench/perf.go", StartLine: 1, Language: "go",
		}

		mustAddNode(t, g, prod)
		mustAddNode(t, g, consumer)

		// 1 edge in: prod -> consumer
		mustAddEdge(t, g, prod.ID, consumer.ID, EdgeTypeCalls)

		// Add many consumer -> prod edges to make ratio very low
		for i := 0; i < 20; i++ {
			callerSym := &ast.Symbol{
				ID:   ast.GenerateID("bench/perf.go", 10+i, "helper"),
				Name: "helper", Kind: ast.SymbolKindFunction,
				FilePath: "bench/perf.go", StartLine: 10 + i, Language: "go",
			}
			mustAddNode(t, g, callerSym)
			mustAddEdge(t, g, callerSym.ID, prod.ID, EdgeTypeCalls)
		}

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// bench/perf.go: 1 edge in, 20 edges out → ratio = 1/21 ≈ 0.048 → non-production
		if fc.IsProduction("bench/perf.go") {
			t.Error("file with ratio < 0.05 should be non-production")
		}
	})
}

// =============================================================================
// ClassifyFiles Entry Point Reinforcement Tests
// =============================================================================

func TestClassifyFiles_EntryPointReinforcement(t *testing.T) {
	t.Run("likely consumer with >50% test entry points is non-production", func(t *testing.T) {
		g := NewGraph("/test")

		prod := &ast.Symbol{
			ID: "core/lib.go:1:Foo", Name: "Foo", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// File with ratio in [0.05, 0.15) range and >50% test entry points
		testFunc1 := &ast.Symbol{
			ID: "mixed/helper_test.go:1:TestA", Name: "TestA", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/helper_test.go", StartLine: 1, Language: "go",
		}
		testFunc2 := &ast.Symbol{
			ID: "mixed/helper_test.go:10:TestB", Name: "TestB", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/helper_test.go", StartLine: 10, Language: "go",
		}
		normalFunc := &ast.Symbol{
			ID: "mixed/helper_test.go:20:setup", Name: "setup", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/helper_test.go", StartLine: 20, Language: "go",
		}

		mustAddNode(t, g, prod)
		mustAddNode(t, g, testFunc1)
		mustAddNode(t, g, testFunc2)
		mustAddNode(t, g, normalFunc)

		// Create ratio in likely-consumer zone (0.05-0.15)
		// 1 edge in, 10 edges out → ratio = 1/11 ≈ 0.09
		mustAddEdge(t, g, prod.ID, testFunc1.ID, EdgeTypeCalls)
		for i := 0; i < 10; i++ {
			extraSym := &ast.Symbol{
				ID:   ast.GenerateID("core/lib.go", 10+i, "bar"),
				Name: "bar", Kind: ast.SymbolKindFunction,
				FilePath: "core/lib.go", StartLine: 10 + i, Language: "go",
			}
			mustAddNode(t, g, extraSym)
			mustAddEdge(t, g, normalFunc.ID, extraSym.ID, EdgeTypeCalls)
		}

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 2/3 symbols are Test* entry points → >50% → non-production
		if fc.IsProduction("mixed/helper_test.go") {
			t.Error("file with >50% test entry points should be non-production")
		}
	})

	t.Run("likely consumer with <50% test entry points is production", func(t *testing.T) {
		g := NewGraph("/test")

		prod := &ast.Symbol{
			ID: "core/lib.go:1:Foo", Name: "Foo", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// File with only 1/3 test entry points
		testFunc := &ast.Symbol{
			ID: "mixed/utils.go:1:TestHelper", Name: "TestHelper", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/utils.go", StartLine: 1, Language: "go",
		}
		normalFunc1 := &ast.Symbol{
			ID: "mixed/utils.go:10:Compute", Name: "Compute", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/utils.go", StartLine: 10, Language: "go",
		}
		normalFunc2 := &ast.Symbol{
			ID: "mixed/utils.go:20:Format", Name: "Format", Kind: ast.SymbolKindFunction,
			FilePath: "mixed/utils.go", StartLine: 20, Language: "go",
		}

		mustAddNode(t, g, prod)
		mustAddNode(t, g, testFunc)
		mustAddNode(t, g, normalFunc1)
		mustAddNode(t, g, normalFunc2)

		// Create ratio in likely-consumer zone
		mustAddEdge(t, g, prod.ID, normalFunc1.ID, EdgeTypeCalls)
		for i := 0; i < 10; i++ {
			extraSym := &ast.Symbol{
				ID:   ast.GenerateID("core/lib.go", 10+i, "baz"),
				Name: "baz", Kind: ast.SymbolKindFunction,
				FilePath: "core/lib.go", StartLine: 10 + i, Language: "go",
			}
			mustAddNode(t, g, extraSym)
			mustAddEdge(t, g, normalFunc2.ID, extraSym.ID, EdgeTypeCalls)
		}

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 1/3 symbols are test entry points → <50% → production (benefit of doubt)
		if !fc.IsProduction("mixed/utils.go") {
			t.Error("file with <50% test entry points should be production")
		}
	})

	t.Run("init and ServeHTTP are not test entry points", func(t *testing.T) {
		g := NewGraph("/test")

		prod := &ast.Symbol{
			ID: "core/lib.go:1:Foo", Name: "Foo", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// File with init and ServeHTTP — NOT test entry points
		initFunc := &ast.Symbol{
			ID: "pkg/server.go:1:init", Name: "init", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/server.go", StartLine: 1, Language: "go",
		}
		serveFunc := &ast.Symbol{
			ID: "pkg/server.go:10:ServeHTTP", Name: "ServeHTTP", Kind: ast.SymbolKindMethod,
			FilePath: "pkg/server.go", StartLine: 10, Language: "go",
		}

		mustAddNode(t, g, prod)
		mustAddNode(t, g, initFunc)
		mustAddNode(t, g, serveFunc)

		// Create ratio in likely-consumer zone
		mustAddEdge(t, g, prod.ID, initFunc.ID, EdgeTypeCalls)
		for i := 0; i < 10; i++ {
			extraSym := &ast.Symbol{
				ID:   ast.GenerateID("core/lib.go", 10+i, "baz"),
				Name: "baz", Kind: ast.SymbolKindFunction,
				FilePath: "core/lib.go", StartLine: 10 + i, Language: "go",
			}
			mustAddNode(t, g, extraSym)
			mustAddEdge(t, g, serveFunc.ID, extraSym.ID, EdgeTypeCalls)
		}

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 0/2 are test entry points → production
		if !fc.IsProduction("pkg/server.go") {
			t.Error("file with init/ServeHTTP should not be classified as test")
		}
	})
}

// =============================================================================
// ClassifyFiles Config Override Tests
// =============================================================================

func TestClassifyFiles_ConfigOverrides(t *testing.T) {
	t.Run("exclude forces non-production", func(t *testing.T) {
		// Create a temp dir with trace.config.yaml
		tmpDir := t.TempDir()
		configContent := `exclude_from_analysis:
  - "vendor/"
  - "generated/"
`
		if err := os.WriteFile(filepath.Join(tmpDir, "trace.config.yaml"), []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		g := NewGraph(tmpDir)
		// Production file that would normally be classified as production
		sym := &ast.Symbol{
			ID: "vendor/lib.go:1:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
			FilePath: "vendor/lib.go", StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, sym)
		g.Freeze()
		hg, _ := WrapGraph(g)

		fc, err := ClassifyFiles(hg, FileClassificationOptions{ProjectRoot: tmpDir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fc.IsProduction("vendor/lib.go") {
			t.Error("excluded file should be non-production")
		}
	})

	t.Run("include forces production", func(t *testing.T) {
		tmpDir := t.TempDir()
		configContent := `include_override:
  - "integration/core/"
`
		if err := os.WriteFile(filepath.Join(tmpDir, "trace.config.yaml"), []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		g := NewGraph(tmpDir)
		// Create a consumer file that graph would classify as non-production
		prod := &ast.Symbol{
			ID: "core/main.go:1:Run", Name: "Run", Kind: ast.SymbolKindFunction,
			FilePath: "core/main.go", StartLine: 1, Language: "go",
		}
		consumer := &ast.Symbol{
			ID: "integration/core/test.go:1:TestRun", Name: "TestRun", Kind: ast.SymbolKindFunction,
			FilePath: "integration/core/test.go", StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, prod)
		mustAddNode(t, g, consumer)
		mustAddEdge(t, g, consumer.ID, prod.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)

		fc, err := ClassifyFiles(hg, FileClassificationOptions{ProjectRoot: tmpDir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// include_override forces production even though ratio = 0
		if !fc.IsProduction("integration/core/test.go") {
			t.Error("included file should be forced to production")
		}
	})

	t.Run("missing config file is not an error", func(t *testing.T) {
		tmpDir := t.TempDir()
		// No config file
		g := NewGraph(tmpDir)
		g.Freeze()
		hg, _ := WrapGraph(g)

		fc, err := ClassifyFiles(hg, FileClassificationOptions{ProjectRoot: tmpDir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fc == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// =============================================================================
// IsProductionFile Fallback Tests
// =============================================================================

func TestIsProductionFile_Fallback(t *testing.T) {
	t.Run("nil classification falls back to heuristic", func(t *testing.T) {
		g := NewGraph("/test")
		g.Freeze()
		hg, _ := WrapGraph(g)

		// No classification set — should fall back to heuristic
		if hg.IsProductionFile("pkg/server_test.go") {
			t.Error("test file should be non-production via heuristic fallback")
		}
		if !hg.IsProductionFile("pkg/server.go") {
			t.Error("source file should be production via heuristic fallback")
		}
		if hg.IsProductionFile("docs/README.md") {
			t.Error("doc file should be non-production via heuristic fallback")
		}
	})

	t.Run("classification overrides heuristic", func(t *testing.T) {
		hg := buildClassificationGraph(t)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		hg.SetFileClassification(fc)

		// Now IsProductionFile delegates to classification, not heuristic
		if !hg.IsProductionFile("pkg/server.go") {
			t.Error("server.go should be production")
		}
		if hg.IsProductionFile("pkg/server_test.go") {
			t.Error("test file should be non-production")
		}
	})

	t.Run("unknown files treated as production", func(t *testing.T) {
		g := NewGraph("/test")
		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, _ := ClassifyFiles(hg, FileClassificationOptions{})
		hg.SetFileClassification(fc)

		// File not in classification at all
		if !hg.IsProductionFile("unknown/file.go") {
			t.Error("unknown file should be treated as production")
		}
	})
}

// =============================================================================
// isTestEntryPoint Tests
// =============================================================================

func TestIsTestEntryPoint(t *testing.T) {
	tests := []struct {
		name     string
		sym      *ast.Symbol
		expected bool
	}{
		// Go test entry points
		{
			name:     "Go Test function",
			sym:      &ast.Symbol{Name: "TestParseConfig", Language: "go"},
			expected: true,
		},
		{
			name:     "Go Benchmark function",
			sym:      &ast.Symbol{Name: "BenchmarkParse", Language: "go"},
			expected: true,
		},
		{
			name:     "Go Example function",
			sym:      &ast.Symbol{Name: "ExampleParse", Language: "go"},
			expected: true,
		},
		{
			name:     "Go Fuzz function",
			sym:      &ast.Symbol{Name: "FuzzParse", Language: "go"},
			expected: true,
		},
		{
			name:     "Go init is NOT test entry point",
			sym:      &ast.Symbol{Name: "init", Language: "go"},
			expected: false,
		},
		{
			name:     "Go main is NOT test entry point",
			sym:      &ast.Symbol{Name: "main", Language: "go"},
			expected: false,
		},
		{
			name:     "Go ServeHTTP is NOT test entry point",
			sym:      &ast.Symbol{Name: "ServeHTTP", Kind: ast.SymbolKindMethod, Language: "go"},
			expected: false,
		},
		{
			name:     "Go normal function is NOT test entry point",
			sym:      &ast.Symbol{Name: "parseConfig", Language: "go"},
			expected: false,
		},

		// Python test entry points
		{
			name:     "Python test_ function",
			sym:      &ast.Symbol{Name: "test_parse", Language: "python"},
			expected: true,
		},
		{
			name:     "Python setUp",
			sym:      &ast.Symbol{Name: "setUp", Language: "python"},
			expected: true,
		},
		{
			name:     "Python tearDown",
			sym:      &ast.Symbol{Name: "tearDown", Language: "python"},
			expected: true,
		},
		{
			name:     "Python setUpClass",
			sym:      &ast.Symbol{Name: "setUpClass", Language: "python"},
			expected: true,
		},
		{
			name:     "Python tearDownClass",
			sym:      &ast.Symbol{Name: "tearDownClass", Language: "python"},
			expected: true,
		},
		{
			name: "Python TestCase subclass",
			sym: &ast.Symbol{
				Name: "TestParser", Kind: ast.SymbolKindClass, Language: "python",
				Metadata: &ast.SymbolMetadata{Extends: "unittest.TestCase"},
			},
			expected: true,
		},
		{
			name: "Python pytest fixture",
			sym: &ast.Symbol{
				Name: "client", Language: "python",
				Metadata: &ast.SymbolMetadata{Decorators: []string{"pytest.fixture"}},
			},
			expected: true,
		},
		{
			name:     "Python normal function",
			sym:      &ast.Symbol{Name: "parse_data", Language: "python"},
			expected: false,
		},

		// JS/TS test entry points
		{
			name:     "JS describe",
			sym:      &ast.Symbol{Name: "describe", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS it",
			sym:      &ast.Symbol{Name: "it", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS test",
			sym:      &ast.Symbol{Name: "test", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS beforeEach",
			sym:      &ast.Symbol{Name: "beforeEach", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS afterEach",
			sym:      &ast.Symbol{Name: "afterEach", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS beforeAll",
			sym:      &ast.Symbol{Name: "beforeAll", Language: "javascript"},
			expected: true,
		},
		{
			name:     "JS afterAll",
			sym:      &ast.Symbol{Name: "afterAll", Language: "javascript"},
			expected: true,
		},
		{
			name:     "TS before",
			sym:      &ast.Symbol{Name: "before", Language: "typescript"},
			expected: true,
		},
		{
			name:     "TS after",
			sym:      &ast.Symbol{Name: "after", Language: "typescript"},
			expected: true,
		},
		{
			name:     "JS normal function",
			sym:      &ast.Symbol{Name: "handleRequest", Language: "javascript"},
			expected: false,
		},

		// Nil symbol
		{
			name:     "nil symbol",
			sym:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTestEntryPoint(tt.sym)
			if result != tt.expected {
				t.Errorf("isTestEntryPoint(%v) = %v, want %v", tt.sym, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// loadTraceConfig Tests
// =============================================================================

func TestLoadTraceConfig(t *testing.T) {
	t.Run("empty project root returns empty config", func(t *testing.T) {
		config, err := loadTraceConfig("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(config.ExcludeFromAnalysis) != 0 {
			t.Error("expected empty exclude list")
		}
		if len(config.IncludeOverride) != 0 {
			t.Error("expected empty include list")
		}
	})

	t.Run("missing file returns empty config", func(t *testing.T) {
		tmpDir := t.TempDir()
		config, err := loadTraceConfig(tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(config.ExcludeFromAnalysis) != 0 {
			t.Error("expected empty exclude list")
		}
	})

	t.Run("valid yaml parses correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := `exclude_from_analysis:
  - "vendor/"
  - "third_party/"
include_override:
  - "integration/core/"
`
		if err := os.WriteFile(filepath.Join(tmpDir, "trace.config.yaml"), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		config, err := loadTraceConfig(tmpDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(config.ExcludeFromAnalysis) != 2 {
			t.Errorf("expected 2 exclude entries, got %d", len(config.ExcludeFromAnalysis))
		}
		if len(config.IncludeOverride) != 1 {
			t.Errorf("expected 1 include entry, got %d", len(config.IncludeOverride))
		}
	})

	t.Run("invalid yaml returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := `{invalid yaml: [unclosed`
		if err := os.WriteFile(filepath.Join(tmpDir, "trace.config.yaml"), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		_, err := loadTraceConfig(tmpDir)
		if err == nil {
			t.Fatal("expected error for invalid YAML")
		}
	})
}

// =============================================================================
// computeFileRatio Tests
// =============================================================================

func TestComputeFileRatio(t *testing.T) {
	t.Run("intra-file edges not counted", func(t *testing.T) {
		g := NewGraph("/test")
		sym1 := &ast.Symbol{
			ID: "pkg/a.go:1:foo", Name: "foo", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 1, Language: "go",
		}
		sym2 := &ast.Symbol{
			ID: "pkg/a.go:10:bar", Name: "bar", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 10, Language: "go",
		}
		mustAddNode(t, g, sym1)
		mustAddNode(t, g, sym2)
		// Intra-file edge: foo -> bar (same file)
		mustAddEdge(t, g, sym1.ID, sym2.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)

		nodes := hg.GetNodesInFile("pkg/a.go")
		edgesIn, edgesOut := computeFileRatio(nodes, hg, "pkg/a.go")

		if edgesIn != 0 {
			t.Errorf("expected 0 cross-file edges in, got %d", edgesIn)
		}
		if edgesOut != 0 {
			t.Errorf("expected 0 cross-file edges out, got %d", edgesOut)
		}
	})

	t.Run("cross-file edges counted correctly", func(t *testing.T) {
		g := NewGraph("/test")
		symA := &ast.Symbol{
			ID: "pkg/a.go:1:foo", Name: "foo", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 1, Language: "go",
		}
		symB := &ast.Symbol{
			ID: "pkg/b.go:1:bar", Name: "bar", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/b.go", StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, symA)
		mustAddNode(t, g, symB)
		// Cross-file: a.go -> b.go
		mustAddEdge(t, g, symA.ID, symB.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)

		// Check from a.go perspective: 0 in, 1 out
		nodesA := hg.GetNodesInFile("pkg/a.go")
		inA, outA := computeFileRatio(nodesA, hg, "pkg/a.go")
		if inA != 0 {
			t.Errorf("expected 0 edges in for a.go, got %d", inA)
		}
		if outA != 1 {
			t.Errorf("expected 1 edge out for a.go, got %d", outA)
		}

		// Check from b.go perspective: 1 in, 0 out
		nodesB := hg.GetNodesInFile("pkg/b.go")
		inB, outB := computeFileRatio(nodesB, hg, "pkg/b.go")
		if inB != 1 {
			t.Errorf("expected 1 edge in for b.go, got %d", inB)
		}
		if outB != 0 {
			t.Errorf("expected 0 edges out for b.go, got %d", outB)
		}
	})

	t.Run("external nodes with empty FilePath skipped", func(t *testing.T) {
		g := NewGraph("/test")
		symA := &ast.Symbol{
			ID: "pkg/a.go:1:foo", Name: "foo", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 1, Language: "go",
		}
		// External node: non-nil Symbol but empty FilePath (placeholder)
		symExt := &ast.Symbol{
			ID: "external:1:fmt.Println", Name: "fmt.Println",
			Kind: ast.SymbolKindExternal, FilePath: "", StartLine: 0,
		}
		mustAddNode(t, g, symA)
		mustAddNode(t, g, symExt)
		mustAddEdge(t, g, symA.ID, symExt.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)

		nodesA := hg.GetNodesInFile("pkg/a.go")
		// GR-60b: External nodes (empty FilePath) are now skipped.
		// Calls to stdlib/external packages should not count as cross-file edges.
		_, outA := computeFileRatio(nodesA, hg, "pkg/a.go")
		if outA != 0 {
			t.Errorf("expected 0 edges out (external skipped), got %d", outA)
		}
	})
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestFileClassification_Stats(t *testing.T) {
	t.Run("nil classification returns zero stats", func(t *testing.T) {
		var fc *FileClassification
		stats := fc.Stats()
		if stats.TotalFiles != 0 {
			t.Errorf("expected 0 total files, got %d", stats.TotalFiles)
		}
	})

	t.Run("stats reflect classification", func(t *testing.T) {
		hg := buildClassificationGraph(t)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		stats := fc.Stats()
		if stats.TotalFiles != 4 {
			t.Errorf("expected 4 total files, got %d", stats.TotalFiles)
		}
		if stats.ProductionFiles < 2 {
			t.Errorf("expected at least 2 production files, got %d", stats.ProductionFiles)
		}
		if stats.NonProductionFiles < 1 {
			t.Errorf("expected at least 1 non-production file, got %d", stats.NonProductionFiles)
		}
	})
}

// =============================================================================
// Heuristic Fallback Tests
// =============================================================================

func TestIsTestFilePath(t *testing.T) {
	positives := []string{
		"pkg/server_test.go",
		"tests/unit/test_parser.py",
		"test/integration.js",
		"src/app.test.ts",
		"src/app.spec.js",
		"integration/e2e.ts",
		"quicktests/data.ts",
		"e2e/login.spec.ts",
		"__tests__/app.test.js",
		"__fixtures__/data.json",
		"cypress/e2e/login.cy.ts",
		"fixtures/data.json",
		"asv_bench/perf.py",
	}
	negatives := []string{
		"pkg/server.go",
		"src/app.ts",
		"lib/handler.py",
		"core/engine.go",
	}

	for _, fp := range positives {
		if !isTestFilePath(fp) {
			t.Errorf("expected isTestFilePath(%q) = true", fp)
		}
	}
	for _, fp := range negatives {
		if isTestFilePath(fp) {
			t.Errorf("expected isTestFilePath(%q) = false", fp)
		}
	}
}

func TestIsDocFilePath(t *testing.T) {
	positives := []string{
		"docs/README.md",
		"doc/guide.rst",
		"examples/demo.go",
		"example/usage.py",
		"src/style.css",
		"config.yaml",
	}
	negatives := []string{
		"pkg/server.go",
		"src/app.ts",
		"lib/handler.py",
	}

	for _, fp := range positives {
		if !isDocFilePath(fp) {
			t.Errorf("expected isDocFilePath(%q) = true", fp)
		}
	}
	for _, fp := range negatives {
		if isDocFilePath(fp) {
			t.Errorf("expected isDocFilePath(%q) = false", fp)
		}
	}
}

// =============================================================================
// GR-60b: New Tests for Code Review Fixes
// =============================================================================

func TestClassifyFiles_ConfigOverrides_ExcludeAndIncludeCollision(t *testing.T) {
	t.Run("include takes precedence over exclude for same file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configContent := `exclude_from_analysis:
  - "vendor/"
include_override:
  - "vendor/special/"
`
		if err := os.WriteFile(filepath.Join(tmpDir, "trace.config.yaml"), []byte(configContent), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		g := NewGraph(tmpDir)
		// File matches both exclude ("vendor/") and include ("vendor/special/")
		sym := &ast.Symbol{
			ID: "vendor/special/lib.go:1:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
			FilePath: "vendor/special/lib.go", StartLine: 1, Language: "go",
		}
		// File matches only exclude
		symVendor := &ast.Symbol{
			ID: "vendor/other.go:1:Util", Name: "Util", Kind: ast.SymbolKindFunction,
			FilePath: "vendor/other.go", StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, sym)
		mustAddNode(t, g, symVendor)
		g.Freeze()
		hg, _ := WrapGraph(g)

		fc, err := ClassifyFiles(hg, FileClassificationOptions{ProjectRoot: tmpDir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// vendor/special/lib.go: matches both, include wins → production
		if !fc.IsProduction("vendor/special/lib.go") {
			t.Error("file matching both exclude and include should be production (include wins)")
		}
		// vendor/other.go: matches only exclude → non-production
		if fc.IsProduction("vendor/other.go") {
			t.Error("file matching only exclude should be non-production")
		}
	})
}

func TestIsTestEntryPoint_LanguageInference(t *testing.T) {
	t.Run("Python file with TestX name is NOT a Go test entry point", func(t *testing.T) {
		// Python function named "TestParseConfig" — NOT a test entry point
		// (Python uses test_* convention, not Test*)
		sym := &ast.Symbol{
			Name: "TestParseConfig", Kind: ast.SymbolKindFunction,
			Language: "", FilePath: "utils/parser.py",
		}
		if isTestEntryPoint(sym) {
			t.Error("Python function TestParseConfig should not match Go Test* pattern")
		}
	})

	t.Run("Go file with TestX name and empty lang IS a test entry point", func(t *testing.T) {
		sym := &ast.Symbol{
			Name: "TestParseConfig", Kind: ast.SymbolKindFunction,
			Language: "", FilePath: "pkg/parser_test.go",
		}
		if !isTestEntryPoint(sym) {
			t.Error("Go file with Test* name should be detected via file extension inference")
		}
	})

	t.Run("TS file with describe and empty lang IS a test entry point", func(t *testing.T) {
		sym := &ast.Symbol{
			Name: "describe", Kind: ast.SymbolKindFunction,
			Language: "", FilePath: "src/app.spec.ts",
		}
		if !isTestEntryPoint(sym) {
			t.Error("TS file with describe should be detected via file extension inference")
		}
	})

	t.Run("unknown extension with empty lang matches nothing", func(t *testing.T) {
		sym := &ast.Symbol{
			Name: "TestSomething", Kind: ast.SymbolKindFunction,
			Language: "", FilePath: "src/something.rb",
		}
		if isTestEntryPoint(sym) {
			t.Error("unknown extension should not match any language patterns")
		}
	})
}

func TestInferLanguageFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"main.go", "go"},
		{"test/parser_test.go", "go"},
		{"handler.py", "python"},
		{"types.pyi", "python"},
		{"app.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"module.mts", "typescript"},
		{"app.js", "javascript"},
		{"component.jsx", "javascript"},
		{"module.mjs", "javascript"},
		{"script.cjs", "javascript"},
		{"unknown.rb", ""},
		{"noext", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := inferLanguageFromPath(tt.path)
			if got != tt.expected {
				t.Errorf("inferLanguageFromPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// GR-60c: Definitive Test File Patterns (Phase 4)
// =============================================================================

func TestIsDefinitiveTestFile(t *testing.T) {
	tests := []struct {
		filePath string
		want     bool
		desc     string
	}{
		// Go: _test.go is compiler-enforced
		{"pkg/handler_test.go", true, "Go test file"},
		{"hugolib/some_test.go", true, "Go test file in subdir"},
		{"main_test.go", true, "Go test file at root"},
		{"pkg/handler.go", false, "Go production file"},
		{"pkg/integrationtest_builder.go", false, "Go file with 'test' in name but not _test.go"},

		// Python: test_*.py and *_test.py
		{"tests/test_handler.py", true, "Python test_ prefix"},
		{"tests/handler_test.py", true, "Python _test suffix"},
		{"conftest.py", true, "pytest conftest"},
		{"src/conftest.py", true, "pytest conftest in subdir"},
		{"handler.py", false, "Python production file"},
		{"test_utils.py", true, "Python test_ at root"},

		// JS/TS: *.test.* and *.spec.*
		{"src/handler.test.ts", true, "TS test file"},
		{"src/handler.spec.ts", true, "TS spec file"},
		{"src/handler.test.js", true, "JS test file"},
		{"src/handler.spec.jsx", true, "JSX spec file"},
		{"src/handler.test.tsx", true, "TSX test file"},
		{"src/handler.ts", false, "TS production file"},
		{"src/handler.js", false, "JS production file"},

		// Definitive test directories
		{"__tests__/handler.js", true, "__tests__ directory"},
		{"src/__tests__/handler.ts", true, "__tests__ nested"},
		{"__fixtures__/data.json", true, "__fixtures__ directory catches all files"},
		{"__mocks__/service.ts", true, "__mocks__ directory"},
		{"quicktests/exampleUtil.js", true, "quicktests directory"},
		{"src/quicktests/foo.js", true, "nested quicktests"},
		{"e2e/login.test.ts", true, "e2e directory"},
		{"cypress/integration/spec.js", true, "cypress directory"},

		// Should NOT match
		{"src/utils.go", false, "regular Go file"},
		{"integration/scopes/service.ts", true, "integration dir is in definitive list"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := isDefinitiveTestFile(tt.filePath)
			if got != tt.want {
				t.Errorf("isDefinitiveTestFile(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

// =============================================================================
// GR-60c: Test Symbol Keyword Density (Phase 5)
// =============================================================================

func TestHasTestKeyword(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"AssertFileContent", true},
		{"TestHelper", true},
		{"MockService", true},
		{"StubHandler", true},
		{"SetupRouter", true},
		{"TeardownDB", true},
		{"BenchmarkSort", true},
		{"ExpectError", true},
		{"VerifyToken", true},
		{"FakeClient", true},
		{"SpyLogger", true},
		{"assertEqual", true},
		// Negative cases
		{"Foo", false},
		{"HandleRequest", false},
		{"ParseConfig", false},
		{"NewRouter", false},
		{"ServeHTTP", false},
		{"init", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasTestKeyword(tt.name)
			if got != tt.want {
				t.Errorf("hasTestKeyword(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHasHighTestSymbolDensity(t *testing.T) {
	t.Run("file with >60% test keywords is flagged", func(t *testing.T) {
		nodes := []*Node{
			{Symbol: &ast.Symbol{Name: "AssertFileContent"}},
			{Symbol: &ast.Symbol{Name: "AssertFileContentExact"}},
			{Symbol: &ast.Symbol{Name: "AssertDestination"}},
			{Symbol: &ast.Symbol{Name: "Build"}}, // not a test keyword
		}
		if !hasHighTestSymbolDensity(nodes) {
			t.Error("expected high test keyword density (3/4 = 75%)")
		}
	})

	t.Run("file with <60% test keywords is not flagged", func(t *testing.T) {
		nodes := []*Node{
			{Symbol: &ast.Symbol{Name: "AssertFileContent"}},
			{Symbol: &ast.Symbol{Name: "Build"}},
			{Symbol: &ast.Symbol{Name: "Parse"}},
			{Symbol: &ast.Symbol{Name: "Run"}},
		}
		if hasHighTestSymbolDensity(nodes) {
			t.Error("expected low test keyword density (1/4 = 25%)")
		}
	})

	t.Run("file with fewer than 3 symbols is not flagged", func(t *testing.T) {
		nodes := []*Node{
			{Symbol: &ast.Symbol{Name: "TestHelper"}},
			{Symbol: &ast.Symbol{Name: "MockService"}},
		}
		if hasHighTestSymbolDensity(nodes) {
			t.Error("expected no flag for tiny file (<3 symbols)")
		}
	})

	t.Run("nil symbols are skipped", func(t *testing.T) {
		nodes := []*Node{
			{Symbol: nil},
			{Symbol: &ast.Symbol{Name: "AssertFoo"}},
			{Symbol: &ast.Symbol{Name: "MockBar"}},
			{Symbol: &ast.Symbol{Name: "StubBaz"}},
			{Symbol: &ast.Symbol{Name: "Handle"}},
		}
		// 3/4 non-nil symbols have test keywords = 75%
		if !hasHighTestSymbolDensity(nodes) {
			t.Error("expected high density with nil symbols skipped")
		}
	})
}

// =============================================================================
// GR-60c: Iterative Ratio Refinement (Phase 6)
// =============================================================================

func TestComputeProductionFileRatio(t *testing.T) {
	t.Run("edges from non-production files are not counted", func(t *testing.T) {
		g := NewGraph("/test")

		// Production file
		prodSym := &ast.Symbol{
			ID: "core/lib.go:1:Foo", Name: "Foo", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// Test infrastructure file (the one we're measuring)
		infraSym := &ast.Symbol{
			ID: "infra/helper.go:1:AssertOK", Name: "AssertOK", Kind: ast.SymbolKindFunction,
			FilePath: "infra/helper.go", StartLine: 1, Language: "go",
		}

		// Test file (non-production)
		testSym := &ast.Symbol{
			ID: "tests/foo_test.go:1:TestFoo", Name: "TestFoo", Kind: ast.SymbolKindFunction,
			FilePath: "tests/foo_test.go", StartLine: 1, Language: "go",
		}

		mustAddNode(t, g, prodSym)
		mustAddNode(t, g, infraSym)
		mustAddNode(t, g, testSym)

		// testSym calls infraSym (non-prod → infra)
		mustAddEdge(t, g, testSym.ID, infraSym.ID, EdgeTypeCalls)
		// infraSym calls prodSym (infra → prod)
		mustAddEdge(t, g, infraSym.ID, prodSym.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)

		// Create a classification where tests/foo_test.go is non-production
		fc := &FileClassification{
			files: map[string]bool{
				"core/lib.go":       true,
				"infra/helper.go":   true,
				"tests/foo_test.go": false, // non-production
			},
		}

		infraNode, _ := hg.GetNode(infraSym.ID)
		nodes := []*Node{infraNode}
		prodIn, prodOut := computeProductionFileRatio(nodes, hg, "infra/helper.go", fc)

		// The edge from testSym should NOT be counted (non-production source)
		if prodIn != 0 {
			t.Errorf("expected 0 production incoming edges, got %d", prodIn)
		}
		// The edge to prodSym should be counted
		if prodOut != 1 {
			t.Errorf("expected 1 production outgoing edge, got %d", prodOut)
		}
	})
}

// =============================================================================
// GR-60c: Integration Test — Test Infrastructure Reclassification
// =============================================================================

func TestClassifyFiles_TestInfrastructure(t *testing.T) {
	t.Run("Go _test.go files are definitively non-production regardless of ratio", func(t *testing.T) {
		g := NewGraph("/test")

		// Production file that calls into a _test.go file (unusual but possible via test helpers)
		prodSym := &ast.Symbol{
			ID: "core/lib.go:1:Foo", Name: "Foo", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// _test.go file that has high incoming edges (would normally look like production)
		testHelper := &ast.Symbol{
			ID: "core/helpers_test.go:1:MakeFixture", Name: "MakeFixture", Kind: ast.SymbolKindFunction,
			FilePath: "core/helpers_test.go", StartLine: 1, Language: "go",
		}

		mustAddNode(t, g, prodSym)
		mustAddNode(t, g, testHelper)

		// Create many incoming edges so ratio would be high
		for i := 0; i < 20; i++ {
			caller := &ast.Symbol{
				ID:   ast.GenerateID("other/caller.go", i, "Caller"),
				Name: "Caller", Kind: ast.SymbolKindFunction,
				FilePath: "other/caller.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, caller)
			mustAddEdge(t, g, caller.ID, testHelper.ID, EdgeTypeCalls)
		}
		// 1 outgoing edge
		mustAddEdge(t, g, testHelper.ID, prodSym.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Despite high ratio (20/21 ≈ 0.95), _test.go is definitive
		if fc.IsProduction("core/helpers_test.go") {
			t.Error("_test.go file should be non-production regardless of ratio")
		}
	})

	t.Run("test infra file reclassified when callers are non-production", func(t *testing.T) {
		// Simulates the Hugo integrationtest_builder.go scenario:
		// - integrationtest_builder.go has AssertFileContent called by many _test.go files
		// - Initial ratio is very high (looks like production)
		// - Phase 4 catches _test.go files → non-production
		// - Phase 6 re-computes ratio using only production edges → ratio drops → reclassified
		g := NewGraph("/test")

		// Production file
		prodSym := &ast.Symbol{
			ID: "core/lib.go:1:Render", Name: "Render", Kind: ast.SymbolKindFunction,
			FilePath: "core/lib.go", StartLine: 1, Language: "go",
		}

		// Test infrastructure file (like integrationtest_builder.go)
		assertSym := &ast.Symbol{
			ID: "hugolib/integrationtest_builder.go:1:AssertFileContent", Name: "AssertFileContent",
			Kind: ast.SymbolKindMethod, FilePath: "hugolib/integrationtest_builder.go",
			StartLine: 1, Language: "go",
		}

		// Other production files that call/reference core/lib.go
		// (prevents core/lib.go from being reclassified — it has real production callers)
		otherProd := &ast.Symbol{
			ID: "core/render.go:1:Execute", Name: "Execute", Kind: ast.SymbolKindFunction,
			FilePath: "core/render.go", StartLine: 1, Language: "go",
		}
		otherProd2 := &ast.Symbol{
			ID: "core/render.go:10:Format", Name: "Format", Kind: ast.SymbolKindFunction,
			FilePath: "core/render.go", StartLine: 10, Language: "go",
		}

		mustAddNode(t, g, prodSym)
		mustAddNode(t, g, assertSym)
		mustAddNode(t, g, otherProd)
		mustAddNode(t, g, otherProd2)

		// Production-to-production edges (core/render.go ↔ core/lib.go)
		mustAddEdge(t, g, otherProd.ID, prodSym.ID, EdgeTypeCalls)
		mustAddEdge(t, g, otherProd2.ID, prodSym.ID, EdgeTypeCalls)
		mustAddEdge(t, g, prodSym.ID, otherProd.ID, EdgeTypeCalls)

		// Many _test.go files call AssertFileContent
		for i := 0; i < 50; i++ {
			testSym := &ast.Symbol{
				ID:   ast.GenerateID("hugolib/some_test.go", i, "TestSomething"),
				Name: "TestSomething", Kind: ast.SymbolKindFunction,
				FilePath: "hugolib/some_test.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, testSym)
			mustAddEdge(t, g, testSym.ID, assertSym.ID, EdgeTypeCalls)
		}

		// AssertFileContent calls into production code
		mustAddEdge(t, g, assertSym.ID, prodSym.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// The _test.go file should be non-production (Phase 4 catches it)
		if fc.IsProduction("hugolib/some_test.go") {
			t.Error("_test.go file should be non-production")
		}

		// The integrationtest_builder.go should be reclassified as non-production
		// by Phase 6 (iterative refinement removes non-production edges,
		// leaving only 1 production outgoing edge and 0 production incoming)
		if fc.IsProduction("hugolib/integrationtest_builder.go") {
			t.Error("test infrastructure file should be reclassified as non-production after iterative refinement")
		}

		// Production files stay production (they have production-to-production edges)
		if !fc.IsProduction("core/lib.go") {
			t.Error("production file should remain production")
		}
		if !fc.IsProduction("core/render.go") {
			t.Error("production file should remain production")
		}
	})

	t.Run("caller purity catches test infra with some production callers", func(t *testing.T) {
		// Simulates the Hugo integrationtest_builder.go scenario:
		// - Builder is called by 100 test files AND 5 production files
		// - Builder calls 10 production functions
		// - Production files have enough cross-file edges among themselves
		//   to remain stable when builder is reclassified
		// prod_ratio = 5/(5+10) = 0.33 → above 0.10 threshold
		// caller purity = 5/105 = 4.8% → below 10% → reclassified
		g := NewGraph("/test")

		// core/engine.go — main production file (10 functions)
		var engineFuncs []*ast.Symbol
		for i := 0; i < 10; i++ {
			sym := &ast.Symbol{
				ID:   ast.GenerateID("core/engine.go", i, "Engine"),
				Name: "Engine", Kind: ast.SymbolKindFunction,
				FilePath: "core/engine.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, sym)
			engineFuncs = append(engineFuncs, sym)
		}

		// core/config.go — another production file (10 functions)
		var configFuncs []*ast.Symbol
		for i := 0; i < 10; i++ {
			sym := &ast.Symbol{
				ID:   ast.GenerateID("core/config.go", i, "Config"),
				Name: "Config", Kind: ast.SymbolKindFunction,
				FilePath: "core/config.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, sym)
			configFuncs = append(configFuncs, sym)
		}

		// Bidirectional production edges: engine ↔ config (strong prod-to-prod)
		for i := 0; i < 10; i++ {
			mustAddEdge(t, g, engineFuncs[i].ID, configFuncs[i].ID, EdgeTypeCalls)
			mustAddEdge(t, g, configFuncs[i].ID, engineFuncs[i].ID, EdgeTypeCalls)
		}

		// hugolib/helpers.go — production utilities (5 functions)
		var helperFuncs []*ast.Symbol
		for i := 0; i < 5; i++ {
			sym := &ast.Symbol{
				ID:   ast.GenerateID("hugolib/helpers.go", i, "Helper"),
				Name: "Helper", Kind: ast.SymbolKindFunction,
				FilePath: "hugolib/helpers.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, sym)
			helperFuncs = append(helperFuncs, sym)
			// Helpers call engine (outgoing prod edge)
			mustAddEdge(t, g, sym.ID, engineFuncs[i].ID, EdgeTypeCalls)
			// Engine calls helpers back (incoming prod edge)
			mustAddEdge(t, g, engineFuncs[i].ID, sym.ID, EdgeTypeCalls)
		}

		// Test infrastructure file
		infraSym := &ast.Symbol{
			ID: "hugolib/integrationtest_builder.go:1:AssertFileContent", Name: "AssertFileContent",
			Kind: ast.SymbolKindMethod, FilePath: "hugolib/integrationtest_builder.go",
			StartLine: 1, Language: "go",
		}
		mustAddNode(t, g, infraSym)

		// Builder calls 10 production functions (outgoing)
		for i := 0; i < 10; i++ {
			mustAddEdge(t, g, infraSym.ID, engineFuncs[i].ID, EdgeTypeCalls)
		}

		// 100 test callers from _test.go files
		for i := 0; i < 100; i++ {
			testSym := &ast.Symbol{
				ID:   ast.GenerateID("hugolib/feature_test.go", i, "TestFeature"),
				Name: "TestFeature", Kind: ast.SymbolKindFunction,
				FilePath: "hugolib/feature_test.go", StartLine: i, Language: "go",
			}
			mustAddNode(t, g, testSym)
			mustAddEdge(t, g, testSym.ID, infraSym.ID, EdgeTypeCalls)
		}

		// 5 production callers (helpers also call builder — referencing types)
		for i := 0; i < 5; i++ {
			mustAddEdge(t, g, helperFuncs[i].ID, infraSym.ID, EdgeTypeCalls)
		}

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Builder should be reclassified by caller purity check:
		// 5 prod callers / 105 total callers = 4.8% < 10%
		if fc.IsProduction("hugolib/integrationtest_builder.go") {
			t.Error("test infrastructure file should be reclassified by caller purity check")
		}

		// Production files stay production (strong mutual edges)
		if !fc.IsProduction("core/engine.go") {
			t.Error("core/engine.go should remain production")
		}
		if !fc.IsProduction("core/config.go") {
			t.Error("core/config.go should remain production")
		}
		if !fc.IsProduction("hugolib/helpers.go") {
			t.Error("hugolib/helpers.go should remain production")
		}
	})

	t.Run("quicktests directory is definitively non-production", func(t *testing.T) {
		g := NewGraph("/test")

		prodSym := &ast.Symbol{
			ID: "src/plot.ts:1:render", Name: "render", Kind: ast.SymbolKindFunction,
			FilePath: "src/plot.ts", StartLine: 1, Language: "typescript",
		}

		// quicktests file with high ratio (many callers)
		qtSym := &ast.Symbol{
			ID: "quicktests/exampleUtil.js:1:makeRandomData", Name: "makeRandomData",
			Kind: ast.SymbolKindFunction, FilePath: "quicktests/exampleUtil.js",
			StartLine: 1, Language: "javascript",
		}

		mustAddNode(t, g, prodSym)
		mustAddNode(t, g, qtSym)

		// Many callers from quicktests
		for i := 0; i < 30; i++ {
			caller := &ast.Symbol{
				ID:   ast.GenerateID("quicktests/test_"+string(rune('a'+i))+".js", 1, "run"),
				Name: "run", Kind: ast.SymbolKindFunction,
				FilePath: "quicktests/test_" + string(rune('a'+i)) + ".js", StartLine: 1,
				Language: "javascript",
			}
			mustAddNode(t, g, caller)
			mustAddEdge(t, g, caller.ID, qtSym.ID, EdgeTypeCalls)
		}
		mustAddEdge(t, g, qtSym.ID, prodSym.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fc.IsProduction("quicktests/exampleUtil.js") {
			t.Error("quicktests/ file should be non-production (definitive directory)")
		}
	})

	t.Run("Python test_*.py is definitively non-production", func(t *testing.T) {
		g := NewGraph("/test")

		prodSym := &ast.Symbol{
			ID: "app/views.py:1:index", Name: "index", Kind: ast.SymbolKindFunction,
			FilePath: "app/views.py", StartLine: 1, Language: "python",
		}

		testSym := &ast.Symbol{
			ID: "tests/test_views.py:1:test_index", Name: "test_index",
			Kind: ast.SymbolKindFunction, FilePath: "tests/test_views.py",
			StartLine: 1, Language: "python",
		}

		mustAddNode(t, g, prodSym)
		mustAddNode(t, g, testSym)

		// Even with incoming edges
		for i := 0; i < 10; i++ {
			caller := &ast.Symbol{
				ID:   ast.GenerateID("tests/helpers.py", i, "setup"),
				Name: "setup", Kind: ast.SymbolKindFunction,
				FilePath: "tests/helpers.py", StartLine: i, Language: "python",
			}
			mustAddNode(t, g, caller)
			mustAddEdge(t, g, caller.ID, testSym.ID, EdgeTypeCalls)
		}
		mustAddEdge(t, g, testSym.ID, prodSym.ID, EdgeTypeCalls)

		g.Freeze()
		hg, _ := WrapGraph(g)
		fc, err := ClassifyFiles(hg, FileClassificationOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if fc.IsProduction("tests/test_views.py") {
			t.Error("test_*.py file should be non-production")
		}
	})
}

func TestLoadTraceConfig_PermissionError(t *testing.T) {
	t.Run("unreadable file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "trace.config.yaml")
		// Write a valid file, then remove read permission
		if err := os.WriteFile(configPath, []byte("exclude_from_analysis:\n  - vendor/\n"), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}
		if err := os.Chmod(configPath, 0000); err != nil {
			t.Fatalf("failed to chmod: %v", err)
		}
		t.Cleanup(func() {
			// Restore permissions so TempDir cleanup works
			os.Chmod(configPath, 0644)
		})

		_, err := loadTraceConfig(tmpDir)
		if err == nil {
			t.Fatal("expected error for unreadable config file")
		}
	})
}
