// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/rag"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

// SymbolIndexingCoordinator manages background symbol indexing into Weaviate.
//
// Description:
//
//	CRS-26l: Coordinates eager symbol indexing so that it can be triggered
//	from both HandleInit (graph build time) and the deps factory (session
//	creation time). Uses a content-aware graph hash to skip re-indexing
//	when the graph hasn't changed.
//
// Thread Safety: Safe for concurrent use. Internal mutex guards state.
type SymbolIndexingCoordinator struct {
	weaviateClient    *weaviate.Client
	weaviateDataSpace string
	embedClient       *rag.EmbedClient
	mu                sync.Mutex
	activeHash        string                 // hash currently being indexed, empty when idle
	lastIndexedHash   string                 // hash of the last successfully indexed graph
	lastStore         *rag.SymbolStore       // cached after successful indexing
	progress          IndexingStatusResponse // live progress state
	cancelFn          context.CancelFunc     // CRS-26n: cancel in-progress indexing
}

// NewSymbolIndexingCoordinator creates a new coordinator for background symbol indexing.
//
// Description:
//
//	CRS-26l: Creates a coordinator that manages the lifecycle of symbol indexing
//	goroutines. The coordinator ensures only one indexing operation runs at a time
//	and caches the resulting SymbolStore for reuse.
//
// Inputs:
//
//	client    - Weaviate client for symbol storage. Must not be nil.
//	dataSpace - Project isolation key for Weaviate collections.
//	embedClient - Embedding client for vector computation. Must not be nil.
//
// Outputs:
//
//	*SymbolIndexingCoordinator - The initialized coordinator.
//
// Thread Safety: The returned coordinator is safe for concurrent use.
func NewSymbolIndexingCoordinator(client *weaviate.Client, dataSpace string, embedClient *rag.EmbedClient) *SymbolIndexingCoordinator {
	return &SymbolIndexingCoordinator{
		weaviateClient:    client,
		weaviateDataSpace: dataSpace,
		embedClient:       embedClient,
	}
}

// TriggerIndexing starts background symbol indexing for the given graph.
//
// Description:
//
//	CRS-26l: Fire-and-forget goroutine that indexes symbols from the graph's
//	SymbolIndex into Weaviate. Uses a content-aware hash to skip if already
//	indexed. Only one indexing operation runs at a time — concurrent calls
//	with the same hash are deduplicated.
//
// Inputs:
//
//	graphID - The graph identifier (used for hash computation).
//	cached  - The cached graph containing the SymbolIndex. Must not be nil.
//
// Thread Safety: Safe for concurrent use. Guards against concurrent indexing.
func (c *SymbolIndexingCoordinator) TriggerIndexing(graphID string, cached *CachedGraph) {
	if cached == nil || cached.Index == nil {
		return
	}

	preHash := computeGraphContentHash(graphID, cached)

	c.mu.Lock()
	if c.activeHash == preHash {
		c.mu.Unlock()
		slog.Debug("CRS-26l: Indexing already in progress for this graph",
			slog.String("graph_hash", preHash))
		return
	}
	if c.lastIndexedHash == preHash {
		c.mu.Unlock()
		slog.Debug("CRS-26l: Symbols already indexed for this graph",
			slog.String("graph_hash", preHash))
		return
	}
	// CRS-26n: Create cancellable context before launching the goroutine so
	// CancelIndexing() can stop it immediately — no race window where cancelFn is nil.
	// CRS-26i: 55K+ exported symbols × 100-per-batch embedding calls need ~10min.
	indexCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	c.activeHash = preHash
	c.cancelFn = cancel
	c.mu.Unlock()

	go func() {
		// CR-6: Recover from panics to prevent crashing the server.
		defer func() {
			if r := recover(); r != nil {
				slog.Error("CRS-26l: Panic in symbol indexing goroutine",
					slog.Any("panic", r),
				)
			}
		}()

		defer func() {
			cancel() // ensure context resources are freed
			c.mu.Lock()
			c.activeHash = ""
			c.cancelFn = nil
			c.mu.Unlock()
		}()

		store, storeErr := rag.NewSymbolStore(c.weaviateClient, c.weaviateDataSpace, c.embedClient)
		if storeErr != nil {
			slog.Warn("CRS-26l: Failed to create symbol store",
				slog.String("error", storeErr.Error()))
			return
		}

		hasHash, hashErr := store.HasGraphHash(indexCtx, preHash)
		if hashErr != nil {
			slog.Warn("CRS-26l: Failed to check graph hash",
				slog.String("error", hashErr.Error()))
			return
		}
		if hasHash {
			slog.Debug("CRS-26l: Symbols already indexed for this graph (Weaviate check)",
				slog.String("graph_hash", preHash))
			c.mu.Lock()
			c.lastStore = store
			c.lastIndexedHash = preHash
			c.mu.Unlock()
			return
		}

		// CRS-26n: Set "deleting" phase so the stack manager's progress poll
		// reflects that stale data is being cleared.
		c.mu.Lock()
		c.progress = IndexingStatusResponse{
			InProgress: true,
			Phase:      "deleting",
		}
		c.mu.Unlock()

		// Delete stale symbols and re-index.
		if delErr := store.DeleteAll(indexCtx); delErr != nil {
			slog.Warn("CRS-26l: Failed to delete stale symbols",
				slog.String("error", delErr.Error()))
		}

		onProgress := func(phase string, completed, total, symbolsTotal int) {
			c.mu.Lock()
			c.progress = IndexingStatusResponse{
				InProgress:       true,
				Phase:            phase,
				SymbolsTotal:     symbolsTotal,
				BatchesCompleted: completed,
				BatchesTotal:     total,
			}
			c.mu.Unlock()
		}

		count, idxErr := store.IndexSymbols(indexCtx, cached.Index, preHash, onProgress)
		if idxErr != nil {
			slog.Warn("CRS-26l: Symbol indexing failed",
				slog.String("error", idxErr.Error()))
			c.mu.Lock()
			c.progress = IndexingStatusResponse{
				InProgress: false,
				Phase:      "complete",
				Error:      idxErr.Error(),
			}
			c.mu.Unlock()
		} else {
			c.mu.Lock()
			c.lastStore = store
			c.lastIndexedHash = preHash
			c.progress = IndexingStatusResponse{
				InProgress:     false,
				Phase:          "complete",
				SymbolsTotal:   count,
				SymbolsIndexed: count,
			}
			c.mu.Unlock()
			slog.Info("CRS-26l: Symbols indexed into Weaviate",
				slog.Int("count", count),
				slog.String("graph_hash", preHash),
			)
		}
	}()
}

