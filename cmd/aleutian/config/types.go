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
Package config provides configuration types and loading for the Aleutian CLI.

# Overview

This package defines the configuration schema for Aleutian, including:
  - Machine configuration for Podman VM
  - Model backend settings (Ollama, OpenAI, Anthropic)
  - Feature toggles and extensions
  - Forecast module configuration
  - Custom optimization profiles

# Configuration File

The configuration is stored at ~/.aleutian/aleutian.yaml and is created
automatically on first run with sensible defaults.

# Example

	model_backend:
	  type: ollama
	  ollama:
	    embedding_model: nomic-embed-text-v2-moe
	    disk_limit_gb: 50
*/
package config

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

const (
	// DefaultEmbeddingModel is the default model for document embeddings.
	DefaultEmbeddingModel = "nomic-embed-text-v2-moe"

	// DefaultLLMModel is the default model for chat/generation.
	DefaultLLMModel = "gpt-oss"

	// DefaultDiskLimitGB is the default maximum disk space for models.
	DefaultDiskLimitGB = 50

	// DefaultOllamaHostURL is the Ollama URL when accessed from the host.
	DefaultOllamaHostURL = "http://localhost:11434"

	// DefaultOllamaContainerURL is the Ollama URL when accessed from containers.
	DefaultOllamaContainerURL = "http://host.containers.internal:11434"
)

// -----------------------------------------------------------------------------
// Hardware Profile Definitions (Single Source of Truth)
// -----------------------------------------------------------------------------

// HardwareProfile defines a complete hardware-to-model mapping.
//
// # Description
//
// This struct captures all settings for a given hardware tier.
// All model selections and thresholds are defined here to avoid
// magic strings scattered across the codebase.
//
// # Fields
//
//   - Name: Profile identifier (low, standard, performance, ultra)
//   - Description: Human-readable description
//   - OllamaModel: The LLM model to use
//   - MaxTokens: Context window size
//   - RerankerModel: Model for RAG reranking
//   - WeaviateQueryLimit: Default vector search limit
//   - RerankFinalK: Number of results after reranking
//   - MinRAM_MB: Minimum RAM threshold (inclusive)
//   - MaxRAM_MB: Maximum RAM threshold (exclusive, 0 = unlimited)
type HardwareProfile struct {
	Name               string
	Description        string
	OllamaModel        string
	MaxTokens          int
	RerankerModel      string
	WeaviateQueryLimit int
	RerankFinalK       int
	MinRAM_MB          int
	MaxRAM_MB          int
}

// BuiltInHardwareProfiles defines the default hardware-to-model mappings.
//
// # Description
//
// These profiles are the SINGLE SOURCE OF TRUTH for model selection.
// Any code that needs model names or RAM thresholds should reference
// these profiles rather than hardcoding strings.
//
// # Profiles
//
//   - Low (< 16GB): gemma3:4b - Basic local inference
//   - Standard (16-32GB): qwen3:14b - Balanced performance
//   - Performance (32-128GB): gpt-oss:20b - Enhanced context with thinking
//   - Ultra (128GB+): gpt-oss:120b - Enterprise grade with thinking
//
// # Example
//
//	profile := config.BuiltInHardwareProfiles["standard"]
//	fmt.Println(profile.OllamaModel) // "qwen3:14b"
var BuiltInHardwareProfiles = map[string]HardwareProfile{
	"low": {
		Name:               "low",
		Description:        "Low (< 16GB) - Basic local inference",
		OllamaModel:        "gemma3:4b",
		MaxTokens:          2048,
		RerankerModel:      "cross-encoder/ms-marco-TinyBERT-L-2-v2",
		WeaviateQueryLimit: 5,
		RerankFinalK:       5,
		MinRAM_MB:          0,
		MaxRAM_MB:          16384,
	},
	"standard": {
		Name:               "standard",
		Description:        "Standard (16-32GB) - Balanced performance",
		OllamaModel:        "qwen3:14b",
		MaxTokens:          4096,
		RerankerModel:      "cross-encoder/ms-marco-MiniLM-L-6-v2",
		WeaviateQueryLimit: 5,
		RerankFinalK:       5,
		MinRAM_MB:          16384,
		MaxRAM_MB:          32768,
	},
	"performance": {
		Name:               "performance",
		Description:        "Performance (32-128GB) - Enhanced context with thinking",
		OllamaModel:        "gpt-oss:20b",
		MaxTokens:          8192,
		RerankerModel:      "cross-encoder/ms-marco-MiniLM-L-6-v2",
		WeaviateQueryLimit: 5,
		RerankFinalK:       10,
		MinRAM_MB:          32768,
		MaxRAM_MB:          131072,
	},
	"ultra": {
		Name:               "ultra",
		Description:        "Ultra (128GB+) - Enterprise grade with thinking",
		OllamaModel:        "gpt-oss:120b",
		MaxTokens:          32768,
		RerankerModel:      "cross-encoder/ms-marco-MiniLM-L-6-v2",
		WeaviateQueryLimit: 5,
		RerankFinalK:       10,
		MinRAM_MB:          131072,
		MaxRAM_MB:          0, // Unlimited
	},
}

// HardwareProfileOrder defines the order for RAM-based profile selection.
// Profiles are checked in this order (highest RAM first).
var HardwareProfileOrder = []string{"ultra", "performance", "standard", "low"}

// GetProfileForRAM returns the appropriate profile name for a given RAM amount.
//
// # Description
//
// Selects the best profile based on available system memory.
// Profiles are checked from highest to lowest RAM requirements.
//
// # Inputs
//
//   - ramMB: Available RAM in megabytes
//
// # Outputs
//
//   - string: Profile name (low, standard, performance, ultra)
//
// # Example
//
//	profile := config.GetProfileForRAM(65536) // returns "performance"
func GetProfileForRAM(ramMB int) string {
	for _, name := range HardwareProfileOrder {
		profile := BuiltInHardwareProfiles[name]
		if ramMB >= profile.MinRAM_MB && (profile.MaxRAM_MB == 0 || ramMB < profile.MaxRAM_MB) {
			return name
		}
	}
	return "low" // fallback
}

// -----------------------------------------------------------------------------
// Enums
// -----------------------------------------------------------------------------

// ForecastMode defines how the forecast service is deployed.
//
// # Description
//
// ForecastMode determines whether Aleutian runs its own forecast service
// or connects to an external Sapheneia instance.
//
// # Values
//
//   - ForecastModeStandalone: Runs Aleutian's containerized forecast service
//   - ForecastModeSapheneia: Connects to external Sapheneia containers
type ForecastMode string

const (
	// ForecastModeStandalone runs Aleutian's own forecast service.
	ForecastModeStandalone ForecastMode = "standalone"

	// ForecastModeSapheneia connects to external Sapheneia containers.
	ForecastModeSapheneia ForecastMode = "sapheneia"
)

