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

	scoreField, _ := computeMatchScore(query, queryLower, "ProcessedStatus", "processedstatus", ast.SymbolKindField)
	scoreFunc, _ := computeMatchScore(query, queryLower, "ProcessedStatus", "processedstatus", ast.SymbolKindFunction)

	if scoreFunc >= scoreField {
		t.Errorf("Function should score better than field: func=%d, field=%d", scoreFunc, scoreField)
	}

	// Test that earlier matches score better
	scoreEarly, _ := computeMatchScore(query, queryLower, "ProcessData", "processdata", ast.SymbolKindFunction)
	scoreLater, _ := computeMatchScore(query, queryLower, "getDatesToProcess", "getdatestoprocess", ast.SymbolKindFunction)

	if scoreEarly >= scoreLater {
		t.Errorf("Earlier match should score better: early=%d, later=%d", scoreEarly, scoreLater)
	}

	// Test that shorter names score better (same match type and position)
	scoreShort, _ := computeMatchScore(query, queryLower, "ProcessData", "processdata", ast.SymbolKindFunction)
	scoreLong, _ := computeMatchScore(query, queryLower, "ProcessDataWithExtraStuff", "processdatawithextrastuff", ast.SymbolKindFunction)

	if scoreShort >= scoreLong {
		t.Errorf("Shorter name should score better: short=%d, long=%d", scoreShort, scoreLong)
	}
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
