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

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

func TestFindImplementationsTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, idx)

	t.Run("finds implementations of Handler", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"interface_name": "Handler",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// UserHandler implements Handler
		output, ok := result.Output.(FindImplementationsOutput)
		if !ok {
			t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
		}

		if len(output.Results) != 1 {
			t.Errorf("got %d result entries, want 1", len(output.Results))
		}
	})

	t.Run("requires interface_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without interface_name")
		}
	})
}

// TestFindImplementationsTool_NilIndexFallback tests nil index fallback.
func TestFindImplementationsTool_NilIndexFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, nil)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Handler",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// Should still find implementations via graph fallback
	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	if len(output.Results) != 1 {
		t.Errorf("got %d result entries, want 1", len(output.Results))
	}
}

func TestFindImplementationsTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphWithCallers(t)
	tool := NewFindImplementationsTool(g, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Handler",
	}})

	if err == nil {
		t.Error("Expected context.Canceled error, got nil")
	}
}

func TestFindImplementationsTool_AcceptsInterfaceClassStruct(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Create symbols with same name but different kinds
	handlerInterface := &ast.Symbol{
		ID:        "handler/handler.go:5:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "handler/handler.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "handler",
		Language:  "go",
	}

	handlerStruct := &ast.Symbol{
		ID:        "other/handler.go:10:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "other/handler.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "other",
		Language:  "go",
	}

	handlerFunc := &ast.Symbol{
		ID:        "util/handler.go:1:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "util/handler.go",
		StartLine: 1,
		EndLine:   5,
		Package:   "util",
		Language:  "go",
	}

	g.AddNode(handlerInterface)
	g.AddNode(handlerStruct)
	g.AddNode(handlerFunc)
	_ = idx.Add(handlerInterface)
	_ = idx.Add(handlerStruct)
	_ = idx.Add(handlerFunc)
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Handler",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	// Both interface and struct should be queried; function should be filtered
	if output.MatchCount != 2 {
		t.Errorf("Expected 2 matches (interface + struct), got %d", output.MatchCount)
	}
}

// TestFindImplementationsTool_ClassInheritance tests that EdgeTypeEmbeds edges
// (used for Python/JS/TS class inheritance) are found by find_implementations (IT-03 C-3b).
func TestFindImplementationsTool_ClassInheritance(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Base class (Python-style)
	baseClass := &ast.Symbol{
		ID:        "app/models.py:10:BaseModel",
		Name:      "BaseModel",
		Kind:      ast.SymbolKindClass,
		FilePath:  "app/models.py",
		StartLine: 10,
		EndLine:   30,
		Package:   "app",
		Language:  "python",
	}

	// Child class that extends BaseModel
	childClass := &ast.Symbol{
		ID:        "app/user.py:5:UserModel",
		Name:      "UserModel",
		Kind:      ast.SymbolKindClass,
		FilePath:  "app/user.py",
		StartLine: 5,
		EndLine:   25,
		Package:   "app",
		Language:  "python",
	}

	g.AddNode(baseClass)
	g.AddNode(childClass)
	_ = idx.Add(baseClass)
	_ = idx.Add(childClass)

	// Add EdgeTypeEmbeds edge (child extends base)
	g.AddEdge(childClass.ID, baseClass.ID, graph.EdgeTypeEmbeds, ast.Location{
		FilePath: childClass.FilePath, StartLine: childClass.StartLine,
	})
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "BaseModel",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	if output.MatchCount != 1 {
		t.Errorf("Expected 1 match for BaseModel, got %d", output.MatchCount)
	}
	if output.TotalImplementations != 1 {
		t.Errorf("Expected 1 implementation (UserModel), got %d", output.TotalImplementations)
	}
	if len(output.Results) > 0 && len(output.Results[0].Implementations) > 0 {
		impl := output.Results[0].Implementations[0]
		if impl.Name != "UserModel" {
			t.Errorf("Expected implementation name 'UserModel', got '%s'", impl.Name)
		}
	} else {
		t.Error("Expected at least one implementation result")
	}
}

