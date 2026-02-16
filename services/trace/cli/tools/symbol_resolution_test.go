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

// testLogger returns a discard logger for tests.
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
}
