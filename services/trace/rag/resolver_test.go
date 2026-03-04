// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// AGPL v3 - See LICENSE.txt and NOTICE.txt

package rag

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// buildTestIndex creates a SymbolIndex with realistic symbols for testing.
func buildTestIndex(t *testing.T) *index.SymbolIndex {
	t.Helper()
	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		{
			ID: "pkg/materials/materials.go:10:FindHotspots", Name: "FindHotspots",
			Kind: ast.SymbolKindFunction, Package: "pkg/materials",
			FilePath: "pkg/materials/materials.go", StartLine: 10, EndLine: 20,
			Exported: true, Language: "go", Signature: "func FindHotspots(ctx context.Context) ([]Hotspot, error)",
		},
		{
			ID: "pkg/materials/types.go:5:Material", Name: "Material",
			Kind: ast.SymbolKindStruct, Package: "pkg/materials",
			FilePath: "pkg/materials/types.go", StartLine: 5, EndLine: 15,
			Exported: true, Language: "go",
		},
		{
			ID: "pkg/render/engine.go:20:DrawFrame", Name: "DrawFrame",
			Kind: ast.SymbolKindFunction, Package: "pkg/render",
			FilePath: "pkg/render/engine.go", StartLine: 20, EndLine: 40,
			Exported: true, Language: "go", Signature: "func DrawFrame(ctx context.Context, scene *Scene) error",
		},
		{
			ID: "pkg/render/engine.go:50:Renderer", Name: "Renderer",
			Kind: ast.SymbolKindInterface, Package: "pkg/render",
			FilePath: "pkg/render/engine.go", StartLine: 50, EndLine: 60,
			Exported: true, Language: "go",
		},
		{
			ID: "pkg/core/handler.go:10:HandleAgent", Name: "HandleAgent",
			Kind: ast.SymbolKindFunction, Package: "pkg/core",
			FilePath: "pkg/core/handler.go", StartLine: 10, EndLine: 30,
			Exported: true, Language: "go", Signature: "func HandleAgent(ctx context.Context, req *Request) error",
		},
		{
			ID: "pkg/core/handler.go:40:handleInternal", Name: "handleInternal",
			Kind: ast.SymbolKindFunction, Package: "pkg/core",
			FilePath: "pkg/core/handler.go", StartLine: 40, EndLine: 50,
			Exported: false, Language: "go",
		},
		{
			ID: "internal/render/soft.go:5:SoftRenderer", Name: "SoftRenderer",
			Kind: ast.SymbolKindStruct, Package: "internal/render",
			FilePath: "internal/render/soft.go", StartLine: 5, EndLine: 25,
			Exported: true, Language: "go",
		},
	}

	if err := idx.AddBatch(symbols); err != nil {
		t.Fatalf("AddBatch failed: %v", err)
	}
	return idx
}

func TestNewStructuralResolver(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	pkgs := r.PackageNames()
	if len(pkgs) == 0 {
		t.Fatal("expected non-empty package names")
	}

	// Should contain all unique packages.
	pkgSet := make(map[string]bool)
	for _, p := range pkgs {
		pkgSet[p] = true
	}

	for _, want := range []string{"pkg/materials", "pkg/render", "pkg/core", "internal/render"} {
		if !pkgSet[want] {
			t.Errorf("expected package %q in %v", want, pkgs)
		}
	}
}

func TestResolve_ExactPackage(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	result := r.Resolve(ctx, "find hotspots in pkg/materials")

	found := false
	for _, e := range result.ResolvedEntities {
		if e.Resolved == "pkg/materials" && e.Kind == "package" {
			found = true
			if e.Confidence != 1.0 {
				t.Errorf("exact package match should have confidence 1.0, got %f", e.Confidence)
			}
		}
	}
	if !found {
		t.Errorf("expected pkg/materials in resolved entities, got %+v", result.ResolvedEntities)
	}
}

func TestResolve_LastSegmentPackage(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	result := r.Resolve(ctx, "find hotspots in materials")

	found := false
	for _, e := range result.ResolvedEntities {
		if e.Resolved == "pkg/materials" && e.Kind == "package" {
			found = true
			if e.Confidence < 0.8 {
				t.Errorf("last-segment match should have confidence >= 0.8, got %f", e.Confidence)
			}
		}
	}
	if !found {
		t.Errorf("expected pkg/materials resolved from 'materials', got %+v", result.ResolvedEntities)
	}
}

