// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

// EgressGuardClient wraps an agentllm.Client with egress control checks.
//
// Description:
//
//	Implements agentllm.Client by delegating Complete() to the inner client
//	after passing pre-flight checks (ControlPlane, Policy, Consent, Classify,
//	RateLimit, Budget, Cost). After the call, records audit trail and metrics.
//
//	Name() and Model() are delegated directly without checks.
//
// Thread Safety: Safe for concurrent use (all components are concurrent-safe).
type EgressGuardClient struct {
	inner         agentllm.Client
	controlPlane  *ProviderControlPlane
	policy        *ProviderPolicy
	consent       *ConsentPolicy
	classifier    DataClassifier
	rateLimiter   *RateLimiter
	tokenBudget   *TokenBudget
	costEstimator *CostEstimator
	auditor       *EgressAuditor
	metrics       *ProviderMetrics
	minimizer     *DataMinimizer
	provider      string
	model         string
	sessionID     string
}

// Complete sends a completion request after passing all egress checks.
//
// Description:
//
//	Pre-flight check order:
//	  1. ControlPlane — is the provider enabled?
//	  2. Policy — is the provider in the allowlist (not in denylist)?
//	  3. Consent — has the user given consent for this provider?
//	  4. Classify — does the content contain sensitive data?
//	  5. RateLimit — is the provider within rate limits?
//	  6. Budget — does the session have enough token budget?
//	  7. Cost — is the estimated cost within the cost ceiling?
//
//	If any check fails, returns the appropriate sentinel error without
//	calling the inner client.
//
// Inputs:
//   - ctx: Context for cancellation and tracing.
//   - request: The LLM completion request.
//
// Outputs:
//   - *agentllm.Response: The response from the inner client.
//   - error: Non-nil if a pre-flight check fails or the inner call errors.
func (g *EgressGuardClient) Complete(ctx context.Context, request *agentllm.Request) (*agentllm.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request == nil {
		return nil, fmt.Errorf("egress guard: request must not be nil")
	}

	ctx, span := otel.Tracer("aleutian.trace").Start(ctx, "egress.EgressGuardClient.Complete",
		oteltrace.WithAttributes(
			attribute.String("provider", g.provider),
			attribute.String("model", g.model),
			attribute.String("session_id", g.sessionID),
		),
	)
	defer span.End()

	requestID := uuid.New().String()
	decision := NewEgressDecision(requestID, g.sessionID, g.provider, g.model)
	start := time.Now()

	// Serialize messages once for classification and hashing
	serialized := serializeAgentRequest(request)

	// Pre-flight checks
	if blocked, blockedBy, reason := g.preFlightChecks(ctx, serialized, decision); blocked {
		decision.DurationMs = time.Since(start).Milliseconds()
		g.auditor.LogBlocked(ctx, decision)
		RecordEgressBlocked(g.provider, blockedBy)
		span.SetAttributes(attribute.String("blocked_by", blockedBy))
		span.SetStatus(codes.Error, reason)
		return nil, fmt.Errorf("%s: %w", reason, sentinelForBlocker(blockedBy))
	}

	// Audit before call
	decision.Allowed = true
	g.auditor.LogBefore(ctx, decision)

	// Minimize request before sending to external provider
	minimizedReq := request
	if g.minimizer != nil {
		var stats MinimizationStats
		minimizedReq, stats = g.minimizer.Minimize(request, g.provider, g.model)
		g.auditor.LogMinimization(ctx, requestID, stats)
	}

	// Delegate to inner client
	resp, err := g.inner.Complete(ctx, minimizedReq)

	// Post-call recording
	callDuration := time.Since(start)
	var inputTokens, outputTokens int
	if resp != nil {
		inputTokens = resp.InputTokens
		outputTokens = resp.OutputTokens
	}

	costCents := g.costEstimator.Record(g.model, inputTokens, outputTokens)
	if g.tokenBudget != nil {
		g.tokenBudget.Record(inputTokens + outputTokens)
	}
	g.metrics.RecordCall(inputTokens, outputTokens, callDuration)

	RecordEgressAllowed(g.provider, inputTokens, outputTokens, callDuration.Seconds(), costCents)

	g.auditor.LogAfter(ctx, requestID, g.provider, g.model,
		inputTokens, outputTokens, callDuration.Milliseconds(), costCents, err)

	if err != nil {
		g.metrics.RecordError()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	span.SetStatus(codes.Ok, "")
	return resp, nil
}

// Name returns the provider name from the inner client.
func (g *EgressGuardClient) Name() string {
	return g.inner.Name()
}

// Model returns the model name from the inner client.
func (g *EgressGuardClient) Model() string {
	return g.inner.Model()
}

// EgressGuardChatClient wraps a ChatClient with egress control checks.
//
// Description:
//
//	Implements ChatClient by delegating Chat() to the inner client
//	after passing pre-flight checks.
//
// Thread Safety: Safe for concurrent use.
type EgressGuardChatClient struct {
	inner         ChatClient
	controlPlane  *ProviderControlPlane
	policy        *ProviderPolicy
	consent       *ConsentPolicy
	classifier    DataClassifier
	rateLimiter   *RateLimiter
	tokenBudget   *TokenBudget
	costEstimator *CostEstimator
	auditor       *EgressAuditor
	metrics       *ProviderMetrics
	provider      string
	model         string
	sessionID     string
}

// Chat sends a chat request after passing all egress checks.
//
// Inputs:
//   - ctx: Context for cancellation and tracing.
//   - messages: The conversation messages.
//   - opts: Chat options.
//
// Outputs:
//   - string: The assistant's response text.
//   - error: Non-nil if a pre-flight check fails or the inner call errors.
func (g *EgressGuardChatClient) Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, span := otel.Tracer("aleutian.trace").Start(ctx, "egress.EgressGuardChatClient.Chat",
		oteltrace.WithAttributes(
			attribute.String("provider", g.provider),
			attribute.String("model", g.model),
			attribute.String("session_id", g.sessionID),
		),
	)
	defer span.End()

	requestID := uuid.New().String()
	decision := NewEgressDecision(requestID, g.sessionID, g.provider, g.model)
	start := time.Now()

	// Serialize messages once for classification and hashing
	serialized := serializeChatMessages(messages)

	// Pre-flight checks
	if blocked, blockedBy, reason := g.preFlightChecks(ctx, serialized, decision); blocked {
		decision.DurationMs = time.Since(start).Milliseconds()
		g.auditor.LogBlocked(ctx, decision)
		RecordEgressBlocked(g.provider, blockedBy)
		span.SetAttributes(attribute.String("blocked_by", blockedBy))
		span.SetStatus(codes.Error, reason)
		return "", fmt.Errorf("%s: %w", reason, sentinelForBlocker(blockedBy))
	}

	decision.Allowed = true
	g.auditor.LogBefore(ctx, decision)

	// Delegate to inner client
	resp, err := g.inner.Chat(ctx, messages, opts)

	callDuration := time.Since(start)

	// Chat responses don't report token counts, estimate based on response length
	estimatedOutputTokens := len(resp) / 4 // rough approximation
	costCents := g.costEstimator.Record(g.model, 0, estimatedOutputTokens)
	g.metrics.RecordCall(0, estimatedOutputTokens, callDuration)

	RecordEgressAllowed(g.provider, 0, estimatedOutputTokens, callDuration.Seconds(), costCents)

	g.auditor.LogAfter(ctx, requestID, g.provider, g.model,
		0, estimatedOutputTokens, callDuration.Milliseconds(), costCents, err)

	if err != nil {
		g.metrics.RecordError()
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", err
	}

	span.SetStatus(codes.Ok, "")
	return resp, nil
}

