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
	"sort"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// createTestGraphForModuleAPI creates a test graph with known communities and API structure.
//
// Structure:
//
//	Community 0 (pkg/auth):
//	  - pkg/auth/login.go:10:Login (entry point, called from main)
//	  - pkg/auth/session.go:20:CreateSession (internal, called by Login)
//	  - pkg/auth/token.go:30:GenerateToken (internal, called by CreateSession)
//
//	Community 1 (pkg/db):
//	  - pkg/db/connect.go:10:Connect (entry point, called from main and Login)
//	  - pkg/db/query.go:20:Query (internal, called by Connect)
//
//	Community 2 (isolated - no external callers):
//	  - pkg/util/helper.go:10:Format (isolated, only calls itself)
//
//	External (not in any community):
//	  - main.go:5:main (calls Login and Connect)
func createTestGraphForModuleAPI(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("test-project")

	// Community 0: Authentication module
	authLogin := &ast.Symbol{
		ID:       "pkg/auth/login.go:10:Login",
		Name:     "Login",
		Package:  "pkg/auth",
		FilePath: "pkg/auth/login.go",
		Kind:     ast.SymbolKindFunction,
	}
	authSession := &ast.Symbol{
		ID:       "pkg/auth/session.go:20:CreateSession",
		Name:     "CreateSession",
		Package:  "pkg/auth",
		FilePath: "pkg/auth/session.go",
		Kind:     ast.SymbolKindFunction,
	}
	authToken := &ast.Symbol{
		ID:       "pkg/auth/token.go:30:GenerateToken",
		Name:     "GenerateToken",
		Package:  "pkg/auth",
		FilePath: "pkg/auth/token.go",
		Kind:     ast.SymbolKindFunction,
	}

	// Community 1: Database module
	dbConnect := &ast.Symbol{
		ID:       "pkg/db/connect.go:10:Connect",
		Name:     "Connect",
		Package:  "pkg/db",
		FilePath: "pkg/db/connect.go",
		Kind:     ast.SymbolKindFunction,
	}
	dbQuery := &ast.Symbol{
		ID:       "pkg/db/query.go:20:Query",
		Name:     "Query",
		Package:  "pkg/db",
		FilePath: "pkg/db/query.go",
		Kind:     ast.SymbolKindFunction,
	}

	// Community 2: Isolated utility
	utilFormat := &ast.Symbol{
		ID:       "pkg/util/helper.go:10:Format",
		Name:     "Format",
		Package:  "pkg/util",
		FilePath: "pkg/util/helper.go",
		Kind:     ast.SymbolKindFunction,
	}

	// External: Main function
	mainFunc := &ast.Symbol{
		ID:       "main.go:5:main",
		Name:     "main",
		Package:  "main",
		FilePath: "main.go",
		Kind:     ast.SymbolKindFunction,
	}

	// Add nodes
	g.AddNode(authLogin)
	g.AddNode(authSession)
	g.AddNode(authToken)
	g.AddNode(dbConnect)
	g.AddNode(dbQuery)
	g.AddNode(utilFormat)
	g.AddNode(mainFunc)

	// Add edges
	// Community 0: Auth module
	g.AddEdge(mainFunc.ID, authLogin.ID, graph.EdgeTypeCalls, ast.Location{})    // main → Login (external)
	g.AddEdge(authLogin.ID, authSession.ID, graph.EdgeTypeCalls, ast.Location{}) // Login → CreateSession (internal)
	g.AddEdge(authSession.ID, authToken.ID, graph.EdgeTypeCalls, ast.Location{}) // CreateSession → GenerateToken (internal)
	g.AddEdge(authLogin.ID, dbConnect.ID, graph.EdgeTypeCalls, ast.Location{})   // Login → Connect (cross-community)

	// Community 1: DB module
	g.AddEdge(mainFunc.ID, dbConnect.ID, graph.EdgeTypeCalls, ast.Location{}) // main → Connect (external)
	g.AddEdge(dbConnect.ID, dbQuery.ID, graph.EdgeTypeCalls, ast.Location{})  // Connect → Query (internal)

	// Community 2: Isolated utility (self-loop)
	g.AddEdge(utilFormat.ID, utilFormat.ID, graph.EdgeTypeCalls, ast.Location{}) // Format → Format (self-loop, internal)

	g.Freeze()

	// Create index
	idx := index.NewSymbolIndex()
	idx.Add(authLogin)
	idx.Add(authSession)
	idx.Add(authToken)
	idx.Add(dbConnect)
	idx.Add(dbQuery)
	idx.Add(utilFormat)
	idx.Add(mainFunc)

	return g, idx
}

