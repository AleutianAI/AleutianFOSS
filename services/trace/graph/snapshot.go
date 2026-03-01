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
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/dgraph-io/badger/v4"
)

// BadgerDB key prefixes for graph snapshots.
const (
	keyPrefixSnap      = "graph:snap:"
	keyPrefixSnapIndex = "graph:snap:index:"
	keySuffixData      = ":data"
	keySuffixMeta      = ":meta"
	keySuffixLatest    = ":latest"
)

// SnapshotMetadata contains metadata about a saved graph snapshot.
type SnapshotMetadata struct {
	// SnapshotID is the unique identifier for this snapshot.
	// Derived from SHA256(ProjectRoot + BuiltAtMilli)[:16].
	SnapshotID string `json:"snapshot_id"`

	// ProjectRoot is the absolute path to the project root.
	ProjectRoot string `json:"project_root"`

	// ProjectHash is SHA256(ProjectRoot)[:16] for key grouping.
	ProjectHash string `json:"project_hash"`

	// GraphHash is the deterministic hash of the graph structure.
	GraphHash string `json:"graph_hash"`

	// Label is an optional human-readable label.
	Label string `json:"label,omitempty"`

	// CreatedAtMilli is when the snapshot was saved (Unix milliseconds UTC).
	CreatedAtMilli int64 `json:"created_at_milli"`

	// NodeCount is the number of nodes in the graph.
	NodeCount int `json:"node_count"`

	// EdgeCount is the number of edges in the graph.
	EdgeCount int `json:"edge_count"`

	// SchemaVersion is the serialization schema version.
	SchemaVersion string `json:"schema_version"`

	// CompressedSize is the size of the gzip-compressed JSON payload in bytes.
	CompressedSize int64 `json:"compressed_size"`

	// ContentHash is the SHA256 hash of the compressed payload.
	ContentHash string `json:"content_hash"`
}

// SnapshotManager manages saving and loading graph snapshots in BadgerDB.
//
// Description:
//
//	Provides CRUD operations for graph snapshots stored as gzip-compressed
//	JSON in BadgerDB. Each snapshot stores the full SerializableGraph plus
//	metadata for listing and filtering.
//
// Thread Safety:
//
//	Safe for concurrent use. BadgerDB handles its own concurrency control.
type SnapshotManager struct {
	db     *badger.DB
	logger *slog.Logger
}

