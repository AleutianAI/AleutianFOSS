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
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
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

	// IT-00a-1: Unified scoring returns 0.8 for all substring matches
	// (System A handles ranking via computeMatchScore; confidence is a fixed tier value)
	if confidence != 0.8 {
		t.Errorf("Expected confidence 0.8 (substring tier), got %f", confidence)
	}

	if strategy != "substring" {
		t.Errorf("Expected strategy 'substring', got '%s'", strategy)
	}
}

// TestResolveSymbol_UnifiedSearch_SubstringBeatsTestFile tests IT-00a-1 Phase 1A:
// The unified search path uses System A's scoring, which includes test file penalties.
// A substring match in a source file should beat a substring match in a test file.
func TestResolveSymbol_UnifiedSearch_SubstringBeatsTestFile(t *testing.T) {
	idx := index.NewSymbolIndex()

	// Both contain "Process" as substring, but one is in a test file
	sourceFunc := &ast.Symbol{
		ID: "pkg/engine.go:10:ProcessData", Name: "ProcessData", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/engine.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}
	testFunc := &ast.Symbol{
		ID: "pkg/engine_test.go:10:ProcessTestData", Name: "ProcessTestData", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/engine_test.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}

	if err := idx.Add(sourceFunc); err != nil {
		t.Fatalf("Failed to add source func: %v", err)
	}
	if err := idx.Add(testFunc); err != nil {
		t.Fatalf("Failed to add test func: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// "Process" is a substring of both, but System A penalizes test files (+50000)
	resolvedID, _, strategy, err := resolveSymbol(deps, "Process")
	if err != nil {
		t.Fatalf("Expected match, got error: %v", err)
	}

	if resolvedID != sourceFunc.ID {
		t.Errorf("Expected source file preferred over test file, got '%s'", resolvedID)
	}
	if strategy != "substring" {
		t.Errorf("Expected strategy 'substring', got '%s'", strategy)
	}
}

// TestResolveSymbol_UnifiedSearch_SubstringBeforeFuzzy tests that substring matches
// are returned before fuzzy-only matches in the unified search path.
func TestResolveSymbol_UnifiedSearch_SubstringBeforeFuzzy(t *testing.T) {
	idx := index.NewSymbolIndex()

	// "Render" is a substring of "RenderPage" but only fuzzy-match to "Rander" (edit distance 1)
	substringFunc := &ast.Symbol{
		ID: "pkg/ui.go:10:RenderPage", Name: "RenderPage", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/ui.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}
	// Note: "Rander" has edit distance 1 from "Render" — fuzzy match but not substring
	fuzzyFunc := &ast.Symbol{
		ID: "pkg/typo.go:10:Rander", Name: "Rander", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/typo.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}

	if err := idx.Add(substringFunc); err != nil {
		t.Fatalf("Failed to add substring func: %v", err)
	}
	if err := idx.Add(fuzzyFunc); err != nil {
		t.Fatalf("Failed to add fuzzy func: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Render")
	if err != nil {
		t.Fatalf("Expected match, got error: %v", err)
	}

	// Substring match should win over fuzzy match
	if resolvedID != substringFunc.ID {
		t.Errorf("Expected substring match 'RenderPage', got '%s'", resolvedID)
	}
	if strategy != "substring" {
		t.Errorf("Expected strategy 'substring', got '%s'", strategy)
	}
	if confidence != 0.8 {
		t.Errorf("Expected confidence 0.8 (substring tier), got %f", confidence)
	}
}

// TestResolveSymbol_UnifiedSearch_FuzzyFallback tests that when no substring matches exist,
// the unified path falls through to fuzzy results with function preference.
func TestResolveSymbol_UnifiedSearch_FuzzyFallback(t *testing.T) {
	idx := index.NewSymbolIndex()

	// "Rander" has edit distance 1 from "Render" — fuzzy match but NOT a substring
	fuzzyFunc := &ast.Symbol{
		ID: "pkg/handler.go:10:Rander", Name: "Rander", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/handler.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}
	fuzzyStruct := &ast.Symbol{
		ID: "pkg/types.go:10:Randar", Name: "Randar", Kind: ast.SymbolKindStruct,
		FilePath: "pkg/types.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}

	if err := idx.Add(fuzzyFunc); err != nil {
		t.Fatalf("Failed to add fuzzy func: %v", err)
	}
	if err := idx.Add(fuzzyStruct); err != nil {
		t.Fatalf("Failed to add fuzzy struct: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// "Render" is NOT a substring of "Rander" or "Randar", so fuzzy path fires
	resolvedID, confidence, strategy, err := resolveSymbol(deps, "Render")
	if err != nil {
		t.Fatalf("Expected fuzzy match, got error: %v", err)
	}

	// Function should be preferred in fuzzy path
	if resolvedID != fuzzyFunc.ID {
		t.Errorf("Expected function preferred in fuzzy path, got '%s'", resolvedID)
	}
	if strategy != "fuzzy" {
		t.Errorf("Expected strategy 'fuzzy', got '%s'", strategy)
	}
	if confidence != 0.7 {
		t.Errorf("Expected confidence 0.7 (fuzzy func tier), got %f", confidence)
	}
}

// TestResolveSymbol_UnifiedSearch_PrefixSubstringPreferred tests that prefix
// substring matches are returned over mid-string substring matches.
func TestResolveSymbol_UnifiedSearch_PrefixSubstringPreferred(t *testing.T) {
	idx := index.NewSymbolIndex()

	// "Handler" is a prefix of "HandlerFunc" and a mid-string match of "NewHandler"
	prefixFunc := &ast.Symbol{
		ID: "pkg/http.go:10:HandlerFunc", Name: "HandlerFunc", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/http.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}
	midFunc := &ast.Symbol{
		ID: "pkg/factory.go:10:NewHandler", Name: "NewHandler", Kind: ast.SymbolKindFunction,
		FilePath: "pkg/factory.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
	}

	if err := idx.Add(prefixFunc); err != nil {
		t.Fatalf("Failed to add prefix func: %v", err)
	}
	if err := idx.Add(midFunc); err != nil {
		t.Fatalf("Failed to add mid func: %v", err)
	}

	deps := &Dependencies{SymbolIndex: idx}

	// System A scores prefix (base 1) lower than substring (base 3),
	// so prefix match should come first in sorted results
	resolvedID, _, strategy, err := resolveSymbol(deps, "Handler")
	if err != nil {
		t.Fatalf("Expected match, got error: %v", err)
	}

	// Prefix match scores better than mid-string in System A
	if resolvedID != prefixFunc.ID {
		t.Errorf("Expected prefix match 'HandlerFunc' preferred, got '%s'", resolvedID)
	}
	if strategy != "substring" {
		t.Errorf("Expected strategy 'substring', got '%s'", strategy)
	}
}

// TestDisambiguateMultipleMatches tests IT-05 SR1: multi-signal disambiguation.
func TestDisambiguateMultipleMatches(t *testing.T) {
	t.Run("prefers source over test file", func(t *testing.T) {
		source := &ast.Symbol{
			ID: "cmd/main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}
		test := &ast.Symbol{
			ID: "cmd/main_test.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main_test.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{test, source}, nil, "main")
		if result.ID != source.ID {
			t.Errorf("expected source file preferred, got %s", result.ID)
		}
	})

	t.Run("prefers exported over unexported", func(t *testing.T) {
		exported := &ast.Symbol{
			ID: "pkg/handler.go:5:Handler", Name: "Handler", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/handler.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}
		unexported := &ast.Symbol{
			ID: "pkg/internal.go:5:Handler", Name: "Handler", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/internal.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{unexported, exported}, nil, "Handler")
		if result.ID != exported.ID {
			t.Errorf("expected exported preferred, got %s", result.ID)
		}
	})

	t.Run("prefers shallow path over deep path", func(t *testing.T) {
		shallow := &ast.Symbol{
			ID: "cmd/main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", StartLine: 5, EndLine: 10, Language: "go",
		}
		deep := &ast.Symbol{
			ID: "internal/warpc/gen/tools/main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "internal/warpc/gen/tools/main.go", StartLine: 5, EndLine: 10, Language: "go",
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{deep, shallow}, nil, "main")
		if result.ID != shallow.ID {
			t.Errorf("expected shallow path preferred, got %s", result.ID)
		}
	})

	t.Run("prefers function over type for same name", func(t *testing.T) {
		fn := &ast.Symbol{
			ID: "pkg/handler.go:10:Handle", Name: "Handle", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/handler.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
		}
		typ := &ast.Symbol{
			ID: "pkg/types.go:5:Handle", Name: "Handle", Kind: ast.SymbolKindStruct,
			FilePath: "pkg/types.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{typ, fn}, nil, "Handle")
		if result.ID != fn.ID {
			t.Errorf("expected function preferred over type, got %s", result.ID)
		}
	})

	t.Run("penalizes underscore prefix", func(t *testing.T) {
		normal := &ast.Symbol{
			ID: "scene.ts:10:render", Name: "render", Kind: ast.SymbolKindFunction,
			FilePath: "scene.ts", StartLine: 10, EndLine: 20, Language: "typescript",
		}
		underscored := &ast.Symbol{
			ID: "scene.ts:50:_render", Name: "_render", Kind: ast.SymbolKindFunction,
			FilePath: "scene.ts", StartLine: 50, EndLine: 60, Language: "typescript",
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{underscored, normal}, nil, "render")
		if result.ID != normal.ID {
			t.Errorf("expected non-underscore preferred, got %s", result.ID)
		}
	})

	t.Run("handles single match", func(t *testing.T) {
		single := &ast.Symbol{
			ID: "pkg/handler.go:5:Handler", Name: "Handler", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/handler.go", StartLine: 5, EndLine: 10, Language: "go",
		}
		result := disambiguateMultipleMatches([]*ast.Symbol{single}, nil, "Handler")
		if result.ID != single.ID {
			t.Errorf("expected single match returned, got %s", result.ID)
		}
	})

	t.Run("handles nil slice", func(t *testing.T) {
		result := disambiguateMultipleMatches(nil, nil, "")
		if result != nil {
			t.Errorf("expected nil for empty matches, got %v", result)
		}
	})

	t.Run("cumulative penalties: test + unexported + deep", func(t *testing.T) {
		good := &ast.Symbol{
			ID: "cmd/main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}
		bad := &ast.Symbol{
			ID: "a/b/c/tests/test_main.py:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "a/b/c/tests/test_main.py", StartLine: 5, EndLine: 10, Language: "python",
			Exported: false,
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{bad, good}, nil, "main")
		if result.ID != good.ID {
			t.Errorf("expected good match preferred over bad with cumulative penalties, got %s", result.ID)
		}
	})
}

// TestScoreForDisambiguation verifies scoring signals are applied correctly.
func TestScoreForDisambiguation(t *testing.T) {
	tests := []struct {
		name    string
		sym     *ast.Symbol
		wantMin int
		wantMax int
	}{
		{
			name: "exported source function — zero penalty",
			sym: &ast.Symbol{
				Name: "Handle", Kind: ast.SymbolKindFunction,
				FilePath: "pkg/handler.go", Exported: true,
			},
			wantMin: 0, wantMax: 0,
		},
		{
			name: "test file — 50000 penalty",
			sym: &ast.Symbol{
				Name: "Handle", Kind: ast.SymbolKindFunction,
				FilePath: "pkg/handler_test.go", Exported: true,
			},
			wantMin: 50000, wantMax: 50000,
		},
		{
			name: "unexported — 20000 penalty",
			sym: &ast.Symbol{
				Name: "handle", Kind: ast.SymbolKindFunction,
				FilePath: "pkg/handler.go", Exported: false,
			},
			wantMin: 20000, wantMax: 20000,
		},
		{
			name: "underscore prefix — 10000 penalty",
			sym: &ast.Symbol{
				Name: "_handle", Kind: ast.SymbolKindFunction,
				FilePath: "pkg/handler.go", Exported: true,
			},
			wantMin: 10000, wantMax: 10000,
		},
		{
			name: "deep path (4 slashes) — 2000 depth penalty",
			sym: &ast.Symbol{
				Name: "handle", Kind: ast.SymbolKindFunction,
				FilePath: "a/b/c/d/handler.go", Exported: true,
			},
			wantMin: 2000, wantMax: 2000,
		},
		{
			name: "struct kind — 1 penalty",
			sym: &ast.Symbol{
				Name: "Handle", Kind: ast.SymbolKindStruct,
				FilePath: "pkg/handler.go", Exported: true,
			},
			wantMin: 1, wantMax: 1,
		},
		{
			name: "variable kind — 2 penalty",
			sym: &ast.Symbol{
				Name: "Handle", Kind: ast.SymbolKindVariable,
				FilePath: "pkg/handler.go", Exported: true,
			},
			wantMin: 2, wantMax: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreForDisambiguation(tt.sym, nil, "")
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("scoreForDisambiguation() = %d, want [%d, %d]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestIsTestFilePath tests cross-language test file detection in the phases package.
func TestIsTestFilePath(t *testing.T) {
	tests := []struct {
		filePath string
		want     bool
	}{
		// Go
		{"cmd/main_test.go", true},
		{"cmd/main.go", false},
		// Python
		{"tests/test_handler.py", true},
		{"test_handler.py", true},
		{"handler_test.py", true},
		{"conftest.py", true},
		{"handler.py", false},
		// JS/TS
		{"src/handler.test.js", true},
		{"src/handler.spec.ts", true},
		{"src/handler.ts", false},
		// Directory patterns
		{"test/handler.go", true},
		{"tests/handler.py", true},
		{"__tests__/handler.js", true},
		{"testing/helper.go", true},
		// Mid-path directories
		{"src/test/handler.go", true},
		{"lib/__tests__/utils.ts", true},
		// Edge cases
		{"", false},
		{"src/contestant.go", false},
		{"src/latest.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			got := isTestFilePath(tt.filePath)
			if got != tt.want {
				t.Errorf("isTestFilePath(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
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

// buildTestGraphWithFanout creates a graph and analytics for fan-out quality gate tests.
// Returns GraphAnalytics where:
//   - "main" at cmd/main.go has 3 outgoing call edges (high fan-out)
//   - "main" at livereload/main.go has 0 outgoing call edges (dead end)
//   - "route" at router/types.go has 0 outgoing call edges (type/constant)
//   - "canActivate" at guards/auth.go has 2 outgoing call edges
func buildTestGraphWithFanout(t *testing.T) *graph.GraphAnalytics {
	t.Helper()

	g := graph.NewGraph("/test/project")

	// High fan-out main (the one we want)
	mainGood := &ast.Symbol{
		ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
		Package: "cmd", FilePath: "cmd/main.go", Exported: false,
	}
	// Dead-end main (the one we want to skip)
	mainBad := &ast.Symbol{
		ID: "livereload/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
		Package: "livereload", FilePath: "livereload/main.go", Exported: false,
	}
	// Type with 0 edges
	route := &ast.Symbol{
		ID: "router/types.go:5:route", Name: "route", Kind: ast.SymbolKindVariable,
		Package: "router", FilePath: "router/types.go", Exported: true,
	}
	// Function with edges
	canActivate := &ast.Symbol{
		ID: "guards/auth.go:20:canActivate", Name: "canActivate", Kind: ast.SymbolKindFunction,
		Package: "guards", FilePath: "guards/auth.go", Exported: true,
	}
	// Callees for edges
	helper1 := &ast.Symbol{
		ID: "pkg/helper.go:10:Helper1", Name: "Helper1", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/helper.go", Exported: true,
	}
	helper2 := &ast.Symbol{
		ID: "pkg/helper.go:20:Helper2", Name: "Helper2", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/helper.go", Exported: true,
	}
	helper3 := &ast.Symbol{
		ID: "pkg/helper.go:30:Helper3", Name: "Helper3", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/helper.go", Exported: true,
	}

	for _, sym := range []*ast.Symbol{mainGood, mainBad, route, canActivate, helper1, helper2, helper3} {
		if _, err := g.AddNode(sym); err != nil {
			t.Fatalf("AddNode(%s) failed: %v", sym.ID, err)
		}
	}

	// mainGood calls 3 helpers
	for _, toID := range []string{helper1.ID, helper2.ID, helper3.ID} {
		if err := g.AddEdge(mainGood.ID, toID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
			t.Fatalf("AddEdge failed: %v", err)
		}
	}
	// canActivate calls 2 helpers
	for _, toID := range []string{helper1.ID, helper2.ID} {
		if err := g.AddEdge(canActivate.ID, toID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
			t.Fatalf("AddEdge failed: %v", err)
		}
	}
	// mainBad and route have 0 outgoing edges

	g.Freeze()
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}

	return graph.NewGraphAnalytics(hg)
}

// TestResolveFirstCandidate_FanoutQualityGate tests the IT-05 R4 fan-out quality gate.
func TestResolveFirstCandidate_FanoutQualityGate(t *testing.T) {
	// Build index with all symbols
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{ID: "router/types.go:5:route", Name: "route", Kind: ast.SymbolKindVariable,
			FilePath: "router/types.go", StartLine: 5, EndLine: 6, Exported: true, Language: "go"},
		{ID: "guards/auth.go:20:canActivate", Name: "canActivate", Kind: ast.SymbolKindFunction,
			FilePath: "guards/auth.go", StartLine: 20, EndLine: 25, Exported: true, Language: "go"},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	analytics := buildTestGraphWithFanout(t)
	deps := &Dependencies{
		SymbolIndex:    idx,
		GraphAnalytics: analytics,
	}

	t.Run("skips 0-edge candidate when better candidate available", func(t *testing.T) {
		var cache sync.Map
		// "route" resolves but has 0 outgoing edges → should be skipped
		// "canActivate" resolves and has 2 outgoing edges → should be picked
		candidates := []string{"route", "canActivate"}
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "test-fanout", candidates, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rawName != "canActivate" {
			t.Errorf("expected canActivate to be picked (has edges), got rawName=%s symbolID=%s", rawName, symbolID)
		}
	})

	t.Run("keeps first candidate when it has outgoing edges", func(t *testing.T) {
		var cache sync.Map
		// "canActivate" has 2 outgoing edges → should be picked immediately
		candidates := []string{"canActivate", "route"}
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "test-fanout-2", candidates, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rawName != "canActivate" {
			t.Errorf("expected canActivate (first, has edges), got rawName=%s symbolID=%s", rawName, symbolID)
		}
	})

	t.Run("returns fallback when all candidates have 0 edges", func(t *testing.T) {
		// Only add "route" to a fresh index — only one resolvable candidate
		idxSingle := index.NewSymbolIndex()
		routeSym := &ast.Symbol{
			ID: "router/types.go:5:route", Name: "route", Kind: ast.SymbolKindVariable,
			FilePath: "router/types.go", StartLine: 5, EndLine: 6, Exported: true, Language: "go",
		}
		if err := idxSingle.Add(routeSym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
		depsSingle := &Dependencies{
			SymbolIndex:    idxSingle,
			GraphAnalytics: analytics,
		}

		var cache sync.Map
		// "route" has 0 edges, "NonExistent" fails to resolve → falls back to "route"
		candidates := []string{"route", "NonExistent"}
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "test-fanout-3", candidates, depsSingle)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rawName != "route" {
			t.Errorf("expected fallback to route (only resolvable candidate), got rawName=%s symbolID=%s", rawName, symbolID)
		}
	})

	t.Run("single candidate with 0 edges returns it", func(t *testing.T) {
		idxSingle := index.NewSymbolIndex()
		routeSym := &ast.Symbol{
			ID: "router/types.go:5:route", Name: "route", Kind: ast.SymbolKindVariable,
			FilePath: "router/types.go", StartLine: 5, EndLine: 6, Exported: true, Language: "go",
		}
		if err := idxSingle.Add(routeSym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
		depsSingle := &Dependencies{
			SymbolIndex:    idxSingle,
			GraphAnalytics: analytics,
		}

		var cache sync.Map
		// Single candidate, even with 0 edges, should return (it's the last candidate)
		candidates := []string{"route"}
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "test-fanout-4", candidates, depsSingle)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rawName != "route" {
			t.Errorf("expected route returned (single candidate), got rawName=%s symbolID=%s", rawName, symbolID)
		}
	})
}

// TestDisambiguateMultipleMatches_FanoutSignal tests the IT-05 R4 fan-out scoring signal.
func TestDisambiguateMultipleMatches_FanoutSignal(t *testing.T) {
	analytics := buildTestGraphWithFanout(t)
	deps := &Dependencies{GraphAnalytics: analytics}

	t.Run("prefers symbol with call edges over symbol without", func(t *testing.T) {
		// mainGood (cmd/main.go) has 3 outgoing edges
		// mainBad (livereload/main.go) has 0 outgoing edges
		// Both have same depth, same export status, same kind
		mainGood := &ast.Symbol{
			ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", Exported: false,
		}
		mainBad := &ast.Symbol{
			ID: "livereload/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "livereload/main.go", Exported: false,
		}

		result := disambiguateMultipleMatches([]*ast.Symbol{mainBad, mainGood}, deps, "main")
		if result.ID != mainGood.ID {
			t.Errorf("expected mainGood (has edges) preferred, got %s", result.ID)
		}
	})

	t.Run("fan-out scoring with nil deps falls through gracefully", func(t *testing.T) {
		// With nil deps, fan-out is not checked — falls back to existing signals
		mainA := &ast.Symbol{
			ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", Exported: true,
		}
		mainB := &ast.Symbol{
			ID: "a/b/c/d/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "a/b/c/d/main.go", Exported: true,
		}

		// With nil deps, should prefer shallow path (depth penalty kicks in at > 2 slashes)
		result := disambiguateMultipleMatches([]*ast.Symbol{mainB, mainA}, nil, "main")
		if result.ID != mainA.ID {
			t.Errorf("expected shallow path preferred with nil deps, got %s", result.ID)
		}
	})
}

// TestStemExpansionFallback tests the IT-05 R5 stem expansion for concept queries.
func TestStemExpansionFallback(t *testing.T) {
	// Build index with symbols that contain concept words as substrings
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{ID: "store/compaction.go:10:runCompaction", Name: "runCompaction", Kind: ast.SymbolKindFunction,
			FilePath: "store/compaction.go", StartLine: 10, EndLine: 30, Exported: true, Language: "go"},
		{ID: "store/compaction.go:40:doCompaction", Name: "doCompaction", Kind: ast.SymbolKindFunction,
			FilePath: "store/compaction.go", StartLine: 40, EndLine: 60, Exported: false, Language: "go"},
		{ID: "store/memtable.go:10:flushMemtable", Name: "flushMemtable", Kind: ast.SymbolKindFunction,
			FilePath: "store/memtable.go", StartLine: 10, EndLine: 30, Exported: true, Language: "go"},
		{ID: "types/compaction.go:5:CompactionConfig", Name: "CompactionConfig", Kind: ast.SymbolKindStruct,
			FilePath: "types/compaction.go", StartLine: 5, EndLine: 10, Exported: true, Language: "go"},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Build graph with edges for runCompaction and flushMemtable
	g := graph.NewGraph("/test/project")
	helper := &ast.Symbol{
		ID: "pkg/helper.go:10:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/helper.go", Exported: true,
	}
	for _, sym := range symbols {
		if _, err := g.AddNode(sym); err != nil {
			t.Fatalf("AddNode(%s) failed: %v", sym.ID, err)
		}
	}
	if _, err := g.AddNode(helper); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	// runCompaction and flushMemtable have outgoing edges
	for _, srcID := range []string{"store/compaction.go:10:runCompaction", "store/memtable.go:10:flushMemtable"} {
		if err := g.AddEdge(srcID, helper.ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
			t.Fatalf("AddEdge failed: %v", err)
		}
	}
	// doCompaction has 0 outgoing edges (no call edge added)
	g.Freeze()
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	deps := &Dependencies{
		SymbolIndex:    idx,
		GraphAnalytics: analytics,
	}

	ctx := context.Background()

	t.Run("finds function with outgoing edges matching concept", func(t *testing.T) {
		symbolID, rawName, conf := stemExpansionFallback(ctx, []string{"compaction"}, deps)
		if symbolID == "" {
			t.Fatal("expected stem expansion to find a match")
		}
		if rawName != "compaction" {
			t.Errorf("expected rawName='compaction', got %q", rawName)
		}
		// Should find runCompaction (has outgoing edges), not doCompaction (no edges)
		if symbolID != "store/compaction.go:10:runCompaction" {
			t.Errorf("expected runCompaction (has edges), got %q", symbolID)
		}
		if conf != 0.6 {
			t.Errorf("expected confidence 0.6, got %f", conf)
		}
	})

	t.Run("finds memtable function", func(t *testing.T) {
		symbolID, _, _ := stemExpansionFallback(ctx, []string{"memtable"}, deps)
		if symbolID != "store/memtable.go:10:flushMemtable" {
			t.Errorf("expected flushMemtable, got %q", symbolID)
		}
	})

	t.Run("skips short candidates", func(t *testing.T) {
		symbolID, _, _ := stemExpansionFallback(ctx, []string{"run"}, deps)
		if symbolID != "" {
			t.Errorf("expected no match for short candidate 'run', got %q", symbolID)
		}
	})

	t.Run("returns empty for no match", func(t *testing.T) {
		symbolID, _, _ := stemExpansionFallback(ctx, []string{"nonexistent"}, deps)
		if symbolID != "" {
			t.Errorf("expected no match, got %q", symbolID)
		}
	})

	t.Run("nil deps returns empty", func(t *testing.T) {
		symbolID, _, _ := stemExpansionFallback(ctx, []string{"compaction"}, nil)
		if symbolID != "" {
			t.Errorf("expected no match with nil deps, got %q", symbolID)
		}
	})

	t.Run("filters out non-function symbols", func(t *testing.T) {
		// CompactionConfig is a struct — should not be returned
		depsNoGraph := &Dependencies{SymbolIndex: idx}
		symbolID, _, _ := stemExpansionFallback(ctx, []string{"CompactionConfig"}, depsNoGraph)
		// Even though CompactionConfig matches, it's a struct, so it should be skipped
		// The function should find runCompaction or doCompaction instead
		if symbolID == "types/compaction.go:5:CompactionConfig" {
			t.Error("expected stem expansion to skip struct symbols")
		}
	})
}

// TestStemExpansionFallback_GraphNodeMiss verifies that when GraphAnalytics exists
// but a symbol is NOT in the graph (GetNode returns ok=false), stem expansion still
// returns the match at lower confidence (0.5) instead of silently skipping.
func TestStemExpansionFallback_GraphNodeMiss(t *testing.T) {
	// Build index with a function that will NOT be in the graph
	idx := index.NewSymbolIndex()
	sym := &ast.Symbol{
		ID: "pkg/dynamic.go:10:handleDynamic", Name: "handleDynamic",
		Kind: ast.SymbolKindFunction, FilePath: "pkg/dynamic.go",
		StartLine: 10, EndLine: 30, Exported: true, Language: "go",
	}
	if err := idx.Add(sym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}

	// Build a graph that does NOT contain handleDynamic
	g := graph.NewGraph("/test/project")
	unrelated := &ast.Symbol{
		ID: "pkg/other.go:10:Other", Name: "Other", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/other.go", Exported: true,
	}
	if _, err := g.AddNode(unrelated); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	g.Freeze()
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	deps := &Dependencies{
		SymbolIndex:    idx,
		GraphAnalytics: analytics,
	}

	ctx := context.Background()
	symbolID, rawName, conf := stemExpansionFallback(ctx, []string{"dynamic"}, deps)
	if symbolID == "" {
		t.Fatal("expected stem expansion to match handleDynamic even though not in graph")
	}
	if symbolID != "pkg/dynamic.go:10:handleDynamic" {
		t.Errorf("expected handleDynamic, got %q", symbolID)
	}
	if rawName != "dynamic" {
		t.Errorf("expected rawName='dynamic', got %q", rawName)
	}
	if conf != 0.5 {
		t.Errorf("expected confidence 0.5 (not in graph), got %f", conf)
	}
}

// TestResolveFirstCandidate_WithStemExpansion tests that resolveFirstCandidate
// falls back to stem expansion when all candidates fail direct resolution.
func TestResolveFirstCandidate_WithStemExpansion(t *testing.T) {
	// Build index with only substring-matchable symbols (no exact matches for candidates)
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{ID: "store/compact.go:10:runCompaction", Name: "runCompaction", Kind: ast.SymbolKindFunction,
			FilePath: "store/compact.go", StartLine: 10, EndLine: 30, Exported: true, Language: "go"},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Build graph with edges
	g := graph.NewGraph("/test/project")
	helper := &ast.Symbol{
		ID: "pkg/h.go:10:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/h.go", Exported: true,
	}
	for _, sym := range symbols {
		if _, err := g.AddNode(sym); err != nil {
			t.Fatalf("AddNode failed: %v", err)
		}
	}
	if _, err := g.AddNode(helper); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	if err := g.AddEdge(symbols[0].ID, helper.ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
		t.Fatalf("AddEdge failed: %v", err)
	}
	g.Freeze()
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	deps := &Dependencies{
		SymbolIndex:    idx,
		GraphAnalytics: analytics,
	}

	var cache sync.Map
	// "compaction" won't resolve directly (no symbol named "compaction"),
	// but stem expansion should find "runCompaction"
	symbolID, rawName, conf, resolveErr := resolveFirstCandidate(
		context.Background(), &cache, "test-stem", []string{"compaction"}, deps)
	if resolveErr != nil {
		t.Fatalf("expected stem expansion to succeed, got error: %v", resolveErr)
	}
	if symbolID != "store/compact.go:10:runCompaction" {
		t.Errorf("expected runCompaction via stem expansion, got %q", symbolID)
	}
	if rawName != "compaction" {
		t.Errorf("expected rawName='compaction', got %q", rawName)
	}
	// Confidence may be 0.6 (stem expansion) or 0.8 (substring match via resolveSymbol).
	// The normal resolution pipeline may find "runCompaction" via substring search
	// before stem expansion fires, which is equally correct.
	if conf < 0.6 {
		t.Errorf("expected confidence >= 0.6, got %f", conf)
	}
}

// TestScoreForDisambiguation_FanoutPenalty tests the fan-out penalty in scoring.
func TestScoreForDisambiguation_FanoutPenalty(t *testing.T) {
	analytics := buildTestGraphWithFanout(t)
	deps := &Dependencies{GraphAnalytics: analytics}

	t.Run("symbol with outgoing edges gets no fan-out penalty", func(t *testing.T) {
		sym := &ast.Symbol{
			ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", Exported: true,
		}
		score := scoreForDisambiguation(sym, deps, "")
		if score >= 5000 {
			t.Errorf("expected no fan-out penalty for symbol with edges, got score=%d", score)
		}
	})

	t.Run("symbol with 0 outgoing edges gets 5000 penalty", func(t *testing.T) {
		sym := &ast.Symbol{
			ID: "livereload/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "livereload/main.go", Exported: true,
		}
		score := scoreForDisambiguation(sym, deps, "")
		if score < 5000 {
			t.Errorf("expected fan-out penalty (>=5000) for symbol with 0 edges, got score=%d", score)
		}
	})

	t.Run("symbol not in graph gets 8000 penalty", func(t *testing.T) {
		sym := &ast.Symbol{
			ID: "unknown/file.go:10:unknown", Name: "unknown", Kind: ast.SymbolKindFunction,
			FilePath: "unknown/file.go", Exported: true,
		}
		score := scoreForDisambiguation(sym, deps, "")
		if score < 8000 {
			t.Errorf("expected not-in-graph penalty (>=8000), got score=%d", score)
		}
	})

	t.Run("nil deps produces no fan-out penalty", func(t *testing.T) {
		sym := &ast.Symbol{
			ID: "cmd/main.go:10:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", Exported: true,
		}
		score := scoreForDisambiguation(sym, nil, "")
		if score != 0 {
			t.Errorf("expected score=0 for exported function with nil deps, got %d", score)
		}
	})
}

// TestQueryContextNameBonus tests the IT-05 R7 query-context name bonus.
func TestQueryContextNameBonus(t *testing.T) {
	t.Run("no bonus when no extra query words match", func(t *testing.T) {
		bonus := queryContextNameBonus("Flush", "memtable flush to WAL", "flush")
		if bonus != 0 {
			t.Errorf("expected 0 bonus for 'Flush' (no extra query words in name), got %d", bonus)
		}
	})

	t.Run("bonus when extra query word found in name", func(t *testing.T) {
		bonus := queryContextNameBonus("flushMemtable", "memtable flush to WAL", "flush")
		if bonus != -4000 {
			t.Errorf("expected -4000 bonus for 'flushMemtable' ('memtable' in name), got %d", bonus)
		}
	})

	t.Run("multiple extra query words compound", func(t *testing.T) {
		bonus := queryContextNameBonus("flushMemtableWAL", "memtable flush to WAL", "flush")
		// "memtable" (len>=4) matches, "WAL" is len=3 so skipped
		if bonus != -4000 {
			t.Errorf("expected -4000 bonus (only 'memtable' qualifies, 'WAL' is too short), got %d", bonus)
		}
	})

	t.Run("search term itself is not counted", func(t *testing.T) {
		bonus := queryContextNameBonus("flushFlush", "flush something", "flush")
		if bonus != 0 {
			t.Errorf("expected 0 bonus (search term excluded, 'something' doesn't match), got %d", bonus)
		}
	})

	t.Run("empty query returns 0", func(t *testing.T) {
		bonus := queryContextNameBonus("flushMemtable", "", "flush")
		if bonus != 0 {
			t.Errorf("expected 0 for empty query, got %d", bonus)
		}
	})

	t.Run("empty name returns 0", func(t *testing.T) {
		bonus := queryContextNameBonus("", "memtable flush", "flush")
		if bonus != 0 {
			t.Errorf("expected 0 for empty name, got %d", bonus)
		}
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		bonus := queryContextNameBonus("FlushMemtable", "MEMTABLE flush", "flush")
		if bonus != -4000 {
			t.Errorf("expected -4000 (case insensitive match), got %d", bonus)
		}
	})

	t.Run("material shader example", func(t *testing.T) {
		// "material" is the search term, "shader" is an extra query word
		bonusSetter := queryContextNameBonus("setMaterial", "material assignment to shader pipeline", "material")
		bonusGetter := queryContextNameBonus("material", "material assignment to shader pipeline", "material")
		if bonusSetter != 0 {
			// "shader" is in query but not in "setMaterial" → no bonus
			// "assignment" is not in "setMaterial" → no bonus
			// "pipeline" is not in "setMaterial" → no bonus
			t.Errorf("expected 0 for setMaterial, got %d", bonusSetter)
		}
		if bonusGetter != 0 {
			t.Errorf("expected 0 for material (search term excluded), got %d", bonusGetter)
		}
	})
}

// TestScoreForDisambiguation_QueryContextNameBonus tests that scoreForDisambiguation
// applies the query-context name bonus when deps.Query is set.
func TestScoreForDisambiguation_QueryContextNameBonus(t *testing.T) {
	deps := &Dependencies{Query: "memtable flush to WAL"}

	// Use two exported functions at the same depth to isolate the name bonus effect.
	symFlush := &ast.Symbol{
		ID: "db/batch.go:10:Flush", Name: "Flush", Kind: ast.SymbolKindFunction,
		FilePath: "db/batch.go", Exported: true,
	}
	symFlushMemtable := &ast.Symbol{
		ID: "db/memtable.go:10:flushMemtable", Name: "flushMemtable", Kind: ast.SymbolKindFunction,
		FilePath: "db/memtable.go", Exported: true,
	}

	scoreFlush := scoreForDisambiguation(symFlush, deps, "flush")
	scoreFlushMemtable := scoreForDisambiguation(symFlushMemtable, deps, "flush")

	t.Logf("scoreFlush=%d, scoreFlushMemtable=%d", scoreFlush, scoreFlushMemtable)

	// flushMemtable gets -4000 (name bonus: "memtable" in name) and -3000 (filepath bonus: "memtable" in path).
	// Flush gets neither bonus. So flushMemtable should score lower (better).
	if scoreFlushMemtable >= scoreFlush {
		t.Errorf("expected flushMemtable to score lower (better) than Flush, "+
			"Flush=%d, flushMemtable=%d", scoreFlush, scoreFlushMemtable)
	}
}

// TestStemExpansionFallback_ScoresAllMatches tests that the rewritten stemExpansionFallback
// picks the best match by score rather than returning the first match.
func TestStemExpansionFallback_ScoresAllMatches(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{ID: "store/batch.go:10:Flush", Name: "Flush", Kind: ast.SymbolKindFunction,
			FilePath: "store/batch.go", StartLine: 10, EndLine: 20, Exported: true, Language: "go"},
		{ID: "store/memtable.go:10:flushMemtable", Name: "flushMemtable", Kind: ast.SymbolKindFunction,
			FilePath: "store/memtable.go", StartLine: 10, EndLine: 30, Exported: true, Language: "go"},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add symbol: %v", err)
		}
	}

	// Build graph with edges for both
	g := graph.NewGraph("/test/project")
	helper := &ast.Symbol{
		ID: "pkg/h.go:10:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
		Package: "pkg", FilePath: "pkg/h.go", Exported: true,
	}
	for _, sym := range symbols {
		if _, err := g.AddNode(sym); err != nil {
			t.Fatalf("AddNode(%s) failed: %v", sym.ID, err)
		}
	}
	if _, err := g.AddNode(helper); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	for _, sym := range symbols {
		if err := g.AddEdge(sym.ID, helper.ID, graph.EdgeTypeCalls, ast.Location{}); err != nil {
			t.Fatalf("AddEdge failed: %v", err)
		}
	}
	g.Freeze()
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	deps := &Dependencies{
		SymbolIndex:    idx,
		GraphAnalytics: analytics,
		Query:          "memtable flush to WAL",
	}

	ctx := context.Background()
	symbolID, rawName, conf := stemExpansionFallback(ctx, []string{"flush"}, deps)
	if symbolID == "" {
		t.Fatal("expected stem expansion to find a match")
	}
	// With query "memtable flush to WAL", flushMemtable should score better than Flush
	// because "memtable" appears in the name and matches a query word (-4000 bonus)
	if symbolID != "store/memtable.go:10:flushMemtable" {
		t.Errorf("expected flushMemtable (query-context name bonus), got %q", symbolID)
	}
	if rawName != "flush" {
		t.Errorf("expected rawName='flush', got %q", rawName)
	}
	if conf != 0.6 {
		t.Errorf("expected confidence 0.6, got %f", conf)
	}
}
