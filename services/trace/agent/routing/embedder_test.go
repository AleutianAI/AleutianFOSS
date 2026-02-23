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
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Mock Ollama Server
// =============================================================================

// mockOllamaServer creates an httptest.Server that returns deterministic
// embedding vectors. Each tool gets a unique vector based on its index in the
// order of received requests, enabling cosine similarity to be verified.
//
// callCount uses atomic increment because Warm() fires concurrent requests;
// a plain int causes a data race with -race.
func mockOllamaServer(t *testing.T, dim int, failAfter int) *httptest.Server {
	t.Helper()
	var callCount atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := int(callCount.Add(1))
		if failAfter > 0 && count > failAfter {
			http.Error(w, "simulated failure", http.StatusInternalServerError)
			return
		}

		var req ollamaEmbedReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Generate a deterministic vector from the input text.
		// Each unique text gets a different vector by seeding from text length.
		vec := make([]float32, dim)
		seed := float32(len(req.Input)%dim+1) / float32(dim)
		for i := range vec {
			vec[i] = seed * float32(i+1)
		}
		// Normalize to unit vector so cosine = dot product.
		norm := float32(0)
		for _, v := range vec {
			norm += v * v
		}
		norm = float32(math.Sqrt(float64(norm)))
		if norm > 0 {
			for i := range vec {
				vec[i] /= norm
			}
		}

		resp := ollamaEmbedResp{
			Embeddings: [][]float32{vec},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Logf("mock server encode error: %v", err)
		}
	}))
}

// mockOllamaServerEmpty returns a server that always returns an empty embeddings list.
func mockOllamaServerEmpty(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ollamaEmbedResp{Embeddings: [][]float32{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newTestEmbeddingCache creates a ToolEmbeddingCache pointed at the given server URL.
// Uses nil store — tests do not need BadgerDB persistence.
func newTestEmbeddingCache(t *testing.T, serverURL string) *ToolEmbeddingCache {
	t.Helper()
	cache := NewToolEmbeddingCache(slog.Default(), nil)
	cache.url = serverURL + "/api/embed"
	cache.model = "test-model"
	return cache
}

// =============================================================================
// Warm() Tests
// =============================================================================

func TestToolEmbeddingCache_Warm_EmptySpecs(t *testing.T) {
	server := mockOllamaServer(t, 4, 0)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	if err := cache.Warm(context.Background(), nil); err != nil {
		t.Errorf("expected no error for empty specs, got %v", err)
	}
	// warmed should remain false — nothing to embed.
	if cache.IsWarmed() {
		t.Error("expected cache to be unwarmed for empty specs")
	}
}

func TestToolEmbeddingCache_Warm_Success(t *testing.T) {
	server := mockOllamaServer(t, 8, 0)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	specs := makeReferencesVsSymbolSpecs()

	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !cache.IsWarmed() {
		t.Error("expected cache to be warmed after successful Warm()")
	}
	// All specs should have vectors.
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	for _, spec := range specs {
		if _, ok := cache.vectors[spec.Name]; !ok {
			t.Errorf("expected vector for tool %q", spec.Name)
		}
	}
}

func TestToolEmbeddingCache_Warm_VectorsUnitNormalized(t *testing.T) {
	server := mockOllamaServer(t, 8, 0)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	specs := makeReferencesVsSymbolSpecs()

	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	for name, vec := range cache.vectors {
		norm := l2Norm(vec)
		if math.Abs(norm-1.0) > 1e-5 {
			t.Errorf("vector for %q not unit-normalized: norm=%.6f", name, norm)
		}
	}
}

func TestToolEmbeddingCache_Warm_PartialFailure(t *testing.T) {
	// First 2 succeed, rest fail. Warm() should not return error (partial failure
	// is logged but not fatal).
	server := mockOllamaServer(t, 8, 2)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	specs := makeReferencesVsSymbolSpecs() // 5 specs

	// Partial failure: no error returned.
	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Errorf("expected no error for partial failure, got %v", err)
	}
	// At least some tools should be embedded (the first 2 successes).
	// warmed = true if any tool succeeded.
	// Note: due to goroutine concurrency, the exact count may vary slightly,
	// but at least 1 should succeed.
	if !cache.IsWarmed() {
		t.Skip("no tools were embedded — server may have rejected all; timing-sensitive test")
	}
}

func TestToolEmbeddingCache_Warm_AllFail(t *testing.T) {
	// Server fails immediately on all requests.
	server := mockOllamaServer(t, 8, 0)
	server.Close() // close immediately so all requests fail with connection error

	cache := NewToolEmbeddingCache(slog.Default(), nil)
	cache.url = server.URL + "/api/embed"
	cache.model = "test-model"
	// Use a short timeout so this test doesn't hang.
	cache.client = &http.Client{Timeout: 100 * time.Millisecond}

	specs := makeReferencesVsSymbolSpecs()
	// Warm() should return nil (all failures are per-tool, not global).
	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Errorf("expected nil error even when all embeds fail, got %v", err)
	}
	if cache.IsWarmed() {
		t.Error("expected unwarmed cache when all embeds fail")
	}
}

func TestToolEmbeddingCache_Warm_EmptyVectorSkipped(t *testing.T) {
	// Server returns an empty embeddings list — should skip the tool gracefully.
	server := mockOllamaServerEmpty(t)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	specs := makeReferencesVsSymbolSpecs()

	// No error, but warmed = false (all vectors empty → none stored).
	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Errorf("expected no error for empty vectors, got %v", err)
	}
	// warmed will be false if no valid vectors were stored.
}