// NewSnapshotManager creates a new SnapshotManager.
//
// Description:
//
//	Creates a manager that uses the given BadgerDB instance for persistence.
//	The DB should be opened by the caller and closed when no longer needed.
//
// Inputs:
//
//	db - An opened BadgerDB instance. Must not be nil.
//	logger - Logger for diagnostic output. Must not be nil.
//
// Outputs:
//
//	*SnapshotManager - The configured manager.
//	error - Non-nil if db or logger is nil.
func NewSnapshotManager(db *badger.DB, logger *slog.Logger) (*SnapshotManager, error) {
	if db == nil {
		return nil, fmt.Errorf("badger db must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}
	return &SnapshotManager{db: db, logger: logger}, nil
}

// Save persists a graph snapshot to BadgerDB.
//
// Description:
//
//	Serializes the graph to JSON, gzip-compresses it, and stores it in
//	BadgerDB along with metadata. Updates the "latest" pointer for the
//	project.
//
// Inputs:
//
//	ctx - Context for cancellation. Must not be nil.
//	g - The graph to snapshot. Must not be nil and should be frozen.
//	label - Optional human-readable label for the snapshot.
//
// Outputs:
//
//	*SnapshotMetadata - Metadata about the saved snapshot.
//	error - Non-nil if serialization or storage fails.
//
// Key Schema:
//
//	graph:snap:{projectHash}:{snapshotID}:data → gzip(JSON(SerializableGraph))
//	graph:snap:{projectHash}:{snapshotID}:meta → JSON(SnapshotMetadata)
//	graph:snap:{projectHash}:latest            → snapshotID
//	graph:snap:index:{snapshotID}              → projectHash
func (m *SnapshotManager) Save(ctx context.Context, g *Graph, label string) (*SnapshotMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if g == nil {
		return nil, fmt.Errorf("graph must not be nil")
	}

	// Serialize the graph
	sg := g.ToSerializable()

	jsonData, err := json.Marshal(sg)
	if err != nil {
		return nil, fmt.Errorf("marshaling graph: %w", err)
	}

	// Gzip compress
	var compressed bytes.Buffer
	gw, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("creating gzip writer: %w", err)
	}
	if _, err := gw.Write(jsonData); err != nil {
		return nil, fmt.Errorf("compressing graph: %w", err)
	}
	if err := gw.Close(); err != nil {
		return nil, fmt.Errorf("closing gzip writer: %w", err)
	}

	compressedData := compressed.Bytes()

	// Compute hashes
	projectHash := hashString(g.ProjectRoot)[:16]
	snapshotID := hashString(fmt.Sprintf("%s:%d", g.ProjectRoot, g.BuiltAtMilli))[:16]
	contentHash := hashBytes(compressedData)

	meta := &SnapshotMetadata{
		SnapshotID:     snapshotID,
		ProjectRoot:    g.ProjectRoot,
		ProjectHash:    projectHash,
		GraphHash:      sg.GraphHash,
		Label:          label,
		CreatedAtMilli: time.Now().UnixMilli(),
		NodeCount:      g.NodeCount(),
		EdgeCount:      g.EdgeCount(),
		SchemaVersion:  GraphSchemaVersion,
		CompressedSize: int64(len(compressedData)),
		ContentHash:    contentHash,
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling metadata: %w", err)
	}

	// Build keys
	dataKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixData
	metaKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixMeta
	latestKey := keyPrefixSnap + projectHash + keySuffixLatest
	indexKey := keyPrefixSnapIndex + snapshotID

	// Write to BadgerDB in a single transaction
	err = m.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(dataKey), compressedData); err != nil {
			return fmt.Errorf("storing data: %w", err)
		}
		if err := txn.Set([]byte(metaKey), metaJSON); err != nil {
			return fmt.Errorf("storing metadata: %w", err)
		}
		if err := txn.Set([]byte(latestKey), []byte(snapshotID)); err != nil {
			return fmt.Errorf("updating latest pointer: %w", err)
		}
		if err := txn.Set([]byte(indexKey), []byte(projectHash)); err != nil {
			return fmt.Errorf("storing reverse index: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("writing snapshot to badger: %w", err)
	}

	m.logger.Info("snapshot saved",
		slog.String("snapshot_id", snapshotID),
		slog.String("project_root", g.ProjectRoot),
		slog.Int("node_count", meta.NodeCount),
		slog.Int("edge_count", meta.EdgeCount),
		slog.Int64("compressed_size", meta.CompressedSize),
	)

	return meta, nil
}

// Load retrieves a graph snapshot by its ID.
//
// Description:
//
//	Loads the compressed data from BadgerDB, decompresses it, and
//	reconstructs the graph using FromSerializable.
//
// Inputs:
//
//	ctx - Context for cancellation. Must not be nil.
//	snapshotID - The snapshot ID to load. Must not be empty.
//
// Outputs:
//
//	*Graph - The reconstructed graph in read-only state.
//	*SnapshotMetadata - The snapshot metadata.
//	error - Non-nil if the snapshot is not found or reconstruction fails.
func (m *SnapshotManager) Load(ctx context.Context, snapshotID string) (*Graph, *SnapshotMetadata, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("ctx must not be nil")
	}
	if snapshotID == "" {
		return nil, nil, fmt.Errorf("snapshot ID must not be empty")
	}

	// Look up projectHash from reverse index
	projectHash, err := m.getProjectHash(snapshotID)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up snapshot %s: %w", snapshotID, err)
	}

	return m.loadByKeys(projectHash, snapshotID)
}

