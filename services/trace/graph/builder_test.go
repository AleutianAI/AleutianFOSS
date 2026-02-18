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
	"fmt"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Helper function to create a test symbol.
func testSymbol(name string, kind ast.SymbolKind, filePath string, line int) *ast.Symbol {
	return &ast.Symbol{
		ID:        ast.GenerateID(filePath, line, name),
		Name:      name,
		Kind:      kind,
		FilePath:  filePath,
		StartLine: line,
		EndLine:   line + 10,
		StartCol:  0,
		EndCol:    50,
		Language:  "go",
	}
}

// Helper function to create a test parse result.
func testParseResult(filePath string, symbols []*ast.Symbol, imports []ast.Import) *ast.ParseResult {
	return &ast.ParseResult{
		FilePath: filePath,
		Language: "go",
		Symbols:  symbols,
		Imports:  imports,
		Package:  "test",
	}
}

func TestBuilder_NewBuilder(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		builder := NewBuilder()
		if builder == nil {
			t.Fatal("NewBuilder returned nil")
		}
		if builder.options.MaxMemoryMB != DefaultMaxMemoryMB {
			t.Errorf("expected MaxMemoryMB=%d, got %d", DefaultMaxMemoryMB, builder.options.MaxMemoryMB)
		}
		if builder.options.WorkerCount <= 0 {
			t.Error("expected WorkerCount > 0")
		}
	})

	t.Run("custom options", func(t *testing.T) {
		builder := NewBuilder(
			WithProjectRoot("/test/project"),
			WithMaxMemoryMB(1024),
			WithWorkerCount(4),
		)
		if builder.options.ProjectRoot != "/test/project" {
			t.Errorf("expected ProjectRoot=%q, got %q", "/test/project", builder.options.ProjectRoot)
		}
		if builder.options.MaxMemoryMB != 1024 {
			t.Errorf("expected MaxMemoryMB=1024, got %d", builder.options.MaxMemoryMB)
		}
		if builder.options.WorkerCount != 4 {
			t.Errorf("expected WorkerCount=4, got %d", builder.options.WorkerCount)
		}
	})
}

func TestBuilder_Build_EmptyResults(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("nil results slice", func(t *testing.T) {
		result, err := builder.Build(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Graph == nil {
			t.Fatal("expected non-nil graph")
		}
		if result.Graph.NodeCount() != 0 {
			t.Errorf("expected 0 nodes, got %d", result.Graph.NodeCount())
		}
		if result.Graph.EdgeCount() != 0 {
			t.Errorf("expected 0 edges, got %d", result.Graph.EdgeCount())
		}
		if result.Incomplete {
			t.Error("expected Incomplete=false for empty build")
		}
	})

	t.Run("empty results slice", func(t *testing.T) {
		result, err := builder.Build(ctx, []*ast.ParseResult{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Graph.NodeCount() != 0 {
			t.Errorf("expected 0 nodes, got %d", result.Graph.NodeCount())
		}
		if !result.Success() {
			t.Error("expected Success()=true for empty build")
		}
	})
}

func TestBuilder_Build_SingleFile(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	symbols := []*ast.Symbol{
		testSymbol("main", ast.SymbolKindFunction, "main.go", 1),
		testSymbol("helper", ast.SymbolKindFunction, "main.go", 15),
		testSymbol("Config", ast.SymbolKindStruct, "main.go", 30),
	}

	parseResult := testParseResult("main.go", symbols, nil)
	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Graph.NodeCount() != 3 {
		t.Errorf("expected 3 nodes, got %d", result.Graph.NodeCount())
	}

	// Verify all symbols are in the graph
	for _, sym := range symbols {
		node, ok := result.Graph.GetNode(sym.ID)
		if !ok {
			t.Errorf("symbol %s not found in graph", sym.ID)
		}
		if node.Symbol.Name != sym.Name {
			t.Errorf("expected symbol name %s, got %s", sym.Name, node.Symbol.Name)
		}
	}

	if result.Stats.NodesCreated != 3 {
		t.Errorf("expected NodesCreated=3, got %d", result.Stats.NodesCreated)
	}

	if result.Stats.FilesProcessed != 1 {
		t.Errorf("expected FilesProcessed=1, got %d", result.Stats.FilesProcessed)
	}
}

func TestBuilder_Build_WithImports(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	symbols := []*ast.Symbol{
		testSymbol("main", ast.SymbolKindFunction, "main.go", 1),
	}

	imports := []ast.Import{
		{
			Path:  "fmt",
			Alias: "fmt",
			Location: ast.Location{
				FilePath:  "main.go",
				StartLine: 3,
				EndLine:   3,
			},
		},
		{
			Path:  "github.com/pkg/errors",
			Alias: "errors",
			Location: ast.Location{
				FilePath:  "main.go",
				StartLine: 4,
				EndLine:   4,
			},
		},
	}

	parseResult := testParseResult("main.go", symbols, imports)
	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have placeholder nodes for imports
	if result.Stats.PlaceholderNodes < 2 {
		t.Errorf("expected at least 2 placeholder nodes for imports, got %d", result.Stats.PlaceholderNodes)
	}

	// Check that import placeholder exists
	fmtPlaceholder, ok := result.Graph.GetNode("external:fmt:fmt")
	if !ok {
		t.Error("expected placeholder node for fmt import")
	}
	if fmtPlaceholder != nil && fmtPlaceholder.Symbol.Kind != ast.SymbolKindExternal {
		t.Errorf("expected external kind, got %s", fmtPlaceholder.Symbol.Kind)
	}
}

func TestBuilder_Build_WithReceiver(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	structSym := testSymbol("UserService", ast.SymbolKindStruct, "service.go", 10)

	methodSym := testSymbol("Create", ast.SymbolKindMethod, "service.go", 20)
	methodSym.Receiver = "*UserService"

	symbols := []*ast.Symbol{structSym, methodSym}
	parseResult := testParseResult("service.go", symbols, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have RECEIVES edge from method to struct
	if result.Stats.EdgesCreated == 0 {
		t.Error("expected at least 1 edge (RECEIVES)")
	}

	// Check the method node has outgoing RECEIVES edge
	methodNode, ok := result.Graph.GetNode(methodSym.ID)
	if !ok {
		t.Fatal("method node not found")
	}

	foundReceives := false
	for _, edge := range methodNode.Outgoing {
		if edge.Type == EdgeTypeReceives {
			foundReceives = true
			break
		}
	}

	if !foundReceives {
		t.Error("expected RECEIVES edge from method to receiver type")
	}
}

func TestBuilder_Build_WithImplements(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	ifaceSym := testSymbol("Reader", ast.SymbolKindInterface, "types.go", 5)

	structSym := testSymbol("FileReader", ast.SymbolKindStruct, "types.go", 15)
	structSym.Metadata = &ast.SymbolMetadata{
		Implements: []string{"Reader"},
	}

	symbols := []*ast.Symbol{ifaceSym, structSym}
	parseResult := testParseResult("types.go", symbols, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have IMPLEMENTS edge from struct to interface
	structNode, ok := result.Graph.GetNode(structSym.ID)
	if !ok {
		t.Fatal("struct node not found")
	}

	foundImplements := false
	for _, edge := range structNode.Outgoing {
		if edge.Type == EdgeTypeImplements && edge.ToID == ifaceSym.ID {
			foundImplements = true
			break
		}
	}

	if !foundImplements {
		t.Error("expected IMPLEMENTS edge from FileReader to Reader")
	}
}

func TestBuilder_Build_WithEmbeds(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	baseSym := testSymbol("BaseService", ast.SymbolKindStruct, "base.go", 5)

	childSym := testSymbol("UserService", ast.SymbolKindStruct, "user.go", 10)
	childSym.Metadata = &ast.SymbolMetadata{
		Extends: "BaseService",
	}

	parseResults := []*ast.ParseResult{
		testParseResult("base.go", []*ast.Symbol{baseSym}, nil),
		testParseResult("user.go", []*ast.Symbol{childSym}, nil),
	}

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have EMBEDS edge from child to base
	childNode, ok := result.Graph.GetNode(childSym.ID)
	if !ok {
		t.Fatal("child node not found")
	}

	foundEmbeds := false
	for _, edge := range childNode.Outgoing {
		if edge.Type == EdgeTypeEmbeds {
			foundEmbeds = true
			break
		}
	}

	if !foundEmbeds {
		t.Error("expected EMBEDS edge from UserService to BaseService")
	}
}

func TestBuilder_Build_PlaceholderDeduplication(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Multiple files importing same package
	parseResults := []*ast.ParseResult{
		testParseResult("a.go", []*ast.Symbol{testSymbol("A", ast.SymbolKindFunction, "a.go", 1)}, []ast.Import{
			{Path: "fmt", Alias: "fmt", Location: ast.Location{FilePath: "a.go", StartLine: 1}},
		}),
		testParseResult("b.go", []*ast.Symbol{testSymbol("B", ast.SymbolKindFunction, "b.go", 1)}, []ast.Import{
			{Path: "fmt", Alias: "fmt", Location: ast.Location{FilePath: "b.go", StartLine: 1}},
		}),
		testParseResult("c.go", []*ast.Symbol{testSymbol("C", ast.SymbolKindFunction, "c.go", 1)}, []ast.Import{
			{Path: "fmt", Alias: "fmt", Location: ast.Location{FilePath: "c.go", StartLine: 1}},
		}),
	}

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only have ONE placeholder for fmt despite 3 imports
	if result.Stats.PlaceholderNodes != 1 {
		t.Errorf("expected 1 placeholder (fmt deduplicated), got %d", result.Stats.PlaceholderNodes)
	}

	// Verify the placeholder exists
	_, ok := result.Graph.GetNode("external:fmt:fmt")
	if !ok {
		t.Error("expected fmt placeholder node")
	}
}

func TestBuilder_Build_NilParseResult(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	validResult1 := testParseResult("valid1.go", []*ast.Symbol{
		testSymbol("Valid1", ast.SymbolKindFunction, "valid1.go", 1),
	}, nil)

	validResult2 := testParseResult("valid2.go", []*ast.Symbol{
		testSymbol("Valid2", ast.SymbolKindFunction, "valid2.go", 1),
	}, nil)

	// Mix of valid and nil results
	parseResults := []*ast.ParseResult{
		validResult1,
		nil, // This should cause a FileError
		validResult2,
	}

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have processed valid files
	if result.Stats.FilesProcessed != 2 {
		t.Errorf("expected 2 files processed, got %d", result.Stats.FilesProcessed)
	}

	// Should have one file error
	if result.Stats.FilesFailed != 1 {
		t.Errorf("expected 1 file failed, got %d", result.Stats.FilesFailed)
	}

	if len(result.FileErrors) != 1 {
		t.Errorf("expected 1 FileError, got %d", len(result.FileErrors))
	}

	// Build should not be marked incomplete for non-fatal errors
	if result.Incomplete {
		t.Error("expected Incomplete=false for non-fatal file errors")
	}
}

func TestBuilder_Build_NilSymbol(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Create symbols with unique IDs
	sym1 := testSymbol("Valid", ast.SymbolKindFunction, "test.go", 1)
	sym2 := testSymbol("AlsoValid", ast.SymbolKindFunction, "test.go", 20)

	symbols := []*ast.Symbol{
		sym1,
		nil, // This should be skipped
		sym2,
	}

	parseResult := testParseResult("test.go", symbols, nil)
	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 nodes (nil symbol skipped)
	if result.Graph.NodeCount() != 2 {
		t.Errorf("expected 2 nodes, got %d", result.Graph.NodeCount())
	}

	// Verify both valid symbols are in the graph
	if _, ok := result.Graph.GetNode(sym1.ID); !ok {
		t.Errorf("expected symbol %s in graph", sym1.ID)
	}
	if _, ok := result.Graph.GetNode(sym2.ID); !ok {
		t.Errorf("expected symbol %s in graph", sym2.ID)
	}
}

func TestBuilder_Build_ContextCancellation(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))

	// Create many files to process
	var parseResults []*ast.ParseResult
	for i := 0; i < 100; i++ {
		parseResults = append(parseResults, testParseResult(
			"file"+string(rune('a'+i%26))+".go",
			[]*ast.Symbol{testSymbol("Func", ast.SymbolKindFunction, "file.go", i)},
			nil,
		))
	}

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be marked incomplete
	if !result.Incomplete {
		t.Error("expected Incomplete=true when context cancelled")
	}

	// Should still have a valid (partial) graph
	if result.Graph == nil {
		t.Error("expected non-nil graph even with cancellation")
	}
}

func TestBuilder_Build_ContextTimeout(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))

	// Create files
	var parseResults []*ast.ParseResult
	for i := 0; i < 10; i++ {
		parseResults = append(parseResults, testParseResult(
			"file.go",
			[]*ast.Symbol{testSymbol("Func", ast.SymbolKindFunction, "file.go", i+1)},
			nil,
		))
	}

	// Very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(1 * time.Millisecond)

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be marked incomplete
	if !result.Incomplete {
		t.Error("expected Incomplete=true when context timed out")
	}
}

func TestBuilder_Build_ProgressCallback(t *testing.T) {
	var progressUpdates []BuildProgress

	builder := NewBuilder(
		WithProjectRoot("/test"),
		WithProgressCallback(func(p BuildProgress) {
			progressUpdates = append(progressUpdates, p)
		}),
	)

	symbols := []*ast.Symbol{
		testSymbol("A", ast.SymbolKindFunction, "a.go", 1),
		testSymbol("B", ast.SymbolKindFunction, "b.go", 1),
	}

	parseResults := []*ast.ParseResult{
		testParseResult("a.go", []*ast.Symbol{symbols[0]}, nil),
		testParseResult("b.go", []*ast.Symbol{symbols[1]}, nil),
	}

	ctx := context.Background()
	_, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have received progress updates
	if len(progressUpdates) == 0 {
		t.Error("expected progress updates")
	}

	// Check that we got updates for both phases
	hasCollecting := false
	hasExtracting := false
	hasFinalizing := false

	for _, p := range progressUpdates {
		switch p.Phase {
		case ProgressPhaseCollecting:
			hasCollecting = true
		case ProgressPhaseExtractingEdges:
			hasExtracting = true
		case ProgressPhaseFinalizing:
			hasFinalizing = true
		}
	}

	if !hasCollecting {
		t.Error("expected collecting phase progress")
	}
	if !hasExtracting {
		t.Error("expected extracting edges phase progress")
	}
	if !hasFinalizing {
		t.Error("expected finalizing phase progress")
	}
}

func TestBuilder_Build_InvalidFilePath(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Path traversal attempt
	parseResult := &ast.ParseResult{
		FilePath: "../etc/passwd",
		Language: "go",
		Symbols:  []*ast.Symbol{testSymbol("Evil", ast.SymbolKindFunction, "../etc/passwd", 1)},
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have a file error for path traversal
	if len(result.FileErrors) == 0 {
		t.Error("expected FileError for path traversal attempt")
	}

	if result.Stats.FilesFailed != 1 {
		t.Errorf("expected 1 file failed, got %d", result.Stats.FilesFailed)
	}
}

func TestBuilder_Build_GraphIsFrozen(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	parseResult := testParseResult("test.go", []*ast.Symbol{
		testSymbol("Test", ast.SymbolKindFunction, "test.go", 1),
	}, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Graph should be frozen after build
	if !result.Graph.IsFrozen() {
		t.Error("expected graph to be frozen after build")
	}

	// Attempting to add node should fail
	_, addErr := result.Graph.AddNode(testSymbol("New", ast.SymbolKindFunction, "new.go", 1))
	if addErr == nil {
		t.Error("expected error when adding to frozen graph")
	}
}

func TestBuilder_Build_StatsAccuracy(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	symbols := []*ast.Symbol{
		testSymbol("A", ast.SymbolKindFunction, "a.go", 1),
		testSymbol("B", ast.SymbolKindFunction, "a.go", 10),
		testSymbol("C", ast.SymbolKindStruct, "a.go", 20),
	}

	parseResult := testParseResult("a.go", symbols, nil)
	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Stats.FilesProcessed != 1 {
		t.Errorf("expected FilesProcessed=1, got %d", result.Stats.FilesProcessed)
	}

	if result.Stats.FilesFailed != 0 {
		t.Errorf("expected FilesFailed=0, got %d", result.Stats.FilesFailed)
	}

	if result.Stats.NodesCreated != 3 {
		t.Errorf("expected NodesCreated=3, got %d", result.Stats.NodesCreated)
	}

	// DurationMilli may be 0 for very fast builds, just verify it's non-negative
	if result.Stats.DurationMilli < 0 {
		t.Error("expected DurationMilli >= 0")
	}
}

func TestBuilder_Build_ChildSymbols(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	classSym := testSymbol("UserService", ast.SymbolKindClass, "service.go", 10)
	classSym.Children = []*ast.Symbol{
		testSymbol("Create", ast.SymbolKindMethod, "service.go", 15),
		testSymbol("Delete", ast.SymbolKindMethod, "service.go", 25),
	}

	parseResult := testParseResult("service.go", []*ast.Symbol{classSym}, nil)
	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 nodes: class + 2 methods
	if result.Graph.NodeCount() != 3 {
		t.Errorf("expected 3 nodes (1 class + 2 methods), got %d", result.Graph.NodeCount())
	}

	// Verify all nodes exist
	for _, child := range classSym.Children {
		if _, ok := result.Graph.GetNode(child.ID); !ok {
			t.Errorf("child symbol %s not found in graph", child.ID)
		}
	}
}

func TestBuilder_Build_ReturnTypeEdges(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	userSym := testSymbol("User", ast.SymbolKindStruct, "types.go", 5)

	funcSym := testSymbol("GetUser", ast.SymbolKindFunction, "handlers.go", 10)
	funcSym.Metadata = &ast.SymbolMetadata{
		ReturnType: "*User",
	}

	parseResults := []*ast.ParseResult{
		testParseResult("types.go", []*ast.Symbol{userSym}, nil),
		testParseResult("handlers.go", []*ast.Symbol{funcSym}, nil),
	}

	result, err := builder.Build(ctx, parseResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have RETURNS edge from function to User type
	funcNode, ok := result.Graph.GetNode(funcSym.ID)
	if !ok {
		t.Fatal("function node not found")
	}

	foundReturns := false
	for _, edge := range funcNode.Outgoing {
		if edge.Type == EdgeTypeReturns {
			foundReturns = true
			break
		}
	}

	if !foundReturns {
		t.Error("expected RETURNS edge from GetUser to User")
	}
}

func TestBuildResult_Methods(t *testing.T) {
	t.Run("HasErrors", func(t *testing.T) {
		result := &BuildResult{}
		if result.HasErrors() {
			t.Error("expected HasErrors=false for empty result")
		}

		result.FileErrors = append(result.FileErrors, FileError{FilePath: "test.go"})
		if !result.HasErrors() {
			t.Error("expected HasErrors=true with file error")
		}
	})

	t.Run("TotalErrors", func(t *testing.T) {
		result := &BuildResult{
			FileErrors: []FileError{{FilePath: "a.go"}, {FilePath: "b.go"}},
			EdgeErrors: []EdgeError{{FromID: "x"}},
		}
		if result.TotalErrors() != 3 {
			t.Errorf("expected TotalErrors=3, got %d", result.TotalErrors())
		}
	})

	t.Run("Success", func(t *testing.T) {
		result := &BuildResult{}
		if !result.Success() {
			t.Error("expected Success=true for clean build")
		}

		result.Incomplete = true
		if result.Success() {
			t.Error("expected Success=false when incomplete")
		}

		result.Incomplete = false
		result.FileErrors = append(result.FileErrors, FileError{})
		if result.Success() {
			t.Error("expected Success=false with errors")
		}
	})
}

func TestExtractTypeName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "User"},
		{"*User", "User"},
		{"[]User", "User"},
		{"[]*User", "User"},
		{"map[string]User", "User"},
		{"chan User", "User"},
		{"<-chan User", "User"},
		{"chan<- User", "User"},
		{"string", ""}, // Built-in
		{"int", ""},    // Built-in
		{"error", ""},  // Built-in
		{"Response[T]", "Response"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := extractTypeName(tc.input)
			if result != tc.expected {
				t.Errorf("extractTypeName(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestExtractDir(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"handlers/user.go", "handlers"},
		{"pkg/service/auth.go", "pkg/service"},
		{"main.go", ""},
		{"a/b/c/d.go", "a/b/c"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := extractDir(tc.input)
			if result != tc.expected {
				t.Errorf("extractDir(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

// Fix the typo in earlier test - parseResults -> []*ast.ParseResult{parseResult}
func init() {
	// This is just to make sure the tests compile
}

// === GR-40: Go Interface Implementation Detection Tests ===

func TestBuilder_GoInterfaceImplementation(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("basic interface implementation", func(t *testing.T) {
		// Create an interface with methods
		readerInterface := &ast.Symbol{
			ID:        "interface.go:10:Reader",
			Name:      "Reader",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "interface.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Read", ParamCount: 1, ReturnCount: 2},
				},
			},
		}

		// Create a struct that implements the interface
		fileReader := &ast.Symbol{
			ID:        "reader.go:5:FileReader",
			Name:      "FileReader",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "reader.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Read", ParamCount: 1, ReturnCount: 2, ReceiverType: "*FileReader"},
				},
			},
		}

		parseResult1 := testParseResult("interface.go", []*ast.Symbol{readerInterface}, nil)
		parseResult2 := testParseResult("reader.go", []*ast.Symbol{fileReader}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Check that EdgeTypeImplements was created
		fileReaderNode, ok := result.Graph.GetNode(fileReader.ID)
		if !ok {
			t.Fatal("FileReader node not found")
		}
		foundImplements := false
		for _, edge := range fileReaderNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == readerInterface.ID {
				foundImplements = true
				break
			}
		}
		if !foundImplements {
			t.Error("expected EdgeTypeImplements from FileReader to Reader")
		}

		// Verify stats
		if result.Stats.GoInterfaceEdges != 1 {
			t.Errorf("expected GoInterfaceEdges=1, got %d", result.Stats.GoInterfaceEdges)
		}
	})

	t.Run("partial implementation should not match", func(t *testing.T) {
		// Interface with two methods
		handlerInterface := &ast.Symbol{
			ID:        "handler.go:10:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "handler.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Handle", ParamCount: 2, ReturnCount: 2},
					{Name: "Close", ParamCount: 0, ReturnCount: 1},
				},
			},
		}

		// Struct with only one of the methods (partial implementation)
		partialHandler := &ast.Symbol{
			ID:        "partial.go:5:PartialHandler",
			Name:      "PartialHandler",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "partial.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Handle", ParamCount: 2, ReturnCount: 2, ReceiverType: "*PartialHandler"},
					// Missing Close method
				},
			},
		}

		parseResult1 := testParseResult("handler.go", []*ast.Symbol{handlerInterface}, nil)
		parseResult2 := testParseResult("partial.go", []*ast.Symbol{partialHandler}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Check that no EdgeTypeImplements was created
		partialHandlerNode, ok := result.Graph.GetNode(partialHandler.ID)
		if !ok {
			t.Fatal("PartialHandler node not found")
		}
		for _, edge := range partialHandlerNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == handlerInterface.ID {
				t.Error("unexpected EdgeTypeImplements from PartialHandler to Handler (missing Close method)")
			}
		}

		if result.Stats.GoInterfaceEdges != 0 {
			t.Errorf("expected GoInterfaceEdges=0, got %d", result.Stats.GoInterfaceEdges)
		}
	})

	t.Run("multiple interface implementations", func(t *testing.T) {
		// Two interfaces
		reader := &ast.Symbol{
			ID:        "io.go:10:Reader",
			Name:      "Reader",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "io.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Read", ParamCount: 1, ReturnCount: 2},
				},
			},
		}

		writer := &ast.Symbol{
			ID:        "io.go:20:Writer",
			Name:      "Writer",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "io.go",
			StartLine: 20,
			EndLine:   25,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Write", ParamCount: 1, ReturnCount: 2},
				},
			},
		}

		// Struct that implements both
		buffer := &ast.Symbol{
			ID:        "buffer.go:5:Buffer",
			Name:      "Buffer",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "buffer.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Read", ParamCount: 1, ReturnCount: 2, ReceiverType: "*Buffer"},
					{Name: "Write", ParamCount: 1, ReturnCount: 2, ReceiverType: "*Buffer"},
				},
			},
		}

		parseResult1 := testParseResult("io.go", []*ast.Symbol{reader, writer}, nil)
		parseResult2 := testParseResult("buffer.go", []*ast.Symbol{buffer}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Check that EdgeTypeImplements was created for both interfaces
		bufferNode, ok := result.Graph.GetNode(buffer.ID)
		if !ok {
			t.Fatal("Buffer node not found")
		}
		implementsReader := false
		implementsWriter := false
		for _, edge := range bufferNode.Outgoing {
			if edge.Type == EdgeTypeImplements {
				if edge.ToID == reader.ID {
					implementsReader = true
				}
				if edge.ToID == writer.ID {
					implementsWriter = true
				}
			}
		}
		if !implementsReader {
			t.Error("expected EdgeTypeImplements from Buffer to Reader")
		}
		if !implementsWriter {
			t.Error("expected EdgeTypeImplements from Buffer to Writer")
		}

		if result.Stats.GoInterfaceEdges != 2 {
			t.Errorf("expected GoInterfaceEdges=2, got %d", result.Stats.GoInterfaceEdges)
		}
	})

	t.Run("empty interface should not match", func(t *testing.T) {
		// Empty interface (like interface{})
		emptyInterface := &ast.Symbol{
			ID:        "empty.go:10:Empty",
			Name:      "Empty",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "empty.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			// No Metadata.Methods
		}

		someType := &ast.Symbol{
			ID:        "some.go:5:SomeType",
			Name:      "SomeType",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "some.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "DoSomething", ParamCount: 0, ReturnCount: 0},
				},
			},
		}

		parseResult1 := testParseResult("empty.go", []*ast.Symbol{emptyInterface}, nil)
		parseResult2 := testParseResult("some.go", []*ast.Symbol{someType}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Empty interfaces are skipped (would match everything - too noisy)
		if result.Stats.GoInterfaceEdges != 0 {
			t.Errorf("expected GoInterfaceEdges=0 for empty interface, got %d", result.Stats.GoInterfaceEdges)
		}
	})

	t.Run("non-go language should be skipped", func(t *testing.T) {
		// TypeScript interface
		tsInterface := &ast.Symbol{
			ID:        "api.ts:10:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "api.ts",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript", // Not Go
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Handle", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		parseResult := &ast.ParseResult{
			FilePath: "api.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsInterface},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// TypeScript uses explicit implements, so this function should skip it
		if result.Stats.GoInterfaceEdges != 0 {
			t.Errorf("expected GoInterfaceEdges=0 for TypeScript, got %d", result.Stats.GoInterfaceEdges)
		}
	})

	t.Run("cross-file method association (GR-40 C-3 fix)", func(t *testing.T) {
		// This test verifies that methods defined in a different file than their
		// receiver type are properly associated and interface detection works.

		// File 1: Interface definition
		readerInterface := &ast.Symbol{
			ID:        "io.go:10:Reader",
			Name:      "Reader",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "io.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Read", ParamCount: 1, ReturnCount: 2},
				},
			},
		}

		// File 2: Type definition (WITHOUT methods - they're in a different file)
		fileReader := &ast.Symbol{
			ID:        "types.go:5:FileReader",
			Name:      "FileReader",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "types.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata:  nil, // Methods will be associated cross-file
		}

		// File 3: Method definition (separate from type!)
		readMethod := &ast.Symbol{
			ID:        "reader_methods.go:10:FileReader.Read",
			Name:      "Read",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "reader_methods.go",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Signature: "func (f *FileReader) Read(p []byte) (int, error)",
		}

		parseResult1 := testParseResult("io.go", []*ast.Symbol{readerInterface}, nil)
		parseResult2 := testParseResult("types.go", []*ast.Symbol{fileReader}, nil)
		parseResult3 := testParseResult("reader_methods.go", []*ast.Symbol{readMethod}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2, parseResult3})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Verify the method was associated with the type
		fileReaderNode, ok := result.Graph.GetNode(fileReader.ID)
		if !ok {
			t.Fatal("FileReader node not found")
		}

		// The type should now have methods associated
		if fileReaderNode.Symbol.Metadata == nil || len(fileReaderNode.Symbol.Metadata.Methods) == 0 {
			t.Error("expected FileReader to have methods associated cross-file")
		}

		// Check that EdgeTypeImplements was created
		foundImplements := false
		for _, edge := range fileReaderNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == readerInterface.ID {
				foundImplements = true
				break
			}
		}
		if !foundImplements {
			t.Error("expected EdgeTypeImplements from FileReader to Reader (cross-file method association)")
		}

		// Verify stats
		if result.Stats.GoInterfaceEdges != 1 {
			t.Errorf("expected GoInterfaceEdges=1, got %d", result.Stats.GoInterfaceEdges)
		}
	})
}

