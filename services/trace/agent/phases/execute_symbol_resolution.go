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
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var symbolResolutionTracer = otel.Tracer("aleutian.trace.phases.symbol_resolution")

// CB-31d: Typed errors for better error handling (M-R-1)
var (
	// ErrSymbolIndexNotAvailable indicates the symbol index is not initialized.
	ErrSymbolIndexNotAvailable = errors.New("symbol index not available")

	// ErrSymbolNotFound indicates no symbol matched the search criteria.
	ErrSymbolNotFound = errors.New("symbol not found")
)

// SymbolResolution holds a cached symbol resolution result.
//
// Description:
//
//	Stores the result of resolving a symbol name to a qualified symbol ID,
//	along with a confidence score indicating resolution quality.
//
// Thread Safety: This type is safe for concurrent use when stored in sync.Map.
type SymbolResolution struct {
	// SymbolID is the fully qualified symbol ID (e.g., "pkg/file.go:SymbolName").
	SymbolID string

	// Confidence is a strategy-based constant (0.0-1.0) indicating resolution quality.
	// IT-00a-1 Phase 4 note: These are hardcoded per-strategy constants, NOT computed
	// from match quality data (e.g., levenshtein distance, score margins). They provide
	// coarse ordering between strategies for observability but should not be used for
	// branching logic. If confidence-based branching is needed in the future, compute
	// from real signals (score margin, match count, edit distance).
	//
	// Current values:
	//   1.0  = exact match by ID
	//   0.95 = single exact name match
	//   0.8  = disambiguated name or substring match
	//   0.7  = fuzzy search match (function/method)
	//   0.6  = disambiguated name match (non-function)
	//   0.5  = fuzzy search match (non-function)
	Confidence float64

	// Strategy is the resolution strategy used ("exact", "name", "fuzzy").
	Strategy string
}

