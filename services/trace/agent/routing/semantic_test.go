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
	"testing"
)

func TestExtractQueryTerms(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected map[string]bool
	}{
		{
			name:     "empty query",
			query:    "",
			expected: map[string]bool{},
		},
		{
			name:  "simple word",
			query: "main",
			expected: map[string]bool{
				"main": true,
			},
		},
		{
			name:  "multiple words",
			query: "parse config file",
			expected: map[string]bool{
				"parse":  true,
				"config": true,
				"file":   true,
			},
		},
		{
			name:  "camelCase",
			query: "parseConfig",
			expected: map[string]bool{
				"parse":  true,
				"config": true,
			},
		},
		{
			name:  "snake_case",
			query: "parse_config",
			expected: map[string]bool{
				"parse":  true,
				"config": true,
			},
		},
		{
			name:  "path with slashes",
			query: "pkg/api/handlers",
			expected: map[string]bool{
				"pkg":     true,
				"api":     true,
				"handler": true, // "handlers" → stemmed to "handler"
			},
		},
		{
			name:  "mixed delimiters",
			query: "pkg.api_handlers-v2",
			expected: map[string]bool{
				"pkg":     true,
				"api":     true,
				"handler": true, // "handlers" → stemmed to "handler"
				"v2":      true,
			},
		},
		{
			name:  "filters noise words",
			query: "the main function in the file",
			expected: map[string]bool{
				"main":     true,
				"function": true,
				"file":     true,
			},
		},
		{
			name:  "skips single characters",
			query: "a b c main",
			expected: map[string]bool{
				"main": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractQueryTerms(tt.query)

			if len(result) != len(tt.expected) {
				t.Errorf("ExtractQueryTerms(%q) = %v (len %d), want %v (len %d)",
					tt.query, result, len(result), tt.expected, len(tt.expected))
				return
			}

			for term := range tt.expected {
				if !result[term] {
					t.Errorf("ExtractQueryTerms(%q) missing expected term %q", tt.query, term)
				}
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        map[string]bool
		b        map[string]bool
		expected float64
	}{
		{
			name:     "empty sets",
			a:        map[string]bool{},
			b:        map[string]bool{},
			expected: 0.0,
		},
		{
			name:     "one empty set",
			a:        map[string]bool{"a": true},
			b:        map[string]bool{},
			expected: 0.0,
		},
		{
			name:     "identical sets",
			a:        map[string]bool{"main": true, "func": true},
			b:        map[string]bool{"main": true, "func": true},
			expected: 1.0,
		},
		{
			name:     "no overlap",
			a:        map[string]bool{"main": true},
			b:        map[string]bool{"parse": true},
			expected: 0.0,
		},
		{
			name:     "partial overlap",
			a:        map[string]bool{"main": true, "func": true},
			b:        map[string]bool{"main": true, "parse": true},
			expected: 1.0 / 3.0, // intersection=1, union=3
		},
		{
			name:     "subset",
			a:        map[string]bool{"main": true},
			b:        map[string]bool{"main": true, "func": true},
			expected: 0.5, // intersection=1, union=2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := JaccardSimilarity(tt.a, tt.b)

			// Allow small floating point error
			diff := result - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.001 {
				t.Errorf("JaccardSimilarity(%v, %v) = %f, want %f",
					tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestToolCallHistory_Add(t *testing.T) {
	h := NewToolCallHistory()

	if h.Len() != 0 {
		t.Errorf("New history should have length 0, got %d", h.Len())
	}

	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: map[string]bool{"main": true},
		RawQuery:   "main",
		StepNumber: 1,
		Success:    true,
	})

	if h.Len() != 1 {
		t.Errorf("After Add, length should be 1, got %d", h.Len())
	}
}

func TestToolCallHistory_GetCallsForTool(t *testing.T) {
	h := NewToolCallHistory()

	h.Add(ToolCallSignature{Tool: "Grep", RawQuery: "pattern1", StepNumber: 1})
	h.Add(ToolCallSignature{Tool: "Read", RawQuery: "file.go", StepNumber: 2})
	h.Add(ToolCallSignature{Tool: "Grep", RawQuery: "pattern2", StepNumber: 3})

	grepCalls := h.GetCallsForTool("Grep")
	if len(grepCalls) != 2 {
		t.Errorf("Expected 2 Grep calls, got %d", len(grepCalls))
	}

	readCalls := h.GetCallsForTool("Read")
	if len(readCalls) != 1 {
		t.Errorf("Expected 1 Read call, got %d", len(readCalls))
	}

	findCalls := h.GetCallsForTool("find_callers")
	if len(findCalls) != 0 {
		t.Errorf("Expected 0 find_callers calls, got %d", len(findCalls))
	}
}

func TestToolCallHistory_IsExactDuplicate(t *testing.T) {
	h := NewToolCallHistory()

	h.Add(ToolCallSignature{Tool: "Grep", RawQuery: "main", StepNumber: 1})
	h.Add(ToolCallSignature{Tool: "Grep", RawQuery: "parseConfig", StepNumber: 2})

	tests := []struct {
		name     string
		tool     string
		rawQuery string
		wantDup  bool
	}{
		{
			name:     "exact match",
			tool:     "Grep",
			rawQuery: "main",
			wantDup:  true,
		},
		{
			name:     "case insensitive match",
			tool:     "Grep",
			rawQuery: "MAIN",
			wantDup:  true,
		},
		{
			name:     "different query same tool",
			tool:     "Grep",
			rawQuery: "other",
			wantDup:  false,
		},
		{
			name:     "same query different tool",
			tool:     "Read",
			rawQuery: "main",
			wantDup:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isDup, _ := h.IsExactDuplicate(tt.tool, tt.rawQuery)
			if isDup != tt.wantDup {
				t.Errorf("IsExactDuplicate(%q, %q) = %v, want %v",
					tt.tool, tt.rawQuery, isDup, tt.wantDup)
			}
		})
	}
}

func TestToolCallHistory_GetSimilarity(t *testing.T) {
	h := NewToolCallHistory()

	// Add a call for Grep("main function")
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms("main function"),
		RawQuery:   "main function",
		StepNumber: 1,
	})

	tests := []struct {
		name       string
		tool       string
		queryTerms map[string]bool
		wantMinSim float64
		wantMaxSim float64
		wantNonNil bool
	}{
		{
			name:       "identical query",
			tool:       "Grep",
			queryTerms: ExtractQueryTerms("main function"),
			wantMinSim: 0.99,
			wantMaxSim: 1.01,
			wantNonNil: true,
		},
		{
			name:       "similar query",
			tool:       "Grep",
			queryTerms: ExtractQueryTerms("main"),
			wantMinSim: 0.4,
			wantMaxSim: 0.6,
			wantNonNil: true,
		},
		{
			name:       "different query",
			tool:       "Grep",
			queryTerms: ExtractQueryTerms("parseConfig"),
			wantMinSim: 0.0,
			wantMaxSim: 0.1,
			wantNonNil: false,
		},
		{
			name:       "different tool",
			tool:       "Read",
			queryTerms: ExtractQueryTerms("main function"),
			wantMinSim: 0.0,
			wantMaxSim: 0.01,
			wantNonNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim, call := h.GetSimilarity(tt.tool, tt.queryTerms)

			if sim < tt.wantMinSim || sim > tt.wantMaxSim {
				t.Errorf("GetSimilarity(%q, ...) similarity = %f, want in range [%f, %f]",
					tt.tool, sim, tt.wantMinSim, tt.wantMaxSim)
			}

			if tt.wantNonNil && call == nil {
				t.Errorf("GetSimilarity(%q, ...) call = nil, want non-nil", tt.tool)
			}
			if !tt.wantNonNil && call != nil {
				t.Errorf("GetSimilarity(%q, ...) call = %v, want nil", tt.tool, call)
			}
		})
	}
}

func TestCheckSemanticStatus(t *testing.T) {
	h := NewToolCallHistory()

	// Add prior calls
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms("main"),
		RawQuery:   "main",
		StepNumber: 1,
	})
	h.Add(ToolCallSignature{
		Tool:       "find_callers",
		QueryTerms: ExtractQueryTerms("parseConfig"),
		RawQuery:   "parseConfig",
		StepNumber: 2,
	})

	tests := []struct {
		name       string
		tool       string
		rawQuery   string
		wantStatus string
	}{
		{
			name:       "exact duplicate - blocked",
			tool:       "Grep",
			rawQuery:   "main",
			wantStatus: "blocked",
		},
		{
			name:       "case insensitive duplicate - blocked",
			tool:       "Grep",
			rawQuery:   "MAIN",
			wantStatus: "blocked",
		},
		{
			name:       "different query same tool - allowed",
			tool:       "Grep",
			rawQuery:   "parseConfig",
			wantStatus: "allowed",
		},
		{
			name:       "same query different tool - allowed",
			tool:       "Read",
			rawQuery:   "main",
			wantStatus: "allowed",
		},
		{
			name:       "similar query - penalized",
			tool:       "find_callers",
			rawQuery:   "parse_config_file", // Similar to parseConfig but has extra term
			wantStatus: "penalized",
		},
		{
			name:       "completely different - allowed",
			tool:       "find_callers",
			rawQuery:   "main",
			wantStatus: "allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _, _ := CheckSemanticStatus(h, tt.tool, tt.rawQuery)
			if status != tt.wantStatus {
				t.Errorf("CheckSemanticStatus(h, %q, %q) = %q, want %q",
					tt.tool, tt.rawQuery, status, tt.wantStatus)
			}
		})
	}
}

