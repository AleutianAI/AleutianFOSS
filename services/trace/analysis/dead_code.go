// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package analysis

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// DeadCodeDetector finds unused code in the codebase.
//
// # Description
//
// Analyzes the dependency graph to find functions, types, and constants
// that have no callers or references. Uses confidence scoring to account
// for reflection, exports, and other uncertainty sources.
//
// # Thread Safety
//
// Safe for concurrent use after construction.
type DeadCodeDetector struct {
	graph *graph.Graph
	index *index.SymbolIndex
}

// DeadCodeResult contains the results of dead code detection.
type DeadCodeResult struct {
	// UnusedFunctions are functions with no callers.
	UnusedFunctions []DeadSymbol `json:"unused_functions"`

	// UnusedTypes are types with no usages.
	UnusedTypes []DeadSymbol `json:"unused_types"`

	// UnusedConstants are constants with no references.
	UnusedConstants []DeadSymbol `json:"unused_constants"`

	// TotalDeadLines is the estimated total lines of dead code.
	TotalDeadLines int `json:"total_dead_lines"`
}

// DeadSymbol represents a potentially dead symbol.
type DeadSymbol struct {
	// ID is the unique symbol identifier.
	ID string `json:"id"`

	// Name is the symbol's name.
	Name string `json:"name"`

	// FilePath is the file containing the symbol.
	FilePath string `json:"file_path"`

	// Line is the starting line number.
	Line int `json:"line"`

	// EndLine is the ending line number (for line count estimation).
	EndLine int `json:"end_line,omitempty"`

	// Reason explains why this is considered dead.
	Reason string `json:"reason"`

	// Confidence is how confident we are (0-100).
	// Lower for exported symbols, reflection-heavy code, etc.
	Confidence int `json:"confidence"`
}

// DeadCodeReason constants explain why code is considered dead.
const (
	DeadReasonNoCallers    = "no_callers"
	DeadReasonNoReferences = "no_references"
	DeadReasonTestOnly     = "test_only"
	DeadReasonUnreachable  = "unreachable"
)

// NewDeadCodeDetector creates a new detector.
//
// # Inputs
//
//   - g: The dependency graph to analyze.
//   - idx: The symbol index for lookups.
//
// # Outputs
//
//   - *DeadCodeDetector: Ready-to-use detector.
func NewDeadCodeDetector(g *graph.Graph, idx *index.SymbolIndex) *DeadCodeDetector {
	return &DeadCodeDetector{
		graph: g,
		index: idx,
	}
}

// Detect finds dead code in the codebase.
//
// # Description
//
// Scans all symbols in the graph and identifies those with no incoming
// edges (callers/references). Special handling for:
//   - main() and init() functions (never dead)
//   - Test functions (can be dead only if no test references them)
//   - Exported symbols (lower confidence since may be used externally)
//   - Reflection-prone code (lower confidence)
//
// # Inputs
//
//   - ctx: Context for cancellation.
//
// # Outputs
//
//   - *DeadCodeResult: Detected dead code.
//   - error: Non-nil on failure.
func (d *DeadCodeDetector) Detect(ctx context.Context) (*DeadCodeResult, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}

	result := &DeadCodeResult{
		UnusedFunctions: make([]DeadSymbol, 0),
		UnusedTypes:     make([]DeadSymbol, 0),
		UnusedConstants: make([]DeadSymbol, 0),
	}

	// Build reverse index of callers
	callerIndex := d.buildCallerIndex()

	// Track total dead lines
	var totalDeadLines int

	// Check each symbol
	for _, node := range d.graph.Nodes() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		sym := node.Symbol
		if sym == nil {
			continue
		}

		// Skip symbols that are always considered "alive"
		if d.isAlwaysAlive(sym) {
			continue
		}

		// Check if symbol has any callers
		callers := callerIndex[sym.ID]
		if len(callers) > 0 {
			continue
		}

		// Calculate confidence
		confidence := d.calculateConfidence(sym)

		// Skip if confidence too low
		if confidence < 20 {
			continue
		}

		dead := DeadSymbol{
			ID:         sym.ID,
			Name:       sym.Name,
			FilePath:   sym.FilePath,
			Line:       sym.StartLine,
			EndLine:    sym.EndLine,
			Reason:     d.determineReason(sym, callerIndex),
			Confidence: confidence,
		}

		// Estimate lines
		lines := 1
		if sym.EndLine > sym.StartLine {
			lines = sym.EndLine - sym.StartLine + 1
		}
		totalDeadLines += lines

		// Categorize by type
		switch sym.Kind {
		case ast.SymbolKindFunction, ast.SymbolKindMethod:
			result.UnusedFunctions = append(result.UnusedFunctions, dead)
		case ast.SymbolKindStruct, ast.SymbolKindInterface:
			result.UnusedTypes = append(result.UnusedTypes, dead)
		case ast.SymbolKindConstant, ast.SymbolKindVariable:
			result.UnusedConstants = append(result.UnusedConstants, dead)
		}
	}

	result.TotalDeadLines = totalDeadLines
	return result, nil
}

