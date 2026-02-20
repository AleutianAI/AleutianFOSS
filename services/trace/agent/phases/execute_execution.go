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

// execute_execution.go contains tool execution functions extracted from
// execute.go as part of CB-30c Phase 2 decomposition.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/grounding"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/integration"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/safety"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -----------------------------------------------------------------------------
// Tool Execution
// -----------------------------------------------------------------------------

// executeToolCalls executes a list of tool invocations.
//
// Description:
//
//	Iterates through tool invocations, executing each one with safety checks,
//	circuit breaker checks, and CRS integration. Records trace steps and
//	updates proof numbers based on execution outcomes.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	invocations - Tool invocations to execute.
//
// Outputs:
//
//	[]*tools.Result - Results from tool execution.
//	bool - True if any tool was blocked by safety.
func (p *ExecutePhase) executeToolCalls(ctx context.Context, deps *Dependencies, invocations []agent.ToolInvocation) ([]*tools.Result, bool) {
	// GR-39a: Filter batch with router before execution to reduce redundant calls.
	// Only applies to batches of 3+ tool calls from the main LLM.
	// The filter uses the session's ToolRouter if it implements BatchFilterer.
	batchSize := len(invocations)
	if batchSize >= batchFilterMinSize && deps != nil && deps.Session != nil {
		router := deps.Session.GetToolRouter()
		if router != nil {
			if bf, ok := router.(BatchFilterer); ok {
				slog.Debug("GR-39a: Batch filter check triggered",
					slog.String("session_id", deps.Session.ID),
					slog.Int("batch_size", batchSize),
					slog.Int("min_size", batchFilterMinSize),
					slog.Bool("has_filterer", bf != nil),
				)

				filtered, err := p.filterBatchWithRouter(ctx, deps, invocations)
				if err != nil {
					slog.Warn("GR-39a: Batch filter error, using original batch",
						slog.String("session_id", deps.Session.ID),
						slog.String("error", err.Error()),
					)
					// Continue with original batch on error
				} else if len(filtered) < batchSize {
					slog.Info("GR-39a: Batch filtered before execution",
						slog.String("session_id", deps.Session.ID),
						slog.Int("original", batchSize),
						slog.Int("filtered", len(filtered)),
						slog.Int("skipped", batchSize-len(filtered)),
					)
					invocations = filtered
				} else {
					slog.Debug("GR-39a: Batch filter kept all tools",
						slog.String("session_id", deps.Session.ID),
						slog.Int("batch_size", batchSize),
					)
				}
			} else {
				slog.Debug("GR-39a: Router does not implement BatchFilterer",
					slog.String("session_id", deps.Session.ID),
					slog.String("router_type", fmt.Sprintf("%T", router)),
				)
			}
		} else {
			slog.Debug("GR-39a: No router available for batch filtering",
				slog.String("session_id", deps.Session.ID),
			)
		}
	} else if batchSize > 0 && batchSize < batchFilterMinSize {
		slog.Debug("GR-39a: Batch too small for filtering",
			slog.Int("batch_size", batchSize),
			slog.Int("min_size", batchFilterMinSize),
		)
	}

	results := make([]*tools.Result, 0, len(invocations))
	blocked := false

	// GR-59 Group B: Track consecutive CB fires per tool.
	// Allocated lazily on first CB fire to avoid allocation on non-CB paths.
	var consecutiveCBFires map[string]int
	var lastCBTool string

	// GR-39b: Build tool count map ONCE before the loop for O(n+m) efficiency.
	// This counts ALL tool calls (router + LLM paths) from session trace steps.
	toolCounts := buildToolCountMapFromSession(deps.Session)

	for i, inv := range invocations {
		// GR-39 Issue 3: Emit routing decision for batch-executed tools.
		// This ensures all tool calls have routing trace steps, not just router-selected ones.
		p.emitToolRouting(deps, &agent.ToolRouterSelection{
			Tool:       inv.Tool,
			Confidence: 1.0, // Batch calls have implicit full confidence from LLM
			Reasoning:  "batch_execution",
			Duration:   0,
		})

		// Refresh graph if dirty files exist (before tool queries stale data)
		p.maybeRefreshGraph(ctx, deps)

		// Emit tool invocation event
		p.emitToolInvocation(deps, &inv)

		// Run safety check if required
		if p.requireSafetyCheck {
			// Generate node ID for CDCL constraint extraction
			nodeID := fmt.Sprintf("tool_%s_%d", inv.Tool, i)
			safetyResult := p.isBlockedBySafety(ctx, deps, &inv, nodeID)

			if safetyResult.Blocked {
				blocked = true
				results = append(results, &tools.Result{
					Success: false,
					Error:   safetyResult.ErrorMessage,
				})
				// Record blocked trace step
				p.recordTraceStep(deps, &inv, nil, 0, safetyResult.ErrorMessage)

				// Record safety violation for CDCL learning (Issue #6)
				// Safety violations are hard signals - CDCL should learn to avoid them
				if deps.Session != nil && len(safetyResult.Constraints) > 0 {
					deps.Session.RecordSafetyViolation(
						nodeID,
						safetyResult.ErrorMessage,
						safetyResult.Constraints,
					)
				}

				// CRS-04: Learn from safety violation
				p.learnFromFailure(ctx, deps, crs.FailureEvent{
					SessionID:    deps.Session.ID,
					FailureType:  crs.FailureTypeSafety,
					Tool:         inv.Tool,
					ErrorMessage: safetyResult.ErrorMessage,
					Source:       crs.SignalSourceSafety,
				})

				// CRS-02: Mark tool path as disproven due to safety violation.
				// Safety violations are hard signals - the path cannot lead to a solution.
				p.markToolDisproven(ctx, deps, &inv, "safety_violation: "+safetyResult.ErrorMessage)
				continue
			}
		}

		// GR-39b: Count-based circuit breaker check BEFORE semantic check.
		// This blocks tool calls after N=2 calls regardless of query similarity.
		// The semantic check (CB-30c) catches variations with similarity >= 0.7,
		// but LLMs can produce queries with < 0.7 similarity (e.g., "main" vs "func main").
		// Count-based check provides a hard stop after threshold is reached.
		if deps.Session != nil {
			callCount := toolCounts[inv.Tool]
			if callCount >= crs.DefaultCircuitBreakerThreshold {
				slog.Info("GR-39b: Count-based circuit breaker fired in LLM path",
					slog.String("session_id", deps.Session.ID),
					slog.String("tool", inv.Tool),
					slog.Int("call_count", callCount),
					slog.Int("threshold", crs.DefaultCircuitBreakerThreshold),
				)

				// Record metric
				grounding.RecordCountCircuitBreaker(inv.Tool, "llm")

				// Record trace step for observability
				// CB-31d Item 3: Don't use Error field for expected circuit breaker activations.
				// Error field causes these to be displayed at ERROR level in test output.
				deps.Session.RecordTraceStep(crs.TraceStep{
					Action: "circuit_breaker",
					Tool:   inv.Tool,
					Metadata: map[string]string{
						"path":      "llm",
						"count":     fmt.Sprintf("%d", callCount),
						"threshold": fmt.Sprintf("%d", crs.DefaultCircuitBreakerThreshold),
						"reason":    fmt.Sprintf("GR-39b: count threshold exceeded (%d >= %d)", callCount, crs.DefaultCircuitBreakerThreshold),
						"expected":  "true", // This is expected behavior, not an error
					},
				})

				// Add span event for tracing
				span := trace.SpanFromContext(ctx)
				if span.IsRecording() {
					span.AddEvent("count_circuit_breaker_fired",
						trace.WithAttributes(
							attribute.String("tool", inv.Tool),
							attribute.Int("count", callCount),
							attribute.String("path", "llm"),
						),
					)
				}

				// Learn from repeated calls (CDCL clause generation)
				p.learnFromFailure(ctx, deps, crs.FailureEvent{
					SessionID:    deps.Session.ID,
					FailureType:  crs.FailureTypeCircuitBreaker,
					Tool:         inv.Tool,
					ErrorMessage: "GR-39b: LLM path count threshold exceeded",
					Source:       crs.SignalSourceHard,
				})

				// Emit coordinator event for activity orchestration
				p.emitCoordinatorEvent(ctx, deps, integration.EventCircuitBreaker, &inv, nil,
					fmt.Sprintf("GR-39b: %s count threshold exceeded (%d >= %d)", inv.Tool, callCount, crs.DefaultCircuitBreakerThreshold),
					crs.ErrorCategoryInternal)

				// GR-44 Rev 2: Set circuit breaker active in LLM path.
				// This ensures handleCompletion knows CB has fired and won't
				// send "Your response didn't use tools as required" messages.
				deps.Session.SetCircuitBreakerActive(true)
				slog.Debug("GR-44 Rev 2: CB flag set in LLM path (count-based)",
					slog.String("session_id", deps.Session.ID),
					slog.String("tool", inv.Tool),
				)

				// GR-59 Group B Part 1: Stronger CB message that signals finality.
				results = append(results, &tools.Result{
					Success: false,
					Error: fmt.Sprintf("GR-39b: Tool %s PERMANENTLY BLOCKED (called %d times, threshold: %d). "+
						"You MUST provide your answer NOW using the results you already have. "+
						"Do NOT call any search tools.", inv.Tool, callCount, crs.DefaultCircuitBreakerThreshold),
				})
				blocked = true

				// GR-59 Group B Part 2: Track consecutive CB fires.
				if consecutiveCBFires == nil {
					consecutiveCBFires = make(map[string]int)
				}
				if lastCBTool == inv.Tool {
					consecutiveCBFires[inv.Tool]++
				} else {
					consecutiveCBFires[inv.Tool] = 1
					lastCBTool = inv.Tool
				}

				// If 2+ consecutive CB fires for the same tool, force immediate return.
				// This eliminates 5+ wasted LLM round-trips after CB activation.
				if consecutiveCBFires[inv.Tool] >= 2 {
					slog.Info("GR-59: Consecutive CB fires forcing immediate synthesis",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", inv.Tool),
						slog.Int("consecutive_fires", consecutiveCBFires[inv.Tool]),
					)
					return results, true
				}

				continue
			}
		}

		// GR-39b: Increment count for this tool (for within-batch duplicate detection).
		// Must happen AFTER circuit breaker check passes but BEFORE execution.
		toolCounts[inv.Tool]++

		// CB-30c: Check for semantic repetition BEFORE executing the tool.
		// This catches cases where the main LLM (not router) calls similar tools repeatedly.
		if deps.Session != nil {
			toolQuery := extractToolQuery(&inv)
			if toolQuery != "" {
				isRepetitive, similarity, similarQuery := p.checkSemanticRepetition(ctx, deps, inv.Tool, toolQuery)
				if isRepetitive {
					slog.Info("CB-30c: Blocking semantically repetitive tool call",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", inv.Tool),
						slog.String("query", toolQuery),
						slog.Float64("similarity", similarity),
						slog.String("similar_to", similarQuery),
					)

					// Record metric
					grounding.RecordSemanticRepetition(inv.Tool, similarity, inv.Tool)

					// Learn from repetition
					p.learnFromFailure(ctx, deps, crs.FailureEvent{
						SessionID:   deps.Session.ID,
						FailureType: crs.FailureTypeSemanticRepetition,
						Tool:        inv.Tool,
						Source:      crs.SignalSourceHard,
					})

					// Emit event
					p.emitCoordinatorEvent(ctx, deps, integration.EventSemanticRepetition, &inv, nil,
						fmt.Sprintf("query %.0f%% similar to '%s'", similarity*100, truncateQuery(similarQuery, 30)),
						crs.ErrorCategoryInternal)

					// GR-44 Rev 2: Set circuit breaker active in LLM path.
					// This ensures handleCompletion knows CB has fired and won't
					// send "Your response didn't use tools as required" messages.
					deps.Session.SetCircuitBreakerActive(true)
					slog.Debug("GR-44 Rev 2: CB flag set in LLM path (semantic repetition)",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", inv.Tool),
					)

					// Return a result that indicates semantic repetition
					// This will cause the completion handler to synthesize instead
					results = append(results, &tools.Result{
						Success: false,
						Error:   fmt.Sprintf("Semantic repetition detected: query %.0f%% similar to previous. Synthesize from existing results.", similarity*100),
					})
					blocked = true
					continue
				}
			}
		}

		// GR-59 Group B: Reset consecutive CB counter when a different tool is called
		// (the LLM changed strategy, so give it a chance).
		if lastCBTool != "" && inv.Tool != lastCBTool {
			lastCBTool = ""
		}

		// Execute the tool with timing
		toolStart := time.Now()
		result := p.executeSingleTool(ctx, deps, &inv)
		toolDuration := time.Since(toolStart)
		results = append(results, result)

		// Record trace step for this tool call
		errMsg := ""
		if !result.Success {
			errMsg = result.Error

			// Phase 11B: Convert "not found" errors to successful informational results (Feb 14, 2026)
			// When a tool definitively determines a symbol doesn't exist, that's a VALID RESULT,
			// not an error. Convert to Success=true to prevent LLM retry loops.
			if isNotFoundError(errMsg) {
				slog.Info("Phase 11B: Converting 'not found' error to informational result",
					slog.String("session_id", deps.Session.ID),
					slog.String("tool", inv.Tool),
					slog.String("original_error", errMsg),
				)

				// Build informational output
				infoText := fmt.Sprintf("## Search Result: Not Found\n\n"+
					"The requested symbol was not found in the codebase.\n\n"+
					"Original message: %s\n\n"+
					"The graph has been fully indexed - this is the definitive answer.\n"+
					"**Do NOT use Grep to search further** - the graph already analyzed all source files.\n",
					errMsg)

				// Replace the failed result with a successful informational result
				result.Success = true
				result.Error = ""
				result.Output = infoText
				result.OutputText = infoText
				if result.TokensUsed == 0 {
					result.TokensUsed = len(infoText) / 4 // Estimate tokens (simple heuristic)
				}
				results[len(results)-1] = result // Update the result we just added
				errMsg = ""                      // Clear error since we converted to success
			}

			// P0-3: Detect validation errors and force synthesis (Feb 14, 2026)
			// If tool failed due to parameter validation, mark circuit breaker as active
			// to force synthesis from existing tool results instead of retrying.
			if strings.Contains(errMsg, "parameter validation") ||
				strings.Contains(errMsg, "required parameter missing") ||
				strings.Contains(errMsg, "validation failed") {
				slog.Warn("P0-3: Validation error detected, will force synthesis",
					slog.String("session_id", deps.Session.ID),
					slog.String("tool", inv.Tool),
					slog.String("error", errMsg),
				)

				// Set circuit breaker flag to prevent LLM from retrying with same tool
				if deps.Session != nil {
					deps.Session.SetCircuitBreakerActive(true)
					slog.Debug("P0-3: Circuit breaker activated due to validation failure",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", inv.Tool),
					)
				}
			}

			// Record error for router feedback
			if deps.Session != nil {
				deps.Session.RecordToolError(inv.Tool, errMsg)
			}

			// CRS-04: Learn from tool execution error
			// Determine error category from error message
			errorCategory := categorizeToolError(errMsg)
			p.learnFromFailure(ctx, deps, crs.FailureEvent{
				SessionID:     deps.Session.ID,
				FailureType:   crs.FailureTypeToolError,
				Tool:          inv.Tool,
				ErrorMessage:  errMsg,
				ErrorCategory: errorCategory,
				Source:        crs.SignalSourceHard,
			})

			// CRS-06: Emit EventToolFailed to Coordinator
			p.emitCoordinatorEvent(ctx, deps, integration.EventToolFailed, &inv, result, errMsg, errorCategory)
		} else {
			// CRS-06: Emit EventToolExecuted to Coordinator for successful execution
			p.emitCoordinatorEvent(ctx, deps, integration.EventToolExecuted, &inv, result, "", crs.ErrorCategoryNone)
		}
		p.recordTraceStep(deps, &inv, result, toolDuration, errMsg)

		// GR-38 Issue 16: Estimate and track tokens for tool results
		// This ensures token metrics include tool output, not just LLM tokens.
		// Previously only hard-forced tools counted tokens, causing low token counts
		// for tool-heavy sessions.
		if result != nil && result.Success && result.Output != nil {
			outputStr := fmt.Sprintf("%v", result.Output)
			estimatedTokens := estimateToolResultTokens(outputStr)
			if estimatedTokens > 0 {
				deps.Session.IncrementMetric(agent.MetricTokens, estimatedTokens)
			}
		}

		// CRS-02: Update proof numbers based on tool execution outcome.
		// Proof number represents COST TO PROVE (lower = better).
		// Success decreases cost (path is viable), failure increases cost.
		p.updateProofNumber(ctx, deps, &inv, result)

		// CRS-03: Check for reasoning cycles after each step.
		// Brent's algorithm detects cycles in O(1) amortized time per step.
		stepNumber := 0
		if deps.Session != nil {
			stepNumber = deps.Session.GetMetric(agent.MetricSteps)
		}
		if cycleDetected, cycleReason := p.checkCycleAfterStep(ctx, deps, &inv, stepNumber, result.Success); cycleDetected {
			// Cycle detected - mark this as a blocked result
			slog.Warn("CRS-03: Cycle triggered circuit breaker",
				slog.String("session_id", deps.Session.ID),
				slog.String("tool", inv.Tool),
				slog.String("reason", cycleReason),
			)

			// CRS-06: Emit EventCycleDetected to Coordinator
			p.emitCoordinatorEvent(ctx, deps, integration.EventCycleDetected, &inv, nil, cycleReason, crs.ErrorCategoryInternal)

			// Continue processing - the cycle states are already marked disproven
			// The circuit breaker will fire on the next tool selection
		}

		// Track file modifications for graph refresh
		p.trackModifiedFiles(deps, result)

		// Emit tool result event
		p.emitToolResult(deps, &inv, result)
	}

	// GR-39 Issue 2: Check for "not found" pattern across results.
	// If multiple tools returned "not found" style messages, the agent is likely
	// searching for something that doesn't exist. Force early synthesis.
	notFoundCount := p.countNotFoundResults(results)
	if notFoundCount >= maxNotFoundBeforeSynthesize {
		slog.Info("GR-39: Not-found pattern detected, signaling synthesis",
			slog.String("session_id", deps.Session.ID),
			slog.Int("not_found_count", notFoundCount),
			slog.Int("threshold", maxNotFoundBeforeSynthesize),
		)
		// Add a synthetic result that signals synthesis should happen
		results = append(results, &tools.Result{
			Success: false,
			Error:   fmt.Sprintf("GR-39: %d tools returned 'not found'. The requested symbol may not exist. Please synthesize a helpful explanation.", notFoundCount),
		})
		blocked = true
	}

	return results, blocked
}

