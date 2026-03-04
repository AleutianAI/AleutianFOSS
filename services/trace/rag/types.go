// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package rag provides graph-backed entity resolution for parameter extraction.
//
// The resolver translates natural language entity references (e.g., "rendering subsystem")
// into concrete graph entities (e.g., package "pkg/render") before the parameter extraction
// model sees them. This reduces the extraction model's job from "reason about the codebase"
// to "copy confirmed values into JSON fields."
//
// Three resolution layers:
//   - Structural (in-memory, O(1)): exact name match against SymbolIndex + package names
//   - Semantic (Weaviate, ~5-15ms): vector similarity search (Phase 2, CRS-25)
//   - Behavioral (CRS history): cross-session learned constraints (Phase 3, CRS-25)
package rag

import "context"

type contextKey struct{}

// WithExtractionContext attaches resolved entities to a context for use by the param extractor.
//
// Description:
//
//	The execute phase calls this after resolving entities, before passing the
//	context to ExtractParams. The param extractor retrieves it via
//	ExtractionContextFromCtx and injects resolved entities into the LLM prompt.
//
// Inputs:
//
//	ctx - Parent context.
//	ec - Resolved entities and graph metadata.
//
// Outputs:
//
//	context.Context - Context carrying the extraction context.
//
// Thread Safety: Safe for concurrent use (context is immutable).
func WithExtractionContext(ctx context.Context, ec *ExtractionContext) context.Context {
	return context.WithValue(ctx, contextKey{}, ec)
}

// ExtractionContextFromCtx retrieves resolved entities from a context.
//
// Outputs:
//
//	*ExtractionContext - The resolved entities, or nil if not set.
//
// Thread Safety: Safe for concurrent use.
func ExtractionContextFromCtx(ctx context.Context) *ExtractionContext {
	if ec, ok := ctx.Value(contextKey{}).(*ExtractionContext); ok {
		return ec
	}
	return nil
}

// Resolver is the interface for RAG entity resolution.
//
// Both StructuralResolver (Layer 1) and CombinedResolver (Layers 1+2) implement this.
// The execute phase depends on this interface, not concrete types.
type Resolver interface {
	// Resolve resolves a query into grounded entities from the code graph.
	//
	// Inputs:
	//   ctx - Context for cancellation and tracing.
	//   query - Natural language query.
	//
	// Outputs:
	//   *ExtractionContext - Resolved entities and graph metadata.
	Resolve(ctx context.Context, query string) *ExtractionContext
}

// ResolvedEntity represents a query token resolved against the code graph.
type ResolvedEntity struct {
	// Raw is the original token from the query (e.g., "materials").
	Raw string `json:"raw"`

	// Kind classifies the entity: "package", "symbol", "file".
	Kind string `json:"kind"`

	// Resolved is the concrete graph entity (e.g., "pkg/materials").
	Resolved string `json:"resolved"`

	// Confidence is how certain the resolution is (0.0 to 1.0).
	// Structural matches get 1.0, semantic matches get the vector similarity score.
	Confidence float64 `json:"confidence"`

	// Candidates lists alternative matches when resolution is ambiguous.
	Candidates []string `json:"candidates,omitempty"`

	// Layer indicates which resolution layer produced this match.
	// "structural", "semantic", or "behavioral".
	Layer string `json:"layer"`
}

// ExtractionContext provides resolved entities and graph metadata to the parameter extractor.
// This is the bridge between the RAG resolver and the LLM extraction prompt.
type ExtractionContext struct {
	// ResolvedEntities are query tokens resolved against the graph.
	ResolvedEntities []ResolvedEntity `json:"resolved_entities,omitempty"`

	// PackageNames are available packages in the graph (for LLM prompt grounding).
	// Limited to top packages by symbol count to avoid prompt bloat.
	PackageNames []string `json:"package_names,omitempty"`

	// SymbolCount is the total number of symbols in the graph.
	SymbolCount int `json:"symbol_count"`
}