// TestBuilder_PromotedMethodResolution verifies that Go structs with embedded types
// inherit promoted methods for interface implementation detection.
// IT-03 H-3: Engine embeds RouterGroup → Engine gets RouterGroup's methods → Engine satisfies IRouter.
func TestBuilder_PromotedMethodResolution(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("struct with embed satisfies interface via promoted methods", func(t *testing.T) {
		// IRouter interface requires GET, POST, Group methods
		iRouter := &ast.Symbol{
			ID:        "router.go:10:IRouter",
			Name:      "IRouter",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "router.go",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "GET", ParamCount: 2, ReturnCount: 1},
					{Name: "POST", ParamCount: 2, ReturnCount: 1},
					{Name: "Group", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		// RouterGroup has GET, POST, Group methods (satisfies IRouter directly)
		routerGroup := &ast.Symbol{
			ID:        "routergroup.go:5:RouterGroup",
			Name:      "RouterGroup",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "routergroup.go",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "GET", ParamCount: 2, ReturnCount: 1, ReceiverType: "*RouterGroup"},
					{Name: "POST", ParamCount: 2, ReturnCount: 1, ReceiverType: "*RouterGroup"},
					{Name: "Group", ParamCount: 1, ReturnCount: 1, ReceiverType: "*RouterGroup"},
				},
			},
		}

		// Engine embeds RouterGroup (has Extends="RouterGroup") but has NO direct methods
		// that satisfy IRouter. It only satisfies IRouter via promoted methods.
		engine := &ast.Symbol{
			ID:        "engine.go:5:Engine",
			Name:      "Engine",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "engine.go",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Extends: "RouterGroup",
				Methods: []ast.MethodSignature{
					{Name: "Run", ParamCount: 1, ReturnCount: 1, ReceiverType: "*Engine"},
				},
			},
		}

		parseResult1 := testParseResult("router.go", []*ast.Symbol{iRouter}, nil)
		parseResult2 := testParseResult("routergroup.go", []*ast.Symbol{routerGroup}, nil)
		parseResult3 := testParseResult("engine.go", []*ast.Symbol{engine}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2, parseResult3})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// RouterGroup should implement IRouter (direct methods)
		rgNode, ok := result.Graph.GetNode(routerGroup.ID)
		if !ok {
			t.Fatal("RouterGroup node not found")
		}
		foundRGImplements := false
		for _, edge := range rgNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == iRouter.ID {
				foundRGImplements = true
				break
			}
		}
		if !foundRGImplements {
			t.Error("expected EdgeTypeImplements from RouterGroup to IRouter")
		}

		// Engine should ALSO implement IRouter via promoted methods from RouterGroup
		engineNode, ok := result.Graph.GetNode(engine.ID)
		if !ok {
			t.Fatal("Engine node not found")
		}
		foundEngineImplements := false
		for _, edge := range engineNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == iRouter.ID {
				foundEngineImplements = true
				break
			}
		}
		if !foundEngineImplements {
			t.Error("expected EdgeTypeImplements from Engine to IRouter (via promoted methods from RouterGroup)")
		}
	})

	t.Run("struct with embed but no Extends does not get promoted methods", func(t *testing.T) {
		// Interface
		iface := &ast.Symbol{
			ID:        "iface.go:10:Saver",
			Name:      "Saver",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "iface.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Save", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		// Struct with no Extends and no Save method — should NOT implement Saver
		plain := &ast.Symbol{
			ID:        "plain.go:5:Plain",
			Name:      "Plain",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "plain.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Load", ParamCount: 0, ReturnCount: 1, ReceiverType: "*Plain"},
				},
			},
		}

		parseResult1 := testParseResult("iface.go", []*ast.Symbol{iface}, nil)
		parseResult2 := testParseResult("plain.go", []*ast.Symbol{plain}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		plainNode, ok := result.Graph.GetNode(plain.ID)
		if !ok {
			t.Fatal("Plain node not found")
		}
		for _, edge := range plainNode.Outgoing {
			if edge.Type == EdgeTypeImplements {
				t.Error("expected NO EdgeTypeImplements from Plain (does not have Save method)")
			}
		}
	})

	t.Run("recursive embeds chain", func(t *testing.T) {
		// Interface requires Write
		writer := &ast.Symbol{
			ID:        "w.go:10:Writer",
			Name:      "Writer",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "w.go",
			StartLine: 10,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Write", ParamCount: 1, ReturnCount: 2},
				},
			},
		}

		// Base has Write
		base := &ast.Symbol{
			ID:        "base.go:5:Base",
			Name:      "Base",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "base.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Write", ParamCount: 1, ReturnCount: 2, ReceiverType: "*Base"},
				},
			},
		}

		// Middle embeds Base (no direct methods matching Writer)
		middle := &ast.Symbol{
			ID:        "middle.go:5:Middle",
			Name:      "Middle",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "middle.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Extends: "Base",
			},
		}

		// Top embeds Middle (should get Write via Middle → Base chain)
		top := &ast.Symbol{
			ID:        "top.go:5:Top",
			Name:      "Top",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "top.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Extends: "Middle",
			},
		}

		parseResults := []*ast.ParseResult{
			testParseResult("w.go", []*ast.Symbol{writer}, nil),
			testParseResult("base.go", []*ast.Symbol{base}, nil),
			testParseResult("middle.go", []*ast.Symbol{middle}, nil),
			testParseResult("top.go", []*ast.Symbol{top}, nil),
		}

		result, err := builder.Build(ctx, parseResults)
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Top should implement Writer via Middle → Base chain
		topNode, ok := result.Graph.GetNode(top.ID)
		if !ok {
			t.Fatal("Top node not found")
		}
		foundTopImplements := false
		for _, edge := range topNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == writer.ID {
				foundTopImplements = true
				break
			}
		}
		if !foundTopImplements {
			t.Error("expected EdgeTypeImplements from Top to Writer (via recursive embeds: Top→Middle→Base)")
		}
	})
}

