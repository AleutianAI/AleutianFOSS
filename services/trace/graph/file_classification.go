// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// GR-60: Graph-Based File Classification
// =============================================================================

// FileClassificationOptions configures the file classification algorithm.
type FileClassificationOptions struct {
	// ProjectRoot is the absolute path to the project root.
	// Used to locate trace.config.yaml for user overrides.
	ProjectRoot string
}

// FileClassificationStats contains summary statistics for logging.
//
// Partition invariant: TotalFiles == ProductionFiles + NonProductionFiles.
// IsolatedFiles is a subset of ProductionFiles (isolated → production).
// LikelyConsumerFiles is a diagnostic counter that overlaps with
// ProductionFiles or NonProductionFiles after Phase 4 resolves them.
type FileClassificationStats struct {
	// TotalFiles is the total number of files classified.
	// Invariant: TotalFiles == ProductionFiles + NonProductionFiles.
	TotalFiles int

	// ProductionFiles is the count of files classified as production.
	// Includes IsolatedFiles (isolated files are treated as production).
	ProductionFiles int

	// NonProductionFiles is the count of files classified as non-production.
	NonProductionFiles int

	// IsolatedFiles is the count of files with zero cross-file edges.
	// These are classified as production (benefit of the doubt).
	// IsolatedFiles is a subset of ProductionFiles.
	IsolatedFiles int

	// LikelyConsumerFiles is a diagnostic counter: files with consumption ratio
	// in [0.05, 0.15) that required entry point reinforcement (Phase 4).
	// Each is resolved into ProductionFiles or NonProductionFiles.
	// This is NOT a separate partition — it overlaps with Prod/NonProd counts.
	LikelyConsumerFiles int
}

// FileClassification holds the classification of all files in the graph.
//
// Description:
//
//	Stores a binary production/non-production classification for each file
//	based on graph topology (consumption ratio). Non-production files are
//	those that primarily consume production code (test, example, benchmark,
//	documentation code) without being consumed back.
//
// Thread Safety:
//
//	FileClassification is safe for concurrent reads after construction.
//	The internal map is never mutated after ClassifyFiles returns.
type FileClassification struct {
	// files maps file path to true (production) or false (non-production).
	files map[string]bool

	// stats contains summary statistics.
	stats FileClassificationStats
}

// IsProduction returns true if the file is classified as production code.
//
// Description:
//
//	O(1) map lookup. Files not present in the classification map are treated
//	as production (conservative default — unknown files are not filtered).
//
// Inputs:
//
//	filePath - The file path to check.
//
// Outputs:
//
//	bool - True if the file is production code or unknown.
//
// Thread Safety: Safe for concurrent use.
func (fc *FileClassification) IsProduction(filePath string) bool {
	if fc == nil || fc.files == nil {
		return true
	}
	isProd, exists := fc.files[filePath]
	if !exists {
		return true // Unknown files are treated as production
	}
	return isProd
}

// Stats returns summary statistics about the classification.
//
// Outputs:
//
//	FileClassificationStats - Copy of the statistics.
//
// Thread Safety: Safe for concurrent use.
func (fc *FileClassification) Stats() FileClassificationStats {
	if fc == nil {
		return FileClassificationStats{}
	}
	return fc.stats
}

