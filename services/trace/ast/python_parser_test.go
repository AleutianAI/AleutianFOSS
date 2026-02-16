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
	"strings"
	"sync"
	"testing"
	"time"
)

// Test data: comprehensive Python example from ticket
const pythonTestSource = `"""Module docstring for test_module."""

from typing import Optional, List
from dataclasses import dataclass
import os
from . import local_module
from ..utils import helper

__all__ = ["User", "fetch_user"]

MODULE_CONSTANT: str = "value"

@dataclass
class User:
    """A user in the system."""
    name: str
    email: Optional[str] = None

    def validate(self) -> bool:
        """Validate the user."""
        return bool(self.name)

    @classmethod
    def from_dict(cls, data: dict) -> "User":
        return cls(**data)

    @staticmethod
    def generate_id() -> str:
        return "id"

    @property
    def display_name(self) -> str:
        return self.name

    def _private_method(self) -> None:
        pass

    def __repr__(self) -> str:
        return f"User({self.name})"

async def fetch_user(user_id: int) -> User:
    """Fetch a user by ID."""
    pass

def helper_function() -> None:
    """A helper function."""
    def nested_function():
        """Nested inside helper."""
        pass

def _private_function() -> None:
    """Should be marked as not exported."""
    pass
`

func TestPythonParser_Parse_EmptyFile(t *testing.T) {
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(""), "empty.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Language != "python" {
		t.Errorf("expected language 'python', got %q", result.Language)
	}

	if result.FilePath != "empty.py" {
		t.Errorf("expected file path 'empty.py', got %q", result.FilePath)
	}
}

func TestPythonParser_Parse_ModuleDocstring(t *testing.T) {
	source := `"""This is the module docstring."""

def foo():
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the module symbol
	var moduleSymbol *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindPackage && sym.Name == "__module__" {
			moduleSymbol = sym
			break
		}
	}

	if moduleSymbol == nil {
		t.Fatal("expected module symbol with docstring")
	}

	if !strings.Contains(moduleSymbol.DocComment, "module docstring") {
		t.Errorf("expected docstring to contain 'module docstring', got %q", moduleSymbol.DocComment)
	}
}

func TestPythonParser_Parse_Function(t *testing.T) {
	source := `def hello(name: str) -> str:
    """Greet someone."""
    return f"Hello, {name}"
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the function
	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "hello" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'hello'")
	}

	if fn.StartLine != 1 {
		t.Errorf("expected start line 1, got %d", fn.StartLine)
	}

	if !strings.Contains(fn.Signature, "hello") {
		t.Errorf("expected signature to contain 'hello', got %q", fn.Signature)
	}

	if !strings.Contains(fn.DocComment, "Greet someone") {
		t.Errorf("expected docstring, got %q", fn.DocComment)
	}

	if fn.Metadata != nil && fn.Metadata.ReturnType != "" {
		if fn.Metadata.ReturnType != "str" {
			t.Errorf("expected return type 'str', got %q", fn.Metadata.ReturnType)
		}
	}
}

func TestPythonParser_Parse_AsyncFunction(t *testing.T) {
	source := `async def fetch_data(url: str) -> dict:
    """Fetch data from URL."""
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the async function
	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "fetch_data" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected async function 'fetch_data'")
	}

	if fn.Metadata == nil || !fn.Metadata.IsAsync {
		t.Error("expected function to be marked as async")
	}

	if !strings.Contains(fn.Signature, "async def") {
		t.Errorf("expected async signature, got %q", fn.Signature)
	}
}

func TestPythonParser_Parse_Class(t *testing.T) {
	source := `class MyClass:
    """A test class."""

    def method(self):
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the class
	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "MyClass" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'MyClass'")
	}

	if !strings.Contains(class.DocComment, "test class") {
		t.Errorf("expected docstring, got %q", class.DocComment)
	}

	// Check for method
	if len(class.Children) == 0 {
		t.Fatal("expected class to have children (methods)")
	}

	var method *Symbol
	for _, child := range class.Children {
		if child.Name == "method" {
			method = child
			break
		}
	}

	if method == nil {
		t.Error("expected method 'method' in class")
	}

	if method.Kind != SymbolKindMethod {
		t.Errorf("expected kind Method, got %s", method.Kind)
	}
}

