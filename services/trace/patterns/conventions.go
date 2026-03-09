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
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// Package-level compiled regexes for convention extraction.
var (
	reErrorWrap     = regexp.MustCompile(`fmt\.Errorf.*%w|errors\.Wrap|xerrors\.Errorf`)
	reErrorf        = regexp.MustCompile(`fmt\.Errorf`)
	reTableDriven   = regexp.MustCompile(`(?:tests|cases|testCases)\s*:?=\s*\[\]`)
	reSubtest       = regexp.MustCompile(`t\.Run\(`)
	reParallel      = regexp.MustCompile(`t\.Parallel\(\)`)
	reDocExample    = regexp.MustCompile(`(?m)^//\s*Example:`)
	reGroupedImport = regexp.MustCompile(`import\s+\([\s\S]*?\n\s*\n[\s\S]*?\)`)
	reAliasImport   = regexp.MustCompile(`\w+\s+"`)
)

// ConventionType categorizes conventions.
type ConventionType string

const (
	// ConventionNaming is a naming convention.
	ConventionNaming ConventionType = "naming"

	// ConventionErrorHandling is an error handling convention.
	ConventionErrorHandling ConventionType = "error_handling"

	// ConventionFileOrganization is a file organization convention.
	ConventionFileOrganization ConventionType = "file_organization"

	// ConventionTesting is a testing convention.
	ConventionTesting ConventionType = "testing"

	// ConventionDocumentation is a documentation convention.
	ConventionDocumentation ConventionType = "documentation"

	// ConventionImports is an import organization convention.
	ConventionImports ConventionType = "imports"
)

// ConventionOptions configures convention extraction.
type ConventionOptions struct {
	// SampleSize is the max symbols to sample per type (0 = all).
	SampleSize int

	// MinFrequency is the minimum frequency to report (0.0 - 1.0).
	MinFrequency float64

	// Types filters which convention categories to extract.
	// Valid values: "naming", "error_handling", "file_organization", "testing", "documentation", "imports".
	// Empty slice means all types.
	Types []string

	// IncludeTests includes test files in analysis.
	IncludeTests bool
}

// DefaultConventionOptions returns sensible defaults.
func DefaultConventionOptions() ConventionOptions {
	return ConventionOptions{
		SampleSize:   100,
		MinFrequency: 0.3,
		IncludeTests: false,
	}
}

// ConventionExtractor extracts coding conventions from a codebase.
//
// # Description
//
// ConventionExtractor analyzes the codebase to infer coding conventions.
// This helps new code follow existing patterns and maintains consistency.
//
// # Thread Safety
//
// This type is safe for concurrent use.
type ConventionExtractor struct {
	idx        *index.SymbolIndex
	fileReader *FileReader
	crs        CRSRecorder
	mu         sync.RWMutex
}

// NewConventionExtractor creates a new convention extractor.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - projectRoot: Project root for reading source files.
//
// # Outputs
//
//   - *ConventionExtractor: Configured extractor.
func NewConventionExtractor(idx *index.SymbolIndex, projectRoot string) *ConventionExtractor {
	return &ConventionExtractor{
		idx:        idx,
		fileReader: NewFileReader(projectRoot),
		crs:        &NopCRSRecorder{},
	}
}