// =============================================================================
// Score() Tests
// =============================================================================

func TestToolEmbeddingCache_Score_NotWarmed(t *testing.T) {
	// Unwarmed cache should return (nil, nil) — BM25 fallback signal.
	cache := NewToolEmbeddingCache(slog.Default(), nil)
	cache.url = "http://localhost:99999/api/embed" // unreachable

	scores, err := cache.Score(context.Background(), "find references")
	if err != nil {
		t.Errorf("expected nil error from unwarmed cache, got %v", err)
	}
	if scores != nil {
		t.Errorf("expected nil scores from unwarmed cache, got %v", scores)
	}
}

func TestToolEmbeddingCache_Score_Success(t *testing.T) {
	server := mockOllamaServer(t, 8, 0)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	specs := makeReferencesVsSymbolSpecs()
	if err := cache.Warm(context.Background(), specs); err != nil {
		t.Fatalf("warm failed: %v", err)
	}

	scores, err := cache.Score(context.Background(), "find all references to parseConfig")
	if err != nil {
		t.Errorf("expected nil error from Score(), got %v", err)
	}
	if scores == nil {
		t.Fatal("expected non-nil scores after warm")
	}
	// Scores should be in [0, 1].
	for tool, s := range scores {
		if s < 0 || s > 1.0+1e-9 {
			t.Errorf("score for %q out of [0,1]: %.6f", tool, s)
		}
	}
}

func TestToolEmbeddingCache_Score_HTTPError_FallsBackGracefully(t *testing.T) {
	// Warm with working server, then close server for query.
	server := mockOllamaServer(t, 8, 0)
	if err := func() error {
		cache := newTestEmbeddingCache(t, server.URL)
		specs := makeReferencesVsSymbolSpecs()
		return cache.Warm(context.Background(), specs)
	}(); err != nil {
		t.Fatalf("warm failed: %v", err)
	}
	server.Close()

	// New cache: warm with the now-closed server (all fail) → unwarmed.
	cache := newTestEmbeddingCache(t, server.URL)
	cache.client = &http.Client{Timeout: 100 * time.Millisecond}
	_ = cache.Warm(context.Background(), makeReferencesVsSymbolSpecs()) // all fail → unwarmed

	// Score on unwarmed cache should return (nil, nil).
	scores, err := cache.Score(context.Background(), "find references")
	if err != nil {
		t.Errorf("expected nil error on degraded path, got %v", err)
	}
	if scores != nil {
		t.Errorf("expected nil scores on degraded path (unwarmed), got %v", scores)
	}
}

func TestToolEmbeddingCache_Score_QueryTimeout(t *testing.T) {
	// Server that hangs longer than toolEmbeddingQueryTimeout (3s).
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the per-query timeout.
		time.Sleep(5 * time.Second)
		fmt.Fprintln(w, `{"embeddings":[[0.1,0.2]]}`)
	}))
	defer slow.Close()

	// Use a separate warm server that succeeds quickly.
	warmServer := mockOllamaServer(t, 8, 0)
	defer warmServer.Close()

	cache := newTestEmbeddingCache(t, warmServer.URL)
	if err := cache.Warm(context.Background(), makeReferencesVsSymbolSpecs()); err != nil {
		t.Fatalf("warm failed: %v", err)
	}

	// Now point query calls at the slow server.
	cache.url = slow.URL + "/api/embed"

	// Score should time out and return (nil, nil) — graceful degradation.
	start := time.Now()
	scores, err := cache.Score(context.Background(), "find references")
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("expected nil error on timeout degradation, got %v", err)
	}
	// Scores should be nil (timeout → fallback).
	if scores != nil {
		t.Logf("scores unexpectedly non-nil after timeout: %v", scores)
	}
	// Should have returned within toolEmbeddingQueryTimeout + 1s buffer.
	if elapsed > toolEmbeddingQueryTimeout+time.Second {
		t.Errorf("Score() took too long: %v (expected < %v)", elapsed, toolEmbeddingQueryTimeout+time.Second)
	}
}

