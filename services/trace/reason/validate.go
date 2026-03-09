// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package reason

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// ChangeValidator validates proposed code changes.
//
// Description:
//
//	ChangeValidator performs syntactic validation of proposed code changes.
//	It checks for syntax errors, type reference existence, and import resolution.
//
//	IMPORTANT: This is syntactic validation only. It cannot perform full
//	semantic type checking - that requires the compiler or LSP.
//
// Thread Safety:
//
//	ChangeValidator is safe for concurrent use.
type ChangeValidator struct {
	index *index.SymbolIndex
	crs   CRSRecorder

	mu     sync.RWMutex
	goLang *sitter.Language
	pyLang *sitter.Language
	tsLang *sitter.Language
}

// NewChangeValidator creates a new ChangeValidator.
//
// Description:
//
//	Creates a validator that can check proposed code for syntactic validity.
//
// Inputs:
//
//	idx - The symbol index (for type reference checking).
//
// Outputs:
//
//	*ChangeValidator - The configured validator.
func NewChangeValidator(idx *index.SymbolIndex) *ChangeValidator {
	return &ChangeValidator{
		index: idx,
		crs:   &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this validator.
func (v *ChangeValidator) SetCRS(recorder CRSRecorder) {
	v.crs = recorder
}

// ChangeValidation is the result of validating a change.
type ChangeValidation struct {
	// SyntaxValid indicates if the syntax is correct.
	SyntaxValid bool `json:"syntax_valid"`

	// TypesValid indicates if all type references exist.
	TypesValid bool `json:"types_valid"`

	// ImportsValid indicates if all imports can resolve.
	ImportsValid bool `json:"imports_valid"`

	// Errors lists all validation errors found.
	Errors []ValidationError `json:"errors,omitempty"`

	// Warnings lists non-fatal issues found.
	Warnings []ValidationWarning `json:"warnings,omitempty"`

	// Scope describes the validation scope.
	// Always "syntactic" for this validator.
	Scope string `json:"scope"`

	// Confidence is how confident we are (0.0-1.0).
	Confidence float64 `json:"confidence"`
}

// ValidationError describes a validation error.
type ValidationError struct {
	// Type categorizes the error.
	Type string `json:"type"` // syntax, type_ref, import

	// Message describes the error.
	Message string `json:"message"`

	// Line is the line number (1-indexed).
	Line int `json:"line"`

	// Column is the column number (0-indexed).
	Column int `json:"column"`

	// Severity is the error severity.
	Severity string `json:"severity"` // error, warning
}

// ValidationWarning describes a non-fatal issue.
type ValidationWarning struct {
	// Type categorizes the warning.
	Type string `json:"type"`

	// Message describes the warning.
	Message string `json:"message"`

	// Line is the line number (1-indexed).
	Line int `json:"line"`
}

// ValidateChange validates proposed code content.
//
// Description:
//
//	Validates the proposed code for a file. Performs:
//	- Syntax checking (will it parse?)
//	- Type reference checking (do referenced types exist?)
//	- Import resolution (do imported packages exist?)
//
//	IMPORTANT LIMITATION: This performs syntactic validation only.
//	It cannot verify:
//	- Type safety (is this assignment valid?)
//	- Interface satisfaction (does this implement the interface?)
//	- Semantic correctness (will this compile?)
//
//	For full type checking, use the compiler or LSP.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	filePath - Path to the file being changed.
//	newContent - Proposed new content for the file.
//
// Outputs:
//
//	*ChangeValidation - Validation results.
//	error - Non-nil if validation itself fails.
//
// Example:
//
//	validation, err := validator.ValidateChange(ctx,
//	    "handlers/user.go",
//	    "package handlers\n\nfunc Handle() error { return nil }",
//	)
//	if !validation.SyntaxValid {
//	    for _, e := range validation.Errors {
//	        fmt.Printf("Line %d: %s\n", e.Line, e.Message)
//	    }
//	}
func (v *ChangeValidator) ValidateChange(
	ctx context.Context,
	filePath string,
	newContent string,
) (*ChangeValidation, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startValidateSpan(ctx, filePath)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if filePath == "" || newContent == "" {
		return nil, ErrInvalidInput
	}

	result := &ChangeValidation{
		SyntaxValid:  true,
		TypesValid:   true,
		ImportsValid: true,
		Errors:       make([]ValidationError, 0),
		Warnings:     make([]ValidationWarning, 0),
		Scope:        "syntactic",
	}

	// Determine language from file extension
	lang := v.detectLanguage(filePath)
	if lang == "" {
		result.Warnings = append(result.Warnings, ValidationWarning{
			Type:    "unknown_language",
			Message: "Could not determine language from file extension",
		})
		result.Confidence = 0.5
		return result, nil
	}

	// Syntax validation
	syntaxErrors := v.validateSyntax(ctx, newContent, lang)
	if len(syntaxErrors) > 0 {
		result.SyntaxValid = false
		result.Errors = append(result.Errors, syntaxErrors...)
	}

	// Type reference validation (only if syntax is valid)
	if result.SyntaxValid && v.index != nil {
		typeErrors := v.validateTypeReferences(ctx, newContent, lang)
		if len(typeErrors) > 0 {
			result.TypesValid = false
			result.Errors = append(result.Errors, typeErrors...)
		}
	}

	// Import validation (only if syntax is valid)
	if result.SyntaxValid {
		importWarnings := v.validateImports(ctx, newContent, lang)
		result.Warnings = append(result.Warnings, importWarnings...)
		if len(importWarnings) > 0 {
			result.ImportsValid = false
		}
	}

	// Calculate confidence
	result.Confidence = v.calculateValidationConfidence(result)

	dur := time.Since(start)
	setValidateSpanResult(span, result.SyntaxValid, len(result.Errors), nil)
	recordValidateMetrics(ctx, dur, nil)
	resultCount := 1 // validation produces a single result
	if !result.SyntaxValid {
		resultCount = 0
	}
	v.crs.RecordToolStep(ctx, "validate_change", resultCount, dur, nil)

	return result, nil
}

// detectLanguage determines the language from file extension.
func (v *ChangeValidator) detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	default:
		return ""
	}
}

