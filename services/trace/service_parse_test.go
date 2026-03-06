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
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCRS23_ParallelParseProjectToResults verifies that parallel file parsing
// produces correct results: all files parsed, errors collected, walk order preserved.
func TestCRS23_ParallelParseProjectToResults(t *testing.T) {
	// Create a temp project with Go files
	tmpDir := t.TempDir()

	goFiles := map[string]string{
		"main.go": `package main

func main() {}
`,
		"util.go": `package main

func helper() string { return "ok" }
`,
		"sub/handler.go": `package sub

type Handler struct{}

func (h *Handler) Handle() {}
`,
	}

	for relPath, content := range goFiles {
		absPath := filepath.Join(tmpDir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	results, stats, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("parseProjectToResults failed: %v", err)
	}

	if stats.FilesParsed != 3 {
		t.Errorf("expected 3 files parsed, got %d", stats.FilesParsed)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 parse results, got %d", len(results))
	}
	if len(stats.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(stats.Errors), stats.Errors)
	}

	// Verify all files are represented
	filesSeen := make(map[string]bool)
	for _, pr := range results {
		filesSeen[pr.FilePath] = true
	}
	for relPath := range goFiles {
		if !filesSeen[relPath] {
			t.Errorf("missing parse result for %s", relPath)
		}
	}
}

// TestCRS23_ParallelParseContextCancellation verifies that parsing stops
// promptly when context is cancelled.
func TestCRS23_ParallelParseContextCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some Go files
	for i := 0; i < 10; i++ {
		name := filepath.Join(tmpDir, "file"+string(rune('a'+i))+".go")
		content := "package main\n\nfunc fn" + string(rune('A'+i)) + "() {}\n"
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(DefaultServiceConfig())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	results, stats, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
	// Either nil error (walk completed before ctx check) or a wrapped
	// context.Canceled is acceptable. The key assertion: no panic.
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected nil or context.Canceled, got %v", err)
	}
	// CR-23-2: Even on cancellation, partial results should be returned.
	if err != nil && stats == nil {
		t.Fatal("expected non-nil stats even on cancellation")
	}
	// results may be nil (walk cancelled before any files collected) or partial.
	_ = results
}

// TestCRS23_ParallelParseEmptyProject verifies that an empty project produces
// no results and no errors.
func TestCRS23_ParallelParseEmptyProject(t *testing.T) {
	tmpDir := t.TempDir()

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	results, stats, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if stats.FilesParsed != 0 {
		t.Errorf("expected 0 files parsed, got %d", stats.FilesParsed)
	}
}

// TestCRS23_ParallelParseExclusions verifies that excluded files are not parsed.
func TestCRS23_ParallelParseExclusions(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"main.go":      "package main\n\nfunc main() {}\n",
		"main_test.go": "package main\n\nfunc TestMain() {}\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, relPath), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	results, stats, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, []string{"*_test.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.FilesParsed != 1 {
		t.Errorf("expected 1 file parsed (test file excluded), got %d", stats.FilesParsed)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// TestCRS23_ParallelParseDeterministicOrder verifies that parallel parsing
// produces results in the same walk order across multiple runs.
func TestCRS23_ParallelParseDeterministicOrder(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with predictable sort order
	fileNames := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	for _, name := range fileNames {
		content := "package main\n\nfunc fn_" + name[:1] + "() {}\n"
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(DefaultServiceConfig())
	ctx := context.Background()

	// Run 5 times and verify order is consistent
	var firstOrder []string
	for run := 0; run < 5; run++ {
		results, _, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}

		var order []string
		for _, pr := range results {
			order = append(order, pr.FilePath)
		}

		if run == 0 {
			firstOrder = order
		} else {
			if len(order) != len(firstOrder) {
				t.Fatalf("run %d: got %d results, expected %d", run, len(order), len(firstOrder))
			}
			for i := range order {
				if order[i] != firstOrder[i] {
					t.Errorf("run %d: result[%d] = %s, expected %s", run, i, order[i], firstOrder[i])
				}
			}
		}
	}
}

// TestCRS23_ParallelParseMaxFilesLimit verifies that the MaxProjectFiles limit
// is enforced during the collection phase.
func TestCRS23_ParallelParseMaxFilesLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 5 files but set limit to 3
	for i := 0; i < 5; i++ {
		name := filepath.Join(tmpDir, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultServiceConfig()
	cfg.MaxProjectFiles = 3
	svc := NewService(cfg)
	ctx := context.Background()

	_, _, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
	if err != ErrProjectTooLarge {
		t.Fatalf("expected ErrProjectTooLarge, got %v", err)
	}
}

// TestCRS23_ParallelParseMaxProjectSizeLimit verifies that the MaxProjectSize
// limit is enforced during the collection phase based on cumulative file size.
func TestCRS23_ParallelParseMaxProjectSizeLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 3 files, each ~100 bytes. Set limit to 250 bytes so the 3rd
	// file pushes past the limit.
	content := "package main\n\n// padding to reach ~100 bytes of content per file\nfunc placeholder() { _ = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaa\" }\n"
	for i := 0; i < 3; i++ {
		name := filepath.Join(tmpDir, "file"+string(rune('a'+i))+".go")
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultServiceConfig()
	cfg.MaxProjectSize = 250 // 2 files fit, 3rd exceeds
	svc := NewService(cfg)
	ctx := context.Background()

	_, _, err := svc.parseProjectToResults(ctx, tmpDir, []string{"go"}, nil)
	if err != ErrProjectTooLarge {
		t.Fatalf("expected ErrProjectTooLarge, got %v", err)
	}
}
