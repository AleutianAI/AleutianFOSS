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
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// =============================================================================
// find_module_api Tool (GR-18b) - Typed Implementation
// =============================================================================

var findModuleAPITracer = otel.Tracer("tools.find_module_api")

// E1: Community detection cache with LRU eviction
type communityCacheEntry struct {
	result    *graph.CommunityResult
	timestamp int64 // Unix milliseconds when cached
}

type communityCache struct {
	mu      sync.RWMutex
	entries map[int64]communityCacheEntry // key: graph.BuiltAtMilli
	lru     []int64                       // LRU order (most recent first)
	maxSize int
}

// newCommunityCache creates a new LRU cache for community detection results.
//
// Description:
//
//	Initializes an empty cache with configurable maximum size. Used to avoid
//	redundant community detection computations on unchanged graphs. Entries
//	are evicted in LRU order when capacity is exceeded.
//
// Inputs:
//   - maxSize: Maximum number of cached entries before LRU eviction. Must be > 0.
//
// Outputs:
//   - *communityCache: Initialized cache with empty entries and LRU list. Never nil.
//
// Thread Safety:
//
//	The returned cache is safe for concurrent use via internal sync.RWMutex.
//
// Example:
//
//	cache := newCommunityCache(10) // Max 10 cached graph generations
//	result := cache.get(graphBuiltAtMilli) // Returns nil if not cached
//	cache.put(graphBuiltAtMilli, communities) // Store result
//
// Assumptions:
//   - maxSize > 0 (not validated, caller responsibility)
//   - Graph generations are stable (BuiltAtMilli doesn't change for same graph)
func newCommunityCache(maxSize int) *communityCache {
	return &communityCache{
		entries: make(map[int64]communityCacheEntry),
		lru:     make([]int64, 0, maxSize),
		maxSize: maxSize,
	}
}

// get retrieves a cached result if available.
// Returns nil if not found or expired.
func (c *communityCache) get(graphBuiltAt int64) *graph.CommunityResult {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, found := c.entries[graphBuiltAt]
	if !found {
		return nil
	}

	return entry.result
}

// put stores a result in the cache with LRU eviction.
func (c *communityCache) put(graphBuiltAt int64, result *graph.CommunityResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Add/update entry
	c.entries[graphBuiltAt] = communityCacheEntry{
		result:    result,
		timestamp: time.Now().UnixMilli(),
	}

	// Update LRU order: move this key to front
	c.lru = c.removeLRU(graphBuiltAt)
	c.lru = append([]int64{graphBuiltAt}, c.lru...)

	// Evict oldest if over capacity
	if len(c.lru) > c.maxSize {
		evictKey := c.lru[len(c.lru)-1]
		delete(c.entries, evictKey)
		c.lru = c.lru[:len(c.lru)-1]
	}
}

// removeLRU removes a key from LRU list (helper for put).
func (c *communityCache) removeLRU(key int64) []int64 {
	result := make([]int64, 0, len(c.lru))
	for _, k := range c.lru {
		if k != key {
			result = append(result, k)
		}
	}
	return result
}

// FindModuleAPIParams contains the validated input parameters.
type FindModuleAPIParams struct {
	// CommunityID is the specific community to analyze.
	// -1 means analyze all communities (default).
	CommunityID int

	// Top is the number of communities to analyze.
	// Default: 10, Max: 50.
	Top int

	// MinCommunitySize is the minimum community size to analyze.
	// Default: 3.
	MinCommunitySize int
}

// ToolName returns the tool name for TypedParams interface.
func (p FindModuleAPIParams) ToolName() string { return "find_module_api" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindModuleAPIParams) ToMap() map[string]any {
	return map[string]any{
		"community_id":       p.CommunityID,
		"top":                p.Top,
		"min_community_size": p.MinCommunitySize,
	}
}

// FindModuleAPIOutput contains the structured result.
type FindModuleAPIOutput struct {
	// Modules is the list of analyzed modules.
	Modules []ModuleAPI `json:"modules"`

	// Summary contains aggregate statistics.
	Summary ModuleAPISummary `json:"summary"`

	// Message is an optional status message.
	Message string `json:"message,omitempty"`
}

// ModuleAPI represents a single code module (community) with its API surface.
type ModuleAPI struct {
	// CommunityID is the community identifier.
	CommunityID int `json:"community_id"`

	// Name is a human-readable module name.
	Name string `json:"name"`

	// DominantPackage is the package containing most nodes.
	DominantPackage string `json:"dominant_package"`

	// Size is the number of nodes in this community.
	Size int `json:"size"`

	// APISurface is the list of entry point functions.
	APISurface []APIFunction `json:"api_surface"`

	// InternalOnly is the list of internal-only functions (not externally callable).
	InternalOnly []string `json:"internal_only,omitempty"`

	// Note is an optional message about this module.
	Note string `json:"note,omitempty"`
}

