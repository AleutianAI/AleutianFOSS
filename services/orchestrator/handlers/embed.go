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
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// EmbedRequest represents a request to compute embeddings via the orchestrator.
//
// Description:
//
//	Wraps the Ollama /api/embed endpoint, allowing internal services (e.g., trace)
//	to compute embeddings without direct access to Ollama. The orchestrator runs
//	on the host network and can reach Ollama, while containerized services cannot.
//
// Fields:
//
//   - Model: Embedding model name. If empty, uses EMBEDDING_MODEL env var.
//   - Inputs: Text strings to embed. At least one required.
//   - Prefix: Nomic model prefix. Defaults to "search_document: " if empty.
//     Use "search_query: " for query-time embedding.
type EmbedRequest struct {
	Model  string   `json:"model"`
	Inputs []string `json:"inputs" binding:"required,min=1"`
	Prefix string   `json:"prefix"`
}

// EmbedResponse contains the computed embedding vectors.
//
// Fields:
//
//   - Embeddings: One vector per input text, in the same order.
//   - Model: The model used for embedding.
type EmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
}

// HandleEmbed returns a handler that computes embeddings via the Ollama backend.
//
// Description:
//
//	Thin wrapper around callOllamaEmbed() exposed as POST /v1/embed.
//	Reads EMBEDDING_SERVICE_URL and EMBEDDING_MODEL from environment.
//	Applies the specified prefix (or default "search_document: ") to each input
//	before calling Ollama, matching the nomic-embed-text model's requirements.
//
// Inputs:
//
//	None (reads configuration from environment variables).
//
// Outputs:
//
//	gin.HandlerFunc - The HTTP handler.
//
// Thread Safety: Safe for concurrent use.
func HandleEmbed() gin.HandlerFunc {
	embedTracer := otel.Tracer("aleutian.orchestrator.handlers")
	return func(c *gin.Context) {
		_, span := embedTracer.Start(c.Request.Context(), "handlers.HandleEmbed")
		defer span.End()

		var req EmbedRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			span.SetStatus(codes.Error, "invalid request")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: inputs is required and must not be empty"})
			return
		}

		span.SetAttributes(attribute.Int("embed.input_count", len(req.Inputs)))

		embeddingServiceURL := os.Getenv("EMBEDDING_SERVICE_URL")
		if embeddingServiceURL == "" {
			slog.Error("CRS-26g: EMBEDDING_SERVICE_URL not set")
			span.SetStatus(codes.Error, "EMBEDDING_SERVICE_URL not set")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "embedding service not configured"})
			return
		}

		model := req.Model
		if model == "" {
			model = os.Getenv("EMBEDDING_MODEL")
			if model == "" {
				model = "nomic-embed-text-v2-moe"
			}
		}
		span.SetAttributes(attribute.String("embed.model", model))

		prefix := req.Prefix
		if prefix == "" {
			prefix = NomicDocumentPrefix
		}
		span.SetAttributes(attribute.String("embed.prefix", prefix))

		// Apply prefix to inputs.
		prefixed := make([]string, len(req.Inputs))
		for i, input := range req.Inputs {
			prefixed[i] = prefix + input
		}

		// Call Ollama directly (bypass callOllamaEmbed which hardcodes document prefix).
		ollamaReq := OllamaEmbedRequest{
			Model: model,
			Input: prefixed,
		}
		embeddings, err := callOllamaEmbedInternal(embeddingServiceURL, ollamaReq)
		if err != nil {
			slog.Error("CRS-26g: Embedding failed",
				slog.String("error", err.Error()),
				slog.Int("input_count", len(req.Inputs)),
			)
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		span.SetAttributes(attribute.Int("embed.output_count", len(embeddings)))
		c.JSON(http.StatusOK, EmbedResponse{
			Embeddings: embeddings,
			Model:      model,
		})
	}
}
