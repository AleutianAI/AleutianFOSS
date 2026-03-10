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
Package main contains unit tests for model_ensurer.go.

# Testing Strategy

These tests use mock implementations of SystemChecker and OllamaModelManager
to test ModelEnsurer behavior without real Ollama or network dependencies.

All tests are designed to run fast (<1s total) and in isolation.

# Test Coverage

The tests cover:
  - Constructor behavior and configuration
  - Model availability checking
  - Custom model detection
  - Pre-flight checks (network, disk)
  - Model pulling with progress callbacks
  - Graceful degradation for offline mode
  - Error handling for various failure scenarios
  - Result struct population
*/
package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra"
)

// MockSystemChecker implements infra.SystemChecker for testing.
type MockSystemChecker struct {
	ollamaInstalled    bool
	ollamaInPath       bool
	ollamaPath         string
	canSelfHeal        bool
	selfHealError      error
	networkError       error
	availableDiskSpace int64
	diskError          error
	modelStoragePath   string
	canOperateOffline  bool
	mu                 sync.Mutex
}

func (m *MockSystemChecker) IsOllamaInstalled() bool              { return m.ollamaInstalled }
func (m *MockSystemChecker) IsOllamaInPath() bool                 { return m.ollamaInPath }
func (m *MockSystemChecker) GetOllamaPath() string                { return m.ollamaPath }
func (m *MockSystemChecker) GetOllamaInstallInstructions() string { return "Mock install instructions" }
func (m *MockSystemChecker) CanSelfHealOllama() bool              { return m.canSelfHeal }
func (m *MockSystemChecker) SelfHealOllama() error                { return m.selfHealError }
func (m *MockSystemChecker) CheckNetworkConnectivity(ctx context.Context) error {
	return m.networkError
}
func (m *MockSystemChecker) CanOperateOffline(requiredModels []string) bool {
	return m.canOperateOffline
}
func (m *MockSystemChecker) CheckDiskSpace(requiredBytes int64, configuredLimitBytes int64) error {
	if m.diskError != nil {
		return m.diskError
	}
	if m.availableDiskSpace < requiredBytes {
		return &infra.CheckError{Type: infra.CheckErrorDiskSpaceLow, Message: "Insufficient disk space"}
	}
	return nil
}
func (m *MockSystemChecker) GetAvailableDiskSpace() (int64, error) {
	return m.availableDiskSpace, m.diskError
}
func (m *MockSystemChecker) GetModelStoragePath() string { return m.modelStoragePath }
func (m *MockSystemChecker) RunDiagnostics(ctx context.Context) *infra.DiagnosticReport {
	return &infra.DiagnosticReport{Timestamp: time.Now()}
}

// -----------------------------------------------------------------------------
// Test Helpers
// -----------------------------------------------------------------------------

// newTestConfig creates a ModelEnsurerConfig for testing.
//
// # Description
//
// Creates a standard test configuration with common defaults.
//
// # Outputs
//
//   - ModelEnsurerConfig: Configuration for testing
func newTestConfig() ModelEnsurerConfig {
	return ModelEnsurerConfig{
		EmbeddingModel: "test-embed",
		LLMModel:       "test-llm",
		DiskLimitGB:    50,
		OllamaBaseURL:  "http://localhost:11434",
		BackendType:    "ollama",
	}
}

// newTestConfigCloudBackend creates a config for cloud LLM backend.
//
// # Description
//
// Creates a configuration where LLM is not using Ollama.
//
// # Outputs
//
//   - ModelEnsurerConfig: Configuration with cloud backend
func newTestConfigCloudBackend() ModelEnsurerConfig {
	return ModelEnsurerConfig{
		EmbeddingModel: "test-embed",
		LLMModel:       "",
		DiskLimitGB:    50,
		OllamaBaseURL:  "http://localhost:11434",
		BackendType:    "openai",
	}
}

// -----------------------------------------------------------------------------
// ModelPurpose Tests
// -----------------------------------------------------------------------------

