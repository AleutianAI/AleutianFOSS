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

func TestGetSignatureTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx, _ := createTestGraphWithSourceFiles(t)
	tool := NewGetSignatureTool(g, idx)

	t.Run("returns signature and doc comment", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "ParseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(GetSignatureOutput)
		if !ok {
			t.Fatalf("Output is not GetSignatureOutput, got %T", result.Output)
		}

		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}

		if len(output.Matches) != 1 {
			t.Fatalf("got %d match entries, want 1", len(output.Matches))
		}

		match := output.Matches[0]
		if match.Name != "ParseConfig" {
			t.Errorf("Name = %q, want %q", match.Name, "ParseConfig")
		}
		if match.Kind != "function" {
			t.Errorf("Kind = %q, want %q", match.Kind, "function")
		}
		if match.Signature != "func ParseConfig(path string) (*Config, error)" {
			t.Errorf("Signature = %q", match.Signature)
		}
		if match.DocComment != "ParseConfig reads and parses the configuration file." {
			t.Errorf("DocComment = %q", match.DocComment)
		}
		if !match.Exported {
			t.Error("Exported should be true")
		}
		if match.Package != "config" {
			t.Errorf("Package = %q, want %q", match.Package, "config")
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
			t.Fatalf("Execute() should succeed with empty results")
		}

		output := result.Output.(GetSignatureOutput)
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

	t.Run("output text contains signature", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "ParseConfig",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !strings.Contains(result.OutputText, "ParseConfig") {
			t.Error("OutputText should contain the function name")
		}
		if !strings.Contains(result.OutputText, "Exported: true") {
			t.Error("OutputText should indicate export status")
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

	t.Run("returns struct signature", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"name": "Config",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output := result.Output.(GetSignatureOutput)
		if output.MatchCount != 1 {
			t.Errorf("got %d matches, want 1", output.MatchCount)
		}
		if len(output.Matches) > 0 && output.Matches[0].Kind != "struct" {
			t.Errorf("Kind = %q, want %q", output.Matches[0].Kind, "struct")
		}
	})
}

func TestGetSignatureTool_StaticDefinitions(t *testing.T) {
	defs := StaticToolDefinitions()
	found := false
	for _, def := range defs {
		if def.Name == "get_signature" {
			found = true
			if _, ok := def.Parameters["name"]; !ok {
				t.Error("missing 'name' parameter")
			}
			break
		}
	}
	if !found {
		t.Error("get_signature not found in StaticToolDefinitions")
	}
}