// buildCallerIndex creates a reverse index of symbol -> callers.
func (d *DeadCodeDetector) buildCallerIndex() map[string][]string {
	callerIndex := make(map[string][]string)

	edges := d.graph.Edges()
	for _, edge := range edges {
		// For CALLS edges, from = caller, to = callee
		if edge.Type == graph.EdgeTypeCalls ||
			edge.Type == graph.EdgeTypeReferences ||
			edge.Type == graph.EdgeTypeImplements {
			callerIndex[edge.ToID] = append(callerIndex[edge.ToID], edge.FromID)
		}
	}

	return callerIndex
}

// isAlwaysAlive returns true for symbols that are never considered dead.
//
// IT-08 H-1: Extended from Go-only to cross-language entry point detection.
func (d *DeadCodeDetector) isAlwaysAlive(sym *ast.Symbol) bool {
	name := sym.Name
	lang := strings.ToLower(sym.Language)

	// === Cross-language entry points ===

	if name == "main" {
		return true
	}

	// === Go entry points ===

	if lang == "" || lang == "go" {
		if name == "init" {
			return true
		}
		if strings.HasPrefix(name, "Test") ||
			strings.HasPrefix(name, "Benchmark") ||
			strings.HasPrefix(name, "Example") ||
			strings.HasPrefix(name, "Fuzz") {
			return true
		}
		if sym.Kind == ast.SymbolKindMethod && name == "ServeHTTP" {
			return true
		}
	}

	// === Python entry points ===

	if lang == "python" {
		if name == "__main__" {
			return true
		}
		if sym.FilePath != "" && filepath.Base(sym.FilePath) == "__main__.py" {
			return true
		}
		if strings.HasPrefix(name, "test_") {
			return true
		}
		if name == "setUp" || name == "tearDown" || name == "setUpClass" || name == "tearDownClass" {
			return true
		}
		if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
			for _, dec := range sym.Metadata.Decorators {
				dl := strings.ToLower(dec)
				if strings.Contains(dl, "route") || strings.Contains(dl, ".get") ||
					strings.Contains(dl, ".post") || strings.Contains(dl, ".put") ||
					strings.Contains(dl, ".delete") || strings.Contains(dl, ".patch") ||
					strings.Contains(dl, "click.command") || strings.Contains(dl, "click.group") ||
					strings.Contains(dl, "fixture") || strings.Contains(dl, ".task") {
					return true
				}
			}
		}
		if sym.Kind == ast.SymbolKindClass && sym.Metadata != nil &&
			strings.HasSuffix(sym.Metadata.Extends, "TestCase") {
			return true
		}
	}

	// === JavaScript / TypeScript entry points ===

	if lang == "javascript" || lang == "typescript" {
		switch name {
		case "it", "test", "describe", "beforeEach", "afterEach",
			"beforeAll", "afterAll", "before", "after":
			return true
		}
		if sym.Exported {
			return true
		}
		if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
			for _, dec := range sym.Metadata.Decorators {
				switch dec {
				case "Controller", "Get", "Post", "Put", "Delete", "Patch",
					"Injectable", "Module", "Guard", "Interceptor", "Pipe":
					return true
				}
			}
		}
		switch name {
		case "getServerSideProps", "getStaticProps", "getStaticPaths",
			"generateStaticParams", "generateMetadata":
			return true
		}
	}

	// Interface methods may be called via interface
	if sym.Kind == ast.SymbolKindMethod {
		node, ok := d.graph.GetNode(sym.ID)
		if ok {
			for _, edge := range node.Outgoing {
				if edge.Type == graph.EdgeTypeImplements {
					return true
				}
			}
		}
	}

	return false
}