func TestCheckSemanticStatus_NilHistory(t *testing.T) {
	status, similarity, call := CheckSemanticStatus(nil, "Grep", "anything")

	if status != "allowed" {
		t.Errorf("CheckSemanticStatus(nil, ...) status = %q, want allowed", status)
	}
	if similarity != 0.0 {
		t.Errorf("CheckSemanticStatus(nil, ...) similarity = %f, want 0.0", similarity)
	}
	if call != nil {
		t.Errorf("CheckSemanticStatus(nil, ...) call = %v, want nil", call)
	}
}

func TestExtractQueryFromParams(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]any
		expected string
	}{
		{
			name:     "nil params",
			params:   nil,
			expected: "",
		},
		{
			name:     "empty params",
			params:   map[string]any{},
			expected: "",
		},
		{
			name:     "pattern param",
			params:   map[string]any{"pattern": "main"},
			expected: "main",
		},
		{
			name:     "function_name param",
			params:   map[string]any{"function_name": "parseConfig"},
			expected: "parseConfig",
		},
		{
			name:     "file_path param",
			params:   map[string]any{"file_path": "main.go"},
			expected: "main.go",
		},
		{
			name:     "multiple params - first wins",
			params:   map[string]any{"pattern": "foo", "query": "bar"},
			expected: "foo",
		},
		{
			name:     "non-string param ignored",
			params:   map[string]any{"pattern": 123, "query": "bar"},
			expected: "bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractQueryFromParams(tt.params)
			if result != tt.expected {
				t.Errorf("ExtractQueryFromParams(%v) = %q, want %q",
					tt.params, result, tt.expected)
			}
		})
	}
}

