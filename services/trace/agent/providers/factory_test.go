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
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
)

func TestLoadRoleConfig_Defaults(t *testing.T) {
	// Clear any existing env vars
	t.Setenv("TRACE_MAIN_PROVIDER", "")
	t.Setenv("TRACE_ROUTER_PROVIDER", "")
	t.Setenv("TRACE_PARAM_PROVIDER", "")
	t.Setenv("TRACE_MAIN_MODEL", "")
	t.Setenv("TRACE_ROUTER_MODEL", "")
	t.Setenv("TRACE_PARAM_MODEL", "")

	cfg, err := LoadRoleConfig("glm-4.7-flash", "granite4:micro-h", "ministral-3:3b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All should default to Ollama
	if cfg.Main.Provider != ProviderOllama {
		t.Errorf("Main.Provider = %q, want %q", cfg.Main.Provider, ProviderOllama)
	}
	if cfg.Router.Provider != ProviderOllama {
		t.Errorf("Router.Provider = %q, want %q", cfg.Router.Provider, ProviderOllama)
	}
	if cfg.ParamExtractor.Provider != ProviderOllama {
		t.Errorf("ParamExtractor.Provider = %q, want %q", cfg.ParamExtractor.Provider, ProviderOllama)
	}

	// Models should use fallbacks
	if cfg.Main.Model != "glm-4.7-flash" {
		t.Errorf("Main.Model = %q, want %q", cfg.Main.Model, "glm-4.7-flash")
	}
	if cfg.Router.Model != "granite4:micro-h" {
		t.Errorf("Router.Model = %q, want %q", cfg.Router.Model, "granite4:micro-h")
	}
	if cfg.ParamExtractor.Model != "ministral-3:3b" {
		t.Errorf("ParamExtractor.Model = %q, want %q", cfg.ParamExtractor.Model, "ministral-3:3b")
	}
}

func TestLoadRoleConfig_ExplicitProviders(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "anthropic")
	t.Setenv("TRACE_MAIN_MODEL", "claude-sonnet-4-20250514")
	t.Setenv("TRACE_ROUTER_PROVIDER", "ollama")
	t.Setenv("TRACE_ROUTER_MODEL", "granite4:micro-h")
	t.Setenv("TRACE_PARAM_PROVIDER", "openai")
	t.Setenv("TRACE_PARAM_MODEL", "gpt-4o-mini")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")

	cfg, err := LoadRoleConfig("fallback-main", "fallback-router", "fallback-param")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Main.Provider != ProviderAnthropic {
		t.Errorf("Main.Provider = %q, want %q", cfg.Main.Provider, ProviderAnthropic)
	}
	if cfg.Main.Model != "claude-sonnet-4-20250514" {
		t.Errorf("Main.Model = %q, want %q", cfg.Main.Model, "claude-sonnet-4-20250514")
	}
	if cfg.Router.Provider != ProviderOllama {
		t.Errorf("Router.Provider = %q, want %q", cfg.Router.Provider, ProviderOllama)
	}
	if cfg.ParamExtractor.Provider != ProviderOpenAI {
		t.Errorf("ParamExtractor.Provider = %q, want %q", cfg.ParamExtractor.Provider, ProviderOpenAI)
	}
	if cfg.ParamExtractor.Model != "gpt-4o-mini" {
		t.Errorf("ParamExtractor.Model = %q, want %q", cfg.ParamExtractor.Model, "gpt-4o-mini")
	}
}

func TestLoadRoleConfig_InvalidProvider(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "invalid_provider")

	_, err := LoadRoleConfig("model", "router", "param")
	if err == nil {
		t.Fatal("expected error for invalid provider, got nil")
	}
}

func TestLoadRoleConfig_ModelEnvOverridesFallback(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "")
	t.Setenv("TRACE_MAIN_MODEL", "custom-model")

	cfg, err := LoadRoleConfig("fallback-model", "router", "param")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Main.Model != "custom-model" {
		t.Errorf("Main.Model = %q, want %q (env should override fallback)", cfg.Main.Model, "custom-model")
	}
}

func TestLoadRoleConfig_OllamaBaseURL(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "")
	t.Setenv("TRACE_MAIN_MODEL", "")
	t.Setenv("OLLAMA_BASE_URL", "http://custom:11434")
	t.Setenv("OLLAMA_URL", "")

	cfg, err := LoadRoleConfig("model", "router", "param")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Main.BaseURL != "http://custom:11434" {
		t.Errorf("Main.BaseURL = %q, want %q", cfg.Main.BaseURL, "http://custom:11434")
	}
}

