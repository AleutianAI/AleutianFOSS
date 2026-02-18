// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

// execute_context.go contains context building and semantic analysis functions
// extracted from execute.go as part of CB-30c Phase 2 decomposition.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/routing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Context Building Functions
// -----------------------------------------------------------------------------

// buildCodeContext creates a CodeContext from phase dependencies.
//
// Description:
//
//	Builds a rich CodeContext for history-aware routing. Includes full tool
//	history with summaries to leverage Mamba2's long-context efficiency.
//	The router can see what tools were already called and what they found,
//	enabling it to suggest the NEXT logical tool rather than repeating.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	*agent.ToolRouterCodeContext - Context for the router.
func (p *ExecutePhase) buildCodeContext(deps *Dependencies) *agent.ToolRouterCodeContext {
	ctx := &agent.ToolRouterCodeContext{}

	// Extract info from assembled context if available
	if deps.Context != nil {
		ctx.Files = len(deps.Context.CodeContext)
		ctx.Symbols = countSymbolsInContext(deps.Context)

		// Detect language from code entries
		ctx.Language = detectLanguageFromContext(deps.Context)
	}

	// Get recent tools from session history if available (legacy support)
	ctx.RecentTools = getRecentToolsFromSession(deps.Session)

	// Get recent tool errors for router feedback
	if deps.Session != nil {
		ctx.PreviousErrors = deps.Session.GetRecentToolErrors()

		// Build tool history with summaries for history-aware routing
		ctx.ToolHistory = buildToolHistoryFromSession(deps.Session)

		// Build progress summary
		ctx.Progress = buildProgressSummary(deps.Session)

		// Set step number
		if deps.EventEmitter != nil {
			ctx.StepNumber = deps.EventEmitter.CurrentStep()
		}
	}

	return ctx
}

// maxToolHistoryEntries limits the number of tool history entries passed to the router.
// This keeps context manageable while still providing sufficient history for
// the router to make informed decisions about the next tool.
const maxToolHistoryEntries = 10

// countToolCalls counts how many times a specific tool appears in the history.
//
// Inputs:
//
//	history - Tool history entries.
//	toolName - The tool name to count.
//
// Outputs:
//
//	int - Number of times the tool was called.
func countToolCalls(history []agent.ToolHistoryEntry, toolName string) int {
	count := 0
	for _, entry := range history {
		if entry.Tool == toolName {
			count++
		}
	}
	return count
}

// buildToolCountMapFromSession builds a map of tool name to call count from session trace steps.
//
// Description:
//
//	Iterates through the session's trace steps and counts calls per tool.
//	Counts both "tool_call" and "tool_call_forced" actions to capture calls
//	from both router and LLM paths.
//
//	GR-39b: This is more accurate than ToolHistory which only captures router
//	path calls. TraceSteps capture ALL tool executions regardless of path.
//
//	Optimization: Build the map once before the invocation loop (O(n+m))
//	instead of counting per-invocation (O(n*m)).
//
// Inputs:
//
//	s - The session to count from. May be nil (returns empty map).
//
// Outputs:
//
//	map[string]int - Map of tool name to call count.
//
// Thread Safety: Safe for concurrent use (reads snapshot of trace steps).
func buildToolCountMapFromSession(s *agent.Session) map[string]int {
	counts := make(map[string]int)
	if s == nil {
		return counts
	}

	steps := s.GetTraceSteps()
	for _, step := range steps {
		// Count both tool_call and tool_call_forced actions
		// CB-31d: tool_call_forced is used by router hard-forcing path
		if step.Action == "tool_call" || step.Action == "tool_call_forced" {
			counts[step.Tool]++
		}
	}

	return counts
}

// -----------------------------------------------------------------------------
// Semantic Repetition Detection
// -----------------------------------------------------------------------------

// semanticRepetitionThreshold is the Jaccard similarity threshold above which
// tool calls are considered semantically repetitive.
// CB-30c: Value of 0.7 means 70% of query terms must overlap.
const semanticRepetitionThreshold = 0.7

// maxSemanticHistorySteps limits how far back we look for semantic repetition.
// Only check the last N steps for performance.
const maxSemanticHistorySteps = 5

// defaultMinHashThreshold is the term count above which we switch to MinHash.
// Below this, simple Jaccard is faster.
const defaultMinHashThreshold = 50

// =============================================================================
// RepetitionDetector - CB-30c Phase 3
// =============================================================================

