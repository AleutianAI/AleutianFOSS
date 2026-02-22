// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
)

// TestGranite4Router_SelectTool_Success tests successful tool selection.
func TestGranite4Router_SelectTool_Success(t *testing.T) {
	// Create mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			return
		}

		// Return a successful tool selection response
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "find_symbol", "confidence": 0.95, "reasoning": "Query asks to find a function"}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create router
	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL
	config.Timeout = 5 * time.Second

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	// Test tool selection
	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol by name"},
		{Name: "grep_codebase", Description: "Search for text patterns"},
		{Name: "read_file", Description: "Read file contents"},
	}

	selection, err := router.SelectTool(context.Background(), "Where is the parseConfig function defined?", tools, nil)
	if err != nil {
		t.Fatalf("SelectTool failed: %v", err)
	}

	if selection.Tool != "find_symbol" {
		t.Errorf("Tool = %s, want find_symbol", selection.Tool)
	}
	if selection.Confidence < 0.9 {
		t.Errorf("Confidence = %.2f, want >= 0.9", selection.Confidence)
	}
}

// TestGranite4Router_SelectTool_LowConfidence tests low confidence fallback.
func TestGranite4Router_SelectTool_LowConfidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "find_symbol", "confidence": 0.5, "reasoning": "Not sure"}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL
	config.ConfidenceThreshold = 0.7

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	selection, err := router.SelectTool(context.Background(), "something vague", tools, nil)

	// Should return selection but with error indicating low confidence
	if err == nil {
		t.Fatal("Expected low confidence error")
	}

	routerErr, ok := err.(*RouterError)
	if !ok {
		t.Fatalf("Expected RouterError, got %T", err)
	}
	if routerErr.Code != ErrCodeLowConfidence {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeLowConfidence)
	}

	// Selection should still be returned
	if selection == nil {
		t.Fatal("Expected selection to be returned even with low confidence")
	}
	if selection.Tool != "find_symbol" {
		t.Errorf("Tool = %s, want find_symbol", selection.Tool)
	}
}

// TestGranite4Router_SelectTool_ParseError tests JSON parse error handling.
func TestGranite4Router_SelectTool_ParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": "This is not valid JSON at all",
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	_, err = router.SelectTool(context.Background(), "test query", tools, nil)
	if err == nil {
		t.Fatal("Expected parse error")
	}

	routerErr, ok := err.(*RouterError)
	if !ok {
		t.Fatalf("Expected RouterError, got %T", err)
	}
	if routerErr.Code != ErrCodeParseError {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeParseError)
	}
}

// TestGranite4Router_SelectTool_NoTools tests error when no tools provided.
func TestGranite4Router_SelectTool_NoTools(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	_, err = router.SelectTool(context.Background(), "test query", []ToolSpec{}, nil)
	if err == nil {
		t.Fatal("Expected no tools error")
	}

	routerErr, ok := err.(*RouterError)
	if !ok {
		t.Fatalf("Expected RouterError, got %T", err)
	}
	if routerErr.Code != ErrCodeNoTools {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeNoTools)
	}
}

// TestGranite4Router_SelectTool_Timeout tests timeout handling.
func TestGranite4Router_SelectTool_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(200 * time.Millisecond)
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "find_symbol", "confidence": 0.9}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL
	config.Timeout = 50 * time.Millisecond // Very short timeout

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	_, err = router.SelectTool(context.Background(), "test query", tools, nil)
	if err == nil {
		t.Fatal("Expected timeout error")
	}

	// Should be a timeout error - check for RouterError with timeout code
	routerErr, ok := err.(*RouterError)
	if !ok {
		// Might be wrapped, check message
		if !strings.Contains(strings.ToLower(err.Error()), "timeout") && !strings.Contains(err.Error(), "deadline") {
			t.Errorf("Expected timeout error, got: %v", err)
		}
		return
	}
	if routerErr.Code != ErrCodeTimeout {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeTimeout)
	}
}

// TestGranite4Router_SelectTool_WithCodeContext tests context passing.
func TestGranite4Router_SelectTool_WithCodeContext(t *testing.T) {
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedBody)

		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "find_symbol", "confidence": 0.85, "reasoning": "Go project"}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	codeContext := &CodeContext{
		Language:    "go",
		Files:       42,
		Symbols:     1000,
		CurrentFile: "main.go",
		RecentTools: []string{"read_file", "grep_codebase"},
	}

	_, err = router.SelectTool(context.Background(), "test query", tools, codeContext)
	if err != nil {
		t.Fatalf("SelectTool failed: %v", err)
	}

	// Verify that context was included in the system prompt
	messages, ok := receivedBody["messages"].([]interface{})
	if !ok || len(messages) < 1 {
		t.Fatal("Expected messages in request body")
	}

	systemMsg := messages[0].(map[string]interface{})
	content := systemMsg["content"].(string)

	if !strings.Contains(content, "go") {
		t.Error("Expected system prompt to contain language")
	}
	if !strings.Contains(content, "42") {
		t.Error("Expected system prompt to contain file count")
	}
}

