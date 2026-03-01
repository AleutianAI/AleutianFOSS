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

func TestFindSymbolTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindSymbolTool(g, idx)

	t.Run("finds symbol by name", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "parseConfig",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}
	})

	t.Run("filters by kind", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "Handler",
			"kind": "interface",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}
	})

	t.Run("requires name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without name")
		}
	})
}

// TestFindSymbolTool_KindClassAndStruct tests that find_symbol accepts kind=class
// and kind=struct filters (IT-03 C-2).
func TestFindSymbolTool_KindClassAndStruct(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Python-style class
	pyClass := &ast.Symbol{
		ID:        "app/models.py:10:UserModel",
		Name:      "UserModel",
		Kind:      ast.SymbolKindClass,
		FilePath:  "app/models.py",
		StartLine: 10,
		EndLine:   30,
		Package:   "app",
		Language:  "python",
	}
	// Go struct with same name
	goStruct := &ast.Symbol{
		ID:        "pkg/models.go:5:UserModel",
		Name:      "UserModel",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/models.go",
		StartLine: 5,
		EndLine:   20,
		Package:   "pkg",
		Language:  "go",
	}
	// Function with same name (should be filtered out)
	fn := &ast.Symbol{
		ID:        "util/factory.go:1:UserModel",
		Name:      "UserModel",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "util/factory.go",
		StartLine: 1,
		EndLine:   5,
		Package:   "util",
		Language:  "go",
	}

	g.AddNode(pyClass)
	g.AddNode(goStruct)
	g.AddNode(fn)
	_ = idx.Add(pyClass)
	_ = idx.Add(goStruct)
	_ = idx.Add(fn)
	g.Freeze()

	tool := NewFindSymbolTool(g, idx)

	t.Run("kind=class returns class and struct symbols", func(t *testing.T) {
		// IT-04: "class" cross-matches Struct because the router may send "class"
		// for Go structs, or "struct" for JS/Python classes.
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "UserModel",
			"kind": "class",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindSymbolOutput)
		if output.MatchCount != 2 {
			t.Errorf("kind=class: got %d matches, want 2 (class + struct)", output.MatchCount)
		}
	})

	t.Run("kind=struct returns struct and class symbols", func(t *testing.T) {
		// IT-04: "struct" cross-matches Class because JS/Python/TS don't have structs;
		// their classes are the equivalent construct.
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "UserModel",
			"kind": "struct",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindSymbolOutput)
		if output.MatchCount != 2 {
			t.Errorf("kind=struct: got %d matches, want 2 (struct + class)", output.MatchCount)
		}
	})

	t.Run("kind=all returns all symbols", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "UserModel",
			"kind": "all",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output := result.Output.(FindSymbolOutput)
		if output.MatchCount != 3 {
			t.Errorf("kind=all: got %d matches, want 3", output.MatchCount)
		}
	})
}

func TestFindSymbolTool_PartialMatch(t *testing.T) {
	ctx := context.Background()

	// Create test index with symbol "getDatesToProcess"
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	sym := &ast.Symbol{
		ID:        "processor/dates.go:10:getDatesToProcess",
		Name:      "getDatesToProcess",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "processor/dates.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "processor",
		Signature: "func getDatesToProcess() []time.Time",
		Exported:  false,
		Language:  "go",
	}

	// Add to graph and index
	g.AddNode(sym)
	if err := idx.Add(sym); err != nil {
		t.Fatalf("Failed to add symbol to index: %v", err)
	}

	tool := NewFindSymbolTool(g, idx)

	t.Run("exact match works", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "getDatesToProcess",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}

		// Exact match should NOT have fuzzy warning
		outputText := result.OutputText
		if strings.Contains(outputText, "⚠️") {
			t.Errorf("Exact match should not have fuzzy warning")
		}
	})

	t.Run("partial match finds it", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "Process", // Partial match
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount < 1 {
			t.Errorf("got %d matches, want at least 1", output.MatchCount)
		}

		// Check that getDatesToProcess is in the results
		found := false
		for _, match := range output.Symbols {
			if match.Name == "getDatesToProcess" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected to find 'getDatesToProcess' in partial match results")
		}

		// Partial match should have fuzzy warning
		outputText := result.OutputText
		if !strings.Contains(outputText, "⚠️") {
			t.Errorf("Partial match should have fuzzy warning, got: %s", outputText)
		}
	})

	t.Run("no match at all", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "NonExistentXYZ",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindSymbolOutput)
		if !ok {
			t.Fatalf("Output is not FindSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 0 {
			t.Errorf("got %d matches, want 0", output.MatchCount)
		}

		// No matches should show "No symbols found"
		outputText := result.OutputText
		if !strings.Contains(outputText, "No symbols found") {
			t.Errorf("Expected 'No symbols found' message, got: %s", outputText)
		}
	})
}

func TestFindSymbolTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "find_symbol" {
			found = true
			if def.Parameters["name"].Required != true {
				t.Error("find_symbol 'name' parameter should be required")
			}
			break
		}
	}
	if !found {
		t.Error("find_symbol not found in StaticToolDefinitions()")
	}
}

// TestFindSymbolTool_TraceStepPopulated verifies CRS integration on success path.
func TestFindSymbolTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindSymbolTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"name": "parseConfig",
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
	if result.TraceStep.Action != "tool_find_symbol" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_symbol'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_symbol" {
		t.Errorf("TraceStep.Tool = %q, want 'find_symbol'", result.TraceStep.Tool)
	}
	if result.TraceStep.Target != "parseConfig" {
		t.Errorf("TraceStep.Target = %q, want 'parseConfig'", result.TraceStep.Target)
	}
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}

	if result.TraceStep.Metadata == nil {
		t.Fatal("TraceStep.Metadata should not be nil")
	}
	for _, key := range []string{"match_count", "used_fuzzy", "kind_filter"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestFindSymbolTool_TraceStepOnError verifies CRS integration on validation error path.
func TestFindSymbolTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindSymbolTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty name")
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_find_symbol" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_symbol'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_symbol" {
		t.Errorf("TraceStep.Tool = %q, want 'find_symbol'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}
