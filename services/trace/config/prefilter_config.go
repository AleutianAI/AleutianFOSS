// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package config

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// Embedded Default Pre-Filter Rules
// =============================================================================

//go:embed prefilter_rules.yaml
var defaultPreFilterRulesYAML []byte

// =============================================================================
// Pre-Filter Configuration Types
// =============================================================================

// PreFilterConfig defines the pre-filter behavior and rules.
//
// Description:
//
//	Contains all configuration for the rule-based pre-filter including
//	forced mappings, negation rules, confusion pairs, and candidate bounds.
//
// Thread Safety: Immutable after loading; safe for concurrent use.
type PreFilterConfig struct {
	// Enabled controls whether the pre-filter is active.
	Enabled bool `yaml:"enabled"`

	// MinCandidates is the minimum number of tools to return.
	// Ensures the router always has enough candidates.
	MinCandidates int `yaml:"min_candidates"`

	// MaxCandidates is the maximum number of tools to return.
	// Limits the router's classification space.
	MaxCandidates int `yaml:"max_candidates"`

	// NegationProximity is the maximum word distance between a negation word
	// and a trigger keyword for negation detection to fire.
	NegationProximity int `yaml:"negation_proximity"`

	// ScoringMode controls Phase 3 scoring: "hybrid" (0.4 BM25 + 0.6 embedding)
	// or "embedding_primary" (pure embedding, passthrough on fallback).
	ScoringMode string `yaml:"scoring_mode"`

	// ScoreGapThreshold is the minimum score drop between consecutive tools
	// that triggers a cutoff. Tools below the gap are excluded. Default: 0.15.
	ScoreGapThreshold float64 `yaml:"score_gap_threshold"`

	// ScoreFloor is the minimum absolute score for inclusion. Default: 0.30.
	ScoreFloor float64 `yaml:"score_floor"`

	// AlwaysInclude lists tool names that must always be in the narrowed set.
	AlwaysInclude []string `yaml:"always_include"`

	// ForcedMappings maps exact phrase patterns to deterministic tool selections.
	ForcedMappings []ForcedMapping `yaml:"forced_mappings"`

	// NegationRules detect negation patterns that change tool selection.
	NegationRules []NegationRule `yaml:"negation_rules"`

	// ConfusionPairs resolve ambiguity between frequently confused tools.
	ConfusionPairs []ConfusionPair `yaml:"confusion_pairs"`

	// RoutingEncyclopedia maps user intent patterns to tools with tiered actions.
	// CB-62 Rev 2: Replaces ad-hoc forced_mappings/confusion_pairs over time.
	RoutingEncyclopedia []EncyclopediaEntry `yaml:"routing_encyclopedia"`
}

// ForcedMapping maps phrase patterns to a deterministic tool selection.
//
// Description:
//
//	When any pattern matches the query (substring or regex), the pre-filter
//	forces the specified tool and skips the LLM router entirely.
type ForcedMapping struct {
	// Patterns are substring or regex patterns to match against the query.
	Patterns []string `yaml:"patterns"`

	// Tool is the tool to force when a pattern matches.
	Tool string `yaml:"tool"`

	// Reason explains why this mapping exists (for logging/tracing).
	Reason string `yaml:"reason"`
}

// NegationRule detects negation patterns that require different tool selection.
//
// Description:
//
//	When a negation word appears within NegationProximity words of a trigger
//	keyword, the pre-filter redirects from WrongTool to CorrectTool.
type NegationRule struct {
	// NegationWords are words that negate meaning (e.g., "no", "not", "never").
	NegationWords []string `yaml:"negation_words"`

	// TriggerKeywords are keywords that, when negated, change the tool choice.
	TriggerKeywords []string `yaml:"trigger_keywords"`

	// WrongTool is the tool the router would likely choose without negation awareness.
	WrongTool string `yaml:"wrong_tool"`

	// CorrectTool is the tool to use when negation is detected.
	CorrectTool string `yaml:"correct_tool"`

	// Action is either "force" (skip router) or "boost" (increase score).
	Action string `yaml:"action"`

	// Reason explains why this rule exists.
	Reason string `yaml:"reason"`
}