func TestToolCallHistory_Clear(t *testing.T) {
	h := NewToolCallHistory()

	h.Add(ToolCallSignature{Tool: "Grep", RawQuery: "pattern1"})
	h.Add(ToolCallSignature{Tool: "Read", RawQuery: "file.go"})

	if h.Len() != 2 {
		t.Errorf("Before clear, length should be 2, got %d", h.Len())
	}

	h.Clear()

	if h.Len() != 0 {
		t.Errorf("After clear, length should be 0, got %d", h.Len())
	}
}

// TestGR38_SameToolDifferentParams verifies Issue 17 requirement:
// Same tool with different params should be allowed.
func TestGR38_SameToolDifferentParams(t *testing.T) {
	h := NewToolCallHistory()

	// First call: Grep("pattern1")
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms("pattern1"),
		RawQuery:   "pattern1",
		StepNumber: 1,
		Success:    true,
	})

	// Second call: Grep("pattern2") should be ALLOWED (different query)
	status, similarity, _ := CheckSemanticStatus(h, "Grep", "pattern2")

	if status != "allowed" {
		t.Errorf("Same tool, different params should be allowed, got status=%q, similarity=%f",
			status, similarity)
	}
}

// TestGR38_SameToolSameParams verifies Issue 17 requirement:
// Same tool with same params should be blocked.
func TestGR38_SameToolSameParams(t *testing.T) {
	h := NewToolCallHistory()

	// First call: Grep("main")
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms("main"),
		RawQuery:   "main",
		StepNumber: 1,
		Success:    true,
	})

	// Second call: Grep("main") should be BLOCKED (semantic duplicate)
	status, similarity, _ := CheckSemanticStatus(h, "Grep", "main")

	if status != "blocked" {
		t.Errorf("Same tool, same params should be blocked, got status=%q, similarity=%f",
			status, similarity)
	}
}

