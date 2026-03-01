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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// recordedRequest captures an HTTP request sent to a mock server.
type recordedRequest struct {
	Body    map[string]interface{}
	Headers http.Header
}

// mockAnthropicServer creates an httptest server that responds like the Anthropic API.
// Returns the server and a pointer to the slice of recorded requests.
func mockAnthropicServer(t *testing.T, statusCode int, responseJSON string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var recorded []recordedRequest
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var bodyMap map[string]interface{}
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			t.Errorf("unmarshaling request body: %v", err)
		}
		mu.Lock()
		recorded = append(recorded, recordedRequest{Body: bodyMap, Headers: r.Header.Clone()})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, responseJSON)
	}))

	return server, &recorded
}

// mockOpenAIServer creates an httptest server that responds like the OpenAI API.
func mockOpenAIServer(t *testing.T, statusCode int, responseJSON string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	return mockAnthropicServer(t, statusCode, responseJSON) // Same HTTP mechanics
}

// mockGeminiServer creates an httptest server that responds like the Gemini API.
// Gemini URLs include the model in the path, so this handles any path.
func mockGeminiServer(t *testing.T, statusCode int, responseJSON string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	return mockAnthropicServer(t, statusCode, responseJSON) // Same HTTP mechanics
}

// slowServer creates a server that delays before responding.
func slowServer(t *testing.T, delay time.Duration, statusCode int, responseJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body to avoid broken pipe
		_, _ = io.ReadAll(r.Body)
		select {
		case <-time.After(delay):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			fmt.Fprint(w, responseJSON)
		case <-r.Context().Done():
			// Client cancelled
		}
	}))
}

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

// =============================================================================
// Phase 1: Temperature 0.0 consistency tests
// =============================================================================

func TestAllAdapters_buildParams_TemperatureZero(t *testing.T) {
	// Temperature 0.0 means "most deterministic" and MUST be passed through.
	adapters := []struct {
		name       string
		buildParam func(*Request) llm.GenerationParams
	}{
		{"anthropic", NewAnthropicAgentAdapter(nil, "claude").buildParams},
		{"openai", NewOpenAIAgentAdapter(nil, "gpt-4o").buildParams},
		{"gemini", NewGeminiAgentAdapter(nil, "gemini").buildParams},
	}

	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			request := &Request{Temperature: 0.0}
			params := a.buildParam(request)
			if params.Temperature == nil {
				t.Fatal("Temperature 0.0 was dropped (nil), but should be set to 0.0")
			}
			if *params.Temperature != 0.0 {
				t.Errorf("Temperature = %v, want 0.0", *params.Temperature)
			}
		})
	}
}

func TestAllAdapters_buildParams_NegativeTemperature(t *testing.T) {
	// Negative temperature means "use provider default" — should NOT be included.
	adapters := []struct {
		name       string
		buildParam func(*Request) llm.GenerationParams
	}{
		{"anthropic", NewAnthropicAgentAdapter(nil, "claude").buildParams},
		{"openai", NewOpenAIAgentAdapter(nil, "gpt-4o").buildParams},
		{"gemini", NewGeminiAgentAdapter(nil, "gemini").buildParams},
	}

	for _, a := range adapters {
		t.Run(a.name, func(t *testing.T) {
			request := &Request{Temperature: -1.0}
			params := a.buildParam(request)
			if params.Temperature != nil {
				t.Errorf("Temperature = %v, want nil for negative value", *params.Temperature)
			}
		})
	}
}

// =============================================================================
// Phase 2: Complete() with mock HTTP servers
// =============================================================================

const anthropicMockResponse = `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"Hello from mock"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":30}}`

const openaiMockResponse = `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from mock"},"finish_reason":"stop"}],"model":"gpt-4o","usage":{"prompt_tokens":50,"completion_tokens":30}}`

const geminiMockResponse = `{"candidates":[{"content":{"parts":[{"text":"Hello from mock"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30}}`