// APIFunction represents a single API entry point.
type APIFunction struct {
	// ID is the full node identifier.
	ID string `json:"id"`

	// Name is the function name extracted from the ID.
	Name string `json:"name"`

	// ExternalCallers is the count of external nodes calling this function.
	ExternalCallers int `json:"external_callers"`

	// InternalNodesDominated is the count of internal nodes this function dominates.
	InternalNodesDominated int `json:"internal_nodes_dominated"`

	// Coverage is the fraction of the module dominated by this function (0.0 to 1.0).
	Coverage float64 `json:"coverage"`

	// Description is a human-readable explanation.
	Description string `json:"description"`
}

// ModuleAPISummary contains aggregate statistics.
type ModuleAPISummary struct {
	// CommunitiesAnalyzed is the number of communities analyzed.
	CommunitiesAnalyzed int `json:"communities_analyzed"`

	// CommunitiesFiltered is the number filtered by min_size.
	CommunitiesFiltered int `json:"communities_filtered"`

	// TotalAPIFunctions is the total count of API functions across all modules.
	TotalAPIFunctions int `json:"total_api_functions"`

	// AvgAPISize is the average number of API functions per module.
	AvgAPISize float64 `json:"avg_api_size"`
}

// findModuleAPITool finds the API surface of code modules.
type findModuleAPITool struct {
	analytics *graph.GraphAnalytics
	graph     *graph.Graph
	index     *index.SymbolIndex
	logger    *slog.Logger
	cache     *communityCache // E1: Per-instance cache for community detection
}

// NewFindModuleAPITool creates a new find_module_api tool.
//
// Description:
//
//	Creates a tool for finding the public API surface of code modules by combining
//	community detection with dominator analysis. Identifies mandatory entry points
//	to each detected module.
//
// Inputs:
//   - analytics: GraphAnalytics instance for community detection and dominators. Must not be nil.
//   - g: Graph for edge traversal. Must not be nil.
//   - idx: SymbolIndex for symbol lookups (can be nil for basic operation).
//
// Outputs:
//   - Tool: The configured find_module_api tool.
//
// Thread Safety: Safe for concurrent use after construction.
//
// Limitations:
//   - Requires community detection and dominator computation (can be expensive)
//   - Coverage calculation limited to dominator-reachable nodes
//   - Module names are heuristic-based (dominant package)
func NewFindModuleAPITool(analytics *graph.GraphAnalytics, g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findModuleAPITool{
		analytics: analytics,
		graph:     g,
		index:     idx,
		logger:    slog.Default(),
		cache:     newCommunityCache(10), // E1: LRU cache with max 10 entries
	}
}

func (t *findModuleAPITool) Name() string {
	return "find_module_api"
}

func (t *findModuleAPITool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findModuleAPITool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_module_api",
		Description: "Find the public API surface of code modules. " +
			"Combines community detection with dominator analysis to identify " +
			"functions that serve as entry points to each module.",
		Parameters: map[string]ParamDef{
			"community_id": {
				Type:        ParamTypeInt,
				Description: "Specific community ID to analyze (default: all, -1 means all)",
				Required:    false,
			},
			"top": {
				Type:        ParamTypeInt,
				Description: "Number of communities to analyze (default: 10, max: 50)",
				Required:    false,
			},
			"min_community_size": {
				Type:        ParamTypeInt,
				Description: "Minimum community size to analyze (default: 3)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    81,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     60 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"module API", "public API", "entry points",
				"module interface", "module boundary",
				"how to use module", "module surface",
			},
			UseWhen: "User asks about module interfaces, public APIs, " +
				"or how to interact with different parts of the codebase.",
			AvoidWhen: "User asks about all entry points globally. " +
				"Use find_entry_points for that.",
		},
	}
}