// RepetitionDetector detects semantically repetitive tool calls.
//
// Description:
//
//	Encapsulates semantic repetition detection with configurable options.
//	Uses simple Jaccard similarity for small term sets, and can optionally
//	use MinHash for larger sets (though this is rarely needed for queries).
//
//	Key Features:
//	- Configurable similarity threshold
//	- Configurable history depth
//	- Tracks alternative suggestions based on what's been tried
//	- OTel tracing built-in
//
// Thread Safety: Safe for concurrent use. All mutable state is protected by mu.
type RepetitionDetector struct {
	// mu protects all mutable state.
	mu sync.Mutex

	// threshold is the Jaccard similarity threshold (0.0-1.0).
	threshold float64

	// maxHistory is how many recent steps to check.
	maxHistory int

	// triedQueries tracks queries that have been tried per tool (for suggesting alternatives).
	// Key: tool name, Value: list of query terms sets
	triedQueries map[string][]map[string]bool

	// minHashThreshold is the term count above which MinHash is used.
	minHashThreshold int
}

// RepetitionDetectorOption configures a RepetitionDetector.
type RepetitionDetectorOption func(*RepetitionDetector)

// WithRepetitionThreshold sets the similarity threshold.
func WithRepetitionThreshold(threshold float64) RepetitionDetectorOption {
	return func(d *RepetitionDetector) {
		if threshold >= 0 && threshold <= 1 {
			d.threshold = threshold
		}
	}
}

// WithRepetitionHistory sets the history depth.
func WithRepetitionHistory(maxHistory int) RepetitionDetectorOption {
	return func(d *RepetitionDetector) {
		if maxHistory > 0 {
			d.maxHistory = maxHistory
		}
	}
}