// LoadLatest loads the most recent snapshot for a project.
//
// Description:
//
//	Uses the "latest" pointer to find and load the most recent snapshot
//	for the given project hash.
//
// Inputs:
//
//	ctx - Context for cancellation. Must not be nil.
//	projectHash - The SHA256(ProjectRoot)[:16] hash. Must not be empty.
//
// Outputs:
//
//	*Graph - The reconstructed graph in read-only state.
//	*SnapshotMetadata - The snapshot metadata.
//	error - Non-nil if no snapshot exists or reconstruction fails.
func (m *SnapshotManager) LoadLatest(ctx context.Context, projectHash string) (*Graph, *SnapshotMetadata, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("ctx must not be nil")
	}
	if projectHash == "" {
		return nil, nil, fmt.Errorf("project hash must not be empty")
	}

	// Read the latest pointer
	latestKey := keyPrefixSnap + projectHash + keySuffixLatest
	var snapshotID string
	err := m.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(latestKey))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			snapshotID = string(val)
			return nil
		})
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reading latest pointer for %s: %w", projectHash, err)
	}

	return m.loadByKeys(projectHash, snapshotID)
}

// List returns metadata for snapshots matching the optional project hash filter.
//
// Description:
//
//	Iterates BadgerDB keys with the snapshot prefix to find metadata entries.
//	If projectHash is non-empty, only snapshots for that project are returned.
//	Results are ordered by CreatedAtMilli descending (newest first).
//
// Inputs:
//
//	ctx - Context for cancellation. Must not be nil.
//	projectHash - Optional filter. If empty, returns all snapshots.
//	limit - Maximum number of results. If <= 0, defaults to 100.
//
// Outputs:
//
//	[]*SnapshotMetadata - The matching snapshots.
//	error - Non-nil if the read fails.
func (m *SnapshotManager) List(ctx context.Context, projectHash string, limit int) ([]*SnapshotMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("ctx must not be nil")
	}
	if limit <= 0 {
		limit = 100
	}

	var results []*SnapshotMetadata

	prefix := keyPrefixSnap
	if projectHash != "" {
		prefix = keyPrefixSnap + projectHash + ":"
	}

	err := m.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(prefix)); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Only process metadata keys
			if !isMetaKey(key) {
				continue
			}

			var meta SnapshotMetadata
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &meta)
			})
			if err != nil {
				m.logger.Warn("skipping corrupt metadata", slog.String("key", key), slog.Any("error", err))
				continue
			}

			results = append(results, &meta)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}

	// Sort by CreatedAtMilli descending (newest first)
	sortSnapshotsByDate(results)

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// Delete removes a snapshot from BadgerDB.
//
// Description:
//
//	Removes all keys associated with the given snapshot ID: data, metadata,
//	and the reverse index entry. If the deleted snapshot was the "latest",
//	the latest pointer is also removed.
//
// Inputs:
//
//	ctx - Context for cancellation. Must not be nil.
//	snapshotID - The snapshot ID to delete. Must not be empty.
//
// Outputs:
//
//	error - Non-nil if the snapshot is not found or deletion fails.
func (m *SnapshotManager) Delete(ctx context.Context, snapshotID string) error {
	if ctx == nil {
		return fmt.Errorf("ctx must not be nil")
	}
	if snapshotID == "" {
		return fmt.Errorf("snapshot ID must not be empty")
	}

	// Look up projectHash
	projectHash, err := m.getProjectHash(snapshotID)
	if err != nil {
		return fmt.Errorf("looking up snapshot %s: %w", snapshotID, err)
	}

	dataKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixData
	metaKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixMeta
	latestKey := keyPrefixSnap + projectHash + keySuffixLatest
	indexKey := keyPrefixSnapIndex + snapshotID

	err = m.db.Update(func(txn *badger.Txn) error {
		// Delete data and metadata
		if err := txn.Delete([]byte(dataKey)); err != nil && err != badger.ErrKeyNotFound {
			return fmt.Errorf("deleting data: %w", err)
		}
		if err := txn.Delete([]byte(metaKey)); err != nil && err != badger.ErrKeyNotFound {
			return fmt.Errorf("deleting metadata: %w", err)
		}
		if err := txn.Delete([]byte(indexKey)); err != nil && err != badger.ErrKeyNotFound {
			return fmt.Errorf("deleting reverse index: %w", err)
		}

		// If this was the latest, remove the latest pointer
		item, err := txn.Get([]byte(latestKey))
		if err == nil {
			var currentLatest string
			_ = item.Value(func(val []byte) error {
				currentLatest = string(val)
				return nil
			})
			if currentLatest == snapshotID {
				if err := txn.Delete([]byte(latestKey)); err != nil && err != badger.ErrKeyNotFound {
					return fmt.Errorf("deleting latest pointer: %w", err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("deleting snapshot %s: %w", snapshotID, err)
	}

	m.logger.Info("snapshot deleted", slog.String("snapshot_id", snapshotID))
	return nil
}

// loadByKeys loads a graph from BadgerDB using known projectHash and snapshotID.
func (m *SnapshotManager) loadByKeys(projectHash, snapshotID string) (*Graph, *SnapshotMetadata, error) {
	dataKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixData
	metaKey := keyPrefixSnap + projectHash + ":" + snapshotID + keySuffixMeta

	var compressedData []byte
	var metaJSON []byte

	err := m.db.View(func(txn *badger.Txn) error {
		// Read compressed data
		dataItem, err := txn.Get([]byte(dataKey))
		if err != nil {
			return fmt.Errorf("reading data for %s: %w", snapshotID, err)
		}
		compressedData, err = dataItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("copying data for %s: %w", snapshotID, err)
		}

		// Read metadata
		metaItem, err := txn.Get([]byte(metaKey))
		if err != nil {
			return fmt.Errorf("reading metadata for %s: %w", snapshotID, err)
		}
		metaJSON, err = metaItem.ValueCopy(nil)
		if err != nil {
			return fmt.Errorf("copying metadata for %s: %w", snapshotID, err)
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Verify content integrity
	actualHash := hashBytes(compressedData)
	var meta SnapshotMetadata
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling metadata for %s: %w", snapshotID, err)
	}
	if meta.ContentHash != "" && meta.ContentHash != actualHash {
		return nil, nil, fmt.Errorf("integrity check failed for %s: expected hash %s, got %s", snapshotID, meta.ContentHash, actualHash)
	}

	// Decompress
	gr, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return nil, nil, fmt.Errorf("decompressing snapshot %s: %w", snapshotID, err)
	}
	defer gr.Close()

	jsonData, err := io.ReadAll(gr)
	if err != nil {
		return nil, nil, fmt.Errorf("reading decompressed data for %s: %w", snapshotID, err)
	}

	// Unmarshal the serializable graph
	var sg SerializableGraph
	if err := json.Unmarshal(jsonData, &sg); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling graph for %s: %w", snapshotID, err)
	}

	// Reconstruct the graph
	g, err := FromSerializable(&sg)
	if err != nil {
		return nil, nil, fmt.Errorf("reconstructing graph for %s: %w", snapshotID, err)
	}

	return g, &meta, nil
}

// getProjectHash retrieves the project hash for a snapshot ID from the reverse index.
func (m *SnapshotManager) getProjectHash(snapshotID string) (string, error) {
	indexKey := keyPrefixSnapIndex + snapshotID
	var projectHash string
	err := m.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(indexKey))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			projectHash = string(val)
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	return projectHash, nil
}

// ProjectHash returns SHA256(projectRoot)[:16] for use as a key prefix.
//
// Description:
//
//	Computes a deterministic, URL-safe hash of the project root path for
//	use as a BadgerDB key prefix. Exported so handlers can convert a
//	project_root query param to the hash used in storage.
//
// Inputs:
//
//	projectRoot - The project root path.
//
// Outputs:
//
//	string - 16-character hex hash.
func ProjectHash(projectRoot string) string {
	return hashString(projectRoot)[:16]
}

// hashString returns the hex-encoded SHA256 hash of a string.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// hashBytes returns the hex-encoded SHA256 hash of a byte slice.
func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// isMetaKey returns true if the key ends with the metadata suffix.
func isMetaKey(key string) bool {
	return len(key) > len(keySuffixMeta) && key[len(key)-len(keySuffixMeta):] == keySuffixMeta
}

// sortSnapshotsByDate sorts snapshots by CreatedAtMilli descending.
func sortSnapshotsByDate(snapshots []*SnapshotMetadata) {
	for i := 1; i < len(snapshots); i++ {
		for j := i; j > 0 && snapshots[j].CreatedAtMilli > snapshots[j-1].CreatedAtMilli; j-- {
			snapshots[j], snapshots[j-1] = snapshots[j-1], snapshots[j]
		}
	}
}
