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
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	bolt "go.etcd.io/bbolt"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// DiskGraph provides read access to a graph persisted in a bbolt file.
//
// Description:
//
//	DiskGraph opens a bbolt file created by MaterializeToDisk and provides
//	metadata accessors and individual node lookups. For full query support,
//	use LoadAsGraph to reconstruct an in-memory Graph.
//
// Thread Safety:
//
//	Safe for concurrent reads. bbolt supports concurrent read transactions.
//	Close must not be called concurrently with reads.
type DiskGraph struct {
	db           *bolt.DB
	projectRoot  string
	nodeCount    int
	edgeCount    int
	graphHash    string
	builtAtMilli int64
	fileMtimes   map[string]int64
}

// OpenDiskGraph opens a bbolt graph file and validates its schema version.
//
// Description:
//
//	Opens the bbolt file in read-only mode, reads metadata from the meta
//	bucket, and validates the schema version matches BboltSchemaVersion.
//	The caller must call Close when done.
//
// Inputs:
//
//	path - Absolute path to the bbolt file. Must exist.
//
// Outputs:
//
//	*DiskGraph - The opened disk graph. Caller must Close.
//	error - Non-nil if the file doesn't exist, is corrupt, or schema mismatch.
//
// Thread Safety: Safe for concurrent use after construction.
func OpenDiskGraph(path string) (*DiskGraph, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{
		ReadOnly: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("opening bbolt file %s: %w", path, err)
	}

	dg := &DiskGraph{db: db}

	// Read and validate metadata.
	if err := dg.readMeta(); err != nil {
		db.Close()
		return nil, fmt.Errorf("reading metadata from %s: %w", path, err)
	}

	return dg, nil
}

// readMeta reads and validates metadata from the meta bucket.
func (dg *DiskGraph) readMeta() error {
	return dg.db.View(func(tx *bolt.Tx) error {
		metaBkt := tx.Bucket(bucketMeta)
		if metaBkt == nil {
			return fmt.Errorf("meta bucket not found")
		}

		// Validate schema version.
		schemaData := metaBkt.Get(metaKeySchemaVersion)
		if schemaData == nil {
			return fmt.Errorf("schema_version not found in meta")
		}
		schema, err := decodeMetaString(schemaData)
		if err != nil {
			return fmt.Errorf("decoding schema_version: %w", err)
		}
		if schema != BboltSchemaVersion {
			return fmt.Errorf("unsupported bbolt schema version %q (expected %q)", schema, BboltSchemaVersion)
		}

		// Read project root.
		if data := metaBkt.Get(metaKeyProjectRoot); data != nil {
			pr, prErr := decodeMetaString(data)
			if prErr != nil {
				return fmt.Errorf("decoding project_root: %w", prErr)
			}
			dg.projectRoot = pr
		}

		// Read node count.
		if data := metaBkt.Get(metaKeyNodeCount); data != nil {
			nc, ncErr := decodeMetaInt(data)
			if ncErr != nil {
				return fmt.Errorf("decoding node_count: %w", ncErr)
			}
			dg.nodeCount = nc
		}

		// Read edge count.
		if data := metaBkt.Get(metaKeyEdgeCount); data != nil {
			ec, ecErr := decodeMetaInt(data)
			if ecErr != nil {
				return fmt.Errorf("decoding edge_count: %w", ecErr)
			}
			dg.edgeCount = ec
		}

		// Read graph hash.
		if data := metaBkt.Get(metaKeyGraphHash); data != nil {
			gh, ghErr := decodeMetaString(data)
			if ghErr != nil {
				return fmt.Errorf("decoding graph_hash: %w", ghErr)
			}
			dg.graphHash = gh
		}

		// Read built_at.
		if data := metaBkt.Get(metaKeyBuiltAt); data != nil {
			ba, baErr := decodeMetaInt64(data)
			if baErr != nil {
				return fmt.Errorf("decoding built_at: %w", baErr)
			}
			dg.builtAtMilli = ba
		}

		// Read file mtimes.
		if data := metaBkt.Get(metaKeyFileMtimes); data != nil {
			fm, fmErr := decodeMetaFileMtimes(data)
			if fmErr != nil {
				return fmt.Errorf("decoding file_mtimes: %w", fmErr)
			}
			dg.fileMtimes = fm
		}

		return nil
	})
}

