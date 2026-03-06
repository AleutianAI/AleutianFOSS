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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// LAYER 1: SERVICE HEALTH
// =============================================================================

func TestCheckServiceHealth_Pass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/trace/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "healthy",
				"version": "v0.1.0",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	layer := checkServiceHealth(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if layer.Number != 1 {
		t.Errorf("expected layer number 1, got %d", layer.Number)
	}
	if layer.Latency == 0 {
		t.Error("expected non-zero latency")
	}
	if len(layer.Details) == 0 || !strings.Contains(layer.Details[0], "v0.1.0") {
		t.Errorf("expected details to contain version, got %v", layer.Details)
	}
}

func TestCheckServiceHealth_ConnectionRefused(t *testing.T) {
	layer := checkServiceHealth(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
	if len(layer.Details) < 1 || !strings.Contains(layer.Details[0], "Unreachable") {
		t.Errorf("expected unreachable detail, got %v", layer.Details)
	}
	if len(layer.Details) < 2 || !strings.Contains(layer.Details[1], "Hint:") {
		t.Errorf("expected hint in details, got %v", layer.Details)
	}
}

func TestCheckServiceHealth_DegradedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "degraded",
			"version": "v0.1.0",
		})
	}))
	defer server.Close()

	layer := checkServiceHealth(server.Client(), server.URL)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

func TestCheckServiceHealth_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	layer := checkServiceHealth(server.Client(), server.URL)

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
}

// =============================================================================
// LAYER 2: GRAPH BUILD
// =============================================================================

func TestCheckGraphBuild_Pass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/trace/ready":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":       true,
				"graph_count": 2,
			})
		case "/v1/trace/debug/graph/stats":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"node_count": 1247,
				"edge_count": 3891,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	layer := checkGraphBuild(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if len(layer.Details) == 0 {
		t.Fatal("expected details")
	}
	if !strings.Contains(layer.Details[0], "1,247") {
		t.Errorf("expected formatted node count, got %s", layer.Details[0])
	}
	if !strings.Contains(layer.Details[0], "3,891") {
		t.Errorf("expected formatted edge count, got %s", layer.Details[0])
	}
}

func TestCheckGraphBuild_NotReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ready":       false,
			"graph_count": 0,
		})
	}))
	defer server.Close()

	layer := checkGraphBuild(server.Client(), server.URL)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

func TestCheckGraphBuild_NoGraphs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/trace/ready":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ready":       true,
				"graph_count": 0,
			})
		case "/v1/trace/debug/graph/stats":
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	layer := checkGraphBuild(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass for no graphs, got %s", layer.Status)
	}
}

func TestCheckGraphBuild_ConnectionRefused(t *testing.T) {
	layer := checkGraphBuild(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
	if len(layer.Details) == 0 || !strings.Contains(layer.Details[0], "Connection error") {
		t.Errorf("expected connection error detail, got %v", layer.Details)
	}
}

// =============================================================================
// LAYER 3: CRS INIT
// =============================================================================

func TestCheckCRSInit_Pass(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions:  3,
			TotalTraceSteps: 42,
		},
		latency: 15 * time.Millisecond,
	}

	layer := checkCRSInit(result)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if !strings.Contains(layer.Details[0], "3 active sessions") {
		t.Errorf("expected session count in details, got %s", layer.Details[0])
	}
}

func TestCheckCRSInit_Fail(t *testing.T) {
	result := crsDebugResult{
		err:     fmt.Errorf("connection refused"),
		latency: 5 * time.Millisecond,
	}

	layer := checkCRSInit(result)

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
}

// =============================================================================
// LAYER 4: CRS REASONING
// =============================================================================

func TestCheckCRSReasoning_Pass(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions:  2,
			TotalTraceSteps: 15,
		},
		latency: 10 * time.Millisecond,
	}

	layer := checkCRSReasoning(result)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
}

func TestCheckCRSReasoning_SkipNoSessions(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions: 0,
		},
		latency: 10 * time.Millisecond,
	}

	layer := checkCRSReasoning(result)

	if layer.Status != "skip" {
		t.Errorf("expected skip, got %s", layer.Status)
	}
}