// NewRepetitionDetector creates a new RepetitionDetector with options.
//
// Description:
//
//	Creates a detector with sensible defaults that can be overridden.
//	Default threshold is 0.7 (70% similarity), history is 5 steps.
//
// Inputs:
//
//	opts - Optional configuration functions.
//
// Outputs:
//
//	*RepetitionDetector - The configured detector.
func NewRepetitionDetector(opts ...RepetitionDetectorOption) *RepetitionDetector {
	d := &RepetitionDetector{
		threshold:        semanticRepetitionThreshold,
		maxHistory:       maxSemanticHistorySteps,
		triedQueries:     make(map[string][]map[string]bool),
		minHashThreshold: defaultMinHashThreshold,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// IsSemanticallyRepetitive checks if a query is similar to recently tried queries.
//
// Description:
//
//	Compares the proposed query against recent queries for the same tool.
//	Uses Jaccard similarity on extracted terms.
//
// Inputs:
//
//	ctx - Context for tracing.
//	tool - The tool name.
//	query - The query string to check.
//	steps - Recent trace steps from session.
//
// Outputs:
//
//	isRepetitive - True if above threshold.
//	similarity - The maximum similarity found.
//	similarQuery - The most similar previous query.
func (d *RepetitionDetector) IsSemanticallyRepetitive(
	ctx context.Context,
	tool string,
	query string,
	steps []crs.TraceStep,
) (isRepetitive bool, similarity float64, similarQuery string) {
	ctx, span := executePhaseTracer.Start(ctx, "RepetitionDetector.IsSemanticallyRepetitive",
		trace.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("query_preview", truncateQuery(query, 50)),
			attribute.Float64("threshold", d.threshold),
		),
	)
	defer span.End()

	if query == "" || len(steps) == 0 {
		return false, 0, ""
	}

	currentTerms := extractQueryTerms(query)
	if len(currentTerms) == 0 {
		return false, 0, ""
	}

	// Check last N steps
	maxSim := 0.0
	simQuery := ""
	startIdx := len(steps) - d.maxHistory
	if startIdx < 0 {
		startIdx = 0
	}

	queryParamNames := []string{"pattern", "query", "search", "symbol", "name", "path", "target", "function_name"}

	for i := len(steps) - 1; i >= startIdx; i-- {
		step := steps[i]
		if step.Tool != tool {
			continue
		}

		// Extract query from Metadata
		prevQuery := ""
		for _, paramName := range queryParamNames {
			if val, ok := step.Metadata[paramName]; ok && val != "" {
				prevQuery = val
				break
			}
		}
		if prevQuery == "" {
			continue
		}

		prevTerms := extractQueryTerms(prevQuery)
		if len(prevTerms) == 0 {
			continue
		}

		sim := jaccardSimilarity(currentTerms, prevTerms)
		if sim > maxSim {
			maxSim = sim
			simQuery = prevQuery
		}
	}

	span.SetAttributes(
		attribute.Float64("max_similarity", maxSim),
		attribute.Bool("is_repetitive", maxSim >= d.threshold),
	)

	if maxSim >= d.threshold {
		span.AddEvent("repetition_detected",
			trace.WithAttributes(attribute.String("similar_to", simQuery)),
		)
		return true, maxSim, simQuery
	}

	return false, maxSim, ""
}

// Record records a tool call for tracking tried queries.
//
// Description:
//
//	Tracks what queries have been tried for each tool, enabling
//	the SuggestAlternative method to suggest different approaches.
//
// Inputs:
//
//	tool - The tool that was called.
//	query - The query that was used.
//
// Thread Safety: Safe for concurrent use.
func (d *RepetitionDetector) Record(tool, query string) {
	terms := extractQueryTerms(query)
	if len(terms) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.triedQueries[tool] = append(d.triedQueries[tool], terms)
}

// SuggestAlternative suggests a different tool or approach based on what's been tried.
//
// Description:
//
//	Analyzes tried queries and suggests alternatives. For example, if
//	multiple Grep calls have been tried with similar patterns, might
//	suggest using find_callers or find_symbol instead.
//
// Inputs:
//
//	currentTool - The tool being considered.
//
// Outputs:
//
//	string - A suggestion, or empty if no suggestion.
//
// Thread Safety: Safe for concurrent use.
func (d *RepetitionDetector) SuggestAlternative(currentTool string) string {
	d.mu.Lock()
	tried := d.triedQueries[currentTool]
	triedLen := len(tried)
	d.mu.Unlock()

	if triedLen < 2 {
		return ""
	}

	// If Grep has been tried multiple times, suggest graph tools
	if currentTool == "Grep" && triedLen >= 2 {
		return "Consider using find_callers, find_callees, or find_symbol instead of text search"
	}

	// If find_symbol tried multiple times, suggest broader search
	if currentTool == "find_symbol" && triedLen >= 2 {
		return "Consider using explore_package or graph_overview for broader context"
	}

	return ""
}

// Reset clears the detector's state for a new session.
//
// Thread Safety: Safe for concurrent use.
func (d *RepetitionDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.triedQueries = make(map[string][]map[string]bool)
}

// checkSemanticRepetition checks if the proposed tool call is semantically similar
// to recent tool calls with the same tool name.
//
// Description:
//
//	Uses Jaccard similarity on query/pattern parameters to detect when
//	the agent is calling the same tool with slightly different but semantically
//	equivalent queries. This prevents loops like:
//	- Grep("parseConfig") → Grep("parse_config") → Grep("ParseConfig")
//
//	CB-30c: Uses TraceSteps (which have Metadata with actual params) instead of
//	CRS StepRecords (which don't populate ToolParams.Query).
//
// Inputs:
//
//	ctx - Context for tracing. Must not be nil.
//	deps - Phase dependencies containing session.
//	tool - The tool name being proposed.
//	query - The query string from the current tool call (extracted from params).
//
// Outputs:
//
//	bool - True if this is a semantic repetition.
//	float64 - The maximum similarity score found.
//	string - The query from history that was most similar.
//
// Thread Safety: Safe for concurrent use (reads from session trace).
func (p *ExecutePhase) checkSemanticRepetition(
	ctx context.Context,
	deps *Dependencies,
	tool string,
	query string,
) (bool, float64, string) {
	ctx, span := executePhaseTracer.Start(ctx, "ExecutePhase.checkSemanticRepetition",
		trace.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("query_preview", truncateQuery(query, 50)),
		),
	)
	defer span.End()

	if deps.Session == nil || query == "" {
		return false, 0, ""
	}

	// Get trace steps from session (these have Metadata with actual params)
	steps := deps.Session.GetTraceSteps()
	if len(steps) == 0 {
		return false, 0, ""
	}

	// Check last N steps for same tool with similar query
	maxSimilarity := 0.0
	similarQuery := ""
	startIdx := len(steps) - maxSemanticHistorySteps
	if startIdx < 0 {
		startIdx = 0
	}

	currentTerms := extractQueryTerms(query)
	if len(currentTerms) == 0 {
		return false, 0, ""
	}

	// GR-39a: Use shared queryParamNames for consistent deduplication across all tools
	for i := len(steps) - 1; i >= startIdx; i-- {
		step := steps[i]

		// Only compare same tool (step.Tool contains the tool name)
		if step.Tool != tool {
			continue
		}

		// Extract query from Metadata
		prevQuery := ""
		for _, paramName := range queryParamNames {
			if val, ok := step.Metadata[paramName]; ok && val != "" {
				prevQuery = val
				break
			}
		}

		if prevQuery == "" {
			continue
		}

		// GR-38 Issue 14: Check for EXACT duplicate first (fast path)
		// This catches cases like find_callees("main") called twice
		if strings.EqualFold(query, prevQuery) {
			span.AddEvent("exact_duplicate_detected",
				trace.WithAttributes(
					attribute.String("tool", tool),
					attribute.String("query", query),
				),
			)
			return true, 1.0, prevQuery
		}

		prevTerms := extractQueryTerms(prevQuery)
		if len(prevTerms) == 0 {
			continue
		}

		// Calculate Jaccard similarity
		similarity := jaccardSimilarity(currentTerms, prevTerms)

		if similarity > maxSimilarity {
			maxSimilarity = similarity
			similarQuery = prevQuery
		}
	}

	span.SetAttributes(
		attribute.Float64("max_similarity", maxSimilarity),
		attribute.Float64("threshold", semanticRepetitionThreshold),
		attribute.Bool("is_repetitive", maxSimilarity >= semanticRepetitionThreshold),
	)

	if maxSimilarity >= semanticRepetitionThreshold {
		span.AddEvent("semantic_repetition_detected",
			trace.WithAttributes(
				attribute.String("similar_to_query", similarQuery),
			),
		)
		return true, maxSimilarity, similarQuery
	}

	return false, maxSimilarity, ""
}

