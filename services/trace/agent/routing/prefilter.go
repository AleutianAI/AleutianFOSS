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
	"context"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/config"
)

// =============================================================================
// Prometheus Metrics (O2)
// =============================================================================

var (
	prefilterNarrowedCount = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "narrowed_count",
		Help:      "Number of tools after pre-filtering",
		Buckets:   []float64{1, 3, 5, 7, 10, 15, 20, 55},
	})

	prefilterForcedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "forced_total",
		Help:      "Total forced tool selections by rule type and tool",
	}, []string{"rule_type", "tool"})

	prefilterLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "latency_seconds",
		Help:      "Pre-filter execution latency",
		Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01},
	})

	prefilterRulesFired = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "rules_fired_total",
		Help:      "Total rules fired by type",
	}, []string{"rule_type"})

	prefilterPassthroughTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "passthrough_total",
		Help:      "Times pre-filter passed through unchanged",
	})

	// IT-06c: Hybrid scoring metrics.
	prefilterHybridMethodTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "hybrid_method_total",
		Help:      "Phase 3 scoring method used: hybrid (BM25+embedding), bm25_only (embedding unavailable), or passthrough (no registry)",
	}, []string{"method"})

	prefilterEmbeddingLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "embedding_latency_seconds",
		Help:      "Latency of the embedding similarity scoring call (Ollama)",
		Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1.0, 3.0},
	})

	prefilterBM25Latency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "bm25_latency_seconds",
		Help:      "Latency of the BM25 scoring call",
		Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01},
	})

	// CB-62: Embedding warm-up metrics.
	prefilterWarmupLatency = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "warmup_latency_seconds",
		Help:      "Time to complete synchronous embedding warm-up",
		Buckets:   []float64{0.0001, 0.001, 0.01, 0.1, 0.3, 1.0, 3.0, 10.0},
	})

	prefilterWarmupSource = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "trace",
		Subsystem: "prefilter",
		Name:      "warmup_source_total",
		Help:      "Source of warm-up vectors: badger_cache, ollama, timeout",
	}, []string{"source"})
)

// =============================================================================
// OTel Tracer
// =============================================================================

var prefilterTracer = otel.Tracer("aleutian.agent.routing.prefilter")

// =============================================================================
// PreFilter Types
// =============================================================================

// compiledPattern holds a pattern string alongside its pre-compiled regex (if applicable).
type compiledPattern struct {
	raw   string
	regex *regexp.Regexp // nil for substring-only patterns
}

// PreFilter narrows tool candidates before the LLM router classifies.
//
// Description:
//
//	Implements a 5-phase deterministic pipeline:
//	1. Forced mapping check (exact phrases → deterministic tool)
//	2. Negation detection (negation word + keyword proximity → redirect)
//	3. Keyword matching (registry keyword index → scored candidates)
//	4. Confusion pair resolution (pattern boost for ambiguous pairs)
//	5. Candidate selection (top N by score, floor at min)
//
// Inputs:
//
//	registry - The tool routing registry for keyword lookup. May be nil (passthrough).
//	cfg - Pre-filter configuration with rules. Must not be nil.
//	logger - Logger for structured output. Must not be nil.
//
// Thread Safety: Safe for concurrent use (all state is read-only after construction).
type PreFilter struct {
	registry *config.ToolRoutingRegistry
	cfg      *config.PreFilterConfig
	logger   *slog.Logger

	// IT-06c: Hybrid Phase 3 scoring components.
	// bm25mu is a read-write mutex protecting the bm25 pointer.
	// Multiple goroutines may read pf.bm25 concurrently (RLock); only the
	// one-time lazy rebuild writes to it (Lock). Using RWMutex prevents the
	// per-call read from serializing all concurrent prefilter invocations.
	bm25mu   sync.RWMutex        // guards bm25 pointer during lazy init
	bm25     *BM25Index          // BM25 lexical scorer; lazily built on first scored request.
	embedder *ToolEmbeddingCache // Semantic scorer; lazily warmed on first scored request.
	warmOnce sync.Once           // ensures embedding warm-up fires exactly once.

	// compiledForcedPatterns holds pre-compiled patterns per forced mapping index.
	compiledForcedPatterns [][]compiledPattern

	// compiledConfusionAPatterns holds pre-compiled tool_a_patterns per confusion pair.
	compiledConfusionAPatterns [][]compiledPattern

	// compiledConfusionBPatterns holds pre-compiled tool_b_patterns per confusion pair.
	compiledConfusionBPatterns [][]compiledPattern

	// compiledEncyclopediaPatterns holds pre-compiled intent patterns per encyclopedia entry.
	// CB-62 Rev 2.
	compiledEncyclopediaPatterns [][]compiledPattern
}

// PreFilterResult contains the output of a pre-filter operation.
//
// Description:
//
//	Holds either a forced tool selection (skip router) or a narrowed
//	set of candidates for the router to classify.
type PreFilterResult struct {
	// NarrowedSpecs is the filtered set of tool specs for the router.
	NarrowedSpecs []ToolSpec

	// ForcedTool is set when the pre-filter deterministically selects a tool.
	// When non-empty, the router should be skipped entirely.
	ForcedTool string

	// ForcedReason explains why the tool was forced.
	ForcedReason string

	// Scores maps tool name to its pre-filter score.
	Scores map[string]float64

	// AppliedRules lists the rules that fired during filtering.
	AppliedRules []string

	// OriginalCount is the number of tools before filtering.
	OriginalCount int

	// NarrowedCount is the number of tools after filtering.
	NarrowedCount int

	// Duration is how long the pre-filter took.
	Duration time.Duration
}

