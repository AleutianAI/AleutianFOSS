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

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_extractable_regions Tool (GR-17g) - Typed Implementation
// =============================================================================

var findExtractableRegionsTracer = otel.Tracer("tools.find_extractable_regions")

// FindExtractableRegionsParams contains the validated input parameters.
type FindExtractableRegionsParams struct {
	// MinSize is the minimum region size to report.
	// Default: 3
	MinSize int

	// MaxSize is the maximum region size.
	// Default: 50
	MaxSize int

	// Top is the number of regions to return.
	// Default: 10, Max: 100
	Top int
}

// ToolName returns the tool name for TypedParams interface.
func (p FindExtractableRegionsParams) ToolName() string { return "find_extractable_regions" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindExtractableRegionsParams) ToMap() map[string]any {
	return map[string]any{
		"min_size": p.MinSize,
		"max_size": p.MaxSize,
		"top":      p.Top,
	}
}

// FindExtractableRegionsOutput contains the structured result.
type FindExtractableRegionsOutput struct {
	// Regions is the list of extractable SESE regions.
	Regions []ExtractableRegionInfo `json:"regions"`

	// RegionCount is the total number of regions found.
	RegionCount int `json:"region_count"`

	// Summary contains aggregate statistics.
	Summary ExtractableRegionsSummary `json:"summary"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// ExtractableRegionInfo holds information about an extractable SESE region.
type ExtractableRegionInfo struct {
	// Entry is the single entry node ID.
	Entry string `json:"entry"`

	// EntryName is the function name of the entry.
	EntryName string `json:"entry_name"`

	// Exit is the single exit node ID.
	Exit string `json:"exit"`

	// ExitName is the function name of the exit.
	ExitName string `json:"exit_name"`

	// Size is the number of nodes in the region.
	Size int `json:"size"`

	// Depth is the nesting depth (0 = top-level).
	Depth int `json:"depth"`

	// Nodes is the list of node IDs in the region (if small enough).
	Nodes []string `json:"nodes,omitempty"`

	// HasNestedRegions indicates if this region contains nested SESE regions.
	HasNestedRegions bool `json:"has_nested_regions,omitempty"`
}

// ExtractableRegionsSummary contains aggregate statistics.
type ExtractableRegionsSummary struct {
	// TotalRegions is the total SESE regions detected (before filtering).
	TotalRegions int `json:"total_regions"`

	// ExtractableCount is the number meeting size criteria.
	ExtractableCount int `json:"extractable_count"`

	// Returned is the number returned (after top limit).
	Returned int `json:"returned"`

	// MaxDepth is the maximum nesting depth found.
	MaxDepth int `json:"max_depth"`

	// AvgSize is the average region size.
	AvgSize float64 `json:"avg_size"`

	// MinSizeUsed is the actual min_size used (after clamping).
	MinSizeUsed int `json:"min_size_used"`

	// MaxSizeUsed is the actual max_size used (after clamping).
	MaxSizeUsed int `json:"max_size_used"`
}

// findExtractableRegionsTool finds SESE regions suitable for extraction.
type findExtractableRegionsTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindExtractableRegionsTool creates a new find_extractable_regions tool.
//
// Description:
//
//	Creates a tool for finding Single-Entry Single-Exit (SESE) regions
//	that are suitable for extraction into separate functions. These regions
//	have exactly one entry and one exit point, making them safe to refactor.
//
// Inputs:
//   - analytics: GraphAnalytics instance for SESE detection. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_extractable_regions tool.
//
// Limitations:
//   - Requires both dominator and post-dominator tree computation
//   - May produce many regions in large codebases; use size filters
//
// Thread Safety: Safe for concurrent use after construction.
func NewFindExtractableRegionsTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findExtractableRegionsTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findExtractableRegionsTool) Name() string {
	return "find_extractable_regions"
}

func (t *findExtractableRegionsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findExtractableRegionsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_extractable_regions",
		Description: "Find code regions that can be safely refactored into separate functions. " +
			"Identifies Single-Entry Single-Exit (SESE) regions suitable for extraction. " +
			"Use this to find refactoring opportunities.",
		Parameters: map[string]ParamDef{
			"min_size": {
				Type:        ParamTypeInt,
				Description: "Minimum region size to report (default: 3)",
				Required:    false,
				Default:     3,
			},
			"max_size": {
				Type:        ParamTypeInt,
				Description: "Maximum region size (default: 50)",
				Required:    false,
				Default:     50,
			},
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of regions to return (default: 10, max: 100)",
				Required:    false,
				Default:     10,
			},
		},
		Category:    CategoryExploration,
		Priority:    80,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     60 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"extractable", "refactor", "SESE", "single entry",
				"single exit", "extract function", "extract method",
				"modularize", "break apart", "decompose",
			},
			UseWhen: "User asks about refactoring opportunities, code extraction, " +
				"or wants to find functions that can be safely modularized.",
			AvoidWhen: "User asks about code structure (use find_communities) or " +
				"dead code (use find_dead_code).",
		},
	}
}

// Execute runs the find_extractable_regions tool.
func (t *findExtractableRegionsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
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
func (t *findExtractableRegionsTool) executeOnce(ctx context.Context, p FindExtractableRegionsParams) (*Result, error) {
	start := time.Now()

	// Start span with context
	ctx, span := findExtractableRegionsTracer.Start(ctx, "findExtractableRegionsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_extractable_regions"),
			attribute.Int("min_size", p.MinSize),
			attribute.Int("max_size", p.MaxSize),
			attribute.Int("top", p.Top),
		),
	)
	defer span.End()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect entry point for dominator tree
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

	// Compute post-dominator tree
	postDomTree, err := t.analytics.PostDominators(ctx, "")
	if err != nil {
		span.RecordError(err)
		t.logger.Debug("failed to compute post-dominators",
			slog.String("tool", "find_extractable_regions"),
			slog.String("error", err.Error()),
		)
		// Continue with empty post-dominator tree
		postDomTree = nil
	}

	// Check context again
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Detect SESE regions
	var seseResult *graph.SESEResult
	var traceStep crs.TraceStep

	if postDomTree != nil {
		seseResult, traceStep = t.analytics.DetectSESERegionsWithCRS(ctx, domTree, postDomTree)
	} else {
		// Create approximation without post-dominators
		seseResult, traceStep = t.approximateSESERegions(ctx, domTree)
	}

	if seseResult == nil || len(seseResult.Regions) == 0 {
		outputText := fmt.Sprintf("No SESE regions found.\nSize range: %d-%d nodes\n", p.MinSize, p.MaxSize)
		return &Result{
			Success: true,
			Output: FindExtractableRegionsOutput{
				Regions:     []ExtractableRegionInfo{},
				RegionCount: 0,
				Summary: ExtractableRegionsSummary{
					TotalRegions:     0,
					ExtractableCount: 0,
					Returned:         0,
					MinSizeUsed:      p.MinSize,
					MaxSizeUsed:      p.MaxSize,
				},
				Message: "No SESE regions found",
			},
			OutputText: outputText,
			TokensUsed: estimateTokens(outputText),
			TraceStep:  &traceStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Filter extractable regions by size
	extractable := seseResult.FilterExtractable(p.MinSize, p.MaxSize)

	// Sort by size descending (larger regions first)
	sort.Slice(extractable, func(i, j int) bool {
		return extractable[i].Size > extractable[j].Size
	})

	// Limit to top N
	if len(extractable) > p.Top {
		extractable = extractable[:p.Top]
	}

	// Build output
	output := t.buildOutput(extractable, seseResult, p)

	// Format text output
	outputText := t.formatText(extractable, output.Summary)

	span.SetAttributes(
		attribute.Int("total_regions", len(seseResult.Regions)),
		attribute.Int("extractable_count", len(extractable)),
		attribute.Int("max_depth", seseResult.MaxDepth),
		attribute.String("trace_action", traceStep.Action),
	)

	t.logger.Debug("find_extractable_regions completed",
		slog.String("tool", "find_extractable_regions"),
		slog.Int("total_regions", len(seseResult.Regions)),
		slog.Int("extractable", len(extractable)),
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
func (t *findExtractableRegionsTool) parseParams(params map[string]any) (FindExtractableRegionsParams, error) {
	p := FindExtractableRegionsParams{
		MinSize: 3,
		MaxSize: 50,
		Top:     10,
	}

	// Extract min_size (optional)
	if minSizeRaw, ok := params["min_size"]; ok {
		if minSize, ok := parseIntParam(minSizeRaw); ok {
			if minSize < 1 {
				t.logger.Debug("min_size below minimum, clamping to 1",
					slog.String("tool", "find_extractable_regions"),
					slog.Int("requested", minSize),
				)
				minSize = 1
			}
			p.MinSize = minSize
		}
	}

	// Extract max_size (optional)
	if maxSizeRaw, ok := params["max_size"]; ok {
		if maxSize, ok := parseIntParam(maxSizeRaw); ok {
			if maxSize < p.MinSize {
				maxSize = p.MinSize // Ensure max >= min
			}
			p.MaxSize = maxSize
		}
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_extractable_regions"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 100 {
				t.logger.Debug("top above maximum, clamping to 100",
					slog.String("tool", "find_extractable_regions"),
					slog.Int("requested", top),
				)
				top = 100
			}
			p.Top = top
		}
	}

	return p, nil
}

// approximateSESERegions creates an approximation without post-dominators.
func (t *findExtractableRegionsTool) approximateSESERegions(
	ctx context.Context,
	domTree *graph.DominatorTree,
) (*graph.SESEResult, crs.TraceStep) {
	start := time.Now()

	// Create approximate SESE regions based on dominator subtrees
	// Each dominator and its dominated nodes form a potential SESE region
	regions := make([]*graph.SESERegion, 0)

	// Collect and sort node IDs for deterministic iteration order
	nodeIDs := make([]string, 0, len(domTree.Children))
	for nodeID := range domTree.Children {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		children := domTree.Children[nodeID]
		if len(children) > 0 {
			// Get all nodes dominated by this node
			dominated := domTree.DominatedBy(nodeID)
			if len(dominated) >= 2 { // At least entry + one other node
				region := &graph.SESERegion{
					Entry: nodeID,
					Exit:  dominated[len(dominated)-1], // Last dominated node as approx exit
					Nodes: dominated,
					Size:  len(dominated),
					Depth: domTree.Depth[nodeID],
				}
				regions = append(regions, region)
			}
		}
	}

	result := &graph.SESEResult{
		Regions:     regions,
		RegionCount: len(regions),
		Extractable: regions, // All approximate regions are potentially extractable
	}

	// Calculate max depth
	for _, r := range regions {
		if r.Depth > result.MaxDepth {
			result.MaxDepth = r.Depth
		}
	}

	traceStep := crs.TraceStep{
		Action:   "analytics_sese_approx",
		Tool:     "DetectSESERegions",
		Duration: time.Since(start),
		Metadata: map[string]string{
			"regions_found": fmt.Sprintf("%d", len(regions)),
			"approximate":   "true",
		},
	}

	return result, traceStep
}

// buildOutput creates the typed output struct.
func (t *findExtractableRegionsTool) buildOutput(
	regions []*graph.SESERegion,
	seseResult *graph.SESEResult,
	params FindExtractableRegionsParams,
) FindExtractableRegionsOutput {
	regionInfos := make([]ExtractableRegionInfo, 0, len(regions))

	var totalSize int
	for _, region := range regions {
		info := ExtractableRegionInfo{
			Entry:     region.Entry,
			EntryName: extractNameFromNodeID(region.Entry),
			Exit:      region.Exit,
			ExitName:  extractNameFromNodeID(region.Exit),
			Size:      region.Size,
			Depth:     region.Depth,
		}

		// Add symbol info if available
		if t.index != nil {
			if sym, ok := t.index.GetByID(region.Entry); ok && sym != nil {
				info.EntryName = sym.Name
			}
			if sym, ok := t.index.GetByID(region.Exit); ok && sym != nil {
				info.ExitName = sym.Name
			}
		}

		// Include nodes list for small regions
		if region.Size <= 20 {
			info.Nodes = region.Nodes
		}

		// Check for nested regions
		info.HasNestedRegions = len(region.Children) > 0

		regionInfos = append(regionInfos, info)
		totalSize += region.Size
	}

	avgSize := 0.0
	if len(regions) > 0 {
		avgSize = float64(totalSize) / float64(len(regions))
	}

	summary := ExtractableRegionsSummary{
		TotalRegions:     len(seseResult.Regions),
		ExtractableCount: len(seseResult.Extractable), // Use pre-computed extractable list
		Returned:         len(regionInfos),
		MaxDepth:         seseResult.MaxDepth,
		AvgSize:          avgSize,
		MinSizeUsed:      params.MinSize,
		MaxSizeUsed:      params.MaxSize,
	}

	return FindExtractableRegionsOutput{
		Regions:     regionInfos,
		RegionCount: len(regionInfos),
		Summary:     summary,
	}
}

// formatText creates a human-readable text summary.
func (t *findExtractableRegionsTool) formatText(
	regions []*graph.SESERegion,
	summary ExtractableRegionsSummary,
) string {
	// Pre-size buffer: ~200 base + 150/region
	estimatedSize := 200 + len(regions)*150
	var sb strings.Builder
	sb.Grow(estimatedSize)

	if len(regions) == 0 {
		sb.WriteString("No extractable SESE regions found.\n")
		sb.WriteString(fmt.Sprintf("Size range: %d-%d nodes\n", summary.MinSizeUsed, summary.MaxSizeUsed))
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d extractable SESE regions", summary.ExtractableCount))
	if summary.Returned < summary.ExtractableCount {
		sb.WriteString(fmt.Sprintf(" (showing top %d)", summary.Returned))
	}
	sb.WriteString("\n\n")

	sb.WriteString(fmt.Sprintf("Size range: %d-%d nodes, Avg size: %.1f\n",
		summary.MinSizeUsed, summary.MaxSizeUsed, summary.AvgSize))
	sb.WriteString(fmt.Sprintf("Max nesting depth: %d\n\n", summary.MaxDepth))

	sb.WriteString("Extractable Regions (sorted by size):\n")
	for i, region := range regions {
		entryName := extractNameFromNodeID(region.Entry)
		exitName := extractNameFromNodeID(region.Exit)

		sb.WriteString(fmt.Sprintf("  %d. %s -> %s (%d nodes)\n",
			i+1, entryName, exitName, region.Size))
		sb.WriteString(fmt.Sprintf("     Entry: %s\n", region.Entry))
		sb.WriteString(fmt.Sprintf("     Exit: %s\n", region.Exit))
		if region.Depth > 0 {
			sb.WriteString(fmt.Sprintf("     Depth: %d\n", region.Depth))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