// Execute runs the find_module_api tool.
func (t *findModuleAPITool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

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

	// Start span with context
	ctx, span := findModuleAPITracer.Start(ctx, "findModuleAPITool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_module_api"),
			attribute.Int("community_id", p.CommunityID),
			attribute.Int("top", p.Top),
			attribute.Int("min_community_size", p.MinCommunitySize),
		),
	)
	defer span.End()

	// Check context cancellation before expensive operation
	if err := ctx.Err(); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Step 1: Detect communities
	communities, commTrace, err := t.detectCommunities(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// E4: Add span event for failure observability
		span.AddEvent("community_detection_failed", trace.WithAttributes(
			attribute.String("error", err.Error()),
		))
		return &Result{
			Success: false,
			Error:   fmt.Sprintf("failed to detect communities for module API analysis: %v", err),
		}, nil
	}

	if len(communities.Communities) == 0 {
		// No communities detected - valid but empty result
		output := FindModuleAPIOutput{
			Modules: []ModuleAPI{},
			Summary: ModuleAPISummary{},
			Message: "No communities detected in graph",
		}
		trace := crs.NewTraceStepBuilder().
			WithAction("tool_module_api").
			WithTool("find_module_api").
			WithDuration(time.Since(start)).
			WithMetadata("communities_detected", "0").
			Build()
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: "No communities detected in graph.\n",
			TokensUsed: 50,
			TraceStep:  &trace,
			Duration:   time.Since(start),
		}, nil
	}

	span.AddEvent("community_detection_complete", trace.WithAttributes(
		attribute.Int("communities", len(communities.Communities)),
	))

	t.logger.Debug("communities detected",
		slog.String("tool", "find_module_api"),
		slog.Int("count", len(communities.Communities)),
	)

	// Step 2: Filter and select communities
	communitiesToAnalyze, filteredCount := t.filterAndSelectCommunities(communities, p)

	if len(communitiesToAnalyze) == 0 {
		// All communities filtered out
		maxSize := 0
		for _, comm := range communities.Communities {
			if len(comm.Nodes) > maxSize {
				maxSize = len(comm.Nodes)
			}
		}
		message := fmt.Sprintf(
			"All %d communities filtered by min_size=%d. Largest community has %d nodes. "+
				"Try min_community_size=%d",
			len(communities.Communities),
			p.MinCommunitySize,
			maxSize,
			maxSize,
		)
		output := FindModuleAPIOutput{
			Modules: []ModuleAPI{},
			Summary: ModuleAPISummary{
				CommunitiesFiltered: filteredCount,
			},
			Message: message,
		}
		span.SetAttributes(attribute.String("result", "all_filtered"))
		t.logger.Warn("all communities filtered",
			slog.Int("min_size", p.MinCommunitySize),
			slog.Int("largest", maxSize),
		)
		trace := crs.NewTraceStepBuilder().
			WithAction("tool_module_api").
			WithTool("find_module_api").
			WithDuration(time.Since(start)).
			WithMetadata("communities_detected", strconv.Itoa(len(communities.Communities))).
			WithMetadata("communities_filtered", strconv.Itoa(filteredCount)).
			WithMetadata("min_community_size", strconv.Itoa(p.MinCommunitySize)).
			Build()
		return &Result{
			Success:    true,
			Output:     output,
			OutputText: message + "\n",
			TokensUsed: 100,
			TraceStep:  &trace,
			Duration:   time.Since(start),
		}, nil
	}

	span.SetAttributes(attribute.Int("communities_to_analyze", len(communitiesToAnalyze)))

	// Step 3: Analyze each community
	modules := make([]ModuleAPI, 0, len(communitiesToAnalyze))
	totalAPICount := 0

	for _, comm := range communitiesToAnalyze {
		// Check context cancellation
		if ctx.Err() != nil {
			span.RecordError(ctx.Err())
			t.logger.Info("context cancelled, returning partial results",
				slog.Int("modules_analyzed", len(modules)),
			)
			break
		}

		span.AddEvent("community_analysis_start", trace.WithAttributes(
			attribute.Int("community_id", comm.ID),
			attribute.Int("size", len(comm.Nodes)),
		))

		module, err := t.analyzeCommunity(ctx, comm)
		if err != nil {
			// Log warning but continue with other communities (graceful degradation)
			t.logger.Warn("failed to analyze community, skipping",
				slog.Int("community_id", comm.ID),
				slog.String("error", err.Error()),
			)
			continue
		}

		modules = append(modules, module)
		totalAPICount += len(module.APISurface)

		span.AddEvent("community_analysis_complete", trace.WithAttributes(
			attribute.Int("community_id", comm.ID),
			attribute.Int("api_functions", len(module.APISurface)),
		))
	}

	// Step 4: Build summary
	avgAPISize := 0.0
	if len(modules) > 0 {
		avgAPISize = float64(totalAPICount) / float64(len(modules))
	}

	summary := ModuleAPISummary{
		CommunitiesAnalyzed: len(modules),
		CommunitiesFiltered: filteredCount,
		TotalAPIFunctions:   totalAPICount,
		AvgAPISize:          avgAPISize,
	}

	output := FindModuleAPIOutput{
		Modules: modules,
		Summary: summary,
	}

	// Format text output
	outputText := t.formatText(modules, summary)

	span.SetAttributes(
		attribute.Int("modules_analyzed", len(modules)),
		attribute.Int("total_api_functions", totalAPICount),
		attribute.String("trace_action", commTrace.Action),
	)

	t.logger.Info("find_module_api completed",
		slog.String("tool", "find_module_api"),
		slog.Int("modules_analyzed", len(modules)),
		slog.Int("total_api_functions", totalAPICount),
		slog.Duration("duration", time.Since(start)),
	)

	// Build comprehensive TraceStep including sub-operation metadata
	finalTrace := crs.NewTraceStepBuilder().
		WithAction("tool_module_api").
		WithTool("find_module_api").
		WithDuration(time.Since(start)).
		WithMetadata("communities_detected", strconv.Itoa(len(communities.Communities))).
		WithMetadata("communities_analyzed", strconv.Itoa(len(modules))).
		WithMetadata("communities_filtered", strconv.Itoa(summary.CommunitiesFiltered)).
		WithMetadata("total_api_functions", strconv.Itoa(totalAPICount)).
		Build()

	return &Result{
		Success:    true,
		Output:     output,
		OutputText: outputText,
		TokensUsed: estimateTokens(outputText),
		TraceStep:  &finalTrace,
		Duration:   time.Since(start),
	}, nil
}

