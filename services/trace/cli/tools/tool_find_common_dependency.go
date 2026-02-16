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

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// =============================================================================
// find_common_dependency Tool (GR-17f) - Typed Implementation
// =============================================================================

var findCommonDependencyTracer = otel.Tracer("tools.find_common_dependency")

// FindCommonDependencyParams contains the validated input parameters.
type FindCommonDependencyParams struct {
	// Targets is the list of function names to find common dependency for.
	// Must contain at least 2 function names.
	Targets []string

	// Entry is the optional entry point for dominator analysis.
	// If empty, auto-detects main/init functions.
	Entry string
}

// FindCommonDependencyOutput contains the structured result.
type FindCommonDependencyOutput struct {
	// LCD contains information about the lowest common dominator.
	LCD LCDInfo `json:"lcd"`

	// Targets is the list of target function names that were analyzed.
	Targets []string `json:"targets"`

	// TargetsResolved is the number of targets that were successfully resolved.
	TargetsResolved int `json:"targets_resolved"`

	// Entry is the entry point name used for dominator analysis.
	Entry string `json:"entry"`

	// EntryID is the full node ID of the entry point.
	EntryID string `json:"entry_id"`

	// PathsToTargets maps each target name to its dominator chain.
	// Each chain is a list of function names from the target up to the LCD.
	PathsToTargets map[string][]string `json:"paths_to_targets"`
}

// LCDInfo contains information about the lowest common dominator.
type LCDInfo struct {
	// ID is the full node ID of the LCD.
	ID string `json:"id"`

	// Name is the function name of the LCD.
	Name string `json:"name"`

	// Depth is the depth of the LCD in the dominator tree.
	Depth int `json:"depth,omitempty"`
}

// ResolvedTarget holds a resolved target with its ID and name.
type ResolvedTarget struct {
	Name string
	ID   string
}

// findCommonDependencyTool finds the shared mandatory dependency between functions.
type findCommonDependencyTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindCommonDependencyTool creates a new find_common_dependency tool instance.
//
// Description:
//
//	Creates a tool that finds the lowest common dominator (LCD) of multiple
//	target functions. The LCD is the deepest function that must be called
//	to reach all specified targets - their shared mandatory dependency.
//
// Inputs:
//
//   - analytics: Graph analytics engine. Must not be nil for meaningful results.
//   - idx: Symbol index for name resolution. Must not be nil for meaningful results.
//
// Outputs:
//
//   - Tool: The tool implementation.
//
// Thread Safety: Safe for concurrent use after construction.
func NewFindCommonDependencyTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findCommonDependencyTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

// Name returns the tool name.
func (t *findCommonDependencyTool) Name() string {
	return "find_common_dependency"
}

// Category returns the tool category.
func (t *findCommonDependencyTool) Category() ToolCategory {
	return CategoryExploration
}

// Definition returns the tool definition for LLM consumption.
func (t *findCommonDependencyTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_common_dependency",
		Description: "Find the shared mandatory dependency between functions. " +
			"Returns the lowest common dominator - the deepest function that " +
			"must be called to reach all specified targets. Use this to understand " +
			"what initialization or setup is shared between multiple code paths.",
		Parameters: map[string]ParamDef{
			"targets": {
				Type:        ParamTypeArray,
				Description: "List of function names to find common dependency for (at least 2 required)",
				Required:    true,
			},
			"entry": {
				Type:        ParamTypeString,
				Description: "Entry point for dominator analysis (default: auto-detect main/init)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    81,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"common dependency", "shared dependency", "common dominator",
				"lowest common", "LCD", "shared prerequisite",
				"what do these have in common", "common ancestor",
			},
			UseWhen: "User asks about what dependency is shared between multiple functions, " +
				"or wants to find the common initialization/setup for several code paths.",
			AvoidWhen: "User asks about a single function's dependencies. " +
				"Use find_dominators for that.",
		},
	}
}

