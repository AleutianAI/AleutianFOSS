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
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

// materializeTestGraph creates a frozen graph, materializes it, and returns path + original graph.
func materializeTestGraph(t *testing.T, nodeCount, edgesPerNode int) (string, *Graph) {
	t.Helper()
	g := buildBboltTestGraph(t, nodeCount, edgesPerNode)
	path := filepath.Join(t.TempDir(), "test.db")
	err := g.MaterializeToDisk(context.Background(), path)
	require.NoError(t, err)
	return path, g
}

func TestDiskGraph_RoundTrip(t *testing.T) {
	path, original := materializeTestGraph(t, 20, 2)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	// Verify metadata matches.
	assert.Equal(t, original.NodeCount(), dg.NodeCount())
	assert.Equal(t, original.EdgeCount(), dg.EdgeCount())
	assert.Equal(t, original.ProjectRoot, dg.ProjectRoot())
	assert.Equal(t, original.BuiltAtMilli, dg.BuiltAtMilli())
	assert.Equal(t, original.Hash(), dg.GraphHash())
	assert.Equal(t, original.FileMtimes, dg.FileMtimes())

	// LoadAsGraph and verify counts + hash.
	loaded, err := dg.LoadAsGraph(context.Background())
	require.NoError(t, err)

	assert.Equal(t, original.NodeCount(), loaded.NodeCount())
	assert.Equal(t, original.EdgeCount(), loaded.EdgeCount())
	assert.Equal(t, original.Hash(), loaded.Hash())
	assert.Equal(t, original.ProjectRoot, loaded.ProjectRoot)
	assert.Equal(t, original.BuiltAtMilli, loaded.BuiltAtMilli)
	assert.True(t, loaded.IsFrozen())
}

func TestDiskGraph_QueryCorrectness(t *testing.T) {
	path, original := materializeTestGraph(t, 15, 2)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	loaded, err := dg.LoadAsGraph(context.Background())
	require.NoError(t, err)

	// Compare FindCallersByID and FindCalleesByID for each node.
	for id, origNode := range original.nodes {
		loadedNode, exists := loaded.GetNode(id)
		require.True(t, exists, "node %s should exist in loaded graph", id)

		// Verify symbol fields.
		assert.Equal(t, origNode.Symbol.Name, loadedNode.Symbol.Name)
		assert.Equal(t, origNode.Symbol.Kind, loadedNode.Symbol.Kind)
		assert.Equal(t, origNode.Symbol.FilePath, loadedNode.Symbol.FilePath)
		assert.Equal(t, origNode.Symbol.StartLine, loadedNode.Symbol.StartLine)
		assert.Equal(t, origNode.Symbol.EndLine, loadedNode.Symbol.EndLine)

		// Verify edge counts match.
		assert.Equal(t, len(origNode.Outgoing), len(loadedNode.Outgoing),
			"outgoing edge count mismatch for %s", id)
		assert.Equal(t, len(origNode.Incoming), len(loadedNode.Incoming),
			"incoming edge count mismatch for %s", id)
	}
}

func TestDiskGraph_GetNode(t *testing.T) {
	path, original := materializeTestGraph(t, 10, 2)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	// Get a node that exists.
	for id, origNode := range original.nodes {
		node, found := dg.GetNode(id)
		require.True(t, found, "node %s should be found", id)

		// Verify Symbol fields.
		assert.Equal(t, origNode.Symbol.Name, node.Symbol.Name)
		assert.Equal(t, origNode.Symbol.Kind, node.Symbol.Kind)
		assert.Equal(t, origNode.Symbol.FilePath, node.Symbol.FilePath)
		assert.Equal(t, origNode.Symbol.StartLine, node.Symbol.StartLine)
		assert.Equal(t, origNode.Symbol.EndLine, node.Symbol.EndLine)
		assert.Equal(t, origNode.Symbol.Signature, node.Symbol.Signature)
		assert.Equal(t, origNode.Symbol.Language, node.Symbol.Language)

		// Verify edges populated.
		assert.Equal(t, len(origNode.Outgoing), len(node.Outgoing),
			"outgoing edges mismatch for %s", id)
		assert.Equal(t, len(origNode.Incoming), len(node.Incoming),
			"incoming edges mismatch for %s", id)

		break // Just test one to keep it fast.
	}

	// Get a node that doesn't exist.
	_, found := dg.GetNode("nonexistent:1:Foo")
	assert.False(t, found)
}

