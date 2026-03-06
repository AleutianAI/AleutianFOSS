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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// mockTraceServer creates a test server that handles all trace endpoints.
func mockTraceServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/trace/init", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		resp := map[string]any{
			"graph_id":          "test-graph-001",
			"files_parsed":      10,
			"symbols_extracted": 50,
			"edges_built":       30,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/callers", func(w http.ResponseWriter, r *http.Request) {
		fn := r.URL.Query().Get("function")
		resp := map[string]any{
			"function": fn,
			"callers":  []string{"main", "handler"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/callees", func(w http.ResponseWriter, r *http.Request) {
		fn := r.URL.Query().Get("function")
		resp := map[string]any{"function": fn, "callees": []string{"helper"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/implementations", func(w http.ResponseWriter, r *http.Request) {
		iface := r.URL.Query().Get("interface")
		resp := map[string]any{"interface": iface, "implementations": []string{"ConcreteType"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/debug/graph/inspect", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		resp := map[string]any{"name": name, "kind": "function"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/call-chain", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"from":  r.URL.Query().Get("from"),
			"to":    r.URL.Query().Get("to"),
			"chain": []string{"main", "handler", "helper"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/references", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"symbol":     r.URL.Query().Get("symbol"),
			"references": []map[string]any{{"file": "main.go", "line": 10}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/analytics/hotspots", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"hotspots": []map[string]any{{"name": "main", "score": 10}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/patterns/dead_code", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"dead_code": []string{"unusedFunc"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/analytics/cycles", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"cycles": [][]string{{"a", "b", "a"}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/analytics/important", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"important": []map[string]any{{"name": "main", "rank": 0.5}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/analytics/communities", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"communities": []map[string]any{{"id": 0, "members": []string{"a", "b"}}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/v1/trace/analytics/path", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		resp := map[string]any{"path": []string{"a", "b", "c"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	return httptest.NewServer(mux)
}

// setupMCPTest creates a connected client/server pair for testing.
func setupMCPTest(t *testing.T, b *bridge.ToolBridge) (*mcp.ClientSession, func()) {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	registerTraceTools(server, b)
	registerResources(server, b)

	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		_ = cs.Close()
		_ = ss.Close()
	}
	return cs, cleanup
}

func TestRegisterTraceTools_AllToolsRegistered(t *testing.T) {
	ts := mockTraceServer(t)
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	toolsResult, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	expectedTools := []string{
		"trace_init_project",
		"trace_find_callers",
		"trace_find_callees",
		"trace_find_implementations",
		"trace_find_symbol",
		"trace_get_call_chain",
		"trace_find_references",
		"trace_find_hotspots",
		"trace_find_dead_code",
		"trace_find_cycles",
		"trace_find_important",
		"trace_find_communities",
		"trace_find_path",
	}

	registeredTools := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		registeredTools[tool.Name] = true
	}

	for _, expected := range expectedTools {
		if !registeredTools[expected] {
			t.Errorf("expected tool %s to be registered", expected)
		}
	}

	if len(toolsResult.Tools) != len(expectedTools) {
		t.Errorf("expected %d tools, got %d", len(expectedTools), len(toolsResult.Tools))
	}
}

func TestToolExecution_InitProject(t *testing.T) {
	ts := mockTraceServer(t)
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_init_project",
		Arguments: map[string]any{"project_root": "/tmp/test-project"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["graph_id"] != "test-graph-001" {
		t.Errorf("expected graph_id=test-graph-001, got %v", resp["graph_id"])
	}

	// Verify graph_id was stored in bridge.
	if b.GraphID() != "test-graph-001" {
		t.Errorf("expected bridge graphID=test-graph-001, got %s", b.GraphID())
	}
}

func TestToolExecution_FindCallers(t *testing.T) {
	ts := mockTraceServer(t)
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_find_callers",
		Arguments: map[string]any{"function_name": "HandleInit"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["function"] != "HandleInit" {
		t.Errorf("expected function=HandleInit, got %v", resp["function"])
	}
}

func TestToolExecution_ServerUnreachable(t *testing.T) {
	b := bridge.NewToolBridge(bridge.WithTraceURL("http://localhost:1"))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_find_callers",
		Arguments: map[string]any{"function_name": "Foo"},
	})
	if err != nil {
		t.Fatalf("call tool should not return Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for unreachable server")
	}
}

// setupTestTracer installs a test tracer provider and returns the span recorder.
func setupTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
		_ = tp.Shutdown(context.Background())
	})
	// Reset the package-level tracer so it uses the new provider.
	mcpTracer = otel.Tracer("aleutian.mcp.server")
	return recorder
}

func TestToolHandler_CreatesSpan(t *testing.T) {
	recorder := setupTestTracer(t)

	ts := mockTraceServer(t)
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_find_callers",
		Arguments: map[string]any{"function_name": "HandleInit"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success")
	}

	spans := recorder.Ended()
	// We expect at least the MCP handler span and the bridge span.
	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans, got %d", len(spans))
	}

	// Find the MCP handler span.
	var mcpSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if strings.HasPrefix(s.Name(), "mcp.tool.") {
			mcpSpan = s
			break
		}
	}
	if mcpSpan == nil {
		t.Fatal("expected an mcp.tool.* span")
	}
	if mcpSpan.Name() != "mcp.tool.trace_find_callers" {
		t.Errorf("expected span name mcp.tool.trace_find_callers, got %q", mcpSpan.Name())
	}

	// Verify tool name attribute.
	attrs := make(map[string]string)
	for _, attr := range mcpSpan.Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	if attrs["mcp.tool"] != "trace_find_callers" {
		t.Errorf("expected mcp.tool=trace_find_callers, got %q", attrs["mcp.tool"])
	}
	if attrs["mcp.param.function_name"] != "HandleInit" {
		t.Errorf("expected mcp.param.function_name=HandleInit, got %q", attrs["mcp.param.function_name"])
	}
}

func TestToolHandler_MultipleToolsCreateDistinctSpans(t *testing.T) {
	recorder := setupTestTracer(t)

	ts := mockTraceServer(t)
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()

	// Call two different tools.
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_find_callers",
		Arguments: map[string]any{"function_name": "A"},
	})
	if err != nil {
		t.Fatalf("call callers: %v", err)
	}

	_, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "trace_find_callees",
		Arguments: map[string]any{"function_name": "B"},
	})
	if err != nil {
		t.Fatalf("call callees: %v", err)
	}

	spans := recorder.Ended()

	// Collect MCP span names.
	var mcpSpanNames []string
	for _, s := range spans {
		if strings.HasPrefix(s.Name(), "mcp.tool.") {
			mcpSpanNames = append(mcpSpanNames, s.Name())
		}
	}

	if len(mcpSpanNames) != 2 {
		t.Fatalf("expected 2 MCP spans, got %d: %v", len(mcpSpanNames), mcpSpanNames)
	}

	// Verify distinct span names.
	foundCallers := false
	foundCallees := false
	for _, name := range mcpSpanNames {
		if name == "mcp.tool.trace_find_callers" {
			foundCallers = true
		}
		if name == "mcp.tool.trace_find_callees" {
			foundCallees = true
		}
	}
	if !foundCallers || !foundCallees {
		t.Errorf("expected spans for both callers and callees, got: %v", mcpSpanNames)
	}
}

func TestResolveTraceURL(t *testing.T) {
	t.Run("flag takes precedence", func(t *testing.T) {
		url := resolveTraceURL("http://flag:9999")
		if url != "http://flag:9999" {
			t.Errorf("expected flag URL, got %s", url)
		}
	})

	t.Run("env var fallback", func(t *testing.T) {
		t.Setenv("ALEUTIAN_TRACE_URL", "http://env:8888")
		url := resolveTraceURL("")
		if url != "http://env:8888" {
			t.Errorf("expected env URL, got %s", url)
		}
	})

	t.Run("default fallback", func(t *testing.T) {
		t.Setenv("ALEUTIAN_TRACE_URL", "")
		url := resolveTraceURL("")
		if url != "http://localhost:12217" {
			t.Errorf("expected default URL, got %s", url)
		}
	})
}
