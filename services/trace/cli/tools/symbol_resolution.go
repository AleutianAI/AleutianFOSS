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
	"log/slog"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// ResolveFunctionWithFuzzy attempts to resolve a function name using exact match first,
// then falls back to fuzzy search if exact match fails.
//
// Description:
//
//	P1 Fix (Feb 14, 2026): Reusable helper for all graph query tools to enable
//	partial matching. Prevents "symbol not found" errors when user provides
//	partial names like "Process" instead of "getDatesToProcess".
//
// Inputs:
//
//	ctx - Context for timeout control (2 second timeout for fuzzy search).
//	index - Symbol index to search.
//	name - Function name to resolve (may be partial).
//	logger - Logger for debugging and observability.
//
// Outputs:
//
//	*ast.Symbol - The resolved symbol (never nil if error is nil).
//	bool - True if fuzzy matching was used, false for exact match.
//	error - Non-nil if symbol could not be found by any method.
//
// Thread Safety: This function is safe for concurrent use.
//
// Example:
//
//	symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, "Process", logger)
//	if err != nil {
//	    return fmt.Errorf("symbol not found: %w", err)
//	}
//	if fuzzy {
//	    fmt.Printf("⚠️ Using partial match: %s\n", symbol.Name)
//	}
func ResolveFunctionWithFuzzy(
	ctx context.Context,
	index *index.SymbolIndex,
	name string,
	logger *slog.Logger,
) (*ast.Symbol, bool, error) {
	if index == nil {
		return nil, false, fmt.Errorf("symbol index is nil")
	}

	// Try exact match first (fast path)
	exactMatches := index.GetByName(name)
	if len(exactMatches) > 0 {
		logger.Info("Symbol resolution: exact match",
			slog.String("query", name),
			slog.String("resolved", exactMatches[0].Name),
			slog.String("kind", exactMatches[0].Kind.String()),
			slog.String("file", exactMatches[0].FilePath),
			slog.Int("line", exactMatches[0].StartLine),
		)
		return exactMatches[0], false, nil
	}

	// Try Type.Method dot notation split (e.g., "Plot.render" → type "Plot", method "render")
	if strings.Contains(name, ".") {
		parts := strings.SplitN(name, ".", 2)
		typeName, methodName := parts[0], parts[1]
		if sym, err := resolveTypeDotMethod(ctx, index, typeName, methodName, logger); err == nil {
			logger.Info("Symbol resolution: dot notation match",
				slog.String("query", name),
				slog.String("type", typeName),
				slog.String("method", methodName),
				slog.String("resolved", sym.Name),
				slog.String("file", sym.FilePath),
			)
			return sym, false, nil
		}
		// Fall through to fuzzy search if dot notation didn't work
	}

	// Fallback: Try fuzzy search (slower, requires context timeout)
	logger.Info("Symbol resolution: exact match failed, trying fuzzy search",
		slog.String("query", name),
	)

	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	fuzzyMatches, err := index.Search(searchCtx, name, 20) // Get top 20 to filter by kind
	if err != nil {
		logger.Debug("Symbol resolution: fuzzy search failed",
			slog.String("query", name),
			slog.String("error", err.Error()),
		)
		return nil, false, fmt.Errorf("no match found for '%s': %w", name, err)
	}

	if len(fuzzyMatches) == 0 {
		logger.Debug("Symbol resolution: no fuzzy matches found",
			slog.String("query", name),
		)
		return nil, false, fmt.Errorf("no match found for '%s'", name)
	}

	// Filter matches to only functions and methods (BEFORE selecting best match!)
	var functionsOnly []*ast.Symbol
	for _, match := range fuzzyMatches {
		if match.Kind == ast.SymbolKindFunction || match.Kind == ast.SymbolKindMethod {
			functionsOnly = append(functionsOnly, match)
		}
	}

	// Log all matches (showing which were filtered)
	logger.Info("Symbol resolution: fuzzy search results",
		slog.String("query", name),
		slog.Int("total_matches", len(fuzzyMatches)),
		slog.Int("functions_only", len(functionsOnly)),
	)

	for i, match := range fuzzyMatches {
		if i < 8 { // Log top 8 matches in detail
			isFunction := match.Kind == ast.SymbolKindFunction || match.Kind == ast.SymbolKindMethod
			logger.Info("Symbol resolution: fuzzy match candidate",
				slog.Int("rank", i+1),
				slog.String("query", name),
				slog.String("matched_name", match.Name),
				slog.String("kind", match.Kind.String()),
				slog.Bool("is_function", isFunction),
				slog.String("file", match.FilePath),
				slog.Int("line", match.StartLine),
			)
		}
	}

	// Check if we have any function/method matches
	if len(functionsOnly) == 0 {
		logger.Debug("Symbol resolution: fuzzy search found matches but none are functions/methods",
			slog.String("query", name),
			slog.Int("total_non_function_matches", len(fuzzyMatches)),
		)
		return nil, false, fmt.Errorf("no function or method named '%s' found (found %d non-function symbols)", name, len(fuzzyMatches))
	}

	// Use first function/method match (best score among functions)
	selectedMatch := functionsOnly[0]
	logger.Info("Symbol resolution: selected fuzzy match (function/method only)",
		slog.String("query", name),
		slog.String("matched", selectedMatch.Name),
		slog.String("kind", selectedMatch.Kind.String()),
		slog.String("file", selectedMatch.FilePath),
		slog.Int("line", selectedMatch.StartLine),
		slog.Int("function_candidates", len(functionsOnly)),
	)

	return selectedMatch, true, nil
}