// extractQueryTerms extracts terms from a query string for Jaccard comparison.
//
// Description:
//
//	Tokenizes the query into lowercase terms, normalizing for comparison.
//	Handles common delimiters like spaces, underscores, and camelCase.
//
// Inputs:
//
//	query - The query string to tokenize.
//
// Outputs:
//
//	map[string]bool - Set of unique lowercase terms.
func extractQueryTerms(query string) map[string]bool {
	terms := make(map[string]bool)

	// Split on common delimiters
	query = strings.ToLower(query)
	query = strings.ReplaceAll(query, "_", " ")
	query = strings.ReplaceAll(query, "-", " ")
	query = strings.ReplaceAll(query, ".", " ")
	query = strings.ReplaceAll(query, "/", " ")

	// Split camelCase: "parseConfig" → "parse config"
	var expanded strings.Builder
	for i, r := range query {
		if i > 0 && r >= 'A' && r <= 'Z' {
			expanded.WriteRune(' ')
		}
		expanded.WriteRune(r)
	}
	query = expanded.String()

	// Extract words
	words := strings.Fields(query)
	for _, word := range words {
		if len(word) >= 2 { // Skip single chars
			terms[word] = true
		}
	}

	return terms
}

// jaccardSimilarity calculates the Jaccard similarity between two term sets.
//
// Description:
//
//	Jaccard = |intersection| / |union|
//	Returns 0.0 if either set is empty, 1.0 if identical.
//
// Inputs:
//
//	a, b - Term sets to compare.
//
// Outputs:
//
//	float64 - Similarity score in range [0.0, 1.0].
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	intersectionCount := 0
	for term := range a {
		if b[term] {
			intersectionCount++
		}
	}

	// |union| = |a| + |b| - |intersection|
	unionCount := len(a) + len(b) - intersectionCount

	if unionCount == 0 {
		return 0.0
	}

	return float64(intersectionCount) / float64(unionCount)
}

// extractToolQuery extracts the query/pattern parameter from a tool invocation.
//
// Description:
//
//	Different tools use different parameter names for their "query" concept.
//	This function extracts the relevant parameter for semantic comparison.
//
// Inputs:
//
//	inv - The tool invocation to extract from.
//
// Outputs:
//
//	string - The query/pattern string, or empty if not found.
//
// queryParamNames contains parameter names used for semantic deduplication.
// GR-39a: Consolidated list used by all 3 dedup layers (UCB1, CB-30c, GR-39a batch filter).
// When adding a new tool, add its primary query parameter here.
var queryParamNames = []string{
	// Original params (GR-38)
	"pattern", "query", "search", "symbol", "name", "path", "target", "function_name", "file_path",
	// GR-39a: Added missing params from tool definitions
	"package",        // explore_package
	"symbol_name",    // symbol search tools
	"interface_name", // find_implementers
	"symbol_id",      // symbol lookup tools
	"direction",      // analyze_data_flow
}

