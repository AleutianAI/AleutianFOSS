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

func TestFindPathTool_IT12Rev3(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForAnalytics(t)

	tool := NewFindPathTool(g, idx)

	t.Run("finds path between connected symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "funcB",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindPathOutput)
		if !ok {
			t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
		}
		if !output.Found {
			t.Error("expected Found=true for connected symbols")
		}
		if output.Length < 1 {
			t.Errorf("expected positive path length, got %d", output.Length)
		}
	})

	t.Run("no path between disconnected symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "funcD",
			"to":   "main",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed even with no path: %s", result.Error)
		}

		output, ok := result.Output.(FindPathOutput)
		if !ok {
			t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
		}
		if output.Found {
			t.Error("expected Found=false for disconnected symbols")
		}
	})

	t.Run("requires from parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"to": "funcA",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without 'from'")
		}
	})

	t.Run("requires to parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without 'to'")
		}
	})

	t.Run("rejects generic word", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "function",
			"to":   "main",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail for generic word 'function'")
		}
	})

	t.Run("nil graph returns error", func(t *testing.T) {
		nilTool := NewFindPathTool(nil, idx)
		result, err := nilTool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "funcA",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail with nil graph")
		}
	})

	t.Run("output text contains path info", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"from": "main",
			"to":   "funcA",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		if !strings.Contains(result.OutputText, "Path from") {
			t.Errorf("OutputText should contain path info, got: %s", result.OutputText)
		}
	})
}

// =============================================================================
// IT-12 Rev 3: Multi-candidate retry for find_path
// =============================================================================

// TestFindPath_RetriesAlternateCandidates verifies that when the first
// From/To resolution produces no path, the tool retries with alternate
// candidates and finds a valid path.
func TestFindPath_RetriesAlternateCandidates(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-retry-path")
	idx := index.NewSymbolIndex()

	// "Render" TYPE — no call edges, no path to anything
	renderType := &ast.Symbol{
		ID:        "src/types.ts:10:Render",
		Name:      "Render",
		Kind:      ast.SymbolKindType,
		FilePath:  "src/types.ts",
		StartLine: 10,
		EndLine:   15,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}
	// "Render" FUNCTION — connected to Display
	renderFunc := &ast.Symbol{
		ID:        "src/renderer.ts:50:Render",
		Name:      "Render",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/renderer.ts",
		StartLine: 50,
		EndLine:   80,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}
	// "Display" — target
	display := &ast.Symbol{
		ID:        "src/display.ts:10:Display",
		Name:      "Display",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/display.ts",
		StartLine: 10,
		EndLine:   30,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}

	g.AddNode(renderType)
	g.AddNode(renderFunc)
	g.AddNode(display)
	for _, sym := range []*ast.Symbol{renderType, renderFunc, display} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	// Only renderFunc → display has a path
	g.AddEdge(renderFunc.ID, display.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: renderFunc.FilePath, StartLine: 60,
	})
	g.Freeze()

	tool := NewFindPathTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"from": "Render",
		"to":   "Display",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindPathOutput)
	if !ok {
		t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
	}

	// IT-12 Rev 3: With multi-candidate retry, the tool should find the path
	// via Render FUNCTION → Display, even if Render TYPE was tried first.
	if !output.Found {
		t.Error("expected Found=true after retry with alternate candidates")
	}
	if output.Length < 1 {
		t.Errorf("expected path length >= 1, got %d", output.Length)
	}
}

func TestStripPackageQualifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "strips gin prefix", input: "gin.New", expected: "New"},
		{name: "strips flask prefix", input: "flask.Blueprint", expected: "Blueprint"},
		{name: "strips http prefix", input: "http.ListenAndServe", expected: "ListenAndServe"},
		{name: "strips pandas prefix", input: "pandas.DataFrame", expected: "DataFrame"},
		{name: "keeps Type.Method", input: "Engine.ServeHTTP", expected: "Engine.ServeHTTP"},
		{name: "keeps Context.JSON", input: "Context.JSON", expected: "Context.JSON"},
		{name: "keeps Plot.render", input: "Plot.render", expected: "Plot.render"},
		{name: "no dot returns as-is", input: "main", expected: "main"},
		{name: "unknown prefix kept", input: "MyType.Method", expected: "MyType.Method"},
		{name: "uppercase prefix kept as Type.Method", input: "GIN.New", expected: "GIN.New"},
		{name: "strips express prefix", input: "express.Router", expected: "Router"},
		{name: "strips badger prefix", input: "badger.Open", expected: "Open"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripPackageQualifier(tt.input)
			if result != tt.expected {
				t.Errorf("stripPackageQualifier(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFindPath_PackageQualifiedSymbol(t *testing.T) {
	ctx := context.Background()

	// Build a graph with "New" function
	g := graph.NewGraph("test")
	idx := index.NewSymbolIndex()

	newSym := &ast.Symbol{
		ID:        "gin.go:10:New",
		Name:      "New",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "gin.go",
		StartLine: 10,
		EndLine:   20,
		Language:  "go",
		Package:   "gin",
	}
	routeSym := &ast.Symbol{
		ID:        "routergroup.go:30:addRoute",
		Name:      "addRoute",
		Kind:      ast.SymbolKindMethod,
		FilePath:  "routergroup.go",
		StartLine: 30,
		EndLine:   40,
		Language:  "go",
		Package:   "gin",
	}

	if err := idx.Add(newSym); err != nil {
		t.Fatalf("failed to add New symbol: %v", err)
	}
	if err := idx.Add(routeSym); err != nil {
		t.Fatalf("failed to add addRoute symbol: %v", err)
	}
	g.AddNode(newSym)
	g.AddNode(routeSym)
	if err := g.AddEdge(newSym.ID, routeSym.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath:  "gin.go",
		StartLine: 15,
	}); err != nil {
		t.Fatalf("failed to add edge: %v", err)
	}

	tool := NewFindPathTool(g, idx)

	// Query with package-qualified name "gin.New"
	result, err := tool.Execute(ctx, FindPathParams{
		From: "gin.New",
		To:   "addRoute",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}

	output, ok := result.Output.(FindPathOutput)
	if !ok {
		t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
	}

	// stripPackageQualifier should turn "gin.New" → "New" before resolution
	if !output.Found {
		t.Error("expected Found=true — stripPackageQualifier should handle 'gin.New'")
	}
	if output.Length != 1 {
		t.Errorf("expected path length 1, got %d", output.Length)
	}
}

// =============================================================================
// IT-R2b: from==to guard tests
// =============================================================================

// TestFindPath_FromEqualsToGuard verifies that when both from and to resolve to
// the same symbol, the tool tries alternate candidates instead of returning a
// useless 0-hop self-reference.
func TestFindPath_FromEqualsToGuard(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-from-eq-to")
	idx := index.NewSymbolIndex()

	// Two symbols named "render" in different files
	renderA := &ast.Symbol{
		ID:        "src/engine.ts:10:render",
		Name:      "render",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/engine.ts",
		StartLine: 10,
		EndLine:   30,
		Package:   "engine",
		Exported:  true,
		Language:  "typescript",
	}
	renderB := &ast.Symbol{
		ID:        "src/scene.ts:20:render",
		Name:      "render",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/scene.ts",
		StartLine: 20,
		EndLine:   50,
		Package:   "scene",
		Exported:  true,
		Language:  "typescript",
	}

	g.AddNode(renderA)
	g.AddNode(renderB)
	for _, sym := range []*ast.Symbol{renderA, renderB} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	// renderA → renderB (connected)
	g.AddEdge(renderA.ID, renderB.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: renderA.FilePath, StartLine: 15,
	})
	g.Freeze()

	tool := NewFindPathTool(g, idx)

	// Both "from" and "to" are "render" — without the guard, both resolve to
	// the same symbol (renderA, which has more edges). The guard should swap
	// the to-candidate to renderB.
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"from": "render",
		"to":   "render",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindPathOutput)
	if !ok {
		t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
	}

	// The guard should find a path between the two different render symbols
	if !output.Found {
		t.Error("expected Found=true — from==to guard should swap to alternate candidate")
	}
	if output.Length < 1 {
		t.Errorf("expected path length >= 1, got %d", output.Length)
	}
}

// TestFindPath_RetryLoopSkipsSameID verifies that the retry loop skips
// combinations where fromCandidate.ID == toCandidate.ID.
func TestFindPath_RetryLoopSkipsSameID(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-retry-skip-same")
	idx := index.NewSymbolIndex()

	// "Init" TYPE — no edges, primary candidate for both
	initType := &ast.Symbol{
		ID:        "src/types.ts:5:Init",
		Name:      "Init",
		Kind:      ast.SymbolKindType,
		FilePath:  "src/types.ts",
		StartLine: 5,
		EndLine:   10,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}
	// "Init" FUNCTION — connected to "Setup"
	initFunc := &ast.Symbol{
		ID:        "src/init.ts:20:Init",
		Name:      "Init",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/init.ts",
		StartLine: 20,
		EndLine:   40,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}
	setup := &ast.Symbol{
		ID:        "src/setup.ts:10:Setup",
		Name:      "Setup",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "src/setup.ts",
		StartLine: 10,
		EndLine:   30,
		Package:   "core",
		Exported:  true,
		Language:  "typescript",
	}

	g.AddNode(initType)
	g.AddNode(initFunc)
	g.AddNode(setup)
	for _, sym := range []*ast.Symbol{initType, initFunc, setup} {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.ID, err)
		}
	}

	// initFunc → setup has a path
	g.AddEdge(initFunc.ID, setup.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: initFunc.FilePath, StartLine: 25,
	})
	g.Freeze()

	tool := NewFindPathTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"from": "Init",
		"to":   "Setup",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindPathOutput)
	if !ok {
		t.Fatalf("Output is not FindPathOutput, got %T", result.Output)
	}

	// Should find the path via initFunc → setup
	if !output.Found {
		t.Error("expected Found=true — retry should find path via Init function")
	}
}

// =============================================================================
// IT-R2b: Constructor is no longer a generic word
// =============================================================================

func TestFindPath_ConstructorNotGeneric(t *testing.T) {
	// "constructor" was previously in genericWords and would be rejected.
	// After IT-R2b Fix 2, it should pass validation.
	err := ValidateSymbolName("constructor", "from", "'main', 'handleRequest'")
	if err != nil {
		t.Errorf("ValidateSymbolName(\"constructor\") should pass after IT-R2b Fix 2, got: %v", err)
	}
}
