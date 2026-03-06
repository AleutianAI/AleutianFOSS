// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestServer creates an httptest.Server that routes requests to
// configurable handlers per path.
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, handler := range handlers {
		mux.HandleFunc(path, handler)
	}
	return httptest.NewServer(mux)
}

func TestNewToolBridge_Defaults(t *testing.T) {
	b := NewToolBridge()
	if b.traceURL != DefaultTraceURL {
		t.Errorf("expected traceURL=%s, got %s", DefaultTraceURL, b.traceURL)
	}
	if b.maxResultSize != DefaultMaxResultSize {
		t.Errorf("expected maxResultSize=%d, got %d", DefaultMaxResultSize, b.maxResultSize)
	}
}

func TestNewToolBridge_Options(t *testing.T) {
	b := NewToolBridge(
		WithTraceURL("http://custom:9999"),
		WithTimeout(5*time.Second),
		WithMaxResultSize(1024),
	)
	if b.traceURL != "http://custom:9999" {
		t.Errorf("expected custom URL, got %s", b.traceURL)
	}
	if b.maxResultSize != 1024 {
		t.Errorf("expected maxResultSize=1024, got %d", b.maxResultSize)
	}
}

func TestCallTool_GETParamEncoding(t *testing.T) {
	var gotQuery string
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"callers":[]}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	// Set a graph_id to verify it gets injected.
	b.setGraphIDForTest("test-graph-123")

	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "MyFunc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	// Verify query string contains mapped params.
	if !strings.Contains(gotQuery, "function=MyFunc") {
		t.Errorf("expected function=MyFunc in query, got: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "graph_id=test-graph-123") {
		t.Errorf("expected graph_id in query, got: %s", gotQuery)
	}
}

func TestCallTool_POSTBodyEncoding(t *testing.T) {
	var gotBody map[string]any
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/analytics/hotspots": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hotspots":[]}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	b.setGraphIDForTest("graph-456")

	result, err := b.CallTool(context.Background(), "trace_find_hotspots", map[string]any{
		"limit": 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	if gotBody["graph_id"] != "graph-456" {
		t.Errorf("expected graph_id=graph-456, got %v", gotBody["graph_id"])
	}
	// limit is mapped to "limit" in the body.
	if gotBody["limit"] == nil {
		t.Error("expected limit in body")
	}
}

func TestCallTool_POSTNoGraphIDForInit(t *testing.T) {
	var gotBody map[string]any
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/init": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"graph_id":"new-graph"}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	b.setGraphIDForTest("old-graph")

	_, err := b.CallTool(context.Background(), "trace_init_project", map[string]any{
		"project_root": "/tmp/project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Init should NOT inject graph_id.
	if _, ok := gotBody["graph_id"]; ok {
		t.Error("init should not include graph_id in body")
	}
	if gotBody["project_root"] != "/tmp/project" {
		t.Errorf("expected project_root=/tmp/project, got %v", gotBody["project_root"])
	}
}

func TestInitProject_StoresGraphID(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/init": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"graph_id":"abc-123","files_parsed":10,"symbols_extracted":50}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	result, err := b.InitProject(context.Background(), "/tmp/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	if b.GraphID() != "abc-123" {
		t.Errorf("expected graphID=abc-123, got %s", b.GraphID())
	}
}

func TestCallTool_ResultTruncation(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			// Return a response larger than 100 chars.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", 200)))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL), WithMaxResultSize(100))
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Content) > 100 {
		t.Errorf("expected truncated result <= 100 chars, got %d", len(result.Content))
	}
	if !strings.HasSuffix(result.Content, "[truncated]") {
		t.Error("expected [truncated] suffix")
	}
}

func TestCallTool_ResultTruncation_TinyMaxSize(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", 200)))
		},
	})
	defer ts.Close()

	// MaxResultSize smaller than the truncation suffix — must not panic.
	b := NewToolBridge(WithTraceURL(ts.URL), WithMaxResultSize(5))
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) > 5 {
		t.Errorf("expected result <= 5 chars, got %d", len(result.Content))
	}
}

func TestCallTool_UnknownTool(t *testing.T) {
	b := NewToolBridge()
	_, err := b.CallTool(context.Background(), "nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected 'unknown tool' in error, got: %v", err)
	}
}

func TestCallTool_4xxError(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"missing parameter"}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for 400 status")
	}
}

func TestCallTool_5xxError(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for 500 status")
	}
}

func TestCallTool_ConnectionRefused(t *testing.T) {
	b := NewToolBridge(WithTraceURL("http://localhost:1")) // Port 1 = unreachable
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("connection refused should not return Go error, got: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for connection refused")
	}
	if !strings.Contains(result.Content, "not reachable") {
		t.Errorf("expected 'not reachable' message, got: %s", result.Content)
	}
}