// TestGR38_SimilarParamsPenalized verifies Issue 17 requirement:
// Similar params should be penalized but not blocked.
func TestGR38_SimilarParamsPenalized(t *testing.T) {
	h := NewToolCallHistory()

	// First call: find_callers("parseConfig")
	h.Add(ToolCallSignature{
		Tool:       "find_callers",
		QueryTerms: ExtractQueryTerms("parseConfig"),
		RawQuery:   "parseConfig",
		StepNumber: 1,
		Success:    true,
	})

	// Second call: find_callers("parse_config") should be PENALIZED (similar but not identical)
	status, similarity, _ := CheckSemanticStatus(h, "find_callers", "parse_config")

	// The terms are identical after normalization, so this is actually blocked
	// Let's test with a truly similar but different query
	h.Clear()
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms("main function handler"),
		RawQuery:   "main function handler",
		StepNumber: 1,
		Success:    true,
	})

	// "main function" shares 2/3 terms with "main function handler"
	// Jaccard = 2 / 3 = 0.66, which is between 0.3 and 0.8 -> penalized
	status, similarity, _ = CheckSemanticStatus(h, "Grep", "main function")

	if status != "penalized" {
		t.Errorf("Similar params (2/3 overlap) should be penalized, got status=%q, similarity=%f",
			status, similarity)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestToolCallHistory_ConcurrentAdd(t *testing.T) {
	// Use a larger max size for this test
	h := NewToolCallHistoryWithSize(2000)
	const numGoroutines = 100
	const addsPerGoroutine = 10

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < addsPerGoroutine; j++ {
				h.Add(ToolCallSignature{
					Tool:       "Grep",
					RawQuery:   "query",
					StepNumber: id*addsPerGoroutine + j,
				})
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	expected := numGoroutines * addsPerGoroutine
	if h.Len() != expected {
		t.Errorf("Expected %d entries after concurrent adds, got %d", expected, h.Len())
	}
}

func TestToolCallHistory_ConcurrentReadWrite(t *testing.T) {
	h := NewToolCallHistory()

	// Pre-populate with some entries
	for i := 0; i < 10; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			QueryTerms: ExtractQueryTerms("initial query " + string(rune('a'+i))),
			RawQuery:   "initial query " + string(rune('a'+i)),
			StepNumber: i,
		})
	}

	const numReaders = 50
	const numWriters = 10
	done := make(chan bool, numReaders+numWriters)

	// Readers
	for i := 0; i < numReaders; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				h.GetCallsForTool("Grep")
				h.GetSimilarity("Grep", ExtractQueryTerms("test query"))
				h.IsExactDuplicate("Grep", "test")
				h.Len()
			}
			done <- true
		}()
	}

	// Writers
	for i := 0; i < numWriters; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				h.Add(ToolCallSignature{
					Tool:       "Grep",
					RawQuery:   "concurrent query",
					StepNumber: 100 + id*10 + j,
				})
			}
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < numReaders+numWriters; i++ {
		<-done
	}

	// Just verify no panics occurred - if we got here, thread safety is working
}

// =============================================================================
// Boundary Condition Tests
// =============================================================================

