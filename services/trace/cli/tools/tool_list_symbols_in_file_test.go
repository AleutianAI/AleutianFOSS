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
	"strings"
	"testing"
)

func TestListSymbolsInFileTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx, _ := createTestGraphWithSourceFiles(t)
	tool := NewListSymbolsInFileTool(g, idx)

	t.Run("lists symbols in file", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "pkg/config/config.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(ListSymbolsInFileOutput)
		if !ok {
			t.Fatalf("Output is not ListSymbolsInFileOutput, got %T", result.Output)
		}

		if output.Path != "pkg/config/config.go" {
			t.Errorf("Path = %q, want %q", output.Path, "pkg/config/config.go")
		}

		// Should find ParseConfig and Config in this file
		if output.SymbolCount != 2 {
			t.Errorf("SymbolCount = %d, want 2", output.SymbolCount)
		}

		// Symbols should be sorted by line number
		if len(output.Symbols) >= 2 {
			if output.Symbols[0].Line >= output.Symbols[1].Line {
				t.Errorf("Symbols not sorted by line: %d >= %d",
					output.Symbols[0].Line, output.Symbols[1].Line)
			}
		}

		// Check specific symbol fields
		foundParseConfig := false
		foundConfig := false
		for _, sym := range output.Symbols {
			if sym.Name == "ParseConfig" {
				foundParseConfig = true
				if sym.Kind != "function" {
					t.Errorf("ParseConfig kind = %q, want %q", sym.Kind, "function")
				}
				if !sym.Exported {
					t.Error("ParseConfig should be exported")
				}
			}
			if sym.Name == "Config" {
				foundConfig = true
				if sym.Kind != "struct" {
					t.Errorf("Config kind = %q, want %q", sym.Kind, "struct")
				}
			}
		}
		if !foundParseConfig {
			t.Error("ParseConfig not found in symbols")
		}
		if !foundConfig {
			t.Error("Config not found in symbols")
		}
	})

	t.Run("returns empty for unknown file", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "nonexistent/file.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with empty results")
		}

		output := result.Output.(ListSymbolsInFileOutput)
		if output.SymbolCount != 0 {
			t.Errorf("SymbolCount = %d, want 0", output.SymbolCount)
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
	})

	t.Run("output text contains table", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "pkg/config/config.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(result.OutputText, "| Line |") {
			t.Error("OutputText should contain a table header")
		}
		if !strings.Contains(result.OutputText, "ParseConfig") {
			t.Error("OutputText should contain ParseConfig")
		}
	})

	t.Run("has trace step", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"path": "pkg/config/config.go",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.TraceStep == nil {
			t.Error("TraceStep should not be nil")
		}
	})
}

func TestListSymbolsInFileTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "list_symbols_in_file" {
			found = true
			if _, ok := def.Parameters["path"]; !ok {
				t.Error("missing 'path' parameter")
			}
			break
		}
	}
	if !found {
		t.Error("list_symbols_in_file not found in StaticToolDefinitions")
	}
}
