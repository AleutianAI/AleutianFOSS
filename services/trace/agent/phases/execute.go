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

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/classifier"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/grounding"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/integration"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/routing"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"github.com/AleutianAI/AleutianFOSS/services/trace/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// executePhaseTracer is the OpenTelemetry tracer for the execute phase.
var executePhaseTracer = otel.Tracer("aleutian.agent.execute")

// ExecutePhase handles the main tool execution loop.
//
// This phase is responsible for:
//   - Sending requests to the LLM with current context
//   - Parsing and executing tool calls from LLM responses
//   - Running safety checks on proposed changes
//   - Updating context with tool results
//   - Forcing tool usage for analytical queries
//   - Determining when to reflect or complete
//
// Thread Safety: ExecutePhase is safe for concurrent use.
type ExecutePhase struct {
	// maxTokens is the maximum tokens for LLM responses.
	maxTokens int

	// reflectionThreshold triggers reflection after this many steps.
	reflectionThreshold int

	// requireSafetyCheck requires safety checks for modifications.
	requireSafetyCheck bool

	// maxGroundingRetries is the max retries on grounding failures (circuit breaker).
	maxGroundingRetries int

	// forcingPolicy determines when to force tool usage.
	forcingPolicy ToolForcingPolicy

	// maxToolForcingRetries limits tool forcing attempts (circuit breaker).
	maxToolForcingRetries int

	// maxStepForForcing is the maximum step number where tool forcing applies.
	maxStepForForcing int

	// toolChoiceSelector selects tool_choice based on query classification.
	// This enables API-level tool forcing rather than prompt-only.
	toolChoiceSelector *classifier.ToolChoiceSelector

	// responseValidator validates LLM responses for quality.
	// Checks for prohibited patterns and tool call requirements.
	responseValidator *classifier.RetryableValidator

	// qualityValidator validates response quality (hedging, citations).
	// Checks for evidence-based claims in analytical responses.
	qualityValidator *classifier.QualityValidator

	// symbolCache caches symbol resolutions per session (CB-31d).
	// Key: "sessionID:symbolName", Value: SymbolResolution
	// Thread Safety: sync.Map is safe for concurrent use.
	symbolCache sync.Map

	// prefilter narrows tool candidates before the LLM router classifies (CB-38).
	// nil = disabled (backward compatible, no behavior change).
	prefilter *routing.PreFilter
}

// ExecutePhaseOption configures an ExecutePhase.
type ExecutePhaseOption func(*ExecutePhase)

// WithMaxTokens sets the maximum response tokens.
//
// Inputs:
//
//	tokens - Maximum token count.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithMaxTokens(tokens int) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.maxTokens = tokens
	}
}

// WithReflectionThreshold sets when to trigger reflection.
//
// Inputs:
//
//	steps - Number of steps before reflection.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithReflectionThreshold(steps int) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.reflectionThreshold = steps
	}
}

// WithSafetyCheck enables or disables safety checks.
//
// Inputs:
//
//	required - Whether safety checks are required.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithSafetyCheck(required bool) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.requireSafetyCheck = required
	}
}

// WithMaxGroundingRetries sets the maximum grounding validation retries.
//
// Inputs:
//
//	retries - Maximum retry count (circuit breaker threshold).
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithMaxGroundingRetries(retries int) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.maxGroundingRetries = retries
	}
}

// WithToolForcingPolicy sets the policy for forcing tool usage.
//
// Inputs:
//
//	policy - The tool forcing policy. If nil, forcing is disabled.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithToolForcingPolicy(policy ToolForcingPolicy) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.forcingPolicy = policy
	}
}

// WithMaxToolForcingRetries sets the maximum tool forcing retries.
//
// Inputs:
//
//	retries - Maximum retry count (circuit breaker threshold).
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithMaxToolForcingRetries(retries int) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.maxToolForcingRetries = retries
	}
}

// WithMaxStepForForcing sets the maximum step for tool forcing.
//
// Inputs:
//
//	step - Maximum step number where forcing applies.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithMaxStepForForcing(step int) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.maxStepForForcing = step
	}
}

// WithQueryClassifier sets the query classifier for both tool forcing and
// tool choice selection.
//
// Description:
//
//	Sets a custom QueryClassifier that will be used by both the forcing
//	policy (to determine when to force tool usage) and the tool choice
//	selector (to select API-level tool_choice parameter).
//
//	This enables using the LLMClassifier instead of the default RegexClassifier
//	for more accurate query classification.
//
// Inputs:
//
//	c - The query classifier to use. If nil, defaults to RegexClassifier.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
//
// Example:
//
//	// Use LLM-based classifier
//	llmClassifier, _ := classifier.NewLLMClassifier(client, toolDefs, config)
//	phase := NewExecutePhase(WithQueryClassifier(llmClassifier))
//
// Thread Safety: The option is safe for concurrent use if the classifier is.
func WithQueryClassifier(c classifier.QueryClassifier) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		if c == nil {
			c = classifier.NewRegexClassifier()
		}
		// Update both components to use the same classifier
		p.forcingPolicy = NewDefaultForcingPolicyWithClassifier(c)
		p.toolChoiceSelector = classifier.NewToolChoiceSelector(c, nil)
	}
}

// WithPreFilter sets the pre-filter for narrowing tool candidates (CB-38).
//
// Description:
//
//	When set, the pre-filter runs before the LLM router to narrow
//	55+ tools to 5-10 candidates (or force a selection outright).
//	nil = disabled (backward compatible).
//
// Inputs:
//
//	pf - The pre-filter instance. May be nil to disable.
//
// Outputs:
//
//	ExecutePhaseOption - The configuration function.
func WithPreFilter(pf *routing.PreFilter) ExecutePhaseOption {
	return func(p *ExecutePhase) {
		p.prefilter = pf
	}
}