// resolveSymbol resolves a symbol name to a graph symbol using multiple strategies.
//
// Description:
//
//	Attempts to find a symbol in the graph using three strategies:
//	1. Exact match by symbol ID (O(1) hash lookup)
//	2. Fuzzy match by symbol name (handles unqualified names like "Handler" → "pkg/foo.go:Handler")
//	3. Partial/fuzzy search using SymbolIndex.Search (handles typos, partial matches)
//
// Inputs:
//   - deps: Dependencies with SymbolIndex (required)
//   - name: Symbol name extracted from query (may be unqualified)
//
// Outputs:
//   - symbolID: Resolved symbol ID (qualified path)
//   - confidence: Resolution confidence (0.0-1.0)
//   - error: Non-nil if no match found
//
// Example:
//
//	symbolID, conf, err := resolveSymbol(deps, "Handler")
//	// symbolID = "pkg/handlers/beacon_upload_handler.go:Handler"
//	// conf = 0.95
//
// Thread Safety: This function is safe for concurrent use.
func resolveSymbol(
	deps *Dependencies,
	name string,
) (symbolID string, confidence float64, strategy string, err error) {
	// CB-31d: Create OTel span for observability
	ctx := context.Background()
	ctx, span := symbolResolutionTracer.Start(ctx, "resolveSymbol",
		trace.WithAttributes(
			attribute.String("name", name),
		),
	)
	defer span.End()

	start := time.Now()
	defer func() {
		duration := time.Since(start)

		// CB-31d: Record Prometheus metrics
		symbolResolutionDuration.Observe(duration.Seconds())
		symbolResolutionAttempts.WithLabelValues(strategy).Inc()
		if err == nil {
			symbolResolutionConfidence.Observe(confidence)
		}

		span.SetAttributes(
			attribute.String("strategy", strategy),
			attribute.Float64("confidence", confidence),
			attribute.Int64("duration_ms", duration.Milliseconds()),
			attribute.Bool("success", err == nil),
		)
		if err == nil {
			slog.Debug("CB-31d: symbol resolution complete",
				slog.String("name", name),
				slog.String("resolved", symbolID),
				slog.String("strategy", strategy),
				slog.Float64("confidence", confidence),
				slog.Duration("duration", duration),
			)
		} else {
			slog.Debug("CB-31d: symbol resolution failed",
				slog.String("name", name),
				slog.Duration("duration", duration),
				slog.String("error", err.Error()),
			)
		}
	}()

	if deps == nil || deps.SymbolIndex == nil {
		return "", 0.0, "failed", ErrSymbolIndexNotAvailable
	}

	// Strategy 1: Exact match by ID (O(1))
	if symbol, ok := deps.SymbolIndex.GetByID(name); ok {
		span.SetAttributes(attribute.String("match_type", "exact"))
		return symbol.ID, 1.0, "exact", nil
	}

	// Strategy 2: Fuzzy match by name (O(1) with secondary index)
	matches := deps.SymbolIndex.GetByName(name)

	if len(matches) == 1 {
		// IT-04 Fix: Single exact name match — return it with high confidence regardless of kind.
		// Previously, non-function matches (classes, structs, interfaces) were skipped in favor of
		// substring/fuzzy search, which meant "TransformNode" (a class) would be deprioritized
		// even when it was the only exact match. An exact name match is always authoritative.
		match := matches[0]
		matchType := "name_single"
		if match.Kind == ast.SymbolKindFunction || match.Kind == ast.SymbolKindMethod {
			matchType = "name_single_function"
		}
		span.SetAttributes(
			attribute.String("match_type", matchType),
			attribute.Int("match_count", 1),
		)
		return match.ID, 0.95, "name", nil
	} else if len(matches) > 1 {
		span.SetAttributes(
			attribute.String("match_type", "name_multiple"),
			attribute.Int("match_count", len(matches)),
		)
		// IT-05 SR1: Multi-signal disambiguation for multiple exact name matches.
		// Instead of blindly picking the first Function-kind match, score all
		// matches using contextual signals (test file, export, depth) and pick
		// the best one. This fixes cases like "main" in cmd/main.go vs
		// internal/warpc/gen/main.go.
		best := disambiguateMultipleMatches(matches, deps, name)
		isFuncOrMethod := best.Kind == ast.SymbolKindFunction || best.Kind == ast.SymbolKindMethod
		conf := 0.8
		strat := "name_disambiguated"
		if !isFuncOrMethod {
			conf = 0.6
			strat = "name_ambiguous"
		}
		span.SetAttributes(
			attribute.Bool("function_preferred", isFuncOrMethod),
			attribute.String("disambiguated_file", best.FilePath),
		)
		return best.ID, conf, strat, nil
	}

	// IT-00a-1 Phase 1A: Unified search — single Search() call, partition by match type.
	// Previously, Strategy 2.5 (substring) and Strategy 3 (fuzzy) were separate Search()
	// calls with independent scoring systems. Now we call Search() once and partition
	// results into substring vs fuzzy-only buckets. System A (computeMatchScore) already
	// ranks substring matches (base score 3*100000) above fuzzy matches (base score
	// 4*100000), so the sorted order naturally separates them.
	searchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	searchResults, searchErr := deps.SymbolIndex.Search(searchCtx, name, 50)

	if searchErr == nil && len(searchResults) > 0 {
		searchLower := strings.ToLower(name)

		// IT-05 R6: Collect ALL substring matches, then pick the best one using
		// fan-out + query-context scoring. Previously, we returned the FIRST
		// substring match blindly, which caused "compaction" to match "bigger"
		// (a fuzzy match) or the wrong substring match in large codebases.
		var substringMatches []*ast.Symbol
		for _, result := range searchResults {
			nameLower := strings.ToLower(result.Name)
			if strings.Contains(nameLower, searchLower) {
				substringMatches = append(substringMatches, result)
			}
		}

		if len(substringMatches) > 0 {
			best := pickBestSubstringMatch(substringMatches, deps, name)
			matchType := "substring"
			if strings.HasPrefix(strings.ToLower(best.Name), searchLower) {
				matchType = "substring_prefix"
			}
			span.SetAttributes(
				attribute.String("match_type", matchType),
				attribute.Int("search_result_count", len(searchResults)),
				attribute.Int("substring_matches", len(substringMatches)),
			)
			return best.ID, 0.8, "substring", nil
		}

		// No substring matches — use best fuzzy result (System A ranked)
		span.SetAttributes(
			attribute.String("match_type", "fuzzy"),
			attribute.Int("search_result_count", len(searchResults)),
		)
		// Prefer functions/methods among fuzzy results
		for _, result := range searchResults {
			if result.Kind == ast.SymbolKindFunction || result.Kind == ast.SymbolKindMethod {
				span.SetAttributes(attribute.Bool("fuzzy_function_preferred", true))
				return result.ID, 0.7, "fuzzy", nil
			}
		}
		// No functions, use first result
		span.SetAttributes(attribute.Bool("fuzzy_function_preferred", false))
		return searchResults[0].ID, 0.5, "fuzzy_ambiguous", nil
	}

	// Build suggestion list from search results
	suggestions := []string{}
	if searchErr == nil && len(searchResults) > 0 {
		for i, result := range searchResults {
			if i >= 3 {
				break
			}
			kindStr := ""
			if result.Kind == ast.SymbolKindStruct || result.Kind == ast.SymbolKindInterface {
				kindStr = fmt.Sprintf(" (%s)", result.Kind)
			}
			suggestions = append(suggestions, result.Name+kindStr)
		}
	}

	span.SetAttributes(
		attribute.String("match_type", "none"),
		attribute.Int("suggestions_count", len(suggestions)),
	)

	if len(suggestions) > 0 {
		return "", 0.0, "failed", fmt.Errorf(
			"%w: %q. Did you mean: %s?",
			ErrSymbolNotFound, name, strings.Join(suggestions, ", "),
		)
	}

	return "", 0.0, "failed", fmt.Errorf("%w: %q", ErrSymbolNotFound, name)
}

