// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// =============================================================================
// Tool Embedding Cache (IT-06c Option I)
// =============================================================================

// toolEmbeddingWarmConcurrency is the number of parallel Ollama calls during warm-up.
// 10 concurrent requests saturates Ollama without overwhelming it.
const toolEmbeddingWarmConcurrency = 10

// toolEmbeddingQueryTimeout is the per-query embedding call timeout.
// Score() is on the hot path; 3 seconds is ample for a local Ollama call.
const toolEmbeddingQueryTimeout = 3 * time.Second

// ollamaEmbedReq is the Ollama /api/embed request body.
type ollamaEmbedReq struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// ollamaEmbedResp is the Ollama /api/embed response body.
type ollamaEmbedResp struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// ToolEmbeddingCache pre-computes and caches embedding vectors for every tool
// at service startup, then uses cosine similarity at query time to score
// how well a user's query matches each tool.
//
// # Description
//
// Embedding-based scoring is semantically robust: "Where is X referenced?"
// and "Where is X used?" produce nearly identical query vectors, both close
// to the find_references tool vector — regardless of exact word form.
//
// The cache calls Ollama's /api/embed endpoint (nomic-embed-text-v2-moe by
// default) in parallel during Warm(). If Ollama is unavailable, the cache
// degrades gracefully: Score() returns (nil, nil) and the hybrid scorer
// falls back to BM25-only mode.
//
// GR-61: Vectors are persisted in BadgerDB (via RouterCacheStore) between
// service restarts. The corpus hash (SHA256 of tool specs + model name)
// serves as the cache key, providing automatic invalidation when the tool
// registry or model changes. If the store is nil, the cache operates in
// in-memory-only mode (no persistence).
//
// # Thread Safety
//
// Safe for concurrent use after Warm() completes.
type ToolEmbeddingCache struct {
	mu      sync.RWMutex
	vectors map[string][]float32 // tool name → unit-normalized embedding vector
	warmed  bool

	url    string // Ollama /api/embed endpoint URL
	model  string // embedding model name
	client *http.Client
	logger *slog.Logger
	store  RouterCacheStore // BadgerDB persistence; nil = in-memory-only
}

// NewToolEmbeddingCache creates an unwarmed embedding cache.
//
// # Description
//
// Reads EMBEDDING_SERVICE_URL and EMBEDDING_MODEL from the environment.
// Call Warm() to pre-compute tool embeddings before the cache can score queries.
//
// GR-61: If store is non-nil, Warm() will check the BadgerDB cache before
// calling Ollama and persist newly computed vectors after warm-up. If store
// is nil, the cache operates in in-memory-only mode — correct for tests and
// for deployments without a routing cache directory configured.
//
// # Inputs
//
//   - logger: Logger for warnings and debug output. Must not be nil.
//   - store: Optional BadgerDB persistence store. Nil disables persistence.
//
// # Outputs
//
//   - *ToolEmbeddingCache: Unwarmed cache. Never nil.
//
// # Thread Safety
//
// The returned cache is safe for concurrent use after Warm() completes.
func NewToolEmbeddingCache(logger *slog.Logger, store RouterCacheStore) *ToolEmbeddingCache {
	if logger == nil {
		logger = slog.Default()
	}

	url := os.Getenv("EMBEDDING_SERVICE_URL")
	if url == "" {
		url = "http://host.containers.internal:11434/api/embed"
	}

	model := os.Getenv("EMBEDDING_MODEL")
	if model == "" {
		model = "nomic-embed-text-v2-moe"
	}

	return &ToolEmbeddingCache{
		vectors: make(map[string][]float32),
		url:     url,
		model:   model,
		client: &http.Client{
			Timeout: 30 * time.Second, // warm-up can be slow; query timeout set per-call
		},
		logger: logger,
		store:  store,
	}
}

