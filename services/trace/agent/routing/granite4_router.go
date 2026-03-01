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
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/providers"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("aleutian.routing")

// =============================================================================
// Granite4 Router Implementation
// =============================================================================

// Granite4Router implements ToolRouter using a fast model for tool routing.
//
// # Description
//
// Uses a fast model (e.g., IBM Granite4 with hybrid Mamba-2 architecture) for
// ultra-fast tool routing. The ChatClient interface allows any provider
// (Ollama, Anthropic, OpenAI, Gemini) to be used as the routing backend.
//
// CB-60: Refactored to use providers.ChatClient instead of direct MultiModelManager
// dependency, enabling any LLM provider for tool routing.
//
// # Thread Safety
//
// Granite4Router is safe for concurrent use.
type Granite4Router struct {
	chatClient    providers.ChatClient
	config        RouterConfig
	promptBuilder *PromptBuilder
	logger        *slog.Logger
}

// NewGranite4Router creates a new router with the specified configuration.
//
// # Description
//
// Creates a router that uses a fast model for tool selection. The ChatClient
// interface allows any provider (Ollama, Anthropic, OpenAI, Gemini) to be used.
//
// CB-60: Accepts providers.ChatClient instead of *llm.MultiModelManager.
//
// # Inputs
//
//   - chatClient: ChatClient for sending routing queries. Must not be nil.
//   - config: Router configuration.
//
// # Outputs
//
//   - *Granite4Router: Configured router.
//   - error: Non-nil if initialization fails.
//
// # Example
//
//	factory := providers.NewProviderFactory(modelManager)
//	client, _ := factory.CreateChatClient(cfg)
//	config := routing.DefaultRouterConfig()
//	router, err := routing.NewGranite4Router(client, config)
func NewGranite4Router(chatClient providers.ChatClient, config RouterConfig) (*Granite4Router, error) {
	slog.Info("NewGranite4Router: Creating router",
		slog.String("model", config.Model),
		slog.String("endpoint", config.OllamaEndpoint),
		slog.Bool("has_chat_client", chatClient != nil))

	if chatClient == nil {
		slog.Error("NewGranite4Router: chatClient is nil")
		return nil, fmt.Errorf("chatClient must not be nil")
	}

	slog.Info("NewGranite4Router: Creating prompt builder")
	promptBuilder, err := NewPromptBuilder()
	if err != nil {
		slog.Error("NewGranite4Router: Failed to create prompt builder",
			slog.String("error", err.Error()))
		return nil, fmt.Errorf("creating prompt builder: %w", err)
	}

	slog.Info("NewGranite4Router: Router created successfully",
		slog.String("model", config.Model))

	return &Granite4Router{
		chatClient:    chatClient,
		config:        config,
		promptBuilder: promptBuilder,
		logger:        slog.Default(),
	}, nil
}

// NewGranite4RouterWithDefaults creates a router with default configuration.
//
// # Description
//
// Convenience constructor that creates a MultiModelManager wrapped in an
// OllamaChatAdapter and uses default configuration. For more control or
// non-Ollama providers, use NewGranite4Router with a custom ChatClient.
//
// # Inputs
//
//   - ollamaEndpoint: Ollama server URL.
//
// # Outputs
//
//   - *Granite4Router: Configured router.
//   - error: Non-nil if initialization fails.
func NewGranite4RouterWithDefaults(ollamaEndpoint string) (*Granite4Router, error) {
	config := DefaultRouterConfig()
	config.OllamaEndpoint = ollamaEndpoint

	modelManager := llm.NewMultiModelManager(ollamaEndpoint)
	chatClient := providers.NewOllamaChatAdapter(modelManager, config.Model)

	return NewGranite4Router(chatClient, config)
}

