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
// IT-06e Bug 4: Dynamic import() Resolution Tests
// =============================================================================
//
// These tests verify that Import{IsDynamic: true, Path: "./Module"} entries
// produced by the parser's extractDynamicImports pass are resolved to
// EdgeTypeReferences edges by the builder's resolveDynamicImportEdges pass.
//
// Test scenarios:
//   - Relative dynamic import creates REFERENCES edge from package symbol to class
//   - External (non-relative) dynamic import does not create an edge
//   - Non-JS/TS files are skipped
//   - DynamicImportEdgesResolved stat is incremented correctly

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// TestResolveDynamicImportEdges_Success verifies that a relative dynamic import
// creates a REFERENCES edge from the file's package symbol to the lazily loaded class.
func TestResolveDynamicImportEdges_Success(t *testing.T) {
	// Simulates:
	//   // src/app.js
	//   const LazyComponent = React.lazy(() => import('./HeavyComponent'))
	//
	//   // src/HeavyComponent.js
	//   export class HeavyComponent { render() { return null; } }

	heavyClassID := "src/HeavyComponent.js:1:HeavyComponent"
	appPackageID := "src/app.js:1:app"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/HeavyComponent.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        heavyClassID,
					Name:      "HeavyComponent",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/HeavyComponent.js",
					StartLine: 1,
					EndLine:   5,
					Language:  "javascript",
					Exported:  true,
				},
			},
		},
		{
			FilePath: "src/app.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        appPackageID,
					Name:      "app",
					Kind:      ast.SymbolKindPackage,
					FilePath:  "src/app.js",
					StartLine: 1,
					EndLine:   1,
					Language:  "javascript",
				},
			},
			Imports: []ast.Import{
				{
					Path:      "./HeavyComponent",
					IsDynamic: true,
					IsModule:  true,
					Location:  ast.Location{FilePath: "src/app.js", StartLine: 2},
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

	// HeavyComponent should have an incoming REFERENCES edge from app.js
	refs, err := g.FindReferencesByID(context.Background(), heavyClassID)
	if err != nil {
		t.Fatalf("FindReferencesByID failed: %v", err)
	}

	if len(refs) == 0 {
		t.Error("Bug 4: HeavyComponent has 0 incoming references; expected reference from src/app.js via dynamic import")
	}

	found := false
	for _, loc := range refs {
		if loc.FilePath == "src/app.js" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Bug 4: no reference from src/app.js to HeavyComponent")
	}

	if result.Stats.DynamicImportEdgesResolved == 0 {
		t.Error("expected DynamicImportEdgesResolved > 0")
	}
}

// TestResolveDynamicImportEdges_ExternalSkipped verifies that dynamic imports
// of external packages (no leading dot) do not create REFERENCES edges.
func TestResolveDynamicImportEdges_ExternalSkipped(t *testing.T) {
	externalClassID := "node_modules/lodash/lodash.js:1:Lodash"
	appPackageID := "src/app.js:1:app"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/app.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        appPackageID,
					Name:      "app",
					Kind:      ast.SymbolKindPackage,
					FilePath:  "src/app.js",
					StartLine: 1,
					Language:  "javascript",
				},
				// Simulate a Lodash class in the project (unlikely but tests the filter)
				{
					ID:        externalClassID,
					Name:      "Lodash",
					Kind:      ast.SymbolKindClass,
					FilePath:  "node_modules/lodash/lodash.js",
					StartLine: 1,
					Language:  "javascript",
				},
			},
			Imports: []ast.Import{
				{
					Path:      "lodash", // no leading dot — external
					IsDynamic: true,
					IsModule:  true,
					Location:  ast.Location{FilePath: "src/app.js", StartLine: 2},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if result.Stats.DynamicImportEdgesResolved != 0 {
		t.Errorf("external dynamic import should not create edges, got %d", result.Stats.DynamicImportEdgesResolved)
	}
}

// TestResolveDynamicImportEdges_NonJSSkipped verifies that non-JS/TS parse results
// are skipped by the dynamic import resolution pass.
func TestResolveDynamicImportEdges_NonJSSkipped(t *testing.T) {
	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/utils.py",
			Language: "python",
			Symbols: []*ast.Symbol{
				{
					ID:        "src/utils.py:1:utils",
					Name:      "utils",
					Kind:      ast.SymbolKindPackage,
					FilePath:  "src/utils.py",
					StartLine: 1,
					Language:  "python",
				},
			},
			Imports: []ast.Import{
				{
					// Even if somehow a Python result has IsDynamic, it should be skipped
					Path:      "./other",
					IsDynamic: true,
					Location:  ast.Location{FilePath: "src/utils.py", StartLine: 1},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if result.Stats.DynamicImportEdgesResolved != 0 {
		t.Errorf("Python file should be skipped by dynamic import pass, got %d edges", result.Stats.DynamicImportEdgesResolved)
	}
}
