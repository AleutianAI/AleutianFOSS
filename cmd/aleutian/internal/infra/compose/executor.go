// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/process"
)

// =============================================================================
// Error Definitions
// =============================================================================

var (
	// ErrComposeNotFound is returned when podman-compose binary is not available.
	ErrComposeNotFound = errors.New("podman-compose not found")

	// ErrComposeFileMissing is returned when a required compose file doesn't exist.
	ErrComposeFileMissing = errors.New("compose file not found")

	// ErrServiceNotFound is returned when a specified service doesn't exist.
	ErrServiceNotFound = errors.New("service not found")

	// ErrContainerNotRunning is returned for exec on stopped container.
	ErrContainerNotRunning = errors.New("container not running")

	// ErrCleanupPartial is returned when cleanup completes with some errors.
	ErrCleanupPartial = errors.New("cleanup completed with errors")

	// ErrInvalidConfig is returned when ComposeConfig is invalid.
	ErrInvalidConfig = errors.New("invalid compose configuration")

	// ErrInvalidEnvVar is returned when an environment variable key is invalid.
	// This prevents config injection attacks through malformed env var names.
	ErrInvalidEnvVar = errors.New("invalid environment variable")
)

// Compile-time assertions that sentinel errors satisfy error interface.
// These are exported for callers to use with errors.Is().
var (
	_ error = ErrComposeNotFound
	_ error = ErrComposeFileMissing
	_ error = ErrServiceNotFound
)

// envVarKeyRegex validates environment variable key names.
// Keys must:
//   - Start with a letter or underscore
//   - Contain only alphanumeric characters and underscores
//   - Not be empty
//
// This prevents shell metacharacter injection and other config attacks.
var envVarKeyRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// =============================================================================
// Interface Definition
// =============================================================================

// ComposeExecutor manages podman-compose operations for the Aleutian stack.
//
// # Description
//
// This interface abstracts all interactions with podman-compose, enabling
// testable orchestration of container services. It handles compose file
// layering (base, override, extensions), environment injection, and
// provides both graceful and forceful container management.
//
// # Security
//
//   - Validates compose file paths to prevent directory traversal
//   - Sanitizes environment variables before injection
//   - Does not log sensitive environment values (tokens, secrets)
//
// # Thread Safety
//
// Implementations must be safe for concurrent use. Operations that modify
// container state (Up, Down, ForceCleanup) should be serialized.
type ComposeExecutor interface {
	// Up starts services defined in the compose configuration.
	//
	// # Description
	//
	// Executes `podman-compose up -d` with optional build flag.
	// Composes files in order: base -> override -> extensions.
	// Injects environment variables from the provided map.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//   - opts: Configuration for the up operation
	//
	// # Outputs
	//
	//   - *ComposeResult: Execution result with stdout/stderr
	//   - error: If compose command fails
	//
	// # Example
	//
	//   result, err := executor.Up(ctx, UpOptions{
	//       ForceBuild: true,
	//       Services:   []string{"orchestrator", "weaviate"},
	//       Env: map[string]string{
	//           "OLLAMA_MODEL": "gpt-oss",
	//       },
	//   })
	//
	// # Limitations
	//
	//   - Does not verify service health after startup (use HealthChecker)
	//   - Build failures are reported but not retried
	//
	// # Assumptions
	//
	//   - Podman daemon is running and accessible
	//   - Compose files exist at configured paths
	//   - Required secrets are pre-created
	Up(ctx context.Context, opts UpOptions) (*ComposeResult, error)

	// Down stops and removes containers defined in compose configuration.
	//
	// # Description
	//
	// Executes `podman-compose down` with optional flags for orphan
	// removal and volume deletion. Attempts graceful shutdown first.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//   - opts: Configuration for the down operation
	//
	// # Outputs
	//
	//   - *ComposeResult: Execution result with stdout/stderr
	//   - error: If compose command fails (may trigger ForceCleanup)
	//
	// # Example
	//
	//   result, err := executor.Down(ctx, DownOptions{
	//       RemoveOrphans: true,
	//       RemoveVolumes: false,
	//       Timeout:       30 * time.Second,
	//   })
	//
	// # Limitations
	//
	//   - Orphan detection relies on compose project labels
	//   - Volume removal is irreversible
	//
	// # Assumptions
	//
	//   - Containers may already be stopped (not an error)
	Down(ctx context.Context, opts DownOptions) (*ComposeResult, error)

	// Stop stops all Aleutian containers with timeout-based escalation.
	//
	// # Description
	//
	// Stops containers using a multi-phase approach:
	//   1. Graceful stop with configurable timeout (default 10s)
	//   2. If containers remain, force stop with 0s timeout
	//
	// This ensures containers are stopped even if they ignore SIGTERM.
	// Use this before Down() to guarantee containers are stopped.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation
	//   - opts: Configuration for stop operation
	//
	// # Outputs
	//
	//   - *StopResult: Details of stopped containers
	//   - error: If stop cannot complete
	//
	// # Example
	//
	//   result, err := executor.Stop(ctx, StopOptions{
	//       GracefulTimeout: 10 * time.Second,
	//   })
	//   fmt.Printf("Stopped %d containers (graceful=%d, forced=%d)\n",
	//       result.TotalStopped, result.GracefulStopped, result.ForceStopped)
	//
	// # Limitations
	//
	//   - Does not remove containers (use Down() or ForceCleanup() after)
	//   - Cannot stop containers in different compose projects
	//
	// # Assumptions
	//
	//   - Podman daemon is accessible
	//   - Containers may already be stopped (not an error)
	Stop(ctx context.Context, opts StopOptions) (*StopResult, error)

	// Logs streams container logs to the provided writer.
	//
	// # Description
	//
	// Executes `podman-compose logs` with optional follow mode.
	// Streams logs to the provided io.Writer until context is cancelled.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation (controls stream lifetime)
	//   - opts: Configuration for log streaming
	//   - w: Writer to receive log output
	//
	// # Outputs
	//
	//   - error: If command fails to start or stream errors
	//
	// # Example
	//
	//   err := executor.Logs(ctx, LogsOptions{
	//       Follow:    true,
	//       Services:  []string{"orchestrator"},
	//       Tail:      100,
	//   }, os.Stdout)
	//
	// # Limitations
	//
	//   - Follow mode blocks until context cancellation
	//   - Large log volumes may consume significant memory
	//
	// # Assumptions
	//
	//   - At least one container exists (otherwise no output)
	Logs(ctx context.Context, opts LogsOptions, w io.Writer) error

	// Status returns the current state of compose services.
	//
	// # Description
	//
	// Executes `podman-compose ps` and parses output to determine
	// which services are running, their health status, and ports.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//
	// # Outputs
	//
	//   - *ComposeStatus: Current state of all services
	//   - error: If status query fails
	//
	// # Example
	//
	//   status, err := executor.Status(ctx)
	//   for _, svc := range status.Services {
	//       fmt.Printf("%s: %s (healthy=%v)\n", svc.Name, svc.State, svc.Healthy)
	//   }
	//
	// # Limitations
	//
	//   - Health status may lag actual container state
	//   - Parsing depends on podman ps --format json output structure
	//
	// # Assumptions
	//
	//   - Compose project exists (has been started at least once)
	//   - Includes all containers (running, stopped, exited) for debugging
	Status(ctx context.Context) (*ComposeStatus, error)

	// RemoveExitedContainers removes containers in exited/stopped state.
	//
	// # Description
	//
	// Removes only containers matching the configured name prefix that are in
	// exited state. Running containers are not affected. This is used before
	// starting containers to clean up stale containers from previous sessions
	// that would otherwise cause health check timeouts.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//
	// # Outputs
	//
	//   - int: Number of containers removed
	//   - error: Non-nil if the removal command fails
	//
	// # Limitations
	//
	//   - Only removes containers matching the configured name prefix
	//   - Does not remove running containers
	//
	// # Assumptions
	//
	//   - Podman daemon is accessible
	RemoveExitedContainers(ctx context.Context) (int, error)

	// ForceCleanup removes all Aleutian containers regardless of compose state.
	//
	// # Description
	//
	// Nuclear option when compose down fails. Executes in order:
	//   1. Force stop all matching containers (podman stop -t 0)
	//   2. Force remove by name filter (name=aleutian-*)
	//   3. Force remove by label filter (io.podman.compose.project=aleutianlocal)
	//   4. Remove matching pods (pods matching aleutian-*)
	//
	// Each step continues even if previous steps fail, collecting all errors.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//
	// # Outputs
	//
	//   - *CleanupResult: Details of stopped/removed containers/pods
	//   - error: If cleanup cannot complete (ErrCleanupPartial if partial)
	//
	// # Example
	//
	//   result, err := executor.ForceCleanup(ctx)
	//   if errors.Is(err, ErrCleanupPartial) {
	//       log.Printf("Cleanup completed with errors: %v", result.Errors)
	//   }
	//   fmt.Printf("Removed %d containers, %d pods\n",
	//       result.ContainersRemoved, result.PodsRemoved)
	//
	// # Limitations
	//
	//   - May leave orphaned volumes (use Down with RemoveVolumes for that)
	//   - Does not remove images
	//
	// # Assumptions
	//
	//   - Podman daemon is accessible
	ForceCleanup(ctx context.Context) (*CleanupResult, error)

	// Exec runs a command inside a running container.
	//
	// # Description
	//
	// Executes `podman-compose exec` to run a command in a service container.
	// Useful for health checks, debugging, and administrative tasks.
	//
	// # Inputs
	//
	//   - ctx: Context for cancellation and timeout
	//   - opts: Configuration for exec operation
	//
	// # Outputs
	//
	//   - *ExecResult: Command output and exit code
	//   - error: If exec fails
	//
	// # Example
	//
	//   result, err := executor.Exec(ctx, ExecOptions{
	//       Service: "weaviate",
	//       Command: []string{"wget", "-q", "-O-", "http://localhost:8080/v1/.well-known/ready"},
	//   })
	//
	// # Limitations
	//
	//   - Container must be running
	//   - No TTY support in non-interactive mode
	//
	// # Assumptions
	//
	//   - Service name matches compose service definition
	Exec(ctx context.Context, opts ExecOptions) (*ExecResult, error)

	// GetComposeFiles returns the list of compose files that will be used.
	//
	// # Description
	//
	// Returns the ordered list of compose files based on current configuration.
	// Useful for debugging and displaying configuration to users.
	//
	// # Inputs
	//
	//   - None
	//
	// # Outputs
	//
	//   - []string: Ordered list of compose file paths
	//
	// # Example
	//
	//   files := executor.GetComposeFiles()
	//   fmt.Println("Using compose files:", strings.Join(files, ", "))
	//
	// # Limitations
	//
	//   - Does not validate file existence
	//
	// # Assumptions
	//
	//   - Configuration has been set via constructor or SetConfig
	GetComposeFiles() []string
}