func TestCallTool_ConcurrentAccess(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"callers":[]}`))
		},
		"/v1/trace/callees": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"callees":[]}`))
		},
		"/v1/trace/analytics/hotspots": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"hotspots":[]}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	b.setGraphIDForTest("concurrent-graph")

	var wg sync.WaitGroup
	errCh := make(chan error, 30)

	tools := []struct {
		name   string
		params map[string]any
	}{
		{"trace_find_callers", map[string]any{"function_name": "Foo"}},
		{"trace_find_callees", map[string]any{"function_name": "Bar"}},
		{"trace_find_hotspots", map[string]any{"limit": 5}},
	}

	for i := 0; i < 10; i++ {
		for _, tool := range tools {
			wg.Add(1)
			go func(name string, params map[string]any) {
				defer wg.Done()
				_, err := b.CallTool(context.Background(), name, params)
				if err != nil {
					errCh <- err
				}
			}(tool.name, tool.params)
		}
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent call error: %v", err)
	}
}

func TestHealthCheck_Success(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/health": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy","version":"1.0.0"}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	result, err := b.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "healthy") {
		t.Errorf("expected healthy in response, got: %s", result.Content)
	}
}

func TestHealthCheck_Unreachable(t *testing.T) {
	b := NewToolBridge(WithTraceURL("http://localhost:1"))
	result, err := b.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("unreachable should not return Go error, got: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for unreachable")
	}
}

func TestGetGraphStats(t *testing.T) {
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/debug/graph/stats": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"total_graphs":1,"total_nodes":100}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	result, err := b.GetGraphStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "total_graphs") {
		t.Errorf("expected stats in response, got: %s", result.Content)
	}
}

func TestCallTool_AllGETTools(t *testing.T) {
	t.Run("trace_find_callees", func(t *testing.T) {
		var gotQuery string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/v1/trace/callees": func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		})
		defer ts.Close()

		b := NewToolBridge(WithTraceURL(ts.URL))
		_, err := b.CallTool(context.Background(), "trace_find_callees", map[string]any{
			"function_name": "Handler",
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(gotQuery, "function=Handler") {
			t.Errorf("expected function=Handler, got: %s", gotQuery)
		}
	})

	t.Run("trace_find_implementations", func(t *testing.T) {
		var gotQuery string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/v1/trace/implementations": func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		})
		defer ts.Close()

		b := NewToolBridge(WithTraceURL(ts.URL))
		_, err := b.CallTool(context.Background(), "trace_find_implementations", map[string]any{
			"interface_name": "Reader",
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(gotQuery, "interface=Reader") {
			t.Errorf("expected interface=Reader, got: %s", gotQuery)
		}
	})

	t.Run("trace_find_symbol", func(t *testing.T) {
		var gotQuery string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/v1/trace/debug/graph/inspect": func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		})
		defer ts.Close()

		b := NewToolBridge(WithTraceURL(ts.URL))
		_, err := b.CallTool(context.Background(), "trace_find_symbol", map[string]any{
			"name": "MyStruct",
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(gotQuery, "name=MyStruct") {
			t.Errorf("expected name=MyStruct, got: %s", gotQuery)
		}
	})

	t.Run("trace_get_call_chain", func(t *testing.T) {
		var gotQuery string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/v1/trace/call-chain": func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		})
		defer ts.Close()

		b := NewToolBridge(WithTraceURL(ts.URL))
		_, err := b.CallTool(context.Background(), "trace_get_call_chain", map[string]any{
			"from": "main", "to": "handler",
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(gotQuery, "from=main") || !strings.Contains(gotQuery, "to=handler") {
			t.Errorf("expected from=main&to=handler, got: %s", gotQuery)
		}
	})

	t.Run("trace_find_references", func(t *testing.T) {
		var gotQuery string
		ts := newTestServer(t, map[string]http.HandlerFunc{
			"/v1/trace/references": func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			},
		})
		defer ts.Close()

		b := NewToolBridge(WithTraceURL(ts.URL))
		_, err := b.CallTool(context.Background(), "trace_find_references", map[string]any{
			"symbol_name": "Config",
		})
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if !strings.Contains(gotQuery, "symbol=Config") {
			t.Errorf("expected symbol=Config, got: %s", gotQuery)
		}
	})
}

// setupTestTracer installs a test tracer provider and returns the span recorder.
// The previous provider is restored when the test completes.
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
	tracer = otel.Tracer("aleutian.mcp.bridge")
	return recorder
}

func TestExecuteRequest_CreatesSpan(t *testing.T) {
	recorder := setupTestTracer(t)

	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"callers":[]}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	_, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one span to be recorded")
	}

	span := spans[0]
	if span.Name() != "bridge.CallTool" {
		t.Errorf("expected span name 'bridge.CallTool', got %q", span.Name())
	}

	// Verify span attributes.
	attrs := make(map[string]string)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}

	if attrs["mcp.tool"] != "trace_find_callers" {
		t.Errorf("expected mcp.tool=trace_find_callers, got %q", attrs["mcp.tool"])
	}
	if attrs["http.method"] != "GET" {
		t.Errorf("expected http.method=GET, got %q", attrs["http.method"])
	}
	if attrs["http.status_code"] != "200" {
		t.Errorf("expected http.status_code=200, got %q", attrs["http.status_code"])
	}
	if attrs["mcp.truncated"] != "false" {
		t.Errorf("expected mcp.truncated=false, got %q", attrs["mcp.truncated"])
	}
}

func TestExecuteRequest_SpanRecordsErrorOnConnectionFailure(t *testing.T) {
	recorder := setupTestTracer(t)

	b := NewToolBridge(WithTraceURL("http://localhost:1"))
	result, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("expected no Go error, got: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for connection failure")
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	span := spans[0]
	if span.Status().Code != codes.Error {
		t.Errorf("expected span status Error, got %v", span.Status().Code)
	}

	// Verify error event was recorded.
	foundError := false
	for _, event := range span.Events() {
		if event.Name == "exception" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("expected error event on span")
	}
}

func TestExecuteRequest_SpanRecordsTruncation(t *testing.T) {
	recorder := setupTestTracer(t)

	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", 200)))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL), WithMaxResultSize(100))
	_, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	span := spans[0]
	// Verify truncated attribute.
	attrs := make(map[string]string)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value.Emit()
	}
	if attrs["mcp.truncated"] != "true" {
		t.Errorf("expected mcp.truncated=true, got %q", attrs["mcp.truncated"])
	}

	// Verify truncation event.
	foundTruncation := false
	for _, event := range span.Events() {
		if event.Name == "result.truncated" {
			foundTruncation = true
			break
		}
	}
	if !foundTruncation {
		t.Error("expected result.truncated event on span")
	}
}

func TestExecuteRequest_SpanRecords4xxError(t *testing.T) {
	recorder := setupTestTracer(t)

	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		},
	})
	defer ts.Close()

	b := NewToolBridge(WithTraceURL(ts.URL))
	_, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("expected at least one span")
	}

	span := spans[0]
	if span.Status().Code != codes.Error {
		t.Errorf("expected span status Error for 400, got %v", span.Status().Code)
	}
	if !strings.Contains(span.Status().Description, "HTTP 400") {
		t.Errorf("expected 'HTTP 400' in status description, got %q", span.Status().Description)
	}
}