// NewPreFilter creates a new PreFilter.
//
// Description:
//
//	Creates a pre-filter with the given registry and configuration.
//	If registry is nil, keyword matching (Phase 3) falls back to legacy
//	BestFor substring matching.
//
//	IT-06c: BM25 and embedding components are lazily initialized on the first
//	Filter/FilterAgentSpecs call that provides non-empty tool specs. The embedding
//	warm-up runs once in a background goroutine; Phase 3 degrades gracefully to
//	BM25-only while warm-up is in progress.
//
//	GR-61: If store is non-nil, the embedding cache will load pre-computed
//	vectors from BadgerDB on warm-up (skipping Ollama) and persist newly
//	computed vectors for future service restarts. Pass nil for tests and for
//	deployments without a routing cache directory.
//
// Inputs:
//
//	registry - Tool routing registry for keyword lookup. May be nil.
//	cfg      - Pre-filter configuration. Must not be nil.
//	logger   - Logger instance. Must not be nil.
//	store    - Optional BadgerDB embedding cache store. Nil disables persistence.
//
// Outputs:
//
//	*PreFilter - The constructed pre-filter.
//
// Thread Safety: The returned PreFilter is safe for concurrent use.
func NewPreFilter(registry *config.ToolRoutingRegistry, cfg *config.PreFilterConfig, logger *slog.Logger, store RouterCacheStore) *PreFilter {
	if cfg == nil {
		cfg = &config.PreFilterConfig{
			Enabled:           false,
			MinCandidates:     config.DefaultMinCandidates,
			MaxCandidates:     config.DefaultMaxCandidates,
			NegationProximity: config.DefaultNegationProximity,
		}
	}
	if logger == nil {
		logger = slog.Default()
	}

	pf := &PreFilter{
		registry: registry,
		cfg:      cfg,
		logger:   logger,
		embedder: NewToolEmbeddingCache(logger, store),
		bm25:     BuildBM25Index(nil), // empty; replaced on first scored call
	}

	// Pre-compile regex patterns for forced mappings.
	pf.compiledForcedPatterns = make([][]compiledPattern, len(cfg.ForcedMappings))
	for i, fm := range cfg.ForcedMappings {
		pf.compiledForcedPatterns[i] = compilePatterns(fm.Patterns, logger)
	}

	// Pre-compile regex patterns for confusion pairs.
	pf.compiledConfusionAPatterns = make([][]compiledPattern, len(cfg.ConfusionPairs))
	pf.compiledConfusionBPatterns = make([][]compiledPattern, len(cfg.ConfusionPairs))
	for i, cp := range cfg.ConfusionPairs {
		pf.compiledConfusionAPatterns[i] = compilePatterns(cp.ToolAPatterns, logger)
		pf.compiledConfusionBPatterns[i] = compilePatterns(cp.ToolBPatterns, logger)
	}

	// CB-62 Rev 2: Pre-compile regex patterns for routing encyclopedia entries.
	pf.compiledEncyclopediaPatterns = make([][]compiledPattern, len(cfg.RoutingEncyclopedia))
	for i, entry := range cfg.RoutingEncyclopedia {
		patterns := make([]string, len(entry.Intents))
		for j, intent := range entry.Intents {
			patterns[j] = intent.Pattern
		}
		pf.compiledEncyclopediaPatterns[i] = compilePatterns(patterns, logger)
	}

	return pf
}

// compilePatterns pre-compiles a list of patterns, treating ".*" patterns as regex.
func compilePatterns(patterns []string, logger *slog.Logger) []compiledPattern {
	result := make([]compiledPattern, len(patterns))
	for i, pattern := range patterns {
		patternLower := strings.ToLower(pattern)
		cp := compiledPattern{raw: patternLower}
		if strings.Contains(patternLower, ".*") {
			re, err := regexp.Compile("(?i)" + patternLower)
			if err != nil {
				logger.Warn("prefilter: invalid regex pattern, will skip",
					slog.String("pattern", pattern),
					slog.String("error", err.Error()),
				)
			} else {
				cp.regex = re
			}
		}
		result[i] = cp
	}
	return result
}

