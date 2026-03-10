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
Package main contains model_ensurer.go which coordinates pre-flight model
verification and automatic downloading for the Aleutian stack.

# Problem Statement

When users run `aleutian stack start`, required Ollama models must be available:

 1. Embedding model (always required for RAG)
 2. LLM model (required if backend=ollama)
 3. Tool router model (optional, for trace agent tool routing)
 4. Parameter extractor model (optional, for trace agent parameter extraction)

Previously, users encountered cryptic errors like "model not found" deep in
the stack startup. This component provides clear, early verification with
automatic remediation.

# Solution

ModelEnsurer coordinates between SystemChecker and OllamaModelManager:

	┌─────────────────────────────────────────────────────────────────┐
	│                         ModelEnsurer                            │
	├─────────────────────────────────────────────────────────────────┤
	│                                                                 │
	│  EnsureModels()                                                 │
	│      │                                                          │
	│      ├── Build list of required models from config              │
	│      │                                                          │
	│      ├── For each model:                                        │
	│      │   ├── OllamaModelManager.HasModel()                      │
	│      │   └── OllamaModelManager.IsCustomModel()                 │
	│      │                                                          │
	│      ├── IF models need pulling:                                │
	│      │   ├── SystemChecker.CheckNetworkConnectivity()           │
	│      │   ├── SystemChecker.CheckDiskSpace()                     │
	│      │   └── OllamaModelManager.PullModel() for each            │
	│      │                                                          │
	│      └── Return ModelEnsureResult                               │
	│                                                                 │
	└─────────────────────────────────────────────────────────────────┘

# Model Versioning

Models support Ollama's name:tag format:
  - "nomic-embed-text-v2-moe" → uses :latest implicitly
  - "nomic-embed-text-v2-moe:latest" → explicit latest
  - "gpt-oss:7b-q4" → specific quantization
  - "llama3:70b" → specific size variant

# Graceful Degradation

If network is unavailable but required models exist locally, the ensurer
returns CanProceed=true with OfflineMode=true and appropriate warnings.

# Usage

	ensurer := NewDefaultModelEnsurer(ModelEnsurerConfig{
	    EmbeddingModel: "nomic-embed-text-v2-moe",
	    LLMModel:       "gpt-oss",
	    DiskLimitGB:    50,
	    BackendType:    "ollama",
	})

	ensurer.SetProgressCallback(func(status string, completed, total int64) {
	    fmt.Printf("\r%s: %d/%d", status, completed, total)
	})

	result, err := ensurer.EnsureModels(ctx)
	if err != nil {
	    log.Fatal(err)
	}

	if !result.CanProceed {
	    log.Fatal("Required models not available")
	}

# Configuration

The ensurer respects these environment variables (which override config):
  - EMBEDDING_MODEL: Override embedding model name
  - OLLAMA_MODEL: Override LLM model name

# Related Files

  - system_checker.go: System pre-flight checks
  - ollama_client.go: Ollama API client
  - cmd_stack.go: Integration point
  - docs/designs/pending/ollama_model_management.md: Architecture
*/
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra"
)

// -----------------------------------------------------------------------------
// Constants
// -----------------------------------------------------------------------------

const (
	// DefaultEmbeddingModel is used when EMBEDDING_MODEL env var is not set.
	DefaultEmbeddingModel = "nomic-embed-text-v2-moe"

	// DefaultLLMModel is used when OLLAMA_MODEL env var is not set.
	DefaultLLMModel = "gpt-oss"

	// DefaultToolRouterModel is the default model for tool routing in the trace agent.
	DefaultToolRouterModel = "granite4:micro-h"

	// DefaultParamExtractorModel is the default model for parameter extraction in the trace agent.
	DefaultParamExtractorModel = "ministral-3:3b"

	// DefaultDiskLimitGB is the default disk space limit for model storage.
	DefaultDiskLimitGB = 50

	// DefaultOllamaBaseURL is the default Ollama server URL.
	DefaultOllamaBaseURL = "http://localhost:11434"

	// GB is bytes in a gigabyte.
	GB = 1024 * 1024 * 1024

	// FallbackModelSizeBytes is used when actual model size cannot be determined.
	FallbackModelSizeBytes = 500 * 1024 * 1024 // 500MB
)

// -----------------------------------------------------------------------------
// Enums
// -----------------------------------------------------------------------------

// ModelPurpose categorizes why a model is needed.
type ModelPurpose int

