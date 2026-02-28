// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// =============================================================================
// ParamExtractor - LLM-Enhanced Parameter Extraction (IT-08b)
// =============================================================================

// ParamExtractor uses a fast LLM to extract tool parameters from natural
// language queries. It corrects errors using semantic understanding of
// hierarchical scoping (e.g., project vs. module names).
//
// # Description
//
// The regex-based parameter extraction in extractToolParameters() fails on
// queries with hierarchical scope references (e.g., "in the Flask helpers
// module" extracts "flask" instead of "helpers"). ParamExtractor uses a
// dedicated small model (ministral-3:3b by default) that runs in parallel
// with the tool router (granite4:micro-h) on a separate Ollama instance.
//
// IT-08e: The extractor runs speculatively on the pre-filter's top candidate
// BEFORE routing completes. If the router confirms the prediction, LLM params
// are used; otherwise regex fallback kicks in.
//
// CB-60: Refactored to use providers.ChatClient instead of direct
// MultiModelManager dependency, enabling any LLM provider.
//
// # Thread Safety
//
// ParamExtractor is safe for concurrent use.
type ParamExtractor struct {
	chatClient providers.ChatClient
	config     ParamExtractorConfig
	logger     *slog.Logger
}

// ParamExtractorConfig configures the parameter extractor.
//
// # Description
//
// Controls the model, timeout, and feature flag for LLM parameter extraction.
// IT-08e: Uses a dedicated small model (ministral-3:3b) separate from the
// tool router to enable parallel execution without Ollama serialization.
type ParamExtractorConfig struct {
	// Model is the Ollama model to use for parameter extraction.
	// IT-08e: Dedicated small model, separate from the router.
	// Default: "ministral-3:3b"
	Model string `json:"model"`

	// Timeout is the maximum time for a parameter extraction call.
	// IT-08e: Increased from 500ms to 2s (dedicated model, no serialization).
	// Default: 2s
	Timeout time.Duration `json:"timeout"`

	// Temperature controls randomness. Lower = more deterministic.
	// Default: 0.1
	Temperature float64 `json:"temperature"`

	// MaxTokens limits the response length.
	// Default: 512
	MaxTokens int `json:"max_tokens"`

	// NumCtx is the context window size.
	// IT-08e: Reduced from 8192 to 4096 (12x headroom for ~300 token budget).
	// Default: 4096
	NumCtx int `json:"num_ctx"`

	// KeepAlive controls how long the model stays in VRAM.
	// Default: "24h"
	KeepAlive string `json:"keep_alive"`

	// Enabled is the feature flag. When false, ExtractParams is a no-op.
	// Default: true
	Enabled bool `json:"enabled"`
}

// DefaultParamExtractorConfig returns sensible defaults.
//
// # Outputs
//
//   - ParamExtractorConfig: Default configuration.
func DefaultParamExtractorConfig() ParamExtractorConfig {
	return ParamExtractorConfig{
		Model:       "ministral-3:3b", // IT-08e: Dedicated small model (not the router model)
		Timeout:     2 * time.Second,  // IT-08e: Longer timeout on dedicated model (no serialization)
		Temperature: 0.1,
		MaxTokens:   512,
		NumCtx:      4096, // IT-08e: 12x headroom for ~300 token budget
		KeepAlive:   "24h",
		Enabled:     true,
	}
}

// NewParamExtractor creates a new LLM-based parameter extractor.
//
// # Description
//
// Creates a ParamExtractor that uses the specified model for semantic
// parameter extraction. The ChatClient interface allows any provider
// (Ollama, Anthropic, OpenAI, Gemini) to be used.
//
// CB-60: Accepts providers.ChatClient instead of *llm.MultiModelManager.
//
// # Inputs
//
//   - chatClient: ChatClient for sending extraction queries. Must not be nil.
//   - config: Extractor configuration.
//
// # Outputs
//
//   - *ParamExtractor: Configured extractor.
//   - error: Non-nil if chatClient is nil.
func NewParamExtractor(chatClient providers.ChatClient, config ParamExtractorConfig) (*ParamExtractor, error) {
	if chatClient == nil {
		return nil, fmt.Errorf("chatClient must not be nil")
	}

	return &ParamExtractor{
		chatClient: chatClient,
		config:     config,
		logger:     slog.Default(),
	}, nil
}

