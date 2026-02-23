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
	"strings"
	"testing"
	"time"
)

func TestTokenBudget_Unlimited(t *testing.T) {
	budget := NewTokenBudget("MAIN", 0)

	ok, _ := budget.CanSpend(1_000_000)
	if !ok {
		t.Error("unlimited budget should always allow spending")
	}

	budget.Record(1_000_000)

	ok, _ = budget.CanSpend(1_000_000)
	if !ok {
		t.Error("unlimited budget should allow spending even after recording")
	}
}

func TestTokenBudget_WithinBudget(t *testing.T) {
	budget := NewTokenBudget("MAIN", 10000)

	ok, remaining := budget.CanSpend(5000)
	if !ok {
		t.Error("should fit within budget")
	}
	if remaining != 5000 {
		t.Errorf("remaining should be 5000, got %d", remaining)
	}
}

func TestTokenBudget_ExceedsBudget(t *testing.T) {
	budget := NewTokenBudget("MAIN", 10000)
	budget.Record(8000)

	ok, remaining := budget.CanSpend(5000)
	if ok {
		t.Error("should exceed budget")
	}
	if remaining != 2000 {
		t.Errorf("remaining should be 2000, got %d", remaining)
	}
}

func TestTokenBudget_ExactBudget(t *testing.T) {
	budget := NewTokenBudget("MAIN", 10000)
	budget.Record(5000)

	ok, remaining := budget.CanSpend(5000)
	if !ok {
		t.Error("exact budget should be allowed")
	}
	if remaining != 0 {
		t.Errorf("remaining should be 0, got %d", remaining)
	}
}

func TestTokenBudget_Summary(t *testing.T) {
	t.Run("unlimited", func(t *testing.T) {
		budget := NewTokenBudget("MAIN", 0)
		budget.Record(5000)
		summary := budget.Summary()
		if !strings.Contains(summary, "unlimited") {
			t.Errorf("summary should mention unlimited, got: %s", summary)
		}
	})

	t.Run("limited", func(t *testing.T) {
		budget := NewTokenBudget("ROUTER", 10000)
		budget.Record(5000)
		summary := budget.Summary()
		if !strings.Contains(summary, "5000/10000") {
			t.Errorf("summary should show 5000/10000, got: %s", summary)
		}
	})
}

func TestTokenBudget_Remaining(t *testing.T) {
	t.Run("unlimited returns -1", func(t *testing.T) {
		budget := NewTokenBudget("MAIN", 0)
		if budget.Remaining() != -1 {
			t.Errorf("unlimited should return -1, got %d", budget.Remaining())
		}
	})

	t.Run("limited returns correct value", func(t *testing.T) {
		budget := NewTokenBudget("MAIN", 10000)
		budget.Record(3000)
		if budget.Remaining() != 7000 {
			t.Errorf("remaining should be 7000, got %d", budget.Remaining())
		}
	})
}

func TestProviderMetrics_RecordCall(t *testing.T) {
	pm := NewProviderMetrics("anthropic")

	pm.RecordCall(100, 200, 500*time.Millisecond)
	pm.RecordCall(150, 300, 700*time.Millisecond)

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.InputTokens != 250 {
		t.Errorf("InputTokens = %d, want 250", pm.InputTokens)
	}
	if pm.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500", pm.OutputTokens)
	}
	if pm.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", pm.TotalCalls)
	}
	if pm.TotalErrors != 0 {
		t.Errorf("TotalErrors = %d, want 0", pm.TotalErrors)
	}
	if pm.TotalLatencyMs != 1200 {
		t.Errorf("TotalLatencyMs = %d, want 1200", pm.TotalLatencyMs)
	}
	if pm.LastCallTimestamp == 0 {
		t.Error("LastCallTimestamp should be set")
	}
}

func TestProviderMetrics_RecordError(t *testing.T) {
	pm := NewProviderMetrics("openai")

	pm.RecordCall(100, 0, 200*time.Millisecond)
	pm.RecordError()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.TotalCalls != 2 {
		t.Errorf("TotalCalls = %d, want 2", pm.TotalCalls)
	}
	if pm.TotalErrors != 1 {
		t.Errorf("TotalErrors = %d, want 1", pm.TotalErrors)
	}
}
