// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// DeadCodeExclusions configures what to exclude from dead code detection.
//
// # Description
//
// Dead code detection is inherently imprecise due to reflection, build tags,
// and external API contracts. These exclusions help reduce false positives.
type DeadCodeExclusions struct {
	// EntryPoints lists function patterns that are always excluded.
	// Supports wildcards: "main", "init", "Test*", "Benchmark*", "Example*"
	EntryPoints []string

	// ExportedSymbols excludes exported symbols (may be external API).
	ExportedSymbols bool

	// InterfaceImpls excludes methods that implement interfaces.
	InterfaceImpls bool

	// ReflectionPatterns lists patterns suggesting reflection usage.
	ReflectionPatterns []string

	// BuildTaggedFiles excludes files with build tags.
	BuildTaggedFiles bool

	// AnnotationPatterns lists comment annotations that exclude symbols.
	// Examples: "@used-by", "@implements", "nolint:deadcode"
	AnnotationPatterns []string
}

// DefaultExclusions returns conservative default exclusions.
//
// IT-08 H-1: Extended from Go-only to cross-language entry point patterns.
func DefaultExclusions() *DeadCodeExclusions {
	return &DeadCodeExclusions{
		EntryPoints: []string{
			// Go entry points
			"main",
			"init",
			"Test*",
			"Benchmark*",
			"Example*",
			"Fuzz*",
			// Python entry points (pytest, unittest)
			"test_*",
			"__main__",
			"setUp",
			"tearDown",
			"setUpClass",
			"tearDownClass",
			// JS/TS test framework entry points (Jest, Mocha, Vitest)
			"it",
			"test",
			"describe",
			"beforeEach",
			"afterEach",
			"beforeAll",
			"afterAll",
			// Next.js special exports
			"getServerSideProps",
			"getStaticProps",
			"getStaticPaths",
		},
		ExportedSymbols: true,
		InterfaceImpls:  true,
		ReflectionPatterns: []string{
			"reflect.ValueOf",
			"reflect.TypeOf",
			"json.Marshal",
			"json.Unmarshal",
			"gorm:",
			"db:",
			"xml:",
			"yaml:",
			"mapstructure:",
			"json:",
		},
		BuildTaggedFiles: true,
		AnnotationPatterns: []string{
			"@used-by",
			"@implements",
			"@entry-point",
			"nolint:deadcode",
			"nolint:unused",
			"//go:generate",
		},
	}
}

// DeadCodeOptions configures dead code detection.
type DeadCodeOptions struct {
	// Exclusions configures what to exclude.
	Exclusions *DeadCodeExclusions

	// IncludeExported includes exported symbols in results.
	IncludeExported bool

	// IncludeTests includes test files in analysis.
	IncludeTests bool

	// MaxResults limits the number of results (0 = unlimited).
	MaxResults int
}

// DefaultDeadCodeOptions returns sensible defaults.
func DefaultDeadCodeOptions() DeadCodeOptions {
	return DeadCodeOptions{
		Exclusions:      DefaultExclusions(),
		IncludeExported: false,
		IncludeTests:    false,
		MaxResults:      0,
	}
}

// DeadCodeFinder finds unreferenced code.
//
// # Description
//
// DeadCodeFinder detects code that appears to be unused. It is conservative
// by default, preferring false negatives over false positives because:
// - Reflection can invoke code without static references
// - Build tags may exclude code from normal builds
// - Exported symbols may be part of a public API
//
// # Thread Safety
//
// This type is safe for concurrent use.
type DeadCodeFinder struct {
	graph       *graph.Graph
	idx         *index.SymbolIndex
	projectRoot string
	mu          sync.RWMutex
}

// NewDeadCodeFinder creates a new dead code finder.
//
// # Inputs
//
//   - g: Code graph for reference analysis.
//   - idx: Symbol index for lookups.
//   - projectRoot: Project root for reading source files.
//
// # Outputs
//
//   - *DeadCodeFinder: Configured finder.
func NewDeadCodeFinder(g *graph.Graph, idx *index.SymbolIndex, projectRoot string) *DeadCodeFinder {
	return &DeadCodeFinder{
		graph:       g,
		idx:         idx,
		projectRoot: projectRoot,
	}
}

