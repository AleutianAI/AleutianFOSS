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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// createTestGraphWithSourceFiles creates a graph and index with symbols pointing
// to actual source files on disk (using t.TempDir()).
func createTestGraphWithSourceFiles(t *testing.T) (*graph.Graph, *index.SymbolIndex, string) {
	t.Helper()

	tmpDir := t.TempDir()

	// Create directory structure
	if err := os.MkdirAll(filepath.Join(tmpDir, "pkg", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write source files
	configSource := `package config

import "os"

// ParseConfig reads and parses the configuration file.
// It returns a Config struct or an error if parsing fails.
func ParseConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(data)
}

type Config struct {
	Host string
	Port int
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "pkg", "config", "config.go"), []byte(configSource), 0o644); err != nil {
		t.Fatal(err)
	}

	mainSource := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "cmd", "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}

	g := graph.NewGraph(tmpDir)
	idx := index.NewSymbolIndex()

	parseConfig := &ast.Symbol{
		ID:         "pkg/config/config.go:7:ParseConfig",
		Name:       "ParseConfig",
		Kind:       ast.SymbolKindFunction,
		FilePath:   "pkg/config/config.go",
		StartLine:  7,
		EndLine:    13,
		Package:    "config",
		Signature:  "func ParseConfig(path string) (*Config, error)",
		DocComment: "ParseConfig reads and parses the configuration file.",
		Exported:   true,
		Language:   "go",
	}

	configStruct := &ast.Symbol{
		ID:        "pkg/config/config.go:15:Config",
		Name:      "Config",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/config/config.go",
		StartLine: 15,
		EndLine:   18,
		Package:   "config",
		Signature: "type Config struct",
		Exported:  true,
		Language:  "go",
	}

	mainFn := &ast.Symbol{
		ID:        "cmd/main.go:5:main",
		Name:      "main",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "cmd/main.go",
		StartLine: 5,
		EndLine:   7,
		Package:   "main",
		Signature: "func main()",
		Exported:  false,
		Language:  "go",
	}

	g.AddNode(parseConfig)
	g.AddNode(configStruct)
	g.AddNode(mainFn)
	g.Freeze()

	idx.Add(parseConfig)
	idx.Add(configStruct)
	idx.Add(mainFn)

	return g, idx, tmpDir
}

func TestReadSymbolTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx, _ := createTestGraphWithSourceFiles(t)
	tool := NewReadSymbolTool(g, idx)

	t.Run("reads source code of named function", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "ParseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(ReadSymbolOutput)
		if !ok {
			t.Fatalf("Output is not ReadSymbolOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}

		if len(output.Matches) != 1 {
			t.Fatalf("got %d match entries, want 1", len(output.Matches))
		}

		match := output.Matches[0]
		if match.Name != "ParseConfig" {
			t.Errorf("got name %q, want %q", match.Name, "ParseConfig")
		}
		if match.FilePath != "pkg/config/config.go" {
			t.Errorf("got file %q, want %q", match.FilePath, "pkg/config/config.go")
		}
		if match.StartLine != 7 {
			t.Errorf("got start_line %d, want 7", match.StartLine)
		}
		if match.EndLine != 13 {
			t.Errorf("got end_line %d, want 13", match.EndLine)
		}
		if match.Language != "go" {
			t.Errorf("got language %q, want %q", match.Language, "go")
		}
		if !strings.Contains(match.Source, "func ParseConfig") {
			t.Errorf("source should contain function declaration, got: %s", match.Source)
		}
		if !strings.Contains(match.Source, "os.ReadFile") {
			t.Errorf("source should contain function body, got: %s", match.Source)
		}
	})

	t.Run("returns not found for non-existent symbol", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "nonExistent",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with empty results, got error: %s", result.Error)
		}

		output, ok := result.Output.(ReadSymbolOutput)
		if !ok {
			t.Fatalf("Output is not ReadSymbolOutput, got %T", result.Output)
		}
		if output.MatchCount != 0 {
			t.Errorf("got %d matches, want 0", output.MatchCount)
		}
	})

	t.Run("requires name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without name parameter")
		}
	})

	t.Run("filters by kind", func(t *testing.T) {
		// Config exists as both a struct — filtering by function should return 0
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "Config",
			"kind": "function",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(ReadSymbolOutput)
		if output.MatchCount != 0 {
			t.Errorf("got %d matches with kind=function, want 0 (Config is a struct)", output.MatchCount)
		}

		// Filtering by struct should return 1
		result2, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "Config",
			"kind": "struct",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output2 := result2.Output.(ReadSymbolOutput)
		if output2.MatchCount != 1 {
			t.Errorf("got %d matches with kind=struct, want 1", output2.MatchCount)
		}
	})

	t.Run("output text contains source code block", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "ParseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(result.OutputText, "```go") {
			t.Error("OutputText should contain a Go code block")
		}
		if !strings.Contains(result.OutputText, "ParseConfig") {
			t.Error("OutputText should mention the function name")
		}
	})

	t.Run("has trace step", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "ParseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.TraceStep == nil {
			t.Error("TraceStep should not be nil")
		}
	})
}

func TestReadSymbolTool_PathTraversal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a symbol that points outside the project root
	g := graph.NewGraph(tmpDir)
	idx := index.NewSymbolIndex()

	malicious := &ast.Symbol{
		ID:        "../../etc/passwd:1:root",
		Name:      "root",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "../../etc/passwd",
		StartLine: 1,
		EndLine:   1,
		Language:  "go",
	}
	g.AddNode(malicious)
	g.Freeze()
	idx.Add(malicious)

	tool := NewReadSymbolTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"name": "root",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The tool should succeed but the source should contain an error message
	if result.Success {
		output := result.Output.(ReadSymbolOutput)
		if output.MatchCount > 0 && len(output.Matches) > 0 {
			if !strings.Contains(output.Matches[0].Source, "error") {
				t.Error("Source should contain error for path traversal attempt")
			}
		}
	}
}

func TestReadSymbolTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "read_symbol" {
			found = true
			if def.Priority != 96 {
				t.Errorf("Priority = %d, want 96", def.Priority)
			}
			if _, ok := def.Parameters["name"]; !ok {
				t.Error("missing 'name' parameter")
			}
			break
		}
	}
	if !found {
		t.Error("read_symbol not found in StaticToolDefinitions")
	}
}
