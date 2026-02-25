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
	"strings"
	"sync"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// =============================================================================
// Semantic Tool Call Tracking (GR-38 Issue 17)
// =============================================================================

// =============================================================================
// Prometheus Metrics (O1.1 Fix)
// =============================================================================

var (
	// semanticChecksTotal counts semantic duplicate checks by status.
	semanticChecksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trace_semantic_checks_total",
		Help: "Total semantic duplicate checks by status",
	}, []string{"status"})

	// semanticSimilarityHistogram tracks similarity score distribution.
	semanticSimilarityHistogram = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_semantic_similarity",
		Help:    "Distribution of semantic similarity scores",
		Buckets: []float64{0.0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})

	// semanticHistorySize tracks current history size.
	semanticHistorySize = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trace_semantic_history_size",
		Help: "Current number of entries in semantic tool history",
	})

	// semanticCheckDuration tracks time spent on semantic checks.
	semanticCheckDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_semantic_check_duration_seconds",
		Help:    "Time spent on semantic duplicate checks",
		Buckets: prometheus.DefBuckets,
	})
)

// =============================================================================
// Configuration (S1.3 Fix)
// =============================================================================

// SemanticConfig holds configurable thresholds for semantic routing.
//
// Description:
//
//	Allows customization of semantic duplicate detection behavior.
//	Use DefaultSemanticConfig() for production defaults.
//
// Thread Safety: Immutable after creation. Safe to share across goroutines.
type SemanticConfig struct {
	// DuplicateThreshold is the similarity threshold for blocking (default 0.8).
	// Calls with similarity >= this are considered semantic duplicates.
	DuplicateThreshold float64

	// PenaltyThreshold is the similarity threshold for penalizing (default 0.3).
	// Calls with similarity >= this (but < duplicate) get UCB1 penalty.
	PenaltyThreshold float64

	// PenaltyWeight is the UCB1 penalty multiplier (default 0.5).
	// SemanticPenalty = similarity * PenaltyWeight.
	PenaltyWeight float64

	// MaxHistorySize is the maximum tool calls to retain (default 100).
	MaxHistorySize int
}

// DefaultSemanticConfig returns production-ready default configuration.
//
// Outputs:
//
//	SemanticConfig - Configuration with default values.
//
// Thread Safety: Safe for concurrent use (returns value type).
func DefaultSemanticConfig() SemanticConfig {
	return SemanticConfig{
		DuplicateThreshold: 0.8,
		PenaltyThreshold:   0.3,
		PenaltyWeight:      0.5,
		MaxHistorySize:     100,
	}
}

// Validate checks that configuration values are sensible.
//
// Outputs:
//
//	bool - True if configuration is valid.
//
// Thread Safety: Safe for concurrent use (read-only).
func (c SemanticConfig) Validate() bool {
	if c.DuplicateThreshold < 0 || c.DuplicateThreshold > 1 {
		return false
	}
	if c.PenaltyThreshold < 0 || c.PenaltyThreshold > 1 {
		return false
	}
	if c.PenaltyThreshold >= c.DuplicateThreshold {
		return false
	}
	if c.PenaltyWeight < 0 {
		return false
	}
	if c.MaxHistorySize <= 0 {
		return false
	}
	return true
}

// MaxToolCallHistorySize is the maximum number of calls to retain in history.
// Older calls are evicted when this limit is reached.
// R1.1 Fix: Prevent unbounded memory growth.
// Deprecated: Use SemanticConfig.MaxHistorySize instead.
const MaxToolCallHistorySize = 100

// noiseWords is the set of common words that don't add semantic value.
// R1.3 Fix: Moved to package level to avoid allocation on every call.
var noiseWords = map[string]bool{
	"the": true, "a": true, "an": true,
	"in": true, "on": true, "at": true,
	"to": true, "for": true, "of": true,
	"is": true, "are": true, "was": true,
	"it": true, "be": true,
}

