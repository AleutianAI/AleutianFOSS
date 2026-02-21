// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_control_dependencies Tool (GR-17c) - Typed Implementation
// =============================================================================

var findControlDependenciesTracer = otel.Tracer("tools.find_control_dependencies")

// FindControlDependenciesParams contains the validated input parameters.
type FindControlDependenciesParams struct {
	// Target is the function name to analyze.
	Target string

	// Depth is the maximum dependency chain depth.
	// Default: 5, Max: 10
	Depth int
}

// ToolName returns the tool name for TypedParams interface.
func (p FindControlDependenciesParams) ToolName() string { return "find_control_dependencies" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindControlDependenciesParams) ToMap() map[string]any {
	return map[string]any{
		"target": p.Target,
		"depth":  p.Depth,
	}
}

// FindControlDependenciesOutput contains the structured result.
type FindControlDependenciesOutput struct {
	// Target is the function that was analyzed.
	Target string `json:"target"`

	// TargetID is the resolved symbol ID.
	TargetID string `json:"target_id,omitempty"`

	// Dependencies lists the nodes that control this target's execution.
	Dependencies []ControllerInfo `json:"dependencies"`

	// DependencyCount is the total number of control dependencies.
	DependencyCount int `json:"dependency_count"`

	// Summary contains aggregate statistics.
	Summary ControlDependencySummary `json:"summary"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// ControllerInfo holds information about a controlling node.
type ControllerInfo struct {
	// ID is the full node ID of the controller.
	ID string `json:"id"`

	// Name is the function name of the controller.
	Name string `json:"name"`

	// File is the source file path.
	File string `json:"file,omitempty"`

	// Line is the line number.
	Line int `json:"line,omitempty"`

	// Package is the package name.
	Package string `json:"package,omitempty"`

	// DependentsCount is how many nodes this controller controls.
	DependentsCount int `json:"dependents_count,omitempty"`

	// Depth is the distance from target in the dependency chain.
	Depth int `json:"depth,omitempty"`
}

// ControlDependencySummary contains aggregate statistics.
type ControlDependencySummary struct {
	// TotalDependencies is the total control dependencies found.
	TotalDependencies int `json:"total_dependencies"`

	// MaxDepth is the maximum depth of the dependency chain.
	MaxDepth int `json:"max_depth"`

	// TopControllers is the number of controllers with most dependents.
	TopControllers int `json:"top_controllers"`

	// NodesAnalyzed is the total nodes considered.
	NodesAnalyzed int `json:"nodes_analyzed"`
}

// findControlDependenciesTool finds which conditionals control whether a function executes.
type findControlDependenciesTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindControlDependenciesTool creates a new find_control_dependencies tool.
//
// Description:
//
//	Creates a tool for finding control dependencies - which conditional
//	nodes (branch points) determine whether a target function executes.
//
// Inputs:
//   - analytics: GraphAnalytics instance for control dependence computation. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_control_dependencies tool.
//
// Limitations:
//   - Requires post-dominator tree computation
//   - May not detect all control flow patterns in highly dynamic code
//
// Thread Safety: Safe for concurrent use after construction.
func NewFindControlDependenciesTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findControlDependenciesTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findControlDependenciesTool) Name() string {
	return "find_control_dependencies"
}

func (t *findControlDependenciesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findControlDependenciesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_control_dependencies",
		Description: "Find which conditionals control whether a function executes. " +
			"Shows decision points that determine if code runs. " +
			"Essential for understanding conditional execution paths.",
		Parameters: map[string]ParamDef{
			"target": {
				Type:        ParamTypeString,
				Description: "Target function to analyze",
				Required:    true,
			},
			"depth": {
				Type:        ParamTypeInt,
				Description: "Maximum dependency chain depth (default: 5, max: 10)",
				Required:    false,
				Default:     5,
			},
		},
		Category:    CategoryExploration,
		Priority:    83,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     45 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"control dependency", "control flow", "conditional",
				"branch", "decision point", "controls execution",
				"determines whether", "if statement", "switch",
			},
			UseWhen: "User asks about what conditionals control a function's execution, " +
				"or wants to understand decision points affecting code flow.",
			AvoidWhen: "User asks about call chains (use get_call_chain) or " +
				"dominators (use find_dominators).",
		},
	}
}

// Execute runs the find_control_dependencies tool.
func (t *findControlDependenciesTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Check for nil analytics
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// GR-17b Fix: Check graph readiness with retry logic
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	for attempt := 0; attempt < maxRetries; attempt++ {
		if t.analytics.IsGraphReady() {
			break
		}

		if attempt < maxRetries-1 {
			t.logger.Info("graph not ready, retrying after delay",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", maxRetries),
				slog.Duration("retry_delay", retryDelay),
			)
			time.Sleep(retryDelay)
		} else {
			return &Result{
				Success: false,
				Error: "graph not ready - indexing still in progress. " +
					"Please wait a few seconds for graph initialization to complete and try again.",
			}, nil
		}
	}

	// Continue with existing logic
	return t.executeOnce(ctx, p)
}

// executeOnce performs a single execution attempt (extracted for retry logic).
func (t *findControlDependenciesTool) executeOnce(ctx context.Context, p FindControlDependenciesParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := findControlDependenciesTracer.Start(ctx, "findControlDependenciesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_control_dependencies"),
			attribute.String("target", p.Target),
			attribute.Int("depth", p.Depth),
		),
	)
	defer span.End()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Resolve target symbol
	targetID, err := t.resolveTarget(ctx, p.Target)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to resolve target: %v", err),
		}, nil
	}

	span.SetAttributes(attribute.String("target_id", targetID))

	// Detect entry and exit points for dominator trees
	entry, err := DetectEntryPoint(ctx, t.index, t.analytics)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to detect entry point: %v", err),
		}, nil
	}

	// Compute post-dominator tree for control dependence
	postDomTree, err := t.analytics.PostDominators(ctx, "")
	if err != nil {
		span.RecordError(err)
		t.logger.Debug("failed to compute post-dominators, using dominator-based approximation",
			slog.String("tool", "find_control_dependencies"),
			slog.String("error", err.Error()),
		)
		// Fall back to using dominator tree as approximation
		postDomTree = nil
	}

	// Compute control dependence
	var controlDeps *graph.ControlDependence
	var traceStep crs.TraceStep

	if postDomTree != nil {
		controlDeps, traceStep = t.analytics.ComputeControlDependenceWithCRS(ctx, postDomTree)
	} else {
		// Use dominator tree as fallback
		domTree, domErr := t.analytics.Dominators(ctx, entry)
		if domErr != nil {
			span.RecordError(domErr)
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("failed to compute dominators: %v", domErr),
			}, nil
		}
		// Create approximation using dominators
		controlDeps, traceStep = t.approximateControlDependence(ctx, domTree, targetID)
	}

	if controlDeps == nil {
		return &Result{
			Success: true,
			Output: FindControlDependenciesOutput{
				Target:          p.Target,
				TargetID:        targetID,
				Dependencies:    []ControllerInfo{},
				DependencyCount: 0,
				Summary: ControlDependencySummary{
					TotalDependencies: 0,
					MaxDepth:          0,
				},
				Message: "No control dependencies found",
			},
			TraceStep: &traceStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Get dependencies for the target
	deps := controlDeps.GetDependencies(targetID)

	// Limit depth if needed
	filteredDeps := t.filterByDepth(deps, p.Depth)

	// Build output
	output := t.buildOutput(p.Target, targetID, filteredDeps, controlDeps)

	// Format text output
	outputText := t.formatText(p.Target, filteredDeps, output.Summary)

	span.SetAttributes(
		attribute.Int("dependencies_found", len(filteredDeps)),
		attribute.String("trace_action", traceStep.Action),
	)

	t.logger.Debug("find_control_dependencies completed",
		slog.String("tool", "find_control_dependencies"),
		slog.String("target", p.Target),
		slog.Int("dependencies", len(filteredDeps)),
	)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &traceStep,
		Duration:   time.Since(start),
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findControlDependenciesTool) parseParams(params map[string]any) (FindControlDependenciesParams, error) {
	p := FindControlDependenciesParams{
		Depth: 5,
	}

	// Extract target (required)
	if targetRaw, ok := params["target"]; ok {
		if target, ok := parseStringParam(targetRaw); ok && target != "" {
			p.Target = target
		}
	}
	if err := ValidateSymbolName(p.Target, "target", "'handleRequest', 'Serve', 'Execute'"); err != nil {
		return p, err
	}

	// Extract depth (optional)
	if depthRaw, ok := params["depth"]; ok {
		if depth, ok := parseIntParam(depthRaw); ok {
			if depth < 1 {
				t.logger.Debug("depth below minimum, clamping to 1",
					slog.String("tool", "find_control_dependencies"),
					slog.Int("requested", depth),
				)
				depth = 1
			} else if depth > 10 {
				t.logger.Debug("depth above maximum, clamping to 10",
					slog.String("tool", "find_control_dependencies"),
					slog.Int("requested", depth),
				)
				depth = 10
			}
			p.Depth = depth
		}
	}

	return p, nil
}

// resolveTarget finds the symbol ID for a target name.
// Falls back to using the name as ID if index is unavailable.
func (t *findControlDependenciesTool) resolveTarget(ctx context.Context, name string) (string, error) {
	if t.index == nil {
		// Fall back to using name as ID when index is unavailable
		t.logger.Debug("symbol index unavailable, using name as ID fallback",
			slog.String("tool", "find_control_dependencies"),
			slog.String("name", name),
		)
		return name, nil
	}

	// P1: Use fuzzy search helper (Feb 14, 2026)
	// This enables partial matching - e.g., "Process" finds "getDatesToProcess"
	symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, t.index, name, t.logger)
	if err != nil {
		return "", err
	}

	// Filter by kind - only accept functions and methods
	if symbol.Kind != ast.SymbolKindFunction && symbol.Kind != ast.SymbolKindMethod {
		return "", fmt.Errorf("symbol '%s' is not a function or method (kind: %s)", symbol.Name, symbol.Kind)
	}

	if fuzzy {
		t.logger.Info("P1: Using fuzzy match for target",
			slog.String("tool", "find_control_dependencies"),
			slog.String("query", name),
			slog.String("matched", symbol.Name),
		)
	}

	return symbol.ID, nil
}

// approximateControlDependence creates an approximation using dominators.
func (t *findControlDependenciesTool) approximateControlDependence(
	ctx context.Context,
	domTree *graph.DominatorTree,
	targetID string,
) (*graph.ControlDependence, crs.TraceStep) {
	start := time.Now()

	// Create a simple control dependence approximation
	// where nodes are control-dependent on their dominators
	deps := make(map[string][]string)
	dependents := make(map[string][]string)

	// Get all dominators of target as approximate control dependencies
	doms := domTree.DominatorsOf(targetID)
	if len(doms) > 0 {
		deps[targetID] = doms
		for _, dom := range doms {
			dependents[dom] = append(dependents[dom], targetID)
		}
	}

	traceStep := crs.TraceStep{
		Action:   "analytics_control_dependence_approx",
		Tool:     "ComputeControlDependence",
		Duration: time.Since(start),
		Metadata: map[string]string{
			"target":       targetID,
			"dependencies": fmt.Sprintf("%d", len(doms)),
			"approximate":  "true",
		},
	}

	return &graph.ControlDependence{
		Dependencies: deps,
		Dependents:   dependents,
		EdgeCount:    len(doms),
		NodeCount:    len(deps),
	}, traceStep
}

// filterByDepth limits dependencies to a maximum depth.
func (t *findControlDependenciesTool) filterByDepth(
	deps []string,
	maxDepth int,
) []string {
	if len(deps) <= maxDepth {
		return deps
	}
	return deps[:maxDepth]
}

// buildOutput creates the typed output struct.
func (t *findControlDependenciesTool) buildOutput(
	target, targetID string,
	deps []string,
	controlDeps *graph.ControlDependence,
) FindControlDependenciesOutput {
	controllers := make([]ControllerInfo, 0, len(deps))

	for i, depID := range deps {
		info := ControllerInfo{
			ID:    depID,
			Name:  extractNameFromNodeID(depID),
			Depth: i + 1,
		}

		// Add symbol info if available
		if t.index != nil {
			if sym, ok := t.index.GetByID(depID); ok && sym != nil {
				info.Name = sym.Name
				info.File = sym.FilePath
				info.Line = sym.StartLine
				info.Package = sym.Package
			}
		}

		// Count dependents for this controller
		if controlDeps != nil {
			info.DependentsCount = len(controlDeps.GetDependents(depID))
		}

		controllers = append(controllers, info)
	}

	maxDepth := 0
	if len(controllers) > 0 {
		maxDepth = controllers[len(controllers)-1].Depth
	}

	summary := ControlDependencySummary{
		TotalDependencies: len(deps),
		MaxDepth:          maxDepth,
		NodesAnalyzed:     controlDeps.NodeCount,
	}

	// Count top controllers (those with many dependents)
	for _, c := range controllers {
		if c.DependentsCount > 5 {
			summary.TopControllers++
		}
	}

	return FindControlDependenciesOutput{
		Target:          target,
		TargetID:        targetID,
		Dependencies:    controllers,
		DependencyCount: len(controllers),
		Summary:         summary,
	}
}

// formatText creates a human-readable text summary.
func (t *findControlDependenciesTool) formatText(
	target string,
	deps []string,
	summary ControlDependencySummary,
) string {
	// Pre-size buffer: ~150 base + 80/dependency
	estimatedSize := 150 + len(deps)*80
	var sb strings.Builder
	sb.Grow(estimatedSize)

	if len(deps) == 0 {
		sb.WriteString(fmt.Sprintf("No control dependencies found for '%s'.\n", target))
		sb.WriteString("This function may execute unconditionally from all paths.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Control Dependencies for '%s':\n\n", target))
	sb.WriteString(fmt.Sprintf("Found %d control dependencies (max depth: %d)\n\n",
		summary.TotalDependencies, summary.MaxDepth))

	sb.WriteString("Controllers (nodes that control execution):\n")
	for i, depID := range deps {
		name := extractNameFromNodeID(depID)
		sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, name))
		sb.WriteString(fmt.Sprintf("     ID: %s\n", depID))
	}

	if summary.TopControllers > 0 {
		sb.WriteString(fmt.Sprintf("\n%d controllers have significant control (>5 dependents)\n",
			summary.TopControllers))
	}

	return sb.String()
}