// =============================================================================
// Supporting Types
// =============================================================================

// ComposeConfig provides configuration for compose operations.
type ComposeConfig struct {
	// StackDir is the directory containing compose files.
	// All compose file paths are relative to this directory.
	StackDir string

	// ProjectName is the compose project name.
	// Default: "aleutianlocal"
	ProjectName string

	// BaseFile is the primary compose file name.
	// Default: "podman-compose.yml"
	BaseFile string

	// OverrideFile is the user override file name.
	// Optional, only used if file exists.
	// Default: "podman-compose.override.yml"
	OverrideFile string

	// ExtensionFiles are additional compose files to include.
	// Applied in order after base and override.
	ExtensionFiles []string

	// ContainerNamePrefix is the prefix for container names.
	// Used for filtering in ForceCleanup.
	// Default: "aleutian-"
	ContainerNamePrefix string

	// DefaultTimeout is the default timeout for compose operations.
	// Default: 5 minutes
	DefaultTimeout time.Duration
}

// UpOptions configures the Up operation.
type UpOptions struct {
	// ForceBuild rebuilds images even if they exist.
	// Maps to: --build flag
	ForceBuild bool

	// Services limits which services to start.
	// Empty means all services.
	Services []string

	// Env contains environment variables to inject.
	// These are passed to compose and available to all services.
	Env map[string]string

	// Detach runs containers in background.
	// Default: true (always detached for this interface)
	Detach bool

	// RemoveOrphans removes containers for services not defined.
	// Default: false
	RemoveOrphans bool

	// Timeout overrides the default operation timeout.
	// Zero means use DefaultTimeout from config.
	Timeout time.Duration
}

// DownOptions configures the Down operation.
type DownOptions struct {
	// RemoveOrphans removes containers for services not in compose file.
	// Maps to: --remove-orphans flag
	RemoveOrphans bool

	// RemoveVolumes removes named volumes declared in compose file.
	// Maps to: -v flag
	// WARNING: This is destructive and cannot be undone.
	RemoveVolumes bool

	// Timeout for graceful container shutdown.
	// Default: 10 seconds per container
	Timeout time.Duration
}

// StopOptions configures the Stop operation.
type StopOptions struct {
	// GracefulTimeout is the time to wait for graceful shutdown (SIGTERM).
	// After this timeout, containers are force-stopped with SIGKILL.
	// Default: 10 seconds
	GracefulTimeout time.Duration

	// Services limits which services to stop.
	// Empty means all Aleutian services (filter: name=aleutian-*).
	Services []string

	// SkipForceStop disables the automatic force-stop after graceful timeout.
	// If true, only sends SIGTERM and waits for GracefulTimeout.
	// Default: false (force-stop enabled)
	SkipForceStop bool
}

// StopResult contains the result of a Stop operation.
type StopResult struct {
	// TotalStopped is the total number of containers stopped.
	TotalStopped int

	// GracefulStopped is containers that stopped gracefully (SIGTERM).
	GracefulStopped int

	// ForceStopped is containers that required force stop (SIGKILL).
	ForceStopped int

	// AlreadyStopped is containers that were already stopped.
	AlreadyStopped int

	// ContainerNames lists all containers that were stopped.
	ContainerNames []string

	// Errors contains any non-fatal errors encountered.
	Errors []string
}

// LogsOptions configures the Logs operation.
type LogsOptions struct {
	// Follow streams logs continuously.
	// Maps to: -f flag
	Follow bool

	// Services limits which services to show logs for.
	// Empty means all services.
	Services []string

	// Tail limits output to last N lines per container.
	// Zero means all logs.
	Tail int

	// Timestamps prepends each line with timestamp.
	// Maps to: --timestamps flag
	Timestamps bool

	// Since shows logs since timestamp.
	// Maps to: --since flag
	Since time.Time
}

// ExecOptions configures the Exec operation.
type ExecOptions struct {
	// Service is the compose service name.
	// Required.
	Service string

	// Command is the command and arguments to execute.
	// Required, must have at least one element.
	Command []string

	// User overrides the user to run as.
	// Maps to: --user flag
	User string

	// WorkDir overrides the working directory.
	// Maps to: --workdir flag
	WorkDir string

	// Env contains additional environment variables.
	Env map[string]string
}

// ComposeResult contains the result of a compose operation.
type ComposeResult struct {
	// Success indicates if the operation completed without error.
	Success bool

	// ExitCode is the exit code of the compose command.
	ExitCode int

	// Stdout contains standard output.
	Stdout string

	// Stderr contains standard error.
	Stderr string

	// Duration is how long the operation took.
	Duration time.Duration

	// Command is the full command that was executed (for debugging).
	Command string
}

// ComposeStatus contains the current state of compose services.
type ComposeStatus struct {
	// Services contains status for each service.
	Services []ServiceStatus

	// Running is the count of running services.
	Running int

	// Stopped is the count of stopped services.
	Stopped int

	// Unhealthy is the count of unhealthy services.
	Unhealthy int
}

// ServiceStatus contains the status of a single service.
type ServiceStatus struct {
	// Name is the compose service name.
	Name string

	// ContainerName is the actual container name.
	ContainerName string

	// State is the container state (running, exited, etc.).
	State string

	// Healthy indicates health check status.
	// nil means no health check defined.
	Healthy *bool

	// Ports contains published port mappings.
	Ports []PortMapping

	// Image is the container image.
	Image string

	// CreatedAt is when the container was created.
	CreatedAt time.Time
}

// PortMapping represents a port binding.
type PortMapping struct {
	// HostIP is the host interface (usually 0.0.0.0).
	HostIP string

	// HostPort is the port on the host.
	HostPort int

	// ContainerPort is the port in the container.
	ContainerPort int

	// Protocol is tcp or udp.
	Protocol string
}

// CleanupResult contains details of a ForceCleanup operation.
type CleanupResult struct {
	// ContainersStopped is the number of containers force-stopped.
	ContainersStopped int

	// ContainersRemoved is the number of containers removed.
	ContainersRemoved int

	// PodsRemoved is the number of pods removed.
	PodsRemoved int

	// ContainerNames lists the names of removed containers.
	ContainerNames []string

	// PodNames lists the names of removed pods.
	PodNames []string

	// Errors contains any non-fatal errors encountered.
	Errors []string
}

