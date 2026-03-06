// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
)

// proxyTracer is the package-level OpenTelemetry tracer.
var proxyTracer = otel.Tracer("aleutian.trace.proxy")

// proxyMeter is the package-level OpenTelemetry meter.
var proxyMeter = otel.Meter("aleutian.trace.proxy")

// sessionEntry tracks an agent session for a conversation thread.
type sessionEntry struct {
	sessionID string
	lastUsed  time.Time
}

// ProxyServer is the OpenAI-compatible proxy that delegates to the trace
// server's agent loop.
//
// Description:
//
//	Translates OpenAI chat completion requests into trace agent loop calls,
//	providing CRS disambiguation, proof tracking, and all 24+ agent tools
//	transparently to any OpenAI-compatible client.
//
// Thread Safety: Safe for concurrent use after construction.
type ProxyServer struct {
	config   ProxyConfig
	client   *http.Client
	sessions sync.Map // map[string]*sessionEntry (thread hash → session)

	// OTel metrics
	requestCounter   metric.Int64Counter
	requestHistogram metric.Float64Histogram
}

// NewProxyServer creates a new proxy server with the given configuration.
//
// Description:
//
//	Initializes the proxy server with an HTTP client configured for the
//	specified timeout and registers OTel metrics instruments.
//
// Inputs:
//
//	config - proxy configuration (immutable after this call)
//
// Outputs:
//
//	*ProxyServer - the initialized proxy server
//
// Limitations:
//
//	The HTTP client timeout applies to the full agent run duration. Long-running
//	agent loops may need an increased timeout.
func NewProxyServer(config ProxyConfig) *ProxyServer {
	p := &ProxyServer{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
	}

	// Register OTel metrics. Errors are non-fatal — metrics just won't report.
	var err error
	p.requestCounter, err = proxyMeter.Int64Counter(
		"proxy_requests_total",
		metric.WithDescription("Total number of proxy requests"),
	)
	if err != nil {
		slog.Warn("Failed to create request counter", slog.String("error", err.Error()))
	}

	p.requestHistogram, err = proxyMeter.Float64Histogram(
		"proxy_request_duration_seconds",
		metric.WithDescription("Duration of proxy requests in seconds"),
	)
	if err != nil {
		slog.Warn("Failed to create request histogram", slog.String("error", err.Error()))
	}

	return p
}

// RegisterRoutes registers all HTTP handlers on the given mux.
//
// Description:
//
//	Sets up the OpenAI-compatible endpoints and health/init endpoints.
//
// Inputs:
//
//	mux - the HTTP request multiplexer to register routes on
func (p *ProxyServer) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/chat/completions", p.handleChatCompletions)
	mux.HandleFunc("GET /v1/models", p.handleModels)
	mux.HandleFunc("GET /health", p.handleHealth)
	mux.HandleFunc("POST /init", p.handleInit)
}

