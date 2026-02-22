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
	"fmt"
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
)

// ProviderFactory creates the right LLM adapters based on provider configuration.
//
// Description:
//
//	ProviderFactory is the central creation point for all LLM adapters.
//	It creates ChatClient adapters (for Router/ParamExtractor) and agent
//	Client adapters (for Main model) based on per-role ProviderConfig.
//
// Thread Safety: ProviderFactory is safe for concurrent use after construction.
type ProviderFactory struct {
	// ollamaModelManager is the shared MultiModelManager for Ollama models.
	// May be nil if Ollama is not used by any role.
	ollamaModelManager *llm.MultiModelManager

	logger *slog.Logger
}

// NewProviderFactory creates a new ProviderFactory.
//
// Description:
//
//	Creates a factory that can create adapters for any provider.
//	The ollamaModelManager is optional and only needed if any role uses Ollama.
//
// Inputs:
//   - ollamaModelManager: Shared MultiModelManager for Ollama (may be nil).
//
// Outputs:
//   - *ProviderFactory: Configured factory.
func NewProviderFactory(ollamaModelManager *llm.MultiModelManager) *ProviderFactory {
	return &ProviderFactory{
		ollamaModelManager: ollamaModelManager,
		logger:             slog.Default(),
	}
}

// CreateChatClient creates a ChatClient adapter for the given provider config.
//
// Description:
//
//	Creates the appropriate ChatClient adapter based on the provider type.
//	Used for Router and ParamExtractor roles which only need simple chat.
//
// Inputs:
//   - cfg: Provider configuration specifying provider type and model.
//
// Outputs:
//   - ChatClient: The chat adapter for the specified provider.
//   - error: Non-nil if the provider is unsupported or construction fails.
//
// Example:
//
//	client, err := factory.CreateChatClient(ProviderConfig{
//	    Provider: "anthropic",
//	    Model:    "claude-haiku-4-5-20251001",
//	    APIKey:   "sk-ant-...",
//	})
func (f *ProviderFactory) CreateChatClient(cfg ProviderConfig) (ChatClient, error) {
	switch cfg.Provider {
	case ProviderOllama:
		if f.ollamaModelManager == nil {
			return nil, fmt.Errorf("Ollama model manager not available")
		}
		return NewOllamaChatAdapter(f.ollamaModelManager, cfg.Model), nil

	case ProviderAnthropic:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for Anthropic provider")
		}
		client, err := llm.NewAnthropicClient()
		if err != nil {
			return nil, fmt.Errorf("creating Anthropic client: %w", err)
		}
		return NewAnthropicChatAdapter(client), nil

	case ProviderOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for OpenAI provider")
		}
		client, err := llm.NewOpenAIClient()
		if err != nil {
			return nil, fmt.Errorf("creating OpenAI client: %w", err)
		}
		return NewOpenAIChatAdapter(client), nil

	case ProviderGemini:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY required for Gemini provider")
		}
		client, err := llm.NewGeminiClient()
		if err != nil {
			return nil, fmt.Errorf("creating Gemini client: %w", err)
		}
		return NewGeminiChatAdapter(client), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %q (valid: %v)", cfg.Provider, ValidProviders)
	}
}

// CreateAgentClient creates an agent/llm.Client adapter for the given provider config.
//
// Description:
//
//	Creates the appropriate agent Client adapter based on the provider type.
//	Used for the Main model role which needs tool calling support.
//
// Inputs:
//   - cfg: Provider configuration specifying provider type and model.
//
// Outputs:
//   - agentllm.Client: The agent client adapter for the specified provider.
//   - error: Non-nil if the provider is unsupported or construction fails.
//
// Example:
//
//	client, err := factory.CreateAgentClient(ProviderConfig{
//	    Provider: "anthropic",
//	    Model:    "claude-sonnet-4-20250514",
//	    APIKey:   "sk-ant-...",
//	})
func (f *ProviderFactory) CreateAgentClient(cfg ProviderConfig) (agentllm.Client, error) {
	switch cfg.Provider {
	case ProviderOllama:
		if f.ollamaModelManager == nil {
			return nil, fmt.Errorf("Ollama model manager not available (all-cloud configuration?)")
		}
		ollamaClient, err := llm.NewOllamaClient()
		if err != nil {
			return nil, fmt.Errorf("creating Ollama client: %w", err)
		}
		return agentllm.NewOllamaAdapter(ollamaClient, cfg.Model), nil

	case ProviderAnthropic:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for Anthropic provider")
		}
		client, err := llm.NewAnthropicClient()
		if err != nil {
			return nil, fmt.Errorf("creating Anthropic client: %w", err)
		}
		return agentllm.NewAnthropicAgentAdapter(client, cfg.Model), nil

	case ProviderOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for OpenAI provider")
		}
		client, err := llm.NewOpenAIClient()
		if err != nil {
			return nil, fmt.Errorf("creating OpenAI client: %w", err)
		}
		return agentllm.NewOpenAIAgentAdapter(client, cfg.Model), nil

	case ProviderGemini:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY required for Gemini provider")
		}
		client, err := llm.NewGeminiClient()
		if err != nil {
			return nil, fmt.Errorf("creating Gemini client: %w", err)
		}
		return agentllm.NewGeminiAgentAdapter(client, cfg.Model), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %q (valid: %v)", cfg.Provider, ValidProviders)
	}
}

// CreateLifecycleManager creates a ModelLifecycleManager for the given provider.
//
// Description:
//
//	Creates the appropriate lifecycle manager based on provider type.
//	Ollama gets a real lifecycle manager; cloud providers get a no-op manager.
//
// Inputs:
//   - cfg: Provider configuration specifying provider type.
//
// Outputs:
//   - ModelLifecycleManager: The lifecycle manager.
//   - error: Non-nil if construction fails.
func (f *ProviderFactory) CreateLifecycleManager(cfg ProviderConfig) (ModelLifecycleManager, error) {
	switch cfg.Provider {
	case ProviderOllama:
		if f.ollamaModelManager == nil {
			return nil, fmt.Errorf("Ollama model manager not available")
		}
		return NewOllamaLifecycleAdapter(f.ollamaModelManager), nil

	case ProviderAnthropic, ProviderOpenAI, ProviderGemini:
		return NewCloudLifecycleAdapter(cfg.Provider), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %q", cfg.Provider)
	}
}
