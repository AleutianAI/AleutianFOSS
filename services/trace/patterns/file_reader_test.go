// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestFileReader_ReadLines(t *testing.T) {
	tmpDir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewFileReader(tmpDir)

	t.Run("reads file correctly", func(t *testing.T) {
		lines, err := reader.ReadLines("test.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(lines) != 5 {
			t.Fatalf("expected 5 lines, got %d", len(lines))
		}
		if lines[0] != "line1" {
			t.Errorf("expected 'line1', got '%s'", lines[0])
		}
	})

	t.Run("cache hit returns same data", func(t *testing.T) {
		lines1, _ := reader.ReadLines("test.go")
		lines2, _ := reader.ReadLines("test.go")

		if &lines1[0] != &lines2[0] {
			t.Error("expected cache hit to return same slice")
		}
		if reader.CacheSize() != 1 {
			t.Errorf("expected cache size 1, got %d", reader.CacheSize())
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		_, err := reader.ReadLines("nonexistent.go")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

func TestFileReader_ReadSymbolCode(t *testing.T) {
	tmpDir := t.TempDir()
	content := "package main\n\nfunc hello() {\n\tfmt.Println(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	reader := NewFileReader(tmpDir)

	t.Run("reads symbol code correctly", func(t *testing.T) {
		sym := &ast.Symbol{
			FilePath:  "main.go",
			StartLine: 3,
			EndLine:   5,
		}
		code, err := reader.ReadSymbolCode(sym)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "func hello() {\n\tfmt.Println(\"hello\")\n}" {
			t.Errorf("unexpected code: %s", code)
		}
	})

	t.Run("nil symbol returns error", func(t *testing.T) {
		_, err := reader.ReadSymbolCode(nil)
		if err == nil {
			t.Error("expected error for nil symbol")
		}
	})

	t.Run("out of bounds returns error", func(t *testing.T) {
		sym := &ast.Symbol{
			FilePath:  "main.go",
			StartLine: 1,
			EndLine:   100,
		}
		_, err := reader.ReadSymbolCode(sym)
		if err == nil {
			t.Error("expected error for out of bounds")
		}
	})
}

func TestFileReader_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 10; i++ {
		name := filepath.Join(tmpDir, "file"+itoa(i)+".go")
		if err := os.WriteFile(name, []byte("line1\nline2\nline3"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	reader := NewFileReader(tmpDir)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = reader.ReadLines("file" + itoa(idx) + ".go")
			}
		}(i)
	}

	wg.Wait()

	if reader.CacheSize() != 10 {
		t.Errorf("expected cache size 10, got %d", reader.CacheSize())
	}
}
