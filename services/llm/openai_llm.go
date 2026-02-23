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
	"os"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// =============================================================================
// OpenAI Wire Types
// =============================================================================

const defaultOpenAIBaseURL = "https://api.openai.com/v1/chat/completions"

type openaiRequest struct {
	Model               string          `json:"model"`
	Messages            []openaiMessage `json:"messages"`
	Temperature         *float32        `json:"temperature,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	TopP                *float32        `json:"top_p,omitempty"`
	Stop                []string        `json:"stop,omitempty"`
	Tools               []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openaiResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Choices []openaiChoice `json:"choices"`
	Error   *openaiError   `json:"error,omitempty"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Tool-related wire types for OpenAI function calling.
type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiCallFunction `json:"function"`
}

type openaiCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// =============================================================================
// Client Implementation
// =============================================================================

// OpenAIClient implements LLMClient for OpenAI models using raw net/http.
//
// Description:
//
//	Uses the OpenAI Chat Completions REST API directly without third-party SDKs.
//	Supports text generation, multi-turn conversations, and function calling.
//
// Thread Safety: OpenAIClient is safe for concurrent use.
type OpenAIClient struct {
	httpClient *http.Client
	apiKey     string
	model      string
	baseURL    string
}

// NewOpenAIClient creates a new OpenAIClient from environment variables.
//
// Description:
//
//	Reads OPENAI_API_KEY and OPENAI_MODEL from the environment.
//	Defaults to "gpt-4o-mini" if OPENAI_MODEL is not set.
//
// Outputs:
//   - *OpenAIClient: The configured client.
//   - error: Non-nil if OPENAI_API_KEY is missing.
//
// NewOpenAIClientWithConfig creates an OpenAIClient with explicit configuration.
//
// Description:
//
//	Creates an OpenAIClient without reading environment variables. Useful
//	for testing with mock servers or when configuration comes from a source
//	other than environment variables.
//
// Inputs:
//   - apiKey: The OpenAI API key.
//   - model: The model name (e.g., "gpt-4o").
//   - baseURL: The base URL for API requests.
//
// Outputs:
//   - *OpenAIClient: The configured client.
func NewOpenAIClientWithConfig(apiKey, model, baseURL string) *OpenAIClient {
	return &OpenAIClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
	}
}

func NewOpenAIClient() (*OpenAIClient, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	model := os.Getenv("OPENAI_MODEL")
	if apiKey == "" {
		slog.Warn("OpenAI API Key is empty. OpenAI Client will not function.")
		return nil, fmt.Errorf("openai: API key is missing (OPENAI_API_KEY)")
	}
	if model == "" {
		model = "gpt-4o-mini"
		slog.Warn("OPENAI_MODEL not set, defaulting to gpt-4o-mini")
	}
	slog.Info("Initializing OpenAI client", "model", model)
	return &OpenAIClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    defaultOpenAIBaseURL,
	}, nil
}

// Generate implements the LLMClient interface.
func (o *OpenAIClient) Generate(ctx context.Context, prompt string, params GenerationParams) (string, error) {
	slog.Debug("Generating text via OpenAI", "model", o.model)
	systemRoleContent := os.Getenv("SYSTEM_ROLE_PROMPT_PERSONA")
	if systemRoleContent == "" {
		systemRoleContent = "You are a helpful assistant."
	}
	messages := []datatypes.Message{
		{Role: "system", Content: systemRoleContent},
		{Role: "user", Content: prompt},
	}
	return o.Chat(ctx, messages, params)
}