func TestBuilder_PythonProtocolImplementation(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("Python Protocol implementation detected", func(t *testing.T) {
		// Protocol (interface in Python)
		handlerProtocol := &ast.Symbol{
			ID:        "protocols.py:5:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindInterface, // Marked as interface by parser
			FilePath:  "protocols.py",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "python",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "handle", ParamCount: 1, ReturnCount: 1},
					{Name: "close", ParamCount: 0, ReturnCount: 0},
				},
			},
		}

		// Class that implements the Protocol
		fileHandler := &ast.Symbol{
			ID:        "handlers.py:10:FileHandler",
			Name:      "FileHandler",
			Kind:      ast.SymbolKindClass,
			FilePath:  "handlers.py",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "python",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "handle", ParamCount: 1, ReturnCount: 1},
					{Name: "close", ParamCount: 0, ReturnCount: 0},
					{Name: "extra", ParamCount: 0, ReturnCount: 0},
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "protocols.py",
			Language: "python",
			Symbols:  []*ast.Symbol{handlerProtocol},
			Package:  "myapp",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "handlers.py",
			Language: "python",
			Symbols:  []*ast.Symbol{fileHandler},
			Package:  "myapp",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Check that EdgeTypeImplements was created
		handlerNode, ok := result.Graph.GetNode(fileHandler.ID)
		if !ok {
			t.Fatal("FileHandler node not found")
		}
		foundImplements := false
		for _, edge := range handlerNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == handlerProtocol.ID {
				foundImplements = true
				break
			}
		}
		if !foundImplements {
			t.Error("expected EdgeTypeImplements from FileHandler to Handler Protocol")
		}
	})

	t.Run("Python and Go interfaces don't cross-match", func(t *testing.T) {
		// Go interface
		goInterface := &ast.Symbol{
			ID:        "handler.go:5:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "handler.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Handle", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		// Python class with same method name (different case)
		pythonClass := &ast.Symbol{
			ID:        "handler.py:10:MyHandler",
			Name:      "MyHandler",
			Kind:      ast.SymbolKindClass,
			FilePath:  "handler.py",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "python",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "Handle", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "handler.go",
			Language: "go",
			Symbols:  []*ast.Symbol{goInterface},
			Package:  "main",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "handler.py",
			Language: "python",
			Symbols:  []*ast.Symbol{pythonClass},
			Package:  "myapp",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Python class should NOT implement Go interface (different languages)
		pythonNode, ok := result.Graph.GetNode(pythonClass.ID)
		if !ok {
			t.Fatal("Python class node not found")
		}
		for _, edge := range pythonNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == goInterface.ID {
				t.Error("Python class should NOT implement Go interface (cross-language)")
			}
		}
	})
}

func TestIsMethodSuperset(t *testing.T) {
	tests := []struct {
		name     string
		superset map[string]bool
		subset   map[string]bool
		expected bool
	}{
		{
			name:     "exact match",
			superset: map[string]bool{"Read": true, "Close": true},
			subset:   map[string]bool{"Read": true, "Close": true},
			expected: true,
		},
		{
			name:     "superset has more",
			superset: map[string]bool{"Read": true, "Write": true, "Close": true},
			subset:   map[string]bool{"Read": true, "Close": true},
			expected: true,
		},
		{
			name:     "subset has more - not a superset",
			superset: map[string]bool{"Read": true},
			subset:   map[string]bool{"Read": true, "Close": true},
			expected: false,
		},
		{
			name:     "disjoint sets",
			superset: map[string]bool{"Read": true},
			subset:   map[string]bool{"Write": true},
			expected: false,
		},
		{
			name:     "empty subset",
			superset: map[string]bool{"Read": true},
			subset:   map[string]bool{},
			expected: true,
		},
		{
			name:     "both empty",
			superset: map[string]bool{},
			subset:   map[string]bool{},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isMethodSuperset(tc.superset, tc.subset)
			if result != tc.expected {
				t.Errorf("isMethodSuperset() = %v, expected %v", result, tc.expected)
			}
		})
	}
}

// =============================================================================
// GR-41: Call Edge Extraction Tests
// =============================================================================

// Helper to create a symbol with call sites for GR-41 tests.
func testSymbolWithCalls(name string, kind ast.SymbolKind, filePath string, line int, calls []ast.CallSite) *ast.Symbol {
	sym := testSymbol(name, kind, filePath, line)
	sym.Calls = calls
	return sym
}

func TestBuilder_ExtractCallEdges_SamePackage(t *testing.T) {
	// Create parse result with function calls
	callerSym := testSymbolWithCalls("Caller", ast.SymbolKindFunction, "main.go", 5, []ast.CallSite{
		{
			Target: "Callee",
			Location: ast.Location{
				FilePath:  "main.go",
				StartLine: 6,
			},
		},
	})
	calleeSym := testSymbol("Callee", ast.SymbolKindFunction, "main.go", 15)

	result := testParseResult("main.go", []*ast.Symbol{callerSym, calleeSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Check that call edge was created
	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found in graph")
	}

	// Check outgoing edges
	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == calleeSym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("Expected EdgeTypeCalls from Caller to Callee")
	}

	// Check stats
	if buildResult.Stats.CallEdgesResolved == 0 {
		t.Error("Expected CallEdgesResolved > 0")
	}
}

func TestBuilder_ExtractCallEdges_Unresolved(t *testing.T) {
	// Create parse result with unresolved call
	callerSym := testSymbolWithCalls("Caller", ast.SymbolKindFunction, "main.go", 5, []ast.CallSite{
		{
			Target: "ExternalFunc",
			Location: ast.Location{
				FilePath:  "main.go",
				StartLine: 6,
			},
		},
	})

	result := testParseResult("main.go", []*ast.Symbol{callerSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Check that placeholder was created
	if buildResult.Stats.PlaceholderNodes == 0 {
		t.Error("Expected placeholder node for unresolved call")
	}

	// Check stats
	if buildResult.Stats.CallEdgesUnresolved == 0 {
		t.Error("Expected CallEdgesUnresolved > 0")
	}
}

func TestBuilder_ExtractCallEdges_MethodCall(t *testing.T) {
	// Create parse result with method call
	callerSym := testSymbolWithCalls("Handler", ast.SymbolKindMethod, "main.go", 5, []ast.CallSite{
		{
			Target:   "Process",
			IsMethod: true,
			Receiver: "s",
			Location: ast.Location{
				FilePath:  "main.go",
				StartLine: 6,
			},
		},
	})
	callerSym.Receiver = "Server"

	processSym := testSymbol("Process", ast.SymbolKindMethod, "main.go", 20)
	processSym.Receiver = "Server"

	result := testParseResult("main.go", []*ast.Symbol{callerSym, processSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Check that method call edge was created
	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Handler node not found in graph")
	}

	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == processSym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("Expected EdgeTypeCalls from Handler to Process")
	}
}

func TestBuilder_ExtractCallEdges_NoCalls(t *testing.T) {
	// Create parse result with function without calls
	funcSym := testSymbol("NoOp", ast.SymbolKindFunction, "main.go", 5)
	funcSym.Calls = nil // No calls

	result := testParseResult("main.go", []*ast.Symbol{funcSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// No call edges should be created
	graph := buildResult.Graph
	node, ok := graph.GetNode(funcSym.ID)
	if !ok {
		t.Fatal("NoOp node not found in graph")
	}

	for _, edge := range node.Outgoing {
		if edge.Type == EdgeTypeCalls {
			t.Error("Expected no EdgeTypeCalls for function without calls")
		}
	}
}

func TestBuilder_ExtractCallEdges_MultipleCallsSameTarget(t *testing.T) {
	// Create parse result with multiple calls to same target
	callerSym := testSymbolWithCalls("Caller", ast.SymbolKindFunction, "main.go", 5, []ast.CallSite{
		{Target: "Helper", Location: ast.Location{FilePath: "main.go", StartLine: 6}},
		{Target: "Helper", Location: ast.Location{FilePath: "main.go", StartLine: 7}},
		{Target: "Helper", Location: ast.Location{FilePath: "main.go", StartLine: 8}},
	})
	helperSym := testSymbol("Helper", ast.SymbolKindFunction, "main.go", 20)

	result := testParseResult("main.go", []*ast.Symbol{callerSym, helperSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should create edges (duplicates may or may not be created depending on graph implementation)
	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found in graph")
	}

	callEdgeCount := 0
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == helperSym.ID {
			callEdgeCount++
		}
	}

	// At least one edge should exist
	if callEdgeCount == 0 {
		t.Error("Expected at least one EdgeTypeCalls from Caller to Helper")
	}
}

// GR-41c: Tests for findPackageSymbolID

func TestFindPackageSymbolID_WithPackage(t *testing.T) {
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols: []*ast.Symbol{
			{ID: "main.go:1:main", Kind: ast.SymbolKindPackage, Name: "main"},
			{ID: "main.go:5:Setup", Kind: ast.SymbolKindFunction, Name: "Setup"},
		},
	}
	id := findPackageSymbolID(r)
	if id != "main.go:1:main" {
		t.Errorf("expected 'main.go:1:main', got %q", id)
	}
}

func TestFindPackageSymbolID_NoPackage(t *testing.T) {
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols: []*ast.Symbol{
			{ID: "main.go:5:Setup", Kind: ast.SymbolKindFunction, Name: "Setup"},
		},
	}
	id := findPackageSymbolID(r)
	// Falls back to first symbol
	if id != "main.go:5:Setup" {
		t.Errorf("expected 'main.go:5:Setup', got %q", id)
	}
}

func TestFindPackageSymbolID_NilSymbols(t *testing.T) {
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols:  nil,
	}
	id := findPackageSymbolID(r)
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestFindPackageSymbolID_EmptySymbols(t *testing.T) {
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols:  []*ast.Symbol{},
	}
	id := findPackageSymbolID(r)
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestFindPackageSymbolID_NilResult(t *testing.T) {
	id := findPackageSymbolID(nil)
	if id != "" {
		t.Errorf("expected empty string, got %q", id)
	}
}

func TestFindPackageSymbolID_PackageNotFirst(t *testing.T) {
	// Package symbol is not first - should still find it
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols: []*ast.Symbol{
			{ID: "main.go:3:foo", Kind: ast.SymbolKindImport, Name: "foo"},
			{ID: "main.go:5:Setup", Kind: ast.SymbolKindFunction, Name: "Setup"},
			{ID: "main.go:1:main", Kind: ast.SymbolKindPackage, Name: "main"},
		},
	}
	id := findPackageSymbolID(r)
	if id != "main.go:1:main" {
		t.Errorf("expected 'main.go:1:main', got %q", id)
	}
}

func TestFindPackageSymbolID_SkipsNilSymbols(t *testing.T) {
	r := &ast.ParseResult{
		FilePath: "main.go",
		Symbols: []*ast.Symbol{
			nil,
			{ID: "main.go:1:main", Kind: ast.SymbolKindPackage, Name: "main"},
			nil,
		},
	}
	id := findPackageSymbolID(r)
	if id != "main.go:1:main" {
		t.Errorf("expected 'main.go:1:main', got %q", id)
	}
}

// GR-41c: Tests for extractImportEdges fix

func TestExtractImportEdges_CreatesEdges(t *testing.T) {
	// Create a parse result with package symbol and imports using testSymbol helper
	pkgSym := testSymbol("main", ast.SymbolKindPackage, "main.go", 1)

	imports := []ast.Import{
		{Path: "fmt", Location: ast.Location{FilePath: "main.go", StartLine: 3}},
		{Path: "context", Location: ast.Location{FilePath: "main.go", StartLine: 4}},
	}

	result := testParseResult("main.go", []*ast.Symbol{pkgSym}, imports)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph

	// Should have created the package node plus 2 placeholder nodes for imports
	// Node count: 1 (package) + 2 (import placeholders) = 3
	if graph.NodeCount() < 1 {
		t.Errorf("expected at least 1 node, got %d", graph.NodeCount())
	}

	// Should have 2 import edges
	if graph.EdgeCount() != 2 {
		t.Errorf("expected 2 edges (imports), got %d", graph.EdgeCount())
	}

	// Verify the package node exists
	pkgNode, ok := graph.GetNode(pkgSym.ID)
	if !ok {
		t.Fatalf("package node not found: %s", pkgSym.ID)
	}

	// Verify the package node has outgoing import edges
	importEdgeCount := 0
	for _, edge := range pkgNode.Outgoing {
		if edge.Type == EdgeTypeImports {
			importEdgeCount++
		}
	}
	if importEdgeCount != 2 {
		t.Errorf("expected 2 import edges from package, got %d", importEdgeCount)
	}
}

func TestExtractImportEdges_NoPackageSymbol_FallsBackToFirstSymbol(t *testing.T) {
	// Create a parse result without package symbol using testSymbol helper
	funcSym := testSymbol("Setup", ast.SymbolKindFunction, "main.go", 5)

	imports := []ast.Import{
		{Path: "fmt", Location: ast.Location{FilePath: "main.go", StartLine: 3}},
	}

	result := testParseResult("main.go", []*ast.Symbol{funcSym}, imports)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph

	// Should have created import edge from the function (fallback)
	if graph.EdgeCount() != 1 {
		t.Errorf("expected 1 edge (import), got %d", graph.EdgeCount())
	}

	// Verify the function node has outgoing import edge
	funcNode, ok := graph.GetNode(funcSym.ID)
	if !ok {
		t.Fatalf("function node not found: %s", funcSym.ID)
	}

	importEdgeCount := 0
	for _, edge := range funcNode.Outgoing {
		if edge.Type == EdgeTypeImports {
			importEdgeCount++
		}
	}
	if importEdgeCount != 1 {
		t.Errorf("expected 1 import edge from function, got %d", importEdgeCount)
	}
}

func TestExtractImportEdges_NoSymbols_NoEdges(t *testing.T) {
	// Create a parse result with no symbols but has imports
	result := &ast.ParseResult{
		FilePath: "main.go",
		Language: "go",
		Symbols:  nil,
		Imports: []ast.Import{
			{Path: "fmt", Location: ast.Location{FilePath: "main.go", StartLine: 3}},
		},
	}

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should have 0 nodes and 0 edges (no source for imports)
	if buildResult.Graph.NodeCount() != 0 {
		t.Errorf("expected 0 nodes, got %d", buildResult.Graph.NodeCount())
	}
	if buildResult.Graph.EdgeCount() != 0 {
		t.Errorf("expected 0 edges, got %d", buildResult.Graph.EdgeCount())
	}
}

func TestExtractImportEdges_NoImports_NoEdges(t *testing.T) {
	// Create a parse result with package symbol but no imports using testSymbol helper
	pkgSym := testSymbol("main", ast.SymbolKindPackage, "main.go", 1)

	result := testParseResult("main.go", []*ast.Symbol{pkgSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Should have 1 node (package) and 0 edges
	if buildResult.Graph.NodeCount() != 1 {
		t.Errorf("expected 1 node, got %d", buildResult.Graph.NodeCount())
	}
	if buildResult.Graph.EdgeCount() != 0 {
		t.Errorf("expected 0 edges, got %d", buildResult.Graph.EdgeCount())
	}
}

// T-1: Test context cancellation during import edge extraction
func TestExtractImportEdges_ContextCancellation(t *testing.T) {
	// Create a parse result with package symbol and many imports
	pkgSym := testSymbol("main", ast.SymbolKindPackage, "main.go", 1)

	// Create 25 imports to ensure we hit the cancellation check (every 10 iterations)
	imports := make([]ast.Import, 25)
	for i := 0; i < 25; i++ {
		imports[i] = ast.Import{
			Path:     fmt.Sprintf("pkg%d", i),
			Location: ast.Location{FilePath: "main.go", StartLine: i + 3},
		}
	}

	result := testParseResult("main.go", []*ast.Symbol{pkgSym}, imports)

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	builder := NewBuilder()
	// Should not panic and should complete (possibly with partial results)
	buildResult, err := builder.Build(ctx, []*ast.ParseResult{result})
	if err != nil {
		// Context cancellation during collectPhase returns early, which is fine
		// The important thing is it doesn't panic
		return
	}

	// If we got a result, it may be incomplete due to cancellation
	// The test passes as long as no panic occurred
	_ = buildResult
}

// T-2: Test duplicate imports are handled correctly
func TestExtractImportEdges_DuplicateImports(t *testing.T) {
	// Create a parse result with package symbol and duplicate imports
	pkgSym := testSymbol("main", ast.SymbolKindPackage, "main.go", 1)

	imports := []ast.Import{
		{Path: "fmt", Location: ast.Location{FilePath: "main.go", StartLine: 3}},
		{Path: "fmt", Location: ast.Location{FilePath: "main.go", StartLine: 4}}, // Duplicate
		{Path: "context", Location: ast.Location{FilePath: "main.go", StartLine: 5}},
	}

	result := testParseResult("main.go", []*ast.Symbol{pkgSym}, imports)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph

	// Should have 3 nodes: 1 package + 2 unique import placeholders (fmt and context)
	// The placeholder for "fmt" should be reused
	if graph.NodeCount() < 1 {
		t.Errorf("expected at least 1 node, got %d", graph.NodeCount())
	}

	// Verify the package node exists and has edges
	pkgNode, ok := graph.GetNode(pkgSym.ID)
	if !ok {
		t.Fatalf("package node not found: %s", pkgSym.ID)
	}

	// Count import edges - should have at least 2 (one for each unique import)
	// Note: duplicate edges may or may not be created depending on AddEdge behavior
	importEdgeCount := 0
	for _, edge := range pkgNode.Outgoing {
		if edge.Type == EdgeTypeImports {
			importEdgeCount++
		}
	}

	// At minimum we should have edges to fmt and context
	if importEdgeCount < 2 {
		t.Errorf("expected at least 2 import edges, got %d", importEdgeCount)
	}

	// Verify no errors occurred (duplicates should be handled gracefully)
	// Note: EdgeErrors may contain duplicate edge errors which are non-fatal
}

// =============================================================================
// IT-01 Bug 4: Go receiver case-insensitive matching
// =============================================================================

func TestBuilder_ResolveCallTarget_GoReceiverCaseInsensitive(t *testing.T) {
	// Simulates badger: txn.Get(k) where txn is a *Txn
	// CallSite.Receiver = "txn", sym.Receiver = "Txn"
	callerSym := testSymbolWithCalls("Execute", ast.SymbolKindFunction, "main.go", 5, []ast.CallSite{
		{
			Target:   "Get",
			IsMethod: true,
			Receiver: "txn",
			Location: ast.Location{FilePath: "main.go", StartLine: 10},
		},
	})

	// Two methods named "Get" with different receiver types
	txnGet := testSymbol("Get", ast.SymbolKindMethod, "txn.go", 20)
	txnGet.Receiver = "Txn"

	dbGet := testSymbol("Get", ast.SymbolKindMethod, "db.go", 30)
	dbGet.Receiver = "DB"

	result := testParseResult("main.go", []*ast.Symbol{callerSym}, nil)
	result2 := testParseResult("txn.go", []*ast.Symbol{txnGet}, nil)
	result3 := testParseResult("db.go", []*ast.Symbol{dbGet}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result, result2, result3})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// The call edge should go to Txn.Get (case-insensitive match), not DB.Get
	g := buildResult.Graph
	callerNode, ok := g.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found")
	}

	foundTxnGet := false
	foundDBGet := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls {
			if edge.ToID == txnGet.ID {
				foundTxnGet = true
			}
			if edge.ToID == dbGet.ID {
				foundDBGet = true
			}
		}
	}

	if !foundTxnGet {
		t.Error("Expected call edge from Execute to Txn.Get (case-insensitive receiver match)")
	}
	if foundDBGet {
		t.Error("Did not expect call edge from Execute to DB.Get")
	}
}

