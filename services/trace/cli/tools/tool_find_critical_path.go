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
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_critical_path Tool (GR-18a) - Typed Implementation
// =============================================================================

var findCriticalPathTracer = otel.Tracer("tools.find_critical_path")

// FindCriticalPathParams contains the validated input parameters.
type FindCriticalPathParams struct {
	// Target is the target function to reach.
	// Required.
	Target string

	// Entry is the entry point for analysis.
	// Optional - will auto-detect if not provided.
	Entry string
}

// FindCriticalPathOutput contains the structured result.
type FindCriticalPathOutput struct {
	// Target is the target function.
	Target string `json:"target"`

	// Entry is the entry point used.
	Entry string `json:"entry"`

	// CriticalPath is the mandatory call sequence from entry to target.
	CriticalPath []CriticalPathNode `json:"critical_path"`

	// PathLength is the number of nodes in the critical path.
	PathLength int `json:"path_length"`

	// Explanation is a human-readable description.
	Explanation string `json:"explanation"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// CriticalPathNode represents a single node in the critical path.
type CriticalPathNode struct {
	// ID is the full node identifier.
	ID string `json:"id"`

	// Name is the function name extracted from the ID.
	Name string `json:"name"`

	// Depth is the distance from the entry point.
	Depth int `json:"depth"`

	// IsMandatory is always true for critical path nodes.
	IsMandatory bool `json:"is_mandatory"`

	// Reason explains why this node is in the path.
	Reason string `json:"reason"`
}

// findCriticalPathTool finds the mandatory call sequence to reach a target.
type findCriticalPathTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindCriticalPathTool creates a new find_critical_path tool.
//
// Description:
//
//	Creates a tool for finding the mandatory call sequence (critical path)
//	to reach a target function using dominator tree analysis.
//
// Inputs:
//   - analytics: GraphAnalytics instance for dominator computation. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_critical_path tool.
//
// Thread Safety: Safe for concurrent use after construction.
//
// Limitations:
//   - Requires dominator tree computation which needs an entry point
//   - Only shows dominator-based critical path, not all possible paths
//   - Target must be reachable from entry point
func NewFindCriticalPathTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findCriticalPathTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findCriticalPathTool) Name() string {
	return "find_critical_path"
}

func (t *findCriticalPathTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findCriticalPathTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_critical_path",
		Description: "Find the mandatory call sequence to reach a target function. " +
			"Shows the exact functions that MUST be called, not just possible paths. " +
			"Uses dominator analysis to identify the critical path.",
		Parameters: map[string]ParamDef{
			"target": {
				Type:        ParamTypeString,
				Description: "Target function name or ID (required)",
				Required:    true,
			},
			"entry": {
				Type:        ParamTypeString,
				Description: "Entry point function (default: auto-detect main/init)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    86,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"critical path", "mandatory path", "must call",
				"required sequence", "essential calls", "cannot bypass",
				"mandatory functions", "critical sequence", "dominator path",
			},
			UseWhen: "User asks about the mandatory or essential call sequence " +
				"to reach a target, or what functions cannot be bypassed.",
			AvoidWhen: "User asks about any possible path (use find_path). " +
				"Use find_dominators if only asking about prerequisites without the path.",
		},
	}
}

// Execute runs the find_critical_path tool.
func (t *findCriticalPathTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	// Parse and validate parameters
	p, err := t.parseParams(params)
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
func (t *findCriticalPathTool) executeOnce(ctx context.Context, p FindCriticalPathParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := findCriticalPathTracer.Start(ctx, "findCriticalPathTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_critical_path"),
			attribute.String("target", p.Target),
			attribute.String("entry", p.Entry),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Auto-detect entry point if not provided
	entry := p.Entry
	if entry == "" {
		detected, err := DetectEntryPoint(ctx, t.index, t.analytics)
		if err != nil {
			span.RecordError(err)
			return &Result{
				Success: false,
				Error:   fmt.Sprintf("failed to detect entry point: %v", err),
			}, nil
		}
		entry = detected
		// Note: DetectEntryPoint is a well-tested shared helper that returns
		// valid entry points. We trust its output without re-validating.
		t.logger.Debug("auto-detected entry point",
			slog.String("tool", "find_critical_path"),
			slog.String("entry", entry),
		)
	} else {
		// Validate manually provided entry point exists
		if t.index != nil {
			if _, exists := t.index.GetByID(entry); !exists {
				err := fmt.Errorf("entry point %q not found in graph", entry)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return &Result{
					Success: false,
					Error:   err.Error(),
				}, nil
			}
		}
	}

	span.SetAttributes(attribute.String("entry_resolved", entry))

	// Resolve target to full ID (supports both "D" and "pkg/d.go:10:D")
	targetID := p.Target
	if t.index != nil {
		resolvedTarget, err := t.resolveSymbol(p.Target)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return &Result{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
		targetID = resolvedTarget
	}
	span.SetAttributes(attribute.String("target_resolved", targetID))

	// Check context again before dominator computation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Compute dominator tree with CRS tracing
	domTree, traceStep := t.analytics.DominatorsWithCRS(ctx, entry)
	if domTree == nil {
		err := fmt.Errorf("failed to compute dominator tree from entry %q", entry)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Extract critical path (dominator chain)
	criticalPath, err := t.extractCriticalPath(entry, targetID, domTree)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Handle unreachable target (empty path)
	if len(criticalPath) == 0 {
		output := FindCriticalPathOutput{
			Target:       targetID,
			Entry:        entry,
			CriticalPath: []CriticalPathNode{},
			PathLength:   0,
			Explanation:  fmt.Sprintf("%s is not reachable from %s", extractNameFromNodeID(targetID), extractNameFromNodeID(entry)),
			Message:      "Target is not reachable from entry point",
		}
		outputText := fmt.Sprintf("%s is not reachable from %s.\n", extractNameFromNodeID(targetID), extractNameFromNodeID(entry))

		span.SetAttributes(
			attribute.Bool("reachable", false),
			attribute.Int("path_length", 0),
		)

		t.logger.Info("target unreachable",
			slog.String("tool", "find_critical_path"),
			slog.String("target", p.Target),
			slog.String("entry", entry),
		)

		return &Result{
			Success:    true, // Not an error - valid result
			Output:     output,
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Build output
	output := FindCriticalPathOutput{
		Target:       targetID,
		Entry:        entry,
		CriticalPath: criticalPath,
		PathLength:   len(criticalPath),
		Explanation:  t.buildExplanation(criticalPath, targetID),
	}

	// Format text output
	outputText := t.formatText(criticalPath, targetID, entry)

	span.SetAttributes(
		attribute.Bool("reachable", true),
		attribute.Int("path_length", len(criticalPath)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Log completion for production debugging
	t.logger.Debug("find_critical_path completed",
		slog.String("tool", "find_critical_path"),
		slog.String("target", targetID),
		slog.String("entry", entry),
		slog.Int("path_length", len(criticalPath)),
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
func (t *findCriticalPathTool) parseParams(params map[string]any) (FindCriticalPathParams, error) {
	p := FindCriticalPathParams{}

	// Extract target (required)
	if targetRaw, ok := params["target"]; ok {
		if target, ok := parseStringParam(targetRaw); ok {
			if target == "" {
				return p, fmt.Errorf("target parameter cannot be empty")
			}
			p.Target = target
		} else {
			return p, fmt.Errorf("target parameter must be a string")
		}
	} else {
		return p, fmt.Errorf("target parameter is required")
	}

	// Extract entry (optional)
	if entryRaw, ok := params["entry"]; ok {
		if entry, ok := parseStringParam(entryRaw); ok {
			p.Entry = entry // Can be empty, will auto-detect
		}
	}

	return p, nil
}

// extractCriticalPath walks the dominator tree from target to entry.
func (t *findCriticalPathTool) extractCriticalPath(entry, target string, domTree *graph.DominatorTree) ([]CriticalPathNode, error) {
	// Check if target is reachable (has dominators)
	dominators := domTree.DominatorsOf(target)
	if len(dominators) == 0 {
		// Target has no dominators - not reachable from entry
		return []CriticalPathNode{}, nil
	}

	// Build critical path: entry → ... → target
	// Dominators are returned in target → entry order, so reverse them
	// Note: dominators includes the target itself at the end
	pathLength := len(dominators)
	path := make([]CriticalPathNode, pathLength)

	// Add dominators in reverse order (entry → target)
	for i, dom := range dominators {
		idx := len(dominators) - 1 - i
		path[idx] = CriticalPathNode{
			ID:          dom,
			Name:        extractNameFromNodeID(dom),
			Depth:       idx,
			IsMandatory: true,
			Reason:      "Dominates target",
		}
	}

	// Update reasons for entry and target
	if len(path) > 0 {
		path[0].Reason = "Entry point"
		path[len(path)-1].Reason = "Target"
	}

	return path, nil
}

// buildExplanation creates a human-readable explanation of the critical path.
func (t *findCriticalPathTool) buildExplanation(path []CriticalPathNode, target string) string {
	if len(path) == 0 {
		return "Target is not reachable"
	}

	if len(path) == 1 {
		return fmt.Sprintf("%s is the entry point (no intermediate calls)", path[0].Name)
	}

	// Build path sequence: name1 → name2 → name3
	names := make([]string, len(path))
	for i, node := range path {
		names[i] = node.Name
	}

	return fmt.Sprintf(
		"To reach %s, you MUST call: %s",
		extractNameFromNodeID(target),
		strings.Join(names, " → "),
	)
}

// formatText creates a human-readable text summary.
func (t *findCriticalPathTool) formatText(path []CriticalPathNode, target, entry string) string {
	// Pre-size buffer for efficiency:
	// ~100 bytes for header/footer text
	// ~60 bytes per node (number + indent + name + arrow + newline)
	estimatedSize := 100 + len(path)*60
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString(fmt.Sprintf("Critical Path to %s\n\n", extractNameFromNodeID(target)))

	if len(path) == 0 {
		sb.WriteString("Target is not reachable from entry point.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Entry: %s\n", extractNameFromNodeID(entry)))
	sb.WriteString(fmt.Sprintf("Target: %s\n", extractNameFromNodeID(target)))
	sb.WriteString(fmt.Sprintf("Path length: %d nodes\n\n", len(path)))

	sb.WriteString("Mandatory Call Sequence:\n")
	for i, node := range path {
		indent := strings.Repeat("  ", node.Depth)
		arrow := ""
		if i < len(path)-1 {
			arrow = " →"
		}
		sb.WriteString(fmt.Sprintf("%s%d. %s%s\n", indent, i+1, node.Name, arrow))
	}

	sb.WriteString("\n")
	sb.WriteString(t.buildExplanation(path, target))
	sb.WriteString("\n")

	return sb.String()
}

// resolveSymbol resolves a symbol name or ID to a full ID.
//
// Supports both full IDs ("pkg/file.go:10:Name") and short names ("Name").
//
// Ambiguous matches: When multiple symbols have the same name (e.g., "Parse"
// in different packages), this method returns the first match. The choice is
// logged at debug level. Users should use full IDs for disambiguation or specify
// an entry point that narrows the scope.
func (t *findCriticalPathTool) resolveSymbol(nameOrID string) (string, error) {
	// Check if it looks like a full ID (contains ":")
	if strings.Contains(nameOrID, ":") {
		// Validate it exists
		if _, exists := t.index.GetByID(nameOrID); exists {
			return nameOrID, nil
		}
		return "", fmt.Errorf("symbol %q not found in graph", nameOrID)
	}

	// Try to find by name
	symbols := t.index.GetByName(nameOrID)
	if len(symbols) == 0 {
		return "", fmt.Errorf("symbol %q not found in graph", nameOrID)
	}
	if len(symbols) > 1 {
		// Multiple matches - use first one (could enhance this later)
		t.logger.Debug("multiple symbols found for name, using first",
			slog.String("name", nameOrID),
			slog.Int("count", len(symbols)),
			slog.String("selected", symbols[0].ID),
		)
	}

	return symbols[0].ID, nil
}