// parseParams validates and extracts typed parameters from the raw map.
func (t *findModuleAPITool) parseParams(params map[string]any) (FindModuleAPIParams, error) {
	p := FindModuleAPIParams{
		CommunityID:      -1, // Default: all communities
		Top:              10, // Default: top 10
		MinCommunitySize: 3,  // Default: min 3 nodes
	}

	// Extract community_id (optional)
	if commIDRaw, ok := params["community_id"]; ok {
		if commID, ok := parseIntParam(commIDRaw); ok {
			p.CommunityID = commID
		}
	}

	// Extract top (optional)
	if topRaw, ok := params["top"]; ok {
		if top, ok := parseIntParam(topRaw); ok {
			// Clamp to [1, 50]
			if top < 1 {
				top = 1
			}
			if top > 50 {
				top = 50
			}
			p.Top = top
		}
	}

	// Extract min_community_size (optional)
	if minSizeRaw, ok := params["min_community_size"]; ok {
		if minSize, ok := parseIntParam(minSizeRaw); ok {
			// Clamp to >= 1
			if minSize < 1 {
				minSize = 1
			}
			p.MinCommunitySize = minSize
		}
	}

	return p, nil
}

// detectCommunities runs community detection with LRU caching.
// E1: Cache results keyed by graph.BuiltAtMilli for performance.
func (t *findModuleAPITool) detectCommunities(ctx context.Context) (*graph.CommunityResult, crs.TraceStep, error) {
	start := time.Now()

	// E1: Check cache first using graph generation timestamp
	graphBuiltAt := t.graph.BuiltAtMilli
	if cachedResult := t.cache.get(graphBuiltAt); cachedResult != nil {
		// Cache hit
		t.logger.Debug("community detection cache hit",
			slog.Int64("graph_built_at", graphBuiltAt),
		)

		// Add span attribute for cache hit
		span := trace.SpanFromContext(ctx)
		span.AddEvent("community_cache_hit", trace.WithAttributes(
			attribute.Int64("graph_built_at", graphBuiltAt),
		))

		traceStep := crs.TraceStep{
			Action:   "analytics_communities_cached",
			Tool:     "DetectCommunities",
			Target:   "",
			Duration: time.Since(start),
		}
		return cachedResult, traceStep, nil
	}

	// Cache miss - compute fresh
	t.logger.Debug("community detection cache miss, computing",
		slog.Int64("graph_built_at", graphBuiltAt),
	)

	// Add span attribute for cache miss
	span := trace.SpanFromContext(ctx)
	span.AddEvent("community_cache_miss", trace.WithAttributes(
		attribute.Int64("graph_built_at", graphBuiltAt),
	))

	// Use default Leiden options
	opts := graph.DefaultLeidenOptions()

	result, err := t.analytics.DetectCommunities(ctx, opts)
	if err != nil {
		return nil, crs.TraceStep{}, fmt.Errorf("detect communities: %w", err)
	}

	// E1: Store in cache
	t.cache.put(graphBuiltAt, result)

	// Build TraceStep
	traceStep := crs.TraceStep{
		Action:   "analytics_communities",
		Tool:     "DetectCommunities",
		Target:   "",
		Duration: time.Since(start),
	}

	return result, traceStep, nil
}

// filterAndSelectCommunities filters by size and selects top N or specific community.
func (t *findModuleAPITool) filterAndSelectCommunities(
	communities *graph.CommunityResult,
	params FindModuleAPIParams,
) ([]graph.Community, int) {
	filteredCount := 0

	// If specific community requested, return just that one
	if params.CommunityID != -1 {
		for _, comm := range communities.Communities {
			if comm.ID == params.CommunityID {
				if len(comm.Nodes) >= params.MinCommunitySize {
					return []graph.Community{comm}, 0
				}
				// Requested community too small
				return []graph.Community{}, 1
			}
		}
		// Community not found
		return []graph.Community{}, 0
	}

	// Filter by min size
	filtered := make([]graph.Community, 0, len(communities.Communities))
	for _, comm := range communities.Communities {
		if len(comm.Nodes) >= params.MinCommunitySize {
			filtered = append(filtered, comm)
		} else {
			filteredCount++
		}
	}

	// Sort by size descending (largest communities first)
	sort.Slice(filtered, func(i, j int) bool {
		return len(filtered[i].Nodes) > len(filtered[j].Nodes)
	})

	// Take top N
	if len(filtered) > params.Top {
		filteredCount += len(filtered) - params.Top
		filtered = filtered[:params.Top]
	}

	return filtered, filteredCount
}

