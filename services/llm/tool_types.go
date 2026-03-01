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

// ToolDef is the generic tool definition used as input to ChatWithTools
// for all providers. Follows the OpenAI function calling schema.
//
// Description:
//
//	Provides a provider-agnostic way to define tools. Each provider's
//	ChatWithTools method converts ToolDef into its wire format
//	(Anthropic input_schema, OpenAI function, Gemini functionDeclarations).
//
// Thread Safety: ToolDef is immutable and safe for concurrent read access.
type ToolDef struct {
	// Type is the tool type. Always "function" for function calling.
	Type string `json:"type"`

	// Function contains the function definition.
	Function ToolFunction `json:"function"`
}

// ToolFunction contains the function name, description, and parameter schema.
type ToolFunction struct {
	// Name is the function name the model will call.
	Name string `json:"name"`

	// Description explains what the function does.
	Description string `json:"description"`

	// Parameters defines the JSON Schema for function parameters.
	Parameters ToolParameters `json:"parameters"`
}

// ToolParameters defines the JSON Schema for tool parameters.
type ToolParameters struct {
	// Type is the JSON Schema type. Always "object" for tool parameters.
	Type string `json:"type"`

	// Properties maps parameter names to their definitions.
	Properties map[string]ToolParamDef `json:"properties,omitempty"`

	// Required lists parameter names that must be provided.
	Required []string `json:"required,omitempty"`
}

// ToolParamDef defines a single parameter in JSON Schema format.
type ToolParamDef struct {
	// Type is the JSON Schema type (string, integer, boolean, number).
	Type string `json:"type"`

	// Description explains what the parameter is for.
	Description string `json:"description,omitempty"`

	// Enum restricts values to a set of options.
	Enum []any `json:"enum,omitempty"`

	// Default is the default value if not provided.
	Default any `json:"default,omitempty"`
}

// ChatMessage is a richer message type that carries tool call metadata.
//
// Description:
//
//	Regular messages use Role + Content. Tool results include ToolCallID.
//	Assistant messages with tool calls include ToolCalls.
//	This bridges the gap between datatypes.Message (which lacks tool call IDs)
//	and the wire formats required by each provider.
//
// Thread Safety: ChatMessage is safe for concurrent read access.
type ChatMessage struct {
	// Role is the message role: "system", "user", "assistant", or "tool".
	Role string `json:"role"`

	// Content is the text content of the message.
	Content string `json:"content,omitempty"`

	// ToolCalls contains tool invocations (for assistant messages).
	ToolCalls []ToolCallResponse `json:"tool_calls,omitempty"`

	// ToolCallID links this message back to a specific tool call (for tool result messages).
	ToolCallID string `json:"tool_call_id,omitempty"`

	// ToolName is the tool name for tool result messages. Required by Gemini's functionResponse.
	ToolName string `json:"tool_name,omitempty"`
}

// ToolCallResponse represents a tool call from any LLM provider.
//
// Description:
//
//	Provider-agnostic representation of a tool call. Each provider's
//	ChatWithTools method populates this from its native response format:
//	- Anthropic: tool_use content blocks
//	- OpenAI: tool_calls array
//	- Gemini: functionCall parts (with synthetic IDs)
//	- Ollama: OllamaToolCall (existing)
//
// Thread Safety: ToolCallResponse is safe for concurrent read access.
type ToolCallResponse struct {
	// ID is the unique identifier for this tool call.
	// Anthropic provides this in tool_use blocks.
	// OpenAI provides this in tool_calls.
	// Gemini does not provide IDs; synthetic ones are generated.
	ID string `json:"id"`

	// Name is the function name to call.
	Name string `json:"name"`

	// Arguments is the raw JSON arguments for the function.
	Arguments json.RawMessage `json:"arguments"`
}

// ArgumentsString returns the arguments as a JSON string.
//
// Description:
//
//	If arguments is already a JSON string value (starts with quote),
//	it returns the unquoted string. If arguments is an object or other
//	JSON value, it returns the raw JSON as-is. Returns "{}" for nil/empty.
//
// Outputs:
//   - string: The arguments as a JSON string suitable for tool execution.
//
// Thread Safety: This method is safe for concurrent use.
func (t *ToolCallResponse) ArgumentsString() string {
	if len(t.Arguments) == 0 {
		return "{}"
	}

	// Check if it's a JSON string (starts with quote)
	if t.Arguments[0] == '"' {
		var s string
		if err := json.Unmarshal(t.Arguments, &s); err == nil {
			return s
		}
	}

	// It's an object or other JSON value, return as-is
	return string(t.Arguments)
}

// ChatWithToolsResult is the provider-agnostic result from ChatWithTools.
//
// Description:
//
//	Contains the LLM response including any tool calls. All provider
//	clients return this from their ChatWithTools method.
//
// Thread Safety: ChatWithToolsResult is safe for concurrent read access.
type ChatWithToolsResult struct {
	// Content is the text response (may be empty if only tool calls).
	Content string

	// ToolCalls contains tool calls from the model.
	ToolCalls []ToolCallResponse

	// StopReason indicates why generation stopped.
	// Values: "end" (normal completion) or "tool_use" (tool calls present).
	StopReason string
}
