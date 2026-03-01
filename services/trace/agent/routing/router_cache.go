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

// =============================================================================
// RouterCacheStore — Embedding Persistence (GR-61 State Management)
// =============================================================================
//
// Tool embedding vectors are expensive to compute (~300ms for 30 tools via
// Ollama) but change only when the tool registry or embedding model changes.
// This store persists them in BadgerDB between service restarts.
//
// Design choices:
//
//	1. BadgerDB (not Weaviate): Tool routing vectors are service infrastructure,
//	   not user data. Weaviate is designed for ANN search over millions of user
//	   documents; doing a lookup of 30 pre-computed vectors does not benefit from
//	   HNSW indexing. BadgerDB is embedded — no network call, no availability
//	   dependency, ~100µs access latency.
//
//	2. Corpus hash as cache key: SHA256(sorted tool specs + model name). Any
//	   change to tool names, keywords, or UseWhen text produces a different hash,
//	   automatically invalidating the cached vectors. No explicit invalidation
//	   API is needed — just delete the ROUTING_CACHE_DIR directory.
//
//	3. BadgerDB native TTL: 7-day expiry is enforced by BadgerDB's GC, not by
//	   application code. No metadata record is needed; expired keys return
//	   ErrKeyNotFound, which the store treats as a cache miss.
//
//	4. BM25 index is NOT persisted: it rebuilds from specs in <1ms and has no
//	   network dependency. Persisting it would add complexity with no benefit.
//
// Storage layout:
//
//	routing/emb/v1/{corpusHash}  →  gob-encoded map[string][]float32
//	                                 (tool name → unit-normalized vector)
//	                                 TTL: 7 days

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	dgbadger "github.com/dgraph-io/badger/v4"

	badgerstore "github.com/AleutianAI/AleutianFOSS/services/trace/storage/badger"
)

// routerCacheDefaultTTL is the default lifetime of a cached embedding entry.
// 7 days is long enough to survive weekends and short deployments without
// accumulating stale data indefinitely.
const routerCacheDefaultTTL = 7 * 24 * time.Hour

// routerCacheKeyPrefix is prepended to the corpus hash to form the BadgerDB key.
// Versioned (v1) to allow future format changes without collision.
const routerCacheKeyPrefix = "routing/emb/v1/"

// errCacheMiss is a sentinel used internally to distinguish "key not found"
// (a normal cache miss) from a genuine storage error in LoadEmbeddings.
var errCacheMiss = errors.New("cache miss")

// =============================================================================
// RouterCacheStore Interface
// =============================================================================

// RouterCacheStore persists tool embedding vectors across service restarts.
//
// # Description
//
// The store is keyed by corpus hash — a SHA256 digest of all tool names,
// keywords, and use_when text plus the embedding model name. Any change to
// the tool registry or model automatically produces a different hash, so the
// previous entry becomes unreachable (expires via TTL) without explicit
// invalidation.
//
// Both methods are nil-safe: the PreFilter and ToolEmbeddingCache check for
// a nil RouterCacheStore and skip persistence, operating in in-memory-only
// mode. This is the correct behavior for tests and for deployments that do
// not configure a routing cache directory.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type RouterCacheStore interface {
	// LoadEmbeddings retrieves cached unit-normalized tool embedding vectors
	// for the given corpus hash.
	//
	// Returns (nil, nil) on cache miss (key absent or TTL expired).
	// Returns (nil, error) on storage failure.
	// Returns (vectors, nil) on cache hit; vectors is never empty on success.
	LoadEmbeddings(ctx context.Context, corpusHash string) (map[string][]float32, error)

	// SaveEmbeddings persists unit-normalized tool embedding vectors for the
	// given corpus hash. The store applies a 7-day TTL automatically.
	//
	// Returns non-nil error only on storage failure. The caller logs the error
	// as a warning and continues — persistence failure is non-fatal; vectors
	// will be recomputed on the next service restart.
	SaveEmbeddings(ctx context.Context, corpusHash string, vectors map[string][]float32) error
}

// =============================================================================
// BadgerRouterCacheStore
// =============================================================================

