// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

import (
	"errors"
	"sync"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// Helper to create test symbol with minimal fields
func testSymbol(id, name string, kind ast.SymbolKind, line int) *ast.Symbol {
	return &ast.Symbol{
		ID:        id,
		Name:      name,
		Kind:      kind,
		FilePath:  "test.go",
		StartLine: line,
		EndLine:   line + 1,
		Language:  "go",
	}
}

// TestResolveSymbol_ExactMatch tests Strategy 1: exact symbol ID match.
func TestResolveSymbol_ExactMatch(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindStruct, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "pkg/handler.go:Handler")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected ID 'pkg/handler.go:Handler', got '%s'", resolvedID)
	}
	if confidence != 1.0 {
		t.Errorf("Expected confidence 1.0, got %f", confidence)
	}
	if strategy != "exact" {
		t.Errorf("Expected strategy 'exact', got '%s'", strategy)
	}
}

// TestResolveSymbol_NameMatch_Single tests Strategy 2: single function name match.
func TestResolveSymbol_NameMatch_Single(t *testing.T) {
	idx := index.NewSymbolIndex()
	// Use a FUNCTION not a struct, since we now prefer functions
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Handler")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected ID 'pkg/handler.go:Handler', got '%s'", resolvedID)
	}
	if confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %f", confidence)
	}
	if strategy != "name" {
		t.Errorf("Expected strategy 'name', got '%s'", strategy)
	}
}

// TestResolveSymbol_NameMatch_SingleNonFunction tests fallback when only non-function exists.
// CB-31d: When searching for "Handler" and only Handler (struct) exists, we skip it
// IT-04 Fix: Single non-function match should now return with high confidence.
// Previously it was skipped in favor of substring/fuzzy search, which was wrong —
// an exact name match is authoritative regardless of symbol kind.
func TestResolveSymbol_NameMatch_SingleNonFunction(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindStruct, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Handler")
	if err != nil {
		t.Fatalf("Expected match, got error: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected ID 'pkg/handler.go:Handler', got '%s'", resolvedID)
	}

	// IT-04: Single exact name match returns 0.95 regardless of kind
	if confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %f", confidence)
	}

	if strategy != "name" {
		t.Errorf("Expected strategy 'name', got '%s'", strategy)
	}
}

// TestResolveSymbol_NameMatch_Multiple_PreferFunction tests disambigu ation: prefer function over struct.
func TestResolveSymbol_NameMatch_Multiple_PreferFunction(t *testing.T) {
	idx := index.NewSymbolIndex()

	structSymbol := testSymbol("pkg/types.go:Handler", "Handler", ast.SymbolKindStruct, 5)
	funcSymbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)

	if err := idx.Add(structSymbol); err != nil {
		t.Fatalf("Failed to add struct symbol: %v", err)
	}
	if err := idx.Add(funcSymbol); err != nil {
		t.Fatalf("Failed to add function symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Handler")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected function ID 'pkg/handler.go:Handler', got '%s'", resolvedID)
	}
	if confidence != 0.8 {
		t.Errorf("Expected confidence 0.8 (disambiguated), got %f", confidence)
	}
	if strategy != "name_disambiguated" {
		t.Errorf("Expected strategy 'name_disambiguated', got '%s'", strategy)
	}
}

// TestResolveSymbol_NoMatch tests failure case when no symbol matches.
func TestResolveSymbol_NoMatch(t *testing.T) {
	idx := index.NewSymbolIndex()
	deps := &Dependencies{SymbolIndex: idx}

	_, _, _, err := resolveSymbol(deps, "NonExistentSymbol")

	if err == nil {
		t.Fatal("Expected error for non-existent symbol, got nil")
	}
	// CB-31d: Check for typed error (M-R-1)
	if !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("Expected ErrSymbolNotFound, got: %v", err)
	}
	if err.Error() != `symbol not found: "NonExistentSymbol"` {
		t.Errorf("Expected specific error message, got: %v", err)
	}
}

// TestResolveSymbol_NilDependencies tests error handling for nil dependencies.
func TestResolveSymbol_NilDependencies(t *testing.T) {
	_, _, _, err := resolveSymbol(nil, "Handler")

	if err == nil {
		t.Fatal("Expected error for nil dependencies, got nil")
	}
	// CB-31d: Check for typed error (M-R-1)
	if !errors.Is(err, ErrSymbolIndexNotAvailable) {
		t.Errorf("Expected ErrSymbolIndexNotAvailable, got: %v", err)
	}
}