// NewConventionExtractorWithReader creates a convention extractor with a shared FileReader.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - reader: Shared cached file reader.
//
// # Outputs
//
//   - *ConventionExtractor: Configured extractor.
func NewConventionExtractorWithReader(idx *index.SymbolIndex, reader *FileReader) *ConventionExtractor {
	return &ConventionExtractor{
		idx:        idx,
		fileReader: reader,
		crs:        &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this extractor.
func (c *ConventionExtractor) SetCRS(recorder CRSRecorder) {
	c.crs = recorder
}

// ExtractConventions extracts coding conventions from the specified scope.
//
// # Description
//
// Analyzes the codebase to identify common patterns in:
// - Naming (camelCase, snake_case, prefixes, suffixes)
// - Error handling (return patterns, wrapping)
// - File organization (one type per file, grouping)
// - Testing (table-driven, subtests)
// - Documentation (format, coverage)
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - scope: Package or file path prefix (empty = all).
//   - opts: Extraction options.
//
// # Outputs
//
//   - []Convention: Extracted conventions.
//   - error: Non-nil on failure.
func (c *ConventionExtractor) ExtractConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) ([]Convention, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startConventionsSpan(ctx, scope)
	defer span.End()

	if opts == nil {
		defaults := DefaultConventionOptions()
		opts = &defaults
	}

	var conventions []Convention

	// Build type filter set for O(1) lookup
	typeFilter := make(map[string]bool, len(opts.Types))
	for _, t := range opts.Types {
		typeFilter[t] = true
	}

	// Extract different types of conventions
	extractors := []struct {
		name    string
		extract func(context.Context, string, *ConventionOptions) []Convention
	}{
		{"naming", c.extractNamingConventions},
		{"error_handling", c.extractErrorHandlingConventions},
		{"file_organization", c.extractFileOrganizationConventions},
		{"testing", c.extractTestingConventions},
		{"documentation", c.extractDocumentationConventions},
		{"imports", c.extractImportConventions},
	}

	for _, extractor := range extractors {
		// Skip if type filter is active and this type isn't included
		if len(typeFilter) > 0 && !typeFilter[extractor.name] {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		extracted := extractor.extract(ctx, scope, opts)

		// Filter by minimum frequency
		for _, conv := range extracted {
			if conv.Frequency >= opts.MinFrequency {
				conventions = append(conventions, conv)
			}
		}
	}

	// Sort by frequency (highest first)
	sort.Slice(conventions, func(i, j int) bool {
		return conventions[i].Frequency > conventions[j].Frequency
	})

	dur := time.Since(start)
	setConventionsSpanResult(span, len(conventions), nil)
	recordConventionsMetrics(ctx, dur, len(conventions), nil)
	c.crs.RecordToolStep(ctx, "extract_conventions", len(conventions), dur, nil)

	return conventions, nil
}

// extractNamingConventions extracts naming patterns.
func (c *ConventionExtractor) extractNamingConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	// Analyze function naming
	functions := c.sampleSymbols(c.idx.GetByKind(ast.SymbolKindFunction), scope, opts)
	funcPatterns := make(map[string]int)
	totalFuncs := 0

	for _, fn := range functions {
		if ctx.Err() != nil {
			break
		}

		totalFuncs++
		patterns := classifyName(fn.Name)
		for _, p := range patterns {
			funcPatterns[p]++
		}
	}

	// Report function naming conventions
	for pattern, count := range funcPatterns {
		if totalFuncs == 0 {
			continue
		}
		freq := float64(count) / float64(totalFuncs)
		if freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionNaming),
				Pattern:     pattern,
				Examples:    c.findExamples(functions, pattern, 3),
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of functions use %s naming", freq*100, pattern),
			})
		}
	}

	// Analyze struct naming
	structs := c.sampleSymbols(c.idx.GetByKind(ast.SymbolKindStruct), scope, opts)
	structSuffixes := make(map[string]int)
	totalStructs := 0

	suffixPatterns := []string{"Service", "Handler", "Client", "Config", "Options", "Builder", "Manager", "Repository"}

	for _, s := range structs {
		if ctx.Err() != nil {
			break
		}

		totalStructs++
		for _, suffix := range suffixPatterns {
			if strings.HasSuffix(s.Name, suffix) {
				structSuffixes[suffix]++
			}
		}
	}

	// Report struct naming conventions
	for suffix, count := range structSuffixes {
		if totalStructs == 0 {
			continue
		}
		freq := float64(count) / float64(totalStructs)
		if freq >= 0.1 { // Lower threshold for suffixes
			conventions = append(conventions, Convention{
				Type:        string(ConventionNaming),
				Pattern:     fmt.Sprintf("*%s suffix", suffix),
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of structs use %s suffix", freq*100, suffix),
			})
		}
	}

	return conventions
}