// maxNotFoundBeforeSynthesize is the number of "not found" results before forcing synthesis.
const maxNotFoundBeforeSynthesize = 3

// countNotFoundResults counts tool results that indicate "not found" patterns.
//
// Description:
//
//	Detects when tools are returning "not found" style messages, which indicates
//	the agent is searching for something that doesn't exist. This prevents the
//	agent from spiraling through many failed search attempts.
//
// Inputs:
//
//	results - The tool results to check.
//
// Outputs:
//
//	int - Count of results with "not found" patterns.
//
// Thread Safety: Safe for concurrent use (read-only).
func (p *ExecutePhase) countNotFoundResults(results []*tools.Result) int {
	count := 0
	for _, r := range results {
		if r == nil {
			continue
		}
		// Check both successful results with "not found" output and error messages
		if r.Success && r.Output != nil {
			if containsNotFoundPattern(fmt.Sprintf("%v", r.Output)) {
				count++
			}
		} else if r.Error != "" && containsNotFoundPattern(r.Error) {
			count++
		}
	}
	return count
}

// containsNotFoundPattern checks if a string contains "not found" style messages.
func containsNotFoundPattern(s string) bool {
	lower := strings.ToLower(s)
	patterns := []string{
		"not found",
		"no results",
		"no matches",
		"no callees",
		"no callers",
		"no symbols",
		"no files",
		"symbol not found",
		"function not found",
		"does not exist",
		"could not find",
		"unable to find",
		"no such",
		"0 results",
		"zero results",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// recordTraceStep records a reasoning trace step for a tool execution.
//
// Inputs:
//
//	deps - Phase dependencies.
//	inv - The tool invocation.
//	result - The tool result (may be nil for blocked calls).
//	duration - How long the tool call took.
//	errMsg - Error message if the call failed.
func (p *ExecutePhase) recordTraceStep(deps *Dependencies, inv *agent.ToolInvocation, result *tools.Result, duration time.Duration, errMsg string) {
	if deps.Session == nil {
		return
	}

	// Build trace step
	step := crs.TraceStep{
		Action:   "tool_call",
		Target:   inv.Tool,
		Tool:     inv.Tool,
		Duration: duration,
		Error:    errMsg,
		Metadata: make(map[string]string),
	}

	// Add tool parameters to metadata (truncated for safety)
	if inv.Parameters != nil {
		// Extract string params
		if inv.Parameters.StringParams != nil {
			for k, v := range inv.Parameters.StringParams {
				if len(v) > 100 {
					v = v[:100] + "..."
				}
				step.Metadata[k] = v
			}
		}
		// Extract int params
		if inv.Parameters.IntParams != nil {
			for k, v := range inv.Parameters.IntParams {
				step.Metadata[k] = fmt.Sprintf("%d", v)
			}
		}
		// Extract bool params
		if inv.Parameters.BoolParams != nil {
			for k, v := range inv.Parameters.BoolParams {
				step.Metadata[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// Extract symbols found from result if available
	if result != nil && result.Success {
		step.SymbolsFound = extractSymbolsFromResult(result)
	}

	// GR-59 Rev 3: Merge tool's own TraceStep metadata into the session step.
	// Tools like find_implementations, find_callers, find_callees record domain-specific
	// metadata (match_count, total_implementations, total_callers, etc.) in result.TraceStep.
	// Without merging, sessionHasPriorGraphToolResults() cannot detect prior graph tool
	// results, causing forced synthesis to miss and allowing unnecessary Grep/Glob loops.
	if result != nil && result.TraceStep != nil && result.TraceStep.Metadata != nil {
		for k, v := range result.TraceStep.Metadata {
			step.Metadata[k] = v
		}
		// Preserve the tool's action if it's more specific (e.g., "tool_find_implementations")
		if result.TraceStep.Action != "" {
			step.Action = result.TraceStep.Action
		}
	}

	deps.Session.RecordTraceStep(step)
}

// extractSymbolsFromResult extracts symbol IDs from a tool result.
func extractSymbolsFromResult(result *tools.Result) []string {
	if result == nil || result.Output == nil {
		return nil
	}

	// Try to extract symbols from common result structures
	var symbols []string

	// Check if Output is a map
	outputMap, ok := result.Output.(map[string]interface{})
	if !ok {
		return nil
	}

	// Check for Symbols field (used by many exploration tools)
	if syms, ok := outputMap["symbols"]; ok {
		if symList, ok := syms.([]interface{}); ok {
			for _, s := range symList {
				if symMap, ok := s.(map[string]interface{}); ok {
					if id, ok := symMap["id"].(string); ok {
						symbols = append(symbols, id)
					}
				}
			}
		}
	}

	// Check for EntryPoints field
	if eps, ok := outputMap["entry_points"]; ok {
		if epList, ok := eps.([]interface{}); ok {
			for _, ep := range epList {
				if epMap, ok := ep.(map[string]interface{}); ok {
					if id, ok := epMap["id"].(string); ok {
						symbols = append(symbols, id)
					}
				}
			}
		}
	}

	// Limit to first 20 symbols to avoid huge traces
	if len(symbols) > 20 {
		symbols = symbols[:20]
	}

	return symbols
}

// -----------------------------------------------------------------------------
// Safety Checking
// -----------------------------------------------------------------------------

// SafetyCheckResult holds the result of a safety check along with metadata
// for CDCL learning.
type SafetyCheckResult struct {
	// Blocked indicates if the operation should be blocked.
	Blocked bool

	// Result is the full safety check result.
	Result *safety.Result

	// ErrorMessage is the error message for learning.
	ErrorMessage string

	// Constraints are the extracted constraints for CDCL.
	Constraints []safety.SafetyConstraint
}

// isBlockedBySafety checks if a tool invocation should be blocked.
//
// Description:
//
//	Performs safety check and extracts constraints for CDCL learning.
//	Safety violations are classified as hard signals so CDCL can learn
//	to avoid patterns that trigger safety blocks.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	inv - The tool invocation.
//	nodeID - The MCTS node ID for constraint extraction.
//
// Outputs:
//
//	*SafetyCheckResult - The check result with learning metadata.
func (p *ExecutePhase) isBlockedBySafety(ctx context.Context, deps *Dependencies, inv *agent.ToolInvocation, nodeID string) *SafetyCheckResult {
	if deps.SafetyGate == nil {
		return &SafetyCheckResult{Blocked: false}
	}

	// Build proposed change from invocation
	change := p.buildProposedChange(inv)
	if change == nil {
		return &SafetyCheckResult{Blocked: false}
	}

	// Run safety check
	result, err := deps.SafetyGate.Check(ctx, []safety.ProposedChange{*change})
	if err != nil {
		// Safety check error - log but don't block
		p.emitError(deps, fmt.Errorf("safety check failed: %w", err), true)
		return &SafetyCheckResult{Blocked: false}
	}

	// Emit safety check event
	p.emitSafetyCheck(deps, result)

	blocked := deps.SafetyGate.ShouldBlock(result)
	if !blocked {
		return &SafetyCheckResult{Blocked: false, Result: result}
	}

	// Extract constraints for CDCL learning
	constraints := safety.ExtractConstraints(result, nodeID)
	errorMsg := result.ToErrorMessage()

	return &SafetyCheckResult{
		Blocked:      true,
		Result:       result,
		ErrorMessage: errorMsg,
		Constraints:  constraints,
	}
}

// buildProposedChange creates a safety change from a tool invocation.
//
// Inputs:
//
//	inv - The tool invocation.
//
// Outputs:
//
//	*safety.ProposedChange - The change, or nil if not applicable.
func (p *ExecutePhase) buildProposedChange(inv *agent.ToolInvocation) *safety.ProposedChange {
	// Map tool names to change types
	switch inv.Tool {
	case "write_file", "edit_file":
		return &safety.ProposedChange{
			Type:   "file_write",
			Target: getStringParamFromToolParams(inv.Parameters, "path"),
		}
	case "delete_file":
		return &safety.ProposedChange{
			Type:   "file_delete",
			Target: getStringParamFromToolParams(inv.Parameters, "path"),
		}
	case "run_command", "shell":
		return &safety.ProposedChange{
			Type:   "shell_command",
			Target: getStringParamFromToolParams(inv.Parameters, "command"),
		}
	default:
		return nil
	}
}

// executeSingleTool executes a single tool invocation.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	inv - The tool invocation.
//
// Outputs:
//
//	*tools.Result - The execution result.
func (p *ExecutePhase) executeSingleTool(ctx context.Context, deps *Dependencies, inv *agent.ToolInvocation) *tools.Result {
	// If no ToolExecutor, skip tool execution
	if deps.ToolExecutor == nil {
		return &tools.Result{
			Success: false,
			Error:   "tool execution not available (no ToolExecutor configured)",
		}
	}

	// Convert ToolParameters to map for internal tool execution
	toolInvocation := &tools.Invocation{
		ID:         inv.ID,
		ToolName:   inv.Tool,
		Parameters: toolParamsToMap(inv.Parameters),
	}

	result, err := deps.ToolExecutor.Execute(ctx, toolInvocation)
	if err != nil {
		return &tools.Result{
			Success: false,
			Error:   err.Error(),
		}
	}

	return result
}

// -----------------------------------------------------------------------------
// Conversation History Management
// -----------------------------------------------------------------------------

// addAssistantToolCallToHistory adds an assistant message with tool calls to conversation history.
//
// Description:
//
//	When the LLM returns a response with tool calls, we must record that
//	the assistant requested those tools BEFORE adding the tool results.
//	This creates the proper message sequence:
//	  user: "query"
//	  assistant: [tool_call: find_entry_points]
//	  tool: [result]
//	  assistant: "final answer"
//
//	Without this step, tool results become orphaned - the LLM sees tool
//	results but doesn't see that it requested them, causing it to
//	re-request the same tools in an infinite loop.
//
// Inputs:
//
//	deps - Phase dependencies.
//	response - The LLM response containing tool calls.
//	invocations - Parsed tool invocations.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) addAssistantToolCallToHistory(deps *Dependencies, response *llm.Response, invocations []agent.ToolInvocation) {
	if deps.Context == nil || len(invocations) == 0 {
		return
	}

	// Build a description of what tools the assistant called
	var toolCallDesc strings.Builder
	toolCallDesc.WriteString("[Tool calls: ")
	for i, inv := range invocations {
		if i > 0 {
			toolCallDesc.WriteString(", ")
		}
		toolCallDesc.WriteString(inv.Tool)
	}
	toolCallDesc.WriteString("]")

	// Add assistant message showing it requested tools
	// This ensures the conversation history shows the assistant's intent
	assistantMsg := agent.Message{
		Role:    "assistant",
		Content: toolCallDesc.String(),
	}

	// Use ContextManager if available for thread safety
	if deps.ContextManager != nil {
		deps.ContextManager.AddMessage(deps.Context, assistantMsg.Role, assistantMsg.Content)
	} else {
		deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, assistantMsg)
	}

	slog.Debug("Added assistant tool call to history",
		slog.String("session_id", deps.Session.ID),
		slog.Int("tool_count", len(invocations)),
		slog.String("tools", toolCallDesc.String()),
	)
}

// updateContextWithResults updates context with tool results.
//
// Description:
//
//	Adds tool execution results to the context's ToolResults slice.
//	Uses ContextManager when available (preferred path with full context management),
//	but falls back to direct append when ContextManager is nil (degraded mode).
//	This fallback ensures ToolResults is always populated for synthesizeFromToolResults().
//
//	Fixed in cb_30b: Previously returned early when ContextManager was nil,
//	causing ToolResults to be empty and synthesizeFromToolResults() to fail.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	results - Tool execution results.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) updateContextWithResults(ctx context.Context, deps *Dependencies, results []*tools.Result) {
	// Validate required dependencies
	if deps.Context == nil {
		sessionID := "unknown"
		if deps.Session != nil {
			sessionID = deps.Session.ID
		}
		slog.Warn("updateContextWithResults: deps.Context is nil, cannot store results",
			slog.String("session_id", sessionID),
			slog.Int("result_count", len(results)),
		)
		return
	}

	if deps.Session == nil {
		slog.Warn("updateContextWithResults: deps.Session is nil, cannot persist results",
			slog.Int("result_count", len(results)),
		)
		return
	}

	for _, result := range results {
		if result == nil {
			continue
		}

		if deps.ContextManager != nil {
			// Preferred path: Use ContextManager for full context management
			// ContextManager handles: truncation, pruning, token estimation, event emission
			updated, err := deps.ContextManager.Update(ctx, deps.Context, result)
			if err != nil {
				p.emitError(deps, fmt.Errorf("context update failed: %w", err), true)
				// Fall through to direct append as fallback
			} else {
				// Update deps.Context with the new context and persist to session
				deps.Context = updated
				deps.Session.SetCurrentContext(updated)
				continue
			}
		}

		// Fallback: Direct append when ContextManager unavailable or failed
		// This ensures ToolResults is always populated for synthesizeFromToolResults()
		// cb_30b fix: Previously this path was missing, causing empty ToolResults
		outputText := result.OutputText
		truncated := result.Truncated

		// Truncate long outputs to prevent context overflow (match ContextManager behavior)
		const maxOutputLen = 4000 // Match DefaultMaxToolResultLength
		if len(outputText) > maxOutputLen {
			outputText = outputText[:maxOutputLen-3] + "..."
			truncated = true
		}

		// Estimate tokens (simple heuristic: ~4 chars per token)
		tokensUsed := result.TokensUsed
		if tokensUsed == 0 && len(outputText) > 0 {
			tokensUsed = (len(outputText) + 3) / 4
		}

		agentResult := agent.ToolResult{
			InvocationID: uuid.NewString(),
			Success:      result.Success,
			Output:       outputText,
			Error:        result.Error,
			Duration:     result.Duration,
			TokensUsed:   tokensUsed,
			Cached:       result.Cached,
			Truncated:    truncated,
		}

		// Append to ToolResults - safe because session access is serialized
		// through the agent loop (one Execute call at a time per session)
		deps.Context.ToolResults = append(deps.Context.ToolResults, agentResult)
		deps.Session.SetCurrentContext(deps.Context)

		// Record in CRS trace for observability (cb_30b enhancement)
		// This ensures the fallback path is visible in reasoning traces
		if deps.Session != nil {
			deps.Session.RecordTraceStep(crs.TraceStep{
				Action: "tool_result_stored",
				Tool:   "context_fallback",
				Target: agentResult.InvocationID,
				Error:  result.Error,
				Metadata: map[string]string{
					"success":    fmt.Sprintf("%t", result.Success),
					"output_len": fmt.Sprintf("%d", len(outputText)),
					"truncated":  fmt.Sprintf("%t", truncated),
					"path":       "fallback_direct_append",
				},
			})
		}

		slog.Debug("updateContextWithResults: direct append (no ContextManager)",
			slog.String("session_id", deps.Session.ID),
			slog.Bool("success", result.Success),
			slog.Int("output_len", len(outputText)),
			slog.Int("tokens_estimated", tokensUsed),
		)
	}
}

// -----------------------------------------------------------------------------
// Reflection and Graph Management
// -----------------------------------------------------------------------------

// shouldReflect determines if reflection is needed.
//
// Inputs:
//
//	deps - Phase dependencies.
//	stepNumber - Current step number.
//
// Outputs:
//
//	bool - True if reflection should occur.
func (p *ExecutePhase) shouldReflect(deps *Dependencies, stepNumber int) bool {
	return stepNumber > 0 && stepNumber%p.reflectionThreshold == 0
}

// maybeRefreshGraph refreshes the graph if dirty files exist.
//
// Description:
//
//	Checks if any files have been marked dirty by previous tool executions.
//	If so, triggers an incremental refresh to update the graph with fresh
//	parse results. This ensures subsequent tool queries return up-to-date data.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//
// Thread Safety:
//
//	Safe for concurrent use. Refresh is atomic (copy-on-write).
func (p *ExecutePhase) maybeRefreshGraph(ctx context.Context, deps *Dependencies) {
	// Skip if no tracker or refresher
	if deps.DirtyTracker == nil || deps.GraphRefresher == nil {
		return
	}

	// Skip if no dirty files
	if !deps.DirtyTracker.HasDirty() {
		return
	}

	// Get dirty files (does not clear - we clear after successful refresh)
	dirtyFiles := deps.DirtyTracker.GetDirtyFiles()
	if len(dirtyFiles) == 0 {
		return
	}

	slog.Info("refreshing graph for modified files",
		slog.String("session_id", deps.Session.ID),
		slog.Int("file_count", len(dirtyFiles)),
	)

	// Perform refresh
	result, err := deps.GraphRefresher.RefreshFiles(ctx, dirtyFiles)
	if err != nil {
		slog.Warn("incremental graph refresh failed, continuing with stale graph",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)
		// Non-fatal: continue with stale data rather than failing
		return
	}

	// Clear the successfully refreshed files
	deps.DirtyTracker.Clear(dirtyFiles)

	slog.Info("graph refreshed",
		slog.String("session_id", deps.Session.ID),
		slog.Int("nodes_removed", result.NodesRemoved),
		slog.Int("nodes_added", result.NodesAdded),
		slog.Duration("duration", result.Duration),
	)

	// GR-29: Invalidate CRS caches after successful refresh
	p.invalidateGraphCaches(ctx, deps, result)

	// Emit graph refresh event
	p.emitGraphRefresh(deps, result, len(dirtyFiles))
}

// invalidateGraphCaches emits a coordinator event for graph cache invalidation.
//
// Description:
//
//	After the graph is refreshed, notifies the MCTS Coordinator via
//	EventGraphRefreshed. The Coordinator will call InvalidateGraphCache()
//	on the CRS, which invalidates the GraphBackedDependencyIndex caches.
//
//	GR-29: Post-GR-32 simplification. Since CRS reads directly from the graph,
//	no data sync is needed - only cache invalidation triggered by event.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing. Must not be nil.
//	deps - Phase dependencies. Must not be nil.
//	result - The graph refresh result. May be nil (no-op).
//
// Thread Safety:
//
//	Safe for concurrent use.
func (p *ExecutePhase) invalidateGraphCaches(ctx context.Context, deps *Dependencies, result *graph.RefreshResult) {
	if result == nil || deps.Coordinator == nil {
		return
	}

	ctx, span := executePhaseTracer.Start(ctx, "execute.InvalidateGraphCaches",
		trace.WithAttributes(
			attribute.String("session_id", deps.Session.ID),
			attribute.Int("nodes_added", result.NodesAdded),
			attribute.Int("nodes_removed", result.NodesRemoved),
			attribute.Int("files_refreshed", result.FilesRefreshed),
		),
	)
	defer span.End()

	// GR-29: Use LoggerWithTrace for trace_id correlation (CLAUDE.md standard)
	logger := mcts.LoggerWithTrace(ctx, slog.Default())

	// Emit coordinator event (GR-29)
	// The Coordinator will handle cache invalidation via its bridge to CRS
	_, err := deps.Coordinator.HandleEvent(ctx, integration.EventGraphRefreshed, &integration.EventData{
		SessionID: deps.Session.ID,
		Metadata: map[string]any{
			"nodes_added":     result.NodesAdded,
			"nodes_removed":   result.NodesRemoved,
			"files_refreshed": result.FilesRefreshed,
		},
	})

	if err != nil {
		logger.Warn("graph refresh event handling failed",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)
		span.SetAttributes(attribute.Bool("event_handled", false))
	} else {
		logger.Debug("graph refresh event emitted",
			slog.String("session_id", deps.Session.ID),
		)
		span.SetAttributes(attribute.Bool("event_handled", true))
	}

	// CB-31d: Clear symbol resolution cache after graph refresh
	// All cached symbol resolutions are now potentially stale
	cacheSize := 0
	p.symbolCache.Range(func(key, value interface{}) bool {
		cacheSize++
		return true
	})
	p.symbolCache = sync.Map{} // Reset to empty map
	logger.Debug("CB-31d: cleared symbol resolution cache after graph refresh",
		slog.String("session_id", deps.Session.ID),
		slog.Int("entries_cleared", cacheSize),
	)
	span.SetAttributes(attribute.Int("symbol_cache_entries_cleared", cacheSize))
}

// trackModifiedFiles marks files modified by a tool result as dirty.
//
// Description:
//
//	After a tool executes, checks if it modified any files and marks
//	them in the DirtyTracker for later refresh.
//
// Inputs:
//
//	deps - Phase dependencies.
//	result - The tool execution result.
func (p *ExecutePhase) trackModifiedFiles(deps *Dependencies, result *tools.Result) {
	// Skip if no tracker or result
	if deps.DirtyTracker == nil || result == nil {
		return
	}

	// Skip if no modified files
	if len(result.ModifiedFiles) == 0 {
		return
	}

	// Mark each modified file as dirty
	for _, path := range result.ModifiedFiles {
		deps.DirtyTracker.MarkDirty(path)
	}

	slog.Debug("tracked modified files",
		slog.String("session_id", deps.Session.ID),
		slog.Int("count", len(result.ModifiedFiles)),
	)
}

// getToolNames extracts tool names from the registry.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	[]string - List of available tool names.
func (p *ExecutePhase) getToolNames(deps *Dependencies) []string {
	if deps.ToolRegistry == nil {
		return nil
	}
	defs := deps.ToolRegistry.GetDefinitions()
	names := make([]string, len(defs))
	for i, def := range defs {
		names[i] = def.Name
	}
	return names
}

// =============================================================================
// CB-31d: Hard Tool Forcing Implementation
// =============================================================================

// extractToolParameters extracts parameters for a tool from the query and context.
//
// Description:
//
//	Uses rule-based extraction to determine tool parameters without calling
//	the Main LLM. This enables direct tool execution for router selections.
//	TR-12 Fix: Tool-specific parameter extraction logic.
//	CB-31d: Added deps parameter for symbol resolution support.
//
// Inputs:
//
//	goCtx - Context for cancellation propagation to downstream operations.
//	query - The user's query string.
//	toolName - The name of the tool to extract parameters for.
//	toolDefs - Available tool definitions.
//	ctx - Assembled context with current file, symbols, etc.
//	deps - Dependencies with graph and index for symbol resolution (CB-31d).
//
// Outputs:
//
//	map[string]interface{} - Extracted parameters.
//	error - Non-nil if parameter extraction fails.
func (p *ExecutePhase) extractToolParameters(
	goCtx context.Context,
	query string,
	toolName string,
	toolDefs []tools.ToolDefinition,
	ctx *agent.AssembledContext,
	deps *Dependencies,
) (map[string]interface{}, error) {
	// Find tool definition
	var toolDef *tools.ToolDefinition
	for i := range toolDefs {
		if toolDefs[i].Name == toolName {
			toolDef = &toolDefs[i]
			break
		}
	}

	if toolDef == nil {
		return nil, fmt.Errorf("tool not found: %s", toolName)
	}

	// Tool-specific parameter extraction
	switch toolName {
	case "list_packages":
		// No parameters required
		return map[string]interface{}{}, nil

	case "graph_overview":
		// Optional parameters with defaults
		params := map[string]interface{}{
			"depth":                2,
			"include_dependencies": true,
			"include_metrics":      true,
		}
		return params, nil

	case "explore_package":
		// Extract package name from query
		pkgName := extractPackageNameFromQuery(query)
		if pkgName == "" {
			return nil, errors.New("could not extract package name from query")
		}
		return map[string]interface{}{
			"package":              pkgName,
			"include_dependencies": true,
			"include_dependents":   true,
		}, nil

	case "find_entry_points":
		// Use defaults
		return map[string]interface{}{}, nil

	case "find_callers", "find_callees":
		// GR-Phase1: Extract function name from query or context
		funcName := extractFunctionNameFromQuery(query)

		// If not found in query, try to get from context (previous tool results)
		if funcName == "" && ctx != nil {
			funcName = extractFunctionNameFromContext(ctx)
		}

		if funcName == "" {
			return nil, fmt.Errorf("could not extract function name from query for %s", toolName)
		}

		return map[string]interface{}{
			"function_name": funcName,
			"limit":         20,
		}, nil

	case "find_implementations":
		// Extract interface/class name from query using implementation-specific patterns first,
		// then fall back to generic function name extraction.
		interfaceName := extractInterfaceNameFromQuery(query)
		if interfaceName == "" {
			interfaceName = extractFunctionNameFromQuery(query)
		}
		if interfaceName == "" && ctx != nil {
			interfaceName = extractFunctionNameFromContext(ctx)
		}
		if interfaceName == "" {
			return nil, fmt.Errorf("could not extract interface name from query")
		}
		return map[string]interface{}{
			"interface_name": interfaceName,
			"limit":          20,
		}, nil

	case "find_references":
		// Extract symbol name from query
		symbolName := extractFunctionNameFromQuery(query)
		if symbolName == "" && ctx != nil {
			symbolName = extractFunctionNameFromContext(ctx)
		}
		if symbolName == "" {
			return nil, fmt.Errorf("could not extract symbol name from query")
		}
		return map[string]interface{}{
			"symbol_name": symbolName,
			"limit":       20,
		}, nil

	// GR-Phase1: Parameter extraction for graph analytics tools
	case "find_hotspots":
		// Extract "top N" and "kind" from query
		// Defaults: top=10, kind="all"
		top := extractTopNFromQuery(query, 10)
		kind := extractKindFromQuery(query)
		slog.Debug("GR-Phase1: extracted find_hotspots params",
			slog.String("tool", toolName),
			slog.Int("top", top),
			slog.String("kind", kind),
		)
		return map[string]interface{}{
			"top":  top,
			"kind": kind,
		}, nil

	case "find_dead_code":
		// Defaults work for most queries
		// include_exported=false, package="", limit=50
		params := map[string]interface{}{
			"include_exported": false,
			"limit":            50,
		}
		// Check if user specifically asks for exported symbols
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "export") || strings.Contains(lowerQuery, "public") {
			params["include_exported"] = true
		}
		// Try to extract package name if specified
		if pkgName := extractPackageNameFromQuery(query); pkgName != "" {
			params["package"] = pkgName
		}
		slog.Debug("GR-Phase1: extracted find_dead_code params",
			slog.String("tool", toolName),
			slog.Bool("include_exported", params["include_exported"].(bool)),
			slog.Int("limit", params["limit"].(int)),
		)
		return params, nil

	case "find_cycles":
		// Defaults: min_size=2, limit=20
		slog.Debug("GR-Phase1: extracted find_cycles params (defaults)",
			slog.String("tool", toolName),
			slog.Int("min_size", 2),
			slog.Int("limit", 20),
		)
		return map[string]interface{}{
			"min_size": 2,
			"limit":    20,
		}, nil

	case "find_path":
		// Extract "from" and "to" symbols - both required
		from, to, ok := extractPathSymbolsFromQuery(query)
		if !ok {
			// Try to extract any two function names from the query
			funcName := extractFunctionNameFromQuery(query)
			if funcName != "" && (from == "" || to == "") {
				if from == "" {
					from = funcName
				} else if to == "" {
					to = funcName
				}
			}
		}
		if from == "" || to == "" {
			slog.Debug("GR-Phase1: find_path extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
				slog.String("from", from),
				slog.String("to", to),
			)
			return nil, fmt.Errorf("could not extract 'from' and 'to' symbols from query for find_path (need both source and target)")
		}
		slog.Debug("GR-Phase1: extracted find_path params",
			slog.String("tool", toolName),
			slog.String("from", from),
			slog.String("to", to),
		)
		return map[string]interface{}{
			"from": from,
			"to":   to,
		}, nil

	case "find_important":
		// Extract "top N" and "kind" from query (same as find_hotspots)
		// Defaults: top=10, kind="all"
		top := extractTopNFromQuery(query, 10)
		kind := extractKindFromQuery(query)
		slog.Debug("GR-Phase1: extracted find_important params",
			slog.String("tool", toolName),
			slog.Int("top", top),
			slog.String("kind", kind),
		)
		return map[string]interface{}{
			"top":  top,
			"kind": kind,
		}, nil

	case "find_symbol":
		// Extract symbol name from query
		symbolName := extractFunctionNameFromQuery(query)
		if symbolName == "" && ctx != nil {
			symbolName = extractFunctionNameFromContext(ctx)
		}
		if symbolName == "" {
			slog.Debug("GR-Phase1: find_symbol extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract symbol name from query for find_symbol")
		}
		params := map[string]interface{}{
			"name": symbolName,
			"kind": "all",
		}
		// Check if user specified a kind filter
		kind := extractKindFromQuery(query)
		if kind != "all" {
			params["kind"] = kind
		}
		slog.Debug("GR-Phase1: extracted find_symbol params",
			slog.String("tool", toolName),
			slog.String("name", symbolName),
			slog.String("kind", kind),
		)
		return params, nil

	case "find_communities":
		// GR-47 Fix: Extract resolution and top from query
		// Resolution: "high" → 2.0, "fine-grained" → 2.0, "low" → 0.5, default 1.0
		// Top: extract "top N" pattern, default 20
		resolution := 1.0 // default medium
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "high") || strings.Contains(lowerQuery, "fine-grained") ||
			strings.Contains(lowerQuery, "fine grained") || strings.Contains(lowerQuery, "detailed") {
			resolution = 2.0
		} else if strings.Contains(lowerQuery, "low") || strings.Contains(lowerQuery, "coarse") ||
			strings.Contains(lowerQuery, "broad") {
			resolution = 0.5
		}
		top := extractTopNFromQuery(query, 20)
		slog.Debug("GR-47: extracted find_communities params",
			slog.String("tool", toolName),
			slog.Float64("resolution", resolution),
			slog.Int("top", top),
		)
		return map[string]interface{}{
			"resolution": resolution,
			"top":        top,
		}, nil

	case "find_articulation_points":
		// GR-47 Fix: Extract top and include_bridges from query
		// Defaults: top=20, include_bridges=true
		top := extractTopNFromQuery(query, 20)
		includeBridges := true
		lowerQuery := strings.ToLower(query)
		// Only exclude bridges if explicitly asked for just points
		if strings.Contains(lowerQuery, "no bridges") || strings.Contains(lowerQuery, "without bridges") ||
			strings.Contains(lowerQuery, "only points") || strings.Contains(lowerQuery, "just points") {
			includeBridges = false
		}
		slog.Debug("GR-47: extracted find_articulation_points params",
			slog.String("tool", toolName),
			slog.Int("top", top),
			slog.Bool("include_bridges", includeBridges),
		)
		return map[string]interface{}{
			"top":             top,
			"include_bridges": includeBridges,
		}, nil

	case "find_dominators":
		// CB-31d: Extract target function and optional entry point
		// Patterns: "dominators of X", "what dominates X", "must call before X"
		// IT-00a-1 Phase 3: Multi-candidate extraction + candidate-loop resolution
		candidates := extractFunctionNameCandidates(query)
		target := ""
		if len(candidates) == 0 && ctx != nil {
			target = extractFunctionNameFromContext(ctx)
		} else if len(candidates) > 0 {
			target = candidates[0]
		}
		if target == "" && len(candidates) == 0 {
			slog.Debug("CB-31d: find_dominators extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract target function from query for find_dominators")
		}

		// CB-31d: Resolve target symbol if possible
		// IT-00a-1: Use candidate-loop resolution when multiple candidates available
		if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			if len(candidates) > 1 {
				resolvedTarget, rawName, confidence, err := resolveFirstCandidate(goCtx, &p.symbolCache, sessionID, candidates, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_dominators",
						slog.String("raw", rawName),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
						slog.Int("candidates", len(candidates)),
					)
					target = resolvedTarget
				}
			} else {
				resolvedTarget, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, target, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_dominators",
						slog.String("raw", target),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
					)
					target = resolvedTarget
				}
			}
		}

		params := map[string]interface{}{
			"target": target,
		}
		// Try to extract entry point if specified (e.g., "dominators from main to X")
		from, _, ok := extractPathSymbolsFromQuery(query)
		if ok && from != "" && from != target {
			// CB-31d: Resolve entry_point symbol if possible
			if deps != nil && deps.SymbolIndex != nil {
				sessionID := ""
				if deps.Session != nil {
					sessionID = deps.Session.ID
				}
				resolvedFrom, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, from, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved entry_point symbol for find_dominators",
						slog.String("raw", from),
						slog.String("resolved", resolvedFrom),
						slog.Float64("confidence", confidence),
					)
					from = resolvedFrom
				}
			}
			params["entry_point"] = from
		}
		slog.Debug("CB-31d: extracted find_dominators params",
			slog.String("tool", toolName),
			slog.String("target", target),
		)
		return params, nil

	case "find_common_dependency":
		// CB-31d: Extract two or more function names
		// Patterns: "common dependency between X and Y", "shared by X and Y", "LCD of X and Y"
		from, to, ok := extractPathSymbolsFromQuery(query)
		if !ok || from == "" || to == "" {
			slog.Debug("CB-31d: find_common_dependency extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
				slog.String("from", from),
				slog.String("to", to),
			)
			return nil, fmt.Errorf("could not extract two function names from query for find_common_dependency")
		}

		// Build targets array
		targets := []string{from, to}

		// CB-31d: Resolve symbols if possible
		if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			// Resolve both targets
			resolvedTargets := make([]string, 0, len(targets))
			for i, target := range targets {
				resolvedTarget, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, target, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_common_dependency",
						slog.Int("index", i),
						slog.String("raw", target),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
					)
					resolvedTargets = append(resolvedTargets, resolvedTarget)
				} else {
					// Keep original if resolution fails
					resolvedTargets = append(resolvedTargets, target)
				}
			}
			targets = resolvedTargets
		}

		// Extract optional entry point from query
		// Patterns: "from main", "starting at X", "entry point Y"
		var entry string
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "from") {
			parts := strings.Split(query, "from")
			if len(parts) > 1 {
				// Extract first word after "from"
				words := strings.Fields(parts[len(parts)-1])
				if len(words) > 0 {
					entry = words[0]
					// Clean up punctuation
					entry = strings.Trim(entry, ".,;:!?")
				}
			}
		}

		params := map[string]interface{}{
			"targets": targets, // Array of strings
		}
		if entry != "" {
			params["entry"] = entry
		}

		slog.Debug("CB-31d: extracted find_common_dependency params",
			slog.String("tool", toolName),
			slog.Any("targets", targets),
			slog.String("entry", entry),
		)
		return params, nil

	case "find_critical_path":
		// CB-31d: Extract target and optional entry point
		// Patterns: "critical path to X", "mandatory path to X", "from Y to X"
		// IT-00a-1 Phase 3: Multi-candidate extraction + candidate-loop resolution
		candidates := extractFunctionNameCandidates(query)
		target := ""
		if len(candidates) == 0 && ctx != nil {
			target = extractFunctionNameFromContext(ctx)
		} else if len(candidates) > 0 {
			target = candidates[0]
		}
		// If still no target, try extracting from path symbols (might be "to" symbol)
		if target == "" && len(candidates) == 0 {
			_, to, ok := extractPathSymbolsFromQuery(query)
			if ok && to != "" {
				target = to
			}
		}
		if target == "" {
			slog.Debug("CB-31d: find_critical_path extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract target function from query for find_critical_path")
		}

		// CB-31d: Resolve target symbol if possible
		// IT-00a-1: Use candidate-loop resolution when multiple candidates available
		if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			if len(candidates) > 1 {
				resolvedTarget, rawName, confidence, err := resolveFirstCandidate(goCtx, &p.symbolCache, sessionID, candidates, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_critical_path",
						slog.String("raw", rawName),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
						slog.Int("candidates", len(candidates)),
					)
					target = resolvedTarget
				}
			} else {
				resolvedTarget, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, target, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_critical_path",
						slog.String("raw", target),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
					)
					target = resolvedTarget
				}
			}
		}

		params := map[string]interface{}{
			"target": target,
		}
		// Try to extract entry point
		from, _, ok := extractPathSymbolsFromQuery(query)
		if ok && from != "" && from != target {
			// CB-31d: Resolve entry_point symbol if possible
			if deps != nil && deps.SymbolIndex != nil {
				sessionID := ""
				if deps.Session != nil {
					sessionID = deps.Session.ID
				}
				resolvedFrom, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, from, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved entry_point symbol for find_critical_path",
						slog.String("raw", from),
						slog.String("resolved", resolvedFrom),
						slog.Float64("confidence", confidence),
					)
					from = resolvedFrom
				}
			}
			params["entry_point"] = from
		}
		slog.Debug("CB-31d: extracted find_critical_path params",
			slog.String("tool", toolName),
			slog.String("target", target),
		)
		return params, nil

	case "find_merge_points":
		// CB-31d: Extract source functions (usually 2+)
		// Patterns: "merge points for X and Y", "where do X and Y converge"
		from, to, ok := extractPathSymbolsFromQuery(query)
		if !ok || from == "" {
			// Try single function extraction as fallback
			funcName := extractFunctionNameFromQuery(query)
			if funcName != "" {
				from = funcName
			}
		}
		sources := []string{}
		if from != "" {
			sources = append(sources, from)
		}
		if to != "" && to != from {
			sources = append(sources, to)
		}
		if len(sources) == 0 {
			slog.Debug("CB-31d: find_merge_points extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			// Return defaults - find merge points for all entry points
			return map[string]interface{}{}, nil
		}

		// CB-31d: Resolve all source symbols if possible
		if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			resolvedSources := make([]string, 0, len(sources))
			for _, source := range sources {
				resolvedSource, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, source, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved source symbol for find_merge_points",
						slog.String("raw", source),
						slog.String("resolved", resolvedSource),
						slog.Float64("confidence", confidence),
					)
					resolvedSources = append(resolvedSources, resolvedSource)
				} else {
					// Fall back to raw name on resolution failure
					resolvedSources = append(resolvedSources, source)
				}
			}
			sources = resolvedSources
		}

		slog.Debug("CB-31d: extracted find_merge_points params",
			slog.String("tool", toolName),
			slog.Int("source_count", len(sources)),
		)
		return map[string]interface{}{
			"sources": sources,
		}, nil

	case "find_control_dependencies":
		// CB-31d: Extract target function
		// Patterns: "control dependencies of X", "what controls X"
		// IT-00a-1 Phase 3: Multi-candidate extraction + candidate-loop resolution
		candidates := extractFunctionNameCandidates(query)
		target := ""
		if len(candidates) > 0 {
			target = candidates[0]
		}
		slog.Info("P0 DEBUG: find_control_dependencies parameter extraction",
			slog.String("tool", toolName),
			slog.String("query_preview", truncateForLog(query, 100)),
			slog.String("extracted_from_query", target),
			slog.Int("candidates", len(candidates)),
		)
		if target == "" && ctx != nil {
			target = extractFunctionNameFromContext(ctx)
			slog.Info("P0 DEBUG: fallback to context extraction",
				slog.String("extracted_from_context", target),
			)
		}
		if target == "" {
			slog.Debug("CB-31d: find_control_dependencies extraction failed",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract target function from query for find_control_dependencies")
		}

		// CB-31d: Resolve target symbol if possible
		// IT-00a-1: Use candidate-loop resolution when multiple candidates available
		if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			if len(candidates) > 1 {
				resolvedTarget, rawName, confidence, err := resolveFirstCandidate(goCtx, &p.symbolCache, sessionID, candidates, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_control_dependencies",
						slog.String("raw", rawName),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
						slog.Int("candidates", len(candidates)),
					)
					target = resolvedTarget
				}
			} else {
				resolvedTarget, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, target, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved target symbol for find_control_dependencies",
						slog.String("raw", target),
						slog.String("resolved", resolvedTarget),
						slog.Float64("confidence", confidence),
					)
					target = resolvedTarget
				}
			}
		}

		slog.Debug("CB-31d: extracted find_control_dependencies params",
			slog.String("tool", toolName),
			slog.String("target", target),
		)
		return map[string]interface{}{
			"target": target,
		}, nil

	case "find_extractable_regions":
		// CB-31d: Use defaults - no parameters required
		// Analyzes entire codebase for SESE regions
		slog.Debug("CB-31d: extracted find_extractable_regions params (defaults)",
			slog.String("tool", toolName),
		)
		return map[string]interface{}{}, nil

	case "find_loops":
		// CB-31d: Optional entry point, otherwise finds all natural loops
		// Patterns: "loops in X", "recursive calls in X"
		// IT-00a-1 Phase 3: Multi-candidate extraction + candidate-loop resolution
		params := map[string]interface{}{}
		candidates := extractFunctionNameCandidates(query)
		funcName := ""
		if len(candidates) > 0 {
			funcName = candidates[0]
		}
		if funcName != "" {
			// CB-31d: Resolve entry_point symbol if possible
			// IT-00a-1: Use candidate-loop resolution when multiple candidates available
			if deps != nil && deps.SymbolIndex != nil {
				sessionID := ""
				if deps.Session != nil {
					sessionID = deps.Session.ID
				}
				if len(candidates) > 1 {
					resolvedFunc, rawName, confidence, err := resolveFirstCandidate(goCtx, &p.symbolCache, sessionID, candidates, deps)
					if err == nil {
						slog.Debug("CB-31d: resolved entry_point symbol for find_loops",
							slog.String("raw", rawName),
							slog.String("resolved", resolvedFunc),
							slog.Float64("confidence", confidence),
							slog.Int("candidates", len(candidates)),
						)
						funcName = resolvedFunc
					}
				} else {
					resolvedFunc, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, funcName, deps)
					if err == nil {
						slog.Debug("CB-31d: resolved entry_point symbol for find_loops",
							slog.String("raw", funcName),
							slog.String("resolved", resolvedFunc),
							slog.Float64("confidence", confidence),
						)
						funcName = resolvedFunc
					}
				}
			}
			params["entry_point"] = funcName
		}
		slog.Debug("CB-31d: extracted find_loops params",
			slog.String("tool", toolName),
		)
		return params, nil

	case "find_weighted_criticality":
		// CB-31d: Extract top N parameter
		// Patterns: "top 10 critical functions", "most critical functions"
		top := extractTopNFromQuery(query, 20)
		slog.Debug("CB-31d: extracted find_weighted_criticality params",
			slog.String("tool", toolName),
			slog.Int("top", top),
		)
		return map[string]interface{}{
			"top": top,
		}, nil

	case "find_module_api":
		// CB-31d: Extract optional top N and min_community_size parameters
		// Patterns: "top 5 module APIs", "module APIs with at least 10 functions"
		// Note: Tool analyzes communities detected by community detection, not specific modules
		params := map[string]interface{}{}

		// Try to extract top N
		top := extractTopNFromQuery(query, 10)
		if top != 10 {
			params["top"] = top
		}

		// Could extract min_community_size if patterns like "with at least X functions" exist
		// For now, use defaults

		slog.Debug("CB-31d: extracted find_module_api params",
			slog.String("tool", toolName),
			slog.Int("top", top),
		)
		return params, nil

	case "Grep":
		// P0-2: Extract pattern parameter for LLM-generated Grep calls (Feb 14, 2026)
		// When LLM calls Grep without parameters, extract search pattern from query context
		pattern := extractSearchPatternFromQuery(query)
		if pattern == "" {
			// Fallback: Try to extract any capitalized word as search target
			words := strings.Fields(query)
			for _, word := range words {
				cleaned := strings.Trim(word, ".,;:!?\"'")
				// Look for capitalized words (likely symbol names)
				if len(cleaned) > 0 && unicode.IsUpper(rune(cleaned[0])) {
					pattern = cleaned
					break
				}
			}
		}

		if pattern == "" {
			slog.Debug("P0-2: Grep parameter extraction failed - no pattern found",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract search pattern from query for Grep")
		}

		params := map[string]interface{}{
			"pattern": pattern,
		}

		// Try to extract output_mode if specified
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "file") && (strings.Contains(lowerQuery, "list") || strings.Contains(lowerQuery, "which")) {
			params["output_mode"] = "files_with_matches"
		} else if strings.Contains(lowerQuery, "count") || strings.Contains(lowerQuery, "how many") {
			params["output_mode"] = "count"
		} else {
			params["output_mode"] = "content" // Default to showing content
		}

		slog.Debug("P0-2: extracted Grep params",
			slog.String("tool", toolName),
			slog.String("pattern", pattern),
			slog.String("output_mode", params["output_mode"].(string)),
		)
		return params, nil

	case "check_reducibility":
		// CB-31d Item 1: Extract show_irreducible parameter (optional, default true)
		// Patterns: "check reducibility", "is this code reducible", "find irreducible regions"
		// Default behavior: Show irreducible regions for debugging
		params := map[string]interface{}{
			"show_irreducible": true, // Default: show irreducible regions
		}

		// Check if user explicitly asks to hide irreducible regions
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "hide") || strings.Contains(lowerQuery, "without") ||
			strings.Contains(lowerQuery, "no details") || strings.Contains(lowerQuery, "summary only") {
			params["show_irreducible"] = false
		}

		slog.Debug("CB-31d: extracted check_reducibility params",
			slog.String("tool", toolName),
			slog.Bool("show_irreducible", params["show_irreducible"].(bool)),
		)
		return params, nil

	case "get_call_chain":
		// CB-31d Item 2: Extract function_name, direction, and max_depth parameters
		// Patterns: "call chain for X", "downstream from X", "upstream to X", "depth 3"
		// Required: function_name
		// Optional: direction (default "downstream"), max_depth (default 5, range 1-10)

		// Extract function name (required)
		// IT-00a-1 Phase 3: Multi-candidate extraction + candidate-loop resolution
		candidates := extractFunctionNameCandidates(query)
		funcName := ""
		if len(candidates) > 0 {
			funcName = candidates[0]
		}
		if funcName == "" && ctx != nil {
			funcName = extractFunctionNameFromContext(ctx)
		}
		if funcName == "" {
			slog.Debug("CB-31d: get_call_chain extraction failed - no function name",
				slog.String("tool", toolName),
				slog.String("query_preview", truncateForLog(query, 100)),
			)
			return nil, fmt.Errorf("could not extract function name from query for get_call_chain")
		}

		// CB-31d: Resolve function name if possible
		// IT-00a-1: Use candidate-loop resolution when multiple candidates available
		// IT-05 Run 2 Fix: Dot-notation names (Type.Method like "Engine.runRenderLoop")
		// fail agent-side resolution because the index stores bare method names, not
		// "Type.Method". The tool's ResolveFunctionWithFuzzy with WithBareMethodFallback()
		// handles this correctly. Skip pre-resolution for dot-notation and let the tool
		// resolve it.
		isDotNotation := strings.Contains(funcName, ".") && !strings.Contains(funcName, "/")
		if isDotNotation {
			// CR-R2-4: Log the passthrough decision for observability.
			slog.Debug("IT-05: skipping agent-side resolution for dot-notation name",
				slog.String("tool", toolName),
				slog.String("func_name", funcName),
			)
		} else if deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			if len(candidates) > 1 {
				resolvedFunc, rawName, confidence, err := resolveFirstCandidate(goCtx, &p.symbolCache, sessionID, candidates, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved function symbol for get_call_chain",
						slog.String("raw", rawName),
						slog.String("resolved", resolvedFunc),
						slog.Float64("confidence", confidence),
						slog.Int("candidates", len(candidates)),
					)
					funcName = resolvedFunc
				}
			} else {
				resolvedFunc, confidence, err := resolveSymbolCached(&p.symbolCache, sessionID, funcName, deps)
				if err == nil {
					slog.Debug("CB-31d: resolved function symbol for get_call_chain",
						slog.String("raw", funcName),
						slog.String("resolved", resolvedFunc),
						slog.Float64("confidence", confidence),
					)
					funcName = resolvedFunc
				}
			}
		}

		// Extract direction (default "downstream")
		direction := "downstream"
		lowerQuery := strings.ToLower(query)
		if strings.Contains(lowerQuery, "upstream") || strings.Contains(lowerQuery, "caller") ||
			strings.Contains(lowerQuery, "who calls") || strings.Contains(lowerQuery, "reverse") {
			direction = "upstream"
		}

		// Extract max_depth (default 5, range 1-10)
		maxDepth := extractTopNFromQuery(query, 5) // Reuse extractTopNFromQuery for "depth N" patterns
		// Also check for explicit "depth" keyword
		if strings.Contains(lowerQuery, "depth") {
			parts := strings.Fields(query)
			for i, part := range parts {
				if strings.Contains(strings.ToLower(part), "depth") && i+1 < len(parts) {
					// Try to parse next word as number
					if n := parseInt(parts[i+1]); n > 0 && n <= 10 {
						maxDepth = n
						break
					}
				}
			}
		}
		// Clamp to valid range
		if maxDepth < 1 {
			maxDepth = 1
		} else if maxDepth > 10 {
			maxDepth = 10
		}

		params := map[string]interface{}{
			"function_name": funcName,
			"direction":     direction,
			"max_depth":     maxDepth,
		}

		// IT-05 R5: Dual-endpoint resolution for "from X to Y" queries.
		// When query has a destination, resolve it too and pass as optional param.
		destCandidates := extractDestinationCandidates(query)
		if len(destCandidates) > 0 && deps != nil && deps.SymbolIndex != nil {
			sessionID := ""
			if deps.Session != nil {
				sessionID = deps.Session.ID
			}
			destID, destName, destConf, destErr := resolveFirstCandidate(
				context.Background(), &p.symbolCache, sessionID, destCandidates, deps)
			if destErr == nil && destConf > 0 {
				params["destination_name"] = destID
				slog.Debug("IT-05 R5: resolved destination for get_call_chain",
					slog.String("raw", destName),
					slog.String("resolved", destID),
					slog.Float64("confidence", destConf),
				)
			}
		}

		slog.Debug("CB-31d: extracted get_call_chain params",
			slog.String("tool", toolName),
			slog.String("function_name", funcName),
			slog.String("direction", direction),
			slog.Int("max_depth", maxDepth),
		)
		return params, nil

	default:
		// For other tools, fallback to Main LLM
		return nil, fmt.Errorf("parameter extraction not implemented for tool: %s", toolName)
	}
}

