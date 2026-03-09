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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// maxMetricsBodyBytes is the maximum size we'll read from /metrics to prevent OOM.
const maxMetricsBodyBytes = 10 * 1024 * 1024 // 10 MB

// TraceHealthLayer represents the result of a single health check layer.
//
// Description:
//
//	Captures the status, details, and latency for one of the 8 verification
//	layers in the trace health check.
//
// Fields:
//
//   - Number: Layer number (1-8)
//   - Name: Human-readable layer name
//   - Status: One of "pass", "warn", "fail", "skip"
//   - Details: Detail lines describing what was found
//   - Latency: How long the check took
type TraceHealthLayer struct {
	Number  int
	Name    string
	Status  string
	Details []string
	Latency time.Duration
}

// TraceHealthReport aggregates results from all 8 health check layers.
//
// Description:
//
//	Contains the complete health check report including all layer results,
//	timestamp, and total duration.
//
// Fields:
//
//   - Layers: Results for each layer
//   - Timestamp: When the health check was performed
//   - TraceURL: Base URL of the trace server used
//   - Duration: Total time to complete all checks
type TraceHealthReport struct {
	Layers    []TraceHealthLayer
	Timestamp time.Time
	TraceURL  string
	Duration  time.Duration
}

// traceHealthHTTPClient abstracts HTTP calls for testability.
type traceHealthHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// traceHealthConfig holds resolved configuration for the health check.
type traceHealthConfig struct {
	TraceURL   string
	JaegerURL  string
	GrafanaURL string
	Client     traceHealthHTTPClient
}

// runTraceHealthCheck orchestrates the 8-layer health check and prints results.
//
// Description:
//
//	Runs all 8 health check layers against the trace server and external
//	observability services. Layers 1-5 run sequentially (with dependency
//	on layer 1, and layers 3-5 sharing one HTTP call). Layers 6-8 run
//	concurrently as they check independent external services.
//
// Inputs:
//
//	None (reads configuration from environment variables).
//
// Outputs:
//
//	Prints formatted ASCII health report to stdout.
//
// Thread Safety: Not applicable (CLI entry point, single-threaded).
func runTraceHealthCheck() {
	cfg := traceHealthConfig{
		TraceURL:   getTraceBaseURL(),
		JaegerURL:  getEnvOrDefault("JAEGER_QUERY_URL", fmt.Sprintf("http://%s:%d", DefaultOrchestratorHost, DefaultJaegerPort)),
		GrafanaURL: getEnvOrDefault("GRAFANA_URL", fmt.Sprintf("http://%s:%d", DefaultOrchestratorHost, DefaultGrafanaPort)),
		Client:     &http.Client{Timeout: 5 * time.Second},
	}
	runTraceHealthCheckWithConfig(cfg)
}