const (
	// ModelPurposeEmbedding indicates model is used for text embeddings.
	ModelPurposeEmbedding ModelPurpose = iota

	// ModelPurposeLLM indicates model is used for language generation.
	ModelPurposeLLM

	// ModelPurposeReranking indicates model is used for search reranking.
	ModelPurposeReranking

	// ModelPurposeToolRouting indicates model is used for tool selection routing.
	ModelPurposeToolRouting

	// ModelPurposeParamExtraction indicates model is used for parameter extraction.
	ModelPurposeParamExtraction
)

// String returns the purpose as a human-readable string.
//
// # Description
//
// Converts the ModelPurpose enum to a lowercase string representation
// suitable for logging and display.
//
// # Outputs
//
//   - string: "embedding", "LLM", "reranking", or "unknown"
//
// # Examples
//
//	purpose := ModelPurposeEmbedding
//	fmt.Println(purpose.String()) // "embedding"
func (p ModelPurpose) String() string {
	switch p {
	case ModelPurposeEmbedding:
		return "embedding"
	case ModelPurposeLLM:
		return "LLM"
	case ModelPurposeReranking:
		return "reranking"
	case ModelPurposeToolRouting:
		return "tool routing"
	case ModelPurposeParamExtraction:
		return "param extraction"
	default:
		return "unknown"
	}
}

// -----------------------------------------------------------------------------
// Data Types
// -----------------------------------------------------------------------------

// RequiredModel represents a model that must be available.
type RequiredModel struct {
	// Name is the model identifier with optional tag (e.g., "gpt-oss:7b-q4").
	Name string

	// Purpose indicates why this model is needed.
	Purpose ModelPurpose

	// Required indicates if missing model is an error (true) or warning (false).
	Required bool

	// DefaultName is the fallback if Name is empty.
	DefaultName string

	// EnvVar is the environment variable that can override Name.
	EnvVar string
}

// ModelStatus represents the status of a single model after checking.
type ModelStatus struct {
	// Name is the model identifier.
	Name string

	// Available indicates if the model exists locally.
	Available bool

	// IsCustom indicates if this is a user-created model (has template).
	IsCustom bool

	// Size is the model size in bytes (0 if unknown).
	Size int64

	// WasPulled indicates if the model was downloaded during this run.
	WasPulled bool

	// Error contains any error encountered while checking/pulling.
	Error error
}

// ModelEnsureResult contains the outcome of EnsureModels.
type ModelEnsureResult struct {
	// ModelsChecked contains status for each model that was verified.
	ModelsChecked []ModelStatus

	// ModelsPulled lists models that were successfully downloaded.
	ModelsPulled []string

	// ModelsSkipped lists custom models that were not pulled.
	ModelsSkipped []string

	// ModelsMissing lists required models that could not be obtained.
	ModelsMissing []string

	// CanProceed indicates if all required models are available.
	CanProceed bool

	// OfflineMode indicates operation without network connectivity.
	OfflineMode bool

	// Warnings contains non-fatal issues encountered.
	Warnings []string
}

// ModelEnsurerConfig holds configuration for ModelEnsurer.
type ModelEnsurerConfig struct {
	// EmbeddingModel is the embedding model name (default: nomic-embed-text-v2-moe).
	EmbeddingModel string

	// LLMModel is the LLM model name (empty if not using Ollama for LLM).
	LLMModel string

	// ToolRouterModel is the model for trace agent tool routing (empty = default granite4:micro-h).
	ToolRouterModel string

	// ParamExtractorModel is the model for trace agent parameter extraction (empty = default ministral-3:3b).
	ParamExtractorModel string

	// DiskLimitGB is the maximum disk space for models (default: 50, 0 = no limit).
	DiskLimitGB int64

	// OllamaBaseURL is the Ollama server URL (default: http://localhost:11434).
	OllamaBaseURL string

	// BackendType is the LLM backend ("ollama", "openai", "anthropic").
	BackendType string
}

// -----------------------------------------------------------------------------
// Interface Definition
// -----------------------------------------------------------------------------

// ModelEnsurer defines the contract for ensuring required models are available.
// This interface coordinates between SystemChecker and OllamaModelManager
// to verify and pull models before stack startup.
//
// Implementations must be safe for concurrent use.
type ModelEnsurer interface {
	// EnsureModels verifies all required models are available.
	// Downloads missing models if network is available.
	// Returns result with details of each model's status.
	EnsureModels(ctx context.Context) (*ModelEnsureResult, error)

	// GetRequiredModels returns the list of models that need to be available.
	GetRequiredModels() []RequiredModel

	// SetProgressCallback sets the callback for pull progress updates.
	// Pass nil to disable progress reporting.
	SetProgressCallback(callback PullProgressCallback)
}

