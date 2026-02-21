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
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

func TestGetCallChainTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewGetCallChainTool(g, idx)

	t.Run("traces downstream from main", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "main",
			"direction":     "downstream",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// main -> parseConfig
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		if output.NodeCount < 2 {
			t.Errorf("got %d nodes, want at least 2", output.NodeCount)
		}
	})

	t.Run("traces upstream from parseConfig", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "parseConfig",
			"direction":     "upstream",
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// parseConfig <- main, initServer, LoadConfig
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		if output.NodeCount < 4 {
			t.Errorf("got %d nodes, want at least 4", output.NodeCount)
		}
	})

	t.Run("requires function_name parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail without function_name")
		}
	})

	t.Run("rejects generic word function_name", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "function",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Success {
			t.Error("Execute() should fail for generic word 'function'")
		}
		if !strings.Contains(result.Error, "generic word") {
			t.Errorf("expected 'generic word' in error, got: %s", result.Error)
		}
	})

	t.Run("resolves class symbol via shared resolution", func(t *testing.T) {
		// Create a graph with a Class symbol that has call edges
		classG := graph.NewGraph("/test-class")
		classIdx := index.NewSymbolIndex()

		scene := &ast.Symbol{
			ID:        "scene.ts:10:Scene",
			Name:      "Scene",
			Kind:      ast.SymbolKindClass,
			FilePath:  "scene.ts",
			StartLine: 10,
			EndLine:   100,
			Package:   "core",
			Language:  "typescript",
			Exported:  true,
		}
		addMesh := &ast.Symbol{
			ID:        "scene.ts:50:addMesh",
			Name:      "addMesh",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "scene.ts",
			StartLine: 50,
			EndLine:   60,
			Package:   "core",
			Language:  "typescript",
			Receiver:  "Scene",
		}

		classG.AddNode(scene)
		classG.AddNode(addMesh)
		if err := classIdx.Add(scene); err != nil {
			t.Fatalf("Failed to add scene: %v", err)
		}
		if err := classIdx.Add(addMesh); err != nil {
			t.Fatalf("Failed to add addMesh: %v", err)
		}

		classG.AddEdge(scene.ID, addMesh.ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: scene.FilePath, StartLine: 15,
		})
		classG.Freeze()

		classTool := NewGetCallChainTool(classG, classIdx)
		result, err := classTool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Scene",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed for class symbol: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}
		if output.NodeCount < 2 {
			t.Errorf("expected at least 2 nodes for Scene→addMesh, got %d", output.NodeCount)
		}
	})

	t.Run("resolves dot-notation via shared resolution", func(t *testing.T) {
		// The test graph has initServer (function), we add a method with receiver
		dotG := graph.NewGraph("/test-dot")
		dotIdx := index.NewSymbolIndex()

		server := &ast.Symbol{
			ID:        "server.go:5:Server",
			Name:      "Server",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "server.go",
			StartLine: 5,
			EndLine:   10,
			Package:   "server",
			Language:  "go",
			Exported:  true,
		}
		run := &ast.Symbol{
			ID:        "server.go:15:Run",
			Name:      "Run",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "server.go",
			StartLine: 15,
			EndLine:   25,
			Package:   "server",
			Language:  "go",
			Receiver:  "Server",
			Exported:  true,
		}
		handler := &ast.Symbol{
			ID:        "server.go:30:handleRequest",
			Name:      "handleRequest",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "server.go",
			StartLine: 30,
			EndLine:   40,
			Package:   "server",
			Language:  "go",
		}

		dotG.AddNode(server)
		dotG.AddNode(run)
		dotG.AddNode(handler)
		if err := dotIdx.Add(server); err != nil {
			t.Fatalf("Failed to add server: %v", err)
		}
		if err := dotIdx.Add(run); err != nil {
			t.Fatalf("Failed to add run: %v", err)
		}
		if err := dotIdx.Add(handler); err != nil {
			t.Fatalf("Failed to add handler: %v", err)
		}

		dotG.AddEdge(run.ID, handler.ID, graph.EdgeTypeCalls, ast.Location{
			FilePath: run.FilePath, StartLine: 20,
		})
		dotG.Freeze()

		dotTool := NewGetCallChainTool(dotG, dotIdx)
		result, err := dotTool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "Server.Run",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed for dot-notation: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}
		if output.NodeCount < 2 {
			t.Errorf("expected at least 2 nodes for Server.Run→handleRequest, got %d", output.NodeCount)
		}
	})

	t.Run("max_depth clamped to 10", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "main",
			"max_depth":     50,
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		// Should succeed — depth is clamped to 10, not rejected
	})

	t.Run("shallow chain for leaf-like node", func(t *testing.T) {
		// LoadConfig calls parseConfig (1 callee), so downstream returns root + callee
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "LoadConfig",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}
		// LoadConfig → parseConfig = 2 nodes
		if output.NodeCount < 1 {
			t.Errorf("expected at least 1 node, got %d", output.NodeCount)
		}
	})
}

func TestGetCallChainTool_DepthTracking(t *testing.T) {
	ctx := context.Background()

	// Build a graph with known BFS structure:
	// main → a, main → b, main → c, a → d
	// BFS order: [main, a, b, c, d]
	// Depths: main=0, a=1, b=1, c=1, d=2
	depthG := graph.NewGraph("/test-depth")
	depthIdx := index.NewSymbolIndex()

	syms := make(map[string]*ast.Symbol)
	for i, name := range []string{"main", "a", "b", "c", "d"} {
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("test.go:%d:%s", (i+1)*10, name),
			Name:      name,
			Kind:      ast.SymbolKindFunction,
			FilePath:  "test.go",
			StartLine: (i + 1) * 10,
			EndLine:   (i+1)*10 + 5,
			Package:   "test",
			Language:  "go",
		}
		syms[name] = sym
		depthG.AddNode(sym)
		if err := depthIdx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", name, err)
		}
	}

	depthG.AddEdge(syms["main"].ID, syms["a"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "test.go", StartLine: 11})
	depthG.AddEdge(syms["main"].ID, syms["b"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "test.go", StartLine: 12})
	depthG.AddEdge(syms["main"].ID, syms["c"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "test.go", StartLine: 13})
	depthG.AddEdge(syms["a"].ID, syms["d"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "test.go", StartLine: 14})
	depthG.Freeze()

	tool := NewGetCallChainTool(depthG, depthIdx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
	}

	// Verify depth assignments
	depthMap := make(map[string]int)
	for _, node := range output.Nodes {
		depthMap[node.Name] = node.Depth
	}

	expectedDepths := map[string]int{
		"main": 0,
		"a":    1,
		"b":    1,
		"c":    1,
		"d":    2,
	}

	for name, wantDepth := range expectedDepths {
		if got, ok := depthMap[name]; !ok {
			t.Errorf("node %q not in output", name)
		} else if got != wantDepth {
			t.Errorf("node %q depth = %d, want %d", name, got, wantDepth)
		}
	}

	// Verify formatText uses correct indentation (not linear)
	outputText := result.OutputText
	// "b" should be at depth 1 (2 spaces indent "  → b()")
	if !strings.Contains(outputText, "  → b()") {
		t.Errorf("formatText should indent b at depth 1 (2 spaces), got:\n%s", outputText)
	}
	// "b" should NOT be at depth 2 (4 spaces indent "    → b()")
	if strings.Contains(outputText, "    → b()") {
		t.Error("formatText uses linear indentation (wrong); b is at depth 1 but indented as depth 2")
	}
	// "d" should be at depth 2 (4 spaces indent "    → d()")
	if !strings.Contains(outputText, "    → d()") {
		t.Errorf("formatText should indent d at depth 2 (4 spaces), got:\n%s", outputText)
	}
}

