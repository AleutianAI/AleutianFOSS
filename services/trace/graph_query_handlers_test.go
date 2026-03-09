// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// setupGraphQueryTestService creates a Service with a pre-cached graph containing
// funcA -> funcB -> funcC for testing graph query endpoints.
func setupGraphQueryTestService(t *testing.T) (*Service, string) {
	t.Helper()

	svc := NewService(DefaultServiceConfig())

	g := graph.NewGraph("/test/project")
	symA := &ast.Symbol{
		ID:            "file.go:1:funcA",
		Name:          "funcA",
		Kind:          ast.SymbolKindFunction,
		FilePath:      "file.go",
		StartLine:     1,
		EndLine:       10,
		Language:      "go",
		Exported:      true,
		ParsedAtMilli: time.Now().UnixMilli(),
	}
	symB := &ast.Symbol{
		ID:            "file.go:20:funcB",
		Name:          "funcB",
		Kind:          ast.SymbolKindFunction,
		FilePath:      "file.go",
		StartLine:     20,
		EndLine:       30,
		Language:      "go",
		Exported:      true,
		ParsedAtMilli: time.Now().UnixMilli(),
	}
	symC := &ast.Symbol{
		ID:            "other.go:1:funcC",
		Name:          "funcC",
		Kind:          ast.SymbolKindFunction,
		FilePath:      "other.go",
		StartLine:     1,
		EndLine:       15,
		Language:      "go",
		Exported:      true,
		ParsedAtMilli: time.Now().UnixMilli(),
	}

	g.AddNode(symA)
	g.AddNode(symB)
	g.AddNode(symC)
	g.AddEdge(symA.ID, symB.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath:  "file.go",
		StartLine: 5,
		EndLine:   5,
	})
	g.AddEdge(symB.ID, symC.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath:  "file.go",
		StartLine: 25,
		EndLine:   25,
	})
	g.Freeze()

	graphID := svc.generateGraphID("/test/project")
	idx := index.NewSymbolIndex()

	svc.graphs[graphID] = &CachedGraph{
		Graph:        g,
		Index:        idx,
		BuiltAtMilli: g.BuiltAtMilli,
		ProjectRoot:  "/test/project",
	}

	return svc, graphID
}

// =============================================================================
// HandleFindCallees Tests
// =============================================================================

func TestHandlers_HandleFindCallees_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		url  string
	}{
		{"missing graph_id", "/v1/trace/callees?function=test"},
		{"missing function", "/v1/trace/callees?graph_id=test"},
		{"missing both", "/v1/trace/callees"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandlers_HandleFindCallees_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/callees?graph_id=nonexistent&function=test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindCallees_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/callees?graph_id="+graphID+"&function=funcA", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp CalleesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Function != "funcA" {
		t.Errorf("expected function 'funcA', got %q", resp.Function)
	}

	if len(resp.Callees) != 1 {
		t.Fatalf("expected 1 callee, got %d", len(resp.Callees))
	}

	if resp.Callees[0].Name != "funcB" {
		t.Errorf("expected callee 'funcB', got %q", resp.Callees[0].Name)
	}
}

func TestHandlers_HandleFindCallees_EmptyResult(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/callees?graph_id="+graphID+"&function=funcC", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp CalleesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Callees) != 0 {
		t.Errorf("expected 0 callees for leaf node, got %d", len(resp.Callees))
	}
}

// =============================================================================
// HandleGetCallChain Tests
// =============================================================================

func TestHandlers_HandleGetCallChain_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		url  string
	}{
		{"missing graph_id", "/v1/trace/call-chain?from=a&to=b"},
		{"missing from", "/v1/trace/call-chain?graph_id=test&to=b"},
		{"missing to", "/v1/trace/call-chain?graph_id=test&from=a"},
		{"missing all", "/v1/trace/call-chain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandlers_HandleGetCallChain_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/call-chain?graph_id=nonexistent&from=a&to=b", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleGetCallChain_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/call-chain?graph_id="+graphID+"&from=funcA&to=funcC", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp CallChainResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.From != "funcA" {
		t.Errorf("expected from 'funcA', got %q", resp.From)
	}
	if resp.To != "funcC" {
		t.Errorf("expected to 'funcC', got %q", resp.To)
	}
	if resp.Length != 2 {
		t.Errorf("expected length 2, got %d", resp.Length)
	}
	if len(resp.Path) != 3 {
		t.Errorf("expected 3 nodes in path (A->B->C), got %d", len(resp.Path))
	}
}