// executeToolDirectlyWithFallback executes a tool directly without calling Main LLM.
//
// Description:
//
//	TR-2 Fix: Executes tool directly with full CRS recording for observability.
//	This is the core of the hard forcing mechanism that prevents Split-Brain.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	toolName - Name of the tool to execute.
//	params - Extracted parameters for the tool.
//	toolDefs - Available tool definitions.
//
// Outputs:
//
//	*PhaseResult - The execution result if successful.
//	error - Non-nil if execution fails.
func (p *ExecutePhase) executeToolDirectlyWithFallback(
	ctx context.Context,
	deps *Dependencies,
	toolName string,
	params map[string]interface{},
	toolDefs []tools.ToolDefinition,
) (*PhaseResult, error) {
	start := time.Now()

	// Get tool from registry
	tool, found := deps.ToolRegistry.Get(toolName)
	if !found {
		return nil, fmt.Errorf("tool implementation not found: %s", toolName)
	}

	// Execute the tool
	result, err := tool.Execute(ctx, params)
	duration := time.Since(start)

	// CRITICAL: Record CRS step for observability (TR-2 Fix)
	stepBuilder := crs.NewTraceStepBuilder().
		WithAction("tool_call_forced").
		WithTool(toolName).
		WithDuration(duration).
		WithMetadata("forced_by", "router").
		WithMetadata("params", fmt.Sprintf("%v", params))

	if err != nil {
		stepBuilder = stepBuilder.WithError(err.Error())
		deps.Session.RecordTraceStep(stepBuilder.Build())
		return nil, fmt.Errorf("tool execution failed: %w", err)
	}

	// Add result preview to trace
	if result != nil && result.Output != nil {
		// result.Output is interface{}, convert to string for preview
		outputStr := fmt.Sprintf("%v", result.Output)
		if len(outputStr) > 200 {
			outputStr = outputStr[:200] + "..."
		}
		stepBuilder = stepBuilder.WithMetadata("result_preview", outputStr)
	}

	// GR-59 Rev 3: Merge tool's own TraceStep metadata into the forced call step.
	// This ensures sessionHasPriorGraphToolResults() can detect that graph tools
	// returned substantive results (match_count, total_implementations, etc.)
	// even when executed via the forced tool call path.
	if result != nil && result.TraceStep != nil && result.TraceStep.Metadata != nil {
		for k, v := range result.TraceStep.Metadata {
			stepBuilder = stepBuilder.WithMetadata(k, v)
		}
	}

	deps.Session.RecordTraceStep(stepBuilder.Build())

	// GR-59 Rev 4: Set session flag when forced graph tool completes successfully.
	// Graph results are authoritative whether positive ("Found 3 implementations")
	// or zero ("Symbol 'X' not found"). Both are definitive answers — the graph
	// analyzed every source file. The LLM must synthesize from these results,
	// not spiral into Grep/Glob loops trying to verify or search independently.
	if graphToolsWithSubstantiveResults[toolName] &&
		result != nil && result.Success {
		deps.Session.SetGraphToolHadSubstantiveResults(true)
		slog.Info("GR-59 Rev 4: Forced graph tool completed — will force synthesis",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", toolName),
			slog.Int("output_len", len(result.OutputText)),
			slog.Bool("has_positive_results", hasSubstantiveGraphResult(result.OutputText)),
		)
	}

	// CB-30c: Estimate and track tokens for tool output
	// This ensures token metrics are non-zero even when using hard-forced tools
	// without LLM calls (which was causing tokens_used=0 in trace_logs_36).
	if result != nil && result.Output != nil {
		outputStr := fmt.Sprintf("%v", result.Output)
		estimatedTokens := estimateToolResultTokens(outputStr)
		if estimatedTokens > 0 {
			deps.Session.IncrementMetric(agent.MetricTokens, estimatedTokens)
			slog.Debug("CB-30c: Estimated tokens for hard-forced tool",
				slog.String("tool", toolName),
				slog.Int("estimated_tokens", estimatedTokens),
				slog.Int("output_len", len(outputStr)),
			)
		}
	}

	// Update context with tool result using ContextManager
	// CRS-07 FIX: Previously discarded the updated context, causing tool results
	// to be lost. Now properly stores the updated context so BuildRequest() can
	// include tool results in LLM messages for synthesis.
	if deps.ContextManager != nil && deps.Context != nil && result != nil {
		updated, err := deps.ContextManager.Update(ctx, deps.Context, result)
		if err != nil {
			slog.Warn("Failed to update context with hard-forced tool result",
				slog.String("tool", toolName),
				slog.String("error", err.Error()),
			)
		} else {
			deps.Context = updated
			deps.Session.SetCurrentContext(updated)
			slog.Debug("CRS-07: Context updated with hard-forced tool result",
				slog.String("tool", toolName),
				slog.Int("tool_results_count", len(updated.ToolResults)),
			)
		}
	}

	// Return success result - this will exit Execute() early
	return &PhaseResult{
		NextState: agent.StateExecute, // Stay in execute to allow router to decide next step
		Response:  fmt.Sprintf("Tool %s executed successfully (hard forced)", toolName),
	}, nil
}