// NewExecutePhase creates a new execution phase.
//
// Inputs:
//
//	opts - Configuration options.
//
// Outputs:
//
//	*ExecutePhase - The configured phase.
func NewExecutePhase(opts ...ExecutePhaseOption) *ExecutePhase {
	// Create classifier for hybrid stack
	queryClassifier := classifier.NewRegexClassifier()

	p := &ExecutePhase{
		maxTokens:             4096,
		reflectionThreshold:   10,
		requireSafetyCheck:    true,
		maxGroundingRetries:   3, // Circuit breaker for hallucination retries
		forcingPolicy:         NewDefaultForcingPolicy(),
		maxToolForcingRetries: 2, // Circuit breaker for tool forcing
		maxStepForForcing:     2, // Only force on early steps
		// Hybrid stack components
		toolChoiceSelector: classifier.NewToolChoiceSelector(queryClassifier, nil),
		responseValidator:  classifier.NewRetryableValidator(2), // Max 2 retries
		qualityValidator:   classifier.NewQualityValidator(nil), // Default config
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Name implements Phase.
//
// Outputs:
//
//	string - "execute"
func (p *ExecutePhase) Name() string {
	return "execute"
}

// Execute implements Phase.
//
// Description:
//
//	Runs a single step of the execution loop:
//	1. Build prompt with current context
//	2. Call LLM for response
//	3. Parse tool calls from response
//	4. For each tool call: run safety check, execute tool
//	5. Update context with results
//	6. Check if reflection is needed
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	deps - Phase dependencies.
//
// Outputs:
//
//	agent.AgentState - Next state (EXECUTE, REFLECT, COMPLETE, or ERROR).
//	error - Non-nil only for unrecoverable errors.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) Execute(ctx context.Context, deps *Dependencies) (agent.AgentState, error) {
	if err := p.validateDependencies(deps); err != nil {
		slog.Error("ExecutePhase validation failed", slog.String("error", err.Error()))
		return agent.StateError, err
	}

	// GR-44 Rev 2: DO NOT reset CB flag here!
	// The CB flag must persist across Execute() retries within the same query.
	// When retryWithStrongerToolChoice returns StateExecute, the agent loop
	// calls Execute() again - we need the CB flag to survive that re-entry.
	// The flag is naturally reset when a new session/query starts.

	// GR-41b: Log router status at session start for debugging
	routerEnabled := deps.Session.IsToolRouterEnabled()
	routerModel := deps.Session.Config.ToolRouterModel

	slog.Info("ExecutePhase starting",
		slog.String("session_id", deps.Session.ID),
		slog.String("query", deps.Query),
		slog.Bool("router_enabled", routerEnabled),
		slog.String("router_model", routerModel),
	)

	// Record router status in trace for debugging
	if !routerEnabled && deps.Session.Config.ToolRouterEnabled {
		// Router was configured but failed to initialize - surface this issue
		slog.Warn("GR-41b: Router configured but not initialized - falling back to main LLM for tool selection",
			slog.String("session_id", deps.Session.ID),
			slog.String("configured_model", routerModel),
		)
		deps.Session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "router_fallback",
			Target:    routerModel,
			Error:     "Router configured but not initialized - check server logs for initialization errors",
			Metadata: map[string]string{
				"configured_model": routerModel,
				"fallback":         "main_llm",
			},
		})
	}

	stepStart := time.Now()
	stepNumber := 0
	if deps.EventEmitter != nil {
		stepNumber = deps.EventEmitter.IncrementStep()
	}

	slog.Info("Building LLM request",
		slog.String("session_id", deps.Session.ID),
		slog.Int("step", stepNumber),
	)

	// IT-08e: Speculative parallel param extraction.
	// Run pre-filter FIRST to get top candidate, then launch LLM extraction
	// on a dedicated model (ministral-3:3b) in parallel with routing
	// (granite4:micro-h on a separate Ollama model slot).
	type llmParamResult struct {
		params map[string]any
		tool   string
		err    error
	}
	llmCh := make(chan llmParamResult, 1)

	var speculativeTool string
	if deps.ParamExtractor != nil && deps.ParamExtractor.IsEnabled() && p.prefilter != nil {
		var pfToolDefs []tools.ToolDefinition
		if deps.ToolRegistry != nil {
			pfToolDefs = deps.ToolRegistry.GetDefinitions()
		}
		pfSpecs := toolDefsToSpecs(pfToolDefs)
		var speculativeSessionCounts map[string]int
		if deps.Session != nil {
			speculativeSessionCounts = buildToolCountMapFromSession(deps.Session)
		}
		pfResult := p.prefilter.FilterAgentSpecs(ctx, deps.Query, pfSpecs, speculativeSessionCounts)

		if pfResult.ForcedTool != "" {
			speculativeTool = pfResult.ForcedTool
		} else if len(pfResult.NarrowedSpecs) > 0 {
			speculativeTool = pfResult.NarrowedSpecs[0].Name
		}

		if speculativeTool != "" {
			go func(tool string) {
				var toolDef *tools.ToolDefinition
				for i := range pfToolDefs {
					if pfToolDefs[i].Name == tool {
						toolDef = &pfToolDefs[i]
						break
					}
				}
				if toolDef == nil {
					llmCh <- llmParamResult{nil, tool, fmt.Errorf("tool def not found: %s", tool)}
					return
				}
				schemas := buildParamSchemas(toolDef)
				if len(schemas) == 0 {
					llmCh <- llmParamResult{nil, tool, fmt.Errorf("no schemas for %s", tool)}
					return
				}
				// No regex hint — LLM extracts from scratch (parallel, no bias)
				result, err := deps.ParamExtractor.ExtractParams(
					ctx, deps.Query, tool, schemas, map[string]any{},
				)
				llmCh <- llmParamResult{result, tool, err}
			}(speculativeTool)
		} else {
			llmCh <- llmParamResult{nil, "", fmt.Errorf("no prefilter candidate")}
		}
	} else {
		llmCh <- llmParamResult{nil, "", fmt.Errorf("param extractor not available")}
	}

	// Build the LLM request (includes routing — ~570ms on granite4:micro-h).
	// While this blocks, ministral-3:3b is extracting params in parallel.
	request, hardForcing, buildErr := p.buildLLMRequest(deps)
	if buildErr != nil {
		// GR-44 Rev 2: Router errors are fatal - propagate up
		slog.Error("GR-44: buildLLMRequest failed due to router error",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", buildErr.Error()),
		)
		return agent.StateError, buildErr
	}

	// GR-59 Rev 4d: Pre-LLM synthesis check — MUST be before hardForcing block.
	// If a forced graph tool already completed (flag set by executeToolDirectlyWithFallback),
	// skip ALL further tool execution and force synthesis directly. This prevents the
	// router from forcing Grep/Glob/Read after the graph already answered authoritatively.
	// Previous placement (Rev 4c) was AFTER the hardForcing block, so it was bypassed
	// when the router selected a non-graph tool (e.g., Grep) on subsequent steps.
	if stepNumber >= 1 && deps.Session != nil && deps.Session.GraphToolHadSubstantiveResults() {
		slog.Info("GR-59 Rev 4d: Pre-tool synthesis — forced graph tool already completed, skipping router-forced tool",
			slog.String("session_id", deps.Session.ID),
			slog.Int("step_number", stepNumber),
			slog.String("router_suggested", func() string {
				if hardForcing != nil {
					return hardForcing.Tool
				}
				return "none"
			}()),
		)
		deps.Session.SetCircuitBreakerActive(true)
		return p.forceLLMSynthesis(ctx, deps, request, stepStart, stepNumber)
	}

	// O1.3 Fix: Capture semantic info for trace step metadata
	var lastSemanticInfo *SemanticInfo

	// Check if hard forcing is enabled (router selected a real tool with high confidence)
	if hardForcing != nil {
		// GR-38 Fix (Issue 17): Use semantic similarity to detect duplicates.
		// This allows same tool with different params (e.g., Grep "parseConfig" vs Grep "buildRequest")
		// while still blocking semantically equivalent calls (Grep "parseConfig" vs Grep "parse_config").

		// O1.2 Fix: Add trace span for semantic check
		_, semanticSpan := executePhaseTracer.Start(ctx, "semantic_duplicate_check",
			trace.WithAttributes(
				attribute.String("tool", hardForcing.Tool),
				attribute.String("query_preview", truncateQuery(deps.Query, 100)),
			),
		)

		semanticHistory := buildSemanticToolHistoryFromSession(deps.Session)
		status, similarity, similarCall := routing.CheckSemanticStatus(
			semanticHistory,
			hardForcing.Tool,
			deps.Query, // Use the user query for semantic comparison
		)

		// O1.3 Fix: Capture semantic info for later use
		lastSemanticInfo = &SemanticInfo{
			Similarity: similarity,
			Status:     status,
		}

		// O1.2 Fix: Record span attributes
		semanticSpan.SetAttributes(
			attribute.String("status", status),
			attribute.Float64("similarity", similarity),
			attribute.Int("history_size", semanticHistory.Len()),
		)
		semanticSpan.End()

		if status == "blocked" {
			slog.Info("GR-38: Skipping hard force, semantically similar call already made",
				slog.String("session_id", deps.Session.ID),
				slog.String("tool", hardForcing.Tool),
				slog.Float64("similarity", similarity),
				slog.String("status", status),
			)
			if similarCall != nil {
				slog.Debug("GR-38: Similar previous call details",
					slog.String("previous_query", similarCall.RawQuery),
					slog.Int("previous_step", similarCall.StepNumber),
				)
			}
			// Cancel hard forcing - fall through to normal LLM flow
			hardForcing = nil
		} else if status == "penalized" {
			// Log but allow - the UCB1 scorer will apply a penalty if this continues
			slog.Debug("GR-38: Similar but distinct call detected, allowing with penalty awareness",
				slog.String("tool", hardForcing.Tool),
				slog.Float64("similarity", similarity),
			)
		}
	}

	// Execute hard forcing if still enabled after duplicate check
	if hardForcing != nil {
		// TR-2 Fix: Execute tool directly with full observability
		slog.Info("Router hard-forcing tool, attempting direct execution (CB-31d)",
			slog.String("session_id", deps.Session.ID),
			slog.String("forced_tool", hardForcing.Tool),
		)

		// Get tool definitions for parameter extraction
		var toolDefs []tools.ToolDefinition
		if deps.ToolRegistry != nil {
			toolDefs = deps.ToolRegistry.GetDefinitions()
		}

		// TR-1 Fix: Extract tool parameters from query/context
		// CB-31d: Pass deps for symbol resolution
		params, paramErr := p.extractToolParameters(ctx, deps.Query, hardForcing.Tool, toolDefs, deps.Context, deps)

		// IT-08e: Collect speculative LLM param result (already running in parallel).
		// The goroutine was launched BEFORE buildLLMRequest, so it has had ~570ms
		// to complete on ministral-3:3b (~150ms typical). Result is ready.
		llmResult := <-llmCh
		if llmResult.err == nil && llmResult.tool == hardForcing.Tool && llmResult.params != nil {
			// Speculative hit: router confirmed our pre-filter prediction
			converted, convErr := convertMapToTypedParams(hardForcing.Tool, llmResult.params)
			if convErr == nil {
				slog.Info("IT-08e: LLM param extraction succeeded (parallel, speculative hit)",
					slog.String("tool", hardForcing.Tool),
				)
				params = converted
				paramErr = nil
			} else {
				slog.Warn("IT-08e: LLM param conversion failed, using regex",
					slog.String("tool", hardForcing.Tool),
					slog.String("error", convErr.Error()),
				)
			}
		} else if llmResult.tool != "" && llmResult.tool != hardForcing.Tool {
			// Mispredict: router picked a different tool than pre-filter top
			slog.Info("IT-08e: Speculative mispredict, discarding LLM params",
				slog.String("speculated", llmResult.tool),
				slog.String("actual", hardForcing.Tool),
			)
		} else if llmResult.err != nil {
			slog.Debug("IT-08e: LLM param extraction unavailable, using regex",
				slog.String("reason", llmResult.err.Error()),
			)
		}

		if paramErr != nil {
			// TR-7 Fix: Fallback to Main LLM on parameter extraction failure
			slog.Warn("Parameter extraction failed, falling back to Main LLM (CB-31d)",
				slog.String("tool", hardForcing.Tool),
				slog.String("error", paramErr.Error()),
			)
			grounding.RecordRouterFallback(hardForcing.Tool, "param_extraction_failed")
			// GR-Phase1 Fix: When hard forcing fails, check if we already have tool results.
			// If so, don't require new tool calls - the LLM should synthesize from existing results.
			// If not, use ToolChoiceAny to encourage tool use without forcing a specific tool.
			if deps.Context != nil && len(deps.Context.ToolResults) > 0 {
				// Already have tool results - let LLM decide (may synthesize)
				request.ToolChoice = nil
				slog.Info("GR-Phase1: Skipping tool forcing, already have tool results",
					slog.String("session_id", deps.Session.ID),
					slog.Int("tool_results", len(deps.Context.ToolResults)),
				)
			} else {
				request.ToolChoice = llm.ToolChoiceAny()
			}
		} else {
			execResult, execErr := p.executeToolDirectlyWithFallback(ctx, deps, hardForcing.Tool, params, toolDefs)
			if execErr != nil {
				// TR-3 Fix: Fallback to Main LLM if direct execution fails
				slog.Warn("Hard-forced tool execution failed, falling back to Main LLM (CB-31d)",
					slog.String("tool", hardForcing.Tool),
					slog.String("error", execErr.Error()),
				)
				grounding.RecordRouterFallback(hardForcing.Tool, "execution_failed")
				// GR-Phase1 Fix: Same as above - check for existing tool results
				if deps.Context != nil && len(deps.Context.ToolResults) > 0 {
					request.ToolChoice = nil
					slog.Info("GR-Phase1: Skipping tool forcing, already have tool results",
						slog.String("session_id", deps.Session.ID),
						slog.Int("tool_results", len(deps.Context.ToolResults)),
					)
				} else {
					request.ToolChoice = llm.ToolChoiceAny()
				}
			} else {
				// Success! Tool executed directly - return early
				grounding.RecordRouterHardForced(hardForcing.Tool, true)
				// O1.3 Fix: Include semantic info in trace step
				if lastSemanticInfo != nil {
					p.emitToolRouting(deps, hardForcing, *lastSemanticInfo)
				} else {
					p.emitToolRouting(deps, hardForcing)
				}

				// GR-59 Rev 5: For graph tools, force synthesis immediately.
				// Graph tools return authoritative results (positive or negative).
				// Returning StateExecute lets the LLM spiral into Grep/Glob loops
				// trying to verify graph results, which wastes steps and triggers
				// circuit breakers. Force the LLM to synthesize from the graph
				// result directly — no further tool calls needed.
				if graphToolsWithSubstantiveResults[hardForcing.Tool] {
					slog.Info("GR-59 Rev 5: Graph tool completed — forcing immediate synthesis",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", hardForcing.Tool),
					)
					return p.forceLLMSynthesis(ctx, deps, request, stepStart, stepNumber)
				}

				// Non-graph tools: return to execution loop for further processing
				return execResult.NextState, nil
			}
		}
	}

	// Send request to LLM
	slog.Info("Sending LLM request",
		slog.String("session_id", deps.Session.ID),
		slog.Int("max_tokens", request.MaxTokens),
		slog.Int("tool_count", len(request.Tools)),
	)

	response, err := p.callLLM(ctx, deps, request)
	if err != nil {
		slog.Error("LLM request failed",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)
		return p.handleLLMError(deps, err)
	}

	slog.Info("LLM response received",
		slog.String("session_id", deps.Session.ID),
		slog.Int("output_tokens", response.OutputTokens),
		slog.Bool("has_tool_calls", response.HasToolCalls()),
		slog.Int("content_len", len(response.Content)),
	)

	// TR-5 Fix: Circuit Breaker "Desperation" Trap
	// Check if circuit breaker was active and LLM tried to escape constraint
	// by outputting tool calls in its text response.
	if !response.HasToolCalls() {
		// Check if this was a circuit breaker forced answer by looking at the request
		circuitBreakerActive := request.ToolChoice != nil && request.ToolChoice.Type == "none" && request.Tools == nil
		if circuitBreakerActive && containsToolCallPattern(response.Content) {
			// LLM escaped the constraint! It output tool calls in text despite ToolChoiceNone()
			slog.Warn("Circuit breaker desperation trap: LLM escaped ToolChoiceNone() constraint (CB-31d)",
				slog.String("session_id", deps.Session.ID),
				slog.Int("response_len", len(response.Content)),
				slog.String("response_preview", truncateForLog(response.Content, 200)),
			)

			// Retry with stronger prompt that explicitly forbids tool calls
			return p.retryDesperationWithStrongerPrompt(ctx, deps, stepStart, stepNumber)
		}

		// Normal completion - no circuit breaker or no escaped patterns
		slog.Info("No tool calls, completing",
			slog.String("session_id", deps.Session.ID),
		)

		return p.handleCompletion(ctx, deps, response, request, stepStart, stepNumber)
	}

	// Parse and execute tool calls
	// IT-05 R5: Use ParseToolCallsWithReAct which tries native JSON parsing first,
	// then falls back to ReAct text parsing for models that emit raw text like
	// "Read src/flask/app.py 1-200" instead of JSON tool calls.
	invocations, usedReAct := llm.ParseToolCallsWithReAct(response)
	if usedReAct {
		slog.Info("IT-05 R5: ReAct fallback parsed tool call from text response",
			slog.Int("invocations", len(invocations)),
		)
	}

	// CRITICAL: Add assistant message with tool calls to conversation history BEFORE execution.
	// This ensures the LLM sees "I requested tool X" followed by "tool X returned Y".
	// Without this, tool results become orphaned and the LLM keeps re-requesting the same tool.
	p.addAssistantToolCallToHistory(deps, response, invocations)

	toolResults, blocked := p.executeToolCalls(ctx, deps, invocations)

	// Update context with results
	p.updateContextWithResults(ctx, deps, toolResults)

	// Handle safety block
	if blocked {
		p.emitError(deps, fmt.Errorf("execution blocked by safety check"), true)

		// IT-06c I-13: Early-exit when all tool calls in this batch were blocked.
		// Without this, the loop continues futilely (steps 1-9 can loop with zero
		// progress before reflection threshold at step 10). Force synthesis from
		// any accumulated results instead of wasting time on blocked tool calls.
		if deps.Session.IsCircuitBreakerActive() {
			slog.Info("IT-06c I-13: All tool calls blocked, forcing synthesis",
				slog.String("session_id", deps.Session.ID),
				slog.Int("step_number", stepNumber),
			)
			return p.forceLLMSynthesis(ctx, deps, request, stepStart, stepNumber)
		}
	}

	// Emit step complete event
	p.emitStepComplete(deps, stepStart, stepNumber, len(invocations))

	// GR-41b: Force synthesis after graph tools return substantive results.
	// This prevents the agent from calling Grep 10+ times after find_callees
	// already returned the answer. If graph tools produced results and we've
	// done at least 2 steps, synthesize instead of continuing to search.
	if p.shouldForceSynthesisAfterGraphTools(deps, toolResults, stepNumber) {
		slog.Info("GR-41b: Forcing synthesis after graph tool results",
			slog.String("session_id", deps.Session.ID),
			slog.Int("step_number", stepNumber),
			slog.Int("tool_results", len(toolResults)),
		)

		// First try to synthesize from the current batch's tool results.
		synthResult := p.synthesizeFromGraphToolResults(deps, toolResults, "graph_tool_completion")
		if synthResult != "" {
			response.Content = synthResult
			return p.handleCompletion(ctx, deps, response, request, stepStart, stepNumber)
		}

		// GR-59 Rev 2: If current batch has no graph results (e.g., only CB errors),
		// force the LLM to produce an answer from conversation history which already
		// contains the graph tool results from prior steps.
		slog.Info("GR-59 Rev 2: Current batch has no graph results, forcing LLM synthesis from history",
			slog.String("session_id", deps.Session.ID),
		)
		deps.Session.SetCircuitBreakerActive(true)
		return p.forceLLMSynthesis(ctx, deps, request, stepStart, stepNumber)
	}

	// Check if reflection is needed
	if p.shouldReflect(deps, stepNumber) {
		p.emitStateTransition(deps, agent.StateExecute, agent.StateReflect, "reflection threshold reached")
		return agent.StateReflect, nil
	}

	// Continue execution
	p.emitStateTransition(deps, agent.StateExecute, agent.StateExecute, "continuing execution")
	return agent.StateExecute, nil
}

