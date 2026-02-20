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
	"fmt"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestInferPackageFromName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Go standard library
		{"dotted Go", "os.MkdirAll", "os"},
		{"Go multi-segment", "filepath.Join", "filepath"},
		{"Go net/http style", "http.ListenAndServe", "http"},

		// Python
		{"dotted Python", "werkzeug.run_simple", "werkzeug"},
		{"Python multi-dot", "os.path.join", "os.path"},
		{"Python deep", "urllib.parse.urlencode", "urllib.parse"},

		// JavaScript/TypeScript
		{"JS method call", "express.Router", "express"},
		{"TS path-like", "react.createElement", "react"},

		// Edge cases
		{"no dot", "Connect", ""},
		{"leading dot", ".relative", ""},
		{"empty", "", ""},
		{"single char before dot", "x.Foo", "x"},
		{"dot at end", "foo.", "foo"},
		{"just a dot", ".", ""},
		{"double dot", "a..b", "a."},
		{"unicode prefix", "pkg.Функция", "pkg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferPackageFromName(tt.input)
			if got != tt.expected {
				t.Errorf("inferPackageFromName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildDepthMap(t *testing.T) {
	t.Run("simple chain A→B→C", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "C"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				{FromID: "B", ToID: "C"},
			},
		}
		depths := buildDepthMap(result)
		if depths["A"] != 0 {
			t.Errorf("depth[A] = %d, want 0", depths["A"])
		}
		if depths["B"] != 1 {
			t.Errorf("depth[B] = %d, want 1", depths["B"])
		}
		if depths["C"] != 2 {
			t.Errorf("depth[C] = %d, want 2", depths["C"])
		}
	})

	t.Run("branching A→B, A→C", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "C"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				{FromID: "A", ToID: "C"},
			},
		}
		depths := buildDepthMap(result)
		if depths["B"] != 1 {
			t.Errorf("depth[B] = %d, want 1", depths["B"])
		}
		if depths["C"] != 1 {
			t.Errorf("depth[C] = %d, want 1", depths["C"])
		}
	})

	t.Run("empty result", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{},
		}
		depths := buildDepthMap(result)
		if len(depths) != 1 { // StartNode always gets depth 0
			t.Errorf("expected 1 entry (start node), got %d", len(depths))
		}
	})

	t.Run("diamond A→B, A→C, B→D, C→D", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "C", "D"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				{FromID: "A", ToID: "C"},
				{FromID: "B", ToID: "D"},
				{FromID: "C", ToID: "D"},
			},
		}
		depths := buildDepthMap(result)
		if depths["D"] != 2 {
			t.Errorf("depth[D] = %d, want 2 (shortest BFS path)", depths["D"])
		}
	})

	t.Run("deep chain 5 levels", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "L0",
			VisitedNodes: []string{"L0", "L1", "L2", "L3", "L4"},
			Edges: []*Edge{
				{FromID: "L0", ToID: "L1"},
				{FromID: "L1", ToID: "L2"},
				{FromID: "L2", ToID: "L3"},
				{FromID: "L3", ToID: "L4"},
			},
		}
		depths := buildDepthMap(result)
		for i := 0; i <= 4; i++ {
			nodeID := fmt.Sprintf("L%d", i)
			if depths[nodeID] != i {
				t.Errorf("depth[%s] = %d, want %d", nodeID, depths[nodeID], i)
			}
		}
	})

	t.Run("disconnected node gets no depth entry", func(t *testing.T) {
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "Disconnected"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				// No edge to "Disconnected"
			},
		}
		depths := buildDepthMap(result)
		if _, ok := depths["Disconnected"]; ok {
			t.Error("disconnected node should not have a depth entry via BFS")
		}
	})
}

