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
	"encoding/json"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestToSerializable_EmptyGraph(t *testing.T) {
	g := NewGraph("/test/project")
	g.Freeze()

	sg := g.ToSerializable()

	if sg.SchemaVersion != GraphSchemaVersion {
		t.Errorf("schema version = %q, want %q", sg.SchemaVersion, GraphSchemaVersion)
	}
	if sg.ProjectRoot != "/test/project" {
		t.Errorf("project root = %q, want %q", sg.ProjectRoot, "/test/project")
	}
	if len(sg.Nodes) != 0 {
		t.Errorf("nodes = %d, want 0", len(sg.Nodes))
	}
	if len(sg.Edges) != 0 {
		t.Errorf("edges = %d, want 0", len(sg.Edges))
	}
	if sg.GraphHash == "" {
		t.Error("graph hash should not be empty")
	}
	if sg.BuiltAtMilli == 0 {
		t.Error("built_at_milli should not be zero for frozen graph")
	}
}

func TestToSerializable_NilGraph(t *testing.T) {
	var g *Graph
	sg := g.ToSerializable()

	if sg.SchemaVersion != GraphSchemaVersion {
		t.Errorf("schema version = %q, want %q", sg.SchemaVersion, GraphSchemaVersion)
	}
	if len(sg.Nodes) != 0 {
		t.Errorf("nodes = %d, want 0", len(sg.Nodes))
	}
	if len(sg.Edges) != 0 {
		t.Errorf("edges = %d, want 0", len(sg.Edges))
	}
}

func TestToSerializable_WithNodesAndEdges(t *testing.T) {
	g := NewGraph("/test/project")

	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	symC := makeSymbol("other.go:1:MyStruct", "MyStruct", ast.SymbolKindStruct, "other.go")

	g.AddNode(symA)
	g.AddNode(symB)
	g.AddNode(symC)

	g.AddEdge("file.go:1:funcA", "file.go:10:funcB", EdgeTypeCalls, makeLocation("file.go", 5))
	g.AddEdge("file.go:10:funcB", "other.go:1:MyStruct", EdgeTypeReferences, makeLocation("file.go", 15))

	g.Freeze()

	sg := g.ToSerializable()

	if len(sg.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(sg.Nodes))
	}
	if len(sg.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(sg.Edges))
	}

	// Verify nodes are sorted by ID
	for i := 1; i < len(sg.Nodes); i++ {
		if sg.Nodes[i-1].ID >= sg.Nodes[i].ID {
			t.Errorf("nodes not sorted: %q >= %q", sg.Nodes[i-1].ID, sg.Nodes[i].ID)
		}
	}

	// Verify edge type string and code
	found := false
	for _, e := range sg.Edges {
		if e.FromID == "file.go:1:funcA" && e.ToID == "file.go:10:funcB" {
			found = true
			if e.Type != "calls" {
				t.Errorf("edge type = %q, want %q", e.Type, "calls")
			}
			if e.TypeCode != EdgeTypeCalls {
				t.Errorf("edge type code = %d, want %d", e.TypeCode, EdgeTypeCalls)
			}
		}
	}
	if !found {
		t.Error("expected edge funcA -> funcB not found")
	}
}

func TestToSerializable_Deterministic(t *testing.T) {
	buildGraph := func() *SerializableGraph {
		g := NewGraph("/test/project")
		symA := makeSymbol("b.go:1:funcB", "funcB", ast.SymbolKindFunction, "b.go")
		symB := makeSymbol("a.go:1:funcA", "funcA", ast.SymbolKindFunction, "a.go")
		g.AddNode(symA)
		g.AddNode(symB)
		g.AddEdge("a.go:1:funcA", "b.go:1:funcB", EdgeTypeCalls, makeLocation("a.go", 5))
		g.Freeze()
		return g.ToSerializable()
	}

	sg1 := buildGraph()
	sg2 := buildGraph()

	json1, _ := json.Marshal(sg1)
	json2, _ := json.Marshal(sg2)

	if string(json1) != string(json2) {
		t.Error("ToSerializable() produced non-deterministic output")
	}
}