// validateDependencies checks that required dependencies are present.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	error - Non-nil if dependencies are missing.
func (p *ExecutePhase) validateDependencies(deps *Dependencies) error {
	if deps == nil {
		return fmt.Errorf("dependencies are nil")
	}
	if deps.Session == nil {
		return fmt.Errorf("session is nil")
	}
	if deps.Context == nil {
		return fmt.Errorf("context is nil")
	}
	if deps.LLMClient == nil {
		return fmt.Errorf("LLM client is nil")
	}
	// ToolExecutor is optional - if nil, we skip tool execution
	return nil
}

// buildLLMRequest creates an LLM request from current context.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	*llm.Request - The LLM request.
//	*agent.ToolRouterSelection - Non-nil if hard forcing is enabled.
//	error - Non-nil if router is configured but fails (GR-44: fatal, no fallback).
func (p *ExecutePhase) buildLLMRequest(deps *Dependencies) (*llm.Request, *agent.ToolRouterSelection, error) {
	// Get available tools
	var toolDefs []tools.ToolDefinition
	var toolNames []string
	if deps.ToolRegistry != nil {
		toolDefs = deps.ToolRegistry.GetDefinitions()
		toolNames = make([]string, len(toolDefs))
		for i, def := range toolDefs {
			toolNames[i] = def.Name
		}
	}

	request := llm.BuildRequest(deps.Context, toolDefs, p.maxTokens)

	// History-Aware Routing: Route on EVERY step with full context.
	// The router (Granite4:3b-h with Mamba2 architecture) handles O(n) linear
	// complexity and can efficiently process tool history to avoid the
	// "Amnesiac Router" bug where it keeps suggesting the same tool.
	//
	// Key insight: Mamba2's 1M token context window and linear complexity
	// means we can pass full tool history without performance penalty.
	routerUsed := false

	// CB-31d: Detailed logging for router usage decision
	sessionExists := deps.Session != nil
	routerEnabled := sessionExists && deps.Session.IsToolRouterEnabled()
	hasQuery := deps.Query != ""
	hasTools := len(toolDefs) > 0

	slog.Info("CB-31d Router decision point",
		slog.Bool("session_exists", sessionExists),
		slog.Bool("router_enabled", routerEnabled),
		slog.Bool("has_query", hasQuery),
		slog.Bool("has_tools", hasTools),
		slog.Int("num_tools", len(toolDefs)),
	)

	if sessionExists && routerEnabled && hasQuery && hasTools {
		router := deps.Session.GetToolRouter()
		slog.Info("CB-31d Router check passed, getting router",
			slog.Bool("router_is_nil", router == nil),
		)
		if router != nil {
			routerSelection, routerErr := p.tryToolRouterSelection(context.Background(), deps, router, toolDefs)
			// GR-44 Rev 2: Router errors are fatal - propagate up
			if routerErr != nil {
				return nil, nil, routerErr
			}
			if routerSelection != nil {
				// Handle meta-actions vs real tools.
				// "answer" and "clarify" are meta-actions that aren't real tools.
				// Using ToolChoiceRequired("answer") would tell the LLM to call a non-existent
				// tool, which it ignores. Fixed in cb_30a after trace_logs_19 analysis.
				if routerSelection.Tool == "answer" || routerSelection.Tool == "clarify" {
					// Meta-action: disable tool calling to force text response
					request.ToolChoice = llm.ToolChoiceNone()
					// Also remove tools from request to reinforce no-tools directive
					request.Tools = nil
					routerUsed = true

					// Log if circuit breaker forced this answer
					circuitBreakerFired := strings.Contains(routerSelection.Reasoning, "Circuit breaker:")

					// CB-31d: When circuit breaker forces answer, add synthesis instruction
					// to override any previous "call a tool" retry messages in conversation.
					// CRS-07: Added explicit prohibition of XML tool_call syntax after
					// trace_logs_30 showed GLM-4.7-flash outputting malformed XML that
					// crashed Ollama's parser.
					if circuitBreakerFired {
						// IT-3/10 FIX-3b: Anti-hallucination circuit breaker prompt.
						// Addresses ISSUE-03, 09, 10, 12, 14, 17F.
						synthesisPrompt := `You have gathered information from tools. Now provide a complete answer.

MANDATORY: YOU MUST RESPOND
- You MUST provide a text response
- Empty responses are not allowed
- If you don't know, say "I don't know" or "Not found"
- NEVER return an empty response

CRITICAL - DO NOT OUTPUT ANY XML OR TOOL CALLS:
- No <tool_call> or </tool_call> tags
- No <function> or </function> tags
- No XML-formatted invocations
- Tools are now disabled - you cannot call more tools

CRITICAL - DO NOT FABRICATE:
- ONLY report data that appeared in tool results above
- Do NOT use training knowledge about this library/framework
- Do NOT invent file paths, function names, line numbers, or call chains
- If tool returned "not found" or empty, say so — do NOT fill in from memory
- If tool returned data for a different question, state what was returned honestly

DO:
- Synthesize a clear answer from the tool results above
- State "not found" if results are empty
- Be concise (2-3 paragraphs maximum)

Provide your answer now:`
						request.Messages = append(request.Messages, llm.Message{
							Role:    "user",
							Content: synthesisPrompt,
						})
					}

					slog.Info("Router selected meta-action, disabling tools (cb_30a fix)",
						slog.String("session_id", deps.Session.ID),
						slog.String("meta_action", routerSelection.Tool),
						slog.Float64("confidence", routerSelection.Confidence),
						slog.Duration("routing_duration", routerSelection.Duration),
						slog.String("reasoning", routerSelection.Reasoning),
						slog.Bool("circuit_breaker_fired", circuitBreakerFired),
					)
				} else {
					// Real tool: Mark for HARD FORCING
					// This will be handled in Execute() before LLM call
					slog.Debug("Router selected real tool, marking for hard forcing",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", routerSelection.Tool),
						slog.Float64("confidence", routerSelection.Confidence),
					)
					// GR-44 Rev 2: Set routerUsed before early return to satisfy enforcement check
					routerUsed = true
					// Return request with hard forcing selection
					return request, routerSelection, nil
				}
				// Emit routing event if we didn't exit early
				if routerUsed {
					p.emitToolRouting(deps, routerSelection)
				}
			}
		}
	}

	// GR-44 Rev 2: Router enforcement - if router is configured, it MUST be used.
	// No silent fallback to classifier allowed.
	if sessionExists && deps.Session.Config.ToolRouterEnabled {
		if !routerUsed {
			// Router was configured but not used - this is a fatal error
			router := deps.Session.GetToolRouter()
			if router == nil {
				errMsg := "GR-44: Router configured but not initialized (nil). Cannot proceed."
				slog.Error(errMsg,
					slog.String("session_id", deps.Session.ID),
					slog.String("configured_model", deps.Session.Config.ToolRouterModel),
				)
				// Record in trace for debugging
				deps.Session.RecordTraceStep(crs.TraceStep{
					Timestamp: time.Now().UnixMilli(),
					Action:    "router_fatal_error",
					Error:     errMsg,
				})
				return nil, nil, errors.New(errMsg)
			}
			// If router exists but wasn't used, something else went wrong
			errMsg := "GR-44: Router configured and initialized but not used. This indicates a logic error."
			slog.Error(errMsg,
				slog.String("session_id", deps.Session.ID),
			)
			return nil, nil, errors.New(errMsg)
		}
	}

	// Classifier fallback ONLY allowed when router is NOT configured.
	// This prevents the main LLM from selecting tools - that's the router's job.
	if !routerUsed && !deps.Session.Config.ToolRouterEnabled && p.toolChoiceSelector != nil && deps.Query != "" && len(toolDefs) > 0 {
		selection := p.toolChoiceSelector.SelectToolChoice(context.Background(), deps.Query, toolNames)

		// Only set tool_choice for analytical queries
		if selection.IsAnalytical {
			request.ToolChoice = selection.ToolChoice

			slog.Debug("tool_choice selected (classifier - router not configured)",
				slog.String("session_id", deps.Session.ID),
				slog.String("tool_choice_type", selection.ToolChoice.Type),
				slog.String("suggested_tool", selection.SuggestedTool),
				slog.Float64("confidence", selection.Confidence),
				slog.String("reason", selection.Reason),
			)
		}
	}

	return request, nil, nil
}