func TestGetCallChainTool_UpstreamDepthTracking(t *testing.T) {
	ctx := context.Background()

	// Build a graph: a → d, main → a, main → b, main → c
	// Upstream from d: d (depth 0) ← a (depth 1) ← main (depth 2)
	upG := graph.NewGraph("/test-upstream-depth")
	upIdx := index.NewSymbolIndex()

	syms := make(map[string]*ast.Symbol)
	for i, name := range []string{"main", "a", "b", "c", "d"} {
		sym := &ast.Symbol{
			ID:        fmt.Sprintf("up.go:%d:%s", (i+1)*10, name),
			Name:      name,
			Kind:      ast.SymbolKindFunction,
			FilePath:  "up.go",
			StartLine: (i + 1) * 10,
			EndLine:   (i+1)*10 + 5,
			Package:   "test",
			Language:  "go",
		}
		syms[name] = sym
		upG.AddNode(sym)
		if err := upIdx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", name, err)
		}
	}

	upG.AddEdge(syms["main"].ID, syms["a"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "up.go", StartLine: 11})
	upG.AddEdge(syms["main"].ID, syms["b"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "up.go", StartLine: 12})
	upG.AddEdge(syms["main"].ID, syms["c"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "up.go", StartLine: 13})
	upG.AddEdge(syms["a"].ID, syms["d"].ID, graph.EdgeTypeCalls, ast.Location{FilePath: "up.go", StartLine: 14})
	upG.Freeze()

	tool := NewGetCallChainTool(upG, upIdx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "d",
		"direction":     "upstream",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
	}

	// Upstream from d: d=0, a=1, main=2
	depthMap := make(map[string]int)
	for _, node := range output.Nodes {
		depthMap[node.Name] = node.Depth
	}

	expectedDepths := map[string]int{
		"d":    0,
		"a":    1,
		"main": 2,
	}

	for name, wantDepth := range expectedDepths {
		if got, ok := depthMap[name]; !ok {
			t.Errorf("node %q not in output", name)
		} else if got != wantDepth {
			t.Errorf("node %q depth = %d, want %d", name, got, wantDepth)
		}
	}

	// b and c should NOT appear (they don't call d)
	if _, ok := depthMap["b"]; ok {
		t.Error("node 'b' should not appear in upstream chain from d")
	}
	if _, ok := depthMap["c"]; ok {
		t.Error("node 'c' should not appear in upstream chain from d")
	}
}

// TestGetCallChainTool_TraceStepPopulated verifies CRS integration on success path.
func TestGetCallChainTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
		"direction":     "downstream",
		"max_depth":     3,
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
	if result.TraceStep.Action != "tool_get_call_chain" {
		t.Errorf("TraceStep.Action = %q, want 'tool_get_call_chain'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "get_call_chain" {
		t.Errorf("TraceStep.Tool = %q, want 'get_call_chain'", result.TraceStep.Tool)
	}
	if result.TraceStep.Target != "main" {
		t.Errorf("TraceStep.Target = %q, want 'main'", result.TraceStep.Target)
	}
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}

	if result.TraceStep.Metadata == nil {
		t.Fatal("TraceStep.Metadata should not be nil")
	}
	for _, key := range []string{"chain_length", "depth", "direction", "truncated"} {
		if _, ok := result.TraceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata should contain %q", key)
		}
	}
	if result.TraceStep.Error != "" {
		t.Errorf("TraceStep.Error should be empty on success, got %q", result.TraceStep.Error)
	}
}

// TestGetCallChainTool_TraceStepOnError verifies CRS integration on validation error path.
func TestGetCallChainTool_TraceStepOnError(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "",
	}})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Success {
		t.Fatal("Execute() should have failed with empty function_name")
	}

	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated even on validation error")
	}
	if result.TraceStep.Action != "tool_get_call_chain" {
		t.Errorf("TraceStep.Action = %q, want 'tool_get_call_chain'", result.TraceStep.Action)
	}
	if result.TraceStep.Tool != "get_call_chain" {
		t.Errorf("TraceStep.Tool = %q, want 'get_call_chain'", result.TraceStep.Tool)
	}
	if result.TraceStep.Error == "" {
		t.Error("TraceStep.Error should be set on validation failure")
	}
}

