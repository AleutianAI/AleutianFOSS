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
	"fmt"
	"log/slog"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/events"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/grounding"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

// ReflectPhase handles progress evaluation and decision making.
//
// This phase is responsible for:
//   - Evaluating progress toward the goal
//   - Deciding whether to continue, complete, or request clarification
//   - Summarizing recent actions for context management
//
// Thread Safety: ReflectPhase is safe for concurrent use.
type ReflectPhase struct {
	// maxSteps is the maximum steps before forcing completion.
	maxSteps int

	// maxTokens is the maximum tokens before forcing completion.
	maxTokens int
}

// ReflectPhaseOption configures a ReflectPhase.
type ReflectPhaseOption func(*ReflectPhase)

// WithMaxSteps sets the maximum allowed steps.
//
// Inputs:
//
//	steps - Maximum step count.
//
// Outputs:
//
//	ReflectPhaseOption - The configuration function.
func WithMaxSteps(steps int) ReflectPhaseOption {
	return func(p *ReflectPhase) {
		p.maxSteps = steps
	}
}

// WithMaxTotalTokens sets the maximum total tokens.
//
// Inputs:
//
//	tokens - Maximum token count.
//
// Outputs:
//
//	ReflectPhaseOption - The configuration function.
func WithMaxTotalTokens(tokens int) ReflectPhaseOption {
	return func(p *ReflectPhase) {
		p.maxTokens = tokens
	}
}

