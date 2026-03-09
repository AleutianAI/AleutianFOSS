// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package main implements the Aleutian Trace OpenAI-compatible proxy.
package main

import "time"

// =============================================================================
// OpenAI API Types (minimal subset for proxy compatibility)
// =============================================================================

// ChatCompletionRequest is the OpenAI-compatible chat completion request.
//
// Description:
//
//	Represents the subset of the OpenAI chat completions API that the proxy
//	needs to understand. Additional fields are ignored (not forwarded, since
//	the proxy delegates to the agent loop, not to Ollama directly).
//
// Thread Safety: Not safe for concurrent use.
type ChatCompletionRequest struct {
	// Model is the model name (e.g., "glm4:latest"). Informational only —
	// the agent loop uses its own configured model.
	Model string `json:"model"`

	// Messages is the conversation history in OpenAI format.
	Messages []ChatMessage `json:"messages"`

	// Stream requests server-sent events. MVP: buffered single-chunk response.
	Stream bool `json:"stream,omitempty"`

	// MaxTokens is the maximum number of tokens to generate. Informational only.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls randomness. Informational only.
	Temperature *float64 `json:"temperature,omitempty"`
}

// ChatMessage is a single message in the OpenAI conversation format.
//
// Description:
//
//	Represents user, assistant, system, or tool messages. The proxy primarily
//	reads user messages to extract the query for the agent loop.
//
// Thread Safety: Not safe for concurrent use.
type ChatMessage struct {
	// Role is one of "system", "user", "assistant", or "tool".
	Role string `json:"role"`

	// Content is the message text.
	Content string `json:"content,omitempty"`

	// ToolCalls contains tool invocations from the assistant. Not used by the
	// proxy (agent loop handles tools internally) but included for type
	// completeness.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID identifies which tool call this message responds to.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation in an assistant message.
//
// Description:
//
//	Included for OpenAI type completeness. The proxy does not generate
//	tool calls — the agent loop handles all tool execution internally.
//
// Thread Safety: Not safe for concurrent use.
type ToolCall struct {
	// ID is the unique identifier for this tool call.
	ID string `json:"id"`

	// Type is always "function" for OpenAI tool calls.
	Type string `json:"type"`

	// Function contains the function name and arguments.
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the function name and serialized arguments.
//
// Thread Safety: Not safe for concurrent use.
type ToolCallFunction struct {
	// Name is the function name.
	Name string `json:"name"`

	// Arguments is the JSON-encoded arguments string.
	Arguments string `json:"arguments"`
}

// ChatCompletionResponse is the OpenAI-compatible chat completion response.
//
// Description:
//
//	The proxy constructs this from the AgentRunResponse returned by the
//	trace server's agent loop. The agent's final response becomes the
//	assistant message content.
//
// Thread Safety: Not safe for concurrent use.
type ChatCompletionResponse struct {
	// ID is a unique identifier for this completion (e.g., "chatcmpl-<uuid>").
	ID string `json:"id"`

	// Object is always "chat.completion".
	Object string `json:"object"`

	// Created is the Unix timestamp of when the response was created.
	Created int64 `json:"created"`

	// Model echoes back the requested model name.
	Model string `json:"model"`

	// Choices contains the completion choices. The proxy always returns
	// exactly one choice.
	Choices []Choice `json:"choices"`

	// Usage contains token usage statistics. Populated from agent loop
	// token counts when available.
	Usage *Usage `json:"usage,omitempty"`
}

// Choice is a single completion choice.
//
// Thread Safety: Not safe for concurrent use.
type Choice struct {
	// Index is always 0 (proxy returns single choice).
	Index int `json:"index"`

	// Message is the assistant's response message.
	Message ChatMessage `json:"message"`

	// FinishReason is "stop" for complete responses.
	FinishReason string `json:"finish_reason"`
}

// Usage contains token usage statistics.
//
// Thread Safety: Not safe for concurrent use.
type Usage struct {
	// PromptTokens is the number of prompt tokens used.
	PromptTokens int `json:"prompt_tokens"`

	// CompletionTokens is the number of completion tokens generated.
	CompletionTokens int `json:"completion_tokens"`

	// TotalTokens is PromptTokens + CompletionTokens.
	TotalTokens int `json:"total_tokens"`
}

// =============================================================================
// SSE Streaming Types
// =============================================================================

// ChatCompletionChunk is the OpenAI-compatible streaming chunk.
//
// Description:
//
//	Used for the buffered SSE response when stream=true. The proxy emits
//	a single chunk containing the full response, then sends [DONE].
//
// Thread Safety: Not safe for concurrent use.
type ChatCompletionChunk struct {
	// ID is a unique identifier for this chunk.
	ID string `json:"id"`

	// Object is always "chat.completion.chunk".
	Object string `json:"object"`

	// Created is the Unix timestamp.
	Created int64 `json:"created"`

	// Model echoes the requested model name.
	Model string `json:"model"`

	// Choices contains the streaming delta choices.
	Choices []ChunkChoice `json:"choices"`
}

// ChunkChoice is a single streaming delta choice.
//
// Thread Safety: Not safe for concurrent use.
type ChunkChoice struct {
	// Index is always 0.
	Index int `json:"index"`

	// Delta contains the incremental content.
	Delta ChatMessageDelta `json:"delta"`

	// FinishReason is nil during streaming, "stop" on the final chunk.
	FinishReason *string `json:"finish_reason"`
}

// ChatMessageDelta is the incremental content in a streaming chunk.
//
// Thread Safety: Not safe for concurrent use.
type ChatMessageDelta struct {
	// Role is set on the first chunk only.
	Role string `json:"role,omitempty"`

	// Content is the incremental text content.
	Content string `json:"content,omitempty"`
}

// =============================================================================
// Model List Types (OpenAI /v1/models format)
// =============================================================================

// ModelListResponse is the OpenAI-compatible model list response.
//
// Thread Safety: Not safe for concurrent use.
type ModelListResponse struct {
	// Object is always "list".
	Object string `json:"object"`

	// Data contains the available models.
	Data []ModelObject `json:"data"`
}

// ModelObject is a single model entry.
//
// Thread Safety: Not safe for concurrent use.
type ModelObject struct {
	// ID is the model identifier (e.g., "glm4:latest").
	ID string `json:"id"`

	// Object is always "model".
	Object string `json:"object"`

	// Created is the Unix timestamp of when the model was created.
	Created int64 `json:"created"`

	// OwnedBy is the model owner (set to "ollama" for local models).
	OwnedBy string `json:"owned_by"`
}

// =============================================================================
// Ollama API Types (for /api/tags response translation)
// =============================================================================

// OllamaTagsResponse is Ollama's response to GET /api/tags.
//
// Thread Safety: Not safe for concurrent use.
type OllamaTagsResponse struct {
	// Models is the list of available models.
	Models []OllamaModel `json:"models"`
}

// OllamaModel is a single Ollama model entry.
//
// Thread Safety: Not safe for concurrent use.
type OllamaModel struct {
	// Name is the model name (e.g., "glm4:latest").
	Name string `json:"name"`

	// ModifiedAt is when the model was last modified.
	ModifiedAt string `json:"modified_at"`
}

// =============================================================================
// Proxy Configuration
// =============================================================================

// ProxyConfig configures the OpenAI-compatible proxy server.
//
// Description:
//
//	Contains all configuration for the proxy including listen address,
//	upstream service URLs, and timeout settings. Immutable after construction.
//
// Thread Safety: Immutable after construction.
type ProxyConfig struct {
	// ListenAddr is the address to listen on (default: ":12218").
	ListenAddr string

	// TraceURL is the trace server URL (default: "http://localhost:12217").
	TraceURL string

	// OllamaURL is the Ollama server URL (default: "http://localhost:11434").
	OllamaURL string

	// ProjectRoot is the optional default project root for agent runs.
	// Can be overridden per-request via X-Project-Root header.
	ProjectRoot string

	// Timeout is the maximum duration for a single agent run (default: 5m).
	Timeout time.Duration

	// HostPrefix is the host-side path prefix for project directories.
	// When set, paths starting with this prefix are rewritten to ContainerPrefix
	// before forwarding to the trace server running inside a container.
	HostPrefix string

	// ContainerPrefix is the container-side mount point corresponding to HostPrefix.
	// Both HostPrefix and ContainerPrefix must be set for path translation to activate.
	ContainerPrefix string
}

// =============================================================================
// Agent API Request Types (proxy-local mirrors to avoid importing trace service)
// =============================================================================

// agentRunRequest is the request body for POST /v1/trace/agent/run.
//
// Thread Safety: Not safe for concurrent use.
type agentRunRequest struct {
	// ProjectRoot is the absolute path to the project root directory.
	ProjectRoot string `json:"project_root"`

	// Query is the user's question or task description.
	Query string `json:"query"`
}

// agentContinueRequest is the request body for POST /v1/trace/agent/continue.
//
// Thread Safety: Not safe for concurrent use.
type agentContinueRequest struct {
	// SessionID is the session to continue.
	SessionID string `json:"session_id"`

	// Clarification is the user's response to the clarification request.
	Clarification string `json:"clarification"`
}

// =============================================================================
// Error Types
// =============================================================================

// openAIErrorResponse is the OpenAI-compatible error response format.
//
// Thread Safety: Not safe for concurrent use.
type openAIErrorResponse struct {
	// Error contains the error details.
	Error openAIErrorDetail `json:"error"`
}

// openAIErrorDetail contains the error message and type.
//
// Thread Safety: Not safe for concurrent use.
type openAIErrorDetail struct {
	// Message is the human-readable error message.
	Message string `json:"message"`

	// Type categorizes the error (e.g., "proxy_error").
	Type string `json:"type"`
}

// =============================================================================
// Health Types
// =============================================================================

// HealthResponse is the combined health status of the proxy and its upstreams.
//
// Thread Safety: Not safe for concurrent use.
type HealthResponse struct {
	// Status is the overall status ("healthy", "degraded", "unhealthy").
	Status string `json:"status"`

	// Proxy is always "up" if the health endpoint responds.
	Proxy string `json:"proxy"`

	// TraceServer is "up" or "down".
	TraceServer string `json:"trace_server"`

	// Ollama is "up" or "down".
	Ollama string `json:"ollama"`
}
