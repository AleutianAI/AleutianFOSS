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
	"sort"
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

	// Confidence is the resolution confidence score (0.0-1.0).
	//   1.0 = exact match by ID
	//   0.95 = single name match
	//   0.8 = multiple name matches, picked function
	//   0.7 = fuzzy search match (function)
	//   0.6 = multiple name matches, picked non-function
	//   0.5 = fuzzy search match (non-function)
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
		// Multiple matches - prefer functions/methods over types
		for _, match := range matches {
			if match.Kind == ast.SymbolKindFunction || match.Kind == ast.SymbolKindMethod {
				span.SetAttributes(attribute.Bool("function_preferred", true))
				return match.ID, 0.8, "name_disambiguated", nil
			}
		}
		// No functions found in multiple matches, return first with lower confidence
		span.SetAttributes(attribute.Bool("function_preferred", false))
		return matches[0].ID, 0.6, "name_ambiguous", nil
	}

	// Strategy 2.5: Substring matching (CB-31d Test 94 fix)
	// Finds symbols whose names CONTAIN the search term
	// Example: "Handler" matches "NewHandler", "handleProcessingErrors"
	searchCtx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel2()
	substringResults, _ := deps.SymbolIndex.Search(searchCtx2, name, 50) // More results for substring scan

	type substringMatch struct {
		symbol *ast.Symbol
		score  float64
	}
	substringMatches := []substringMatch{}

	for _, result := range substringResults {
		nameLower := strings.ToLower(result.Name)
		searchLower := strings.ToLower(name)

		// Check if symbol name contains search term
		if strings.Contains(nameLower, searchLower) {
			score := 0.75 // Base score for substring match

			// Bonus for prefix match ("Handler" in "HandlerFunc")
			if strings.HasPrefix(nameLower, searchLower) {
				score = 0.85
			}

			// Bonus for functions/methods (prefer over types)
			if result.Kind == ast.SymbolKindFunction || result.Kind == ast.SymbolKindMethod {
				score += 0.05
			}

			substringMatches = append(substringMatches, substringMatch{result, score})
		}
	}

	// Return best substring match if any found
	if len(substringMatches) > 0 {
		// Sort by score descending
		sort.Slice(substringMatches, func(i, j int) bool {
			return substringMatches[i].score > substringMatches[j].score
		})

		best := substringMatches[0]
		span.SetAttributes(
			attribute.String("match_type", "substring"),
			attribute.Float64("match_score", best.score),
			attribute.Int("substring_candidates", len(substringMatches)),
		)
		return best.symbol.ID, best.score, "substring", nil
	}

	// Strategy 3: Partial/fuzzy search using Search API (fallback)
	searchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond) // Increased from 100ms
	defer cancel()
	searchResults, searchErr := deps.SymbolIndex.Search(searchCtx, name, 10) // Limit to top 10
	if searchErr == nil && len(searchResults) > 0 {
		span.SetAttributes(
			attribute.String("match_type", "fuzzy"),
			attribute.Int("fuzzy_result_count", len(searchResults)),
		)
		// Prefer functions/methods
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

	// Build suggestion list from fuzzy search results (even if none matched preferences)
	suggestions := []string{}
	if searchErr == nil && len(searchResults) > 0 {
		for i, result := range searchResults {
			if i >= 3 {
				break
			} // Max 3 suggestions
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
