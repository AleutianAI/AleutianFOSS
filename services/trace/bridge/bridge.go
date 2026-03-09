// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package bridge provides a reusable HTTP client wrapping all trace tool endpoints.
//
// Description:
//
//	ToolBridge translates MCP tool calls into HTTP requests against the Trace service.
//	It stores a graph_id after InitProject() and auto-injects it into all subsequent
//	CallTool() requests so MCP callers never deal with graph_id directly.
//
// Thread Safety: All exported methods are safe for concurrent use.
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	// DefaultTraceURL is the default URL for the Trace service.
	DefaultTraceURL = "http://localhost:12217"

	// DefaultTimeout is the default HTTP request timeout.
	DefaultTimeout = 30 * time.Second

	// DefaultMaxResultSize is the maximum number of characters returned in a tool result.
	DefaultMaxResultSize = 8192

	// truncationSuffix is appended when results exceed MaxResultSize.
	truncationSuffix = "\n[truncated]"
)

// ToolResult holds the result of a tool call.
//
// Description:
//
//	Contains the JSON text returned by the tool endpoint, truncated to MaxResultSize
//	if the response exceeds it. IsError is true when the upstream returned an error
//	status or the request failed.
type ToolResult struct {
	// Content is the JSON text result from the tool endpoint.
	Content string

	// IsError is true if the tool call resulted in an error.
	IsError bool
}

// Option configures a ToolBridge.
type Option func(*ToolBridge)

// WithTraceURL sets the base URL for the Trace service.
//
// Description:
//
//	Overrides the default trace URL (http://localhost:12217).
//
// Inputs:
//
//	url - the base URL including scheme and port (e.g., "http://remote:12217")
func WithTraceURL(url string) Option {
	return func(b *ToolBridge) {
		b.traceURL = url
	}
}

// WithTimeout sets the HTTP request timeout.
//
// Description:
//
//	Overrides the default 30s timeout for HTTP requests to the Trace service.
//	This sets a timeout field that is applied when the HTTP client is created
//	inside NewToolBridge, so ordering with WithHTTPClient does not matter.
//	If WithHTTPClient is also used, this option has no effect (the caller
//	is responsible for configuring their own client's timeout).
//
// Inputs:
//
//	d - timeout duration
func WithTimeout(d time.Duration) Option {
	return func(b *ToolBridge) {
		b.timeout = d
	}
}

// WithMaxResultSize sets the maximum result size in characters.
//
// Description:
//
//	Tool results exceeding this size are truncated with a "[truncated]" suffix.
//
// Inputs:
//
//	n - max number of characters in the result
func WithMaxResultSize(n int) Option {
	return func(b *ToolBridge) {
		b.maxResultSize = n
	}
}

// WithHTTPClient sets a custom HTTP client for the bridge.
//
// Description:
//
//	Allows injecting a custom *http.Client (useful for testing with httptest).
//	When set, WithTimeout has no effect — the caller controls the client's timeout.
//
// Inputs:
//
//	c - the HTTP client to use
func WithHTTPClient(c *http.Client) Option {
	return func(b *ToolBridge) {
		b.httpClient = c
		b.customClient = true
	}
}

// ToolBridge is a reusable HTTP client that translates MCP tool calls
// into HTTP requests against the Trace service.
//
// Description:
//
//	After InitProject() stores the graph_id, all subsequent CallTool() calls
//	auto-inject it. GET tools map params to query strings; POST tools merge
//	graph_id into the JSON body.
//
// Thread Safety: All methods are safe for concurrent use. A sync.RWMutex
// guards the graphID field. httpClient is inherently safe.
type ToolBridge struct {
	traceURL      string
	httpClient    *http.Client
	timeout       time.Duration
	customClient  bool
	maxResultSize int

	mu      sync.RWMutex
	graphID string
}

// NewToolBridge creates a new ToolBridge with the given options.
//
// Description:
//
//	Creates a bridge with defaults (localhost:12217, 30s timeout, 8192 max chars)
//	then applies any functional options. The HTTP client is created after all
//	options are applied so that WithTimeout and WithHTTPClient do not conflict.
//
// Inputs:
//
//	opts - functional options (WithTraceURL, WithTimeout, WithMaxResultSize, WithHTTPClient)
//
// Outputs:
//
//	*ToolBridge - the configured bridge
func NewToolBridge(opts ...Option) *ToolBridge {
	b := &ToolBridge{
		traceURL:      DefaultTraceURL,
		timeout:       DefaultTimeout,
		maxResultSize: DefaultMaxResultSize,
	}
	for _, opt := range opts {
		opt(b)
	}
	// Create the default HTTP client after options, so WithTimeout is respected.
	// If WithHTTPClient was used, httpClient is already set.
	if !b.customClient {
		b.httpClient = &http.Client{Timeout: b.timeout}
	}
	return b
}

