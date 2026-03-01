// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package ast

import (
	"context"
	"testing"
)

// ============================================================================
// Python TypeReference extraction tests (IT-06 Bug 9)
// ============================================================================

func TestPythonParser_TypeReferences_FunctionParams(t *testing.T) {
	source := `
from pandas import Series, DataFrame

def process(data: Series, config: Config) -> DataFrame:
    return DataFrame(data)
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "process" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'process'")
	}

	if len(fn.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on function 'process'")
	}

	names := make(map[string]bool)
	for _, tr := range fn.TypeReferences {
		names[tr.Name] = true
	}

	for _, expected := range []string{"Series", "Config", "DataFrame"} {
		if !names[expected] {
			t.Errorf("expected TypeReferences to contain '%s', got %v", expected, fn.TypeReferences)
		}
	}
}

func TestPythonParser_TypeReferences_SkipsPrimitives(t *testing.T) {
	source := `
def foo(x: int, y: str) -> bool:
    return True
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	// All types are primitives — should have no TypeReferences
	if len(fn.TypeReferences) != 0 {
		t.Errorf("expected no TypeReferences for primitive-only annotations, got %v", fn.TypeReferences)
	}
}

func TestPythonParser_TypeReferences_ClassVariable(t *testing.T) {
	source := `
class Foo:
    Bar: Config
    Baz: int
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Class variables are children of the class symbol
	var barSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Foo" && sym.Kind == SymbolKindClass {
			for _, child := range sym.Children {
				if child.Name == "Bar" {
					barSym = child
					break
				}
			}
		}
	}

	if barSym == nil {
		t.Fatal("expected class variable 'Bar' as child of class 'Foo'")
	}

	if len(barSym.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on variable 'Bar'")
	}

	if barSym.TypeReferences[0].Name != "Config" {
		t.Errorf("expected TypeReference 'Config', got '%s'", barSym.TypeReferences[0].Name)
	}
}

func TestPythonParser_TypeReferences_OptionalWrapped(t *testing.T) {
	source := `
from typing import Optional

def foo(x: Optional[Series]) -> None:
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	// Optional is in skip list, but Series should be extracted
	names := make(map[string]bool)
	for _, tr := range fn.TypeReferences {
		names[tr.Name] = true
	}

	if !names["Series"] {
		t.Errorf("expected TypeReferences to contain 'Series', got %v", fn.TypeReferences)
	}
	if names["Optional"] {
		t.Error("expected TypeReferences to NOT contain 'Optional' (skip list)")
	}
}

// ============================================================================
// TypeScript TypeReference extraction tests (IT-06 Bug 9)
// ============================================================================

func TestTypeScriptParser_TypeReferences_FunctionParams(t *testing.T) {
	source := `export function process(data: Series, config: Config): DataFrame {
    return new DataFrame(data);
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "process" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'process'")
	}

	if len(fn.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on function 'process'")
	}

	names := make(map[string]bool)
	for _, tr := range fn.TypeReferences {
		names[tr.Name] = true
	}

	for _, expected := range []string{"Series", "Config", "DataFrame"} {
		if !names[expected] {
			t.Errorf("expected TypeReferences to contain '%s', got %v", expected, fn.TypeReferences)
		}
	}
}

func TestTypeScriptParser_TypeReferences_SkipsPrimitives(t *testing.T) {
	source := `export function foo(x: number, y: string): boolean {
    return true;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	// All types are primitives — should have no TypeReferences
	if len(fn.TypeReferences) != 0 {
		t.Errorf("expected no TypeReferences for primitive-only annotations, got %v", fn.TypeReferences)
	}
}

func TestTypeScriptParser_TypeReferences_MethodParams(t *testing.T) {
	source := `export class Foo {
    public process(data: Series): Config {
        return new Config();
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Methods are children of the class symbol
	var method *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Foo" && sym.Kind == SymbolKindClass {
			for _, child := range sym.Children {
				if child.Name == "process" && child.Kind == SymbolKindMethod {
					method = child
					break
				}
			}
		}
	}

	if method == nil {
		t.Fatal("expected method 'process' as child of class 'Foo'")
	}

	if len(method.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on method 'process'")
	}

	names := make(map[string]bool)
	for _, tr := range method.TypeReferences {
		names[tr.Name] = true
	}

	if !names["Series"] {
		t.Errorf("expected TypeReferences to contain 'Series', got %v", method.TypeReferences)
	}
	if !names["Config"] {
		t.Errorf("expected TypeReferences to contain 'Config', got %v", method.TypeReferences)
	}
}

// ============================================================================
// Go TypeReference extraction tests (IT-06 Bug 9)
// ============================================================================

func TestGoParser_TypeReferences_FunctionParams(t *testing.T) {
	source := `package main

func Process(data *Series, config Config) *DataFrame {
    return &DataFrame{}
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "Process" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'Process'")
	}

	if len(fn.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on function 'Process'")
	}

	names := make(map[string]bool)
	for _, tr := range fn.TypeReferences {
		names[tr.Name] = true
	}

	for _, expected := range []string{"Series", "Config", "DataFrame"} {
		if !names[expected] {
			t.Errorf("expected TypeReferences to contain '%s', got %v", expected, fn.TypeReferences)
		}
	}
}

func TestGoParser_TypeReferences_SkipsPrimitives(t *testing.T) {
	source := `package main

func Foo(x int, y string) bool {
    return true
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "Foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'Foo'")
	}

	if len(fn.TypeReferences) != 0 {
		t.Errorf("expected no TypeReferences for primitive-only types, got %v", fn.TypeReferences)
	}
}

func TestGoParser_TypeReferences_MethodParams(t *testing.T) {
	source := `package main

type Foo struct{}

func (f *Foo) Process(data Series) Config {
    return Config{}
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var method *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod && sym.Name == "Process" {
			method = sym
			break
		}
	}

	if method == nil {
		t.Fatal("expected method 'Process'")
	}

	if len(method.TypeReferences) == 0 {
		t.Fatal("expected TypeReferences on method 'Process'")
	}

	names := make(map[string]bool)
	for _, tr := range method.TypeReferences {
		names[tr.Name] = true
	}

	if !names["Series"] {
		t.Errorf("expected TypeReferences to contain 'Series', got %v", method.TypeReferences)
	}
	if !names["Config"] {
		t.Errorf("expected TypeReferences to contain 'Config', got %v", method.TypeReferences)
	}
}

func TestGoParser_TypeReferences_MultipleReturnTypes(t *testing.T) {
	source := `package main

func Fetch(id string) (Config, error) {
    return Config{}, nil
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "Fetch" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'Fetch'")
	}

	names := make(map[string]bool)
	for _, tr := range fn.TypeReferences {
		names[tr.Name] = true
	}

	// Config should be present, error and string are builtins
	if !names["Config"] {
		t.Errorf("expected TypeReferences to contain 'Config', got %v", fn.TypeReferences)
	}
	if names["error"] {
		t.Error("expected TypeReferences to NOT contain 'error' (builtin)")
	}
	if names["string"] {
		t.Error("expected TypeReferences to NOT contain 'string' (builtin)")
	}
}

// ============================================================================
// Builder extractTypeRefEdges tests (IT-06 Bug 9)
// ============================================================================
// Builder integration tests are in services/trace/graph/builder_test.go
// and verified via integration tests. The parser-level tests above
// confirm TypeReferences are populated correctly.