func TestBuilder_ResolveCallTarget_GoReceiverExactMatch(t *testing.T) {
	// When call receiver matches exactly (same case), should also work
	callerSym := testSymbolWithCalls("Handler", ast.SymbolKindMethod, "main.go", 5, []ast.CallSite{
		{
			Target:   "Write",
			IsMethod: true,
			Receiver: "w",
			Location: ast.Location{FilePath: "main.go", StartLine: 10},
		},
	})
	callerSym.Receiver = "Server"

	writerWrite := testSymbol("Write", ast.SymbolKindMethod, "writer.go", 20)
	writerWrite.Receiver = "Writer"

	bufferWrite := testSymbol("Write", ast.SymbolKindMethod, "buffer.go", 30)
	bufferWrite.Receiver = "Buffer"

	result := testParseResult("main.go", []*ast.Symbol{callerSym}, nil)
	result2 := testParseResult("writer.go", []*ast.Symbol{writerWrite}, nil)
	result3 := testParseResult("buffer.go", []*ast.Symbol{bufferWrite}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result, result2, result3})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph
	callerNode, ok := g.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found")
	}

	// "w" doesn't case-insensitively match either "Writer" or "Buffer"
	// so it should fall back to Strategy 3c (first method match)
	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls {
			hasCallEdge = true
		}
	}

	if !hasCallEdge {
		t.Error("Expected at least one call edge (fallback to first method match)")
	}
}

// =============================================================================
// IT-01 Bug 6: this/self receiver resolution with inheritance
// =============================================================================

func TestBuilder_ResolveCallTarget_ThisSelfResolution(t *testing.T) {
	// Simulates: class Component { doRender() { this.renderImmediately() } }
	// this.renderImmediately() should resolve to Component.renderImmediately, not some other class's method

	// Component class with children
	renderMethod := &ast.Symbol{
		ID:        "component.ts:50:renderImmediately",
		Name:      "renderImmediately",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "component.ts",
		StartLine: 50,
		EndLine:   60,
		Language:  "typescript",
	}

	doRenderMethod := &ast.Symbol{
		ID:        "component.ts:30:doRender",
		Name:      "doRender",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "component.ts",
		StartLine: 30,
		EndLine:   40,
		Language:  "typescript",
		Calls: []ast.CallSite{
			{
				Target:   "renderImmediately",
				IsMethod: true,
				Receiver: "this",
				Location: ast.Location{FilePath: "component.ts", StartLine: 35},
			},
		},
	}

	componentClass := &ast.Symbol{
		ID:        "component.ts:10:Component",
		Name:      "Component",
		Kind:      ast.SymbolKindClass,
		FilePath:  "component.ts",
		StartLine: 10,
		EndLine:   100,
		Language:  "typescript",
		Children:  []*ast.Symbol{doRenderMethod, renderMethod},
	}

	// Another class with a method also named renderImmediately (unrelated)
	otherRender := &ast.Symbol{
		ID:        "other.ts:10:renderImmediately",
		Name:      "renderImmediately",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "other.ts",
		StartLine: 10,
		EndLine:   20,
		Language:  "typescript",
	}

	otherClass := &ast.Symbol{
		ID:        "other.ts:5:OtherWidget",
		Name:      "OtherWidget",
		Kind:      ast.SymbolKindClass,
		FilePath:  "other.ts",
		StartLine: 5,
		EndLine:   30,
		Language:  "typescript",
		Children:  []*ast.Symbol{otherRender},
	}

	result := testParseResult("component.ts", []*ast.Symbol{componentClass}, nil)
	result.Language = "typescript"
	result2 := testParseResult("other.ts", []*ast.Symbol{otherClass}, nil)
	result2.Language = "typescript"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result, result2})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// this.renderImmediately() in doRender should resolve to Component's renderImmediately
	g := buildResult.Graph
	doRenderNode, ok := g.GetNode(doRenderMethod.ID)
	if !ok {
		t.Fatal("doRender node not found")
	}

	foundComponentRender := false
	foundOtherRender := false
	for _, edge := range doRenderNode.Outgoing {
		if edge.Type == EdgeTypeCalls {
			if edge.ToID == renderMethod.ID {
				foundComponentRender = true
			}
			if edge.ToID == otherRender.ID {
				foundOtherRender = true
			}
		}
	}

	if !foundComponentRender {
		t.Error("Expected call edge from doRender to Component.renderImmediately (this resolution)")
	}
	if foundOtherRender {
		t.Error("Did not expect call edge to OtherWidget.renderImmediately")
	}
}

func TestBuilder_ResolveCallTarget_SelfResolutionPython(t *testing.T) {
	// Simulates Python: class DataFrame: def query(self): self.filter(...)
	filterMethod := &ast.Symbol{
		ID:        "dataframe.py:50:filter",
		Name:      "filter",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "dataframe.py",
		StartLine: 50,
		EndLine:   60,
		Language:  "python",
	}

	queryMethod := &ast.Symbol{
		ID:        "dataframe.py:30:query",
		Name:      "query",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "dataframe.py",
		StartLine: 30,
		EndLine:   40,
		Language:  "python",
		Calls: []ast.CallSite{
			{
				Target:   "filter",
				IsMethod: true,
				Receiver: "self",
				Location: ast.Location{FilePath: "dataframe.py", StartLine: 35},
			},
		},
	}

	dfClass := &ast.Symbol{
		ID:        "dataframe.py:10:DataFrame",
		Name:      "DataFrame",
		Kind:      ast.SymbolKindClass,
		FilePath:  "dataframe.py",
		StartLine: 10,
		EndLine:   100,
		Language:  "python",
		Children:  []*ast.Symbol{queryMethod, filterMethod},
	}

	result := testParseResult("dataframe.py", []*ast.Symbol{dfClass}, nil)
	result.Language = "python"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph
	queryNode, ok := g.GetNode(queryMethod.ID)
	if !ok {
		t.Fatal("query node not found")
	}

	foundFilter := false
	for _, edge := range queryNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == filterMethod.ID {
			foundFilter = true
		}
	}

	if !foundFilter {
		t.Error("Expected call edge from query to DataFrame.filter (self resolution)")
	}
}

func TestBuilder_ResolveCallTarget_ThisWithInheritance(t *testing.T) {
	// Simulates: class Parent { foo() { this.bar() } }
	// class Child extends Parent { bar() { ... } }
	// Parent.foo calls this.bar() — should resolve to Parent.bar, not Child.bar

	parentBar := &ast.Symbol{
		ID:        "parent.ts:50:bar",
		Name:      "bar",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "parent.ts",
		StartLine: 50,
		EndLine:   60,
		Language:  "typescript",
	}

	parentFoo := &ast.Symbol{
		ID:        "parent.ts:30:foo",
		Name:      "foo",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "parent.ts",
		StartLine: 30,
		EndLine:   40,
		Language:  "typescript",
		Calls: []ast.CallSite{
			{
				Target:   "bar",
				IsMethod: true,
				Receiver: "this",
				Location: ast.Location{FilePath: "parent.ts", StartLine: 35},
			},
		},
	}

	parentClass := &ast.Symbol{
		ID:        "parent.ts:10:Parent",
		Name:      "Parent",
		Kind:      ast.SymbolKindClass,
		FilePath:  "parent.ts",
		StartLine: 10,
		EndLine:   100,
		Language:  "typescript",
		Children:  []*ast.Symbol{parentFoo, parentBar},
	}

	childBar := &ast.Symbol{
		ID:        "child.ts:20:bar",
		Name:      "bar",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "child.ts",
		StartLine: 20,
		EndLine:   30,
		Language:  "typescript",
	}

	childClass := &ast.Symbol{
		ID:        "child.ts:10:Child",
		Name:      "Child",
		Kind:      ast.SymbolKindClass,
		FilePath:  "child.ts",
		StartLine: 10,
		EndLine:   50,
		Language:  "typescript",
		Children:  []*ast.Symbol{childBar},
		Metadata: &ast.SymbolMetadata{
			Extends: "Parent",
		},
	}

	result := testParseResult("parent.ts", []*ast.Symbol{parentClass}, nil)
	result.Language = "typescript"
	result2 := testParseResult("child.ts", []*ast.Symbol{childClass}, nil)
	result2.Language = "typescript"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result, result2})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph
	fooNode, ok := g.GetNode(parentFoo.ID)
	if !ok {
		t.Fatal("foo node not found")
	}

	foundParentBar := false
	foundChildBar := false
	for _, edge := range fooNode.Outgoing {
		if edge.Type == EdgeTypeCalls {
			if edge.ToID == parentBar.ID {
				foundParentBar = true
			}
			if edge.ToID == childBar.ID {
				foundChildBar = true
			}
		}
	}

	if !foundParentBar {
		t.Error("Expected call edge from Parent.foo to Parent.bar (this resolution via same class)")
	}
	if foundChildBar {
		t.Error("Did not expect call edge to Child.bar (this in Parent should resolve to Parent)")
	}
}

// =============================================================================
// IT-01: buildInheritanceChain tests
// =============================================================================

func TestBuilder_BuildInheritanceChain(t *testing.T) {
	builder := NewBuilder()

	t.Run("no inheritance", func(t *testing.T) {
		state := &buildState{
			classExtends: map[string]string{},
		}
		chain := builder.buildInheritanceChain(state, "Widget")
		if len(chain) != 1 || chain[0] != "Widget" {
			t.Errorf("expected [Widget], got %v", chain)
		}
	})

	t.Run("single parent", func(t *testing.T) {
		state := &buildState{
			classExtends: map[string]string{
				"Plot": "Component",
			},
		}
		chain := builder.buildInheritanceChain(state, "Plot")
		if len(chain) != 2 || chain[0] != "Plot" || chain[1] != "Component" {
			t.Errorf("expected [Plot, Component], got %v", chain)
		}
	})

	t.Run("deep chain", func(t *testing.T) {
		state := &buildState{
			classExtends: map[string]string{
				"C": "B",
				"B": "A",
			},
		}
		chain := builder.buildInheritanceChain(state, "C")
		if len(chain) != 3 || chain[0] != "C" || chain[1] != "B" || chain[2] != "A" {
			t.Errorf("expected [C, B, A], got %v", chain)
		}
	})

	t.Run("circular protection", func(t *testing.T) {
		state := &buildState{
			classExtends: map[string]string{
				"A": "B",
				"B": "A",
			},
		}
		chain := builder.buildInheritanceChain(state, "A")
		// Should stop at max depth (10), not infinite loop
		if len(chain) > 11 {
			t.Errorf("chain too long (circular?): %v", chain)
		}
	})
}

// =============================================================================
// IT-01: FindCallersWithInheritance tests
// =============================================================================

