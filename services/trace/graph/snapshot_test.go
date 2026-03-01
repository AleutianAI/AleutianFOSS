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
	"log/slog"
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/dgraph-io/badger/v4"
)

// newTestDB creates an in-memory BadgerDB for testing.
func newTestDB(t *testing.T) *badger.DB {
	t.Helper()
	opts := badger.DefaultOptions("").WithInMemory(true).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("failed to open in-memory badger: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestSnapshotManager creates a SnapshotManager with in-memory DB.
func newTestSnapshotManager(t *testing.T) *SnapshotManager {
	t.Helper()
	db := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mgr, err := NewSnapshotManager(db, logger)
	if err != nil {
		t.Fatalf("NewSnapshotManager: %v", err)
	}
	return mgr
}

// buildSnapshotTestGraph creates a small test graph for snapshot tests.
func buildSnapshotTestGraph() *Graph {
	g := NewGraph("/test/project")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	g.AddNode(symA)
	g.AddNode(symB)
	g.AddEdge("file.go:1:funcA", "file.go:10:funcB", EdgeTypeCalls, makeLocation("file.go", 5))
	g.Freeze()
	return g
}

func TestNewSnapshotManager_NilDB(t *testing.T) {
	logger := slog.Default()
	_, err := NewSnapshotManager(nil, logger)
	if err == nil {
		t.Error("expected error for nil DB")
	}
}

func TestNewSnapshotManager_NilLogger(t *testing.T) {
	db := newTestDB(t)
	_, err := NewSnapshotManager(db, nil)
	if err == nil {
		t.Error("expected error for nil logger")
	}
}

func TestSnapshotManager_SaveAndLoad(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()
	g := buildSnapshotTestGraph()

	// Save
	meta, err := mgr.Save(ctx, g, "test snapshot")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if meta.SnapshotID == "" {
		t.Error("snapshot ID should not be empty")
	}
	if meta.ProjectRoot != "/test/project" {
		t.Errorf("project root = %q, want %q", meta.ProjectRoot, "/test/project")
	}
	if meta.NodeCount != 2 {
		t.Errorf("node count = %d, want 2", meta.NodeCount)
	}
	if meta.EdgeCount != 1 {
		t.Errorf("edge count = %d, want 1", meta.EdgeCount)
	}
	if meta.Label != "test snapshot" {
		t.Errorf("label = %q, want %q", meta.Label, "test snapshot")
	}
	if meta.CompressedSize <= 0 {
		t.Error("compressed size should be > 0")
	}
	if meta.ContentHash == "" {
		t.Error("content hash should not be empty")
	}
	if meta.SchemaVersion != GraphSchemaVersion {
		t.Errorf("schema version = %q, want %q", meta.SchemaVersion, GraphSchemaVersion)
	}

	// Load
	loaded, loadedMeta, err := mgr.Load(ctx, meta.SnapshotID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.NodeCount() != g.NodeCount() {
		t.Errorf("loaded node count = %d, want %d", loaded.NodeCount(), g.NodeCount())
	}
	if loaded.EdgeCount() != g.EdgeCount() {
		t.Errorf("loaded edge count = %d, want %d", loaded.EdgeCount(), g.EdgeCount())
	}
	if loaded.Hash() != g.Hash() {
		t.Errorf("loaded hash = %q, want %q", loaded.Hash(), g.Hash())
	}
	if loadedMeta.Label != "test snapshot" {
		t.Errorf("loaded label = %q, want %q", loadedMeta.Label, "test snapshot")
	}
}

func TestSnapshotManager_SaveNilGraph(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	_, err := mgr.Save(ctx, nil, "")
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestSnapshotManager_LoadNonexistent(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	_, _, err := mgr.Load(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent snapshot")
	}
}

func TestSnapshotManager_LoadLatest(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()
	g := buildSnapshotTestGraph()

	meta, err := mgr.Save(ctx, g, "latest test")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, loadedMeta, err := mgr.LoadLatest(ctx, meta.ProjectHash)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}

	if loaded.NodeCount() != g.NodeCount() {
		t.Errorf("loaded node count = %d, want %d", loaded.NodeCount(), g.NodeCount())
	}
	if loadedMeta.SnapshotID != meta.SnapshotID {
		t.Errorf("loaded snapshot ID = %q, want %q", loadedMeta.SnapshotID, meta.SnapshotID)
	}
}

func TestSnapshotManager_List(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	// Save two snapshots for different projects
	g1 := buildSnapshotTestGraph()
	meta1, err := mgr.Save(ctx, g1, "first")
	if err != nil {
		t.Fatalf("Save 1: %v", err)
	}

	g2 := NewGraph("/other/project")
	symX := makeSymbol("x.go:1:funcX", "funcX", ast.SymbolKindFunction, "x.go")
	g2.AddNode(symX)
	g2.Freeze()
	_, err = mgr.Save(ctx, g2, "second")
	if err != nil {
		t.Fatalf("Save 2: %v", err)
	}

	// List all
	all, err := mgr.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list all = %d, want 2", len(all))
	}

	// List filtered by project
	filtered, err := mgr.List(ctx, meta1.ProjectHash, 100)
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Errorf("list filtered = %d, want 1", len(filtered))
	}
	if filtered[0].ProjectRoot != "/test/project" {
		t.Errorf("filtered project = %q, want %q", filtered[0].ProjectRoot, "/test/project")
	}
}

func TestSnapshotManager_ListLimit(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	// Save two snapshots (different BuiltAtMilli to get different IDs)
	g1 := buildSnapshotTestGraph()
	mgr.Save(ctx, g1, "first")

	g2 := NewGraph("/other/project")
	symX := makeSymbol("x.go:1:funcX", "funcX", ast.SymbolKindFunction, "x.go")
	g2.AddNode(symX)
	g2.Freeze()
	mgr.Save(ctx, g2, "second")

	// Limit to 1
	limited, err := mgr.List(ctx, "", 1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("list limited = %d, want 1", len(limited))
	}
}

func TestSnapshotManager_Delete(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()
	g := buildSnapshotTestGraph()

	meta, err := mgr.Save(ctx, g, "to delete")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify it exists
	_, _, err = mgr.Load(ctx, meta.SnapshotID)
	if err != nil {
		t.Fatalf("Load before delete: %v", err)
	}

	// Delete
	err = mgr.Delete(ctx, meta.SnapshotID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it's gone
	_, _, err = mgr.Load(ctx, meta.SnapshotID)
	if err == nil {
		t.Error("expected error loading deleted snapshot")
	}

	// List should be empty
	list, err := mgr.List(ctx, meta.ProjectHash, 100)
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("list after delete = %d, want 0", len(list))
	}
}

func TestSnapshotManager_DeleteNonexistent(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	err := mgr.Delete(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent snapshot")
	}
}

func TestSnapshotManager_SaveNilCtx(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	g := buildSnapshotTestGraph()

	//nolint:staticcheck // testing nil ctx
	_, err := mgr.Save(nil, g, "")
	if err == nil {
		t.Error("expected error for nil ctx")
	}
}

func TestSnapshotManager_LoadEmptyID(t *testing.T) {
	mgr := newTestSnapshotManager(t)
	ctx := context.Background()

	_, _, err := mgr.Load(ctx, "")
	if err == nil {
		t.Error("expected error for empty snapshot ID")
	}
}

func TestProjectHash(t *testing.T) {
	h1 := ProjectHash("/test/project")
	h2 := ProjectHash("/test/project")
	h3 := ProjectHash("/other/project")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
	if len(h1) != 16 {
		t.Errorf("hash length = %d, want 16", len(h1))
	}
}