func TestResolve_AmbiguousPackage(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	// "render" matches both "pkg/render" and "internal/render"
	result := r.Resolve(ctx, "find hotspots in render")

	found := false
	for _, e := range result.ResolvedEntities {
		if e.Kind == "package" && len(e.Candidates) > 1 {
			found = true
			if e.Confidence >= 0.9 {
				t.Errorf("ambiguous match should have confidence < 0.9, got %f", e.Confidence)
			}
		}
	}
	if !found {
		t.Errorf("expected ambiguous package match for 'render', got %+v", result.ResolvedEntities)
	}
}

func TestResolve_SymbolName(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	result := r.Resolve(ctx, `find callers of "HandleAgent"`)

	found := false
	for _, e := range result.ResolvedEntities {
		if e.Raw == "HandleAgent" && e.Confidence == 1.0 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected HandleAgent resolved with confidence 1.0, got %+v", result.ResolvedEntities)
	}
}

func TestResolve_PreferExported(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	// "HandleAgent" is exported, "handleInternal" is not.
	result := r.Resolve(ctx, "tell me about HandleAgent")

	for _, e := range result.ResolvedEntities {
		if e.Raw == "HandleAgent" {
			if e.Resolved != "pkg/core.HandleAgent" {
				t.Errorf("expected exported symbol resolved as pkg/core.HandleAgent, got %q", e.Resolved)
			}
		}
	}
}

func TestResolve_PackageNamesInContext(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	result := r.Resolve(ctx, "anything")

	if len(result.PackageNames) == 0 {
		t.Error("expected non-empty PackageNames in ExtractionContext")
	}
	if result.SymbolCount == 0 {
		t.Error("expected non-zero SymbolCount in ExtractionContext")
	}
}

func TestResolve_NoMatchesReturnsEmptyEntities(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	result := r.Resolve(ctx, "tell me about quantum physics")

	if len(result.ResolvedEntities) != 0 {
		t.Errorf("expected no resolved entities for unrelated query, got %+v", result.ResolvedEntities)
	}
	// PackageNames and SymbolCount should still be populated.
	if len(result.PackageNames) == 0 {
		t.Error("PackageNames should still be populated even with no matches")
	}
}

func TestResolve_ContextCancellation(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result := r.Resolve(ctx, "find hotspots in materials package render core")
	// Should return early without panicking.
	if result == nil {
		t.Fatal("expected non-nil result even with cancelled context")
	}
}

func TestResolveDetailed_ReturnsUnresolvedTokens(t *testing.T) {
	idx := buildTestIndex(t)
	r := NewStructuralResolver(idx)

	ctx := context.Background()
	_, detail := r.ResolveDetailed(ctx, "find hotspots in materials and quantum")

	// "materials" should be resolved (package), "quantum" should be unresolved.
	if len(detail.UnresolvedTokens) == 0 {
		t.Error("expected unresolved tokens for 'quantum'")
	}

	foundQuantum := false
	for _, tok := range detail.UnresolvedTokens {
		if tok == "quantum" {
			foundQuantum = true
		}
	}
	if !foundQuantum {
		t.Errorf("expected 'quantum' in unresolved tokens, got %v", detail.UnresolvedTokens)
	}
}

func TestCombinedResolver_StructuralOnly(t *testing.T) {
	idx := buildTestIndex(t)
	structural := NewStructuralResolver(idx)

	// Combined resolver with nil semantic → structural-only mode.
	combined := NewCombinedResolver(structural, nil)

	ctx := context.Background()
	result := combined.Resolve(ctx, "find hotspots in materials")

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	found := false
	for _, e := range result.ResolvedEntities {
		if e.Resolved == "pkg/materials" && e.Kind == "package" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pkg/materials in resolved entities, got %+v", result.ResolvedEntities)
	}
}

func TestResolverInterface(t *testing.T) {
	idx := buildTestIndex(t)
	structural := NewStructuralResolver(idx)
	combined := NewCombinedResolver(structural, nil)

	// Both should satisfy the Resolver interface.
	var _ Resolver = structural
	var _ Resolver = combined
}