// -----------------------------------------------------------------------------
// Struct Definition
// -----------------------------------------------------------------------------

// DefaultModelEnsurer implements ModelEnsurer using SystemChecker and OllamaModelManager.
type DefaultModelEnsurer struct {
	// Dependencies (injected for testing)
	systemChecker infra.SystemChecker
	modelManager  OllamaModelManager

	// Configuration
	requiredModels   []RequiredModel
	diskLimitBytes   int64
	progressCallback PullProgressCallback

	// Thread safety
	mu sync.RWMutex
}

// -----------------------------------------------------------------------------
// Constructors
// -----------------------------------------------------------------------------

// NewDefaultModelEnsurer creates a ModelEnsurer with production dependencies.
//
// # Description
//
// Creates a ModelEnsurer configured for the local system with real
// SystemChecker and OllamaClient dependencies. This is the constructor
// used in production code.
//
// # Inputs
//
//   - cfg: Configuration specifying model names, disk limits, and backend type
//
// # Outputs
//
//   - *DefaultModelEnsurer: Fully configured ensurer ready for use
//
// # Examples
//
//	ensurer := NewDefaultModelEnsurer(ModelEnsurerConfig{
//	    EmbeddingModel: "nomic-embed-text-v2-moe",
//	    LLMModel:       "gpt-oss",
//	    DiskLimitGB:    50,
//	    BackendType:    "ollama",
//	})
//	result, err := ensurer.EnsureModels(ctx)
//
// # Limitations
//
//   - Requires Ollama server to be running at OllamaBaseURL
//   - Cannot be used for unit testing (use NewDefaultModelEnsurerWithDeps)
//
// # Assumptions
//
//   - Ollama server is accessible at the configured URL
//   - ensureOllamaRunning() has been called before this
//   - Network may or may not be available
func NewDefaultModelEnsurer(cfg ModelEnsurerConfig) *DefaultModelEnsurer {
	baseURL := cfg.OllamaBaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	return NewDefaultModelEnsurerWithDeps(
		infra.NewDefaultSystemChecker(),
		NewOllamaClient(baseURL),
		cfg,
	)
}

// NewDefaultModelEnsurerWithDeps creates a ModelEnsurer with injected dependencies.
//
// # Description
//
// Creates a ModelEnsurer with custom dependencies for testing. This allows
// unit tests to inject mock implementations of SystemChecker and
// OllamaModelManager to test behavior without real Ollama or network.
//
// # Inputs
//
//   - checker: SystemChecker implementation for system pre-flight checks
//   - manager: OllamaModelManager implementation for model operations
//   - cfg: Configuration specifying model names, disk limits, and backend type
//
// # Outputs
//
//   - *DefaultModelEnsurer: Configured ensurer with injected dependencies
//
// # Examples
//
//	mockChecker := &MockSystemChecker{
//	    networkError: nil,
//	    availableDiskSpace: 100 * GB,
//	}
//	mockManager := &MockOllamaModelManager{
//	    hasModelMap: map[string]bool{"gpt-oss": true},
//	}
//	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
//
// # Limitations
//
//   - Caller must ensure dependencies are not nil
//   - Caller must ensure dependencies are properly configured
//
// # Assumptions
//
//   - Both checker and manager are valid, non-nil implementations
//   - Dependencies have been initialized before calling this
func NewDefaultModelEnsurerWithDeps(
	checker infra.SystemChecker,
	manager OllamaModelManager,
	cfg ModelEnsurerConfig,
) *DefaultModelEnsurer {
	requiredModels := buildRequiredModelsList(cfg)
	diskLimitBytes := calculateDiskLimitBytes(cfg.DiskLimitGB)

	return &DefaultModelEnsurer{
		systemChecker:  checker,
		modelManager:   manager,
		requiredModels: requiredModels,
		diskLimitBytes: diskLimitBytes,
	}
}

