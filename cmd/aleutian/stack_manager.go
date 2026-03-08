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
Package main provides StackManager for orchestrating Aleutian stack lifecycle.

StackManager is the primary orchestrator that coordinates all stack operations:
infrastructure provisioning, secrets management, model verification, profile
resolution, container orchestration, and health checking.

# Architecture

StackManager sits at the top of the dependency hierarchy:

	┌─────────────────────────────────────────────────────────────────┐
	│                        StackManager                             │
	│  (Orchestrates startup, shutdown, status, logs)                 │
	├─────────────────────────────────────────────────────────────────┤
	│                                                                 │
	│  Start() sequence:                                              │
	│    1. InfrastructureManager.EnsureReady()   // Podman machine   │
	│    2. ModelEnsurer.EnsureModels()           // Ollama models    │
	│    3. SecretsManager.EnsureExists()         // API keys         │
	│    4. CachePathResolver.Resolve()           // Model cache      │
	│    5. ProfileResolver.Resolve()             // Environment vars │
	│    6. ComposeExecutor.Up()                  // Containers       │
	│    7. HealthChecker.WaitForServices()       // Health check     │
	│                                                                 │
	└─────────────────────────────────────────────────────────────────┘

# Design Principles

  - Dependency Injection: All operations go through injected interfaces
  - Single Responsibility: Each dependency handles one concern
  - Testability: Full mock support for all dependencies
  - Error Context: Errors are wrapped with diagnostic information
  - Graceful Degradation: ModelEnsurer is optional (nil-safe)

# Phase Integration

This component integrates all previous refactoring phases:
  - Phase 0: Config (config package)
  - Phase 1: ProcessManager (used by dependencies)
  - Phase 2: UserPrompter (used by InfrastructureManager)
  - Phase 3: DiagnosticsCollector
  - Phase 4: ProfileResolver
  - Phase 5: InfrastructureManager
  - Phase 6: SecretsManager
  - Phase 7: CachePathResolver
  - Phase 8: ComposeExecutor
  - Phase 9: HealthChecker
  - ModelEnsurer (already implemented)

# Thread Safety

StackManager is safe for concurrent use. However, only one Start/Stop/Destroy
operation should be in progress at a time. Concurrent operations are serialized
via mutex.

# Usage

	// Create all dependencies
	proc := NewDefaultProcessManager()
	infra := NewDefaultInfrastructureManager(proc, prompter, metrics)
	secrets := NewDefaultSecretsManager(proc, config.Global.Secrets.UseEnv)
	cache := NewDefaultCachePathResolver(proc, prompter)
	compose, _ := NewDefaultComposeExecutor(composeCfg, proc)
	health := NewDefaultHealthChecker(proc, healthCfg)
	models := NewDefaultModelEnsurer(modelCfg)
	profile := NewDefaultProfileResolver(proc, customProfiles)
	diagnostics := NewDefaultDiagnosticsCollector(proc, formatter, storage)

	// Create and use StackManager
	mgr := NewDefaultStackManager(
	    infra, secrets, cache, compose, health,
	    models, profile, diagnostics, config.Global,
	)

	err := mgr.Start(ctx, StartOptions{Profile: "performance"})
	if err != nil {
	    log.Fatal(err)
	}
*/
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/config"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/diagnostics"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/health"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/compose"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/util"
)

// =============================================================================
// Error Definitions
// =============================================================================

var (
	// ErrStackNotRunning is returned when an operation requires a running stack.
	ErrStackNotRunning = errors.New("stack is not running")

	// ErrStackAlreadyRunning is returned when trying to start an already running stack.
	ErrStackAlreadyRunning = errors.New("stack is already running")

	// ErrInfrastructureNotReady is returned when infrastructure setup fails.
	ErrInfrastructureNotReady = errors.New("infrastructure not ready")

	// ErrSecretsNotReady is returned when secrets cannot be provisioned.
	ErrSecretsNotReady = errors.New("secrets not ready")

	// ErrModelsNotReady is returned when required models are not available.
	ErrModelsNotReady = errors.New("models not ready")

	// ErrCacheNotReady is returned when model cache cannot be resolved.
	ErrCacheNotReady = errors.New("cache not ready")

	// ErrProfileResolutionFailed is returned when profile cannot be determined.
	ErrProfileResolutionFailed = errors.New("profile resolution failed")

	// ErrComposeUpFailed is returned when container startup fails.
	ErrComposeUpFailed = errors.New("compose up failed")

	// ErrServicesUnhealthy is returned when services fail health checks.
	ErrServicesUnhealthy = errors.New("services unhealthy")

	// ErrNilDependency is returned when a required dependency is nil.
	ErrNilDependency = errors.New("required dependency is nil")

	// ErrDestroyPartial is returned when destroy completes with partial failures.
	ErrDestroyPartial = errors.New("destroy completed with partial failures")

	// ErrInvalidServiceName is returned when a service name contains invalid characters.
	ErrInvalidServiceName = errors.New("invalid service name")

	// ErrVerificationFailed is returned when post-operation verification fails.
	ErrVerificationFailed = errors.New("operation verification failed")

	// ErrPanicRecovered is returned when a panic was recovered during an operation.
	ErrPanicRecovered = errors.New("panic recovered during operation")
)

// Compile-time assertions to ensure sentinel errors implement error interface.
var (
	_ error = ErrStackNotRunning
	_ error = ErrStackAlreadyRunning
	_ error = ErrInfrastructureNotReady
	_ error = ErrSecretsNotReady
	_ error = ErrModelsNotReady
	_ error = ErrCacheNotReady
	_ error = ErrProfileResolutionFailed
	_ error = ErrComposeUpFailed
	_ error = ErrServicesUnhealthy
	_ error = ErrNilDependency
	_ error = ErrDestroyPartial
	_ error = ErrInvalidServiceName
	_ error = ErrVerificationFailed
	_ error = ErrPanicRecovered
)

// =============================================================================
// Security Constants and Patterns
// =============================================================================

// serviceNamePattern validates compose service names.
// Per docker-compose spec: lowercase letters, digits, hyphens, and underscores.
var serviceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// sensitivePatterns are regex patterns that match sensitive data in error messages.
// These are redacted before logging or storing in diagnostics.
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|password|token|credential)[=:\s]+[^\s]+`),
	regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9]+)`),                  // OpenAI/Anthropic API keys
	regexp.MustCompile(`(?i)(bearer\s+[a-zA-Z0-9._-]+)`),         // Bearer tokens
	regexp.MustCompile(`(?i)([a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+)`), // Email addresses
}

// =============================================================================
// DestroyResult for Partial Failure Tracking
// =============================================================================

// DestroyResult contains the outcome of a Destroy operation.
//
// # Description
//
// Provides detailed information about what succeeded and failed during
// destroy, allowing callers to understand partial failure states.
type DestroyResult struct {
	// Success is true if all phases completed without error.
	Success bool

	// StopError contains error from stop phase (nil if successful).
	StopError error

	// DownError contains error from compose down phase (nil if successful).
	DownError error

	// CleanupError contains error from force cleanup phase (nil if successful).
	CleanupError error

	// VerificationError contains error from post-destroy verification (nil if successful).
	VerificationError error

	// ContainersRemaining is the count of containers still present after destroy.
	// Zero indicates clean destruction.
	ContainersRemaining int
}

// HasErrors returns true if any phase encountered an error.
func (r *DestroyResult) HasErrors() bool {
	return r.StopError != nil || r.DownError != nil ||
		r.CleanupError != nil || r.VerificationError != nil
}

// =============================================================================
// Interface Definition
// =============================================================================

