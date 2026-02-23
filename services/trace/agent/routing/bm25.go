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
	"strings"
)

// =============================================================================
// BM25 Index (IT-06c Option J)
// =============================================================================

// BM25 tuning constants. Standard values recommended by Robertson et al.
const (
	// bm25K1 controls term frequency saturation. Higher = slower saturation.
	// Range [1.2, 2.0] is typical. 1.5 is a robust middle ground.
	bm25K1 = 1.5

	// bm25B controls document length normalization.
	// 0 = no normalization, 1 = full normalization. 0.75 is the standard default.
	bm25B = 0.75
)

// bm25Doc holds the BM25 representation of a single tool's corpus.
type bm25Doc struct {
	// name is the tool identifier.
	name string

	// tf maps each term to its frequency within this tool's BM25 document.
	tf map[string]int

	// len is the total number of terms in this tool's document (after tokenization).
	len int
}

// BM25Index is a pre-built inverted index over tool descriptions.
//
// # Description
//
// Implements Okapi BM25 ranking over a corpus of tool "documents" where each
// document is the concatenation of a tool's keywords and use_when text.
// At query time, BM25 produces a ranked score for each tool that is
// proportional to how well its document matches the query terms, weighted
// by term rarity across all tools (IDF).
//
// This replaces plain substring keyword counting, which has no IDF weighting
// and no morphological flexibility.
//
// # Thread Safety
//
// BM25Index is immutable after construction via BuildBM25Index. Safe for
// concurrent use without additional synchronization.
type BM25Index struct {
	// docs holds the per-tool tokenized documents.
	docs []bm25Doc

	// idf maps each term to its inverse document frequency score.
	// Computed once at build time as: log((N+1)/(df+1)) + 1 (Lucene-style smoothing).
	idf map[string]float64

	// avgLen is the average document length across all tools.
	avgLen float64
}

// BuildBM25Index constructs a BM25Index from a slice of ToolSpecs.
//
// # Description
//
// Each tool's "document" is built from its keywords and use_when text.
// Tokenization reuses ExtractQueryTerms from semantic.go (same package),
// which handles camelCase splitting and noise-word removal.
// IDF is computed with Lucene-style add-one smoothing to avoid zero division.
//
// # Inputs
//
//   - specs: Tool specifications to index. Empty slice returns a valid but
//     empty index that will produce zero scores for all queries.
//
// # Outputs
//
//   - *BM25Index: The constructed index. Never nil.
//
// # Thread Safety
//
// The returned index is immutable and safe for concurrent use.
func BuildBM25Index(specs []ToolSpec) *BM25Index {
	if len(specs) == 0 {
		return &BM25Index{
			idf: make(map[string]float64),
		}
	}

	docs := make([]bm25Doc, 0, len(specs))
	totalLen := 0

	// Build document frequency count for IDF computation.
	// df[term] = number of tools whose document contains term.
	df := make(map[string]int)

	for _, spec := range specs {
		doc := buildDoc(spec)
		docs = append(docs, doc)
		totalLen += doc.len

		// Each term present in this doc contributes 1 to its df.
		for term := range doc.tf {
			df[term]++
		}
	}

	N := len(docs)
	avgLen := float64(totalLen) / float64(N)

	// Compute IDF for every unique term.
	// Formula: log((N + 1) / (df + 1)) + 1
	// The +1 in numerator and denominator is Lucene-style smoothing.
	// The trailing +1 ensures IDF is always >= 1.
	idf := make(map[string]float64, len(df))
	for term, docFreq := range df {
		idf[term] = math.Log(float64(N+1)/float64(docFreq+1)) + 1.0
	}

	return &BM25Index{
		docs:   docs,
		idf:    idf,
		avgLen: avgLen,
	}
}

