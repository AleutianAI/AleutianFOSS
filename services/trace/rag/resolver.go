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
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// maxPackagesInContext limits the number of package names included in the extraction context.
const maxPackagesInContext = 30

var tracer = otel.Tracer("rag")

// StructuralResolver resolves query tokens against the SymbolIndex and package names.
//
// This is Layer 1 of the three-layer RAG resolution. It handles exact and prefix
// matches with O(1) lookups, covering 60-70% of entity resolution queries.
//
// Thread Safety: Safe for concurrent use after construction.
type StructuralResolver struct {
	index *index.SymbolIndex

	// packageNames is a sorted list of package names from the graph.
	// Built once from the SymbolIndex on construction.
	packageNames []string

	// packageSet provides O(1) exact lookup for package names.
	packageSet map[string]bool

	// packageByLastSegment maps the last path segment to full package paths.
	// "materials" → ["pkg/materials"], "render" → ["pkg/render", "internal/render"]
	packageByLastSegment map[string][]string

	// symbolCountByPackage caches the number of symbols per package.
	// Built once at construction for O(1) lookup in topPackages().
	symbolCountByPackage map[string]int
}

// NewStructuralResolver creates a resolver from a SymbolIndex.
//
// Description:
//
//	Extracts unique package names from all symbols in the index and builds
//	lookup structures for exact, prefix, and last-segment matching.
//
// Inputs:
//
//	idx - The symbol index to resolve against. Must not be nil.
//
// Outputs:
//
//	*StructuralResolver - Ready to resolve queries.
//
// Thread Safety: Safe for concurrent use after construction.
func NewStructuralResolver(idx *index.SymbolIndex) *StructuralResolver {
	pkgSet := make(map[string]bool)
	pkgByLast := make(map[string][]string)
	symCountByPkg := make(map[string]int)

	// Extract unique packages from all symbols and count symbols per package.
	for _, kind := range allSymbolKinds() {
		for _, sym := range idx.GetByKind(kind) {
			pkg := sym.Package
			if pkg == "" {
				continue
			}
			symCountByPkg[pkg]++
			if !pkgSet[pkg] {
				pkgSet[pkg] = true
				lastSeg := lastPathSegment(pkg)
				if lastSeg != "" {
					pkgByLast[lastSeg] = append(pkgByLast[lastSeg], pkg)
				}
			}
		}
	}

	// Sort package names for deterministic output.
	names := make([]string, 0, len(pkgSet))
	for pkg := range pkgSet {
		names = append(names, pkg)
	}
	sort.Strings(names)

	return &StructuralResolver{
		index:                idx,
		packageNames:         names,
		packageSet:           pkgSet,
		packageByLastSegment: pkgByLast,
		symbolCountByPackage: symCountByPkg,
	}
}

// Resolve resolves a query into grounded entities from the code graph.
//
// Description:
//
//	Tokenizes the query, then checks each token against:
//	1. Package names (exact match, last-segment match, prefix match)
//	2. Symbol names (exact match via SymbolIndex)
//	3. File paths (suffix match via SymbolIndex)
//
//	Returns resolved entities with confidence scores. Structural matches
//	get confidence 1.0 (exact) or 0.9 (prefix/segment).
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	query - Natural language query.
//
// Outputs:
//
//	*ExtractionContext - Resolved entities and graph metadata for the param extractor.
//
// Thread Safety: Safe for concurrent use.
func (r *StructuralResolver) Resolve(ctx context.Context, query string) *ExtractionContext {
	ec, _ := r.ResolveDetailed(ctx, query)
	return ec
}

// StructuralResult holds the output of structural resolution including unresolved tokens.
type StructuralResult struct {
	// UnresolvedTokens are tokens that couldn't be matched structurally.
	// These are candidates for semantic resolution (Layer 2).
	UnresolvedTokens []string

	// ResolvedSet contains the resolved values for deduplication.
	ResolvedSet map[string]bool
}

