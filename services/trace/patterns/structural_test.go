// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// ============================================================================
// Fingerprint Tests
// ============================================================================

func TestNewFingerprinter(t *testing.T) {
	config := DefaultFingerprintConfig()
	fp := NewFingerprinter(config)

	if fp == nil {
		t.Fatal("NewFingerprinter returned nil")
	}

	if len(fp.hashSeeds) != config.NumHashFuncs {
		t.Errorf("expected %d hash seeds, got %d", config.NumHashFuncs, len(fp.hashSeeds))
	}
}

func TestFingerprint_NilSymbol(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())
	result := fp.Fingerprint(nil, "some code")

	if result != nil {
		t.Error("expected nil for nil symbol")
	}
}

func TestFingerprint_EmptyCode(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())
	symbol := &ast.Symbol{
		ID:        "test:1:TestFunc",
		Name:      "TestFunc",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   10,
	}

	result := fp.Fingerprint(symbol, "")
	if result != nil {
		t.Error("expected nil for empty code")
	}
}

func TestFingerprint_ValidCode(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())
	symbol := &ast.Symbol{
		ID:        "test.go:1:TestFunc",
		Name:      "TestFunc",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	code := `func TestFunc() {
		if true {
			return
		}
	}`

	result := fp.Fingerprint(symbol, code)

	if result == nil {
		t.Fatal("expected non-nil fingerprint")
	}

	if result.SymbolID != symbol.ID {
		t.Errorf("expected SymbolID %s, got %s", symbol.ID, result.SymbolID)
	}

	if result.LineCount != 5 {
		t.Errorf("expected LineCount 5, got %d", result.LineCount)
	}

	if len(result.TokenHashes) == 0 {
		t.Error("expected non-empty TokenHashes")
	}

	if len(result.MinHashSig) == 0 {
		t.Error("expected non-empty MinHashSig")
	}
}

func TestJaccardSimilarity_Identical(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())

	symbol := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	code := `func test() {
		x := 1
		return x
	}`

	fp1 := fp.Fingerprint(symbol, code)
	fp2 := fp.Fingerprint(symbol, code)

	similarity := fp1.JaccardSimilarity(fp2)

	if similarity != 1.0 {
		t.Errorf("expected similarity 1.0 for identical code, got %f", similarity)
	}
}

func TestJaccardSimilarity_Different(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())

	symbol1 := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	symbol2 := &ast.Symbol{
		ID:        "test.go:10:Func2",
		Name:      "Func2",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 10,
		EndLine:   15,
	}

	code1 := `func add(a, b int) int {
		return a + b
	}`

	code2 := `func multiply(x, y float64) float64 {
		for i := 0; i < 10; i++ {
			x = x * y
		}
		return x
	}`

	fp1 := fp.Fingerprint(symbol1, code1)
	fp2 := fp.Fingerprint(symbol2, code2)

	similarity := fp1.JaccardSimilarity(fp2)

	if similarity >= 0.5 {
		t.Errorf("expected low similarity for different code, got %f", similarity)
	}
}

func TestEstimatedJaccard(t *testing.T) {
	fp := NewFingerprinter(DefaultFingerprintConfig())

	symbol := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	code := `func test() {
		x := 1
		y := 2
		return x + y
	}`

	fp1 := fp.Fingerprint(symbol, code)
	fp2 := fp.Fingerprint(symbol, code)

	estimated := fp1.EstimatedJaccard(fp2)

	if estimated != 1.0 {
		t.Errorf("expected estimated similarity 1.0 for identical code, got %f", estimated)
	}
}

// ============================================================================
// LSH Index Tests
// ============================================================================

func TestNewLSHIndex(t *testing.T) {
	idx := NewLSHIndex(20, 5)

	if idx == nil {
		t.Fatal("NewLSHIndex returned nil")
	}

	if idx.numBands != 20 {
		t.Errorf("expected 20 bands, got %d", idx.numBands)
	}

	if idx.rowsPerBand != 5 {
		t.Errorf("expected 5 rows per band, got %d", idx.rowsPerBand)
	}
}

func TestLSHIndex_AddAndQuery(t *testing.T) {
	idx := NewLSHIndex(20, 5)
	fingerprinter := NewFingerprinter(DefaultFingerprintConfig())

	// Create similar code snippets
	symbol1 := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	code := `func process(data []int) []int {
		result := make([]int, len(data))
		for i, v := range data {
			result[i] = v * 2
		}
		return result
	}`

	fp := fingerprinter.Fingerprint(symbol1, code)
	idx.Add(fp)

	if idx.Size() != 1 {
		t.Errorf("expected size 1, got %d", idx.Size())
	}

	// Query for the same fingerprint should find it
	candidates := idx.Query(fp)

	// Self should not be returned
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates (self excluded), got %d", len(candidates))
	}
}