// IsEnabled returns true if the extractor is enabled.
//
// # Outputs
//
//   - bool: True if the feature flag is set.
//
// # Thread Safety
//
// Safe for concurrent use (reads immutable config).
func (e *ParamExtractor) IsEnabled() bool {
	return e.config.Enabled
}

// ParamSchema describes a single parameter for the LLM prompt.
// This is an alias for the agent-level interface type.
type ParamSchema = agent.ParamExtractorSchema

// ExtractParams uses the LLM to extract or correct tool parameters.
//
// # Description
//
// Takes the user query, tool name, parameter schema, and the regex-based
// extraction result. Sends these to the LLM which acts as a semantic parser
// to correct hierarchical scoping errors. Returns the corrected parameters
// or an error (caller should fall back to regex result).
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout.
//   - query: The user's natural language query.
//   - toolName: The name of the selected tool.
//   - paramSchemas: Parameter definitions for the tool.
//   - regexHint: The regex extractor's output (used as a hint).
//
// # Outputs
//
//   - map[string]any: Corrected parameter values.
//   - error: Non-nil if extraction fails (caller should use regexHint).
//
// # Thread Safety
//
// Safe for concurrent use.
func (e *ParamExtractor) ExtractParams(
	ctx context.Context,
	query string,
	toolName string,
	paramSchemas []ParamSchema,
	regexHint map[string]any,
) (map[string]any, error) {
	if !e.config.Enabled {
		return nil, fmt.Errorf("param extractor is disabled")
	}

	ctx, span := tracer.Start(ctx, "ParamExtractor.ExtractParams")
	defer span.End()

	span.SetAttributes(
		attribute.String("extractor.model", e.config.Model),
		attribute.String("extractor.tool", toolName),
		attribute.String("query_preview", truncate(query, 100)),
	)

	startTime := time.Now()

	// Apply timeout
	if e.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.config.Timeout)
		defer cancel()
	}

	// Build the prompt
	systemPrompt := e.buildSystemPrompt(toolName, paramSchemas, regexHint)
	userPrompt := fmt.Sprintf("User query: %s", query)

	messages := []datatypes.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	opts := providers.ChatOptions{
		Temperature: e.config.Temperature,
		MaxTokens:   e.config.MaxTokens,
		NumCtx:      e.config.NumCtx,
		KeepAlive:   e.config.KeepAlive,
		Model:       e.config.Model,
	}

	// Call the model
	response, err := e.chatClient.Chat(ctx, messages, opts)
	if err != nil {
		duration := time.Since(startTime)
		if ctx.Err() == context.DeadlineExceeded {
			span.SetStatus(codes.Error, "timeout")
			RecordParamExtractionLatency(e.config.Model, "timeout", duration.Seconds())
			RecordParamExtractionTotal(e.config.Model, "timeout")
			return nil, fmt.Errorf("param extraction timed out: %w", err)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "chat failed")
		RecordParamExtractionLatency(e.config.Model, "error", duration.Seconds())
		RecordParamExtractionTotal(e.config.Model, "error")
		return nil, fmt.Errorf("param extraction chat failed: %w", err)
	}

	// Parse the response
	result, err := e.parseResponse(response)
	if err != nil {
		duration := time.Since(startTime)
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse failed")
		RecordParamExtractionLatency(e.config.Model, "parse_error", duration.Seconds())
		RecordParamExtractionTotal(e.config.Model, "parse_error")
		return nil, fmt.Errorf("param extraction parse failed: %w", err)
	}

	duration := time.Since(startTime)

	// Log diff between regex and LLM when they disagree
	e.logParamDiff(toolName, regexHint, result)

	// Record success metrics
	RecordParamExtractionLatency(e.config.Model, "success", duration.Seconds())
	RecordParamExtractionTotal(e.config.Model, "success")

	span.SetAttributes(
		attribute.Int64("extractor.duration_ms", duration.Milliseconds()),
	)

	e.logger.Info("IT-08b: LLM param extraction succeeded",
		slog.String("tool", toolName),
		slog.Duration("duration", duration),
	)

	return result, nil
}