// CancelIndexing cancels any in-progress indexing goroutine via context.
//
// Description:
//
//	CRS-26n: Called before re-triggering indexing on project switch to stop
//	the old goroutine. The goroutine's context is cancelled, which causes
//	Weaviate batch operations and embedding calls to abort promptly.
//
// Thread Safety: Safe for concurrent use.
func (c *SymbolIndexingCoordinator) CancelIndexing() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
		slog.Info("CRS-26n: Cancelled in-progress indexing",
			slog.String("active_hash", c.activeHash))
	}
}

// ResetState clears lastIndexedHash, lastStore, and progress so the next
// TriggerIndexing doesn't short-circuit on a stale hash.
//
// Description:
//
//	CRS-26n: Called after CancelIndexing when switching projects. Without
//	this, a project with an identical hash to the previous one would be
//	skipped because lastIndexedHash still matches.
//
// Thread Safety: Safe for concurrent use.
func (c *SymbolIndexingCoordinator) ResetState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastIndexedHash = ""
	c.lastStore = nil
	c.activeHash = ""
	c.progress = IndexingStatusResponse{}
	slog.Info("CRS-26n: Indexing coordinator state reset")
}

// GetProgress returns a snapshot of the current indexing progress.
//
// Description:
//
//	Returns the latest IndexingStatusResponse under lock. Used by the
//	HandleIndexingStatus endpoint for the trace-proxy to poll.
//
// Outputs:
//
//	IndexingStatusResponse - A copy of the current progress state.
//
// Thread Safety: Safe for concurrent use.
func (c *SymbolIndexingCoordinator) GetProgress() IndexingStatusResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.progress
}

// GetSymbolStore returns the cached SymbolStore from the last successful indexing.
//
// Description:
//
//	CRS-26l: Returns the SymbolStore populated by the most recent successful
//	TriggerIndexing call. Returns nil if indexing hasn't completed yet.
//
// Outputs:
//
//	*rag.SymbolStore - The cached store, or nil if not yet available.
//
// Thread Safety: Safe for concurrent use.
func (c *SymbolIndexingCoordinator) GetSymbolStore() *rag.SymbolStore {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastStore
}
