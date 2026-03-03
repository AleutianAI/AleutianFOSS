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

// execute_conceptual.go contains the conceptual symbol resolution pipeline
// (D3 prune/annotate/validate) and its helpers. Extracted from execute_helpers.go
// as part of D3a decomposition.

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/config"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// conceptualResolutionResult holds the output of resolveConceptualName.
//
// D3: Returns a struct instead of a plain string so callers can check
// whether the validator overrode the LLM pick and emit CRS FailureEvents.
type conceptualResolutionResult struct {
	// Resolved is the final symbol name to use.
	Resolved string

	// Overridden is true if validateTierSelection replaced the LLM's pick.
	Overridden bool

	// LLMPick is the symbol name the LLM originally returned (before validation).
	LLMPick string

	// LLMPickTier is the tier of the LLM's original pick (0, 1, or 2).
	LLMPickTier int
}

// conceptualResolutionOpts configures optional behavior for resolveConceptualName.
//
// D3c: Replaces the variadic *graph.GraphAnalytics parameter to support
// additional options (reachability filtering, source context) without
// growing the parameter list further.
type conceptualResolutionOpts struct {
	// Analytics provides graph analytics for edge annotation and reachability checks.
	Analytics *graph.GraphAnalytics

	// FromSymbolID is the resolved from-side symbol ID for reachability pre-filtering.
	// D3c Option 3: Only used for find_path to-side resolution. When set,
	// candidates unreachable from this symbol are removed before LLM call.
	FromSymbolID string

	// SourceContext describes the source function for the LLM prompt.
	// D3c Option 5: e.g., "Bind (method on Context in context.go:757)".
	// When non-empty, prepended to the LLM prompt so the model knows what
	// the destination should connect to.
	SourceContext string

	// AcceptAnyKind when true means non-callable index matches (types, structs,
	// interfaces) are accepted as valid. Used by find_symbol, find_implementations,
	// and find_references which target any symbol kind, not just functions/methods.
	// IT_CRS_01: Without this, those tools would incorrectly enter LLM resolution
	// when the name matches a type in the index.
	AcceptAnyKind bool
}

