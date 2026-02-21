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

// execute_helpers.go contains standalone utility functions extracted from execute.go
// as part of CB-30c Phase 2 decomposition.

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// -----------------------------------------------------------------------------
// Semantic Correction Cache (GR-Phase1)
// -----------------------------------------------------------------------------

// semanticCorrectionCache tracks which corrections have been applied per session.
// This is a simple in-memory cache to avoid duplicate warnings when Execute()
// is called multiple times for the same query.
var (
	semanticCorrectionCache   = make(map[string]bool) // key: "sessionID:queryHash:tool"
	semanticCorrectionCacheMu sync.RWMutex
)

// markSemanticCorrectionApplied records that a semantic correction was applied.
func markSemanticCorrectionApplied(sessionID, query, correctedTool string) {
	key := buildSemanticCorrectionKey(sessionID, query, correctedTool)
	semanticCorrectionCacheMu.Lock()
	semanticCorrectionCache[key] = true
	semanticCorrectionCacheMu.Unlock()
}

// hasSemanticCorrectionInCache checks if a correction was already applied.
func hasSemanticCorrectionInCache(sessionID, query, correctedTool string) bool {
	key := buildSemanticCorrectionKey(sessionID, query, correctedTool)
	semanticCorrectionCacheMu.RLock()
	defer semanticCorrectionCacheMu.RUnlock()
	return semanticCorrectionCache[key]
}

// buildSemanticCorrectionKey creates a cache key from session, query, and tool.
func buildSemanticCorrectionKey(sessionID, query, correctedTool string) string {
	// Use first 50 chars of query to avoid huge keys
	queryKey := query
	if len(queryKey) > 50 {
		queryKey = queryKey[:50]
	}
	return fmt.Sprintf("%s:%s:%s", sessionID, queryKey, correctedTool)
}

// ClearSemanticCorrectionCache clears the cache (for testing).
func ClearSemanticCorrectionCache() {
	semanticCorrectionCacheMu.Lock()
	semanticCorrectionCache = make(map[string]bool)
	semanticCorrectionCacheMu.Unlock()
}

// -----------------------------------------------------------------------------
// String Truncation Utilities
// -----------------------------------------------------------------------------

// truncateString truncates a string to maxLen with ellipsis.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// truncateQuery truncates a query string for logging.
func truncateQuery(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateOutput truncates a string to maxLen characters.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// truncateForLog truncates a string for logging, attempting word boundaries.
//
// # Inputs
//
//   - s: String to truncate.
//   - maxLen: Maximum length.
//
// # Outputs
//
//   - string: Truncated string with "..." suffix if truncated.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Find last space before maxLen
	truncated := s[:maxLen]
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}

// -----------------------------------------------------------------------------
// Parameter Extraction Utilities
// -----------------------------------------------------------------------------

// getStringParamFromToolParams extracts a string parameter from tool parameters.
//
// Inputs:
//
//	params - The tool parameters.
//	key - The parameter key to extract.
//
// Outputs:
//
//	string - The parameter value, or empty string if not found
func getStringParamFromToolParams(params *agent.ToolParameters, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params.GetString(key); ok {
		return v
	}
	return ""
}

// toolParamsToMap converts ToolParameters to a map for internal tool execution.
//
// Inputs:
//
//	params - The tool parameters
//
// Outputs:
//
//	map[string]any - Parameters as a map
func toolParamsToMap(params *agent.ToolParameters) map[string]any {
	result := make(map[string]any)
	if params == nil {
		return result
	}

	for k, v := range params.StringParams {
		result[k] = v
	}
	for k, v := range params.IntParams {
		result[k] = v
	}
	for k, v := range params.BoolParams {
		result[k] = v
	}

	return result
}

// extractPackageNameFromQuery extracts a package name from a query string.
//
// Description:
//
//	Uses simple heuristics to identify package names in queries like:
//	  - "about package X"
//	  - "about the X package"
//	  - "pkg/something" or "path/to/package"
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	string - The extracted package name, or empty if not found.
func extractPackageNameFromQuery(query string) string {
	query = strings.ToLower(query)

	// Pattern 1: "about package X" or "about the X package"
	if idx := strings.Index(query, "package"); idx >= 0 {
		after := query[idx+7:]
		words := strings.Fields(after)
		if len(words) > 0 {
			pkg := strings.Trim(words[0], "?,.")
			if pkg != "" && pkg != "the" && pkg != "a" && pkg != "an" {
				return pkg
			}
			if len(words) > 1 {
				pkg = strings.Trim(words[1], "?,.")
				return pkg
			}
		}
	}

	// Pattern 2: "pkg/something" or "path/to/package"
	if strings.Contains(query, "pkg/") || strings.Contains(query, "/") {
		words := strings.Fields(query)
		for _, word := range words {
			if strings.Contains(word, "/") {
				return strings.Trim(word, "?,.")
			}
		}
	}

	return ""
}

// extractPackageContextFromQuery extracts a package/module context hint from
// a natural language query. Used to disambiguate symbol resolution when multiple
// symbols share the same name across different packages.
//
// Description:
//
//	IT-06c Bug C: Queries like "What functions does the Build method call in hugolib?"
//	contain package context ("in hugolib") that should be used to disambiguate
//	when multiple symbols match "Build". This function extracts such hints.
//
//	Recognized patterns:
//	  - "in <package>" (most common): "in hugolib", "in flask", "in gin"
//	  - "from <package>": "from the hugolib package"
//	  - "of <package>": "of the express module"
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	string - The extracted package/module context, or empty if not found.
//
// Thread Safety: Safe for concurrent use (stateless function).
func extractPackageContextFromQuery(query string) string {
	lowerQuery := strings.ToLower(query)
	words := strings.Fields(lowerQuery)

	// Pattern 1: "in <package>" at end of query or before punctuation.
	// Match the LAST "in <word>" that looks like a package name.
	// Skip "in the codebase", "in the graph", etc.
	skipAfterIn := map[string]bool{
		"the": true, "a": true, "an": true, "this": true, "that": true,
		"codebase": true, "code": true, "graph": true, "project": true,
		"repo": true, "repository": true, "system": true, "source": true,
	}

	for i := len(words) - 2; i >= 0; i-- {
		if words[i] == "in" {
			candidate := strings.Trim(words[i+1], "?,.()")
			if candidate == "" || skipAfterIn[candidate] {
				continue
			}
			// Must look like a package name: lowercase, alphanumeric, no spaces
			if isPackageLikeName(candidate) {
				return candidate
			}
		}
	}

	// Pattern 2: "<package> package/module/library" — "the hugolib package"
	packageKeywords := map[string]bool{
		"package": true, "module": true, "library": true, "lib": true,
	}
	for i := 0; i < len(words)-1; i++ {
		if packageKeywords[words[i+1]] {
			candidate := strings.Trim(words[i], "?,.()")
			if candidate == "the" || candidate == "a" || candidate == "an" {
				continue
			}
			if isPackageLikeName(candidate) {
				return candidate
			}
		}
	}

	return ""
}