// calculateConfidence determines how confident we are this is dead code.
func (d *DeadCodeDetector) calculateConfidence(sym *ast.Symbol) int {
	confidence := 100

	// Exported symbols may be used externally - reduce confidence
	if isExported(sym.Name) {
		confidence -= 30
	}

	// Check for reflection indicators
	if d.hasReflectionIndicators(sym) {
		confidence -= 25
	}

	// Methods have lower confidence (may be called via interface)
	if sym.Kind == ast.SymbolKindMethod {
		confidence -= 15
	}

	// Callback patterns have lower confidence
	if d.isLikelyCallback(sym) {
		confidence -= 20
	}

	// Types used in struct fields may be serialized
	if sym.Kind == ast.SymbolKindStruct {
		confidence -= 10
	}

	// Clamp to reasonable range
	if confidence < 0 {
		confidence = 0
	}

	return confidence
}

// isExported checks if a name is exported (starts with uppercase).
func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

// hasReflectionIndicators checks if symbol might be used via reflection.
func (d *DeadCodeDetector) hasReflectionIndicators(sym *ast.Symbol) bool {
	// Check file content for reflection imports
	// This is a heuristic - look for common reflection patterns in filename
	fp := strings.ToLower(sym.FilePath)
	if strings.Contains(fp, "reflect") ||
		strings.Contains(fp, "marshal") ||
		strings.Contains(fp, "codec") ||
		strings.Contains(fp, "json") ||
		strings.Contains(fp, "yaml") {
		return true
	}

	// Check if symbol has decorators (common serialization pattern)
	if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
		return true
	}

	return false
}

// isLikelyCallback checks if symbol looks like a callback function.
func (d *DeadCodeDetector) isLikelyCallback(sym *ast.Symbol) bool {
	name := strings.ToLower(sym.Name)

	// Common callback naming patterns
	callbackPatterns := []string{
		"handler", "callback", "hook",
		"listener", "observer", "subscriber",
		"onfoo", "handlefoo", "processfoo",
	}

	for _, pattern := range callbackPatterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}

	// Check if registered as handler somewhere
	// This would require more context about the registration pattern

	return false
}

// determineReason figures out why the symbol is considered dead.
func (d *DeadCodeDetector) determineReason(sym *ast.Symbol, callerIndex map[string][]string) string {
	// Check if only called from tests
	callers := callerIndex[sym.ID]
	if len(callers) > 0 {
		allTest := true
		for _, callerID := range callers {
			if node, ok := d.graph.GetNode(callerID); ok && node.Symbol != nil {
				if !strings.Contains(node.Symbol.FilePath, "_test.go") {
					allTest = false
					break
				}
			}
		}
		if allTest {
			return DeadReasonTestOnly
		}
	}

	// Default reason based on symbol type
	switch sym.Kind {
	case ast.SymbolKindFunction, ast.SymbolKindMethod:
		return DeadReasonNoCallers
	default:
		return DeadReasonNoReferences
	}
}

// DeadCodeDetectorOption configures the detector.
type DeadCodeDetectorOption func(*deadCodeOptions)

type deadCodeOptions struct {
	minConfidence     int
	excludePatterns   []string
	includeTestOnly   bool
	excludeInterfaces bool
}

// WithMinConfidence sets the minimum confidence threshold.
func WithMinConfidence(min int) DeadCodeDetectorOption {
	return func(o *deadCodeOptions) {
		o.minConfidence = min
	}
}