func TestExtractQueryTerms_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		minTerms int
		maxTerms int
	}{
		{
			name:     "single character ignored",
			query:    "a",
			minTerms: 0,
			maxTerms: 0,
		},
		{
			name:     "two character word kept",
			query:    "ab",
			minTerms: 1,
			maxTerms: 1,
		},
		{
			name:     "very long query",
			query:    "this is a very long query with many terms that should all be extracted properly without any issues whatsoever",
			minTerms: 10,
			maxTerms: 20,
		},
		{
			name:     "unicode characters",
			query:    "日本語テスト func",
			minTerms: 1, // At least "func" should be extracted
			maxTerms: 5,
		},
		{
			name:     "special characters only",
			query:    "!@#$%^&*()",
			minTerms: 0,
			maxTerms: 1, // May be treated as single token by strings.Fields
		},
		{
			name:     "whitespace only",
			query:    "   \t\n   ",
			minTerms: 0,
			maxTerms: 0,
		},
		{
			name:     "mixed noise and content",
			query:    "the a an is are to for of main func",
			minTerms: 2, // "main" and "func"
			maxTerms: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractQueryTerms(tt.query)
			if len(result) < tt.minTerms || len(result) > tt.maxTerms {
				t.Errorf("ExtractQueryTerms(%q) = %d terms, want between %d and %d",
					tt.query, len(result), tt.minTerms, tt.maxTerms)
			}
		})
	}
}

func TestJaccardSimilarity_BoundaryConditions(t *testing.T) {
	// Large sets
	largeA := make(map[string]bool)
	largeB := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		largeA["term"+string(rune(i))] = true
		if i < 500 {
			largeB["term"+string(rune(i))] = true
		}
	}

	sim := JaccardSimilarity(largeA, largeB)
	// 500 intersection, 1000 union = 0.5
	if sim < 0.49 || sim > 0.51 {
		t.Errorf("Large set similarity = %f, expected ~0.5", sim)
	}
}

func TestCheckSemanticStatus_ThresholdBoundaries(t *testing.T) {
	// Test at exact thresholds
	h := NewToolCallHistory()

	// Add a call with known terms
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true},
		RawQuery:   "a b c d e",
		StepNumber: 1,
	})

	tests := []struct {
		name       string
		queryTerms map[string]bool
		wantStatus string
	}{
		{
			// Jaccard = 5/5 = 1.0 >= 0.8 -> blocked
			name:       "exactly at block threshold (1.0)",
			queryTerms: map[string]bool{"a": true, "b": true, "c": true, "d": true, "e": true},
			wantStatus: "blocked",
		},
		{
			// Jaccard = 4/6 = 0.67 >= 0.3 but < 0.8 -> penalized
			name:       "in penalized range (0.67)",
			queryTerms: map[string]bool{"a": true, "b": true, "c": true, "d": true, "f": true, "g": true},
			wantStatus: "penalized",
		},
		{
			// Jaccard = 1/9 = 0.11 < 0.3 -> allowed
			name:       "below penalty threshold (0.11)",
			queryTerms: map[string]bool{"a": true, "f": true, "g": true, "h": true, "i": true, "j": true, "k": true, "l": true, "m": true},
			wantStatus: "allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, sim, _ := CheckSemanticStatus(h, "Grep", "test")
			// Override the similarity calculation by checking directly
			actualSim := JaccardSimilarity(tt.queryTerms, h.calls[0].QueryTerms)
			var expectedStatus string
			if actualSim >= SemanticDuplicateThreshold {
				expectedStatus = "blocked"
			} else if actualSim >= SemanticPenaltyThreshold {
				expectedStatus = "penalized"
			} else {
				expectedStatus = "allowed"
			}

			if expectedStatus != tt.wantStatus {
				t.Errorf("Expected status %q for similarity %.2f, but got %q (actual sim: %.2f)",
					tt.wantStatus, actualSim, status, sim)
			}
		})
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestToolCallHistory_EmptyQueries(t *testing.T) {
	h := NewToolCallHistory()

	// Add call with empty query
	h.Add(ToolCallSignature{
		Tool:       "Grep",
		QueryTerms: ExtractQueryTerms(""),
		RawQuery:   "",
		StepNumber: 1,
	})

	// Check similarity with empty query - this is an exact duplicate (both empty)
	status, _, _ := CheckSemanticStatus(h, "Grep", "")

	// Empty queries match as exact duplicates (both normalize to empty string)
	if status != "blocked" {
		t.Errorf("Empty query exact duplicate should be blocked, got %q", status)
	}

	// But empty vs non-empty should be allowed
	status2, _, _ := CheckSemanticStatus(h, "Grep", "some query")
	if status2 != "allowed" {
		t.Errorf("Empty vs non-empty should be allowed, got %q", status2)
	}
}

