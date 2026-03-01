// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package index

import (
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestComputeMatchScore(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		symbolName     string
		symbolKind     ast.SymbolKind
		wantMatchType  string
		shouldMatch    bool
		expectedBetter []string // Symbols that should score better than this one
	}{
		{
			name:          "exact match beats everything",
			query:         "Process",
			symbolName:    "Process",
			symbolKind:    ast.SymbolKindFunction,
			wantMatchType: "exact",
			shouldMatch:   true,
		},
		{
			name:          "prefix match at position 0 (starts with query)",
			query:         "Process",
			symbolName:    "ProcessData",
			symbolKind:    ast.SymbolKindFunction,
			wantMatchType: "prefix", // Prefix is better than camelCase!
			shouldMatch:   true,
		},
		{
			name:          "camelCase in middle is good",
			query:         "Process",
			symbolName:    "getDatesToProcess",
			symbolKind:    ast.SymbolKindFunction,
			wantMatchType: "camelCase",
			shouldMatch:   true,
		},
		{
			name:          "substring match works",
			query:         "Process",
			symbolName:    "DetectFailedProcessing",
			symbolKind:    ast.SymbolKindFunction,
			wantMatchType: "substring",
			shouldMatch:   true,
		},
		{
			name:          "field gets penalty but still matches as prefix",
			query:         "Process",
			symbolName:    "ProcessedStatus",
			symbolKind:    ast.SymbolKindField,
			wantMatchType: "prefix", // "Processed" starts with "Process"
			shouldMatch:   true,
		},
		{
			name:          "no match for unrelated symbol",
			query:         "Process",
			symbolName:    "UnrelatedFunction",
			symbolKind:    ast.SymbolKindFunction,
			wantMatchType: "no_match",
			shouldMatch:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, matchType := computeMatchScore(
				tt.query,
				toLower(tt.query),
				tt.symbolName,
				toLower(tt.symbolName),
				tt.symbolKind,
				"src/main.go", // Non-test, normal depth
				true,          // Exported
			)

			if tt.shouldMatch && score == -1 {
				t.Errorf("Expected match but got score -1")
			}

			if !tt.shouldMatch && score != -1 {
				t.Errorf("Expected no match but got score %d", score)
			}

			if tt.shouldMatch && matchType != tt.wantMatchType {
				t.Errorf("Expected matchType %q, got %q", tt.wantMatchType, matchType)
			}
		})
	}
}

func TestComputeMatchScore_Ranking(t *testing.T) {
	// Test that functions score better than fields for same match quality
	query := "Process"
	queryLower := "process"

	scoreField, _ := computeMatchScore(query, queryLower, "ProcessedStatus", "processedstatus", ast.SymbolKindField, "src/main.go", true)
	scoreFunc, _ := computeMatchScore(query, queryLower, "ProcessedStatus", "processedstatus", ast.SymbolKindFunction, "src/main.go", true)

	if scoreFunc >= scoreField {
		t.Errorf("Function should score better than field: func=%d, field=%d", scoreFunc, scoreField)
	}

	// Test that earlier matches score better
	scoreEarly, _ := computeMatchScore(query, queryLower, "ProcessData", "processdata", ast.SymbolKindFunction, "src/main.go", true)
	scoreLater, _ := computeMatchScore(query, queryLower, "getDatesToProcess", "getdatestoprocess", ast.SymbolKindFunction, "src/main.go", true)

	if scoreEarly >= scoreLater {
		t.Errorf("Earlier match should score better: early=%d, later=%d", scoreEarly, scoreLater)
	}

	// Test that shorter names score better (same match type and position)
	scoreShort, _ := computeMatchScore(query, queryLower, "ProcessData", "processdata", ast.SymbolKindFunction, "src/main.go", true)
	scoreLong, _ := computeMatchScore(query, queryLower, "ProcessDataWithExtraStuff", "processdatawithextrastuff", ast.SymbolKindFunction, "src/main.go", true)

	if scoreShort >= scoreLong {
		t.Errorf("Shorter name should score better: short=%d, long=%d", scoreShort, scoreLong)
	}
}

