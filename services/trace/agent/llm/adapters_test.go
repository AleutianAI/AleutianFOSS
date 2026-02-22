// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package llm

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// =============================================================================
// AnthropicAgentAdapter Tests
// =============================================================================

func TestAnthropicAgentAdapter_Name(t *testing.T) {
	adapter := NewAnthropicAgentAdapter(nil, "claude-sonnet")
	if adapter.Name() != "anthropic" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "anthropic")
	}
}

func TestAnthropicAgentAdapter_Model(t *testing.T) {
	adapter := NewAnthropicAgentAdapter(nil, "claude-sonnet-4-20250514")
	if adapter.Model() != "claude-sonnet-4-20250514" {
		t.Errorf("Model() = %q, want %q", adapter.Model(), "claude-sonnet-4-20250514")
	}
}

func TestAnthropicAgentAdapter_Complete_NilRequest(t *testing.T) {
	adapter := NewAnthropicAgentAdapter(nil, "claude-sonnet")
	resp, err := adapter.Complete(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
	if resp.StopReason != "end" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end")
	}
}

func TestAnthropicAgentAdapter_convertMessages_ToolRole(t *testing.T) {
	adapter := NewAnthropicAgentAdapter(nil, "claude-sonnet")

	request := &Request{
		SystemPrompt: "System prompt",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{
				Role: "tool",
				ToolResults: []ToolCallResult{
					{Content: "result1"},
					{Content: "result2"},
				},
			},
		},
	}

	messages := adapter.convertMessages(request)

	// Should have: system + user + tool-as-user = 3
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}

	// System message
	if messages[0].Role != "system" {
		t.Errorf("messages[0].Role = %q, want %q", messages[0].Role, "system")
	}

	// Tool role should be converted to user
	if messages[2].Role != "user" {
		t.Errorf("messages[2].Role = %q, want %q (tool should map to user)", messages[2].Role, "user")
	}

	// Tool results should be joined
	if messages[2].Content != "result1\nresult2" {
		t.Errorf("messages[2].Content = %q, want %q", messages[2].Content, "result1\nresult2")
	}
}

// =============================================================================
// OpenAIAgentAdapter Tests
// =============================================================================

func TestOpenAIAgentAdapter_Name(t *testing.T) {
	adapter := NewOpenAIAgentAdapter(nil, "gpt-4o")
	if adapter.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "openai")
	}
}

func TestOpenAIAgentAdapter_Model(t *testing.T) {
	adapter := NewOpenAIAgentAdapter(nil, "gpt-4o")
	if adapter.Model() != "gpt-4o" {
		t.Errorf("Model() = %q, want %q", adapter.Model(), "gpt-4o")
	}
}

func TestOpenAIAgentAdapter_Complete_NilRequest(t *testing.T) {
	adapter := NewOpenAIAgentAdapter(nil, "gpt-4o")
	resp, err := adapter.Complete(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
}

func TestOpenAIAgentAdapter_convertMessages(t *testing.T) {
	adapter := NewOpenAIAgentAdapter(nil, "gpt-4o")

	request := &Request{
		SystemPrompt: "Be helpful",
		Messages: []Message{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello!"},
			{Role: "tool", Content: "data", ToolResults: []ToolCallResult{{Content: "tool output"}}},
		},
	}

	messages := adapter.convertMessages(request)

	if len(messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(messages))
	}

	if messages[0].Role != "system" {
		t.Errorf("messages[0].Role = %q, want system", messages[0].Role)
	}
	if messages[3].Role != "user" {
		t.Errorf("messages[3].Role = %q, want user (tool should map to user)", messages[3].Role)
	}
}

// =============================================================================
// GeminiAgentAdapter Tests
// =============================================================================

func TestGeminiAgentAdapter_Name(t *testing.T) {
	adapter := NewGeminiAgentAdapter(nil, "gemini-1.5-flash")
	if adapter.Name() != "gemini" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "gemini")
	}
}

func TestGeminiAgentAdapter_Model(t *testing.T) {
	adapter := NewGeminiAgentAdapter(nil, "gemini-2.0-flash")
	if adapter.Model() != "gemini-2.0-flash" {
		t.Errorf("Model() = %q, want %q", adapter.Model(), "gemini-2.0-flash")
	}
}