// ToolCallSignature represents a unique tool call with semantic signature.
//
// Description:
//
//	Captures the tool name and extracted query terms for semantic comparison.
//	Two calls are considered semantically equivalent if they have the same tool
//	and high Jaccard similarity between their query terms.
//
// Thread Safety: Immutable after creation.
type ToolCallSignature struct {
	// Tool is the tool name (e.g., "Grep", "find_callers").
	Tool string

	// QueryTerms are extracted terms from the query parameters.
	// Used for Jaccard similarity comparison.
	QueryTerms map[string]bool

	// RawQuery is the original query string before term extraction.
	// Stored for exact duplicate detection and debugging.
	RawQuery string

	// StepNumber is when this call was made (1-indexed).
	StepNumber int

	// Success indicates whether the tool call succeeded.
	Success bool
}

// ToolCallHistory tracks tool calls with semantic signatures.
//
// Description:
//
//	Maintains a history of tool calls for semantic duplicate detection.
//	Provides methods to check if a proposed call is similar to prior calls.
//	R1.1 Fix: History is limited to MaxToolCallHistorySize entries.
//	P1.1 Fix: Tool-indexed map for O(1) tool lookup.
//
// Thread Safety: Safe for concurrent use. All public methods acquire locks.
type ToolCallHistory struct {
	mu        sync.RWMutex
	calls     []ToolCallSignature
	toolIndex map[string][]int // P1.1 Fix: maps tool name -> indices in calls slice
	maxSize   int
	config    SemanticConfig
}

// NewToolCallHistory creates a new empty history with default configuration.
//
// Outputs:
//
//	*ToolCallHistory - The history instance.
//
// Thread Safety: The returned instance is safe for concurrent use.
func NewToolCallHistory() *ToolCallHistory {
	return NewToolCallHistoryWithConfig(DefaultSemanticConfig())
}

// NewToolCallHistoryWithSize creates a new empty history with specified max size.
//
// Inputs:
//
//	maxSize - Maximum number of entries to retain. If <= 0, uses default.
//
// Outputs:
//
//	*ToolCallHistory - The history instance.
//
// Thread Safety: The returned instance is safe for concurrent use.
//
// Deprecated: Use NewToolCallHistoryWithConfig for full configuration.
func NewToolCallHistoryWithSize(maxSize int) *ToolCallHistory {
	config := DefaultSemanticConfig()
	if maxSize > 0 {
		config.MaxHistorySize = maxSize
	}
	return NewToolCallHistoryWithConfig(config)
}

// NewToolCallHistoryWithConfig creates a new empty history with full configuration.
//
// Inputs:
//
//	config - Semantic configuration. If invalid, uses defaults.
//
// Outputs:
//
//	*ToolCallHistory - The history instance.
//
// Thread Safety: The returned instance is safe for concurrent use.
func NewToolCallHistoryWithConfig(config SemanticConfig) *ToolCallHistory {
	if !config.Validate() {
		config = DefaultSemanticConfig()
	}
	return &ToolCallHistory{
		calls:     make([]ToolCallSignature, 0, min(config.MaxHistorySize, 100)),
		toolIndex: make(map[string][]int),
		maxSize:   config.MaxHistorySize,
		config:    config,
	}
}

// Add records a tool call signature in the history.
//
// Description:
//
//	Appends the signature to history. If history exceeds maxSize,
//	older entries are evicted (sliding window). Updates the tool index.
//
// Inputs:
//
//	sig - The tool call signature to record.
//
// Thread Safety: Safe for concurrent use. Acquires write lock.
func (h *ToolCallHistory) Add(sig ToolCallSignature) {
	h.mu.Lock()
	defer h.mu.Unlock()

	idx := len(h.calls)
	h.calls = append(h.calls, sig)

	// P1.1 Fix: Update tool index
	h.toolIndex[sig.Tool] = append(h.toolIndex[sig.Tool], idx)

	// R1.1 Fix: Evict oldest entries if over limit
	if len(h.calls) > h.maxSize {
		excess := len(h.calls) - h.maxSize
		h.calls = h.calls[excess:]
		// P1.1 Fix: Rebuild index after eviction
		h.rebuildIndexLocked()
	}

	// O1.1 Fix: Update gauge metric
	semanticHistorySize.Set(float64(len(h.calls)))
}