// runTraceHealthCheckWithConfig runs the health check with injectable configuration.
//
// Description:
//
//	Core health check logic separated from runTraceHealthCheck for testability.
//	Accepts a config struct so tests can inject mocked servers and URLs.
//
// Inputs:
//
//   - cfg: Health check configuration with URLs and HTTP client
//
// Outputs:
//
//	Prints formatted ASCII health report to stdout.
//
// Thread Safety: Not applicable (single-threaded execution).
func runTraceHealthCheckWithConfig(cfg traceHealthConfig) {
	start := time.Now()
	report := TraceHealthReport{
		Timestamp: start.UTC(),
		TraceURL:  cfg.TraceURL,
	}

	// Layer 1: Service Health (prerequisite for layers 2-5)
	layer1 := checkServiceHealth(cfg.Client, cfg.TraceURL)
	report.Layers = append(report.Layers, layer1)

	// Layers 2-5: Only run if service is reachable
	if layer1.Status != "fail" {
		layer2 := checkGraphBuild(cfg.Client, cfg.TraceURL)
		report.Layers = append(report.Layers, layer2)

		// Layers 3-5 share one HTTP call to CRS debug endpoint
		crsResp := fetchCRSDebug(cfg.Client, cfg.TraceURL)
		layer3 := checkCRSInit(crsResp)
		layer4 := checkCRSReasoning(crsResp)
		layer5 := checkPersistence(crsResp)
		report.Layers = append(report.Layers, layer3, layer4, layer5)
	} else {
		// Skip layers 2-5 if service unreachable
		for _, name := range []string{"Graph Build", "CRS Init/Restore", "CRS Reasoning", "Cross-Session Persistence"} {
			report.Layers = append(report.Layers, TraceHealthLayer{
				Number:  len(report.Layers) + 1,
				Name:    name,
				Status:  "skip",
				Details: []string{"Skipped: service unreachable"},
			})
		}
	}

	// Layer 6: Prometheus Metrics — skip if trace server unreachable (same host)
	// Layers 7-8: Independent external services, run concurrently
	var layer6 TraceHealthLayer
	if layer1.Status == "fail" {
		layer6 = TraceHealthLayer{
			Number:  6,
			Name:    "Prometheus Metrics",
			Status:  "skip",
			Details: []string{"Skipped: trace server unreachable"},
		}
	} else {
		layer6 = checkMetrics(cfg.Client, cfg.TraceURL)
	}
	report.Layers = append(report.Layers, layer6)

	var mu sync.Mutex
	var wg sync.WaitGroup
	layers78 := make([]TraceHealthLayer, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		result := checkJaeger(cfg.Client, cfg.JaegerURL)
		mu.Lock()
		layers78[0] = result
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		result := checkGrafana(cfg.Client, cfg.GrafanaURL)
		mu.Lock()
		layers78[1] = result
		mu.Unlock()
	}()
	wg.Wait()

	report.Layers = append(report.Layers, layers78...)
	report.Duration = time.Since(start)

	printTraceHealthReport(report)
}

// crsDebugResult holds the result of fetching the CRS debug endpoint.
type crsDebugResult struct {
	response *crsDebugResponse
	err      error
	latency  time.Duration
}

// crsDebugResponse mirrors the JSON structure of the CRS debug endpoint.
type crsDebugResponse struct {
	ActiveSessions        int `json:"active_sessions"`
	TotalTraceSteps       int `json:"total_trace_steps"`
	CircuitBreakersActive int `json:"circuit_breakers_active"`
}

// fetchCRSDebug calls the CRS debug endpoint once and returns the result.
//
// Description:
//
//	Makes a single HTTP GET to /v1/trace/agent/debug/crs and returns
//	the parsed response. Called once, result shared by layers 3-5.
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - traceURL: Base URL of the trace server
//
// Outputs:
//
//   - crsDebugResult: Parsed response, error, and latency
//
// Thread Safety: Safe for concurrent use (read-only after return).
func fetchCRSDebug(client traceHealthHTTPClient, traceURL string) crsDebugResult {
	start := time.Now()
	req, err := http.NewRequest("GET", traceURL+"/v1/trace/agent/debug/crs", nil)
	if err != nil {
		return crsDebugResult{err: fmt.Errorf("creating request: %w", err), latency: time.Since(start)}
	}

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return crsDebugResult{err: fmt.Errorf("connecting to CRS debug: %w", err), latency: latency}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return crsDebugResult{err: fmt.Errorf("CRS debug returned HTTP %d", resp.StatusCode), latency: latency}
	}

	var crsResp crsDebugResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&crsResp); decodeErr != nil {
		return crsDebugResult{err: fmt.Errorf("decoding CRS response: %w", decodeErr), latency: latency}
	}

	return crsDebugResult{response: &crsResp, latency: latency}
}