// disambiguateMultipleMatches picks the best symbol from multiple exact name matches
// using multi-signal scoring.
//
// Description:
//
//	IT-05 SR1: When multiple symbols share the same name (e.g., multiple "main"
//	functions across packages), this function scores each candidate using
//	contextual signals and returns the best one.
//
//	Scoring signals (lower is better):
//	  - Test file: +50000 if symbol is in a test file
//	  - Export: +20000 if symbol is unexported
//	  - Underscore prefix: +10000 if name starts with _
//	  - Directory depth: +1000 per level beyond 2
//	  - Kind: +0 for function/method, +1 for type, +2 for variable
//
// Inputs:
//
//	matches - Slice of symbols with the same name (len >= 2).
//
// Outputs:
//
//	*ast.Symbol - The best-scoring match.
//
// Thread Safety: Safe for concurrent use (stateless function).
func disambiguateMultipleMatches(matches []*ast.Symbol, deps *Dependencies, searchTerm string) *ast.Symbol {
	if len(matches) == 0 {
		return nil
	}
	if len(matches) == 1 {
		return matches[0]
	}

	best := matches[0]
	bestScore := scoreForDisambiguation(best, deps, searchTerm)

	for _, m := range matches[1:] {
		s := scoreForDisambiguation(m, deps, searchTerm)
		if s < bestScore {
			best = m
			bestScore = s
		}
	}

	return best
}