func TestToSerializable_JSONRoundTrip(t *testing.T) {
	g := NewGraph("/test/project")

	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symA.Signature = "func(ctx context.Context) error"
	symA.DocComment = "funcA does something."
	symA.Package = "main"
	symA.Exported = true

	g.AddNode(symA)
	g.Freeze()

	sg := g.ToSerializable()

	// Marshal to JSON
	data, err := json.Marshal(sg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Unmarshal back
	var sg2 SerializableGraph
	if err := json.Unmarshal(data, &sg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if sg2.SchemaVersion != sg.SchemaVersion {
		t.Errorf("schema version mismatch: %q vs %q", sg2.SchemaVersion, sg.SchemaVersion)
	}
	if sg2.ProjectRoot != sg.ProjectRoot {
		t.Errorf("project root mismatch")
	}
	if len(sg2.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(sg2.Nodes))
	}
	if sg2.Nodes[0].Symbol.Signature != "func(ctx context.Context) error" {
		t.Errorf("signature not preserved: %q", sg2.Nodes[0].Symbol.Signature)
	}
}

func TestFromSerializable_RoundTrip(t *testing.T) {
	// Build original graph
	g := NewGraph("/test/project")

	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	symC := makeSymbol("other.go:1:MyStruct", "MyStruct", ast.SymbolKindStruct, "other.go")

	g.AddNode(symA)
	g.AddNode(symB)
	g.AddNode(symC)

	loc1 := makeLocation("file.go", 5)
	loc2 := makeLocation("file.go", 15)
	g.AddEdge("file.go:1:funcA", "file.go:10:funcB", EdgeTypeCalls, loc1)
	g.AddEdge("file.go:10:funcB", "other.go:1:MyStruct", EdgeTypeReferences, loc2)

	g.Freeze()

	// Serialize
	sg := g.ToSerializable()

	// Deserialize
	g2, err := FromSerializable(sg)
	if err != nil {
		t.Fatalf("FromSerializable: %v", err)
	}

	// Verify structural equality
	if g2.NodeCount() != g.NodeCount() {
		t.Errorf("node count = %d, want %d", g2.NodeCount(), g.NodeCount())
	}
	if g2.EdgeCount() != g.EdgeCount() {
		t.Errorf("edge count = %d, want %d", g2.EdgeCount(), g.EdgeCount())
	}
	if g2.ProjectRoot != g.ProjectRoot {
		t.Errorf("project root = %q, want %q", g2.ProjectRoot, g.ProjectRoot)
	}
	if g2.BuiltAtMilli != g.BuiltAtMilli {
		t.Errorf("built_at_milli = %d, want %d", g2.BuiltAtMilli, g.BuiltAtMilli)
	}
	if !g2.IsFrozen() {
		t.Error("reconstructed graph should be frozen")
	}

	// Verify nodes exist
	for _, id := range []string{"file.go:1:funcA", "file.go:10:funcB", "other.go:1:MyStruct"} {
		node, ok := g2.GetNode(id)
		if !ok {
			t.Errorf("node %s not found", id)
			continue
		}
		if node.Symbol == nil {
			t.Errorf("node %s has nil symbol", id)
		}
	}

	// Verify secondary indexes
	funcNodes := g2.GetNodesByName("funcA")
	if len(funcNodes) != 1 {
		t.Errorf("GetNodesByName(funcA) = %d, want 1", len(funcNodes))
	}

	structNodes := g2.GetNodesByKind(ast.SymbolKindStruct)
	if len(structNodes) != 1 {
		t.Errorf("GetNodesByKind(Struct) = %d, want 1", len(structNodes))
	}

	callEdges := g2.GetEdgesByType(EdgeTypeCalls)
	if len(callEdges) != 1 {
		t.Errorf("GetEdgesByType(Calls) = %d, want 1", len(callEdges))
	}

	fileEdges := g2.GetEdgesByFile("file.go")
	if len(fileEdges) != 2 {
		t.Errorf("GetEdgesByFile(file.go) = %d, want 2", len(fileEdges))
	}

	// Verify edge details
	nodeA, _ := g2.GetNode("file.go:1:funcA")
	if len(nodeA.Outgoing) != 1 {
		t.Errorf("funcA outgoing = %d, want 1", len(nodeA.Outgoing))
	}
	if len(nodeA.Incoming) != 0 {
		t.Errorf("funcA incoming = %d, want 0", len(nodeA.Incoming))
	}

	nodeB, _ := g2.GetNode("file.go:10:funcB")
	if len(nodeB.Outgoing) != 1 {
		t.Errorf("funcB outgoing = %d, want 1", len(nodeB.Outgoing))
	}
	if len(nodeB.Incoming) != 1 {
		t.Errorf("funcB incoming = %d, want 1", len(nodeB.Incoming))
	}

	// Verify hash is the same
	if g2.Hash() != g.Hash() {
		t.Errorf("hash mismatch: %q vs %q", g2.Hash(), g.Hash())
	}
}

func TestFromSerializable_FullJSONRoundTrip(t *testing.T) {
	// Build, serialize to JSON, unmarshal, reconstruct
	g := NewGraph("/test/project")
	symA := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	symB := makeSymbol("file.go:10:funcB", "funcB", ast.SymbolKindFunction, "file.go")
	g.AddNode(symA)
	g.AddNode(symB)
	g.AddEdge("file.go:1:funcA", "file.go:10:funcB", EdgeTypeCalls, makeLocation("file.go", 5))
	g.Freeze()

	// Serialize → JSON → Deserialize
	sg := g.ToSerializable()
	data, err := json.Marshal(sg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var sg2 SerializableGraph
	if err := json.Unmarshal(data, &sg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	g2, err := FromSerializable(&sg2)
	if err != nil {
		t.Fatalf("FromSerializable: %v", err)
	}

	if g2.NodeCount() != g.NodeCount() {
		t.Errorf("node count = %d, want %d", g2.NodeCount(), g.NodeCount())
	}
	if g2.EdgeCount() != g.EdgeCount() {
		t.Errorf("edge count = %d, want %d", g2.EdgeCount(), g.EdgeCount())
	}
	if g2.Hash() != g.Hash() {
		t.Errorf("hash mismatch after full JSON round-trip")
	}
}

func TestFromSerializable_NilInput(t *testing.T) {
	_, err := FromSerializable(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}

func TestFromSerializable_UnsupportedSchemaVersion(t *testing.T) {
	sg := &SerializableGraph{
		SchemaVersion: "99.0",
		ProjectRoot:   "/test",
		Nodes:         []SerializableNode{},
		Edges:         []SerializableEdge{},
	}

	_, err := FromSerializable(sg)
	if err == nil {
		t.Error("expected error for unsupported schema version")
	}
}

func TestFromSerializable_NilSymbol(t *testing.T) {
	sg := &SerializableGraph{
		SchemaVersion: GraphSchemaVersion,
		ProjectRoot:   "/test",
		Nodes: []SerializableNode{
			{ID: "test:1:foo", Symbol: nil},
		},
	}

	_, err := FromSerializable(sg)
	if err == nil {
		t.Error("expected error for nil symbol")
	}
}

func TestFromSerializable_MissingEdgeNode(t *testing.T) {
	sym := makeSymbol("file.go:1:funcA", "funcA", ast.SymbolKindFunction, "file.go")
	sg := &SerializableGraph{
		SchemaVersion: GraphSchemaVersion,
		ProjectRoot:   "/test",
		Nodes: []SerializableNode{
			{ID: sym.ID, Symbol: sym},
		},
		Edges: []SerializableEdge{
			{
				FromID:   sym.ID,
				ToID:     "nonexistent:1:funcB",
				TypeCode: EdgeTypeCalls,
			},
		},
	}

	_, err := FromSerializable(sg)
	if err == nil {
		t.Error("expected error for edge referencing nonexistent node")
	}
}

func TestFromSerializable_PreservesEdgeLocations(t *testing.T) {
	g := NewGraph("/test")
	symA := makeSymbol("a.go:1:f", "f", ast.SymbolKindFunction, "a.go")
	symB := makeSymbol("b.go:1:g", "g", ast.SymbolKindFunction, "b.go")
	g.AddNode(symA)
	g.AddNode(symB)

	loc := ast.Location{
		FilePath:  "a.go",
		StartLine: 5,
		EndLine:   5,
		StartCol:  10,
		EndCol:    20,
	}
	g.AddEdge(symA.ID, symB.ID, EdgeTypeCalls, loc)
	g.Freeze()

	sg := g.ToSerializable()
	g2, err := FromSerializable(sg)
	if err != nil {
		t.Fatalf("FromSerializable: %v", err)
	}

	nodeA, _ := g2.GetNode(symA.ID)
	if len(nodeA.Outgoing) != 1 {
		t.Fatalf("outgoing = %d, want 1", len(nodeA.Outgoing))
	}

	edge := nodeA.Outgoing[0]
	if edge.Location.FilePath != "a.go" {
		t.Errorf("location file = %q, want %q", edge.Location.FilePath, "a.go")
	}
	if edge.Location.StartLine != 5 {
		t.Errorf("location start line = %d, want 5", edge.Location.StartLine)
	}
	if edge.Location.StartCol != 10 {
		t.Errorf("location start col = %d, want 10", edge.Location.StartCol)
	}
	if edge.Location.EndCol != 20 {
		t.Errorf("location end col = %d, want 20", edge.Location.EndCol)
	}
}