// tryToolRouterSelection attempts to get a tool selection from the ToolRouter.
//
// Description:
//
//	Converts tool definitions to ToolSpecs, calls the router, and returns
//	the selection if confidence is above threshold.
//	GR-44 Rev 2: Router errors are now FATAL - no silent fallback allowed.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	router - The ToolRouter to use.
//	toolDefs - Available tool definitions.
//
// Outputs:
//
//	*agent.ToolRouterSelection - The selection if confident, nil for low confidence.
//	error - Non-nil if router fails (GR-44: fatal error, no fallback).
func (p *ExecutePhase) tryToolRouterSelection(ctx context.Context, deps *Dependencies, router agent.ToolRouter, toolDefs []tools.ToolDefinition) (*agent.ToolRouterSelection, error) {
	// GR-Phase1: Track router invocation count to debug duplicate calls
	routerCallID := time.Now().UnixNano()
	slog.Info("CB-31d tryToolRouterSelection CALLED",
		slog.String("session_id", deps.Session.ID),
		slog.Int("num_tool_defs", len(toolDefs)),
		slog.String("router_model", router.Model()),
		slog.Int64("call_id", routerCallID),
	)

	ctx, span := executePhaseTracer.Start(ctx, "ExecutePhase.tryToolRouterSelection")
	defer span.End()

	// Convert tool definitions to ToolSpecs for the router
	toolSpecs := toolDefsToSpecs(toolDefs)

	// GR-61: Build session counts once for both the prefilter UCB1 penalty
	// and the legacy circuit-breaker check below. Avoids iterating trace steps
	// twice in the same function call.
	var sessionCounts map[string]int
	if deps.Session != nil {
		sessionCounts = buildToolCountMapFromSession(deps.Session)
	}

	// CB-38: Pre-filter narrows candidate set before LLM router
	if p.prefilter != nil {
		pfResult := p.prefilter.FilterAgentSpecs(ctx, deps.Query, toolSpecs, sessionCounts)
		if pfResult.ForcedTool != "" {
			// CB-38: Check circuit breaker before accepting forced selection.
			// A forced tool that has been called too many times should not bypass
			// the circuit breaker — fall through to normal routing instead.
			cbBlocked := false
			if deps.Session != nil && deps.Session.HasCRS() {
				cbResult := deps.Session.GetCRS().CheckCircuitBreaker(deps.Session.ID, pfResult.ForcedTool)
				if cbResult.ShouldFire {
					cbBlocked = true
					slog.Warn("CB-38 prefilter forced tool blocked by circuit breaker",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", pfResult.ForcedTool),
						slog.String("cb_reason", cbResult.Reason),
					)
				}
			} else if deps.Session != nil {
				// GR-61: reuse sessionCounts built above — no second iteration.
				if sessionCounts[pfResult.ForcedTool] >= maxRepeatedToolCalls {
					cbBlocked = true
					slog.Warn("CB-38 prefilter forced tool blocked by circuit breaker (legacy)",
						slog.String("session_id", deps.Session.ID),
						slog.String("tool", pfResult.ForcedTool),
						slog.Int("call_count", sessionCounts[pfResult.ForcedTool]),
					)
				}
			}

			if !cbBlocked {
				slog.Info("CB-38 prefilter forced tool selection",
					slog.String("session_id", deps.Session.ID),
					slog.String("tool", pfResult.ForcedTool),
					slog.String("reason", pfResult.ForcedReason),
				)
				span.SetAttributes(
					attribute.String("prefilter_forced_tool", pfResult.ForcedTool),
					attribute.String("prefilter_forced_reason", pfResult.ForcedReason),
				)

				// CB-38: Record forced selection as CRS TraceStep
				deps.Session.RecordTraceStep(crs.TraceStep{
					Timestamp: time.Now().UnixMilli(),
					Action:    "prefilter_forced",
					Target:    pfResult.ForcedTool,
					Tool:      "prefilter",
					Duration:  pfResult.Duration,
					Metadata: map[string]string{
						"reason":         pfResult.ForcedReason,
						"original_count": fmt.Sprintf("%d", pfResult.OriginalCount),
					},
				})

				return &agent.ToolRouterSelection{
					Tool:       pfResult.ForcedTool,
					Confidence: 1.0,
					Reasoning:  fmt.Sprintf("Prefilter forced: %s", pfResult.ForcedReason),
					Duration:   pfResult.Duration,
				}, nil
			}
			// Circuit breaker fired — fall through to normal routing with narrowed specs
		}
		toolSpecs = pfResult.NarrowedSpecs // 5-10 instead of 55
		span.SetAttributes(
			attribute.Int("prefilter_original", pfResult.OriginalCount),
			attribute.Int("prefilter_narrowed", pfResult.NarrowedCount),
		)
	}

	// Build code context for the router
	codeContext := p.buildCodeContext(deps)

	span.SetAttributes(
		attribute.Int("num_tools", len(toolSpecs)),
		attribute.String("query_preview", truncateQuery(deps.Query, 100)),
	)

	// Call the router
	slog.Info("CB-31d tryToolRouterSelection calling router.SelectTool",
		slog.String("session_id", deps.Session.ID),
		slog.Int("num_specs", len(toolSpecs)),
		slog.String("query_preview", truncateQuery(deps.Query, 100)),
	)

	selection, err := router.SelectTool(ctx, deps.Query, toolSpecs, codeContext)
	if err != nil {
		// GR-44 Rev 2: Router failure is FATAL - no silent fallback to classifier.
		// The router is the router PERIOD. If it fails, the whole process fails.
		errMsg := fmt.Sprintf("GR-44: Router selection failed: %v", err)
		slog.Error(errMsg,
			slog.String("session_id", deps.Session.ID),
			slog.String("router_model", router.Model()),
		)
		span.SetAttributes(attribute.String("router_fatal_error", err.Error()))
		span.SetStatus(codes.Error, errMsg)

		// Record in trace for debugging
		deps.Session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "router_fatal_error",
			Error:     errMsg,
		})

		return nil, errors.New(errMsg)
	}

	slog.Info("CB-31d tryToolRouterSelection router.SelectTool RETURNED",
		slog.String("session_id", deps.Session.ID),
		slog.String("selected_tool", selection.Tool),
		slog.Float64("confidence", selection.Confidence),
	)

	// GR-Phase1: Semantic validation to catch callers/callees confusion
	// Only validate if tool hasn't already been corrected (check for marker in reasoning)
	if !strings.Contains(selection.Reasoning, "Semantic correction:") {
		if correctedTool, wasChanged, reason := ValidateToolQuerySemantics(deps.Query, selection.Tool); wasChanged {
			originalTool := selection.Tool

			// GR-Phase1: Check if we've already applied this correction for this query
			// Use fast in-memory cache first, then fall back to trace step check
			alreadyCorrected := hasSemanticCorrectionInCache(deps.Session.ID, deps.Query, correctedTool)
			if !alreadyCorrected {
				// Double-check with trace steps (belt and suspenders)
				alreadyCorrected = hasSemanticCorrectionForQuery(deps.Session, deps.Query, correctedTool)
			}

			if !alreadyCorrected {
				// Log at INFO — the semantic correction is working as designed,
				// not indicating a problem. The router model simply can't distinguish
				// callers vs callees grammar; the correction layer fixes it.
				slog.Info("GR-Phase1: Router selection semantically corrected",
					slog.String("session_id", deps.Session.ID),
					slog.String("original_tool", originalTool),
					slog.String("corrected_tool", correctedTool),
					slog.String("reason", reason),
					slog.Int64("call_id", routerCallID),
				)

				// Mark as corrected in fast cache
				markSemanticCorrectionApplied(deps.Session.ID, deps.Query, correctedTool)

				// GR-Phase1: Record semantic correction in CRS trace for observability
				if deps.Session != nil && deps.Session.HasCRS() {
					deps.Session.RecordTraceStep(crs.TraceStep{
						Timestamp: time.Now().UnixMilli(),
						Action:    "semantic_correction",
						Target:    correctedTool,
						Tool:      "semantic_validator",
						Metadata: map[string]string{
							"original_tool":  originalTool,
							"corrected_tool": correctedTool,
							"reason":         reason,
							"query_preview":  truncateQuery(deps.Query, 100),
						},
					})
				}
			} else {
				slog.Debug("GR-Phase1: Semantic correction already applied, skipping duplicate",
					slog.String("session_id", deps.Session.ID),
					slog.String("corrected_tool", correctedTool),
				)
			}

			span.SetAttributes(
				attribute.String("semantic_correction_original", originalTool),
				attribute.String("semantic_correction_applied", correctedTool),
				attribute.String("semantic_correction_reason", reason),
			)

			selection.Tool = correctedTool
			selection.Reasoning = fmt.Sprintf("Semantic correction: %s. Original: %s", reason, selection.Reasoning)
		}
	}

	// Check confidence threshold
	threshold := 0.7 // Default
	if deps.Session.Config != nil && deps.Session.Config.ToolRouterConfidence > 0 {
		threshold = deps.Session.Config.ToolRouterConfidence
	}

	if selection.Confidence < threshold {
		// CB-31e Phase 4: Block fallback to main LLM for tool selection
		// Instead of returning nil (which triggers classifier fallback),
		// we log the low confidence and still use the router's suggestion
		// if we have gathered enough tool results to synthesize an answer.
		slog.Info("CB-31e: Router low confidence, checking if synthesis is possible",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", selection.Tool),
			slog.Float64("confidence", selection.Confidence),
			slog.Float64("threshold", threshold),
		)
		span.SetAttributes(
			attribute.String("fallback_blocked", "true"),
			attribute.Float64("confidence", selection.Confidence),
			attribute.Float64("threshold", threshold),
		)

		// Check if we have tool results to synthesize from
		hasToolResults := false
		var toolResultCount int
		if deps.Session != nil {
			if ctx := deps.Session.GetCurrentContext(); ctx != nil && len(ctx.ToolResults) > 0 {
				hasToolResults = true
				toolResultCount = len(ctx.ToolResults)
			}
		}

		if hasToolResults {
			// CB-31e: Force synthesis instead of fallback
			slog.Info("CB-31e: Forcing synthesis from existing tool results",
				slog.String("session_id", deps.Session.ID),
				slog.Int("tool_result_count", toolResultCount),
			)
			// Record metric
			config.RecordFallbackBlocked()
			// Return "answer" tool to force synthesis
			return &agent.ToolRouterSelection{
				Tool:       "answer",
				Confidence: 0.9,
				Reasoning:  "CB-31e: Router low confidence but tool results available - forcing synthesis",
			}, nil
		}

		// If no tool results, still try the router's suggestion (better than random classifier)
		// but log that this is a degraded path
		slog.Warn("CB-31e: No tool results available, using low-confidence router suggestion",
			slog.String("session_id", deps.Session.ID),
			slog.String("tool", selection.Tool),
			slog.Float64("confidence", selection.Confidence),
		)
		// Record metric
		config.RecordFallbackBlocked()

		// C-2: Record tool selection as TraceStep in CRS trace (low confidence path)
		if deps.Session != nil && deps.Session.HasCRS() {
			deps.Session.RecordTraceStep(crs.TraceStep{
				Timestamp: time.Now().UnixMilli(),
				Action:    "tool_selection",
				Target:    selection.Tool,
				Tool:      "router",
				Duration:  selection.Duration,
				Metadata: map[string]string{
					"source":           "low_confidence_fallback",
					"confidence":       fmt.Sprintf("%.2f", selection.Confidence),
					"fallback_blocked": "true",
					"duration_ms":      fmt.Sprintf("%d", selection.Duration.Milliseconds()),
				},
			})
		}
		// Continue with the selection despite low confidence
	}

	span.SetAttributes(
		attribute.String("selected_tool", selection.Tool),
		attribute.Float64("confidence", selection.Confidence),
		attribute.Int64("routing_duration_ms", selection.Duration.Milliseconds()),
	)

	// Circuit breaker: Check if the tool path is disproven or has been called too many times.
	// CRS-02: Prefer CRS proof status check when available, fall back to count-based check.
	// This prevents infinite loops where the model ignores tool history and keeps suggesting
	// the same tool. Fixed in cb_30a after trace_logs_18 showed repeated find_entry_points.
	var cbShouldFire bool
	var cbReason string

	// CRS-02: Use proof-based circuit breaker when CRS is available
	if deps.Session != nil && deps.Session.HasCRS() {
		cbResult := deps.Session.GetCRS().CheckCircuitBreaker(deps.Session.ID, selection.Tool)
		cbShouldFire = cbResult.ShouldFire
		cbReason = cbResult.Reason
		slog.Debug("CRS-02 circuit breaker check",
			slog.String("session_id", deps.Session.ID),
			slog.String("suggested_tool", selection.Tool),
			slog.Bool("should_fire", cbShouldFire),
			slog.String("reason", cbReason),
			slog.Uint64("proof_number", cbResult.ProofNumber),
			slog.String("status", cbResult.Status.String()),
		)
	} else if codeContext != nil && codeContext.ToolHistory != nil {
		// CB-31d: Fall back to count-based check when CRS not available
		callCount := countToolCalls(codeContext.ToolHistory, selection.Tool)
		slog.Debug("CB-31d circuit breaker check (legacy)",
			slog.String("session_id", deps.Session.ID),
			slog.String("suggested_tool", selection.Tool),
			slog.Int("call_count", callCount),
			slog.Int("max_allowed", maxRepeatedToolCalls),
			slog.Int("history_size", len(codeContext.ToolHistory)),
		)
		if callCount >= maxRepeatedToolCalls {
			cbShouldFire = true
			cbReason = fmt.Sprintf("%s already called %d times", selection.Tool, callCount)
		}
	}

	if cbShouldFire {
		slog.Info("Circuit breaker: forcing answer due to proof status",
			slog.String("session_id", deps.Session.ID),
			slog.String("suggested_tool", selection.Tool),
			slog.String("reason", cbReason),
		)
		span.SetAttributes(
			attribute.String("circuit_breaker", "proof_disproven"),
			attribute.String("blocked_tool", selection.Tool),
			attribute.String("cb_reason", cbReason),
		)

		// CRS-04: Learn from circuit breaker activation
		p.learnFromFailure(ctx, deps, crs.FailureEvent{
			SessionID:   deps.Session.ID,
			FailureType: crs.FailureTypeCircuitBreaker,
			Tool:        selection.Tool,
			Source:      crs.SignalSourceHard,
		})

		// CRS-06: Emit EventCircuitBreaker to Coordinator
		p.emitCoordinatorEvent(ctx, deps, integration.EventCircuitBreaker, nil, nil, cbReason, crs.ErrorCategoryInternal)

		// GR-44: Set circuit breaker active so execute phase doesn't require tools
		deps.Session.SetCircuitBreakerActive(true)

		// Force "answer" to synthesize a response from gathered information
		return &agent.ToolRouterSelection{
			Tool:       "answer",
			Confidence: 0.8,
			Reasoning:  fmt.Sprintf("Circuit breaker: %s. Synthesizing answer from gathered information.", cbReason),
			Duration:   selection.Duration,
		}, nil
	}

	// CB-30c: Semantic repetition check
	// Detects when the same tool is being called with semantically similar queries.
	// This catches patterns like Grep("parseConfig") → Grep("parse_config") that
	// the simple count-based circuit breaker misses.
	if selection.Tool != "answer" && deps.Session != nil && deps.Query != "" {
		isRepetitive, similarity, similarQuery := p.checkSemanticRepetition(ctx, deps, selection.Tool, deps.Query)

		if isRepetitive {
			srReason := fmt.Sprintf("semantic repetition: query %.0f%% similar to previous '%s'",
				similarity*100, truncateQuery(similarQuery, 30))

			slog.Info("CB-30c Semantic repetition: forcing answer",
				slog.String("session_id", deps.Session.ID),
				slog.String("tool", selection.Tool),
				slog.Float64("similarity", similarity),
				slog.String("similar_to", similarQuery),
			)

			span.SetAttributes(
				attribute.String("semantic_repetition", "detected"),
				attribute.Float64("similarity", similarity),
				attribute.String("similar_to", truncateQuery(similarQuery, 50)),
			)

			// Record metric
			grounding.RecordSemanticRepetition(selection.Tool, similarity, selection.Tool)

			// CRS-04: Learn from semantic repetition
			p.learnFromFailure(ctx, deps, crs.FailureEvent{
				SessionID:   deps.Session.ID,
				FailureType: crs.FailureTypeSemanticRepetition,
				Tool:        selection.Tool,
				Source:      crs.SignalSourceHard, // Jaccard is deterministic
			})

			// CRS-06: Emit EventSemanticRepetition to Coordinator
			p.emitCoordinatorEvent(ctx, deps, integration.EventSemanticRepetition, nil, nil, srReason, crs.ErrorCategoryInternal)

			// GR-44: Set circuit breaker active so execute phase doesn't require tools
			deps.Session.SetCircuitBreakerActive(true)

			// Force "answer" to synthesize
			return &agent.ToolRouterSelection{
				Tool:       "answer",
				Confidence: 0.8,
				Reasoning:  fmt.Sprintf("Semantic repetition: %s. Synthesizing answer from gathered information.", srReason),
				Duration:   selection.Duration,
			}, nil
		}
	}

	// CRS-05: UCB1-enhanced tool selection
	// This replaces the CRS-04 clause check with full UCB1 scoring that includes:
	// - Clause blocking via unit propagation
	// - Proof number penalties
	// - Exploration bonuses for less-used tools
	// - Cache for repeated state lookups
	if selection.Tool != "answer" && deps.Session != nil {
		ucb1Ctx := getUCB1Context(deps.Session.ID)
		availableTools := getAvailableToolNames(toolDefs)

		ucb1Selection, modified := p.selectToolWithUCB1(ctx, deps, ucb1Ctx, selection, availableTools)

		if modified {
			span.SetAttributes(
				attribute.String("ucb1_original_tool", selection.Tool),
				attribute.String("ucb1_selected_tool", ucb1Selection.Tool),
				attribute.Float64("ucb1_score", ucb1Selection.Confidence),
				attribute.Bool("ucb1_modified", true),
			)
		} else {
			span.SetAttributes(
				attribute.String("ucb1_selected_tool", ucb1Selection.Tool),
				attribute.Float64("ucb1_score", ucb1Selection.Confidence),
				attribute.Bool("ucb1_modified", false),
			)
		}

		// C-2: Record tool selection as TraceStep in CRS trace
		if deps.Session.HasCRS() {
			deps.Session.RecordTraceStep(crs.TraceStep{
				Timestamp: time.Now().UnixMilli(),
				Action:    "tool_selection",
				Target:    ucb1Selection.Tool,
				Tool:      "router",
				Duration:  selection.Duration,
				Metadata: map[string]string{
					"source":           "router",
					"confidence":       fmt.Sprintf("%.2f", ucb1Selection.Confidence),
					"original_tool":    selection.Tool,
					"ucb1_modified":    fmt.Sprintf("%t", modified),
					"fallback_blocked": "false",
					"duration_ms":      fmt.Sprintf("%d", selection.Duration.Milliseconds()),
				},
			})
		}

		// C-2: Record routing decision metric
		config.RecordRoutingDecision(ucb1Selection.Tool, "router")

		return ucb1Selection, nil
	}

	// C-2: Record tool selection as TraceStep in CRS trace (answer path)
	if deps.Session != nil && deps.Session.HasCRS() {
		deps.Session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "tool_selection",
			Target:    selection.Tool,
			Tool:      "router",
			Duration:  selection.Duration,
			Metadata: map[string]string{
				"source":           "router",
				"confidence":       fmt.Sprintf("%.2f", selection.Confidence),
				"fallback_blocked": "false",
				"duration_ms":      fmt.Sprintf("%d", selection.Duration.Milliseconds()),
			},
		})
	}

	// C-2: Record routing decision metric
	config.RecordRoutingDecision(selection.Tool, "router")

	return selection, nil
}

