// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// Shared Helper Functions for Graph Query Tools
// =============================================================================
//
// NOTE: Some helper functions like extractNameFromNodeID, extractPackageFromNodeID,
// matchesKind, and minInt are defined in graph_query_tools.go and will be migrated
// here as part of the TOOLS-01 refactoring ticket.

// DetectEntryPoint finds a suitable entry point for dominator analysis.
//
// Description:
//
//	Searches for well-known entry point functions (main, init) using the symbol
//	index, then falls back to graph analytics to detect nodes with no incoming edges.
//	This is the canonical method for finding entry points and should be used by all
//	tools requiring dominator tree computation.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - idx: Symbol index for name-based search. May be nil (will skip index search).
//   - analytics: Graph analytics for detecting entry nodes. Must not be nil.
//
// Outputs:
//   - string: Node ID of the detected entry point.
//   - error: Non-nil if no suitable entry point found.
//
// Thread Safety: Safe for concurrent use (read-only operations).
func DetectEntryPoint(ctx context.Context, idx *index.SymbolIndex, analytics *graph.GraphAnalytics) (string, error) {
	// Try to find main or init functions first using the index
	if idx != nil {
		for _, name := range []string{"main", "Main", "init", "Init"} {
			results, err := idx.Search(ctx, name, 1)
			if err == nil && len(results) > 0 {
				return results[0].ID, nil
			}
		}
	}

	// Fall back to graph analytics to detect entry nodes (no incoming edges)
	if analytics != nil {
		entryNodes := analytics.DetectEntryNodes(ctx)
		if len(entryNodes) > 0 {
			return entryNodes[0], nil
		}
	}

	return "", fmt.Errorf("no suitable entry point found in graph")
}

// extractFileFromNodeID extracts the file path from a node ID.
//
// Node IDs follow the format: "path/to/file.go:line:name"
// This extracts the "path/to/file.go" portion.
//
// Thread Safety: Safe for concurrent use.
func extractFileFromNodeID(nodeID string) string {
	for i, c := range nodeID {
		if c == ':' {
			return nodeID[:i]
		}
	}
	return ""
}

// extractLineFromNodeID extracts the line number from a node ID.
//
// Node IDs follow the format: "path/to/file.go:line:name"
// This extracts the "line" portion as an integer.
//
// Thread Safety: Safe for concurrent use.
func extractLineFromNodeID(nodeID string) int {
	colonCount := 0
	start := 0
	for i, c := range nodeID {
		if c == ':' {
			colonCount++
			if colonCount == 1 {
				start = i + 1
			} else if colonCount == 2 {
				// Parse line number
				line := 0
				for j := start; j < i; j++ {
					c := nodeID[j]
					if c >= '0' && c <= '9' {
						line = line*10 + int(c-'0')
					} else {
						return 0
					}
				}
				return line
			}
		}
	}
	return 0
}

// extractNameFromNodeID extracts the symbol name from a node ID.
//
// Node IDs follow the format: "path/to/file.go:line:name"
// This extracts the "name" portion.
//
// Thread Safety: Safe for concurrent use.
func extractNameFromNodeID(nodeID string) string {
	colonCount := 0
	for i, c := range nodeID {
		if c == ':' {
			colonCount++
			if colonCount == 2 {
				return nodeID[i+1:]
			}
		}
	}
	return nodeID
}