// Execute runs the find_common_dependency tool.
func (t *findCommonDependencyTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()
	logger := telemetry.LoggerWithTrace(ctx, t.logger)

	// Start OTel span
	ctx, span := findCommonDependencyTracer.Start(ctx, "findCommonDependencyTool.Execute")
	defer span.End()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		span.AddEvent("invalid_params", trace.WithAttributes(
			attribute.String("error", err.Error()),
		))
		logger.Debug("find_common_dependency: invalid parameters", slog.String("error", err.Error()))
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Nil analytics check
	if t.analytics == nil {
		span.AddEvent("nil_analytics")
		logger.Debug("find_common_dependency: analytics not initialized")
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Resolve targets to node IDs
	resolvedTargets, unresolvedTargets := t.resolveTargets(ctx, p.Targets)

	// Phase 11A: Treat "not found" as successful informational result (Feb 14, 2026)
	if len(unresolvedTargets) > 0 {
		span.AddEvent("unresolved_targets", trace.WithAttributes(
			attribute.StringSlice("unresolved", unresolvedTargets),
			attribute.StringSlice("resolved", getResolvedNames(resolvedTargets)),
		))
		logger.Info("find_common_dependency: some targets not found - returning informational result",
			slog.Any("unresolved", unresolvedTargets),
			slog.Any("resolved", getResolvedNames(resolvedTargets)),
		)

		// Build informational output text
		var sb strings.Builder
		sb.WriteString("## Symbol Verification Result\n\n")
		sb.WriteString(fmt.Sprintf("Searched for: %s\n\n", strings.Join(p.Targets, ", ")))

		if len(resolvedTargets) > 0 {
			sb.WriteString(fmt.Sprintf("✓ Found: %s\n", strings.Join(getResolvedNames(resolvedTargets), ", ")))
		}
		sb.WriteString(fmt.Sprintf("✗ Not found: %s\n\n", strings.Join(unresolvedTargets, ", ")))

		sb.WriteString("The graph has been fully indexed - this is the definitive answer.\n")
		sb.WriteString("**Do NOT use Grep to search further** - the graph already analyzed all source files.\n\n")

		if len(resolvedTargets) > 0 {
			sb.WriteString("Since not all targets exist, common dependency analysis cannot be performed.\n")
		} else {
			sb.WriteString("None of the requested symbols exist in the codebase.\n")
		}

		outputText := sb.String()
		return &Result{
			Success:    true, // Success because we definitively verified non-existence
			Output:     outputText,
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
		}, nil
	}

	if len(resolvedTargets) < 2 {
		span.AddEvent("insufficient_resolved_targets")
		return &Result{
			Success: false,
			Error:   "need at least 2 resolved targets for common dependency analysis",
		}, nil
	}

	// Auto-detect entry point if not specified
	entryNodeID, err := t.resolveEntryPoint(ctx, p.Entry)
	if err != nil {
		span.AddEvent("entry_resolution_error", trace.WithAttributes(
			attribute.String("error", err.Error()),
		))
		logger.Debug("find_common_dependency: entry resolution failed",
			slog.String("error", err.Error()),
		)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to resolve entry point: %s", err.Error()),
		}, nil
	}

	span.AddEvent("analysis_params", trace.WithAttributes(
		attribute.Int("target_count", len(resolvedTargets)),
		attribute.String("entry", entryNodeID),
	))

	logger.Debug("find_common_dependency: starting analysis",
		slog.Int("target_count", len(resolvedTargets)),
		slog.String("entry", entryNodeID),
	)

	// Compute dominator tree
	domTree, domStep := t.analytics.DominatorsWithCRS(ctx, entryNodeID)

	// Check if dominator computation failed
	if domTree == nil || len(domTree.ImmediateDom) == 0 {
		span.AddEvent("dominator_computation_failed")
		logger.Debug("find_common_dependency: dominator tree computation failed")
		return &Result{
			Success:   false,
			Error:     "failed to compute dominator tree (empty result)",
			TraceStep: &domStep,
		}, nil
	}

	// Get target node IDs
	targetNodeIDs := make([]string, 0, len(resolvedTargets))
	for _, rt := range resolvedTargets {
		targetNodeIDs = append(targetNodeIDs, rt.ID)
	}

	// Find LCD using CRS-enabled method
	lcd, lcdStep := t.analytics.LCDWithCRS(ctx, domTree, targetNodeIDs)

	// Build trace step
	duration := time.Since(start)
	traceStep := t.buildTraceStep(duration, entryNodeID, len(resolvedTargets), lcd, domStep, lcdStep)

	// If LCD is empty, the targets might not be reachable from entry
	if lcd == "" {
		span.AddEvent("no_common_dependency")
		logger.Debug("find_common_dependency: no common dependency found")
		return &Result{
			Success:   false,
			Error:     "no common dependency found (targets may not be reachable from entry)",
			TraceStep: &traceStep,
		}, nil
	}

	// Build typed output
	output := t.buildOutput(resolvedTargets, entryNodeID, lcd, domTree)

	// Format text output
	outputText := t.formatText(output)

	span.AddEvent("analysis_complete", trace.WithAttributes(
		attribute.String("lcd", lcd),
		attribute.Int("duration_ms", int(duration.Milliseconds())),
	))

	logger.Info("find_common_dependency: analysis complete",
		slog.String("lcd", output.LCD.Name),
		slog.Int("targets", len(resolvedTargets)),
		slog.Duration("duration", duration),
	)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &traceStep,
		Duration:   duration,
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findCommonDependencyTool) parseParams(params map[string]any) (FindCommonDependencyParams, error) {
	var p FindCommonDependencyParams
	var targets []string

	// Strategy 1: Try new format (targets array)
	if targetsRaw, ok := params["targets"]; ok {
		parsedTargets, ok := parseStringArray(targetsRaw)
		if ok {
			targets = parsedTargets
		}
	}

	// Strategy 2: Fallback to old format (function_a, function_b) for backward compatibility
	// This handles LLM-generated calls that might use the old parameter names
	if len(targets) == 0 {
		var legacyTargets []string
		if funcA, okA := params["function_a"]; okA {
			if str, ok := parseStringParam(funcA); ok && str != "" {
				legacyTargets = append(legacyTargets, str)
			}
		}
		if funcB, okB := params["function_b"]; okB {
			if str, ok := parseStringParam(funcB); ok && str != "" {
				legacyTargets = append(legacyTargets, str)
			}
		}

		if len(legacyTargets) >= 2 {
			targets = legacyTargets
			t.logger.Debug("find_common_dependency: using legacy parameter format",
				slog.String("function_a", legacyTargets[0]),
				slog.String("function_b", legacyTargets[1]))
		}
	}

	// Validate we have at least 2 targets
	if len(targets) < 2 {
		return p, fmt.Errorf("targets must be an array of at least 2 function names (got %d). Use 'targets' array or 'function_a'/'function_b' parameters", len(targets))
	}

	p.Targets = targets

	// Extract entry (optional)
	if entryRaw, ok := params["entry"]; ok {
		if entry, ok := parseStringParam(entryRaw); ok {
			p.Entry = entry
		}
	}

	return p, nil
}