func extractToolQuery(inv *agent.ToolInvocation) string {
	if inv == nil || inv.Parameters == nil {
		return ""
	}

	// Check StringParams first
	if inv.Parameters.StringParams != nil {
		for _, name := range queryParamNames {
			if val, ok := inv.Parameters.StringParams[name]; ok && val != "" {
				return val
			}
		}
	}

	// Fallback: try to parse from RawJSON
	if len(inv.Parameters.RawJSON) > 0 {
		var rawParams map[string]interface{}
		if err := json.Unmarshal(inv.Parameters.RawJSON, &rawParams); err == nil {
			for _, name := range queryParamNames {
				if val, ok := rawParams[name]; ok {
					if strVal, isStr := val.(string); isStr && strVal != "" {
						return strVal
					}
				}
			}
		}
	}

	return ""
}

// -----------------------------------------------------------------------------
// Token Estimation
// -----------------------------------------------------------------------------

// estimateToolResultTokens estimates token count for tool output.
//
// Description:
//
//	Uses tiered estimation based on content type, as per CB-30c review:
//	- JSON is denser (~3.5 chars/token)
//	- Code is sparser (~5 chars/token)
//	- Default prose (~4 chars/token)
//
// Inputs:
//
//	result - The tool output as a string.
//
// Outputs:
//
//	int - Estimated token count.
func estimateToolResultTokens(result string) int {
	if len(result) == 0 {
		return 0
	}

	trimmed := strings.TrimSpace(result)

	// JSON is denser (~3.5 chars/token)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return len(result) * 10 / 35
	}

	// Code is sparser (~5 chars/token due to whitespace and indentation)
	if strings.Contains(result, "func ") || strings.Contains(result, "def ") ||
		strings.Contains(result, "class ") || strings.Contains(result, "package ") {
		return len(result) / 5
	}

	// Default prose (~4 chars/token)
	return len(result) / 4
}

// =============================================================================
// TokenTracker - CB-30c Phase 3
// =============================================================================

// defaultTokenBudget is the default total token budget for a session.
// This is a conservative default; can be overridden per session.
const defaultTokenBudget = 100000

// synthesisThreshold is the percentage of budget at which synthesis is recommended.
const synthesisThreshold = 0.85

// TokenTracker tracks token usage and budget for a session.
//
// Description:
//
//	Provides centralized token tracking with budget management.
//	Tracks prompt, response, and tool result tokens separately.
//	Can recommend synthesis when approaching budget limits.
//
// Thread Safety: Safe for concurrent use. All mutable state is protected by mu.
type TokenTracker struct {
	// mu protects all mutable state.
	mu sync.RWMutex

	// promptTokens is total prompt tokens sent to LLM.
	promptTokens int

	// responseTokens is total response tokens from LLM.
	responseTokens int

	// toolTokens is estimated tokens from tool results.
	toolTokens int

	// budget is the total token budget for this session.
	budget int
}

// TokenTrackerOption configures a TokenTracker.
type TokenTrackerOption func(*TokenTracker)

// WithTokenBudget sets the token budget.
func WithTokenBudget(budget int) TokenTrackerOption {
	return func(t *TokenTracker) {
		if budget > 0 {
			t.budget = budget
		}
	}
}

// NewTokenTracker creates a new TokenTracker with options.
//
// Description:
//
//	Creates a tracker with default budget that can be overridden.
//
// Inputs:
//
//	opts - Optional configuration functions.
//
// Outputs:
//
//	*TokenTracker - The configured tracker.
func NewTokenTracker(opts ...TokenTrackerOption) *TokenTracker {
	t := &TokenTracker{
		budget: defaultTokenBudget,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// AddToolResult estimates and adds token count for a tool result.
//
// Description:
//
//	Uses tiered estimation based on content type.
//
// Inputs:
//
//	result - The tool output string.
//
// Outputs:
//
//	int - Estimated token count added.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) AddToolResult(result string) int {
	tokens := estimateToolResultTokens(result)
	t.mu.Lock()
	t.toolTokens += tokens
	t.mu.Unlock()
	return tokens
}

// AddLLMUsage records prompt and response tokens from an LLM call.
//
// Inputs:
//
//	promptTokens - Tokens in the prompt.
//	responseTokens - Tokens in the response.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) AddLLMUsage(promptTokens, responseTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.promptTokens += promptTokens
	t.responseTokens += responseTokens
}