// -----------------------------------------------------------------------------
// CRS-02: Proof Index Integration
// -----------------------------------------------------------------------------

// updateProofNumber updates the proof number for a tool path based on execution outcome.
//
// Description:
//
//	Called after tool execution to update the CRS proof index.
//	- Success: Decrements proof number (path is easier to prove)
//	- Failure: Increments proof number (path is harder to prove)
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies containing session.
//	inv - The tool invocation.
//	result - The tool execution result.
func (p *ExecutePhase) updateProofNumber(ctx context.Context, deps *Dependencies, inv *agent.ToolInvocation, result *tools.Result) {
	if deps.Session == nil {
		return
	}

	crsInstance := deps.Session.GetCRS()
	if crsInstance == nil {
		return
	}

	// Build node ID for this tool path
	nodeID := fmt.Sprintf("session:%s:tool:%s", deps.Session.ID, inv.Tool)

	var updateType crs.ProofUpdateType
	var reason string

	if result.Success {
		// Success: decrease proof number (path is viable)
		updateType = crs.ProofUpdateTypeDecrement
		reason = "tool_success"
	} else {
		// Failure: increase proof number (path is problematic)
		updateType = crs.ProofUpdateTypeIncrement
		reason = "tool_failure: " + result.Error
	}

	err := crsInstance.UpdateProofNumber(ctx, crs.ProofUpdate{
		NodeID: nodeID,
		Type:   updateType,
		Delta:  1,
		Reason: reason,
		Source: crs.SignalSourceHard, // Tool execution is a hard signal
	})
	if err != nil {
		slog.Warn("CRS-02: failed to update proof number",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", inv.Tool),
			slog.String("error", err.Error()),
		)
	}
}