// ExecResult contains the result of an Exec operation.
type ExecResult struct {
	// ExitCode is the exit code of the executed command.
	ExitCode int

	// Stdout contains standard output.
	Stdout string

	// Stderr contains standard error.
	Stderr string
}

// =============================================================================
// Default Implementation
// =============================================================================

// DefaultComposeExecutor implements ComposeExecutor using podman-compose.
type DefaultComposeExecutor struct {
	config     ComposeConfig
	proc       process.Manager
	osStatFunc func(string) (os.FileInfo, error)
	mu         sync.Mutex
}

// NewDefaultComposeExecutor creates a new ComposeExecutor with the given configuration.
//
// # Description
//
// Creates an executor configured for podman-compose operations.
// Validates the configuration and sets sensible defaults.
//
// # Inputs
//
//   - cfg: Compose configuration (StackDir required)
//   - proc: ProcessManager for command execution
//
// # Outputs
//
//   - *DefaultComposeExecutor: Configured executor
//   - error: If configuration is invalid
//
// # Example
//
//	executor, err := NewDefaultComposeExecutor(ComposeConfig{
//	    StackDir:    "/home/user/.aleutian",
//	    ProjectName: "aleutianlocal",
//	}, processManager)
//
// # Defaults Applied
//
//   - ProjectName: "aleutianlocal"
//   - BaseFile: "podman-compose.yml"
//   - OverrideFile: "podman-compose.override.yml"
//   - ContainerNamePrefix: "aleutian-"
//   - DefaultTimeout: 5 minutes
//
// # Limitations
//
//   - Does not verify podman-compose is installed (checked at runtime)
//   - Does not verify StackDir exists (checked at runtime)
//
// # Assumptions
//
//   - StackDir will exist when operations are executed
//   - ProcessManager is properly initialized and not nil
func NewDefaultComposeExecutor(cfg ComposeConfig, proc process.Manager) (*DefaultComposeExecutor, error) {
	if err := validateComposeConfig(&cfg); err != nil {
		return nil, err
	}

	applyComposeConfigDefaults(&cfg)

	return &DefaultComposeExecutor{
		config:     cfg,
		proc:       proc,
		osStatFunc: os.Stat,
	}, nil
}

// validateComposeConfig validates the ComposeConfig fields.
//
// # Description
//
// Ensures required fields are present and valid. Returns an error
// wrapping ErrInvalidConfig if validation fails.
//
// # Inputs
//
//   - cfg: Pointer to configuration to validate
//
// # Outputs
//
//   - error: ErrInvalidConfig with details if validation fails, nil otherwise
//
// # Example
//
//	if err := validateComposeConfig(&cfg); err != nil {
//	    return nil, err
//	}
//
// # Limitations
//
//   - Only validates that StackDir is non-empty
//   - Does not validate that StackDir exists on filesystem
//
// # Assumptions
//
//   - cfg is not nil
func validateComposeConfig(cfg *ComposeConfig) error {
	if cfg.StackDir == "" {
		return fmt.Errorf("%w: StackDir is required", ErrInvalidConfig)
	}
	return nil
}

// applyComposeConfigDefaults applies default values to empty fields.
//
// # Description
//
// Sets sensible defaults for optional configuration fields.
// Only modifies fields that are empty/zero-valued.
//
// # Inputs
//
//   - cfg: Pointer to configuration to modify
//
// # Outputs
//
//   - None (modifies cfg in place)
//
// # Example
//
//	applyComposeConfigDefaults(&cfg)
//	// cfg.ProjectName is now "aleutianlocal" if it was empty
//
// # Limitations
//
//   - Cannot distinguish between intentionally empty and unset fields
//
// # Assumptions
//
//   - cfg is not nil
//   - Called after validation
func applyComposeConfigDefaults(cfg *ComposeConfig) {
	if cfg.ProjectName == "" {
		cfg.ProjectName = "aleutianlocal"
	}
	if cfg.BaseFile == "" {
		cfg.BaseFile = "podman-compose.yml"
	}
	if cfg.OverrideFile == "" {
		cfg.OverrideFile = "podman-compose.override.yml"
	}
	if cfg.ContainerNamePrefix == "" {
		cfg.ContainerNamePrefix = "aleutian-"
	}
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = 5 * time.Minute
	}
}

// =============================================================================
// Interface Implementation
// =============================================================================

// Up starts services defined in the compose configuration.
//
// # Description
//
// Executes `podman-compose up -d` with optional build flag.
// Composes files in order: base -> override -> extensions.
// Injects environment variables from the provided map.
// Acquires mutex to serialize with other mutating operations.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//   - opts: Configuration for the up operation including:
//   - ForceBuild: Whether to rebuild images
//   - Services: Specific services to start (empty = all)
//   - Env: Environment variables to inject
//   - RemoveOrphans: Whether to remove orphan containers
//   - Timeout: Override default timeout
//
// # Outputs
//
//   - *ComposeResult: Contains stdout, stderr, exit code, duration
//   - error: If compose command fails or context is cancelled
//
// # Example
//
//	result, err := executor.Up(ctx, UpOptions{
//	    ForceBuild: true,
//	    Env: map[string]string{"OLLAMA_MODEL": "gpt-oss"},
//	})
//	if err != nil {
//	    log.Printf("Up failed: %v\nStderr: %s", err, result.Stderr)
//	}
//
// # Limitations
//
//   - Does not verify service health after startup (use HealthChecker)
//   - Build failures are reported but not retried
//   - Blocks until containers are started (not until healthy)
//
// # Assumptions
//
//   - Podman daemon is running and accessible
//   - Compose files exist at configured paths
//   - Required secrets are pre-created
func (e *DefaultComposeExecutor) Up(ctx context.Context, opts UpOptions) (*ComposeResult, error) {
	// Validate env vars before proceeding to prevent config injection
	if err := e.validateEnvVars(opts.Env); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	args := e.buildComposeFileArgs()
	args = append(args, "up", "-d")

	if opts.ForceBuild {
		args = append(args, "--build")
	}
	if opts.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	if len(opts.Services) > 0 {
		args = append(args, opts.Services...)
	}

	timeout := e.resolveTimeout(opts.Timeout)

	return e.runCompose(ctx, args, opts.Env, timeout)
}

// Down stops and removes containers defined in compose configuration.
//
// # Description
//
// Executes `podman-compose down` with optional flags for orphan
// removal and volume deletion. Acquires mutex to serialize with
// other mutating operations.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//   - opts: Configuration for the down operation including:
//   - RemoveOrphans: Remove containers for undefined services
//   - RemoveVolumes: Remove named volumes (destructive!)
//   - Timeout: Override default timeout
//
// # Outputs
//
//   - *ComposeResult: Contains stdout, stderr, exit code, duration
//   - error: If compose command fails or context is cancelled
//
// # Example
//
//	result, err := executor.Down(ctx, DownOptions{
//	    RemoveOrphans: true,
//	    RemoveVolumes: false,
//	})
//	if err != nil {
//	    // May want to call ForceCleanup() as fallback
//	    log.Printf("Down failed: %v", err)
//	}
//
// # Limitations
//
//   - Orphan detection relies on compose project labels
//   - Volume removal is irreversible
//   - May fail if containers are stuck (use Stop() first)
//
// # Assumptions
//
//   - Containers may already be stopped (not an error)
//   - Podman daemon is accessible
func (e *DefaultComposeExecutor) Down(ctx context.Context, opts DownOptions) (*ComposeResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	args := e.buildComposeFileArgs()
	args = append(args, "down")

	if opts.RemoveOrphans {
		args = append(args, "--remove-orphans")
	}
	if opts.RemoveVolumes {
		args = append(args, "-v")
	}

	timeout := e.resolveTimeout(opts.Timeout)

	return e.runCompose(ctx, args, nil, timeout)
}