// NewReflectPhase creates a new reflection phase.
//
// Inputs:
//
//	opts - Configuration options.
//
// Outputs:
//
//	*ReflectPhase - The configured phase.
func NewReflectPhase(opts ...ReflectPhaseOption) *ReflectPhase {
	p := &ReflectPhase{
		maxSteps:  50,
		maxTokens: 100000,
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
//	string - "reflect"
func (p *ReflectPhase) Name() string {
	return "reflect"
}

// Execute implements Phase.
//
// Description:
//
//	Evaluates the current progress and decides on the next action:
//	- EXECUTE: Continue with more steps
//	- COMPLETE: Task is done
//	- CLARIFY: Need user input
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	deps - Phase dependencies.
//
// Outputs:
//
//	agent.AgentState - Next state (EXECUTE, COMPLETE, or CLARIFY).
//	error - Non-nil only for unrecoverable errors.
//
// Thread Safety: This method is safe for concurrent use.
func (p *ReflectPhase) Execute(ctx context.Context, deps *Dependencies) (agent.AgentState, error) {
	if err := p.validateDependencies(deps); err != nil {
		return agent.StateError, err
	}

	// Gather reflection input
	input := p.gatherReflectionInput(deps)

	// Check hard limits first
	if p.exceedsLimits(input) {
		return p.handleLimitExceeded(deps, input)
	}

	// Perform reflection analysis
	output := p.analyzeProgress(deps, input)

	// Emit reflection event
	p.emitReflection(deps, input, output)

	// Handle the decision
	return p.handleDecision(deps, output)
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
func (p *ReflectPhase) validateDependencies(deps *Dependencies) error {
	if deps == nil {
		return fmt.Errorf("dependencies are nil")
	}
	if deps.Session == nil {
		return fmt.Errorf("session is nil")
	}
	if deps.Context == nil {
		return fmt.Errorf("context is nil")
	}
	return nil
}

// gatherReflectionInput collects data for reflection analysis.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	*ReflectionInput - The gathered input data.
func (p *ReflectPhase) gatherReflectionInput(deps *Dependencies) *ReflectionInput {
	metrics := deps.Session.GetMetrics()

	input := &ReflectionInput{
		StepsCompleted: metrics.TotalSteps,
		TokensUsed:     deps.Context.TotalTokens,
		ToolsInvoked:   metrics.ToolCalls,
		RecentResults:  p.getRecentResults(deps),
	}

	// Get last response from conversation history
	if len(deps.Context.ConversationHistory) > 0 {
		lastMsg := deps.Context.ConversationHistory[len(deps.Context.ConversationHistory)-1]
		if lastMsg.Role == "assistant" {
			input.LastResponse = lastMsg.Content
		}
	}

	return input
}

// getRecentResults returns the most recent tool results.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	[]agent.ToolResult - Recent tool results (max 5).
func (p *ReflectPhase) getRecentResults(deps *Dependencies) []agent.ToolResult {
	results := deps.Context.ToolResults
	if len(results) > 5 {
		return results[len(results)-5:]
	}
	return results
}

// exceedsLimits checks if hard limits have been exceeded.
//
// Inputs:
//
//	input - The reflection input.
//
// Outputs:
//
//	bool - True if limits are exceeded.
func (p *ReflectPhase) exceedsLimits(input *ReflectionInput) bool {
	if input.StepsCompleted >= p.maxSteps {
		return true
	}
	if input.TokensUsed >= p.maxTokens {
		return true
	}
	return false
}

// handleLimitExceeded handles the case when limits are exceeded.
//
// Inputs:
//
//	deps - Phase dependencies.
//	input - The reflection input.
//
// Outputs:
//
//	agent.AgentState - COMPLETE.
//	error - Always nil.
func (p *ReflectPhase) handleLimitExceeded(deps *Dependencies, input *ReflectionInput) (agent.AgentState, error) {
	var reason string
	if input.StepsCompleted >= p.maxSteps {
		reason = "maximum steps reached"
	} else {
		reason = "maximum tokens reached"
	}

	// Synthesize a final response before completing
	p.synthesizeResponse(context.Background(), deps, reason)

	p.emitReflection(deps, input, &ReflectionOutput{
		Decision: DecisionComplete,
		Reason:   reason,
	})

	p.emitStateTransition(deps, agent.StateReflect, agent.StateComplete, reason)

	return agent.StateComplete, nil
}

// analyzeProgress evaluates progress and determines next action.
//
// Description:
//
//	Uses heuristics to decide whether to continue execution,
//	mark as complete, or request user clarification.
//	In practice, this could use the LLM for more sophisticated analysis.
//
// Inputs:
//
//	deps - Phase dependencies.
//	input - The reflection input data.
//
// Outputs:
//
//	*ReflectionOutput - The decision and reasoning.
//
// CLARIFY Policy:
//
//	CLARIFY should only be triggered when the agent is genuinely stuck after
//	multiple failed attempts. It should NOT be used for:
//	  - Broad questions (agent should explore the codebase)
//	  - "Which file/function?" questions (agent should search)
//	  - Requests that can be answered by exploration
//
//	CLARIFY IS appropriate when:
//	  - Multiple tool calls have failed repeatedly
//	  - The agent cannot make progress despite trying
//	  - There's a genuine blocker (missing permissions, invalid paths, etc.)
func (p *ReflectPhase) analyzeProgress(deps *Dependencies, input *ReflectionInput) *ReflectionOutput {
	// Check if recent results indicate completion
	if p.looksComplete(input) {
		return &ReflectionOutput{
			Decision: DecisionComplete,
			Reason:   "task appears complete based on recent results",
		}
	}

	// Check if we're stuck (repeated failures indicate a genuine blocker)
	// Note: This is the ONLY path to CLARIFY from Reflect - we don't go to
	// CLARIFY just because a question is broad or vague. The agent should
	// explore the codebase and provide comprehensive answers.
	if p.looksStuck(input) {
		return &ReflectionOutput{
			Decision:            DecisionClarify,
			Reason:              "multiple recent failures suggest clarification needed",
			ClarificationPrompt: "I've encountered some difficulties. Could you provide more details about what you're trying to accomplish?",
		}
	}

	// Default: continue execution
	return &ReflectionOutput{
		Decision: DecisionContinue,
		Reason:   "progress is being made, continuing execution",
	}
}

// looksComplete determines if the task appears complete.
//
// Inputs:
//
//	input - The reflection input.
//
// Outputs:
//
//	bool - True if the task looks complete.
func (p *ReflectPhase) looksComplete(input *ReflectionInput) bool {
	// If the last response mentions completion
	if input.LastResponse != "" {
		completionPhrases := []string{
			"complete",
			"finished",
			"done",
			"accomplished",
			"implemented",
		}
		for _, phrase := range completionPhrases {
			if containsIgnoreCase(input.LastResponse, phrase) {
				return true
			}
		}
	}

	// Check for legitimate "not found" answers
	// These indicate a complete answer (symbol doesn't exist) rather than being stuck
	if input.LastResponse != "" {
		notFoundPhrases := []string{
			"not found",
			"does not exist",
			"could not locate",
			"unable to find",
			"no function",
			"no symbol",
			"no matches",
		}

		lastRespLower := strings.ToLower(input.LastResponse)
		hasNotFound := false
		for _, phrase := range notFoundPhrases {
			if strings.Contains(lastRespLower, phrase) {
				hasNotFound = true
				break
			}
		}

		// If response says "not found" AND mentions searching, it's complete
		if hasNotFound {
			searchIndicators := []string{"searched", "looked", "analyz", "checked", "queried"}
			hasSearch := false
			for _, indicator := range searchIndicators {
				if strings.Contains(lastRespLower, indicator) {
					hasSearch = true
					break
				}
			}

			if hasSearch {
				slog.Debug("REFLECT: Detected legitimate 'not found' answer - marking complete",
					slog.String("response_preview", input.LastResponse[:min(100, len(input.LastResponse))]))
				return true
			}
		}
	}

	// Check for empty response loop (Fix 1C - Feb 13, 2026)
	// If we have multiple consecutive results with empty responses,
	// the LLM is stuck in an empty response loop. Mark as complete
	// to trigger final synthesis from tool results.
	if input.LastResponse == "" && len(input.RecentResults) >= 3 {
		// Count results with no meaningful output
		emptyOrFailed := 0
		for _, r := range input.RecentResults {
			// Consider it empty/failed if:
			// - Failed execution, OR
			// - Success but output is tiny (just error message or empty)
			outputStr := fmt.Sprintf("%v", r.Output)
			if !r.Success || len(outputStr) < 100 {
				emptyOrFailed++
			}
		}

		// If most results are empty/failed, we're in a loop
		// Require >75% to be empty (at least 3/4) to avoid false positives
		if float64(emptyOrFailed)/float64(len(input.RecentResults)) > 0.75 {
			slog.Debug("REFLECT: Detected empty response loop - marking complete",
				slog.Int("total_results", len(input.RecentResults)),
				slog.Int("empty_or_failed", emptyOrFailed),
				slog.String("last_response_len", fmt.Sprintf("%d", len(input.LastResponse))))
			return true
		}
	}

	// Phase 11C: Fix "tool-only mode" detection (Feb 14, 2026)
	// Original P0-1 incorrectly checked tool results instead of LLM responses.
	// Now checks LastResponse for tool call patterns with minimal narrative.
	if input.LastResponse != "" && len(input.RecentResults) >= 2 {
		// Check if last LLM response has tool calls but minimal explanatory text
		lastRespLower := strings.ToLower(input.LastResponse)

		// Tool call indicators in LLM response
		hasToolCall := strings.Contains(lastRespLower, "<tool_call>") ||
					   strings.Contains(lastRespLower, "</tool_call>") ||
					   strings.Contains(lastRespLower, "tool_calls")

		// Remove tool call XML to count actual narrative text
		narrativeText := input.LastResponse
		if idx := strings.Index(narrativeText, "<tool_call>"); idx >= 0 {
			narrativeText = narrativeText[:idx]
		}
		narrativeText = strings.TrimSpace(narrativeText)

		// If LLM has tool calls but <20 chars of narrative, it's tool-only mode
		if hasToolCall && len(narrativeText) < 20 {
			slog.Debug("Phase 11C: Detected tool-only mode - LLM calling tools with minimal narrative",
				slog.Int("narrative_chars", len(narrativeText)),
				slog.Int("response_chars", len(input.LastResponse)),
				slog.String("narrative_preview", narrativeText))
			return true
		}
	}

	// If all recent results are successful and there's substantial progress
	if len(input.RecentResults) >= 3 {
		allSuccessful := true
		for _, r := range input.RecentResults {
			if !r.Success {
				allSuccessful = false
				break
			}
		}
		if allSuccessful {
			return true
		}
	}

	return false
}

// looksStuck determines if the agent appears to be stuck.
//
// Inputs:
//
//	input - The reflection input.
//
// Outputs:
//
//	bool - True if the agent looks stuck.
func (p *ReflectPhase) looksStuck(input *ReflectionInput) bool {
	if len(input.RecentResults) < 3 {
		return false
	}

	// If last response contains "not found", it's a valid answer, not stuck
	if input.LastResponse != "" {
		lastRespLower := strings.ToLower(input.LastResponse)
		if strings.Contains(lastRespLower, "not found") ||
			strings.Contains(lastRespLower, "does not exist") {
			return false // Valid "not found" answer, not stuck
		}
	}

	// Count recent failures
	failures := 0
	for _, r := range input.RecentResults {
		if !r.Success {
			failures++
		}
	}

	// More than 60% failures indicates being stuck
	return float64(failures)/float64(len(input.RecentResults)) > 0.6
}

// handleDecision transitions to the appropriate state based on decision.
//
// Inputs:
//
//	deps - Phase dependencies.
//	output - The reflection decision.
//
// Outputs:
//
//	agent.AgentState - The next state.
//	error - Always nil.
func (p *ReflectPhase) handleDecision(deps *Dependencies, output *ReflectionOutput) (agent.AgentState, error) {
	var nextState agent.AgentState

	switch output.Decision {
	case DecisionContinue:
		nextState = agent.StateExecute
	case DecisionComplete:
		// Synthesize a final response before completing
		p.synthesizeResponse(context.Background(), deps, output.Reason)
		nextState = agent.StateComplete
	case DecisionClarify:
		nextState = agent.StateClarify
	default:
		nextState = agent.StateExecute
	}

	p.emitStateTransition(deps, agent.StateReflect, nextState, output.Reason)

	return nextState, nil
}

// emitReflection emits a reflection event.
//
// Inputs:
//
//	deps - Phase dependencies.
//	input - The reflection input.
//	output - The reflection output.
func (p *ReflectPhase) emitReflection(deps *Dependencies, input *ReflectionInput, output *ReflectionOutput) {
	if deps.EventEmitter == nil {
		return
	}

	deps.EventEmitter.Emit(events.TypeReflection, &events.ReflectionData{
		StepsCompleted: input.StepsCompleted,
		TokensUsed:     input.TokensUsed,
		Decision:       string(output.Decision),
		Reason:         output.Reason,
	})
}

// emitStateTransition emits a state transition event.
//
// Inputs:
//
//	deps - Phase dependencies.
//	from - The previous state.
//	to - The new state.
//	reason - The reason for the transition.
func (p *ReflectPhase) emitStateTransition(deps *Dependencies, from, to agent.AgentState, reason string) {
	if deps.EventEmitter == nil {
		return
	}

	deps.EventEmitter.Emit(events.TypeStateTransition, &events.StateTransitionData{
		FromState: from,
		ToState:   to,
		Reason:    reason,
	})
}

// synthesizeResponse generates a final response when completing without one.
//
// Description:
//
//	When the Reflect phase decides to complete (due to limits or decision),
//	but the agent hasn't produced a final text response yet, this method
//	asks the LLM to synthesize a summary of what was learned.
//
//	If synthesis fails due to context overflow or LLM errors, this method
//	generates a fallback response summarizing the tools used and findings.
//
//	Post-synthesis verification runs after each synthesis attempt to catch
//	hallucinations. If verification fails, synthesis is retried with stricter
//	prompts up to MaxRetries times.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	deps - Phase dependencies.
//	reason - The reason for completion.
func (p *ReflectPhase) synthesizeResponse(ctx context.Context, deps *Dependencies, reason string) {
	// Check if we already have a real response (not a tool call placeholder)
	if deps.Context != nil {
		for i := len(deps.Context.ConversationHistory) - 1; i >= 0; i-- {
			msg := deps.Context.ConversationHistory[i]
			if msg.Role == "assistant" && msg.Content != "" {
				// cb_30b fix: Skip tool call placeholder messages
				// These are internal bookkeeping "[Tool calls: X, Y, Z]" not real responses
				if strings.HasPrefix(msg.Content, "[Tool calls:") {
					continue
				}
				// Already have a real response, no need to synthesize
				slog.Debug("Skipping synthesis - response already exists",
					slog.String("session_id", deps.Session.ID),
				)
				return
			}
			// Stop if we hit a user message (no assistant response after it)
			if msg.Role == "user" {
				break
			}
		}
	}

	// No LLM client available, use fallback
	if deps.LLMClient == nil {
		slog.Warn("Cannot synthesize response - no LLM client, using fallback",
			slog.String("session_id", deps.Session.ID),
		)
		p.storeFallbackResponse(deps)
		return
	}

	slog.Info("Synthesizing final response",
		slog.String("session_id", deps.Session.ID),
		slog.String("reason", reason),
	)

	// Estimate context size and truncate if needed to prevent overflow
	synthesisCtx := p.prepareSynthesisContext(deps)

	// Build anchored synthesis prompt if builder available
	anchoredBuilder := p.getAnchoredSynthesisBuilder(deps)
	userQuestion := grounding.ExtractUserQuestion(synthesisCtx)
	projectLang := p.detectProjectLanguage(synthesisCtx)

	var baseSynthesisPrompt string
	if anchoredBuilder != nil {
		baseSynthesisPrompt = anchoredBuilder.BuildAnchoredSynthesisPrompt(ctx, synthesisCtx, userQuestion, projectLang)
	} else {
		// Fallback to basic prompt
		baseSynthesisPrompt = "Based on the tools you used and information you gathered, please provide a concise summary answering the user's original question. Focus on the key findings and insights."
	}

	// Get post-synthesis verifier if available
	postSynthesisVerifier := p.getPostSynthesisVerifier(deps)
	maxRetries := 3
	if postSynthesisVerifier == nil {
		maxRetries = 1 // Single attempt without verification
	}

	var lastResponse string
	var lastViolations []grounding.Violation

	for retryCount := 0; retryCount < maxRetries; retryCount++ {
		// Generate prompt (with increasing strictness on retries)
		synthesisPrompt := baseSynthesisPrompt
		if postSynthesisVerifier != nil && retryCount > 0 && len(lastViolations) > 0 {
			synthesisPrompt = postSynthesisVerifier.GenerateStricterPrompt(baseSynthesisPrompt, lastViolations, grounding.StrictnessLevel(retryCount))
		}

		// Create fresh context copy for this attempt
		attemptCtx := p.copySynthesisContext(synthesisCtx)
		attemptCtx.ConversationHistory = append(attemptCtx.ConversationHistory, agent.Message{
			Role:    "user",
			Content: synthesisPrompt,
		})

		// Build and send LLM request (no tools - just get a text response)
		request := llm.BuildRequest(attemptCtx, nil, 4096)

		response, err := deps.LLMClient.Complete(ctx, request)
		if err != nil {
			slog.Error("Failed to synthesize response",
				slog.String("session_id", deps.Session.ID),
				slog.String("error", err.Error()),
				slog.Int("retry_count", retryCount),
			)
			if retryCount == maxRetries-1 {
				p.storeFallbackResponse(deps)
				return
			}
			continue
		}

		// Check for empty response (context overflow or model issue)
		if response.Content == "" {
			slog.Warn("Synthesis returned empty content",
				slog.String("session_id", deps.Session.ID),
				slog.Int("output_tokens", response.OutputTokens),
				slog.String("stop_reason", response.StopReason),
				slog.Int("retry_count", retryCount),
			)
			if retryCount == maxRetries-1 {
				p.storeFallbackResponse(deps)
				return
			}
			continue
		}

		lastResponse = response.Content

		// Post-synthesis verification
		if postSynthesisVerifier != nil {
			verifyResult, verifyErr := postSynthesisVerifier.VerifyPostSynthesis(ctx, response.Content, deps.Context, retryCount)
			if verifyErr != nil {
				slog.Warn("Post-synthesis verification error",
					slog.String("session_id", deps.Session.ID),
					slog.String("error", verifyErr.Error()),
				)
				// Treat verification errors as pass (non-fatal)
			} else if !verifyResult.Passed {
				slog.Warn("Post-synthesis verification failed",
					slog.String("session_id", deps.Session.ID),
					slog.Int("violations", len(verifyResult.Violations)),
					slog.Int("retry_count", retryCount),
					slog.String("strictness", verifyResult.StrictnessLevel.String()),
				)
				lastViolations = verifyResult.Violations

				// Check if we've exhausted retries
				if verifyResult.NeedsFeedbackLoop {
					slog.Info("Post-synthesis feedback loop triggered",
						slog.String("session_id", deps.Session.ID),
						slog.Int("questions", len(verifyResult.FeedbackQuestions)),
					)
					// For now, store the best response we have with a warning
					// Future: trigger re-exploration
					lastResponse = p.appendVerificationWarning(lastResponse, verifyResult)
					break
				}

				// Retry with stricter prompt
				continue
			} else {
				slog.Debug("Post-synthesis verification passed",
					slog.String("session_id", deps.Session.ID),
					slog.Int("retry_count", retryCount),
				)
			}
		}

		// Verification passed or not available - use this response
		break
	}

	// Store the synthesized response
	if lastResponse != "" && deps.Context != nil {
		deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
			Role:    "assistant",
			Content: lastResponse,
		})
		// Persist to session
		deps.Session.SetCurrentContext(deps.Context)

		slog.Info("Synthesized response stored",
			slog.String("session_id", deps.Session.ID),
			slog.Int("response_len", len(lastResponse)),
		)
	} else if lastResponse == "" {
		p.storeFallbackResponse(deps)
	}
}

// getPostSynthesisVerifier extracts the post-synthesis verifier from deps if available.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	grounding.PostSynthesisVerifier - The verifier, or nil if not available.
func (p *ReflectPhase) getPostSynthesisVerifier(deps *Dependencies) grounding.PostSynthesisVerifier {
	if deps.ResponseGrounder == nil {
		return nil
	}

	// Type assert to get the PostSynthesisVerifier interface
	if verifier, ok := deps.ResponseGrounder.(grounding.PostSynthesisVerifier); ok {
		return verifier
	}

	return nil
}

// copySynthesisContext creates a shallow copy of the context for retry attempts.
//
// Inputs:
//
//	ctx - The context to copy.
//
// Outputs:
//
//	*agent.AssembledContext - A copy that can be modified independently.
func (p *ReflectPhase) copySynthesisContext(ctx *agent.AssembledContext) *agent.AssembledContext {
	if ctx == nil {
		return &agent.AssembledContext{}
	}

	// Create a copy with copied slices
	copy := &agent.AssembledContext{
		SystemPrompt: ctx.SystemPrompt,
		TotalTokens:  ctx.TotalTokens,
	}

	// Copy conversation history
	if len(ctx.ConversationHistory) > 0 {
		copy.ConversationHistory = make([]agent.Message, len(ctx.ConversationHistory))
		for i, msg := range ctx.ConversationHistory {
			copy.ConversationHistory[i] = msg
		}
	}

	// Copy tool results
	if len(ctx.ToolResults) > 0 {
		copy.ToolResults = make([]agent.ToolResult, len(ctx.ToolResults))
		for i, result := range ctx.ToolResults {
			copy.ToolResults[i] = result
		}
	}

	// Copy code context
	if len(ctx.CodeContext) > 0 {
		copy.CodeContext = make([]agent.CodeEntry, len(ctx.CodeContext))
		for i, entry := range ctx.CodeContext {
			copy.CodeContext[i] = entry
		}
	}

	return copy
}

// appendVerificationWarning appends a warning to the response about verification issues.
//
// Inputs:
//
//	response - The original response.
//	result - The verification result with violations.
//
// Outputs:
//
//	string - The response with appended warning.
func (p *ReflectPhase) appendVerificationWarning(response string, result *grounding.PostSynthesisResult) string {
	if result == nil || len(result.Violations) == 0 {
		return response
	}

	warning := "\n\n---\n⚠️ **Note**: This response may contain some unverified claims. "
	warning += "Consider using exploration tools to verify specific details."

	return response + warning
}

// getAnchoredSynthesisBuilder extracts the anchored synthesis builder from deps if available.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	grounding.AnchoredSynthesisBuilder - The builder, or nil if not available.
func (p *ReflectPhase) getAnchoredSynthesisBuilder(deps *Dependencies) grounding.AnchoredSynthesisBuilder {
	if deps.AnchoredSynthesisBuilder != nil {
		return deps.AnchoredSynthesisBuilder
	}
	return nil
}

// detectProjectLanguage detects the primary language from context.
//
// Inputs:
//
//	ctx - The assembled context with code entries.
//
// Outputs:
//
//	string - The detected language (e.g., "go", "python"), or empty if unknown.
func (p *ReflectPhase) detectProjectLanguage(ctx *agent.AssembledContext) string {
	if ctx == nil {
		return ""
	}

	// Count file extensions
	langCounts := make(map[string]int)

	for _, entry := range ctx.CodeContext {
		lang := grounding.DetectLanguageFromPath(entry.FilePath)
		if lang != "" {
			langCounts[lang]++
		}
	}

	// Find the most common language
	maxCount := 0
	primaryLang := ""
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			primaryLang = lang
		}
	}

	return primaryLang
}

