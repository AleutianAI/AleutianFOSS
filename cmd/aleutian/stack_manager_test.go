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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/config"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/diagnostics"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/health"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra"
	"github.com/AleutianAI/AleutianFOSS/cmd/aleutian/internal/infra/compose"
)

// =============================================================================
// Test Mocks for StackManager Dependencies
// =============================================================================

// testInfraManager is a minimal mock for InfrastructureManager.
type testInfraManager struct {
	ensureReadyFunc       func(ctx context.Context, opts infra.InfrastructureOptions) error
	getMachineStatusFunc  func(ctx context.Context, machineName string) (*infra.MachineStatus, error)
	ensureReadyCalls      []infra.InfrastructureOptions
	getMachineStatusCalls []string
	mu                    sync.Mutex
}

func newTestInfraManager() *testInfraManager {
	return &testInfraManager{
		ensureReadyCalls:      make([]infra.InfrastructureOptions, 0),
		getMachineStatusCalls: make([]string, 0),
	}
}

func (m *testInfraManager) EnsureReady(ctx context.Context, opts infra.InfrastructureOptions) error {
	m.mu.Lock()
	m.ensureReadyCalls = append(m.ensureReadyCalls, opts)
	m.mu.Unlock()
	if m.ensureReadyFunc != nil {
		return m.ensureReadyFunc(ctx, opts)
	}
	return nil
}

func (m *testInfraManager) GetMachineStatus(ctx context.Context, machineName string) (*infra.MachineStatus, error) {
	m.mu.Lock()
	m.getMachineStatusCalls = append(m.getMachineStatusCalls, machineName)
	m.mu.Unlock()
	if m.getMachineStatusFunc != nil {
		return m.getMachineStatusFunc(ctx, machineName)
	}
	return &infra.MachineStatus{Exists: true, Running: true}, nil
}

func (m *testInfraManager) ValidateMounts(_ context.Context, _ []string) (*infra.MountValidation, error) {
	return &infra.MountValidation{Valid: true}, nil
}

func (m *testInfraManager) ProvisionMachine(_ context.Context, _ infra.MachineSpec) error {
	return nil
}

func (m *testInfraManager) StartMachine(_ context.Context, _ string) error {
	return nil
}

func (m *testInfraManager) StopMachine(_ context.Context, _ string) error {
	return nil
}

func (m *testInfraManager) RemoveMachine(_ context.Context, _ string, _ bool, _ string) error {
	return nil
}

func (m *testInfraManager) VerifyMounts(_ context.Context, _ string, _ []string) (*infra.MountVerification, error) {
	return &infra.MountVerification{Match: true}, nil
}

func (m *testInfraManager) DetectConflicts(_ context.Context) (*infra.ConflictReport, error) {
	return &infra.ConflictReport{HasConflicts: false}, nil
}

func (m *testInfraManager) HasForeignWorkloads(_ context.Context) (*infra.WorkloadAssessment, error) {
	return &infra.WorkloadAssessment{HasForeignWorkloads: false}, nil
}

func (m *testInfraManager) VerifyNetworkIsolation(_ context.Context, _ string) (*infra.NetworkIsolationStatus, error) {
	return &infra.NetworkIsolationStatus{Isolated: true}, nil
}

// testProfileResolver is a minimal mock for ProfileResolver.
type testProfileResolver struct {
	resolveFunc  func(ctx context.Context, opts ProfileOptions) (map[string]string, error)
	resolveCalls []ProfileOptions
	mu           sync.Mutex
}

func newTestProfileResolver() *testProfileResolver {
	return &testProfileResolver{
		resolveCalls: make([]ProfileOptions, 0),
	}
}

func (m *testProfileResolver) Resolve(ctx context.Context, opts ProfileOptions) (map[string]string, error) {
	m.mu.Lock()
	m.resolveCalls = append(m.resolveCalls, opts)
	m.mu.Unlock()
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, opts)
	}
	return map[string]string{"ALEUTIAN_PROFILE": "standard"}, nil
}

func (m *testProfileResolver) DetectHardware(_ context.Context) (*HardwareInfo, error) {
	return &HardwareInfo{SystemRAM_MB: 16384, CPUCores: 8}, nil
}

func (m *testProfileResolver) GetProfileInfo(name string) (*ProfileInfo, bool) {
	return &ProfileInfo{Name: name}, true
}