// IsValid checks if the mode is a known value.
//
// # Description
//
// Returns true if the ForecastMode is one of the defined constants.
//
// # Outputs
//
//   - bool: True if valid, false otherwise
func (m ForecastMode) IsValid() bool {
	switch m {
	case ForecastModeStandalone, ForecastModeSapheneia:
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Primary Configuration Types
// -----------------------------------------------------------------------------

// AleutianConfig is the root configuration structure for the Aleutian CLI.
//
// # Description
//
// Contains all configuration sections for the Aleutian system, including
// infrastructure, model backend, features, and optional modules.
//
// # Fields
//
//   - Machine: Podman machine configuration (macOS only)
//   - Extensions: Paths to custom compose files
//   - Secrets: Secret storage configuration
//   - Features: Feature toggle flags
//   - ModelBackend: LLM backend configuration
//   - Forecast: Timeseries forecasting module
//   - Profiles: Custom optimization profiles
//
// # Example
//
//	cfg := config.DefaultConfig()
//	cfg.ModelBackend.Type = "ollama"
//	cfg.ModelBackend.Ollama.EmbeddingModel = "nomic-embed-text-v2-moe"
type AleutianConfig struct {
	// Meta contains versioning and audit information.
	// Required for compliance with GDPR, HIPAA, and CCPA.
	Meta ConfigMeta `yaml:"meta"`

	// Machine configures the Podman virtual machine (macOS only).
	Machine MachineConfig `yaml:"machine"`

	// Extensions lists paths to custom podman-compose files.
	Extensions []string `yaml:"extensions"`

	// Secrets configures secret storage (env vars or keychain).
	Secrets SecretsConfig `yaml:"secrets"`

	// Features toggles optional system services.
	Features FeatureConfig `yaml:"features"`

	// ModelBackend configures the LLM backend (ollama, openai, etc.).
	ModelBackend BackendConfig `yaml:"model_backend"`

	// Forecast configures the optional timeseries forecast module.
	Forecast ForecastConfig `yaml:"forecast"`

	// ModelManagement configures model downloads, verification, and governance.
	// Includes version pinning, auto-selection, fallback chains, and audit logging.
	ModelManagement ModelManagementConfig `yaml:"model_management"`

	// SessionIntegrity configures conversation integrity verification.
	// Includes hash chain verification and enterprise compliance features.
	SessionIntegrity SessionIntegrityConfig `yaml:"session_integrity"`

	// Profiles defines custom optimization profiles.
	// These extend the built-in profiles (low, standard, performance, ultra).
	Profiles []ProfileConfig `yaml:"profiles,omitempty"`

	// Observability configures the observability stack (Prometheus, Grafana).
	// Enabled by default; set enabled: false to disable.
	Observability ObservabilityConfig `yaml:"observability"`
}

// -----------------------------------------------------------------------------
// Infrastructure Configuration
// -----------------------------------------------------------------------------

// MachineConfig configures the Podman virtual machine.
//
// # Description
//
// On macOS, containers run inside a Linux VM managed by Podman.
// This configuration controls the VM's resources and mount points.
//
// # Fields
//
//   - Id: Machine name (default: "podman-machine-default")
//   - CPUCount: Number of CPU cores allocated
//   - MemoryAmount: RAM in MB allocated to the VM
//   - Drives: Host paths to mount into the VM
//
// # Limitations
//
// This configuration is only used on macOS. On Linux, containers
// run natively without a VM.
type MachineConfig struct {
	// Id is the Podman machine name.
	Id string `yaml:"id"`

	// CPUCount is the number of CPU cores for the VM.
	CPUCount int `yaml:"cpu_count"`

	// MemoryAmount is the RAM allocation in MB.
	MemoryAmount int `yaml:"memory_amount"`

	// Drives lists host paths to mount into the VM.
	Drives []string `yaml:"drives"`
}

// SecretsConfig configures how secrets are stored and accessed.
//
// # Description
//
// Configures secret management with support for multiple backends.
// Backends are tried in priority order until a secret is found.
//
// # Backend Priority Order
//
//  1. HashiCorp Vault (if VaultAddress is set) - Enterprise/production
//  2. 1Password CLI (if Use1Password is true) - Cross-platform, recommended
//  3. macOS Keychain (if UseKeychain is true) - macOS only, built-in
//  4. Linux libsecret (if UseLibsecret is true) - Linux only, GNOME/KDE
//  5. Environment variables (if UseEnv is true) - Fallback, CI/Docker
//
// # Auto-Detection
//
// By default, backends are auto-detected based on platform and available
// CLI tools. Explicit configuration overrides auto-detection.
//
// # Example YAML
//
//	secrets:
//	  use_env: true            # Fallback for CI/Docker
//	  use_keychain: true       # macOS (auto-detected)
//	  use_1password: true      # Cross-platform (auto-detected if `op` in PATH)
//	  onepassword_vault: "Aleutian"
//	  timeout: 10s
//	  required:
//	    - ANTHROPIC_API_KEY
//
// # Limitations
//
// Vault support is planned for Phase 6B and is not yet implemented.
type SecretsConfig struct {
	// UseEnv enables reading secrets from environment variables.
	// This is the fallback backend, always checked last.
	// Recommended: true (for CI/CD and Docker compatibility).
	UseEnv bool `yaml:"use_env"`

	// UseKeychain enables reading secrets from macOS Keychain.
	// Ignored on non-macOS platforms.
	// The Keychain is accessed via: security find-generic-password
	// Recommended: true on macOS (auto-detected).
	UseKeychain bool `yaml:"use_keychain,omitempty"`

	// Use1Password enables reading secrets from 1Password CLI.
	// Requires: 1Password CLI (`op`) installed and authenticated.
	// Works on: macOS, Linux, Windows.
	// Access pattern: op read "op://Vault/ItemName/password"
	// Recommended: true for teams using 1Password (auto-detected if `op` in PATH).
	Use1Password bool `yaml:"use_1password,omitempty"`

	// OnePasswordVault is the 1Password vault name for Aleutian secrets.
	// Default: "Aleutian"
	OnePasswordVault string `yaml:"onepassword_vault,omitempty"`

	// UseLibsecret enables reading secrets from Linux Secret Service.
	// Works with GNOME Keyring, KDE Wallet, and other providers.
	// Requires: libsecret installed, secret-tool CLI available.
	// Access pattern: secret-tool lookup service aleutian key SECRET_NAME
	// Recommended: true on Linux desktops (auto-detected if `secret-tool` in PATH).
	UseLibsecret bool `yaml:"use_libsecret,omitempty"`

	// VaultAddress is the HashiCorp Vault server address.
	// If set, enables Vault backend (highest priority).
	// Example: "https://vault.example.com:8200"
	// NOTE: Vault support is planned for Phase 6B.
	VaultAddress string `yaml:"vault_address,omitempty"`

	// VaultPath is the path prefix for secrets in Vault.
	// Default: "secret/data/aleutian"
	VaultPath string `yaml:"vault_path,omitempty"`

	// Timeout is the maximum time to wait for CLI backends.
	// Default: 10 seconds (allows time for biometric prompts).
	// Set to 0 to use the default timeout.
	Timeout time.Duration `yaml:"timeout,omitempty"`

	// Required lists secrets that must be present for startup.
	// If any are missing, initialization fails with clear error.
	Required []string `yaml:"required,omitempty"`

	// Redact lists additional secret names to redact from logs.
	// Well-known secrets (ANTHROPIC_API_KEY, etc.) are always redacted.
	Redact []string `yaml:"redact,omitempty"`
}

// GetTimeout returns the configured timeout or the default (10 seconds).
//
// # Description
//
// Returns the timeout duration for CLI backend operations.
// Uses 10 seconds as default to allow time for biometric prompts.
//
// # Outputs
//
//   - time.Duration: The timeout duration
func (c *SecretsConfig) GetTimeout() time.Duration {
	if c == nil || c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

// GetOnePasswordVault returns the 1Password vault name or default.
//
// # Description
//
// Returns the vault name for 1Password lookups.
// Uses "Aleutian" as the default vault name.
//
// # Outputs
//
//   - string: The vault name
func (c *SecretsConfig) GetOnePasswordVault() string {
	if c == nil || c.OnePasswordVault == "" {
		return "Aleutian"
	}
	return c.OnePasswordVault
}

// GetVaultPath returns the Vault path prefix or default.
//
// # Description
//
// Returns the path prefix for HashiCorp Vault secret lookups.
// Uses "secret/data/aleutian" as the default path.
//
// # Outputs
//
//   - string: The Vault path prefix
func (c *SecretsConfig) GetVaultPath() string {
	if c == nil || c.VaultPath == "" {
		return "secret/data/aleutian"
	}
	return c.VaultPath
}

// FeatureConfig toggles optional system features.
//
// # Description
//
// Controls which optional services are enabled in the stack.
type FeatureConfig struct {
	// Observability enables metrics, tracing, and logging services.
	Observability bool `yaml:"observability"`

	// RagEngine enables the RAG (Retrieval-Augmented Generation) pipeline.
	RagEngine bool `yaml:"rag_engine"`
}

// -----------------------------------------------------------------------------
// Model Backend Configuration
// -----------------------------------------------------------------------------

// BackendConfig configures the LLM backend.
//
// # Description
//
// Determines which LLM provider is used and its configuration.
// Supports local (Ollama) and cloud (OpenAI, Anthropic) backends.
//
// # Fields
//
//   - Type: Backend type ("ollama", "openai", "anthropic", "remote_tgi")
//   - BaseURL: API endpoint for cloud backends
//   - Ollama: Ollama-specific configuration (when Type is "ollama")
//
// # Example
//
//	backend := BackendConfig{
//	    Type: "ollama",
//	    Ollama: OllamaConfig{
//	        EmbeddingModel: "nomic-embed-text-v2-moe",
//	        DiskLimitGB:    50,
//	    },
//	}
type BackendConfig struct {
	// Type specifies the backend: "ollama", "openai", "anthropic", "remote_tgi".
	Type string `yaml:"type"`

	// BaseURL is the API endpoint for cloud backends.
	BaseURL string `yaml:"base_url,omitempty"`

	// Ollama contains Ollama-specific settings.
	// Only used when Type is "ollama".
	Ollama OllamaConfig `yaml:"ollama,omitempty"`
}

// OllamaConfig configures the Ollama model backend.
//
// # Description
//
// Contains settings specific to running Ollama locally, including
// which models to use for embeddings and LLM, and resource limits.
//
// # Fields
//
//   - EmbeddingModel: Model for document embeddings (default: nomic-embed-text-v2-moe)
//   - LLMModel: Model for chat/generation (default: determined by profile)
//   - DiskLimitGB: Maximum disk space for models (default: 50)
//   - BaseURL: Ollama API endpoint (default: http://localhost:11434)
//
// # Example
//
//	ollama := OllamaConfig{
//	    EmbeddingModel: "nomic-embed-text-v2-moe",
//	    LLMModel:       "gpt-oss:7b",
//	    DiskLimitGB:    100,
//	}
type OllamaConfig struct {
	// EmbeddingModel is the model used for document embeddings.
	// Default: "nomic-embed-text-v2-moe"
	EmbeddingModel string `yaml:"embedding_model,omitempty"`

	// LLMModel is the model used for chat/generation.
	// If empty, the model is determined by the optimization profile.
	LLMModel string `yaml:"llm_model,omitempty"`

	// ToolRouterModel is the model for trace agent tool routing.
	// Default: "granite4:micro-h"
	ToolRouterModel string `yaml:"tool_router_model,omitempty"`

	// ParamExtractorModel is the model for trace agent parameter extraction.
	// Default: "ministral-3:3b"
	ParamExtractorModel string `yaml:"param_extractor_model,omitempty"`

	// DiskLimitGB is the maximum disk space (GB) for storing models.
	// Default: 50
	DiskLimitGB int64 `yaml:"disk_limit_gb,omitempty"`

	// BaseURL is the Ollama API endpoint.
	// Default: "http://localhost:11434" for host access.
	BaseURL string `yaml:"base_url,omitempty"`
}

// GetEmbeddingModel returns the configured embedding model or the default.
//
// # Description
//
// Returns the embedding model from configuration, falling back to
// DefaultEmbeddingModel if not configured.
//
// # Outputs
//
//   - string: The embedding model name
func (c *OllamaConfig) GetEmbeddingModel() string {
	if c.EmbeddingModel == "" {
		return DefaultEmbeddingModel
	}
	return c.EmbeddingModel
}

// GetDiskLimitGB returns the configured disk limit or the default.
//
// # Description
//
// Returns the disk limit from configuration, falling back to
// DefaultDiskLimitGB if not configured or zero.
//
// # Outputs
//
//   - int64: The disk limit in gigabytes
func (c *OllamaConfig) GetDiskLimitGB() int64 {
	if c.DiskLimitGB <= 0 {
		return DefaultDiskLimitGB
	}
	return c.DiskLimitGB
}

// GetBaseURL returns the configured base URL or the default.
//
// # Description
//
// Returns the Ollama API URL from configuration, falling back to
// DefaultOllamaHostURL if not configured.
//
// # Outputs
//
//   - string: The Ollama API base URL
func (c *OllamaConfig) GetBaseURL() string {
	if c.BaseURL == "" {
		return DefaultOllamaHostURL
	}
	return c.BaseURL
}

// -----------------------------------------------------------------------------
// Model Management Configuration
// -----------------------------------------------------------------------------

// ModelManagementConfig contains model storage and verification settings.
//
// # Description
//
// Configuration for controlling model downloads, storage limits,
// verification behavior, and advanced features like version pinning,
// auto-selection, and fallback chains.
//
// # YAML Example
//
//	model_management:
//	  disk_limit_gb: 100
//	  verify_on_start: true
//	  allowed_models:
//	    - nomic-embed-text-v2-moe
//	    - llama3:8b
//	  version_pinning:
//	    enabled: true
//	  fallback_chains:
//	    llm:
//	      primary: "llama3:8b"
//	      fallbacks: ["phi3:mini", "tinyllama"]
type ModelManagementConfig struct {
	// DiskLimitGB is maximum disk space for model storage.
	// Set to 0 for no limit.
	// Default: 50
	DiskLimitGB int64 `yaml:"disk_limit_gb"`

	// AllowedModels restricts which models can be downloaded.
	// Empty slice means all models are allowed.
	// Enterprise feature for governance.
	AllowedModels []string `yaml:"allowed_models,omitempty"`

	// VerifyOnStart controls whether models are checked on stack start.
	// Default: true
	VerifyOnStart bool `yaml:"verify_on_start"`

	// OfflineModeAllowed permits operation without network.
	// When true, missing models generate warnings not errors.
	// Default: true
	OfflineModeAllowed bool `yaml:"offline_mode_allowed"`

	// VersionPinning configures model version locking.
	VersionPinning VersionPinningConfig `yaml:"version_pinning,omitempty"`

	// Integrity configures model integrity verification.
	Integrity IntegrityConfig `yaml:"integrity,omitempty"`

	// Parallel configures parallel download behavior.
	Parallel ParallelDownloadConfig `yaml:"parallel,omitempty"`

	// AutoSelection configures automatic model selection.
	AutoSelection AutoSelectionConfig `yaml:"auto_selection,omitempty"`

	// FallbackChains defines ordered fallback lists for model categories.
	FallbackChains map[string]FallbackChain `yaml:"fallback_chains,omitempty"`

	// SizeEstimation configures download size warnings.
	SizeEstimation SizeEstimationConfig `yaml:"size_estimation,omitempty"`

	// Audit configures compliance audit logging.
	Audit AuditLoggingConfig `yaml:"audit,omitempty"`
}

// VersionPinningConfig configures model version locking.
//
// # Description
//
// Enables locking models to specific SHA-256 digests for reproducible
// deployments and security compliance.
type VersionPinningConfig struct {
	// Enabled toggles version pinning.
	// Default: false
	Enabled bool `yaml:"enabled"`

	// Models maps model categories to pinned versions.
	// Keys: "embedding", "llm", etc.
	Models map[string]PinnedModel `yaml:"models,omitempty"`
}

// PinnedModel represents a model locked to a specific version.
type PinnedModel struct {
	// Name is the model identifier.
	Name string `yaml:"name"`

	// Digest is the required SHA-256 hash.
	// If set, download will fail if digest doesn't match.
	// Format: "sha256:abc123..."
	Digest string `yaml:"digest,omitempty"`

	// AllowUpgrade permits newer versions if pinned version unavailable.
	AllowUpgrade bool `yaml:"allow_upgrade"`
}

// IntegrityConfig configures model integrity verification.
type IntegrityConfig struct {
	// VerifyAfterDownload enables SHA-256 verification after pull.
	// Default: true
	VerifyAfterDownload bool `yaml:"verify_after_download"`

	// VerifyOnStartup enables verification on stack start.
	// Default: true
	VerifyOnStartup bool `yaml:"verify_on_startup"`

	// FailOnMismatch fails startup if verification fails.
	// Default: true
	FailOnMismatch bool `yaml:"fail_on_mismatch"`
}

// ParallelDownloadConfig configures concurrent model downloads.
type ParallelDownloadConfig struct {
	// Enabled toggles parallel downloads.
	// Default: true
	Enabled bool `yaml:"enabled"`

	// MaxConcurrent limits simultaneous downloads.
	// Default: 3
	MaxConcurrent int `yaml:"max_concurrent"`

	// BandwidthLimitMbps limits download speed (0 = unlimited).
	// Default: 0
	BandwidthLimitMbps int `yaml:"bandwidth_limit_mbps"`
}

// AutoSelectionConfig configures automatic model selection based on hardware.
type AutoSelectionConfig struct {
	// Enabled toggles auto-selection.
	// Default: true
	Enabled bool `yaml:"enabled"`

	// PreferQuantized favors quantized models over full precision.
	// Default: true
	PreferQuantized bool `yaml:"prefer_quantized"`

	// MinContextWindow requires minimum context window size.
	// Default: 4096
	MinContextWindow int `yaml:"min_context_window"`

	// ExplicitLLM overrides auto-selection for LLM.
	ExplicitLLM string `yaml:"explicit_llm,omitempty"`

	// ExplicitEmbedding overrides auto-selection for embeddings.
	ExplicitEmbedding string `yaml:"explicit_embedding,omitempty"`
}

// FallbackChain defines an ordered list of models to try.
type FallbackChain struct {
	// Primary is the preferred model.
	Primary string `yaml:"primary"`

	// Fallbacks are tried in order if primary fails.
	Fallbacks []string `yaml:"fallbacks,omitempty"`
}

// Models returns all models in the chain (primary + fallbacks).
func (c *FallbackChain) Models() []string {
	if c == nil || c.Primary == "" {
		return nil
	}
	result := make([]string, 0, 1+len(c.Fallbacks))
	result = append(result, c.Primary)
	result = append(result, c.Fallbacks...)
	return result
}

// SizeEstimationConfig configures download size warnings.
type SizeEstimationConfig struct {
	// Enabled toggles size estimation.
	// Default: true
	Enabled bool `yaml:"enabled"`

	// WarnThresholdGB warns before large downloads.
	// Default: 10
	WarnThresholdGB int `yaml:"warn_threshold_gb"`

	// RequireConfirmationGB requires user confirmation for huge downloads.
	// Default: 50
	RequireConfirmationGB int `yaml:"require_confirmation_gb"`
}

// AuditLoggingConfig configures compliance audit logging.
type AuditLoggingConfig struct {
	// Enabled toggles audit logging.
	// Default: true
	Enabled bool `yaml:"enabled"`

	// LogPulls records model download attempts.
	// Default: true
	LogPulls bool `yaml:"log_pulls"`

	// LogVerifications records integrity checks.
	// Default: true
	LogVerifications bool `yaml:"log_verifications"`

	// LogBlocks records blocked model requests.
	// Default: true
	LogBlocks bool `yaml:"log_blocks"`

	// IncludeHostname adds hostname to audit events.
	// Default: true
	IncludeHostname bool `yaml:"include_hostname"`

	// IncludeUser adds username to audit events.
	// Default: true
	IncludeUser bool `yaml:"include_user"`
}

// -----------------------------------------------------------------------------
// Session Integrity & Verification Configuration
// -----------------------------------------------------------------------------

// SessionIntegrityConfig configures conversation integrity verification.
//
// # Description
//
// Controls the hash chain verification features for chat sessions.
// The hash chain provides tamper-evident logging similar to blockchain.
//
// # YAML Example
//
//	session_integrity:
//	  enabled: true
//	  verification_mode: full
//	  enterprise:
//	    hmac:
//	      enabled: true
//	      key_provider: vault
//	    tsa:
//	      enabled: true
//	      provider: digicert
//	    hsm:
//	      enabled: false
//	    audit:
//	      enabled: true
//	      siem_endpoint: "https://siem.example.com/events"
type SessionIntegrityConfig struct {
	// Enabled toggles session integrity verification.
	// Default: true
	Enabled bool `yaml:"enabled"`

	// VerificationMode controls verification depth.
	// Options: "quick" (PrevHash links only), "full" (recompute all hashes)
	// Default: "quick"
	VerificationMode string `yaml:"verification_mode"`

	// AutoVerifyOnEnd automatically verifies chain when session ends.
	// Default: true
	AutoVerifyOnEnd bool `yaml:"auto_verify_on_end"`

	// ShowHashInSummary displays integrity info in session summary.
	// Default: true
	ShowHashInSummary bool `yaml:"show_hash_in_summary"`

	// Enterprise contains enterprise extension configuration.
	// These features require enterprise license.
	Enterprise EnterpriseIntegrityConfig `yaml:"enterprise,omitempty"`
}

// EnterpriseIntegrityConfig contains enterprise-only integrity features.
//
// # Description
//
// Configuration for enterprise extensions including HMAC, digital signatures,
// trusted timestamping, HSM integration, and compliance audit logging.
//
// # Thread Safety
//
// Configuration is read-only after initialization.
type EnterpriseIntegrityConfig struct {
	// HMAC configures keyed hash verification.
	HMAC HMACConfig `yaml:"hmac,omitempty"`

	// Signatures configures digital signature verification.
	Signatures SignatureConfig `yaml:"signatures,omitempty"`

	// TSA configures RFC 3161 trusted timestamping.
	TSA TSAConfig `yaml:"tsa,omitempty"`

	// HSM configures hardware security module integration.
	HSM HSMConfig `yaml:"hsm,omitempty"`

	// Audit configures compliance audit logging.
	Audit VerificationAuditConfig `yaml:"audit,omitempty"`

	// Authorization configures access control for verification.
	Authorization AuthorizationConfig `yaml:"authorization,omitempty"`

	// RateLimiting configures request rate limits.
	RateLimiting RateLimitConfig `yaml:"rate_limiting,omitempty"`

	// Caching configures verification result caching.
	Caching VerificationCacheConfig `yaml:"caching,omitempty"`

	// Scheduling configures periodic verification.
	Scheduling VerificationScheduleConfig `yaml:"scheduling,omitempty"`

	// Alerting configures failure alerts.
	Alerting VerificationAlertConfig `yaml:"alerting,omitempty"`
}

// HMACConfig configures HMAC-based keyed verification.
//
// # Description
//
// HMAC provides authentication in addition to integrity, proving
// who created the hash. Required for multi-tenant security.
type HMACConfig struct {
	// Enabled toggles HMAC verification.
	Enabled bool `yaml:"enabled"`

	// KeyProvider specifies where keys are stored.
	// Options: "vault", "aws_kms", "azure_keyvault", "gcp_kms", "env"
	KeyProvider string `yaml:"key_provider"`

	// KeyID is the identifier for the current active key.
	KeyID string `yaml:"key_id,omitempty"`

	// VaultPath is the path to keys in HashiCorp Vault.
	VaultPath string `yaml:"vault_path,omitempty"`

	// Algorithm specifies the HMAC algorithm.
	// Options: "sha256", "sha384", "sha512"
	// Default: "sha256"
	Algorithm string `yaml:"algorithm,omitempty"`
}

// SignatureConfig configures digital signature verification.
//
// # Description
//
// Digital signatures provide legal non-repudiation for compliance
// with eIDAS, ESIGN Act, and 21 CFR Part 11.
type SignatureConfig struct {
	// Enabled toggles signature verification.
	Enabled bool `yaml:"enabled"`

	// Algorithm specifies the signature algorithm.
	// Options: "rsa2048", "rsa4096", "ecdsa_p256", "ecdsa_p384", "ed25519"
	// Default: "ecdsa_p256"
	Algorithm string `yaml:"algorithm,omitempty"`

	// CertStorePath is the path to certificate store.
	CertStorePath string `yaml:"cert_store_path,omitempty"`

	// OCSPEnabled toggles certificate revocation checking.
	OCSPEnabled bool `yaml:"ocsp_enabled"`
}

// TSAConfig configures RFC 3161 trusted timestamping.
//
// # Description
//
// Trusted timestamps prove content existed at a specific time,
// required for MiFID II, SOX, and legal evidence.
type TSAConfig struct {
	// Enabled toggles trusted timestamping.
	Enabled bool `yaml:"enabled"`

	// Provider specifies the TSA provider.
	// Options: "digicert", "globalsign", "sectigo", "freetsa", "custom"
	Provider string `yaml:"provider"`

	// URL is the TSA endpoint (for custom provider).
	URL string `yaml:"url,omitempty"`

	// CertPath is the path to TSA certificate for verification.
	CertPath string `yaml:"cert_path,omitempty"`

	// Timeout is the maximum time for TSA requests.
	// Default: 30s
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

// HSMConfig configures hardware security module integration.
//
// # Description
//
// HSM integration provides highest security level where keys
// never leave tamper-resistant hardware. Required for FIPS 140-2
// Level 3, PCI-DSS, and government requirements.
type HSMConfig struct {
	// Enabled toggles HSM usage.
	Enabled bool `yaml:"enabled"`

	// Provider specifies the HSM vendor/interface.
	// Options: "pkcs11", "aws_cloudhsm", "azure_hsm", "thales_luna"
	Provider string `yaml:"provider"`

	// LibraryPath is the path to PKCS#11 library.
	LibraryPath string `yaml:"library_path,omitempty"`

	// SlotID is the HSM slot identifier.
	SlotID int `yaml:"slot_id,omitempty"`

	// KeyLabel is the label of the signing key in HSM.
	KeyLabel string `yaml:"key_label,omitempty"`

	// PIN is the HSM PIN (should use secrets manager).
	// Use "env:HSM_PIN" or "vault:secret/hsm/pin" syntax.
	PIN string `yaml:"pin,omitempty"`
}

// ValidHMACAlgorithms lists acceptable HMAC algorithms.
// SHA-1 is explicitly excluded due to security concerns.
var ValidHMACAlgorithms = map[string]bool{
	"sha256": true,
	"sha384": true,
	"sha512": true,
}

// Validate checks that the HMACConfig has valid settings.
//
// # Description
//
// Validates that the algorithm is one of the supported values.
// Returns an error if the configuration is invalid.
//
// # Outputs
//
//   - error: nil if valid, descriptive error otherwise
func (c *HMACConfig) Validate() error {
	if !c.Enabled {
		return nil // No validation needed when disabled
	}
	// Default to sha256 if not specified
	if c.Algorithm == "" {
		return nil // Will use default
	}
	if !ValidHMACAlgorithms[c.Algorithm] {
		return &ValidationError{
			Field:   "hmac.algorithm",
			Value:   c.Algorithm,
			Message: "must be one of: sha256, sha384, sha512",
		}
	}
	return nil
}

// ValidHSMProviders lists acceptable HSM provider types.
var ValidHSMProviders = map[string]bool{
	"pkcs11":       true,
	"aws_cloudhsm": true,
	"azure_hsm":    true,
	"thales_luna":  true,
}

// Validate checks that the HSMConfig has valid settings.
//
// # Description
//
// Validates that the provider is one of the supported values.
// Returns an error if the configuration is invalid.
//
// # Outputs
//
//   - error: nil if valid, descriptive error otherwise
func (c *HSMConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider == "" {
		return &ValidationError{
			Field:   "hsm.provider",
			Value:   "",
			Message: "required when HSM is enabled",
		}
	}
	if !ValidHSMProviders[c.Provider] {
		return &ValidationError{
			Field:   "hsm.provider",
			Value:   c.Provider,
			Message: "must be one of: pkcs11, aws_cloudhsm, azure_hsm, thales_luna",
		}
	}
	return nil
}

// ValidTSAProviders lists acceptable TSA provider types.
var ValidTSAProviders = map[string]bool{
	"digicert":   true,
	"globalsign": true,
	"sectigo":    true,
	"freetsa":    true,
	"custom":     true,
}

// Validate checks that the TSAConfig has valid settings.
//
// # Description
//
// Validates that the provider is one of the supported values.
// When provider is "custom", a URL must be specified.
// Returns an error if the configuration is invalid.
//
// # Outputs
//
//   - error: nil if valid, descriptive error otherwise
func (c *TSAConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider == "" {
		return &ValidationError{
			Field:   "tsa.provider",
			Value:   "",
			Message: "required when TSA is enabled",
		}
	}
	if !ValidTSAProviders[c.Provider] {
		return &ValidationError{
			Field:   "tsa.provider",
			Value:   c.Provider,
			Message: "must be one of: digicert, globalsign, sectigo, freetsa, custom",
		}
	}
	if c.Provider == "custom" && c.URL == "" {
		return &ValidationError{
			Field:   "tsa.url",
			Value:   "",
			Message: "required when provider is 'custom'",
		}
	}
	return nil
}

// ValidSignatureAlgorithms lists acceptable signature algorithms.
var ValidSignatureAlgorithms = map[string]bool{
	"rsa2048":    true,
	"rsa4096":    true,
	"ecdsa_p256": true,
	"ecdsa_p384": true,
	"ed25519":    true,
}

// Validate checks that the SignatureConfig has valid settings.
//
// # Description
//
// Validates that the algorithm is one of the supported values.
// Returns an error if the configuration is invalid.
//
// # Outputs
//
//   - error: nil if valid, descriptive error otherwise
func (c *SignatureConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	// Default to ecdsa_p256 if not specified
	if c.Algorithm == "" {
		return nil
	}
	if !ValidSignatureAlgorithms[c.Algorithm] {
		return &ValidationError{
			Field:   "signatures.algorithm",
			Value:   c.Algorithm,
			Message: "must be one of: rsa2048, rsa4096, ecdsa_p256, ecdsa_p384, ed25519",
		}
	}
	return nil
}

// ValidationError represents a configuration validation failure.
//
// # Description
//
// Provides structured error information for configuration validation
// failures, including the field name, invalid value, and error message.
type ValidationError struct {
	Field   string
	Value   string
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return "config validation error: " + e.Field + " (" + e.Value + "): " + e.Message
}

// VerificationAuditConfig configures compliance audit logging.
//
// # Description
//
// All verification attempts are logged for compliance with
// SOC 2, HIPAA, GDPR, and PCI-DSS audit requirements.
type VerificationAuditConfig struct {
	// Enabled toggles audit logging.
	Enabled bool `yaml:"enabled"`

	// Destination specifies where audit logs are sent.
	// Options: "siem", "s3", "gcs", "file", "syslog"
	Destination string `yaml:"destination"`

	// SIEMEndpoint is the SIEM webhook URL.
	SIEMEndpoint string `yaml:"siem_endpoint,omitempty"`

	// S3Bucket is the S3 bucket for audit logs.
	S3Bucket string `yaml:"s3_bucket,omitempty"`

	// GCSBucket is the GCS bucket for audit logs.
	GCSBucket string `yaml:"gcs_bucket,omitempty"`

	// FilePath is the local file path for audit logs.
	FilePath string `yaml:"file_path,omitempty"`

	// RetentionDays is how long to retain audit logs.
	// Default: 2555 (7 years for financial compliance)
	RetentionDays int `yaml:"retention_days,omitempty"`

	// SignLogs enables signing audit log entries.
	SignLogs bool `yaml:"sign_logs"`
}

// AuthorizationConfig configures access control for verification.
//
// # Description
//
// Controls who can verify which sessions for multi-tenant
// security and compliance requirements.
type AuthorizationConfig struct {
	// Enabled toggles authorization checks.
	Enabled bool `yaml:"enabled"`

	// Provider specifies the policy engine.
	// Options: "opa", "casbin", "internal"
	Provider string `yaml:"provider"`

	// OPAEndpoint is the OPA server URL.
	OPAEndpoint string `yaml:"opa_endpoint,omitempty"`

	// PolicyPath is the policy document path.
	PolicyPath string `yaml:"policy_path,omitempty"`
}

// RateLimitConfig configures verification request rate limiting.
type RateLimitConfig struct {
	// Enabled toggles rate limiting.
	Enabled bool `yaml:"enabled"`

	// RequestsPerMinute per user.
	// Default: 60
	RequestsPerMinute int `yaml:"requests_per_minute,omitempty"`

	// RequestsPerHour per tenant.
	// Default: 1000
	RequestsPerHour int `yaml:"requests_per_hour,omitempty"`

	// BurstSize allows short bursts above limit.
	// Default: 10
	BurstSize int `yaml:"burst_size,omitempty"`
}

// VerificationCacheConfig configures caching of verification results.
type VerificationCacheConfig struct {
	// Enabled toggles result caching.
	Enabled bool `yaml:"enabled"`

	// Backend specifies cache implementation.
	// Options: "redis", "memcached", "memory"
	Backend string `yaml:"backend"`

	// RedisURL is the Redis connection string.
	RedisURL string `yaml:"redis_url,omitempty"`

	// TTL is cache entry lifetime.
	// Default: 5m
	TTL time.Duration `yaml:"ttl,omitempty"`
}

// VerificationScheduleConfig configures periodic background verification.
type VerificationScheduleConfig struct {
	// Enabled toggles scheduled verification.
	Enabled bool `yaml:"enabled"`

	// DefaultInterval is how often to verify sessions.
	// Default: 24h
	DefaultInterval time.Duration `yaml:"default_interval,omitempty"`

	// PrioritySessionInterval for high-priority sessions.
	// Default: 1h
	PrioritySessionInterval time.Duration `yaml:"priority_session_interval,omitempty"`

	// MaxConcurrent limits parallel verifications.
	// Default: 10
	MaxConcurrent int `yaml:"max_concurrent,omitempty"`
}

// VerificationAlertConfig configures failure alerting.
type VerificationAlertConfig struct {
	// Enabled toggles alerting.
	Enabled bool `yaml:"enabled"`

	// Destinations lists alert channels.
	// Options: "pagerduty", "slack", "email", "webhook"
	Destinations []string `yaml:"destinations,omitempty"`

	// PagerDutyKey is the PagerDuty integration key.
	PagerDutyKey string `yaml:"pagerduty_key,omitempty"`

	// SlackWebhook is the Slack incoming webhook URL.
	SlackWebhook string `yaml:"slack_webhook,omitempty"`

	// EmailRecipients for email alerts.
	EmailRecipients []string `yaml:"email_recipients,omitempty"`

	// WebhookURL for custom alert webhook.
	WebhookURL string `yaml:"webhook_url,omitempty"`

	// ThrottleMinutes prevents alert spam.
	// Default: 15
	ThrottleMinutes int `yaml:"throttle_minutes,omitempty"`
}

// DefaultSessionIntegrityConfig returns sensible defaults.
//
// # Description
//
// Creates a SessionIntegrityConfig with open-source defaults.
// Enterprise features are disabled by default.
//
// # Outputs
//
//   - SessionIntegrityConfig: Configuration with default values
func DefaultSessionIntegrityConfig() SessionIntegrityConfig {
	return SessionIntegrityConfig{
		Enabled:           true,
		VerificationMode:  "quick",
		AutoVerifyOnEnd:   true,
		ShowHashInSummary: true,
		Enterprise: EnterpriseIntegrityConfig{
			HMAC:       HMACConfig{Enabled: false},
			Signatures: SignatureConfig{Enabled: false},
			TSA:        TSAConfig{Enabled: false},
			HSM:        HSMConfig{Enabled: false},
			Audit: VerificationAuditConfig{
				Enabled:       false,
				RetentionDays: 2555,
			},
			Authorization: AuthorizationConfig{Enabled: false},
			RateLimiting: RateLimitConfig{
				Enabled:           false,
				RequestsPerMinute: 60,
				RequestsPerHour:   1000,
				BurstSize:         10,
			},
			Caching: VerificationCacheConfig{
				Enabled: false,
				TTL:     5 * time.Minute,
			},
			Scheduling: VerificationScheduleConfig{
				Enabled:                 false,
				DefaultInterval:         24 * time.Hour,
				PrioritySessionInterval: 1 * time.Hour,
				MaxConcurrent:           10,
			},
			Alerting: VerificationAlertConfig{
				Enabled:         false,
				ThrottleMinutes: 15,
			},
		},
	}
}

// DefaultModelManagementConfig returns sensible defaults.
//
// # Description
//
// Creates a ModelManagementConfig with production-ready defaults
// that enable verification, auto-selection, and parallel downloads.
//
// # Outputs
//
//   - ModelManagementConfig: Configuration with default values
func DefaultModelManagementConfig() ModelManagementConfig {
	return ModelManagementConfig{
		DiskLimitGB:        50,
		AllowedModels:      nil, // All allowed
		VerifyOnStart:      true,
		OfflineModeAllowed: true,
		Integrity: IntegrityConfig{
			VerifyAfterDownload: true,
			VerifyOnStartup:     true,
			FailOnMismatch:      true,
		},
		Parallel: ParallelDownloadConfig{
			Enabled:       true,
			MaxConcurrent: 3,
		},
		AutoSelection: AutoSelectionConfig{
			Enabled:          true,
			PreferQuantized:  true,
			MinContextWindow: 4096,
		},
		SizeEstimation: SizeEstimationConfig{
			Enabled:               true,
			WarnThresholdGB:       10,
			RequireConfirmationGB: 50,
		},
		Audit: AuditLoggingConfig{
			Enabled:          true,
			LogPulls:         true,
			LogVerifications: true,
			LogBlocks:        true,
			IncludeHostname:  true,
			IncludeUser:      true,
		},
	}
}

// -----------------------------------------------------------------------------
// Forecast Configuration
// -----------------------------------------------------------------------------

// ForecastConfig configures the optional timeseries forecast module.
//
// # Description
//
// Controls the forecast/timeseries functionality, including whether
// to run a local service or connect to external Sapheneia.
type ForecastConfig struct {
	// Enabled toggles the forecast module on/off.
	Enabled bool `yaml:"enabled"`

	// Mode determines deployment: "standalone" or "sapheneia".
	Mode ForecastMode `yaml:"mode"`
}

// -----------------------------------------------------------------------------
// Observability Configuration
// -----------------------------------------------------------------------------

// ObservabilityConfig configures the observability stack.
//
// # Description
//
// Controls the Prometheus and Grafana services that provide metrics
// collection and visualization. Enabled by default.
//
// # Fields
//
//   - Enabled: Toggle observability stack (default: true)
//   - PrometheusPort: Host port for Prometheus UI (default: 9091)
//   - GrafanaPort: Host port for Grafana UI (default: 3000)
//
// # Example YAML
//
//	observability:
//	  enabled: true
//	  prometheus_port: 12215
//	  grafana_port: 12216
type ObservabilityConfig struct {
	// Enabled toggles the observability stack on/off.
	// Default: true (observability is enabled by default)
	Enabled *bool `yaml:"enabled,omitempty"`

	// PrometheusPort is the host port for Prometheus UI.
	// Default: 12215
	PrometheusPort int `yaml:"prometheus_port,omitempty"`

	// GrafanaPort is the host port for Grafana UI.
	// Default: 12216
	GrafanaPort int `yaml:"grafana_port,omitempty"`
}

// IsEnabled returns whether observability is enabled.
// Returns true if Enabled is nil (default) or explicitly true.
func (o ObservabilityConfig) IsEnabled() bool {
	return o.Enabled == nil || *o.Enabled
}

// -----------------------------------------------------------------------------
// Profile Configuration
// -----------------------------------------------------------------------------

// ProfileConfig defines a custom optimization profile.
//
// # Description
//
// Allows users to define custom profiles that extend or override
// the built-in profiles (low, standard, performance, ultra).
//
// # Fields
//
//   - Name: Unique identifier for the profile
//   - OllamaModel: LLM model to use with this profile
//   - MaxTokens: Context window size
//   - RerankerModel: Reranking model for RAG
//   - MinRAM_MB: Minimum RAM required to use this profile
//
// # Example YAML
//
//	profiles:
//	  - name: my-custom
//	    ollama_model: mixtral:8x7b
//	    max_tokens: 16384
//	    reranker_model: cross-encoder/ms-marco-MiniLM-L-6-v2
//	    min_ram_mb: 48000
type ProfileConfig struct {
	// Name is the unique identifier for this profile.
	Name string `yaml:"name"`

	// OllamaModel is the LLM model to use with this profile.
	OllamaModel string `yaml:"ollama_model"`

	// MaxTokens is the context window size for the LLM.
	MaxTokens int `yaml:"max_tokens"`

	// RerankerModel is the model used for reranking in RAG.
	RerankerModel string `yaml:"reranker_model"`

	// MinRAM_MB is the minimum RAM (in MB) required for this profile.
	MinRAM_MB int64 `yaml:"min_ram_mb"`
}

// -----------------------------------------------------------------------------
// Configuration Metadata (Versioning & Audit)
// -----------------------------------------------------------------------------

// ConfigMeta contains metadata for configuration versioning and auditing.
//
// # Description
//
// Tracks when and how the configuration was created or modified.
// Required for compliance with GDPR, HIPAA, and CCPA audit requirements.
//
// # Fields
//
//   - Version: Schema version for migration support
//   - CreatedAt: Unix millisecond timestamp when config was first created
//   - ModifiedAt: Unix millisecond timestamp when config was last modified
//   - ModifiedBy: Identifier of who/what modified the config
//
// # Timestamp Format
//
// All timestamps are stored as Unix milliseconds (int64) for precision
// and easy comparison. Use time.UnixMilli() to convert.
type ConfigMeta struct {
	// Version is the configuration schema version.
	// Used for migration when schema changes.
	Version string `yaml:"version"`

	// CreatedAt is the Unix millisecond timestamp when config was created.
	CreatedAt int64 `yaml:"created_at"`

	// ModifiedAt is the Unix millisecond timestamp when config was last modified.
	ModifiedAt int64 `yaml:"modified_at"`

	// ModifiedBy identifies who or what modified the config.
	// Examples: "user", "aleutian-cli", "migration-v2"
	ModifiedBy string `yaml:"modified_by"`
}

// CreatedAtTime returns the CreatedAt timestamp as a time.Time.
//
// # Description
//
// Converts the Unix millisecond timestamp to a Go time.Time value.
//
// # Outputs
//
//   - time.Time: The creation time
func (m *ConfigMeta) CreatedAtTime() time.Time {
	return time.UnixMilli(m.CreatedAt)
}

// ModifiedAtTime returns the ModifiedAt timestamp as a time.Time.
//
// # Description
//
// Converts the Unix millisecond timestamp to a Go time.Time value.
//
// # Outputs
//
//   - time.Time: The modification time
func (m *ConfigMeta) ModifiedAtTime() time.Time {
	return time.UnixMilli(m.ModifiedAt)
}

// CurrentConfigVersion is the current configuration schema version.
const CurrentConfigVersion = "1.0.0"

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// findExternalDrives automatically discovers mounted external drives on macOS.
//
// # Description
//
// Scans /Volumes for mounted external drives, filtering out system volumes
// like "Macintosh HD" and hidden directories.
//
// # Outputs
//
//   - []string: List of external drive paths, or nil on non-macOS or error
//
// # Limitations
//
// Only works on macOS. Returns nil on other platforms.
func findExternalDrives() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	var externalDrives []string
	volumesDir := "/Volumes"
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		return nil
	}
	cmd := exec.Command("mount")
	output, err := cmd.Output()
	mountOutput := string(output)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "Macintosh HD" || strings.HasPrefix(name, ".") || name == "Recovery" {
			continue
		}

		fullPath := filepath.Join(volumesDir, name)
		if err == nil && strings.Contains(mountOutput, fullPath) {
			externalDrives = append(externalDrives, fullPath)
		}
	}
	return externalDrives
}

// buildDefaultDrives determines default mount paths based on the host OS.
//
// # Description
//
// Creates a list of default paths to mount into the Podman VM.
// Includes the user's home directory and platform-specific mount points.
//
// # Outputs
//
//   - []string: List of paths to mount
func buildDefaultDrives() []string {
	var defaultDrives []string

	// Always mount the user's home directory
	home, err := os.UserHomeDir()
	if err == nil {
		defaultDrives = append(defaultDrives, home)
	}

	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/Volumes"); err == nil {
			defaultDrives = append(defaultDrives, "/Volumes")
		}
		extDrives := findExternalDrives()
		defaultDrives = append(defaultDrives, extDrives...)
	} else if runtime.GOOS == "linux" {
		if _, err := os.Stat("/mnt"); err == nil {
			defaultDrives = append(defaultDrives, "/mnt")
		}
		if _, err := os.Stat("/media"); err == nil {
			defaultDrives = append(defaultDrives, "/media")
		}
	}

	return defaultDrives
}

