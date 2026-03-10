// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"fmt"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/config"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/diagnostics"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/health"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/compose"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/process"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/util"
)

// =============================================================================
// INTERFACES
// =============================================================================

// StackFactory creates StackManager instances with all required dependencies.
//
// This interface enables dependency injection for testing - production code
// uses DefaultStackFactory, while tests can provide mock implementations.
type StackFactory interface {
	// CreateStackManager builds a fully configured StackManager.
	//
	// # Description
	//
	// Wires together all components required by StackManager: ProcessManager,
	// InfrastructureManager, SecretsManager, CachePathResolver, ComposeExecutor,
	// HealthChecker, ModelEnsurer, ProfileResolver, and DiagnosticsCollector.
	//
	// # Inputs
	//
	//   - cfg: The global Aleutian configuration containing all settings.
	//   - stackDir: Directory containing stack files (compose files, overrides).
	//   - cliVersion: CLI version string for diagnostics and telemetry.
	//
	// # Outputs
	//
	//   - StackManager: Ready-to-use stack manager with all dependencies wired.
	//   - error: Non-nil if any dependency creation fails.
	CreateStackManager(cfg *config.AleutianConfig, stackDir, cliVersion string) (StackManager, error)
}

// =============================================================================
// STRUCTS
// =============================================================================

// DefaultStackFactory is the production implementation of StackFactory.
//
// It creates real implementations of all StackManager dependencies including
// ProcessManager, InfrastructureManager, ComposeExecutor, HealthChecker, etc.
type DefaultStackFactory struct{}

// =============================================================================
// METHODS
// =============================================================================

// NewDefaultStackFactory creates a new DefaultStackFactory instance.
//
// # Description
//
// Returns a factory that produces StackManagers with real production
// dependencies. Use this in production code; use mock factories in tests.
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - *DefaultStackFactory: A factory instance ready to create StackManagers.
//
// # Examples
//
//	factory := NewDefaultStackFactory()
//	mgr, err := factory.CreateStackManager(&config.Global, stackDir, "0.4.0")
//
// # Limitations
//
//   - Creates all dependencies even if only some are needed.
//   - Not suitable for unit tests; use mock factories instead.
//
// # Assumptions
//
//   - None.
func NewDefaultStackFactory() *DefaultStackFactory {
	return &DefaultStackFactory{}
}

// CreateStackManager builds a fully configured StackManager with production dependencies.
//
// # Description
//
// This method wires together all components required by StackManager in the
// correct order, respecting dependency relationships:
//
//	ProcessManager -> InfrastructureManager -> SecretsManager ->
//	CachePathResolver -> ComposeExecutor -> HealthChecker ->
//	ModelEnsurer -> ProfileResolver -> DiagnosticsCollector -> StackManager
//
// # Inputs
//
//   - cfg: The global Aleutian configuration containing:
//   - Machine settings (ID, drives)
//   - Model backend settings (type, Ollama config)
//   - Secrets configuration
//   - Profile settings
//   - stackDir: The directory containing stack files (compose files, overrides).
//   - cliVersion: The CLI version string for diagnostics and telemetry.
//
// # Outputs
//
//   - StackManager: Ready-to-use stack manager with all dependencies wired.
//   - error: Non-nil if any dependency creation fails, with wrapped context.
//
// # Examples
//
//	factory := NewDefaultStackFactory()
//	mgr, err := factory.CreateStackManager(&config.Global, "/path/to/stack", "0.4.0")
//	if err != nil {
//	    log.Fatalf("Failed to create stack manager: %v", err)
//	}
//	err = mgr.Start(ctx, opts)
//
// # Limitations
//
//   - Creates all dependencies even if only some operations are needed.
//   - Not suitable for unit tests; use mock implementations instead.
//   - ModelEnsurer is only created if backend type is "ollama".
//
// # Assumptions
//
//   - Config is valid and loaded.
//   - Stack directory exists and is accessible.
//   - External services (Podman, Ollama) are installed if needed.
func (f *DefaultStackFactory) CreateStackManager(cfg *config.AleutianConfig, stackDir, cliVersion string) (StackManager, error) {
	proc := f.createProcessManager()
	prompter := f.createUserPrompter()

	diagnosticsCollector, err := f.createDiagnosticsCollector(cliVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to create diagnostics collector: %w", err)
	}

	metrics := f.createDiagnosticsMetrics()
	infraMgr := f.createInfrastructureManager(proc, prompter, metrics)
	secretsMgr := f.createSecretsManager(cfg, metrics)
	cacheMgr := f.createCachePathResolver(cfg, stackDir, proc, prompter)

	composeMgr, err := f.createComposeExecutor(cfg, stackDir, proc)
	if err != nil {
		return nil, fmt.Errorf("failed to create compose executor: %w", err)
	}

	healthMgr := f.createHealthChecker(proc)
	profileMgr := f.createProfileResolver(cfg, proc)
	modelMgr := f.createModelEnsurer(cfg)

	stackMgr, err := NewDefaultStackManager(
		infraMgr,
		secretsMgr,
		cacheMgr,
		composeMgr,
		healthMgr,
		modelMgr,
		profileMgr,
		diagnosticsCollector,
		cfg,
		stackDir,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create stack manager: %w", err)
	}

	return stackMgr, nil
}

