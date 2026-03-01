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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

const (
	anthropicAPIVersion = "2023-06-01"
	defaultBaseURL      = "https://api.anthropic.com/v1/messages"
)

type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	System    []systemBlock      `json:"system,omitempty"` // Top-level system prompt
	MaxTokens int                `json:"max_tokens"`
	// Optional params
	Thinking *thinkingParams   `json:"thinking,omitempty"`
	Tools    []toolsDefinition `json:"tools,omitempty"`

	Temperature *float32 `json:"temperature,omitempty"`
	TopP        *float32 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	StopSeqs    []string `json:"stop_sequences,omitempty"`
	Stream      bool     `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
	Error   *anthropicError    `json:"error,omitempty"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type thinkingParams struct {
	Type         string `json:"type"` // Must be "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

type cacheControl struct {
	Type string `json:"type"` // Must be "ephemeral"
}

type toolsDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"` // JSON Schema
}

type anthropicContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// anthropicToolMessage is a message with structured content blocks.
// Used for ChatWithTools where content must be an array of content blocks
// (e.g., tool_use, tool_result) rather than a plain string.
type anthropicToolMessage struct {
	Role    string        `json:"role"`
	Content []interface{} `json:"content"`
}

// anthropicToolRequest is the request payload for ChatWithTools.
// It uses interface{} for messages to support both string and structured content.
type anthropicToolRequest struct {
	Model     string        `json:"model"`
	Messages  []interface{} `json:"messages"`
	System    []systemBlock `json:"system,omitempty"`
	MaxTokens int           `json:"max_tokens"`
	Tools     []interface{} `json:"tools,omitempty"`

	Temperature *float32 `json:"temperature,omitempty"`
	TopP        *float32 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	StopSeqs    []string `json:"stop_sequences,omitempty"`
}

// anthropicToolUseBlock is a tool_use content block in the request (assistant message).
type anthropicToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// anthropicToolResultBlock is a tool_result content block in the request (user message).
type anthropicToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

// anthropicTextBlock is a text content block.
type anthropicTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// anthropicToolDef is a tool definition for the Anthropic API.
type anthropicToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

// anthropicToolResponse is used for parsing tool_use blocks from responses.
type anthropicToolResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Content    []json.RawMessage `json:"content"`
	Error      *anthropicError   `json:"error,omitempty"`
	StopReason string            `json:"stop_reason,omitempty"`
}

// anthropicContentBlock is used for parsing individual content blocks from response.
type anthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// --- Client Implementation ---

type AnthropicClient struct {
	httpClient *http.Client
	apiKey     string
	model      string
	baseURL    string
}

// NewAnthropicClientWithConfig creates an AnthropicClient with explicit configuration.
//
// Description:
//
//	Creates an AnthropicClient without reading environment variables. Useful
//	for testing with mock servers or when configuration comes from a source
//	other than environment variables.
//
// Inputs:
//   - apiKey: The Anthropic API key.
//   - model: The model name (e.g., "claude-sonnet-4-20250514").
//   - baseURL: The base URL for API requests.
//
// Outputs:
//   - *AnthropicClient: The configured client.
func NewAnthropicClientWithConfig(apiKey, model, baseURL string) *AnthropicClient {
	return &AnthropicClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
	}
}

func NewAnthropicClient() (*AnthropicClient, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("CLAUDE_MODEL")

	// 1. Robust Secret Loading
	if apiKey == "" {
		secretPath := "/run/secrets/anthropic_api_key"
		if content, err := os.ReadFile(secretPath); err == nil {
			apiKey = strings.TrimSpace(string(content))
			slog.Info("Read Anthropic API Key from Podman Secrets")
		}
	}

	// 2. Graceful Failure
	if apiKey == "" {
		slog.Warn("Anthropic API Key is missing.")
		return nil, fmt.Errorf("anthropic: API key is missing (ANTHROPIC_API_KEY)")
	}

	if model == "" {
		// Because we are using raw strings now, we can default to the ID directly
		// without needing a library constant.
		model = "claude-3-5-sonnet-20240620"
		slog.Info("CLAUDE_MODEL not set, defaulting to", "model", model)
	}

	return &AnthropicClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultBaseURL,
	}, nil
}

// Generate implements the LLMClient interface
func (a *AnthropicClient) Generate(ctx context.Context, prompt string, params GenerationParams) (string, error) {
	messages := []datatypes.Message{
		{Role: "user", Content: prompt},
	}
	return a.Chat(ctx, messages, params)
}

