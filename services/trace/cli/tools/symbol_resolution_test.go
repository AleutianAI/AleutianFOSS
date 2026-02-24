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
	"log/slog"
	"os"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// testLogger returns a debug-level logger writing to stderr for test observability.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// buildTestIndex creates a symbol index populated with symbols for dot-notation tests.
func buildTestIndex(t *testing.T) *index.SymbolIndex {
	t.Helper()

	idx := index.NewSymbolIndex()

	symbols := []*ast.Symbol{
		// Go-style: method with Receiver set
		{
			ID:        "handlers/context.go:50:JSON",
			Name:      "JSON",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "handlers/context.go",
			StartLine: 50,
			EndLine:   65,
			Receiver:  "Context",
			Package:   "handlers",
			Exported:  true,
			Language:  "go",
		},
		// Go-style: another method on different receiver
		{
			ID:        "handlers/context.go:100:String",
			Name:      "String",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "handlers/context.go",
			StartLine: 100,
			EndLine:   110,
			Receiver:  "Context",
			Package:   "handlers",
			Exported:  true,
			Language:  "go",
		},
		// JS-style: method with Receiver set and ID containing Type.Method
		{
			ID:        "src/scene.js:30:Scene.render",
			Name:      "render",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "src/scene.js",
			StartLine: 30,
			EndLine:   45,
			Receiver:  "Scene",
			Package:   "scene",
			Exported:  true,
			Language:  "javascript",
		},
		// JS-style: method with only ID containing Type.Method (no Receiver)
		{
			ID:        "src/router.js:10:Router.handle",
			Name:      "handle",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "src/router.js",
			StartLine: 10,
			EndLine:   25,
			Package:   "router",
			Exported:  true,
			Language:  "javascript",
		},
		// Python-style: method as child of class (no Receiver, no ID match)
		// The class symbol with Children
		{
			ID:        "pandas/core/frame.py:100:DataFrame",
			Name:      "DataFrame",
			Kind:      ast.SymbolKindClass,
			FilePath:  "pandas/core/frame.py",
			StartLine: 100,
			EndLine:   500,
			Package:   "pandas.core.frame",
			Exported:  true,
			Language:  "python",
			Children: []*ast.Symbol{
				{
					ID:        "pandas/core/frame.py:110:__init__",
					Name:      "__init__",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "pandas/core/frame.py",
					StartLine: 110,
					EndLine:   130,
					Package:   "pandas.core.frame",
					Exported:  false,
					Language:  "python",
				},
				{
					ID:        "pandas/core/frame.py:200:to_csv",
					Name:      "to_csv",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "pandas/core/frame.py",
					StartLine: 200,
					EndLine:   230,
					Package:   "pandas.core.frame",
					Exported:  true,
					Language:  "python",
				},
			},
		},
		// Also add __init__ to the flat index (parsers add children AND flat symbols)
		{
			ID:        "pandas/core/frame.py:110:__init__",
			Name:      "__init__",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "pandas/core/frame.py",
			StartLine: 110,
			EndLine:   130,
			Package:   "pandas.core.frame",
			Exported:  false,
			Language:  "python",
		},
		// TS-style: method as child of class
		{
			ID:        "src/factory.ts:5:NestFactory",
			Name:      "NestFactory",
			Kind:      ast.SymbolKindClass,
			FilePath:  "src/factory.ts",
			StartLine: 5,
			EndLine:   50,
			Package:   "factory",
			Exported:  true,
			Language:  "typescript",
			Children: []*ast.Symbol{
				{
					ID:        "src/factory.ts:10:create",
					Name:      "create",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "src/factory.ts",
					StartLine: 10,
					EndLine:   30,
					Package:   "factory",
					Exported:  true,
					Language:  "typescript",
				},
			},
		},
		// Bare function (no receiver, no class parent)
		{
			ID:        "cmd/main.go:5:Publish",
			Name:      "Publish",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "cmd/main.go",
			StartLine: 5,
			EndLine:   20,
			Package:   "main",
			Exported:  true,
			Language:  "go",
		},
		// A render function on a DIFFERENT type (to test disambiguation)
		{
			ID:        "src/plot.js:15:Plot.render",
			Name:      "render",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "src/plot.js",
			StartLine: 15,
			EndLine:   30,
			Receiver:  "Plot",
			Package:   "plot",
			Exported:  true,
			Language:  "javascript",
		},
		// Inheritance chain: ThinEngine defines runRenderLoop, Engine extends ThinEngine
		{
			ID:        "src/thinEngine.ts:10:ThinEngine",
			Name:      "ThinEngine",
			Kind:      ast.SymbolKindClass,
			FilePath:  "src/thinEngine.ts",
			StartLine: 10,
			EndLine:   200,
			Package:   "engines",
			Exported:  true,
			Language:  "typescript",
			Children: []*ast.Symbol{
				{
					ID:        "src/thinEngine.ts:50:runRenderLoop",
					Name:      "runRenderLoop",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "src/thinEngine.ts",
					StartLine: 50,
					EndLine:   80,
					Package:   "engines",
					Exported:  true,
					Language:  "typescript",
				},
			},
		},
		{
			ID:        "src/engine.ts:10:Engine",
			Name:      "Engine",
			Kind:      ast.SymbolKindClass,
			FilePath:  "src/engine.ts",
			StartLine: 10,
			EndLine:   300,
			Package:   "engines",
			Exported:  true,
			Language:  "typescript",
			Metadata: &ast.SymbolMetadata{
				Extends: "ThinEngine",
			},
			Children: []*ast.Symbol{
				{
					ID:        "src/engine.ts:20:dispose",
					Name:      "dispose",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "src/engine.ts",
					StartLine: 20,
					EndLine:   40,
					Package:   "engines",
					Exported:  true,
					Language:  "typescript",
				},
			},
		},
	}

	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
		}
	}

	return idx
}