// rebuildIndexLocked rebuilds the tool index from scratch.
// Must be called with write lock held.
func (h *ToolCallHistory) rebuildIndexLocked() {
	h.toolIndex = make(map[string][]int, len(h.toolIndex))
	for i, call := range h.calls {
		h.toolIndex[call.Tool] = append(h.toolIndex[call.Tool], i)
	}
}

// GetCallsForTool returns all calls for a specific tool.
//
// Description:
//
//	P1.1 Fix: Uses tool index for O(1) lookup instead of O(n) scan.
//
// Inputs:
//
//	tool - The tool name to filter by.
//
// Outputs:
//
//	[]ToolCallSignature - Copies of calls matching the tool name.
//
// Thread Safety: Safe for concurrent use. Acquires read lock.
func (h *ToolCallHistory) GetCallsForTool(tool string) []ToolCallSignature {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// P1.1 Fix: Use tool index for O(1) lookup
	indices := h.toolIndex[tool]
	if len(indices) == 0 {
		return nil
	}

	result := make([]ToolCallSignature, len(indices))
	for i, idx := range indices {
		result[i] = h.calls[idx]
	}
	return result
}

// GetSimilarity finds the most similar prior call for a tool+query combination.
//
// Description:
//
//	Compares the proposed query terms against all prior calls of the same tool.
//	Returns the maximum similarity found and a copy of the most similar call.
//	I1.1 Fix: Returns copy instead of pointer to avoid data races.
//	P1.1 Fix: Uses tool index to only check relevant calls.
//
// Inputs:
//
//	tool - The tool name to check.
//	queryTerms - Extracted terms from the proposed call's parameters.
//
// Outputs:
//
//	maxSimilarity - Highest Jaccard similarity found (0.0-1.0).
//	mostSimilar - Copy of the most similar prior call, or nil if none found.
//
// Thread Safety: Safe for concurrent use. Acquires read lock.
func (h *ToolCallHistory) GetSimilarity(tool string, queryTerms map[string]bool) (float64, *ToolCallSignature) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// P1.1 Fix: Use tool index to only iterate relevant calls
	indices := h.toolIndex[tool]
	if len(indices) == 0 {
		return 0.0, nil
	}

	var maxSimilarity float64
	var mostSimilarIdx int = -1

	for _, idx := range indices {
		similarity := JaccardSimilarity(queryTerms, h.calls[idx].QueryTerms)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
			mostSimilarIdx = idx
		}
	}

	// I1.1 Fix: Return a copy of the signature, not a pointer into the slice
	if mostSimilarIdx >= 0 {
		callCopy := h.calls[mostSimilarIdx]
		return maxSimilarity, &callCopy
	}

	return maxSimilarity, nil
}

// IsExactDuplicate checks if a call is an exact duplicate of a prior call.
//
// Description:
//
//	An exact duplicate is same tool + same raw query (case-insensitive).
//	P1.3 Fix: Uses EqualFold directly without redundant ToLower.
//	I1.1 Fix: Returns copy instead of pointer to avoid data races.
//	P1.1 Fix: Uses tool index to only check relevant calls.
//
// Inputs:
//
//	tool - The tool name.
//	rawQuery - The raw query string.
//
// Outputs:
//
//	bool - True if an exact duplicate exists.
//	*ToolCallSignature - Copy of the duplicate call, or nil if none.
//
// Thread Safety: Safe for concurrent use. Acquires read lock.
func (h *ToolCallHistory) IsExactDuplicate(tool, rawQuery string) (bool, *ToolCallSignature) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// P1.1 Fix: Use tool index to only iterate relevant calls
	indices := h.toolIndex[tool]
	if len(indices) == 0 {
		return false, nil
	}

	// P1.3 Fix: TrimSpace once, use EqualFold for case-insensitive comparison
	trimmedQuery := strings.TrimSpace(rawQuery)

	for _, idx := range indices {
		if strings.EqualFold(strings.TrimSpace(h.calls[idx].RawQuery), trimmedQuery) {
			// I1.1 Fix: Return a copy
			callCopy := h.calls[idx]
			return true, &callCopy
		}
	}

	return false, nil
}

