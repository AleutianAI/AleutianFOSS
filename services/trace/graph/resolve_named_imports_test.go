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

// =============================================================================
// GR-62: Named Import Resolution Tests
// =============================================================================
//
// These tests verify that Python "from X import Y" statements create
// EdgeTypeReferences edges pointing at the Y symbol node, enabling
// FindReferencesByID to return cross-file callers.
//
// Test scenarios:
//   - Simple named import (from .globals import request)
//   - Multi-name import (from .globals import request, g)
//   - Aliased import (from .globals import request as req)
//   - External library import (from flask import Flask — not in project index)
//   - Wildcard import (from .globals import *) — must be skipped
//   - Circular imports (A imports from B, B imports from A) — no infinite loop
//   - Non-Python results — must be skipped entirely
//   - Missing package symbol — skipped gracefully without panic

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// Helpers
// =============================================================================

// makePythonPackageSymbol creates a minimal SymbolKindPackage symbol for a Python file.
// The builder's findPackageSymbolID searches r.Symbols for a SymbolKindPackage.
// StartLine/EndLine must be set to pass Symbol.Validate() (StartLine >= 1, EndLine >= StartLine).
func makePythonPackageSymbol(filePath, pkgName string) *ast.Symbol {
	return &ast.Symbol{
		ID:        ast.GenerateID(filePath, 1, pkgName),
		Name:      pkgName,
		Kind:      ast.SymbolKindPackage,
		FilePath:  filePath,
		Language:  "python",
		Exported:  true,
		StartLine: 1,
		EndLine:   1,
	}
}

// makePythonVariable creates a module-level variable symbol (like flask's `request`).
// StartLine/EndLine must be set to pass Symbol.Validate() (StartLine >= 1, EndLine >= StartLine).
func makePythonVariable(filePath string, line int, name string) *ast.Symbol {
	return &ast.Symbol{
		ID:        ast.GenerateID(filePath, line, name),
		Name:      name,
		Kind:      ast.SymbolKindVariable,
		FilePath:  filePath,
		Language:  "python",
		Exported:  true,
		StartLine: line,
		EndLine:   line,
	}
}

// makeImport builds an ast.Import for a "from Path import Names..." statement.
func makeImport(path string, names []string, isRelative, isWildcard bool) ast.Import {
	return ast.Import{
		Path:       path,
		Names:      names,
		IsRelative: isRelative,
		IsWildcard: isWildcard,
	}
}

// countEdgesOfType counts edges of a specific type in node.Incoming.
func countEdgesOfType(g *Graph, nodeID string, edgeType EdgeType) int {
	node, ok := g.GetNode(nodeID)
	if !ok {
		return 0
	}
	count := 0
	for _, e := range node.Incoming {
		if e.Type == edgeType {
			count++
		}
	}
	return count
}

// hasEdge checks whether an edge of the given type exists from→to.
func hasEdge(g *Graph, fromID, toID string, edgeType EdgeType) bool {
	node, ok := g.GetNode(toID)
	if !ok {
		return false
	}
	for _, e := range node.Incoming {
		if e.Type == edgeType && e.FromID == fromID {
			return true
		}
	}
	return false
}

// =============================================================================
// Tests
// =============================================================================

// TestResolveNamedImportEdges_SimpleNamedImport verifies the core case:
// "from flask.globals import request" in app.py creates an EdgeTypeReferences
// edge from app.py's package symbol to flask.globals.request.
func TestResolveNamedImportEdges_SimpleNamedImport(t *testing.T) {
	// Define symbols
	//   flask/globals.py  contains `request` (variable, line 46)
	//   flask/app.py      does `from .globals import request`

	globalsVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")

	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")

	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, globalsVar},
			Imports:  []ast.Import{},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				makeImport("flask.globals", []string{"request"}, true, false),
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Incomplete {
		t.Fatal("Build incomplete")
	}

	// flask.globals.request should have one incoming EdgeTypeReferences from app.py's pkg sym.
	targetID := globalsVar.ID
	sourceID := appPkg.ID

	if !hasEdge(result.Graph, sourceID, targetID, EdgeTypeReferences) {
		t.Errorf("expected EdgeTypeReferences from %q to %q; got incoming edges: %v",
			sourceID, targetID, incomingEdgeList(result.Graph, targetID))
	}

	if result.Stats.NamedImportEdgesResolved == 0 {
		t.Error("expected NamedImportEdgesResolved > 0")
	}
}

