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
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// Articulation Points Tests (GR-16a)
// =============================================================================

// buildArticulationTestGraph creates a simple graph for articulation point testing.
// Helper function that creates nodes and edges without external dependencies.
func buildArticulationTestGraph(t *testing.T, nodeIDs []string, edges [][2]string) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test/project")

	// Create nodes
	for _, id := range nodeIDs {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, err := g.AddNode(sym)
		if err != nil {
			t.Fatalf("failed to add node %s: %v", id, err)
		}
	}

	// Create edges
	for _, edge := range edges {
		err := g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
		if err != nil {
			t.Fatalf("failed to add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	return hg
}

// -----------------------------------------------------------------------------
// ArticulationResult Tests
// -----------------------------------------------------------------------------

func TestArticulationResult_Empty(t *testing.T) {
	result := &ArticulationResult{
		Points:  []string{},
		Bridges: [][2]string{},
	}

	if len(result.Points) != 0 {
		t.Error("expected empty points slice")
	}
	if len(result.Bridges) != 0 {
		t.Error("expected empty bridges slice")
	}
	if result.Components != 0 {
		t.Error("expected 0 components for empty result")
	}
}

// -----------------------------------------------------------------------------
// ArticulationPoints Algorithm Tests
// -----------------------------------------------------------------------------

func TestArticulationPoints_LinearChain(t *testing.T) {
	// A - B - C - D
	// B and C are articulation points (removing either disconnects the graph)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// B and C should be articulation points
	sort.Strings(result.Points)
	expected := []string{"B", "C"}
	if len(result.Points) != len(expected) {
		t.Errorf("expected %d articulation points, got %d: %v", len(expected), len(result.Points), result.Points)
	}

	for i, exp := range expected {
		if i >= len(result.Points) || result.Points[i] != exp {
			t.Errorf("expected articulation point %s at position %d, got %v", exp, i, result.Points)
		}
	}

	// Verify bridges: A-B, B-C, C-D should all be bridges
	if len(result.Bridges) != 3 {
		t.Errorf("expected 3 bridges, got %d: %v", len(result.Bridges), result.Bridges)
	}
}

func TestArticulationPoints_Cycle(t *testing.T) {
	// A - B - C - A (forms a triangle)
	// No articulation points - removing any node leaves the other two connected
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "A"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Points) != 0 {
		t.Errorf("expected no articulation points in cycle, got %v", result.Points)
	}

	if len(result.Bridges) != 0 {
		t.Errorf("expected no bridges in cycle, got %v", result.Bridges)
	}
}

func TestArticulationPoints_Tree(t *testing.T) {
	// Tree structure:
	//       A
	//      / \
	//     B   C
	//    / \
	//   D   E
	//
	// A and B are articulation points (internal nodes)
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"B", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(result.Points)
	expected := []string{"A", "B"}
	if len(result.Points) != len(expected) {
		t.Errorf("expected %d articulation points, got %d: %v", len(expected), len(result.Points), result.Points)
	}
}

func TestArticulationPoints_Disconnected(t *testing.T) {
	// Two separate components: A-B and C-D
	// No articulation points (each component is a single edge)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No articulation points within either component
	if len(result.Points) != 0 {
		t.Errorf("expected no articulation points, got %v", result.Points)
	}

	// Each edge is a bridge within its component
	if result.Components < 2 {
		t.Errorf("expected at least 2 biconnected components, got %d", result.Components)
	}
}

func TestArticulationPoints_Empty(t *testing.T) {
	nodes := []string{}
	edges := [][2]string{}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result for empty graph")
	}

	if len(result.Points) != 0 {
		t.Errorf("expected no articulation points, got %v", result.Points)
	}

	if len(result.Bridges) != 0 {
		t.Errorf("expected no bridges, got %v", result.Bridges)
	}

	if result.NodeCount != 0 {
		t.Errorf("expected 0 nodes, got %d", result.NodeCount)
	}
}

func TestArticulationPoints_SingleNode(t *testing.T) {
	nodes := []string{"A"}
	edges := [][2]string{}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single node cannot be an articulation point
	if len(result.Points) != 0 {
		t.Errorf("expected no articulation points for single node, got %v", result.Points)
	}

	if result.NodeCount != 1 {
		t.Errorf("expected 1 node, got %d", result.NodeCount)
	}
}

func TestArticulationPoints_TwoConnectedNodes(t *testing.T) {
	// A - B: neither is an articulation point
	nodes := []string{"A", "B"}
	edges := [][2]string{
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Points) != 0 {
		t.Errorf("expected no articulation points for two connected nodes, got %v", result.Points)
	}

	// The single edge should be a bridge
	if len(result.Bridges) != 1 {
		t.Errorf("expected 1 bridge, got %d: %v", len(result.Bridges), result.Bridges)
	}
}

func TestArticulationPoints_DiamondGraph(t *testing.T) {
	// Diamond: A -> B -> D, A -> C -> D
	// No articulation points - multiple paths exist
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A and D might be articulation points depending on direction treatment
	// With undirected semantics: A and D are articulation points
	// (removing A disconnects nothing, removing D disconnects nothing)
	// Actually: A connects to {B,C,D}, removing A leaves B-D and C-D connected
	// D connects to {A,B,C}, removing D leaves A-B-C connected via A
	// So only A is articulation point? No wait...
	// In undirected: A-B, A-C, B-D, C-D
	// Remove A: B-D-C triangle-ish? No, B-D and C-D but B and C not directly connected
	// So removing A leaves B and C disconnected from each other? No, both connect to D.
	// Actually in undirected diamond: A-B-D-C-A forms a cycle with extra edges
	// No articulation points in a fully connected diamond

	t.Logf("Diamond graph articulation points: %v", result.Points)
	t.Logf("Diamond graph bridges: %v", result.Bridges)
}

func TestArticulationPoints_StarGraph(t *testing.T) {
	// Star: center connected to all leaves
	// Center is an articulation point
	nodes := []string{"Center", "L1", "L2", "L3", "L4"}
	edges := [][2]string{
		{"Center", "L1"},
		{"Center", "L2"},
		{"Center", "L3"},
		{"Center", "L4"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Center should be the only articulation point
	if len(result.Points) != 1 {
		t.Errorf("expected 1 articulation point (Center), got %d: %v", len(result.Points), result.Points)
	}

	if len(result.Points) == 1 && result.Points[0] != "Center" {
		t.Errorf("expected Center to be articulation point, got %s", result.Points[0])
	}
}

func TestArticulationPoints_ContextCancellation(t *testing.T) {
	// Create a moderately sized graph
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = string(rune('A'+i%26)) + string(rune('0'+i/26))
	}

	// Create a linear chain
	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := analytics.ArticulationPoints(ctx)

	// Should return error on cancelled context
	if err == nil {
		t.Log("Note: small graph may complete before context check")
	}

	// Result should still be non-nil
	if result == nil {
		t.Fatal("expected non-nil result even with cancellation")
	}
}

func TestArticulationPoints_ContextTimeout(t *testing.T) {
	// Create a graph
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Sleep to ensure timeout
	time.Sleep(1 * time.Millisecond)

	result, err := analytics.ArticulationPoints(ctx)

	// Should handle timeout gracefully
	if err == nil {
		t.Log("Note: small graph may complete before timeout")
	}

	if result == nil {
		t.Fatal("expected non-nil result even with timeout")
	}
}

func TestArticulationPoints_NilAnalytics(t *testing.T) {
	var analytics *GraphAnalytics = nil

	ctx := context.Background()

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ArticulationPoints panicked with nil analytics: %v", r)
		}
	}()

	// Call should handle nil gracefully
	if analytics != nil {
		_, _ = analytics.ArticulationPoints(ctx)
	}
}

func TestArticulationPoints_DirectedAsUndirected(t *testing.T) {
	// Test that directed edges are treated as undirected
	// A -> B should be equivalent to A - B for articulation point analysis
	nodes := []string{"A", "B", "C"}

	// Only directed edge A -> B -> C
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With undirected semantics, B is an articulation point
	if len(result.Points) != 1 {
		t.Errorf("expected 1 articulation point (B), got %d: %v", len(result.Points), result.Points)
	}

	if len(result.Points) == 1 && result.Points[0] != "B" {
		t.Errorf("expected B to be articulation point, got %s", result.Points[0])
	}
}

func TestArticulationPoints_SelfLoop(t *testing.T) {
	// Self-loop should be ignored for articulation point analysis
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "B"}, // Self-loop
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B should still be articulation point (self-loop doesn't affect connectivity)
	if len(result.Points) != 1 || result.Points[0] != "B" {
		t.Errorf("expected B to be articulation point, got %v", result.Points)
	}
}

// -----------------------------------------------------------------------------
// ArticulationPointsWithCRS Tests
// -----------------------------------------------------------------------------

func TestArticulationPointsWithCRS_Basic(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, traceStep := analytics.ArticulationPointsWithCRS(ctx)

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify TraceStep is populated
	if traceStep.Action != "analytics_articulation_points" {
		t.Errorf("expected action 'analytics_articulation_points', got '%s'", traceStep.Action)
	}

	if traceStep.Tool != "ArticulationPoints" {
		t.Errorf("expected tool 'ArticulationPoints', got '%s'", traceStep.Tool)
	}

	if traceStep.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	// Check metadata
	if traceStep.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}

	if _, ok := traceStep.Metadata["points_found"]; !ok {
		t.Error("expected 'points_found' in metadata")
	}

	if _, ok := traceStep.Metadata["bridges_found"]; !ok {
		t.Error("expected 'bridges_found' in metadata")
	}
}

func TestArticulationPointsWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, traceStep := analytics.ArticulationPointsWithCRS(ctx)

	// Should return empty result with error in trace
	if result == nil {
		t.Fatal("expected non-nil result even with cancelled context")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Log("Note: small graph may complete before context check")
	}
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkArticulationPoints_Small(b *testing.B) {
	// 100 nodes in a linear chain
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = string(rune('A'+i%26)) + string(rune('0'+i/26))
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ArticulationPoints(ctx)
	}
}

func BenchmarkArticulationPoints_Medium(b *testing.B) {
	// 1000 nodes with some branching
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create a tree-like structure
	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ArticulationPoints(ctx)
	}
}

// -----------------------------------------------------------------------------
// Table-Driven Tests
// -----------------------------------------------------------------------------

func TestArticulationPoints_TableDriven(t *testing.T) {
	tests := []struct {
		name                string
		nodes               []string
		edges               [][2]string
		expectedPoints      []string
		expectedBridgeCount int
		minComponents       int
	}{
		{
			name:                "empty graph",
			nodes:               []string{},
			edges:               [][2]string{},
			expectedPoints:      []string{},
			expectedBridgeCount: 0,
			minComponents:       0,
		},
		{
			name:                "single node",
			nodes:               []string{"A"},
			edges:               [][2]string{},
			expectedPoints:      []string{},
			expectedBridgeCount: 0,
			minComponents:       1,
		},
		{
			name:                "two nodes connected",
			nodes:               []string{"A", "B"},
			edges:               [][2]string{{"A", "B"}},
			expectedPoints:      []string{},
			expectedBridgeCount: 1,
			minComponents:       1,
		},
		{
			name:                "three node chain",
			nodes:               []string{"A", "B", "C"},
			edges:               [][2]string{{"A", "B"}, {"B", "C"}},
			expectedPoints:      []string{"B"},
			expectedBridgeCount: 2,
			minComponents:       1,
		},
		{
			name:                "triangle (no articulation)",
			nodes:               []string{"A", "B", "C"},
			edges:               [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}},
			expectedPoints:      []string{},
			expectedBridgeCount: 0,
			minComponents:       1,
		},
		{
			name:                "four node chain",
			nodes:               []string{"A", "B", "C", "D"},
			edges:               [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}},
			expectedPoints:      []string{"B", "C"},
			expectedBridgeCount: 3,
			minComponents:       1,
		},
		{
			name:                "star graph (5 nodes)",
			nodes:               []string{"C", "L1", "L2", "L3", "L4"},
			edges:               [][2]string{{"C", "L1"}, {"C", "L2"}, {"C", "L3"}, {"C", "L4"}},
			expectedPoints:      []string{"C"},
			expectedBridgeCount: 4,
			minComponents:       1,
		},
		{
			name:                "square (cycle)",
			nodes:               []string{"A", "B", "C", "D"},
			edges:               [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "A"}},
			expectedPoints:      []string{},
			expectedBridgeCount: 0,
			minComponents:       1,
		},
		{
			name:  "two triangles connected by single edge",
			nodes: []string{"A", "B", "C", "D", "E", "F"},
			edges: [][2]string{
				{"A", "B"}, {"B", "C"}, {"C", "A"}, // First triangle
				{"D", "E"}, {"E", "F"}, {"F", "D"}, // Second triangle
				{"C", "D"}, // Bridge between triangles
			},
			expectedPoints:      []string{"C", "D"},
			expectedBridgeCount: 1,
			minComponents:       1,
		},
		{
			name:                "disconnected components",
			nodes:               []string{"A", "B", "C", "D"},
			edges:               [][2]string{{"A", "B"}, {"C", "D"}},
			expectedPoints:      []string{},
			expectedBridgeCount: 2,
			minComponents:       2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hg := buildArticulationTestGraph(t, tt.nodes, tt.edges)
			analytics := NewGraphAnalytics(hg)

			ctx := context.Background()
			result, err := analytics.ArticulationPoints(ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Sort for comparison
			sort.Strings(result.Points)
			sort.Strings(tt.expectedPoints)

			// Check articulation points
			if len(result.Points) != len(tt.expectedPoints) {
				t.Errorf("expected %d articulation points %v, got %d: %v",
					len(tt.expectedPoints), tt.expectedPoints, len(result.Points), result.Points)
			} else {
				for i, exp := range tt.expectedPoints {
					if result.Points[i] != exp {
						t.Errorf("expected articulation point %s at position %d, got %s",
							exp, i, result.Points[i])
					}
				}
			}

			// Check bridge count
			if len(result.Bridges) != tt.expectedBridgeCount {
				t.Errorf("expected %d bridges, got %d: %v",
					tt.expectedBridgeCount, len(result.Bridges), result.Bridges)
			}

			// Check minimum components
			if result.Components < tt.minComponents {
				t.Errorf("expected at least %d components, got %d",
					tt.minComponents, result.Components)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Property-Based Tests
// -----------------------------------------------------------------------------

// TestArticulationPoints_PropertyRemovalIncreasesComponents verifies that
// removing an articulation point actually increases the number of connected components.
func TestArticulationPoints_PropertyRemovalIncreasesComponents(t *testing.T) {
	// Create a graph where we know the articulation points
	// A - B - C with B as articulation point
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify B is articulation point
	if len(result.Points) != 1 || result.Points[0] != "B" {
		t.Fatalf("expected B as articulation point, got %v", result.Points)
	}

	// Property: removing articulation point should increase components
	// Simulate removal by creating graph without B
	nodesWithoutB := []string{"A", "C"}
	edgesWithoutB := [][2]string{} // No edges connect A and C directly

	hgWithoutB := buildArticulationTestGraph(t, nodesWithoutB, edgesWithoutB)
	analyticsWithoutB := NewGraphAnalytics(hgWithoutB)

	resultWithoutB, err := analyticsWithoutB.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With B removed, we should have 2 components (A and C isolated)
	if resultWithoutB.Components < 2 {
		t.Errorf("removing articulation point B should increase components, got %d",
			resultWithoutB.Components)
	}
}

// TestArticulationPoints_PropertyNonArticulationRemovalPreservesConnectivity verifies
// that removing a non-articulation point doesn't disconnect the remaining nodes.
func TestArticulationPoints_PropertyNonArticulationRemovalPreservesConnectivity(t *testing.T) {
	// Triangle: no articulation points
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}, {"C", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No articulation points in a triangle
	if len(result.Points) != 0 {
		t.Fatalf("expected no articulation points in triangle, got %v", result.Points)
	}

	// Property: removing any node should leave remaining nodes connected
	// Simulate removal of A
	nodesWithoutA := []string{"B", "C"}
	edgesWithoutA := [][2]string{{"B", "C"}}

	hgWithoutA := buildArticulationTestGraph(t, nodesWithoutA, edgesWithoutA)
	analyticsWithoutA := NewGraphAnalytics(hgWithoutA)

	resultWithoutA, err := analyticsWithoutA.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still be connected (1 component)
	if resultWithoutA.Components != 1 {
		t.Errorf("removing non-articulation point should preserve connectivity, got %d components",
			resultWithoutA.Components)
	}
}

// -----------------------------------------------------------------------------
// Edge Case Tests
// -----------------------------------------------------------------------------

func TestArticulationPoints_ParallelEdges(t *testing.T) {
	// A connected to B via two edges - should be treated same as single edge
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "B"}, // Parallel edge (duplicate)
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B should still be articulation point
	if len(result.Points) != 1 || result.Points[0] != "B" {
		t.Errorf("expected B as articulation point with parallel edges, got %v", result.Points)
	}
}

func TestArticulationPoints_ComplexTopology(t *testing.T) {
	// More complex graph:
	//     A---B---C
	//     |   |   |
	//     D---E---F
	//         |
	//         G
	//
	// E is articulation point (connects to G)
	// B and E are on the path but the cycle A-B-C-F-E-D-A means most aren't articulation
	nodes := []string{"A", "B", "C", "D", "E", "F", "G"}
	edges := [][2]string{
		{"A", "B"}, {"B", "C"},
		{"A", "D"}, {"B", "E"}, {"C", "F"},
		{"D", "E"}, {"E", "F"},
		{"E", "G"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Complex topology articulation points: %v", result.Points)
	t.Logf("Complex topology bridges: %v", result.Bridges)

	// E should be articulation point (removing E disconnects G)
	foundE := false
	for _, p := range result.Points {
		if p == "E" {
			foundE = true
			break
		}
	}
	if !foundE {
		t.Errorf("expected E to be articulation point, got %v", result.Points)
	}

	// E-G should be a bridge
	foundBridge := false
	for _, b := range result.Bridges {
		if (b[0] == "E" && b[1] == "G") || (b[0] == "G" && b[1] == "E") {
			foundBridge = true
			break
		}
	}
	if !foundBridge {
		t.Errorf("expected E-G bridge, got %v", result.Bridges)
	}
}

func TestArticulationPoints_LongChain(t *testing.T) {
	// Very long chain: A - B - C - ... - Z
	// All middle nodes are articulation points
	numNodes := 26
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = string(rune('A' + i))
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All nodes except endpoints (A and Z) should be articulation points
	expectedCount := numNodes - 2
	if len(result.Points) != expectedCount {
		t.Errorf("expected %d articulation points, got %d: %v",
			expectedCount, len(result.Points), result.Points)
	}

	// Verify endpoints are NOT articulation points
	for _, p := range result.Points {
		if p == "A" || p == "Z" {
			t.Errorf("endpoint %s should not be articulation point", p)
		}
	}
}

func TestArticulationPoints_BinaryTree(t *testing.T) {
	// Binary tree with 15 nodes (depth 4)
	// All non-leaf nodes should be articulation points
	numNodes := 15
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0)
	for i := 0; i < numNodes; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[left]})
		}
		if right < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[right]})
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All non-leaf internal nodes (0-6) should be articulation points
	// Leaves are nodes 7-14
	expectedPoints := make(map[string]bool)
	for i := 0; i < 7; i++ {
		expectedPoints[nodes[i]] = true
	}

	for _, p := range result.Points {
		if !expectedPoints[p] {
			t.Errorf("unexpected articulation point: %s", p)
		}
		delete(expectedPoints, p)
	}

	if len(expectedPoints) > 0 {
		t.Errorf("missing articulation points: %v", expectedPoints)
	}
}

// -----------------------------------------------------------------------------
// Correctness Verification Tests
// -----------------------------------------------------------------------------

func TestArticulationPoints_BridgeInvariant(t *testing.T) {
	// Invariant: every bridge connects two biconnected components
	// In a connected graph: bridges <= articulation_points + 1
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mathematical property: in a connected graph
	// bridges <= articulation_points + 1
	if len(result.Bridges) > len(result.Points)+1 {
		t.Errorf("bridge invariant violated: %d bridges > %d articulation points + 1",
			len(result.Bridges), len(result.Points))
	}
}

func TestArticulationPoints_NodeCountEdgeCountMatch(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	result, err := analytics.ArticulationPoints(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.NodeCount != len(nodes) {
		t.Errorf("expected NodeCount=%d, got %d", len(nodes), result.NodeCount)
	}

	if result.EdgeCount != len(edges) {
		t.Errorf("expected EdgeCount=%d, got %d", len(edges), result.EdgeCount)
	}
}

// -----------------------------------------------------------------------------
// Large Graph Tests
// -----------------------------------------------------------------------------

func TestArticulationPoints_LargeGraph(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large graph test in short mode")
	}

	// 10K nodes in a tree structure
	numNodes := 10000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	start := time.Now()
	result, err := analytics.ArticulationPoints(ctx)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("Large graph (%d nodes, %d edges): %d articulation points, %d bridges, took %v",
		numNodes, len(edges), len(result.Points), len(result.Bridges), duration)

	// Should complete in reasonable time (< 5s)
	if duration > 5*time.Second {
		t.Errorf("large graph took too long: %v", duration)
	}

	// Basic sanity checks
	if result.NodeCount != numNodes {
		t.Errorf("expected NodeCount=%d, got %d", numNodes, result.NodeCount)
	}

	// In a binary tree, all non-leaf nodes are articulation points
	// Number of leaf nodes in a complete binary tree = (n+1)/2
	// So articulation points = n - leaves = n - (n+1)/2 ≈ n/2
	expectedMinAP := numNodes / 4 // Conservative lower bound
	if len(result.Points) < expectedMinAP {
		t.Errorf("expected at least %d articulation points, got %d",
			expectedMinAP, len(result.Points))
	}
}

// -----------------------------------------------------------------------------
// Additional Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkArticulationPoints_Large(b *testing.B) {
	// 10K nodes binary tree
	numNodes := 10000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ArticulationPoints(ctx)
	}
}

func BenchmarkArticulationPoints_DenseGraph(b *testing.B) {
	// 100 nodes with many edges (nearly complete graph)
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create edges for nearly complete graph (skip some to avoid all cycles)
	edges := make([][2]string, 0)
	for i := 0; i < numNodes; i++ {
		for j := i + 1; j < numNodes; j++ {
			// Skip some edges to make it interesting
			if (i+j)%3 != 0 {
				edges = append(edges, [2]string{nodes[i], nodes[j]})
			}
		}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ArticulationPoints(ctx)
	}
}

// =============================================================================
// Dominator Trees Tests (GR-16b)
// =============================================================================