// Len returns the number of recorded calls.
//
// Thread Safety: Safe for concurrent use.
func (h *ToolCallHistory) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.calls)
}

// Clear removes all recorded calls and resets the tool index.
//
// Thread Safety: Safe for concurrent use. Acquires write lock.
func (h *ToolCallHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = h.calls[:0]
	h.toolIndex = make(map[string][]int)
	semanticHistorySize.Set(0)
}

// Trim reduces the history to the specified size, keeping the most recent entries.
//
// Description:
//
//	R1.2 Fix: Allows external callers to trim history if needed.
//	Rebuilds the tool index after trimming.
//
// Inputs:
//
//	size - Maximum number of entries to keep. If <= 0, clears all entries.
//
// Thread Safety: Safe for concurrent use. Acquires write lock.
func (h *ToolCallHistory) Trim(size int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if size <= 0 {
		h.calls = h.calls[:0]
		h.toolIndex = make(map[string][]int)
		semanticHistorySize.Set(0)
		return
	}

	if len(h.calls) > size {
		excess := len(h.calls) - size
		h.calls = h.calls[excess:]
		h.rebuildIndexLocked()
	}
	semanticHistorySize.Set(float64(len(h.calls)))
}

// MaxSize returns the maximum history size.
//
// Thread Safety: Safe for concurrent use (reads immutable field).
func (h *ToolCallHistory) MaxSize() int {
	return h.maxSize
}

// Config returns the semantic configuration.
//
// Thread Safety: Safe for concurrent use (returns copy of immutable config).
func (h *ToolCallHistory) Config() SemanticConfig {
	return h.config
}

// =============================================================================
// Semantic Similarity Functions
// =============================================================================

// delimiterSet marks characters that should be treated as word delimiters.
// P1.2 Fix: Package-level set avoids allocation per call.
var delimiterSet = [256]bool{
	'_': true, '-': true, '.': true, '/': true, '\\': true, ':': true,
	' ': true, '\t': true, '\n': true, '\r': true,
}

// ExtractQueryTerms extracts terms from a query string for Jaccard comparison.
//
// Description:
//
//	Tokenizes the query into lowercase terms, normalizing for comparison.
//	Handles common delimiters like spaces, underscores, and camelCase.
//	R1.4 Fix: Uses unicode.IsUpper for proper Unicode handling.
//	P1.2 Fix: Single-pass algorithm reduces allocations.
//
// Inputs:
//
//	query - The query string to tokenize.
//
// Outputs:
//
//	map[string]bool - Set of unique lowercase terms.
//
// Thread Safety: Safe for concurrent use (no shared state modified).

// stemTerm applies minimal suffix stripping to normalize singular/plural
// and verb forms for BM25 matching. This is a lightweight Porter-lite stemmer
// that handles the most common English suffixes without a full stemming library.
//
// IT-16/ISSUE-01: Without stemming, "extend" (query) ≠ "extends" (keyword)
// causes BM25 to miss find_implementations entirely. The prefilter then
// excludes it from the candidate set, and the router's correct selection
// gets rejected by the validation layer.
//
// Rules (applied in order, first match wins):
//   - "ies" → "y" (dependencies → dependency)
//   - "ches"/"shes"/"sses" → drop "es" (matches → match)
//   - "ses" → drop "s" (analyses → analyse) — but not "sses" (already handled)
//   - "es" → drop "es" only if stem ≥ 3 chars (classes → class)
//   - "ing" → drop "ing" only if stem ≥ 3 chars (calling → call)
//   - "ed" → drop "ed" only if stem ≥ 3 chars (called → call)
//   - "s" → drop "s" only if stem ≥ 3 chars and not ending in "ss" (extends → extend)
func stemTerm(word string) string {
	n := len(word)
	if n <= 3 {
		return word
	}

	// "ies" → "y"
	if n > 4 && strings.HasSuffix(word, "ies") {
		return word[:n-3] + "y"
	}
	// "ches", "shes", "sses" → drop "es"
	if n > 4 && (strings.HasSuffix(word, "ches") || strings.HasSuffix(word, "shes") || strings.HasSuffix(word, "sses")) {
		return word[:n-2]
	}
	// "nces" → drop "s" only (references → reference, not referenc)
	if n > 4 && strings.HasSuffix(word, "nces") {
		return word[:n-1]
	}
	// "ses" → drop "s" (but not "sses", handled above)
	if n > 4 && strings.HasSuffix(word, "ses") {
		return word[:n-1]
	}
	// "es" → drop "es" if stem ≥ 3
	if n > 4 && strings.HasSuffix(word, "es") {
		return word[:n-2]
	}
	// "ing" → drop "ing" if stem ≥ 3
	if n > 5 && strings.HasSuffix(word, "ing") {
		return word[:n-3]
	}
	// "ed" → drop "ed" if stem ≥ 3
	if n > 4 && strings.HasSuffix(word, "ed") {
		return word[:n-2]
	}
	// "s" → drop "s" if stem ≥ 3 and not "ss"
	if strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && n > 3 {
		return word[:n-1]
	}

	return word
}

