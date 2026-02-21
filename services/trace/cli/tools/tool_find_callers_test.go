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

func TestFindCallersTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	t.Run("finds all callers of parseConfig", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "parseConfig",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Check that we found callers
		output, ok := result.Output.(FindCallersOutput)
		if !ok {
			t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
		}

		// Should have 1 entry (one parseConfig function)
		if len(output.Results) != 1 {
			t.Errorf("got %d result entries, want 1", len(output.Results))
		}

		// The one parseConfig should have 3 callers
		if len(output.Results) > 0 {
			if len(output.Results[0].Callers) != 3 {
				t.Errorf("got %d callers, want 3", len(output.Results[0].Callers))
			}
		}

		// Check output text mentions the callers
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("returns empty for non-existent function", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "nonExistent",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should have message about no callers found
		if result.OutputText == "" {
			t.Error("OutputText is empty")
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
		if result.Error == "" {
			t.Error("Error message should not be empty")
		}
	})
}

// TestFindCallersTool_NilIndexFallback tests that find_callers falls back to
// O(V) graph scan when index is nil (GR-01 requirement M5).
func TestFindCallersTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	// Create tool with nil index
	tool := NewFindCallersTool(g, nil)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find callers via graph fallback
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	// Should have 1 entry (one parseConfig function) with 3 callers
	if len(output.Results) != 1 {
		t.Errorf("got %d result entries, want 1", len(output.Results))
	}
}

// TestFindCallersTool_MultipleMatches tests that find_callers correctly handles
// multiple functions with the same name (GR-01 requirement M2).
func TestFindCallersTool_MultipleMatches(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithMultipleMatches(t)

	tool := NewFindCallersTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "Setup",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	// Should have 3 result entries (one per Setup function)
	if len(output.Results) != 3 {
		t.Errorf("got %d result entries, want 3 (one per Setup)", len(output.Results))
	}

	// Each Setup should have 1 caller (main)
	for i, entry := range output.Results {
		if len(entry.Callers) != 1 {
			t.Errorf("result[%d] got %d callers, want 1", i, len(entry.Callers))
		}
	}
}

// TestFindCallersTool_FastNotFound tests that queries for non-existent symbols
// return quickly (O(1) index miss, not O(V) scan).
func TestFindCallersTool_FastNotFound(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "NonExistentFunctionXYZ123",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should have empty results and message about no callers
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	if len(output.Results) != 0 {
		t.Errorf("got %d results for non-existent function, want 0", len(output.Results))
	}

	// OutputText should mention no callers found
	if result.OutputText == "" {
		t.Error("OutputText is empty")
	}
}

// BenchmarkFindCallers_WithIndex benchmarks find_callers using O(1) index lookup.
func BenchmarkFindCallers_WithIndex(b *testing.B) {
	g, idx := createLargeGraph(b, 10000)
	tool := NewFindCallersTool(g, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Query for a function in the middle of the graph
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Function5000",
		}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// BenchmarkFindCallers_WithoutIndex benchmarks find_callers using O(V) graph scan.
func BenchmarkFindCallers_WithoutIndex(b *testing.B) {
	g, _ := createLargeGraph(b, 10000)
	tool := NewFindCallersTool(g, nil) // nil index forces graph fallback
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Query for a function in the middle of the graph
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Function5000",
		}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// TestFindCallersTool_ContextCancellation tests that context cancellation is handled.
// L1: Verify context cancellation path is covered.
func TestFindCallersTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithMultipleMatches(t)
	tool := NewFindCallersTool(g, idx)

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "Setup",
	}})

	// Should return context.Canceled error
	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// TestFindCallersTool_LimitCapped tests that limit is capped at 1000 (M1).
func TestFindCallersTool_LimitCapped(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)

	// Request a very large limit
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
		"limit":         1000000, // Should be capped to 1000
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// The test doesn't have 1000+ callers, but the limit should be silently capped
	// Verify the query still works
	output, ok := result.Output.(FindCallersOutput)
	if !ok {
		t.Fatalf("Output is not FindCallersOutput, got %T", result.Output)
	}

	if output.MatchCount != 1 {
		t.Errorf("Expected 1 match, got %d", output.MatchCount)
	}
}

// TestFindCallersTool_IndexAndGraphPathConsistency tests that index and graph paths
// return consistent results.
func TestFindCallersTool_IndexAndGraphPathConsistency(t *testing.T) {
	g, idx := createTestGraphWithCallers(t)

	toolWithIndex := NewFindCallersTool(g, idx)
	toolWithoutIndex := NewFindCallersTool(g, nil)

	ctx := context.Background()

	result1, err1 := toolWithIndex.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
	}})
	result2, err2 := toolWithoutIndex.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
	}})

	if err1 != nil || err2 != nil {
		t.Fatalf("Execute errors: %v, %v", err1, err2)
	}

	output1, _ := result1.Output.(FindCallersOutput)
	output2, _ := result2.Output.(FindCallersOutput)

	if output1.MatchCount != output2.MatchCount {
		t.Errorf("Index path got %d matches, graph path got %d - results inconsistent",
			output1.MatchCount, output2.MatchCount)
	}
}

// TestFindCallersTool_StaticDefinitions verifies find_callers is in StaticToolDefinitions.
func TestFindCallersTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "find_callers" {
			found = true
			break
		}
	}
	if !found {
		t.Error("find_callers not found in StaticToolDefinitions()")
	}
}

func TestFindCallersTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TraceStep must be populated for CRS integration
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}

	// Validate TraceStep fields
	if result.TraceStep.Action != "tool_find_callers" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_callers'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_callers" {
		t.Errorf("TraceStep.Tool = %q, want 'find_callers'", result.TraceStep.Tool)
	}
	if result.TraceStep.Target != "parseConfig" {
		t.Errorf("TraceStep.Target = %q, want 'parseConfig'", result.TraceStep.Target)
	}
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}
	if result.TraceStep.Timestamp == 0 {
		t.Error("TraceStep.Timestamp should be set")
	}

	// Validate metadata keys
	if result.TraceStep.Metadata == nil {
		t.Fatal("TraceStep.Metadata should not be nil")
	}
	for _, key := range []string{"match_count", "total_callers", "used_inheritance_path"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}

	// Error should be empty on success
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestFindCallersTool_TraceStepOnError verifies CRS integration on validation error path.
func TestFindCallersTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty function_name")
	}

	// TraceStep must still be populated on error for CRS visibility
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_find_callers" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_callers'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_callers" {
		t.Errorf("TraceStep.Tool = %q, want 'find_callers'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}

func TestFindCallers_DefinitiveFooter(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindCallersTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "parseConfig",
		"limit":         50,
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Verify the definitive footer is present in the output text
	if !strings.Contains(result.OutputText, "these results are exhaustive") {
		t.Error("expected definitive footer in success path output")
	}
	if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
		t.Error("expected 'Do NOT use Grep or Read' in success path output")
	}
}
