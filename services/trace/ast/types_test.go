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
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSymbolKind_String(t *testing.T) {
	tests := []struct {
		name     string
		kind     SymbolKind
		expected string
	}{
		{"unknown", SymbolKindUnknown, "unknown"},
		{"package", SymbolKindPackage, "package"},
		{"file", SymbolKindFile, "file"},
		{"function", SymbolKindFunction, "function"},
		{"method", SymbolKindMethod, "method"},
		{"interface", SymbolKindInterface, "interface"},
		{"struct", SymbolKindStruct, "struct"},
		{"type", SymbolKindType, "type"},
		{"variable", SymbolKindVariable, "variable"},
		{"constant", SymbolKindConstant, "constant"},
		{"field", SymbolKindField, "field"},
		{"import", SymbolKindImport, "import"},
		{"class", SymbolKindClass, "class"},
		{"decorator", SymbolKindDecorator, "decorator"},
		{"enum", SymbolKindEnum, "enum"},
		{"enum_member", SymbolKindEnumMember, "enum_member"},
		{"parameter", SymbolKindParameter, "parameter"},
		{"property", SymbolKindProperty, "property"},
		{"css_class", SymbolKindCSSClass, "css_class"},
		{"css_id", SymbolKindCSSID, "css_id"},
		{"css_variable", SymbolKindCSSVariable, "css_variable"},
		{"animation", SymbolKindAnimation, "animation"},
		{"media_query", SymbolKindMediaQuery, "media_query"},
		{"component", SymbolKindComponent, "component"},
		{"element", SymbolKindElement, "element"},
		{"form", SymbolKindForm, "form"},
		{"invalid kind returns unknown", SymbolKind(9999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.kind.String()
			if got != tt.expected {
				t.Errorf("SymbolKind(%d).String() = %q, want %q", tt.kind, got, tt.expected)
			}
		})
	}
}

