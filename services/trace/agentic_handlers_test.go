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

// =============================================================================
// TOOL DISCOVERY TESTS
// =============================================================================

func TestHandlers_HandleGetTools(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/codebuddy/tools", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp ToolsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Should have 24 tools
	if len(resp.Tools) != 24 {
		t.Errorf("expected 24 tools, got %d", len(resp.Tools))
	}

	// Verify tool categories are present
	categories := make(map[string]int)
	for _, tool := range resp.Tools {
		categories[tool.Category]++
	}

	expectedCategories := map[string]int{
		"explore":    9,
		"reason":     6,
		"coordinate": 3,
		"patterns":   6,
	}

	for cat, expected := range expectedCategories {
		if got := categories[cat]; got != expected {
			t.Errorf("category %q: expected %d tools, got %d", cat, expected, got)
		}
	}
}

// =============================================================================
// EXPLORATION HANDLER TESTS
// =============================================================================

func TestHandlers_HandleFindEntryPoints_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/explore/entry_points", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Code != "GRAPH_NOT_FOUND" {
		t.Errorf("expected code GRAPH_NOT_FOUND, got %q", resp.Code)
	}
}

func TestHandlers_HandleFindEntryPoints_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	// Missing required graph_id
	body := `{}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/explore/entry_points", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Code != "INVALID_REQUEST" {
		t.Errorf("expected code INVALID_REQUEST, got %q", resp.Code)
	}
}

func TestHandlers_HandleTraceDataFlow_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent", "symbol_id": "test"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/explore/data_flow", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindSimilarCode_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent", "symbol_id": "test"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/explore/similar_code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// REASONING HANDLER TESTS
// =============================================================================

func TestHandlers_HandleCheckBreakingChanges_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent", "symbol_id": "test", "new_signature": "func Test()"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/reason/breaking_changes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleValidateChange_Success(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"code": "func Hello() {}", "language": "go"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/reason/validate_change", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp AgenticResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.LatencyMs < 0 {
		t.Error("expected non-negative latency")
	}
}

func TestHandlers_HandleValidateChange_InvalidRequest(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	// Missing required code field
	body := `{"language": "go"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/reason/validate_change", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// COORDINATION HANDLER TESTS
// =============================================================================

func TestHandlers_HandlePlanMultiFileChange_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent", "target_id": "test", "change_type": "add_parameter"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/coordinate/plan_changes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleValidatePlan_PlanNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"plan_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/coordinate/validate_plan", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Code != "PLAN_NOT_FOUND" {
		t.Errorf("expected code PLAN_NOT_FOUND, got %q", resp.Code)
	}
}

func TestHandlers_HandlePreviewChanges_PlanNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"plan_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/coordinate/preview_changes", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

// =============================================================================
// PATTERN HANDLER TESTS
// =============================================================================

func TestHandlers_HandleDetectPatterns_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/detect", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindCodeSmells_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/code_smells", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindDuplication_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/duplication", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindCircularDeps_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/circular_deps", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleExtractConventions_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/conventions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandlers_HandleFindDeadCode_GraphNotFound(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	body := `{"graph_id": "nonexistent"}`
	req, _ := http.NewRequest("POST", "/v1/codebuddy/patterns/dead_code", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

// =============================================================================
// ENDPOINT ROUTING TESTS
// =============================================================================

func TestAgenticRoutes_AllEndpointsExist(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	endpoints := []struct {
		method string
		path   string
	}{
		// Tool discovery
		{"GET", "/v1/codebuddy/tools"},
		// Exploration
		{"POST", "/v1/codebuddy/explore/entry_points"},
		{"POST", "/v1/codebuddy/explore/data_flow"},
		{"POST", "/v1/codebuddy/explore/error_flow"},
		{"POST", "/v1/codebuddy/explore/config_usage"},
		{"POST", "/v1/codebuddy/explore/similar_code"},
		{"POST", "/v1/codebuddy/explore/minimal_context"},
		{"POST", "/v1/codebuddy/explore/summarize_file"},
		{"POST", "/v1/codebuddy/explore/summarize_package"},
		{"POST", "/v1/codebuddy/explore/change_impact"},
		// Reasoning
		{"POST", "/v1/codebuddy/reason/breaking_changes"},
		{"POST", "/v1/codebuddy/reason/simulate_change"},
		{"POST", "/v1/codebuddy/reason/validate_change"},
		{"POST", "/v1/codebuddy/reason/test_coverage"},
		{"POST", "/v1/codebuddy/reason/side_effects"},
		{"POST", "/v1/codebuddy/reason/suggest_refactor"},
		// Coordination
		{"POST", "/v1/codebuddy/coordinate/plan_changes"},
		{"POST", "/v1/codebuddy/coordinate/validate_plan"},
		{"POST", "/v1/codebuddy/coordinate/preview_changes"},
		// Patterns
		{"POST", "/v1/codebuddy/patterns/detect"},
		{"POST", "/v1/codebuddy/patterns/code_smells"},
		{"POST", "/v1/codebuddy/patterns/duplication"},
		{"POST", "/v1/codebuddy/patterns/circular_deps"},
		{"POST", "/v1/codebuddy/patterns/conventions"},
		{"POST", "/v1/codebuddy/patterns/dead_code"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			var req *http.Request
			if ep.method == "POST" {
				req, _ = http.NewRequest(ep.method, ep.path, bytes.NewBufferString("{}"))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req, _ = http.NewRequest(ep.method, ep.path, nil)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Should not be 404 (route exists)
			if w.Code == http.StatusNotFound {
				t.Errorf("endpoint %s %s returned 404 - route not registered", ep.method, ep.path)
			}
		})
	}
}

// =============================================================================
// HELPER FUNCTION FOR INTEGRATION TESTS
// =============================================================================

// setupTestRouterWithInitializedGraph creates a router with a pre-initialized graph
// for integration testing. This requires a valid project directory.
func setupTestRouterWithInitializedGraph(t *testing.T, projectRoot string) (*gin.Engine, string) {
	t.Helper()

	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	// Initialize a graph
	body, _ := json.Marshal(InitRequest{ProjectRoot: projectRoot})
	req, _ := http.NewRequest("POST", "/v1/codebuddy/init", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Skipf("Could not initialize graph (status %d): %s", w.Code, w.Body.String())
	}

	var resp InitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal init response: %v", err)
	}

	return router, resp.GraphID
}