// ClassifyFiles classifies all files in the graph as production or non-production
// using a layered approach: graph topology, language-specific file patterns,
// test symbol density, and iterative ratio refinement.
//
// Description:
//
//	The core insight: test code calls production code, but production code
//	never calls test code. This is measurable as the "consumption ratio":
//
//	  edges_in  = cross-file edges pointing INTO this file
//	  edges_out = cross-file edges pointing OUT OF this file
//	  ratio     = edges_in / (edges_in + edges_out)
//
//	A file that only consumes (test/example) has ratio ≈ 0 (many edges out, few in).
//	A file that only produces (core library) has ratio ≈ 1 (many edges in, few out).
//
//	However, the raw ratio alone misclassifies test infrastructure files —
//	files like integrationtest_builder.go that are called by many test files.
//	These have high edges_in (from tests calling them) and look like production.
//
//	The algorithm uses 7 phases to handle this:
//	  Phase 1: Load config overrides
//	  Phase 2: Group nodes by file
//	  Phase 3: Initial consumption ratio (classifies obvious cases)
//	  Phase 4: Definitive test file patterns (Go _test.go, Python test_*.py, etc.)
//	  Phase 5: Entry point reinforcement + test symbol keyword density
//	  Phase 6: Iterative ratio refinement (re-compute using only production edges)
//	  Phase 7: Config overrides (user always wins)
//
// Inputs:
//
//	hg - The hierarchical graph to classify. Must not be nil and must be frozen.
//	opts - Classification options (project root for config file).
//
// Outputs:
//
//	*FileClassification - The classification result. Never nil on success.
//	error - Non-nil if the graph is nil or not frozen.
//
// Thread Safety: Safe for concurrent use (read-only on the graph).
//
// Limitations:
//
//   - File-level classification (not directory-level).
//   - Binary production/non-production (no test vs example vs docs distinction).
//   - Computed at graph freeze time; does not update dynamically.
//
// Assumptions:
//
//   - Graph is frozen and fully indexed before calling.
//   - Edge topology accurately reflects code dependencies.
func ClassifyFiles(hg *HierarchicalGraph, opts FileClassificationOptions) (*FileClassification, error) {
	if hg == nil {
		return nil, ErrNilGraph
	}
	if !hg.IsFrozen() {
		return nil, ErrGraphNotFrozen
	}

	fc := &FileClassification{
		files: make(map[string]bool),
	}

	// Phase 1: Load trace.config.yaml for user overrides.
	// On error (permission denied, invalid YAML), continue with empty config.
	config, configErr := loadTraceConfig(opts.ProjectRoot)
	if configErr != nil {
		slog.Warn("GR-60: trace.config.yaml error, using defaults",
			slog.String("error", configErr.Error()),
			slog.String("project_root", opts.ProjectRoot),
		)
	}

	// Phase 2: Group nodes by file path using the hierarchical graph's file index
	allFiles := make(map[string][]*Node)
	for _, node := range hg.Nodes() {
		if node.Symbol == nil || node.Symbol.FilePath == "" {
			continue
		}
		fp := node.Symbol.FilePath
		allFiles[fp] = append(allFiles[fp], node)
	}

	// Phase 3: Compute initial consumption ratio for each file.
	// This classifies obvious cases: pure consumers (ratio < 0.05) and
	// clear production code (ratio >= 0.15). Borderline cases are deferred.
	likelyConsumers := make(map[string]bool)

	for filePath, nodes := range allFiles {
		fc.stats.TotalFiles++

		edgesIn, edgesOut := computeFileRatio(nodes, hg, filePath)
		total := edgesIn + edgesOut

		if total == 0 {
			// Isolated files: production (benefit of the doubt)
			fc.files[filePath] = true
			fc.stats.IsolatedFiles++
			fc.stats.ProductionFiles++
			continue
		}

		ratio := float64(edgesIn) / float64(total)

		if ratio < 0.05 {
			// Pure consumer: non-production
			fc.files[filePath] = false
			fc.stats.NonProductionFiles++
		} else if ratio < 0.15 {
			// Likely consumer: deferred to Phase 5
			likelyConsumers[filePath] = true
			fc.stats.LikelyConsumerFiles++
		} else {
			// Tentatively production (may be reclassified in Phase 4 or 6)
			fc.files[filePath] = true
			fc.stats.ProductionFiles++
		}
	}

	// Phase 4: Definitive test file patterns.
	// Language-specific file naming conventions are strong signals that override
	// the consumption ratio. Go _test.go is enforced by the compiler. Python
	// test_*.py and JS/TS *.test.ts / *.spec.ts are near-universal conventions.
	phase4Reclassified := 0
	for filePath := range allFiles {
		// Only reclassify files currently marked as production
		if isProd, exists := fc.files[filePath]; !exists || !isProd {
			continue
		}
		if isDefinitiveTestFile(filePath) {
			fc.files[filePath] = false
			fc.stats.ProductionFiles--
			fc.stats.NonProductionFiles++
			phase4Reclassified++
		}
	}
	if phase4Reclassified > 0 {
		slog.Info("GR-60: Phase 4 definitive test patterns reclassified files",
			slog.Int("count", phase4Reclassified),
		)
	}

	// Phase 5: Entry point reinforcement + test symbol keyword density.
	// For likely-consumer files (ratio 0.05–0.15) and production files with
	// test-heavy symbol names, check if the file's symbols indicate test code.
	// Two checks:
	//   (a) >50% test entry points → non-production (original Phase 4 logic)
	//   (b) >50% symbols have test-related keywords → non-production
	// Files resolved as production here are exempt from Phase 6 iterative
	// refinement — they already had a borderline ratio and were given benefit
	// of the doubt after symbol analysis.
	phase5Exempt := make(map[string]bool) // files resolved as production by Phase 5
	for filePath := range likelyConsumers {
		nodes := allFiles[filePath]
		if classifyBySymbolAnalysis(nodes) {
			fc.files[filePath] = false
			fc.stats.NonProductionFiles++
		} else {
			fc.files[filePath] = true
			fc.stats.ProductionFiles++
			phase5Exempt[filePath] = true
		}
	}

	// Also check production files for test keyword density — catches test
	// infrastructure files that passed the ratio check but are full of
	// test-related symbols (e.g., Assert*, Mock*, setup helpers).
	phase5Reclassified := 0
	for filePath := range allFiles {
		if isProd, exists := fc.files[filePath]; !exists || !isProd {
			continue
		}
		nodes := allFiles[filePath]
		if hasHighTestSymbolDensity(nodes) {
			fc.files[filePath] = false
			fc.stats.ProductionFiles--
			fc.stats.NonProductionFiles++
			phase5Reclassified++
		}
	}
	if phase5Reclassified > 0 {
		slog.Info("GR-60: Phase 5 test keyword density reclassified files",
			slog.Int("count", phase5Reclassified),
		)
	}

	// Phase 6: Iterative ratio refinement.
	// Re-compute the consumption ratio but only counting edges from/to files
	// currently classified as production. Test infrastructure files like
	// integrationtest_builder.go have high ratio because they're called by
	// many test files — once those callers are classified as non-production,
	// the "effective" ratio drops and the file can be reclassified.
	// Iterate until stable (typically converges in 2–3 passes, capped at 5).
	for pass := 0; pass < 5; pass++ {
		reclassified := 0
		for filePath, nodes := range allFiles {
			isProd, exists := fc.files[filePath]
			if !exists || !isProd {
				continue // Only re-examine production files
			}
			if phase5Exempt[filePath] {
				continue // Resolved as production by Phase 5 entry point analysis
			}

			prodIn, prodOut := computeProductionFileRatio(nodes, hg, filePath, fc)
			total := prodIn + prodOut
			if total == 0 {
				// All edges are from/to non-production files → reclassify
				// But only if the file had edges originally (not isolated)
				origIn, origOut := computeFileRatio(nodes, hg, filePath)
				if origIn+origOut > 0 {
					fc.files[filePath] = false
					fc.stats.ProductionFiles--
					fc.stats.NonProductionFiles++
					reclassified++
				}
				continue
			}

			ratio := float64(prodIn) / float64(total)

			// GR-60c: Caller purity check. If a file originally had many
			// incoming edges but < 10% survive after removing non-production
			// callers, the file is test infrastructure regardless of its
			// outgoing edges. Example: Hugo's integrationtest_builder.go has
			// 3193 incoming edges from _test.go files, only 136 from prod
			// (4.3%) — but its 115 outgoing edges to production code keep
			// the ratio at 0.54. The caller purity check catches this.
			origIn, origOut := computeFileRatio(nodes, hg, filePath)
			callerPurityFailed := origIn > 20 && float64(prodIn)/float64(origIn) < 0.10

			if ratio < 0.10 || callerPurityFailed {
				// When counting only production edges, this file is a consumer
				fc.files[filePath] = false
				fc.stats.ProductionFiles--
				fc.stats.NonProductionFiles++
				reclassified++
				if callerPurityFailed && ratio >= 0.10 {
					slog.Info("GR-60c: Phase 6 caller purity reclassified file",
						slog.String("file", filePath),
						slog.Int("prod_in", prodIn),
						slog.Int("orig_in", origIn),
						slog.Float64("caller_purity", float64(prodIn)/float64(origIn)),
						slog.Float64("prod_ratio", ratio),
					)
				}
			} else if pass == 0 && strings.Contains(strings.ToLower(filePath), "test") {
				// GR-60c diagnostic: log test-path files that survive Phase 6
				slog.Info("GR-60c: Phase 6 kept test-path file as production",
					slog.String("file", filePath),
					slog.Int("prod_in", prodIn),
					slog.Int("prod_out", prodOut),
					slog.Float64("prod_ratio", ratio),
					slog.Int("orig_in", origIn),
					slog.Int("orig_out", origOut),
				)
			}
		}

		if reclassified == 0 {
			slog.Info("GR-60: Phase 6 iterative refinement converged",
				slog.Int("passes", pass+1),
			)
			break
		}
		slog.Info("GR-60: Phase 6 iterative refinement pass",
			slog.Int("pass", pass+1),
			slog.Int("reclassified", reclassified),
		)
	}

	// Phase 7: Apply config overrides (single pass, include takes precedence over exclude).
	// User overrides always win — applied last so no algorithm phase can undo them.
	overrides := make(map[string]bool) // filePath → true=force-prod, false=force-nonprod
	for filePath := range allFiles {
		for _, prefix := range config.ExcludeFromAnalysis {
			if strings.HasPrefix(filePath, prefix) {
				overrides[filePath] = false
				break
			}
		}
		// Include takes precedence: if file matches both exclude and include,
		// include wins. E.g., exclude "vendor/" + include "vendor/special/" →
		// "vendor/special/lib.go" is production.
		for _, prefix := range config.IncludeOverride {
			if strings.HasPrefix(filePath, prefix) {
				overrides[filePath] = true
				break
			}
		}
	}
	for filePath, forceProd := range overrides {
		wasProd := fc.files[filePath]
		fc.files[filePath] = forceProd
		if wasProd && !forceProd {
			fc.stats.ProductionFiles--
			fc.stats.NonProductionFiles++
		} else if !wasProd && forceProd {
			fc.stats.NonProductionFiles--
			fc.stats.ProductionFiles++
		}
	}

	return fc, nil
}

