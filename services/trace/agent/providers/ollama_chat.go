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
	"fmt"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// OllamaChatAdapter wraps MultiModelManager to implement ChatClient.
//
// Description:
//
//	Delegates chat requests to the shared MultiModelManager, which coordinates
//	multiple Ollama models and prevents VRAM thrashing.
//
// Thread Safety: OllamaChatAdapter is safe for concurrent use.
type OllamaChatAdapter struct {
	manager      *llm.MultiModelManager
	defaultModel string
}

// NewOllamaChatAdapter creates a new OllamaChatAdapter.
//
// Description:
//
//	Creates an adapter that delegates to the MultiModelManager for chat.
//	The defaultModel is used as a fallback when ChatOptions.Model is empty.
//
// Inputs:
//   - manager: The MultiModelManager to delegate to. Must not be nil.
//   - defaultModel: Fallback model when ChatOptions.Model is empty. May be empty
//     if the caller always provides a model in ChatOptions.
//
// Outputs:
//   - *OllamaChatAdapter: The configured adapter.
func NewOllamaChatAdapter(manager *llm.MultiModelManager, defaultModel string) *OllamaChatAdapter {
	return &OllamaChatAdapter{manager: manager, defaultModel: defaultModel}
}

// Chat implements ChatClient by delegating to MultiModelManager.Chat.
func (a *OllamaChatAdapter) Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error) {
	if a.manager == nil {
		return "", fmt.Errorf("Ollama model manager is nil")
	}

	temp := float32(opts.Temperature)
	maxTokens := opts.MaxTokens
	numCtx := opts.NumCtx

	params := llm.GenerationParams{
		Temperature:   &temp,
		MaxTokens:     &maxTokens,
		KeepAlive:     opts.KeepAlive,
		ModelOverride: opts.Model,
	}
	if numCtx > 0 {
		params.NumCtx = &numCtx
	}

	model := opts.Model
	if model == "" {
		model = a.defaultModel
	}
	if model == "" {
		return "", fmt.Errorf("model must be specified in ChatOptions or at adapter construction")
	}

	return a.manager.Chat(ctx, model, messages, params)
}

// OllamaLifecycleAdapter wraps MultiModelManager for lifecycle operations.
//
// Thread Safety: OllamaLifecycleAdapter is safe for concurrent use.
type OllamaLifecycleAdapter struct {
	manager *llm.MultiModelManager
}

// NewOllamaLifecycleAdapter creates a new OllamaLifecycleAdapter.
//
// Inputs:
//   - manager: The MultiModelManager to delegate to. Must not be nil.
//
// Outputs:
//   - *OllamaLifecycleAdapter: The configured adapter.
func NewOllamaLifecycleAdapter(manager *llm.MultiModelManager) *OllamaLifecycleAdapter {
	return &OllamaLifecycleAdapter{manager: manager}
}

// WarmModel loads a model into VRAM via MultiModelManager.
func (a *OllamaLifecycleAdapter) WarmModel(ctx context.Context, model string, opts WarmupOptions) error {
	return a.manager.WarmModel(ctx, model, opts.KeepAlive, opts.NumCtx)
}

// UnloadModel unloads a model from VRAM via MultiModelManager.
func (a *OllamaLifecycleAdapter) UnloadModel(ctx context.Context, model string) error {
	return a.manager.UnloadModel(ctx, model)
}

// IsLocal returns true because Ollama manages local GPU resources.
func (a *OllamaLifecycleAdapter) IsLocal() bool {
	return true
}
