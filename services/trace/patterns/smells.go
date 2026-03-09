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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// Package-level compiled regexes for smell detection.
var (
	reErrorSwallowEmpty   = regexp.MustCompile(`if\s+err\s*!=\s*nil\s*{\s*}`)
	reErrorSwallowComment = regexp.MustCompile(`if\s+err\s*!=\s*nil\s*{\s*//.*\s*}`)
	reErrorSwallowIgnore  = regexp.MustCompile(`_\s*=\s*\w+\(`)
	reMagicNumber         = regexp.MustCompile(`\b(\d{3,})\b`)
	reEmptyInterface      = regexp.MustCompile(`interface\s*{\s*}`)
	reAnyKeyword          = regexp.MustCompile(`\bany\b`)
)

// SmellOptions configures code smell detection.
type SmellOptions struct {
	// Thresholds configures detection thresholds.
	Thresholds SmellThresholds

	// MinSeverity filters results by minimum severity.
	MinSeverity Severity

	// ContextLines controls how many lines of surrounding code to include
	// in the Code field of each CodeSmell result (0 = no code context).
	ContextLines int

	// IncludeTests includes test files in analysis.
	IncludeTests bool

	// MaxResults limits the number of results (0 = unlimited).
	MaxResults int
}

// DefaultSmellOptions returns sensible defaults.
func DefaultSmellOptions() SmellOptions {
	return SmellOptions{
		Thresholds:   DefaultSmellThresholds(),
		MinSeverity:  SeverityWarning,
		IncludeTests: false,
		MaxResults:   0,
	}
}

// SmellFinder finds code smells in the codebase.
//
// # Description
//
// SmellFinder detects potential code quality issues like long functions,
// god objects, error swallowing, and more. All detections are
// configurable via thresholds.
//
// # Thread Safety
//
// This type is safe for concurrent use.
type SmellFinder struct {
	idx        *index.SymbolIndex
	fileReader *FileReader
	crs        CRSRecorder
	mu         sync.RWMutex
}

// NewSmellFinder creates a new code smell finder.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - projectRoot: Project root for reading source files.
//
// # Outputs
//
//   - *SmellFinder: Configured finder.
func NewSmellFinder(idx *index.SymbolIndex, projectRoot string) *SmellFinder {
	return &SmellFinder{
		idx:        idx,
		fileReader: NewFileReader(projectRoot),
		crs:        &NopCRSRecorder{},
	}
}

