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

func TestFindCalleesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, idx)

	t.Run("finds callees of main", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "main",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// main calls parseConfig
		output, ok := result.Output.(FindCalleesOutput)
		if !ok {
			t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
		}

		if len(output.ResolvedCallees) != 1 {
			t.Errorf("got %d resolved callees, want 1", len(output.ResolvedCallees))
		}
	})

	t.Run("requires function_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without function_name")
		}
	})
}

// TestFindCalleesTool_NilIndexFallback tests nil index fallback for find_callees.
func TestFindCalleesTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, nil)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find callees via graph fallback
	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	if len(output.ResolvedCallees) != 1 {
		t.Errorf("got %d resolved callees, want 1", len(output.ResolvedCallees))
	}
}

// TestFindCalleesTool_MultipleMatches tests multiple symbol matches.
func TestFindCalleesTool_MultipleMatches(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMultipleMatches(t)

	tool := NewFindCalleesTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	// main has 3 callees (three Setup functions)
	if len(output.ResolvedCallees) != 3 {
		t.Errorf("got %d resolved callees, want 3", len(output.ResolvedCallees))
	}
}

// BenchmarkFindCallees_WithIndex benchmarks find_callees using O(1) index lookup.
func BenchmarkFindCallees_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindCalleesTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Function5000",
		}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// BenchmarkFindCallees_WithoutIndex benchmarks find_callees using O(V) graph scan.
func BenchmarkFindCallees_WithoutIndex(b *testing.B) {
	g, _ := createLargeGraph(b, 10000)
	tool := NewFindCalleesTool(g, nil) // nil index forces graph fallback
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Function5000",
		}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// TestFindCalleesTool_ContextCancellation tests context cancellation for find_callees.
func TestFindCalleesTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithMultipleMatches(t)
	tool := NewFindCalleesTool(g, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
	}})

	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
}

// TestFindCalleesTool_DotNotationResolution tests Type.Method dot-notation resolution.
func TestFindCalleesTool_DotNotationResolution(t *testing.T) {
	g, idx := createTestGraphForDotNotation(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	t.Run("resolves Type.Method when method exists on type", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Server.Start",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCalleesOutput)
		if !ok {
			t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
		}
		if output.TotalCount == 0 {
			t.Error("Expected callees for Server.Start, got none")
		}
		if output.ResolvedCount < 1 {
			t.Errorf("Expected at least 1 resolved callee, got %d", output.ResolvedCount)
		}
	})

	t.Run("IT-02 C-1: falls back to bare name when dot-notation fails", func(t *testing.T) {
		// "DB.Open" where Open is a package-level function, not a method on DB
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "DB.Open",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCalleesOutput)
		if !ok {
			t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
		}
		// Should find callees via bare-name fallback to "Open"
		if output.TotalCount == 0 {
			t.Error("Expected callees for Open (bare name fallback), got none")
		}
	})
}

// TestFindCalleesTool_ExternalCallees tests external/stdlib callee classification.
func TestFindCalleesTool_ExternalCallees(t *testing.T) {
	g, idx := createTestGraphForDotNotation(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "Open",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	// Open calls both in-codebase and external functions
	if output.ExternalCount == 0 {
		t.Error("Expected external callees, got none")
	}
	if output.ResolvedCount == 0 {
		t.Error("Expected resolved callees, got none")
	}
}

// TestFindCalleesTool_EmptyCallees tests the empty-result message formatting.
func TestFindCalleesTool_EmptyCallees(t *testing.T) {
	g, idx := createTestGraphForDotNotation(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "leafFunction",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	if output.TotalCount != 0 {
		t.Errorf("Expected 0 callees for leaf function, got %d", output.TotalCount)
	}

	// Verify output text contains the "no callees" message
	if !strings.Contains(result.OutputText, "Callees of 'leafFunction' not found") {
		t.Errorf("OutputText should contain 'Callees not found' message, got: %s", result.OutputText)
	}
}

// TestFindCalleesTool_LimitClamping tests limit parameter edge cases.
func TestFindCalleesTool_LimitClamping(t *testing.T) {
	g, idx := createTestGraphForDotNotation(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	t.Run("limit zero clamped to 1", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Open",
			"limit":         0,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})

	t.Run("limit above 1000 clamped", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Open",
			"limit":         5000,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
	})
}

// TestFindCalleesTool_ResolvedDedup tests that resolved callees are deduplicated (L-1).
func TestFindCalleesTool_ResolvedDedup(t *testing.T) {
	g, idx := createTestGraphWithDuplicateCallees(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "dispatch",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	// Both "dispatch" symbols call "process" — but process should only appear once
	if output.ResolvedCount != 1 {
		t.Errorf("Expected 1 unique resolved callee (process), got %d", output.ResolvedCount)
	}
}

// TestFindCalleesTool_FormatTextOutput verifies text formatting for various scenarios.
func TestFindCalleesTool_FormatTextOutput(t *testing.T) {
	g, idx := createTestGraphForDotNotation(t)
	ctx := context.Background()
	tool := NewFindCalleesTool(g, idx)

	t.Run("output contains In-Codebase and External sections", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Open",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		if !strings.Contains(result.OutputText, "In-Codebase Callees") {
			t.Error("OutputText should contain 'In-Codebase Callees' section")
		}
		if !strings.Contains(result.OutputText, "External/Stdlib Callees") {
			t.Error("OutputText should contain 'External/Stdlib Callees' section")
		}
	})
}

