// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package providers

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Provider constants for supported LLM providers.
const (
	ProviderOllama    = "ollama"
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGemini    = "gemini"
)

// Role constants for LLM roles in the Trace agent.
const (
	RoleMain           = "MAIN"
	RoleRouter         = "ROUTER"
	RoleParamExtractor = "PARAM"
)

// ProviderConfig holds the configuration for a single LLM provider instance.
//
// Description:
//
//	Specifies which provider to use, which model, and any provider-specific
//	settings. Used by ProviderFactory to create the right adapter.
type ProviderConfig struct {
	// Provider is the backend to use: "ollama", "anthropic", "openai", "gemini".
	Provider string

	// Model is the provider-specific model identifier.
	// Examples: "granite4:micro-h" (Ollama), "claude-sonnet-4-20250514" (Anthropic).
	Model string

	// BaseURL is an optional endpoint override.
	// For Ollama: defaults to OLLAMA_BASE_URL or http://localhost:11434.
	// For cloud providers: uses the provider's default API URL.
	BaseURL string

	// APIKey is the authentication key for cloud providers.
	// Loaded from environment: ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY.
	APIKey string

	// KeepAlive controls model VRAM lifetime (Ollama-specific).
	KeepAlive string

	// NumCtx sets the context window size (Ollama-specific).
	NumCtx int
}

// RoleConfig holds per-role provider configurations.
//
// Description:
//
//	Contains the provider configuration for each of the three LLM roles
//	in the Trace agent: Main (synthesizer), Router, and ParamExtractor.
type RoleConfig struct {
	Main           ProviderConfig
	Router         ProviderConfig
	ParamExtractor ProviderConfig
}

// ValidProviders contains the set of valid provider names.
var ValidProviders = []string{ProviderOllama, ProviderAnthropic, ProviderOpenAI, ProviderGemini}

// isValidProvider checks if a provider name is valid.
func isValidProvider(provider string) bool {
	for _, p := range ValidProviders {
		if provider == p {
			return true
		}
	}
	return false
}

// ResolveOllamaURL resolves the Ollama server URL from environment variables.
//
// Description:
//
//	Resolution order:
//	  1. OLLAMA_BASE_URL (preferred)
//	  2. OLLAMA_URL (deprecated, emits warning)
//	  3. http://localhost:11434 (default)
//
// Outputs:
//   - string: The resolved Ollama URL.
func ResolveOllamaURL() string {
	if url := os.Getenv("OLLAMA_BASE_URL"); url != "" {
		return url
	}
	if url := os.Getenv("OLLAMA_URL"); url != "" {
		slog.Warn("OLLAMA_URL is deprecated, use OLLAMA_BASE_URL instead",
			slog.String("ollama_url", url))
		return url
	}
	return "http://localhost:11434"
}

// InferProvider infers the provider from a model name prefix.
//
// Description:
//
//	Maps known model name prefixes to provider names:
//	  - "claude-*" -> "anthropic"
//	  - "gpt-*" -> "openai"
//	  - "gemini-*" -> "gemini"
//	  - anything else -> "" (unknown)
//
//	This is a utility function for display/inference purposes.
//	It does not auto-apply; the default Ollama fallback is unchanged.
//
// Inputs:
//   - model: The model name to infer from.
//
// Outputs:
//   - string: The inferred provider name, or empty string if unknown.
func InferProvider(model string) string {
	if strings.HasPrefix(model, "claude-") {
		return ProviderAnthropic
	}
	if strings.HasPrefix(model, "gpt-") {
		return ProviderOpenAI
	}
	if strings.HasPrefix(model, "gemini-") {
		return ProviderGemini
	}
	return ""
}

