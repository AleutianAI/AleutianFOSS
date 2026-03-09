// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

//go:build integration

package rag_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
	wvclient "github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// setupEmbedClient creates an EmbedClient for integration tests from ORCHESTRATOR_URL env.
func setupEmbedClient(t *testing.T) *rag.EmbedClient {
	t.Helper()
	orchURL := os.Getenv("ORCHESTRATOR_URL")
	if orchURL == "" {
		t.Skip("ORCHESTRATOR_URL not set, skipping integration test")
	}
	model := os.Getenv("EMBEDDING_MODEL")
	if model == "" {
		model = "nomic-embed-text-v2-moe"
	}
	embedder, err := rag.NewEmbedClient(orchURL, model)
	if err != nil {
		t.Fatalf("NewEmbedClient: %v", err)
	}
	return embedder
}

// weaviateURL returns the Weaviate URL from env or skips the test.
func weaviateURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("WEAVIATE_SERVICE_URL")
	if url == "" {
		t.Skip("WEAVIATE_SERVICE_URL not set, skipping integration test")
	}
	return url
}

// setupWeaviateClient creates a Weaviate client for integration tests.
func setupWeaviateClient(t *testing.T) *wvclient.Client {
	t.Helper()
	url := weaviateURL(t)
	cfg := wvclient.Config{
		Host:   url[len("http://"):], // Strip scheme for client
		Scheme: "http",
	}
	client, err := wvclient.NewClient(cfg)
	if err != nil {
		t.Fatalf("Failed to create Weaviate client: %v", err)
	}
	return client
}

// buildTestIndex creates a SymbolIndex with test symbols.
func buildTestIndex(t *testing.T) *index.SymbolIndex {
	t.Helper()
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{
			ID:         "test:1",
			Name:       "HandleRequest",
			Kind:       ast.SymbolKindFunction,
			Package:    "pkg/handlers",
			FilePath:   "pkg/handlers/request.go",
			Exported:   true,
			Language:   "go",
			Signature:  "func HandleRequest(ctx context.Context, req *Request) (*Response, error)",
			DocComment: "HandleRequest processes incoming HTTP requests and dispatches them to registered route handlers.",
			StartLine:  10,
			EndLine:    50,
		},
		{
			ID:         "test:2",
			Name:       "RenderPage",
			Kind:       ast.SymbolKindFunction,
			Package:    "pkg/render",
			FilePath:   "pkg/render/page.go",
			Exported:   true,
			Language:   "go",
			Signature:  "func RenderPage(ctx context.Context, tmpl *Template) ([]byte, error)",
			DocComment: "RenderPage processes the template rendering pipeline for HTML output.",
			StartLine:  15,
			EndLine:    45,
		},
		{
			ID:         "test:3",
			Name:       "NewPool",
			Kind:       ast.SymbolKindFunction,
			Package:    "pkg/db",
			FilePath:   "pkg/db/pool.go",
			Exported:   true,
			Language:   "go",
			Signature:  "func NewPool(ctx context.Context, connStr string) (*Pool, error)",
			DocComment: "NewPool creates a new database connection pool with configurable max connections.",
			StartLine:  20,
			EndLine:    60,
		},
		{
			ID:         "test:4",
			Name:       "AuthMiddleware",
			Kind:       ast.SymbolKindFunction,
			Package:    "pkg/auth",
			FilePath:   "pkg/auth/middleware.go",
			Exported:   true,
			Language:   "go",
			Signature:  "func AuthMiddleware(next http.Handler) http.Handler",
			DocComment: "AuthMiddleware validates authentication tokens and rejects unauthorized requests.",
			StartLine:  5,
			EndLine:    30,
		},
		{
			ID:        "test:5",
			Name:      "ServeHTTP",
			Kind:      ast.SymbolKindMethod,
			Package:   "pkg/server",
			FilePath:  "pkg/server/router.go",
			Exported:  true,
			Language:  "go",
			Receiver:  "Router",
			Signature: "func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request)",
			StartLine: 100,
			EndLine:   150,
		},
	}
	for _, sym := range symbols {
		idx.Add(sym)
	}
	return idx
}