// extractErrorHandlingConventions extracts error handling patterns.
func (c *ConventionExtractor) extractErrorHandlingConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	functions := c.sampleSymbols(c.idx.GetByKind(ast.SymbolKindFunction), scope, opts)
	methods := c.sampleSymbols(c.idx.GetByKind(ast.SymbolKindMethod), scope, opts)
	allFuncs := append(functions, methods...)

	// Count error return patterns
	returnsError := 0
	wrapsErrors := 0
	usesErrorf := 0
	totalFuncs := 0

	errwrapPatterns := reErrorWrap
	errorfPattern := reErrorf

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !c.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		totalFuncs++

		// Check if function returns error
		if strings.Contains(fn.Signature, "error") {
			returnsError++
		}

		// Read function code to check patterns
		code, err := c.fileReader.ReadSymbolCode(fn)
		if err != nil {
			continue
		}

		if errwrapPatterns.MatchString(code) {
			wrapsErrors++
		}
		if errorfPattern.MatchString(code) {
			usesErrorf++
		}
	}

	// Report error handling conventions
	if totalFuncs > 0 {
		if freq := float64(returnsError) / float64(totalFuncs); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionErrorHandling),
				Pattern:     "explicit error returns",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of functions return error", freq*100),
			})
		}

		if returnsError > 0 {
			if freq := float64(wrapsErrors) / float64(returnsError); freq >= opts.MinFrequency {
				conventions = append(conventions, Convention{
					Type:        string(ConventionErrorHandling),
					Pattern:     "error wrapping with context",
					Frequency:   freq,
					Description: fmt.Sprintf("%.0f%% of error-returning functions use error wrapping", freq*100),
				})
			}
		}
	}

	return conventions
}

// extractFileOrganizationConventions extracts file organization patterns.
func (c *ConventionExtractor) extractFileOrganizationConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	// Count types per file
	typesPerFile := make(map[string]int)

	structs := c.idx.GetByKind(ast.SymbolKindStruct)
	for _, s := range structs {
		if !c.inScope(s, scope, opts.IncludeTests) {
			continue
		}
		typesPerFile[s.FilePath]++
	}

	// Analyze pattern: one type per file vs multiple
	singleType := 0
	multiType := 0

	for _, count := range typesPerFile {
		if count == 1 {
			singleType++
		} else {
			multiType++
		}
	}

	total := singleType + multiType
	if total > 0 {
		singleFreq := float64(singleType) / float64(total)
		if singleFreq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionFileOrganization),
				Pattern:     "one type per file",
				Frequency:   singleFreq,
				Description: fmt.Sprintf("%.0f%% of files contain a single type", singleFreq*100),
			})
		}
	}

	// Check for common file naming patterns
	filePatterns := make(map[string]int)
	files := make(map[string]bool)

	for _, s := range c.idx.GetByKind(ast.SymbolKindFunction) {
		files[s.FilePath] = true
	}
	for _, s := range c.idx.GetByKind(ast.SymbolKindStruct) {
		files[s.FilePath] = true
	}

	for filePath := range files {
		base := filepath.Base(filePath)

		if strings.HasSuffix(base, "_test.go") {
			filePatterns["*_test.go"] = filePatterns["*_test.go"] + 1
		}
		if strings.HasSuffix(base, "_internal.go") || strings.Contains(filePath, "/internal/") {
			filePatterns["internal pattern"] = filePatterns["internal pattern"] + 1
		}
		if base == "types.go" || base == "model.go" || base == "models.go" {
			filePatterns["types/models file"] = filePatterns["types/models file"] + 1
		}
		if base == "errors.go" {
			filePatterns["dedicated errors file"] = filePatterns["dedicated errors file"] + 1
		}
	}

	totalFiles := len(files)
	for pattern, count := range filePatterns {
		if totalFiles > 0 {
			freq := float64(count) / float64(totalFiles)
			if freq >= 0.1 {
				conventions = append(conventions, Convention{
					Type:        string(ConventionFileOrganization),
					Pattern:     pattern,
					Frequency:   freq,
					Description: fmt.Sprintf("%.0f%% of files match %s pattern", freq*100, pattern),
				})
			}
		}
	}

	return conventions
}