// prepareSynthesisContext creates a reduced context for synthesis.
//
// Description:
//
//	Creates a copy of the context with truncated tool results to prevent
//	context overflow during synthesis. Keeps the most recent and relevant
//	information while staying within reasonable token limits.
//
// Inputs:
//
//	deps - Phase dependencies.
//
// Outputs:
//
//	*agent.AssembledContext - A reduced context suitable for synthesis.
func (p *ReflectPhase) prepareSynthesisContext(deps *Dependencies) *agent.AssembledContext {
	if deps.Context == nil {
		return &agent.AssembledContext{}
	}

	// Estimate current context size (rough: 4 chars per token)
	totalSize := len(deps.Context.SystemPrompt)
	for _, msg := range deps.Context.ConversationHistory {
		totalSize += len(msg.Content)
	}
	for _, result := range deps.Context.ToolResults {
		totalSize += len(result.Output)
	}

	slog.Debug("Synthesis context size estimation",
		slog.String("session_id", deps.Session.ID),
		slog.Int("total_chars", totalSize),
		slog.Int("estimated_tokens", totalSize/4),
		slog.Int("tool_results", len(deps.Context.ToolResults)),
	)

	// If context is small enough (under ~20K tokens), use as-is
	maxContextChars := 80000 // ~20K tokens at 4 chars/token
	if totalSize <= maxContextChars {
		return deps.Context
	}

	slog.Info("Truncating context for synthesis",
		slog.String("session_id", deps.Session.ID),
		slog.Int("original_chars", totalSize),
		slog.Int("max_chars", maxContextChars),
	)

	// Create a reduced context
	reduced := &agent.AssembledContext{
		SystemPrompt: deps.Context.SystemPrompt,
		TotalTokens:  deps.Context.TotalTokens,
	}

	// Keep the user's original query (first user message) and last few messages
	if len(deps.Context.ConversationHistory) > 0 {
		reduced.ConversationHistory = append(reduced.ConversationHistory, deps.Context.ConversationHistory[0])

		// Add a summary message if we're truncating
		if len(deps.Context.ConversationHistory) > 5 {
			reduced.ConversationHistory = append(reduced.ConversationHistory, agent.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[%d intermediate messages and tool calls truncated for synthesis]", len(deps.Context.ConversationHistory)-5),
			})
		}

		// Keep the last 4 messages
		start := len(deps.Context.ConversationHistory) - 4
		if start < 1 {
			start = 1
		}
		for i := start; i < len(deps.Context.ConversationHistory); i++ {
			reduced.ConversationHistory = append(reduced.ConversationHistory, deps.Context.ConversationHistory[i])
		}
	}

	// Keep only the most recent tool results with truncated output
	maxResultLen := 2000
	maxResults := 5
	start := len(deps.Context.ToolResults) - maxResults
	if start < 0 {
		start = 0
	}
	for i := start; i < len(deps.Context.ToolResults); i++ {
		result := deps.Context.ToolResults[i]
		truncatedOutput := result.Output
		if len(truncatedOutput) > maxResultLen {
			truncatedOutput = truncatedOutput[:maxResultLen] + "\n... [output truncated]"
		}
		reduced.ToolResults = append(reduced.ToolResults, agent.ToolResult{
			InvocationID: result.InvocationID,
			Output:       truncatedOutput,
			Success:      result.Success,
		})
	}

	return reduced
}