// setGraphIDForTest sets the graph ID directly. Test-only helper.
func (b *ToolBridge) setGraphIDForTest(id string) {
	b.mu.Lock()
	b.graphID = id
	b.mu.Unlock()
}

// InitProject initializes a project graph and stores the graph_id.
//
// Description:
//
//	Calls POST /v1/trace/init with the project_root, stores the returned
//	graph_id for all subsequent tool calls. Returns the init response JSON.
//
// Inputs:
//
//	ctx - context for cancellation and timeout
//	projectRoot - absolute path to the project root directory
//
// Outputs:
//
//	*ToolResult - the init response JSON
//	error - wrapped error if the request fails
//
// Thread Safety: Safe for concurrent use; acquires write lock on graphID.
func (b *ToolBridge) InitProject(ctx context.Context, projectRoot string) (*ToolResult, error) {
	result, err := b.CallTool(ctx, "trace_init_project", map[string]any{
		"project_root": projectRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("initializing project: %w", err)
	}
	if result.IsError {
		return result, nil
	}

	// Extract graph_id from response.
	var resp struct {
		GraphID string `json:"graph_id"`
	}
	if err := json.Unmarshal([]byte(result.Content), &resp); err != nil {
		return nil, fmt.Errorf("parsing init response: %w", err)
	}
	if resp.GraphID != "" {
		b.mu.Lock()
		b.graphID = resp.GraphID
		b.mu.Unlock()
	}
	return result, nil
}

// GraphID returns the currently stored graph ID.
//
// Description:
//
//	Returns the graph_id set by InitProject(). Empty if not yet initialized.
//
// Thread Safety: Safe for concurrent use; acquires read lock.
func (b *ToolBridge) GraphID() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.graphID
}

// CallTool executes a trace tool by name with the given parameters.
//
// Description:
//
//	Looks up the tool route, constructs an HTTP request (GET with query params
//	or POST with JSON body), auto-injects graph_id, executes the request,
//	decodes the response, and truncates if necessary.
//
// Inputs:
//
//	ctx - context for cancellation and timeout
//	toolName - the MCP tool name (e.g., "trace_find_callers")
//	params - MCP parameter key-value pairs
//
// Outputs:
//
//	*ToolResult - the tool result (may be truncated)
//	error - wrapped error if the request fails or the tool is unknown
//
// Limitations:
//
//	Results exceeding MaxResultSize are truncated with "[truncated]" suffix.
//
// Thread Safety: Safe for concurrent use.
func (b *ToolBridge) CallTool(ctx context.Context, toolName string, params map[string]any) (*ToolResult, error) {
	route, ok := toolRoutes[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}

	b.mu.RLock()
	graphID := b.graphID
	b.mu.RUnlock()

	var req *http.Request
	var err error

	switch route.Method {
	case "GET":
		req, err = b.buildGETRequest(ctx, route, params, graphID)
	case "POST":
		req, err = b.buildPOSTRequest(ctx, route, toolName, params, graphID)
	default:
		return nil, fmt.Errorf("unsupported HTTP method %s for tool %s", route.Method, toolName)
	}
	if err != nil {
		return nil, fmt.Errorf("calling tool %s: %w", toolName, err)
	}

	return b.executeRequest(ctx, req, toolName, route.Method)
}

// HealthCheck calls the trace health endpoint.
//
// Description:
//
//	Calls GET /v1/trace/health and returns the response JSON.
//
// Inputs:
//
//	ctx - context for cancellation and timeout
//
// Outputs:
//
//	*ToolResult - the health response JSON
//	error - wrapped error if the request fails
//
// Thread Safety: Safe for concurrent use.
func (b *ToolBridge) HealthCheck(ctx context.Context) (*ToolResult, error) {
	return b.doGET(ctx, "/v1/trace/health", "health check")
}

// GetGraphStats calls the graph stats debug endpoint.
//
// Description:
//
//	Calls GET /v1/trace/debug/graph/stats and returns the response JSON.
//
// Inputs:
//
//	ctx - context for cancellation and timeout
//
// Outputs:
//
//	*ToolResult - the stats response JSON
//	error - wrapped error if the request fails
//
// Thread Safety: Safe for concurrent use.
func (b *ToolBridge) GetGraphStats(ctx context.Context) (*ToolResult, error) {
	return b.doGET(ctx, "/v1/trace/debug/graph/stats", "graph stats")
}

// doGET executes a simple GET request with read-limiting and truncation.
func (b *ToolBridge) doGET(ctx context.Context, path string, label string) (*ToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", b.traceURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: building request: %w", label, err)
	}
	return b.executeRequest(ctx, req, label, "GET")
}