// scoreForDisambiguation computes a disambiguation score for a symbol.
// Lower scores indicate more relevant symbols.
func scoreForDisambiguation(sym *ast.Symbol, deps *Dependencies, searchTerm string) int {
	score := 0

	// Test file penalty
	if isTestFilePath(sym.FilePath) {
		score += 50000
	}

	// Export penalty
	if !sym.Exported {
		score += 20000
	}

	// Underscore prefix penalty
	if len(sym.Name) > 0 && sym.Name[0] == '_' {
		score += 10000
	}

	// IT-05 R4 Fix (R4-CFA-5): Fan-out penalty.
	// Symbols with 0 outgoing call edges are likely constants, fields, or types
	// that won't produce useful call chain results. Penalize them so symbols
	// with actual call edges are preferred during disambiguation.
	if deps != nil && deps.GraphAnalytics != nil {
		if node, ok := deps.GraphAnalytics.GetNode(sym.ID); ok {
			if len(node.Outgoing) == 0 {
				score += 5000
			}
		} else {
			// Symbol not in graph at all — heavy penalty
			score += 8000
		}
	}

	// Directory depth penalty
	depth := strings.Count(sym.FilePath, "/")
	if depth > 2 {
		score += (depth - 2) * 1000
	}

	// IT-05 R6: Query-context relevance bonus.
	// When disambiguating "main" across cmd/main.go vs livereload/gen/main.go,
	// prefer the one whose file path matches words from the query.
	// E.g., "main function to page rendering" → prefer cmd/main.go (shorter path,
	// no "gen" or "livereload" context). But "content parsing" → prefer files in
	// content/ directory.
	if deps != nil && deps.Query != "" {
		filePathLower := strings.ToLower(sym.FilePath)
		for _, word := range strings.Fields(strings.ToLower(deps.Query)) {
			if len(word) >= 4 && strings.Contains(filePathLower, word) {
				score -= 3000
				break
			}
		}

		// IT-05 R7: Query-context name bonus (same as scoreSubstringMatch).
		score += queryContextNameBonus(sym.Name, deps.Query, searchTerm)
	}

	// Kind preference: functions/methods > types > variables
	switch sym.Kind {
	case ast.SymbolKindFunction, ast.SymbolKindMethod:
		// Best — no penalty
	case ast.SymbolKindClass, ast.SymbolKindStruct, ast.SymbolKindInterface, ast.SymbolKindType:
		score += 1
	default:
		score += 2
	}

	return score
}

