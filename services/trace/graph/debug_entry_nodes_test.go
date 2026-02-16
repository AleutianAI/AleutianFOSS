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
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// TestDetectEntryNodes_Debug is a diagnostic test to understand why DetectEntryNodes
// is returning empty despite having a main function.
func TestDetectEntryNodes_Debug(t *testing.T) {
	t.Run("simple main function", func(t *testing.T) {
		g := NewGraph("/test/project")

		main := &ast.Symbol{
			ID:       "main.go:1:main",
			Name:     "main",
			Package:  "main",
			Kind:     ast.SymbolKindFunction,
			Language: "go",
		}

		helper := &ast.Symbol{
			ID:       "helper.go:1:helper",
			Name:     "helper",
			Package:  "main",
			Kind:     ast.SymbolKindFunction,
			Language: "go",
		}

		if _, err := g.AddNode(main); err != nil {
			t.Fatalf("AddNode main: %v", err)
		}
		if _, err := g.AddNode(helper); err != nil {
			t.Fatalf("AddNode helper: %v", err)
		}

		// main calls helper (main -> helper)
		if err := g.AddEdge(main.ID, helper.ID, EdgeTypeCalls, ast.Location{}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}

		g.Freeze()

		hg, err := WrapGraph(g)
		if err != nil {
			t.Fatalf("WrapGraph: %v", err)
		}

		analytics := NewGraphAnalytics(hg)
		entries := analytics.DetectEntryNodes(context.Background())

		t.Logf("Total nodes: %d", g.NodeCount())
		t.Logf("Total edges: %d", len(g.Edges()))
		t.Logf("Detected entry nodes: %d", len(entries))

		// Inspect all nodes
		for _, node := range g.Nodes() {
			t.Logf("Node %s: Name=%s, Incoming=%d, Outgoing=%d",
				node.ID, node.Symbol.Name,
				len(node.Incoming), len(node.Outgoing))

			if len(node.Incoming) > 0 {
				t.Logf("  Incoming edges:")
				for _, edge := range node.Incoming {
					fromNode, _ := g.GetNode(edge.FromID)
					t.Logf("    <- %s (type=%s)", fromNode.Symbol.Name, edge.Type.String())
				}
			}
		}

		if len(entries) == 0 {
			t.Fatal("Expected to find entry nodes, but got empty list")
		}

		if len(entries) != 1 {
			t.Fatalf("Expected 1 entry node, got %d", len(entries))
		}

		if entries[0] != main.ID {
			t.Errorf("Expected main to be entry node, got %s", entries[0])
		}
	})
}