// NewSmellFinderWithReader creates a smell finder with a shared FileReader.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - reader: Shared cached file reader.
//
// # Outputs
//
//   - *SmellFinder: Configured finder.
func NewSmellFinderWithReader(idx *index.SymbolIndex, reader *FileReader) *SmellFinder {
	return &SmellFinder{
		idx:        idx,
		fileReader: reader,
		crs:        &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this finder.
//
// # Inputs
//
//   - recorder: CRS recorder for step tracking.
func (s *SmellFinder) SetCRS(recorder CRSRecorder) {
	s.crs = recorder
}

// FindCodeSmells finds code smells in the specified scope.
//
// # Description
//
// Scans the codebase for various code smells including:
// - Long functions (exceeds line threshold)
// - Long parameter lists (exceeds param threshold)
// - God objects (types with too many methods)
// - Error swallowing (empty error handling)
// - Magic numbers (unexplained numeric literals)
// - Deep nesting (excessive indentation)
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - scope: Package or file path prefix (empty = all).
//   - opts: Detection options.
//
// # Outputs
//
//   - []CodeSmell: Found code smells.
//   - error: Non-nil on failure.
func (s *SmellFinder) FindCodeSmells(
	ctx context.Context,
	scope string,
	opts *SmellOptions,
) ([]CodeSmell, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startSmellsSpan(ctx, scope)
	defer span.End()

	if opts == nil {
		defaults := DefaultSmellOptions()
		opts = &defaults
	}

	var results []CodeSmell

	// Run all smell detectors
	detectors := []struct {
		name   string
		detect func(context.Context, string, *SmellOptions) []CodeSmell
	}{
		{"long_function", s.detectLongFunctions},
		{"long_parameter_list", s.detectLongParameterLists},
		{"god_object", s.detectGodObjects},
		{"error_swallowing", s.detectErrorSwallowing},
		{"magic_numbers", s.detectMagicNumbers},
		{"deep_nesting", s.detectDeepNesting},
		{"empty_interface", s.detectEmptyInterface},
	}

	for _, detector := range detectors {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		smells := detector.detect(ctx, scope, opts)

		// Filter by severity
		for _, smell := range smells {
			if severityRank(smell.Severity) >= severityRank(opts.MinSeverity) {
				results = append(results, smell)
			}
		}
	}

	// Sort by severity (highest first), then by location
	sort.Slice(results, func(i, j int) bool {
		if severityRank(results[i].Severity) != severityRank(results[j].Severity) {
			return severityRank(results[i].Severity) > severityRank(results[j].Severity)
		}
		return results[i].Location < results[j].Location
	})

	// Apply max results limit
	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	dur := time.Since(start)
	setSmellsSpanResult(span, len(results), nil)
	recordSmellsMetrics(ctx, dur, len(results), nil)
	s.crs.RecordToolStep(ctx, "find_code_smells", len(results), dur, nil)

	return results, nil
}

// detectLongFunctions finds functions that exceed the line threshold.
func (s *SmellFinder) detectLongFunctions(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		lineCount := fn.EndLine - fn.StartLine + 1
		if lineCount > opts.Thresholds.MaxFunctionLines {
			severity := SeverityWarning
			if lineCount > opts.Thresholds.MaxFunctionLines*2 {
				severity = SeverityError
			}

			smells = append(smells, CodeSmell{
				Type:     SmellLongFunction,
				Severity: severity,
				Location: fmt.Sprintf("%s:%d", fn.FilePath, fn.StartLine),
				Description: fmt.Sprintf(
					"Function '%s' has %d lines (threshold: %d)",
					fn.Name, lineCount, opts.Thresholds.MaxFunctionLines,
				),
				Suggestion: "Consider breaking this function into smaller, focused functions",
				Value:      lineCount,
				Threshold:  opts.Thresholds.MaxFunctionLines,
			})
		}
	}

	return smells
}

// detectLongParameterLists finds functions with too many parameters.
func (s *SmellFinder) detectLongParameterLists(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		// Count parameters from signature
		paramCount := countParameters(fn.Signature)
		if paramCount > opts.Thresholds.MaxParameters {
			severity := SeverityWarning
			if paramCount > opts.Thresholds.MaxParameters+3 {
				severity = SeverityError
			}

			smells = append(smells, CodeSmell{
				Type:     SmellLongParameterList,
				Severity: severity,
				Location: fmt.Sprintf("%s:%d", fn.FilePath, fn.StartLine),
				Description: fmt.Sprintf(
					"Function '%s' has %d parameters (threshold: %d)",
					fn.Name, paramCount, opts.Thresholds.MaxParameters,
				),
				Suggestion: "Consider using an options struct or builder pattern",
				Value:      paramCount,
				Threshold:  opts.Thresholds.MaxParameters,
			})
		}
	}

	return smells
}

// detectGodObjects finds types with too many methods.
func (s *SmellFinder) detectGodObjects(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	// Count methods per type
	methodCounts := make(map[string]int)
	typeSymbols := make(map[string]*ast.Symbol)

	// Get all structs and types
	structs := s.idx.GetByKind(ast.SymbolKindStruct)
	types := s.idx.GetByKind(ast.SymbolKindType)
	allTypes := append(structs, types...)

	for _, t := range allTypes {
		if !s.inScope(t, scope, opts.IncludeTests) {
			continue
		}
		typeSymbols[t.Name] = t
		methodCounts[t.Name] = 0
	}

	// Count methods
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	for _, m := range methods {
		if m.Receiver != "" {
			// Extract type name from receiver (e.g., "*MyType" -> "MyType")
			typeName := strings.TrimPrefix(m.Receiver, "*")
			if _, exists := methodCounts[typeName]; exists {
				methodCounts[typeName]++
			}
		}
	}

	// Find god objects
	for typeName, count := range methodCounts {
		if ctx.Err() != nil {
			break
		}

		if count > opts.Thresholds.MaxMethodCount {
			typeSymbol, exists := typeSymbols[typeName]
			if !exists {
				continue
			}

			severity := SeverityWarning
			if count > opts.Thresholds.MaxMethodCount*2 {
				severity = SeverityError
			}

			smells = append(smells, CodeSmell{
				Type:     SmellGodObject,
				Severity: severity,
				Location: fmt.Sprintf("%s:%d", typeSymbol.FilePath, typeSymbol.StartLine),
				Description: fmt.Sprintf(
					"Type '%s' has %d methods (threshold: %d)",
					typeName, count, opts.Thresholds.MaxMethodCount,
				),
				Suggestion: "Consider splitting this type into smaller, focused types",
				Value:      count,
				Threshold:  opts.Thresholds.MaxMethodCount,
			})
		}
	}

	return smells
}