// TestResolveNamedImportEdges_MultiNameImport verifies that
// "from .globals import request, g" creates two separate edges.
func TestResolveNamedImportEdges_MultiNameImport(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	gVar := makePythonVariable("flask/globals.py", 60, "g")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")

	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")

	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar, gVar},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				makeImport("flask.globals", []string{"request", "g"}, true, false),
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !hasEdge(result.Graph, appPkg.ID, requestVar.ID, EdgeTypeReferences) {
		t.Errorf("expected edge to request; incoming: %v", incomingEdgeList(result.Graph, requestVar.ID))
	}
	if !hasEdge(result.Graph, appPkg.ID, gVar.ID, EdgeTypeReferences) {
		t.Errorf("expected edge to g; incoming: %v", incomingEdgeList(result.Graph, gVar.ID))
	}
	if result.Stats.NamedImportEdgesResolved < 2 {
		t.Errorf("expected NamedImportEdgesResolved >= 2, got %d", result.Stats.NamedImportEdgesResolved)
	}
}

// TestResolveNamedImportEdges_AliasedImport verifies that
// "from .globals import request as req" still creates the edge pointing at
// the original `request` symbol (not `req`, which is the local alias).
func TestResolveNamedImportEdges_AliasedImport(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")
	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")

	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				// "request as req" — local name is req, original is request.
				// parseAliasedName("request as req") → (localName="req", originalName="request")
				makeImport("flask.globals", []string{"request as req"}, true, false),
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Edge must point at the original symbol (request), not a "req" symbol.
	if !hasEdge(result.Graph, appPkg.ID, requestVar.ID, EdgeTypeReferences) {
		t.Errorf("expected edge to original 'request' symbol via aliased import; incoming: %v",
			incomingEdgeList(result.Graph, requestVar.ID))
	}
}

