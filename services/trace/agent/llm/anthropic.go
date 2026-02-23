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
	"fmt"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"log/slog"
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

	// Create OTel span
	ctx, span := otel.Tracer(llmTracerName).Start(ctx, "agent.llm.AnthropicAdapter.Complete",
		trace.WithAttributes(
			attribute.String("provider", "anthropic"),
			attribute.String("model", a.model),
			attribute.Int("message_count", len(request.Messages)),
			attribute.Int("tool_count", 0),
		),
	)
	defer span.End()

	// Track active requests
	incActiveRequests(ctx, "anthropic")
	defer decActiveRequests("anthropic")

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())

	messages := a.convertMessages(request)

	logger.Info("AnthropicAgentAdapter sending request",
		slog.String("model", a.model),
		slog.Int("message_count", len(messages)),
		slog.Int("tool_count", len(request.Tools)),
	)

	params := a.buildParams(request)
	startTime := time.Now()

	// Call Anthropic Chat
	content, err := a.client.Chat(ctx, messages, params)
	duration := time.Since(startTime)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recordLLMMetrics("anthropic", duration, 0, 0, err)
		return nil, err
	}

	if len(strings.TrimSpace(content)) == 0 {
		emptyErr := &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(messages),
			Model:        a.model,
		}
		span.RecordError(emptyErr)
		span.SetStatus(codes.Error, emptyErr.Error())
		recordLLMMetrics("anthropic", duration, 0, 0, emptyErr)
		return nil, emptyErr
	}

	inputTokens := estimateInputTokens(messages)
	outputTokens := estimateTokens(content)

	span.AddEvent("response_received", trace.WithAttributes(
		attribute.Int("input_tokens", inputTokens),
		attribute.Int("output_tokens", outputTokens),
		attribute.String("stop_reason", "end"),
	))

	recordLLMMetrics("anthropic", duration, inputTokens, outputTokens, nil)

	// Build CRS TraceStep
	traceStep := crs.NewTraceStepBuilder().
		WithAction("provider_call").
		WithTarget(a.model).
		WithTool("AnthropicAdapter").
		WithDuration(duration).
		WithMetadata("provider", "anthropic").
		WithMetadata("tokens_sent", fmt.Sprintf("%d", inputTokens)).
		WithMetadata("tokens_received", fmt.Sprintf("%d", outputTokens)).
		WithMetadata("model", a.model).
		Build()

	return &Response{
		Content:      content,
		StopReason:   "end",
		TokensUsed:   estimateTokens(content),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     duration,
		Model:        a.model,
		TraceStep:    &traceStep,
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

	// Create OTel span
	ctx, span := otel.Tracer(llmTracerName).Start(ctx, "agent.llm.AnthropicAdapter.CompleteWithTools",
		trace.WithAttributes(
			attribute.String("provider", "anthropic"),
			attribute.String("model", a.model),
			attribute.Int("message_count", len(chatMessages)),
			attribute.Int("tool_count", len(toolDefs)),
		),
	)
	defer span.End()

	// Track active requests
	incActiveRequests(ctx, "anthropic")
	defer decActiveRequests("anthropic")

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())
	logger.Info("AnthropicAgentAdapter sending request with tools",
		slog.String("model", a.model),
		slog.Int("message_count", len(chatMessages)),
		slog.Int("tool_count", len(toolDefs)),
	)

	startTime := time.Now()

	result, err := a.client.ChatWithTools(ctx, chatMessages, params, toolDefs)
	duration := time.Since(startTime)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recordLLMMetrics("anthropic", duration, 0, 0, err)
		return nil, err
	}

	if len(strings.TrimSpace(result.Content)) == 0 && len(result.ToolCalls) == 0 {
		emptyErr := &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(chatMessages),
			Model:        a.model,
		}
		span.RecordError(emptyErr)
		span.SetStatus(codes.Error, emptyErr.Error())
		recordLLMMetrics("anthropic", duration, 0, 0, emptyErr)
		return nil, emptyErr
	}

	var agentToolCalls []ToolCall
	for _, tc := range result.ToolCalls {
		agentToolCalls = append(agentToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.ArgumentsString(),
		})
	}

	inputTokens := estimateInputTokensChat(chatMessages)
	outputTokens := estimateTokens(result.Content)

	span.AddEvent("response_received", trace.WithAttributes(
		attribute.Int("input_tokens", inputTokens),
		attribute.Int("output_tokens", outputTokens),
		attribute.Int("tool_calls", len(agentToolCalls)),
		attribute.String("stop_reason", result.StopReason),
	))

	recordLLMMetrics("anthropic", duration, inputTokens, outputTokens, nil)

	// Build CRS TraceStep
	traceStep := crs.NewTraceStepBuilder().
		WithAction("provider_call").
		WithTarget(a.model).
		WithTool("AnthropicAdapter").
		WithDuration(duration).
		WithMetadata("provider", "anthropic").
		WithMetadata("tokens_sent", fmt.Sprintf("%d", inputTokens)).
		WithMetadata("tokens_received", fmt.Sprintf("%d", outputTokens)).
		WithMetadata("model", a.model).
		Build()

	return &Response{
		Content:      result.Content,
		ToolCalls:    agentToolCalls,
		StopReason:   result.StopReason,
		TokensUsed:   estimateTokens(result.Content),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     duration,
		Model:        a.model,
		TraceStep:    &traceStep,
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
