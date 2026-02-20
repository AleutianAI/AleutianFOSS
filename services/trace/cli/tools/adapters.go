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
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/explore"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// RegisterExploreTools registers all CB-20 explore tools with the registry.
//
// Description:
//
//	Registers adapters for the exploration tools from the explore package.
//	These tools require a graph and index to be initialized.
//
// Inputs:
//
//	registry - The tool registry
//	g - The code graph
//	idx - The symbol index
func RegisterExploreTools(registry *Registry, g *graph.Graph, idx *index.SymbolIndex) {
	// Level 0: Project overview (highest priority)
	registry.Register(NewGraphOverviewTool(g, idx))

	// Level 1: Package-level navigation
	registry.Register(NewExplorePackageTool(g, idx))
	registry.Register(NewListPackagesTool(idx))

	// Level 2: File-level exploration
	registry.Register(NewSummarizeFileTool(g, idx))

	// Level 3: Symbol-level tracing
	registry.Register(NewFindEntryPointsTool(g, idx))
	registry.Register(NewTraceDataFlowTool(g, idx))
	registry.Register(NewTraceErrorFlowTool(g, idx))
	registry.Register(NewBuildMinimalContextTool(g, idx))
	registry.Register(NewFindSimilarCodeTool(g, idx))
	registry.Register(NewFindConfigUsageTool(g, idx))

	// Level 4: Graph query tools (CB-30c Phase 4)
	// These expose graph query functions directly to the agent for answering
	// questions like "Find all functions that call X" without using Grep.
	registry.Register(NewFindCallersTool(g, idx))
	registry.Register(NewFindCalleesTool(g, idx))
	registry.Register(NewFindImplementationsTool(g, idx))
	registry.Register(NewFindSymbolTool(g, idx))
	registry.Register(NewGetCallChainTool(g, idx))
	registry.Register(NewFindReferencesTool(g, idx))

	// Level 5: Graph analytics tools (GR-02 to GR-05, GR-12/GR-13, GR-15)
	// These wrap GraphAnalytics for code quality insights.
	// Requires wrapping Graph into HierarchicalGraph for analytics.
	if g.IsFrozen() {
		hg, err := graph.WrapGraph(g)
		if err == nil && hg != nil {
			analytics := graph.NewGraphAnalytics(hg)
			registry.Register(NewFindHotspotsTool(analytics, idx))
			registry.Register(NewFindDeadCodeTool(analytics, idx))
			registry.Register(NewFindCyclesTool(analytics, idx))
			registry.Register(NewFindImportantTool(analytics, idx))           // GR-13: PageRank
			registry.Register(NewFindCommunitiesTool(analytics, idx))         // GR-15: Leiden
			registry.Register(NewFindArticulationPointsTool(analytics, idx))  // GR-17a: Articulation points
			registry.Register(NewFindDominatorsTool(analytics, idx))          // GR-17: Dominators
			registry.Register(NewFindLoopsTool(analytics, idx))               // GR-17e: Natural loops
			registry.Register(NewFindMergePointsTool(analytics, idx))         // GR-17d: Merge points
			registry.Register(NewFindCommonDependencyTool(analytics, idx))    // GR-17f: Common dependency
			registry.Register(NewFindControlDependenciesTool(analytics, idx)) // GR-17c: Control dependencies
			registry.Register(NewFindExtractableRegionsTool(analytics, idx))  // GR-17g: Extractable regions
			registry.Register(NewCheckReducibilityTool(analytics, idx))       // GR-17h: Check reducibility
			registry.Register(NewFindCriticalPathTool(analytics, idx))        // GR-18a: Critical path
			registry.Register(NewFindModuleAPITool(analytics, g, idx))        // GR-18b: Module API surface
			registry.Register(NewFindWeightedCriticalityTool(analytics, idx)) // GR-18c: Weighted criticality
		}
	}

	// find_path uses Graph directly (doesn't need HierarchicalGraph)
	registry.Register(NewFindPathTool(g, idx))
}

// ============================================================================
// find_entry_points Tool
// ============================================================================

// findEntryPointsTool wraps explore.EntryPointFinder.
type findEntryPointsTool struct {
	finder *explore.EntryPointFinder
}

// NewFindEntryPointsTool creates the find_entry_points tool.
func NewFindEntryPointsTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findEntryPointsTool{
		finder: explore.NewEntryPointFinder(g, idx),
	}
}

func (t *findEntryPointsTool) Name() string {
	return "find_entry_points"
}