// preFlightChecks runs all egress pre-flight checks for either guard type.
// Returns (blocked, blockerName, reason) — blocked=false means all checks passed.
func (g *EgressGuardClient) preFlightChecks(ctx context.Context, serialized []byte, decision *EgressDecision) (bool, string, string) {
	return runPreFlightChecks(
		ctx, serialized, decision,
		g.controlPlane, g.policy, g.consent, g.classifier,
		g.rateLimiter, g.tokenBudget, g.costEstimator,
		g.provider, g.model,
	)
}

func (g *EgressGuardChatClient) preFlightChecks(ctx context.Context, serialized []byte, decision *EgressDecision) (bool, string, string) {
	return runPreFlightChecks(
		ctx, serialized, decision,
		g.controlPlane, g.policy, g.consent, g.classifier,
		g.rateLimiter, g.tokenBudget, g.costEstimator,
		g.provider, g.model,
	)
}

// runPreFlightChecks is the shared implementation for all guard types.
func runPreFlightChecks(
	ctx context.Context,
	serialized []byte,
	decision *EgressDecision,
	controlPlane *ProviderControlPlane,
	policy *ProviderPolicy,
	consent *ConsentPolicy,
	classifier DataClassifier,
	rateLimiter *RateLimiter,
	tokenBudget *TokenBudget,
	costEstimator *CostEstimator,
	provider, model string,
) (bool, string, string) {
	// P-1: Kill switch
	if enabled, reason := controlPlane.IsEnabled(provider); !enabled {
		decision.BlockedBy = "kill_switch"
		decision.BlockReason = reason
		return true, "kill_switch", reason
	}

	// P-2: Allowlist/denylist
	if allowed, reason := policy.IsAllowed(provider); !allowed {
		decision.BlockedBy = "policy"
		decision.BlockReason = reason
		return true, "policy", reason
	}

	// P-4: Consent
	if consented, reason := consent.HasConsent(provider); !consented {
		decision.BlockedBy = "consent"
		decision.BlockReason = reason
		return true, "consent", reason
	}

	// P-3: Data classification
	sensitivity := classifier.Classify(ctx, serialized)
	decision.Sensitivity = sensitivity
	RecordEgressSensitivity(provider, sensitivity)

	if !sensitivity.AllowsExternalSend() {
		reason := fmt.Sprintf("data classified as %s — cannot send to external provider %q", sensitivity, provider)
		decision.BlockedBy = "sensitive_data"
		decision.BlockReason = reason
		return true, "sensitive_data", reason
	}

	// Content hash for audit
	decision.ContentHash = HashContent(serialized)

	// Rate limit
	if allowed, retryAfter := rateLimiter.Allow(provider); !allowed {
		reason := fmt.Sprintf("rate limit exceeded for %q — retry after %v", provider, retryAfter)
		decision.BlockedBy = "rate_limit"
		decision.BlockReason = reason
		return true, "rate_limit", reason
	}

	// Estimated tokens (rough: 1 token per 4 bytes)
	estimatedTokens := len(serialized) / 4
	if estimatedTokens < 100 {
		estimatedTokens = 100
	}
	decision.EstimatedTokens = estimatedTokens

	// P-7: Token budget
	if tokenBudget != nil {
		if ok, remaining := tokenBudget.CanSpend(estimatedTokens); !ok {
			reason := fmt.Sprintf("token budget exhausted — %d tokens remaining, need %d", remaining, estimatedTokens)
			decision.BlockedBy = "budget"
			decision.BlockReason = reason
			return true, "budget", reason
		}
	}

	// Cost estimation
	estimatedOutput := estimatedTokens / 2 // assume output is ~half of input
	if ok, costCents := costEstimator.CanAfford(model, estimatedTokens, estimatedOutput); !ok {
		reason := fmt.Sprintf("cost limit would be exceeded — estimated %.2f cents", costCents)
		decision.BlockedBy = "cost"
		decision.BlockReason = reason
		decision.EstimatedCostCents = costCents
		return true, "cost", reason
	} else {
		decision.EstimatedCostCents = costCents
	}

	return false, "", ""
}

