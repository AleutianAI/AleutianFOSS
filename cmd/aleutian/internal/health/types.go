// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package health

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// HealthCheckVersion is the current version of health check definitions.
// Increment when check semantics change to enable backwards compatibility.
const HealthCheckVersion = "1.0.0"

// HealthCheckType specifies the method used to check service health.
//
// # Description
//
// Defines the protocol or mechanism used to determine if a service
// is healthy. Each type has different requirements and behaviors.
//
// # Version
//
// This type is versioned. Current version: 1.0.0
// Changes to check semantics require version increment.
//
// # Examples
//
//	checkType := HealthCheckHTTP
//	if checkType == HealthCheckHTTP {
//	    // Perform HTTP GET request
//	}
//
// # Limitations
//
//   - HealthCheckTCP only verifies port is open, not service health
//   - HealthCheckProcess may have false positives (zombie processes)
//
// # Assumptions
//
//   - HTTP checks expect the service to respond within timeout
//   - Container checks assume podman is the container runtime
type HealthCheckType string

const (
	// HealthCheckHTTP checks health via HTTP GET request.
	// Expects 2xx status code by default.
	HealthCheckHTTP HealthCheckType = "http"

	// HealthCheckTCP checks health via TCP connection.
	// Only verifies the port is accepting connections.
	HealthCheckTCP HealthCheckType = "tcp"

	// HealthCheckContainer checks health via container runtime state.
	// Queries podman for container running state.
	HealthCheckContainer HealthCheckType = "container"

	// HealthCheckProcess checks health via process existence.
	// Uses pgrep to verify process is running.
	HealthCheckProcess HealthCheckType = "process"
)

// HealthState represents the binary health state of a service.
//
// # Description
//
// Represents the outcome of a health check. States are mutually
// exclusive and represent a point-in-time snapshot.
//
// # Version
//
// This type is versioned. Current version: 1.0.0
//
// # Examples
//
//	status := HealthStatus{State: HealthStateHealthy}
//	if status.State == HealthStateHealthy {
//	    fmt.Println("Service is healthy")
//	}
//
// # Limitations
//
//   - Binary states don't capture degraded performance
//   - State is point-in-time, may change immediately after check
//
// # Assumptions
//
//   - A service can only be in one state at a time
//   - Skipped state is used when check was intentionally not performed
type HealthState string

const (
	// HealthStateHealthy indicates the service is responding normally.
	HealthStateHealthy HealthState = "healthy"

	// HealthStateUnhealthy indicates the service is not responding correctly.
	HealthStateUnhealthy HealthState = "unhealthy"

	// HealthStateUnreachable indicates the service could not be contacted.
	HealthStateUnreachable HealthState = "unreachable"

	// HealthStateSkipped indicates the service was not checked.
	HealthStateSkipped HealthState = "skipped"
)

// ServiceDefinition describes a service to health check.
//
// # Description
//
// Defines the parameters needed to perform a health check on a service,
// including the check type, endpoint, and criticality. Each definition
// has a unique ID for tracking and correlation.
//
// # Inputs
//
// ServiceDefinition is typically created via DefaultServiceDefinitions()
// or manually constructed with required fields.
//
// # Outputs
//
// Used as input to HealthChecker.CheckService() and WaitForServices().
//
// # Examples
//
//	def := ServiceDefinition{
//	    ID:            GenerateID(),
//	    Name:          "MyService",
//	    URL:           "http://localhost:8080/health",
//	    CheckType:     HealthCheckHTTP,
//	    Critical:      true,
//	    Version:       HealthCheckVersion,
//	    CreatedAt:     time.Now(),
//	}
//
// # Limitations
//
//   - URL is required for HTTP and TCP checks
//   - ContainerName is required for container checks
//   - Only one check type per definition
//
// # Assumptions
//
//   - Service endpoints are accessible from the checker host
//   - Container names are unique within the runtime
//   - Version matches HealthCheckVersion constant
type ServiceDefinition struct {
	// ID is a unique identifier for this service definition.
	// Used for tracking, logging, and correlation.
	ID string

	// Name is the human-readable service name.
	Name string

	// URL is the health check endpoint (for HTTP/TCP checks).
	URL string

	// ContainerName is the container name (empty for host services).
	ContainerName string

	// CheckType specifies how to check health.
	CheckType HealthCheckType

	// Critical marks the service as required for startup.
	// If a critical service fails, WaitForServices returns an error.
	Critical bool

	// Skip indicates this service should not be checked at all.
	// Skipped services are reported with HealthStateSkipped.
	// Use this when a service is not applicable in the current mode.
	Skip bool

	// Timeout overrides default per-check timeout.
	// Zero means use default.
	Timeout time.Duration

	// ExpectedStatus is the expected HTTP status code (default: 200).
	// Only used for HTTP checks.
	ExpectedStatus int

	// Version indicates the check definition version.
	// Used for backwards compatibility when check semantics change.
	Version string

	// CreatedAt is when this definition was created.
	CreatedAt time.Time

	// UpdatedAt is when this definition was last modified.
	UpdatedAt time.Time
}

