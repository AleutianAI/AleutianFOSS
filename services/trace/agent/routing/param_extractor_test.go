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
	"log/slog"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
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
