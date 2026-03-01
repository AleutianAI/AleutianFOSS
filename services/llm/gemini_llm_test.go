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

func TestNewGeminiClient_MissingAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	_, err := NewGeminiClient()
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestNewGeminiClient_DefaultModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_MODEL", "")

	client, err := NewGeminiClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.model != "gemini-1.5-flash" {
		t.Errorf("model = %q, want %q", client.model, "gemini-1.5-flash")
	}
}

func TestNewGeminiClient_CustomModel(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_MODEL", "gemini-2.0-flash")

	client, err := NewGeminiClient()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.model != "gemini-2.0-flash" {
		t.Errorf("model = %q, want %q", client.model, "gemini-2.0-flash")
	}
}

func TestGeminiClient_Chat_Success(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if len(req.Contents) == 0 {
			t.Error("expected at least one content block")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Return success response
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role: "model",
						Parts: []geminiPart{
							{Text: "Hello, I am Gemini!"},
						},
					},
					FinishReason: "STOP",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{
		{Role: "user", Content: "Hello"},
	}

	result, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, I am Gemini!" {
		t.Errorf("result = %q, want %q", result, "Hello, I am Gemini!")
	}
}

func TestGeminiClient_Chat_WithSystemPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify system instruction was extracted
		if req.SystemInstruction == nil {
			t.Error("expected system instruction to be set")
		} else if len(req.SystemInstruction.Parts) == 0 {
			t.Error("expected system instruction parts")
		} else if req.SystemInstruction.Parts[0].Text != "You are helpful." {
			t.Errorf("system text = %q, want %q", req.SystemInstruction.Parts[0].Text, "You are helpful.")
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{{Text: "OK"}},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
	}

	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeminiClient_Chat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"code": 500, "message": "internal error", "status": "INTERNAL"}}`))
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestGeminiClient_Chat_EmptyCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for empty candidates")
	}
}

func TestGeminiClient_Chat_WithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if req.GenerationConfig == nil {
			t.Error("expected generation config")
		} else {
			if req.GenerationConfig.Temperature == nil || *req.GenerationConfig.Temperature != 0.5 {
				t.Error("expected temperature 0.5")
			}
			if req.GenerationConfig.MaxOutputTokens == nil || *req.GenerationConfig.MaxOutputTokens != 100 {
				t.Error("expected max tokens 100")
			}
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content:      geminiContent{Parts: []geminiPart{{Text: "response"}}},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	temp := float32(0.5)
	maxTokens := 100
	params := GenerationParams{
		Temperature: &temp,
		MaxTokens:   &maxTokens,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeminiClient_Generate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content:      geminiContent{Parts: []geminiPart{{Text: "generated"}}},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	result, err := client.Generate(context.Background(), "prompt", GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "generated" {
		t.Errorf("result = %q, want %q", result, "generated")
	}
}

func TestGeminiClient_ChatStream_NotImplemented(t *testing.T) {
	client := &GeminiClient{
		apiKey: "test-key",
		model:  "gemini-1.5-flash",
	}

	err := client.ChatStream(context.Background(), nil, GenerationParams{}, nil)
	if err == nil {
		t.Fatal("expected error for unimplemented streaming")
	}
}

func TestGeminiClient_Chat_APIKeyInHeaderNotURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify API key is in header, NOT in URL query parameter
		headerKey := r.Header.Get("x-goog-api-key")
		if headerKey != "test-api-key-12345" {
			t.Errorf("x-goog-api-key header = %q, want %q", headerKey, "test-api-key-12345")
		}

		// Verify key is NOT in URL query string
		queryKey := r.URL.Query().Get("key")
		if queryKey != "" {
			t.Errorf("API key found in URL query parameter: %q — this is a security vulnerability (B-1/P-8)", queryKey)
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content:      geminiContent{Parts: []geminiPart{{Text: "OK"}}},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-api-key-12345",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGeminiClient_Chat_ErrorIncludesProviderPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"code": 401, "message": "invalid key", "status": "UNAUTHENTICATED"}}`))
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "bad-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "gemini:") {
		t.Errorf("error message should include 'gemini:' prefix, got: %s", errMsg)
	}
}

func TestGeminiClient_Chat_ErrorBodyRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return error body containing a secret
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": "forbidden for key=AIzaSyAbcDefGhiJklMnoPqrStUvWxYz0123456789extra"}`))
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-1.5-flash",
		baseURL:    server.URL,
	}

	messages := []datatypes.Message{{Role: "user", Content: "Hi"}}
	_, err := client.Chat(context.Background(), messages, GenerationParams{})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "AIzaSy") {
		t.Errorf("error message should not contain raw API key, got: %s", errMsg)
	}
}