// Stop stops all Aleutian containers with timeout-based escalation.
//
// # Description
//
// Stops containers using a multi-phase approach:
//  1. Graceful stop: Sends SIGTERM, waits GracefulTimeout (default 10s)
//  2. Force stop: Sends SIGKILL to any remaining containers
//
// This ensures containers are stopped even if they ignore SIGTERM.
// Acquires mutex to serialize with other mutating operations.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - opts: Configuration for stop operation including:
//   - GracefulTimeout: Time to wait for SIGTERM (default 10s)
//   - Services: Specific services to stop (empty = all)
//   - SkipForceStop: If true, don't escalate to SIGKILL
//
// # Outputs
//
//   - *StopResult: Contains counts of graceful/forced stops and errors
//   - error: If stop cannot complete (partial results still returned)
//
// # Example
//
//	result, err := executor.Stop(ctx, StopOptions{
//	    GracefulTimeout: 15 * time.Second,
//	})
//	fmt.Printf("Stopped: %d graceful, %d forced\n",
//	    result.GracefulStopped, result.ForceStopped)
//
// # Limitations
//
//   - Does not remove containers (use Down() or ForceCleanup() after)
//   - Cannot stop containers in different compose projects
//   - Error list may contain non-fatal errors even on success
//
// # Assumptions
//
//   - Podman daemon is accessible
//   - Containers may already be stopped (counted as AlreadyStopped)
func (e *DefaultComposeExecutor) Stop(ctx context.Context, opts StopOptions) (*StopResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := &StopResult{
		ContainerNames: []string{},
		Errors:         []string{},
	}

	gracefulTimeout := e.resolveGracefulTimeout(opts.GracefulTimeout)

	// Get list of running containers before stopping
	runningBefore, err := e.listRunningContainers(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("failed to list containers: %v", err))
	}

	// Phase 1: Graceful stop with timeout
	gracefulErr := e.executeGracefulStop(ctx, gracefulTimeout)
	if gracefulErr != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("graceful stop: %v", gracefulErr))
	}

	// Check which containers stopped gracefully
	runningAfterGraceful, _ := e.listRunningContainers(ctx)
	result.GracefulStopped = len(runningBefore) - len(runningAfterGraceful)

	// Phase 2: Force stop if containers remain and not skipped
	if !opts.SkipForceStop && len(runningAfterGraceful) > 0 {
		forceErr := e.executeForceStop(ctx)
		if forceErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("force stop: %v", forceErr))
		}

		runningAfterForce, _ := e.listRunningContainers(ctx)
		result.ForceStopped = len(runningAfterGraceful) - len(runningAfterForce)
	}

	// Calculate totals
	result.TotalStopped = result.GracefulStopped + result.ForceStopped

	// Get names of stopped containers
	for _, name := range runningBefore {
		result.ContainerNames = append(result.ContainerNames, name)
	}

	if len(result.Errors) > 0 {
		return result, fmt.Errorf("stop completed with errors: %v", result.Errors)
	}
	return result, nil
}

