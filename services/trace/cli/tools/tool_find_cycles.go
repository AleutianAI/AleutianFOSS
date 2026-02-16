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

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_cycles Tool - Typed Implementation
// =============================================================================

var findCyclesTracer = otel.Tracer("tools.find_cycles")

// FindCyclesParams contains the validated input parameters.
type FindCyclesParams struct {
	// MinSize is the minimum cycle size to report.
	// Default: 2
	MinSize int

	// Limit is the maximum number of cycles to return.
	// Default: 20, Max: 100
	Limit int
}

// FindCyclesOutput contains the structured result.
type FindCyclesOutput struct {
	// CycleCount is the number of cycles returned.
	CycleCount int `json:"cycle_count"`

	// Cycles is the list of detected cycles.
	Cycles []CycleInfo `json:"cycles"`
}

// CycleInfo holds information about a single cycle.
type CycleInfo struct {
	// CycleNumber is the position in the result list (1-based).
	CycleNumber int `json:"cycle_number"`

	// Length is the number of nodes in this cycle.
	Length int `json:"length"`

	// Packages lists the packages involved in this cycle.
	Packages []string `json:"packages"`

	// Nodes is the list of nodes in the cycle.
	Nodes []CycleNode `json:"nodes"`
}

// CycleNode represents a node in a cycle.
type CycleNode struct {
	// ID is the node ID.
	ID string `json:"id"`

	// Name is the symbol name.
	Name string `json:"name,omitempty"`

	// File is the source file path.
	File string `json:"file,omitempty"`

	// Line is the line number.
	Line int `json:"line,omitempty"`
}

// findCyclesTool finds circular dependencies in the codebase.
type findCyclesTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindCyclesTool creates the find_cycles tool.
//
// Description:
//
//	Creates a tool that finds circular dependencies in the codebase using
//	Tarjan's SCC algorithm. Cycles indicate tight coupling that can make
//	code harder to maintain, test, and understand.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance for cycle detection. Must not be nil.
//   - idx: The symbol index for resolving node IDs to symbol names. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_cycles tool implementation.
//
// Limitations:
//
//   - Only detects call-graph cycles, not import cycles or data flow cycles
//   - Maximum 100 cycles per query to prevent excessive output
//   - Large cycles (many nodes) may be harder to visualize in text output
//
// Assumptions:
//
//   - Graph is frozen before tool creation
//   - Tarjan's algorithm runs in O(V+E) time
func NewFindCyclesTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findCyclesTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findCyclesTool) Name() string {
	return "find_cycles"
}

func (t *findCyclesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findCyclesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_cycles",
		Description: "Find circular dependencies in the codebase. " +
			"Uses Tarjan's SCC algorithm to detect cycles. " +
			"Cycles indicate tight coupling that can make code harder to maintain.",
		Parameters: map[string]ParamDef{
			"min_size": {
				Type:        ParamTypeInt,
				Description: "Minimum cycle size to report (default: 2)",
				Required:    false,
				Default:     2,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of cycles to return",
				Required:    false,
				Default:     20,
			},
		},
		Category:    CategoryExploration,
		Priority:    82,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     15 * time.Second,
	}
}