func TestGeminiAgentAdapter_Complete_NilRequest(t *testing.T) {
	adapter := NewGeminiAgentAdapter(nil, "gemini-1.5-flash")
	resp, err := adapter.Complete(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "" {
		t.Errorf("Content = %q, want empty", resp.Content)
	}
}

func TestGeminiAgentAdapter_convertMessages(t *testing.T) {
	adapter := NewGeminiAgentAdapter(nil, "gemini-1.5-flash")

	request := &Request{
		Messages: []Message{
			{Role: "user", Content: "Hi"},
			{Role: "tool", Content: "", ToolResults: []ToolCallResult{{Content: "A"}, {Content: "B"}}},
		},
	}

	messages := adapter.convertMessages(request)

	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2 (no system prompt)", len(messages))
	}

	// Tool role → user
	if messages[1].Role != "user" {
		t.Errorf("messages[1].Role = %q, want user", messages[1].Role)
	}
	if messages[1].Content != "A\nB" {
		t.Errorf("messages[1].Content = %q, want %q", messages[1].Content, "A\nB")
	}
}

func TestGeminiAgentAdapter_buildParams(t *testing.T) {
	adapter := NewGeminiAgentAdapter(nil, "gemini-1.5-flash")

	request := &Request{
		MaxTokens:     1000,
		Temperature:   0.7,
		StopSequences: []string{"STOP"},
	}

	params := adapter.buildParams(request)

	if params.Temperature == nil || *params.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", params.Temperature)
	}
	if params.MaxTokens == nil || *params.MaxTokens != 1000 {
		t.Errorf("MaxTokens = %v, want 1000", params.MaxTokens)
	}
	if len(params.Stop) != 1 || params.Stop[0] != "STOP" {
		t.Errorf("Stop = %v, want [STOP]", params.Stop)
	}
}

// =============================================================================
// Cross-adapter consistency tests
// =============================================================================

func TestAllAdapters_convertMessages_NoSystemPrompt(t *testing.T) {
	// All adapters should produce the same number of messages when no system prompt
	request := &Request{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	anthropic := NewAnthropicAgentAdapter(nil, "claude")
	openai := NewOpenAIAgentAdapter(nil, "gpt-4o")
	gemini := NewGeminiAgentAdapter(nil, "gemini")

	aMessages := anthropic.convertMessages(request)
	oMessages := openai.convertMessages(request)
	gMessages := gemini.convertMessages(request)

	if len(aMessages) != 1 {
		t.Errorf("anthropic: len = %d, want 1", len(aMessages))
	}
	if len(oMessages) != 1 {
		t.Errorf("openai: len = %d, want 1", len(oMessages))
	}
	if len(gMessages) != 1 {
		t.Errorf("gemini: len = %d, want 1", len(gMessages))
	}
}

func TestAllAdapters_convertMessages_WithSystemPrompt(t *testing.T) {
	request := &Request{
		SystemPrompt: "Be helpful",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	anthropic := NewAnthropicAgentAdapter(nil, "claude")
	openai := NewOpenAIAgentAdapter(nil, "gpt-4o")
	gemini := NewGeminiAgentAdapter(nil, "gemini")

	aMessages := anthropic.convertMessages(request)
	oMessages := openai.convertMessages(request)
	gMessages := gemini.convertMessages(request)

	// All should have system + user = 2
	if len(aMessages) != 2 {
		t.Errorf("anthropic: len = %d, want 2", len(aMessages))
	}
	if len(oMessages) != 2 {
		t.Errorf("openai: len = %d, want 2", len(oMessages))
	}
	if len(gMessages) != 2 {
		t.Errorf("gemini: len = %d, want 2", len(gMessages))
	}
}

func TestAllAdapters_ToolRoleMapsToUser(t *testing.T) {
	request := &Request{
		Messages: []Message{
			{Role: "tool", Content: "result"},
		},
	}

	adapters := []struct {
		name    string
		convert func(*Request) []datatypes.Message
	}{
		{"anthropic", NewAnthropicAgentAdapter(nil, "claude").convertMessages},
		{"openai", NewOpenAIAgentAdapter(nil, "gpt-4o").convertMessages},
		{"gemini", NewGeminiAgentAdapter(nil, "gemini").convertMessages},
	}

	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			messages := a.convert(request)
			if len(messages) != 1 {
				t.Fatalf("len = %d, want 1", len(messages))
			}
			if messages[0].Role != "user" {
				t.Errorf("Role = %q, want user (tool should map to user)", messages[0].Role)
			}
		})
	}
}