// Chat implements the LLMClient interface
func (a *AnthropicClient) Chat(ctx context.Context, messages []datatypes.Message, params GenerationParams) (string, error) {
	var apiMessages []anthropicMessage
	var systemPrompt string

	// 1. Convert generic messages to Anthropic format
	for _, msg := range messages {
		if strings.ToLower(msg.Role) == "system" {
			systemPrompt = msg.Content
			continue
		}

		role := msg.Role
		// Map "assistant" (standard) to "assistant" (anthropic) - usually same
		// Map "user" to "user"

		apiMessages = append(apiMessages, anthropicMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	// Handle System Prompt with Caching
	var systemBlocks []systemBlock
	if systemPrompt != "" {
		block := systemBlock{
			Type: "text",
			Text: systemPrompt,
		}
		if len(systemPrompt) > 1024 {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		systemBlocks = append(systemBlocks, block)
	}

	// Build Payload
	reqPayload := anthropicRequest{
		Model:     a.model,
		Messages:  apiMessages,
		System:    systemBlocks,
		MaxTokens: 4096,
	}

	// Apply optional generation parameters
	if params.Temperature != nil {
		reqPayload.Temperature = params.Temperature
	}
	if params.TopP != nil {
		reqPayload.TopP = params.TopP
	}
	if params.TopK != nil {
		reqPayload.TopK = params.TopK
	}
	if len(params.Stop) > 0 {
		reqPayload.StopSeqs = params.Stop
	}
	if params.MaxTokens != nil {
		reqPayload.MaxTokens = *params.MaxTokens
	}

	if len(params.ToolDefinitions) > 0 {
		var tools []toolsDefinition
		toolBytes, marshalErr := json.Marshal(params.ToolDefinitions)
		if marshalErr != nil {
			return "", fmt.Errorf("anthropic: marshaling tool definitions: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(toolBytes, &tools); unmarshalErr != nil {
			return "", fmt.Errorf("anthropic: unmarshaling tool definitions: %w", unmarshalErr)
		}
		reqPayload.Tools = tools
	}

	// Enable Thinking if requested (must match buildStreamRequest behavior — fix B-5)
	if params.EnableThinking {
		reqPayload.Thinking = &thinkingParams{
			Type:         "enabled",
			BudgetTokens: params.BudgetTokens,
		}
		minRequired := params.BudgetTokens + 2048 // Budget + Room for answer
		if reqPayload.MaxTokens < minRequired {
			slog.Info("Adjusting MaxTokens to accommodate Thinking budget", "old", reqPayload.MaxTokens, "new", minRequired)
			reqPayload.MaxTokens = minRequired
		}
	}

	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("anthropic: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return "", fmt.Errorf("anthropic: creating HTTP request: %w", err)
	}

	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")

	slog.Debug("Sending REST request to Anthropic", "model", a.model)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("anthropic: reading response body (status %d): %w", resp.StatusCode, readErr)
	}

	slog.Info("Anthropic response received",
		slog.Int("status", resp.StatusCode),
		slog.Int("body_length", len(bodyBytes)),
		slog.String("model", a.model),
	)
	slog.Debug("Anthropic response body",
		slog.String("body", SafeLogString(string(bodyBytes))),
	)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("anthropic: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("anthropic: API error: %s - %s", apiResp.Error.Type, SafeLogString(apiResp.Error.Message))
	}

	if len(apiResp.Content) == 0 {
		return "", fmt.Errorf("anthropic: received empty content")
	}

	finalText := ""

	for _, block := range apiResp.Content {
		if block.Type == "text" {
			finalText += block.Text
		}
		if block.Type == "thinking" {
			slog.Debug("Claude Thoughts", "thinking", SafeLogString(block.Thinking))
		}
	}

	if finalText == "" {
		return "", fmt.Errorf("anthropic: received content but no text block found (check logs for thoughts)")
	}

	return finalText, nil
}

// ChatWithTools sends a chat request with tool definitions and returns tool calls.
//
// Description:
//
//	Extends Chat to support Anthropic's native function calling API. Converts
//	generic ToolDef and ChatMessage types to Anthropic wire format, including
//	structured content blocks for tool_use and tool_result messages.
//
// Inputs:
//   - ctx: Context for cancellation and timeout.
//   - messages: Conversation history with tool metadata.
//   - params: Generation parameters.
//   - tools: Tool definitions for function calling.
//
// Outputs:
//   - *ChatWithToolsResult: Content and/or tool calls.
//   - error: Non-nil on failure.
//
// Thread Safety: This method is safe for concurrent use.
func (a *AnthropicClient) ChatWithTools(ctx context.Context, messages []ChatMessage,
	params GenerationParams, tools []ToolDef) (*ChatWithToolsResult, error) {

	slog.Debug("ChatWithTools via Anthropic",
		slog.String("model", a.model),
		slog.Int("messages", len(messages)),
		slog.Int("tools", len(tools)),
	)

	// Convert ChatMessage to Anthropic format with structured content blocks
	var apiMessages []interface{}
	var systemPrompt string

	for _, msg := range messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
			continue
		}

		switch {
		case msg.Role == "tool" && msg.ToolCallID != "":
			// Tool result → user message with tool_result content block
			apiMessages = append(apiMessages, anthropicToolMessage{
				Role: "user",
				Content: []interface{}{
					anthropicToolResultBlock{
						Type:      "tool_result",
						ToolUseID: msg.ToolCallID,
						Content:   msg.Content,
					},
				},
			})

		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			// Assistant message with tool calls → content blocks
			var blocks []interface{}
			if msg.Content != "" {
				blocks = append(blocks, anthropicTextBlock{
					Type: "text",
					Text: msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage(`{}`)
				}
				blocks = append(blocks, anthropicToolUseBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: input,
				})
			}
			apiMessages = append(apiMessages, anthropicToolMessage{
				Role:    "assistant",
				Content: blocks,
			})

		default:
			// Regular message (string content)
			apiMessages = append(apiMessages, anthropicMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	// Build system blocks
	var systemBlocks []systemBlock
	if systemPrompt != "" {
		block := systemBlock{
			Type: "text",
			Text: systemPrompt,
		}
		if len(systemPrompt) > 1024 {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		systemBlocks = append(systemBlocks, block)
	}

	// Convert tools
	var apiTools []interface{}
	for _, td := range tools {
		apiTools = append(apiTools, anthropicToolDef{
			Name:        td.Function.Name,
			Description: td.Function.Description,
			InputSchema: td.Function.Parameters,
		})
	}

	// Build request
	reqPayload := anthropicToolRequest{
		Model:     a.model,
		Messages:  apiMessages,
		System:    systemBlocks,
		MaxTokens: 4096,
		Tools:     apiTools,
	}

	if params.Temperature != nil {
		reqPayload.Temperature = params.Temperature
	}
	if params.TopP != nil {
		reqPayload.TopP = params.TopP
	}
	if params.TopK != nil {
		reqPayload.TopK = params.TopK
	}
	if len(params.Stop) > 0 {
		reqPayload.StopSeqs = params.Stop
	}
	if params.MaxTokens != nil {
		reqPayload.MaxTokens = *params.MaxTokens
	}

	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("anthropic: creating HTTP request: %w", err)
	}

	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	// Parse response with tool_use support
	var apiResp anthropicToolResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("anthropic: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("anthropic: API error: %s - %s", apiResp.Error.Type, SafeLogString(apiResp.Error.Message))
	}

	// Parse content blocks
	result := &ChatWithToolsResult{}
	var textParts []string

	for _, raw := range apiResp.Content {
		var block anthropicContentBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			slog.Warn("Failed to parse content block", "error", err)
			continue
		}

		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: input,
			})
		}
	}

	result.Content = strings.Join(textParts, "")

	if len(result.ToolCalls) > 0 {
		result.StopReason = "tool_use"
	} else {
		result.StopReason = "end"
	}

	return result, nil
}

