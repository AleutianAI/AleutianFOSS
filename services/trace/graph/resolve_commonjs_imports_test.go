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
// IT-06d Bug D+E: CommonJS Import Resolution Tests
// =============================================================================
//
// These tests verify that JavaScript CommonJS require() imports create
// EdgeTypeReferences edges and that resolveViaImportMap correctly resolves
// bare-identifier calls (new Route()) to the cross-file class rather than
// the same-file alias variable.
//
// Test scenarios:
//   - semanticNameFromImportPath derives correct PascalCase names
//   - matchesImportPath strips JS extensions correctly (Bug E fix)
//   - buildImportNameMap includes CommonJS aliases (Bug E fix)
//   - resolveCommonJSAliasImportEdges creates REFERENCES edges (Bug D fix)
//   - Bug E: new Route() from Router.route resolves to Route class (not variable)
//   - Bug D: res alias in express.js references Response class
//   - External imports (no leading dot) are skipped
//   - Non-JS/TS files are skipped

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// Unit tests: semanticNameFromImportPath
// =============================================================================

func TestSemanticNameFromImportPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
		desc  string
	}{
		{"./response", "Response", "simple relative: ./response → Response"},
		{"./route", "Route", "simple relative: ./route → Route"},
		{"./router/index", "Router", "index file: ./router/index → Router (parent dir)"},
		{"../utils", "Utils", "parent-relative: ../utils → Utils"},
		{"./application", "Application", "camelCase: ./application → Application"},
		{"./request", "Request", "simple: ./request → Request"},
		{"./router/route", "Route", "nested: ./router/route → Route (leaf segment)"},
		{"./response.js", "Response", "with .js extension: ./response.js → Response"},
		{"./route.ts", "Route", "with .ts extension: ./route.ts → Route"},
		{"express", "", "external module: no leading dot → empty"},
		{"react", "", "external react: no leading dot → empty"},
		{".", "", "bare dot: cannot derive → empty"},
		{"./", "", "trailing slash only: empty name → empty"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := semanticNameFromImportPath(tc.input)
			if got != tc.want {
				t.Errorf("semanticNameFromImportPath(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Unit tests: matchesImportPath JS extension stripping (Bug E)
// =============================================================================

func TestMatchesImportPath_JSExtensions(t *testing.T) {
	cases := []struct {
		filePath   string
		importPath string
		want       bool
		desc       string
	}{
		// JS extensions must be stripped for CommonJS relative imports
		{"lib/router/route.js", "./route", true, "route.js matches ./route"},
		{"lib/response.js", "./response", true, "response.js matches ./response"},
		{"lib/application.js", "./application", true, "application.js matches ./application"},
		{"lib/router/index.js", "./router/index", true, "index.js matches ./router/index"},
		{"src/utils.ts", "./utils", true, "utils.ts matches ./utils"},
		{"components/Header.jsx", "./Header", true, "Header.jsx matches ./Header"},
		{"components/Header.tsx", "./Header", true, "Header.tsx matches ./Header"},
		{"lib/module.mjs", "./module", true, "module.mjs matches ./module"},
		{"lib/module.cjs", "./module", true, "module.cjs matches ./module"},

		// Non-matches: wrong file
		{"lib/router/index.js", "./route", false, "index.js does NOT match ./route"},
		{"lib/application.js", "./response", false, "application.js does NOT match ./response"},

		// Python extensions still work (no regression)
		{"src/flask/globals.py", ".globals", true, "Python: .globals matches globals.py"},
		{"pandas/core/reshape/merge.py", "pandas.core.reshape.merge", true, "Python: absolute import"},

		// Non-matches for Python (no regression)
		{"src/flask/globals.py", ".app", false, "Python: .app does not match globals.py"},
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
// Unit tests: buildImportNameMap includes CommonJS aliases (Bug E)
// =============================================================================

func TestBuildImportNameMap_CommonJSAlias(t *testing.T) {
	// Simulate lib/router/index.js with: var Route = require('./route')
	state := &buildState{
		symbolsByID:   make(map[string]*ast.Symbol),
		symbolsByName: make(map[string][]*ast.Symbol),
		importNameMap: make(map[string]map[string]importEntry),
		fileImports: map[string][]ast.Import{
			"lib/router/index.js": {
				{
					Path:       "./route",
					Alias:      "Route",
					IsCommonJS: true,
					Location: ast.Location{
						FilePath:  "lib/router/index.js",
						StartLine: 16,
					},
				},
			},
		},
	}

	builder := NewBuilder()
	builder.buildImportNameMap(state)

	fileMap := state.importNameMap["lib/router/index.js"]
	if fileMap == nil {
		t.Fatal("expected importNameMap entry for lib/router/index.js, got nil")
	}

	entry, ok := fileMap["Route"]
	if !ok {
		t.Fatal("expected importNameMap[lib/router/index.js][Route] to be populated")
	}
	if entry.ModulePath != "./route" {
		t.Errorf("ModulePath = %q; want %q", entry.ModulePath, "./route")
	}
	if entry.OriginalName != "Route" {
		t.Errorf("OriginalName = %q; want %q", entry.OriginalName, "Route")
	}
}

func TestBuildImportNameMap_CommonJSAliasSkipsExternal(t *testing.T) {
	// External require (no leading dot) should also be indexed, but matchesImportPath
	// will filter it out — what matters is that it doesn't panic and that local imports
	// are included correctly alongside external ones.
	state := &buildState{
		symbolsByID:   make(map[string]*ast.Symbol),
		symbolsByName: make(map[string][]*ast.Symbol),
		importNameMap: make(map[string]map[string]importEntry),
		fileImports: map[string][]ast.Import{
			"lib/app.js": {
				// External: var express = require('express')
				{Path: "express", Alias: "express", IsCommonJS: true},
				// Local: var Route = require('./route')
				{Path: "./route", Alias: "Route", IsCommonJS: true},
			},
		},
	}

	builder := NewBuilder()
	builder.buildImportNameMap(state)

	fileMap := state.importNameMap["lib/app.js"]
	if fileMap == nil {
		t.Fatal("expected importNameMap entry for lib/app.js")
	}

	// Both should be indexed (matchesImportPath handles filtering at resolution time)
	if _, ok := fileMap["Route"]; !ok {
		t.Error("expected importNameMap[lib/app.js][Route] to be indexed")
	}
}

func TestBuildImportNameMap_CommonJSAliasNoOverrideNamedImport(t *testing.T) {
	// Named import (from destructuring) takes priority over alias import.
	// If both "Route" (named) and "Route" (whole-module alias) are present,
	// the named import wins.
	state := &buildState{
		symbolsByID:   make(map[string]*ast.Symbol),
		symbolsByName: make(map[string][]*ast.Symbol),
		importNameMap: make(map[string]map[string]importEntry),
		fileImports: map[string][]ast.Import{
			"lib/app.js": {
				// Named import: const { Route } = require('express') — Names populated
				{Path: "express", Names: []string{"Route"}, IsCommonJS: true},
				// Whole-module alias: var Route = require('./route') — should NOT override
				{Path: "./route", Alias: "Route", IsCommonJS: true},
			},
		},
	}

	builder := NewBuilder()
	builder.buildImportNameMap(state)

	fileMap := state.importNameMap["lib/app.js"]
	if fileMap == nil {
		t.Fatal("expected importNameMap entry")
	}

	entry, ok := fileMap["Route"]
	if !ok {
		t.Fatal("expected importNameMap[lib/app.js][Route]")
	}
	// Named import (from "express" with Names) runs in the first pass and populates the map.
	// The CommonJS alias (./route) should NOT override it.
	if entry.ModulePath != "express" {
		t.Errorf("ModulePath = %q; want %q (named import should take priority)", entry.ModulePath, "express")
	}
}

// =============================================================================
// Integration test: Bug E — new Route() resolves to Route class, not variable
// =============================================================================
//
// Scenario (lib/router/index.js):
//   var Route = require('./route')           // CommonJS import, creates alias variable
//   proto.route = function route(path) {
//       var route = new Route(path)           // should → Route class in route.js
//   }
//
// lib/router/route.js:
//   function Route(path) { this.path = path }  // constructor → SymbolKindClass
//
// Without fix: resolveSymbolByName("Route") returns same-file variable first.
// With fix: resolveViaImportMap redirects to Route class via importNameMap.

func TestBugE_NewRouteResolvesToRouteClass(t *testing.T) {
	// Build parse results simulating the express lib/router structure.
	routeClassID := "lib/router/route.js:1:Route"
	routeVarID := "lib/router/index.js:16:Route"
	routeMethodID := "lib/router/index.js:500:Router.route"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "lib/router/route.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					// Route constructor: isConstructorFunction → SymbolKindClass
					ID:        routeClassID,
					Name:      "Route",
					Kind:      ast.SymbolKindClass,
					FilePath:  "lib/router/route.js",
					StartLine: 1,
					EndLine:   15,
					Language:  "javascript",
					Exported:  true,
				},
			},
		},
		{
			FilePath: "lib/router/index.js",
			Language: "javascript",
			// var Route = require('./route')
			Imports: []ast.Import{
				{
					Path:       "./route",
					Alias:      "Route",
					IsCommonJS: true,
					Location: ast.Location{
						FilePath:  "lib/router/index.js",
						StartLine: 16,
					},
				},
			},
			Symbols: []*ast.Symbol{
				{
					// The alias variable created by var Route = require('./route')
					ID:        routeVarID,
					Name:      "Route",
					Kind:      ast.SymbolKindVariable,
					FilePath:  "lib/router/index.js",
					StartLine: 16,
					EndLine:   16,
					Language:  "javascript",
				},
				{
					// proto.route = function route(path) { var route = new Route(path) }
					// Receiver = "Router" (from exportAliases["proto"] = "Router")
					ID:        routeMethodID,
					Name:      "route",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "lib/router/index.js",
					StartLine: 500,
					EndLine:   510,
					Language:  "javascript",
					Receiver:  "Router",
					Exported:  true,
					Calls: []ast.CallSite{
						// new Route(path) → Target="Route", IsMethod=false
						{
							Target:   "Route",
							IsMethod: false,
							Location: ast.Location{FilePath: "lib/router/index.js", StartLine: 503},
						},
					},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := result.Graph
	t.Logf("Build stats: nodes=%d edges=%d call_resolved=%d call_unresolved=%d commonjs=%d",
		result.Stats.NodesCreated, result.Stats.EdgesCreated,
		result.Stats.CallEdgesResolved, result.Stats.CallEdgesUnresolved,
		result.Stats.CommonJSImportEdgesResolved)

	// The Route CLASS (in route.js) must have incoming references.
	refs, err := g.FindReferencesByID(context.Background(), routeClassID)
	if err != nil {
		t.Fatalf("FindReferencesByID failed: %v", err)
	}

	t.Logf("Route class incoming edges: %v", incomingEdgeList(g, routeClassID))
	t.Logf("Route class references: %d", len(refs))
	for _, loc := range refs {
		t.Logf("  reference at %s:%d", loc.FilePath, loc.StartLine)
	}

	if len(refs) == 0 {
		t.Error("BUG E: Route class has 0 incoming references; expected reference from lib/router/index.js")
	}

	// Verify at least one reference is from lib/router/index.js
	foundIndexRef := false
	for _, loc := range refs {
		if loc.FilePath == "lib/router/index.js" {
			foundIndexRef = true
			break
		}
	}
	if !foundIndexRef {
		t.Error("BUG E: no reference from lib/router/index.js to Route class")
	}

	// The Route VARIABLE (same-file alias) should NOT be the sole/primary reference
	// target for the new Route() call site. Verify the CALLS edge from Router.route
	// points to the CLASS (route.js), not to the VARIABLE (index.js).
	allEdges := g.Edges()
	foundCallToClass := false
	for _, e := range allEdges {
		if e.FromID == routeMethodID && e.ToID == routeClassID && e.Type == EdgeTypeCalls {
			foundCallToClass = true
			t.Logf("CALLS edge: %s → %s ✓", e.FromID, e.ToID)
			break
		}
	}
	if !foundCallToClass {
		// Log what call edges exist from routeMethodID
		for _, e := range allEdges {
			if e.FromID == routeMethodID {
				t.Logf("  call edge from Router.route: → %s (type=%v)", e.ToID, e.Type)
			}
		}
		t.Error("BUG E: Router.route CALLS edge does not point to Route class in route.js")
	}
}

// =============================================================================
// Integration test: Bug D — res alias references Response synthetic class
// =============================================================================
//
// Scenario (lib/express.js):
//   var res = require('./response')                    // CommonJS import
//   app.response = Object.create(res, {app: ...})      // top-level, NOT in a function body
//
// lib/response.js:
//   var res = Object.create(http.ServerResponse.prototype)
//   module.exports = res                               // → synthetic "Response" class
//   res.send = function(body) { ... }                  // methods on res → Receiver="Response"
//
// Without fix: no REFERENCES edge → express.js invisible in find_references("Response").
// With fix: res variable in express.js → REFERENCES → Response class in response.js.

func TestBugD_ResAliasReferencesResponseClass(t *testing.T) {
	responseClassID := "lib/response.js:1:Response"
	resVarExpressID := "lib/express.js:22:res"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "lib/response.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					// Synthetic class emitted by emitSyntheticClassSymbols
					ID:        responseClassID,
					Name:      "Response",
					Kind:      ast.SymbolKindClass,
					FilePath:  "lib/response.js",
					StartLine: 1,
					EndLine:   1,
					Language:  "javascript",
					Exported:  true,
				},
			},
		},
		{
			FilePath: "lib/express.js",
			Language: "javascript",
			Imports: []ast.Import{
				{
					Path:       "./response",
					Alias:      "res",
					IsCommonJS: true,
					Location: ast.Location{
						FilePath:  "lib/express.js",
						StartLine: 22,
					},
				},
			},
			Symbols: []*ast.Symbol{
				{
					// var res = require('./response') — creates alias variable
					ID:        resVarExpressID,
					Name:      "res",
					Kind:      ast.SymbolKindVariable,
					FilePath:  "lib/express.js",
					StartLine: 22,
					EndLine:   22,
					Language:  "javascript",
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := result.Graph
	t.Logf("Build stats: commonjs_edges=%d", result.Stats.CommonJSImportEdgesResolved)

	// The Response CLASS must have an incoming REFERENCES edge from lib/express.js
	refs, err := g.FindReferencesByID(context.Background(), responseClassID)
	if err != nil {
		t.Fatalf("FindReferencesByID failed: %v", err)
	}

	t.Logf("Response class incoming edges: %v", incomingEdgeList(g, responseClassID))
	t.Logf("Response class references: %d", len(refs))
	for _, loc := range refs {
		t.Logf("  reference at %s:%d", loc.FilePath, loc.StartLine)
	}

	if len(refs) == 0 {
		t.Error("BUG D: Response class has 0 incoming references; expected reference from lib/express.js")
	}

	foundExpressRef := false
	for _, loc := range refs {
		if loc.FilePath == "lib/express.js" {
			foundExpressRef = true
			t.Logf("express.js reference at line %d ✓", loc.StartLine)
			break
		}
	}
	if !foundExpressRef {
		t.Error("BUG D: no reference from lib/express.js to Response class")
	}

	if result.Stats.CommonJSImportEdgesResolved == 0 {
		t.Error("expected CommonJSImportEdgesResolved > 0")
	}
}

// =============================================================================
// Edge case: external modules are NOT linked (Bug D scope guard)
// =============================================================================

func TestBugD_ExternalRequireNotLinked(t *testing.T) {
	// var express = require('express') — external module, no leading dot
	// Should NOT create a REFERENCES edge to any internal symbol named "Express"
	expressClassID := "lib/express.js:1:Express"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "lib/express.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        expressClassID,
					Name:      "Express",
					Kind:      ast.SymbolKindClass,
					FilePath:  "lib/express.js",
					StartLine: 1,
					EndLine:   1,
					Language:  "javascript",
					Exported:  true,
				},
			},
		},
		{
			FilePath: "lib/app.js",
			Language: "javascript",
			Imports: []ast.Import{
				{
					// External module — should be skipped
					Path:       "express",
					Alias:      "express",
					IsCommonJS: true,
					Location:   ast.Location{FilePath: "lib/app.js", StartLine: 1},
				},
			},
			Symbols: []*ast.Symbol{
				{
					ID:        "lib/app.js:1:express",
					Name:      "express",
					Kind:      ast.SymbolKindVariable,
					FilePath:  "lib/app.js",
					StartLine: 1,
					EndLine:   1,
					Language:  "javascript",
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := result.Graph

	// Express class in lib/express.js should NOT get a REFERENCES edge from app.js
	refs, err := g.FindReferencesByID(context.Background(), expressClassID)
	if err != nil {
		t.Fatalf("FindReferencesByID failed: %v", err)
	}

	// semanticNameFromImportPath("express") returns "" (no leading dot) → no edge
	for _, loc := range refs {
		if loc.FilePath == "lib/app.js" {
			t.Errorf("external require('express') should NOT create REFERENCES edge; got reference from %s:%d",
				loc.FilePath, loc.StartLine)
		}
	}

	if result.Stats.CommonJSImportEdgesResolved != 0 {
		t.Errorf("expected 0 CommonJS edges for external module, got %d",
			result.Stats.CommonJSImportEdgesResolved)
	}
}