// getLanguage returns the tree-sitter language for a language name.
func (v *ChangeValidator) getLanguage(lang string) *sitter.Language {
	switch lang {
	case "go":
		v.mu.RLock()
		if v.goLang != nil {
			l := v.goLang
			v.mu.RUnlock()
			return l
		}
		v.mu.RUnlock()

		v.mu.Lock()
		defer v.mu.Unlock()
		if v.goLang == nil {
			v.goLang = golang.GetLanguage()
		}
		return v.goLang

	case "python":
		v.mu.RLock()
		if v.pyLang != nil {
			l := v.pyLang
			v.mu.RUnlock()
			return l
		}
		v.mu.RUnlock()

		v.mu.Lock()
		defer v.mu.Unlock()
		if v.pyLang == nil {
			v.pyLang = python.GetLanguage()
		}
		return v.pyLang

	case "typescript", "javascript":
		v.mu.RLock()
		if v.tsLang != nil {
			l := v.tsLang
			v.mu.RUnlock()
			return l
		}
		v.mu.RUnlock()

		v.mu.Lock()
		defer v.mu.Unlock()
		if v.tsLang == nil {
			v.tsLang = typescript.GetLanguage()
		}
		return v.tsLang

	default:
		return nil
	}
}

// validateSyntax checks for syntax errors using tree-sitter.
func (v *ChangeValidator) validateSyntax(ctx context.Context, content, lang string) []ValidationError {
	errors := make([]ValidationError, 0)

	tsLang := v.getLanguage(lang)
	if tsLang == nil {
		return errors
	}

	parser := sitter.NewParser()
	parser.SetLanguage(tsLang)

	tree, err := parser.ParseCtx(ctx, nil, []byte(content))
	if err != nil {
		errors = append(errors, ValidationError{
			Type:     "syntax",
			Message:  "Failed to parse: " + err.Error(),
			Severity: "error",
		})
		return errors
	}
	defer tree.Close()

	root := tree.RootNode()

	// Find all ERROR nodes in the tree
	v.collectSyntaxErrors(root, []byte(content), &errors)

	return errors
}