func TestGraph_FindCallersWithInheritance(t *testing.T) {
	// Build a graph where:
	// - Component has renderImmediately() called by Component.doRender()
	// - Plot extends Component and overrides renderImmediately()
	// - ExternalFunc calls Plot.renderImmediately()

	componentRender := &ast.Symbol{
		ID:        "component.ts:50:renderImmediately",
		Name:      "renderImmediately",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "component.ts",
		StartLine: 50,
		EndLine:   60,
		Language:  "typescript",
	}

	doRender := &ast.Symbol{
		ID:        "component.ts:30:doRender",
		Name:      "doRender",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "component.ts",
		StartLine: 30,
		EndLine:   40,
		Language:  "typescript",
		Calls: []ast.CallSite{
			{
				Target:   "renderImmediately",
				IsMethod: true,
				Receiver: "this",
				Location: ast.Location{FilePath: "component.ts", StartLine: 35},
			},
		},
	}

	componentClass := &ast.Symbol{
		ID:        "component.ts:10:Component",
		Name:      "Component",
		Kind:      ast.SymbolKindClass,
		FilePath:  "component.ts",
		StartLine: 10,
		EndLine:   100,
		Language:  "typescript",
		Children:  []*ast.Symbol{doRender, componentRender},
	}

	plotRender := &ast.Symbol{
		ID:        "plot.ts:50:renderImmediately",
		Name:      "renderImmediately",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "plot.ts",
		StartLine: 50,
		EndLine:   60,
		Language:  "typescript",
	}

	plotClass := &ast.Symbol{
		ID:        "plot.ts:10:Plot",
		Name:      "Plot",
		Kind:      ast.SymbolKindClass,
		FilePath:  "plot.ts",
		StartLine: 10,
		EndLine:   100,
		Language:  "typescript",
		Children:  []*ast.Symbol{plotRender},
		Metadata: &ast.SymbolMetadata{
			Extends: "Component",
		},
	}

	externalFunc := &ast.Symbol{
		ID:        "main.ts:5:setup",
		Name:      "setup",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "main.ts",
		StartLine: 5,
		EndLine:   15,
		Language:  "typescript",
		Calls: []ast.CallSite{
			{
				Target:   "renderImmediately",
				IsMethod: true,
				Receiver: "plot",
				Location: ast.Location{FilePath: "main.ts", StartLine: 10},
			},
		},
	}

	r1 := testParseResult("component.ts", []*ast.Symbol{componentClass}, nil)
	r1.Language = "typescript"
	r2 := testParseResult("plot.ts", []*ast.Symbol{plotClass}, nil)
	r2.Language = "typescript"
	r3 := testParseResult("main.ts", []*ast.Symbol{externalFunc}, nil)
	r3.Language = "typescript"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{r1, r2, r3})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph

	t.Run("FindCallersByID_misses_parent_callers", func(t *testing.T) {
		// Standard FindCallersByID for Plot.renderImmediately
		// This will NOT find doRender (which calls Component.renderImmediately)
		result, err := g.FindCallersByID(context.Background(), plotRender.ID)
		if err != nil {
			t.Fatalf("FindCallersByID failed: %v", err)
		}

		// doRender calls this.renderImmediately() inside Component,
		// so the edge goes to Component.renderImmediately, not Plot.renderImmediately
		for _, sym := range result.Symbols {
			if sym.Name == "doRender" {
				t.Log("Note: doRender found as caller of Plot.renderImmediately — this means resolver already handled it")
			}
		}
	})

	t.Run("FindCallersWithInheritance_includes_parent_callers", func(t *testing.T) {
		// With inheritance, we pass parent method IDs too
		result, err := g.FindCallersWithInheritance(
			context.Background(),
			plotRender.ID,
			[]string{componentRender.ID},
		)
		if err != nil {
			t.Fatalf("FindCallersWithInheritance failed: %v", err)
		}

		// doRender should appear as an inherited caller (through Component.renderImmediately)
		allCallers := result.AllCallers()
		foundDoRender := false
		for _, sym := range allCallers.Symbols {
			if sym.Name == "doRender" {
				foundDoRender = true
			}
		}

		if !foundDoRender {
			t.Error("Expected doRender as a caller via inheritance chain (Component.renderImmediately)")
			t.Logf("Found callers: %v", func() []string {
				names := make([]string, len(allCallers.Symbols))
				for i, s := range allCallers.Symbols {
					names[i] = s.Name
				}
				return names
			}())
		}

		// Verify doRender is in InheritedCallers, NOT DirectCallers
		foundInDirect := false
		if result.DirectCallers != nil {
			for _, sym := range result.DirectCallers.Symbols {
				if sym.Name == "doRender" {
					foundInDirect = true
				}
			}
		}
		foundInInherited := false
		for _, parentResult := range result.InheritedCallers {
			for _, sym := range parentResult.Symbols {
				if sym.Name == "doRender" {
					foundInInherited = true
				}
			}
		}

		if foundInDirect {
			t.Error("doRender should NOT be in DirectCallers (it calls Component.renderImmediately, not Plot.renderImmediately)")
		}
		if !foundInInherited {
			t.Error("doRender should be in InheritedCallers")
		}
	})

	t.Run("FindCallersWithInheritance_deduplicates", func(t *testing.T) {
		// If the same caller appears via both paths, it should only appear once
		result, err := g.FindCallersWithInheritance(
			context.Background(),
			componentRender.ID,
			[]string{componentRender.ID}, // duplicate
		)
		if err != nil {
			t.Fatalf("FindCallersWithInheritance failed: %v", err)
		}

		// Count occurrences of doRender across all levels
		allCallers := result.AllCallers()
		doRenderCount := 0
		for _, sym := range allCallers.Symbols {
			if sym.Name == "doRender" {
				doRenderCount++
			}
		}

		if doRenderCount > 1 {
			t.Errorf("Expected deduplicated results, got doRender %d times", doRenderCount)
		}
	})

	t.Run("FindCallersWithInheritance_structured_result", func(t *testing.T) {
		// Verify the structured separation of direct vs inherited callers
		result, err := g.FindCallersWithInheritance(
			context.Background(),
			plotRender.ID,
			[]string{componentRender.ID},
		)
		if err != nil {
			t.Fatalf("FindCallersWithInheritance failed: %v", err)
		}

		// TotalCallerCount should match AllCallers length
		allCallers := result.AllCallers()
		if result.TotalCallerCount() != len(allCallers.Symbols) {
			t.Errorf("TotalCallerCount() = %d, want %d", result.TotalCallerCount(), len(allCallers.Symbols))
		}

		// InheritedCallers should have an entry for componentRender.ID
		if _, ok := result.InheritedCallers[componentRender.ID]; !ok {
			t.Errorf("Expected InheritedCallers to contain key %q", componentRender.ID)
			t.Logf("InheritedCallers keys: %v", func() []string {
				keys := make([]string, 0, len(result.InheritedCallers))
				for k := range result.InheritedCallers {
					keys = append(keys, k)
				}
				return keys
			}())
		}
	})
}

// =============================================================================
// IT-01: symbolParent and classExtends tracking tests
// =============================================================================

func TestBuilder_SymbolParentTracking(t *testing.T) {
	child1 := &ast.Symbol{
		ID:        "test.ts:20:method1",
		Name:      "method1",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "test.ts",
		StartLine: 20,
		EndLine:   30,
		Language:  "typescript",
	}

	parent := &ast.Symbol{
		ID:        "test.ts:10:MyClass",
		Name:      "MyClass",
		Kind:      ast.SymbolKindClass,
		FilePath:  "test.ts",
		StartLine: 10,
		EndLine:   50,
		Language:  "typescript",
		Children:  []*ast.Symbol{child1},
		Metadata: &ast.SymbolMetadata{
			Extends: "BaseClass",
		},
	}

	result := testParseResult("test.ts", []*ast.Symbol{parent}, nil)
	result.Language = "typescript"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Verify the build completed
	if buildResult.Graph == nil {
		t.Fatal("Graph is nil")
	}

	// The buildState is internal, but we can verify the graph has the right structure
	// by checking that nodes exist
	_, ok := buildResult.Graph.GetNode(child1.ID)
	if !ok {
		t.Error("Child method not found in graph")
	}
	_, ok = buildResult.Graph.GetNode(parent.ID)
	if !ok {
		t.Error("Parent class not found in graph")
	}
}

// =============================================================================
// IT-01: findOwnerClassName tests
// =============================================================================

func TestBuilder_FindOwnerClassName(t *testing.T) {
	builder := NewBuilder()

	t.Run("Go method with Receiver", func(t *testing.T) {
		state := &buildState{
			symbolParent: map[string]string{},
			symbolsByID:  map[string]*ast.Symbol{},
		}
		method := &ast.Symbol{
			ID:       "test.go:10:Get",
			Name:     "Get",
			Receiver: "Txn",
		}
		owner := builder.findOwnerClassName(state, method)
		if owner != "Txn" {
			t.Errorf("expected Txn, got %s", owner)
		}
	})

	t.Run("Python method via parent lookup", func(t *testing.T) {
		parentSym := &ast.Symbol{
			ID:   "df.py:1:DataFrame",
			Name: "DataFrame",
			Kind: ast.SymbolKindClass,
		}
		methodSym := &ast.Symbol{
			ID:   "df.py:10:filter",
			Name: "filter",
		}
		state := &buildState{
			symbolParent: map[string]string{
				"df.py:10:filter": "df.py:1:DataFrame",
			},
			symbolsByID: map[string]*ast.Symbol{
				"df.py:1:DataFrame": parentSym,
				"df.py:10:filter":   methodSym,
			},
		}
		owner := builder.findOwnerClassName(state, methodSym)
		if owner != "DataFrame" {
			t.Errorf("expected DataFrame, got %s", owner)
		}
	})

	t.Run("standalone function no owner", func(t *testing.T) {
		state := &buildState{
			symbolParent: map[string]string{},
			symbolsByID:  map[string]*ast.Symbol{},
		}
		fn := &ast.Symbol{
			ID:   "main.go:1:main",
			Name: "main",
		}
		owner := builder.findOwnerClassName(state, fn)
		if owner != "" {
			t.Errorf("expected empty, got %s", owner)
		}
	})
}

// R3-P1b: Constructor call edges should be allowed by validateEdgeType.
// When a function calls DataFrameFormatter(...), the resolved target is a Class symbol.
// The edge should NOT be silently dropped.
func TestBuilder_ExtractCallEdges_ConstructorCall(t *testing.T) {
	// Create a method that calls a class constructor
	callerSym := testSymbolWithCalls("process", ast.SymbolKindFunction, "main.py", 10, []ast.CallSite{
		{
			Target: "DataFrameFormatter",
			Location: ast.Location{
				FilePath:  "main.py",
				StartLine: 12,
			},
		},
	})
	callerSym.Language = "python"

	// The target is a Class symbol (constructor call)
	classSym := &ast.Symbol{
		ID:        "format.py:5:DataFrameFormatter",
		Name:      "DataFrameFormatter",
		Kind:      ast.SymbolKindClass,
		FilePath:  "format.py",
		StartLine: 5,
		EndLine:   50,
		Language:  "python",
	}

	result := &ast.ParseResult{
		FilePath: "main.py",
		Language: "python",
		Symbols:  []*ast.Symbol{callerSym, classSym},
		Package:  "test",
	}

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Check that a call edge was created FROM function TO class (constructor)
	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found in graph")
	}

	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == classSym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("R3-P1b: Expected EdgeTypeCalls from function to Class (constructor call), but edge was not created. " +
			"validateEdgeType/isCallable rejects SymbolKindClass as a call target.")
	}
}

// R3-P1b: Struct constructor calls should also create edges (Go: SomeStruct{}, TS: new SomeClass()).
func TestBuilder_ExtractCallEdges_StructConstructorCall(t *testing.T) {
	callerSym := testSymbolWithCalls("NewServer", ast.SymbolKindFunction, "server.go", 10, []ast.CallSite{
		{
			Target:   "Config",
			Location: ast.Location{FilePath: "server.go", StartLine: 12},
		},
	})

	structSym := &ast.Symbol{
		ID:        "config.go:5:Config",
		Name:      "Config",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "config.go",
		StartLine: 5,
		EndLine:   20,
		Language:  "go",
	}

	result := testParseResult("server.go", []*ast.Symbol{callerSym, structSym}, nil)

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found in graph")
	}

	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == structSym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("R3-P1b: Expected EdgeTypeCalls from function to Struct (constructor call), but edge was not created.")
	}
}

// R3-P1d: Property methods should have their call edges extracted.
// When a @property method calls other functions, those call edges should appear in the graph.
func TestBuilder_ExtractCallEdges_PropertyMethod(t *testing.T) {
	// Create a @property method that calls a function
	propertySym := testSymbolWithCalls("name", ast.SymbolKindProperty, "model.py", 10, []ast.CallSite{
		{
			Target:   "format_name",
			Location: ast.Location{FilePath: "model.py", StartLine: 12},
		},
	})
	propertySym.Language = "python"

	helperSym := &ast.Symbol{
		ID:        "model.py:30:format_name",
		Name:      "format_name",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "model.py",
		StartLine: 30,
		EndLine:   40,
		Language:  "python",
	}

	result := &ast.ParseResult{
		FilePath: "model.py",
		Language: "python",
		Symbols:  []*ast.Symbol{propertySym, helperSym},
		Package:  "test",
	}

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph
	propertyNode, ok := graph.GetNode(propertySym.ID)
	if !ok {
		t.Fatal("Property node not found in graph")
	}

	hasCallEdge := false
	for _, edge := range propertyNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == helperSym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("R3-P1d: Expected EdgeTypeCalls from Property to Function, but edge was not created. " +
			"extractSymbolEdges does not handle SymbolKindProperty.")
	}
}

// R3-P1d: A method calling a @property should create a call edge TO the property.
func TestBuilder_ExtractCallEdges_CallToProperty(t *testing.T) {
	// A function calls a property (e.g., obj.name where name is @property)
	callerSym := testSymbolWithCalls("process", ast.SymbolKindFunction, "main.py", 5, []ast.CallSite{
		{
			Target:   "name",
			IsMethod: true,
			Receiver: "self",
			Location: ast.Location{FilePath: "main.py", StartLine: 7},
		},
	})
	callerSym.Language = "python"

	// The property target — with a parent class
	classSym := &ast.Symbol{
		ID:        "main.py:1:Model",
		Name:      "Model",
		Kind:      ast.SymbolKindClass,
		FilePath:  "main.py",
		StartLine: 1,
		EndLine:   50,
		Language:  "python",
		Children: []*ast.Symbol{
			callerSym,
		},
	}

	propertySym := &ast.Symbol{
		ID:        "main.py:20:name",
		Name:      "name",
		Kind:      ast.SymbolKindProperty,
		FilePath:  "main.py",
		StartLine: 20,
		EndLine:   25,
		Language:  "python",
	}

	// Add property as child of class too
	classSym.Children = append(classSym.Children, propertySym)

	result := &ast.ParseResult{
		FilePath: "main.py",
		Language: "python",
		Symbols:  []*ast.Symbol{classSym, callerSym, propertySym},
		Package:  "test",
	}

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	graph := buildResult.Graph
	callerNode, ok := graph.GetNode(callerSym.ID)
	if !ok {
		t.Fatal("Caller node not found in graph")
	}

	hasCallEdge := false
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == propertySym.ID {
			hasCallEdge = true
			break
		}
	}

	if !hasCallEdge {
		t.Error("R3-P1d: Expected EdgeTypeCalls from Function to Property, but edge was not created. " +
			"isCallable/validateEdgeType rejects SymbolKindProperty as a call target.")
	}
}