// ResolveDetailed resolves a query and also returns unresolved tokens for Layer 2.
//
// Description:
//
//	Same as Resolve but additionally returns tokens that couldn't be matched
//	structurally. The CombinedResolver uses these as input for semantic search.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	query - Natural language query.
//
// Outputs:
//
//	*ExtractionContext - Resolved entities and graph metadata.
//	*StructuralResult - Full result including unresolved tokens.
//
// Thread Safety: Safe for concurrent use.
func (r *StructuralResolver) ResolveDetailed(ctx context.Context, query string) (*ExtractionContext, *StructuralResult) {
	ctx, span := tracer.Start(ctx, "rag.StructuralResolver.Resolve")
	defer span.End()

	tokens := TokenizeQuery(query)

	var resolved []ResolvedEntity
	var unresolved []string
	resolvedSet := make(map[string]bool)

	for _, token := range tokens {
		if ctx.Err() != nil {
			break
		}

		matched := false

		// Try package resolution.
		if entity, ok := r.resolvePackage(token); ok {
			if !resolvedSet[entity.Resolved] {
				resolvedSet[entity.Resolved] = true
				resolved = append(resolved, entity)
			}
			matched = true
		}

		// Try symbol resolution (even if package matched — same token can be both).
		if !matched {
			if entity, ok := r.resolveSymbol(token); ok {
				if !resolvedSet[entity.Resolved] {
					resolvedSet[entity.Resolved] = true
					resolved = append(resolved, entity)
				}
				matched = true
			}
		}

		// Try file path resolution.
		if !matched {
			if entity, ok := r.resolveFile(token); ok {
				if !resolvedSet[entity.Resolved] {
					resolvedSet[entity.Resolved] = true
					resolved = append(resolved, entity)
				}
				matched = true
			}
		}

		if !matched && looksLikeIdentifier(token) {
			unresolved = append(unresolved, token)
		}
	}

	span.SetAttributes(
		attribute.Int("rag.tokens", len(tokens)),
		attribute.Int("rag.resolved", len(resolved)),
		attribute.Int("rag.unresolved", len(unresolved)),
	)
	if len(resolved) > 0 {
		slog.Info("CRS-25: Structural resolution",
			slog.Int("tokens", len(tokens)),
			slog.Int("resolved", len(resolved)),
		)
	}

	ec := &ExtractionContext{
		ResolvedEntities: resolved,
		PackageNames:     r.topPackages(),
		SymbolCount:      r.index.Stats().TotalSymbols,
	}

	return ec, &StructuralResult{
		UnresolvedTokens: unresolved,
		ResolvedSet:      resolvedSet,
	}
}

// resolvePackage tries to match a token to a package name.
func (r *StructuralResolver) resolvePackage(token string) (ResolvedEntity, bool) {
	lower := strings.ToLower(token)

	// O(1) exact match first.
	if r.packageSet[token] {
		return ResolvedEntity{
			Raw:        token,
			Kind:       "package",
			Resolved:   token,
			Confidence: 1.0,
			Layer:      "structural",
		}, true
	}
	// Case-insensitive fallback (O(P), rare path — only when casing differs).
	for _, pkg := range r.packageNames {
		if strings.EqualFold(pkg, token) {
			return ResolvedEntity{
				Raw:        token,
				Kind:       "package",
				Resolved:   pkg,
				Confidence: 1.0,
				Layer:      "structural",
			}, true
		}
	}

	// Last-segment match: "materials" → "pkg/materials".
	if matches, ok := r.packageByLastSegment[lower]; ok && len(matches) > 0 {
		entity := ResolvedEntity{
			Raw:        token,
			Kind:       "package",
			Resolved:   matches[0],
			Confidence: 0.9,
			Layer:      "structural",
		}
		if len(matches) > 1 {
			entity.Candidates = matches
			entity.Confidence = 0.8 // ambiguous
		}
		return entity, true
	}

	// Prefix match: "mat" → "materials" (only if unambiguous).
	var prefixMatches []string
	for _, pkg := range r.packageNames {
		lastSeg := lastPathSegment(pkg)
		if strings.HasPrefix(strings.ToLower(lastSeg), lower) && len(lower) >= 3 {
			prefixMatches = append(prefixMatches, pkg)
		}
	}
	if len(prefixMatches) == 1 {
		return ResolvedEntity{
			Raw:        token,
			Kind:       "package",
			Resolved:   prefixMatches[0],
			Confidence: 0.85,
			Layer:      "structural",
		}, true
	}

	return ResolvedEntity{}, false
}

