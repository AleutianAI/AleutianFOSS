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
	"strings"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestIsGenericWord(t *testing.T) {
	t.Run("rejects programming construct nouns", func(t *testing.T) {
		words := []string{
			"class", "classes", "interface", "interfaces",
			"struct", "structs", "type", "types",
			"function", "functions", "method", "methods",
			"module", "modules", "package", "packages",
			"variable", "variables", "constant", "constants",
			"prototype", "prototypes", "constructor", "constructors",
			"object", "objects", "property", "properties",
			"field", "fields", "parameter", "parameters",
			"argument", "arguments", "enum", "enums",
		}
		for _, w := range words {
			if !isGenericWord(w) {
				t.Errorf("expected isGenericWord(%q) = true", w)
			}
		}
	})

	t.Run("rejects relationship nouns", func(t *testing.T) {
		words := []string{
			"implementation", "implementations",
			"extension", "extensions",
			"subclass", "subclasses",
			"caller", "callers", "callee", "callees",
			"reference", "references",
			"dependency", "dependencies",
		}
		for _, w := range words {
			if !isGenericWord(w) {
				t.Errorf("expected isGenericWord(%q) = true", w)
			}
		}
	})

	t.Run("rejects English stopwords", func(t *testing.T) {
		words := []string{"the", "a", "an", "all", "any", "some", "this", "that", "what", "which"}
		for _, w := range words {
			if !isGenericWord(w) {
				t.Errorf("expected isGenericWord(%q) = true", w)
			}
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		for _, w := range []string{"Classes", "CLASSES", "cLaSsEs", "FUNCTION", "Type"} {
			if !isGenericWord(w) {
				t.Errorf("expected isGenericWord(%q) = true (case insensitive)", w)
			}
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		for _, w := range []string{" classes ", "\tfunction\n", "  type "} {
			if !isGenericWord(w) {
				t.Errorf("expected isGenericWord(%q) = true (whitespace trimmed)", w)
			}
		}
	})

	t.Run("accepts real symbol names", func(t *testing.T) {
		names := []string{
			"Router", "Handler", "Scale", "Plot", "Axis",
			"AbstractMesh", "SessionInterface", "DataFrame",
			"handleRequest", "NewServer", "Parse",
			"http.Handler", "gin.Context", "io.Reader",
			"main", "init", "setUp", "tearDown",
			"Config", "Logger", "Middleware", "Component",
			"UserService", "AuthController", "EventEmitter",
		}
		for _, n := range names {
			if isGenericWord(n) {
				t.Errorf("expected isGenericWord(%q) = false (real symbol name)", n)
			}
		}
	})

	t.Run("accepts empty string", func(t *testing.T) {
		// Empty string is not a generic word — emptiness is handled by ValidateSymbolName
		if isGenericWord("") {
			t.Error("expected isGenericWord(\"\") = false")
		}
	})
}

func TestValidateSymbolName(t *testing.T) {
	t.Run("rejects empty string", func(t *testing.T) {
		err := ValidateSymbolName("", "function_name", "'Serve', 'Parse'")
		if err == nil {
			t.Fatal("expected error for empty name")
		}
		if !strings.Contains(err.Error(), "function_name is required") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects generic word with helpful message", func(t *testing.T) {
		err := ValidateSymbolName("classes", "interface_name", "'Scale', 'Router'")
		if err == nil {
			t.Fatal("expected error for generic word")
		}
		if !strings.Contains(err.Error(), "appears to be a generic word") {
			t.Errorf("expected 'generic word' in error: %v", err)
		}
		if !strings.Contains(err.Error(), "'Scale', 'Router'") {
			t.Errorf("expected examples in error: %v", err)
		}
		if !strings.Contains(err.Error(), "interface_name") {
			t.Errorf("expected param name in error: %v", err)
		}
	})

	t.Run("accepts valid symbol names", func(t *testing.T) {
		for _, name := range []string{"Router", "handleRequest", "Scale", "DataFrame"} {
			if err := ValidateSymbolName(name, "name", "'x'"); err != nil {
				t.Errorf("ValidateSymbolName(%q) = %v, want nil", name, err)
			}
		}
	})

	t.Run("uses correct param name in errors", func(t *testing.T) {
		err := ValidateSymbolName("", "target", "'main'")
		if !strings.Contains(err.Error(), "target is required") {
			t.Errorf("expected 'target is required': %v", err)
		}

		err = ValidateSymbolName("functions", "target", "'main'")
		if !strings.Contains(err.Error(), "target 'functions'") {
			t.Errorf("expected 'target' in generic word error: %v", err)
		}
	})
}

func TestMatchesPackageScope(t *testing.T) {
	t.Run("empty filter matches everything", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "foo",
			FilePath: "src/core/handler.go",
			Package:  "core",
		}
		if !matchesPackageScope(sym, "") {
			t.Error("expected empty filter to match")
		}
	})

	t.Run("nil symbol returns false", func(t *testing.T) {
		if matchesPackageScope(nil, "core") {
			t.Error("expected nil symbol to not match")
		}
	})

	t.Run("Go exact package match", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "HandleRequest",
			FilePath: "handlers/agent.go",
			Package:  "handlers",
		}
		if !matchesPackageScope(sym, "handlers") {
			t.Error("expected Go package exact match")
		}
	})

	t.Run("Go package match is case insensitive", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "HandleRequest",
			FilePath: "handlers/agent.go",
			Package:  "handlers",
		}
		if !matchesPackageScope(sym, "Handlers") {
			t.Error("expected case-insensitive Go package match")
		}
	})

	t.Run("file path boundary match for directory segment", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "merge",
			FilePath: "pandas/core/reshape/merge.py",
			Package:  "", // Python: Package not set
		}
		if !matchesPackageScope(sym, "reshape") {
			t.Error("expected 'reshape' to match directory segment in file path")
		}
	})

	t.Run("Python empty-Package path match", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "helper_func",
			FilePath: "src/flask/helpers.py",
			Package:  "", // Python: Package not set
		}
		if !matchesPackageScope(sym, "flask") {
			t.Error("expected 'flask' to match directory segment for Python symbol")
		}
	})

	t.Run("JS file stem match", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "Engine",
			FilePath: "src/Engines/engine.ts",
			Package:  "", // TS: Package not set
		}
		if !matchesPackageScope(sym, "engine") {
			t.Error("expected 'engine' to match file stem 'engine.ts'")
		}
	})

	t.Run("no false positive on substring", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "dialogHelper",
			FilePath: "src/ui/dialog/helper.go",
			Package:  "dialog",
		}
		// "log" should NOT match "dialog" because containsPackageSegment
		// requires boundary-aware matching
		if matchesPackageScope(sym, "log") {
			t.Error("expected 'log' to NOT match 'dialog' (substring, not boundary)")
		}
	})

	t.Run("no match returns false", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "foo",
			FilePath: "src/core/handler.go",
			Package:  "core",
		}
		if matchesPackageScope(sym, "nonexistent") {
			t.Error("expected 'nonexistent' to not match")
		}
	})

	t.Run("PascalCase directory matched case insensitively", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "AbstractMesh",
			FilePath: "src/Engines/Meshes/abstractMesh.ts",
			Package:  "",
		}
		if !matchesPackageScope(sym, "meshes") {
			t.Error("expected 'meshes' to match 'Meshes' directory (case insensitive)")
		}
	})

	t.Run("Go slash-path package", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "Parse",
			FilePath: "hugolib/page/parse.go",
			Package:  "page",
		}
		if !matchesPackageScope(sym, "page") {
			t.Error("expected 'page' to match Go package 'page'")
		}
	})

	// IT-08d: Trailing slash stripped from filter
	t.Run("trailing slash stripped from filter", func(t *testing.T) {
		sym := &ast.Symbol{
			Name:     "mathUtils",
			FilePath: "src/utils/mathutils.ts",
			Package:  "",
		}
		if !matchesPackageScope(sym, "src/utils/") {
			t.Error("expected 'src/utils/' (with trailing slash) to match 'src/utils/mathutils.ts' after TrimRight")
		}
	})
}