// analyzeCommunity analyzes a single community to find its API surface.
func (t *findModuleAPITool) analyzeCommunity(ctx context.Context, comm graph.Community) (ModuleAPI, error) {
	// E4: Add span for community analysis
	ctx, span := findModuleAPITracer.Start(ctx, "analyzeCommunity",
		trace.WithAttributes(
			attribute.Int("community_id", comm.ID),
			attribute.Int("size", len(comm.Nodes)),
		),
	)
	defer span.End()

	// Find external callers
	externalCallers, err := t.findExternalCallers(comm)
	if err != nil {
		// E4: Add span event for failure observability
		span.RecordError(err)
		span.AddEvent("find_external_callers_failed", trace.WithAttributes(
			attribute.String("error", err.Error()),
		))
		return ModuleAPI{}, fmt.Errorf("find external callers: %w", err)
	}

	// If no external callers, this is an isolated module
	if len(externalCallers) == 0 {
		return ModuleAPI{
			CommunityID:  comm.ID,
			Name:         t.generateModuleName(comm),
			Size:         len(comm.Nodes),
			APISurface:   []APIFunction{},
			InternalOnly: comm.Nodes,
			Note:         "Isolated module - no external callers detected",
		}, nil
	}

	// Compute API surface
	apiSurface, err := t.computeAPISurface(ctx, comm, externalCallers)
	if err != nil {
		// E4: Add span event for failure observability
		span.RecordError(err)
		span.AddEvent("compute_api_surface_failed", trace.WithAttributes(
			attribute.String("error", err.Error()),
		))
		return ModuleAPI{}, fmt.Errorf("compute API surface: %w", err)
	}

	// Find dominant package
	dominantPkg := t.findDominantPackage(comm.Nodes)

	// Separate internal-only functions
	internalOnly := t.findInternalOnlyFunctions(comm.Nodes, apiSurface)

	return ModuleAPI{
		CommunityID:     comm.ID,
		Name:            t.generateModuleName(comm),
		DominantPackage: dominantPkg,
		Size:            len(comm.Nodes),
		APISurface:      apiSurface,
		InternalOnly:    internalOnly,
	}, nil
}

// findExternalCallers finds nodes outside the community that call into it.
func (t *findModuleAPITool) findExternalCallers(comm graph.Community) (map[string][]string, error) {
	// Build membership set for O(1) lookup
	memberSet := make(map[string]bool, len(comm.Nodes))
	for _, m := range comm.Nodes {
		memberSet[m] = true
	}

	externalCallers := make(map[string][]string) // internal node → []external callers

	// For each node in community, check incoming edges
	for _, nodeID := range comm.Nodes {
		node, exists := t.graph.GetNode(nodeID)
		if !exists {
			continue
		}

		// Check incoming edges from the node
		for _, edge := range node.Incoming {
			fromID := edge.FromID
			// If caller is NOT in this community, it's external
			if !memberSet[fromID] {
				externalCallers[nodeID] = append(externalCallers[nodeID], fromID)
			}
		}
	}

	return externalCallers, nil
}

// computeAPISurface computes the API surface for a community.
func (t *findModuleAPITool) computeAPISurface(
	ctx context.Context,
	comm graph.Community,
	externalCallers map[string][]string,
) ([]APIFunction, error) {
	apiSurface := make([]APIFunction, 0, len(externalCallers))

	for internalNode, callers := range externalCallers {
		// Compute coverage: what fraction of the community does this entry point dominate?
		// For efficiency, we compute dominators on the community subgraph (E2 enhancement)
		coverage, dominated, err := t.computeCoverage(ctx, comm, internalNode)
		if err != nil {
			// Log warning but continue with zero coverage (graceful degradation)
			t.logger.Warn("failed to compute coverage for API function",
				slog.String("node", internalNode),
				slog.String("error", err.Error()),
			)
			// E4: Add span event for partial failure observability
			span := trace.SpanFromContext(ctx)
			span.AddEvent("coverage_computation_failed", trace.WithAttributes(
				attribute.String("node", internalNode),
				attribute.String("error", err.Error()),
			))
			coverage = 0.0
			dominated = 0
		}

		description := t.generateAPIDescription(coverage, len(callers))

		apiSurface = append(apiSurface, APIFunction{
			ID:                     internalNode,
			Name:                   extractNameFromNodeID(internalNode),
			ExternalCallers:        len(callers),
			InternalNodesDominated: dominated,
			Coverage:               coverage,
			Description:            description,
		})
	}

	// Sort by coverage descending (primary), external callers descending (secondary),
	// then by name ascending (tertiary) for deterministic CRS caching
	sort.Slice(apiSurface, func(i, j int) bool {
		if apiSurface[i].Coverage != apiSurface[j].Coverage {
			return apiSurface[i].Coverage > apiSurface[j].Coverage
		}
		if apiSurface[i].ExternalCallers != apiSurface[j].ExternalCallers {
			return apiSurface[i].ExternalCallers > apiSurface[j].ExternalCallers
		}
		// Tertiary sort by name for determinism (E3 enhancement)
		return apiSurface[i].Name < apiSurface[j].Name
	})

	return apiSurface, nil
}

