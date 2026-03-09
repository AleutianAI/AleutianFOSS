// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleEmbed_ValidRequest(t *testing.T) {
	// Mock Ollama server.
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OllamaEmbedRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)
		assert.Len(t, req.Input, 2)
		// Verify prefix was applied.
		assert.True(t, strings.HasPrefix(req.Input[0], "search_document: "))
		assert.True(t, strings.HasPrefix(req.Input[1], "search_document: "))

		resp := OllamaEmbedResponse{
			Model:      "test-model",
			Embeddings: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollama.Close()

	t.Setenv("EMBEDDING_SERVICE_URL", ollama.URL)
	t.Setenv("EMBEDDING_MODEL", "test-model")

	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{"inputs": ["hello world", "test input"], "model": "test-model"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp EmbedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "test-model", resp.Model)
	assert.Len(t, resp.Embeddings, 2)
	assert.Equal(t, float32(0.1), resp.Embeddings[0][0])
}

func TestHandleEmbed_EmptyInputs(t *testing.T) {
	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{"inputs": []}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleEmbed_MissingInputs(t *testing.T) {
	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleEmbed_DefaultModelFromEnv(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OllamaEmbedRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "env-model", req.Model)

		resp := OllamaEmbedResponse{
			Model:      "env-model",
			Embeddings: [][]float32{{0.1}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollama.Close()

	t.Setenv("EMBEDDING_SERVICE_URL", ollama.URL)
	t.Setenv("EMBEDDING_MODEL", "env-model")

	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{"inputs": ["test"]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleEmbed_CustomQueryPrefix(t *testing.T) {
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OllamaEmbedRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		// Verify query prefix was applied instead of document prefix.
		assert.True(t, strings.HasPrefix(req.Input[0], "search_query: "), "got: %s", req.Input[0])

		resp := OllamaEmbedResponse{
			Model:      req.Model,
			Embeddings: [][]float32{{0.7, 0.8}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ollama.Close()

	t.Setenv("EMBEDDING_SERVICE_URL", ollama.URL)

	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{"inputs": ["find rendering functions"], "prefix": "search_query: "}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleEmbed_NoEmbeddingServiceURL(t *testing.T) {
	t.Setenv("EMBEDDING_SERVICE_URL", "")

	router := gin.New()
	router.POST("/v1/embed", HandleEmbed())

	body := `{"inputs": ["test"]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/embed", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
