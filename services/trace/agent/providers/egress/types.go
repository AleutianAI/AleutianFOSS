// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package egress provides data egress control, audit trail, and compliance
// enforcement for LLM provider calls. It implements a decorator pattern that
// wraps raw LLM clients with pre-flight checks (kill switch, allowlist/denylist,
// consent, data classification, rate limiting, token budgets, cost estimation)
// and post-call auditing (structured logging with content hashes, Prometheus
// metrics).
//
// Thread Safety:
//
//	All exported types are safe for concurrent use unless documented otherwise.
package egress

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// ChatClient mirrors providers.ChatClient to avoid an import cycle between
// the egress and providers packages. Go's structural typing ensures that
// any providers.ChatClient implementation automatically satisfies this interface.
//
// Thread Safety: Implementations must be safe for concurrent use.
type ChatClient interface {
	Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error)
}

// ChatOptions mirrors providers.ChatOptions. Kept structurally identical so
// that callers can convert between the two with a simple cast.
type ChatOptions struct {
	Temperature float64
	MaxTokens   int
	KeepAlive   string
	NumCtx      int
	Model       string
}

// DataSensitivity represents the classification level of data being sent
// to an external provider.
type DataSensitivity int

const (
	// SensitivityPublic is data with no restrictions on external transmission.
	SensitivityPublic DataSensitivity = iota

	// SensitivityConfidential is data that may be sent externally with audit logging.
	SensitivityConfidential

	// SensitivityPII is personally identifiable information. Must not leave the environment.
	SensitivityPII

	// SensitivityPHI is protected health information. Must not leave the environment.
	SensitivityPHI

	// SensitivitySecret is secret data (API keys, credentials). Must not leave the environment.
	SensitivitySecret
)