func ExtractQueryTerms(query string) map[string]bool {
	terms := make(map[string]bool)

	if query == "" {
		return terms
	}

	// P1.2 Fix: Single-pass extraction with camelCase splitting and delimiter handling
	// Pre-allocate builder with estimated capacity
	var wordBuilder strings.Builder
	wordBuilder.Grow(32)

	var prevWasUpper bool
	var prevWasDelim bool = true // Start as if after delimiter

	flushWord := func() {
		if wordBuilder.Len() >= 2 {
			word := wordBuilder.String()
			if !noiseWords[word] {
				terms[stemTerm(word)] = true
			}
		}
		wordBuilder.Reset()
	}

	for _, r := range query {
		// Check if this is a delimiter (ASCII only for delimiters)
		isDelim := r < 256 && delimiterSet[byte(r)]
		if isDelim {
			flushWord()
			prevWasDelim = true
			prevWasUpper = false
			continue
		}

		// R1.4 Fix: Use unicode.IsUpper for proper Unicode handling
		isUpper := unicode.IsUpper(r)

		// Split on camelCase boundary (lowercase -> uppercase transition)
		if isUpper && !prevWasUpper && !prevWasDelim {
			flushWord()
		}

		// Write lowercase rune
		wordBuilder.WriteRune(unicode.ToLower(r))
		prevWasUpper = isUpper
		prevWasDelim = false
	}

	// Flush final word
	flushWord()

	return terms
}

// JaccardSimilarity calculates the Jaccard similarity between two term sets.
//
// Description:
//
//	Jaccard = |intersection| / |union|
//	Returns 0.0 if either set is empty, 1.0 if identical.
//
// Inputs:
//
//	a, b - Term sets to compare. Nil maps are treated as empty.
//
// Outputs:
//
//	float64 - Similarity score in range [0.0, 1.0].
//
// Thread Safety: Safe for concurrent use (read-only on inputs).
func JaccardSimilarity(a, b map[string]bool) float64 {
	// S1.1 Fix: Handle nil maps gracefully
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	intersectionCount := 0
	for term := range a {
		if b[term] {
			intersectionCount++
		}
	}

	// |union| = |a| + |b| - |intersection|
	unionCount := len(a) + len(b) - intersectionCount

	if unionCount == 0 {
		return 0.0
	}

	return float64(intersectionCount) / float64(unionCount)
}

// ExtractQueryFromParams extracts the query/pattern parameter from tool params.
//
// Description:
//
//	Different tools use different parameter names for their "query" concept.
//	This function extracts the relevant parameter for semantic comparison.
//
// Inputs:
//
//	params - Map of parameter name to value.
//
// Outputs:
//
//	string - The query/pattern string, or empty if not found.
func ExtractQueryFromParams(params map[string]any) string {
	// Parameter names that typically contain the "query" concept
	queryParamNames := []string{
		"pattern",
		"query",
		"search",
		"symbol",
		"name",
		"path",
		"target",
		"function_name",
		"file_path",
		"type",
		"kind",
	}

	for _, paramName := range queryParamNames {
		if val, ok := params[paramName]; ok {
			if strVal, ok := val.(string); ok && strVal != "" {
				return strVal
			}
		}
	}

	return ""
}