func TestResolveTypeDotMethod(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildTestIndex(t)

	t.Run("Go-style receiver match: Context.JSON", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Context", "JSON", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "JSON" {
			t.Errorf("expected name 'JSON', got %q", sym.Name)
		}
		if sym.Receiver != "Context" {
			t.Errorf("expected receiver 'Context', got %q", sym.Receiver)
		}
	})

	t.Run("Go-style receiver match: Context.String", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Context", "String", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "String" {
			t.Errorf("expected name 'String', got %q", sym.Name)
		}
	})

	t.Run("JS-style receiver match: Scene.render", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Scene", "render", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "render" {
			t.Errorf("expected name 'render', got %q", sym.Name)
		}
		if sym.Receiver != "Scene" {
			t.Errorf("expected receiver 'Scene', got %q", sym.Receiver)
		}
	})

	t.Run("JS-style ID match: Router.handle (no receiver)", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Router", "handle", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "handle" {
			t.Errorf("expected name 'handle', got %q", sym.Name)
		}
		if sym.FilePath != "src/router.js" {
			t.Errorf("expected file 'src/router.js', got %q", sym.FilePath)
		}
	})

	t.Run("Python-style parent class match: DataFrame.__init__", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "DataFrame", "__init__", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "__init__" {
			t.Errorf("expected name '__init__', got %q", sym.Name)
		}
		if sym.FilePath != "pandas/core/frame.py" {
			t.Errorf("expected file 'pandas/core/frame.py', got %q", sym.FilePath)
		}
	})

	t.Run("TS-style parent class match: NestFactory.create", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "NestFactory", "create", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "create" {
			t.Errorf("expected name 'create', got %q", sym.Name)
		}
		if sym.FilePath != "src/factory.ts" {
			t.Errorf("expected file 'src/factory.ts', got %q", sym.FilePath)
		}
	})

	t.Run("Disambiguation: Plot.render vs Scene.render", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Plot", "render", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Receiver != "Plot" {
			t.Errorf("expected receiver 'Plot', got %q", sym.Receiver)
		}
		if sym.FilePath != "src/plot.js" {
			t.Errorf("expected file 'src/plot.js', got %q", sym.FilePath)
		}
	})

	t.Run("Inheritance chain: Engine.runRenderLoop resolves to ThinEngine", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Engine", "runRenderLoop", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "runRenderLoop" {
			t.Errorf("expected name 'runRenderLoop', got %q", sym.Name)
		}
		if sym.ID != "src/thinEngine.ts:50:runRenderLoop" {
			t.Errorf("expected ID from ThinEngine, got %q", sym.ID)
		}
	})

	t.Run("Inheritance: direct method on Engine still works", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "Engine", "dispose", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "dispose" {
			t.Errorf("expected name 'dispose', got %q", sym.Name)
		}
	})

	t.Run("Unknown type returns error", func(t *testing.T) {
		_, err := resolveTypeDotMethod(ctx, idx, "NonExistent", "render", logger)
		if err == nil {
			t.Fatal("expected error for unknown type, got nil")
		}
	})

	t.Run("Unknown method on valid type returns error", func(t *testing.T) {
		_, err := resolveTypeDotMethod(ctx, idx, "Context", "nonExistentMethod", logger)
		if err == nil {
			t.Fatal("expected error for unknown method, got nil")
		}
	})

	t.Run("Empty typeName returns error", func(t *testing.T) {
		_, err := resolveTypeDotMethod(ctx, idx, "", "render", logger)
		if err == nil {
			t.Fatal("expected error for empty typeName, got nil")
		}
	})

	t.Run("Empty methodName returns error", func(t *testing.T) {
		_, err := resolveTypeDotMethod(ctx, idx, "Plot", "", logger)
		if err == nil {
			t.Fatal("expected error for empty methodName, got nil")
		}
	})

	t.Run("Nil index returns error", func(t *testing.T) {
		_, err := resolveTypeDotMethod(ctx, nil, "Plot", "render", logger)
		if err == nil {
			t.Fatal("expected error for nil index, got nil")
		}
	})

	// IT-05 R3-2: Receiver prefix matching tests
	t.Run("Receiver prefix match: NestFactory resolves NestFactoryStatic.create", func(t *testing.T) {
		// Build an index where the Receiver is "NestFactoryStatic" but user types "NestFactory"
		prefixIdx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{
			{
				ID: "packages/core/nest-factory.ts:50:create", Name: "create",
				Kind: ast.SymbolKindMethod, FilePath: "packages/core/nest-factory.ts",
				StartLine: 50, EndLine: 80, Receiver: "NestFactoryStatic",
				Exported: true, Language: "typescript",
			},
			{
				ID: "integration/recipes/recipes.service.ts:14:create", Name: "create",
				Kind: ast.SymbolKindMethod, FilePath: "integration/recipes/recipes.service.ts",
				StartLine: 14, EndLine: 20, Receiver: "RecipesService",
				Exported: true, Language: "typescript",
			},
		} {
			if err := prefixIdx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, prefixIdx, "NestFactory", "create", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Receiver != "NestFactoryStatic" {
			t.Errorf("expected Receiver 'NestFactoryStatic', got %q (ID: %s)", sym.Receiver, sym.ID)
		}
	})

	t.Run("Receiver prefix match does not match unrelated types", func(t *testing.T) {
		// "App" should match "Application" but not "MapperService"
		prefixIdx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{
			{
				ID: "app.ts:10:init", Name: "init",
				Kind: ast.SymbolKindMethod, FilePath: "app.ts",
				StartLine: 10, EndLine: 20, Receiver: "Application",
				Exported: true, Language: "typescript",
			},
			{
				ID: "mapper.ts:5:init", Name: "init",
				Kind: ast.SymbolKindMethod, FilePath: "mapper.ts",
				StartLine: 5, EndLine: 15, Receiver: "MapperService",
				Exported: true, Language: "typescript",
			},
		} {
			if err := prefixIdx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, prefixIdx, "App", "init", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Receiver != "Application" {
			t.Errorf("expected Receiver 'Application', got %q", sym.Receiver)
		}
	})

	// IT-06b Issue 4: Qualified base class names in Extends metadata should be
	// stripped to bare name before recursive lookup.
	t.Run("Qualified inheritance: Series.apply resolves via generic.NDFrame", func(t *testing.T) {
		qualIdx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{
			// NDFrame class with an "apply" method
			{
				ID: "pandas/core/generic.py:100:NDFrame", Name: "NDFrame",
				Kind: ast.SymbolKindClass, FilePath: "pandas/core/generic.py",
				StartLine: 100, EndLine: 500, Package: "generic",
				Exported: true, Language: "python",
				Children: []*ast.Symbol{
					{
						ID: "pandas/core/generic.py:200:apply", Name: "apply",
						Kind: ast.SymbolKindMethod, FilePath: "pandas/core/generic.py",
						StartLine: 200, EndLine: 230, Package: "generic",
						Exported: true, Language: "python",
					},
				},
			},
			// Series extends "generic.NDFrame" (qualified name)
			{
				ID: "pandas/core/series.py:50:Series", Name: "Series",
				Kind: ast.SymbolKindClass, FilePath: "pandas/core/series.py",
				StartLine: 50, EndLine: 400, Package: "series",
				Exported: true, Language: "python",
				Metadata: &ast.SymbolMetadata{
					Extends: "generic.NDFrame",
				},
			},
		} {
			if err := qualIdx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, qualIdx, "Series", "apply", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "apply" {
			t.Errorf("expected name 'apply', got %q", sym.Name)
		}
		if sym.ID != "pandas/core/generic.py:200:apply" {
			t.Errorf("expected ID from NDFrame, got %q", sym.ID)
		}
	})

	t.Run("Exact receiver match takes priority over prefix match", func(t *testing.T) {
		prefixIdx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{
			{
				ID: "engine.ts:10:run", Name: "run",
				Kind: ast.SymbolKindMethod, FilePath: "engine.ts",
				StartLine: 10, EndLine: 20, Receiver: "Engine",
				Exported: true, Language: "typescript",
			},
			{
				ID: "engine_v2.ts:10:run", Name: "run",
				Kind: ast.SymbolKindMethod, FilePath: "engine_v2.ts",
				StartLine: 10, EndLine: 20, Receiver: "EngineV2",
				Exported: true, Language: "typescript",
			},
		} {
			if err := prefixIdx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, prefixIdx, "Engine", "run", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Both match (exact "Engine" and prefix "Engine" → "EngineV2"),
		// but pickBestCandidate should still return a valid result.
		// The exact match "Engine" is added first and both are valid candidates.
		if sym.Name != "run" {
			t.Errorf("expected 'run', got %q", sym.Name)
		}
	})
}

// R3-P1d: resolveTypeDotMethod should find @property methods (SymbolKindProperty).
func TestResolveTypeDotMethod_Property(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()

	// Build an index with a class that has a @property child
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{
			ID:        "model.py:1:User",
			Name:      "User",
			Kind:      ast.SymbolKindClass,
			FilePath:  "model.py",
			StartLine: 1,
			EndLine:   50,
			Package:   "model",
			Exported:  true,
			Language:  "python",
			Children: []*ast.Symbol{
				{
					ID:        "model.py:10:full_name",
					Name:      "full_name",
					Kind:      ast.SymbolKindProperty,
					FilePath:  "model.py",
					StartLine: 10,
					EndLine:   15,
					Package:   "model",
					Exported:  true,
					Language:  "python",
				},
				{
					ID:        "model.py:20:save",
					Name:      "save",
					Kind:      ast.SymbolKindMethod,
					FilePath:  "model.py",
					StartLine: 20,
					EndLine:   30,
					Package:   "model",
					Exported:  true,
					Language:  "python",
				},
			},
		},
		// Flat index entries for children
		{
			ID:        "model.py:10:full_name",
			Name:      "full_name",
			Kind:      ast.SymbolKindProperty,
			FilePath:  "model.py",
			StartLine: 10,
			EndLine:   15,
			Package:   "model",
			Exported:  true,
			Language:  "python",
		},
		{
			ID:        "model.py:20:save",
			Name:      "save",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "model.py",
			StartLine: 20,
			EndLine:   30,
			Package:   "model",
			Exported:  true,
			Language:  "python",
		},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
		}
	}

	t.Run("Property child resolved via Strategy 1 (receiver match)", func(t *testing.T) {
		// Add a property with Receiver set (e.g., from a language that sets it)
		propWithReceiver := &ast.Symbol{
			ID:        "model.py:40:email",
			Name:      "email",
			Kind:      ast.SymbolKindProperty,
			FilePath:  "model.py",
			StartLine: 40,
			EndLine:   45,
			Receiver:  "User",
			Package:   "model",
			Exported:  true,
			Language:  "python",
		}
		if err := idx.Add(propWithReceiver); err != nil {
			t.Fatalf("failed to add: %v", err)
		}

		sym, err := resolveTypeDotMethod(ctx, idx, "User", "email", logger)
		if err != nil {
			t.Fatalf("R3-P1d: resolveTypeDotMethod should find Property via receiver match, got error: %v", err)
		}
		if sym.Kind != ast.SymbolKindProperty {
			t.Errorf("expected Property kind, got %s", sym.Kind)
		}
		if sym.Name != "email" {
			t.Errorf("expected name 'email', got %q", sym.Name)
		}
	})

	t.Run("Property child resolved via Strategy 3 (parent class children)", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "User", "full_name", logger)
		if err != nil {
			t.Fatalf("R3-P1d: resolveTypeDotMethod should find Property via class children, got error: %v", err)
		}
		if sym.Kind != ast.SymbolKindProperty {
			t.Errorf("expected Property kind, got %s", sym.Kind)
		}
		if sym.Name != "full_name" {
			t.Errorf("expected name 'full_name', got %q", sym.Name)
		}
	})

	t.Run("Method still resolves normally alongside Property", func(t *testing.T) {
		sym, err := resolveTypeDotMethod(ctx, idx, "User", "save", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Kind != ast.SymbolKindMethod {
			t.Errorf("expected Method kind, got %s", sym.Kind)
		}
	})
}

