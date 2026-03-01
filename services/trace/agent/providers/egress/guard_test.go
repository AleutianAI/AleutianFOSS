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
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

// =============================================================================
// Mock implementations
// =============================================================================

type mockAgentClient struct {
	name     string
	model    string
	response *agentllm.Response
	err      error
}

func (m *mockAgentClient) Complete(_ context.Context, _ *agentllm.Request) (*agentllm.Response, error) {
	return m.response, m.err
}

func (m *mockAgentClient) Name() string  { return m.name }
func (m *mockAgentClient) Model() string { return m.model }

type mockChatClient struct {
	response string
	err      error
}

func (m *mockChatClient) Chat(_ context.Context, _ []datatypes.Message, _ ChatOptions) (string, error) {
	return m.response, m.err
}

// sensitiveClassifier always classifies data as PII.
type sensitiveClassifier struct{}

func (c *sensitiveClassifier) Classify(_ context.Context, _ []byte) DataSensitivity {
	return SensitivityPII
}

// =============================================================================
// Helper to build a guard with defaults
// =============================================================================

func buildTestGuardClient(inner agentllm.Client, opts ...func(*EgressGuardClient)) *EgressGuardClient {
	logger := slog.Default()
	g := &EgressGuardClient{
		inner:         inner,
		controlPlane:  NewProviderControlPlane(true),
		policy:        NewProviderPolicy(nil, nil),
		consent:       NewConsentPolicy(false, map[string]bool{"anthropic": true}),
		classifier:    NewNoOpClassifier(),
		rateLimiter:   NewRateLimiter(map[string]int{"anthropic": 60}),
		tokenBudget:   NewTokenBudget("MAIN", 0),
		costEstimator: NewCostEstimator(0),
		auditor:       NewEgressAuditor(logger, false, false),
		metrics:       NewProviderMetrics("anthropic"),
		provider:      "anthropic",
		model:         "claude-sonnet-4-20250514",
		sessionID:     "test-session",
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

func buildTestGuardChatClient(inner ChatClient, opts ...func(*EgressGuardChatClient)) *EgressGuardChatClient {
	logger := slog.Default()
	g := &EgressGuardChatClient{
		inner:         inner,
		controlPlane:  NewProviderControlPlane(true),
		policy:        NewProviderPolicy(nil, nil),
		consent:       NewConsentPolicy(false, map[string]bool{"anthropic": true}),
		classifier:    NewNoOpClassifier(),
		rateLimiter:   NewRateLimiter(map[string]int{"anthropic": 60}),
		tokenBudget:   NewTokenBudget("ROUTER", 0),
		costEstimator: NewCostEstimator(0),
		auditor:       NewEgressAuditor(logger, false, false),
		metrics:       NewProviderMetrics("anthropic"),
		provider:      "anthropic",
		model:         "claude-haiku-4-5-20251001",
		sessionID:     "test-session",
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// =============================================================================
// EgressGuardClient tests
// =============================================================================

func TestEgressGuardClient_Complete_Success(t *testing.T) {
	inner := &mockAgentClient{
		name:  "anthropic",
		model: "claude-sonnet-4-20250514",
		response: &agentllm.Response{
			Content:      "Hello!",
			InputTokens:  100,
			OutputTokens: 50,
			Duration:     200 * time.Millisecond,
		},
	}

	guard := buildTestGuardClient(inner)
	ctx := context.Background()

	req := &agentllm.Request{
		Messages: []agentllm.Message{{Role: "user", Content: "Hi"}},
	}

	resp, err := guard.Complete(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello!")
	}
}

func TestEgressGuardClient_Complete_KillSwitch(t *testing.T) {
	inner := &mockAgentClient{}

	guard := buildTestGuardClient(inner, func(g *EgressGuardClient) {
		g.controlPlane = NewProviderControlPlane(false) // disabled
	})
	ctx := context.Background()

	_, err := guard.Complete(ctx, &agentllm.Request{})
	if err == nil {
		t.Fatal("expected error from kill switch")
	}
	if !errors.Is(err, ErrProviderDisabled) {
		t.Errorf("error should wrap ErrProviderDisabled, got: %v", err)
	}
}

func TestEgressGuardClient_Complete_PolicyDenied(t *testing.T) {
	inner := &mockAgentClient{}

	guard := buildTestGuardClient(inner, func(g *EgressGuardClient) {
		g.policy = NewProviderPolicy(nil, map[string]bool{"anthropic": true})
	})
	ctx := context.Background()

	_, err := guard.Complete(ctx, &agentllm.Request{})
	if err == nil {
		t.Fatal("expected error from policy")
	}
	if !errors.Is(err, ErrProviderDenied) {
		t.Errorf("error should wrap ErrProviderDenied, got: %v", err)
	}
}

func TestEgressGuardClient_Complete_NoConsent(t *testing.T) {
	inner := &mockAgentClient{}

	guard := buildTestGuardClient(inner, func(g *EgressGuardClient) {
		g.consent = NewConsentPolicy(false, map[string]bool{}) // no consent
	})
	ctx := context.Background()

	_, err := guard.Complete(ctx, &agentllm.Request{})
	if err == nil {
		t.Fatal("expected error from consent")
	}
	if !errors.Is(err, ErrNoConsent) {
		t.Errorf("error should wrap ErrNoConsent, got: %v", err)
	}
}

func TestEgressGuardClient_Complete_SensitiveData(t *testing.T) {
	inner := &mockAgentClient{}

	guard := buildTestGuardClient(inner, func(g *EgressGuardClient) {
		g.classifier = &sensitiveClassifier{}
	})
	ctx := context.Background()

	_, err := guard.Complete(ctx, &agentllm.Request{
		Messages: []agentllm.Message{{Role: "user", Content: "some pii data"}},
	})
	if err == nil {
		t.Fatal("expected error from sensitive data")
	}
	if !errors.Is(err, ErrSensitiveData) {
		t.Errorf("error should wrap ErrSensitiveData, got: %v", err)
	}
}

func TestEgressGuardClient_Complete_BudgetExhausted(t *testing.T) {
	inner := &mockAgentClient{}

	guard := buildTestGuardClient(inner, func(g *EgressGuardClient) {
		g.tokenBudget = NewTokenBudget("MAIN", 10) // very small budget
	})
	ctx := context.Background()

	req := &agentllm.Request{
		Messages: []agentllm.Message{{Role: "user", Content: "A long message that exceeds the tiny budget of 10 tokens which should cause it to fail the budget check"}},
	}

	_, err := guard.Complete(ctx, req)
	if err == nil {
		t.Fatal("expected error from budget")
	}
	if !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Errorf("error should wrap ErrTokenBudgetExhausted, got: %v", err)
	}
}

func TestEgressGuardClient_Complete_InnerError(t *testing.T) {
	inner := &mockAgentClient{
		err: errors.New("api timeout"),
	}

	guard := buildTestGuardClient(inner)
	ctx := context.Background()

	_, err := guard.Complete(ctx, &agentllm.Request{})
	if err == nil {
		t.Fatal("expected error from inner client")
	}
	if err.Error() != "api timeout" {
		t.Errorf("error = %q, want %q", err.Error(), "api timeout")
	}
}

func TestEgressGuardClient_NameModel(t *testing.T) {
	inner := &mockAgentClient{name: "anthropic", model: "claude-sonnet-4-20250514"}
	guard := buildTestGuardClient(inner)

	if guard.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", guard.Name(), "anthropic")
	}
	if guard.Model() != "claude-sonnet-4-20250514" {
		t.Errorf("Model() = %q, want %q", guard.Model(), "claude-sonnet-4-20250514")
	}
}

// =============================================================================
// EgressGuardChatClient tests
// =============================================================================

func TestEgressGuardChatClient_Chat_Success(t *testing.T) {
	inner := &mockChatClient{response: "tool_name"}

	guard := buildTestGuardChatClient(inner)
	ctx := context.Background()

	resp, err := guard.Chat(ctx, []datatypes.Message{{Role: "user", Content: "route this"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "tool_name" {
		t.Errorf("response = %q, want %q", resp, "tool_name")
	}
}

func TestEgressGuardChatClient_Chat_Blocked(t *testing.T) {
	inner := &mockChatClient{}

	guard := buildTestGuardChatClient(inner, func(g *EgressGuardChatClient) {
		g.controlPlane = NewProviderControlPlane(false)
	})
	ctx := context.Background()

	_, err := guard.Chat(ctx, []datatypes.Message{{Role: "user", Content: "test"}}, ChatOptions{})
	if err == nil {
		t.Fatal("expected error from kill switch")
	}
	if !errors.Is(err, ErrProviderDisabled) {
		t.Errorf("error should wrap ErrProviderDisabled, got: %v", err)
	}
}

// =============================================================================
// Interface compliance
// =============================================================================

func TestInterfaceCompliance(t *testing.T) {
	var _ agentllm.Client = (*EgressGuardClient)(nil)
	var _ ChatClient = (*EgressGuardChatClient)(nil)
}

// =============================================================================
// Serialization helpers
// =============================================================================

func TestSerializeAgentRequest(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		data := serializeAgentRequest(nil)
		if data == nil {
			t.Error("nil request should return empty non-nil slice")
		}
		if len(data) != 0 {
			t.Error("nil request should return empty slice")
		}
	})

	t.Run("empty request", func(t *testing.T) {
		data := serializeAgentRequest(&agentllm.Request{})
		if data == nil {
			t.Error("empty request should return non-nil slice")
		}
	})

	t.Run("with messages", func(t *testing.T) {
		req := &agentllm.Request{
			SystemPrompt: "You are helpful.",
			Messages: []agentllm.Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
			},
		}
		data := serializeAgentRequest(req)
		if len(data) == 0 {
			t.Error("should produce non-empty output")
		}
	})
}

func TestSerializeChatMessages(t *testing.T) {
	t.Run("nil messages", func(t *testing.T) {
		data := serializeChatMessages(nil)
		if data == nil {
			t.Error("nil messages should return empty non-nil slice")
		}
		if len(data) != 0 {
			t.Error("nil messages should return empty slice")
		}
	})

	t.Run("with messages", func(t *testing.T) {
		msgs := []datatypes.Message{
			{Role: "user", Content: "Hello"},
		}
		data := serializeChatMessages(msgs)
		if len(data) == 0 {
			t.Error("should produce non-empty output")
		}
	})
}

func TestEgressGuardClient_Complete_NilRequest(t *testing.T) {
	inner := &mockAgentClient{}
	guard := buildTestGuardClient(inner)

	_, err := guard.Complete(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if err.Error() != "egress guard: request must not be nil" {
		t.Errorf("unexpected error message: %v", err)
	}
}
