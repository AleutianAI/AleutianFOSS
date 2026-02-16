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
// find_communities Tool (GR-15) - Typed Implementation
// =============================================================================

var findCommunitiesTracer = otel.Tracer("tools.find_communities")

// FindCommunitiesParams contains the validated input parameters.
type FindCommunitiesParams struct {
	// MinSize is the minimum community size to report.
	// Default: 3, Max: 100
	MinSize int

	// Resolution controls community granularity.
	// 0.1 = large communities, 1.0 = balanced, 5.0 = small communities.
	// Default: 1.0
	Resolution float64

	// Top is the number of communities to return.
	// Default: 20, Max: 50
	Top int

	// ShowCrossEdges indicates whether to show edges between communities.
	// Default: true
	ShowCrossEdges bool
}

// FindCommunitiesOutput contains the structured result.
type FindCommunitiesOutput struct {
	// Modularity is the modularity score (0-1, higher = better separation).
	Modularity float64 `json:"modularity"`

	// ModularityQuality is a human-readable quality label.
	ModularityQuality string `json:"modularity_quality"`

	// CommunityCount is the number of communities returned.
	CommunityCount int `json:"community_count"`

	// TotalCommunities is the total number of communities detected.
	TotalCommunities int `json:"total_communities"`

	// Algorithm is the algorithm used (Leiden).
	Algorithm string `json:"algorithm"`

	// Converged indicates whether the algorithm converged.
	Converged bool `json:"converged"`

	// Iterations is the number of iterations run.
	Iterations int `json:"iterations"`

	// NodeCount is the total number of nodes in the graph.
	NodeCount int `json:"node_count"`

	// EdgeCount is the total number of edges in the graph.
	EdgeCount int `json:"edge_count"`

	// Communities is the list of detected communities.
	Communities []CommunityInfo `json:"communities"`

	// CrossPackageCommunities lists IDs of communities spanning multiple packages.
	CrossPackageCommunities []int `json:"cross_package_communities"`

	// CrossCommunityEdges lists edges between communities.
	CrossCommunityEdges []CrossEdgeInfo `json:"cross_community_edges,omitempty"`
}

// CommunityInfo holds information about a single community.
type CommunityInfo struct {
	// ID is the community identifier.
	ID int `json:"id"`

	// Size is the number of members in this community.
	Size int `json:"size"`

	// DominantPackage is the most common package in this community.
	DominantPackage string `json:"dominant_package"`

	// Packages lists all packages represented in this community.
	Packages []string `json:"packages"`

	// IsCrossPackage indicates if this community spans multiple packages.
	IsCrossPackage bool `json:"is_cross_package"`

	// Connectivity is the ratio of internal to total edges.
	Connectivity float64 `json:"connectivity"`

	// InternalEdges is the count of edges within this community.
	InternalEdges int `json:"internal_edges"`

	// ExternalEdges is the count of edges to other communities.
	ExternalEdges int `json:"external_edges"`

	// Members is a sample of member node IDs (limited to 10).
	Members []CommunityMember `json:"members"`
}

// CommunityMember represents a member of a community.
type CommunityMember struct {
	ID string `json:"id"`
}

// CrossEdgeInfo represents edges between communities.
type CrossEdgeInfo struct {
	FromCommunity int `json:"from_community"`
	ToCommunity   int `json:"to_community"`
	Count         int `json:"count"`
}

// findCommunitiesTool discovers natural code communities using Leiden algorithm.
type findCommunitiesTool struct {
	analytics *graph.GraphAnalytics
	index     *index.SymbolIndex
	logger    *slog.Logger
}

// NewFindCommunitiesTool creates the find_communities tool.
//
// Description:
//
//	Creates a tool that discovers natural code communities using the
//	Leiden algorithm. Unlike package boundaries which are organizational
//	choices, communities reflect actual code coupling patterns.
//
// Inputs:
//
//   - analytics: The GraphAnalytics instance. Must not be nil.
//   - idx: Symbol index for name lookups. Must not be nil.
//
// Outputs:
//
//   - Tool: The find_communities tool implementation.
//
// Limitations:
//
//   - Leiden is more expensive than simple queries: O(V+E) per iteration
//   - Maximum 50 communities reported to prevent excessive output
//   - Large codebases (>100K nodes) may take several seconds
//
// Assumptions:
//
//   - Graph is frozen and indexed before tool creation
//   - Analytics wraps a HierarchicalGraph
func NewFindCommunitiesTool(analytics *graph.GraphAnalytics, idx *index.SymbolIndex) Tool {
	return &findCommunitiesTool{
		analytics: analytics,
		index:     idx,
		logger:    slog.Default(),
	}
}