// newConfigMeta creates a new ConfigMeta with current timestamp.
//
// # Description
//
// Initializes metadata for a new configuration file with the
// current schema version and creation timestamp.
//
// # Outputs
//
//   - ConfigMeta: Initialized metadata
func newConfigMeta() ConfigMeta {
	now := time.Now().UnixMilli()
	return ConfigMeta{
		Version:    CurrentConfigVersion,
		CreatedAt:  now,
		ModifiedAt: now,
		ModifiedBy: "aleutian-cli",
	}
}

// DefaultConfig returns the default Aleutian configuration.
//
// # Description
//
// Creates a new AleutianConfig with sensible defaults for all settings.
// This is used when no configuration file exists on first run.
//
// # Outputs
//
//   - AleutianConfig: Configuration with default values
//
// # Default Values
//
//   - Machine: 6 CPUs, 20GB RAM, auto-detected drives
//   - Backend: Ollama with default embedding model
//   - Features: Observability and RAG enabled
//   - Forecast: Standalone mode enabled
func DefaultConfig() AleutianConfig {
	return AleutianConfig{
		Meta: newConfigMeta(),
		Machine: MachineConfig{
			Id:           "podman-machine-default",
			CPUCount:     6,
			MemoryAmount: 20480,
			Drives:       buildDefaultDrives(),
		},
		Extensions: []string{},
		Secrets:    SecretsConfig{UseEnv: false},
		Features: FeatureConfig{
			Observability: true,
			RagEngine:     true,
		},
		ModelBackend: BackendConfig{
			Type:    "ollama",
			BaseURL: DefaultOllamaContainerURL,
			Ollama: OllamaConfig{
				EmbeddingModel: DefaultEmbeddingModel,
				DiskLimitGB:    DefaultDiskLimitGB,
				BaseURL:        DefaultOllamaHostURL,
			},
		},
		Forecast: ForecastConfig{
			Enabled: true,
			Mode:    ForecastModeStandalone,
		},
		ModelManagement:  DefaultModelManagementConfig(),
		SessionIntegrity: DefaultSessionIntegrityConfig(),
		Profiles:         []ProfileConfig{},
	}
}

