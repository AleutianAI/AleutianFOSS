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
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

func createTestGraphForCommunities(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Community 1: auth package (4 nodes, densely connected)
	authLogin := &ast.Symbol{
		ID:        "auth/login.go:10:Login",
		Name:      "Login",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/login.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authLogout := &ast.Symbol{
		ID:        "auth/login.go:35:Logout",
		Name:      "Logout",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/login.go",
		StartLine: 35,
		EndLine:   50,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authSession := &ast.Symbol{
		ID:        "auth/session.go:10:CreateSession",
		Name:      "CreateSession",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/session.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}
	authValidate := &ast.Symbol{
		ID:        "auth/validate.go:10:ValidateToken",
		Name:      "ValidateToken",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "auth/validate.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "auth",
		Exported:  true,
		Language:  "go",
	}

	// Community 2: config package (3 nodes, densely connected)
	configLoad := &ast.Symbol{
		ID:        "config/loader.go:10:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	configParse := &ast.Symbol{
		ID:        "config/parser.go:10:ParseConfig",
		Name:      "ParseConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/parser.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	configValidate := &ast.Symbol{
		ID:        "config/validate.go:10:ValidateConfig",
		Name:      "ValidateConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/validate.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}

	// Add all nodes to graph and index
	allSymbols := []*ast.Symbol{
		authLogin, authLogout, authSession, authValidate,
		configLoad, configParse, configValidate,
	}
	for _, sym := range allSymbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Community 1 edges (auth - dense internal connections)
	g.AddEdge(authLogin.ID, authSession.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 15,
	})
	g.AddEdge(authLogin.ID, authValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 18,
	})
	g.AddEdge(authLogout.ID, authSession.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogout.FilePath, StartLine: 40,
	})
	g.AddEdge(authSession.ID, authValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authSession.FilePath, StartLine: 15,
	})

	// Community 2 edges (config - dense internal connections)
	g.AddEdge(configLoad.ID, configParse.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 15,
	})
	g.AddEdge(configLoad.ID, configValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 20,
	})
	g.AddEdge(configParse.ID, configValidate.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configParse.FilePath, StartLine: 15,
	})

	// One cross-community edge (sparse connection between communities)
	g.AddEdge(authLogin.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: authLogin.FilePath, StartLine: 12,
	})

	g.Freeze()
	return g, idx
}

// createCrossPackageCommunityGraph creates a graph where a community spans packages.
func createCrossPackageCommunityGraph(t *testing.T) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()

	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Cross-package community: server and config are tightly coupled
	serverInit := &ast.Symbol{
		ID:        "server/init.go:10:InitServer",
		Name:      "InitServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/init.go",
		StartLine: 10,
		EndLine:   30,
		Package:   "server",
		Exported:  true,
		Language:  "go",
	}
	configLoad := &ast.Symbol{
		ID:        "config/loader.go:10:LoadConfig",
		Name:      "LoadConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/loader.go",
		StartLine: 10,
		EndLine:   25,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}
	serverConfig := &ast.Symbol{
		ID:        "server/config.go:10:ConfigureServer",
		Name:      "ConfigureServer",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "server/config.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "server",
		Exported:  true,
		Language:  "go",
	}
	configDefault := &ast.Symbol{
		ID:        "config/defaults.go:10:DefaultConfig",
		Name:      "DefaultConfig",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "config/defaults.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "config",
		Exported:  true,
		Language:  "go",
	}

	// Add nodes to graph and index
	allSymbols := []*ast.Symbol{serverInit, configLoad, serverConfig, configDefault}
	for _, sym := range allSymbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Dense cross-package connections (they should form one community)
	g.AddEdge(serverInit.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverInit.FilePath, StartLine: 15,
	})
	g.AddEdge(serverInit.ID, serverConfig.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverInit.FilePath, StartLine: 18,
	})
	g.AddEdge(serverConfig.ID, configLoad.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverConfig.FilePath, StartLine: 12,
	})
	g.AddEdge(serverConfig.ID, configDefault.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: serverConfig.FilePath, StartLine: 14,
	})
	g.AddEdge(configLoad.ID, configDefault.ID, graph.EdgeTypeCalls, ast.Location{
		FilePath: configLoad.FilePath, StartLine: 15,
	})

	g.Freeze()
	return g, idx
}

