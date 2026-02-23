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
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/orchestrator/datatypes"
)

// GeminiClient implements LLMClient for Google Gemini models.
//
// Description:
//
//	Uses the Gemini REST API (generateContent) for chat and generation.
//	Supports text generation and multi-turn conversations.
//
// Thread Safety: GeminiClient is safe for concurrent use.
type GeminiClient struct {
	httpClient *http.Client
	apiKey     string
	model      string
	baseURL    string
}

// NewGeminiClient creates a new GeminiClient from environment variables.
//
// Description:
//
//	Reads GEMINI_API_KEY and GEMINI_MODEL from the environment.
//	Defaults to "gemini-1.5-flash" if GEMINI_MODEL is not set.
//
// Outputs:
//   - *GeminiClient: The configured client.
//   - error: Non-nil if GEMINI_API_KEY is missing.
//
// NewGeminiClientWithConfig creates a GeminiClient with explicit configuration.
//
// Description:
//
//	Creates a GeminiClient without reading environment variables. Useful
//	for testing with mock servers or when configuration comes from a source
//	other than environment variables.
//
// Inputs:
//   - apiKey: The Gemini API key.
//   - model: The model name (e.g., "gemini-1.5-flash").
//   - baseURL: The base URL for API requests (e.g., "https://generativelanguage.googleapis.com/v1beta").
//
// Outputs:
//   - *GeminiClient: The configured client.
func NewGeminiClientWithConfig(apiKey, model, baseURL string) *GeminiClient {
	return &GeminiClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
	}
}

func NewGeminiClient() (*GeminiClient, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("gemini: API key is missing (GEMINI_API_KEY)")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-1.5-flash"
		slog.Info("GEMINI_MODEL not set, defaulting to gemini-1.5-flash")
	}

	slog.Info("Initializing Gemini client", slog.String("model", model))

	return &GeminiClient{
		httpClient: &http.Client{Timeout: 120 * time.Second},
		apiKey:     apiKey,
		model:      model,
		baseURL:    "https://generativelanguage.googleapis.com/v1beta",
	}, nil
}

// geminiRequest is the request payload for the Gemini generateContent API.
type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiToolDeclaration `json:"tools,omitempty"`
}

// geminiContent represents a content block in the Gemini API.
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

// geminiPart represents a part of a content block.
type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResp `json:"functionResponse,omitempty"`
}

// geminiFunctionCall represents a function call from the model.
type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// geminiFunctionResp represents a function response to send back.
type geminiFunctionResp struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

// geminiFunctionDeclaration defines a function for the Gemini API.
type geminiFunctionDeclaration struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// geminiToolDeclaration wraps function declarations for the tools array.
type geminiToolDeclaration struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

// geminiGenerationConfig controls generation behavior.
type geminiGenerationConfig struct {
	Temperature     *float32 `json:"temperature,omitempty"`
	TopP            *float32 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

// geminiResponse is the response from the Gemini generateContent API.
type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata,omitempty"`
	Error         *geminiError      `json:"error,omitempty"`
}

// geminiCandidate represents a candidate response.
type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

// geminiUsage contains token usage information.
type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// geminiError represents an API error.
type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Generate implements LLMClient.Generate using the Gemini API.
func (g *GeminiClient) Generate(ctx context.Context, prompt string, params GenerationParams) (string, error) {
	messages := []datatypes.Message{
		{Role: "user", Content: prompt},
	}
	return g.Chat(ctx, messages, params)
}

// Chat implements LLMClient.Chat using the Gemini generateContent API.
func (g *GeminiClient) Chat(ctx context.Context, messages []datatypes.Message, params GenerationParams) (string, error) {
	model := g.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	reqPayload := g.buildRequest(messages, params)

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("gemini: marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", g.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("gemini: creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.apiKey)

	slog.Debug("Sending request to Gemini",
		slog.String("model", model),
		slog.Int("content_count", len(reqPayload.Contents)),
	)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gemini: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	var apiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", fmt.Errorf("gemini: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return "", fmt.Errorf("gemini: API error [%d] %s: %s",
			apiResp.Error.Code, apiResp.Error.Status, SafeLogString(apiResp.Error.Message))
	}

	if len(apiResp.Candidates) == 0 {
		return "", fmt.Errorf("gemini: returned no candidates")
	}

	// Extract text from the first candidate
	var textParts []string
	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
	}

	result := strings.Join(textParts, "")
	if result == "" {
		return "", fmt.Errorf("gemini: returned empty text content")
	}

	slog.Debug("Received Gemini response",
		slog.String("model", model),
		slog.Int("response_len", len(result)),
		slog.String("finish_reason", apiResp.Candidates[0].FinishReason),
	)

	return result, nil
}