// buildRequiredModelsList constructs the list of required models from config.
//
// # Description
//
// Builds the list of RequiredModel structs based on the configuration.
// The embedding model is always added. The LLM model is only added if
// the backend type is "ollama".
//
// # Inputs
//
//   - cfg: Configuration containing model names and backend type
//
// # Outputs
//
//   - []RequiredModel: List of models that must be available
//
// # Examples
//
//	cfg := ModelEnsurerConfig{
//	    EmbeddingModel: "nomic-embed-text-v2-moe",
//	    LLMModel:       "gpt-oss",
//	    BackendType:    "ollama",
//	}
//	models := buildRequiredModelsList(cfg)
//	// models contains both embedding and LLM models
//
// # Limitations
//
//   - Only supports single embedding model (by design)
//   - LLM model only included for "ollama" backend
//
// # Assumptions
//
//   - Empty model names will use defaults
//   - BackendType determines if LLM model is required
func buildRequiredModelsList(cfg ModelEnsurerConfig) []RequiredModel {
	models := []RequiredModel{}

	embeddingModel := resolveModelName(cfg.EmbeddingModel, DefaultEmbeddingModel)
	models = append(models, RequiredModel{
		Name:        embeddingModel,
		Purpose:     ModelPurposeEmbedding,
		Required:    true,
		DefaultName: DefaultEmbeddingModel,
		EnvVar:      "EMBEDDING_MODEL",
	})

	if cfg.BackendType == "ollama" {
		llmModel := resolveModelName(cfg.LLMModel, DefaultLLMModel)
		models = append(models, RequiredModel{
			Name:        llmModel,
			Purpose:     ModelPurposeLLM,
			Required:    true,
			DefaultName: DefaultLLMModel,
			EnvVar:      "OLLAMA_MODEL",
		})

		routerModel := resolveModelName(cfg.ToolRouterModel, DefaultToolRouterModel)
		models = append(models, RequiredModel{
			Name:        routerModel,
			Purpose:     ModelPurposeToolRouting,
			Required:    false, // Non-fatal: trace service degrades to regex fallback
			DefaultName: DefaultToolRouterModel,
			EnvVar:      "TRACE_ROUTER_MODEL",
		})

		paramModel := resolveModelName(cfg.ParamExtractorModel, DefaultParamExtractorModel)
		models = append(models, RequiredModel{
			Name:        paramModel,
			Purpose:     ModelPurposeParamExtraction,
			Required:    false, // Non-fatal: trace service degrades to regex fallback
			DefaultName: DefaultParamExtractorModel,
			EnvVar:      "TRACE_PARAM_MODEL",
		})
	}

	return models
}

// resolveModelName returns the provided name or falls back to default.
//
// # Description
//
// Simple helper that returns the provided model name if non-empty,
// otherwise returns the default name.
//
// # Inputs
//
//   - provided: The configured model name (may be empty)
//   - defaultName: The fallback model name
//
// # Outputs
//
//   - string: The resolved model name
//
// # Examples
//
//	name := resolveModelName("", "gpt-oss")      // returns "gpt-oss"
//	name := resolveModelName("llama3:70b", "")  // returns "llama3:70b"
func resolveModelName(provided, defaultName string) string {
	if provided == "" {
		return defaultName
	}
	return provided
}

// calculateDiskLimitBytes converts GB limit to bytes.
//
// # Description
//
// Converts the disk limit from gigabytes to bytes. If the provided
// limit is 0, uses the default limit.
//
// # Inputs
//
//   - limitGB: Disk limit in gigabytes (0 = use default)
//
// # Outputs
//
//   - int64: Disk limit in bytes
//
// # Examples
//
//	bytes := calculateDiskLimitBytes(50)  // returns 50 * 1024^3
//	bytes := calculateDiskLimitBytes(0)   // returns DefaultDiskLimitGB * 1024^3
func calculateDiskLimitBytes(limitGB int64) int64 {
	if limitGB == 0 {
		return DefaultDiskLimitGB * GB
	}
	return limitGB * GB
}

// -----------------------------------------------------------------------------
// Interface Methods
// -----------------------------------------------------------------------------

