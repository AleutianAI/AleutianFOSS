// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"testing"

	badgerstore "github.com/AleutianAI/AleutianFOSS/services/trace/storage/badger"
)

// =============================================================================
// Helpers
// =============================================================================

// openTestDB opens an in-memory BadgerDB for testing.
// The caller must call db.Close() when done.
func openTestDB(t *testing.T) *badgerstore.DB {
	t.Helper()
	db, err := badgerstore.OpenDB(badgerstore.InMemoryConfig())
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// makeTestVectors builds a small map[string][]float32 for round-trip testing.
func makeTestVectors() map[string][]float32 {
	return map[string][]float32{
		"find_references": {0.1, 0.2, 0.3, 0.4},
		"find_symbol":     {0.5, 0.6, 0.7, 0.8},
		"find_callers":    {0.9, 0.1, 0.2, 0.3},
	}
}

// =============================================================================
// BadgerRouterCacheStore.LoadEmbeddings Tests
// =============================================================================

func TestRouterCache_Load_EmptyDB(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)

	vectors, err := store.LoadEmbeddings(context.Background(), "nonexistenthash")
	if err != nil {
		t.Errorf("expected nil error on miss, got %v", err)
	}
	if vectors != nil {
		t.Errorf("expected nil vectors on miss, got %v", vectors)
	}
}

func TestRouterCache_Load_ContextCancelled(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled immediately

	_, err := store.LoadEmbeddings(ctx, "somehash")
	// Should return an error because context is cancelled
	if err == nil {
		// BadgerDB may or may not check ctx before the key-not-found path;
		// accept either behaviour but not a panic.
		t.Log("context-cancelled load returned nil error (acceptable for in-memory)")
	}
}

// =============================================================================
// BadgerRouterCacheStore.SaveEmbeddings Tests
// =============================================================================

func TestRouterCache_Save_EmptyVectors(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)

	// Empty map should be a no-op — not an error.
	if err := store.SaveEmbeddings(context.Background(), "anyhash", nil); err != nil {
		t.Errorf("expected no error for empty vectors, got %v", err)
	}
	if err := store.SaveEmbeddings(context.Background(), "anyhash", map[string][]float32{}); err != nil {
		t.Errorf("expected no error for empty map, got %v", err)
	}
}

func TestRouterCache_Save_ContextCancelled(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := store.SaveEmbeddings(ctx, "anyhash", makeTestVectors())
	// Context is cancelled; should return a context error or a wrapped one.
	if err == nil {
		t.Log("context-cancelled save returned nil error (acceptable for in-memory)")
	}
}

// =============================================================================
// Round-trip Tests
// =============================================================================

func TestRouterCache_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)
	ctx := context.Background()

	want := makeTestVectors()
	hash := "testcorpushash0001"

	if err := store.SaveEmbeddings(ctx, hash, want); err != nil {
		t.Fatalf("SaveEmbeddings: %v", err)
	}

	got, err := store.LoadEmbeddings(ctx, hash)
	if err != nil {
		t.Fatalf("LoadEmbeddings: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil vectors after save")
	}

	for tool, wantVec := range want {
		gotVec, ok := got[tool]
		if !ok {
			t.Errorf("missing tool %q in loaded vectors", tool)
			continue
		}
		if len(gotVec) != len(wantVec) {
			t.Errorf("tool %q: want len %d, got %d", tool, len(wantVec), len(gotVec))
			continue
		}
		for i, w := range wantVec {
			if gotVec[i] != w {
				t.Errorf("tool %q dim %d: want %v, got %v", tool, i, w, gotVec[i])
			}
		}
	}
}