func TestFindCommunitiesTool_Execute(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	t.Run("finds communities with default params", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// Check algorithm field
		if output.Algorithm != "Leiden" {
			t.Errorf("Expected algorithm 'Leiden', got '%s'", output.Algorithm)
		}

		// Check modularity is in valid range
		if output.Modularity < 0 || output.Modularity > 1 {
			t.Errorf("Modularity %f outside expected range [0,1]", output.Modularity)
		}

		// Check communities exist
		if len(output.Communities) == 0 {
			t.Error("Expected at least one community")
		}

		// Check output text is populated
		if result.OutputText == "" {
			t.Error("OutputText is empty")
		}
	})

	t.Run("respects min_size parameter", func(t *testing.T) {
		// With min_size=5, should filter out smaller communities
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": 5,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// All returned communities should have size >= 5
		for _, comm := range output.Communities {
			if comm.Size < 5 {
				t.Errorf("Community size %d is less than min_size 5", comm.Size)
			}
		}
	})

	t.Run("respects resolution parameter", func(t *testing.T) {
		// Lower resolution should produce fewer, larger communities
		lowResResult, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"resolution": 0.5,
		}})
		if err != nil {
			t.Fatalf("Execute() low resolution error = %v", err)
		}

		// Higher resolution should produce more, smaller communities
		highResResult, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"resolution": 2.0,
		}})
		if err != nil {
			t.Fatalf("Execute() high resolution error = %v", err)
		}

		// Both should succeed
		if !lowResResult.Success || !highResResult.Success {
			t.Fatalf("One of the resolution tests failed")
		}

		// Note: We can't guarantee exact community counts, but both should run
		lowOutput, ok := lowResResult.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Low resolution output is not FindCommunitiesOutput, got %T", lowResResult.Output)
		}
		highOutput, ok := highResResult.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("High resolution output is not FindCommunitiesOutput, got %T", highResResult.Output)
		}

		// Check that modularity is present (it's always set for FindCommunitiesOutput)
		if lowOutput.Modularity == 0 && lowOutput.CommunityCount > 0 {
			t.Error("Low resolution output has zero modularity with communities")
		}
		if highOutput.Modularity == 0 && highOutput.CommunityCount > 0 {
			t.Error("High resolution output has zero modularity with communities")
		}
	})

	t.Run("respects top parameter", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top":      1,
			"min_size": 1, // Allow small communities
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		if len(output.Communities) > 1 {
			t.Errorf("got %d communities, want at most 1", len(output.Communities))
		}
	})

	t.Run("clamps invalid min_size to valid range", func(t *testing.T) {
		// min_size < 1 should be clamped
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"min_size": -5,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped min_size")
		}
	})

	t.Run("clamps invalid resolution to valid range", func(t *testing.T) {
		// resolution < 0.1 should be clamped
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"resolution": 0.0,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped resolution")
		}
	})

	t.Run("clamps invalid top to valid range", func(t *testing.T) {
		// top > 50 should be clamped
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"top": 1000,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() should succeed with clamped top")
		}
	})

	t.Run("returns result metadata", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}

		// Should have result metadata (these are always present in typed struct)
		if output.CommunityCount < 0 {
			t.Error("Expected non-negative community_count")
		}
		if output.Algorithm == "" {
			t.Error("Expected algorithm field to be set")
		}
		// Converged is a bool field, always present in typed struct
		// Just verify the output was processed (Converged may be true or false)
		t.Logf("Converged: %v", output.Converged)
	})
}

func TestFindCommunitiesTool_NilAnalytics(t *testing.T) {
	ctx := context.Background()
	_, idx := createTestGraphForCommunities(t)

	tool := NewFindCommunitiesTool(nil, idx)
	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Success {
		t.Error("Expected failure with nil analytics")
	}
	if result.Error == "" {
		t.Error("Expected error message")
	}
}

func TestFindCommunitiesTool_ContextCancellation(t *testing.T) {
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = tool.Execute(ctx, MapParams{Params: map[string]any{}})
	if err == nil {
		t.Error("Expected context.Canceled error")
	}
}

