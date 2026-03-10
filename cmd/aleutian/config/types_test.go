// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

/*
Package config contains unit tests for configuration types.

# Testing Strategy

These tests verify:
  - Default values are correctly applied
  - Getter methods return expected values
  - ConfigMeta is properly initialized
  - ForecastMode validation works correctly
*/
package config

import (
	"os/exec"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// ForecastMode Tests
// -----------------------------------------------------------------------------

// TestForecastMode_IsValid verifies the IsValid method.
func TestForecastMode_IsValid(t *testing.T) {
	tests := []struct {
		mode     ForecastMode
		expected bool
	}{
		{ForecastModeStandalone, true},
		{ForecastModeSapheneia, true},
		{ForecastMode("invalid"), false},
		{ForecastMode(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			if got := tt.mode.IsValid(); got != tt.expected {
				t.Errorf("ForecastMode(%q).IsValid() = %v, want %v",
					tt.mode, got, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// OllamaConfig Tests
// -----------------------------------------------------------------------------

// TestOllamaConfig_GetEmbeddingModel verifies default fallback.
func TestOllamaConfig_GetEmbeddingModel(t *testing.T) {
	tests := []struct {
		name     string
		config   OllamaConfig
		expected string
	}{
		{
			name:     "returns configured value",
			config:   OllamaConfig{EmbeddingModel: "custom-embed"},
			expected: "custom-embed",
		},
		{
			name:     "returns default when empty",
			config:   OllamaConfig{EmbeddingModel: ""},
			expected: DefaultEmbeddingModel,
		},
		{
			name:     "returns default for zero value",
			config:   OllamaConfig{},
			expected: DefaultEmbeddingModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetEmbeddingModel(); got != tt.expected {
				t.Errorf("GetEmbeddingModel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestOllamaConfig_GetDiskLimitGB verifies default fallback.
func TestOllamaConfig_GetDiskLimitGB(t *testing.T) {
	tests := []struct {
		name     string
		config   OllamaConfig
		expected int64
	}{
		{
			name:     "returns configured value",
			config:   OllamaConfig{DiskLimitGB: 100},
			expected: 100,
		},
		{
			name:     "returns default when zero",
			config:   OllamaConfig{DiskLimitGB: 0},
			expected: DefaultDiskLimitGB,
		},
		{
			name:     "returns default when negative",
			config:   OllamaConfig{DiskLimitGB: -10},
			expected: DefaultDiskLimitGB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetDiskLimitGB(); got != tt.expected {
				t.Errorf("GetDiskLimitGB() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// TestOllamaConfig_GetBaseURL verifies default fallback.
func TestOllamaConfig_GetBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		config   OllamaConfig
		expected string
	}{
		{
			name:     "returns configured value",
			config:   OllamaConfig{BaseURL: "http://custom:11434"},
			expected: "http://custom:11434",
		},
		{
			name:     "returns default when empty",
			config:   OllamaConfig{BaseURL: ""},
			expected: DefaultOllamaHostURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetBaseURL(); got != tt.expected {
				t.Errorf("GetBaseURL() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// ConfigMeta Tests
// -----------------------------------------------------------------------------

// TestNewConfigMeta verifies metadata initialization.
func TestNewConfigMeta(t *testing.T) {
	before := time.Now().UnixMilli()
	meta := newConfigMeta()
	after := time.Now().UnixMilli()

	// Check version
	if meta.Version != CurrentConfigVersion {
		t.Errorf("Version = %q, want %q", meta.Version, CurrentConfigVersion)
	}

	// Check ModifiedBy
	if meta.ModifiedBy != "aleutian-cli" {
		t.Errorf("ModifiedBy = %q, want %q", meta.ModifiedBy, "aleutian-cli")
	}

	// Verify timestamps are within bounds
	if meta.CreatedAt < before || meta.CreatedAt > after {
		t.Errorf("CreatedAt %d not between %d and %d", meta.CreatedAt, before, after)
	}

	if meta.ModifiedAt < before || meta.ModifiedAt > after {
		t.Errorf("ModifiedAt %d not between %d and %d", meta.ModifiedAt, before, after)
	}

	// CreatedAt and ModifiedAt should be equal for new config
	if meta.CreatedAt != meta.ModifiedAt {
		t.Errorf("CreatedAt (%d) != ModifiedAt (%d) for new config",
			meta.CreatedAt, meta.ModifiedAt)
	}
}

// TestConfigMeta_TimeConversion verifies timestamp helper methods.
func TestConfigMeta_TimeConversion(t *testing.T) {
	now := time.Now()
	meta := ConfigMeta{
		CreatedAt:  now.UnixMilli(),
		ModifiedAt: now.UnixMilli(),
	}

	createdTime := meta.CreatedAtTime()
	modifiedTime := meta.ModifiedAtTime()

	// Allow 1ms tolerance due to conversion precision
	if createdTime.Sub(now).Abs() > time.Millisecond {
		t.Errorf("CreatedAtTime() differs by more than 1ms from original")
	}

	if modifiedTime.Sub(now).Abs() > time.Millisecond {
		t.Errorf("ModifiedAtTime() differs by more than 1ms from original")
	}
}

// -----------------------------------------------------------------------------
// DefaultConfig Tests
// -----------------------------------------------------------------------------

// TestDefaultConfig_HasMeta verifies metadata is included.
func TestDefaultConfig_HasMeta(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Meta.Version == "" {
		t.Error("Meta.Version should not be empty")
	}

	if cfg.Meta.CreatedAt == 0 {
		t.Error("Meta.CreatedAt should not be zero")
	}

	if cfg.Meta.ModifiedAt == 0 {
		t.Error("Meta.ModifiedAt should not be zero")
	}

	if cfg.Meta.ModifiedBy == "" {
		t.Error("Meta.ModifiedBy should not be empty")
	}
}

// TestDefaultConfig_OllamaDefaults verifies Ollama configuration.
func TestDefaultConfig_OllamaDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ModelBackend.Type != "ollama" {
		t.Errorf("ModelBackend.Type = %q, want %q", cfg.ModelBackend.Type, "ollama")
	}

	if cfg.ModelBackend.Ollama.EmbeddingModel != DefaultEmbeddingModel {
		t.Errorf("Ollama.EmbeddingModel = %q, want %q",
			cfg.ModelBackend.Ollama.EmbeddingModel, DefaultEmbeddingModel)
	}

	if cfg.ModelBackend.Ollama.DiskLimitGB != DefaultDiskLimitGB {
		t.Errorf("Ollama.DiskLimitGB = %d, want %d",
			cfg.ModelBackend.Ollama.DiskLimitGB, DefaultDiskLimitGB)
	}

	if cfg.ModelBackend.Ollama.BaseURL != DefaultOllamaHostURL {
		t.Errorf("Ollama.BaseURL = %q, want %q",
			cfg.ModelBackend.Ollama.BaseURL, DefaultOllamaHostURL)
	}
}

// TestDefaultConfig_MachineDefaults verifies machine configuration.
func TestDefaultConfig_MachineDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Machine.Id != "podman-machine-default" {
		t.Errorf("Machine.Id = %q, want %q", cfg.Machine.Id, "podman-machine-default")
	}

	if cfg.Machine.CPUCount != 6 {
		t.Errorf("Machine.CPUCount = %d, want %d", cfg.Machine.CPUCount, 6)
	}

	if cfg.Machine.MemoryAmount != 20480 {
		t.Errorf("Machine.MemoryAmount = %d, want %d", cfg.Machine.MemoryAmount, 20480)
	}
}

// TestDefaultConfig_FeatureDefaults verifies feature toggles.
func TestDefaultConfig_FeatureDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Features.Observability {
		t.Error("Features.Observability should be true by default")
	}

	if !cfg.Features.RagEngine {
		t.Error("Features.RagEngine should be true by default")
	}
}

// TestDefaultConfig_ForecastDefaults verifies forecast configuration.
func TestDefaultConfig_ForecastDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Forecast.Enabled {
		t.Error("Forecast.Enabled should be true by default")
	}

	if cfg.Forecast.Mode != ForecastModeStandalone {
		t.Errorf("Forecast.Mode = %q, want %q",
			cfg.Forecast.Mode, ForecastModeStandalone)
	}
}

// TestDefaultConfig_ProfilesEmpty verifies profiles start empty.
func TestDefaultConfig_ProfilesEmpty(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Profiles) != 0 {
		t.Errorf("Profiles should be empty, got %d items", len(cfg.Profiles))
	}
}

// -----------------------------------------------------------------------------
// Constants Tests
// -----------------------------------------------------------------------------

// TestConstants verifies constant values are as expected.
func TestConstants(t *testing.T) {
	if DefaultEmbeddingModel != "nomic-embed-text-v2-moe" {
		t.Errorf("DefaultEmbeddingModel = %q, want %q",
			DefaultEmbeddingModel, "nomic-embed-text-v2-moe")
	}

	if DefaultLLMModel != "gpt-oss" {
		t.Errorf("DefaultLLMModel = %q, want %q", DefaultLLMModel, "gpt-oss")
	}

	if DefaultDiskLimitGB != 50 {
		t.Errorf("DefaultDiskLimitGB = %d, want %d", DefaultDiskLimitGB, 50)
	}

	if DefaultOllamaHostURL != "http://localhost:11434" {
		t.Errorf("DefaultOllamaHostURL = %q, want %q",
			DefaultOllamaHostURL, "http://localhost:11434")
	}

	if DefaultOllamaContainerURL != "http://host.containers.internal:11434" {
		t.Errorf("DefaultOllamaContainerURL = %q, want %q",
			DefaultOllamaContainerURL, "http://host.containers.internal:11434")
	}

	if CurrentConfigVersion != "1.0.0" {
		t.Errorf("CurrentConfigVersion = %q, want %q",
			CurrentConfigVersion, "1.0.0")
	}
}

// -----------------------------------------------------------------------------
// ProfileConfig Tests
// -----------------------------------------------------------------------------

// TestProfileConfig_Fields verifies profile field assignment.
func TestProfileConfig_Fields(t *testing.T) {
	profile := ProfileConfig{
		Name:          "test-profile",
		OllamaModel:   "llama3:70b",
		MaxTokens:     32768,
		RerankerModel: "cross-encoder/ms-marco-MiniLM-L-6-v2",
		MinRAM_MB:     65536,
	}

	if profile.Name != "test-profile" {
		t.Errorf("Name = %q, want %q", profile.Name, "test-profile")
	}

	if profile.OllamaModel != "llama3:70b" {
		t.Errorf("OllamaModel = %q, want %q", profile.OllamaModel, "llama3:70b")
	}

	if profile.MaxTokens != 32768 {
		t.Errorf("MaxTokens = %d, want %d", profile.MaxTokens, 32768)
	}

	if profile.MinRAM_MB != 65536 {
		t.Errorf("MinRAM_MB = %d, want %d", profile.MinRAM_MB, 65536)
	}
}

// -----------------------------------------------------------------------------
// Enterprise Config Validation Tests
// -----------------------------------------------------------------------------

// TestHMACConfig_ValidAlgorithms verifies valid HMAC algorithms are accepted.
func TestHMACConfig_ValidAlgorithms(t *testing.T) {
	validAlgorithms := []string{"sha256", "sha384", "sha512"}

	for _, algo := range validAlgorithms {
		t.Run(algo, func(t *testing.T) {
			cfg := HMACConfig{
				Enabled:   true,
				Algorithm: algo,
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() for algorithm %q returned error: %v", algo, err)
			}
		})
	}
}

// TestHMACConfig_InvalidAlgorithm verifies invalid HMAC algorithms are rejected.
func TestHMACConfig_InvalidAlgorithm(t *testing.T) {
	invalidAlgorithms := []string{"sha1", "md5", "sha128", "invalid", "SHA256"}

	for _, algo := range invalidAlgorithms {
		t.Run(algo, func(t *testing.T) {
			cfg := HMACConfig{
				Enabled:   true,
				Algorithm: algo,
			}
			err := cfg.Validate()
			if err == nil {
				t.Errorf("Validate() for algorithm %q should return error", algo)
			}
			// Verify it's a ValidationError
			if _, ok := err.(*ValidationError); !ok {
				t.Errorf("Expected ValidationError, got %T", err)
			}
		})
	}
}

// TestHMACConfig_DisabledSkipsValidation verifies disabled config is always valid.
func TestHMACConfig_DisabledSkipsValidation(t *testing.T) {
	cfg := HMACConfig{
		Enabled:   false,
		Algorithm: "invalid-algo",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should skip validation when disabled, got: %v", err)
	}
}

// TestHMACConfig_EmptyAlgorithmUsesDefault verifies empty algorithm is valid.
func TestHMACConfig_EmptyAlgorithmUsesDefault(t *testing.T) {
	cfg := HMACConfig{
		Enabled:   true,
		Algorithm: "", // Will use default sha256
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should accept empty algorithm (uses default), got: %v", err)
	}
}

// TestHSMConfig_ValidProviders verifies valid HSM providers are accepted.
func TestHSMConfig_ValidProviders(t *testing.T) {
	validProviders := []string{"pkcs11", "aws_cloudhsm", "azure_hsm", "thales_luna"}

	for _, provider := range validProviders {
		t.Run(provider, func(t *testing.T) {
			cfg := HSMConfig{
				Enabled:  true,
				Provider: provider,
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() for provider %q returned error: %v", provider, err)
			}
		})
	}
}

// TestHSMConfig_InvalidProvider verifies invalid HSM providers are rejected.
func TestHSMConfig_InvalidProvider(t *testing.T) {
	invalidProviders := []string{"random_provider", "hsm", "yubihsm", "Invalid"}

	for _, provider := range invalidProviders {
		t.Run(provider, func(t *testing.T) {
			cfg := HSMConfig{
				Enabled:  true,
				Provider: provider,
			}
			err := cfg.Validate()
			if err == nil {
				t.Errorf("Validate() for provider %q should return error", provider)
			}
			// Verify it's a ValidationError
			if _, ok := err.(*ValidationError); !ok {
				t.Errorf("Expected ValidationError, got %T", err)
			}
		})
	}
}

// TestHSMConfig_EmptyProviderWhenEnabled verifies empty provider is rejected.
func TestHSMConfig_EmptyProviderWhenEnabled(t *testing.T) {
	cfg := HSMConfig{
		Enabled:  true,
		Provider: "",
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should reject empty provider when enabled")
	}
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Errorf("Expected ValidationError, got %T", err)
	}
	if valErr.Field != "hsm.provider" {
		t.Errorf("Expected field 'hsm.provider', got %q", valErr.Field)
	}
}

// TestHSMConfig_DisabledSkipsValidation verifies disabled config is always valid.
func TestHSMConfig_DisabledSkipsValidation(t *testing.T) {
	cfg := HSMConfig{
		Enabled:  false,
		Provider: "invalid-provider",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should skip validation when disabled, got: %v", err)
	}
}

// TestTSAConfig_ValidProviders verifies valid TSA providers are accepted.
func TestTSAConfig_ValidProviders(t *testing.T) {
	validProviders := []string{"digicert", "globalsign", "sectigo", "freetsa"}

	for _, provider := range validProviders {
		t.Run(provider, func(t *testing.T) {
			cfg := TSAConfig{
				Enabled:  true,
				Provider: provider,
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() for provider %q returned error: %v", provider, err)
			}
		})
	}
}

// TestTSAConfig_CustomProviderRequiresURL verifies custom provider requires URL.
func TestTSAConfig_CustomProviderRequiresURL(t *testing.T) {
	// Custom without URL should fail
	cfg := TSAConfig{
		Enabled:  true,
		Provider: "custom",
		URL:      "",
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should reject custom provider without URL")
	}
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Errorf("Expected ValidationError, got %T", err)
	}
	if valErr.Field != "tsa.url" {
		t.Errorf("Expected field 'tsa.url', got %q", valErr.Field)
	}
}

// TestTSAConfig_CustomProviderWithURL verifies custom provider with URL is valid.
func TestTSAConfig_CustomProviderWithURL(t *testing.T) {
	cfg := TSAConfig{
		Enabled:  true,
		Provider: "custom",
		URL:      "https://tsa.example.com/timestamp",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() for custom provider with URL returned error: %v", err)
	}
}

// TestTSAConfig_InvalidProvider verifies invalid TSA providers are rejected.
func TestTSAConfig_InvalidProvider(t *testing.T) {
	invalidProviders := []string{"random_tsa", "verisign", "comodo", "Invalid"}

	for _, provider := range invalidProviders {
		t.Run(provider, func(t *testing.T) {
			cfg := TSAConfig{
				Enabled:  true,
				Provider: provider,
			}
			err := cfg.Validate()
			if err == nil {
				t.Errorf("Validate() for provider %q should return error", provider)
			}
		})
	}
}

// TestTSAConfig_EmptyProviderWhenEnabled verifies empty provider is rejected.
func TestTSAConfig_EmptyProviderWhenEnabled(t *testing.T) {
	cfg := TSAConfig{
		Enabled:  true,
		Provider: "",
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should reject empty provider when enabled")
	}
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Errorf("Expected ValidationError, got %T", err)
	}
	if valErr.Field != "tsa.provider" {
		t.Errorf("Expected field 'tsa.provider', got %q", valErr.Field)
	}
}

// TestTSAConfig_DisabledSkipsValidation verifies disabled config is always valid.
func TestTSAConfig_DisabledSkipsValidation(t *testing.T) {
	cfg := TSAConfig{
		Enabled:  false,
		Provider: "invalid-provider",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should skip validation when disabled, got: %v", err)
	}
}

// TestSignatureConfig_ValidAlgorithms verifies valid signature algorithms are accepted.
func TestSignatureConfig_ValidAlgorithms(t *testing.T) {
	validAlgorithms := []string{"rsa2048", "rsa4096", "ecdsa_p256", "ecdsa_p384", "ed25519"}

	for _, algo := range validAlgorithms {
		t.Run(algo, func(t *testing.T) {
			cfg := SignatureConfig{
				Enabled:   true,
				Algorithm: algo,
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("Validate() for algorithm %q returned error: %v", algo, err)
			}
		})
	}
}

// TestSignatureConfig_InvalidAlgorithm verifies invalid signature algorithms are rejected.
func TestSignatureConfig_InvalidAlgorithm(t *testing.T) {
	invalidAlgorithms := []string{"rsa1024", "dsa", "ecdsa_p521", "Invalid", "RSA2048"}

	for _, algo := range invalidAlgorithms {
		t.Run(algo, func(t *testing.T) {
			cfg := SignatureConfig{
				Enabled:   true,
				Algorithm: algo,
			}
			err := cfg.Validate()
			if err == nil {
				t.Errorf("Validate() for algorithm %q should return error", algo)
			}
			// Verify it's a ValidationError
			if _, ok := err.(*ValidationError); !ok {
				t.Errorf("Expected ValidationError, got %T", err)
			}
		})
	}
}

// TestSignatureConfig_DisabledSkipsValidation verifies disabled config is always valid.
func TestSignatureConfig_DisabledSkipsValidation(t *testing.T) {
	cfg := SignatureConfig{
		Enabled:   false,
		Algorithm: "invalid-algo",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should skip validation when disabled, got: %v", err)
	}
}

// TestSignatureConfig_EmptyAlgorithmUsesDefault verifies empty algorithm is valid.
func TestSignatureConfig_EmptyAlgorithmUsesDefault(t *testing.T) {
	cfg := SignatureConfig{
		Enabled:   true,
		Algorithm: "", // Will use default ecdsa_p256
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() should accept empty algorithm (uses default), got: %v", err)
	}
}

// TestValidationError_Error verifies error message formatting.
func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "test.field",
		Value:   "bad-value",
		Message: "is not valid",
	}

	expected := "config validation error: test.field (bad-value): is not valid"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

// TestValidationError_EmptyValue verifies error message with empty value.
func TestValidationError_EmptyValue(t *testing.T) {
	err := &ValidationError{
		Field:   "test.field",
		Value:   "",
		Message: "is required",
	}

	expected := "config validation error: test.field (): is required"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

// -----------------------------------------------------------------------------
// GetProfileForRAM Tests
// -----------------------------------------------------------------------------

// TestGetProfileForRAM verifies RAM-based profile selection.
func TestGetProfileForRAM(t *testing.T) {
	tests := []struct {
		name     string
		ramMB    int
		expected string
	}{
		{"very low RAM returns low", 4096, "low"},
		{"8GB returns low", 8192, "low"},
		{"16GB returns standard", 16384, "standard"},
		{"32GB returns performance", 32768, "performance"},
		{"64GB returns performance", 65536, "performance"},
		{"128GB returns ultra", 131072, "ultra"},
		{"zero RAM returns low", 0, "low"},
		{"negative RAM returns low", -1000, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetProfileForRAM(tt.ramMB)
			if result != tt.expected {
				t.Errorf("GetProfileForRAM(%d) = %q, want %q", tt.ramMB, result, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// SecretsConfig Tests
// -----------------------------------------------------------------------------

// TestSecretsConfig_GetTimeout verifies timeout default and custom values.
func TestSecretsConfig_GetTimeout(t *testing.T) {
	tests := []struct {
		name     string
		config   *SecretsConfig
		expected time.Duration
	}{
		{
			name:     "nil config returns default",
			config:   nil,
			expected: 10 * time.Second,
		},
		{
			name:     "zero timeout returns default",
			config:   &SecretsConfig{Timeout: 0},
			expected: 10 * time.Second,
		},
		{
			name:     "negative timeout returns default",
			config:   &SecretsConfig{Timeout: -5 * time.Second},
			expected: 10 * time.Second,
		},
		{
			name:     "positive timeout returns configured value",
			config:   &SecretsConfig{Timeout: 30 * time.Second},
			expected: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetTimeout()
			if result != tt.expected {
				t.Errorf("GetTimeout() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestSecretsConfig_GetOnePasswordVault verifies vault name default and custom values.
func TestSecretsConfig_GetOnePasswordVault(t *testing.T) {
	tests := []struct {
		name     string
		config   *SecretsConfig
		expected string
	}{
		{
			name:     "nil config returns default",
			config:   nil,
			expected: "Aleutian",
		},
		{
			name:     "empty vault returns default",
			config:   &SecretsConfig{OnePasswordVault: ""},
			expected: "Aleutian",
		},
		{
			name:     "custom vault returns configured value",
			config:   &SecretsConfig{OnePasswordVault: "MyVault"},
			expected: "MyVault",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetOnePasswordVault()
			if result != tt.expected {
				t.Errorf("GetOnePasswordVault() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestSecretsConfig_GetVaultPath verifies Vault path default and custom values.
func TestSecretsConfig_GetVaultPath(t *testing.T) {
	tests := []struct {
		name     string
		config   *SecretsConfig
		expected string
	}{
		{
			name:     "nil config returns default",
			config:   nil,
			expected: "secret/data/aleutian",
		},
		{
			name:     "empty path returns default",
			config:   &SecretsConfig{VaultPath: ""},
			expected: "secret/data/aleutian",
		},
		{
			name:     "custom path returns configured value",
			config:   &SecretsConfig{VaultPath: "secret/data/custom"},
			expected: "secret/data/custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetVaultPath()
			if result != tt.expected {
				t.Errorf("GetVaultPath() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// FallbackChain Tests
// -----------------------------------------------------------------------------

// TestFallbackChain_Models verifies model list generation.
func TestFallbackChain_Models(t *testing.T) {
	tests := []struct {
		name     string
		chain    *FallbackChain
		expected []string
	}{
		{
			name:     "nil chain returns nil",
			chain:    nil,
			expected: nil,
		},
		{
			name:     "empty primary returns nil",
			chain:    &FallbackChain{Primary: ""},
			expected: nil,
		},
		{
			name:     "primary only returns single model",
			chain:    &FallbackChain{Primary: "gpt-4"},
			expected: []string{"gpt-4"},
		},
		{
			name: "primary with fallbacks returns all",
			chain: &FallbackChain{
				Primary:   "gpt-4",
				Fallbacks: []string{"gpt-3.5", "llama3"},
			},
			expected: []string{"gpt-4", "gpt-3.5", "llama3"},
		},
		{
			name: "primary with empty fallbacks",
			chain: &FallbackChain{
				Primary:   "gpt-4",
				Fallbacks: []string{},
			},
			expected: []string{"gpt-4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.chain.Models()
			if len(result) != len(tt.expected) {
				t.Fatalf("Models() returned %d items, want %d", len(result), len(tt.expected))
			}
			for i, model := range result {
				if model != tt.expected[i] {
					t.Errorf("Models()[%d] = %q, want %q", i, model, tt.expected[i])
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Hardware Profile Tests
// -----------------------------------------------------------------------------

// TestBuiltInHardwareProfiles verifies built-in profiles exist.
func TestBuiltInHardwareProfiles(t *testing.T) {
	expectedProfiles := []string{"low", "standard", "performance", "ultra"}

	for _, name := range expectedProfiles {
		t.Run(name, func(t *testing.T) {
			profile, exists := BuiltInHardwareProfiles[name]
			if !exists {
				t.Fatalf("Profile %q not found", name)
			}
			if profile.Name != name {
				t.Errorf("Profile name = %q, want %q", profile.Name, name)
			}
		})
	}
}

// TestHardwareProfileOrder verifies profile order is defined.
func TestHardwareProfileOrder(t *testing.T) {
	if len(HardwareProfileOrder) == 0 {
		t.Error("HardwareProfileOrder should not be empty")
	}

	// Verify all profiles in order exist
	for _, name := range HardwareProfileOrder {
		if _, exists := BuiltInHardwareProfiles[name]; !exists {
			t.Errorf("Profile %q in order but not in BuiltInHardwareProfiles", name)
		}
	}
}

// =============================================================================
// GR-75: LSP Configuration Tests
// =============================================================================

// TestLSPConfig_Defaults verifies default LSP configuration values.
func TestLSPConfig_Defaults(t *testing.T) {
	cfg := DefaultLSPConfig()

	t.Run("disabled_by_default", func(t *testing.T) {
		if cfg.Enabled {
			t.Error("LSP should be disabled by default")
		}
	})

	t.Run("languages_enabled_by_default", func(t *testing.T) {
		if !cfg.PythonEnabled {
			t.Error("Python should be enabled by default")
		}
		if !cfg.TypeScriptEnabled {
			t.Error("TypeScript should be enabled by default")
		}
	})

	t.Run("timeout_defaults", func(t *testing.T) {
		if cfg.StartupTimeout != 30*time.Second {
			t.Errorf("StartupTimeout = %v, want 30s", cfg.StartupTimeout)
		}
		if cfg.RequestTimeout != 5*time.Second {
			t.Errorf("RequestTimeout = %v, want 5s", cfg.RequestTimeout)
		}
		if cfg.IdleTimeout != 10*time.Minute {
			t.Errorf("IdleTimeout = %v, want 10m", cfg.IdleTimeout)
		}
	})
}

// TestLSPConfig_FromEnv verifies environment variable parsing.
func TestLSPConfig_FromEnv(t *testing.T) {
	t.Run("all_enabled", func(t *testing.T) {
		t.Setenv("LSP_ENABLED", "true")
		t.Setenv("LSP_PYTHON_ENABLED", "true")
		t.Setenv("LSP_TYPESCRIPT_ENABLED", "true")
		t.Setenv("LSP_STARTUP_TIMEOUT_SECONDS", "60")
		t.Setenv("LSP_REQUEST_TIMEOUT_SECONDS", "10")

		cfg := LSPConfigFromEnv()

		if !cfg.Enabled {
			t.Error("LSP should be enabled")
		}
		if !cfg.PythonEnabled {
			t.Error("Python should be enabled")
		}
		if !cfg.TypeScriptEnabled {
			t.Error("TypeScript should be enabled")
		}
		if cfg.StartupTimeout != 60*time.Second {
			t.Errorf("StartupTimeout = %v, want 60s", cfg.StartupTimeout)
		}
		if cfg.RequestTimeout != 10*time.Second {
			t.Errorf("RequestTimeout = %v, want 10s", cfg.RequestTimeout)
		}
	})

	t.Run("python_disabled", func(t *testing.T) {
		t.Setenv("LSP_ENABLED", "true")
		t.Setenv("LSP_PYTHON_ENABLED", "false")
		t.Setenv("LSP_TYPESCRIPT_ENABLED", "true")

		cfg := LSPConfigFromEnv()

		if !cfg.Enabled {
			t.Error("LSP should be enabled")
		}
		if cfg.PythonEnabled {
			t.Error("Python should be disabled")
		}
		if !cfg.TypeScriptEnabled {
			t.Error("TypeScript should be enabled")
		}
	})

	t.Run("typescript_disabled", func(t *testing.T) {
		t.Setenv("LSP_ENABLED", "true")
		t.Setenv("LSP_PYTHON_ENABLED", "true")
		t.Setenv("LSP_TYPESCRIPT_ENABLED", "false")

		cfg := LSPConfigFromEnv()

		if cfg.TypeScriptEnabled {
			t.Error("TypeScript should be disabled")
		}
	})

	t.Run("disabled_by_default", func(t *testing.T) {
		// Clear all LSP env vars
		t.Setenv("LSP_ENABLED", "")
		t.Setenv("LSP_PYTHON_ENABLED", "")
		t.Setenv("LSP_TYPESCRIPT_ENABLED", "")

		cfg := LSPConfigFromEnv()

		if cfg.Enabled {
			t.Error("LSP should be disabled when LSP_ENABLED is empty")
		}
		// Languages should default to true even when LSP is disabled
		if !cfg.PythonEnabled {
			t.Error("Python should default to true")
		}
		if !cfg.TypeScriptEnabled {
			t.Error("TypeScript should default to true")
		}
	})

	t.Run("invalid_timeout_uses_default", func(t *testing.T) {
		t.Setenv("LSP_ENABLED", "true")
		t.Setenv("LSP_STARTUP_TIMEOUT_SECONDS", "not-a-number")
		t.Setenv("LSP_REQUEST_TIMEOUT_SECONDS", "-5")

		cfg := LSPConfigFromEnv()

		if cfg.StartupTimeout != 30*time.Second {
			t.Errorf("StartupTimeout = %v, want 30s (default on invalid)", cfg.StartupTimeout)
		}
		if cfg.RequestTimeout != 5*time.Second {
			t.Errorf("RequestTimeout = %v, want 5s (default on negative)", cfg.RequestTimeout)
		}
	})
}

// TestLSPServerVerifier_AllAvailable verifies when all binaries are found.
func TestLSPServerVerifier_AllAvailable(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		PythonEnabled:     true,
		TypeScriptEnabled: true,
	}
	v := NewLSPServerVerifier(cfg)
	v.lookPath = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	result := v.Verify()

	if !result.PythonAvailable {
		t.Error("Python should be available")
	}
	if !result.TypeScriptAvailable {
		t.Error("TypeScript should be available")
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Expected 0 warnings, got %d: %v", len(result.Warnings), result.Warnings)
	}
}

// TestLSPServerVerifier_PythonMissing verifies Python disabled but TypeScript still works.
func TestLSPServerVerifier_PythonMissing(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		PythonEnabled:     true,
		TypeScriptEnabled: true,
	}
	v := NewLSPServerVerifier(cfg)
	v.lookPath = func(file string) (string, error) {
		if file == "pyright-langserver" {
			return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
		}
		return "/usr/local/bin/" + file, nil
	}

	result := v.Verify()

	if result.PythonAvailable {
		t.Error("Python should not be available")
	}
	if !result.TypeScriptAvailable {
		t.Error("TypeScript should still be available")
	}
	if len(result.Warnings) != 1 {
		t.Errorf("Expected 1 warning, got %d", len(result.Warnings))
	}
}

// TestLSPServerVerifier_AllMissing verifies graceful degradation.
func TestLSPServerVerifier_AllMissing(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		PythonEnabled:     true,
		TypeScriptEnabled: true,
	}
	v := NewLSPServerVerifier(cfg)
	v.lookPath = func(file string) (string, error) {
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	result := v.Verify()

	if result.PythonAvailable {
		t.Error("Python should not be available")
	}
	if result.TypeScriptAvailable {
		t.Error("TypeScript should not be available")
	}
	if len(result.Warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d", len(result.Warnings))
	}
}

// TestLSPServerVerifier_LanguageDisabled verifies disabled languages are not checked.
func TestLSPServerVerifier_LanguageDisabled(t *testing.T) {
	cfg := LSPConfig{
		Enabled:           true,
		PythonEnabled:     false,
		TypeScriptEnabled: false,
	}
	v := NewLSPServerVerifier(cfg)
	lookPathCalled := false
	v.lookPath = func(file string) (string, error) {
		lookPathCalled = true
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}

	result := v.Verify()

	if lookPathCalled {
		t.Error("lookPath should not be called for disabled languages")
	}
	if result.PythonAvailable {
		t.Error("Python should not be available (disabled)")
	}
	if result.TypeScriptAvailable {
		t.Error("TypeScript should not be available (disabled)")
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Expected 0 warnings for disabled languages, got %d", len(result.Warnings))
	}
}
