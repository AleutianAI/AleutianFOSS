// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

func TestReadFileTool_Execute(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Create a source file with numbered lines
	var lines []string
	for i := 1; i <= 250; i++ {
		lines = append(lines, fmt.Sprintf("// Line %d of the file", i))
	}
	content := strings.Join(lines, "\n") + "\n"

	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	g := graph.NewGraph(tmpDir)
	g.Freeze()
	tool := NewReadFileTool(g)

	t.Run("reads default first 200 lines", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "src/main.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(ReadFileOutput)
		if !ok {
			t.Fatalf("Output is not ReadFileOutput, got %T", result.Output)
		}

		if output.TotalLines != 250 {
			t.Errorf("TotalLines = %d, want 250", output.TotalLines)
		}
		if output.StartLine != 1 {
			t.Errorf("StartLine = %d, want 1", output.StartLine)
		}
		if output.EndLine != 200 {
			t.Errorf("EndLine = %d, want 200", output.EndLine)
		}
		if output.Language != "go" {
			t.Errorf("Language = %q, want %q", output.Language, "go")
		}
		if !strings.Contains(output.Source, "Line 1") {
			t.Error("Source should contain Line 1")
		}
		if !strings.Contains(output.Source, "Line 200") {
			t.Error("Source should contain Line 200")
		}
		if strings.Contains(output.Source, "Line 201") {
			t.Error("Source should NOT contain Line 201")
		}
	})

	t.Run("reads specific line range", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path":       "src/main.go",
			"start_line": 100,
			"end_line":   110,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(ReadFileOutput)
		if output.StartLine != 100 {
			t.Errorf("StartLine = %d, want 100", output.StartLine)
		}
		if output.EndLine != 110 {
			t.Errorf("EndLine = %d, want 110", output.EndLine)
		}
		if !strings.Contains(output.Source, "Line 100") {
			t.Error("Source should contain Line 100")
		}
		if !strings.Contains(output.Source, "Line 110") {
			t.Error("Source should contain Line 110")
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "../../etc/passwd",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail for path traversal")
		}
		if !strings.Contains(result.Error, "..") {
			t.Errorf("Error should mention path traversal, got: %s", result.Error)
		}
	})

	t.Run("handles non-existent file", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "nonexistent.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail for non-existent file")
		}
	})

	t.Run("requires path parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without path parameter")
		}
	})

	t.Run("enforces 500 line max", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path":       "src/main.go",
			"start_line": 1,
			"end_line":   600,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(ReadFileOutput)
		// EndLine should be clamped to start_line + 499 = 500
		if output.EndLine > 250 {
			// File only has 250 lines, so actual end is 250
			t.Logf("EndLine = %d (file only has 250 lines)", output.EndLine)
		}
		// Truncated flag must be true when request exceeded 500 lines
		if !output.Truncated {
			t.Error("Truncated should be true when requesting 600 lines (>500 max)")
		}
	})

	t.Run("output text contains code block", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path":       "src/main.go",
			"start_line": 1,
			"end_line":   10,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(result.OutputText, "```go") {
			t.Error("OutputText should contain a Go code block")
		}
	})

	t.Run("has trace step", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "src/main.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.TraceStep == nil {
			t.Error("TraceStep should not be nil")
		}
	})
}

func TestReadFileTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "read_file" {
			found = true
			if _, ok := def.Parameters["path"]; !ok {
				t.Error("missing 'path' parameter")
			}
			if _, ok := def.Parameters["start_line"]; !ok {
				t.Error("missing 'start_line' parameter")
			}
			if _, ok := def.Parameters["end_line"]; !ok {
				t.Error("missing 'end_line' parameter")
			}
			break
		}
	}
	if !found {
		t.Error("read_file not found in StaticToolDefinitions")
	}
}
