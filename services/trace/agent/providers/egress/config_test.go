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
	"testing"
)

func TestLoadEgressConfig_Defaults(t *testing.T) {
	// Clear all relevant env vars for clean test
	envVars := []string{
		"TRACE_EGRESS_ENABLED", "TRACE_LOCAL_ONLY",
		"TRACE_EGRESS_ALLOWLIST", "TRACE_EGRESS_DENYLIST",
		"TRACE_CONSENT_ANTHROPIC", "TRACE_CONSENT_OPENAI", "TRACE_CONSENT_GEMINI",
		"TRACE_BUDGET_MAIN_TOKENS", "TRACE_BUDGET_ROUTER_TOKENS", "TRACE_BUDGET_PARAM_TOKENS",
		"TRACE_COST_LIMIT_CENTS",
		"TRACE_RATE_ANTHROPIC_PER_MIN", "TRACE_RATE_OPENAI_PER_MIN", "TRACE_RATE_GEMINI_PER_MIN",
		"TRACE_AUDIT_ENABLED", "TRACE_AUDIT_HASH_CONTENT",
	}
	for _, v := range envVars {
		t.Setenv(v, "")
	}

	cfg := LoadEgressConfig()

	if !cfg.Enabled {
		t.Error("Enabled should default to true")
	}
	if cfg.LocalOnly {
		t.Error("LocalOnly should default to false")
	}
	if len(cfg.Allowlist) != 0 {
		t.Errorf("Allowlist should be empty, got %v", cfg.Allowlist)
	}
	if len(cfg.Denylist) != 0 {
		t.Errorf("Denylist should be empty, got %v", cfg.Denylist)
	}
	if !cfg.AuditEnabled {
		t.Error("AuditEnabled should default to true")
	}
	if !cfg.AuditHashContent {
		t.Error("AuditHashContent should default to true")
	}
	if cfg.BudgetMainTokens != 0 {
		t.Errorf("BudgetMainTokens should default to 0, got %d", cfg.BudgetMainTokens)
	}
	if cfg.CostLimitCents != 0 {
		t.Errorf("CostLimitCents should default to 0, got %f", cfg.CostLimitCents)
	}

	// Default rate limits
	for _, p := range []string{"anthropic", "openai", "gemini"} {
		if cfg.RateLimitsPerMin[p] != 60 {
			t.Errorf("RateLimitsPerMin[%s] should default to 60, got %d", p, cfg.RateLimitsPerMin[p])
		}
	}

	// Default consent (all false)
	for _, p := range []string{"anthropic", "openai", "gemini"} {
		if cfg.ProviderConsent[p] {
			t.Errorf("ProviderConsent[%s] should default to false", p)
		}
	}
}