func resolveConceptualName(
	ctx context.Context,
	name string,
	query string,
	idx *index.SymbolIndex,
	extractor agent.ParamExtractor,
	session *agent.Session,
	opts conceptualResolutionOpts,
) conceptualResolutionResult {
	unchanged := conceptualResolutionResult{Resolved: name}
	if idx == nil || extractor == nil || !extractor.IsEnabled() || name == "" {
		return unchanged
	}

	// A resolvable name contains ":" (full ID) or is dot-notation (Type.Method).
	isDotNotation := strings.Contains(name, ".") && !strings.Contains(name, "/")

	// Dot-notation names like "Table.layout" also need resolution — the LLM
	// hallucinated a compound form that doesn't exist in the index.
	if strings.Contains(name, ":") {
		return unchanged // Already a full ID
	}

	// Try resolving via the index — if it succeeds, the name is real.
	// IT-12 Rev 4: Only exit early if at least one match is a callable kind
	// (function/method). If all matches are types/structs/interfaces, continue
	// to LLM resolution which will find a better function-level starting point
	// for call chain queries. For example, "Site" in Hugo matches a struct and
	// a getter method, but the user asking about "site initialization" needs
	// a function like "newHugoSites" or "NewSite".
	if !isDotNotation {
		syms := idx.GetByName(name)
		if len(syms) > 0 {
			// IT_CRS_01: When AcceptAnyKind is set, any index match is valid.
			// find_symbol, find_implementations, and find_references target types/interfaces
			// as well as functions, so non-callable matches should not trigger LLM resolution.
			if opts.AcceptAnyKind {
				return unchanged
			}
			hasCallable := false
			for _, sym := range syms {
				if sym.Kind == ast.SymbolKindFunction || sym.Kind == ast.SymbolKindMethod {
					hasCallable = true
					break
				}
			}
			if hasCallable {
				return unchanged // Name exists in index with callable symbols
			}
			slog.Debug("IT-12 Rev 4: name exists but only as non-callable kinds, continuing resolution",
				slog.String("name", name),
				slog.Int("matches", len(syms)),
			)
		}
	} else {
		// For dot-notation (e.g., "Scene.constructor"), try the full form first.
		syms := idx.GetByName(name)
		if len(syms) > 0 {
			return unchanged
		}
		// IT-R2d: Check if the bare method part exists in the index.
		// If it does, return the ORIGINAL dot-notation name — NOT the bare part.
		// The tool-side ResolveFunctionCandidates handles dot-notation correctly
		// via resolveTypeDotMethod(Type, Method) which uses Receiver filtering.
		// Stripping the type prefix here (e.g., "Scene.constructor" → "constructor")
		// loses the disambiguation context, causing "constructor" to resolve to
		// whichever class's constructor ranks highest (e.g., Node instead of Scene).
		parts := strings.SplitN(name, ".", 2)
		if len(parts) == 2 {
			syms = idx.GetByName(parts[1])
			if len(syms) > 0 {
				return unchanged // Preserve dot-notation for tool-side resolution
			}
		}
	}

	// Name not found — apply conceptual resolution.
	// IT-12 Rev 2: Use the hallucinated name itself as the primary keyword source
	// (e.g., "Table.layout" → ["table", "layout"]). This ensures that when find_path
	// resolves From and To independently, each gets a DIFFERENT candidate pool.
	// Previously, both used tokenizeQueryKeywords(query) on the full query, producing
	// identical candidates and causing the LLM to pick the same symbol for both.
	// We combine name-derived keywords with query keywords for broader coverage,
	// but name keywords come first for priority.
	// IT-R2c: Split camelCase before replacing dots/underscores.
	// LLM-hallucinated names like "sceneGraphUpdate" must become
	// "scene Graph Update" so tokenizeQueryKeywords produces 3 tokens
	// instead of 1, enabling synonym expansion and proper tiering.
	nameForKeywords := splitCamelCase(name)
	nameForKeywords = strings.ReplaceAll(nameForKeywords, ".", " ")
	nameForKeywords = strings.ReplaceAll(nameForKeywords, "_", " ")
	nameTokens := tokenizeQueryKeywords(nameForKeywords)
	nameKeywords := expandConceptSynonyms(nameTokens)
	queryKeywords := tokenizeQueryKeywords(query)

	// IT-12 Rev 5e: Extract domain nouns and concept values early so we can
	// generate compound search keywords BEFORE the candidate search.
	// Without this, searching for "render" in Hugo returns 25 exact-match
	// Render methods, and "renderPages" (a prefix match) gets truncated.
	domainNouns := extractDomainNouns(nameTokens)
	conceptValues := extractConceptValues(nameTokens)

	// IT-12 Rev 5e: Generate compound keywords from conceptValue+domainNoun.
	// e.g., domainNouns=["page"], conceptValues=["render","draw",...] →
	// compound keywords: ["renderpage", "drawpage", "paintpage", ...].
	// Searching for "renderpage" finds "renderPages" via prefix match,
	// which individual keyword "render" misses when exact Render matches
	// consume all 25 slots.
	if len(domainNouns) > 0 && len(conceptValues) > 0 {
		seen := make(map[string]bool)
		for _, kw := range nameKeywords {
			seen[strings.ToLower(kw)] = true
		}
		for _, cv := range conceptValues {
			for _, dn := range domainNouns {
				compound := cv + dn // e.g., "render" + "page" → "renderpage"
				if !seen[compound] {
					seen[compound] = true
					nameKeywords = append(nameKeywords, compound)
				}
			}
		}
	}

	// IT-12 Rev 4: Search name-derived keywords first. Only add query keywords
	// if name keywords produce fewer than 3 candidates. This prevents "menu assembly"
	// from being diluted by query keywords like "site" which pull in unrelated
	// symbols and cause the LLM to pick the wrong candidate for both endpoints.
	var symCandidates []agent.SymbolCandidate
	if len(nameKeywords) > 0 {
		symCandidates = searchSymbolCandidates(ctx, idx, nameKeywords, 25)
	}
	if len(symCandidates) < 3 {
		// Not enough from name alone — add query keywords for broader coverage.
		seen := make(map[string]bool)
		for _, kw := range nameKeywords {
			seen[kw] = true
		}
		var extraKeywords []string
		for _, kw := range queryKeywords {
			if !seen[kw] {
				extraKeywords = append(extraKeywords, kw)
				seen[kw] = true
			}
		}
		if len(extraKeywords) > 0 {
			extraCandidates := searchSymbolCandidates(ctx, idx, extraKeywords, 25)
			symCandidates = append(symCandidates, extraCandidates...)
		}
	}
	if len(nameKeywords) == 0 {
		// Name had no usable keywords — use query keywords directly.
		symCandidates = searchSymbolCandidates(ctx, idx, queryKeywords, 25)
	}
	if len(symCandidates) == 0 {
		return unchanged
	}

	// IT-12 Rev 4: Annotate candidates with edge counts from the graph.
	// This gives the LLM a strong signal: Build (47 calls out) vs Site (1 call out).
	// Functions with more edges are better starting points for path/chain queries.
	ga := opts.Analytics
	if ga != nil {
		for i := range symCandidates {
			syms := idx.GetByName(symCandidates[i].Name)
			for _, sym := range syms {
				if sym.FilePath == symCandidates[i].FilePath && sym.StartLine == symCandidates[i].Line {
					if node, ok := ga.GetNode(sym.ID); ok {
						symCandidates[i].OutEdges = len(node.Outgoing)
						symCandidates[i].InEdges = len(node.Incoming)
					}
					break
				}
			}
		}
	}

	// IT-12 Rev 5c: Log domain nouns and tier breakdown for debugging.
	// domainNouns and conceptValues were extracted before candidate search (Rev 5e).
	tier0Count, tier1Count := 0, 0
	for _, c := range symCandidates {
		switch candidateTier(c, domainNouns, conceptValues) {
		case 0:
			tier0Count++
		case 1:
			tier1Count++
		}
	}
	slog.Info("IT-12: domain noun extraction",
		slog.String("hallucinated", name),
		slog.Any("name_tokens", nameTokens),
		slog.Any("domain_nouns", domainNouns),
		slog.Any("concept_values", conceptValues),
		slog.Int("total_candidates", len(symCandidates)),
		slog.Int("tier0_count", tier0Count),
		slog.Int("tier1_count", tier1Count),
		slog.Int("tier2_count", len(symCandidates)-tier0Count-tier1Count),
	)

	// IT-12 Rev 5d / D3c: Three-level sort — tier → concept exactness → total edges.
	// Tier 0 (domain+concept) first, then tier 1, then tier 2.
	// D3c: Within same tier, prefer exact concept matches (exactness=3) over prefix (2)
	// over contains (1). This ensures "validate" (exact synonym) sorts above
	// "validateHeader" (prefix match) within tier1.
	sort.SliceStable(symCandidates, func(i, j int) bool {
		tierI := candidateTier(symCandidates[i], domainNouns, conceptValues)
		tierJ := candidateTier(symCandidates[j], domainNouns, conceptValues)
		if tierI != tierJ {
			return tierI < tierJ // lower tier = better
		}
		// D3c: Within same tier, prefer exact concept matches
		ei := conceptExactnessScore(symCandidates[i], conceptValues)
		ej := conceptExactnessScore(symCandidates[j], conceptValues)
		if ei != ej {
			return ei > ej // higher exactness = better
		}
		// Tiebreaker: total edges
		totalI := symCandidates[i].OutEdges + symCandidates[i].InEdges
		totalJ := symCandidates[j].OutEdges + symCandidates[j].InEdges
		return totalI > totalJ
	})

	// D3: Prune candidates by tier before sending to LLM.
	symCandidates = pruneCandidatesByTier(symCandidates, domainNouns, conceptValues)

	// Record CRS TraceStep for pruning (nil-safe)
	if session != nil {
		session.RecordTraceStep(crs.TraceStep{
			Action: "conceptual_prune",
			Tool:   "resolve_conceptual",
			Metadata: map[string]string{
				"hallucinated":   name,
				"pruned_count":   fmt.Sprintf("%d", len(symCandidates)),
				"original_tier0": fmt.Sprintf("%d", tier0Count),
				"original_tier1": fmt.Sprintf("%d", tier1Count),
			},
		})
	}

	// D3c Option 3: Remove unreachable candidates (find_path to-side only).
	if opts.FromSymbolID != "" && opts.Analytics != nil {
		symCandidates = filterByReachability(ctx, symCandidates, opts.FromSymbolID,
			opts.Analytics, idx, 5)
	}

	// D3c Option 7: Auto-pick when exactly one candidate is an exact concept synonym.
	// Fires AFTER reachability filter so unreachable exact matches are removed first.
	if autoPick, ok := autoPickExactConceptMatch(symCandidates, conceptValues, domainNouns); ok {
		slog.Info("D3c: auto-picked exact concept synonym match",
			slog.String("picked", autoPick.Name),
			slog.String("hallucinated", name))
		if session != nil {
			session.RecordTraceStep(crs.TraceStep{
				Action: "conceptual_auto_pick",
				Tool:   "resolve_conceptual",
				Metadata: map[string]string{
					"hallucinated": name,
					"auto_picked":  autoPick.Name,
				},
			})
		}
		return conceptualResolutionResult{Resolved: autoPick.Name}
	}

	// D3: Recount tier boundaries after pruning for the annotated prompt.
	prunedTier0Count := 0
	prunedTier1Count := 0
	for _, c := range symCandidates {
		switch candidateTier(c, domainNouns, conceptValues) {
		case 0:
			prunedTier0Count++
		case 1:
			prunedTier1Count++
		}
	}

	slog.Info("D3: post-prune tier counts",
		slog.String("hallucinated", name),
		slog.Int("pruned_total", len(symCandidates)),
		slog.Int("pruned_tier0", prunedTier0Count),
		slog.Int("pruned_tier1", prunedTier1Count),
	)

	// IT-12 Rev 2: Include the specific hallucinated concept in the query
	// so the LLM knows which endpoint to resolve (e.g., "Table layout" vs
	// "Axis rendering" for a find_path query).
	resolveQuery := fmt.Sprintf("Resolve the concept '%s' from: %s", name, query)
	resolved, err := extractor.ResolveConceptualSymbol(ctx, resolveQuery, symCandidates,
		prunedTier0Count, prunedTier1Count, opts.SourceContext)
	if err != nil {
		slog.Warn("IT-12: conceptual symbol resolution failed",
			slog.String("hallucinated", name),
			slog.String("error", err.Error()),
		)
		return unchanged
	}
	if resolved == "" {
		return unchanged
	}

	// D3: Post-LLM validation — override if tier0 candidate is clearly better.
	validated, overridden := validateTierSelection(resolved, symCandidates, domainNouns, conceptValues)

	// Determine the LLM pick's tier for reporting
	llmPickTier := 2
	for _, c := range symCandidates {
		if c.Name == resolved {
			llmPickTier = candidateTier(c, domainNouns, conceptValues)
			break
		}
	}

	// Record CRS TraceStep for validation (nil-safe)
	if session != nil {
		session.RecordTraceStep(crs.TraceStep{
			Action: "conceptual_validate",
			Tool:   "resolve_conceptual",
			Metadata: map[string]string{
				"hallucinated":  name,
				"llm_pick":      resolved,
				"llm_pick_tier": fmt.Sprintf("%d", llmPickTier),
				"validated":     validated,
				"overridden":    fmt.Sprintf("%t", overridden),
			},
		})
	}

	slog.Info("IT-12: conceptual symbol resolution replaced hallucinated name",
		slog.String("hallucinated", name),
		slog.String("resolved", validated),
		slog.String("llm_pick", resolved),
		slog.Bool("overridden", overridden),
		slog.Int("candidates", len(symCandidates)),
	)
	return conceptualResolutionResult{
		Resolved:    validated,
		Overridden:  overridden,
		LLMPick:     resolved,
		LLMPickTier: llmPickTier,
	}
}