// resolveTargets resolves target names to node IDs.
func (t *findCommonDependencyTool) resolveTargets(
	ctx context.Context,
	targets []string,
) ([]ResolvedTarget, []string) {
	resolved := make([]ResolvedTarget, 0, len(targets))
	unresolved := make([]string, 0)

	for _, target := range targets {
		// P1: Use fuzzy search helper (Feb 14, 2026)
		if t.index != nil {
			symbol, fuzzy, err := ResolveFunctionWithFuzzy(ctx, t.index, target, t.logger)
			if err == nil {
				if fuzzy {
					t.logger.Info("P1: Using fuzzy match for target",
						slog.String("tool", "find_common_dependency"),
						slog.String("query", target),
						slog.String("matched", symbol.Name),
					)
				}
				resolved = append(resolved, ResolvedTarget{
					Name: symbol.Name,
					ID:   symbol.ID,
				})
				continue
			}
		}

		// If not found, add to unresolved
		unresolved = append(unresolved, target)
	}

	return resolved, unresolved
}

// getResolvedNames extracts the names from resolved targets.
func getResolvedNames(resolved []ResolvedTarget) []string {
	names := make([]string, 0, len(resolved))
	for _, r := range resolved {
		names = append(names, r.Name)
	}
	return names
}

// resolveEntryPoint resolves the entry point to a node ID.
func (t *findCommonDependencyTool) resolveEntryPoint(ctx context.Context, entry string) (string, error) {
	// If entry specified, try to resolve it
	if entry != "" {
		if t.index != nil {
			results, err := t.index.Search(ctx, entry, 1)
			if err == nil && len(results) > 0 {
				return results[0].ID, nil
			}
		}
		return "", fmt.Errorf("entry point '%s' not found", entry)
	}

	// Auto-detect entry points
	entryNodes := t.analytics.DetectEntryNodes(ctx)
	if len(entryNodes) == 0 {
		return "", fmt.Errorf("no entry points found (no nodes without incoming edges)")
	}

	// Use first detected entry (main/init prioritized by DetectEntryNodes)
	return entryNodes[0], nil
}