// EnsureModels verifies all required models are available.
//
// # Description
//
// Performs the complete model verification workflow:
//  1. Checks each required model's availability via checkAllModels
//  2. Determines which models need to be pulled
//  3. Performs pre-flight checks (network, disk) via performPreflightChecks
//  4. Downloads missing models via pullMissingModels
//  5. Assembles and returns comprehensive result
//
// This is the main entry point for model verification.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout control
//
// # Outputs
//
//   - *ModelEnsureResult: Detailed outcome including:
//   - ModelsChecked: Status of each model
//   - ModelsPulled: Successfully downloaded models
//   - ModelsMissing: Required models that couldn't be obtained
//   - CanProceed: Whether stack startup should continue
//   - Warnings: Non-fatal issues
//   - error: Non-nil only for fatal errors (e.g., Ollama connection failed)
//
// # Examples
//
//	result, err := ensurer.EnsureModels(ctx)
//	if err != nil {
//	    // Fatal error - cannot continue
//	    log.Fatalf("Model verification failed: %v", err)
//	}
//	if !result.CanProceed {
//	    // Required models missing
//	    fmt.Printf("Missing models: %v\n", result.ModelsMissing)
//	    fmt.Println("Use --skip-model-check for offline mode")
//	    os.Exit(1)
//	}
//	if result.OfflineMode {
//	    fmt.Println("Warning: Operating in offline mode")
//	}
//
// # Limitations
//
//   - Cannot recover from Ollama server being unreachable
//   - Large model downloads may take significant time
//   - Network failures during pull will cause that model to fail
//
// # Assumptions
//
//   - Ollama server is running (ensureOllamaRunning was called)
//   - Context timeout is appropriate for model download sizes
//   - Disk space check was accurate at time of check
func (e *DefaultModelEnsurer) EnsureModels(ctx context.Context) (*ModelEnsureResult, error) {
	result := e.initializeResult()

	// Step 1: Check all model availability
	needsPull, err := e.checkAllModels(ctx, result)
	if err != nil {
		return nil, err
	}

	// Step 2: Early return if no pulling needed
	if len(needsPull) == 0 {
		slog.Debug("All required models are available")
		return result, nil
	}

	// Step 3: Pre-flight checks
	canPull, offlineMode, diskWarning, err := e.performPreflightChecks(ctx, needsPull)
	if err != nil {
		return nil, err
	}
	result.OfflineMode = offlineMode

	// Add disk limit warning if present (soft warning - we still proceed)
	if diskWarning != "" {
		result.Warnings = append(result.Warnings, diskWarning)
		slog.Warn("Disk limit exceeded but proceeding", "warning", diskWarning)
	}

	// Step 4: Handle case where we cannot pull
	if !canPull {
		e.markMissingModels(result, needsPull)
		return result, nil
	}

	// Step 5: Pull missing models
	e.pullMissingModels(ctx, result, needsPull)

	return result, nil
}

// GetRequiredModels returns the list of models that need to be available.
//
// # Description
//
// Returns a copy of the configured list of required models. This list
// is built during construction based on the ModelEnsurerConfig and
// does not change after construction.
//
// # Outputs
//
//   - []RequiredModel: Copy of required model specifications
//
// # Examples
//
//	models := ensurer.GetRequiredModels()
//	for _, m := range models {
//	    fmt.Printf("Model: %s\n", m.Name)
//	    fmt.Printf("  Purpose: %s\n", m.Purpose)
//	    fmt.Printf("  Required: %v\n", m.Required)
//	    fmt.Printf("  Env override: %s\n", m.EnvVar)
//	}
//
// # Limitations
//
//   - Returns a copy, so modifications don't affect the ensurer
//
// # Assumptions
//
//   - None - this is a pure getter with no side effects
func (e *DefaultModelEnsurer) GetRequiredModels() []RequiredModel {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]RequiredModel, len(e.requiredModels))
	copy(result, e.requiredModels)
	return result
}

// SetProgressCallback sets the callback for pull progress updates.
//
// # Description
//
// Configures a callback function that receives progress updates during
// model downloads. The callback is invoked by OllamaModelManager.PullModel
// as data is received.
//
// Pass nil to disable progress reporting.
//
// # Inputs
//
//   - callback: Function receiving (status, completed, total) or nil
//
// # Examples
//
//	// Enable progress display
//	ensurer.SetProgressCallback(func(status string, completed, total int64) {
//	    if total > 0 {
//	        pct := float64(completed) / float64(total) * 100
//	        fmt.Printf("\r  %s: %.1f%%", status, pct)
//	    } else {
//	        fmt.Printf("\r  %s...", status)
//	    }
//	})
//
//	// Disable progress display
//	ensurer.SetProgressCallback(nil)
//
// # Limitations
//
//   - Callback is invoked synchronously during pull
//   - Long-running callbacks will slow down the download
//
// # Assumptions
//
//   - Callback is safe to call from any goroutine
//   - Callback does not block for extended periods
func (e *DefaultModelEnsurer) SetProgressCallback(callback PullProgressCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progressCallback = callback
}

// -----------------------------------------------------------------------------
// Private Methods - Single Responsibility
// -----------------------------------------------------------------------------