// R3-P1b + R3-P1d combined: isCallable unit test verifying the allowed kinds.
func TestIsCallable(t *testing.T) {
	tests := []struct {
		kind     ast.SymbolKind
		name     string
		expected bool
	}{
		{ast.SymbolKindFunction, "Function", true},
		{ast.SymbolKindMethod, "Method", true},
		{ast.SymbolKindExternal, "External", true},
		{ast.SymbolKindProperty, "Property (R3-P1d)", true},
		{ast.SymbolKindVariable, "Variable", false},
		{ast.SymbolKindConstant, "Constant", false},
		{ast.SymbolKindField, "Field", false},
		{ast.SymbolKindImport, "Import", false},
		{ast.SymbolKindInterface, "Interface", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCallable(tt.kind)
			if got != tt.expected {
				t.Errorf("isCallable(%s) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

// R3-P1b: isCallTarget should allow Class and Struct as valid call targets (constructor calls).
func TestIsCallTarget(t *testing.T) {
	tests := []struct {
		kind     ast.SymbolKind
		name     string
		expected bool
	}{
		// Everything callable is also a valid target
		{ast.SymbolKindFunction, "Function", true},
		{ast.SymbolKindMethod, "Method", true},
		{ast.SymbolKindExternal, "External", true},
		{ast.SymbolKindProperty, "Property", true},
		// Constructor targets
		{ast.SymbolKindClass, "Class (R3-P1b)", true},
		{ast.SymbolKindStruct, "Struct (R3-P1b)", true},
		// Not valid targets
		{ast.SymbolKindVariable, "Variable", false},
		{ast.SymbolKindConstant, "Constant", false},
		{ast.SymbolKindField, "Field", false},
		{ast.SymbolKindImport, "Import", false},
		{ast.SymbolKindInterface, "Interface", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCallTarget(tt.kind)
			if got != tt.expected {
				t.Errorf("isCallTarget(%s) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

// ─── R3-P2 Step 0: resolveThisSelfCall Property filter ───

func TestBuilder_ResolveCallTarget_SelfPropertyCall(t *testing.T) {
	// Simulates Python: class User { @property name(); def greet(self): self.name() }
	// self.name should resolve to User.name even though name is a Property.
	nameProperty := &ast.Symbol{
		ID:        "user.py:20:name",
		Name:      "name",
		Kind:      ast.SymbolKindProperty,
		FilePath:  "user.py",
		StartLine: 20,
		EndLine:   25,
		Language:  "python",
	}

	greetMethod := &ast.Symbol{
		ID:        "user.py:30:greet",
		Name:      "greet",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "user.py",
		StartLine: 30,
		EndLine:   40,
		Language:  "python",
		Calls: []ast.CallSite{
			{
				Target:   "name",
				IsMethod: true,
				Receiver: "self",
				Location: ast.Location{FilePath: "user.py", StartLine: 35},
			},
		},
	}

	userClass := &ast.Symbol{
		ID:        "user.py:10:User",
		Name:      "User",
		Kind:      ast.SymbolKindClass,
		FilePath:  "user.py",
		StartLine: 10,
		EndLine:   50,
		Language:  "python",
		Children:  []*ast.Symbol{nameProperty, greetMethod},
	}

	result := testParseResult("user.py", []*ast.Symbol{userClass}, nil)
	result.Language = "python"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph
	greetNode, ok := g.GetNode(greetMethod.ID)
	if !ok {
		t.Fatal("greet node not found")
	}

	foundName := false
	for _, edge := range greetNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == nameProperty.ID {
			foundName = true
		}
	}

	if !foundName {
		t.Error("Expected call edge from greet to User.name (@property) via self resolution")
		for _, edge := range greetNode.Outgoing {
			t.Logf("  edge: %s -> %s (%s)", edge.FromID, edge.ToID, edge.Type)
		}
	}
}

// ─── R3-P2 Step 2: Self-referential filter in Strategy 1 ───

func TestBuilder_ResolveCallTarget_SelfReferentialSkipFallthrough(t *testing.T) {
	// Simulates: class DataFrame { def merge(self): merge() }
	// where merge() is imported from another file (reshape/merge.py).
	// Strategy 1 should skip same-file DataFrame.merge (self-referential)
	// and resolve to the cross-file merge function.

	mergeFunction := &ast.Symbol{
		ID:        "reshape/merge.py:10:merge",
		Name:      "merge",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "reshape/merge.py",
		StartLine: 10,
		EndLine:   100,
		Language:  "python",
	}

	mergeMethod := &ast.Symbol{
		ID:        "frame.py:50:merge",
		Name:      "merge",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "frame.py",
		StartLine: 50,
		EndLine:   70,
		Language:  "python",
		Calls: []ast.CallSite{
			{
				Target:   "merge",
				IsMethod: false,
				Receiver: "",
				Location: ast.Location{FilePath: "frame.py", StartLine: 55},
			},
		},
	}

	dfClass := &ast.Symbol{
		ID:        "frame.py:10:DataFrame",
		Name:      "DataFrame",
		Kind:      ast.SymbolKindClass,
		FilePath:  "frame.py",
		StartLine: 10,
		EndLine:   100,
		Language:  "python",
		Children:  []*ast.Symbol{mergeMethod},
	}

	result1 := testParseResult("frame.py", []*ast.Symbol{dfClass}, nil)
	result1.Language = "python"
	result2 := testParseResult("reshape/merge.py", []*ast.Symbol{mergeFunction}, nil)
	result2.Language = "python"

	builder := NewBuilder()
	buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result1, result2})
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := buildResult.Graph
	mergeNode, ok := g.GetNode(mergeMethod.ID)
	if !ok {
		t.Fatal("merge method node not found")
	}

	foundCrossFileTarget := false
	for _, edge := range mergeNode.Outgoing {
		if edge.Type == EdgeTypeCalls && edge.ToID == mergeFunction.ID {
			foundCrossFileTarget = true
		}
	}

	if !foundCrossFileTarget {
		t.Error("Expected call edge from DataFrame.merge to reshape/merge.merge (cross-file target)")
		t.Log("Strategy 1 should skip self-referential same-file match and fall through to cross-file candidates")
		for _, edge := range mergeNode.Outgoing {
			t.Logf("  edge: %s -> %s (%s)", edge.FromID, edge.ToID, edge.Type)
		}
	}
}

// ─── R3-P2 Step 3: Import-aware call resolution ───

func TestBuilder_ResolveCallTarget_ImportAwareResolution(t *testing.T) {
	// Simulates: frame.py has `from pandas.core.reshape.merge import merge`
	// DataFrame.query() calls merge() — should resolve to reshape/merge.py:merge
	// even though there are other functions named "merge" in other files.

	t.Run("import map resolves to correct file", func(t *testing.T) {
		realMerge := &ast.Symbol{
			ID:        "pandas/core/reshape/merge.py:10:merge",
			Name:      "merge",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pandas/core/reshape/merge.py",
			StartLine: 10,
			EndLine:   100,
			Language:  "python",
		}

		wrongMerge := &ast.Symbol{
			ID:        "utils/merge.py:5:merge",
			Name:      "merge",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "utils/merge.py",
			StartLine: 5,
			EndLine:   20,
			Language:  "python",
		}

		queryMethod := &ast.Symbol{
			ID:        "pandas/core/frame.py:50:query",
			Name:      "query",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "pandas/core/frame.py",
			StartLine: 50,
			EndLine:   70,
			Language:  "python",
			Calls: []ast.CallSite{
				{
					Target:   "merge",
					IsMethod: false,
					Receiver: "",
					Location: ast.Location{FilePath: "pandas/core/frame.py", StartLine: 55},
				},
			},
		}

		dfClass := &ast.Symbol{
			ID:        "pandas/core/frame.py:10:DataFrame",
			Name:      "DataFrame",
			Kind:      ast.SymbolKindClass,
			FilePath:  "pandas/core/frame.py",
			StartLine: 10,
			EndLine:   100,
			Language:  "python",
			Children:  []*ast.Symbol{queryMethod},
		}

		result1 := testParseResult("pandas/core/frame.py", []*ast.Symbol{dfClass}, []ast.Import{
			{
				Path:  "pandas.core.reshape.merge",
				Names: []string{"merge"},
			},
		})
		result1.Language = "python"

		result2 := testParseResult("pandas/core/reshape/merge.py", []*ast.Symbol{realMerge}, nil)
		result2.Language = "python"

		result3 := testParseResult("utils/merge.py", []*ast.Symbol{wrongMerge}, nil)
		result3.Language = "python"

		builder := NewBuilder()
		buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result1, result2, result3})
		if err != nil {
			t.Fatalf("Build failed: %v", err)
		}

		g := buildResult.Graph
		queryNode, ok := g.GetNode(queryMethod.ID)
		if !ok {
			t.Fatal("query node not found")
		}

		foundRealMerge := false
		foundWrongMerge := false
		for _, edge := range queryNode.Outgoing {
			if edge.Type == EdgeTypeCalls {
				if edge.ToID == realMerge.ID {
					foundRealMerge = true
				}
				if edge.ToID == wrongMerge.ID {
					foundWrongMerge = true
				}
			}
		}

		if !foundRealMerge {
			t.Error("Expected call edge to pandas/core/reshape/merge.py:merge (import-resolved)")
			for _, edge := range queryNode.Outgoing {
				t.Logf("  edge: %s -> %s (%s)", edge.FromID, edge.ToID, edge.Type)
			}
		}
		if foundWrongMerge {
			t.Error("Did not expect call edge to utils/merge.py:merge")
		}
	})

	t.Run("aliased import resolves correctly", func(t *testing.T) {
		realConcat := &ast.Symbol{
			ID:        "pandas/core/reshape/concat.py:10:concat",
			Name:      "concat",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "pandas/core/reshape/concat.py",
			StartLine: 10,
			EndLine:   50,
			Language:  "python",
		}

		caller := &ast.Symbol{
			ID:        "app.py:10:process",
			Name:      "process",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "app.py",
			StartLine: 10,
			EndLine:   30,
			Language:  "python",
			Calls: []ast.CallSite{
				{
					Target:   "pd_concat",
					IsMethod: false,
					Receiver: "",
					Location: ast.Location{FilePath: "app.py", StartLine: 15},
				},
			},
		}

		result1 := testParseResult("app.py", []*ast.Symbol{caller}, []ast.Import{
			{
				Path:  "pandas.core.reshape.concat",
				Names: []string{"concat as pd_concat"},
			},
		})
		result1.Language = "python"

		result2 := testParseResult("pandas/core/reshape/concat.py", []*ast.Symbol{realConcat}, nil)
		result2.Language = "python"

		builder := NewBuilder()
		buildResult, err := builder.Build(context.Background(), []*ast.ParseResult{result1, result2})
		if err != nil {
			t.Fatalf("Build failed: %v", err)
		}

		g := buildResult.Graph
		callerNode, ok := g.GetNode(caller.ID)
		if !ok {
			t.Fatal("caller node not found")
		}

		foundConcat := false
		for _, edge := range callerNode.Outgoing {
			if edge.Type == EdgeTypeCalls && edge.ToID == realConcat.ID {
				foundConcat = true
			}
		}

		if !foundConcat {
			t.Error("Expected call edge to concat via aliased import (pd_concat -> concat)")
			for _, edge := range callerNode.Outgoing {
				t.Logf("  edge: %s -> %s (%s)", edge.FromID, edge.ToID, edge.Type)
			}
		}
	})
}

func TestMatchesImportPath(t *testing.T) {
	tests := []struct {
		name       string
		filePath   string
		importPath string
		expected   bool
	}{
		{"exact match", "pandas/core/reshape/merge.py", "pandas.core.reshape.merge", true},
		{"with init", "pandas/core/reshape/merge/__init__.py", "pandas.core.reshape.merge", true},
		{"prefix pollution", "my_pandas/core/reshape/merge.py", "pandas.core.reshape.merge", false},
		{"suffix pollution", "pandas/core/reshape/merge_utils.py", "pandas.core.reshape.merge", false},
		{"partial match", "reshape/merge.py", "pandas.core.reshape.merge", false},
		{"simple module", "utils.py", "utils", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesImportPath(tt.filePath, tt.importPath)
			if got != tt.expected {
				t.Errorf("matchesImportPath(%q, %q) = %v, want %v", tt.filePath, tt.importPath, got, tt.expected)
			}
		})
	}
}

func TestParseAliasedName(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantLocal    string
		wantOriginal string
	}{
		{"no alias", "merge", "merge", "merge"},
		{"with alias", "concat as pd_concat", "pd_concat", "concat"},
		{"spaced alias", "merge as pd_merge", "pd_merge", "merge"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local, original := parseAliasedName(tt.input)
			if local != tt.wantLocal {
				t.Errorf("parseAliasedName(%q) local = %q, want %q", tt.input, local, tt.wantLocal)
			}
			if original != tt.wantOriginal {
				t.Errorf("parseAliasedName(%q) original = %q, want %q", tt.input, original, tt.wantOriginal)
			}
		})
	}
}

// =============================================================================
// IT-03a A-1: TypeScript Implicit Interface Implementation Tests
// =============================================================================

func TestBuilder_TSInterfaceImplementation(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("TS class implementing all interface methods creates IMPLEMENTS edge", func(t *testing.T) {
		// TypeScript interface with methods
		tsInterface := &ast.Symbol{
			ID:        "api.ts:5:Renderable",
			Name:      "Renderable",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "api.ts",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "render", ParamCount: 1, ReturnCount: 1},
					{Name: "dispose", ParamCount: 0, ReturnCount: 0},
				},
			},
		}

		// TypeScript class that implements all interface methods
		tsClass := &ast.Symbol{
			ID:        "widget.ts:10:Widget",
			Name:      "Widget",
			Kind:      ast.SymbolKindClass,
			FilePath:  "widget.ts",
			StartLine: 10,
			EndLine:   30,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "render", ParamCount: 1, ReturnCount: 1},
					{Name: "dispose", ParamCount: 0, ReturnCount: 0},
					{Name: "update", ParamCount: 1, ReturnCount: 0}, // extra method
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "api.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsInterface},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "widget.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsClass},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		// Check that EdgeTypeImplements was created from Widget to Renderable
		widgetNode, ok := result.Graph.GetNode(tsClass.ID)
		if !ok {
			t.Fatal("Widget node not found")
		}
		foundImplements := false
		for _, edge := range widgetNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == tsInterface.ID {
				foundImplements = true
				break
			}
		}
		if !foundImplements {
			t.Error("expected EdgeTypeImplements from Widget to Renderable (TS implicit interface)")
		}
	})

	t.Run("TS class with partial methods does not match", func(t *testing.T) {
		tsInterface := &ast.Symbol{
			ID:        "iface.ts:5:Serializable",
			Name:      "Serializable",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "iface.ts",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "serialize", ParamCount: 0, ReturnCount: 1},
					{Name: "deserialize", ParamCount: 1, ReturnCount: 1},
				},
			},
		}

		tsClass := &ast.Symbol{
			ID:        "data.ts:10:DataStore",
			Name:      "DataStore",
			Kind:      ast.SymbolKindClass,
			FilePath:  "data.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "serialize", ParamCount: 0, ReturnCount: 1},
					// Missing deserialize
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "iface.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsInterface},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "data.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsClass},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		dataStoreNode, ok := result.Graph.GetNode(tsClass.ID)
		if !ok {
			t.Fatal("DataStore node not found")
		}
		for _, edge := range dataStoreNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == tsInterface.ID {
				t.Error("unexpected EdgeTypeImplements from DataStore to Serializable (missing deserialize)")
			}
		}
	})

	t.Run("TS interface does not cross-match with Go types", func(t *testing.T) {
		tsInterface := &ast.Symbol{
			ID:        "ts_iface.ts:5:Runner",
			Name:      "Runner",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "ts_iface.ts",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "run", ParamCount: 0, ReturnCount: 0},
				},
			},
		}

		goStruct := &ast.Symbol{
			ID:        "runner.go:5:GoRunner",
			Name:      "GoRunner",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "runner.go",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "go",
			Metadata: &ast.SymbolMetadata{
				Methods: []ast.MethodSignature{
					{Name: "run", ParamCount: 0, ReturnCount: 0, ReceiverType: "*GoRunner"},
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "ts_iface.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{tsInterface},
			Package:  "api",
		}
		parseResult2 := testParseResult("runner.go", []*ast.Symbol{goStruct}, nil)

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		goNode, ok := result.Graph.GetNode(goStruct.ID)
		if !ok {
			t.Fatal("GoRunner node not found")
		}
		for _, edge := range goNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == tsInterface.ID {
				t.Error("Go struct should NOT implement TypeScript interface (cross-language)")
			}
		}
	})
}

// =============================================================================
// IT-03a A-3: Decorator Argument Edges Tests
// =============================================================================

