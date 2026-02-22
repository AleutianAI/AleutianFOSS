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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

func TestNewOpenAIClient_MissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewOpenAIClient()
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "openai:") {
		t.Errorf("error should include 'openai:' prefix, got: %s", errMsg)
	}
}

func TestNewOpenAIClient_DefaultModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_MODEL", "")

	client, err := NewOpenAIClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.model != "gpt-4o-mini" {
		t.Errorf("model = %q, want %q", client.model, "gpt-4o-mini")
	}
}

func TestNewOpenAIClient_CustomModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_MODEL", "gpt-4o")

	client, err := NewOpenAIClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.model != "gpt-4o" {
		t.Errorf("model = %q, want %q", client.model, "gpt-4o")
	}
}

func TestOpenAIClient_Chat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-key")
		}

		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.Model != "gpt-4o-mini" {
			t.Errorf("model = %q, want %q", req.Model, "gpt-4o-mini")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "Hello from OpenAI!"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{
		{Role: "user", Content: "Hello"},
	}

	result, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello from OpenAI!" {
		t.Errorf("result = %q, want %q", result, "Hello from OpenAI!")
	}
}

func TestOpenAIClient_Chat_UnknownRoleMappedToUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify the unknown role was mapped to "user"
		for _, msg := range req.Messages {
			if msg.Content == "unknown role content" {
				if msg.Role != "user" {
					t.Errorf("unknown role should be mapped to 'user', got %q", msg.Role)
				}
			}
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "response"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{
		{Role: "user", Content: "normal message"},
		{Role: "tool_result", Content: "unknown role content"},
	}

	result, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "response" {
		t.Errorf("result = %q, want %q", result, "response")
	}
}

func TestOpenAIClient_Chat_KnownRoleMappings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		expectedRoles := map[string]string{
			"system message":    "system",
			"user message":      "user",
			"assistant message": "assistant",
		}
		for _, msg := range req.Messages {
			if expected, ok := expectedRoles[msg.Content]; ok {
				if msg.Role != expected {
					t.Errorf("content %q: role = %q, want %q", msg.Content, msg.Role, expected)
				}
			}
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "OK"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{
		{Role: "system", Content: "system message"},
		{Role: "user", Content: "user message"},
		{Role: "assistant", Content: "assistant message"},
	}

	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAIClient_Chat_ErrorIncludesProviderPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "invalid key", "type": "auth_error"}}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "bad-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "openai:") {
		t.Errorf("error should include 'openai:' prefix, got: %s", errMsg)
	}
}

func TestOpenAIClient_Chat_NoChoicesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "openai:") {
		t.Errorf("error should include 'openai:' prefix, got: %s", err.Error())
	}
}

