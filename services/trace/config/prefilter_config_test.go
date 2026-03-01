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
	"testing"
)

func TestLoadPreFilterConfig_Embedded(t *testing.T) {
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, defaultPreFilterRulesYAML)
	if err != nil {
		t.Fatalf("LoadPreFilterConfig failed on embedded YAML: %v", err)
	}

	if !cfg.Enabled {
		t.Error("expected enabled = true")
	}
	if cfg.MinCandidates != 3 {
		t.Errorf("expected min_candidates = 3, got %d", cfg.MinCandidates)
	}
	if cfg.MaxCandidates != 20 {
		t.Errorf("expected max_candidates = 20, got %d", cfg.MaxCandidates)
	}
	if cfg.NegationProximity != 3 {
		t.Errorf("expected negation_proximity = 3, got %d", cfg.NegationProximity)
	}
	if len(cfg.AlwaysInclude) == 0 || cfg.AlwaysInclude[0] != "answer" {
		t.Error("expected always_include to contain 'answer'")
	}
	if len(cfg.ForcedMappings) == 0 {
		t.Error("expected at least one forced mapping")
	}
	if len(cfg.NegationRules) == 0 {
		t.Error("expected at least one negation rule")
	}
	if len(cfg.ConfusionPairs) == 0 {
		t.Error("expected at least one confusion pair")
	}
}

func TestLoadPreFilterConfig_Defaults(t *testing.T) {
	yaml := []byte(`
enabled: true
forced_mappings: []
negation_rules: []
confusion_pairs: []
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MinCandidates != DefaultMinCandidates {
		t.Errorf("expected default min_candidates = %d, got %d", DefaultMinCandidates, cfg.MinCandidates)
	}
	if cfg.MaxCandidates != DefaultMaxCandidates {
		t.Errorf("expected default max_candidates = %d, got %d", DefaultMaxCandidates, cfg.MaxCandidates)
	}
	if cfg.NegationProximity != DefaultNegationProximity {
		t.Errorf("expected default negation_proximity = %d, got %d", DefaultNegationProximity, cfg.NegationProximity)
	}
}

func TestLoadPreFilterConfig_DefaultBoostAmount(t *testing.T) {
	yaml := []byte(`
enabled: true
confusion_pairs:
  - tool_a: find_callers
    tool_b: find_callees
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.ConfusionPairs) != 1 {
		t.Fatalf("expected 1 confusion pair, got %d", len(cfg.ConfusionPairs))
	}
	if cfg.ConfusionPairs[0].BoostAmount != DefaultBoostAmount {
		t.Errorf("expected default boost = %f, got %f", DefaultBoostAmount, cfg.ConfusionPairs[0].BoostAmount)
	}
}

func TestLoadPreFilterConfig_Validation_EmptyTool(t *testing.T) {
	yaml := []byte(`
enabled: true
forced_mappings:
  - patterns: ["test"]
    tool: ""
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty tool")
	}
}

func TestLoadPreFilterConfig_Validation_EmptyPatterns(t *testing.T) {
	yaml := []byte(`
enabled: true
forced_mappings:
  - patterns: []
    tool: some_tool
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty patterns")
	}
}

func TestLoadPreFilterConfig_Validation_InvalidNegationAction(t *testing.T) {
	yaml := []byte(`
enabled: true
negation_rules:
  - negation_words: ["no"]
    trigger_keywords: ["callers"]
    wrong_tool: find_callers
    correct_tool: find_dead_code
    action: invalid
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid action")
	}
}

func TestLoadPreFilterConfig_Validation_BoostActionRejected(t *testing.T) {
	yaml := []byte(`
enabled: true
negation_rules:
  - negation_words: ["no"]
    trigger_keywords: ["callers"]
    wrong_tool: find_callers
    correct_tool: find_dead_code
    action: boost
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for boost action (only force is supported)")
	}
}