// testModelEnsurer is a minimal mock for ModelEnsurer.
type testModelEnsurer struct {
	ensureModelsFunc  func(ctx context.Context) (*ModelEnsureResult, error)
	ensureModelsCalls int
	mu                sync.Mutex
}

func newTestModelEnsurer() *testModelEnsurer {
	return &testModelEnsurer{}
}

func (m *testModelEnsurer) EnsureModels(ctx context.Context) (*ModelEnsureResult, error) {
	m.mu.Lock()
	m.ensureModelsCalls++
	m.mu.Unlock()
	if m.ensureModelsFunc != nil {
		return m.ensureModelsFunc(ctx)
	}
	return &ModelEnsureResult{CanProceed: true}, nil
}

func (m *testModelEnsurer) GetRequiredModels() []RequiredModel {
	return nil
}

func (m *testModelEnsurer) SetProgressCallback(_ PullProgressCallback) {}

// testComposeExecutor is a minimal mock for ComposeExecutor.
type testComposeExecutor struct {
	upFunc           func(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error)
	downFunc         func(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error)
	stopFunc         func(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error)
	statusFunc       func(ctx context.Context) (*compose.ComposeStatus, error)
	logsFunc         func(ctx context.Context, opts compose.LogsOptions, w io.Writer) error
	forceCleanupFunc func(ctx context.Context) (*compose.CleanupResult, error)
	// destroyed tracks whether Down() has been called for stateful behavior
	destroyed bool
	mu        sync.Mutex
}

func newTestComposeExecutor() *testComposeExecutor {
	return &testComposeExecutor{
		statusFunc: func(ctx context.Context) (*compose.ComposeStatus, error) {
			return &compose.ComposeStatus{
				Running: 3,
				Stopped: 0,
				Services: []compose.ServiceStatus{
					{Name: "orchestrator", State: "running", ContainerName: "aleutian-orchestrator"},
					{Name: "weaviate", State: "running", ContainerName: "aleutian-weaviate"},
					{Name: "ollama", State: "running", ContainerName: "aleutian-ollama"},
				},
			}, nil
		},
		upFunc: func(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error) {
			return &compose.ComposeResult{Success: true}, nil
		},
		downFunc: func(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error) {
			return &compose.ComposeResult{Success: true}, nil
		},
		stopFunc: func(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error) {
			return &compose.StopResult{TotalStopped: 3, GracefulStopped: 3}, nil
		},
		forceCleanupFunc: func(ctx context.Context) (*compose.CleanupResult, error) {
			return &compose.CleanupResult{ContainersRemoved: 0, PodsRemoved: 0}, nil
		},
		logsFunc: func(ctx context.Context, opts compose.LogsOptions, w io.Writer) error {
			return nil
		},
	}
}

func (m *testComposeExecutor) Up(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error) {
	if m.upFunc != nil {
		return m.upFunc(ctx, opts)
	}
	return &compose.ComposeResult{Success: true}, nil
}

func (m *testComposeExecutor) Down(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error) {
	m.mu.Lock()
	m.destroyed = true
	m.mu.Unlock()
	if m.downFunc != nil {
		return m.downFunc(ctx, opts)
	}
	return &compose.ComposeResult{Success: true}, nil
}

func (m *testComposeExecutor) Stop(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error) {
	if m.stopFunc != nil {
		return m.stopFunc(ctx, opts)
	}
	return &compose.StopResult{TotalStopped: 3}, nil
}

func (m *testComposeExecutor) Logs(ctx context.Context, opts compose.LogsOptions, w io.Writer) error {
	if m.logsFunc != nil {
		return m.logsFunc(ctx, opts, w)
	}
	return nil
}

func (m *testComposeExecutor) Status(ctx context.Context) (*compose.ComposeStatus, error) {
	m.mu.Lock()
	isDestroyed := m.destroyed
	m.mu.Unlock()

	// After Down() is called, return empty status for verification
	if isDestroyed {
		return &compose.ComposeStatus{Running: 0, Stopped: 0, Services: nil}, nil
	}

	if m.statusFunc != nil {
		return m.statusFunc(ctx)
	}
	return &compose.ComposeStatus{Running: 3}, nil
}

func (m *testComposeExecutor) ForceCleanup(ctx context.Context) (*compose.CleanupResult, error) {
	if m.forceCleanupFunc != nil {
		return m.forceCleanupFunc(ctx)
	}
	return &compose.CleanupResult{}, nil
}