func TestClassifyExternalNodes(t *testing.T) {
	t.Run("nil graph returns nil", func(t *testing.T) {
		result := &TraversalResult{VisitedNodes: []string{"A"}}
		got := ClassifyExternalNodes(nil, result)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("nil result returns nil", func(t *testing.T) {
		g := NewGraph("test")
		got := ClassifyExternalNodes(g, nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty visited nodes returns nil", func(t *testing.T) {
		g := NewGraph("test")
		result := &TraversalResult{VisitedNodes: []string{}}
		got := ClassifyExternalNodes(g, result)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("no external nodes returns nil", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "main.go:1:main", Name: "main",
			Kind: ast.SymbolKindFunction, FilePath: "main.go",
		})
		result := &TraversalResult{
			StartNode:    "main.go:1:main",
			VisitedNodes: []string{"main.go:1:main"},
		}
		got := ClassifyExternalNodes(g, result)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("detects external node with package", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "main.go:1:main", Name: "main",
			Kind: ast.SymbolKindFunction, FilePath: "main.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:pandas:read_csv", Name: "read_csv",
			Kind: ast.SymbolKindExternal, Package: "pandas",
		})
		g.AddEdge("main.go:1:main", "external:pandas:read_csv", EdgeTypeCalls,
			ast.Location{FilePath: "main.go", StartLine: 5})

		result := &TraversalResult{
			StartNode:    "main.go:1:main",
			VisitedNodes: []string{"main.go:1:main", "external:pandas:read_csv"},
			Edges:        []*Edge{{FromID: "main.go:1:main", ToID: "external:pandas:read_csv"}},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		if got[0].Name != "read_csv" {
			t.Errorf("Name = %q, want %q", got[0].Name, "read_csv")
		}
		if got[0].Package != "pandas" {
			t.Errorf("Package = %q, want %q", got[0].Package, "pandas")
		}
		if got[0].CalledFrom != "main.go:1:main" {
			t.Errorf("CalledFrom = %q, want %q", got[0].CalledFrom, "main.go:1:main")
		}
		if got[0].Depth != 1 {
			t.Errorf("Depth = %d, want 1", got[0].Depth)
		}
	})

	t.Run("infers package from dotted name when Package empty", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "main.go:1:main", Name: "main",
			Kind: ast.SymbolKindFunction, FilePath: "main.go",
		})
		// Legacy placeholder without Package field
		g.AddNode(&ast.Symbol{
			ID: "external::os.MkdirAll", Name: "os.MkdirAll",
			Kind: ast.SymbolKindExternal, Package: "",
		})
		g.AddEdge("main.go:1:main", "external::os.MkdirAll", EdgeTypeCalls,
			ast.Location{FilePath: "main.go", StartLine: 10})

		result := &TraversalResult{
			StartNode:    "main.go:1:main",
			VisitedNodes: []string{"main.go:1:main", "external::os.MkdirAll"},
			Edges:        []*Edge{{FromID: "main.go:1:main", ToID: "external::os.MkdirAll"}},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		if got[0].Package != "os" {
			t.Errorf("Package = %q, want %q (inferred from dotted name)", got[0].Package, "os")
		}
	})

	t.Run("multiple externals at different depths", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "A", Name: "funcA",
			Kind: ast.SymbolKindFunction, FilePath: "a.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "B", Name: "funcB",
			Kind: ast.SymbolKindFunction, FilePath: "b.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:os:MkdirAll", Name: "MkdirAll",
			Kind: ast.SymbolKindExternal, Package: "os",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:fmt:Println", Name: "Println",
			Kind: ast.SymbolKindExternal, Package: "fmt",
		})
		g.AddEdge("A", "B", EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 5})
		g.AddEdge("A", "external:fmt:Println", EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 6})
		g.AddEdge("B", "external:os:MkdirAll", EdgeTypeCalls, ast.Location{FilePath: "b.go", StartLine: 10})

		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "external:fmt:Println", "external:os:MkdirAll"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				{FromID: "A", ToID: "external:fmt:Println"},
				{FromID: "B", ToID: "external:os:MkdirAll"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 2 {
			t.Fatalf("expected 2 externals, got %d", len(got))
		}

		// Find each by name
		var fmtExt, osExt *ExternalDependency
		for i := range got {
			if got[i].Name == "Println" {
				fmtExt = &got[i]
			}
			if got[i].Name == "MkdirAll" {
				osExt = &got[i]
			}
		}

		if fmtExt == nil || osExt == nil {
			t.Fatal("expected both Println and MkdirAll externals")
		}

		if fmtExt.Depth != 1 {
			t.Errorf("Println depth = %d, want 1", fmtExt.Depth)
		}
		if osExt.Depth != 2 {
			t.Errorf("MkdirAll depth = %d, want 2", osExt.Depth)
		}
		if fmtExt.CalledFrom != "A" {
			t.Errorf("Println CalledFrom = %q, want %q", fmtExt.CalledFrom, "A")
		}
		if osExt.CalledFrom != "B" {
			t.Errorf("MkdirAll CalledFrom = %q, want %q", osExt.CalledFrom, "B")
		}
	})

	t.Run("node not in graph is skipped", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "A", Name: "funcA",
			Kind: ast.SymbolKindFunction, FilePath: "a.go",
		})
		// Traversal references a node ID "ghost" not in the graph
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "ghost"},
			Edges:        []*Edge{{FromID: "A", ToID: "ghost"}},
		}
		got := ClassifyExternalNodes(g, result)
		if got != nil {
			t.Errorf("expected nil (ghost not in graph, so not external), got %v", got)
		}
	})

	t.Run("node with nil Symbol is skipped", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "A", Name: "funcA",
			Kind: ast.SymbolKindFunction, FilePath: "a.go",
		})
		// Manually inject a node with nil Symbol by adding then removing
		// This tests the nil guard in ClassifyExternalNodes
		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A"},
		}
		got := ClassifyExternalNodes(g, result)
		if got != nil {
			t.Errorf("expected nil for internal-only nodes, got %v", got)
		}
	})

	t.Run("external at depth 0 is root node itself", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "external:redis:Connect", Name: "Connect",
			Kind: ast.SymbolKindExternal, Package: "redis",
		})
		result := &TraversalResult{
			StartNode:    "external:redis:Connect",
			VisitedNodes: []string{"external:redis:Connect"},
		}
		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		if got[0].Depth != 0 {
			t.Errorf("Depth = %d, want 0 (root node)", got[0].Depth)
		}
		if got[0].CalledFrom != "" {
			t.Errorf("CalledFrom = %q, want empty (no caller for root)", got[0].CalledFrom)
		}
	})

	t.Run("mixed internal and external preserves order", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "main", Name: "main",
			Kind: ast.SymbolKindFunction, FilePath: "main.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "internal1", Name: "processData",
			Kind: ast.SymbolKindFunction, FilePath: "process.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:pandas:read_csv", Name: "read_csv",
			Kind: ast.SymbolKindExternal, Package: "pandas",
		})
		g.AddNode(&ast.Symbol{
			ID: "internal2", Name: "saveResult",
			Kind: ast.SymbolKindFunction, FilePath: "save.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:boto3:upload_file", Name: "upload_file",
			Kind: ast.SymbolKindExternal, Package: "boto3",
		})

		g.AddEdge("main", "internal1", EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 1})
		g.AddEdge("main", "external:pandas:read_csv", EdgeTypeCalls, ast.Location{FilePath: "main.go", StartLine: 2})
		g.AddEdge("internal1", "internal2", EdgeTypeCalls, ast.Location{FilePath: "process.go", StartLine: 1})
		g.AddEdge("internal2", "external:boto3:upload_file", EdgeTypeCalls, ast.Location{FilePath: "save.go", StartLine: 1})

		result := &TraversalResult{
			StartNode: "main",
			VisitedNodes: []string{
				"main", "internal1", "external:pandas:read_csv",
				"internal2", "external:boto3:upload_file",
			},
			Edges: []*Edge{
				{FromID: "main", ToID: "internal1"},
				{FromID: "main", ToID: "external:pandas:read_csv"},
				{FromID: "internal1", ToID: "internal2"},
				{FromID: "internal2", ToID: "external:boto3:upload_file"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 2 {
			t.Fatalf("expected 2 externals, got %d", len(got))
		}

		// Should preserve traversal order
		if got[0].Name != "read_csv" {
			t.Errorf("first external = %q, want %q (traversal order)", got[0].Name, "read_csv")
		}
		if got[1].Name != "upload_file" {
			t.Errorf("second external = %q, want %q (traversal order)", got[1].Name, "upload_file")
		}

		// Verify depth values
		if got[0].Depth != 1 {
			t.Errorf("read_csv depth = %d, want 1", got[0].Depth)
		}
		if got[1].Depth != 3 {
			t.Errorf("upload_file depth = %d, want 3", got[1].Depth)
		}
	})

	t.Run("external with empty package and no dot in name", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "A", Name: "caller",
			Kind: ast.SymbolKindFunction, FilePath: "a.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external::Connect", Name: "Connect",
			Kind: ast.SymbolKindExternal, Package: "",
		})
		g.AddEdge("A", "external::Connect", EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 1})

		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "external::Connect"},
			Edges:        []*Edge{{FromID: "A", ToID: "external::Connect"}},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		// Package should remain empty — no dot in "Connect" to infer from
		if got[0].Package != "" {
			t.Errorf("Package = %q, want empty (no inference possible)", got[0].Package)
		}
	})

	t.Run("multiple callers map to first edge only", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "A", Name: "funcA",
			Kind: ast.SymbolKindFunction, FilePath: "a.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "B", Name: "funcB",
			Kind: ast.SymbolKindFunction, FilePath: "b.go",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:log:Println", Name: "Println",
			Kind: ast.SymbolKindExternal, Package: "log",
		})
		g.AddEdge("A", "external:log:Println", EdgeTypeCalls, ast.Location{FilePath: "a.go", StartLine: 1})
		g.AddEdge("B", "external:log:Println", EdgeTypeCalls, ast.Location{FilePath: "b.go", StartLine: 1})

		result := &TraversalResult{
			StartNode:    "A",
			VisitedNodes: []string{"A", "B", "external:log:Println"},
			Edges: []*Edge{
				{FromID: "A", ToID: "B"},
				{FromID: "A", ToID: "external:log:Println"},
				{FromID: "B", ToID: "external:log:Println"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		// CalledFrom should be first edge's source ("A"), not "B"
		if got[0].CalledFrom != "A" {
			t.Errorf("CalledFrom = %q, want %q (first edge wins)", got[0].CalledFrom, "A")
		}
	})

	t.Run("Python Flask pipeline with multiple external boundaries", func(t *testing.T) {
		// Simulates: app.run() → werkzeug.run_simple() + logging.getLogger()
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "app.py:10:run", Name: "run",
			Kind: ast.SymbolKindMethod, FilePath: "app.py", Package: "myapp",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:werkzeug.serving:run_simple", Name: "run_simple",
			Kind: ast.SymbolKindExternal, Package: "werkzeug.serving",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:logging:getLogger", Name: "getLogger",
			Kind: ast.SymbolKindExternal, Package: "logging",
		})

		g.AddEdge("app.py:10:run", "external:werkzeug.serving:run_simple", EdgeTypeCalls,
			ast.Location{FilePath: "app.py", StartLine: 15})
		g.AddEdge("app.py:10:run", "external:logging:getLogger", EdgeTypeCalls,
			ast.Location{FilePath: "app.py", StartLine: 12})

		result := &TraversalResult{
			StartNode:    "app.py:10:run",
			VisitedNodes: []string{"app.py:10:run", "external:werkzeug.serving:run_simple", "external:logging:getLogger"},
			Edges: []*Edge{
				{FromID: "app.py:10:run", ToID: "external:werkzeug.serving:run_simple"},
				{FromID: "app.py:10:run", ToID: "external:logging:getLogger"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 2 {
			t.Fatalf("expected 2 externals, got %d", len(got))
		}

		packages := make(map[string]bool)
		for _, ext := range got {
			packages[ext.Package] = true
			if ext.Depth != 1 {
				t.Errorf("%s depth = %d, want 1", ext.Name, ext.Depth)
			}
			if ext.CalledFrom != "app.py:10:run" {
				t.Errorf("%s CalledFrom = %q, want %q", ext.Name, ext.CalledFrom, "app.py:10:run")
			}
		}
		if !packages["werkzeug.serving"] {
			t.Error("expected werkzeug.serving in external packages")
		}
		if !packages["logging"] {
			t.Error("expected logging in external packages")
		}
	})

	t.Run("Go deep chain internal→internal→external", func(t *testing.T) {
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "handler", Name: "HandleRequest",
			Kind: ast.SymbolKindFunction, FilePath: "handler.go", Package: "api",
		})
		g.AddNode(&ast.Symbol{
			ID: "service", Name: "ProcessOrder",
			Kind: ast.SymbolKindFunction, FilePath: "service.go", Package: "service",
		})
		g.AddNode(&ast.Symbol{
			ID: "repo", Name: "SaveOrder",
			Kind: ast.SymbolKindFunction, FilePath: "repo.go", Package: "repo",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:database/sql:Exec", Name: "Exec",
			Kind: ast.SymbolKindExternal, Package: "database/sql",
		})

		g.AddEdge("handler", "service", EdgeTypeCalls, ast.Location{FilePath: "handler.go", StartLine: 1})
		g.AddEdge("service", "repo", EdgeTypeCalls, ast.Location{FilePath: "service.go", StartLine: 1})
		g.AddEdge("repo", "external:database/sql:Exec", EdgeTypeCalls, ast.Location{FilePath: "repo.go", StartLine: 1})

		result := &TraversalResult{
			StartNode:    "handler",
			VisitedNodes: []string{"handler", "service", "repo", "external:database/sql:Exec"},
			Edges: []*Edge{
				{FromID: "handler", ToID: "service"},
				{FromID: "service", ToID: "repo"},
				{FromID: "repo", ToID: "external:database/sql:Exec"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 1 {
			t.Fatalf("expected 1 external, got %d", len(got))
		}
		if got[0].Depth != 3 {
			t.Errorf("Exec depth = %d, want 3 (handler→service→repo→Exec)", got[0].Depth)
		}
		if got[0].CalledFrom != "repo" {
			t.Errorf("Exec CalledFrom = %q, want %q", got[0].CalledFrom, "repo")
		}
		if got[0].Package != "database/sql" {
			t.Errorf("Exec Package = %q, want %q", got[0].Package, "database/sql")
		}
	})

	t.Run("JS/TS Express pipeline", func(t *testing.T) {
		// Simulates: app.listen() → calls http.createServer (external)
		g := NewGraph("test")
		g.AddNode(&ast.Symbol{
			ID: "app.ts:5:listen", Name: "listen",
			Kind: ast.SymbolKindMethod, FilePath: "app.ts", Package: "myapp",
		})
		g.AddNode(&ast.Symbol{
			ID: "middleware.ts:10:cors", Name: "cors",
			Kind: ast.SymbolKindFunction, FilePath: "middleware.ts", Package: "myapp",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:http:createServer", Name: "createServer",
			Kind: ast.SymbolKindExternal, Package: "http",
		})
		g.AddNode(&ast.Symbol{
			ID: "external:cors:corsMiddleware", Name: "corsMiddleware",
			Kind: ast.SymbolKindExternal, Package: "cors",
		})

		g.AddEdge("app.ts:5:listen", "middleware.ts:10:cors", EdgeTypeCalls, ast.Location{FilePath: "app.ts", StartLine: 10})
		g.AddEdge("app.ts:5:listen", "external:http:createServer", EdgeTypeCalls, ast.Location{FilePath: "app.ts", StartLine: 15})
		g.AddEdge("middleware.ts:10:cors", "external:cors:corsMiddleware", EdgeTypeCalls, ast.Location{FilePath: "middleware.ts", StartLine: 12})

		result := &TraversalResult{
			StartNode: "app.ts:5:listen",
			VisitedNodes: []string{
				"app.ts:5:listen", "middleware.ts:10:cors",
				"external:http:createServer", "external:cors:corsMiddleware",
			},
			Edges: []*Edge{
				{FromID: "app.ts:5:listen", ToID: "middleware.ts:10:cors"},
				{FromID: "app.ts:5:listen", ToID: "external:http:createServer"},
				{FromID: "middleware.ts:10:cors", ToID: "external:cors:corsMiddleware"},
			},
		}

		got := ClassifyExternalNodes(g, result)
		if len(got) != 2 {
			t.Fatalf("expected 2 externals, got %d", len(got))
		}

		var httpExt, corsExt *ExternalDependency
		for i := range got {
			if got[i].Package == "http" {
				httpExt = &got[i]
			}
			if got[i].Package == "cors" {
				corsExt = &got[i]
			}
		}

		if httpExt == nil || corsExt == nil {
			t.Fatal("expected both http and cors externals")
		}
		if httpExt.Depth != 1 {
			t.Errorf("http external depth = %d, want 1", httpExt.Depth)
		}
		if corsExt.Depth != 2 {
			t.Errorf("cors external depth = %d, want 2", corsExt.Depth)
		}
	})
}