// Warm pre-computes and caches an embedding vector for every tool spec.
//
// # Description
//
// Builds an embedding document for each tool (name + keywords + use_when),
// then calls Ollama in parallel (up to toolEmbeddingWarmConcurrency concurrent
// requests). Vectors are stored unit-normalized for efficient cosine similarity.
//
// If any single tool fails to embed, a warning is logged and that tool is
// skipped — it will receive score 0 from Score(). If all tools fail, warmed
// remains false and Score() degrades gracefully.
//
// # Inputs
//
//   - ctx: Context for the warm-up HTTP calls. Cancellation aborts all pending embeds.
//   - specs: Tool specifications to embed. Empty slice is a no-op.
//
// # Outputs
//
//   - error: Non-nil if the Ollama endpoint is completely unreachable. Partial
//     failures (individual tools) are logged as warnings, not returned as errors.
//
// # Thread Safety
//
// Not safe to call concurrently. Call once at service startup.
func (c *ToolEmbeddingCache) Warm(ctx context.Context, specs []ToolSpec) error {
	if len(specs) == 0 {
		return nil
	}

	// GR-61 Step 8: Check BadgerDB cache before calling Ollama.
	// The corpus hash captures all signals that determine vector shape:
	// tool names, BestFor keywords, UseWhen text, and embedding model name.
	// Any change produces a different hash → automatic cache miss → fresh warm-up.
	corpusHash := computeCorpusHash(specs, c.model)
	if c.store != nil {
		cached, err := c.store.LoadEmbeddings(ctx, corpusHash)
		if err != nil {
			c.logger.Warn("embedding cache: store load failed, continuing with Ollama warm-up",
				slog.String("error", err.Error()),
			)
		} else if len(cached) > 0 {
			c.mu.Lock()
			for name, vec := range cached {
				c.vectors[name] = vec // already unit-normalized on save
			}
			c.warmed = true
			c.mu.Unlock()
			c.logger.Info("embedding cache: loaded from BadgerDB (skipping Ollama warm-up)",
				slog.Int("tool_count", len(cached)),
				slog.String("corpus_hash", shortHash(corpusHash)),
			)
			return nil
		}
	}

	c.logger.Info("embedding cache: starting Ollama warm-up",
		slog.Int("tool_count", len(specs)),
		slog.String("url", c.url),
		slog.String("model", c.model),
	)

	type result struct {
		name   string
		vector []float32
	}

	resultCh := make(chan result, len(specs))
	g, gctx := errgroup.WithContext(ctx)

	// Semaphore to limit concurrency.
	sem := make(chan struct{}, toolEmbeddingWarmConcurrency)

	for _, spec := range specs {
		s := spec // capture
		g.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			doc := buildEmbeddingDoc(s)
			vec, err := c.embed(gctx, doc)
			if err != nil {
				c.logger.Warn("embedding cache: failed to embed tool",
					slog.String("tool", s.Name),
					slog.String("error", err.Error()),
				)
				// Individual failure is not fatal.
				return nil
			}

			resultCh <- result{name: s.Name, vector: vec}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("embedding cache warm-up: %w", err)
	}
	close(resultCh)

	c.mu.Lock()
	for r := range resultCh {
		norm := l2Norm(r.vector)
		if norm > 0 {
			// Store unit-normalized vector so cosine = dot product at query time.
			normalized := make([]float32, len(r.vector))
			for i, v := range r.vector {
				normalized[i] = v / float32(norm)
			}
			c.vectors[r.name] = normalized
		}
	}
	c.warmed = len(c.vectors) > 0

	// Capture embeddedCount and snapshot vectors under lock, then release before
	// the BadgerDB write. Avoids holding the lock during a potentially slow I/O
	// call, and ensures the log below reads a consistent value.
	embeddedCount := len(c.vectors)
	var toSave map[string][]float32
	if c.warmed && c.store != nil {
		toSave = make(map[string][]float32, len(c.vectors))
		for k, v := range c.vectors {
			toSave[k] = v
		}
	}
	c.mu.Unlock()

	c.logger.Info("embedding cache: warm-up complete",
		slog.Int("embedded_tools", embeddedCount),
		slog.Int("requested_tools", len(specs)),
	)

	// GR-61: Persist to BadgerDB after releasing the lock.
	// Persistence failure is non-fatal: vectors are already in RAM.
	if toSave != nil {
		if err := c.store.SaveEmbeddings(ctx, corpusHash, toSave); err != nil {
			c.logger.Warn("embedding cache: failed to persist vectors to BadgerDB",
				slog.String("error", err.Error()),
				slog.String("corpus_hash", shortHash(corpusHash)),
			)
		} else {
			c.logger.Debug("embedding cache: persisted vectors to BadgerDB",
				slog.Int("tool_count", len(toSave)),
				slog.String("corpus_hash", shortHash(corpusHash)),
			)
		}
	}

	return nil
}