// isPackageLikeName returns true if the string looks like a package or module name.
// Package names are typically lowercase, alphanumeric, and may contain underscores or hyphens.
func isPackageLikeName(s string) bool {
	if len(s) == 0 || len(s) > 50 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// extractFunctionNameFromQuery extracts a function name from a natural language query.
//
// Description:
//
//	GR-Phase1: Extracts function names for find_callers/find_callees parameter extraction.
//	IT-00a-1 Phase 3: Thin wrapper over extractFunctionNameCandidates that returns
//	the highest-priority candidate.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	string - The extracted function name, or empty if not found.
func extractFunctionNameFromQuery(query string) string {
	candidates := extractFunctionNameCandidates(query)
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

// extractFunctionNameCandidates extracts ranked function name candidates from a
// natural language query. Each pattern contributes candidates instead of returning
// the first match, enabling callers to try multiple candidates against symbol
// resolution when the first extraction is wrong.
//
// Description:
//
//	IT-00a-1 Phase 3: Multi-candidate extraction replaces single-shot extraction.
//	Handles patterns like:
//	  - "What does main call?" → ["main"]
//	  - "Who calls parseConfig?" → ["parseConfig"]
//	  - "find callers of handleRequest" → ["handleRequest"]
//	  - "Build the call graph from ProcessData" → ["ProcessData"]
//	  - "Show callers of BuildRequest in the handler" → ["BuildRequest", "handler"]
//
//	Patterns are evaluated in priority order (0 → 7). Each pattern appends candidates
//	to the list; duplicates are suppressed. The first candidate is always the
//	highest-confidence extraction.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	[]string - Ranked function name candidates (best first). May be empty.
//
// Thread Safety: Safe for concurrent use (stateless function).
func extractFunctionNameCandidates(query string) []string {
	var candidates []string
	seen := make(map[string]bool)

	addCandidate := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			candidates = append(candidates, name)
		}
	}

	lowerQuery := strings.ToLower(query)

	// IT-05 FN1: Strip compound phrases before word-level extraction.
	// "Show the call chain from X" contains "call" which Pattern 1/3 would
	// extract as a function name. Stripping "call chain" first prevents this.
	// Both query and lowerQuery must be sanitized in sync to preserve index alignment.
	skipPhrases := []string{
		"call chain", "call graph", "call hierarchy", "call tree",
		"call stack", "call flow", "call path",
	}
	for _, phrase := range skipPhrases {
		for {
			idx := strings.Index(strings.ToLower(query), phrase)
			if idx < 0 {
				break
			}
			query = query[:idx] + query[idx+len(phrase):]
		}
	}
	lowerQuery = strings.ToLower(query)

	// IT-05 Run 2 Fix: Compute "to" boundary for "from X to Y" queries.
	// In "Show the call chain from Engine.runRenderLoop to mesh rendering",
	// words after "to" are destination concepts (mesh, rendering), not source functions.
	// Patterns 5-7 (positional scans) must not extract words beyond this boundary.
	// Patterns 0-4 are context-aware (specific keyword triggers) and operate on the full query.
	fallbackBoundary := len(query) // default: no boundary (use full query)
	if fromIdx := strings.Index(lowerQuery, " from "); fromIdx >= 0 {
		if toIdx := strings.Index(lowerQuery[fromIdx:], " to "); toIdx >= 0 {
			fallbackBoundary = fromIdx + toIdx
		}
	}

	// IT-01 Bug 3: Pattern 0 — "X method/function on (the) Y type/class/struct"
	// Handles: "Who calls the Get method on the Transaction type?" → "Transaction.Get"
	// Handles: "Who calls the render method on the Scene type?" → "Scene.render"
	// Must come first because "Get" is in skipWords and would be lost by later patterns.
	if dotName := extractTypeDotMethodFromQuery(query); dotName != "" {
		addCandidate(dotName)
	}

	// Pattern 0b: Direct dot-notation in query — "Who calls Transaction.Get?"
	// If the query contains an explicit Type.Method token, extract it directly.
	for _, word := range strings.Fields(query) {
		candidate := strings.Trim(word, "?,.()")
		if strings.Contains(candidate, ".") {
			parts := strings.SplitN(candidate, ".", 2)
			if len(parts) == 2 && isValidTypeName(parts[0]) && len(parts[1]) > 0 {
				// Reject file extensions — "Babylon.js", "Express.ts", "Flask.py" are not Type.Method
				if isFileExtension(parts[1]) {
					continue
				}
				// Validate the method part starts with a letter (reject "Foo.123")
				methodFirstRune := rune(parts[1][0])
				if unicode.IsLetter(methodFirstRune) || parts[1][0] == '_' {
					addCandidate(candidate)
				}
			}
		}
	}

	// Pattern 1: "what does X call" or "what functions does X call"
	// IT-06c: Skip articles ("the", "a", "an") between "does"/"do" and the function name.
	// "What functions does the Build method call" → skip "the" → extract "Build".
	if strings.Contains(lowerQuery, "call") {
		words := strings.Fields(query) // Keep original case
		for i, word := range words {
			lowerWord := strings.ToLower(word)
			if lowerWord == "does" || lowerWord == "do" {
				// Skip articles between "does"/"do" and the function name
				j := i + 1
				for j < len(words) {
					article := strings.ToLower(words[j])
					if article == "the" || article == "a" || article == "an" {
						j++
						continue
					}
					break
				}
				if j < len(words) {
					candidate := strings.Trim(words[j], "?,.()")
					if isValidFunctionName(candidate) {
						addCandidate(candidate)
					}
				}
			}
		}
	}

	// Pattern 2: "callers of X" or "callees of X"
	// CR-R2-6: Apply fallbackBoundary — skip " of " matches in the destination zone.
	if idx := strings.Index(lowerQuery, " of "); idx >= 0 {
		if !(fallbackBoundary < len(query) && idx >= fallbackBoundary) {
			after := query[idx+4:] // Keep original case
			words := strings.Fields(after)
			if len(words) > 0 {
				candidate := strings.Trim(words[0], "?,.()")
				if isValidFunctionName(candidate) {
					addCandidate(candidate)
				}
			}
		}
	}

	// Pattern 3: "who/what calls X" - function name after "calls"
	if idx := strings.Index(lowerQuery, "calls "); idx >= 0 {
		after := query[idx+6:] // Keep original case
		words := strings.Fields(after)
		if len(words) > 0 {
			candidate := strings.Trim(words[0], "?,.()")
			if isValidFunctionName(candidate) {
				addCandidate(candidate)
			}
		}
	}

	// Pattern 4: "called by X" - function name after "by"
	if idx := strings.Index(lowerQuery, "called by "); idx >= 0 {
		after := query[idx+10:] // Keep original case
		words := strings.Fields(after)
		if len(words) > 0 {
			candidate := strings.Trim(words[0], "?,.()")
			if isValidFunctionName(candidate) {
				addCandidate(candidate)
			}
		}
	}

	// IT-06 Pattern 4b: "from X" — extract function name after "from"
	// Handles: "Show call chain from main", "upstream from parseConfig"
	// This is a context-aware pattern that uses isValidFunctionName (not isFunctionLikeName),
	// allowing lowercase single-word names like "main" that would be rejected by Pattern 7.
	if idx := strings.Index(lowerQuery, " from "); idx >= 0 {
		after := query[idx+6:] // Keep original case
		fromWords := strings.Fields(after)
		if len(fromWords) > 0 {
			candidate := strings.Trim(fromWords[0], "?,.()")
			if isValidFunctionName(candidate) {
				addCandidate(candidate)
			}
		}
	}

	// P0 Fix (Feb 14, 2026): Pattern 5: "for X function/method" or "of X function/method"
	// Handles queries like "control dependencies for Process function"
	// CR-R2-1: Apply fallbackBoundary — skip matches in the destination zone of "from X to Y".
	// IT-06: Use isValidSymbolNameBeforeKindKeyword for Pattern 5 as well,
	// matching the fix applied to Pattern 6.
	for _, pattern := range []string{" for ", " of "} {
		if idx := strings.Index(lowerQuery, pattern); idx >= 0 {
			// CR-R2-1: If this "for"/"of" match starts at or past the "to" boundary,
			// it's in the destination zone — skip it.
			if fallbackBoundary < len(query) && idx >= fallbackBoundary {
				continue
			}
			after := query[idx+len(pattern):] // Keep original case
			words := strings.Fields(after)
			// Look for pattern: <Symbol> function/method
			for i := 0; i < len(words)-1; i++ {
				nextWordLower := strings.ToLower(words[i+1])
				if isSymbolKindKeyword(nextWordLower) {
					candidate := strings.Trim(words[i], "?,.()")
					if isValidSymbolNameBeforeKindKeyword(candidate) {
						addCandidate(candidate)
					}
				}
			}
		}
	}

	// P0 Fix (Feb 14, 2026): Pattern 6: "X function/method/class/struct" anywhere in query
	// Handles: "What dominates Process function", "Find Process method"
	// IT-04 Fix: Also handles "Where is the TransformNode class defined?"
	// IT-05 Run 2 Fix: Apply fallbackBoundary to Pattern 6 as well — "view function"
	// after "to" in "from X to a view function" is a destination, not the source.
	// CR-R2-2: Use strings.Fields on the truncated query to compute the word limit.
	// This handles whitespace normalization correctly (compound phrase stripping can
	// create double spaces that strings.Fields collapses).
	words := strings.Fields(query)
	fallbackWordLimit := len(words) // default: scan all words
	if fallbackBoundary < len(query) {
		fallbackWordLimit = len(strings.Fields(query[:fallbackBoundary]))
	}
	// CR-R2-5: The -1 is intentional: Pattern 6 peeks at words[i+1] for the kind keyword,
	// so we need the peeked word to also be before the boundary.
	// IT-06: Use isValidSymbolNameBeforeKindKeyword instead of isValidFunctionName
	// because when a word is immediately followed by a kind keyword ("Component type"),
	// it IS the symbol name even if it's also a programming term.
	for i := 0; i < fallbackWordLimit-1 && i < len(words)-1; i++ {
		nextWordLower := strings.ToLower(words[i+1])
		if isSymbolKindKeyword(nextWordLower) {
			candidate := strings.Trim(words[i], "?,.()[]{}\"'")
			if isValidSymbolNameBeforeKindKeyword(candidate) {
				addCandidate(candidate)
			}
		}
	}

	// Pattern 7 (fallback): Look for CamelCase or snake_case function names
	// P0 Fix (Feb 14, 2026): This now correctly skips query keywords like "control", "dependencies"
	// IT-05 Run 2 Fix: Apply fallbackBoundary with CamelCase exemption (R3-3).
	// Before boundary: extract all valid function-like names (existing behavior).
	// After boundary: extract ONLY strictly CamelCase names (e.g., "canActivate", "DataFrame"),
	// which are strong signals for real symbol names. Single-case words like "rendering",
	// "handler", "mesh" are blocked — they are destination concept words.
	if fallbackBoundary < len(query) {
		// Scan before boundary: all valid function-like names
		for _, word := range strings.Fields(query[:fallbackBoundary]) {
			candidate := strings.Trim(word, "?,.()[]{}\"'")
			if isValidFunctionName(candidate) && isFunctionLikeName(candidate) {
				addCandidate(candidate)
			}
		}
		// Scan after boundary: CamelCase-only (IT-05 R3-3)
		for _, word := range strings.Fields(query[fallbackBoundary:]) {
			candidate := strings.Trim(word, "?,.()[]{}\"'")
			if isValidFunctionName(candidate) && isFunctionLikeName(candidate) && isStrictCamelCase(candidate) {
				addCandidate(candidate)
			}
		}
	} else {
		// No boundary: scan full query (original behavior)
		for _, word := range strings.Fields(query) {
			candidate := strings.Trim(word, "?,.()[]{}\"'")
			if isValidFunctionName(candidate) && isFunctionLikeName(candidate) {
				addCandidate(candidate)
			}
		}
	}

	return candidates
}