func (m *testComposeExecutor) Exec(_ context.Context, _ compose.ExecOptions) (*compose.ExecResult, error) {
	return &compose.ExecResult{ExitCode: 0}, nil
}

func (m *testComposeExecutor) GetComposeFiles() []string {
	return []string{"podman-compose.yml"}
}

// testHealthChecker is a minimal mock for HealthChecker.
type testHealthChecker struct {
	waitForServicesFunc func(ctx context.Context, services []health.ServiceDefinition, opts health.WaitOptions) (*health.WaitResult, error)
}

func newTestHealthChecker() *testHealthChecker {
	return &testHealthChecker{
		waitForServicesFunc: func(ctx context.Context, services []health.ServiceDefinition, opts health.WaitOptions) (*health.WaitResult, error) {
			return &health.WaitResult{
				Success:  true,
				Duration: 5 * time.Second,
				Services: []health.HealthStatus{
					{Name: "orchestrator", State: health.HealthStateHealthy},
					{Name: "weaviate", State: health.HealthStateHealthy},
				},
			}, nil
		},
	}
}

func (m *testHealthChecker) WaitForServices(ctx context.Context, services []health.ServiceDefinition, opts health.WaitOptions) (*health.WaitResult, error) {
	if m.waitForServicesFunc != nil {
		return m.waitForServicesFunc(ctx, services, opts)
	}
	return &health.WaitResult{Success: true}, nil
}

func (m *testHealthChecker) CheckService(_ context.Context, _ health.ServiceDefinition) (*health.HealthStatus, error) {
	return &health.HealthStatus{State: health.HealthStateHealthy}, nil
}

func (m *testHealthChecker) CheckAllServices(_ context.Context, _ []health.ServiceDefinition) ([]health.HealthStatus, error) {
	return []health.HealthStatus{{State: health.HealthStateHealthy}}, nil
}

func (m *testHealthChecker) IsContainerRunning(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// testDiagnosticsCollector is a minimal mock for DiagnosticsCollector.
type testDiagnosticsCollector struct {
	collectFunc func(ctx context.Context, opts diagnostics.CollectOptions) (*diagnostics.DiagnosticsResult, error)
}

func newTestDiagnosticsCollector() *testDiagnosticsCollector {
	return &testDiagnosticsCollector{
		collectFunc: func(ctx context.Context, opts diagnostics.CollectOptions) (*diagnostics.DiagnosticsResult, error) {
			return &diagnostics.DiagnosticsResult{Location: "/tmp/diagnostics.json"}, nil
		},
	}
}

func (m *testDiagnosticsCollector) Collect(ctx context.Context, opts diagnostics.CollectOptions) (*diagnostics.DiagnosticsResult, error) {
	if m.collectFunc != nil {
		return m.collectFunc(ctx, opts)
	}
	return &diagnostics.DiagnosticsResult{}, nil
}

func (m *testDiagnosticsCollector) GetLastResult() *diagnostics.DiagnosticsResult {
	return &diagnostics.DiagnosticsResult{}
}

func (m *testDiagnosticsCollector) SetTracer(_ diagnostics.DiagnosticsTracer) {}

func (m *testDiagnosticsCollector) SetFormatter(_ diagnostics.DiagnosticsFormatter) {}

func (m *testDiagnosticsCollector) SetStorage(_ diagnostics.DiagnosticsStorage) {}

// =============================================================================
// Test Helper Functions
// =============================================================================

type stackTestMocks struct {
	infra       *testInfraManager
	secrets     *MockSecretsManager
	cache       *MockCachePathResolver
	compose     *testComposeExecutor
	health      *testHealthChecker
	models      *testModelEnsurer
	profile     *testProfileResolver
	diagnostics *testDiagnosticsCollector
}

func newTestStackManagerWithMocks() (*DefaultStackManager, *stackTestMocks) {
	mocks := &stackTestMocks{
		infra:       newTestInfraManager(),
		secrets:     NewMockSecretsManager(),
		cache:       NewMockCachePathResolver(),
		compose:     newTestComposeExecutor(),
		health:      newTestHealthChecker(),
		models:      newTestModelEnsurer(),
		profile:     newTestProfileResolver(),
		diagnostics: newTestDiagnosticsCollector(),
	}

	// Configure mock defaults for secrets
	mocks.secrets.Secrets["ANTHROPIC_API_KEY"] = "sk-ant-test"

	// Configure mock defaults for cache
	mocks.cache.ResolvedPaths[CacheTypeModels] = "/test/cache/models"

	cfg := &config.AleutianConfig{
		Machine: config.MachineConfig{
			Id:           "test-machine",
			CPUCount:     4,
			MemoryAmount: 8192,
		},
	}

	mgr, err := NewDefaultStackManager(
		mocks.infra,
		mocks.secrets,
		mocks.cache,
		mocks.compose,
		mocks.health,
		mocks.models,
		mocks.profile,
		mocks.diagnostics,
		cfg,
	)
	if err != nil {
		panic(fmt.Sprintf("failed to create test manager: %v", err))
	}

	mgr.SetOutput(&bytes.Buffer{})

	return mgr, mocks
}

// =============================================================================
// Start() Tests
// =============================================================================

func TestDefaultStackManager_Start_Success(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Start(ctx, StartOptions{})
	if err != nil {
		t.Fatalf("Start() returned unexpected error: %v", err)
	}

	if len(mocks.infra.ensureReadyCalls) != 1 {
		t.Errorf("EnsureReady called %d times, want 1", len(mocks.infra.ensureReadyCalls))
	}

	if mocks.models.ensureModelsCalls != 1 {
		t.Errorf("EnsureModels called %d times, want 1", mocks.models.ensureModelsCalls)
	}

	if len(mocks.profile.resolveCalls) != 1 {
		t.Errorf("Resolve called %d times, want 1", len(mocks.profile.resolveCalls))
	}
}

func TestDefaultStackManager_Start_SkipModelCheck(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Start(ctx, StartOptions{SkipModelCheck: true})
	if err != nil {
		t.Fatalf("Start() returned unexpected error: %v", err)
	}

	if mocks.models.ensureModelsCalls != 0 {
		t.Errorf("EnsureModels called %d times, want 0", mocks.models.ensureModelsCalls)
	}
}

func TestDefaultStackManager_Start_InfraFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.infra.ensureReadyFunc = func(ctx context.Context, opts infra.InfrastructureOptions) error {
		return errors.New("podman machine failed")
	}

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, ErrInfrastructureNotReady) {
		t.Errorf("error should wrap ErrInfrastructureNotReady, got: %v", err)
	}
}