// StackManager orchestrates the lifecycle of the Aleutian stack.
//
// # Description
//
// This is the primary interface for starting, stopping, and managing
// the containerized services that make up Aleutian. It coordinates
// infrastructure setup, model verification, secrets provisioning,
// profile resolution, container orchestration, and health checking.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use. However, only one
// Start/Stop/Destroy operation should be in progress at a time.
// Concurrent operations are serialized via mutex.
//
// # Context Handling
//
// All methods accept context.Context for cancellation and timeout.
// Long-running operations like Start() respect context cancellation
// at each phase boundary.
//
// # Error Handling
//
// All errors include context about which phase failed. On failure,
// diagnostics are collected automatically for troubleshooting.
type StackManager interface {
	// Start initializes and starts all stack services.
	//
	// # Description
	//
	// Main entry point that orchestrates the complete startup sequence:
	//   1. Infrastructure readiness (Podman machine)
	//   2. Model availability verification (optional, skipped if SkipModelCheck)
	//   3. Secrets provisioning (API keys, tokens)
	//   4. Cache path resolution (model storage location)
	//   5. Profile-based environment configuration
	//   6. Container orchestration (podman-compose up)
	//   7. Health check verification
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation/timeout. Checked at each phase boundary.
	//   - opts: Configuration for the start operation
	//
	// # Outputs
	//
	//   - error: Non-nil if startup fails at any phase. Error includes phase context.
	//
	// # Examples
	//
	//   // Basic start
	//   err := mgr.Start(ctx, StartOptions{})
	//
	//   // Start with explicit profile and force rebuild
	//   err := mgr.Start(ctx, StartOptions{
	//       ForceBuild: true,
	//       Profile:    "performance",
	//   })
	//
	//   // CI/CD start (no prompts, auto-approve)
	//   err := mgr.Start(ctx, StartOptions{
	//       NonInteractive: true,
	//       AutoApprove:    true,
	//       SkipModelCheck: true,
	//   })
	//
	// # Error Handling
	//
	// On failure, diagnostics are collected automatically. The error
	// message indicates which phase failed:
	//   - ErrInfrastructureNotReady: Podman machine issues
	//   - ErrModelsNotReady: Ollama model unavailable
	//   - ErrSecretsNotReady: API key provisioning failed
	//   - ErrCacheNotReady: Cache path resolution failed
	//   - ErrProfileResolutionFailed: Hardware detection failed
	//   - ErrComposeUpFailed: Container startup failed
	//   - ErrServicesUnhealthy: Health check timeout
	//
	// # Limitations
	//
	//   - Only one Start operation should be in progress at a time
	//   - Requires Podman daemon to be running (macOS)
	//   - Network may be required for model pulling
	//
	// # Assumptions
	//
	//   - Configuration is valid
	//   - Stack directory exists
	//   - All dependencies are properly initialized
	Start(ctx context.Context, opts StartOptions) error

	// Stop gracefully stops all running services.
	//
	// # Description
	//
	// Stops all stack containers using a multi-phase approach:
	//   1. Graceful stop with SIGTERM (configurable timeout)
	//   2. Force stop with SIGKILL if containers don't respond
	//
	// Does NOT remove containers or volumes. Use Destroy() for full cleanup.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation/timeout
	//
	// # Outputs
	//
	//   - error: Non-nil if stop fails. Returns nil if already stopped.
	//
	// # Examples
	//
	//   if err := mgr.Stop(ctx); err != nil {
	//       log.Printf("Stop failed: %v", err)
	//   }
	//
	// # Limitations
	//
	//   - Returns nil if stack is already stopped (not an error)
	//   - Does not remove containers (use Destroy for that)
	//
	// # Assumptions
	//
	//   - Podman daemon is accessible
	Stop(ctx context.Context) error

	// Destroy stops and removes all services and optionally data.
	//
	// # Description
	//
	// Complete teardown of the stack:
	//   1. Stops all containers (graceful then force)
	//   2. Removes containers via podman-compose down
	//   3. Optionally removes data files (volumes, cache)
	//
	// This is a destructive operation. Data cannot be recovered.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation/timeout
	//   - removeFiles: If true, also removes data volumes and cache
	//
	// # Outputs
	//
	//   - error: Non-nil if destruction fails
	//
	// # Examples
	//
	//   // Remove containers but keep data
	//   err := mgr.Destroy(ctx, false)
	//
	//   // Full cleanup including all data
	//   err := mgr.Destroy(ctx, true)
	//
	// # Limitations
	//
	//   - removeFiles=true is irreversible
	//   - May leave orphaned resources if interrupted
	//
	// # Assumptions
	//
	//   - User has confirmed destructive operation
	Destroy(ctx context.Context, removeFiles bool) error

	// Status returns the current state of all services.
	//
	// # Description
	//
	// Queries the current state of all stack components:
	//   - Podman machine state (macOS)
	//   - Container states (running, stopped, exited)
	//   - Service health status
	//   - Resource usage (if available)
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation/timeout
	//
	// # Outputs
	//
	//   - *StackStatus: Current state of all services
	//   - error: Non-nil if status query fails
	//
	// # Examples
	//
	//   status, err := mgr.Status(ctx)
	//   if err != nil {
	//       return err
	//   }
	//   fmt.Printf("State: %s, Running: %d, Healthy: %d\n",
	//       status.State, status.RunningCount, status.HealthyCount)
	//
	// # Limitations
	//
	//   - Resource usage may not be available on all platforms
	//   - Health status reflects point-in-time state
	//
	// # Assumptions
	//
	//   - Podman daemon is accessible
	Status(ctx context.Context) (*StackStatus, error)

	// Logs streams logs from specified services.
	//
	// # Description
	//
	// Streams container logs to stdout. Supports:
	//   - Following logs in real-time
	//   - Filtering by service name
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation (terminates streaming)
	//   - services: Service names to stream (empty = all services)
	//
	// # Outputs
	//
	//   - error: Non-nil if streaming fails to start
	//
	// # Examples
	//
	//   // Stream all logs
	//   err := mgr.Logs(ctx, nil)
	//
	//   // Stream specific service logs
	//   err := mgr.Logs(ctx, []string{"orchestrator", "weaviate"})
	//
	// # Limitations
	//
	//   - Blocks until context is cancelled
	//   - Large log volumes may consume memory
	//
	// # Assumptions
	//
	//   - At least one container exists
	Logs(ctx context.Context, services []string) error
}

// =============================================================================
// Supporting Types
// =============================================================================

// StartOptions configures the stack start operation.
//
// # Description
//
// Provides configuration for the Start() method including build options,
// profile selection, model checking, and interactive mode settings.
type StartOptions struct {
	// ForceRecreate triggers machine recreation if drift detected.
	// Corresponds to --force-recreate CLI flag.
	ForceRecreate bool

	// FixMounts forces mount configuration fix even when foreign containers are running.
	// This will stop foreign containers to allow machine recreation.
	// Corresponds to --fix-mounts CLI flag.
	FixMounts bool

	// ForceBuild rebuilds container images even if they exist.
	// Corresponds to --build CLI flag.
	ForceBuild bool

	// SkipModelCheck skips Ollama model verification during startup.
	// Useful for offline/air-gapped deployments with pre-downloaded models.
	// Corresponds to --skip-model-check CLI flag.
	SkipModelCheck bool

	// Profile selects the optimization profile.
	// Options: "low", "standard", "performance", "ultra", "manual"
	// Empty string means auto-detect based on hardware.
	// Corresponds to --profile CLI flag.
	Profile string

	// BackendOverride overrides the model backend type.
	// Options: "ollama", "openai", "anthropic"
	// Empty string means use configuration default.
	// Corresponds to --backend CLI flag.
	BackendOverride string

	// ForecastMode sets the forecast service mode.
	// Options: "live", "backtest"
	// Empty string means use configuration default.
	// Corresponds to --forecast-mode CLI flag.
	ForecastMode string

	// NonInteractive disables all user prompts.
	// Use for CI/CD or scripted deployments.
	// Corresponds to --non-interactive CLI flag.
	NonInteractive bool

	// AutoApprove automatically accepts prompts (like --yes flag).
	// Corresponds to --yes CLI flag.
	AutoApprove bool

	// Services limits operations to specific services.
	// If empty, all services are affected.
	// Corresponds to --service CLI flag (can be specified multiple times).
	Services []string
}

// StackStatus represents the current state of the stack.
//
// # Description
//
// Contains comprehensive status information for all stack components
// including container states, health status, and resource usage.
type StackStatus struct {
	// State is the overall stack state.
	// Values: "running", "stopped", "partial", "unknown"
	State string

	// RunningCount is the number of running containers.
	RunningCount int

	// StoppedCount is the number of stopped containers.
	StoppedCount int

	// HealthyCount is the number of healthy containers.
	HealthyCount int

	// UnhealthyCount is the number of unhealthy containers.
	UnhealthyCount int

	// Services contains status for each individual service.
	Services []StackServiceInfo

	// MachineState is the Podman machine state (macOS only).
	// Values: "running", "stopped", "not_found"
	MachineState string

	// LastStarted is when the stack was last started.
	// Zero value if never started or unknown.
	LastStarted time.Time

	// Uptime is the duration since last start.
	// Zero if not running.
	Uptime time.Duration
}

// StackServiceInfo contains status information for a single service.
//
// # Description
//
// Provides detailed information about a container service including
// state, health, ports, and resource usage.
type StackServiceInfo struct {
	// Name is the service name from compose file.
	Name string

	// ContainerName is the actual container name.
	ContainerName string

	// State is the container state.
	// Values: "running", "exited", "created", "paused"
	State string

	// Healthy indicates health check status.
	// nil means no health check defined.
	Healthy *bool

	// Ports lists published port mappings (e.g., "8080:8080/tcp").
	Ports []string

	// Image is the container image name.
	Image string

	// StartedAt is when the container started.
	// Zero value if never started.
	StartedAt time.Time

	// CPUPercent is CPU usage percentage (if available, -1 if unknown).
	CPUPercent float64

	// MemoryMB is memory usage in megabytes (if available, -1 if unknown).
	MemoryMB int64
}

// =============================================================================
// Security Helpers
// =============================================================================

// discardWriter is a no-op writer used when output is nil.
// This prevents nil pointer panics while clearly indicating the issue.
type discardWriter struct{}

// Write implements io.Writer, discarding all data.
func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

// sanitizeErrorForDiagnostics removes sensitive data from error messages.
//
// # Description
//
// Redacts API keys, tokens, passwords, and other sensitive patterns
// from error messages before storing in diagnostics files.
//
// # Inputs
//
//   - errMsg: Error message that may contain sensitive data
//
// # Outputs
//
//   - string: Sanitized error message with sensitive data replaced by [REDACTED]
//
// # Examples
//
//	msg := sanitizeErrorForDiagnostics("API key sk-ant-abc123 is invalid")
//	// Returns: "API key [REDACTED] is invalid"
//
// # Limitations
//
//   - Pattern-based; may miss some sensitive data or over-redact
//
// # Assumptions
//
//   - sensitivePatterns is properly initialized
func sanitizeErrorForDiagnostics(errMsg string) string {
	result := errMsg
	for _, pattern := range sensitivePatterns {
		result = pattern.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}

// validateServiceName checks if a service name is safe for compose operations.
//
// # Description
//
// Validates service names against docker-compose naming rules to prevent
// injection attacks or undefined behavior.
//
// # Inputs
//
//   - name: Service name to validate
//
// # Outputs
//
//   - error: ErrInvalidServiceName if validation fails, nil otherwise
//
// # Examples
//
//	err := validateServiceName("orchestrator")  // nil
//	err := validateServiceName("../../etc")     // ErrInvalidServiceName
//
// # Limitations
//
//   - Strict validation; some valid edge cases may be rejected
//
// # Assumptions
//
//   - Service names follow docker-compose conventions
func validateServiceName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty service name", ErrInvalidServiceName)
	}
	if len(name) > 63 {
		return fmt.Errorf("%w: service name exceeds 63 characters", ErrInvalidServiceName)
	}
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q contains invalid characters", ErrInvalidServiceName, name)
	}
	return nil
}

// validateServiceNames checks all service names in a slice.
//
// # Description
//
// Validates all service names in the slice, returning error on first invalid name.
//
// # Inputs
//
//   - names: Slice of service names to validate
//
// # Outputs
//
//   - error: ErrInvalidServiceName if any name is invalid, nil otherwise
//
// # Examples
//
//	err := validateServiceNames([]string{"orchestrator", "weaviate"})  // nil
//	err := validateServiceNames([]string{"valid", "../bad"})           // error
//
// # Limitations
//
//   - Stops at first invalid name
//
// # Assumptions
//
//   - Empty slice is valid (means "all services")
func validateServiceNames(names []string) error {
	for _, name := range names {
		if err := validateServiceName(name); err != nil {
			return err
		}
	}
	return nil
}

// recoverPanic converts a recovered panic into an error.
//
// # Description
//
// Used with defer to safely recover from panics in mutating operations.
// Ensures the mutex is released and the error is properly propagated.
// Intended to be called from a deferred function with recover().
//
// # Inputs
//
//   - r: The value returned from recover() (nil if no panic)
//   - errPtr: Pointer to the error variable to set
//
// # Outputs
//
//   - None (sets *errPtr if panic occurred)
//
// # Examples
//
//	func (s *DefaultStackManager) SomeMethod() (err error) {
//	    defer func() {
//	        recoverPanic(recover(), &err)
//	    }()
//	    // ... method body
//	}
//
// # Limitations
//
//   - Must be called from within a deferred function
//   - Original panic stack trace is lost (logged instead)
//
// # Assumptions
//
//   - errPtr is non-nil
func recoverPanic(r interface{}, errPtr *error) {
	if r == nil {
		return
	}

	var panicErr error
	switch v := r.(type) {
	case error:
		panicErr = fmt.Errorf("%w: %v", ErrPanicRecovered, v)
	case string:
		panicErr = fmt.Errorf("%w: %s", ErrPanicRecovered, v)
	default:
		panicErr = fmt.Errorf("%w: %v", ErrPanicRecovered, v)
	}

	if *errPtr == nil {
		*errPtr = panicErr
	}
}