func TestGetCallChainTool_InheritanceAwareUpstream(t *testing.T) {
	ctx := context.Background()

	inhG := graph.NewGraph("/test-inheritance")
	inhIdx := index.NewSymbolIndex()

	baseClass := &ast.Symbol{
		ID: "base.ts:10:BaseClass", Name: "BaseClass", Kind: ast.SymbolKindClass,
		FilePath: "base.ts", StartLine: 10, EndLine: 50, Package: "core",
		Language: "typescript", Exported: true,
	}
	baseProcess := &ast.Symbol{
		ID: "base.ts:20:process", Name: "process", Kind: ast.SymbolKindMethod,
		FilePath: "base.ts", StartLine: 20, EndLine: 30, Package: "core",
		Language: "typescript", Receiver: "BaseClass",
	}
	derivedClass := &ast.Symbol{
		ID: "derived.ts:10:DerivedClass", Name: "DerivedClass", Kind: ast.SymbolKindClass,
		FilePath: "derived.ts", StartLine: 10, EndLine: 50, Package: "core",
		Language: "typescript", Exported: true,
		Metadata: &ast.SymbolMetadata{Extends: "BaseClass"},
	}
	derivedProcess := &ast.Symbol{
		ID: "derived.ts:20:process", Name: "process", Kind: ast.SymbolKindMethod,
		FilePath: "derived.ts", StartLine: 20, EndLine: 30, Package: "core",
		Language: "typescript", Receiver: "DerivedClass",
	}
	externalCaller := &ast.Symbol{
		ID: "caller.ts:5:handleData", Name: "handleData", Kind: ast.SymbolKindFunction,
		FilePath: "caller.ts", StartLine: 5, EndLine: 15, Package: "app",
		Language: "typescript", Exported: true,
	}

	for _, sym := range []*ast.Symbol{baseClass, baseProcess, derivedClass, derivedProcess, externalCaller} {
		inhG.AddNode(sym)
		if err := inhIdx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}

	inhG.AddEdge(externalCaller.ID, baseProcess.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: "caller.ts", StartLine: 10,
	})
	// DerivedClass inherits from BaseClass (Metadata.Extends handles lookup;
	// use EdgeTypeImplements as the closest available edge type)
	inhG.AddEdge(derivedClass.ID, baseClass.ID, graph.EdgeTypeImplements, ast.Location{
		FilePath: "derived.ts", StartLine: 10,
	})
	inhG.Freeze()

	tool := NewGetCallChainTool(inhG, inhIdx)

	t.Run("upstream from derived process finds callers of parent method", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "DerivedClass.process",
			"direction":     "upstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		t.Logf("Nodes found: %d", output.NodeCount)
		for _, node := range output.Nodes {
			t.Logf("  Node: %s (depth=%d)", node.Name, node.Depth)
		}
	})

	t.Run("upstream from base process finds direct callers", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "BaseClass.process",
			"direction":     "upstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
		}

		if output.NodeCount < 2 {
			t.Errorf("expected at least 2 nodes, got %d", output.NodeCount)
		}

		found := false
		for _, node := range output.Nodes {
			if node.Name == "handleData" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected externalCaller 'handleData' in upstream chain")
		}
	})
}

// =============================================================================
// IT-05 CR-3: Graph Fallback When Index Resolution Fails
// =============================================================================

// TestGetCallChainTool_GraphFallback verifies that when the symbol index doesn't
// contain a symbol but the graph does, the graph fallback is used.
func TestGetCallChainTool_GraphFallback(t *testing.T) {
	ctx := context.Background()

	fallbackG := graph.NewGraph("/test-fallback")
	fallbackIdx := index.NewSymbolIndex()

	orphan := &ast.Symbol{
		ID: "orphan.go:10:orphanFunc", Name: "orphanFunc", Kind: ast.SymbolKindFunction,
		FilePath: "orphan.go", StartLine: 10, EndLine: 20, Package: "main",
		Language: "go", Exported: true,
	}
	child := &ast.Symbol{
		ID: "child.go:5:childFunc", Name: "childFunc", Kind: ast.SymbolKindFunction,
		FilePath: "child.go", StartLine: 5, EndLine: 15, Package: "main",
		Language: "go", Exported: true,
	}

	fallbackG.AddNode(orphan)
	fallbackG.AddNode(child)

	if err := fallbackIdx.Add(child); err != nil {
		t.Fatalf("Failed to add child: %v", err)
	}

	fallbackG.AddEdge(orphan.ID, child.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: orphan.FilePath, StartLine: 15,
	})
	fallbackG.Freeze()

	tool := NewGetCallChainTool(fallbackG, fallbackIdx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "orphanFunc",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s (graph fallback should have found it)", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
	}

	if output.NodeCount < 2 {
		t.Errorf("expected at least 2 nodes via graph fallback, got %d", output.NodeCount)
	}
}

// TestGetCallChainTool_DefaultDirection verifies that omitting direction defaults
// to downstream traversal.
func TestGetCallChainTool_DefaultDirection(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphWithCallers(t)

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "main",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
	}

	if output.NodeCount < 2 {
		t.Errorf("expected at least 2 nodes with default downstream direction, got %d", output.NodeCount)
	}
}

// TestGetCallChainTool_EmptyGraphResult verifies behavior when a symbol has no
// connections in the requested direction.
func TestGetCallChainTool_EmptyGraphResult(t *testing.T) {
	ctx := context.Background()

	isoG := graph.NewGraph("/test-isolated")
	isoIdx := index.NewSymbolIndex()

	isolated := &ast.Symbol{
		ID: "iso.go:5:isolatedFunc", Name: "isolatedFunc", Kind: ast.SymbolKindFunction,
		FilePath: "iso.go", StartLine: 5, EndLine: 15, Package: "main",
		Language: "go", Exported: true,
	}
	isoG.AddNode(isolated)
	if err := isoIdx.Add(isolated); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	isoG.Freeze()

	tool := NewGetCallChainTool(isoG, isoIdx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"function_name": "isolatedFunc",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed for isolated node: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("Output is not GetCallChainOutput, got %T", result.Output)
	}

	if output.EdgeCount > 0 {
		t.Errorf("expected 0 edges for isolated node, got %d", output.EdgeCount)
	}
}

// TestGetCallChainTool_DefinitiveFooter verifies that get_call_chain output
// includes graph markers for pass-through detection by getSingleFormattedResult.
// IT-06c M-5: Without these markers, forceLLMSynthesis sends the full call chain
// to gpt-oss:20b which chokes on large outputs (33KB+) and returns empty.
func TestGetCallChainTool_DefinitiveFooter(t *testing.T) {
	ctx := context.Background()

	t.Run("positive result has Found prefix and exhaustive footer", func(t *testing.T) {
		g := graph.NewGraph("/test-footer")
		idx := index.NewSymbolIndex()

		main := &ast.Symbol{
			ID: "main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "main.go", StartLine: 5, EndLine: 20, Package: "main",
			Language: "go", Exported: true,
		}
		helper := &ast.Symbol{
			ID: "util.go:10:helper", Name: "helper", Kind: ast.SymbolKindFunction,
			FilePath: "util.go", StartLine: 10, EndLine: 15, Package: "main",
			Language: "go", Exported: true,
		}
		g.AddNode(main)
		g.AddNode(helper)
		g.AddEdge(main.ID, helper.ID, graph.EdgeTypeCalls, ast.Location{})
		for _, sym := range []*ast.Symbol{main, helper} {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("Failed to add: %v", err)
			}
		}
		g.Freeze()

		tool := NewGetCallChainTool(g, idx)
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "main",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.HasPrefix(strings.TrimSpace(result.OutputText), "Found ") {
			t.Errorf("expected OutputText to start with 'Found ', got: %q", result.OutputText[:80])
		}
		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected 'these results are exhaustive' in positive output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read' in positive output")
		}
	})

	t.Run("isolated node still has exhaustive footer", func(t *testing.T) {
		// An isolated node (1 visited node, 0 edges) goes through the positive
		// path with "Found 1 nodes..." — it still needs the exhaustive footer.
		g := graph.NewGraph("/test-footer-zero")
		idx := index.NewSymbolIndex()

		isolated := &ast.Symbol{
			ID: "iso.go:5:isolatedFunc", Name: "isolatedFunc", Kind: ast.SymbolKindFunction,
			FilePath: "iso.go", StartLine: 5, EndLine: 15, Package: "main",
			Language: "go", Exported: true,
		}
		g.AddNode(isolated)
		if err := idx.Add(isolated); err != nil {
			t.Fatalf("Failed to add: %v", err)
		}
		g.Freeze()

		tool := NewGetCallChainTool(g, idx)
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "isolatedFunc",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if !strings.Contains(result.OutputText, "these results are exhaustive") {
			t.Error("expected 'these results are exhaustive' in isolated node output")
		}
		if !strings.Contains(result.OutputText, "Do NOT use Grep or Read to verify") {
			t.Error("expected 'Do NOT use Grep or Read' in isolated node output")
		}
	})
}

