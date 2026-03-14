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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// =============================================================================
// Layer 1: Gemini Wire Format — gemini_client.go
// =============================================================================

// geminiResponseWithSignature builds a Gemini API response JSON with
// thoughtSignature on function call parts.
func geminiResponseWithSignature(sig string) string {
	return fmt.Sprintf(`{
		"candidates": [{
			"content": {
				"role": "model",
				"parts": [{
					"functionCall": {"name": "find_hotspots", "args": {"depth": 3}},
					"thoughtSignature": %q
				}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
	}`, sig)
}

// geminiResponseNoSignature builds a Gemini 2.5 response without thoughtSignature.
func geminiResponseNoSignature() string {
	return `{
		"candidates": [{
			"content": {
				"role": "model",
				"parts": [{
					"functionCall": {"name": "find_hotspots", "args": {"depth": 3}}
				}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
	}`
}

func TestGeminiChatWithTools_ThoughtSignature_Captured(t *testing.T) {
	server, _ := mockGeminiServer(t, http.StatusOK, geminiResponseWithSignature("gemini3-sig-abc123"))
	defer server.Close()

	client := NewGeminiClientWithConfig("test-key", "gemini-3-flash-preview", server.URL)

	messages := []ChatMessage{
		{Role: "user", Content: "Find hotspots"},
	}
	toolDefs := []ToolDef{{
		Type:     "function",
		Function: ToolFunction{Name: "find_hotspots", Description: "Find hotspots"},
	}}

	result, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, toolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools() error: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ThoughtSignature != "gemini3-sig-abc123" {
		t.Errorf("ThoughtSignature = %q, want %q",
			result.ToolCalls[0].ThoughtSignature, "gemini3-sig-abc123")
	}
}

func TestGeminiChatWithTools_ThoughtSignature_Echoed(t *testing.T) {
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return a text response (no more tool calls)
		fmt.Fprint(w, `{
			"candidates": [{"content": {"role": "model", "parts": [{"text": "Done"}]}, "finishReason": "STOP"}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
		}`)
	}))
	defer server.Close()

	client := NewGeminiClientWithConfig("test-key", "gemini-3-flash-preview", server.URL)

	// Send a conversation that includes a prior assistant turn with tool calls + signature
	messages := []ChatMessage{
		{Role: "user", Content: "Find hotspots"},
		{
			Role: "assistant",
			ToolCalls: []ToolCallResponse{
				{
					ID:               "gemini-call-0",
					Name:             "find_hotspots",
					Arguments:        json.RawMessage(`{"depth":3}`),
					ThoughtSignature: "gemini3-sig-echo-test",
				},
			},
		},
		{
			Role:     "tool",
			ToolName: "find_hotspots",
			Content:  `{"result": "3 hotspots found"}`,
		},
	}

	_, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, nil)
	if err != nil {
		t.Fatalf("ChatWithTools() error: %v", err)
	}

	// Parse the captured request body to verify thoughtSignature was echoed
	var req geminiRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("parsing captured request: %v", err)
	}

	// Find the model content with functionCall parts
	found := false
	for _, content := range req.Contents {
		if content.Role != "model" {
			continue
		}
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				found = true
				if part.ThoughtSignature != "gemini3-sig-echo-test" {
					t.Errorf("echoed ThoughtSignature = %q, want %q",
						part.ThoughtSignature, "gemini3-sig-echo-test")
				}
			}
		}
	}
	if !found {
		t.Error("no functionCall part found in outbound request")
	}
}

func TestGeminiChatWithTools_ThoughtSignature_Empty(t *testing.T) {
	server, _ := mockGeminiServer(t, http.StatusOK, geminiResponseNoSignature())
	defer server.Close()

	client := NewGeminiClientWithConfig("test-key", "gemini-2.5-flash", server.URL)

	messages := []ChatMessage{
		{Role: "user", Content: "Find hotspots"},
	}
	toolDefs := []ToolDef{{
		Type:     "function",
		Function: ToolFunction{Name: "find_hotspots", Description: "Find hotspots"},
	}}

	result, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, toolDefs)
	if err != nil {
		t.Fatalf("ChatWithTools() error: %v", err)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ThoughtSignature != "" {
		t.Errorf("ThoughtSignature = %q, want empty for Gemini 2.5",
			result.ToolCalls[0].ThoughtSignature)
	}
}