// ConfusionPair resolves ambiguity between two frequently confused tools.
//
// Description:
//
//	When both tools in a pair appear in the candidate set, pattern matching
//	determines which one to boost. This prevents the router from picking
//	the wrong one in an ambiguous pair.
type ConfusionPair struct {
	// ToolA is the first tool in the pair.
	ToolA string `yaml:"tool_a"`

	// ToolB is the second tool in the pair.
	ToolB string `yaml:"tool_b"`

	// ToolAPatterns boost ToolA when matched.
	ToolAPatterns []string `yaml:"tool_a_patterns"`

	// ToolBPatterns boost ToolB when matched.
	ToolBPatterns []string `yaml:"tool_b_patterns"`

	// BoostAmount is the score boost applied (default 3.0).
	BoostAmount float64 `yaml:"boost_amount"`
}

// EncyclopediaEntry represents a single tool's intent-to-routing mapping.
//
// Description:
//
//	Maps user intent patterns to a tool with a tiered action:
//	  - "force": deterministic selection, skip the router entirely.
//	  - "boost": add boost_amount to the tool's embedding score.
//	  - "hint": ensure the tool is in the candidate set (min_candidates fill).
//
//	Anti-signals suppress the entry when matched, preventing false positives.
//	CB-62 Rev 2.
//
// Thread Safety: Immutable after loading; safe for concurrent use.
type EncyclopediaEntry struct {
	// Tool is the target tool name.
	Tool string `yaml:"tool"`

	// Tier is the action tier: "force", "boost", or "hint".
	Tier string `yaml:"tier"`

	// BoostAmount is the score bonus applied when tier=boost. Ignored for other tiers.
	// Range [0.0, 0.30] to nudge without overriding embedding scores.
	BoostAmount float64 `yaml:"boost_amount"`

	// Intents are regex or substring patterns describing user intent.
	Intents []IntentPattern `yaml:"intents"`

	// AntiSignals suppress this entry when ANY anti-signal matches the query.
	// Use specific phrases, not single words.
	AntiSignals []string `yaml:"anti_signals"`

	// ConflictWith names a tool that conflicts with this entry.
	// Used for documentation and future conflict resolution.
	ConflictWith string `yaml:"conflict_with,omitempty"`

	// Reason explains why this mapping exists (for logging/tracing).
	Reason string `yaml:"reason"`
}

// IntentPattern is a regex or substring pattern describing a user intent.
//
// Description:
//
//	Patterns containing ".*" are treated as regex; otherwise substring match.
//	CB-62 Rev 2.
type IntentPattern struct {
	// Pattern is the matching pattern (regex if contains ".*", otherwise substring).
	Pattern string `yaml:"pattern"`

	// Description optionally explains what this pattern matches.
	Description string `yaml:"description,omitempty"`
}

// =============================================================================
// Defaults
// =============================================================================

const (
	// DefaultMinCandidates is the default minimum candidate count.
	DefaultMinCandidates = 3

	// DefaultMaxCandidates is the default maximum candidate count.
	DefaultMaxCandidates = 20

	// DefaultNegationProximity is the default maximum word distance for negation.
	DefaultNegationProximity = 3

	// DefaultBoostAmount is the default confusion pair boost.
	DefaultBoostAmount = 3.0

	// DefaultScoringMode is the default Phase 3 scoring strategy.
	// "embedding_primary" uses pure embedding scoring with passthrough fallback.
	DefaultScoringMode = "embedding_primary"

	// DefaultScoreGapThreshold is the minimum score drop between consecutive
	// tools that triggers a cutoff in the adaptive candidate window.
	DefaultScoreGapThreshold = 0.15

	// DefaultScoreFloor is the minimum absolute score for inclusion in the
	// candidate set. Tools scoring below this are excluded.
	DefaultScoreFloor = 0.30
)

// =============================================================================
// Singleton Pre-Filter Config
// =============================================================================

var (
	prefilterConfigMu      sync.RWMutex
	prefilterConfigOnce    sync.Once
	cachedPreFilterConfig  *PreFilterConfig
	prefilterConfigLoadErr error
)