// isTestFilePath checks if a file path indicates a test file.
// Mirrors index.isTestFile but in the phases package.
func isTestFilePath(filePath string) bool {
	lower := strings.ToLower(filePath)

	// Directory-based detection — check both mid-path ("/test/") and path-start ("test/")
	for _, dir := range []string{"test/", "tests/", "__tests__/", "testing/"} {
		if strings.HasPrefix(lower, dir) || strings.Contains(lower, "/"+dir) {
			return true
		}
	}

	// Go test files
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}

	// Python test files
	if strings.HasSuffix(lower, "_test.py") || strings.HasSuffix(lower, "conftest.py") {
		return true
	}

	// Python test_ prefix
	lastSlash := strings.LastIndex(lower, "/")
	fileName := lower
	if lastSlash >= 0 {
		fileName = lower[lastSlash+1:]
	}
	if strings.HasPrefix(fileName, "test_") {
		return true
	}

	// JS/TS test files
	for _, suffix := range []string{
		".test.js", ".test.ts", ".test.jsx", ".test.tsx",
		".spec.js", ".spec.ts", ".spec.jsx", ".spec.tsx",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

// pickBestSubstringMatch picks the best symbol from multiple substring matches
// using fan-out scoring and query-context relevance.
//
// Description:
//
//	IT-05 R6: When multiple symbols match a search term as substring (e.g.,
//	"compaction" matches "runCompaction", "doCompaction", "compactDef"),
//	this function scores them to pick the most useful one for call chain analysis.
//
//	Scoring (lower is better):
//	  - Fan-out bonus: -1000 per outgoing edge (prefer symbols with more call edges)
//	  - Kind preference: +0 for function/method, +5000 for other kinds
//	  - Test file penalty: +50000
//	  - Query-context bonus: -2000 if file path contains words from the query
//	  - Directory depth penalty: +500 per level beyond 2
//
// Inputs:
//
//	matches - Symbols whose names contain the search term.
//	deps - Dependencies with GraphAnalytics and Query.
//
// Outputs:
//
//	*ast.Symbol - The best-scoring match.
func pickBestSubstringMatch(matches []*ast.Symbol, deps *Dependencies, searchTerm string) *ast.Symbol {
	if len(matches) == 1 {
		return matches[0]
	}

	// Extract context words from the query for relevance scoring
	var queryContextWords []string
	if deps != nil && deps.Query != "" {
		for _, word := range strings.Fields(strings.ToLower(deps.Query)) {
			if len(word) >= 4 { // Only meaningful words
				queryContextWords = append(queryContextWords, word)
			}
		}
	}

	best := matches[0]
	bestScore := scoreSubstringMatch(best, deps, queryContextWords, searchTerm)

	for _, m := range matches[1:] {
		s := scoreSubstringMatch(m, deps, queryContextWords, searchTerm)
		if s < bestScore {
			best = m
			bestScore = s
		}
	}

	return best
}

// scoreSubstringMatch computes a score for a substring match. Lower is better.
func scoreSubstringMatch(sym *ast.Symbol, deps *Dependencies, queryContextWords []string, searchTerm string) int {
	score := 0

	// Kind preference: functions/methods are what we want for call chains
	if sym.Kind != ast.SymbolKindFunction && sym.Kind != ast.SymbolKindMethod {
		score += 5000
	}

	// Test file penalty
	if isTestFilePath(sym.FilePath) {
		score += 50000
	}

	// Fan-out bonus: prefer symbols with more outgoing edges (richer call chains)
	if deps != nil && deps.GraphAnalytics != nil {
		if node, ok := deps.GraphAnalytics.GetNode(sym.ID); ok {
			edgeCount := len(node.Outgoing)
			if edgeCount == 0 {
				score += 3000 // No edges — likely not useful for call chains
			} else {
				score -= min(edgeCount*200, 2000) // Cap bonus at -2000
			}
		} else {
			score += 1000 // Not in graph
		}
	}

	// Query-context relevance: bonus if file path matches words from the query.
	// E.g., query about "compaction" prefers symbols in "compaction.go" or "levels.go"
	// over symbols in "merge_iterator.go".
	if len(queryContextWords) > 0 {
		filePathLower := strings.ToLower(sym.FilePath)
		for _, word := range queryContextWords {
			if strings.Contains(filePathLower, word) {
				score -= 2000
				break // One match is enough
			}
		}
	}

	// Directory depth penalty (prefer shallower files — more likely core code)
	depth := strings.Count(sym.FilePath, "/")
	if depth > 2 {
		score += (depth - 2) * 500
	}

	// IT-05 R7: Query-context name bonus.
	// Prefer symbols whose names contain OTHER query words beyond the search term.
	// E.g., query "memtable flush" + searchTerm "flush" → "flushMemtable" gets -4000.
	if deps != nil && deps.Query != "" {
		score += queryContextNameBonus(sym.Name, deps.Query, searchTerm)
	}

	return score
}

// queryContextNameBonus returns a negative score (bonus) when the symbol's name
// contains words from the user's query beyond the search term itself.
//
// Description:
//
//	IT-05 R7: Makes compound names that match multiple query words rank higher.
//	Example: query = "memtable flush to WAL", searchTerm = "flush"
//	  - sym.Name = "Flush" → bonus = 0 (no other query words in name)
//	  - sym.Name = "flushMemtable" → bonus = -4000 ("memtable" found in name)
//
// Inputs:
//   - symName: The symbol's name to check.
//   - query: The full user query string.
//   - searchTerm: The primary search term (excluded from bonus to avoid double-counting).
//
// Outputs:
//   - int: Negative score (bonus) per additional query word found in the name.
//     Returns 0 if no additional query words match.
//
// Thread Safety: Safe for concurrent use (stateless function).
func queryContextNameBonus(symName string, query string, searchTerm string) int {
	if query == "" || symName == "" {
		return 0
	}

	nameLower := strings.ToLower(symName)
	searchTermLower := strings.ToLower(searchTerm)

	bonus := 0
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if len(word) < 4 {
			continue // Skip short words
		}
		if word == searchTermLower {
			continue // Don't double-count the search term itself
		}
		if strings.Contains(nameLower, word) {
			bonus -= 4000 // Strong bonus per additional query word in name
		}
	}
	return bonus
}