// Close closes the underlying bbolt database.
//
// Description:
//
//	Releases the file lock on the bbolt file. Must be called when done.
//
// Thread Safety: Must not be called concurrently with other methods.
func (dg *DiskGraph) Close() error {
	if dg.db != nil {
		return dg.db.Close()
	}
	return nil
}

// GetNode reads a single node from the bbolt file by ID.
//
// Description:
//
//	Reads the node from the nodes bucket and its edges from edges_out and
//	edges_in buckets in a single read transaction. Returns a Node with
//	populated Outgoing and Incoming slices, identical to in-memory Graph.
//
// Inputs:
//
//	id - The node ID to look up.
//
// Outputs:
//
//	*Node - The node with Symbol, Outgoing, and Incoming populated.
//	bool - True if the node was found.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) GetNode(id string) (*Node, bool) {
	var node *Node

	err := dg.db.View(func(tx *bolt.Tx) error {
		// Read node.
		nodesBkt := tx.Bucket(bucketNodes)
		if nodesBkt == nil {
			return nil
		}
		nodeData := nodesBkt.Get([]byte(id))
		if nodeData == nil {
			return nil
		}

		var decErr error
		node, decErr = decodeNode(nodeData)
		if decErr != nil {
			return fmt.Errorf("decoding node %s: %w", id, decErr)
		}

		// Read outgoing edges.
		edgesOutBkt := tx.Bucket(bucketEdgesOut)
		if edgesOutBkt != nil {
			if data := edgesOutBkt.Get([]byte(id)); data != nil {
				edges, err := decodeEdges(data)
				if err != nil {
					return fmt.Errorf("decoding outgoing edges for %s: %w", id, err)
				}
				node.Outgoing = edges
			}
		}

		// Read incoming edges.
		edgesInBkt := tx.Bucket(bucketEdgesIn)
		if edgesInBkt != nil {
			if data := edgesInBkt.Get([]byte(id)); data != nil {
				edges, err := decodeEdges(data)
				if err != nil {
					return fmt.Errorf("decoding incoming edges for %s: %w", id, err)
				}
				node.Incoming = edges
			}
		}

		return nil
	})

	if err != nil || node == nil {
		return nil, false
	}

	return node, true
}

// NodeCount returns the number of nodes recorded in metadata.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) NodeCount() int {
	return dg.nodeCount
}

// EdgeCount returns the number of edges recorded in metadata.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) EdgeCount() int {
	return dg.edgeCount
}

// ProjectRoot returns the project root recorded in metadata.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) ProjectRoot() string {
	return dg.projectRoot
}

// FileMtimes returns the file modification times recorded at build time.
//
// Description:
//
//	Returns a copy of the file mtimes map to prevent mutation.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) FileMtimes() map[string]int64 {
	if dg.fileMtimes == nil {
		return nil
	}
	result := make(map[string]int64, len(dg.fileMtimes))
	for k, v := range dg.fileMtimes {
		result[k] = v
	}
	return result
}

// BuiltAtMilli returns the build timestamp recorded in metadata.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) BuiltAtMilli() int64 {
	return dg.builtAtMilli
}

// GraphHash returns the graph hash recorded in metadata.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) GraphHash() string {
	return dg.graphHash
}

