// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"go.opentelemetry.io/otel/attribute"
)

// =============================================================================
// Multi-Model Manager
// =============================================================================

// MultiModelManager coordinates multiple Ollama models to prevent thrashing.
//
// # Description
//
// Ollama by default unloads models when a different model is requested, which
// causes "thrashing" when alternating between models (e.g., tool router + main LLM).
// MultiModelManager uses keep_alive to keep multiple models loaded in VRAM.
//
// # Thread Safety
//
// MultiModelManager is safe for concurrent use.
//
// # Example
//
//	mgr := NewMultiModelManager("http://localhost:11434")
//	err := mgr.WarmModels(ctx, []ModelWarmupConfig{
//	    {Model: "granite4:micro-h", KeepAlive: "-1", Priority: 1},
//	    {Model: "glm-4.7-flash", KeepAlive: "-1", Priority: 2},
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	// Now both models are loaded and will stay in VRAM
//	resp, err := mgr.Chat(ctx, "granite4:micro-h", messages, params)
type MultiModelManager struct {
	baseURL    string
	httpClient *http.Client
	models     map[string]*ManagedModel
	mu         sync.RWMutex
	logger     *slog.Logger
}

// ManagedModel tracks a model's lifecycle state.
//
// # Description
//
// Tracks whether a model is loaded, when it was loaded, and its keep_alive setting.
// Used by MultiModelManager to manage model lifecycle and detect warming issues.
type ManagedModel struct {
	// Name is the model identifier (e.g., "granite4:micro-h").
	Name string `json:"name"`

	// KeepAlive is the keep_alive setting for this model.
	// "-1" = infinite, "5m" = 5 minutes, "0" = unload immediately.
	KeepAlive string `json:"keep_alive"`

	// IsLoaded indicates whether the model is currently loaded in VRAM.
	IsLoaded bool `json:"is_loaded"`

	// LoadedAt is when the model was loaded into VRAM.
	LoadedAt time.Time `json:"loaded_at"`

	// LastUsed is when the model was last used for inference.
	LastUsed time.Time `json:"last_used"`

	// LoadDuration is how long it took to load the model.
	LoadDuration time.Duration `json:"load_duration"`

	// WarmupError contains any error from the warmup attempt.
	WarmupError error `json:"-"`
}

// ModelWarmupConfig specifies how to warm a model.
//
// # Description
//
// Configuration for warming a model including the keep_alive setting
// and priority for loading order.
type ModelWarmupConfig struct {
	// Model is the model name (e.g., "granite4:micro-h").
	Model string

	// KeepAlive controls how long the model stays loaded.
	// "-1" = infinite (recommended for multi-model), "5m" = 5 minutes.
	KeepAlive string

	// Priority determines loading order. Higher = load first.
	Priority int

	// MaxWaitMs is the timeout for warmup in milliseconds.
	// 0 means use default (60 seconds).
	MaxWaitMs int

	// NumCtx is the context window size for this model.
	// MUST be set to prevent Ollama from using default 4096.
	// Recommended: 16384 for router, 65536 for main agent.
	NumCtx int
}

// NewMultiModelManager creates a new MultiModelManager.
//
// # Description
//
// Creates a manager for coordinating multiple Ollama models. The manager
// tracks model state and uses keep_alive to prevent thrashing.
//
// # Inputs
//
//   - baseURL: Ollama server URL (e.g., "http://localhost:11434").
//
// # Outputs
//
//   - *MultiModelManager: Configured manager ready for use.
//
// # Example
//
//	mgr := NewMultiModelManager("http://localhost:11434")
func NewMultiModelManager(baseURL string) *MultiModelManager {
	return &MultiModelManager{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Long timeout for model loading
		},
		models: make(map[string]*ManagedModel),
		logger: slog.Default(),
	}
}