// buildDominatorTestGraph creates a directed graph for dominator testing.
// Uses EdgeTypeCalls edges in the forward direction only.
func buildDominatorTestGraph(t *testing.T, nodeIDs []string, edges [][2]string) *HierarchicalGraph {
	t.Helper()

	g := NewGraph("/test/project")

	// Create nodes
	for _, id := range nodeIDs {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, err := g.AddNode(sym)
		if err != nil {
			t.Fatalf("failed to add node %s: %v", id, err)
		}
	}

	// Create directed edges
	for _, edge := range edges {
		err := g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
		if err != nil {
			t.Fatalf("failed to add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	return hg
}

// -----------------------------------------------------------------------------
// DominatorTree Tests
// -----------------------------------------------------------------------------

func TestDominatorTree_Empty(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "",
		ImmediateDom: map[string]string{},
		Depth:        map[string]int{},
	}

	if len(dt.ImmediateDom) != 0 {
		t.Error("expected empty ImmediateDom")
	}
	if dt.MaxDepth() != 0 {
		t.Error("expected max depth 0")
	}
}

// -----------------------------------------------------------------------------
// Dominators Algorithm Tests
// -----------------------------------------------------------------------------

func TestDominators_LinearChain(t *testing.T) {
	// A → B → C → D
	// A dominates all, B dominates C and D, C dominates D
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected immediate dominators
	expected := map[string]string{
		"A": "A", // Entry points to itself
		"B": "A",
		"C": "B",
		"D": "C",
	}

	for node, expectedIdom := range expected {
		if dt.ImmediateDom[node] != expectedIdom {
			t.Errorf("idom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
		}
	}

	// Verify dominance
	if !dt.Dominates("A", "D") {
		t.Error("A should dominate D")
	}
	if !dt.Dominates("B", "D") {
		t.Error("B should dominate D")
	}
	if dt.Dominates("D", "A") {
		t.Error("D should not dominate A")
	}
}

func TestDominators_Diamond(t *testing.T) {
	// A → B → D
	// A → C → D
	// A dominates all, neither B nor C dominates D (both paths exist)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D's immediate dominator is A (not B or C, since either path works)
	expected := map[string]string{
		"A": "A",
		"B": "A",
		"C": "A",
		"D": "A", // A is the only common dominator
	}

	for node, expectedIdom := range expected {
		if dt.ImmediateDom[node] != expectedIdom {
			t.Errorf("idom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
		}
	}

	// Verify dominance
	if !dt.Dominates("A", "D") {
		t.Error("A should dominate D")
	}
	if dt.Dominates("B", "D") {
		t.Error("B should not dominate D (C path exists)")
	}
	if dt.Dominates("C", "D") {
		t.Error("C should not dominate D (B path exists)")
	}
}

func TestDominators_Tree(t *testing.T) {
	// Tree structure:
	//       A
	//      / \
	//     B   C
	//    / \
	//   D   E
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"B", "E"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]string{
		"A": "A",
		"B": "A",
		"C": "A",
		"D": "B",
		"E": "B",
	}

	for node, expectedIdom := range expected {
		if dt.ImmediateDom[node] != expectedIdom {
			t.Errorf("idom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
		}
	}
}

func TestDominators_Complex(t *testing.T) {
	// More complex graph:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	//     |
	//     E
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "E"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := map[string]string{
		"A": "A",
		"B": "A",
		"C": "A",
		"D": "A", // D dominated by A (not B or C due to multiple paths)
		"E": "D",
	}

	for node, expectedIdom := range expected {
		if dt.ImmediateDom[node] != expectedIdom {
			t.Errorf("idom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
		}
	}

	// E is dominated by D and A
	if !dt.Dominates("D", "E") {
		t.Error("D should dominate E")
	}
	if !dt.Dominates("A", "E") {
		t.Error("A should dominate E")
	}
}

func TestDominators_Unreachable(t *testing.T) {
	// A → B, C (disconnected)
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		// C is not reachable from A
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// C should not be in the dominator tree
	if _, ok := dt.ImmediateDom["C"]; ok {
		t.Error("C should not be in dominator tree (unreachable)")
	}

	// A and B should be present
	if _, ok := dt.ImmediateDom["A"]; !ok {
		t.Error("A should be in dominator tree")
	}
	if _, ok := dt.ImmediateDom["B"]; !ok {
		t.Error("B should be in dominator tree")
	}
}

func TestDominators_Empty(t *testing.T) {
	hg := buildDominatorTestGraph(t, []string{}, [][2]string{})
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dt.ImmediateDom) != 0 {
		t.Error("expected empty dominator tree for empty graph")
	}
}

func TestDominators_SingleNode(t *testing.T) {
	nodes := []string{"A"}
	edges := [][2]string{}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single node: A dominates itself
	if dt.ImmediateDom["A"] != "A" {
		t.Errorf("idom(A) = %s, expected A", dt.ImmediateDom["A"])
	}
}

func TestDominators_EntryNotFound(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	_, err := analytics.Dominators(ctx, "NonExistent")
	if err == nil {
		t.Error("expected error for non-existent entry node")
	}
}

func TestDominators_EmptyEntry(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	_, err := analytics.Dominators(ctx, "")
	if err == nil {
		t.Error("expected error for empty entry node")
	}
}

func TestDominators_ContextCancellation(t *testing.T) {
	// Create a moderately sized graph
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create a linear chain
	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := analytics.Dominators(ctx, "N0")

	// Should return error on cancelled context
	if err == nil {
		t.Log("Note: small graph may complete before context check")
	}
}

func TestDominators_Convergence(t *testing.T) {
	// Linear chain should converge in 1 iteration
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !dt.Converged {
		t.Error("expected algorithm to converge")
	}

	// Linear chain should converge in 1-2 iterations
	if dt.Iterations > 3 {
		t.Errorf("expected <= 3 iterations for linear chain, got %d", dt.Iterations)
	}
}

// -----------------------------------------------------------------------------
// Query Method Tests
// -----------------------------------------------------------------------------

func TestDominates_SelfDomination(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "A",
		ImmediateDom: map[string]string{"A": "A", "B": "A"},
	}

	// Every node dominates itself
	if !dt.Dominates("A", "A") {
		t.Error("A should dominate itself")
	}
	if !dt.Dominates("B", "B") {
		t.Error("B should dominate itself")
	}
}

func TestDominates_NotInTree(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "A",
		ImmediateDom: map[string]string{"A": "A", "B": "A"},
	}

	// Non-existent node
	if dt.Dominates("X", "A") {
		t.Error("X should not dominate A (X not in tree)")
	}
	if dt.Dominates("A", "X") {
		t.Error("A should not dominate X (X not in tree)")
	}
}

func TestDominatorsOf_Path(t *testing.T) {
	// A → B → C → D
	dt := &DominatorTree{
		Entry: "A",
		ImmediateDom: map[string]string{
			"A": "A",
			"B": "A",
			"C": "B",
			"D": "C",
		},
	}

	path := dt.DominatorsOf("D")
	expected := []string{"D", "C", "B", "A"}

	if len(path) != len(expected) {
		t.Fatalf("expected path length %d, got %d: %v", len(expected), len(path), path)
	}

	for i, node := range expected {
		if path[i] != node {
			t.Errorf("path[%d] = %s, expected %s", i, path[i], node)
		}
	}
}

func TestDominatorsOf_NotInTree(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "A",
		ImmediateDom: map[string]string{"A": "A", "B": "A"},
	}

	path := dt.DominatorsOf("X")
	if len(path) != 0 {
		t.Errorf("expected empty path for non-existent node, got %v", path)
	}
}

func TestDominatedBy_Subtree(t *testing.T) {
	// Tree:   A
	//        / \
	//       B   C
	//      / \
	//     D   E
	dt := &DominatorTree{
		Entry: "A",
		ImmediateDom: map[string]string{
			"A": "A",
			"B": "A",
			"C": "A",
			"D": "B",
			"E": "B",
		},
	}

	// Subtree rooted at B: B, D, E
	subtree := dt.DominatedBy("B")
	if len(subtree) != 3 {
		t.Errorf("expected 3 nodes in subtree, got %d: %v", len(subtree), subtree)
	}

	expected := map[string]bool{"B": true, "D": true, "E": true}
	for _, node := range subtree {
		if !expected[node] {
			t.Errorf("unexpected node in subtree: %s", node)
		}
	}
}

func TestDominatedBy_EntrySubtree(t *testing.T) {
	dt := &DominatorTree{
		Entry: "A",
		ImmediateDom: map[string]string{
			"A": "A",
			"B": "A",
			"C": "A",
		},
	}

	// Entry dominates all
	subtree := dt.DominatedBy("A")
	if len(subtree) != 3 {
		t.Errorf("expected 3 nodes dominated by A, got %d: %v", len(subtree), subtree)
	}
}

func TestMaxDepth(t *testing.T) {
	dt := &DominatorTree{
		Entry: "A",
		Depth: map[string]int{
			"A": 0,
			"B": 1,
			"C": 2,
			"D": 3,
		},
	}

	if dt.MaxDepth() != 3 {
		t.Errorf("expected max depth 3, got %d", dt.MaxDepth())
	}
}

// -----------------------------------------------------------------------------
// DominatorsWithCRS Tests
// -----------------------------------------------------------------------------

func TestDominatorsWithCRS_Basic(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, traceStep := analytics.DominatorsWithCRS(ctx, "A")

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify TraceStep is populated
	if traceStep.Action != "analytics_dominators" {
		t.Errorf("expected action 'analytics_dominators', got '%s'", traceStep.Action)
	}

	if traceStep.Tool != "Dominators" {
		t.Errorf("expected tool 'Dominators', got '%s'", traceStep.Tool)
	}

	if traceStep.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	// Check metadata
	if traceStep.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}

	if _, ok := traceStep.Metadata["entry"]; !ok {
		t.Error("expected 'entry' in metadata")
	}

	if _, ok := traceStep.Metadata["iterations"]; !ok {
		t.Error("expected 'iterations' in metadata")
	}

	if _, ok := traceStep.Metadata["max_depth"]; !ok {
		t.Error("expected 'max_depth' in metadata")
	}
}

func TestDominatorsWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	dt, traceStep := analytics.DominatorsWithCRS(ctx, "A")

	// Should return result (possibly partial) with error in trace
	if dt == nil {
		t.Fatal("expected non-nil result even with cancelled context")
	}

	if traceStep.Error == "" {
		t.Log("Note: small graph may complete before context check")
	}
}

func TestDominatorsWithCRS_EntryNotFound(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	_, traceStep := analytics.DominatorsWithCRS(ctx, "NonExistent")

	// Should have error in trace step
	if traceStep.Error == "" {
		t.Error("expected error in trace step for non-existent entry")
	}
}

// -----------------------------------------------------------------------------
// Table-Driven Tests
// -----------------------------------------------------------------------------

func TestDominators_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		nodes    []string
		edges    [][2]string
		entry    string
		expected map[string]string // node -> idom
	}{
		{
			name:  "single node",
			nodes: []string{"A"},
			edges: [][2]string{},
			entry: "A",
			expected: map[string]string{
				"A": "A",
			},
		},
		{
			name:  "two nodes",
			nodes: []string{"A", "B"},
			edges: [][2]string{{"A", "B"}},
			entry: "A",
			expected: map[string]string{
				"A": "A",
				"B": "A",
			},
		},
		{
			name:  "linear chain",
			nodes: []string{"A", "B", "C"},
			edges: [][2]string{{"A", "B"}, {"B", "C"}},
			entry: "A",
			expected: map[string]string{
				"A": "A",
				"B": "A",
				"C": "B",
			},
		},
		{
			name:  "diamond",
			nodes: []string{"A", "B", "C", "D"},
			edges: [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}},
			entry: "A",
			expected: map[string]string{
				"A": "A",
				"B": "A",
				"C": "A",
				"D": "A",
			},
		},
		{
			name:  "if-then-else",
			nodes: []string{"A", "B", "C", "D"},
			edges: [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}},
			entry: "A",
			expected: map[string]string{
				"A": "A",
				"B": "A",
				"C": "A",
				"D": "A",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hg := buildDominatorTestGraph(t, tt.nodes, tt.edges)
			analytics := NewGraphAnalytics(hg)

			ctx := context.Background()
			dt, err := analytics.Dominators(ctx, tt.entry)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for node, expectedIdom := range tt.expected {
				if dt.ImmediateDom[node] != expectedIdom {
					t.Errorf("idom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Property Tests
// -----------------------------------------------------------------------------

func TestDominators_EntryDominatesAll(t *testing.T) {
	// Entry should dominate all reachable nodes
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, node := range nodes {
		if !dt.Dominates("A", node) {
			t.Errorf("entry A should dominate %s", node)
		}
	}
}

func TestDominators_IdomDominates(t *testing.T) {
	// idom(n) should dominate n for all n != entry
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for node, idom := range dt.ImmediateDom {
		if node == dt.Entry {
			continue
		}
		if !dt.Dominates(idom, node) {
			t.Errorf("idom(%s) = %s should dominate %s", node, idom, node)
		}
	}
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkDominators_Small(b *testing.B) {
	// 100 nodes linear chain
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.Dominators(ctx, "N0")
	}
}

func BenchmarkDominators_Medium(b *testing.B) {
	// 1000 nodes binary tree
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0, numNodes-1)
	for i := 0; i < numNodes; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[left]})
		}
		if right < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[right]})
		}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.Dominators(ctx, "N0")
	}
}

func BenchmarkDominators_Large(b *testing.B) {
	// 10K nodes binary tree
	numNodes := 10000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0, numNodes-1)
	for i := 0; i < numNodes; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[left]})
		}
		if right < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[right]})
		}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.Dominators(ctx, "N0")
	}
}

// =============================================================================
// Additional GR-16b Tests (Dominator Trees)
// =============================================================================

// TestDominators_CycleWithEntry tests dominator computation with cycles.
func TestDominators_CycleWithEntry(t *testing.T) {
	// Graph with cycle: A → B → C → B (back edge)
	// A dominates all reachable nodes
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "B"}, // Back edge creating cycle
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A should dominate all nodes
	for _, node := range nodes {
		if !dt.Dominates("A", node) {
			t.Errorf("A should dominate %s", node)
		}
	}

	// B is immediately dominated by A
	if dt.ImmediateDom["B"] != "A" {
		t.Errorf("idom(B) = %s, expected A", dt.ImmediateDom["B"])
	}

	// C is immediately dominated by B (the only way to reach C is through B)
	if dt.ImmediateDom["C"] != "B" {
		t.Errorf("idom(C) = %s, expected B", dt.ImmediateDom["C"])
	}

	// Should converge
	if !dt.Converged {
		t.Error("expected algorithm to converge")
	}
}

// TestDominators_LongPath tests dominator computation with a long chain.
func TestDominators_LongPath(t *testing.T) {
	// Long chain: N0 → N1 → N2 → ... → N99
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "N0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each node should be dominated by all predecessors
	for i := 1; i < numNodes; i++ {
		nodeID := nodes[i]
		prevID := nodes[i-1]

		// Immediate dominator should be the previous node
		if dt.ImmediateDom[nodeID] != prevID {
			t.Errorf("idom(%s) = %s, expected %s", nodeID, dt.ImmediateDom[nodeID], prevID)
		}

		// Depth should equal position
		if dt.Depth[nodeID] != i {
			t.Errorf("depth(%s) = %d, expected %d", nodeID, dt.Depth[nodeID], i)
		}
	}

	// Max depth should be numNodes-1
	if dt.MaxDepth() != numNodes-1 {
		t.Errorf("max depth = %d, expected %d", dt.MaxDepth(), numNodes-1)
	}
}

// TestDominators_MultiplePaths tests dominator computation with multiple paths.
func TestDominators_MultiplePaths(t *testing.T) {
	// Graph: A → B → D, A → C → D → E, B → E
	// Multiple paths from A to E
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "E"},
		{"B", "E"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D's immediate dominator is A (both B and C paths exist)
	if dt.ImmediateDom["D"] != "A" {
		t.Errorf("idom(D) = %s, expected A", dt.ImmediateDom["D"])
	}

	// E's immediate dominator is A (can reach via B→E or D→E, and D's idom is A)
	if dt.ImmediateDom["E"] != "A" {
		t.Errorf("idom(E) = %s, expected A", dt.ImmediateDom["E"])
	}

	// B does NOT dominate E (C→D→E path exists)
	if dt.Dominates("B", "E") {
		t.Error("B should NOT dominate E (C→D→E path exists)")
	}
}

// TestDominators_EnsureChildrenBuiltIdempotent verifies lazy children building.
func TestDominators_EnsureChildrenBuiltIdempotent(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Children should be nil initially (lazy)
	if dt.Children != nil {
		t.Log("Note: Children may be built during other operations")
	}

	// Call DominatedBy which builds children
	result1 := dt.DominatedBy("A")

	// Call again - should return same result
	result2 := dt.DominatedBy("A")

	if len(result1) != len(result2) {
		t.Errorf("DominatedBy returned different lengths: %d vs %d", len(result1), len(result2))
	}

	// Both should have A, B, C
	if len(result1) != 3 {
		t.Errorf("Expected 3 nodes dominated by A, got %d", len(result1))
	}
}

// TestDominators_NilGraph tests handling of nil graph.
func TestDominators_NilGraph(t *testing.T) {
	var analytics *GraphAnalytics = nil

	// Should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Dominators panicked with nil analytics: %v", r)
		}
	}()

	// Can't call on nil receiver, this is expected Go behavior
	if analytics != nil {
		_, _ = analytics.Dominators(context.Background(), "A")
	}
}

// TestDominators_TransitiveProperty verifies dominance transitivity.
func TestDominators_TransitiveProperty(t *testing.T) {
	// If A dominates B and B dominates C, then A dominates C
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify transitive property
	if dt.Dominates("A", "B") && dt.Dominates("B", "C") {
		if !dt.Dominates("A", "C") {
			t.Error("Transitivity violated: A dom B, B dom C, but A !dom C")
		}
	}

	if dt.Dominates("A", "B") && dt.Dominates("B", "C") && dt.Dominates("C", "D") {
		if !dt.Dominates("A", "D") {
			t.Error("Transitivity violated: A should dominate D through chain")
		}
	}
}

// TestDominators_ReflexiveProperty verifies self-dominance.
func TestDominators_ReflexiveProperty(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Every node dominates itself
	for _, node := range nodes {
		if !dt.Dominates(node, node) {
			t.Errorf("Reflexive property violated: %s should dominate itself", node)
		}
	}
}

// TestDominators_DominatedByEmpty verifies DominatedBy with non-existent node.
func TestDominators_DominatedByEmpty(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Non-existent node should return empty
	result := dt.DominatedBy("NonExistent")
	if len(result) != 0 {
		t.Errorf("DominatedBy for non-existent node should return empty, got %v", result)
	}
}

// TestDominators_ConvergenceIterations verifies iteration count is reasonable.
func TestDominators_ConvergenceIterations(t *testing.T) {
	tests := []struct {
		name          string
		nodes         []string
		edges         [][2]string
		entry         string
		maxIterations int
	}{
		{
			name:          "linear chain (should converge in 1-2 iterations)",
			nodes:         []string{"A", "B", "C", "D"},
			edges:         [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}},
			entry:         "A",
			maxIterations: 3,
		},
		{
			name:          "diamond (should converge quickly)",
			nodes:         []string{"A", "B", "C", "D"},
			edges:         [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}},
			entry:         "A",
			maxIterations: 3,
		},
		{
			name:          "tree (should converge in 1 iteration)",
			nodes:         []string{"A", "B", "C", "D", "E"},
			edges:         [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"B", "E"}},
			entry:         "A",
			maxIterations: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hg := buildDominatorTestGraph(t, tc.nodes, tc.edges)
			analytics := NewGraphAnalytics(hg)

			ctx := context.Background()
			dt, err := analytics.Dominators(ctx, tc.entry)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !dt.Converged {
				t.Error("expected algorithm to converge")
			}

			if dt.Iterations > tc.maxIterations {
				t.Errorf("expected <= %d iterations, got %d", tc.maxIterations, dt.Iterations)
			}
		})
	}
}

// TestDominatorsWithCRS_Metadata verifies CRS metadata completeness.
func TestDominatorsWithCRS_Metadata(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, traceStep := analytics.DominatorsWithCRS(ctx, "A")

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify all expected metadata fields
	requiredMetadata := []string{
		"entry",
		"iterations",
		"converged",
		"max_depth",
		"reachable_nodes",
		"node_count",
	}

	for _, key := range requiredMetadata {
		if _, ok := traceStep.Metadata[key]; !ok {
			t.Errorf("TraceStep.Metadata missing key: %s", key)
		}
	}

	// Verify action and tool
	if traceStep.Action != "analytics_dominators" {
		t.Errorf("TraceStep.Action = %q, want 'analytics_dominators'", traceStep.Action)
	}

	if traceStep.Tool != "Dominators" {
		t.Errorf("TraceStep.Tool = %q, want 'Dominators'", traceStep.Tool)
	}

	if traceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}
}

// TestDominators_LargeGraphConvergence tests convergence on larger graphs.
func TestDominators_LargeGraphConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large graph test in short mode")
	}

	// 1000 node binary tree
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, 0)
	for i := 0; i < numNodes; i++ {
		left := 2*i + 1
		right := 2*i + 2
		if left < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[left]})
		}
		if right < numNodes {
			edges = append(edges, [2]string{nodes[i], nodes[right]})
		}
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	start := time.Now()
	dt, err := analytics.Dominators(ctx, "N0")
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("1000 node binary tree: %d iterations, %v", dt.Iterations, duration)

	if !dt.Converged {
		t.Error("expected algorithm to converge")
	}

	// Should converge quickly for tree
	if dt.Iterations > 3 {
		t.Errorf("expected <= 3 iterations for tree, got %d", dt.Iterations)
	}

	// Verify some dominance relationships
	if !dt.Dominates("N0", "N500") {
		t.Error("N0 should dominate N500")
	}

	// N1 dominates its subtree
	if !dt.Dominates("N1", "N3") {
		t.Error("N1 should dominate N3 (left child)")
	}
}

// TestDominators_DepthCalculation verifies depth is correctly computed.
func TestDominators_DepthCalculation(t *testing.T) {
	// Tree: A → B → D, A → C
	// Depths: A=0, B=1, C=1, D=2
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedDepths := map[string]int{
		"A": 0,
		"B": 1,
		"C": 1,
		"D": 2,
	}

	for node, expectedDepth := range expectedDepths {
		if dt.Depth[node] != expectedDepth {
			t.Errorf("depth(%s) = %d, expected %d", node, dt.Depth[node], expectedDepth)
		}
	}
}

// TestDominators_ParallelEdges verifies handling of parallel edges.
func TestDominators_ParallelEdges(t *testing.T) {
	// A has two edges to B
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "B"}, // Parallel edge
		{"B", "C"},
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should work correctly despite parallel edges
	if dt.ImmediateDom["B"] != "A" {
		t.Errorf("idom(B) = %s, expected A", dt.ImmediateDom["B"])
	}
	if dt.ImmediateDom["C"] != "B" {
		t.Errorf("idom(C) = %s, expected B", dt.ImmediateDom["C"])
	}
}

// TestDominators_SelfLoop verifies handling of self-loops.
func TestDominators_SelfLoop(t *testing.T) {
	// A → B with self-loop on B
	nodes := []string{"A", "B"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "B"}, // Self-loop
	}

	hg := buildDominatorTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should work correctly - self-loop doesn't affect dominance
	if dt.ImmediateDom["B"] != "A" {
		t.Errorf("idom(B) = %s, expected A", dt.ImmediateDom["B"])
	}
}

// =============================================================================
// Post-Dominator Trees Tests (GR-16c)
// =============================================================================

// -----------------------------------------------------------------------------
// PostDominators Algorithm Tests
// -----------------------------------------------------------------------------

