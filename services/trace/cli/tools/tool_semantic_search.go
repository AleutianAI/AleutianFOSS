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

	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
)

// =============================================================================
// semantic_search Tool - CRS-26m
// =============================================================================

var semanticSearchTracer = otel.Tracer("tools.semantic_search")

// SemanticSearchParams contains the validated input parameters.
type SemanticSearchParams struct {
	// Query is the natural language search query.
	Query string

	// Limit is the maximum number of results to return.
	// Default: 10, Max: 50
	Limit int

	// MinScore is the minimum similarity threshold (0.0-1.0).
	// Default: 0.5
	MinScore float64
}

// ToolName returns the tool name for TypedParams interface.
func (p SemanticSearchParams) ToolName() string { return "semantic_search" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p SemanticSearchParams) ToMap() map[string]any {
	return map[string]any{
		"query":     p.Query,
		"limit":     p.Limit,
		"min_score": p.MinScore,
	}
}

// SemanticSearchResult represents a single semantic search result.
type SemanticSearchResult struct {
	// Name is the symbol name.
	Name string `json:"name"`

	// Kind is the symbol kind (function, method, class, etc.).
	Kind string `json:"kind"`

	// PackagePath is the package/module path.
	PackagePath string `json:"package_path"`

	// FilePath is the source file path.
	FilePath string `json:"file_path"`

	// Score is the similarity score (0.0-1.0, higher is more similar).
	Score float64 `json:"score"`
}

// SemanticSearchOutput contains the structured result.
type SemanticSearchOutput struct {
	// Query is the search query that was executed.
	Query string `json:"query"`

	// ResultCount is the number of results found.
	ResultCount int `json:"result_count"`

	// Results contains the ranked search results.
	Results []SemanticSearchResult `json:"results"`
}

// semanticSearchTool performs vector similarity search over code symbols.
//
// Description:
//
//	CRS-26m: Searches code symbols by meaning/concept using vector similarity.
//	Embeds the query via EmbedClient, then performs a nearVector search against
//	Weaviate's CodeSymbol collection. Returns ranked results with similarity scores.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type semanticSearchTool struct {
	wvClient    *weaviate.Client
	dataSpace   string
	embedClient *rag.EmbedClient
	logger      *slog.Logger
}

// NewSemanticSearchTool creates the semantic_search tool.
//
// Description:
//
//	CRS-26m: Creates a tool for semantic code search using vector similarity.
//	Gracefully degrades if wvClient or embedClient is nil.
//
// Inputs:
//
//   - wvClient: Weaviate client. May be nil (graceful degradation).
//   - dataSpace: Project isolation key for Weaviate.
//   - embedClient: Embedding client. May be nil (graceful degradation).
//
// Outputs:
//
//   - Tool: The semantic_search tool implementation.
//
// Thread Safety: Safe for concurrent use after construction.
func NewSemanticSearchTool(wvClient *weaviate.Client, dataSpace string, embedClient *rag.EmbedClient) Tool {
	return &semanticSearchTool{
		wvClient:    wvClient,
		dataSpace:   dataSpace,
		embedClient: embedClient,
		logger:      slog.Default(),
	}
}

func (t *semanticSearchTool) Name() string {
	return "semantic_search"
}

func (t *semanticSearchTool) Category() ToolCategory {
	return CategorySemantic
}

func (t *semanticSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "semantic_search",
		Description: "Search code symbols by meaning or concept using vector similarity. " +
			"Use when the user asks for code related to a concept rather than an exact name. " +
			"Examples: 'find code related to authentication', 'search for error handling patterns'. " +
			"Returns ranked results with similarity scores.",
		Parameters: map[string]ParamDef{
			"query": {
				Type:        ParamTypeString,
				Description: "Natural language search query describing the code concept to find",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of results to return (default 10, max 50)",
				Required:    false,
				Default:     10,
			},
			"min_score": {
				Type:        ParamTypeFloat,
				Description: "Minimum similarity score threshold (0.0-1.0, default 0.5)",
				Required:    false,
				Default:     0.5,
			},
		},
		Category:    CategorySemantic,
		Priority:    70,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"semantic search", "similar to", "related to",
				"conceptually similar", "vector search", "meaning",
				"semantically", "find code about", "code related to",
			},
			UseWhen: "User asks for code by concept or meaning rather than exact name. " +
				"Examples: 'find code related to authentication', 'search for symbols similar to error handling'.",
			AvoidWhen: "User asks for exact symbol names — use find_symbol instead. " +
				"User asks about callers, callees, or call chains — use graph query tools. " +
				"User asks about file contents — use Grep or Read instead.",
		},
	}
}