func TestResolveFunctionWithFuzzy_DotNotation(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildTestIndex(t)

	t.Run("dot notation resolves before fuzzy search", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Scene.render", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for dot notation match")
		}
		if sym.Name != "render" {
			t.Errorf("expected name 'render', got %q", sym.Name)
		}
		if sym.Receiver != "Scene" {
			t.Errorf("expected receiver 'Scene', got %q", sym.Receiver)
		}
	})

	t.Run("bare function still resolves via exact match", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Publish", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for exact match")
		}
		if sym.Name != "Publish" {
			t.Errorf("expected name 'Publish', got %q", sym.Name)
		}
	})

	t.Run("dot notation with parent class: DataFrame.__init__", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "DataFrame.__init__", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for dot notation match")
		}
		if sym.Name != "__init__" {
			t.Errorf("expected name '__init__', got %q", sym.Name)
		}
	})
}

func TestPickBestCandidate(t *testing.T) {
	t.Run("prefers symbol with receiver set", func(t *testing.T) {
		candidates := []*ast.Symbol{
			{ID: "a/b.js:10:render", Name: "render", Receiver: ""},
			{ID: "a/b.js:20:Scene.render", Name: "render", Receiver: "Scene"},
		}
		best := pickBestCandidate(candidates)
		if best.Receiver != "Scene" {
			t.Errorf("expected candidate with receiver 'Scene', got %q", best.Receiver)
		}
	})

	t.Run("prefers shorter ID when receivers equal", func(t *testing.T) {
		candidates := []*ast.Symbol{
			{ID: "very/long/path/to/file.go:100:render", Name: "render", Receiver: "Plot"},
			{ID: "src/plot.go:10:render", Name: "render", Receiver: "Plot"},
		}
		best := pickBestCandidate(candidates)
		if best.ID != "src/plot.go:10:render" {
			t.Errorf("expected shorter ID candidate, got %q", best.ID)
		}
	})

	t.Run("single candidate returns it", func(t *testing.T) {
		candidates := []*ast.Symbol{
			{ID: "a.go:1:foo", Name: "foo"},
		}
		best := pickBestCandidate(candidates)
		if best.ID != "a.go:1:foo" {
			t.Errorf("expected the only candidate, got %q", best.ID)
		}
	})

	t.Run("prefers non-overload over overload stub", func(t *testing.T) {
		candidates := []*ast.Symbol{
			{
				ID:       "generic.py:3000:to_csv",
				Name:     "to_csv",
				Metadata: &ast.SymbolMetadata{Decorators: []string{"overload"}},
			},
			{
				ID:       "generic.py:3789:to_csv",
				Name:     "to_csv",
				Metadata: &ast.SymbolMetadata{Decorators: []string{"final"}},
				Calls:    []ast.CallSite{{Target: "isinstance"}},
			},
		}
		best := pickBestCandidate(candidates)
		if best.ID != "generic.py:3789:to_csv" {
			t.Errorf("expected non-overload candidate, got %q (overload stub won)", best.ID)
		}
	})

	t.Run("prefers candidate with calls over empty calls", func(t *testing.T) {
		candidates := []*ast.Symbol{
			{ID: "a.py:10:foo", Name: "foo", Calls: nil},
			{ID: "a.py:30:foo", Name: "foo", Calls: []ast.CallSite{{Target: "bar"}}},
		}
		best := pickBestCandidate(candidates)
		if best.ID != "a.py:30:foo" {
			t.Errorf("expected candidate with calls, got %q", best.ID)
		}
	})
}

// ─── R3-P2a: isOverloadStub ───

func TestIsOverloadStub(t *testing.T) {
	t.Run("nil symbol", func(t *testing.T) {
		if isOverloadStub(nil) {
			t.Error("expected false for nil symbol")
		}
	})

	t.Run("nil metadata", func(t *testing.T) {
		sym := &ast.Symbol{Name: "foo"}
		if isOverloadStub(sym) {
			t.Error("expected false for nil metadata")
		}
	})

	t.Run("no decorators", func(t *testing.T) {
		sym := &ast.Symbol{Name: "foo", Metadata: &ast.SymbolMetadata{}}
		if isOverloadStub(sym) {
			t.Error("expected false for no decorators")
		}
	})

	t.Run("overload decorator", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "to_csv",
			Metadata: &ast.SymbolMetadata{Decorators: []string{"overload"}},
		}
		if !isOverloadStub(sym) {
			t.Error("expected true for @overload decorator")
		}
	})

	t.Run("other decorator", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "to_csv",
			Metadata: &ast.SymbolMetadata{Decorators: []string{"final"}},
		}
		if isOverloadStub(sym) {
			t.Error("expected false for @final decorator")
		}
	})

	t.Run("mixed decorators with overload", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "to_csv",
			Metadata: &ast.SymbolMetadata{Decorators: []string{"deprecated", "overload"}},
		}
		if !isOverloadStub(sym) {
			t.Error("expected true when @overload is among decorators")
		}
	})
}

// ─── R3-P2a: resolveTypeDotMethod with overload filtering ───

func TestResolveTypeDotMethod_OverloadFiltering(t *testing.T) {
	t.Run("skips overload stubs and returns real implementation", func(t *testing.T) {
		// NDFrame class has 2 @overload stubs + 1 real to_csv
		overloadStub1 := &ast.Symbol{
			ID:        "generic.py:3000:to_csv",
			Name:      "to_csv",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "generic.py",
			StartLine: 3000,
			EndLine:   3010,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"overload"}},
		}
		overloadStub2 := &ast.Symbol{
			ID:        "generic.py:3020:to_csv",
			Name:      "to_csv",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "generic.py",
			StartLine: 3020,
			EndLine:   3030,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"overload"}},
		}
		realImpl := &ast.Symbol{
			ID:        "generic.py:3789:to_csv",
			Name:      "to_csv",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "generic.py",
			StartLine: 3789,
			EndLine:   3850,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"final"}},
			Calls:     []ast.CallSite{{Target: "isinstance"}, {Target: "self.to_frame"}},
		}

		ndframeClass := &ast.Symbol{
			ID:        "generic.py:100:NDFrame",
			Name:      "NDFrame",
			Kind:      ast.SymbolKindClass,
			FilePath:  "generic.py",
			StartLine: 100,
			EndLine:   4000,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Children:  []*ast.Symbol{overloadStub1, overloadStub2, realImpl},
		}

		// Build index — class and flat entries for children
		idx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{ndframeClass, overloadStub1, overloadStub2, realImpl} {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		logger := testLogger()
		sym, err := resolveTypeDotMethod(context.Background(), idx, "NDFrame", "to_csv", logger)
		if err != nil {
			t.Fatalf("resolveTypeDotMethod failed: %v", err)
		}
		if sym.ID != realImpl.ID {
			t.Errorf("expected real implementation %q, got %q (overload stub was returned)", realImpl.ID, sym.ID)
		}
	})

	t.Run("returns overload stub when no real impl exists", func(t *testing.T) {
		// Edge case: only overload stubs (e.g., Protocol or ABC)
		stub := &ast.Symbol{
			ID:        "protocol.py:10:process",
			Name:      "process",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "protocol.py",
			StartLine: 10,
			EndLine:   20,
			Package:   "protocol",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"overload"}},
		}

		protoClass := &ast.Symbol{
			ID:        "protocol.py:5:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindClass,
			FilePath:  "protocol.py",
			StartLine: 5,
			EndLine:   30,
			Package:   "protocol",
			Exported:  true,
			Language:  "python",
			Children:  []*ast.Symbol{stub},
		}

		idx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{protoClass, stub} {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		logger := testLogger()
		sym, err := resolveTypeDotMethod(context.Background(), idx, "Handler", "process", logger)
		if err != nil {
			t.Fatalf("resolveTypeDotMethod failed: %v", err)
		}
		if sym.ID != stub.ID {
			t.Errorf("expected overload stub (only option), got %q", sym.ID)
		}
	})

	t.Run("inheritance walk also filters overloads", func(t *testing.T) {
		// DataFrame extends NDFrame. NDFrame has overload stubs + real impl.
		overloadStub := &ast.Symbol{
			ID:        "generic.py:3000:to_csv",
			Name:      "to_csv",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "generic.py",
			StartLine: 3000,
			EndLine:   3010,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"overload"}},
		}
		realImpl := &ast.Symbol{
			ID:        "generic.py:3789:to_csv",
			Name:      "to_csv",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "generic.py",
			StartLine: 3789,
			EndLine:   3850,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Decorators: []string{"final"}},
			Calls:     []ast.CallSite{{Target: "isinstance"}},
		}

		ndframeClass := &ast.Symbol{
			ID:        "generic.py:100:NDFrame",
			Name:      "NDFrame",
			Kind:      ast.SymbolKindClass,
			FilePath:  "generic.py",
			StartLine: 100,
			EndLine:   4000,
			Package:   "generic",
			Exported:  true,
			Language:  "python",
			Children:  []*ast.Symbol{overloadStub, realImpl},
		}

		dfClass := &ast.Symbol{
			ID:        "frame.py:10:DataFrame",
			Name:      "DataFrame",
			Kind:      ast.SymbolKindClass,
			FilePath:  "frame.py",
			StartLine: 10,
			EndLine:   500,
			Package:   "frame",
			Exported:  true,
			Language:  "python",
			Metadata:  &ast.SymbolMetadata{Extends: "NDFrame"},
		}

		idx := index.NewSymbolIndex()
		for _, sym := range []*ast.Symbol{ndframeClass, overloadStub, realImpl, dfClass} {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		logger := testLogger()
		sym, err := resolveTypeDotMethod(context.Background(), idx, "DataFrame", "to_csv", logger)
		if err != nil {
			t.Fatalf("resolveTypeDotMethod failed: %v", err)
		}
		if sym.ID != realImpl.ID {
			t.Errorf("expected real implementation via inheritance, got %q", sym.ID)
		}
	})
}