func TestDefaultStackManager_Start_CacheFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.cache.ResolveError = errors.New("no space left")

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, ErrCacheNotReady) {
		t.Errorf("error should wrap ErrCacheNotReady, got: %v", err)
	}
}

func TestDefaultStackManager_Start_ProfileFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.profile.resolveFunc = func(ctx context.Context, opts ProfileOptions) (map[string]string, error) {
		return nil, errors.New("hardware detection failed")
	}

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, ErrProfileResolutionFailed) {
		t.Errorf("error should wrap ErrProfileResolutionFailed, got: %v", err)
	}
}

func TestDefaultStackManager_Start_ComposeFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.upFunc = func(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error) {
		return nil, errors.New("container build failed")
	}

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, ErrComposeUpFailed) {
		t.Errorf("error should wrap ErrComposeUpFailed, got: %v", err)
	}
}

func TestDefaultStackManager_Start_HealthFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.health.waitForServicesFunc = func(ctx context.Context, services []health.ServiceDefinition, opts health.WaitOptions) (*health.WaitResult, error) {
		return &health.WaitResult{
			Success:        false,
			FailedCritical: []string{"orchestrator"},
		}, nil
	}

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, ErrServicesUnhealthy) {
		t.Errorf("error should wrap ErrServicesUnhealthy, got: %v", err)
	}
}

func TestDefaultStackManager_Start_ContextCancelled(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should be context.Canceled, got: %v", err)
	}
}

// =============================================================================
// Stop() Tests
// =============================================================================

func TestDefaultStackManager_Stop_Success(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() returned unexpected error: %v", err)
	}
}

func TestDefaultStackManager_Stop_NotRunning(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.statusFunc = func(ctx context.Context) (*compose.ComposeStatus, error) {
		return &compose.ComposeStatus{Running: 0, Stopped: 3}, nil
	}

	err := mgr.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() returned unexpected error: %v", err)
	}
}

func TestDefaultStackManager_Stop_Failure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.stopFunc = func(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error) {
		return nil, errors.New("stop timeout")
	}

	err := mgr.Stop(ctx)
	if err == nil {
		t.Fatal("Stop() should have returned error")
	}
}

// =============================================================================
// Destroy() Tests
// =============================================================================