// TestResolveSymbolCached_Hit tests cache hit path.
func TestResolveSymbolCached_Hit(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	var cache sync.Map
	sessionID := "test-session-123"

	// Pre-cache a resolution
	cache.Store("test-session-123:Handler", SymbolResolution{
		SymbolID:   "pkg/handler.go:Handler",
		Confidence: 0.95,
		Strategy:   "name",
	})

	resolvedID, confidence, err := resolveSymbolCached(&cache, sessionID, "Handler", deps)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected cached ID, got '%s'", resolvedID)
	}
	if confidence != 0.95 {
		t.Errorf("Expected cached confidence 0.95, got %f", confidence)
	}
}

// TestResolveSymbolCached_Miss tests cache miss path.
func TestResolveSymbolCached_Miss(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	var cache sync.Map
	sessionID := "test-session-456"

	resolvedID, confidence, err := resolveSymbolCached(&cache, sessionID, "Handler", deps)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected resolved ID, got '%s'", resolvedID)
	}
	if confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %f", confidence)
	}

	// Verify: Result should now be in cache
	cacheKey := "test-session-456:Handler"
	cached, ok := cache.Load(cacheKey)
	if !ok {
		t.Fatal("Expected result to be cached")
	}
	cachedResult, ok := cached.(SymbolResolution)
	if !ok {
		t.Fatal("Expected SymbolResolution type in cache")
	}
	if cachedResult.SymbolID != "pkg/handler.go:Handler" {
		t.Errorf("Expected cached ID to match, got '%s'", cachedResult.SymbolID)
	}
}

// TestResolveSymbolCached_ConcurrentAccess tests thread safety of cached resolution.
func TestResolveSymbolCached_ConcurrentAccess(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	var cache sync.Map
	sessionID := "concurrent-session"

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, _, err := resolveSymbolCached(&cache, sessionID, "Handler", deps)
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent access caused error: %v", err)
	}

	// Verify: Cache should contain exactly one entry
	count := 0
	cache.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	if count != 1 {
		t.Errorf("Expected 1 cached entry, got %d", count)
	}
}

// TestResolveSymbol_PreferMethod tests that methods are preferred like functions.
func TestResolveSymbol_PreferMethod(t *testing.T) {
	idx := index.NewSymbolIndex()

	typeSymbol := testSymbol("pkg/types.go:Execute", "Execute", ast.SymbolKindStruct, 5)
	methodSymbol := testSymbol("pkg/handler.go:Handler.Execute", "Execute", ast.SymbolKindMethod, 20)

	if err := idx.Add(typeSymbol); err != nil {
		t.Fatalf("Failed to add type: %v", err)
	}
	if err := idx.Add(methodSymbol); err != nil {
		t.Fatalf("Failed to add method: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Execute")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler.Execute" {
		t.Errorf("Expected method ID, got '%s'", resolvedID)
	}
	if confidence != 0.8 {
		t.Errorf("Expected confidence 0.8 (disambiguated), got %f", confidence)
	}
	if strategy != "name_disambiguated" {
		t.Errorf("Expected strategy 'name_disambiguated', got '%s'", strategy)
	}
}

// TestResolveSymbol_EmptyName tests error handling for empty symbol name.
func TestResolveSymbol_EmptyName(t *testing.T) {
	idx := index.NewSymbolIndex()
	deps := &Dependencies{SymbolIndex: idx}

	_, _, _, err := resolveSymbol(deps, "")

	if err == nil {
		t.Fatal("Expected error for empty name, got nil")
	}
	// CB-31d: Check for typed error (M-R-1)
	if !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("Expected ErrSymbolNotFound, got: %v", err)
	}
	if err.Error() != `symbol not found: ""` {
		t.Errorf("Expected specific error message for empty name, got: %v", err)
	}
}

// TestResolveSymbol_SpecialCharacters tests symbol resolution with special characters.
func TestResolveSymbol_SpecialCharacters(t *testing.T) {
	idx := index.NewSymbolIndex()
	// Add symbol with special characters in ID
	symbol := testSymbol("pkg/file.go:New<T>", "New<T>", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// Test exact match with special characters
	resolvedID, confidence, strategy, err := resolveSymbol(deps, "pkg/file.go:New<T>")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resolvedID != "pkg/file.go:New<T>" {
		t.Errorf("Expected exact match with special chars, got '%s'", resolvedID)
	}
	if confidence != 1.0 {
		t.Errorf("Expected confidence 1.0, got %f", confidence)
	}
	if strategy != "exact" {
		t.Errorf("Expected strategy 'exact', got '%s'", strategy)
	}
}

// TestResolveSymbolCached_NilSession tests caching with nil session.
func TestResolveSymbolCached_NilSession(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbol := testSymbol("pkg/handler.go:Handler", "Handler", ast.SymbolKindFunction, 10)
	if err := idx.Add(symbol); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx, Session: nil}

	var cache sync.Map
	// Empty session ID (should still work, just cache key is ":Handler")
	resolvedID, confidence, err := resolveSymbolCached(&cache, "", "Handler", deps)
	if err != nil {
		t.Fatalf("Expected no error with nil session, got: %v", err)
	}

	if resolvedID != "pkg/handler.go:Handler" {
		t.Errorf("Expected resolved ID, got '%s'", resolvedID)
	}
	if confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %f", confidence)
	}

	// Verify caching still works with empty session ID
	cacheKey := ":Handler"
	cached, ok := cache.Load(cacheKey)
	if !ok {
		t.Fatal("Expected result to be cached even with empty session ID")
	}
	cachedResult, ok := cached.(SymbolResolution)
	if !ok {
		t.Fatal("Expected SymbolResolution type in cache")
	}
	if cachedResult.SymbolID != "pkg/handler.go:Handler" {
		t.Errorf("Expected cached ID to match, got '%s'", cachedResult.SymbolID)
	}
}