// ExtractQueryFromMetadata extracts query from trace step metadata.
//
// Description:
//
//	Trace steps store parameters in Metadata as string values.
//	This function extracts the query for semantic comparison.
//
// Inputs:
//
//	metadata - Map of parameter name to string value.
//
// Outputs:
//
//	string - The query string, or empty if not found.
func ExtractQueryFromMetadata(metadata map[string]string) string {
	queryParamNames := []string{
		"pattern",
		"query",
		"search",
		"symbol",
		"name",
		"path",
		"target",
		"function_name",
		"file_path",
		"type",
		"kind",
	}

	for _, paramName := range queryParamNames {
		if val, ok := metadata[paramName]; ok && val != "" {
			return val
		}
	}

	return ""
}

// =============================================================================
// Semantic Similarity Thresholds
// =============================================================================

const (
	// SemanticDuplicateThreshold is the similarity threshold for blocking.
	// Calls with similarity >= this are considered semantic duplicates.
	SemanticDuplicateThreshold = 0.8

	// SemanticPenaltyThreshold is the similarity threshold for penalizing.
	// Calls with similarity >= this (but < duplicate threshold) get penalized.
	SemanticPenaltyThreshold = 0.3

	// DefaultSemanticPenaltyWeight is the default UCB1 penalty weight.
	// SemanticPenalty = similarity * weight.
	DefaultSemanticPenaltyWeight = 0.5
)

// CheckSemanticStatus evaluates a proposed call against history.
//
// Description:
//
//	Returns the semantic status of a proposed tool call:
//	- Blocked: Similarity >= DuplicateThreshold (semantic duplicate)
//	- Penalized: Similarity >= PenaltyThreshold (similar but not duplicate)
//	- Allowed: Similarity < PenaltyThreshold (sufficiently different)
//	S1.3 Fix: Uses configurable thresholds from history's config.
//	O1.1 Fix: Records Prometheus metrics.
//
// Inputs:
//
//	history - The call history to check against. If nil, returns "allowed".
//	tool - The proposed tool name. If empty, returns "allowed".
//	rawQuery - The raw query string.
//
// Outputs:
//
//	status - "blocked", "penalized", or "allowed".
//	similarity - The maximum similarity found.
//	similarCall - Copy of the most similar prior call, or nil.
//
// Thread Safety: Safe for concurrent use.
func CheckSemanticStatus(history *ToolCallHistory, tool, rawQuery string) (status string, similarity float64, similarCall *ToolCallSignature) {
	// S1.1 Fix: Validate inputs
	if history == nil {
		return "allowed", 0.0, nil
	}
	if tool == "" {
		return "allowed", 0.0, nil
	}

	// O1.1 Fix: Track check duration
	timer := prometheus.NewTimer(semanticCheckDuration)
	defer timer.ObserveDuration()

	// S1.3 Fix: Use configurable thresholds
	config := history.Config()

	// Check for exact duplicate first (fast path)
	if isDup, dupCall := history.IsExactDuplicate(tool, rawQuery); isDup {
		semanticChecksTotal.WithLabelValues("blocked").Inc()
		semanticSimilarityHistogram.Observe(1.0)
		return "blocked", 1.0, dupCall
	}

	// Check semantic similarity
	queryTerms := ExtractQueryTerms(rawQuery)
	similarity, similarCall = history.GetSimilarity(tool, queryTerms)

	// O1.1 Fix: Record similarity distribution
	semanticSimilarityHistogram.Observe(similarity)

	if similarity >= config.DuplicateThreshold {
		semanticChecksTotal.WithLabelValues("blocked").Inc()
		return "blocked", similarity, similarCall
	}

	if similarity >= config.PenaltyThreshold {
		semanticChecksTotal.WithLabelValues("penalized").Inc()
		return "penalized", similarity, similarCall
	}

	semanticChecksTotal.WithLabelValues("allowed").Inc()
	return "allowed", similarity, similarCall
}
