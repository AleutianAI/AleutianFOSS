// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package code_buddy

import (
	"io"
	"log/slog"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// makeValidSymbol creates a minimal valid symbol for testing.
func makeValidSymbol(name string, filePath string, startLine int) *ast.Symbol {
	return &ast.Symbol{
		ID:        ast.GenerateID(filePath, startLine, name),
		Name:      name,
		Kind:      ast.SymbolKindFunction,
		FilePath:  filePath,
		StartLine: startLine,
		EndLine:   startLine + 5,
		StartCol:  0,
		EndCol:    1,
		Language:  "go",
	}
}

// makeInvalidSymbol creates a symbol that will fail Validate() (missing Language).
func makeInvalidSymbol(name string, filePath string, startLine int) *ast.Symbol {
	return &ast.Symbol{
		ID:        ast.GenerateID(filePath, startLine, name),
		Name:      name,
		Kind:      ast.SymbolKindFunction,
		FilePath:  filePath,
		StartLine: startLine,
		EndLine:   startLine + 5,
		StartCol:  0,
		EndCol:    1,
		Language:  "", // invalid — Validate requires non-empty
	}
}

// TestAddSymbolsToIndexRecursive_AllValid verifies that all valid symbols are added
// and counters are correct.
func TestAddSymbolsToIndexRecursive_AllValid(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	symbols := []*ast.Symbol{
		makeValidSymbol("Foo", "foo.go", 1),
		makeValidSymbol("Bar", "bar.go", 1),
		makeValidSymbol("Baz", "baz.go", 1),
	}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	if added != 3 {
		t.Errorf("expected added=3, got %d", added)
	}
	if dropped != 0 {
		t.Errorf("expected dropped=0, got %d", dropped)
	}
	if retried != 0 {
		t.Errorf("expected retried=0, got %d", retried)
	}

	stats := idx.Stats()
	if stats.TotalSymbols != 3 {
		t.Errorf("expected 3 symbols in index, got %d", stats.TotalSymbols)
	}
}

// TestAddSymbolsToIndexRecursive_DuplicateSkipped verifies that duplicate symbols
// are logged and counted as dropped.
func TestAddSymbolsToIndexRecursive_DuplicateSkipped(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sym := makeValidSymbol("Foo", "foo.go", 1)
	dupSym := makeValidSymbol("Foo", "foo.go", 1) // same ID

	symbols := []*ast.Symbol{sym, dupSym}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	if added != 1 {
		t.Errorf("expected added=1, got %d", added)
	}
	if dropped != 1 {
		t.Errorf("expected dropped=1 (duplicate), got %d", dropped)
	}
	if retried != 0 {
		t.Errorf("expected retried=0, got %d", retried)
	}
}

// TestAddSymbolsToIndexRecursive_InvalidChildCascade verifies that when a parent has
// an invalid child (causing parent Validate to fail), the function strips children,
// retries the parent, and adds children independently.
func TestAddSymbolsToIndexRecursive_InvalidChildCascade(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	invalidChild := makeInvalidSymbol("badChild", "foo.go", 10)
	validChild := makeValidSymbol("goodChild", "foo.go", 20)

	parent := makeValidSymbol("Parent", "foo.go", 1)
	parent.Children = []*ast.Symbol{invalidChild, validChild}

	symbols := []*ast.Symbol{parent}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	// Parent should fail first (invalid child in Validate), then retry succeeds without children
	// validChild should be added independently, invalidChild should be dropped
	if retried != 1 {
		t.Errorf("expected retried=1 (parent retried without children), got %d", retried)
	}
	if added != 2 {
		t.Errorf("expected added=2 (parent + validChild), got %d", added)
	}
	if dropped != 1 {
		t.Errorf("expected dropped=1 (invalidChild), got %d", dropped)
	}

	// Verify parent is in the index
	results := idx.GetByName("Parent")
	if len(results) != 1 {
		t.Errorf("expected Parent in index, got %d results", len(results))
	}

	// Verify validChild is in the index
	results = idx.GetByName("goodChild")
	if len(results) != 1 {
		t.Errorf("expected goodChild in index, got %d results", len(results))
	}

	// Verify invalidChild is NOT in the index
	results = idx.GetByName("badChild")
	if len(results) != 0 {
		t.Errorf("expected badChild NOT in index, got %d results", len(results))
	}
}

// TestAddSymbolsToIndexRecursive_NilSymbolsSkipped verifies that nil entries in the
// symbol slice are safely skipped.
func TestAddSymbolsToIndexRecursive_NilSymbolsSkipped(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	symbols := []*ast.Symbol{
		nil,
		makeValidSymbol("Foo", "foo.go", 1),
		nil,
	}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	if added != 1 {
		t.Errorf("expected added=1, got %d", added)
	}
	if dropped != 0 {
		t.Errorf("expected dropped=0, got %d", dropped)
	}
	if retried != 0 {
		t.Errorf("expected retried=0, got %d", retried)
	}
}

// TestAddSymbolsToIndexRecursive_InvalidSymbolNoChildren verifies that an invalid
// symbol without children is logged and dropped.
func TestAddSymbolsToIndexRecursive_InvalidSymbolNoChildren(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	symbols := []*ast.Symbol{
		makeInvalidSymbol("BadSym", "foo.go", 1),
	}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	if added != 0 {
		t.Errorf("expected added=0, got %d", added)
	}
	if dropped != 1 {
		t.Errorf("expected dropped=1, got %d", dropped)
	}
	if retried != 0 {
		t.Errorf("expected retried=0, got %d", retried)
	}
}

// TestAddSymbolsToIndexRecursive_ValidChildren verifies that valid child symbols
// are recursively added to the index.
func TestAddSymbolsToIndexRecursive_ValidChildren(t *testing.T) {
	idx := index.NewSymbolIndex()
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))

	child1 := makeValidSymbol("Method1", "foo.go", 5)
	child2 := makeValidSymbol("Method2", "foo.go", 15)

	parent := makeValidSymbol("MyClass", "foo.go", 1)
	parent.Children = []*ast.Symbol{child1, child2}

	symbols := []*ast.Symbol{parent}

	added, dropped, retried := addSymbolsToIndexRecursive(idx, symbols, logger)

	if added != 3 {
		t.Errorf("expected added=3 (parent + 2 children), got %d", added)
	}
	if dropped != 0 {
		t.Errorf("expected dropped=0, got %d", dropped)
	}
	if retried != 0 {
		t.Errorf("expected retried=0, got %d", retried)
	}

	// All three should be findable
	for _, name := range []string{"MyClass", "Method1", "Method2"} {
		results := idx.GetByName(name)
		if len(results) != 1 {
			t.Errorf("expected %s in index, got %d results", name, len(results))
		}
	}
}