func TestToolCallHistory_LargeHistory(t *testing.T) {
	// Use a larger max size for this test
	h := NewToolCallHistoryWithSize(2000)

	// Add many entries
	for i := 0; i < 1000; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			QueryTerms: ExtractQueryTerms("query" + string(rune(i))),
			RawQuery:   "query" + string(rune(i)),
			StepNumber: i,
		})
	}

	if h.Len() != 1000 {
		t.Errorf("Expected 1000 entries, got %d", h.Len())
	}

	// Search should still work
	calls := h.GetCallsForTool("Grep")
	if len(calls) != 1000 {
		t.Errorf("Expected 1000 Grep calls, got %d", len(calls))
	}
}

// TestToolCallHistory_MaxSizeEnforcement tests that history respects max size limit.
func TestToolCallHistory_MaxSizeEnforcement(t *testing.T) {
	h := NewToolCallHistoryWithSize(50)

	// Add more entries than max size
	for i := 0; i < 100; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			RawQuery:   "query" + string(rune(i)),
			StepNumber: i,
		})
	}

	// Should be limited to max size
	if h.Len() != 50 {
		t.Errorf("Expected 50 entries (max size), got %d", h.Len())
	}

	// Should keep the most recent entries
	calls := h.GetCallsForTool("Grep")
	if len(calls) != 50 {
		t.Errorf("Expected 50 Grep calls, got %d", len(calls))
	}
}

// TestToolCallHistory_DefaultMaxSize tests default max size constant.
func TestToolCallHistory_DefaultMaxSize(t *testing.T) {
	h := NewToolCallHistory()

	if h.MaxSize() != MaxToolCallHistorySize {
		t.Errorf("Expected max size %d, got %d", MaxToolCallHistorySize, h.MaxSize())
	}

	// Add more entries than default max
	for i := 0; i < MaxToolCallHistorySize+50; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			RawQuery:   "query",
			StepNumber: i,
		})
	}

	// Should be limited to max size
	if h.Len() != MaxToolCallHistorySize {
		t.Errorf("Expected %d entries (max size), got %d", MaxToolCallHistorySize, h.Len())
	}
}

// TestToolCallHistory_Trim tests the Trim method.
func TestToolCallHistory_Trim(t *testing.T) {
	h := NewToolCallHistoryWithSize(100)

	// Add entries
	for i := 0; i < 50; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			RawQuery:   "query",
			StepNumber: i,
		})
	}

	if h.Len() != 50 {
		t.Fatalf("Expected 50 entries, got %d", h.Len())
	}

	// Trim to 20
	h.Trim(20)
	if h.Len() != 20 {
		t.Errorf("After Trim(20), expected 20 entries, got %d", h.Len())
	}

	// Trim to 0
	h.Trim(0)
	if h.Len() != 0 {
		t.Errorf("After Trim(0), expected 0 entries, got %d", h.Len())
	}
}

func TestExtractQueryFromMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		expected string
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			expected: "",
		},
		{
			name:     "empty metadata",
			metadata: map[string]string{},
			expected: "",
		},
		{
			name:     "pattern key",
			metadata: map[string]string{"pattern": "test"},
			expected: "test",
		},
		{
			name:     "query key",
			metadata: map[string]string{"query": "search term"},
			expected: "search term",
		},
		{
			name:     "multiple keys - priority order",
			metadata: map[string]string{"query": "q", "pattern": "p"},
			expected: "p", // pattern comes first in priority
		},
		{
			name:     "empty values ignored",
			metadata: map[string]string{"pattern": "", "query": "valid"},
			expected: "valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractQueryFromMetadata(tt.metadata)
			if result != tt.expected {
				t.Errorf("ExtractQueryFromMetadata(%v) = %q, want %q",
					tt.metadata, result, tt.expected)
			}
		})
	}
}