// TestGranite4Router_SelectTool_InvalidToolFallback tests fallback for unknown tool.
func TestGranite4Router_SelectTool_InvalidToolFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Router returns a tool name that doesn't exist
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "nonexistent_tool", "confidence": 0.9, "reasoning": "test"}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL
	config.ConfidenceThreshold = 0.5 // Lower threshold since confidence gets reduced

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
		{Name: "grep_codebase", Description: "Search for text"},
	}

	selection, err := router.SelectTool(context.Background(), "test query", tools, nil)
	if err != nil {
		t.Fatalf("SelectTool failed: %v", err)
	}

	// Should fall back to first tool
	if selection.Tool != "find_symbol" {
		t.Errorf("Tool = %s, want find_symbol (fallback)", selection.Tool)
	}

	// Confidence should be reduced (0.9 * 0.8 = 0.72)
	if selection.Confidence > 0.75 {
		t.Errorf("Confidence = %.2f, expected reduced due to fallback", selection.Confidence)
	}
}

// TestGranite4Router_Model tests Model() method.
func TestGranite4Router_Model(t *testing.T) {
	config := DefaultRouterConfig()
	config.Model = "custom-model"

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	if router.Model() != "custom-model" {
		t.Errorf("Model() = %s, want custom-model", router.Model())
	}
}

// TestGranite4Router_Close tests Close() method.
func TestGranite4Router_Close(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	err = router.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// TestDefaultRouterConfig tests default configuration.
func TestDefaultRouterConfig(t *testing.T) {
	config := DefaultRouterConfig()

	if config.Model != "granite4:micro-h" {
		t.Errorf("Model = %s, want granite4:micro-h", config.Model)
	}
	if config.Timeout != 500*time.Millisecond {
		t.Errorf("Timeout = %v, want 500ms", config.Timeout)
	}
	if config.ConfidenceThreshold != 0.7 {
		t.Errorf("ConfidenceThreshold = %.2f, want 0.7", config.ConfidenceThreshold)
	}
	if config.KeepAlive != "24h" {
		t.Errorf("KeepAlive = %s, want 24h", config.KeepAlive)
	}
	if config.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", config.MaxTokens)
	}
}

// TestNewGranite4Router_NilChatClient tests error on nil chat client.
func TestNewGranite4Router_NilChatClient(t *testing.T) {
	config := DefaultRouterConfig()
	_, err := NewGranite4Router(nil, config)
	if err == nil {
		t.Fatal("Expected error for nil chatClient")
	}
}

// TestParseResponse_MarkdownCodeBlock tests parsing JSON from markdown code blocks.
func TestParseResponse_MarkdownCodeBlock(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	// Test response wrapped in markdown code block
	response := "```json\n{\"tool\": \"find_symbol\", \"confidence\": 0.8, \"reasoning\": \"test\"}\n```"

	selection, err := router.parseResponse(response, tools)
	if err != nil {
		t.Fatalf("parseResponse failed: %v", err)
	}

	if selection.Tool != "find_symbol" {
		t.Errorf("Tool = %s, want find_symbol", selection.Tool)
	}
}

// TestParseResponse_ExtraText tests parsing JSON with surrounding text.
func TestParseResponse_ExtraText(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "grep_codebase", Description: "Search for text"},
	}

	// Test response with extra text before/after JSON
	response := "Based on the query, I would recommend: {\"tool\": \"grep_codebase\", \"confidence\": 0.75, \"reasoning\": \"searching\"} That should work."

	selection, err := router.parseResponse(response, tools)
	if err != nil {
		t.Fatalf("parseResponse failed: %v", err)
	}

	if selection.Tool != "grep_codebase" {
		t.Errorf("Tool = %s, want grep_codebase", selection.Tool)
	}
}

// TestParseResponse_EmptyResponse tests GR-Phase1 empty response handling.
func TestParseResponse_EmptyResponse(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	testCases := []struct {
		name     string
		response string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
		{"newlines only", "\n\n\n"},
		{"tabs and spaces", "\t  \t  "},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := router.parseResponse(tc.response, tools)
			if err == nil {
				t.Fatal("Expected error for empty response")
			}

			routerErr, ok := err.(*RouterError)
			if !ok {
				t.Fatalf("Expected RouterError, got %T", err)
			}
			if routerErr.Code != ErrCodeEmptyResponse {
				t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeEmptyResponse)
			}
			if !routerErr.Retryable {
				t.Error("Expected error to be retryable")
			}
		})
	}
}

// TestGranite4Router_SelectTool_EmptyModelResponse tests handling of empty model response.
func TestGranite4Router_SelectTool_EmptyModelResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Model returns empty content - simulates cold start or context issue
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": "", // Empty response
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	tools := []ToolSpec{
		{Name: "find_symbol", Description: "Find a symbol"},
	}

	_, err = router.SelectTool(context.Background(), "test query", tools, nil)
	if err == nil {
		t.Fatal("Expected error for empty model response")
	}

	routerErr, ok := err.(*RouterError)
	if !ok {
		t.Fatalf("Expected RouterError, got %T: %v", err, err)
	}
	if routerErr.Code != ErrCodeEmptyResponse {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeEmptyResponse)
	}
}
