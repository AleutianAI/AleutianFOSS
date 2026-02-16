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

// TestGoParser_PackageFieldSet verifies that the Package field is set correctly
// on function and method symbols. This is critical for entry point detection
// which relies on matching both Name AND Package.
//
// Bug: GR-17 - Package field was empty, causing find_entry_points to fail.
func TestGoParser_PackageFieldSet(t *testing.T) {
	parser := NewGoParser()

	code := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
	helper()
}

func helper() {
	fmt.Println("World")
}

type Server struct{}

func (s *Server) Handle() {
	fmt.Println("Handling")
}
`

	result, err := parser.Parse(context.Background(), []byte(code), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Find the main function
	var mainFunc *Symbol
	var helperFunc *Symbol
	var handleMethod *Symbol

	for _, sym := range result.Symbols {
		switch {
		case sym.Kind == SymbolKindFunction && sym.Name == "main":
			mainFunc = sym
		case sym.Kind == SymbolKindFunction && sym.Name == "helper":
			helperFunc = sym
		case sym.Kind == SymbolKindMethod && sym.Name == "Handle":
			handleMethod = sym
		}
	}

	if mainFunc == nil {
		t.Fatal("main function not found")
	}
	if helperFunc == nil {
		t.Fatal("helper function not found")
	}
	if handleMethod == nil {
		t.Fatal("Handle method not found")
	}

	// Verify Package field is set
	if mainFunc.Package != "main" {
		t.Errorf("main function Package = %q, want %q", mainFunc.Package, "main")
	}
	if helperFunc.Package != "main" {
		t.Errorf("helper function Package = %q, want %q", helperFunc.Package, "main")
	}
	if handleMethod.Package != "main" {
		t.Errorf("Handle method Package = %q, want %q", handleMethod.Package, "main")
	}

	t.Logf("✓ All symbols have Package field set correctly")
	t.Logf("  main: Package=%q", mainFunc.Package)
	t.Logf("  helper: Package=%q", helperFunc.Package)
	t.Logf("  Handle: Package=%q", handleMethod.Package)
}

// TestGoParser_PackageFieldDifferentPackages verifies Package field works
// with different package names.
func TestGoParser_PackageFieldDifferentPackages(t *testing.T) {
	testCases := []struct {
		name        string
		packageName string
		code        string
	}{
		{
			name:        "package main",
			packageName: "main",
			code:        "package main\n\nfunc main() {}\n",
		},
		{
			name:        "package handlers",
			packageName: "handlers",
			code:        "package handlers\n\nfunc NewHandler() {}\n",
		},
		{
			name:        "package routes",
			packageName: "routes",
			code:        "package routes\n\nfunc SetupRoutes() {}\n",
		},
	}

	parser := NewGoParser()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parser.Parse(context.Background(), []byte(tc.code), "test.go")
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			// Find a function symbol
			var funcSym *Symbol
			for _, sym := range result.Symbols {
				if sym.Kind == SymbolKindFunction {
					funcSym = sym
					break
				}
			}

			if funcSym == nil {
				t.Fatal("no function symbol found")
			}

			if funcSym.Package != tc.packageName {
				t.Errorf("Package = %q, want %q", funcSym.Package, tc.packageName)
			}

			t.Logf("✓ %s: Package=%q", funcSym.Name, funcSym.Package)
		})
	}
}