// -----------------------------------------------------------------------------
// LSP Configuration (GR-75)
// -----------------------------------------------------------------------------

// LSPConfig controls language server availability and behavior.
//
// # Description
//
// In Tier 1 (CLI via `brew install aleutian`), Enabled is false and no
// language servers are spawned — the CLI has zero runtime dependencies.
// In Tier 2 (container via `aleutian stack start`), Enabled is true and
// Pyright + typescript-language-server are bundled in the image.
//
// # YAML Example
//
//	lsp:
//	  enabled: true
//	  python_enabled: true
//	  typescript_enabled: true
//	  startup_timeout: 30s
//	  request_timeout: 5s
//	  idle_timeout: 10m
//
// # Thread Safety
//
// Immutable after construction.
type LSPConfig struct {
	// Enabled toggles the entire LSP enrichment pipeline.
	// Default: false (CLI), true (container, via LSP_ENABLED env var)
	Enabled bool `yaml:"enabled"`

	// PythonEnabled toggles Pyright for Python projects.
	// Default: true when Enabled is true
	PythonEnabled bool `yaml:"python_enabled"`

	// TypeScriptEnabled toggles typescript-language-server for TS projects.
	// Default: true when Enabled is true
	TypeScriptEnabled bool `yaml:"typescript_enabled"`

	// StartupTimeout is the maximum time to wait for a language server to start.
	// Default: 30s
	StartupTimeout time.Duration `yaml:"startup_timeout"`

	// RequestTimeout is the maximum time for a single LSP query.
	// Default: 5s
	RequestTimeout time.Duration `yaml:"request_timeout"`

	// IdleTimeout is how long an idle language server stays alive before shutdown.
	// Default: 10 minutes
	IdleTimeout time.Duration `yaml:"idle_timeout"`
}