func tokenizeQueryKeywords(query string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "to": true, "from": true,
		"in": true, "of": true, "for": true, "and": true, "or": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"this": true, "that": true, "these": true, "those": true,
		"show": true, "find": true, "get": true, "list": true,
		"what": true, "how": true, "where": true, "which": true,
		"call": true, "chain": true, "through": true, "between": true,
		"codebase": true, "code": true, "function": true, "method": true,
		"class": true, "type": true, "any": true, "all": true,
		"circular": true, "dependency": true, "dependencies": true,
	}

	words := strings.Fields(strings.ToLower(query))
	var keywords []string
	seen := make(map[string]bool)

	for _, w := range words {
		// Strip punctuation
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(w) < 3 || stopWords[w] || seen[w] {
			continue
		}
		// Strip -ing suffix to get root form for better index matching.
		// "assigning" → "assign", "rendering" → "render"
		// Keep both the root and the full word as keywords.
		if strings.HasSuffix(w, "ing") && len(w) > 6 {
			root := strings.TrimSuffix(w, "ing")
			if !seen[root] {
				keywords = append(keywords, root)
				seen[root] = true
			}
		}
		if !seen[w] {
			keywords = append(keywords, w)
			seen[w] = true
		}
	}

	return keywords
}

// getConceptSynonyms returns the concept synonym mappings loaded from
// config/concept_synonyms.yaml. Uses sync.Once internally for thread safety.
//
// To modify the synonym mappings, edit services/trace/config/concept_synonyms.yaml.
// See that file's header comments for editing guidelines and testing instructions.
func getConceptSynonyms() map[string][]string {
	return config.MustLoadConceptSynonyms()
}