// MergeSessionOverrides returns a copy of base with Router and ParamExtractor
// models overridden by per-session values when non-empty.
//
// Description:
//
//	Creates a shallow copy of the base RoleConfig and overrides the Router.Model
//	and ParamExtractor.Model fields when the override values are non-empty.
//	The Main config is never overridden per-session.
//
// Inputs:
//   - base: The base RoleConfig loaded at startup. Must not be nil.
//   - routerModel: Override for Router.Model. Empty string means keep base.
//   - paramModel: Override for ParamExtractor.Model. Empty string means keep base.
//
// Outputs:
//   - *RoleConfig: A new RoleConfig with overrides applied.
func MergeSessionOverrides(base *RoleConfig, routerModel, paramModel string) *RoleConfig {
	if base == nil {
		return nil
	}

	// Copy all fields explicitly (lesson J-7: no implicit field forwarding)
	merged := &RoleConfig{
		Main: ProviderConfig{
			Provider:  base.Main.Provider,
			Model:     base.Main.Model,
			BaseURL:   base.Main.BaseURL,
			APIKey:    base.Main.APIKey,
			KeepAlive: base.Main.KeepAlive,
			NumCtx:    base.Main.NumCtx,
		},
		Router: ProviderConfig{
			Provider:  base.Router.Provider,
			Model:     base.Router.Model,
			BaseURL:   base.Router.BaseURL,
			APIKey:    base.Router.APIKey,
			KeepAlive: base.Router.KeepAlive,
			NumCtx:    base.Router.NumCtx,
		},
		ParamExtractor: ProviderConfig{
			Provider:  base.ParamExtractor.Provider,
			Model:     base.ParamExtractor.Model,
			BaseURL:   base.ParamExtractor.BaseURL,
			APIKey:    base.ParamExtractor.APIKey,
			KeepAlive: base.ParamExtractor.KeepAlive,
			NumCtx:    base.ParamExtractor.NumCtx,
		},
	}

	if routerModel != "" {
		merged.Router.Model = routerModel
	}
	if paramModel != "" {
		merged.ParamExtractor.Model = paramModel
	}

	return merged
}

// LoadRoleConfig reads per-role provider configuration from environment variables.
//
// Description:
//
//	Reads TRACE_<ROLE>_PROVIDER and TRACE_<ROLE>_MODEL environment variables
//	for each role. Falls back to Ollama with existing env vars for backward
//	compatibility when the new vars are not set.
//
// Resolution order:
//  1. TRACE_<ROLE>_PROVIDER -> explicit provider
//  2. Fallback: "ollama" (backward compatible)
//  3. TRACE_<ROLE>_MODEL -> explicit model
//  4. Fallback: existing env vars (OLLAMA_MODEL for main, session config for router/param)
//
// Inputs:
//   - mainModelFallback: Fallback model for the main role (e.g., from OLLAMA_MODEL).
//   - routerModelFallback: Fallback model for the router role (e.g., from SessionConfig).
//   - paramModelFallback: Fallback model for the param extractor role.
//
// Outputs:
//   - *RoleConfig: Per-role configurations.
//   - error: Non-nil if an invalid provider is specified.
//
// Example:
//
//	cfg, err := LoadRoleConfig("glm-4.7-flash", "granite4:micro-h", "ministral-3:3b")
func LoadRoleConfig(mainModelFallback, routerModelFallback, paramModelFallback string) (*RoleConfig, error) {
	mainCfg, err := loadSingleRoleConfig(RoleMain, mainModelFallback)
	if err != nil {
		return nil, fmt.Errorf("loading main role config: %w", err)
	}

	routerCfg, err := loadSingleRoleConfig(RoleRouter, routerModelFallback)
	if err != nil {
		return nil, fmt.Errorf("loading router role config: %w", err)
	}

	paramCfg, err := loadSingleRoleConfig(RoleParamExtractor, paramModelFallback)
	if err != nil {
		return nil, fmt.Errorf("loading param extractor role config: %w", err)
	}

	return &RoleConfig{
		Main:           mainCfg,
		Router:         routerCfg,
		ParamExtractor: paramCfg,
	}, nil
}

// loadSingleRoleConfig loads configuration for a single role.
func loadSingleRoleConfig(role, modelFallback string) (ProviderConfig, error) {
	providerEnv := fmt.Sprintf("TRACE_%s_PROVIDER", role)
	modelEnv := fmt.Sprintf("TRACE_%s_MODEL", role)

	provider := os.Getenv(providerEnv)
	if provider == "" {
		provider = ProviderOllama
	}

	if !isValidProvider(provider) {
		return ProviderConfig{}, fmt.Errorf("invalid provider %q for %s (valid: %v)", provider, providerEnv, ValidProviders)
	}

	model := os.Getenv(modelEnv)
	if model == "" {
		model = modelFallback
	}

	cfg := ProviderConfig{
		Provider: provider,
		Model:    model,
	}

	// Load provider-specific settings
	switch provider {
	case ProviderOllama:
		cfg.BaseURL = ResolveOllamaURL()
	case ProviderAnthropic:
		cfg.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	case ProviderOpenAI:
		cfg.APIKey = os.Getenv("OPENAI_API_KEY")
	case ProviderGemini:
		cfg.APIKey = os.Getenv("GEMINI_API_KEY")
	}

	// Validate: if provider is explicitly set but model is empty and no fallback was provided,
	// return a descriptive error to help the operator diagnose misconfiguration.
	explicitProvider := os.Getenv(providerEnv)
	if explicitProvider != "" && cfg.Model == "" {
		return ProviderConfig{}, fmt.Errorf(
			"%s is %q but no model specified (set %s or pass fallback)",
			providerEnv, provider, modelEnv,
		)
	}

	return cfg, nil
}
