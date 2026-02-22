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

	"github.com/gin-gonic/gin"
)

func init() {
	// Set Gin to test mode to reduce noise
	gin.SetMode(gin.TestMode)
}

func setupTestRouter(svc *Service) *gin.Engine {
	router := gin.New()
	handlers := NewHandlers(svc)
	v1 := router.Group("/v1")
	RegisterRoutes(v1, handlers)
	return router
}

func TestHandlers_HandleHealth(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp HealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "healthy" {
		t.Errorf("expected status 'healthy', got %q", resp.Status)
	}

	if resp.Version != ServiceVersion {
		t.Errorf("expected version %q, got %q", ServiceVersion, resp.Version)
	}
}

func TestHandlers_HandleReady(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	// Mark warmup as complete for this test
	MarkWarmupComplete()
	defer ResetWarmupStatus() // Clean up after test

	req, _ := http.NewRequest("GET", "/v1/codebuddy/ready", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp ReadyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Ready {
		t.Error("expected Ready=true")
	}

	if resp.GraphCount != 0 {
		t.Errorf("expected 0 graphs, got %d", resp.GraphCount)
	}
}

func TestHandlers_HandleInit_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "empty body",
			body:       "{}",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
		{
			name:       "relative path",
			body:       `{"project_root": "relative/path"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_PATH",
		},
		{
			name:       "path traversal",
			body:       `{"project_root": "/some/path/../traversal"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "PATH_TRAVERSAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/v1/codebuddy/init",
				bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, w.Code)
			}

			var errResp ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}

			if errResp.Code != tt.wantCode {
				t.Errorf("expected code %q, got %q", tt.wantCode, errResp.Code)
			}
		})
	}
}

func TestHandlers_HandleContext_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent", "query": "find HandleUser"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/context",
		bytes.NewBufferString(body))
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

func TestHandlers_HandleContext_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		body string
	}{
		{"missing graph_id", `{"query": "test"}`},
		{"missing query", `{"graph_id": "test"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/v1/codebuddy/context",
				bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}
		})
	}
}

func TestHandlers_HandleCallers_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		url  string
	}{
		{"missing graph_id", "/v1/codebuddy/callers?function=test"},
		{"missing function", "/v1/codebuddy/callers?graph_id=test"},
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

func TestHandlers_HandleCallers_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/callers?graph_id=nonexistent&function=test", nil)
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

func TestHandlers_HandleImplementations_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	tests := []struct {
		name string
		url  string
	}{
		{"missing graph_id", "/v1/codebuddy/implementations?interface=test"},
		{"missing interface", "/v1/codebuddy/implementations?graph_id=test"},
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

func TestHandlers_HandleSymbol_MissingParameters(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	// Missing graph_id
	req, _ := http.NewRequest("GET", "/v1/codebuddy/symbol/test-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleSymbol_GraphNotInitialized(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/symbol/test-id?graph_id=nonexistent", nil)
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

func TestSymbolInfoFromAST(t *testing.T) {
	t.Run("nil symbol returns nil", func(t *testing.T) {
		result := SymbolInfoFromAST(nil)
		if result != nil {
			t.Error("expected nil for nil input")
		}
	})
}

// =============================================================================
// DEBUG ENDPOINT TESTS (GR-43)
// =============================================================================

func TestHandlers_HandleGetGraphStats_NoGraphsCached(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/debug/graph/stats", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "NO_GRAPHS" {
		t.Errorf("expected code 'NO_GRAPHS', got %q", errResp.Code)
	}
}

func TestHandlers_HandleGetGraphStats_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/debug/graph/stats?graph_id=nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if errResp.Code != "GRAPH_NOT_FOUND" {
		t.Errorf("expected code 'GRAPH_NOT_FOUND', got %q", errResp.Code)
	}
}
