// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"math"
	"testing"
)

// =============================================================================
// BuildBM25Index Tests
// =============================================================================

func TestBuildBM25Index_Empty(t *testing.T) {
	idx := BuildBM25Index(nil)
	if idx == nil {
		t.Fatal("expected non-nil index for nil specs")
	}
	if !idx.IsEmpty() {
		t.Error("expected IsEmpty() == true for nil specs")
	}
}

func TestBuildBM25Index_EmptySlice(t *testing.T) {
	idx := BuildBM25Index([]ToolSpec{})
	if idx == nil {
		t.Fatal("expected non-nil index for empty specs")
	}
	if !idx.IsEmpty() {
		t.Error("expected IsEmpty() == true for empty specs")
	}
}

func TestBuildBM25Index_Single(t *testing.T) {
	specs := []ToolSpec{
		{
			Name:    "find_references",
			BestFor: []string{"references", "usages", "where is it used"},
			UseWhen: "Use when finding where a symbol is referenced",
		},
	}
	idx := BuildBM25Index(specs)
	if idx.IsEmpty() {
		t.Error("expected non-empty index for 1 spec")
	}
	if len(idx.docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(idx.docs))
	}
	if idx.avgLen <= 0 {
		t.Error("expected positive avgLen")
	}
}

func TestBuildBM25Index_MultipleSpecs(t *testing.T) {
	specs := makeReferencesVsSymbolSpecs()
	idx := BuildBM25Index(specs)
	if idx.IsEmpty() {
		t.Error("expected non-empty index")
	}
	if len(idx.docs) != len(specs) {
		t.Errorf("expected %d docs, got %d", len(specs), len(idx.docs))
	}
	if len(idx.idf) == 0 {
		t.Error("expected non-empty IDF map")
	}
}

func TestBuildBM25Index_IDFSmoothing(t *testing.T) {
	// IDF = log((N+1)/(df+1)) + 1. With N=1, df=1, IDF = log(1) + 1 = 1.0.
	// All terms in a single-doc corpus should get IDF = 1.0.
	specs := []ToolSpec{
		{Name: "find_references", BestFor: []string{"references"}},
	}
	idx := BuildBM25Index(specs)
	for term, idf := range idx.idf {
		expected := math.Log(float64(2)/float64(2)) + 1.0 // log(1)+1 = 1.0
		if math.Abs(idf-expected) > 1e-9 {
			t.Errorf("term %q: expected IDF %.6f, got %.6f", term, expected, idf)
		}
	}
}

func TestBuildBM25Index_IDFRareTermsScoreHigher(t *testing.T) {
	// "references" appears in 1 of 3 docs → rare → high IDF.
	// "find" appears in all 3 docs → common → low IDF.
	specs := []ToolSpec{
		{Name: "find_references", BestFor: []string{"find", "references"}},
		{Name: "find_callers", BestFor: []string{"find", "callers"}},
		{Name: "find_callees", BestFor: []string{"find", "callees"}},
	}
	idx := BuildBM25Index(specs)

	findIDF := idx.idf["find"]
	refsIDF := idx.idf["references"]

	// "references" in 1/3 docs; "find" in 3/3 docs → references must have higher IDF.
	if refsIDF <= findIDF {
		t.Errorf("rare term 'references' (IDF %.4f) should score higher than common term 'find' (IDF %.4f)",
			refsIDF, findIDF)
	}
}

// =============================================================================
// BM25Index.Score Tests
// =============================================================================

func TestBM25Score_EmptyQuery(t *testing.T) {
	idx := BuildBM25Index(makeReferencesVsSymbolSpecs())
	scores := idx.Score("")
	if len(scores) != 0 {
		t.Errorf("expected empty scores for empty query, got %d entries", len(scores))
	}
}

func TestBM25Score_EmptyIndex(t *testing.T) {
	idx := BuildBM25Index(nil)
	scores := idx.Score("find references")
	if len(scores) != 0 {
		t.Errorf("expected empty scores for empty index, got %d entries", len(scores))
	}
}

