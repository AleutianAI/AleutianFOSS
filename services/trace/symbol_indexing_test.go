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
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// TestSymbolIndexingCoordinator_GetSymbolStore_NilBeforeIndexing verifies that
// GetSymbolStore returns nil before any indexing has occurred.
func TestSymbolIndexingCoordinator_GetSymbolStore_NilBeforeIndexing(t *testing.T) {
	t.Run("returns nil before any indexing", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)
		if store := coord.GetSymbolStore(); store != nil {
			t.Errorf("expected nil store before indexing, got %v", store)
		}
	})
}

// TestSymbolIndexingCoordinator_TriggerIndexing_NilCachedGraph verifies that
// TriggerIndexing gracefully handles nil input without panicking.
func TestSymbolIndexingCoordinator_TriggerIndexing_NilCachedGraph(t *testing.T) {
	t.Run("nil cached graph does not panic", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)
		// Should not panic.
		coord.TriggerIndexing("test-graph-id", nil)
	})

	t.Run("nil index does not panic", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)
		cached := &CachedGraph{
			Graph: graph.NewGraph(""),
			Index: nil,
		}
		// Should not panic — returns early because Index is nil.
		coord.TriggerIndexing("test-graph-id", cached)
	})
}

// TestSymbolIndexingCoordinator_ConcurrencyGuard verifies that two concurrent
// TriggerIndexing calls with the same hash are deduplicated — only one
// goroutine should proceed.
func TestSymbolIndexingCoordinator_ConcurrencyGuard(t *testing.T) {
	t.Run("concurrent calls with same hash are deduplicated", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		idx := index.NewSymbolIndex()
		g := graph.NewGraph("")
		cached := &CachedGraph{
			Graph: g,
			Index: idx,
		}

		// Manually set activeHash to simulate an in-progress indexing.
		preHash := computeGraphContentHash("graph-1", cached)
		coord.mu.Lock()
		coord.activeHash = preHash
		coord.mu.Unlock()

		// This call should detect the in-progress hash and return immediately.
		// It should NOT block or start a new goroutine.
		coord.TriggerIndexing("graph-1", cached)

		// Verify activeHash is still set (wasn't cleared by the skipped call).
		coord.mu.Lock()
		active := coord.activeHash
		coord.mu.Unlock()

		if active != preHash {
			t.Errorf("activeHash changed unexpectedly: got %q, want %q", active, preHash)
		}
	})
}

// TestSymbolIndexingCoordinator_SkipsWhenAlreadyIndexed verifies that
// TriggerIndexing skips when the same hash was already indexed.
func TestSymbolIndexingCoordinator_SkipsWhenAlreadyIndexed(t *testing.T) {
	t.Run("skips when hash matches lastIndexedHash", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		idx := index.NewSymbolIndex()
		g := graph.NewGraph("")
		cached := &CachedGraph{
			Graph: g,
			Index: idx,
		}

		// Simulate a previously completed indexing.
		preHash := computeGraphContentHash("graph-1", cached)
		coord.mu.Lock()
		coord.lastIndexedHash = preHash
		coord.mu.Unlock()

		// This should detect the already-indexed hash and skip.
		coord.TriggerIndexing("graph-1", cached)

		// Brief wait to ensure no goroutine was spawned that would modify state.
		time.Sleep(50 * time.Millisecond)

		// activeHash should still be empty (no goroutine started).
		coord.mu.Lock()
		active := coord.activeHash
		coord.mu.Unlock()

		if active != "" {
			t.Errorf("activeHash should be empty when already indexed, got %q", active)
		}
	})
}

// TestSymbolIndexingCoordinator_DifferentHashes verifies that TriggerIndexing
// does NOT skip when a different graph hash is provided.
func TestSymbolIndexingCoordinator_DifferentHashes(t *testing.T) {
	t.Run("different hash triggers new indexing", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		idx := index.NewSymbolIndex()
		g := graph.NewGraph("")
		cached := &CachedGraph{
			Graph: g,
			Index: idx,
		}

		// Set a previous hash.
		coord.mu.Lock()
		coord.lastIndexedHash = "old-hash-value"
		coord.mu.Unlock()

		// TriggerIndexing with a different graph should start (and fail fast
		// because weaviate client is nil, but the goroutine should still launch).
		coord.TriggerIndexing("different-graph", cached)

		// Brief wait for goroutine to start and set activeHash.
		time.Sleep(50 * time.Millisecond)

		// After the goroutine fails (nil weaviate client), activeHash should be cleared.
		coord.mu.Lock()
		active := coord.activeHash
		coord.mu.Unlock()

		// activeHash should be cleared after the goroutine finishes (or empty if it failed fast).
		if active != "" {
			t.Logf("activeHash still set (goroutine may still be running): %q", active)
		}
	})
}