// expandConceptSynonyms takes tokenized keywords and expands conceptual nouns
// into their function name verb equivalents using config/concept_synonyms.yaml.
// For example, ["site", "initialization"] expands to include "init", "new",
// "build", "setup", etc.
//
// This ensures that when a user says "site initialization", the search finds
// functions like Build, NewSite, initSite — not just the Site struct getter.
//
// To modify the mappings, edit services/trace/config/concept_synonyms.yaml.
//
// # Thread Safety
//
// Safe for concurrent use (config loaded via sync.Once).
func expandConceptSynonyms(keywords []string) []string {
	synonymMap := getConceptSynonyms()
	seen := make(map[string]bool, len(keywords))
	expanded := make([]string, 0, len(keywords)*2)
	for _, kw := range keywords {
		if seen[kw] {
			continue
		}
		seen[kw] = true
		expanded = append(expanded, kw)
		if synonyms, ok := synonymMap[kw]; ok {
			for _, syn := range synonyms {
				if !seen[syn] {
					seen[syn] = true
					expanded = append(expanded, syn)
				}
			}
		}
	}
	return expanded
}

// extractDomainNouns identifies domain-specific nouns from a hallucinated name
// by removing tokens that are concept synonym keys.
//
// Description:
//
//	When a user says "menu assembly", "assembly" is a concept synonym key (maps
//	to verbs like assemble, build, compose). "menu" is NOT a concept key — it's
//	a domain noun describing WHAT is being assembled. Domain nouns are strong
//	signals for candidate ranking: assembleMenus contains "menu" and should rank
//	above Build which only matches via the "build" synonym.
//
// Inputs:
//   - nameTokens: Lowercased tokens from the hallucinated name (output of
//     tokenizeQueryKeywords). Must not contain concept-synonym-expanded tokens —
//     pass the pre-expansion tokenization.
//
// Outputs:
//   - []string: Lowercased domain nouns. Returns nil if all tokens are concept keys
//     or input is empty.
//
// Limitations:
//   - Only checks top-level concept synonym keys and their -ing-stripped roots.
//     A token that is a synonym VALUE (e.g., "build" under the "builder" key) is
//     treated as a domain noun.
//   - Short domain nouns (3 chars, e.g., "log") may false-positive on substring
//     matching in candidateTier. Mitigated by tokenizeQueryKeywords filtering
//     tokens < 3 chars, but 3-char tokens pass through.
//
// Assumptions:
//   - nameTokens are already lowercased (tokenizeQueryKeywords lowercases).
//   - Concept synonyms YAML is loadable (panics via MustLoadConceptSynonyms if not).
//
// Thread Safety:
//
//	Safe for concurrent use (config loaded via sync.Once).
func extractDomainNouns(nameTokens []string) []string {
	synonymMap := getConceptSynonyms()
	var nouns []string
	for _, token := range nameTokens {
		lower := strings.ToLower(token)
		if _, isConceptKey := synonymMap[lower]; isConceptKey {
			continue
		}
		// Check if this is an -ing-stripped root of a concept key.
		// tokenizeQueryKeywords strips -ing from words > 6 chars, producing
		// roots like "render" from "rendering". If the -ing form is a concept
		// key, this root should also be treated as a concept token, not a domain noun.
		if _, isConceptKeyIng := synonymMap[lower+"ing"]; isConceptKeyIng {
			continue
		}
		nouns = append(nouns, lower)
	}
	return nouns
}