// TotalUsed returns the total tokens used across all categories.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) TotalUsed() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.promptTokens + t.responseTokens + t.toolTokens
}

// RemainingBudget returns the remaining token budget.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) RemainingBudget() int {
	t.mu.RLock()
	total := t.promptTokens + t.responseTokens + t.toolTokens
	budget := t.budget
	t.mu.RUnlock()

	remaining := budget - total
	if remaining < 0 {
		return 0
	}
	return remaining
}

// UsagePercent returns the percentage of budget used (0.0-1.0).
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) UsagePercent() float64 {
	t.mu.RLock()
	budget := t.budget
	total := t.promptTokens + t.responseTokens + t.toolTokens
	t.mu.RUnlock()

	if budget == 0 {
		return 0
	}
	return float64(total) / float64(budget)
}

// ShouldSynthesize returns true if synthesis is recommended based on budget.
//
// Description:
//
//	Returns true when usage exceeds the synthesis threshold (default 85%).
//	This signals that the agent should synthesize an answer rather than
//	continue making tool calls.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) ShouldSynthesize() bool {
	return t.UsagePercent() >= synthesisThreshold
}

// GetBreakdown returns a breakdown of token usage by category.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) GetBreakdown() map[string]int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return map[string]int{
		"prompt":   t.promptTokens,
		"response": t.responseTokens,
		"tool":     t.toolTokens,
		"total":    t.promptTokens + t.responseTokens + t.toolTokens,
		"budget":   t.budget,
	}
}

// Reset clears the tracker for a new session.
//
// Thread Safety: Safe for concurrent use.
func (t *TokenTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.promptTokens = 0
	t.responseTokens = 0
	t.toolTokens = 0
}

// -----------------------------------------------------------------------------
// Error Categorization
// -----------------------------------------------------------------------------

// isNotFoundError checks if an error message indicates a definitive "not found" result.
//
// Description:
//
//	Phase 11B (Feb 14, 2026): Detects when a tool has definitively determined
//	that a symbol/file/resource does not exist. These are VALID RESULTS, not
//	errors that should trigger retries.
//
// Inputs:
//
//	errMsg - The error message from tool execution.
//
// Outputs:
//
//	bool - True if this is a definitive "not found" result.
//
// Thread Safety: Safe for concurrent use (stateless function).
func isNotFoundError(errMsg string) bool {
	if errMsg == "" {
		return false
	}

	errLower := strings.ToLower(errMsg)

	// Check for explicit "not found" patterns
	notFoundPatterns := []string{
		"not found",
		"does not exist",
		"could not locate",
		"unable to find",
		"no function",
		"no symbol",
		"no matches",
		"target(s) not found", // find_common_dependency specific
		"symbol not found",    // find_symbol specific
	}

	for _, pattern := range notFoundPatterns {
		if strings.Contains(errLower, pattern) {
			return true
		}
	}

	return false
}