// GetPreFilterConfig returns the cached pre-filter configuration.
//
// Description:
//
//	Loads the pre-filter rules on first call and caches for subsequent calls.
//	Uses sync.Once for thread-safe initialization.
//
// Inputs:
//
//	ctx - Context for tracing. Must not be nil.
//
// Outputs:
//
//	*PreFilterConfig - The loaded configuration. Never nil on success.
//	error - Non-nil if loading or validation failed.
//
// Thread Safety: Safe for concurrent use via sync.Once.
func GetPreFilterConfig(ctx context.Context) (*PreFilterConfig, error) {
	if ctx == nil {
		return nil, fmt.Errorf("GetPreFilterConfig: ctx must not be nil")
	}

	prefilterConfigMu.RLock()
	if cachedPreFilterConfig != nil || prefilterConfigLoadErr != nil {
		cfg, err := cachedPreFilterConfig, prefilterConfigLoadErr
		prefilterConfigMu.RUnlock()
		return cfg, err
	}
	prefilterConfigMu.RUnlock()

	prefilterConfigMu.Lock()
	defer prefilterConfigMu.Unlock()

	if cachedPreFilterConfig != nil || prefilterConfigLoadErr != nil {
		return cachedPreFilterConfig, prefilterConfigLoadErr
	}

	prefilterConfigOnce.Do(func() {
		cachedPreFilterConfig, prefilterConfigLoadErr = LoadPreFilterConfig(ctx, defaultPreFilterRulesYAML)
	})

	return cachedPreFilterConfig, prefilterConfigLoadErr
}

// ResetPreFilterConfig resets the cached config for testing.
//
// Description:
//
//	Clears the cached pre-filter config so tests can reload with different data.
//
// Thread Safety: Safe for concurrent use.
func ResetPreFilterConfig() {
	prefilterConfigMu.Lock()
	defer prefilterConfigMu.Unlock()
	cachedPreFilterConfig = nil
	prefilterConfigLoadErr = nil
	prefilterConfigOnce = sync.Once{}
}

// LoadPreFilterConfig loads and validates a PreFilterConfig from YAML bytes.
//
// Description:
//
//	Parses the YAML, applies defaults for missing fields, and validates
//	all rules for consistency (non-empty tool names, valid actions, etc.).
//
// Inputs:
//
//	ctx - Context for tracing.
//	data - Raw YAML bytes to parse.
//
// Outputs:
//
//	*PreFilterConfig - The validated configuration.
//	error - Non-nil if parsing or validation fails.
func LoadPreFilterConfig(ctx context.Context, data []byte) (*PreFilterConfig, error) {
	_, span := toolRegistryTracer.Start(ctx, "config.LoadPreFilterConfig")
	defer span.End()

	if len(data) == 0 {
		return nil, fmt.Errorf("LoadPreFilterConfig: empty YAML data")
	}

	// SEC2: File size limit
	if len(data) > MaxYAMLFileSize {
		return nil, fmt.Errorf("LoadPreFilterConfig: YAML data exceeds maximum size (%d > %d)", len(data), MaxYAMLFileSize)
	}

	var cfg PreFilterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("LoadPreFilterConfig: parsing YAML: %w", err)
	}

	// Apply defaults for missing fields
	if cfg.MinCandidates <= 0 {
		cfg.MinCandidates = DefaultMinCandidates
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = DefaultMaxCandidates
	}
	if cfg.NegationProximity <= 0 {
		cfg.NegationProximity = DefaultNegationProximity
	}
	if cfg.ScoringMode == "" {
		cfg.ScoringMode = DefaultScoringMode
	}
	if cfg.ScoreGapThreshold == 0 {
		cfg.ScoreGapThreshold = DefaultScoreGapThreshold
	}
	if cfg.ScoreFloor == 0 {
		cfg.ScoreFloor = DefaultScoreFloor
	}

	// Ensure min <= max
	if cfg.MinCandidates > cfg.MaxCandidates {
		cfg.MinCandidates = cfg.MaxCandidates
	}

	// Apply default boost amount where missing
	for i := range cfg.ConfusionPairs {
		if cfg.ConfusionPairs[i].BoostAmount <= 0 {
			cfg.ConfusionPairs[i].BoostAmount = DefaultBoostAmount
		}
	}

	// Validate rules
	if err := validatePreFilterConfig(&cfg); err != nil {
		return nil, fmt.Errorf("LoadPreFilterConfig: validation: %w", err)
	}

	span.SetAttributes(
		attribute.Bool("enabled", cfg.Enabled),
		attribute.Int("forced_mappings", len(cfg.ForcedMappings)),
		attribute.Int("negation_rules", len(cfg.NegationRules)),
		attribute.Int("confusion_pairs", len(cfg.ConfusionPairs)),
		attribute.Int("routing_encyclopedia", len(cfg.RoutingEncyclopedia)),
		attribute.Int("min_candidates", cfg.MinCandidates),
		attribute.Int("max_candidates", cfg.MaxCandidates),
		attribute.String("scoring_mode", cfg.ScoringMode),
		attribute.Float64("score_gap_threshold", cfg.ScoreGapThreshold),
		attribute.Float64("score_floor", cfg.ScoreFloor),
	)

	slog.Info("pre-filter config loaded",
		slog.Bool("enabled", cfg.Enabled),
		slog.Int("forced_mappings", len(cfg.ForcedMappings)),
		slog.Int("negation_rules", len(cfg.NegationRules)),
		slog.Int("confusion_pairs", len(cfg.ConfusionPairs)),
		slog.Int("routing_encyclopedia", len(cfg.RoutingEncyclopedia)),
		slog.String("scoring_mode", cfg.ScoringMode),
	)

	return &cfg, nil
}