// TestFindCalleesTool_StaticDefinitions verifies find_callees is in StaticToolDefinitions.
func TestFindCalleesTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "find_callees" {
			found = true
			break
		}
	}
	if !found {
		t.Error("find_callees not found in StaticToolDefinitions()")
	}
}

// TestFindCalleesTool_TraceStepPopulated verifies CRS integration on success path.
func TestFindCalleesTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}
	if result.TraceStep.Action != "tool_find_callees" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_callees'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_callees" {
		t.Errorf("TraceStep.Tool = %q, want 'find_callees'", result.TraceStep.Tool)
	}
	if result.TraceStep.Target != "main" {
		t.Errorf("TraceStep.Target = %q, want 'main'", result.TraceStep.Target)
	}
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}

	if result.TraceStep.Metadata == nil {
		t.Fatal("TraceStep.Metadata should not be nil")
	}
	for _, key := range []string{"resolved_count", "external_count", "total_count"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestFindCalleesTool_TraceStepOnError verifies CRS integration on validation error path.
func TestFindCalleesTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty function_name")
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_find_callees" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_callees'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_callees" {
		t.Errorf("TraceStep.Tool = %q, want 'find_callees'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}

// TestFindCallees_DefinitiveFooter verifies the success path includes the definitive answer footer.
func TestFindCallees_DefinitiveFooter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCalleesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
		"limit":         50,
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// If there are callees, check for definitive footer
	output, ok := result.Output.(FindCalleesOutput)
	if ok && output.TotalCount > 0 {
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected definitive footer in success path output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read' in success path output")
		}
	}
}

// TestFindCallees_TypeAliasMessage verifies IT-06b Issue 3: when the resolved symbol
// is a type alias (SymbolKindType), the zero-result message explains it's a type alias
// rather than saying "does not call any other functions".
func TestFindCallees_TypeAliasMessage(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// HandlerFunc is a type alias — no callable body, no outgoing edges.
	handlerFunc := &ast.Symbol{
		ID: "gin/gin.go:20:HandlerFunc", Name: "HandlerFunc",
		Kind: ast.SymbolKindType, FilePath: "gin/gin.go",
		StartLine: 20, EndLine: 20, Package: "gin", Language: "go",
	}
	g.AddNode(handlerFunc)
	if err := idx.Add(handlerFunc); err != nil {
		t.Fatalf("failed to add: %v", err)
	}
	g.Freeze()

	tool := NewFindCalleesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"function_name": "HandlerFunc"}})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	// ResolvedKind should be "type"
	if output.ResolvedKind != ast.SymbolKindType.String() {
		t.Errorf("expected ResolvedKind=%q, got %q", ast.SymbolKindType.String(), output.ResolvedKind)
	}

	// Output text should contain the type alias explanation
	if !strings.Contains(result.OutputText, "is a type alias") {
		t.Errorf("expected 'is a type alias' in output, got: %s", result.OutputText)
	}
	if strings.Contains(result.OutputText, "does not call any other functions") {
		t.Errorf("type alias should NOT get generic 'does not call' message, got: %s", result.OutputText)
	}
	if !strings.Contains(result.OutputText, "find_references") {
		t.Errorf("expected 'find_references' suggestion in output, got: %s", result.OutputText)
	}
}

// TestFindCallees_ResolvedKindEmpty_WhenNotFound verifies that ResolvedKind is empty
// (not "unknown") when no symbol is found in the index.
func TestFindCallees_ResolvedKindEmpty_WhenNotFound(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()
	g.Freeze()

	tool := NewFindCalleesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"function_name": "NonExistentSymbol"}})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output, ok := result.Output.(FindCalleesOutput)
	if !ok {
		t.Fatalf("Output is not FindCalleesOutput, got %T", result.Output)
	}

	if output.ResolvedKind != "" {
		t.Errorf("expected empty ResolvedKind for unresolved symbol, got %q", output.ResolvedKind)
	}
}

// =============================================================================
// Local helpers for find_callees tests
// =============================================================================