// handleChatCompletions handles POST /v1/chat/completions.
//
// Description:
//
//	Main proxy endpoint. Extracts the user query from the OpenAI message
//	history, delegates to the trace server agent loop, and translates
//	the agent response back to OpenAI format.
//
//	Session correlation: hashes the first user message to generate a stable
//	thread ID. Same conversation thread → same agent session (via /continue).
//
//	Streaming: When stream=true, buffers the full agent response and emits
//	it as a single SSE chunk followed by [DONE].
//
// Inputs:
//
//	w - HTTP response writer
//	r - HTTP request with ChatCompletionRequest body
//
// Outputs:
//
//	Writes ChatCompletionResponse (or SSE chunks) to w.
//
// Assumptions:
//
//	OpenAI clients send full message history on every request. The first
//	user message is stable across requests in the same conversation.
func (p *ProxyServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := r.Context()

	ctx, span := proxyTracer.Start(ctx, "proxy.handleChatCompletions")
	defer span.End()

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())

	// Decode request. Limit body to 10 MB (large conversations can be big).
	const maxChatBody = 10 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.SetStatus(codes.Error, "invalid request body")
		logger.Warn("Invalid request body", slog.String("error", err.Error()))
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Extract last user message as query.
	query := extractLastUserMessage(req.Messages)
	if query == "" {
		span.SetStatus(codes.Error, "no user message found")
		writeError(w, http.StatusBadRequest, "no user message found in messages array")
		return
	}

	span.SetAttributes(
		attribute.String("proxy.model", req.Model),
		attribute.Int("proxy.query_length", len(query)),
		attribute.Bool("proxy.stream", req.Stream),
	)

	// Resolve project root: header > config default.
	projectRoot := r.Header.Get("X-Project-Root")
	if projectRoot == "" {
		projectRoot = p.config.ProjectRoot
	}
	if projectRoot == "" {
		span.SetStatus(codes.Error, "no project root")
		writeError(w, http.StatusBadRequest, "project_root required: set X-Project-Root header or --project-root flag")
		return
	}

	// Session correlation: hash first user message to get thread key.
	threadKey := computeThreadKey(req.Messages)
	span.SetAttributes(attribute.String("proxy.thread_key", threadKey))

	// Streaming path: write SSE headers immediately, send heartbeat comments
	// during the blocking agent call so clients don't timeout or show stale UI.
	if req.Stream {
		p.handleStreaming(ctx, w, span, logger, start, req.Model, threadKey, projectRoot, query)
		return
	}

	// Non-streaming path: block until agent completes, return JSON.
	agentResp, sessionReused, err := p.callAgentLoop(ctx, threadKey, projectRoot, query)
	if err != nil {
		telemetry.RecordError(span, err)
		logger.Error("Agent loop call failed", slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, "agent loop error: "+err.Error())
		return
	}

	p.recordCompletion(ctx, span, logger, start, agentResp, sessionReused)

	openAIResp, statusCode := p.translateResponse(agentResp, req.Model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(openAIResp); err != nil {
		logger.Error("Failed to encode response", slog.String("error", err.Error()))
	}
}

// handleModels handles GET /v1/models.
//
// Description:
//
//	Proxies to Ollama's /api/tags endpoint and reformats the response
//	as an OpenAI-compatible model list.
//
// Inputs:
//
//	w - HTTP response writer
//	r - HTTP request
//
// Outputs:
//
//	Writes ModelListResponse to w.
func (p *ProxyServer) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := proxyTracer.Start(ctx, "proxy.handleModels")
	defer span.End()

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())

	resp, err := p.client.Get(p.config.OllamaURL + "/api/tags")
	if err != nil {
		telemetry.RecordError(span, err)
		logger.Warn("Failed to reach Ollama", slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, "failed to reach Ollama: "+err.Error())
		return
	}
	defer resp.Body.Close()

	var ollamaResp OllamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		telemetry.RecordError(span, err)
		writeError(w, http.StatusBadGateway, "failed to decode Ollama response: "+err.Error())
		return
	}

	models := make([]ModelObject, 0, len(ollamaResp.Models))
	now := time.Now().Unix()
	for _, m := range ollamaResp.Models {
		models = append(models, ModelObject{
			ID:      m.Name,
			Object:  "model",
			Created: now,
			OwnedBy: "ollama",
		})
	}

	telemetry.SetSpanOK(span)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ModelListResponse{
		Object: "list",
		Data:   models,
	}); err != nil {
		logger.Error("Failed to encode models response", slog.String("error", err.Error()))
	}
}