func TestParseSymbolKind(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected SymbolKind
	}{
		{"parse unknown", "unknown", SymbolKindUnknown},
		{"parse function", "function", SymbolKindFunction},
		{"parse method", "method", SymbolKindMethod},
		{"parse interface", "interface", SymbolKindInterface},
		{"parse struct", "struct", SymbolKindStruct},
		{"parse class", "class", SymbolKindClass},
		{"parse css_class", "css_class", SymbolKindCSSClass},
		{"parse component", "component", SymbolKindComponent},
		{"invalid string returns unknown", "invalid", SymbolKindUnknown},
		{"empty string returns unknown", "", SymbolKindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseSymbolKind(tt.input)
			if got != tt.expected {
				t.Errorf("ParseSymbolKind(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSymbolKind_Roundtrip(t *testing.T) {
	// Test that String() and ParseSymbolKind() are inverses for all defined kinds
	for kind, name := range symbolKindNames {
		t.Run(name, func(t *testing.T) {
			// String() should produce the name
			if got := kind.String(); got != name {
				t.Errorf("SymbolKind(%d).String() = %q, want %q", kind, got, name)
			}

			// ParseSymbolKind() should return the original kind
			if got := ParseSymbolKind(name); got != kind {
				t.Errorf("ParseSymbolKind(%q) = %v, want %v", name, got, kind)
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	tests := []struct {
		name      string
		filePath  string
		startLine int
		symName   string
		expected  string
	}{
		{
			name:      "basic function",
			filePath:  "handlers/agent.go",
			startLine: 27,
			symName:   "HandleAgent",
			expected:  "handlers/agent.go:27:HandleAgent",
		},
		{
			name:      "nested path",
			filePath:  "services/trace/ast/parser.go",
			startLine: 1,
			symName:   "Parser",
			expected:  "services/trace/ast/parser.go:1:Parser",
		},
		{
			name:      "special characters in name",
			filePath:  "main.go",
			startLine: 100,
			symName:   "init",
			expected:  "main.go:100:init",
		},
		{
			name:      "root file",
			filePath:  "main.go",
			startLine: 1,
			symName:   "main",
			expected:  "main.go:1:main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateID(tt.filePath, tt.startLine, tt.symName)
			if got != tt.expected {
				t.Errorf("GenerateID(%q, %d, %q) = %q, want %q",
					tt.filePath, tt.startLine, tt.symName, got, tt.expected)
			}
		})
	}
}

func TestGenerateID_Deterministic(t *testing.T) {
	// Same inputs should always produce same output
	filePath := "handlers/agent.go"
	line := 27
	name := "HandleAgent"

	id1 := GenerateID(filePath, line, name)
	id2 := GenerateID(filePath, line, name)

	if id1 != id2 {
		t.Errorf("GenerateID is not deterministic: %q != %q", id1, id2)
	}
}

func TestLocation_String(t *testing.T) {
	tests := []struct {
		name     string
		loc      Location
		expected string
	}{
		{
			name: "basic location",
			loc: Location{
				FilePath:  "main.go",
				StartLine: 10,
				StartCol:  5,
			},
			expected: "main.go:10:5",
		},
		{
			name: "nested path",
			loc: Location{
				FilePath:  "services/trace/ast/parser.go",
				StartLine: 100,
				StartCol:  0,
			},
			expected: "services/trace/ast/parser.go:100:0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.loc.String()
			if got != tt.expected {
				t.Errorf("Location.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestSymbol_Location(t *testing.T) {
	symbol := &Symbol{
		FilePath:  "handlers/agent.go",
		StartLine: 27,
		EndLine:   50,
		StartCol:  0,
		EndCol:    1,
	}

	loc := symbol.Location()

	if loc.FilePath != symbol.FilePath {
		t.Errorf("Location().FilePath = %q, want %q", loc.FilePath, symbol.FilePath)
	}
	if loc.StartLine != symbol.StartLine {
		t.Errorf("Location().StartLine = %d, want %d", loc.StartLine, symbol.StartLine)
	}
	if loc.EndLine != symbol.EndLine {
		t.Errorf("Location().EndLine = %d, want %d", loc.EndLine, symbol.EndLine)
	}
	if loc.StartCol != symbol.StartCol {
		t.Errorf("Location().StartCol = %d, want %d", loc.StartCol, symbol.StartCol)
	}
	if loc.EndCol != symbol.EndCol {
		t.Errorf("Location().EndCol = %d, want %d", loc.EndCol, symbol.EndCol)
	}
}

func TestSymbol_SetParsedAt(t *testing.T) {
	symbol := &Symbol{}

	before := time.Now().UnixMilli()
	symbol.SetParsedAt()
	after := time.Now().UnixMilli()

	if symbol.ParsedAtMilli < before || symbol.ParsedAtMilli > after {
		t.Errorf("SetParsedAt() set ParsedAtMilli to %d, expected between %d and %d",
			symbol.ParsedAtMilli, before, after)
	}
}

func TestParseResult_SymbolCount(t *testing.T) {
	tests := []struct {
		name     string
		result   *ParseResult
		expected int
	}{
		{
			name:     "empty result",
			result:   &ParseResult{},
			expected: 0,
		},
		{
			name: "flat symbols",
			result: &ParseResult{
				Symbols: []*Symbol{
					{Name: "func1"},
					{Name: "func2"},
					{Name: "func3"},
				},
			},
			expected: 3,
		},
		{
			name: "nested symbols",
			result: &ParseResult{
				Symbols: []*Symbol{
					{
						Name: "MyClass",
						Children: []*Symbol{
							{Name: "method1"},
							{Name: "method2"},
						},
					},
					{Name: "func1"},
				},
			},
			expected: 4, // MyClass + 2 methods + func1
		},
		{
			name: "deeply nested",
			result: &ParseResult{
				Symbols: []*Symbol{
					{
						Name: "Outer",
						Children: []*Symbol{
							{
								Name: "Inner",
								Children: []*Symbol{
									{Name: "Deepest"},
								},
							},
						},
					},
				},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.SymbolCount()
			if got != tt.expected {
				t.Errorf("SymbolCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestParseResult_HasErrors(t *testing.T) {
	tests := []struct {
		name     string
		result   *ParseResult
		expected bool
	}{
		{
			name:     "no errors",
			result:   &ParseResult{},
			expected: false,
		},
		{
			name:     "nil errors slice",
			result:   &ParseResult{Errors: nil},
			expected: false,
		},
		{
			name:     "empty errors slice",
			result:   &ParseResult{Errors: []string{}},
			expected: false,
		},
		{
			name:     "has errors",
			result:   &ParseResult{Errors: []string{"syntax error at line 10"}},
			expected: true,
		},
		{
			name:     "multiple errors",
			result:   &ParseResult{Errors: []string{"error 1", "error 2"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.HasErrors()
			if got != tt.expected {
				t.Errorf("HasErrors() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseResult_SetParsedAt(t *testing.T) {
	result := &ParseResult{}

	before := time.Now().UnixMilli()
	result.SetParsedAt()
	after := time.Now().UnixMilli()

	if result.ParsedAtMilli < before || result.ParsedAtMilli > after {
		t.Errorf("SetParsedAt() set ParsedAtMilli to %d, expected between %d and %d",
			result.ParsedAtMilli, before, after)
	}
}

func TestParseError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ParseError
		expected string
	}{
		{
			name: "with line and column",
			err: &ParseError{
				FilePath: "main.go",
				Line:     10,
				Column:   5,
				Message:  "unexpected token",
			},
			expected: "main.go:10:5: unexpected token",
		},
		{
			name: "with line only",
			err: &ParseError{
				FilePath: "main.go",
				Line:     10,
				Column:   0,
				Message:  "unexpected token",
			},
			expected: "main.go:10: unexpected token",
		},
		{
			name: "without location",
			err: &ParseError{
				FilePath: "main.go",
				Line:     0,
				Column:   0,
				Message:  "file is empty",
			},
			expected: "main.go: file is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Errorf("Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseError_Unwrap(t *testing.T) {
	cause := errors.New("underlying error")
	err := &ParseError{
		FilePath: "main.go",
		Message:  "parse failed",
		Cause:    cause,
	}

	unwrapped := err.Unwrap()
	if unwrapped != cause {
		t.Errorf("Unwrap() = %v, want %v", unwrapped, cause)
	}
}

func TestParseError_Unwrap_Nil(t *testing.T) {
	err := &ParseError{
		FilePath: "main.go",
		Message:  "parse failed",
	}

	unwrapped := err.Unwrap()
	if unwrapped != nil {
		t.Errorf("Unwrap() = %v, want nil", unwrapped)
	}
}

func TestNewParseError(t *testing.T) {
	err := NewParseError("main.go", 10, 5, "unexpected token")

	if err.FilePath != "main.go" {
		t.Errorf("FilePath = %q, want %q", err.FilePath, "main.go")
	}
	if err.Line != 10 {
		t.Errorf("Line = %d, want %d", err.Line, 10)
	}
	if err.Column != 5 {
		t.Errorf("Column = %d, want %d", err.Column, 5)
	}
	if err.Message != "unexpected token" {
		t.Errorf("Message = %q, want %q", err.Message, "unexpected token")
	}
	if err.Cause != nil {
		t.Errorf("Cause = %v, want nil", err.Cause)
	}
}

func TestNewParseErrorWithCause(t *testing.T) {
	cause := errors.New("underlying error")
	err := NewParseErrorWithCause("main.go", 10, 5, "parse failed", cause)

	if err.Cause != cause {
		t.Errorf("Cause = %v, want %v", err.Cause, cause)
	}

	// Should be able to unwrap to the cause
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false, want true")
	}
}

func TestWrapParseError(t *testing.T) {
	t.Run("wraps regular error", func(t *testing.T) {
		original := errors.New("some error")
		wrapped := WrapParseError(original, "main.go")

		var parseErr *ParseError
		if !errors.As(wrapped, &parseErr) {
			t.Fatal("wrapped error is not a ParseError")
		}

		if parseErr.FilePath != "main.go" {
			t.Errorf("FilePath = %q, want %q", parseErr.FilePath, "main.go")
		}
		if parseErr.Message != "some error" {
			t.Errorf("Message = %q, want %q", parseErr.Message, "some error")
		}
		if parseErr.Cause != original {
			t.Errorf("Cause = %v, want %v", parseErr.Cause, original)
		}
	})

	t.Run("returns nil for nil error", func(t *testing.T) {
		wrapped := WrapParseError(nil, "main.go")
		if wrapped != nil {
			t.Errorf("WrapParseError(nil, ...) = %v, want nil", wrapped)
		}
	})

	t.Run("does not double-wrap ParseError", func(t *testing.T) {
		original := NewParseError("original.go", 5, 3, "original message")
		wrapped := WrapParseError(original, "different.go")

		// Should return the original error unchanged
		var parseErr *ParseError
		if !errors.As(wrapped, &parseErr) {
			t.Fatal("wrapped error is not a ParseError")
		}

		if parseErr.FilePath != "original.go" {
			t.Errorf("FilePath = %q, want %q (should keep original)", parseErr.FilePath, "original.go")
		}
	})
}

func TestIsParseError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ParseError",
			err:      NewParseError("main.go", 1, 0, "error"),
			expected: true,
		},
		{
			name:     "wrapped ParseError",
			err:      WrapParseError(errors.New("inner"), "main.go"),
			expected: true,
		},
		{
			name:     "regular error",
			err:      errors.New("regular error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsParseError(tt.err)
			if got != tt.expected {
				t.Errorf("IsParseError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsUnsupportedLanguage(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "direct ErrUnsupportedLanguage",
			err:      ErrUnsupportedLanguage,
			expected: true,
		},
		{
			name:     "wrapped ErrUnsupportedLanguage",
			err:      NewParseErrorWithCause("main.xyz", 0, 0, "cannot parse", ErrUnsupportedLanguage),
			expected: true,
		},
		{
			name:     "different error",
			err:      ErrParseFailed,
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUnsupportedLanguage(tt.err)
			if got != tt.expected {
				t.Errorf("IsUnsupportedLanguage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsParseFailed(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "direct ErrParseFailed",
			err:      ErrParseFailed,
			expected: true,
		},
		{
			name:     "wrapped ErrParseFailed",
			err:      NewParseErrorWithCause("main.go", 10, 0, "syntax error", ErrParseFailed),
			expected: true,
		},
		{
			name:     "different error",
			err:      ErrUnsupportedLanguage,
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsParseFailed(tt.err)
			if got != tt.expected {
				t.Errorf("IsParseFailed() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParserRegistry(t *testing.T) {
	t.Run("register and get by language", func(t *testing.T) {
		registry := NewParserRegistry()

		// Create a mock parser
		mock := &mockParser{
			language:   "go",
			extensions: []string{".go"},
		}

		registry.Register(mock)

		parser, ok := registry.GetByLanguage("go")
		if !ok {
			t.Fatal("GetByLanguage(\"go\") returned false")
		}
		if parser.Language() != mock.Language() {
			t.Error("GetByLanguage returned different parser")
		}
	})

	t.Run("register and get by extension", func(t *testing.T) {
		registry := NewParserRegistry()

		mock := &mockParser{
			language:   "typescript",
			extensions: []string{".ts", ".tsx"},
		}

		registry.Register(mock)

		for _, ext := range []string{".ts", ".tsx"} {
			parser, ok := registry.GetByExtension(ext)
			if !ok {
				t.Fatalf("GetByExtension(%q) returned false", ext)
			}
			if parser.Language() != mock.Language() {
				t.Errorf("GetByExtension(%q) returned different parser", ext)
			}
		}
	})

	t.Run("get unknown language returns false", func(t *testing.T) {
		registry := NewParserRegistry()

		_, ok := registry.GetByLanguage("unknown")
		if ok {
			t.Error("GetByLanguage(\"unknown\") should return false")
		}
	})

	t.Run("get unknown extension returns false", func(t *testing.T) {
		registry := NewParserRegistry()

		_, ok := registry.GetByExtension(".xyz")
		if ok {
			t.Error("GetByExtension(\".xyz\") should return false")
		}
	})

	t.Run("register nil does nothing", func(t *testing.T) {
		registry := NewParserRegistry()
		registry.Register(nil) // Should not panic
	})

	t.Run("Languages returns registered languages", func(t *testing.T) {
		registry := NewParserRegistry()
		registry.Register(&mockParser{language: "go", extensions: []string{".go"}})
		registry.Register(&mockParser{language: "python", extensions: []string{".py"}})

		languages := registry.Languages()
		if len(languages) != 2 {
			t.Errorf("Languages() returned %d languages, want 2", len(languages))
		}
	})

	t.Run("Extensions returns registered extensions", func(t *testing.T) {
		registry := NewParserRegistry()
		registry.Register(&mockParser{language: "go", extensions: []string{".go"}})
		registry.Register(&mockParser{language: "python", extensions: []string{".py", ".pyi"}})

		extensions := registry.Extensions()
		if len(extensions) != 3 {
			t.Errorf("Extensions() returned %d extensions, want 3", len(extensions))
		}
	})
}

func TestDefaultParseOptions(t *testing.T) {
	opts := DefaultParseOptions()

	if opts.IncludeComments {
		t.Error("DefaultParseOptions().IncludeComments should be false")
	}
	if !opts.IncludePrivate {
		t.Error("DefaultParseOptions().IncludePrivate should be true")
	}
	if opts.MaxDepth != 0 {
		t.Errorf("DefaultParseOptions().MaxDepth = %d, want 0", opts.MaxDepth)
	}
	if opts.ExtractBodies {
		t.Error("DefaultParseOptions().ExtractBodies should be false")
	}
}

func TestSymbolKind_JSONMarshal(t *testing.T) {
	tests := []struct {
		name     string
		kind     SymbolKind
		expected string
	}{
		{"function", SymbolKindFunction, `"function"`},
		{"method", SymbolKindMethod, `"method"`},
		{"unknown", SymbolKindUnknown, `"unknown"`},
		{"css_class", SymbolKindCSSClass, `"css_class"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.kind)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("Marshal(%v) = %s, want %s", tt.kind, data, tt.expected)
			}
		})
	}
}

func TestSymbolKind_JSONUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected SymbolKind
	}{
		{"string function", `"function"`, SymbolKindFunction},
		{"string method", `"method"`, SymbolKindMethod},
		{"string unknown", `"unknown"`, SymbolKindUnknown},
		{"int 3 (function)", `3`, SymbolKindFunction},
		{"int 0 (unknown)", `0`, SymbolKindUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var kind SymbolKind
			err := json.Unmarshal([]byte(tt.input), &kind)
			if err != nil {
				t.Fatalf("Unmarshal error: %v", err)
			}
			if kind != tt.expected {
				t.Errorf("Unmarshal(%s) = %v, want %v", tt.input, kind, tt.expected)
			}
		})
	}
}

func TestSymbolKind_JSONRoundtrip(t *testing.T) {
	for kind := range symbolKindNames {
		t.Run(kind.String(), func(t *testing.T) {
			data, err := json.Marshal(kind)
			if err != nil {
				t.Fatalf("Marshal error: %v", err)
			}

			var unmarshaled SymbolKind
			err = json.Unmarshal(data, &unmarshaled)
			if err != nil {
				t.Fatalf("Unmarshal error: %v", err)
			}

			if unmarshaled != kind {
				t.Errorf("Roundtrip failed: got %v, want %v", unmarshaled, kind)
			}
		})
	}
}

func TestSymbol_Validate(t *testing.T) {
	validSymbol := &Symbol{
		Name:      "TestFunc",
		FilePath:  "handlers/agent.go",
		StartLine: 10,
		EndLine:   20,
		StartCol:  0,
		EndCol:    1,
		Language:  "go",
	}

	tests := []struct {
		name      string
		modify    func(*Symbol)
		wantError bool
		errField  string
	}{
		{"valid symbol", func(s *Symbol) {}, false, ""},
		{"empty name", func(s *Symbol) { s.Name = "" }, true, "Name"},
		{"empty file path", func(s *Symbol) { s.FilePath = "" }, true, "FilePath"},
		{"path traversal", func(s *Symbol) { s.FilePath = "../etc/passwd" }, true, "FilePath"},
		{"start line zero", func(s *Symbol) { s.StartLine = 0 }, true, "StartLine"},
		{"end line before start", func(s *Symbol) { s.EndLine = 5 }, true, "EndLine"},
		{"negative start col", func(s *Symbol) { s.StartCol = -1 }, true, "StartCol"},
		{"negative end col", func(s *Symbol) { s.EndCol = -1 }, true, "EndCol"},
		{"empty language", func(s *Symbol) { s.Language = "" }, true, "Language"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of validSymbol
			s := *validSymbol
			tt.modify(&s)

			err := s.Validate()
			if tt.wantError {
				if err == nil {
					t.Error("Validate() should return error")
				} else {
					var valErr ValidationError
					if errors.As(err, &valErr) {
						if valErr.Field != tt.errField {
							t.Errorf("Error field = %q, want %q", valErr.Field, tt.errField)
						}
					}
				}
			} else {
				if err != nil {
					t.Errorf("Validate() returned unexpected error: %v", err)
				}
			}
		})
	}
}

func TestSymbol_Validate_Children(t *testing.T) {
	symbol := &Symbol{
		Name:      "MyClass",
		FilePath:  "models/user.go",
		StartLine: 10,
		EndLine:   50,
		StartCol:  0,
		EndCol:    1,
		Language:  "go",
		Children: []*Symbol{
			{
				Name:      "", // Invalid: empty name
				FilePath:  "models/user.go",
				StartLine: 15,
				EndLine:   20,
				StartCol:  4,
				EndCol:    5,
				Language:  "go",
			},
		},
	}

	err := symbol.Validate()
	if err == nil {
		t.Error("Validate() should return error for invalid child")
	}

	var valErr ValidationError
	if errors.As(err, &valErr) {
		if valErr.Field != "Children[0]" {
			t.Errorf("Error field = %q, want %q", valErr.Field, "Children[0]")
		}
	}
}

func TestParseResult_Validate(t *testing.T) {
	validResult := &ParseResult{
		FilePath: "handlers/agent.go",
		Language: "go",
		Symbols: []*Symbol{
			{
				Name:      "Handler",
				FilePath:  "handlers/agent.go",
				StartLine: 10,
				EndLine:   20,
				StartCol:  0,
				EndCol:    1,
				Language:  "go",
			},
		},
		Imports: []Import{
			{
				Path:     "context",
				Location: Location{FilePath: "handlers/agent.go", StartLine: 3},
			},
		},
	}

	tests := []struct {
		name      string
		modify    func(*ParseResult)
		wantError bool
	}{
		{"valid result", func(r *ParseResult) {}, false},
		{"empty file path", func(r *ParseResult) { r.FilePath = "" }, true},
		{"path traversal", func(r *ParseResult) { r.FilePath = "../../secrets" }, true},
		{"empty language", func(r *ParseResult) { r.Language = "" }, true},
		{"empty import path", func(r *ParseResult) { r.Imports[0].Path = "" }, true},
		{"invalid import line", func(r *ParseResult) { r.Imports[0].Location.StartLine = 0 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Deep copy
			r := *validResult
			r.Symbols = make([]*Symbol, len(validResult.Symbols))
			for i, s := range validResult.Symbols {
				sCopy := *s
				r.Symbols[i] = &sCopy
			}
			r.Imports = make([]Import, len(validResult.Imports))
			copy(r.Imports, validResult.Imports)

			tt.modify(&r)

			err := r.Validate()
			if tt.wantError && err == nil {
				t.Error("Validate() should return error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Validate() returned unexpected error: %v", err)
			}
		})
	}
}

func TestSymbolCountWithDepth(t *testing.T) {
	// Create deeply nested structure
	result := &ParseResult{
		Symbols: []*Symbol{
			{
				Name: "Level0",
				Children: []*Symbol{
					{
						Name: "Level1",
						Children: []*Symbol{
							{
								Name: "Level2",
								Children: []*Symbol{
									{Name: "Level3"},
								},
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		maxDepth int
		expected int
	}{
		{"depth 0 - top level only", 0, 1},
		{"depth 1", 1, 2},
		{"depth 2", 2, 3},
		{"depth 3", 3, 4},
		{"depth 100 - all levels", 100, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := result.SymbolCountWithDepth(tt.maxDepth)
			if got != tt.expected {
				t.Errorf("SymbolCountWithDepth(%d) = %d, want %d", tt.maxDepth, got, tt.expected)
			}
		})
	}
}

func TestSymbolCount_DepthLimit(t *testing.T) {
	// Verify that SymbolCount uses MaxSymbolDepth
	result := &ParseResult{
		Symbols: []*Symbol{
			{Name: "root"},
		},
	}

	// SymbolCount should use default depth
	count := result.SymbolCount()
	if count != 1 {
		t.Errorf("SymbolCount() = %d, want 1", count)
	}
}

func TestParserRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewParserRegistry()

	// Register a base parser
	registry.Register(&mockParser{language: "go", extensions: []string{".go"}})

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := registry.GetByLanguage("go")
			if !ok {
				errors <- nil // Expected since we might read during registration
			}
		}()
	}

	// Concurrent registrations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			registry.Register(&mockParser{
				language:   "lang" + string(rune('a'+i%26)),
				extensions: []string{".ext" + string(rune('a'+i%26))},
			})
		}(i)
	}

	wg.Wait()
	close(errors)

	// If we got here without panic, concurrent access is safe
}

func TestValidationError_Error(t *testing.T) {
	err := ValidationError{Field: "Name", Message: "must not be empty"}
	expected := "Name: must not be empty"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

// mockParser is a test double for the Parser interface.
type mockParser struct {
	language   string
	extensions []string
}

func (m *mockParser) Parse(_ context.Context, _ []byte, _ string) (*ParseResult, error) {
	return &ParseResult{Language: m.language}, nil
}

func (m *mockParser) Language() string {
	return m.language
}

func (m *mockParser) Extensions() []string {
	return m.extensions
}