// checkServiceHealth verifies the trace server is responding on /v1/trace/health.
//
// Description:
//
//	Layer 1: Calls GET /v1/trace/health and checks for status=="healthy".
//	This is the prerequisite for all subsequent layers.
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - traceURL: Base URL of the trace server
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with pass/fail status
//
// Thread Safety: Safe for concurrent use.
func checkServiceHealth(client traceHealthHTTPClient, traceURL string) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 1, Name: "Service Health"}
	start := time.Now()

	req, err := http.NewRequest("GET", traceURL+"/v1/trace/health", nil)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Request error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	resp, err := client.Do(req)
	layer.Latency = time.Since(start)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{
			fmt.Sprintf("Unreachable: %s (%v)", traceURL, err),
			"Hint: Start the trace server with: aleutian stack start --service trace",
		}
		return layer
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if resp.StatusCode != http.StatusOK {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("HTTP %d from health endpoint", resp.StatusCode)}
		return layer
	}

	var healthResp struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&healthResp); decodeErr != nil {
		layer.Status = "warn"
		layer.Details = []string{"Health endpoint responded but returned invalid JSON"}
		return layer
	}

	if healthResp.Status != "healthy" {
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Status: %s (version %s)", healthResp.Status, healthResp.Version)}
		return layer
	}

	layer.Status = "pass"
	layer.Details = []string{fmt.Sprintf("%s responding on %s", healthResp.Version, traceURL)}
	return layer
}