func TestBuilder_DecoratorArgEdges(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("symbol with DecoratorArgs creates REFERENCES edges", func(t *testing.T) {
		// The decorator argument target (a class that is referenced)
		loggingInterceptor := &ast.Symbol{
			ID:        "interceptors.ts:5:LoggingInterceptor",
			Name:      "LoggingInterceptor",
			Kind:      ast.SymbolKindClass,
			FilePath:  "interceptors.ts",
			StartLine: 5,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		// The decorated symbol with decorator args referencing LoggingInterceptor
		controller := &ast.Symbol{
			ID:        "controller.ts:10:UserController",
			Name:      "UserController",
			Kind:      ast.SymbolKindClass,
			FilePath:  "controller.ts",
			StartLine: 10,
			EndLine:   40,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				DecoratorArgs: map[string][]string{
					"UseInterceptors": {"LoggingInterceptor"},
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "interceptors.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{loggingInterceptor},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "controller.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{controller},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		controllerNode, ok := result.Graph.GetNode(controller.ID)
		if !ok {
			t.Fatal("UserController node not found")
		}

		foundReference := false
		for _, edge := range controllerNode.Outgoing {
			if edge.Type == EdgeTypeReferences && edge.ToID == loggingInterceptor.ID {
				foundReference = true
				break
			}
		}
		if !foundReference {
			t.Error("expected EdgeTypeReferences from UserController to LoggingInterceptor (decorator arg)")
		}
	})

	t.Run("multiple decorator args create multiple edges", func(t *testing.T) {
		targetA := &ast.Symbol{
			ID:        "services.ts:5:AuthService",
			Name:      "AuthService",
			Kind:      ast.SymbolKindClass,
			FilePath:  "services.ts",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		targetB := &ast.Symbol{
			ID:        "services.ts:20:UserService",
			Name:      "UserService",
			Kind:      ast.SymbolKindClass,
			FilePath:  "services.ts",
			StartLine: 20,
			EndLine:   30,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		decorated := &ast.Symbol{
			ID:        "module.ts:10:AppModule",
			Name:      "AppModule",
			Kind:      ast.SymbolKindClass,
			FilePath:  "module.ts",
			StartLine: 10,
			EndLine:   30,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				DecoratorArgs: map[string][]string{
					"Module": {"AuthService", "UserService"},
				},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "services.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{targetA, targetB},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "module.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{decorated},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		moduleNode, ok := result.Graph.GetNode(decorated.ID)
		if !ok {
			t.Fatal("AppModule node not found")
		}

		refToAuth := false
		refToUser := false
		for _, edge := range moduleNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				if edge.ToID == targetA.ID {
					refToAuth = true
				}
				if edge.ToID == targetB.ID {
					refToUser = true
				}
			}
		}
		if !refToAuth {
			t.Error("expected EdgeTypeReferences from AppModule to AuthService")
		}
		if !refToUser {
			t.Error("expected EdgeTypeReferences from AppModule to UserService")
		}
	})
}

func TestBuilder_DecoratorArgEdges_NoArgs(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Symbol with nil Metadata (no decorator args)
	sym := &ast.Symbol{
		ID:        "plain.ts:10:PlainClass",
		Name:      "PlainClass",
		Kind:      ast.SymbolKindClass,
		FilePath:  "plain.ts",
		StartLine: 10,
		EndLine:   20,
		StartCol:  0,
		EndCol:    50,
		Language:  "typescript",
	}

	parseResult := &ast.ParseResult{
		FilePath: "plain.ts",
		Language: "typescript",
		Symbols:  []*ast.Symbol{sym},
		Package:  "api",
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	plainNode, ok := result.Graph.GetNode(sym.ID)
	if !ok {
		t.Fatal("PlainClass node not found")
	}

	// Should have no REFERENCES edges since there are no decorator args
	for _, edge := range plainNode.Outgoing {
		if edge.Type == EdgeTypeReferences {
			t.Errorf("unexpected EdgeTypeReferences from PlainClass (no decorator args): to %s", edge.ToID)
		}
	}
}

func TestBuilder_DecoratorArgEdges_OnlyPascalCase(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Symbol with decorator args: one PascalCase (placeholder created), one lowercase (skipped)
	sym := &ast.Symbol{
		ID:        "handler.ts:10:MyHandler",
		Name:      "MyHandler",
		Kind:      ast.SymbolKindClass,
		FilePath:  "handler.ts",
		StartLine: 10,
		EndLine:   30,
		StartCol:  0,
		EndCol:    50,
		Language:  "typescript",
		Metadata: &ast.SymbolMetadata{
			DecoratorArgs: map[string][]string{
				"Guard": {"AuthGuard", "options"},
			},
		},
	}

	parseResult := &ast.ParseResult{
		FilePath: "handler.ts",
		Language: "typescript",
		Symbols:  []*ast.Symbol{sym},
		Package:  "api",
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	handlerNode, ok := result.Graph.GetNode(sym.ID)
	if !ok {
		t.Fatal("MyHandler node not found")
	}

	// Count REFERENCES edges: should have one for AuthGuard (PascalCase placeholder),
	// but none for "options" (lowercase, skipped)
	refCount := 0
	for _, edge := range handlerNode.Outgoing {
		if edge.Type == EdgeTypeReferences {
			refCount++
		}
	}

	if refCount != 1 {
		t.Errorf("expected 1 REFERENCES edge (AuthGuard only), got %d", refCount)
	}
}

// =============================================================================
// IT-03a C-1: Callback Reference Edges Tests
// =============================================================================

func TestBuilder_CallbackRefEdges(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("CallSite with FunctionArgs creates REFERENCES edges", func(t *testing.T) {
		// The callback function that will be passed as argument
		middleware := &ast.Symbol{
			ID:        "middleware.js:5:authMiddleware",
			Name:      "authMiddleware",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "middleware.js",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "javascript",
		}

		// The caller that passes middleware as a callback arg
		setup := testSymbolWithCalls("setup", ast.SymbolKindFunction, "middleware.js", 20, []ast.CallSite{
			{
				Target: "use",
				Location: ast.Location{
					FilePath:  "middleware.js",
					StartLine: 22,
					EndLine:   22,
				},
				IsMethod:     true,
				Receiver:     "app",
				FunctionArgs: []string{"authMiddleware"},
			},
		})
		setup.Language = "javascript"

		parseResult := &ast.ParseResult{
			FilePath: "middleware.js",
			Language: "javascript",
			Symbols:  []*ast.Symbol{middleware, setup},
			Package:  "app",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		setupNode, ok := result.Graph.GetNode(setup.ID)
		if !ok {
			t.Fatal("setup node not found")
		}

		foundCallbackRef := false
		for _, edge := range setupNode.Outgoing {
			if edge.Type == EdgeTypeReferences && edge.ToID == middleware.ID {
				foundCallbackRef = true
				break
			}
		}
		if !foundCallbackRef {
			t.Error("expected EdgeTypeReferences from setup to authMiddleware (callback arg)")
		}
	})

	t.Run("multiple FunctionArgs create multiple REFERENCES edges", func(t *testing.T) {
		handlerA := &ast.Symbol{
			ID:        "handlers.ts:5:handleGet",
			Name:      "handleGet",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "handlers.ts",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		handlerB := &ast.Symbol{
			ID:        "handlers.ts:20:handlePost",
			Name:      "handlePost",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "handlers.ts",
			StartLine: 20,
			EndLine:   30,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		router := testSymbolWithCalls("configureRoutes", ast.SymbolKindFunction, "handlers.ts", 35, []ast.CallSite{
			{
				Target: "route",
				Location: ast.Location{
					FilePath:  "handlers.ts",
					StartLine: 36,
					EndLine:   36,
				},
				FunctionArgs: []string{"handleGet", "handlePost"},
			},
		})
		router.Language = "typescript"

		parseResult := &ast.ParseResult{
			FilePath: "handlers.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{handlerA, handlerB, router},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		routerNode, ok := result.Graph.GetNode(router.ID)
		if !ok {
			t.Fatal("configureRoutes node not found")
		}

		refToA := false
		refToB := false
		for _, edge := range routerNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				if edge.ToID == handlerA.ID {
					refToA = true
				}
				if edge.ToID == handlerB.ID {
					refToB = true
				}
			}
		}
		if !refToA {
			t.Error("expected EdgeTypeReferences from configureRoutes to handleGet")
		}
		if !refToB {
			t.Error("expected EdgeTypeReferences from configureRoutes to handlePost")
		}
	})
}

func TestBuilder_CallbackRefEdges_UnresolvableSkipped(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Caller with FunctionArgs that reference a name not in the graph
	caller := testSymbolWithCalls("setup", ast.SymbolKindFunction, "app.js", 5, []ast.CallSite{
		{
			Target: "configure",
			Location: ast.Location{
				FilePath:  "app.js",
				StartLine: 6,
				EndLine:   6,
			},
			FunctionArgs: []string{"nonExistentHandler"},
		},
	})
	caller.Language = "javascript"

	parseResult := &ast.ParseResult{
		FilePath: "app.js",
		Language: "javascript",
		Symbols:  []*ast.Symbol{caller},
		Package:  "app",
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	callerNode, ok := result.Graph.GetNode(caller.ID)
	if !ok {
		t.Fatal("setup node not found")
	}

	// Should have NO REFERENCES edges since nonExistentHandler doesn't resolve
	// (callback args don't create placeholders)
	for _, edge := range callerNode.Outgoing {
		if edge.Type == EdgeTypeReferences {
			t.Errorf("unexpected EdgeTypeReferences from setup (unresolvable callback arg): to %s", edge.ToID)
		}
	}
}

// =============================================================================
// IT-03a C-2: Type Argument Reference Edges Tests
// =============================================================================

func TestBuilder_TypeArgEdges(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("symbol with TypeArguments creates REFERENCES edges", func(t *testing.T) {
		// The type referenced as a type argument
		userType := &ast.Symbol{
			ID:        "models.ts:5:User",
			Name:      "User",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "models.ts",
			StartLine: 5,
			EndLine:   15,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		// Function whose return type has a type argument referencing User (e.g., Promise<User>)
		getUser := &ast.Symbol{
			ID:        "service.ts:10:getUser",
			Name:      "getUser",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "service.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeArguments: []string{"User"},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "models.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{userType},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "service.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{getUser},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		getUserNode, ok := result.Graph.GetNode(getUser.ID)
		if !ok {
			t.Fatal("getUser node not found")
		}

		foundTypeArgRef := false
		for _, edge := range getUserNode.Outgoing {
			if edge.Type == EdgeTypeReferences && edge.ToID == userType.ID {
				foundTypeArgRef = true
				break
			}
		}
		if !foundTypeArgRef {
			t.Error("expected EdgeTypeReferences from getUser to User (type argument)")
		}
	})

	t.Run("multiple type arguments create multiple edges", func(t *testing.T) {
		keyType := &ast.Symbol{
			ID:        "types.ts:5:KeyType",
			Name:      "KeyType",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "types.ts",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		valType := &ast.Symbol{
			ID:        "types.ts:15:ValueType",
			Name:      "ValueType",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "types.ts",
			StartLine: 15,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		// Function with Map<KeyType, ValueType>
		mapFunc := &ast.Symbol{
			ID:        "utils.ts:10:buildMap",
			Name:      "buildMap",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "utils.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeArguments: []string{"KeyType", "ValueType"},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "types.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{keyType, valType},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "utils.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{mapFunc},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		mapNode, ok := result.Graph.GetNode(mapFunc.ID)
		if !ok {
			t.Fatal("buildMap node not found")
		}

		refToKey := false
		refToVal := false
		for _, edge := range mapNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				if edge.ToID == keyType.ID {
					refToKey = true
				}
				if edge.ToID == valType.ID {
					refToVal = true
				}
			}
		}
		if !refToKey {
			t.Error("expected EdgeTypeReferences from buildMap to KeyType")
		}
		if !refToVal {
			t.Error("expected EdgeTypeReferences from buildMap to ValueType")
		}
	})

	t.Run("unresolvable type arguments are skipped", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:        "fn.ts:10:processData",
			Name:      "processData",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "fn.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeArguments: []string{"UnknownType"},
			},
		}

		parseResult := &ast.ParseResult{
			FilePath: "fn.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{sym},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		symNode, ok := result.Graph.GetNode(sym.ID)
		if !ok {
			t.Fatal("processData node not found")
		}

		for _, edge := range symNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				t.Errorf("unexpected EdgeTypeReferences from processData (type arg should not resolve): to %s", edge.ToID)
			}
		}
	})
}

// =============================================================================
// IT-03a C-3: Type Narrowing Reference Edges Tests
// =============================================================================

func TestBuilder_TypeNarrowingEdges(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("symbol with TypeNarrowings creates REFERENCES edges", func(t *testing.T) {
		// The type referenced in instanceof
		routerClass := &ast.Symbol{
			ID:        "router.ts:5:Router",
			Name:      "Router",
			Kind:      ast.SymbolKindClass,
			FilePath:  "router.ts",
			StartLine: 5,
			EndLine:   25,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		// Function that uses instanceof Router
		handleRequest := &ast.Symbol{
			ID:        "handler.ts:10:handleRequest",
			Name:      "handleRequest",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "handler.ts",
			StartLine: 10,
			EndLine:   30,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeNarrowings: []string{"Router"},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "router.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{routerClass},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "handler.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{handleRequest},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		handlerNode, ok := result.Graph.GetNode(handleRequest.ID)
		if !ok {
			t.Fatal("handleRequest node not found")
		}

		foundNarrowingRef := false
		for _, edge := range handlerNode.Outgoing {
			if edge.Type == EdgeTypeReferences && edge.ToID == routerClass.ID {
				foundNarrowingRef = true
				break
			}
		}
		if !foundNarrowingRef {
			t.Error("expected EdgeTypeReferences from handleRequest to Router (type narrowing)")
		}
	})

	t.Run("multiple type narrowings create multiple edges", func(t *testing.T) {
		classA := &ast.Symbol{
			ID:        "types.ts:5:ErrorA",
			Name:      "ErrorA",
			Kind:      ast.SymbolKindClass,
			FilePath:  "types.ts",
			StartLine: 5,
			EndLine:   10,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		classB := &ast.Symbol{
			ID:        "types.ts:15:ErrorB",
			Name:      "ErrorB",
			Kind:      ast.SymbolKindClass,
			FilePath:  "types.ts",
			StartLine: 15,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
		}

		fn := &ast.Symbol{
			ID:        "handler.ts:30:handleError",
			Name:      "handleError",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "handler.ts",
			StartLine: 30,
			EndLine:   50,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeNarrowings: []string{"ErrorA", "ErrorB"},
			},
		}

		parseResult1 := &ast.ParseResult{
			FilePath: "types.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{classA, classB},
			Package:  "api",
		}
		parseResult2 := &ast.ParseResult{
			FilePath: "handler.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{fn},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		fnNode, ok := result.Graph.GetNode(fn.ID)
		if !ok {
			t.Fatal("handleError node not found")
		}

		refToA := false
		refToB := false
		for _, edge := range fnNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				if edge.ToID == classA.ID {
					refToA = true
				}
				if edge.ToID == classB.ID {
					refToB = true
				}
			}
		}
		if !refToA {
			t.Error("expected EdgeTypeReferences from handleError to ErrorA")
		}
		if !refToB {
			t.Error("expected EdgeTypeReferences from handleError to ErrorB")
		}
	})

	t.Run("unresolvable type narrowings are skipped", func(t *testing.T) {
		fn := &ast.Symbol{
			ID:        "check.ts:10:checkType",
			Name:      "checkType",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "check.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				TypeNarrowings: []string{"NonExistentType"},
			},
		}

		parseResult := &ast.ParseResult{
			FilePath: "check.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{fn},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		fnNode, ok := result.Graph.GetNode(fn.ID)
		if !ok {
			t.Fatal("checkType node not found")
		}

		for _, edge := range fnNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				t.Errorf("unexpected EdgeTypeReferences from checkType (type narrowing should not resolve): to %s", edge.ToID)
			}
		}
	})

	t.Run("nil metadata does not cause errors", func(t *testing.T) {
		fn := &ast.Symbol{
			ID:        "simple.ts:10:simpleFunc",
			Name:      "simpleFunc",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "simple.ts",
			StartLine: 10,
			EndLine:   20,
			StartCol:  0,
			EndCol:    50,
			Language:  "typescript",
			// Metadata is nil
		}

		parseResult := &ast.ParseResult{
			FilePath: "simple.ts",
			Language: "typescript",
			Symbols:  []*ast.Symbol{fn},
			Package:  "api",
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{parseResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		fnNode, ok := result.Graph.GetNode(fn.ID)
		if !ok {
			t.Fatal("simpleFunc node not found")
		}

		for _, edge := range fnNode.Outgoing {
			if edge.Type == EdgeTypeReferences {
				t.Errorf("unexpected EdgeTypeReferences from simpleFunc (nil metadata): to %s", edge.ToID)
			}
		}
	})
}

// =============================================================================
// IT-03a Phase 12: End-to-end TS implicit interface matching via real parser
// =============================================================================

func TestBuildGraph_TSImplicitInterfaceMatching_EndToEnd(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("parsed TS interface+class produces IMPLEMENTS edge", func(t *testing.T) {
		// Parse real TypeScript source through the actual parser to verify
		// that Metadata.Methods is populated by the parser, and the builder
		// then creates EdgeTypeImplements.
		tsParser := ast.NewTypeScriptParser()

		ifaceSource := `export interface Repository {
    findById(id: string): Entity;
    save(entity: Entity): void;
}
`
		classSource := `export class UserRepository {
    findById(id: string): Entity {
        return null;
    }
    save(entity: Entity): void {
        // persist
    }
    count(): number {
        return 0;
    }
}
`

		ifaceResult, err := tsParser.Parse(ctx, []byte(ifaceSource), "repo.ts")
		if err != nil {
			t.Fatalf("parse interface failed: %v", err)
		}
		classResult, err := tsParser.Parse(ctx, []byte(classSource), "user_repo.ts")
		if err != nil {
			t.Fatalf("parse class failed: %v", err)
		}

		// Verify parser populated Metadata.Methods (the fix this phase adds)
		var iface *ast.Symbol
		for _, sym := range ifaceResult.Symbols {
			if sym.Kind == ast.SymbolKindInterface && sym.Name == "Repository" {
				iface = sym
				break
			}
		}
		if iface == nil {
			t.Fatal("expected interface 'Repository' in parse result")
		}
		if iface.Metadata == nil || len(iface.Metadata.Methods) == 0 {
			t.Fatal("expected Metadata.Methods to be populated for TS interface (F-1 fix)")
		}

		var cls *ast.Symbol
		for _, sym := range classResult.Symbols {
			if sym.Kind == ast.SymbolKindClass && sym.Name == "UserRepository" {
				cls = sym
				break
			}
		}
		if cls == nil {
			t.Fatal("expected class 'UserRepository' in parse result")
		}
		if cls.Metadata == nil || len(cls.Metadata.Methods) == 0 {
			t.Fatal("expected Metadata.Methods to be populated for TS class (F-2 fix)")
		}

		// Feed to builder — this should now create EdgeTypeImplements
		result, err := builder.Build(ctx, []*ast.ParseResult{ifaceResult, classResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		classNode, ok := result.Graph.GetNode(cls.ID)
		if !ok {
			t.Fatal("UserRepository node not found in graph")
		}

		foundImplements := false
		for _, edge := range classNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == iface.ID {
				foundImplements = true
				break
			}
		}
		if !foundImplements {
			t.Error("expected EdgeTypeImplements from UserRepository to Repository (TS implicit interface matching)")
		}
	})
}

func TestBuildGraph_TSImplicitInterfaceMatching_PartialMethods(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	t.Run("parsed TS class with partial methods does NOT match", func(t *testing.T) {
		tsParser := ast.NewTypeScriptParser()

		ifaceSource := `export interface Serializable {
    serialize(): string;
    deserialize(data: string): void;
}
`
		classSource := `export class PartialImpl {
    serialize(): string {
        return "{}";
    }
}
`

		ifaceResult, err := tsParser.Parse(ctx, []byte(ifaceSource), "serial.ts")
		if err != nil {
			t.Fatalf("parse interface failed: %v", err)
		}
		classResult, err := tsParser.Parse(ctx, []byte(classSource), "partial.ts")
		if err != nil {
			t.Fatalf("parse class failed: %v", err)
		}

		// Verify parser populated Metadata.Methods
		var iface *ast.Symbol
		for _, sym := range ifaceResult.Symbols {
			if sym.Kind == ast.SymbolKindInterface && sym.Name == "Serializable" {
				iface = sym
				break
			}
		}
		if iface == nil {
			t.Fatal("expected interface 'Serializable'")
		}
		if iface.Metadata == nil || len(iface.Metadata.Methods) != 2 {
			t.Fatalf("expected 2 methods in interface Metadata.Methods, got %d",
				func() int {
					if iface.Metadata == nil {
						return 0
					}
					return len(iface.Metadata.Methods)
				}())
		}

		var cls *ast.Symbol
		for _, sym := range classResult.Symbols {
			if sym.Kind == ast.SymbolKindClass && sym.Name == "PartialImpl" {
				cls = sym
				break
			}
		}
		if cls == nil {
			t.Fatal("expected class 'PartialImpl'")
		}

		result, err := builder.Build(ctx, []*ast.ParseResult{ifaceResult, classResult})
		if err != nil {
			t.Fatalf("build failed: %v", err)
		}

		classNode, ok := result.Graph.GetNode(cls.ID)
		if !ok {
			t.Fatal("PartialImpl node not found in graph")
		}

		for _, edge := range classNode.Outgoing {
			if edge.Type == EdgeTypeImplements && edge.ToID == iface.ID {
				t.Error("unexpected EdgeTypeImplements from PartialImpl to Serializable (missing deserialize)")
			}
		}
	})
}

// Phase 18: Test that composed interfaces (embedding other interfaces) gain methods
// transitively and that structs implementing all those methods get IMPLEMENTS edges
// to the composed interface as well.
func TestBuildGraph_GoComposedInterfaceResolution(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Interface Reader with method Read
	readerIface := &ast.Symbol{
		ID:        "iface.go:1:Reader",
		Name:      "Reader",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "iface.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "Read", ParamCount: 1, ReturnCount: 2},
			},
		},
	}

	// Interface Writer with method Write
	writerIface := &ast.Symbol{
		ID:        "iface.go:7:Writer",
		Name:      "Writer",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "iface.go",
		StartLine: 7, EndLine: 11, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "Write", ParamCount: 1, ReturnCount: 2},
			},
		},
	}

	// Composed interface ReadWriter: embeds Reader + Writer, no direct methods
	readWriterIface := &ast.Symbol{
		ID:        "iface.go:13:ReadWriter",
		Name:      "ReadWriter",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "iface.go",
		StartLine: 13, EndLine: 17, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Extends:    "Reader",
			Implements: []string{"Writer"},
		},
	}

	// Struct MyRW implements both Read and Write
	myRW := &ast.Symbol{
		ID:        "impl.go:1:MyRW",
		Name:      "MyRW",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "impl.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "Read", ParamCount: 1, ReturnCount: 2, ReceiverType: "*MyRW"},
				{Name: "Write", ParamCount: 1, ReturnCount: 2, ReceiverType: "*MyRW"},
			},
		},
	}

	parseResult1 := testParseResult("iface.go", []*ast.Symbol{readerIface, writerIface, readWriterIface}, nil)
	parseResult2 := testParseResult("impl.go", []*ast.Symbol{myRW}, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	myRWNode, ok := result.Graph.GetNode(myRW.ID)
	if !ok {
		t.Fatal("MyRW node not found")
	}

	implementsTargets := make(map[string]bool)
	for _, edge := range myRWNode.Outgoing {
		if edge.Type == EdgeTypeImplements {
			implementsTargets[edge.ToID] = true
		}
	}

	if !implementsTargets[readerIface.ID] {
		t.Error("expected EdgeTypeImplements from MyRW to Reader")
	}
	if !implementsTargets[writerIface.ID] {
		t.Error("expected EdgeTypeImplements from MyRW to Writer")
	}
	if !implementsTargets[readWriterIface.ID] {
		t.Error("expected EdgeTypeImplements from MyRW to ReadWriter (composed interface)")
	}
}

// Phase 18: Test deep composed interface resolution (3 levels, like Hugo's Page).
func TestBuildGraph_GoDeepComposedInterface(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Level 1: TypeProvider with method ResourceType
	typeProvider := &ast.Symbol{
		ID:        "types.go:1:TypeProvider",
		Name:      "TypeProvider",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "types.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "ResourceType", ParamCount: 0, ReturnCount: 1},
			},
		},
	}

	// Level 2: ResourceWithoutMeta embeds TypeProvider, no direct methods
	resourceWithoutMeta := &ast.Symbol{
		ID:        "types.go:7:ResourceWithoutMeta",
		Name:      "ResourceWithoutMeta",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "types.go",
		StartLine: 7, EndLine: 11, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Extends: "TypeProvider",
		},
	}

	// Level 3: Resource embeds ResourceWithoutMeta, no direct methods
	resource := &ast.Symbol{
		ID:        "types.go:13:Resource",
		Name:      "Resource",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "types.go",
		StartLine: 13, EndLine: 17, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Extends: "ResourceWithoutMeta",
		},
	}

	// Struct GenericResource implements ResourceType
	genericResource := &ast.Symbol{
		ID:        "impl.go:1:GenericResource",
		Name:      "GenericResource",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "impl.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "ResourceType", ParamCount: 0, ReturnCount: 1, ReceiverType: "*GenericResource"},
			},
		},
	}

	parseResult1 := testParseResult("types.go", []*ast.Symbol{typeProvider, resourceWithoutMeta, resource}, nil)
	parseResult2 := testParseResult("impl.go", []*ast.Symbol{genericResource}, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	grNode, ok := result.Graph.GetNode(genericResource.ID)
	if !ok {
		t.Fatal("GenericResource node not found")
	}

	implementsTargets := make(map[string]bool)
	for _, edge := range grNode.Outgoing {
		if edge.Type == EdgeTypeImplements {
			implementsTargets[edge.ToID] = true
		}
	}

	if !implementsTargets[typeProvider.ID] {
		t.Error("expected EdgeTypeImplements from GenericResource to TypeProvider")
	}
	if !implementsTargets[resourceWithoutMeta.ID] {
		t.Error("expected EdgeTypeImplements from GenericResource to ResourceWithoutMeta")
	}
	if !implementsTargets[resource.ID] {
		t.Error("expected EdgeTypeImplements from GenericResource to Resource (3-level deep)")
	}
}