// initializeResult creates a new empty ModelEnsureResult.
//
// # Description
//
// Creates and returns a new ModelEnsureResult with all slices initialized
// to empty (not nil) and CanProceed set to true.
//
// # Outputs
//
//   - *ModelEnsureResult: Initialized result struct
func (e *DefaultModelEnsurer) initializeResult() *ModelEnsureResult {
	return &ModelEnsureResult{
		ModelsChecked: []ModelStatus{},
		ModelsPulled:  []string{},
		ModelsSkipped: []string{},
		ModelsMissing: []string{},
		CanProceed:    true,
		Warnings:      []string{},
	}
}

// checkAllModels checks availability of all required models.
//
// # Description
//
// Iterates through all required models and checks each one's availability.
// Populates the result's ModelsChecked and ModelsSkipped lists.
// Returns the list of models that need to be pulled.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: Result struct to populate with model statuses
//
// # Outputs
//
//   - []RequiredModel: Models that need to be pulled
//   - error: Non-nil if model checking fails
func (e *DefaultModelEnsurer) checkAllModels(ctx context.Context, result *ModelEnsureResult) ([]RequiredModel, error) {
	needsPull := []RequiredModel{}

	for _, model := range e.requiredModels {
		status, err := e.checkSingleModel(ctx, model)
		if err != nil {
			return nil, fmt.Errorf("failed to check model %s: %w", model.Name, err)
		}

		result.ModelsChecked = append(result.ModelsChecked, status)

		if status.Available {
			if status.IsCustom {
				result.ModelsSkipped = append(result.ModelsSkipped, model.Name)
			}
		} else {
			needsPull = append(needsPull, model)
		}
	}

	return needsPull, nil
}

// checkSingleModel checks if a single model exists and gathers its metadata.
//
// # Description
//
// Checks if a specific model is available locally using the model manager.
// If available, also checks if it's a custom model and gets its size.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - model: The model specification to check
//
// # Outputs
//
//   - ModelStatus: Status including availability, custom flag, size
//   - error: Non-nil if the check operation fails
func (e *DefaultModelEnsurer) checkSingleModel(ctx context.Context, model RequiredModel) (ModelStatus, error) {
	status := ModelStatus{
		Name: model.Name,
	}

	exists, err := e.modelManager.HasModel(ctx, model.Name)
	if err != nil {
		return status, fmt.Errorf("HasModel failed: %w", err)
	}
	status.Available = exists

	if exists {
		status.IsCustom = e.checkIfCustomModel(ctx, model.Name)
		status.Size = e.getModelSize(ctx, model.Name)
	}

	return status, nil
}

// checkIfCustomModel determines if a model is user-created.
//
// # Description
//
// Checks if a model was created locally (has template field).
// Logs but does not propagate errors - returns false on error.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - modelName: Name of the model to check
//
// # Outputs
//
//   - bool: True if model is custom/local, false otherwise or on error
func (e *DefaultModelEnsurer) checkIfCustomModel(ctx context.Context, modelName string) bool {
	isCustom, err := e.modelManager.IsCustomModel(ctx, modelName)
	if err != nil {
		slog.Debug("Could not determine if model is custom",
			"model", modelName, "error", err)
		return false
	}
	return isCustom
}

// getModelSize retrieves the size of a model.
//
// # Description
//
// Gets the model size in bytes. Returns 0 on error rather than
// propagating the error, since size is informational.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - modelName: Name of the model
//
// # Outputs
//
//   - int64: Model size in bytes, or 0 if unknown
func (e *DefaultModelEnsurer) getModelSize(ctx context.Context, modelName string) int64 {
	size, err := e.modelManager.GetModelSize(ctx, modelName)
	if err != nil {
		return 0
	}
	return size
}

// performPreflightChecks verifies network and disk before pulling.
//
// # Description
//
// Performs pre-flight checks before attempting to pull models:
//  1. Checks network connectivity to Ollama registry
//  2. If network fails, checks if offline operation is possible
//  3. Calculates required disk space for all models
//  4. Verifies sufficient disk space is available
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - modelsToPull: List of models that need to be downloaded
//
// # Outputs
//
//   - canPull: True if pulling should proceed
//   - offlineMode: True if operating without network
//   - diskWarning: Non-empty if disk limit exceeded (soft warning)
//   - error: Non-nil for fatal errors (physical disk full, etc.)
func (e *DefaultModelEnsurer) performPreflightChecks(ctx context.Context, modelsToPull []RequiredModel) (canPull bool, offlineMode bool, diskWarning string, err error) {
	networkOK := e.checkNetwork(ctx)

	if !networkOK {
		canOperate := e.checkOfflineCapability(modelsToPull)
		if canOperate {
			return false, true, "", nil
		}
		return false, false, "", fmt.Errorf("network unavailable and required models not cached locally")
	}

	diskWarning, err = e.checkDiskSpace(ctx, modelsToPull)
	if err != nil {
		return false, false, "", err
	}

	return true, false, diskWarning, nil
}

