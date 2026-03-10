// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	bolt "go.etcd.io/bbolt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// bbolt bucket names for graph persistence.
var (
	bucketNodes    = []byte("nodes")
	bucketEdgesOut = []byte("edges_out")
	bucketEdgesIn  = []byte("edges_in")
	bucketIdxName  = []byte("idx_name")
	bucketIdxKind  = []byte("idx_kind")
	bucketIdxFile  = []byte("idx_file")
	bucketMeta     = []byte("meta")
)

// bbolt meta keys.
var (
	metaKeySchemaVersion = []byte("schema_version")
	metaKeyProjectRoot   = []byte("project_root")
	metaKeyBuiltAt       = []byte("built_at")
	metaKeyNodeCount     = []byte("node_count")
	metaKeyEdgeCount     = []byte("edge_count")
	metaKeyGraphHash     = []byte("graph_hash")
	metaKeyFileMtimes    = []byte("file_mtimes")
)

// MaterializeToDisk persists a frozen graph to a bbolt file.
//
// Description:
//
//	Writes all nodes, edges, secondary indexes, and metadata to a bbolt file
//	in a single atomic transaction. The graph must be frozen (read-only).
//	If a file already exists at path, it is replaced atomically via
//	write-to-temp + rename.
//
// Inputs:
//
//	ctx - Context for cancellation and tracing. Must not be nil.
//	path - Absolute path to the bbolt file to create. Parent dir must exist.
//
// Outputs:
//
//	error - Non-nil if the graph is not frozen, encoding fails, or I/O fails.
//
// Limitations:
//
//	Requires the graph to be frozen. Does not compress the bbolt file itself
//	(individual values are snappy-compressed).
//
// Thread Safety:
//
//	Safe for concurrent use on frozen graphs (read-only access to graph data).
func (g *Graph) MaterializeToDisk(ctx context.Context, path string) error {
	ctx, span := otel.Tracer("graph").Start(ctx, "graph.MaterializeToDisk")
	defer span.End()

	if g == nil {
		return fmt.Errorf("graph must not be nil")
	}
	if !g.IsFrozen() {
		return fmt.Errorf("graph must be frozen before materializing to disk")
	}

	span.SetAttributes(
		attribute.Int("node_count", g.NodeCount()),
		attribute.Int("edge_count", g.EdgeCount()),
		attribute.String("project_root", g.ProjectRoot),
	)

	// Write to a temp file then rename for atomicity.
	tmpPath := path + ".tmp"
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpPath)
		}
	}()

	db, err := bolt.Open(tmpPath, 0o600, &bolt.Options{
		Timeout:      5 * time.Second,
		NoGrowSync:   false,
		FreelistType: bolt.FreelistMapType,
	})
	if err != nil {
		return fmt.Errorf("opening bbolt file %s: %w", tmpPath, err)
	}

	if err := g.writeToBbolt(db); err != nil {
		db.Close()
		return err
	}

	if err := db.Close(); err != nil {
		return fmt.Errorf("closing bbolt file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming bbolt file %s -> %s: %w", tmpPath, path, err)
	}
	renamed = true

	return nil
}