// extractConceptValues returns the synonym values (verb forms) for any concept
// keys found among the name tokens. For example, if nameTokens contains
// "rendering" (a concept key), this returns its values: ["render", "draw", "paint", ...].
// Also checks -ing reconstituted forms: if "render" is a token and "rendering"
// is a concept key, the values for "rendering" are included.
//
// Thread Safety: Safe for concurrent use (config loaded via sync.Once).
func extractConceptValues(nameTokens []string) []string {
	synonymMap := getConceptSynonyms()
	seen := make(map[string]bool)
	var values []string
	for _, token := range nameTokens {
		lower := strings.ToLower(token)
		// Check if the token itself is a concept key.
		if syns, ok := synonymMap[lower]; ok {
			for _, s := range syns {
				if !seen[s] {
					seen[s] = true
					values = append(values, s)
				}
			}
		}
		// Check -ing reconstituted form (e.g., "render" → "rendering").
		if syns, ok := synonymMap[lower+"ing"]; ok {
			for _, s := range syns {
				if !seen[s] {
					seen[s] = true
					values = append(values, s)
				}
			}
		}
	}
	return values
}

// candidateTier assigns a sort tier to a symbol candidate based on whether
// its name contains domain nouns and/or concept synonym values from the query.
//
// Description:
//
//	Three-tier ranking:
//	  Tier 0: candidate name contains BOTH a domain noun AND a concept synonym
//	          value. e.g., renderPages contains "page" (domain noun) AND "render"
//	          (synonym value for "rendering"). This is the strongest signal.
//	  Tier 1: candidate name contains a domain noun but NO concept synonym value.
//	          e.g., Page, pageState contain "page" but not "render"/"draw"/etc.
//	  Tier 2: candidate name matches neither. e.g., Build, Render.
//
//	When domainNouns is empty, all candidates get tier 2 (no regression from
//	pre-Rev 5 behavior — pure edge-count sort).
//
// Inputs:
//   - c: Symbol candidate to classify. Only c.Name is read.
//   - domainNouns: Lowercased domain nouns from extractDomainNouns. May be nil/empty.
//   - conceptValues: Lowercased concept synonym values (verb forms like "render",
//     "build", "init"). May be nil/empty.
//
// Outputs:
//   - int: 0 (domain noun + concept match), 1 (domain noun only), or 2 (neither).
//     Lower is better.
//
// Limitations:
//   - Uses substring matching (strings.Contains), not word-boundary matching.
//     A domain noun "log" would match "catalogBuilder". Mitigated by skipping
//     nouns shorter than 4 characters (see Rev 5a Fix #3).
//
// Assumptions:
//   - domainNouns and conceptValues are already lowercased.
//   - Candidate Name uses camelCase or PascalCase (standard for most languages).
//
// Thread Safety:
//
//	Safe for concurrent use (no shared mutable state).
func candidateTier(c agent.SymbolCandidate, domainNouns []string, conceptValues []string) int {
	if len(domainNouns) == 0 {
		return 2
	}
	lowerName := strings.ToLower(c.Name)

	// Check domain noun presence in candidate name.
	hasDomainNoun := false
	for _, noun := range domainNouns {
		if len(noun) < 4 {
			continue // Skip short nouns — too many false positives via substring
		}
		if strings.Contains(lowerName, noun) {
			hasDomainNoun = true
			break
		}
	}

	// Check concept synonym presence in candidate name.
	hasConceptValue := false
	for _, cv := range conceptValues {
		if len(cv) < 4 {
			continue
		}
		if strings.Contains(lowerName, cv) {
			hasConceptValue = true
			break
		}
	}

	// Tier assignment:
	//   tier0: domain noun + concept synonym (both present in name)
	//   tier1: domain noun OR concept synonym (either present)
	//   tier2: neither
	if hasDomainNoun && hasConceptValue {
		return 0
	}
	if hasDomainNoun || hasConceptValue {
		return 1
	}
	return 2
}

