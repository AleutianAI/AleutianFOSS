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

import "encoding/json"

// GenerationParams holds parameters for LLM generation.
//
// Description:
//
//	Contains all configurable parameters for text generation including
//	temperature, sampling parameters, and tool definitions.
//
// Thread Safety: GenerationParams is a value type, safe for concurrent reads.
type GenerationParams struct {
	Temperature     *float32      `json:"temperature"`
	TopK            *int          `json:"top_k"`
	TopP            *float32      `json:"top_p"`
	MaxTokens       *int          `json:"max_tokens"`
	Stop            []string      `json:"stop"`
	ToolDefinitions []interface{} `json:"tools,omitempty"`
	EnableThinking  bool          `json:"thinking,omitempty"`
	BudgetTokens    int           `json:"budget_tokens,omitempty"`
	ModelOverride   string        `json:"model_override,omitempty"`
	KeepAlive       string        `json:"keep_alive,omitempty"`
	NumCtx          *int          `json:"num_ctx,omitempty"`
	ToolChoice      *ToolChoice   `json:"tool_choice,omitempty"`
}

// ToolDef is the generic tool definition used as input to ChatWithTools.
//
// Thread Safety: ToolDef is immutable and safe for concurrent read access.
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction contains the function name, description, and parameter schema.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  ToolParameters `json:"parameters"`
}

// ToolParameters defines the JSON Schema for tool parameters.
type ToolParameters struct {
	Type       string                  `json:"type"`
	Properties map[string]ToolParamDef `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

// ToolParamDef defines a single parameter in JSON Schema format.
type ToolParamDef struct {
	Type        string        `json:"type"`
	Description string        `json:"description,omitempty"`
	Enum        []any         `json:"enum,omitempty"`
	Default     any           `json:"default,omitempty"`
	Items       *ToolParamDef `json:"items,omitempty"`
}

// ChatMessage is a richer message type that carries tool call metadata.
//
// Thread Safety: ChatMessage is safe for concurrent read access.
type ChatMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content,omitempty"`
	ToolCalls  []ToolCallResponse `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	ToolName   string             `json:"tool_name,omitempty"`
}

// ToolCallResponse represents a tool call from any LLM provider.
//
// Thread Safety: ToolCallResponse is safe for concurrent read access.
type ToolCallResponse struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`

	// ThoughtSignature is an opaque token from Gemini 3 models that must be
	// echoed back in subsequent requests to preserve reasoning context.
	// Empty for non-Gemini providers and older Gemini models.
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ArgumentsString returns the arguments as a JSON string.
//
// Description:
//
//	If arguments is already a JSON string value (starts with quote),
//	it returns the unquoted string. Otherwise returns raw JSON as-is.
//	Returns "{}" for nil/empty.
//
// Thread Safety: This method is safe for concurrent use.
func (t *ToolCallResponse) ArgumentsString() string {
	if len(t.Arguments) == 0 {
		return "{}"
	}
	if t.Arguments[0] == '"' {
		var s string
		if err := json.Unmarshal(t.Arguments, &s); err == nil {
			return s
		}
	}
	return string(t.Arguments)
}

// ChatWithToolsResult is the provider-agnostic result from ChatWithTools.
//
// Thread Safety: ChatWithToolsResult is safe for concurrent read access.
type ChatWithToolsResult struct {
	Content    string
	ToolCalls  []ToolCallResponse
	StopReason string
}

// StreamEventType represents the type of streaming event.
type StreamEventType string

const (
	StreamEventToken    StreamEventType = "token"
	StreamEventThinking StreamEventType = "thinking"
	StreamEventError    StreamEventType = "error"
)

// StreamEvent represents a single event during LLM streaming.
type StreamEvent struct {
	Type    StreamEventType
	Content string
	Error   string
}

// StreamCallback is called for each event during streaming.
type StreamCallback func(event StreamEvent) error