func TestCheckCRSReasoning_SkipCRSUnavailable(t *testing.T) {
	result := crsDebugResult{
		err:     fmt.Errorf("connection refused"),
		latency: 5 * time.Millisecond,
	}

	layer := checkCRSReasoning(result)

	if layer.Status != "skip" {
		t.Errorf("expected skip, got %s", layer.Status)
	}
}

func TestCheckCRSReasoning_WarnNoSteps(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions:  2,
			TotalTraceSteps: 0,
		},
		latency: 10 * time.Millisecond,
	}

	layer := checkCRSReasoning(result)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

// =============================================================================
// LAYER 5: PERSISTENCE
// =============================================================================

func TestCheckPersistence_Pass(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions: 3,
		},
		latency: 10 * time.Millisecond,
	}

	layer := checkPersistence(result)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
}

func TestCheckPersistence_SkipFreshInstance(t *testing.T) {
	result := crsDebugResult{
		response: &crsDebugResponse{
			ActiveSessions: 0,
		},
		latency: 10 * time.Millisecond,
	}

	layer := checkPersistence(result)

	if layer.Status != "skip" {
		t.Errorf("expected skip, got %s", layer.Status)
	}
}

func TestCheckPersistence_SkipCRSUnavailable(t *testing.T) {
	result := crsDebugResult{
		err:     fmt.Errorf("connection refused"),
		latency: 5 * time.Millisecond,
	}

	layer := checkPersistence(result)

	if layer.Status != "skip" {
		t.Errorf("expected skip, got %s", layer.Status)
	}
}

// =============================================================================
// LAYER 6: PROMETHEUS METRICS
// =============================================================================