// Chat implements LLMClient.Chat using the OpenAI chat completions API.
//
// Description:
//
//	Converts datatypes.Message to OpenAI format and sends a chat completion
//	request via raw HTTP. Handles system, user, and assistant roles.
//
// Inputs:
//   - ctx: Context for cancellation and timeout.
//   - messages: Conversation history.
//   - params: Generation parameters.
//
// Outputs:
//   - string: The assistant's response text.
//   - error: Non-nil if the request fails.
//
// Thread Safety: This method is safe for concurrent use.
func (o *OpenAIClient) Chat(ctx context.Context, messages []datatypes.Message, params GenerationParams) (string, error) {
	model := o.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	slog.Debug("Chat via OpenAI", slog.String("model", model), slog.Int("messages", len(messages)))

	// Convert messages to OpenAI format
	oaiMessages := make([]openaiMessage, 0, len(messages))
	for _, msg := range messages {
		role := msg.Role
		switch role {
		case "system", "user", "assistant":
			// valid roles, keep as-is
		default:
			slog.Warn("OpenAI: unknown message role, mapping to user",
				slog.String("unknown_role", role),
				slog.String("model", model),
			)
			role = "user"
		}
		oaiMessages = append(oaiMessages, openaiMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	reqPayload := openaiRequest{
		Model:    model,
		Messages: oaiMessages,
	}
	if params.Temperature != nil {
		reqPayload.Temperature = params.Temperature
	}
	if params.MaxTokens != nil {
		reqPayload.MaxCompletionTokens = params.MaxTokens
	}
	if params.TopP != nil {
		reqPayload.TopP = params.TopP
	}
	if len(params.Stop) > 0 {
		reqPayload.Stop = params.Stop
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("openai: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("openai: creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	slog.Debug("Sending request to OpenAI", slog.String("model", model))

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("openai: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("openai: API error: %s - %s", apiResp.Error.Type, SafeLogString(apiResp.Error.Message))
	}

	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("openai: returned no choices")
	}

	slog.Debug("Received OpenAI chat response",
		slog.String("finish_reason", apiResp.Choices[0].FinishReason),
		slog.Int("response_len", len(apiResp.Choices[0].Message.Content)),
	)

	return apiResp.Choices[0].Message.Content, nil
}

// ChatWithTools sends a chat request with tool definitions and returns tool calls.
//
// Description:
//
//	Extends Chat to support OpenAI's function calling API. Converts generic
//	ToolDef and ChatMessage types to OpenAI wire format, sends the request,
//	and parses tool_calls from the response.
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
func (o *OpenAIClient) ChatWithTools(ctx context.Context, messages []ChatMessage,
	params GenerationParams, tools []ToolDef) (*ChatWithToolsResult, error) {

	model := o.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	slog.Debug("ChatWithTools via OpenAI",
		slog.String("model", model),
		slog.Int("messages", len(messages)),
		slog.Int("tools", len(tools)),
	)

	// Convert ChatMessage to OpenAI format
	oaiMessages := make([]openaiMessage, 0, len(messages))
	for _, msg := range messages {
		oaiMsg := openaiMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Handle tool result messages
		if msg.Role == "tool" && msg.ToolCallID != "" {
			oaiMsg.ToolCallID = msg.ToolCallID
		}

		// Handle assistant messages with tool calls
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openaiCallFunction{
						Name:      tc.Name,
						Arguments: tc.ArgumentsString(),
					},
				})
			}
		}

		oaiMessages = append(oaiMessages, oaiMsg)
	}

	// Convert ToolDef to OpenAI format
	oaiTools := make([]openaiTool, 0, len(tools))
	for _, td := range tools {
		oaiTools = append(oaiTools, openaiTool{
			Type: "function",
			Function: openaiFunction{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			},
		})
	}

	reqPayload := openaiRequest{
		Model:    model,
		Messages: oaiMessages,
		Tools:    oaiTools,
	}
	if params.Temperature != nil {
		reqPayload.Temperature = params.Temperature
	}
	if params.MaxTokens != nil {
		reqPayload.MaxCompletionTokens = params.MaxTokens
	}
	if params.TopP != nil {
		reqPayload.TopP = params.TopP
	}
	if len(params.Stop) > 0 {
		reqPayload.Stop = params.Stop
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("openai: marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("openai: creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	var apiResp openaiResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("openai: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("openai: API error: %s - %s", apiResp.Error.Type, SafeLogString(apiResp.Error.Message))
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai: returned no choices")
	}

	choice := apiResp.Choices[0]
	result := &ChatWithToolsResult{
		Content: choice.Message.Content,
	}

	// Convert tool calls
	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	if len(result.ToolCalls) > 0 {
		result.StopReason = "tool_use"
	} else {
		result.StopReason = "end"
	}

	return result, nil
}

// ChatStream streams a conversation response token-by-token.
//
// Description:
//
//	Currently not implemented for OpenAIClient. Returns an error
//	indicating that streaming is not supported for this backend.
//
// Inputs:
//   - ctx: Context for cancellation and timeout.
//   - messages: Conversation history.
//   - params: Generation parameters.
//   - callback: Callback for streaming events.
//
// Outputs:
//   - error: Always returns an error.
//
// Limitations:
//   - Streaming is not implemented for OpenAI backend.
func (o *OpenAIClient) ChatStream(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, callback StreamCallback) error {
	return fmt.Errorf("openai: streaming not implemented")
}