// =============================================================================
// Streaming Types (for SSE parsing)
// =============================================================================

// anthropicStreamEvent represents a single SSE event from Anthropic.
type anthropicStreamEvent struct {
	Type string `json:"type"`
}

// anthropicContentBlockDelta contains delta content for streaming.
type anthropicContentBlockDelta struct {
	Type  string                `json:"type"`
	Index int                   `json:"index"`
	Delta anthropicDeltaContent `json:"delta"`
}

// anthropicDeltaContent contains the actual text delta.
type anthropicDeltaContent struct {
	Type     string `json:"type"` // "text_delta" or "thinking_delta"
	Text     string `json:"text,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

// anthropicMessageDelta contains the message-level delta (stop reason, etc).
type anthropicMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
}

// anthropicStreamError represents an error event in the stream.
type anthropicStreamError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// =============================================================================
// Streaming Implementation
// =============================================================================

// ChatStream implements streaming chat for the LLMClient interface.
//
// # Description
//
// Sends a chat request to Anthropic with streaming enabled, then reads
// the SSE response line-by-line and calls the callback for each token.
// Handles both regular text tokens and thinking tokens.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout.
//   - messages: Conversation history.
//   - params: Generation parameters.
//   - callback: Called for each streaming event.
//
// # Outputs
//
//   - error: Non-nil on network failure, API error, or callback abort.
//
// # Examples
//
//	err := client.ChatStream(ctx, messages, params, func(e StreamEvent) error {
//	    if e.Type == StreamEventToken {
//	        fmt.Print(e.Content)
//	    }
//	    return nil
//	})
//
// # Limitations
//
//   - Requires valid Anthropic API key
//   - Timeout applies to entire stream duration
//
// # Assumptions
//
//   - Anthropic API is available
//   - Network is stable for stream duration
func (a *AnthropicClient) ChatStream(
	ctx context.Context,
	messages []datatypes.Message,
	params GenerationParams,
	callback StreamCallback,
) error {
	// Build the streaming request (reuse logic from Chat)
	reqPayload, err := a.buildStreamRequest(messages, params)
	if err != nil {
		return fmt.Errorf("anthropic: building stream request: %w", err)
	}

	// Create HTTP request
	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return fmt.Errorf("anthropic: marshaling stream request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		return fmt.Errorf("anthropic: creating stream HTTP request: %w", err)
	}

	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")

	slog.Debug("Sending streaming request to Anthropic", "model", a.model)

	// Use a longer timeout for streaming
	streamClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := streamClient.Do(req)
	if err != nil {
		// Send error event to callback
		_ = callback(StreamEvent{Type: StreamEventError, Error: err.Error()})
		return fmt.Errorf("anthropic: stream HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("anthropic: reading stream error body (status %d): %w", resp.StatusCode, readErr)
		}
		errMsg := fmt.Sprintf("anthropic: stream API returned status %d", resp.StatusCode)
		_ = callback(StreamEvent{Type: StreamEventError, Error: errMsg})
		return fmt.Errorf("%s: %s", errMsg, SafeLogString(string(bodyBytes)))
	}

	// Process SSE stream
	return a.processSSEStream(ctx, resp.Body, callback)
}

// buildStreamRequest creates the Anthropic request payload with streaming enabled.
//
// # Description
//
// Builds the request payload similar to Chat but with Stream: true.
// Extracts system prompts and converts messages to Anthropic format.
//
// # Inputs
//
//   - messages: Conversation history.
//   - params: Generation parameters.
//
// # Outputs
//
//   - anthropicRequest: Request payload ready for JSON marshaling.
//   - error: Non-nil if construction fails.
func (a *AnthropicClient) buildStreamRequest(
	messages []datatypes.Message,
	params GenerationParams,
) (anthropicRequest, error) {
	var apiMessages []anthropicMessage
	var systemPrompt string

	// Convert generic messages to Anthropic format
	for _, msg := range messages {
		if strings.ToLower(msg.Role) == "system" {
			systemPrompt = msg.Content
			continue
		}
		apiMessages = append(apiMessages, anthropicMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Handle System Prompt with Caching
	var systemBlocks []systemBlock
	if systemPrompt != "" {
		block := systemBlock{
			Type: "text",
			Text: systemPrompt,
		}
		if len(systemPrompt) > 1024 {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		systemBlocks = append(systemBlocks, block)
	}

	// Build Payload with streaming enabled
	reqPayload := anthropicRequest{
		Model:     a.model,
		Messages:  apiMessages,
		System:    systemBlocks,
		MaxTokens: 4096,
		Stream:    true, // Enable streaming
	}

	// Apply optional parameters
	if params.Temperature != nil {
		reqPayload.Temperature = params.Temperature
	}
	if params.TopP != nil {
		reqPayload.TopP = params.TopP
	}
	if params.TopK != nil {
		reqPayload.TopK = params.TopK
	}
	if len(params.Stop) > 0 {
		reqPayload.StopSeqs = params.Stop
	}

	// Handle tools
	if len(params.ToolDefinitions) > 0 {
		var tools []toolsDefinition
		toolBytes, marshalErr := json.Marshal(params.ToolDefinitions)
		if marshalErr != nil {
			return anthropicRequest{}, fmt.Errorf("anthropic: marshaling tool definitions: %w", marshalErr)
		}
		if unmarshalErr := json.Unmarshal(toolBytes, &tools); unmarshalErr != nil {
			return anthropicRequest{}, fmt.Errorf("anthropic: unmarshaling tool definitions: %w", unmarshalErr)
		}
		reqPayload.Tools = tools
	}

	// Enable Thinking if requested
	if params.EnableThinking {
		reqPayload.Thinking = &thinkingParams{
			Type:         "enabled",
			BudgetTokens: params.BudgetTokens,
		}
		minRequired := params.BudgetTokens + 2048
		if reqPayload.MaxTokens < minRequired {
			reqPayload.MaxTokens = minRequired
		}
	}

	return reqPayload, nil
}

// processSSEStream reads and processes the SSE event stream.
//
// # Description
//
// Reads the SSE stream line-by-line, parses events, and calls the
// callback for token and thinking events. Handles errors gracefully
// by calling the callback with an error event.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - body: HTTP response body containing SSE events.
//   - callback: Called for each streaming event.
//
// # Outputs
//
//   - error: Non-nil on parse error, stream error, or callback abort.
func (a *AnthropicClient) processSSEStream(
	ctx context.Context,
	body io.Reader,
	callback StreamCallback,
) error {
	scanner := bufio.NewScanner(body)
	var eventType string
	var dataBuffer strings.Builder

	for scanner.Scan() {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			_ = callback(StreamEvent{Type: StreamEventError, Error: "stream cancelled"})
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		// Empty line signals end of event
		if line == "" {
			if dataBuffer.Len() > 0 && eventType != "" {
				if err := a.handleSSEEvent(eventType, dataBuffer.String(), callback); err != nil {
					return err
				}
				dataBuffer.Reset()
				eventType = ""
			}
			continue
		}

		// Parse SSE format
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataBuffer.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}

	if err := scanner.Err(); err != nil {
		_ = callback(StreamEvent{Type: StreamEventError, Error: err.Error()})
		return fmt.Errorf("anthropic: stream read error: %w", err)
	}

	return nil
}

// handleSSEEvent processes a single SSE event.
//
// # Description
//
// Parses the SSE event data and calls the appropriate callback based
// on event type. Handles content_block_delta (tokens), error events,
// and message completion.
//
// # Inputs
//
//   - eventType: SSE event type (content_block_delta, error, etc.)
//   - data: JSON data payload.
//   - callback: Callback to invoke.
//
// # Outputs
//
//   - error: Non-nil on parse error or callback error.
func (a *AnthropicClient) handleSSEEvent(
	eventType string,
	data string,
	callback StreamCallback,
) error {
	switch eventType {
	case "content_block_delta":
		var delta anthropicContentBlockDelta
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			slog.Warn("Failed to parse content_block_delta", "error", err, "data", data)
			return nil // Don't fail on parse errors, continue stream
		}

		// Determine event type based on delta type
		switch delta.Delta.Type {
		case "text_delta":
			if delta.Delta.Text != "" {
				if err := callback(StreamEvent{
					Type:    StreamEventToken,
					Content: delta.Delta.Text,
				}); err != nil {
					return fmt.Errorf("callback error: %w", err)
				}
			}
		case "thinking_delta":
			if delta.Delta.Thinking != "" {
				if err := callback(StreamEvent{
					Type:    StreamEventThinking,
					Content: delta.Delta.Thinking,
				}); err != nil {
					return fmt.Errorf("callback error: %w", err)
				}
			}
		}

	case "error":
		var streamErr anthropicStreamError
		if err := json.Unmarshal([]byte(data), &streamErr); err != nil {
			slog.Warn("Failed to parse error event", "error", err, "data", data)
			_ = callback(StreamEvent{Type: StreamEventError, Error: "stream error"})
			return fmt.Errorf("stream error: %s", data)
		}
		errMsg := fmt.Sprintf("%s: %s", streamErr.Error.Type, SafeLogString(streamErr.Error.Message))
		_ = callback(StreamEvent{Type: StreamEventError, Error: errMsg})
		return fmt.Errorf("anthropic: stream error: %s", errMsg)

	case "message_start", "content_block_start", "content_block_stop", "message_delta", "message_stop", "ping":
		// These are informational events, ignore them
		slog.Debug("Received SSE event", "type", eventType)

	default:
		slog.Debug("Unknown SSE event type", "type", eventType, "data", data)
	}

	return nil
}
