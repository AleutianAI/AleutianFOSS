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
// find_dominators Tool (GR-17) - Typed Implementation
// =============================================================================

var findDominatorsTracer = otel.Tracer("tools.find_dominators")

// FindDominatorsParams contains the validated input parameters.
type FindDominatorsParams struct {
	// Target is the target function name or ID to find dominators for.
	// Required.
	Target string

	// Entry is the entry point for dominator analysis.
	// If empty, auto-detects main/init.
	Entry string

	// ShowTree indicates whether to show full dominator subtree of target.
	// Default: false
	ShowTree bool
}

// ToolName returns the tool name for TypedParams interface.
func (p FindDominatorsParams) ToolName() string { return "find_dominators" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindDominatorsParams) ToMap() map[string]any {
	m := map[string]any{
		"target":    p.Target,
		"show_tree": p.ShowTree,
	}
	if p.Entry != "" {
		m["entry"] = p.Entry
	}
	return m
}

// FindDominatorsOutput contains the structured result.
type FindDominatorsOutput struct {
	// Target is the resolved target node ID.
	Target string `json:"target"`

	// TargetName is the function name of the target.
	TargetName string `json:"target_name"`

	// Entry is the entry point used for analysis.
	Entry string `json:"entry"`

	// EntryAutoDetected indicates if the entry was auto-detected.
	EntryAutoDetected bool `json:"entry_auto_detected"`

	// Dominators is the list of functions that dominate the target.
	// Ordered from entry to immediate dominator (shallow to deep).
	Dominators []DominatorInfo `json:"dominators"`

	// Depth is the depth of the target in the dominator tree.
	Depth int `json:"depth"`

	// ImmediateDominator is the immediate dominator of the target.
	ImmediateDominator string `json:"immediate_dominator"`

	// Subtree contains nodes dominated by the target (when show_tree=true).
	Subtree []string `json:"subtree,omitempty"`

	// Explanation is a human-readable summary.
	Explanation string `json:"explanation"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// DominatorInfo holds information about a single dominator.
type DominatorInfo struct {
	// ID is the full node ID.
	ID string `json:"id"`

	// Name is the function name.
	Name string `json:"name"`

	// File is the source file path.
	File string `json:"file"`

	// Line is the line number in the source file.
	Line int `json:"line"`

	// Depth is the depth in the dominator tree (0 = entry).
	Depth int `json:"depth"`
}

// findDominatorsTool implements the find_dominators tool.
type findDominatorsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindDominatorsTool creates the find_dominators tool.
//
// Description:
//
//	Creates a tool that finds all functions that must be called to reach a
//	target function. Uses dominator tree analysis to identify mandatory
//	call sequences from entry points to the target.
//
// Inputs:
//   - analytics: Graph analytics instance for dominator computation. Must not be nil.
//   - idx: Symbol index for name resolution. May be nil.
//
// Outputs:
//   - Tool: The find_dominators tool. Never nil if analytics is valid.
//
// Limitations:
//   - Requires a designated entry point (auto-detected or specified)
//   - May return empty result if target is unreachable from entry
//
// Assumptions:
//   - Graph is frozen and indexed before tool creation
//   - Analytics wraps a HierarchicalGraph
func NewFindDominatorsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findDominatorsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findDominatorsTool) Name() string {
	return "find_dominators"
}

func (t *findDominatorsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findDominatorsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_dominators",
		Description: "Find all functions that must be called to reach a target function. " +
			"Shows the mandatory call sequence from entry points to the target. " +
			"Use this to understand dependencies and required initialization.",
		Parameters: map[string]ParamDef{
			"target": {
				Type:        ParamTypeString,
				Description: "Target function name or ID",
				Required:    true,
			},
			"entry": {
				Type:        ParamTypeString,
				Description: "Entry point (default: auto-detect main/init)",
				Required:    false,
			},
			"show_tree": {
				Type:        ParamTypeBool,
				Description: "Show full dominator subtree of target (default: false)",
				Required:    false,
				Default:     false,
			},
		},
		Category:    CategoryExploration,
		Priority:    85,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"dominators", "must call", "required before",
				"mandatory", "prerequisites", "must go through",
				"instrumentation point", "what must be called",
			},
			UseWhen: "User asks what functions MUST be called before reaching a target, " +
				"or asks about mandatory prerequisites and dependencies.",
			AvoidWhen: "User asks about all possible callers. Use find_callers for that. " +
				"Use find_critical_path if asking about the exact mandatory sequence.",
		},
	}
}

// Execute runs the find_dominators tool.
func (t *findDominatorsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	// Parse and validate parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Nil analytics check
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "analytics not initialized",
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
func (t *findDominatorsTool) executeOnce(ctx context.Context, p *FindDominatorsParams) (*Result, error) {
	start := time.Now()

	// Create OTel span
	ctx, span := findDominatorsTracer.Start(ctx, "findDominatorsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_dominators"),
			attribute.String("target", p.Target),
			attribute.String("entry", p.Entry),
			attribute.Bool("show_tree", p.ShowTree),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Determine entry point
	entryAutoDetected := false
	entry := p.Entry
	if entry == "" {
		var detectErr error
		entry, detectErr = DetectEntryPoint(ctx, t.index, t.analytics)
		if detectErr != nil {
			span.RecordError(detectErr)
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("failed to detect entry point: %v", detectErr),
			}, nil
		}
		entryAutoDetected = true
	}

	span.SetAttributes(attribute.String("resolved_entry", entry))
	span.SetAttributes(attribute.Bool("entry_auto_detected", entryAutoDetected))

	// Resolve target
	targetID, err := t.resolveTarget(ctx, p.Target)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("target resolution failed: %v", err),
		}, nil
	}

	span.SetAttributes(attribute.String("resolved_target", targetID))

	// Compute dominator tree
	domTree, traceStep := t.analytics.DominatorsWithCRS(ctx, entry)
	if domTree == nil {
		return &Result{
			Success: false,
			Error:   "failed to compute dominator tree",
		}, nil
	}

	// Get dominators of target
	dominatorIDs := domTree.DominatorsOf(targetID)

	// Check if target is reachable (has dominators)
	if len(dominatorIDs) == 0 {
		output := FindDominatorsOutput{
			Target:            targetID,
			TargetName:        extractNameFromNodeID(targetID),
			Entry:             entry,
			EntryAutoDetected: entryAutoDetected,
			Dominators:        []DominatorInfo{},
			Depth:             0,
			Explanation:       fmt.Sprintf("%s is not reachable from entry point %s", extractNameFromNodeID(targetID), extractNameFromNodeID(entry)),
			Message:           "Target not reachable from entry point",
		}
		outputText := t.formatText(output)
		duration := time.Since(start)

		return &Result{
			Success:    true,
			Output:     output,
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   duration,
		}, nil
	}

	// Build dominator info list (reverse order: entry to immediate dominator)
	dominators := make([]DominatorInfo, 0, len(dominatorIDs))
	for i := len(dominatorIDs) - 1; i >= 0; i-- {
		nodeID := dominatorIDs[i]
		if nodeID == targetID {
			continue // Don't include target itself
		}
		dominators = append(dominators, DominatorInfo{
			ID:    nodeID,
			Name:  extractNameFromNodeID(nodeID),
			File:  extractFileFromNodeID(nodeID),
			Line:  extractLineFromNodeID(nodeID),
			Depth: domTree.Depth[nodeID],
		})
	}

	// Find immediate dominator
	immediateDom := ""
	if idom, ok := domTree.ImmediateDom[targetID]; ok && idom != targetID {
		immediateDom = idom
	}

	// Get subtree if requested
	var subtree []string
	if p.ShowTree {
		subtree = t.getDominatedNodes(domTree, targetID)
	}

	// Build explanation
	explanation := t.buildExplanation(dominators, extractNameFromNodeID(targetID))

	// Build output
	output := FindDominatorsOutput{
		Target:             targetID,
		TargetName:         extractNameFromNodeID(targetID),
		Entry:              entry,
		EntryAutoDetected:  entryAutoDetected,
		Dominators:         dominators,
		Depth:              domTree.Depth[targetID],
		ImmediateDominator: immediateDom,
		Subtree:            subtree,
		Explanation:        explanation,
	}

	duration := time.Since(start)
	outputText := t.formatText(output)

	// Set span attributes for result
	span.SetAttributes(
		attribute.Int("dominators_count", len(dominators)),
		attribute.Int("depth", domTree.Depth[targetID]),
		attribute.String("immediate_dominator", immediateDom),
	)

	// Create tool-level trace step
	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_dominators").
		WithTarget(targetID).
		WithTool("find_dominators").
		WithDuration(duration).
		WithMetadata("entry", entry).
		WithMetadata("entry_auto_detected", fmt.Sprintf("%v", entryAutoDetected)).
		WithMetadata("depth", fmt.Sprintf("%d", domTree.Depth[targetID])).
		WithMetadata("dominators_count", fmt.Sprintf("%d", len(dominators))).
		WithMetadata("show_tree", fmt.Sprintf("%v", p.ShowTree)).
		Build()

	t.logger.Debug("find_dominators completed",
		slog.String("tool", "find_dominators"),
		slog.String("target", targetID),
		slog.Int("dominators_count", len(dominators)),
		slog.Int("depth", domTree.Depth[targetID]),
	)

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &toolStep,
		Duration:   duration,
	}, nil
}

// parseParams validates and extracts parameters.
func (t *findDominatorsTool) parseParams(params map[string]any) (*FindDominatorsParams, error) {
	p := &FindDominatorsParams{
		ShowTree: false, // default
	}

	// Extract target (required)
	targetRaw, ok := params["target"]
	if !ok {
		return nil, fmt.Errorf("missing required parameter 'target'")
	}
	target, ok := parseStringParam(targetRaw)
	if !ok || target == "" {
		return nil, fmt.Errorf("target must be a non-empty string")
	}
	p.Target = target

	// Extract entry (optional)
	if entryRaw, ok := params["entry"]; ok {
		if entry, ok := parseStringParam(entryRaw); ok {
			p.Entry = entry
		}
	}

	// Extract show_tree (optional)
	if showTreeRaw, ok := params["show_tree"]; ok {
		if showTree, ok := parseBoolParam(showTreeRaw); ok {
			p.ShowTree = showTree
		}
	}

	return p, nil
}

// resolveTarget finds the symbol ID for a target name.
func (t *findDominatorsTool) resolveTarget(ctx context.Context, name string) (string, error) {
	if t.index == nil {
		// Fall back to using name as ID when index is unavailable
		t.logger.Debug("symbol index unavailable, using name as ID fallback",
			slog.String("tool", "find_dominators"),
			slog.String("name", name),
		)
		return name, nil
	}

	// First check if name is already a full ID
	if strings.Contains(name, ":") {
		return name, nil
	}

	// P1: Use fuzzy search helper (Feb 14, 2026)
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
			slog.String("tool", "find_dominators"),
			slog.String("query", name),
			slog.String("matched", symbol.Name),
		)
	}

	return symbol.ID, nil
}

// getDominatedNodes returns all nodes dominated by the given node.
func (t *findDominatorsTool) getDominatedNodes(domTree *graph.DominatorTree, nodeID string) []string {
	result := make([]string, 0)

	// Build children map if needed
	children := domTree.Children
	if children == nil {
		return result
	}

	// BFS to collect all dominated nodes
	queue := []string{nodeID}
	visited := make(map[string]bool)

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current] {
			continue
		}
		visited[current] = true

		if current != nodeID { // Don't include the node itself
			result = append(result, current)
		}

		if childList, ok := children[current]; ok {
			queue = append(queue, childList...)
		}
	}

	return result
}

// buildExplanation creates a human-readable explanation.
func (t *findDominatorsTool) buildExplanation(dominators []DominatorInfo, targetName string) string {
	if len(dominators) == 0 {
		return fmt.Sprintf("%s is an entry point or unreachable", targetName)
	}

	names := make([]string, len(dominators))
	for i, d := range dominators {
		names[i] = d.Name
	}

	return fmt.Sprintf("All paths to %s MUST go through: %s", targetName, strings.Join(names, " → "))
}

// formatText creates a human-readable text output.
func (t *findDominatorsTool) formatText(output FindDominatorsOutput) string {
	// Estimate size: header + dominators + explanation
	estimatedSize := 300 + len(output.Dominators)*100 + len(output.Subtree)*50

	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString(fmt.Sprintf("Dominators of %s\n", output.TargetName))
	sb.WriteString(strings.Repeat("=", 40))
	sb.WriteString("\n\n")

	// Entry info
	entryType := "specified"
	if output.EntryAutoDetected {
		entryType = "auto-detected"
	}
	sb.WriteString(fmt.Sprintf("Entry point (%s): %s\n", entryType, extractNameFromNodeID(output.Entry)))
	sb.WriteString(fmt.Sprintf("Target depth: %d\n\n", output.Depth))

	// Dominators list
	if len(output.Dominators) == 0 {
		sb.WriteString("No dominators found.\n")
		if output.Message != "" {
			sb.WriteString(fmt.Sprintf("Note: %s\n", output.Message))
		}
	} else {
		sb.WriteString("Mandatory call sequence:\n")
		for i, dom := range output.Dominators {
			prefix := "  "
			if i == len(output.Dominators)-1 {
				prefix = "→ " // Mark immediate dominator
			}
			sb.WriteString(fmt.Sprintf("%s%d. %s (%s:%d)\n", prefix, i+1, dom.Name, dom.File, dom.Line))
		}
		sb.WriteString(fmt.Sprintf("→ %s (target)\n", output.TargetName))
	}

	// Immediate dominator
	if output.ImmediateDominator != "" {
		sb.WriteString(fmt.Sprintf("\nImmediate dominator: %s\n", extractNameFromNodeID(output.ImmediateDominator)))
	}

	// Subtree (if shown)
	if len(output.Subtree) > 0 {
		sb.WriteString(fmt.Sprintf("\nDominated subtree (%d nodes):\n", len(output.Subtree)))
		maxShow := 20
		for i, nodeID := range output.Subtree {
			if i >= maxShow {
				sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(output.Subtree)-maxShow))
				break
			}
			sb.WriteString(fmt.Sprintf("  - %s\n", extractNameFromNodeID(nodeID)))
		}
	}

	// Explanation
	sb.WriteString(fmt.Sprintf("\n%s\n", output.Explanation))

	return sb.String()
}
