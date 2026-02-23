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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// =============================================================================
// classifyError Tests
// =============================================================================

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "empty response error",
			err:      &EmptyResponseError{Duration: time.Second, MessageCount: 5, Model: "test"},
			expected: "empty_response",
		},
		{
			name:     "context deadline exceeded",
			err:      errors.New("context deadline exceeded"),
			expected: "timeout",
		},
		{
			name:     "context canceled",
			err:      errors.New("context canceled"),
			expected: "timeout",
		},
		{
			name:     "timeout in message",
			err:      errors.New("request timeout after 30s"),
			expected: "timeout",
		},
		{
			name:     "401 unauthorized",
			err:      errors.New("API returned 401: Unauthorized"),
			expected: "auth",
		},
		{
			name:     "403 forbidden",
			err:      errors.New("API returned 403"),
			expected: "auth",
		},
		{
			name:     "api key invalid",
			err:      errors.New("invalid api key"),
			expected: "auth",
		},
		{
			name:     "429 rate limit",
			err:      errors.New("API returned 429: Too Many Requests"),
			expected: "rate_limit",
		},
		{
			name:     "rate limit message",
			err:      errors.New("rate limit exceeded"),
			expected: "rate_limit",
		},
		{
			name:     "500 server error",
			err:      errors.New("API returned 500"),
			expected: "server",
		},
		{
			name:     "502 bad gateway",
			err:      errors.New("API returned 502: Bad Gateway"),
			expected: "server",
		},
		{
			name:     "503 service unavailable",
			err:      errors.New("API returned 503"),
			expected: "server",
		},
		{
			name:     "internal error message",
			err:      errors.New("internal error occurred"),
			expected: "server",
		},
		{
			name:     "unknown error",
			err:      errors.New("something completely unexpected happened"),
			expected: "unknown",
		},
		{
			name:     "port number not confused with status code",
			err:      errors.New("connection refused on port 5001"),
			expected: "unknown",
		},
		{
			name:     "timeout with large number not confused with status code",
			err:      errors.New("timeout after 5000ms"),
			expected: "timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.expected {
				t.Errorf("classifyError(%v) = %q, want %q", tt.err, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// recordLLMMetrics Tests
// =============================================================================

func TestRecordLLMMetrics_Success(t *testing.T) {
	// This test verifies that recording metrics does not panic on success path.
	// Prometheus counters are auto-registered via promauto, so we just verify no panic.
	recordLLMMetrics("anthropic", 2*time.Second, 100, 50, nil)
	recordLLMMetrics("openai", 1*time.Second, 200, 100, nil)
	recordLLMMetrics("gemini", 500*time.Millisecond, 150, 75, nil)
	recordLLMMetrics("ollama", 3*time.Second, 300, 200, nil)
}

func TestRecordLLMMetrics_Error(t *testing.T) {
	// Verify error path doesn't panic and records error type.
	recordLLMMetrics("anthropic", time.Second, 0, 0, errors.New("context deadline exceeded"))
	recordLLMMetrics("openai", time.Second, 0, 0, errors.New("API returned 429"))
	recordLLMMetrics("gemini", time.Second, 0, 0, &EmptyResponseError{})
	recordLLMMetrics("ollama", time.Second, 0, 0, errors.New("unknown failure"))
}

// =============================================================================
// Active requests gauge Tests
// =============================================================================

func TestActiveRequests_IncDec(t *testing.T) {
	// Verify inc/dec don't panic
	incActiveRequests("anthropic")
	decActiveRequests("anthropic")

	incActiveRequests("ollama")
	incActiveRequests("ollama")
	decActiveRequests("ollama")
	decActiveRequests("ollama")
}

// =============================================================================
// OTel Span Tests (using test exporter)
// =============================================================================

func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

func TestComplete_SpanCreated_Anthropic(t *testing.T) {
	exporter := setupTestTracer(t)

	// Create a mock Anthropic server that returns a valid response
	anthropicResp := `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"Hello from test"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":30}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, anthropicResp)
	}))
	defer server.Close()

	client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// Verify response has TraceStep
	if resp.TraceStep == nil {
		t.Fatal("Response.TraceStep is nil, expected non-nil")
	}
	if resp.TraceStep.Action != "provider_call" {
		t.Errorf("TraceStep.Action = %q, want %q", resp.TraceStep.Action, "provider_call")
	}
	if resp.TraceStep.Tool != "AnthropicAdapter" {
		t.Errorf("TraceStep.Tool = %q, want %q", resp.TraceStep.Tool, "AnthropicAdapter")
	}
	if resp.TraceStep.Target != "claude-sonnet-4-20250514" {
		t.Errorf("TraceStep.Target = %q, want %q", resp.TraceStep.Target, "claude-sonnet-4-20250514")
	}
	if resp.TraceStep.Metadata["provider"] != "anthropic" {
		t.Errorf("TraceStep.Metadata[provider] = %q, want %q", resp.TraceStep.Metadata["provider"], "anthropic")
	}

	// Verify OTel span was created
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}

	foundSpan := false
	for _, s := range spans {
		if s.Name == "agent.llm.AnthropicAdapter.Complete" {
			foundSpan = true
			// Check attributes
			attrs := make(map[string]string)
			for _, a := range s.Attributes {
				attrs[string(a.Key)] = a.Value.Emit()
			}
			if attrs["provider"] != "anthropic" {
				t.Errorf("span provider = %q, want %q", attrs["provider"], "anthropic")
			}
			if attrs["model"] != "claude-sonnet-4-20250514" {
				t.Errorf("span model = %q, want %q", attrs["model"], "claude-sonnet-4-20250514")
			}
		}
	}
	if !foundSpan {
		t.Error("span 'agent.llm.AnthropicAdapter.Complete' not found")
	}
}

func TestComplete_SpanCreated_OpenAI(t *testing.T) {
	exporter := setupTestTracer(t)

	openaiResp := `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from test"},"finish_reason":"stop"}],"model":"gpt-4o","usage":{"prompt_tokens":50,"completion_tokens":30}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openaiResp)
	}))
	defer server.Close()

	client := llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", server.URL)
	adapter := NewOpenAIAgentAdapter(client, "gpt-4o")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.TraceStep == nil {
		t.Fatal("Response.TraceStep is nil")
	}
	if resp.TraceStep.Tool != "OpenAIAdapter" {
		t.Errorf("TraceStep.Tool = %q, want %q", resp.TraceStep.Tool, "OpenAIAdapter")
	}

	spans := exporter.GetSpans()
	foundSpan := false
	for _, s := range spans {
		if s.Name == "agent.llm.OpenAIAdapter.Complete" {
			foundSpan = true
		}
	}
	if !foundSpan {
		t.Error("span 'agent.llm.OpenAIAdapter.Complete' not found")
	}
}