// DefaultLSPConfig returns the default LSP configuration (disabled, for CLI mode).
//
// Description:
//
//	Returns a config with LSP disabled and sensible timeout defaults.
//	Container mode overrides Enabled via LSP_ENABLED env var.
//
// Outputs:
//
//	LSPConfig - Default configuration with LSP disabled.
func DefaultLSPConfig() LSPConfig {
	return LSPConfig{
		Enabled:           false,
		PythonEnabled:     true,
		TypeScriptEnabled: true,
		StartupTimeout:    30 * time.Second,
		RequestTimeout:    5 * time.Second,
		IdleTimeout:       10 * time.Minute,
	}
}

// LSPConfigFromEnv reads LSP configuration from environment variables.
//
// Description:
//
//	GR-75: Reads LSP_ENABLED, LSP_PYTHON_ENABLED, LSP_TYPESCRIPT_ENABLED,
//	LSP_STARTUP_TIMEOUT_SECONDS, and LSP_REQUEST_TIMEOUT_SECONDS from the
//	environment. Called during service initialization in container mode.
//
// Outputs:
//
//	LSPConfig - Configuration populated from environment variables.
//
// Thread Safety:
//
//	Safe for concurrent use (reads only environment).
func LSPConfigFromEnv() LSPConfig {
	cfg := DefaultLSPConfig()
	cfg.Enabled = os.Getenv("LSP_ENABLED") == "true"

	// PythonEnabled defaults to true; only disable if explicitly set to "false"
	if os.Getenv("LSP_PYTHON_ENABLED") == "false" {
		cfg.PythonEnabled = false
	}

	// TypeScriptEnabled defaults to true; only disable if explicitly set to "false"
	if os.Getenv("LSP_TYPESCRIPT_ENABLED") == "false" {
		cfg.TypeScriptEnabled = false
	}

	if v := os.Getenv("LSP_STARTUP_TIMEOUT_SECONDS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.StartupTimeout = time.Duration(d) * time.Second
		}
	}
	if v := os.Getenv("LSP_REQUEST_TIMEOUT_SECONDS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			cfg.RequestTimeout = time.Duration(d) * time.Second
		}
	}

	return cfg
}