// extractTestingConventions extracts testing patterns.
func (c *ConventionExtractor) extractTestingConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	// Get all test functions
	functions := c.idx.GetByKind(ast.SymbolKindFunction)
	var testFuncs []*ast.Symbol

	for _, fn := range functions {
		if strings.HasPrefix(fn.Name, "Test") && strings.HasSuffix(fn.FilePath, "_test.go") {
			if scope == "" || strings.HasPrefix(fn.FilePath, scope) {
				testFuncs = append(testFuncs, fn)
			}
		}
	}

	if len(testFuncs) == 0 {
		return conventions
	}

	// Check for common testing patterns
	tableTests := 0
	subtests := 0
	parallelTests := 0

	tableDrivenPattern := reTableDriven
	subtestPattern := reSubtest
	parallelPattern := reParallel

	for _, fn := range testFuncs {
		if ctx.Err() != nil {
			break
		}

		code, err := c.fileReader.ReadSymbolCode(fn)
		if err != nil {
			continue
		}

		if tableDrivenPattern.MatchString(code) {
			tableTests++
		}
		if subtestPattern.MatchString(code) {
			subtests++
		}
		if parallelPattern.MatchString(code) {
			parallelTests++
		}
	}

	total := len(testFuncs)
	if total > 0 {
		if freq := float64(tableTests) / float64(total); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionTesting),
				Pattern:     "table-driven tests",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of tests use table-driven style", freq*100),
			})
		}

		if freq := float64(subtests) / float64(total); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionTesting),
				Pattern:     "subtests with t.Run",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of tests use subtests", freq*100),
			})
		}

		if freq := float64(parallelTests) / float64(total); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionTesting),
				Pattern:     "parallel tests",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of tests run in parallel", freq*100),
			})
		}
	}

	return conventions
}

// extractDocumentationConventions extracts documentation patterns.
func (c *ConventionExtractor) extractDocumentationConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	// Check exported functions for documentation
	functions := c.sampleSymbols(c.idx.GetByKind(ast.SymbolKindFunction), scope, opts)

	totalExported := 0
	documented := 0
	hasExamples := 0

	examplePattern := reDocExample

	for _, fn := range functions {
		if ctx.Err() != nil {
			break
		}

		if !fn.Exported {
			continue
		}

		totalExported++

		if fn.DocComment != "" {
			documented++
			if examplePattern.MatchString(fn.DocComment) {
				hasExamples++
			}
		}
	}

	if totalExported > 0 {
		if freq := float64(documented) / float64(totalExported); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionDocumentation),
				Pattern:     "exported function documentation",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of exported functions are documented", freq*100),
			})
		}

		if documented > 0 {
			if freq := float64(hasExamples) / float64(documented); freq >= 0.1 {
				conventions = append(conventions, Convention{
					Type:        string(ConventionDocumentation),
					Pattern:     "documentation with examples",
					Frequency:   freq,
					Description: fmt.Sprintf("%.0f%% of documented functions include examples", freq*100),
				})
			}
		}
	}

	return conventions
}