// Execute runs the find_cycles tool.
func (t *findCyclesTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	// Parse and validate parameters
	p, err := t.parseParams(params)
	if err != nil {
		return &Result{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	// Validate analytics is available
	if t.analytics == nil {
		return &Result{
			Success: false,
			Error:   "graph analytics not initialized",
		}, nil
	}

	// Start span with context
	ctx, span := findCyclesTracer.Start(ctx, "findCyclesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_cycles"),
			attribute.Int("min_size", p.MinSize),
			attribute.Int("limit", p.Limit),
		),
	)
	defer span.End()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Get cycles
	cycles, traceStep := t.analytics.CyclicDependenciesWithCRS(ctx)

	span.SetAttributes(
		attribute.Int("raw_cycles_count", len(cycles)),
		attribute.String("trace_action", traceStep.Action),
	)

	// Filter by min_size and apply limit
	var filtered []graph.CyclicDependency
	for _, cycle := range cycles {
		if cycle.Length >= p.MinSize {
			filtered = append(filtered, cycle)
		}
		if len(filtered) >= p.Limit {
			break
		}
	}

	span.SetAttributes(attribute.Int("filtered_cycles_count", len(filtered)))

	// Structured logging for edge cases
	if len(cycles) > 0 && len(filtered) == 0 {
		t.logger.Debug("all cycles filtered by min_size",
			slog.String("tool", "find_cycles"),
			slog.Int("raw_count", len(cycles)),
			slog.Int("min_size", p.MinSize),
		)
	} else if len(filtered) >= p.Limit {
		t.logger.Debug("cycle results limited",
			slog.String("tool", "find_cycles"),
			slog.Int("raw_count", len(cycles)),
			slog.Int("limit", p.Limit),
			slog.Int("returned", len(filtered)),
		)
	}

	// Build typed output
	output := t.buildOutput(filtered)

	// Format text output
	outputText := t.formatText(filtered)

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
func (t *findCyclesTool) parseParams(params map[string]any) (FindCyclesParams, error) {
	p := FindCyclesParams{
		MinSize: 2,
		Limit:   20,
	}

	// Extract min_size (optional)
	if minSizeRaw, ok := params["min_size"]; ok {
		if minSize, ok := parseIntParam(minSizeRaw); ok {
			if minSize < 2 {
				t.logger.Debug("min_size below minimum, clamping to 2",
					slog.String("tool", "find_cycles"),
					slog.Int("requested", minSize),
				)
				minSize = 2
			}
			p.MinSize = minSize
		}
	}

	// Extract limit (optional)
	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok {
			if limit < 1 {
				t.logger.Debug("limit below minimum, clamping to 1",
					slog.String("tool", "find_cycles"),
					slog.Int("requested", limit),
				)
				limit = 1
			} else if limit > 100 {
				t.logger.Debug("limit above maximum, clamping to 100",
					slog.String("tool", "find_cycles"),
					slog.Int("requested", limit),
				)
				limit = 100
			}
			p.Limit = limit
		}
	}

	return p, nil
}

// buildOutput creates the typed output struct.
func (t *findCyclesTool) buildOutput(cycles []graph.CyclicDependency) FindCyclesOutput {
	cycleInfos := make([]CycleInfo, 0, len(cycles))

	for i, cycle := range cycles {
		// Resolve node names from IDs
		nodes := make([]CycleNode, 0, len(cycle.Nodes))
		for _, nodeID := range cycle.Nodes {
			node := CycleNode{ID: nodeID}
			if t.index != nil {
				if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
					node.Name = sym.Name
					node.File = sym.FilePath
					node.Line = sym.StartLine
				}
			}
			nodes = append(nodes, node)
		}

		cycleInfos = append(cycleInfos, CycleInfo{
			CycleNumber: i + 1,
			Length:      cycle.Length,
			Packages:    cycle.Packages,
			Nodes:       nodes,
		})
	}

	return FindCyclesOutput{
		CycleCount: len(cycleInfos),
		Cycles:     cycleInfos,
	}
}

// formatText creates a human-readable text summary.
func (t *findCyclesTool) formatText(cycles []graph.CyclicDependency) string {
	var sb strings.Builder

	if len(cycles) == 0 {
		sb.WriteString("No circular dependencies found.\n")
		sb.WriteString("This is good news! The codebase has no detectable cycles.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d circular dependencies:\n\n", len(cycles)))

	for i, cycle := range cycles {
		sb.WriteString(fmt.Sprintf("Cycle %d (%d nodes):\n", i+1, cycle.Length))

		// Show the cycle path
		for j, nodeID := range cycle.Nodes {
			prefix := "  "
			if j < len(cycle.Nodes)-1 {
				prefix = "  -> "
			}

			nodeName := nodeID
			nodeFile := ""
			if t.index != nil {
				if sym, ok := t.index.GetByID(nodeID); ok && sym != nil {
					nodeName = sym.Name + "()"
					nodeFile = fmt.Sprintf(" [%s:%d]", sym.FilePath, sym.StartLine)
				}
			}

			if j == 0 {
				sb.WriteString(fmt.Sprintf("  %s%s\n", nodeName, nodeFile))
			} else {
				sb.WriteString(fmt.Sprintf("%s%s%s\n", prefix, nodeName, nodeFile))
			}
		}

		// Show closing edge back to first node
		if len(cycle.Nodes) > 0 {
			firstNode := cycle.Nodes[0]
			firstName := firstNode
			if t.index != nil {
				if sym, ok := t.index.GetByID(firstNode); ok && sym != nil {
					firstName = sym.Name + "()"
				}
			}
			sb.WriteString(fmt.Sprintf("  -> %s (cycle back)\n", firstName))
		}

		if len(cycle.Packages) > 1 {
			sb.WriteString(fmt.Sprintf("  Packages involved: %s\n", strings.Join(cycle.Packages, ", ")))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