// buildSystemPrompt constructs the system prompt for parameter extraction.
func (e *ParamExtractor) buildSystemPrompt(toolName string, paramSchemas []ParamSchema, regexHint map[string]any) string {
	var sb strings.Builder

	sb.WriteString(`You are a parameter extraction assistant for code analysis tools.

Given a user's query about a codebase, extract the correct parameter values
for the selected tool. Pay attention to hierarchical scoping:
- Project names (flask, pandas, express, hugo, gin, nestjs, badger, babylonjs, plottable) are NOT package/module names
- Module/package names refer to specific subsystems WITHIN a project
- "in the Flask helpers module" -> package is "helpers", not "flask"
- "in the Pandas reshape module" -> package is "reshape", not "pandas"
- "in the Hugo hugolib package" -> package is "hugolib", not "hugo"
- If the user mentions ONLY a project name with no specific module, set "package" to "" (empty)
- "Find dead code in Express" -> package is "" (Express is the project, not a package)
- "Find dead code in Flask" -> package is "" (Flask is the project, not a package)

CONCEPTUAL vs LITERAL scope names:
- LITERAL package/directory names: "table", "hugolib", "io", "plots", "microservices"
  -> Set package to the literal name (it matches a real directory or Go package)
- CONCEPTUAL descriptions: "write path", "rendering pipeline", "materials subsystem",
  "Node class hierarchy", "groupby module" (when module = conceptual, not a directory)
  -> Set package to "" (empty). These are DESCRIPTIONS, not literal paths.
  A conceptual description with no matching directory will silently return empty results.
- RULE: If the scope name contains spaces or is an abstract concept, set package to ""

SINGLE-WORD CONCEPTUAL TRAPS — these look like package names but are NOT:
- "write path" -> package="" (conceptual, no "write" package exists)
- "read path" -> package="" (conceptual)
- "transaction layer" -> package="" (conceptual)
- "rendering pipeline" -> package="" (conceptual)
- "materials subsystem" -> package="" (conceptual)
- When in doubt about a SINGLE word like "materials", "rendering", "engine", "core":
  these CAN be directories but may NOT match the Package metadata field.
  Set package="" — empty is safe (returns global), a wrong literal silently returns zero.

LANGUAGE-SPECIFIC PACKAGE RULES:
- Go projects (gin, hugo, badger): Symbols have real Package metadata.
  Literal directory names ("table", "hugolib", "tpl") work as package filters.
- JS/TS projects (babylonjs, express, nestjs, plottable): Symbols have Package="".
  Package filters ALWAYS return empty for JS/TS. For subsystem scoping in JS/TS,
  set package to "" (empty) and let the tool use path-based filtering from context.
- Python projects (flask, pandas): Symbols have Package="".
  Same rule as JS/TS — use package="" for all Python scope queries.
- RULE: For JS/TS/Python projects, ALWAYS set package to "" (empty string).
  Only Go projects support non-empty package filters.

"from X to Y" pattern — CRITICAL EXTRACTION ORDER (for get_call_chain AND find_path):
- The word IMMEDIATELY AFTER "from" is the START symbol (X)
- The word IMMEDIATELY AFTER "to" is the END symbol (Y)
- NEVER reverse the order. NEVER swap X and Y.
- For get_call_chain: function_name = X (the start), direction = "downstream"
  ALWAYS use direction = "downstream" for "from X to Y" queries.
  NEVER set direction = "upstream" for "from...to" — the user is asking what X leads to.
  Example: "Trace the call chain from Scene.render to Engine._renderFrame"
           -> function_name = "render", direction = "downstream"
  Example: "Show the call chain from the main function to the page rendering logic"
           -> function_name = "main", direction = "downstream" (NOT upstream)
- For find_path: from = X (the start), to = Y (the target)
  Example: "Find the path from Scene.render to Engine._renderFrame"
           -> from = "Scene.render", to = "Engine._renderFrame"
  Example: "Find the shortest path from gin.New to adding a route"
           -> from = "New", to = "addRoute" (strip "gin." prefix)
  NOTE: find_path accepts Type.Method format (e.g., "Engine.ServeHTTP") because it resolves
  both endpoints separately. get_call_chain accepts bare function name only (e.g., "render")
  because it traces from a single starting point.
- Strip package qualifiers from symbols: "gin.New" -> "New", "flask.Blueprint" -> "Blueprint"
  (the graph indexes symbols without package prefixes)
- IMPORTANT: Use the ACTUAL parameter names listed in the Parameters section below,
  not the example names above. get_call_chain uses "function_name"/"direction";
  find_path uses "from"/"to".

Symbol name extraction rules:
- Distinguishing package.func from Type.Method:
  If the prefix is a known project/package name in lowercase (gin, flask, http, os, fmt),
  strip it: "gin.New" -> "New", "http.ListenAndServe" -> "ListenAndServe"
  If both parts are PascalCase or the prefix is a type name,
  keep the dot: "Engine.ServeHTTP" -> "Engine.ServeHTTP", "Context.JSON" -> "Context.JSON"
  When in doubt, keep the dot — ResolveFunctionWithFuzzy handles Type.Method resolution.
- Multi-word concepts should use PascalCase if they map to a type: "resource transformation" -> "ResourceTransformation"
- Algorithm names are NOT symbols: PageRank, BFS, DFS, Leiden, Tarjan, SCC — NEVER extract these as function_name or symbol targets

`)

	sb.WriteString(fmt.Sprintf("Tool: %s\n", toolName))
	sb.WriteString("Parameters:\n")

	for _, p := range paramSchemas {
		required := "optional"
		if p.Required {
			required = "required"
		}
		defaultStr := ""
		if p.Default != "" {
			defaultStr = fmt.Sprintf(", default: %s", p.Default)
		}
		sb.WriteString(fmt.Sprintf("  - %s (%s, %s%s): %s\n",
			p.Name, p.Type, required, defaultStr, p.Description))
	}

	// IT-08e: Include regex hint only when non-empty.
	// In parallel mode the hint is empty (extraction happens from scratch).
	// In serial/fallback mode the hint may contain regex results.
	if len(regexHint) > 0 {
		hintJSON, err := json.Marshal(regexHint)
		if err != nil {
			hintJSON = []byte("{}")
		}
		sb.WriteString(fmt.Sprintf(`
For reference, a regex-based extractor produced this guess (it frequently
makes errors — ignore it if it contradicts the query):
%s

`, string(hintJSON)))
	}

	sb.WriteString(`
Respond with ONLY a JSON object containing the parameter values.
Do not include any explanation or markdown formatting.
`)

	return sb.String()
}