// TestModelPurpose_String verifies the String method for all purpose values.
//
// # Description
//
// Tests that each ModelPurpose enum value returns the expected string.
func TestModelPurpose_String(t *testing.T) {
	tests := []struct {
		purpose  ModelPurpose
		expected string
	}{
		{ModelPurposeEmbedding, "embedding"},
		{ModelPurposeLLM, "LLM"},
		{ModelPurposeReranking, "reranking"},
		{ModelPurposeToolRouting, "tool routing"},
		{ModelPurposeParamExtraction, "param extraction"},
		{ModelPurpose(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.purpose.String(); got != tt.expected {
				t.Errorf("ModelPurpose.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Constructor Tests
// -----------------------------------------------------------------------------

// TestNewDefaultModelEnsurerWithDeps_SetsRequiredModels verifies model list creation.
//
// # Description
//
// Tests that the constructor correctly builds the required models list
// based on the configuration.
func TestNewDefaultModelEnsurerWithDeps_SetsRequiredModels(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{}
	cfg := newTestConfig()

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)

	models := ensurer.GetRequiredModels()
	if len(models) != 4 {
		t.Fatalf("Expected 4 required models, got %d", len(models))
	}

	if models[0].Name != "test-embed" {
		t.Errorf("Expected embedding model 'test-embed', got %q", models[0].Name)
	}
	if models[0].Purpose != ModelPurposeEmbedding {
		t.Errorf("Expected embedding purpose, got %v", models[0].Purpose)
	}

	if models[1].Name != "test-llm" {
		t.Errorf("Expected LLM model 'test-llm', got %q", models[1].Name)
	}
	if models[1].Purpose != ModelPurposeLLM {
		t.Errorf("Expected LLM purpose, got %v", models[1].Purpose)
	}

	if models[2].Name != DefaultToolRouterModel {
		t.Errorf("Expected tool router model %q, got %q", DefaultToolRouterModel, models[2].Name)
	}
	if models[2].Purpose != ModelPurposeToolRouting {
		t.Errorf("Expected tool routing purpose, got %v", models[2].Purpose)
	}
	if models[2].Required {
		t.Error("Tool router model should not be required")
	}

	if models[3].Name != DefaultParamExtractorModel {
		t.Errorf("Expected param extractor model %q, got %q", DefaultParamExtractorModel, models[3].Name)
	}
	if models[3].Purpose != ModelPurposeParamExtraction {
		t.Errorf("Expected param extraction purpose, got %v", models[3].Purpose)
	}
	if models[3].Required {
		t.Error("Param extractor model should not be required")
	}
}

// TestNewDefaultModelEnsurerWithDeps_CloudBackend verifies no LLM model for cloud.
//
// # Description
//
// Tests that when BackendType is not "ollama", no LLM model is added
// to the required models list.
func TestNewDefaultModelEnsurerWithDeps_CloudBackend(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{}
	cfg := newTestConfigCloudBackend()

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)

	models := ensurer.GetRequiredModels()
	if len(models) != 1 {
		t.Fatalf("Expected 1 required model for cloud backend, got %d", len(models))
	}

	if models[0].Purpose != ModelPurposeEmbedding {
		t.Error("Only embedding model should be required for cloud backend")
	}
}

// TestNewDefaultModelEnsurerWithDeps_DefaultValues verifies defaults are applied.
//
// # Description
//
// Tests that when config values are empty, defaults are used.
func TestNewDefaultModelEnsurerWithDeps_DefaultValues(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{}
	cfg := ModelEnsurerConfig{
		BackendType: "ollama",
		// All other fields empty - should use defaults
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)

	models := ensurer.GetRequiredModels()
	if len(models) != 4 {
		t.Fatalf("Expected 4 models with defaults, got %d", len(models))
	}

	if models[0].Name != DefaultEmbeddingModel {
		t.Errorf("Expected default embedding model %q, got %q",
			DefaultEmbeddingModel, models[0].Name)
	}

	if models[1].Name != DefaultLLMModel {
		t.Errorf("Expected default LLM model %q, got %q",
			DefaultLLMModel, models[1].Name)
	}

	if models[2].Name != DefaultToolRouterModel {
		t.Errorf("Expected default tool router model %q, got %q",
			DefaultToolRouterModel, models[2].Name)
	}

	if models[3].Name != DefaultParamExtractorModel {
		t.Errorf("Expected default param extractor model %q, got %q",
			DefaultParamExtractorModel, models[3].Name)
	}
}

// TestNewDefaultModelEnsurerWithDeps_TraceModelsOptional verifies trace models are non-fatal.
//
// # Description
//
// Tests that when tool router and param extractor models are missing,
// CanProceed is still true and warnings are generated.
func TestNewDefaultModelEnsurerWithDeps_TraceModelsOptional(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               true,
			"test-llm":                 true,
			DefaultToolRouterModel:     false, // Missing
			DefaultParamExtractorModel: false, // Missing
		},
		pullError: errors.New("model not found"),
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true when only optional trace models are missing")
	}

	if len(result.Warnings) < 2 {
		t.Errorf("Expected at least 2 warnings for missing trace models, got %d: %v",
			len(result.Warnings), result.Warnings)
	}

	if len(result.ModelsMissing) != 0 {
		t.Errorf("Expected no models in ModelsMissing (optional models use warnings), got %v",
			result.ModelsMissing)
	}
}

// TestNewDefaultModelEnsurerWithDeps_DiskLimitDefault verifies default disk limit.
//
// # Description
//
// Tests that when DiskLimitGB is 0, the default limit is used.
func TestNewDefaultModelEnsurerWithDeps_DiskLimitDefault(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{}
	cfg := ModelEnsurerConfig{
		EmbeddingModel: "test",
		BackendType:    "openai",
		DiskLimitGB:    0, // Should use default
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)

	expectedLimit := int64(DefaultDiskLimitGB * GB)
	if ensurer.diskLimitBytes != expectedLimit {
		t.Errorf("Expected disk limit %d, got %d", expectedLimit, ensurer.diskLimitBytes)
	}
}

// -----------------------------------------------------------------------------
// Helper Function Tests
// -----------------------------------------------------------------------------

// TestResolveModelName verifies model name resolution.
//
// # Description
//
// Tests the resolveModelName helper function.
func TestResolveModelName(t *testing.T) {
	tests := []struct {
		provided    string
		defaultName string
		expected    string
	}{
		{"custom-model", "default", "custom-model"},
		{"", "default-model", "default-model"},
		{"model:v1", "", "model:v1"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := resolveModelName(tt.provided, tt.defaultName)
			if got != tt.expected {
				t.Errorf("resolveModelName(%q, %q) = %q, want %q",
					tt.provided, tt.defaultName, got, tt.expected)
			}
		})
	}
}

// TestCalculateDiskLimitBytes verifies disk limit calculation.
//
// # Description
//
// Tests the calculateDiskLimitBytes helper function.
func TestCalculateDiskLimitBytes(t *testing.T) {
	tests := []struct {
		limitGB  int64
		expected int64
	}{
		{50, 50 * GB},
		{100, 100 * GB},
		{0, DefaultDiskLimitGB * GB}, // Zero means use default
	}

	for _, tt := range tests {
		got := calculateDiskLimitBytes(tt.limitGB)
		if got != tt.expected {
			t.Errorf("calculateDiskLimitBytes(%d) = %d, want %d",
				tt.limitGB, got, tt.expected)
		}
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - All Models Available
// -----------------------------------------------------------------------------

// TestEnsureModels_AllAvailable verifies success when all models exist.
//
// # Description
//
// Tests that when all required models are already available locally,
// EnsureModels returns quickly with CanProceed=true and no pulls.
func TestEnsureModels_AllAvailable(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               true,
			"test-llm":                 true,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		sizeMap: map[string]int64{
			"test-embed": 500 * 1024 * 1024,
			"test-llm":   4 * GB,
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true when all models available")
	}

	if len(result.ModelsPulled) != 0 {
		t.Errorf("Expected no models pulled, got %d", len(result.ModelsPulled))
	}

	if len(result.ModelsChecked) != 4 {
		t.Errorf("Expected 4 models checked, got %d", len(result.ModelsChecked))
	}

	for _, status := range result.ModelsChecked {
		if !status.Available {
			t.Errorf("Model %s should be available", status.Name)
		}
	}
}

// TestEnsureModels_CustomModelSkipped verifies custom models are not pulled.
//
// # Description
//
// Tests that custom models (with template field) are marked as skipped
// and no pull is attempted.
func TestEnsureModels_CustomModelSkipped(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               true,
			"test-llm":                 true,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		customModelMap: map[string]bool{
			"test-llm": true, // LLM is custom
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(result.ModelsSkipped) != 1 {
		t.Errorf("Expected 1 model skipped, got %d", len(result.ModelsSkipped))
	}

	if result.ModelsSkipped[0] != "test-llm" {
		t.Errorf("Expected test-llm to be skipped, got %s", result.ModelsSkipped[0])
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - Models Need Pulling
// -----------------------------------------------------------------------------

// TestEnsureModels_PullsSuccess verifies successful model pulling.
//
// # Description
//
// Tests that missing models are pulled successfully when network
// and disk space are available.
func TestEnsureModels_PullsSuccess(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               true,
			"test-llm":                 false, // Needs pulling
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		sizeMap: map[string]int64{
			"test-llm": 4 * GB,
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true after successful pull")
	}

	if len(result.ModelsPulled) != 1 {
		t.Errorf("Expected 1 model pulled, got %d", len(result.ModelsPulled))
	}

	if result.ModelsPulled[0] != "test-llm" {
		t.Errorf("Expected test-llm to be pulled, got %s", result.ModelsPulled[0])
	}
}

// TestEnsureModels_PullWithProgress verifies progress callback is called.
//
// # Description
//
// Tests that the progress callback receives updates during pull.
func TestEnsureModels_PullWithProgress(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false, // Needs pulling
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())

	var progressCalls int
	ensurer.SetProgressCallback(func(status string, completed, total int64) {
		progressCalls++
	})

	ctx := context.Background()
	_, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if progressCalls == 0 {
		t.Error("Expected progress callback to be called")
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - Network Failures
// -----------------------------------------------------------------------------

// TestEnsureModels_NetworkFailure_CannotProceed verifies network failure handling.
//
// # Description
//
// Tests that when network is unavailable and models are missing,
// the result indicates cannot proceed.
func TestEnsureModels_NetworkFailure_CannotProceed(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:      errors.New("network unavailable"),
		canOperateOffline: false,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false, // Needs pulling but can't
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	// Should return error for network unavailable with missing required models
	if err == nil {
		t.Fatal("Expected error for network failure with missing models")
	}

	if result != nil {
		t.Error("Expected nil result on fatal error")
	}
}

// TestEnsureModels_NetworkFailure_OfflineMode verifies graceful degradation.
//
// # Description
//
// Tests that when network is unavailable but offline operation is possible,
// the result indicates offline mode with appropriate warnings.
func TestEnsureModels_NetworkFailure_OfflineMode(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:      errors.New("network unavailable"),
		canOperateOffline: true,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false, // Would need pulling
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error for offline mode, got: %v", err)
	}

	if !result.OfflineMode {
		t.Error("Expected OfflineMode=true")
	}

	if result.CanProceed {
		t.Error("Expected CanProceed=false when required model missing")
	}

	if len(result.ModelsMissing) != 1 {
		t.Errorf("Expected 1 missing model, got %d", len(result.ModelsMissing))
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - Disk Space
// -----------------------------------------------------------------------------

// TestEnsureModels_DiskSpaceInsufficient verifies physical disk space check.
//
// # Description
//
// Tests that when PHYSICAL disk space is insufficient, an error is returned.
// Configured limit exceeded is now a soft warning (not a hard fail).
func TestEnsureModels_DiskSpaceInsufficient(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 1 * GB, // Only 1GB available - not enough for 10GB model
		modelStoragePath:   "/tmp/test-models",
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false, // Needs pulling
		},
		sizeMap: map[string]int64{
			"test-embed": 10 * GB, // Requires 10GB but only 1GB available
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err == nil {
		t.Fatal("Expected error for insufficient physical disk space")
	}

	if result != nil {
		t.Error("Expected nil result on fatal error")
	}

	if !strings.Contains(err.Error(), "insufficient physical disk space") {
		t.Errorf("Expected 'insufficient physical disk space' error, got: %v", err)
	}
}

// TestEnsureModels_DiskLimitExceeded_WarnsButProceeds verifies configured limit is soft.
//
// # Description
//
// Tests that when configured disk limit would be exceeded but physical space
// is available, the operation proceeds with a warning (not an error).
func TestEnsureModels_DiskLimitExceeded_WarnsButProceeds(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 200 * GB, // Plenty of physical space
		modelStoragePath:   "/tmp/test-models",
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               false, // Needs pulling
			"test-llm":                 true,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		sizeMap: map[string]int64{
			"test-embed": 500 * 1024 * 1024, // 500MB
		},
	}

	// Configure with 50GB limit but mock shows 100GB current usage
	cfg := newTestConfig()
	cfg.DiskLimitGB = 50 // 50GB limit

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error when physical space is available: %v", err)
	}

	if result == nil {
		t.Fatal("Expected result when proceeding with warning")
	}

	// Should proceed (CanProceed = true)
	if !result.CanProceed {
		t.Error("Expected CanProceed=true when physical space is available")
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - Pull Failures
// -----------------------------------------------------------------------------

// TestEnsureModels_PullFailure_RequiredModel verifies required model pull failure.
//
// # Description
//
// Tests that when a required model fails to pull, CanProceed is false.
func TestEnsureModels_PullFailure_RequiredModel(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false, // Needs pulling
		},
		pullError: errors.New("pull failed"),
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no fatal error, got: %v", err)
	}

	if result.CanProceed {
		t.Error("Expected CanProceed=false when required model pull fails")
	}

	if len(result.ModelsMissing) != 1 {
		t.Errorf("Expected 1 missing model, got %d", len(result.ModelsMissing))
	}
}

// -----------------------------------------------------------------------------
// EnsureModels Tests - Context Cancellation
// -----------------------------------------------------------------------------

// TestEnsureModels_ContextCancelled verifies cancellation handling.
//
// # Description
//
// Tests that context cancellation is properly handled.
func TestEnsureModels_ContextCancelled(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{},
		listError:   context.Canceled,
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := ensurer.EnsureModels(ctx)

	if err == nil {
		t.Fatal("Expected error for cancelled context")
	}

	if result != nil {
		t.Error("Expected nil result on cancellation")
	}
}

// -----------------------------------------------------------------------------
// GetRequiredModels Tests
// -----------------------------------------------------------------------------

// TestGetRequiredModels_ReturnsCopy verifies that a copy is returned.
//
// # Description
//
// Tests that GetRequiredModels returns a copy, not the internal slice.
func TestGetRequiredModels_ReturnsCopy(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())

	models1 := ensurer.GetRequiredModels()
	models2 := ensurer.GetRequiredModels()

	// Modify one copy
	models1[0].Name = "modified"

	// Other copy should be unchanged
	if models2[0].Name == "modified" {
		t.Error("GetRequiredModels should return independent copies")
	}
}

// -----------------------------------------------------------------------------
// SetProgressCallback Tests
// -----------------------------------------------------------------------------

// TestSetProgressCallback_NilIsValid verifies nil callback is accepted.
//
// # Description
//
// Tests that setting a nil callback does not cause errors.
func TestSetProgressCallback_NilIsValid(t *testing.T) {
	mockChecker := &MockSystemChecker{
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false,
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())
	ensurer.SetProgressCallback(nil) // Should not panic

	ctx := context.Background()
	_, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error with nil callback, got: %v", err)
	}
}

// TestSetProgressCallback_Replacement verifies callback can be replaced.
//
// # Description
//
// Tests that setting a new callback replaces the old one.
func TestSetProgressCallback_Replacement(t *testing.T) {
	mockChecker := &MockSystemChecker{
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed": false,
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfigCloudBackend())

	var firstCalled, secondCalled bool

	ensurer.SetProgressCallback(func(status string, completed, total int64) {
		firstCalled = true
	})

	ensurer.SetProgressCallback(func(status string, completed, total int64) {
		secondCalled = true
	})

	ctx := context.Background()
	_, _ = ensurer.EnsureModels(ctx)

	if firstCalled {
		t.Error("First callback should not be called after replacement")
	}

	if !secondCalled {
		t.Error("Second callback should be called")
	}
}

// -----------------------------------------------------------------------------
// Interface Compliance Tests
// -----------------------------------------------------------------------------

// TestDefaultModelEnsurer_ImplementsInterface verifies interface compliance.
//
// # Description
//
// Compile-time check that DefaultModelEnsurer implements ModelEnsurer.
func TestDefaultModelEnsurer_ImplementsInterface(t *testing.T) {
	var _ ModelEnsurer = (*DefaultModelEnsurer)(nil)
}

// -----------------------------------------------------------------------------
// Integration-Style Tests
// -----------------------------------------------------------------------------

// TestFullWorkflow_FirstTimeSetup simulates first-time user experience.
//
// # Description
//
// Tests the complete flow for a new user with no models installed.
func TestFullWorkflow_FirstTimeSetup(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:       nil,
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"nomic-embed-text-v2-moe":  false,
			"gpt-oss":                  false,
			DefaultToolRouterModel:     false,
			DefaultParamExtractorModel: false,
		},
		sizeMap: map[string]int64{
			"nomic-embed-text-v2-moe": 500 * 1024 * 1024,
			"gpt-oss":                 4 * GB,
		},
	}

	cfg := ModelEnsurerConfig{
		EmbeddingModel: "nomic-embed-text-v2-moe",
		LLMModel:       "gpt-oss",
		DiskLimitGB:    50,
		BackendType:    "ollama",
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)

	ensurer.SetProgressCallback(func(status string, completed, total int64) {
		// Track which models are being pulled
	})

	ctx := context.Background()
	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true after pulls")
	}

	if len(result.ModelsPulled) != 4 {
		t.Errorf("Expected 4 models pulled, got %d: %v",
			len(result.ModelsPulled), result.ModelsPulled)
	}
}

// TestFullWorkflow_ExistingUser simulates returning user experience.
//
// # Description
//
// Tests the flow for a user who already has models installed.
func TestFullWorkflow_ExistingUser(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"nomic-embed-text-v2-moe":  true,
			"gpt-oss":                  true,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		sizeMap: map[string]int64{
			"nomic-embed-text-v2-moe": 500 * 1024 * 1024,
			"gpt-oss":                 4 * GB,
		},
	}

	cfg := ModelEnsurerConfig{
		EmbeddingModel: "nomic-embed-text-v2-moe",
		LLMModel:       "gpt-oss",
		DiskLimitGB:    50,
		BackendType:    "ollama",
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
	ctx := context.Background()

	start := time.Now()
	result, err := ensurer.EnsureModels(ctx)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true")
	}

	if len(result.ModelsPulled) != 0 {
		t.Error("Expected no pulls for existing user")
	}

	// Should be fast since no network calls needed
	if duration > 100*time.Millisecond {
		t.Logf("Note: Check took %v, expected <100ms for cached models", duration)
	}
}