// extractImportConventions extracts import organization patterns.
func (c *ConventionExtractor) extractImportConventions(
	ctx context.Context,
	scope string,
	opts *ConventionOptions,
) []Convention {
	var conventions []Convention

	// Check import organization by reading file headers
	functions := c.idx.GetByKind(ast.SymbolKindFunction)
	files := make(map[string]bool)

	for _, fn := range functions {
		if c.inScope(fn, scope, opts.IncludeTests) {
			files[fn.FilePath] = true
		}
	}

	groupedImports := 0
	aliasedImports := 0
	totalFiles := 0

	groupedPattern := reGroupedImport
	aliasPattern := reAliasImport

	for filePath := range files {
		if ctx.Err() != nil {
			break
		}

		totalFiles++

		lines, err := c.fileReader.ReadLines(filePath)
		if err != nil {
			continue
		}

		// Get import section (first 50 lines should be enough)
		if len(lines) > 50 {
			lines = lines[:50]
		}
		header := strings.Join(lines, "\n")

		if groupedPattern.MatchString(header) {
			groupedImports++
		}
		if aliasPattern.MatchString(header) {
			aliasedImports++
		}
	}

	if totalFiles > 0 {
		if freq := float64(groupedImports) / float64(totalFiles); freq >= opts.MinFrequency {
			conventions = append(conventions, Convention{
				Type:        string(ConventionImports),
				Pattern:     "grouped imports (stdlib, external, internal)",
				Frequency:   freq,
				Description: fmt.Sprintf("%.0f%% of files use grouped imports", freq*100),
			})
		}
	}

	return conventions
}

// Helper functions

func (c *ConventionExtractor) sampleSymbols(symbols []*ast.Symbol, scope string, opts *ConventionOptions) []*ast.Symbol {
	// Filter by scope
	var filtered []*ast.Symbol
	for _, s := range symbols {
		if c.inScope(s, scope, opts.IncludeTests) {
			filtered = append(filtered, s)
		}
	}

	// Sample if needed
	if opts.SampleSize > 0 && len(filtered) > opts.SampleSize {
		filtered = filtered[:opts.SampleSize]
	}

	return filtered
}

func (c *ConventionExtractor) inScope(sym *ast.Symbol, scope string, includeTests bool) bool {
	if sym == nil {
		return false
	}

	if !includeTests && strings.HasSuffix(sym.FilePath, "_test.go") {
		return false
	}

	if scope == "" {
		return true
	}

	return strings.HasPrefix(sym.FilePath, scope)
}

func classifyName(name string) []string {
	var patterns []string

	// Check case style
	if isUpperCamelCase(name) {
		patterns = append(patterns, "PascalCase")
	} else if isLowerCamelCase(name) {
		patterns = append(patterns, "camelCase")
	}

	// Check for common prefixes
	prefixes := []string{"Get", "Set", "Is", "Has", "New", "Create", "Delete", "Update", "Handle"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			patterns = append(patterns, p+"* prefix")
			break
		}
	}

	return patterns
}

func isUpperCamelCase(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	// No underscores
	return !strings.Contains(s, "_")
}

func isLowerCamelCase(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	// No underscores
	return !strings.Contains(s, "_")
}

func (c *ConventionExtractor) findExamples(symbols []*ast.Symbol, pattern string, max int) []string {
	var examples []string

	for _, s := range symbols {
		if len(examples) >= max {
			break
		}

		patterns := classifyName(s.Name)
		for _, p := range patterns {
			if p == pattern {
				examples = append(examples, s.Name)
				break
			}
		}
	}

	return examples
}

// Summary generates a summary of extracted conventions.
func (c *ConventionExtractor) Summary(conventions []Convention) string {
	if len(conventions) == 0 {
		return "No conventions extracted"
	}

	counts := make(map[string]int)
	for _, conv := range conventions {
		counts[conv.Type]++
	}

	var parts []string
	for typ, count := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", typ, count))
	}

	sort.Strings(parts)

	return fmt.Sprintf("Extracted %d convention(s): %s",
		len(conventions), strings.Join(parts, ", "))
}