// toolDefsToSpecs converts tool.ToolDefinition slice to agent.ToolRouterSpec slice.
//
// Inputs:
//
//	defs - Tool definitions to convert.
//
// Outputs:
//
//	[]agent.ToolRouterSpec - Converted tool specs.
func toolDefsToSpecs(defs []tools.ToolDefinition) []agent.ToolRouterSpec {
	specs := make([]agent.ToolRouterSpec, len(defs))
	for i, def := range defs {
		// Extract parameter names from the Parameters map
		var params []string
		if def.Parameters != nil {
			params = make([]string, 0, len(def.Parameters))
			for name := range def.Parameters {
				params = append(params, name)
			}
		}

		// CB-31e: Convert InsteadOf to router substitution format
		var insteadOf []agent.ToolRouterSubstitution
		for _, sub := range def.WhenToUse.InsteadOf {
			insteadOf = append(insteadOf, agent.ToolRouterSubstitution{
				Tool: sub.Tool,
				When: sub.When,
			})
		}

		specs[i] = agent.ToolRouterSpec{
			Name:        def.Name,
			Description: def.Description,
			BestFor:     def.WhenToUse.Keywords,  // CB-31e: Now populated from WhenToUse
			UseWhen:     def.WhenToUse.UseWhen,   // CB-31e: New field
			AvoidWhen:   def.WhenToUse.AvoidWhen, // CB-31e: New field
			InsteadOf:   insteadOf,               // CB-31e: New field
			Params:      params,
			Category:    def.Category.String(),
		}
	}
	return specs
}

// maxRepeatedToolCalls is the circuit breaker threshold for repeated tool suggestions.
// If the router suggests a tool that has already been called this many times,
// we force selection of "answer" to synthesize from gathered information.
// This prevents infinite loops where the model ignores tool history.
// Fixed in cb_30a after trace_logs_18 showed 5+ repeated find_entry_points calls.
//
// NOTE: This must match crs.DefaultCircuitBreakerThreshold for consistent behavior.
const maxRepeatedToolCalls = crs.DefaultCircuitBreakerThreshold

// callLLM sends a request to the LLM and emits events.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	request - The LLM request.
//
// Outputs:
//
//	*llm.Response - The LLM response.
//	error - Non-nil if the request fails.
func (p *ExecutePhase) callLLM(ctx context.Context, deps *Dependencies, request *llm.Request) (*llm.Response, error) {
	// Emit LLM request event
	p.emitLLMRequest(deps, request)

	startTime := time.Now()

	// Call LLM
	response, err := deps.LLMClient.Complete(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("LLM request failed: %w", err)
	}

	// Emit LLM response event
	p.emitLLMResponse(deps, response)

	// Record LLM call as TraceStep for CRS debugging (prompt hierarchy enhancement)
	if deps.Session != nil {
		// Get last user message for the trace
		lastUserMsg := ""
		for i := len(request.Messages) - 1; i >= 0; i-- {
			if request.Messages[i].Role == "user" && request.Messages[i].Content != "" {
				lastUserMsg = request.Messages[i].Content
				if len(lastUserMsg) > 200 {
					lastUserMsg = lastUserMsg[:200] + "..."
				}
				break
			}
		}

		// Get content preview
		contentPreview := response.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}

		step := crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "llm_call",
			Target:    deps.LLMClient.Model(),
			Tool:      "llm",
			Duration:  time.Since(startTime),
			Metadata: map[string]string{
				"message_count":     fmt.Sprintf("%d", len(request.Messages)),
				"system_prompt_len": fmt.Sprintf("%d", len(request.SystemPrompt)),
				"tool_count":        fmt.Sprintf("%d", len(request.Tools)),
				"last_user_message": lastUserMsg,
				"output_tokens":     fmt.Sprintf("%d", response.OutputTokens),
				"input_tokens":      fmt.Sprintf("%d", response.InputTokens),
				"content_len":       fmt.Sprintf("%d", len(response.Content)),
				"content_preview":   contentPreview,
				"stop_reason":       response.StopReason,
				"tool_call_count":   fmt.Sprintf("%d", len(response.ToolCalls)),
				"provider":          deps.LLMClient.Name(),
			},
		}
		// Merge provider-level metadata from Response.TraceStep if present
		if response.TraceStep != nil {
			for k, v := range response.TraceStep.Metadata {
				if _, exists := step.Metadata[k]; !exists {
					step.Metadata[k] = v
				}
			}
		}
		deps.Session.RecordTraceStep(step)
	}

	return response, nil
}

// handleLLMError handles LLM request errors.
//
// Description:
//
//	Handles different LLM error types with appropriate recovery strategies.
//	For EmptyResponseError (context overflow), synthesizes a graceful response
//	from gathered tool results instead of failing. Fixed in cb_30a.
//
// Inputs:
//
//	deps - Phase dependencies.
//	err - The LLM error.
//
// Outputs:
//
//	agent.AgentState - ERROR for unrecoverable, COMPLETE if graceful recovery.
//	error - The original error or nil if recovered.
func (p *ExecutePhase) handleLLMError(deps *Dependencies, err error) (agent.AgentState, error) {
	// Check for EmptyResponseError - often caused by context overflow.
	// Instead of failing, synthesize a response from gathered tool results.
	// This provides a graceful degradation when the model is overwhelmed.
	// Fixed in cb_30a after trace_logs_18 showed 23 messages causing empty response.
	var emptyErr *llm.EmptyResponseError
	if errors.As(err, &emptyErr) {
		slog.Warn("Attempting graceful recovery from empty response",
			slog.String("session_id", deps.Session.ID),
			slog.Int("message_count", emptyErr.MessageCount),
			slog.Duration("duration", emptyErr.Duration),
		)

		if state, recovered := p.tryToolResultRecovery(deps, "empty_response"); recovered {
			return state, nil
		}

		// No tool results to synthesize from - fall through to error
		slog.Warn("Graceful recovery failed: no tool results to synthesize from",
			slog.String("session_id", deps.Session.ID),
		)
	}

	// IT-05 R6: Check for Ollama tool-parsing errors.
	// When the LLM (gpt-oss:20b) emits raw text that looks like a tool call during
	// synthesis (e.g., find_callers("canActivate")), Ollama's internal parser tries
	// to parse it as JSON and returns HTTP 500. This is not a real failure — the graph
	// tool already produced good results. Recover by passing through tool results.
	if isOllamaToolParsingError(err) {
		slog.Warn("IT-05 R6: Ollama tool-parsing error detected, attempting graceful recovery",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)

		if state, recovered := p.tryToolResultRecovery(deps, "ollama_tool_parse_error"); recovered {
			return state, nil
		}
	}

	p.emitError(deps, err, false)
	return agent.StateError, err
}

// isOllamaToolParsingError detects Ollama HTTP 500 errors caused by the LLM emitting
// raw text that Ollama's internal parser interprets as a malformed tool call.
//
// Example error: "ollama chat failed with status 500: {"error":"error parsing tool call:
// raw='find_callers(\"canActivate\")', err=invalid character 'i' ..."}"
//
// This happens when gpt-oss:20b generates function-call-like text during synthesis
// despite being told not to request tool calls. Ollama tries to parse the output
// as JSON tool calls and fails.
func isOllamaToolParsingError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "error parsing tool call") ||
		(strings.Contains(errStr, "status 500") && strings.Contains(errStr, "tool call"))
}

