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
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
)

// =============================================================================
// find_similar_symbols Tool - CRS-26m
// =============================================================================

var findSimilarSymbolsTracer = otel.Tracer("tools.find_similar_symbols")

// FindSimilarSymbolsParams contains the validated input parameters.
type FindSimilarSymbolsParams struct {
	// SymbolName is the name of the symbol to find similar ones for.
	SymbolName string

	// Limit is the maximum number of similar symbols to return.
	// Default: 10, Max: 50
	Limit int
}

// ToolName returns the tool name for TypedParams interface.
func (p FindSimilarSymbolsParams) ToolName() string { return "find_similar_symbols" }

// ToMap converts typed parameters to the map consumed by Tool.Execute().
func (p FindSimilarSymbolsParams) ToMap() map[string]any {
	return map[string]any{
		"symbol_name": p.SymbolName,
		"limit":       p.Limit,
	}
}

// FindSimilarSymbolsOutput contains the structured result.
type FindSimilarSymbolsOutput struct {
	// SourceSymbol is the symbol that was used as the search anchor.
	SourceSymbol string `json:"source_symbol"`

	// SourceFile is the file containing the source symbol.
	SourceFile string `json:"source_file"`

	// ResultCount is the number of similar symbols found.
	ResultCount int `json:"result_count"`

	// Results contains the ranked similar symbols.
	Results []SemanticSearchResult `json:"results"`
}

// findSimilarSymbolsTool finds symbols semantically similar to a given symbol.
//
// Description:
//
//	CRS-26m: Given a symbol name, looks it up in the index, constructs a search
//	text from the symbol's metadata, embeds it, and performs a nearVector search
//	to find semantically similar symbols. Excludes the source symbol from results.
//
// Thread Safety: Safe for concurrent use. All operations are read-only.
type findSimilarSymbolsTool struct {
	wvClient    *weaviate.Client
	dataSpace   string
	embedClient *rag.EmbedClient
	idx         *index.SymbolIndex
	logger      *slog.Logger
}

// NewFindSimilarSymbolsTool creates the find_similar_symbols tool.
//
// Description:
//
//	CRS-26m: Creates a tool for finding symbols with similar purpose/behavior.
//	Gracefully degrades if any dependency is nil.
//
// Inputs:
//
//   - wvClient: Weaviate client. May be nil (graceful degradation).
//   - dataSpace: Project isolation key for Weaviate.
//   - embedClient: Embedding client. May be nil (graceful degradation).
//   - idx: Symbol index for O(1) lookups. May be nil (graceful degradation).
//
// Outputs:
//
//   - Tool: The find_similar_symbols tool implementation.
//
// Thread Safety: Safe for concurrent use after construction.
func NewFindSimilarSymbolsTool(wvClient *weaviate.Client, dataSpace string, embedClient *rag.EmbedClient, idx *index.SymbolIndex) Tool {
	return &findSimilarSymbolsTool{
		wvClient:    wvClient,
		dataSpace:   dataSpace,
		embedClient: embedClient,
		idx:         idx,
		logger:      slog.Default(),
	}
}

func (t *findSimilarSymbolsTool) Name() string {
	return "find_similar_symbols"
}

func (t *findSimilarSymbolsTool) Category() ToolCategory {
	return CategorySemantic
}

func (t *findSimilarSymbolsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "find_similar_symbols",
		Description: "Find symbols with similar purpose or behavior to a given symbol. " +
			"Use when the user has a specific symbol and wants to find related functions/methods. " +
			"Examples: 'find symbols similar to parseConfig', 'what functions are like validateInput?'",
		Parameters: map[string]ParamDef{
			"symbol_name": {
				Type:        ParamTypeString,
				Description: "Name of the symbol to find similar ones for (e.g., 'parseConfig', 'validateInput')",
				Required:    true,
			},
			"limit": {
				Type:        ParamTypeInt,
				Description: "Maximum number of similar symbols to return (default 10, max 50)",
				Required:    false,
				Default:     10,
			},
		},
		Category:    CategorySemantic,
		Priority:    65,
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
		WhenToUse: WhenToUse{
			Keywords: []string{
				"similar symbols", "symbols like", "resembles",
				"similar to function", "related functions", "similar methods",
			},
			UseWhen: "User has a specific symbol name and wants to find other symbols with " +
				"similar purpose or behavior. Examples: 'find symbols similar to parseConfig'.",
			AvoidWhen: "User wants callers or callees — use graph query tools. " +
				"User wants implementations or subclasses — use find_implementations.",
		},
	}
}