func TestAllAdapters_Complete_Success(t *testing.T) {
	providers := []struct {
		name          string
		createAdapter func(url string) Client
		mockResponse  string
	}{
		{
			"anthropic",
			func(url string) Client {
				client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", url)
				return NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")
			},
			anthropicMockResponse,
		},
		{
			"openai",
			func(url string) Client {
				client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", url)
				return NewOpenAIAgentAdapter(client, "gpt-4o")
			},
			openaiMockResponse,
		},
		{
			"gemini",
			func(url string) Client {
				// Gemini URL needs the /models/model:generateContent path appended by the client.
				// The baseURL for GeminiClient is just the prefix before /models/.
				client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", url)
				return NewGeminiAgentAdapter(client, "gemini-1.5-flash")
			},
			geminiMockResponse,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			server, _ := mockAnthropicServer(t, http.StatusOK, p.mockResponse)
			defer server.Close()

			adapter := p.createAdapter(server.URL)

			request := &Request{
				SystemPrompt: "Be helpful",
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
				Temperature: 0.7,
			}

			resp, err := adapter.Complete(context.Background(), request)
			if err != nil {
				t.Fatalf("Complete() error: %v", err)
			}

			if resp.Content != "Hello from mock" {
				t.Errorf("Content = %q, want %q", resp.Content, "Hello from mock")
			}
			if resp.Duration <= 0 {
				t.Errorf("Duration = %v, want > 0", resp.Duration)
			}
			if resp.Model != adapter.Model() {
				t.Errorf("Model = %q, want %q", resp.Model, adapter.Model())
			}
		})
	}
}