// createProcessManager creates a ProcessManager for command execution.
//
// # Description
//
// Creates the foundation component for all external command execution.
// ProcessManager is used by most other components to run podman, docker-compose, etc.
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - process.Manager: Ready-to-use process manager.
//
// # Limitations
//
//   - Returns production implementation only.
//
// # Assumptions
//
//   - None.
func (f *DefaultStackFactory) createProcessManager() process.Manager {
	return process.NewDefaultManager()
}

// createUserPrompter creates a UserPrompter for interactive user input.
//
// # Description
//
// Creates a prompter for interactive terminal prompts (confirmations, secrets, etc.).
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - util.UserPrompter: Ready-to-use user prompter.
//
// # Limitations
//
//   - Returns production implementation that reads from stdin.
//
// # Assumptions
//
//   - Running in an interactive terminal environment.
func (f *DefaultStackFactory) createUserPrompter() util.UserPrompter {
	return util.NewInteractivePrompter()
}

// createDiagnosticsCollector creates a DiagnosticsCollector for error tracking.
//
// # Description
//
// Creates a collector for gathering system diagnostics during error conditions.
// Includes CLI version for correlation with known issues.
//
// # Inputs
//
//   - cliVersion: CLI version string for diagnostics correlation.
//
// # Outputs
//
//   - diagnostics.DiagnosticsCollector: Ready-to-use diagnostics collector.
//   - error: Non-nil if collector creation fails.
//
// # Limitations
//
//   - Requires write access to diagnostics storage location.
//
// # Assumptions
//
//   - cliVersion is a valid semver string.
func (f *DefaultStackFactory) createDiagnosticsCollector(cliVersion string) (diagnostics.DiagnosticsCollector, error) {
	return diagnostics.NewDefaultDiagnosticsCollector(cliVersion)
}

// createDiagnosticsMetrics creates a DiagnosticsMetrics for metrics recording.
//
// # Description
//
// Creates a metrics recorder for tracking operational metrics. Currently returns
// a no-op implementation; can be replaced with Prometheus exporter in production.
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - diagnostics.DiagnosticsMetrics: Ready-to-use metrics recorder.
//
// # Limitations
//
//   - Currently returns no-op implementation.
//
// # Assumptions
//
//   - None.
func (f *DefaultStackFactory) createDiagnosticsMetrics() diagnostics.DiagnosticsMetrics {
	return diagnostics.NewNoOpDiagnosticsMetrics()
}

// createInfrastructureManager creates an InfrastructureManager for Podman lifecycle.
//
// # Description
//
// Creates manager for Podman machine lifecycle operations (init, start, stop,
// mount verification, conflict detection).
//
// # Inputs
//
//   - proc: process.Manager for executing podman commands.
//   - prompter: UserPrompter for interactive confirmations.
//   - metrics: DiagnosticsMetrics for recording infrastructure metrics.
//
// # Outputs
//
//   - infra.InfrastructureManager: Ready-to-use infrastructure manager.
//
// # Limitations
//
//   - macOS-specific; Podman machine not required on Linux.
//
// # Assumptions
//
//   - Podman is installed and available in PATH.
func (f *DefaultStackFactory) createInfrastructureManager(
	proc process.Manager,
	prompter util.UserPrompter,
	metrics diagnostics.DiagnosticsMetrics,
) infra.InfrastructureManager {
	return infra.NewDefaultInfrastructureManager(proc, prompter, metrics)
}

