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
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// TokenBudget tracks token consumption against a per-role budget (P-7).
//
// Description:
//
//	Enforces a maximum token budget for a session role. The budget check
//	happens before the API call with an estimated token count, and the
//	actual usage is recorded after the call completes.
//
//	A limit of 0 means unlimited (no enforcement).
//
// Thread Safety: Safe for concurrent use via sync.Mutex.
type TokenBudget struct {
	mu       sync.Mutex
	role     string
	limit    int
	consumed int
}

// NewTokenBudget creates a new token budget for a role.
//
// Inputs:
//   - role: The role name (e.g., "MAIN", "ROUTER", "PARAM").
//   - limit: Maximum tokens allowed. 0 means unlimited.
//
// Outputs:
//   - *TokenBudget: Configured budget tracker.
func NewTokenBudget(role string, limit int) *TokenBudget {
	return &TokenBudget{
		role:  role,
		limit: limit,
	}
}

// CanSpend checks whether the estimated token count fits within the budget.
//
// Inputs:
//   - estimated: The estimated number of tokens for the upcoming request.
//
// Outputs:
//   - bool: True if the request fits within the remaining budget.
//   - int: Remaining tokens after this request would complete.
func (b *TokenBudget) CanSpend(estimated int) (bool, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit == 0 {
		return true, 0 // unlimited
	}

	remaining := b.limit - b.consumed
	if estimated > remaining {
		return false, remaining
	}

	return true, remaining - estimated
}

// Record records actual token consumption after an API call.
//
// Inputs:
//   - actual: The actual number of tokens consumed.
func (b *TokenBudget) Record(actual int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.consumed += actual
}

// Summary returns a summary of the token budget state for logging.
//
// Outputs:
//   - string: Human-readable summary (e.g., "MAIN: 5000/100000 tokens used").
func (b *TokenBudget) Summary() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit == 0 {
		return fmt.Sprintf("%s: %d tokens used (unlimited)", b.role, b.consumed)
	}
	return fmt.Sprintf("%s: %d/%d tokens used", b.role, b.consumed, b.limit)
}

// Remaining returns the number of tokens remaining in the budget.
//
// Outputs:
//   - int: Remaining tokens. Returns -1 for unlimited budgets.
func (b *TokenBudget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.limit == 0 {
		return -1
	}
	remaining := b.limit - b.consumed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ProviderMetrics tracks per-provider usage metrics within a session (P-7).
//
// Description:
//
//	Accumulates input/output tokens, call counts, errors, and latency
//	for a single provider across a session. Used for end-of-session
//	reporting and cost analysis.
//
// Thread Safety: Safe for concurrent use via sync.Mutex.
type ProviderMetrics struct {
	mu                sync.Mutex
	Provider          string
	InputTokens       int
	OutputTokens      int
	TotalCalls        int
	TotalErrors       int
	TotalLatencyMs    int64
	LastCallTimestamp int64 // Unix milliseconds UTC per CLAUDE.md 5.6
}

// NewProviderMetrics creates a new provider metrics tracker.
//
// Inputs:
//   - provider: The provider name.
//
// Outputs:
//   - *ProviderMetrics: Configured tracker.
func NewProviderMetrics(provider string) *ProviderMetrics {
	return &ProviderMetrics{Provider: provider}
}

// RecordCall records a successful API call.
//
// Inputs:
//   - inputTokens: Number of input tokens.
//   - outputTokens: Number of output tokens.
//   - latency: Call duration.
func (m *ProviderMetrics) RecordCall(inputTokens, outputTokens int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.InputTokens += inputTokens
	m.OutputTokens += outputTokens
	m.TotalCalls++
	m.TotalLatencyMs += latency.Milliseconds()
	m.LastCallTimestamp = time.Now().UnixMilli()
}

// RecordError records a failed API call.
func (m *ProviderMetrics) RecordError() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalErrors++
	m.TotalCalls++
	m.LastCallTimestamp = time.Now().UnixMilli()
}

// LogSummary logs the provider metrics summary.
//
// Inputs:
//   - logger: The logger to use.
func (m *ProviderMetrics) LogSummary(logger *slog.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()

	logger.Info("provider session metrics",
		slog.String("provider", m.Provider),
		slog.Int("input_tokens", m.InputTokens),
		slog.Int("output_tokens", m.OutputTokens),
		slog.Int("total_calls", m.TotalCalls),
		slog.Int("total_errors", m.TotalErrors),
		slog.Int64("total_latency_ms", m.TotalLatencyMs),
	)
}