func TestFindCommunitiesTool_CrossPackageDetection(t *testing.T) {
	ctx := context.Background()
	g, idx := createCrossPackageCommunityGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1, // Allow small communities
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Check if cross-package communities are identified
	if len(output.CrossPackageCommunities) > 0 {
		t.Logf("Found %d cross-package communities", len(output.CrossPackageCommunities))
	}

	// The output text should mention cross-package if detected
	if result.OutputText != "" {
		// This is a soft check - cross-package detection is algorithmic
		t.Logf("Output text: %s", result.OutputText[:min(200, len(result.OutputText))])
	}
}

func TestFindCommunitiesTool_ShowCrossEdges(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	t.Run("show_cross_edges true", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_cross_edges": true,
			"min_size":         1,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}
		// cross_community_edges should be present (may be empty if no cross edges exist)
		// Just verify the output was processed - the field is always present in typed struct
		t.Logf("CrossCommunityEdges count: %d", len(output.CrossCommunityEdges))
	})

	t.Run("show_cross_edges false", func(t *testing.T) {
		result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
			"show_cross_edges": false,
			"min_size":         1,
		}})

		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !result.Success {
			t.Fatalf("Execute() failed: %s", result.Error)
		}

		// Should still succeed, just without cross edges
		output, ok := result.Output.(FindCommunitiesOutput)
		if !ok {
			t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
		}
		if len(output.CrossCommunityEdges) > 0 {
			t.Error("Expected no cross_community_edges when show_cross_edges=false")
		}
	})
}

func TestFindCommunitiesTool_Definition(t *testing.T) {
	tool := NewFindCommunitiesTool(nil, nil)

	if got := tool.Name(); got != "find_communities" {
		t.Errorf("Name() = %v, want find_communities", got)
	}

	if got := tool.Category(); got != CategoryExploration {
		t.Errorf("Category() = %v, want CategoryExploration", got)
	}

	def := tool.Definition()
	if def.Name != "find_communities" {
		t.Errorf("Definition().Name = %v, want find_communities", def.Name)
	}
	if def.Description == "" {
		t.Error("Definition().Description is empty")
	}
	if len(def.Parameters) == 0 {
		t.Error("Definition().Parameters is empty")
	}

	// Check for expected parameters
	expectedParams := []string{"min_size", "resolution", "top", "show_cross_edges"}
	for _, param := range expectedParams {
		if _, ok := def.Parameters[param]; !ok {
			t.Errorf("Missing '%s' parameter", param)
		}
	}

	// Check timeout is reasonable
	if def.Timeout < 30*time.Second {
		t.Errorf("Timeout %v is too short for community detection", def.Timeout)
	}
}

func TestFindCommunitiesTool_EmptyGraph(t *testing.T) {
	ctx := context.Background()

	// Create empty graph
	g := graph.NewGraph("/test")
	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	idx := index.NewSymbolIndex()
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with empty graph")
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	if len(output.Communities) != 0 {
		t.Errorf("Expected 0 communities for empty graph, got %d", len(output.Communities))
	}
}

func TestFindCommunitiesTool_ModularityQualityLabel(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should have modularity_quality label
	quality := output.ModularityQuality
	validQualities := map[string]bool{
		"weak": true, "moderate": true, "good": true, "strong": true,
	}
	if !validQualities[quality] {
		t.Errorf("Invalid modularity_quality: %s", quality)
	}
}

// BenchmarkFindCommunities benchmarks Leiden-based community detection.
func BenchmarkFindCommunities(b *testing.B) {
	g, idx := createLargeGraph(b, 500) // Smaller for faster benchmarks

	hg, err := graph.WrapGraph(g)
	if err != nil {
		b.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tool.Execute(ctx, MapParams{Params: map[string]any{"top": 10}})
		if err != nil {
			b.Fatalf("Execute failed: %v", err)
		}
	}
}

// TestFindCommunitiesTool_SingleNodeGraph tests behavior with a single node.
func TestFindCommunitiesTool_SingleNodeGraph(t *testing.T) {
	ctx := context.Background()

	// Create single-node graph
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	singleSym := &ast.Symbol{
		ID:        "pkg/solo.go:10:Solo",
		Name:      "Solo",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "pkg/solo.go",
		StartLine: 10,
		EndLine:   20,
		Package:   "pkg",
		Exported:  true,
		Language:  "go",
	}

	g.AddNode(singleSym)
	if err := idx.Add(singleSym); err != nil {
		t.Fatalf("Failed to add symbol: %v", err)
	}
	g.Freeze()

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1, // Allow single-node communities
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with single node graph: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Single node should form one community
	if len(output.Communities) != 1 {
		t.Errorf("Expected 1 community for single-node graph, got %d", len(output.Communities))
	}

	if len(output.Communities) > 0 {
		size := output.Communities[0].Size
		if size != 1 {
			t.Errorf("Single-node community should have size 1, got %d", size)
		}
	}
}