// =============================================================================
// Default Implementation
// =============================================================================

// DefaultStackManager implements StackManager by coordinating all
// infrastructure, secrets, models, caching, profiles, and compose operations.
//
// # Description
//
// Production implementation that orchestrates all stack lifecycle
// operations. All external operations go through injected interfaces
// for testability.
//
// # Thread Safety
//
// Safe for concurrent use. Operations that modify state (Start, Stop,
// Destroy) are serialized with mutex.
//
// # Dependencies
//
// All dependencies are required except models (which may be nil for
// SkipModelCheck mode):
//   - infra: Podman machine lifecycle (macOS)
//   - secrets: API key provisioning
//   - cache: Model cache path resolution
//   - compose: Container orchestration
//   - health: Service health verification
//   - models: Ollama model verification (may be nil)
//   - profile: Hardware-based configuration
//   - diagnostics: Error diagnostics collection
//   - config: Global configuration
type DefaultStackManager struct {
	// infra handles Podman machine lifecycle (Phase 5).
	infra infra.InfrastructureManager

	// secrets handles API key provisioning (Phase 6).
	secrets SecretsManager

	// cache resolves model cache paths (Phase 7).
	cache CachePathResolver

	// compose executes podman-compose commands (Phase 8).
	compose compose.ComposeExecutor

	// health verifies service availability (Phase 9).
	health health.HealthChecker

	// models ensures Ollama models are available (already implemented).
	// May be nil if model checking is disabled.
	models ModelEnsurer

	// profile resolves hardware-based configuration (Phase 4).
	profile ProfileResolver

	// diagnostics collects error diagnostics (Phase 3).
	diagnostics diagnostics.DiagnosticsCollector

	// config is the global Aleutian configuration (Phase 0).
	config *config.AleutianConfig

	// stackDir is the stack directory path used for compose operations.
	// Used to set TRACE_PROJECTS_DIR for the trace container volume mount.
	stackDir string

	// output is where status messages are written.
	// Default: os.Stdout
	output io.Writer

	// mu serializes mutating operations (Start, Stop, Destroy).
	mu sync.Mutex
}

// NewDefaultStackManager creates a stack manager with all dependencies.
//
// # Description
//
// Creates a ready-to-use StackManager with injected dependencies.
// All dependencies except models are required. Models may be nil
// if model checking should be skipped. Validates all required
// dependencies and returns an error if any are nil.
//
// # Inputs
//
//   - infra: InfrastructureManager for Podman machine (required)
//   - secrets: SecretsManager for secret provisioning (required)
//   - cache: CachePathResolver for model cache paths (required)
//   - compose: ComposeExecutor for container orchestration (required)
//   - health: HealthChecker for service health (required)
//   - models: ModelEnsurer for model verification (may be nil)
//   - profile: ProfileResolver for hardware-based config (required)
//   - diagnostics: DiagnosticsCollector for error info (required)
//   - cfg: AleutianConfig global configuration (required)
//   - stackDir: stack directory path for TRACE_PROJECTS_DIR (required)
//
// # Outputs
//
//   - *DefaultStackManager: Ready-to-use manager
//   - error: ErrNilDependency if any required dependency is nil
//
// # Examples
//
//	// Full dependencies
//	mgr, err := NewDefaultStackManager(
//	    infra, secrets, cache, compose, health,
//	    models, profile, diagnostics, config.Global, stackDir,
//	)
//	if err != nil {
//	    return fmt.Errorf("failed to create stack manager: %w", err)
//	}
//
//	// Without model checking
//	mgr, err := NewDefaultStackManager(
//	    infra, secrets, cache, compose, health,
//	    nil, // models = nil is allowed
//	    profile, diagnostics, config.Global, stackDir,
//	)
//
// # Limitations
//
//   - Does not validate that dependencies are properly configured
//   - Caller is responsible for ensuring dependencies are functional
//
// # Assumptions
//
//   - All required dependencies are properly initialized
//   - Dependencies will be valid for the lifetime of the manager
//   - models may be nil (model checking will be skipped)
func NewDefaultStackManager(
	infraMgr infra.InfrastructureManager,
	secrets SecretsManager,
	cache CachePathResolver,
	composeMgr compose.ComposeExecutor,
	health health.HealthChecker,
	models ModelEnsurer,
	profile ProfileResolver,
	diagnostics diagnostics.DiagnosticsCollector,
	cfg *config.AleutianConfig,
	stackDir string,
) (*DefaultStackManager, error) {
	// Validate required dependencies
	if infraMgr == nil {
		return nil, fmt.Errorf("%w: InfrastructureManager", ErrNilDependency)
	}
	if secrets == nil {
		return nil, fmt.Errorf("%w: SecretsManager", ErrNilDependency)
	}
	if cache == nil {
		return nil, fmt.Errorf("%w: CachePathResolver", ErrNilDependency)
	}
	if composeMgr == nil {
		return nil, fmt.Errorf("%w: ComposeExecutor", ErrNilDependency)
	}
	if health == nil {
		return nil, fmt.Errorf("%w: HealthChecker", ErrNilDependency)
	}
	if profile == nil {
		return nil, fmt.Errorf("%w: ProfileResolver", ErrNilDependency)
	}
	if diagnostics == nil {
		return nil, fmt.Errorf("%w: DiagnosticsCollector", ErrNilDependency)
	}
	if cfg == nil {
		return nil, fmt.Errorf("%w: AleutianConfig", ErrNilDependency)
	}
	// Note: models may be nil (model checking skipped)

	return &DefaultStackManager{
		infra:       infraMgr,
		secrets:     secrets,
		cache:       cache,
		compose:     composeMgr,
		health:      health,
		models:      models,
		profile:     profile,
		diagnostics: diagnostics,
		config:      cfg,
		stackDir:    stackDir,
		output:      os.Stdout,
	}, nil
}

// SetOutput configures the output writer for status messages.
//
// # Description
//
// Allows redirecting status output for testing or logging.
// Default is os.Stdout. If nil is passed, a discard writer
// is used to prevent nil pointer panics.
//
// # Inputs
//
//   - w: Writer for status messages (nil uses discard writer)
//
// # Examples
//
//	var buf bytes.Buffer
//	mgr.SetOutput(&buf)
//	mgr.Start(ctx, opts)
//	output := buf.String()
//
//	// Suppress output
//	mgr.SetOutput(nil)
//
// # Limitations
//
//   - Output sent to nil writer is silently discarded
//
// # Assumptions
//
//   - None
func (s *DefaultStackManager) SetOutput(w io.Writer) {
	if w == nil {
		s.output = discardWriter{}
	} else {
		s.output = w
	}
}

// =============================================================================
// Interface Methods (Stubs - To Be Implemented in Step 2/3)
// =============================================================================

// Start initializes and starts all stack services.
//
// See interface documentation for full details.
func (s *DefaultStackManager) Start(ctx context.Context, opts StartOptions) (err error) {
	// Serialize mutating operations to prevent concurrent starts.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Recover from panics to prevent deadlocks and ensure error propagation.
	defer func() {
		recoverPanic(recover(), &err)
	}()

	startTime := time.Now()

	// Phase 1: Infrastructure readiness
	if err := s.ensureInfrastructureReady(ctx, opts); err != nil {
		return err
	}

	// Phase 2: Model verification (optional)
	if err := s.ensureModelsReady(ctx, opts); err != nil {
		return err
	}

	// Phase 3: Secrets verification
	if err := s.ensureSecretsReady(ctx, opts); err != nil {
		return err
	}

	// Phase 4: Cache path resolution
	cachePath, err := s.resolveCachePath(ctx)
	if err != nil {
		return err
	}

	// Phase 5: Profile resolution (environment variables)
	env, err := s.resolveEnvironment(ctx, opts, cachePath)
	if err != nil {
		return err
	}

	// Phase 6: Container orchestration
	if err := s.startContainers(ctx, opts, env); err != nil {
		return err
	}

	// Phase 7: Health verification
	if err := s.waitForHealthy(ctx, opts); err != nil {
		return err
	}

	// Success - print summary
	s.printStartupSummary(startTime, opts)
	return nil
}

// =============================================================================
// Start Phase Helpers
// =============================================================================

// ensureInfrastructureReady verifies the Podman machine is ready for operations.
//
// # Description
//
// Ensures the container infrastructure (Podman machine on macOS) is running
// and properly configured. This is the first phase of startup because all
// subsequent operations depend on container runtime availability.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing ForceRecreate, NonInteractive, AutoApprove
//
// # Outputs
//
//   - error: Non-nil if infrastructure cannot be made ready
//
// # Examples
//
//	err := s.ensureInfrastructureReady(ctx, StartOptions{
//	    ForceRecreate:  true,
//	    NonInteractive: true,
//	})
//
// # Limitations
//
//   - On macOS, requires Podman Desktop or podman CLI installed
//   - May prompt user for confirmation unless NonInteractive is set
//
// # Assumptions
//
//   - InfrastructureManager dependency is non-nil
//   - Config contains valid machine settings
func (s *DefaultStackManager) ensureInfrastructureReady(ctx context.Context, opts StartOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.output, "Checking infrastructure...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	infraOpts := s.buildInfrastructureOptions(opts)

	if err := s.infra.EnsureReady(ctx, infraOpts); err != nil {
		s.collectDiagnostics(ctx, "infrastructure", err)
		return fmt.Errorf("%w: %v", ErrInfrastructureNotReady, err)
	}

	return nil
}

// buildInfrastructureOptions constructs InfrastructureOptions from config and start options.
//
// # Description
//
// Merges default infrastructure options with values from configuration
// and command-line start options. Handles nil config gracefully.
//
// # Inputs
//
//   - opts: Start options containing user preferences
//
// # Outputs
//
//   - InfrastructureOptions: Fully populated options struct
//
// # Examples
//
//	infraOpts := s.buildInfrastructureOptions(StartOptions{ForceRecreate: true})
//
// # Limitations
//
//   - Does not validate config values
//
// # Assumptions
//
//   - DefaultInfrastructureOptions() returns sensible defaults
func (s *DefaultStackManager) buildInfrastructureOptions(opts StartOptions) infra.InfrastructureOptions {
	infraOpts := infra.DefaultInfrastructureOptions()

	// Apply config overrides if available
	if s.config != nil {
		if s.config.Machine.Id != "" {
			infraOpts.MachineName = s.config.Machine.Id
		}
		if s.config.Machine.CPUCount > 0 {
			infraOpts.CPUs = s.config.Machine.CPUCount
		}
		if s.config.Machine.MemoryAmount > 0 {
			infraOpts.MemoryMB = s.config.Machine.MemoryAmount
		}
		infraOpts.Mounts = s.config.Machine.Drives
	}

	// Apply start options
	infraOpts.ForceRecreate = opts.ForceRecreate
	infraOpts.FixMounts = opts.FixMounts
	infraOpts.SkipPrompts = opts.NonInteractive
	infraOpts.AllowSensitiveMounts = opts.AutoApprove

	return infraOpts
}