// Execute runs the semantic_search tool.
//
// Description:
//
//	Embeds the query, performs nearVector search in Weaviate, and returns
//	ranked results with similarity scores.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	params - Must contain "query" (string). Optional: "limit" (int), "min_score" (float64).
//
// Outputs:
//
//	*Result - Ranked search results with similarity scores.
//	error - Non-nil only for context cancellation.
//
// Thread Safety: Safe for concurrent use.
func (t *semanticSearchTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Graceful degradation
	if t.wvClient == nil || t.embedClient == nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_semantic_search").
			WithTool("semantic_search").
			WithDuration(time.Since(start)).
			WithError("semantic search unavailable: Weaviate or embedding service not configured").
			Build()
		return &Result{
			Success:   false,
			Error:     "Semantic search is not available. Weaviate or embedding service is not configured for this session.",
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Parse parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_semantic_search").
			WithTool("semantic_search").
			WithDuration(time.Since(start)).
			WithError(err.Error()).
			Build()
		return &Result{
			Success:   false,
			Error:     err.Error(),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Start span
	ctx, span := semanticSearchTracer.Start(ctx, "tools.SemanticSearchTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "semantic_search"),
			attribute.String("query", p.Query),
			attribute.Int("limit", p.Limit),
			attribute.Float64("min_score", p.MinScore),
		),
	)
	defer span.End()

	// Embed query
	queryVec, err := t.embedClient.EmbedQuery(ctx, p.Query)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_semantic_search").
			WithTool("semantic_search").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("embedding query: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Failed to embed query: %v", err),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Build Weaviate query
	where := filters.Where().
		WithPath([]string{"dataSpace"}).
		WithOperator(filters.Equal).
		WithValueString(t.dataSpace)

	fields := []graphql.Field{
		{Name: "symbolId"},
		{Name: "name"},
		{Name: "kind"},
		{Name: "packagePath"},
		{Name: "filePath"},
		{Name: "exported"},
		{Name: "_additional", Fields: []graphql.Field{
			{Name: "distance"},
		}},
	}

	// Convert min_score to max distance (Weaviate cosine: distance = 1 - similarity)
	maxDistance := float32(1.0 - p.MinScore)

	nearVector := t.wvClient.GraphQL().NearVectorArgBuilder().
		WithVector(queryVec).
		WithDistance(maxDistance)

	result, err := t.wvClient.GraphQL().Get().
		WithClassName(rag.CodeSymbolClassName).
		WithFields(fields...).
		WithWhere(where).
		WithNearVector(nearVector).
		WithLimit(p.Limit).
		Do(ctx)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_semantic_search").
			WithTool("semantic_search").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("weaviate query: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Semantic search failed: %v", err),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	if result.Errors != nil && len(result.Errors) > 0 {
		errMsgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_semantic_search").
			WithTool("semantic_search").
			WithDuration(time.Since(start)).
			WithError(strings.Join(errMsgs, "; ")).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Semantic search errors: %s", strings.Join(errMsgs, "; ")),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Parse results
	output := t.parseResults(p.Query, result)

	span.SetAttributes(attribute.Int("result_count", output.ResultCount))

	duration := time.Since(start)

	// Format text output
	outputText := t.formatText(output)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_semantic_search").
		WithTarget(p.Query).
		WithTool("semantic_search").
		WithDuration(duration).
		WithMetadata("result_count", fmt.Sprintf("%d", output.ResultCount)).
		WithMetadata("limit", fmt.Sprintf("%d", p.Limit)).
		WithMetadata("min_score", fmt.Sprintf("%.2f", p.MinScore)).
		Build()

	return &Result{
		Success:     true,
		Output:      output,
		OutputText:  outputText,
		TokensUsed:  estimateTokens(outputText),
		TraceStep:   &toolStep,
		Duration:    duration,
		ResultCount: output.ResultCount,
	}, nil
}

// parseParams validates and extracts typed parameters.
func (t *semanticSearchTool) parseParams(params map[string]any) (SemanticSearchParams, error) {
	p := SemanticSearchParams{
		Limit:    10,
		MinScore: 0.5,
	}

	if queryRaw, ok := params["query"]; ok {
		if query, ok := parseStringParam(queryRaw); ok && query != "" {
			p.Query = query
		}
	}
	if p.Query == "" {
		return p, fmt.Errorf("parameter 'query' is required and must be a non-empty string")
	}

	if limitRaw, ok := params["limit"]; ok {
		if limit, ok := parseIntParam(limitRaw); ok {
			if limit < 1 {
				limit = 1
			} else if limit > 50 {
				limit = 50
			}
			p.Limit = limit
		}
	}

	if minScoreRaw, ok := params["min_score"]; ok {
		if score, ok := parseFloatParam(minScoreRaw); ok {
			if score < 0.0 {
				score = 0.0
			} else if score > 1.0 {
				score = 1.0
			}
			p.MinScore = score
		}
	}

	return p, nil
}

// parseResults extracts search results from the Weaviate GraphQL response.
func (t *semanticSearchTool) parseResults(query string, result *models.GraphQLResponse) SemanticSearchOutput {
	output := SemanticSearchOutput{
		Query:   query,
		Results: make([]SemanticSearchResult, 0),
	}

	if result == nil || result.Data == nil {
		return output
	}

	get, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return output
	}

	objects, ok := get[rag.CodeSymbolClassName].([]interface{})
	if !ok {
		return output
	}

	for _, obj := range objects {
		props, ok := obj.(map[string]interface{})
		if !ok {
			continue
		}

		sr := SemanticSearchResult{
			Name:        getStringProp(props, "name"),
			Kind:        getStringProp(props, "kind"),
			PackagePath: getStringProp(props, "packagePath"),
			FilePath:    getStringProp(props, "filePath"),
		}

		// Extract distance from _additional and convert to similarity score
		if additional, ok := props["_additional"].(map[string]interface{}); ok {
			if dist, ok := additional["distance"].(float64); ok {
				sr.Score = 1.0 - dist // cosine distance → similarity
			}
		}

		output.Results = append(output.Results, sr)
	}

	output.ResultCount = len(output.Results)
	return output
}

// formatText creates a human-readable text summary.
func (t *semanticSearchTool) formatText(output SemanticSearchOutput) string {
	var sb strings.Builder

	if output.ResultCount == 0 {
		sb.WriteString(fmt.Sprintf("## Semantic Search: No results for '%s'\n\n", output.Query))
		sb.WriteString("No symbols matched the query with sufficient similarity.\n")
		sb.WriteString("Try broadening the query or lowering the min_score threshold.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d symbols related to '%s':\n\n", output.ResultCount, output.Query))

	for i, r := range output.Results {
		sb.WriteString(fmt.Sprintf("%d. %s (%s) — score: %.3f\n", i+1, r.Name, r.Kind, r.Score))
		if r.PackagePath != "" {
			sb.WriteString(fmt.Sprintf("   Package: %s\n", r.PackagePath))
		}
		if r.FilePath != "" {
			sb.WriteString(fmt.Sprintf("   File: %s\n", r.FilePath))
		}
	}

	return sb.String()
}

// getStringProp extracts a string property from a map.
func getStringProp(props map[string]interface{}, key string) string {
	if v, ok := props[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