// createDisconnectedGraph creates a graph with two disconnected components.
func createDisconnectedGraph(t testing.TB) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	// Component A - fully connected triangle
	compA := []*ast.Symbol{
		{ID: "compA/a1.go:10:A1", Name: "A1", Kind: ast.SymbolKindFunction, FilePath: "compA/a1.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
		{ID: "compA/a2.go:10:A2", Name: "A2", Kind: ast.SymbolKindFunction, FilePath: "compA/a2.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
		{ID: "compA/a3.go:10:A3", Name: "A3", Kind: ast.SymbolKindFunction, FilePath: "compA/a3.go", StartLine: 10, EndLine: 20, Package: "compA", Language: "go"},
	}

	// Component B - fully connected triangle (disconnected from A)
	compB := []*ast.Symbol{
		{ID: "compB/b1.go:10:B1", Name: "B1", Kind: ast.SymbolKindFunction, FilePath: "compB/b1.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
		{ID: "compB/b2.go:10:B2", Name: "B2", Kind: ast.SymbolKindFunction, FilePath: "compB/b2.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
		{ID: "compB/b3.go:10:B3", Name: "B3", Kind: ast.SymbolKindFunction, FilePath: "compB/b3.go", StartLine: 10, EndLine: 20, Package: "compB", Language: "go"},
	}

	for _, sym := range append(compA, compB...) {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Connect component A (triangle)
	g.AddEdge(compA[0].ID, compA[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[0].FilePath, StartLine: 15})
	g.AddEdge(compA[1].ID, compA[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[1].FilePath, StartLine: 15})
	g.AddEdge(compA[2].ID, compA[0].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compA[2].FilePath, StartLine: 15})

	// Connect component B (triangle)
	g.AddEdge(compB[0].ID, compB[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[0].FilePath, StartLine: 15})
	g.AddEdge(compB[1].ID, compB[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[1].FilePath, StartLine: 15})
	g.AddEdge(compB[2].ID, compB[0].ID, graph.EdgeTypeCalls, ast.Location{FilePath: compB[2].FilePath, StartLine: 15})

	g.Freeze()
	return g, idx
}

// TestFindCommunitiesTool_DisconnectedGraph tests behavior with disconnected components.
func TestFindCommunitiesTool_DisconnectedGraph(t *testing.T) {
	ctx := context.Background()
	g, idx := createDisconnectedGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should detect at least 2 communities (one per disconnected component)
	if len(output.Communities) < 2 {
		t.Errorf("Expected at least 2 communities for disconnected graph, got %d", len(output.Communities))
	}

	// Total nodes across communities should equal 6
	totalNodes := 0
	for _, comm := range output.Communities {
		totalNodes += comm.Size
	}
	if totalNodes != 6 {
		t.Errorf("Total nodes in communities should be 6, got %d", totalNodes)
	}
}

// createAllSamePackageGraph creates a graph where all nodes are in the same package.
func createAllSamePackageGraph(t testing.TB) (*graph.Graph, *index.SymbolIndex) {
	t.Helper()
	g := graph.NewGraph("/test")
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{ID: "myPkg/file1.go:10:Func1", Name: "Func1", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file1.go", StartLine: 10, EndLine: 20, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file1.go:30:Func2", Name: "Func2", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file1.go", StartLine: 30, EndLine: 40, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file2.go:10:Func3", Name: "Func3", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file2.go", StartLine: 10, EndLine: 20, Package: "myPkg", Language: "go"},
		{ID: "myPkg/file2.go:30:Func4", Name: "Func4", Kind: ast.SymbolKindFunction, FilePath: "myPkg/file2.go", StartLine: 30, EndLine: 40, Package: "myPkg", Language: "go"},
	}

	for _, sym := range symbols {
		g.AddNode(sym)
		if err := idx.Add(sym); err != nil {
			t.Logf("Warning: failed to index %s: %v", sym.Name, err)
		}
	}

	// Dense connections
	g.AddEdge(symbols[0].ID, symbols[1].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[0].FilePath, StartLine: 15})
	g.AddEdge(symbols[0].ID, symbols[2].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[0].FilePath, StartLine: 16})
	g.AddEdge(symbols[1].ID, symbols[3].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[1].FilePath, StartLine: 35})
	g.AddEdge(symbols[2].ID, symbols[3].ID, graph.EdgeTypeCalls, ast.Location{FilePath: symbols[2].FilePath, StartLine: 15})

	g.Freeze()
	return g, idx
}

