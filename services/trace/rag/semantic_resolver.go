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
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
	"go.opentelemetry.io/otel/attribute"
)

// availabilityTTL is how long a cached IsAvailable result is valid.
const availabilityTTL = 30 * time.Second

// maxSemanticResults is the number of Weaviate nearVector results to fetch per query token.
const maxSemanticResults = 5

// minSemanticDistance is the maximum distance (lower = more similar) to accept.
// Weaviate cosine distance: 0 = identical, 2 = opposite.
const minSemanticDistance float32 = 0.6

// SemanticResolver resolves query tokens against Weaviate's vector index.
//
// This is Layer 2 of the three-layer RAG resolution. It handles fuzzy/conceptual
// matches like "rendering subsystem" → "pkg/render" using pre-computed embeddings.
// CRS-26j: Uses nearVector queries with vectors from the orchestrator's /v1/embed
// endpoint, replacing the previous nearText approach that required Weaviate to
// reach Ollama directly (broken in containers).
//
// Thread Safety: Safe for concurrent use after construction.
type SemanticResolver struct {
	client    *weaviate.Client
	dataSpace string
	embedder  *EmbedClient

	// availableCache caches the result of IsAvailable to avoid per-query network calls.
	// Uses atomic int64 for the timestamp and int32 for the result (0=unavailable, 1=available).
	availableCachedAt atomic.Int64
	availableCached   atomic.Int32
}

// NewSemanticResolver creates a resolver backed by Weaviate nearVector search.
//
// Inputs:
//
//	client - Weaviate client. Must not be nil.
//	dataSpace - Project isolation key. Must not be empty.
//	embedder - Embedding client for query vectorization. Must not be nil.
//
// Outputs:
//
//	*SemanticResolver - Ready to resolve queries.
//	error - Non-nil if client is nil, dataSpace is empty, or embedder is nil.
//
// Thread Safety: Safe for concurrent use after construction.
func NewSemanticResolver(client *weaviate.Client, dataSpace string, embedder *EmbedClient) (*SemanticResolver, error) {
	if client == nil {
		return nil, errors.New("client must not be nil")
	}
	if dataSpace == "" {
		return nil, errors.New("dataSpace must not be empty")
	}
	if embedder == nil {
		return nil, errors.New("embedder must not be nil")
	}
	return &SemanticResolver{
		client:    client,
		dataSpace: dataSpace,
		embedder:  embedder,
	}, nil
}

// Resolve runs nearVector search for unresolved tokens and returns additional entities.
//
// Description:
//
//	Takes the tokens that the StructuralResolver couldn't resolve and searches
//	Weaviate for semantically similar code symbols. Returns resolved entities
//	with confidence based on cosine distance.
//
//	Only returns results above the minimum similarity threshold. Results are
//	deduplicated against entities already resolved by the structural layer.
//
//	Individual token search failures are logged and skipped — the function
//	returns partial results rather than failing entirely. The returned error
//	is always nil in the current implementation (reserved for future use).
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	unresolvedTokens - Tokens that the structural resolver couldn't match.
//	alreadyResolved - Set of resolved values from structural resolution (for dedup).
//
// Outputs:
//
//	[]ResolvedEntity - Semantically resolved entities (may be partial on per-token failures).
//	error - Currently always nil. Individual token failures are logged, not propagated.
//
// Thread Safety: Safe for concurrent use.
func (r *SemanticResolver) Resolve(ctx context.Context, unresolvedTokens []string, alreadyResolved map[string]bool) ([]ResolvedEntity, error) {
	ctx, span := tracer.Start(ctx, "rag.SemanticResolver.Resolve")
	defer span.End()
	span.SetAttributes(attribute.Int("rag.unresolved_tokens", len(unresolvedTokens)))

	if len(unresolvedTokens) == 0 {
		return nil, nil
	}

	// Build dataSpace filter.
	where := filters.Where().
		WithPath([]string{"dataSpace"}).
		WithOperator(filters.Equal).
		WithValueString(r.dataSpace)

	// Fields to retrieve.
	fields := []graphql.Field{
		{Name: "symbolId"},
		{Name: "name"},
		{Name: "kind"},
		{Name: "packagePath"},
		{Name: "filePath"},
		{Name: "exported"},
		{Name: "_additional", Fields: []graphql.Field{
			{Name: "distance"},
		}},
	}

	var resolved []ResolvedEntity

	// Search for each unresolved token.
	for _, token := range unresolvedTokens {
		if ctx.Err() != nil {
			break
		}

		// CRS-26j: Embed the query token via orchestrator, then use nearVector.
		queryVec, err := r.embedder.EmbedQuery(ctx, token)
		if err != nil {
			slog.Warn("CRS-26j: Failed to embed query token",
				slog.String("token", token),
				slog.String("error", err.Error()),
			)
			continue
		}

		nearVector := r.client.GraphQL().NearVectorArgBuilder().
			WithVector(queryVec).
			WithDistance(minSemanticDistance)

		result, err := r.client.GraphQL().Get().
			WithClassName(CodeSymbolClassName).
			WithFields(fields...).
			WithWhere(where).
			WithNearVector(nearVector).
			WithLimit(maxSemanticResults).
			Do(ctx)
		if err != nil {
			slog.Warn("CRS-26j: Semantic search failed for token",
				slog.String("token", token),
				slog.String("error", err.Error()),
			)
			continue
		}

		if result.Errors != nil && len(result.Errors) > 0 {
			for _, e := range result.Errors {
				slog.Warn("CRS-26j: Semantic search GraphQL error",
					slog.String("token", token),
					slog.String("error", e.Message),
				)
			}
			continue
		}

		entities := r.parseResults(token, result, alreadyResolved)
		resolved = append(resolved, entities...)
	}

	span.SetAttributes(attribute.Int("rag.semantic_resolved", len(resolved)))
	if len(resolved) > 0 {
		slog.Info("CRS-26j: Semantic resolution",
			slog.Int("tokens", len(unresolvedTokens)),
			slog.Int("resolved", len(resolved)),
		)
	}

	return resolved, nil
}

