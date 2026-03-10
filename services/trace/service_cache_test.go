// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGR70a_CacheHit_ReturnsCachedGraph verifies that a second Init call with
// the same project root returns the cached graph immediately without rebuilding.
func TestGR70a_CacheHit_ReturnsCachedGraph(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoFiles(t, tmpDir)

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	// First init: full build
	resp1, err := svc.Init(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("first Init failed: %v", err)
	}
	if resp1.GraphID == "" {
		t.Fatal("expected non-empty graph ID")
	}
	if resp1.SymbolsExtracted == 0 {
		t.Fatal("expected symbols from first init")
	}

	// Second init: should hit cache (no forceRebuild)
	start := time.Now()
	resp2, err := svc.Init(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("second Init failed: %v", err)
	}
	elapsed := time.Since(start)

	t.Run("same_graph_id", func(t *testing.T) {
		if resp2.GraphID != resp1.GraphID {
			t.Errorf("expected same graph ID %s, got %s", resp1.GraphID, resp2.GraphID)
		}
	})

	t.Run("same_node_count", func(t *testing.T) {
		if resp2.SymbolsExtracted != resp1.SymbolsExtracted {
			t.Errorf("expected %d symbols, got %d", resp1.SymbolsExtracted, resp2.SymbolsExtracted)
		}
	})

	t.Run("same_edge_count", func(t *testing.T) {
		if resp2.EdgesBuilt != resp1.EdgesBuilt {
			t.Errorf("expected %d edges, got %d", resp1.EdgesBuilt, resp2.EdgesBuilt)
		}
	})

	t.Run("fast_return", func(t *testing.T) {
		if elapsed > 100*time.Millisecond {
			t.Errorf("cache hit took %v, expected < 100ms", elapsed)
		}
	})

	t.Run("no_files_parsed", func(t *testing.T) {
		if resp2.FilesParsed != 0 {
			t.Errorf("cache hit should report 0 files parsed, got %d", resp2.FilesParsed)
		}
	})

	t.Run("not_marked_as_refresh", func(t *testing.T) {
		if resp2.IsRefresh {
			t.Error("cache hit should set IsRefresh=false")
		}
	})
}

// TestGR70a_ForceRebuild_RebuildsGraph verifies that passing forceRebuild=true
// triggers a full rebuild even when a cached graph exists.
func TestGR70a_ForceRebuild_RebuildsGraph(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoFiles(t, tmpDir)

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	// First init: full build
	resp1, err := svc.Init(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("first Init failed: %v", err)
	}

	// Second init with forceRebuild=true: should rebuild
	resp2, err := svc.Init(ctx, tmpDir, []string{"go"}, nil, true)
	if err != nil {
		t.Fatalf("forced rebuild Init failed: %v", err)
	}

	t.Run("same_graph_id", func(t *testing.T) {
		if resp2.GraphID != resp1.GraphID {
			t.Errorf("expected same graph ID %s, got %s", resp1.GraphID, resp2.GraphID)
		}
	})

	t.Run("files_parsed", func(t *testing.T) {
		if resp2.FilesParsed == 0 {
			t.Error("forced rebuild should parse files, got 0")
		}
	})
}

// TestGR70a_AgentInitAfterStartup_NoBuild simulates the startup → agent session
// flow: HandleInit builds the graph, then ServiceAdapter.InitGraph (which calls
// Init without forceRebuild) should return the cached graph instantly.
func TestGR70a_AgentInitAfterStartup_NoBuild(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestGoFiles(t, tmpDir)

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	// Simulate HandleInit: forceRebuild=true
	resp1, err := svc.Init(ctx, tmpDir, []string{"go"}, nil, true)
	if err != nil {
		t.Fatalf("startup Init failed: %v", err)
	}
	if resp1.FilesParsed == 0 {
		t.Fatal("startup init should parse files")
	}

	// Simulate ServiceAdapter.InitGraph: no forceRebuild (agent session)
	start := time.Now()
	resp2, err := svc.Init(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("agent Init failed: %v", err)
	}
	elapsed := time.Since(start)

	t.Run("returns_cached_graph", func(t *testing.T) {
		if resp2.GraphID != resp1.GraphID {
			t.Errorf("expected cached graph ID %s, got %s", resp1.GraphID, resp2.GraphID)
		}
	})

	t.Run("no_rebuild", func(t *testing.T) {
		if resp2.FilesParsed != 0 {
			t.Errorf("agent init should not parse files, got %d", resp2.FilesParsed)
		}
	})

	t.Run("sub_millisecond", func(t *testing.T) {
		if elapsed > 50*time.Millisecond {
			t.Errorf("agent init took %v, expected < 50ms", elapsed)
		}
	})

	t.Run("symbols_match", func(t *testing.T) {
		if resp2.SymbolsExtracted != resp1.SymbolsExtracted {
			t.Errorf("expected %d symbols, got %d", resp1.SymbolsExtracted, resp2.SymbolsExtracted)
		}
	})
}

// writeTestGoFiles creates a minimal Go project for testing.
func writeTestGoFiles(t *testing.T, dir string) {
	t.Helper()

	files := map[string]string{
		"main.go": `package main

func main() {
	helper()
}
`,
		"util.go": `package main

func helper() string { return "ok" }
`,
		"sub/handler.go": `package sub

type Handler struct{}

func (h *Handler) Handle() {}
`,
	}

	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