// minInt returns the smaller of two integers.
//
// Thread Safety: Safe for concurrent use.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// maxInt returns the larger of two integers.
//
// Thread Safety: Safe for concurrent use.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clampInt clamps a value between min and max bounds.
//
// Thread Safety: Safe for concurrent use.
func clampInt(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

// parseStringArray extracts a string array from a parameter value.
//
// Handles both []string and []interface{} (from JSON unmarshaling).
//
// Thread Safety: Safe for concurrent use.
func parseStringArray(value any) ([]string, bool) {
	switch v := value.(type) {
	case []string:
		return v, true
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

// parseIntParam extracts an integer from a parameter value.
//
// Handles both int and float64 (from JSON unmarshaling).
//
// Thread Safety: Safe for concurrent use.
func parseIntParam(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

// parseFloatParam extracts a float64 from a parameter value.
//
// Thread Safety: Safe for concurrent use.
func parseFloatParam(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// parseBoolParam extracts a boolean from a parameter value.
//
// Thread Safety: Safe for concurrent use.
func parseBoolParam(value any) (bool, bool) {
	if b, ok := value.(bool); ok {
		return b, true
	}
	return false, false
}

// parseStringParam extracts a string from a parameter value.
//
// Thread Safety: Safe for concurrent use.
func parseStringParam(value any) (string, bool) {
	if s, ok := value.(string); ok {
		return s, true
	}
	return "", false
}

// =============================================================================
// Package Scope Matching
// =============================================================================

// matchesPackageScope checks if a symbol matches a package filter using
// boundary-aware matching across multiple strategies.
//
// Description:
//
//	Determines whether a symbol belongs to a given package scope. This function
//	handles cross-language scoping: Go symbols have Package populated from the
//	package declaration, while Python/JS/TS symbols have Package="" and must be
//	matched via file path or file stem.
//
//	Uses containsPackageSegment() (from symbol_resolution.go) for boundary-aware
//	matching that avoids false positives like "log" matching "dialog".
//
// Inputs:
//   - sym: The symbol to check. Must not be nil.
//   - packageFilter: The package/scope name to match against. If empty, returns true.
//
// Outputs:
//   - bool: True if the symbol matches the package filter.
//
// Match strategies (tried in order):
//  1. Boundary-aware match on sym.Package (Go symbols where Package is populated)
//  2. Boundary-aware match on sym.FilePath (all languages — matches directory segments)
//  3. File stem match: base name without extension == filter (handles "engine.ts" matching "engine")
//
// Thread Safety: Safe for concurrent use (read-only operations).
func matchesPackageScope(sym *ast.Symbol, packageFilter string) bool {
	if packageFilter == "" {
		return true
	}
	if sym == nil {
		return false
	}

	// IT-08d: Strip trailing slash so "src/utils/" matches "src/utils/file.ts"
	// via boundary check (haystack[endPos]='/') instead of failing (haystack[endPos]='m').
	lowerFilter := strings.ToLower(strings.TrimRight(packageFilter, "/"))

	// Strategy 1: Boundary-aware match on sym.Package (Go symbols)
	if sym.Package != "" {
		if containsPackageSegment(strings.ToLower(sym.Package), lowerFilter) {
			return true
		}
	}

	// Strategy 2: Boundary-aware match on sym.FilePath (all languages)
	if sym.FilePath != "" {
		if containsPackageSegment(strings.ToLower(sym.FilePath), lowerFilter) {
			return true
		}
	}

	// Strategy 3: File stem match (base name without extension)
	// Handles single-file modules: "engine" matches "engine.ts"
	if sym.FilePath != "" {
		base := filepath.Base(sym.FilePath)
		ext := filepath.Ext(base)
		stem := strings.ToLower(strings.TrimSuffix(base, ext))
		if stem == lowerFilter {
			return true
		}
	}

	return false
}

// =============================================================================
// Symbol Name Validation
// =============================================================================

// genericWords is the set of common English words that are never valid symbol names.
// When an LLM extracts a parameter from a natural-language query, it sometimes picks up
// a generic noun (e.g., "classes" from "What classes extend Scale?") instead of the
// actual symbol name ("Scale"). This set catches those misextractions early so the tool
// can return a clear error message guiding the LLM to retry with the correct name.
//
// The set is intentionally broad: a false positive (rejecting a real symbol named
// "function") is far less costly than a false negative (querying the graph for "classes"
// and returning zero results with no explanation).
var genericWords = map[string]bool{
	// Programming construct nouns — the most common misextractions
	"class":      true,
	"classes":    true,
	"interface":  true,
	"interfaces": true,
	"struct":     true,
	"structs":    true,
	"type":       true,
	"types":      true,
	"function":   true,
	"functions":  true,
	"method":     true,
	"methods":    true,
	"module":     true,
	"modules":    true,
	"package":    true,
	"packages":   true,
	"variable":   true,
	"variables":  true,
	"constant":   true,
	"constants":  true,
	"prototype":  true,
	"prototypes": true,
	// IT-R2b Fix 2: Removed "constructor"/"constructors" — valid JS/TS symbol name.
	// Resolution pipeline handles disambiguation of multiple constructor symbols.
	"object":     true,
	"objects":    true,
	"property":   true,
	"properties": true,
	"field":      true,
	"fields":     true,
	"parameter":  true,
	"parameters": true,
	"argument":   true,
	"arguments":  true,
	"enum":       true,
	"enums":      true,

	// Relationship nouns — LLM picks these from "find all X" queries
	"implementation":  true,
	"implementations": true,
	"extension":       true,
	"extensions":      true,
	"subclass":        true,
	"subclasses":      true,
	"superclass":      true,
	"superclasses":    true,
	"derivative":      true,
	"derivatives":     true,
	"caller":          true,
	"callers":         true,
	"callee":          true,
	"callees":         true,
	"reference":       true,
	"references":      true,
	"dependency":      true,
	"dependencies":    true,
	"parent":          true,
	"parents":         true,
	"child":           true,
	"children":        true,

	// English articles, pronouns, and prepositions that slip through
	"the":     true,
	"a":       true,
	"an":      true,
	"all":     true,
	"any":     true,
	"some":    true,
	"every":   true,
	"each":    true,
	"this":    true,
	"that":    true,
	"it":      true,
	"them":    true,
	"what":    true,
	"which":   true,
	"who":     true,
	"how":     true,
	"where":   true,
	"when":    true,
	"from":    true,
	"into":    true,
	"with":    true,
	"base":    true,
	"given":   true,
	"code":    true,
	"file":    true,
	"files":   true,
	"symbol":  true,
	"symbols": true,
	"name":    true,
	"names":   true,
}

// isGenericWord returns true if the lowercased input matches a known generic English
// word that should never be used as a symbol name in a graph query.
//
// Description:
//
//	Checks whether a candidate symbol name is actually a generic English word
//	that the LLM misextracted from a natural-language query. For example, given
//	the query "What classes extend Scale?", an LLM might extract "classes" as the
//	symbol name instead of "Scale". This function catches that mistake.
//
// Inputs:
//   - name: The candidate symbol name. Must not be empty.
//
// Outputs:
//   - bool: True if the name is a known generic word, false otherwise.
//
// Thread Safety: Safe for concurrent use (read-only map access).
func isGenericWord(name string) bool {
	return genericWords[strings.ToLower(strings.TrimSpace(name))]
}

// ValidateSymbolName checks that a symbol name parameter is non-empty and not a
// generic English word. Returns a user-facing error message that guides the LLM
// to retry with the correct symbol name.
//
// Description:
//
//	Shared validation for all tools that accept a symbol/function/type name
//	parameter from LLM-extracted input. Rejects empty strings and generic words
//	with an error message that includes example symbol names to help the LLM
//	self-correct on retry.
//
// Inputs:
//   - name: The candidate symbol name to validate.
//   - paramName: The parameter name for error messages (e.g., "function_name", "interface_name").
//   - examples: Example valid symbol names for the error hint (e.g., "handleRequest", "Router").
//
// Outputs:
//   - error: Non-nil if validation fails, with a descriptive message.
//
// Thread Safety: Safe for concurrent use.
func ValidateSymbolName(name, paramName string, examples string) error {
	if name == "" {
		return fmt.Errorf("%s is required", paramName)
	}
	if isGenericWord(name) {
		return fmt.Errorf("%s '%s' appears to be a generic word, not a symbol name. "+
			"Please extract the actual symbol name from the query "+
			"(e.g., %s)", paramName, name, examples)
	}
	return nil
}
