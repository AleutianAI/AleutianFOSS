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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/bridge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResources_Health(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/trace/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"healthy","version":"1.0.0"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "trace://health"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected resource contents")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "healthy") {
		t.Errorf("expected healthy in response, got: %s", text)
	}
}

func TestResources_GraphStats(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/trace/debug/graph/stats" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total_graphs":2,"total_nodes":500}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	b := bridge.NewToolBridge(bridge.WithTraceURL(ts.URL))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "trace://graph/stats"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected resource contents")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "total_graphs") {
		t.Errorf("expected total_graphs in response, got: %s", text)
	}
}

func TestResources_HealthUnreachable(t *testing.T) {
	b := bridge.NewToolBridge(bridge.WithTraceURL("http://localhost:1"))
	cs, cleanup := setupMCPTest(t, b)
	defer cleanup()

	ctx := context.Background()
	result, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "trace://health"})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected resource contents")
	}

	text := result.Contents[0].Text
	if !strings.Contains(text, "not reachable") {
		t.Errorf("expected 'not reachable' message, got: %s", text)
	}
}