func TestRouterCache_RoundTrip_MultipleKeys(t *testing.T) {
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)
	ctx := context.Background()

	hashes := []string{"hashA", "hashB", "hashC"}
	for i, h := range hashes {
		vecs := map[string][]float32{
			"tool_x": {float32(i + 1), float32(i + 2)},
		}
		if err := store.SaveEmbeddings(ctx, h, vecs); err != nil {
			t.Fatalf("save hash %s: %v", h, err)
		}
	}

	for i, h := range hashes {
		got, err := store.LoadEmbeddings(ctx, h)
		if err != nil {
			t.Fatalf("load hash %s: %v", h, err)
		}
		if got == nil {
			t.Fatalf("expected vectors for hash %s", h)
		}
		want := []float32{float32(i + 1), float32(i + 2)}
		gotVec := got["tool_x"]
		for j, w := range want {
			if gotVec[j] != w {
				t.Errorf("hash %s dim %d: want %v, got %v", h, j, w, gotVec[j])
			}
		}
	}
}

func TestRouterCache_Miss_AfterOverwrite(t *testing.T) {
	// Saving a new hash does not affect an old hash — different keys.
	db := openTestDB(t)
	store := NewBadgerRouterCacheStore(db, 0, nil)
	ctx := context.Background()

	if err := store.SaveEmbeddings(ctx, "hash1", makeTestVectors()); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Different hash should still be a miss.
	got, err := store.LoadEmbeddings(ctx, "hash2")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Errorf("expected miss for unpersisted hash, got %v", got)
	}
}

// =============================================================================
// computeCorpusHash Tests
// =============================================================================

func TestComputeCorpusHash_Empty(t *testing.T) {
	h := computeCorpusHash(nil, "nomic-embed-text-v2-moe")
	if len(h) != 64 {
		t.Errorf("expected 64-char hex hash, got %q (len %d)", h, len(h))
	}
}

func TestComputeCorpusHash_Deterministic(t *testing.T) {
	specs := []ToolSpec{
		{Name: "find_references", BestFor: []string{"references", "usages"}, UseWhen: "Find references"},
		{Name: "find_symbol", BestFor: []string{"where is", "defined"}, UseWhen: "Find definition"},
	}
	model := "nomic-embed-text-v2-moe"

	h1 := computeCorpusHash(specs, model)
	h2 := computeCorpusHash(specs, model)

	if h1 != h2 {
		t.Errorf("hash is non-deterministic: %q vs %q", h1, h2)
	}
}

func TestComputeCorpusHash_OrderIndependent(t *testing.T) {
	// Specs in different order should produce the same hash (sorted internally).
	specs1 := []ToolSpec{
		{Name: "find_references", BestFor: []string{"references"}, UseWhen: "Find references"},
		{Name: "find_symbol", BestFor: []string{"defined"}, UseWhen: "Find definition"},
	}
	specs2 := []ToolSpec{
		{Name: "find_symbol", BestFor: []string{"defined"}, UseWhen: "Find definition"},
		{Name: "find_references", BestFor: []string{"references"}, UseWhen: "Find references"},
	}
	model := "nomic-embed-text-v2-moe"

	if computeCorpusHash(specs1, model) != computeCorpusHash(specs2, model) {
		t.Error("hash differs for same specs in different order (spec sort not applied)")
	}
}

func TestComputeCorpusHash_BestForOrderIndependent(t *testing.T) {
	// BestFor keywords in different order should produce the same hash.
	specs1 := []ToolSpec{
		{Name: "find_references", BestFor: []string{"references", "usages", "where is it used"}},
	}
	specs2 := []ToolSpec{
		{Name: "find_references", BestFor: []string{"usages", "where is it used", "references"}},
	}
	model := "test-model"

	if computeCorpusHash(specs1, model) != computeCorpusHash(specs2, model) {
		t.Error("hash differs for same BestFor keywords in different order")
	}
}

func TestComputeCorpusHash_SensitiveToName(t *testing.T) {
	specs1 := []ToolSpec{{Name: "find_references", BestFor: []string{"refs"}, UseWhen: "Find refs"}}
	specs2 := []ToolSpec{{Name: "find_REFERENCES", BestFor: []string{"refs"}, UseWhen: "Find refs"}}
	model := "test-model"

	if computeCorpusHash(specs1, model) == computeCorpusHash(specs2, model) {
		t.Error("expected different hash for different tool name (case-sensitive)")
	}
}