func TestPythonParser_Parse_DecoratedFunction(t *testing.T) {
	source := `@decorator
@another_decorator
def decorated_func():
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "decorated_func" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected decorated function")
	}

	if fn.Metadata == nil || len(fn.Metadata.Decorators) == 0 {
		t.Fatal("expected decorators in metadata")
	}

	if len(fn.Metadata.Decorators) != 2 {
		t.Errorf("expected 2 decorators, got %d", len(fn.Metadata.Decorators))
	}
}

func TestPythonParser_Parse_DecoratedClass(t *testing.T) {
	source := `@dataclass
class DataClass:
    name: str
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "DataClass" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected decorated class")
	}

	if class.Metadata == nil || len(class.Metadata.Decorators) == 0 {
		t.Fatal("expected decorators in metadata")
	}

	found := false
	for _, dec := range class.Metadata.Decorators {
		if dec == "dataclass" {
			found = true
			break
		}
	}

	if !found {
		t.Error("expected @dataclass decorator")
	}
}

func TestPythonParser_Parse_NestedFunction(t *testing.T) {
	source := `def outer():
    def inner():
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var outer *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "outer" {
			outer = sym
			break
		}
	}

	if outer == nil {
		t.Fatal("expected outer function")
	}

	if len(outer.Children) == 0 {
		t.Fatal("expected nested function as child")
	}

	var inner *Symbol
	for _, child := range outer.Children {
		if child.Name == "inner" {
			inner = child
			break
		}
	}

	if inner == nil {
		t.Error("expected inner function")
	}
}

func TestPythonParser_Parse_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	parser := NewPythonParser()
	_, err := parser.Parse(ctx, []byte("def foo(): pass"), "test.py")

	if err == nil {
		t.Error("expected error from canceled context")
	}

	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected canceled error, got: %v", err)
	}
}

func TestPythonParser_Parse_Import(t *testing.T) {
	source := `import os`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Path != "os" {
		t.Errorf("expected import path 'os', got %q", imp.Path)
	}
}

func TestPythonParser_Parse_ImportAlias(t *testing.T) {
	source := `import numpy as np`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Path != "numpy" {
		t.Errorf("expected import path 'numpy', got %q", imp.Path)
	}
	if imp.Alias != "np" {
		t.Errorf("expected alias 'np', got %q", imp.Alias)
	}
}

func TestPythonParser_Parse_ImportFrom(t *testing.T) {
	source := `from collections import OrderedDict, Counter`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Path != "collections" {
		t.Errorf("expected import path 'collections', got %q", imp.Path)
	}
	if len(imp.Names) != 2 {
		t.Errorf("expected 2 names, got %d", len(imp.Names))
	}
}

func TestPythonParser_Parse_ImportWildcard(t *testing.T) {
	source := `from module import *`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsWildcard {
		t.Error("expected wildcard import")
	}
}

func TestPythonParser_Parse_RelativeImport(t *testing.T) {
	source := `from . import local`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsRelative {
		t.Error("expected relative import")
	}
}

func TestPythonParser_Parse_RelativeImportParent(t *testing.T) {
	source := `from ..utils import helper`

	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsRelative {
		t.Error("expected relative import")
	}
	if !strings.HasPrefix(imp.Path, "..") {
		t.Errorf("expected path to start with '..', got %q", imp.Path)
	}
}

func TestPythonParser_Parse_TypeHints(t *testing.T) {
	source := `def process(data: List[int]) -> Optional[str]:
    pass
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

	// Type hints should be in signature
	if !strings.Contains(fn.Signature, "data") {
		t.Errorf("expected signature to contain parameter, got %q", fn.Signature)
	}
}

func TestPythonParser_Parse_Property(t *testing.T) {
	source := `class MyClass:
    @property
    def value(self) -> int:
        return self._value
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "MyClass" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'MyClass'")
	}

	var prop *Symbol
	for _, child := range class.Children {
		if child.Name == "value" {
			prop = child
			break
		}
	}

	if prop == nil {
		t.Fatal("expected property 'value'")
	}

	if prop.Kind != SymbolKindProperty {
		t.Errorf("expected kind Property, got %s", prop.Kind)
	}
}

func TestPythonParser_Parse_StaticMethod(t *testing.T) {
	source := `class MyClass:
    @staticmethod
    def static_method():
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	var method *Symbol
	for _, child := range class.Children {
		if child.Name == "static_method" {
			method = child
			break
		}
	}

	if method == nil {
		t.Fatal("expected static_method")
	}

	if method.Metadata == nil || !method.Metadata.IsStatic {
		t.Error("expected static method to have IsStatic: true")
	}
}