func TestLoadRoleConfig_OllamaURLFallback(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_URL", "http://alt:11434")

	cfg, err := LoadRoleConfig("model", "router", "param")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Main.BaseURL != "http://alt:11434" {
		t.Errorf("Main.BaseURL = %q, want %q", cfg.Main.BaseURL, "http://alt:11434")
	}
}

func TestNewProviderFactory_NilModelManager(t *testing.T) {
	factory := NewProviderFactory(nil)
	if factory == nil {
		t.Fatal("expected non-nil factory")
	}
}

func TestProviderFactory_CreateChatClient_OllamaRequiresManager(t *testing.T) {
	factory := NewProviderFactory(nil)

	_, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOllama})
	if err == nil {
		t.Fatal("expected error when creating Ollama client without model manager")
	}
}

func TestProviderFactory_CreateAgentClient_OllamaRequiresManager(t *testing.T) {
	factory := NewProviderFactory(nil)

	_, err := factory.CreateAgentClient(ProviderConfig{Provider: ProviderOllama, Model: "test"})
	if err == nil {
		t.Fatal("expected error when creating Ollama agent client without model manager")
	}
}

func TestProviderFactory_CreateChatClient_CloudRequiresAPIKey(t *testing.T) {
	factory := NewProviderFactory(nil)

	tests := []struct {
		provider string
	}{
		{ProviderAnthropic},
		{ProviderOpenAI},
		{ProviderGemini},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			_, err := factory.CreateChatClient(ProviderConfig{Provider: tt.provider, APIKey: ""})
			if err == nil {
				t.Fatalf("expected error for %s without API key", tt.provider)
			}
		})
	}
}

