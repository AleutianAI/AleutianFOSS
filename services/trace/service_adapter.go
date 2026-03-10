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
	"fmt"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
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
		excludes: []string{
			"vendor", "vendor/*",
			"*_test.go",
			"node_modules", "node_modules/*",
			".venv", ".venv/*",
			".git", ".git/*",
			"__pycache__", "__pycache__/*",
			"models_cache", "models_cache/*",
			".tox", ".tox/*",
		},
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

// EnrichmentTraceStep implements agent.EnrichmentStepProvider.
//
// Description:
//
//	GR-76: Returns a TraceStep describing the LSP enrichment quality of the
//	cached graph. The CRS journal uses this to record whether graph edges are
//	backed by LSP type-aware resolution or name heuristics only.
//
// Inputs:
//
//	graphID - The graph ID to query.
//
// Outputs:
//
//	*crs.TraceStep - The enrichment TraceStep, or nil if no graph is cached.
//
// Thread Safety: This method is safe for concurrent use.
func (a *ServiceAdapter) EnrichmentTraceStep(graphID string) *crs.TraceStep {
	a.service.mu.RLock()
	cached, ok := a.service.graphs[graphID]
	a.service.mu.RUnlock()

	if !ok || cached == nil {
		return nil
	}

	stats := cached.EnrichmentStats
	meta := map[string]string{
		"placeholders_queried":  fmt.Sprintf("%d", stats.PlaceholdersQueried),
		"placeholders_resolved": fmt.Sprintf("%d", stats.PlaceholdersResolved),
		"placeholders_failed":   fmt.Sprintf("%d", stats.PlaceholdersFailed),
		"placeholders_skipped":  fmt.Sprintf("%d", stats.PlaceholdersSkipped),
		"files_queried":         fmt.Sprintf("%d", stats.FilesQueried),
		"orphans_removed":       fmt.Sprintf("%d", stats.OrphanedRemoved),
		"lsp_errors":            fmt.Sprintf("%d", stats.LSPErrors),
		"duration_us":           fmt.Sprintf("%d", stats.DurationMicro),
	}

	if stats.PlaceholdersQueried > 0 {
		rate := float64(stats.PlaceholdersResolved) / float64(stats.PlaceholdersQueried) * 100
		meta["resolution_rate"] = fmt.Sprintf("%.1f%%", rate)
		meta["lsp_enabled"] = "true"
	} else {
		meta["lsp_enabled"] = "false"
	}

	return &crs.TraceStep{
		Timestamp: time.Now().UnixMilli(),
		Action:    "graph_enrichment",
		Target:    cached.ProjectRoot,
		Metadata:  meta,
	}
}

// Ensure ServiceAdapter implements agent.GraphInitializer.
var _ agent.GraphInitializer = (*ServiceAdapter)(nil)

// Ensure ServiceAdapter implements agent.EnrichmentStepProvider.
var _ agent.EnrichmentStepProvider = (*ServiceAdapter)(nil)