// tryToolResultRecovery attempts to recover from an LLM error by synthesizing
// a response from previously gathered tool results.
//
// Returns (state, true) if recovery succeeded, or (0, false) if no recovery possible.
func (p *ExecutePhase) tryToolResultRecovery(deps *Dependencies, reason string) (agent.AgentState, bool) {
	summary := p.synthesizeFromToolResults(deps)
	if summary == "" {
		return "", false
	}

	slog.Info("Graceful recovery: synthesized response from tool results",
		slog.String("session_id", deps.Session.ID),
		slog.String("reason", reason),
		slog.Int("summary_len", len(summary)),
	)

	// Add synthesized response to conversation history as assistant message
	if deps.Context != nil {
		deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
			Role:    "assistant",
			Content: summary,
		})
		deps.Session.SetCurrentContext(deps.Context)
	}

	toolResultCount := 0
	if deps.Context != nil {
		toolResultCount = len(deps.Context.ToolResults)
	}
	p.emitError(deps, fmt.Errorf("recovered from %s: synthesized from %d tool results", reason, toolResultCount), false)
	return agent.StateComplete, true
}

// handleCompletion handles LLM responses with no tool calls.
//
// Description:
//
//	Validates the response against grounding rules (anti-hallucination).
//	If critical violations are detected and retries are available, builds
//	a correction prompt and returns EXECUTE to retry. Implements circuit
//	breaker pattern to prevent infinite retry loops.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	response - The LLM response.
//	stepStart - When the step started.
//	stepNumber - The step number.
//
// Outputs:
//
//	agent.AgentState - COMPLETE if grounded, EXECUTE if retry needed.
//	error - Non-nil only for unrecoverable errors.
func (p *ExecutePhase) handleCompletion(ctx context.Context, deps *Dependencies, response *llm.Response, request *llm.Request, stepStart time.Time, stepNumber int) (agent.AgentState, error) {
	slog.Info("Handling completion",
		slog.String("session_id", deps.Session.ID),
		slog.Int("output_tokens", response.OutputTokens),
		slog.Int("response_len", len(response.Content)),
	)

	// Update session metrics with token usage
	if response.OutputTokens > 0 {
		deps.Session.IncrementMetric(agent.MetricTokens, response.OutputTokens)
	}
	deps.Session.IncrementMetric(agent.MetricLLMCalls, 1)

	// GR-39a Issue 1: Handle empty response by synthesizing from tool results
	// This fixes cases where the LLM returns no content after tool calls
	if strings.TrimSpace(response.Content) == "" {
		slog.Warn("GR-39a: Empty response content detected, attempting synthesis",
			slog.String("session_id", deps.Session.ID),
			slog.Int("output_tokens", response.OutputTokens),
		)

		synthesized := p.synthesizeFromToolResults(deps)
		if synthesized != "" {
			slog.Info("GR-39a: Synthesized response from tool results",
				slog.String("session_id", deps.Session.ID),
				slog.Int("synthesized_len", len(synthesized)),
			)
			response.Content = synthesized
		} else {
			// CB-31 Fix: Provide user-friendly fallback instead of empty response.
			// This ensures the user always receives meaningful feedback even when:
			// - LLM returns empty response AND
			// - ToolResults is empty AND
			// - TraceSteps fallback also fails
			slog.Warn("CB-31: No tool results available for synthesis, providing fallback response",
				slog.String("session_id", deps.Session.ID),
			)

			// Build a contextual fallback message based on what we know
			fallbackMsg := p.buildEmptyResponseFallback(deps)
			response.Content = fallbackMsg

			// Record this in trace for debugging
			if deps.Session != nil {
				deps.Session.RecordTraceStep(crs.TraceStep{
					Action: "synthesis_fallback",
					Tool:   "empty_response_handler",
					Metadata: map[string]string{
						"reason":       "no_tool_results",
						"fallback_len": fmt.Sprintf("%d", len(fallbackMsg)),
					},
				})
			}
		}
	}

	// GR-59 Group F: Detect LLM "surrender" responses ("I don't know") when tool results exist.
	// If the LLM gives up despite having graph tool results, retry once with a synthesis directive.
	if isSurrenderResponse(response.Content) && deps.Context != nil && len(deps.Context.ToolResults) > 0 {
		surrenderRetries := deps.Session.GetMetric(agent.MetricSurrenderRetries)
		if surrenderRetries < 1 {
			slog.Info("GR-59: Surrender detected with tool results available, retrying synthesis",
				slog.String("session_id", deps.Session.ID),
				slog.String("response_preview", truncateForLog(response.Content, 50)),
				slog.Int("tool_results", len(deps.Context.ToolResults)),
			)
			deps.Session.IncrementMetric(agent.MetricSurrenderRetries, 1)

			synthesized := p.synthesizeFromToolResults(deps)
			if synthesized != "" {
				response.Content = synthesized
			}
		}
	}

	// Validate response for prohibited patterns (hybrid stack layer 4)
	if p.responseValidator != nil {
		// GR-Phase1 Fix: Use the actual tool_choice from the request instead of
		// re-computing from classifier. This prevents false failures when:
		// 1. Router hard-forced a tool (executed directly, LLM gets no tool_choice)
		// 2. Circuit breaker fired (tool_choice=none is correct)
		// 3. Classifier was already called once in buildLLMRequest
		validation := p.responseValidator.Validate(response, request.ToolChoice)
		if !validation.Valid {
			forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)

			slog.Warn("Response validation failed",
				slog.String("session_id", deps.Session.ID),
				slog.String("reason", validation.Reason),
				slog.String("pattern", validation.MatchedPattern),
				slog.Bool("retryable", validation.Retryable),
				slog.Int("retry_count", forcingRetries),
			)

			// GR-44: Skip tool requirement when circuit breaker has forced synthesis mode.
			// This prevents the death spiral where CB fires but execute phase still demands tools.
			if deps.Session.IsCircuitBreakerActive() {
				slog.Debug("GR-44: Skipping tool requirement (circuit breaker active)",
					slog.String("session_id", deps.Session.ID),
					slog.String("validation_reason", validation.Reason),
				)
				// Proceed to completion - don't retry with stronger tool_choice
			} else if validation.Retryable && p.responseValidator.ShouldRetry(validation, forcingRetries) {
				// Check if we should retry with stronger tool_choice
				return p.retryWithStrongerToolChoice(ctx, deps, response, validation, stepNumber)
			}
		}
	}

	// Validate response quality (hedging language, citations) for analytical queries
	if p.qualityValidator != nil && response.Content != "" {
		// GR-Phase1 Fix: Determine if analytical from actual request tool_choice
		// instead of re-computing from classifier. A query is analytical if:
		// - tool_choice is "required" (specific tool) or "any" (must use a tool)
		// - NOT "none" (synthesis mode) or "auto" (LLM decides)
		isAnalytical := request.ToolChoice != nil &&
			request.ToolChoice.Type != "none" &&
			request.ToolChoice.Type != "auto"

		qualityResult := p.qualityValidator.ValidateQuality(response.Content, isAnalytical)
		if !qualityResult.Valid {
			forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)

			slog.Warn("Response quality validation failed",
				slog.String("session_id", deps.Session.ID),
				slog.String("reason", qualityResult.Reason),
				slog.String("pattern", qualityResult.MatchedPattern),
				slog.Bool("retryable", qualityResult.Retryable),
				slog.Int("retry_count", forcingRetries),
			)

			// Retry with quality correction if retryable and under limit
			if qualityResult.Retryable && forcingRetries < p.maxToolForcingRetries {
				return p.retryWithQualityCorrection(ctx, deps, response, qualityResult, stepNumber)
			}
		}
	}

	// Check if tool forcing should be applied (before grounding validation)
	// GR-44: Skip tool forcing when circuit breaker has fired
	// IT-11: Skip tool forcing when already in synthesis mode (ToolChoice=none).
	// forceLLMSynthesis delegates to handleCompletion after a successful synthesis,
	// but without this check, shouldForceToolUsage returns true (analytical query,
	// step ≤ 2, retries < max) and triggers a redundant second EXECUTE loop that
	// wastes ~9-18s on re-routing + a second LLM synthesis call.
	isSynthesisMode := request.ToolChoice != nil && request.ToolChoice.Type == "none"
	if !isSynthesisMode && !deps.Session.IsCircuitBreakerActive() && p.shouldForceToolUsage(ctx, deps, stepNumber) {
		return p.forceToolUsage(ctx, deps, response, stepNumber)
	}

	// Validate response against grounding (anti-hallucination)
	responseContent := response.Content
	var groundingResult *grounding.Result

	if deps.ResponseGrounder != nil && deps.Context != nil {
		var err error
		groundingResult, err = deps.ResponseGrounder.Validate(ctx, response.Content, deps.Context)
		if err != nil {
			slog.Warn("Grounding validation error",
				slog.String("session_id", deps.Session.ID),
				slog.String("error", err.Error()),
			)
			// Continue with unvalidated response on error
		}
	}

	// Handle grounding result
	if groundingResult != nil {
		slog.Info("Grounding validation complete",
			slog.String("session_id", deps.Session.ID),
			slog.Bool("grounded", groundingResult.Grounded),
			slog.Float64("confidence", groundingResult.Confidence),
			slog.Int("critical_count", groundingResult.CriticalCount),
			slog.Int("warning_count", groundingResult.WarningCount),
		)

		// Log violations
		for _, v := range groundingResult.Violations {
			slog.Warn("Grounding violation",
				slog.String("session_id", deps.Session.ID),
				slog.String("type", string(v.Type)),
				slog.String("severity", string(v.Severity)),
				slog.String("code", v.Code),
				slog.String("message", v.Message),
			)
		}

		// Check if we should reject and retry
		if deps.ResponseGrounder.ShouldReject(groundingResult) {
			retryCount := deps.Session.GetMetric(agent.MetricGroundingRetries)

			if retryCount < p.maxGroundingRetries {
				// Build correction prompt and retry
				correctionPrompt := p.buildCorrectionPrompt(groundingResult)

				slog.Info("Grounding rejection - requesting retry",
					slog.String("session_id", deps.Session.ID),
					slog.Int("retry_count", retryCount+1),
					slog.Int("max_retries", p.maxGroundingRetries),
				)

				// Add correction as user message to trigger re-generation
				if deps.ContextManager != nil {
					deps.ContextManager.AddMessage(deps.Context, "user", correctionPrompt)
				} else if deps.Context != nil {
					deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
						Role:    "user",
						Content: correctionPrompt,
					})
				}

				deps.Session.IncrementMetric(agent.MetricGroundingRetries, 1)

				// Return to EXECUTE to get a new response
				return agent.StateExecute, nil
			}

			// Circuit breaker triggered - log and continue with best effort
			slog.Error("Grounding circuit breaker triggered - accepting ungrounded response",
				slog.String("session_id", deps.Session.ID),
				slog.Int("retry_count", retryCount),
				slog.Int("critical_violations", groundingResult.CriticalCount),
			)

			// Add warning footnote about potential issues
			responseContent = response.Content + "\n\n---\n⚠️ **Warning**: This response may contain inaccuracies. Please verify code references."
		} else {
			// Response is grounded or only has warnings - add footnote if needed
			footnote := deps.ResponseGrounder.GenerateFootnote(groundingResult)
			if footnote != "" {
				responseContent = response.Content + footnote
			}
		}
	}

	// Add response to context conversation history
	if deps.ContextManager != nil {
		deps.ContextManager.AddMessage(deps.Context, "assistant", responseContent)
		deps.Session.SetCurrentContext(deps.Context)
	} else if deps.Context != nil {
		deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
			Role:    "assistant",
			Content: responseContent,
		})
		deps.Session.SetCurrentContext(deps.Context)
	}

	// Emit step complete
	p.emitStepComplete(deps, stepStart, stepNumber, 0)

	// Record completion trace step
	if deps.Session != nil {
		completionStep := crs.TraceStep{
			Action:   "complete",
			Target:   "response",
			Duration: time.Since(stepStart),
			Metadata: map[string]string{
				"step_number":   fmt.Sprintf("%d", stepNumber),
				"output_tokens": fmt.Sprintf("%d", response.OutputTokens),
				"response_len":  fmt.Sprintf("%d", len(responseContent)),
			},
		}
		if groundingResult != nil {
			completionStep.Metadata["grounded"] = fmt.Sprintf("%v", groundingResult.Grounded)
			completionStep.Metadata["confidence"] = fmt.Sprintf("%.2f", groundingResult.Confidence)
		}
		deps.Session.RecordTraceStep(completionStep)
	}

	// Transition to complete
	p.emitStateTransition(deps, agent.StateExecute, agent.StateComplete, "task completed")

	slog.Info("ExecutePhase completed successfully",
		slog.String("session_id", deps.Session.ID),
	)

	return agent.StateComplete, nil
}