// WaitOptions configures WaitForServices behavior.
//
// # Description
//
// Controls timeout, polling intervals, and failure modes for
// waiting on services to become healthy. Uses exponential backoff
// to reduce load during heavy startup conditions.
//
// # Inputs
//
// Typically created via DefaultWaitOptions() and modified as needed.
//
// # Outputs
//
// Passed to HealthChecker.WaitForServices() to control wait behavior.
//
// # Examples
//
//	opts := DefaultWaitOptions()
//	opts.Timeout = 120 * time.Second  // Longer timeout
//	opts.FailFast = true              // Stop on first failure
//	result, err := checker.WaitForServices(ctx, services, opts)
//
// # Limitations
//
//   - Jitter is applied uniformly; no per-service jitter
//   - MaxInterval caps backoff; very slow services may timeout
//
// # Assumptions
//
//   - Multiplier > 1.0 for exponential growth
//   - Jitter in range [0, 1] for meaningful randomization
//   - InitialInterval <= MaxInterval
type WaitOptions struct {
	// ID is a unique identifier for this wait operation.
	ID string

	// Timeout is the overall timeout for waiting (default: 60s).
	Timeout time.Duration

	// InitialInterval is the first poll interval (default: 1s).
	// Used with exponential backoff to give system breathing room.
	InitialInterval time.Duration

	// MaxInterval is the maximum poll interval (default: 8s).
	// Backoff stops increasing after reaching this value.
	MaxInterval time.Duration

	// Multiplier is the backoff multiplier (default: 2.0).
	// Each interval is multiplied by this until MaxInterval is reached.
	Multiplier float64

	// Jitter adds randomness to prevent thundering herd (default: 0.1).
	// Range: [interval * (1-Jitter), interval * (1+Jitter)]
	Jitter float64

	// SkipOptional skips non-critical services if true.
	SkipOptional bool

	// FailFast returns immediately on first critical failure if true.
	FailFast bool

	// CreatedAt is when these options were created.
	CreatedAt time.Time
}

// DefaultWaitOptions returns sensible defaults with exponential backoff.
//
// # Description
//
// Returns WaitOptions configured for typical startup scenarios:
// - 60 second overall timeout
// - Exponential backoff: 1s -> 2s -> 4s -> 8s -> 8s...
// - 10% jitter to prevent thundering herd
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - WaitOptions: Configured options with unique ID and timestamp
//
// # Examples
//
//	opts := DefaultWaitOptions()
//	fmt.Printf("Wait ID: %s\n", opts.ID)
//	fmt.Printf("Timeout: %v\n", opts.Timeout)
//
// # Limitations
//
//   - Defaults may not suit all deployment scenarios
//   - 60s timeout may be too short for cold starts
//
// # Assumptions
//
//   - Services typically start within 60 seconds
//   - Network latency is minimal (localhost)
func DefaultWaitOptions() WaitOptions {
	return WaitOptions{
		ID:              GenerateID(),
		Timeout:         60 * time.Second,
		InitialInterval: 1 * time.Second,
		MaxInterval:     8 * time.Second,
		Multiplier:      2.0,
		Jitter:          0.1,
		SkipOptional:    false,
		FailFast:        false,
		CreatedAt:       time.Now(),
	}
}

// WaitResult contains the outcome of WaitForServices.
//
// # Description
//
// Provides detailed information about which services became healthy,
// which failed, and how long the wait took. Includes unique ID for
// tracking and correlation with logs.
//
// # Inputs
//
// Created by HealthChecker.WaitForServices().
//
// # Outputs
//
// Returned to caller with complete wait operation results.
//
// # Examples
//
//	result, err := checker.WaitForServices(ctx, services, opts)
//	if err != nil {
//	    for _, name := range result.FailedCritical {
//	        fmt.Printf("Critical failure: %s\n", name)
//	    }
//	}
//	fmt.Printf("Wait took %v (ID: %s)\n", result.Duration, result.ID)
//
// # Limitations
//
//   - Does not include intermediate check results
//   - Duration includes all retries, not individual check times
//
// # Assumptions
//
//   - Services list matches input services
//   - FailedCritical only contains critical service names
type WaitResult struct {
	// ID is a unique identifier for this wait result.
	ID string

	// Success is true if all critical services became healthy.
	Success bool

	// Duration is how long the wait took.
	Duration time.Duration

	// Services contains the final status of each service.
	Services []HealthStatus

	// FailedCritical contains names of critical services that failed.
	FailedCritical []string

	// Skipped contains names of services that were skipped.
	Skipped []string

	// StartedAt is when the wait operation started.
	StartedAt time.Time

	// CompletedAt is when the wait operation completed.
	CompletedAt time.Time

	// OptionsID references the WaitOptions used.
	OptionsID string
}