// extractDestinationCandidates extracts function name candidates from the
// destination portion of "from X to Y" queries.
//
// Description:
//
//	IT-05 R5: For get_call_chain dual-endpoint resolution. Extracts candidates
//	from the text after "to" in "from X to Y" patterns. Returns nil if the
//	query is not a "from X to Y" pattern.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	[]string - Candidate function names from the destination part, or nil.
//
// Thread Safety: Safe for concurrent use (stateless function).
func extractDestinationCandidates(query string) []string {
	lowerQuery := strings.ToLower(query)
	fromIdx := strings.Index(lowerQuery, " from ")
	if fromIdx < 0 {
		return nil
	}
	// Use LastIndex to find the final " to " — handles multi-hop queries like
	// "from login to the dashboard to the settings page" by extracting "the settings page".
	toIdx := strings.LastIndex(lowerQuery[fromIdx:], " to ")
	if toIdx < 0 {
		return nil
	}
	destPart := query[fromIdx+toIdx+4:] // After the last " to "
	return extractFunctionNameCandidates(destPart)
}

// isValidFunctionName checks if a string looks like a valid function name.
func isValidFunctionName(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	// Must start with letter
	if !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return false
	}
	// IT-06b Issue 2: Reject names containing brackets, braces, or angle brackets.
	// These characters are never valid in function/symbol names across any language.
	// Prevents ConversationHistory contamination like "[Tool calls: Grep]" → "Grep]".
	if strings.ContainsAny(s, "[]{}()<>") {
		return false
	}
	// Skip common non-function words (GR-Phase1: expanded for path extraction)
	// P0 Fix (Feb 14, 2026): Added "control", "dependencies", "dependency", "common"
	// to prevent extracting query keywords instead of symbol names
	lower := strings.ToLower(s)
	// IT-04 Audit: Comprehensive skipWords aligned with genericWords (tool_helpers.go).
	// Every word in genericWords should also be here, plus query-specific verbs/adverbs.
	// Gap analysis identified 13 missing words; this list is now exhaustive.
	skipWords := []string{
		// English articles, pronouns, prepositions (aligned with genericWords)
		"the", "a", "an", "this", "that", "what", "who", "how", "which", "where", "when",
		"it", "them", "some", "every", "each", "into", "given",
		// IT-06: Additional prepositions that appear in queries
		"across", "about", "through", "around", "over", "under", "within",
		// Query verbs and adjectives
		"function", "method", "all", "any", "find", "show", "list", "get",
		"path", "from", "to", "between", "and", "or", "with", "for", "in",
		"most", "important", "top", "are", "is", "does", "do", "has", "have",
		"defined", "codebase", "located", "location", "used", "called",
		// IT-05 FN1: Direction/action words for call chain queries
		"upstream", "downstream", "build", "trace", "analyze", "display",
		// Graph/tool relationship nouns
		"these", "those", "connection", "connected", "calls", "callers", "callees",
		"control", "dependencies", "dependency", "common", "dominators", "dominator",
		"references", "reference", "implementations", "implementation", "symbol", "symbols",
		"hotspots", "hotspot", "cycles", "cycle", "communities", "community",
		"extends", "extend", "implements", "implement", "subclass", "subclasses",
		"superclass", "superclasses", "derivative", "derivatives",
		"parent", "parents", "child", "children",
		// Programming construct nouns (aligned with genericWords)
		"classes", "class", "interface", "interfaces", "structs", "struct",
		"base", "abstract", "derive", "derives", "inherit", "inherits",
		"type", "types", "enum", "enums",
		"prototype", "prototypes", "constructor", "constructors",
		"object", "objects", "property", "properties", "field", "fields",
		"variable", "variables", "constant", "constants",
		"parameter", "parameters", "argument", "arguments",
		"module", "modules", "package", "packages",
		"decorator", "component",
		// File/code nouns (aligned with genericWords)
		"code", "file", "files", "name", "names",
		"extension", "extensions",
		"caller", "callee",
		// IT-05 Run 2 Fix: Concept/action words from "from X to Y" query destinations.
		// These are never function names but appear in destination phrases like
		// "to mesh rendering", "to value retrieval", "to handler execution".
		"rendering", "creation", "retrieval", "persistence", "execution",
		"compilation", "initialization", "processing", "assembly",
		"assigning", "parsing", "dispatch", "handling",
		"update", "validation", "construction", "aggregation",
	}
	for _, skip := range skipWords {
		if lower == skip {
			return false
		}
	}
	return true
}