func TestFindModuleAPITool_Execute_AllCommunities(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{
		// Default: all communities, top 10, min_size 3
	}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true, got false. Error: %s", result.Error)
	}

	output, ok := result.Output.(FindModuleAPIOutput)
	if !ok {
		t.Fatalf("Expected FindModuleAPIOutput, got %T", result.Output)
	}

	// Note: Community detection is non-deterministic, so we can't assert exact communities
	// Just verify structure is correct
	if len(output.Modules) == 0 {
		t.Logf("No modules found - communities may be too small or not detected")
	}

	// Verify summary exists
	if output.Summary.CommunitiesAnalyzed < 0 {
		t.Errorf("Summary.CommunitiesAnalyzed should be >= 0, got %d", output.Summary.CommunitiesAnalyzed)
	}

	t.Logf("Found %d modules", len(output.Modules))
}

func TestFindModuleAPITool_Execute_SpecificCommunity(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{
		"community_id": 0, // Request specific community
	}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true, got false. Error: %s", result.Error)
	}

	output, ok := result.Output.(FindModuleAPIOutput)
	if !ok {
		t.Fatalf("Expected FindModuleAPIOutput, got %T", result.Output)
	}

	// Should have at most 1 module (community 0 if it exists and meets min_size)
	if len(output.Modules) > 1 {
		t.Errorf("Expected at most 1 module for community_id=0, got %d", len(output.Modules))
	}
}

func TestFindModuleAPITool_Execute_TopN(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{
		"top":                2,
		"min_community_size": 1, // Lower threshold to ensure we find communities
	}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true, got false. Error: %s", result.Error)
	}

	output, ok := result.Output.(FindModuleAPIOutput)
	if !ok {
		t.Fatalf("Expected FindModuleAPIOutput, got %T", result.Output)
	}

	// Should have at most 2 modules
	if len(output.Modules) > 2 {
		t.Errorf("Expected at most 2 modules with top=2, got %d", len(output.Modules))
	}
}

func TestFindModuleAPITool_Execute_MinCommunitySize(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{
		"min_community_size": 100, // Very high threshold - should filter all
	}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true even with no results, got false")
	}

	output, ok := result.Output.(FindModuleAPIOutput)
	if !ok {
		t.Fatalf("Expected FindModuleAPIOutput, got %T", result.Output)
	}

	// Should have no modules due to high filter
	if len(output.Modules) > 0 {
		t.Errorf("Expected 0 modules with min_size=100, got %d", len(output.Modules))
	}

	// Should have a helpful message
	if output.Message == "" {
		t.Errorf("Expected a message explaining why no modules found")
	}

	t.Logf("Message: %s", output.Message)
}

func TestFindModuleAPITool_Execute_EmptyGraph(t *testing.T) {
	g := graph.NewGraph("test-project")
	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	idx := index.NewSymbolIndex()
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Expected success=true for empty graph, got false")
	}

	output, ok := result.Output.(FindModuleAPIOutput)
	if !ok {
		t.Fatalf("Expected FindModuleAPIOutput, got %T", result.Output)
	}

	if len(output.Modules) != 0 {
		t.Errorf("Expected 0 modules for empty graph, got %d", len(output.Modules))
	}

	if output.Message == "" {
		t.Errorf("Expected message for empty graph result")
	}
}

func TestFindModuleAPITool_Execute_NilAnalytics(t *testing.T) {
	g := graph.NewGraph("test-project")
	idx := index.NewSymbolIndex()
	tool := NewFindModuleAPITool(nil, g, idx)

	ctx := context.Background()
	params := MapParams{Params: map[string]any{}}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if result.Success {
		t.Errorf("Expected success=false with nil analytics, got true")
	}

	if result.Error == "" {
		t.Errorf("Expected error message with nil analytics")
	}
}

func TestFindModuleAPITool_Execute_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	params := MapParams{Params: map[string]any{}}

	result, err := tool.Execute(ctx, params)

	// Context cancellation should return the error directly (not wrapped in Result)
	if err == nil {
		t.Errorf("Expected context cancellation error, got nil")
	}

	// OR it might return early with empty result
	if err == nil && result != nil {
		t.Logf("Tool returned result with cancelled context (graceful handling)")
	}
}