// WarmModels pre-loads multiple models into VRAM.
//
// # Description
//
// Loads models into VRAM by sending minimal requests with keep_alive set.
// Models are loaded in priority order (highest first). This prevents
// cold-start latency on first real request.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - configs: Models to warm with their configurations.
//
// # Outputs
//
//   - error: Non-nil if any model fails to load.
//
// # Example
//
//	err := mgr.WarmModels(ctx, []ModelWarmupConfig{
//	    {Model: "glm-4.7-flash", KeepAlive: "-1", Priority: 1},
//	    {Model: "granite4:micro-h", KeepAlive: "-1", Priority: 2},
//	})
//
// # Limitations
//
//   - Models are loaded sequentially by priority to avoid VRAM contention.
//   - If VRAM is insufficient, later models may evict earlier ones.
func (m *MultiModelManager) WarmModels(ctx context.Context, configs []ModelWarmupConfig) error {
	if len(configs) == 0 {
		return nil
	}

	// Sort by priority (highest first) - simple bubble sort for small lists
	sorted := make([]ModelWarmupConfig, len(configs))
	copy(sorted, configs)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Priority > sorted[i].Priority {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	m.logger.Info("Warming models",
		slog.Int("count", len(configs)),
	)

	// Load models sequentially to avoid VRAM contention
	for _, cfg := range sorted {
		if err := m.WarmModel(ctx, cfg.Model, cfg.KeepAlive, cfg.NumCtx); err != nil {
			m.logger.Error("Failed to warm model",
				slog.String("model", cfg.Model),
				slog.String("error", err.Error()),
			)
			// Store error but continue with other models
			m.mu.Lock()
			if managed, ok := m.models[cfg.Model]; ok {
				managed.WarmupError = err
			}
			m.mu.Unlock()
			return fmt.Errorf("warming model %s: %w", cfg.Model, err)
		}
	}

	return nil
}

// WarmModel loads a single model into VRAM with keep_alive.
//
// # Description
//
// Sends a minimal chat request to load the model and set keep_alive.
// Uses a simple ping message to minimize token usage.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - model: Model name (e.g., "granite4:micro-h").
//   - keepAlive: Keep alive setting ("-1" for infinite).
//
// # Outputs
//
//   - error: Non-nil if the model fails to load.
func (m *MultiModelManager) WarmModel(ctx context.Context, model string, keepAlive string, numCtx int) error {
	startTime := time.Now()

	m.logger.Info("Warming model",
		slog.String("model", model),
		slog.String("keep_alive", keepAlive),
		slog.Int("num_ctx", numCtx),
	)

	// Build options with num_ctx to ensure model loads with correct context window
	options := make(map[string]interface{})
	if numCtx > 0 {
		options["num_ctx"] = numCtx
	}

	// Create minimal warmup request
	req := ollamaChatRequest{
		Model: model,
		Messages: []datatypes.Message{
			{Role: "user", Content: "ping"},
		},
		Stream:    false,
		KeepAlive: keepAlive,
		Options:   options,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling warmup request: %w", err)
	}

	chatURL := m.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("creating warmup request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending warmup request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("warmup failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Drain response body
	_, _ = io.ReadAll(resp.Body)

	loadDuration := time.Since(startTime)

	// Track the model
	m.mu.Lock()
	m.models[model] = &ManagedModel{
		Name:         model,
		KeepAlive:    keepAlive,
		IsLoaded:     true,
		LoadedAt:     time.Now(),
		LastUsed:     time.Now(),
		LoadDuration: loadDuration,
	}
	m.mu.Unlock()

	m.logger.Info("Model warmed successfully",
		slog.String("model", model),
		slog.Duration("load_duration", loadDuration),
	)

	return nil
}

// Chat sends a chat request to a specific model.
//
// # Description
//
// Routes the request to the specified model with keep_alive preservation.
// Updates last-used timestamp for the model.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout.
//   - model: Which model to use (e.g., "granite4:micro-h").
//   - messages: Conversation history.
//   - params: Generation parameters.
//
// # Outputs
//
//   - string: Response content.
//   - error: Non-nil on failure.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (m *MultiModelManager) Chat(ctx context.Context, model string,
	messages []datatypes.Message, params GenerationParams) (string, error) {

	// CB-31d: Log every Chat call to trace router usage
	numCtx := 0
	if params.NumCtx != nil {
		numCtx = *params.NumCtx
	}
	slog.Info("CB-31d MultiModelManager.Chat CALLED",
		slog.String("model", model),
		slog.Int("num_messages", len(messages)),
		slog.Int("num_ctx", numCtx),
		slog.String("keep_alive", params.KeepAlive),
		slog.String("base_url", m.baseURL),
	)

	ctx, span := tracer.Start(ctx, "MultiModelManager.Chat")
	defer span.End()
	span.SetAttributes(attribute.String("llm.model", model))

	// Get the managed model's keep_alive setting
	m.mu.RLock()
	managed, exists := m.models[model]
	keepAlive := ""
	if exists {
		keepAlive = managed.KeepAlive
	}
	m.mu.RUnlock()

	// Override params with model-specific settings
	params.ModelOverride = model
	if params.KeepAlive == "" && keepAlive != "" {
		params.KeepAlive = keepAlive
	}

	// Build the request using existing logic from OllamaClient
	options := m.buildOptions(params)

	payload := ollamaChatRequest{
		Model:     model,
		Messages:  messages,
		Stream:    false,
		Options:   options,
		KeepAlive: params.KeepAlive,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling chat request: %w", err)
	}

	chatURL := m.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("sending chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	// Update last used time
	m.mu.Lock()
	if managed, ok := m.models[model]; ok {
		managed.LastUsed = time.Now()
	}
	m.mu.Unlock()

	slog.Info("CB-31d MultiModelManager.Chat SUCCEEDED",
		slog.String("model", model),
		slog.Int("response_len", len(chatResp.Message.Content)),
	)

	return chatResp.Message.Content, nil
}

// ChatWithTools sends a chat request with tools to a specific model.
//
// # Description
//
// Like Chat but with tool definitions for function calling.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout.
//   - model: Which model to use.
//   - messages: Conversation history.
//   - params: Generation parameters.
//   - tools: Tool definitions for function calling.
//
// # Outputs
//
//   - *ChatWithToolsResult: Content and/or tool calls.
//   - error: Non-nil on failure.
func (m *MultiModelManager) ChatWithTools(ctx context.Context, model string,
	messages []datatypes.Message, params GenerationParams,
	tools []OllamaTool) (*ChatWithToolsResult, error) {

	ctx, span := tracer.Start(ctx, "MultiModelManager.ChatWithTools")
	defer span.End()
	span.SetAttributes(attribute.String("llm.model", model))
	span.SetAttributes(attribute.Int("llm.num_tools", len(tools)))

	// Get the managed model's keep_alive setting
	m.mu.RLock()
	managed, exists := m.models[model]
	keepAlive := ""
	if exists {
		keepAlive = managed.KeepAlive
	}
	m.mu.RUnlock()

	// Override params with model-specific settings
	params.ModelOverride = model
	if params.KeepAlive == "" && keepAlive != "" {
		params.KeepAlive = keepAlive
	}

	options := m.buildOptions(params)

	payload := ollamaChatRequest{
		Model:     model,
		Messages:  messages,
		Stream:    false,
		Options:   options,
		Tools:     tools,
		KeepAlive: params.KeepAlive,
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling chat request: %w", err)
	}

	chatURL := m.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("creating chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp ollamaChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	// Update last used time
	m.mu.Lock()
	if managed, ok := m.models[model]; ok {
		managed.LastUsed = time.Now()
	}
	m.mu.Unlock()

	// Convert OllamaToolCall to generic ToolCallResponse
	var toolCalls []ToolCallResponse
	for _, tc := range chatResp.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCallResponse{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	result := &ChatWithToolsResult{
		Content:   chatResp.Message.Content,
		ToolCalls: toolCalls,
	}

	if len(result.ToolCalls) > 0 {
		result.StopReason = "tool_use"
	} else {
		result.StopReason = "end"
	}

	return result, nil
}

// GetLoadedModels returns currently tracked models.
//
// # Description
//
// Returns a snapshot of all models that have been warmed or used.
// Note: This doesn't query Ollama directly; it returns tracked state.
//
// # Outputs
//
//   - []ManagedModel: Copy of tracked model states.
func (m *MultiModelManager) GetLoadedModels() []ManagedModel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	models := make([]ManagedModel, 0, len(m.models))
	for _, managed := range m.models {
		models = append(models, *managed)
	}
	return models
}

// UnloadModel explicitly unloads a model from VRAM.
//
// # Description
//
// Sends a request with keep_alive="0" to immediately unload the model.
// Use this for cleanup when a session ends.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - model: Model to unload.
//
// # Outputs
//
//   - error: Non-nil if unload fails.
func (m *MultiModelManager) UnloadModel(ctx context.Context, model string) error {
	m.logger.Info("Unloading model", slog.String("model", model))

	// Send request with keep_alive="0" to unload immediately
	req := ollamaChatRequest{
		Model: model,
		Messages: []datatypes.Message{
			{Role: "user", Content: "bye"},
		},
		Stream:    false,
		KeepAlive: "0", // Unload immediately
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling unload request: %w", err)
	}

	chatURL := m.baseURL + "/api/chat"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("creating unload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending unload request: %w", err)
	}
	defer resp.Body.Close()

	// Drain and ignore response - we just want the side effect
	_, _ = io.ReadAll(resp.Body)

	// Update tracking
	m.mu.Lock()
	if managed, ok := m.models[model]; ok {
		managed.IsLoaded = false
	}
	m.mu.Unlock()

	return nil
}

// buildOptions constructs the options map from GenerationParams.
func (m *MultiModelManager) buildOptions(params GenerationParams) map[string]interface{} {
	options := make(map[string]interface{})

	if params.Temperature != nil {
		options["temperature"] = *params.Temperature
	} else {
		options["temperature"] = float32(0.2)
	}

	if params.TopK != nil {
		options["top_k"] = *params.TopK
	} else {
		options["top_k"] = 20
	}

	if params.TopP != nil {
		options["top_p"] = *params.TopP
	} else {
		options["top_p"] = float32(0.9)
	}

	if params.MaxTokens != nil {
		options["num_predict"] = *params.MaxTokens
	} else {
		options["num_predict"] = 8192
	}

	if len(params.Stop) > 0 {
		options["stop"] = params.Stop
	}

	// Set context window size if provided.
	// CRITICAL: This MUST be passed on every request to prevent Ollama from
	// resetting to default 4096 context window. CB-31d fix.
	if params.NumCtx != nil && *params.NumCtx > 0 {
		options["num_ctx"] = *params.NumCtx
	}

	return options
}