func TestMergeTraversals(t *testing.T) {
	t.Run("nil secondary leaves primary unchanged", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a", "b"},
			Edges:        []*graph.Edge{{FromID: "a", ToID: "b"}},
			Depth:        1,
		}
		mergeTraversals(primary, nil)
		if len(primary.VisitedNodes) != 2 {
			t.Errorf("expected 2 nodes, got %d", len(primary.VisitedNodes))
		}
	})

	t.Run("empty secondary leaves primary unchanged", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a", "b"},
			Edges:        []*graph.Edge{{FromID: "a", ToID: "b"}},
			Depth:        1,
		}
		secondary := &graph.TraversalResult{
			VisitedNodes: []string{},
		}
		mergeTraversals(primary, secondary)
		if len(primary.VisitedNodes) != 2 {
			t.Errorf("expected 2 nodes, got %d", len(primary.VisitedNodes))
		}
	})

	t.Run("deduplicates nodes", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a", "b"},
			Edges:        []*graph.Edge{{FromID: "a", ToID: "b"}},
			Depth:        1,
		}
		secondary := &graph.TraversalResult{
			VisitedNodes: []string{"b", "c"},
			Edges:        []*graph.Edge{{FromID: "b", ToID: "c"}},
			Depth:        2,
		}
		mergeTraversals(primary, secondary)
		if len(primary.VisitedNodes) != 3 {
			t.Errorf("expected 3 unique nodes, got %d: %v", len(primary.VisitedNodes), primary.VisitedNodes)
		}
		if len(primary.Edges) != 2 {
			t.Errorf("expected 2 unique edges, got %d", len(primary.Edges))
		}
	})

	t.Run("deduplicates edges", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a", "b"},
			Edges:        []*graph.Edge{{FromID: "a", ToID: "b"}},
			Depth:        1,
		}
		secondary := &graph.TraversalResult{
			VisitedNodes: []string{"a", "b"},
			Edges:        []*graph.Edge{{FromID: "a", ToID: "b"}},
			Depth:        1,
		}
		mergeTraversals(primary, secondary)
		if len(primary.Edges) != 1 {
			t.Errorf("expected 1 deduplicated edge, got %d", len(primary.Edges))
		}
	})

	t.Run("takes deeper depth", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a"},
			Depth:        1,
		}
		secondary := &graph.TraversalResult{
			VisitedNodes: []string{"b"},
			Depth:        5,
		}
		mergeTraversals(primary, secondary)
		if primary.Depth != 5 {
			t.Errorf("expected depth 5, got %d", primary.Depth)
		}
	})

	t.Run("propagates truncation flag", func(t *testing.T) {
		primary := &graph.TraversalResult{
			VisitedNodes: []string{"a"},
			Truncated:    false,
		}
		secondary := &graph.TraversalResult{
			VisitedNodes: []string{"b"},
			Truncated:    true,
		}
		mergeTraversals(primary, secondary)
		if !primary.Truncated {
			t.Error("expected truncated flag to be propagated")
		}
	})
}