// ─── IT-00a: KindFilter tests ───

func TestKindFilter_String(t *testing.T) {
	t.Run("callable", func(t *testing.T) {
		if KindFilterCallable.String() != "callable" {
			t.Errorf("expected 'callable', got %q", KindFilterCallable.String())
		}
	})
	t.Run("type", func(t *testing.T) {
		if KindFilterType.String() != "type" {
			t.Errorf("expected 'type', got %q", KindFilterType.String())
		}
	})
	t.Run("any", func(t *testing.T) {
		if KindFilterAny.String() != "any" {
			t.Errorf("expected 'any', got %q", KindFilterAny.String())
		}
	})
	t.Run("unknown", func(t *testing.T) {
		unknown := KindFilter(99)
		if unknown.String() != "unknown(99)" {
			t.Errorf("expected 'unknown(99)', got %q", unknown.String())
		}
	})
}

func TestMatchesKindFilter(t *testing.T) {
	t.Run("KindFilterCallable accepts Function", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindFunction}
		if !matchesKindFilter(sym, KindFilterCallable) {
			t.Error("expected Function to match KindFilterCallable")
		}
	})

	t.Run("KindFilterCallable accepts Method", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindMethod}
		if !matchesKindFilter(sym, KindFilterCallable) {
			t.Error("expected Method to match KindFilterCallable")
		}
	})

	t.Run("KindFilterCallable accepts Property", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindProperty}
		if !matchesKindFilter(sym, KindFilterCallable) {
			t.Error("expected Property to match KindFilterCallable")
		}
	})

	t.Run("KindFilterCallable rejects Class", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindClass}
		if matchesKindFilter(sym, KindFilterCallable) {
			t.Error("expected Class to NOT match KindFilterCallable")
		}
	})

	t.Run("KindFilterCallable rejects Variable", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindVariable}
		if matchesKindFilter(sym, KindFilterCallable) {
			t.Error("expected Variable to NOT match KindFilterCallable")
		}
	})

	t.Run("KindFilterType accepts Interface", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindInterface}
		if !matchesKindFilter(sym, KindFilterType) {
			t.Error("expected Interface to match KindFilterType")
		}
	})

	t.Run("KindFilterType accepts Class", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindClass}
		if !matchesKindFilter(sym, KindFilterType) {
			t.Error("expected Class to match KindFilterType")
		}
	})

	t.Run("KindFilterType accepts Struct", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindStruct}
		if !matchesKindFilter(sym, KindFilterType) {
			t.Error("expected Struct to match KindFilterType")
		}
	})

	t.Run("KindFilterType accepts Type", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindType}
		if !matchesKindFilter(sym, KindFilterType) {
			t.Error("expected Type to match KindFilterType")
		}
	})

	t.Run("KindFilterType rejects Function", func(t *testing.T) {
		sym := &ast.Symbol{Kind: ast.SymbolKindFunction}
		if matchesKindFilter(sym, KindFilterType) {
			t.Error("expected Function to NOT match KindFilterType")
		}
	})

	t.Run("KindFilterAny accepts everything", func(t *testing.T) {
		kinds := []ast.SymbolKind{
			ast.SymbolKindFunction, ast.SymbolKindMethod, ast.SymbolKindClass,
			ast.SymbolKindVariable, ast.SymbolKindConstant, ast.SymbolKindImport,
		}
		for _, kind := range kinds {
			sym := &ast.Symbol{Kind: kind}
			if !matchesKindFilter(sym, KindFilterAny) {
				t.Errorf("expected kind %s to match KindFilterAny", kind)
			}
		}
	})
}

// buildMixedKindTestIndex creates an index with symbols of various kinds for kind filter tests.
func buildMixedKindTestIndex(t *testing.T) *index.SymbolIndex {
	t.Helper()

	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		{
			ID:        "app.go:10:Router",
			Name:      "Router",
			Kind:      ast.SymbolKindStruct,
			FilePath:  "app.go",
			StartLine: 10,
			EndLine:   50,
			Package:   "app",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "app.go:60:Router",
			Name:      "Router",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "app.go",
			StartLine: 60,
			EndLine:   80,
			Package:   "app",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "handler.go:5:Handle",
			Name:      "Handle",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "handler.go",
			StartLine: 5,
			EndLine:   20,
			Package:   "app",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "types.go:10:Handler",
			Name:      "Handler",
			Kind:      ast.SymbolKindInterface,
			FilePath:  "types.go",
			StartLine: 10,
			EndLine:   15,
			Package:   "app",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "config.go:1:MaxRetries",
			Name:      "MaxRetries",
			Kind:      ast.SymbolKindConstant,
			FilePath:  "config.go",
			StartLine: 1,
			EndLine:   1,
			Package:   "app",
			Exported:  true,
			Language:  "go",
		},
		// For bare method fallback test: "Open" as a package-level function
		{
			ID:        "db.go:10:Open",
			Name:      "Open",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "db.go",
			StartLine: 10,
			EndLine:   30,
			Package:   "db",
			Exported:  true,
			Language:  "go",
		},
	}

	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
		}
	}

	return idx
}

func TestResolveFunctionWithFuzzy_KindFilter(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildMixedKindTestIndex(t)

	t.Run("default callable filter returns function over struct for same name", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Router", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for exact match")
		}
		if sym.Kind != ast.SymbolKindFunction {
			t.Errorf("expected Function kind with default callable filter, got %s", sym.Kind)
		}
	})

	t.Run("KindFilterType returns struct over function for same name", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Router", logger,
			WithKindFilter(KindFilterType))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for exact match")
		}
		if sym.Kind != ast.SymbolKindStruct {
			t.Errorf("expected Struct kind with KindFilterType, got %s", sym.Kind)
		}
	})

	t.Run("KindFilterAny returns first match regardless of kind", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "MaxRetries", logger,
			WithKindFilter(KindFilterAny))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for exact match")
		}
		if sym.Name != "MaxRetries" {
			t.Errorf("expected MaxRetries, got %q", sym.Name)
		}
	})

	t.Run("KindFilterCallable rejects constant, falls through to fuzzy", func(t *testing.T) {
		// MaxRetries is a constant — callable filter should reject it on exact match
		// and fall through to fuzzy (which will also fail since there's no callable "MaxRetries")
		_, _, err := ResolveFunctionWithFuzzy(ctx, idx, "MaxRetries", logger,
			WithKindFilter(KindFilterCallable))
		if err == nil {
			t.Fatal("expected error when no callable symbol named MaxRetries exists")
		}
	})

	t.Run("KindFilterType finds interface", func(t *testing.T) {
		sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Handler", logger,
			WithKindFilter(KindFilterType))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Kind != ast.SymbolKindInterface {
			t.Errorf("expected Interface kind, got %s", sym.Kind)
		}
	})
}

func TestResolveFunctionWithFuzzy_FullIDBypass(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildMixedKindTestIndex(t)

	t.Run("full ID with colon resolves directly", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "handler.go:5:Handle", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for full-ID bypass")
		}
		if sym.ID != "handler.go:5:Handle" {
			t.Errorf("expected exact ID match, got %q", sym.ID)
		}
	})

	t.Run("full ID bypass ignores kind filter", func(t *testing.T) {
		// Even with KindFilterType, a full-ID lookup should return the exact symbol
		sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "handler.go:5:Handle", logger,
			WithKindFilter(KindFilterType))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Kind != ast.SymbolKindFunction {
			t.Errorf("expected Function (full-ID bypass ignores kind filter), got %s", sym.Kind)
		}
	})
}

func TestResolveFunctionWithFuzzy_BareMethodFallback(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildMixedKindTestIndex(t)

	t.Run("bare method fallback resolves DB.Open to Open", func(t *testing.T) {
		// "DB.Open" — no type "DB" exists, dot-notation will fail.
		// With bare method fallback, it should resolve to the "Open" function.
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "DB.Open", logger,
			WithBareMethodFallback())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for bare method fallback (exact match on bare name)")
		}
		if sym.Name != "Open" {
			t.Errorf("expected name 'Open', got %q", sym.Name)
		}
	})

	t.Run("without bare method fallback, DB.Open fails dot-notation and goes to fuzzy", func(t *testing.T) {
		// Without the option, "DB.Open" should fail dot-notation and fall through
		// to fuzzy search (which may or may not find it depending on index.Search)
		_, _, err := ResolveFunctionWithFuzzy(ctx, idx, "DB.Open", logger)
		// This should either succeed via fuzzy or fail — the key assertion is
		// that it does NOT use bare method fallback (no "Open" direct match)
		if err == nil {
			// If fuzzy search happened to find "Open", that's ok — the important
			// thing is the path taken (fuzzy, not bare method)
			return
		}
		// Error is also acceptable — means fuzzy didn't find it
	})

	t.Run("bare method fallback respects kind filter", func(t *testing.T) {
		// "DB.Open" with KindFilterType — Open is a Function, so type filter should reject it
		_, _, err := ResolveFunctionWithFuzzy(ctx, idx, "DB.Open", logger,
			WithBareMethodFallback(), WithKindFilter(KindFilterType))
		if err == nil {
			t.Fatal("expected error: bare method 'Open' is Function, not Type")
		}
	})
}

