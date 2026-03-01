// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package agent

import (
	"context"
	"fmt"
)

// GraphInitializer defines the initialization capability needed from a service.
//
// Description:
//
//	This interface abstracts the graph initialization capability.
//	Implementations can wrap trace.Service or provide mock behavior.
type GraphInitializer interface {
	// InitGraph initializes a code graph for a project.
	//
	// Inputs:
	//   ctx - Context for cancellation.
	//   projectRoot - Path to the project root.
	//
	// Outputs:
	//   string - The graph ID.
	//   error - Non-nil if initialization fails.
	InitGraph(ctx context.Context, projectRoot string) (string, error)
}

// ServiceGraphProvider adapts a GraphInitializer to phases.GraphProvider.
//
// Description:
//
//	ServiceGraphProvider wraps a GraphInitializer to provide graph
//	initialization capabilities to the agent phases. It implements
//	the phases.GraphProvider interface.
//
// Thread Safety: ServiceGraphProvider is safe for concurrent use if
// the underlying initializer is safe for concurrent use.
type ServiceGraphProvider struct {
	initializer GraphInitializer
}

// NewServiceGraphProvider creates a new graph provider.
//
// Description:
//
//	Creates a GraphProvider that delegates to the provided initializer.
//
// Inputs:
//
//	initializer - The graph initializer to wrap.
//
// Outputs:
//
//	*ServiceGraphProvider - The new provider.
func NewServiceGraphProvider(initializer GraphInitializer) *ServiceGraphProvider {
	return &ServiceGraphProvider{
		initializer: initializer,
	}
}

// Initialize implements phases.GraphProvider.
//
// Description:
//
//	Initializes a code graph for the given project root by delegating
//	to the wrapped initializer.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	projectRoot - Path to the project root.
//
// Outputs:
//
//	string - The graph ID.
//	error - Non-nil if initialization fails.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ServiceGraphProvider) Initialize(ctx context.Context, projectRoot string) (string, error) {
	if p.initializer == nil {
		return "", fmt.Errorf("graph initializer is nil")
	}

	graphID, err := p.initializer.InitGraph(ctx, projectRoot)
	if err != nil {
		return "", fmt.Errorf("graph initialization failed: %w", err)
	}

	return graphID, nil
}

// IsAvailable implements phases.GraphProvider.
//
// Description:
//
//	Returns whether the graph service is available for use.
//	Returns true if the initializer is set.
//
// Outputs:
//
//	bool - True if the initializer is available.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ServiceGraphProvider) IsAvailable() bool {
	return p.initializer != nil
}

// NullGraphProvider is a no-op graph provider for degraded mode.
//
// Description:
//
//	NullGraphProvider always returns errors and reports unavailable.
//	Use this when the graph service is not configured or unavailable.
//
// Thread Safety: NullGraphProvider is safe for concurrent use.
type NullGraphProvider struct{}

// Initialize implements phases.GraphProvider.
//
// Description:
//
//	Always returns an error indicating the service is unavailable.
//
// Outputs:
//
//	string - Empty string.
//	error - Always returns ErrServiceUnavailable.
func (p *NullGraphProvider) Initialize(ctx context.Context, projectRoot string) (string, error) {
	return "", fmt.Errorf("graph service unavailable (degraded mode)")
}

// IsAvailable implements phases.GraphProvider.
//
// Description:
//
//	Always returns false.
//
// Outputs:
//
//	bool - Always false.
func (p *NullGraphProvider) IsAvailable() bool {
	return false
}