func TestAnthropicAdapter_Complete_ErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantInErr  string
	}{
		{
			"401_unauthorized",
			http.StatusUnauthorized,
			`{"type":"error","error":{"type":"authentication_error","message":"Invalid API key"}}`,
			"401",
		},
		{
			"429_rate_limit",
			http.StatusTooManyRequests,
			`{"type":"error","error":{"type":"rate_limit_error","message":"Rate limited"}}`,
			"429",
		},
		{
			"500_server_error",
			http.StatusInternalServerError,
			`{"type":"error","error":{"type":"server_error","message":"Internal error"}}`,
			"500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := mockAnthropicServer(t, tt.statusCode, tt.body)
			defer server.Close()

			client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
			adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

			request := &Request{
				Messages:    []Message{{Role: "user", Content: "Hello"}},
				Temperature: 0.7,
			}

			_, err := adapter.Complete(context.Background(), request)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestOpenAIAdapter_Complete_ErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantInErr  string
	}{
		{"401_unauthorized", http.StatusUnauthorized, `{"error":{"type":"invalid_api_key","message":"Bad key"}}`, "401"},
		{"429_rate_limit", http.StatusTooManyRequests, `{"error":{"type":"rate_limit","message":"Rate limited"}}`, "429"},
		{"500_server_error", http.StatusInternalServerError, `{"error":{"type":"server","message":"Error"}}`, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := mockOpenAIServer(t, tt.statusCode, tt.body)
			defer server.Close()

			client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", server.URL)
			adapter := NewOpenAIAgentAdapter(client, "gpt-4o")

			request := &Request{
				Messages:    []Message{{Role: "user", Content: "Hello"}},
				Temperature: 0.7,
			}

			_, err := adapter.Complete(context.Background(), request)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestGeminiAdapter_Complete_ErrorResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantInErr  string
	}{
		{"401_unauthorized", http.StatusUnauthorized, `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"Bad key"}}`, "401"},
		{"429_rate_limit", http.StatusTooManyRequests, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota"}}`, "429"},
		{"500_server_error", http.StatusInternalServerError, `{"error":{"code":500,"status":"INTERNAL","message":"Error"}}`, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := mockGeminiServer(t, tt.statusCode, tt.body)
			defer server.Close()

			client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", server.URL)
			adapter := NewGeminiAgentAdapter(client, "gemini-1.5-flash")

			request := &Request{
				Messages:    []Message{{Role: "user", Content: "Hello"}},
				Temperature: 0.7,
			}

			_, err := adapter.Complete(context.Background(), request)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestAnthropicAdapter_Complete_EmptyResponse(t *testing.T) {
	// Anthropic client returns its own error when text content is empty,
	// before the adapter's EmptyResponseError check fires.
	emptyResp := `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":""}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":0}}`
	server, _ := mockAnthropicServer(t, http.StatusOK, emptyResp)
	defer server.Close()

	client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	request := &Request{
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: 0.7,
	}

	_, err := adapter.Complete(context.Background(), request)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	// The error comes from the underlying Anthropic client, not EmptyResponseError
	if !strings.Contains(err.Error(), "no text block") && !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want to contain 'no text block' or 'empty'", err.Error())
	}
}

func TestOpenAIAdapter_Complete_EmptyResponse(t *testing.T) {
	emptyResp := `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}],"model":"gpt-4o","usage":{"prompt_tokens":50,"completion_tokens":0}}`
	server, _ := mockOpenAIServer(t, http.StatusOK, emptyResp)
	defer server.Close()

	client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", server.URL)
	adapter := NewOpenAIAgentAdapter(client, "gpt-4o")

	request := &Request{
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: 0.7,
	}

	_, err := adapter.Complete(context.Background(), request)
	if err == nil {
		t.Fatal("expected EmptyResponseError for empty content")
	}
	if !isEmptyResponseError(err) {
		t.Errorf("expected EmptyResponseError, got %T: %v", err, err)
	}
}

func TestGeminiAdapter_Complete_EmptyResponse(t *testing.T) {
	emptyResp := `{"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":0}}`
	server, _ := mockGeminiServer(t, http.StatusOK, emptyResp)
	defer server.Close()

	client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", server.URL)
	adapter := NewGeminiAgentAdapter(client, "gemini-1.5-flash")

	request := &Request{
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: 0.7,
	}

	_, err := adapter.Complete(context.Background(), request)
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

// isEmptyResponseError checks if an error is an EmptyResponseError.
func isEmptyResponseError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*EmptyResponseError)
	return ok
}

// =============================================================================
// Request body verification tests
// =============================================================================

func TestAnthropicAdapter_Complete_RequestBodyVerification(t *testing.T) {
	server, recorded := mockAnthropicServer(t, http.StatusOK, anthropicMockResponse)
	defer server.Close()

	client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	t.Run("temperature_zero_present", func(t *testing.T) {
		request := &Request{
			Messages:    []Message{{Role: "user", Content: "Hello"}},
			Temperature: 0.0,
		}

		_, err := adapter.Complete(context.Background(), request)
		if err != nil {
			t.Fatalf("Complete() error: %v", err)
		}

		// The Anthropic Chat method doesn't directly get temperature from agent adapter's buildParams
		// because the underlying Chat method has its own param handling.
		// But we verify the adapter buildParams produces the correct params.
		params := adapter.buildParams(request)
		if params.Temperature == nil {
			t.Fatal("buildParams dropped Temperature 0.0")
		}
		if *params.Temperature != 0.0 {
			t.Errorf("buildParams Temperature = %v, want 0.0", *params.Temperature)
		}
	})

	t.Run("temperature_0_7_present", func(t *testing.T) {
		request := &Request{
			Messages:    []Message{{Role: "user", Content: "Hello"}},
			Temperature: 0.7,
		}

		params := adapter.buildParams(request)
		if params.Temperature == nil {
			t.Fatal("buildParams dropped Temperature 0.7")
		}
		if *params.Temperature != 0.7 {
			t.Errorf("buildParams Temperature = %v, want 0.7", *params.Temperature)
		}
	})

	t.Run("messages_present_in_request", func(t *testing.T) {
		if len(*recorded) == 0 {
			t.Skip("no recorded requests")
		}
		body := (*recorded)[0].Body
		if _, ok := body["messages"]; !ok {
			t.Error("request body missing 'messages' field")
		}
		if _, ok := body["model"]; !ok {
			t.Error("request body missing 'model' field")
		}
	})

	t.Run("headers_present", func(t *testing.T) {
		if len(*recorded) == 0 {
			t.Skip("no recorded requests")
		}
		headers := (*recorded)[0].Headers
		if headers.Get("x-api-key") != "test-key" {
			t.Errorf("x-api-key = %q, want %q", headers.Get("x-api-key"), "test-key")
		}
		if headers.Get("anthropic-version") == "" {
			t.Error("anthropic-version header missing")
		}
	})
}

func TestOpenAIAdapter_Complete_RequestBodyVerification(t *testing.T) {
	server, recorded := mockOpenAIServer(t, http.StatusOK, openaiMockResponse)
	defer server.Close()

	client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", server.URL)
	adapter := NewOpenAIAgentAdapter(client, "gpt-4o")

	request := &Request{
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: 0.7,
	}

	_, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(*recorded) == 0 {
		t.Fatal("no requests recorded")
	}

	body := (*recorded)[0].Body
	if _, ok := body["messages"]; !ok {
		t.Error("request body missing 'messages' field")
	}

	headers := (*recorded)[0].Headers
	auth := headers.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("Authorization = %q, want Bearer prefix", auth)
	}
}

func TestGeminiAdapter_Complete_RequestBodyVerification(t *testing.T) {
	server, recorded := mockGeminiServer(t, http.StatusOK, geminiMockResponse)
	defer server.Close()

	client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", server.URL)
	adapter := NewGeminiAgentAdapter(client, "gemini-1.5-flash")

	request := &Request{
		Messages:    []Message{{Role: "user", Content: "Hello"}},
		Temperature: 0.7,
	}

	_, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if len(*recorded) == 0 {
		t.Fatal("no requests recorded")
	}

	body := (*recorded)[0].Body
	if _, ok := body["contents"]; !ok {
		t.Error("request body missing 'contents' field")
	}

	headers := (*recorded)[0].Headers
	if headers.Get("x-goog-api-key") != "test-key" {
		t.Errorf("x-goog-api-key = %q, want %q", headers.Get("x-goog-api-key"), "test-key")
	}
}

// =============================================================================
// Phase 4: Context cancellation tests
// =============================================================================

func TestAllAdapters_Complete_ContextCancellation(t *testing.T) {
	providers := []struct {
		name          string
		createAdapter func(url string) Client
		mockResponse  string
	}{
		{
			"anthropic",
			func(url string) Client {
				client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", url)
				return NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")
			},
			anthropicMockResponse,
		},
		{
			"openai",
			func(url string) Client {
				client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", url)
				return NewOpenAIAgentAdapter(client, "gpt-4o")
			},
			openaiMockResponse,
		},
		{
			"gemini",
			func(url string) Client {
				client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", url)
				return NewGeminiAgentAdapter(client, "gemini-1.5-flash")
			},
			geminiMockResponse,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			server := slowServer(t, 5*time.Second, http.StatusOK, p.mockResponse)
			defer server.Close()

			adapter := p.createAdapter(server.URL)

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			request := &Request{
				Messages:    []Message{{Role: "user", Content: "Hello"}},
				Temperature: 0.7,
			}

			start := time.Now()
			_, err := adapter.Complete(ctx, request)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("expected error from context cancellation")
			}

			// Should complete well before the server's 5s delay
			if elapsed > 2*time.Second {
				t.Errorf("took %v, expected < 2s (context should cancel quickly)", elapsed)
			}

			errStr := strings.ToLower(err.Error())
			if !strings.Contains(errStr, "context") && !strings.Contains(errStr, "deadline") && !strings.Contains(errStr, "cancel") {
				t.Errorf("error = %q, expected to mention context/deadline/cancel", err.Error())
			}
		})
	}
}