// computeCoverage computes what fraction of the community is dominated by the given entry point.
// E2: Uses local subgraph dominators for efficiency (100x+ speedup on large graphs).
func (t *findModuleAPITool) computeCoverage(ctx context.Context, comm graph.Community, entryNode string) (float64, int, error) {
	// Handle empty community
	if len(comm.Nodes) == 0 {
		return 0.0, 0, nil
	}

	// E2: Extract community subgraph for efficient dominator computation
	subgraph, err := t.extractSubgraph(comm)
	if err != nil {
		// Fallback: use full graph if subgraph extraction fails
		t.logger.Warn("failed to extract subgraph, using full graph",
			slog.Int("community_id", comm.ID),
			slog.String("error", err.Error()),
		)
		return t.computeCoverageFullGraph(ctx, comm, entryNode)
	}

	// Compute dominators on the local subgraph
	subAnalytics := graph.NewGraphAnalytics(subgraph)
	domTree, err := subAnalytics.Dominators(ctx, entryNode)
	if err != nil {
		// Entry node might not be reachable in subgraph (disconnected component)
		// Return zero coverage for unreachable entry points
		t.logger.Debug("entry node not reachable in subgraph",
			slog.String("entry", entryNode),
			slog.Int("community_id", comm.ID),
		)
		return 0.0, 0, nil
	}

	// Get all nodes dominated by the entry point (already filtered to subgraph)
	dominated := domTree.DominatedBy(entryNode)

	// All dominated nodes are within the community (by construction)
	internalDominated := len(dominated)

	coverage := float64(internalDominated) / float64(len(comm.Nodes))
	return coverage, internalDominated, nil
}

// computeCoverageFullGraph is the fallback implementation using the full graph.
//
// Description:
//
//	Computes coverage by running dominators on the full graph and filtering
//	results to community members. This is less efficient than E2's subgraph
//	approach (O(E) vs O(E_c)) but serves as a robust fallback when subgraph
//	extraction fails. Used to ensure graceful degradation.
//
// Inputs:
//   - ctx: Context for cancellation. Must not be nil.
//   - comm: Community to compute coverage for. Must have valid Nodes list.
//   - entryNode: Node ID to use as dominator root. Must be valid node in graph.
//
// Outputs:
//   - float64: Coverage ratio [0.0, 1.0] - fraction of community dominated by entry.
//   - int: Count of internal nodes dominated by entry node.
//   - error: Non-nil if dominator computation fails on full graph.
//
// Thread Safety: Safe for concurrent use (read-only graph access).
//
// Example:
//
//	coverage, dominated, err := t.computeCoverageFullGraph(ctx, community, "pkg/auth/Login")
//	// coverage might be 0.75 (75% of community dominated)
//	// dominated might be 15 (15 nodes out of 20)
//
// Performance:
//   - Time: O(E) - full graph dominators, then O(V_c) filtering
//   - Space: O(V) - full dominator tree in memory
//   - Slower than E2 subgraph approach, but more robust
//
// Limitations:
//   - Uses full graph (potentially thousands of nodes)
//   - Filters dominated set post-computation (less efficient)
//   - Entry node must be reachable from graph root
//
// Assumptions:
//   - Entry node exists in graph (no validation, will error if not found)
//   - Community nodes are subset of graph nodes
//   - Graph is frozen and immutable
//
// E2: Fallback when subgraph extraction fails.
func (t *findModuleAPITool) computeCoverageFullGraph(ctx context.Context, comm graph.Community, entryNode string) (float64, int, error) {
	// Compute dominators with entry node as root on full graph
	domTree, err := t.analytics.Dominators(ctx, entryNode)
	if err != nil {
		return 0.0, 0, fmt.Errorf("compute dominators from %s: %w", entryNode, err)
	}

	// Get all nodes dominated by the entry point
	dominated := domTree.DominatedBy(entryNode)

	// Build membership set for filtering
	memberSet := make(map[string]bool, len(comm.Nodes))
	for _, m := range comm.Nodes {
		memberSet[m] = true
	}

	// Count how many dominated nodes are in this community
	internalDominated := 0
	for _, d := range dominated {
		if memberSet[d] {
			internalDominated++
		}
	}

	coverage := float64(internalDominated) / float64(len(comm.Nodes))
	return coverage, internalDominated, nil
}

// extractSubgraph creates a subgraph containing only the community's nodes and internal edges.
// E2: Enables efficient dominator computation on large graphs.
func (t *findModuleAPITool) extractSubgraph(comm graph.Community) (*graph.HierarchicalGraph, error) {
	// Build membership set for O(1) lookup
	memberSet := make(map[string]bool, len(comm.Nodes))
	for _, m := range comm.Nodes {
		memberSet[m] = true
	}

	// Create new graph for subgraph
	subgraph := graph.NewGraph(fmt.Sprintf("community_%d_subgraph", comm.ID))

	// Add all community nodes to subgraph
	for _, nodeID := range comm.Nodes {
		node, exists := t.graph.GetNode(nodeID)
		if !exists {
			// Node missing from graph - skip but continue
			continue
		}

		// Add node to subgraph using its Symbol
		if node.Symbol != nil {
			_, err := subgraph.AddNode(node.Symbol)
			if err != nil {
				// Skip nodes that fail to add (shouldn't happen but be defensive)
				continue
			}
		}
	}

	// Add internal edges (both endpoints in community)
	for _, nodeID := range comm.Nodes {
		node, exists := t.graph.GetNode(nodeID)
		if !exists {
			continue
		}

		// Add outgoing edges where target is also in community
		for _, edge := range node.Outgoing {
			toID := edge.ToID
			if memberSet[toID] {
				// Both endpoints in community - add to subgraph
				subgraph.AddEdge(nodeID, toID, edge.Type, edge.Location)
			}
		}
	}

	// Freeze subgraph for analysis
	subgraph.Freeze()

	// Wrap as HierarchicalGraph for analytics
	hierarchical, err := graph.WrapGraph(subgraph)
	if err != nil {
		return nil, fmt.Errorf("wrap subgraph: %w", err)
	}

	return hierarchical, nil
}