func TestBM25Score_NoMatch(t *testing.T) {
	idx := BuildBM25Index(makeReferencesVsSymbolSpecs())
	// Query terms that appear in no tool document.
	scores := idx.Score("xyzzy quux zork")
	if len(scores) != 0 {
		t.Errorf("expected no scores for all-miss query, got %d entries", len(scores))
	}
}

func TestBM25Score_NormalizedToOne(t *testing.T) {
	idx := BuildBM25Index(makeReferencesVsSymbolSpecs())
	scores := idx.Score("references usages where is it used")
	if len(scores) == 0 {
		t.Skip("no scores produced — corpus may not contain query terms")
	}
	var maxScore float64
	for _, s := range scores {
		if s > maxScore {
			maxScore = s
		}
	}
	if math.Abs(maxScore-1.0) > 1e-9 {
		t.Errorf("expected max score to be exactly 1.0, got %.6f", maxScore)
	}
}

func TestBM25Score_AllScoresInRange(t *testing.T) {
	idx := BuildBM25Index(makeReferencesVsSymbolSpecs())
	scores := idx.Score("find references symbol definition")
	for tool, s := range scores {
		if s < 0 || s > 1.0+1e-9 {
			t.Errorf("score for %q out of [0,1]: %.6f", tool, s)
		}
	}
}

// TestBM25Score_FindReferences_vs_FindSymbol is the central regression guard for GR-61.
//
// The IT-06 routing regression: queries like "where is X referenced" were
// misrouted to find_symbol because substring counting gave find_symbol 1 point
// (keyword "where is") while find_references scored 0 ("references" ≠ "referenced").
//
// BM25 does NOT stem — "referenced" and "references" are distinct tokens after
// ExtractQueryTerms. The BM25 fix therefore targets queries that DO use the exact
// token "references" in the query. The morphological fix for "referenced" → route
// to find_references is handled by Phase 1 (Option L intent templates) and
// embedding similarity (Option I). This test verifies the BM25 signal for exact
// term overlap.
func TestBM25Score_FindReferences_vs_FindSymbol_ExactTerm(t *testing.T) {
	specs := makeReferencesVsSymbolSpecs()
	idx := BuildBM25Index(specs)

	// "references" is an exact term match for find_references but not find_symbol.
	scores := idx.Score("find all references to parseConfig")

	refsScore := scores["find_references"]
	symScore := scores["find_symbol"]

	if refsScore <= symScore {
		t.Errorf("find_references BM25 score (%.4f) should exceed find_symbol (%.4f) for 'references' query",
			refsScore, symScore)
	}
}

func TestBM25Score_FindSymbol_WinsDefinitionQuery(t *testing.T) {
	specs := makeReferencesVsSymbolSpecs()
	idx := BuildBM25Index(specs)

	// "definition" / "defined" / "where is X" aligns with find_symbol corpus.
	scores := idx.Score("where is parseConfig defined find definition")

	symScore := scores["find_symbol"]
	refsScore := scores["find_references"]

	// find_symbol should score at least as high as find_references for definition queries.
	if symScore < refsScore {
		t.Errorf("find_symbol BM25 score (%.4f) should be >= find_references (%.4f) for definition query",
			symScore, refsScore)
	}
}