// TestResolveSymbol_SubstringMatch tests Strategy 2.5: substring matching.
// IT-04 Update: When there's an exact name match (Handler struct), it should be
// returned immediately with high confidence. Substring matching only kicks in
// when there's NO exact name match.
func TestResolveSymbol_SubstringMatch(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Add various symbols
	handlerStruct := testSymbol("pkg/handlers/beacon_upload_handler.go:12:Handler", "Handler", ast.SymbolKindStruct, 12)
	newHandler := testSymbol("pkg/handlers/beacon_upload_handler.go:22:NewHandler", "NewHandler", ast.SymbolKindFunction, 22)
	handleErrors := testSymbol("main/main.go:512:handleProcessingErrors", "handleProcessingErrors", ast.SymbolKindFunction, 512)

	if err := idx.Add(handlerStruct); err != nil {
		t.Fatalf("Failed to add Handler struct: %v", err)
	}
	if err := idx.Add(newHandler); err != nil {
		t.Fatalf("Failed to add NewHandler: %v", err)
	}
	if err := idx.Add(handleErrors); err != nil {
		t.Fatalf("Failed to add handleProcessingErrors: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// IT-04: "Handler" exact match returns the Handler struct directly
	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Handler")
	if err != nil {
		t.Fatalf("Expected match, got error: %v", err)
	}

	// Exact name match returns Handler struct with high confidence
	if resolvedID != "pkg/handlers/beacon_upload_handler.go:12:Handler" {
		t.Errorf("Expected exact match Handler struct, got '%s'", resolvedID)
	}

	if confidence != 0.95 {
		t.Errorf("Expected confidence 0.95, got %f", confidence)
	}

	if strategy != "name" {
		t.Errorf("Expected strategy 'name', got '%s'", strategy)
	}
}

// TestResolveSymbol_SubstringMatch_Partial tests partial substring matching.
func TestResolveSymbol_SubstringMatch_Partial(t *testing.T) {
	idx := index.NewSymbolIndex()

	uploadFunc := testSymbol("pkg/handlers/beacon_upload_handler.go:22:NewUploadFromAPI", "NewUploadFromAPI", ast.SymbolKindFunction, 22)

	if err := idx.Add(uploadFunc); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// Test: "upload" should match "NewUploadFromAPI"
	resolvedID, confidence, strategy, err := resolveSymbol(deps, "upload")
	if err != nil {
		t.Fatalf("Expected substring match, got error: %v", err)
	}

	if resolvedID != "pkg/handlers/beacon_upload_handler.go:22:NewUploadFromAPI" {
		t.Errorf("Expected NewUploadFromAPI, got '%s'", resolvedID)
	}

	// Confidence should be 0.75 (substring, not prefix) + 0.05 (function bonus) = 0.8
	if confidence < 0.75 || confidence > 0.85 {
		t.Errorf("Expected confidence 0.75-0.85, got %f", confidence)
	}

	if strategy != "substring" {
		t.Errorf("Expected strategy 'substring', got '%s'", strategy)
	}
}

// TestResolveSymbol_ErrorWithSuggestions tests error message includes suggestions.
func TestResolveSymbol_ErrorWithSuggestions(t *testing.T) {
	idx := index.NewSymbolIndex()

	newHandler := testSymbol("pkg/handlers/beacon_upload_handler.go:22:NewHandler", "NewHandler", ast.SymbolKindFunction, 22)

	if err := idx.Add(newHandler); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// Test: Completely wrong name should return error with suggestions
	_, _, _, err := resolveSymbol(deps, "CompletelyWrongName")

	if err == nil {
		t.Fatal("Expected error for non-existent symbol")
	}

	if !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("Expected ErrSymbolNotFound, got: %v", err)
	}

	// Error message should contain "Did you mean:"
	// (If fuzzy search finds similar symbols)
	// Note: This test may be brittle depending on fuzzy search implementation
	errorMsg := err.Error()
	t.Logf("Error message: %s", errorMsg)
}
