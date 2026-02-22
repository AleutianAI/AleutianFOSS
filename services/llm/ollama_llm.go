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
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

var tracer = otel.Tracer("aleutian.llm.ollama") // Specific tracer name

type OllamaClient struct {
	httpClient *http.Client
	baseURL    string
	model      string
}

// Ollama API request structure
type ollamaGenerateRequest struct {
	Model     string                 `json:"model"`
	Prompt    string                 `json:"prompt"`
	Stream    bool                   `json:"stream"`
	Options   map[string]interface{} `json:"options,omitempty"`
	KeepAlive string                 `json:"keep_alive,omitempty"` // "-1" = infinite, "5m" = 5 minutes, "0" = unload immediately
}

type ollamaGenerateResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
}

type ollamaChatRequest struct {
	Model      string                 `json:"model"`
	Messages   []datatypes.Message    `json:"messages"`
	Stream     bool                   `json:"stream"`
	Options    map[string]interface{} `json:"options,omitempty"`
	Tools      []OllamaTool           `json:"tools,omitempty"`
	ToolChoice *ollamaToolChoice      `json:"tool_choice,omitempty"` // Controls tool selection behavior
	KeepAlive  string                 `json:"keep_alive,omitempty"`  // "-1" = infinite, "5m" = 5 minutes, "0" = unload immediately
}

// ollamaToolChoice represents the tool_choice parameter for Ollama's API.
//
// Description:
//
//	Controls whether and which tools the model calls. Follows OpenAI-compatible format.
//	- type "auto": Model decides whether to call tools
//	- type "none": Model cannot call tools
//	- type "any"/"required": Model MUST call at least one tool
//	- type "function" with function.name: Model MUST call the specified tool
//
// Thread Safety: This type is immutable and safe for concurrent read access.
type ollamaToolChoice struct {
	Type     string                    `json:"type,omitempty"`
	Function *ollamaToolChoiceFunction `json:"function,omitempty"`
}

// ollamaToolChoiceFunction specifies a specific function to call.
type ollamaToolChoiceFunction struct {
	Name string `json:"name"`
}

// convertToolChoice converts a GenerationParams ToolChoice to Ollama's API format.
//
// Description:
//
//	Maps the generic ToolChoice type to Ollama's expected format:
//	- "auto" → type: "auto" (model decides)
//	- "none" → type: "none" (no tools)
//	- "any"  → type: "required" (must call some tool, OpenAI-compatible)
//	- "tool" with Name → type: "function" with function.name set
//
// Inputs:
//
//	tc - The ToolChoice to convert. If nil, returns nil (use model default).
//
// Outputs:
//
//	*ollamaToolChoice - The converted format, or nil if tc is nil.
//
// Thread Safety: This function is safe for concurrent use.
func convertToolChoice(tc *ToolChoice) *ollamaToolChoice {
	if tc == nil {
		return nil
	}

	switch tc.Type {
	case "auto":
		return &ollamaToolChoice{Type: "auto"}
	case "none":
		return &ollamaToolChoice{Type: "none"}
	case "any":
		// OpenAI uses "required" for forcing any tool
		return &ollamaToolChoice{Type: "required"}
	case "tool":
		// Force specific tool using OpenAI-compatible format
		return &ollamaToolChoice{
			Type: "function",
			Function: &ollamaToolChoiceFunction{
				Name: tc.Name,
			},
		}
	default:
		// Unknown type, let model decide
		slog.Warn("Unknown tool_choice type, defaulting to auto",
			slog.String("type", tc.Type),
		)
		return &ollamaToolChoice{Type: "auto"}
	}
}

// =============================================================================
// Tool Calling Types (Ollama API compatible)
// =============================================================================

// OllamaTool represents a tool definition for Ollama's function calling API.
//
// Description:
//
//	Ollama uses an OpenAI-compatible format for tool definitions.
//	This structure is serialized in the "tools" array of chat requests.
//
// Example:
//
//	{
//	  "type": "function",
//	  "function": {
//	    "name": "read_file",
//	    "description": "Read contents of a file",
//	    "parameters": {...}
//	  }
//	}
type OllamaTool struct {
	Type     string             `json:"type"`
	Function OllamaToolFunction `json:"function"`
}

// OllamaToolFunction contains the function definition within an OllamaTool.
type OllamaToolFunction struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Parameters  OllamaToolParameters `json:"parameters"`
}

// OllamaToolParameters defines the JSON Schema for tool parameters.
type OllamaToolParameters struct {
	Type       string                    `json:"type"`
	Properties map[string]OllamaParamDef `json:"properties,omitempty"`
	Required   []string                  `json:"required,omitempty"`
}

// OllamaParamDef defines a single parameter in JSON Schema format.
type OllamaParamDef struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// OllamaToolCall represents a tool call from Ollama's response.
type OllamaToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function OllamaFunctionCall `json:"function"`
}

// OllamaFunctionCall contains the function name and arguments.
//
// Note: Arguments can be either a JSON string or a JSON object depending on the model.
// Some models (like glm-4.7-flash) return arguments as an object, while others
// (OpenAI-compatible) return it as a JSON-encoded string.
type OllamaFunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ArgumentsString returns the arguments as a JSON string.
// If arguments is already a string, it returns the unquoted string.
// If arguments is an object, it returns the JSON-encoded string.
func (f *OllamaFunctionCall) ArgumentsString() string {
	if len(f.Arguments) == 0 {
		return "{}"
	}

	// Check if it's a JSON string (starts with quote)
	if f.Arguments[0] == '"' {
		var s string
		if err := json.Unmarshal(f.Arguments, &s); err == nil {
			return s
		}
	}

	// It's an object or other JSON value, return as-is
	return string(f.Arguments)
}

type ollamaChatResponse struct {
	Message   ollamaChatMessage `json:"message"`
	CreatedAt string            `json:"created_at"`
	Done      bool              `json:"done"`
}