// buildTraceStep creates the combined CRS trace step.
func (t *findCommonDependencyTool) buildTraceStep(
	duration time.Duration,
	entry string,
	targetCount int,
	lcd string,
	domStep, lcdStep crs.TraceStep,
) crs.TraceStep {
	return crs.NewTraceStepBuilder().
		WithAction("tool_find_common_dependency").
		WithTarget(entry).
		WithTool("find_common_dependency").
		WithDuration(duration).
		WithMetadata("entry", entry).
		WithMetadata("target_count", fmt.Sprintf("%d", targetCount)).
		WithMetadata("lcd", lcd).
		WithMetadata("dom_iterations", domStep.Metadata["iterations"]).
		WithMetadata("dom_converged", domStep.Metadata["converged"]).
		Build()
}

// buildOutput creates the typed output struct.
func (t *findCommonDependencyTool) buildOutput(
	targets []ResolvedTarget,
	entry string,
	lcd string,
	domTree *graph.DominatorTree,
) FindCommonDependencyOutput {
	// Build LCD info
	lcdInfo := LCDInfo{
		ID:   lcd,
		Name: extractNameFromNodeID(lcd),
	}

	// Get depth if available
	if depth, ok := domTree.Depth[lcd]; ok {
		lcdInfo.Depth = depth
	}

	// Build target names list
	targetNames := make([]string, 0, len(targets))
	for _, rt := range targets {
		targetNames = append(targetNames, rt.Name)
	}

	// Build paths from each target to LCD (dominator chain)
	paths := make(map[string][]string)
	for _, rt := range targets {
		domChain := domTree.DominatorsOf(rt.ID)
		pathNames := make([]string, 0, len(domChain))
		for _, nodeID := range domChain {
			pathNames = append(pathNames, extractNameFromNodeID(nodeID))
		}
		paths[rt.Name] = pathNames
	}

	return FindCommonDependencyOutput{
		LCD:             lcdInfo,
		Targets:         targetNames,
		TargetsResolved: len(targets),
		Entry:           extractNameFromNodeID(entry),
		EntryID:         entry,
		PathsToTargets:  paths,
	}
}

// formatText creates a human-readable text summary.
func (t *findCommonDependencyTool) formatText(output FindCommonDependencyOutput) string {
	// Pre-size buffer: ~200 base + 100/target
	estimatedSize := 200 + len(output.Targets)*100
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString(fmt.Sprintf("Common Dependency Analysis\n"))
	sb.WriteString(strings.Repeat("=", 40))
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("Targets analyzed: %s\n", strings.Join(output.Targets, ", ")))
	sb.WriteString(fmt.Sprintf("Entry point: %s\n\n", output.Entry))

	sb.WriteString(fmt.Sprintf("Lowest Common Dominator (LCD): %s\n", output.LCD.Name))
	sb.WriteString(fmt.Sprintf("  ID: %s\n", output.LCD.ID))
	if output.LCD.Depth > 0 {
		sb.WriteString(fmt.Sprintf("  Depth: %d\n", output.LCD.Depth))
	}
	sb.WriteString("\n")

	sb.WriteString("All paths to these targets MUST go through this function.\n")

	if len(output.PathsToTargets) > 0 {
		sb.WriteString("\nDominator chains to LCD:\n")
		for target, path := range output.PathsToTargets {
			if len(path) > 0 {
				sb.WriteString(fmt.Sprintf("  %s: %s\n", target, strings.Join(path, " → ")))
			}
		}
	}

	return sb.String()
}