// ensureModelsReady verifies required Ollama models are available.
//
// # Description
//
// Checks that all required models (embedding, LLM) are available locally
// or can be downloaded. This phase is skipped if SkipModelCheck is true
// or if no ModelEnsurer was provided.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing SkipModelCheck flag
//
// # Outputs
//
//   - error: Non-nil if required models are unavailable
//
// # Examples
//
//	// Normal check
//	err := s.ensureModelsReady(ctx, StartOptions{})
//
//	// Skip for offline deployment
//	err := s.ensureModelsReady(ctx, StartOptions{SkipModelCheck: true})
//
// # Limitations
//
//   - Requires network connectivity for downloading missing models
//   - Large models may take significant time to download
//
// # Assumptions
//
//   - Ollama server is running if model check is enabled
//   - models field may be nil (gracefully skipped)
func (s *DefaultStackManager) ensureModelsReady(ctx context.Context, opts StartOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Skip if model checking is disabled or no ModelEnsurer
	if opts.SkipModelCheck || s.models == nil {
		return nil
	}

	if _, err := fmt.Fprintf(s.output, "Checking required models...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	result, err := s.models.EnsureModels(ctx)
	if err != nil {
		s.collectDiagnostics(ctx, "models", err)
		return fmt.Errorf("%w: %v", ErrModelsNotReady, err)
	}

	if !result.CanProceed {
		err := fmt.Errorf("missing required models: %v", result.ModelsMissing)
		s.collectDiagnostics(ctx, "models", err)
		return fmt.Errorf("%w: %v", ErrModelsNotReady, err)
	}

	// Log warnings and downloads
	s.logModelResults(result)

	return nil
}

// logModelResults logs warnings and downloaded models from EnsureModels result.
//
// # Description
//
// Formats and outputs model verification results including any warnings
// and the list of models that were downloaded during this run.
//
// # Inputs
//
//   - result: The result from ModelEnsurer.EnsureModels()
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.logModelResults(result)
//
// # Limitations
//
//   - Output format is not configurable
//
// # Assumptions
//
//   - result is non-nil
//   - s.output is non-nil
func (s *DefaultStackManager) logModelResults(result *ModelEnsureResult) {
	for _, warn := range result.Warnings {
		if _, err := fmt.Fprintf(s.output, "  Warning: %s\n", warn); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	if len(result.ModelsPulled) > 0 {
		if _, err := fmt.Fprintf(s.output, "  Downloaded: %v\n", result.ModelsPulled); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}
}

// ensureSecretsReady verifies required API keys and secrets are available.
//
// # Description
//
// Checks that secrets required for the configured backend are present.
// Different backends require different secrets (e.g., Anthropic needs
// ANTHROPIC_API_KEY, OpenAI needs OPENAI_API_KEY).
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing BackendOverride
//
// # Outputs
//
//   - error: Non-nil if required secrets are missing
//
// # Examples
//
//	err := s.ensureSecretsReady(ctx, StartOptions{BackendOverride: "anthropic"})
//
// # Limitations
//
//   - Only checks for presence, not validity of secrets
//   - Does not create missing secrets
//
// # Assumptions
//
//   - SecretsManager dependency is non-nil
//   - Secret names follow standard naming conventions
func (s *DefaultStackManager) ensureSecretsReady(ctx context.Context, opts StartOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.output, "Checking secrets...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	requiredSecrets := s.getRequiredSecrets(opts)

	for _, secretName := range requiredSecrets {
		if err := s.verifySecretExists(ctx, secretName); err != nil {
			return err
		}
	}

	return nil
}

// getRequiredSecrets returns the list of secrets required for the backend.
//
// # Description
//
// Determines which secrets are required based on the model backend type.
// Ollama requires no API keys; Anthropic and OpenAI require their
// respective API keys.
//
// # Inputs
//
//   - opts: Start options containing BackendOverride
//
// # Outputs
//
//   - []string: List of required secret names
//
// # Examples
//
//	secrets := s.getRequiredSecrets(StartOptions{BackendOverride: "anthropic"})
//	// Returns: ["ANTHROPIC_API_KEY"]
//
// # Limitations
//
//   - Only handles known backend types
//   - Unknown backends return empty list
//
// # Assumptions
//
//   - Backend type is one of: ollama, anthropic, openai
func (s *DefaultStackManager) getRequiredSecrets(opts StartOptions) []string {
	backendType := opts.BackendOverride
	if backendType == "" && s.config != nil {
		backendType = s.config.ModelBackend.Type
	}

	var required []string
	switch backendType {
	case "anthropic":
		required = append(required, "ANTHROPIC_API_KEY")
	case "openai":
		required = append(required, "OPENAI_API_KEY")
		// ollama doesn't require API keys
	}

	return required
}

// verifySecretExists checks if a specific secret is available.
//
// # Description
//
// Verifies that a named secret exists in the configured secret backends.
// Collects diagnostics and returns a descriptive error if not found.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - secretName: The canonical name of the secret to verify
//
// # Outputs
//
//   - error: Non-nil if secret is missing or verification fails
//
// # Examples
//
//	err := s.verifySecretExists(ctx, "ANTHROPIC_API_KEY")
//
// # Limitations
//
//   - Does not validate secret value format
//
// # Assumptions
//
//   - SecretsManager dependency is non-nil
func (s *DefaultStackManager) verifySecretExists(ctx context.Context, secretName string) error {
	hasSecret, err := s.secrets.HasSecret(ctx, secretName)
	if err != nil {
		s.collectDiagnostics(ctx, "secrets", err)
		return fmt.Errorf("%w: failed to check %s: %v", ErrSecretsNotReady, secretName, err)
	}

	if !hasSecret {
		err := fmt.Errorf("required secret %s not found", secretName)
		s.collectDiagnostics(ctx, "secrets", err)
		return fmt.Errorf("%w: %v", ErrSecretsNotReady, err)
	}

	return nil
}

// resolveCachePath determines the optimal model cache location.
//
// # Description
//
// Resolves the path where model files should be stored. This may be
// a local directory or an external drive, depending on configuration
// and available space.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - string: Absolute path to the cache directory
//   - error: Non-nil if cache cannot be resolved
//
// # Examples
//
//	path, err := s.resolveCachePath(ctx)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Using cache: %s\n", path)
//
// # Limitations
//
//   - May require user confirmation for external drives
//
// # Assumptions
//
//   - CachePathResolver dependency is non-nil
func (s *DefaultStackManager) resolveCachePath(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if _, err := fmt.Fprintf(s.output, "Resolving cache path...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	cachePath, err := s.cache.Resolve(ctx, CacheTypeModels)
	if err != nil {
		s.collectDiagnostics(ctx, "cache", err)
		return "", fmt.Errorf("%w: %v", ErrCacheNotReady, err)
	}

	if _, err := fmt.Fprintf(s.output, "  Using cache: %s\n", cachePath); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
	return cachePath, nil
}

// resolveEnvironment builds environment variables for container startup.
//
// # Description
//
// Resolves hardware-based profile settings and combines them with
// cache path, backend overrides, and forecast mode into a complete
// environment map for compose.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing Profile, BackendOverride, ForecastMode
//   - cachePath: Resolved cache directory path
//
// # Outputs
//
//   - map[string]string: Environment variables for containers
//   - error: Non-nil if profile resolution fails
//
// # Examples
//
//	env, err := s.resolveEnvironment(ctx, StartOptions{Profile: "performance"}, "/cache")
//
// # Limitations
//
//   - Profile auto-detection may be slow on first run
//
// # Assumptions
//
//   - ProfileResolver dependency is non-nil
//   - cachePath is a valid directory path
func (s *DefaultStackManager) resolveEnvironment(ctx context.Context, opts StartOptions, cachePath string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := fmt.Fprintf(s.output, "Resolving profile...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	profileOpts := s.buildProfileOptions(opts)

	env, err := s.profile.Resolve(ctx, profileOpts)
	if err != nil {
		s.collectDiagnostics(ctx, "profile", err)
		return nil, fmt.Errorf("%w: %v", ErrProfileResolutionFailed, err)
	}

	// Add additional environment variables
	env["ALEUTIAN_MODELS_CACHE"] = cachePath

	// Set TRACE_PROJECTS_DIR for the trace container volume mount.
	// Mounts the stack directory at /projects inside the container,
	// enabling graph initialization and file-based tools.
	// Users can override via TRACE_PROJECTS_DIR env var.
	if os.Getenv("TRACE_PROJECTS_DIR") == "" {
		env["TRACE_PROJECTS_DIR"] = s.stackDir
	}

	if opts.BackendOverride != "" {
		env["MODEL_BACKEND_TYPE"] = opts.BackendOverride
	}

	if opts.ForecastMode != "" {
		env["FORECAST_MODE"] = opts.ForecastMode

		// When using sapheneia mode, ensure InfluxDB token is configured
		// This token is required by data-fetcher and orchestrator to connect to InfluxDB
		if opts.ForecastMode == "sapheneia" {
			influxToken := os.Getenv("INFLUXDB_TOKEN")
			if influxToken == "" {
				// Use a default development token if not set
				// Must match podman-compose.timeseries.yml and cmd_evaluation.go defaults
				influxToken = "aleutian-dev-token-2026"
				slog.Info("Using default InfluxDB token for development")
			}
			env["INFLUXDB_TOKEN"] = influxToken
			env["DOCKER_INFLUXDB_INIT_ADMIN_TOKEN"] = influxToken
		}
	}

	// Log resolved profile
	if profileName, ok := env["ALEUTIAN_PROFILE"]; ok {
		if _, err := fmt.Fprintf(s.output, "  Using profile: %s\n", profileName); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	return env, nil
}

// buildProfileOptions constructs ProfileOptions from config and start options.
//
// # Description
//
// Creates ProfileOptions with explicit profile, backend type, and
// custom profiles from configuration.
//
// # Inputs
//
//   - opts: Start options containing Profile and BackendOverride
//
// # Outputs
//
//   - ProfileOptions: Fully populated options struct
//
// # Examples
//
//	profileOpts := s.buildProfileOptions(StartOptions{Profile: "ultra"})
//
// # Limitations
//
//   - Does not validate profile names
//
// # Assumptions
//
//   - config may be nil (handled gracefully)
func (s *DefaultStackManager) buildProfileOptions(opts StartOptions) ProfileOptions {
	profileOpts := ProfileOptions{
		ExplicitProfile: opts.Profile,
		BackendType:     opts.BackendOverride,
	}

	if profileOpts.BackendType == "" && s.config != nil {
		profileOpts.BackendType = s.config.ModelBackend.Type
	}

	if s.config != nil {
		profileOpts.CustomProfiles = s.config.Profiles
	}

	return profileOpts
}

// startContainers launches services via podman-compose.
//
// # Description
//
// Executes podman-compose up with the resolved environment variables
// and build options. Containers are started in detached mode.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing ForceBuild flag
//   - env: Environment variables for containers
//
// # Outputs
//
//   - error: Non-nil if compose fails
//
// # Examples
//
//	err := s.startContainers(ctx, StartOptions{ForceBuild: true}, env)
//
// # Limitations
//
//   - Does not verify service health (use waitForHealthy)
//   - Build failures are not retried
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
//   - Compose files exist at configured paths
func (s *DefaultStackManager) startContainers(ctx context.Context, opts StartOptions, env map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.output, "Starting containers...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	removed, err := s.compose.RemoveExitedContainers(ctx)
	if err != nil {
		slog.Debug("Failed to remove exited containers", "error", err)
	}
	if removed > 0 {
		if _, err := fmt.Fprintf(s.output, "  Removed %d exited container(s)\n", removed); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	upOpts := compose.UpOptions{
		ForceBuild: opts.ForceBuild,
		Env:        env,
		Detach:     true,
		Services:   opts.Services,
	}

	var result *compose.ComposeResult
	var composeErr error

	// Use spinner for visual feedback during container startup
	spinErr := util.SpinWhileContext(ctx, "Pulling images and starting services...", func() error {
		var err error
		result, err = s.compose.Up(ctx, upOpts)
		composeErr = err
		return err
	})

	if spinErr != nil {
		if composeErr != nil {
			s.collectDiagnostics(ctx, "compose", composeErr)
			s.checkRegistryAuthHint(result, composeErr)
			return fmt.Errorf("%w: %v", ErrComposeUpFailed, composeErr)
		}
		return spinErr
	}

	// Log meaningful stderr (not progress indicators)
	s.logComposeWarnings(result)

	return nil
}

// logComposeWarnings logs any meaningful warnings from compose output.
//
// # Description
//
// Filters and logs stderr output from compose, excluding progress
// indicators and empty content.
//
// # Inputs
//
//   - result: The result from ComposeExecutor.Up()
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.logComposeWarnings(result)
//
// # Limitations
//
//   - Simple string filtering may miss some progress indicators
//
// # Assumptions
//
//   - result may be nil (handled gracefully)
func (s *DefaultStackManager) logComposeWarnings(result *compose.ComposeResult) {
	if result == nil {
		return
	}

	stderr := strings.TrimSpace(result.Stderr)
	if stderr == "" {
		return
	}

	// Always check for registry auth errors, even in pull output
	s.checkRegistryAuthHint(result, nil)

	// Skip progress indicators (but only if no auth errors were found)
	if strings.Contains(stderr, "Pulling") && !isRegistryAuthError(stderr) {
		return
	}

	if _, err := fmt.Fprintf(s.output, "  Compose output: %s\n", stderr); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
}

// registryAuthPatterns contains stderr substrings that indicate registry
// authentication failures. Checked case-insensitively.
var registryAuthPatterns = []string{
	"unauthorized",
	"authentication required",
	"denied: access forbidden",
	"403 forbidden",
	"401",
	"not authenticated",
	"access denied",
	"login required",
	"credential",
	"unauthenticated",
}

// isRegistryAuthError checks if stderr output contains registry authentication errors.
//
// Description:
//
//	Scans output for common patterns from Docker/Podman registry auth failures
//	including GCR, Docker Hub, and generic OCI registries.
//
// Inputs:
//
//   - output: stderr or error string to check
//
// Outputs:
//
//   - bool: true if output contains an auth-related error pattern
//
// Thread Safety: This function is safe for concurrent use (stateless).
func isRegistryAuthError(output string) bool {
	lower := strings.ToLower(output)
	for _, pattern := range registryAuthPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// checkRegistryAuthHint checks compose result and error for registry auth failures
// and prints actionable guidance.
//
// Description:
//
//	Examines compose stderr and error message for patterns indicating expired
//	or missing registry credentials. When detected, prints hints for Docker Hub,
//	Google Container Registry, and other common registries.
//
// Inputs:
//
//   - result: Compose result (may be nil)
//   - composeErr: Error from compose up (may be nil)
//
// Outputs:
//
//   - None (writes hints to s.output)
//
// Thread Safety: This function is safe for concurrent use.
func (s *DefaultStackManager) checkRegistryAuthHint(result *compose.ComposeResult, composeErr error) {
	var combined string
	if result != nil {
		combined = result.Stderr
	}
	if composeErr != nil {
		combined += " " + composeErr.Error()
	}

	if !isRegistryAuthError(combined) {
		return
	}

	hint := "\n  Registry authentication failed during image pull.\n" +
		"  Common fixes:\n" +
		"    - Google Container Registry:  gcloud auth login && gcloud auth configure-docker\n" +
		"    - Docker Hub:                 podman login docker.io\n" +
		"    - Generic registry:           podman login <registry-url>\n"

	if _, err := fmt.Fprintf(s.output, "%s\n", hint); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
}

// waitForHealthy waits for all services to pass health checks.
//
// # Description
//
// Polls services with exponential backoff until all critical services
// are healthy or timeout is reached. Extended timeout is used when
// models were just downloaded.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - opts: Start options containing SkipModelCheck (affects timeout)
//
// # Outputs
//
//   - error: Non-nil if services fail to become healthy
//
// # Examples
//
//	err := s.waitForHealthy(ctx, StartOptions{})
//
// # Limitations
//
//   - Cannot distinguish between "starting" and "permanently failed"
//   - Fixed timeout values
//
// # Assumptions
//
//   - HealthChecker dependency is non-nil
//   - DefaultServiceDefinitions() returns valid definitions
func (s *DefaultStackManager) waitForHealthy(ctx context.Context, opts StartOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.output, "Waiting for services to become healthy...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	// Use forecast mode-aware service definitions
	// When using sapheneia mode, Ollama is non-critical
	services := health.ServiceDefinitionsForMode(opts.ForecastMode)

	// When --service is specified, only check health for targeted services.
	// Without this filter, exited containers from previous sessions cause
	// health check timeouts even though the targeted service started fine.
	if len(opts.Services) > 0 {
		services = s.filterServiceDefinitions(services, opts.Services)
	}

	waitOpts := health.DefaultWaitOptions()

	// Extended timeout when models may still be loading
	if !opts.SkipModelCheck && s.models != nil {
		waitOpts.Timeout = 180 * time.Second
	}

	result, err := s.health.WaitForServices(ctx, services, waitOpts)
	if err != nil {
		s.collectDiagnostics(ctx, "health", err)
		return fmt.Errorf("%w: %v", ErrServicesUnhealthy, err)
	}

	if !result.Success {
		failedNames := s.getFailedServiceNames(result)
		err := fmt.Errorf("services unhealthy: %v", failedNames)
		s.collectDiagnostics(ctx, "health", err)
		return fmt.Errorf("%w: %v", ErrServicesUnhealthy, err)
	}

	if _, err := fmt.Fprintf(s.output, "  All %d services healthy (took %v)\n",
		len(result.Services), result.Duration.Round(time.Millisecond)); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	return nil
}

// getFailedServiceNames extracts failed service names from wait result.
//
// # Description
//
// Returns the list of critical services that failed health checks.
//
// # Inputs
//
//   - result: The result from HealthChecker.WaitForServices()
//
// # Outputs
//
//   - []string: Names of all failed critical services
//
// # Examples
//
//	names := s.getFailedServiceNames(result)
//
// # Limitations
//
//   - Only returns critical failures (non-critical are not tracked separately)
//
// # Assumptions
//
//   - result is non-nil
func (s *DefaultStackManager) getFailedServiceNames(result *health.WaitResult) []string {
	return result.FailedCritical
}

// filterServiceDefinitions returns only the service definitions matching the
// given compose service names.
//
// # Description
//
// Filters service definitions by matching compose service names against
// container names. The container name prefix (e.g., "aleutian-") is prepended
// to each service name for matching. Services with no container name (e.g.,
// host-level Ollama) are excluded when filtering.
//
// # Inputs
//
//   - defs: Full list of service definitions
//   - serviceNames: Compose service names to keep (e.g., ["trace", "weaviate"])
//
// # Outputs
//
//   - []health.ServiceDefinition: Filtered definitions matching the requested services
//
// # Limitations
//
//   - Assumes container names follow the pattern "<prefix><service_name>"
//
// # Assumptions
//
//   - serviceNames is non-empty (caller checks)
func (s *DefaultStackManager) filterServiceDefinitions(defs []health.ServiceDefinition, serviceNames []string) []health.ServiceDefinition {
	wanted := make(map[string]bool, len(serviceNames))
	for _, name := range serviceNames {
		wanted["aleutian-"+name] = true
	}

	var filtered []health.ServiceDefinition
	for _, def := range defs {
		if def.ContainerName != "" && wanted[def.ContainerName] {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

// collectDiagnostics gathers diagnostic information after an error.
//
// # Description
//
// Collects system and container diagnostics when an error occurs
// during startup. The diagnostics file path is logged for user
// reference. Sensitive data (API keys, tokens, passwords) is
// automatically sanitized before storage.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - phase: Name of the phase where error occurred
//   - err: The error that triggered diagnostics collection
//
// # Outputs
//
//   - None (writes to s.output on success or failure)
//
// # Examples
//
//	s.collectDiagnostics(ctx, "infrastructure", err)
//
// # Limitations
//
//   - Collection may fail; failure is logged but not propagated
//   - Sanitization is pattern-based and may miss some sensitive data
//
// # Assumptions
//
//   - diagnostics may be nil (gracefully skipped)
func (s *DefaultStackManager) collectDiagnostics(ctx context.Context, phase string, err error) {
	if s.diagnostics == nil {
		return
	}

	// Sanitize error message to remove sensitive data before storing
	sanitizedDetails := sanitizeErrorForDiagnostics(err.Error())

	opts := diagnostics.CollectOptions{
		Reason:  fmt.Sprintf("stack_start_%s_failure", phase),
		Details: sanitizedDetails,
		Tags: map[string]string{
			"component": "stack_manager",
			"phase":     phase,
		},
	}

	result, diagErr := s.diagnostics.Collect(ctx, opts)
	if diagErr != nil {
		if _, err := fmt.Fprintf(s.output, "  Warning: Failed to collect diagnostics: %v\n", diagErr); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
		return
	}

	if _, err := fmt.Fprintf(s.output, "  Diagnostics saved: %s\n", result.Location); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
}

// printStartupSummary outputs a summary after successful startup.
//
// # Description
//
// Prints the startup duration and access URLs for the running services.
// Optionally shows the active profile if one was explicitly selected.
//
// # Inputs
//
//   - startTime: When Start() was called
//   - opts: Start options containing Profile
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.printStartupSummary(startTime, StartOptions{Profile: "performance"})
//
// # Limitations
//
//   - Access URLs are hardcoded
//
// # Assumptions
//
//   - s.output is non-nil
func (s *DefaultStackManager) printStartupSummary(startTime time.Time, opts StartOptions) {
	duration := time.Since(startTime).Round(time.Millisecond)
	if _, err := fmt.Fprintf(s.output, "\nStack started successfully in %v\n", duration); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	if _, err := fmt.Fprintf(s.output, "\nAccess points:\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
	if _, err := fmt.Fprintf(s.output, "  Chat UI:      http://localhost:8501\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
	if _, err := fmt.Fprintf(s.output, "  API:          http://localhost:8080\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
	if _, err := fmt.Fprintf(s.output, "  Weaviate:     http://localhost:12127\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	if opts.Profile != "" {
		if _, err := fmt.Fprintf(s.output, "\nProfile: %s\n", opts.Profile); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}
}

// Stop gracefully stops all running services.
//
// See interface documentation for full details.
func (s *DefaultStackManager) Stop(ctx context.Context) (err error) {
	// Serialize mutating operations to prevent concurrent stops.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Recover from panics to prevent deadlocks and ensure error propagation.
	defer func() {
		recoverPanic(recover(), &err)
	}()

	startTime := time.Now()

	// Phase 1: Check if stack is running
	isRunning, err := s.isStackRunning(ctx)
	if err != nil {
		return err
	}
	if !isRunning {
		if _, err := fmt.Fprintf(s.output, "Stack is not running.\n"); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
		return nil
	}

	// Phase 2: Stop containers gracefully
	if err := s.stopContainersGracefully(ctx); err != nil {
		return err
	}

	s.printStopSummary(startTime)
	return nil
}

// =============================================================================
// Stop Phase Helpers
// =============================================================================

// isStackRunning checks whether any Aleutian containers are currently running.
//
// # Description
//
// Queries compose status to determine if the stack is active.
// Returns true if at least one container is in running state.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - bool: True if at least one container is running
//   - error: Non-nil if status query fails
//
// # Examples
//
//	running, err := s.isStackRunning(ctx)
//	if err != nil {
//	    return err
//	}
//	if !running {
//	    fmt.Println("Stack is not running")
//	}
//
// # Limitations
//
//   - May briefly return stale state during transitions
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) isStackRunning(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	status, err := s.compose.Status(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check stack status: %w", err)
	}

	return status.Running > 0, nil
}

// stopContainersGracefully stops and removes all containers with a graceful timeout.
//
// # Description
//
// Executes a three-phase stop and cleanup:
//  1. Graceful stop with SIGTERM (10 second timeout)
//  2. Force stop with SIGKILL if containers don't respond
//  3. Force cleanup to remove stopped containers
//
// The cleanup phase ensures containers are fully removed, not just stopped.
// This prevents issues with `podman compose down` failing due to container
// dependencies or stale container state.
//
// Logs progress and any warnings from the stop operation.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - error: Non-nil if stop fails
//
// # Examples
//
//	if err := s.stopContainersGracefully(ctx); err != nil {
//	    return fmt.Errorf("stop failed: %w", err)
//	}
//
// # Limitations
//
//   - Uses fixed 10-second graceful timeout
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
//   - At least one container is running
func (s *DefaultStackManager) stopContainersGracefully(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	stopOpts := compose.StopOptions{
		GracefulTimeout: 10 * time.Second,
	}

	var result *compose.StopResult
	var stopErr error

	// Phase 1: Use spinner for visual feedback during stop
	spinErr := util.SpinWhileContext(ctx, "Stopping containers...", func() error {
		var err error
		result, err = s.compose.Stop(ctx, stopOpts)
		stopErr = err
		return err
	})

	if spinErr != nil {
		if stopErr != nil {
			s.collectDiagnostics(ctx, "stop", stopErr)
			return fmt.Errorf("failed to stop containers: %w", stopErr)
		}
		return spinErr
	}

	s.logStopResult(result)

	// Phase 2: Force cleanup to remove stopped containers
	// This ensures a clean state and prevents dependency issues on next start
	var cleanupResult *compose.CleanupResult
	var cleanupErr error

	spinErr = util.SpinWhileContext(ctx, "Removing containers...", func() error {
		var err error
		cleanupResult, err = s.compose.ForceCleanup(ctx)
		cleanupErr = err
		return nil // Don't fail on cleanup errors, just log them
	})

	if spinErr != nil {
		// Context cancelled during cleanup
		return spinErr
	}

	// Log cleanup errors as debug (non-fatal)
	if cleanupErr != nil {
		slog.Debug("cleanup completed with errors", "error", cleanupErr)
	}
	s.logCleanupResult(cleanupResult)
	return nil
}

// logStopResult logs details from the stop operation.
//
// # Description
//
// Formats and outputs the result of stopping containers,
// including counts of graceful vs forced stops.
//
// # Inputs
//
//   - result: The result from ComposeExecutor.Stop()
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.logStopResult(result)
//
// # Limitations
//
//   - Output format is not configurable
//
// # Assumptions
//
//   - result may be nil (handled gracefully)
func (s *DefaultStackManager) logStopResult(result *compose.StopResult) {
	if result == nil {
		return
	}

	if result.TotalStopped > 0 {
		if _, err := fmt.Fprintf(s.output, "  Stopped %d containers (graceful=%d, forced=%d)\n",
			result.TotalStopped, result.GracefulStopped, result.ForceStopped); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	if len(result.Errors) > 0 {
		for _, errMsg := range result.Errors {
			if _, err := fmt.Fprintf(s.output, "  Warning: %s\n", errMsg); err != nil {
				slog.Warn("failed to write output", "error", err)
			}
		}
	}
}

// printStopSummary outputs a summary after successful stop.
//
// # Description
//
// Prints the stop duration and success message.
//
// # Inputs
//
//   - startTime: When Stop() was called
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.printStopSummary(startTime)
//
// # Limitations
//
//   - None
//
// # Assumptions
//
//   - s.output is non-nil
func (s *DefaultStackManager) printStopSummary(startTime time.Time) {
	duration := time.Since(startTime).Round(time.Millisecond)
	if _, err := fmt.Fprintf(s.output, "Stack stopped successfully in %v\n", duration); err != nil {
		slog.Warn("failed to write output", "error", err)
	}
}

// Destroy stops and removes all services and optionally data.
//
// See interface documentation for full details.
func (s *DefaultStackManager) Destroy(ctx context.Context, removeFiles bool) (err error) {
	// Serialize mutating operations to prevent concurrent destroys.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Recover from panics to prevent deadlocks and ensure error propagation.
	defer func() {
		recoverPanic(recover(), &err)
	}()

	startTime := time.Now()

	// Track errors from each phase for aggregation
	result := &DestroyResult{Success: true}

	// Phase 1: Stop containers first
	if stopErr := s.stopContainersForDestroy(ctx); stopErr != nil {
		// Continue with destroy even if stop fails
		result.StopError = stopErr
		if _, err := fmt.Fprintf(s.output, "  Warning: stop failed, continuing with destroy: %v\n", stopErr); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	// Phase 2: Remove containers via compose down
	if downErr := s.removeContainers(ctx, removeFiles); downErr != nil {
		result.DownError = downErr
		// This is critical - return immediately
		return s.buildDestroyError(result)
	}

	// Phase 3: Force cleanup for any stragglers
	if cleanupErr := s.forceCleanupRemainingContainers(ctx); cleanupErr != nil {
		result.CleanupError = cleanupErr
		if _, err := fmt.Fprintf(s.output, "  Warning: cleanup completed with errors: %v\n", cleanupErr); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	// Phase 4: Post-operation verification
	if verifyErr := s.verifyDestroyComplete(ctx); verifyErr != nil {
		result.VerificationError = verifyErr
		if _, err := fmt.Fprintf(s.output, "  Warning: verification failed: %v\n", verifyErr); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}

	s.printDestroySummary(startTime, removeFiles)

	// Return aggregated error if any non-critical failures occurred
	if result.HasErrors() {
		return s.buildDestroyError(result)
	}
	return nil
}

// buildDestroyError creates an aggregated error from DestroyResult.
//
// # Description
//
// Combines all errors from the destroy phases into a single error
// message for reporting. Uses ErrDestroyPartial as the base error.
//
// # Inputs
//
//   - result: DestroyResult containing phase errors
//
// # Outputs
//
//   - error: Aggregated error with details from all failed phases
//
// # Examples
//
//	err := s.buildDestroyError(result)
//	// Returns: "destroy completed with partial failures: stop: ..., down: ..."
//
// # Limitations
//
//   - Error message may be long if multiple phases failed
//
// # Assumptions
//
//   - result is non-nil
func (s *DefaultStackManager) buildDestroyError(result *DestroyResult) error {
	var parts []string

	if result.StopError != nil {
		parts = append(parts, fmt.Sprintf("stop: %v", result.StopError))
	}
	if result.DownError != nil {
		parts = append(parts, fmt.Sprintf("down: %v", result.DownError))
	}
	if result.CleanupError != nil {
		parts = append(parts, fmt.Sprintf("cleanup: %v", result.CleanupError))
	}
	if result.VerificationError != nil {
		parts = append(parts, fmt.Sprintf("verification: %v", result.VerificationError))
	}

	if len(parts) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrDestroyPartial, strings.Join(parts, "; "))
}

// verifyDestroyComplete checks that no containers remain after destroy.
//
// # Description
//
// Post-operation verification that confirms all Aleutian containers
// have been removed. Returns error if containers are still present.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - error: ErrVerificationFailed if containers remain, nil otherwise
//
// # Examples
//
//	if err := s.verifyDestroyComplete(ctx); err != nil {
//	    log.Printf("containers may still exist: %v", err)
//	}
//
// # Limitations
//
//   - Only checks for running containers, not orphaned resources
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) verifyDestroyComplete(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	status, err := s.compose.Status(ctx)
	if err != nil {
		// Can't verify - return warning error
		return fmt.Errorf("%w: unable to verify: %v", ErrVerificationFailed, err)
	}

	if status.Running > 0 || status.Stopped > 0 {
		return fmt.Errorf("%w: %d containers still present",
			ErrVerificationFailed, status.Running+status.Stopped)
	}

	return nil
}

// =============================================================================
// Destroy Phase Helpers
// =============================================================================

// stopContainersForDestroy stops containers before removal.
//
// # Description
//
// Attempts to gracefully stop containers before compose down.
// This is a best-effort operation; failures are logged but
// do not stop the destroy process.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - error: Non-nil if stop fails (caller may choose to continue)
//
// # Examples
//
//	if err := s.stopContainersForDestroy(ctx); err != nil {
//	    log.Printf("stop warning: %v", err)
//	}
//
// # Limitations
//
//   - Best-effort; may fail if containers are unresponsive
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) stopContainersForDestroy(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	stopOpts := compose.StopOptions{
		GracefulTimeout: 5 * time.Second, // Shorter timeout for destroy
	}

	// Use spinner for visual feedback during stop
	return util.SpinWhileContext(ctx, "Stopping containers...", func() error {
		_, err := s.compose.Stop(ctx, stopOpts)
		return err
	})
}

// removeContainers executes compose down to remove containers.
//
// # Description
//
// Runs podman-compose down with orphan removal. If removeFiles is true,
// also removes volumes (destructive operation).
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//   - removeFiles: If true, also remove volumes
//
// # Outputs
//
//   - error: Non-nil if compose down fails
//
// # Examples
//
//	// Remove containers but keep volumes
//	err := s.removeContainers(ctx, false)
//
//	// Full cleanup including volumes
//	err := s.removeContainers(ctx, true)
//
// # Limitations
//
//   - Volume removal is irreversible
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) removeContainers(ctx context.Context, removeFiles bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(s.output, "Removing containers...\n"); err != nil {
		slog.Warn("failed to write output", "error", err)
	}

	downOpts := compose.DownOptions{
		RemoveOrphans: true,
		RemoveVolumes: removeFiles,
		Timeout:       30 * time.Second,
	}

	result, err := s.compose.Down(ctx, downOpts)
	if err != nil {
		s.collectDiagnostics(ctx, "destroy", err)
		return fmt.Errorf("compose down failed: %w", err)
	}

	s.logComposeDownResult(result)
	return nil
}

// logComposeDownResult logs details from the compose down operation.
//
// # Description
//
// Formats and outputs any meaningful stderr from compose down,
// filtering out progress indicators.
//
// # Inputs
//
//   - result: The result from ComposeExecutor.Down()
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.logComposeDownResult(result)
//
// # Limitations
//
//   - Simple string filtering may miss some messages
//
// # Assumptions
//
//   - result may be nil (handled gracefully)
func (s *DefaultStackManager) logComposeDownResult(result *compose.ComposeResult) {
	if result == nil {
		return
	}

	stderr := strings.TrimSpace(result.Stderr)
	if stderr != "" && !strings.Contains(stderr, "Stopping") {
		if _, err := fmt.Fprintf(s.output, "  Compose: %s\n", stderr); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}
}

// forceCleanupRemainingContainers removes any orphaned containers.
//
// # Description
//
// Executes force cleanup to remove any containers that compose down
// may have missed. This handles orphaned containers and pods.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - error: Non-nil if force cleanup has errors (ErrCleanupPartial)
//
// # Examples
//
//	if err := s.forceCleanupRemainingContainers(ctx); err != nil {
//	    log.Printf("cleanup warning: %v", err)
//	}
//
// # Limitations
//
//   - Does not remove images
//   - May leave orphaned volumes
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) forceCleanupRemainingContainers(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	result, err := s.compose.ForceCleanup(ctx)
	if err != nil {
		return err
	}

	s.logCleanupResult(result)
	return nil
}

// logCleanupResult logs details from the force cleanup operation.
//
// # Description
//
// Formats and outputs the result of force cleanup, including
// counts of containers and pods removed.
//
// # Inputs
//
//   - result: The result from ComposeExecutor.ForceCleanup()
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.logCleanupResult(result)
//
// # Limitations
//
//   - Output format is not configurable
//
// # Assumptions
//
//   - result may be nil (handled gracefully)
func (s *DefaultStackManager) logCleanupResult(result *compose.CleanupResult) {
	if result == nil {
		return
	}

	if result.ContainersRemoved > 0 || result.PodsRemoved > 0 {
		if _, err := fmt.Fprintf(s.output, "  Cleaned up: %d containers, %d pods\n",
			result.ContainersRemoved, result.PodsRemoved); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}
}

// printDestroySummary outputs a summary after successful destroy.
//
// # Description
//
// Prints the destroy duration and what was removed.
//
// # Inputs
//
//   - startTime: When Destroy() was called
//   - removeFiles: Whether volumes were also removed
//
// # Outputs
//
//   - None (writes to s.output)
//
// # Examples
//
//	s.printDestroySummary(startTime, true)
//
// # Limitations
//
//   - None
//
// # Assumptions
//
//   - s.output is non-nil
func (s *DefaultStackManager) printDestroySummary(startTime time.Time, removeFiles bool) {
	duration := time.Since(startTime).Round(time.Millisecond)
	if removeFiles {
		if _, err := fmt.Fprintf(s.output, "Stack destroyed (including volumes) in %v\n", duration); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	} else {
		if _, err := fmt.Fprintf(s.output, "Stack destroyed (volumes preserved) in %v\n", duration); err != nil {
			slog.Warn("failed to write output", "error", err)
		}
	}
}

// Status returns the current state of all services.
//
// See interface documentation for full details.
func (s *DefaultStackManager) Status(ctx context.Context) (*StackStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Phase 1: Get compose service status
	composeStatus, err := s.getComposeStatus(ctx)
	if err != nil {
		return nil, err
	}

	// Phase 2: Get machine status (macOS only)
	machineState, err := s.getMachineState(ctx)
	if err != nil {
		// Non-fatal; continue with container status
		machineState = "unknown"
	}

	// Phase 3: Build combined status
	status := s.buildStackStatus(composeStatus, machineState)

	return status, nil
}

// =============================================================================
// Status Phase Helpers
// =============================================================================

// getComposeStatus retrieves the current state of compose services.
//
// # Description
//
// Queries podman-compose for the current state of all services.
// Returns structured information including running count and health.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - *ComposeStatus: Current state of services
//   - error: Non-nil if status query fails
//
// # Examples
//
//	status, err := s.getComposeStatus(ctx)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Running: %d\n", status.Running)
//
// # Limitations
//
//   - Status reflects point-in-time state
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
func (s *DefaultStackManager) getComposeStatus(ctx context.Context) (*compose.ComposeStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	status, err := s.compose.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get compose status: %w", err)
	}

	return status, nil
}

// getMachineState retrieves the Podman machine state on macOS.
//
// # Description
//
// Queries the Podman machine status to determine if infrastructure
// is running. Returns "not_applicable" on non-macOS platforms.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout
//
// # Outputs
//
//   - string: Machine state ("running", "stopped", "not_found", "not_applicable")
//   - error: Non-nil if query fails
//
// # Examples
//
//	state, err := s.getMachineState(ctx)
//	if err != nil {
//	    log.Printf("machine status unknown: %v", err)
//	}
//
// # Limitations
//
//   - Only meaningful on macOS
//
// # Assumptions
//
//   - InfrastructureManager dependency is non-nil
//   - Config contains machine name
func (s *DefaultStackManager) getMachineState(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	machineName := s.getMachineName()

	machineStatus, err := s.infra.GetMachineStatus(ctx, machineName)
	if err != nil {
		return "", err
	}

	return s.machineStatusToString(machineStatus), nil
}

// getMachineName returns the configured Podman machine name.
//
// # Description
//
// Returns the machine name from config or the default name
// if not configured.
//
// # Inputs
//
//   - None (uses s.config)
//
// # Outputs
//
//   - string: Machine name
//
// # Examples
//
//	name := s.getMachineName()
//
// # Limitations
//
//   - None
//
// # Assumptions
//
//   - Config may be nil (returns default)
func (s *DefaultStackManager) getMachineName() string {
	if s.config != nil && s.config.Machine.Id != "" {
		return s.config.Machine.Id
	}
	return infra.DefaultMachineName
}

// machineStatusToString converts MachineStatus to a display string.
//
// # Description
//
// Maps the structured MachineStatus to a simple string for display
// in StackStatus.MachineState.
//
// # Inputs
//
//   - status: The MachineStatus from InfrastructureManager
//
// # Outputs
//
//   - string: "running", "stopped", or "not_found"
//
// # Examples
//
//	state := s.machineStatusToString(status)
//
// # Limitations
//
//   - Loses detailed state information
//
// # Assumptions
//
//   - status is non-nil
func (s *DefaultStackManager) machineStatusToString(status *infra.MachineStatus) string {
	if status == nil || !status.Exists {
		return "not_found"
	}
	if status.Running {
		return "running"
	}
	return "stopped"
}

// buildStackStatus creates a StackStatus from compose and machine info.
//
// # Description
//
// Combines compose service status and machine state into a unified
// StackStatus struct. Determines overall state based on running counts.
//
// # Inputs
//
//   - composeStatus: Status from ComposeExecutor
//   - machineState: Machine state string
//
// # Outputs
//
//   - *StackStatus: Combined status
//
// # Examples
//
//	status := s.buildStackStatus(composeStatus, "running")
//
// # Limitations
//
//   - Does not include resource usage (CPU/memory)
//
// # Assumptions
//
//   - composeStatus is non-nil
func (s *DefaultStackManager) buildStackStatus(composeStatus *compose.ComposeStatus, machineState string) *StackStatus {
	status := &StackStatus{
		MachineState:   machineState,
		RunningCount:   composeStatus.Running,
		StoppedCount:   composeStatus.Stopped,
		UnhealthyCount: composeStatus.Unhealthy,
	}

	// Determine overall state
	status.State = s.determineOverallState(composeStatus)

	// Calculate healthy count
	status.HealthyCount = s.countHealthyServices(composeStatus)

	// Convert service status
	status.Services = s.convertServiceStatus(composeStatus.Services)

	return status
}

// determineOverallState calculates the overall stack state.
//
// # Description
//
// Determines if the stack is "running", "stopped", "partial", or "unknown"
// based on container counts.
//
// # Inputs
//
//   - status: ComposeStatus with container counts
//
// # Outputs
//
//   - string: Overall state
//
// # Examples
//
//	state := s.determineOverallState(status)
//
// # Limitations
//
//   - Simple heuristic based on counts
//
// # Assumptions
//
//   - status is non-nil
func (s *DefaultStackManager) determineOverallState(status *compose.ComposeStatus) string {
	if status.Running == 0 && status.Stopped == 0 {
		return "unknown"
	}
	if status.Running > 0 && status.Stopped == 0 {
		return "running"
	}
	if status.Running == 0 && status.Stopped > 0 {
		return "stopped"
	}
	return "partial"
}

// countHealthyServices counts services with healthy status.
//
// # Description
//
// Iterates through services and counts those with Healthy=true.
// Services without health checks (Healthy=nil) are not counted.
//
// # Inputs
//
//   - status: ComposeStatus with service list
//
// # Outputs
//
//   - int: Number of healthy services
//
// # Examples
//
//	count := s.countHealthyServices(status)
//
// # Limitations
//
//   - Only counts services with explicit health checks
//
// # Assumptions
//
//   - status is non-nil
func (s *DefaultStackManager) countHealthyServices(status *compose.ComposeStatus) int {
	count := 0
	for _, svc := range status.Services {
		if svc.Healthy != nil && *svc.Healthy {
			count++
		}
	}
	return count
}

// convertServiceStatus converts ComposeExecutor's ServiceStatus to StackServiceInfo.
//
// # Description
//
// Maps the internal service status representation to the public
// StackServiceInfo format, including port mappings.
//
// # Inputs
//
//   - services: List of ServiceStatus from ComposeExecutor
//
// # Outputs
//
//   - []StackServiceInfo: Converted service info
//
// # Examples
//
//	info := s.convertServiceStatus(composeStatus.Services)
//
// # Limitations
//
//   - Resource usage (CPU/memory) not available from compose
//
// # Assumptions
//
//   - services is non-nil
func (s *DefaultStackManager) convertServiceStatus(services []compose.ServiceStatus) []StackServiceInfo {
	result := make([]StackServiceInfo, len(services))
	for i, svc := range services {
		result[i] = s.convertSingleServiceStatus(svc)
	}
	return result
}

// convertSingleServiceStatus converts a single ServiceStatus to StackServiceInfo.
//
// # Description
//
// Maps a single service's status including name, state, health, and ports.
// Sets CPU/memory to -1 (unavailable).
//
// # Inputs
//
//   - svc: Single ServiceStatus from ComposeExecutor
//
// # Outputs
//
//   - StackServiceInfo: Converted service info
//
// # Examples
//
//	info := s.convertSingleServiceStatus(svc)
//
// # Limitations
//
//   - CPU and memory not available from compose status
//
// # Assumptions
//
//   - svc contains valid data
func (s *DefaultStackManager) convertSingleServiceStatus(svc compose.ServiceStatus) StackServiceInfo {
	info := StackServiceInfo{
		Name:          svc.Name,
		ContainerName: svc.ContainerName,
		State:         svc.State,
		Healthy:       svc.Healthy,
		Image:         svc.Image,
		StartedAt:     svc.CreatedAt,
		CPUPercent:    -1, // Not available from compose status
		MemoryMB:      -1, // Not available from compose status
	}

	info.Ports = s.convertPortMappings(svc.Ports)

	return info
}

// convertPortMappings converts PortMapping slice to string slice.
//
// # Description
//
// Formats port mappings as "hostIP:hostPort:containerPort/protocol" strings
// for display purposes.
//
// # Inputs
//
//   - ports: List of PortMapping from ServiceStatus
//
// # Outputs
//
//   - []string: Formatted port strings
//
// # Examples
//
//	ports := s.convertPortMappings(svc.Ports)
//	// Returns: ["0.0.0.0:8080:8080/tcp"]
//
// # Limitations
//
//   - Protocol defaults to "tcp" if not specified
//
// # Assumptions
//
//   - ports may be nil (returns empty slice)
func (s *DefaultStackManager) convertPortMappings(ports []compose.PortMapping) []string {
	result := make([]string, len(ports))
	for i, port := range ports {
		result[i] = s.formatPortMapping(port)
	}
	return result
}

// formatPortMapping formats a single port mapping for display.
//
// # Description
//
// Creates a string representation of a port mapping in
// "hostIP:hostPort:containerPort/protocol" format.
//
// # Inputs
//
//   - port: Single PortMapping to format
//
// # Outputs
//
//   - string: Formatted port string
//
// # Examples
//
//	s := s.formatPortMapping(PortMapping{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 8080, Protocol: "tcp"})
//	// Returns: "0.0.0.0:8080:8080/tcp"
//
// # Limitations
//
//   - Assumes TCP if protocol is empty
//
// # Assumptions
//
//   - port contains valid data
func (s *DefaultStackManager) formatPortMapping(port compose.PortMapping) string {
	protocol := port.Protocol
	if protocol == "" {
		protocol = "tcp"
	}
	return fmt.Sprintf("%s:%d:%d/%s",
		port.HostIP, port.HostPort, port.ContainerPort, protocol)
}

// Logs streams logs from specified services.
//
// See interface documentation for full details.
func (s *DefaultStackManager) Logs(ctx context.Context, services []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Validate service names to prevent injection attacks
	if len(services) > 0 {
		if err := validateServiceNames(services); err != nil {
			return fmt.Errorf("invalid service name: %w", err)
		}
	}

	// Check if stack is running
	isRunning, err := s.isStackRunning(ctx)
	if err != nil {
		return err
	}
	if !isRunning {
		return ErrStackNotRunning
	}

	// Stream logs
	return s.streamLogs(ctx, services)
}

// =============================================================================
// Logs Phase Helpers
// =============================================================================

// streamLogs streams container logs to output.
//
// # Description
//
// Initiates log streaming from compose services. Follows logs in real-time
// until the context is cancelled. If services is empty, streams all services.
//
// # Inputs
//
//   - ctx: Context for cancellation (controls stream lifetime)
//   - services: Service names to stream (empty = all)
//
// # Outputs
//
//   - error: Non-nil if streaming fails to start
//
// # Examples
//
//	// Stream all services
//	err := s.streamLogs(ctx, nil)
//
//	// Stream specific services
//	err := s.streamLogs(ctx, []string{"orchestrator"})
//
// # Limitations
//
//   - Blocks until context cancellation
//   - Output goes to s.output
//
// # Assumptions
//
//   - ComposeExecutor dependency is non-nil
//   - At least one container is running
func (s *DefaultStackManager) streamLogs(ctx context.Context, services []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	logsOpts := s.buildLogsOptions(services)

	return s.compose.Logs(ctx, logsOpts, s.output)
}

// buildLogsOptions constructs LogsOptions for streaming.
//
// # Description
//
// Creates LogsOptions with follow enabled and the specified
// service filter.
//
// # Inputs
//
//   - services: Service names to stream (empty = all)
//
// # Outputs
//
//   - LogsOptions: Configured options for log streaming
//
// # Examples
//
//	opts := s.buildLogsOptions([]string{"orchestrator"})
//
// # Limitations
//
//   - Follow is always enabled
//   - No tail limit
//
// # Assumptions
//
//   - services may be nil or empty
func (s *DefaultStackManager) buildLogsOptions(services []string) compose.LogsOptions {
	return compose.LogsOptions{
		Follow:     true,
		Services:   services,
		Timestamps: true,
	}
}

// =============================================================================
// Mock Implementation
// =============================================================================

// MockStackManager is a test double for StackManager.
//
// # Description
//
// Provides a configurable mock implementation for testing.
// Each method can be configured with a custom function.
// Tracks all calls for verification.
//
// # Thread Safety
//
// Safe for concurrent use. Call tracking uses mutex.
//
// # Examples
//
//	mock := &MockStackManager{
//	    StartFunc: func(ctx context.Context, opts StartOptions) error {
//	        return nil // success
//	    },
//	}
//	err := mock.Start(ctx, StartOptions{})
//	assert.Equal(t, 1, len(mock.StartCalls))
type MockStackManager struct {
	// StartFunc is called when Start is invoked.
	StartFunc func(ctx context.Context, opts StartOptions) error

	// StopFunc is called when Stop is invoked.
	StopFunc func(ctx context.Context) error

	// DestroyFunc is called when Destroy is invoked.
	DestroyFunc func(ctx context.Context, removeFiles bool) error

	// StatusFunc is called when Status is invoked.
	StatusFunc func(ctx context.Context) (*StackStatus, error)

	// LogsFunc is called when Logs is invoked.
	LogsFunc func(ctx context.Context, services []string) error

	// StartCalls records all Start invocations.
	StartCalls []StartOptions

	// StopCalls records the number of Stop invocations.
	StopCalls int

	// DestroyCalls records all Destroy invocations (removeFiles value).
	DestroyCalls []bool

	// StatusCalls records the number of Status invocations.
	StatusCalls int

	// LogsCalls records all Logs invocations.
	LogsCalls [][]string

	// mu protects call tracking.
	mu sync.Mutex
}

// Start implements StackManager.
func (m *MockStackManager) Start(ctx context.Context, opts StartOptions) error {
	m.mu.Lock()
	m.StartCalls = append(m.StartCalls, opts)
	m.mu.Unlock()

	if m.StartFunc != nil {
		return m.StartFunc(ctx, opts)
	}
	return nil
}

// Stop implements StackManager.
func (m *MockStackManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.StopCalls++
	m.mu.Unlock()

	if m.StopFunc != nil {
		return m.StopFunc(ctx)
	}
	return nil
}

// Destroy implements StackManager.
func (m *MockStackManager) Destroy(ctx context.Context, removeFiles bool) error {
	m.mu.Lock()
	m.DestroyCalls = append(m.DestroyCalls, removeFiles)
	m.mu.Unlock()

	if m.DestroyFunc != nil {
		return m.DestroyFunc(ctx, removeFiles)
	}
	return nil
}

// Status implements StackManager.
func (m *MockStackManager) Status(ctx context.Context) (*StackStatus, error) {
	m.mu.Lock()
	m.StatusCalls++
	m.mu.Unlock()

	if m.StatusFunc != nil {
		return m.StatusFunc(ctx)
	}
	return &StackStatus{State: "running"}, nil
}

// Logs implements StackManager.
func (m *MockStackManager) Logs(ctx context.Context, services []string) error {
	m.mu.Lock()
	m.LogsCalls = append(m.LogsCalls, services)
	m.mu.Unlock()

	if m.LogsFunc != nil {
		return m.LogsFunc(ctx, services)
	}
	return nil
}

// =============================================================================
// Compile-time Interface Compliance
// =============================================================================

var _ StackManager = (*DefaultStackManager)(nil)
var _ StackManager = (*MockStackManager)(nil)
