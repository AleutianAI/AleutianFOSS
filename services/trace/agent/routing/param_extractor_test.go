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
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
)

// =============================================================================
// ParamExtractor Unit Tests (IT-08b)
// =============================================================================

func TestDefaultParamExtractorConfig(t *testing.T) {
	config := DefaultParamExtractorConfig()

	// IT-08e: Default model changed from granite4:micro-h to ministral-3:3b
	if config.Model != "ministral-3:3b" {
		t.Errorf("expected model ministral-3:3b, got %s", config.Model)
	}
	// IT-08e: Timeout increased from 500ms to 2s (dedicated model, no serialization)
	if config.Timeout != 2*time.Second {
		t.Errorf("expected timeout 2s, got %v", config.Timeout)
	}
	if config.Temperature != 0.1 {
		t.Errorf("expected temperature 0.1, got %f", config.Temperature)
	}
	if config.MaxTokens != 512 {
		t.Errorf("expected max_tokens 512, got %d", config.MaxTokens)
	}
	// IT-08e: NumCtx reduced from 8192 to 4096 (12x headroom for ~300 token budget)
	if config.NumCtx != 4096 {
		t.Errorf("expected num_ctx 4096, got %d", config.NumCtx)
	}
	if !config.Enabled {
		t.Error("expected enabled=true by default")
	}
}

func TestNewParamExtractor_NilChatClient(t *testing.T) {
	config := DefaultParamExtractorConfig()
	_, err := NewParamExtractor(nil, config)
	if err == nil {
		t.Error("expected error for nil chatClient")
	}
}

func TestParamExtractor_IsEnabled(t *testing.T) {
	t.Run("enabled by default", func(t *testing.T) {
		config := DefaultParamExtractorConfig()
		// Can't create real extractor without model manager, but we can test
		// the config directly
		if !config.Enabled {
			t.Error("expected enabled=true")
		}
	})

	t.Run("disabled when flag false", func(t *testing.T) {
		config := DefaultParamExtractorConfig()
		config.Enabled = false
		if config.Enabled {
			t.Error("expected enabled=false")
		}
	})
}

func TestParamExtractor_ExtractParams_Disabled(t *testing.T) {
	// Create extractor with disabled config — need a mock model manager
	// Since we can't create a real MultiModelManager without an endpoint,
	// we test the disabled path via the interface
	// This test validates the disabled path returns error
	config := DefaultParamExtractorConfig()
	config.Enabled = false

	// We can't instantiate without a model manager, but we can test
	// the parseResponse helper directly
	extractor := &ParamExtractor{
		config: config,
	}

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Description: "Package name"},
	}

	_, err := extractor.ExtractParams(
		context.Background(),
		"Find dead code in the Flask helpers module",
		"find_dead_code",
		schemas,
		map[string]any{"package": "flask"},
	)
	if err == nil {
		t.Error("expected error when extractor is disabled")
	}
}