// detectErrorSwallowing finds empty error handling.
func (s *SmellFinder) detectErrorSwallowing(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	patterns := []*regexp.Regexp{
		reErrorSwallowEmpty,
		reErrorSwallowComment,
		reErrorSwallowIgnore,
	}

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		// Read function code
		code, err := s.fileReader.ReadSymbolCode(fn)
		if err != nil {
			continue
		}

		for _, pattern := range patterns {
			matches := pattern.FindAllStringIndex(code, -1)
			for _, match := range matches {
				// Calculate line number
				lineOffset := strings.Count(code[:match[0]], "\n")
				line := fn.StartLine + lineOffset

				codeSnippet := code[match[0]:match[1]]
				if opts.ContextLines > 0 {
					codeSnippet = extractContext(code, match[0], match[1], opts.ContextLines)
				}

				smells = append(smells, CodeSmell{
					Type:        SmellErrorSwallowing,
					Severity:    SeverityWarning,
					Location:    fmt.Sprintf("%s:%d", fn.FilePath, line),
					Description: fmt.Sprintf("Potential error swallowing in '%s'", fn.Name),
					Suggestion:  "Handle the error: log it, return it, or document why it's intentionally ignored",
					Code:        codeSnippet,
				})
			}
		}
	}

	return smells
}

// detectMagicNumbers finds unexplained numeric literals.
func (s *SmellFinder) detectMagicNumbers(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	pattern := reMagicNumber
	excluded := map[string]bool{
		"100": true, "1000": true, "1024": true,
		"8080": true, "3000": true, "443": true, "80": true,
	}

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		code, err := s.fileReader.ReadSymbolCode(fn)
		if err != nil {
			continue
		}

		matches := pattern.FindAllStringSubmatchIndex(code, -1)
		for _, match := range matches {
			if len(match) < 4 {
				continue
			}

			number := code[match[2]:match[3]]
			if excluded[number] {
				continue
			}

			// Check if it's part of a constant assignment (allow those)
			lineStart := strings.LastIndex(code[:match[0]], "\n") + 1
			lineEnd := strings.Index(code[match[0]:], "\n")
			if lineEnd == -1 {
				lineEnd = len(code) - match[0]
			}
			lineContent := code[lineStart : match[0]+lineEnd]
			if strings.Contains(lineContent, "const") || strings.Contains(lineContent, "=") && strings.Contains(lineContent[:strings.Index(lineContent, "=")], strings.ToUpper(strings.ToUpper(number)[:1])) {
				continue
			}

			lineOffset := strings.Count(code[:match[0]], "\n")
			line := fn.StartLine + lineOffset

			smells = append(smells, CodeSmell{
				Type:        SmellMagicNumber,
				Severity:    SeverityInfo,
				Location:    fmt.Sprintf("%s:%d", fn.FilePath, line),
				Description: fmt.Sprintf("Magic number '%s' in '%s'", number, fn.Name),
				Suggestion:  "Consider extracting to a named constant",
				Code:        number,
			})
		}
	}

	return smells
}