// Filter narrows the tool candidate set based on query analysis.
//
// Description:
//
//	Runs the 5-phase pipeline to either force a tool selection or
//	narrow the candidate set for the LLM router. Returns all specs
//	unchanged (passthrough) when disabled, query is empty, or no
//	rules match.
//
//	IT-06c: Phase 3 now uses hybrid BM25 + embedding scoring instead of
//	plain keyword substring counting. sessionCounts provides per-tool
//	selection counts for the current session; tools already selected
//	receive a UCB1 exploration penalty. Pass nil to disable the penalty.
//
// Inputs:
//
//	ctx           - Context for tracing and cancellation. Must not be nil.
//	query         - The user's query string.
//	allSpecs      - All available tool specs.
//	sessionCounts - Current session tool selection counts (tool → count). May be nil.
//
// Outputs:
//
//	*PreFilterResult - The filtering result with narrowed specs or forced tool.
//
// Thread Safety: Safe for concurrent use.
func (pf *PreFilter) Filter(ctx context.Context, query string, allSpecs []ToolSpec, sessionCounts map[string]int) *PreFilterResult {
	start := time.Now()

	ctx, span := prefilterTracer.Start(ctx, "routing.PreFilter.Filter")
	defer span.End()

	result := &PreFilterResult{
		NarrowedSpecs: allSpecs,
		Scores:        make(map[string]float64),
		OriginalCount: len(allSpecs),
	}

	// Passthrough conditions
	if !pf.cfg.Enabled || len(query) == 0 || len(allSpecs) == 0 {
		result.NarrowedCount = len(allSpecs)
		result.Duration = time.Since(start)
		prefilterPassthroughTotal.Inc()
		span.SetAttributes(
			attribute.Bool("passthrough", true),
			attribute.String("reason", pf.passthroughReason(query, allSpecs)),
		)
		return result
	}

	queryLower := strings.ToLower(query)

	// Build spec index for lookup
	specIndex := make(map[string]ToolSpec, len(allSpecs))
	for _, spec := range allSpecs {
		specIndex[spec.Name] = spec
	}

	// Phase 0: Routing Encyclopedia (CB-62 Rev 2)
	encycForcedTool, encycBoosts, encycHints := pf.applyEncyclopedia(queryLower)
	if encycForcedTool != "" {
		// Validate forced tool exists in the available spec set
		if _, exists := specIndex[encycForcedTool]; exists {
			result.ForcedTool = encycForcedTool
			result.ForcedReason = "routing_encyclopedia:force"
			result.AppliedRules = append(result.AppliedRules, "encyclopedia_force:"+encycForcedTool)
			result.NarrowedCount = 1
			result.Duration = time.Since(start)

			prefilterForcedTotal.WithLabelValues("encyclopedia_force", encycForcedTool).Inc()
			prefilterRulesFired.WithLabelValues("encyclopedia_force").Inc()
			prefilterLatency.Observe(result.Duration.Seconds())
			prefilterNarrowedCount.Observe(1)

			span.SetAttributes(
				attribute.String("forced_tool", encycForcedTool),
				attribute.String("forced_reason", "routing_encyclopedia:force"),
				attribute.Int("original_count", result.OriginalCount),
			)

			pf.logger.Info("prefilter encyclopedia force selection",
				slog.String("tool", encycForcedTool),
				slog.String("query_preview", truncateForLog(query, 80)),
			)
			return result
		}
		pf.logger.Warn("prefilter encyclopedia forced tool not in spec set, skipping",
			slog.String("tool", encycForcedTool),
		)
	}

	// Phase 1: Forced mapping check
	if tool, reason, matched := pf.checkForcedMappings(queryLower); matched {
		// Validate forced tool exists in the available spec set
		if _, exists := specIndex[tool]; !exists {
			pf.logger.Warn("prefilter forced mapping tool not in spec set, skipping",
				slog.String("tool", tool),
				slog.String("reason", reason),
			)
		} else {
			result.ForcedTool = tool
			result.ForcedReason = reason
			result.AppliedRules = append(result.AppliedRules, "forced_mapping:"+tool)
			result.NarrowedCount = 1
			result.Duration = time.Since(start)

			prefilterForcedTotal.WithLabelValues("forced_mapping", tool).Inc()
			prefilterRulesFired.WithLabelValues("forced_mapping").Inc()
			prefilterLatency.Observe(result.Duration.Seconds())
			prefilterNarrowedCount.Observe(1)

			span.SetAttributes(
				attribute.String("forced_tool", tool),
				attribute.String("forced_reason", reason),
				attribute.Int("original_count", result.OriginalCount),
			)

			pf.logger.Info("prefilter forced selection",
				slog.String("tool", tool),
				slog.String("reason", reason),
				slog.String("query_preview", truncateForLog(query, 80)),
			)
			return result
		}
	}

	// Phase 2: Negation detection
	if tool, reason, matched := pf.checkNegationRules(queryLower); matched {
		// Validate forced tool exists in the available spec set
		if _, exists := specIndex[tool]; !exists {
			pf.logger.Warn("prefilter negation tool not in spec set, skipping",
				slog.String("tool", tool),
				slog.String("reason", reason),
			)
		} else {
			result.ForcedTool = tool
			result.ForcedReason = reason
			result.AppliedRules = append(result.AppliedRules, "negation:"+tool)
			result.NarrowedCount = 1
			result.Duration = time.Since(start)

			prefilterForcedTotal.WithLabelValues("negation", tool).Inc()
			prefilterRulesFired.WithLabelValues("negation").Inc()
			prefilterLatency.Observe(result.Duration.Seconds())
			prefilterNarrowedCount.Observe(1)

			span.SetAttributes(
				attribute.String("forced_tool", tool),
				attribute.String("forced_reason", reason),
				attribute.Int("original_count", result.OriginalCount),
			)

			pf.logger.Info("prefilter negation forced",
				slog.String("tool", tool),
				slog.String("reason", reason),
				slog.String("query_preview", truncateForLog(query, 80)),
			)
			return result
		}
	}

	// Phase 3: Hybrid scoring (BM25 + embedding + UCB1 session penalty).
	scores := pf.scoreHybrid(ctx, queryLower, allSpecs, sessionCounts)
	for k, v := range scores {
		result.Scores[k] = v
	}
	if len(scores) > 0 {
		result.AppliedRules = append(result.AppliedRules, "hybrid_scoring")
		prefilterRulesFired.WithLabelValues("hybrid_scoring").Inc()
	}

	// Phase 3.5: Apply encyclopedia boosts (CB-62 Rev 2).
	// Boosts are applied after embedding scoring to nudge tools into the
	// candidate set without overriding the embedding model's signal.
	if len(encycBoosts) > 0 && result.Scores != nil {
		for tool, boost := range encycBoosts {
			result.Scores[tool] += boost
			result.AppliedRules = append(result.AppliedRules, "encyclopedia_boost:"+tool)
		}
		prefilterRulesFired.WithLabelValues("encyclopedia_boost").Inc()
	}

	// Phase 4: Confusion pair resolution
	pf.resolveConfusionPairs(queryLower, result.Scores, result)

	// Phase 5: Candidate selection
	narrowed := pf.selectCandidates(result.Scores, allSpecs)

	// Phase 5.5: Ensure encyclopedia hints are in the candidate set (CB-62 Rev 2).
	if len(encycHints) > 0 {
		narrowedSet := make(map[string]bool, len(narrowed))
		for _, s := range narrowed {
			narrowedSet[s.Name] = true
		}
		for _, hint := range encycHints {
			if !narrowedSet[hint] {
				if spec, exists := specIndex[hint]; exists {
					narrowed = append(narrowed, spec)
					result.AppliedRules = append(result.AppliedRules, "encyclopedia_hint:"+hint)
				}
			}
		}
		if len(encycHints) > 0 {
			prefilterRulesFired.WithLabelValues("encyclopedia_hint").Inc()
		}
	}
	result.NarrowedSpecs = narrowed
	result.NarrowedCount = len(narrowed)
	result.Duration = time.Since(start)

	prefilterLatency.Observe(result.Duration.Seconds())
	prefilterNarrowedCount.Observe(float64(result.NarrowedCount))

	span.SetAttributes(
		attribute.Int("original_count", result.OriginalCount),
		attribute.Int("narrowed_count", result.NarrowedCount),
		attribute.Int("rules_fired", len(result.AppliedRules)),
	)

	if result.NarrowedCount < result.OriginalCount {
		pf.logger.Info("prefilter narrowed candidates",
			slog.Int("original", result.OriginalCount),
			slog.Int("narrowed", result.NarrowedCount),
			slog.String("query_preview", truncateForLog(query, 80)),
		)
	} else {
		prefilterPassthroughTotal.Inc()
	}

	return result
}