// FindDeadCode finds unreferenced code in the specified scope.
//
// # Description
//
// Analyzes the code graph to find symbols with no incoming references.
// Uses a conservative approach to avoid false positives.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - scope: Package or file path prefix (empty = all).
//   - opts: Detection options.
//
// # Outputs
//
//   - []DeadCode: Found dead code.
//   - error: Non-nil on failure.
//
// # Example
//
//	finder := NewDeadCodeFinder(graph, index, "/project")
//	dead, err := finder.FindDeadCode(ctx, "pkg/internal", nil)
func (d *DeadCodeFinder) FindDeadCode(
	ctx context.Context,
	scope string,
	opts *DeadCodeOptions,
) ([]DeadCode, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	if opts == nil {
		defaults := DefaultDeadCodeOptions()
		opts = &defaults
	}

	if opts.Exclusions == nil {
		opts.Exclusions = DefaultExclusions()
	}

	var results []DeadCode

	// Build a set of referenced symbols
	referenced := d.buildReferencedSet()

	// Check all symbols
	allSymbols := d.getAllSymbols()

	for _, sym := range allSymbols {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Check scope
		if !d.inScope(sym, scope, opts.IncludeTests) {
			continue
		}

		// Check if referenced
		if referenced[sym.ID] {
			continue
		}

		// Check exclusions
		excluded, reason := d.isExcluded(sym, opts)
		if excluded {
			continue
		}

		// Calculate confidence based on how certain we are
		confidence := d.calculateConfidence(sym, opts, reason)

		results = append(results, DeadCode{
			Type:       sym.Kind.String(),
			Name:       sym.Name,
			FilePath:   sym.FilePath,
			Line:       sym.StartLine,
			Reason:     "unreferenced",
			Confidence: confidence,
		})
	}

	// Sort by confidence (highest first), then by file path
	sort.Slice(results, func(i, j int) bool {
		if results[i].Confidence != results[j].Confidence {
			return results[i].Confidence > results[j].Confidence
		}
		return results[i].FilePath < results[j].FilePath
	})

	// Apply max results limit
	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	return results, nil
}

// buildReferencedSet builds a set of all referenced symbol IDs.
func (d *DeadCodeFinder) buildReferencedSet() map[string]bool {
	referenced := make(map[string]bool)

	// All edges represent references
	for _, edge := range d.graph.Edges() {
		// The target of an edge is referenced
		referenced[edge.ToID] = true
	}

	return referenced
}

// getAllSymbols returns all symbols to analyze.
func (d *DeadCodeFinder) getAllSymbols() []*ast.Symbol {
	var symbols []*ast.Symbol

	// Get functions
	functions := d.idx.GetByKind(ast.SymbolKindFunction)
	symbols = append(symbols, functions...)

	// Get methods
	methods := d.idx.GetByKind(ast.SymbolKindMethod)
	symbols = append(symbols, methods...)

	// Get types
	structs := d.idx.GetByKind(ast.SymbolKindStruct)
	symbols = append(symbols, structs...)

	types := d.idx.GetByKind(ast.SymbolKindType)
	symbols = append(symbols, types...)

	interfaces := d.idx.GetByKind(ast.SymbolKindInterface)
	symbols = append(symbols, interfaces...)

	// Get variables
	variables := d.idx.GetByKind(ast.SymbolKindVariable)
	symbols = append(symbols, variables...)

	// Get constants
	constants := d.idx.GetByKind(ast.SymbolKindConstant)
	symbols = append(symbols, constants...)

	return symbols
}