// handleHealth handles GET /health.
//
// Description:
//
//	Returns the combined health status of the proxy, trace server, and Ollama.
//	The proxy itself is always "up" if this endpoint responds.
//
// Inputs:
//
//	w - HTTP response writer
//	r - HTTP request
//
// Outputs:
//
//	Writes HealthResponse to w.
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, span := proxyTracer.Start(ctx, "proxy.handleHealth")
	defer span.End()

	healthClient := &http.Client{Timeout: 3 * time.Second}

	traceStatus := "up"
	if resp, err := healthClient.Get(p.config.TraceURL + "/v1/trace/health"); err != nil {
		traceStatus = "down"
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			traceStatus = "down"
		}
	}

	ollamaStatus := "up"
	if resp, err := healthClient.Get(p.config.OllamaURL + "/api/tags"); err != nil {
		ollamaStatus = "down"
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			ollamaStatus = "down"
		}
	}

	overall := "healthy"
	if traceStatus == "down" && ollamaStatus == "down" {
		overall = "unhealthy"
	} else if traceStatus == "down" || ollamaStatus == "down" {
		overall = "degraded"
	}

	telemetry.SetSpanOK(span)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(HealthResponse{
		Status:      overall,
		Proxy:       "up",
		TraceServer: traceStatus,
		Ollama:      ollamaStatus,
	}); err != nil {
		slog.Error("Failed to encode health response", slog.String("error", err.Error()))
	}
}

// handleInit handles POST /init.
//
// Description:
//
//	Forwards project initialization to the trace server's /v1/trace/init
//	endpoint. This triggers code graph construction for the specified project.
//
// Inputs:
//
//	w - HTTP response writer
//	r - HTTP request with {"project_root": "..."} body
//
// Outputs:
//
//	Proxies the trace server response to w.
func (p *ProxyServer) handleInit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, span := proxyTracer.Start(ctx, "proxy.handleInit")
	defer span.End()

	logger := telemetry.LoggerWithTrace(ctx, slog.Default())

	// Limit request body to 1 MB to prevent memory exhaustion.
	const maxInitBody = 1 << 20
	limitedBody := http.MaxBytesReader(w, r.Body, maxInitBody)

	var initReq initRequest
	if err := json.NewDecoder(limitedBody).Decode(&initReq); err != nil {
		telemetry.RecordError(span, err)
		writeError(w, http.StatusBadRequest, "failed to decode init request: "+err.Error())
		return
	}

	// Translate host path to container path if configured.
	initReq.ProjectRoot = p.translatePath(initReq.ProjectRoot)

	body, err := json.Marshal(initReq)
	if err != nil {
		telemetry.RecordError(span, err)
		writeError(w, http.StatusInternalServerError, "failed to marshal init request: "+err.Error())
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.config.TraceURL+"/v1/trace/init", bytes.NewReader(body))
	if err != nil {
		telemetry.RecordError(span, err)
		writeError(w, http.StatusInternalServerError, "failed to create request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		telemetry.RecordError(span, err)
		logger.Error("Failed to reach trace server", slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, "failed to reach trace server: "+err.Error())
		return
	}
	defer resp.Body.Close()

	telemetry.SetSpanOK(span)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		logger.Error("Failed to copy init response", slog.String("error", err.Error()))
	}
}

// callAgentLoop calls the trace server agent loop, reusing existing sessions
// when possible.
//
// Description:
//
//	Determines whether to create a new agent session (POST /v1/trace/agent/run)
//	or continue an existing one (POST /v1/trace/agent/continue) based on the
//	conversation thread key.
//
// Inputs:
//
//	ctx         - request context with tracing
//	threadKey   - hash of the first user message (stable per conversation)
//	projectRoot - absolute path to the project directory
//	query       - the user's latest message
//
// Outputs:
//
//	*agentRunResponse - the agent's response
//	bool              - true if an existing session was reused
//	error             - any error from the agent call
//
// Assumptions:
//
//	The trace server agent endpoints are available at config.TraceURL.
func (p *ProxyServer) callAgentLoop(ctx context.Context, threadKey, projectRoot, query string) (*agentRunResponse, bool, error) {
	ctx, span := proxyTracer.Start(ctx, "proxy.callAgentLoop",
		trace.WithAttributes(attribute.String("thread_key", threadKey)),
	)
	defer span.End()

	// Check for existing session.
	if entry, ok := p.sessions.Load(threadKey); ok {
		se := entry.(*sessionEntry)
		// Check TTL (1 hour). Read lastUsed atomically via sync.Map load.
		if time.Since(se.lastUsed) < time.Hour {
			// Store a new entry to avoid racing on lastUsed mutation.
			p.sessions.Store(threadKey, &sessionEntry{
				sessionID: se.sessionID,
				lastUsed:  time.Now(),
			})
			resp, err := p.callAgentContinue(ctx, se.sessionID, query)
			if err != nil {
				// Session may have expired on server side — fall through to new run.
				slog.Warn("Agent continue failed, creating new session",
					slog.String("session_id", se.sessionID),
					slog.String("error", err.Error()),
				)
				p.sessions.Delete(threadKey)
			} else {
				span.SetAttributes(attribute.Bool("session_reused", true))
				return resp, true, nil
			}
		} else {
			p.sessions.Delete(threadKey)
		}
	}

	// New session.
	resp, err := p.callAgentRun(ctx, projectRoot, query)
	if err != nil {
		return nil, false, err
	}

	// Store session mapping.
	p.sessions.Store(threadKey, &sessionEntry{
		sessionID: resp.SessionID,
		lastUsed:  time.Now(),
	})

	span.SetAttributes(attribute.Bool("session_reused", false))
	return resp, false, nil
}