// =============================================================================
// Phase 1: Forced Mapping Check
// =============================================================================

// checkForcedMappings checks if the query matches any forced mapping patterns.
//
// Description:
//
//	Iterates through forced mappings and checks if any pattern is a substring
//	of the query. Patterns containing ".*" are treated as regex.
//
// Inputs:
//
//	queryLower - Lowercase query string.
//
// Outputs:
//
//	tool - The forced tool name, or "" if no match.
//	reason - The reason for forcing, or "" if no match.
//	matched - True if a forced mapping matched.
func (pf *PreFilter) checkForcedMappings(queryLower string) (tool string, reason string, matched bool) {
	for i, fm := range pf.cfg.ForcedMappings {
		if i >= len(pf.compiledForcedPatterns) {
			break
		}
		for _, cp := range pf.compiledForcedPatterns[i] {
			if matchCompiledPattern(queryLower, cp) {
				return fm.Tool, fm.Reason, true
			}
		}
	}
	return "", "", false
}

// matchCompiledPattern checks if a query matches a pre-compiled pattern.
func matchCompiledPattern(queryLower string, cp compiledPattern) bool {
	if cp.regex != nil {
		return cp.regex.MatchString(queryLower)
	}
	return strings.Contains(queryLower, cp.raw)
}

// =============================================================================
// Phase 2: Negation Detection
// =============================================================================

// checkNegationRules detects negation patterns in the query.
//
// Description:
//
//	Tokenizes the query and checks if any negation word appears within
//	NegationProximity words before a trigger keyword. Multi-word trigger
//	keywords are matched as contiguous subsequences.
//
// Algorithm:
//
//  1. Tokenize: words = lowercase(query).split()
//  2. For each NegationRule:
//     a. Find all positions of negation words in tokens
//     b. Find all positions of trigger keywords (multi-word → contiguous subsequence)
//     c. For each (neg_pos, kw_pos) pair where kw_pos > neg_pos:
//     if (kw_pos - neg_pos) ≤ negation_proximity → MATCH
//     d. On match: return (CorrectTool, Reason, true)
//  3. No match → return ("", "", false)
//
// Inputs:
//
//	queryLower - Lowercase query string.
//
// Outputs:
//
//	tool - The correct tool to use, or "" if no match.
//	reason - The reason for the correction, or "" if no match.
//	matched - True if a negation pattern was detected.
func (pf *PreFilter) checkNegationRules(queryLower string) (tool string, reason string, matched bool) {
	words := strings.Fields(queryLower)
	if len(words) == 0 {
		return "", "", false
	}

	for _, rule := range pf.cfg.NegationRules {
		// Find positions of negation words
		var negPositions []int
		for i, word := range words {
			for _, negWord := range rule.NegationWords {
				if word == negWord {
					negPositions = append(negPositions, i)
					break
				}
			}
		}
		if len(negPositions) == 0 {
			continue
		}

		// Find positions of trigger keywords
		var kwPositions []int
		for _, kw := range rule.TriggerKeywords {
			kwLower := strings.ToLower(kw)
			kwWords := strings.Fields(kwLower)
			if len(kwWords) == 0 {
				continue
			}

			if len(kwWords) == 1 {
				// Single-word keyword
				for i, word := range words {
					if word == kwWords[0] {
						kwPositions = append(kwPositions, i)
					}
				}
			} else {
				// Multi-word keyword: find contiguous subsequence
				for i := 0; i <= len(words)-len(kwWords); i++ {
					match := true
					for j, kw := range kwWords {
						if words[i+j] != kw {
							match = false
							break
						}
					}
					if match {
						kwPositions = append(kwPositions, i)
					}
				}
			}
		}

		// Check proximity
		for _, negPos := range negPositions {
			for _, kwPos := range kwPositions {
				if kwPos > negPos {
					dist := kwPos - negPos
					if dist <= pf.cfg.NegationProximity {
						return rule.CorrectTool, rule.Reason, true
					}
				}
			}
		}
	}

	return "", "", false
}