func TestGeminiClient_BuildRequest_RoleMapping(t *testing.T) {
	client := &GeminiClient{
		apiKey: "test-key",
		model:  "gemini-1.5-flash",
	}

	messages := []datatypes.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "user", Content: "bye"},
	}

	req := client.buildRequest(messages, GenerationParams{})

	// System should be in systemInstruction
	if req.SystemInstruction == nil {
		t.Fatal("expected systemInstruction")
	}
	if req.SystemInstruction.Parts[0].Text != "sys" {
		t.Errorf("system text = %q, want %q", req.SystemInstruction.Parts[0].Text, "sys")
	}

	// Should have 3 contents (user, assistant=model, user)
	if len(req.Contents) != 3 {
		t.Fatalf("contents len = %d, want 3", len(req.Contents))
	}
	if req.Contents[0].Role != "user" {
		t.Errorf("contents[0].Role = %q, want %q", req.Contents[0].Role, "user")
	}
	if req.Contents[1].Role != "model" {
		t.Errorf("contents[1].Role = %q, want %q (assistant maps to model)", req.Contents[1].Role, "model")
	}
	if req.Contents[2].Role != "user" {
		t.Errorf("contents[2].Role = %q, want %q", req.Contents[2].Role, "user")
	}
}

// =============================================================================
// ChatWithTools Tests
// =============================================================================

func TestGeminiClient_ChatWithTools_FunctionCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify tools were sent
		if len(req.Tools) != 1 {
			t.Errorf("len(Tools) = %d, want 1", len(req.Tools))
		}
		if len(req.Tools[0].FunctionDeclarations) != 1 {
			t.Errorf("len(FunctionDeclarations) = %d, want 1", len(req.Tools[0].FunctionDeclarations))
		}
		if req.Tools[0].FunctionDeclarations[0].Name != "read_file" {
			t.Errorf("tool name = %q, want %q", req.Tools[0].FunctionDeclarations[0].Name, "read_file")
		}

		// Return functionCall response
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role: "model",
						Parts: []geminiPart{
							{FunctionCall: &geminiFunctionCall{
								Name: "read_file",
								Args: map[string]interface{}{"path": "/src/main.go"},
							}},
						},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-2.0-flash",
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
	if result.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", result.ToolCalls[0].Name, "read_file")
	}
}

func TestGeminiClient_ChatWithTools_SyntheticID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role: "model",
						Parts: []geminiPart{
							{FunctionCall: &geminiFunctionCall{
								Name: "tool_a",
								Args: map[string]interface{}{},
							}},
							{FunctionCall: &geminiFunctionCall{
								Name: "tool_b",
								Args: map[string]interface{}{},
							}},
						},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-2.0-flash",
		baseURL:    server.URL,
	}

	result, err := client.ChatWithTools(context.Background(),
		[]ChatMessage{{Role: "user", Content: "Do things"}},
		GenerationParams{}, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}
	// Verify synthetic IDs are generated
	if result.ToolCalls[0].ID != "gemini-call-0" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", result.ToolCalls[0].ID, "gemini-call-0")
	}
	if result.ToolCalls[1].ID != "gemini-call-1" {
		t.Errorf("ToolCalls[1].ID = %q, want %q", result.ToolCalls[1].ID, "gemini-call-1")
	}
}

func TestGeminiClient_ChatWithTools_FunctionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req geminiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify functionResponse was sent
		if len(req.Contents) < 3 {
			t.Errorf("expected at least 3 contents, got %d", len(req.Contents))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		toolContent := req.Contents[2]
		if len(toolContent.Parts) != 1 {
			t.Errorf("tool content parts = %d, want 1", len(toolContent.Parts))
		}
		if toolContent.Parts[0].FunctionResponse == nil {
			t.Error("expected functionResponse in tool content")
		} else {
			if toolContent.Parts[0].FunctionResponse.Name != "read_file" {
				t.Errorf("functionResponse.Name = %q, want %q",
					toolContent.Parts[0].FunctionResponse.Name, "read_file")
			}
		}

		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{{Text: "The file contains Go code."}},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-2.0-flash",
		baseURL:    server.URL,
	}

	messages := []ChatMessage{
		{Role: "user", Content: "Read main.go"},
		{
			Role: "assistant",
			ToolCalls: []ToolCallResponse{
				{
					ID:        "gemini-call-0",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				},
			},
		},
		{
			Role:     "tool",
			Content:  `{"result": "package main"}`,
			ToolName: "read_file",
		},
	}

	result, err := client.ChatWithTools(context.Background(), messages, GenerationParams{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StopReason != "end" {
		t.Errorf("StopReason = %q, want %q", result.StopReason, "end")
	}
	if result.Content != "The file contains Go code." {
		t.Errorf("Content = %q, want expected text", result.Content)
	}
}

func TestGeminiClient_ChatWithTools_NoToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Parts: []geminiPart{{Text: "No tools needed."}},
					},
					FinishReason: "STOP",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := &GeminiClient{
		httpClient: server.Client(),
		apiKey:     "test-key",
		model:      "gemini-2.0-flash",
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