// markToolDisproven marks a tool path as disproven in the proof index.
//
// Description:
//
//	Called when a tool is blocked by safety or after repeated failures.
//	Marks the path as disproven (infinite cost to prove) and propagates
//	the disproof to parent decisions.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies containing session.
//	inv - The tool invocation.
//	reason - Why the tool was disproven.
func (p *ExecutePhase) markToolDisproven(ctx context.Context, deps *Dependencies, inv *agent.ToolInvocation, reason string) {
	if deps.Session == nil {
		return
	}

	crsInstance := deps.Session.GetCRS()
	if crsInstance == nil {
		return
	}

	// Build node ID for this tool path
	nodeID := fmt.Sprintf("session:%s:tool:%s", deps.Session.ID, inv.Tool)

	err := crsInstance.UpdateProofNumber(ctx, crs.ProofUpdate{
		NodeID: nodeID,
		Type:   crs.ProofUpdateTypeDisproven,
		Reason: reason,
		Source: crs.SignalSourceSafety, // Safety violation is a hard signal
	})
	if err != nil {
		slog.Warn("CRS-02: failed to mark tool disproven",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", inv.Tool),
			slog.String("error", err.Error()),
		)
		return
	}

	// Propagate disproof to parent decisions
	affected := crsInstance.PropagateDisproof(ctx, nodeID)
	if affected > 0 {
		slog.Debug("CRS-02: disproof propagated to parents",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", inv.Tool),
			slog.Int("affected_nodes", affected),
		)
	}
}
