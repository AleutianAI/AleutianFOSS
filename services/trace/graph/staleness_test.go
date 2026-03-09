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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestNewStalenessChecker_NilMtimes(t *testing.T) {
	sc := NewStalenessChecker("/tmp", nil)
	if sc != nil {
		t.Error("expected nil checker for nil mtimes")
	}
}

func TestNewStalenessChecker_EmptyMtimes(t *testing.T) {
	sc := NewStalenessChecker("/tmp", map[string]int64{})
	if sc != nil {
		t.Error("expected nil checker for empty mtimes")
	}
}

func TestStalenessChecker_NoChanges(t *testing.T) {
	// Create a temp dir with a file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Record the current mtime
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mtimes := map[string]int64{
		"main.go": info.ModTime().Unix(),
	}

	sc := NewStalenessChecker(tmpDir, mtimes)
	if sc == nil {
		t.Fatal("expected non-nil checker")
	}

	result := sc.Check()
	if result.IsStale {
		t.Error("expected not stale when files unchanged")
	}
	if result.ChangedFileCount != 0 {
		t.Errorf("expected 0 changed files, got %d", result.ChangedFileCount)
	}
	if result.TotalFileCount != 1 {
		t.Errorf("expected 1 total file, got %d", result.TotalFileCount)
	}
}

func TestStalenessChecker_FileModified(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Record an old mtime (earlier than current)
	mtimes := map[string]int64{
		"main.go": time.Now().Add(-10 * time.Second).Unix(),
	}

	sc := NewStalenessChecker(tmpDir, mtimes)
	result := sc.Check()

	if !result.IsStale {
		t.Error("expected stale when file mtime differs")
	}
	if result.ChangedFileCount != 1 {
		t.Errorf("expected 1 changed file, got %d", result.ChangedFileCount)
	}
}

func TestStalenessChecker_FileDeleted(t *testing.T) {
	tmpDir := t.TempDir()

	// Reference a file that doesn't exist
	mtimes := map[string]int64{
		"deleted.go": time.Now().Unix(),
	}

	sc := NewStalenessChecker(tmpDir, mtimes)
	result := sc.Check()

	if !result.IsStale {
		t.Error("expected stale when file deleted")
	}
	if result.ChangedFileCount != 1 {
		t.Errorf("expected 1 changed (deleted) file, got %d", result.ChangedFileCount)
	}
}

func TestStalenessChecker_CacheResult(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, _ := os.Stat(testFile)
	mtimes := map[string]int64{
		"main.go": info.ModTime().Unix(),
	}

	sc := NewStalenessChecker(tmpDir, mtimes)

	// First check
	result1 := sc.Check()
	if result1.IsStale {
		t.Fatal("expected not stale on first check")
	}

	// Second check should return cached result
	result2 := sc.Check()
	if result2.CheckedAt != result1.CheckedAt {
		t.Error("expected cached result with same CheckedAt")
	}
}

func TestStalenessChecker_InvalidateCache(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	info, _ := os.Stat(testFile)
	mtimes := map[string]int64{
		"main.go": info.ModTime().Unix(),
	}

	sc := NewStalenessChecker(tmpDir, mtimes)

	// Check and cache
	result1 := sc.Check()

	// Invalidate
	sc.InvalidateCache()

	// Next check should be fresh (different CheckedAt)
	// Small sleep to ensure time.Now() differs
	time.Sleep(2 * time.Millisecond)
	result2 := sc.Check()

	if result2.CheckedAt.Equal(result1.CheckedAt) {
		t.Error("expected fresh result after cache invalidation")
	}
}

func TestStalenessChecker_PercentChanged(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 5 files
	for i := 0; i < 5; i++ {
		name := filepath.Join(tmpDir, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte("package main"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// Record correct mtimes for 3 files, wrong for 2
	mtimes := make(map[string]int64)
	for i := 0; i < 5; i++ {
		name := "file" + string(rune('a'+i)) + ".go"
		absPath := filepath.Join(tmpDir, name)
		info, _ := os.Stat(absPath)
		if i < 3 {
			mtimes[name] = info.ModTime().Unix() // correct
		} else {
			mtimes[name] = info.ModTime().Unix() - 100 // stale
		}
	}

	sc := NewStalenessChecker(tmpDir, mtimes)
	result := sc.Check()

	if !result.IsStale {
		t.Error("expected stale")
	}
	if result.ChangedFileCount != 2 {
		t.Errorf("expected 2 changed, got %d", result.ChangedFileCount)
	}
	// 2/5 = 0.4
	if result.PercentChanged < 0.39 || result.PercentChanged > 0.41 {
		t.Errorf("expected ~0.4 percent changed, got %f", result.PercentChanged)
	}
	if !result.SampleBased {
		// 5 files < StalenessCheckSampleSize (50), so NOT sample-based
		t.Log("correctly not sample-based for 5 files")
	}
}

func TestStalenessChecker_SampleBased(t *testing.T) {
	tmpDir := t.TempDir()

	// Create more files than the sample size
	count := StalenessCheckSampleSize + 20
	mtimes := make(map[string]int64)
	for i := 0; i < count; i++ {
		name := filepath.Join(tmpDir, "file_"+string(rune('a'+(i%26)))+string(rune('0'+(i/26)))+".go")
		if err := os.WriteFile(name, []byte("package main"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		info, _ := os.Stat(name)
		relName := filepath.Base(name)
		mtimes[relName] = info.ModTime().Unix()
	}

	sc := NewStalenessChecker(tmpDir, mtimes)
	result := sc.Check()

	if result.SampleBased != true {
		t.Error("expected sample-based check for large file set")
	}
	if result.TotalFileCount != StalenessCheckSampleSize {
		t.Errorf("expected %d checked files, got %d", StalenessCheckSampleSize, result.TotalFileCount)
	}
}

func TestRecordFileMtimes(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Build a minimal graph with a node referencing the file
	g := NewGraph(tmpDir)
	sym := &ast.Symbol{
		ID:        "main.go:1:main",
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "main.go",
		StartLine: 1,
		EndLine:   5,
		Language:  "go",
	}
	if _, err := g.AddNode(sym); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	g.Freeze()

	// Record mtimes
	RecordFileMtimes(g, tmpDir)

	if len(g.FileMtimes) != 1 {
		t.Fatalf("expected 1 file mtime, got %d", len(g.FileMtimes))
	}

	mtime, ok := g.FileMtimes["main.go"]
	if !ok {
		t.Fatal("expected main.go in FileMtimes")
	}

	info, _ := os.Stat(testFile)
	if mtime != info.ModTime().Unix() {
		t.Errorf("expected mtime %d, got %d", info.ModTime().Unix(), mtime)
	}
}

func TestRecordFileMtimes_NilGraph(t *testing.T) {
	// Should not panic
	RecordFileMtimes(nil, "/tmp")
}
