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
	"log/slog"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// OllamaAdapter adapts services/llm.OllamaClient to the agent's Client interface.
//
// Description:
//
//	OllamaAdapter wraps the existing OllamaClient to provide LLM capabilities
//	for the agent loop. This allows Trace to use local Ollama models
//	(like gpt-oss:20b) for code assistance.
//
// Thread Safety:
//
//	OllamaAdapter is safe for concurrent use.
type OllamaAdapter struct {
	client *llm.OllamaClient
	model  string
}

// NewOllamaAdapter creates a new OllamaAdapter.
//
// Description:
//
//	Creates an adapter wrapping the provided OllamaClient.
//
// Inputs:
//
//	client - The OllamaClient to wrap. Must not be nil.
//	model - The model name for identification.
//
// Outputs:
//
//	*OllamaAdapter - The configured adapter.
//
// Example:
//
//	ollamaClient, _ := llm.NewOllamaClient()
//	adapter := NewOllamaAdapter(ollamaClient, "gpt-oss:20b")
func NewOllamaAdapter(client *llm.OllamaClient, model string) *OllamaAdapter {
	return &OllamaAdapter{
		client: client,
		model:  model,
	}
}

// Complete implements Client.
//
// Description:
//
//	Sends a completion request to Ollama and returns the response.
//	Converts between agent message format and Ollama's datatypes.Message format.
//	If tools are provided in the request, uses ChatWithTools to enable function calling.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	request - The completion request.
//
// Outputs:
//
//	*Response - The LLM response.
//	error - Non-nil if the request failed.
//
// Thread Safety: This method is safe for concurrent use.
func (a *OllamaAdapter) Complete(ctx context.Context, request *Request) (*Response, error) {
	if request == nil {
		slog.Warn("OllamaAdapter.Complete called with nil request")
		return &Response{
			Content:    "",
			StopReason: "end",
		}, nil
	}

	// Convert agent messages to datatypes.Message format
	messages := a.convertMessages(request)

	// Build generation params
	params := a.buildParams(request)

	// Use ChatWithTools if tools are provided
	if len(request.Tools) > 0 {
		return a.completeWithTools(ctx, messages, params, request.Tools)
	}

	// Create OTel span for non-tools path
	ctx, span := otel.Tracer(llmTracerName).Start(ctx, "agent.llm.OllamaAdapter.Complete",
		trace.WithAttributes(
			attribute.String("provider", "ollama"),
			attribute.String("model", a.model),
			attribute.Int("message_count", len(messages)),
			attribute.Int("tool_count", 0),
		),
	)
	defer span.End()

	// Track active requests
	incActiveRequests("ollama")
	defer decActiveRequests("ollama")

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())
	logger.Info("OllamaAdapter sending request",
		slog.String("model", a.model),
		slog.Int("message_count", len(messages)),
		slog.Int("tool_count", len(request.Tools)),
		slog.String("system_prompt_preview", truncate(request.SystemPrompt, 100)),
	)

	// Log each message for debugging
	for i, msg := range messages {
		logger.Debug("OllamaAdapter message",
			slog.Int("index", i),
			slog.String("role", msg.Role),
			slog.Int("content_len", len(msg.Content)),
			slog.String("content_preview", truncate(msg.Content, 300)),
		)
	}

	// Call Ollama without tools
	startTime := time.Now()
	content, err := a.client.Chat(ctx, messages, params)
	duration := time.Since(startTime)

	if err != nil {
		logger.Error("OllamaAdapter.Chat failed",
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recordLLMMetrics("ollama", duration, 0, 0, err)
		return nil, err
	}

	logger.Info("OllamaAdapter received response",
		slog.Int("content_len", len(content)),
		slog.String("content_preview", truncate(content, 200)),
		slog.Duration("duration", duration),
	)

	// Handle empty response
	if len(strings.TrimSpace(content)) == 0 {
		logger.Warn("OllamaAdapter received empty response",
			slog.Duration("duration", duration),
			slog.Int("message_count", len(messages)),
		)
		emptyErr := &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(messages),
			Model:        a.model,
		}
		span.RecordError(emptyErr)
		span.SetStatus(codes.Error, emptyErr.Error())
		recordLLMMetrics("ollama", duration, 0, 0, emptyErr)
		return nil, emptyErr
	}

	inputTokens := estimateInputTokens(messages)
	outputTokens := estimateTokens(content)

	span.AddEvent("response_received", trace.WithAttributes(
		attribute.Int("input_tokens", inputTokens),
		attribute.Int("output_tokens", outputTokens),
		attribute.String("stop_reason", "end"),
	))

	recordLLMMetrics("ollama", duration, inputTokens, outputTokens, nil)

	// Build CRS TraceStep
	traceStep := crs.NewTraceStepBuilder().
		WithAction("provider_call").
		WithTarget(a.model).
		WithTool("OllamaAdapter").
		WithDuration(duration).
		WithMetadata("provider", "ollama").
		WithMetadata("tokens_sent", fmt.Sprintf("%d", inputTokens)).
		WithMetadata("tokens_received", fmt.Sprintf("%d", outputTokens)).
		WithMetadata("model", a.model).
		Build()

	// Build response
	return &Response{
		Content:      content,
		StopReason:   "end",
		TokensUsed:   inputTokens + outputTokens,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     duration,
		Model:        a.model,
		TraceStep:    &traceStep,
	}, nil
}

