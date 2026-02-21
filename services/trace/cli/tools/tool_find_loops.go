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
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_loops Tool (GR-17e) - Typed Implementation
// =============================================================================

var findLoopsTracer = otel.Tracer("tools.find_loops")

// FindLoopsParams contains the validated input parameters.
type FindLoopsParams struct {
	// Top is the number of loops to return.
	// Default: 20, Max: 100
	Top int

	// MinSize is the minimum loop body size.
	// Default: 1
	MinSize int

	// ShowNesting indicates whether to show loop nesting hierarchy.
	// Default: true
	ShowNesting bool
}

// ToolName returns the tool name for TypedParams interface.
func (p FindLoopsParams) ToolName() string { return "find_loops" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindLoopsParams) ToMap() map[string]any {
	return map[string]any{
		"top":          p.Top,
		"min_size":     p.MinSize,
		"show_nesting": p.ShowNesting,
	}
}

// FindLoopsOutput contains the structured result.
type FindLoopsOutput struct {
	// Loops is the list of detected natural loops.
	Loops []LoopInfo `json:"loops"`

	// Summary contains aggregate statistics.
	Summary LoopsSummary `json:"summary"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// LoopInfo holds information about a single natural loop.
type LoopInfo struct {
	// Header is the full node ID of the loop header.
	Header string `json:"header"`

	// HeaderName is the function name of the loop header.
	HeaderName string `json:"header_name"`

	// BodySize is the number of nodes in the loop body.
	BodySize int `json:"body_size"`

	// RecursionType describes the type of recursion (direct, mutual, complex).
	RecursionType string `json:"recursion_type"`

	// Body is the list of node IDs in the loop body.
	Body []string `json:"body"`

	// BackEdges are the edges that form this loop.
	BackEdges []BackEdgeInfo `json:"back_edges,omitempty"`

	// Depth is the nesting depth of this loop.
	Depth int `json:"depth,omitempty"`

	// ParentHeader is the header of the enclosing loop (if any).
	ParentHeader string `json:"parent_header,omitempty"`

	// NestedLoops lists headers of loops nested within this one.
	NestedLoops []string `json:"nested_loops,omitempty"`
}

// BackEdgeInfo represents a back edge in a loop.
type BackEdgeInfo struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// LoopsSummary contains aggregate statistics about loops.
type LoopsSummary struct {
	// TotalLoops is the total number of loops detected.
	TotalLoops int `json:"total_loops"`

	// Returned is the number of loops returned in this result.
	Returned int `json:"returned"`

	// DirectRecursion is the count of self-calling functions.
	DirectRecursion int `json:"direct_recursion"`

	// MutualRecursion is the count of two-function cycles.
	MutualRecursion int `json:"mutual_recursion"`

	// ComplexCycles is the count of larger cycles (3+ nodes).
	ComplexCycles int `json:"complex_cycles"`

	// MaxNesting is the maximum nesting depth.
	MaxNesting int `json:"max_nesting"`

	// DeepestLoop is the header of the deepest nested loop.
	DeepestLoop string `json:"deepest_loop,omitempty"`
}

// findLoopsTool detects natural loops and recursive call patterns in the graph.
type findLoopsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindLoopsTool creates a new find_loops tool.
//
// Description:
//
//	Creates a tool for detecting natural loops (recursion and cyclic call patterns)
//	in the call graph using dominator-based back edge analysis.
//
// Inputs:
//   - analytics: GraphAnalytics instance for loop detection. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_loops tool.
//
// Thread Safety: Safe for concurrent use after construction.
//
// Limitations:
//   - Requires dominator tree computation which needs an entry point
//   - May not detect all mutual recursion patterns in highly disconnected graphs
func NewFindLoopsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findLoopsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findLoopsTool) Name() string {
	return "find_loops"
}

func (t *findLoopsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findLoopsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_loops",
		Description: "Detect recursion and cyclic call patterns in the codebase. " +
			"Identifies natural loops via back edges in the call graph. " +
			"Shows loop nesting hierarchy for understanding recursive structures.",
		Parameters: map[string]ParamDef{
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of loops to return (default: 20, max: 100)",
				Required:    false,
				Default:     20,
			},
			"min_size": {
				Type:        ParamTypeInt,
				Description: "Minimum loop body size (default: 1)",
				Required:    false,
				Default:     1,
			},
			"show_nesting": {
				Type:        ParamTypeBool,
				Description: "Show loop nesting hierarchy (default: true)",
				Required:    false,
				Default:     true,
			},
		},
		Category:    CategoryExploration,
		Priority:    84,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     30 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"loops", "recursion", "recursive", "cyclic",
				"back edge", "self-call", "mutual recursion",
				"call cycle", "iteration", "repeated calls",
			},
			UseWhen: "User asks about recursion patterns, loops in call graph, " +
				"cyclic dependencies, or wants to find recursive functions.",
			AvoidWhen: "User asks about dependency cycles (use find_cycles) or " +
				"articulation points (use find_articulation_points).",
		},
	}
}

// Execute runs the find_loops tool.
func (t *findLoopsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
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
func (t *findLoopsTool) executeOnce(ctx context.Context, p FindLoopsParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := findLoopsTracer.Start(ctx, "findLoopsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_loops"),
			attribute.Int("top", p.Top),
			attribute.Int("min_size", p.MinSize),
			attribute.Bool("show_nesting", p.ShowNesting),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect entry point for dominator analysis
	entry, err := DetectEntryPoint(ctx, t.index, t.analytics)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to detect entry point: %v", err),
		}, nil
	}

	span.SetAttributes(attribute.String("entry", entry))

	// Compute dominator tree
	domTree, err := t.analytics.Dominators(ctx, entry)
	if err != nil {
		span.RecordError(err)
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to compute dominators: %v", err),
		}, nil
	}

	// Check context again before loop detection
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect loops using dominator-based algorithm
	loopNest, traceStep := t.analytics.DetectLoopsWithCRS(ctx, domTree)
	if loopNest == nil || len(loopNest.Loops) == 0 {
		// No loops found - return success with empty result
		span.SetAttributes(attribute.Int("loops_found", 0))
		output := FindLoopsOutput{
			Loops: []LoopInfo{},
			Summary: LoopsSummary{
				TotalLoops:      0,
				Returned:        0,
				DirectRecursion: 0,
				MutualRecursion: 0,
				ComplexCycles:   0,
				MaxNesting:      0,
			},
			Message: "No natural loops detected in the call graph",
		}
		outputText := "No natural loops detected in the call graph.\n"
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Filter and sort loops
	filteredLoops := t.filterAndSortLoops(loopNest.Loops, p.MinSize, p.Top)

	// Classify recursion types
	summary := t.classifyRecursionTypes(filteredLoops, loopNest)

	// Build typed output
	output := t.buildOutput(filteredLoops, loopNest, p.ShowNesting, summary, p.Top)

	// Format text output
	outputText := t.formatText(filteredLoops, loopNest, summary, p.Top)

	span.SetAttributes(
		attribute.Int("loops_found", len(loopNest.Loops)),
		attribute.Int("loops_returned", len(filteredLoops)),
		attribute.Int("max_depth", loopNest.MaxDepth),
		attribute.String("trace_action", traceStep.Action),
	)

	// Log completion for production debugging
	t.logger.Debug("find_loops completed",
		slog.String("tool", "find_loops"),
		slog.Int("loops_returned", len(filteredLoops)),
		slog.Int("total_loops", len(loopNest.Loops)),
		slog.Int("max_depth", loopNest.MaxDepth),
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
func (t *findLoopsTool) parseParams(params map[string]any) (FindLoopsParams, error) {
	p := FindLoopsParams{
		Top:         20,
		MinSize:     1,
		ShowNesting: true,
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_loops"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_loops"),
					slog.Int("requested", top),
				)
				top = 100
			}
			p.Top = top
		}
	}

	// Extract min_size (optional)
	if minSizeRaw, ok := params["min_size"]; ok {
		if minSize, ok := parseIntParam(minSizeRaw); ok {
			if minSize < 1 {
				t.logger.Debug("min_size below minimum, clamping to 1",
					slog.String("tool", "find_loops"),
					slog.Int("requested", minSize),
				)
				minSize = 1
			}
			p.MinSize = minSize
		}
	}

	// Extract show_nesting (optional)
	if showNestingRaw, ok := params["show_nesting"]; ok {
		if showNesting, ok := parseBoolParam(showNestingRaw); ok {
			p.ShowNesting = showNesting
		}
	}

	return p, nil
}

// filterAndSortLoops filters loops by min_size and returns top N sorted by size.
func (t *findLoopsTool) filterAndSortLoops(loops []*graph.Loop, minSize, top int) []*graph.Loop {
	// Filter by minimum size
	filtered := make([]*graph.Loop, 0, len(loops))
	for _, loop := range loops {
		if loop.Size >= minSize {
			filtered = append(filtered, loop)
		}
	}

	// Sort by size descending, then by header name for stability
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Size != filtered[j].Size {
			return filtered[i].Size > filtered[j].Size
		}
		return filtered[i].Header < filtered[j].Header
	})

	// Return top N
	if len(filtered) > top {
		filtered = filtered[:top]
	}

	return filtered
}

// classifyRecursionTypes analyzes loops to classify recursion patterns.
func (t *findLoopsTool) classifyRecursionTypes(loops []*graph.Loop, loopNest *graph.LoopNest) LoopsSummary {
	summary := LoopsSummary{
		TotalLoops: len(loopNest.Loops),
		Returned:   len(loops),
		MaxNesting: loopNest.MaxDepth,
	}

	for _, loop := range loops {
		// Classify by loop size.
		// Note: loop.Size includes the header node itself, so:
		//   Size=1 means just the header (direct self-call)
		//   Size=2 means header + 1 other node (mutual recursion A<->B)
		//   Size>=3 means complex cycle with multiple nodes
		switch loop.Size {
		case 1:
			summary.DirectRecursion++ // Self-loop
		case 2:
			summary.MutualRecursion++ // Mutual recursion (A <-> B)
		default:
			summary.ComplexCycles++ // Larger cycles
		}

		// Track deepest loop
		if loop.Depth == loopNest.MaxDepth && summary.DeepestLoop == "" {
			summary.DeepestLoop = loop.Header
		}
	}

	return summary
}

// classifyLoopRecursion returns a human-readable recursion type for a single loop.
// Note: loop.Size includes the header node itself.
func (t *findLoopsTool) classifyLoopRecursion(loop *graph.Loop) string {
	switch loop.Size {
	case 1:
		return "direct recursion" // Self-call
	case 2:
		return "mutual recursion" // A<->B pattern
	default:
		return fmt.Sprintf("complex cycle (%d nodes)", loop.Size)
	}
}

// buildOutput creates the typed output struct.
func (t *findLoopsTool) buildOutput(
	loops []*graph.Loop,
	loopNest *graph.LoopNest,
	showNesting bool,
	summary LoopsSummary,
	top int,
) FindLoopsOutput {
	// Format individual loops
	loopInfos := make([]LoopInfo, 0, len(loops))
	for _, loop := range loops {
		info := LoopInfo{
			Header:        loop.Header,
			HeaderName:    extractNameFromNodeID(loop.Header),
			BodySize:      loop.Size,
			RecursionType: t.classifyLoopRecursion(loop),
			Body:          loop.Body,
		}

		// Add back edges
		if len(loop.BackEdges) > 0 {
			info.BackEdges = make([]BackEdgeInfo, 0, len(loop.BackEdges))
			for _, edge := range loop.BackEdges {
				info.BackEdges = append(info.BackEdges, BackEdgeInfo{
					From: edge[0],
					To:   edge[1],
				})
			}
		}

		// Add nesting info if requested
		if showNesting {
			info.Depth = loop.Depth
			if loop.Parent != nil {
				info.ParentHeader = loop.Parent.Header
			}
			if len(loop.Children) > 0 {
				info.NestedLoops = make([]string, 0, len(loop.Children))
				for _, child := range loop.Children {
					info.NestedLoops = append(info.NestedLoops, child.Header)
				}
			}
		}

		loopInfos = append(loopInfos, info)
	}

	return FindLoopsOutput{
		Loops:   loopInfos,
		Summary: summary,
	}
}

// formatText creates a human-readable text summary.
func (t *findLoopsTool) formatText(
	loops []*graph.Loop,
	loopNest *graph.LoopNest,
	summary LoopsSummary,
	top int,
) string {
	// Pre-size buffer: ~200 base + 150/loop
	estimatedSize := 200 + len(loops)*150
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString(fmt.Sprintf("Found %d natural loops", len(loopNest.Loops)))
	if len(loopNest.Loops) > top {
		sb.WriteString(fmt.Sprintf(" (showing top %d)", top))
	}
	sb.WriteString("\n\n")

	// Recursion type breakdown
	sb.WriteString("Recursion Types:\n")
	if summary.DirectRecursion > 0 {
		sb.WriteString(fmt.Sprintf("  - Direct recursion (self-calls): %d\n", summary.DirectRecursion))
	}
	if summary.MutualRecursion > 0 {
		sb.WriteString(fmt.Sprintf("  - Mutual recursion (A<->B): %d\n", summary.MutualRecursion))
	}
	if summary.ComplexCycles > 0 {
		sb.WriteString(fmt.Sprintf("  - Complex cycles (3+ nodes): %d\n", summary.ComplexCycles))
	}
	sb.WriteString("\n")

	// List loops
	sb.WriteString("Loops (sorted by size):\n")
	for i, loop := range loops {
		name := extractNameFromNodeID(loop.Header)
		recursionType := t.classifyLoopRecursion(loop)
		sb.WriteString(fmt.Sprintf("  %d. %s (%s)\n", i+1, name, recursionType))
		sb.WriteString(fmt.Sprintf("     Header: %s\n", loop.Header))
		sb.WriteString(fmt.Sprintf("     Body size: %d nodes\n", loop.Size))
		if loop.Depth > 0 {
			sb.WriteString(fmt.Sprintf("     Nesting depth: %d\n", loop.Depth))
		}
		sb.WriteString("\n")
	}

	// Nesting summary
	if loopNest.MaxDepth > 0 {
		sb.WriteString(fmt.Sprintf("Maximum nesting depth: %d\n", loopNest.MaxDepth))
		if summary.DeepestLoop != "" {
			sb.WriteString(fmt.Sprintf("Deepest loop header: %s\n", summary.DeepestLoop))
		}
	}

	return sb.String()
}