// checkNetwork verifies network connectivity.
//
// # Description
//
// Checks if the Ollama registry is reachable. Returns true if
// network is available, false otherwise. Does not return errors
// since network unavailability is a recoverable state.
//
// # Inputs
//
//   - ctx: Context for cancellation
//
// # Outputs
//
//   - bool: True if network is available
func (e *DefaultModelEnsurer) checkNetwork(ctx context.Context) bool {
	err := e.systemChecker.CheckNetworkConnectivity(ctx)
	if err != nil {
		slog.Debug("Network check failed", "error", err)
		return false
	}
	return true
}

// checkOfflineCapability determines if offline operation is possible.
//
// # Description
//
// Checks if all models in the list can be operated without network.
// Uses SystemChecker.CanOperateOffline to determine this.
//
// # Inputs
//
//   - models: List of models to check
//
// # Outputs
//
//   - bool: True if offline operation is possible
func (e *DefaultModelEnsurer) checkOfflineCapability(models []RequiredModel) bool {
	modelNames := make([]string, len(models))
	for i, m := range models {
		modelNames[i] = m.Name
	}
	return e.systemChecker.CanOperateOffline(modelNames)
}

// checkDiskSpace verifies sufficient disk space for downloads.
//
// # Description
//
// Calculates the total required disk space for all models to pull
// and verifies that sufficient space is available. Differentiates between:
//   - Physical disk space insufficient: hard fail
//   - Configured limit exceeded: soft warning (returns warning string, proceeds)
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - models: List of models to download
//
// # Outputs
//
//   - warning: Non-empty if configured limit exceeded but physical space OK
//   - error: Non-nil only if physical disk space is insufficient
func (e *DefaultModelEnsurer) checkDiskSpace(ctx context.Context, models []RequiredModel) (warning string, err error) {
	if len(models) == 0 {
		return "", nil
	}

	totalSize := e.calculateTotalSize(ctx, models)
	if totalSize <= 0 {
		return "", nil
	}

	// Check physical disk space first (hard fail)
	available, err := e.systemChecker.GetAvailableDiskSpace()
	if err != nil {
		// Can't determine space - proceed with warning
		return "Could not verify disk space availability", nil
	}

	if available < totalSize {
		return "", fmt.Errorf("insufficient physical disk space: need %s, have %s available",
			formatBytesForHumans(totalSize), formatBytesForHumans(available))
	}

	// Check configured limit (soft warning only - don't block)
	if e.diskLimitBytes > 0 {
		currentUsage, _ := e.systemChecker.GetAvailableDiskSpace()
		// Get actual usage from directory size
		storagePath := e.systemChecker.GetModelStoragePath()
		if storagePath != "" {
			if usage, err := getDirectorySizeHelper(storagePath); err == nil {
				currentUsage = usage
			}
		}

		if currentUsage+totalSize > e.diskLimitBytes {
			warning = fmt.Sprintf(
				"Download will exceed configured limit (%s): current usage %s + download %s > limit %s. "+
					"Proceeding anyway - adjust model_management.disk_limit_gb if needed.",
				formatBytesForHumans(e.diskLimitBytes),
				formatBytesForHumans(currentUsage),
				formatBytesForHumans(totalSize),
				formatBytesForHumans(e.diskLimitBytes),
			)
		}
	}

	return warning, nil
}