func TestOpenAIClient_Chat_ModelOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.Model != "gpt-4o" {
			t.Errorf("model = %q, want %q (should be overridden)", req.Model, "gpt-4o")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "using override model"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o-mini",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	params := GenerationParams{ModelOverride: "gpt-4o"}
	result, err := client.Chat(context.Background(), messages, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "using override model" {
		t.Errorf("result = %q, want %q", result, "using override model")
	}
}

func TestOpenAIClient_ChatStream_NotImplemented(t *testing.T) {
	client := &OpenAIClient{model: "test"}
	err := client.ChatStream(context.Background(), nil, GenerationParams{}, nil)
	if err == nil {
		t.Fatal("expected error for unimplemented streaming")
	}
	if !strings.Contains(err.Error(), "openai:") {
		t.Errorf("error should include 'openai:' prefix, got: %s", err.Error())
	}
}

func TestOpenAIClient_ChatWithTools_SingleToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify tools were sent
		if len(req.Tools) != 1 {
			t.Errorf("len(Tools) = %d, want 1", len(req.Tools))
		}
		if req.Tools[0].Function.Name != "read_file" {
			t.Errorf("Tools[0].Function.Name = %q, want %q", req.Tools[0].Function.Name, "read_file")
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message: openaiMessage{
						Role: "assistant",
						ToolCalls: []openaiToolCall{
							{
								ID:   "call_abc123",
								Type: "function",
								Function: openaiCallFunction{
									Name:      "read_file",
									Arguments: `{"path":"/src/main.go"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o",
		baseURL:    server.URL,
	}

	messages := []ChatMessage{
		{Role: "user", Content: "Read main.go"},
	}
	tools := []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read a file",
				Parameters: ToolParameters{
					Type: "object",
					Properties: map[string]ToolParamDef{
						"path": {Type: "string", Description: "File path"},
					},
					Required: []string{"path"},
				},
			},
		},
	}

	result, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "tool_use")
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_abc123" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", result.ToolCalls[0].ID, "call_abc123")
	}
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", result.ToolCalls[0].Name, "read_file")
	}
	if result.ToolCalls[0].ArgumentsString() != `{"path":"/src/main.go"}` {
		t.Errorf("Arguments = %q, want JSON object", result.ToolCalls[0].ArgumentsString())
	}
}

func TestOpenAIClient_ChatWithTools_ParallelToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message: openaiMessage{
						Role: "assistant",
						ToolCalls: []openaiToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: openaiCallFunction{
									Name:      "read_file",
									Arguments: `{"path":"a.go"}`,
								},
							},
							{
								ID:   "call_2",
								Type: "function",
								Function: openaiCallFunction{
									Name:      "read_file",
									Arguments: `{"path":"b.go"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o",
		baseURL:    server.URL,
	}

	result, err := client.ChatWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "Read files"}},
		GenerationParams{},
		[]ToolDef{{Type: "function", Function: ToolFunction{Name: "read_file"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", result.ToolCalls[0].ID, "call_1")
	}
	if result.ToolCalls[1].ID != "call_2" {
		t.Errorf("ToolCalls[1].ID = %q, want %q", result.ToolCalls[1].ID, "call_2")
	}
}

func TestOpenAIClient_ChatWithTools_ToolResultMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify tool result message format
		toolMsg := req.Messages[2]
		if toolMsg.Role != "tool" {
			t.Errorf("tool msg role = %q, want %q", toolMsg.Role, "tool")
		}
		if toolMsg.ToolCallID != "call_abc" {
			t.Errorf("tool msg ToolCallID = %q, want %q", toolMsg.ToolCallID, "call_abc")
		}

		// Verify assistant message with tool calls
		assistantMsg := req.Messages[1]
		if len(assistantMsg.ToolCalls) != 1 {
			t.Errorf("assistant tool_calls count = %d, want 1", len(assistantMsg.ToolCalls))
		}

		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "The file contains..."},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o",
		baseURL:    server.URL,
	}

	messages := []ChatMessage{
		{Role: "user", Content: "Read main.go"},
		{
			Role: "assistant",
			ToolCalls: []ToolCallResponse{
				{
					ID:        "call_abc",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			},
		},
		{
			Role:       "tool",
			Content:    "package main\nfunc main() {}",
			ToolCallID: "call_abc",
		},
	}

	result, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "end" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end")
	}
	if result.Content != "The file contains..." {
		t.Errorf("Content = %q, want %q", result.Content, "The file contains...")
	}
}

func TestOpenAIClient_ChatWithTools_NoToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{
				{
					Message:      openaiMessage{Role: "assistant", Content: "I don't need tools for this."},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &OpenAIClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gpt-4o",
		baseURL:    server.URL,
	}

	result, err := client.ChatWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "Hi"}},
		GenerationParams{},
		[]ToolDef{{Type: "function", Function: ToolFunction{Name: "read_file"}}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "end" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end")
	}
	if len(result.ToolCalls) != 0 {
		t.Errorf("len(ToolCalls) = %d, want 0", len(result.ToolCalls))
	}
}