// =============================================================================
// Phase 3: Hybrid Scoring (IT-06c)
// =============================================================================

// scoreHybrid scores tools using the configured scoring mode with UCB1
// session penalty. Replaces the old scoreByKeywords substring counting.
//
// # Description
//
// CB-62: Scoring mode is controlled by cfg.ScoringMode:
//
//   - "embedding_primary" (default): Pure embedding cosine similarity [0,1].
//     BM25 is still computed for OTel span debugging but does NOT affect
//     ranking. When embeddings are unavailable (Ollama dead), returns nil
//     scores → selectCandidates passes ALL tools to the router.
//   - "hybrid" (legacy): 0.4 × BM25 + 0.6 × embedding blend. Falls back to
//     BM25-only if the embedder is not warmed or the Ollama call fails.
//
// Both modes apply UCB1 session penalty (step 4):
//  1. BM25 scoring (computed in both modes; used only in hybrid mode).
//  2. Embedding similarity (requires Ollama): cosine similarity [0,1].
//  3. Mode switch: embedding_primary → pure embedding or nil passthrough;
//     hybrid → 0.4 BM25 + 0.6 embedding blend.
//  4. UCB1 session penalty: subtract 0.15 per prior selection of each tool
//     in the current session, floored at 0. Encourages exploration.
//     Skipped when scores are nil (passthrough mode).
//
// # Inputs
//
//   - ctx: Context for the embedding HTTP call.
//   - queryLower: Lowercase query string.
//   - allSpecs: All available tool specs (used only for BestFor fallback).
//   - sessionCounts: Per-tool selection counts for this session. May be nil.
//
// # Outputs
//
//   - map[string]float64: Tool name → score. Nil in passthrough mode (embeddings unavailable).
func (pf *PreFilter) scoreHybrid(ctx context.Context, queryLower string, allSpecs []ToolSpec, sessionCounts map[string]int) map[string]float64 {
	// --- Lazy corpus init (one-time, double-checked) ---
	// On the first call that provides non-empty specs, build the BM25 index
	// and kick off the background embedding warm-up exactly once.
	//
	// warmOnce.Do is called AFTER releasing bm25mu to avoid nesting a sync.Once
	// inside an external lock. specsForWarm is captured while the write lock is
	// held (before bm25mu.Unlock) so it's safe to use in the goroutine.
	var specsForWarm []ToolSpec
	if len(allSpecs) > 0 {
		// Fast path: read lock to check emptiness without blocking other readers.
		pf.bm25mu.RLock()
		isEmpty := pf.bm25.IsEmpty()
		pf.bm25mu.RUnlock()

		if isEmpty {
			pf.bm25mu.Lock()
			// Double-check: another goroutine may have built it while we waited.
			if pf.bm25.IsEmpty() {
				pf.bm25 = BuildBM25Index(allSpecs)
				pf.logger.Info("prefilter: BM25 corpus built",
					slog.Int("tool_count", len(allSpecs)),
				)
				// Capture allSpecs snapshot here, under the write lock, for use
				// in the warmOnce goroutine below (after the lock is released).
				specsForWarm = allSpecs
			}
			pf.bm25mu.Unlock()
		}
	}

	// CB-62: Synchronous embedding warm-up with bounded timeout.
	// Belt-and-suspenders: startup already warms, but this protects against
	// edge cases where scoreHybrid is called before startup completes.
	if specsForWarm != nil {
		pf.warmOnce.Do(func() {
			warmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			warmStart := time.Now()
			if err := pf.embedder.Warm(warmCtx, specsForWarm); err != nil {
				pf.logger.Warn("prefilter: embedding warm-up failed, will passthrough all tools",
					slog.String("error", err.Error()),
				)
				prefilterWarmupSource.WithLabelValues("timeout").Inc()
			} else {
				prefilterWarmupSource.WithLabelValues("ollama").Inc()
			}
			prefilterWarmupLatency.Observe(time.Since(warmStart).Seconds())
		})
	}

	// --- Embedding ---
	embStart := time.Now()
	embScores, _ := pf.embedder.Score(ctx, queryLower) // nil on graceful degradation
	prefilterEmbeddingLatency.Observe(time.Since(embStart).Seconds())

	// --- Scoring mode switch (CB-62) ---
	var scores map[string]float64
	if pf.cfg.ScoringMode == "embedding_primary" {
		// CB-62: In embedding_primary mode, BM25 is never used for ranking.
		// Skip BM25 computation entirely to avoid wasted CPU.
		if embScores != nil {
			// Normal path: pure embedding scoring.
			scores = embScores
			prefilterHybridMethodTotal.WithLabelValues("embedding_primary").Inc()
		} else {
			// Degraded path: Ollama unavailable.
			// DO NOT use BM25 — it cannot handle typos, misspellings, or synonyms.
			// Return nil scores → selectCandidates will passthrough ALL tools to
			// the router. granite4:micro-h handles 55 tools at ~11K tokens fine.
			scores = nil
			prefilterHybridMethodTotal.WithLabelValues("passthrough").Inc()
			pf.logger.Warn("prefilter: embeddings unavailable, passing all tools to router")
		}
	} else {
		// Legacy hybrid mode (backward compat): 0.4 BM25 + 0.6 embedding.
		// BM25 is only computed in hybrid mode.
		pf.bm25mu.RLock()
		bm25idx := pf.bm25
		pf.bm25mu.RUnlock()

		bm25Start := time.Now()
		bm25Scores := bm25idx.Score(queryLower)
		prefilterBM25Latency.Observe(time.Since(bm25Start).Seconds())

		// Fall back to legacy keyword counting only when the BM25 corpus is empty
		// (service startup race: allSpecs arrived but BM25 hasn't been built yet).
		if len(bm25Scores) == 0 && bm25idx.IsEmpty() {
			bm25Scores = pf.scoreByKeywordsLegacy(queryLower, allSpecs)
		}

		if embScores == nil {
			// BM25-only mode: Ollama unavailable or not yet warmed.
			scores = bm25Scores
			prefilterHybridMethodTotal.WithLabelValues("bm25_only").Inc()
		} else {
			// Collect all tool names present in either score set.
			allTools := make(map[string]struct{}, len(bm25Scores)+len(embScores))
			for t := range bm25Scores {
				allTools[t] = struct{}{}
			}
			for t := range embScores {
				allTools[t] = struct{}{}
			}

			scores = make(map[string]float64, len(allTools))
			const alphaBM25 = 0.4
			const alphaEmb = 0.6
			for t := range allTools {
				blended := alphaBM25*bm25Scores[t] + alphaEmb*embScores[t]
				if blended > 0 {
					scores[t] = blended
				}
			}
			prefilterHybridMethodTotal.WithLabelValues("hybrid").Inc()
		}
	}

	// --- UCB1 session penalty (Option K) ---
	// Tools used more often in this session get progressively penalized,
	// encouraging the router to explore alternatives.
	// CB-62: Skip when scores is nil (passthrough mode — no ranking to penalize).
	if sessionCounts != nil && scores != nil {
		const penaltyPerUse = 0.15
		for tool, s := range scores {
			n := sessionCounts[tool]
			if n > 0 {
				scores[tool] = math.Max(0, s-penaltyPerUse*float64(n))
			}
		}
	}

	return scores
}