// ollamaChatMessage extends datatypes.Message to include tool calls.
//
// Description:
//
//	Ollama returns tool calls in the message.tool_calls field when the
//	model decides to call tools. This type captures that response.
type ollamaChatMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []OllamaToolCall `json:"tool_calls,omitempty"`
}

// =============================================================================
// Streaming Types and Configuration
// =============================================================================

// StreamConfig configures streaming behavior for ChatStream.
//
// # Description
//
// StreamConfig provides fine-grained control over streaming behavior including
// privacy controls for thinking tokens, rate limiting to prevent overwhelming
// clients, and maximum length limits for safety.
//
// # Fields
//
//   - RedactThinking: If true, thinking tokens from models like gpt-oss are not
//     emitted to the callback. The thinking still occurs server-side.
//   - MaxThinkingLength: Maximum characters for thinking content per stream.
//     0 means unlimited. Truncates if exceeded.
//   - RateLimitPerSecond: Maximum callback invocations per second. 0 disables.
//   - MaxResponseLength: Maximum total response characters. 0 means unlimited.
//
// # Examples
//
//	// Privacy-focused config
//	cfg := StreamConfig{RedactThinking: true, MaxThinkingLength: 1000}
//
//	// Rate-limited streaming
//	cfg := StreamConfig{RateLimitPerSecond: 100}
//
// # Limitations
//
//   - Rate limiting adds latency to token delivery
//   - Truncation loses data (use logging to preserve if needed)
//
// # Assumptions
//
//   - Reasonable limits prevent DoS from runaway generation
type StreamConfig struct {
	RedactThinking     bool `json:"redact_thinking"`
	MaxThinkingLength  int  `json:"max_thinking_length"`
	RateLimitPerSecond int  `json:"rate_limit_per_second"`
	MaxResponseLength  int  `json:"max_response_length"`
}

// DefaultStreamConfig returns a StreamConfig with safe defaults.
//
// # Description
//
// Returns a configuration suitable for most use cases:
// - Thinking tokens are passed through (not redacted)
// - No rate limiting (full speed)
// - 100KB max response length for safety
//
// # Outputs
//
//   - StreamConfig: Default configuration.
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		RedactThinking:     false,
		MaxThinkingLength:  0,
		RateLimitPerSecond: 0,
		MaxResponseLength:  100 * 1024, // 100KB safety limit
	}
}

