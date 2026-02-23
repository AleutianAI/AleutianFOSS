// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package llm provides the LLM client interface for the agent loop.
//
// This package defines the interface that LLM providers must implement
// to work with the agent. Actual implementations are injected at runtime.
//
// Thread Safety:
//
//	All types in this package are designed for concurrent use.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// Client defines the interface for LLM interactions.
//
// Implementations must be safe for concurrent use.
type Client interface {
	// Complete sends a prompt to the LLM and returns a response.
	//
	// Inputs:
	//   ctx - Context for cancellation and timeout
	//   request - The completion request
	//
	// Outputs:
	//   *Response - The LLM response
	//   error - Non-nil if the request failed
	Complete(ctx context.Context, request *Request) (*Response, error)

	// Name returns the provider name (e.g., "anthropic", "openai").
	Name() string

	// Model returns the model being used.
	Model() string
}

// ToolChoice specifies how the model should select tools.
//
// The tool_choice parameter controls whether and which tools the model calls.
// This enables forcing tool usage at the API level rather than relying on prompts.
type ToolChoice struct {
	// Type controls tool selection behavior:
	// - "auto": Model decides whether to call tools (default)
	// - "any": Model MUST call at least one tool
	// - "tool": Model MUST call the specific named tool
	// - "none": Model cannot call tools (text response only)
	Type string `json:"type"`

	// Name is required when Type is "tool". Specifies which tool to force.
	Name string `json:"name,omitempty"`
}

// ToolChoiceAuto allows the model to decide whether to call tools.
func ToolChoiceAuto() *ToolChoice {
	return &ToolChoice{Type: "auto"}
}

// ToolChoiceAny forces the model to call at least one tool.
func ToolChoiceAny() *ToolChoice {
	return &ToolChoice{Type: "any"}
}

// ToolChoiceRequired forces the model to call a specific tool by name.
func ToolChoiceRequired(toolName string) *ToolChoice {
	return &ToolChoice{Type: "tool", Name: toolName}
}

// ToolChoiceNone prevents the model from calling any tools.
func ToolChoiceNone() *ToolChoice {
	return &ToolChoice{Type: "none"}
}