// isSurrenderResponse detects when the LLM gives up instead of answering.
//
// Description:
//
//	Checks short LLM responses (< 50 chars) for "I don't know" style phrases.
//	This catches the edge case where the LLM surrenders despite having valid
//	tool results in the session that it could summarize.
//
// Inputs:
//
//   - response: The LLM response text.
//
// Outputs:
//
//   - bool: True if the response is a surrender.
//
// Thread Safety: This function is safe for concurrent use.
func isSurrenderResponse(response string) bool {
	lower := strings.ToLower(strings.TrimSpace(response))
	if len(lower) > 50 {
		return false // Only check short responses
	}
	surrenderPhrases := []string{
		"i don't know",
		"i do not know",
		"i'm not sure",
		"i cannot determine",
		"i'm unable to",
		"i am unable to",
	}
	for _, phrase := range surrenderPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// categorizeToolError maps error messages to ErrorCategory for CDCL learning.
//
// Description:
//
//	Analyzes error messages to categorize them for clause generation.
//	This enables more specific clauses that block tool+error combinations.
//
// Inputs:
//
//	errMsg - The error message from tool execution.
//
// Outputs:
//
//	crs.ErrorCategory - The error category.
func categorizeToolError(errMsg string) crs.ErrorCategory {
	errLower := strings.ToLower(errMsg)

	switch {
	case strings.Contains(errLower, "not found") ||
		strings.Contains(errLower, "no such file") ||
		strings.Contains(errLower, "does not exist") ||
		strings.Contains(errLower, "enoent"): // CR-8: Unix errno
		return crs.ErrorCategoryToolNotFound

	case strings.Contains(errLower, "invalid param") ||
		strings.Contains(errLower, "invalid argument") ||
		strings.Contains(errLower, "missing required") ||
		strings.Contains(errLower, "einval"): // CR-8: Unix errno
		return crs.ErrorCategoryInvalidParams

	case strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "timed out") ||
		strings.Contains(errLower, "deadline") ||
		strings.Contains(errLower, "i/o timeout") || // CR-8: Go net timeout
		strings.Contains(errLower, "context deadline"): // CR-8: Context timeout
		return crs.ErrorCategoryTimeout

	case strings.Contains(errLower, "rate limit") ||
		strings.Contains(errLower, "too many requests") ||
		strings.Contains(errLower, "429"): // CR-8: HTTP status
		return crs.ErrorCategoryRateLimited

	case strings.Contains(errLower, "permission") ||
		strings.Contains(errLower, "access denied") ||
		strings.Contains(errLower, "forbidden") ||
		strings.Contains(errLower, "eperm") || // CR-8: Unix errno
		strings.Contains(errLower, "eacces"): // CR-8: Unix errno
		return crs.ErrorCategoryPermission

	case strings.Contains(errLower, "network") ||
		strings.Contains(errLower, "connection") ||
		strings.Contains(errLower, "eof") || // CR-8: Unexpected EOF
		strings.Contains(errLower, "broken pipe") || // CR-8: Unix
		strings.Contains(errLower, "reset by peer"): // CR-8: TCP reset
		return crs.ErrorCategoryNetwork

	default:
		return crs.ErrorCategoryInternal
	}
}

// -----------------------------------------------------------------------------
// Tool History Building
// -----------------------------------------------------------------------------

// buildToolHistoryFromSession extracts tool history with summaries from session.
//
// Description:
//
//	Iterates through the session's trace steps and builds a history of
//	tool calls with brief summaries of what each tool found. This enables
//	history-aware routing where the router can see what was already tried.
//
// Inputs:
//
//	s - The session to extract history from.
//
// Outputs:
//
//	[]agent.ToolHistoryEntry - Tool history with summaries.
func buildToolHistoryFromSession(s *agent.Session) []agent.ToolHistoryEntry {
	if s == nil {
		return nil
	}

	traceSteps := s.GetTraceSteps()
	if len(traceSteps) == 0 {
		return nil
	}

	var history []agent.ToolHistoryEntry
	stepNum := 0

	for _, step := range traceSteps {
		// Include both tool_call and tool_call_forced actions.
		// CB-31d fix: tool_call_forced was not being counted, so circuit breaker
		// never detected repeated forced tool calls from the router.
		if step.Action != "tool_call" && step.Action != "tool_call_forced" {
			continue
		}

		stepNum++
		entry := agent.ToolHistoryEntry{
			Tool:       step.Tool,
			Success:    step.Error == "",
			StepNumber: stepNum,
		}

		// Build summary based on tool type and results
		entry.Summary = buildToolSummary(step)

		history = append(history, entry)
	}

	// Limit to last N entries to keep context manageable
	if len(history) > maxToolHistoryEntries {
		history = history[len(history)-maxToolHistoryEntries:]
	}

	return history
}

// buildToolSummary creates a brief summary of what a tool call found.
//
// Inputs:
//
//	step - The trace step for the tool call.
//
// Outputs:
//
//	string - Brief summary of the result.
func buildToolSummary(step crs.TraceStep) string {
	if step.Error != "" {
		return "FAILED: " + truncateString(step.Error, 50)
	}

	// Extract summary from metadata if available
	if summary, ok := step.Metadata["summary"]; ok && summary != "" {
		return truncateString(summary, 100)
	}

	// Build summary based on symbols found
	if len(step.SymbolsFound) > 0 {
		return fmt.Sprintf("Found %d symbols", len(step.SymbolsFound))
	}

	// Default to a generic success message with target
	if step.Target != "" {
		return "Processed " + truncateString(step.Target, 50)
	}

	return "Completed successfully"
}