// inScope checks if a symbol is in the requested scope.
func (d *DeadCodeFinder) inScope(sym *ast.Symbol, scope string, includeTests bool) bool {
	if sym == nil {
		return false
	}

	// Check test file exclusion
	if !includeTests && strings.HasSuffix(sym.FilePath, "_test.go") {
		return false
	}

	// Check scope
	if scope == "" {
		return true
	}

	return strings.HasPrefix(sym.FilePath, scope)
}

// isExcluded checks if a symbol should be excluded from dead code detection.
func (d *DeadCodeFinder) isExcluded(sym *ast.Symbol, opts *DeadCodeOptions) (bool, string) {
	if sym == nil {
		return true, "nil symbol"
	}

	exclusions := opts.Exclusions

	// Check entry points
	for _, pattern := range exclusions.EntryPoints {
		if matchPattern(pattern, sym.Name) {
			return true, "entry point"
		}
	}

	// Check exported symbols
	if exclusions.ExportedSymbols && sym.Exported && !opts.IncludeExported {
		return true, "exported symbol"
	}

	// Check interface implementations
	if exclusions.InterfaceImpls && sym.Kind == ast.SymbolKindMethod {
		// Check if this method implements an interface
		if d.implementsInterface(sym) {
			return true, "interface implementation"
		}
	}

	// Check for reflection patterns in file
	if len(exclusions.ReflectionPatterns) > 0 {
		if d.hasReflectionPattern(sym, exclusions.ReflectionPatterns) {
			return true, "reflection pattern"
		}
	}

	// Check build tags
	if exclusions.BuildTaggedFiles && d.hasBuildTag(sym.FilePath) {
		return true, "build tagged"
	}

	// Check annotation patterns
	if len(exclusions.AnnotationPatterns) > 0 {
		if d.hasAnnotation(sym, exclusions.AnnotationPatterns) {
			return true, "annotation"
		}
	}

	return false, ""
}

// matchPattern matches a pattern with wildcards.
func matchPattern(pattern, name string) bool {
	// Handle wildcard suffix
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(name, prefix)
	}

	// Exact match
	return pattern == name
}

// implementsInterface checks if a method implements an interface.
func (d *DeadCodeFinder) implementsInterface(sym *ast.Symbol) bool {
	if sym.Kind != ast.SymbolKindMethod {
		return false
	}

	// Check if there's an implements edge for the receiver type
	// This is a simplified check - full interface matching would require
	// comparing method signatures
	node, exists := d.graph.GetNode(sym.ID)
	if !exists {
		return false
	}

	// Check if the receiver type has any implements edges
	for _, edge := range node.Incoming {
		if edge.Type == graph.EdgeTypeImplements {
			return true
		}
	}

	// Also check if receiver type is used in an interface context
	if sym.Receiver != "" {
		receiverType := strings.TrimPrefix(sym.Receiver, "*")
		receiverSymbols := d.idx.GetByName(receiverType)
		for _, rs := range receiverSymbols {
			if rs.Kind == ast.SymbolKindStruct || rs.Kind == ast.SymbolKindType {
				rsNode, ok := d.graph.GetNode(rs.ID)
				if !ok {
					continue
				}
				for _, edge := range rsNode.Outgoing {
					if edge.Type == graph.EdgeTypeImplements {
						return true
					}
				}
			}
		}
	}

	return false
}

// hasReflectionPattern checks if a symbol's file uses reflection.
func (d *DeadCodeFinder) hasReflectionPattern(sym *ast.Symbol, patterns []string) bool {
	// Read file content
	filePath := filepath.Join(d.projectRoot, sym.FilePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	contentStr := string(content)

	for _, pattern := range patterns {
		if strings.Contains(contentStr, pattern) {
			return true
		}
	}

	return false
}

// hasBuildTag checks if a file has build tags.
func (d *DeadCodeFinder) hasBuildTag(filePath string) bool {
	fullPath := filepath.Join(d.projectRoot, filePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false
	}

	// Check for build tags at the start of the file
	// Format: //go:build ... or // +build ...
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "//go:build") || strings.HasPrefix(trimmed, "// +build") {
			return true
		}
		// Build constraints must be before package declaration
		if strings.HasPrefix(trimmed, "package ") {
			break
		}
	}

	return false
}

