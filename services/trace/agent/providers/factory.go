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
	"log/slog"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers/egress"
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

	// egressBuilder wraps created clients with egress guard decorators.
	// When nil, clients are returned unwrapped (no egress control).
	egressBuilder *egress.EgressGuardBuilder

	logger *slog.Logger
}

// FactoryOption configures a ProviderFactory.
type FactoryOption func(*ProviderFactory)

// WithEgressGuard configures the factory to wrap all created cloud clients
// with egress guard decorators for data egress control and audit trail.
//
// Inputs:
//   - builder: The egress guard builder with shared components.
//
// Outputs:
//   - FactoryOption: Option to pass to NewProviderFactory.
func WithEgressGuard(builder *egress.EgressGuardBuilder) FactoryOption {
	return func(f *ProviderFactory) {
		f.egressBuilder = builder
	}
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
//   - opts: Optional factory configuration (e.g., WithEgressGuard).
//
// Outputs:
//   - *ProviderFactory: Configured factory.
func NewProviderFactory(ollamaModelManager *llm.MultiModelManager, opts ...FactoryOption) *ProviderFactory {
	f := &ProviderFactory{
		ollamaModelManager: ollamaModelManager,
		logger:             slog.Default(),
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
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
	var rawClient agentllm.Client

	switch cfg.Provider {
	case ProviderOllama:
		if f.ollamaModelManager == nil {
			return nil, fmt.Errorf("Ollama model manager not available (all-cloud configuration?)")
		}
		ollamaClient, err := llm.NewOllamaClient()
		if err != nil {
			return nil, fmt.Errorf("creating Ollama client: %w", err)
		}
		rawClient = agentllm.NewOllamaAdapter(ollamaClient, cfg.Model)

	case ProviderAnthropic:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY required for Anthropic provider")
		}
		client, err := llm.NewAnthropicClient()
		if err != nil {
			return nil, fmt.Errorf("creating Anthropic client: %w", err)
		}
		rawClient = agentllm.NewAnthropicAgentAdapter(client, cfg.Model)

	case ProviderOpenAI:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for OpenAI provider")
		}
		client, err := llm.NewOpenAIClient()
		if err != nil {
			return nil, fmt.Errorf("creating OpenAI client: %w", err)
		}
		rawClient = agentllm.NewOpenAIAgentAdapter(client, cfg.Model)

	case ProviderGemini:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY required for Gemini provider")
		}
		client, err := llm.NewGeminiClient()
		if err != nil {
			return nil, fmt.Errorf("creating Gemini client: %w", err)
		}
		rawClient = agentllm.NewGeminiAgentAdapter(client, cfg.Model)

	default:
		return nil, fmt.Errorf("unsupported provider: %q (valid: %v)", cfg.Provider, ValidProviders)
	}

	// CB-60d: Wrap with egress guard if configured.
	// Note: The main LLM client is created once and shared across all agent sessions,
	// so we use "shared-main" as the session ID. The token budget is 0 (unlimited) at
	// this level because per-session budgets are enforced by wrapping router/param clients
	// at the handler layer where the actual session ID is available.
	if f.egressBuilder != nil {
		rawClient = f.egressBuilder.WrapAgentClient(rawClient, cfg.Provider, cfg.Model, "shared-main", 0)
	}

	return rawClient, nil
}

// EgressBuilder returns the egress guard builder, if configured.
//
// Outputs:
//   - *egress.EgressGuardBuilder: The builder, or nil if not configured.
func (f *ProviderFactory) EgressBuilder() *egress.EgressGuardBuilder {
	return f.egressBuilder
}

// WrapChatClientForEgress wraps a providers.ChatClient with egress guard checks.
//
// Description:
//
//	Bridges the type gap between providers.ChatClient (which uses providers.ChatOptions)
//	and egress.ChatClient (which uses egress.ChatOptions) by using a thin adapter.
//	This enables per-session egress wrapping for router and param extractor clients
//	at the handler layer where the actual session ID is available.
//
//	Returns the original client unwrapped if no egress builder is configured.
//
// Inputs:
//   - client: The providers.ChatClient to wrap.
//   - provider: The provider name (e.g., "anthropic").
//   - model: The model name.
//   - sessionID: The agent session ID.
//   - role: The role name ("ROUTER" or "PARAM").
//   - tokenLimit: Token budget for this session/role. 0 means unlimited.
//
// Outputs:
//   - ChatClient: The guarded client (or the original if no egress builder or provider is "ollama").
func (f *ProviderFactory) WrapChatClientForEgress(
	client ChatClient,
	provider, model, sessionID, role string,
	tokenLimit int,
) ChatClient {
	if f.egressBuilder == nil {
		return client
	}
	// Adapt providers.ChatClient → egress.ChatClient
	adapted := &chatClientAdapter{inner: client}
	wrapped := f.egressBuilder.WrapChatClient(adapted, provider, model, sessionID, role, tokenLimit)
	// Adapt egress.ChatClient → providers.ChatClient
	return &egressChatClientAdapter{inner: wrapped}
}

// chatClientAdapter adapts providers.ChatClient to egress.ChatClient.
// Required because Go's nominal typing means providers.ChatOptions != egress.ChatOptions
// even though they have identical fields.
type chatClientAdapter struct {
	inner ChatClient
}

func (a *chatClientAdapter) Chat(ctx context.Context, messages []datatypes.Message, opts egress.ChatOptions) (string, error) {
	return a.inner.Chat(ctx, messages, ChatOptions{
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		KeepAlive:   opts.KeepAlive,
		NumCtx:      opts.NumCtx,
		Model:       opts.Model,
	})
}

// egressChatClientAdapter adapts egress.ChatClient back to providers.ChatClient.
type egressChatClientAdapter struct {
	inner egress.ChatClient
}

func (a *egressChatClientAdapter) Chat(ctx context.Context, messages []datatypes.Message, opts ChatOptions) (string, error) {
	return a.inner.Chat(ctx, messages, egress.ChatOptions{
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		KeepAlive:   opts.KeepAlive,
		NumCtx:      opts.NumCtx,
		Model:       opts.Model,
	})
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