// LoadAsGraph reconstructs a full in-memory Graph from the bbolt file.
//
// Description:
//
//	Iterates all nodes in the nodes bucket and all outgoing edges in the
//	edges_out bucket, calling AddNode and AddEdge to reconstruct the graph
//	with all secondary indexes. Faster than BadgerDB path because:
//	  1. No JSON parsing — binary gob decode
//	  2. No gzip — snappy is faster
//	  3. Sequential B+ tree reads are cache-friendly
//
// Inputs:
//
//	ctx - Context for cancellation and tracing. Must not be nil.
//
// Outputs:
//
//	*Graph - The reconstructed graph in frozen, read-only state.
//	error - Non-nil if reconstruction fails.
//
// Limitations:
//
//	Loads the entire graph into memory. For large graphs, this may require
//	significant RAM. Phase 1b will add direct-from-disk query support.
//
// Thread Safety: Safe for concurrent use on the DiskGraph.
func (dg *DiskGraph) LoadAsGraph(ctx context.Context) (*Graph, error) {
	ctx, span := otel.Tracer("graph").Start(ctx, "graph.DiskGraph.LoadAsGraph")
	defer span.End()

	span.SetAttributes(
		attribute.Int("node_count", dg.nodeCount),
		attribute.Int("edge_count", dg.edgeCount),
		attribute.String("project_root", dg.projectRoot),
	)

	// Headroom of +100 allows incremental refresh to add nodes/edges
	// after loading without hitting capacity limits.
	g := NewGraph(dg.projectRoot,
		WithMaxNodes(max(dg.nodeCount+100, DefaultMaxNodes)),
		WithMaxEdges(max(dg.edgeCount+100, DefaultMaxEdges)),
	)

	err := dg.db.View(func(tx *bolt.Tx) error {
		// Add all nodes.
		nodesBkt := tx.Bucket(bucketNodes)
		if nodesBkt == nil {
			return fmt.Errorf("nodes bucket not found")
		}

		err := nodesBkt.ForEach(func(k, v []byte) error {
			node, err := decodeNode(v)
			if err != nil {
				return fmt.Errorf("decoding node %s: %w", string(k), err)
			}
			if node.Symbol == nil {
				return fmt.Errorf("node %s has nil symbol", string(k))
			}
			if _, err := g.AddNode(node.Symbol); err != nil {
				return fmt.Errorf("adding node %s: %w", string(k), err)
			}
			return nil
		})
		if err != nil {
			return err
		}

		// Add all edges from edges_out bucket.
		edgesOutBkt := tx.Bucket(bucketEdgesOut)
		if edgesOutBkt == nil {
			// No edges — valid for empty or node-only graphs.
			return nil
		}

		return edgesOutBkt.ForEach(func(k, v []byte) error {
			edges, err := decodeEdges(v)
			if err != nil {
				return fmt.Errorf("decoding outgoing edges for %s: %w", string(k), err)
			}
			for _, edge := range edges {
				if err := g.AddEdge(edge.FromID, edge.ToID, edge.Type, edge.Location); err != nil {
					return fmt.Errorf("adding edge %s -> %s: %w", edge.FromID, edge.ToID, err)
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("loading graph from bbolt: %w", err)
	}

	g.Freeze()
	g.BuiltAtMilli = dg.builtAtMilli
	g.FileMtimes = dg.FileMtimes()

	return g, nil
}

// nodesByNameFromDisk reads the name index from the bbolt file.
//
// Description:
//
//	Reads node IDs associated with a symbol name from the idx_name bucket.
//	Phase 1b foundation: enables direct name lookups without loading the
//	full graph.
//
// Inputs:
//
//	name - The symbol name to look up.
//
// Outputs:
//
//	[]string - Node IDs matching the name. Nil if not found.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) nodesByNameFromDisk(name string) []string {
	var ids []string

	_ = dg.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketIdxName)
		if bkt == nil {
			return nil
		}
		data := bkt.Get([]byte(name))
		if data == nil {
			return nil
		}
		var err error
		ids, err = decodeStringSlice(data)
		if err != nil {
			return err
		}
		return nil
	})

	return ids
}

// nodesByKindFromDisk reads the kind index from the bbolt file.
//
// Description:
//
//	Reads node IDs associated with a symbol kind from the idx_kind bucket.
//	Phase 1b foundation.
//
// Inputs:
//
//	kind - The symbol kind to look up.
//
// Outputs:
//
//	[]string - Node IDs matching the kind. Nil if not found.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) nodesByKindFromDisk(kind ast.SymbolKind) []string {
	var ids []string

	_ = dg.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketIdxKind)
		if bkt == nil {
			return nil
		}
		data := bkt.Get([]byte(kind.String()))
		if data == nil {
			return nil
		}
		var err error
		ids, err = decodeStringSlice(data)
		if err != nil {
			return err
		}
		return nil
	})

	return ids
}

// nodesByFileFromDisk reads the file index from the bbolt file.
//
// Description:
//
//	Reads node IDs associated with a file path from the idx_file bucket.
//	Phase 1b foundation.
//
// Inputs:
//
//	filePath - The file path to look up.
//
// Outputs:
//
//	[]string - Node IDs in the file. Nil if not found.
//
// Thread Safety: Safe for concurrent use.
func (dg *DiskGraph) nodesByFileFromDisk(filePath string) []string {
	var ids []string

	_ = dg.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bucketIdxFile)
		if bkt == nil {
			return nil
		}
		data := bkt.Get([]byte(filePath))
		if data == nil {
			return nil
		}
		var err error
		ids, err = decodeStringSlice(data)
		if err != nil {
			return err
		}
		return nil
	})

	return ids
}