func TestGeminiChatWithTools_NoSignature_RenderedAsText_OnGemini3(t *testing.T) {
	// CB-73a: On Gemini 3+ models, assistant messages with tool calls that
	// lack thought signatures (router-originated) should be rendered as text,
	// not as functionCall parts.
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"candidates": [{"content": {"role": "model", "parts": [{"text": "Done"}]}, "finishReason": "STOP"}],
			"usageMetadata": {"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15}
		}`)
	}))
	defer server.Close()

	// Use a Gemini 3 model name → requires thought signatures
	client := NewGeminiClientWithConfig("test-key", "gemini-3-flash-preview", server.URL)

	messages := []ChatMessage{
		{Role: "user", Content: "Find hotspots"},
		{
			Role:    "assistant",
			Content: "I'll use the read_file tool to help answer this.",
			ToolCalls: []ToolCallResponse{
				{
					ID:        "inv-1",
					Name:      "read_file",
					Arguments: json.RawMessage(`{}`),
					// No ThoughtSignature — router-originated
				},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "inv-1",
			ToolName:   "read_file",
			Content:    `file contents here`,
		},
	}

	_, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, nil)
	if err != nil {
		t.Fatalf("ChatWithTools() error: %v", err)
	}

	// Parse the captured request
	var req geminiRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("parsing captured request: %v", err)
	}

	// Verify NO functionCall parts exist (should be text)
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				t.Errorf("found unexpected functionCall part for %q — should be text on Gemini 3+",
					part.FunctionCall.Name)
			}
			if part.FunctionResponse != nil {
				t.Errorf("found unexpected functionResponse part — should be text on Gemini 3+")
			}
		}
	}

	// Verify the tool result appears as text
	foundToolResultText := false
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if part.Text != "" && (contains(part.Text, "read_file") || contains(part.Text, "file contents")) {
				foundToolResultText = true
			}
		}
	}
	if !foundToolResultText {
		t.Error("expected router-originated tool result to appear as text")
	}
}

// contains is a helper for string containment check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGeminiModelRequiresThoughtSignature(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"gemini-3-flash-preview", true},
		{"gemini-3.1-pro-preview", true},
		{"gemini-3.1-flash-lite-preview", true},
		{"gemini-2.5-flash", false},
		{"gemini-2.5-pro", false},
		{"gemini-2.0-flash", false},
		{"gemini-1.5-pro", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := geminiModelRequiresThoughtSignature(tt.model)
			if got != tt.expected {
				t.Errorf("geminiModelRequiresThoughtSignature(%q) = %v, want %v",
					tt.model, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Layer 2: Adapter completeWithTools
// =============================================================================

func TestGeminiAdapter_CompleteWithTools_ThoughtSignature(t *testing.T) {
	server, _ := mockGeminiServer(t, http.StatusOK, geminiResponseWithSignature("gemini3-adapter-sig"))
	defer server.Close()

	client := NewGeminiClientWithConfig("test-key", "gemini-3-flash-preview", server.URL)
	adapter := NewGeminiAgentAdapter(client, "gemini-3-flash-preview")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Find hotspots"}},
		MaxTokens: 1024,
		Tools: []tools.ToolDefinition{{
			Name:        "find_hotspots",
			Description: "Find hotspots",
			Parameters:  map[string]tools.ParamDef{"depth": {Type: "integer", Required: true}},
		}},
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ThoughtSignature != "gemini3-adapter-sig" {
		t.Errorf("ThoughtSignature = %q, want %q",
			resp.ToolCalls[0].ThoughtSignature, "gemini3-adapter-sig")
	}
}

func TestAnthropicAdapter_CompleteWithTools_ThoughtSignature_Empty(t *testing.T) {
	anthropicResp := `{
		"id": "msg_test", "type": "message", "role": "assistant",
		"content": [{"type": "tool_use", "id": "toolu_1", "name": "find_hotspots", "input": {"depth": 3}}],
		"model": "claude-sonnet-4-20250514", "stop_reason": "tool_use",
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`

	server, _ := mockAnthropicServer(t, http.StatusOK, anthropicResp)
	defer server.Close()

	client := NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Find hotspots"}},
		MaxTokens: 1024,
		Tools: []tools.ToolDefinition{{
			Name:        "find_hotspots",
			Description: "Find hotspots",
			Parameters:  map[string]tools.ParamDef{"depth": {Type: "integer", Required: true}},
		}},
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ThoughtSignature != "" {
		t.Errorf("ThoughtSignature = %q, want empty for Anthropic",
			resp.ToolCalls[0].ThoughtSignature)
	}
}

func TestOpenAIAdapter_CompleteWithTools_ThoughtSignature_Empty(t *testing.T) {
	openaiResp := `{
		"id": "chatcmpl-test", "object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"tool_calls": [{
					"id": "call_1", "type": "function",
					"function": {"name": "find_hotspots", "arguments": "{\"depth\":3}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 50}
	}`

	server, _ := mockOpenAIServer(t, http.StatusOK, openaiResp)
	defer server.Close()

	client := NewOpenAIClientWithConfig("test-key", "gpt-4o", server.URL)
	adapter := NewOpenAIAgentAdapter(client, "gpt-4o")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Find hotspots"}},
		MaxTokens: 1024,
		Tools: []tools.ToolDefinition{{
			Name:        "find_hotspots",
			Description: "Find hotspots",
			Parameters:  map[string]tools.ParamDef{"depth": {Type: "integer", Required: true}},
		}},
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ThoughtSignature != "" {
		t.Errorf("ThoughtSignature = %q, want empty for OpenAI",
			resp.ToolCalls[0].ThoughtSignature)
	}
}

// =============================================================================
// Layer 3: ParseToolCalls — additional cases
// =============================================================================

func TestParseToolCalls_ThoughtSignature_Empty(t *testing.T) {
	response := &Response{
		ToolCalls: []ToolCall{
			{ID: "call-1", Name: "find_hotspots", Arguments: `{"depth": 3}`},
			{ID: "call-2", Name: "find_dead_code", Arguments: `{}`},
		},
	}

	invocations := ParseToolCalls(response)
	if len(invocations) != 2 {
		t.Fatalf("len(invocations) = %d, want 2", len(invocations))
	}
	for i, inv := range invocations {
		if inv.ThoughtSignature != "" {
			t.Errorf("invocations[%d].ThoughtSignature = %q, want empty", i, inv.ThoughtSignature)
		}
	}
}

func TestParseToolCalls_ThoughtSignature_Mixed(t *testing.T) {
	response := &Response{
		ToolCalls: []ToolCall{
			{ID: "call-1", Name: "find_hotspots", Arguments: `{}`, ThoughtSignature: "sig-A"},
			{ID: "call-2", Name: "find_dead_code", Arguments: `{}`},
			{ID: "call-3", Name: "Grep", Arguments: `{}`, ThoughtSignature: "sig-C"},
		},
	}

	invocations := ParseToolCalls(response)
	if len(invocations) != 3 {
		t.Fatalf("len(invocations) = %d, want 3", len(invocations))
	}

	expected := []string{"sig-A", "", "sig-C"}
	for i, inv := range invocations {
		if inv.ThoughtSignature != expected[i] {
			t.Errorf("invocations[%d].ThoughtSignature = %q, want %q",
				i, inv.ThoughtSignature, expected[i])
		}
	}
}

// =============================================================================
// Layer 5: BuildRequest — additional cases
// =============================================================================

func TestBuildRequest_ThoughtSignature_Empty(t *testing.T) {
	ctx := &agent.AssembledContext{
		SystemPrompt: "test",
		ToolResults: []agent.ToolResult{
			{
				InvocationID: "inv-1",
				Tool:         "find_hotspots",
				Success:      true,
				Output:       "result",
			},
		},
	}

	request := BuildRequest(ctx, nil, 1024)

	for _, msg := range request.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			if msg.ToolCalls[0].ThoughtSignature != "" {
				t.Errorf("synthetic ToolCall.ThoughtSignature = %q, want empty",
					msg.ToolCalls[0].ThoughtSignature)
			}
		}
	}
}

func TestBuildRequest_MultipleToolResults_ThoughtSignature(t *testing.T) {
	ctx := &agent.AssembledContext{
		SystemPrompt: "test",
		ToolResults: []agent.ToolResult{
			{
				InvocationID:     "inv-1",
				Tool:             "find_hotspots",
				Success:          true,
				Output:           "hotspots",
				ThoughtSignature: "sig-AAA",
			},
			{
				InvocationID:     "inv-2",
				Tool:             "Grep",
				Success:          true,
				Output:           "grep results",
				ThoughtSignature: "sig-BBB",
			},
			{
				InvocationID: "inv-3",
				Tool:         "find_dead_code",
				Success:      true,
				Output:       "dead code",
				// No signature
			},
		},
	}

	request := BuildRequest(ctx, nil, 1024)

	// Collect all synthetic assistant ToolCalls in order
	var signatures []string
	for _, msg := range request.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			signatures = append(signatures, msg.ToolCalls[0].ThoughtSignature)
		}
	}

	expected := []string{"sig-AAA", "sig-BBB", ""}
	if len(signatures) != len(expected) {
		t.Fatalf("found %d synthetic assistant messages, want %d", len(signatures), len(expected))
	}
	for i, sig := range signatures {
		if sig != expected[i] {
			t.Errorf("signatures[%d] = %q, want %q", i, sig, expected[i])
		}
	}
}

// =============================================================================
// Layer 9: Full Round-Trip
// =============================================================================

func TestThoughtSignature_FullRoundTrip(t *testing.T) {
	// Simulate the full lifecycle:
	// 1. Gemini response → ToolCallResponse (with signature)
	// 2. Adapter → ToolCall (with signature)
	// 3. ParseToolCalls → ToolInvocation (with signature)
	// 4. Execute produces ToolResult (with signature)
	// 5. BuildRequest → synthetic ToolCall (with signature)
	// 6. convertToChat → ChatMessage.ToolCalls (with signature)
	// 7. Gemini request echoes the signature

	const originalSig = "gemini3-roundtrip-sig-42"

	// Step 1: Simulate Gemini wire format response parsing
	geminiResp := geminiResponseWithSignature(originalSig)
	server, _ := mockGeminiServer(t, http.StatusOK, geminiResp)
	defer server.Close()

	client := NewGeminiClientWithConfig("test-key", "gemini-3-flash-preview", server.URL)
	toolDefs := []ToolDef{{
		Type:     "function",
		Function: ToolFunction{Name: "find_hotspots", Description: "Find hotspots"},
	}}

	chatResult, err := client.ChatWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "test"}}, GenerationParams{}, toolDefs)
	if err != nil {
		t.Fatalf("Step 1 (ChatWithTools): %v", err)
	}
	if chatResult.ToolCalls[0].ThoughtSignature != originalSig {
		t.Fatalf("Step 1: ThoughtSignature = %q, want %q",
			chatResult.ToolCalls[0].ThoughtSignature, originalSig)
	}

	// Step 2: Adapter converts ToolCallResponse → ToolCall
	agentToolCall := ToolCall{
		ID:               chatResult.ToolCalls[0].ID,
		Name:             chatResult.ToolCalls[0].Name,
		Arguments:        chatResult.ToolCalls[0].ArgumentsString(),
		ThoughtSignature: chatResult.ToolCalls[0].ThoughtSignature,
	}
	if agentToolCall.ThoughtSignature != originalSig {
		t.Fatalf("Step 2: ThoughtSignature = %q, want %q",
			agentToolCall.ThoughtSignature, originalSig)
	}

	// Step 3: ParseToolCalls converts ToolCall → ToolInvocation
	resp := &Response{ToolCalls: []ToolCall{agentToolCall}}
	invocations := ParseToolCalls(resp)
	if invocations[0].ThoughtSignature != originalSig {
		t.Fatalf("Step 3: ThoughtSignature = %q, want %q",
			invocations[0].ThoughtSignature, originalSig)
	}

	// Step 4: Execution produces ToolResult (simulated)
	toolResult := agent.ToolResult{
		InvocationID:     invocations[0].ID,
		Tool:             invocations[0].Tool,
		Success:          true,
		Output:           "3 hotspots found",
		ThoughtSignature: invocations[0].ThoughtSignature,
	}
	if toolResult.ThoughtSignature != originalSig {
		t.Fatalf("Step 4: ThoughtSignature = %q, want %q",
			toolResult.ThoughtSignature, originalSig)
	}

	// Step 5: BuildRequest creates synthetic messages from ToolResult
	assembledCtx := &agent.AssembledContext{
		SystemPrompt: "test system prompt",
		ToolResults:  []agent.ToolResult{toolResult},
	}
	request := BuildRequest(assembledCtx, nil, 1024)

	// Find the synthetic assistant ToolCall
	var syntheticTC *ToolCall
	for _, msg := range request.Messages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			syntheticTC = &msg.ToolCalls[0]
			break
		}
	}
	if syntheticTC == nil {
		t.Fatal("Step 5: no synthetic assistant ToolCall found")
	}
	if syntheticTC.ThoughtSignature != originalSig {
		t.Fatalf("Step 5: ThoughtSignature = %q, want %q",
			syntheticTC.ThoughtSignature, originalSig)
	}

	// Step 6: convertToChat converts Message.ToolCall → ChatMessage.ToolCallResponse
	chatMessages := convertToChat(request)

	var chatTC *ToolCallResponse
	for _, msg := range chatMessages {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			chatTC = &msg.ToolCalls[0]
			break
		}
	}
	if chatTC == nil {
		t.Fatal("Step 6: no ChatMessage ToolCall found")
	}
	if chatTC.ThoughtSignature != originalSig {
		t.Fatalf("Step 6: ThoughtSignature = %q, want %q",
			chatTC.ThoughtSignature, originalSig)
	}

	// Step 7: Verify the signature would be echoed in Gemini request
	// (Already tested by TestGeminiChatWithTools_ThoughtSignature_Echoed,
	// but we can verify the ChatMessage has it ready for the wire format)
	t.Logf("Full round-trip verified: signature %q survived 6 struct conversions", originalSig)
}