// parseResults extracts ResolvedEntity values from a Weaviate GraphQL response.
func (r *SemanticResolver) parseResults(token string, result *models.GraphQLResponse, alreadyResolved map[string]bool) []ResolvedEntity {
	if result.Data == nil {
		return nil
	}

	get, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return nil
	}

	objects, ok := get[CodeSymbolClassName].([]interface{})
	if !ok {
		return nil
	}

	var entities []ResolvedEntity
	for _, obj := range objects {
		props, ok := obj.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := props["name"].(string)
		kind, _ := props["kind"].(string)
		pkg, _ := props["packagePath"].(string)

		// Build resolved value.
		resolvedValue := name
		if pkg != "" {
			resolvedValue = pkg + "." + name
		}

		// Skip if already resolved by structural layer.
		if alreadyResolved[resolvedValue] || alreadyResolved[name] || alreadyResolved[pkg] {
			continue
		}

		// Extract distance from _additional.
		distance := 1.0
		if additional, ok := props["_additional"].(map[string]interface{}); ok {
			if d, ok := additional["distance"].(float64); ok {
				distance = d
			}
		}

		// Convert distance to confidence: 0 distance = 1.0 confidence, 0.6 distance = 0.4 confidence.
		confidence := 1.0 - distance
		if confidence < 0.4 {
			continue
		}

		entities = append(entities, ResolvedEntity{
			Raw:        token,
			Kind:       kind,
			Resolved:   resolvedValue,
			Confidence: confidence,
			Layer:      "semantic",
		})
	}

	return entities
}

// IsAvailable checks if the semantic resolver can serve queries.
//
// Description:
//
//	Returns false if Weaviate is unavailable, allowing callers to skip
//	semantic resolution and fall back to structural-only. Results are
//	cached for 30 seconds to avoid per-query network health checks.
//
// Inputs:
//
//	ctx - Context for cancellation.
//
// Outputs:
//
//	bool - True if Weaviate is reachable.
//
// Thread Safety: Safe for concurrent use.
func (r *SemanticResolver) IsAvailable(ctx context.Context) bool {
	cachedAt := r.availableCachedAt.Load()
	if cachedAt > 0 && time.Since(time.UnixMilli(cachedAt)) < availabilityTTL {
		return r.availableCached.Load() == 1
	}

	isReady, err := r.client.Misc().ReadyChecker().Do(ctx)
	if err != nil {
		r.availableCached.Store(0)
		r.availableCachedAt.Store(time.Now().UnixMilli())
		return false
	}

	if isReady {
		r.availableCached.Store(1)
	} else {
		r.availableCached.Store(0)
	}
	r.availableCachedAt.Store(time.Now().UnixMilli())
	return isReady
}