func TestLSHIndex_FindAllDuplicates(t *testing.T) {
	idx := NewLSHIndex(20, 5)
	fingerprinter := NewFingerprinter(DefaultFingerprintConfig())

	// Create two similar code snippets
	symbol1 := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	symbol2 := &ast.Symbol{
		ID:        "test.go:20:Func2",
		Name:      "Func2",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 20,
		EndLine:   25,
	}

	code1 := `func process(data []int) []int {
		result := make([]int, len(data))
		for i, v := range data {
			result[i] = v * 2
		}
		return result
	}`

	// Very similar code
	code2 := `func process(data []int) []int {
		result := make([]int, len(data))
		for i, v := range data {
			result[i] = v * 2
		}
		return result
	}`

	fp1 := fingerprinter.Fingerprint(symbol1, code1)
	fp2 := fingerprinter.Fingerprint(symbol2, code2)

	idx.Add(fp1)
	idx.Add(fp2)

	pairs := idx.FindAllDuplicates(0.8)

	if len(pairs) != 1 {
		t.Errorf("expected 1 duplicate pair, got %d", len(pairs))
	}
}

func TestLSHIndex_Remove(t *testing.T) {
	idx := NewLSHIndex(20, 5)
	fingerprinter := NewFingerprinter(DefaultFingerprintConfig())

	symbol := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
	}

	code := `func test() { return }`
	fp := fingerprinter.Fingerprint(symbol, code)

	idx.Add(fp)
	if idx.Size() != 1 {
		t.Fatal("expected size 1 after add")
	}

	idx.Remove(symbol.ID)
	if idx.Size() != 0 {
		t.Errorf("expected size 0 after remove, got %d", idx.Size())
	}
}

func TestLSHIndex_Stats(t *testing.T) {
	idx := NewLSHIndex(20, 5)

	stats := idx.Stats()

	if stats.NumBands != 20 {
		t.Errorf("expected 20 bands, got %d", stats.NumBands)
	}

	if stats.RowsPerBand != 5 {
		t.Errorf("expected 5 rows per band, got %d", stats.RowsPerBand)
	}

	if stats.NumFingerprints != 0 {
		t.Errorf("expected 0 fingerprints, got %d", stats.NumFingerprints)
	}
}

// ============================================================================
// Package Graph Tests
// ============================================================================

func TestNewPackageGraph(t *testing.T) {
	pg := NewPackageGraph()

	if pg == nil {
		t.Fatal("NewPackageGraph returned nil")
	}

	if len(pg.packages) != 0 {
		t.Error("expected empty packages map")
	}
}

func TestPackageGraph_FindCycles(t *testing.T) {
	pg := NewPackageGraph()

	// Create a cycle: A -> B -> C -> A
	pg.addImport("pkg/a", "pkg/b")
	pg.addImport("pkg/b", "pkg/c")
	pg.addImport("pkg/c", "pkg/a")

	cycles := pg.FindCycles()

	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d", len(cycles))
	}

	if len(cycles[0]) != 3 {
		t.Errorf("expected cycle length 3, got %d", len(cycles[0]))
	}
}

func TestPackageGraph_NoCycles(t *testing.T) {
	pg := NewPackageGraph()

	// Create non-cyclic dependencies
	pg.addImport("pkg/a", "pkg/b")
	pg.addImport("pkg/a", "pkg/c")
	pg.addImport("pkg/b", "pkg/d")

	cycles := pg.FindCycles()

	if len(cycles) != 0 {
		t.Errorf("expected 0 cycles, got %d", len(cycles))
	}
}

func TestPackageGraph_TopologicalSort(t *testing.T) {
	pg := NewPackageGraph()

	// Create DAG: a imports b, a imports c, b imports d
	// So: a → b → d, a → c
	pg.addImport("pkg/a", "pkg/b")
	pg.addImport("pkg/a", "pkg/c")
	pg.addImport("pkg/b", "pkg/d")

	sorted := pg.TopologicalSort()

	if sorted == nil {
		t.Fatal("expected valid topological sort")
	}

	// Verify all packages are present
	if len(sorted) != 4 {
		t.Fatalf("expected 4 packages, got %d", len(sorted))
	}

	// Verify it's a valid topological order: for each edge a→b,
	// a should come before b (importer before importee in this implementation)
	positions := make(map[string]int)
	for i, pkg := range sorted {
		positions[pkg] = i
	}

	// Check all edges: from should come before to
	edges := map[string][]string{
		"pkg/a": {"pkg/b", "pkg/c"},
		"pkg/b": {"pkg/d"},
	}

	for from, tos := range edges {
		fromPos, fromOK := positions[from]
		if !fromOK {
			continue
		}
		for _, to := range tos {
			toPos, toOK := positions[to]
			if !toOK {
				continue
			}
			if fromPos >= toPos {
				t.Errorf("in topo sort, %s (pos %d) should come before %s (pos %d)",
					from, fromPos, to, toPos)
			}
		}
	}
}

func TestPackageGraph_TopologicalSort_WithCycle(t *testing.T) {
	pg := NewPackageGraph()

	// Create cycle
	pg.addImport("pkg/a", "pkg/b")
	pg.addImport("pkg/b", "pkg/a")

	sorted := pg.TopologicalSort()

	if sorted != nil {
		t.Error("expected nil for graph with cycle")
	}
}