func TestDiskGraph_SchemaVersion(t *testing.T) {
	t.Run("wrong schema version", func(t *testing.T) {
		// Create a bbolt file with schema_version = "99.0" via the bbolt API.
		badPath := filepath.Join(t.TempDir(), "bad_schema.db")
		db, err := bolt.Open(badPath, 0o600, &bolt.Options{Timeout: 5 * time.Second})
		require.NoError(t, err)

		err = db.Update(func(tx *bolt.Tx) error {
			metaBkt, bErr := tx.CreateBucket(bucketMeta)
			if bErr != nil {
				return bErr
			}
			data, eErr := encodeMetaString("99.0")
			if eErr != nil {
				return eErr
			}
			return metaBkt.Put(metaKeySchemaVersion, data)
		})
		require.NoError(t, err)
		require.NoError(t, db.Close())

		_, err = OpenDiskGraph(badPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported bbolt schema version")
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := OpenDiskGraph(filepath.Join(t.TempDir(), "nonexistent.db"))
		assert.Error(t, err)
	})
}

func TestDiskGraph_CorruptFile(t *testing.T) {
	corruptPath := filepath.Join(t.TempDir(), "corrupt.db")

	// Write garbage data.
	err := os.WriteFile(corruptPath, []byte("not a bbolt file"), 0o600)
	require.NoError(t, err)

	_, err = OpenDiskGraph(corruptPath)
	assert.Error(t, err)
}

func TestDiskGraph_EmptyGraph(t *testing.T) {
	g := NewGraph("/test/empty")
	g.Freeze()

	path := filepath.Join(t.TempDir(), "empty.db")
	err := g.MaterializeToDisk(context.Background(), path)
	require.NoError(t, err)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	assert.Equal(t, 0, dg.NodeCount())
	assert.Equal(t, 0, dg.EdgeCount())
	assert.Equal(t, "/test/empty", dg.ProjectRoot())

	loaded, err := dg.LoadAsGraph(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, loaded.NodeCount())
	assert.Equal(t, 0, loaded.EdgeCount())
	assert.True(t, loaded.IsFrozen())
}

func TestDiskGraph_ConcurrentReads(t *testing.T) {
	path, original := materializeTestGraph(t, 20, 2)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	// Collect some node IDs to look up.
	var nodeIDs []string
	for id := range original.nodes {
		nodeIDs = append(nodeIDs, id)
		if len(nodeIDs) >= 10 {
			break
		}
	}

	// 10 goroutines doing concurrent GetNode.
	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := nodeIDs[idx%len(nodeIDs)]
			node, found := dg.GetNode(id)
			if !found {
				errCh <- fmt.Errorf("goroutine %d: node %s not found", idx, id)
				return
			}
			if node.Symbol == nil {
				errCh <- fmt.Errorf("goroutine %d: node %s has nil symbol", idx, id)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

func TestDiskGraph_IndexLookups(t *testing.T) {
	path, original := materializeTestGraph(t, 10, 1)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	// Test name index.
	for _, node := range original.nodes {
		ids := dg.nodesByNameFromDisk(node.Symbol.Name)
		assert.NotEmpty(t, ids, "name index should have entries for %s", node.Symbol.Name)
		assert.Contains(t, ids, node.ID)
		break // Just test one.
	}

	// Test kind index.
	funcIDs := dg.nodesByKindFromDisk(ast.SymbolKindFunction)
	assert.Equal(t, 10, len(funcIDs), "all 10 nodes are functions")

	// Test file index.
	fileIDs := dg.nodesByFileFromDisk("file0.go")
	assert.NotEmpty(t, fileIDs, "file index should have entries for file0.go")

	// Test missing entries.
	assert.Nil(t, dg.nodesByNameFromDisk("NonExistentSymbol"))
	assert.Nil(t, dg.nodesByKindFromDisk(ast.SymbolKindInterface))
	assert.Nil(t, dg.nodesByFileFromDisk("nonexistent.go"))
}

func TestDiskGraph_FileMtimesIsolation(t *testing.T) {
	path, _ := materializeTestGraph(t, 5, 1)

	dg, err := OpenDiskGraph(path)
	require.NoError(t, err)
	defer dg.Close()

	// Modify the returned map.
	mtimes := dg.FileMtimes()
	mtimes["injected.go"] = 999

	// Original should not be affected.
	mtimes2 := dg.FileMtimes()
	_, exists := mtimes2["injected.go"]
	assert.False(t, exists, "FileMtimes should return a copy")
}

func BenchmarkDiskGraph_LoadAsGraph(b *testing.B) {
	// Build a graph with 1000 nodes.
	g := NewGraph("/bench/project")
	for i := 0; i < 1000; i++ {
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("file%d.go:%d:Func%d", i%10, i*10+1, i),
			Name:      fmt.Sprintf("Func%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("file%d.go", i%10),
			StartLine: i*10 + 1,
			EndLine:   i*10 + 9,
			Language:  "go",
		}
		_, _ = g.AddNode(sym)
	}

	nodes := make([]string, 0, 1000)
	for id := range g.nodes {
		nodes = append(nodes, id)
	}
	for i := 0; i+1 < len(nodes); i++ {
		_ = g.AddEdge(nodes[i], nodes[i+1], EdgeTypeCalls, ast.Location{
			FilePath:  "bench.go",
			StartLine: i + 1,
			EndLine:   i + 1,
		})
	}
	g.Freeze()

	path := filepath.Join(b.TempDir(), "bench.db")
	require.NoError(b, g.MaterializeToDisk(context.Background(), path))

	ctx := context.Background()

	dg, err := OpenDiskGraph(path)
	require.NoError(b, err)
	defer dg.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err = dg.LoadAsGraph(ctx)
		require.NoError(b, err)
	}
}

// copyFile copies src to dst.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))
}