func TestPythonParser_Parse_ClassMethod(t *testing.T) {
	source := `class MyClass:
    @classmethod
    def class_method(cls):
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	var method *Symbol
	for _, child := range class.Children {
		if child.Name == "class_method" {
			method = child
			break
		}
	}

	if method == nil {
		t.Fatal("expected class_method")
	}

	if method.Metadata == nil || !method.Metadata.IsStatic {
		t.Error("expected classmethod to have IsStatic: true")
	}
}

func TestPythonParser_Parse_MultipleDecorators(t *testing.T) {
	source := `@decorator1
@decorator2
@decorator3
def multi_decorated():
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "multi_decorated" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected decorated function")
	}

	if fn.Metadata == nil || len(fn.Metadata.Decorators) != 3 {
		t.Errorf("expected 3 decorators, got %v", fn.Metadata)
	}
}

func TestPythonParser_Parse_PrivateFunction(t *testing.T) {
	source := `def _private_function():
    pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "_private_function" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected private function")
	}

	if fn.Exported {
		t.Error("expected _private_function to be unexported")
	}
}

func TestPythonParser_Parse_MangledName(t *testing.T) {
	source := `class MyClass:
    def __mangled_method(self):
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	var method *Symbol
	for _, child := range class.Children {
		if child.Name == "__mangled_method" {
			method = child
			break
		}
	}

	if method == nil {
		t.Fatal("expected __mangled_method")
	}

	if method.Exported {
		t.Error("expected __mangled_method to be unexported")
	}
}

