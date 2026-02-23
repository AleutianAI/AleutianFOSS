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
	"log/slog"

	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

// EgressGuardBuilder constructs egress guard decorators from shared components.
//
// Description:
//
//	Holds the shared infrastructure (ControlPlane, Policy, Consent, Classifier,
//	Auditor, CostEstimator, RateLimiter, SecretManager) and creates per-session
//	guard wrappers. TokenBudget is per-session, created fresh per wrap call.
//
// Thread Safety: Safe for concurrent use. All shared components are concurrent-safe.
type EgressGuardBuilder struct {
	controlPlane  *ProviderControlPlane
	policy        *ProviderPolicy
	consent       *ConsentPolicy
	classifier    DataClassifier
	auditor       *EgressAuditor
	costEstimator *CostEstimator
	rateLimiter   *RateLimiter
	secretManager *SecretManager
	minimizer     *DataMinimizer
	logger        *slog.Logger
}

// NewEgressGuardBuilder creates a builder from egress configuration.
//
// Description:
//
//	Initializes all shared components from the provided config and classifier.
//	The classifier should be a PolicyEngineClassifier when available, or a
//	NoOpClassifier as a fallback.
//
// Inputs:
//   - cfg: Egress configuration loaded from environment variables.
//   - classifier: Data classifier for content inspection.
//
// Outputs:
//   - *EgressGuardBuilder: Configured builder ready to create guard wrappers.
func NewEgressGuardBuilder(cfg *EgressConfig, classifier DataClassifier) *EgressGuardBuilder {
	if classifier == nil {
		classifier = NewNoOpClassifier()
	}

	logger := slog.Default().With(slog.String("component", "egress"))

	return &EgressGuardBuilder{
		controlPlane:  NewProviderControlPlane(cfg.Enabled),
		policy:        NewProviderPolicy(cfg.Allowlist, cfg.Denylist),
		consent:       NewConsentPolicy(cfg.LocalOnly, cfg.ProviderConsent),
		classifier:    classifier,
		auditor:       NewEgressAuditor(logger, cfg.AuditEnabled, cfg.AuditHashContent),
		costEstimator: NewCostEstimator(cfg.CostLimitCents),
		rateLimiter:   NewRateLimiter(cfg.RateLimitsPerMin),
		secretManager: NewSecretManager(0),
		minimizer:     NewDataMinimizer(cfg.MinimizationEnabled, cfg.MinContextTokens, logger),
		logger:        logger,
	}
}

// WrapAgentClient wraps an agentllm.Client with egress guard checks.
//
// Description:
//
//	Creates a new EgressGuardClient that decorates the inner client.
//	A fresh TokenBudget is created per call using the provided token limit.
//
// Inputs:
//   - client: The raw LLM client to wrap.
//   - provider: The provider name (e.g., "anthropic").
//   - model: The model name.
//   - sessionID: The agent session ID.
//   - tokenLimit: Token budget for this session/role. 0 means unlimited.
//
// Outputs:
//   - agentllm.Client: The guarded client (or the original if provider is "ollama").
func (b *EgressGuardBuilder) WrapAgentClient(
	client agentllm.Client,
	provider, model, sessionID string,
	tokenLimit int,
) agentllm.Client {
	// Ollama is local — no egress guard needed
	if provider == "ollama" {
		return client
	}

	return &EgressGuardClient{
		inner:         client,
		controlPlane:  b.controlPlane,
		policy:        b.policy,
		consent:       b.consent,
		classifier:    b.classifier,
		rateLimiter:   b.rateLimiter,
		tokenBudget:   NewTokenBudget("MAIN", tokenLimit),
		costEstimator: b.costEstimator,
		auditor:       b.auditor,
		metrics:       NewProviderMetrics(provider),
		minimizer:     b.minimizer,
		provider:      provider,
		model:         model,
		sessionID:     sessionID,
	}
}

// WrapChatClient wraps a ChatClient with egress guard checks.
//
// Description:
//
//	Creates a new EgressGuardChatClient for Router or ParamExtractor roles.
//
// Inputs:
//   - client: The raw ChatClient to wrap.
//   - provider: The provider name.
//   - model: The model name.
//   - sessionID: The agent session ID.
//   - role: The role name ("ROUTER" or "PARAM").
//   - tokenLimit: Token budget for this session/role. 0 means unlimited.
//
// Outputs:
//   - ChatClient: The guarded client (or the original if provider is "ollama").
func (b *EgressGuardBuilder) WrapChatClient(
	client ChatClient,
	provider, model, sessionID, role string,
	tokenLimit int,
) ChatClient {
	// Ollama is local — no egress guard needed
	if provider == "ollama" {
		return client
	}

	return &EgressGuardChatClient{
		inner:         client,
		controlPlane:  b.controlPlane,
		policy:        b.policy,
		consent:       b.consent,
		classifier:    b.classifier,
		rateLimiter:   b.rateLimiter,
		tokenBudget:   NewTokenBudget(role, tokenLimit),
		costEstimator: b.costEstimator,
		auditor:       b.auditor,
		metrics:       NewProviderMetrics(provider),
		provider:      provider,
		model:         model,
		sessionID:     sessionID,
	}
}

// ControlPlane returns the shared control plane for runtime kill switch access.
//
// Outputs:
//   - *ProviderControlPlane: The shared control plane.
func (b *EgressGuardBuilder) ControlPlane() *ProviderControlPlane {
	return b.controlPlane
}

// CostEstimator returns the shared cost estimator for end-of-session reporting.
//
// Outputs:
//   - *CostEstimator: The shared cost estimator.
func (b *EgressGuardBuilder) CostEstimator() *CostEstimator {
	return b.costEstimator
}