// HealthStatus represents the health of a single service.
//
// # Description
//
// Contains the result of a health check including state,
// latency, and diagnostic information. Each status has a
// unique ID for tracking individual check results.
//
// # Inputs
//
// Created by HealthChecker.CheckService() or CheckAllServices().
//
// # Outputs
//
// Returned to caller with health check results.
//
// # Examples
//
//	status, err := checker.CheckService(ctx, serviceDef)
//	if err != nil {
//	    log.Printf("Check failed: %v", err)
//	}
//	fmt.Printf("[%s] %s: %s (latency: %v)\n",
//	    status.ID, status.Name, status.State, status.Latency)
//
// # Limitations
//
//   - Point-in-time snapshot; state may change immediately
//   - HTTPStatus only populated for HTTP checks
//   - ContainerState only populated for container checks
//
// # Assumptions
//
//   - Latency is measured from check start to response
//   - LastChecked is set to check completion time
type HealthStatus struct {
	// ID is a unique identifier for this health status.
	ID string

	// Name is the service name.
	Name string

	// State is the health state (healthy, unhealthy, unreachable, skipped).
	State HealthState

	// Message provides additional context (error message, etc.).
	Message string

	// Latency is how long the health check took.
	Latency time.Duration

	// LastChecked is when the check was performed.
	LastChecked time.Time

	// HTTPStatus is the HTTP status code (for HTTP checks).
	HTTPStatus int

	// ContainerState is the container state (for container checks).
	ContainerState string

	// ServiceDefinitionID references the ServiceDefinition checked.
	ServiceDefinitionID string

	// CheckVersion is the version of the check that produced this result.
	CheckVersion string
}

// HealthCheckerConfig configures the DefaultHealthChecker.
//
// # Description
//
// Provides configuration for health check behavior including
// timeouts and expected responses. Configuration is versioned
// to track changes over time.
//
// # Inputs
//
// Typically created via DefaultHealthCheckerConfig() and modified.
//
// # Outputs
//
// Passed to NewDefaultHealthChecker() to configure the checker.
//
// # Examples
//
//	cfg := DefaultHealthCheckerConfig()
//	cfg.DefaultTimeout = 10 * time.Second  // Longer timeout
//	checker := NewDefaultHealthChecker(proc, cfg)
//
// # Limitations
//
//   - Global defaults apply to all services unless overridden
//   - ContainerNamePrefix is a simple string match
//
// # Assumptions
//
//   - All Aleutian containers use "aleutian-" prefix
//   - 5 second timeout is sufficient for healthy services
type HealthCheckerConfig struct {
	// ID is a unique identifier for this configuration.
	ID string

	// DefaultTimeout is the per-check timeout (default: 5s).
	DefaultTimeout time.Duration

	// DefaultExpectedStatus is the expected HTTP status (default: 200).
	DefaultExpectedStatus int

	// ContainerNamePrefix filters containers (default: "aleutian-").
	ContainerNamePrefix string

	// Version indicates the configuration version.
	Version string

	// CreatedAt is when this configuration was created.
	CreatedAt time.Time
}

// DefaultHealthCheckerConfig returns sensible defaults.
//
// # Description
//
// Returns HealthCheckerConfig suitable for typical Aleutian services.
// Includes unique ID and timestamp for tracking.
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - HealthCheckerConfig: Configured options
//
// # Examples
//
//	cfg := DefaultHealthCheckerConfig()
//	fmt.Printf("Config ID: %s, Version: %s\n", cfg.ID, cfg.Version)
//
// # Limitations
//
//   - 5 second timeout may be too short for cold services
//   - Only supports HTTP 200 as default expected status
//
// # Assumptions
//
//   - Services respond quickly when healthy
//   - HTTP 200 indicates healthy for all services
func DefaultHealthCheckerConfig() HealthCheckerConfig {
	return HealthCheckerConfig{
		ID:                    GenerateID(),
		DefaultTimeout:        5 * time.Second,
		DefaultExpectedStatus: 200,
		ContainerNamePrefix:   "aleutian-",
		Version:               HealthCheckVersion,
		CreatedAt:             time.Now(),
	}
}

