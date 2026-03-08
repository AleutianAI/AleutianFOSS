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
Package main provides No-Op (Null Object) implementations for all major interfaces.

These implementations satisfy interface contracts without performing any actual work.
Use them as safe defaults when optional dependencies are not provided, preventing
nil pointer panics while maintaining type safety.

# Design Rationale

In Go, a nil pointer to a struct can satisfy an interface check, but calling
methods on it causes a panic. By providing No-Op implementations, we can:
  - Prevent nil pointer panics in production
  - Simplify testing by not requiring mock setup for unused dependencies
  - Make optional dependencies explicit in the type system

# Usage

	manager, err := NewSomeManager(deps)
	// If deps.MetricsStore is nil, use NoOpMetricsStore internally

# Thread Safety

All No-Op implementations are safe for concurrent use (they do nothing).
*/
package main

import (
	"context"
	"io"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/health"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/compose"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/process"
)

// =============================================================================
// NoOpMetricsStore
// =============================================================================

// NoOpMetricsStore is a safe default that does nothing.
//
// Use this instead of nil to satisfy the MetricsStore interface when
// metrics collection is not needed or not configured.
type NoOpMetricsStore struct{}

// Record does nothing.
func (n *NoOpMetricsStore) Record(service, metric string, value float64, timestamp time.Time) {}

// Query returns nil.
func (n *NoOpMetricsStore) Query(service, metric string, start, end time.Time) []MetricPoint {
	return nil
}

// GetBaseline returns nil.
func (n *NoOpMetricsStore) GetBaseline(service, metric string, window time.Duration) *BaselineStats {
	return nil
}

// Flush does nothing and returns nil.
func (n *NoOpMetricsStore) Flush(ctx context.Context) error {
	return nil
}

// Prune does nothing and returns 0, nil.
func (n *NoOpMetricsStore) Prune(olderThan time.Duration) (int, error) {
	return 0, nil
}

// Close does nothing and returns nil.
func (n *NoOpMetricsStore) Close() error {
	return nil
}

// =============================================================================
// NoOpProcessManager
// =============================================================================

// NoOpProcessManager is a safe default that does nothing.
//
// Use this for testing or when process management is not needed.
// All operations return success without performing any actual work.
type NoOpProcessManager struct{}

// Run does nothing and returns empty bytes, nil.
func (n *NoOpProcessManager) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return []byte{}, nil
}

// RunWithInput does nothing and returns empty bytes, nil.
func (n *NoOpProcessManager) RunWithInput(ctx context.Context, name string, input []byte, args ...string) ([]byte, error) {
	return []byte{}, nil
}

// RunInDir does nothing and returns empty strings, 0, nil.
func (n *NoOpProcessManager) RunInDir(ctx context.Context, dir string, env []string, name string, args ...string) (string, string, int, error) {
	return "", "", 0, nil
}

// RunStreaming does nothing and returns nil.
func (n *NoOpProcessManager) RunStreaming(ctx context.Context, dir string, w io.Writer, name string, args ...string) error {
	return nil
}

// Start does nothing and returns 0, nil.
func (n *NoOpProcessManager) Start(ctx context.Context, name string, args ...string) (int, error) {
	return 0, nil
}

// IsRunning returns false, 0, nil.
func (n *NoOpProcessManager) IsRunning(ctx context.Context, pattern string) (bool, int, error) {
	return false, 0, nil
}

// =============================================================================
// NoOpComposeExecutor
// =============================================================================

// NoOpComposeExecutor is a safe default that does nothing.
//
// Use this for testing or when compose operations are not needed.
type NoOpComposeExecutor struct{}

// Up returns success without doing anything.
func (n *NoOpComposeExecutor) Up(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error) {
	return &compose.ComposeResult{Success: true, ExitCode: 0}, nil
}

// Down returns success without doing anything.
func (n *NoOpComposeExecutor) Down(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error) {
	return &compose.ComposeResult{Success: true, ExitCode: 0}, nil
}

// Stop returns empty result.
func (n *NoOpComposeExecutor) Stop(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error) {
	return &compose.StopResult{}, nil
}

// Logs does nothing.
func (n *NoOpComposeExecutor) Logs(ctx context.Context, opts compose.LogsOptions, w io.Writer) error {
	return nil
}

// Status returns empty status.
func (n *NoOpComposeExecutor) Status(ctx context.Context) (*compose.ComposeStatus, error) {
	return &compose.ComposeStatus{Services: []compose.ServiceStatus{}}, nil
}

// RemoveExitedContainers returns zero removed.
func (n *NoOpComposeExecutor) RemoveExitedContainers(ctx context.Context) (int, error) {
	return 0, nil
}

// ForceCleanup returns empty result.
func (n *NoOpComposeExecutor) ForceCleanup(ctx context.Context) (*compose.CleanupResult, error) {
	return &compose.CleanupResult{}, nil
}

// Exec returns empty result.
func (n *NoOpComposeExecutor) Exec(ctx context.Context, opts compose.ExecOptions) (*compose.ExecResult, error) {
	return &compose.ExecResult{ExitCode: 0}, nil
}

// GetComposeFiles returns empty slice.
func (n *NoOpComposeExecutor) GetComposeFiles() []string {
	return []string{}
}

// =============================================================================
// NoOpHealthChecker
// =============================================================================

// NoOpHealthChecker is a safe default that reports all services as healthy.
//
// Use this for testing or when health checking is not needed.
type NoOpHealthChecker struct{}

// CheckService returns healthy status.
func (n *NoOpHealthChecker) CheckService(ctx context.Context, service health.ServiceDefinition) (*health.HealthStatus, error) {
	return &health.HealthStatus{
		ID:          health.GenerateID(),
		Name:        service.Name,
		State:       health.HealthStateHealthy,
		LastChecked: time.Now(),
	}, nil
}

// CheckAllServices returns all services as healthy.
func (n *NoOpHealthChecker) CheckAllServices(ctx context.Context, services []health.ServiceDefinition) ([]health.HealthStatus, error) {
	results := make([]health.HealthStatus, len(services))
	for i, svc := range services {
		results[i] = health.HealthStatus{
			ID:          health.GenerateID(),
			Name:        svc.Name,
			State:       health.HealthStateHealthy,
			LastChecked: time.Now(),
		}
	}
	return results, nil
}

// WaitForServices returns immediately with success.
func (n *NoOpHealthChecker) WaitForServices(ctx context.Context, services []health.ServiceDefinition, opts health.WaitOptions) (*health.WaitResult, error) {
	return &health.WaitResult{
		ID:      health.GenerateID(),
		Success: true,
	}, nil
}

// IsContainerRunning returns true, nil.
func (n *NoOpHealthChecker) IsContainerRunning(ctx context.Context, containerName string) (bool, error) {
	return true, nil
}

// =============================================================================
// Compile-time interface satisfaction checks
// =============================================================================

var (
	_ MetricsStore            = (*NoOpMetricsStore)(nil)
	_ process.Manager         = (*NoOpProcessManager)(nil)
	_ compose.ComposeExecutor = (*NoOpComposeExecutor)(nil)
	_ health.HealthChecker    = (*NoOpHealthChecker)(nil)
)