// resolveSymbolCached wraps resolveSymbol with session-level caching.
//
// Description:
//
//	Caches symbol resolutions per session to avoid repeated lookups.
//	Cache is keyed by "sessionID:symbolName" and cleared on graph refresh.
//
// Inputs:
//   - cache: Session-level cache (sync.Map)
//   - sessionID: Current session ID
//   - name: Symbol name to resolve
//   - deps: Dependencies with graph access
//
// Outputs:
//   - symbolID: Resolved symbol ID
//   - confidence: Resolution confidence
//   - error: Non-nil if resolution fails
//
// Thread Safety: This function is safe for concurrent use (uses sync.Map).
func resolveSymbolCached(
	cache *sync.Map,
	sessionID string,
	name string,
	deps *Dependencies,
) (symbolID string, confidence float64, err error) {
	cacheKey := fmt.Sprintf("%s:%s", sessionID, name)

	// Check cache
	if cached, ok := cache.Load(cacheKey); ok {
		if result, ok := cached.(SymbolResolution); ok {
			// CB-31d: Record cache hit metric
			symbolCacheHits.Inc()
			slog.Debug("CB-31d: symbol resolution: cache hit",
				slog.String("name", name),
				slog.String("resolved", result.SymbolID),
				slog.Float64("confidence", result.Confidence),
				slog.String("strategy", result.Strategy),
			)
			return result.SymbolID, result.Confidence, nil
		}
	}

	// CB-31d: Record cache miss metric
	symbolCacheMisses.Inc()

	// Resolve
	symbolID, confidence, strategy, err := resolveSymbol(deps, name)
	if err != nil {
		return "", 0.0, err
	}

	// Cache result
	cache.Store(cacheKey, SymbolResolution{
		SymbolID:   symbolID,
		Confidence: confidence,
		Strategy:   strategy,
	})

	slog.Debug("CB-31d: symbol resolution: cache miss, resolved and cached",
		slog.String("name", name),
		slog.String("resolved", symbolID),
		slog.Float64("confidence", confidence),
		slog.String("strategy", strategy),
	)

	return symbolID, confidence, nil
}