// TestFindImplementationsTool_MixedEdgeTypes tests that both EdgeTypeImplements
// and EdgeTypeEmbeds edges are returned without duplicates (IT-03 C-3c).
func TestFindImplementationsTool_MixedEdgeTypes(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Interface target
	iface := &ast.Symbol{
		ID:        "pkg/iface.go:5:Serializer",
		Name:      "Serializer",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "pkg/iface.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "pkg",
		Language:  "go",
	}

	// Type that implements via EdgeTypeImplements
	implType := &ast.Symbol{
		ID:        "pkg/json.go:10:JSONSerializer",
		Name:      "JSONSerializer",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/json.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Language:  "go",
	}

	// Type that embeds via EdgeTypeEmbeds
	embedType := &ast.Symbol{
		ID:        "pkg/xml.go:10:XMLSerializer",
		Name:      "XMLSerializer",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/xml.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Language:  "go",
	}

	g.AddNode(iface)
	g.AddNode(implType)
	g.AddNode(embedType)
	_ = idx.Add(iface)
	_ = idx.Add(implType)
	_ = idx.Add(embedType)

	// Add both edge types pointing to the same target
	g.AddEdge(implType.ID, iface.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: implType.FilePath, StartLine: implType.StartLine,
	})
	g.AddEdge(embedType.ID, iface.ID, graph.EdgeTypeEmbeds, ast.Location{
		FilePath: embedType.FilePath, StartLine: embedType.StartLine,
	})
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Serializer",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	if output.MatchCount != 1 {
		t.Errorf("Expected 1 match (Serializer interface), got %d", output.MatchCount)
	}
	if output.TotalImplementations != 2 {
		t.Errorf("Expected 2 implementations (JSON + XML), got %d", output.TotalImplementations)
	}

	// Verify no duplicates: add a duplicate edge and confirm count stays the same
	// (The dedup is tested implicitly - if both edge types pointed from the same
	// node, dedup would collapse them to 1)
}

// TestFindImplementationsTool_DedupSameSymbolBothEdgeTypes tests that a single symbol
// with both EdgeTypeImplements AND EdgeTypeEmbeds to the same target appears only
// once in results (IT-03 C-3 dedup correctness).
func TestFindImplementationsTool_DedupSameSymbolBothEdgeTypes(t *testing.T) {
	ctx := context.Background()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Target interface
	iface := &ast.Symbol{
		ID:        "pkg/iface.go:5:Store",
		Name:      "Store",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "pkg/iface.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "pkg",
		Language:  "go",
	}

	// Single type with BOTH edge types to the interface
	impl := &ast.Symbol{
		ID:        "pkg/mem.go:10:MemStore",
		Name:      "MemStore",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/mem.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Language:  "go",
	}

	g.AddNode(iface)
	g.AddNode(impl)
	_ = idx.Add(iface)
	_ = idx.Add(impl)

	// Add both edge types from the SAME source to SAME target
	g.AddEdge(impl.ID, iface.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: impl.FilePath, StartLine: impl.StartLine,
	})
	g.AddEdge(impl.ID, iface.ID, graph.EdgeTypeEmbeds, ast.Location{
		FilePath: impl.FilePath, StartLine: impl.StartLine,
	})
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Store",
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindImplementationsOutput)
	if !ok {
		t.Fatalf("Output is not FindImplementationsOutput, got %T", result.Output)
	}

	// MemStore should appear only once despite having both edge types
	if output.TotalImplementations != 1 {
		t.Errorf("Expected 1 unique implementation (dedup), got %d", output.TotalImplementations)
	}
}