// buildCorrectionPrompt creates a prompt to correct grounding violations.
func (p *ExecutePhase) buildCorrectionPrompt(result *grounding.Result) string {
	var issues []string
	for _, v := range result.Violations {
		if v.Severity == grounding.SeverityCritical {
			issues = append(issues, v.Message)
		}
	}

	prompt := "Your previous response had grounding issues that need correction:\n\n"
	for i, issue := range issues {
		prompt += fmt.Sprintf("%d. %s\n", i+1, issue)
	}
	prompt += "\nPlease provide a corrected response that:\n"
	prompt += "- Only discusses code that appears in the provided context\n"
	prompt += "- Uses [file:line] citations for specific code references\n"
	prompt += "- Matches the project's programming language\n"
	prompt += "- Says \"I don't see X in the context\" if something is not present\n"

	return prompt
}

// shouldForceToolUsage determines if tool usage should be forced.
//
// Description:
//
//	Checks the tool forcing policy to determine if the LLM should be
//	prompted to use tools instead of returning a text-only response.
//
// Inputs:
//
//	ctx - Context for tracing.
//	deps - Phase dependencies.
//	stepNumber - Current step number.
//
// Outputs:
//
//	bool - True if tool usage should be forced.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) shouldForceToolUsage(ctx context.Context, deps *Dependencies, stepNumber int) bool {
	// Skip if no forcing policy configured
	if p.forcingPolicy == nil {
		return false
	}

	// Skip if deps is nil (shouldn't happen after validation)
	if deps == nil || deps.Session == nil {
		return false
	}

	// Get current retry count
	forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)

	// Build available tools list
	var availableTools []string
	if deps.ToolRegistry != nil {
		defs := deps.ToolRegistry.GetDefinitions()
		availableTools = make([]string, len(defs))
		for i, def := range defs {
			availableTools[i] = def.Name
		}
	}

	// Build forcing request
	req := &ForcingRequest{
		Query:             deps.Query,
		StepNumber:        stepNumber,
		ForcingRetries:    forcingRetries,
		MaxRetries:        p.maxToolForcingRetries,
		MaxStepForForcing: p.maxStepForForcing,
		AvailableTools:    availableTools,
	}

	return p.forcingPolicy.ShouldForce(ctx, req)
}

// forceToolUsage injects a hint and returns to EXECUTE state for retry.
//
// Description:
//
//	When tool forcing is triggered, this method:
//	1. Builds a forcing hint with tool suggestion
//	2. Emits a tool forcing event
//	3. Adds the hint to conversation history
//	4. Increments the forcing retry metric
//	5. Returns StateExecute to retry
//
// Inputs:
//
//	ctx - Context for tracing.
//	deps - Phase dependencies.
//	response - The LLM response that triggered forcing.
//	stepNumber - Current step number.
//
// Outputs:
//
//	agent.AgentState - StateExecute to retry.
//	error - Non-nil only for unrecoverable errors.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) forceToolUsage(ctx context.Context, deps *Dependencies, response *llm.Response, stepNumber int) (agent.AgentState, error) {
	forcingRetries := deps.Session.GetMetric(agent.MetricToolForcingRetries)

	slog.Info("Forcing tool usage",
		slog.String("session_id", deps.Session.ID),
		slog.String("query", deps.Query),
		slog.Int("step", stepNumber),
		slog.Int("retry", forcingRetries+1),
	)

	// Build available tools list
	var availableTools []string
	if deps.ToolRegistry != nil {
		defs := deps.ToolRegistry.GetDefinitions()
		availableTools = make([]string, len(defs))
		for i, def := range defs {
			availableTools[i] = def.Name
		}
	}

	// Build forcing request and hint
	req := &ForcingRequest{
		Query:             deps.Query,
		StepNumber:        stepNumber,
		ForcingRetries:    forcingRetries,
		MaxRetries:        p.maxToolForcingRetries,
		MaxStepForForcing: p.maxStepForForcing,
		AvailableTools:    availableTools,
	}

	hint := p.forcingPolicy.BuildHint(ctx, req)

	// Emit tool forcing event
	p.emitToolForcing(deps, req, hint, stepNumber)

	// Add hint to conversation via ContextManager (thread-safe)
	if deps.ContextManager != nil {
		deps.ContextManager.AddMessage(deps.Context, "user", hint)
	} else if deps.Context != nil {
		// Fallback: direct append (less safe but functional)
		deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
			Role:    "user",
			Content: hint,
		})
	}

	// Increment forcing retry metric
	deps.Session.IncrementMetric(agent.MetricToolForcingRetries, 1)

	// Return to EXECUTE to retry with the hint
	return agent.StateExecute, nil
}

// buildEmptyResponseFallback constructs a user-friendly fallback message when
// both the LLM and synthesis fail to produce content.
//
// Description:
//
//	CB-31 Fix: Instead of returning an empty response to the user, this function
//	builds a contextual message explaining what happened. It examines the session
//	state to provide specific feedback about what was attempted.
//
// Inputs:
//
//	deps - Phase dependencies containing session and context.
//
// Outputs:
//
//	string - A user-friendly fallback message, never empty.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ExecutePhase) buildEmptyResponseFallback(deps *Dependencies) string {
	var sb strings.Builder

	// Start with an apologetic but informative message
	sb.WriteString("I was unable to generate a complete response for your query.\n\n")

	// Add context about what was attempted
	toolCount := 0
	stepCount := 0
	if deps.Session != nil {
		steps := deps.Session.GetTraceSteps()
		stepCount = len(steps)
		for _, step := range steps {
			if step.Action == "tool_call" || step.Action == "tool_call_forced" {
				toolCount++
			}
		}
	}

	if toolCount > 0 {
		sb.WriteString(fmt.Sprintf("**Exploration Summary**: I executed %d tool calls across %d steps, ", toolCount, stepCount))
		sb.WriteString("but was unable to synthesize a coherent answer from the results.\n\n")
		sb.WriteString("This may indicate:\n")
		sb.WriteString("- The relevant code wasn't found in the searched locations\n")
		sb.WriteString("- The query requires information not available in the codebase\n")
		sb.WriteString("- A technical issue with processing the tool outputs\n\n")
	} else {
		sb.WriteString("No codebase exploration was completed before this response was generated.\n\n")
		sb.WriteString("This may indicate:\n")
		sb.WriteString("- The query couldn't be matched to appropriate tools\n")
		sb.WriteString("- A context or configuration issue\n\n")
	}

	// Provide actionable suggestions
	sb.WriteString("**Suggestions**:\n")
	sb.WriteString("1. Try rephrasing your question with more specific details\n")
	sb.WriteString("2. Mention specific file names, function names, or code patterns\n")
	sb.WriteString("3. Break complex queries into smaller, focused questions\n")

	// Add the query for reference if available
	if deps.Query != "" && len(deps.Query) < 200 {
		sb.WriteString(fmt.Sprintf("\n**Original Query**: %s\n", deps.Query))
	}

	return sb.String()
}

// =============================================================================
// GR-41b: Graph Tool Synthesis Enhancement
// =============================================================================

// graphToolsWithSubstantiveResults are the graph tools that, when they return
// results, indicate we have enough information to synthesize an answer.
// IT-04 Audit: ALL graph tools must be in this map. Every graph tool analyzes the
// complete code graph (every source file) and returns authoritative, exhaustive results.
// The LLM should NEVER be allowed to spiral into Grep/Glob to "verify" graph results.
//
// Previously only 7 of 23 graph tools were listed, causing the LLM to spiral after
// the other 16. Now all 23 are listed.
var graphToolsWithSubstantiveResults = map[string]bool{
	// Core graph traversal
	"find_callers":         true,
	"find_callees":         true,
	"find_implementations": true,
	"find_references":      true,
	"find_symbol":          true,
	"find_path":            true,
	"get_call_chain":       true,
	// Graph analytics
	"find_hotspots":             true,
	"find_important":            true,
	"find_weighted_criticality": true,
	"find_dead_code":            true,
	"find_cycles":               true,
	"find_loops":                true,
	"find_communities":          true,
	"find_articulation_points":  true,
	"find_merge_points":         true,
	"find_dominators":           true,
	"find_control_dependencies": true,
	"find_critical_path":        true,
	"find_common_dependency":    true,
	"find_extractable_regions":  true,
	"find_module_api":           true,
	"check_reducibility":        true,
	"find_similar_code":         true, // IT-06c I-11: Was missing, preventing forced synthesis
}

// shouldForceSynthesisAfterGraphTools determines if we should force synthesis
// instead of letting the LLM call more tools.
//
// Description:
//
//	This prevents the "Grep 10 times" problem where the agent keeps searching
//	after already getting good results from graph tools. If a graph tool like
//	find_callees returned substantive results, we should synthesize instead of
//	continuing to search.
//
// Inputs:
//
//	deps - Phase dependencies.
//	toolResults - Results from the most recent tool execution.
//	stepNumber - Current step number in execution.
//
// Outputs:
//
//	bool - True if synthesis should be forced.
//
// Thread Safety: Safe for concurrent use (read-only operations).
func (p *ExecutePhase) shouldForceSynthesisAfterGraphTools(deps *Dependencies, toolResults []*tools.Result, stepNumber int) bool {
	// Only consider forcing synthesis after at least 1 step
	// Step 0 is the initial tool execution — give it a chance to explore.
	// After step 0, if a graph tool returned substantive results (including
	// forced tool calls at step 1), force synthesis to avoid circuit breaker.
	if stepNumber < 1 {
		return false
	}

	// GR-59 Rev 4: Direct session flag from forced tool execution.
	// Set by executeToolDirectlyWithFallback when a graph tool returns
	// substantive results. No metadata round-trip — most reliable signal.
	if deps.Session != nil && deps.Session.GraphToolHadSubstantiveResults() {
		slog.Info("GR-59 Rev 4: Forcing synthesis — forced graph tool had substantive results",
			slog.String("session_id", deps.Session.ID),
			slog.Int("step_number", stepNumber),
		)
		return true
	}

	// Check if any of the executed tools in the CURRENT batch were graph tools with results
	for _, result := range toolResults {
		if result == nil || result.Error != "" {
			continue
		}

		// Get the tool name from the invocation if available
		toolName := getToolNameFromResult(result)

		// Check if this is a graph tool that returns substantive results
		if graphToolsWithSubstantiveResults[toolName] {
			// Check if the result contains actual data (not "No X found" messages)
			if hasSubstantiveGraphResult(result.OutputText) {
				slog.Debug("GR-41b: Graph tool returned substantive results (current batch)",
					slog.String("tool", toolName),
					slog.Int("content_len", len(result.OutputText)),
				)
				return true
			}
		}
	}

	// GR-59 Rev 2: Check session history for PREVIOUS graph tool results.
	// The current batch may contain only Grep/Read (non-graph tools) while
	// a graph tool returned substantive results in a prior step. Without this
	// check, the LLM keeps calling Grep after find_implementations already answered.
	if deps.Session != nil && p.sessionHasPriorGraphToolResults(deps) {
		slog.Info("GR-59 Rev 2: Forcing synthesis — prior graph tool had substantive results",
			slog.String("session_id", deps.Session.ID),
			slog.Int("step_number", stepNumber),
		)
		return true
	}

	return false
}

// sessionHasPriorGraphToolResults checks session trace step history for any
// previous graph tool execution that returned substantive results.
//
// Description:
//
//	This addresses the blind spot where shouldForceSynthesisAfterGraphTools only
//	checked the current batch's tool results. When a graph tool (e.g., find_implementations)
//	returned results at step 0 but the LLM called Grep at step 1, the current batch
//	contained only Grep results — missing the graph tool's substantive output entirely.
//
// Inputs:
//
//	deps - Phase dependencies with session access.
//
// Outputs:
//
//	bool - True if any prior graph tool returned non-zero results.
//
// Thread Safety: Safe for concurrent use (read-only).
func (p *ExecutePhase) sessionHasPriorGraphToolResults(deps *Dependencies) bool {
	traceSteps := deps.Session.GetTraceSteps()
	// Result count metadata keys used by graph tools to indicate substantive output.
	resultCountKeys := []string{
		"match_count",           // find_callers, find_implementations
		"total_callers",         // find_callers
		"total_implementations", // find_implementations
		"resolved_count",        // find_callees
		"total_count",           // find_callees
		"reference_count",       // find_references
		"chain_length",          // get_call_chain
	}

	for _, step := range traceSteps {
		if !graphToolsWithSubstantiveResults[step.Tool] || step.Error != "" {
			continue
		}
		for _, key := range resultCountKeys {
			if val, ok := step.Metadata[key]; ok && val != "0" && val != "" {
				slog.Debug("GR-59 Rev 2: Found prior graph tool with substantive results",
					slog.String("tool", step.Tool),
					slog.String("key", key),
					slog.String("value", val),
				)
				return true
			}
		}
	}
	return false
}

