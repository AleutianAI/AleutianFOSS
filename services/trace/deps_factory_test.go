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
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/phases"
)

// =============================================================================
// Factory Option Tests
// =============================================================================

func TestWithCoordinatorEnabled_EnablesCoordinator(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(true),
	)

	if !factory.enableCoordinator {
		t.Error("Expected enableCoordinator to be true")
	}
}

func TestWithCoordinatorEnabled_DisablesCoordinator(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(false),
	)

	if factory.enableCoordinator {
		t.Error("Expected enableCoordinator to be false")
	}
}

func TestNewDependenciesFactory_DefaultDisablesCoordinator(t *testing.T) {
	factory := NewDependenciesFactory()

	if factory.enableCoordinator {
		t.Error("Expected enableCoordinator to be false by default")
	}
}

// =============================================================================
// Create Tests
// =============================================================================

func TestCreate_WithCoordinatorEnabled_CreatesCoordinator(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(true),
	)

	session := &agent.Session{
		ID: "test-session-123",
	}

	depsAny, err := factory.Create(session, "test query")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	deps, ok := depsAny.(*phases.Dependencies)
	if !ok {
		t.Fatal("Expected *phases.Dependencies")
	}

	if deps.Coordinator == nil {
		t.Error("Expected Coordinator to be created when enabled")
	}
}

func TestCreate_WithoutCoordinatorEnabled_NoCoordinator(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(false),
	)

	session := &agent.Session{
		ID: "test-session-456",
	}

	depsAny, err := factory.Create(session, "test query")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	deps, ok := depsAny.(*phases.Dependencies)
	if !ok {
		t.Fatal("Expected *phases.Dependencies")
	}

	if deps.Coordinator != nil {
		t.Error("Expected Coordinator to be nil when disabled")
	}
}

func TestCreate_WithCoordinatorEnabled_RegistersForCleanup(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(true),
	)

	sessionID := "test-cleanup-session"
	session := &agent.Session{
		ID: sessionID,
	}

	_, err := factory.Create(session, "test query")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Verify the coordinator is registered in the registry
	coordinatorRegistry.mu.RLock()
	_, exists := coordinatorRegistry.coordinators[sessionID]
	coordinatorRegistry.mu.RUnlock()

	if !exists {
		t.Error("Expected coordinator to be registered for cleanup")
	}

	// Clean up
	cleanupCoordinator(sessionID)

	// Verify cleanup worked
	coordinatorRegistry.mu.RLock()
	_, stillExists := coordinatorRegistry.coordinators[sessionID]
	coordinatorRegistry.mu.RUnlock()

	if stillExists {
		t.Error("Expected coordinator to be removed after cleanup")
	}
}

// =============================================================================
// Cleanup Tests
// =============================================================================

func TestCleanupCoordinator_HandlesNonExistent(t *testing.T) {
	// Should not panic when cleaning up non-existent session
	cleanupCoordinator("non-existent-session-id")
}

func TestCoordinatorRegistry_ConcurrentAccess(t *testing.T) {
	factory := NewDependenciesFactory(
		WithCoordinatorEnabled(true),
	)

	// Create multiple sessions concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			session := &agent.Session{
				ID: "concurrent-session-" + string(rune('0'+idx)),
			}
			_, _ = factory.Create(session, "query")
			cleanupCoordinator(session.ID)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