func TestComplete_SpanCreated_Gemini(t *testing.T) {
	exporter := setupTestTracer(t)

	geminiResp := `{"candidates":[{"content":{"parts":[{"text":"Hello from test"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, geminiResp)
	}))
	defer server.Close()

	client := llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", server.URL)
	adapter := NewGeminiAgentAdapter(client, "gemini-1.5-flash")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if resp.TraceStep == nil {
		t.Fatal("Response.TraceStep is nil")
	}
	if resp.TraceStep.Tool != "GeminiAdapter" {
		t.Errorf("TraceStep.Tool = %q, want %q", resp.TraceStep.Tool, "GeminiAdapter")
	}

	spans := exporter.GetSpans()
	foundSpan := false
	for _, s := range spans {
		if s.Name == "agent.llm.GeminiAdapter.Complete" {
			foundSpan = true
		}
	}
	if !foundSpan {
		t.Error("span 'agent.llm.GeminiAdapter.Complete' not found")
	}
}

func TestCompleteWithTools_SpanCreated_Anthropic(t *testing.T) {
	exporter := setupTestTracer(t)

	// Mock server that returns a tool call response
	anthropicToolResp := `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"explore_file","input":{"file_path":"main.go"}}],"model":"claude-sonnet-4-20250514","stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":50}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, anthropicToolResp)
	}))
	defer server.Close()

	client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Explore main.go"}},
		MaxTokens: 100,
		Tools: []tools.ToolDefinition{{
			Name:        "explore_file",
			Description: "Explore a file",
			Parameters:  map[string]tools.ParamDef{"file_path": {Type: "string", Required: true}},
		}},
	}

	_, err := adapter.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	spans := exporter.GetSpans()
	foundSpan := false
	for _, s := range spans {
		if s.Name == "agent.llm.AnthropicAdapter.CompleteWithTools" {
			foundSpan = true
		}
	}
	if !foundSpan {
		t.Error("span 'agent.llm.AnthropicAdapter.CompleteWithTools' not found")
	}
}

// =============================================================================
// CRS TraceStep on error path
// =============================================================================

func TestComplete_CRSTraceStep_Error(t *testing.T) {
	_ = setupTestTracer(t)

	// Server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"server error"}`)
	}))
	defer server.Close()

	client := llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL)
	adapter := NewAnthropicAgentAdapter(client, "claude-sonnet-4-20250514")

	request := &Request{
		Messages:  []Message{{Role: "user", Content: "Hello"}},
		MaxTokens: 100,
	}

	resp, err := adapter.Complete(context.Background(), request)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	// On error, response is nil so TraceStep is not set
	if resp != nil {
		t.Error("expected nil response on error")
	}
}

// =============================================================================
// Ollama adapter tests (limited — no NewOllamaClientWithConfig available)
// =============================================================================

func TestOllamaAdapter_NilRequest_NoSpan(t *testing.T) {
	// OllamaAdapter with nil request returns early without creating spans.
	// We can't easily test with a mock server because NewOllamaClient reads env vars.
	// This test verifies the nil-request guard works.
	_ = setupTestTracer(t)

	// Create adapter with nil client — nil request guard fires before client is used
	adapter := &OllamaAdapter{client: nil, model: "test-model"}

	resp, err := adapter.Complete(context.Background(), nil)
	if err != nil {
		t.Fatalf("Complete(nil) error: %v", err)
	}
	if resp.StopReason != "end" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "end")
	}
	// TraceStep should be nil for nil request path
	if resp.TraceStep != nil {
		t.Error("TraceStep should be nil for nil request")
	}
}