// isValidSymbolNameBeforeKindKeyword checks if a name is a valid symbol name in the
// context of Pattern 6, where the word is immediately followed by a kind keyword
// (e.g., "Component type", "Request object", "Entry struct").
//
// Description:
//
//	IT-06: This uses a SMALLER skipWords list than isValidFunctionName. When a word
//	is followed by a kind keyword, it's strong evidence that the word IS the symbol name,
//	even if it's also a programming construct noun (e.g., "component", "object", "property").
//	We only skip articles, pronouns, prepositions, and query verbs — NOT programming terms.
//
// Inputs:
//   - s: The candidate word to check.
//
// Outputs:
//   - bool: True if the name is valid as a symbol name before a kind keyword.
func isValidSymbolNameBeforeKindKeyword(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	// Must start with letter
	if !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return false
	}
	// IT-06b Issue 2: Reject names containing brackets, braces, or angle brackets.
	if strings.ContainsAny(s, "[]{}()<>") {
		return false
	}
	lower := strings.ToLower(s)
	// Only skip articles, pronouns, prepositions, and query verbs.
	// Do NOT skip programming construct nouns — they are valid symbol names
	// when qualified by a kind keyword ("Component type" = Component is the symbol).
	skipWords := []string{
		// Articles, pronouns, prepositions
		"the", "a", "an", "this", "that", "what", "who", "how", "which", "where", "when",
		"it", "them", "some", "every", "each", "into", "given",
		// Query verbs and adjectives
		"all", "any", "find", "show", "list", "get",
		"from", "to", "between", "and", "or", "with", "for", "in",
		"most", "important", "top", "are", "is", "does", "do", "has", "have",
		"defined", "codebase", "located", "location", "used", "called",
		// IT-06c: Removed "build" from skipWords. When followed by a kind keyword
		// ("Build method", "Build function"), "Build" is clearly a symbol name.
		// The kind keyword disambiguates it from the verb "build".
		"upstream", "downstream", "trace", "analyze", "display",
		"these", "those",
		// Prepositions that show up in queries
		"across", "about", "through", "around", "over", "under",
	}
	for _, skip := range skipWords {
		if lower == skip {
			return false
		}
	}
	return true
}

// extractInterfaceNameFromQuery extracts an interface or base class name from an
// implementation-related query using inheritance-specific patterns.
//
// Description:
//
//	Recognizes patterns specific to find_implementations queries:
//	  "What classes extend AbstractMesh?" → "AbstractMesh"
//	  "What implements the Reader interface?" → "Reader"
//	  "What subclasses Light in Babylon.js?" → "Light"
//	  "Find implementations of SessionInterface" → "SessionInterface"
//
//	Returns "" if no inheritance-specific pattern is found.
//	The caller should fall back to extractFunctionNameFromQuery.
func extractInterfaceNameFromQuery(query string) string {
	lowerQuery := strings.ToLower(query)
	words := strings.Fields(query)

	// Pattern 1: "extend(s) X" — X is the base class/interface
	for _, keyword := range []string{"extends ", "extend "} {
		if idx := strings.Index(lowerQuery, keyword); idx >= 0 {
			after := query[idx+len(keyword):]
			afterWords := strings.Fields(after)
			// Skip articles: "the"
			for _, w := range afterWords {
				candidate := strings.Trim(w, "?,.()")
				candidateLower := strings.ToLower(candidate)
				if candidateLower == "the" || candidateLower == "a" || candidateLower == "an" {
					continue
				}
				if isValidFunctionName(candidate) && !isQueryKeyword(candidateLower) {
					return candidate
				}
				break
			}
		}
	}

	// Pattern 2: "implement(s) X" — X is the interface
	for _, keyword := range []string{"implements ", "implement "} {
		if idx := strings.Index(lowerQuery, keyword); idx >= 0 {
			after := query[idx+len(keyword):]
			afterWords := strings.Fields(after)
			for _, w := range afterWords {
				candidate := strings.Trim(w, "?,.()")
				candidateLower := strings.ToLower(candidate)
				if candidateLower == "the" || candidateLower == "a" || candidateLower == "an" {
					continue
				}
				if isValidFunctionName(candidate) && !isQueryKeyword(candidateLower) {
					return candidate
				}
				break
			}
		}
	}

	// Pattern 3: "subclass(es) of X" or "implementations of X"
	for _, keyword := range []string{"subclasses of ", "subclass of ", "implementations of ", "implementation of "} {
		if idx := strings.Index(lowerQuery, keyword); idx >= 0 {
			after := query[idx+len(keyword):]
			afterWords := strings.Fields(after)
			for _, w := range afterWords {
				candidate := strings.Trim(w, "?,.()")
				candidateLower := strings.ToLower(candidate)
				if candidateLower == "the" || candidateLower == "a" || candidateLower == "an" {
					continue
				}
				if isValidFunctionName(candidate) && !isQueryKeyword(candidateLower) {
					return candidate
				}
				break
			}
		}
	}

	// Pattern 4: "X class" or "X interface" or "X base class" — the word before "class"/"interface"
	for i := 0; i < len(words)-1; i++ {
		nextLower := strings.ToLower(words[i+1])
		if nextLower == "class" || nextLower == "interface" || nextLower == "struct" || nextLower == "type" {
			candidate := strings.Trim(words[i], "?,.()")
			candidateLower := strings.ToLower(candidate)
			if candidateLower == "the" || candidateLower == "a" || candidateLower == "an" ||
				candidateLower == "base" || candidateLower == "abstract" || candidateLower == "parent" {
				continue
			}
			if isValidFunctionName(candidate) && !isQueryKeyword(candidateLower) {
				return candidate
			}
		}
	}

	return ""
}