// LSPVerifyResult holds the outcome of language server binary verification.
//
// Description:
//
//	GR-75: Returned by LSPServerVerifier.Verify() to indicate which language
//	servers are available in the runtime environment.
//
// Thread Safety:
//
//	Immutable after construction.
type LSPVerifyResult struct {
	// PythonAvailable is true if pyright-langserver is found in PATH.
	PythonAvailable bool

	// TypeScriptAvailable is true if typescript-language-server is found in PATH.
	TypeScriptAvailable bool

	// Warnings contains messages for missing binaries.
	Warnings []string
}

// LSPServerVerifier checks that required language server binaries exist.
//
// Description:
//
//	GR-75: Called during service startup when LSP is enabled. Checks
//	exec.LookPath for each enabled language's binary. Missing binaries
//	disable that language's enrichment gracefully (no startup failure).
//
// Thread Safety:
//
//	Safe for concurrent use (stateless, reads config and PATH only).
type LSPServerVerifier struct {
	config LSPConfig

	// lookPath is the function used to locate binaries. Defaults to
	// exec.LookPath. Overridable for testing.
	lookPath func(file string) (string, error)
}

// NewLSPServerVerifier creates a new verifier for the given config.
//
// Description:
//
//	Creates a verifier that uses exec.LookPath to check for language
//	server binaries.
//
// Inputs:
//
//	config - LSP configuration to verify against.
//
// Outputs:
//
//	*LSPServerVerifier - The verifier instance.
func NewLSPServerVerifier(config LSPConfig) *LSPServerVerifier {
	return &LSPServerVerifier{
		config:   config,
		lookPath: exec.LookPath,
	}
}