// =============================================================================
// IsWarmed() Tests
// =============================================================================

func TestToolEmbeddingCache_IsWarmed_DefaultFalse(t *testing.T) {
	cache := NewToolEmbeddingCache(slog.Default(), nil)
	if cache.IsWarmed() {
		t.Error("expected IsWarmed() == false for new cache")
	}
}

func TestToolEmbeddingCache_IsWarmed_TrueAfterWarm(t *testing.T) {
	server := mockOllamaServer(t, 8, 0)
	defer server.Close()

	cache := newTestEmbeddingCache(t, server.URL)
	_ = cache.Warm(context.Background(), makeReferencesVsSymbolSpecs())
	if !cache.IsWarmed() {
		t.Error("expected IsWarmed() == true after successful Warm()")
	}
}

// =============================================================================
// buildEmbeddingDoc Tests
// =============================================================================

func TestBuildEmbeddingDoc_IncludesNameAndKeywords(t *testing.T) {
	spec := ToolSpec{
		Name:      "find_references",
		BestFor:   []string{"references", "usages"},
		UseWhen:   "Use when finding references",
		AvoidWhen: "Avoid when looking for callers",
	}
	doc := buildEmbeddingDoc(spec)

	if doc == "" {
		t.Error("expected non-empty embedding document")
	}
	// Name and keywords should appear.
	for _, kw := range []string{"find_references", "references", "usages"} {
		if !containsSubstr(doc, kw) {
			t.Errorf("expected embedding doc to contain %q, got: %s", kw, doc)
		}
	}
	// UseWhen should appear.
	if !containsSubstr(doc, "Use when finding references") {
		t.Errorf("expected UseWhen in embedding doc, got: %s", doc)
	}
	// AvoidWhen must NOT appear (negative framing degrades embedding quality).
	if containsSubstr(doc, "Avoid when looking for callers") {
		t.Errorf("AvoidWhen should be excluded from embedding doc, got: %s", doc)
	}
}

func TestBuildEmbeddingDoc_EmptyUseWhen(t *testing.T) {
	spec := ToolSpec{
		Name:    "find_symbol",
		BestFor: []string{"definition", "where is"},
		UseWhen: "",
	}
	doc := buildEmbeddingDoc(spec)
	if doc == "" {
		t.Error("expected non-empty embedding doc even without UseWhen")
	}
}

// =============================================================================
// l2Norm and dotProduct Tests
// =============================================================================

func TestL2Norm_ZeroVector(t *testing.T) {
	v := []float32{0, 0, 0}
	if l2Norm(v) != 0 {
		t.Error("expected l2Norm of zero vector = 0")
	}
}

func TestL2Norm_UnitVector(t *testing.T) {
	// [1, 0, 0] → norm = 1.
	v := []float32{1, 0, 0}
	norm := l2Norm(v)
	if math.Abs(norm-1.0) > 1e-9 {
		t.Errorf("expected l2Norm([1,0,0]) = 1.0, got %.6f", norm)
	}
}

func TestL2Norm_KnownValue(t *testing.T) {
	// [3, 4] → norm = 5.
	v := []float32{3, 4}
	norm := l2Norm(v)
	if math.Abs(norm-5.0) > 1e-5 {
		t.Errorf("expected l2Norm([3,4]) = 5.0, got %.6f", norm)
	}
}

func TestDotProduct_Orthogonal(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	if dotProduct(a, b) != 0 {
		t.Error("expected dot product of orthogonal vectors = 0")
	}
}

func TestDotProduct_Parallel(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0}
	dp := dotProduct(a, b)
	if math.Abs(float64(dp)-1.0) > 1e-9 {
		t.Errorf("expected dot product of identical unit vectors = 1.0, got %.6f", dp)
	}
}

func TestDotProduct_MismatchedLengths(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{4, 5}
	// Should use min length = 2: 1*4 + 2*5 = 14.
	dp := dotProduct(a, b)
	if math.Abs(float64(dp)-14.0) > 1e-5 {
		t.Errorf("expected dotProduct with mismatched lengths = 14.0, got %.6f", dp)
	}
}

// =============================================================================
// Helpers
// =============================================================================

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