// storeFallbackResponse generates and stores a fallback response.
//
// Description:
//
//	When LLM synthesis fails, this method creates a fallback response
//	summarizing the tool results and discoveries.
//
// Inputs:
//
//	deps - Phase dependencies.
func (p *ReflectPhase) storeFallbackResponse(deps *Dependencies) {
	if deps.Context == nil {
		return
	}

	// Build a fallback response from tool results
	var sb strings.Builder
	sb.WriteString("Based on my exploration of the codebase, here's what I found:\n\n")

	// Count tool results and extract findings
	successfulResults := 0
	var findings []string
	for _, result := range deps.Context.ToolResults {
		// Extract key findings from successful tool results
		if result.Success && result.Output != "" {
			successfulResults++
			// Take first 300 chars of each result as a finding preview
			preview := result.Output
			if len(preview) > 300 {
				preview = preview[:300] + "..."
			}
			// Clean up the preview (remove excessive newlines)
			preview = strings.ReplaceAll(preview, "\n\n", "\n")
			findings = append(findings, preview)
		}
	}

	// Report exploration summary
	sb.WriteString(fmt.Sprintf("**Exploration summary:** %d tool calls, %d successful results\n\n",
		len(deps.Context.ToolResults), successfulResults))

	// Report key findings (up to 5)
	if len(findings) > 0 {
		sb.WriteString("**Key discoveries:**\n")
		maxFindings := 5
		if len(findings) < maxFindings {
			maxFindings = len(findings)
		}
		for i := 0; i < maxFindings; i++ {
			sb.WriteString(fmt.Sprintf("\n%d. %s\n", i+1, findings[i]))
		}
	}

	sb.WriteString("\n---\n*Note: Full synthesis was not available due to context size. This is a summary of the exploration results.*")

	fallbackContent := sb.String()

	deps.Context.ConversationHistory = append(deps.Context.ConversationHistory, agent.Message{
		Role:    "assistant",
		Content: fallbackContent,
	})
	deps.Session.SetCurrentContext(deps.Context)

	slog.Info("Stored fallback response",
		slog.String("session_id", deps.Session.ID),
		slog.Int("response_len", len(fallbackContent)),
		slog.Int("total_results", len(deps.Context.ToolResults)),
		slog.Int("findings_count", len(findings)),
	)
}