func TestBM25Score_IDFWeighting_RareTermBoosted(t *testing.T) {
	// Build an index where "references" is rare (only in find_references doc).
	// A query containing "references" should score find_references highest.
	specs := []ToolSpec{
		{
			Name:    "find_references",
			BestFor: []string{"references", "usages"},
			UseWhen: "Find all references and usages of a symbol",
		},
		{
			Name:    "find_callers",
			BestFor: []string{"callers", "call sites", "who calls"},
			UseWhen: "Find all callers of a function",
		},
		{
			Name:    "find_callees",
			BestFor: []string{"callees", "dependencies", "outgoing calls"},
			UseWhen: "Find all callees of a function",
		},
		{
			Name:    "find_dead_code",
			BestFor: []string{"dead code", "unused", "unreachable", "no references"},
			UseWhen: "Find dead or unreachable code",
		},
	}
	idx := BuildBM25Index(specs)

	// "references" appears in find_references (BestFor) AND find_dead_code ("no references").
	// "usages" appears only in find_references.
	// Query with "usages" should uniquely prefer find_references.
	scores := idx.Score("find all usages of parseConfig")

	if scores["find_references"] <= 0 {
		t.Error("expected find_references to score > 0 for 'usages' query")
	}
	for tool, s := range scores {
		if tool != "find_references" && s >= scores["find_references"] {
			t.Errorf("expected find_references to lead; %q scored %.4f vs find_references %.4f",
				tool, s, scores["find_references"])
		}
	}
}

func TestBM25Score_CamelCaseSplitting(t *testing.T) {
	// ExtractQueryTerms handles camelCase: "parseConfig" → ["parse", "config"].
	// A tool with keyword "parse" should score for query "parseConfig".
	specs := []ToolSpec{
		{Name: "find_symbol", BestFor: []string{"parse", "definition", "where is"}},
		{Name: "find_references", BestFor: []string{"references", "usages"}},
	}
	idx := BuildBM25Index(specs)

	scores := idx.Score("parseConfig symbol")
	// find_symbol should score because "parse" from "parseConfig" matches its corpus.
	if scores["find_symbol"] <= 0 {
		t.Error("expected find_symbol to score > 0 for camelCase query token 'parseConfig'")
	}
}

func TestBM25Score_Deterministic(t *testing.T) {
	// Same query should produce identical scores on repeated calls.
	specs := makeReferencesVsSymbolSpecs()
	idx := BuildBM25Index(specs)
	query := "find all references to the parseConfig function"

	scores1 := idx.Score(query)
	scores2 := idx.Score(query)

	for tool, s1 := range scores1 {
		s2 := scores2[tool]
		if math.Abs(s1-s2) > 1e-12 {
			t.Errorf("non-deterministic score for %q: %.12f vs %.12f", tool, s1, s2)
		}
	}
}

// =============================================================================
// IsEmpty Tests
// =============================================================================

func TestBM25Index_IsEmpty_EmptyIndex(t *testing.T) {
	idx := BuildBM25Index(nil)
	if !idx.IsEmpty() {
		t.Error("expected IsEmpty() for nil specs")
	}
}

func TestBM25Index_IsEmpty_NonEmpty(t *testing.T) {
	idx := BuildBM25Index(makeReferencesVsSymbolSpecs())
	if idx.IsEmpty() {
		t.Error("expected !IsEmpty() for populated index")
	}
}

// =============================================================================
// Helpers
// =============================================================================

// makeReferencesVsSymbolSpecs builds the minimal spec set that reproduces the
// IT-06 routing confusion: find_references vs find_symbol vs find_callers.
func makeReferencesVsSymbolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Name:    "find_references",
			BestFor: []string{"references", "usages", "where is it used", "find all references"},
			UseWhen: "Use when the user wants to find all places where a symbol is referenced or used",
		},
		{
			Name:    "find_symbol",
			BestFor: []string{"where is", "defined", "definition", "declaration", "find symbol", "locate"},
			UseWhen: "Use when the user wants to find where a symbol is declared or defined",
		},
		{
			Name:    "find_callers",
			BestFor: []string{"callers", "who calls", "call sites", "incoming calls", "what calls"},
			UseWhen: "Use when the user wants to find all call sites for a function",
		},
		{
			Name:    "find_callees",
			BestFor: []string{"callees", "what does it call", "outgoing calls", "dependencies"},
			UseWhen: "Use when the user wants to find what functions a given function calls",
		},
		{
			Name:    "find_dead_code",
			BestFor: []string{"dead code", "unused", "unreachable", "no callers", "no references", "never called"},
			UseWhen: "Use when the user wants to find code that is unreachable or has no callers",
		},
	}
}