// SelectTool chooses the best tool for the given query.
//
// # Description
//
// Uses the micro LLM to analyze the query and select the most appropriate
// tool. Returns an error if the selection fails or confidence is too low.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout.
//   - query: The user's question or request.
//   - availableTools: Tools currently available.
//   - codeContext: Optional context about the codebase.
//
// # Outputs
//
//   - *ToolSelection: The selected tool with confidence.
//   - error: Non-nil if routing fails.
func (r *Granite4Router) SelectTool(ctx context.Context, query string, availableTools []ToolSpec, codeContext *CodeContext) (*ToolSelection, error) {
	slog.Info("CB-31d Granite4Router.SelectTool CALLED",
		slog.String("model", r.config.Model),
		slog.Int("num_tools", len(availableTools)),
		slog.Int("num_ctx", r.config.NumCtx),
		slog.String("keep_alive", r.config.KeepAlive),
		slog.Duration("timeout", r.config.Timeout),
	)

	ctx, span := tracer.Start(ctx, "Granite4Router.SelectTool")
	defer span.End()

	span.SetAttributes(
		attribute.String("router.model", r.config.Model),
		attribute.Int("router.num_tools", len(availableTools)),
		attribute.String("query_preview", truncate(query, 100)),
	)

	startTime := time.Now()

	// Validate inputs
	if len(availableTools) == 0 {
		return nil, NewRouterError(ErrCodeNoTools, "no tools available for selection", false)
	}

	// Apply timeout
	if r.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.config.Timeout)
		defer cancel()
	}

	// Build prompts
	systemPrompt, err := r.promptBuilder.BuildSystemPrompt(availableTools, codeContext)
	if err != nil {
		return nil, fmt.Errorf("building system prompt: %w", err)
	}

	userPrompt := r.promptBuilder.BuildUserPrompt(query)

	r.logger.Debug("Routing query",
		slog.String("model", r.config.Model),
		slog.Int("num_tools", len(availableTools)),
		slog.String("query_preview", truncate(query, 50)),
	)

	// Call the model
	messages := []datatypes.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	opts := providers.ChatOptions{
		Temperature: r.config.Temperature,
		MaxTokens:   r.config.MaxTokens,
		NumCtx:      r.config.NumCtx,
		KeepAlive:   r.config.KeepAlive,
		Model:       r.config.Model,
	}

	slog.Info("CB-31d Granite4Router calling chatClient.Chat",
		slog.String("model", r.config.Model),
		slog.Int("num_messages", len(messages)),
		slog.Int("system_prompt_len", len(systemPrompt)),
		slog.Int("user_prompt_len", len(userPrompt)),
		slog.Int("num_ctx", opts.NumCtx),
	)

	response, err := r.chatClient.Chat(ctx, messages, opts)
	if err != nil {
		slog.Error("CB-31d Granite4Router.Chat FAILED",
			slog.String("model", r.config.Model),
			slog.String("error", err.Error()),
			slog.Duration("elapsed", time.Since(startTime)),
		)
		duration := time.Since(startTime)
		if ctx.Err() == context.DeadlineExceeded {
			span.SetStatus(codes.Error, "timeout")
			RecordRoutingLatency(r.config.Model, "error", duration.Seconds())
			RecordRoutingError(r.config.Model, "timeout")
			return nil, NewRouterError(ErrCodeTimeout, "routing timed out", true)
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "chat failed")
		RecordRoutingLatency(r.config.Model, "error", duration.Seconds())
		RecordRoutingError(r.config.Model, "chat_failed")
		return nil, fmt.Errorf("router chat failed: %w", err)
	}

	slog.Info("CB-31d Granite4Router.Chat SUCCEEDED",
		slog.String("model", r.config.Model),
		slog.Int("response_len", len(response)),
		slog.String("response_preview", truncate(response, 200)),
		slog.Duration("elapsed", time.Since(startTime)),
	)

	// Parse the response
	selection, err := r.parseResponse(response, availableTools)
	if err != nil {
		duration := time.Since(startTime)
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse failed")
		RecordRoutingLatency(r.config.Model, "error", duration.Seconds())
		RecordRoutingError(r.config.Model, "parse_error")
		return nil, err
	}

	selection.Duration = time.Since(startTime)

	// Record confidence metric
	RecordRoutingConfidence(r.config.Model, selection.Confidence)

	// Check confidence threshold
	if selection.Confidence < r.config.ConfidenceThreshold {
		span.SetStatus(codes.Error, "low confidence")
		RecordRoutingLatency(r.config.Model, "low_confidence", selection.Duration.Seconds())
		RecordRoutingFallback(r.config.Model, "low_confidence")
		return selection, NewRouterError(
			ErrCodeLowConfidence,
			fmt.Sprintf("confidence %.2f below threshold %.2f", selection.Confidence, r.config.ConfidenceThreshold),
			false,
		)
	}

	// Record successful selection
	RecordRoutingLatency(r.config.Model, "success", selection.Duration.Seconds())
	RecordRoutingSelection(r.config.Model, selection.Tool)

	span.SetAttributes(
		attribute.String("selection.tool", selection.Tool),
		attribute.Float64("selection.confidence", selection.Confidence),
		attribute.Int64("selection.duration_ms", selection.Duration.Milliseconds()),
	)

	r.logger.Info("Tool selected",
		slog.String("tool", selection.Tool),
		slog.Float64("confidence", selection.Confidence),
		slog.Duration("duration", selection.Duration),
		slog.String("reasoning", selection.Reasoning),
	)

	return selection, nil
}

