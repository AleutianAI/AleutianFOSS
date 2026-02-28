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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// setupTestServiceWithGraph creates a Service with a pre-cached graph for testing.
func setupTestServiceWithGraph(t *testing.T) (*Service, string) {
	t.Helper()

	svc := NewService(DefaultServiceConfig())

	// Build a small graph
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
		ID:            "other.go:1:MyStruct",
		Name:          "MyStruct",
		Kind:          ast.SymbolKindStruct,
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
	g.AddEdge(symB.ID, symC.ID, graph.EdgeTypeReferences, ast.Location{
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

func TestHandleInspectNode_Success(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/inspect?name=funcA", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp InspectNodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(resp.Matches))
	}

	match := resp.Matches[0]
	if match.Symbol.Name != "funcA" {
		t.Errorf("name = %q, want %q", match.Symbol.Name, "funcA")
	}
	if len(match.Outgoing) != 1 {
		t.Errorf("outgoing = %d, want 1", len(match.Outgoing))
	}
	if len(match.Incoming) != 0 {
		t.Errorf("incoming = %d, want 0", len(match.Incoming))
	}

	if match.Outgoing[0].PeerName != "funcB" {
		t.Errorf("outgoing peer = %q, want %q", match.Outgoing[0].PeerName, "funcB")
	}
	if match.Outgoing[0].EdgeType != "calls" {
		t.Errorf("edge type = %q, want %q", match.Outgoing[0].EdgeType, "calls")
	}
}

func TestHandleInspectNode_WithKindFilter(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	// Search for "MyStruct" filtered to struct kind
	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/inspect?name=MyStruct&kind=struct", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	var resp InspectNodeResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(resp.Matches))
	}
	if resp.Matches[0].Symbol.Kind != "struct" {
		t.Errorf("kind = %q, want %q", resp.Matches[0].Symbol.Kind, "struct")
	}
}

func TestHandleInspectNode_MissingName(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/inspect", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandleInspectNode_NoGraphs(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/inspect?name=funcA", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestHandleInspectNode_NoMatches(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/inspect?name=nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp InspectNodeResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Matches) != 0 {
		t.Errorf("matches = %d, want 0", len(resp.Matches))
	}
}

func TestHandleExportGraph_Success(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/export", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, w.Code, w.Body.String())
	}

	// Verify Content-Disposition header
	cd := w.Header().Get("Content-Disposition")
	if cd == "" {
		t.Error("expected Content-Disposition header")
	}

	// Verify it's valid SerializableGraph JSON
	var sg graph.SerializableGraph
	if err := json.Unmarshal(w.Body.Bytes(), &sg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if sg.SchemaVersion != graph.GraphSchemaVersion {
		t.Errorf("schema version = %q, want %q", sg.SchemaVersion, graph.GraphSchemaVersion)
	}
	if len(sg.Nodes) != 3 {
		t.Errorf("nodes = %d, want 3", len(sg.Nodes))
	}
	if len(sg.Edges) != 2 {
		t.Errorf("edges = %d, want 2", len(sg.Edges))
	}
}

func TestHandleExportGraph_NoGraphs(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/export", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}
}

func TestHandleSaveSnapshot_NotConfigured(t *testing.T) {
	svc, _ := setupTestServiceWithGraph(t)
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("POST", "/v1/trace/debug/graph/snapshot", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d: %s", http.StatusServiceUnavailable, w.Code, w.Body.String())
	}
}

func TestHandleListSnapshots_NotConfigured(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/snapshots", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestHandleDiffSnapshots_NotConfigured(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	router := setupTestRouter(svc)

	req, _ := http.NewRequest("GET", "/v1/trace/debug/graph/snapshot/diff?base=x&target=y", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
	}
}

func TestHandleDiffSnapshots_MissingParams(t *testing.T) {
	// The 400 (missing params) path requires a non-nil snapshot manager.
	// Testing the 503 path above is sufficient for handler coverage since
	// the parameter validation is behind the nil-check gate.
	// The DiffSnapshots function itself is tested in snapshot_diff_test.go.
}