func TestLoadEgressConfig_CustomValues(t *testing.T) {
	t.Setenv("TRACE_EGRESS_ENABLED", "false")
	t.Setenv("TRACE_LOCAL_ONLY", "true")
	t.Setenv("TRACE_EGRESS_ALLOWLIST", "anthropic,openai")
	t.Setenv("TRACE_EGRESS_DENYLIST", "gemini")
	t.Setenv("TRACE_CONSENT_ANTHROPIC", "true")
	t.Setenv("TRACE_CONSENT_OPENAI", "false")
	t.Setenv("TRACE_CONSENT_GEMINI", "")
	t.Setenv("TRACE_BUDGET_MAIN_TOKENS", "100000")
	t.Setenv("TRACE_BUDGET_ROUTER_TOKENS", "5000")
	t.Setenv("TRACE_BUDGET_PARAM_TOKENS", "3000")
	t.Setenv("TRACE_COST_LIMIT_CENTS", "50.5")
	t.Setenv("TRACE_RATE_ANTHROPIC_PER_MIN", "30")
	t.Setenv("TRACE_RATE_OPENAI_PER_MIN", "120")
	t.Setenv("TRACE_RATE_GEMINI_PER_MIN", "")
	t.Setenv("TRACE_AUDIT_ENABLED", "false")
	t.Setenv("TRACE_AUDIT_HASH_CONTENT", "false")

	cfg := LoadEgressConfig()

	if cfg.Enabled {
		t.Error("Enabled should be false")
	}
	if !cfg.LocalOnly {
		t.Error("LocalOnly should be true")
	}
	if !cfg.Allowlist["anthropic"] || !cfg.Allowlist["openai"] {
		t.Errorf("Allowlist should contain anthropic and openai, got %v", cfg.Allowlist)
	}
	if !cfg.Denylist["gemini"] {
		t.Errorf("Denylist should contain gemini, got %v", cfg.Denylist)
	}
	if !cfg.ProviderConsent["anthropic"] {
		t.Error("ProviderConsent[anthropic] should be true")
	}
	if cfg.ProviderConsent["openai"] {
		t.Error("ProviderConsent[openai] should be false")
	}
	if cfg.BudgetMainTokens != 100000 {
		t.Errorf("BudgetMainTokens = %d, want 100000", cfg.BudgetMainTokens)
	}
	if cfg.BudgetRouterTokens != 5000 {
		t.Errorf("BudgetRouterTokens = %d, want 5000", cfg.BudgetRouterTokens)
	}
	if cfg.BudgetParamTokens != 3000 {
		t.Errorf("BudgetParamTokens = %d, want 3000", cfg.BudgetParamTokens)
	}
	if cfg.CostLimitCents != 50.5 {
		t.Errorf("CostLimitCents = %f, want 50.5", cfg.CostLimitCents)
	}
	if cfg.RateLimitsPerMin["anthropic"] != 30 {
		t.Errorf("RateLimitsPerMin[anthropic] = %d, want 30", cfg.RateLimitsPerMin["anthropic"])
	}
	if cfg.RateLimitsPerMin["openai"] != 120 {
		t.Errorf("RateLimitsPerMin[openai] = %d, want 120", cfg.RateLimitsPerMin["openai"])
	}
	if cfg.RateLimitsPerMin["gemini"] != 60 {
		t.Errorf("RateLimitsPerMin[gemini] = %d, want 60 (default)", cfg.RateLimitsPerMin["gemini"])
	}
	if cfg.AuditEnabled {
		t.Error("AuditEnabled should be false")
	}
	if cfg.AuditHashContent {
		t.Error("AuditHashContent should be false")
	}
}

func TestLoadEgressConfig_InvalidValues(t *testing.T) {
	t.Setenv("TRACE_EGRESS_ENABLED", "notabool")
	t.Setenv("TRACE_BUDGET_MAIN_TOKENS", "notanint")
	t.Setenv("TRACE_COST_LIMIT_CENTS", "notafloat")

	cfg := LoadEgressConfig()

	// Invalid values should use defaults
	if !cfg.Enabled {
		t.Error("Invalid bool should fall back to default true")
	}
	if cfg.BudgetMainTokens != 0 {
		t.Errorf("Invalid int should fall back to default 0, got %d", cfg.BudgetMainTokens)
	}
	if cfg.CostLimitCents != 0 {
		t.Errorf("Invalid float should fall back to default 0, got %f", cfg.CostLimitCents)
	}
}

func TestEnvSet_Whitespace(t *testing.T) {
	t.Setenv("TRACE_EGRESS_ALLOWLIST", " anthropic , openai , ")

	cfg := LoadEgressConfig()

	if !cfg.Allowlist["anthropic"] {
		t.Error("Allowlist should contain 'anthropic' after trimming")
	}
	if !cfg.Allowlist["openai"] {
		t.Error("Allowlist should contain 'openai' after trimming")
	}
	if len(cfg.Allowlist) != 2 {
		t.Errorf("Allowlist should have 2 entries, got %d", len(cfg.Allowlist))
	}
}
