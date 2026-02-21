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
)

// L2: Add find_references benchmark
func BenchmarkFindReferences_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindReferencesTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "Function5000",
		}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// TestFindReferencesTool_TraceStepPopulated verifies CRS integration on success path.
func TestFindReferencesTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "parseConfig",
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
	if result.TraceStep.Action != "tool_find_references" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_references'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_references" {
		t.Errorf("TraceStep.Tool = %q, want 'find_references'", result.TraceStep.Tool)
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
	// IT-06 Change 4: Enhanced metadata keys
	for _, key := range []string{"reference_count", "symbol_resolved", "resolved_name", "symbol_kind", "fuzzy_match"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}
	if result.TraceStep.Metadata["symbol_resolved"] != "true" {
		t.Errorf("TraceStep.Metadata[symbol_resolved] = %q, want 'true'", result.TraceStep.Metadata["symbol_resolved"])
	}
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestFindReferencesTool_TraceStepOnError verifies CRS integration on validation error path.
func TestFindReferencesTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty symbol_name")
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_find_references" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_references'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_references" {
		t.Errorf("TraceStep.Tool = %q, want 'find_references'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}

// TestFindReferences_SharedResolution verifies IT-06 Bug 1 fix: shared resolution
// with fuzzy match and KindFilterAny replaces inline GetByName.
func TestFindReferences_SharedResolution(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)

	t.Run("exact match resolves and finds references", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "parseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindReferencesOutput)
		if !ok {
			t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
		}

		// parseConfig has 3 callers (main, initServer, LoadConfig) = 3 reference edges
		if output.ReferenceCount != 3 {
			t.Errorf("ReferenceCount = %d, want 3", output.ReferenceCount)
		}
		if output.ResolvedName != "parseConfig" {
			t.Errorf("ResolvedName = %q, want 'parseConfig'", output.ResolvedName)
		}
		if output.SymbolKind != "function" {
			t.Errorf("SymbolKind = %q, want 'function'", output.SymbolKind)
		}
		if output.DefinedAt != "config/parser.go:10" {
			t.Errorf("DefinedAt = %q, want 'config/parser.go:10'", output.DefinedAt)
		}
	})

	t.Run("resolves interface symbols with KindFilterAny", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "Handler",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindReferencesOutput)
		if !ok {
			t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
		}

		// Handler has 1 incoming edge (UserHandler implements Handler)
		if output.ReferenceCount != 1 {
			t.Errorf("ReferenceCount = %d, want 1", output.ReferenceCount)
		}
		if output.SymbolKind != "interface" {
			t.Errorf("SymbolKind = %q, want 'interface'", output.SymbolKind)
		}
	})

	t.Run("resolves struct symbols with KindFilterAny", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "UserHandler",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindReferencesOutput)
		if !ok {
			t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
		}

		if output.ResolvedName != "UserHandler" {
			t.Errorf("ResolvedName = %q, want 'UserHandler'", output.ResolvedName)
		}
		if output.SymbolKind != "struct" {
			t.Errorf("SymbolKind = %q, want 'struct'", output.SymbolKind)
		}
	})
}

// TestFindReferences_NotFoundMessage verifies IT-06 Bug 2/4 fix: distinct "not found"
// message when symbol cannot be resolved, with pattern matching synthesis gate.
func TestFindReferences_NotFoundMessage(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "CompletelyNonExistentSymbol",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed (not-found is a valid graph result)")
	}

	// Bug 4: Output must contain "not found" for synthesis gate (GR-59 Rev 4b)
	if !strings.Contains(result.OutputText, "not found") {
		t.Errorf("OutputText should contain 'not found' for synthesis gate, got:\n%s", result.OutputText)
	}

	// Bug 2: Must say symbol not found, not "no references"
	if !strings.Contains(result.OutputText, "not found in the codebase") {
		t.Errorf("OutputText should contain 'not found in the codebase' for symbol-not-found path, got:\n%s", result.OutputText)
	}

	// Bug 3: Definitive footer
	if !strings.Contains(result.OutputText, "Do NOT use Grep") {
		t.Errorf("OutputText should contain definitive footer, got:\n%s", result.OutputText)
	}

	// TraceStep should indicate symbol_resolved=false
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated")
	}
	if result.TraceStep.Metadata["symbol_resolved"] != "false" {
		t.Errorf("TraceStep.Metadata[symbol_resolved] = %q, want 'false'",
			result.TraceStep.Metadata["symbol_resolved"])
	}

	// Output struct should have 0 references
	output, ok := result.Output.(FindReferencesOutput)
	if !ok {
		t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
	}
	if output.ReferenceCount != 0 {
		t.Errorf("ReferenceCount = %d, want 0", output.ReferenceCount)
	}
}