// conceptExactnessScore scores how precisely a candidate name matches concept synonyms.
//
// Description:
//
//	D3c: Within the same tier, candidates whose names are exact concept synonyms
//	should rank above those that merely contain a synonym as a substring. This
//	prevents "validateHeader" (prefix match) from being undifferentiated from
//	"validate" (exact match) when both are tier1.
//
// Inputs:
//   - c: Symbol candidate to score. Only c.Name is read.
//   - conceptValues: Lowercased concept synonym values (e.g., "validate", "check").
//
// Outputs:
//   - int: 3 = name IS a concept synonym (exact), 2 = name starts with concept,
//     1 = name contains concept, 0 = no match. Higher is better.
//
// Thread Safety: Safe for concurrent use (pure function).
func conceptExactnessScore(c agent.SymbolCandidate, conceptValues []string) int {
	lowerName := strings.ToLower(c.Name)
	bestScore := 0
	for _, cv := range conceptValues {
		if len(cv) < 4 {
			continue // Skip short values — same as candidateTier
		}
		if lowerName == cv {
			return 3 // exact match: "validate" == "validate"
		}
		if strings.HasPrefix(lowerName, cv) && bestScore < 2 {
			bestScore = 2 // prefix: "validateStruct" starts with "validate"
		}
		if strings.Contains(lowerName, cv) && bestScore < 1 {
			bestScore = 1 // contains: "getMapFromValidation" contains "valid"
		}
	}
	return bestScore
}

// autoPickExactConceptMatch returns a candidate if exactly one candidate's name
// is an exact concept synonym match. Skips LLM call entirely for unambiguous cases.
//
// Description:
//
//	D3c Option 7: When a candidate's name IS a concept synonym (exactness=3)
//	and it's the only such candidate, the LLM call is unnecessary — the answer
//	is deterministic. This saves ~200ms of LLM latency and eliminates the risk
//	of the LLM picking a wrong candidate.
//
// Inputs:
//   - candidates: Symbol candidates (already sorted/pruned).
//   - conceptValues: Lowercased concept synonym values.
//
// Outputs:
//   - agent.SymbolCandidate: The auto-picked candidate (valid only if ok is true).
//   - bool: True if exactly one exact match was found and auto-picked.
//
// Thread Safety: Safe for concurrent use (pure function).
func autoPickExactConceptMatch(
	candidates []agent.SymbolCandidate,
	conceptValues []string,
	domainNouns []string,
) (agent.SymbolCandidate, bool) {
	if len(candidates) == 0 {
		return agent.SymbolCandidate{}, false
	}

	// Determine the best tier present in the candidate list.
	bestTier := 2
	for _, c := range candidates {
		t := candidateTier(c, domainNouns, conceptValues)
		if t < bestTier {
			bestTier = t
		}
	}

	// Only consider exact concept matches within the best tier.
	// This prevents a tier1 exact match (e.g., "Build") from being auto-picked
	// when tier0 candidates exist (e.g., "assembleMenus").
	var exactMatches []agent.SymbolCandidate
	for _, c := range candidates {
		if candidateTier(c, domainNouns, conceptValues) == bestTier &&
			conceptExactnessScore(c, conceptValues) == 3 {
			exactMatches = append(exactMatches, c)
		}
	}
	if len(exactMatches) == 1 {
		return exactMatches[0], true
	}
	// D3c Rev 2: When multiple exact matches all share the same name (e.g., two
	// "validate" functions in different files), they're the same logical symbol.
	// Auto-pick the first one (highest edge count due to sort order).
	if len(exactMatches) > 1 {
		allSameName := true
		for i := 1; i < len(exactMatches); i++ {
			if exactMatches[i].Name != exactMatches[0].Name {
				allSameName = false
				break
			}
		}
		if allSameName {
			return exactMatches[0], true
		}
	}
	return agent.SymbolCandidate{}, false
}

