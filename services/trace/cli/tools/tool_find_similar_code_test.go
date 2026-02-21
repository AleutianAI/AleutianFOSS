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
	"testing"
)

func TestFindSimilarCodeTool_LazyBuild(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t) // frozen graph with 6 symbols

	tool := NewFindSimilarCodeTool(g, idx)

	// The constructor does NOT call Build(). Verify the tool still works
	// because Execute() triggers lazy Build().
	t.Run("first execute triggers lazy build", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_id": "config/parser.go:10:parseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		// The tool should succeed (Build runs, then FindSimilarCode runs).
		// It may find 0 similar results (fingerprints differ), but it should
		// NOT fail with "graph not ready".
		if !result.Success {
			if result.Error == "graph not ready" {
				t.Fatalf("Execute() returned 'graph not ready' — lazy Build() did not fire")
			}
			// Other errors (e.g., symbol not found in fingerprints) are acceptable
			// for this test — the point is Build() was called.
			t.Logf("Execute() returned non-fatal error: %s", result.Error)
		}
	})

	t.Run("second execute skips build", func(t *testing.T) {
		// Second call should still work (Build() is idempotent via IsBuilt check).
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"symbol_id": "config/parser.go:10:parseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success && result.Error == "graph not ready" {
			t.Fatalf("Second Execute() returned 'graph not ready'")
		}
	})

	t.Run("missing symbol_id returns error", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Fatal("Expected failure for missing symbol_id")
		}
		if result.Error != "symbol_id is required" {
			t.Errorf("got error %q, want 'symbol_id is required'", result.Error)
		}
	})
}

func TestFindSimilarCodeTool_UnfrozenGraph(t *testing.T) {
	// Build() requires a frozen graph. If the graph isn't frozen,
	// lazy Build() should fail gracefully.
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	// Unfreeze by creating a new unfrozen graph with the same symbols
	// (createTestGraphWithCallers returns a frozen graph, so we need a fresh one)
	unfrozenG, unfrozenIdx := createUnfrozenTestGraph(t)

	tool := NewFindSimilarCodeTool(unfrozenG, unfrozenIdx)
	_ = g
	_ = idx

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"symbol_id": "test/func.go:1:testFunc",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Fatal("Expected failure for unfrozen graph")
	}
	if result.Error == "" {
		t.Fatal("Expected error message for unfrozen graph")
	}
}