// isQueryKeyword returns true if the word is a common query keyword that should
// not be extracted as a symbol name.
func isQueryKeyword(lower string) bool {
	switch lower {
	case "what", "which", "who", "how", "where", "when", "why",
		"classes", "class", "types", "type", "functions", "function",
		"methods", "method", "interfaces", "interface", "structs", "struct",
		"all", "any", "find", "show", "list", "get", "are", "is",
		"does", "do", "has", "have", "the", "a", "an", "in", "on",
		"from", "to", "with", "for", "base", "abstract", "parent",
		"this", "that", "these", "those":
		return true
	}
	return false
}

// isFileExtension returns true if the string is a common programming language file extension.
// Used to reject "Babylon.js", "Express.ts", etc. as Type.Method patterns.
func isFileExtension(s string) bool {
	// Only match if the extension is already lowercase — "js", "ts", "py", etc.
	// Uppercase like "JSON", "HTML" are valid method names (e.g., Context.JSON).
	if s != strings.ToLower(s) {
		return false
	}
	switch s {
	case "js", "ts", "jsx", "tsx", "py", "go", "rs", "rb", "java", "kt",
		"cs", "cpp", "cc", "c", "h", "hpp", "swift", "m", "mm",
		"css", "html", "htm", "xml", "json", "yaml", "yml", "toml",
		"md", "txt", "sh", "bash", "zsh", "sql", "proto", "wasm":
		return true
	}
	return false
}

// isSymbolKindKeyword returns true if the word is a programming construct keyword
// that typically follows a symbol name in queries (e.g., "TransformNode class",
// "Process function", "Engine struct").
//
// IT-04 Fix: Previously only "function"/"method"/"symbol" were recognized, causing
// Pattern 6 to miss "X class defined?" queries.
//
// IT-04 Audit: Comprehensive coverage across Go, Python, TypeScript, JavaScript:
//   - Go: struct, interface, type, func, method
//   - Python: class, decorator, module, function, method
//   - TypeScript/JavaScript: class, interface, enum, type, function, method,
//     prototype, constructor, component, property
//   - Cross-language: variable, constant, field, parameter, symbol
func isSymbolKindKeyword(word string) bool {
	switch word {
	// Core symbol kinds (all languages)
	case "function", "func", "method", "symbol",
		"class", "struct", "interface", "type", "enum",
		// JS/TS specific
		"prototype", "constructor", "object", "component",
		// Member-level
		"variable", "var", "constant", "const",
		"property", "field", "parameter",
		// Python specific
		"decorator", "module":
		return true
	}
	return false
}

// isFunctionLikeName checks if a name looks like a function (CamelCase, snake_case,
// contains digits, or starts with uppercase).
//
// IT-06: Removed `|| len(s) <= 15` which made ANY short word pass (e.g., "across",
// "entry", "route"). Now requires at least one structural signal: CamelCase, snake_case,
// digits, or starts with uppercase (PascalCase single-word names like "Entry", "Config").
func isFunctionLikeName(s string) bool {
	if len(s) == 0 {
		return false
	}
	// CamelCase: has uppercase in middle
	hasUpperInMiddle := false
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			hasUpperInMiddle = true
			break
		}
	}
	// snake_case or has digits
	hasUnderscore := strings.Contains(s, "_")
	hasDigit := strings.ContainsAny(s, "0123456789")

	// IT-06: PascalCase single-word names (starts with uppercase, e.g., "Entry", "Config",
	// "Series", "Flask") are strong symbol signals.
	startsWithUpper := s[0] >= 'A' && s[0] <= 'Z'

	return hasUpperInMiddle || hasUnderscore || hasDigit || startsWithUpper
}