// scoreByKeywordsLegacy is the original keyword substring scoring kept as a
// fallback when BM25 produces no results (e.g., empty specs at startup).
// It preserves pre-IT-06c behavior exactly.
func (pf *PreFilter) scoreByKeywordsLegacy(queryLower string, allSpecs []ToolSpec) map[string]float64 {
	scores := make(map[string]float64)

	if pf.registry != nil {
		matches := pf.registry.FindToolsByKeyword(queryLower)
		for _, m := range matches {
			scores[m.ToolName] = float64(m.MatchCount)
		}
	} else {
		for _, spec := range allSpecs {
			count := 0
			for _, kw := range spec.BestFor {
				if strings.Contains(queryLower, strings.ToLower(kw)) {
					count++
				}
			}
			if count > 0 {
				scores[spec.Name] = float64(count)
			}
		}
	}

	return scores
}

// =============================================================================
// Phase 4: Confusion Pair Resolution
// =============================================================================

// resolveConfusionPairs applies confusion pair boosts based on pattern matching.
//
// Description:
//
//	For each confusion pair, checks query patterns to determine which tool
//	to boost. A boost is applied when exactly one side's patterns match,
//	regardless of whether the tools already have keyword scores. Patterns
//	containing ".*" are pre-compiled regex; otherwise they are substring matches.
//
// Mutation contract: This method mutates the scores map and appends to
// result.AppliedRules in-place. Callers must own both values.
//
// Inputs:
//
//	queryLower - Lowercase query string.
//	scores - Current scores to modify in-place.
//	result - PreFilterResult to append applied rules.
func (pf *PreFilter) resolveConfusionPairs(queryLower string, scores map[string]float64, result *PreFilterResult) {
	// CB-62: Scale boost for embedding_primary mode where scores are cosine [0,1].
	// YAML boost_amount values (e.g., 3.0) were calibrated for the old hybrid blend
	// where BM25 keyword counts could be large. In embedding_primary mode, a raw
	// 3.0 boost on [0,1] scores completely overwhelms the ranking.
	// Scale factor: 0.1 → a YAML boost of 3.0 becomes 0.30 on the [0,1] scale.
	boostScale := 1.0
	if pf.cfg.ScoringMode == "embedding_primary" {
		boostScale = 0.1
	}

	for i, pair := range pf.cfg.ConfusionPairs {
		if i >= len(pf.compiledConfusionAPatterns) || i >= len(pf.compiledConfusionBPatterns) {
			break
		}

		aMatched := matchCompiledPatterns(queryLower, pf.compiledConfusionAPatterns[i])
		bMatched := matchCompiledPatterns(queryLower, pf.compiledConfusionBPatterns[i])

		boost := pair.BoostAmount * boostScale

		if aMatched && !bMatched {
			scores[pair.ToolA] += boost
			result.AppliedRules = append(result.AppliedRules, "confusion_pair_boost:"+pair.ToolA)
			prefilterRulesFired.WithLabelValues("confusion_pair").Inc()
		} else if bMatched && !aMatched {
			scores[pair.ToolB] += boost
			result.AppliedRules = append(result.AppliedRules, "confusion_pair_boost:"+pair.ToolB)
			prefilterRulesFired.WithLabelValues("confusion_pair").Inc()
		}
		// If both or neither match, no boost applied — let the router decide
	}
}

