// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"math"
	"strings"
	"testing"
)

func TestCostEstimator_Unlimited(t *testing.T) {
	estimator := NewCostEstimator(0)

	ok, _ := estimator.CanAfford("claude-sonnet-4-20250514", 100000, 10000)
	if !ok {
		t.Error("unlimited estimator should always afford")
	}
}

func TestCostEstimator_WithinLimit(t *testing.T) {
	estimator := NewCostEstimator(1000) // 1000 cents = $10

	ok, costCents := estimator.CanAfford("gpt-4o-mini", 1000, 1000)
	if !ok {
		t.Error("should be within limit")
	}
	// gpt-4o-mini: input $0.15/M, output $0.60/M
	// 1000 input = $0.00015 = 0.015 cents
	// 1000 output = $0.0006 = 0.06 cents
	// total ≈ 0.075 cents
	if costCents > 1 {
		t.Errorf("cost for 1000 tokens of gpt-4o-mini should be < 1 cent, got %.4f", costCents)
	}
}

func TestCostEstimator_ExceedsLimit(t *testing.T) {
	estimator := NewCostEstimator(1) // 1 cent limit

	// Record some usage first
	estimator.Record("claude-sonnet-4-20250514", 100000, 100000)

	// Try to spend more
	ok, _ := estimator.CanAfford("claude-sonnet-4-20250514", 100000, 100000)
	if ok {
		t.Error("should exceed limit after significant usage")
	}
}

func TestCostEstimator_Record(t *testing.T) {
	estimator := NewCostEstimator(0)

	cost1 := estimator.Record("gpt-4o-mini", 1000, 1000)
	cost2 := estimator.Record("gpt-4o-mini", 1000, 1000)

	total := estimator.TotalCostCents()
	expected := cost1 + cost2

	if math.Abs(total-expected) > 0.001 {
		t.Errorf("TotalCostCents = %.4f, want %.4f", total, expected)
	}
}

func TestCostEstimator_UnknownModel(t *testing.T) {
	estimator := NewCostEstimator(0)

	ok, costCents := estimator.CanAfford("some-unknown-model", 1000, 1000)
	if !ok {
		t.Error("unlimited should afford unknown model")
	}
	// Unknown model uses conservative pricing ($5/M input, $15/M output)
	// 1000 input = $0.005 = 0.5 cents
	// 1000 output = $0.015 = 1.5 cents
	// total = 2 cents
	if costCents < 1 || costCents > 3 {
		t.Errorf("unknown model cost should be ~2 cents for 1000 tokens, got %.4f", costCents)
	}
}

func TestCostEstimator_Summary(t *testing.T) {
	t.Run("unlimited", func(t *testing.T) {
		estimator := NewCostEstimator(0)
		estimator.Record("gpt-4o-mini", 1000, 1000)
		summary := estimator.Summary()
		if !strings.Contains(summary, "unlimited") {
			t.Errorf("summary should mention unlimited, got: %s", summary)
		}
	})

	t.Run("limited", func(t *testing.T) {
		estimator := NewCostEstimator(1000)
		summary := estimator.Summary()
		if !strings.Contains(summary, "limit") {
			t.Errorf("summary should mention limit, got: %s", summary)
		}
	})
}

func TestCostEstimator_KnownModels(t *testing.T) {
	estimator := NewCostEstimator(0)

	// Verify all known models have reasonable pricing
	models := []string{
		"claude-sonnet-4-20250514",
		"claude-haiku-4-5-20251001",
		"gpt-4o",
		"gpt-4o-mini",
		"gemini-1.5-flash",
		"gemini-1.5-pro",
		"gemini-2.0-flash",
	}

	for _, model := range models {
		ok, cost := estimator.CanAfford(model, 1000, 1000)
		if !ok {
			t.Errorf("unlimited should afford %s", model)
		}
		if cost <= 0 {
			t.Errorf("cost for %s should be > 0, got %.6f", model, cost)
		}
		t.Logf("%s: 1000 in + 1000 out = %.4f cents", model, cost)
	}
}
