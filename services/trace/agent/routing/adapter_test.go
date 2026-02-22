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
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
)

// TestRouterAdapter_SelectTool_TypeConversion tests that types are converted correctly.
func TestRouterAdapter_SelectTool_TypeConversion(t *testing.T) {
	// Create mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "find_symbol", "confidence": 0.9, "reasoning": "Found matching tool"}`,
			},
			"done": true,
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create router and adapter
	config := DefaultRouterConfig()
	config.OllamaEndpoint = server.URL
	config.Timeout = 5 * time.Second

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager(server.URL), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	// Create adapter
	adapter := NewRouterAdapter(router)

	// Test with agent types
	agentTools := []agent.ToolRouterSpec{
		{
			Name:        "find_symbol",
			Description: "Find a symbol by name",
			BestFor:     []string{"finding functions", "finding types"},
			Params:      []string{"name", "kind"},
			Category:    "search",
		},
		{
			Name:        "grep_codebase",
			Description: "Search for text patterns",
			Params:      []string{"pattern"},
			Category:    "search",
		},
	}

	agentContext := &agent.ToolRouterCodeContext{
		Language:    "go",
		Files:       100,
		Symbols:     500,
		CurrentFile: "main.go",
		RecentTools: []string{"read_file"},
	}

	selection, err := adapter.SelectTool(context.Background(), "Where is parseConfig?", agentTools, agentContext)
	if err != nil {
		t.Fatalf("SelectTool failed: %v", err)
	}

	// Verify agent types are returned
	if selection.Tool != "find_symbol" {
		t.Errorf("Tool = %s, want find_symbol", selection.Tool)
	}
	if selection.Confidence < 0.8 {
		t.Errorf("Confidence = %.2f, want >= 0.8", selection.Confidence)
	}
	if selection.Reasoning != "Found matching tool" {
		t.Errorf("Reasoning = %s, want 'Found matching tool'", selection.Reasoning)
	}
}

// TestRouterAdapter_SelectTool_NilContext tests handling of nil context.
func TestRouterAdapter_SelectTool_NilContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "read_file", "confidence": 0.85, "reasoning": "File read request"}`,
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

	adapter := NewRouterAdapter(router)

	agentTools := []agent.ToolRouterSpec{
		{Name: "read_file", Description: "Read file contents"},
	}

	// Call with nil context
	selection, err := adapter.SelectTool(context.Background(), "Show me main.go", agentTools, nil)
	if err != nil {
		t.Fatalf("SelectTool with nil context failed: %v", err)
	}

	if selection.Tool != "read_file" {
		t.Errorf("Tool = %s, want read_file", selection.Tool)
	}
}

// TestRouterAdapter_SelectTool_EmptyBestFor tests handling of empty BestFor slice.
func TestRouterAdapter_SelectTool_EmptyBestFor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": `{"tool": "answer", "confidence": 0.95, "reasoning": "General question"}`,
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

	adapter := NewRouterAdapter(router)

	// Tool with empty BestFor
	agentTools := []agent.ToolRouterSpec{
		{
			Name:        "answer",
			Description: "Provide a direct answer",
			BestFor:     nil, // Empty
			Params:      nil, // Empty
			Category:    "",  // Empty
		},
	}

	selection, err := adapter.SelectTool(context.Background(), "What is Go?", agentTools, nil)
	if err != nil {
		t.Fatalf("SelectTool failed: %v", err)
	}

	if selection.Tool != "answer" {
		t.Errorf("Tool = %s, want answer", selection.Tool)
	}
}

// TestRouterAdapter_Model tests Model() method delegation.
func TestRouterAdapter_Model(t *testing.T) {
	config := DefaultRouterConfig()
	config.Model = "test-model"

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	adapter := NewRouterAdapter(router)

	if adapter.Model() != "test-model" {
		t.Errorf("Model() = %s, want test-model", adapter.Model())
	}
}

// TestRouterAdapter_Close tests Close() method delegation.
func TestRouterAdapter_Close(t *testing.T) {
	config := DefaultRouterConfig()

	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	adapter := NewRouterAdapter(router)

	err = adapter.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

// TestRouterAdapter_ErrorPropagation tests that errors are properly propagated.
func TestRouterAdapter_ErrorPropagation(t *testing.T) {
	// Server that returns invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"model": "granite4:micro-h",
			"message": map[string]interface{}{
				"role":    "assistant",
				"content": "not valid json",
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

	adapter := NewRouterAdapter(router)

	agentTools := []agent.ToolRouterSpec{
		{Name: "test_tool", Description: "Test tool"},
	}

	_, err = adapter.SelectTool(context.Background(), "test query", agentTools, nil)
	if err == nil {
		t.Fatal("Expected error to be propagated")
	}

	// Verify it's a RouterError
	routerErr, ok := err.(*RouterError)
	if !ok {
		t.Fatalf("Expected RouterError, got %T", err)
	}
	if routerErr.Code != ErrCodeParseError {
		t.Errorf("Error code = %s, want %s", routerErr.Code, ErrCodeParseError)
	}
}

// TestRouterAdapter_ImplementsInterface verifies the adapter implements agent.ToolRouter.
func TestRouterAdapter_ImplementsInterface(t *testing.T) {
	config := DefaultRouterConfig()
	chatClient := providers.NewOllamaChatAdapter(llm.NewMultiModelManager("http://localhost:11434"), "")
	router, err := NewGranite4Router(chatClient, config)
	if err != nil {
		t.Fatalf("NewGranite4Router failed: %v", err)
	}

	adapter := NewRouterAdapter(router)

	// This compile-time check verifies the interface is implemented
	var _ agent.ToolRouter = adapter
}