// TestDisambiguateGraphNodes tests IT-00a-1 Phase 2: graph fallback disambiguation.
func TestDisambiguateGraphNodes(t *testing.T) {
	t.Run("prefers source over test file", func(t *testing.T) {
		source := &ast.Symbol{
			ID: "cmd/main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}
		test := &ast.Symbol{
			ID: "cmd/main_test.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main_test.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}

		result := disambiguateGraphNodes([]*ast.Symbol{test, source})
		if result.ID != source.ID {
			t.Errorf("expected source file preferred, got %s", result.ID)
		}
	})

	t.Run("prefers exported over unexported", func(t *testing.T) {
		exported := &ast.Symbol{
			ID: "pkg/a.go:5:Handler", Name: "Handler", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}
		unexported := &ast.Symbol{
			ID: "pkg/b.go:5:Handler", Name: "Handler", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/b.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}

		result := disambiguateGraphNodes([]*ast.Symbol{unexported, exported})
		if result.ID != exported.ID {
			t.Errorf("expected exported preferred, got %s", result.ID)
		}
	})

	t.Run("prefers shallow over deep path", func(t *testing.T) {
		shallow := &ast.Symbol{
			ID: "cmd/main.go:5:Run", Name: "Run", Kind: ast.SymbolKindFunction,
			FilePath: "cmd/main.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}
		deep := &ast.Symbol{
			ID: "internal/warpc/gen/run.go:5:Run", Name: "Run", Kind: ast.SymbolKindFunction,
			FilePath: "internal/warpc/gen/run.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}

		result := disambiguateGraphNodes([]*ast.Symbol{deep, shallow})
		if result.ID != shallow.ID {
			t.Errorf("expected shallow path preferred, got %s", result.ID)
		}
	})

	t.Run("prefers function over struct", func(t *testing.T) {
		fn := &ast.Symbol{
			ID: "pkg/a.go:10:Process", Name: "Process", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
		}
		st := &ast.Symbol{
			ID: "pkg/b.go:10:Process", Name: "Process", Kind: ast.SymbolKindStruct,
			FilePath: "pkg/b.go", StartLine: 10, EndLine: 20, Language: "go", Exported: true,
		}

		result := disambiguateGraphNodes([]*ast.Symbol{st, fn})
		if result.ID != fn.ID {
			t.Errorf("expected function preferred over struct, got %s", result.ID)
		}
	})

	t.Run("single node returns it", func(t *testing.T) {
		sym := &ast.Symbol{
			ID: "pkg/a.go:10:X", Name: "X", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/a.go", StartLine: 10, EndLine: 20, Language: "go",
		}
		result := disambiguateGraphNodes([]*ast.Symbol{sym})
		if result.ID != sym.ID {
			t.Errorf("expected single node returned, got %s", result.ID)
		}
	})

	t.Run("nil slice returns nil", func(t *testing.T) {
		result := disambiguateGraphNodes(nil)
		if result != nil {
			t.Errorf("expected nil for nil slice, got %v", result)
		}
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		result := disambiguateGraphNodes([]*ast.Symbol{})
		if result != nil {
			t.Errorf("expected nil for empty slice, got %v", result)
		}
	})

	t.Run("cumulative signals — test+unexported+deep loses to source+exported+shallow", func(t *testing.T) {
		good := &ast.Symbol{
			ID: "pkg/handler.go:5:Process", Name: "Process", Kind: ast.SymbolKindFunction,
			FilePath: "pkg/handler.go", StartLine: 5, EndLine: 10, Language: "go", Exported: true,
		}
		bad := &ast.Symbol{
			ID: "internal/deep/test/helper_test.go:5:Process", Name: "Process", Kind: ast.SymbolKindFunction,
			FilePath: "internal/deep/test/helper_test.go", StartLine: 5, EndLine: 10, Language: "go", Exported: false,
		}

		result := disambiguateGraphNodes([]*ast.Symbol{bad, good})
		if result.ID != good.ID {
			t.Errorf("expected good symbol preferred, got %s", result.ID)
		}
	})
}

// TestIsGraphNodeTestFile tests the test file detection for graph node disambiguation.
func TestIsGraphNodeTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"cmd/main_test.go", true},
		{"cmd/main.go", false},
		{"test/handler.go", true},
		{"tests/helper.py", true},
		{"__tests__/component.test.js", true},
		{"src/handler.spec.ts", true},
		{"pkg/handler.go", false},
		{"test_utils.py", true},
		{"conftest.py", true},
		{"src/render.test.tsx", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isGraphNodeTestFile(tt.path)
			if got != tt.want {
				t.Errorf("isGraphNodeTestFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestGetCallChainTool_DestinationParam(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-dest")
	idx := index.NewSymbolIndex()

	main := &ast.Symbol{
		ID: "main.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
		FilePath: "main.go", StartLine: 5, EndLine: 15, Package: "main",
		Language: "go", Exported: false,
	}
	helper := &ast.Symbol{
		ID: "helper.go:5:Helper", Name: "Helper", Kind: ast.SymbolKindFunction,
		FilePath: "helper.go", StartLine: 5, EndLine: 15, Package: "main",
		Language: "go", Exported: true,
	}
	g.AddNode(main)
	g.AddNode(helper)
	g.AddEdge(main.ID, helper.ID, graph.EdgeTypeCalls, ast.Location{})
	if err := idx.Add(main); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	if err := idx.Add(helper); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)

	t.Run("destination_name is accepted as parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name":    "main",
			"direction":        "downstream",
			"destination_name": "helper.go:5:Helper",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output type mismatch: %T", result.Output)
		}
		// Main calls Helper, so depth should be > 0
		if output.Depth == 0 {
			t.Errorf("expected depth > 0 for main→Helper chain")
		}
	})
}

// TestGetCallChainTool_DepthZeroEnrichment tests IT-05 R5: when a symbol has
// no call edges, the output includes an explanatory message.
func TestGetCallChainTool_DepthZeroEnrichment(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-d0")
	idx := index.NewSymbolIndex()

	// Isolated function with no outgoing call edges
	isolated := &ast.Symbol{
		ID: "app.go:5:listen", Name: "listen", Kind: ast.SymbolKindFunction,
		FilePath: "app.go", StartLine: 5, EndLine: 15, Package: "app",
		Language: "go", Exported: true,
	}
	g.AddNode(isolated)
	if err := idx.Add(isolated); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)

	t.Run("depth-0 result includes explanatory message", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name": "listen",
			"direction":     "downstream",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output type mismatch: %T", result.Output)
		}
		if output.Message == "" {
			t.Error("expected non-empty message for depth-0 result")
		}
		if !strings.Contains(output.Message, "no further call edges") {
			t.Errorf("expected message about no call edges, got %q", output.Message)
		}
	})

	t.Run("depth-0 with destination includes destination info", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name":    "listen",
			"direction":        "downstream",
			"destination_name": "handler.go:10:handleRequest",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output type mismatch: %T", result.Output)
		}
		if !strings.Contains(output.Message, "Destination") {
			t.Errorf("expected message to mention destination, got %q", output.Message)
		}
		if output.DestinationFound {
			t.Error("DestinationFound should be false when destination is not in traversal")
		}
	})
}

// TestGetCallChainTool_DestinationFoundWithPath tests IT-05 R5 review fix:
// when DestinationName is set and the destination IS found in the traversal,
// the output includes DestinationFound=true and PathToDestination.
func TestGetCallChainTool_DestinationFoundWithPath(t *testing.T) {
	ctx := context.Background()

	g := graph.NewGraph("/test-dest-path")
	idx := index.NewSymbolIndex()

	// Build a chain: main → processRequest → handleDB → writeLog
	symbols := []*ast.Symbol{
		{ID: "app.go:5:main", Name: "main", Kind: ast.SymbolKindFunction,
			FilePath: "app.go", StartLine: 5, EndLine: 15, Package: "app",
			Language: "go", Exported: true},
		{ID: "handler.go:10:processRequest", Name: "processRequest", Kind: ast.SymbolKindFunction,
			FilePath: "handler.go", StartLine: 10, EndLine: 30, Package: "handler",
			Language: "go", Exported: true},
		{ID: "db.go:5:handleDB", Name: "handleDB", Kind: ast.SymbolKindFunction,
			FilePath: "db.go", StartLine: 5, EndLine: 20, Package: "db",
			Language: "go", Exported: true},
		{ID: "log.go:5:writeLog", Name: "writeLog", Kind: ast.SymbolKindFunction,
			FilePath: "log.go", StartLine: 5, EndLine: 15, Package: "log",
			Language: "go", Exported: true},
	}
	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add: %v", err)
		}
	}
	// main → processRequest → handleDB → writeLog
	g.AddEdge("app.go:5:main", "handler.go:10:processRequest", graph.EdgeTypeCalls, ast.Location{})
	g.AddEdge("handler.go:10:processRequest", "db.go:5:handleDB", graph.EdgeTypeCalls, ast.Location{})
	g.AddEdge("db.go:5:handleDB", "log.go:5:writeLog", graph.EdgeTypeCalls, ast.Location{})
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)

	t.Run("destination found in traversal", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name":    "main",
			"direction":        "downstream",
			"destination_name": "db.go:5:handleDB",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output type mismatch: %T", result.Output)
		}
		if !output.DestinationFound {
			t.Error("expected DestinationFound=true")
		}
		if len(output.PathToDestination) == 0 {
			t.Fatal("expected non-empty PathToDestination")
		}
		// Path should be: main → processRequest → handleDB (3 nodes)
		if len(output.PathToDestination) != 3 {
			t.Errorf("expected 3 nodes in path, got %d", len(output.PathToDestination))
		}
		if output.PathToDestination[0].ID != "app.go:5:main" {
			t.Errorf("path[0] should be main, got %q", output.PathToDestination[0].ID)
		}
		if output.PathToDestination[len(output.PathToDestination)-1].ID != "db.go:5:handleDB" {
			t.Errorf("path[last] should be handleDB, got %q", output.PathToDestination[len(output.PathToDestination)-1].ID)
		}
		if !strings.Contains(output.Message, "Found static path") {
			t.Errorf("expected path-found message, got %q", output.Message)
		}
	})

	t.Run("destination not in traversal", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"function_name":    "main",
			"direction":        "downstream",
			"destination_name": "nonexistent.go:5:missing",
		}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		output, ok := result.Output.(GetCallChainOutput)
		if !ok {
			t.Fatalf("Output type mismatch: %T", result.Output)
		}
		if output.DestinationFound {
			t.Error("expected DestinationFound=false for missing destination")
		}
		if len(output.PathToDestination) > 0 {
			t.Error("expected empty PathToDestination for missing destination")
		}
	})
}