func TestHandlers_HandleGetCallChain_SymbolNotFound(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/call-chain?graph_id="+graphID+"&from=nonexistent&to=funcC", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "SYMBOL_NOT_FOUND" {
		t.Errorf("expected code 'SYMBOL_NOT_FOUND', got %q", errResp.Code)
	}
}

// =============================================================================
// HandleFindReferences Tests
// =============================================================================

func TestHandlers_HandleFindReferences_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		url  string
	}{
		{"missing graph_id", "/v1/trace/references?symbol=test"},
		{"missing symbol", "/v1/trace/references?graph_id=test"},
		{"missing both", "/v1/trace/references"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandlers_HandleFindReferences_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/references?graph_id=nonexistent&symbol=test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindReferences_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	// funcB has an incoming call edge from funcA (file.go:5)
	req, _ := http.NewRequest("GET", "/v1/trace/references?graph_id="+graphID+"&symbol=funcB", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp ReferencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Symbol != "funcB" {
		t.Errorf("expected symbol 'funcB', got %q", resp.Symbol)
	}

	if len(resp.References) == 0 {
		t.Error("expected at least 1 reference for funcB")
	}
}

// =============================================================================
// HandleFindHotspots Tests
// =============================================================================

func TestHandlers_HandleFindHotspots_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("POST", "/v1/trace/analytics/hotspots", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindHotspots_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindHotspotsRequest{GraphID: "nonexistent"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/hotspots", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindHotspots_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindHotspotsRequest{GraphID: graphID, Limit: 5})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/hotspots", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
	if resp.LatencyMs < 0 {
		t.Error("expected non-negative latency")
	}
}

// =============================================================================
// HandleFindCycles Tests
// =============================================================================

func TestHandlers_HandleFindCycles_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("POST", "/v1/trace/analytics/cycles", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindCycles_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindCyclesRequest{GraphID: "nonexistent"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/cycles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindCycles_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindCyclesRequest{GraphID: graphID})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/cycles", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Linear graph (A->B->C) has no cycles
	if resp.Result == nil {
		t.Error("expected non-nil result (empty slice, not nil)")
	}
}

// =============================================================================
// HandleFindImportant Tests
// =============================================================================

func TestHandlers_HandleFindImportant_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("POST", "/v1/trace/analytics/important", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindImportant_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindImportantRequest{GraphID: "nonexistent"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/important", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindImportant_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindImportantRequest{GraphID: graphID, Limit: 3})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/important", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
}

// =============================================================================
// HandleFindCommunities Tests
// =============================================================================

func TestHandlers_HandleFindCommunities_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("POST", "/v1/trace/analytics/communities", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindCommunities_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindCommunitiesRequest{GraphID: "nonexistent"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/communities", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindCommunities_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindCommunitiesRequest{GraphID: graphID})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/communities", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
}

// =============================================================================
// HandleFindPath Tests
// =============================================================================

func TestHandlers_HandleFindPath_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		body string
	}{
		{"missing graph_id", `{"from":"a","to":"b"}`},
		{"missing from", `{"graph_id":"test","to":"b"}`},
		{"missing to", `{"graph_id":"test","from":"a"}`},
		{"empty body", `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/v1/trace/analytics/path", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandlers_HandleFindPath_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindPathRequest{GraphID: "nonexistent", From: "a", To: "b"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/path", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_INITIALIZED" {
		t.Errorf("expected code 'GRAPH_NOT_INITIALIZED', got %q", errResp.Code)
	}
}

func TestHandlers_HandleFindPath_Success(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindPathRequest{GraphID: graphID, From: "funcA", To: "funcC"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/path", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Result == nil {
		t.Error("expected non-nil result")
	}
}

func TestHandlers_HandleFindPath_SymbolNotFound(t *testing.T) {
	svc, graphID := setupGraphQueryTestService(t)
	router := setupTestRouter(svc)

	body, _ := json.Marshal(FindPathRequest{GraphID: graphID, From: "nonexistent", To: "funcC"})
	req, _ := http.NewRequest("POST", "/v1/trace/analytics/path", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d: %s", http.StatusBadRequest, w.Code, w.Body.String())
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "SYMBOL_NOT_FOUND" {
		t.Errorf("expected code 'SYMBOL_NOT_FOUND', got %q", errResp.Code)
	}
}