// writeToBbolt writes all graph data to a bbolt database in a single transaction.
func (g *Graph) writeToBbolt(db *bolt.DB) error {
	return db.Update(func(tx *bolt.Tx) error {
		// Create all buckets.
		nodesBkt, err := tx.CreateBucket(bucketNodes)
		if err != nil {
			return fmt.Errorf("creating nodes bucket: %w", err)
		}
		edgesOutBkt, err := tx.CreateBucket(bucketEdgesOut)
		if err != nil {
			return fmt.Errorf("creating edges_out bucket: %w", err)
		}
		edgesInBkt, err := tx.CreateBucket(bucketEdgesIn)
		if err != nil {
			return fmt.Errorf("creating edges_in bucket: %w", err)
		}
		idxNameBkt, err := tx.CreateBucket(bucketIdxName)
		if err != nil {
			return fmt.Errorf("creating idx_name bucket: %w", err)
		}
		idxKindBkt, err := tx.CreateBucket(bucketIdxKind)
		if err != nil {
			return fmt.Errorf("creating idx_kind bucket: %w", err)
		}
		idxFileBkt, err := tx.CreateBucket(bucketIdxFile)
		if err != nil {
			return fmt.Errorf("creating idx_file bucket: %w", err)
		}
		metaBkt, err := tx.CreateBucket(bucketMeta)
		if err != nil {
			return fmt.Errorf("creating meta bucket: %w", err)
		}

		// Write nodes in deterministic order.
		nodeIDs := make([]string, 0, len(g.nodes))
		for id := range g.nodes {
			nodeIDs = append(nodeIDs, id)
		}
		sort.Strings(nodeIDs)

		for _, id := range nodeIDs {
			node := g.nodes[id]
			data, err := encodeNode(node)
			if err != nil {
				return fmt.Errorf("encoding node %s: %w", id, err)
			}
			if err := nodesBkt.Put([]byte(id), data); err != nil {
				return fmt.Errorf("storing node %s: %w", id, err)
			}
		}

		// Build and write outgoing/incoming edge maps.
		// Map iteration order is non-deterministic, but bbolt stores keys in
		// B+ tree order (sorted by key bytes), so the on-disk layout is always
		// deterministic regardless of insertion order.
		outgoing := make(map[string][]*Edge)
		incoming := make(map[string][]*Edge)
		for _, edge := range g.edges {
			outgoing[edge.FromID] = append(outgoing[edge.FromID], edge)
			incoming[edge.ToID] = append(incoming[edge.ToID], edge)
		}

		for id, edges := range outgoing {
			data, err := encodeEdges(edges)
			if err != nil {
				return fmt.Errorf("encoding outgoing edges for %s: %w", id, err)
			}
			if err := edgesOutBkt.Put([]byte(id), data); err != nil {
				return fmt.Errorf("storing outgoing edges for %s: %w", id, err)
			}
		}

		for id, edges := range incoming {
			data, err := encodeEdges(edges)
			if err != nil {
				return fmt.Errorf("encoding incoming edges for %s: %w", id, err)
			}
			if err := edgesInBkt.Put([]byte(id), data); err != nil {
				return fmt.Errorf("storing incoming edges for %s: %w", id, err)
			}
		}

		// Write name index.
		for name, nodes := range g.nodesByName {
			if name == "" {
				continue
			}
			ids := make([]string, 0, len(nodes))
			for _, n := range nodes {
				ids = append(ids, n.ID)
			}
			sort.Strings(ids)
			data, err := encodeStringSlice(ids)
			if err != nil {
				return fmt.Errorf("encoding name index for %s: %w", name, err)
			}
			if err := idxNameBkt.Put([]byte(name), data); err != nil {
				return fmt.Errorf("storing name index for %s: %w", name, err)
			}
		}

		// Write kind index.
		for kind, nodes := range g.nodesByKind {
			ids := make([]string, 0, len(nodes))
			for _, n := range nodes {
				ids = append(ids, n.ID)
			}
			sort.Strings(ids)
			data, err := encodeStringSlice(ids)
			if err != nil {
				return fmt.Errorf("encoding kind index for %v: %w", kind, err)
			}
			if err := idxKindBkt.Put([]byte(kind.String()), data); err != nil {
				return fmt.Errorf("storing kind index for %v: %w", kind, err)
			}
		}

		// Write file index (derived from node file paths).
		fileIndex := make(map[string][]string)
		for _, id := range nodeIDs {
			node := g.nodes[id]
			if node.Symbol != nil && node.Symbol.FilePath != "" {
				fileIndex[node.Symbol.FilePath] = append(fileIndex[node.Symbol.FilePath], id)
			}
		}
		for filePath, ids := range fileIndex {
			sort.Strings(ids)
			data, err := encodeStringSlice(ids)
			if err != nil {
				return fmt.Errorf("encoding file index for %s: %w", filePath, err)
			}
			if err := idxFileBkt.Put([]byte(filePath), data); err != nil {
				return fmt.Errorf("storing file index for %s: %w", filePath, err)
			}
		}

		// Write metadata using type-safe encode helpers.
		type metaEntry struct {
			key  []byte
			data []byte
		}
		encodeAll := func() ([]metaEntry, error) {
			var entries []metaEntry
			add := func(key []byte, encFn func() ([]byte, error)) error {
				data, err := encFn()
				if err != nil {
					return fmt.Errorf("encoding meta %s: %w", string(key), err)
				}
				entries = append(entries, metaEntry{key: key, data: data})
				return nil
			}
			if err := add(metaKeySchemaVersion, func() ([]byte, error) { return encodeMetaString(BboltSchemaVersion) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyProjectRoot, func() ([]byte, error) { return encodeMetaString(g.ProjectRoot) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyBuiltAt, func() ([]byte, error) { return encodeMetaInt64(g.BuiltAtMilli) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyNodeCount, func() ([]byte, error) { return encodeMetaInt(g.NodeCount()) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyEdgeCount, func() ([]byte, error) { return encodeMetaInt(g.EdgeCount()) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyGraphHash, func() ([]byte, error) { return encodeMetaString(g.Hash()) }); err != nil {
				return nil, err
			}
			if err := add(metaKeyFileMtimes, func() ([]byte, error) { return encodeMetaFileMtimes(g.FileMtimes) }); err != nil {
				return nil, err
			}
			return entries, nil
		}

		entries, err := encodeAll()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := metaBkt.Put(entry.key, entry.data); err != nil {
				return fmt.Errorf("storing meta %s: %w", string(entry.key), err)
			}
		}

		return nil
	})
}