func TestDefaultStackManager_Destroy_WithVolumes(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Destroy(ctx, true)
	if err != nil {
		t.Fatalf("Destroy() returned unexpected error: %v", err)
	}
}

func TestDefaultStackManager_Destroy_WithoutVolumes(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Destroy(ctx, false)
	if err != nil {
		t.Fatalf("Destroy() returned unexpected error: %v", err)
	}
}

func TestDefaultStackManager_Destroy_DownFailure(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.downFunc = func(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error) {
		return nil, errors.New("down failed")
	}

	err := mgr.Destroy(ctx, false)
	if err == nil {
		t.Fatal("Destroy() should have returned error")
	}
}

// =============================================================================
// Status() Tests
// =============================================================================

func TestDefaultStackManager_Status_Running(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() returned unexpected error: %v", err)
	}

	if status.State != "running" {
		t.Errorf("State = %q, want 'running'", status.State)
	}

	if status.RunningCount != 3 {
		t.Errorf("RunningCount = %d, want 3", status.RunningCount)
	}
}

func TestDefaultStackManager_Status_Stopped(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.statusFunc = func(ctx context.Context) (*compose.ComposeStatus, error) {
		return &compose.ComposeStatus{Running: 0, Stopped: 3}, nil
	}

	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() returned unexpected error: %v", err)
	}

	if status.State != "stopped" {
		t.Errorf("State = %q, want 'stopped'", status.State)
	}
}

func TestDefaultStackManager_Status_Partial(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.statusFunc = func(ctx context.Context) (*compose.ComposeStatus, error) {
		return &compose.ComposeStatus{Running: 2, Stopped: 1}, nil
	}

	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() returned unexpected error: %v", err)
	}

	if status.State != "partial" {
		t.Errorf("State = %q, want 'partial'", status.State)
	}
}

// =============================================================================
// Logs() Tests
// =============================================================================

func TestDefaultStackManager_Logs_Success(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Logs(ctx, nil)
	if err != nil {
		t.Fatalf("Logs() returned unexpected error: %v", err)
	}
}

func TestDefaultStackManager_Logs_NotRunning(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	mocks.compose.statusFunc = func(ctx context.Context) (*compose.ComposeStatus, error) {
		return &compose.ComposeStatus{Running: 0, Stopped: 3}, nil
	}

	err := mgr.Logs(ctx, nil)
	if err == nil {
		t.Fatal("Logs() should have returned error")
	}

	if !errors.Is(err, ErrStackNotRunning) {
		t.Errorf("error should be ErrStackNotRunning, got: %v", err)
	}
}

func TestDefaultStackManager_Logs_WithServices(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	err := mgr.Logs(ctx, []string{"orchestrator"})
	if err != nil {
		t.Fatalf("Logs() returned unexpected error: %v", err)
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestDefaultStackManager_ConcurrentStart(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.Start(ctx, StartOptions{})
		}()
	}
	wg.Wait()
}

func TestDefaultStackManager_ConcurrentStop(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.Stop(ctx)
		}()
	}
	wg.Wait()
}

// =============================================================================
// Security Hardening Tests
// =============================================================================

func TestNewDefaultStackManager_NilDependency(t *testing.T) {
	cfg := &config.AleutianConfig{}

	tests := []struct {
		name     string
		nilField string
		create   func() (*DefaultStackManager, error)
	}{
		{
			name:     "nil infra",
			nilField: "InfrastructureManager",
			create: func() (*DefaultStackManager, error) {
				return NewDefaultStackManager(
					nil, // infra
					NewMockSecretsManager(),
					NewMockCachePathResolver(),
					newTestComposeExecutor(),
					newTestHealthChecker(),
					newTestModelEnsurer(),
					newTestProfileResolver(),
					newTestDiagnosticsCollector(),
					cfg,
				)
			},
		},
		{
			name:     "nil secrets",
			nilField: "SecretsManager",
			create: func() (*DefaultStackManager, error) {
				return NewDefaultStackManager(
					newTestInfraManager(),
					nil, // secrets
					NewMockCachePathResolver(),
					newTestComposeExecutor(),
					newTestHealthChecker(),
					newTestModelEnsurer(),
					newTestProfileResolver(),
					newTestDiagnosticsCollector(),
					cfg,
				)
			},
		},
		{
			name:     "nil config",
			nilField: "AleutianConfig",
			create: func() (*DefaultStackManager, error) {
				return NewDefaultStackManager(
					newTestInfraManager(),
					NewMockSecretsManager(),
					NewMockCachePathResolver(),
					newTestComposeExecutor(),
					newTestHealthChecker(),
					newTestModelEnsurer(),
					newTestProfileResolver(),
					newTestDiagnosticsCollector(),
					nil, // config
				)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := tc.create()
			if err == nil {
				t.Fatalf("expected error for nil %s, got nil", tc.nilField)
			}
			if mgr != nil {
				t.Error("expected nil manager when error is returned")
			}
			if !errors.Is(err, ErrNilDependency) {
				t.Errorf("expected ErrNilDependency, got: %v", err)
			}
		})
	}
}