// parseResponse extracts JSON parameters from the LLM response.
func (e *ParamExtractor) parseResponse(response string) (map[string]any, error) {
	response = strings.TrimSpace(response)

	if len(response) == 0 {
		return nil, fmt.Errorf("empty response from model")
	}

	// Clean up markdown code blocks
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Find JSON in response
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, fmt.Errorf("no JSON object found in response: %s", truncate(response, 100))
	}

	jsonStr := response[startIdx : endIdx+1]

	var result map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w, response: %s", err, truncate(jsonStr, 100))
	}

	return result, nil
}

// ResolveConceptualSymbol uses the LLM to pick the best symbol from candidates
// when the query uses conceptual descriptions instead of function names.
//
// # Description
//
// IT-12: When regex-based extraction produces words like "assigning" that
// don't match any symbol in the index, this method searches the index for
// keywords from the query, collects candidate symbols, and asks the LLM
// to pick the best starting point. This gives the LLM actual codebase
// knowledge it otherwise lacks.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout. Must not be nil.
//   - query: The user's natural language query.
//   - candidates: Symbol candidates found by keyword search of the index.
//
// # Outputs
//
//   - string: The best symbol name from the candidates.
//   - error: Non-nil if resolution fails (extractor disabled, no candidates,
//     LLM returns invalid name).
//
// # Thread Safety
//
// Safe for concurrent use.
func (e *ParamExtractor) ResolveConceptualSymbol(
	ctx context.Context,
	query string,
	candidates []agent.SymbolCandidate,
) (string, error) {
	if !e.config.Enabled {
		return "", fmt.Errorf("conceptual resolution disabled: extractor not enabled")
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("conceptual resolution failed: no candidates provided")
	}

	ctx, span := tracer.Start(ctx, "ParamExtractor.ResolveConceptualSymbol")
	defer span.End()

	span.SetAttributes(
		attribute.String("extractor.model", e.config.Model),
		attribute.Int("candidates.count", len(candidates)),
		attribute.String("query_preview", truncate(query, 100)),
	)

	startTime := time.Now()

	// Apply timeout
	if e.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.config.Timeout)
		defer cancel()
	}

	// Build focused prompt
	var sb strings.Builder
	sb.WriteString(`You are a symbol resolution assistant. The user described a code concept in natural language. Below are real symbols from the codebase that match keywords in the query. Pick the single symbol that is the BEST starting point for tracing call chains.

Rules:
- Pick a function/method that IMPLEMENTS the behavior, not a simple accessor or getter
- CRITICAL: If the query mentions a specific domain noun (like "menu", "table", "axis", "user"), ALWAYS prefer symbols whose NAME CONTAINS that noun, even if they have fewer call edges. "assembleMenus" is ALWAYS better for "menu assembly" than "Build"
- When NO candidate name contains the domain noun, THEN prefer functions with the most call edges as a tiebreaker
- Prefer internal/private methods over public accessors — they have richer call chains
- For "assigning X": pick _setX or applyX over a bare property named X
- For "rendering": pick render() or _render() over a getRender accessor
- For "initialization": pick Build, New, Init, or Setup over a getter
- If the query says "from X to Y", pick the symbol closest to X (the starting point)
- Return ONLY the symbol name, nothing else

`)
	sb.WriteString("Available symbols:\n")
	cap := len(candidates)
	if cap > 50 {
		cap = 50
	}
	// IT-12 Rev 4: Show edge counts when available to help the LLM pick
	// high-connectivity symbols (better starting points for path/chain queries).
	hasEdgeInfo := false
	for i := 0; i < cap; i++ {
		if candidates[i].OutEdges > 0 || candidates[i].InEdges > 0 {
			hasEdgeInfo = true
			break
		}
	}
	for i := 0; i < cap; i++ {
		c := candidates[i]
		if hasEdgeInfo {
			sb.WriteString(fmt.Sprintf("  - %s (%s) in %s:%d [%d calls out, %d calls in]\n",
				c.Name, c.Kind, c.FilePath, c.Line, c.OutEdges, c.InEdges))
		} else {
			sb.WriteString(fmt.Sprintf("  - %s (%s) in %s:%d\n", c.Name, c.Kind, c.FilePath, c.Line))
		}
	}

	// IT-12 Rev 5c: Log the candidate list as presented to the LLM for debugging.
	// Exact tier counts are logged by the caller in resolveConceptualName().
	var candidatePreview strings.Builder
	for i := 0; i < cap && i < 15; i++ {
		c := candidates[i]
		candidatePreview.WriteString(fmt.Sprintf("[%d] %s (%s, %d out, %d in) ", i+1, c.Name, c.Kind, c.OutEdges, c.InEdges))
	}
	e.logger.Info("IT-12: candidates presented to LLM",
		slog.Int("total_candidates", len(candidates)),
		slog.Int("shown_to_llm", cap),
		slog.String("top_15", candidatePreview.String()),
	)

	messages := []datatypes.Message{
		{Role: "system", Content: sb.String()},
		{Role: "user", Content: fmt.Sprintf("Query: %s\n\nBest starting symbol:", query)},
	}

	opts := providers.ChatOptions{
		Temperature: 0.1,
		MaxTokens:   64, // Only need a symbol name
		NumCtx:      e.config.NumCtx,
		KeepAlive:   e.config.KeepAlive,
		Model:       e.config.Model,
	}

	response, err := e.chatClient.Chat(ctx, messages, opts)
	if err != nil {
		duration := time.Since(startTime)
		span.RecordError(err)
		span.SetStatus(codes.Error, "chat failed")
		RecordParamExtractionLatency(e.config.Model, "conceptual_error", duration.Seconds())
		RecordParamExtractionTotal(e.config.Model, "conceptual_error")
		return "", fmt.Errorf("conceptual resolution chat failed: %w", err)
	}

	duration := time.Since(startTime)

	// IT-12 Rev 5c: Log raw LLM response for debugging.
	e.logger.Info("IT-12: LLM raw response for conceptual resolution",
		slog.String("raw_response", response),
		slog.Duration("duration", duration),
	)

	// Parse: response should be just a symbol name, but small LLMs often include
	// extra context like "material (method) in file.ts:614". Clean up the response.
	symbolName := strings.TrimSpace(response)

	// Strip markdown formatting (backticks, bold)
	symbolName = strings.Trim(symbolName, "`*")

	// Validate it's actually in our candidate list (exact match on full response)
	for _, c := range candidates {
		if c.Name == symbolName {
			span.SetAttributes(attribute.String("resolved_symbol", symbolName))
			RecordParamExtractionLatency(e.config.Model, "conceptual_success", duration.Seconds())
			RecordParamExtractionTotal(e.config.Model, "conceptual_success")
			e.logger.Info("IT-12: conceptual symbol resolution succeeded",
				slog.String("resolved", symbolName),
				slog.Duration("duration", duration),
			)
			return symbolName, nil
		}
	}

	// Try extracting just the first token (before any space, paren, or comma).
	// Handles: "material (method) in file.ts:614" → "material"
	firstToken := symbolName
	for _, sep := range []string{" ", "(", ",", "\t", "\n"} {
		if idx := strings.Index(firstToken, sep); idx > 0 {
			firstToken = firstToken[:idx]
		}
	}
	firstToken = strings.Trim(firstToken, "`*\"'")

	if firstToken != symbolName {
		for _, c := range candidates {
			if c.Name == firstToken {
				span.SetAttributes(attribute.String("resolved_symbol", c.Name))
				RecordParamExtractionLatency(e.config.Model, "conceptual_success", duration.Seconds())
				RecordParamExtractionTotal(e.config.Model, "conceptual_success")
				e.logger.Info("IT-12: conceptual symbol resolution succeeded (first-token match)",
					slog.String("llm_response", symbolName),
					slog.String("resolved", c.Name),
					slog.Duration("duration", duration),
				)
				return c.Name, nil
			}
		}
	}

	// Try partial match (LLM might return "Type.Method" when candidate is just "Method")
	for _, c := range candidates {
		if strings.HasSuffix(symbolName, "."+c.Name) || strings.HasSuffix(symbolName, c.Name) {
			span.SetAttributes(attribute.String("resolved_symbol", c.Name))
			RecordParamExtractionLatency(e.config.Model, "conceptual_success", duration.Seconds())
			RecordParamExtractionTotal(e.config.Model, "conceptual_success")
			e.logger.Info("IT-12: conceptual symbol resolution succeeded (partial match)",
				slog.String("llm_response", symbolName),
				slog.String("resolved", c.Name),
				slog.Duration("duration", duration),
			)
			return c.Name, nil
		}
	}

	// Try matching any candidate name that appears anywhere in the response.
	// Handles: "The best symbol is _setMaterial because..." → "_setMaterial"
	for _, c := range candidates {
		if strings.Contains(symbolName, c.Name) && len(c.Name) > 2 {
			span.SetAttributes(attribute.String("resolved_symbol", c.Name))
			RecordParamExtractionLatency(e.config.Model, "conceptual_success", duration.Seconds())
			RecordParamExtractionTotal(e.config.Model, "conceptual_success")
			e.logger.Info("IT-12: conceptual symbol resolution succeeded (contains match)",
				slog.String("llm_response", symbolName),
				slog.String("resolved", c.Name),
				slog.Duration("duration", duration),
			)
			return c.Name, nil
		}
	}

	span.SetAttributes(attribute.String("llm_response", symbolName))
	RecordParamExtractionLatency(e.config.Model, "conceptual_mismatch", duration.Seconds())
	RecordParamExtractionTotal(e.config.Model, "conceptual_mismatch")
	return "", fmt.Errorf("LLM returned '%s' which is not in candidate list", symbolName)
}

// logParamDiff logs differences between regex and LLM extraction results.
func (e *ParamExtractor) logParamDiff(toolName string, regexHint, llmResult map[string]any) {
	for key, llmVal := range llmResult {
		regexVal, exists := regexHint[key]
		if !exists {
			e.logger.Info("IT-08b: LLM added param not in regex result",
				slog.String("tool", toolName),
				slog.String("param", key),
				slog.Any("llm_value", llmVal),
			)
			continue
		}
		if fmt.Sprintf("%v", regexVal) != fmt.Sprintf("%v", llmVal) {
			e.logger.Info("IT-08b: LLM corrected regex extraction",
				slog.String("tool", toolName),
				slog.String("param", key),
				slog.Any("regex_value", regexVal),
				slog.Any("llm_value", llmVal),
			)
		}
	}
}