// FilterBatch evaluates a batch of tool calls and returns filter decisions.
//
// # Description
//
// Uses the router model to decide which tool calls in a batch should be
// executed (KEEP) vs skipped (SKIP). This helps reduce redundant tool calls
// when the main LLM requests multiple similar operations.
//
// GR-39a: Implements the BatchFilterer interface for semantic deduplication.
//
// # Inputs
//
//   - ctx: Context for cancellation/timeout.
//   - prompt: Filter prompt containing tool calls with similarity scores.
//
// # Outputs
//
//   - string: Response with KEEP/SKIP decisions (e.g., "1:KEEP 2:SKIP 3:KEEP").
//   - error: Non-nil if the filter call fails.
//
// # Thread Safety
//
// This method is safe for concurrent use.
func (r *Granite4Router) FilterBatch(ctx context.Context, prompt string) (string, error) {
	ctx, span := tracer.Start(ctx, "Granite4Router.FilterBatch")
	defer span.End()

	span.SetAttributes(
		attribute.String("model", r.config.Model),
		attribute.Int("prompt_len", len(prompt)),
	)

	startTime := time.Now()

	// Build messages for the model manager
	systemPrompt := "You are a tool filter that decides which tool calls to KEEP or SKIP based on relevance and redundancy. Respond only with the format: 1:KEEP 2:SKIP 3:KEEP ..."
	messages := []datatypes.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}

	// Build chat options
	opts := providers.ChatOptions{
		Temperature: r.config.Temperature,
		MaxTokens:   256, // Short response expected
		NumCtx:      r.config.NumCtx,
		KeepAlive:   r.config.KeepAlive,
		Model:       r.config.Model,
	}

	// Use chat client for coordinated model access
	response, err := r.chatClient.Chat(ctx, messages, opts)
	if err != nil {
		duration := time.Since(startTime)
		span.RecordError(err)
		span.SetStatus(codes.Error, "filter failed")
		RecordRoutingLatency(r.config.Model, "filter_error", duration.Seconds())
		RecordRoutingError(r.config.Model, "filter_failed")
		return "", fmt.Errorf("batch filter failed: %w", err)
	}

	duration := time.Since(startTime)
	span.SetAttributes(
		attribute.Int("response_len", len(response)),
		attribute.Int64("duration_ms", duration.Milliseconds()),
	)

	RecordRoutingLatency(r.config.Model, "filter_success", duration.Seconds())

	r.logger.Debug("Batch filter completed",
		slog.String("model", r.config.Model),
		slog.Duration("duration", duration),
		slog.Int("response_len", len(response)),
	)

	return response, nil
}