// isDefinitiveTestFile returns true if the file path matches a language-specific
// test file naming convention that is unambiguous.
//
// Description:
//
//	These are patterns enforced by language toolchains or near-universal conventions:
//	  Go:     *_test.go (compiler-enforced, excluded from production builds)
//	  Python: test_*.py, *_test.py, conftest.py (pytest convention)
//	  JS/TS:  *.test.{js,ts,jsx,tsx,mjs,cjs}, *.spec.{js,ts,jsx,tsx,mjs,cjs}
//	  Any:    files in definitive test directories (quicktests/, __tests__/, etc.)
//
// Inputs:
//
//	filePath - The file path to check.
//
// Outputs:
//
//	bool - True if the file is definitively a test file by naming convention.
//
// Thread Safety: Safe for concurrent use (pure function).
func isDefinitiveTestFile(filePath string) bool {
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	nameNoExt := strings.TrimSuffix(base, ext)
	lower := strings.ToLower(filePath)

	// === Go: _test.go is compiler-enforced ===
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// === Python: test_*.py, *_test.py, conftest.py ===
	if ext == ".py" || ext == ".pyi" {
		lowerName := strings.ToLower(nameNoExt)
		if strings.HasPrefix(lowerName, "test_") || strings.HasSuffix(lowerName, "_test") {
			return true
		}
		if lowerName == "conftest" {
			return true
		}
	}

	// === JS/TS: *.test.*, *.spec.* ===
	if isJSOrTSExt(ext) {
		// Check for double extension: foo.test.ts, foo.spec.js
		if strings.Contains(nameNoExt, ".test") || strings.Contains(nameNoExt, ".spec") {
			return true
		}
	}

	// === Definitive test directories ===
	// These are directories whose SOLE purpose is test/example code.
	for _, dir := range []string{
		"__tests__/", "__fixtures__/", "__mocks__/",
		"quicktests/", "e2e/", "cypress/",
		"integration/",
	} {
		if strings.Contains(lower, "/"+dir) || strings.HasPrefix(lower, dir) {
			return true
		}
	}

	return false
}