// filterByReachability removes candidates unreachable from fromID via graph BFS.
//
// Description:
//
//	D3c Option 3: For find_path to-side resolution, candidates that are unreachable
//	from the resolved from-side symbol are removed. This prevents the LLM from
//	picking a candidate that would produce an empty path result.
//
//	Only checks the top maxCheck candidates to bound BFS cost. If all checked
//	candidates are unreachable, returns the original list (graceful degradation).
//
// Inputs:
//   - ctx: Context for cancellation.
//   - candidates: Symbol candidates to filter.
//   - fromID: The resolved from-side symbol ID.
//   - analytics: GraphAnalytics for path queries.
//   - idx: Symbol index for looking up candidate IDs.
//   - maxCheck: Maximum number of candidates to check reachability for.
//
// Outputs:
//   - []agent.SymbolCandidate: Filtered candidates. Equal or shorter than input.
//
// Thread Safety: Safe for concurrent use.
func filterByReachability(
	ctx context.Context,
	candidates []agent.SymbolCandidate,
	fromID string,
	analytics *graph.GraphAnalytics,
	idx *index.SymbolIndex,
	maxCheck int,
) []agent.SymbolCandidate {
	if len(candidates) == 0 || fromID == "" || analytics == nil || idx == nil {
		return candidates
	}

	if maxCheck > len(candidates) {
		maxCheck = len(candidates)
	}

	var reachable []agent.SymbolCandidate
	checkedCount := 0

	for _, c := range candidates {
		if checkedCount >= maxCheck {
			// Keep unchecked candidates as-is (don't remove what we didn't verify)
			reachable = append(reachable, c)
			continue
		}

		// Look up candidate's symbol ID
		syms := idx.GetByName(c.Name)
		if len(syms) == 0 {
			// Can't verify — keep it
			reachable = append(reachable, c)
			continue
		}

		// Find the matching symbol by file+line
		var candidateID string
		for _, sym := range syms {
			if sym.FilePath == c.FilePath && sym.StartLine == c.Line {
				candidateID = sym.ID
				break
			}
		}
		if candidateID == "" {
			// No exact match — use first
			candidateID = syms[0].ID
		}

		checkedCount++

		// Check reachability via ShortestPath
		pathResult, err := analytics.ShortestPath(ctx, fromID, candidateID)
		if err != nil {
			// Error checking — keep the candidate
			reachable = append(reachable, c)
			continue
		}
		if pathResult != nil && len(pathResult.Path) > 0 {
			reachable = append(reachable, c)
		} else {
			slog.Debug("D3c: filtering unreachable candidate",
				slog.String("candidate", c.Name),
				slog.String("from", fromID),
			)
		}
	}

	// Graceful degradation: if all checked candidates are unreachable, return original
	if len(reachable) == 0 {
		slog.Info("D3c: all checked candidates unreachable, keeping original list",
			slog.Int("checked", checkedCount),
			slog.Int("total", len(candidates)),
		)
		return candidates
	}

	return reachable
}

// pruneCandidatesByTier reduces the candidate list by tier to prevent the LLM
// from being overwhelmed with low-relevance candidates.
//
// Description:
//
//	D3: Pre-LLM pruning guardrail. Candidates must already be sorted by tier
//	(tier0 first, then tier1, then tier2). Rules:
//	  - Keep ALL tier0 candidates
//	  - Keep top N tier1: 10 if tier0 < 3, else 5
//	  - Keep tier2 only if tier0 + tier1 < 8 (max 5)
//	  - Hard cap: 20 total
//
// Inputs:
//
//   - candidates: Symbol candidates sorted by tier (ascending).
//   - domainNouns: Domain nouns extracted from the hallucinated name.
//   - conceptValues: Concept synonym values for the query.
//
// Outputs:
//
//   - []agent.SymbolCandidate: Pruned candidate list. May be shorter or equal.
//
// Thread Safety: Safe for concurrent use (pure function).
func pruneCandidatesByTier(
	candidates []agent.SymbolCandidate,
	domainNouns []string,
	conceptValues []string,
) []agent.SymbolCandidate {
	if len(candidates) == 0 {
		return candidates
	}

	var tier0, tier1, tier2 []agent.SymbolCandidate
	for _, c := range candidates {
		switch candidateTier(c, domainNouns, conceptValues) {
		case 0:
			tier0 = append(tier0, c)
		case 1:
			tier1 = append(tier1, c)
		default:
			tier2 = append(tier2, c)
		}
	}

	// Cap tier1: 10 if few tier0, else 5
	tier1Cap := 10
	if len(tier0) >= 3 {
		tier1Cap = 5
	}
	if len(tier1) > tier1Cap {
		tier1 = tier1[:tier1Cap]
	}

	// Include tier2 only if tier0+tier1 is sparse
	if len(tier0)+len(tier1) >= 8 {
		tier2 = nil
	} else if len(tier2) > 5 {
		tier2 = tier2[:5]
	}

	result := make([]agent.SymbolCandidate, 0, len(tier0)+len(tier1)+len(tier2))
	result = append(result, tier0...)
	result = append(result, tier1...)
	result = append(result, tier2...)

	// Hard cap
	if len(result) > 20 {
		result = result[:20]
	}

	return result
}