// TestExtractPathToDestination tests the path extraction helper directly.
func TestExtractPathToDestination(t *testing.T) {
	// Simulate a BFS tree: A → B → C → D
	parents := map[string]string{
		"B": "A",
		"C": "B",
		"D": "C",
	}
	depths := map[string]int{
		"A": 0,
		"B": 1,
		"C": 2,
		"D": 3,
	}

	path := extractPathToDestination("D", parents, depths, nil)
	if len(path) != 4 {
		t.Fatalf("expected 4 nodes in path A→B→C→D, got %d", len(path))
	}
	expectedIDs := []string{"A", "B", "C", "D"}
	for i, expected := range expectedIDs {
		if path[i].ID != expected {
			t.Errorf("path[%d].ID = %q, want %q", i, path[i].ID, expected)
		}
		if path[i].Depth != i {
			t.Errorf("path[%d].Depth = %d, want %d", i, path[i].Depth, i)
		}
	}

	// CalledBy relationships
	if path[0].CalledBy != "" {
		t.Errorf("root node should have empty CalledBy, got %q", path[0].CalledBy)
	}
	if path[1].CalledBy != "A" {
		t.Errorf("path[1].CalledBy = %q, want 'A'", path[1].CalledBy)
	}
	if path[3].CalledBy != "C" {
		t.Errorf("path[3].CalledBy = %q, want 'C'", path[3].CalledBy)
	}
}

func TestGetCallChainTool_ExternalBoundaryAnnotation(t *testing.T) {
	g, idx := createTestGraphWithExternalCallees(t)

	tool := NewGetCallChainTool(g, idx)

	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "Open",
		"direction":     "downstream",
		"max_depth":     3,
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("output is not GetCallChainOutput, got %T", result.Output)
	}

	// Should find MkdirAll as external
	var foundExternal bool
	for _, node := range output.Nodes {
		if node.IsExternal {
			foundExternal = true
			if node.Name != "MkdirAll" {
				t.Errorf("expected external node name 'MkdirAll', got %q", node.Name)
			}
			if node.ExternalPkg != "os" {
				t.Errorf("expected external package 'os', got %q", node.ExternalPkg)
			}
		}
	}

	if !foundExternal {
		t.Error("expected at least one external node in output, found none")
	}

	// ExternalDependencies summary should be populated
	if len(output.ExternalDependencies) == 0 {
		t.Error("expected ExternalDependencies to be populated")
	}

	foundOsMkdirAll := false
	for _, dep := range output.ExternalDependencies {
		if dep == "os.MkdirAll" {
			foundOsMkdirAll = true
		}
	}
	if !foundOsMkdirAll {
		t.Errorf("expected 'os.MkdirAll' in ExternalDependencies, got %v", output.ExternalDependencies)
	}

	// OutputText should contain external annotation
	if !strings.Contains(result.OutputText, "(external: os)") {
		t.Errorf("OutputText should contain '(external: os)', got:\n%s", result.OutputText)
	}

	// OutputText should contain external dependencies summary
	if !strings.Contains(result.OutputText, "External Dependencies") {
		t.Errorf("OutputText should contain 'External Dependencies' summary, got:\n%s", result.OutputText)
	}
}

// TestGetCallChainTool_ExternalBoundaryUpstream tests IT-05a with upstream direction.
// External nodes should still be annotated when traversing callers.
func TestGetCallChainTool_ExternalBoundaryUpstream(t *testing.T) {
	g, idx := createTestGraphWithExternalUpstream(t)

	tool := NewGetCallChainTool(g, idx)

	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "ProcessData",
		"direction":     "upstream",
		"max_depth":     3,
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("output is not GetCallChainOutput, got %T", result.Output)
	}

	// External caller should be annotated
	var foundExternal bool
	for _, node := range output.Nodes {
		if node.IsExternal {
			foundExternal = true
			if node.ExternalPkg != "framework" {
				t.Errorf("expected external package 'framework', got %q", node.ExternalPkg)
			}
		}
	}

	if !foundExternal {
		t.Error("expected external node in upstream traversal")
	}

	// OutputText should contain the external annotation
	if !strings.Contains(result.OutputText, "(external:") {
		t.Errorf("OutputText should contain external annotation:\n%s", result.OutputText)
	}
}

// TestGetCallChainTool_NoExternals verifies that when no external nodes exist,
// ExternalDependencies is empty and no summary section appears.
func TestGetCallChainTool_NoExternals(t *testing.T) {
	g := graph.NewGraph("/test-no-ext")
	idx := index.NewSymbolIndex()

	funcA := &ast.Symbol{
		ID: "a.go:1:FuncA", Name: "FuncA",
		Kind: ast.SymbolKindFunction, FilePath: "a.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "main", Language: "go",
	}
	funcB := &ast.Symbol{
		ID: "b.go:1:FuncB", Name: "FuncB",
		Kind: ast.SymbolKindFunction, FilePath: "b.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "main", Language: "go",
	}
	for _, sym := range []*ast.Symbol{funcA, funcB} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}
	g.AddEdge(funcA.ID, funcB.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 5})
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "FuncA",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := result.Output.(GetCallChainOutput)
	if len(output.ExternalDependencies) != 0 {
		t.Errorf("expected no ExternalDependencies, got %v", output.ExternalDependencies)
	}

	// No "External Dependencies" section in text
	if strings.Contains(result.OutputText, "External Dependencies") {
		t.Errorf("OutputText should NOT contain External Dependencies when none exist:\n%s", result.OutputText)
	}

	// No "(external:" annotation
	if strings.Contains(result.OutputText, "(external:") {
		t.Errorf("OutputText should NOT contain external annotation:\n%s", result.OutputText)
	}
}