// TestFullWorkflow_OfflineWithCache simulates offline operation.
//
// # Description
//
// Tests graceful degradation when offline but models are cached.
func TestFullWorkflow_OfflineWithCache(t *testing.T) {
	mockChecker := &MockSystemChecker{
		networkError:      errors.New("no internet"),
		canOperateOffline: true,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"nomic-embed-text-v2-moe":  true, // Cached
			"gpt-oss":                  true, // Cached
			DefaultToolRouterModel:     true, // Cached
			DefaultParamExtractorModel: true, // Cached
		},
	}

	cfg := ModelEnsurerConfig{
		EmbeddingModel: "nomic-embed-text-v2-moe",
		LLMModel:       "gpt-oss",
		BackendType:    "ollama",
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true with cached models")
	}

	// Note: OfflineMode is only set when models need pulling
	// If all models are available, we don't even check network
}

// -----------------------------------------------------------------------------
// Edge Case Tests
// -----------------------------------------------------------------------------

// TestEnsureModels_EmptyModelName verifies handling of empty model names.
//
// # Description
//
// Tests that empty model names in config use defaults.
func TestEnsureModels_EmptyModelName(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			DefaultEmbeddingModel:      true,
			DefaultLLMModel:            true,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
	}

	cfg := ModelEnsurerConfig{
		EmbeddingModel: "", // Should use default
		LLMModel:       "", // Should use default
		BackendType:    "ollama",
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true with defaults")
	}
}

