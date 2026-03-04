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
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
)

// CombinedResolver chains structural (Layer 1) and semantic (Layer 2) resolution.
//
// Description:
//
//	First runs the StructuralResolver for O(1) exact matches. Then passes
//	unresolved tokens to the SemanticResolver for nearText vector search.
//	If Weaviate is unavailable, gracefully degrades to structural-only.
//
// Thread Safety: Safe for concurrent use after construction.
type CombinedResolver struct {
	structural *StructuralResolver
	semantic   *SemanticResolver
}

// NewCombinedResolver creates a resolver that chains structural and semantic layers.
//
// Inputs:
//
//	structural - Layer 1 resolver. Must not be nil.
//	semantic - Layer 2 resolver. May be nil (degrades to structural-only).
//
// Outputs:
//
//	*CombinedResolver - Ready to resolve queries.
//
// Thread Safety: Safe for concurrent use after construction.
func NewCombinedResolver(structural *StructuralResolver, semantic *SemanticResolver) *CombinedResolver {
	return &CombinedResolver{
		structural: structural,
		semantic:   semantic,
	}
}

// Resolve runs both resolution layers and returns merged results.
//
// Description:
//
//  1. Structural resolution (O(1), ~0ms): exact name matching.
//
//  2. Semantic resolution (~5-15ms): nearText vector search for remaining tokens.
//
//  3. Merge results, deduplicating by resolved value.
//
//     If the semantic resolver is nil or Weaviate is unavailable, only
//     structural results are returned (graceful degradation).
//
// Inputs:
//
//	ctx - Context for cancellation and tracing.
//	query - Natural language query.
//
// Outputs:
//
//	*ExtractionContext - Merged resolved entities from both layers.
//
// Thread Safety: Safe for concurrent use.
func (c *CombinedResolver) Resolve(ctx context.Context, query string) *ExtractionContext {
	ctx, span := tracer.Start(ctx, "rag.CombinedResolver.Resolve")
	defer span.End()

	// Layer 1: Structural (always runs).
	ec, detail := c.structural.ResolveDetailed(ctx, query)

	// Layer 2: Semantic (only if available and there are unresolved tokens).
	if c.semantic != nil && len(detail.UnresolvedTokens) > 0 {
		if c.semantic.IsAvailable(ctx) {
			semanticEntities, err := c.semantic.Resolve(ctx, detail.UnresolvedTokens, detail.ResolvedSet)
			if err != nil {
				slog.Warn("CRS-25: Semantic resolution failed, using structural-only",
					slog.String("error", err.Error()),
				)
			} else if len(semanticEntities) > 0 {
				ec.ResolvedEntities = append(ec.ResolvedEntities, semanticEntities...)
			}
		} else {
			slog.Debug("CRS-25: Weaviate unavailable, using structural-only")
		}
	}

	span.SetAttributes(
		attribute.Int("rag.total_resolved", len(ec.ResolvedEntities)),
	)

	return ec
}

// Structural returns the underlying structural resolver for direct access.
func (c *CombinedResolver) Structural() *StructuralResolver {
	return c.structural
}