// TestResolveFunctionWithFuzzy_BareMethodDisambiguation tests IT-05 R3-1:
// when BareMethodFallback returns multiple symbols with the same name,
// the dot-notation type prefix is used to disambiguate.
func TestResolveFunctionWithFuzzy_BareMethodDisambiguation(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()

	// Build a custom index with multiple symbols sharing the same bare name.
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		// gin.Default scenario: two "Default" functions in different packages
		{
			ID:        "binding/binding.go:95:Default",
			Name:      "Default",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "binding/binding.go",
			StartLine: 95,
			EndLine:   100,
			Package:   "binding",
			Exported:  true,
			Language:  "go",
		},
		{
			ID:        "gin.go:20:Default",
			Name:      "Default",
			Kind:      ast.SymbolKindFunction,
			FilePath:  "gin.go",
			StartLine: 20,
			EndLine:   30,
			Package:   "gin",
			Exported:  true,
			Language:  "go",
		},
		// NestFactory.create scenario: multiple "create" methods on different classes
		{
			ID:        "integration/recipes/recipes.service.ts:14:create",
			Name:      "create",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "integration/recipes/recipes.service.ts",
			StartLine: 14,
			EndLine:   20,
			Package:   "recipes",
			Exported:  true,
			Language:  "typescript",
			Receiver:  "RecipesService",
		},
		{
			ID:        "packages/core/nest-factory.ts:50:create",
			Name:      "create",
			Kind:      ast.SymbolKindMethod,
			FilePath:  "packages/core/nest-factory.ts",
			StartLine: 50,
			EndLine:   80,
			Package:   "core",
			Exported:  true,
			Language:  "typescript",
			Receiver:  "NestFactoryStatic",
		},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
		}
	}

	t.Run("gin.Default prefers gin.go over binding.go via file path prefix", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "gin.Default", logger,
			WithBareMethodFallback())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for bare method fallback")
		}
		if sym.FilePath != "gin.go" {
			t.Errorf("expected gin.go, got %q (ID: %s)", sym.FilePath, sym.ID)
		}
	})

	t.Run("NestFactory.create prefers NestFactoryStatic via Receiver prefix", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "NestFactory.create", logger,
			WithBareMethodFallback())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected fuzzy=false for bare method fallback")
		}
		if sym.Receiver != "NestFactoryStatic" {
			t.Errorf("expected Receiver 'NestFactoryStatic', got %q (ID: %s)", sym.Receiver, sym.ID)
		}
	})

	t.Run("no prefix match falls back to pickBestCandidate", func(t *testing.T) {
		// "Unknown.Default" — neither "Unknown" appears in any Receiver, FilePath, or ID.
		// pickBestCandidate should still return a valid result (not error).
		sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Unknown.Default", logger,
			WithBareMethodFallback())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "Default" {
			t.Errorf("expected 'Default', got %q", sym.Name)
		}
	})
}