// TestFindReferences_ZeroRefsMessage verifies IT-06 Bug 2 fix: distinct message when
// symbol is resolved but has 0 incoming reference edges.
func TestFindReferences_ZeroRefsMessage(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)

	// "main" exists in the graph but has no incoming edges (it's an entry point)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "main",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should mention the symbol exists but has no references
	if !strings.Contains(result.OutputText, "not found") {
		t.Errorf("OutputText should contain 'not found' for synthesis gate, got:\n%s", result.OutputText)
	}
	if !strings.Contains(result.OutputText, "no incoming reference edges") {
		t.Errorf("OutputText should explain 'no incoming reference edges', got:\n%s", result.OutputText)
	}

	// Should include definition location
	if !strings.Contains(result.OutputText, "cmd/app/main.go:5") {
		t.Errorf("OutputText should contain definition location 'cmd/app/main.go:5', got:\n%s", result.OutputText)
	}

	// Bug 3: Definitive footer
	if !strings.Contains(result.OutputText, "Do NOT use Grep") {
		t.Errorf("OutputText should contain definitive footer, got:\n%s", result.OutputText)
	}

	// Output struct should have definition info
	output, ok := result.Output.(FindReferencesOutput)
	if !ok {
		t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
	}
	if output.ReferenceCount != 0 {
		t.Errorf("ReferenceCount = %d, want 0", output.ReferenceCount)
	}
	if output.ResolvedName != "main" {
		t.Errorf("ResolvedName = %q, want 'main'", output.ResolvedName)
	}
	if output.DefinedAt != "cmd/app/main.go:5" {
		t.Errorf("DefinedAt = %q, want 'cmd/app/main.go:5'", output.DefinedAt)
	}
}

// TestFindReferences_DefinitiveFooter verifies IT-06 Bug 3 fix: all output paths
// include a definitive footer to prevent Grep spirals.
func TestFindReferences_DefinitiveFooter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)

	t.Run("success path has footer", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "parseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected definitive footer in success path output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read' in success path output")
		}
	})

	t.Run("zero refs path has footer", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "main",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep") {
			t.Error("expected definitive footer in zero-refs path output")
		}
	})

	t.Run("not found path has footer", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_name": "CompletelyNonExistentSymbol",
		}})
		if err != nil {
			t.Fatalf("Execute() returned error: %v", err)
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep") {
			t.Error("expected definitive footer in not-found path output")
		}
	})
}

// TestFindReferences_DefinitionLocation verifies IT-06 Change 3: resolved symbol
// definition location, kind, and name are included in structured output.
func TestFindReferences_DefinitionLocation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindReferencesTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_name": "parseConfig",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindReferencesOutput)
	if !ok {
		t.Fatalf("Output is not FindReferencesOutput, got %T", result.Output)
	}

	if output.DefinedAt != "config/parser.go:10" {
		t.Errorf("DefinedAt = %q, want 'config/parser.go:10'", output.DefinedAt)
	}
	if output.SymbolKind != "function" {
		t.Errorf("SymbolKind = %q, want 'function'", output.SymbolKind)
	}
	if output.ResolvedName != "parseConfig" {
		t.Errorf("ResolvedName = %q, want 'parseConfig'", output.ResolvedName)
	}

	// Text output should include definition location
	if !strings.Contains(result.OutputText, "config/parser.go:10") {
		t.Errorf("OutputText should contain definition location, got:\n%s", result.OutputText)
	}
}