// checkGraphBuild verifies the graph build status via /v1/trace/ready and /v1/trace/debug/graph/stats.
//
// Description:
//
//	Layer 2: Checks readiness and graph statistics. Passes if ready==true
//	and at least one graph is loaded with nodes and edges.
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - traceURL: Base URL of the trace server
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with graph statistics
//
// Thread Safety: Safe for concurrent use.
func checkGraphBuild(client traceHealthHTTPClient, traceURL string) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 2, Name: "Graph Build"}
	start := time.Now()

	// Check readiness
	req, err := http.NewRequest("GET", traceURL+"/v1/trace/ready", nil)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Request error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	resp, err := client.Do(req)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Connection error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	var readyResp struct {
		Ready      bool `json:"ready"`
		GraphCount int  `json:"graph_count"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&readyResp); decodeErr != nil {
		layer.Status = "warn"
		layer.Details = []string{"Ready endpoint returned invalid JSON"}
		layer.Latency = time.Since(start)
		return layer
	}

	if !readyResp.Ready {
		layer.Status = "warn"
		layer.Details = []string{"Service not ready (warmup in progress)"}
		layer.Latency = time.Since(start)
		return layer
	}

	// Check graph stats
	statsReq, err := http.NewRequest("GET", traceURL+"/v1/trace/debug/graph/stats", nil)
	if err != nil {
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Ready but unable to fetch graph stats: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	statsResp, err := client.Do(statsReq)
	layer.Latency = time.Since(start)
	if err != nil {
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Ready with %d graphs, stats unavailable", readyResp.GraphCount)}
		return layer
	}
	defer func() {
		if closeErr := statsResp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if statsResp.StatusCode != http.StatusOK {
		// No graphs loaded is not an error — just no projects analyzed yet
		if readyResp.GraphCount == 0 {
			layer.Status = "pass"
			layer.Details = []string{"Ready, no graphs loaded yet (no projects analyzed)"}
			return layer
		}
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Ready with %d graphs, stats returned HTTP %d", readyResp.GraphCount, statsResp.StatusCode)}
		return layer
	}

	var graphStats struct {
		NodeCount int `json:"node_count"`
		EdgeCount int `json:"edge_count"`
	}
	if decodeErr := json.NewDecoder(statsResp.Body).Decode(&graphStats); decodeErr != nil {
		layer.Status = "pass"
		layer.Details = []string{fmt.Sprintf("%d graphs loaded", readyResp.GraphCount)}
		return layer
	}

	layer.Status = "pass"
	layer.Details = []string{fmt.Sprintf("%d graphs loaded: %s nodes, %s edges",
		readyResp.GraphCount,
		formatNumber(graphStats.NodeCount),
		formatNumber(graphStats.EdgeCount))}
	return layer
}

// checkCRSInit verifies CRS initialization from the shared debug response.
//
// Description:
//
//	Layer 3: Checks that CRS is initialized by verifying the debug endpoint
//	responded successfully. Shows active session count.
//
// Inputs:
//
//   - result: Pre-fetched CRS debug result (shared with layers 4-5)
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with CRS initialization status
//
// Thread Safety: Safe for concurrent use (read-only).
func checkCRSInit(result crsDebugResult) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 3, Name: "CRS Init/Restore", Latency: result.latency}

	if result.err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("CRS unavailable: %v", result.err)}
		return layer
	}

	layer.Status = "pass"
	layer.Details = []string{fmt.Sprintf("CRS initialized, %d active sessions", result.response.ActiveSessions)}
	return layer
}

// checkCRSReasoning verifies active CRS reasoning from the shared debug response.
//
// Description:
//
//	Layer 4: Checks if TotalTraceSteps > 0, indicating active reasoning
//	has occurred. Skips if no sessions exist or CRS is unavailable.
//
// Inputs:
//
//   - result: Pre-fetched CRS debug result (shared with layers 3, 5)
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with reasoning status
//
// Thread Safety: Safe for concurrent use (read-only).
func checkCRSReasoning(result crsDebugResult) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 4, Name: "CRS Reasoning", Latency: result.latency}

	if result.err != nil {
		layer.Status = "skip"
		layer.Details = []string{"Skipped: CRS unavailable"}
		return layer
	}

	if result.response.ActiveSessions == 0 {
		layer.Status = "skip"
		layer.Details = []string{"No active sessions to verify reasoning"}
		return layer
	}

	if result.response.TotalTraceSteps > 0 {
		layer.Status = "pass"
		layer.Details = []string{fmt.Sprintf("%d trace steps across %d sessions",
			result.response.TotalTraceSteps, result.response.ActiveSessions)}
		return layer
	}

	layer.Status = "warn"
	layer.Details = []string{"Sessions exist but no trace steps recorded"}
	return layer
}

// checkPersistence verifies cross-session persistence from the shared debug response.
//
// Description:
//
//	Layer 5: Checks for checkpoint data in the CRS debug response.
//	Skips if no sessions exist (fresh instance) or CRS is unavailable.
//
// Inputs:
//
//   - result: Pre-fetched CRS debug result (shared with layers 3-4)
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with persistence status
//
// Thread Safety: Safe for concurrent use (read-only).
func checkPersistence(result crsDebugResult) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 5, Name: "Cross-Session Persistence", Latency: result.latency}

	if result.err != nil {
		layer.Status = "skip"
		layer.Details = []string{"Skipped: CRS unavailable"}
		return layer
	}

	if result.response.ActiveSessions == 0 {
		layer.Status = "skip"
		layer.Details = []string{"No checkpoint data (fresh instance)"}
		return layer
	}

	// If sessions exist, persistence is working
	layer.Status = "pass"
	layer.Details = []string{fmt.Sprintf("%d sessions persisted", result.response.ActiveSessions)}
	return layer
}

// checkMetrics verifies the Prometheus metrics endpoint is serving metrics.
//
// Description:
//
//	Layer 6: Calls GET /v1/metrics and checks that the response contains
//	Prometheus metric definitions (# TYPE lines). Reads at most
//	maxMetricsBodyBytes to prevent unbounded memory usage.
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - traceURL: Base URL of the trace server
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with metric family count
//
// Thread Safety: Safe for concurrent use.
func checkMetrics(client traceHealthHTTPClient, traceURL string) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 6, Name: "Prometheus Metrics"}
	start := time.Now()

	req, err := http.NewRequest("GET", traceURL+"/v1/metrics", nil)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Request error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	resp, err := client.Do(req)
	layer.Latency = time.Since(start)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Metrics endpoint unreachable: %v", err)}
		return layer
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if resp.StatusCode != http.StatusOK {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Metrics endpoint returned HTTP %d", resp.StatusCode)}
		return layer
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetricsBodyBytes))
	if err != nil {
		layer.Status = "warn"
		layer.Details = []string{"Metrics endpoint responded but body unreadable"}
		return layer
	}

	content := string(body)
	familyCount := strings.Count(content, "# TYPE")
	if familyCount == 0 {
		layer.Status = "warn"
		layer.Details = []string{"Metrics endpoint responded but no metric families found"}
		return layer
	}

	layer.Status = "pass"
	layer.Details = []string{fmt.Sprintf("%d metric families exported on /metrics", familyCount)}
	return layer
}

// checkJaeger verifies Jaeger is reachable and has trace data for aleutian-trace.
//
// Description:
//
//	Layer 7: Calls GET {jaegerURL}/api/services and checks if
//	"aleutian-trace" appears in the services list.
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - jaegerURL: Base URL of the Jaeger query service
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with Jaeger status
//
// Thread Safety: Safe for concurrent use.
func checkJaeger(client traceHealthHTTPClient, jaegerURL string) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 7, Name: "Jaeger/OTel Traces"}
	start := time.Now()

	req, err := http.NewRequest("GET", jaegerURL+"/api/services", nil)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Request error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	resp, err := client.Do(req)
	layer.Latency = time.Since(start)
	if err != nil {
		layer.Status = "warn"
		layer.Details = []string{
			fmt.Sprintf("Jaeger unreachable at %s (%v)", jaegerURL, err),
			"Hint: docker-compose -f deploy/docker-compose.observability.yml up -d",
		}
		return layer
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if resp.StatusCode != http.StatusOK {
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Jaeger returned HTTP %d", resp.StatusCode)}
		return layer
	}

	var jaegerResp struct {
		Data []string `json:"data"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&jaegerResp); decodeErr != nil {
		layer.Status = "warn"
		layer.Details = []string{"Jaeger responded but returned invalid JSON"}
		return layer
	}

	for _, svc := range jaegerResp.Data {
		if svc == "aleutian-trace" {
			layer.Status = "pass"
			layer.Details = []string{fmt.Sprintf("Jaeger tracking %d services including aleutian-trace", len(jaegerResp.Data))}
			return layer
		}
	}

	layer.Status = "warn"
	if len(jaegerResp.Data) > 0 {
		layer.Details = []string{fmt.Sprintf("Jaeger reachable (%d services), but \"aleutian-trace\" not found", len(jaegerResp.Data))}
	} else {
		layer.Details = []string{"Jaeger reachable but no services registered"}
	}
	return layer
}