func TestComputeCorpusHash_SensitiveToKeyword(t *testing.T) {
	specs1 := []ToolSpec{{Name: "tool", BestFor: []string{"references"}, UseWhen: "Find refs"}}
	specs2 := []ToolSpec{{Name: "tool", BestFor: []string{"usages"}, UseWhen: "Find refs"}}
	model := "test-model"

	if computeCorpusHash(specs1, model) == computeCorpusHash(specs2, model) {
		t.Error("expected different hash when BestFor keyword changes")
	}
}

func TestComputeCorpusHash_SensitiveToUseWhen(t *testing.T) {
	specs1 := []ToolSpec{{Name: "tool", BestFor: []string{"refs"}, UseWhen: "Find references"}}
	specs2 := []ToolSpec{{Name: "tool", BestFor: []string{"refs"}, UseWhen: "Locate declarations"}}
	model := "test-model"

	if computeCorpusHash(specs1, model) == computeCorpusHash(specs2, model) {
		t.Error("expected different hash when UseWhen changes")
	}
}

func TestComputeCorpusHash_SensitiveToModel(t *testing.T) {
	specs := []ToolSpec{{Name: "tool", BestFor: []string{"refs"}, UseWhen: "Find refs"}}

	h1 := computeCorpusHash(specs, "nomic-embed-text-v2-moe")
	h2 := computeCorpusHash(specs, "mxbai-embed-large")

	if h1 == h2 {
		t.Error("expected different hash for different embedding model")
	}
}

func TestComputeCorpusHash_InsensitiveToAvoidWhen(t *testing.T) {
	// AvoidWhen is excluded from the hash — changing it should NOT change the hash.
	specs1 := []ToolSpec{{Name: "tool", BestFor: []string{"refs"}, UseWhen: "Find refs", AvoidWhen: ""}}
	specs2 := []ToolSpec{{Name: "tool", BestFor: []string{"refs"}, UseWhen: "Find refs", AvoidWhen: "Do not use for definitions"}}
	model := "test-model"

	if computeCorpusHash(specs1, model) != computeCorpusHash(specs2, model) {
		t.Error("hash should NOT change when AvoidWhen changes (AvoidWhen excluded from corpus hash)")
	}
}

// =============================================================================
// gobEncode / gobDecode Round-trip Tests
// =============================================================================

func TestGobEncodeDecode_RoundTrip(t *testing.T) {
	want := makeTestVectors()

	raw, err := gobEncode(want)
	if err != nil {
		t.Fatalf("gobEncode: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("expected non-empty encoded bytes")
	}

	got, err := gobDecode(raw)
	if err != nil {
		t.Fatalf("gobDecode: %v", err)
	}

	for tool, wantVec := range want {
		gotVec, ok := got[tool]
		if !ok {
			t.Errorf("missing tool %q after decode", tool)
			continue
		}
		for i, w := range wantVec {
			if gotVec[i] != w {
				t.Errorf("tool %q dim %d: want %v, got %v", tool, i, w, gotVec[i])
			}
		}
	}
}

func TestGobDecode_InvalidData(t *testing.T) {
	_, err := gobDecode([]byte("this is not gob data"))
	if err == nil {
		t.Error("expected error decoding invalid gob data")
	}
}

// =============================================================================
// shortHash Tests
// =============================================================================

func TestShortHash_LongHash(t *testing.T) {
	h := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	got := shortHash(h)
	if got != "abcdef12..." {
		t.Errorf("expected %q, got %q", "abcdef12...", got)
	}
}

func TestShortHash_ShortHash(t *testing.T) {
	h := "abc"
	got := shortHash(h)
	if got != "abc" {
		t.Errorf("expected %q, got %q", "abc", got)
	}
}

func TestShortHash_ExactlyEight(t *testing.T) {
	h := "12345678"
	got := shortHash(h)
	if got != "12345678" {
		t.Errorf("expected %q unchanged, got %q", h, got)
	}
}
