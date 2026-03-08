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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// embedBatchSize is the maximum number of texts to embed in a single request.
// 100 is the sweet spot — Ollama takes ~1s per batch at this size.
// Larger batches (500) cause 15s+ latency due to GPU memory pressure.
const embedBatchSize = 100

// embedConcurrency is the number of concurrent embedding requests.
// 4 workers × 100 per batch: 556 requests / 4 = 139 rounds × ~1s ≈ 2-3 min for 55K symbols.
const embedConcurrency = 4

// embedTimeout is the per-batch timeout for embedding requests.
const embedTimeout = 60 * time.Second

// embedRequest is the wire format for the orchestrator's /v1/embed endpoint.
type embedRequest struct {
	Model  string   `json:"model"`
	Inputs []string `json:"inputs"`
	Prefix string   `json:"prefix"`
}

// embedResponse is the wire format returned by the orchestrator's /v1/embed endpoint.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
}

// EmbedClient calls the orchestrator's /v1/embed endpoint for vector computation.
//
// Description:
//
//	CRS-26i: Routes embedding requests through the orchestrator, which has
//	network access to Ollama on the host. This solves the container networking
//	issue where Weaviate/trace cannot reach Ollama directly.
//
// Thread Safety: Safe for concurrent use after construction.
type EmbedClient struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewEmbedClient creates an EmbedClient for the orchestrator's /v1/embed endpoint.
//
// Inputs:
//
//	orchestratorURL - Base URL of the orchestrator (e.g., "http://orchestrator:8081").
//	  Must not be empty.
//	model - Embedding model name (e.g., "nomic-embed-text-v2-moe").
//	  If empty, defaults to "nomic-embed-text-v2-moe".
//
// Outputs:
//
//	*EmbedClient - The configured client.
//	error - Non-nil if orchestratorURL is empty.
//
// Thread Safety: Safe for concurrent use after construction.
func NewEmbedClient(orchestratorURL, model string) (*EmbedClient, error) {
	if orchestratorURL == "" {
		return nil, errors.New("orchestratorURL must not be empty")
	}
	if model == "" {
		model = "nomic-embed-text-v2-moe"
	}
	return &EmbedClient{
		baseURL:    orchestratorURL,
		model:      model,
		httpClient: &http.Client{Timeout: embedTimeout},
	}, nil
}

// EmbedDocuments returns vectors for document texts using "search_document: " prefix.
//
// Description:
//
//	Batches texts in chunks of 100 to avoid OOM. Each batch is sent to the
//	orchestrator's /v1/embed endpoint with the "search_document: " prefix,
//	which is required by nomic-embed-text models for document indexing.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	texts - Document texts to embed. Must not be empty.
//
// Outputs:
//
//	[][]float32 - One vector per input text, in the same order.
//	error - Non-nil if the request fails or texts is empty.
//
// Thread Safety: Safe for concurrent use.
func (c *EmbedClient) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	ctx, span := tracer.Start(ctx, "rag.EmbedClient.EmbedDocuments")
	defer span.End()
	span.SetAttributes(attribute.Int("embed.text_count", len(texts)))

	if len(texts) == 0 {
		span.SetStatus(codes.Error, "empty input")
		return nil, errors.New("texts must not be empty")
	}

	// Build batch ranges.
	type batchRange struct {
		index int // position in results slice
		start int
		end   int
	}
	var batches []batchRange
	for i := 0; i < len(texts); i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batches = append(batches, batchRange{index: len(batches), start: i, end: end})
	}

	// Run batches concurrently with bounded parallelism.
	results := make([][]float32, len(texts))
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, embedConcurrency)
	var wg sync.WaitGroup

	for _, b := range batches {
		if ctx.Err() != nil {
			break
		}
		mu.Lock()
		if firstErr != nil {
			mu.Unlock()
			break
		}
		mu.Unlock()

		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func(br batchRange) {
			defer wg.Done()
			defer func() { <-sem }() // release slot

			vectors, err := c.embed(ctx, texts[br.start:br.end], "search_document: ")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("embedding %d texts (batch offset %d): %w", len(texts), br.start, err)
				}
				return
			}
			// Place vectors at correct positions.
			for j, vec := range vectors {
				results[br.start+j] = vec
			}
		}(b)
	}
	wg.Wait()

	if firstErr != nil {
		span.RecordError(firstErr)
		span.SetStatus(codes.Error, firstErr.Error())
		return nil, firstErr
	}

	span.SetAttributes(attribute.Int("embed.vector_count", len(results)))
	return results, nil
}

// EmbedQuery returns a vector for a query text using "search_query: " prefix.
//
// Description:
//
//	Used at query time by the SemanticResolver to embed the user's search
//	tokens before running nearVector queries against Weaviate.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	query - Query text to embed. Must not be empty.
//
// Outputs:
//
//	[]float32 - The query embedding vector.
//	error - Non-nil if the request fails or query is empty.
//
// Thread Safety: Safe for concurrent use.
func (c *EmbedClient) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	ctx, span := tracer.Start(ctx, "rag.EmbedClient.EmbedQuery")
	defer span.End()

	if query == "" {
		span.SetStatus(codes.Error, "empty query")
		return nil, errors.New("query must not be empty")
	}

	vectors, err := c.embed(ctx, []string{query}, "search_query: ")
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	if len(vectors) == 0 {
		return nil, errors.New("embedding returned no vectors")
	}

	return vectors[0], nil
}

// embed sends a batch of texts to the orchestrator's /v1/embed endpoint.
func (c *EmbedClient) embed(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	reqBody := embedRequest{
		Model:  c.model,
		Inputs: texts,
		Prefix: prefix,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling embed request: %w", err)
	}

	url := c.baseURL + "/v1/embed"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("creating embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling orchestrator /v1/embed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading embed response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("orchestrator /v1/embed returned status %d: %s", resp.StatusCode, string(body))
	}

	var embedResp embedResponse
	if err := json.Unmarshal(body, &embedResp); err != nil {
		return nil, fmt.Errorf("decoding embed response: %w", err)
	}

	if len(embedResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: expected %d, got %d", len(texts), len(embedResp.Embeddings))
	}

	return embedResp.Embeddings, nil
}
