// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package seeder provides library documentation seeding for Trace.
//
// The seeder extracts documentation from project dependencies and indexes
// them into Weaviate for context assembly. It supports Go modules initially,
// with extensibility for Python and TypeScript.
package seeder

import (
	"errors"
	"path/filepath"
	"strings"
)

// Sentinel errors for the seeder package.
var (
	// ErrNoGoMod indicates no go.mod file was found.
	ErrNoGoMod = errors.New("go.mod not found")

	// ErrModuleNotCached indicates the module is not in the local cache.
	ErrModuleNotCached = errors.New("module not in local cache")

	// ErrUnsupportedLanguage indicates an unsupported language.
	ErrUnsupportedLanguage = errors.New("unsupported language")

	// ErrEmptyProjectRoot indicates projectRoot is empty.
	ErrEmptyProjectRoot = errors.New("projectRoot must not be empty")

	// ErrRelativeProjectRoot indicates projectRoot is not absolute.
	ErrRelativeProjectRoot = errors.New("projectRoot must be absolute path")

	// ErrPathTraversal indicates projectRoot contains path traversal.
	ErrPathTraversal = errors.New("projectRoot must not contain path traversal")

	// ErrNilClient indicates the Weaviate client is nil.
	ErrNilClient = errors.New("weaviate client must not be nil")
)

// ValidateProjectRoot validates that a project root path is safe to use.
//
// Description:
//
//	Validates the project root path for security. Rejects empty paths,
//	relative paths, and paths containing traversal sequences.
//
// Inputs:
//
//	projectRoot - Path to validate
//
// Outputs:
//
//	error - Non-nil if validation fails
func ValidateProjectRoot(projectRoot string) error {
	if projectRoot == "" {
		return ErrEmptyProjectRoot
	}
	if !filepath.IsAbs(projectRoot) {
		return ErrRelativeProjectRoot
	}
	if strings.Contains(projectRoot, "..") {
		return ErrPathTraversal
	}
	return nil
}

// Dependency represents a project dependency.
type Dependency struct {
	// ModulePath is the module path (e.g., "github.com/gin-gonic/gin").
	ModulePath string `json:"module_path"`

	// Version is the module version (e.g., "v1.9.1").
	Version string `json:"version"`

	// Language is the dependency language ("go", "python", "typescript").
	Language string `json:"language"`

	// LocalPath is the resolved local cache path.
	LocalPath string `json:"local_path"`

	// Indirect indicates if this is an indirect dependency.
	Indirect bool `json:"indirect"`
}

// LibraryDoc represents documentation for a library symbol.
type LibraryDoc struct {
	// DocID is a unique identifier for this doc entry.
	// Format: "{library}@{version}:{symbol_path}"
	DocID string `json:"doc_id"`

	// Library is the library/module path (e.g., "github.com/gin-gonic/gin").
	Library string `json:"library"`

	// Version is the library version (e.g., "v1.9.1").
	Version string `json:"version"`

	// SymbolPath is the fully qualified symbol path.
	// Format: "package.SymbolName" (e.g., "gin.Context" or "gin.Context.JSON")
	SymbolPath string `json:"symbol_path"`

	// SymbolKind is the type of symbol ("function", "method", "type", "constant", "variable").
	SymbolKind string `json:"symbol_kind"`

	// Signature is the type signature (e.g., "func(code int, obj interface{})").
	Signature string `json:"signature"`

	// DocContent is the documentation text.
	DocContent string `json:"doc_content"`

	// Example is an optional code example.
	Example string `json:"example,omitempty"`

	// DataSpace is the Weaviate data space for isolation.
	DataSpace string `json:"data_space"`
}

// SeedResult contains the result of a seeding operation.
type SeedResult struct {
	// DependenciesFound is the number of dependencies discovered.
	DependenciesFound int `json:"dependencies_found"`

	// DocsIndexed is the number of documentation entries indexed.
	DocsIndexed int `json:"docs_indexed"`

	// Errors contains non-fatal errors encountered during seeding.
	Errors []string `json:"errors,omitempty"`
}

// GenerateDocID creates a unique ID for a library doc.
func GenerateDocID(library, version, symbolPath string) string {
	return library + "@" + version + ":" + symbolPath
}
