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
	"os"
	"strconv"
	"strings"
)

// EgressConfig holds all configuration for the egress guard system.
//
// Description:
//
//	Loaded from environment variables at startup via LoadEgressConfig().
//	All fields have safe defaults (egress enabled, audit enabled, no cost limit).
//
// Thread Safety: EgressConfig is a value type. Safe to copy and share after loading.
type EgressConfig struct {
	// Enabled controls the global kill switch. When false, all cloud egress is blocked.
	// Env: TRACE_EGRESS_ENABLED (default: "true")
	Enabled bool

	// LocalOnly when true blocks all cloud providers, allowing only Ollama.
	// Env: TRACE_LOCAL_ONLY (default: "false")
	LocalOnly bool

	// Allowlist is a set of explicitly allowed provider names. Empty means all allowed.
	// Env: TRACE_EGRESS_ALLOWLIST (comma-separated, default: "")
	Allowlist map[string]bool

	// Denylist is a set of explicitly denied provider names. Denylist takes precedence.
	// Env: TRACE_EGRESS_DENYLIST (comma-separated, default: "")
	Denylist map[string]bool

	// ProviderConsent maps provider names to consent status.
	// Env: TRACE_CONSENT_ANTHROPIC, TRACE_CONSENT_OPENAI, TRACE_CONSENT_GEMINI
	ProviderConsent map[string]bool

	// BudgetMainTokens is the token budget for the main role per session.
	// Env: TRACE_BUDGET_MAIN_TOKENS (default: 0 = unlimited)
	BudgetMainTokens int

	// BudgetRouterTokens is the token budget for the router role per session.
	// Env: TRACE_BUDGET_ROUTER_TOKENS (default: 0 = unlimited)
	BudgetRouterTokens int

	// BudgetParamTokens is the token budget for the param extractor role per session.
	// Env: TRACE_BUDGET_PARAM_TOKENS (default: 0 = unlimited)
	BudgetParamTokens int

	// CostLimitCents is the maximum cost in US cents for the session.
	// Env: TRACE_COST_LIMIT_CENTS (default: 0 = unlimited)
	CostLimitCents float64

	// RateLimitsPerMin maps provider names to per-minute rate limits.
	// Env: TRACE_RATE_ANTHROPIC_PER_MIN, TRACE_RATE_OPENAI_PER_MIN, etc.
	// Default: 60 for all cloud providers.
	RateLimitsPerMin map[string]int

	// AuditEnabled controls whether egress audit logging is active.
	// Env: TRACE_AUDIT_ENABLED (default: "true")
	AuditEnabled bool

	// AuditHashContent controls whether request content is SHA256-hashed in audit logs.
	// Env: TRACE_AUDIT_HASH_CONTENT (default: "true")
	AuditHashContent bool

	// MinimizationEnabled controls whether request minimization is applied
	// before sending to external providers. When false, requests are sent as-is.
	// Env: TRACE_MINIMIZATION_ENABLED (default: "true")
	MinimizationEnabled bool

	// MinContextTokens is the threshold below which minimization is skipped.
	// Requests estimated at fewer tokens than this are already small enough.
	// Env: TRACE_MIN_CONTEXT_TOKENS (default: 500)
	MinContextTokens int
}

// LoadEgressConfig reads egress configuration from environment variables.
//
// Description:
//
//	Reads all TRACE_EGRESS_*, TRACE_LOCAL_ONLY, TRACE_CONSENT_*, TRACE_BUDGET_*,
//	TRACE_COST_*, TRACE_RATE_*, and TRACE_AUDIT_* environment variables. Provides
//	safe defaults for all values.
//
// Outputs:
//   - *EgressConfig: Fully populated configuration.
func LoadEgressConfig() *EgressConfig {
	cfg := &EgressConfig{
		Enabled:             envBool("TRACE_EGRESS_ENABLED", true),
		LocalOnly:           envBool("TRACE_LOCAL_ONLY", false),
		Allowlist:           envSet("TRACE_EGRESS_ALLOWLIST"),
		Denylist:            envSet("TRACE_EGRESS_DENYLIST"),
		AuditEnabled:        envBool("TRACE_AUDIT_ENABLED", true),
		AuditHashContent:    envBool("TRACE_AUDIT_HASH_CONTENT", true),
		BudgetMainTokens:    envInt("TRACE_BUDGET_MAIN_TOKENS", 0),
		BudgetRouterTokens:  envInt("TRACE_BUDGET_ROUTER_TOKENS", 0),
		BudgetParamTokens:   envInt("TRACE_BUDGET_PARAM_TOKENS", 0),
		CostLimitCents:      envFloat("TRACE_COST_LIMIT_CENTS", 0),
		MinimizationEnabled: envBool("TRACE_MINIMIZATION_ENABLED", true),
		MinContextTokens:    envInt("TRACE_MIN_CONTEXT_TOKENS", 500),
		RateLimitsPerMin:    make(map[string]int),
		ProviderConsent:     make(map[string]bool),
	}

	// Load per-provider consent
	cloudProviders := []string{"anthropic", "openai", "gemini"}
	for _, p := range cloudProviders {
		envKey := "TRACE_CONSENT_" + strings.ToUpper(p)
		cfg.ProviderConsent[p] = envBool(envKey, false)
	}

	// Load per-provider rate limits (default: 60/min)
	for _, p := range cloudProviders {
		envKey := "TRACE_RATE_" + strings.ToUpper(p) + "_PER_MIN"
		cfg.RateLimitsPerMin[p] = envInt(envKey, 60)
	}

	return cfg
}

// envBool reads a boolean environment variable with a default value.
func envBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

// envInt reads an integer environment variable with a default value.
func envInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return n
}

// envFloat reads a float64 environment variable with a default value.
func envFloat(key string, defaultVal float64) float64 {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// envSet reads a comma-separated environment variable into a set.
// Returns an empty map (not nil) if the variable is unset.
func envSet(key string) map[string]bool {
	result := make(map[string]bool)
	val := os.Getenv(key)
	if val == "" {
		return result
	}
	for _, item := range strings.Split(val, ",") {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result[trimmed] = true
		}
	}
	return result
}