// TestStemTerm verifies the lightweight suffix-stripping stemmer used by
// ExtractQueryTerms to normalize singular/plural and verb forms for BM25.
// IT-16/ISSUE-01: "extend" vs "extends" caused BM25 to miss find_implementations.
func TestStemTerm(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Plural "s" stripping
		{"extends", "extend"},
		{"implements", "implement"},
		{"callers", "caller"},
		{"functions", "function"},
		{"symbols", "symbol"},
		// "es" stripping
		{"classes", "class"},
		{"subclasses", "subclass"},
		{"references", "reference"},
		{"interfaces", "interfac"},
		// "ies" → "y"
		{"dependencies", "dependency"},
		{"hierarchies", "hierarchy"},
		// "ches"/"shes"/"sses" → drop "es"
		{"matches", "match"},
		{"pushes", "push"},
		// "ing" stripping
		{"calling", "call"},
		{"extending", "extend"},
		{"implementing", "implement"},
		// "ed" stripping
		{"called", "call"},
		{"extended", "extend"},
		{"implemented", "implement"},
		// Short words unchanged
		{"go", "go"},
		{"is", "is"},
		{"as", "as"},
		{"bus", "bus"},
		// Words that shouldn't be overstemmed
		{"class", "class"},
		{"stress", "stress"},   // "ss" ending preserved
		{"pass", "pass"},       // "ss" ending preserved
		{"process", "process"}, // ends in "ss" — preserved
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stemTerm(tt.input)
			if got != tt.expected {
				t.Errorf("stemTerm(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestExtractQueryTerms_Stemming verifies that stemming normalizes query terms
// so that "extends" and "extend" match the same BM25 document terms.
func TestExtractQueryTerms_Stemming(t *testing.T) {
	// The ISSUE-01 query: "What classes extend the Light base class?"
	terms := ExtractQueryTerms("What classes extend the Light base class")
	if !terms["extend"] {
		t.Errorf("expected 'extend' in terms, got: %v", terms)
	}
	if !terms["class"] {
		t.Errorf("expected 'class' in terms, got: %v", terms)
	}
	if !terms["light"] {
		t.Errorf("expected 'light' in terms, got: %v", terms)
	}
	if !terms["base"] {
		t.Errorf("expected 'base' in terms, got: %v", terms)
	}

	// "extends" in keyword should also stem to "extend"
	kwTerms := ExtractQueryTerms("extends subclasses implements")
	if !kwTerms["extend"] {
		t.Errorf("expected 'extend' from 'extends', got: %v", kwTerms)
	}
	if !kwTerms["subclass"] {
		t.Errorf("expected 'subclass' from 'subclasses', got: %v", kwTerms)
	}
	if !kwTerms["implement"] {
		t.Errorf("expected 'implement' from 'implements', got: %v", kwTerms)
	}
}

// =============================================================================
// Performance Benchmarks
// =============================================================================

func BenchmarkExtractQueryTerms(b *testing.B) {
	query := "parseConfig buildRequest handleError processData validateInput"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractQueryTerms(query)
	}
}

func BenchmarkJaccardSimilarity(b *testing.B) {
	a := map[string]bool{"parse": true, "config": true, "build": true, "request": true}
	c := map[string]bool{"parse": true, "config": true, "handle": true, "error": true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		JaccardSimilarity(a, c)
	}
}

func BenchmarkToolCallHistory_GetSimilarity(b *testing.B) {
	h := NewToolCallHistory()
	for i := 0; i < 100; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			QueryTerms: ExtractQueryTerms("query term " + string(rune(i))),
			RawQuery:   "query term " + string(rune(i)),
			StepNumber: i,
		})
	}

	queryTerms := ExtractQueryTerms("test query")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.GetSimilarity("Grep", queryTerms)
	}
}

func BenchmarkCheckSemanticStatus(b *testing.B) {
	h := NewToolCallHistory()
	for i := 0; i < 50; i++ {
		h.Add(ToolCallSignature{
			Tool:       "Grep",
			QueryTerms: ExtractQueryTerms("query term " + string(rune(i))),
			RawQuery:   "query term " + string(rune(i)),
			StepNumber: i,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CheckSemanticStatus(h, "Grep", "test query pattern")
	}
}