// hasAnnotation checks if a symbol has an exclusion annotation.
func (d *DeadCodeFinder) hasAnnotation(sym *ast.Symbol, patterns []string) bool {
	// Check doc comment
	for _, pattern := range patterns {
		if strings.Contains(sym.DocComment, pattern) {
			return true
		}
	}

	// Read source to check inline comments
	code, err := d.readSymbolContext(sym)
	if err != nil {
		return false
	}

	for _, pattern := range patterns {
		if strings.Contains(code, pattern) {
			return true
		}
	}

	return false
}

// readSymbolContext reads the source code around a symbol.
func (d *DeadCodeFinder) readSymbolContext(sym *ast.Symbol) (string, error) {
	filePath := filepath.Join(d.projectRoot, sym.FilePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")

	// Get lines before and including the symbol
	startLine := sym.StartLine - 5 // Include context before
	if startLine < 1 {
		startLine = 1
	}
	endLine := sym.StartLine + 2 // Include a bit after

	if endLine > len(lines) {
		endLine = len(lines)
	}

	return strings.Join(lines[startLine-1:endLine], "\n"), nil
}

// calculateConfidence determines how confident we are that this is dead code.
func (d *DeadCodeFinder) calculateConfidence(sym *ast.Symbol, opts *DeadCodeOptions, excludeReason string) float64 {
	confidence := 0.9 // Base confidence for unreferenced

	// Lower confidence for various edge cases
	switch sym.Kind {
	case ast.SymbolKindMethod:
		// Methods might be called via interface
		confidence *= 0.8
	case ast.SymbolKindVariable, ast.SymbolKindConstant:
		// Variables might be used via reflection
		confidence *= 0.85
	case ast.SymbolKindStruct, ast.SymbolKindType:
		// Types might be used for type assertions
		confidence *= 0.75
	}

	// Lower confidence if file has struct tags (likely reflection)
	if d.hasStructTags(sym.FilePath) {
		confidence *= 0.7
	}

	// Lower confidence for unexported symbols in packages with exported symbols
	// (might be helpers for exported functions)
	if !sym.Exported {
		confidence *= 0.9
	}

	// Clamp to [0, 1]
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.1 {
		confidence = 0.1
	}

	return confidence
}

// hasStructTags checks if a file contains struct tags (suggesting reflection use).
func (d *DeadCodeFinder) hasStructTags(filePath string) bool {
	fullPath := filepath.Join(d.projectRoot, filePath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false
	}

	// Look for struct tag patterns
	tagPattern := regexp.MustCompile("`[^`]+`")
	return tagPattern.Match(content)
}

// Summary generates a summary of dead code findings.
func (d *DeadCodeFinder) Summary(deadCode []DeadCode) string {
	if len(deadCode) == 0 {
		return "No dead code detected"
	}

	counts := make(map[string]int)
	for _, dc := range deadCode {
		counts[dc.Type]++
	}

	var parts []string
	for typ, count := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", typ, count))
	}

	sort.Strings(parts)

	return fmt.Sprintf("Found %d potentially dead code item(s): %s",
		len(deadCode), strings.Join(parts, ", "))
}

// IsExcluded is a public wrapper for exclusion checking.
//
// # Inputs
//
//   - symbol: The symbol to check.
//   - g: The code graph.
//
// # Outputs
//
//   - bool: True if the symbol should be excluded.
//   - string: The reason for exclusion.
func (e *DeadCodeExclusions) IsExcluded(symbol *ast.Symbol, g *graph.Graph) (bool, string) {
	if symbol == nil {
		return true, "nil symbol"
	}

	// Check entry points
	for _, pattern := range e.EntryPoints {
		if matchPattern(pattern, symbol.Name) {
			return true, "entry point: " + pattern
		}
	}

	// Check exported
	if e.ExportedSymbols && symbol.Exported {
		return true, "exported symbol"
	}

	return false, ""
}