// matchCompiledPatterns checks if any pre-compiled pattern matches the query.
func matchCompiledPatterns(queryLower string, patterns []compiledPattern) bool {
	for _, cp := range patterns {
		if matchCompiledPattern(queryLower, cp) {
			return true
		}
	}
	return false
}

// =============================================================================
// Phase 5: Candidate Selection
// =============================================================================

// selectCandidates selects candidates using adaptive score-gap-based cutoff.
//
// Description:
//
//	CB-62: Replaces fixed top-N selection with adaptive window:
//	1. Passthrough: nil scores (embeddings unavailable) → return all specs to router.
//	2. Sort by score descending.
//	3. Score floor: drop tools below ScoreFloor (default 0.30).
//	4. Gap cutoff: starting at MinCandidates, find first gap > ScoreGapThreshold
//	   between consecutive scores. Cut there.
//	5. Cap at MaxCandidates as safety valve.
//	6. AlwaysInclude + MinCandidates fill (unchanged).
//
// Inputs:
//
//	scores - Tool scores from phases 3-4. Nil signals passthrough.
//	allSpecs - All available tool specs (for filling to min).
//
// Outputs:
//
//	[]ToolSpec - The narrowed candidate set.
func (pf *PreFilter) selectCandidates(scores map[string]float64, allSpecs []ToolSpec) []ToolSpec {
	// Passthrough: no scores means embeddings unavailable, let router see everything.
	if len(scores) == 0 {
		return allSpecs
	}

	// Sort tools by score descending.
	type scoredTool struct {
		name  string
		score float64
	}
	var sorted []scoredTool
	for name, score := range scores {
		sorted = append(sorted, scoredTool{name, score})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].name < sorted[j].name // Stable sort by name
	})

	// Apply score floor: drop tools below ScoreFloor.
	var aboveFloor []scoredTool
	for _, st := range sorted {
		if st.score >= pf.cfg.ScoreFloor {
			aboveFloor = append(aboveFloor, st)
		}
	}
	// Ensure at least MinCandidates (even if below floor).
	if len(aboveFloor) < pf.cfg.MinCandidates && len(sorted) > len(aboveFloor) {
		limit := pf.cfg.MinCandidates
		if limit > len(sorted) {
			limit = len(sorted)
		}
		aboveFloor = sorted[:limit]
	}

	// Find gap cutoff: starting at MinCandidates, scan for first gap > threshold.
	cutoff := len(aboveFloor)
	for i := pf.cfg.MinCandidates; i < len(aboveFloor); i++ {
		gap := aboveFloor[i-1].score - aboveFloor[i].score
		if gap > pf.cfg.ScoreGapThreshold {
			cutoff = i
			break
		}
	}
	// Cap at MaxCandidates.
	if cutoff > pf.cfg.MaxCandidates {
		cutoff = pf.cfg.MaxCandidates
	}

	selected := make(map[string]bool, cutoff)
	for i := 0; i < cutoff; i++ {
		selected[aboveFloor[i].name] = true
	}

	// Always include required tools.
	for _, name := range pf.cfg.AlwaysInclude {
		selected[name] = true
	}

	// If below MinCandidates, fill from allSpecs by original order.
	if len(selected) < pf.cfg.MinCandidates {
		for _, spec := range allSpecs {
			if len(selected) >= pf.cfg.MinCandidates {
				break
			}
			selected[spec.Name] = true
		}
	}

	// Build result preserving original order.
	var result []ToolSpec
	for _, spec := range allSpecs {
		if selected[spec.Name] {
			result = append(result, spec)
		}
	}

	return result
}

// =============================================================================
// Agent Type Adapter (CB-38: bridges routing.ToolSpec ↔ agent.ToolRouterSpec)
// =============================================================================

// AgentPreFilterResult contains the pre-filter output using agent package types.
//
// Description:
//
//	Same as PreFilterResult but uses agent.ToolRouterSpec instead of
//	routing.ToolSpec, for direct use in the execute phase.
type AgentPreFilterResult struct {
	// NarrowedSpecs is the filtered set of tool specs for the router.
	NarrowedSpecs []agent.ToolRouterSpec

	// ForcedTool is set when the pre-filter deterministically selects a tool.
	ForcedTool string

	// ForcedReason explains why the tool was forced.
	ForcedReason string

	// Duration is how long the pre-filter took.
	Duration time.Duration

	// AppliedRules lists the rules that fired during filtering.
	AppliedRules []string

	// OriginalCount is the number of tools before filtering.
	OriginalCount int

	// NarrowedCount is the number of tools after filtering.
	NarrowedCount int
}