func TestPackageGraph_FindShortestCycle(t *testing.T) {
	pg := NewPackageGraph()

	pg.addImport("pkg/a", "pkg/b")
	pg.addImport("pkg/b", "pkg/c")
	pg.addImport("pkg/c", "pkg/a")

	cycle := pg.FindShortestCycle("pkg/a")

	if cycle == nil {
		t.Fatal("expected to find cycle")
	}

	// Cycle should start and end with pkg/a
	if cycle[0] != "pkg/a" || cycle[len(cycle)-1] != "pkg/a" {
		t.Error("cycle should start and end with pkg/a")
	}
}

// ============================================================================
// Circular Dependency Finder Tests
// ============================================================================

func TestNewCircularDepFinder(t *testing.T) {
	g := graph.NewGraph("/test")
	finder := NewCircularDepFinder(g, "github.com/test")

	if finder == nil {
		t.Fatal("NewCircularDepFinder returned nil")
	}
}

func TestCircularDepFinder_FindCircularDeps_NilContext(t *testing.T) {
	g := graph.NewGraph("/test")
	finder := NewCircularDepFinder(g, "github.com/test")

	_, err := finder.FindCircularDeps(nil, "", CircularDepPackage)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCircularDepFinder_Summary(t *testing.T) {
	g := graph.NewGraph("/test")
	finder := NewCircularDepFinder(g, "github.com/test")

	// Empty
	summary := finder.Summary(nil)
	if summary != "No circular dependencies detected" {
		t.Errorf("unexpected summary: %s", summary)
	}

	// With deps
	deps := []CircularDep{
		{Type: string(CircularDepPackage), Cycle: []string{"a", "b", "a"}},
		{Type: string(CircularDepPackage), Cycle: []string{"c", "d", "c"}},
	}

	summary = finder.Summary(deps)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

// ============================================================================
// Duplication Finder Tests
// ============================================================================

func TestNewDuplicationFinder(t *testing.T) {
	idx := index.NewSymbolIndex()

	finder := NewDuplicationFinder(idx, "/test")

	if finder == nil {
		t.Fatal("NewDuplicationFinder returned nil")
	}
}

func TestDuplicationFinder_FindDuplication_NilContext(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewDuplicationFinder(idx, "/test")

	_, err := finder.FindDuplication(nil, "", nil)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestDuplicationFinder_Summary(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewDuplicationFinder(idx, "/test")

	// Empty
	summary := finder.Summary(nil)
	if summary != "No code duplication detected" {
		t.Errorf("unexpected summary: %s", summary)
	}

	// With duplications
	dups := []Duplication{
		{Type: string(DuplicationExact), Similarity: 1.0},
		{Type: string(DuplicationNear), Similarity: 0.9},
	}

	summary = finder.Summary(dups)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestDuplicationFinder_Stats(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewDuplicationFinder(idx, "/test")

	stats := finder.Stats()

	if stats.Indexed {
		t.Error("expected indexed to be false initially")
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

func TestDuplicationDetection_EndToEnd(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Add some symbols
	sym1 := &ast.Symbol{
		ID:        "test.go:1:Func1",
		Name:      "Func1",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   10,
		Language:  "go",
	}

	sym2 := &ast.Symbol{
		ID:        "test.go:20:Func2",
		Name:      "Func2",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 20,
		EndLine:   30,
		Language:  "go",
	}

	idx.Add(sym1)
	idx.Add(sym2)

	// Create finder (without file system access, this won't find much)
	finder := NewDuplicationFinder(idx, "/nonexistent")

	ctx := context.Background()
	opts := DefaultDuplicationOptions()
	opts.MinLines = 1 // Lower threshold for testing

	// This won't find duplicates without actual files, but it shouldn't crash
	_, err := finder.BuildIndex(ctx, &opts)

	// Should not return an error, just skip symbols it can't read
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCircularDepDetection_EndToEnd(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create symbols representing files in different packages
	fileA := &ast.Symbol{
		ID:       "pkg/a/a.go:1:file",
		Name:     "a.go",
		Kind:     ast.SymbolKindFile,
		FilePath: "pkg/a/a.go",
		Package:  "a",
		Language: "go",
	}

	fileB := &ast.Symbol{
		ID:       "pkg/b/b.go:1:file",
		Name:     "b.go",
		Kind:     ast.SymbolKindFile,
		FilePath: "pkg/b/b.go",
		Package:  "b",
		Language: "go",
	}

	idx.Add(fileA)
	idx.Add(fileB)

	_, _ = g.AddNode(fileA)
	_, _ = g.AddNode(fileB)
	g.Freeze()

	finder := NewCircularDepFinder(g, "github.com/test")

	ctx := context.Background()
	deps, err := finder.FindCircularDeps(ctx, "", CircularDepPackage)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// No cycles expected with this simple setup
	if len(deps) != 0 {
		t.Errorf("expected no cycles, got %d", len(deps))
	}
}