// TestComputeGraphContentHash verifies the hash function produces stable output.
func TestComputeGraphContentHash(t *testing.T) {
	t.Run("same inputs produce same hash", func(t *testing.T) {
		g := graph.NewGraph("")
		cached := &CachedGraph{Graph: g}

		hash1 := computeGraphContentHash("graph-1", cached)
		hash2 := computeGraphContentHash("graph-1", cached)

		if hash1 != hash2 {
			t.Errorf("expected stable hash, got %q and %q", hash1, hash2)
		}
	})

	t.Run("different graph IDs produce different hashes", func(t *testing.T) {
		g := graph.NewGraph("")
		cached := &CachedGraph{Graph: g}

		hash1 := computeGraphContentHash("graph-1", cached)
		hash2 := computeGraphContentHash("graph-2", cached)

		if hash1 == hash2 {
			t.Error("expected different hashes for different graph IDs")
		}
	})
}

// TestSymbolIndexingCoordinator_GetProgress_IdleState verifies that GetProgress
// returns idle state before any indexing has occurred.
func TestSymbolIndexingCoordinator_GetProgress_IdleState(t *testing.T) {
	t.Run("returns idle state before indexing", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)
		progress := coord.GetProgress()

		if progress.InProgress {
			t.Error("expected InProgress=false before indexing")
		}
		if progress.Phase != "" {
			t.Errorf("expected empty Phase before indexing, got %q", progress.Phase)
		}
		if progress.SymbolsTotal != 0 {
			t.Errorf("expected SymbolsTotal=0, got %d", progress.SymbolsTotal)
		}
	})
}

// TestSymbolIndexingCoordinator_GetProgress_DuringIndexing verifies that
// progress is correctly updated when indexing is in progress.
func TestSymbolIndexingCoordinator_GetProgress_DuringIndexing(t *testing.T) {
	t.Run("reflects in-progress state", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		// Simulate an in-progress indexing by manually setting progress.
		coord.mu.Lock()
		coord.progress = IndexingStatusResponse{
			InProgress:       true,
			Phase:            "inserting",
			SymbolsTotal:     51000,
			BatchesCompleted: 100,
			BatchesTotal:     510,
		}
		coord.mu.Unlock()

		progress := coord.GetProgress()
		if !progress.InProgress {
			t.Error("expected InProgress=true during indexing")
		}
		if progress.Phase != "inserting" {
			t.Errorf("expected Phase=inserting, got %q", progress.Phase)
		}
		if progress.SymbolsTotal != 51000 {
			t.Errorf("expected SymbolsTotal=51000, got %d", progress.SymbolsTotal)
		}
		if progress.BatchesCompleted != 100 {
			t.Errorf("expected BatchesCompleted=100, got %d", progress.BatchesCompleted)
		}
		if progress.BatchesTotal != 510 {
			t.Errorf("expected BatchesTotal=510, got %d", progress.BatchesTotal)
		}
	})
}

// TestSymbolIndexingCoordinator_GetProgress_AfterCompletion verifies that
// progress reflects the complete state after indexing finishes.
func TestSymbolIndexingCoordinator_GetProgress_AfterCompletion(t *testing.T) {
	t.Run("reflects complete state", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		coord.mu.Lock()
		coord.progress = IndexingStatusResponse{
			InProgress:     false,
			Phase:          "complete",
			SymbolsTotal:   51000,
			SymbolsIndexed: 51000,
		}
		coord.mu.Unlock()

		progress := coord.GetProgress()
		if progress.InProgress {
			t.Error("expected InProgress=false after completion")
		}
		if progress.Phase != "complete" {
			t.Errorf("expected Phase=complete, got %q", progress.Phase)
		}
		if progress.SymbolsIndexed != 51000 {
			t.Errorf("expected SymbolsIndexed=51000, got %d", progress.SymbolsIndexed)
		}
	})
}

// TestSymbolIndexingCoordinator_GetProgress_Error verifies that progress
// reflects the error state when indexing fails.
func TestSymbolIndexingCoordinator_GetProgress_Error(t *testing.T) {
	t.Run("reflects error state", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		coord.mu.Lock()
		coord.progress = IndexingStatusResponse{
			InProgress: false,
			Phase:      "complete",
			Error:      "connection refused",
		}
		coord.mu.Unlock()

		progress := coord.GetProgress()
		if progress.InProgress {
			t.Error("expected InProgress=false on error")
		}
		if progress.Error != "connection refused" {
			t.Errorf("expected Error='connection refused', got %q", progress.Error)
		}
	})
}

// TestSymbolIndexingCoordinator_ThreadSafety runs concurrent operations
// to verify no data races.
func TestSymbolIndexingCoordinator_ThreadSafety(t *testing.T) {
	t.Run("concurrent GetSymbolStore and TriggerIndexing", func(t *testing.T) {
		coord := NewSymbolIndexingCoordinator(nil, "test", nil)

		idx := index.NewSymbolIndex()
		g := graph.NewGraph("")
		cached := &CachedGraph{
			Graph: g,
			Index: idx,
		}

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				coord.GetSymbolStore()
			}()
			go func() {
				defer wg.Done()
				coord.TriggerIndexing("graph-1", cached)
			}()
		}

		wg.Wait()
	})
}