func TestProviderFactory_CreateChatClient_InvalidProvider(t *testing.T) {
	factory := NewProviderFactory(nil)

	_, err := factory.CreateChatClient(ProviderConfig{Provider: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
}

func TestProviderFactory_CreateChatClient_Ollama(t *testing.T) {
	mgr := llm.NewMultiModelManager("http://localhost:11434")
	factory := NewProviderFactory(mgr)

	client, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOllama, Model: "test-model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestProviderFactory_CreateLifecycleManager_Ollama(t *testing.T) {
	mgr := llm.NewMultiModelManager("http://localhost:11434")
	factory := NewProviderFactory(mgr)

	lm, err := factory.CreateLifecycleManager(ProviderConfig{Provider: ProviderOllama})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !lm.IsLocal() {
		t.Error("Ollama lifecycle manager should be local")
	}
}

func TestProviderFactory_CreateLifecycleManager_Cloud(t *testing.T) {
	factory := NewProviderFactory(nil)

	for _, provider := range []string{ProviderAnthropic, ProviderOpenAI, ProviderGemini} {
		t.Run(provider, func(t *testing.T) {
			lm, err := factory.CreateLifecycleManager(ProviderConfig{Provider: provider})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if lm.IsLocal() {
				t.Errorf("%s lifecycle manager should not be local", provider)
			}
		})
	}
}

func TestIsValidProvider(t *testing.T) {
	for _, p := range ValidProviders {
		if !isValidProvider(p) {
			t.Errorf("isValidProvider(%q) = false, want true", p)
		}
	}

	if isValidProvider("invalid") {
		t.Error("isValidProvider(\"invalid\") = true, want false")
	}
}

// =============================================================================
// CB-60b: ResolveOllamaURL Tests
// =============================================================================

func TestResolveOllamaURL_BaseURLPriority(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "http://base:11434")
	t.Setenv("OLLAMA_URL", "http://deprecated:11434")

	got := ResolveOllamaURL()
	if got != "http://base:11434" {
		t.Errorf("ResolveOllamaURL() = %q, want %q (OLLAMA_BASE_URL should win)", got, "http://base:11434")
	}
}

func TestResolveOllamaURL_DeprecatedFallback(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_URL", "http://deprecated:11434")

	got := ResolveOllamaURL()
	if got != "http://deprecated:11434" {
		t.Errorf("ResolveOllamaURL() = %q, want %q", got, "http://deprecated:11434")
	}
}

func TestResolveOllamaURL_Default(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_URL", "")

	got := ResolveOllamaURL()
	if got != "http://localhost:11434" {
		t.Errorf("ResolveOllamaURL() = %q, want %q", got, "http://localhost:11434")
	}
}

// =============================================================================
// CB-60b: InferProvider Tests
// =============================================================================

func TestInferProvider_KnownPrefixes(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"claude-sonnet-4-20250514", ProviderAnthropic},
		{"claude-haiku-4-5-20251001", ProviderAnthropic},
		{"gpt-4o", ProviderOpenAI},
		{"gpt-4o-mini", ProviderOpenAI},
		{"gemini-1.5-flash", ProviderGemini},
		{"gemini-2.0-pro", ProviderGemini},
		{"granite4:micro-h", ""},
		{"glm-4.7-flash", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := InferProvider(tt.model)
			if got != tt.want {
				t.Errorf("InferProvider(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

// =============================================================================
// CB-60b: MergeSessionOverrides Tests
// =============================================================================

func TestMergeSessionOverrides_EmptyKeepsBase(t *testing.T) {
	base := &RoleConfig{
		Main:           ProviderConfig{Provider: ProviderOllama, Model: "main-model"},
		Router:         ProviderConfig{Provider: ProviderOllama, Model: "router-model"},
		ParamExtractor: ProviderConfig{Provider: ProviderOllama, Model: "param-model"},
	}

	merged := MergeSessionOverrides(base, "", "")

	if merged.Router.Model != "router-model" {
		t.Errorf("Router.Model = %q, want %q", merged.Router.Model, "router-model")
	}
	if merged.ParamExtractor.Model != "param-model" {
		t.Errorf("ParamExtractor.Model = %q, want %q", merged.ParamExtractor.Model, "param-model")
	}
	if merged.Main.Model != "main-model" {
		t.Errorf("Main.Model = %q, want %q", merged.Main.Model, "main-model")
	}
}

func TestMergeSessionOverrides_OverridesApplied(t *testing.T) {
	base := &RoleConfig{
		Main:           ProviderConfig{Provider: ProviderOllama, Model: "main-model"},
		Router:         ProviderConfig{Provider: ProviderOllama, Model: "router-model"},
		ParamExtractor: ProviderConfig{Provider: ProviderOllama, Model: "param-model"},
	}

	merged := MergeSessionOverrides(base, "new-router", "new-param")

	if merged.Router.Model != "new-router" {
		t.Errorf("Router.Model = %q, want %q", merged.Router.Model, "new-router")
	}
	if merged.ParamExtractor.Model != "new-param" {
		t.Errorf("ParamExtractor.Model = %q, want %q", merged.ParamExtractor.Model, "new-param")
	}
}

func TestMergeSessionOverrides_MainNeverOverridden(t *testing.T) {
	base := &RoleConfig{
		Main:           ProviderConfig{Provider: ProviderAnthropic, Model: "claude-sonnet", APIKey: "sk-test"},
		Router:         ProviderConfig{Provider: ProviderOllama, Model: "router-model"},
		ParamExtractor: ProviderConfig{Provider: ProviderOllama, Model: "param-model"},
	}

	merged := MergeSessionOverrides(base, "new-router", "new-param")

	// Main must be identical to base
	if merged.Main.Provider != base.Main.Provider {
		t.Errorf("Main.Provider = %q, want %q", merged.Main.Provider, base.Main.Provider)
	}
	if merged.Main.Model != base.Main.Model {
		t.Errorf("Main.Model = %q, want %q", merged.Main.Model, base.Main.Model)
	}
	if merged.Main.APIKey != base.Main.APIKey {
		t.Errorf("Main.APIKey was modified")
	}
}

func TestMergeSessionOverrides_NilBase(t *testing.T) {
	result := MergeSessionOverrides(nil, "router", "param")
	if result != nil {
		t.Error("expected nil for nil base")
	}
}

func TestMergeSessionOverrides_DoesNotMutateBase(t *testing.T) {
	base := &RoleConfig{
		Router:         ProviderConfig{Model: "original"},
		ParamExtractor: ProviderConfig{Model: "original"},
	}

	_ = MergeSessionOverrides(base, "override", "override")

	if base.Router.Model != "original" {
		t.Errorf("base.Router.Model was mutated to %q", base.Router.Model)
	}
	if base.ParamExtractor.Model != "original" {
		t.Errorf("base.ParamExtractor.Model was mutated to %q", base.ParamExtractor.Model)
	}
}

// =============================================================================
// CB-60b: Validation Tests
// =============================================================================

func TestLoadRoleConfig_ExplicitProviderEmptyModel_Error(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "anthropic")
	t.Setenv("TRACE_MAIN_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	_, err := LoadRoleConfig("", "", "")
	if err == nil {
		t.Fatal("expected error when provider is explicitly set but model is empty")
	}

	// Verify error message is descriptive
	errMsg := err.Error()
	if !contains(errMsg, "TRACE_MAIN_PROVIDER") || !contains(errMsg, "anthropic") {
		t.Errorf("error message should mention TRACE_MAIN_PROVIDER and anthropic, got: %s", errMsg)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// CB-60f: All-cloud config (nil ModelManager)
// =============================================================================

func TestProviderFactory_AllCloudConfig_NilModelManager(t *testing.T) {
	// With nil ModelManager, cloud providers should work but Ollama should fail.
	factory := NewProviderFactory(nil)

	// Cloud ChatClient should succeed with API keys
	t.Run("anthropic_chat_succeeds", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		_, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderAnthropic, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("expected success for Anthropic with API key: %v", err)
		}
	})

	t.Run("openai_chat_succeeds", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "test-key")
		_, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOpenAI, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("expected success for OpenAI with API key: %v", err)
		}
	})

	t.Run("gemini_chat_succeeds", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "test-key")
		_, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderGemini, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("expected success for Gemini with API key: %v", err)
		}
	})

	t.Run("ollama_chat_fails", func(t *testing.T) {
		_, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOllama})
		if err == nil {
			t.Fatal("expected error for Ollama without model manager")
		}
	})

	t.Run("ollama_agent_fails", func(t *testing.T) {
		_, err := factory.CreateAgentClient(ProviderConfig{Provider: ProviderOllama, Model: "test"})
		if err == nil {
			t.Fatal("expected error for Ollama agent without model manager")
		}
	})

	// Cloud lifecycle managers should succeed
	for _, provider := range []string{ProviderAnthropic, ProviderOpenAI, ProviderGemini} {
		t.Run(provider+"_lifecycle_succeeds", func(t *testing.T) {
			lm, err := factory.CreateLifecycleManager(ProviderConfig{Provider: provider})
			if err != nil {
				t.Fatalf("expected success: %v", err)
			}
			if lm.IsLocal() {
				t.Error("cloud lifecycle manager should not be local")
			}
		})
	}
}