// completeWithTools handles requests with tool definitions.
//
// Description:
//
//	Converts tool definitions to Ollama format, calls ChatWithTools,
//	and parses tool calls from the response.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	messages - Converted messages in Ollama format.
//	params - Generation parameters.
//	toolDefs - Tool definitions from the request.
//
// Outputs:
//
//	*Response - The LLM response with tool calls if present.
//	error - Non-nil if the request failed.
func (a *OllamaAdapter) completeWithTools(
	ctx context.Context,
	messages []datatypes.Message,
	params llm.GenerationParams,
	toolDefs []tools.ToolDefinition,
) (*Response, error) {
	// Convert tool definitions to Ollama format
	ollamaTools := convertToolDefinitions(toolDefs)

	// Create OTel span
	ctx, span := otel.Tracer(llmTracerName).Start(ctx, "agent.llm.OllamaAdapter.CompleteWithTools",
		trace.WithAttributes(
			attribute.String("provider", "ollama"),
			attribute.String("model", a.model),
			attribute.Int("message_count", len(messages)),
			attribute.Int("tool_count", len(ollamaTools)),
		),
	)
	defer span.End()

	// Track active requests
	incActiveRequests("ollama")
	defer decActiveRequests("ollama")

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())
	logger.Debug("OllamaAdapter calling ChatWithTools",
		slog.Int("num_tools", len(ollamaTools)),
	)

	// Call Ollama with tools
	startTime := time.Now()
	result, err := a.client.ChatWithTools(ctx, messages, params, ollamaTools)
	duration := time.Since(startTime)

	if err != nil {
		logger.Error("OllamaAdapter.ChatWithTools failed",
			slog.String("error", err.Error()),
		)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		recordLLMMetrics("ollama", duration, 0, 0, err)
		return nil, err
	}

	logger.Info("OllamaAdapter received tool response",
		slog.Int("content_len", len(result.Content)),
		slog.Int("tool_calls", len(result.ToolCalls)),
		slog.String("stop_reason", result.StopReason),
		slog.Duration("duration", duration),
	)

	// Handle empty response with no tool calls
	if len(strings.TrimSpace(result.Content)) == 0 && len(result.ToolCalls) == 0 {
		logger.Warn("OllamaAdapter received empty response with no tool calls",
			slog.Duration("duration", duration),
			slog.Int("message_count", len(messages)),
		)
		emptyErr := &EmptyResponseError{
			Duration:     duration,
			MessageCount: len(messages),
			Model:        a.model,
		}
		span.RecordError(emptyErr)
		span.SetStatus(codes.Error, emptyErr.Error())
		recordLLMMetrics("ollama", duration, 0, 0, emptyErr)
		return nil, emptyErr
	}

	// Convert generic tool calls to agent format
	var agentToolCalls []ToolCall
	for _, tc := range result.ToolCalls {
		agentToolCalls = append(agentToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.ArgumentsString(),
		})
	}

	inputTokens := estimateInputTokens(messages)
	outputTokens := estimateTokens(result.Content)

	span.AddEvent("response_received", trace.WithAttributes(
		attribute.Int("input_tokens", inputTokens),
		attribute.Int("output_tokens", outputTokens),
		attribute.Int("tool_calls", len(agentToolCalls)),
		attribute.String("stop_reason", result.StopReason),
	))

	recordLLMMetrics("ollama", duration, inputTokens, outputTokens, nil)

	// Build CRS TraceStep
	traceStep := crs.NewTraceStepBuilder().
		WithAction("provider_call").
		WithTarget(a.model).
		WithTool("OllamaAdapter").
		WithDuration(duration).
		WithMetadata("provider", "ollama").
		WithMetadata("tokens_sent", fmt.Sprintf("%d", inputTokens)).
		WithMetadata("tokens_received", fmt.Sprintf("%d", outputTokens)).
		WithMetadata("model", a.model).
		Build()

	return &Response{
		Content:      result.Content,
		ToolCalls:    agentToolCalls,
		StopReason:   result.StopReason,
		TokensUsed:   inputTokens + outputTokens,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Duration:     duration,
		Model:        a.model,
		TraceStep:    &traceStep,
	}, nil
}

