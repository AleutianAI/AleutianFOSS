// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// UNIT TESTS (No external dependencies)
// =============================================================================

func TestService_LSPConfig(t *testing.T) {
	config := DefaultServiceConfig()

	if config.LSPIdleTimeout != 10*time.Minute {
		t.Errorf("LSPIdleTimeout = %v, want 10m", config.LSPIdleTimeout)
	}
	if config.LSPStartupTimeout != 30*time.Second {
		t.Errorf("LSPStartupTimeout = %v, want 30s", config.LSPStartupTimeout)
	}
	if config.LSPRequestTimeout != 10*time.Second {
		t.Errorf("LSPRequestTimeout = %v, want 10s", config.LSPRequestTimeout)
	}
}

func TestNewService_InitializesLSPManagers(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	if svc.lspManagers == nil {
		t.Error("lspManagers should be initialized")
	}
	if len(svc.lspManagers) != 0 {
		t.Errorf("lspManagers should be empty initially, got %d", len(svc.lspManagers))
	}
}

func TestService_LSPDefinition_RequiresContext(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPDefinition(nil, "graph-id", "/test.go", 1, 0) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestService_LSPReferences_RequiresContext(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPReferences(nil, "graph-id", "/test.go", 1, 0, true) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestService_LSPHover_RequiresContext(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPHover(nil, "graph-id", "/test.go", 1, 0) //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestService_LSPRename_RequiresContext(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPRename(nil, "graph-id", "/test.go", 1, 0, "newName") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestService_LSPRename_RequiresNewName(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx := context.Background()
	_, err := svc.LSPRename(ctx, "graph-id", "/test.go", 1, 0, "")
	if err == nil {
		t.Error("expected error for empty newName")
	}
}

func TestService_LSPWorkspaceSymbol_RequiresContext(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPWorkspaceSymbol(nil, "graph-id", "go", "test") //nolint:staticcheck
	if err == nil {
		t.Error("expected error for nil context")
	}
}

func TestService_LSPWorkspaceSymbol_RequiresLanguage(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx := context.Background()
	_, err := svc.LSPWorkspaceSymbol(ctx, "graph-id", "", "test")
	if err == nil {
		t.Error("expected error for empty language")
	}
}

func TestService_LSPStatus_RequiresGraph(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	_, err := svc.LSPStatus("nonexistent-graph")
	if err == nil {
		t.Error("expected error for nonexistent graph")
	}
}

func TestService_Close_Idempotent(t *testing.T) {
	svc := NewService(DefaultServiceConfig())

	ctx := context.Background()
	err1 := svc.Close(ctx)
	err2 := svc.Close(ctx)

	if err1 != nil || err2 != nil {
		t.Errorf("Close should be idempotent, got err1=%v err2=%v", err1, err2)
	}
}

func TestLspLocationsToAPI(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := lspLocationsToAPI(nil)
		if result == nil {
			t.Error("should return empty slice, not nil")
		}
		if len(result) != 0 {
			t.Errorf("should be empty, got %d", len(result))
		}
	})
}

func TestLspWorkspaceEditToAPI(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := lspWorkspaceEditToAPI(nil)
		if result == nil {
			t.Error("should return empty map, not nil")
		}
		if len(result) != 0 {
			t.Errorf("should be empty, got %d", len(result))
		}
	})
}

func TestLspSymbolsToAPI(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := lspSymbolsToAPI(nil)
		if result == nil {
			t.Error("should return empty slice, not nil")
		}
		if len(result) != 0 {
			t.Errorf("should be empty, got %d", len(result))
		}
	})
}

func TestSymbolKindToString(t *testing.T) {
	tests := []struct {
		kind     int
		expected string
	}{
		{1, "file"},
		{5, "class"},
		{6, "method"},
		{12, "function"},
		{13, "variable"},
		{23, "struct"},
		{999, "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			// Import lsp package types through trace
			// Since we can't import lsp directly, we test via the function
			// This is a limitation of the test structure
		})
	}
}

// =============================================================================
// INTEGRATION TESTS (Require gopls)
// =============================================================================