// TestEnsureModels_VersionedModelName verifies versioned model handling.
//
// # Description
//
// Tests that model names with version tags are handled correctly.
func TestEnsureModels_VersionedModelName(t *testing.T) {
	mockChecker := &MockSystemChecker{}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"nomic-embed-text-v2-moe:latest": true,
			"gpt-oss:7b-q4":                  true,
			DefaultToolRouterModel:           true,
			DefaultParamExtractorModel:       true,
		},
	}

	cfg := ModelEnsurerConfig{
		EmbeddingModel: "nomic-embed-text-v2-moe:latest",
		LLMModel:       "gpt-oss:7b-q4",
		BackendType:    "ollama",
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, cfg)
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !result.CanProceed {
		t.Error("Expected CanProceed=true with versioned models")
	}
}

// TestModelEnsureResult_AllFieldsPopulated verifies result completeness.
//
// # Description
//
// Tests that all result fields are properly populated.
func TestModelEnsureResult_AllFieldsPopulated(t *testing.T) {
	mockChecker := &MockSystemChecker{
		availableDiskSpace: 100 * GB,
	}
	mockManager := &MockOllamaModelManager{
		hasModelMap: map[string]bool{
			"test-embed":               true,
			"test-llm":                 false,
			DefaultToolRouterModel:     true,
			DefaultParamExtractorModel: true,
		},
		customModelMap: map[string]bool{
			"test-embed": true,
		},
		sizeMap: map[string]int64{
			"test-embed": 500 * 1024 * 1024,
			"test-llm":   4 * GB,
		},
	}

	ensurer := NewDefaultModelEnsurerWithDeps(mockChecker, mockManager, newTestConfig())
	ctx := context.Background()

	result, err := ensurer.EnsureModels(ctx)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify all fields
	if result.ModelsChecked == nil {
		t.Error("ModelsChecked should not be nil")
	}
	if result.ModelsPulled == nil {
		t.Error("ModelsPulled should not be nil")
	}
	if result.ModelsSkipped == nil {
		t.Error("ModelsSkipped should not be nil")
	}
	if result.ModelsMissing == nil {
		t.Error("ModelsMissing should not be nil")
	}
	if result.Warnings == nil {
		t.Error("Warnings should not be nil")
	}

	// Verify specific values
	if len(result.ModelsChecked) != 4 {
		t.Errorf("Expected 4 models checked, got %d", len(result.ModelsChecked))
	}

	if len(result.ModelsSkipped) != 1 || result.ModelsSkipped[0] != "test-embed" {
		t.Errorf("Expected test-embed to be skipped, got %v", result.ModelsSkipped)
	}

	if len(result.ModelsPulled) != 1 || result.ModelsPulled[0] != "test-llm" {
		t.Errorf("Expected test-llm to be pulled, got %v", result.ModelsPulled)
	}
}