// parseResponse parses the JSON response from the router model.
//
// # Description
//
// Extracts the tool selection from the model's JSON response.
// Handles common variations in output format.
//
// # Inputs
//
//   - response: Raw response from the model.
//   - availableTools: Tools to validate against.
//
// # Outputs
//
//   - *ToolSelection: Parsed selection.
//   - error: Non-nil if parsing fails.
func (r *Granite4Router) parseResponse(response string, availableTools []ToolSpec) (*ToolSelection, error) {
	// GR-Phase1: Check for empty response before any processing.
	// Empty responses indicate model warmup issues or context problems.
	// Return specific error code for better debugging.
	if len(strings.TrimSpace(response)) == 0 {
		slog.Warn("Router received empty response from model",
			slog.String("model", r.config.Model),
		)
		return nil, NewRouterError(
			ErrCodeEmptyResponse,
			"router model returned empty response",
			true, // retryable - model may need warmup
		)
	}

	// Clean up the response - remove markdown code blocks if present
	response = strings.TrimSpace(response)
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Try to find JSON in the response
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, NewRouterError(
			ErrCodeParseError,
			"no JSON object found in response: "+truncate(response, 100),
			false,
		)
	}

	jsonStr := response[startIdx : endIdx+1]

	// Parse JSON
	var result struct {
		Tool       string  `json:"tool"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, NewRouterError(
			ErrCodeParseError,
			fmt.Sprintf("failed to parse JSON: %v, response: %s", err, truncate(jsonStr, 100)),
			false,
		)
	}

	// I-2 Fix: Clamp confidence to valid range BEFORE any modifications.
	// This ensures normalized confidence before tool validation penalty.
	if result.Confidence < 0 {
		result.Confidence = 0
	}
	if result.Confidence > 1 {
		result.Confidence = 1
	}

	// Validate tool exists
	toolValid := false
	for _, t := range availableTools {
		if t.Name == result.Tool {
			toolValid = true
			break
		}
	}

	if !toolValid {
		// CB-62 Rev 2: Try case-insensitive exact match first (typo correction).
		corrected := r.findClosestTool(result.Tool, availableTools)
		if corrected != result.Tool {
			// Case-insensitive match found — simple correction, not a prefilter miss.
			slog.Warn("Router model returned tool with wrong case, corrected",
				slog.String("model", r.config.Model),
				slog.String("original", result.Tool),
				slog.String("corrected", corrected),
			)
			result.Tool = corrected
			result.Confidence *= 0.8
		} else {
			// CB-62 Rev 2: Prefilter miss detected. The router model picked a tool
			// that the prefilter excluded. Instead of falling back to tools[0]
			// (which is a random wrong tool), signal the miss to the
			// EscalatingRouter for recovery.
			slog.Warn("Router model picked tool outside candidate set (prefilter miss)",
				slog.String("model", r.config.Model),
				slog.String("raw_pick", result.Tool),
				slog.Float64("confidence", result.Confidence),
			)
			return &ToolSelection{
				Tool:          result.Tool,
				Confidence:    result.Confidence,
				Reasoning:     result.Reasoning,
				PrefilterMiss: true,
				RawModelPick:  result.Tool,
			}, nil
		}
	}

	return &ToolSelection{
		Tool:       result.Tool,
		Confidence: result.Confidence,
		Reasoning:  result.Reasoning,
	}, nil
}

// findClosestTool finds a case-insensitive exact match for a tool name.
//
// # Description
//
// CB-62 Rev 2: Simplified to case-insensitive exact match only. The old
// prefix/contains/tools[0] fallback chain was the root cause of IT-7144:
// when the router correctly picked a tool not in the candidate set, the
// fallback chain silently returned tools[0] (a random wrong tool).
//
// Now, if no case-insensitive match exists, the original name is returned
// unchanged. The caller (parseResponse) uses the mismatch to detect a
// prefilter miss and set PrefilterMiss=true for EscalatingRouter recovery.
func (r *Granite4Router) findClosestTool(name string, tools []ToolSpec) string {
	nameLower := strings.ToLower(name)

	// Case-insensitive exact match only.
	for _, t := range tools {
		if strings.ToLower(t.Name) == nameLower {
			return t.Name
		}
	}

	// No match — return original name unchanged.
	// Caller detects corrected == original as "not found in set".
	return name
}

// Model returns the model being used for routing.
func (r *Granite4Router) Model() string {
	return r.config.Model
}

// Close releases any resources held by the router.
//
// # Description
//
// Currently a no-op since the MultiModelManager is shared.
// Future implementations might unload the router model here.
func (r *Granite4Router) Close() error {
	// No-op: MultiModelManager is shared and manages model lifecycle
	return nil
}

// WarmRouter pre-loads the routing model using the provided lifecycle manager.
//
// # Description
//
// Warms up the router model so the first routing request doesn't incur
// model loading latency. Should be called during initialization.
//
// CB-60: Refactored to accept a ModelLifecycleManager instead of using
// the internal model manager. For cloud providers, warmup is a no-op.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - lifecycle: Lifecycle manager for model warmup. If nil, warmup is skipped.
//
// # Outputs
//
//   - error: Non-nil if warmup fails.
func (r *Granite4Router) WarmRouter(ctx context.Context, lifecycle providers.ModelLifecycleManager) error {
	if lifecycle == nil {
		r.logger.Info("WarmRouter: No lifecycle manager, skipping warmup",
			slog.String("model", r.config.Model))
		return nil
	}

	r.logger.Info("WarmRouter: Starting model warmup",
		slog.String("model", r.config.Model),
		slog.String("keep_alive", r.config.KeepAlive),
		slog.Bool("is_local", lifecycle.IsLocal()))

	startTime := time.Now()
	err := lifecycle.WarmModel(ctx, r.config.Model, providers.WarmupOptions{
		KeepAlive: r.config.KeepAlive,
		NumCtx:    r.config.NumCtx,
	})
	duration := time.Since(startTime)

	if err != nil {
		r.logger.Error("WarmRouter: Model warmup failed",
			slog.String("model", r.config.Model),
			slog.Duration("duration", duration),
			slog.String("error", err.Error()),
			slog.String("error_type", fmt.Sprintf("%T", err)))
	} else {
		r.logger.Info("WarmRouter: Model warmup succeeded",
			slog.String("model", r.config.Model),
			slog.Duration("duration", duration))
	}

	RecordModelWarmup(r.config.Model, duration.Seconds(), err == nil)
	return err
}

// =============================================================================
// Helpers
// =============================================================================

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