// generateAPIDescription creates a human-readable description of an API function.
func (t *findModuleAPITool) generateAPIDescription(coverage float64, externalCallers int) string {
	var rank string
	switch {
	case coverage >= 0.8:
		rank = "Primary entry point"
	case coverage >= 0.5:
		rank = "Major entry point"
	case coverage >= 0.2:
		rank = "Secondary entry point"
	default:
		rank = "Minor entry point"
	}

	return fmt.Sprintf("%s - dominates %.0f%% of module, called by %d external nodes",
		rank, coverage*100, externalCallers)
}

// findDominantPackage finds the package containing the most nodes in the community.
func (t *findModuleAPITool) findDominantPackage(members []string) string {
	if len(members) == 0 {
		return "(unknown)"
	}

	packageCounts := make(map[string]int)

	for _, nodeID := range members {
		pkg := extractPackage(nodeID)
		packageCounts[pkg]++
	}

	maxCount := 0
	dominant := ""
	for pkg, count := range packageCounts {
		// Lexicographic tiebreaker for determinism
		if count > maxCount || (count == maxCount && pkg < dominant) {
			maxCount = count
			dominant = pkg
		}
	}

	if dominant == "" {
		return "(unknown)"
	}

	return dominant
}

// extractPackage extracts the package path from a node ID.
// Node ID format: "path/to/file.go:line:FuncName"
func extractPackage(nodeID string) string {
	if nodeID == "" {
		return "(unknown)"
	}

	// Split by ':'
	parts := strings.Split(nodeID, ":")
	if len(parts) == 0 {
		return "(unknown)"
	}

	// First part is the file path
	filePath := parts[0]

	// Extract directory (package) from file path
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash == -1 {
		return "(unknown)"
	}

	pkg := filePath[:lastSlash]
	if pkg == "" {
		return "(unknown)"
	}

	return pkg
}

// generateModuleName generates a human-readable name for the module.
// E5: Enhanced with semantic pattern recognition for common module types.
func (t *findModuleAPITool) generateModuleName(comm graph.Community) string {
	pkg := t.findDominantPackage(comm.Nodes)
	if pkg == "(unknown)" {
		return fmt.Sprintf("Module %d", comm.ID)
	}

	// Extract last component of package for pattern matching
	lastSlash := strings.LastIndex(pkg, "/")
	pkgName := pkg
	if lastSlash != -1 && lastSlash < len(pkg)-1 {
		pkgName = pkg[lastSlash+1:]
	}

	// E5: Pattern map for semantic module naming
	// Match against common patterns (case-insensitive, substring match)
	semanticName := matchSemanticPattern(pkgName)
	if semanticName != "" {
		return semanticName
	}

	// Fallback to package-based name
	return fmt.Sprintf("%s Module", pkgName)
}