// formatBytesForHumans formats bytes as human-readable string.
func formatBytesForHumans(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// getDirectorySizeHelper calculates directory size (helper to avoid import cycle).
func getDirectorySizeHelper(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// calculateTotalSize sums the size of all models to download.
//
// # Description
//
// Calculates the total bytes required to download all specified models.
// Uses fallback size if actual size cannot be determined.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - models: List of models to size
//
// # Outputs
//
//   - int64: Total size in bytes
func (e *DefaultModelEnsurer) calculateTotalSize(ctx context.Context, models []RequiredModel) int64 {
	var total int64
	for _, model := range models {
		size, err := e.modelManager.GetModelSize(ctx, model.Name)
		if err != nil {
			slog.Debug("Could not get model size, using fallback",
				"model", model.Name, "fallback", FallbackModelSizeBytes)
			size = FallbackModelSizeBytes
		}
		total += size
	}
	return total
}

// markMissingModels updates result for models that cannot be obtained.
//
// # Description
//
// For each model that needs pulling but cannot be pulled (offline, etc.),
// updates the result to mark required models as missing and optional
// models as warnings.
//
// # Inputs
//
//   - result: Result struct to update
//   - models: Models that could not be obtained
func (e *DefaultModelEnsurer) markMissingModels(result *ModelEnsureResult, models []RequiredModel) {
	for _, model := range models {
		if model.Required {
			result.CanProceed = false
			result.ModelsMissing = append(result.ModelsMissing, model.Name)
		} else {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("Optional model %s not available", model.Name))
		}
	}
}

// pullMissingModels downloads each missing model.
//
// # Description
//
// Iterates through models that need pulling and attempts to download each.
// Updates the result with success/failure status for each model.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: Result struct to update
//   - models: Models to download
func (e *DefaultModelEnsurer) pullMissingModels(ctx context.Context, result *ModelEnsureResult, models []RequiredModel) {
	for _, model := range models {
		err := e.pullSingleModel(ctx, model)
		e.updateResultAfterPull(result, model, err)
	}
}

// pullSingleModel downloads a single model.
//
// # Description
//
// Downloads one model using the model manager, passing through
// the configured progress callback.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - model: Model to download
//
// # Outputs
//
//   - error: Non-nil if pull fails
func (e *DefaultModelEnsurer) pullSingleModel(ctx context.Context, model RequiredModel) error {
	e.mu.RLock()
	callback := e.progressCallback
	e.mu.RUnlock()

	slog.Info("Pulling model", "model", model.Name, "purpose", model.Purpose.String())

	err := e.modelManager.PullModel(ctx, model.Name, callback)
	if err != nil {
		if modelErr, ok := err.(*ModelError); ok {
			return fmt.Errorf("pull failed: %s", modelErr.FullError())
		}
		return fmt.Errorf("pull failed: %w", err)
	}

	slog.Info("Model pulled successfully", "model", model.Name)
	return nil
}

// updateResultAfterPull updates result based on pull outcome.
//
// # Description
//
// Updates the ModelEnsureResult after a pull attempt. On success,
// adds to ModelsPulled and updates status. On failure, handles
// required vs optional models differently.
//
// # Inputs
//
//   - result: Result struct to update
//   - model: Model that was pulled (or failed)
//   - err: Error from pull attempt (nil on success)
func (e *DefaultModelEnsurer) updateResultAfterPull(result *ModelEnsureResult, model RequiredModel, err error) {
	if err != nil {
		e.handlePullFailure(result, model, err)
	} else {
		e.handlePullSuccess(result, model)
	}
}

// handlePullSuccess updates result for successful pull.
//
// # Description
//
// Updates the result to reflect a successful model download.
// Adds to ModelsPulled list and updates the model's status.
//
// # Inputs
//
//   - result: Result struct to update
//   - model: Model that was successfully pulled
func (e *DefaultModelEnsurer) handlePullSuccess(result *ModelEnsureResult, model RequiredModel) {
	result.ModelsPulled = append(result.ModelsPulled, model.Name)

	for i := range result.ModelsChecked {
		if result.ModelsChecked[i].Name == model.Name {
			result.ModelsChecked[i].Available = true
			result.ModelsChecked[i].WasPulled = true
			break
		}
	}
}

// handlePullFailure updates result for failed pull.
//
// # Description
//
// Updates the result to reflect a failed model download. Required
// models cause CanProceed=false, optional models add warnings.
//
// # Inputs
//
//   - result: Result struct to update
//   - model: Model that failed to pull
//   - err: Error from the failed pull
func (e *DefaultModelEnsurer) handlePullFailure(result *ModelEnsureResult, model RequiredModel, err error) {
	if model.Required {
		result.CanProceed = false
		result.ModelsMissing = append(result.ModelsMissing, model.Name)
	} else {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("Failed to pull optional model %s: %v", model.Name, err))
	}

	for i := range result.ModelsChecked {
		if result.ModelsChecked[i].Name == model.Name {
			result.ModelsChecked[i].Error = err
			break
		}
	}
}