// buildDoc tokenizes a ToolSpec into a bm25Doc.
//
// The BM25 document for a tool is the concatenation of:
//   - All keyword strings (joined with spaces)
//   - The use_when text
//
// AvoidWhen is deliberately excluded — negative guidance does not help
// match relevant queries and would dilute term frequency signals.
func buildDoc(spec ToolSpec) bm25Doc {
	// Build the raw document string.
	parts := make([]string, 0, len(spec.BestFor)+2)
	parts = append(parts, spec.Name)
	parts = append(parts, spec.BestFor...)
	if spec.UseWhen != "" {
		parts = append(parts, spec.UseWhen)
	}
	raw := strings.Join(parts, " ")

	// Tokenize with the shared ExtractQueryTerms function (same package).
	// This handles: lowercase, camelCase splitting, noise-word removal,
	// delimiter normalization (spaces, underscores, dots).
	termSet := ExtractQueryTerms(raw)

	// Convert term set to frequency map.
	// ExtractQueryTerms deduplicates; each term appears exactly once per doc,
	// so tf=1 for all terms (binary presence). True TF would require a
	// non-deduplicating tokenizer, but for routing over ~30 tools, IDF does
	// the heavy lifting and binary presence is sufficient.
	//
	// Note: bm25Doc.len is the unique-term count (vocabulary size), not the
	// raw token count. Document-length normalization (the b factor) therefore
	// penalizes tools with larger vocabularies rather than longer raw text.
	// With a 30-tool corpus and short BestFor keyword lists this is acceptable.
	tf := make(map[string]int, len(termSet))
	for term := range termSet {
		tf[term] = 1
	}

	return bm25Doc{
		name: spec.Name,
		tf:   tf,
		len:  len(tf),
	}
}

// IsEmpty reports whether the index contains no tool documents.
//
// # Description
//
// Returns true for an index built from nil or empty specs (the initial state
// of a lazily-initialized PreFilter). Used by scoreHybrid to trigger the
// one-time corpus build when allSpecs first becomes available.
//
// # Thread Safety
//
// The BM25Index is immutable after construction. IsEmpty() is safe to call
// concurrently, but the caller must hold at least a read lock on the
// enclosing PreFilter.bm25mu when reading the *BM25Index pointer itself.
func (idx *BM25Index) IsEmpty() bool {
	return len(idx.docs) == 0
}

// Score computes the BM25 score for each tool given a query string.
//
// # Description
//
// Tokenizes the query, then for each tool computes:
//
//	score(tool, query) = Σ_t [ idf(t) × (tf(t,doc) × (k1+1)) / (tf(t,doc) + k1 × (1 - b + b × dl/avgdl)) ]
//
// where t ranges over unique query terms present in the tool's document.
// Scores are normalized to [0.0, 1.0] by dividing by the maximum score.
//
// # Inputs
//
//   - query: The raw query string. Empty query returns empty scores map.
//
// # Outputs
//
//   - map[string]float64: Tool name → normalized BM25 score in [0.0, 1.0].
//     Tools with zero BM25 score are omitted from the result.
//
// # Thread Safety
//
// Safe for concurrent use. Does not modify the index.
func (idx *BM25Index) Score(query string) map[string]float64 {
	if query == "" || len(idx.docs) == 0 {
		return make(map[string]float64)
	}

	queryTerms := ExtractQueryTerms(query)
	if len(queryTerms) == 0 {
		return make(map[string]float64)
	}

	scores := make(map[string]float64, len(idx.docs))
	var maxScore float64

	for _, doc := range idx.docs {
		score := bm25Score(queryTerms, doc, idx.idf, idx.avgLen)
		if score > 0 {
			scores[doc.name] = score
			if score > maxScore {
				maxScore = score
			}
		}
	}

	// Normalize to [0, 1].
	if maxScore > 0 {
		for name := range scores {
			scores[name] /= maxScore
		}
	}

	return scores
}

// bm25Score computes the raw BM25 score for a single (query, doc) pair.
func bm25Score(queryTerms map[string]bool, doc bm25Doc, idf map[string]float64, avgLen float64) float64 {
	dl := float64(doc.len)
	var score float64

	for term := range queryTerms {
		tf, inDoc := doc.tf[term]
		if !inDoc {
			continue
		}

		termIDF, knownTerm := idf[term]
		if !knownTerm {
			continue
		}

		// BM25 numerator: tf * (k1 + 1)
		tfFloat := float64(tf)
		numerator := tfFloat * (bm25K1 + 1)

		// BM25 denominator: tf + k1 * (1 - b + b * dl/avgdl)
		lengthNorm := bm25K1 * (1.0 - bm25B + bm25B*dl/avgLen)
		denominator := tfFloat + lengthNorm

		score += termIDF * (numerator / denominator)
	}

	return score
}