// TestComputeMatchScore_MultiSignal tests IT-05 SR1 contextual penalties.
func TestComputeMatchScore_MultiSignal(t *testing.T) {
	query := "main"
	queryLower := "main"

	t.Run("source file outranks test file for exact match", func(t *testing.T) {
		scoreSource, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "cmd/main.go", true)
		scoreTest, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "cmd/main_test.go", true)

		if scoreSource >= scoreTest {
			t.Errorf("source file should outrank test file: source=%d, test=%d", scoreSource, scoreTest)
		}
	})

	t.Run("exported outranks unexported for exact match", func(t *testing.T) {
		scoreExported, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "cmd/main.go", true)
		scoreUnexported, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "cmd/main.go", false)

		if scoreExported >= scoreUnexported {
			t.Errorf("exported should outrank unexported: exported=%d, unexported=%d", scoreExported, scoreUnexported)
		}
	})

	t.Run("shallow file outranks deep nested file", func(t *testing.T) {
		scoreShallow, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "cmd/main.go", true)
		scoreDeep, _ := computeMatchScore(query, queryLower, "main", "main", ast.SymbolKindFunction, "internal/warpc/gen/tools/main.go", true)

		if scoreShallow >= scoreDeep {
			t.Errorf("shallow file should outrank deep file: shallow=%d, deep=%d", scoreShallow, scoreDeep)
		}
	})

	t.Run("underscore prefix gets additional penalty", func(t *testing.T) {
		scoreNormal, _ := computeMatchScore("render", "render", "render", "render", ast.SymbolKindFunction, "scene.ts", false)
		scoreUnderscore, _ := computeMatchScore("render", "render", "_render", "_render", ast.SymbolKindFunction, "scene.ts", false)

		// _render should have both unexported penalty and underscore penalty
		if scoreNormal >= scoreUnderscore {
			t.Errorf("non-underscore should outrank underscore prefix: normal=%d, underscore=%d", scoreNormal, scoreUnderscore)
		}
	})

	t.Run("source fuzzy beats test exact for same name", func(t *testing.T) {
		// An exported function in source should beat a test file exact match
		scoreSourceExact, _ := computeMatchScore("pivot_table", "pivot_table", "pivot_table", "pivot_table",
			ast.SymbolKindFunction, "pandas/core/reshape.py", true)
		scoreTestExact, _ := computeMatchScore("pivot_table", "pivot_table", "pivot_table", "pivot_table",
			ast.SymbolKindFunction, "pandas/tests/test_expressions.py", true)

		if scoreSourceExact >= scoreTestExact {
			t.Errorf("source exact should outrank test exact: source=%d, test=%d", scoreSourceExact, scoreTestExact)
		}
	})
}

// TestIsTestFile tests all language test file detection patterns.
func TestIsTestFile(t *testing.T) {
	tests := []struct {
		filePath string
		want     bool
	}{
		// Go
		{"cmd/main_test.go", true},
		{"pkg/handler_test.go", true},
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
		{"src/handler.test.tsx", true},
		{"src/handler.js", false},
		{"src/handler.ts", false},

		// Directory patterns
		{"test/handler.go", true},
		{"tests/handler.py", true},
		{"__tests__/handler.js", true},
		{"testing/helper.go", true},

		// Edge cases
		{"", false},
		{"src/contestant.go", false},
		{"src/latest.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			got := isTestFile(tt.filePath)
			if got != tt.want {
				t.Errorf("isTestFile(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

// TestComputeContextualPenalties tests the penalty computation directly.
func TestComputeContextualPenalties(t *testing.T) {
	t.Run("zero penalty for exported source file at depth 2", func(t *testing.T) {
		penalty := computeContextualPenalties("src/handler.go", true, "Handler")
		if penalty != 0 {
			t.Errorf("expected 0 penalty for normal source file, got %d", penalty)
		}
	})

	t.Run("test file gets 50000 penalty", func(t *testing.T) {
		penalty := computeContextualPenalties("tests/test_handler.py", true, "handler")
		if penalty < 50000 {
			t.Errorf("expected >= 50000 penalty for test file, got %d", penalty)
		}
	})

	t.Run("unexported gets 20000 penalty", func(t *testing.T) {
		penalty := computeContextualPenalties("src/handler.go", false, "handler")
		if penalty < 20000 {
			t.Errorf("expected >= 20000 penalty for unexported, got %d", penalty)
		}
	})

	t.Run("deep path gets depth penalty", func(t *testing.T) {
		penalty := computeContextualPenalties("a/b/c/d/e/handler.go", true, "handler")
		// Depth = 4 slashes, beyond 2 = 2 * 1000 = 2000
		if penalty < 2000 {
			t.Errorf("expected >= 2000 penalty for deep path, got %d", penalty)
		}
	})

	t.Run("penalties are cumulative", func(t *testing.T) {
		// Test file + unexported + underscore + deep = all penalties combined
		penalty := computeContextualPenalties("a/b/c/tests/test_handler.py", false, "_handler")
		expectedMin := 50000 + 20000 + 10000 + 1000 // test + unexported + underscore + depth
		if penalty < expectedMin {
			t.Errorf("expected >= %d cumulative penalty, got %d", expectedMin, penalty)
		}
	})
}

func TestFindCamelCaseWordMatch(t *testing.T) {
	tests := []struct {
		name       string
		symbolName string
		query      string
		wantPos    int
	}{
		{
			name:       "match at start",
			symbolName: "ProcessData",
			query:      "Process",
			wantPos:    0,
		},
		{
			name:       "match at camelCase boundary",
			symbolName: "getDatesToProcess",
			query:      "Process",
			wantPos:    10, // "Process" starts at index 10
		},
		{
			name:       "match at camelCase boundary - Data",
			symbolName: "ProcessData",
			query:      "Data",
			wantPos:    7,
		},
		{
			name:       "no match - not word boundary",
			symbolName: "Unprocessed",
			query:      "process",
			wantPos:    -1,
		},
		{
			name:       "case insensitive",
			symbolName: "getDatesToProcess",
			query:      "process",
			wantPos:    10, // "process" starts at index 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPos := findCamelCaseWordMatch(tt.symbolName, tt.query)
			if gotPos != tt.wantPos {
				t.Errorf("findCamelCaseWordMatch(%q, %q) = %d, want %d",
					tt.symbolName, tt.query, gotPos, tt.wantPos)
			}
		})
	}
}

func toLower(s string) string {
	// Simple helper for test
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			result[i] = s[i] + 32
		} else {
			result[i] = s[i]
		}
	}
	return string(result)
}
