// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package providers defines provider-agnostic interfaces and factories for
// LLM backends used by the Trace agent. It enables per-role provider
// configuration (Main, Router, ParamExtractor) so each role can use a
// different provider (Ollama, Anthropic, OpenAI, Gemini).
//
// Thread Safety:
//
//	All interfaces in this package must be implemented as safe for concurrent use.
package providers

import (
	"context"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// ChatClient is the minimal interface used by Router and ParamExtractor.
//
// Description:
//
//	Router and ParamExtractor only need simple chat (no tool calls, no streaming).
//	This minimal interface makes adapters trivial for any provider.
//
// Thread Safety: Implementations must be safe for concurrent use.
type ChatClient interface {
	// Chat sends messages and returns the assistant's response text.
	//
	// Inputs:
	//   - ctx: Context for cancellation and timeout.
	//   - messages: Conversation messages (system, user, assistant).
	//   - opts: Provider-agnostic chat options.
	//
	// Outputs:
	//   - string: The assistant's response text.
	//   - error: Non-nil on failure.
	Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error)
}

// ChatOptions holds provider-agnostic options for a chat request.
//
// Description:
//
//	Contains options common to all providers plus Ollama-specific fields
//	that are ignored by cloud providers.
type ChatOptions struct {
	// Temperature controls randomness (0.0-1.0). Set to 0.0 for most
	// deterministic output. Set to a negative value (e.g., -1) to omit
	// from the request and use the provider's default. The Go zero value
	// (0.0) is treated as an explicit "most deterministic" setting.
	Temperature float64

	// MaxTokens limits the response length.
	MaxTokens int

	// KeepAlive controls model VRAM lifetime (Ollama-specific, ignored by cloud).
	KeepAlive string

	// NumCtx sets the context window size (Ollama-specific, ignored by cloud).
	NumCtx int

	// Model specifies the model for this request. For OllamaChatAdapter, if empty,
	// falls back to the defaultModel set at adapter construction time.
	// For cloud providers, this is typically ignored (model set at client creation).
	Model string
}

// ModelLifecycleManager handles provider-specific model lifecycle operations.
//
// Description:
//
//	Ollama needs explicit warmup (loading model into VRAM) and unload operations.
//	Cloud providers only need an auth check on warmup. The IsLocal() method
//	allows callers to skip the re-warm dance for cloud providers.
//
// Thread Safety: Implementations must be safe for concurrent use.
type ModelLifecycleManager interface {
	// WarmModel pre-loads or validates a model.
	//
	// For Ollama: loads model into VRAM with keep_alive.
	// For cloud: validates API key and connectivity.
	//
	// Inputs:
	//   - ctx: Context for cancellation.
	//   - model: Provider-specific model identifier.
	//   - opts: Warmup options.
	//
	// Outputs:
	//   - error: Non-nil if warmup/validation fails.
	WarmModel(ctx context.Context, model string, opts WarmupOptions) error

	// UnloadModel releases model resources.
	//
	// For Ollama: unloads model from VRAM.
	// For cloud: no-op.
	//
	// Inputs:
	//   - ctx: Context for cancellation.
	//   - model: Provider-specific model identifier.
	//
	// Outputs:
	//   - error: Non-nil if unload fails.
	UnloadModel(ctx context.Context, model string) error

	// IsLocal returns true if the provider manages local GPU resources.
	//
	// When true, the caller should perform the re-warm dance after loading
	// a new model (re-warm main model after loading router model).
	// When false (cloud providers), no re-warm is needed.
	IsLocal() bool
}

// WarmupOptions configures model warmup behavior.
type WarmupOptions struct {
	// KeepAlive controls how long the model stays loaded (Ollama-specific).
	KeepAlive string

	// NumCtx sets the context window size (Ollama-specific).
	NumCtx int
}
