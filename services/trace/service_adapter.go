// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package code_buddy

import (
	"context"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
)

// ServiceAdapter wraps Service to implement agent.GraphInitializer.
//
// Description:
//
//	ServiceAdapter provides a simplified interface to Service for use
//	by the agent graph provider. It uses default languages and excludes
//	for graph initialization.
//
// Thread Safety: ServiceAdapter is safe for concurrent use if the
// underlying Service is safe for concurrent use.
type ServiceAdapter struct {
	service   *Service
	languages []string
	excludes  []string
}

// NewServiceAdapter creates a new adapter.
//
// Description:
//
//	Creates an adapter wrapping the provided Service with default
//	languages (go, python, javascript, typescript) and excludes (vendor, tests).
//
// Inputs:
//
//	service - The Service to wrap.
//
// Outputs:
//
//	*ServiceAdapter - The new adapter.
func NewServiceAdapter(service *Service) *ServiceAdapter {
	return &ServiceAdapter{
		service:   service,
		languages: []string{"go", "python", "javascript", "typescript"},
		excludes:  []string{"vendor", "vendor/*", "*_test.go", "node_modules", "node_modules/*"},
	}
}

// WithLanguages sets the languages to parse.
//
// Inputs:
//
//	languages - Languages to parse.
//
// Outputs:
//
//	*ServiceAdapter - The adapter for chaining.
func (a *ServiceAdapter) WithLanguages(languages []string) *ServiceAdapter {
	a.languages = languages
	return a
}

// WithExcludes sets the exclude patterns.
//
// Inputs:
//
//	excludes - Glob patterns to exclude.
//
// Outputs:
//
//	*ServiceAdapter - The adapter for chaining.
func (a *ServiceAdapter) WithExcludes(excludes []string) *ServiceAdapter {
	a.excludes = excludes
	return a
}

// InitGraph implements agent.GraphInitializer.
//
// Description:
//
//	Initializes a code graph by calling the underlying Service.Init
//	with the configured languages and excludes.
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
func (a *ServiceAdapter) InitGraph(ctx context.Context, projectRoot string) (string, error) {
	result, err := a.service.Init(ctx, projectRoot, a.languages, a.excludes)
	if err != nil {
		return "", err
	}
	return result.GraphID, nil
}

// Ensure ServiceAdapter implements agent.GraphInitializer.
var _ agent.GraphInitializer = (*ServiceAdapter)(nil)