// agentRunResponse mirrors the trace server's AgentRunResponse. Defined here
// to avoid importing the trace service package (the proxy communicates via HTTP).
type agentRunResponse struct {
	SessionID    string         `json:"session_id"`
	State        string         `json:"state"`
	StepsTaken   int            `json:"steps_taken"`
	TokensUsed   int            `json:"tokens_used"`
	Response     string         `json:"response,omitempty"`
	NeedsClarify *clarifyDetail `json:"needs_clarify,omitempty"`
	Error        string         `json:"error,omitempty"`
	DegradedMode bool           `json:"degraded_mode"`
}

// clarifyDetail contains clarification request details.
type clarifyDetail struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"`
}

// callAgentRun sends POST /v1/trace/agent/run to create a new agent session.
//
// Inputs:
//
//	ctx         - request context
//	projectRoot - project root path
//	query       - user query
//
// Outputs:
//
//	*agentRunResponse - the agent's response
//	error             - HTTP or decoding errors
func (p *ProxyServer) callAgentRun(ctx context.Context, projectRoot, query string) (*agentRunResponse, error) {
	payload := agentRunRequest{
		ProjectRoot: p.translatePath(projectRoot),
		Query:       query,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent run request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.config.TraceURL+"/v1/trace/agent/run", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating agent run request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling agent run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent run returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var agentResp agentRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("decoding agent run response: %w", err)
	}

	return &agentResp, nil
}

// callAgentContinue sends POST /v1/trace/agent/continue to continue an
// existing agent session.
//
// Inputs:
//
//	ctx           - request context
//	sessionID     - the session to continue
//	clarification - the user's follow-up message
//
// Outputs:
//
//	*agentRunResponse - the agent's response
//	error             - HTTP or decoding errors
func (p *ProxyServer) callAgentContinue(ctx context.Context, sessionID, clarification string) (*agentRunResponse, error) {
	payload := agentContinueRequest{
		SessionID:     sessionID,
		Clarification: clarification,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent continue request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.config.TraceURL+"/v1/trace/agent/continue", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating agent continue request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling agent continue: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent continue returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var agentResp agentRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
		return nil, fmt.Errorf("decoding agent continue response: %w", err)
	}

	return &agentResp, nil
}

