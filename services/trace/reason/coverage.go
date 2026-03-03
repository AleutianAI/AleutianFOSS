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
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// TestCoverageFinder analyzes which symbols are covered by tests.
//
// Description:
//
//	TestCoverageFinder uses the code graph to determine which symbols
//	are exercised by test functions. It provides structural coverage
//	information based on call relationships.
//
// Thread Safety:
//
//	TestCoverageFinder is safe for concurrent use.
type TestCoverageFinder struct {
	graph *graph.Graph
	index *index.SymbolIndex
	crs   CRSRecorder
}

// NewTestCoverageFinder creates a new TestCoverageFinder.
//
// Description:
//
//	Creates a finder that can analyze test coverage using the code graph.
//
// Inputs:
//
//	g - The code graph. Must be frozen.
//	idx - The symbol index.
//
// Outputs:
//
//	*TestCoverageFinder - The configured finder.
func NewTestCoverageFinder(g *graph.Graph, idx *index.SymbolIndex) *TestCoverageFinder {
	return &TestCoverageFinder{
		graph: g,
		index: idx,
		crs:   &NopCRSRecorder{},
	}
}

// SetCRS configures CRS recording for this finder.
func (f *TestCoverageFinder) SetCRS(recorder CRSRecorder) {
	f.crs = recorder
}

// TestCoverage is the result of coverage analysis for a symbol.
type TestCoverage struct {
	// TargetID is the symbol being analyzed.
	TargetID string `json:"target_id"`

	// IsCovered indicates if any test exercises this symbol.
	IsCovered bool `json:"is_covered"`

	// DirectTests lists tests that directly call this symbol.
	DirectTests []TestInfo `json:"direct_tests"`

	// IndirectTests lists tests that call this symbol transitively.
	IndirectTests []TestInfo `json:"indirect_tests"`

	// CoverageDepth is the minimum call depth from any test.
	CoverageDepth int `json:"coverage_depth"`

	// Confidence is how confident we are in the analysis (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// Limitations lists what we couldn't analyze.
	Limitations []string `json:"limitations"`
}

// TestInfo describes a test that covers a symbol.
type TestInfo struct {
	// TestID is the test function symbol ID.
	TestID string `json:"test_id"`

	// TestName is the name of the test function.
	TestName string `json:"test_name"`

	// FilePath is the test file path.
	FilePath string `json:"file_path"`

	// CallDepth is how many calls away from the target.
	CallDepth int `json:"call_depth"`

	// CallPath is the chain of calls from test to target.
	CallPath []string `json:"call_path,omitempty"`
}

