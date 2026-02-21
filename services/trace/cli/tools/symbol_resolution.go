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

// KindFilter controls which symbol kinds are accepted during resolution.
//
// Description:
//
//	Used as an option to ResolveFunctionWithFuzzy to configure which symbol
//	kinds are accepted on both the exact match and fuzzy search paths.
//	Default is KindFilterCallable, which preserves the original behavior of
//	filtering to Function, Method, and Property symbols.
//
// Thread Safety: This type is safe for concurrent use (immutable after creation).
type KindFilter int

const (
	// KindFilterCallable accepts Function, Method, and Property symbols.
	// This is the default, preserving the original behavior of ResolveFunctionWithFuzzy.
	KindFilterCallable KindFilter = iota

	// KindFilterType accepts Interface, Class, Struct, and Type symbols.
	// Used by find_implementations and similar type-focused tools.
	KindFilterType

	// KindFilterAny accepts all symbol kinds without filtering.
	// Used by find_references and tools that operate on any symbol.
	KindFilterAny
)

// String returns the string representation of the KindFilter.
//
// Outputs:
//   - string: Human-readable filter name for logging ("callable", "type", "any").
//
// Thread Safety: This method is safe for concurrent use.
func (kf KindFilter) String() string {
	switch kf {
	case KindFilterCallable:
		return "callable"
	case KindFilterType:
		return "type"
	case KindFilterAny:
		return "any"
	default:
		return fmt.Sprintf("unknown(%d)", int(kf))
	}
}

// ResolveFuzzyOpt configures optional behavior for ResolveFunctionWithFuzzy.
//
// Thread Safety: Option functions are safe for concurrent use.
type ResolveFuzzyOpt func(*resolveFuzzyConfig)

// resolveFuzzyConfig holds the configuration for ResolveFunctionWithFuzzy options.
type resolveFuzzyConfig struct {
	// kindFilter controls which symbol kinds are accepted (default: KindFilterCallable).
	kindFilter KindFilter

	// bareMethodFallback enables the IT-02 C-1 bare method name fallback.
	// When dot-notation resolution fails, tries resolving just the method part alone.
	bareMethodFallback bool
}

// WithKindFilter sets which symbol kinds to accept during resolution.
//
// Description:
//
//	Overrides the default KindFilterCallable behavior. Use KindFilterType for
//	type-focused tools (find_implementations) or KindFilterAny for tools that
//	operate on any symbol kind (find_references).
//
// Inputs:
//   - kf: The kind filter to apply to both exact match and fuzzy search paths.
//
// Example:
//
//	sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Router", logger,
//	    WithKindFilter(KindFilterType))
//
// Thread Safety: This function is safe for concurrent use.
func WithKindFilter(kf KindFilter) ResolveFuzzyOpt {
	return func(c *resolveFuzzyConfig) {
		c.kindFilter = kf
	}
}

// WithBareMethodFallback enables the bare method name fallback for dot-notation queries.
//
// Description:
//
//	IT-02 C-1: When dot-notation resolution fails (e.g., "DB.Open" where Open is
//	a package-level function, not a method on DB), the resolver will try resolving
//	just the method part ("Open") alone via exact match and kind filtering.
//
// Example:
//
//	sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "DB.Open", logger,
//	    WithBareMethodFallback())
//
// Thread Safety: This function is safe for concurrent use.
func WithBareMethodFallback() ResolveFuzzyOpt {
	return func(c *resolveFuzzyConfig) {
		c.bareMethodFallback = true
	}
}

// matchesKindFilter returns true if the symbol's kind is accepted by the filter.
//
// Description:
//
//	Evaluates a symbol against the configured KindFilter. Used internally by
//	ResolveFunctionWithFuzzy to filter both exact match and fuzzy search results.
//
// Inputs:
//   - sym: The symbol to check. Must not be nil.
//   - kf: The kind filter to evaluate against.
//
// Outputs:
//   - bool: True if the symbol's kind passes the filter.
//
// Thread Safety: This function is safe for concurrent use.
func matchesKindFilter(sym *ast.Symbol, kf KindFilter) bool {
	switch kf {
	case KindFilterCallable:
		return sym.Kind == ast.SymbolKindFunction ||
			sym.Kind == ast.SymbolKindMethod ||
			sym.Kind == ast.SymbolKindProperty
	case KindFilterType:
		return sym.Kind == ast.SymbolKindInterface ||
			sym.Kind == ast.SymbolKindClass ||
			sym.Kind == ast.SymbolKindStruct ||
			sym.Kind == ast.SymbolKindType
	case KindFilterAny:
		return true
	default:
		return true
	}
}