// Execute runs the find_similar_symbols tool.
//
// Description:
//
//	Looks up the symbol in the index, constructs search text from its metadata,
//	embeds it, and performs a nearVector search excluding the source symbol.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	params - Must contain "symbol_name" (string). Optional: "limit" (int).
//
// Outputs:
//
//	*Result - Ranked similar symbols with similarity scores.
//	error - Non-nil only for context cancellation.
//
// Thread Safety: Safe for concurrent use.
func (t *findSimilarSymbolsTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	start := time.Now()

	// Graceful degradation
	if t.wvClient == nil || t.embedClient == nil || t.idx == nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_similar_symbols").
			WithTool("find_similar_symbols").
			WithDuration(time.Since(start)).
			WithError("find_similar_symbols unavailable: required services not configured").
			Build()
		return &Result{
			Success:   false,
			Error:     "Find similar symbols is not available. Weaviate, embedding service, or symbol index is not configured for this session.",
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Parse parameters
	p, err := t.parseParams(params.ToMap())
	if err != nil {
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_similar_symbols").
			WithTool("find_similar_symbols").
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
	ctx, span := findSimilarSymbolsTracer.Start(ctx, "tools.FindSimilarSymbolsTool.Execute",
		trace.WithAttributes(
			attribute.String("tool", "find_similar_symbols"),
			attribute.String("symbol_name", p.SymbolName),
			attribute.Int("limit", p.Limit),
		),
	)
	defer span.End()

	// Look up symbol in index
	symbols := t.idx.GetByName(p.SymbolName)
	if len(symbols) == 0 {
		span.SetAttributes(attribute.Bool("symbol_not_found", true))
		toolStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_similar_symbols").
			WithTarget(p.SymbolName).
			WithTool("find_similar_symbols").
			WithDuration(time.Since(start)).
			WithMetadata("result_count", "0").
			WithError("symbol not found").
			Build()
		return &Result{
			Success:    true,
			Output:     FindSimilarSymbolsOutput{SourceSymbol: p.SymbolName},
			OutputText: fmt.Sprintf("## Find Similar Symbols: '%s' not found\n\nNo symbol named '%s' exists in this codebase.\n", p.SymbolName, p.SymbolName),
			TraceStep:  &toolStep,
			Duration:   time.Since(start),
		}, nil
	}

	// Use the first matching symbol
	sourceSym := symbols[0]

	// Construct search text from symbol metadata
	searchText := rag.BuildSearchText(sourceSym)

	// Embed the search text
	queryVec, err := t.embedClient.EmbedQuery(ctx, searchText)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_similar_symbols").
			WithTool("find_similar_symbols").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("embedding symbol: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Failed to embed symbol: %v", err),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Build Weaviate query — exclude source symbol by ID
	dataSpaceFilter := filters.Where().
		WithPath([]string{"dataSpace"}).
		WithOperator(filters.Equal).
		WithValueString(t.dataSpace)

	excludeFilter := filters.Where().
		WithPath([]string{"symbolId"}).
		WithOperator(filters.NotEqual).
		WithValueString(sourceSym.ID)

	combinedFilter := filters.Where().
		WithOperator(filters.And).
		WithOperands([]*filters.WhereBuilder{dataSpaceFilter, excludeFilter})

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

	nearVector := t.wvClient.GraphQL().NearVectorArgBuilder().
		WithVector(queryVec).
		WithDistance(0.5) // max distance for "similar" symbols

	result, err := t.wvClient.GraphQL().Get().
		WithClassName(rag.CodeSymbolClassName).
		WithFields(fields...).
		WithWhere(combinedFilter).
		WithNearVector(nearVector).
		WithLimit(p.Limit).
		Do(ctx)
	if err != nil {
		span.RecordError(err)
		errStep := crs.NewTraceStepBuilder().
			WithAction("tool_find_similar_symbols").
			WithTool("find_similar_symbols").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("weaviate query: %v", err)).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Similar symbol search failed: %v", err),
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
			WithAction("tool_find_similar_symbols").
			WithTool("find_similar_symbols").
			WithDuration(time.Since(start)).
			WithError(strings.Join(errMsgs, "; ")).
			Build()
		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("Similar symbol search errors: %s", strings.Join(errMsgs, "; ")),
			TraceStep: &errStep,
			Duration:  time.Since(start),
		}, nil
	}

	// Parse results — reuse SemanticSearchResult type
	output := t.parseResults(sourceSym, result)

	span.SetAttributes(attribute.Int("result_count", output.ResultCount))

	duration := time.Since(start)
	outputText := t.formatText(output)

	toolStep := crs.NewTraceStepBuilder().
		WithAction("tool_find_similar_symbols").
		WithTarget(p.SymbolName).
		WithTool("find_similar_symbols").
		WithDuration(duration).
		WithMetadata("result_count", fmt.Sprintf("%d", output.ResultCount)).
		WithMetadata("source_file", sourceSym.FilePath).
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
func (t *findSimilarSymbolsTool) parseParams(params map[string]any) (FindSimilarSymbolsParams, error) {
	p := FindSimilarSymbolsParams{
		Limit: 10,
	}

	if nameRaw, ok := params["symbol_name"]; ok {
		if name, ok := parseStringParam(nameRaw); ok && name != "" {
			p.SymbolName = name
		}
	}
	if err := ValidateSymbolName(p.SymbolName, "symbol_name", "'parseConfig', 'validateInput', 'handleRequest'"); err != nil {
		return p, err
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

	return p, nil
}

// parseResults extracts search results from the Weaviate GraphQL response.
func (t *findSimilarSymbolsTool) parseResults(sourceSym *ast.Symbol, result *models.GraphQLResponse) FindSimilarSymbolsOutput {
	output := FindSimilarSymbolsOutput{
		SourceSymbol: sourceSym.Name,
		SourceFile:   sourceSym.FilePath,
		Results:      make([]SemanticSearchResult, 0),
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

		if additional, ok := props["_additional"].(map[string]interface{}); ok {
			if dist, ok := additional["distance"].(float64); ok {
				sr.Score = 1.0 - dist
			}
		}

		output.Results = append(output.Results, sr)
	}

	output.ResultCount = len(output.Results)
	return output
}

// formatText creates a human-readable text summary.
func (t *findSimilarSymbolsTool) formatText(output FindSimilarSymbolsOutput) string {
	var sb strings.Builder

	if output.ResultCount == 0 {
		sb.WriteString(fmt.Sprintf("## Find Similar Symbols: No results for '%s'\n\n", output.SourceSymbol))
		sb.WriteString("No semantically similar symbols found.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d symbols similar to '%s' (%s):\n\n",
		output.ResultCount, output.SourceSymbol, output.SourceFile))

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