// convertToolDefinitions converts agent tool definitions to Ollama format.
//
// Description:
//
//	Maps tools.ToolDefinition to llm.OllamaTool for the Ollama API.
//	Preserves parameter types, descriptions, and required fields.
//
// Inputs:
//
//	defs - Tool definitions in agent format.
//
// Outputs:
//
//	[]llm.OllamaTool - Tools in Ollama API format.
func convertToolDefinitions(defs []tools.ToolDefinition) []llm.OllamaTool {
	if len(defs) == 0 {
		return nil
	}

	result := make([]llm.OllamaTool, 0, len(defs))
	for _, def := range defs {
		// Convert parameters
		properties := make(map[string]llm.OllamaParamDef)
		var required []string

		for paramName, paramDef := range def.Parameters {
			properties[paramName] = llm.OllamaParamDef{
				Type:        string(paramDef.Type),
				Description: paramDef.Description,
				Enum:        paramDef.Enum,
				Default:     paramDef.Default,
			}
			if paramDef.Required {
				required = append(required, paramName)
			}
		}

		result = append(result, llm.OllamaTool{
			Type: "function",
			Function: llm.OllamaToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters: llm.OllamaToolParameters{
					Type:       "object",
					Properties: properties,
					Required:   required,
				},
			},
		})
	}

	return result
}

// truncate truncates a string to maxLen chars.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Name implements Client.
func (a *OllamaAdapter) Name() string {
	return "ollama"
}

// Model implements Client.
func (a *OllamaAdapter) Model() string {
	return a.model
}

// convertMessages converts agent messages to Ollama format.
//
// Description:
//
//	Converts llm.Message to datatypes.Message for Ollama API.
//	IMPORTANT: For "tool" role messages, the content is stored in ToolResults,
//	not in the Content field. This method extracts the actual content.
//
// Inputs:
//
//	request - The agent request containing messages.
//
// Outputs:
//
//	[]datatypes.Message - Messages in Ollama format.
func (a *OllamaAdapter) convertMessages(request *Request) []datatypes.Message {
	messages := make([]datatypes.Message, 0, len(request.Messages)+1)

	// Add system prompt as first message if present
	if request.SystemPrompt != "" {
		messages = append(messages, datatypes.Message{
			Role:    "system",
			Content: request.SystemPrompt,
		})
	}

	// Convert each message
	for _, msg := range request.Messages {
		content := msg.Content

		// BUG FIX: For tool messages, content is in ToolResults, not Content field.
		// The client.go BuildRequest function stores tool results in ToolResults[].Content,
		// but the conversion was previously ignoring this and reading empty msg.Content.
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

		messages = append(messages, datatypes.Message{
			Role:    msg.Role,
			Content: content,
		})
	}

	return messages
}

// buildParams converts agent request parameters to Ollama format.
//
// Inputs:
//
//	request - The agent request containing parameters.
//
// Outputs:
//
//	llm.GenerationParams - Parameters in Ollama format.
func (a *OllamaAdapter) buildParams(request *Request) llm.GenerationParams {
	params := llm.GenerationParams{}

	if request.MaxTokens > 0 {
		maxTokens := request.MaxTokens
		params.MaxTokens = &maxTokens
	}

	if request.Temperature > 0 {
		temp := float32(request.Temperature)
		params.Temperature = &temp
	}

	if len(request.StopSequences) > 0 {
		params.Stop = request.StopSequences
	}

	// Pass through multi-model support fields
	if request.ModelOverride != "" {
		params.ModelOverride = request.ModelOverride
	}

	if request.KeepAlive != "" {
		params.KeepAlive = request.KeepAlive
	}

	// Set context window size for main agent (64K for analysis tasks).
	// This MUST be passed on every request to prevent Ollama from
	// resetting to default 4096 context window.
	numCtx := 65536 // 64K tokens for main agent
	params.NumCtx = &numCtx

	// Set keep_alive to prevent Ollama from unloading the model between requests.
	// This is critical for multi-model operation to prevent thrashing.
	if params.KeepAlive == "" {
		params.KeepAlive = "24h"
	}

	// Pass through tool_choice for API-level tool forcing (cb_30a fix).
	// This enables the CB-28d-6 escalation logic (auto → any → tool) to work.
	if request.ToolChoice != nil {
		params.ToolChoice = convertAgentToolChoice(request.ToolChoice)
	}

	return params
}

// convertAgentToolChoice converts agent ToolChoice to shared llm.ToolChoice.
//
// Description:
//
//	Maps the agent's ToolChoice type to the shared llm.ToolChoice format
//	so it can be passed through to the Ollama client.
//
// Inputs:
//
//	tc - The agent ToolChoice to convert.
//
// Outputs:
//
//	*llm.ToolChoice - The converted format.
//
// Thread Safety: This function is safe for concurrent use.
func convertAgentToolChoice(tc *ToolChoice) *llm.ToolChoice {
	if tc == nil {
		return nil
	}

	return &llm.ToolChoice{
		Type: tc.Type,
		Name: tc.Name,
	}
}

// estimateTokens provides a rough token estimate.
//
// Description:
//
//	Estimates token count as ~4 characters per token.
//	This is a rough approximation; actual counts depend on the tokenizer.
//
// Inputs:
//
//	content - The text to estimate tokens for.
//
// Outputs:
//
//	int - Estimated token count.
func estimateTokens(content string) int {
	return len(content) / 4
}

// estimateInputTokens estimates input tokens from messages.
//
// Inputs:
//
//	messages - The input messages.
//
// Outputs:
//
//	int - Estimated input token count.
func estimateInputTokens(messages []datatypes.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
	}
	return total / 4
}