// sentinelForBlocker returns the appropriate sentinel error for a blocker name.
func sentinelForBlocker(blockedBy string) error {
	switch blockedBy {
	case "kill_switch":
		return ErrProviderDisabled
	case "policy":
		return ErrProviderDenied
	case "consent":
		return ErrNoConsent
	case "sensitive_data":
		return ErrSensitiveData
	case "rate_limit":
		return ErrRateLimited
	case "budget":
		return ErrTokenBudgetExhausted
	case "cost":
		return ErrCostLimitReached
	default:
		return ErrProviderDisabled
	}
}

// serializeAgentRequest serializes an agent request's messages for classification.
// Returns an empty (non-nil) byte slice for requests with no content, ensuring
// downstream classification and hashing always receive valid input.
func serializeAgentRequest(req *agentllm.Request) []byte {
	if req == nil {
		return []byte{}
	}

	var sb strings.Builder
	if req.SystemPrompt != "" {
		sb.WriteString(req.SystemPrompt)
		sb.WriteByte('\n')
	}
	for _, msg := range req.Messages {
		sb.WriteString(msg.Content)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// serializeChatMessages serializes chat messages for classification.
// Returns an empty (non-nil) byte slice for empty message lists, ensuring
// downstream classification and hashing always receive valid input.
func serializeChatMessages(messages []datatypes.Message) []byte {
	if len(messages) == 0 {
		return []byte{}
	}

	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(msg.Content)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}