func TestNewDefaultStackManager_NilModelsAllowed(t *testing.T) {
	cfg := &config.AleutianConfig{}

	mgr, err := NewDefaultStackManager(
		newTestInfraManager(),
		NewMockSecretsManager(),
		NewMockCachePathResolver(),
		newTestComposeExecutor(),
		newTestHealthChecker(),
		nil, // models can be nil
		newTestProfileResolver(),
		newTestDiagnosticsCollector(),
		cfg,
	)

	if err != nil {
		t.Fatalf("nil models should be allowed, got error: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestDefaultStackManager_Logs_InvalidServiceName(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	ctx := context.Background()

	tests := []struct {
		name     string
		services []string
	}{
		{"path traversal", []string{"../../etc/passwd"}},
		{"shell injection", []string{"foo;rm -rf /"}},
		{"empty name", []string{""}},
		{"too long", []string{string(make([]byte, 100))}},
		{"uppercase", []string{"Orchestrator"}},
		{"spaces", []string{"my service"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mgr.Logs(ctx, tc.services)
			if err == nil {
				t.Fatalf("expected error for services %v, got nil", tc.services)
			}
			if !errors.Is(err, ErrInvalidServiceName) {
				t.Errorf("expected ErrInvalidServiceName, got: %v", err)
			}
		})
	}
}

func TestDefaultStackManager_SetOutput_Nil(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()

	// Should not panic when setting nil output
	mgr.SetOutput(nil)

	// Verify that operations don't panic with nil output
	ctx := context.Background()
	_ = mgr.Start(ctx, StartOptions{})
}

func TestSanitizeErrorForDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "api key pattern",
			input:    "failed with api_key=sk-1234567890abc",
			contains: "[REDACTED]",
		},
		{
			name:     "bearer token",
			input:    "auth failed: Bearer eyJhbGciOiJIUzI1NiJ9.test",
			contains: "[REDACTED]",
		},
		{
			name:     "safe message preserved",
			input:    "connection timeout after 30s",
			contains: "connection timeout after 30s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeErrorForDiagnostics(tc.input)
			if !strings.Contains(result, tc.contains) {
				t.Errorf("expected result to contain %q, got: %q", tc.contains, result)
			}
		})
	}
}

func TestDefaultStackManager_Start_PanicRecovery(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	// Configure infrastructure to panic
	mocks.infra.ensureReadyFunc = func(ctx context.Context, opts infra.InfrastructureOptions) error {
		panic("simulated infrastructure panic")
	}

	err := mgr.Start(ctx, StartOptions{})

	// Panic should be recovered and converted to error
	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}

	if !errors.Is(err, ErrPanicRecovered) {
		t.Errorf("expected ErrPanicRecovered, got: %v", err)
	}

	if !strings.Contains(err.Error(), "simulated infrastructure panic") {
		t.Errorf("error should contain panic message, got: %v", err)
	}

	// Verify mutex is released by being able to call Start again
	mocks.infra.ensureReadyFunc = nil // Reset to normal
	err2 := mgr.Start(ctx, StartOptions{})
	if err2 != nil {
		t.Errorf("second Start should succeed, got error: %v", err2)
	}
}

func TestDefaultStackManager_Stop_PanicRecovery(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	// Configure stop to panic
	mocks.compose.stopFunc = func(ctx context.Context, opts compose.StopOptions) (*compose.StopResult, error) {
		panic("simulated stop panic")
	}

	err := mgr.Stop(ctx)

	// Panic should be recovered and converted to error
	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}

	// Panic is recovered either by the spinner (SpinWhileContext) or by the Stop function.
	// Both wrap the panic in an error message containing "panic".
	errMsg := err.Error()
	if !strings.Contains(errMsg, "panic") {
		t.Errorf("expected error message to mention panic, got: %v", err)
	}
}