// Score embeds the query and returns cosine similarity vs each cached tool vector.
//
// # Description
//
// Returns (nil, nil) in two cases where the caller should fall back to BM25:
//  1. The cache was never warmed (Ollama was unavailable at startup).
//  2. The Ollama call for the query embedding fails or times out.
//
// Returns a non-nil map on success. Scores are in [0.0, 1.0]; tools absent
// from the cache have implicit score 0 and are omitted from the map.
//
// # Inputs
//
//   - ctx: Context for cancellation. A per-call timeout is applied internally.
//   - query: The user's raw query string.
//
// # Outputs
//
//   - map[string]float64: Tool name → cosine similarity in [0.0, 1.0].
//     Nil signals graceful degradation — caller should use BM25 only.
//   - error: Always nil. Errors are absorbed and signaled via nil map.
//
// # Thread Safety
//
// Safe for concurrent use after Warm() completes.
func (c *ToolEmbeddingCache) Score(ctx context.Context, query string) (map[string]float64, error) {
	c.mu.RLock()
	warmed := c.warmed
	c.mu.RUnlock()

	if !warmed {
		return nil, nil
	}

	// Apply a tight timeout for the query embedding call.
	embedCtx, cancel := context.WithTimeout(ctx, toolEmbeddingQueryTimeout)
	defer cancel()

	queryVec, err := c.embed(embedCtx, query)
	if err != nil {
		c.logger.Warn("embedding cache: query embedding failed, falling back to BM25",
			slog.String("error", err.Error()),
		)
		return nil, nil
	}

	// Unit-normalize query vector.
	queryNorm := l2Norm(queryVec)
	if queryNorm == 0 {
		return nil, nil
	}
	queryUnit := make([]float32, len(queryVec))
	for i, v := range queryVec {
		queryUnit[i] = v / float32(queryNorm)
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	scores := make(map[string]float64, len(c.vectors))
	for toolName, toolVec := range c.vectors {
		sim := dotProduct(queryUnit, toolVec) // dot of two unit vectors = cosine
		if sim > 0 {
			scores[toolName] = float64(sim)
		}
	}

	return scores, nil
}

// IsWarmed reports whether the cache has been successfully warmed.
//
// # Thread Safety
//
// Safe for concurrent use.
func (c *ToolEmbeddingCache) IsWarmed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.warmed
}

// =============================================================================
// Helpers
// =============================================================================

// buildEmbeddingDoc constructs the text document used to embed a tool.
//
// The document includes the tool name, its keywords (for lexical signal),
// and its use_when text (for semantic signal). AvoidWhen is excluded:
// negative guidance confuses embedding models and reduces signal quality.
func buildEmbeddingDoc(spec ToolSpec) string {
	parts := make([]string, 0, len(spec.BestFor)+2)
	parts = append(parts, spec.Name)
	parts = append(parts, spec.BestFor...)
	if spec.UseWhen != "" {
		parts = append(parts, spec.UseWhen)
	}
	return strings.Join(parts, ". ")
}

// embed calls the Ollama /api/embed endpoint and returns the embedding vector.
func (c *ToolEmbeddingCache) embed(ctx context.Context, text string) ([]float32, error) {
	reqBody, err := json.Marshal(ollamaEmbedReq{
		Model: c.model,
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed HTTP call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed service returned %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp ollamaEmbedResp
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return nil, fmt.Errorf("parse embed response: %w", err)
	}
	if len(ollamaResp.Embeddings) == 0 || len(ollamaResp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embed service returned empty vector")
	}

	return ollamaResp.Embeddings[0], nil
}

// l2Norm computes the L2 (Euclidean) norm of a float32 vector.
func l2Norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// dotProduct computes the dot product of two float32 vectors.
// Both vectors must have the same length; mismatched lengths use the shorter.
func dotProduct(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var sum float32
	for i := 0; i < n; i++ {
		sum += a[i] * b[i]
	}
	return sum
}