// =============================================================================
// Phase 5: Concurrent safety tests
// =============================================================================

func TestAllAdapters_Complete_ConcurrentSafety(t *testing.T) {
	providers := []struct {
		name          string
		createAdapter func(url string) Client
		mockResponse  string
	}{
		{
			"anthropic",
			func(url string) Client {
				client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", url)
				return NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")
			},
			anthropicMockResponse,
		},
		{
			"openai",
			func(url string) Client {
				client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", url)
				return NewOpenAIAgentAdapter(client, "gpt-4o")
			},
			openaiMockResponse,
		},
		{
			"gemini",
			func(url string) Client {
				client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", url)
				return NewGeminiAgentAdapter(client, "gemini-1.5-flash")
			},
			geminiMockResponse,
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			server, _ := mockAnthropicServer(t, http.StatusOK, p.mockResponse)
			defer server.Close()

			adapter := p.createAdapter(server.URL)

			const concurrency = 10
			results := make(chan *Response, concurrency)
			errors := make(chan error, concurrency)

			var wg sync.WaitGroup
			wg.Add(concurrency)

			for i := 0; i < concurrency; i++ {
				go func(idx int) {
					defer wg.Done()
					request := &Request{
						Messages:    []Message{{Role: "user", Content: fmt.Sprintf("Hello %d", idx)}},
						Temperature: 0.7,
					}
					resp, err := adapter.Complete(context.Background(), request)
					if err != nil {
						errors <- err
					} else {
						results <- resp
					}
				}(i)
			}

			wg.Wait()
			close(results)
			close(errors)

			var errs []error
			for err := range errors {
				errs = append(errs, err)
			}

			var resps []*Response
			for resp := range results {
				resps = append(resps, resp)
			}

			if len(errs) > 0 {
				t.Errorf("got %d errors out of %d concurrent requests: %v", len(errs), concurrency, errs[0])
			}

			if len(resps) != concurrency {
				t.Errorf("got %d successful responses, want %d", len(resps), concurrency)
			}

			for _, resp := range resps {
				if resp.Content != "Hello from mock" {
					t.Errorf("Content = %q, want %q", resp.Content, "Hello from mock")
				}
			}
		})
	}
}