func TestDefaultStackManager_Destroy_PanicRecovery(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	ctx := context.Background()

	// Configure down to panic
	mocks.compose.downFunc = func(ctx context.Context, opts compose.DownOptions) (*compose.ComposeResult, error) {
		panic("simulated destroy panic")
	}

	err := mgr.Destroy(ctx, false)

	// Panic should be recovered and converted to error
	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}

	if !errors.Is(err, ErrPanicRecovered) {
		t.Errorf("expected ErrPanicRecovered, got: %v", err)
	}
}

// =============================================================================
// Registry Auth Detection Tests
// =============================================================================

func TestIsRegistryAuthError(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"unauthorized", "Error: unauthorized: access denied", true},
		{"401 status", "HTTP 401: not authorized", true},
		{"authentication required", "Error: authentication required for gcr.io", true},
		{"denied access", "denied: access forbidden", true},
		{"login required", "Error: login required", true},
		{"unauthenticated", "error: unauthenticated: invalid token", true},
		{"credential error", "credential helper failed", true},
		{"403 forbidden", "Error response: 403 Forbidden", true},
		{"normal pull", "Pulling image docker.io/library/redis:latest", false},
		{"build error", "container build failed: syntax error", false},
		{"empty string", "", false},
		{"network error", "dial tcp: connection refused", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRegistryAuthError(tt.output)
			if got != tt.want {
				t.Errorf("isRegistryAuthError(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestCheckRegistryAuthHint_PrintsHint(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	var buf bytes.Buffer
	mgr.SetOutput(&buf)

	result := &compose.ComposeResult{
		Stderr: "Error: unauthorized: access to gcr.io/my-project/image denied",
	}
	mgr.checkRegistryAuthHint(result, nil)

	output := buf.String()
	if !strings.Contains(output, "Registry authentication failed") {
		t.Error("expected auth failure message in output")
	}
	if !strings.Contains(output, "gcloud auth login") {
		t.Error("expected gcloud hint in output")
	}
	if !strings.Contains(output, "podman login") {
		t.Error("expected podman login hint in output")
	}
}

func TestCheckRegistryAuthHint_NoHintForNormalErrors(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	var buf bytes.Buffer
	mgr.SetOutput(&buf)

	result := &compose.ComposeResult{
		Stderr: "Error: network timeout pulling image",
	}
	mgr.checkRegistryAuthHint(result, nil)

	if buf.Len() > 0 {
		t.Errorf("expected no output for non-auth error, got: %s", buf.String())
	}
}

func TestCheckRegistryAuthHint_DetectsInError(t *testing.T) {
	mgr, _ := newTestStackManagerWithMocks()
	var buf bytes.Buffer
	mgr.SetOutput(&buf)

	mgr.checkRegistryAuthHint(nil, errors.New("401 Unauthorized"))

	if !strings.Contains(buf.String(), "Registry authentication failed") {
		t.Error("expected auth failure message when error contains auth pattern")
	}
}

func TestStart_ComposeAuthFailure_ShowsHint(t *testing.T) {
	mgr, mocks := newTestStackManagerWithMocks()
	var buf bytes.Buffer
	mgr.SetOutput(&buf)
	ctx := context.Background()

	mocks.compose.upFunc = func(ctx context.Context, opts compose.UpOptions) (*compose.ComposeResult, error) {
		return &compose.ComposeResult{
			Stderr: "Error: unauthorized: authentication required for gcr.io/my-project/image",
		}, errors.New("compose up failed: unauthorized")
	}

	err := mgr.Start(ctx, StartOptions{})
	if err == nil {
		t.Fatal("Start() should have returned error")
	}

	output := buf.String()
	if !strings.Contains(output, "gcloud auth login") {
		t.Errorf("expected gcloud auth hint in output, got: %s", output)
	}
}

// =============================================================================
// Compile-time Interface Compliance
// =============================================================================

var _ infra.InfrastructureManager = (*testInfraManager)(nil)
var _ ProfileResolver = (*testProfileResolver)(nil)
var _ ModelEnsurer = (*testModelEnsurer)(nil)
var _ compose.ComposeExecutor = (*testComposeExecutor)(nil)
var _ health.HealthChecker = (*testHealthChecker)(nil)
var _ diagnostics.DiagnosticsCollector = (*testDiagnosticsCollector)(nil)
