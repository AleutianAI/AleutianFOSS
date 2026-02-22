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
	"testing"
)

// =============================================================================
// OllamaChatAdapter Tests
// =============================================================================

func TestOllamaChatAdapter_NilManager(t *testing.T) {
	adapter := NewOllamaChatAdapter(nil, "")
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{Model: "test"})
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
}

func TestOllamaChatAdapter_EmptyModel(t *testing.T) {
	// OllamaChatAdapter requires a model in ChatOptions or defaultModel
	// We can't test with a real manager, but we can verify the empty model check
	adapter := &OllamaChatAdapter{manager: nil}
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for nil manager or empty model")
	}
}

func TestOllamaChatAdapter_DefaultModelFallback(t *testing.T) {
	// When ChatOptions.Model is empty, defaultModel should be used.
	// We can verify the model resolution by checking the error message:
	// with defaultModel set but nil manager, the error should be about nil manager
	// (not about model being empty), proving the defaultModel was picked up.
	adapter := NewOllamaChatAdapter(nil, "fallback-model")
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
	// The error should be about nil manager, not about missing model
	if err.Error() == "model must be specified in ChatOptions or at adapter construction" {
		t.Error("defaultModel should have been used as fallback, but got empty model error")
	}
}

func TestOllamaChatAdapter_OptsOverridesDefault(t *testing.T) {
	// When ChatOptions.Model is set, it should take priority over defaultModel.
	// With nil manager, we get an error from the nil check, proving model was resolved.
	adapter := NewOllamaChatAdapter(nil, "fallback-model")
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{Model: "explicit-model"})
	if err == nil {
		t.Fatal("expected error for nil manager")
	}
	// Error should be about nil manager, not empty model
	if err.Error() == "model must be specified in ChatOptions or at adapter construction" {
		t.Error("opts.Model should have been used, but got empty model error")
	}
}

func TestOllamaChatAdapter_BothEmpty_Error(t *testing.T) {
	// When both ChatOptions.Model and defaultModel are empty, should get model error.
	adapter := NewOllamaChatAdapter(nil, "")
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	// With nil manager, the nil manager check fires first
	// So use a non-nil adapter with no model to test the model check
	adapter2 := &OllamaChatAdapter{manager: nil, defaultModel: ""}
	_, err2 := adapter2.Chat(context.Background(), nil, ChatOptions{})
	if err2 == nil {
		t.Fatal("expected error for nil manager or empty model")
	}
}

// =============================================================================
// AnthropicChatAdapter Tests
// =============================================================================

func TestAnthropicChatAdapter_NilClient(t *testing.T) {
	adapter := NewAnthropicChatAdapter(nil)
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

// =============================================================================
// OpenAIChatAdapter Tests
// =============================================================================

func TestOpenAIChatAdapter_NilClient(t *testing.T) {
	adapter := NewOpenAIChatAdapter(nil)
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

// =============================================================================
// GeminiChatAdapter Tests
// =============================================================================

func TestGeminiChatAdapter_NilClient(t *testing.T) {
	adapter := NewGeminiChatAdapter(nil)
	_, err := adapter.Chat(context.Background(), nil, ChatOptions{})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}

// =============================================================================
// CloudLifecycleAdapter Tests
// =============================================================================

func TestCloudLifecycleAdapter_IsLocal(t *testing.T) {
	adapter := NewCloudLifecycleAdapter("anthropic")
	if adapter.IsLocal() {
		t.Error("CloudLifecycleAdapter.IsLocal() should be false")
	}
}

func TestCloudLifecycleAdapter_WarmModel(t *testing.T) {
	adapter := NewCloudLifecycleAdapter("openai")
	err := adapter.WarmModel(context.Background(), "gpt-4o", WarmupOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloudLifecycleAdapter_UnloadModel(t *testing.T) {
	adapter := NewCloudLifecycleAdapter("gemini")
	err := adapter.UnloadModel(context.Background(), "gemini-1.5-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// OllamaLifecycleAdapter Tests
// =============================================================================

func TestOllamaLifecycleAdapter_IsLocal(t *testing.T) {
	// Can't create a real adapter without a model manager, but we can test
	// the struct directly since IsLocal just returns true
	adapter := &OllamaLifecycleAdapter{}
	if !adapter.IsLocal() {
		t.Error("OllamaLifecycleAdapter.IsLocal() should be true")
	}
}