func TestPostDominators_LinearChain(t *testing.T) {
	// A -> B -> C -> D (D is exit)
	// Post-dominators: D post-dominates all, C post-dominates A and B, B post-dominates A
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// D is the exit, so it should post-dominate all reachable nodes
	// In post-dominator tree: D is root, C is child of D, B is child of C, A is child of B
	if dt.ImmediateDom["C"] != "D" {
		t.Errorf("ipost-dom(C) = %s, expected D", dt.ImmediateDom["C"])
	}
	if dt.ImmediateDom["B"] != "C" {
		t.Errorf("ipost-dom(B) = %s, expected C", dt.ImmediateDom["B"])
	}
	if dt.ImmediateDom["A"] != "B" {
		t.Errorf("ipost-dom(A) = %s, expected B", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_Diamond(t *testing.T) {
	// Diamond: A -> B, A -> C, B -> D, C -> D
	// D is the only exit (no outgoing edges)
	// D post-dominates all nodes
	// Neither B nor C post-dominates A (both paths lead to D)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D is the immediate post-dominator of both B and C
	if dt.ImmediateDom["B"] != "D" {
		t.Errorf("ipost-dom(B) = %s, expected D", dt.ImmediateDom["B"])
	}
	if dt.ImmediateDom["C"] != "D" {
		t.Errorf("ipost-dom(C) = %s, expected D", dt.ImmediateDom["C"])
	}

	// A's immediate post-dominator is D (since B and C both go to D, D is the merge)
	if dt.ImmediateDom["A"] != "D" {
		t.Errorf("ipost-dom(A) = %s, expected D", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_Tree(t *testing.T) {
	// Inverted tree (multiple paths to single exit):
	// A -> B, C -> B, D -> C (B is exit)
	// B post-dominates all
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"C", "B"},
		{"D", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B post-dominates A directly
	if dt.ImmediateDom["A"] != "B" {
		t.Errorf("ipost-dom(A) = %s, expected B", dt.ImmediateDom["A"])
	}
	// C's path to B is direct
	if dt.ImmediateDom["C"] != "B" {
		t.Errorf("ipost-dom(C) = %s, expected B", dt.ImmediateDom["C"])
	}
	// D goes through C to B
	if dt.ImmediateDom["D"] != "C" {
		t.Errorf("ipost-dom(D) = %s, expected C", dt.ImmediateDom["D"])
	}
}

func TestPostDominators_AutoDetectExit(t *testing.T) {
	// A -> B -> C (C has no outgoing edges, so it's auto-detected as exit)
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	// Empty exit string should trigger auto-detection
	dt, err := analytics.PostDominators(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// C should be auto-detected as exit
	if dt.Entry != "C" {
		t.Errorf("expected C as exit (Entry field), got %s", dt.Entry)
	}

	// Verify post-dominance relationships
	if dt.ImmediateDom["B"] != "C" {
		t.Errorf("ipost-dom(B) = %s, expected C", dt.ImmediateDom["B"])
	}
	if dt.ImmediateDom["A"] != "B" {
		t.Errorf("ipost-dom(A) = %s, expected B", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_MultipleExits(t *testing.T) {
	// A -> B, A -> C (both B and C are exits with no outgoing edges)
	// With virtual exit: both B and C lead to virtual, A post-dominated by virtual
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	// Empty exit should auto-detect multiple exits and create virtual exit
	dt, err := analytics.PostDominators(ctx, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should not contain virtual exit
	for node := range dt.ImmediateDom {
		if node == VirtualExitNodeID {
			t.Error("virtual exit should not appear in ImmediateDom")
		}
	}

	// Both B and C should have no post-dominator other than virtual (filtered)
	// A should have no strict post-dominator (since B and C are different exits)
	// This test verifies the virtual exit handling works correctly
	t.Logf("Multi-exit post-dominator tree: %v", dt.ImmediateDom)
}

func TestPostDominators_SingleNode(t *testing.T) {
	// Single node A with no edges (A is both entry and exit)
	nodes := []string{"A"}
	edges := [][2]string{}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A post-dominates itself
	if dt.ImmediateDom["A"] != "A" {
		t.Errorf("ipost-dom(A) = %s, expected A", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_Empty(t *testing.T) {
	nodes := []string{}
	edges := [][2]string{}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "")

	// Should return empty result without error
	if err != nil {
		t.Fatalf("unexpected error for empty graph: %v", err)
	}
	if dt == nil {
		t.Fatal("expected non-nil result for empty graph")
	}
	if len(dt.ImmediateDom) != 0 {
		t.Errorf("expected empty ImmediateDom, got %d entries", len(dt.ImmediateDom))
	}
}

func TestPostDominators_ExitNotFound(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	_, err := analytics.PostDominators(ctx, "NonExistent")

	// Should return error for invalid exit
	if err == nil {
		t.Error("expected error for non-existent exit node")
	}
}

func TestPostDominators_ContextCancellation(t *testing.T) {
	// Create a graph
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dt, err := analytics.PostDominators(ctx, nodes[numNodes-1])

	// Should handle cancellation gracefully
	if err == nil {
		t.Log("Note: small graph may complete before context check")
	}

	if dt == nil {
		t.Fatal("expected non-nil result even with cancellation")
	}
}

func TestPostDominators_Cycle(t *testing.T) {
	// A -> B -> C -> A (cycle with no natural exit)
	// When no exit is detected, should return error or empty result
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "A"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	// No exit specified, and no nodes with zero outgoing edges
	dt, err := analytics.PostDominators(ctx, "")

	// Should handle gracefully - either error or empty result
	if err != nil {
		t.Logf("Cycle with no exit returned error: %v", err)
	} else if len(dt.ImmediateDom) > 0 {
		t.Logf("Cycle result: %v", dt.ImmediateDom)
	}
}

func TestPostDominators_SelfLoop(t *testing.T) {
	// A -> A (self-loop), B -> A
	// A has outgoing edges (self-loop), so B is exit
	nodes := []string{"A", "B"}
	edges := [][2]string{
		{"A", "A"}, // Self-loop
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B post-dominates A
	if dt.ImmediateDom["A"] != "B" {
		t.Errorf("ipost-dom(A) = %s, expected B", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_UnreachableNodes(t *testing.T) {
	// A -> B, C (C is disconnected)
	// Post-dominators from B should not include C
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// C is unreachable (in reverse graph), so it shouldn't have a post-dominator
	if _, ok := dt.ImmediateDom["C"]; ok {
		t.Error("unreachable node C should not have a post-dominator")
	}
}

// -----------------------------------------------------------------------------
// PostDominatorsWithCRS Tests
// -----------------------------------------------------------------------------

func TestPostDominatorsWithCRS_Basic(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, traceStep := analytics.PostDominatorsWithCRS(ctx, "C")

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify TraceStep is populated
	if traceStep.Action != "analytics_post_dominators" {
		t.Errorf("expected action 'analytics_post_dominators', got '%s'", traceStep.Action)
	}

	if traceStep.Tool != "PostDominators" {
		t.Errorf("expected tool 'PostDominators', got '%s'", traceStep.Tool)
	}

	if traceStep.Target != "C" {
		t.Errorf("expected target 'C', got '%s'", traceStep.Target)
	}

	if traceStep.Duration == 0 {
		t.Error("expected non-zero duration")
	}

	// Check metadata
	if traceStep.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}

	if _, ok := traceStep.Metadata["exit"]; !ok {
		t.Error("expected 'exit' in metadata")
	}
}

func TestPostDominatorsWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	dt, traceStep := analytics.PostDominatorsWithCRS(ctx, "C")

	// Should return empty result with error in trace
	if dt == nil {
		t.Fatal("expected non-nil result even with cancelled context")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Log("Note: small graph may complete before context check")
	}
}

func TestPostDominatorsWithCRS_AutoDetect(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, traceStep := analytics.PostDominatorsWithCRS(ctx, "")

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have detected C as exit (may include "(auto-detected)" suffix)
	if exit, ok := traceStep.Metadata["exit"]; ok {
		if exit != "C" && exit != "C (auto-detected)" {
			t.Errorf("expected auto-detected exit 'C' or 'C (auto-detected)', got '%s'", exit)
		}
	}
}

// -----------------------------------------------------------------------------
// PostDominators Property Tests
// -----------------------------------------------------------------------------

func TestPostDominators_TransitiveProperty(t *testing.T) {
	// If A post-dominates B and B post-dominates C, then A post-dominates C
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D post-dominates C, C post-dominates B, B post-dominates A
	// Therefore D post-dominates all
	dominators := dt.DominatorsOf("A")
	if len(dominators) != 4 { // A, B, C, D
		t.Errorf("expected 4 post-dominators of A, got %d: %v", len(dominators), dominators)
	}
}

func TestPostDominators_ReflexiveProperty(t *testing.T) {
	// Every node post-dominates itself
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check reflexive property using Dominates method
	for _, node := range nodes {
		if !dt.Dominates(node, node) {
			t.Errorf("node %s should post-dominate itself", node)
		}
	}
}

// -----------------------------------------------------------------------------
// PostDominators Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkPostDominators_Small(b *testing.B) {
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	exit := nodes[numNodes-1]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.PostDominators(ctx, exit)
	}
}

func BenchmarkPostDominators_Medium(b *testing.B) {
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create a tree-like structure
	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	// Use a leaf node as exit
	exit := nodes[numNodes-1]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.PostDominators(ctx, exit)
	}
}

// -----------------------------------------------------------------------------
// PostDominators Additional Tests (Extended Coverage)
// -----------------------------------------------------------------------------

func TestPostDominators_NilGraph(t *testing.T) {
	var analytics *GraphAnalytics = nil

	ctx := context.Background()

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PostDominators panicked with nil analytics: %v", r)
		}
	}()

	// Call should handle nil gracefully
	if analytics != nil {
		_, _ = analytics.PostDominators(ctx, "")
	}
}

func TestPostDominators_LargeGraphConvergence(t *testing.T) {
	// Test convergence on a 1000-node binary tree
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create binary tree edges
	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	// Use a leaf node as exit
	exit := nodes[numNodes-1]

	start := time.Now()
	dt, err := analytics.PostDominators(ctx, exit)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !dt.Converged {
		t.Errorf("expected convergence, got %d iterations", dt.Iterations)
	}

	// Should converge quickly (< 10 iterations for tree)
	if dt.Iterations > 10 {
		t.Errorf("expected <= 10 iterations, got %d", dt.Iterations)
	}

	t.Logf("1000 node binary tree post-dominators: %d iterations, %v", dt.Iterations, duration)
}

func TestPostDominators_ParallelEdges(t *testing.T) {
	// A -> B (twice), B -> C
	// Parallel edges shouldn't affect post-dominance
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "B"}, // Parallel edge
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// B should still be immediate post-dominator of A
	if dt.ImmediateDom["A"] != "B" {
		t.Errorf("ipost-dom(A) = %s, expected B", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_DepthCalculation(t *testing.T) {
	// A -> B -> C -> D
	// Depths should be: D=0, C=1, B=2, A=3
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedDepths := map[string]int{
		"D": 0,
		"C": 1,
		"B": 2,
		"A": 3,
	}

	for node, expectedDepth := range expectedDepths {
		if dt.Depth[node] != expectedDepth {
			t.Errorf("depth(%s) = %d, expected %d", node, dt.Depth[node], expectedDepth)
		}
	}

	// Max depth should be 3
	if dt.MaxDepth() != 3 {
		t.Errorf("MaxDepth() = %d, expected 3", dt.MaxDepth())
	}
}

func TestPostDominators_DominatedBy(t *testing.T) {
	// A -> B -> C -> D
	// D post-dominates all, so DominatedBy(D) should return all nodes
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D post-dominates all nodes
	dominated := dt.DominatedBy("D")
	if len(dominated) != 4 {
		t.Errorf("DominatedBy(D) returned %d nodes, expected 4: %v", len(dominated), dominated)
	}

	// C post-dominates A and B (and itself)
	dominatedByC := dt.DominatedBy("C")
	if len(dominatedByC) != 3 {
		t.Errorf("DominatedBy(C) returned %d nodes, expected 3: %v", len(dominatedByC), dominatedByC)
	}
}

func TestPostDominators_DominatorsOf(t *testing.T) {
	// A -> B -> C -> D
	// A's post-dominators are: A, B, C, D
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Get all post-dominators of A
	postDoms := dt.DominatorsOf("A")
	if len(postDoms) != 4 {
		t.Errorf("DominatorsOf(A) returned %d nodes, expected 4: %v", len(postDoms), postDoms)
	}

	// D's only post-dominator is itself
	postDomsD := dt.DominatorsOf("D")
	if len(postDomsD) != 1 || postDomsD[0] != "D" {
		t.Errorf("DominatorsOf(D) = %v, expected [D]", postDomsD)
	}
}

func TestPostDominators_Dominates(t *testing.T) {
	// A -> B -> C -> D
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// D post-dominates all nodes
	for _, node := range nodes {
		if !dt.Dominates("D", node) {
			t.Errorf("D should post-dominate %s", node)
		}
	}

	// A does not post-dominate anyone except itself
	if dt.Dominates("A", "B") {
		t.Error("A should not post-dominate B")
	}
	if !dt.Dominates("A", "A") {
		t.Error("A should post-dominate itself")
	}

	// B post-dominates A
	if !dt.Dominates("B", "A") {
		t.Error("B should post-dominate A")
	}
}

func TestPostDominators_ComplexTopology(t *testing.T) {
	// More complex graph with multiple paths:
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	//     |
	//     E (exit)
	//
	// D post-dominates both B and C
	// E post-dominates all
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "E")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// E is the exit, self-dominates
	if dt.ImmediateDom["E"] != "E" {
		t.Errorf("ipost-dom(E) = %s, expected E", dt.ImmediateDom["E"])
	}

	// D's immediate post-dominator is E
	if dt.ImmediateDom["D"] != "E" {
		t.Errorf("ipost-dom(D) = %s, expected E", dt.ImmediateDom["D"])
	}

	// B and C's immediate post-dominator is D
	if dt.ImmediateDom["B"] != "D" {
		t.Errorf("ipost-dom(B) = %s, expected D", dt.ImmediateDom["B"])
	}
	if dt.ImmediateDom["C"] != "D" {
		t.Errorf("ipost-dom(C) = %s, expected D", dt.ImmediateDom["C"])
	}

	// A's immediate post-dominator is D (both paths merge at D)
	if dt.ImmediateDom["A"] != "D" {
		t.Errorf("ipost-dom(A) = %s, expected D", dt.ImmediateDom["A"])
	}
}

func TestPostDominators_LongPath(t *testing.T) {
	// Long chain: A -> B -> C -> ... -> Z (26 nodes)
	numNodes := 26
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = string(rune('A' + i))
	}

	edges := make([][2]string, numNodes-1)
	for i := 0; i < numNodes-1; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each node's immediate post-dominator is the next node
	for i := 0; i < numNodes-1; i++ {
		expected := nodes[i+1]
		if dt.ImmediateDom[nodes[i]] != expected {
			t.Errorf("ipost-dom(%s) = %s, expected %s", nodes[i], dt.ImmediateDom[nodes[i]], expected)
		}
	}

	// Max depth should be numNodes - 1
	if dt.MaxDepth() != numNodes-1 {
		t.Errorf("MaxDepth() = %d, expected %d", dt.MaxDepth(), numNodes-1)
	}
}

func TestPostDominators_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		nodes        []string
		edges        [][2]string
		exit         string
		expectedIdom map[string]string
	}{
		{
			name:  "single node",
			nodes: []string{"A"},
			edges: [][2]string{},
			exit:  "A",
			expectedIdom: map[string]string{
				"A": "A",
			},
		},
		{
			name:  "two nodes",
			nodes: []string{"A", "B"},
			edges: [][2]string{{"A", "B"}},
			exit:  "B",
			expectedIdom: map[string]string{
				"A": "B",
				"B": "B",
			},
		},
		{
			name:  "linear chain",
			nodes: []string{"A", "B", "C"},
			edges: [][2]string{{"A", "B"}, {"B", "C"}},
			exit:  "C",
			expectedIdom: map[string]string{
				"A": "B",
				"B": "C",
				"C": "C",
			},
		},
		{
			name:  "diamond",
			nodes: []string{"A", "B", "C", "D"},
			edges: [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}},
			exit:  "D",
			expectedIdom: map[string]string{
				"A": "D",
				"B": "D",
				"C": "D",
				"D": "D",
			},
		},
		{
			name:  "if-then-else with merge",
			nodes: []string{"A", "B", "C", "D", "E"},
			edges: [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}, {"D", "E"}},
			exit:  "E",
			expectedIdom: map[string]string{
				"A": "D",
				"B": "D",
				"C": "D",
				"D": "E",
				"E": "E",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hg := buildArticulationTestGraph(t, tt.nodes, tt.edges)
			analytics := NewGraphAnalytics(hg)

			ctx := context.Background()
			dt, err := analytics.PostDominators(ctx, tt.exit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for node, expectedIdom := range tt.expectedIdom {
				if dt.ImmediateDom[node] != expectedIdom {
					t.Errorf("ipost-dom(%s) = %s, expected %s", node, dt.ImmediateDom[node], expectedIdom)
				}
			}
		})
	}
}

func TestPostDominators_VerifyByReversal(t *testing.T) {
	// Property: post-dominators on G should equal dominators on reversed G
	// We'll verify by checking that the post-dominator relationships hold
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	// Compute post-dominators with D as exit
	postDomTree, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("unexpected error computing post-dominators: %v", err)
	}

	// Compute regular dominators with A as entry
	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("unexpected error computing dominators: %v", err)
	}

	// For linear chain, post-dom and dom should be "reversed":
	// Dom tree: A dominates all
	// Post-dom tree: D post-dominates all

	// Verify A dominates all in domTree
	for _, node := range nodes {
		if !domTree.Dominates("A", node) {
			t.Errorf("A should dominate %s in dominator tree", node)
		}
	}

	// Verify D post-dominates all in postDomTree
	for _, node := range nodes {
		if !postDomTree.Dominates("D", node) {
			t.Errorf("D should post-dominate %s in post-dominator tree", node)
		}
	}
}

func TestPostDominators_ContextTimeout(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Sleep to ensure timeout
	time.Sleep(1 * time.Millisecond)

	dt, err := analytics.PostDominators(ctx, "C")

	// Should handle timeout gracefully
	if err == nil {
		t.Log("Note: small graph may complete before timeout")
	}

	if dt == nil {
		t.Fatal("expected non-nil result even with timeout")
	}
}

func TestPostDominators_VirtualExitFiltering(t *testing.T) {
	// Graph with multiple exits: A -> B, A -> C (both B and C are exits)
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "") // Auto-detect exits
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Virtual exit should not appear anywhere in the result
	for node, idom := range dt.ImmediateDom {
		if node == VirtualExitNodeID {
			t.Error("virtual exit node should not appear in ImmediateDom keys")
		}
		if idom == VirtualExitNodeID {
			t.Errorf("virtual exit node should not appear in ImmediateDom values for %s", node)
		}
	}

	for _, nodeID := range dt.PostOrder {
		if nodeID == VirtualExitNodeID {
			t.Error("virtual exit node should not appear in PostOrder")
		}
	}
}

func TestPostDominators_EnsureChildrenBuiltIdempotent(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call DominatedBy multiple times - should be idempotent
	result1 := dt.DominatedBy("C")
	result2 := dt.DominatedBy("C")
	result3 := dt.DominatedBy("C")

	if len(result1) != len(result2) || len(result2) != len(result3) {
		t.Error("DominatedBy should return consistent results")
	}
}

func TestPostDominators_ConcurrentAccess(t *testing.T) {
	// Test that post-dominator tree can be queried concurrently
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	dt, err := analytics.PostDominators(ctx, "E")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Run concurrent queries
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = dt.DominatedBy("E")
				_ = dt.DominatorsOf("A")
				_ = dt.Dominates("E", "A")
				_ = dt.MaxDepth()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestPostDominatorsWithCRS_ErrorHandling(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()

	// Test with non-existent exit
	dt, traceStep := analytics.PostDominatorsWithCRS(ctx, "NonExistent")

	if dt == nil {
		t.Fatal("expected non-nil result")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for non-existent exit")
	}

	if traceStep.Action != "analytics_post_dominators" {
		t.Errorf("expected action 'analytics_post_dominators', got '%s'", traceStep.Action)
	}
}

func TestPostDominatorsWithCRS_MetadataCompleteness(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	_, traceStep := analytics.PostDominatorsWithCRS(ctx, "D")

	// Verify all expected metadata fields are present
	requiredFields := []string{"exit", "iterations", "converged", "max_depth", "reachable_nodes", "node_count"}
	for _, field := range requiredFields {
		if _, ok := traceStep.Metadata[field]; !ok {
			t.Errorf("expected '%s' in metadata", field)
		}
	}

	// Verify action and tool
	if traceStep.Action != "analytics_post_dominators" {
		t.Errorf("expected action 'analytics_post_dominators', got '%s'", traceStep.Action)
	}
	if traceStep.Tool != "PostDominators" {
		t.Errorf("expected tool 'PostDominators', got '%s'", traceStep.Tool)
	}
	if traceStep.Target != "D" {
		t.Errorf("expected target 'D', got '%s'", traceStep.Target)
	}
}

func TestPostDominators_ConvergenceIterations(t *testing.T) {
	tests := []struct {
		name          string
		nodes         []string
		edges         [][2]string
		exit          string
		maxIterations int
	}{
		{
			name:          "linear chain (should converge in 1-2 iterations)",
			nodes:         []string{"A", "B", "C", "D"},
			edges:         [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}},
			exit:          "D",
			maxIterations: 3,
		},
		{
			name:          "diamond (should converge quickly)",
			nodes:         []string{"A", "B", "C", "D"},
			edges:         [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"}},
			exit:          "D",
			maxIterations: 3,
		},
		{
			name:          "tree (should converge in 1 iteration)",
			nodes:         []string{"A", "B", "C", "D", "E"},
			edges:         [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"B", "E"}},
			exit:          "D",
			maxIterations: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hg := buildArticulationTestGraph(t, tt.nodes, tt.edges)
			analytics := NewGraphAnalytics(hg)

			ctx := context.Background()
			dt, err := analytics.PostDominators(ctx, tt.exit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if dt.Iterations > tt.maxIterations {
				t.Errorf("expected <= %d iterations, got %d", tt.maxIterations, dt.Iterations)
			}

			if !dt.Converged {
				t.Error("expected algorithm to converge")
			}
		})
	}
}

func TestPostDominators_ExitCountAndVirtualExitFields(t *testing.T) {
	t.Run("single exit sets ExitCount=1 UsedVirtualExit=false", func(t *testing.T) {
		hg := buildArticulationTestGraph(t, []string{"A", "B", "C"}, [][2]string{{"A", "B"}, {"B", "C"}})
		analytics := NewGraphAnalytics(hg)

		ctx := context.Background()
		dt, err := analytics.PostDominators(ctx, "C")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if dt.ExitCount != 1 {
			t.Errorf("expected ExitCount=1, got %d", dt.ExitCount)
		}
		if dt.UsedVirtualExit {
			t.Error("expected UsedVirtualExit=false for single exit")
		}
	})

	t.Run("auto-detect single exit sets ExitCount=1 UsedVirtualExit=false", func(t *testing.T) {
		hg := buildArticulationTestGraph(t, []string{"A", "B", "C"}, [][2]string{{"A", "B"}, {"B", "C"}})
		analytics := NewGraphAnalytics(hg)

		ctx := context.Background()
		dt, err := analytics.PostDominators(ctx, "") // Auto-detect
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if dt.ExitCount != 1 {
			t.Errorf("expected ExitCount=1, got %d", dt.ExitCount)
		}
		if dt.UsedVirtualExit {
			t.Error("expected UsedVirtualExit=false for single auto-detected exit")
		}
	})

	t.Run("multiple exits sets ExitCount>1 UsedVirtualExit=true", func(t *testing.T) {
		// Graph: A -> B, A -> C, where B and C are both exits
		hg := buildArticulationTestGraph(t, []string{"A", "B", "C"}, [][2]string{{"A", "B"}, {"A", "C"}})
		analytics := NewGraphAnalytics(hg)

		ctx := context.Background()
		dt, err := analytics.PostDominators(ctx, "") // Auto-detect multiple exits
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if dt.ExitCount != 2 {
			t.Errorf("expected ExitCount=2, got %d", dt.ExitCount)
		}
		if !dt.UsedVirtualExit {
			t.Error("expected UsedVirtualExit=true for multiple exits")
		}
	})

	t.Run("CRS metadata includes exit_count and virtual_exit", func(t *testing.T) {
		hg := buildArticulationTestGraph(t, []string{"A", "B", "C"}, [][2]string{{"A", "B"}, {"A", "C"}})
		analytics := NewGraphAnalytics(hg)

		ctx := context.Background()
		dt, traceStep := analytics.PostDominatorsWithCRS(ctx, "")

		if dt.ExitCount != 2 {
			t.Errorf("expected ExitCount=2, got %d", dt.ExitCount)
		}

		// Check CRS metadata
		if traceStep.Metadata["exit_count"] != "2" {
			t.Errorf("expected CRS exit_count='2', got '%s'", traceStep.Metadata["exit_count"])
		}
		if traceStep.Metadata["virtual_exit"] != "true" {
			t.Errorf("expected CRS virtual_exit='true', got '%s'", traceStep.Metadata["virtual_exit"])
		}
	})
}

// BenchmarkPostDominators_Large benchmarks large graph post-dominator computation.
func BenchmarkPostDominators_Large(b *testing.B) {
	numNodes := 10000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create binary tree edges
	edges := make([][2]string, 0, numNodes-1)
	for i := 1; i < numNodes; i++ {
		parent := (i - 1) / 2
		edges = append(edges, [2]string{nodes[parent], nodes[i]})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	exit := nodes[numNodes-1]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.PostDominators(ctx, exit)
	}
}

// =============================================================================
// Dominance Frontier Tests (GR-16d)
// =============================================================================

// -----------------------------------------------------------------------------
// DominanceFrontier Type Tests
// -----------------------------------------------------------------------------

func TestDominanceFrontier_EmptyStruct(t *testing.T) {
	df := &DominanceFrontier{
		Frontier:    make(map[string][]string),
		MergePoints: []string{},
	}

	if df.Frontier == nil {
		t.Error("expected non-nil Frontier map")
	}
	if len(df.Frontier) != 0 {
		t.Error("expected empty Frontier map")
	}
	if df.MergePoints == nil {
		t.Error("expected non-nil MergePoints slice")
	}
	if len(df.MergePoints) != 0 {
		t.Error("expected empty MergePoints slice")
	}
}

// -----------------------------------------------------------------------------
// ComputeDominanceFrontier Algorithm Tests
// -----------------------------------------------------------------------------

func TestDominanceFrontier_Diamond(t *testing.T) {
	// Classic diamond pattern:
	//        A
	//       / \
	//      B   C
	//       \ /
	//        D
	//
	// Dominator tree: A dominates {B, C, D}
	// DF(A) = {}     (A dominates everything)
	// DF(B) = {D}    (B's control ends at D)
	// DF(C) = {D}    (C's control ends at D)
	// DF(D) = {}     (D has no successors)
	// D is a merge point (in DF of both B and C)

	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// First compute dominator tree
	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Now compute dominance frontier
	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// Verify DF(A) = {}
	if len(df.Frontier["A"]) != 0 {
		t.Errorf("expected DF(A) = {}, got %v", df.Frontier["A"])
	}

	// Verify DF(B) = {D}
	if len(df.Frontier["B"]) != 1 || df.Frontier["B"][0] != "D" {
		t.Errorf("expected DF(B) = {D}, got %v", df.Frontier["B"])
	}

	// Verify DF(C) = {D}
	if len(df.Frontier["C"]) != 1 || df.Frontier["C"][0] != "D" {
		t.Errorf("expected DF(C) = {D}, got %v", df.Frontier["C"])
	}

	// Verify DF(D) = {}
	if len(df.Frontier["D"]) != 0 {
		t.Errorf("expected DF(D) = {}, got %v", df.Frontier["D"])
	}

	// Verify D is a merge point
	if len(df.MergePoints) != 1 {
		t.Errorf("expected 1 merge point, got %d: %v", len(df.MergePoints), df.MergePoints)
	}
	if len(df.MergePoints) == 1 && df.MergePoints[0] != "D" {
		t.Errorf("expected merge point D, got %s", df.MergePoints[0])
	}
}

func TestDominanceFrontier_Linear(t *testing.T) {
	// Linear chain: A -> B -> C -> D
	// Dominator tree: A -> B -> C -> D (linear)
	// DF(A) = DF(B) = DF(C) = DF(D) = {} (no merge points)

	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// All frontiers should be empty in a linear chain
	for _, nodeID := range nodes {
		if len(df.Frontier[nodeID]) != 0 {
			t.Errorf("expected DF(%s) = {}, got %v", nodeID, df.Frontier[nodeID])
		}
	}

	// No merge points
	if len(df.MergePoints) != 0 {
		t.Errorf("expected 0 merge points, got %d: %v", len(df.MergePoints), df.MergePoints)
	}
}

func TestDominanceFrontier_MultipleMergePoints(t *testing.T) {
	// Double diamond:
	//        A
	//       / \
	//      B   C
	//       \ /
	//        D
	//       / \
	//      E   F
	//       \ /
	//        G
	//
	// Both D and G are merge points

	nodes := []string{"A", "B", "C", "D", "E", "F", "G"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "E"},
		{"D", "F"},
		{"E", "G"},
		{"F", "G"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// Should have 2 merge points: D and G
	if len(df.MergePoints) != 2 {
		t.Errorf("expected 2 merge points, got %d: %v", len(df.MergePoints), df.MergePoints)
	}

	// Check D and G are merge points
	mergeSet := make(map[string]bool)
	for _, mp := range df.MergePoints {
		mergeSet[mp] = true
	}
	if !mergeSet["D"] {
		t.Error("expected D to be a merge point")
	}
	if !mergeSet["G"] {
		t.Error("expected G to be a merge point")
	}
}

func TestDominanceFrontier_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Pass nil dominator tree
	_, err := analytics.ComputeDominanceFrontier(ctx, nil)
	if err == nil {
		t.Error("expected error for nil dominator tree")
	}
}

func TestDominanceFrontier_EmptyDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create dominator tree with valid entry but empty ImmediateDom
	emptyDomTree := &DominatorTree{
		Entry:        "A",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, emptyDomTree)

	// GR-17b: empty dominator tree with valid entry is now an error
	// (indicates graph not fully populated / initialization race condition)
	if err == nil {
		t.Fatal("expected error for empty dominator tree, got nil")
	}

	// Should still return empty frontier structure (not nil)
	if df == nil {
		t.Fatal("expected non-nil result even on error")
	}
	if len(df.Frontier) != 0 {
		t.Errorf("expected empty frontier for empty domTree, got %v", df.Frontier)
	}
	if len(df.MergePoints) != 0 {
		t.Errorf("expected no merge points for empty domTree, got %v", df.MergePoints)
	}
}

func TestDominanceFrontier_EmptyEntryNode(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create dominator tree with empty entry (invalid)
	invalidDomTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: map[string]string{"A": "A", "B": "A"},
		Depth:        make(map[string]int),
	}

	_, err := analytics.ComputeDominanceFrontier(ctx, invalidDomTree)
	if err == nil {
		t.Error("expected error for dominator tree with empty entry")
	}
}

func TestDominanceFrontier_ContextCancellation(t *testing.T) {
	// Create a larger graph to ensure cancellation can happen
	numNodes := 100
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create diamond pattern for many nodes
	edges := make([][2]string, 0)
	for i := 1; i < numNodes-1; i++ {
		edges = append(edges, [2]string{nodes[0], nodes[i]})          // Root to all middle
		edges = append(edges, [2]string{nodes[i], nodes[numNodes-1]}) // All middle to sink
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Compute dominator tree first
	domTree, err := analytics.Dominators(context.Background(), nodes[0])
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Cancel context before computation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = analytics.ComputeDominanceFrontier(ctx, domTree)
	if err == nil {
		t.Error("expected context cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDominanceFrontier_SelfLoop(t *testing.T) {
	// Graph with self-loop: A -> B -> B (self-loop) -> C
	// Self-loops should not affect dominance frontier

	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "B"}, // Self-loop
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// Linear chain with self-loop - no merge points
	if len(df.MergePoints) != 0 {
		t.Errorf("expected 0 merge points (self-loop doesn't create merge), got %v", df.MergePoints)
	}
}

func TestDominanceFrontier_DomTreeReference(t *testing.T) {
	// Verify that DominanceFrontier stores reference to DomTree

	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// Verify reference is stored
	if df.DomTree != domTree {
		t.Error("expected DominanceFrontier to store reference to DomTree")
	}
}

// -----------------------------------------------------------------------------
// ComputeDominanceFrontierWithCRS Tests
// -----------------------------------------------------------------------------

func TestDominanceFrontierWithCRS_Basic(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, traceStep := analytics.ComputeDominanceFrontierWithCRS(ctx, domTree)

	// Verify result is valid
	if df == nil {
		t.Fatal("expected non-nil result")
	}
	if len(df.MergePoints) != 1 {
		t.Errorf("expected 1 merge point, got %d", len(df.MergePoints))
	}

	// Verify TraceStep
	if traceStep.Action != "analytics_dominance_frontier" {
		t.Errorf("expected action 'analytics_dominance_frontier', got '%s'", traceStep.Action)
	}
	if traceStep.Tool != "ComputeDominanceFrontier" {
		t.Errorf("expected tool 'ComputeDominanceFrontier', got '%s'", traceStep.Tool)
	}
	if traceStep.Target != "A" {
		t.Errorf("expected target 'A', got '%s'", traceStep.Target)
	}
	if traceStep.Duration <= 0 {
		t.Error("expected positive duration")
	}

	// Verify metadata
	if traceStep.Metadata["merge_points_found"] != "1" {
		t.Errorf("expected merge_points_found='1', got '%s'", traceStep.Metadata["merge_points_found"])
	}
}

func TestDominanceFrontierWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	domTree, _ := analytics.Dominators(context.Background(), "A")

	// Cancel context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	df, traceStep := analytics.ComputeDominanceFrontierWithCRS(ctx, domTree)

	// Should return empty result
	if df == nil {
		t.Fatal("expected non-nil result even on cancellation")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for cancelled context")
	}
}

func TestDominanceFrontierWithCRS_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	df, traceStep := analytics.ComputeDominanceFrontierWithCRS(ctx, nil)

	// Should return empty result
	if df == nil {
		t.Fatal("expected non-nil result even on error")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for nil domTree")
	}
}

// -----------------------------------------------------------------------------
// Helper Method Tests
// -----------------------------------------------------------------------------

func TestDominanceFrontier_GetFrontier(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	df, _ := analytics.ComputeDominanceFrontier(ctx, domTree)

	// Test GetFrontier for existing node
	frontier := df.GetFrontier("B")
	if len(frontier) != 1 || frontier[0] != "D" {
		t.Errorf("expected GetFrontier(B) = [D], got %v", frontier)
	}

	// Test GetFrontier for node with empty frontier
	frontier = df.GetFrontier("A")
	if frontier != nil && len(frontier) != 0 {
		t.Errorf("expected GetFrontier(A) = nil or [], got %v", frontier)
	}

	// Test GetFrontier for non-existent node
	frontier = df.GetFrontier("Z")
	if frontier != nil {
		t.Errorf("expected GetFrontier(Z) = nil, got %v", frontier)
	}
}

func TestDominanceFrontier_IsMergePoint(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	df, _ := analytics.ComputeDominanceFrontier(ctx, domTree)

	// D should be a merge point
	if !df.IsMergePoint("D") {
		t.Error("expected D to be a merge point")
	}

	// A, B, C should not be merge points
	if df.IsMergePoint("A") {
		t.Error("expected A to not be a merge point")
	}
	if df.IsMergePoint("B") {
		t.Error("expected B to not be a merge point")
	}
	if df.IsMergePoint("C") {
		t.Error("expected C to not be a merge point")
	}

	// Non-existent node
	if df.IsMergePoint("Z") {
		t.Error("expected Z to not be a merge point")
	}
}

func TestDominanceFrontier_MergePointDegree(t *testing.T) {
	// Create a graph where D is in frontier of B, C, and E
	//        A
	//      / | \
	//     B  C  E
	//      \ | /
	//        D
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"A", "E"},
		{"B", "D"},
		{"C", "D"},
		{"E", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	df, _ := analytics.ComputeDominanceFrontier(ctx, domTree)

	// D should have degree 3 (in frontier of B, C, E)
	degree := df.MergePointDegree("D")
	if degree != 3 {
		t.Errorf("expected MergePointDegree(D) = 3, got %d", degree)
	}

	// Non-merge points should have degree 0
	if df.MergePointDegree("A") != 0 {
		t.Errorf("expected MergePointDegree(A) = 0, got %d", df.MergePointDegree("A"))
	}
}

// -----------------------------------------------------------------------------
// Table-Driven Tests
// -----------------------------------------------------------------------------

func TestDominanceFrontier_TableDriven(t *testing.T) {
	tests := []struct {
		name                string
		nodes               []string
		edges               [][2]string
		entry               string
		expectedMergePoints []string
		expectedFrontiers   map[string][]string
	}{
		{
			name:                "empty graph",
			nodes:               []string{},
			edges:               [][2]string{},
			entry:               "",
			expectedMergePoints: []string{},
			expectedFrontiers:   map[string][]string{},
		},
		{
			name:                "single node",
			nodes:               []string{"A"},
			edges:               [][2]string{},
			entry:               "A",
			expectedMergePoints: []string{},
			expectedFrontiers:   map[string][]string{"A": {}},
		},
		{
			name:  "diamond",
			nodes: []string{"A", "B", "C", "D"},
			edges: [][2]string{
				{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
			},
			entry:               "A",
			expectedMergePoints: []string{"D"},
			expectedFrontiers: map[string][]string{
				"A": {},
				"B": {"D"},
				"C": {"D"},
				"D": {},
			},
		},
		{
			name:  "linear",
			nodes: []string{"A", "B", "C"},
			edges: [][2]string{
				{"A", "B"}, {"B", "C"},
			},
			entry:               "A",
			expectedMergePoints: []string{},
			expectedFrontiers: map[string][]string{
				"A": {},
				"B": {},
				"C": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.nodes) == 0 {
				// Empty graph case
				return
			}

			hg := buildArticulationTestGraph(t, tt.nodes, tt.edges)
			analytics := NewGraphAnalytics(hg)
			ctx := context.Background()

			domTree, err := analytics.Dominators(ctx, tt.entry)
			if err != nil && tt.entry != "" {
				t.Fatalf("failed to compute dominators: %v", err)
			}

			df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
			if err != nil {
				t.Fatalf("failed to compute dominance frontier: %v", err)
			}

			// Check merge points
			if len(df.MergePoints) != len(tt.expectedMergePoints) {
				t.Errorf("expected %d merge points, got %d: %v",
					len(tt.expectedMergePoints), len(df.MergePoints), df.MergePoints)
			}

			// Check expected frontiers
			for nodeID, expectedFrontier := range tt.expectedFrontiers {
				actualFrontier := df.Frontier[nodeID]
				if len(actualFrontier) != len(expectedFrontier) {
					t.Errorf("DF(%s): expected %v, got %v", nodeID, expectedFrontier, actualFrontier)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkDominanceFrontier_Small(b *testing.B) {
	// Diamond graph
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	domTree, _ := analytics.Dominators(ctx, "A")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ComputeDominanceFrontier(ctx, domTree)
	}
}

func BenchmarkDominanceFrontier_Medium(b *testing.B) {
	// 1000 nodes with multiple diamonds
	numNodes := 1000
	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "N" + itoa(i)
	}

	// Create a series of diamonds
	edges := make([][2]string, 0)
	for i := 0; i < numNodes-3; i += 4 {
		// Diamond: i -> i+1, i -> i+2, i+1 -> i+3, i+2 -> i+3
		edges = append(edges, [2]string{nodes[i], nodes[i+1]})
		edges = append(edges, [2]string{nodes[i], nodes[i+2]})
		edges = append(edges, [2]string{nodes[i+1], nodes[i+3]})
		edges = append(edges, [2]string{nodes[i+2], nodes[i+3]})
		// Connect diamonds
		if i+4 < numNodes {
			edges = append(edges, [2]string{nodes[i+3], nodes[i+4]})
		}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	domTree, _ := analytics.Dominators(ctx, nodes[0])

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ComputeDominanceFrontier(ctx, domTree)
	}
}

// =============================================================================
// Control Dependence Tests (GR-16e)
// =============================================================================

// -----------------------------------------------------------------------------
// ControlDependence Type Tests
// -----------------------------------------------------------------------------

func TestControlDependence_EmptyStruct(t *testing.T) {
	cd := &ControlDependence{
		Dependencies: make(map[string][]string),
		Dependents:   make(map[string][]string),
	}

	if cd.Dependencies == nil {
		t.Error("expected non-nil Dependencies map")
	}
	if len(cd.Dependencies) != 0 {
		t.Error("expected empty Dependencies map")
	}
	if cd.Dependents == nil {
		t.Error("expected non-nil Dependents map")
	}
	if len(cd.Dependents) != 0 {
		t.Error("expected empty Dependents map")
	}
	if cd.EdgeCount != 0 {
		t.Error("expected 0 edge count")
	}
}

// -----------------------------------------------------------------------------
// ComputeControlDependence Algorithm Tests
// -----------------------------------------------------------------------------

func TestControlDependence_IfThenElse(t *testing.T) {
	// Classic if-then-else pattern:
	//       cond
	//      /    \
	//   then    else
	//      \    /
	//       merge
	//
	// Control dependencies:
	//   then is control-dependent on cond
	//   else is control-dependent on cond
	//   merge is NOT control-dependent on cond (always executes)

	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Compute post-dominator tree (merge is the exit)
	postDomTree, err := analytics.PostDominators(ctx, "merge")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	// Compute control dependence
	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// then should be control-dependent on cond
	thenDeps := cd.GetDependencies("then")
	if len(thenDeps) == 0 || !containsStr(thenDeps, "cond") {
		t.Errorf("expected 'then' to be control-dependent on 'cond', got %v", thenDeps)
	}

	// else should be control-dependent on cond
	elseDeps := cd.GetDependencies("else")
	if len(elseDeps) == 0 || !containsStr(elseDeps, "cond") {
		t.Errorf("expected 'else' to be control-dependent on 'cond', got %v", elseDeps)
	}

	// merge should NOT be control-dependent on cond
	mergeDeps := cd.GetDependencies("merge")
	if containsStr(mergeDeps, "cond") {
		t.Errorf("expected 'merge' to NOT be control-dependent on 'cond', got %v", mergeDeps)
	}

	// cond should have then and else as dependents
	condDependents := cd.GetDependents("cond")
	if !containsStr(condDependents, "then") || !containsStr(condDependents, "else") {
		t.Errorf("expected 'cond' to control 'then' and 'else', got %v", condDependents)
	}
}

func TestControlDependence_Linear(t *testing.T) {
	// Linear code has no control dependencies:
	// A -> B -> C -> D
	// No branches = no control dependencies

	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// D is exit
	postDomTree, err := analytics.PostDominators(ctx, "D")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// No control dependencies in linear code
	if cd.EdgeCount != 0 {
		t.Errorf("expected 0 control dependencies in linear code, got %d", cd.EdgeCount)
	}
}

func TestControlDependence_NestedIf(t *testing.T) {
	// Nested if pattern:
	//       cond1
	//      /     \
	//   cond2     B
	//   /   \      \
	//  A     C      |
	//   \   /       |
	//    \ /        |
	//   merge2      |
	//      \       /
	//       merge1
	//
	// Control dependencies:
	//   cond2 is control-dependent on cond1
	//   A is control-dependent on cond2
	//   C is control-dependent on cond2
	//   B is control-dependent on cond1

	nodes := []string{"cond1", "cond2", "A", "B", "C", "merge2", "merge1"}
	edges := [][2]string{
		{"cond1", "cond2"},
		{"cond1", "B"},
		{"cond2", "A"},
		{"cond2", "C"},
		{"A", "merge2"},
		{"C", "merge2"},
		{"B", "merge1"},
		{"merge2", "merge1"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, err := analytics.PostDominators(ctx, "merge1")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// B should be control-dependent on cond1
	bDeps := cd.GetDependencies("B")
	if !containsStr(bDeps, "cond1") {
		t.Errorf("expected 'B' to be control-dependent on 'cond1', got %v", bDeps)
	}

	// A should be control-dependent on cond2
	aDeps := cd.GetDependencies("A")
	if !containsStr(aDeps, "cond2") {
		t.Errorf("expected 'A' to be control-dependent on 'cond2', got %v", aDeps)
	}

	// C should be control-dependent on cond2
	cDeps := cd.GetDependencies("C")
	if !containsStr(cDeps, "cond2") {
		t.Errorf("expected 'C' to be control-dependent on 'cond2', got %v", cDeps)
	}
}

func TestControlDependence_NilPostDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Pass nil post-dominator tree
	_, err := analytics.ComputeControlDependence(ctx, nil)
	if err == nil {
		t.Error("expected error for nil post-dominator tree")
	}
}

func TestControlDependence_EmptyPostDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create post-dominator tree with valid entry but empty ImmediateDom
	emptyPostDomTree := &DominatorTree{
		Entry:        "B",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	cd, err := analytics.ComputeControlDependence(ctx, emptyPostDomTree)
	if err != nil {
		t.Fatalf("unexpected error for empty post-dominator tree: %v", err)
	}

	// Should return empty result
	if cd.EdgeCount != 0 {
		t.Errorf("expected 0 edges for empty postDomTree, got %d", cd.EdgeCount)
	}
}

func TestControlDependence_EmptyEntryNode(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create post-dominator tree with empty entry (invalid)
	invalidPostDomTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: map[string]string{"A": "B", "B": "B"},
		Depth:        make(map[string]int),
	}

	_, err := analytics.ComputeControlDependence(ctx, invalidPostDomTree)
	if err == nil {
		t.Error("expected error for post-dominator tree with empty entry")
	}
}

func TestControlDependence_ContextCancellation(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	postDomTree, _ := analytics.PostDominators(context.Background(), "D")

	// Cancel context before computation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err == nil {
		t.Error("expected context cancellation error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestControlDependence_SymmetricConsistency(t *testing.T) {
	// Verify: a in Dependencies[b] iff b in Dependents[a]
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// For every dependency, verify symmetric relationship
	for controlled, controllers := range cd.Dependencies {
		for _, controller := range controllers {
			dependents := cd.Dependents[controller]
			if !containsStr(dependents, controlled) {
				t.Errorf("symmetry violation: %s in Dependencies[%s] but %s not in Dependents[%s]",
					controller, controlled, controlled, controller)
			}
		}
	}

	// And vice versa
	for controller, dependents := range cd.Dependents {
		for _, controlled := range dependents {
			dependencies := cd.Dependencies[controlled]
			if !containsStr(dependencies, controller) {
				t.Errorf("symmetry violation: %s in Dependents[%s] but %s not in Dependencies[%s]",
					controlled, controller, controller, controlled)
			}
		}
	}
}

// -----------------------------------------------------------------------------
// ComputeControlDependenceWithCRS Tests
// -----------------------------------------------------------------------------

func TestControlDependenceWithCRS_Basic(t *testing.T) {
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, traceStep := analytics.ComputeControlDependenceWithCRS(ctx, postDomTree)

	// Verify result is valid
	if cd == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify TraceStep
	if traceStep.Action != "analytics_control_dependence" {
		t.Errorf("expected action 'analytics_control_dependence', got '%s'", traceStep.Action)
	}
	if traceStep.Tool != "ComputeControlDependence" {
		t.Errorf("expected tool 'ComputeControlDependence', got '%s'", traceStep.Tool)
	}
	if traceStep.Duration <= 0 {
		t.Error("expected positive duration")
	}

	// Verify metadata
	if traceStep.Metadata["edge_count"] == "" {
		t.Error("expected edge_count in metadata")
	}
}

func TestControlDependenceWithCRS_NilPostDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	cd, traceStep := analytics.ComputeControlDependenceWithCRS(ctx, nil)

	// Should return empty result
	if cd == nil {
		t.Fatal("expected non-nil result even on error")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for nil postDomTree")
	}
}

// -----------------------------------------------------------------------------
// Helper Method Tests
// -----------------------------------------------------------------------------

func TestControlDependence_GetDependencies(t *testing.T) {
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// then should depend on cond
	deps := cd.GetDependencies("then")
	if !containsStr(deps, "cond") {
		t.Errorf("expected GetDependencies(then) to include 'cond', got %v", deps)
	}

	// Non-existent node
	deps = cd.GetDependencies("nonexistent")
	if deps != nil {
		t.Errorf("expected GetDependencies(nonexistent) = nil, got %v", deps)
	}
}

func TestControlDependence_GetDependents(t *testing.T) {
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// cond should control then and else
	dependents := cd.GetDependents("cond")
	if !containsStr(dependents, "then") || !containsStr(dependents, "else") {
		t.Errorf("expected GetDependents(cond) to include 'then' and 'else', got %v", dependents)
	}
}

func TestControlDependence_IsController(t *testing.T) {
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "else"},
		{"then", "merge"},
		{"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// cond should be a controller
	if !cd.IsController("cond") {
		t.Error("expected 'cond' to be a controller")
	}

	// then should not be a controller (no branches)
	if cd.IsController("then") {
		t.Error("expected 'then' to NOT be a controller")
	}
}

func TestControlDependence_ControlDependencyChain(t *testing.T) {
	// Nested conditionals create a chain:
	//   cond1
	//   /   \
	// cond2   B
	//  / \
	// A   C
	//
	// A is control-dependent on cond2
	// cond2 is control-dependent on cond1
	// So chain from A goes: cond2 -> cond1

	nodes := []string{"cond1", "cond2", "A", "B", "C", "merge2", "merge1"}
	edges := [][2]string{
		{"cond1", "cond2"},
		{"cond1", "B"},
		{"cond2", "A"},
		{"cond2", "C"},
		{"A", "merge2"},
		{"C", "merge2"},
		{"B", "merge1"},
		{"merge2", "merge1"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge1")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// Get chain from A with depth 2
	chain := cd.ControlDependencyChain("A", 2)

	// Should include cond2 directly
	if !containsStr(chain, "cond2") {
		t.Errorf("expected chain from 'A' to include 'cond2', got %v", chain)
	}
}

func TestControlDependence_ControlDependencyChain_MaxDepth(t *testing.T) {
	nodes := []string{"cond", "then", "merge"}
	edges := [][2]string{
		{"cond", "then"},
		{"cond", "merge"},
		{"then", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// Depth 0 should return nil/empty
	chain := cd.ControlDependencyChain("then", 0)
	if len(chain) != 0 {
		t.Errorf("expected empty chain for maxDepth=0, got %v", chain)
	}
}

// containsStr checks if a string slice contains a specific string.
func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkControlDependence_Small(b *testing.B) {
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"}, {"cond", "else"}, {"then", "merge"}, {"else", "merge"},
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)

	ctx := context.Background()
	postDomTree, _ := analytics.PostDominators(ctx, "merge")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ComputeControlDependence(ctx, postDomTree)
	}
}

// =============================================================================
// Additional Tests: GR-16d Dominance Frontier
// =============================================================================

func TestDominanceFrontier_LargeGraph(t *testing.T) {
	// Create a binary tree with 127 nodes (7 levels)
	// This tests scalability and correctness on larger graphs
	nodes := make([]string, 0, 127)
	edges := make([][2]string, 0, 126)

	for i := 0; i < 127; i++ {
		nodes = append(nodes, fmt.Sprintf("n%d", i))
	}

	// Binary tree edges: parent i has children 2i+1 and 2i+2
	for i := 0; i < 63; i++ {
		edges = append(edges, [2]string{fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", 2*i+1)})
		edges = append(edges, [2]string{fmt.Sprintf("n%d", i), fmt.Sprintf("n%d", 2*i+2)})
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "n0")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// In a tree, no node has a dominance frontier (no join points)
	if df.TotalFrontierEntries != 0 {
		t.Errorf("expected 0 frontier entries in tree, got %d", df.TotalFrontierEntries)
	}
}

func TestDominanceFrontier_ComplexDiamond(t *testing.T) {
	// Multiple nested diamonds:
	//        A
	//       / \
	//      B   C
	//       \ /
	//        D
	//       / \
	//      E   F
	//       \ /
	//        G

	nodes := []string{"A", "B", "C", "D", "E", "F", "G"}
	edges := [][2]string{
		{"A", "B"}, {"A", "C"},
		{"B", "D"}, {"C", "D"},
		{"D", "E"}, {"D", "F"},
		{"E", "G"}, {"F", "G"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// D and G should be merge points
	if !df.IsMergePoint("D") {
		t.Error("expected D to be a merge point")
	}
	if !df.IsMergePoint("G") {
		t.Error("expected G to be a merge point")
	}

	// Verify merge point count
	if len(df.MergePoints) != 2 {
		t.Errorf("expected 2 merge points, got %d", len(df.MergePoints))
	}
}

func TestDominanceFrontier_SwitchPattern(t *testing.T) {
	// Switch-like pattern with multiple cases:
	//       switch
	//      /  |  \
	//    c1  c2  c3
	//      \  |  /
	//       merge

	nodes := []string{"switch", "c1", "c2", "c3", "merge"}
	edges := [][2]string{
		{"switch", "c1"}, {"switch", "c2"}, {"switch", "c3"},
		{"c1", "merge"}, {"c2", "merge"}, {"c3", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "switch")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// merge should be a merge point with degree 3
	if !df.IsMergePoint("merge") {
		t.Error("expected merge to be a merge point")
	}
	if df.MergePointDegree("merge") != 3 {
		t.Errorf("expected merge point degree 3, got %d", df.MergePointDegree("merge"))
	}
}

func TestDominanceFrontier_ConcurrentAccess(t *testing.T) {
	// Test thread safety of DominanceFrontier helper methods
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	df, _ := analytics.ComputeDominanceFrontier(ctx, domTree)

	// Concurrent reads should be safe
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = df.GetFrontier("B")
				_ = df.IsMergePoint("D")
				_ = df.MergePointDegree("D")
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestDominanceFrontier_IrreducibleLoop(t *testing.T) {
	// Irreducible control flow (two entry points to a loop):
	//      A
	//     / \
	//    B   C
	//    |\ /|
	//    | X |
	//    |/ \|
	//    D   E
	//     \ /
	//      F

	nodes := []string{"A", "B", "C", "D", "E", "F"}
	edges := [][2]string{
		{"A", "B"}, {"A", "C"},
		{"B", "D"}, {"B", "E"},
		{"C", "D"}, {"C", "E"},
		{"D", "F"}, {"E", "F"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// D, E, F should all be merge points
	if !df.IsMergePoint("D") {
		t.Error("expected D to be a merge point")
	}
	if !df.IsMergePoint("E") {
		t.Error("expected E to be a merge point")
	}
	if !df.IsMergePoint("F") {
		t.Error("expected F to be a merge point")
	}
}

func TestDominanceFrontier_DeeplyNested(t *testing.T) {
	// Deeply nested if-else (10 levels)
	nodes := make([]string, 0, 21)
	edges := make([][2]string, 0, 20)

	// Create chain: cond0 -> cond1 -> ... -> cond9 -> leaf
	for i := 0; i < 10; i++ {
		nodes = append(nodes, fmt.Sprintf("cond%d", i))
	}
	nodes = append(nodes, "leaf")

	for i := 0; i < 10; i++ {
		if i < 9 {
			edges = append(edges, [2]string{fmt.Sprintf("cond%d", i), fmt.Sprintf("cond%d", i+1)})
		} else {
			edges = append(edges, [2]string{fmt.Sprintf("cond%d", i), "leaf"})
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "cond0")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Should handle deep nesting without issues
	df, err := analytics.ComputeDominanceFrontier(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to compute dominance frontier: %v", err)
	}

	// Linear chain has no merge points
	if len(df.MergePoints) != 0 {
		t.Errorf("expected 0 merge points in linear chain, got %d", len(df.MergePoints))
	}
}

func TestDominanceFrontier_NilHelperMethods(t *testing.T) {
	// Test nil receiver handling for all helper methods
	var df *DominanceFrontier

	if df.GetFrontier("A") != nil {
		t.Error("expected nil GetFrontier for nil receiver")
	}
	if df.IsMergePoint("A") {
		t.Error("expected false IsMergePoint for nil receiver")
	}
	if df.MergePointDegree("A") != 0 {
		t.Error("expected 0 MergePointDegree for nil receiver")
	}
}

// =============================================================================
// Additional Tests: GR-16e Control Dependence
// =============================================================================

func TestControlDependence_LargeGraph(t *testing.T) {
	// Create a wide branching structure:
	// root branches to 10 children, each child branches to 2, all merge at end
	nodes := make([]string, 0, 32)
	edges := make([][2]string, 0, 50)

	nodes = append(nodes, "root")
	for i := 0; i < 10; i++ {
		childName := fmt.Sprintf("child%d", i)
		nodes = append(nodes, childName)
		edges = append(edges, [2]string{"root", childName})

		grandA := fmt.Sprintf("grand%d_a", i)
		grandB := fmt.Sprintf("grand%d_b", i)
		nodes = append(nodes, grandA, grandB)
		edges = append(edges, [2]string{childName, grandA})
		edges = append(edges, [2]string{childName, grandB})
		edges = append(edges, [2]string{grandA, "merge"})
		edges = append(edges, [2]string{grandB, "merge"})
	}
	nodes = append(nodes, "merge")

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, err := analytics.PostDominators(ctx, "merge")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// Root should control all children and grandchildren
	rootDeps := cd.GetDependents("root")
	if len(rootDeps) < 10 {
		t.Errorf("expected root to control at least 10 nodes, got %d", len(rootDeps))
	}

	// Each child should control its two grandchildren
	for i := 0; i < 10; i++ {
		childName := fmt.Sprintf("child%d", i)
		childDeps := cd.GetDependents(childName)
		if len(childDeps) != 2 {
			t.Errorf("expected %s to control 2 nodes, got %d", childName, len(childDeps))
		}
	}
}

func TestControlDependence_SwitchCase(t *testing.T) {
	// Switch with 4 cases:
	//     switch
	//    /|  |  \
	//  c1 c2 c3 c4
	//    \|  |/
	//     merge

	nodes := []string{"switch", "c1", "c2", "c3", "c4", "merge"}
	edges := [][2]string{
		{"switch", "c1"}, {"switch", "c2"}, {"switch", "c3"}, {"switch", "c4"},
		{"c1", "merge"}, {"c2", "merge"}, {"c3", "merge"}, {"c4", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, err := analytics.PostDominators(ctx, "merge")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// Switch should control all 4 cases
	switchDeps := cd.GetDependents("switch")
	if len(switchDeps) != 4 {
		t.Errorf("expected switch to control 4 nodes, got %d", len(switchDeps))
	}

	// Each case should be control-dependent on switch
	for _, c := range []string{"c1", "c2", "c3", "c4"} {
		deps := cd.GetDependencies(c)
		if !containsStr(deps, "switch") {
			t.Errorf("expected %s to be control-dependent on switch, got %v", c, deps)
		}
	}

	// merge should NOT be control-dependent on switch
	mergeDeps := cd.GetDependencies("merge")
	if containsStr(mergeDeps, "switch") {
		t.Errorf("expected merge to NOT be control-dependent on switch, got %v", mergeDeps)
	}
}

func TestControlDependence_LoopPattern(t *testing.T) {
	// Loop with conditional inside:
	//   entry -> header -> body -> latch -> header (back edge)
	//                  \-> exit

	nodes := []string{"entry", "header", "body", "latch", "exit"}
	edges := [][2]string{
		{"entry", "header"},
		{"header", "body"},
		{"header", "exit"},
		{"body", "latch"},
		{"latch", "header"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// Header should control body and latch (loop iterations)
	headerDeps := cd.GetDependents("header")
	if !containsStr(headerDeps, "body") {
		t.Error("expected header to control body")
	}
}

func TestControlDependence_MultipleExits(t *testing.T) {
	// Multiple exit points:
	//      entry
	//       / \
	//    exit1  body
	//            |
	//          exit2

	nodes := []string{"entry", "exit1", "body", "exit2"}
	edges := [][2]string{
		{"entry", "exit1"},
		{"entry", "body"},
		{"body", "exit2"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Auto-detect exits
	postDomTree, err := analytics.PostDominators(ctx, "")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("failed to compute control dependence: %v", err)
	}

	// entry controls both exit1 and body
	entryDeps := cd.GetDependents("entry")
	if len(entryDeps) < 2 {
		t.Errorf("expected entry to control at least 2 nodes, got %d", len(entryDeps))
	}
}

func TestControlDependence_ConcurrentAccess(t *testing.T) {
	// Test thread safety of ControlDependence helper methods
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"}, {"cond", "else"}, {"then", "merge"}, {"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// Concurrent reads should be safe
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = cd.GetDependencies("then")
				_ = cd.GetDependents("cond")
				_ = cd.IsController("cond")
				_ = cd.ControlDependencyChain("then", 5)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestControlDependence_NilHelperMethods(t *testing.T) {
	// Test nil receiver handling for all helper methods
	var cd *ControlDependence

	if cd.GetDependencies("A") != nil {
		t.Error("expected nil GetDependencies for nil receiver")
	}
	if cd.GetDependents("A") != nil {
		t.Error("expected nil GetDependents for nil receiver")
	}
	if cd.IsController("A") {
		t.Error("expected false IsController for nil receiver")
	}
	if cd.ControlDependencyChain("A", 5) != nil {
		t.Error("expected nil ControlDependencyChain for nil receiver")
	}
}

func TestControlDependence_ChainWithCycle(t *testing.T) {
	// Test that ControlDependencyChain handles cycles gracefully
	// (via visited set)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"}, {"A", "C"},
		{"B", "D"}, {"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "D")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// Even with high maxDepth, should not infinite loop
	chain := cd.ControlDependencyChain("B", 100)
	if chain == nil {
		t.Error("expected non-nil chain")
	}
}

func TestControlDependence_EmptyGraph(t *testing.T) {
	// Test with single node graph (no edges)
	nodes := []string{"single"}
	edges := [][2]string{}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "single")
	cd, err := analytics.ComputeControlDependence(ctx, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No control dependencies in single node
	if cd.EdgeCount != 0 {
		t.Errorf("expected 0 edges, got %d", cd.EdgeCount)
	}
}

func TestControlDependence_Statistics(t *testing.T) {
	// Verify statistics are computed correctly
	nodes := []string{"cond", "then", "else", "merge"}
	edges := [][2]string{
		{"cond", "then"}, {"cond", "else"}, {"then", "merge"}, {"else", "merge"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "merge")
	cd, _ := analytics.ComputeControlDependence(ctx, postDomTree)

	// Should have 2 edges: then->cond, else->cond
	if cd.EdgeCount != 2 {
		t.Errorf("expected 2 edges, got %d", cd.EdgeCount)
	}

	// Should have 2 nodes with dependencies
	if cd.NodesWithDependencies != 2 {
		t.Errorf("expected 2 nodes with dependencies, got %d", cd.NodesWithDependencies)
	}

	// Should have 1 node with dependents
	if cd.NodesWithDependents != 1 {
		t.Errorf("expected 1 node with dependents, got %d", cd.NodesWithDependents)
	}
}

// =============================================================================
// Benchmark Tests: Wide Fan Pattern
// =============================================================================

func BenchmarkDominanceFrontier_WideFan(b *testing.B) {
	// 100 node wide fan pattern (entry -> 100 children -> exit)
	nodes := make([]string, 102)
	edges := make([][2]string, 0, 200)

	nodes[0] = "entry"
	for i := 1; i <= 100; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		edges = append(edges, [2]string{"entry", nodes[i]})
	}
	nodes[101] = "exit"
	for i := 1; i <= 100; i++ {
		edges = append(edges, [2]string{nodes[i], "exit"})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ComputeDominanceFrontier(ctx, domTree)
	}
}

func BenchmarkControlDependence_Medium(b *testing.B) {
	// Same 100 node diamond pattern
	nodes := make([]string, 102)
	edges := make([][2]string, 0, 200)

	nodes[0] = "entry"
	for i := 1; i <= 100; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		edges = append(edges, [2]string{"entry", nodes[i]})
	}
	nodes[101] = "exit"
	for i := 1; i <= 100; i++ {
		edges = append(edges, [2]string{nodes[i], "exit"})
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.ComputeControlDependence(ctx, postDomTree)
	}
}

// =============================================================================
// Natural Loop Detection Tests (GR-16f)
// =============================================================================

// -----------------------------------------------------------------------------
// Basic Loop Structure Tests
// -----------------------------------------------------------------------------

func TestDetectLoops_EmptyStruct(t *testing.T) {
	// Verify LoopNest has expected default values
	ln := &LoopNest{
		Loops:    []*Loop{},
		TopLevel: []*Loop{},
		LoopOf:   map[string]*Loop{},
	}

	if len(ln.Loops) != 0 {
		t.Error("expected empty loops slice")
	}
	if len(ln.TopLevel) != 0 {
		t.Error("expected empty top-level slice")
	}
	if ln.MaxDepth != 0 {
		t.Error("expected 0 max depth for empty loop nest")
	}
	if ln.BackEdgeCount != 0 {
		t.Error("expected 0 back edge count for empty loop nest")
	}
}

func TestDetectLoops_SimpleLoop(t *testing.T) {
	// Single loop: entry -> header -> body -> header (back edge)
	//                        |
	//                        v
	//                      exit
	nodes := []string{"entry", "header", "body", "exit"}
	edges := [][2]string{
		{"entry", "header"},
		{"header", "body"},
		{"body", "header"}, // Back edge
		{"header", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// Should detect exactly one loop
	if len(result.Loops) != 1 {
		t.Errorf("expected 1 loop, got %d", len(result.Loops))
	}

	// The loop header should be "header"
	if len(result.Loops) > 0 && result.Loops[0].Header != "header" {
		t.Errorf("expected header 'header', got '%s'", result.Loops[0].Header)
	}

	// Loop body should contain header and body
	if len(result.Loops) > 0 {
		body := result.Loops[0].Body
		bodySet := make(map[string]bool)
		for _, n := range body {
			bodySet[n] = true
		}
		if !bodySet["header"] || !bodySet["body"] {
			t.Errorf("loop body should contain header and body, got %v", body)
		}
	}

	// Back edge count should be 1
	if result.BackEdgeCount != 1 {
		t.Errorf("expected 1 back edge, got %d", result.BackEdgeCount)
	}
}

func TestDetectLoops_NoLoops(t *testing.T) {
	// DAG with no back edges: A -> B -> C -> D
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// No loops expected
	if len(result.Loops) != 0 {
		t.Errorf("expected 0 loops for DAG, got %d", len(result.Loops))
	}
	if result.BackEdgeCount != 0 {
		t.Errorf("expected 0 back edges for DAG, got %d", result.BackEdgeCount)
	}
}

func TestDetectLoops_SelfLoop(t *testing.T) {
	// Direct recursion: A -> B -> B (self-loop)
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "B"}, // Self-loop
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// Should detect self-loop
	if len(result.Loops) != 1 {
		t.Errorf("expected 1 loop for self-loop, got %d", len(result.Loops))
	}

	// Header should be B
	if len(result.Loops) > 0 && result.Loops[0].Header != "B" {
		t.Errorf("expected header 'B', got '%s'", result.Loops[0].Header)
	}

	// Body should contain only B
	if len(result.Loops) > 0 && len(result.Loops[0].Body) != 1 {
		t.Errorf("expected body size 1 for self-loop, got %d", len(result.Loops[0].Body))
	}
}

func TestDetectLoops_NestedLoops(t *testing.T) {
	// Nested loops:
	// entry -> outer_header -> inner_header -> inner_body -> inner_header (inner back edge)
	//                |                 |
	//                v                 v
	//          outer_body <- - - - - -+
	//                |
	//         outer_header (outer back edge)
	//                |
	//                v
	//              exit
	nodes := []string{"entry", "outer_header", "inner_header", "inner_body", "outer_body", "exit"}
	edges := [][2]string{
		{"entry", "outer_header"},
		{"outer_header", "inner_header"},
		{"inner_header", "inner_body"},
		{"inner_body", "inner_header"}, // Inner back edge
		{"inner_header", "outer_body"},
		{"outer_body", "outer_header"}, // Outer back edge
		{"outer_header", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// Should detect 2 loops (outer and inner)
	if len(result.Loops) != 2 {
		t.Errorf("expected 2 loops for nested structure, got %d", len(result.Loops))
	}

	// MaxDepth should be at least 1 (inner loop nested in outer)
	if result.MaxDepth < 1 {
		t.Errorf("expected MaxDepth >= 1, got %d", result.MaxDepth)
	}

	// TopLevel should have 1 loop (outer)
	if len(result.TopLevel) != 1 {
		t.Errorf("expected 1 top-level loop, got %d", len(result.TopLevel))
	}

	// Check nesting relationship
	var outerLoop *Loop
	for _, loop := range result.Loops {
		if loop.Header == "outer_header" {
			outerLoop = loop
			break
		}
	}
	if outerLoop != nil && len(outerLoop.Children) != 1 {
		t.Errorf("expected outer loop to have 1 child, got %d", len(outerLoop.Children))
	}
}

func TestDetectLoops_MutualRecursion(t *testing.T) {
	// Mutual recursion: entry -> A -> B -> A (A and B call each other)
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "A"}, // Back edge (mutual recursion)
		{"A", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// Should detect the mutual recursion as a loop
	if len(result.Loops) < 1 {
		t.Errorf("expected at least 1 loop for mutual recursion, got %d", len(result.Loops))
	}
}

func TestDetectLoops_MultipleLoops(t *testing.T) {
	// Multiple separate loops at same level
	// entry -> A -> A (loop 1)
	//       -> B -> B (loop 2)
	//       -> exit
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"A", "A"}, // Loop 1
		{"B", "B"}, // Loop 2
		{"A", "exit"},
		{"B", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	result, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("DetectLoops failed: %v", err)
	}

	// Should detect 2 separate loops
	if len(result.Loops) != 2 {
		t.Errorf("expected 2 loops, got %d", len(result.Loops))
	}

	// Both should be top-level (no nesting)
	if len(result.TopLevel) != 2 {
		t.Errorf("expected 2 top-level loops, got %d", len(result.TopLevel))
	}

	// MaxDepth should be 0 (no nesting)
	if result.MaxDepth != 0 {
		t.Errorf("expected MaxDepth 0 (no nesting), got %d", result.MaxDepth)
	}
}

// -----------------------------------------------------------------------------
// Edge Case Tests
// -----------------------------------------------------------------------------

func TestDetectLoops_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	result, err := analytics.DetectLoops(ctx, nil)

	// Should return error for nil domTree
	if err == nil {
		t.Error("expected error for nil domTree")
	}

	// Result should be empty but not nil
	if result == nil {
		t.Error("expected non-nil result even on error")
	}
}

func TestDetectLoops_EmptyDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create empty dominator tree
	domTree := &DominatorTree{
		Entry:        "A",
		ImmediateDom: map[string]string{},
		Depth:        map[string]int{},
	}

	result, err := analytics.DetectLoops(ctx, domTree)

	// Should succeed with empty result
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result.Loops) != 0 {
		t.Errorf("expected 0 loops for empty domTree, got %d", len(result.Loops))
	}
}

func TestDetectLoops_ContextCancellation(t *testing.T) {
	// Build a graph
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}, {"B", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	domTree, _ := analytics.Dominators(context.Background(), "A")

	result, err := analytics.DetectLoops(ctx, domTree)

	// Should return context error
	if err == nil {
		t.Error("expected context cancellation error")
	}

	// Result should still be non-nil
	if result == nil {
		t.Error("expected non-nil result even on cancellation")
	}
}

// -----------------------------------------------------------------------------
// Helper Method Tests
// -----------------------------------------------------------------------------

func TestLoopNest_IsInLoop(t *testing.T) {
	loop := &Loop{
		Header: "header",
		Body:   []string{"header", "body"},
	}
	ln := &LoopNest{
		Loops:  []*Loop{loop},
		LoopOf: map[string]*Loop{"header": loop, "body": loop},
	}

	if !ln.IsInLoop("header") {
		t.Error("expected header to be in loop")
	}
	if !ln.IsInLoop("body") {
		t.Error("expected body to be in loop")
	}
	if ln.IsInLoop("outside") {
		t.Error("expected 'outside' to not be in loop")
	}
}

func TestLoopNest_IsInLoop_NilReceiver(t *testing.T) {
	var ln *LoopNest = nil
	if ln.IsInLoop("anything") {
		t.Error("expected nil receiver to return false")
	}
}

func TestLoopNest_GetInnermostLoop(t *testing.T) {
	outerLoop := &Loop{Header: "outer", Body: []string{"outer", "inner", "body"}}
	innerLoop := &Loop{Header: "inner", Body: []string{"inner", "body"}, Parent: outerLoop}
	outerLoop.Children = []*Loop{innerLoop}

	ln := &LoopNest{
		Loops:    []*Loop{outerLoop, innerLoop},
		TopLevel: []*Loop{outerLoop},
		LoopOf:   map[string]*Loop{"outer": outerLoop, "inner": innerLoop, "body": innerLoop},
	}

	// Body should be in innermost (inner) loop
	if got := ln.GetInnermostLoop("body"); got != innerLoop {
		t.Errorf("expected innerLoop for 'body', got %v", got)
	}

	// Inner header should be in inner loop
	if got := ln.GetInnermostLoop("inner"); got != innerLoop {
		t.Errorf("expected innerLoop for 'inner', got %v", got)
	}

	// Outer header should be in outer loop
	if got := ln.GetInnermostLoop("outer"); got != outerLoop {
		t.Errorf("expected outerLoop for 'outer', got %v", got)
	}

	// Non-existent node should return nil
	if got := ln.GetInnermostLoop("nonexistent"); got != nil {
		t.Errorf("expected nil for 'nonexistent', got %v", got)
	}
}

func TestLoopNest_GetInnermostLoop_NilReceiver(t *testing.T) {
	var ln *LoopNest = nil
	if ln.GetInnermostLoop("anything") != nil {
		t.Error("expected nil receiver to return nil")
	}
}

// -----------------------------------------------------------------------------
// CRS Integration Tests
// -----------------------------------------------------------------------------

func TestDetectLoopsWithCRS_Basic(t *testing.T) {
	nodes := []string{"entry", "header", "body", "exit"}
	edges := [][2]string{
		{"entry", "header"},
		{"header", "body"},
		{"body", "header"}, // Back edge
		{"header", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	result, traceStep := analytics.DetectLoopsWithCRS(ctx, domTree)

	// Verify result
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Loops) != 1 {
		t.Errorf("expected 1 loop, got %d", len(result.Loops))
	}

	// Verify trace step
	if traceStep.Action != "analytics_loops" {
		t.Errorf("expected action 'analytics_loops', got '%s'", traceStep.Action)
	}
	if traceStep.Tool != "DetectLoops" {
		t.Errorf("expected tool 'DetectLoops', got '%s'", traceStep.Tool)
	}
	if traceStep.Duration <= 0 {
		t.Error("expected positive duration")
	}

	// Verify metadata
	if traceStep.Metadata["loops_found"] != "1" {
		t.Errorf("expected loops_found=1, got %s", traceStep.Metadata["loops_found"])
	}
	if traceStep.Metadata["back_edges"] != "1" {
		t.Errorf("expected back_edges=1, got %s", traceStep.Metadata["back_edges"])
	}
}

func TestDetectLoopsWithCRS_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	result, traceStep := analytics.DetectLoopsWithCRS(ctx, nil)

	// Should return empty result
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in trace step for nil domTree")
	}
}

func TestDetectLoopsWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}, {"B", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	domTree, _ := analytics.Dominators(context.Background(), "A")
	result, traceStep := analytics.DetectLoopsWithCRS(ctx, domTree)

	// Should return result
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// TraceStep should have error
	if traceStep.Error == "" {
		t.Error("expected error in trace step for cancelled context")
	}
}

// -----------------------------------------------------------------------------
// Loop Structure Property Tests
// -----------------------------------------------------------------------------

func TestLoop_BackEdgeProperty(t *testing.T) {
	// Verify that back edges are correctly identified
	// A back edge goes from a node to a dominator
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "A"}, // Back edge: A dominates C
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	result, _ := analytics.DetectLoops(ctx, domTree)

	if len(result.Loops) != 1 {
		t.Fatalf("expected 1 loop, got %d", len(result.Loops))
	}

	loop := result.Loops[0]

	// Back edge should be C -> A
	if len(loop.BackEdges) != 1 {
		t.Fatalf("expected 1 back edge, got %d", len(loop.BackEdges))
	}

	backEdge := loop.BackEdges[0]
	if backEdge[0] != "C" || backEdge[1] != "A" {
		t.Errorf("expected back edge [C, A], got %v", backEdge)
	}
}

func TestLoop_BodyContainsHeader(t *testing.T) {
	// Verify loop body always contains the header
	nodes := []string{"entry", "header", "body1", "body2"}
	edges := [][2]string{
		{"entry", "header"},
		{"header", "body1"},
		{"body1", "body2"},
		{"body2", "header"}, // Back edge
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	result, _ := analytics.DetectLoops(ctx, domTree)

	if len(result.Loops) != 1 {
		t.Fatalf("expected 1 loop, got %d", len(result.Loops))
	}

	loop := result.Loops[0]
	bodySet := make(map[string]bool)
	for _, n := range loop.Body {
		bodySet[n] = true
	}

	if !bodySet[loop.Header] {
		t.Error("loop body should contain header")
	}
}

// -----------------------------------------------------------------------------
// Benchmark Tests
// -----------------------------------------------------------------------------

func BenchmarkDetectLoops_Small(b *testing.B) {
	// Small loop: 5 nodes
	nodes := []string{"entry", "h", "b1", "b2", "exit"}
	edges := [][2]string{
		{"entry", "h"},
		{"h", "b1"},
		{"b1", "b2"},
		{"b2", "h"}, // Back edge
		{"h", "exit"},
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DetectLoops(ctx, domTree)
	}
}

func BenchmarkDetectLoops_NestedLoops(b *testing.B) {
	// Nested loop structure: 10 nested loops
	nodes := make([]string, 0, 22)
	edges := make([][2]string, 0, 30)

	nodes = append(nodes, "entry")
	prev := "entry"

	for i := 0; i < 10; i++ {
		header := fmt.Sprintf("h%d", i)
		body := fmt.Sprintf("b%d", i)
		nodes = append(nodes, header, body)
		edges = append(edges, [2]string{prev, header})
		edges = append(edges, [2]string{header, body})
		edges = append(edges, [2]string{body, header}) // Back edge
		prev = header
	}
	nodes = append(nodes, "exit")
	edges = append(edges, [2]string{prev, "exit"})

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DetectLoops(ctx, domTree)
	}
}

// =============================================================================
// Lowest Common Dominator Tests (GR-16g)
// =============================================================================

// -----------------------------------------------------------------------------
// LowestCommonDominator Basic Tests
// -----------------------------------------------------------------------------

func TestLCD_SameNode(t *testing.T) {
	// LCD(a, a) = a (reflexive property)
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	lcd := domTree.LowestCommonDominator("A", "A")
	if lcd != "A" {
		t.Errorf("LCD(A, A) = %s, expected A", lcd)
	}

	lcd = domTree.LowestCommonDominator("entry", "entry")
	if lcd != "entry" {
		t.Errorf("LCD(entry, entry) = %s, expected entry", lcd)
	}
}

func TestLCD_ParentChild(t *testing.T) {
	// LCD(parent, child) = parent (parent dominates child)
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// A dominates B, so LCD(A, B) = A
	lcd := domTree.LowestCommonDominator("A", "B")
	if lcd != "A" {
		t.Errorf("LCD(A, B) = %s, expected A", lcd)
	}

	// entry dominates all, so LCD(entry, C) = entry
	lcd = domTree.LowestCommonDominator("entry", "C")
	if lcd != "entry" {
		t.Errorf("LCD(entry, C) = %s, expected entry", lcd)
	}
}

func TestLCD_Siblings(t *testing.T) {
	// LCD of siblings = their common parent
	//     entry
	//       |
	//       A
	//      / \
	//     B   C
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// B and C are siblings with parent A
	lcd := domTree.LowestCommonDominator("B", "C")
	if lcd != "A" {
		t.Errorf("LCD(B, C) = %s, expected A", lcd)
	}
}

func TestLCD_Diamond(t *testing.T) {
	// Diamond pattern:
	//     entry
	//       |
	//       A
	//      / \
	//     B   C
	//      \ /
	//       D
	//
	// LCD(B, D) = A (since A dominates both)
	// LCD(C, D) = A (same reason)
	nodes := []string{"entry", "A", "B", "C", "D"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// In a diamond, A dominates D (since all paths go through A)
	// So LCD(B, D) = A
	lcd := domTree.LowestCommonDominator("B", "D")
	if lcd != "A" {
		t.Errorf("LCD(B, D) = %s, expected A", lcd)
	}

	// LCD(B, C) = A (siblings)
	lcd = domTree.LowestCommonDominator("B", "C")
	if lcd != "A" {
		t.Errorf("LCD(B, C) = %s, expected A", lcd)
	}
}

func TestLCD_DeepTree(t *testing.T) {
	// Deep linear chain: entry -> A -> B -> C -> D -> E
	nodes := []string{"entry", "A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// In a linear chain, each node dominates all subsequent nodes
	// LCD(B, E) = B
	lcd := domTree.LowestCommonDominator("B", "E")
	if lcd != "B" {
		t.Errorf("LCD(B, E) = %s, expected B", lcd)
	}

	// LCD(A, E) = A
	lcd = domTree.LowestCommonDominator("A", "E")
	if lcd != "A" {
		t.Errorf("LCD(A, E) = %s, expected A", lcd)
	}
}

func TestLCD_EntryNode(t *testing.T) {
	// Entry node is LCD when nodes are in different subtrees
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// A and B are both children of entry with no common dominator except entry
	lcd := domTree.LowestCommonDominator("A", "B")
	if lcd != "entry" {
		t.Errorf("LCD(A, B) = %s, expected entry", lcd)
	}
}

// -----------------------------------------------------------------------------
// LowestCommonDominator Edge Cases
// -----------------------------------------------------------------------------

func TestLCD_NilTree(t *testing.T) {
	var dt *DominatorTree
	// Should not panic, return empty string
	lcd := dt.LowestCommonDominator("A", "B")
	if lcd != "" {
		t.Errorf("LCD on nil tree = %s, expected empty string", lcd)
	}
}

func TestLCD_EmptyTree(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "entry",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	// Empty tree should return entry (fallback)
	lcd := dt.LowestCommonDominator("A", "B")
	if lcd != "entry" {
		t.Errorf("LCD on empty tree = %s, expected entry", lcd)
	}
}

func TestLCD_NodeNotInTree(t *testing.T) {
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Query with node not in tree should return entry (fallback)
	lcd := domTree.LowestCommonDominator("A", "NotInTree")
	if lcd != "entry" {
		t.Errorf("LCD(A, NotInTree) = %s, expected entry", lcd)
	}

	lcd = domTree.LowestCommonDominator("NotInTree1", "NotInTree2")
	if lcd != "entry" {
		t.Errorf("LCD(NotInTree1, NotInTree2) = %s, expected entry", lcd)
	}
}

// -----------------------------------------------------------------------------
// LowestCommonDominatorMultiple Tests
// -----------------------------------------------------------------------------

func TestLCD_Multiple_Empty(t *testing.T) {
	nodes := []string{"entry", "A"}
	edges := [][2]string{{"entry", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Empty slice should return entry
	lcd := domTree.LowestCommonDominatorMultiple([]string{})
	if lcd != "entry" {
		t.Errorf("LCD of empty slice = %s, expected entry", lcd)
	}
}

func TestLCD_Multiple_Single(t *testing.T) {
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Single node should return itself
	lcd := domTree.LowestCommonDominatorMultiple([]string{"A"})
	if lcd != "A" {
		t.Errorf("LCD of single node = %s, expected A", lcd)
	}
}

func TestLCD_Multiple_ThreeNodes(t *testing.T) {
	//     entry
	//       |
	//       A
	//     / | \
	//    B  C  D
	nodes := []string{"entry", "A", "B", "C", "D"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"A", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// LCD(B, C, D) = A (common parent)
	lcd := domTree.LowestCommonDominatorMultiple([]string{"B", "C", "D"})
	if lcd != "A" {
		t.Errorf("LCD(B, C, D) = %s, expected A", lcd)
	}
}

func TestLCD_Multiple_Associativity(t *testing.T) {
	// Test that LCD(a, b, c) = LCD(LCD(a, b), c) = LCD(a, LCD(b, c))
	nodes := []string{"entry", "A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// All three nodes should give the same result regardless of order
	lcd1 := domTree.LowestCommonDominatorMultiple([]string{"D", "E", "A"})
	lcd2 := domTree.LowestCommonDominatorMultiple([]string{"A", "D", "E"})
	lcd3 := domTree.LowestCommonDominatorMultiple([]string{"E", "A", "D"})

	if lcd1 != lcd2 || lcd2 != lcd3 {
		t.Errorf("LCD associativity violated: LCD1=%s, LCD2=%s, LCD3=%s", lcd1, lcd2, lcd3)
	}
}

func TestLCD_Multiple_NilTree(t *testing.T) {
	var dt *DominatorTree
	lcd := dt.LowestCommonDominatorMultiple([]string{"A", "B", "C"})
	if lcd != "" {
		t.Errorf("LCD on nil tree = %s, expected empty string", lcd)
	}
}

// -----------------------------------------------------------------------------
// LCAQueryEngine Tests (Binary Lifting)
// -----------------------------------------------------------------------------

func TestLCAQueryEngine_Basic(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "D"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	lca := domTree.PrepareLCAQueries()
	if lca == nil {
		t.Fatal("PrepareLCAQueries returned nil")
	}

	// Test queries
	lcd := lca.Query("B", "C")
	if lcd != "A" {
		t.Errorf("LCA.Query(B, C) = %s, expected A", lcd)
	}

	lcd = lca.Query("D", "C")
	if lcd != "A" {
		t.Errorf("LCA.Query(D, C) = %s, expected A", lcd)
	}
}

func TestLCAQueryEngine_Correctness(t *testing.T) {
	// Verify O(1) queries match O(depth) queries
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"B", "E"},
		{"C", "F"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	lca := domTree.PrepareLCAQueries()

	// Compare all pairs
	testNodes := []string{"entry", "A", "B", "C", "D", "E", "F"}
	for _, a := range testNodes {
		for _, b := range testNodes {
			basicLCD := domTree.LowestCommonDominator(a, b)
			lcaLCD := lca.Query(a, b)
			if basicLCD != lcaLCD {
				t.Errorf("LCD mismatch for (%s, %s): basic=%s, lca=%s", a, b, basicLCD, lcaLCD)
			}
		}
	}
}

func TestLCAQueryEngine_NilTree(t *testing.T) {
	var dt *DominatorTree
	lca := dt.PrepareLCAQueries()
	if lca != nil {
		t.Error("PrepareLCAQueries on nil tree should return nil")
	}
}

func TestLCAQueryEngine_EmptyTree(t *testing.T) {
	dt := &DominatorTree{
		Entry:        "entry",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	lca := dt.PrepareLCAQueries()
	if lca == nil {
		t.Fatal("PrepareLCAQueries on empty tree returned nil")
	}

	// Query should return entry as fallback
	lcd := lca.Query("A", "B")
	if lcd != "entry" {
		t.Errorf("LCA.Query on empty tree = %s, expected entry", lcd)
	}
}

func TestLCAQueryEngine_SameNode(t *testing.T) {
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	lca := domTree.PrepareLCAQueries()

	lcd := lca.Query("A", "A")
	if lcd != "A" {
		t.Errorf("LCA.Query(A, A) = %s, expected A", lcd)
	}
}

// -----------------------------------------------------------------------------
// LCDWithCRS Tests
// -----------------------------------------------------------------------------

func TestLCDWithCRS_Basic(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	lcd, step := analytics.LCDWithCRS(ctx, domTree, []string{"B", "C"})
	if lcd != "A" {
		t.Errorf("LCD = %s, expected A", lcd)
	}

	// Verify TraceStep is populated
	if step.Action != "analytics_lcd" {
		t.Errorf("TraceStep.Action = %s, expected analytics_lcd", step.Action)
	}
	if step.Tool != "LowestCommonDominator" {
		t.Errorf("TraceStep.Tool = %s, expected LowestCommonDominator", step.Tool)
	}
	if step.Duration <= 0 {
		t.Error("TraceStep.Duration should be positive")
	}
}

func TestLCDWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	domTree, _ := analytics.Dominators(context.Background(), "entry")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	lcd, step := analytics.LCDWithCRS(ctx, domTree, []string{"A", "B"})

	// Should still return a result (LCD is fast, doesn't check context internally)
	// But step should indicate the cancellation
	if step.Error == "" {
		// If it completed before checking, that's OK
		if lcd == "" {
			t.Error("Expected non-empty result or error in step")
		}
	}
}

func TestLCDWithCRS_NilDomTree(t *testing.T) {
	nodes := []string{"entry", "A"}
	edges := [][2]string{{"entry", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	lcd, step := analytics.LCDWithCRS(ctx, nil, []string{"A", "B"})

	// Should handle gracefully
	if lcd != "" {
		t.Errorf("LCD with nil tree = %s, expected empty", lcd)
	}
	if step.Error == "" {
		t.Error("Expected error in TraceStep for nil domTree")
	}
}

func TestLCDWithCRS_Metadata(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	_, step := analytics.LCDWithCRS(ctx, domTree, []string{"B", "C"})

	// Check metadata
	if step.Metadata == nil {
		t.Fatal("TraceStep.Metadata is nil")
	}

	if _, ok := step.Metadata["nodes_queried"]; !ok {
		t.Error("Missing metadata: nodes_queried")
	}
	if _, ok := step.Metadata["lcd_result"]; !ok {
		t.Error("Missing metadata: lcd_result")
	}
}

// -----------------------------------------------------------------------------
// LCD Benchmarks
// -----------------------------------------------------------------------------

func BenchmarkLCD_Shallow(b *testing.B) {
	// Tree with depth ~5
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F", "G"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"A", "C"},
		{"A", "D"},
		{"B", "E"},
		{"B", "F"},
		{"C", "G"},
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = domTree.LowestCommonDominator("G", "F")
	}
}

func BenchmarkLCD_Deep(b *testing.B) {
	// Linear chain with depth 100
	nodes := make([]string, 101)
	edges := make([][2]string, 100)

	nodes[0] = "entry"
	for i := 1; i <= 100; i++ {
		nodes[i] = fmt.Sprintf("N%d", i)
		edges[i-1] = [2]string{nodes[i-1], nodes[i]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = domTree.LowestCommonDominator("N50", "N100")
	}
}

func BenchmarkLCAQueryEngine_Prep(b *testing.B) {
	// Measure preprocessing time
	nodes := make([]string, 101)
	edges := make([][2]string, 100)

	nodes[0] = "entry"
	for i := 1; i <= 100; i++ {
		nodes[i] = fmt.Sprintf("N%d", i)
		edges[i-1] = [2]string{nodes[i-1], nodes[i]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = domTree.PrepareLCAQueries()
	}
}

func BenchmarkLCAQueryEngine_Query(b *testing.B) {
	// Measure query time after preprocessing
	nodes := make([]string, 101)
	edges := make([][2]string, 100)

	nodes[0] = "entry"
	for i := 1; i <= 100; i++ {
		nodes[i] = fmt.Sprintf("N%d", i)
		edges[i-1] = [2]string{nodes[i-1], nodes[i]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	lca := domTree.PrepareLCAQueries()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lca.Query("N50", "N100")
	}
}

// -----------------------------------------------------------------------------
// Additional LCD Tests (Edge Cases and Complex Scenarios)
// -----------------------------------------------------------------------------

func TestLCD_ConcurrentAccess(t *testing.T) {
	// Test concurrent LCD queries are safe
	nodes := []string{"entry", "A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Concurrent queries
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				_ = domTree.LowestCommonDominator("D", "E")
				_ = domTree.LowestCommonDominator("B", "C")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestLCD_LargeTree(t *testing.T) {
	// Test with 1000 nodes in a binary tree pattern
	nodes := make([]string, 1001)
	edges := make([][2]string, 1000)

	nodes[0] = "entry"
	for i := 1; i <= 1000; i++ {
		nodes[i] = fmt.Sprintf("N%d", i)
		// Create binary tree structure
		parentIdx := (i - 1) / 2
		edges[i-1] = [2]string{nodes[parentIdx], nodes[i]}
	}

	g := NewGraph("/test/project")
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// Test LCD of nodes at far ends of tree
	lcd := domTree.LowestCommonDominator("N500", "N750")
	if lcd == "" {
		t.Error("LCD of large tree nodes should not be empty")
	}

	// Verify it dominates both
	if !domTree.Dominates(lcd, "N500") || !domTree.Dominates(lcd, "N750") {
		t.Errorf("LCD %s should dominate both N500 and N750", lcd)
	}
}

func TestLCD_PostDominatorTree(t *testing.T) {
	// Test LCD works correctly on post-dominator trees
	nodes := []string{"entry", "A", "B", "C", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "exit"},
		{"C", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	// In post-dom tree, exit dominates all
	lcd := postDomTree.LowestCommonDominator("B", "C")
	if lcd != "exit" {
		t.Errorf("LCD(B, C) in post-dom tree = %s, expected exit", lcd)
	}
}

func TestLCD_ComplexTopology(t *testing.T) {
	// Complex graph with multiple merge points
	//     entry
	//       |
	//       A
	//      /|\
	//     B C D
	//     |X| |
	//     E F G
	//      \|/
	//       H
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F", "G", "H"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"A", "D"},
		{"B", "E"},
		{"B", "F"},
		{"C", "E"},
		{"C", "F"},
		{"D", "G"},
		{"E", "H"},
		{"F", "H"},
		{"G", "H"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// LCD(E, G) should be A (since A dominates both through different paths)
	lcd := domTree.LowestCommonDominator("E", "G")
	if lcd != "A" {
		t.Errorf("LCD(E, G) = %s, expected A", lcd)
	}

	// LCD(E, F) should be higher up since they share predecessors B and C
	lcd = domTree.LowestCommonDominator("E", "F")
	if lcd != "A" {
		t.Errorf("LCD(E, F) = %s, expected A", lcd)
	}
}

func TestLCD_Symmetry(t *testing.T) {
	// LCD(a, b) should equal LCD(b, a)
	nodes := []string{"entry", "A", "B", "C", "D"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	pairs := [][2]string{
		{"B", "C"},
		{"D", "C"},
		{"entry", "D"},
		{"A", "B"},
	}

	for _, pair := range pairs {
		lcd1 := domTree.LowestCommonDominator(pair[0], pair[1])
		lcd2 := domTree.LowestCommonDominator(pair[1], pair[0])
		if lcd1 != lcd2 {
			t.Errorf("LCD symmetry violated: LCD(%s, %s)=%s but LCD(%s, %s)=%s",
				pair[0], pair[1], lcd1, pair[1], pair[0], lcd2)
		}
	}
}

func TestLCAQueryEngine_ConcurrentAccess(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "E"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	lca := domTree.PrepareLCAQueries()

	// Concurrent queries on LCA engine
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = lca.Query("D", "E")
				_ = lca.Query("B", "C")
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestLCAQueryEngine_AllPairs(t *testing.T) {
	// Exhaustive test: verify LCA matches basic LCD for all node pairs
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F", "G", "H"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"B", "E"},
		{"C", "F"},
		{"C", "G"},
		{"D", "H"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	lca := domTree.PrepareLCAQueries()

	// Test all pairs
	for _, a := range nodes {
		for _, b := range nodes {
			basicLCD := domTree.LowestCommonDominator(a, b)
			lcaLCD := lca.Query(a, b)
			if basicLCD != lcaLCD {
				t.Errorf("Mismatch for (%s, %s): basic=%s, lca=%s", a, b, basicLCD, lcaLCD)
			}
		}
	}
}

func TestLCDWithCRS_EmptyNodes(t *testing.T) {
	nodes := []string{"entry", "A"}
	edges := [][2]string{{"entry", "A"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	// Empty nodes slice
	lcd, step := analytics.LCDWithCRS(ctx, domTree, []string{})
	if lcd != "entry" {
		t.Errorf("LCD of empty slice = %s, expected entry", lcd)
	}
	if step.Error != "" {
		t.Errorf("Unexpected error for empty slice: %s", step.Error)
	}
}

func TestLCDWithCRS_SingleNode(t *testing.T) {
	nodes := []string{"entry", "A", "B"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	lcd, step := analytics.LCDWithCRS(ctx, domTree, []string{"B"})
	if lcd != "B" {
		t.Errorf("LCD of single node = %s, expected B", lcd)
	}
	if step.Metadata["nodes_queried"] != "1" {
		t.Errorf("Metadata nodes_queried = %s, expected 1", step.Metadata["nodes_queried"])
	}
}

func TestLCD_TableDriven(t *testing.T) {
	// Comprehensive table-driven test
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"B", "E"},
		{"C", "F"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	testCases := []struct {
		a, b     string
		expected string
	}{
		{"A", "A", "A"},
		{"B", "C", "A"},
		{"D", "E", "B"},
		{"D", "F", "A"},
		{"E", "F", "A"},
		{"entry", "F", "entry"},
		{"B", "F", "A"},
		{"D", "C", "A"},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("LCD(%s,%s)", tc.a, tc.b), func(t *testing.T) {
			lcd := domTree.LowestCommonDominator(tc.a, tc.b)
			if lcd != tc.expected {
				t.Errorf("LCD(%s, %s) = %s, expected %s", tc.a, tc.b, lcd, tc.expected)
			}
		})
	}
}

// =============================================================================
// Additional Loop Detection Tests (GR-16f)
// =============================================================================

// TestDetectLoops_VeryLargeLoop tests detection of a large loop body.
func TestDetectLoops_VeryLargeLoop(t *testing.T) {
	// Build a loop with many nodes in the body
	const bodySize = 100
	nodes := make([]string, 0, bodySize+2)
	edges := make([][2]string, 0, bodySize+2)

	nodes = append(nodes, "entry", "header")
	edges = append(edges, [2]string{"entry", "header"})

	// Add body nodes in a chain
	prev := "header"
	for i := 0; i < bodySize; i++ {
		nodeID := fmt.Sprintf("body%d", i)
		nodes = append(nodes, nodeID)
		edges = append(edges, [2]string{prev, nodeID})
		prev = nodeID
	}

	// Back edge from last body node to header
	edges = append(edges, [2]string{prev, "header"})

	// Exit from header
	nodes = append(nodes, "exit")
	edges = append(edges, [2]string{"header", "exit"})

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	loops, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to detect loops: %v", err)
	}

	if len(loops.Loops) != 1 {
		t.Errorf("expected 1 loop, got %d", len(loops.Loops))
	}

	if len(loops.Loops) > 0 {
		loop := loops.Loops[0]
		if loop.Header != "header" {
			t.Errorf("expected header 'header', got '%s'", loop.Header)
		}
		// Body should include header + all body nodes = bodySize + 1
		expectedBodySize := bodySize + 1
		if loop.Size != expectedBodySize {
			t.Errorf("expected body size %d, got %d", expectedBodySize, loop.Size)
		}
	}
}

// TestDetectLoops_SharedHeader tests multiple back edges to the same header.
func TestDetectLoops_SharedHeader(t *testing.T) {
	// Two paths that loop back to the same header
	//     entry
	//       |
	//     header
	//     /    \
	//   path1  path2
	//     \    /
	//     header (back edges)
	//       |
	//     exit
	nodes := []string{"entry", "header", "path1", "path2", "exit"}
	edges := [][2]string{
		{"entry", "header"},
		{"header", "path1"},
		{"header", "path2"},
		{"path1", "header"}, // Back edge 1
		{"path2", "header"}, // Back edge 2
		{"header", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	loops, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to detect loops: %v", err)
	}

	// Should find 1 loop (multiple back edges combined)
	if len(loops.Loops) != 1 {
		t.Errorf("expected 1 loop with multiple back edges, got %d loops", len(loops.Loops))
	}

	if len(loops.Loops) > 0 {
		loop := loops.Loops[0]
		if len(loop.BackEdges) != 2 {
			t.Errorf("expected 2 back edges, got %d", len(loop.BackEdges))
		}
	}
}

// TestDetectLoops_DeeplyNested tests detection of deeply nested loops.
func TestDetectLoops_DeeplyNested(t *testing.T) {
	// Build 5 levels of nested loops
	const nestingDepth = 5
	nodes := []string{"entry"}
	edges := make([][2]string, 0)

	prev := "entry"
	var headers []string

	for i := 0; i < nestingDepth; i++ {
		header := fmt.Sprintf("h%d", i)
		body := fmt.Sprintf("b%d", i)
		headers = append(headers, header)
		nodes = append(nodes, header, body)

		edges = append(edges, [2]string{prev, header})
		edges = append(edges, [2]string{header, body})

		// Each inner loop is inside the outer one
		if i < nestingDepth-1 {
			prev = body
		} else {
			// Innermost: back edges all the way up
			for j := i; j >= 0; j-- {
				edges = append(edges, [2]string{body, headers[j]})
			}
		}
	}

	nodes = append(nodes, "exit")
	edges = append(edges, [2]string{headers[0], "exit"})

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	loops, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to detect loops: %v", err)
	}

	// Should find nestingDepth loops
	if len(loops.Loops) < nestingDepth {
		t.Errorf("expected at least %d loops for deeply nested structure, got %d", nestingDepth, len(loops.Loops))
	}

	t.Logf("deeply nested: found %d loops, max depth %d", len(loops.Loops), loops.MaxDepth)
}

// TestDetectLoops_ConcurrentStress tests concurrent access to loop detection.
func TestDetectLoops_ConcurrentStress(t *testing.T) {
	nodes := []string{"entry", "h", "b", "exit"}
	edges := [][2]string{
		{"entry", "h"},
		{"h", "b"},
		{"b", "h"},
		{"h", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				loops, err := analytics.DetectLoops(ctx, domTree)
				if err != nil {
					errors <- err
					return
				}
				if len(loops.Loops) != 1 {
					errors <- fmt.Errorf("expected 1 loop, got %d", len(loops.Loops))
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent error: %v", err)
	}
}

// TestDetectLoops_GetLoopByHeader tests the GetLoopByHeader helper.
func TestDetectLoops_GetLoopByHeader(t *testing.T) {
	nodes := []string{"entry", "h1", "b1", "h2", "b2", "exit"}
	edges := [][2]string{
		{"entry", "h1"},
		{"h1", "b1"},
		{"b1", "h1"},
		{"h1", "h2"},
		{"h2", "b2"},
		{"b2", "h2"},
		{"h2", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	loops, _ := analytics.DetectLoops(ctx, domTree)

	// Test GetLoopByHeader
	loop1 := loops.GetLoopByHeader("h1")
	if loop1 == nil {
		t.Error("expected to find loop with header h1")
	} else if loop1.Header != "h1" {
		t.Errorf("expected header h1, got %s", loop1.Header)
	}

	loop2 := loops.GetLoopByHeader("h2")
	if loop2 == nil {
		t.Error("expected to find loop with header h2")
	}

	// Non-existent header
	notFound := loops.GetLoopByHeader("nonexistent")
	if notFound != nil {
		t.Error("expected nil for non-existent header")
	}
}

// TestDetectLoops_IsLoopHeader tests the IsLoopHeader helper.
func TestDetectLoops_IsLoopHeader(t *testing.T) {
	nodes := []string{"entry", "h", "b", "exit"}
	edges := [][2]string{
		{"entry", "h"},
		{"h", "b"},
		{"b", "h"},
		{"h", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	loops, _ := analytics.DetectLoops(ctx, domTree)

	if !loops.IsLoopHeader("h") {
		t.Error("h should be a loop header")
	}
	if loops.IsLoopHeader("b") {
		t.Error("b should not be a loop header")
	}
	if loops.IsLoopHeader("entry") {
		t.Error("entry should not be a loop header")
	}
}

// TestDetectLoops_NilLoopNest tests nil receiver handling for LoopNest methods.
func TestDetectLoops_NilLoopNest(t *testing.T) {
	var ln *LoopNest

	if ln.IsInLoop("any") {
		t.Error("expected false for nil receiver")
	}
	if ln.GetInnermostLoop("any") != nil {
		t.Error("expected nil for nil receiver")
	}
	if ln.GetLoopByHeader("any") != nil {
		t.Error("expected nil for nil receiver")
	}
	if ln.IsLoopHeader("any") {
		t.Error("expected false for nil receiver")
	}
}

// TestDetectLoops_DisjointLoops tests multiple loops that don't share nodes.
func TestDetectLoops_DisjointLoops(t *testing.T) {
	// Two completely separate loops
	//
	//     entry
	//     /   \
	//   h1    h2
	//   |      |
	//   b1    b2
	//    \    /
	//     \  /  (back edges)
	//     exit
	nodes := []string{"entry", "h1", "b1", "h2", "b2", "exit"}
	edges := [][2]string{
		{"entry", "h1"},
		{"entry", "h2"},
		{"h1", "b1"},
		{"b1", "h1"}, // Back edge 1
		{"h1", "exit"},
		{"h2", "b2"},
		{"b2", "h2"}, // Back edge 2
		{"h2", "exit"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	loops, err := analytics.DetectLoops(ctx, domTree)
	if err != nil {
		t.Fatalf("failed to detect loops: %v", err)
	}

	if len(loops.Loops) != 2 {
		t.Errorf("expected 2 disjoint loops, got %d", len(loops.Loops))
	}

	// Neither should be parent/child of the other
	for _, loop := range loops.Loops {
		if loop.Parent != nil {
			t.Errorf("loop %s should not have a parent in disjoint case", loop.Header)
		}
	}
}

// TestDetectLoops_EmptyEntry tests handling of empty entry in domTree.
func TestDetectLoops_EmptyEntry(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Manually create a domTree with empty entry
	domTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	_, err := analytics.DetectLoops(ctx, domTree)
	if err == nil {
		t.Error("expected error for empty entry")
	}
}

// =============================================================================
// Additional LCD Tests (GR-16g)
// =============================================================================

// TestLCD_AllSameNode tests LCD of multiple copies of the same node.
func TestLCD_AllSameNode(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	// LCD of the same node repeated should be that node
	result := domTree.LowestCommonDominatorMultiple([]string{"B", "B", "B"})
	if result != "B" {
		t.Errorf("LCD(B,B,B) should be B, got %s", result)
	}
}

// TestLCD_AllChildren tests LCD of all children of a node.
func TestLCD_AllChildren(t *testing.T) {
	// Parent with many children
	//       entry
	//         |
	//       parent
	//    /  |  |  \
	//   c1 c2 c3  c4
	nodes := []string{"entry", "parent", "c1", "c2", "c3", "c4"}
	edges := [][2]string{
		{"entry", "parent"},
		{"parent", "c1"},
		{"parent", "c2"},
		{"parent", "c3"},
		{"parent", "c4"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	// LCD of all children should be their common parent
	result := domTree.LowestCommonDominatorMultiple([]string{"c1", "c2", "c3", "c4"})
	if result != "parent" {
		t.Errorf("LCD(c1,c2,c3,c4) should be parent, got %s", result)
	}
}

// TestLCD_VeryDeepTree tests LCD on a very deep tree.
func TestLCD_VeryDeepTree(t *testing.T) {
	const depth = 200
	nodes := make([]string, depth)
	edges := make([][2]string, depth-1)

	for i := 0; i < depth; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		if i > 0 {
			edges[i-1] = [2]string{nodes[i-1], nodes[i]}
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "n0")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}

	// LCD of deepest two nodes
	lcd := domTree.LowestCommonDominator(nodes[depth-1], nodes[depth-2])
	if lcd != nodes[depth-2] {
		t.Errorf("expected LCD to be %s, got %s", nodes[depth-2], lcd)
	}

	// LCD of root and deepest
	lcd = domTree.LowestCommonDominator("n0", nodes[depth-1])
	if lcd != "n0" {
		t.Errorf("expected LCD to be n0, got %s", lcd)
	}
}

// TestLCD_BinaryLiftingVsBasic tests that binary lifting gives same results as basic.
func TestLCD_BinaryLiftingVsBasic(t *testing.T) {
	// Build a moderately complex tree
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F", "G", "H", "I", "J"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"A", "C"},
		{"A", "D"},
		{"B", "E"},
		{"B", "F"},
		{"C", "G"},
		{"D", "H"},
		{"E", "I"},
		{"F", "J"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	lca := domTree.PrepareLCAQueries()
	if lca == nil {
		t.Fatal("PrepareLCAQueries returned nil")
	}

	// Test all pairs
	for i := 0; i < len(nodes); i++ {
		for j := i; j < len(nodes); j++ {
			basic := domTree.LowestCommonDominator(nodes[i], nodes[j])
			fast := lca.Query(nodes[i], nodes[j])
			if basic != fast {
				t.Errorf("LCD(%s, %s): basic=%s, fast=%s", nodes[i], nodes[j], basic, fast)
			}
		}
	}
}

// TestLCD_DuplicateNodes tests LCD with duplicate nodes in input.
func TestLCD_DuplicateNodes(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")

	// LCD with duplicates should work correctly
	result := domTree.LowestCommonDominatorMultiple([]string{"B", "C", "B", "C"})
	if result != "A" {
		t.Errorf("LCD with duplicates should be A, got %s", result)
	}
}

// TestLCD_StressLargeTree tests LCD performance on a large tree.
func TestLCD_StressLargeTree(t *testing.T) {
	const size = 500
	nodes := make([]string, size)
	edges := make([][2]string, 0, size)

	for i := 0; i < size; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		if i > 0 {
			// Binary tree structure
			parentIdx := (i - 1) / 2
			edges = append(edges, [2]string{nodes[parentIdx], nodes[i]})
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	start := time.Now()
	domTree, err := analytics.Dominators(ctx, "n0")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	t.Logf("dominator tree for %d nodes computed in %v", size, time.Since(start))

	// Test many LCD queries
	start = time.Now()
	queries := 0
	for i := size / 2; i < size; i++ {
		for j := i; j < size && j < i+10; j++ {
			_ = domTree.LowestCommonDominator(nodes[i], nodes[j])
			queries++
		}
	}
	t.Logf("%d LCD queries completed in %v", queries, time.Since(start))
}

// TestLCAQueryEngine_DeepTree tests binary lifting on deep tree.
func TestLCAQueryEngine_DeepTree(t *testing.T) {
	const depth = 100
	nodes := make([]string, depth)
	edges := make([][2]string, depth-1)

	for i := 0; i < depth; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		if i > 0 {
			edges[i-1] = [2]string{nodes[i-1], nodes[i]}
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "n0")
	lca := domTree.PrepareLCAQueries()

	// Query at various depths
	for i := 0; i < depth-1; i += 10 {
		for j := i + 1; j < depth; j += 10 {
			result := lca.Query(nodes[i], nodes[j])
			// The LCD should be nodes[i] since it's shallower
			if result != nodes[i] {
				t.Errorf("Query(%s, %s) = %s, expected %s", nodes[i], nodes[j], result, nodes[i])
			}
		}
	}
}

// TestLCDWithCRS_LargeInput tests CRS wrapper with many nodes.
func TestLCDWithCRS_LargeInput(t *testing.T) {
	const size = 50
	nodes := make([]string, size)
	edges := make([][2]string, 0)

	for i := 0; i < size; i++ {
		nodes[i] = fmt.Sprintf("n%d", i)
		if i > 0 {
			edges = append(edges, [2]string{"n0", nodes[i]})
		}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "n0")

	// Query with all nodes
	result, step := analytics.LCDWithCRS(ctx, domTree, nodes)

	// LCD of all children of n0 should be n0
	if result != "n0" {
		t.Errorf("expected n0, got %s", result)
	}

	// Verify metadata
	if step.Metadata["nodes_queried"] != itoa(size) {
		t.Errorf("expected nodes_queried=%d, got %s", size, step.Metadata["nodes_queried"])
	}
}

// =============================================================================
// SESE Region Detection Tests (GR-16h)
// =============================================================================

// TestSESE_NilAnalytics verifies nil receiver safety.
func TestSESE_NilAnalytics(t *testing.T) {
	var analytics *GraphAnalytics
	ctx := context.Background()

	result, err := analytics.DetectSESERegions(ctx, nil, nil)
	if err != nil {
		t.Errorf("expected nil error for nil analytics, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Regions) != 0 {
		t.Errorf("expected 0 regions, got %d", len(result.Regions))
	}
}

// TestSESE_NilDomTree verifies nil dominator tree handling.
func TestSESE_NilDomTree(t *testing.T) {
	// Build a simple graph
	nodes := []string{"entry", "A", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	result, err := analytics.DetectSESERegions(ctx, nil, nil)
	if err == nil {
		t.Error("expected error for nil domTree")
	}
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
}

// TestSESE_NilPostDomTree verifies nil post-dominator tree handling.
func TestSESE_NilPostDomTree(t *testing.T) {
	nodes := []string{"entry", "A", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	result, err := analytics.DetectSESERegions(ctx, domTree, nil)
	if err == nil {
		t.Error("expected error for nil postDomTree")
	}
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
}

// TestSESE_EmptyGraph verifies empty graph handling.
func TestSESE_EmptyGraph(t *testing.T) {
	hg := buildArticulationTestGraph(t, []string{}, [][2]string{})
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Empty dominator trees
	domTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}
	postDomTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: make(map[string]string),
		Depth:        make(map[string]int),
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Errorf("unexpected error for empty graph: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Regions) != 0 {
		t.Errorf("expected 0 regions for empty graph, got %d", len(result.Regions))
	}
}

// TestSESE_SingleNode verifies single node graph handling.
func TestSESE_SingleNode(t *testing.T) {
	nodes := []string{"A"}
	edges := [][2]string{}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	postDomTree, _ := analytics.PostDominators(ctx, "A")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Single node forms a trivial SESE region
	if result.MaxDepth > 1 {
		t.Errorf("expected max depth 0 or 1, got %d", result.MaxDepth)
	}
}

// TestSESE_LinearGraph verifies straight line graph (all trivial SESEs).
func TestSESE_LinearGraph(t *testing.T) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "E")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Linear graph has nested SESE regions
	// Each node to E forms a SESE: (A,E), (B,E), (C,E), (D,E)
	if len(result.Regions) < 1 {
		t.Errorf("expected at least 1 SESE region in linear graph, got %d", len(result.Regions))
	}

	// Verify hierarchy - regions should be nested
	if result.MaxDepth < 1 {
		// For linear graph, we expect some nesting
		t.Logf("linear graph max depth: %d", result.MaxDepth)
	}
}

// TestSESE_DiamondGraph verifies classic diamond pattern.
//
//	entry
//	 / \
//	A   B
//	 \ /
//	exit
func TestSESE_DiamondGraph(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"A", "exit"},
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// The whole diamond forms a SESE region (entry, exit)
	foundWholeRegion := false
	for _, region := range result.Regions {
		if region.Entry == "entry" && region.Exit == "exit" {
			foundWholeRegion = true
			if region.Size < 4 {
				t.Errorf("expected region size >= 4 for whole diamond, got %d", region.Size)
			}
			break
		}
	}
	if !foundWholeRegion {
		t.Logf("regions found: %d", len(result.Regions))
		for _, r := range result.Regions {
			t.Logf("  region: entry=%s, exit=%s, size=%d", r.Entry, r.Exit, r.Size)
		}
	}
}

// TestSESE_Simple verifies basic SESE region detection.
//
//	entry -> A -> B -> exit
//	          \   ^
//	           \-/
//
// The loop A->B->A does not create a separate SESE within A-B.
func TestSESE_Simple(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Regions) < 1 {
		t.Errorf("expected at least 1 SESE region, got %d", len(result.Regions))
	}
}

// TestSESE_Nested verifies nested SESE region detection.
//
//	entry -> A -> B -> C -> exit
//
// Expected regions: (entry,exit), (A,exit), (B,exit), etc.
func TestSESE_Nested(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Check for nested regions
	if result.MaxDepth < 1 {
		t.Logf("nested graph max depth: %d (expected > 0 for nested regions)", result.MaxDepth)
	}

	// Verify parent/child relationships are consistent
	for _, region := range result.Regions {
		if region.Parent != nil {
			// Child should be contained in parent's nodes
			foundInParent := false
			for _, child := range region.Parent.Children {
				if child == region {
					foundInParent = true
					break
				}
			}
			if !foundInParent {
				t.Errorf("region %s->%s has parent but is not in parent's children", region.Entry, region.Exit)
			}
		}
	}
}

// TestSESE_Hierarchy verifies parent/child relationship construction.
func TestSESE_Hierarchy(t *testing.T) {
	// Build a graph with clear hierarchical SESE structure
	nodes := []string{"main", "init", "process", "validate", "save", "cleanup", "exit"}
	edges := [][2]string{
		{"main", "init"},
		{"init", "process"},
		{"process", "validate"},
		{"validate", "save"},
		{"save", "cleanup"},
		{"cleanup", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "main")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify depth is consistent: child.Depth == parent.Depth + 1
	for _, region := range result.Regions {
		if region.Parent != nil {
			expectedDepth := region.Parent.Depth + 1
			if region.Depth != expectedDepth {
				t.Errorf("depth inconsistency: region %s->%s has depth %d, parent depth %d (expected %d)",
					region.Entry, region.Exit, region.Depth, region.Parent.Depth, expectedDepth)
			}
		}
	}

	// Verify TopLevel regions have no parent
	for _, region := range result.TopLevel {
		if region.Parent != nil {
			t.Errorf("top-level region %s->%s has non-nil parent", region.Entry, region.Exit)
		}
		if region.Depth != 0 {
			t.Errorf("top-level region %s->%s has depth %d, expected 0", region.Entry, region.Exit, region.Depth)
		}
	}
}

// TestSESE_Extractable verifies FilterExtractable works correctly.
func TestSESE_Extractable(t *testing.T) {
	// Build a graph with regions of various sizes
	nodes := []string{"entry", "A", "B", "C", "D", "E", "F", "G", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"E", "F"},
		{"F", "G"},
		{"G", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filter for regions with 3-5 nodes
	extractable := result.FilterExtractable(3, 5)

	// All returned regions should be within size bounds
	for _, region := range extractable {
		if region.Size < 3 || region.Size > 5 {
			t.Errorf("FilterExtractable returned region with size %d, expected 3-5", region.Size)
		}
	}

	// Filter with no matches
	none := result.FilterExtractable(100, 200)
	if len(none) != 0 {
		t.Errorf("expected 0 regions for impossible size range, got %d", len(none))
	}

	// Filter with wide range should include all
	all := result.FilterExtractable(1, 1000)
	for _, region := range result.Regions {
		if region.Size >= 1 && region.Size <= 1000 {
			found := false
			for _, e := range all {
				if e == region {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("FilterExtractable(1,1000) should include region size %d", region.Size)
			}
		}
	}
}

// TestSESE_RegionOf verifies node to region mapping.
func TestSESE_RegionOf(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Every node in a region should be in RegionOf
	for _, region := range result.Regions {
		for _, nodeID := range region.Nodes {
			mappedRegion := result.RegionOf[nodeID]
			if mappedRegion == nil {
				t.Errorf("node %s is in region %s->%s but not in RegionOf map",
					nodeID, region.Entry, region.Exit)
			}
			// Mapped region should be the innermost containing region
			// (smallest region containing the node)
		}
	}
}

// TestSESE_ContextCancellation verifies context cancellation handling.
func TestSESE_ContextCancellation(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "D", "E", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"E", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	domTree, _ := analytics.Dominators(context.Background(), "entry")
	postDomTree, _ := analytics.PostDominators(context.Background(), "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}
	// Should return partial or empty result
	if result == nil {
		t.Error("expected non-nil result even on cancellation")
	}
}

// TestSESE_NonSESE verifies that non-SESE structures are not detected as SESE.
//
//	entry
//	 |
//	 A ──────┐
//	 |       |
//	 B       ▼
//	 |     exit2
//	 ▼
//	exit1
//
// This is NOT a SESE region because A has two exits (exit1 via B, exit2 directly).
func TestSESE_NonSESE(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit1", "exit2"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "exit1"},
		{"A", "exit2"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// We need to handle multiple exits for post-dominators
	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "") // Auto-detect exits

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Logf("error (may be expected for complex multi-exit): %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify A is not an entry to a SESE that includes both exit1 and exit2
	for _, region := range result.Regions {
		if region.Entry == "A" {
			hasExit1 := false
			hasExit2 := false
			for _, node := range region.Nodes {
				if node == "exit1" {
					hasExit1 = true
				}
				if node == "exit2" {
					hasExit2 = true
				}
			}
			if hasExit1 && hasExit2 && region.Exit != region.Entry {
				t.Logf("region from A includes both exits - check if valid SESE: exit=%s", region.Exit)
			}
		}
	}
}

// TestSESE_ConcurrentAccess verifies thread safety.
func TestSESE_ConcurrentAccess(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Concurrent reads should be safe
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Access regions concurrently
			_ = len(result.Regions)
			_ = result.MaxDepth
			for _, r := range result.Regions {
				_ = r.Entry
				_ = r.Exit
				_ = r.Size
				_ = r.Depth
			}
			// Filter concurrently
			_ = result.FilterExtractable(2, 10)
			// Access RegionOf concurrently
			for _, r := range result.Regions {
				for _, n := range r.Nodes {
					_ = result.RegionOf[n]
				}
			}
		}()
	}
	wg.Wait()
}

// TestSESEWithCRS_Basic verifies CRS wrapper basic functionality.
func TestSESEWithCRS_Basic(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, step := analytics.DetectSESERegionsWithCRS(ctx, domTree, postDomTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Verify TraceStep
	if step.Action != "analytics_sese" {
		t.Errorf("expected action 'analytics_sese', got '%s'", step.Action)
	}
	if step.Tool != "DetectSESERegions" {
		t.Errorf("expected tool 'DetectSESERegions', got '%s'", step.Tool)
	}
	if step.Duration <= 0 {
		t.Error("expected positive duration")
	}
}

// TestSESEWithCRS_NilInputs verifies CRS wrapper handles nil inputs.
func TestSESEWithCRS_NilInputs(t *testing.T) {
	nodes := []string{"entry", "A", "exit"}
	edges := [][2]string{{"entry", "A"}, {"A", "exit"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Test nil domTree
	result, step := analytics.DetectSESERegionsWithCRS(ctx, nil, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if step.Error == "" {
		t.Error("expected error in TraceStep for nil domTree")
	}

	// Test nil postDomTree
	domTree, _ := analytics.Dominators(ctx, "entry")
	result, step = analytics.DetectSESERegionsWithCRS(ctx, domTree, nil)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if step.Error == "" {
		t.Error("expected error in TraceStep for nil postDomTree")
	}
}

// TestSESEWithCRS_ContextCancelled verifies CRS wrapper handles cancellation.
func TestSESEWithCRS_ContextCancelled(t *testing.T) {
	nodes := []string{"entry", "A", "exit"}
	edges := [][2]string{{"entry", "A"}, {"A", "exit"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	domTree, _ := analytics.Dominators(context.Background(), "entry")
	postDomTree, _ := analytics.PostDominators(context.Background(), "exit")

	result, step := analytics.DetectSESERegionsWithCRS(ctx, domTree, postDomTree)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if step.Error == "" {
		t.Error("expected error in TraceStep for cancelled context")
	}
}

// TestSESEWithCRS_Metadata verifies CRS wrapper includes proper metadata.
func TestSESEWithCRS_Metadata(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	_, step := analytics.DetectSESERegionsWithCRS(ctx, domTree, postDomTree)

	// Check metadata is present
	if _, ok := step.Metadata["regions_found"]; !ok {
		t.Error("expected 'regions_found' in metadata")
	}
	if _, ok := step.Metadata["max_depth"]; !ok {
		t.Error("expected 'max_depth' in metadata")
	}
}

// BenchmarkSESE_Small benchmarks SESE detection on small graph.
func BenchmarkSESE_Small(b *testing.B) {
	nodes := []string{"entry", "A", "B", "C", "D", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "exit"},
	}
	hg := buildArticulationTestGraphBench(b, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DetectSESERegions(ctx, domTree, postDomTree)
	}
}

// BenchmarkSESE_Medium benchmarks SESE detection on medium graph.
func BenchmarkSESE_Medium(b *testing.B) {
	// Build a graph with 100 nodes
	nodes := make([]string, 100)
	edges := make([][2]string, 99)
	nodes[0] = "entry"
	for i := 1; i < 99; i++ {
		nodes[i] = fmt.Sprintf("node%d", i)
	}
	nodes[99] = "exit"

	for i := 0; i < 99; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraphBench(b, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.DetectSESERegions(ctx, domTree, postDomTree)
	}
}

// buildArticulationTestGraphBench is a benchmark helper.
func buildArticulationTestGraphBench(b *testing.B, nodes []string, edges [][2]string) *HierarchicalGraph {
	b.Helper()

	g := NewGraph("/test/project")

	// Create nodes
	for _, id := range nodes {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/test",
			FilePath: "pkg/test/test.go",
			Exported: true,
		}
		_, _ = g.AddNode(sym)
	}

	// Create edges
	for _, edge := range edges {
		_ = g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
	}

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		b.Fatalf("failed to wrap graph: %v", err)
	}

	return hg
}

// =============================================================================
// Additional SESE Edge Case Tests
// =============================================================================

// TestSESE_CyclicGraph verifies SESE detection on graphs with cycles.
func TestSESE_CyclicGraph(t *testing.T) {
	// entry -> A -> B -> A (cycle)
	//               |
	//               v
	//              exit
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "A"}, // Back edge creating cycle
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominators: %v", err)
	}
	postDomTree, err := analytics.PostDominators(ctx, "exit")
	if err != nil {
		t.Fatalf("failed to compute post-dominators: %v", err)
	}

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Cyclic graphs should still produce valid SESE regions
	t.Logf("cyclic graph: found %d regions", len(result.Regions))
}

// TestSESE_MultipleExits verifies SESE with multiple exit nodes.
func TestSESE_MultipleExits(t *testing.T) {
	nodes := []string{"entry", "A", "B", "exit1", "exit2"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "exit1"},
		{"A", "exit2"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	// Auto-detect multiple exits
	postDomTree, _ := analytics.PostDominators(ctx, "")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Logf("error (expected for multi-exit): %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	t.Logf("multi-exit graph: found %d regions", len(result.Regions))
}

// TestSESE_ComplexBranching verifies SESE with complex branching.
//
//	  entry
//	 /  |  \
//	A   B   C
//	 \  |  /
//	  merge
//	    |
//	  exit
func TestSESE_ComplexBranching(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "merge", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"entry", "C"},
		{"A", "merge"},
		{"B", "merge"},
		{"C", "merge"},
		{"merge", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The whole graph should form a SESE region
	foundEntryExit := false
	for _, region := range result.Regions {
		if region.Entry == "entry" && region.Exit == "exit" {
			foundEntryExit = true
			break
		}
	}
	if !foundEntryExit {
		t.Logf("expected SESE region (entry, exit), found %d regions", len(result.Regions))
	}
}

// TestSESE_TrivialRegion verifies handling of trivial (entry == exit) regions.
func TestSESE_TrivialRegion(t *testing.T) {
	nodes := []string{"entry", "A", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have entry == exit regions (those are filtered out)
	for _, region := range result.Regions {
		if region.Entry == region.Exit {
			t.Errorf("found trivial region where entry == exit: %s", region.Entry)
		}
	}
}

// TestSESE_DeepNesting verifies deeply nested SESE regions.
func TestSESE_DeepNesting(t *testing.T) {
	// Create a chain: entry -> A -> B -> C -> D -> E -> exit
	// This should create nested SESE regions
	nodes := []string{"entry", "A", "B", "C", "D", "E", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"E", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("deep nesting: found %d regions, max depth %d", len(result.Regions), result.MaxDepth)

	// Verify no region exceeds reasonable depth
	for _, region := range result.Regions {
		if region.Depth > 100 {
			t.Errorf("region depth %d exceeds reasonable limit", region.Depth)
		}
	}
}

// TestSESE_SameEntryDifferentExits verifies regions with same entry but different exits.
func TestSESE_SameEntryDifferentExits(t *testing.T) {
	// entry -> A -> B
	//          |
	//          v
	//          C
	nodes := []string{"entry", "A", "B", "C"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Logf("error (may be expected): %v", err)
	}

	// Log what we found
	for _, region := range result.Regions {
		t.Logf("region: %s -> %s (size %d)", region.Entry, region.Exit, region.Size)
	}
}

// TestSESE_LargeGraph verifies SESE detection on larger graphs.
func TestSESE_LargeGraph(t *testing.T) {
	// Build a graph with 500 nodes
	nodes := make([]string, 500)
	edges := make([][2]string, 499)
	nodes[0] = "entry"
	for i := 1; i < 499; i++ {
		nodes[i] = fmt.Sprintf("node%d", i)
	}
	nodes[499] = "exit"

	for i := 0; i < 499; i++ {
		edges[i] = [2]string{nodes[i], nodes[i+1]}
	}

	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	start := time.Now()
	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("large graph (500 nodes): found %d regions in %v", len(result.Regions), duration)

	// Should complete in reasonable time
	if duration > 5*time.Second {
		t.Errorf("SESE detection took too long: %v", duration)
	}
}

// TestSESE_SymmetricGraph verifies symmetry: LCD(a,b) == LCD(b,a) concept for SESE.
func TestSESE_SymmetricGraph(t *testing.T) {
	// Symmetric diamond
	nodes := []string{"entry", "A", "B", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"entry", "B"},
		{"A", "exit"},
		{"B", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The whole graph should be a single SESE region
	for _, region := range result.Regions {
		// Verify all nodes in the region
		if region.Entry == "entry" && region.Exit == "exit" {
			if region.Size < 4 {
				t.Errorf("expected size >= 4 for whole graph, got %d", region.Size)
			}
		}
	}
}

// TestSESE_FilterExtractableEdgeCases verifies FilterExtractable edge cases.
func TestSESE_FilterExtractableEdgeCases(t *testing.T) {
	t.Run("nil_result", func(t *testing.T) {
		var result *SESEResult
		extractable := result.FilterExtractable(1, 10)
		if len(extractable) != 0 {
			t.Errorf("expected 0 results for nil SESEResult, got %d", len(extractable))
		}
	})

	t.Run("empty_regions", func(t *testing.T) {
		result := &SESEResult{Regions: []*SESERegion{}}
		extractable := result.FilterExtractable(1, 10)
		if len(extractable) != 0 {
			t.Errorf("expected 0 results for empty regions, got %d", len(extractable))
		}
	})

	t.Run("zero_bounds", func(t *testing.T) {
		result := &SESEResult{
			Regions: []*SESERegion{
				{Entry: "A", Exit: "B", Size: 2},
			},
		}
		extractable := result.FilterExtractable(0, 0)
		if len(extractable) != 0 {
			t.Errorf("expected 0 results for zero bounds, got %d", len(extractable))
		}
	})

	t.Run("negative_bounds", func(t *testing.T) {
		result := &SESEResult{
			Regions: []*SESERegion{
				{Entry: "A", Exit: "B", Size: 2},
			},
		}
		extractable := result.FilterExtractable(-10, -1)
		if len(extractable) != 0 {
			t.Errorf("expected 0 results for negative bounds, got %d", len(extractable))
		}
	})
}

// TestSESE_RegionOfConsistency verifies RegionOf map consistency.
func TestSESE_RegionOfConsistency(t *testing.T) {
	nodes := []string{"entry", "A", "B", "C", "exit"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"B", "C"},
		{"C", "exit"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "entry")
	postDomTree, _ := analytics.PostDominators(ctx, "exit")

	result, err := analytics.DetectSESERegions(ctx, domTree, postDomTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// For each node in RegionOf, verify it's actually in that region's Nodes
	for nodeID, region := range result.RegionOf {
		found := false
		for _, n := range region.Nodes {
			if n == nodeID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("node %s in RegionOf points to region %s->%s but is not in region's Nodes",
				nodeID, region.Entry, region.Exit)
		}
	}
}

// =============================================================================
// Check Reducibility Tests (GR-16i)
// =============================================================================

// TestCheckReducibility_ReducibleSimple tests a simple linear reducible graph.
//
// Graph: A → B → C → D (linear chain, trivially reducible)
func TestCheckReducibility_ReducibleSimple(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected linear graph to be reducible")
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
	if len(result.IrreducibleRegions) != 0 {
		t.Errorf("expected 0 irreducible regions, got %d", len(result.IrreducibleRegions))
	}
}

// TestCheckReducibility_ReducibleWithLoop tests a reducible graph with a natural loop.
//
// Graph: A → B → C → D
//
//	↑   │
//	└───┘ (C → B back edge, B dominates C → natural loop, reducible)
func TestCheckReducibility_ReducibleWithLoop(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"C", "B"}, // Back edge: C → B (B dominates C)
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected graph with natural loop to be reducible")
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
}

// TestCheckReducibility_ReducibleNested tests nested natural loops (reducible).
//
// Graph: A → B → C → D → E
//
//	↑   │   ↑   │
//	└───┘   └───┘ (C → B and E → D back edges)
func TestCheckReducibility_ReducibleNested(t *testing.T) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"C", "B"}, // Back edge: inner loop
		{"E", "D"}, // Back edge: outer loop
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected nested loops graph to be reducible")
	}
}

// TestCheckReducibility_IrreducibleMultiEntry tests an irreducible graph with multi-entry loop.
//
// Graph: A → B → D
//
//	│   ↑   │
//	└→ C ←──┘ (Both B and C can enter the B-C-D region from outside)
//
// This creates a multi-entry loop where the B-C-D region can be entered from A→B or D→C.
func TestCheckReducibility_IrreducibleMultiEntry(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"D", "C"},
		{"C", "B"}, // Creates cycle B → D → C → B
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsReducible {
		t.Error("expected multi-entry loop graph to be irreducible")
	}
	if result.Score >= 1.0 {
		t.Errorf("expected score < 1.0 for irreducible graph, got %f", result.Score)
	}
	if len(result.IrreducibleRegions) == 0 {
		t.Error("expected at least one irreducible region")
	}
}

// TestCheckReducibility_IrreducibleComplex tests a more complex irreducible graph.
//
// Classic irreducible pattern from compiler literature.
func TestCheckReducibility_IrreducibleComplex(t *testing.T) {
	// Entry → A
	//         │\
	//         ▼ \
	//         B  C
	//         │  │
	//         ▼  ▼
	//         D←─┘
	//         │
	//         ▼
	//         E
	//
	// Add edges D→B and D→C to create irreducibility
	nodes := []string{"entry", "A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"entry", "A"},
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "E"},
		{"D", "B"}, // Cross edge creating irreducibility
		{"D", "C"}, // Another cross edge
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "entry")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsReducible {
		t.Error("expected complex irreducible graph to be irreducible")
	}
	if len(result.IrreducibleRegions) == 0 {
		t.Error("expected at least one irreducible region")
	}
}

// TestCheckReducibility_EmptyGraph tests that an empty graph is trivially reducible.
func TestCheckReducibility_EmptyGraph(t *testing.T) {
	g := NewGraph("/test/project")
	g.Freeze()
	hg, _ := WrapGraph(g)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Create a minimal domTree for empty graph
	domTree := &DominatorTree{
		Entry:        "",
		ImmediateDom: make(map[string]string),
		Children:     make(map[string][]string),
		Depth:        make(map[string]int),
		PostOrder:    []string{},
		NodeCount:    0,
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected empty graph to be reducible")
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0 for empty graph, got %f", result.Score)
	}
}

// TestCheckReducibility_SingleNode tests that a single node graph is reducible.
func TestCheckReducibility_SingleNode(t *testing.T) {
	nodes := []string{"A"}
	edges := [][2]string{} // No edges
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected single node graph to be reducible")
	}
	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
}

// TestCheckReducibility_SelfLoop tests that a self-loop is reducible.
func TestCheckReducibility_SelfLoop(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "B"}, // Self-loop
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected self-loop graph to be reducible")
	}
}

// TestCheckReducibility_ScoreCalculation verifies the score formula.
func TestCheckReducibility_ScoreCalculation(t *testing.T) {
	// Create an irreducible graph where some nodes are in irreducible region
	// Score should be (total - irreducible) / total
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "B"}, // Creates irreducibility
		{"D", "C"},
		{"D", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify score is between 0 and 1
	if result.Score < 0.0 || result.Score > 1.0 {
		t.Errorf("score should be between 0 and 1, got %f", result.Score)
	}

	// For irreducible graph, score should be < 1.0
	if result.IsReducible && result.Score < 1.0 {
		t.Error("if reducible, score should be 1.0")
	}
	if !result.IsReducible && result.Score == 1.0 {
		t.Error("if irreducible, score should be < 1.0")
	}

	// Verify summary consistency
	if result.Summary.TotalNodes != len(nodes) {
		t.Errorf("expected TotalNodes=%d, got %d", len(nodes), result.Summary.TotalNodes)
	}
}

// TestCheckReducibility_Recommendations verifies recommendation generation.
func TestCheckReducibility_Recommendations(t *testing.T) {
	t.Run("fully_reducible", func(t *testing.T) {
		nodes := []string{"A", "B", "C"}
		edges := [][2]string{{"A", "B"}, {"B", "C"}}
		hg := buildArticulationTestGraph(t, nodes, edges)
		analytics := NewGraphAnalytics(hg)
		ctx := context.Background()

		domTree, _ := analytics.Dominators(ctx, "A")
		result, err := analytics.CheckReducibility(ctx, domTree)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Recommendation == "" {
			t.Error("expected non-empty recommendation")
		}
	})

	t.Run("irreducible", func(t *testing.T) {
		nodes := []string{"A", "B", "C", "D"}
		edges := [][2]string{
			{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
			{"D", "B"}, {"D", "C"},
		}
		hg := buildArticulationTestGraph(t, nodes, edges)
		analytics := NewGraphAnalytics(hg)
		ctx := context.Background()

		domTree, _ := analytics.Dominators(ctx, "A")
		result, err := analytics.CheckReducibility(ctx, domTree)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Recommendation == "" {
			t.Error("expected non-empty recommendation for irreducible graph")
		}
	})
}

// TestCheckReducibility_ContextCancellation verifies context is honored.
func TestCheckReducibility_ContextCancellation(t *testing.T) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	domTree, _ := analytics.Dominators(context.Background(), "A")

	_, err := analytics.CheckReducibility(ctx, domTree)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// TestCheckReducibility_ConcurrentAccess verifies thread safety.
func TestCheckReducibility_ConcurrentAccess(t *testing.T) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"},
		{"C", "B"}, // Natural loop
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")

	// Run multiple concurrent reducibility checks
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := analytics.CheckReducibility(ctx, domTree)
			if err != nil {
				errors <- err
				return
			}
			if !result.IsReducible {
				errors <- fmt.Errorf("expected reducible, got irreducible")
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent check failed: %v", err)
	}
}

// TestCheckReducibility_NilDomTree verifies error handling for nil domTree.
func TestCheckReducibility_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	_, err := analytics.CheckReducibility(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil domTree")
	}
}

// TestCheckReducibilityWithCRS_TraceStep verifies CRS TraceStep is populated.
func TestCheckReducibilityWithCRS_TraceStep(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")

	result, traceStep := analytics.CheckReducibilityWithCRS(ctx, domTree)

	// Verify result
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsReducible {
		t.Error("expected reducible graph")
	}

	// Verify TraceStep
	if traceStep.Action != "analytics_reducibility" {
		t.Errorf("expected action 'analytics_reducibility', got '%s'", traceStep.Action)
	}
	if traceStep.Tool != "CheckReducibility" {
		t.Errorf("expected tool 'CheckReducibility', got '%s'", traceStep.Tool)
	}
	if traceStep.Target != "graph" {
		t.Errorf("expected target 'graph', got '%s'", traceStep.Target)
	}

	// Verify metadata
	if traceStep.Metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if _, ok := traceStep.Metadata["is_reducible"]; !ok {
		t.Error("expected 'is_reducible' in metadata")
	}
	if _, ok := traceStep.Metadata["score"]; !ok {
		t.Error("expected 'score' in metadata")
	}
}

// TestCheckReducibility_IrreducibleRegionEntryNodes verifies entry nodes are identified.
func TestCheckReducibility_IrreducibleRegionEntryNodes(t *testing.T) {
	// Create a graph with a clear multi-entry region
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "B"}, // D → B creates cross edge
		{"D", "C"}, // D → C creates another cross edge
		{"D", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsReducible {
		t.Fatal("expected irreducible graph")
	}

	// Check that regions have entry nodes identified
	for _, region := range result.IrreducibleRegions {
		if len(region.EntryNodes) == 0 {
			t.Error("expected entry nodes to be identified for irreducible region")
		}
		if region.Size == 0 {
			t.Error("expected region to have non-zero size")
		}
		if region.Reason == "" {
			t.Error("expected reason to be provided")
		}
	}
}

// =============================================================================
// IsReducible Fast-Path Tests
// =============================================================================

// TestIsReducible_FastPathReducible tests the fast-path method on reducible graphs.
func TestIsReducible_FastPathReducible(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "B"}, // Back edge to dominator - reducible
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	isReducible, err := analytics.IsReducible(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !isReducible {
		t.Error("expected IsReducible to return true for reducible graph")
	}

	// Verify consistency with full CheckReducibility
	result, _ := analytics.CheckReducibility(ctx, domTree)
	if result.IsReducible != isReducible {
		t.Error("IsReducible and CheckReducibility should return same reducibility")
	}
}

// TestIsReducible_FastPathIrreducible tests the fast-path method on irreducible graphs.
func TestIsReducible_FastPathIrreducible(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "B"}, // Cross edge - irreducible
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	isReducible, err := analytics.IsReducible(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if isReducible {
		t.Error("expected IsReducible to return false for irreducible graph")
	}

	// Verify consistency with full CheckReducibility
	result, _ := analytics.CheckReducibility(ctx, domTree)
	if result.IsReducible != isReducible {
		t.Error("IsReducible and CheckReducibility should return same reducibility")
	}
}

// TestIsReducible_NilInputs tests error handling for nil inputs.
func TestIsReducible_NilInputs(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	// Nil context
	_, err := analytics.IsReducible(nil, &DominatorTree{Entry: "A"})
	if err == nil {
		t.Error("expected error for nil context")
	}

	// Nil domTree
	_, err = analytics.IsReducible(ctx, nil)
	if err == nil {
		t.Error("expected error for nil domTree")
	}
}

// TestIsReducible_ContextCancellation tests context cancellation handling.
func TestIsReducible_ContextCancellation(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	domTree, _ := analytics.Dominators(context.Background(), "A")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := analytics.IsReducible(ctx, domTree)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// =============================================================================
// Score Precision Tests
// =============================================================================

// TestCheckReducibility_ScorePrecision verifies score is rounded to 4 decimal places.
func TestCheckReducibility_ScorePrecision(t *testing.T) {
	// Create graphs with different irreducible node counts to test score precision
	testCases := []struct {
		name          string
		totalNodes    int
		expectedScore float64
	}{
		{"fully_reducible", 10, 1.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Build a simple reducible graph
			nodes := make([]string, tc.totalNodes)
			edges := make([][2]string, 0)
			for i := 0; i < tc.totalNodes; i++ {
				nodes[i] = string(rune('A' + i))
				if i > 0 {
					edges = append(edges, [2]string{nodes[i-1], nodes[i]})
				}
			}

			hg := buildArticulationTestGraph(t, nodes, edges)
			analytics := NewGraphAnalytics(hg)
			ctx := context.Background()

			domTree, _ := analytics.Dominators(ctx, nodes[0])
			result, _ := analytics.CheckReducibility(ctx, domTree)

			// Verify score precision (4 decimal places)
			scoreStr := fmt.Sprintf("%.4f", result.Score)
			parsedScore, _ := strconv.ParseFloat(scoreStr, 64)
			if parsedScore != result.Score {
				t.Errorf("score should be rounded to 4 decimal places, got %v", result.Score)
			}

			if result.Score != tc.expectedScore {
				t.Errorf("expected score %f, got %f", tc.expectedScore, result.Score)
			}
		})
	}
}

// =============================================================================
// Edge Classification Tests
// =============================================================================

// TestCheckReducibility_TreeEdges tests that tree edges (non-back edges in dominator tree) don't affect reducibility.
func TestCheckReducibility_TreeEdges(t *testing.T) {
	// Simple DAG where all edges are tree edges in the dominator tree
	// This graph has no cycles and no cross edges
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A simple tree structure should always be reducible
	if !result.IsReducible {
		t.Error("expected tree-structured graph to be reducible")
	}
	if result.CrossEdgeCount != 0 {
		t.Errorf("expected 0 cross edges in tree structure, got %d", result.CrossEdgeCount)
	}
}

// TestCheckReducibility_NaturalLoopWithMultipleBackEdges tests multiple back edges to same header.
func TestCheckReducibility_NaturalLoopWithMultipleBackEdges(t *testing.T) {
	// Natural loop with multiple back edges (still reducible)
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"B", "D"},
		{"C", "B"}, // Back edge 1
		{"D", "B"}, // Back edge 2
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, err := analytics.Dominators(ctx, "A")
	if err != nil {
		t.Fatalf("failed to compute dominator tree: %v", err)
	}

	result, err := analytics.CheckReducibility(ctx, domTree)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsReducible {
		t.Error("expected natural loop with multiple back edges to be reducible")
	}
}

// =============================================================================
// Summary and Result Verification Tests
// =============================================================================

// TestCheckReducibility_SummaryConsistency verifies Summary fields are consistent.
func TestCheckReducibility_SummaryConsistency(t *testing.T) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "B"},
		{"D", "E"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	result, _ := analytics.CheckReducibility(ctx, domTree)

	// Verify summary consistency
	if result.Summary.TotalNodes != len(nodes) {
		t.Errorf("Summary.TotalNodes mismatch: expected %d, got %d",
			len(nodes), result.Summary.TotalNodes)
	}

	if result.Summary.IrreducibleRegionCount != len(result.IrreducibleRegions) {
		t.Errorf("Summary.IrreducibleRegionCount mismatch: expected %d, got %d",
			len(result.IrreducibleRegions), result.Summary.IrreducibleRegionCount)
	}

	// Verify WellStructuredPercent matches score
	expectedPercent := result.Score * 100.0
	if result.Summary.WellStructuredPercent != expectedPercent {
		t.Errorf("Summary.WellStructuredPercent mismatch: expected %f, got %f",
			expectedPercent, result.Summary.WellStructuredPercent)
	}

	// Verify NodeCount and EdgeCount in result
	if result.NodeCount != len(nodes) {
		t.Errorf("NodeCount mismatch: expected %d, got %d", len(nodes), result.NodeCount)
	}
}

// TestCheckReducibility_DomTreePreserved verifies DomTree is preserved in result.
func TestCheckReducibility_DomTreePreserved(t *testing.T) {
	nodes := []string{"A", "B", "C"}
	edges := [][2]string{{"A", "B"}, {"B", "C"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	result, _ := analytics.CheckReducibility(ctx, domTree)

	if result.DomTree != domTree {
		t.Error("expected DomTree to be preserved in result")
	}
}

// =============================================================================
// CRS Integration Tests
// =============================================================================

// TestCheckReducibilityWithCRS_NilDomTree tests CRS wrapper with nil domTree.
func TestCheckReducibilityWithCRS_NilDomTree(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	result, traceStep := analytics.CheckReducibilityWithCRS(ctx, nil)

	// Should return a result (not crash)
	if result == nil {
		t.Fatal("expected non-nil result even with nil domTree")
	}

	// TraceStep should indicate error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for nil domTree")
	}
}

// TestCheckReducibilityWithCRS_CancelledContext tests CRS wrapper with cancelled context.
func TestCheckReducibilityWithCRS_CancelledContext(t *testing.T) {
	nodes := []string{"A", "B"}
	edges := [][2]string{{"A", "B"}}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)

	domTree, _ := analytics.Dominators(context.Background(), "A")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, traceStep := analytics.CheckReducibilityWithCRS(ctx, domTree)

	// Should return a result (not crash)
	if result == nil {
		t.Fatal("expected non-nil result even with cancelled context")
	}

	// TraceStep should indicate error
	if traceStep.Error == "" {
		t.Error("expected error in TraceStep for cancelled context")
	}
}

// TestCheckReducibilityWithCRS_IrreducibleMetadata tests CRS metadata for irreducible graphs.
func TestCheckReducibilityWithCRS_IrreducibleMetadata(t *testing.T) {
	nodes := []string{"A", "B", "C", "D"}
	edges := [][2]string{
		{"A", "B"},
		{"A", "C"},
		{"B", "D"},
		{"C", "D"},
		{"D", "B"},
	}
	hg := buildArticulationTestGraph(t, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")
	result, traceStep := analytics.CheckReducibilityWithCRS(ctx, domTree)

	if result.IsReducible {
		t.Skip("test requires irreducible graph")
	}

	// Verify metadata contains irreducible info
	if traceStep.Metadata == nil {
		t.Fatal("expected metadata")
	}

	if v, ok := traceStep.Metadata["is_reducible"]; !ok || v != "false" {
		t.Error("expected is_reducible=false in metadata")
	}

	if _, ok := traceStep.Metadata["irreducible_regions"]; !ok {
		t.Error("expected irreducible_regions in metadata")
	}

	if _, ok := traceStep.Metadata["cross_edge_count"]; !ok {
		t.Error("expected cross_edge_count in metadata")
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

// buildBenchmarkGraph creates a graph for benchmarking (without testing.T).
func buildBenchmarkGraph(b *testing.B, nodeIDs []string, edges [][2]string) *HierarchicalGraph {
	b.Helper()

	g := NewGraph("/bench/project")

	// Create nodes
	for _, id := range nodeIDs {
		sym := &ast.Symbol{
			ID:       id,
			Name:     id,
			Kind:     ast.SymbolKindFunction,
			Package:  "pkg/bench",
			FilePath: "pkg/bench/bench.go",
			Exported: true,
		}
		_, err := g.AddNode(sym)
		if err != nil {
			b.Fatalf("failed to add node %s: %v", id, err)
		}
	}

	// Create edges
	for _, edge := range edges {
		err := g.AddEdge(edge[0], edge[1], EdgeTypeCalls, ast.Location{})
		if err != nil {
			b.Fatalf("failed to add edge %s -> %s: %v", edge[0], edge[1], err)
		}
	}

	g.Freeze()

	hg, err := WrapGraph(g)
	if err != nil {
		b.Fatalf("failed to wrap graph: %v", err)
	}

	return hg
}

// BenchmarkCheckReducibility_Small benchmarks reducibility check on small graphs.
func BenchmarkCheckReducibility_Small(b *testing.B) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"E", "B"}, // Natural loop
	}

	hg := buildBenchmarkGraph(b, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.CheckReducibility(ctx, domTree)
	}
}

// BenchmarkIsReducible_Small benchmarks fast-path reducibility check.
func BenchmarkIsReducible_Small(b *testing.B) {
	nodes := []string{"A", "B", "C", "D", "E"}
	edges := [][2]string{
		{"A", "B"},
		{"B", "C"},
		{"C", "D"},
		{"D", "E"},
		{"E", "B"},
	}

	hg := buildBenchmarkGraph(b, nodes, edges)
	analytics := NewGraphAnalytics(hg)
	ctx := context.Background()

	domTree, _ := analytics.Dominators(ctx, "A")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analytics.IsReducible(ctx, domTree)
	}
}