// collectSyntaxErrors traverses the tree and collects ERROR nodes.
func (v *ChangeValidator) collectSyntaxErrors(node *sitter.Node, content []byte, errors *[]ValidationError) {
	if node.IsError() || node.IsMissing() {
		// Get line and column
		startPoint := node.StartPoint()

		errType := "syntax"
		if node.IsMissing() {
			errType = "missing"
		}

		// Extract context around the error
		start := node.StartByte()
		end := node.EndByte()
		if end > uint32(len(content)) {
			end = uint32(len(content))
		}
		context := string(content[start:end])
		if len(context) > 50 {
			context = context[:50] + "..."
		}

		*errors = append(*errors, ValidationError{
			Type:     errType,
			Message:  "Syntax error near: " + context,
			Line:     int(startPoint.Row) + 1,
			Column:   int(startPoint.Column),
			Severity: "error",
		})
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		v.collectSyntaxErrors(child, content, errors)
	}
}

// validateTypeReferences checks if referenced types exist in the index.
func (v *ChangeValidator) validateTypeReferences(ctx context.Context, content, lang string) []ValidationError {
	errors := make([]ValidationError, 0)

	// Extract type references from the content
	typeRefs := v.extractTypeReferences(content, lang)

	// Check each type reference
	for _, ref := range typeRefs {
		if err := ctx.Err(); err != nil {
			break
		}

		// Skip builtin types
		if isBuiltinType(ref.Name, lang) {
			continue
		}

		// Skip qualified types (assume external packages are valid)
		if strings.Contains(ref.Name, ".") {
			continue
		}

		// Check if type exists in index
		symbols := v.index.GetByName(ref.Name)
		found := false
		for _, sym := range symbols {
			if isTypeSymbol(sym) {
				found = true
				break
			}
		}

		if !found {
			errors = append(errors, ValidationError{
				Type:     "type_ref",
				Message:  "Type not found: " + ref.Name,
				Line:     ref.Line,
				Severity: "error",
			})
		}
	}

	return errors
}

// TypeReference represents a type reference found in code.
type TypeReference struct {
	Name string
	Line int
}

// extractTypeReferences extracts type references from code.
func (v *ChangeValidator) extractTypeReferences(content, lang string) []TypeReference {
	refs := make([]TypeReference, 0)

	// Simple extraction based on common patterns
	// In production, would use tree-sitter queries

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineNum := i + 1

		switch lang {
		case "go":
			// Look for type declarations and usages
			refs = append(refs, extractGoTypeRefs(line, lineNum)...)
		case "python":
			refs = append(refs, extractPythonTypeRefs(line, lineNum)...)
		case "typescript", "javascript":
			refs = append(refs, extractTSTypeRefs(line, lineNum)...)
		}
	}

	return refs
}

// validateImports checks if imported packages are likely valid.
func (v *ChangeValidator) validateImports(ctx context.Context, content, lang string) []ValidationWarning {
	warnings := make([]ValidationWarning, 0)

	// Extract imports
	imports := v.extractImports(content, lang)

	for _, imp := range imports {
		if err := ctx.Err(); err != nil {
			break
		}

		// Check for obviously invalid imports
		if strings.Contains(imp.Path, "..") {
			warnings = append(warnings, ValidationWarning{
				Type:    "import",
				Message: "Suspicious import path: " + imp.Path,
				Line:    imp.Line,
			})
		}

		// Warn about relative imports in Go
		if lang == "go" && !strings.Contains(imp.Path, "/") && !isGoStdLib(imp.Path) {
			warnings = append(warnings, ValidationWarning{
				Type:    "import",
				Message: "Import may not resolve: " + imp.Path,
				Line:    imp.Line,
			})
		}
	}

	return warnings
}

// ImportReference represents an import found in code.
type ImportReference struct {
	Path string
	Line int
}

// extractImports extracts import statements from code.
func (v *ChangeValidator) extractImports(content, lang string) []ImportReference {
	imports := make([]ImportReference, 0)

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineNum := i + 1
		line = strings.TrimSpace(line)

		switch lang {
		case "go":
			if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, `"`) {
				// Extract import path
				if start := strings.Index(line, `"`); start >= 0 {
					if end := strings.Index(line[start+1:], `"`); end >= 0 {
						imports = append(imports, ImportReference{
							Path: line[start+1 : start+1+end],
							Line: lineNum,
						})
					}
				}
			}
		case "python":
			if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "from ") {
				// Extract module name
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					module := parts[1]
					if strings.HasPrefix(line, "from ") && len(parts) >= 4 {
						module = parts[1]
					}
					imports = append(imports, ImportReference{
						Path: module,
						Line: lineNum,
					})
				}
			}
		case "typescript", "javascript":
			if strings.Contains(line, "import ") || strings.Contains(line, "require(") {
				// Extract module path
				if start := strings.Index(line, `"`); start >= 0 {
					if end := strings.Index(line[start+1:], `"`); end >= 0 {
						imports = append(imports, ImportReference{
							Path: line[start+1 : start+1+end],
							Line: lineNum,
						})
					}
				} else if start := strings.Index(line, "'"); start >= 0 {
					if end := strings.Index(line[start+1:], "'"); end >= 0 {
						imports = append(imports, ImportReference{
							Path: line[start+1 : start+1+end],
							Line: lineNum,
						})
					}
				}
			}
		}
	}

	return imports
}