// ResolveMultipleFunctionsWithFuzzy resolves multiple function names,
// using fuzzy matching as fallback for each.
//
// Description:
//
//	P1 Fix (Feb 14, 2026): Batch version of ResolveFunctionWithFuzzy for tools
//	that accept multiple targets (e.g., find_common_dependency).
//
// Inputs:
//
//	ctx - Context for timeout control.
//	index - Symbol index to search.
//	names - Function names to resolve.
//	logger - Logger for debugging.
//
// Outputs:
//
//	[]*ast.Symbol - Resolved symbols (length matches input names).
//	[]bool - Fuzzy match indicators (parallel to symbols).
//	error - Non-nil if ANY symbol could not be found.
//
// Thread Safety: This function is safe for concurrent use.
//
// Example:
//
//	symbols, fuzzy, err := ResolveMultipleFunctionsWithFuzzy(ctx, index,
//	    []string{"Handler", "Middleware"}, logger)
//	if err != nil {
//	    return fmt.Errorf("failed to resolve targets: %w", err)
//	}
func ResolveMultipleFunctionsWithFuzzy(
	ctx context.Context,
	index *index.SymbolIndex,
	names []string,
	logger *slog.Logger,
) ([]*ast.Symbol, []bool, error) {
	symbols := make([]*ast.Symbol, 0, len(names))
	fuzzyFlags := make([]bool, 0, len(names))

	for i, name := range names {
		symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, name, logger)
		if err != nil {
			logger.Debug("P1: Failed to resolve symbol in batch",
				slog.Int("index", i),
				slog.String("name", name),
				slog.String("error", err.Error()),
			)
			return nil, nil, fmt.Errorf("failed to resolve '%s': %w", name, err)
		}

		symbols = append(symbols, symbol)
		fuzzyFlags = append(fuzzyFlags, fuzzy)
	}

	return symbols, fuzzyFlags, nil
}