// WithExcludePatterns sets patterns to exclude from detection.
func WithExcludePatterns(patterns []string) DeadCodeDetectorOption {
	return func(o *deadCodeOptions) {
		o.excludePatterns = patterns
	}
}

// DeadCodeEnricher implements Enricher for dead code detection.
type DeadCodeEnricher struct {
	detector *DeadCodeDetector
	mu       sync.RWMutex
	cache    *DeadCodeResult
}

// NewDeadCodeEnricher creates an enricher for dead code detection.
func NewDeadCodeEnricher(g *graph.Graph, idx *index.SymbolIndex) *DeadCodeEnricher {
	return &DeadCodeEnricher{
		detector: NewDeadCodeDetector(g, idx),
	}
}

// Name returns the enricher name.
func (e *DeadCodeEnricher) Name() string {
	return "dead_code"
}

// Priority returns execution priority (run after basic analysis).
func (e *DeadCodeEnricher) Priority() int {
	return 2
}

// Enrich adds dead code information to the blast radius.
func (e *DeadCodeEnricher) Enrich(ctx context.Context, target *EnrichmentTarget, result *EnhancedBlastRadius) error {
	if ctx == nil {
		return ErrNilContext
	}

	// Get or compute dead code result (cached)
	e.mu.RLock()
	cached := e.cache
	e.mu.RUnlock()

	if cached == nil {
		detected, err := e.detector.Detect(ctx)
		if err != nil {
			return err
		}
		e.mu.Lock()
		e.cache = detected
		cached = detected
		e.mu.Unlock()
	}

	// Check if target symbol is dead
	for _, dead := range cached.UnusedFunctions {
		if dead.ID == target.SymbolID {
			result.DeadCode = &DeadCodeInfo{
				IsDead:     true,
				Reason:     dead.Reason,
				Confidence: dead.Confidence,
			}
			return nil
		}
	}

	for _, dead := range cached.UnusedTypes {
		if dead.ID == target.SymbolID {
			result.DeadCode = &DeadCodeInfo{
				IsDead:     true,
				Reason:     dead.Reason,
				Confidence: dead.Confidence,
			}
			return nil
		}
	}

	for _, dead := range cached.UnusedConstants {
		if dead.ID == target.SymbolID {
			result.DeadCode = &DeadCodeInfo{
				IsDead:     true,
				Reason:     dead.Reason,
				Confidence: dead.Confidence,
			}
			return nil
		}
	}

	// Check if any callers are dead
	deadCallerCount := 0
	for _, caller := range result.DirectCallers {
		for _, dead := range cached.UnusedFunctions {
			if dead.ID == caller.ID {
				deadCallerCount++
				break
			}
		}
	}

	result.DeadCode = &DeadCodeInfo{
		IsDead:            false,
		DeadCallerCount:   deadCallerCount,
		TotalDeadLines:    cached.TotalDeadLines,
		DeadFunctionCount: len(cached.UnusedFunctions),
		DeadTypeCount:     len(cached.UnusedTypes),
	}

	return nil
}

// DeadCodeInfo contains dead code analysis for a symbol.
type DeadCodeInfo struct {
	// IsDead indicates if the target symbol is dead.
	IsDead bool `json:"is_dead"`

	// Reason for being dead (if IsDead is true).
	Reason string `json:"reason,omitempty"`

	// Confidence in the dead code detection (0-100).
	Confidence int `json:"confidence,omitempty"`

	// DeadCallerCount is how many callers are themselves dead.
	DeadCallerCount int `json:"dead_caller_count,omitempty"`

	// TotalDeadLines is total dead lines in the codebase.
	TotalDeadLines int `json:"total_dead_lines,omitempty"`

	// DeadFunctionCount is total dead functions.
	DeadFunctionCount int `json:"dead_function_count,omitempty"`

	// DeadTypeCount is total dead types.
	DeadTypeCount int `json:"dead_type_count,omitempty"`
}

// Invalidate clears the cached dead code result.
func (e *DeadCodeEnricher) Invalidate() {
	e.mu.Lock()
	e.cache = nil
	e.mu.Unlock()
}