// translateResponse converts an agent loop response to OpenAI format.
//
// Description:
//
//	Maps agent states to OpenAI response format:
//	  - COMPLETE → response as assistant content, finish_reason "stop"
//	  - CLARIFY  → question as assistant content, finish_reason "stop"
//	  - ERROR    → error message as content, HTTP 500
//	  - Other    → state + response as content, finish_reason "stop"
//
// Inputs:
//
//	resp  - the agent loop response
//	model - the model name to echo back
//
// Outputs:
//
//	*ChatCompletionResponse - the OpenAI-formatted response
//	int                     - the HTTP status code to return
func (p *ProxyServer) translateResponse(resp *agentRunResponse, model string) (*ChatCompletionResponse, int) {
	var content string
	var statusCode int

	switch resp.State {
	case "COMPLETE":
		content = resp.Response
		statusCode = http.StatusOK
	case "CLARIFY":
		if resp.NeedsClarify != nil {
			content = resp.NeedsClarify.Question
		} else {
			content = "I need more information to answer your question."
		}
		statusCode = http.StatusOK
	case "ERROR":
		content = "Error: " + resp.Error
		statusCode = http.StatusInternalServerError
	default:
		// Intermediate states (PLAN, EXECUTE, etc.) — return whatever response
		// is available.
		content = resp.Response
		if content == "" {
			content = fmt.Sprintf("Agent is in state %s", resp.State)
		}
		statusCode = http.StatusOK
	}

	completionID := fmt.Sprintf("chatcmpl-%s", resp.SessionID)

	return &ChatCompletionResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []Choice{
			{
				Index: 0,
				Message: ChatMessage{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
		Usage: &Usage{
			TotalTokens: resp.TokensUsed,
		},
	}, statusCode
}

// heartbeatInterval controls how often SSE heartbeat comments are sent during
// the blocking agent call. SSE comments (lines starting with ':') are ignored
// by OpenAI client libraries but keep the connection alive and prevent
// clients from showing timeout/stall indicators.
const heartbeatInterval = 2 * time.Second

// handleStreaming handles the streaming code path for chat completions.
//
// Description:
//
//	Writes SSE headers immediately, then sends heartbeat comments every 2
//	seconds while the agent loop executes. When the agent completes, stops
//	the heartbeat and emits the response as a single SSE data chunk followed
//	by [DONE].
//
//	This prevents the 5-30 second silent hang that causes users to cancel.
//	SSE comments (": processing") are spec-compliant and ignored by OpenAI
//	client parsers, but they keep the HTTP connection alive and signal to
//	browser-based UIs that data is still coming.
//
// Inputs:
//
//	ctx         - request context with tracing
//	w           - HTTP response writer (must implement http.Flusher)
//	span        - the parent OTel span
//	logger      - structured logger with trace context
//	start       - request start time for duration metrics
//	model       - model name to echo in the response
//	threadKey   - session correlation key
//	projectRoot - project root for the agent run
//	query       - the user's query
//
// Assumptions:
//
//	w implements http.Flusher (true for net/http default and httptest).
func (p *ProxyServer) handleStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	span trace.Span,
	logger *slog.Logger,
	start time.Time,
	model, threadKey, projectRoot, query string,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// ResponseWriter doesn't support flushing — fall back to buffered.
		logger.Warn("ResponseWriter does not implement http.Flusher, falling back to buffered")
		agentResp, sessionReused, err := p.callAgentLoop(ctx, threadKey, projectRoot, query)
		if err != nil {
			telemetry.RecordError(span, err)
			writeError(w, http.StatusBadGateway, "agent loop error: "+err.Error())
			return
		}
		p.recordCompletion(ctx, span, logger, start, agentResp, sessionReused)
		openAIResp, _ := p.translateResponse(agentResp, model)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(openAIResp)
		return
	}

	// Write SSE headers immediately so the client knows we're alive.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Start heartbeat: send SSE comments every 2 seconds while the agent
	// loop is running. The ':' prefix makes these SSE comments, which are
	// silently ignored by all OpenAI-compatible client parsers.
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(w, ": processing\n\n")
				flusher.Flush()
			}
		}
	}()

	// Block on the agent call. Heartbeats keep the connection alive.
	agentResp, sessionReused, err := p.callAgentLoop(ctx, threadKey, projectRoot, query)
	close(done)
	<-stopped // Wait for heartbeat goroutine to fully exit before writing.

	if err != nil {
		telemetry.RecordError(span, err)
		logger.Error("Agent loop call failed (streaming)", slog.String("error", err.Error()))
		// Headers already sent as 200 — emit error as SSE data so the client
		// can display it. This matches OpenAI's behavior for mid-stream errors.
		errChunk := ChatCompletionChunk{
			ID:      "chatcmpl-error",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChunkChoice{{
				Index: 0,
				Delta: ChatMessageDelta{
					Role:    "assistant",
					Content: "Error: " + err.Error(),
				},
			}},
		}
		writeSSEChunk(w, flusher, errChunk)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	p.recordCompletion(ctx, span, logger, start, agentResp, sessionReused)

	// Emit the response as SSE data.
	openAIResp, _ := p.translateResponse(agentResp, model)

	content := ""
	if len(openAIResp.Choices) > 0 {
		content = openAIResp.Choices[0].Message.Content
	}

	finishReason := "stop"
	chunk := ChatCompletionChunk{
		ID:      openAIResp.ID,
		Object:  "chat.completion.chunk",
		Created: openAIResp.Created,
		Model:   openAIResp.Model,
		Choices: []ChunkChoice{{
			Index: 0,
			Delta: ChatMessageDelta{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: &finishReason,
		}},
	}

	writeSSEChunk(w, flusher, chunk)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// writeSSEChunk marshals a chunk to JSON and writes it as an SSE data line.
//
// Inputs:
//
//	w       - HTTP response writer
//	flusher - the flusher interface for immediate delivery
//	chunk   - the chunk to marshal and write
func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionChunk) {
	chunkJSON, err := json.Marshal(chunk)
	if err != nil {
		slog.Error("Failed to marshal SSE chunk", slog.String("error", err.Error()))
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
	flusher.Flush()
}

// recordCompletion records OTel span attributes, metrics, and logs for a
// completed agent call. Shared between streaming and non-streaming paths.
//
// Inputs:
//
//	ctx           - request context
//	span          - the OTel span to annotate
//	logger        - structured logger
//	start         - request start time
//	agentResp     - the agent's response
//	sessionReused - whether an existing session was continued
func (p *ProxyServer) recordCompletion(
	ctx context.Context,
	span trace.Span,
	logger *slog.Logger,
	start time.Time,
	agentResp *agentRunResponse,
	sessionReused bool,
) {
	span.SetAttributes(
		attribute.Bool("proxy.session_reused", sessionReused),
		attribute.String("proxy.agent_state", agentResp.State),
		attribute.Int("proxy.steps_taken", agentResp.StepsTaken),
	)

	duration := time.Since(start).Seconds()
	if p.requestCounter != nil {
		p.requestCounter.Add(ctx, 1,
			metric.WithAttributes(
				attribute.String("state", agentResp.State),
				attribute.Bool("session_reused", sessionReused),
			),
		)
	}
	if p.requestHistogram != nil {
		p.requestHistogram.Record(ctx, duration,
			metric.WithAttributes(attribute.String("state", agentResp.State)),
		)
	}

	if agentResp.State == "ERROR" {
		span.SetStatus(codes.Error, agentResp.Error)
	} else {
		telemetry.SetSpanOK(span)
	}

	logger.Info("Proxy request completed",
		slog.String("state", agentResp.State),
		slog.Int("steps", agentResp.StepsTaken),
		slog.Float64("duration_s", duration),
	)
}

// CleanupExpiredSessions removes sessions that haven't been used within the
// TTL period.
//
// Description:
//
//	Called periodically to prevent memory leaks from abandoned conversations.
//	Sessions older than 1 hour are removed.
//
// Thread Safety: Safe for concurrent use.
func (p *ProxyServer) CleanupExpiredSessions() {
	now := time.Now()
	p.sessions.Range(func(key, value any) bool {
		entry := value.(*sessionEntry)
		if now.Sub(entry.lastUsed) >= time.Hour {
			p.sessions.Delete(key)
		}
		return true
	})
}

// initRequest is the concrete request body for POST /init.
// Mirrors services/trace.InitRequest fields to avoid silent data loss during
// decode-translate-re-encode. Using a concrete type instead of map[string]any
// per project standards.
//
// Thread Safety: Not safe for concurrent use.
type initRequest struct {
	// ProjectRoot is the absolute path to the project root directory.
	ProjectRoot string `json:"project_root"`

	// Languages is the list of languages to parse. Default: ["go"].
	Languages []string `json:"languages,omitempty"`

	// ExcludePatterns is a list of glob patterns to exclude.
	ExcludePatterns []string `json:"exclude_patterns,omitempty"`
}

// translatePath rewrites a host filesystem path to its container-side equivalent.
//
// Description:
//
//	When HostPrefix and ContainerPrefix are both configured, any path starting
//	with HostPrefix has that prefix replaced with ContainerPrefix. This allows
//	the proxy running on the host to send container-valid paths to the trace
//	server running inside a container with a volume mount.
//
// Inputs:
//
//	hostPath - the original path (typically from the host filesystem)
//
// Outputs:
//
//	string - the translated path, or hostPath unchanged if no translation applies
//
// Thread Safety: Safe for concurrent use (reads immutable config).
func (p *ProxyServer) translatePath(hostPath string) string {
	if p.config.HostPrefix == "" || p.config.ContainerPrefix == "" {
		return hostPath
	}
	if !strings.HasPrefix(hostPath, p.config.HostPrefix) {
		return hostPath
	}
	// Ensure the prefix match is on a directory boundary to prevent
	// false matches: "/foo/bar" must not match "/foo/barbaz".
	remainder := hostPath[len(p.config.HostPrefix):]
	if remainder != "" && remainder[0] != '/' {
		return hostPath
	}
	return p.config.ContainerPrefix + remainder
}

// =============================================================================
// Helper Functions
// =============================================================================

// extractLastUserMessage returns the content of the last "user" role message.
//
// Inputs:
//
//	messages - the conversation message history
//
// Outputs:
//
//	string - the last user message content, or empty if none found
func extractLastUserMessage(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}

// computeThreadKey generates a stable hash from the first user message in the
// conversation. This serves as the key for session correlation — the same
// conversation always starts with the same first message.
//
// Inputs:
//
//	messages - the conversation message history
//
// Outputs:
//
//	string - hex-encoded SHA-256 hash of the first user message, or "no-user-msg"
func computeThreadKey(messages []ChatMessage) string {
	for _, m := range messages {
		if m.Role == "user" {
			h := sha256.Sum256([]byte(m.Content))
			return hex.EncodeToString(h[:16]) // 128-bit prefix is sufficient
		}
	}
	return "no-user-msg"
}

// writeError writes a JSON error response in OpenAI error format.
//
// Inputs:
//
//	w      - HTTP response writer
//	status - HTTP status code
//	msg    - error message
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := openAIErrorResponse{
		Error: openAIErrorDetail{
			Message: msg,
			Type:    "proxy_error",
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("Failed to write error response", slog.String("error", err.Error()))
	}
}

// startSessionCleanup starts a background goroutine that periodically cleans
// up expired sessions.
//
// Description:
//
//	Runs every 10 minutes and removes sessions older than 1 hour. Stops when
//	the context is cancelled.
//
// Inputs:
//
//	ctx - context for cancellation
//	p   - the proxy server whose sessions to clean
func startSessionCleanup(ctx context.Context, p *ProxyServer) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.CleanupExpiredSessions()
		}
	}
}

// corsMiddleware adds CORS headers for browser-based clients.
//
// Description:
//
//	Open WebUI and similar browser-based clients need CORS headers to
//	communicate with the proxy.
//
// Inputs:
//
//	next - the next handler in the chain
//
// Outputs:
//
//	http.Handler - the wrapped handler with CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Project-Root")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request with structured fields.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		// Don't log health checks at info level — too noisy.
		if strings.HasPrefix(r.URL.Path, "/health") {
			return
		}

		slog.Info("HTTP request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Float64("duration_s", time.Since(start).Seconds()),
		)
	})
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code before writing it.
func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