// Logs streams container logs to the provided writer.
//
// # Description
//
// Executes `podman-compose logs` with optional follow mode.
// Streams logs to the provided io.Writer until context is cancelled.
// Does not acquire mutex (read-only operation).
//
// # Inputs
//
//   - ctx: Context for cancellation (controls stream lifetime)
//   - opts: Configuration for log streaming including:
//   - Follow: Stream continuously until cancelled
//   - Services: Specific services to show (empty = all)
//   - Tail: Limit to last N lines (0 = all)
//   - Timestamps: Prepend timestamp to each line
//   - Since: Show logs since this time
//   - w: Writer to receive log output
//
// # Outputs
//
//   - error: If command fails to start or stream errors
//
// # Example
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	err := executor.Logs(ctx, LogsOptions{
//	    Follow:   true,
//	    Services: []string{"orchestrator"},
//	    Tail:     100,
//	}, os.Stdout)
//
// # Limitations
//
//   - Follow mode blocks until context cancellation
//   - Large log volumes may consume significant memory
//   - No built-in rate limiting
//
// # Assumptions
//
//   - At least one container exists (otherwise no output)
//   - Writer is safe for concurrent writes if needed
func (e *DefaultComposeExecutor) Logs(ctx context.Context, opts LogsOptions, w io.Writer) error {
	args := e.buildComposeFileArgs()
	args = append(args, "logs")

	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", opts.Tail))
	}
	if opts.Timestamps {
		args = append(args, "--timestamps")
	}
	if !opts.Since.IsZero() {
		args = append(args, "--since", opts.Since.Format(time.RFC3339))
	}
	if len(opts.Services) > 0 {
		args = append(args, opts.Services...)
	}

	return e.runComposeStreaming(ctx, args, w)
}

// Status returns the current state of compose services.
//
// # Description
//
// Executes `podman ps` with JSON output and parses the result.
// Returns status for all containers (running, stopped, exited).
// Does not acquire mutex (read-only operation).
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//
// # Outputs
//
//   - *ComposeStatus: Contains service list and counts
//   - error: If status query or parsing fails
//
// # Example
//
//	status, err := executor.Status(ctx)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Running: %d, Stopped: %d, Unhealthy: %d\n",
//	    status.Running, status.Stopped, status.Unhealthy)
//
// # Limitations
//
//   - Health status may lag actual container state
//   - Parsing depends on podman ps --format json output structure
//   - Does not include containers from other projects
//
// # Assumptions
//
//   - Compose project exists (has been started at least once)
//   - Podman daemon is accessible
func (e *DefaultComposeExecutor) Status(ctx context.Context) (*ComposeStatus, error) {
	args := []string{
		"ps",
		"-a",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
		"--format", "json",
	}

	output, err := e.runPodman(ctx, args, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	return e.parseContainerStatus(output.Stdout)
}

// RemoveExitedContainers removes containers in exited state matching the name prefix.
//
// # Description
//
// Runs `podman rm` with filters for the configured container name prefix and
// exited status. Only affects stopped/exited containers — running containers
// are untouched. Returns the count of containers actually removed.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//
// # Outputs
//
//   - int: Number of containers removed
//   - error: Non-nil if the podman command fails
//
// # Limitations
//
//   - Only removes containers matching the configured name prefix
//   - Does not remove running containers
//
// # Assumptions
//
//   - Podman daemon is accessible
func (e *DefaultComposeExecutor) RemoveExitedContainers(ctx context.Context) (int, error) {
	args := []string{
		"rm",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
		"--filter", "status=exited",
	}

	output, err := e.runPodman(ctx, args, 30*time.Second)
	if err != nil {
		return 0, fmt.Errorf("removing exited containers: %w", err)
	}

	removed := e.parseLines(output.Stdout)
	return len(removed), nil
}

// ForceCleanup removes all Aleutian containers regardless of compose state.
//
// # Description
//
// Nuclear option when compose down fails. Executes four steps:
//  1. Force stop all matching containers (podman stop -t 0)
//  2. Force remove by name filter (name=aleutian-*)
//  3. Force remove by label filter (io.podman.compose.project=...)
//  4. Remove matching pods
//
// Each step continues even if previous steps fail.
// Acquires mutex to serialize with other mutating operations.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//
// # Outputs
//
//   - *CleanupResult: Contains counts and error list
//   - error: ErrCleanupPartial if some steps failed, nil otherwise
//
// # Example
//
//	result, err := executor.ForceCleanup(ctx)
//	if errors.Is(err, ErrCleanupPartial) {
//	    log.Printf("Partial cleanup: %v", result.Errors)
//	}
//	fmt.Printf("Removed %d containers\n", result.ContainersRemoved)
//
// # Limitations
//
//   - May leave orphaned volumes (use Down with RemoveVolumes)
//   - Does not remove images
//   - Cannot distinguish containers that failed vs succeeded
//
// # Assumptions
//
//   - Podman daemon is accessible
//   - Some failures are expected (container may not exist)
func (e *DefaultComposeExecutor) ForceCleanup(ctx context.Context) (*CleanupResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	result := &CleanupResult{
		ContainerNames: []string{},
		PodNames:       []string{},
		Errors:         []string{},
	}

	// Step 1: Force stop all containers
	e.executeForceStopForCleanup(ctx, result)

	// Step 2: Remove by name filter
	e.removeContainersByName(ctx, result)

	// Step 3: Remove by label filter
	e.removeContainersByLabel(ctx, result)

	// Step 4: Remove pods
	e.removePods(ctx, result)

	if len(result.Errors) > 0 {
		return result, ErrCleanupPartial
	}
	return result, nil
}

// Exec runs a command inside a running container.
//
// # Description
//
// Executes `podman-compose exec` to run a command in a service container.
// Uses -T flag to disable pseudo-TTY for non-interactive use.
// Does not acquire mutex (does not modify container state).
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout
//   - opts: Configuration for exec operation including:
//   - Service: Compose service name (required)
//   - Command: Command and arguments (required)
//   - User: Override user to run as
//   - WorkDir: Override working directory
//   - Env: Additional environment variables
//
// # Outputs
//
//   - *ExecResult: Contains stdout, stderr, exit code
//   - error: ErrContainerNotRunning if container stopped, other errors
//
// # Example
//
//	result, err := executor.Exec(ctx, ExecOptions{
//	    Service: "weaviate",
//	    Command: []string{"wget", "-q", "-O-", "http://localhost:8080/health"},
//	})
//	if err == ErrContainerNotRunning {
//	    log.Println("Container not running, skipping health check")
//	}
//
// # Limitations
//
//   - Container must be running
//   - No TTY support (non-interactive only)
//   - Cannot exec into stopped containers
//
// # Assumptions
//
//   - Service name matches compose service definition
//   - Container has the required commands available
func (e *DefaultComposeExecutor) Exec(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	if err := e.validateExecOptions(opts); err != nil {
		return nil, err
	}
	// Validate env vars before proceeding to prevent config injection
	if err := e.validateEnvVars(opts.Env); err != nil {
		return nil, err
	}

	args := e.buildExecArgs(opts)
	result, err := e.runCompose(ctx, args, nil, e.config.DefaultTimeout)

	if err != nil {
		if e.isContainerNotRunningError(result) {
			return nil, ErrContainerNotRunning
		}
		return nil, err
	}

	return &ExecResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, nil
}

// GetComposeFiles returns the list of compose files that will be used.
//
// # Description
//
// Returns the ordered list of compose files based on current configuration.
// Checks filesystem for existence of optional files (override, extensions).
// Does not acquire mutex (read-only, no external calls).
//
// # Inputs
//
//   - None
//
// # Outputs
//
//   - []string: Ordered list of absolute compose file paths
//
// # Example
//
//	files := executor.GetComposeFiles()
//	// ["/home/user/.aleutian/podman-compose.yml",
//	//  "/home/user/.aleutian/podman-compose.override.yml"]
//
// # Limitations
//
//   - Does not validate file content/syntax
//   - Returns paths even if files have been deleted since check
//
// # Assumptions
//
//   - Base file always exists (not checked here)
//   - osStatFunc is properly initialized
func (e *DefaultComposeExecutor) GetComposeFiles() []string {
	files := []string{}

	// Base file (required, always included)
	basePath := filepath.Join(e.config.StackDir, e.config.BaseFile)
	files = append(files, basePath)

	// Override file (only if exists)
	overridePath := filepath.Join(e.config.StackDir, e.config.OverrideFile)
	if e.fileExists(overridePath) {
		files = append(files, overridePath)
	}

	// Extension files (only if exist)
	for _, ext := range e.config.ExtensionFiles {
		extPath := filepath.Join(e.config.StackDir, ext)
		if e.fileExists(extPath) {
			files = append(files, extPath)
		}
	}

	return files
}

// =============================================================================
// Private Helper Methods
// =============================================================================

// buildComposeFileArgs builds the -f arguments for compose files.
//
// # Description
//
// Constructs the file arguments in the correct order:
// base -> override (if exists) -> extensions (if exist).
// Uses GetComposeFiles to determine which files to include.
//
// # Inputs
//
//   - None (uses executor's configuration)
//
// # Outputs
//
//   - []string: Arguments including -f flags for each compose file
//
// # Example
//
//	args := e.buildComposeFileArgs()
//	// ["-f", "/path/base.yml", "-f", "/path/override.yml"]
//
// # Limitations
//
//   - Does not validate that files are readable
//
// # Assumptions
//
//   - GetComposeFiles returns valid paths
func (e *DefaultComposeExecutor) buildComposeFileArgs() []string {
	args := []string{}

	for _, file := range e.GetComposeFiles() {
		args = append(args, "-f", file)
	}

	return args
}

// runCompose executes a podman-compose command.
//
// # Description
//
// Runs podman-compose with the given arguments, environment, and timeout.
// Logs the command being executed (with sensitive values redacted).
// Creates a child context with the specified timeout.
//
// # Inputs
//
//   - ctx: Parent context for cancellation
//   - args: Command arguments (including -f flags)
//   - env: Environment variables to inject (may be nil)
//   - timeout: Maximum time for command execution
//
// # Outputs
//
//   - *ComposeResult: Contains stdout, stderr, exit code, duration, command
//   - error: If command fails or times out
//
// # Example
//
//	result, err := e.runCompose(ctx, []string{"-f", "compose.yml", "up"}, nil, 5*time.Minute)
//
// # Limitations
//
//   - Captures all output in memory (not suitable for streaming)
//   - Timeout applies to entire command, not individual operations
//
// # Assumptions
//
//   - ProcessManager.RunCommandWithEnv is implemented correctly
//   - StackDir exists and is accessible
func (e *DefaultComposeExecutor) runCompose(ctx context.Context, args []string, env map[string]string, timeout time.Duration) (*ComposeResult, error) {
	start := time.Now()

	cmdEnv := e.buildCommandEnvironment(env)
	cmdStr := fmt.Sprintf("podman-compose %s", strings.Join(args, " "))
	e.logCommand(cmdStr, env)

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, exitCode, err := e.proc.RunInDir(execCtx, e.config.StackDir, cmdEnv, "podman-compose", args...)

	result := &ComposeResult{
		Success:  exitCode == 0 && err == nil,
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		Duration: time.Since(start),
		Command:  cmdStr,
	}

	if err != nil {
		return result, fmt.Errorf("compose command failed: %w", err)
	}
	if exitCode != 0 {
		return result, fmt.Errorf("compose command exited with code %d: %s", exitCode, stderr)
	}

	return result, nil
}

// runComposeStreaming executes a podman-compose command with streaming output.
//
// # Description
//
// Runs podman-compose and streams output to the provided writer.
// Used for logs command with follow mode. Does not capture output.
//
// # Inputs
//
//   - ctx: Context for cancellation (terminates streaming)
//   - args: Command arguments
//   - w: Writer for streaming output
//
// # Outputs
//
//   - error: If command fails to start
//
// # Example
//
//	err := e.runComposeStreaming(ctx, []string{"logs", "-f"}, os.Stdout)
//
// # Limitations
//
//   - Cannot capture output (streams directly to writer)
//   - Exit code not available until stream ends
//
// # Assumptions
//
//   - ProcessManager.RunCommandStreaming is implemented
//   - Writer handles concurrent writes appropriately
func (e *DefaultComposeExecutor) runComposeStreaming(ctx context.Context, args []string, w io.Writer) error {
	cmdStr := fmt.Sprintf("podman-compose %s", strings.Join(args, " "))
	e.logCommand(cmdStr, nil)

	return e.proc.RunStreaming(ctx, e.config.StackDir, w, "podman-compose", args...)
}

// runPodman executes a direct podman command.
//
// # Description
//
// Runs podman (not compose) for operations like stop, rm, ps.
// Used when we need direct container manipulation rather than
// going through compose.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - args: Command arguments
//   - timeout: Command timeout
//
// # Outputs
//
//   - *ComposeResult: Contains stdout, stderr, exit code, duration
//   - error: If command fails or times out
//
// # Example
//
//	result, err := e.runPodman(ctx, []string{"ps", "-a", "--format", "json"}, 30*time.Second)
//
// # Limitations
//
//   - Does not use compose file layering
//   - No environment injection
//
// # Assumptions
//
//   - podman binary is in PATH
//   - Podman daemon is accessible
func (e *DefaultComposeExecutor) runPodman(ctx context.Context, args []string, timeout time.Duration) (*ComposeResult, error) {
	start := time.Now()
	cmdStr := fmt.Sprintf("podman %s", strings.Join(args, " "))

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, exitCode, err := e.proc.RunInDir(execCtx, "", nil, "podman", args...)

	result := &ComposeResult{
		Success:  exitCode == 0 && err == nil,
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
		Duration: time.Since(start),
		Command:  cmdStr,
	}

	if err != nil {
		return result, fmt.Errorf("podman command failed: %w", err)
	}
	if exitCode != 0 {
		return result, fmt.Errorf("podman command exited with code %d: %s", exitCode, stderr)
	}

	return result, nil
}

// listRunningContainers returns names of running containers matching the prefix.
//
// # Description
//
// Queries podman for running containers with the configured name prefix.
// Used to track which containers are running before/after stop operations.
//
// # Inputs
//
//   - ctx: Context for cancellation
//
// # Outputs
//
//   - []string: Container IDs of running containers
//   - error: If query fails
//
// # Example
//
//	running, err := e.listRunningContainers(ctx)
//	fmt.Printf("Found %d running containers\n", len(running))
//
// # Limitations
//
//   - Returns container IDs, not names
//   - Only finds containers matching name prefix
//
// # Assumptions
//
//   - ContainerNamePrefix is properly configured
func (e *DefaultComposeExecutor) listRunningContainers(ctx context.Context) ([]string, error) {
	args := []string{
		"ps", "-q",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
		"--filter", "status=running",
	}

	output, err := e.runPodman(ctx, args, 30*time.Second)
	if err != nil {
		return nil, err
	}

	return e.parseLines(output.Stdout), nil
}

// parseContainerStatus parses podman ps JSON output to ComposeStatus.
//
// # Description
//
// Converts JSON container list to structured status.
// Extracts service names from container names, parses health status,
// and counts running/stopped/unhealthy containers.
//
// # Inputs
//
//   - jsonOutput: Raw JSON from podman ps --format json
//
// # Outputs
//
//   - *ComposeStatus: Parsed status with service list and counts
//   - error: If JSON parsing fails
//
// # Example
//
//	status, err := e.parseContainerStatus(`[{"Names":["aleutian-weaviate-1"],...}]`)
//
// # Limitations
//
//   - Depends on specific podman JSON output format
//   - Health status extracted from Status string (may be fragile)
//
// # Assumptions
//
//   - JSON format matches expected structure
//   - Container names follow pattern: prefix-servicename-N
func (e *DefaultComposeExecutor) parseContainerStatus(jsonOutput string) (*ComposeStatus, error) {
	status := &ComposeStatus{
		Services: []ServiceStatus{},
	}

	if strings.TrimSpace(jsonOutput) == "" {
		return status, nil
	}

	var containers []struct {
		Names   []string `json:"Names"`
		State   string   `json:"State"`
		Status  string   `json:"Status"`
		Image   string   `json:"Image"`
		Created any      `json:"Created"` // Can be string or Unix timestamp (number)
		Ports   []struct {
			HostIP        string `json:"host_ip"`
			HostPort      int    `json:"host_port"`
			ContainerPort int    `json:"container_port"`
			Protocol      string `json:"protocol"`
		} `json:"Ports"`
	}

	if err := json.Unmarshal([]byte(jsonOutput), &containers); err != nil {
		return nil, fmt.Errorf("failed to parse container JSON: %w", err)
	}

	for _, c := range containers {
		svc := e.buildServiceStatus(c.Names, c.State, c.Status, c.Image, c.Ports)
		status.Services = append(status.Services, svc)
		e.updateStatusCounts(status, c.State, svc.Healthy)
	}

	return status, nil
}

// buildServiceStatus creates a ServiceStatus from container data.
//
// # Description
//
// Constructs a ServiceStatus struct from raw container data.
// Extracts service name from container name, parses health status,
// and converts port mappings.
//
// # Inputs
//
//   - names: Container names array (uses first element)
//   - state: Container state string
//   - statusStr: Container status string (contains health info)
//   - image: Container image name
//   - ports: Port mapping data
//
// # Outputs
//
//   - ServiceStatus: Populated service status struct
//
// # Example
//
//	svc := e.buildServiceStatus(
//	    []string{"aleutian-weaviate-1"},
//	    "running",
//	    "Up 2 hours (healthy)",
//	    "weaviate:latest",
//	    []portData{...},
//	)
//
// # Limitations
//
//   - Assumes first name in array is primary name
//   - Health parsing is string-based
//
// # Assumptions
//
//   - Names array has at least one element (or empty name used)
func (e *DefaultComposeExecutor) buildServiceStatus(names []string, state, statusStr, image string, ports []struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}) ServiceStatus {
	name := ""
	if len(names) > 0 {
		name = names[0]
	}

	svc := ServiceStatus{
		Name:          e.extractServiceName(name),
		ContainerName: name,
		State:         state,
		Image:         image,
		Ports:         []PortMapping{},
	}

	svc.Healthy = e.parseHealthStatus(statusStr)

	for _, p := range ports {
		svc.Ports = append(svc.Ports, PortMapping{
			HostIP:        p.HostIP,
			HostPort:      p.HostPort,
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
		})
	}

	return svc
}

// updateStatusCounts updates the running/stopped/unhealthy counts.
//
// # Description
//
// Increments the appropriate counter in ComposeStatus based on
// container state and health.
//
// # Inputs
//
//   - status: ComposeStatus to update
//   - state: Container state string
//   - healthy: Health status pointer (nil if no healthcheck)
//
// # Outputs
//
//   - None (modifies status in place)
//
// # Example
//
//	e.updateStatusCounts(status, "running", &true)
//	// status.Running++
//
// # Limitations
//
//   - Only recognizes "running" and "exited"/"stopped" states
//
// # Assumptions
//
//   - status is not nil
func (e *DefaultComposeExecutor) updateStatusCounts(status *ComposeStatus, state string, healthy *bool) {
	switch state {
	case "running":
		status.Running++
	case "exited", "stopped":
		status.Stopped++
	}
	if healthy != nil && !*healthy {
		status.Unhealthy++
	}
}

// parseHealthStatus extracts health status from status string.
//
// # Description
//
// Parses the status string from podman ps to determine health.
// Looks for "healthy" or "unhealthy" in the string.
//
// # Inputs
//
//   - statusStr: Status string like "Up 2 hours (healthy)"
//
// # Outputs
//
//   - *bool: true if healthy, false if unhealthy, nil if no healthcheck
//
// # Example
//
//	health := e.parseHealthStatus("Up 2 hours (healthy)")
//	// *health == true
//
// # Limitations
//
//   - String-based parsing may break with different podman versions
//
// # Assumptions
//
//   - Status format is consistent
func (e *DefaultComposeExecutor) parseHealthStatus(statusStr string) *bool {
	if strings.Contains(statusStr, "healthy") && !strings.Contains(statusStr, "unhealthy") {
		healthy := true
		return &healthy
	}
	if strings.Contains(statusStr, "unhealthy") {
		healthy := false
		return &healthy
	}
	return nil
}

// extractServiceName extracts compose service name from container name.
//
// # Description
//
// Container names follow pattern: prefix-servicename-N
// This extracts the service name portion by removing prefix
// and trailing numeric suffix.
//
// # Inputs
//
//   - containerName: Full container name like "aleutian-weaviate-1"
//
// # Outputs
//
//   - string: Service name like "weaviate"
//
// # Example
//
//	name := e.extractServiceName("aleutian-go-orchestrator-1")
//	// name == "go-orchestrator"
//
// # Limitations
//
//   - Assumes specific naming pattern
//   - May not work if service name contains numbers at end
//
// # Assumptions
//
//   - Container name follows compose naming convention
func (e *DefaultComposeExecutor) extractServiceName(containerName string) string {
	name := strings.TrimPrefix(containerName, e.config.ContainerNamePrefix)

	parts := strings.Split(name, "-")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		if _, err := fmt.Sscanf(lastPart, "%d", new(int)); err == nil {
			parts = parts[:len(parts)-1]
		}
	}

	return strings.Join(parts, "-")
}