// TestFindImplementationsTool_TextOutputLabels tests that formatText produces
// language-appropriate labels for different target symbol kinds (IT-03 C-3).
func TestFindImplementationsTool_TextOutputLabels(t *testing.T) {
	ctx := context.Background()

	t.Run("class target shows Base class label", func(t *testing.T) {
		g := graph.NewGraph("/test")
		idx := index.NewSymbolIndex()

		base := &ast.Symbol{
			ID:        "app/base.py:10:BaseView",
			Name:      "BaseView",
			Kind:      ast.SymbolKindClass,
			FilePath:  "app/base.py",
			StartLine: 10,
			EndLine:   30,
			Package:   "app",
			Language:  "python",
		}
		child := &ast.Symbol{
			ID:        "app/views.py:5:UserView",
			Name:      "UserView",
			Kind:      ast.SymbolKindClass,
			FilePath:  "app/views.py",
			StartLine: 5,
			EndLine:   25,
			Package:   "app",
			Language:  "python",
		}

		g.AddNode(base)
		g.AddNode(child)
		_ = idx.Add(base)
		_ = idx.Add(child)
		g.AddEdge(child.ID, base.ID, graph.EdgeTypeEmbeds, ast.Location{
			FilePath: child.FilePath, StartLine: child.StartLine,
		})
		g.Freeze()

		tool := NewFindImplementationsTool(g, idx)
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"interface_name": "BaseView"}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result.OutputText, "Base class") {
			t.Errorf("Expected text output to contain 'Base class', got:\n%s", result.OutputText)
		}
		if !strings.Contains(result.OutputText, "UserView") {
			t.Errorf("Expected text output to contain 'UserView', got:\n%s", result.OutputText)
		}
	})

	t.Run("interface target shows Interface label", func(t *testing.T) {
		g := graph.NewGraph("/test")
		idx := index.NewSymbolIndex()

		iface := &ast.Symbol{
			ID:        "pkg/reader.go:5:Reader",
			Name:      "Reader",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "pkg/reader.go",
			StartLine: 5,
			EndLine:   10,
			Package:   "pkg",
			Language:  "go",
		}
		impl := &ast.Symbol{
			ID:        "pkg/file.go:10:FileReader",
			Name:      "FileReader",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "pkg/file.go",
			StartLine: 10,
			EndLine:   20,
			Package:   "pkg",
			Language:  "go",
		}

		g.AddNode(iface)
		g.AddNode(impl)
		_ = idx.Add(iface)
		_ = idx.Add(impl)
		g.AddEdge(impl.ID, iface.ID, graph.EdgeTypeImplements, ast.Location{
			FilePath: impl.FilePath, StartLine: impl.StartLine,
		})
		g.Freeze()

		tool := NewFindImplementationsTool(g, idx)
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{"interface_name": "Reader"}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result.OutputText, "Interface") {
			t.Errorf("Expected text output to contain 'Interface', got:\n%s", result.OutputText)
		}
	})
}

// TestFindImplementationsTool_TraceStepPopulated verifies CRS integration on success path.
func TestFindImplementationsTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Handler",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}
	if result.TraceStep.Action != "tool_find_implementations" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_implementations'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_implementations" {
		t.Errorf("TraceStep.Tool = %q, want 'find_implementations'", result.TraceStep.Tool)
	}
	if result.TraceStep.Target != "Handler" {
		t.Errorf("TraceStep.Target = %q, want 'Handler'", result.TraceStep.Target)
	}
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}

	if result.TraceStep.Metadata == nil {
		t.Fatal("TraceStep.Metadata should not be nil")
	}
	for _, key := range []string{"match_count", "total_implementations", "index_used"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestFindImplementationsTool_TraceStepOnError verifies CRS integration on validation error path.
func TestFindImplementationsTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewFindImplementationsTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty interface_name")
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_find_implementations" {
		t.Errorf("TraceStep.Action = %q, want 'tool_find_implementations'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "find_implementations" {
		t.Errorf("TraceStep.Tool = %q, want 'find_implementations'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}

func TestFindImplementations_DefinitiveFooter(t *testing.T) {
	ctx := context.Background()

	// Use the test graph that has implementations
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	iface := &ast.Symbol{
		ID:        "pkg/iface.go:5:Handler",
		Name:      "Handler",
		Kind:      ast.SymbolKindInterface,
		FilePath:  "pkg/iface.go",
		StartLine: 5,
		EndLine:   10,
		Package:   "pkg",
		Language:  "go",
	}
	impl := &ast.Symbol{
		ID:        "pkg/impl.go:10:MyHandler",
		Name:      "MyHandler",
		Kind:      ast.SymbolKindStruct,
		FilePath:  "pkg/impl.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Language:  "go",
	}

	g.AddNode(iface)
	g.AddNode(impl)
	g.AddEdge(impl.ID, iface.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: impl.FilePath, StartLine: impl.StartLine,
	})
	idx.Add(iface)
	idx.Add(impl)
	g.Freeze()

	tool := NewFindImplementationsTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"interface_name": "Handler",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	if !strings.Contains(result.OutputText, "these results are exhaustive") {
		t.Error("expected definitive footer in success path output")
	}
	if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
		t.Error("expected 'Do NOT use Grep or Read' in success path output")
	}
}