// createSecretsManager creates a SecretsManager for API key provisioning.
//
// # Description
//
// Creates manager for secure storage and retrieval of API keys and secrets.
// Uses system keychain on macOS, encrypted file storage on other platforms.
//
// # Inputs
//
//   - cfg: Configuration containing secrets backend settings.
//   - metrics: DiagnosticsMetrics for recording secret access metrics.
//
// # Outputs
//
//   - SecretsManager: Ready-to-use secrets manager.
//
// # Limitations
//
//   - Keychain access may require user authorization on first use.
//
// # Assumptions
//
//   - Config secrets settings are valid.
func (f *DefaultStackFactory) createSecretsManager(
	cfg *config.AleutianConfig,
	metrics diagnostics.DiagnosticsMetrics,
) SecretsManager {
	return NewDefaultSecretsManager(cfg.Secrets, metrics)
}

// createCachePathResolver creates a CachePathResolver for model cache paths.
//
// # Description
//
// Creates resolver for determining model cache locations, supporting multiple
// drives and container mount verification.
//
// # Inputs
//
//   - cfg: Configuration containing machine and drive settings.
//   - stackDir: Stack directory for default cache location.
//   - proc: process.Manager for container mount verification.
//   - prompter: UserPrompter for drive selection prompts.
//
// # Outputs
//
//   - CachePathResolver: Ready-to-use cache path resolver.
//
// # Limitations
//
//   - Drive selection may be needed on first run.
//
// # Assumptions
//
//   - Stack directory exists and is writable.
func (f *DefaultStackFactory) createCachePathResolver(
	cfg *config.AleutianConfig,
	stackDir string,
	proc process.Manager,
	prompter util.UserPrompter,
) CachePathResolver {
	cacheConfig := CacheConfig{
		StackDir:         stackDir,
		ConfiguredDrives: cfg.Machine.Drives,
		MachineName:      cfg.Machine.Id,
	}
	return NewDefaultCachePathResolver(cacheConfig, proc, prompter)
}

// createComposeExecutor creates a ComposeExecutor for container orchestration.
//
// # Description
//
// Creates executor for running podman-compose operations (up, down, logs, ps).
// Includes extension compose files based on configuration:
//   - podman-compose.observability.yml: Prometheus + Grafana (enabled by default)
//   - podman-compose.timeseries.yml: InfluxDB (when ForecastMode is "sapheneia")
//
// # Inputs
//
//   - cfg: Configuration containing ForecastMode and Observability settings.
//   - stackDir: Directory containing compose files.
//   - proc: process.Manager for executing compose commands.
//
// # Outputs
//
//   - compose.ComposeExecutor: Ready-to-use compose executor.
//   - error: Non-nil if executor creation fails.
//
// # Limitations
//
//   - Requires podman-compose or docker-compose in PATH.
//
// # Assumptions
//
//   - Stack directory contains valid compose files.
func (f *DefaultStackFactory) createComposeExecutor(
	cfg *config.AleutianConfig,
	stackDir string,
	proc process.Manager,
) (compose.ComposeExecutor, error) {
	composeConfig := compose.ComposeConfig{
		StackDir:    stackDir,
		ProjectName: "aleutian",
	}

	// Include observability stack (Prometheus + Grafana) by default
	// Users can disable via: observability: { enabled: false }
	if cfg.Observability.IsEnabled() {
		composeConfig.ExtensionFiles = append(composeConfig.ExtensionFiles,
			"podman-compose.observability.yml")
	}

	// When using Sapheneia integration, include the timeseries compose file
	// which provides InfluxDB and data-fetcher services for forecast data
	if cfg.Forecast.Mode == config.ForecastModeSapheneia {
		composeConfig.ExtensionFiles = append(composeConfig.ExtensionFiles,
			"podman-compose.timeseries.yml")
	}

	return compose.NewDefaultComposeExecutor(composeConfig, proc)
}

