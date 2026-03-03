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
// Smell Finder Tests
// ============================================================================

func TestNewSmellFinder(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewSmellFinder(idx, "/test")

	if finder == nil {
		t.Fatal("NewSmellFinder returned nil")
	}
}

func TestSmellFinder_FindCodeSmells_NilContext(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewSmellFinder(idx, "/test")

	_, err := finder.FindCodeSmells(nil, "", nil)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestSmellFinder_FindCodeSmells_EmptyIndex(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewSmellFinder(idx, "/test")

	ctx := context.Background()
	smells, err := finder.FindCodeSmells(ctx, "", nil)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(smells) != 0 {
		t.Errorf("expected 0 smells for empty index, got %d", len(smells))
	}
}

func TestSmellFinder_Summary(t *testing.T) {
	idx := index.NewSymbolIndex()
	finder := NewSmellFinder(idx, "/test")

	// Empty
	summary := finder.Summary(nil)
	if summary != "No code smells detected" {
		t.Errorf("unexpected summary: %s", summary)
	}

	// With smells
	smells := []CodeSmell{
		{Type: SmellLongFunction, Severity: SeverityWarning},
		{Type: SmellLongFunction, Severity: SeverityError},
		{Type: SmellGodObject, Severity: SeverityWarning},
	}

	summary = finder.Summary(smells)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestSmellThresholds_DefaultValues(t *testing.T) {
	thresholds := DefaultSmellThresholds()

	if thresholds.MaxFunctionLines != 50 {
		t.Errorf("expected MaxFunctionLines 50, got %d", thresholds.MaxFunctionLines)
	}

	if thresholds.MaxParameters != 5 {
		t.Errorf("expected MaxParameters 5, got %d", thresholds.MaxParameters)
	}

	if thresholds.MaxMethodCount != 20 {
		t.Errorf("expected MaxMethodCount 20, got %d", thresholds.MaxMethodCount)
	}

	if thresholds.MaxNestingDepth != 4 {
		t.Errorf("expected MaxNestingDepth 4, got %d", thresholds.MaxNestingDepth)
	}
}

func TestDefaultSmellOptions(t *testing.T) {
	opts := DefaultSmellOptions()

	if opts.MinSeverity != SeverityWarning {
		t.Errorf("expected MinSeverity WARNING, got %s", opts.MinSeverity)
	}

	if opts.IncludeTests {
		t.Error("expected IncludeTests to be false")
	}
}

func TestCountParameters(t *testing.T) {
	tests := []struct {
		signature string
		expected  int
	}{
		{"func()", 0},
		{"func(a int)", 1},
		{"func(a, b int)", 2},
		{"func(a int, b string)", 2},
		{"func(a int, b string, c bool)", 3},
		{"func(ctx context.Context, opts *Options)", 2},
		{"func(fn func(int) bool)", 1},
		{"func(a, b, c, d, e, f int)", 6},
	}

	for _, tt := range tests {
		result := countParameters(tt.signature)
		if result != tt.expected {
			t.Errorf("countParameters(%q) = %d, want %d", tt.signature, result, tt.expected)
		}
	}
}

func TestCalculateMaxNesting(t *testing.T) {
	tests := []struct {
		code     string
		expected int
	}{
		{"", 0},
		{"{}", 1},
		{"{{}}", 2},
		{"{{{}}{}}}", 3},
		{"if x { if y { if z { } } }", 3},
	}

	for _, tt := range tests {
		result := calculateMaxNesting(tt.code)
		if result != tt.expected {
			t.Errorf("calculateMaxNesting(%q) = %d, want %d", tt.code, result, tt.expected)
		}
	}
}

// ============================================================================
// Dead Code Finder Tests
// ============================================================================

func TestNewDeadCodeFinder(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()
	finder := NewDeadCodeFinder(g, idx, "/test")

	if finder == nil {
		t.Fatal("NewDeadCodeFinder returned nil")
	}
}

func TestDeadCodeFinder_FindDeadCode_NilContext(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()
	finder := NewDeadCodeFinder(g, idx, "/test")

	_, err := finder.FindDeadCode(nil, "", nil)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestDeadCodeFinder_Summary(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()
	finder := NewDeadCodeFinder(g, idx, "/test")

	// Empty
	summary := finder.Summary(nil)
	if summary != "No dead code detected" {
		t.Errorf("unexpected summary: %s", summary)
	}

	// With dead code
	dead := []DeadCode{
		{Type: "function", Name: "unusedFunc"},
		{Type: "function", Name: "anotherFunc"},
		{Type: "type", Name: "UnusedType"},
	}

	summary = finder.Summary(dead)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestDefaultExclusions(t *testing.T) {
	excl := DefaultExclusions()

	if excl == nil {
		t.Fatal("DefaultExclusions returned nil")
	}

	// Check entry points
	if len(excl.EntryPoints) == 0 {
		t.Error("expected non-empty EntryPoints")
	}

	// main should be excluded
	found := false
	for _, ep := range excl.EntryPoints {
		if ep == "main" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'main' in EntryPoints")
	}

	// Exported symbols should be excluded by default
	if !excl.ExportedSymbols {
		t.Error("expected ExportedSymbols to be true")
	}

	// Interface impls should be excluded
	if !excl.InterfaceImpls {
		t.Error("expected InterfaceImpls to be true")
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		name     string
		expected bool
	}{
		{"main", "main", true},
		{"main", "Main", false},
		{"Test*", "TestFoo", true},
		{"Test*", "TestBar", true},
		{"Test*", "test", false},
		{"Benchmark*", "BenchmarkFoo", true},
		{"*", "", true},
		{"*", "anything", true},
	}

	for _, tt := range tests {
		result := matchPattern(tt.pattern, tt.name)
		if result != tt.expected {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.name, result, tt.expected)
		}
	}
}

func TestDeadCodeExclusions_IsExcluded(t *testing.T) {
	excl := DefaultExclusions()
	g := graph.NewGraph("/test")

	tests := []struct {
		symbol   *ast.Symbol
		excluded bool
		reason   string
	}{
		{
			symbol: &ast.Symbol{
				Name:     "main",
				Exported: true,
			},
			excluded: true,
			reason:   "entry point",
		},
		{
			symbol: &ast.Symbol{
				Name:     "TestFoo",
				Exported: true,
			},
			excluded: true,
			reason:   "entry point",
		},
		{
			symbol: &ast.Symbol{
				Name:     "PublicFunc",
				Exported: true,
			},
			excluded: true,
			reason:   "exported",
		},
		{
			symbol: &ast.Symbol{
				Name:     "privateFunc",
				Exported: false,
			},
			excluded: false,
			reason:   "",
		},
	}

	for _, tt := range tests {
		excluded, reason := excl.IsExcluded(tt.symbol, g)
		if excluded != tt.excluded {
			t.Errorf("IsExcluded(%s) = %v, want %v", tt.symbol.Name, excluded, tt.excluded)
		}
		if excluded && reason == "" {
			t.Errorf("excluded symbol %s should have a reason", tt.symbol.Name)
		}
	}
}

func TestDeadCodeFinder_WithReferencedSymbols(t *testing.T) {
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create a referenced function
	fn := &ast.Symbol{
		ID:        "test.go:1:referencedFunc",
		Name:      "referencedFunc",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
		Exported:  false,
		Language:  "go",
	}

	caller := &ast.Symbol{
		ID:        "test.go:10:caller",
		Name:      "caller",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 10,
		EndLine:   15,
		Exported:  false,
		Language:  "go",
	}

	idx.Add(fn)
	idx.Add(caller)

	_, _ = g.AddNode(fn)
	_, _ = g.AddNode(caller)
	_ = g.AddEdge(caller.ID, fn.ID, graph.EdgeTypeCalls, ast.Location{})
	g.Freeze()

	finder := NewDeadCodeFinder(g, idx, "/test")

	ctx := context.Background()
	opts := DefaultDeadCodeOptions()
	opts.IncludeExported = true // Include all for testing

	dead, err := finder.FindDeadCode(ctx, "", &opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// referencedFunc should NOT be in dead code (it's referenced)
	// caller SHOULD be in dead code (unreferenced)
	for _, d := range dead {
		if d.Name == "referencedFunc" {
			t.Error("referencedFunc should not be marked as dead code")
		}
	}
}

// ============================================================================
// Convention Extractor Tests
// ============================================================================

func TestNewConventionExtractor(t *testing.T) {
	idx := index.NewSymbolIndex()
	extractor := NewConventionExtractor(idx, "/test")

	if extractor == nil {
		t.Fatal("NewConventionExtractor returned nil")
	}
}

func TestConventionExtractor_ExtractConventions_NilContext(t *testing.T) {
	idx := index.NewSymbolIndex()
	extractor := NewConventionExtractor(idx, "/test")

	_, err := extractor.ExtractConventions(nil, "", nil)

	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput, got %v", err)
	}
}

func TestConventionExtractor_Summary(t *testing.T) {
	idx := index.NewSymbolIndex()
	extractor := NewConventionExtractor(idx, "/test")

	// Empty
	summary := extractor.Summary(nil)
	if summary != "No conventions extracted" {
		t.Errorf("unexpected summary: %s", summary)
	}

	// With conventions
	conventions := []Convention{
		{Type: string(ConventionNaming), Pattern: "PascalCase", Frequency: 0.8},
		{Type: string(ConventionTesting), Pattern: "table-driven", Frequency: 0.7},
	}

	summary = extractor.Summary(conventions)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestDefaultConventionOptions(t *testing.T) {
	opts := DefaultConventionOptions()

	if opts.SampleSize != 100 {
		t.Errorf("expected SampleSize 100, got %d", opts.SampleSize)
	}

	if opts.MinFrequency != 0.3 {
		t.Errorf("expected MinFrequency 0.3, got %f", opts.MinFrequency)
	}

	if opts.IncludeTests {
		t.Error("expected IncludeTests to be false")
	}
}

func TestClassifyName(t *testing.T) {
	tests := []struct {
		name     string
		contains string
	}{
		{"GetUser", "PascalCase"},
		{"getUserData", "camelCase"},
		{"NewService", "New* prefix"},
		{"CreateConfig", "Create* prefix"},
		{"IsValid", "Is* prefix"},
		{"HasPermission", "Has* prefix"},
	}

	for _, tt := range tests {
		patterns := classifyName(tt.name)
		found := false
		for _, p := range patterns {
			if p == tt.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("classifyName(%q) should contain %q, got %v", tt.name, tt.contains, patterns)
		}
	}
}

func TestIsUpperCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", false},
		{"a", false},
		{"A", true},
		{"Abc", true},
		{"ABC", true},
		{"AbcDef", true},
		{"abc", false},
		{"abc_def", false},
		{"Abc_Def", false},
	}

	for _, tt := range tests {
		result := isUpperCamelCase(tt.input)
		if result != tt.expected {
			t.Errorf("isUpperCamelCase(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestIsLowerCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", false},
		{"a", true},
		{"A", false},
		{"abc", true},
		{"abcDef", true},
		{"Abc", false},
		{"abc_def", false},
	}

	for _, tt := range tests {
		result := isLowerCamelCase(tt.input)
		if result != tt.expected {
			t.Errorf("isLowerCamelCase(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

// ============================================================================
// Integration Tests
// ============================================================================

func TestSmellDetection_LongFunction(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Create a long function (60 lines)
	longFunc := &ast.Symbol{
		ID:        "test.go:1:longFunc",
		Name:      "longFunc",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   60,
		Signature: "func()",
		Exported:  false,
		Language:  "go",
	}

	idx.Add(longFunc)

	finder := NewSmellFinder(idx, "/nonexistent") // No file access

	ctx := context.Background()
	opts := DefaultSmellOptions()
	opts.Thresholds.MaxFunctionLines = 50

	// Direct call to detectLongFunctions (since we can't read actual files)
	smells := finder.detectLongFunctions(ctx, "", &opts)

	if len(smells) != 1 {
		t.Fatalf("expected 1 smell, got %d", len(smells))
	}

	if smells[0].Type != SmellLongFunction {
		t.Errorf("expected SmellLongFunction, got %s", smells[0].Type)
	}

	if smells[0].Value != 60 {
		t.Errorf("expected value 60, got %d", smells[0].Value)
	}
}

func TestSmellDetection_GodObject(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Create a type
	godType := &ast.Symbol{
		ID:        "test.go:1:GodService",
		Name:      "GodService",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   10,
		Exported:  true,
		Language:  "go",
	}
	idx.Add(godType)

	// Create 25 methods for it
	for i := 0; i < 25; i++ {
		method := &ast.Symbol{
			ID:        "test.go:" + itoa(10+i) + ":Method" + itoa(i),
			Name:      "Method" + itoa(i),
			Kind:      ast.SymbolKindMethod,
			FilePath:  "test.go",
			StartLine: 10 + i,
			EndLine:   15 + i,
			Receiver:  "*GodService",
			Exported:  true,
			Language:  "go",
		}
		idx.Add(method)
	}

	finder := NewSmellFinder(idx, "/nonexistent")

	ctx := context.Background()
	opts := DefaultSmellOptions()
	opts.Thresholds.MaxMethodCount = 20

	smells := finder.detectGodObjects(ctx, "", &opts)

	if len(smells) != 1 {
		t.Fatalf("expected 1 smell, got %d", len(smells))
	}

	if smells[0].Type != SmellGodObject {
		t.Errorf("expected SmellGodObject, got %s", smells[0].Type)
	}

	if smells[0].Value != 25 {
		t.Errorf("expected value 25, got %d", smells[0].Value)
	}
}

func TestConventionExtraction_NamingPatterns(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Add functions with consistent naming
	names := []string{"GetUser", "GetConfig", "GetService", "SetUser", "SetConfig"}
	for i, name := range names {
		fn := &ast.Symbol{
			ID:        "test.go:" + itoa(i+1) + ":" + name,
			Name:      name,
			Kind:      ast.SymbolKindFunction,
			FilePath:  "test.go",
			StartLine: i + 1,
			EndLine:   i + 10,
			Exported:  true,
			Language:  "go",
		}
		idx.Add(fn)
	}

	extractor := NewConventionExtractor(idx, "/nonexistent")

	ctx := context.Background()
	opts := DefaultConventionOptions()
	opts.MinFrequency = 0.3

	conventions := extractor.extractNamingConventions(ctx, "", &opts)

	// Should detect PascalCase and Get*/Set* patterns
	foundPascal := false
	foundGetPrefix := false

	for _, conv := range conventions {
		if conv.Pattern == "PascalCase" {
			foundPascal = true
		}
		if conv.Pattern == "Get* prefix" {
			foundGetPrefix = true
		}
	}

	if !foundPascal {
		t.Error("expected to detect PascalCase convention")
	}

	if !foundGetPrefix {
		t.Error("expected to detect Get* prefix convention")
	}
}