// resolveFirstCandidate tries to resolve each candidate name in order, returning the
// first one that succeeds. This enables multi-candidate extraction: when the primary
// extraction is wrong (e.g., extracts "Build" from "Build the call graph from ProcessData"),
// the next candidate ("ProcessData") gets tried.
//
// Description:
//
//	IT-00a-1 Phase 3: Candidate-loop resolution. Uses resolveSymbolCached for each
//	candidate, stopping at the first successful resolution.
//	IT-05 R5: Added ctx parameter for stem expansion timeout control.
//	When all candidates fail direct resolution (or all resolve to 0-edge symbols),
//	falls back to stem expansion search.
//
// Inputs:
//
//   - ctx: Context for cancellation and timeout. Must not be nil.
//   - cache: Session-level cache (sync.Map).
//   - sessionID: Current session ID.
//   - candidates: Ranked candidate names (best first).
//   - deps: Dependencies with graph access.
//
// Outputs:
//
//   - symbolID: Resolved symbol ID from the first successful candidate.
//   - rawName: The candidate name that resolved successfully.
//   - confidence: Resolution confidence.
//   - error: Non-nil if ALL candidates fail to resolve.
//
// Thread Safety: Safe for concurrent use (uses sync.Map internally).
func resolveFirstCandidate(
	ctx context.Context,
	cache *sync.Map,
	sessionID string,
	candidates []string,
	deps *Dependencies,
) (symbolID string, rawName string, confidence float64, err error) {
	if len(candidates) == 0 {
		return "", "", 0, fmt.Errorf("no candidates provided for resolution")
	}

	// IT-05 R4 Fix: Track the first successful resolution as fallback.
	// If no candidate passes the fan-out quality gate, we return the first
	// resolution rather than failing entirely.
	var fallbackID, fallbackName string
	var fallbackConf float64
	hasFallback := false

	var lastErr error
	for _, candidate := range candidates {
		resolved, conf, resolveErr := resolveSymbolCached(cache, sessionID, candidate, deps)
		if resolveErr == nil {
			// IT-05 R4 Fix: Fan-out quality gate.
			// When a resolved symbol has 0 outgoing call edges AND more candidates
			// remain, skip it — it's likely a constant, field, or type with no
			// downstream call chain. Save as fallback in case all candidates fail
			// the gate.
			if !hasFallback {
				fallbackID = resolved
				fallbackName = candidate
				fallbackConf = conf
				hasFallback = true
			}

			hasCallEdges := true
			if deps != nil && deps.GraphAnalytics != nil {
				if node, ok := deps.GraphAnalytics.GetNode(resolved); ok {
					hasCallEdges = len(node.Outgoing) > 0
				}
			}

			if !hasCallEdges && candidate != candidates[len(candidates)-1] {
				slog.Debug("IT-05: skipping candidate with 0 outgoing call edges",
					slog.String("candidate", candidate),
					slog.String("symbol_id", resolved),
					slog.Int("remaining_candidates", len(candidates)-1),
				)
				continue
			}

			if len(candidates) > 1 {
				slog.Debug("IT-00a-1: resolved via candidate loop",
					slog.String("resolved_candidate", candidate),
					slog.String("symbol_id", resolved),
					slog.Int("total_candidates", len(candidates)),
					slog.Bool("passed_fanout_gate", hasCallEdges),
				)
			}
			return resolved, candidate, conf, nil
		}
		lastErr = resolveErr
	}

	// IT-05 R4 Fix: If we had a resolution that failed the fan-out gate but
	// no better candidate was found, return the fallback rather than failing.
	if hasFallback {
		slog.Debug("IT-05: returning fallback resolution (all candidates had 0 call edges)",
			slog.String("fallback_candidate", fallbackName),
			slog.String("fallback_id", fallbackID),
		)
		return fallbackID, fallbackName, fallbackConf, nil
	}

	// IT-05 R5: Stem expansion — last resort for concept queries.
	// When no candidate resolves directly (or all resolved to 0-edge symbols),
	// search for functions whose names CONTAIN the candidate as a substring.
	// This handles "concept queries" where users describe flows ("memtable flush")
	// rather than naming functions directly.
	if stemID, stemName, stemConf := stemExpansionFallback(ctx, candidates, deps); stemID != "" {
		slog.Debug("IT-05 R5: stem expansion resolved concept query",
			slog.String("stem_candidate", stemName),
			slog.String("resolved_id", stemID),
		)
		return stemID, stemName, stemConf, nil
	}

	return "", candidates[0], 0, fmt.Errorf("none of %d candidates resolved: last error: %w", len(candidates), lastErr)
}