// Verify checks that all enabled language server binaries are available.
//
// Description:
//
//	GR-75: For each enabled language, checks that the corresponding binary
//	exists in PATH via exec.LookPath. Returns which servers are available
//	and warnings for any that are missing. Also logs warnings via slog.
//
// Outputs:
//
//	LSPVerifyResult - Availability status and warnings.
//
// Thread Safety:
//
//	Safe for concurrent use.
func (v *LSPServerVerifier) Verify() LSPVerifyResult {
	result := LSPVerifyResult{}

	if v.config.PythonEnabled {
		_, err := v.lookPath("pyright-langserver")
		result.PythonAvailable = (err == nil)
		if !result.PythonAvailable {
			msg := "pyright-langserver not found in PATH; Python LSP enrichment disabled"
			result.Warnings = append(result.Warnings, msg)
			slog.Warn("GR-75: " + msg)
		}
	}

	if v.config.TypeScriptEnabled {
		_, err := v.lookPath("typescript-language-server")
		result.TypeScriptAvailable = (err == nil)
		if !result.TypeScriptAvailable {
			msg := "typescript-language-server not found in PATH; TypeScript LSP enrichment disabled"
			result.Warnings = append(result.Warnings, msg)
			slog.Warn("GR-75: " + msg)
		}
	}

	return result
}