// buildProgressSummary creates a summary of current progress.
//
// Inputs:
//
//	s - The session to summarize.
//
// Outputs:
//
//	string - Progress summary.
func buildProgressSummary(s *agent.Session) string {
	if s == nil {
		return ""
	}

	traceSteps := s.GetTraceSteps()
	if len(traceSteps) == 0 {
		return "No tools called yet"
	}

	// Count tools by category
	toolCounts := make(map[string]int)
	toolOrder := make([]string, 0) // Track insertion order for deterministic output
	totalSymbols := 0

	for _, step := range traceSteps {
		if step.Action == "tool_call" && step.Error == "" {
			if toolCounts[step.Tool] == 0 {
				toolOrder = append(toolOrder, step.Tool)
			}
			toolCounts[step.Tool]++
			totalSymbols += len(step.SymbolsFound)
		}
	}

	// Build summary in deterministic order
	var parts []string
	for _, tool := range toolOrder {
		parts = append(parts, fmt.Sprintf("%s(%d)", tool, toolCounts[tool]))
	}

	summary := fmt.Sprintf("Tools used: %s", strings.Join(parts, ", "))
	if totalSymbols > 0 {
		summary += fmt.Sprintf("; %d symbols found", totalSymbols)
	}

	return summary
}

// -----------------------------------------------------------------------------
// Context Analysis Helpers
// -----------------------------------------------------------------------------

// countSymbolsInContext counts unique symbols referenced in the context.
func countSymbolsInContext(ctx *agent.AssembledContext) int {
	if ctx == nil {
		return 0
	}
	// Count code entries as a proxy for symbols
	return len(ctx.CodeContext)
}

// detectLanguageFromContext attempts to detect the primary language from context.
func detectLanguageFromContext(ctx *agent.AssembledContext) string {
	if ctx == nil || len(ctx.CodeContext) == 0 {
		return ""
	}

	// Simple heuristic: look at file extensions in the code context
	goCount, pyCount := 0, 0
	for _, entry := range ctx.CodeContext {
		if strings.HasSuffix(entry.FilePath, ".go") {
			goCount++
		} else if strings.HasSuffix(entry.FilePath, ".py") {
			pyCount++
		}
	}

	if goCount > pyCount {
		return "go"
	} else if pyCount > goCount {
		return "python"
	}
	return ""
}

// getRecentToolsFromSession extracts recent tool names from session history.
func getRecentToolsFromSession(s *agent.Session) []string {
	if s == nil {
		return nil
	}

	history := s.GetHistory()
	if len(history) == 0 {
		return nil
	}

	// Get last 5 unique tools
	seen := make(map[string]bool)
	var recent []string
	for i := len(history) - 1; i >= 0 && len(recent) < 5; i-- {
		entry := history[i]
		if entry.Type == "tool_call" && entry.ToolName != "" && !seen[entry.ToolName] {
			seen[entry.ToolName] = true
			recent = append(recent, entry.ToolName)
		}
	}
	return recent
}

// -----------------------------------------------------------------------------
// Semantic Tool History (GR-38 Issue 17)
// -----------------------------------------------------------------------------

// buildSemanticToolHistoryFromSession converts session trace steps to routing.ToolCallHistory.
//
// Description:
//
//	Extracts tool calls from session trace steps and builds a semantic history
//	for duplicate detection. Uses the Metadata["query"] field from tool_routing
//	steps as the raw query for semantic comparison.
//
// Inputs:
//
//	s - The session to extract history from.
//
// Outputs:
//
//	*routing.ToolCallHistory - Semantic history for CheckSemanticStatus.
//
// Thread Safety: Safe for concurrent use.
func buildSemanticToolHistoryFromSession(s *agent.Session) *routing.ToolCallHistory {
	history := routing.NewToolCallHistory()

	if s == nil {
		return history
	}

	traceSteps := s.GetTraceSteps()
	if len(traceSteps) == 0 {
		return history
	}

	stepNum := 0
	for _, step := range traceSteps {
		// Look at tool_routing steps which have the query in metadata
		if step.Action != "tool_routing" {
			continue
		}

		stepNum++

		// Extract query from metadata
		rawQuery := ""
		if step.Metadata != nil {
			rawQuery = step.Metadata["query"]
		}

		// Build signature
		sig := routing.ToolCallSignature{
			Tool:       step.Target, // tool_routing uses Target for tool name
			QueryTerms: routing.ExtractQueryTerms(rawQuery),
			RawQuery:   rawQuery,
			StepNumber: stepNum,
			Success:    step.Error == "",
		}

		history.Add(sig)
	}

	// R1.2 Fix: Trim history to maxToolHistoryEntries
	// ToolCallHistory now has Add() auto-trimming, but we also trim explicitly
	// in case session has many tool_routing steps.
	if history.Len() > maxToolHistoryEntries {
		history.Trim(maxToolHistoryEntries)
	}

	return history
}
