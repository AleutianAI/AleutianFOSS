// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package rag

import (
	"strings"
	"unicode"
)

// stopwords are common English words that should not be treated as entity candidates.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "in": true, "of": true,
	"for": true, "to": true, "and": true, "or": true, "is": true,
	"it": true, "by": true, "on": true, "at": true, "with": true,
	"from": true, "that": true, "this": true, "are": true, "be": true,
	"has": true, "have": true, "was": true, "were": true, "been": true,
	"do": true, "does": true, "did": true, "will": true, "would": true,
	"can": true, "could": true, "should": true, "may": true, "might": true,
	"what": true, "which": true, "who": true, "how": true, "where": true,
	"when": true, "why": true, "all": true, "each": true, "every": true,
	"most": true, "some": true, "any": true, "no": true, "not": true,
	"me": true, "my": true, "i": true, "we": true, "our": true,
	"you": true, "your": true, "they": true, "their": true, "its": true,
}

// toolVerbs are verbs commonly used to describe tool actions, not entity names.
var toolVerbs = map[string]bool{
	"find": true, "get": true, "list": true, "show": true, "search": true,
	"analyze": true, "check": true, "trace": true, "explore": true,
	"look": true, "tell": true, "give": true, "describe": true,
}

// TokenizeQuery extracts candidate entity names from a natural language query.
//
// Description:
//
//	Splits a query into tokens that might be code entity references
//	(symbol names, package names, file paths). Handles quoted strings,
//	CamelCase splitting, snake_case splitting, and path-like tokens.
//	Filters stopwords and common tool verbs.
//
// Inputs:
//
//	query - Natural language query (e.g., "find hotspots in the rendering subsystem")
//
// Outputs:
//
//	[]string - Candidate entity tokens, deduplicated, order preserved.
//
// Thread Safety: Stateless, safe for concurrent use.
func TokenizeQuery(query string) []string {
	var candidates []string
	seen := make(map[string]bool)

	addCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(s) < 2 {
			return
		}
		lower := strings.ToLower(s)
		if stopwords[lower] || toolVerbs[lower] {
			return
		}
		if !seen[lower] {
			seen[lower] = true
			candidates = append(candidates, s)
		}
	}

	// Pass 1: Extract quoted strings as high-priority candidates.
	remaining := extractQuoted(query, addCandidate)

	// Pass 2: Split remaining text into words.
	words := strings.Fields(remaining)
	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'`")
		if word == "" {
			continue
		}

		// Path-like tokens (contains / or \) are kept whole.
		if strings.ContainsAny(word, "/\\") {
			addCandidate(word)
			// Also add the last path component.
			parts := strings.FieldsFunc(word, func(r rune) bool { return r == '/' || r == '\\' })
			if len(parts) > 1 {
				addCandidate(parts[len(parts)-1])
			}
			continue
		}

		// snake_case tokens: add whole and split parts.
		if strings.Contains(word, "_") {
			addCandidate(word)
			for _, part := range strings.Split(word, "_") {
				addCandidate(part)
			}
			continue
		}

		// CamelCase tokens: add whole and split parts.
		if hasMixedCase(word) {
			addCandidate(word)
			for _, part := range splitCamelCase(word) {
				addCandidate(part)
			}
			continue
		}

		addCandidate(word)
	}

	// Pass 3: Build compound phrases from adjacent non-stopword tokens.
	// "rendering subsystem" → "rendering subsystem" as a single candidate.
	nonStop := filterNonStopwords(words)
	for i := 0; i < len(nonStop)-1; i++ {
		compound := nonStop[i] + " " + nonStop[i+1]
		addCandidate(compound)
	}

	return candidates
}

// extractQuoted extracts quoted substrings from the query, passes them to addFn,
// and returns the query with quoted parts removed.
func extractQuoted(query string, addFn func(string)) string {
	var result strings.Builder
	i := 0
	for i < len(query) {
		if query[i] == '"' || query[i] == '\'' || query[i] == '`' {
			quote := query[i]
			j := i + 1
			for j < len(query) && query[j] != quote {
				j++
			}
			if j < len(query) {
				addFn(query[i+1 : j])
				i = j + 1
				continue
			}
		}
		result.WriteByte(query[i])
		i++
	}
	return result.String()
}

// hasMixedCase returns true if the string contains both upper and lower case letters.
func hasMixedCase(s string) bool {
	hasUpper, hasLower := false, false
	for _, r := range s {
		if unicode.IsUpper(r) {
			hasUpper = true
		}
		if unicode.IsLower(r) {
			hasLower = true
		}
		if hasUpper && hasLower {
			return true
		}
	}
	return false
}

// splitCamelCase splits a CamelCase or camelCase string into parts.
// "FindHotspots" → ["Find", "Hotspots"]
// "handleHTTPRequest" → ["handle", "HTTP", "Request"]
func splitCamelCase(s string) []string {
	runes := []rune(s)
	var parts []string
	start := 0

	for i := 1; i < len(runes); i++ {
		// Split before this rune if:
		// 1. Previous is lowercase and this is uppercase: "handleH" → split before H
		// 2. This is uppercase, previous is uppercase, and next is lowercase: "HTTPRequest" → split before R
		if unicode.IsUpper(runes[i]) {
			if unicode.IsLower(runes[i-1]) {
				parts = append(parts, string(runes[start:i]))
				start = i
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) && i > start+1 {
				parts = append(parts, string(runes[start:i]))
				start = i
			}
		}
	}

	if start < len(runes) {
		parts = append(parts, string(runes[start:]))
	}

	return parts
}

// filterNonStopwords returns words that are not stopwords or tool verbs.
func filterNonStopwords(words []string) []string {
	var result []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		lower := strings.ToLower(w)
		if w != "" && !stopwords[lower] && !toolVerbs[lower] {
			result = append(result, w)
		}
	}
	return result
}