// String returns the human-readable name of the sensitivity level.
func (d DataSensitivity) String() string {
	switch d {
	case SensitivityPublic:
		return "public"
	case SensitivityConfidential:
		return "confidential"
	case SensitivityPII:
		return "pii"
	case SensitivityPHI:
		return "phi"
	case SensitivitySecret:
		return "secret"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}

// AllowsExternalSend returns true if data at this sensitivity level may be
// transmitted to an external cloud provider.
//
// Only Public and Confidential data is allowed to leave the environment.
// PII, PHI, and Secret data must remain local.
func (d DataSensitivity) AllowsExternalSend() bool {
	return d == SensitivityPublic || d == SensitivityConfidential
}

// ParseSensitivity converts a PolicyEngine classification string to a
// DataSensitivity enum value.
//
// Inputs:
//   - classification: The string returned by PolicyEngine.ClassifyData()
//     (e.g., "public", "PII", "Secret", "Confidential").
//
// Outputs:
//   - DataSensitivity: The corresponding enum value. Defaults to SensitivitySecret
//     for unknown classifications (fail-safe).
func ParseSensitivity(classification string) DataSensitivity {
	switch classification {
	case "public":
		return SensitivityPublic
	case "Confidential":
		return SensitivityConfidential
	case "PII":
		return SensitivityPII
	case "PHI":
		return SensitivityPHI
	case "Secret":
		return SensitivitySecret
	default:
		// Fail-safe: unknown classifications are treated as secret
		return SensitivitySecret
	}
}

// =============================================================================
// Sentinel Errors
// =============================================================================

var (
	// ErrProviderDisabled is returned when the global kill switch or a
	// per-provider kill switch has been activated.
	ErrProviderDisabled = errors.New("egress: provider disabled by kill switch")

	// ErrProviderDenied is returned when a provider is blocked by the
	// allowlist/denylist policy.
	ErrProviderDenied = errors.New("egress: provider denied by policy")

	// ErrNoConsent is returned when user consent has not been given for
	// a specific cloud provider.
	ErrNoConsent = errors.New("egress: no user consent for provider")

	// ErrSensitiveData is returned when data classification detects PII,
	// PHI, or Secret data in the outgoing request.
	ErrSensitiveData = errors.New("egress: sensitive data detected in request")

	// ErrTokenBudgetExhausted is returned when the session's token budget
	// for a role has been exhausted.
	ErrTokenBudgetExhausted = errors.New("egress: token budget exhausted")

	// ErrCostLimitReached is returned when the estimated cost of a request
	// would exceed the configured cost ceiling.
	ErrCostLimitReached = errors.New("egress: cost limit reached")

	// ErrRateLimited is returned when a provider's rate limit has been
	// exceeded.
	ErrRateLimited = errors.New("egress: rate limited")

	// ErrSecretNotFound is returned when a required secret cannot be
	// retrieved from the secret backend.
	ErrSecretNotFound = errors.New("egress: secret not found")
)

// =============================================================================
// MinimizationStats — metrics from the data minimization pipeline
// =============================================================================

// MinimizationStats captures what the DataMinimizer changed when minimizing
// a request before sending it to an external provider. Used for audit logging
// and cost analysis.
//
// Thread Safety: MinimizationStats is a value type. Safe to copy.
type MinimizationStats struct {
	// OriginalTokens is the estimated token count before minimization.
	OriginalTokens int

	// MinimizedTokens is the estimated token count after minimization.
	MinimizedTokens int

	// SystemPromptDelta is the token reduction in the system prompt.
	SystemPromptDelta int

	// ToolDefsDelta is the token reduction from tool definition filtering.
	ToolDefsDelta int

	// MessagesDelta is the token reduction from message minimization.
	MessagesDelta int

	// TruncatedResults is the number of tool results that were truncated.
	TruncatedResults int

	// DroppedMessages is the number of messages dropped for context window fit.
	DroppedMessages int
}

// Reduction returns the total token reduction as a percentage (0-100).
// Returns 0 if OriginalTokens is 0.
func (s MinimizationStats) Reduction() float64 {
	if s.OriginalTokens == 0 {
		return 0
	}
	return float64(s.OriginalTokens-s.MinimizedTokens) / float64(s.OriginalTokens) * 100
}

// =============================================================================
// EgressDecision — audit record for each egress attempt
// =============================================================================

// EgressDecision captures the outcome of a single egress pre-flight check
// sequence. It is used by the auditor to produce structured log entries.
//
// Thread Safety: EgressDecision is a value type and safe to copy.
type EgressDecision struct {
	// RequestID is a unique identifier for this egress attempt.
	RequestID string

	// SessionID links this decision to the originating agent session.
	SessionID string

	// Provider is the target LLM provider (e.g., "anthropic", "openai").
	Provider string

	// Model is the specific model being called.
	Model string

	// Sensitivity is the classification of the outgoing data.
	Sensitivity DataSensitivity

	// ContentHash is the SHA256 hex digest of the serialized request content.
	// Used for compliance verification without storing the actual content.
	ContentHash string

	// Allowed is true if the request passed all pre-flight checks.
	Allowed bool

	// BlockedBy is the name of the check that blocked the request (empty if allowed).
	BlockedBy string

	// BlockReason is a human-readable explanation of why the request was blocked.
	BlockReason string

	// EstimatedTokens is the estimated token count for the request.
	EstimatedTokens int

	// EstimatedCostCents is the estimated cost in US cents.
	EstimatedCostCents float64

	// Timestamp is when the decision was made (Unix milliseconds UTC).
	Timestamp int64

	// DurationMs is how long the pre-flight checks took (milliseconds).
	DurationMs int64
}

// NewEgressDecision creates a new EgressDecision with the timestamp set to now.
//
// Inputs:
//   - requestID: Unique identifier for this egress attempt.
//   - sessionID: The agent session ID.
//   - provider: Target LLM provider name.
//   - model: Target model name.
//
// Outputs:
//   - *EgressDecision: A new decision with Timestamp set.
func NewEgressDecision(requestID, sessionID, provider, model string) *EgressDecision {
	return &EgressDecision{
		RequestID: requestID,
		SessionID: sessionID,
		Provider:  provider,
		Model:     model,
		Timestamp: time.Now().UnixMilli(),
	}
}