// resolveTypeDotMethod resolves a Type.Method query by finding a method named
// methodName that belongs to the type named typeName.
//
// Description:
//
//	Handles dot-notation queries like "Plot.render", "Context.JSON", or
//	"DataFrame.__init__" by splitting the query and matching the method to
//	its owning type via three strategies:
//	  1. Receiver field match (Go, JS): sym.Receiver == typeName
//	  2. ID contains match (JS fallback): sym.ID contains "typeName.methodName"
//	  3. Parent class match (Python, TS): type symbol's Children contain the method
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - idx: Symbol index to search. Must not be nil.
//   - typeName: The type/class part of the query (e.g., "Plot").
//   - methodName: The method part of the query (e.g., "render").
//   - logger: Logger for debugging. Must not be nil.
//
// Outputs:
//   - *ast.Symbol: The resolved method symbol. Never nil on success.
//   - error: Non-nil if no matching method could be found.
//
// Thread Safety: This function is safe for concurrent use.
func resolveTypeDotMethod(
	ctx context.Context,
	idx *index.SymbolIndex,
	typeName string,
	methodName string,
	logger *slog.Logger,
) (*ast.Symbol, error) {
	if idx == nil {
		return nil, fmt.Errorf("symbol index is nil")
	}
	if typeName == "" || methodName == "" {
		return nil, fmt.Errorf("typeName and methodName must not be empty")
	}

	// Strategy 1 & 2: Look up all symbols named methodName and filter
	methodMatches := idx.GetByName(methodName)

	var candidates []*ast.Symbol
	for _, sym := range methodMatches {
		if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction {
			continue
		}

		// Strategy 1: Receiver field match (Go, JS set this)
		if sym.Receiver == typeName {
			candidates = append(candidates, sym)
			continue
		}

		// Strategy 2: ID contains "typeName.methodName" (JS fallback)
		if strings.Contains(sym.ID, typeName+"."+methodName) {
			candidates = append(candidates, sym)
			continue
		}
	}

	// If we found candidates via receiver/ID, pick the best one
	if len(candidates) > 0 {
		best := pickBestCandidate(candidates)
		logger.Debug("resolveTypeDotMethod: matched via receiver/ID",
			slog.String("type", typeName),
			slog.String("method", methodName),
			slog.String("resolved_id", best.ID),
			slog.Int("candidates", len(candidates)),
		)
		return best, nil
	}

	// Strategy 3: Parent class match (Python, TS — Receiver may be empty)
	// Find the type/class symbol, then check its Children for the method
	typeMatches := idx.GetByName(typeName)
	for _, typeSym := range typeMatches {
		if typeSym.Kind != ast.SymbolKindClass &&
			typeSym.Kind != ast.SymbolKindStruct &&
			typeSym.Kind != ast.SymbolKindInterface &&
			typeSym.Kind != ast.SymbolKindType {
			continue
		}

		for _, child := range typeSym.Children {
			if child.Name == methodName &&
				(child.Kind == ast.SymbolKindMethod || child.Kind == ast.SymbolKindFunction) {
				logger.Debug("resolveTypeDotMethod: matched via parent class children",
					slog.String("type", typeName),
					slog.String("method", methodName),
					slog.String("resolved_id", child.ID),
					slog.String("parent_id", typeSym.ID),
				)
				return child, nil
			}
		}
	}

	// Strategy 4: Inheritance chain walk
	// If the type extends a parent, recursively search for the method on the parent.
	for _, typeSym := range typeMatches {
		if typeSym.Kind != ast.SymbolKindClass && typeSym.Kind != ast.SymbolKindStruct &&
			typeSym.Kind != ast.SymbolKindInterface && typeSym.Kind != ast.SymbolKindType {
			continue
		}
		if typeSym.Metadata == nil || typeSym.Metadata.Extends == "" {
			continue
		}
		logger.Debug("resolveTypeDotMethod: trying inheritance chain",
			slog.String("type", typeName),
			slog.String("extends", typeSym.Metadata.Extends),
		)
		parentSym, err := resolveTypeDotMethod(ctx, idx, typeSym.Metadata.Extends, methodName, logger)
		if err == nil {
			return parentSym, nil
		}
	}

	return nil, fmt.Errorf("no method '%s' found on type '%s'", methodName, typeName)
}

// pickBestCandidate selects the best symbol from a list of candidates.
// Prefers symbols with Receiver set, then shortest ID (most specific).
//
// Inputs:
//   - candidates: Non-empty slice of candidate symbols.
//
// Outputs:
//   - *ast.Symbol: The best candidate. Never nil when input is non-empty.
func pickBestCandidate(candidates []*ast.Symbol) *ast.Symbol {
	if len(candidates) == 1 {
		return candidates[0]
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		// Prefer symbols with Receiver set (more explicit match)
		if c.Receiver != "" && best.Receiver == "" {
			best = c
			continue
		}
		if c.Receiver == "" && best.Receiver != "" {
			continue
		}
		// Among equal receiver status, prefer shorter ID (more specific)
		if len(c.ID) < len(best.ID) {
			best = c
		}
	}

	return best
}