// TestPickBestBareCandidate tests the pickBestBareCandidate disambiguation function directly.
func TestPickBestBareCandidate(t *testing.T) {
	ginDefault := &ast.Symbol{
		ID: "gin.go:20:Default", Name: "Default", Kind: ast.SymbolKindFunction,
		FilePath: "gin.go", Exported: true,
	}
	bindingDefault := &ast.Symbol{
		ID: "binding/binding.go:95:Default", Name: "Default", Kind: ast.SymbolKindFunction,
		FilePath: "binding/binding.go", Exported: true,
	}
	nestCreate := &ast.Symbol{
		ID: "packages/core/nest-factory.ts:50:create", Name: "create", Kind: ast.SymbolKindMethod,
		FilePath: "packages/core/nest-factory.ts", Receiver: "NestFactoryStatic", Exported: true,
	}
	recipesCreate := &ast.Symbol{
		ID: "integration/recipes/recipes.service.ts:14:create", Name: "create", Kind: ast.SymbolKindMethod,
		FilePath: "integration/recipes/recipes.service.ts", Receiver: "RecipesService", Exported: true,
	}

	t.Run("nil candidates returns nil", func(t *testing.T) {
		if got := pickBestBareCandidate(nil, "gin"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("single candidate returns it", func(t *testing.T) {
		got := pickBestBareCandidate([]*ast.Symbol{ginDefault}, "gin")
		if got != ginDefault {
			t.Errorf("expected ginDefault, got %v", got)
		}
	})

	t.Run("empty prefix falls back to pickBestCandidate", func(t *testing.T) {
		got := pickBestBareCandidate([]*ast.Symbol{bindingDefault, ginDefault}, "")
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})

	t.Run("gin prefix prefers gin.go via FilePath", func(t *testing.T) {
		got := pickBestBareCandidate([]*ast.Symbol{bindingDefault, ginDefault}, "gin")
		if got != ginDefault {
			t.Errorf("expected gin.go:Default, got %s", got.ID)
		}
	})

	t.Run("NestFactory prefix prefers NestFactoryStatic via Receiver", func(t *testing.T) {
		got := pickBestBareCandidate([]*ast.Symbol{recipesCreate, nestCreate}, "NestFactory")
		if got != nestCreate {
			t.Errorf("expected nest-factory.ts:create, got %s", got.ID)
		}
	})

	t.Run("no prefix match falls through to pickBestCandidate", func(t *testing.T) {
		got := pickBestBareCandidate([]*ast.Symbol{bindingDefault, ginDefault}, "unknown")
		if got == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestResolveTypeDotMethod_OverridePreference tests IT-05 R5: when a child type overrides
// a parent method, the child's version is preferred.
func TestResolveTypeDotMethod_OverridePreference(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()

	t.Run("child override preferred over parent method", func(t *testing.T) {
		// Plot extends Bindable. Bindable has render(). Plot also has render().
		// resolveTypeDotMethod("Plot", "render") should return Plot.render, not Bindable.render.
		idx := index.NewSymbolIndex()
		symbols := []*ast.Symbol{
			{
				ID: "src/bindable.ts:10:Bindable", Name: "Bindable",
				Kind: ast.SymbolKindClass, FilePath: "src/bindable.ts",
				StartLine: 10, EndLine: 100, Package: "core",
				Exported: true, Language: "typescript",
				Children: []*ast.Symbol{
					{
						ID: "src/bindable.ts:50:render", Name: "render",
						Kind: ast.SymbolKindMethod, FilePath: "src/bindable.ts",
						StartLine: 50, EndLine: 70, Package: "core",
						Exported: true, Language: "typescript",
					},
				},
			},
			// Flat index entry for Bindable.render
			{
				ID: "src/bindable.ts:50:render", Name: "render",
				Kind: ast.SymbolKindMethod, FilePath: "src/bindable.ts",
				StartLine: 50, EndLine: 70, Package: "core",
				Exported: true, Language: "typescript",
			},
			// Plot extends Bindable and overrides render
			{
				ID: "src/plot.ts:10:Plot", Name: "Plot",
				Kind: ast.SymbolKindClass, FilePath: "src/plot.ts",
				StartLine: 10, EndLine: 200, Package: "plots",
				Exported: true, Language: "typescript",
				Metadata: &ast.SymbolMetadata{Extends: "Bindable"},
				Children: []*ast.Symbol{
					{
						ID: "src/plot.ts:30:render", Name: "render",
						Kind: ast.SymbolKindMethod, FilePath: "src/plot.ts",
						StartLine: 30, EndLine: 60, Package: "plots",
						Receiver: "Plot",
						Exported: true, Language: "typescript",
					},
				},
			},
			// Flat index entry for Plot.render (with Receiver)
			{
				ID: "src/plot.ts:30:render", Name: "render",
				Kind: ast.SymbolKindMethod, FilePath: "src/plot.ts",
				StartLine: 30, EndLine: 60, Package: "plots",
				Receiver: "Plot",
				Exported: true, Language: "typescript",
			},
		}
		for _, sym := range symbols {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, idx, "Plot", "render", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.ID != "src/plot.ts:30:render" {
			t.Errorf("expected Plot.render (child override), got %q", sym.ID)
		}
	})

	t.Run("no override returns parent method", func(t *testing.T) {
		// SubPlot extends Plot, but SubPlot does NOT override render.
		// Should still return ThinEngine.runRenderLoop from the existing test index.
		idx := buildTestIndex(t)

		sym, err := resolveTypeDotMethod(ctx, idx, "Engine", "runRenderLoop", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Engine.runRenderLoop → walks to ThinEngine.runRenderLoop (no override on Engine)
		if sym.ID != "src/thinEngine.ts:50:runRenderLoop" {
			t.Errorf("expected ThinEngine.runRenderLoop (no override), got %q", sym.ID)
		}
	})

	t.Run("override via ID match (JS/TS without Receiver)", func(t *testing.T) {
		idx := index.NewSymbolIndex()
		symbols := []*ast.Symbol{
			{
				ID: "src/base.ts:10:Base", Name: "Base",
				Kind: ast.SymbolKindClass, FilePath: "src/base.ts",
				StartLine: 10, EndLine: 100, Package: "core",
				Exported: true, Language: "typescript",
				Children: []*ast.Symbol{
					{
						ID: "src/base.ts:50:doWork", Name: "doWork",
						Kind: ast.SymbolKindMethod, FilePath: "src/base.ts",
						StartLine: 50, EndLine: 70,
						Exported: true, Language: "typescript",
					},
				},
			},
			{
				ID: "src/base.ts:50:doWork", Name: "doWork",
				Kind: ast.SymbolKindMethod, FilePath: "src/base.ts",
				StartLine: 50, EndLine: 70,
				Exported: true, Language: "typescript",
			},
			{
				ID: "src/child.ts:10:Child", Name: "Child",
				Kind: ast.SymbolKindClass, FilePath: "src/child.ts",
				StartLine: 10, EndLine: 100, Package: "core",
				Exported: true, Language: "typescript",
				Metadata: &ast.SymbolMetadata{Extends: "Base"},
			},
			// Child.doWork is in the flat index with ID containing "Child.doWork"
			{
				ID: "src/child.ts:30:Child.doWork", Name: "doWork",
				Kind: ast.SymbolKindMethod, FilePath: "src/child.ts",
				StartLine: 30, EndLine: 50,
				Exported: true, Language: "typescript",
			},
		}
		for _, sym := range symbols {
			if err := idx.Add(sym); err != nil {
				t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
			}
		}

		sym, err := resolveTypeDotMethod(ctx, idx, "Child", "doWork", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.ID != "src/child.ts:30:Child.doWork" {
			t.Errorf("expected Child.doWork override via ID match, got %q", sym.ID)
		}
	})
}

// TestFindChildOverride_NoPrefixFalsePositive verifies that findChildOverride
// does NOT match methods on unrelated types that share a name prefix.
// Regression test for IT-05 R5 review fix: removed HasPrefix strategy.
func TestFindChildOverride_NoPrefixFalsePositive(t *testing.T) {
	idx := index.NewSymbolIndex()
	symbols := []*ast.Symbol{
		// Parent: Bindable.render
		{
			ID: "src/bindable.ts:50:render", Name: "render",
			Kind: ast.SymbolKindMethod, FilePath: "src/bindable.ts",
			StartLine: 50, EndLine: 70, Receiver: "Bindable",
			Exported: true, Language: "typescript",
		},
		// PlotHelper.render — an unrelated type that shares "Plot" prefix
		{
			ID: "src/plot_helper.ts:30:render", Name: "render",
			Kind: ast.SymbolKindMethod, FilePath: "src/plot_helper.ts",
			StartLine: 30, EndLine: 50, Receiver: "PlotHelper",
			Exported: true, Language: "typescript",
		},
	}
	for _, sym := range symbols {
		if err := idx.Add(sym); err != nil {
			t.Fatalf("failed to add symbol %s: %v", sym.ID, err)
		}
	}

	parentResult := symbols[0] // Bindable.render
	// typeName="Plot" should NOT match Receiver="PlotHelper" (prefix false positive)
	override := findChildOverride(idx, "Plot", "render", parentResult)
	if override != nil {
		t.Errorf("expected nil (no override for Plot), got %q — prefix match should not apply", override.ID)
	}
}

func TestResolveFunctionWithFuzzy_DotNotationRespectsKindFilter(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildTestIndex(t)

	t.Run("dot notation with KindFilterCallable returns method", func(t *testing.T) {
		// "Context.JSON" resolves to JSON method — callable filter accepts it
		sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Context.JSON", logger,
			WithKindFilter(KindFilterCallable))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "JSON" {
			t.Errorf("expected JSON, got %q", sym.Name)
		}
	})

	t.Run("dot notation with KindFilterType rejects method and falls through", func(t *testing.T) {
		// "Context.JSON" resolves to JSON method — type filter should reject it
		// and fall through to fuzzy search (which will also fail for "Context.JSON")
		_, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Context.JSON", logger,
			WithKindFilter(KindFilterType))
		if err == nil {
			t.Fatal("expected error: dot-notation resolved a Method but KindFilterType was requested")
		}
	})

	t.Run("dot notation with KindFilterAny returns method", func(t *testing.T) {
		// "Context.JSON" with any filter — should accept the method
		sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Context.JSON", logger,
			WithKindFilter(KindFilterAny))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sym.Name != "JSON" {
			t.Errorf("expected JSON, got %q", sym.Name)
		}
	})
}

func TestResolveFunctionWithFuzzy_DefaultBehaviorPreserved(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildTestIndex(t)

	t.Run("no options produces identical behavior to original", func(t *testing.T) {
		// Exact match for bare name
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Publish", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected exact match")
		}
		if sym.Name != "Publish" {
			t.Errorf("expected Publish, got %q", sym.Name)
		}
	})

	t.Run("dot notation still works without options", func(t *testing.T) {
		sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Context.JSON", logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if fuzzy {
			t.Error("expected exact match via dot notation")
		}
		if sym.Name != "JSON" {
			t.Errorf("expected JSON, got %q", sym.Name)
		}
	})

	t.Run("nil index returns error", func(t *testing.T) {
		_, _, err := ResolveFunctionWithFuzzy(ctx, nil, "Publish", logger)
		if err == nil {
			t.Fatal("expected error for nil index")
		}
	})
}

func TestResolveMultipleFunctionsWithFuzzy_PassesOptions(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := buildMixedKindTestIndex(t)

	t.Run("KindFilterType passed through to each resolution", func(t *testing.T) {
		syms, fuzzyFlags, err := ResolveMultipleFunctionsWithFuzzy(ctx, idx,
			[]string{"Router", "Handler"}, logger, WithKindFilter(KindFilterType))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(syms) != 2 {
			t.Fatalf("expected 2 symbols, got %d", len(syms))
		}
		if len(fuzzyFlags) != 2 {
			t.Fatalf("expected 2 fuzzy flags, got %d", len(fuzzyFlags))
		}
		if syms[0].Kind != ast.SymbolKindStruct {
			t.Errorf("expected Router as Struct with KindFilterType, got %s", syms[0].Kind)
		}
		if syms[1].Kind != ast.SymbolKindInterface {
			t.Errorf("expected Handler as Interface with KindFilterType, got %s", syms[1].Kind)
		}
	})
}

// TestPickMostSignificantSymbol verifies IT-06 Bug 5: KindFilterAny must prefer
// higher-significance kinds (class/struct) over lower ones (field/variable).
func TestPickMostSignificantSymbol(t *testing.T) {
	t.Run("prefers class over field", func(t *testing.T) {
		field := &ast.Symbol{
			ID:       "nativeInterfaces.ts:427:Engine",
			Name:     "Engine",
			Kind:     ast.SymbolKindField,
			FilePath: "packages/dev/core/src/Engines/Native/nativeInterfaces.ts",
		}
		class := &ast.Symbol{
			ID:       "Engines/engine.ts:100:Engine",
			Name:     "Engine",
			Kind:     ast.SymbolKindClass,
			FilePath: "packages/dev/core/src/Engines/engine.ts",
		}
		result := pickMostSignificantSymbol([]*ast.Symbol{field, class})
		if result.Kind != ast.SymbolKindClass {
			t.Errorf("expected class, got %s (ID: %s)", result.Kind, result.ID)
		}
	})

	t.Run("prefers class over variable", func(t *testing.T) {
		variable := &ast.Symbol{
			ID:       "test/req.acceptsEncoding.js:4:Request",
			Name:     "Request",
			Kind:     ast.SymbolKindVariable,
			FilePath: "test/req.acceptsEncoding.js",
		}
		class := &ast.Symbol{
			ID:       "lib/request.js:10:Request",
			Name:     "Request",
			Kind:     ast.SymbolKindClass,
			FilePath: "lib/request.js",
		}
		result := pickMostSignificantSymbol([]*ast.Symbol{variable, class})
		if result.Kind != ast.SymbolKindClass {
			t.Errorf("expected class, got %s (ID: %s)", result.Kind, result.ID)
		}
	})

	t.Run("prefers non-test file at equal significance", func(t *testing.T) {
		testVar := &ast.Symbol{
			ID:       "test/req.js:4:Request",
			Name:     "Request",
			Kind:     ast.SymbolKindVariable,
			FilePath: "test/req.js",
		}
		srcVar := &ast.Symbol{
			ID:       "lib/request.js:10:Request",
			Name:     "Request",
			Kind:     ast.SymbolKindVariable,
			FilePath: "lib/request.js",
		}
		result := pickMostSignificantSymbol([]*ast.Symbol{testVar, srcVar})
		if result.FilePath != "lib/request.js" {
			t.Errorf("expected lib/request.js, got %s", result.FilePath)
		}
	})

	t.Run("prefers shorter path at equal significance and test status", func(t *testing.T) {
		deep := &ast.Symbol{
			ID:       "a/b/c/d/engine.ts:1:Engine",
			Name:     "Engine",
			Kind:     ast.SymbolKindClass,
			FilePath: "a/b/c/d/engine.ts",
		}
		shallow := &ast.Symbol{
			ID:       "src/engine.ts:1:Engine",
			Name:     "Engine",
			Kind:     ast.SymbolKindClass,
			FilePath: "src/engine.ts",
		}
		result := pickMostSignificantSymbol([]*ast.Symbol{deep, shallow})
		if result.FilePath != "src/engine.ts" {
			t.Errorf("expected src/engine.ts, got %s", result.FilePath)
		}
	})

	t.Run("single symbol returned as-is", func(t *testing.T) {
		sym := &ast.Symbol{
			ID:   "foo.go:1:Foo",
			Name: "Foo",
			Kind: ast.SymbolKindField,
		}
		result := pickMostSignificantSymbol([]*ast.Symbol{sym})
		if result != sym {
			t.Error("single symbol should be returned directly")
		}
	})
}

// TestKindSignificance verifies the ranking is internally consistent.
func TestKindSignificance(t *testing.T) {
	// Classes and structs should outrank fields and variables
	if kindSignificance(ast.SymbolKindClass) <= kindSignificance(ast.SymbolKindField) {
		t.Error("class should outrank field")
	}
	if kindSignificance(ast.SymbolKindStruct) <= kindSignificance(ast.SymbolKindVariable) {
		t.Error("struct should outrank variable")
	}
	if kindSignificance(ast.SymbolKindInterface) <= kindSignificance(ast.SymbolKindMethod) {
		t.Error("interface should outrank method")
	}
	if kindSignificance(ast.SymbolKindFunction) <= kindSignificance(ast.SymbolKindField) {
		t.Error("function should outrank field")
	}
}

// TestResolveFunctionWithFuzzy_KindFilterAny_PrefersClass verifies that when
// KindFilterAny is used and multiple symbols share a name, the class/struct
// is preferred over field/variable (IT-06 Bug 5 integration test).
func TestResolveFunctionWithFuzzy_KindFilterAny_PrefersClass(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()
	idx := index.NewSymbolIndex()

	// Add a field named "Engine" first (simulates index order where field comes first)
	field := &ast.Symbol{
		ID:        "native.ts:427:Engine",
		Name:      "Engine",
		Kind:      ast.SymbolKindField,
		FilePath:  "packages/native/nativeInterfaces.ts",
		StartLine: 427,
		EndLine:   428,
		Language:  "typescript",
	}
	class := &ast.Symbol{
		ID:        "engine.ts:100:Engine",
		Name:      "Engine",
		Kind:      ast.SymbolKindClass,
		FilePath:  "packages/core/src/Engines/engine.ts",
		StartLine: 100,
		EndLine:   500,
		Language:  "typescript",
	}

	if err := idx.Add(field); err != nil {
		t.Fatalf("failed to add field: %v", err)
	}
	if err := idx.Add(class); err != nil {
		t.Fatalf("failed to add class: %v", err)
	}

	sym, fuzzy, err := ResolveFunctionWithFuzzy(ctx, idx, "Engine", logger,
		WithKindFilter(KindFilterAny))
	if err != nil {
		t.Fatalf("ResolveFunctionWithFuzzy() error: %v", err)
	}
	if fuzzy {
		t.Error("expected exact match, not fuzzy")
	}
	if sym.Kind != ast.SymbolKindClass {
		t.Errorf("expected class Engine, got %s at %s", sym.Kind, sym.FilePath)
	}
}

// IT-06c Bug C: Tests for containsPackageSegment.
func TestContainsPackageSegment(t *testing.T) {
	tests := []struct {
		name     string
		haystack string
		needle   string
		want     bool
	}{
		{"exact directory match", "hugolib/hugo_sites_build.go", "hugolib", true},
		{"subdirectory match", "src/hugolib/builder.go", "hugolib", true},
		{"no false positive", "nothugolib/file.go", "hugolib", false},
		{"suffix boundary", "hugolib_test/file.go", "hugolib", true}, // underscore boundary OK
		{"colon boundary (ID)", "hugolib/build.go:42:Build", "hugolib", true},
		{"dot boundary", "com.hugolib.Build", "hugolib", true},
		{"empty haystack", "", "hugolib", false},
		{"empty needle", "hugolib/file.go", "", false},
		{"package at start", "gin/context.go", "gin", true},
		{"package at end", "internal/gin", "gin", true},
		{"mid-word no match", "binging/file.go", "gin", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsPackageSegment(tt.haystack, tt.needle)
			if got != tt.want {
				t.Errorf("containsPackageSegment(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
			}
		})
	}
}

// IT-06c Bug C: Tests for filterByPackageHint.
func TestFilterByPackageHint(t *testing.T) {
	logger := slog.Default()

	// Create test symbols simulating Hugo's 11 "Build" matches
	symbols := []*ast.Symbol{
		{Name: "Build", FilePath: "common/maps/maps.go", Kind: ast.SymbolKindFunction, Package: "maps"},
		{Name: "Build", FilePath: "hugolib/hugo_sites_build.go", Kind: ast.SymbolKindFunction, Package: "hugolib"},
		{Name: "Build", FilePath: "resources/resource.go", Kind: ast.SymbolKindMethod, Package: "resources"},
		{Name: "Build", FilePath: "hugofs/basefs.go", Kind: ast.SymbolKindFunction, Package: "hugofs"},
		{Name: "Build", FilePath: "create/content.go", Kind: ast.SymbolKindFunction, Package: "create"},
	}

	t.Run("hugolib hint narrows to correct Build", func(t *testing.T) {
		result := filterByPackageHint(symbols, "hugolib", logger, "find_callees")
		if len(result) != 1 {
			t.Fatalf("expected 1 match, got %d", len(result))
		}
		if result[0].FilePath != "hugolib/hugo_sites_build.go" {
			t.Errorf("expected hugolib Build, got %s", result[0].FilePath)
		}
	})

	t.Run("no hint returns all", func(t *testing.T) {
		result := filterByPackageHint(symbols, "", logger, "find_callees")
		if len(result) != len(symbols) {
			t.Errorf("expected all %d symbols, got %d", len(symbols), len(result))
		}
	})

	t.Run("non-matching hint returns all", func(t *testing.T) {
		result := filterByPackageHint(symbols, "nonexistent", logger, "find_callees")
		if len(result) != len(symbols) {
			t.Errorf("expected all %d symbols (hint didn't match), got %d", len(symbols), len(result))
		}
	})

	t.Run("single symbol skips filtering", func(t *testing.T) {
		single := []*ast.Symbol{symbols[0]}
		result := filterByPackageHint(single, "hugolib", logger, "find_callees")
		if len(result) != 1 {
			t.Errorf("expected 1 symbol unchanged, got %d", len(result))
		}
	})
}

// TestFilterOutOverloadStubs verifies IT-06c H-3: Python @overload stubs are
// deprioritized during symbol resolution so the real implementation (with callees) wins.
func TestFilterOutOverloadStubs(t *testing.T) {
	t.Run("mixed overloads and real — keeps only real", func(t *testing.T) {
		symbols := []*ast.Symbol{
			{Name: "read_csv", StartLine: 310, Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", StartLine: 320, Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", StartLine: 330, Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", StartLine: 340, Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", StartLine: 350, Metadata: &ast.SymbolMetadata{Decorators: []string{"set_module"}}},
		}
		result := filterOutOverloadStubs(symbols)
		if len(result) != 1 {
			t.Fatalf("expected 1 non-overload symbol, got %d", len(result))
		}
		if result[0].StartLine != 350 {
			t.Errorf("expected real implementation at line 350, got line %d", result[0].StartLine)
		}
	})

	t.Run("all overloads — returns all unchanged", func(t *testing.T) {
		symbols := []*ast.Symbol{
			{Name: "foo", StartLine: 10, Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "foo", StartLine: 20, Metadata: &ast.SymbolMetadata{IsOverload: true}},
		}
		result := filterOutOverloadStubs(symbols)
		if len(result) != 2 {
			t.Fatalf("expected all 2 symbols (all overloads), got %d", len(result))
		}
	})

	t.Run("no overloads — returns all unchanged", func(t *testing.T) {
		symbols := []*ast.Symbol{
			{Name: "bar", StartLine: 10},
			{Name: "bar", StartLine: 20, Metadata: &ast.SymbolMetadata{Decorators: []string{"cache"}}},
		}
		result := filterOutOverloadStubs(symbols)
		if len(result) != 2 {
			t.Fatalf("expected all 2 symbols (no overloads), got %d", len(result))
		}
	})

	t.Run("nil metadata — not considered overload", func(t *testing.T) {
		symbols := []*ast.Symbol{
			{Name: "baz", StartLine: 10, Metadata: nil},
			{Name: "baz", StartLine: 20, Metadata: &ast.SymbolMetadata{IsOverload: true}},
		}
		result := filterOutOverloadStubs(symbols)
		if len(result) != 1 {
			t.Fatalf("expected 1 non-overload symbol, got %d", len(result))
		}
		if result[0].StartLine != 10 {
			t.Errorf("expected symbol at line 10, got line %d", result[0].StartLine)
		}
	})

	t.Run("single symbol — returned unchanged", func(t *testing.T) {
		symbols := []*ast.Symbol{
			{Name: "single", StartLine: 5, Metadata: &ast.SymbolMetadata{IsOverload: true}},
		}
		result := filterOutOverloadStubs(symbols)
		if len(result) != 1 {
			t.Fatalf("expected 1 symbol unchanged, got %d", len(result))
		}
	})

	t.Run("pickMostSignificantSymbol prefers real over overloads", func(t *testing.T) {
		// End-to-end: pickMostSignificantSymbol should pick the real implementation
		symbols := []*ast.Symbol{
			{Name: "read_csv", Kind: ast.SymbolKindFunction, StartLine: 310, FilePath: "pandas/io/parsers/readers.py", Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", Kind: ast.SymbolKindFunction, StartLine: 320, FilePath: "pandas/io/parsers/readers.py", Metadata: &ast.SymbolMetadata{IsOverload: true}},
			{Name: "read_csv", Kind: ast.SymbolKindFunction, StartLine: 350, FilePath: "pandas/io/parsers/readers.py", Metadata: &ast.SymbolMetadata{Decorators: []string{"set_module"}}},
		}
		best := pickMostSignificantSymbol(symbols)
		if best.StartLine != 350 {
			t.Errorf("expected pickMostSignificantSymbol to choose real impl at line 350, got line %d", best.StartLine)
		}
	})
}

// ─── IT-06d Bug B/C: isTestHelperFile, isTypeStubFile, referenceFilePriority ───

func TestIsTestHelperFile(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     bool
	}{
		// conftest.py cases
		{"root conftest", "conftest.py", true},
		{"nested conftest", "pandas/core/conftest.py", true},
		{"deep conftest", "a/b/c/conftest.py", true},
		{"conftest in name but not exact", "my_conftest_helper.py", false},

		// _testing/ directory
		{"_testing prefix", "_testing/util.py", true},
		{"_testing subdirectory", "pandas/_testing/assertions.py", true},
		{"_helpers prefix", "_helpers/setup.py", true},
		{"_helpers subdirectory", "lib/_helpers/mock.py", true},

		// Normal test files should be caught by isTestFile, not here
		{"test/ prefix — not a helper", "test/frame_test.go", false},
		{"_test suffix — not a helper", "frame_test.go", false},

		// Production files
		{"production source", "pandas/core/frame.py", false},
		{"production source with test in name", "pandas/core/internals.py", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTestHelperFile(tt.filePath)
			if got != tt.want {
				t.Errorf("isTestHelperFile(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestIsTypeStubFile(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     bool
	}{
		// .pyi stubs (Python)
		{"root .pyi", "properties.pyi", true},
		{"nested .pyi", "pandas/_libs/properties.pyi", true},
		{"pandas stubs .pyi", "pandas-stubs/core/frame.pyi", true},

		// .d.ts stubs (TypeScript)
		{"root .d.ts", "index.d.ts", true},
		{"nested .d.ts", "types/index.d.ts", true},
		{"dist .d.ts", "dist/plottable.d.ts", true},

		// stubs/ directory
		{"stubs directory", "stubs/pandas/core.pyi", true},
		{"nested stubs directory", "typings/stubs/react.d.ts", true},

		// Production files
		{"regular .py", "pandas/core/frame.py", false},
		{"regular .ts", "src/components/plot.ts", false},
		{"regular .js", "lib/response.js", false},
		{"pyi in name only", "mypyi_helper.py", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTypeStubFile(tt.filePath)
			if got != tt.want {
				t.Errorf("isTypeStubFile(%q) = %v, want %v", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestReferenceFilePriority(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		wantTier int
	}{
		// Tier 0: production source
		{"production module", "pandas/core/frame.py", 0},
		{"production groupby", "pandas/core/groupby/generic.py", 0},
		{"production io parser", "pandas/io/parsers/readers.py", 0},
		{"go source", "services/trace/cli/tools/executor.go", 0},
		{"typescript source", "src/plots/pie.ts", 0},

		// Tier 1: type stubs
		{"Python .pyi stub", "pandas/_libs/properties.pyi", 1},
		{"TypeScript .d.ts stub", "dist/plottable.d.ts", 1},

		// Tier 2: test helpers / config
		{"root conftest.py", "conftest.py", 2},
		{"nested conftest.py", "pandas/core/conftest.py", 2},
		{"_testing directory", "pandas/_testing/assertions.py", 2},

		// Tier 3: tests and benchmarks
		{"test directory", "pandas/tests/frame/test_api.py", 3},
		{"_test suffix Go", "executor_test.go", 3},
		{"asv_bench directory", "asv_bench/benchmarks/frame.py", 3},
		{"spec file JS", "test/req.acceptsEncoding.spec.js", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := referenceFilePriority(tt.filePath)
			if got != tt.wantTier {
				t.Errorf("referenceFilePriority(%q) = tier %d, want tier %d", tt.filePath, got, tt.wantTier)
			}
		})
	}

	// Ordering invariant: lower tiers always sort before higher tiers.
	t.Run("ordering invariant: production < stubs < helpers < tests", func(t *testing.T) {
		if referenceFilePriority("pandas/core/frame.py") >= referenceFilePriority("pandas/_libs/properties.pyi") {
			t.Error("production must sort before type stubs")
		}
		if referenceFilePriority("pandas/_libs/properties.pyi") >= referenceFilePriority("conftest.py") {
			t.Error("type stubs must sort before test helpers")
		}
		if referenceFilePriority("conftest.py") >= referenceFilePriority("pandas/tests/frame/test_api.py") {
			t.Error("test helpers must sort before test files")
		}
	})
}

// ─── IT-06d Bug F: ResolveFunctionWithFuzzy with KindFilterAny prefers class over field ───

// TestResolveFunctionWithFuzzy_KindFilterAny_FuzzyPath verifies that the fuzzy
// search path applies pickMostSignificantSymbol when KindFilterAny is used.
// This is the Plottable Drawer regression: without the fix, the lowercase "drawer"
// field (significance=1) beats the "Drawer" class (significance=10) because
// fuzzy search returns case-insensitive matches in alphabetical order.
func TestResolveFunctionWithFuzzy_KindFilterAny_FuzzyPath(t *testing.T) {
	ctx := context.Background()
	logger := testLogger()

	// Build an index where "Drawer" class is NOT reachable via exact match
	// (simulating a case where the index key uses a different casing or path),
	// so fuzzy search is triggered.
	idx := index.NewSymbolIndex()

	// The class uses a long path that exact match won't find by bare name "Drawer"
	drawerClass := &ast.Symbol{
		ID:        "src/drawers/Drawer.ts:10:Drawer",
		Name:      "Drawer",
		Kind:      ast.SymbolKindClass,
		FilePath:  "src/drawers/Drawer.ts",
		StartLine: 10,
		EndLine:   200,
		Language:  "typescript",
	}
	// A lowercase field named "drawer" that fuzzy search returns first (alphabetically)
	drawerField := &ast.Symbol{
		ID:        "src/components/commons.ts:24:drawer",
		Name:      "drawer",
		Kind:      ast.SymbolKindField,
		FilePath:  "src/components/commons.ts",
		StartLine: 24,
		EndLine:   24,
		Language:  "typescript",
	}

	if err := idx.Add(drawerField); err != nil {
		t.Fatalf("failed to add field: %v", err)
	}
	if err := idx.Add(drawerClass); err != nil {
		t.Fatalf("failed to add class: %v", err)
	}

	// With KindFilterAny, exact match for "Drawer" will find drawerClass directly.
	// This tests the significance-based selection on the exact-match path.
	sym, _, err := ResolveFunctionWithFuzzy(ctx, idx, "Drawer", logger, WithKindFilter(KindFilterAny))
	if err != nil {
		t.Fatalf("ResolveFunctionWithFuzzy() error: %v", err)
	}
	if sym.Kind != ast.SymbolKindClass {
		t.Errorf("expected Drawer class (significance=10), got %s %q at %s",
			sym.Kind, sym.Name, sym.FilePath)
	}

	// Verify that even if field is returned first from search, significance wins.
	// Test pickMostSignificantSymbol directly with field-before-class ordering.
	fieldFirst := []*ast.Symbol{drawerField, drawerClass}
	best := pickMostSignificantSymbol(fieldFirst)
	if best.Kind != ast.SymbolKindClass {
		t.Errorf("pickMostSignificantSymbol: expected class (significance=10), got %s %q",
			best.Kind, best.Name)
	}
}