// BadgerRouterCacheStore implements RouterCacheStore backed by a BadgerDB
// instance. The DB is expected to be a service-global singleton opened at
// startup with its own path, separate from per-project CRS journals.
//
// # Description
//
// Vectors are gob-encoded as map[string][]float32. Encoding is compact
// (~4 bytes/float32; 30 tools × 768 dims ≈ 90KB) and fast (~5µs
// encode/decode). The key is the corpus hash prefixed with the storage
// layout version string.
//
// TTL is enforced by BadgerDB's native GC — no application-level expiry
// check is needed. Expired keys return ErrKeyNotFound, which this store
// treats as a cache miss.
//
// # Thread Safety
//
// Safe for concurrent use. BadgerDB transactions are per-goroutine.
type BadgerRouterCacheStore struct {
	db     *badgerstore.DB
	ttl    time.Duration
	logger *slog.Logger
}

// NewBadgerRouterCacheStore creates a BadgerRouterCacheStore backed by the
// given DB instance.
//
// # Description
//
// The DB must be opened by the caller (typically in main) and must not be
// closed before the store is done being used. The caller is responsible for
// the DB lifecycle — this store does not own the DB.
//
// # Inputs
//
//   - db: Opened BadgerDB wrapper. Must not be nil.
//   - ttl: Lifetime for each cached entry. Pass 0 to use the default (7 days).
//   - logger: Logger for cache hit/miss diagnostics. May be nil.
//
// # Outputs
//
//   - *BadgerRouterCacheStore: Ready-to-use store. Never nil.
//
// # Thread Safety
//
// The returned store is safe for concurrent use.
func NewBadgerRouterCacheStore(db *badgerstore.DB, ttl time.Duration, logger *slog.Logger) *BadgerRouterCacheStore {
	if db == nil {
		panic("NewBadgerRouterCacheStore: db must not be nil")
	}
	if ttl <= 0 {
		ttl = routerCacheDefaultTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BadgerRouterCacheStore{db: db, ttl: ttl, logger: logger}
}

// LoadEmbeddings retrieves cached unit-normalized tool embedding vectors.
//
// # Description
//
// Looks up the key routing/emb/v1/{corpusHash}. Returns (nil, nil) on miss
// (key not found or TTL expired). Returns (nil, error) on storage or decode
// failure. Returns (vectors, nil) on success.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - corpusHash: Hex SHA256 of the tool corpus + model name (from computeCorpusHash).
//
// # Outputs
//
//   - map[string][]float32: Tool name → unit-normalized vector. Nil on miss or error.
//   - error: Non-nil on storage or decode failure. Nil on miss and on success.
//
// # Thread Safety
//
// Safe for concurrent use.
func (s *BadgerRouterCacheStore) LoadEmbeddings(ctx context.Context, corpusHash string) (map[string][]float32, error) {
	key := routerCacheKey(corpusHash)

	var raw []byte
	err := s.db.WithReadTxn(ctx, func(txn *dgbadger.Txn) error {
		item, err := txn.Get(key)
		if errors.Is(err, dgbadger.ErrKeyNotFound) {
			return errCacheMiss
		}
		if err != nil {
			return fmt.Errorf("get cache key: %w", err)
		}
		raw, err = item.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("copy value: %w", err)
		}
		return nil
	})

	if errors.Is(err, errCacheMiss) {
		s.logger.Debug("router cache: miss", slog.String("hash", shortHash(corpusHash)))
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("router cache load: %w", err)
	}

	vectors, err := gobDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("router cache decode: %w", err)
	}

	s.logger.Debug("router cache: hit",
		slog.String("hash", shortHash(corpusHash)),
		slog.Int("tool_count", len(vectors)),
	)
	return vectors, nil
}