func (t *findCommunitiesTool) Name() string {
	return "find_communities"
}

func (t *findCommunitiesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findCommunitiesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_communities",
		Description: "Detect natural code communities using Leiden algorithm. " +
			"Finds groups of tightly-coupled, well-connected symbols that may not align with packages. " +
			"Use this to discover real module boundaries vs package organization. " +
			"Highlights cross-package communities as refactoring candidates.",
		Parameters: map[string]ParamDef{
			"min_size": {
				Type:        ParamTypeInt,
				Description: "Minimum community size to report (default: 3, max: 100)",
				Required:    false,
				Default:     3,
			},
			"resolution": {
				Type:        ParamTypeFloat,
				Description: "Community granularity: 0.1=large, 1.0=balanced, 5.0=small (default: 1.0)",
				Required:    false,
				Default:     1.0,
			},
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of communities to return (default: 20, max: 50)",
				Required:    false,
				Default:     20,
			},
			"show_cross_edges": {
				Type:        ParamTypeBool,
				Description: "Show edges between communities for seam identification (default: true)",
				Required:    false,
				Default:     true,
			},
		},
		Category:    CategoryExploration,
		Priority:    82,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     60 * time.Second,
	}
}

// Execute runs the find_communities tool.
func (t *findCommunitiesTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
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
	ctx, span := findCommunitiesTracer.Start(ctx, "findCommunitiesTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_communities"),
			attribute.Int("min_size", p.MinSize),
			attribute.Float64("resolution", p.Resolution),
			attribute.Int("top", p.Top),
			attribute.Bool("show_cross_edges", p.ShowCrossEdges),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Build Leiden options
	opts := &graph.LeidenOptions{
		Resolution:       p.Resolution,
		MinCommunitySize: p.MinSize,
	}

	// Call DetectCommunitiesWithCRS for tracing
	result, traceStep := t.analytics.DetectCommunitiesWithCRS(ctx, opts)

	span.SetAttributes(
		attribute.Int("raw_communities", len(result.Communities)),
		attribute.Float64("modularity", result.Modularity),
		attribute.Bool("converged", result.Converged),
		attribute.Int("iterations", result.Iterations),
		attribute.String("trace_action", traceStep.Action),
	)

	// Filter communities by min_size (already done in Leiden, but double-check)
	var filtered []graph.Community
	for _, comm := range result.Communities {
		if len(comm.Nodes) >= p.MinSize {
			filtered = append(filtered, comm)
		}
	}

	// Sort by size (largest first)
	sort.Slice(filtered, func(i, j int) bool {
		return len(filtered[i].Nodes) > len(filtered[j].Nodes)
	})

	// Trim to top
	if len(filtered) > p.Top {
		filtered = filtered[:p.Top]
	}

	span.SetAttributes(attribute.Int("filtered_communities", len(filtered)))

	// Identify cross-package communities
	crossPkgIDs := t.identifyCrossPackageCommunities(filtered)

	// Calculate cross-community edges if requested
	var crossEdges []CrossEdgeInfo
	if p.ShowCrossEdges && len(filtered) > 1 {
		crossEdges = t.calculateCrossCommunityEdges(filtered)
	}

	// Build typed output
	output := t.buildOutput(result, filtered, crossPkgIDs, crossEdges)

	// Format text output
	outputText := t.formatText(result, filtered, crossPkgIDs, crossEdges)

	// Log completion for production debugging
	t.logger.Debug("find_communities completed",
		slog.String("tool", "find_communities"),
		slog.Int("communities_found", len(filtered)),
		slog.Float64("modularity", result.Modularity),
		slog.Bool("converged", result.Converged),
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
func (t *findCommunitiesTool) parseParams(params map[string]any) (FindCommunitiesParams, error) {
	p := FindCommunitiesParams{
		MinSize:        3,
		Resolution:     1.0,
		Top:            20,
		ShowCrossEdges: true,
	}

	// Extract min_size (optional)
	if minSizeRaw, ok := params["min_size"]; ok {
		if minSize, ok := parseIntParam(minSizeRaw); ok {
			if minSize < 1 {
				t.logger.Debug("min_size below minimum, clamping to 1",
					slog.String("tool", "find_communities"),
					slog.Int("requested", minSize),
				)
				minSize = 1
			} else if minSize > 100 {
				t.logger.Debug("min_size above maximum, clamping to 100",
					slog.String("tool", "find_communities"),
					slog.Int("requested", minSize),
				)
				minSize = 100
			}
			p.MinSize = minSize
		}
	}

	// Extract resolution (optional)
	if resolutionRaw, ok := params["resolution"]; ok {
		if resolution, ok := parseFloatParam(resolutionRaw); ok {
			if resolution < 0.1 {
				t.logger.Debug("resolution below minimum, clamping to 0.1",
					slog.String("tool", "find_communities"),
					slog.Float64("requested", resolution),
				)
				resolution = 0.1
			} else if resolution > 5.0 {
				t.logger.Debug("resolution above maximum, clamping to 5.0",
					slog.String("tool", "find_communities"),
					slog.Float64("requested", resolution),
				)
				resolution = 5.0
			}
			p.Resolution = resolution
		}
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			if top < 1 {
				t.logger.Debug("top below minimum, clamping to 1",
					slog.String("tool", "find_communities"),
					slog.Int("requested", top),
				)
				top = 1
			} else if top > 50 {
				t.logger.Debug("top above maximum, clamping to 50",
					slog.String("tool", "find_communities"),
					slog.Int("requested", top),
				)
				top = 50
			}
			p.Top = top
		}
	}

	// Extract show_cross_edges (optional)
	if showCrossEdgesRaw, ok := params["show_cross_edges"]; ok {
		if showCrossEdges, ok := parseBoolParam(showCrossEdgesRaw); ok {
			p.ShowCrossEdges = showCrossEdges
		}
	}

	return p, nil
}

// identifyCrossPackageCommunities returns IDs of communities that span multiple packages.
func (t *findCommunitiesTool) identifyCrossPackageCommunities(communities []graph.Community) []int {
	var crossPkgIDs []int

	for _, comm := range communities {
		pkgs := make(map[string]bool)
		for _, nodeID := range comm.Nodes {
			pkg := t.extractPackageFromNodeID(nodeID)
			if pkg != "" {
				pkgs[pkg] = true
			}
		}
		if len(pkgs) > 1 {
			crossPkgIDs = append(crossPkgIDs, comm.ID)
		}
	}

	return crossPkgIDs
}

// extractPackageFromNodeID extracts the package path from a node ID.
// Node ID format: "pkg/subpkg/file.go:line:symbol"
func (t *findCommunitiesTool) extractPackageFromNodeID(nodeID string) string {
	colonIdx := strings.Index(nodeID, ":")
	if colonIdx == -1 {
		return ""
	}
	pathPart := nodeID[:colonIdx]
	lastSlash := strings.LastIndex(pathPart, "/")
	if lastSlash == -1 {
		return ""
	}
	return pathPart[:lastSlash]
}

// calculateCrossCommunityEdges returns edges between communities.
func (t *findCommunitiesTool) calculateCrossCommunityEdges(communities []graph.Community) []CrossEdgeInfo {
	if len(communities) < 2 {
		return nil
	}

	var result []CrossEdgeInfo

	for i, commI := range communities {
		if commI.ExternalEdges == 0 {
			continue
		}
		for j := i + 1; j < len(communities); j++ {
			commJ := communities[j]
			if commJ.ExternalEdges == 0 {
				continue
			}
			estimatedEdges := (commI.ExternalEdges + commJ.ExternalEdges) / (len(communities) - 1)
			if estimatedEdges > 0 {
				result = append(result, CrossEdgeInfo{
					FromCommunity: commI.ID,
					ToCommunity:   commJ.ID,
					Count:         estimatedEdges,
				})
			}
		}
	}

	// Sort by count (most edges first)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	// Limit to top 10 edges
	if len(result) > 10 {
		result = result[:10]
	}

	return result
}

// getModularityQuality returns a quality label for the modularity score.
func (t *findCommunitiesTool) getModularityQuality(modularity float64) string {
	switch {
	case modularity < 0.3:
		return "weak"
	case modularity < 0.5:
		return "moderate"
	case modularity < 0.7:
		return "good"
	default:
		return "strong"
	}
}

// buildOutput creates the typed output struct.
func (t *findCommunitiesTool) buildOutput(
	result *graph.CommunityResult,
	filtered []graph.Community,
	crossPkgIDs []int,
	crossEdges []CrossEdgeInfo,
) FindCommunitiesOutput {
	crossPkgSet := make(map[int]bool)
	for _, id := range crossPkgIDs {
		crossPkgSet[id] = true
	}

	communities := make([]CommunityInfo, 0, len(filtered))
	for _, comm := range filtered {
		// Get packages in this community
		pkgs := make(map[string]int)
		for _, nodeID := range comm.Nodes {
			pkg := t.extractPackageFromNodeID(nodeID)
			if pkg != "" {
				pkgs[pkg]++
			} else if comm.DominantPackage != "" {
				pkgs[comm.DominantPackage]++
			}
		}
		pkgList := make([]string, 0, len(pkgs))
		for pkg := range pkgs {
			pkgList = append(pkgList, pkg)
		}

		// Build member list (limit to 10)
		members := make([]CommunityMember, 0, minInt(len(comm.Nodes), 10))
		limit := minInt(len(comm.Nodes), 10)
		for i := 0; i < limit; i++ {
			members = append(members, CommunityMember{ID: comm.Nodes[i]})
		}

		communities = append(communities, CommunityInfo{
			ID:              comm.ID,
			Size:            len(comm.Nodes),
			DominantPackage: comm.DominantPackage,
			Packages:        pkgList,
			IsCrossPackage:  crossPkgSet[comm.ID],
			Connectivity:    comm.Connectivity,
			InternalEdges:   comm.InternalEdges,
			ExternalEdges:   comm.ExternalEdges,
			Members:         members,
		})
	}

	return FindCommunitiesOutput{
		Modularity:              result.Modularity,
		ModularityQuality:       t.getModularityQuality(result.Modularity),
		CommunityCount:          len(filtered),
		TotalCommunities:        len(result.Communities),
		Algorithm:               "Leiden",
		Converged:               result.Converged,
		Iterations:              result.Iterations,
		NodeCount:               result.NodeCount,
		EdgeCount:               result.EdgeCount,
		Communities:             communities,
		CrossPackageCommunities: crossPkgIDs,
		CrossCommunityEdges:     crossEdges,
	}
}

// formatText creates a human-readable text summary.
func (t *findCommunitiesTool) formatText(
	result *graph.CommunityResult,
	filtered []graph.Community,
	crossPkgIDs []int,
	crossEdges []CrossEdgeInfo,
) string {
	var sb strings.Builder

	if len(filtered) == 0 {
		sb.WriteString("No communities found matching criteria.\n")
		return sb.String()
	}

	// Header
	quality := t.getModularityQuality(result.Modularity)
	sb.WriteString(fmt.Sprintf("Detected %d communities (modularity: %.2f - %s structure):\n\n",
		len(filtered), result.Modularity, quality))

	// Build cross-package set
	crossPkgSet := make(map[int]bool)
	for _, id := range crossPkgIDs {
		crossPkgSet[id] = true
	}

	// Communities
	for i, comm := range filtered {
		if crossPkgSet[comm.ID] {
			sb.WriteString(fmt.Sprintf("Community %d (%d symbols) [REFACTOR] - spans multiple packages\n",
				i+1, len(comm.Nodes)))
		} else {
			sb.WriteString(fmt.Sprintf("Community %d (%d symbols) - %s\n",
				i+1, len(comm.Nodes), comm.DominantPackage))
		}

		sb.WriteString(fmt.Sprintf("  Connectivity: %.0f%% internal edges\n",
			comm.Connectivity*100))

		// Sample members (limit to 5)
		limit := minInt(len(comm.Nodes), 5)
		sb.WriteString("  Members: ")
		for j := 0; j < limit; j++ {
			if j > 0 {
				sb.WriteString(", ")
			}
			nodeID := comm.Nodes[j]
			parts := strings.Split(nodeID, ":")
			if len(parts) > 0 {
				sb.WriteString(parts[len(parts)-1])
			} else {
				sb.WriteString(nodeID)
			}
		}
		if len(comm.Nodes) > limit {
			sb.WriteString(fmt.Sprintf(" (+%d more)", len(comm.Nodes)-limit))
		}
		sb.WriteString("\n")

		if crossPkgSet[comm.ID] {
			sb.WriteString("  -> These symbols are tightly coupled but in different packages\n")
		}

		sb.WriteString("\n")
	}

	// Cross-community edges
	if len(crossEdges) > 0 {
		sb.WriteString("Cross-community edges (abstraction seams):\n")
		for _, edge := range crossEdges {
			sb.WriteString(fmt.Sprintf("  Community %d -> %d: %d edges\n",
				edge.FromCommunity, edge.ToCommunity, edge.Count))
		}
		sb.WriteString("\n")
	}

	// Summary
	sb.WriteString("Summary:\n")
	if len(crossPkgIDs) > 0 {
		sb.WriteString(fmt.Sprintf("  - %d cross-package communities identified (refactoring candidates)\n",
			len(crossPkgIDs)))
	}
	sb.WriteString(fmt.Sprintf("  - Modularity %.2f indicates %s overall structure\n",
		result.Modularity, quality))
	if result.Converged {
		sb.WriteString(fmt.Sprintf("  - Algorithm converged in %d iterations\n", result.Iterations))
	}

	return sb.String()
}