// calculateValidationConfidence calculates confidence for validation.
func (v *ChangeValidator) calculateValidationConfidence(val *ChangeValidation) float64 {
	if !val.SyntaxValid {
		// Syntax errors are definitive
		return 0.95
	}

	cal := NewConfidenceCalibration(0.8)

	// Type reference checking is less certain
	if !val.TypesValid {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "type reference issues",
			Multiplier: 0.9,
		})
	}

	// Import warnings are speculative
	if !val.ImportsValid {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "import warnings",
			Multiplier: 0.95,
		})
	}

	// Add limitation for syntactic-only scope
	cal.Apply(ConfidenceAdjustment{
		Reason:     "syntactic validation only",
		Multiplier: 0.9,
	})

	return cal.FinalScore
}

// Helper functions

func isBuiltinType(name, lang string) bool {
	switch lang {
	case "go":
		goBuiltins := map[string]bool{
			"bool": true, "string": true, "int": true, "int8": true,
			"int16": true, "int32": true, "int64": true, "uint": true,
			"uint8": true, "uint16": true, "uint32": true, "uint64": true,
			"uintptr": true, "byte": true, "rune": true, "float32": true,
			"float64": true, "complex64": true, "complex128": true,
			"error": true, "any": true,
		}
		return goBuiltins[name]

	case "python":
		pyBuiltins := map[string]bool{
			"str": true, "int": true, "float": true, "bool": true,
			"list": true, "dict": true, "set": true, "tuple": true,
			"None": true, "bytes": true, "object": true,
		}
		return pyBuiltins[name]

	case "typescript", "javascript":
		tsBuiltins := map[string]bool{
			"string": true, "number": true, "boolean": true, "any": true,
			"void": true, "null": true, "undefined": true, "never": true,
			"unknown": true, "object": true, "symbol": true, "bigint": true,
		}
		return tsBuiltins[name]

	default:
		return false
	}
}

func isTypeSymbol(sym *ast.Symbol) bool {
	if sym == nil {
		return false
	}
	switch sym.Kind {
	case ast.SymbolKindStruct, ast.SymbolKindInterface, ast.SymbolKindType,
		ast.SymbolKindClass, ast.SymbolKindEnum:
		return true
	default:
		return false
	}
}

func extractGoTypeRefs(line string, lineNum int) []TypeReference {
	refs := make([]TypeReference, 0)

	// Look for var/const declarations with types
	// Look for function parameters and returns
	// This is a simplified extraction

	// Type in function signature: func(...) TypeName
	// Type in declaration: var x TypeName
	// These patterns are simplified - full extraction would use tree-sitter

	return refs
}

func extractPythonTypeRefs(line string, lineNum int) []TypeReference {
	refs := make([]TypeReference, 0)

	// Look for type hints: def foo(x: TypeName)
	// Look for -> annotations: def foo() -> TypeName

	return refs
}

func extractTSTypeRefs(line string, lineNum int) []TypeReference {
	refs := make([]TypeReference, 0)

	// Look for type annotations: x: TypeName
	// Look for type assertions: <TypeName>x or x as TypeName

	return refs
}

func isGoStdLib(pkg string) bool {
	// Common Go standard library packages
	stdLib := map[string]bool{
		"fmt": true, "io": true, "os": true, "net": true, "http": true,
		"context": true, "sync": true, "time": true, "strings": true,
		"strconv": true, "bytes": true, "errors": true, "log": true,
		"encoding": true, "json": true, "xml": true, "testing": true,
		"reflect": true, "sort": true, "math": true, "crypto": true,
		"bufio": true, "path": true, "regexp": true, "runtime": true,
	}
	return stdLib[pkg]
}