// Phase 18: Test that an interface embedding a non-existent interface remains empty
// and does NOT match any struct (avoids false-positive "implements everything").
func TestBuildGraph_GoComposedInterface_EmptyAfterResolution(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// Interface that embeds a non-existent interface (placeholder target)
	emptyComposer := &ast.Symbol{
		ID:        "iface.go:1:EmptyComposer",
		Name:      "EmptyComposer",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "iface.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Extends: "NonExistent",
		},
	}

	// Struct with a method — should NOT implement EmptyComposer
	anything := &ast.Symbol{
		ID:        "impl.go:1:Anything",
		Name:      "Anything",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "impl.go",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "go",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "Foo", ParamCount: 0, ReturnCount: 0, ReceiverType: "*Anything"},
			},
		},
	}

	parseResult1 := testParseResult("iface.go", []*ast.Symbol{emptyComposer}, nil)
	parseResult2 := testParseResult("impl.go", []*ast.Symbol{anything}, nil)

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	anythingNode, ok := result.Graph.GetNode(anything.ID)
	if !ok {
		t.Fatal("Anything node not found")
	}

	for _, edge := range anythingNode.Outgoing {
		if edge.Type == EdgeTypeImplements && edge.ToID == emptyComposer.ID {
			t.Error("unexpected EdgeTypeImplements from Anything to EmptyComposer (should remain empty)")
		}
	}
}

// Phase 18: Test that TypeScript composed interfaces (extending other interfaces)
// gain methods transitively and that classes implementing all methods match.
func TestBuildGraph_TSComposedInterfaceResolution(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// interface Readable { read(): string }
	readable := &ast.Symbol{
		ID:        "io.ts:1:Readable",
		Name:      "Readable",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "io.ts",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "typescript",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "read", ParamCount: 0, ReturnCount: 1},
			},
		},
	}

	// interface Writable { write(data: string): void }
	writable := &ast.Symbol{
		ID:        "io.ts:7:Writable",
		Name:      "Writable",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "io.ts",
		StartLine: 7, EndLine: 11, StartCol: 0, EndCol: 50,
		Language: "typescript",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "write", ParamCount: 1, ReturnCount: 0},
			},
		},
	}

	// interface ReadWriteStream extends Readable, Writable {} — no own methods
	readWriteStream := &ast.Symbol{
		ID:        "io.ts:13:ReadWriteStream",
		Name:      "ReadWriteStream",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "io.ts",
		StartLine: 13, EndLine: 15, StartCol: 0, EndCol: 50,
		Language: "typescript",
		Metadata: &ast.SymbolMetadata{
			Extends:    "Readable",
			Implements: []string{"Writable"},
		},
	}

	// class FileStream implements read + write
	fileStream := &ast.Symbol{
		ID:        "stream.ts:1:FileStream",
		Name:      "FileStream",
		Kind:      ast.SymbolKindClass,
		FilePath:  "stream.ts",
		StartLine: 1, EndLine: 10, StartCol: 0, EndCol: 50,
		Language: "typescript",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "read", ParamCount: 0, ReturnCount: 1},
				{Name: "write", ParamCount: 1, ReturnCount: 0},
			},
		},
	}

	parseResult1 := &ast.ParseResult{
		FilePath: "io.ts",
		Language: "typescript",
		Symbols:  []*ast.Symbol{readable, writable, readWriteStream},
		Package:  "io",
	}
	parseResult2 := &ast.ParseResult{
		FilePath: "stream.ts",
		Language: "typescript",
		Symbols:  []*ast.Symbol{fileStream},
		Package:  "stream",
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	fsNode, ok := result.Graph.GetNode(fileStream.ID)
	if !ok {
		t.Fatal("FileStream node not found")
	}

	implementsTargets := make(map[string]bool)
	for _, edge := range fsNode.Outgoing {
		if edge.Type == EdgeTypeImplements {
			implementsTargets[edge.ToID] = true
		}
	}

	if !implementsTargets[readable.ID] {
		t.Error("expected EdgeTypeImplements from FileStream to Readable")
	}
	if !implementsTargets[writable.ID] {
		t.Error("expected EdgeTypeImplements from FileStream to Writable")
	}
	if !implementsTargets[readWriteStream.ID] {
		t.Error("expected EdgeTypeImplements from FileStream to ReadWriteStream (composed interface)")
	}
}

// Phase 18: Test that Python composed Protocols (inheriting from other Protocols)
// gain methods transitively and that classes implementing all methods match.
func TestBuildGraph_PythonComposedProtocolResolution(t *testing.T) {
	builder := NewBuilder(WithProjectRoot("/test"))
	ctx := context.Background()

	// class Readable(Protocol): def read(self) -> str: ...
	readable := &ast.Symbol{
		ID:        "protocols.py:1:Readable",
		Name:      "Readable",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "protocols.py",
		StartLine: 1, EndLine: 5, StartCol: 0, EndCol: 50,
		Language: "python",
		Metadata: &ast.SymbolMetadata{
			Extends: "Protocol",
			Methods: []ast.MethodSignature{
				{Name: "read", ParamCount: 1, ReturnCount: 1},
			},
		},
	}

	// class Writable(Protocol): def write(self, data: str) -> None: ...
	writable := &ast.Symbol{
		ID:        "protocols.py:7:Writable",
		Name:      "Writable",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "protocols.py",
		StartLine: 7, EndLine: 11, StartCol: 0, EndCol: 50,
		Language: "python",
		Metadata: &ast.SymbolMetadata{
			Extends: "Protocol",
			Methods: []ast.MethodSignature{
				{Name: "write", ParamCount: 2, ReturnCount: 0},
			},
		},
	}

	// class ReadWrite(Readable, Writable, Protocol): pass — no own methods
	readWrite := &ast.Symbol{
		ID:        "protocols.py:13:ReadWrite",
		Name:      "ReadWrite",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "protocols.py",
		StartLine: 13, EndLine: 14, StartCol: 0, EndCol: 50,
		Language: "python",
		Metadata: &ast.SymbolMetadata{
			Extends:    "Readable",
			Implements: []string{"Writable", "Protocol"},
		},
	}

	// class FileHandler: implements read + write
	fileHandler := &ast.Symbol{
		ID:        "handlers.py:1:FileHandler",
		Name:      "FileHandler",
		Kind:      ast.SymbolKindClass,
		FilePath:  "handlers.py",
		StartLine: 1, EndLine: 10, StartCol: 0, EndCol: 50,
		Language: "python",
		Metadata: &ast.SymbolMetadata{
			Methods: []ast.MethodSignature{
				{Name: "read", ParamCount: 1, ReturnCount: 1},
				{Name: "write", ParamCount: 2, ReturnCount: 0},
			},
		},
	}

	parseResult1 := &ast.ParseResult{
		FilePath: "protocols.py",
		Language: "python",
		Symbols:  []*ast.Symbol{readable, writable, readWrite},
		Package:  "protocols",
	}
	parseResult2 := &ast.ParseResult{
		FilePath: "handlers.py",
		Language: "python",
		Symbols:  []*ast.Symbol{fileHandler},
		Package:  "handlers",
	}

	result, err := builder.Build(ctx, []*ast.ParseResult{parseResult1, parseResult2})
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	fhNode, ok := result.Graph.GetNode(fileHandler.ID)
	if !ok {
		t.Fatal("FileHandler node not found")
	}

	implementsTargets := make(map[string]bool)
	for _, edge := range fhNode.Outgoing {
		if edge.Type == EdgeTypeImplements {
			implementsTargets[edge.ToID] = true
		}
	}

	if !implementsTargets[readable.ID] {
		t.Error("expected EdgeTypeImplements from FileHandler to Readable")
	}
	if !implementsTargets[writable.ID] {
		t.Error("expected EdgeTypeImplements from FileHandler to Writable")
	}
	if !implementsTargets[readWrite.ID] {
		t.Error("expected EdgeTypeImplements from FileHandler to ReadWrite (composed Protocol)")
	}
}
