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
	"regexp"
	"sort"
	"strings"
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

	// compiledForcedPatterns holds pre-compiled patterns per forced mapping index.
	compiledForcedPatterns [][]compiledPattern

	// compiledConfusionAPatterns holds pre-compiled tool_a_patterns per confusion pair.
	compiledConfusionAPatterns [][]compiledPattern

	// compiledConfusionBPatterns holds pre-compiled tool_b_patterns per confusion pair.
	compiledConfusionBPatterns [][]compiledPattern
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
//	If registry is nil, keyword matching (Phase 3) is skipped.
//
// Inputs:
//
//	registry - Tool routing registry for keyword lookup. May be nil.
//	cfg - Pre-filter configuration. Must not be nil.
//	logger - Logger instance. Must not be nil.
//
// Outputs:
//
//	*PreFilter - The constructed pre-filter.
//
// Thread Safety: The returned PreFilter is safe for concurrent use.
func NewPreFilter(registry *config.ToolRoutingRegistry, cfg *config.PreFilterConfig, logger *slog.Logger) *PreFilter {
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
	}

	// Pre-compile regex patterns for forced mappings
	pf.compiledForcedPatterns = make([][]compiledPattern, len(cfg.ForcedMappings))
	for i, fm := range cfg.ForcedMappings {
		pf.compiledForcedPatterns[i] = compilePatterns(fm.Patterns, logger)
	}

	// Pre-compile regex patterns for confusion pairs
	pf.compiledConfusionAPatterns = make([][]compiledPattern, len(cfg.ConfusionPairs))
	pf.compiledConfusionBPatterns = make([][]compiledPattern, len(cfg.ConfusionPairs))
	for i, cp := range cfg.ConfusionPairs {
		pf.compiledConfusionAPatterns[i] = compilePatterns(cp.ToolAPatterns, logger)
		pf.compiledConfusionBPatterns[i] = compilePatterns(cp.ToolBPatterns, logger)
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
// Inputs:
//
//	ctx - Context for tracing and cancellation. Must not be nil.
//	query - The user's query string.
//	allSpecs - All available tool specs.
//
// Outputs:
//
//	*PreFilterResult - The filtering result with narrowed specs or forced tool.
//
// Thread Safety: Safe for concurrent use.
func (pf *PreFilter) Filter(ctx context.Context, query string, allSpecs []ToolSpec) *PreFilterResult {
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

	// Phase 3: Keyword matching
	scores := pf.scoreByKeywords(queryLower, allSpecs)
	for k, v := range scores {
		result.Scores[k] = v
	}
	if len(scores) > 0 {
		result.AppliedRules = append(result.AppliedRules, "keyword_matching")
		prefilterRulesFired.WithLabelValues("keyword_matching").Inc()
	}

	// Phase 4: Confusion pair resolution
	pf.resolveConfusionPairs(queryLower, result.Scores, result)

	// Phase 5: Candidate selection
	narrowed := pf.selectCandidates(result.Scores, allSpecs, specIndex)
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
// Phase 3: Keyword Matching
// =============================================================================

// scoreByKeywords scores tools based on keyword matches from the registry.
//
// Description:
//
//	Uses the ToolRoutingRegistry.FindToolsByKeyword() to find matching tools
//	and assigns scores based on match count. Falls back to basic substring
//	matching on BestFor keywords if registry is nil.
//
// Inputs:
//
//	queryLower - Lowercase query string.
//	allSpecs - All available tool specs.
//
// Outputs:
//
//	map[string]float64 - Score per tool name.
func (pf *PreFilter) scoreByKeywords(queryLower string, allSpecs []ToolSpec) map[string]float64 {
	scores := make(map[string]float64)

	if pf.registry != nil {
		// Use registry keyword index (O(1) lookup per keyword)
		matches := pf.registry.FindToolsByKeyword(queryLower)
		for _, m := range matches {
			scores[m.ToolName] = float64(m.MatchCount)
		}
	} else {
		// Fallback: score based on BestFor keywords in specs
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
	for i, pair := range pf.cfg.ConfusionPairs {
		if i >= len(pf.compiledConfusionAPatterns) || i >= len(pf.compiledConfusionBPatterns) {
			break
		}

		aMatched := matchCompiledPatterns(queryLower, pf.compiledConfusionAPatterns[i])
		bMatched := matchCompiledPatterns(queryLower, pf.compiledConfusionBPatterns[i])

		if aMatched && !bMatched {
			scores[pair.ToolA] += pair.BoostAmount
			result.AppliedRules = append(result.AppliedRules, "confusion_pair_boost:"+pair.ToolA)
			prefilterRulesFired.WithLabelValues("confusion_pair").Inc()
		} else if bMatched && !aMatched {
			scores[pair.ToolB] += pair.BoostAmount
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

// selectCandidates selects the top candidates by score with min/max bounds.
//
// Description:
//
//	Sorts tools by score, takes top MaxCandidates, ensures MinCandidates
//	floor, and always includes tools from AlwaysInclude list.
//
// Inputs:
//
//	scores - Tool scores from phases 3-4.
//	allSpecs - All available tool specs (for filling to min).
//	specIndex - Tool name to spec lookup.
//
// Outputs:
//
//	[]ToolSpec - The narrowed candidate set.
func (pf *PreFilter) selectCandidates(scores map[string]float64, allSpecs []ToolSpec, specIndex map[string]ToolSpec) []ToolSpec {
	// If no scores, return all specs (passthrough)
	if len(scores) == 0 {
		return allSpecs
	}

	// Sort tools by score descending
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

	// Take top MaxCandidates
	max := pf.cfg.MaxCandidates
	if len(sorted) < max {
		max = len(sorted)
	}
	selected := make(map[string]bool, max)
	for i := 0; i < max; i++ {
		selected[sorted[i].name] = true
	}

	// Always include required tools
	for _, name := range pf.cfg.AlwaysInclude {
		selected[name] = true
	}

	// If below MinCandidates, fill from allSpecs by original order
	if len(selected) < pf.cfg.MinCandidates {
		for _, spec := range allSpecs {
			if len(selected) >= pf.cfg.MinCandidates {
				break
			}
			selected[spec.Name] = true
		}
	}

	// Build result preserving original order
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
// Inputs:
//
//	ctx - Context for tracing.
//	query - The user's query string.
//	allSpecs - All available tool specs in agent format.
//
// Outputs:
//
//	*AgentPreFilterResult - The filtering result.
//
// Thread Safety: Safe for concurrent use.
func (pf *PreFilter) FilterAgentSpecs(ctx context.Context, query string, allSpecs []agent.ToolRouterSpec) *AgentPreFilterResult {
	// Convert agent specs to routing specs
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

	// Run the pre-filter
	pfResult := pf.Filter(ctx, query, routingSpecs)

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