// getToolNameFromResult extracts the tool name from a result.
// The tool name is typically embedded in the output or needs to be tracked separately.
func getToolNameFromResult(result *tools.Result) string {
	if result == nil {
		return ""
	}

	// Check if Output has a Name field (depends on tool implementation)
	if m, ok := result.Output.(map[string]any); ok {
		if name, ok := m["tool"].(string); ok {
			return name
		}
	}

	// For graph tools, check OutputText for patterns
	outputLower := strings.ToLower(result.OutputText)
	if strings.Contains(outputLower, "callers found") || strings.Contains(outputLower, "called by") {
		return "find_callers"
	}
	if strings.Contains(outputLower, "callees found") || strings.Contains(outputLower, "calls to") || strings.Contains(outputLower, "functions called") {
		return "find_callees"
	}
	if strings.Contains(outputLower, "references found") || strings.Contains(outputLower, "referenced by") {
		return "find_references"
	}
	if strings.Contains(outputLower, "implementations found") || strings.Contains(outputLower, "implements") || strings.Contains(outputLower, "classes that implement") || strings.Contains(outputLower, "classes extend") {
		return "find_implementations"
	}
	if strings.Contains(outputLower, "call chain") {
		return "get_call_chain"
	}

	return ""
}

// hasSubstantiveGraphResult checks if a graph tool result contains actual data
// rather than "No results found" type messages.
func hasSubstantiveGraphResult(content string) bool {
	if content == "" {
		return false
	}

	// Check for common "no results" patterns
	// GR-59 Rev 4 root cause fix: All graph tools now include "not found" in their
	// zero-result headers (e.g., "Symbol 'X' not found", "Callers of 'X' not found",
	// "Callees of 'X' not found", "Implementations of 'X' not found"). The universal
	// "not found" pattern catches all of them without tool-specific workarounds.
	noResultPatterns := []string{
		"not found",
		"No references found",
		"No results",
	}

	contentLower := strings.ToLower(content)
	for _, pattern := range noResultPatterns {
		if strings.Contains(contentLower, strings.ToLower(pattern)) {
			return false
		}
	}

	// If content is substantial (more than just a short error/empty message)
	// consider it as having results
	return len(content) > 50
}

// synthesizeFromGraphToolResults creates a synthesis from graph tool results.
//
// Description:
//
//	Called when we want to force synthesis from tool results without going
//	back to the LLM for more tool calls. This is used by GR-41b to synthesize
//	after graph tools return good results.
//
// Inputs:
//
//	deps - Phase dependencies.
//	toolResults - Tool results to synthesize from.
//	reason - Why synthesis was triggered (for logging).
//
// Outputs:
//
//	string - Synthesized response, or empty if synthesis failed.
//
// Thread Safety: Safe for concurrent use.
func (p *ExecutePhase) synthesizeFromGraphToolResults(deps *Dependencies, toolResults []*tools.Result, reason string) string {
	if len(toolResults) == 0 {
		return ""
	}

	// Build a summary of the tool results
	var sb strings.Builder
	sb.WriteString("Based on my analysis of the codebase:\n\n")

	for _, result := range toolResults {
		if result == nil || result.Error != "" {
			continue
		}

		// Get the tool name
		toolName := getToolNameFromResult(result)
		content := result.OutputText

		// Format based on tool type
		switch toolName {
		case "find_callees":
			sb.WriteString("**Functions called:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		case "find_callers":
			sb.WriteString("**Called by:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		case "find_references":
			sb.WriteString("**References found:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		case "find_implementations":
			sb.WriteString("**Implementations found:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		case "get_call_chain":
			sb.WriteString("**Call chain:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		case "Read", "read_file", "read_symbol":
			// GR-Phase1 Fix: Skip raw file content in synthesis.
			// Read tool outputs are too verbose and unstructured for synthesis.
			// They often contain comments, build instructions, etc. that aren't
			// relevant to the query. Graph tools provide structured data that's
			// better for synthesis.
			slog.Debug("GR-Phase1: Skipping Read tool output in synthesis",
				slog.Int("content_len", len(content)),
			)
			continue

		case "find_entry_points":
			sb.WriteString("**Entry points found:**\n")
			sb.WriteString(content)
			sb.WriteString("\n\n")

		default:
			// For other graph tools, include the content if substantive
			// but skip generic file operations
			if len(content) > 50 && !strings.Contains(toolName, "read") && !strings.Contains(toolName, "grep") {
				sb.WriteString(fmt.Sprintf("**%s result:**\n", toolName))
				// Truncate very long results
				if len(content) > 2000 {
					content = content[:2000] + "\n... (truncated)"
				}
				sb.WriteString(content)
				sb.WriteString("\n\n")
			}
		}
	}

	synthesized := sb.String()
	if len(synthesized) > 50 {
		slog.Info("GR-41b: Synthesized from graph tool results",
			slog.String("session_id", deps.Session.ID),
			slog.String("reason", reason),
			slog.Int("result_len", len(synthesized)),
		)

		// Record trace step
		if deps.Session != nil {
			deps.Session.RecordTraceStep(crs.TraceStep{
				Timestamp: time.Now().UnixMilli(),
				Action:    "forced_synthesis",
				Target:    reason,
				Metadata: map[string]string{
					"reason":       reason,
					"result_count": fmt.Sprintf("%d", len(toolResults)),
					"output_len":   fmt.Sprintf("%d", len(synthesized)),
				},
			})
		}

		return synthesized
	}

	return ""
}

// forceLLMSynthesis forces the LLM to produce a final answer from conversation history.
//
// Description:
//
//	GR-59 Rev 2: When a graph tool returned substantive results in a prior step
//	but the current batch contains only CB errors or non-graph tool results,
//	force the LLM to synthesize an answer. The conversation history already
//	contains the graph tool output, so the LLM has everything it needs.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	request - The original LLM request (used for signature compatibility).
//	stepStart - When this step began (for duration tracking).
//	stepNumber - Current step number.
//
// Outputs:
//
//	agent.AgentState - The next state.
//	error - Non-nil on failure.
//
// Thread Safety: Safe for concurrent use.
func (p *ExecutePhase) forceLLMSynthesis(
	ctx context.Context,
	deps *Dependencies,
	request *llm.Request,
	stepStart time.Time,
	stepNumber int,
) (agent.AgentState, error) {
	slog.Info("GR-59 Rev 2: Forcing LLM synthesis from conversation history",
		slog.String("session_id", deps.Session.ID),
		slog.Int("step_number", stepNumber),
	)

	// Phase 20: Check if tool results already contain a single authoritative graph result.
	// If so, return it directly — calling the LLM risks hallucination that contradicts
	// the tool's definitive answer (e.g., "Symbol not found" → LLM fabricates details).
	if deps.Context != nil && len(deps.Context.ToolResults) > 0 {
		if singleResult, ok := getSingleFormattedResult(deps.Context.ToolResults); ok {
			slog.Info("GR-59 Rev 2: Pass-through — authoritative graph result found, skipping LLM synthesis",
				slog.String("session_id", deps.Session.ID),
			)
			// Record trace step for observability
			if deps.Session != nil {
				deps.Session.RecordTraceStep(crs.TraceStep{
					Timestamp: time.Now().UnixMilli(),
					Action:    "synthesis",
					Tool:      "tool_results",
					Metadata:  map[string]string{"reason": "graph_passthrough"},
				})
			}
			// Create a synthetic response with the pass-through content and delegate
			// to handleCompletion for proper session state updates.
			passthroughResponse := &llm.Response{
				Content: singleResult,
			}
			return p.handleCompletion(ctx, deps, passthroughResponse, request, stepStart, stepNumber)
		}
	}

	// Build request without tools — force text-only response.
	synthRequest := llm.BuildRequest(deps.Context, nil, p.maxTokens)

	// IT-11: Strip code context and system prompt from synthesis request.
	// BuildRequest includes the full RAG code context (8-25KB) and system prompt
	// as messages. These are unnecessary for synthesis — the LLM only needs
	// the user query + tool results + synthesis instruction. Sending the full
	// context causes glm-4.7-flash to return empty responses non-deterministically
	// on large codebases (BabylonJS, NestJS, Plottable).
	stripped := make([]llm.Message, 0, len(synthRequest.Messages))
	for _, msg := range synthRequest.Messages {
		// Skip the code context message (injected by BuildRequest from CodeContext)
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "Here is relevant code from the codebase:") {
			continue
		}
		stripped = append(stripped, msg)
	}
	synthRequest.Messages = stripped
	synthRequest.SystemPrompt = "" // System prompt not needed for synthesis

	totalInputLen := 0
	for _, msg := range synthRequest.Messages {
		totalInputLen += len(msg.Content)
		for _, tr := range msg.ToolResults {
			totalInputLen += len(tr.Content)
		}
	}
	slog.Info("IT-11: Synthesis request after stripping code context",
		slog.String("session_id", deps.Session.ID),
		slog.Int("message_count", len(synthRequest.Messages)),
		slog.Int("total_input_bytes", totalInputLen),
	)

	// IT-3/10 FIX-3a: Anti-hallucination synthesis prompt. Addresses ISSUE-03, 09, 10, 12, 14, 17F.
	synthesisPrompt := `You have gathered information from tools. Now provide a complete answer.

MANDATORY: YOU MUST RESPOND WITH A COMPLETE ANSWER based on the tool results in the conversation above.
Graph tools have already provided exhaustive results — do NOT request more tool calls.
Summarize the findings clearly and completely.

CRITICAL RULES FOR SYNTHESIS — VIOLATING THESE PRODUCES HARMFUL OUTPUT:

1. ONLY use data returned by tools. Do NOT use your training knowledge about this codebase,
   library, or framework. The graph is the ONLY authoritative source. If the tool returned
   no results or "not found", report that directly. A response of "X was not found in the
   codebase graph" is CORRECT — do not fill in what you think the answer should be.

2. Do NOT fabricate file paths, line numbers, function names, class names, or call chains
   that were not in the tool output. Do not invent intermediate nodes in paths. Do not
   add usage examples or references that were not returned by the tool.

3. If tool output was TRUNCATED (e.g., "showing 10 of 20 communities"), state that
   additional results exist but were not returned. Do NOT invent content for the
   missing entries.

4. When reporting IDs or labels from tool output (community numbers, rank positions),
   quote them EXACTLY as returned. Do not renumber or reinterpret.

5. If the tool returned data for a DIFFERENT question than what was asked (e.g., callee
   data when cycles were requested), say "The tool returned [what it returned], which
   does not directly answer the question about [what was asked]." Do NOT reinterpret
   callee data as cycle analysis.`

	// Append synthesis instruction as the last user message.
	synthRequest.Messages = append(synthRequest.Messages, llm.Message{
		Role:    "user",
		Content: synthesisPrompt,
	})
	synthRequest.ToolChoice = llm.ToolChoiceNone()
	synthRequest.Tools = nil

	response, err := p.callLLM(ctx, deps, synthRequest)
	if err != nil {
		slog.Error("GR-59 Rev 2: Synthesis LLM call failed",
			slog.String("session_id", deps.Session.ID),
			slog.String("error", err.Error()),
		)
		return p.handleLLMError(deps, err)
	}

	// Record trace step
	if deps.Session != nil {
		deps.Session.RecordTraceStep(crs.TraceStep{
			Timestamp: time.Now().UnixMilli(),
			Action:    "forced_synthesis",
			Target:    "gr59_rev2_session_history",
			Metadata: map[string]string{
				"reason":        "prior_graph_tool_results",
				"step_number":   fmt.Sprintf("%d", stepNumber),
				"output_tokens": fmt.Sprintf("%d", response.OutputTokens),
			},
		})
	}

	return p.handleCompletion(ctx, deps, response, synthRequest, stepStart, stepNumber)
}