// stemExpansionFallback searches for symbols whose names contain a candidate
// as a substring, filtered to functions/methods with outgoing call edges.
//
// Description:
//
//	IT-05 R5: Handles "concept queries" where users describe flows ("memtable flush")
//	rather than naming functions. When direct resolution fails for all candidates,
//	this searches for symbols whose names CONTAIN the candidate as a substring
//	(e.g., "compaction" → finds "runCompaction", "doCompaction", "compact").
//
//	Language-agnostic — SymbolIndex.Search() indexes all parsers.
//	Works for Go (runCompaction), Python (flush_memtable), JS/TS (setMaterial).
//
//	Overlap note: resolveSymbol (called by resolveSymbolCached in the main candidate
//	loop) also calls SymbolIndex.Search() for fuzzy/substring matching. In many cases,
//	resolveSymbol finds the substring match first at confidence 0.8. This function
//	provides net-new value only when resolveSymbol's match resolves to a non-function
//	symbol (e.g., a type or variable) that fails the fan-out quality gate, and then
//	stem expansion finds a function/method with call edges. The overlapping coverage
//	is intentional — this is a safety net for edge cases, not a primary resolution path.
//
// Inputs:
//   - ctx: Context for timeout control (500ms timeout). Must not be nil.
//   - candidates: Candidate words to search for as substrings.
//   - deps: Dependencies with SymbolIndex and optionally GraphAnalytics.
//
// Outputs:
//   - symbolID: Resolved symbol ID, or empty if no match found.
//   - rawName: The candidate that matched.
//   - confidence: Fixed at 0.6 (with graph) or 0.5 (without graph).
//
// Thread Safety: Safe for concurrent use.
func stemExpansionFallback(
	ctx context.Context,
	candidates []string,
	deps *Dependencies,
) (symbolID string, rawName string, confidence float64) {
	if deps == nil || deps.SymbolIndex == nil {
		return "", "", 0
	}

	searchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	// IT-05 R7: Collect ALL eligible matches across all candidates, then pick the best.
	// Previously, we returned the FIRST function/method with outgoing edges, which
	// meant ordering of search results determined the winner — not query relevance.
	type scoredMatch struct {
		id        string
		candidate string
		score     int
		hasGraph  bool
	}
	var allMatches []scoredMatch

	// Build query context words once
	var queryContextWords []string
	if deps.Query != "" {
		for _, word := range strings.Fields(strings.ToLower(deps.Query)) {
			if len(word) >= 4 {
				queryContextWords = append(queryContextWords, word)
			}
		}
	}

	for _, candidate := range candidates {
		if len(candidate) < 4 {
			continue // Too short for meaningful substring search
		}

		results, err := deps.SymbolIndex.Search(searchCtx, candidate, 30)
		if err != nil || len(results) == 0 {
			continue
		}

		candidateLower := strings.ToLower(candidate)

		for _, result := range results {
			nameLower := strings.ToLower(result.Name)
			if !strings.Contains(nameLower, candidateLower) {
				continue
			}
			if result.Kind != ast.SymbolKindFunction && result.Kind != ast.SymbolKindMethod {
				continue
			}

			// Score using the same system as scoreSubstringMatch
			score := scoreSubstringMatch(result, deps, queryContextWords, candidate)

			hasGraph := false
			if deps.GraphAnalytics != nil {
				if node, ok := deps.GraphAnalytics.GetNode(result.ID); ok {
					hasGraph = true
					if len(node.Outgoing) == 0 {
						continue // Skip 0-edge symbols entirely in stem expansion
					}
				}
			}

			allMatches = append(allMatches, scoredMatch{
				id:        result.ID,
				candidate: candidate,
				score:     score,
				hasGraph:  hasGraph,
			})
		}
	}

	if len(allMatches) == 0 {
		return "", "", 0
	}

	// Pick the best (lowest score)
	best := allMatches[0]
	for _, m := range allMatches[1:] {
		if m.score < best.score {
			best = m
		}
	}

	conf := 0.6
	if !best.hasGraph {
		conf = 0.5
	}

	slog.Debug("IT-05 R7: stem expansion picked best match",
		slog.String("candidate", best.candidate),
		slog.String("symbol_id", best.id),
		slog.Int("score", best.score),
		slog.Int("total_matches", len(allMatches)),
	)

	return best.id, best.candidate, conf
}