// TestGetCallChainTool_MultipleExternals tests multiple external boundaries at
// different depths with deduplication in the summary.
func TestGetCallChainTool_MultipleExternals(t *testing.T) {
	g, idx := createTestGraphWithMultipleExternals(t)

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "Main",
		"direction":     "downstream",
		"max_depth":     5,
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := result.Output.(GetCallChainOutput)

	// Count external nodes in output
	externalCount := 0
	for _, node := range output.Nodes {
		if node.IsExternal {
			externalCount++
		}
	}
	if externalCount != 3 {
		t.Errorf("expected 3 external nodes, got %d", externalCount)
	}

	// ExternalDependencies should have 3 unique entries
	if len(output.ExternalDependencies) != 3 {
		t.Errorf("expected 3 ExternalDependencies, got %d: %v",
			len(output.ExternalDependencies), output.ExternalDependencies)
	}

	// Check specific dependencies are present
	depSet := make(map[string]bool)
	for _, dep := range output.ExternalDependencies {
		depSet[dep] = true
	}
	for _, expected := range []string{"fmt.Println", "os.MkdirAll", "database/sql.Query"} {
		if !depSet[expected] {
			t.Errorf("expected %q in ExternalDependencies, got %v", expected, output.ExternalDependencies)
		}
	}

	// Text should have external annotations for each
	for _, pkg := range []string{"fmt", "os", "database/sql"} {
		if !strings.Contains(result.OutputText, fmt.Sprintf("(external: %s)", pkg)) {
			t.Errorf("OutputText missing '(external: %s)' annotation:\n%s", pkg, result.OutputText)
		}
	}

	// Text should have depth info in summary
	if !strings.Contains(result.OutputText, "depth") {
		t.Errorf("OutputText summary should show depth info:\n%s", result.OutputText)
	}
}

// TestGetCallChainTool_ExternalWithEmptyPackage tests that externals with empty
// package still get the "(external)" annotation without a package name.
func TestGetCallChainTool_ExternalWithEmptyPackage(t *testing.T) {
	g := graph.NewGraph("/test-empty-pkg")
	idx := index.NewSymbolIndex()

	caller := &ast.Symbol{
		ID: "main.go:1:InvokeExternal", Name: "InvokeExternal",
		Kind: ast.SymbolKindFunction, FilePath: "main.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "main", Language: "go",
	}
	// External with no package info at all
	unknownExt := &ast.Symbol{
		ID: "external::UnknownFunc", Name: "UnknownFunc",
		Kind: ast.SymbolKindExternal, Package: "",
	}

	g.AddNode(caller)
	g.AddNode(unknownExt)
	if err := idx.Add(caller); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	g.AddEdge(caller.ID, unknownExt.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 5})
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "InvokeExternal",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute failed: %s", result.Error)
	}

	output, ok := result.Output.(GetCallChainOutput)
	if !ok {
		t.Fatalf("output is not GetCallChainOutput, got %T (text: %s)", result.Output, result.OutputText)
	}

	// Should have the external node
	var found bool
	for _, node := range output.Nodes {
		if node.IsExternal {
			found = true
			if node.ExternalPkg != "" {
				t.Errorf("expected empty ExternalPkg, got %q", node.ExternalPkg)
			}
			if node.Name != "UnknownFunc" {
				t.Errorf("expected Name 'UnknownFunc', got %q", node.Name)
			}
		}
	}
	if !found {
		t.Error("expected external node in output")
	}

	// OutputText should have "(external)" without package
	if !strings.Contains(result.OutputText, "(external)") {
		t.Errorf("OutputText should contain '(external)' for unknown-package external:\n%s", result.OutputText)
	}
}

// TestGetCallChainTool_ExternalNodeNotInIndex verifies that external nodes
// (which are not in the symbol index) still get proper names from graph data.
func TestGetCallChainTool_ExternalNodeNotInIndex(t *testing.T) {
	g := graph.NewGraph("/test-not-in-index")
	idx := index.NewSymbolIndex()

	caller := &ast.Symbol{
		ID: "main.go:1:Start", Name: "Start",
		Kind: ast.SymbolKindFunction, FilePath: "main.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "main", Language: "go",
	}
	extRedis := &ast.Symbol{
		ID: "external:redis:Connect", Name: "Connect",
		Kind: ast.SymbolKindExternal, Package: "redis",
	}

	g.AddNode(caller)
	g.AddNode(extRedis)
	// Only caller in index, external NOT in index
	if err := idx.Add(caller); err != nil {
		t.Fatalf("Failed to add: %v", err)
	}
	g.AddEdge(caller.ID, extRedis.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 5})
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "Start",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := result.Output.(GetCallChainOutput)
	for _, node := range output.Nodes {
		if node.IsExternal {
			// Name should come from graph, not index
			if node.Name != "Connect" {
				t.Errorf("external node Name = %q, want %q (from graph)", node.Name, "Connect")
			}
			// File and Line should be zero (external has no source)
			if node.File != "" {
				t.Errorf("external node File = %q, want empty", node.File)
			}
			if node.Line != 0 {
				t.Errorf("external node Line = %d, want 0", node.Line)
			}
		}
	}
}

// TestGetCallChainTool_ExternalDependenciesDedup verifies that duplicate
// external symbols (same package.name) appear only once in the summary.
func TestGetCallChainTool_ExternalDependenciesDedup(t *testing.T) {
	g := graph.NewGraph("/test-dedup")
	idx := index.NewSymbolIndex()

	funcA := &ast.Symbol{
		ID: "a.go:1:FuncA", Name: "FuncA",
		Kind: ast.SymbolKindFunction, FilePath: "a.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "pkg", Language: "go",
	}
	funcB := &ast.Symbol{
		ID: "b.go:1:FuncB", Name: "FuncB",
		Kind: ast.SymbolKindFunction, FilePath: "b.go",
		Exported: true, StartLine: 1, EndLine: 10, Package: "pkg", Language: "go",
	}
	// Two different external nodes that resolve to the same package.name
	ext1 := &ast.Symbol{
		ID: "external:fmt:Println_1", Name: "Println",
		Kind: ast.SymbolKindExternal, Package: "fmt",
	}
	ext2 := &ast.Symbol{
		ID: "external:fmt:Println_2", Name: "Println",
		Kind: ast.SymbolKindExternal, Package: "fmt",
	}

	for _, sym := range []*ast.Symbol{funcA, funcB} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add: %v", err)
		}
	}
	g.AddNode(ext1)
	g.AddNode(ext2)
	g.AddEdge(funcA.ID, funcB.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 2})
	g.AddEdge(funcA.ID, ext1.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 3})
	g.AddEdge(funcB.ID, ext2.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "b.go", StartLine: 3})
	g.Freeze()

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "FuncA",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	output := result.Output.(GetCallChainOutput)

	// Two IsExternal nodes in the output
	extCount := 0
	for _, node := range output.Nodes {
		if node.IsExternal {
			extCount++
		}
	}
	if extCount != 2 {
		t.Errorf("expected 2 external nodes, got %d", extCount)
	}

	// But ExternalDependencies should deduplicate to 1 entry
	if len(output.ExternalDependencies) != 1 {
		t.Errorf("expected 1 deduplicated ExternalDependency, got %d: %v",
			len(output.ExternalDependencies), output.ExternalDependencies)
	}
	if len(output.ExternalDependencies) > 0 && output.ExternalDependencies[0] != "fmt.Println" {
		t.Errorf("expected 'fmt.Println', got %q", output.ExternalDependencies[0])
	}
}

