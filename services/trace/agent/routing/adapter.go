// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
)

// RouterAdapter adapts a routing.ToolRouter to the agent.ToolRouter interface.
//
// # Description
//
// The routing package defines its own types (ToolSpec, CodeContext, ToolSelection)
// for internal use, but the agent package expects its own types (ToolRouterSpec,
// ToolRouterCodeContext, ToolRouterSelection). This adapter bridges the two.
//
// # Thread Safety
//
// RouterAdapter is safe for concurrent use if the underlying router is.
type RouterAdapter struct {
	router ToolRouter
}

// NewRouterAdapter creates an adapter that implements agent.ToolRouter.
//
// # Inputs
//
//   - router: The underlying ToolRouter (Granite4Router, EscalatingRouter, etc).
//
// # Outputs
//
//   - agent.ToolRouter: The adapted router.
func NewRouterAdapter(router ToolRouter) agent.ToolRouter {
	return &RouterAdapter{router: router}
}

// SelectTool implements agent.ToolRouter.
//
// Converts agent types to routing types, calls the underlying router,
// and converts the result back to agent types.
func (a *RouterAdapter) SelectTool(ctx context.Context, query string, availableTools []agent.ToolRouterSpec, codeContext *agent.ToolRouterCodeContext) (*agent.ToolRouterSelection, error) {
	slog.Info("CB-31d RouterAdapter.SelectTool CALLED",
		slog.Int("num_tools", len(availableTools)),
		slog.String("query_preview", truncateForLog(query, 100)),
		slog.Bool("has_code_context", codeContext != nil),
		slog.String("router_model", a.router.Model()),
	)

	// Convert agent.ToolRouterSpec to routing.ToolSpec
	routingSpecs := make([]ToolSpec, len(availableTools))
	for i, spec := range availableTools {
		routingSpecs[i] = ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			BestFor:     spec.BestFor,
			Params:      spec.Params,
			Category:    spec.Category,
			UseWhen:     spec.UseWhen,
			AvoidWhen:   spec.AvoidWhen,
		}
	}

	// Convert agent.ToolRouterCodeContext to routing.CodeContext
	var routingContext *CodeContext
	if codeContext != nil {
		routingContext = &CodeContext{
			Language:    codeContext.Language,
			Files:       codeContext.Files,
			Symbols:     codeContext.Symbols,
			CurrentFile: codeContext.CurrentFile,
			RecentTools: codeContext.RecentTools,
			Progress:    codeContext.Progress,
			StepNumber:  codeContext.StepNumber,
		}

		// Convert PreviousErrors if present
		if len(codeContext.PreviousErrors) > 0 {
			routingContext.PreviousErrors = make([]ToolError, len(codeContext.PreviousErrors))
			for i, err := range codeContext.PreviousErrors {
				routingContext.PreviousErrors[i] = ToolError{
					Tool:      err.Tool,
					Error:     err.Error,
					Timestamp: err.Timestamp,
				}
			}
		}

		// Convert ToolHistory for history-aware routing
		if len(codeContext.ToolHistory) > 0 {
			routingContext.ToolHistory = make([]ToolHistoryEntry, len(codeContext.ToolHistory))
			for i, entry := range codeContext.ToolHistory {
				routingContext.ToolHistory[i] = ToolHistoryEntry{
					Tool:       entry.Tool,
					Summary:    entry.Summary,
					Success:    entry.Success,
					StepNumber: entry.StepNumber,
				}
			}
		}
	}

	// Call the underlying router
	slog.Info("CB-31d RouterAdapter calling Granite4Router.SelectTool",
		slog.Int("num_specs", len(routingSpecs)),
	)

	selection, err := a.router.SelectTool(ctx, query, routingSpecs, routingContext)
	if err != nil {
		slog.Error("CB-31d RouterAdapter.SelectTool FAILED",
			slog.String("error", err.Error()),
		)
		return nil, err
	}

	slog.Info("CB-31d RouterAdapter.SelectTool SUCCEEDED",
		slog.String("selected_tool", selection.Tool),
		slog.Float64("confidence", selection.Confidence),
		slog.Duration("duration", selection.Duration),
	)

	// Convert routing.ToolSelection to agent.ToolRouterSelection
	return &agent.ToolRouterSelection{
		Tool:       selection.Tool,
		Confidence: selection.Confidence,
		ParamsHint: selection.ParamsHint,
		Reasoning:  selection.Reasoning,
		Duration:   selection.Duration,
	}, nil
}

// Model implements agent.ToolRouter.
func (a *RouterAdapter) Model() string {
	return a.router.Model()
}

// Close implements agent.ToolRouter.
func (a *RouterAdapter) Close() error {
	return a.router.Close()
}

// WarmRouter exposes the underlying router's WarmRouter method.
//
// # Description
//
// Allows callers to warm the router model. This is not part of the
// agent.ToolRouter interface but is useful during initialization.
// If the underlying router does not support WarmRouter (e.g., EscalatingRouter),
// this is a no-op.
//
// CB-60: Now accepts a ModelLifecycleManager for provider-agnostic warmup.
func (a *RouterAdapter) WarmRouter(ctx context.Context, lifecycle providers.ModelLifecycleManager) error {
	type warmable interface {
		WarmRouter(ctx context.Context, lifecycle providers.ModelLifecycleManager) error
	}
	if w, ok := a.router.(warmable); ok {
		return w.WarmRouter(ctx, lifecycle)
	}
	return nil
}

// truncateForLog truncates a string for logging purposes.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