// checkGrafana verifies Grafana is reachable and healthy.
//
// Description:
//
//	Layer 8: Calls GET {grafanaURL}/api/health and checks for database=="ok".
//
// Inputs:
//
//   - client: HTTP client for making requests
//   - grafanaURL: Base URL of the Grafana instance
//
// Outputs:
//
//   - TraceHealthLayer: Layer result with Grafana status
//
// Thread Safety: Safe for concurrent use.
func checkGrafana(client traceHealthHTTPClient, grafanaURL string) TraceHealthLayer {
	layer := TraceHealthLayer{Number: 8, Name: "Grafana Dashboard"}
	start := time.Now()

	req, err := http.NewRequest("GET", grafanaURL+"/api/health", nil)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Request error: %v", err)}
		layer.Latency = time.Since(start)
		return layer
	}

	resp, err := client.Do(req)
	layer.Latency = time.Since(start)
	if err != nil {
		layer.Status = "fail"
		layer.Details = []string{
			fmt.Sprintf("Unreachable: %s (%v)", grafanaURL, err),
			"Hint: docker-compose -f deploy/docker-compose.observability.yml up -d",
		}
		return layer
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close
		}
	}()

	if resp.StatusCode != http.StatusOK {
		layer.Status = "fail"
		layer.Details = []string{fmt.Sprintf("Grafana returned HTTP %d", resp.StatusCode)}
		return layer
	}

	var grafanaResp struct {
		Database string `json:"database"`
		Version  string `json:"version"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&grafanaResp); decodeErr != nil {
		layer.Status = "warn"
		layer.Details = []string{"Grafana responded but returned invalid JSON"}
		return layer
	}

	if grafanaResp.Database != "ok" {
		layer.Status = "warn"
		layer.Details = []string{fmt.Sprintf("Grafana database status: %s", grafanaResp.Database)}
		return layer
	}

	detail := "Grafana healthy"
	if grafanaResp.Version != "" {
		detail = fmt.Sprintf("Grafana %s healthy", grafanaResp.Version)
	}
	layer.Status = "pass"
	layer.Details = []string{detail}
	return layer
}

// printTraceHealthReport renders the health report as formatted ASCII art.
//
// Description:
//
//	Produces a box-drawn report showing each layer's status with color coding.
//	Reuses the box-drawing helpers from cmd_health.go.
//
// Inputs:
//
//   - report: Complete health report to render
//
// Outputs:
//
//	Prints formatted report to stdout.
//
// Thread Safety: Not applicable (writes to stdout).
func printTraceHealthReport(report TraceHealthReport) {
	fprintTraceHealthReport(os.Stdout, report)
}

// fprintTraceHealthReport renders the health report to the given writer.
//
// Description:
//
//	Core rendering logic separated from printTraceHealthReport so tests
//	can capture output without redirecting os.Stdout.
//
// Inputs:
//
//   - w: Writer to render to
//   - report: Complete health report to render
//
// Outputs:
//
//	Writes formatted report to w.
//
// Thread Safety: Not safe for concurrent use (writes are not synchronized).
func fprintTraceHealthReport(w io.Writer, report TraceHealthReport) {
	width := 75

	fprintBoxTop(w, width)
	fprintBoxCenter(w, "ALEUTIAN TRACE HEALTH CHECK", width)
	fprintBoxCenter(w, report.Timestamp.Format("2006-01-02 15:04:05 UTC"), width)
	fprintBoxSeparator(w, width)

	for _, layer := range report.Layers {
		fprintBoxLine(w, "", width)

		// Status icon and color
		icon, color := statusIconAndColor(layer.Status)
		statusLabel := strings.ToUpper(layer.Status)

		// Format latency
		latencyStr := ""
		if layer.Latency > 0 {
			latencyStr = fmt.Sprintf("  (%s)", formatLatency(layer.Latency))
		}

		// Layer header line
		headerLine := fmt.Sprintf("  [%d] %-28s %s%s %s%s%s",
			layer.Number, layer.Name,
			color, icon, statusLabel, colorReset, latencyStr)
		fprintBoxLine(w, headerLine, width)

		// Detail lines
		for _, detail := range layer.Details {
			fprintBoxLine(w, fmt.Sprintf("      └─ %s", detail), width)
		}
	}

	fprintBoxLine(w, "", width)
	fprintBoxSeparator(w, width)

	// Summary line
	pass, warn, fail, skip := countStatuses(report.Layers)
	summaryLine := fmt.Sprintf("  Summary: %s%d PASS%s  %d SKIP  %s%d WARN%s  %s%d FAIL%s     Completed in %s",
		colorGreen, pass, colorReset,
		skip,
		colorYellow, warn, colorReset,
		colorRed, fail, colorReset,
		formatLatency(report.Duration))
	fprintBoxLine(w, summaryLine, width)

	fprintBoxBottom(w, width)
}

// fprintBoxTop writes a box top border to the writer.
func fprintBoxTop(w io.Writer, width int) {
	fmt.Fprint(w, boxTopLeft)
	for i := 0; i < width-2; i++ {
		fmt.Fprint(w, boxHorizontal)
	}
	fmt.Fprintln(w, boxTopRight)
}

// fprintBoxBottom writes a box bottom border to the writer.
func fprintBoxBottom(w io.Writer, width int) {
	fmt.Fprint(w, boxBottomLeft)
	for i := 0; i < width-2; i++ {
		fmt.Fprint(w, boxHorizontal)
	}
	fmt.Fprintln(w, boxBottomRight)
}

// fprintBoxSeparator writes a box separator line to the writer.
func fprintBoxSeparator(w io.Writer, width int) {
	fmt.Fprint(w, boxLeftT)
	for i := 0; i < width-2; i++ {
		fmt.Fprint(w, boxHorizontal)
	}
	fmt.Fprintln(w, boxRightT)
}

// fprintBoxLine writes a box content line to the writer.
func fprintBoxLine(w io.Writer, content string, width int) {
	visibleLen := visibleLength(content)
	padding := width - 4 - visibleLen
	if padding < 0 {
		padding = 0
	}
	fmt.Fprintf(w, "%s %s%s %s\n", boxVertical, content, strings.Repeat(" ", padding), boxVertical)
}

// fprintBoxCenter writes a centered box content line to the writer.
func fprintBoxCenter(w io.Writer, content string, width int) {
	visibleLen := visibleLength(content)
	totalPadding := width - 4 - visibleLen
	leftPad := totalPadding / 2
	rightPad := totalPadding - leftPad
	fmt.Fprintf(w, "%s %s%s%s %s\n", boxVertical,
		strings.Repeat(" ", leftPad), content, strings.Repeat(" ", rightPad), boxVertical)
}

// statusIconAndColor returns the icon and ANSI color for a health status.
//
// Description:
//
//	Maps status strings to their display icons and colors.
//
// Inputs:
//
//   - status: One of "pass", "warn", "fail", "skip"
//
// Outputs:
//
//   - icon: Unicode status icon
//   - color: ANSI color escape code
//
// Thread Safety: Safe for concurrent use (pure function).
func statusIconAndColor(status string) (icon, color string) {
	switch status {
	case "pass":
		return "✓", colorGreen
	case "warn":
		return "⚠", colorYellow
	case "fail":
		return "✗", colorRed
	case "skip":
		return "◐", "\033[90m" // gray
	default:
		return "?", colorReset
	}
}

// countStatuses tallies the pass/warn/fail/skip counts from a slice of layers.
//
// Description:
//
//	Iterates through layers and counts each status type for the summary line.
//
// Inputs:
//
//   - layers: Slice of health check layer results
//
// Outputs:
//
//   - pass, warn, fail, skip: Counts of each status
//
// Thread Safety: Safe for concurrent use (read-only).
func countStatuses(layers []TraceHealthLayer) (pass, warn, fail, skip int) {
	for _, l := range layers {
		switch l.Status {
		case "pass":
			pass++
		case "warn":
			warn++
		case "fail":
			fail++
		case "skip":
			skip++
		}
	}
	return
}

// formatLatency formats a duration into a human-friendly string.
//
// Description:
//
//	Formats durations shorter than 1s as milliseconds, otherwise as seconds.
//
// Inputs:
//
//   - d: Duration to format
//
// Outputs:
//
//   - string: Formatted duration (e.g. "12ms", "1.5s")
//
// Thread Safety: Safe for concurrent use (pure function).
func formatLatency(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// formatNumber formats a non-negative integer with comma separators for readability.
//
// Description:
//
//	Adds comma separators to large numbers (e.g. 1247 -> "1,247").
//	Handles negative numbers by formatting the absolute value and
//	prepending a minus sign.
//
// Inputs:
//
//   - n: Integer to format
//
// Outputs:
//
//   - string: Comma-formatted number
//
// Thread Safety: Safe for concurrent use (pure function).
func formatNumber(n int) string {
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}

	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}

	var result strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result.WriteByte(',')
		}
		result.WriteRune(c)
	}

	formatted := result.String()
	if negative {
		return "-" + formatted
	}
	return formatted
}

// getEnvOrDefault returns the value of an environment variable or a default.
//
// Description:
//
//	Looks up the named environment variable and returns its value if set
//	and non-empty, otherwise returns the default value.
//
// Inputs:
//
//   - key: Environment variable name
//   - defaultVal: Value to return if env var is not set
//
// Outputs:
//
//   - string: Environment variable value or default
//
// Thread Safety: Safe for concurrent use.
func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