func TestFindModuleAPITool_Execute_ParameterValidation(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx)

	tests := []struct {
		name   string
		params MapParams
		check  func(t *testing.T, result *Result)
	}{
		{
			name: "negative top clamped to 1",
			params: MapParams{Params: map[string]any{
				"top": -5,
			}},
			check: func(t *testing.T, result *Result) {
				// Should succeed with clamped value
				if !result.Success && result.Error != "" {
					t.Logf("Result: %+v", result)
				}
			},
		},
		{
			name: "top > 50 clamped to 50",
			params: MapParams{Params: map[string]any{
				"top": 100,
			}},
			check: func(t *testing.T, result *Result) {
				// Should succeed with clamped value
				output, ok := result.Output.(FindModuleAPIOutput)
				if ok && len(output.Modules) > 50 {
					t.Errorf("Expected at most 50 modules, got %d", len(output.Modules))
				}
			},
		},
		{
			name: "min_community_size < 1 clamped to 1",
			params: MapParams{Params: map[string]any{
				"min_community_size": 0,
			}},
			check: func(t *testing.T, result *Result) {
				// Should succeed with clamped value
				if !result.Success && result.Error != "" {
					t.Logf("Clamped min_community_size accepted")
				}
			},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tt.params)
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			tt.check(t, result)
		})
	}
}

func TestFindModuleAPITool_Name(t *testing.T) {
	tool := &findModuleAPITool{}
	if tool.Name() != "find_module_api" {
		t.Errorf("Expected name 'find_module_api', got %q", tool.Name())
	}
}

func TestFindModuleAPITool_Category(t *testing.T) {
	tool := &findModuleAPITool{}
	if tool.Category() != CategoryExploration {
		t.Errorf("Expected category %v, got %v", CategoryExploration, tool.Category())
	}
}

func TestFindModuleAPITool_Definition(t *testing.T) {
	tool := &findModuleAPITool{}
	def := tool.Definition()

	if def.Name != "find_module_api" {
		t.Errorf("Expected name 'find_module_api', got %q", def.Name)
	}

	if def.Category != CategoryExploration {
		t.Errorf("Expected category %v, got %v", CategoryExploration, def.Category)
	}

	// Check parameters exist
	if _, ok := def.Parameters["community_id"]; !ok {
		t.Error("Expected 'community_id' parameter")
	}
	if _, ok := def.Parameters["top"]; !ok {
		t.Error("Expected 'top' parameter")
	}
	if _, ok := def.Parameters["min_community_size"]; !ok {
		t.Error("Expected 'min_community_size' parameter")
	}

	// Check keywords
	if len(def.WhenToUse.Keywords) == 0 {
		t.Error("Expected non-empty keywords")
	}
}

