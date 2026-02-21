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

	// AlwaysInclude lists tool names that must always be in the narrowed set.
	AlwaysInclude []string `yaml:"always_include"`

	// ForcedMappings maps exact phrase patterns to deterministic tool selections.
	ForcedMappings []ForcedMapping `yaml:"forced_mappings"`

	// NegationRules detect negation patterns that change tool selection.
	NegationRules []NegationRule `yaml:"negation_rules"`

	// ConfusionPairs resolve ambiguity between frequently confused tools.
	ConfusionPairs []ConfusionPair `yaml:"confusion_pairs"`
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

// =============================================================================
// Defaults
// =============================================================================

const (
	// DefaultMinCandidates is the default minimum candidate count.
	DefaultMinCandidates = 3

	// DefaultMaxCandidates is the default maximum candidate count.
	DefaultMaxCandidates = 10

	// DefaultNegationProximity is the default maximum word distance for negation.
	DefaultNegationProximity = 3

	// DefaultBoostAmount is the default confusion pair boost.
	DefaultBoostAmount = 3.0
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
		attribute.Int("min_candidates", cfg.MinCandidates),
		attribute.Int("max_candidates", cfg.MaxCandidates),
	)

	slog.Info("pre-filter config loaded",
		slog.Bool("enabled", cfg.Enabled),
		slog.Int("forced_mappings", len(cfg.ForcedMappings)),
		slog.Int("negation_rules", len(cfg.NegationRules)),
		slog.Int("confusion_pairs", len(cfg.ConfusionPairs)),
	)

	return &cfg, nil
}

// validatePreFilterConfig checks all rules for consistency.
func validatePreFilterConfig(cfg *PreFilterConfig) error {
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

	return nil
}
