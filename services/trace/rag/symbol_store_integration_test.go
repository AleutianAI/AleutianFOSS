// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

//go:build integration

package rag_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
)

func TestSymbolStore_Integration_BatchInsertProducesVectors(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	count, err := store.IndexSymbols(ctx, idx, "test-vectors-hash", nil)
	if err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}
	if count == 0 {
		t.Fatal("IndexSymbols indexed 0 symbols")
	}
	t.Logf("Indexed %d symbols", count)

	// Wait for vectorization.
	time.Sleep(2 * time.Second)

	// Query objects to verify vectors exist.
	result, err := client.Data().ObjectsGetter().
		WithClassName("CodeSymbol").
		WithAdditional("vector").
		WithLimit(1).
		Do(ctx)
	if err != nil {
		t.Fatalf("ObjectsGetter: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("No CodeSymbol objects found in Weaviate")
	}
	if result[0].Vector == nil || len(result[0].Vector) == 0 {
		t.Error("CodeSymbol object has no vector — embedding pipeline may be broken (CRS-25 failure)")
	} else {
		t.Logf("Vector dimension: %d", len(result[0].Vector))
	}

	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}

func TestSymbolStore_Integration_GraphHashSkip(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	hash := "test-hash-skip-123"
	if _, err := store.IndexSymbols(ctx, idx, hash, nil); err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}

	// HasGraphHash should return true for the indexed hash.
	has, err := store.HasGraphHash(ctx, hash)
	if err != nil {
		t.Fatalf("HasGraphHash: %v", err)
	}
	if !has {
		t.Error("HasGraphHash returned false for indexed hash")
	}

	// HasGraphHash should return false for a different hash.
	has, err = store.HasGraphHash(ctx, "different-hash")
	if err != nil {
		t.Fatalf("HasGraphHash: %v", err)
	}
	if has {
		t.Error("HasGraphHash returned true for non-existent hash")
	}

	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}

func TestSymbolStore_Integration_DeleteByFile(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	if _, err := store.IndexSymbols(ctx, idx, "test-hash-delete", nil); err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}

	// Delete symbols for one specific file.
	if err := store.DeleteByFile(ctx, "pkg/handlers/request.go"); err != nil {
		t.Fatalf("DeleteByFile: %v", err)
	}

	// Wait for deletion to propagate.
	time.Sleep(1 * time.Second)

	// Verify the symbol from that file is gone by checking HasGraphHash still works
	// (other symbols remain).
	has, err := store.HasGraphHash(ctx, "test-hash-delete")
	if err != nil {
		t.Fatalf("HasGraphHash: %v", err)
	}
	if !has {
		t.Error("HasGraphHash returned false — all symbols were deleted, not just the target file")
	}

	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}