func TestPythonParser_Parse_DunderMethod(t *testing.T) {
	source := `class MyClass:
    def __init__(self):
        pass

    def __str__(self):
        pass
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	for _, child := range class.Children {
		if child.Name == "__init__" || child.Name == "__str__" {
			if !child.Exported {
				t.Errorf("expected dunder method %s to be exported", child.Name)
			}
		}
	}
}

func TestPythonParser_Parse_FileTooLarge(t *testing.T) {
	parser := NewPythonParser(WithPythonMaxFileSize(100)) // 100 bytes max

	largeContent := make([]byte, 200)
	for i := range largeContent {
		largeContent[i] = 'x'
	}

	_, err := parser.Parse(context.Background(), largeContent, "large.py")

	if err == nil {
		t.Error("expected error for file too large")
	}

	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size exceeded error, got: %v", err)
	}
}

func TestPythonParser_Parse_InvalidUTF8(t *testing.T) {
	parser := NewPythonParser()

	// Invalid UTF-8 sequence
	invalidContent := []byte{0xff, 0xfe}

	_, err := parser.Parse(context.Background(), invalidContent, "invalid.py")

	if err == nil {
		t.Error("expected error for invalid UTF-8")
	}

	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("expected UTF-8 error, got: %v", err)
	}
}

func TestPythonParser_Parse_Validation(t *testing.T) {
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(pythonTestSource), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validate should pass
	if err := result.Validate(); err != nil {
		t.Errorf("validation failed: %v", err)
	}

	// Check all symbols are valid
	for _, sym := range result.Symbols {
		if err := sym.Validate(); err != nil {
			t.Errorf("symbol %s validation failed: %v", sym.Name, err)
		}
	}
}

func TestPythonParser_Parse_Hash(t *testing.T) {
	parser := NewPythonParser()
	content := []byte("def foo(): pass")

	result1, err := parser.Parse(context.Background(), content, "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result2, err := parser.Parse(context.Background(), content, "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result1.Hash == "" {
		t.Error("expected non-empty hash")
	}

	if result1.Hash != result2.Hash {
		t.Error("expected deterministic hash for same content")
	}

	// Different content should produce different hash
	result3, err := parser.Parse(context.Background(), []byte("def bar(): pass"), "test.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result1.Hash == result3.Hash {
		t.Error("expected different hash for different content")
	}
}

func TestPythonParser_Parse_SyntaxError(t *testing.T) {
	source := `def broken(
    # Missing closing paren and body
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	// Should return partial result, not error
	if err != nil {
		t.Fatalf("expected partial result, got error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if !result.HasErrors() {
		t.Error("expected errors for syntax error")
	}
}

func TestPythonParser_Parse_IndentationError(t *testing.T) {
	source := `def foo():
pass  # Should be indented
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	// Should return partial result
	if err != nil {
		t.Fatalf("expected partial result, got error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Tree-sitter may or may not catch indentation as syntax error
	// Just ensure we get a result
}

func TestPythonParser_Parse_Concurrent(t *testing.T) {
	parser := NewPythonParser()
	sources := []string{
		`def func1(): pass`,
		`class Class1: pass`,
		`async def async1(): pass`,
		`import os`,
		`def func2(x: int) -> str: pass`,
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(sources)*10)

	// Run many concurrent parses
	for i := 0; i < 10; i++ {
		for j, src := range sources {
			wg.Add(1)
			go func(idx int, source string) {
				defer wg.Done()
				_, err := parser.Parse(context.Background(), []byte(source), "test.py")
				if err != nil {
					errors <- err
				}
			}(j, src)
		}
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent parse error: %v", err)
	}
}

func TestPythonParser_Language(t *testing.T) {
	parser := NewPythonParser()
	if parser.Language() != "python" {
		t.Errorf("expected language 'python', got %q", parser.Language())
	}
}

func TestPythonParser_Extensions(t *testing.T) {
	parser := NewPythonParser()
	extensions := parser.Extensions()

	expectedExts := map[string]bool{".py": true, ".pyi": true}
	for _, ext := range extensions {
		if !expectedExts[ext] {
			t.Errorf("unexpected extension: %q", ext)
		}
		delete(expectedExts, ext)
	}

	if len(expectedExts) > 0 {
		t.Errorf("missing extensions: %v", expectedExts)
	}
}

func TestPythonParser_Parse_ComprehensiveExample(t *testing.T) {
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(pythonTestSource), "test_module.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify imports
	if len(result.Imports) == 0 {
		t.Error("expected imports to be extracted")
	}

	// Find specific imports
	var typingImport, osImport, relativeImport *Import
	for i := range result.Imports {
		imp := &result.Imports[i]
		switch {
		case imp.Path == "typing":
			typingImport = imp
		case imp.Path == "os":
			osImport = imp
		case imp.IsRelative && strings.HasPrefix(imp.Path, "."):
			relativeImport = imp
		}
	}

	if typingImport == nil {
		t.Error("expected typing import")
	} else if len(typingImport.Names) == 0 {
		t.Error("expected typing import to have names")
	}

	if osImport == nil {
		t.Error("expected os import")
	}

	if relativeImport == nil {
		t.Error("expected relative import")
	}

	// Find User class
	var userClass *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "User" {
			userClass = sym
			break
		}
	}

	if userClass == nil {
		t.Fatal("expected User class")
	}

	if userClass.Metadata == nil || len(userClass.Metadata.Decorators) == 0 {
		t.Error("expected User class to have @dataclass decorator")
	}

	// Check User methods
	methodNames := make(map[string]bool)
	for _, child := range userClass.Children {
		methodNames[child.Name] = true
	}

	expectedMethods := []string{"validate", "from_dict", "generate_id", "display_name"}
	for _, name := range expectedMethods {
		if !methodNames[name] {
			t.Errorf("expected method %s in User class", name)
		}
	}

	// Find async function
	var fetchUser *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "fetch_user" {
			fetchUser = sym
			break
		}
	}

	if fetchUser == nil {
		t.Fatal("expected fetch_user function")
	}

	if fetchUser.Metadata == nil || !fetchUser.Metadata.IsAsync {
		t.Error("expected fetch_user to be async")
	}

	// Find helper_function with nested function
	var helperFn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "helper_function" {
			helperFn = sym
			break
		}
	}

	if helperFn == nil {
		t.Fatal("expected helper_function")
	}

	if len(helperFn.Children) == 0 {
		t.Error("expected helper_function to have nested function")
	}

	// Find private function
	var privateFn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "_private_function" {
			privateFn = sym
			break
		}
	}

	if privateFn == nil {
		t.Fatal("expected _private_function")
	}

	if privateFn.Exported {
		t.Error("expected _private_function to be unexported")
	}
}

func TestPythonParser_Parse_Timeout(t *testing.T) {
	// Create a context that times out very quickly
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for timeout
	time.Sleep(10 * time.Millisecond)

	parser := NewPythonParser()
	_, err := parser.Parse(ctx, []byte("def foo(): pass"), "test.py")

	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestPythonParser_Parse_ModuleVariable(t *testing.T) {
	source := `MODULE_CONSTANT: str = "value"
regular_variable = 42
`
	parser := NewPythonParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.py")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var constant, variable *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "MODULE_CONSTANT" {
			constant = sym
		}
		if sym.Name == "regular_variable" {
			variable = sym
		}
	}

	if constant == nil {
		t.Error("expected MODULE_CONSTANT")
	} else if constant.Kind != SymbolKindConstant {
		t.Errorf("expected MODULE_CONSTANT to be constant, got %s", constant.Kind)
	}

	if variable == nil {
		t.Error("expected regular_variable")
	} else if variable.Kind != SymbolKindVariable {
		t.Errorf("expected regular_variable to be variable, got %s", variable.Kind)
	}
}

// Benchmark parsing
func BenchmarkPythonParser_Parse(b *testing.B) {
	parser := NewPythonParser()
	content := []byte(pythonTestSource)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.Parse(context.Background(), content, "test.py")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPythonParser_Parse_Concurrent(b *testing.B) {
	parser := NewPythonParser()
	content := []byte(pythonTestSource)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := parser.Parse(context.Background(), content, "test.py")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// === GR-40a: Python Protocol Detection Tests ===

const pythonProtocolSource = `from typing import Protocol

class Handler(Protocol):
    def handle(self, request) -> Response:
        ...

    def close(self) -> None:
        ...

class Reader(Protocol):
    def read(self, n: int) -> bytes:
        ...

class FileHandler:
    def handle(self, request) -> Response:
        return Response()

    def close(self) -> None:
        pass

    def extra_method(self):
        pass

class PartialHandler:
    def handle(self, request) -> Response:
        return Response()
    # Missing close() method
`

const pythonABCSource = `from abc import ABC, abstractmethod

class BaseHandler(ABC):
    @abstractmethod
    def handle(self, request):
        pass

    @abstractmethod
    def close(self):
        pass
`

func TestPythonParser_ProtocolDetection(t *testing.T) {
	parser := NewPythonParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(pythonProtocolSource), "protocols.py")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	t.Run("Protocol classes are marked as interfaces", func(t *testing.T) {
		var handler, reader *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "Handler" {
				handler = sym
			}
			if sym.Name == "Reader" {
				reader = sym
			}
		}

		if handler == nil {
			t.Fatal("Handler class not found")
		}
		if handler.Kind != SymbolKindInterface {
			t.Errorf("expected Handler.Kind=SymbolKindInterface, got %v", handler.Kind)
		}

		if reader == nil {
			t.Fatal("Reader class not found")
		}
		if reader.Kind != SymbolKindInterface {
			t.Errorf("expected Reader.Kind=SymbolKindInterface, got %v", reader.Kind)
		}
	})

	t.Run("Protocol classes have methods in Metadata", func(t *testing.T) {
		var handler *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "Handler" {
				handler = sym
				break
			}
		}
		if handler == nil {
			t.Fatal("Handler not found")
		}

		if handler.Metadata == nil {
			t.Fatal("Handler.Metadata is nil")
		}

		if len(handler.Metadata.Methods) != 2 {
			t.Errorf("expected 2 methods in Handler.Metadata.Methods, got %d", len(handler.Metadata.Methods))
		}

		methodNames := make(map[string]bool)
		for _, m := range handler.Metadata.Methods {
			methodNames[m.Name] = true
		}
		if !methodNames["handle"] {
			t.Error("expected handle method in Handler.Metadata.Methods")
		}
		if !methodNames["close"] {
			t.Error("expected close method in Handler.Metadata.Methods")
		}
	})

	t.Run("Regular classes have methods in Metadata", func(t *testing.T) {
		var fileHandler *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "FileHandler" {
				fileHandler = sym
				break
			}
		}
		if fileHandler == nil {
			t.Fatal("FileHandler not found")
		}

		if fileHandler.Kind != SymbolKindClass {
			t.Errorf("expected FileHandler.Kind=SymbolKindClass, got %v", fileHandler.Kind)
		}

		if fileHandler.Metadata == nil {
			t.Fatal("FileHandler.Metadata is nil")
		}

		// Should have handle, close, extra_method
		if len(fileHandler.Metadata.Methods) != 3 {
			t.Errorf("expected 3 methods in FileHandler.Metadata.Methods, got %d", len(fileHandler.Metadata.Methods))
		}
	})

	t.Run("Partial implementation has fewer methods", func(t *testing.T) {
		var partial *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "PartialHandler" {
				partial = sym
				break
			}
		}
		if partial == nil {
			t.Fatal("PartialHandler not found")
		}

		if partial.Metadata == nil {
			t.Fatal("PartialHandler.Metadata is nil")
		}

		// Should only have handle method
		if len(partial.Metadata.Methods) != 1 {
			t.Errorf("expected 1 method in PartialHandler.Metadata.Methods, got %d", len(partial.Metadata.Methods))
		}
	})
}

func TestPythonParser_ABCDetection(t *testing.T) {
	parser := NewPythonParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(pythonABCSource), "abc_test.py")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	t.Run("ABC classes are marked as interfaces", func(t *testing.T) {
		var baseHandler *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "BaseHandler" {
				baseHandler = sym
				break
			}
		}

		if baseHandler == nil {
			t.Fatal("BaseHandler class not found")
		}
		if baseHandler.Kind != SymbolKindInterface {
			t.Errorf("expected BaseHandler.Kind=SymbolKindInterface, got %v", baseHandler.Kind)
		}
	})
}

func TestPythonParser_MethodSignatureExtraction(t *testing.T) {
	parser := NewPythonParser()
	ctx := context.Background()

	source := `class MyClass:
    def simple_method(self):
        pass

    def with_params(self, a, b, c):
        pass

    def with_return(self, x: int) -> str:
        return ""

    def with_tuple_return(self, x) -> Tuple[int, str, bool]:
        return (1, "", True)
`

	result, err := parser.Parse(ctx, []byte(source), "methods.py")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	var myClass *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "MyClass" {
			myClass = sym
			break
		}
	}
	if myClass == nil {
		t.Fatal("MyClass not found")
	}

	if myClass.Metadata == nil || len(myClass.Metadata.Methods) == 0 {
		t.Fatal("MyClass.Metadata.Methods is empty")
	}

	methodsByName := make(map[string]MethodSignature)
	for _, m := range myClass.Metadata.Methods {
		methodsByName[m.Name] = m
	}

	t.Run("simple method has 0 params (self excluded)", func(t *testing.T) {
		m := methodsByName["simple_method"]
		if m.ParamCount != 0 {
			t.Errorf("expected ParamCount=0, got %d", m.ParamCount)
		}
	})

	t.Run("method with params excludes self", func(t *testing.T) {
		m := methodsByName["with_params"]
		if m.ParamCount != 3 {
			t.Errorf("expected ParamCount=3, got %d", m.ParamCount)
		}
	})

	t.Run("method with return type", func(t *testing.T) {
		m := methodsByName["with_return"]
		if m.ReturnCount != 1 {
			t.Errorf("expected ReturnCount=1, got %d", m.ReturnCount)
		}
	})

	t.Run("method with tuple return", func(t *testing.T) {
		m := methodsByName["with_tuple_return"]
		if m.ReturnCount != 3 {
			t.Errorf("expected ReturnCount=3, got %d", m.ReturnCount)
		}
	})
}

// === GR-40a H-2: Benchmark Tests for Protocol Detection ===

// BenchmarkProtocolDetection benchmarks Protocol class detection.
func BenchmarkProtocolDetection(b *testing.B) {
	parser := NewPythonParser()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(ctx, []byte(pythonProtocolSource), "protocol.py")
	}
}

// BenchmarkMethodSignatureExtraction benchmarks method signature extraction.
func BenchmarkMethodSignatureExtraction(b *testing.B) {
	parser := NewPythonParser()
	ctx := context.Background()

	source := `class LargeClass:
    def method1(self, a: int, b: str) -> bool: pass
    def method2(self, x, y, z) -> Tuple[int, str]: pass
    def method3(self) -> None: pass
    def method4(self, data: bytes) -> int: pass
    def method5(self, callback) -> list: pass
    def method6(self, ctx, opts) -> dict: pass
    def method7(self, a, b, c, d, e) -> str: pass
    def method8(self) -> object: pass
    def method9(self, arg) -> None: pass
    def method10(self, x: float) -> float: pass
`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(ctx, []byte(source), "large.py")
	}
}

// BenchmarkABCDetection benchmarks ABC class detection.
func BenchmarkABCDetection(b *testing.B) {
	parser := NewPythonParser()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(ctx, []byte(pythonABCSource), "abc.py")
	}
}

// GR-41: Tests for Python call site extraction

const pythonCallsSource = `
class Flask:
    """A minimal Flask-like app."""

    def wsgi_app(self, environ, start_response):
        """The WSGI application."""
        ctx = self.request_context(environ)
        response = self.full_dispatch_request()
        return response(environ, start_response)

    def full_dispatch_request(self):
        """Dispatch the request."""
        self.try_trigger_before_first_request_functions()
        rv = self.dispatch_request()
        return self.finalize_request(rv)

    def dispatch_request(self):
        """Dispatch to the handler."""
        rule = self.url_map.match()
        return self.view_functions[rule.endpoint]()

def standalone_function():
    """A top-level function that calls other functions."""
    result = helper()
    data = process_data(result)
    return format_output(data)

class Server:
    def start(self):
        db.connect()
        self.listen()
        logger.info("started")

    def handle_request(self, request):
        validated = validate(request)
        response = self.process(validated)
        self.send_response(response)
`

func TestPythonParser_ExtractCallSites_SelfMethodCalls(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the wsgi_app method
	var wsgiApp *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Flask" {
			for _, child := range sym.Children {
				if child.Name == "wsgi_app" {
					wsgiApp = child
					break
				}
			}
		}
	}

	if wsgiApp == nil {
		t.Fatal("wsgi_app method not found")
	}

	if len(wsgiApp.Calls) == 0 {
		t.Fatal("wsgi_app should have call sites extracted")
	}

	// wsgi_app calls: self.request_context(), self.full_dispatch_request(), response()
	callTargets := make(map[string]bool)
	for _, call := range wsgiApp.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["request_context"] {
		t.Error("expected call to request_context, got calls:", callTargetNames(wsgiApp.Calls))
	}
	if !callTargets["full_dispatch_request"] {
		t.Error("expected call to full_dispatch_request, got calls:", callTargetNames(wsgiApp.Calls))
	}

	// Verify self.method() calls have IsMethod=true and Receiver="self"
	for _, call := range wsgiApp.Calls {
		if call.Target == "request_context" || call.Target == "full_dispatch_request" {
			if !call.IsMethod {
				t.Errorf("call to %s should be IsMethod=true", call.Target)
			}
			if call.Receiver != "self" {
				t.Errorf("call to %s should have Receiver='self', got %q", call.Target, call.Receiver)
			}
		}
	}
}

func TestPythonParser_ExtractCallSites_FullDispatchRequest(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find full_dispatch_request method
	var fullDispatch *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Flask" {
			for _, child := range sym.Children {
				if child.Name == "full_dispatch_request" {
					fullDispatch = child
					break
				}
			}
		}
	}

	if fullDispatch == nil {
		t.Fatal("full_dispatch_request method not found")
	}

	// full_dispatch_request calls: self.try_trigger_before_first_request_functions(),
	// self.dispatch_request(), self.finalize_request()
	if len(fullDispatch.Calls) < 3 {
		t.Errorf("expected at least 3 calls, got %d: %v", len(fullDispatch.Calls), callTargetNames(fullDispatch.Calls))
	}

	callTargets := make(map[string]bool)
	for _, call := range fullDispatch.Calls {
		callTargets[call.Target] = true
	}

	for _, expected := range []string{"try_trigger_before_first_request_functions", "dispatch_request", "finalize_request"} {
		if !callTargets[expected] {
			t.Errorf("expected call to %s, got: %v", expected, callTargetNames(fullDispatch.Calls))
		}
	}
}

func TestPythonParser_ExtractCallSites_SimpleFunctionCalls(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find standalone_function
	var standaloneFn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "standalone_function" {
			standaloneFn = sym
			break
		}
	}

	if standaloneFn == nil {
		t.Fatal("standalone_function not found")
	}

	if len(standaloneFn.Calls) < 3 {
		t.Errorf("expected at least 3 calls, got %d: %v", len(standaloneFn.Calls), callTargetNames(standaloneFn.Calls))
	}

	callTargets := make(map[string]bool)
	for _, call := range standaloneFn.Calls {
		callTargets[call.Target] = true
	}

	for _, expected := range []string{"helper", "process_data", "format_output"} {
		if !callTargets[expected] {
			t.Errorf("expected call to %s, got: %v", expected, callTargetNames(standaloneFn.Calls))
		}
	}

	// These should NOT be method calls
	for _, call := range standaloneFn.Calls {
		if call.IsMethod {
			t.Errorf("call to %s should not be IsMethod (receiver=%q)", call.Target, call.Receiver)
		}
	}
}

func TestPythonParser_ExtractCallSites_MixedCalls(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find Server.handle_request — has both simple calls and self.method() calls
	var handleRequest *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Server" {
			for _, child := range sym.Children {
				if child.Name == "handle_request" {
					handleRequest = child
					break
				}
			}
		}
	}

	if handleRequest == nil {
		t.Fatal("handle_request method not found")
	}

	// handle_request calls: validate(request), self.process(validated), self.send_response(response)
	if len(handleRequest.Calls) < 3 {
		t.Errorf("expected at least 3 calls, got %d: %v", len(handleRequest.Calls), callTargetNames(handleRequest.Calls))
	}

	for _, call := range handleRequest.Calls {
		switch call.Target {
		case "validate":
			if call.IsMethod {
				t.Error("validate should not be a method call")
			}
		case "process":
			if !call.IsMethod || call.Receiver != "self" {
				t.Errorf("process should be self.process, got IsMethod=%v, Receiver=%q", call.IsMethod, call.Receiver)
			}
		case "send_response":
			if !call.IsMethod || call.Receiver != "self" {
				t.Errorf("send_response should be self.send_response, got IsMethod=%v, Receiver=%q", call.IsMethod, call.Receiver)
			}
		}
	}
}

func TestPythonParser_ExtractCallSites_ObjectMethodCalls(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find Server.start — has obj.method() calls (db.connect, logger.info)
	var startMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Server" {
			for _, child := range sym.Children {
				if child.Name == "start" {
					startMethod = child
					break
				}
			}
		}
	}

	if startMethod == nil {
		t.Fatal("start method not found")
	}

	// start calls: db.connect(), self.listen(), logger.info()
	callMap := make(map[string]*CallSite)
	for i := range startMethod.Calls {
		callMap[startMethod.Calls[i].Target] = &startMethod.Calls[i]
	}

	if call, ok := callMap["connect"]; ok {
		if !call.IsMethod || call.Receiver != "db" {
			t.Errorf("db.connect should have IsMethod=true, Receiver='db', got %v, %q", call.IsMethod, call.Receiver)
		}
	} else {
		t.Error("expected call to connect (from db.connect)")
	}

	if call, ok := callMap["listen"]; ok {
		if !call.IsMethod || call.Receiver != "self" {
			t.Errorf("self.listen should have IsMethod=true, Receiver='self', got %v, %q", call.IsMethod, call.Receiver)
		}
	} else {
		t.Error("expected call to listen (from self.listen)")
	}

	if call, ok := callMap["info"]; ok {
		if !call.IsMethod || call.Receiver != "logger" {
			t.Errorf("logger.info should have IsMethod=true, Receiver='logger', got %v, %q", call.IsMethod, call.Receiver)
		}
	} else {
		t.Error("expected call to info (from logger.info)")
	}
}

func TestPythonParser_ExtractCallSites_CallLocations(t *testing.T) {
	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(pythonCallsSource), "flask_app.py")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that call locations have valid line numbers
	for _, sym := range result.Symbols {
		checkCallLocations(t, sym)
		for _, child := range sym.Children {
			checkCallLocations(t, child)
		}
	}
}

func checkCallLocations(t *testing.T, sym *Symbol) {
	t.Helper()
	for _, call := range sym.Calls {
		if call.Location.StartLine <= 0 {
			t.Errorf("call to %s in %s has invalid StartLine: %d", call.Target, sym.Name, call.Location.StartLine)
		}
		if call.Location.FilePath != "flask_app.py" {
			t.Errorf("call to %s in %s has wrong FilePath: %q", call.Target, sym.Name, call.Location.FilePath)
		}
	}
}

// callTargetNames is a helper to extract call target names for error messages.
func callTargetNames(calls []CallSite) []string {
	names := make([]string, len(calls))
	for i, call := range calls {
		if call.IsMethod {
			names[i] = call.Receiver + "." + call.Target
		} else {
			names[i] = call.Target
		}
	}
	return names
}