// FindTestCoverage finds tests that cover a symbol.
//
// Description:
//
//	Analyzes the code graph to find all tests that exercise a given symbol,
//	either directly or transitively through call chains.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	targetID - The symbol ID to analyze.
//
// Outputs:
//
//	*TestCoverage - Coverage results.
//	error - Non-nil if the analysis fails.
//
// Example:
//
//	coverage, err := finder.FindTestCoverage(ctx, "pkg/handlers.Handle")
//	if !coverage.IsCovered {
//	    fmt.Println("Symbol is not covered by any tests")
//	}
//
// Limitations:
//
//   - Only detects structural coverage (calls), not path coverage
//   - Does not account for conditional execution
//   - Cannot detect table-driven test coverage
func (f *TestCoverageFinder) FindTestCoverage(
	ctx context.Context,
	targetID string,
) (*TestCoverage, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startCoverageSpan(ctx, targetID)
	defer span.End()

	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if targetID == "" {
		return nil, ErrInvalidInput
	}
	if f.graph != nil && !f.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	// Find the target symbol
	symbol, found := f.index.GetByID(targetID)
	if !found || symbol == nil {
		return nil, ErrSymbolNotFound
	}

	result := &TestCoverage{
		TargetID:      targetID,
		IsCovered:     false,
		DirectTests:   make([]TestInfo, 0),
		IndirectTests: make([]TestInfo, 0),
		CoverageDepth: -1,
		Limitations:   make([]string, 0),
	}

	if f.graph == nil {
		result.Limitations = append(result.Limitations,
			"Graph not available - cannot analyze coverage")
		result.Confidence = 0.2
		return result, nil
	}

	// Find all test functions
	tests := f.findTestFunctions()
	if len(tests) == 0 {
		result.Limitations = append(result.Limitations,
			"No test functions found in index")
		result.Confidence = 0.5
		return result, nil
	}

	// For each test, check if it reaches the target
	for _, test := range tests {
		if err := ctx.Err(); err != nil {
			return result, ErrContextCanceled
		}

		depth, path := f.findCallPath(test.ID, targetID)
		if depth < 0 {
			continue // No path found
		}

		testInfo := TestInfo{
			TestID:    test.ID,
			TestName:  test.Name,
			FilePath:  test.FilePath,
			CallDepth: depth,
			CallPath:  path,
		}

		if depth == 1 {
			result.DirectTests = append(result.DirectTests, testInfo)
		} else {
			result.IndirectTests = append(result.IndirectTests, testInfo)
		}

		// Update minimum depth
		if result.CoverageDepth < 0 || depth < result.CoverageDepth {
			result.CoverageDepth = depth
		}
	}

	// Determine if covered
	result.IsCovered = len(result.DirectTests) > 0 || len(result.IndirectTests) > 0

	// Calculate confidence
	result.Confidence = f.calculateCoverageConfidence(result)

	dur := time.Since(start)
	testCount := len(result.DirectTests) + len(result.IndirectTests)
	setCoverageSpanResult(span, result.IsCovered, nil)
	recordCoverageMetrics(ctx, dur, result.IsCovered, nil)
	f.crs.RecordToolStep(ctx, "find_test_coverage", testCount, dur, nil)

	return result, nil
}

// findTestFunctions finds all test functions in the index.
func (f *TestCoverageFinder) findTestFunctions() []*ast.Symbol {
	tests := make([]*ast.Symbol, 0)

	allFuncs := f.index.GetByKind(ast.SymbolKindFunction)
	for _, fn := range allFuncs {
		if isTestFunction(fn) {
			tests = append(tests, fn)
		}
	}

	return tests
}

// findCallPath finds the call path from a source to a target.
// Returns the depth and path, or -1 and nil if no path exists.
func (f *TestCoverageFinder) findCallPath(sourceID, targetID string) (int, []string) {
	// BFS to find shortest path
	type pathEntry struct {
		nodeID string
		depth  int
		path   []string
	}

	visited := make(map[string]bool)
	queue := []pathEntry{{
		nodeID: sourceID,
		depth:  0,
		path:   []string{sourceID},
	}}

	maxDepth := 20 // Limit search depth

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current.nodeID] {
			continue
		}
		visited[current.nodeID] = true

		// Check if we reached target
		if current.nodeID == targetID && current.depth > 0 {
			return current.depth, current.path
		}

		// Don't go too deep
		if current.depth >= maxDepth {
			continue
		}

		// Get outgoing calls
		node, found := f.graph.GetNode(current.nodeID)
		if !found || node == nil {
			continue
		}

		for _, edge := range node.Outgoing {
			if edge.Type != graph.EdgeTypeCalls {
				continue
			}
			if visited[edge.ToID] {
				continue
			}

			newPath := make([]string, len(current.path)+1)
			copy(newPath, current.path)
			newPath[len(current.path)] = edge.ToID

			queue = append(queue, pathEntry{
				nodeID: edge.ToID,
				depth:  current.depth + 1,
				path:   newPath,
			})
		}
	}

	return -1, nil // No path found
}