// ollamaStreamChunk represents a single NDJSON line from Ollama streaming.
//
// # Description
//
// When streaming is enabled, Ollama returns newline-delimited JSON objects.
// Each chunk contains the incremental content, optional thinking content
// (for models like gpt-oss), and metadata about the generation.
//
// # Fields
//
//   - Model: Model name that generated this chunk.
//   - CreatedAt: ISO8601 timestamp of chunk generation.
//   - Message: The message content (partial).
//   - Thinking: Thinking/reasoning content (gpt-oss, DeepSeek-R1).
//   - Done: True if this is the final chunk.
//   - DoneReason: Why generation stopped (stop, length, etc).
//   - TotalDuration: Total time in nanoseconds (final chunk only).
//   - Error: Error message if something went wrong.
type ollamaStreamChunk struct {
	Model         string            `json:"model,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	Message       datatypes.Message `json:"message,omitempty"`
	Thinking      string            `json:"thinking,omitempty"`
	Done          bool              `json:"done"`
	DoneReason    string            `json:"done_reason,omitempty"`
	TotalDuration int64             `json:"total_duration,omitempty"`
	Error         string            `json:"error,omitempty"`
}

// =============================================================================
// Streaming Metrics (OTel)
// =============================================================================

var (
	streamMetricsOnce sync.Once
	streamTokenCount  metric.Int64Counter
	streamDuration    metric.Float64Histogram
	streamErrorCount  metric.Int64Counter
)

// initStreamMetrics initializes OTel metrics for streaming.
func initStreamMetrics() {
	streamMetricsOnce.Do(func() {
		meter := otel.Meter("aleutian.llm.ollama")

		var err error
		streamTokenCount, err = meter.Int64Counter(
			"ollama_stream_tokens_total",
			metric.WithDescription("Total tokens streamed from Ollama"),
		)
		if err != nil {
			slog.Warn("Failed to create stream token counter", "error", err)
		}

		streamDuration, err = meter.Float64Histogram(
			"ollama_stream_duration_seconds",
			metric.WithDescription("Duration of streaming requests"),
		)
		if err != nil {
			slog.Warn("Failed to create stream duration histogram", "error", err)
		}

		streamErrorCount, err = meter.Int64Counter(
			"ollama_stream_errors_total",
			metric.WithDescription("Total streaming errors"),
		)
		if err != nil {
			slog.Warn("Failed to create stream error counter", "error", err)
		}
	})
}

func NewOllamaClient() (*OllamaClient, error) {
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	model := os.Getenv("OLLAMA_MODEL")
	if baseURL == "" {
		return nil, fmt.Errorf("OLLAMA_BASE_URL environment variable not set")
	}
	if model == "" {
		slog.Warn("OLLAMA_MODEL not set, requests must specify model, default gpt-oss")
		model = "gpt-oss"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	slog.Info("Initializing Ollama client", "base_url", baseURL, "default_model", model)
	return &OllamaClient{
		httpClient: &http.Client{Timeout: 5 * time.Minute},
		baseURL:    baseURL,
		model:      model,
	}, nil
}

// Generate implements the LLMClient interface
func (o *OllamaClient) Generate(ctx context.Context, prompt string,
	params GenerationParams) (string, error) {

	ctx, span := tracer.Start(ctx, "OllamaClient.Generate")
	defer span.End()
	span.SetAttributes(attribute.String("llm.model", o.model))
	slog.Debug("Generating text via Ollama", "model", o.model)
	generateURL := o.baseURL + "/api/generate"
	options := make(map[string]interface{})
	if params.Temperature != nil {
		options["temperature"] = *params.Temperature
	} else {
		defaultTemp := float32(0.2)
		options["temperature"] = &defaultTemp
	}
	if params.TopK != nil {
		options["top_k"] = *params.TopK
	} else {
		defaultTopK := 20
		options["top_k"] = defaultTopK
	}
	if params.TopP != nil {
		options["top_p"] = *params.TopP
	} else {
		defaultTopP := float32(0.9)
		options["top_p"] = defaultTopP
	}
	if params.MaxTokens != nil {
		options["num_predict"] = *params.MaxTokens
	} else {
		defaultMaxTokens := 8192
		options["num_predict"] = defaultMaxTokens
	}
	if len(params.Stop) > 0 {
		options["stop"] = params.Stop
	}
	payload := ollamaGenerateRequest{
		Model:   o.model,
		Prompt:  prompt,
		Stream:  false,
		Options: options,
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to marshal request to Ollama: %w", err)
	}

	// Use NewRequestWithContext to respect context cancellation/timeout
	req, err := http.NewRequestWithContext(ctx, "POST", generateURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to create request to Ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error("Ollama API call failed", "error", err)
		return "", fmt.Errorf("Ollama API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to read response body from Ollama: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			var errResp struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(respBodyBytes, &errResp); err == nil && strings.Contains(errResp.Error, "model") && strings.Contains(errResp.Error, "not found") {
				slog.Warn("Ollama model not found", "model", o.model)
				// Return a specific, user-friendly error
				return "", fmt.Errorf("model '%s' not found. Please run: 'ollama pull %s'", o.model, o.model)
			}
		}
		slog.Error("Ollama returned an error", "status_code", resp.StatusCode, "response", string(respBodyBytes))
		return "", fmt.Errorf("Ollama failed with status %d: %s", resp.StatusCode, string(respBodyBytes))
	}

	var ollamaResp ollamaGenerateResponse
	if err := json.Unmarshal(respBodyBytes, &ollamaResp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error("Failed to parse JSON response from Ollama", "error", err, "response", string(respBodyBytes))
		return "", fmt.Errorf("failed to parse Ollama response: %w", err)
	}

	slog.Debug("Received response from Ollama")
	return ollamaResp.Response, nil
}

func (o *OllamaClient) Chat(ctx context.Context, messages []datatypes.Message,
	params GenerationParams) (string, error) {

	ctx, span := tracer.Start(ctx, "OllamaClient.Chat")
	defer span.End()
	span.SetAttributes(attribute.String("llm.model", o.model))
	span.SetAttributes(attribute.Int("llm.num_messages", len(messages)))

	slog.Debug("Generating text via Ollama", "model", o.model)
	chatURL := o.baseURL + "/api/chat"
	options := make(map[string]interface{})
	if params.Temperature != nil {
		options["temperature"] = *params.Temperature
	} else {
		defaultTemp := float32(0.2)
		options["temperature"] = &defaultTemp
	}
	if params.TopK != nil {
		options["top_k"] = *params.TopK
	} else {
		defaultTopK := 20
		options["top_k"] = defaultTopK
	}
	if params.TopP != nil {
		options["top_p"] = *params.TopP
	} else {
		defaultTopP := float32(0.9)
		options["top_p"] = defaultTopP
	}
	if params.MaxTokens != nil {
		options["num_predict"] = *params.MaxTokens
	} else {
		defaultMaxTokens := 8192
		options["num_predict"] = defaultMaxTokens
	}
	if len(params.Stop) > 0 {
		options["stop"] = params.Stop
	}

	// Set context window size if provided.
	// CRITICAL: This MUST be passed on every request to prevent Ollama from
	// resetting to default 4096 context window. CB-31d fix.
	if params.NumCtx != nil && *params.NumCtx > 0 {
		options["num_ctx"] = *params.NumCtx
	}

	// Use ModelOverride if provided, otherwise use client's default model
	model := o.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	payload := ollamaChatRequest{
		Model:     model,
		Messages:  messages,
		Stream:    false,
		Options:   options,
		KeepAlive: params.KeepAlive, // Pass through keep_alive for multi-model support
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal chat request to Ollama: %w", err)
	}

	// Use NewRequestWithContext to respect context cancellation/timeout
	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to create chat request to Ollama: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to send the request to %s: %v", chatURL, err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		span.RecordError(readErr)
		span.SetStatus(codes.Error, readErr.Error())
		return "", fmt.Errorf("failed to read response body: %v", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		httpErr := fmt.Errorf("ollama chat failed with status %d: %s", resp.StatusCode, string(respBody))
		slog.Error("Ollama chat returned an error", "status_code", resp.StatusCode,
			"response", string(respBody))
		span.RecordError(httpErr)
		span.SetStatus(codes.Error, httpErr.Error())
		return "", httpErr
	}
	var ollamaResp ollamaChatResponse
	if err = json.Unmarshal(respBody, &ollamaResp); err != nil {
		slog.Error("Failed to parse JSON chat response from Ollama", "error", err,
			"response", string(respBody))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("failed to marshal the response from the ollama Chat")
	}
	if ollamaResp.Message.Role != "assistant" {
		slog.Warn("Ollama chat response message role was not 'assistant'", "role", ollamaResp.Message.Role)
	}
	return ollamaResp.Message.Content, nil
}

// ChatWithToolsResult is defined in tool_types.go as the generic result type.
// It uses []ToolCallResponse instead of []OllamaToolCall.

// ChatWithTools sends a chat request with tools and returns both content and tool calls.
//
// Description:
//
//	Extends Chat to support Ollama's function calling API. When tools are provided,
//	the model may respond with tool_calls instead of (or in addition to) content.
//
// Inputs:
//
//	ctx - Context for cancellation and timeout.
//	messages - Conversation history.
//	params - Generation parameters.
//	tools - Tool definitions for function calling.
//
// Outputs:
//
//	*ChatWithToolsResult - Content and/or tool calls.
//	error - Non-nil on failure.
//
// Thread Safety: This method is safe for concurrent use.
func (o *OllamaClient) ChatWithTools(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, tools []OllamaTool) (*ChatWithToolsResult, error) {

	ctx, span := tracer.Start(ctx, "OllamaClient.ChatWithTools")
	defer span.End()
	span.SetAttributes(attribute.String("llm.model", o.model))
	span.SetAttributes(attribute.Int("llm.num_messages", len(messages)))
	span.SetAttributes(attribute.Int("llm.num_tools", len(tools)))

	// Log tool_choice if set (important for debugging tool forcing)
	toolChoiceType := "auto"
	toolChoiceName := ""
	if params.ToolChoice != nil {
		toolChoiceType = params.ToolChoice.Type
		toolChoiceName = params.ToolChoice.Name
	}

	slog.Debug("Chat with tools via Ollama",
		"model", o.model,
		"num_messages", len(messages),
		"num_tools", len(tools),
		"tool_choice_type", toolChoiceType,
		"tool_choice_name", toolChoiceName,
	)

	chatURL := o.baseURL + "/api/chat"
	options := make(map[string]interface{})
	if params.Temperature != nil {
		options["temperature"] = *params.Temperature
	} else {
		defaultTemp := float32(0.2)
		options["temperature"] = &defaultTemp
	}
	if params.TopK != nil {
		options["top_k"] = *params.TopK
	} else {
		defaultTopK := 20
		options["top_k"] = defaultTopK
	}
	if params.TopP != nil {
		options["top_p"] = *params.TopP
	} else {
		defaultTopP := float32(0.9)
		options["top_p"] = defaultTopP
	}
	if params.MaxTokens != nil {
		options["num_predict"] = *params.MaxTokens
	} else {
		defaultMaxTokens := 8192
		options["num_predict"] = defaultMaxTokens
	}
	if len(params.Stop) > 0 {
		options["stop"] = params.Stop
	}

	// Set context window size if provided.
	// CRITICAL: This MUST be passed on every request to prevent Ollama from
	// resetting to default 4096 context window. CB-31d fix.
	if params.NumCtx != nil && *params.NumCtx > 0 {
		options["num_ctx"] = *params.NumCtx
	}

	// Use ModelOverride if provided, otherwise use client's default model
	model := o.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	payload := ollamaChatRequest{
		Model:      model,
		Messages:   messages,
		Stream:     false,
		Options:    options,
		Tools:      tools,
		ToolChoice: convertToolChoice(params.ToolChoice),
		KeepAlive:  params.KeepAlive, // Pass through keep_alive for multi-model support
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("failed to marshal chat request to Ollama: %w", err)
	}

	const maxToolParseRetries = 2
	const toolParseRetryDelay = 500 * time.Millisecond

	var respBody []byte
	var ollamaResp ollamaChatResponse

	for attempt := 0; attempt <= maxToolParseRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("failed to create chat request to Ollama: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := o.httpClient.Do(req)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("failed to send request to %s: %v", chatURL, err)
		}

		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("failed to read response body: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			respStr := string(respBody)

			// Retry on LLM tool call JSON parse errors (intermittent formatting issue)
			if resp.StatusCode == http.StatusInternalServerError &&
				strings.Contains(respStr, "error parsing tool call") &&
				attempt < maxToolParseRetries {

				slog.Info("LLM produced malformed tool call JSON, retrying",
					slog.Int("attempt", attempt+1),
					slog.Int("max_retries", maxToolParseRetries),
					slog.String("error", respStr),
				)
				span.AddEvent("tool_call_parse_retry", trace.WithAttributes(
					attribute.Int("attempt", attempt+1),
				))
				time.Sleep(toolParseRetryDelay)
				continue
			}

			httpErr := fmt.Errorf("ollama chat failed with status %d: %s", resp.StatusCode, respStr)
			slog.Error("Ollama chat returned an error",
				"status_code", resp.StatusCode,
				"response", respStr,
			)
			span.RecordError(httpErr)
			span.SetStatus(codes.Error, httpErr.Error())
			return nil, httpErr
		}

		// Success — break out of retry loop
		break
	}

	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		slog.Error("Failed to parse JSON chat response from Ollama",
			"error", err,
			"response", string(respBody),
		)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("failed to parse ollama chat response: %w", err)
	}

	// Convert OllamaToolCall to generic ToolCallResponse
	var toolCalls []ToolCallResponse
	for _, tc := range ollamaResp.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCallResponse{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	result := &ChatWithToolsResult{
		Content:   ollamaResp.Message.Content,
		ToolCalls: toolCalls,
	}

	// Determine stop reason
	if len(result.ToolCalls) > 0 {
		result.StopReason = "tool_use"
		slog.Debug("Ollama returned tool calls",
			"num_tool_calls", len(result.ToolCalls),
		)
	} else {
		result.StopReason = "end"
	}

	return result, nil
}

// =============================================================================
// Streaming Interface
// =============================================================================

// StreamProcessor defines the contract for processing streaming responses.
//
// # Description
//
// StreamProcessor abstracts the streaming response processing logic,
// allowing for different implementations (e.g., testing, rate-limited).
// Implementations must be safe for single-threaded use within a stream.
//
// # Methods
//
//   - ProcessChunk: Process a single NDJSON chunk and emit events.
//   - GetTokenCount: Return total tokens processed.
//   - GetResponseLength: Return total response characters.
//
// # Thread Safety
//
// Not required to be thread-safe; called from single goroutine per stream.
//
// # Limitations
//
//   - Single stream per processor instance
//
// # Assumptions
//
//   - Called sequentially for each chunk in order
type StreamProcessor interface {
	// ProcessChunk processes a single parsed chunk and invokes callback.
	//
	// # Inputs
	//   - ctx: Context for cancellation.
	//   - chunk: Parsed stream chunk.
	//   - callback: Event callback.
	//
	// # Outputs
	//   - bool: True if stream is done.
	//   - error: Non-nil on callback error or processing failure.
	ProcessChunk(ctx context.Context, chunk *ollamaStreamChunk, callback StreamCallback) (bool, error)

	// GetTokenCount returns total tokens processed so far.
	GetTokenCount() int

	// GetResponseLength returns total response characters processed.
	GetResponseLength() int
}

// =============================================================================
// Stream Processor Implementation
// =============================================================================

// DefaultStreamProcessor implements StreamProcessor with configurable behavior.
//
// # Description
//
// DefaultStreamProcessor handles chunk processing with support for:
// - Thinking token redaction (privacy)
// - Response length limits (safety)
// - Thinking length limits (safety)
// - Rate limiting (backpressure)
//
// # Fields
//
//   - cfg: Stream configuration.
//   - rateLimiter: Optional rate limiter for callback invocations.
//   - tokenCount: Running count of content tokens.
//   - responseLen: Running total of response characters.
//   - thinkingLen: Running total of thinking characters.
//
// # Thread Safety
//
// Not thread-safe. Use one instance per stream.
//
// # Examples
//
//	processor := NewDefaultStreamProcessor(StreamConfig{RedactThinking: true}, nil)
//	done, err := processor.ProcessChunk(ctx, chunk, callback)
//
// # Limitations
//
//   - Single use per stream
//
// # Assumptions
//
//   - Chunks arrive in order
type DefaultStreamProcessor struct {
	cfg         StreamConfig
	rateLimiter *rate.Limiter
	tokenCount  int
	responseLen int
	thinkingLen int
}

// NewDefaultStreamProcessor creates a new DefaultStreamProcessor.
//
// # Description
//
// Creates a processor with the given configuration and optional rate limiter.
// If cfg.RateLimitPerSecond > 0 and rateLimiter is nil, creates one automatically.
//
// # Inputs
//
//   - cfg: Stream configuration.
//   - rateLimiter: Optional pre-configured rate limiter. If nil and rate limiting
//     is configured, one will be created.
//
// # Outputs
//
//   - *DefaultStreamProcessor: Configured processor ready for use.
//
// # Examples
//
//	// Auto-create rate limiter from config
//	p := NewDefaultStreamProcessor(StreamConfig{RateLimitPerSecond: 50}, nil)
//
//	// Use custom rate limiter
//	limiter := rate.NewLimiter(100, 10)
//	p := NewDefaultStreamProcessor(StreamConfig{}, limiter)
//
// # Limitations
//
//   - Rate limiter is shared if passed in; be careful with concurrent streams
//
// # Assumptions
//
//   - cfg has reasonable values (non-negative limits)
func NewDefaultStreamProcessor(cfg StreamConfig, rateLimiter *rate.Limiter) *DefaultStreamProcessor {
	if rateLimiter == nil && cfg.RateLimitPerSecond > 0 {
		rateLimiter = rate.NewLimiter(rate.Limit(cfg.RateLimitPerSecond), 1)
	}
	return &DefaultStreamProcessor{
		cfg:         cfg,
		rateLimiter: rateLimiter,
		tokenCount:  0,
		responseLen: 0,
		thinkingLen: 0,
	}
}

// ProcessChunk processes a single chunk and emits appropriate events.
//
// # Description
//
// Handles a single NDJSON chunk from Ollama streaming response:
// 1. Checks for error in chunk and emits error event
// 2. Processes thinking content (if present and not redacted)
// 3. Processes content tokens
// 4. Applies length limits and rate limiting
// 5. Returns done status from chunk
//
// # Inputs
//
//   - ctx: Context for cancellation and rate limiter waiting.
//   - chunk: Parsed Ollama stream chunk.
//   - callback: Callback to invoke for each event.
//
// # Outputs
//
//   - bool: True if chunk.Done is true (stream complete).
//   - error: Non-nil on callback error, rate limiter error, or chunk error.
//
// # Examples
//
//	chunk := &ollamaStreamChunk{Message: datatypes.Message{Content: "Hello"}}
//	done, err := processor.ProcessChunk(ctx, chunk, callback)
//
// # Limitations
//
//   - Truncates content silently when limits exceeded
//   - Logging should be added for truncation events
//
// # Assumptions
//
//   - chunk is non-nil and valid
//   - callback handles events quickly
func (p *DefaultStreamProcessor) ProcessChunk(ctx context.Context, chunk *ollamaStreamChunk, callback StreamCallback) (bool, error) {
	// Handle error in chunk
	if chunk.Error != "" {
		return p.handleChunkError(chunk, callback)
	}

	// Handle thinking content
	if err := p.processThinkingContent(ctx, chunk, callback); err != nil {
		return false, err
	}

	// Handle content tokens
	if err := p.processContentToken(ctx, chunk, callback); err != nil {
		return false, err
	}

	return chunk.Done, nil
}

// handleChunkError emits an error event for a chunk with an error field.
//
// # Description
//
// When Ollama returns an error in the stream (e.g., model not found),
// this method emits a StreamEventError and returns the error.
//
// # Inputs
//
//   - chunk: Chunk containing error message.
//   - callback: Callback to notify of error.
//
// # Outputs
//
//   - bool: Always true (stream should terminate).
//   - error: Always non-nil with the chunk error.
//
// # Examples
//
//	chunk := &ollamaStreamChunk{Error: "model not found"}
//	done, err := p.handleChunkError(chunk, callback)
//	// done=true, err="ollama stream error: model not found"
//
// # Limitations
//
//   - Callback error is ignored; chunk error takes precedence
//
// # Assumptions
//
//   - chunk.Error is non-empty
func (p *DefaultStreamProcessor) handleChunkError(chunk *ollamaStreamChunk, callback StreamCallback) (bool, error) {
	errEvent := StreamEvent{
		Type:  StreamEventError,
		Error: chunk.Error,
	}
	_ = callback(errEvent) // Best effort to notify
	return true, fmt.Errorf("ollama stream error: %s", chunk.Error)
}

// processThinkingContent handles thinking tokens from reasoning models.
//
// # Description
//
// Processes the Thinking field from chunks (gpt-oss, DeepSeek-R1 models).
// Applies redaction if configured, enforces length limits, and emits
// StreamEventThinking events.
//
// # Inputs
//
//   - ctx: Context for rate limiter.
//   - chunk: Chunk potentially containing thinking content.
//   - callback: Callback for thinking events.
//
// # Outputs
//
//   - error: Non-nil on callback error or rate limiter error.
//
// # Examples
//
//	chunk := &ollamaStreamChunk{Thinking: "Let me analyze..."}
//	err := p.processThinkingContent(ctx, chunk, callback)
//
// # Limitations
//
//   - Thinking is silently truncated at limit
//
// # Assumptions
//
//   - MaxThinkingLength=0 means unlimited
func (p *DefaultStreamProcessor) processThinkingContent(ctx context.Context, chunk *ollamaStreamChunk, callback StreamCallback) error {
	// Skip if no thinking or redaction enabled
	if chunk.Thinking == "" || p.cfg.RedactThinking {
		return nil
	}

	thinkingContent := chunk.Thinking

	// Apply thinking length limit
	if p.cfg.MaxThinkingLength > 0 {
		remaining := p.cfg.MaxThinkingLength - p.thinkingLen
		if remaining <= 0 {
			return nil // Already at limit
		}
		if len(thinkingContent) > remaining {
			thinkingContent = thinkingContent[:remaining]
		}
	}

	p.thinkingLen += len(thinkingContent)

	// Apply rate limiting
	if err := p.waitForRateLimiter(ctx); err != nil {
		return err
	}

	event := StreamEvent{
		Type:    StreamEventThinking,
		Content: thinkingContent,
	}
	if err := callback(event); err != nil {
		return fmt.Errorf("thinking callback error: %w", err)
	}

	return nil
}

// processContentToken handles content tokens from the response.
//
// # Description
//
// Processes the Message.Content field from chunks. Applies response
// length limits, rate limiting, and emits StreamEventToken events.
//
// # Inputs
//
//   - ctx: Context for rate limiter.
//   - chunk: Chunk potentially containing content.
//   - callback: Callback for token events.
//
// # Outputs
//
//   - error: Non-nil on callback error or rate limiter error.
//
// # Examples
//
//	chunk := &ollamaStreamChunk{Message: datatypes.Message{Content: "Hello"}}
//	err := p.processContentToken(ctx, chunk, callback)
//
// # Limitations
//
//   - Content is silently truncated at limit
//
// # Assumptions
//
//   - MaxResponseLength=0 means unlimited
func (p *DefaultStreamProcessor) processContentToken(ctx context.Context, chunk *ollamaStreamChunk, callback StreamCallback) error {
	// Skip if no content
	if chunk.Message.Content == "" {
		return nil
	}

	content := chunk.Message.Content

	// Apply response length limit
	if p.cfg.MaxResponseLength > 0 {
		remaining := p.cfg.MaxResponseLength - p.responseLen
		if remaining <= 0 {
			return nil // Already at limit
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
	}

	p.responseLen += len(content)
	p.tokenCount++

	// Apply rate limiting
	if err := p.waitForRateLimiter(ctx); err != nil {
		return err
	}

	event := StreamEvent{
		Type:    StreamEventToken,
		Content: content,
	}
	if err := callback(event); err != nil {
		return fmt.Errorf("content callback error: %w", err)
	}

	return nil
}

// waitForRateLimiter waits for rate limiter if configured.
//
// # Description
//
// Blocks until the rate limiter allows proceeding. Returns immediately
// if no rate limiter is configured.
//
// # Inputs
//
//   - ctx: Context for cancellation during wait.
//
// # Outputs
//
//   - error: Non-nil if context is cancelled during wait.
//
// # Examples
//
//	err := p.waitForRateLimiter(ctx)
//	if err != nil {
//	    return err // Context cancelled
//	}
//
// # Limitations
//
//   - May add significant latency with low rate limits
//
// # Assumptions
//
//   - Rate limiter was configured at construction time
func (p *DefaultStreamProcessor) waitForRateLimiter(ctx context.Context) error {
	if p.rateLimiter == nil {
		return nil
	}
	return p.rateLimiter.Wait(ctx)
}

// GetTokenCount returns the number of content tokens processed.
//
// # Description
//
// Returns the running count of content tokens (chunks with non-empty
// Message.Content) processed so far.
//
// # Outputs
//
//   - int: Number of content tokens.
//
// # Examples
//
//	count := processor.GetTokenCount()
//
// # Limitations
//
//   - Does not count thinking tokens
//
// # Assumptions
//
//   - Called after processing is complete or for progress reporting
func (p *DefaultStreamProcessor) GetTokenCount() int {
	return p.tokenCount
}

// GetResponseLength returns the total response characters processed.
//
// # Description
//
// Returns the total number of characters from content tokens,
// after any truncation from length limits.
//
// # Outputs
//
//   - int: Total response characters.
//
// # Examples
//
//	length := processor.GetResponseLength()
//
// # Limitations
//
//   - Does not include thinking content
//
// # Assumptions
//
//   - Called after processing is complete or for progress reporting
func (p *DefaultStreamProcessor) GetResponseLength() int {
	return p.responseLen
}

// =============================================================================
// ChatStream Method
// =============================================================================

// ChatStream streams a conversation response token-by-token.
//
// # Description
//
// Streams responses from Ollama's /api/chat endpoint with stream=true.
// Each token is delivered via the callback as a StreamEvent. Supports
// thinking models (gpt-oss, DeepSeek-R1) which emit thinking tokens.
// Uses default StreamConfig.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout. Cancellation stops streaming.
//   - messages: Conversation history with system, user, assistant messages.
//   - params: Generation parameters (temperature, max_tokens, etc).
//   - callback: Called for each token. Return error to abort streaming.
//
// # Outputs
//
//   - error: Non-nil on network failure, API error, or callback error.
//
// # Examples
//
//	var response strings.Builder
//	err := client.ChatStream(ctx, messages, params, func(e StreamEvent) error {
//	    switch e.Type {
//	    case StreamEventToken:
//	        response.WriteString(e.Content)
//	        fmt.Print(e.Content)
//	    case StreamEventThinking:
//	        fmt.Printf("[thinking] %s", e.Content)
//	    case StreamEventError:
//	        return fmt.Errorf("stream error: %s", e.Error)
//	    }
//	    return nil
//	})
//
// # Limitations
//
//   - Requires Ollama server to support streaming
//   - Thinking field only populated by thinking-capable models
//
// # Assumptions
//
//   - Ollama server is running and accessible
//   - Model supports chat API
func (o *OllamaClient) ChatStream(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, callback StreamCallback) error {
	return o.ChatStreamWithConfig(ctx, messages, params, callback, DefaultStreamConfig())
}

// ChatStreamWithConfig streams with explicit configuration.
//
// # Description
//
// Like ChatStream but accepts a StreamConfig for fine-grained control
// over privacy (thinking redaction), rate limiting, and length limits.
// This is the primary implementation; ChatStream delegates to this.
//
// # Inputs
//
//   - ctx: Context for cancellation and timeout.
//   - messages: Conversation history.
//   - params: Generation parameters.
//   - callback: Streaming event callback.
//   - cfg: Streaming configuration.
//
// # Outputs
//
//   - error: Non-nil on failure.
//
// # Examples
//
//	cfg := StreamConfig{RedactThinking: true, RateLimitPerSecond: 50}
//	err := client.ChatStreamWithConfig(ctx, messages, params, callback, cfg)
//
// # Limitations
//
//   - Rate limiting adds latency
//   - Truncation loses content
//
// # Assumptions
//
//   - Config values are reasonable (not negative, etc)
func (o *OllamaClient) ChatStreamWithConfig(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, callback StreamCallback, cfg StreamConfig) error {

	// Initialize metrics
	initStreamMetrics()

	// Start tracing span
	ctx, span := tracer.Start(ctx, "OllamaClient.ChatStream")
	defer span.End()

	// Set span attributes
	o.setStreamSpanAttributes(span, messages, cfg)

	startTime := time.Now()
	slog.Debug("Starting streaming chat via Ollama",
		"model", o.model,
		"num_messages", len(messages),
	)

	// Execute streaming request
	resp, err := o.executeStreamRequest(ctx, messages, params, span)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Process streaming response
	processor := NewDefaultStreamProcessor(cfg, nil)
	err = o.readStreamResponse(ctx, resp.Body, processor, callback)

	// Record completion metrics
	o.recordStreamMetrics(ctx, processor, startTime, err)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "stream processing error")
		return err
	}

	return nil
}

// setStreamSpanAttributes sets tracing attributes for streaming.
//
// # Description
//
// Configures the OpenTelemetry span with relevant attributes for
// debugging and monitoring streaming requests.
//
// # Inputs
//
//   - span: OpenTelemetry span to configure.
//   - messages: Conversation messages for count attribute.
//   - cfg: Stream config for privacy attribute.
//
// # Outputs
//
//   - None (modifies span in place).
//
// # Examples
//
//	o.setStreamSpanAttributes(span, messages, cfg)
//
// # Limitations
//
//   - Only sets basic attributes; more can be added
//
// # Assumptions
//
//   - span is valid and active
func (o *OllamaClient) setStreamSpanAttributes(span interface {
	SetAttributes(...attribute.KeyValue)
}, messages []datatypes.Message, cfg StreamConfig) {
	span.SetAttributes(
		attribute.String("llm.model", o.model),
		attribute.Int("llm.num_messages", len(messages)),
		attribute.Bool("stream.redact_thinking", cfg.RedactThinking),
	)
}

// executeStreamRequest sends the streaming request to Ollama.
//
// # Description
//
// Constructs and executes the HTTP POST request to Ollama's /api/chat
// endpoint with stream=true. Handles request construction, execution,
// and initial error checking (non-200 status).
//
// # Inputs
//
//   - ctx: Context for request.
//   - messages: Conversation messages.
//   - params: Generation parameters.
//   - span: Span for error recording.
//
// # Outputs
//
//   - *http.Response: Response with body ready for streaming. Caller must close.
//   - error: Non-nil on request failure or non-200 status.
//
// # Examples
//
//	resp, err := o.executeStreamRequest(ctx, messages, params, span)
//	if err != nil {
//	    return err
//	}
//	defer resp.Body.Close()
//
// # Limitations
//
//   - Does not retry on transient failures
//
// # Assumptions
//
//   - Ollama server is running at o.baseURL
func (o *OllamaClient) executeStreamRequest(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, span interface {
		RecordError(error, ...trace.EventOption)
		SetStatus(codes.Code, string)
	}) (*http.Response, error) {

	chatURL := o.baseURL + "/api/chat"
	options := o.buildStreamOptions(params)

	// Use ModelOverride if provided, otherwise use client's default model
	model := o.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	payload := ollamaChatRequest{
		Model:     model,
		Messages:  messages,
		Stream:    true,
		Options:   options,
		KeepAlive: params.KeepAlive, // Pass through keep_alive for multi-model support
	}

	reqBody, err := json.Marshal(payload)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal error")
		return nil, fmt.Errorf("failed to marshal streaming request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(reqBody))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request creation error")
		return nil, fmt.Errorf("failed to create streaming request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request error")
		o.recordStreamError(ctx, "connection")
		return nil, fmt.Errorf("streaming request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		span.RecordError(fmt.Errorf("status %d", resp.StatusCode))
		span.SetStatus(codes.Error, "non-200 status")
		o.recordStreamError(ctx, "http_error")
		return nil, fmt.Errorf("ollama streaming failed with status %d: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

// buildStreamOptions constructs options map from GenerationParams.
//
// # Description
//
// Builds the options map for Ollama streaming request from generation
// parameters. Uses defaults for nil values.
//
// # Inputs
//
//   - params: Generation parameters.
//
// # Outputs
//
//   - map[string]interface{}: Options for Ollama request.
//
// # Examples
//
//	temp := float32(0.7)
//	params := GenerationParams{Temperature: &temp}
//	options := o.buildStreamOptions(params)
//	// options["temperature"] = 0.7
//
// # Limitations
//
//   - Only supports common parameters; Ollama-specific options not exposed
//
// # Assumptions
//
//   - Defaults are appropriate for most use cases
func (o *OllamaClient) buildStreamOptions(params GenerationParams) map[string]interface{} {
	options := make(map[string]interface{})

	if params.Temperature != nil {
		options["temperature"] = *params.Temperature
	} else {
		options["temperature"] = float32(0.2)
	}

	if params.TopK != nil {
		options["top_k"] = *params.TopK
	} else {
		options["top_k"] = 20
	}

	if params.TopP != nil {
		options["top_p"] = *params.TopP
	} else {
		options["top_p"] = float32(0.9)
	}

	if params.MaxTokens != nil {
		options["num_predict"] = *params.MaxTokens
	} else {
		options["num_predict"] = 8192
	}

	if len(params.Stop) > 0 {
		options["stop"] = params.Stop
	}

	// Set context window size if provided
	if params.NumCtx != nil && *params.NumCtx > 0 {
		options["num_ctx"] = *params.NumCtx
	}

	return options
}

// readStreamResponse reads NDJSON stream and processes each chunk.
//
// # Description
//
// Reads the response body line by line, parsing each NDJSON chunk and
// delegating to the processor. Handles scanner setup, context cancellation,
// and chunk parsing.
//
// # Inputs
//
//   - ctx: Context for cancellation checking.
//   - body: Response body reader.
//   - processor: StreamProcessor to handle chunks.
//   - callback: Callback passed to processor.
//
// # Outputs
//
//   - error: Non-nil on parsing error, processor error, or context cancellation.
//
// # Examples
//
//	processor := NewDefaultStreamProcessor(cfg, nil)
//	err := o.readStreamResponse(ctx, resp.Body, processor, callback)
//
// # Limitations
//
//   - Max line size is 1MB
//   - Malformed chunks are skipped with warning
//
// # Assumptions
//
//   - Ollama returns well-formed NDJSON
func (o *OllamaClient) readStreamResponse(ctx context.Context, body io.Reader,
	processor StreamProcessor, callback StreamCallback) error {

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // 64KB buffer, 1MB max line

	for scanner.Scan() {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Parse chunk
		chunk, err := o.parseStreamChunk(line)
		if err != nil {
			slog.Warn("Failed to parse stream chunk", "error", err)
			continue
		}

		// Process chunk
		done, err := processor.ProcessChunk(ctx, chunk, callback)
		if err != nil {
			return err
		}

		if done {
			slog.Debug("Stream completed via done flag")
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	return nil
}

// parseStreamChunk parses a single NDJSON line.
//
// # Description
//
// Unmarshals a JSON line from the streaming response into a structured
// chunk. Returns error if JSON is malformed.
//
// # Inputs
//
//   - line: Raw bytes of a single NDJSON line.
//
// # Outputs
//
//   - *ollamaStreamChunk: Parsed chunk.
//   - error: Non-nil if JSON parsing fails.
//
// # Examples
//
//	line := []byte(`{"message":{"content":"Hi"},"done":false}`)
//	chunk, err := o.parseStreamChunk(line)
//
// # Limitations
//
//   - Expects valid JSON
//
// # Assumptions
//
//   - line is a complete JSON object
func (o *OllamaClient) parseStreamChunk(line []byte) (*ollamaStreamChunk, error) {
	var chunk ollamaStreamChunk
	if err := json.Unmarshal(line, &chunk); err != nil {
		return nil, fmt.Errorf("invalid JSON chunk: %w", err)
	}
	return &chunk, nil
}

// recordStreamMetrics records OTel metrics for stream completion.
//
// # Description
//
// Records duration histogram, token count, and error count (if error)
// to OpenTelemetry metrics.
//
// # Inputs
//
//   - ctx: Context for metrics.
//   - processor: Processor with token count.
//   - startTime: When streaming started.
//   - err: Error if streaming failed (nil on success).
//
// # Outputs
//
//   - None (records to OTel).
//
// # Examples
//
//	o.recordStreamMetrics(ctx, processor, startTime, nil)
//
// # Limitations
//
//   - Requires metrics to be initialized
//
// # Assumptions
//
//   - initStreamMetrics was called
func (o *OllamaClient) recordStreamMetrics(ctx context.Context, processor StreamProcessor,
	startTime time.Time, err error) {

	duration := time.Since(startTime).Seconds()
	tokenCount := processor.GetTokenCount()

	if streamDuration != nil {
		streamDuration.Record(ctx, duration,
			metric.WithAttributes(attribute.String("model", o.model)))
	}
	if streamTokenCount != nil {
		streamTokenCount.Add(ctx, int64(tokenCount),
			metric.WithAttributes(attribute.String("model", o.model)))
	}
	if err != nil && streamErrorCount != nil {
		streamErrorCount.Add(ctx, 1,
			metric.WithAttributes(attribute.String("error_type", "processing")))
	}

	slog.Debug("Streaming completed",
		"model", o.model,
		"tokens", tokenCount,
		"duration_ms", time.Since(startTime).Milliseconds(),
	)
}

// recordStreamError records a streaming error metric.
//
// # Description
//
// Records a streaming error to the OTel error counter with the given
// error type attribute.
//
// # Inputs
//
//   - ctx: Context for metrics.
//   - errorType: Type of error (connection, http_error, processing).
//
// # Outputs
//
//   - None (records to OTel).
//
// # Examples
//
//	o.recordStreamError(ctx, "connection")
//
// # Limitations
//
//   - Requires streamErrorCount to be initialized
//
// # Assumptions
//
//   - initStreamMetrics was called
func (o *OllamaClient) recordStreamError(ctx context.Context, errorType string) {
	if streamErrorCount != nil {
		streamErrorCount.Add(ctx, 1,
			metric.WithAttributes(attribute.String("error_type", errorType)))
	}
}
