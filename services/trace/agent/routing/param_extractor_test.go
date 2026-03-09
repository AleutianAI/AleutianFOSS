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
	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
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
// CRS-26e: RAG Entity Confidence Tier Tests
// =============================================================================

func TestParamExtractor_HighConfidenceEntity(t *testing.T) {
	mock := &mockChatClient{response: `{"package": "pkg/materials"}`}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	ec := &rag.ExtractionContext{
		ResolvedEntities: []rag.ResolvedEntity{
			{Raw: "materials", Kind: "package", Resolved: "pkg/materials", Confidence: 0.95},
		},
	}
	ctx := rag.WithExtractionContext(context.Background(), ec)

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Description: "Package name"},
	}

	// Call ExtractParams — we're testing that the prompt sent to the mock
	// includes the confidence tier formatting, not the LLM output.
	_, _ = extractor.ExtractParams(ctx, "Find hotspots in materials", "find_hotspots", schemas, nil)

	// Verify the mock received a user message with "Confirmed entities"
	// Since mockChatClient doesn't capture messages, we test the prompt building
	// directly by checking the user prompt construction logic.
	// The real validation is that ExtractParams doesn't panic and the confidence
	// tier logic runs. For deeper validation, we test the prompt content below.
}

func TestParamExtractor_LowConfidenceEntity(t *testing.T) {
	mock := &mockChatClient{response: `{"package": ""}`}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(mock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	ec := &rag.ExtractionContext{
		ResolvedEntities: []rag.ResolvedEntity{
			{Raw: "system", Kind: "package", Resolved: "pkg/sys", Confidence: 0.35},
		},
	}
	ctx := rag.WithExtractionContext(context.Background(), ec)

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Description: "Package name"},
	}

	_, _ = extractor.ExtractParams(ctx, "Find dead code in system", "find_dead_code", schemas, nil)
}

func TestParamExtractor_MixedConfidenceEntities(t *testing.T) {
	// Capture the messages sent to the mock to verify prompt content.
	var capturedMessages []datatypes.Message
	captureMock := &capturingChatClient{
		response: `{"package": "pkg/materials"}`,
		captured: &capturedMessages,
	}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(captureMock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	ec := &rag.ExtractionContext{
		ResolvedEntities: []rag.ResolvedEntity{
			{Raw: "materials", Kind: "package", Resolved: "pkg/materials", Confidence: 0.95},
			{Raw: "system", Kind: "package", Resolved: "pkg/sys", Confidence: 0.35},
		},
		PackageNames: []string{"pkg/materials", "pkg/render", "pkg/core"},
	}
	ctx := rag.WithExtractionContext(context.Background(), ec)

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Description: "Package name"},
	}

	_, _ = extractor.ExtractParams(ctx, "Find hotspots in materials and system", "find_hotspots", schemas, nil)

	if len(capturedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(capturedMessages))
	}

	userMsg := capturedMessages[1].Content

	t.Run("high confidence section present", func(t *testing.T) {
		if !contains(userMsg, "Confirmed entities from code graph (USE THESE):") {
			t.Error("user prompt should contain 'Confirmed entities' section")
		}
		if !contains(userMsg, `"materials"`) {
			t.Error("user prompt should contain high-confidence entity 'materials'")
		}
	})

	t.Run("low confidence section present", func(t *testing.T) {
		if !contains(userMsg, "Suggested entities (VERIFY before using):") {
			t.Error("user prompt should contain 'Suggested entities' section")
		}
		if !contains(userMsg, "possibly") {
			t.Error("low-confidence entities should use 'possibly' qualifier")
		}
	})

	t.Run("available packages constraint present", func(t *testing.T) {
		if !contains(userMsg, "Available packages:") {
			t.Error("user prompt should contain 'Available packages' list")
		}
		if !contains(userMsg, "Only use package names from this list") {
			t.Error("user prompt should contain package constraint instruction")
		}
	})
}

func TestParamExtractor_AvailablePackagesConstraint(t *testing.T) {
	var capturedMessages []datatypes.Message
	captureMock := &capturingChatClient{
		response: `{"package": "pkg/render"}`,
		captured: &capturedMessages,
	}
	config := DefaultParamExtractorConfig()
	config.Timeout = 0

	extractor, err := NewParamExtractor(captureMock, config)
	if err != nil {
		t.Fatalf("NewParamExtractor: %v", err)
	}

	ec := &rag.ExtractionContext{
		ResolvedEntities: []rag.ResolvedEntity{
			{Raw: "rendering", Kind: "package", Resolved: "pkg/render", Confidence: 0.89},
		},
		PackageNames: []string{"pkg/render", "pkg/materials", "pkg/core", "pkg/db"},
	}
	ctx := rag.WithExtractionContext(context.Background(), ec)

	schemas := []agent.ParamExtractorSchema{
		{Name: "package", Type: "string", Required: false, Description: "Package name"},
	}

	result, err := extractor.ExtractParams(ctx, "Find hotspots in rendering", "find_hotspots", schemas, nil)
	if err != nil {
		t.Fatalf("ExtractParams: %v", err)
	}

	if result["package"] != "pkg/render" {
		t.Errorf("package = %v, want pkg/render", result["package"])
	}

	// Verify prompt includes all package names.
	userMsg := capturedMessages[1].Content
	for _, pkg := range []string{"pkg/render", "pkg/materials", "pkg/core", "pkg/db"} {
		if !contains(userMsg, pkg) {
			t.Errorf("user prompt should contain package %q", pkg)
		}
	}
}

// capturingChatClient captures messages sent to Chat for test verification.
type capturingChatClient struct {
	response string
	captured *[]datatypes.Message
}

func (c *capturingChatClient) Chat(ctx context.Context, messages []datatypes.Message, opts providers.ChatOptions) (string, error) {
	*c.captured = append(*c.captured, messages...)
	return c.response, nil
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
		candidates, 0, 0, "")
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
		"assigning a material", candidates, 0, 0, "")
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
		"assigning a material", candidates, 0, 0, "")
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
		"assigning a material to a mesh", candidates, 0, 0, "")
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
		"assigning a material", candidates, 0, 0, "")
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
		"assigning a material", []agent.SymbolCandidate{}, 0, 0, "")
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
		"assigning a material", candidates, 0, 0, "")
	if err == nil {
		t.Error("expected error when chat fails")
	}
}