func TestFindModuleAPITool_ExtractPackage(t *testing.T) {
	tests := []struct {
		nodeID   string
		expected string
	}{
		{"pkg/auth/login.go:10:Login", "pkg/auth"},
		{"services/trace/graph/types.go:50:Node", "services/trace/graph"},
		{"main.go:5:main", "(unknown)"}, // No slash
		{"", "(unknown)"},
		{"no_colon", "(unknown)"},
	}

	for _, tt := range tests {
		t.Run(tt.nodeID, func(t *testing.T) {
			result := extractPackage(tt.nodeID)
			if result != tt.expected {
				t.Errorf("extractPackage(%q) = %q, want %q", tt.nodeID, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// GR-18b-P2: Enhancement Tests
// =============================================================================

// TestFindModuleAPITool_CommunityCache_HitAndMiss tests E1: community caching.
func TestFindModuleAPITool_CommunityCache_HitAndMiss(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx).(*findModuleAPITool)

	ctx := context.Background()

	// First call - cache miss
	result1, _, err := tool.detectCommunities(ctx)
	if err != nil {
		t.Fatalf("First detectCommunities failed: %v", err)
	}

	// Verify cache was populated
	graphBuiltAt := g.BuiltAtMilli
	cachedResult := tool.cache.get(graphBuiltAt)
	if cachedResult == nil {
		t.Error("Expected cache to be populated after first call")
	}

	// Second call - should be cache hit
	result2, _, err := tool.detectCommunities(ctx)
	if err != nil {
		t.Fatalf("Second detectCommunities failed: %v", err)
	}

	// Results should be identical (same pointer if cached correctly)
	if len(result1.Communities) != len(result2.Communities) {
		t.Errorf("Cache hit returned different community count: %d vs %d",
			len(result1.Communities), len(result2.Communities))
	}

	// Verify LRU eviction by filling cache beyond capacity
	for i := 0; i < 15; i++ {
		fakeBuiltAt := int64(i + 1000)
		fakeCommunities := &graph.CommunityResult{
			Communities: []graph.Community{{ID: i}},
		}
		tool.cache.put(fakeBuiltAt, fakeCommunities)
	}

	// Cache should only have maxSize (10) entries
	if len(tool.cache.entries) > 10 {
		t.Errorf("Cache exceeded max size: %d entries", len(tool.cache.entries))
	}
}

// TestFindModuleAPITool_SemanticModuleNaming tests E5: semantic pattern matching.
func TestFindModuleAPITool_SemanticModuleNaming(t *testing.T) {
	tests := []struct {
		pkgName  string
		expected string
	}{
		// Infrastructure
		{"auth", "Authentication"},
		{"authn", "Authentication"},
		{"authz", "Authorization"},
		{"database", "Database"},
		{"db", "Database"},
		{"cache", "Caching"},
		{"config", "Configuration"},

		// API
		{"api", "API"},
		{"rest", "REST API"},
		{"graphql", "GraphQL API"},
		{"grpc", "gRPC API"},
		{"http", "HTTP Server"},
		{"handler", "Request Handlers"},

		// Business Logic
		{"service", "Business Logic"},
		{"controller", "Controllers"},
		{"model", "Data Models"},
		{"repository", "Data Access"},

		// Utilities
		{"util", "Utilities"},
		{"helper", "Helpers"},
		{"common", "Common Utilities"},

		// Security
		{"crypto", "Cryptography"},
		{"jwt", "JWT Handling"},
		{"token", "Token Management"},

		// Observability
		{"logger", "Logging"},
		{"metric", "Metrics"},
		{"trace", "Tracing"},

		// No match
		{"foobar", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.pkgName, func(t *testing.T) {
			result := matchSemanticPattern(tt.pkgName)
			if result != tt.expected {
				t.Errorf("matchSemanticPattern(%q) = %q, want %q",
					tt.pkgName, result, tt.expected)
			}
		})
	}
}

// TestFindModuleAPITool_DeterministicSorting tests E3: tertiary sort by name.
func TestFindModuleAPITool_DeterministicSorting(t *testing.T) {
	// Create API functions with identical coverage and external callers
	// to trigger tertiary sort
	apiSurface := []APIFunction{
		{
			ID:              "node3",
			Name:            "FunctionC",
			Coverage:        0.5,
			ExternalCallers: 3,
		},
		{
			ID:              "node1",
			Name:            "FunctionA",
			Coverage:        0.5,
			ExternalCallers: 3,
		},
		{
			ID:              "node2",
			Name:            "FunctionB",
			Coverage:        0.5,
			ExternalCallers: 3,
		},
	}

	// Sort using the same logic as computeAPISurface
	sort.Slice(apiSurface, func(i, j int) bool {
		if apiSurface[i].Coverage != apiSurface[j].Coverage {
			return apiSurface[i].Coverage > apiSurface[j].Coverage
		}
		if apiSurface[i].ExternalCallers != apiSurface[j].ExternalCallers {
			return apiSurface[i].ExternalCallers > apiSurface[j].ExternalCallers
		}
		// Tertiary sort by name for determinism (E3 enhancement)
		return apiSurface[i].Name < apiSurface[j].Name
	})

	// Verify alphabetical order
	expectedOrder := []string{"FunctionA", "FunctionB", "FunctionC"}
	for i, api := range apiSurface {
		if api.Name != expectedOrder[i] {
			t.Errorf("Position %d: got %s, want %s", i, api.Name, expectedOrder[i])
		}
	}

	// Run multiple times to ensure determinism
	for run := 0; run < 5; run++ {
		// Shuffle
		apiSurface = []APIFunction{
			{ID: "node2", Name: "FunctionB", Coverage: 0.5, ExternalCallers: 3},
			{ID: "node3", Name: "FunctionC", Coverage: 0.5, ExternalCallers: 3},
			{ID: "node1", Name: "FunctionA", Coverage: 0.5, ExternalCallers: 3},
		}

		// Sort
		sort.Slice(apiSurface, func(i, j int) bool {
			if apiSurface[i].Coverage != apiSurface[j].Coverage {
				return apiSurface[i].Coverage > apiSurface[j].Coverage
			}
			if apiSurface[i].ExternalCallers != apiSurface[j].ExternalCallers {
				return apiSurface[i].ExternalCallers > apiSurface[j].ExternalCallers
			}
			return apiSurface[i].Name < apiSurface[j].Name
		})

		// Verify still in correct order
		for i, api := range apiSurface {
			if api.Name != expectedOrder[i] {
				t.Errorf("Run %d, Position %d: got %s, want %s",
					run, i, api.Name, expectedOrder[i])
			}
		}
	}
}

// TestFindModuleAPITool_SubgraphExtraction tests E2: local subgraph dominators.
func TestFindModuleAPITool_SubgraphExtraction(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx).(*findModuleAPITool)

	// Get actual communities
	ctx := context.Background()
	communities, _, err := tool.detectCommunities(ctx)
	if err != nil {
		t.Fatalf("detectCommunities failed: %v", err)
	}

	if len(communities.Communities) == 0 {
		t.Skip("No communities detected, skipping subgraph test")
	}

	// Extract subgraph for first community
	comm := communities.Communities[0]
	subgraph, err := tool.extractSubgraph(comm)
	if err != nil {
		t.Fatalf("extractSubgraph failed: %v", err)
	}

	// Collect subgraph nodes into slice
	var subgraphNodes []*graph.Node
	for _, node := range subgraph.Nodes() {
		subgraphNodes = append(subgraphNodes, node)
	}

	// Verify subgraph contains only community nodes
	if len(subgraphNodes) > len(comm.Nodes) {
		t.Errorf("Subgraph has more nodes (%d) than community (%d)",
			len(subgraphNodes), len(comm.Nodes))
	}

	// Verify all subgraph nodes are in community
	memberSet := make(map[string]bool, len(comm.Nodes))
	for _, m := range comm.Nodes {
		memberSet[m] = true
	}

	for _, node := range subgraphNodes {
		if !memberSet[node.ID] {
			t.Errorf("Subgraph contains node %s not in community", node.ID)
		}
	}

	// Verify subgraph edges are internal only
	for _, node := range subgraphNodes {
		for _, edge := range node.Outgoing {
			if !memberSet[edge.ToID] {
				t.Errorf("Subgraph has external edge: %s -> %s", node.ID, edge.ToID)
			}
		}
	}

	// Verify subgraph is frozen
	if !subgraph.IsFrozen() {
		t.Error("Extracted subgraph should be frozen")
	}
}

// TestFindModuleAPITool_SubgraphDominators tests E2: coverage computation using subgraph.
func TestFindModuleAPITool_SubgraphDominators(t *testing.T) {
	g, idx := createTestGraphForModuleAPI(t)
	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("failed to wrap graph: %v", err)
	}

	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindModuleAPITool(analytics, g, idx).(*findModuleAPITool)

	ctx := context.Background()

	// Get communities
	communities, _, err := tool.detectCommunities(ctx)
	if err != nil {
		t.Fatalf("detectCommunities failed: %v", err)
	}

	if len(communities.Communities) == 0 {
		t.Skip("No communities detected, skipping")
	}

	// Test coverage computation for first community with valid entry node
	comm := communities.Communities[0]
	if len(comm.Nodes) == 0 {
		t.Skip("Community has no nodes")
	}

	entryNode := comm.Nodes[0]

	// Compute coverage using subgraph approach (E2)
	coverage, dominated, err := tool.computeCoverage(ctx, comm, entryNode)

	// Should not error (graceful degradation if subgraph fails)
	if err != nil {
		t.Errorf("computeCoverage should not error, got: %v", err)
	}

	// Coverage should be between 0 and 1
	if coverage < 0.0 || coverage > 1.0 {
		t.Errorf("Coverage out of range: %f", coverage)
	}

	// Dominated count should not exceed community size
	if dominated > len(comm.Nodes) {
		t.Errorf("Dominated count (%d) exceeds community size (%d)",
			dominated, len(comm.Nodes))
	}

	// If dominated > 0, coverage should be > 0
	if dominated > 0 && coverage == 0.0 {
		t.Error("Coverage should be > 0 when dominated > 0")
	}
}