// executeGracefulStop executes graceful stop with specified timeout.
//
// # Description
//
// Runs podman stop with the graceful timeout, sending SIGTERM
// and waiting for containers to stop gracefully.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - timeout: Time to wait for graceful shutdown
//
// # Outputs
//
//   - error: If stop command fails
//
// # Example
//
//	err := e.executeGracefulStop(ctx, 10*time.Second)
//
// # Limitations
//
//   - Stops all containers matching prefix, cannot be selective
//
// # Assumptions
//
//   - Containers may already be stopped (not an error)
func (e *DefaultComposeExecutor) executeGracefulStop(ctx context.Context, timeout time.Duration) error {
	args := []string{
		"stop",
		"-t", fmt.Sprintf("%d", int(timeout.Seconds())),
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
	}

	_, err := e.runPodman(ctx, args, e.config.DefaultTimeout)
	return err
}

// executeForceStop executes force stop with zero timeout.
//
// # Description
//
// Runs podman stop with 0 timeout, immediately sending SIGKILL.
// Used when graceful stop doesn't stop all containers.
//
// # Inputs
//
//   - ctx: Context for cancellation
//
// # Outputs
//
//   - error: If stop command fails
//
// # Example
//
//	err := e.executeForceStop(ctx)
//
// # Limitations
//
//   - Forceful, doesn't give containers time to clean up
//
// # Assumptions
//
//   - Containers may already be stopped (not an error)
func (e *DefaultComposeExecutor) executeForceStop(ctx context.Context) error {
	args := []string{
		"stop",
		"-t", "0",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
	}

	_, err := e.runPodman(ctx, args, 30*time.Second)
	return err
}

// executeForceStopForCleanup executes force stop as part of cleanup.
//
// # Description
//
// Force stops containers during cleanup, recording any errors.
// Part of the ForceCleanup multi-step process.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: CleanupResult to record errors
//
// # Outputs
//
//   - None (modifies result in place)
//
// # Example
//
//	e.executeForceStopForCleanup(ctx, result)
//
// # Limitations
//
//   - Errors are recorded but don't stop cleanup
//
// # Assumptions
//
//   - result is not nil
func (e *DefaultComposeExecutor) executeForceStopForCleanup(ctx context.Context, result *CleanupResult) {
	args := []string{
		"stop",
		"-t", "0",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
	}

	if _, err := e.runPodman(ctx, args, 30*time.Second); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("force stop: %v", err))
	}
}

