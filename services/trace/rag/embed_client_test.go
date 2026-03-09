// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNewEmbedClient_EmptyURL(t *testing.T) {
	_, err := NewEmbedClient("", "model")
	if err == nil {
		t.Error("NewEmbedClient() should error on empty URL")
	}
}

func TestNewEmbedClient_DefaultModel(t *testing.T) {
	c, err := NewEmbedClient("http://localhost:8081", "")
	if err != nil {
		t.Fatalf("NewEmbedClient() error: %v", err)
	}
	if c.model != "nomic-embed-text-v2-moe" {
		t.Errorf("model = %q, want default", c.model)
	}
}

func TestEmbedDocuments_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Prefix != "search_document: " {
			t.Errorf("prefix = %q, want 'search_document: '", req.Prefix)
		}
		if len(req.Inputs) != 2 {
			t.Errorf("inputs count = %d, want 2", len(req.Inputs))
		}

		resp := embedResponse{
			Embeddings: [][]float32{{0.1, 0.2}, {0.3, 0.4}},
			Model:      req.Model,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c, _ := NewEmbedClient(server.URL, "test-model")
	vectors, err := c.EmbedDocuments(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("EmbedDocuments() error: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("vectors count = %d, want 2", len(vectors))
	}
	if vectors[0][0] != 0.1 {
		t.Errorf("vectors[0][0] = %f, want 0.1", vectors[0][0])
	}
}

func TestEmbedDocuments_EmptyInput(t *testing.T) {
	c, _ := NewEmbedClient("http://localhost:8081", "model")
	_, err := c.EmbedDocuments(context.Background(), []string{})
	if err == nil {
		t.Error("EmbedDocuments() should error on empty input")
	}
}

func TestEmbedQuery_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Prefix != "search_query: " {
			t.Errorf("prefix = %q, want 'search_query: '", req.Prefix)
		}
		resp := embedResponse{
			Embeddings: [][]float32{{0.5, 0.6, 0.7}},
			Model:      req.Model,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c, _ := NewEmbedClient(server.URL, "test-model")
	vec, err := c.EmbedQuery(context.Background(), "find rendering functions")
	if err != nil {
		t.Fatalf("EmbedQuery() error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("vector length = %d, want 3", len(vec))
	}
	if vec[0] != 0.5 {
		t.Errorf("vec[0] = %f, want 0.5", vec[0])
	}
}

func TestEmbedQuery_EmptyInput(t *testing.T) {
	c, _ := NewEmbedClient("http://localhost:8081", "model")
	_, err := c.EmbedQuery(context.Background(), "")
	if err == nil {
		t.Error("EmbedQuery() should error on empty input")
	}
}

func TestEmbedDocuments_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "model not loaded"}`))
	}))
	defer server.Close()

	c, _ := NewEmbedClient(server.URL, "test-model")
	_, err := c.EmbedDocuments(context.Background(), []string{"test"})
	if err == nil {
		t.Error("EmbedDocuments() should error on server 500")
	}
}

func TestEmbedDocuments_Batching(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		var req embedRequest
		json.NewDecoder(r.Body).Decode(&req)

		vecs := make([][]float32, len(req.Inputs))
		for i := range vecs {
			vecs[i] = []float32{float32(i)}
		}
		resp := embedResponse{Embeddings: vecs, Model: req.Model}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c, _ := NewEmbedClient(server.URL, "test-model")
	// 250 texts should produce 3 batches (100 + 100 + 50) with embedBatchSize=100.
	texts := make([]string, 250)
	for i := range texts {
		texts[i] = "text"
	}
	vectors, err := c.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatalf("EmbedDocuments() error: %v", err)
	}
	if len(vectors) != 250 {
		t.Errorf("vectors count = %d, want 250", len(vectors))
	}
	if callCount.Load() != 3 {
		t.Errorf("server calls = %d, want 3 (batching)", callCount.Load())
	}
}