// DefaultServiceDefinitions returns the standard Aleutian services.
//
// # Description
//
// Returns ServiceDefinition for all core Aleutian services.
// Critical services are required for startup; optional services
// are checked but don't block startup. Each definition has a
// unique ID and version for tracking.
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - []ServiceDefinition: All standard services with IDs and timestamps
//
// # Examples
//
//	services := DefaultServiceDefinitions()
//	for _, svc := range services {
//	    fmt.Printf("[%s] %s (critical: %v, version: %s)\n",
//	        svc.ID, svc.Name, svc.Critical, svc.Version)
//	}
//
// # Limitations
//
//   - Hardcoded ports may not match custom deployments
//   - RAG Engine uses container check (no HTTP endpoint)
//
// # Assumptions
//
//   - Services run on localhost
//   - Ports match podman-compose.yaml configuration
//   - Ollama runs as host service, not containerized
func DefaultServiceDefinitions() []ServiceDefinition {
	now := time.Now()
	return []ServiceDefinition{
		{
			ID:            GenerateID(),
			Name:          "Orchestrator",
			URL:           "http://localhost:12210/health",
			ContainerName: "aleutian-go-orchestrator",
			CheckType:     HealthCheckHTTP,
			Critical:      true,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            GenerateID(),
			Name:          "Weaviate",
			URL:           "http://localhost:12212/v1/.well-known/ready",
			ContainerName: "aleutian-weaviate",
			CheckType:     HealthCheckHTTP,
			Critical:      true,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            GenerateID(),
			Name:          "Ollama",
			URL:           "http://localhost:11434/",
			ContainerName: "", // Host service, not containerized
			CheckType:     HealthCheckHTTP,
			Critical:      true,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            GenerateID(),
			Name:          "RAG Engine",
			ContainerName: "aleutian-rag-engine",
			CheckType:     HealthCheckContainer,
			Critical:      true,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            GenerateID(),
			Name:          "Data Fetcher",
			URL:           "http://localhost:12001/health",
			ContainerName: "aleutian-data-fetcher",
			CheckType:     HealthCheckHTTP,
			Critical:      false,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            GenerateID(),
			Name:          "Forecast",
			URL:           "http://localhost:12000/health",
			ContainerName: "aleutian-forecast",
			CheckType:     HealthCheckHTTP,
			Critical:      false,
			Version:       HealthCheckVersion,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}
}

// ServiceDefinitionsForMode returns service definitions appropriate for the forecast mode.
//
// # Description
//
// Returns service definitions adjusted based on forecast mode.
// When using "sapheneia" mode, Ollama is skipped entirely since
// Sapheneia provides its own inference capabilities.
//
// # Inputs
//
//   - forecastMode: The forecast mode ("standalone" or "sapheneia")
//
// # Outputs
//
//   - []ServiceDefinition: Service definitions with appropriate settings
//
// # Examples
//
//	services := ServiceDefinitionsForMode("sapheneia")
//	// Ollama.Skip == true (not checked, reported as HealthStateSkipped)
//
// # Limitations
//
//   - Only recognizes "standalone" and "sapheneia" modes
//
// # Assumptions
//
//   - Services run on localhost
//   - Ports match podman-compose.yaml configuration
func ServiceDefinitionsForMode(forecastMode string) []ServiceDefinition {
	services := DefaultServiceDefinitions()

	// When using Sapheneia mode, skip Ollama entirely since
	// Sapheneia provides its own inference capabilities
	if forecastMode == "sapheneia" {
		for i := range services {
			if services[i].Name == "Ollama" {
				services[i].Skip = true
			}
		}
	}

	return services
}

// GenerateID creates a unique identifier for health check entities.
//
// # Description
//
// Generates a cryptographically random hex string suitable for
// uniquely identifying health check entities (statuses, results,
// configurations, etc.).
//
// # Inputs
//
// None.
//
// # Outputs
//
//   - string: 16-character hex string (8 random bytes)
//
// # Examples
//
//	id := GenerateID()
//	fmt.Printf("Generated ID: %s\n", id)  // e.g., "a1b2c3d4e5f67890"
//
// # Limitations
//
//   - Not a UUID; shorter for readability
//   - Collision probability is low but non-zero for very high volumes
//
// # Assumptions
//
//   - crypto/rand is available and functioning
//   - 8 bytes provides sufficient uniqueness for this use case
func GenerateID() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405.000")))[:16]
	}
	return hex.EncodeToString(b)
}
