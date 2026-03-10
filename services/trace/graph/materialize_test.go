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
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

// buildBboltTestGraph creates a small frozen graph for bbolt persistence testing.
func buildBboltTestGraph(t *testing.T, nodeCount, edgesPerNode int) *Graph {
	t.Helper()

	g := NewGraph("/test/project")

	// Create nodes.
	symbols := make([]*ast.Symbol, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("file%d.go:%d:Func%d", i%3, i*10+1, i),
			Name:      fmt.Sprintf("Func%d", i),
			Kind:      ast.SymbolKindFunction,
			FilePath:  fmt.Sprintf("file%d.go", i%3),
			StartLine: i*10 + 1,
			EndLine:   i*10 + 9,
			StartCol:  0,
			EndCol:    1,
			Signature: "func() error",
			Package:   "main",
			Exported:  i%2 == 0,
			Language:  "go",
		}
		symbols = append(symbols, sym)
		_, err := g.AddNode(sym)
		require.NoError(t, err)
	}

	// Create edges between consecutive nodes.
	for i := 0; i < nodeCount; i++ {
		for j := 0; j < edgesPerNode && i+j+1 < nodeCount; j++ {
			err := g.AddEdge(
				symbols[i].ID,
				symbols[i+j+1].ID,
				EdgeTypeCalls,
				ast.Location{
					FilePath:  symbols[i].FilePath,
					StartLine: symbols[i].StartLine + j + 1,
					EndLine:   symbols[i].StartLine + j + 1,
				},
			)
			require.NoError(t, err)
		}
	}

	g.Freeze()
	g.FileMtimes = map[string]int64{
		"file0.go": 1710000000,
		"file1.go": 1710000001,
		"file2.go": 1710000002,
	}

	return g
}

func TestMaterializeToDisk_SmallGraph(t *testing.T) {
	g := buildBboltTestGraph(t, 10, 2)

	path := filepath.Join(t.TempDir(), "test.db")
	err := g.MaterializeToDisk(context.Background(), path)
	require.NoError(t, err)

	// Verify file exists.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.Size() > 0)

	// Open and verify buckets.
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db.Close()

	err = db.View(func(tx *bolt.Tx) error {
		// Verify all buckets exist.
		for _, bkt := range [][]byte{
			bucketNodes, bucketEdgesOut, bucketEdgesIn,
			bucketIdxName, bucketIdxKind, bucketIdxFile, bucketMeta,
		} {
			b := tx.Bucket(bkt)
			require.NotNil(t, b, "bucket %s should exist", string(bkt))
		}

		// Verify node count.
		nodesBkt := tx.Bucket(bucketNodes)
		nodeCount := 0
		_ = nodesBkt.ForEach(func(_, _ []byte) error {
			nodeCount++
			return nil
		})
		assert.Equal(t, 10, nodeCount)

		// Verify metadata.
		metaBkt := tx.Bucket(bucketMeta)

		schemaData := metaBkt.Get(metaKeySchemaVersion)
		require.NotNil(t, schemaData)
		schema, err := decodeMetaString(schemaData)
		require.NoError(t, err)
		assert.Equal(t, BboltSchemaVersion, schema)

		nodeCountData := metaBkt.Get(metaKeyNodeCount)
		require.NotNil(t, nodeCountData)
		nc, err := decodeMetaInt(nodeCountData)
		require.NoError(t, err)
		assert.Equal(t, 10, nc)

		edgeCountData := metaBkt.Get(metaKeyEdgeCount)
		require.NotNil(t, edgeCountData)
		ec, err := decodeMetaInt(edgeCountData)
		require.NoError(t, err)
		assert.Equal(t, g.EdgeCount(), ec)

		projectRootData := metaBkt.Get(metaKeyProjectRoot)
		require.NotNil(t, projectRootData)
		pr, err := decodeMetaString(projectRootData)
		require.NoError(t, err)
		assert.Equal(t, "/test/project", pr)

		return nil
	})
	require.NoError(t, err)
}

func TestMaterializeToDisk_Determinism(t *testing.T) {
	g := buildBboltTestGraph(t, 20, 2)
	dir := t.TempDir()

	path1 := filepath.Join(dir, "graph1.db")
	path2 := filepath.Join(dir, "graph2.db")

	err := g.MaterializeToDisk(context.Background(), path1)
	require.NoError(t, err)

	err = g.MaterializeToDisk(context.Background(), path2)
	require.NoError(t, err)

	// Read both files and compare all bucket contents.
	db1, err := bolt.Open(path1, 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db1.Close()

	db2, err := bolt.Open(path2, 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db2.Close()

	// Compare node bucket contents.
	var nodes1, nodes2 []string
	_ = db1.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNodes).ForEach(func(k, v []byte) error {
			nodes1 = append(nodes1, string(k))
			return nil
		})
	})
	_ = db2.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketNodes).ForEach(func(k, v []byte) error {
			nodes2 = append(nodes2, string(k))
			return nil
		})
	})
	assert.Equal(t, nodes1, nodes2, "node keys should be identical across materializations")

	// Compare metadata.
	var hash1, hash2 string
	_ = db1.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketMeta).Get(metaKeyGraphHash)
		hash1, _ = decodeMetaString(data)
		return nil
	})
	_ = db2.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketMeta).Get(metaKeyGraphHash)
		hash2, _ = decodeMetaString(data)
		return nil
	})
	assert.Equal(t, hash1, hash2, "graph hashes should be identical")
}

func TestMaterializeToDisk_NotFrozen(t *testing.T) {
	g := NewGraph("/test/project")
	sym := &ast.Symbol{
		ID:        "test.go:1:Foo",
		Name:      "Foo",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "test.go",
		StartLine: 1,
		EndLine:   5,
		Language:  "go",
	}
	_, err := g.AddNode(sym)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "test.db")
	err = g.MaterializeToDisk(context.Background(), path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "frozen")
}

func TestMaterializeToDisk_EmptyGraph(t *testing.T) {
	g := NewGraph("/test/project")
	g.Freeze()

	path := filepath.Join(t.TempDir(), "test.db")
	err := g.MaterializeToDisk(context.Background(), path)
	require.NoError(t, err)

	// Verify metadata is correct.
	db, err := bolt.Open(path, 0o600, &bolt.Options{ReadOnly: true})
	require.NoError(t, err)
	defer db.Close()

	err = db.View(func(tx *bolt.Tx) error {
		metaBkt := tx.Bucket(bucketMeta)

		nc, err := decodeMetaInt(metaBkt.Get(metaKeyNodeCount))
		require.NoError(t, err)
		assert.Equal(t, 0, nc)

		ec, err := decodeMetaInt(metaBkt.Get(metaKeyEdgeCount))
		require.NoError(t, err)
		assert.Equal(t, 0, ec)

		return nil
	})
	require.NoError(t, err)
}

func TestMaterializeToDisk_NilGraph(t *testing.T) {
	var g *Graph
	path := filepath.Join(t.TempDir(), "test.db")
	err := g.MaterializeToDisk(context.Background(), path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func BenchmarkMaterializeToDisk(b *testing.B) {
	// Build a graph with 1000 nodes and 2000 edges.
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

	// Add edges.
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
		if i+2 < len(nodes) {
			_ = g.AddEdge(nodes[i], nodes[i+2], EdgeTypeReferences, ast.Location{
				FilePath:  "bench.go",
				StartLine: i + 1,
				EndLine:   i + 1,
			})
		}
	}
	g.Freeze()

	dir := b.TempDir()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("bench_%d.db", i))
		if err := g.MaterializeToDisk(ctx, path); err != nil {
			b.Fatal(err)
		}
	}
}