func TestExecuteRequest_PropagatesTraceContext(t *testing.T) {
	var gotTraceparent string
	ts := newTestServer(t, map[string]http.HandlerFunc{
		"/v1/trace/callers": func(w http.ResponseWriter, r *http.Request) {
			gotTraceparent = r.Header.Get("Traceparent")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		},
	})
	defer ts.Close()

	recorder := setupTestTracer(t)
	_ = recorder // We just need the provider installed.

	b := NewToolBridge(WithTraceURL(ts.URL))
	_, err := b.CallTool(context.Background(), "trace_find_callers", map[string]any{
		"function_name": "Foo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The W3C traceparent header should have been propagated.
	if gotTraceparent == "" {
		t.Error("expected Traceparent header to be propagated to trace server")
	}
	// W3C traceparent format: version-trace_id-parent_id-flags
	parts := strings.Split(gotTraceparent, "-")
	if len(parts) != 4 {
		t.Errorf("expected 4-part traceparent, got %q", gotTraceparent)
	}
}

func TestCallTool_AllPOSTTools(t *testing.T) {
	postTools := []struct {
		name     string
		path     string
		params   map[string]any
		checkKey string
	}{
		{"trace_find_dead_code", "/v1/trace/patterns/dead_code", nil, "graph_id"},
		{"trace_find_cycles", "/v1/trace/analytics/cycles", nil, "graph_id"},
		{"trace_find_communities", "/v1/trace/analytics/communities", nil, "graph_id"},
		{"trace_find_important", "/v1/trace/analytics/important", map[string]any{"limit": 3}, "limit"},
		{"trace_find_path", "/v1/trace/analytics/path", map[string]any{"from": "A", "to": "B"}, "from"},
	}

	for _, tc := range postTools {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			ts := newTestServer(t, map[string]http.HandlerFunc{
				tc.path: func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(body, &gotBody)
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				},
			})
			defer ts.Close()

			b := NewToolBridge(WithTraceURL(ts.URL))
			b.setGraphIDForTest("test-graph")

			_, err := b.CallTool(context.Background(), tc.name, tc.params)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if gotBody[tc.checkKey] == nil {
				t.Errorf("expected %s in body, got: %v", tc.checkKey, gotBody)
			}
		})
	}
}