func TestLoadPreFilterConfig_Validation_SameToolInPair(t *testing.T) {
	yaml := []byte(`
enabled: true
confusion_pairs:
  - tool_a: find_callers
    tool_b: find_callers
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for same tool in pair")
	}
}

func TestLoadPreFilterConfig_Validation_NegationMissingWords(t *testing.T) {
	yaml := []byte(`
enabled: true
negation_rules:
  - negation_words: []
    trigger_keywords: ["callers"]
    wrong_tool: find_callers
    correct_tool: find_dead_code
    action: force
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty negation_words")
	}
}

func TestLoadPreFilterConfig_Validation_NegationMissingTrigger(t *testing.T) {
	yaml := []byte(`
enabled: true
negation_rules:
  - negation_words: ["no"]
    trigger_keywords: []
    wrong_tool: find_callers
    correct_tool: find_dead_code
    action: force
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty trigger_keywords")
	}
}

func TestLoadPreFilterConfig_EmptyData(t *testing.T) {
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, []byte{})
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestLoadPreFilterConfig_InvalidYAML(t *testing.T) {
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, []byte("{{{{not yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadPreFilterConfig_MinExceedsMax(t *testing.T) {
	yaml := []byte(`
enabled: true
min_candidates: 20
max_candidates: 5
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.MinCandidates > cfg.MaxCandidates {
		t.Errorf("min_candidates (%d) should be <= max_candidates (%d)", cfg.MinCandidates, cfg.MaxCandidates)
	}
}

func TestGetPreFilterConfig_NilContext(t *testing.T) {
	_, err := GetPreFilterConfig(nil) //nolint:staticcheck // testing nil ctx
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestGetPreFilterConfig_Singleton(t *testing.T) {
	ResetPreFilterConfig()
	defer ResetPreFilterConfig()

	ctx := context.Background()
	cfg1, err := GetPreFilterConfig(ctx)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	cfg2, err := GetPreFilterConfig(ctx)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if cfg1 != cfg2 {
		t.Error("expected same pointer from singleton")
	}
}

// =============================================================================
// CB-62: New field tests
// =============================================================================

func TestLoadPreFilterConfig_Embedded_CB62Fields(t *testing.T) {
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, defaultPreFilterRulesYAML)
	if err != nil {
		t.Fatalf("LoadPreFilterConfig failed: %v", err)
	}

	if cfg.ScoringMode != "embedding_primary" {
		t.Errorf("expected scoring_mode = embedding_primary, got %q", cfg.ScoringMode)
	}
	if cfg.ScoreGapThreshold != 0.15 {
		t.Errorf("expected score_gap_threshold = 0.15, got %f", cfg.ScoreGapThreshold)
	}
	if cfg.ScoreFloor != 0.30 {
		t.Errorf("expected score_floor = 0.30, got %f", cfg.ScoreFloor)
	}
}

func TestLoadPreFilterConfig_CB62Defaults(t *testing.T) {
	yaml := []byte(`
enabled: true
forced_mappings: []
negation_rules: []
confusion_pairs: []
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ScoringMode != DefaultScoringMode {
		t.Errorf("expected default scoring_mode = %q, got %q", DefaultScoringMode, cfg.ScoringMode)
	}
	if cfg.ScoreGapThreshold != DefaultScoreGapThreshold {
		t.Errorf("expected default score_gap_threshold = %f, got %f", DefaultScoreGapThreshold, cfg.ScoreGapThreshold)
	}
	if cfg.ScoreFloor != DefaultScoreFloor {
		t.Errorf("expected default score_floor = %f, got %f", DefaultScoreFloor, cfg.ScoreFloor)
	}
	if cfg.MaxCandidates != DefaultMaxCandidates {
		t.Errorf("expected default max_candidates = %d, got %d", DefaultMaxCandidates, cfg.MaxCandidates)
	}
}

func TestLoadPreFilterConfig_Validation_InvalidScoringMode(t *testing.T) {
	yaml := []byte(`
enabled: true
scoring_mode: invalid_mode
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid scoring_mode")
	}
}

func TestLoadPreFilterConfig_Validation_HybridScoringMode(t *testing.T) {
	yaml := []byte(`
enabled: true
scoring_mode: hybrid
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error for valid hybrid mode: %v", err)
	}
	if cfg.ScoringMode != "hybrid" {
		t.Errorf("expected scoring_mode = hybrid, got %q", cfg.ScoringMode)
	}
}

// =============================================================================
// CB-62 Rev 2: Routing Encyclopedia Validation Tests
// =============================================================================

func TestEncyclopediaValidation_ValidTiers(t *testing.T) {
	for _, tier := range []string{"force", "boost", "hint"} {
		t.Run(tier, func(t *testing.T) {
			yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: ` + tier + `
    intents:
      - pattern: "test pattern"
    reason: "test"
`)
			if tier == "boost" {
				yaml = []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: boost
    boost_amount: 0.25
    intents:
      - pattern: "test pattern"
    reason: "test"
`)
			}
			ctx := context.Background()
			_, err := LoadPreFilterConfig(ctx, yaml)
			if err != nil {
				t.Fatalf("expected valid tier %q to be accepted, got error: %v", tier, err)
			}
		})
	}
}

func TestEncyclopediaValidation_InvalidTier(t *testing.T) {
	yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: invalid_tier
    intents:
      - pattern: "test pattern"
    reason: "test"
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid tier")
	}
}

func TestEncyclopediaValidation_BoostAmountOnlyForBoost(t *testing.T) {
	yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: force
    boost_amount: 0.25
    intents:
      - pattern: "test pattern"
    reason: "test"
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for boost_amount on non-boost tier")
	}
}

func TestEncyclopediaValidation_EmptyTool(t *testing.T) {
	yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: ""
    tier: force
    intents:
      - pattern: "test pattern"
    reason: "test"
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty tool")
	}
}

func TestEncyclopediaValidation_EmptyIntents(t *testing.T) {
	yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: force
    intents: []
    reason: "test"
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty intents")
	}
}

func TestEncyclopediaValidation_EmptyPattern(t *testing.T) {
	yaml := []byte(`
enabled: true
routing_encyclopedia:
  - tool: test_tool
    tier: force
    intents:
      - pattern: ""
    reason: "test"
`)
	ctx := context.Background()
	_, err := LoadPreFilterConfig(ctx, yaml)
	if err == nil {
		t.Fatal("expected validation error for empty pattern")
	}
}

func TestLoadPreFilterConfig_Embedded_EncyclopediaLoaded(t *testing.T) {
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, defaultPreFilterRulesYAML)
	if err != nil {
		t.Fatalf("LoadPreFilterConfig failed: %v", err)
	}

	if len(cfg.RoutingEncyclopedia) == 0 {
		t.Error("expected at least one routing encyclopedia entry")
	}

	// Verify find_implementations is in the encyclopedia
	found := false
	for _, entry := range cfg.RoutingEncyclopedia {
		if entry.Tool == "find_implementations" {
			found = true
			if entry.Tier != "boost" {
				t.Errorf("expected find_implementations tier=boost, got %q", entry.Tier)
			}
			if entry.BoostAmount != 0.25 {
				t.Errorf("expected find_implementations boost_amount=0.25, got %f", entry.BoostAmount)
			}
		}
	}
	if !found {
		t.Error("expected find_implementations in routing encyclopedia")
	}
}

func TestLoadPreFilterConfig_ScoreGapAndFloorAcceptZero(t *testing.T) {
	// When YAML explicitly sets 0.0, defaults should NOT override.
	// However, Go yaml.v3 unmarshals 0 as zero-value, and our default logic
	// uses == 0 check. Explicit zero is indistinguishable from missing.
	// This test documents the behavior: 0 gets default applied.
	yaml := []byte(`
enabled: true
scoring_mode: embedding_primary
score_gap_threshold: 0.0
score_floor: 0.0
`)
	ctx := context.Background()
	cfg, err := LoadPreFilterConfig(ctx, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Zero is treated as "not set" → default applied
	if cfg.ScoreGapThreshold != DefaultScoreGapThreshold {
		t.Errorf("expected default score_gap_threshold when zero, got %f", cfg.ScoreGapThreshold)
	}
	if cfg.ScoreFloor != DefaultScoreFloor {
		t.Errorf("expected default score_floor when zero, got %f", cfg.ScoreFloor)
	}
}