func (t *findEntryPointsTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findEntryPointsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "find_entry_points",
		Description: "Discovers entry points in the codebase (main functions, HTTP handlers, CLI commands, tests, etc.)",
		Parameters: map[string]ParamDef{
			"type": {
				Type:        ParamTypeString,
				Description: "Type of entry point to find: 'main', 'handler', 'command', 'test', 'lambda', 'grpc', or 'all'",
				Required:    false,
				Default:     "all",
				Enum:        []any{"main", "handler", "command", "test", "lambda", "grpc", "all"},
			},
			"package": {
				Type:        ParamTypeString,
				Description: "Filter results to a specific package path",
				Required:    false,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of results to return",
				Required:    false,
				Default:     100,
			},
			"include_tests": {
				Type:        ParamTypeBool,
				Description: "Include test entry points in results",
				Required:    false,
				Default:     false,
			},
		},
		Category:    CategoryExploration,
		Priority:    90,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

func (t *findEntryPointsTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	opts := explore.DefaultEntryPointOptions()

	if typeStr, ok := params["type"].(string); ok && typeStr != "" {
		opts.Type = explore.EntryPointType(typeStr)
	}

	if pkg, ok := params["package"].(string); ok {
		opts.Package = pkg
	}

	if limit, ok := getIntParam(params, "limit"); ok {
		opts.Limit = limit
	}

	if includeTests, ok := params["include_tests"].(bool); ok {
		opts.IncludeTests = includeTests
	}

	result, err := t.finder.FindEntryPoints(ctx, opts)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// trace_data_flow Tool
// ============================================================================

// traceDataFlowTool wraps explore.DataFlowTracer.
type traceDataFlowTool struct {
	tracer *explore.DataFlowTracer
}

// NewTraceDataFlowTool creates the trace_data_flow tool.
func NewTraceDataFlowTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &traceDataFlowTool{
		tracer: explore.NewDataFlowTracer(g, idx),
	}
}

func (t *traceDataFlowTool) Name() string {
	return "trace_data_flow"
}

func (t *traceDataFlowTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *traceDataFlowTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "trace_data_flow",
		Description: "Traces data flow through function calls, identifying sources, transforms, and sinks",
		Parameters: map[string]ParamDef{
			"symbol_id": {
				Type:        ParamTypeString,
				Description: "The symbol ID to start tracing from",
				Required:    true,
			},
			"max_hops": {
				Type:        ParamTypeInt,
				Description: "Maximum depth to trace",
				Required:    false,
				Default:     5,
			},
		},
		Category:    CategoryExploration,
		Priority:    85,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     15 * time.Second,
	}
}

func (t *traceDataFlowTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	symbolID, ok := params["symbol_id"].(string)
	if !ok || symbolID == "" {
		return &Result{Success: false, Error: "symbol_id is required"}, nil
	}

	var opts []explore.ExploreOption
	if maxHops, ok := getIntParam(params, "max_hops"); ok {
		opts = append(opts, explore.WithMaxHops(maxHops))
	}

	result, err := t.tracer.TraceDataFlow(ctx, symbolID, opts...)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// trace_error_flow Tool
// ============================================================================

// traceErrorFlowTool wraps explore.ErrorFlowTracer.
type traceErrorFlowTool struct {
	tracer *explore.ErrorFlowTracer
}

// NewTraceErrorFlowTool creates the trace_error_flow tool.
func NewTraceErrorFlowTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &traceErrorFlowTool{
		tracer: explore.NewErrorFlowTracer(g, idx),
	}
}

func (t *traceErrorFlowTool) Name() string {
	return "trace_error_flow"
}

func (t *traceErrorFlowTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *traceErrorFlowTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "trace_error_flow",
		Description: "Traces error propagation paths through the codebase, identifying origins, handlers, and escapes",
		Parameters: map[string]ParamDef{
			"symbol_id": {
				Type:        ParamTypeString,
				Description: "The symbol ID to start tracing from",
				Required:    true,
			},
			"max_hops": {
				Type:        ParamTypeInt,
				Description: "Maximum depth to trace",
				Required:    false,
				Default:     5,
			},
		},
		Category:    CategoryExploration,
		Priority:    80,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     15 * time.Second,
	}
}

func (t *traceErrorFlowTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	symbolID, ok := params["symbol_id"].(string)
	if !ok || symbolID == "" {
		return &Result{Success: false, Error: "symbol_id is required"}, nil
	}

	var opts []explore.ExploreOption
	if maxHops, ok := getIntParam(params, "max_hops"); ok {
		opts = append(opts, explore.WithMaxHops(maxHops))
	}

	result, err := t.tracer.TraceErrorFlow(ctx, symbolID, opts...)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// build_minimal_context Tool
// ============================================================================

// buildMinimalContextTool wraps explore.MinimalContextBuilder.
type buildMinimalContextTool struct {
	builder *explore.MinimalContextBuilder
	index   *index.SymbolIndex // GR-47: For partial symbol name resolution
}

// NewBuildMinimalContextTool creates the build_minimal_context tool.
func NewBuildMinimalContextTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &buildMinimalContextTool{
		builder: explore.NewMinimalContextBuilder(g, idx),
		index:   idx,
	}
}