// calculateCoverageConfidence calculates confidence for the coverage analysis.
func (f *TestCoverageFinder) calculateCoverageConfidence(coverage *TestCoverage) float64 {
	cal := NewConfidenceCalibration(0.85)

	// Reduce confidence for each limitation
	for range coverage.Limitations {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "coverage limitation",
			Multiplier: 0.85,
		})
	}

	// Increase confidence with more test coverage
	if len(coverage.DirectTests) > 2 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "multiple direct tests",
			Multiplier: 1.05,
		})
	}

	// Decrease confidence if only indirect coverage
	if len(coverage.DirectTests) == 0 && len(coverage.IndirectTests) > 0 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "only indirect coverage",
			Multiplier: 0.9,
		})
	}

	// Deep call paths are less reliable
	if coverage.CoverageDepth > 5 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "deep call chain",
			Multiplier: 0.9,
		})
	}

	// Static analysis limitation
	cal.Apply(AdjustmentStaticAnalysisOnly)

	return cal.FinalScore
}

// FindUncoveredSymbols finds symbols that have no test coverage.
//
// Description:
//
//	Scans all symbols in the index and identifies those that are not
//	reached by any test function.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	kinds - Symbol kinds to check. If empty, checks functions and methods.
//
// Outputs:
//
//	[]string - IDs of uncovered symbols.
//	error - Non-nil if the analysis fails.
func (f *TestCoverageFinder) FindUncoveredSymbols(
	ctx context.Context,
	kinds []ast.SymbolKind,
) ([]string, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if f.graph != nil && !f.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	// Default to functions and methods
	if len(kinds) == 0 {
		kinds = []ast.SymbolKind{ast.SymbolKindFunction, ast.SymbolKindMethod}
	}

	uncovered := make([]string, 0)

	// Get all symbols of requested kinds
	for _, kind := range kinds {
		symbols := f.index.GetByKind(kind)

		for _, sym := range symbols {
			if err := ctx.Err(); err != nil {
				return uncovered, ErrContextCanceled
			}

			// Skip test functions themselves
			if isTestFunction(sym) {
				continue
			}

			// Skip unexported symbols (optional - could be configurable)
			if !sym.Exported && sym.Language == "go" {
				continue
			}

			coverage, err := f.FindTestCoverage(ctx, sym.ID)
			if err != nil {
				continue // Skip symbols we can't analyze
			}

			if !coverage.IsCovered {
				uncovered = append(uncovered, sym.ID)
			}
		}
	}

	return uncovered, nil
}

// FindTestsForFile finds all tests that cover symbols in a file.
//
// Description:
//
//	Identifies all test functions that exercise any symbol defined
//	in the given file.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	filePath - Path to the file to analyze.
//
// Outputs:
//
//	[]TestInfo - Tests that cover symbols in the file.
//	error - Non-nil if the analysis fails.
func (f *TestCoverageFinder) FindTestsForFile(
	ctx context.Context,
	filePath string,
) ([]TestInfo, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if filePath == "" {
		return nil, ErrInvalidInput
	}
	if f.graph != nil && !f.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	// Normalize file path
	filePath = filepath.Clean(filePath)

	// Find all symbols in the file
	allSymbols := f.index.GetByKind(ast.SymbolKindFunction)
	allSymbols = append(allSymbols, f.index.GetByKind(ast.SymbolKindMethod)...)

	fileSymbols := make([]*ast.Symbol, 0)
	for _, sym := range allSymbols {
		if normalizePath(sym.FilePath) == normalizePath(filePath) {
			fileSymbols = append(fileSymbols, sym)
		}
	}

	// Collect all tests that cover any symbol in the file
	testMap := make(map[string]TestInfo)

	for _, sym := range fileSymbols {
		if err := ctx.Err(); err != nil {
			break
		}

		coverage, err := f.FindTestCoverage(ctx, sym.ID)
		if err != nil {
			continue
		}

		for _, test := range coverage.DirectTests {
			if _, exists := testMap[test.TestID]; !exists {
				testMap[test.TestID] = test
			}
		}
		for _, test := range coverage.IndirectTests {
			if _, exists := testMap[test.TestID]; !exists {
				testMap[test.TestID] = test
			}
		}
	}

	// Convert map to slice
	tests := make([]TestInfo, 0, len(testMap))
	for _, test := range testMap {
		tests = append(tests, test)
	}

	return tests, nil
}