// validatePreFilterConfig checks all rules for consistency.
func validatePreFilterConfig(cfg *PreFilterConfig) error {
	// Validate scoring mode
	switch cfg.ScoringMode {
	case "hybrid", "embedding_primary":
		// valid
	default:
		return fmt.Errorf("scoring_mode must be 'hybrid' or 'embedding_primary', got %q", cfg.ScoringMode)
	}

	// Validate forced mappings
	for i, fm := range cfg.ForcedMappings {
		if fm.Tool == "" {
			return fmt.Errorf("forced_mapping[%d]: tool must not be empty", i)
		}
		if len(fm.Patterns) == 0 {
			return fmt.Errorf("forced_mapping[%d] (%s): patterns must not be empty", i, fm.Tool)
		}
	}

	// Validate negation rules
	for i, nr := range cfg.NegationRules {
		if nr.CorrectTool == "" {
			return fmt.Errorf("negation_rule[%d]: correct_tool must not be empty", i)
		}
		if nr.WrongTool == "" {
			return fmt.Errorf("negation_rule[%d]: wrong_tool must not be empty", i)
		}
		if len(nr.NegationWords) == 0 {
			return fmt.Errorf("negation_rule[%d] (%s): negation_words must not be empty", i, nr.CorrectTool)
		}
		if len(nr.TriggerKeywords) == 0 {
			return fmt.Errorf("negation_rule[%d] (%s): trigger_keywords must not be empty", i, nr.CorrectTool)
		}
		if nr.Action != "force" {
			return fmt.Errorf("negation_rule[%d] (%s): action must be 'force', got %q", i, nr.CorrectTool, nr.Action)
		}
	}

	// Validate confusion pairs
	for i, cp := range cfg.ConfusionPairs {
		if cp.ToolA == "" {
			return fmt.Errorf("confusion_pair[%d]: tool_a must not be empty", i)
		}
		if cp.ToolB == "" {
			return fmt.Errorf("confusion_pair[%d]: tool_b must not be empty", i)
		}
		if cp.ToolA == cp.ToolB {
			return fmt.Errorf("confusion_pair[%d]: tool_a and tool_b must be different (%s)", i, cp.ToolA)
		}
	}

	// Validate routing encyclopedia entries (CB-62 Rev 2)
	for i, entry := range cfg.RoutingEncyclopedia {
		if entry.Tool == "" {
			return fmt.Errorf("routing_encyclopedia[%d]: tool must not be empty", i)
		}
		switch entry.Tier {
		case "force", "boost", "hint":
			// valid
		default:
			return fmt.Errorf("routing_encyclopedia[%d] (%s): tier must be 'force', 'boost', or 'hint', got %q", i, entry.Tool, entry.Tier)
		}
		if entry.Tier != "boost" && entry.BoostAmount > 0 {
			return fmt.Errorf("routing_encyclopedia[%d] (%s): boost_amount is only valid for tier=boost, got tier=%q with boost_amount=%f", i, entry.Tool, entry.Tier, entry.BoostAmount)
		}
		if len(entry.Intents) == 0 {
			return fmt.Errorf("routing_encyclopedia[%d] (%s): intents must not be empty", i, entry.Tool)
		}
		for j, intent := range entry.Intents {
			if intent.Pattern == "" {
				return fmt.Errorf("routing_encyclopedia[%d] (%s): intents[%d].pattern must not be empty", i, entry.Tool, j)
			}
		}
	}

	return nil
}