// ResolveFunctionWithFuzzy attempts to resolve a function name using exact match first,
// then falls back to fuzzy search if exact match fails.
//
// Description:
//
//	Shared resolution helper for all graph query tools. Resolves a user-provided
//	symbol name to a concrete *ast.Symbol via a multi-strategy pipeline:
//	  1. Full-ID bypass: if name contains ":", treat as a full symbol ID
//	  2. Exact match: index.GetByName(name) with kind filtering
//	  3. Dot-notation: "Type.Method" split via resolveTypeDotMethod (4 strategies)
//	  4. Bare method fallback: try just method part when dot-notation fails (opt-in)
//	  5. Fuzzy search: index.Search with kind filtering
//
//	IT-00a (Feb 18, 2026): Extended with configurable kind filtering via
//	ResolveFuzzyOpt options. Default behavior (no options) preserves original
//	Function/Method/Property filtering for backward compatibility.
//
// Inputs:
//   - ctx: Context for timeout control (2 second timeout for fuzzy search). Must not be nil.
//   - index: Symbol index to search. Must not be nil.
//   - name: Symbol name to resolve (may be partial, dot-notation, or full ID).
//   - logger: Logger for debugging and observability. Must not be nil.
//   - opts: Optional configuration (WithKindFilter, WithBareMethodFallback).
//
// Outputs:
//   - *ast.Symbol: The resolved symbol. Never nil if error is nil.
//   - bool: True if fuzzy matching was used, false for exact/dot-notation match.
//   - error: Non-nil if symbol could not be found by any method.
//
// Thread Safety: This function is safe for concurrent use.
//
// Example:
//
//	// Default (callable symbols):
//	symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, "Process", logger)
//
//	// Type-only symbols:
//	symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, "Router", logger,
//	    WithKindFilter(KindFilterType))
//
//	// Any kind with bare method fallback:
//	symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, "DB.Open", logger,
//	    WithKindFilter(KindFilterAny), WithBareMethodFallback())
func ResolveFunctionWithFuzzy(
	ctx context.Context,
	index *index.SymbolIndex,
	name string,
	logger *slog.Logger,
	opts ...ResolveFuzzyOpt,
) (*ast.Symbol, bool, error) {
	if index == nil {
		return nil, false, fmt.Errorf("symbol index is nil")
	}

	// Apply options with defaults preserving original behavior
	cfg := resolveFuzzyConfig{
		kindFilter:         KindFilterCallable,
		bareMethodFallback: false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// Step 0: Full-ID bypass — if name looks like a full symbol ID, look up directly
	if strings.Contains(name, ":") {
		if sym, ok := index.GetByID(name); ok {
			logger.Info("Symbol resolution: full-ID bypass",
				slog.String("query", name),
				slog.String("resolved", sym.Name),
				slog.String("kind", sym.Kind.String()),
			)
			return sym, false, nil
		}
	}

	// Step 1: Try exact match first (fast path) with kind filtering
	exactMatches := index.GetByName(name)
	if len(exactMatches) > 0 {
		if cfg.kindFilter == KindFilterAny {
			// IT-06 Bug 5: When multiple symbols share the same name, prefer
			// higher-significance kinds (class > struct > interface > function > method)
			// over low-significance kinds (field, variable, parameter). Without this,
			// "Engine" in BabylonJS resolves to a field instead of the Engine class,
			// and "Request" in Express resolves to a test variable instead of the
			// Request object in lib/request.js.
			best := pickMostSignificantSymbol(exactMatches)
			logger.Info("Symbol resolution: exact match",
				slog.String("query", name),
				slog.String("resolved", best.Name),
				slog.String("kind", best.Kind.String()),
				slog.String("file", best.FilePath),
				slog.Int("line", best.StartLine),
				slog.Int("candidates", len(exactMatches)),
			)
			return best, false, nil
		}
		// Apply kind filter — find first match of the right kind
		for _, sym := range exactMatches {
			if matchesKindFilter(sym, cfg.kindFilter) {
				logger.Info("Symbol resolution: exact match",
					slog.String("query", name),
					slog.String("resolved", sym.Name),
					slog.String("kind", sym.Kind.String()),
					slog.String("file", sym.FilePath),
					slog.Int("line", sym.StartLine),
				)
				return sym, false, nil
			}
		}
		// Exact matches exist but none pass the kind filter — fall through
		logger.Debug("Symbol resolution: exact matches found but none match kind filter",
			slog.String("query", name),
			slog.Int("total_matches", len(exactMatches)),
			slog.String("kind_filter", cfg.kindFilter.String()),
		)
	}

	// Step 2: Try Type.Method dot notation split (e.g., "Plot.render" → type "Plot", method "render")
	if strings.Contains(name, ".") {
		parts := strings.SplitN(name, ".", 2)
		typeName, methodName := parts[0], parts[1]
		if sym, err := resolveTypeDotMethod(ctx, index, typeName, methodName, logger); err == nil {
			// Apply kind filter to dot-notation result (resolveTypeDotMethod returns
			// Function/Method/Property; if caller wants types only, this should fall through)
			if matchesKindFilter(sym, cfg.kindFilter) {
				logger.Info("Symbol resolution: dot notation match",
					slog.String("query", name),
					slog.String("type", typeName),
					slog.String("method", methodName),
					slog.String("resolved", sym.Name),
					slog.String("file", sym.FilePath),
				)
				return sym, false, nil
			}
			logger.Debug("Symbol resolution: dot notation match rejected by kind filter",
				slog.String("query", name),
				slog.String("resolved_kind", sym.Kind.String()),
				slog.String("kind_filter", cfg.kindFilter.String()),
			)
		}

		// Step 3: IT-02 C-1 bare method name fallback (opt-in)
		// IT-05 R3-1: Collect all kind-filtered bare matches, then disambiguate using
		// the dot-notation type prefix. Previously, this returned the first match in
		// parse order (non-deterministic), causing "gin.Default" to resolve to
		// binding/binding.go:Default instead of gin.go:Default.
		if cfg.bareMethodFallback {
			bareMatches := index.GetByName(methodName)
			var filtered []*ast.Symbol
			for _, sym := range bareMatches {
				if matchesKindFilter(sym, cfg.kindFilter) {
					filtered = append(filtered, sym)
				}
			}
			if len(filtered) == 1 {
				logger.Info("Symbol resolution: bare method fallback",
					slog.String("query", name),
					slog.String("bare_name", methodName),
					slog.String("resolved", filtered[0].Name),
					slog.String("kind", filtered[0].Kind.String()),
					slog.String("file", filtered[0].FilePath),
				)
				return filtered[0], false, nil
			}
			if len(filtered) > 1 {
				best := pickBestBareCandidate(filtered, typeName)
				logger.Info("Symbol resolution: bare method fallback (disambiguated)",
					slog.String("query", name),
					slog.String("bare_name", methodName),
					slog.String("type_prefix", typeName),
					slog.String("resolved", best.Name),
					slog.String("file", best.FilePath),
					slog.Int("candidates", len(filtered)),
				)
				return best, false, nil
			}
		}
		// Fall through to fuzzy search if dot notation didn't work
	}

	// Step 4: Fuzzy search (slower, requires context timeout)
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

	// Filter fuzzy matches by configured kind filter
	var kindFiltered []*ast.Symbol
	for _, match := range fuzzyMatches {
		if matchesKindFilter(match, cfg.kindFilter) {
			kindFiltered = append(kindFiltered, match)
		}
	}

	// Log all matches (showing which were filtered)
	logger.Info("Symbol resolution: fuzzy search results",
		slog.String("query", name),
		slog.Int("total_matches", len(fuzzyMatches)),
		slog.Int("kind_filtered", len(kindFiltered)),
	)

	for i, match := range fuzzyMatches {
		if i < 8 { // Log top 8 matches in detail
			logger.Info("Symbol resolution: fuzzy match candidate",
				slog.Int("rank", i+1),
				slog.String("query", name),
				slog.String("matched_name", match.Name),
				slog.String("kind", match.Kind.String()),
				slog.Bool("matches_filter", matchesKindFilter(match, cfg.kindFilter)),
				slog.String("file", match.FilePath),
				slog.Int("line", match.StartLine),
			)
		}
	}

	// Check if we have any matches after kind filtering
	if len(kindFiltered) == 0 {
		logger.Debug("Symbol resolution: fuzzy search found matches but none pass kind filter",
			slog.String("query", name),
			slog.Int("total_matches", len(fuzzyMatches)),
			slog.String("kind_filter", cfg.kindFilter.String()),
		)
		return nil, false, fmt.Errorf("no symbol named '%s' found matching kind filter (found %d symbols of other kinds)", name, len(fuzzyMatches))
	}

	// Use first kind-filtered match (best score among matching symbols)
	selectedMatch := kindFiltered[0]
	logger.Info("Symbol resolution: selected fuzzy match",
		slog.String("query", name),
		slog.String("matched", selectedMatch.Name),
		slog.String("kind", selectedMatch.Kind.String()),
		slog.String("file", selectedMatch.FilePath),
		slog.Int("line", selectedMatch.StartLine),
		slog.Int("candidates", len(kindFiltered)),
	)

	return selectedMatch, true, nil
}

// ResolveMultipleFunctionsWithFuzzy resolves multiple function names,
// using fuzzy matching as fallback for each.
//
// Description:
//
//	Batch version of ResolveFunctionWithFuzzy for tools that accept multiple
//	targets (e.g., find_common_dependency, find_path). Passes through any
//	ResolveFuzzyOpt options to each individual resolution call.
//
// Inputs:
//   - ctx: Context for timeout control. Must not be nil.
//   - index: Symbol index to search. Must not be nil.
//   - names: Function names to resolve. Must not be empty.
//   - logger: Logger for debugging. Must not be nil.
//   - opts: Optional configuration passed to each ResolveFunctionWithFuzzy call.
//
// Outputs:
//   - []*ast.Symbol: Resolved symbols (length matches input names). Nil on error.
//   - []bool: Fuzzy match indicators (parallel to symbols). Nil on error.
//   - error: Non-nil if ANY symbol could not be found.
//
// Thread Safety: This function is safe for concurrent use.
//
// Example:
//
//	symbols, fuzzy, err := ResolveMultipleFunctionsWithFuzzy(ctx, index,
//	    []string{"Handler", "Middleware"}, logger, WithKindFilter(KindFilterCallable))
//	if err != nil {
//	    return fmt.Errorf("failed to resolve targets: %w", err)
//	}
func ResolveMultipleFunctionsWithFuzzy(
	ctx context.Context,
	index *index.SymbolIndex,
	names []string,
	logger *slog.Logger,
	opts ...ResolveFuzzyOpt,
) ([]*ast.Symbol, []bool, error) {
	symbols := make([]*ast.Symbol, 0, len(names))
	fuzzyFlags := make([]bool, 0, len(names))

	for i, name := range names {
		symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, index, name, logger, opts...)
		if err != nil {
			logger.Debug("Failed to resolve symbol in batch",
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
		// R3-P1d: Include Property symbols (Python @property, TS get/set accessors).
		if sym.Kind != ast.SymbolKindMethod && sym.Kind != ast.SymbolKindFunction && sym.Kind != ast.SymbolKindProperty {
			continue
		}

		// Strategy 1: Receiver field match (Go, JS set this)
		if sym.Receiver == typeName {
			candidates = append(candidates, sym)
			continue
		}

		// Strategy 1b (IT-05 R3-2): Receiver prefix match.
		// Handles cases where the user writes an abbreviated type name, e.g.,
		// "NestFactory.create" when the actual Receiver is "NestFactoryStatic".
		// HasPrefix avoids overly broad matching (Contains would match "act"
		// in "NestFactoryStatic"). The result goes into candidates[] and is
		// further disambiguated by pickBestCandidate, limiting false-positive risk.
		if sym.Receiver != "" && strings.HasPrefix(sym.Receiver, typeName) {
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

		// R3-P2a: Collect matching children, partition by overload status.
		// Prefer non-overload (real implementation) over @overload stubs.
		var nonOverloadMatch *ast.Symbol
		var overloadFallback *ast.Symbol
		for _, child := range typeSym.Children {
			// R3-P1d: Include Property symbols in class children search.
			if child.Name == methodName &&
				(child.Kind == ast.SymbolKindMethod || child.Kind == ast.SymbolKindFunction || child.Kind == ast.SymbolKindProperty) {
				if isOverloadStub(child) {
					if overloadFallback == nil {
						overloadFallback = child
					}
				} else {
					nonOverloadMatch = child
					break // Non-overload wins immediately
				}
			}
		}
		if nonOverloadMatch != nil {
			logger.Debug("resolveTypeDotMethod: matched via parent class children (non-overload)",
				slog.String("type", typeName),
				slog.String("method", methodName),
				slog.String("resolved_id", nonOverloadMatch.ID),
				slog.String("parent_id", typeSym.ID),
			)
			return nonOverloadMatch, nil
		}
		if overloadFallback != nil {
			logger.Debug("resolveTypeDotMethod: matched via parent class children (overload fallback)",
				slog.String("type", typeName),
				slog.String("method", methodName),
				slog.String("resolved_id", overloadFallback.ID),
				slog.String("parent_id", typeSym.ID),
			)
			return overloadFallback, nil
		}
	}

	// Strategy 4: Inheritance chain walk
	// If the type extends a parent, recursively search for the method on the parent.
	// IT-05 R5: After finding a parent method, check if the original (child) type
	// overrides it — if so, prefer the override.
	for _, typeSym := range typeMatches {
		if typeSym.Kind != ast.SymbolKindClass && typeSym.Kind != ast.SymbolKindStruct &&
			typeSym.Kind != ast.SymbolKindInterface && typeSym.Kind != ast.SymbolKindType {
			continue
		}
		if typeSym.Metadata == nil || typeSym.Metadata.Extends == "" {
			logger.Debug("resolveTypeDotMethod: Strategy 4 skipped (no Extends metadata)",
				slog.String("type", typeName),
				slog.String("symbol_id", typeSym.ID),
			)
			continue
		}

		// IT-06b Issue 4: Strip qualified name prefixes before recursive lookup.
		// Python base classes may be stored as "generic.NDFrame" but the index
		// stores symbols by bare name "NDFrame". Defense-in-depth — the parser
		// should also strip qualifiers (see python_parser.go IT-06b fix).
		parentTypeName := typeSym.Metadata.Extends
		if dotIdx := strings.LastIndex(parentTypeName, "."); dotIdx >= 0 {
			parentTypeName = parentTypeName[dotIdx+1:]
		}

		logger.Debug("resolveTypeDotMethod: trying inheritance chain",
			slog.String("type", typeName),
			slog.String("extends", parentTypeName),
		)
		parentSym, err := resolveTypeDotMethod(ctx, idx, parentTypeName, methodName, logger)
		if err == nil {
			// IT-05 R5 Fix: Check if original type overrides the parent method.
			if override := findChildOverride(idx, typeName, methodName, parentSym); override != nil {
				logger.Debug("resolveTypeDotMethod: found child override of parent method",
					slog.String("type", typeName),
					slog.String("method", methodName),
					slog.String("parent_method", parentSym.ID),
					slog.String("override", override.ID),
				)
				return override, nil
			}
			return parentSym, nil
		}
	}

	return nil, fmt.Errorf("no method '%s' found on type '%s'", methodName, typeName)
}

// findChildOverride checks if a type has a method that overrides a parent method.
//
// Description:
//
//	IT-05 R5: Used when resolveTypeDotMethod Strategy 4 walks up to a parent class
//	and finds a method there. Before returning the parent method, we check if the
//	original (child) type has its own version of the method (an override).
//
//	This handles cases like Plot.render where Plot extends Bindable, and
//	Bindable has render(), but Plot overrides it with its own render().
//
// Inputs:
//   - idx: Symbol index to search. Must not be nil.
//   - typeName: The original (child) type name (e.g., "Plot").
//   - methodName: The method name being resolved (e.g., "render").
//   - parentResult: The parent method that was found via inheritance walk.
//
// Outputs:
//   - *ast.Symbol: The child's override method, or nil if no override exists.
//
// Thread Safety: This function is safe for concurrent use.
func findChildOverride(
	idx *index.SymbolIndex,
	typeName string,
	methodName string,
	parentResult *ast.Symbol,
) *ast.Symbol {
	if idx == nil || parentResult == nil {
		return nil
	}

	// Search all methods named methodName
	methods := idx.GetByName(methodName)
	for _, m := range methods {
		if m.Kind != ast.SymbolKindMethod && m.Kind != ast.SymbolKindFunction &&
			m.Kind != ast.SymbolKindProperty {
			continue
		}
		// Skip the parent result itself
		if m.ID == parentResult.ID {
			continue
		}

		// Strategy 1: Receiver field match (Go, JS set this)
		if m.Receiver == typeName {
			return m
		}

		// Strategy 2: ID contains "typeName.methodName" (JS/TS)
		if strings.Contains(m.ID, typeName+"."+methodName) {
			return m
		}

		// Strategy 3: File base name matches type name (TypeScript/JavaScript).
		// IT-05 R6: In TS, parsers don't set Receiver on class methods, and the
		// ID is file-based (e.g., "src/components/plot.ts:293:render"). Check if
		// the file's base name (without extension) matches the type name exactly.
		// E.g., typeName="Plot", filePath="src/components/plot.ts" → base="plot" → match.
		// But typeName="Plot", filePath="src/plot_helper.ts" → base="plot_helper" → no match.
		if m.FilePath != "" && m.FilePath != parentResult.FilePath {
			baseName := fileBaseName(m.FilePath)
			if strings.EqualFold(baseName, typeName) {
				return m
			}
		}
	}
	return nil
}

// fileBaseName returns the base file name without extension.
// E.g., "src/components/plot.ts" → "plot", "handlers/user.go" → "user".
func fileBaseName(filePath string) string {
	// Get the last path component
	base := filePath
	if lastSlash := strings.LastIndex(filePath, "/"); lastSlash >= 0 {
		base = filePath[lastSlash+1:]
	}
	// Strip extension (handle .ts, .go, .py, .js, .tsx, .jsx, etc.)
	if dot := strings.Index(base, "."); dot >= 0 {
		base = base[:dot]
	}
	return base
}

// isOverloadStub returns true if the symbol is a Python @overload typing stub.
//
// Description:
//
//	Checks the symbol's Metadata.Decorators for "overload". These stubs exist
//	only for the type checker and have no body (or an ellipsis body). They should
//	be deprioritized in favor of the real implementation when resolving symbols
//	for call graph queries.
//
// Inputs:
//
//	sym - The symbol to check. May be nil (returns false).
//
// Outputs:
//
//	bool - True if the symbol has an @overload decorator.
//
// Thread Safety: This function is safe for concurrent use.
func isOverloadStub(sym *ast.Symbol) bool {
	if sym == nil || sym.Metadata == nil {
		return false
	}
	for _, dec := range sym.Metadata.Decorators {
		if dec == "overload" {
			return true
		}
	}
	return false
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
		// R3-P2a: Prefer non-overload over overload stub
		bestIsStub := isOverloadStub(best)
		cIsStub := isOverloadStub(c)
		if !cIsStub && bestIsStub {
			best = c
			continue
		}
		if cIsStub && !bestIsStub {
			continue
		}

		// R3-P2a: Prefer candidate with calls over empty calls (stub heuristic)
		bestHasCalls := len(best.Calls) > 0
		cHasCalls := len(c.Calls) > 0
		if cHasCalls && !bestHasCalls {
			best = c
			continue
		}
		if !cHasCalls && bestHasCalls {
			continue
		}

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

// pickBestBareCandidate selects the best symbol from bare method fallback candidates,
// using the dot-notation type prefix as a disambiguation signal.
//
// Description:
//
//	IT-05 R3-1: When BareMethodFallback produces multiple matches (e.g., GetByName("Default")
//	returns symbols from gin.go and binding/binding.go), this function prefers symbols whose
//	Receiver, FilePath, or ID contains the original type prefix. This ensures "gin.Default"
//	resolves to gin.go:Default rather than binding/binding.go:Default.
//
//	The algorithm partitions candidates into prefix-matches and non-prefix-matches, then
//	applies pickBestCandidate on the preferred set. If no prefix-match exists, falls back
//	to pickBestCandidate on the full set.
//
// Inputs:
//   - candidates: Non-empty, kind-filtered slice of candidate symbols (len >= 2).
//   - typePrefix: The type/package part from the dot-notation query (e.g., "gin" from "gin.Default").
//     May be empty, in which case pickBestCandidate is used directly.
//
// Outputs:
//   - *ast.Symbol: The best candidate. Never nil when input is non-empty.
//
// Thread Safety: This function is safe for concurrent use (stateless).
func pickBestBareCandidate(candidates []*ast.Symbol, typePrefix string) *ast.Symbol {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	if typePrefix == "" {
		return pickBestCandidate(candidates)
	}

	// Partition: prefer symbols where Receiver, FilePath, or ID contains the type prefix.
	// Use case-sensitive matching — type names are identifiers with specific casing.
	var prefixMatches []*ast.Symbol
	for _, sym := range candidates {
		if strings.Contains(sym.Receiver, typePrefix) ||
			strings.Contains(sym.FilePath, strings.ToLower(typePrefix)) ||
			strings.Contains(sym.ID, typePrefix) {
			prefixMatches = append(prefixMatches, sym)
		}
	}

	if len(prefixMatches) > 0 {
		return pickBestCandidate(prefixMatches)
	}
	return pickBestCandidate(candidates)
}

// kindSignificance returns a ranking score for a symbol kind, where higher is more
// significant. Used by pickMostSignificantSymbol to prefer classes and interfaces
// over fields and variables when resolving ambiguous names.
//
// Description:
//
//	IT-06 Bug 5: When KindFilterAny is used and multiple symbols share the same name,
//	the resolver must prefer higher-level constructs. For example, "Engine" should
//	resolve to the Engine class rather than a field named "Engine" in some interface.
//	"Request" should resolve to the Request object in lib/request.js rather than a
//	local variable named "Request" in a test file.
//
// Inputs:
//   - kind: The symbol kind to rank.
//
// Outputs:
//   - int: Significance score (higher = more significant). Range 0-10.
//
// Thread Safety: This function is safe for concurrent use (pure function).
func kindSignificance(kind ast.SymbolKind) int {
	switch kind {
	case ast.SymbolKindClass:
		return 10
	case ast.SymbolKindStruct:
		return 10
	case ast.SymbolKindInterface:
		return 9
	case ast.SymbolKindType:
		return 8
	case ast.SymbolKindEnum:
		return 7
	case ast.SymbolKindFunction:
		return 6
	case ast.SymbolKindMethod:
		return 5
	case ast.SymbolKindProperty:
		return 4
	case ast.SymbolKindConstant:
		return 3
	case ast.SymbolKindVariable:
		return 2
	case ast.SymbolKindField:
		return 1
	default:
		return 0
	}
}

// pickMostSignificantSymbol selects the symbol with the highest kind significance
// from a list of exact-name matches.
//
// Description:
//
//	IT-06 Bug 5: When multiple symbols share the same name and KindFilterAny is used,
//	this function picks the most architecturally significant one. Among equal significance,
//	prefers non-test files (files not containing "/test" or "_test") and shorter file paths.
//
// Inputs:
//   - symbols: Non-empty slice of symbols with the same name. Must not be empty.
//
// Outputs:
//   - *ast.Symbol: The most significant symbol. Never nil when input is non-empty.
//
// Thread Safety: This function is safe for concurrent use (stateless).
func pickMostSignificantSymbol(symbols []*ast.Symbol) *ast.Symbol {
	if len(symbols) == 1 {
		return symbols[0]
	}

	// IT-06c H-3: If there are @overload stubs mixed with a real implementation,
	// filter out the stubs first. Overload stubs (Python @overload) have no body
	// and zero callees — the real implementation should always be preferred.
	symbols = filterOutOverloadStubs(symbols)
	if len(symbols) == 1 {
		return symbols[0]
	}

	best := symbols[0]
	bestSig := kindSignificance(best.Kind)
	bestIsTest := isTestFile(best.FilePath)

	for _, sym := range symbols[1:] {
		sig := kindSignificance(sym.Kind)
		isTest := isTestFile(sym.FilePath)

		// Higher significance wins
		if sig > bestSig {
			best = sym
			bestSig = sig
			bestIsTest = isTest
			continue
		}
		if sig < bestSig {
			continue
		}

		// Equal significance: prefer non-test file over test file
		if !isTest && bestIsTest {
			best = sym
			bestSig = sig
			bestIsTest = isTest
			continue
		}
		if isTest && !bestIsTest {
			continue
		}

		// Equal significance, same test status: prefer shorter path (more central)
		if len(sym.FilePath) < len(best.FilePath) {
			best = sym
			bestSig = sig
			bestIsTest = isTest
		}
	}

	return best
}

// filterOutOverloadStubs removes Python @overload stubs when a non-overload
// implementation of the same name exists. If ALL symbols are overload stubs
// (or none are), returns the original slice unchanged.
//
// IT-06c H-3: Python @overload stubs are type-checking hints with `...` bodies
// and zero callees. The real implementation has the actual function body with callees.
// Example: pandas read_csv has 4 @overload stubs + 1 real implementation.
func filterOutOverloadStubs(symbols []*ast.Symbol) []*ast.Symbol {
	if len(symbols) <= 1 {
		return symbols
	}

	var nonOverloads []*ast.Symbol
	hasOverloads := false

	for _, sym := range symbols {
		if sym.Metadata != nil && sym.Metadata.IsOverload {
			hasOverloads = true
		} else {
			nonOverloads = append(nonOverloads, sym)
		}
	}

	// Only filter if there's a mix: some overloads AND some non-overloads.
	// If all are overloads or none are, return original.
	if !hasOverloads || len(nonOverloads) == 0 {
		return symbols
	}

	return nonOverloads
}

// isTestFile returns true if the file path looks like a test or benchmark file.
//
// Thread Safety: This function is safe for concurrent use (pure function).
func isTestFile(filePath string) bool {
	return strings.Contains(filePath, "/test") ||
		strings.HasPrefix(filePath, "test/") ||
		strings.Contains(filePath, "_test") ||
		strings.Contains(filePath, "/tests/") ||
		strings.HasPrefix(filePath, "tests/") ||
		strings.Contains(filePath, "/benchmark") ||
		strings.Contains(filePath, "/asv_bench/") ||
		strings.HasPrefix(filePath, "asv_bench/") ||
		strings.Contains(filePath, ".test.") ||
		strings.Contains(filePath, ".spec.")
}

// filterByPackageHint narrows a list of symbols to those whose FilePath, Package,
// or ID contains the given package hint. If no symbols match the hint, returns
// the original list unchanged (don't lose symbols just because the hint was wrong).
//
// Description:
//
//	IT-06c Bug C: When index.GetByName("Build") returns 11 symbols across different
//	packages, and the query contains "in hugolib", this function narrows the list
//	to symbols from the hugolib directory/package. The hint is matched case-insensitively
//	against FilePath, Package, and the directory component of the symbol's file path.
//
// Inputs:
//   - symbols: Non-empty slice of symbols to filter.
//   - hint: Package/directory name to match (e.g., "hugolib"). Must not be empty.
//   - logger: Logger for observability.
//   - toolName: Tool name for logging context.
//
// Outputs:
//   - []*ast.Symbol: Filtered symbols. Same as input if no matches found.
//
// Thread Safety: This function is safe for concurrent use (stateless).
func filterByPackageHint(symbols []*ast.Symbol, hint string, logger *slog.Logger, toolName string) []*ast.Symbol {
	if hint == "" || len(symbols) <= 1 {
		return symbols
	}

	lowerHint := strings.ToLower(hint)
	var matches []*ast.Symbol

	for _, sym := range symbols {
		lowerPath := strings.ToLower(sym.FilePath)
		lowerPkg := strings.ToLower(sym.Package)
		lowerID := strings.ToLower(sym.ID)

		// Match if hint appears in the file path, package name, or symbol ID.
		// Use directory-boundary matching to avoid false positives:
		// "hugolib" should match "hugolib/" or "/hugolib/" but not "nothugolib".
		if containsPackageSegment(lowerPath, lowerHint) ||
			containsPackageSegment(lowerPkg, lowerHint) ||
			containsPackageSegment(lowerID, lowerHint) {
			matches = append(matches, sym)
		}
	}

	if len(matches) == 0 {
		logger.Debug("IT-06c: package hint matched no symbols, using all",
			slog.String("tool", toolName),
			slog.String("hint", hint),
			slog.Int("total", len(symbols)),
		)
		return symbols
	}

	logger.Info("IT-06c: package hint disambiguated symbols",
		slog.String("tool", toolName),
		slog.String("hint", hint),
		slog.Int("before", len(symbols)),
		slog.Int("after", len(matches)),
	)
	return matches
}

// containsPackageSegment checks if haystack contains needle as a path/package segment.
// Returns true if needle appears at a directory boundary (preceded by "/" or start-of-string
// and followed by "/" or end-of-string or "_" or ".").
// This avoids false positives like "nothugolib" matching "hugolib".
func containsPackageSegment(haystack, needle string) bool {
	idx := 0
	for {
		pos := strings.Index(haystack[idx:], needle)
		if pos < 0 {
			return false
		}
		absPos := idx + pos
		endPos := absPos + len(needle)

		// Check boundary before the match
		beforeOK := absPos == 0 || haystack[absPos-1] == '/' || haystack[absPos-1] == '.'
		// Check boundary after the match
		afterOK := endPos >= len(haystack) || haystack[endPos] == '/' || haystack[endPos] == '_' || haystack[endPos] == '.' || haystack[endPos] == ':'

		if beforeOK && afterOK {
			return true
		}

		// Move past this match and try again
		idx = absPos + 1
		if idx >= len(haystack) {
			return false
		}
	}
}
