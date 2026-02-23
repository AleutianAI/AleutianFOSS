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
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// AnthropicAgentAdapter adapts AnthropicClient to the agent's Client interface.
//
// Description:
//
//	Wraps the existing AnthropicClient to provide LLM capabilities for the
//	agent loop using Anthropic Claude models. Converts between agent message
//	format and Anthropic's datatypes.Message format.
//
// Thread Safety: AnthropicAgentAdapter is safe for concurrent use.
type AnthropicAgentAdapter struct {
	client *llm.AnthropicClient
	model  string
}

// NewAnthropicAgentAdapter creates a new AnthropicAgentAdapter.
//
// Inputs:
//   - client: The AnthropicClient to wrap. Must not be nil.
//   - model: The model name for identification.
//
// Outputs:
//   - *AnthropicAgentAdapter: The configured adapter.
func NewAnthropicAgentAdapter(client *llm.AnthropicClient, model string) *AnthropicAgentAdapter {
	return &AnthropicAgentAdapter{
		client: client,
		model:  model,
	}
}

// Complete implements Client.Complete for Anthropic.
func (a *AnthropicAgentAdapter) Complete(ctx context.Context, request *Request) (*Response, error) {
	if request == nil {
		slog.Warn("AnthropicAgentAdapter.Complete called with nil request")
		return &Response{Content: "", StopReason: "end"}, nil
	}

	// Use ChatWithTools path when tools are provided
	if len(request.Tools) > 0 {
		return a.completeWithTools(ctx, request)
	}

	messages := a.convertMessages(request)

	slog.Info("AnthropicAgentAdapter sending request",
		slog.String("model", a.model),
		slog.Int("message_count", len(messages)),
		slog.Int("tool_count", len(request.Tools)),
	)

	params := a.buildParams(request)
	startTime := time.Now()

	// Call Anthropic Chat
	content, err := a.client.Chat(ctx, messages, params)
	if err != nil {
		return nil, err
	}

	duration := time.Since(startTime)

	if len(strings.TrimSpace(content)) == 0 {
		return nil, &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(messages),
			Model:        a.model,
		}
	}

	return &Response{
		Content:      content,
		StopReason:   "end",
		TokensUsed:   estimateTokens(content),
		InputTokens:  estimateInputTokens(messages),
		OutputTokens: estimateTokens(content),
		Duration:     duration,
		Model:        a.model,
	}, nil
}

// Name implements Client.Name.
func (a *AnthropicAgentAdapter) Name() string {
	return "anthropic"
}

// Model implements Client.Model.
func (a *AnthropicAgentAdapter) Model() string {
	return a.model
}

// convertMessages converts agent messages to Anthropic format.
func (a *AnthropicAgentAdapter) convertMessages(request *Request) []datatypes.Message {
	messages := make([]datatypes.Message, 0, len(request.Messages)+1)

	if request.SystemPrompt != "" {
		messages = append(messages, datatypes.Message{
			Role:    "system",
			Content: request.SystemPrompt,
		})
	}

	for _, msg := range request.Messages {
		content := msg.Content
		if msg.Role == "tool" && len(msg.ToolResults) > 0 {
			var parts []string
			for _, tr := range msg.ToolResults {
				if tr.Content != "" {
					parts = append(parts, tr.Content)
				}
			}
			if len(parts) > 0 {
				content = strings.Join(parts, "\n")
			}
		}

		// Anthropic doesn't support "tool" role, convert to "user"
		role := msg.Role
		if role == "tool" {
			role = "user"
		}

		messages = append(messages, datatypes.Message{
			Role:    role,
			Content: content,
		})
	}

	return messages
}

// completeWithTools handles requests with tool definitions using ChatWithTools.
//
// Description:
//
//	Converts tool definitions and messages to generic LLM format,
//	calls ChatWithTools, and converts the result back to agent format.
//
// Inputs:
//   - ctx: Context for cancellation and timeout.
//   - request: The completion request with tools.
//
// Outputs:
//   - *Response: The LLM response with tool calls if present.
//   - error: Non-nil if the request failed.
func (a *AnthropicAgentAdapter) completeWithTools(ctx context.Context, request *Request) (*Response, error) {
	chatMessages := convertToChat(request)
	toolDefs := convertToolDefs(request.Tools)
	params := a.buildParams(request)
	startTime := time.Now()

	slog.Info("AnthropicAgentAdapter sending request with tools",
		slog.String("model", a.model),
		slog.Int("message_count", len(chatMessages)),
		slog.Int("tool_count", len(toolDefs)),
	)

	result, err := a.client.ChatWithTools(ctx, chatMessages, params, toolDefs)
	if err != nil {
		return nil, err
	}

	duration := time.Since(startTime)

	if len(strings.TrimSpace(result.Content)) == 0 && len(result.ToolCalls) == 0 {
		return nil, &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(chatMessages),
			Model:        a.model,
		}
	}

	var agentToolCalls []ToolCall
	for _, tc := range result.ToolCalls {
		agentToolCalls = append(agentToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.ArgumentsString(),
		})
	}

	return &Response{
		Content:      result.Content,
		ToolCalls:    agentToolCalls,
		StopReason:   result.StopReason,
		TokensUsed:   estimateTokens(result.Content),
		InputTokens:  estimateInputTokensChat(chatMessages),
		OutputTokens: estimateTokens(result.Content),
		Duration:     duration,
		Model:        a.model,
	}, nil
}

// buildParams converts agent request parameters to Anthropic format.
func (a *AnthropicAgentAdapter) buildParams(request *Request) llm.GenerationParams {
	params := llm.GenerationParams{}

	if request.MaxTokens > 0 {
		maxTokens := request.MaxTokens
		params.MaxTokens = &maxTokens
	}

	if request.Temperature >= 0 {
		temp := float32(request.Temperature)
		params.Temperature = &temp
	}

	if len(request.StopSequences) > 0 {
		params.Stop = request.StopSequences
	}

	if request.ToolChoice != nil {
		params.ToolChoice = convertAgentToolChoice(request.ToolChoice)
	}

	return params
}