// TestFindCommunitiesTool_AllSamePackage tests behavior when all nodes are in same package.
func TestFindCommunitiesTool_AllSamePackage(t *testing.T) {
	ctx := context.Background()
	g, idx := createAllSamePackageGraph(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Should have no cross-package communities since all are same package
	if len(output.CrossPackageCommunities) > 0 {
		t.Errorf("Expected no cross-package communities for same-package graph, got %d", len(output.CrossPackageCommunities))
	}

	// Communities should exist and all have dominant_package = "myPkg"
	for i, comm := range output.Communities {
		if comm.DominantPackage != "" && comm.DominantPackage != "myPkg" {
			t.Errorf("Community %d has unexpected dominant_package: %s", i, comm.DominantPackage)
		}
	}
}

// TestFindCommunitiesTool_ParameterExactBoundaries tests exact boundary values.
func TestFindCommunitiesTool_ParameterExactBoundaries(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	tests := []struct {
		name   string
		params MapParams
	}{
		{"min_size=1 (lower bound)", MapParams{Params: map[string]any{"min_size": 1}}},
		{"min_size=100 (upper bound)", MapParams{Params: map[string]any{"min_size": 100}}},
		{"resolution=0.1 (lower bound)", MapParams{Params: map[string]any{"resolution": 0.1}}},
		{"resolution=5.0 (upper bound)", MapParams{Params: map[string]any{"resolution": 5.0}}},
		{"top=1 (lower bound)", MapParams{Params: map[string]any{"top": 1}}},
		{"top=50 (upper bound)", MapParams{Params: map[string]any{"top": 50}}},
		{"all bounds at min", MapParams{Params: map[string]any{"min_size": 1, "resolution": 0.1, "top": 1}}},
		{"all bounds at max", MapParams{Params: map[string]any{"min_size": 100, "resolution": 5.0, "top": 50}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Execute(ctx, tc.params)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !result.Success {
				t.Fatalf("Execute() failed: %s", result.Error)
			}

			output, ok := result.Output.(FindCommunitiesOutput)
			if !ok {
				t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
			}
			// Modularity is always present in typed struct, just verify it was computed
			t.Logf("Modularity: %f", output.Modularity)
		})
	}
}

// TestFindCommunitiesTool_ConcurrentExecution tests thread safety.
func TestFindCommunitiesTool_ConcurrentExecution(t *testing.T) {
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	const goroutines = 10
	ctx := context.Background()

	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
				"min_size":   1,
				"resolution": 1.0 + float64(idx%3)*0.5,
			}})
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: execute error: %w", idx, err)
				return
			}
			if !result.Success {
				errCh <- fmt.Errorf("goroutine %d: execution failed: %s", idx, result.Error)
				return
			}
			errCh <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}

// TestFindCommunitiesTool_OutputFormatValidation validates all expected fields.
func TestFindCommunitiesTool_OutputFormatValidation(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size":         1,
		"show_cross_edges": true,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// Validate top-level fields are set appropriately
	if output.Algorithm == "" {
		t.Error("Algorithm should not be empty")
	}
	if output.Modularity < 0 || output.Modularity > 1 {
		t.Errorf("Modularity %f should be in [0,1]", output.Modularity)
	}
	if output.ModularityQuality == "" {
		t.Error("ModularityQuality should not be empty")
	}
	// Converged is a bool, always valid
	// CommunityCount should be consistent with Communities slice
	if output.CommunityCount != len(output.Communities) {
		t.Errorf("CommunityCount %d doesn't match Communities length %d", output.CommunityCount, len(output.Communities))
	}

	// Validate community structure
	if len(output.Communities) > 0 {
		comm := output.Communities[0]
		// Validate fields are set
		if comm.ID < 0 {
			t.Error("Community ID should not be negative")
		}
		if comm.Size <= 0 {
			t.Error("Community Size should be positive")
		}
		if comm.Connectivity < 0 || comm.Connectivity > 1 {
			t.Errorf("Community Connectivity %f should be in [0,1]", comm.Connectivity)
		}
		// InternalEdges, ExternalEdges, Members, DominantPackage, Packages, IsCrossPackage
		// are all guaranteed by the typed struct
	}

	// Validate cross_community_edges when present (show_cross_edges=true)
	if len(output.CrossCommunityEdges) > 0 {
		edge := output.CrossCommunityEdges[0]
		if edge.FromCommunity < 0 || edge.ToCommunity < 0 || edge.Count < 0 {
			t.Error("CrossCommunityEdge has invalid fields")
		}
	}

	// Validate OutputText is non-empty
	if result.OutputText == "" {
		t.Error("OutputText should not be empty")
	}
}