func (t *buildMinimalContextTool) Name() string {
	return "build_minimal_context"
}

func (t *buildMinimalContextTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *buildMinimalContextTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "build_minimal_context",
		Description: "Builds minimal context needed to understand a function, including types and key callees",
		Parameters: map[string]ParamDef{
			"symbol_id": {
				Type:        ParamTypeString,
				Description: "The symbol ID to build context for",
				Required:    true,
			},
			"token_budget": {
				Type:        ParamTypeInt,
				Description: "Maximum token budget for the context",
				Required:    false,
				Default:     4000,
			},
		},
		Category:    CategoryExploration,
		Priority:    95,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

func (t *buildMinimalContextTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	symbolID, ok := params["symbol_id"].(string)
	if !ok || symbolID == "" {
		return &Result{Success: false, Error: "symbol_id is required"}, nil
	}

	var opts []explore.ExploreOption
	if tokenBudget, ok := getIntParam(params, "token_budget"); ok {
		opts = append(opts, explore.WithTokenBudget(tokenBudget))
	}

	// GR-47 Fix: Try to resolve partial symbol names to full IDs
	// If symbolID doesn't contain ":" (not a full ID), try to find matching symbols
	resolvedID := symbolID
	if t.index != nil && !strings.Contains(symbolID, ":") {
		// This looks like a partial name (e.g., "SetupRoutes" instead of "pkg/routes.go:9:SetupRoutes")
		matches := t.index.GetByName(symbolID)
		if len(matches) == 1 {
			// Exact single match - use it
			resolvedID = matches[0].ID
			slog.Debug("GR-47: resolved partial symbol name to full ID",
				slog.String("partial", symbolID),
				slog.String("resolved", resolvedID),
			)
		} else if len(matches) > 1 {
			// Multiple matches - try to find the best one (prefer functions over types)
			for _, m := range matches {
				if m.Kind == ast.SymbolKindFunction || m.Kind == ast.SymbolKindMethod {
					resolvedID = m.ID
					slog.Debug("GR-47: resolved partial symbol name (multiple matches, chose function)",
						slog.String("partial", symbolID),
						slog.String("resolved", resolvedID),
						slog.Int("total_matches", len(matches)),
					)
					break
				}
			}
			// If no function found, use the first match
			if resolvedID == symbolID && len(matches) > 0 {
				resolvedID = matches[0].ID
				slog.Debug("GR-47: resolved partial symbol name (multiple matches, chose first)",
					slog.String("partial", symbolID),
					slog.String("resolved", resolvedID),
					slog.Int("total_matches", len(matches)),
				)
			}
		}
		// If no matches, we'll try with the original symbolID and let it fail naturally
	}

	result, err := t.builder.BuildMinimalContext(ctx, resolvedID, opts...)
	if err != nil {
		// Include helpful error message with the attempted resolution
		errMsg := err.Error()
		if resolvedID != symbolID {
			errMsg = fmt.Sprintf("%s (resolved %q to %q)", errMsg, symbolID, resolvedID)
		}
		return &Result{Success: false, Error: errMsg}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// find_similar_code Tool
// ============================================================================

// findSimilarCodeTool wraps explore.SimilarityEngine.
type findSimilarCodeTool struct {
	engine *explore.SimilarityEngine
}

// NewFindSimilarCodeTool creates the find_similar_code tool.
func NewFindSimilarCodeTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findSimilarCodeTool{
		engine: explore.NewSimilarityEngine(g, idx),
	}
}

func (t *findSimilarCodeTool) Name() string {
	return "find_similar_code"
}

func (t *findSimilarCodeTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findSimilarCodeTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "find_similar_code",
		Description: "Finds code structurally or semantically similar to a target symbol",
		Parameters: map[string]ParamDef{
			"symbol_id": {
				Type:        ParamTypeString,
				Description: "The symbol ID to find similar code for",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of similar code blocks to return",
				Required:    false,
				Default:     10,
			},
			"min_similarity": {
				Type:        ParamTypeFloat,
				Description: "Minimum similarity score (0.0-1.0)",
				Required:    false,
				Default:     0.5,
			},
		},
		Category:    CategoryExploration,
		Priority:    75,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     20 * time.Second,
	}
}

func (t *findSimilarCodeTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	symbolID, ok := params["symbol_id"].(string)
	if !ok || symbolID == "" {
		return &Result{Success: false, Error: "symbol_id is required"}, nil
	}

	var opts []explore.ExploreOption
	if limit, ok := getIntParam(params, "limit"); ok {
		opts = append(opts, explore.WithMaxNodes(limit))
	}

	result, err := t.engine.FindSimilarCode(ctx, symbolID, opts...)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	// Filter by minimum similarity if specified
	if minSim, ok := params["min_similarity"].(float64); ok && minSim > 0 {
		filtered := make([]explore.SimilarResult, 0)
		for _, r := range result.Results {
			if r.Similarity >= minSim {
				filtered = append(filtered, r)
			}
		}
		result.Results = filtered
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// summarize_file Tool
// ============================================================================

// summarizeFileTool wraps explore.FileSummarizer.
type summarizeFileTool struct {
	summarizer *explore.FileSummarizer
}

// NewSummarizeFileTool creates the summarize_file tool.
func NewSummarizeFileTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &summarizeFileTool{
		summarizer: explore.NewFileSummarizer(g, idx),
	}
}

func (t *summarizeFileTool) Name() string {
	return "summarize_file"
}

func (t *summarizeFileTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *summarizeFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "summarize_file",
		Description: "Provides a structured summary of a file including types, functions, and purpose",
		Parameters: map[string]ParamDef{
			"file_path": {
				Type:        ParamTypeString,
				Description: "The relative path to the file to summarize",
				Required:    true,
			},
		},
		Category:    CategoryExploration,
		Priority:    70,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

func (t *summarizeFileTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return &Result{Success: false, Error: "file_path is required"}, nil
	}

	result, err := t.summarizer.SummarizeFile(ctx, filePath)
	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// find_config_usage Tool
// ============================================================================

// findConfigUsageTool wraps explore.ConfigFinder.
type findConfigUsageTool struct {
	finder *explore.ConfigFinder
}

// NewFindConfigUsageTool creates the find_config_usage tool.
func NewFindConfigUsageTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &findConfigUsageTool{
		finder: explore.NewConfigFinder(g, idx),
	}
}

func (t *findConfigUsageTool) Name() string {
	return "find_config_usage"
}

func (t *findConfigUsageTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *findConfigUsageTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "find_config_usage",
		Description: "Finds where configuration values are used in the codebase. If config_key is empty or omitted, discovers all configuration access points.",
		Parameters: map[string]ParamDef{
			"config_key": {
				Type:        ParamTypeString,
				Description: "The configuration key or pattern to search for. Leave empty to discover all config access points (environment variables, config files, flags).",
				Required:    false, // Changed to optional for discovery mode
			},
		},
		Category:    CategoryExploration,
		Priority:    65,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

func (t *findConfigUsageTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	configKey, _ := params["config_key"].(string)

	var result *explore.ConfigUsage
	var err error

	if configKey == "" {
		// Discovery mode: find all config access points
		result, err = t.finder.DiscoverConfigs(ctx)
	} else {
		// Specific search mode
		result, err = t.finder.FindConfigUsage(ctx, configKey)
	}

	if err != nil {
		return &Result{Success: false, Error: err.Error()}, nil
	}

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// estimateTokens estimates the token count for a string.
// Approximation: ~4 characters per token for code.
func estimateTokens(s string) int {
	return len(s) / 4
}

// getIntParam extracts an int parameter from the params map.
// Handles both int and float64 (JSON unmarshaling produces float64).
func getIntParam(params map[string]any, key string) (int, bool) {
	v, ok := params[key]
	if !ok {
		return 0, false
	}

	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	default:
		return 0, false
	}
}

// ============================================================================
// list_packages Tool
// ============================================================================

// listPackagesTool lists all Go packages in the codebase.
type listPackagesTool struct {
	index *index.SymbolIndex
}

// NewListPackagesTool creates the list_packages tool.
//
// Description:
//
//	Creates a tool that lists all Go packages in the codebase by extracting
//	unique package names from the symbol index. This directly answers questions
//	like "What packages exist?" without requiring the model to be creative.
//
// Inputs:
//
//	idx - The symbol index containing parsed symbols.
//
// Outputs:
//
//	Tool - The list_packages tool.
func NewListPackagesTool(idx *index.SymbolIndex) Tool {
	return &listPackagesTool{
		index: idx,
	}
}

func (t *listPackagesTool) Name() string {
	return "list_packages"
}

func (t *listPackagesTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *listPackagesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "list_packages",
		Description: "Lists all packages/modules in the codebase with their paths, file counts, and symbol counts. Use this to understand project structure and package organization.",
		Parameters: map[string]ParamDef{
			"include_tests": {
				Type:        ParamTypeBool,
				Description: "Include test packages (*_test) in results",
				Required:    false,
				Default:     false,
			},
			"filter": {
				Type:        ParamTypeString,
				Description: "Filter packages by name prefix (e.g., 'pkg/api' to show only api-related packages)",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    95, // Higher than find_entry_points for package-related questions
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// PackageInfo contains information about a discovered package.
type PackageInfo struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Files       []string `json:"files"`
	FileCount   int      `json:"file_count"`
	SymbolCount int      `json:"symbol_count"`
	Types       int      `json:"types"`
	Functions   int      `json:"functions"`
	IsTest      bool     `json:"is_test,omitempty"`
}

// ListPackagesResult is the output of the list_packages tool.
type ListPackagesResult struct {
	Packages   []PackageInfo `json:"packages"`
	TotalCount int           `json:"total_count"`
}

func (t *listPackagesTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	start := time.Now()

	includeTests := false
	if v, ok := params["include_tests"].(bool); ok {
		includeTests = v
	}

	filter := ""
	if v, ok := params["filter"].(string); ok {
		filter = v
	}

	// Get all package symbols directly - more efficient than iterating all files
	packageSymbols := t.index.GetByKind(ast.SymbolKindPackage)

	// Group by package name and collect file info
	packageMap := make(map[string]*PackageInfo)
	filesByPackage := make(map[string]map[string]bool)

	for _, pkgSym := range packageSymbols {
		pkgName := pkgSym.Name
		if pkgName == "" {
			continue
		}

		// Apply filter
		if filter != "" && !strings.HasPrefix(pkgName, filter) && !strings.HasPrefix(pkgSym.FilePath, filter) {
			continue
		}

		// Skip test packages if not requested
		isTest := strings.HasSuffix(pkgName, "_test") || strings.HasSuffix(pkgSym.FilePath, "_test.go")
		if isTest && !includeTests {
			continue
		}

		// Initialize package info if not exists
		if _, exists := packageMap[pkgName]; !exists {
			packageMap[pkgName] = &PackageInfo{
				Name:   pkgName,
				Path:   extractPackagePath(pkgSym.FilePath),
				IsTest: isTest,
			}
			filesByPackage[pkgName] = make(map[string]bool)
		}

		// Track files
		filesByPackage[pkgName][pkgSym.FilePath] = true
	}

	// Now count symbols per package by getting symbols from files
	for pkgName, files := range filesByPackage {
		info := packageMap[pkgName]
		for filePath := range files {
			symbols := t.index.GetByFile(filePath)
			for _, sym := range symbols {
				if sym.Kind == ast.SymbolKindPackage {
					continue // Don't count the package symbol itself
				}
				info.SymbolCount++
				switch sym.Kind {
				case ast.SymbolKindStruct, ast.SymbolKindClass, ast.SymbolKindInterface, ast.SymbolKindType, ast.SymbolKindEnum:
					info.Types++
				case ast.SymbolKindFunction, ast.SymbolKindMethod:
					info.Functions++
				}
			}
		}
	}

	// Build result
	packages := make([]PackageInfo, 0, len(packageMap))
	for pkgName, info := range packageMap {
		files := filesByPackage[pkgName]
		info.FileCount = len(files)
		info.Files = make([]string, 0, len(files))
		for f := range files {
			info.Files = append(info.Files, f)
		}
		// Sort files for deterministic output
		sort.Strings(info.Files)
		packages = append(packages, *info)
	}

	// Sort packages by name for deterministic output
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Name < packages[j].Name
	})

	result := &ListPackagesResult{
		Packages:   packages,
		TotalCount: len(packages),
	}

	duration := time.Since(start)

	// Build CRS trace step for full observability
	packageNames := make([]string, 0, len(packages))
	for _, pkg := range packages {
		packageNames = append(packageNames, pkg.Name)
	}

	stepBuilder := crs.NewTraceStepBuilder().
		WithAction("list_packages").
		WithTarget("project").
		WithTool("list_packages").
		WithDuration(duration).
		WithSymbolsFound(packageNames).
		WithMetadata("navigation_level", "1").
		WithMetadata("total_packages", strconv.Itoa(result.TotalCount)).
		WithMetadata("filter", filter).
		WithMetadata("include_tests", strconv.FormatBool(includeTests))

	// Mark each package as proven (existence confirmed via AST)
	for _, pkg := range packages {
		stepBuilder.WithProofUpdate(
			"pkg:"+pkg.Name,
			"proven",
			fmt.Sprintf("Found in %d files with %d symbols", pkg.FileCount, pkg.SymbolCount),
			"hard", // AST parsing is hard signal
		)
	}

	traceStep := stepBuilder.Build()

	output, _ := json.Marshal(result)
	return &Result{
		Success:    true,
		Output:     result,
		OutputText: string(output),
		TokensUsed: estimateTokens(string(output)),
		Duration:   duration,
		TraceStep:  &traceStep,
		Metadata: map[string]any{
			"level":           "package",
			"packages_found":  result.TotalCount,
			"navigation_tool": true,
		},
	}, nil
}

// extractPackagePath extracts the directory path from a file path.
func extractPackagePath(filePath string) string {
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash > 0 {
		return filePath[:lastSlash]
	}
	return "."
}

// ============================================================================
// Mock Tool for Testing
// ============================================================================

// MockTool is a simple tool implementation for testing.
type MockTool struct {
	name        string
	category    ToolCategory
	definition  ToolDefinition
	ExecuteFunc func(ctx context.Context, params map[string]any) (*Result, error)
}

// NewMockTool creates a mock tool for testing.
func NewMockTool(name string, category ToolCategory) *MockTool {
	return &MockTool{
		name:     name,
		category: category,
		definition: ToolDefinition{
			Name:        name,
			Description: fmt.Sprintf("Mock tool: %s", name),
			Category:    category,
			Parameters:  make(map[string]ParamDef),
		},
		ExecuteFunc: func(ctx context.Context, params map[string]any) (*Result, error) {
			return &Result{
				Success:    true,
				OutputText: fmt.Sprintf("Mock result from %s", name),
			}, nil
		},
	}
}

func (t *MockTool) Name() string               { return t.name }
func (t *MockTool) Category() ToolCategory     { return t.category }
func (t *MockTool) Definition() ToolDefinition { return t.definition }
func (t *MockTool) WithDefinition(d ToolDefinition) *MockTool {
	t.definition = d
	return t
}

func (t *MockTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	return t.ExecuteFunc(ctx, params)
}

// ============================================================================
// Static Tool Definitions for Classification
// ============================================================================

// StaticToolDefinitions returns tool definitions without needing a graph.
//
// Description:
//
//	Returns the definitions for all exploration tools. These can be used
//	for query classification without initializing the full tool system.
//	The definitions include tool names, descriptions, and parameter schemas.
//
// Outputs:
//
//	[]ToolDefinition - The static tool definitions.
//
// Example:
//
//	defs := tools.StaticToolDefinitions()
//	classifier, _ := classifier.NewLLMClassifier(client, defs, config)
//
// Thread Safety: This function is safe for concurrent use.
func StaticToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "find_entry_points",
			Description: "Discovers entry points in the codebase (main functions, HTTP handlers, CLI commands, tests, etc.)",
			Parameters: map[string]ParamDef{
				"type": {
					Type:        ParamTypeString,
					Description: "Type of entry point to find: 'main', 'handler', 'command', 'test', 'lambda', 'grpc', or 'all'",
					Required:    false,
					Default:     "all",
					Enum:        []any{"main", "handler", "command", "test", "lambda", "grpc", "all"},
				},
				"package": {
					Type:        ParamTypeString,
					Description: "Filter results to a specific package path",
					Required:    false,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of results to return",
					Required:    false,
					Default:     100,
				},
				"include_tests": {
					Type:        ParamTypeBool,
					Description: "Include test entry points in results",
					Required:    false,
					Default:     false,
				},
			},
			Category:    CategoryExploration,
			Priority:    90,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
			Name:        "trace_data_flow",
			Description: "Traces data flow through function calls, identifying sources, transforms, and sinks",
			Parameters: map[string]ParamDef{
				"symbol_id": {
					Type:        ParamTypeString,
					Description: "The symbol ID to start tracing from",
					Required:    true,
				},
				"max_hops": {
					Type:        ParamTypeInt,
					Description: "Maximum depth to trace",
					Required:    false,
					Default:     5,
				},
			},
			Category:    CategoryExploration,
			Priority:    85,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     15 * time.Second,
		},
		{
			Name:        "trace_error_flow",
			Description: "Traces error propagation paths to understand how errors are handled and escalated",
			Parameters: map[string]ParamDef{
				"symbol_id": {
					Type:        ParamTypeString,
					Description: "The symbol ID to start tracing from",
					Required:    true,
				},
				"max_depth": {
					Type:        ParamTypeInt,
					Description: "Maximum depth to trace",
					Required:    false,
					Default:     5,
				},
			},
			Category:    CategoryExploration,
			Priority:    80,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     15 * time.Second,
		},
		{
			Name:        "build_minimal_context",
			Description: "Builds minimal context required to understand a symbol, filtering by relevance",
			Parameters: map[string]ParamDef{
				"symbol_id": {
					Type:        ParamTypeString,
					Description: "The symbol ID to build context for",
					Required:    true,
				},
				"max_tokens": {
					Type:        ParamTypeInt,
					Description: "Maximum tokens for context",
					Required:    false,
					Default:     4000,
				},
			},
			Category:    CategoryExploration,
			Priority:    75,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
			Name:        "find_similar_code",
			Description: "Finds code similar to a given symbol or pattern",
			Parameters: map[string]ParamDef{
				"symbol_id": {
					Type:        ParamTypeString,
					Description: "The symbol ID to find similar code for",
					Required:    true,
				},
				"threshold": {
					Type:        ParamTypeFloat,
					Description: "Similarity threshold (0.0-1.0)",
					Required:    false,
					Default:     0.7,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of results",
					Required:    false,
					Default:     20,
				},
			},
			Category:    CategoryExploration,
			Priority:    70,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
			Name:        "summarize_file",
			Description: "Generates a summary of a file's contents and structure",
			Parameters: map[string]ParamDef{
				"path": {
					Type:        ParamTypeString,
					Description: "The file path to summarize",
					Required:    true,
				},
			},
			Category:    CategoryExploration,
			Priority:    65,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
			Name:        "find_config_usage",
			Description: "Finds where configuration values are used in the codebase. If config_key is empty or omitted, discovers all configuration access points.",
			Parameters: map[string]ParamDef{
				"config_key": {
					Type:        ParamTypeString,
					Description: "The configuration key or pattern to search for. Leave empty to discover all config access points (environment variables, config files, flags).",
					Required:    false,
				},
			},
			Category:    CategoryExploration,
			Priority:    65,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
			Name:        "list_packages",
			Description: "Lists all packages/modules in the codebase with their paths, file counts, and symbol counts. Use this to understand project structure and package organization.",
			Parameters: map[string]ParamDef{
				"include_tests": {
					Type:        ParamTypeBool,
					Description: "Include test packages (*_test) in results",
					Required:    false,
					Default:     false,
				},
				"filter": {
					Type:        ParamTypeString,
					Description: "Filter packages by name prefix (e.g., 'pkg/api' to show only api-related packages)",
					Required:    false,
				},
			},
			Category:    CategoryExploration,
			Priority:    95,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
		// GR-02 to GR-05: Graph Analytics Tools
		{
			Name: "find_hotspots",
			Description: "Find the most-connected symbols in the codebase (hotspots). " +
				"Hotspots indicate high coupling and potential refactoring targets. " +
				"Returns symbols ranked by connectivity score (inDegree*2 + outDegree).",
			Parameters: map[string]ParamDef{
				"top": {
					Type:        ParamTypeInt,
					Description: "Number of hotspots to return (1-100)",
					Required:    false,
					Default:     10,
				},
				"kind": {
					Type:        ParamTypeString,
					Description: "Filter by symbol kind: function, type, or all",
					Required:    false,
					Default:     "all",
					Enum:        []any{"function", "type", "all"},
				},
			},
			Category:    CategoryExploration,
			Priority:    86,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
		{
			Name: "find_dead_code",
			Description: "Find potentially unused code (symbols with no callers). " +
				"Excludes entry points (main, init, Test*) and interface methods. " +
				"By default only shows unexported symbols; use include_exported=true for all.",
			Parameters: map[string]ParamDef{
				"include_exported": {
					Type:        ParamTypeBool,
					Description: "Include exported symbols (by default only unexported are shown)",
					Required:    false,
					Default:     false,
				},
				"package": {
					Type:        ParamTypeString,
					Description: "Filter to a specific package path",
					Required:    false,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of results to return",
					Required:    false,
					Default:     50,
				},
			},
			Category:    CategoryExploration,
			Priority:    84,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
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
		},
		{
			Name: "find_path",
			Description: "Find the shortest path between two symbols. " +
				"Uses BFS to find the minimum-hop path. " +
				"Useful for understanding how two pieces of code are connected.",
			Parameters: map[string]ParamDef{
				"from": {
					Type:        ParamTypeString,
					Description: "Starting symbol name (e.g., 'main', 'parseConfig')",
					Required:    true,
				},
				"to": {
					Type:        ParamTypeString,
					Description: "Target symbol name",
					Required:    true,
				},
			},
			Category:    CategoryExploration,
			Priority:    83,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		{
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
		},
		{
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
		},
		{
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
		},
		{
			Name: "check_reducibility",
			Description: "Check if the call graph is reducible (well-structured). " +
				"Reducible graphs have clean loop structures without 'goto spaghetti'. " +
				"Non-reducible regions indicate complex or potentially problematic code.",
			Parameters: map[string]ParamDef{
				"show_irreducible": {
					Type:        ParamTypeBool,
					Description: "List specific irreducible regions (default: true)",
					Required:    false,
					Default:     true,
				},
			},
			Category:    CategoryExploration,
			Priority:    79,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     30 * time.Second,
		},
		// IT-03 C-3: Add find_implementations to static definitions with cross-language
		// description so the LLM classifier routes "extends" and "subclass" queries correctly.
		{
			Name: "find_implementations",
			Description: "Find all types that implement a given interface or extend a given class. " +
				"Works across languages: Go interfaces (structural typing), Python class inheritance and ABCs, " +
				"JS/TS class extends and implements. " +
				"Use for 'what implements X?', 'what extends X?', 'what subclasses X?', 'what derives from X?'.",
			Parameters: map[string]ParamDef{
				"interface_name": {
					Type:        ParamTypeString,
					Description: "Name of the interface or base class to find implementations/subclasses for (e.g., 'Reader', 'AbstractMesh', 'Index', 'SessionInterface')",
					Required:    true,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of implementations to return",
					Required:    false,
					Default:     50,
				},
			},
			Category:    CategoryExploration,
			Priority:    93,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
		// IT-02 H-1: Add find_callers and find_callees — the two most fundamental
		// graph query tools were missing from static definitions, causing the LLM
		// classifier to lack awareness for tool routing.
		// IT-02 R6-FIX: Add get_call_chain to static definitions so the LLM
		// classifier is aware of it and can distinguish it from find_callees.
		{
			Name: "get_call_chain",
			Description: "Get the TRANSITIVE call chain for a function — traces ALL calls recursively up to depth 10. " +
				"Can trace 'downstream' (what does X call, and what do those call, recursively) or 'upstream' (what calls X, recursively). " +
				"ONLY use when the user explicitly asks for the FULL call hierarchy, transitive calls, impact analysis, or call chains. " +
				"DO NOT use for simple 'what does X call' questions — those need find_callees for DIRECT callees only. " +
				"Results from this tool are TRANSITIVE (indirect) calls, NOT direct callees.",
			Parameters: map[string]ParamDef{
				"function_name": {
					Type:        ParamTypeString,
					Description: "Name of the function to trace",
					Required:    true,
				},
				"direction": {
					Type:        ParamTypeString,
					Description: "Traversal direction: 'downstream' (callees) or 'upstream' (callers)",
					Required:    false,
					Default:     "downstream",
					Enum:        []any{"downstream", "upstream"},
				},
				"max_depth": {
					Type:        ParamTypeInt,
					Description: "Maximum traversal depth (1-10)",
					Required:    false,
					Default:     5,
				},
			},
			Category:    CategoryExploration,
			Priority:    80, // Lower than find_callees (94) to prefer direct queries
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     10 * time.Second,
		},
		// IT-04: Add find_symbol to static definitions so the LLM classifier
		// can route "where is X defined?" and "find X" queries correctly.
		{
			Name: "find_symbol",
			Description: "Look up a symbol (function, type, class, interface, variable) by name. " +
				"Returns the symbol's definition location (file and line), kind, package, and signature. " +
				"Use for 'where is X defined?', 'find X', 'locate X', 'what is X?'. " +
				"Works across all languages: Go structs/interfaces, Python classes, JS/TS classes and functions.",
			Parameters: map[string]ParamDef{
				"name": {
					Type:        ParamTypeString,
					Description: "Name of the symbol to find (e.g., 'Router', 'DataFrame', 'Context', 'NestFactory')",
					Required:    true,
				},
				"kind": {
					Type:        ParamTypeString,
					Description: "Filter by symbol kind: function, method, class, struct, interface, type, variable, constant, enum, or all",
					Required:    false,
					Default:     "all",
					Enum:        []any{"function", "method", "class", "struct", "interface", "type", "variable", "constant", "enum", "all"},
				},
			},
			Category:    CategoryExploration,
			Priority:    92,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
		{
			Name: "find_callers",
			Description: "Find all functions that CALL a given function (upstream dependencies). " +
				"The target function is the OBJECT being called. " +
				"Use when asked 'who calls X?' or 'find usages of X' or 'where is X called from?'. " +
				"DO NOT use this when the query asks 'what does X call' or 'what functions does X call' — use find_callees instead. " +
				"Returns the list of callers with file locations and signatures.",
			Parameters: map[string]ParamDef{
				"function_name": {
					Type:        ParamTypeString,
					Description: "Name of the function to find callers for. Use Type.Method format for methods (e.g., 'Context.JSON', 'DB.Open'). For standalone functions, just the name (e.g., 'parseConfig').",
					Required:    true,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of callers to return",
					Required:    false,
					Default:     50,
				},
			},
			Category:    CategoryExploration,
			Priority:    95,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
		{
			Name: "find_callees",
			Description: "Find all functions that a given function CALLS (downstream dependencies). " +
				"The target function is the SUBJECT doing the calling — 'what does X call?' " +
				"USE THIS when asked 'what does X call?', 'what functions does X call?', 'what functions does X depend on?', or 'what methods does X invoke?'. " +
				"NOT for 'who calls X' — use find_callers for that instead (where X is the OBJECT being called). " +
				"Returns the list of called functions with file locations.",
			Parameters: map[string]ParamDef{
				"function_name": {
					Type:        ParamTypeString,
					Description: "Name of the function to find callees for. Use Type.Method format for methods (e.g., 'Context.JSON', 'DB.Open'). For standalone functions, just the name (e.g., 'parseConfig').",
					Required:    true,
				},
				"limit": {
					Type:        ParamTypeInt,
					Description: "Maximum number of callees to return",
					Required:    false,
					Default:     50,
				},
			},
			Category:    CategoryExploration,
			Priority:    94,
			Requires:    []string{"graph_initialized"},
			SideEffects: false,
			Timeout:     5 * time.Second,
		},
	}
}