// createTestGraphForDotNotation creates a test graph with:
// - Server.Start method that calls logInfo and listen
// - Open package-level function (NOT a method on DB) that calls validateOpts and externalLib
// - leafFunction with no callees
// - externalLib as an external symbol (no file path)
func createTestGraphForDotNotation(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Server type
	server := &ast.Symbol{
		ID: "server/server.go:5:Server", Name: "Server",
		Kind: ast.SymbolKindStruct, FilePath: "server/server.go",
		StartLine: 5, EndLine: 10, Package: "server", Language: "go",
	}

	// Server.Start method (has Receiver set)
	serverStart := &ast.Symbol{
		ID: "server/server.go:15:Start", Name: "Start",
		Kind: ast.SymbolKindMethod, FilePath: "server/server.go",
		StartLine: 15, EndLine: 30, Package: "server", Language: "go",
		Receiver: "Server",
	}

	// Package-level Open function (NOT a method on DB)
	openFunc := &ast.Symbol{
		ID: "db/db.go:10:Open", Name: "Open",
		Kind: ast.SymbolKindFunction, FilePath: "db/db.go",
		StartLine: 10, EndLine: 50, Package: "db", Language: "go",
		Signature: "func Open(opt Options) (*DB, error)",
	}

	// DB type (exists but Open is NOT a method on it)
	dbType := &ast.Symbol{
		ID: "db/db.go:5:DB", Name: "DB",
		Kind: ast.SymbolKindStruct, FilePath: "db/db.go",
		StartLine: 5, EndLine: 8, Package: "db", Language: "go",
	}

	// logInfo — in-codebase callee
	logInfo := &ast.Symbol{
		ID: "log/logger.go:20:logInfo", Name: "logInfo",
		Kind: ast.SymbolKindFunction, FilePath: "log/logger.go",
		StartLine: 20, EndLine: 25, Package: "log", Language: "go",
	}

	// listen — in-codebase callee
	listen := &ast.Symbol{
		ID: "server/server.go:40:listen", Name: "listen",
		Kind: ast.SymbolKindFunction, FilePath: "server/server.go",
		StartLine: 40, EndLine: 50, Package: "server", Language: "go",
	}

	// validateOpts — in-codebase callee for Open
	validateOpts := &ast.Symbol{
		ID: "db/validate.go:5:validateOpts", Name: "validateOpts",
		Kind: ast.SymbolKindFunction, FilePath: "db/validate.go",
		StartLine: 5, EndLine: 15, Package: "db", Language: "go",
	}

	// externalLib — external callee (no file path)
	externalLib := &ast.Symbol{
		ID: "external:os:MkdirAll", Name: "MkdirAll",
		Kind: ast.SymbolKindExternal, FilePath: "",
		StartLine: 0, EndLine: 0, Package: "os", Language: "go",
	}

	// leafFunction — no outgoing calls
	leafFunction := &ast.Symbol{
		ID: "util/leaf.go:5:leafFunction", Name: "leafFunction",
		Kind: ast.SymbolKindFunction, FilePath: "util/leaf.go",
		StartLine: 5, EndLine: 10, Package: "util", Language: "go",
	}

	// Add all nodes to graph and index (external symbols only to graph, not index)
	indexable := []*ast.Symbol{server, serverStart, openFunc, dbType, logInfo, listen, validateOpts, leafFunction}
	for _, sym := range indexable {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}
	// External symbol only in graph (index rejects empty FilePath)
	g.AddNode(externalLib)

	// Edges: Server.Start calls logInfo, listen
	g.AddEdge(serverStart.ID, logInfo.ID, graph.EdgeTypeCalls, ast.Location{FilePath: serverStart.FilePath, StartLine: 20})
	g.AddEdge(serverStart.ID, listen.ID, graph.EdgeTypeCalls, ast.Location{FilePath: serverStart.FilePath, StartLine: 25})

	// Edges: Open calls validateOpts, MkdirAll (external)
	g.AddEdge(openFunc.ID, validateOpts.ID, graph.EdgeTypeCalls, ast.Location{FilePath: openFunc.FilePath, StartLine: 15})
	g.AddEdge(openFunc.ID, externalLib.ID, graph.EdgeTypeCalls, ast.Location{FilePath: openFunc.FilePath, StartLine: 20})

	g.Freeze()
	return g, idx
}

// createTestGraphWithDuplicateCallees creates a graph where two "dispatch" symbols
// both call the same "process" symbol, testing L-1 deduplication.
func createTestGraphWithDuplicateCallees(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	dispatch1 := &ast.Symbol{
		ID: "cmd/a.go:10:dispatch", Name: "dispatch",
		Kind: ast.SymbolKindFunction, FilePath: "cmd/a.go",
		StartLine: 10, EndLine: 20, Package: "cmd", Language: "go",
	}
	dispatch2 := &ast.Symbol{
		ID: "cmd/b.go:10:dispatch", Name: "dispatch",
		Kind: ast.SymbolKindFunction, FilePath: "cmd/b.go",
		StartLine: 10, EndLine: 20, Package: "cmd", Language: "go",
	}
	process := &ast.Symbol{
		ID: "core/process.go:5:process", Name: "process",
		Kind: ast.SymbolKindFunction, FilePath: "core/process.go",
		StartLine: 5, EndLine: 15, Package: "core", Language: "go",
	}

	for _, sym := range []*ast.Symbol{dispatch1, dispatch2, process} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}

	// Both dispatch symbols call the same process
	g.AddEdge(dispatch1.ID, process.ID, graph.EdgeTypeCalls, ast.Location{FilePath: dispatch1.FilePath, StartLine: 15})
	g.AddEdge(dispatch2.ID, process.ID, graph.EdgeTypeCalls, ast.Location{FilePath: dispatch2.FilePath, StartLine: 15})

	g.Freeze()
	return g, idx
}