// isJSOrTSExt returns true if the extension is a JavaScript or TypeScript extension.
func isJSOrTSExt(ext string) bool {
	switch ext {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

// classifyBySymbolAnalysis returns true (non-production) if the file's symbols
// indicate test code via entry point analysis.
//
// Description:
//
//	Checks if >50% of the file's symbols are test entry points (Test*, test_*,
//	describe, it, etc.). Used for likely-consumer files (ratio 0.05–0.15).
//
// Inputs:
//
//	nodes - All graph nodes in the file.
//
// Outputs:
//
//	bool - True if the file should be classified as non-production.
//
// Thread Safety: Safe for concurrent use (read-only).
func classifyBySymbolAnalysis(nodes []*Node) bool {
	testEntryCount := 0
	totalSymbols := len(nodes)

	for _, node := range nodes {
		if node.Symbol == nil {
			totalSymbols--
			continue
		}
		if isTestEntryPoint(node.Symbol) {
			testEntryCount++
		}
	}

	return totalSymbols > 0 && float64(testEntryCount)/float64(totalSymbols) > 0.50
}

// hasHighTestSymbolDensity returns true if a file's symbols are dominated by
// test-related keywords, indicating test infrastructure even if the consumption
// ratio is high.
//
// Description:
//
//	Checks symbol names for test-related keywords: Assert, Test, Mock, Stub,
//	Fixture, Setup, Teardown, Benchmark, Expect, Verify, Fake, Spy. This
//	catches test utility files like integrationtest_builder.go where the
//	functions (AssertFileContent, AssertFileContentExact) are test helpers
//	but the file has a high consumption ratio because many _test.go files
//	call into it.
//
//	Threshold: >60% of symbols contain test keywords → non-production.
//	The threshold is deliberately higher than the entry point check (50%)
//	because keyword matching is fuzzier than entry point pattern matching.
//
// Inputs:
//
//	nodes - All graph nodes in the file.
//
// Outputs:
//
//	bool - True if the file has high test keyword density.
//
// Thread Safety: Safe for concurrent use (read-only).
func hasHighTestSymbolDensity(nodes []*Node) bool {
	testKeywordCount := 0
	totalSymbols := 0

	for _, node := range nodes {
		if node.Symbol == nil {
			continue
		}
		totalSymbols++
		if hasTestKeyword(node.Symbol.Name) {
			testKeywordCount++
		}
	}

	// Need at least 3 symbols to avoid false positives on tiny files
	return totalSymbols >= 3 && float64(testKeywordCount)/float64(totalSymbols) > 0.60
}

// hasTestKeyword returns true if a symbol name contains a test-related keyword.
//
// Description:
//
//	Case-insensitive check for keywords that indicate test infrastructure:
//	Assert, Test, Mock, Stub, Fixture, Setup, Teardown, Benchmark,
//	Expect, Verify, Fake, Spy. Uses case-insensitive substring matching.
//
// Inputs:
//
//	name - The symbol name to check.
//
// Outputs:
//
//	bool - True if the name contains a test keyword.
//
// Thread Safety: Safe for concurrent use (pure function).
func hasTestKeyword(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range testKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// testKeywords are lowercase substrings that indicate test infrastructure.
var testKeywords = []string{
	"assert", "test", "mock", "stub", "fixture",
	"setup", "teardown", "benchmark", "expect",
	"verify", "fake", "spy",
}

// computeProductionFileRatio counts cross-file edges but only counting edges
// from/to files currently classified as production.
//
// Description:
//
//	Like computeFileRatio but filters: only counts an edge if the other file
//	is currently classified as production in the FileClassification. This
//	enables iterative refinement — once test files are classified as
//	non-production, their edges no longer inflate the ratio of test
//	infrastructure files they call into.
//
// Inputs:
//
//	nodes - All nodes in the file.
//	hg - The hierarchical graph for node lookups.
//	filePath - The file path being analyzed.
//	fc - Current classification state for production/non-production checks.
//
// Outputs:
//
//	prodIn - Count of cross-file incoming edges from production files.
//	prodOut - Count of cross-file outgoing edges to production files.
//
// Thread Safety: Safe for concurrent use (read-only).
func computeProductionFileRatio(nodes []*Node, hg *HierarchicalGraph, filePath string, fc *FileClassification) (prodIn, prodOut int) {
	for _, node := range nodes {
		// Count incoming edges from production files only
		for _, edge := range node.Incoming {
			fromNode, ok := hg.GetNode(edge.FromID)
			if !ok || fromNode.Symbol == nil || fromNode.Symbol.FilePath == "" {
				continue
			}
			otherFile := fromNode.Symbol.FilePath
			if otherFile != filePath && fc.IsProduction(otherFile) {
				prodIn++
			}
		}

		// Count outgoing edges to production files only
		for _, edge := range node.Outgoing {
			toNode, ok := hg.GetNode(edge.ToID)
			if !ok || toNode.Symbol == nil || toNode.Symbol.FilePath == "" {
				continue
			}
			otherFile := toNode.Symbol.FilePath
			if otherFile != filePath && fc.IsProduction(otherFile) {
				prodOut++
			}
		}
	}
	return prodIn, prodOut
}

// computeFileRatio counts cross-file incoming and outgoing edges for a file.
//
// Description:
//
//	For each node in the file, iterates its incoming and outgoing edges.
//	Only counts edges that cross file boundaries (source file != target file).
//	Skips edges where the other node has no Symbol (external placeholders).
//
// Inputs:
//
//	nodes - All nodes in the file.
//	hg - The hierarchical graph for node lookups.
//	filePath - The file path being analyzed.
//
// Outputs:
//
//	edgesIn - Count of cross-file incoming edges.
//	edgesOut - Count of cross-file outgoing edges.
//
// Thread Safety: Safe for concurrent use (read-only).
func computeFileRatio(nodes []*Node, hg *HierarchicalGraph, filePath string) (edgesIn, edgesOut int) {
	for _, node := range nodes {
		// Count incoming edges from other files
		for _, edge := range node.Incoming {
			fromNode, ok := hg.GetNode(edge.FromID)
			if !ok || fromNode.Symbol == nil || fromNode.Symbol.FilePath == "" {
				continue // Skip unresolved nodes and external placeholders
			}
			if fromNode.Symbol.FilePath != filePath {
				edgesIn++
			}
		}

		// Count outgoing edges to other files
		for _, edge := range node.Outgoing {
			toNode, ok := hg.GetNode(edge.ToID)
			if !ok || toNode.Symbol == nil || toNode.Symbol.FilePath == "" {
				continue // Skip unresolved nodes and external placeholders
			}
			if toNode.Symbol.FilePath != filePath {
				edgesOut++
			}
		}
	}
	return edgesIn, edgesOut
}

// isTestEntryPoint checks if a symbol is a test runner entry point.
//
// Description:
//
//	Narrower than the existing isEntryPoint() in analytics.go. This only
//	matches symbols that are invoked by test runners, NOT general entry
//	points like init(), main(), or ServeHTTP(). The distinction matters
//	because init() and ServeHTTP() are production entry points that should
//	not cause a file to be classified as non-production.
//
//	Patterns detected:
//	  Go:     Test*, Benchmark*, Example*, Fuzz*
//	  Python: test_*, setUp, tearDown, setUpClass, tearDownClass, TestCase subclasses,
//	          pytest fixtures
//	  JS/TS:  it, test, describe, beforeEach, afterEach, beforeAll, afterAll, before, after
//
// Inputs:
//
//	sym - The symbol to check. Must not be nil.
//
// Outputs:
//
//	bool - True if the symbol is a test entry point.
//
// Thread Safety: Safe for concurrent use (pure function).
func isTestEntryPoint(sym *ast.Symbol) bool {
	if sym == nil {
		return false
	}

	name := sym.Name
	lang := strings.ToLower(sym.Language)

	// GR-60b: Infer language from file extension when sym.Language is empty.
	// Without this, a Python function named "TestParseConfig" would falsely
	// match Go patterns (Go uses Test*, Python uses test_*).
	if lang == "" {
		lang = inferLanguageFromPath(sym.FilePath)
	}

	// === Go test entry points ===
	if lang == "go" {
		if len(name) > 4 && name[:4] == "Test" {
			return true
		}
		if len(name) > 9 && name[:9] == "Benchmark" {
			return true
		}
		if len(name) > 7 && name[:7] == "Example" {
			return true
		}
		if len(name) > 4 && name[:4] == "Fuzz" {
			return true
		}
	}

	// === Python test entry points ===
	if lang == "python" {
		if strings.HasPrefix(name, "test_") {
			return true
		}
		switch name {
		case "setUp", "tearDown", "setUpClass", "tearDownClass":
			return true
		}
		// pytest fixtures
		if sym.Metadata != nil && len(sym.Metadata.Decorators) > 0 {
			for _, dec := range sym.Metadata.Decorators {
				if strings.Contains(strings.ToLower(dec), "fixture") {
					return true
				}
			}
		}
		// TestCase subclasses
		if sym.Kind == ast.SymbolKindClass && sym.Metadata != nil && sym.Metadata.Extends != "" {
			if strings.HasSuffix(sym.Metadata.Extends, "TestCase") {
				return true
			}
		}
	}

	// === JavaScript / TypeScript test entry points ===
	if lang == "javascript" || lang == "typescript" {
		switch name {
		case "it", "test", "describe", "beforeEach", "afterEach",
			"beforeAll", "afterAll", "before", "after":
			return true
		}
	}

	return false
}

// inferLanguageFromPath detects language from file extension.
//
// Description:
//
//	Local copy of the language detection logic for use in the graph package.
//	The graph package cannot import lint.LanguageFromPath or
//	grounding.DetectLanguageFromPath (circular dependency), so this is a
//	standalone copy covering the 4 languages isTestEntryPoint handles.
//
// Inputs:
//
//	filePath - The file path to analyze.
//
// Outputs:
//
//	string - Language identifier ("go", "python", "typescript", "javascript")
//	         or empty string for unknown extensions.
//
// Thread Safety: Safe for concurrent use (pure function).
func inferLanguageFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py", ".pyi":
		return "python"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	default:
		return ""
	}
}

// isTestFilePath returns true if the file path matches test file heuristics.
//
// Description:
//
//	Copy of the heuristic from symbol_resolution.go for use in the graph
//	package. The graph package cannot import the tools package, so this
//	is a standalone copy used as a fallback when FileClassification is nil.
//
// Thread Safety: Safe for concurrent use (pure function).
func isTestFilePath(filePath string) bool {
	return strings.Contains(filePath, "/test") ||
		strings.HasPrefix(filePath, "test/") ||
		strings.Contains(filePath, "_test") ||
		strings.Contains(filePath, "/tests/") ||
		strings.HasPrefix(filePath, "tests/") ||
		strings.Contains(filePath, "/benchmark") ||
		strings.Contains(filePath, "/asv_bench/") ||
		strings.HasPrefix(filePath, "asv_bench/") ||
		strings.Contains(filePath, ".test.") ||
		strings.Contains(filePath, ".spec.") ||
		strings.Contains(filePath, "/integration/") ||
		strings.HasPrefix(filePath, "integration/") ||
		strings.Contains(filePath, "/quicktests/") ||
		strings.HasPrefix(filePath, "quicktests/") ||
		strings.Contains(filePath, "/e2e/") ||
		strings.HasPrefix(filePath, "e2e/") ||
		strings.Contains(filePath, "/__tests__/") ||
		strings.HasPrefix(filePath, "__tests__/") ||
		strings.Contains(filePath, "/__fixtures__/") ||
		strings.HasPrefix(filePath, "__fixtures__/") ||
		strings.Contains(filePath, "/cypress/") ||
		strings.HasPrefix(filePath, "cypress/") ||
		strings.Contains(filePath, "/fixtures/") ||
		strings.HasPrefix(filePath, "fixtures/")
}

// isDocFilePath returns true if the file path matches documentation file heuristics.
//
// Description:
//
//	Copy of the heuristic from symbol_resolution.go for use in the graph
//	package. Used as a fallback when FileClassification is nil.
//
// Thread Safety: Safe for concurrent use (pure function).
func isDocFilePath(filePath string) bool {
	lower := strings.ToLower(filePath)

	for _, dir := range []string{"doc/", "docs/", "documentation/", "examples/", "example/"} {
		if strings.HasPrefix(lower, dir) || strings.Contains(lower, "/"+dir) {
			return true
		}
	}

	for _, ext := range []string{".md", ".rst", ".txt", ".html", ".css", ".json", ".yaml", ".yml", ".toml", ".xml", ".csv", ".svg", ".png", ".jpg", ".gif", ".ico"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	for _, name := range []string{"makefile", "dockerfile", "rakefile", "gemfile", "setup.cfg", "pyproject.toml", "package.json", "tsconfig.json"} {
		base := lower
		if idx := strings.LastIndex(lower, "/"); idx >= 0 {
			base = lower[idx+1:]
		}
		if base == name {
			return true
		}
	}

	return false
}
