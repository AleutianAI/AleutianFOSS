// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// =============================================================================
// classifyChatError Tests
// =============================================================================

func TestClassifyChatError(t *testing.T) {
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
			name:     "nil client",
			err:      errors.New("Anthropic client is nil"),
			expected: "nil_client",
		},
		{
			name:     "nil manager",
			err:      errors.New("Ollama model manager is nil"),
			expected: "nil_client",
		},
		{
			name:     "context timeout",
			err:      errors.New("context deadline exceeded"),
			expected: "timeout",
		},
		{
			name:     "401 unauthorized",
			err:      errors.New("API returned 401"),
			expected: "auth",
		},
		{
			name:     "429 rate limit",
			err:      errors.New("API returned 429: Too Many Requests"),
			expected: "rate_limit",
		},
		{
			name:     "500 server error",
			err:      errors.New("API returned 500"),
			expected: "server",
		},
		{
			name:     "unknown error",
			err:      errors.New("some random error"),
			expected: "unknown",
		},
		{
			name:     "authentication failure",
			err:      errors.New("authentication failed for provider"),
			expected: "auth",
		},
		{
			name:     "internal error",
			err:      errors.New("internal error occurred"),
			expected: "server",
		},
		{
			name:     "port number not confused with status code",
			err:      errors.New("connection refused on port 5001"),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyChatError(tt.err)
			if got != tt.expected {
				t.Errorf("classifyChatError(%v) = %q, want %q", tt.err, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// recordChatMetrics Tests
// =============================================================================

func TestRecordChatMetrics_Success(t *testing.T) {
	// Verify recording metrics on success doesn't panic.
	recordChatMetrics("anthropic", 500*time.Millisecond, nil)
	recordChatMetrics("openai", 1*time.Second, nil)
	recordChatMetrics("gemini", 2*time.Second, nil)
	recordChatMetrics("ollama", 100*time.Millisecond, nil)
}

func TestRecordChatMetrics_Error(t *testing.T) {
	// Verify recording metrics on error doesn't panic.
	recordChatMetrics("anthropic", time.Second, errors.New("context deadline exceeded"))
	recordChatMetrics("openai", time.Second, errors.New("API returned 500"))
	recordChatMetrics("gemini", time.Second, errors.New("some error"))
	recordChatMetrics("ollama", time.Second, errors.New("Ollama model manager is nil"))
}

// =============================================================================
// OTel Span Tests for ChatClient adapters
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

const (
	testAnthropicResp = `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"Hello from mock"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":50,"output_tokens":30}}`
	testOpenAIResp    = `{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello from mock"},"finish_reason":"stop"}],"model":"gpt-4o","usage":{"prompt_tokens":50,"completion_tokens":30}}`
	testGeminiResp    = `{"candidates":[{"content":{"parts":[{"text":"Hello from mock"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":30}}`
)

func TestChat_SpanCreated_AllProviders(t *testing.T) {
	providers := []struct {
		name         string
		createClient func(url string) ChatClient
		mockResp     string
		spanName     string
	}{
		{
			name: "anthropic",
			createClient: func(url string) ChatClient {
				return NewAnthropicChatAdapter(llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", url))
			},
			mockResp: testAnthropicResp,
			spanName: "providers.AnthropicChatAdapter.Chat",
		},
		{
			name: "openai",
			createClient: func(url string) ChatClient {
				return NewOpenAIChatAdapter(llm.NewOpenAIClientWithConfig("test-key", "gpt-4o", url))
			},
			mockResp: testOpenAIResp,
			spanName: "providers.OpenAIChatAdapter.Chat",
		},
		{
			name: "gemini",
			createClient: func(url string) ChatClient {
				return NewGeminiChatAdapter(llm.NewGeminiClientWithConfig("test-key", "gemini-1.5-flash", url))
			},
			mockResp: testGeminiResp,
			spanName: "providers.GeminiChatAdapter.Chat",
		},
	}

	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			exporter := setupTestTracer(t)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, p.mockResp)
			}))
			defer server.Close()

			client := p.createClient(server.URL)
			messages := []datatypes.Message{{Role: "user", Content: "Hello"}}

			result, err := client.Chat(context.Background(), messages, ChatOptions{Temperature: 0.7})
			if err != nil {
				t.Fatalf("Chat() error: %v", err)
			}
			if result != "Hello from mock" {
				t.Errorf("result = %q, want %q", result, "Hello from mock")
			}

			// Verify span was created
			spans := exporter.GetSpans()
			foundSpan := false
			for _, s := range spans {
				if s.Name == p.spanName {
					foundSpan = true
					// Verify provider attribute
					for _, attr := range s.Attributes {
						if string(attr.Key) == "provider" && attr.Value.AsString() != p.name {
							t.Errorf("span provider = %q, want %q", attr.Value.AsString(), p.name)
						}
					}
				}
			}
			if !foundSpan {
				t.Errorf("span %q not found in %d spans", p.spanName, len(spans))
			}
		})
	}
}

func TestChat_SpanRecordsError(t *testing.T) {
	exporter := setupTestTracer(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"server error"}`)
	}))
	defer server.Close()

	client := NewAnthropicChatAdapter(llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL))
	messages := []datatypes.Message{{Role: "user", Content: "Hello"}}

	_, err := client.Chat(context.Background(), messages, ChatOptions{Temperature: 0.7})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	// Verify span was created and has error status
	spans := exporter.GetSpans()
	foundSpan := false
	for _, s := range spans {
		if s.Name == "providers.AnthropicChatAdapter.Chat" {
			foundSpan = true
			if s.Status.Code != codes.Error {
				t.Errorf("span status = %v, want %v", s.Status.Code, codes.Error)
			}
		}
	}
	if !foundSpan {
		t.Error("error span not found")
	}
}

func TestChat_MetricsRecorded(t *testing.T) {
	// Verify metrics are recorded without panic on both success and error paths.
	// We can't easily inspect Prometheus counters in unit tests without a custom registry,
	// but we verify the code path doesn't panic.

	server, _ := chatMockServer(t, http.StatusOK, testAnthropicResp)
	defer server.Close()

	client := NewAnthropicChatAdapter(llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", server.URL))
	messages := []datatypes.Message{{Role: "user", Content: "Hello"}}

	// Success path
	_, err := client.Chat(context.Background(), messages, ChatOptions{Temperature: 0.7})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	// Error path
	errorServer, _ := chatMockServer(t, http.StatusInternalServerError, `{"error":"fail"}`)
	defer errorServer.Close()

	errorClient := NewAnthropicChatAdapter(llm.NewAnthropicClientWithConfig("test-key", "claude-sonnet-4-20250514", errorServer.URL))
	_, _ = errorClient.Chat(context.Background(), messages, ChatOptions{Temperature: 0.7})
}