// removeContainersByName removes containers by name filter.
//
// # Description
//
// Removes containers matching the name prefix filter.
// Part of the ForceCleanup multi-step process.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: CleanupResult to record results and errors
//
// # Outputs
//
//   - None (modifies result in place)
//
// # Example
//
//	e.removeContainersByName(ctx, result)
//
// # Limitations
//
//   - Errors are recorded but don't stop cleanup
//
// # Assumptions
//
//   - result is not nil
func (e *DefaultComposeExecutor) removeContainersByName(ctx context.Context, result *CleanupResult) {
	args := []string{
		"rm", "-f",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
	}

	if output, err := e.runPodman(ctx, args, 30*time.Second); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("remove by name: %v", err))
	} else {
		removed := e.parseLines(output.Stdout)
		result.ContainerNames = append(result.ContainerNames, removed...)
		result.ContainersRemoved += len(removed)
	}
}

// removeContainersByLabel removes containers by label filter.
//
// # Description
//
// Removes containers matching the compose project label.
// Catches containers that may not match the name filter.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: CleanupResult to record results and errors
//
// # Outputs
//
//   - None (modifies result in place)
//
// # Example
//
//	e.removeContainersByLabel(ctx, result)
//
// # Limitations
//
//   - May have overlap with name-based removal (counted twice)
//
// # Assumptions
//
//   - ProjectName is properly configured
func (e *DefaultComposeExecutor) removeContainersByLabel(ctx context.Context, result *CleanupResult) {
	args := []string{
		"rm", "-f",
		"--filter", fmt.Sprintf("label=io.podman.compose.project=%s", e.config.ProjectName),
	}

	if output, err := e.runPodman(ctx, args, 30*time.Second); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("remove by label: %v", err))
	} else {
		removed := e.parseLines(output.Stdout)
		result.ContainerNames = append(result.ContainerNames, removed...)
		result.ContainersRemoved += len(removed)
	}
}

// removePods removes pods matching the name filter.
//
// # Description
//
// Lists and removes pods matching the container name prefix.
// Handles podman's pod management for compose stacks.
//
// # Inputs
//
//   - ctx: Context for cancellation
//   - result: CleanupResult to record results and errors
//
// # Outputs
//
//   - None (modifies result in place)
//
// # Example
//
//	e.removePods(ctx, result)
//
// # Limitations
//
//   - Iterates pods one by one (could be slow with many pods)
//
// # Assumptions
//
//   - Pods follow same naming convention as containers
func (e *DefaultComposeExecutor) removePods(ctx context.Context, result *CleanupResult) {
	listArgs := []string{
		"pod", "ls", "-q",
		"--filter", fmt.Sprintf("name=%s", e.config.ContainerNamePrefix),
	}

	output, err := e.runPodman(ctx, listArgs, 30*time.Second)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list pods: %v", err))
		return
	}

	pods := e.parseLines(output.Stdout)
	for _, pod := range pods {
		if pod == "" {
			continue
		}
		rmArgs := []string{"pod", "rm", "-f", pod}
		if _, err := e.runPodman(ctx, rmArgs, 30*time.Second); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("remove pod %s: %v", pod, err))
		} else {
			result.PodNames = append(result.PodNames, pod)
			result.PodsRemoved++
		}
	}
}

// validateExecOptions validates ExecOptions before execution.
//
// # Description
//
// Ensures required fields are present for exec operation.
//
// # Inputs
//
//   - opts: ExecOptions to validate
//
// # Outputs
//
//   - error: ErrInvalidConfig if validation fails
//
// # Example
//
//	if err := e.validateExecOptions(opts); err != nil {
//	    return nil, err
//	}
//
// # Limitations
//
//   - Only validates required fields, not content
//
// # Assumptions
//
//   - Called before exec execution
func (e *DefaultComposeExecutor) validateExecOptions(opts ExecOptions) error {
	if opts.Service == "" {
		return fmt.Errorf("%w: service name is required", ErrInvalidConfig)
	}
	if len(opts.Command) == 0 {
		return fmt.Errorf("%w: command is required", ErrInvalidConfig)
	}
	return nil
}

// buildExecArgs builds arguments for exec command.
//
// # Description
//
// Constructs the compose exec command arguments from options.
//
// # Inputs
//
//   - opts: ExecOptions containing exec configuration
//
// # Outputs
//
//   - []string: Command arguments for podman-compose exec
//
// # Example
//
//	args := e.buildExecArgs(ExecOptions{Service: "web", Command: []string{"ls"}})
//	// ["-f", "...", "exec", "-T", "web", "ls"]
//
// # Limitations
//
//   - Always includes -T (no TTY)
//
// # Assumptions
//
//   - Options have been validated
func (e *DefaultComposeExecutor) buildExecArgs(opts ExecOptions) []string {
	args := e.buildComposeFileArgs()
	args = append(args, "exec", "-T")

	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	if opts.WorkDir != "" {
		args = append(args, "--workdir", opts.WorkDir)
	}
	for k, v := range opts.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, opts.Service)
	args = append(args, opts.Command...)

	return args
}

// isContainerNotRunningError checks if error indicates container not running.
//
// # Description
//
// Examines compose result stderr to determine if the error
// is because the target container is not running.
//
// # Inputs
//
//   - result: ComposeResult from failed exec attempt
//
// # Outputs
//
//   - bool: true if error indicates container not running
//
// # Example
//
//	if e.isContainerNotRunningError(result) {
//	    return nil, ErrContainerNotRunning
//	}
//
// # Limitations
//
//   - String-based detection may break with different versions
//
// # Assumptions
//
//   - result is not nil
func (e *DefaultComposeExecutor) isContainerNotRunningError(result *ComposeResult) bool {
	if result == nil {
		return false
	}
	return strings.Contains(result.Stderr, "not running") ||
		strings.Contains(result.Stderr, "No such container")
}

// buildCommandEnvironment builds the environment for command execution.
//
// # Description
//
// Combines current process environment with additional variables.
// User-provided variables override existing environment variables
// with the same key to ensure deterministic behavior.
//
// # Inputs
//
//   - env: Additional environment variables (may be nil)
//
// # Outputs
//
//   - []string: Combined environment in KEY=VALUE format
//
// # Example
//
//	cmdEnv := e.buildCommandEnvironment(map[string]string{"FOO": "bar"})
//
// # Limitations
//
//   - Inherits all process environment variables
//
// # Assumptions
//
//   - os.Environ() returns valid environment in KEY=VALUE format
func (e *DefaultComposeExecutor) buildCommandEnvironment(env map[string]string) []string {
	// Build map from current environment for deduplication
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.Index(e, "="); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	// Override with user-provided values
	for k, v := range env {
		envMap[k] = v
	}

	// Convert back to slice
	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

// resolveTimeout returns the timeout to use, applying default if zero.
//
// # Description
//
// Returns provided timeout if non-zero, otherwise returns default.
//
// # Inputs
//
//   - timeout: Requested timeout (may be zero)
//
// # Outputs
//
//   - time.Duration: Timeout to use
//
// # Example
//
//	timeout := e.resolveTimeout(0) // returns e.config.DefaultTimeout
//
// # Limitations
//
//   - Cannot distinguish between explicitly zero and unset
//
// # Assumptions
//
//   - DefaultTimeout is properly configured
func (e *DefaultComposeExecutor) resolveTimeout(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return e.config.DefaultTimeout
	}
	return timeout
}

// resolveGracefulTimeout returns the graceful timeout to use.
//
// # Description
//
// Returns provided timeout if non-zero, otherwise returns 10 seconds.
//
// # Inputs
//
//   - timeout: Requested timeout (may be zero)
//
// # Outputs
//
//   - time.Duration: Graceful timeout to use
//
// # Example
//
//	timeout := e.resolveGracefulTimeout(0) // returns 10*time.Second
//
// # Limitations
//
//   - Default is hardcoded to 10 seconds
//
// # Assumptions
//
//   - 10 seconds is a reasonable default for graceful shutdown
func (e *DefaultComposeExecutor) resolveGracefulTimeout(timeout time.Duration) time.Duration {
	if timeout == 0 {
		return 10 * time.Second
	}
	return timeout
}

