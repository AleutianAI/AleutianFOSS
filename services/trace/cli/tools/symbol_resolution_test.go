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