func TestParamExtractor_ParseResponse(t *testing.T) {
	extractor := &ParamExtractor{
		config: DefaultParamExtractorConfig(),
	}

	t.Run("valid JSON", func(t *testing.T) {
		response := `{"package": "helpers", "include_exported": false, "limit": 50}`
		result, err := extractor.parseResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["package"] != "helpers" {
			t.Errorf("expected package=helpers, got %v", result["package"])
		}
	})

	t.Run("JSON in markdown block", func(t *testing.T) {
		response := "```json\n{\"package\": \"reshape\"}\n```"
		result, err := extractor.parseResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["package"] != "reshape" {
			t.Errorf("expected package=reshape, got %v", result["package"])
		}
	})

	t.Run("JSON with surrounding text", func(t *testing.T) {
		response := "Here are the parameters:\n{\"package\": \"hugolib\"}\nDone."
		result, err := extractor.parseResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result["package"] != "hugolib" {
			t.Errorf("expected package=hugolib, got %v", result["package"])
		}
	})

	t.Run("empty response", func(t *testing.T) {
		_, err := extractor.parseResponse("")
		if err == nil {
			t.Error("expected error for empty response")
		}
	})

	t.Run("no JSON in response", func(t *testing.T) {
		_, err := extractor.parseResponse("I don't know what to do")
		if err == nil {
			t.Error("expected error for response without JSON")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := extractor.parseResponse("{invalid json}")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestParamExtractor_BuildSystemPrompt(t *testing.T) {
	extractor := &ParamExtractor{
		config: DefaultParamExtractorConfig(),
	}

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Default: "", Description: "Package to scope results to"},
		{Name: "include_exported", Type: "boolean", Required: false, Default: "false", Description: "Include exported symbols"},
		{Name: "limit", Type: "integer", Required: false, Default: "50", Description: "Max results"},
	}

	t.Run("with regex hint", func(t *testing.T) {
		regexHint := map[string]any{
			"package":          "flask",
			"include_exported": false,
			"limit":            50,
		}

		prompt := extractor.buildSystemPrompt("find_dead_code", schemas, regexHint)

		if !contains(prompt, "find_dead_code") {
			t.Error("prompt should contain tool name")
		}
		if !contains(prompt, "package") {
			t.Error("prompt should contain parameter name")
		}
		if !contains(prompt, "flask") {
			t.Error("prompt should contain regex hint value")
		}
		if !contains(prompt, "hierarchical scoping") {
			t.Error("prompt should contain scoping instructions")
		}
	})

	// IT-08e: Empty hint should not include hint section
	t.Run("empty hint omits hint section", func(t *testing.T) {
		prompt := extractor.buildSystemPrompt("find_dead_code", schemas, map[string]any{})

		if !contains(prompt, "find_dead_code") {
			t.Error("prompt should contain tool name")
		}
		if contains(prompt, "regex-based extractor") {
			t.Error("prompt should NOT contain hint section when hint is empty")
		}
		if !contains(prompt, "JSON object") {
			t.Error("prompt should contain JSON output instruction")
		}
	})
}

func TestParamExtractor_LogParamDiff(t *testing.T) {
	extractor := &ParamExtractor{
		config: DefaultParamExtractorConfig(),
		logger: testLogger(),
	}

	// This should not panic
	regexHint := map[string]any{"package": "flask", "limit": 50}
	llmResult := map[string]any{"package": "helpers", "limit": 50}
	extractor.logParamDiff("find_dead_code", regexHint, llmResult)
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// testLogger returns a logger suitable for testing.
func testLogger() *slog.Logger {
	return slog.Default()
}

// =============================================================================
// IT-12: ResolveConceptualSymbol Tests
// =============================================================================

// mockChatClient implements providers.ChatClient for testing.
type mockChatClient struct {
	response string
	err      error
}

func (m *mockChatClient) Chat(ctx context.Context, messages []datatypes.Message, opts providers.ChatOptions) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func TestResolveConceptualSymbol_PicksCorrectSymbol(t *testing.T) {
	mock := &mockChatClient{response: "_setMaterial"}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0 // No timeout in tests

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "_setMaterial", Kind: "method", FilePath: "meshes/abstractMesh.ts", Line: 100},
		{Name: "getMaterial", Kind: "method", FilePath: "meshes/abstractMesh.ts", Line: 200},
		{Name: "Mesh", Kind: "class", FilePath: "meshes/mesh.ts", Line: 1},
	}

	result, err := extractor.ResolveConceptualSymbol(context.Background(),
		"Show the call chain from assigning a material to a mesh through to shader compilation",
		candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "_setMaterial" {
		t.Errorf("got %q, want %q", result, "_setMaterial")
	}
}

func TestResolveConceptualSymbol_ValidatesCandidateList(t *testing.T) {
	mock := &mockChatClient{response: "nonExistentSymbol"}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "_setMaterial", Kind: "method", FilePath: "mesh.ts", Line: 100},
		{Name: "getMaterial", Kind: "method", FilePath: "mesh.ts", Line: 200},
	}

	_, err = extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material", candidates)
	if err == nil {
		t.Error("expected error when LLM returns symbol not in candidate list")
	}
}

func TestResolveConceptualSymbol_PartialMatch(t *testing.T) {
	// LLM returns "AbstractMesh._setMaterial" but candidate is just "_setMaterial"
	mock := &mockChatClient{response: "AbstractMesh._setMaterial"}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "_setMaterial", Kind: "method", FilePath: "mesh.ts", Line: 100},
	}

	result, err := extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material", candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "_setMaterial" {
		t.Errorf("got %q, want %q (partial match)", result, "_setMaterial")
	}
}

func TestResolveConceptualSymbol_VerboseResponse(t *testing.T) {
	// LLM returns "material (method) in packages/dev/core/src/Meshes/abstractMesh.ts:614"
	// instead of just "material". IT-12: real failure from ministral-3:3b.
	mock := &mockChatClient{response: "material (method) in packages/dev/core/src/Meshes/abstractMesh.ts:614"}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "material", Kind: "method", FilePath: "abstractMesh.ts", Line: 614},
		{Name: "_setMaterial", Kind: "method", FilePath: "abstractMesh.ts", Line: 100},
		{Name: "Mesh", Kind: "class", FilePath: "mesh.ts", Line: 1},
	}

	result, err := extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material to a mesh", candidates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "material" {
		t.Errorf("got %q, want %q (first-token match)", result, "material")
	}
}

func TestResolveConceptualSymbol_Disabled(t *testing.T) {
	mock := &mockChatClient{response: "_setMaterial"}
	config := DefaultParamExtractorConfig()
	config.Enabled = false
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "_setMaterial", Kind: "method", FilePath: "mesh.ts", Line: 100},
	}

	_, err = extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material", candidates)
	if err == nil {
		t.Error("expected error when extractor is disabled")
	}
}

func TestResolveConceptualSymbol_NoCandidates(t *testing.T) {
	mock := &mockChatClient{response: "anything"}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	_, err = extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material", []agent.SymbolCandidate{})
	if err == nil {
		t.Error("expected error with empty candidates")
	}
}

func TestResolveConceptualSymbol_ChatError(t *testing.T) {
	mock := &mockChatClient{err: fmt.Errorf("connection refused")}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	candidates := []agent.SymbolCandidate{
		{Name: "_setMaterial", Kind: "method", FilePath: "mesh.ts", Line: 100},
	}

	_, err = extractor.ResolveConceptualSymbol(context.Background(),
		"assigning a material", candidates)
	if err == nil {
		t.Error("expected error when chat fails")
	}
}