// fileExists checks if a file exists using the injected stat function.
//
// # Description
//
// Wrapper around osStatFunc to check file existence.
//
// # Inputs
//
//   - path: Path to check
//
// # Outputs
//
//   - bool: true if file exists
//
// # Example
//
//	if e.fileExists("/path/to/file") {
//	    // file exists
//	}
//
// # Limitations
//
//   - Does not distinguish between file and directory
//   - Does not check permissions
//
// # Assumptions
//
//   - osStatFunc is properly initialized
func (e *DefaultComposeExecutor) fileExists(path string) bool {
	_, err := e.osStatFunc(path)
	return err == nil
}

// parseLines splits output into non-empty lines.
//
// # Description
//
// Utility to split multiline output, trimming whitespace
// and filtering empty lines.
//
// # Inputs
//
//   - output: Raw output string
//
// # Outputs
//
//   - []string: Non-empty trimmed lines
//
// # Example
//
//	lines := e.parseLines("line1\n\nline2\n")
//	// lines == []string{"line1", "line2"}
//
// # Limitations
//
//   - Trims all whitespace from each line
//
// # Assumptions
//
//   - Unix-style newlines
func (e *DefaultComposeExecutor) parseLines(output string) []string {
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// logCommand logs the command being executed, redacting sensitive values.
//
// # Description
//
// Prints command to stdout with environment variables.
// Sensitive values (tokens, secrets, etc.) are shown as [REDACTED].
//
// # Inputs
//
//   - cmd: Command string to log
//   - env: Environment variables (sensitive ones redacted)
//
// # Outputs
//
//   - None (prints to stdout)
//
// # Example
//
//	e.logCommand("podman-compose up", map[string]string{"API_TOKEN": "secret"})
//	// Prints: Executing: podman-compose up (in /path)
//	//         Environment:
//	//           - API_TOKEN=[REDACTED]
//
// # Limitations
//
//   - Always prints, no verbosity control
//
// # Assumptions
//
//   - Stdout is appropriate for logging
func (e *DefaultComposeExecutor) logCommand(cmd string, env map[string]string) {
	fmt.Printf("Executing: %s (in %s)\n", cmd, e.config.StackDir)
	if len(env) > 0 {
		fmt.Println("  Environment:")
		for k, v := range env {
			if e.isSensitiveEnvVar(k) {
				fmt.Printf("    - %s=[REDACTED]\n", k)
			} else {
				fmt.Printf("    - %s=%s\n", k, v)
			}
		}
	}
}

// isSensitiveEnvVar checks if an environment variable name is sensitive.
//
// # Description
//
// Identifies variables that should not be logged in plaintext.
// Checks for common sensitive patterns like TOKEN, SECRET, KEY, PASSWORD.
//
// # Inputs
//
//   - name: Environment variable name
//
// # Outputs
//
//   - bool: true if variable is sensitive
//
// # Example
//
//	e.isSensitiveEnvVar("API_TOKEN")     // true
//	e.isSensitiveEnvVar("OLLAMA_MODEL")  // false
//
// # Limitations
//
//   - Pattern-based, may have false positives/negatives
//
// # Assumptions
//
//   - Common naming conventions are followed
func (e *DefaultComposeExecutor) isSensitiveEnvVar(name string) bool {
	upper := strings.ToUpper(name)
	return strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "KEY") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "CREDENTIAL")
}

// validateEnvVars validates all environment variable keys in the map.
//
// # Description
//
// Ensures all environment variable keys match the allowed pattern
// (alphanumeric and underscore, starting with letter or underscore).
// This prevents config injection attacks through malformed env var names.
//
// # Inputs
//
//   - env: Environment variables map to validate
//
// # Outputs
//
//   - error: ErrInvalidEnvVar with details if any key is invalid
//
// # Example
//
//	if err := e.validateEnvVars(opts.Env); err != nil {
//	    return nil, err
//	}
//
// # Limitations
//
//   - Does not validate values, only keys
//   - May reject unconventional but valid POSIX names
//
// # Assumptions
//
//   - Standard POSIX env var naming conventions are desired
func (e *DefaultComposeExecutor) validateEnvVars(env map[string]string) error {
	for key := range env {
		if !envVarKeyRegex.MatchString(key) {
			return fmt.Errorf("%w: key %q contains invalid characters (must match [a-zA-Z_][a-zA-Z0-9_]*)", ErrInvalidEnvVar, key)
		}
	}
	return nil
}

// =============================================================================
// Mock Implementation
// =============================================================================

// MockComposeExecutor is a test double for ComposeExecutor.
//
// # Description
//
// Provides a configurable mock implementation for testing.
// Each method can be configured with a custom function.
// Tracks all calls for verification.
//
// # Example
//
//	mock := &MockComposeExecutor{
//	    UpFunc: func(ctx context.Context, opts UpOptions) (*ComposeResult, error) {
//	        return &ComposeResult{Success: true}, nil
//	    },
//	}
//	result, _ := mock.Up(ctx, UpOptions{})
//	assert.Equal(t, 1, len(mock.UpCalls))
type MockComposeExecutor struct {
	UpFunc                     func(context.Context, UpOptions) (*ComposeResult, error)
	DownFunc                   func(context.Context, DownOptions) (*ComposeResult, error)
	StopFunc                   func(context.Context, StopOptions) (*StopResult, error)
	LogsFunc                   func(context.Context, LogsOptions, io.Writer) error
	StatusFunc                 func(context.Context) (*ComposeStatus, error)
	ForceCleanupFunc           func(context.Context) (*CleanupResult, error)
	RemoveExitedContainersFunc func(context.Context) (int, error)
	ExecFunc                   func(context.Context, ExecOptions) (*ExecResult, error)
	GetComposeFilesFunc        func() []string

	UpCalls      []UpOptions
	DownCalls    []DownOptions
	StopCalls    []StopOptions
	CleanupCalls int
	mu           sync.Mutex
}

// Up implements ComposeExecutor.
func (m *MockComposeExecutor) Up(ctx context.Context, opts UpOptions) (*ComposeResult, error) {
	m.mu.Lock()
	m.UpCalls = append(m.UpCalls, opts)
	m.mu.Unlock()

	if m.UpFunc != nil {
		return m.UpFunc(ctx, opts)
	}
	return &ComposeResult{Success: true}, nil
}

// Down implements ComposeExecutor.
func (m *MockComposeExecutor) Down(ctx context.Context, opts DownOptions) (*ComposeResult, error) {
	m.mu.Lock()
	m.DownCalls = append(m.DownCalls, opts)
	m.mu.Unlock()

	if m.DownFunc != nil {
		return m.DownFunc(ctx, opts)
	}
	return &ComposeResult{Success: true}, nil
}

// Stop implements ComposeExecutor.
func (m *MockComposeExecutor) Stop(ctx context.Context, opts StopOptions) (*StopResult, error) {
	m.mu.Lock()
	m.StopCalls = append(m.StopCalls, opts)
	m.mu.Unlock()

	if m.StopFunc != nil {
		return m.StopFunc(ctx, opts)
	}
	return &StopResult{TotalStopped: 0}, nil
}

// Logs implements ComposeExecutor.
func (m *MockComposeExecutor) Logs(ctx context.Context, opts LogsOptions, w io.Writer) error {
	if m.LogsFunc != nil {
		return m.LogsFunc(ctx, opts, w)
	}
	return nil
}

// Status implements ComposeExecutor.
func (m *MockComposeExecutor) Status(ctx context.Context) (*ComposeStatus, error) {
	if m.StatusFunc != nil {
		return m.StatusFunc(ctx)
	}
	return &ComposeStatus{Services: []ServiceStatus{}}, nil
}

// ForceCleanup implements ComposeExecutor.
func (m *MockComposeExecutor) ForceCleanup(ctx context.Context) (*CleanupResult, error) {
	m.mu.Lock()
	m.CleanupCalls++
	m.mu.Unlock()

	if m.ForceCleanupFunc != nil {
		return m.ForceCleanupFunc(ctx)
	}
	return &CleanupResult{}, nil
}

// RemoveExitedContainers implements ComposeExecutor.
func (m *MockComposeExecutor) RemoveExitedContainers(ctx context.Context) (int, error) {
	if m.RemoveExitedContainersFunc != nil {
		return m.RemoveExitedContainersFunc(ctx)
	}
	return 0, nil
}

// Exec implements ComposeExecutor.
func (m *MockComposeExecutor) Exec(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, opts)
	}
	return &ExecResult{ExitCode: 0}, nil
}

// GetComposeFiles implements ComposeExecutor.
func (m *MockComposeExecutor) GetComposeFiles() []string {
	if m.GetComposeFilesFunc != nil {
		return m.GetComposeFilesFunc()
	}
	return []string{}
}