func TestProviderFactory_MixedConfig(t *testing.T) {
	// Mixed config: Ollama for some roles, cloud for others.
	mgr := llm.NewMultiModelManager("http://localhost:11434")
	factory := NewProviderFactory(mgr)

	t.Run("ollama_chat_succeeds_with_manager", func(t *testing.T) {
		client, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOllama, Model: "granite4:micro-h"})
		if err != nil {
			t.Fatalf("expected success: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
	})

	t.Run("cloud_chat_succeeds_alongside_ollama", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "test-key")
		client, err := factory.CreateChatClient(ProviderConfig{Provider: ProviderOpenAI, APIKey: "test-key"})
		if err != nil {
			t.Fatalf("expected success: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
	})

	t.Run("ollama_lifecycle_is_local", func(t *testing.T) {
		lm, err := factory.CreateLifecycleManager(ProviderConfig{Provider: ProviderOllama})
		if err != nil {
			t.Fatalf("expected success: %v", err)
		}
		if !lm.IsLocal() {
			t.Error("Ollama lifecycle should be local")
		}
	})
}

func TestLoadRoleConfig_GeminiProvider(t *testing.T) {
	t.Setenv("TRACE_MAIN_PROVIDER", "gemini")
	t.Setenv("TRACE_MAIN_MODEL", "gemini-1.5-flash")
	t.Setenv("GEMINI_API_KEY", "test-key")

	// Unset to avoid OLLAMA_URL pollution
	prevURL := os.Getenv("OLLAMA_URL")
	prevBase := os.Getenv("OLLAMA_BASE_URL")
	t.Setenv("OLLAMA_URL", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	defer func() {
		os.Setenv("OLLAMA_URL", prevURL)
		os.Setenv("OLLAMA_BASE_URL", prevBase)
	}()

	cfg, err := LoadRoleConfig("fallback", "router", "param")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Main.Provider != ProviderGemini {
		t.Errorf("Main.Provider = %q, want %q", cfg.Main.Provider, ProviderGemini)
	}
	if cfg.Main.Model != "gemini-1.5-flash" {
		t.Errorf("Main.Model = %q, want %q", cfg.Main.Model, "gemini-1.5-flash")
	}
	if cfg.Main.APIKey != "test-key" {
		t.Errorf("Main.APIKey = %q, want %q", cfg.Main.APIKey, "test-key")
	}
}