// resolveSymbol tries to match a token to a symbol name.
func (r *StructuralResolver) resolveSymbol(token string) (ResolvedEntity, bool) {
	// Only try symbol resolution for tokens that look like identifiers.
	if !looksLikeIdentifier(token) {
		return ResolvedEntity{}, false
	}

	symbols := r.index.GetByName(token)
	if len(symbols) == 0 {
		return ResolvedEntity{}, false
	}

	// Prefer exported symbols.
	var exported, unexported []string
	for _, sym := range symbols {
		loc := sym.FilePath + ":" + sym.Name
		if sym.Exported {
			exported = append(exported, loc)
		} else {
			unexported = append(unexported, loc)
		}
	}

	best := symbols[0]
	for _, sym := range symbols {
		if sym.Exported {
			best = sym
			break
		}
	}

	entity := ResolvedEntity{
		Raw:        token,
		Kind:       best.Kind.String(),
		Resolved:   best.Name,
		Confidence: 1.0,
		Layer:      "structural",
	}

	if best.Package != "" {
		entity.Resolved = best.Package + "." + best.Name
	}

	allLocs := make([]string, 0, len(exported)+len(unexported))
	allLocs = append(allLocs, exported...)
	allLocs = append(allLocs, unexported...)
	if len(allLocs) > 1 {
		entity.Candidates = allLocs
		entity.Confidence = 0.85 // ambiguous — multiple symbols with same name
	}

	return entity, true
}

// resolveFile tries to match a token to a file path.
func (r *StructuralResolver) resolveFile(token string) (ResolvedEntity, bool) {
	// Only try file resolution for tokens that look like file paths.
	if !strings.Contains(token, ".") && !strings.Contains(token, "/") {
		return ResolvedEntity{}, false
	}

	symbols := r.index.GetByFile(token)
	if len(symbols) > 0 {
		return ResolvedEntity{
			Raw:        token,
			Kind:       "file",
			Resolved:   token,
			Confidence: 1.0,
			Layer:      "structural",
		}, true
	}

	return ResolvedEntity{}, false
}

// topPackages returns the top N package names by symbol count.
func (r *StructuralResolver) topPackages() []string {
	if len(r.packageNames) <= maxPackagesInContext {
		return r.packageNames
	}

	type pkgCount struct {
		name  string
		count int
	}
	counts := make([]pkgCount, 0, len(r.packageNames))
	for _, pkg := range r.packageNames {
		counts = append(counts, pkgCount{pkg, r.symbolCountByPackage[pkg]})
	}

	sort.Slice(counts, func(i, j int) bool {
		return counts[i].count > counts[j].count
	})

	result := make([]string, 0, maxPackagesInContext)
	for i := 0; i < maxPackagesInContext && i < len(counts); i++ {
		result = append(result, counts[i].name)
	}
	sort.Strings(result)
	return result
}

// PackageNames returns all known package names.
//
// Thread Safety: Safe for concurrent use.
func (r *StructuralResolver) PackageNames() []string {
	result := make([]string, len(r.packageNames))
	copy(result, r.packageNames)
	return result
}

// lastPathSegment returns the last component of a path.
// "pkg/materials" → "materials", "services/trace/graph" → "graph".
func lastPathSegment(path string) string {
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return strings.ToLower(parts[i])
		}
	}
	return ""
}

// looksLikeIdentifier returns true if the token could be a code identifier.
func looksLikeIdentifier(token string) bool {
	if len(token) < 2 {
		return false
	}
	// Must start with a letter or underscore.
	first := rune(token[0])
	if !unicode.IsLetter(first) && first != '_' {
		return false
	}
	// Must contain only letters, digits, and underscores.
	for _, r := range token {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// allSymbolKinds returns all AST symbol kinds for iteration.
var allSymbolKinds = sync.OnceValue(func() []ast.SymbolKind {
	return []ast.SymbolKind{
		ast.SymbolKindFunction,
		ast.SymbolKindMethod,
		ast.SymbolKindClass,
		ast.SymbolKindInterface,
		ast.SymbolKindStruct,
		ast.SymbolKindType,
		ast.SymbolKindVariable,
		ast.SymbolKindConstant,
		ast.SymbolKindField,
		ast.SymbolKindPackage,
	}
})