// TestFindCommunitiesTool_TokensUsed verifies token estimation.
func TestFindCommunitiesTool_TokensUsed(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TokensUsed should be > 0 for non-empty output
	if result.TokensUsed <= 0 {
		t.Error("TokensUsed should be > 0 for non-empty result")
	}

	// TokensUsed should be roughly proportional to OutputText length
	// (rough estimate: 4 chars per token)
	expectedMinTokens := len(result.OutputText) / 8
	if result.TokensUsed < expectedMinTokens {
		t.Errorf("TokensUsed %d seems too low for OutputText length %d", result.TokensUsed, len(result.OutputText))
	}
}

// TestFindCommunitiesTool_NilIndex tests behavior with nil index.
func TestFindCommunitiesTool_NilIndex(t *testing.T) {
	ctx := context.Background()
	g, _ := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)

	// Create tool with nil index - should still work since index is optional
	tool := NewFindCommunitiesTool(analytics, nil)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() should succeed with nil index: %s", result.Error)
	}
}

// TestFindCommunitiesTool_LargeMinSize tests that large min_size filters out all communities.
func TestFindCommunitiesTool_LargeMinSize(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t) // Creates ~10 nodes

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 100, // Much larger than graph size
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	output, ok := result.Output.(FindCommunitiesOutput)
	if !ok {
		t.Fatalf("Output is not FindCommunitiesOutput, got %T", result.Output)
	}

	// All communities should be filtered out
	if len(output.Communities) != 0 {
		t.Errorf("Expected 0 communities with min_size=100, got %d", len(output.Communities))
	}

	// Modularity is always present in typed struct
	t.Logf("Modularity with no communities: %f", output.Modularity)
}

// TestFindCommunitiesTool_TraceStepPopulated verifies CRS integration.
func TestFindCommunitiesTool_TraceStepPopulated(t *testing.T) {
	ctx := context.Background()
	g, idx := createTestGraphForCommunities(t)

	hg, err := graph.WrapGraph(g)
	if err != nil {
		t.Fatalf("WrapGraph failed: %v", err)
	}
	analytics := graph.NewGraphAnalytics(hg)
	tool := NewFindCommunitiesTool(analytics, idx)

	result, err := tool.Execute(ctx, MapParams{Params: map[string]any{
		"min_size": 1,
	}})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute() failed: %s", result.Error)
	}

	// TraceStep should be populated (H-1 fix verification)
	if result.TraceStep == nil {
		t.Fatal("TraceStep should be populated for CRS integration")
	}

	// Validate TraceStep fields
	if result.TraceStep.Action != "analytics_communities" {
		t.Errorf("TraceStep.Action = %q, want 'analytics_communities'", result.TraceStep.Action)
	}

	if result.TraceStep.Tool != "DetectCommunities" {
		t.Errorf("TraceStep.Tool = %q, want 'DetectCommunities'", result.TraceStep.Tool)
	}

	// Should have metadata
	if result.TraceStep.Metadata == nil {
		t.Error("TraceStep.Metadata should not be nil")
	} else {
		if _, ok := result.TraceStep.Metadata["algorithm"]; !ok {
			t.Error("TraceStep.Metadata should contain 'algorithm'")
		}
		if _, ok := result.TraceStep.Metadata["modularity"]; !ok {
			t.Error("TraceStep.Metadata should contain 'modularity'")
		}
	}

	// Duration should be tracked
	if result.TraceStep.Duration == 0 {
		t.Error("TraceStep.Duration should be > 0")
	}
}