// Request represents a completion request to the LLM.
type Request struct {
	// SystemPrompt is the system message.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Messages is the conversation history.
	Messages []Message `json:"messages"`

	// Tools defines available tools for the LLM.
	Tools []tools.ToolDefinition `json:"tools,omitempty"`

	// ToolChoice controls tool selection behavior.
	// If nil, defaults to "auto" (model decides).
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`

	// MaxTokens limits the response length.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0-1.0). Set to 0.0 for most
	// deterministic output. Set to a negative value (e.g., -1) to omit
	// from the request and use the provider's default. The Go zero value
	// (0.0) is treated as an explicit "most deterministic" setting.
	Temperature float64 `json:"temperature,omitempty"`

	// StopSequences defines sequences that stop generation.
	StopSequences []string `json:"stop_sequences,omitempty"`

	// ModelOverride allows using a different model for this request.
	// Used for multi-model scenarios (e.g., tool routing with a fast model).
	// Empty string means use the client's default model.
	ModelOverride string `json:"model_override,omitempty"`

	// KeepAlive controls how long the model stays loaded in VRAM.
	// Values: "-1" = infinite, "5m" = 5 minutes (default), "0" = unload immediately.
	// Used to prevent model thrashing when alternating between models.
	KeepAlive string `json:"keep_alive,omitempty"`
}

// Message represents a conversation message.
type Message struct {
	// Role is "user", "assistant", or "system".
	Role string `json:"role"`

	// Content is the text content.
	Content string `json:"content"`

	// ToolCalls contains tool invocations (for assistant messages).
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolResults contains tool results (for tool messages).
	ToolResults []ToolCallResult `json:"tool_results,omitempty"`
}

// ToolCall represents a tool invocation by the LLM.
type ToolCall struct {
	// ID is a unique identifier for this call.
	ID string `json:"id"`

	// Name is the tool name.
	Name string `json:"name"`

	// Arguments are the tool arguments as JSON.
	Arguments string `json:"arguments"`
}

// ToolCallResult contains the result of a tool call.
type ToolCallResult struct {
	// ToolCallID links back to the tool call.
	ToolCallID string `json:"tool_call_id"`

	// Content is the result content.
	Content string `json:"content"`

	// IsError indicates if this is an error result.
	IsError bool `json:"is_error,omitempty"`
}

// EmptyResponseError indicates the LLM returned an empty response.
//
// This typically indicates a problem with the model, context, or request.
// Empty responses should not silently pass through as they cause downstream
// issues in the agent loop.
//
// Fixed in cb_30a.
type EmptyResponseError struct {
	// Duration is how long the request took before returning empty.
	Duration time.Duration

	// MessageCount is the number of input messages sent.
	MessageCount int

	// Model is the model that returned the empty response.
	Model string
}

// Error implements the error interface.
func (e *EmptyResponseError) Error() string {
	return fmt.Sprintf(
		"LLM returned empty response after %v (model: %s, messages: %d)",
		e.Duration, e.Model, e.MessageCount,
	)
}

// Response represents an LLM response.
type Response struct {
	// Content is the text response.
	Content string `json:"content"`

	// ToolCalls contains any tool calls the LLM wants to make.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// StopReason indicates why generation stopped.
	// Values: "end", "max_tokens", "tool_use", "stop_sequence"
	StopReason string `json:"stop_reason"`

	// TokensUsed is the total tokens consumed (input + output).
	TokensUsed int `json:"tokens_used"`

	// InputTokens is the input token count.
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the output token count.
	OutputTokens int `json:"output_tokens"`

	// Duration is how long the request took.
	Duration time.Duration `json:"duration"`

	// Model is the model that generated this response.
	Model string `json:"model,omitempty"`

	// TraceStep is the CRS trace step recorded for this LLM call.
	// Callers can use this to record the step in the CRS trace recorder.
	// May be nil if CRS recording was not applicable.
	TraceStep *crs.TraceStep `json:"trace_step,omitempty"`
}

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// BuildRequest creates a request from agent context.
//
// Description:
//
//	Converts the agent's assembled context into an LLM request
//	suitable for the Complete method.
//
// Inputs:
//
//	ctx - Assembled context from the agent
//	availableTools - Tools available for this request
//	maxTokens - Maximum response tokens
//
// Outputs:
//
//	*Request - The built request
func BuildRequest(
	ctx *agent.AssembledContext,
	availableTools []tools.ToolDefinition,
	maxTokens int,
) *Request {
	if ctx == nil {
		return &Request{MaxTokens: maxTokens}
	}

	messages := make([]Message, 0, len(ctx.ConversationHistory)+len(ctx.ToolResults)+1)

	// Add code context as a system message if present
	if len(ctx.CodeContext) > 0 {
		codeContextMsg := formatCodeContext(ctx.CodeContext)
		messages = append(messages, Message{
			Role:    "user",
			Content: codeContextMsg,
		})
	}

	// Add conversation history
	for _, msg := range ctx.ConversationHistory {
		messages = append(messages, Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Add recent tool results as tool messages
	for _, result := range ctx.ToolResults {
		messages = append(messages, Message{
			Role: "tool",
			ToolResults: []ToolCallResult{{
				ToolCallID: result.InvocationID,
				Content:    result.Output,
				IsError:    !result.Success,
			}},
		})
	}

	return &Request{
		SystemPrompt: ctx.SystemPrompt,
		Messages:     messages,
		Tools:        availableTools,
		MaxTokens:    maxTokens,
		Temperature:  0.7,
	}
}

// formatCodeContext formats code entries into a readable message.
func formatCodeContext(entries []agent.CodeEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Here is relevant code from the codebase:\n\n")

	for _, entry := range entries {
		sb.WriteString("--- ")
		sb.WriteString(entry.FilePath)
		if entry.SymbolName != "" {
			sb.WriteString(" (")
			sb.WriteString(entry.SymbolName)
			sb.WriteString(")")
		}
		sb.WriteString(" ---\n")
		sb.WriteString(entry.Content)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// ParseToolCalls extracts tool invocations from an LLM response.
//
// Description:
//
//	Parses native tool_calls from the LLM response structure.
//	For models that don't support native function calling, use
//	ParseToolCallsWithReAct instead.
//
// Inputs:
//
//	response - The LLM response
//
// Outputs:
//
//	[]agent.ToolInvocation - Parsed tool invocations
//
// Thread Safety: This function is safe for concurrent use.
func ParseToolCalls(response *Response) []agent.ToolInvocation {
	if response == nil || len(response.ToolCalls) == 0 {
		return nil
	}

	invocations := make([]agent.ToolInvocation, 0, len(response.ToolCalls))
	for _, call := range response.ToolCalls {
		// Parse arguments JSON into ToolParameters
		params := parseArguments(call.Arguments)

		invocations = append(invocations, agent.ToolInvocation{
			ID:         call.ID,
			Tool:       call.Name,
			Parameters: params,
		})
	}

	return invocations
}

// ParseToolCallsWithReAct extracts tool invocations with ReAct fallback.
//
// Description:
//
//	First attempts to parse native tool_calls from the response.
//	If no native tool calls are found, falls back to parsing
//	ReAct-style text format (Thought/Action/Action Input).
//
//	This enables tool usage with models that don't support native
//	function calling.
//
// Inputs:
//
//	response - The LLM response
//
// Outputs:
//
//	[]agent.ToolInvocation - Parsed tool invocations (from either source)
//	bool - True if invocations came from ReAct parsing (not native)
//
// Example:
//
//	invocations, usedReAct := ParseToolCallsWithReAct(response)
//	if usedReAct {
//	    // Format next tool result as Observation
//	}
//
// Thread Safety: This function is safe for concurrent use.
func ParseToolCallsWithReAct(response *Response) ([]agent.ToolInvocation, bool) {
	if response == nil {
		return nil, false
	}

	// First try native tool calls
	if len(response.ToolCalls) > 0 {
		return ParseToolCalls(response), false
	}

	// Fall back to ReAct parsing
	if response.Content == "" {
		return nil, false
	}

	reactResult := ParseReAct(response.Content)
	if !reactResult.HasAction() {
		return nil, false
	}

	toolCall := reactResult.ToToolCall()
	if toolCall == nil {
		return nil, false
	}

	// Convert to ToolInvocation
	params := parseArguments(toolCall.Arguments)
	invocation := agent.ToolInvocation{
		ID:         toolCall.ID,
		Tool:       toolCall.Name,
		Parameters: params,
	}

	return []agent.ToolInvocation{invocation}, true
}

// parseArguments parses JSON arguments string into ToolParameters.
//
// Description:
//
//	Parses a JSON string of tool arguments into typed parameter maps.
//	Supports string, int, float, and bool parameters. The raw JSON is
//	also stored for tools that need custom parsing.
//
// Inputs:
//
//	argsJSON - Raw JSON string of arguments (e.g., '{"file_path": "/foo", "depth": 3}')
//
// Outputs:
//
//	*agent.ToolParameters - Parsed parameters with typed maps populated
func parseArguments(argsJSON string) *agent.ToolParameters {
	params := &agent.ToolParameters{
		StringParams: make(map[string]string),
		IntParams:    make(map[string]int),
		BoolParams:   make(map[string]bool),
	}

	if argsJSON == "" || argsJSON == "{}" {
		return params
	}

	// Store raw JSON for flexible parsing by tool implementations
	params.RawJSON = []byte(argsJSON)

	// Parse JSON into a generic map
	var rawMap map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &rawMap); err != nil {
		// Log parse error but return params with just RawJSON set
		// Tools can still try to parse RawJSON directly
		return params
	}

	// Populate typed maps based on actual JSON types
	for key, value := range rawMap {
		if value == nil {
			continue
		}

		switch v := value.(type) {
		case string:
			params.StringParams[key] = v
		case bool:
			params.BoolParams[key] = v
		case float64:
			// JSON numbers are float64 - check if it's actually an int
			if v == float64(int(v)) {
				params.IntParams[key] = int(v)
			} else {
				// Store floats as strings for now (tools can parse RawJSON if needed)
				params.StringParams[key] = fmt.Sprintf("%v", v)
			}
		case int:
			params.IntParams[key] = v
		case int64:
			params.IntParams[key] = int(v)
		default:
			// For arrays, objects, etc. - store as JSON string
			if bytes, err := json.Marshal(v); err == nil {
				params.StringParams[key] = string(bytes)
			}
		}
	}

	return params
}