// createHealthChecker creates a HealthChecker for service health verification.
//
// # Description
//
// Creates checker for verifying service health via HTTP endpoints and
// container status. Supports configurable timeouts and retry logic.
//
// # Inputs
//
//   - proc: process.Manager for container status checks.
//
// # Outputs
//
//   - health.HealthChecker: Ready-to-use health checker.
//
// # Limitations
//
//   - HTTP health checks require services to expose health endpoints.
//
// # Assumptions
//
//   - Services are configured with health check endpoints.
func (f *DefaultStackFactory) createHealthChecker(proc process.Manager) health.HealthChecker {
	healthConfig := health.DefaultHealthCheckerConfig()
	return health.NewDefaultHealthChecker(proc, healthConfig)
}

// createProfileResolver creates a ProfileResolver for hardware-based configuration.
//
// # Description
//
// Creates resolver for detecting hardware capabilities and selecting
// appropriate model profiles (RAM, GPU, etc.).
//
// # Inputs
//
//   - cfg: Configuration containing profile settings and overrides.
//   - proc: process.Manager for hardware detection commands.
//
// # Outputs
//
//   - ProfileResolver: Ready-to-use profile resolver.
//
// # Limitations
//
//   - Hardware detection may not work in all virtualized environments.
//
// # Assumptions
//
//   - Config profile settings are valid.
func (f *DefaultStackFactory) createProfileResolver(
	cfg *config.AleutianConfig,
	proc process.Manager,
) ProfileResolver {
	hardwareDetector := NewDefaultHardwareDetector(proc)
	return NewDefaultProfileResolver(hardwareDetector, cfg.Profiles)
}

// createModelEnsurer creates a ModelEnsurer for model verification.
//
// # Description
//
// Creates ensurer for verifying required AI models are available locally
// or can be pulled. Only created if backend type is "ollama".
//
// # Inputs
//
//   - cfg: Configuration containing model backend settings.
//
// # Outputs
//
//   - ModelEnsurer: Ready-to-use model ensurer, or nil if not using Ollama.
//
// # Limitations
//
//   - Only supports Ollama backend currently.
//   - Returns nil for non-Ollama backends.
//
// # Assumptions
//
//   - Ollama is running if backend type is "ollama".
func (f *DefaultStackFactory) createModelEnsurer(cfg *config.AleutianConfig) ModelEnsurer {
	if cfg.ModelBackend.Type != "ollama" {
		return nil
	}

	modelConfig := ModelEnsurerConfig{
		OllamaBaseURL:       cfg.ModelBackend.Ollama.BaseURL,
		EmbeddingModel:      cfg.ModelBackend.Ollama.EmbeddingModel,
		LLMModel:            cfg.ModelBackend.Ollama.LLMModel,
		ToolRouterModel:     cfg.ModelBackend.Ollama.ToolRouterModel,
		ParamExtractorModel: cfg.ModelBackend.Ollama.ParamExtractorModel,
		DiskLimitGB:         cfg.ModelBackend.Ollama.DiskLimitGB,
		BackendType:         cfg.ModelBackend.Type,
	}
	return NewDefaultModelEnsurer(modelConfig)
}

// =============================================================================
// PACKAGE-LEVEL FACTORY FUNCTION
// =============================================================================

// CreateProductionStackManager creates a StackManager with all production dependencies.
//
// # Description
//
// Convenience function that creates a DefaultStackFactory and uses it to build
// a StackManager. This is the primary entry point for CLI code.
//
// # Inputs
//
//   - cfg: The global Aleutian configuration containing all settings.
//   - stackDir: Directory containing stack files (compose files, overrides).
//   - cliVersion: CLI version string for diagnostics and telemetry.
//
// # Outputs
//
//   - StackManager: Ready-to-use stack manager with all dependencies wired.
//   - error: Non-nil if any dependency creation fails.
//
// # Examples
//
//	mgr, err := CreateProductionStackManager(&config.Global, stackDir, "0.4.0")
//	if err != nil {
//	    log.Fatalf("Failed to create stack manager: %v", err)
//	}
//	err = mgr.Start(ctx, opts)
//
// # Limitations
//
//   - Creates all dependencies even if only some operations are needed.
//   - Not suitable for unit tests; use mock implementations instead.
//
// # Assumptions
//
//   - Config is valid and loaded.
//   - Stack directory exists and is accessible.
func CreateProductionStackManager(cfg *config.AleutianConfig, stackDir, cliVersion string) (StackManager, error) {
	factory := NewDefaultStackFactory()
	return factory.CreateStackManager(cfg, stackDir, cliVersion)
}