func TestSemanticResolver_Integration_ReturnsResults(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	// Ensure schema exists.
	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	// Index test symbols.
	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	count, err := store.IndexSymbols(ctx, idx, "test-hash-1", nil)
	if err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}
	if count == 0 {
		t.Fatal("IndexSymbols returned 0 — no symbols indexed")
	}
	t.Logf("Indexed %d symbols", count)

	// Wait for Weaviate to process vectors.
	time.Sleep(2 * time.Second)

	// Create semantic resolver and test.
	resolver, err := rag.NewSemanticResolver(client, dataSpace, setupEmbedClient(t))
	if err != nil {
		t.Fatalf("NewSemanticResolver: %v", err)
	}

	// Test: "HTTP handler" should match HandleRequest or ServeHTTP.
	results, err := resolver.Resolve(ctx, []string{"HTTP handler"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(results) == 0 {
		t.Error("Resolve('HTTP handler') returned 0 results, expected matches")
	}
	for _, r := range results {
		t.Logf("Result: %s → %s %q (confidence: %.2f)", r.Raw, r.Kind, r.Resolved, r.Confidence)
	}

	// Cleanup.
	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}

func TestSemanticResolver_Integration_EmptyForGibberish(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	if _, err := store.IndexSymbols(ctx, idx, "test-hash-gibberish", nil); err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}
	time.Sleep(2 * time.Second)

	resolver, err := rag.NewSemanticResolver(client, dataSpace, setupEmbedClient(t))
	if err != nil {
		t.Fatalf("NewSemanticResolver: %v", err)
	}

	// "xyzzy quantum fluxcapacitor" should return no meaningful matches.
	results, err := resolver.Resolve(ctx, []string{"xyzzy quantum fluxcapacitor"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Expect either no results or all low-confidence.
	for _, r := range results {
		if r.Confidence > 0.7 {
			t.Errorf("Gibberish query returned high-confidence match: %s → %q (%.2f)",
				r.Raw, r.Resolved, r.Confidence)
		}
	}

	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}

func TestSemanticResolver_Integration_DocCommentImproveMatch(t *testing.T) {
	client := setupWeaviateClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dataSpace := fmt.Sprintf("integration-test-%d", time.Now().UnixNano())

	if err := rag.EnsureCodeSymbolSchema(ctx, client); err != nil {
		t.Fatalf("EnsureCodeSymbolSchema: %v", err)
	}

	store, err := rag.NewSymbolStore(client, dataSpace, nil)
	if err != nil {
		t.Fatalf("NewSymbolStore: %v", err)
	}

	idx := buildTestIndex(t)
	if _, err := store.IndexSymbols(ctx, idx, "test-hash-doccomment", nil); err != nil {
		t.Fatalf("IndexSymbols: %v", err)
	}
	time.Sleep(2 * time.Second)

	resolver, err := rag.NewSemanticResolver(client, dataSpace, setupEmbedClient(t))
	if err != nil {
		t.Fatalf("NewSemanticResolver: %v", err)
	}

	// "rendering pipeline" should match RenderPage (whose doccomment mentions "rendering pipeline").
	results, err := resolver.Resolve(ctx, []string{"rendering pipeline"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	found := false
	for _, r := range results {
		t.Logf("Result: %s → %s %q (confidence: %.2f)", r.Raw, r.Kind, r.Resolved, r.Confidence)
		if r.Resolved == "pkg/render" || r.Resolved == "RenderPage" {
			found = true
		}
	}
	if !found {
		t.Error("Expected 'rendering pipeline' to match RenderPage or pkg/render")
	}

	if err := store.DeleteAll(ctx); err != nil {
		t.Logf("Cleanup failed: %v", err)
	}
}