// TestResolveNamedImportEdges_ExternalLibrarySkipped verifies that importing
// from an external library that has no symbol in the index is silently skipped.
func TestResolveNamedImportEdges_ExternalLibrarySkipped(t *testing.T) {
	appPkg := makePythonPackageSymbol("myapp/app.py", "myapp.app")

	results := []*ast.ParseResult{
		{
			FilePath: "myapp/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				// Flask is not in this project's index — should be silently skipped.
				makeImport("flask", []string{"Flask", "request"}, false, false),
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// No error should be produced for unresolved external imports.
	for _, ee := range result.EdgeErrors {
		if ee.EdgeType == EdgeTypeReferences {
			t.Errorf("unexpected edge error for external import: %v", ee.Err)
		}
	}
	// Stat should stay 0 — nothing was resolved.
	if result.Stats.NamedImportEdgesResolved != 0 {
		t.Errorf("expected 0 resolved for external-only imports, got %d",
			result.Stats.NamedImportEdgesResolved)
	}
}

// TestResolveNamedImportEdges_WildcardSkipped verifies that "from X import *"
// is not processed (we can't enumerate what * means statically).
func TestResolveNamedImportEdges_WildcardSkipped(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")
	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")

	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				makeImport("flask.globals", nil, true, true), // IsWildcard=true
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Wildcard: no named import edges should be created.
	if result.Stats.NamedImportEdgesResolved != 0 {
		t.Errorf("expected 0 resolved for wildcard import, got %d", result.Stats.NamedImportEdgesResolved)
	}
	if count := countEdgesOfType(result.Graph, requestVar.ID, EdgeTypeReferences); count != 0 {
		t.Errorf("expected 0 EdgeTypeReferences on request, got %d", count)
	}
}

// TestResolveNamedImportEdges_CircularImports verifies that circular imports
// (A imports from B, B imports from A) do not cause infinite loops.
// The pass does not recurse — it just iterates imports once — so this is
// structural, but we test it explicitly for regression safety.
func TestResolveNamedImportEdges_CircularImports(t *testing.T) {
	aVar := makePythonVariable("pkg/a.py", 10, "alpha")
	bVar := makePythonVariable("pkg/b.py", 10, "beta")
	aPkg := makePythonPackageSymbol("pkg/a.py", "pkg.a")
	bPkg := makePythonPackageSymbol("pkg/b.py", "pkg.b")

	results := []*ast.ParseResult{
		{
			FilePath: "pkg/a.py",
			Language: "python",
			Symbols:  []*ast.Symbol{aPkg, aVar},
			Imports:  []ast.Import{makeImport("pkg.b", []string{"beta"}, false, false)},
		},
		{
			FilePath: "pkg/b.py",
			Language: "python",
			Symbols:  []*ast.Symbol{bPkg, bVar},
			Imports:  []ast.Import{makeImport("pkg.a", []string{"alpha"}, false, false)},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Both edges should exist (A→beta, B→alpha) without deadlock or panic.
	if !hasEdge(result.Graph, aPkg.ID, bVar.ID, EdgeTypeReferences) {
		t.Errorf("expected edge a→beta; incoming: %v", incomingEdgeList(result.Graph, bVar.ID))
	}
	if !hasEdge(result.Graph, bPkg.ID, aVar.ID, EdgeTypeReferences) {
		t.Errorf("expected edge b→alpha; incoming: %v", incomingEdgeList(result.Graph, aVar.ID))
	}
	if result.Stats.NamedImportEdgesResolved < 2 {
		t.Errorf("expected >= 2 resolved, got %d", result.Stats.NamedImportEdgesResolved)
	}
}

// TestResolveNamedImportEdges_NonPythonSkipped verifies that Go/JS/TS results
// are not processed — the pass is Python-only.
func TestResolveNamedImportEdges_NonPythonSkipped(t *testing.T) {
	// A Go function that happens to share a name with a Python variable.
	goPkg := &ast.Symbol{
		ID:        ast.GenerateID("pkg/handler.go", 1, "handler"),
		Name:      "handler",
		Kind:      ast.SymbolKindPackage,
		FilePath:  "pkg/handler.go",
		Language:  "go",
		StartLine: 1,
		EndLine:   1,
	}
	goFn := &ast.Symbol{
		ID:        ast.GenerateID("pkg/handler.go", 10, "Handle"),
		Name:      "Handle",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "pkg/handler.go",
		Language:  "go",
		StartLine: 10,
		EndLine:   15,
	}

	results := []*ast.ParseResult{
		{
			FilePath: "pkg/handler.go",
			Language: "go",
			Symbols:  []*ast.Symbol{goPkg, goFn},
			Imports:  []ast.Import{makeImport("fmt", []string{"Println"}, false, false)},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// No named import edges for Go.
	if result.Stats.NamedImportEdgesResolved != 0 {
		t.Errorf("expected 0 for non-Python, got %d", result.Stats.NamedImportEdgesResolved)
	}
}

// TestResolveNamedImportEdges_MultipleImporters verifies that multiple files
// importing the same symbol each produce their own edge (N importing files →
// N incoming edges on the target symbol).
func TestResolveNamedImportEdges_MultipleImporters(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")

	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")
	loggingPkg := makePythonPackageSymbol("flask/logging.py", "flask.logging")
	templatingPkg := makePythonPackageSymbol("flask/templating.py", "flask.templating")

	imp := makeImport("flask.globals", []string{"request"}, true, false)

	results := []*ast.ParseResult{
		{FilePath: "flask/globals.py", Language: "python", Symbols: []*ast.Symbol{globalsPkg, requestVar}},
		{FilePath: "flask/app.py", Language: "python", Symbols: []*ast.Symbol{appPkg}, Imports: []ast.Import{imp}},
		{FilePath: "flask/logging.py", Language: "python", Symbols: []*ast.Symbol{loggingPkg}, Imports: []ast.Import{imp}},
		{FilePath: "flask/templating.py", Language: "python", Symbols: []*ast.Symbol{templatingPkg}, Imports: []ast.Import{imp}},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	count := countEdgesOfType(result.Graph, requestVar.ID, EdgeTypeReferences)
	if count < 3 {
		t.Errorf("expected >= 3 incoming EdgeTypeReferences on request, got %d", count)
	}
	if result.Stats.NamedImportEdgesResolved < 3 {
		t.Errorf("expected >= 3 NamedImportEdgesResolved, got %d", result.Stats.NamedImportEdgesResolved)
	}
}

// TestResolveNamedImportEdges_MissingPackageSymbol verifies that a file with no
// SymbolKindPackage symbol is gracefully skipped without panic or error.
func TestResolveNamedImportEdges_MissingPackageSymbol(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")

	// app.py has imports but NO package symbol — parser may produce this for
	// malformed or stub files.
	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{}, // no package symbol
			Imports:  []ast.Import{makeImport("flask.globals", []string{"request"}, true, false)},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// No edges from app.py (it has no source node), no panic, no error.
	for _, ee := range result.EdgeErrors {
		t.Errorf("unexpected edge error: from=%s to=%s err=%v", ee.FromID, ee.ToID, ee.Err)
	}
	if count := countEdgesOfType(result.Graph, requestVar.ID, EdgeTypeReferences); count != 0 {
		t.Errorf("expected 0 edges (no source), got %d", count)
	}
}

// TestResolveNamedImportEdges_RelativeImportPath verifies that the Python parser's
// actual import path format is handled correctly. The Python parser stores relative
// imports with their leading dot(s) intact: "from .globals import request" is stored
// as imp.Path = ".globals", not "globals". matchesImportPath must strip the leading
// dot before converting to a file path fragment.
//
// This is the root cause of the GR-62 production failure found in integration test
// 6004: Flask stores all intra-package imports as ".globals", ".cli", etc.
func TestResolveNamedImportEdges_RelativeImportPath(t *testing.T) {
	// Mirrors the actual Flask codebase: globals.py defines `request`, and
	// app.py imports it as "from .globals import request" (stored as ".globals").
	requestVar := makePythonVariable("src/flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("src/flask/globals.py", "flask.globals")
	appPkg := makePythonPackageSymbol("src/flask/app.py", "flask.app")

	results := []*ast.ParseResult{
		{
			FilePath: "src/flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar},
		},
		{
			FilePath: "src/flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				// ".globals" is exactly what the Python parser stores for "from .globals import request".
				makeImport(".globals", []string{"request"}, true, false),
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Incomplete {
		t.Fatal("Build incomplete")
	}

	targetID := requestVar.ID
	sourceID := appPkg.ID

	if !hasEdge(result.Graph, sourceID, targetID, EdgeTypeReferences) {
		t.Errorf("expected EdgeTypeReferences from %q to %q via relative import \".globals\"; got: %v",
			sourceID, targetID, incomingEdgeList(result.Graph, targetID))
	}
	if result.Stats.NamedImportEdgesResolved == 0 {
		t.Error("expected NamedImportEdgesResolved > 0")
	}
}

// TestResolveNamedImportEdges_EdgeLocationPreserved verifies that the created edge
// carries the import statement's file:line location so FindReferencesByID can return
// distinct per-file references (not collapsed to ":0" by the deduplication key).
//
// Root cause addressed: before GR-62b, AddEdge was called with ast.Location{} (empty),
// so all named-import edges had FilePath="" StartLine=0. The find_references tool
// deduplicates by "file:line" key, collapsing every edge to a single ":0" entry.
func TestResolveNamedImportEdges_EdgeLocationPreserved(t *testing.T) {
	requestVar := makePythonVariable("flask/globals.py", 46, "request")
	globalsPkg := makePythonPackageSymbol("flask/globals.py", "flask.globals")
	appPkg := makePythonPackageSymbol("flask/app.py", "flask.app")

	importLoc := ast.Location{
		FilePath:  "flask/app.py",
		StartLine: 37,
		EndLine:   37,
	}

	results := []*ast.ParseResult{
		{
			FilePath: "flask/globals.py",
			Language: "python",
			Symbols:  []*ast.Symbol{globalsPkg, requestVar},
		},
		{
			FilePath: "flask/app.py",
			Language: "python",
			Symbols:  []*ast.Symbol{appPkg},
			Imports: []ast.Import{
				{
					Path:       "flask.globals",
					Names:      []string{"request"},
					IsRelative: true,
					Location:   importLoc,
				},
			},
		},
	}

	b := NewBuilder()
	result, err := b.Build(context.Background(), results)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	targetNode, ok := result.Graph.GetNode(requestVar.ID)
	if !ok {
		t.Fatal("request node not found")
	}

	found := false
	for _, e := range targetNode.Incoming {
		if e.Type == EdgeTypeReferences {
			found = true
			if e.Location.FilePath == "" {
				t.Errorf("edge location FilePath is empty — dedup will collapse to ':0'; want %q", importLoc.FilePath)
			} else if e.Location.FilePath != importLoc.FilePath {
				t.Errorf("edge location FilePath = %q; want %q", e.Location.FilePath, importLoc.FilePath)
			}
			if e.Location.StartLine != importLoc.StartLine {
				t.Errorf("edge location StartLine = %d; want %d", e.Location.StartLine, importLoc.StartLine)
			}
		}
	}
	if !found {
		t.Error("no EdgeTypeReferences edge found on request node")
	}
}

// TestMatchesImportPath_RelativeImports directly tests matchesImportPath's handling
// of Python relative import paths (dot-prefixed) as stored by the Python parser.
func TestMatchesImportPath_RelativeImports(t *testing.T) {
	cases := []struct {
		filePath   string
		importPath string
		want       bool
		desc       string
	}{
		// Relative imports (parser stores leading dot)
		{"src/flask/globals.py", ".globals", true, "single-dot relative: .globals matches globals.py"},
		{"src/flask/globals.py", "..globals", true, "double-dot relative: ..globals still matches globals.py (fragment match)"},
		{"src/pandas/core/reshape/merge.py", ".merge", true, "single-dot relative: .merge matches merge.py"},

		// Absolute imports (unchanged behavior)
		{"src/flask/globals.py", "flask.globals", true, "absolute: flask.globals matches src/flask/globals.py"},
		{"pandas/core/reshape/merge.py", "pandas.core.reshape.merge", true, "absolute deep path"},

		// Non-matches
		{"src/flask/globals.py", ".app", false, ".app should not match globals.py"},
		{"src/flask/globals.py", ".", false, "bare dot (from . import x) should not match"},
		{"src/flask/globals.py", "flask.app", false, "flask.app should not match globals.py"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := matchesImportPath(tc.filePath, tc.importPath)
			if got != tc.want {
				t.Errorf("matchesImportPath(%q, %q) = %v; want %v", tc.filePath, tc.importPath, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Helper: edge list for test diagnostics
// =============================================================================

func incomingEdgeList(g *Graph, nodeID string) []string {
	node, ok := g.GetNode(nodeID)
	if !ok {
		return []string{"(node not found)"}
	}
	out := make([]string, 0, len(node.Incoming))
	for _, e := range node.Incoming {
		out = append(out, e.FromID+":"+e.Type.String())
	}
	return out
}