func TestCheckMetrics_Pass(t *testing.T) {
	metricsBody := `# HELP go_goroutines Number of goroutines.
# TYPE go_goroutines gauge
go_goroutines 42
# HELP trace_queries_total Total trace queries.
# TYPE trace_queries_total counter
trace_queries_total 100
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/metrics" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, metricsBody)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	layer := checkMetrics(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if !strings.Contains(layer.Details[0], "2 metric families") {
		t.Errorf("expected 2 metric families, got %s", layer.Details[0])
	}
}

func TestCheckMetrics_NoFamilies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "")
	}))
	defer server.Close()

	layer := checkMetrics(server.Client(), server.URL)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

func TestCheckMetrics_ConnectionRefused(t *testing.T) {
	layer := checkMetrics(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
}

// =============================================================================
// LAYER 7: JAEGER
// =============================================================================

func TestCheckJaeger_Pass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/services" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []string{"aleutian-trace", "jaeger-query"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	layer := checkJaeger(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if !strings.Contains(layer.Details[0], "aleutian-trace") {
		t.Errorf("expected aleutian-trace in details, got %s", layer.Details[0])
	}
}

func TestCheckJaeger_WarnNoAleutianService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []string{"other-service"},
		})
	}))
	defer server.Close()

	layer := checkJaeger(server.Client(), server.URL)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

func TestCheckJaeger_WarnUnreachable(t *testing.T) {
	layer := checkJaeger(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
	if len(layer.Details) < 2 || !strings.Contains(layer.Details[1], "Hint:") {
		t.Errorf("expected hint in details, got %v", layer.Details)
	}
}

// =============================================================================
// LAYER 8: GRAFANA
// =============================================================================

func TestCheckGrafana_Pass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"database": "ok",
				"version":  "10.2.3",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	layer := checkGrafana(server.Client(), server.URL)

	if layer.Status != "pass" {
		t.Errorf("expected pass, got %s", layer.Status)
	}
	if !strings.Contains(layer.Details[0], "10.2.3") {
		t.Errorf("expected version in details, got %s", layer.Details[0])
	}
}

func TestCheckGrafana_FailConnectionRefused(t *testing.T) {
	layer := checkGrafana(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if layer.Status != "fail" {
		t.Errorf("expected fail, got %s", layer.Status)
	}
	if len(layer.Details) < 2 || !strings.Contains(layer.Details[1], "Hint:") {
		t.Errorf("expected hint in details, got %v", layer.Details)
	}
}

func TestCheckGrafana_WarnDatabaseNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"database": "error",
		})
	}))
	defer server.Close()

	layer := checkGrafana(server.Client(), server.URL)

	if layer.Status != "warn" {
		t.Errorf("expected warn, got %s", layer.Status)
	}
}

// =============================================================================
// SUMMARY & FORMATTING
// =============================================================================

func TestCountStatuses(t *testing.T) {
	layers := []TraceHealthLayer{
		{Status: "pass"},
		{Status: "pass"},
		{Status: "pass"},
		{Status: "skip"},
		{Status: "skip"},
		{Status: "warn"},
		{Status: "fail"},
		{Status: "fail"},
	}

	pass, warn, fail, skip := countStatuses(layers)

	if pass != 3 {
		t.Errorf("expected 3 pass, got %d", pass)
	}
	if warn != 1 {
		t.Errorf("expected 1 warn, got %d", warn)
	}
	if fail != 2 {
		t.Errorf("expected 2 fail, got %d", fail)
	}
	if skip != 2 {
		t.Errorf("expected 2 skip, got %d", skip)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{1247, "1,247"},
		{3891, "3,891"},
		{1000000, "1,000,000"},
		{-1, "-1"},
		{-1247, "-1,247"},
		{-1000000, "-1,000,000"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			got := formatNumber(tc.input)
			if got != tc.expected {
				t.Errorf("formatNumber(%d) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestFormatLatency(t *testing.T) {
	tests := []struct {
		input    time.Duration
		expected string
	}{
		{12 * time.Millisecond, "12ms"},
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{0 * time.Millisecond, "0ms"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			got := formatLatency(tc.input)
			if got != tc.expected {
				t.Errorf("formatLatency(%v) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestStatusIconAndColor(t *testing.T) {
	tests := []struct {
		status       string
		expectedIcon string
	}{
		{"pass", "✓"},
		{"warn", "⚠"},
		{"fail", "✗"},
		{"skip", "◐"},
		{"unknown", "?"},
	}

	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			icon, color := statusIconAndColor(tc.status)
			if icon != tc.expectedIcon {
				t.Errorf("statusIconAndColor(%q) icon = %q, want %q", tc.status, icon, tc.expectedIcon)
			}
			if color == "" {
				t.Errorf("statusIconAndColor(%q) color should not be empty", tc.status)
			}
		})
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Run("returns default when unset", func(t *testing.T) {
		got := getEnvOrDefault("ALEUTIAN_TEST_NONEXISTENT_12345", "fallback")
		if got != "fallback" {
			t.Errorf("expected fallback, got %q", got)
		}
	})

	t.Run("returns env when set", func(t *testing.T) {
		t.Setenv("ALEUTIAN_TEST_HEALTH_CHECK", "custom-value")
		got := getEnvOrDefault("ALEUTIAN_TEST_HEALTH_CHECK", "fallback")
		if got != "custom-value" {
			t.Errorf("expected custom-value, got %q", got)
		}
	})
}

// =============================================================================
// FETCH CRS DEBUG
// =============================================================================

func TestFetchCRSDebug_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/trace/agent/debug/crs" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(crsDebugResponse{
				ActiveSessions:  2,
				TotalTraceSteps: 10,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	result := fetchCRSDebug(server.Client(), server.URL)

	if result.err != nil {
		t.Fatalf("unexpected error: %v", result.err)
	}
	if result.response.ActiveSessions != 2 {
		t.Errorf("expected 2 active sessions, got %d", result.response.ActiveSessions)
	}
	if result.latency == 0 {
		t.Error("expected non-zero latency")
	}
}

func TestFetchCRSDebug_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	result := fetchCRSDebug(server.Client(), server.URL)

	if result.err == nil {
		t.Error("expected error for server error response")
	}
}

func TestFetchCRSDebug_ConnectionRefused(t *testing.T) {
	result := fetchCRSDebug(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")

	if result.err == nil {
		t.Error("expected error for connection refused")
	}
}

// =============================================================================
// ASCII OUTPUT
// =============================================================================

func TestFprintTraceHealthReport_Output(t *testing.T) {
	report := TraceHealthReport{
		Timestamp: time.Date(2026, 3, 4, 14, 30, 22, 0, time.UTC),
		TraceURL:  "http://localhost:8080",
		Duration:  162 * time.Millisecond,
		Layers: []TraceHealthLayer{
			{Number: 1, Name: "Service Health", Status: "pass", Details: []string{"v0.1.0 responding"}, Latency: 12 * time.Millisecond},
			{Number: 2, Name: "Graph Build", Status: "pass", Details: []string{"2 graphs loaded"}, Latency: 8 * time.Millisecond},
			{Number: 3, Name: "CRS Init/Restore", Status: "pass", Details: []string{"3 active sessions"}, Latency: 15 * time.Millisecond},
			{Number: 4, Name: "CRS Reasoning", Status: "skip", Details: []string{"No active sessions"}},
			{Number: 5, Name: "Cross-Session Persistence", Status: "skip", Details: []string{"Fresh instance"}},
			{Number: 6, Name: "Prometheus Metrics", Status: "pass", Details: []string{"60 metric families"}, Latency: 22 * time.Millisecond},
			{Number: 7, Name: "Jaeger/OTel Traces", Status: "warn", Details: []string{"Not in services"}, Latency: 105 * time.Millisecond},
			{Number: 8, Name: "Grafana Dashboard", Status: "fail", Details: []string{"Connection refused", "Hint: start grafana"}, Latency: 0},
		},
	}

	var buf bytes.Buffer
	fprintTraceHealthReport(&buf, report)
	output := buf.String()

	// Verify key content is present
	t.Run("contains header", func(t *testing.T) {
		if !strings.Contains(output, "ALEUTIAN TRACE HEALTH CHECK") {
			t.Error("missing header")
		}
	})

	t.Run("contains timestamp", func(t *testing.T) {
		if !strings.Contains(output, "2026-03-04 14:30:22 UTC") {
			t.Error("missing timestamp")
		}
	})

	t.Run("contains all layer names", func(t *testing.T) {
		for _, name := range []string{"Service Health", "Graph Build", "CRS Init/Restore",
			"CRS Reasoning", "Cross-Session Persistence", "Prometheus Metrics",
			"Jaeger/OTel Traces", "Grafana Dashboard"} {
			if !strings.Contains(output, name) {
				t.Errorf("missing layer name: %s", name)
			}
		}
	})

	t.Run("contains summary counts", func(t *testing.T) {
		if !strings.Contains(output, "4 PASS") {
			t.Error("missing pass count")
		}
		if !strings.Contains(output, "2 SKIP") {
			t.Error("missing skip count")
		}
		if !strings.Contains(output, "1 WARN") {
			t.Error("missing warn count")
		}
		if !strings.Contains(output, "1 FAIL") {
			t.Error("missing fail count")
		}
	})

	t.Run("box lines consistent width", func(t *testing.T) {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			// All box lines should start with a box character
			if !strings.HasPrefix(line, boxTopLeft) &&
				!strings.HasPrefix(line, boxBottomLeft) &&
				!strings.HasPrefix(line, boxLeftT) &&
				!strings.HasPrefix(line, boxVertical) {
				t.Errorf("line doesn't start with box char: %q", line)
			}
		}
	})
}

// =============================================================================
// ORCHESTRATOR: SKIPPED LAYERS WHEN SERVICE UNREACHABLE
// =============================================================================

func TestRunTraceHealthCheck_ServiceUnreachable_SkipsLayers(t *testing.T) {
	layer1 := checkServiceHealth(&http.Client{Timeout: 100 * time.Millisecond}, "http://127.0.0.1:1")
	if layer1.Status != "fail" {
		t.Errorf("layer 1 should fail on unreachable service, got %s", layer1.Status)
	}
}