// ChatWithTools sends a chat request with tool definitions and returns tool calls.
//
// Description:
//
//	Extends Chat to support Gemini's function calling API. Converts generic
//	ToolDef and ChatMessage types to Gemini wire format, including
//	functionCall and functionResponse parts.
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
func (g *GeminiClient) ChatWithTools(ctx context.Context, messages []ChatMessage,
	params GenerationParams, tools []ToolDef) (*ChatWithToolsResult, error) {

	model := g.model
	if params.ModelOverride != "" {
		model = params.ModelOverride
	}

	slog.Debug("ChatWithTools via Gemini",
		slog.String("model", model),
		slog.Int("messages", len(messages)),
		slog.Int("tools", len(tools)),
	)

	req := geminiRequest{}

	// Build generation config
	genConfig := g.buildGenConfig(params)
	if genConfig != nil {
		req.GenerationConfig = genConfig
	}

	// Convert tools
	if len(tools) > 0 {
		var funcDecls []geminiFunctionDeclaration
		for _, td := range tools {
			funcDecls = append(funcDecls, geminiFunctionDeclaration{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  td.Function.Parameters,
			})
		}
		req.Tools = []geminiToolDeclaration{{FunctionDeclarations: funcDecls}}
	}

	// Convert messages
	for _, msg := range messages {
		switch {
		case msg.Role == "system":
			req.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: msg.Content}},
			}

		case msg.Role == "tool" && msg.ToolName != "":
			// Tool result → functionResponse part
			var respData map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Content), &respData); err != nil {
				// If not valid JSON, wrap in a result object
				respData = map[string]interface{}{"result": msg.Content}
			}
			req.Contents = append(req.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{
					{FunctionResponse: &geminiFunctionResp{
						Name:     msg.ToolName,
						Response: respData,
					}},
				},
			})

		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			// Assistant with tool calls → functionCall parts
			var parts []geminiPart
			if msg.Content != "" {
				parts = append(parts, geminiPart{Text: msg.Content})
			}
			for _, tc := range msg.ToolCalls {
				var args map[string]interface{}
				if err := json.Unmarshal(tc.Arguments, &args); err != nil {
					args = map[string]interface{}{}
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: args,
					},
				})
			}
			req.Contents = append(req.Contents, geminiContent{
				Role:  "model",
				Parts: parts,
			})

		case msg.Role == "assistant":
			req.Contents = append(req.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: msg.Content}},
			})

		case msg.Role == "user":
			req.Contents = append(req.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})

		default:
			req.Contents = append(req.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}

	// Marshal and send request
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshaling request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", g.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("gemini: creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: API returned status %d: %s", resp.StatusCode, SafeLogString(string(bodyBytes)))
	}

	var apiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("gemini: parsing response JSON: %w", err)
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("gemini: API error [%d] %s: %s",
			apiResp.Error.Code, apiResp.Error.Status, SafeLogString(apiResp.Error.Message))
	}

	if len(apiResp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: returned no candidates")
	}

	// Parse response parts
	result := &ChatWithToolsResult{}
	var textParts []string
	callIndex := 0

	for _, part := range apiResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			// Convert args to JSON
			argsJSON, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				argsJSON = []byte(`{}`)
			}
			// Generate synthetic ID since Gemini doesn't provide one
			result.ToolCalls = append(result.ToolCalls, ToolCallResponse{
				ID:        fmt.Sprintf("gemini-call-%d", callIndex),
				Name:      part.FunctionCall.Name,
				Arguments: json.RawMessage(argsJSON),
			})
			callIndex++
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

// buildGenConfig creates a generation config from params.
func (g *GeminiClient) buildGenConfig(params GenerationParams) *geminiGenerationConfig {
	genConfig := &geminiGenerationConfig{}
	hasConfig := false

	if params.Temperature != nil {
		genConfig.Temperature = params.Temperature
		hasConfig = true
	}
	if params.TopP != nil {
		genConfig.TopP = params.TopP
		hasConfig = true
	}
	if params.TopK != nil {
		genConfig.TopK = params.TopK
		hasConfig = true
	}
	if params.MaxTokens != nil {
		genConfig.MaxOutputTokens = params.MaxTokens
		hasConfig = true
	}
	if len(params.Stop) > 0 {
		genConfig.StopSequences = params.Stop
		hasConfig = true
	}

	if hasConfig {
		return genConfig
	}
	return nil
}

// ChatStream implements LLMClient.ChatStream. Currently not implemented for Gemini.
func (g *GeminiClient) ChatStream(ctx context.Context, messages []datatypes.Message,
	params GenerationParams, callback StreamCallback) error {
	return fmt.Errorf("gemini: streaming not implemented")
}

// buildRequest constructs the Gemini API request from messages and params.
func (g *GeminiClient) buildRequest(messages []datatypes.Message, params GenerationParams) geminiRequest {
	req := geminiRequest{}

	// Build generation config
	genConfig := &geminiGenerationConfig{}
	hasGenConfig := false

	if params.Temperature != nil {
		genConfig.Temperature = params.Temperature
		hasGenConfig = true
	}
	if params.TopP != nil {
		genConfig.TopP = params.TopP
		hasGenConfig = true
	}
	if params.TopK != nil {
		genConfig.TopK = params.TopK
		hasGenConfig = true
	}
	if params.MaxTokens != nil {
		genConfig.MaxOutputTokens = params.MaxTokens
		hasGenConfig = true
	}
	if len(params.Stop) > 0 {
		genConfig.StopSequences = params.Stop
		hasGenConfig = true
	}

	if hasGenConfig {
		req.GenerationConfig = genConfig
	}

	// Convert messages to Gemini format
	for _, msg := range messages {
		switch strings.ToLower(msg.Role) {
		case "system":
			// Gemini uses systemInstruction for system prompts
			req.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: msg.Content}},
			}
		case "user":
			req.Contents = append(req.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		case "assistant":
			req.Contents = append(req.Contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		default:
			// Map unknown roles to user
			req.Contents = append(req.Contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: msg.Content}},
			})
		}
	}

	return req
}