// validateTierSelection checks whether the LLM's symbol pick should be
// overridden by a tier0 candidate.
//
// Description:
//
//	D3: Post-LLM validation guardrail. When the LLM picks a non-tier0
//	symbol but a tier0 candidate with sufficient call edges exists (>= 3),
//	the validator overrides the pick. This prevents the LLM's positional
//	bias or domain-ignorance from selecting an irrelevant symbol.
//
// Inputs:
//
//   - llmPick: The symbol name returned by the LLM.
//   - candidates: The (pruned) candidate list that was sent to the LLM.
//   - domainNouns: Domain nouns extracted from the hallucinated name.
//   - conceptValues: Concept synonym values for the query.
//
// Outputs:
//
//   - validated: The final symbol name (either llmPick or the override).
//   - overridden: True if the validator replaced the LLM's pick.
//
// Thread Safety: Safe for concurrent use (pure function).
func validateTierSelection(
	llmPick string,
	candidates []agent.SymbolCandidate,
	domainNouns []string,
	conceptValues []string,
) (validated string, overridden bool) {
	if llmPick == "" || len(candidates) == 0 {
		return llmPick, false
	}

	// Determine the tier of the LLM's pick
	pickTier := 2 // default if not found
	for _, c := range candidates {
		if c.Name == llmPick {
			pickTier = candidateTier(c, domainNouns, conceptValues)
			break
		}
	}

	// If the LLM picked tier0, accept
	if pickTier == 0 {
		return llmPick, false
	}

	// Find the best tier0 candidate (first in sorted list)
	var bestTier0 *agent.SymbolCandidate
	for i := range candidates {
		if candidateTier(candidates[i], domainNouns, conceptValues) == 0 {
			bestTier0 = &candidates[i]
			break
		}
	}

	// Tier0 exists with sufficient edges → override any non-tier0 pick
	if bestTier0 != nil && bestTier0.OutEdges >= 3 {
		return bestTier0.Name, true
	}

	// No tier0 (or trivial tier0). If LLM picked tier2 and tier1 exists,
	// override to the best tier1 candidate (concept or domain match beats neither).
	if pickTier == 2 {
		var bestTier1 *agent.SymbolCandidate
		for i := range candidates {
			if candidateTier(candidates[i], domainNouns, conceptValues) == 1 {
				bestTier1 = &candidates[i]
				break
			}
		}
		if bestTier1 != nil && (bestTier1.OutEdges >= 1 || bestTier1.InEdges >= 3) {
			return bestTier1.Name, true
		}
	}

	return llmPick, false
}

// searchSymbolCandidates searches the symbol index for symbols matching
// query keywords and returns deduplicated candidates filtered to callable kinds.
//
// # Description
//
// IT-12: Used for conceptual symbol resolution. Searches the index for
// each keyword and collects candidate symbols, filtering out non-callable
// kinds (imports, variables, fields, constants, properties).
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - idx: The symbol index to search. Must not be nil.
//   - keywords: Keywords to search for.
//   - maxPerKeyword: Maximum results per keyword search.
//
// # Outputs
//
//   - []agent.SymbolCandidate: Deduplicated candidate symbols. May be empty.
//
// # Thread Safety
//
// Safe for concurrent use.
func searchSymbolCandidates(
	ctx context.Context,
	idx *index.SymbolIndex,
	keywords []string,
	maxPerKeyword int,
) []agent.SymbolCandidate {
	if idx == nil || len(keywords) == 0 {
		return nil
	}

	// Non-callable kinds to filter out. Interfaces, types, classes, and structs
	// are excluded because they have no call edges — picking one as a starting
	// point for get_call_chain or find_path produces empty/shallow results (IT-12).
	// For conceptual resolution we always want functions/methods as starting points.
	nonCallableKinds := map[ast.SymbolKind]bool{
		ast.SymbolKindImport:    true,
		ast.SymbolKindVariable:  true,
		ast.SymbolKindField:     true,
		ast.SymbolKindConstant:  true,
		ast.SymbolKindProperty:  true,
		ast.SymbolKindInterface: true,
		ast.SymbolKindType:      true,
		ast.SymbolKindClass:     true,
		ast.SymbolKindStruct:    true,
	}

	seen := make(map[string]bool)
	var candidates []agent.SymbolCandidate

	for _, kw := range keywords {
		results, err := idx.Search(ctx, kw, maxPerKeyword)
		if err != nil {
			continue
		}
		slog.Debug("IT-12: searchSymbolCandidates keyword result",
			slog.String("keyword", kw),
			slog.Int("raw_hits", len(results)),
		)
		for _, sym := range results {
			if seen[sym.ID] {
				continue
			}
			seen[sym.ID] = true

			// Filter to callable kinds
			if nonCallableKinds[sym.Kind] {
				continue
			}

			// Filter out test file symbols — test fixtures like conftest.py's
			// frame_or_series (249 incoming edges) dominate edge-count sorting
			// and cause the LLM to resolve to test helpers instead of production code.
			if isTestFilePath(sym.FilePath) {
				continue
			}

			candidates = append(candidates, agent.SymbolCandidate{
				Name:     sym.Name,
				Kind:     sym.Kind.String(),
				FilePath: sym.FilePath,
				Line:     sym.StartLine,
				Receiver: sym.Receiver, // D3c: populate receiver for method ownership in prompt
			})
		}
	}

	return candidates
}