// TestGetCallChainTool_ExternalSearchLibraryDocsHint verifies the output text
// includes the hint about using search_library_docs.
func TestGetCallChainTool_ExternalSearchLibraryDocsHint(t *testing.T) {
	g, idx := createTestGraphWithExternalCallees(t)

	tool := NewGetCallChainTool(g, idx)
	result, err := tool.Execute(context.Background(), MapParams{Params: map[string]any{
		"function_name": "Open",
		"direction":     "downstream",
	}})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if !strings.Contains(result.OutputText, "search_library_docs") {
		t.Errorf("OutputText should mention search_library_docs tool:\n%s", result.OutputText)
	}
}

// =============================================================================
// Local test helpers for get_call_chain tests
// =============================================================================

func createTestGraphWithExternalUpstream(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test-ext-upstream")
	idx := index.NewSymbolIndex()

	processData := &ast.Symbol{
		ID: "process.go:10:ProcessData", Name: "ProcessData",
		Kind: ast.SymbolKindFunction, FilePath: "process.go",
		Exported: true, StartLine: 10, EndLine: 30, Package: "svc", Language: "go",
	}
	internalCaller := &ast.Symbol{
		ID: "handler.go:5:HandleEvent", Name: "HandleEvent",
		Kind: ast.SymbolKindFunction, FilePath: "handler.go",
		Exported: true, StartLine: 5, EndLine: 20, Package: "svc", Language: "go",
	}
	externalCaller := &ast.Symbol{
		ID: "external:framework:Dispatch", Name: "Dispatch",
		Kind: ast.SymbolKindExternal, Package: "framework",
	}

	for _, sym := range []*ast.Symbol{processData, internalCaller} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}
	g.AddNode(externalCaller)

	// Both internal and external call ProcessData
	g.AddEdge(internalCaller.ID, processData.ID, graph.EdgeTypeCalls,
		ast.Location{FilePath: "handler.go", StartLine: 10})
	g.AddEdge(externalCaller.ID, processData.ID, graph.EdgeTypeCalls,
		ast.Location{FilePath: "", StartLine: 0})

	g.Freeze()
	return g, idx
}

// createTestGraphWithMultipleExternals creates a graph with 3 external
// boundaries at different depths for comprehensive output testing.
func createTestGraphWithMultipleExternals(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test-multi-ext")
	idx := index.NewSymbolIndex()

	mainFunc := &ast.Symbol{
		ID: "main.go:1:Main", Name: "Main",
		Kind: ast.SymbolKindFunction, FilePath: "main.go",
		Exported: true, StartLine: 1, EndLine: 20, Package: "main", Language: "go",
	}
	helper := &ast.Symbol{
		ID: "helper.go:1:Setup", Name: "Setup",
		Kind: ast.SymbolKindFunction, FilePath: "helper.go",
		Exported: true, StartLine: 1, EndLine: 15, Package: "main", Language: "go",
	}
	repo := &ast.Symbol{
		ID: "repo.go:1:FetchData", Name: "FetchData",
		Kind: ast.SymbolKindFunction, FilePath: "repo.go",
		Exported: true, StartLine: 1, EndLine: 20, Package: "data", Language: "go",
	}

	extFmt := &ast.Symbol{
		ID: "external:fmt:Println", Name: "Println",
		Kind: ast.SymbolKindExternal, Package: "fmt",
	}
	extOs := &ast.Symbol{
		ID: "external:os:MkdirAll", Name: "MkdirAll",
		Kind: ast.SymbolKindExternal, Package: "os",
	}
	extSQL := &ast.Symbol{
		ID: "external:database/sql:Query", Name: "Query",
		Kind: ast.SymbolKindExternal, Package: "database/sql",
	}

	for _, sym := range []*ast.Symbol{mainFunc, helper, repo} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}
	for _, ext := range []*ast.Symbol{extFmt, extOs, extSQL} {
		g.AddNode(ext)
	}

	// Main → fmt.Println (depth 1), Main → Setup → os.MkdirAll (depth 2),
	// Main → Setup → FetchData → sql.Query (depth 3)
	g.AddEdge(mainFunc.ID, extFmt.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 5})
	g.AddEdge(mainFunc.ID, helper.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 6})
	g.AddEdge(helper.ID, extOs.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "helper.go", StartLine: 5})
	g.AddEdge(helper.ID, repo.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "helper.go", StartLine: 6})
	g.AddEdge(repo.ID, extSQL.ID, graph.EdgeTypeCalls, ast.Location{FilePath: "repo.go", StartLine: 10})

	g.Freeze()
	return g, idx
}

// createTestGraphWithExternalCallees creates a graph with an external boundary
// node for IT-05a testing.
func createTestGraphWithExternalCallees(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("test")
	idx := index.NewSymbolIndex()

	openFunc := &ast.Symbol{
		ID: "db/open.go:10:Open", Name: "Open",
		Kind: ast.SymbolKindFunction, FilePath: "db/open.go",
		Exported: true, StartLine: 10, EndLine: 30, Package: "db", Language: "go",
	}
	validateOpts := &ast.Symbol{
		ID: "db/validate.go:5:validateOpts", Name: "validateOpts",
		Kind: ast.SymbolKindFunction, FilePath: "db/validate.go",
		StartLine: 5, EndLine: 15, Package: "db", Language: "go",
	}
	externalMkdirAll := &ast.Symbol{
		ID: "external:os:MkdirAll", Name: "MkdirAll",
		Kind: ast.SymbolKindExternal, Package: "os",
	}

	for _, sym := range []*ast.Symbol{openFunc, validateOpts} {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Fatalf("Failed to add %s: %v", sym.Name, err)
		}
	}
	// External symbol only in graph (index rejects empty FilePath)
	g.AddNode(externalMkdirAll)

	g.AddEdge(openFunc.ID, validateOpts.ID, graph.EdgeTypeCalls, ast.Location{FilePath: openFunc.FilePath, StartLine: 15})
	g.AddEdge(openFunc.ID, externalMkdirAll.ID, graph.EdgeTypeCalls, ast.Location{FilePath: openFunc.FilePath, StartLine: 20})

	g.Freeze()
	return g, idx
}