// CoverageReport is a summary of test coverage for a codebase.
type CoverageReport struct {
	// TotalSymbols is the total number of symbols analyzed.
	TotalSymbols int `json:"total_symbols"`

	// CoveredSymbols is the number of symbols with test coverage.
	CoveredSymbols int `json:"covered_symbols"`

	// UncoveredSymbols lists symbols without coverage.
	UncoveredSymbols []string `json:"uncovered_symbols"`

	// CoveragePercent is the percentage of covered symbols.
	CoveragePercent float64 `json:"coverage_percent"`

	// TestCount is the total number of test functions.
	TestCount int `json:"test_count"`

	// Confidence is how confident we are in the report (0.0-1.0).
	Confidence float64 `json:"confidence"`
}

// GenerateCoverageReport generates a coverage report for the codebase.
//
// Description:
//
//	Analyzes all functions and methods in the codebase and generates
//	a summary report of test coverage.
//
// Inputs:
//
//	ctx - Context for cancellation.
//
// Outputs:
//
//	*CoverageReport - The coverage report.
//	error - Non-nil if the analysis fails.
func (f *TestCoverageFinder) GenerateCoverageReport(
	ctx context.Context,
) (*CoverageReport, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, ErrContextCanceled
	}
	if f.graph != nil && !f.graph.IsFrozen() {
		return nil, ErrGraphNotReady
	}

	report := &CoverageReport{
		UncoveredSymbols: make([]string, 0),
	}

	// Count tests
	tests := f.findTestFunctions()
	report.TestCount = len(tests)

	// Get all analyzable symbols
	kinds := []ast.SymbolKind{ast.SymbolKindFunction, ast.SymbolKindMethod}
	allSymbols := make([]*ast.Symbol, 0)
	for _, kind := range kinds {
		allSymbols = append(allSymbols, f.index.GetByKind(kind)...)
	}

	// Filter out test functions
	nonTestSymbols := make([]*ast.Symbol, 0)
	for _, sym := range allSymbols {
		if !isTestFunction(sym) {
			nonTestSymbols = append(nonTestSymbols, sym)
		}
	}

	report.TotalSymbols = len(nonTestSymbols)

	// Check coverage for each
	for _, sym := range nonTestSymbols {
		if err := ctx.Err(); err != nil {
			return report, ErrContextCanceled
		}

		coverage, err := f.FindTestCoverage(ctx, sym.ID)
		if err != nil {
			continue
		}

		if coverage.IsCovered {
			report.CoveredSymbols++
		} else {
			report.UncoveredSymbols = append(report.UncoveredSymbols, sym.ID)
		}
	}

	// Calculate percentage
	if report.TotalSymbols > 0 {
		report.CoveragePercent = float64(report.CoveredSymbols) / float64(report.TotalSymbols) * 100
	}

	// Calculate confidence
	cal := NewConfidenceCalibration(0.8)
	if f.graph == nil {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "no graph available",
			Multiplier: 0.5,
		})
	}
	if report.TestCount == 0 {
		cal.Apply(ConfidenceAdjustment{
			Reason:     "no tests found",
			Multiplier: 0.6,
		})
	}
	cal.Apply(AdjustmentStaticAnalysisOnly)
	report.Confidence = cal.FinalScore

	return report, nil
}

// normalizePath normalizes a file path for comparison.
func normalizePath(path string) string {
	path = filepath.Clean(path)
	// Remove leading ./ if present
	path = strings.TrimPrefix(path, "./")
	return path
}