// SaveEmbeddings persists unit-normalized tool embedding vectors with a 7-day TTL.
//
// # Description
//
// Encodes vectors as gob-encoded map[string][]float32 and writes to BadgerDB
// under the key routing/emb/v1/{corpusHash} with the configured TTL. After
// TTL expires, the key is invisible to LoadEmbeddings (returns cache miss).
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - corpusHash: Hex SHA256 of the tool corpus + model name.
//   - vectors: Unit-normalized embedding vectors, keyed by tool name. Must not be empty.
//
// # Outputs
//
//   - error: Non-nil on encode or storage failure.
//
// # Thread Safety
//
// Safe for concurrent use.
func (s *BadgerRouterCacheStore) SaveEmbeddings(ctx context.Context, corpusHash string, vectors map[string][]float32) error {
	if len(vectors) == 0 {
		return nil
	}

	raw, err := gobEncode(vectors)
	if err != nil {
		return fmt.Errorf("router cache encode: %w", err)
	}

	key := routerCacheKey(corpusHash)
	err = s.db.WithTxn(ctx, func(txn *dgbadger.Txn) error {
		entry := dgbadger.NewEntry(key, raw).WithTTL(s.ttl)
		return txn.SetEntry(entry)
	})
	if err != nil {
		return fmt.Errorf("router cache save: %w", err)
	}

	s.logger.Debug("router cache: saved",
		slog.String("hash", shortHash(corpusHash)),
		slog.Int("tool_count", len(vectors)),
		slog.Duration("ttl", s.ttl),
	)
	return nil
}

// =============================================================================
// Corpus Hash
// =============================================================================

// computeCorpusHash computes a deterministic SHA256 hash of the tool corpus
// and embedding model name.
//
// # Description
//
// The hash captures all signals that determine the shape and content of the
// embedding vectors:
//   - Tool name (changes if tool is renamed)
//   - Tool BestFor keywords (changes if keywords are added/removed/changed)
//   - Tool UseWhen text (changes if description is updated)
//   - Embedding model name (changes if EMBEDDING_MODEL env var changes)
//
// AvoidWhen is excluded: it is not used in the embedding document (negative
// framing degrades embedding quality) so a change to AvoidWhen does not
// require re-embedding.
//
// Specs are sorted by Name and BestFor keywords are sorted internally for
// determinism regardless of YAML key ordering.
//
// # Inputs
//
//   - specs: Tool specifications. Empty slice produces a valid hash.
//   - model: Embedding model name (e.g. "nomic-embed-text-v2-moe").
//
// # Outputs
//
//   - string: Lowercase hex-encoded SHA256 digest (64 characters).
//
// # Thread Safety
//
// Stateless. Safe for concurrent use.
func computeCorpusHash(specs []ToolSpec, model string) string {
	// Sort specs by name for determinism regardless of YAML ordering.
	sorted := make([]ToolSpec, len(specs))
	copy(sorted, specs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	h := sha256.New()
	for _, s := range sorted {
		// Sort BestFor within each spec for determinism.
		bestFor := make([]string, len(s.BestFor))
		copy(bestFor, s.BestFor)
		sort.Strings(bestFor)

		// Tab-delimited fields; newline terminates each tool entry.
		// This format is stable across Go versions.
		fmt.Fprintf(h, "%s\t%s\t%s\n", s.Name, strings.Join(bestFor, ","), s.UseWhen)
	}
	fmt.Fprintf(h, "model=%s\n", model)

	return hex.EncodeToString(h.Sum(nil))
}

// =============================================================================
// Helpers
// =============================================================================

// routerCacheKey builds the BadgerDB key for the given corpus hash.
func routerCacheKey(corpusHash string) []byte {
	return []byte(routerCacheKeyPrefix + corpusHash)
}

// shortHash returns the first 8 characters of a hash for log display.
func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8] + "..."
	}
	return h
}

// gobEncode serializes a map[string][]float32 using encoding/gob.
func gobEncode(vectors map[string][]float32) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(vectors); err != nil {
		return nil, fmt.Errorf("gob encode: %w", err)
	}
	return buf.Bytes(), nil
}

// gobDecode deserializes a map[string][]float32 from gob-encoded bytes.
func gobDecode(data []byte) (map[string][]float32, error) {
	var vectors map[string][]float32
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&vectors); err != nil {
		return nil, fmt.Errorf("gob decode: %w", err)
	}
	return vectors, nil
}