// isStrictCamelCase returns true if the name has an uppercase letter after position 0
// (e.g., "canActivate", "runRenderLoop", "DataFrame"). Single-word all-lowercase names
// like "route", "handler", "mesh" return false.
//
// Description:
//
//	IT-05 R3-3: Used by Pattern 7 to distinguish symbol names from concept words in the
//	destination zone of "from X to Y" queries. CamelCase is a strong signal that a word
//	is a real symbol name (function, method, class) rather than a natural language concept
//	like "rendering", "execution", or "initialization".
//
// Inputs:
//   - s: The candidate word to check. Must not be empty.
//
// Outputs:
//   - bool: True if s contains an uppercase letter at position > 0.
//
// Thread Safety: This function is safe for concurrent use (stateless).
func isStrictCamelCase(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// extractTypeDotMethodFromQuery extracts "Type.Method" from natural language queries
// that mention a method on a type.
//
// Description:
//
//	Recognizes patterns like:
//	  "the Get method on the Transaction type" → "Transaction.Get"
//	  "the render method on Scene" → "Scene.render"
//	  "the __init__ method on the DataFrame class" → "DataFrame.__init__"
//	  "the create method on NestFactory" → "NestFactory.create"
//
// The pattern searched is: <method> method/function on (the) <type> (type|class|struct)?
//
// Inputs:
//
//   - query: The user's natural language query.
//
// Outputs:
//
//   - string: "Type.Method" dot notation, or "" if pattern not found.
//
// Limitations:
//
//   - Only matches when the query contains "method" or "function" keyword
//   - Does not handle "the X on Y" without method/function keyword
//   - Type name must start with uppercase letter (excludes Python lowercase module names)
//
// Assumptions:
//
//   - Query is in English
//   - Type names follow Go/JS/TS/Python naming conventions (uppercase first letter)
func extractTypeDotMethodFromQuery(query string) string {
	words := strings.Fields(query)
	lowerWords := make([]string, len(words))
	for i, w := range words {
		lowerWords[i] = strings.ToLower(w)
	}

	// Look for pattern: <method> method/function on [the] <Type> [type/class/struct]
	for i := 0; i < len(lowerWords)-1; i++ {
		if lowerWords[i+1] != "method" && lowerWords[i+1] != "function" {
			continue
		}

		methodName := strings.Trim(words[i], "?,.()")
		if methodName == "" || strings.ToLower(methodName) == "the" {
			continue
		}

		// Look for "on [the] <Type>" after "method/function"
		j := i + 2
		if j >= len(lowerWords) {
			continue
		}
		if lowerWords[j] != "on" {
			continue
		}
		j++
		if j >= len(lowerWords) {
			continue
		}

		// Skip optional "the"
		if lowerWords[j] == "the" {
			j++
			if j >= len(lowerWords) {
				continue
			}
		}

		typeName := strings.Trim(words[j], "?,.()")
		if typeName == "" {
			continue
		}

		// Validate the type name starts with uppercase (Go/JS/TS convention)
		// or is a valid identifier for Python (e.g., DataFrame)
		if isValidTypeName(typeName) {
			result := typeName + "." + methodName
			slog.Debug("extractTypeDotMethodFromQuery: matched",
				slog.String("query", query),
				slog.String("type", typeName),
				slog.String("method", methodName),
				slog.String("result", result),
			)
			return result
		}
	}

	slog.Debug("extractTypeDotMethodFromQuery: no match",
		slog.String("query", query),
	)
	return ""
}

// isValidTypeName checks if a string looks like a type/class name.
//
// Description:
//
//	Returns true if the name starts with an uppercase letter and contains
//	only alphanumeric characters. This matches Go types (Context, Transaction),
//	JS/TS classes (Scene, Router), and Python classes (DataFrame).
//
// Inputs:
//
//   - s: The candidate type name string.
//
// Outputs:
//
//   - bool: True if the string looks like a valid type/class name.
//
// Limitations:
//
//   - Rejects lowercase-first names, which excludes some Python module names
//   - Maximum 100 characters
//
// Assumptions:
//
//   - Type names follow Go/JS/TS/Python class naming conventions (PascalCase)
func isValidTypeName(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	// Must start with uppercase letter (using unicode for consistency with loop body)
	firstRune := rune(s[0])
	if !unicode.IsUpper(firstRune) {
		return false
	}
	// Must contain only alphanumeric characters
	for _, c := range s {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// extractFunctionNameFromContext tries to extract a function name from previous context.
//
// Description:
//
//	GR-Phase1: When the query doesn't contain an explicit function name,
//	look at previous tool results or conversation to find one.
//	For example, if find_entry_points was previously called, we can
//	extract "main" from its results.
//
// Inputs:
//
//	ctx - The assembled context with previous tool results.
//
// Outputs:
//
//	string - The extracted function name, or empty if not found.
func extractFunctionNameFromContext(ctx *agent.AssembledContext) string {
	if ctx == nil {
		return ""
	}

	// Check previous tool results for function names
	for _, result := range ctx.ToolResults {
		output := result.Output

		// Look for entry_points results which typically contain "main"
		if strings.Contains(output, "Entry Points") || strings.Contains(output, "main/main.go") {
			// Look for "main" specifically in entry points output
			if strings.Contains(output, "main") {
				return "main"
			}
		}

		// Extract function names from structured output
		if funcName := extractFunctionFromToolOutput(output); funcName != "" {
			return funcName
		}
	}

	// IT-06b Issue 2: ConversationHistory is NOT a valid source for symbol names.
	// It contains tool placeholder messages like "[Tool calls: Grep]" that pollute
	// symbol extraction (e.g., "Grep]" extracted as a valid symbol name).
	// Only ToolResults (checked above) should be used for context-based extraction.

	return ""
}

// extractFunctionFromToolOutput extracts a function name from tool output.
func extractFunctionFromToolOutput(output string) string {
	// Look for common patterns in tool output
	// Pattern: "function_name: X" or "Function: X"
	patterns := []string{"function:", "func ", "Function:", "name:"}
	lowerOutput := strings.ToLower(output)

	for _, pattern := range patterns {
		if idx := strings.Index(lowerOutput, pattern); idx >= 0 {
			after := output[idx+len(pattern):]
			words := strings.Fields(after)
			if len(words) > 0 {
				candidate := strings.Trim(words[0], "`,\"'")
				if isValidFunctionName(candidate) {
					return candidate
				}
			}
		}
	}

	return ""
}

// extractSearchPatternFromQuery extracts a search pattern for Grep tool.
//
// Description:
//
//	P0-2 (Feb 14, 2026): Extracts search pattern when LLM calls Grep without
//	explicit parameters. Looks for quoted strings, symbol names, or keywords.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	string - The extracted search pattern, or empty if not found.
func extractSearchPatternFromQuery(query string) string {
	// Pattern 1: Look for quoted strings (most explicit)
	// Examples: search for "Handler", find "Process"
	if idx := strings.Index(query, `"`); idx >= 0 {
		after := query[idx+1:]
		if endIdx := strings.Index(after, `"`); endIdx >= 0 {
			pattern := after[:endIdx]
			if len(pattern) > 0 {
				return pattern
			}
		}
	}

	// Pattern 2: "search for X", "find X", "look for X"
	searchPhrases := []string{"search for ", "find ", "look for ", "grep ", "locate "}
	lowerQuery := strings.ToLower(query)
	for _, phrase := range searchPhrases {
		if idx := strings.Index(lowerQuery, phrase); idx >= 0 {
			after := query[idx+len(phrase):]
			words := strings.Fields(after)
			if len(words) > 0 {
				candidate := strings.Trim(words[0], "?,.()")
				if len(candidate) > 0 {
					return candidate
				}
			}
		}
	}

	// Pattern 3: "where is X", "show X"
	wherePhrases := []string{"where is ", "show ", "display "}
	for _, phrase := range wherePhrases {
		if idx := strings.Index(lowerQuery, phrase); idx >= 0 {
			after := query[idx+len(phrase):]
			words := strings.Fields(after)
			if len(words) > 0 {
				candidate := strings.Trim(words[0], "?,.()")
				if len(candidate) > 0 && candidate != "me" && candidate != "the" {
					return candidate
				}
			}
		}
	}

	// Pattern 4: Extract capitalized words (likely symbol names)
	// Only use this if query contains keywords suggesting search intent
	if strings.Contains(lowerQuery, "search") || strings.Contains(lowerQuery, "find") ||
		strings.Contains(lowerQuery, "locate") || strings.Contains(lowerQuery, "grep") {
		words := strings.Fields(query)
		for _, word := range words {
			cleaned := strings.Trim(word, "?,.()'\"")
			if len(cleaned) > 0 && unicode.IsUpper(rune(cleaned[0])) {
				// Skip common capitalized non-symbols
				if cleaned != "I" && cleaned != "What" && cleaned != "Where" &&
					cleaned != "Show" && cleaned != "Find" && cleaned != "Search" {
					return cleaned
				}
			}
		}
	}

	return ""
}

// -----------------------------------------------------------------------------
// Parameter Extraction Helpers (GR-Phase1)
// -----------------------------------------------------------------------------

// maxTopNValue is the maximum allowed value for "top N" extraction.
// Values exceeding this return the default to prevent resource exhaustion.
const maxTopNValue = 100

// Pre-compiled regexes for parameter extraction (S-1 review finding).
// Using pre-compiled regexes avoids per-call compilation overhead.
var (
	// topNRegex matches "top N" patterns like "top 5", "TOP 10", "top 20".
	// Captures the numeric value in group 1.
	topNRegex = regexp.MustCompile(`(?i)\btop\s*(\d+)\b`)

	// numberRegex matches any standalone number (unused, kept for future use).
	numberRegex = regexp.MustCompile(`\b(\d+)\b`)

	// pathFromRegex matches "from X" patterns, optionally with quotes.
	// Examples: "from main", "from 'funcA'", "from \"funcB\"".
	pathFromRegex = regexp.MustCompile(`(?i)\bfrom\s+['"]?(\w+)['"]?`)

	// pathToRegex matches "to X" patterns, optionally with quotes.
	// Examples: "to parseConfig", "to 'funcB'".
	pathToRegex = regexp.MustCompile(`(?i)\bto\s+['"]?(\w+)['"]?`)
)

// extractTopNFromQuery extracts a "top N" value from queries like "top 5 hotspots".
//
// Description:
//
//	Parses patterns like "top 5", "top 10", "top 20 symbols" to extract N.
//	Returns the default value if no pattern is found or if N exceeds maxTopNValue.
//
// Inputs:
//
//   - query: The user's query string. Must not be nil.
//   - defaultVal: Default value if no "top N" pattern found.
//
// Outputs:
//
//   - int: The extracted value (1 <= N <= maxTopNValue) or defaultVal.
//
// Limitations:
//
//   - Only matches "top N" with space separator, not "top-N" with hyphen.
//   - Values > 100 return defaultVal to prevent resource exhaustion.
//
// Assumptions:
//
//   - Query is valid UTF-8 string.
func extractTopNFromQuery(query string, defaultVal int) int {
	// Pattern: "top N" with optional whitespace (case-insensitive)
	if matches := topNRegex.FindStringSubmatch(query); len(matches) > 1 {
		if n, err := strconv.Atoi(matches[1]); err == nil && n > 0 && n <= maxTopNValue {
			return n
		}
	}
	return defaultVal
}

// extractKindFromQuery extracts a symbol kind filter from the query.
//
// Description:
//
//	Looks for "functions", "types", "function", "type", "methods", "struct",
//	or "interface" keywords to determine the symbol kind filter.
//	Returns "all" if no specific kind is found.
//
// Inputs:
//
//   - query: The user's query string. Must not be nil.
//
// Outputs:
//
//   - string: One of "function", "type", or "all".
//
// Limitations:
//
//   - Uses simple substring matching; may false-positive on words like "functional".
//   - Does not distinguish between Go-specific kinds (struct vs interface).
//
// Assumptions:
//
//   - Query is valid UTF-8 string.
//   - "methods" maps to "function" kind for graph queries.
//   - "struct" and "interface" map to "type" kind for graph queries.
//
// extractKindFromQuery extracts a symbol kind filter from the user's query.
//
// IT-04 Audit: Added class, enum, variable, constant, decorator, method mappings.
// Previously only "function" and "type" were recognized, causing incorrect kind
// filters for Python/JS/TS class queries and Go enum queries.
func extractKindFromQuery(query string) string {
	lowerQuery := strings.ToLower(query)

	// Check for function-related keywords
	if strings.Contains(lowerQuery, "function") || strings.Contains(lowerQuery, "func ") ||
		strings.Contains(lowerQuery, "functions") {
		return "function"
	}

	// Check for method-related keywords (separate from function for precision)
	if strings.Contains(lowerQuery, " method") || strings.Contains(lowerQuery, "methods") {
		return "method"
	}

	// Check for class-related keywords (Python, JS, TS)
	if strings.Contains(lowerQuery, " class") || strings.Contains(lowerQuery, "classes") {
		return "class"
	}

	// Check for struct-related keywords (Go)
	if strings.Contains(lowerQuery, "struct") || strings.Contains(lowerQuery, "structs") {
		return "struct"
	}

	// Check for interface-related keywords
	if strings.Contains(lowerQuery, "interface") || strings.Contains(lowerQuery, "interfaces") {
		return "interface"
	}

	// Check for enum-related keywords (TS, Python)
	if strings.Contains(lowerQuery, " enum") || strings.Contains(lowerQuery, "enums") {
		return "enum"
	}

	// Check for type-related keywords (generic)
	if strings.Contains(lowerQuery, " type") || strings.Contains(lowerQuery, "types") {
		return "type"
	}

	// Check for variable/constant keywords
	if strings.Contains(lowerQuery, "variable") || strings.Contains(lowerQuery, "variables") ||
		strings.Contains(lowerQuery, " var ") {
		return "variable"
	}
	if strings.Contains(lowerQuery, "constant") || strings.Contains(lowerQuery, "constants") ||
		strings.Contains(lowerQuery, " const ") {
		return "constant"
	}

	return "all"
}

// extractPathSymbolsFromQuery extracts "from" and "to" symbols for find_path.
//
// Description:
//
//	Parses patterns like "path from main to parseConfig",
//	"how does funcA connect to funcB", or "between X and Y".
//	Uses three extraction strategies in order of reliability:
//	  1. Explicit "from X to Y" patterns
//	  2. "between X and Y" patterns
//	  3. CamelCase/snake_case function name fallback (only if one symbol found)
//
// Inputs:
//
//   - query: The user's query string. Must not be nil.
//
// Outputs:
//
//   - from: The source symbol name, or empty string if not found.
//   - to: The target symbol name, or empty string if not found.
//   - ok: True if BOTH symbols were found.
//
// Limitations:
//
//   - Fallback pattern only activates if one symbol is already found.
//   - Common words are filtered via isValidFunctionName to reduce false positives.
//   - Quoted symbols are extracted but quotes are stripped.
//
// Assumptions:
//
//   - Symbol names follow Go naming conventions (CamelCase or snake_case).
//   - Query is valid UTF-8 string.
func extractPathSymbolsFromQuery(query string) (from, to string, ok bool) {
	// Pattern 1: "from X to Y"
	// IT-06: Use isValidFunctionName only (not isFunctionLikeName) because
	// "from X to Y" provides strong context — the words after "from"/"to" are
	// symbol names even if lowercase (e.g., "main", "init").
	if fromMatches := pathFromRegex.FindStringSubmatch(query); len(fromMatches) > 1 {
		candidate := fromMatches[1]
		if isValidFunctionName(candidate) {
			from = candidate
		}
	}
	if toMatches := pathToRegex.FindStringSubmatch(query); len(toMatches) > 1 {
		candidate := toMatches[1]
		if isValidFunctionName(candidate) {
			to = candidate
		}
	}

	// Pattern 2: "path between X and Y"
	if from == "" || to == "" {
		lowerQuery := strings.ToLower(query)
		if idx := strings.Index(lowerQuery, "between "); idx >= 0 {
			after := query[idx+8:]
			if andIdx := strings.Index(strings.ToLower(after), " and "); andIdx >= 0 {
				fromPart := strings.TrimSpace(after[:andIdx])
				toPart := strings.TrimSpace(after[andIdx+5:])

				// Extract function names
				fromWords := strings.Fields(fromPart)
				toWords := strings.Fields(toPart)

				if len(fromWords) > 0 && from == "" {
					candidate := strings.Trim(fromWords[len(fromWords)-1], "?,.()")
					if isValidFunctionName(candidate) && isFunctionLikeName(candidate) {
						from = candidate
					}
				}
				if len(toWords) > 0 && to == "" {
					candidate := strings.Trim(toWords[0], "?,.()")
					if isValidFunctionName(candidate) && isFunctionLikeName(candidate) {
						to = candidate
					}
				}
			}
		}
	}

	// Pattern 3: Look for CamelCase or snake_case function names in the query
	// Only use this as a fallback if we have at least one symbol from patterns above
	// This prevents false positives from common words
	if (from != "" && to == "") || (from == "" && to != "") {
		words := strings.Fields(query)
		for _, word := range words {
			candidate := strings.Trim(word, "?,.()'\"")
			if isValidFunctionName(candidate) && isFunctionLikeName(candidate) {
				// Skip if it's the same as what we already have
				if candidate == from || candidate == to {
					continue
				}
				if from == "" {
					from = candidate
				} else if to == "" {
					to = candidate
				}
				if from != "" && to != "" {
					break
				}
			}
		}
	}

	return from, to, from != "" && to != ""
}

// -----------------------------------------------------------------------------
// Tool Name Utilities
// -----------------------------------------------------------------------------

// getAvailableToolNames extracts tool names from tool definitions.
//
// Inputs:
//
//	toolDefs - Tool definitions.
//
// Outputs:
//
//	[]string - List of tool names.
func getAvailableToolNames(toolDefs []tools.ToolDefinition) []string {
	names := make([]string, len(toolDefs))
	for i, def := range toolDefs {
		names[i] = def.Name
	}
	return names
}

// -----------------------------------------------------------------------------
// Semantic Validation
// -----------------------------------------------------------------------------

// ValidateToolQuerySemantics checks if the selected tool matches the query semantics.
//
// Description:
//
//	GR-Phase1: Post-router validation to catch obvious semantic mismatches.
//	Specifically designed to detect find_callers vs find_callees confusion.
//
// Inputs:
//
//	query - The user's query string.
//	selectedTool - The tool selected by the router.
//
// Outputs:
//
//	correctedTool - The validated/corrected tool name.
//	wasChanged - True if the tool was changed from the original selection.
//	reason - Explanation if the tool was changed.
func ValidateToolQuerySemantics(query, selectedTool string) (correctedTool string, wasChanged bool, reason string) {
	lowerQuery := strings.ToLower(query)

	// IT-05 R1: Detect call chain queries misrouted to find_callers or find_callees.
	// "Show the call chain from X" should always use get_call_chain.
	callChainPatterns := []string{
		"call chain",
		"call graph",
		"call hierarchy",
		"call tree",
		"transitive call",
		"recursive call",
		"full call",
	}
	if selectedTool == "find_callers" || selectedTool == "find_callees" {
		for _, pattern := range callChainPatterns {
			if strings.Contains(lowerQuery, pattern) {
				return "get_call_chain", true, "Query contains '" + pattern + "' which indicates get_call_chain, not " + selectedTool
			}
		}
	}

	// Pattern detection for callers vs callees confusion
	// Callees patterns: "what does X call", "what functions does X call", "what X calls"
	// Callers patterns: "who calls X", "what calls X", "callers of X"

	// Strong callees indicators (asking what a function calls, not who calls it)
	calleesPatterns := []string{
		"what does",      // "what does main call"
		"what functions", // "what functions does main call"
		"functions that", // "functions that main calls"
		"called by main", // "functions called by main" (main is the caller)
	}

	// Strong callers indicators (asking who/what calls a function)
	callersPatterns := []string{
		"who calls",     // "who calls parseConfig"
		"what calls",    // "what calls parseConfig"
		"callers of",    // "callers of parseConfig"
		"usages of",     // "usages of parseConfig"
		"uses of",       // "uses of parseConfig"
		"references to", // "references to parseConfig"
	}

	// Check for find_callers mismatch (should be find_callees)
	if selectedTool == "find_callers" {
		for _, pattern := range calleesPatterns {
			if strings.Contains(lowerQuery, pattern) {
				// Special case: "called by X" where X is the target means callers of X
				// But "functions called by X" where X is a function means callees of X
				if pattern == "called by main" {
					// Check if query is about a specific function being the caller
					// e.g., "what functions are called by main" → callees
					if strings.Contains(lowerQuery, "functions") ||
						strings.Contains(lowerQuery, "what is") ||
						strings.Contains(lowerQuery, "what are") {
						return "find_callees", true, "Query asks 'functions called BY X' which means callees (downstream), not callers"
					}
				} else {
					return "find_callees", true, "Query pattern '" + pattern + "' indicates callees (what X calls), not callers (who calls X)"
				}
			}
		}
	}

	// Check for find_callees mismatch (should be find_callers)
	if selectedTool == "find_callees" {
		for _, pattern := range callersPatterns {
			if strings.Contains(lowerQuery, pattern) {
				return "find_callers", true, "Query pattern '" + pattern + "' indicates callers (who calls X), not callees (what X calls)"
			}
		}
	}

	// No mismatch detected
	return selectedTool, false, ""
}

// hasSemanticCorrectionForQuery checks if a semantic correction has already been
// applied for the given query in this session.
//
// Description:
//
//	GR-Phase1: Prevents duplicate semantic correction warnings when Execute()
//	is called multiple times for the same query (e.g., after hard-forced tool
//	execution returns StateExecute).
//
// Inputs:
//
//	session - The agent session containing trace steps.
//	query - The user's query string.
//	correctedTool - The tool that was corrected to.
//
// Outputs:
//
//	bool - True if a semantic correction was already recorded for this query.
func hasSemanticCorrectionForQuery(session *agent.Session, query, correctedTool string) bool {
	if session == nil {
		return false
	}

	steps := session.GetTraceSteps()

	// GR-Phase1 Debug: Log what we're checking
	semanticCount := 0
	for _, s := range steps {
		if s.Action == "semantic_correction" {
			semanticCount++
		}
	}

	queryPreview := query
	if len(queryPreview) > 100 {
		queryPreview = queryPreview[:100]
	}

	if semanticCount > 0 || len(steps) > 5 {
		slog.Debug("GR-Phase1: hasSemanticCorrectionForQuery called",
			slog.Int("steps", len(steps)),
			slog.Int("semantic_corrections", semanticCount),
			slog.String("looking_for", correctedTool),
			slog.String("query_prefix", queryPreview[:min(30, len(queryPreview))]),
		)
	}

	if len(steps) == 0 {
		return false
	}

	for _, step := range steps {
		if step.Action != "semantic_correction" {
			continue
		}
		if step.Target != correctedTool {
			continue
		}

		// Check if this correction was for the same query
		// Use looser matching to handle truncation differences
		stepQuery, ok := step.Metadata["query_preview"]
		if !ok {
			// If no query recorded, consider it a match for safety
			// (older correction, same tool)
			slog.Debug("GR-Phase1: found match (no query metadata)",
				slog.String("target", step.Target),
			)
			return true
		}

		// Match if queries share a significant prefix (first 50 chars)
		minLen := 50
		if len(queryPreview) < minLen {
			minLen = len(queryPreview)
		}
		if len(stepQuery) < minLen {
			minLen = len(stepQuery)
		}
		if minLen > 0 && queryPreview[:minLen] == stepQuery[:minLen] {
			slog.Debug("GR-Phase1: found match (prefix)",
				slog.String("target", step.Target),
			)
			return true
		}

		// Also match if one is a prefix of the other
		if strings.HasPrefix(stepQuery, queryPreview) || strings.HasPrefix(queryPreview, stepQuery) {
			slog.Debug("GR-Phase1: found match (hasPrefix)",
				slog.String("target", step.Target),
			)
			return true
		}

		slog.Debug("GR-Phase1: near miss in semantic correction check",
			slog.String("action", step.Action),
			slog.String("target", step.Target),
		)
	}

	return false
}

// parseInt attempts to parse a string as an integer.
//
// Description:
//
//	Wrapper around strconv.Atoi that returns 0 on error.
//	Used by parameter extraction logic to parse numeric values from queries.
//
// Inputs:
//
//	s - The string to parse.
//
// Outputs:
//
//	int - The parsed integer, or 0 if parsing fails.
//
// Thread Safety: Safe for concurrent use.
func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
