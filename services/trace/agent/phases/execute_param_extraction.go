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

// execute_param_extraction.go contains functions that extract typed parameters
// from natural-language queries. Extracted from execute_helpers.go as part of
// D3a decomposition.

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

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
// symbols share the same name across different packages, and to scope
// find_hotspots results to a specific subsystem.
//
// Description:
//
//	IT-06c Bug C: Queries like "What functions does the Build method call in hugolib?"
//	contain package context ("in hugolib") that should be used to disambiguate
//	when multiple symbols match "Build". This function extracts such hints.
//
//	IT-07 Phase 2: Extended to handle subsystem-scoped queries for find_hotspots:
//	"hotspots in the binding subsystem", "hotspots within the materials subsystem".
//
//	Recognized patterns:
//	  - "in/within <package>" (most common): "in hugolib", "within flask"
//	  - "in/within the <package> <scope_kw>": "in the binding subsystem"
//	  - "<package> package/module/subsystem/...": "the hugolib package"
//	  - "in/within the <path>" (file paths): "within the lib/router directory"
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
	// IT-08d: Keep original-case words for capitalization heuristic.
	// Capitalized words (Express, Flask, Pandas) are likely project names,
	// not package/module names. Lowercase words (gin, flask, hugolib) are valid.
	originalWords := strings.Fields(query)

	// Prepositions that introduce a package/scope context.
	isPrep := func(w string) bool {
		return w == "in" || w == "within"
	}

	// Articles to skip after prepositions.
	articles := map[string]bool{
		"the": true, "a": true, "an": true, "this": true, "that": true,
	}

	// Words that are generic location context, not package names.
	// Note: "system" and "code" appear here AND in scopeKeywords. This is intentional:
	// in skipGeneric they prevent "in system" or "in code" from matching as package names,
	// in scopeKeywords they act as scope confirmers for a preceding word ("auth system").
	skipGeneric := map[string]bool{
		"codebase": true, "code": true, "graph": true, "project": true,
		"repo": true, "repository": true, "source": true, "system": true,
		// IT-08: Dead code adjectives — "dead code", "unused code", "orphan code"
		// must not match as package names in any pattern.
		"dead": true, "unused": true, "orphan": true, "unreferenced": true,
	}

	// IT-07 Phase 2: Keywords that confirm the preceding word is a scope/package name.
	scopeKeywords := map[string]bool{
		"package": true, "module": true, "library": true, "lib": true,
		"subsystem": true, "directory": true, "dir": true,
		"components": true, "component": true, "pipeline": true,
		"system": true, "path": true, "code": true,
		// IT-08: "class" is a scope keyword for queries like "in the Engine class".
		"class": true,
	}

	// Pattern 1: "in/within <package>" — direct package name after preposition.
	// Match the LAST occurrence, scanning right to left.
	// Skip articles and generic words.
	for i := len(words) - 2; i >= 0; i-- {
		if !isPrep(words[i]) {
			continue
		}
		candidate := strings.Trim(words[i+1], "?,.()")
		if candidate == "" || articles[candidate] || skipGeneric[candidate] {
			continue
		}
		// Check for file path pattern: "in lib/router" or "within src/plots"
		if strings.Contains(candidate, "/") {
			return candidate
		}
		// IT-08d: Skip capitalized candidates — likely project names (Express, Flask).
		// Lowercase package names (gin, flask, hugolib) pass through.
		// IT-R2c Fix C: Exception — if the capitalized word is immediately followed by
		// a scope keyword ("in Engine class", "in Node hierarchy"), use the capitalized
		// word lowercased as the package. This handles class-scoped queries.
		if i+1 < len(originalWords) {
			origCandidate := strings.Trim(originalWords[i+1], "?,.()")
			if len(origCandidate) > 0 && unicode.IsUpper(rune(origCandidate[0])) {
				// Check if next word after capitalized is a scope keyword
				if i+2 < len(words) {
					nextNext := strings.Trim(words[i+2], "?,.()")
					if scopeKeywords[nextNext] {
						// "in Engine class" → "engine"
						return strings.ToLower(origCandidate)
					}
				}
				continue
			}
		}
		// Must look like a package name: lowercase, alphanumeric, no spaces
		if isPackageLikeName(candidate) {
			return candidate
		}
	}

	// Pattern 1.5 (IT-43c): "in/within <Capitalized> <word>... <scope_keyword>"
	// When Pattern 1 skips a capitalized word (project name) and there are more
	// words after it ending with a scope keyword, extract the first package-like
	// word after the project name.
	// Example: "in Pandas indexing and selection code" → "indexing"
	for i := 0; i < len(words)-2; i++ {
		if !isPrep(words[i]) {
			continue
		}
		// Check if next word is capitalized (project name)
		if i+1 >= len(originalWords) {
			continue
		}
		origNext := strings.Trim(originalWords[i+1], "?,.()")
		if len(origNext) == 0 || !unicode.IsUpper(rune(origNext[0])) {
			continue
		}
		nextLower := strings.Trim(words[i+1], "?,.()")
		if articles[nextLower] || skipGeneric[nextLower] {
			continue
		}
		// Scan forward from i+2 to find a scope keyword
		for j := i + 3; j < len(words); j++ {
			scopeWord := strings.Trim(words[j], "?,.()")
			if scopeKeywords[scopeWord] {
				// Take the first package-like word between project name and scope keyword
				for k := i + 2; k < j; k++ {
					candidate := strings.Trim(words[k], "?,.()")
					if isPackageLikeName(candidate) && !skipGeneric[candidate] {
						return candidate
					}
				}
				break
			}
		}
	}

	// Pattern 2: "in/within the <package> [<extra_words>...] <scope_keyword>"
	// Article-mediated. Handles single-word ("in the render package") and
	// multi-word ("in the value log subsystem", "in the Engine class").
	//
	// IT-08: Scan forward from the word after the article to find the scope
	// keyword. Return the FIRST package-like word after the article, not the
	// last — the first word in a multi-word subsystem description is typically
	// the actual domain name (value, math, blueprint), while subsequent words
	// are qualifiers (log, utilities, registration).
	for i := 0; i < len(words)-2; i++ {
		if !isPrep(words[i]) {
			continue
		}
		next := strings.Trim(words[i+1], "?,.()")
		if !articles[next] {
			continue
		}
		// Scan forward from i+3 to find the scope keyword.
		for j := i + 3; j < len(words); j++ {
			scopeWord := strings.Trim(words[j], "?,.()")
			if scopeKeywords[scopeWord] {
				// IT-08d/IT-43c: When multiple words between article and scope keyword,
				// check if first word is capitalized (project name).
				// "in the Flask helpers module" → "Flask" capitalized → first lowercase = "helpers"
				// "in the Pandas indexing and selection code" → "Pandas" capitalized → first lowercase = "indexing"
				// "in the value log subsystem" → "value" lowercase → use words[i+2]="value"
				if j > i+3 && i+2 < len(originalWords) {
					origFirst := strings.Trim(originalWords[i+2], "?,.()")
					if len(origFirst) > 0 && unicode.IsUpper(rune(origFirst[0])) {
						// IT-43c: First word is a proper noun (project name like "Pandas", "Flask").
						// Find the first lowercase, package-like word after it instead of
						// taking the word before the scope keyword.
						// "in the Pandas indexing and selection code" → "indexing" (not "selection")
						// "in the Flask helpers module" → "helpers"
						for k := i + 3; k < j; k++ {
							candidate := strings.Trim(words[k], "?,.()")
							if isPackageLikeName(candidate) && !skipGeneric[candidate] {
								return candidate
							}
						}
						// Fallback: word before scope keyword (original behavior)
						moduleCandidate := strings.Trim(words[j-1], "?,.()")
						if isPackageLikeName(moduleCandidate) && !skipGeneric[moduleCandidate] {
							return moduleCandidate
						}
					}
				}
				// Default: use first package-like word after article.
				pkgCandidate := strings.Trim(words[i+2], "?,.()")
				if isPackageLikeName(pkgCandidate) && !skipGeneric[pkgCandidate] {
					return pkgCandidate
				}
				break
			}
		}
		// Also check: "in the lib/router directory" — path after article.
		pathCandidate := strings.Trim(words[i+2], "?,.()")
		if strings.Contains(pathCandidate, "/") {
			return pathCandidate
		}
	}

	// Pattern 3: "<package> package/module/subsystem/..." — "the hugolib package"
	for i := 0; i < len(words)-1; i++ {
		nextWord := strings.Trim(words[i+1], "?,.()")
		if scopeKeywords[nextWord] {
			candidate := strings.Trim(words[i], "?,.()")
			if articles[candidate] {
				continue
			}
			if isPackageLikeName(candidate) && !skipGeneric[candidate] {
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

	// Pattern 1b (IT-06c H-4): "Where is X used/referenced/defined"
	// Handles find_references queries like:
	//   "Where is the request proxy used across the codebase?" → "request"
	//   "Where is the db session used?" → "db"
	//   "Where is Config referenced?" → "Config"
	// The word immediately after "is the" (skipping articles) that passes
	// isValidFunctionName is the symbol name. "used", "referenced", "defined"
	// are in skipWords so they won't be extracted.
	if strings.Contains(lowerQuery, "where is") {
		words := strings.Fields(query)
		for i, word := range words {
			lw := strings.ToLower(word)
			if lw == "is" && i > 0 && strings.ToLower(words[i-1]) == "where" {
				// Skip articles after "is"
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
				break
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
		// Programming construct nouns (mostly aligned with genericWords in tool_helpers.go)
		"classes", "class", "interface", "interfaces", "structs", "struct",
		"base", "abstract", "derive", "derives", "inherit", "inherits",
		"type", "types", "enum", "enums",
		// IT-R2b: "constructor"/"constructors" removed from genericWords (tool_helpers.go)
		// but kept here — bare "constructor" in NL queries ("from constructor to X") is
		// genuinely ambiguous. The LLM/param-extractor path uses ValidateSymbolName
		// (which no longer blocks it), so qualified "Scene.constructor" still works.
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
	// IT-R2b Fix 1: Changed \w+ to [\w.]+ to capture dot-notation names.
	// "from Engine.runRenderLoop" → captures "Engine.runRenderLoop" (not just "Engine").
	// The dot is essential for Type.Method resolution downstream.
	pathFromRegex = regexp.MustCompile(`(?i)\bfrom\s+['"]?([\w.]+)['"]?`)

	// pathToRegex matches "to X" patterns, optionally with quotes.
	// IT-R2b Fix 1: Changed \w+ to [\w.]+ to capture dot-notation names.
	pathToRegex = regexp.MustCompile(`(?i)\bto\s+['"]?([\w.]+)['"]?`)

	// IT-R2c Fix D: Multi-word phrase regexes for conceptual resolution.
	// Captures up to 4 words after "from"/"to" to pass to conceptual resolution.
	// "from memtable flush to disk persistence" → from_phrase="memtable flush", to_phrase="disk persistence"
	// The single-word regex above extracts the primary symbol name; these capture
	// the full conceptual phrase for richer context in resolveConceptualName().
	pathFromPhraseRegex = regexp.MustCompile(`(?i)\bfrom\s+['"]?([\w.]+(?:\s+[\w.]+){0,3})['"]?\s+to\b`)
	pathToPhraseRegex   = regexp.MustCompile(`(?i)\bto\s+['"]?([\w.]+(?:\s+[\w.]+){0,3})['"]?(?:\s*[?.!]?\s*$)`)
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

// extractSortByFromQuery detects sort dimension hints in find_hotspots queries.
//
// IT-07 Phase 3: Queries like "highest fan-in" or "highest fan-out" imply
// dimension-specific sorting rather than composite score ranking.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	string - "in" for fan-in/InDegree queries, "out" for fan-out/OutDegree queries,
//	         "score" (default) for composite score.
//
// Thread Safety: Safe for concurrent use (stateless function).
func extractSortByFromQuery(query string) string {
	lowerQuery := strings.ToLower(query)
	if strings.Contains(lowerQuery, "fan-out") || strings.Contains(lowerQuery, "fanout") ||
		strings.Contains(lowerQuery, "outgoing") || strings.Contains(lowerQuery, "outdegree") {
		return "out"
	}
	if strings.Contains(lowerQuery, "fan-in") || strings.Contains(lowerQuery, "fanin") ||
		strings.Contains(lowerQuery, "incoming") || strings.Contains(lowerQuery, "indegree") ||
		strings.Contains(lowerQuery, "most called") || strings.Contains(lowerQuery, "called by") {
		return "in"
	}
	return "score"
}

// extractExcludeTestsFromQuery determines whether to include test files in results.
//
// IT-07 Phase 3: By default, test files are excluded. Only include them when
// the query explicitly asks about test code.
//
// Inputs:
//
//	query - The user's query string.
//
// Outputs:
//
//	bool - true to exclude test files (default), false to include them.
//
// Thread Safety: Safe for concurrent use (stateless function).
func extractExcludeTestsFromQuery(query string) bool {
	lowerQuery := strings.ToLower(query)
	// If the user explicitly mentions test code, include test files.
	if strings.Contains(lowerQuery, "test file") || strings.Contains(lowerQuery, "test code") ||
		strings.Contains(lowerQuery, "in tests") || strings.Contains(lowerQuery, "including tests") ||
		strings.Contains(lowerQuery, "test functions") {
		return false
	}
	return true
}

// extractReverseFromQuery detects if the user wants reverse-sorted results.
//
// Description:
//
//	Returns true when the query asks for lowest-ranked, peripheral, or least
//	important symbols. IT-R2c Fix E: Enables find_important to return ascending
//	results for "lowest PageRank" / "peripheral functions" queries.
//
// Inputs:
//
//   - query: The user's query string.
//
// Outputs:
//
//   - bool: true if the query implies reverse (ascending) sort order.
func extractReverseFromQuery(query string) bool {
	lowerQuery := strings.ToLower(query)
	reverseIndicators := []string{
		"lowest pagerank",
		"least important",
		"peripheral",
		"least connected",
		"least significant",
		"bottom",
		"least central",
		"least influential",
	}
	for _, indicator := range reverseIndicators {
		if strings.Contains(lowerQuery, indicator) {
			return true
		}
	}
	return false
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
		// IT-R2b: Trim trailing dots — [\w.]+ can capture "Engine." at sentence boundaries.
		candidate := strings.TrimRight(fromMatches[1], ".")
		if isValidFunctionName(candidate) {
			from = candidate
		}
	}
	if toMatches := pathToRegex.FindStringSubmatch(query); len(toMatches) > 1 {
		candidate := strings.TrimRight(toMatches[1], ".")
		if isValidFunctionName(candidate) {
			to = candidate
		}
	}

	// IT-R2c Fix D: Extract multi-word conceptual phrases for richer resolution.
	// When the user writes "from memtable flush to disk persistence", the single-word
	// regex captures from="memtable" and to="disk". But conceptual resolution works
	// better with the full phrase "memtable flush" / "disk persistence" because the
	// extra words provide domain context for synonym expansion.
	// Override single-word extraction with phrase when the phrase contains multiple words.
	if fromPhraseMatches := pathFromPhraseRegex.FindStringSubmatch(query); len(fromPhraseMatches) > 1 {
		phrase := strings.TrimSpace(fromPhraseMatches[1])
		words := strings.Fields(phrase)
		if len(words) > 1 {
			// Multi-word phrase — use full phrase for conceptual resolution
			from = phrase
		}
	}
	if toPhraseMatches := pathToPhraseRegex.FindStringSubmatch(query); len(toPhraseMatches) > 1 {
		phrase := strings.TrimSpace(toPhraseMatches[1])
		words := strings.Fields(phrase)
		if len(words) > 1 {
			// Multi-word phrase — use full phrase for conceptual resolution
			to = phrase
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

// validateExtractedPathParams detects when the param extractor produced a
// destination symbol that is suspicious and should be re-derived from the query.
//
// Description:
//
//	After extracting from/to params for find_path or get_call_chain, this
//	function checks three rules that catch common param extractor failures:
//
//	  Rule 1 — Destination-Source Overlap: "to" is a substring of "from",
//	    meaning the extractor confused the source name for the destination.
//	    Example: from="Context.Bind", to="Bind" → Bind is part of Context.Bind.
//
//	  Rule 2 — Same Symbol: normalized from == normalized to. The extractor
//	    produced the same symbol for both endpoints.
//
//	  Rule 3 — Short Generic Name: "to" is very short (< 4 chars) and appears
//	    inside a longer conceptual phrase in the query. The extractor latched
//	    onto a fragment instead of the full concept.
//
//	When a rule fires, the function attempts to re-derive "to" from the query
//	using the phrase after the last "to" preposition, producing a multi-word
//	conceptual name that conceptual resolution can tokenize and expand.
//
// Inputs:
//
//   - from: Extracted source symbol name.
//   - to: Extracted destination symbol name.
//   - query: Original user query string.
//
// Outputs:
//
//   - string: The validated (possibly re-derived) "from" value.
//   - string: The validated (possibly re-derived) "to" value.
//   - bool: True if any value was modified.
//
// Limitations:
//
//   - Only handles "from X to Y" query patterns. Other patterns (e.g., "between
//     X and Y") are not re-derived because the phrase extraction is different.
//   - Re-derivation uses simple regex; may produce imperfect multi-word phrases.
func validateExtractedPathParams(from, to, query string) (string, string, bool) {
	if from == "" || to == "" {
		return from, to, false
	}

	changed := false
	lowerFrom := strings.ToLower(from)
	lowerTo := strings.ToLower(to)

	// Rule 1: Destination is a substring of source (case-insensitive).
	// Example: from="Context.Bind", to="Bind" → "bind" is in "context.bind"
	rule1 := strings.Contains(lowerFrom, lowerTo)

	// Rule 2: Same symbol after normalization (strip dots, lowercase).
	// Example: from="Context.Bind", to="Context.Bind"
	normalFrom := strings.ReplaceAll(lowerFrom, ".", "")
	normalTo := strings.ReplaceAll(lowerTo, ".", "")
	rule2 := normalFrom == normalTo

	// Rule 3: "to" is very short and appears inside a longer phrase in the query.
	// Example: query has "to data validation", extractor picked to="data"
	rule3 := len(to) < 4 && !strings.Contains(to, ".")

	if rule1 || rule2 || rule3 {
		reason := "unknown"
		if rule1 {
			reason = "destination_substring_of_source"
		} else if rule2 {
			reason = "same_symbol"
		} else {
			reason = "short_generic_name"
		}

		// Attempt to re-derive "to" from the query phrase after "to" preposition.
		reDerived := reDeriveDestinationFromQuery(query, from)
		if reDerived != "" && strings.ToLower(reDerived) != lowerTo {
			slog.Info("D3b: param validation override on 'to'",
				slog.String("rule", reason),
				slog.String("original_to", to),
				slog.String("re_derived_to", reDerived),
				slog.String("from", from),
			)
			to = reDerived
			changed = true
		} else {
			slog.Debug("D3b: param validation rule fired but re-derivation unchanged",
				slog.String("rule", reason),
				slog.String("to", to),
				slog.String("from", from),
			)
		}
	}

	return from, to, changed
}

// reDeriveDestinationFromQuery extracts the conceptual destination phrase from
// the query text by finding the last "to" preposition and taking the words
// that follow it (up to 4 words, excluding the source symbol).
//
// Description:
//
//	Given a query like "Show the call chain from Context.Bind to data validation",
//	this function finds "to data validation" and returns "data validation" as the
//	conceptual phrase for resolution.
//
// Inputs:
//
//   - query: The original user query.
//   - from: The source symbol (used to avoid capturing it as destination).
//
// Outputs:
//
//   - string: The re-derived destination phrase, or "" if extraction failed.
func reDeriveDestinationFromQuery(query, from string) string {
	lowerQuery := strings.ToLower(query)

	// Find the last occurrence of " to " in the query.
	// Using last occurrence because "from X to Y" puts "to" after "from".
	lastToIdx := strings.LastIndex(lowerQuery, " to ")
	if lastToIdx < 0 {
		return ""
	}

	// Extract everything after " to ".
	afterTo := strings.TrimSpace(query[lastToIdx+4:])
	if afterTo == "" {
		return ""
	}

	// Take up to 4 words, stopping at sentence-ending punctuation.
	words := strings.Fields(afterTo)
	var phrase []string
	for i, w := range words {
		if i >= 4 {
			break
		}
		// Stop at sentence-ending punctuation.
		clean := strings.TrimRight(w, "?!.;,")
		if clean == "" {
			break
		}
		// Skip if the word is the source symbol or part of it.
		if strings.EqualFold(clean, from) || strings.EqualFold(clean, strings.TrimPrefix(strings.ToLower(from), strings.ToLower(strings.SplitN(from, ".", 2)[0])+".")) {
			continue
		}
		phrase = append(phrase, clean)
		// If we trimmed punctuation, this was the last word.
		if len(clean) < len(w) {
			break
		}
	}

	if len(phrase) == 0 {
		return ""
	}

	// Join as camelCase if multiple words (for conceptual resolution tokenization).
	// "data validation" → "dataValidation"
	if len(phrase) > 1 {
		var camelParts []string
		for i, p := range phrase {
			if i == 0 {
				camelParts = append(camelParts, strings.ToLower(p))
			} else {
				if len(p) > 0 {
					camelParts = append(camelParts, strings.ToUpper(p[:1])+strings.ToLower(p[1:]))
				}
			}
		}
		return strings.Join(camelParts, "")
	}

	return phrase[0]
}

// extractFilePathFromQuery extracts a file path from a user query.
//
// Description:
//
//	Looks for patterns like quoted paths, paths with extensions, or
//	paths following keywords like "file", "in", "of", "from".
//	Returns the extracted path or empty string if none found.
//
// Inputs:
//
//   - query: The raw user query string.
//
// Outputs:
//
//   - string: The extracted file path, or "" if no path found.
//
// Limitations:
//
//   - Does not validate the path exists on disk.
//   - May not handle all path formats (e.g., Windows backslashes).
//
// Assumptions:
//
//   - Paths use forward slashes and have file extensions.
func extractFilePathFromQuery(query string) string {
	// Pattern 1: Quoted path — "src/main.go" or 'src/main.go'
	quotedPathRegex := regexp.MustCompile(`['"]([^'"]+\.[a-zA-Z]{1,10})['"]`)
	if m := quotedPathRegex.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}

	// Pattern 2: Backtick-quoted path — `src/main.go`
	backtickPathRegex := regexp.MustCompile("`([^`]+\\.[a-zA-Z]{1,10})`")
	if m := backtickPathRegex.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}

	// Pattern 3: Path with directory separator and extension — src/main.go, pkg/config/config.go
	slashPathRegex := regexp.MustCompile(`\b([\w./-]+/[\w.-]+\.[a-zA-Z]{1,10})\b`)
	if m := slashPathRegex.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}

	// Pattern 4: Simple filename with extension after keywords — "file main.go", "in utils.py"
	keywordFileRegex := regexp.MustCompile(`(?i)(?:file|in|of|from)\s+([\w.-]+\.[a-zA-Z]{1,10})\b`)
	if m := keywordFileRegex.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}

	// Pattern 5: Bare filename with common extension at word boundary
	bareFileRegex := regexp.MustCompile(`\b([\w.-]+\.(?:go|py|js|ts|tsx|jsx|rs|java|rb|c|h|cpp|cc|hpp|cs|yaml|yml|json|md|sh))\b`)
	if m := bareFileRegex.FindStringSubmatch(query); len(m) > 1 {
		return m[1]
	}

	return ""
}

// extractLineRangeFromQuery extracts start and end line numbers from a user query.
//
// Description:
//
//	Looks for patterns like "lines 100-200", "line 50 to 100", "lines 100 through 200",
//	or "line 42". Returns defaults if no match found.
//
// Inputs:
//
//   - query: The raw user query string.
//   - defaultStart: Default start line if none found (typically 1).
//   - defaultEnd: Default end line if none found (typically 200).
//
// Outputs:
//
//   - start: The extracted or default start line.
//   - end: The extracted or default end line.
//
// Limitations:
//
//   - Does not validate lines against actual file length.
//
// Assumptions:
//
//   - Line numbers are positive integers.
func extractLineRangeFromQuery(query string, defaultStart, defaultEnd int) (start, end int) {
	start = defaultStart
	end = defaultEnd

	// Pattern 1: "lines 100-200", "lines 100 to 200", "lines 100 through 200"
	rangeRegex := regexp.MustCompile(`(?i)lines?\s+(\d+)\s*[-–—]\s*(\d+)`)
	if m := rangeRegex.FindStringSubmatch(query); len(m) > 2 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			start = s
		}
		if e, err := strconv.Atoi(m[2]); err == nil && e > 0 {
			end = e
		}
		return
	}

	// Pattern 2: "lines 100 to 200"
	rangeToRegex := regexp.MustCompile(`(?i)lines?\s+(\d+)\s+(?:to|through)\s+(\d+)`)
	if m := rangeToRegex.FindStringSubmatch(query); len(m) > 2 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			start = s
		}
		if e, err := strconv.Atoi(m[2]); err == nil && e > 0 {
			end = e
		}
		return
	}

	// Pattern 3: "line 42" (single line → show context around it)
	singleLineRegex := regexp.MustCompile(`(?i)line\s+(\d+)\b`)
	if m := singleLineRegex.FindStringSubmatch(query); len(m) > 1 {
		if s, err := strconv.Atoi(m[1]); err == nil && s > 0 {
			start = s
			end = s + 50 // Show 50 lines from the specified line
		}
		return
	}

	return
}