// FilterAgentSpecs narrows agent.ToolRouterSpec candidates.
//
// Description:
//
//	Converts agent types to routing types, runs the pre-filter pipeline,
//	and converts results back. This is the primary integration point for
//	the execute phase.
//
//	IT-06c: sessionCounts provides per-tool selection counts for the current
//	session, used by Phase 3 UCB1 exploration penalty. Pass nil to disable.
//
// Inputs:
//
//	ctx           - Context for tracing.
//	query         - The user's query string.
//	allSpecs      - All available tool specs in agent format.
//	sessionCounts - Per-tool selection counts for this session. May be nil.
//
// Outputs:
//
//	*AgentPreFilterResult - The filtering result.
//
// Thread Safety: Safe for concurrent use.
func (pf *PreFilter) FilterAgentSpecs(ctx context.Context, query string, allSpecs []agent.ToolRouterSpec, sessionCounts map[string]int) *AgentPreFilterResult {
	// Convert agent specs to routing specs.
	routingSpecs := make([]ToolSpec, len(allSpecs))
	for i, s := range allSpecs {
		routingSpecs[i] = ToolSpec{
			Name:        s.Name,
			Description: s.Description,
			BestFor:     s.BestFor,
			Params:      s.Params,
			Category:    s.Category,
			UseWhen:     s.UseWhen,
			AvoidWhen:   s.AvoidWhen,
		}
	}

	// Run the pre-filter with session counts.
	pfResult := pf.Filter(ctx, query, routingSpecs, sessionCounts)

	// Convert narrowed specs back to agent format
	// Build index for O(1) lookup
	agentSpecIndex := make(map[string]agent.ToolRouterSpec, len(allSpecs))
	for _, s := range allSpecs {
		agentSpecIndex[s.Name] = s
	}

	narrowedAgent := make([]agent.ToolRouterSpec, 0, len(pfResult.NarrowedSpecs))
	for _, rs := range pfResult.NarrowedSpecs {
		if as, ok := agentSpecIndex[rs.Name]; ok {
			narrowedAgent = append(narrowedAgent, as)
		}
	}

	return &AgentPreFilterResult{
		NarrowedSpecs: narrowedAgent,
		ForcedTool:    pfResult.ForcedTool,
		ForcedReason:  pfResult.ForcedReason,
		Duration:      pfResult.Duration,
		AppliedRules:  pfResult.AppliedRules,
		OriginalCount: pfResult.OriginalCount,
		NarrowedCount: pfResult.NarrowedCount,
	}
}

// =============================================================================
// Phase 0: Routing Encyclopedia (CB-62 Rev 2)
// =============================================================================

// applyEncyclopedia runs Phase 0: Routing Encyclopedia matching.
//
// Description:
//
//	For each encyclopedia entry, checks if any intent pattern matches the query
//	AND no anti-signal matches. Applies the entry's tier action:
//	  - force: return the tool immediately (deterministic, skip router)
//	  - boost: add boost_amount to the tool's embedding score
//	  - hint:  ensure the tool is in the candidate set (min_candidates fill)
//
// Inputs:
//
//	queryLower - Lowercase query string.
//
// Outputs:
//
//	forcedTool - Non-empty if a tier=force entry matched.
//	boosts     - Map of tool name → boost amount for tier=boost entries.
//	hints      - List of tool names for tier=hint entries.
func (pf *PreFilter) applyEncyclopedia(queryLower string) (string, map[string]float64, []string) {
	if len(pf.cfg.RoutingEncyclopedia) == 0 {
		return "", nil, nil
	}

	boosts := make(map[string]float64)
	var hints []string

	for i, entry := range pf.cfg.RoutingEncyclopedia {
		if i >= len(pf.compiledEncyclopediaPatterns) {
			break
		}

		// Check anti-signals first (cheap string contains).
		if pf.matchesAntiSignals(queryLower, entry.AntiSignals) {
			continue
		}

		// Check intent patterns.
		if !matchCompiledPatterns(queryLower, pf.compiledEncyclopediaPatterns[i]) {
			continue
		}

		switch entry.Tier {
		case "force":
			return entry.Tool, nil, nil
		case "boost":
			boosts[entry.Tool] += entry.BoostAmount
		case "hint":
			hints = append(hints, entry.Tool)
		}
	}

	return "", boosts, hints
}

// matchesAntiSignals checks if any anti-signal is a substring of the query.
//
// Inputs:
//
//	queryLower  - Lowercase query string.
//	antiSignals - List of anti-signal phrases to check.
//
// Outputs:
//
//	bool - True if any anti-signal matches.
func (pf *PreFilter) matchesAntiSignals(queryLower string, antiSignals []string) bool {
	for _, signal := range antiSignals {
		if strings.Contains(queryLower, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

// =============================================================================
// Helpers
// =============================================================================

// passthroughReason returns a human-readable reason for passthrough.
func (pf *PreFilter) passthroughReason(query string, allSpecs []ToolSpec) string {
	if !pf.cfg.Enabled {
		return "disabled"
	}
	if len(query) == 0 {
		return "empty_query"
	}
	if len(allSpecs) == 0 {
		return "no_specs"
	}
	return "unknown"
}