// executeRequest sends an HTTP request and returns a ToolResult with
// read-limiting, truncation, OTel spans, and metrics recording applied.
//
// Description:
//
//	Creates an OTel span for the HTTP request, propagates trace context
//	to the downstream service, records metrics (duration, result size,
//	errors, truncations), and returns the response as a ToolResult.
//
// Inputs:
//
//	ctx - Context for span creation and metric recording.
//	req - The prepared HTTP request.
//	toolName - The MCP tool name (for span attributes and metrics).
//	method - The HTTP method (for metric labels).
//
// Outputs:
//
//	*ToolResult - The tool result (may be truncated).
//	error - Wrapped error if response reading fails.
//
// Thread Safety: Safe for concurrent use.
func (b *ToolBridge) executeRequest(ctx context.Context, req *http.Request, toolName string, method string) (*ToolResult, error) {
	ctx, span := startBridgeSpan(ctx, toolName, method, req.URL.String())
	defer span.End()

	// Propagate trace context to the trace server so spans link in Jaeger.
	req = telemetry.PropagateToRequest(ctx, req)

	start := time.Now()
	resp, err := b.httpClient.Do(req)
	durationSeconds := time.Since(start).Seconds()

	if err != nil {
		telemetry.RecordError(span, err)
		recordToolCall(ctx, toolName, "error")
		recordToolError(ctx, toolName, "connection")
		recordToolDuration(ctx, toolName, method, durationSeconds)
		return &ToolResult{
			Content: fmt.Sprintf("Trace server not reachable at %s. Start with: aleutian stack start", b.traceURL),
			IsError: true,
		}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	// Cap the read to 2x maxResultSize to avoid unbounded memory allocation
	// from a misbehaving server, while still allowing truncation to produce
	// a meaningful prefix.
	maxRead := int64(b.maxResultSize) * 2
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRead))
	if err != nil {
		telemetry.RecordError(span, err)
		recordToolCall(ctx, toolName, "error")
		recordToolError(ctx, toolName, "read")
		recordToolDuration(ctx, toolName, method, durationSeconds)
		return nil, fmt.Errorf("calling tool %s: reading response: %w", toolName, err)
	}

	resultSize := int64(len(body))
	span.SetAttributes(attribute.Int64("mcp.result_size", resultSize))
	recordToolResultSize(ctx, toolName, resultSize)

	content := string(body)
	isError := resp.StatusCode >= 400

	truncated := len(content) > b.maxResultSize
	content = b.truncate(content)
	span.SetAttributes(attribute.Bool("mcp.truncated", truncated))

	if truncated {
		telemetry.AddSpanEvent(span, "result.truncated",
			attribute.Int("mcp.original_size", int(resultSize)),
			attribute.Int("mcp.max_size", b.maxResultSize),
		)
		recordToolTruncation(ctx, toolName)
	}

	if isError {
		errorType := "5xx"
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			errorType = "4xx"
		}
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", resp.StatusCode))
		recordToolCall(ctx, toolName, "error")
		recordToolError(ctx, toolName, errorType)
	} else {
		telemetry.SetSpanOK(span)
		recordToolCall(ctx, toolName, "ok")
	}

	recordToolDuration(ctx, toolName, method, durationSeconds)

	return &ToolResult{
		Content: content,
		IsError: isError,
	}, nil
}

// truncate shortens content to maxResultSize if it exceeds the limit.
// If maxResultSize is too small to include the suffix, content is truncated
// to maxResultSize without the suffix to avoid a panic.
func (b *ToolBridge) truncate(content string) string {
	if len(content) <= b.maxResultSize {
		return content
	}
	suffixLen := len(truncationSuffix)
	if b.maxResultSize <= suffixLen {
		return content[:b.maxResultSize]
	}
	return content[:b.maxResultSize-suffixLen] + truncationSuffix
}

// buildGETRequest constructs a GET request with query parameters.
func (b *ToolBridge) buildGETRequest(ctx context.Context, route toolRoute, params map[string]any, graphID string) (*http.Request, error) {
	reqURL, err := url.Parse(b.traceURL + route.Path)
	if err != nil {
		return nil, fmt.Errorf("parsing URL: %w", err)
	}

	q := reqURL.Query()
	if graphID != "" {
		q.Set("graph_id", graphID)
	}
	for mcpName, httpName := range route.ParamMap {
		if v, ok := params[mcpName]; ok {
			q.Set(httpName, fmt.Sprintf("%v", v))
		}
	}
	reqURL.RawQuery = q.Encode()

	return http.NewRequestWithContext(ctx, "GET", reqURL.String(), nil)
}

// buildPOSTRequest constructs a POST request with a JSON body.
func (b *ToolBridge) buildPOSTRequest(ctx context.Context, route toolRoute, toolName string, params map[string]any, graphID string) (*http.Request, error) {
	body := make(map[string]any)

	// For init, don't inject graph_id (we're creating one).
	if toolName != "trace_init_project" && graphID != "" {
		body["graph_id"] = graphID
	}

	// Map MCP params to body fields.
	for mcpName, httpName := range route.ParamMap {
		if v, ok := params[mcpName]; ok {
			body[httpName] = v
		}
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.traceURL+route.Path, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