// detectDeepNesting finds excessive nesting depth.
func (s *SmellFinder) detectDeepNesting(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		code, err := s.fileReader.ReadSymbolCode(fn)
		if err != nil {
			continue
		}

		maxDepth := calculateMaxNesting(code)
		if maxDepth > opts.Thresholds.MaxNestingDepth {
			severity := SeverityWarning
			if maxDepth > opts.Thresholds.MaxNestingDepth+2 {
				severity = SeverityError
			}

			smells = append(smells, CodeSmell{
				Type:     SmellDeepNesting,
				Severity: severity,
				Location: fmt.Sprintf("%s:%d", fn.FilePath, fn.StartLine),
				Description: fmt.Sprintf(
					"Function '%s' has nesting depth of %d (threshold: %d)",
					fn.Name, maxDepth, opts.Thresholds.MaxNestingDepth,
				),
				Suggestion: "Consider using early returns, extracting helper functions, or inverting conditions",
				Value:      maxDepth,
				Threshold:  opts.Thresholds.MaxNestingDepth,
			})
		}
	}

	return smells
}

// detectEmptyInterface finds overuse of interface{} / any.
func (s *SmellFinder) detectEmptyInterface(ctx context.Context, scope string, opts *SmellOptions) []CodeSmell {
	var smells []CodeSmell

	patterns := []*regexp.Regexp{
		reEmptyInterface,
		reAnyKeyword,
	}

	functions := s.idx.GetByKind(ast.SymbolKindFunction)
	methods := s.idx.GetByKind(ast.SymbolKindMethod)
	allFuncs := append(functions, methods...)

	for _, fn := range allFuncs {
		if ctx.Err() != nil {
			break
		}

		if !s.inScope(fn, scope, opts.IncludeTests) {
			continue
		}

		// Check signature for empty interface
		for _, pattern := range patterns {
			if pattern.MatchString(fn.Signature) {
				smells = append(smells, CodeSmell{
					Type:        SmellEmptyInterface,
					Severity:    SeverityInfo,
					Location:    fmt.Sprintf("%s:%d", fn.FilePath, fn.StartLine),
					Description: fmt.Sprintf("Function '%s' uses empty interface/any in signature", fn.Name),
					Suggestion:  "Consider using a concrete type or a more specific interface",
					Code:        fn.Signature,
				})
			}
		}
	}

	return smells
}

// inScope checks if a symbol is in the requested scope.
func (s *SmellFinder) inScope(sym *ast.Symbol, scope string, includeTests bool) bool {
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

// countParameters counts the number of parameters in a function signature.
func countParameters(signature string) int {
	// Find the parameter list in the signature
	start := strings.Index(signature, "(")
	end := strings.Index(signature, ")")

	if start == -1 || end == -1 || end <= start {
		return 0
	}

	params := signature[start+1 : end]
	if params == "" {
		return 0
	}

	// Count commas, but handle nested parentheses
	count := 1
	depth := 0
	for _, r := range params {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}

	return count
}

// calculateMaxNesting calculates the maximum nesting depth in code.
func calculateMaxNesting(code string) int {
	maxDepth := 0
	currentDepth := 0

	for _, r := range code {
		switch r {
		case '{':
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
		case '}':
			if currentDepth > 0 {
				currentDepth--
			}
		}
	}

	return maxDepth
}

// extractContext extracts lines around a match position within code, providing
// contextLines lines before and after the match.
func extractContext(code string, matchStart, matchEnd, contextLines int) string {
	lines := strings.Split(code, "\n")
	startLineIdx := strings.Count(code[:matchStart], "\n")
	endLineIdx := strings.Count(code[:matchEnd], "\n")

	from := startLineIdx - contextLines
	if from < 0 {
		from = 0
	}
	to := endLineIdx + contextLines + 1
	if to > len(lines) {
		to = len(lines)
	}

	return strings.Join(lines[from:to], "\n")
}

// Summary generates a summary of code smell findings.
func (s *SmellFinder) Summary(smells []CodeSmell) string {
	if len(smells) == 0 {
		return "No code smells detected"
	}

	counts := make(map[SmellType]int)
	for _, smell := range smells {
		counts[smell.Type]++
	}

	var parts []string
	for smellType, count := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", smellType, count))
	}

	// Sort for consistent output
	sort.Strings(parts)

	return fmt.Sprintf("Found %d code smell(s): %s",
		len(smells), strings.Join(parts, ", "))
}