// matchSemanticPattern matches package names against common semantic patterns.
//
// Description:
//
//	Performs case-insensitive substring matching against a curated list of
//	60+ common module patterns (auth, db, api, etc.) to generate human-readable
//	module names. Returns empty string if no pattern matches, allowing caller
//	to use fallback naming strategy.
//
// Inputs:
//   - pkgName: Package name to match (typically last component of package path).
//     Can be empty or contain any valid Go identifier characters.
//
// Outputs:
//   - string: Semantic name if pattern matches (e.g., "Authentication" for "auth"),
//     or empty string if no pattern found.
//
// Thread Safety: Safe for concurrent use (read-only data, no shared state).
//
// Example:
//
//	matchSemanticPattern("auth") // Returns "Authentication"
//	matchSemanticPattern("authz") // Returns "Authorization"
//	matchSemanticPattern("foobar") // Returns ""
//
// Limitations:
//   - Substring matching: "authenticator" will match "auth" pattern
//   - Pattern order matters: more specific patterns must come before general ones
//   - No fuzzy matching or edit distance (exact substring only)
//
// Assumptions:
//   - Package names use common English patterns
//   - Pattern list is comprehensive for typical Go codebases
//
// E5: Semantic pattern recognition for module naming.
func matchSemanticPattern(pkgName string) string {
	// Normalize for matching (lowercase)
	normalized := strings.ToLower(pkgName)

	// Pattern map: substring → semantic name
	// Order matters: more specific patterns first
	patterns := []struct {
		pattern  string
		semantic string
	}{
		// Infrastructure & Core (specific patterns first)
		{"authz", "Authorization"},
		{"permission", "Authorization"},
		{"authn", "Authentication"},
		{"auth", "Authentication"}, // Must come after authz/authn
		{"db", "Database"},
		{"database", "Database"},
		{"cache", "Caching"},
		{"storage", "Storage"},
		{"config", "Configuration"},
		{"setting", "Configuration"},

		// API & Communication
		{"api", "API"},
		{"rest", "REST API"},
		{"graphql", "GraphQL API"},
		{"grpc", "gRPC API"},
		{"http", "HTTP Server"},
		{"server", "HTTP Server"},
		{"handler", "Request Handlers"},
		{"router", "Routing"},
		{"middleware", "Middleware"},
		{"websocket", "WebSocket"},

		// Business Logic
		{"service", "Business Logic"},
		{"controller", "Controllers"},
		{"usecase", "Use Cases"},
		{"domain", "Domain Logic"},
		{"model", "Data Models"},
		{"entity", "Entities"},
		{"repository", "Data Access"},
		{"repo", "Data Access"},

		// Utilities & Support
		{"util", "Utilities"},
		{"helper", "Helpers"},
		{"common", "Common Utilities"},
		{"shared", "Shared Components"},
		{"lib", "Library"},
		{"pkg", "Package"},

		// Testing & Quality
		{"test", "Testing"},
		{"mock", "Test Mocks"},
		{"fixture", "Test Fixtures"},

		// Observability
		{"log", "Logging"},
		{"logger", "Logging"},
		{"metric", "Metrics"},
		{"trace", "Tracing"},
		{"monitor", "Monitoring"},

		// Security
		{"crypto", "Cryptography"},
		{"encrypt", "Encryption"},
		{"security", "Security"},
		{"jwt", "JWT Handling"},
		{"token", "Token Management"},

		// External Integration
		{"client", "External Client"},
		{"adapter", "External Adapter"},
		{"integration", "External Integration"},
		{"webhook", "Webhook Handler"},

		// Data Processing
		{"parser", "Parsing"},
		{"validator", "Validation"},
		{"serializer", "Serialization"},
		{"transformer", "Data Transformation"},
		{"processor", "Data Processing"},

		// UI/Frontend (if analyzing full-stack)
		{"ui", "User Interface"},
		{"view", "Views"},
		{"template", "Templates"},
		{"render", "Rendering"},
	}

	// Match first pattern that appears as substring
	for _, p := range patterns {
		if strings.Contains(normalized, p.pattern) {
			return p.semantic
		}
	}

	return ""
}

// findInternalOnlyFunctions returns functions that are internal-only (not in API surface).
func (t *findModuleAPITool) findInternalOnlyFunctions(members []string, apiSurface []APIFunction) []string {
	// Build set of API function IDs
	apiSet := make(map[string]bool, len(apiSurface))
	for _, api := range apiSurface {
		apiSet[api.ID] = true
	}

	// Find members not in API surface
	internalOnly := make([]string, 0)
	for _, m := range members {
		if !apiSet[m] {
			internalOnly = append(internalOnly, m)
		}
	}

	return internalOnly
}

// formatText creates a human-readable text summary.
func (t *findModuleAPITool) formatText(modules []ModuleAPI, summary ModuleAPISummary) string {
	// Pre-size buffer for efficiency
	estimatedSize := 200 + len(modules)*300
	var sb strings.Builder
	sb.Grow(estimatedSize)

	sb.WriteString("Module API Surface Analysis\n\n")

	if len(modules) == 0 {
		sb.WriteString("No modules found.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Analyzed %d modules\n", summary.CommunitiesAnalyzed))
	if summary.CommunitiesFiltered > 0 {
		sb.WriteString(fmt.Sprintf("Filtered %d small communities\n", summary.CommunitiesFiltered))
	}
	sb.WriteString(fmt.Sprintf("Total API functions: %d (avg %.1f per module)\n\n",
		summary.TotalAPIFunctions, summary.AvgAPISize))

	for i, module := range modules {
		sb.WriteString(fmt.Sprintf("Module %d: %s\n", i+1, module.Name))
		sb.WriteString(fmt.Sprintf("  Community ID: %d\n", module.CommunityID))
		sb.WriteString(fmt.Sprintf("  Size: %d functions\n", module.Size))
		if module.DominantPackage != "" {
			sb.WriteString(fmt.Sprintf("  Package: %s\n", module.DominantPackage))
		}

		if module.Note != "" {
			sb.WriteString(fmt.Sprintf("  Note: %s\n", module.Note))
		}

		if len(module.APISurface) > 0 {
			sb.WriteString("\n  API Surface:\n")
			for j, api := range module.APISurface {
				sb.WriteString(fmt.Sprintf("    %d. %s (%.0f%% coverage, %d external callers)\n",
					j+1, api.Name, api.Coverage*100, api.ExternalCallers))
				if api.Description != "" {
					sb.WriteString(fmt.Sprintf("       %s\n", api.Description))
				}
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}