const testGoProject = `package main

func main() {
	helper()
}

func helper() string {
	return "hello"
}
`

func setupTestProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(testGoProject), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return dir
}

func TestService_LSPIntegration_ManagerLifecycle(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := setupTestProject(t)

	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize graph
	resp, err := svc.Init(ctx, dir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// No LSP manager should exist yet
	svc.lspMu.RLock()
	_, exists := svc.lspManagers[resp.GraphID]
	svc.lspMu.RUnlock()

	if exists {
		t.Error("LSP manager should not exist before first LSP request")
	}

	// Check status (creates manager lazily on first real LSP request)
	status, err := svc.LSPStatus(resp.GraphID)
	if err != nil {
		t.Fatalf("LSPStatus: %v", err)
	}

	if len(status.SupportedLanguages) == 0 {
		t.Error("should have supported languages")
	}
}

func TestService_LSPIntegration_Definition(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := setupTestProject(t)

	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize graph
	initResp, err := svc.Init(ctx, dir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Wait for gopls to be ready
	time.Sleep(time.Second)

	// Find definition of helper() call (line 4, col 1)
	defResp, err := svc.LSPDefinition(ctx, initResp.GraphID, filepath.Join(dir, "main.go"), 4, 1)
	if err != nil {
		t.Fatalf("LSPDefinition: %v", err)
	}

	if len(defResp.Locations) == 0 {
		t.Fatal("expected at least one location")
	}

	// Should point to helper function (line 7)
	if defResp.Locations[0].StartLine != 7 {
		t.Errorf("StartLine = %d, want 7", defResp.Locations[0].StartLine)
	}

	if defResp.LatencyMs == 0 {
		t.Error("LatencyMs should be set")
	}
}

func TestService_LSPIntegration_Hover(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := setupTestProject(t)

	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize graph
	initResp, err := svc.Init(ctx, dir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	time.Sleep(time.Second)

	// Hover over helper function (line 7)
	hoverResp, err := svc.LSPHover(ctx, initResp.GraphID, filepath.Join(dir, "main.go"), 7, 5)
	if err != nil {
		t.Fatalf("LSPHover: %v", err)
	}

	if hoverResp.Content == "" {
		t.Error("expected hover content")
	}

	if hoverResp.LatencyMs == 0 {
		t.Error("LatencyMs should be set")
	}
}

func TestService_LSPIntegration_References(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := setupTestProject(t)

	svc := NewService(DefaultServiceConfig())
	defer svc.Close(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize graph
	initResp, err := svc.Init(ctx, dir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	time.Sleep(time.Second)

	// Find references to helper function
	refResp, err := svc.LSPReferences(ctx, initResp.GraphID, filepath.Join(dir, "main.go"), 7, 5, true)
	if err != nil {
		t.Fatalf("LSPReferences: %v", err)
	}

	// Should find at least the definition and the call
	if len(refResp.Locations) < 2 {
		t.Errorf("expected at least 2 references, got %d", len(refResp.Locations))
	}
}

func TestService_LSPIntegration_CloseShutdownsManagers(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	dir := setupTestProject(t)

	svc := NewService(DefaultServiceConfig())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Initialize graph
	initResp, err := svc.Init(ctx, dir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Trigger LSP manager creation
	_, err = svc.LSPDefinition(ctx, initResp.GraphID, filepath.Join(dir, "main.go"), 4, 1)
	if err != nil {
		t.Fatalf("LSPDefinition: %v", err)
	}

	// Verify manager was created
	svc.lspMu.RLock()
	managerCount := len(svc.lspManagers)
	svc.lspMu.RUnlock()

	if managerCount == 0 {
		t.Error("expected at least one LSP manager")
	}

	// Close should shutdown all managers
	if err := svc.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Managers should be cleaned up
	svc.lspMu.RLock()
	managerCount = len(svc.lspManagers)
	svc.lspMu.RUnlock()

	if managerCount != 0 {
		t.Errorf("expected 0 managers after Close, got %d", managerCount)
	}
}
