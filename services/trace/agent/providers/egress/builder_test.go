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
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

func defaultTestConfig() *EgressConfig {
	return &EgressConfig{
		Enabled:          true,
		LocalOnly:        false,
		Allowlist:        make(map[string]bool),
		Denylist:         make(map[string]bool),
		ProviderConsent:  map[string]bool{"anthropic": true, "openai": true, "gemini": true},
		AuditEnabled:     false,
		AuditHashContent: false,
		RateLimitsPerMin: map[string]int{"anthropic": 60, "openai": 60, "gemini": 60},
	}
}

func TestEgressGuardBuilder_WrapAgentClient_OllamaPassthrough(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	inner := &mockAgentClient{name: "ollama", model: "granite4:micro-h"}
	wrapped := builder.WrapAgentClient(inner, "ollama", "granite4:micro-h", "sess-1", 0)

	// Ollama should get the raw client back (no wrapping)
	if _, ok := wrapped.(*EgressGuardClient); ok {
		t.Error("ollama should not be wrapped with EgressGuardClient")
	}
	if wrapped != inner {
		t.Error("wrapped client should be identical to inner for ollama")
	}
}

func TestEgressGuardBuilder_WrapAgentClient_CloudWrapped(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	inner := &mockAgentClient{
		name:  "anthropic",
		model: "claude-sonnet-4-20250514",
		response: &agentllm.Response{
			Content:      "response",
			InputTokens:  50,
			OutputTokens: 20,
			Duration:     100 * time.Millisecond,
		},
	}
	wrapped := builder.WrapAgentClient(inner, "anthropic", "claude-sonnet-4-20250514", "sess-1", 0)

	guard, ok := wrapped.(*EgressGuardClient)
	if !ok {
		t.Fatal("cloud provider should be wrapped with EgressGuardClient")
	}

	// Verify it works
	ctx := context.Background()
	resp, err := guard.Complete(ctx, &agentllm.Request{
		Messages: []agentllm.Message{{Role: "user", Content: "test"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "response" {
		t.Errorf("Content = %q, want %q", resp.Content, "response")
	}
}

func TestEgressGuardBuilder_WrapChatClient_OllamaPassthrough(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	inner := &mockChatClient{response: "tool"}
	wrapped := builder.WrapChatClient(inner, "ollama", "granite4:micro-h", "sess-1", "ROUTER", 0)

	if _, ok := wrapped.(*EgressGuardChatClient); ok {
		t.Error("ollama should not be wrapped")
	}
}

func TestEgressGuardBuilder_WrapChatClient_CloudWrapped(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	inner := &mockChatClient{response: "find_hotspots"}
	wrapped := builder.WrapChatClient(inner, "anthropic", "claude-haiku-4-5-20251001", "sess-1", "ROUTER", 0)

	guard, ok := wrapped.(*EgressGuardChatClient)
	if !ok {
		t.Fatal("cloud provider should be wrapped")
	}

	ctx := context.Background()
	resp, err := guard.Chat(ctx, []datatypes.Message{{Role: "user", Content: "test"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "find_hotspots" {
		t.Errorf("response = %q, want %q", resp, "find_hotspots")
	}
}

func TestEgressGuardBuilder_NilClassifier(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	// Should not panic — uses NoOpClassifier
	inner := &mockAgentClient{
		response: &agentllm.Response{Content: "ok"},
	}
	wrapped := builder.WrapAgentClient(inner, "anthropic", "claude-sonnet-4-20250514", "sess-1", 0)

	ctx := context.Background()
	_, err := wrapped.Complete(ctx, &agentllm.Request{})
	if err != nil {
		t.Fatalf("unexpected error with nil classifier: %v", err)
	}
}

func TestEgressGuardBuilder_ControlPlane(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	cp := builder.ControlPlane()
	if cp == nil {
		t.Fatal("ControlPlane() should not return nil")
	}

	enabled, _ := cp.IsEnabled("anthropic")
	if !enabled {
		t.Error("anthropic should be enabled by default")
	}
}

func TestEgressGuardBuilder_CostEstimator(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.CostLimitCents = 100
	builder := NewEgressGuardBuilder(cfg, nil)

	ce := builder.CostEstimator()
	if ce == nil {
		t.Fatal("CostEstimator() should not return nil")
	}
}

func TestEgressGuardBuilder_TokenBudgetPerSession(t *testing.T) {
	cfg := defaultTestConfig()
	builder := NewEgressGuardBuilder(cfg, nil)

	inner := &mockAgentClient{
		response: &agentllm.Response{Content: "ok", InputTokens: 50, OutputTokens: 20},
	}

	// Two wraps should get independent token budgets
	wrap1 := builder.WrapAgentClient(inner, "anthropic", "claude-sonnet-4-20250514", "sess-1", 1000)
	wrap2 := builder.WrapAgentClient(inner, "anthropic", "claude-sonnet-4-20250514", "sess-2", 1000)

	guard1 := wrap1.(*EgressGuardClient)
	guard2 := wrap2.(*EgressGuardClient)

	if guard1.tokenBudget == guard2.tokenBudget {
		t.Error("each wrap should get an independent token budget")
	}
}
