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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// DuplicationType categorizes duplicates.
type DuplicationType string

const (
	// DuplicationExact is byte-for-byte identical code.
	DuplicationExact DuplicationType = "exact"

	// DuplicationNear is structurally similar code with minor differences.
	DuplicationNear DuplicationType = "near"

	// DuplicationStructural is same control flow with different names.
	DuplicationStructural DuplicationType = "structural"
)

// DuplicationOptions configures duplication detection.
type DuplicationOptions struct {
	// MinLines is the minimum lines to consider (default: 5).
	MinLines int

	// SimilarityThreshold is the minimum similarity (default: 0.8).
	SimilarityThreshold float64

	// Type filters results by duplication type ("exact", "near", "structural").
	// Empty string means all types.
	Type string

	// IncludeTests includes test files in analysis.
	IncludeTests bool

	// MaxResults limits the number of results (0 = unlimited).
	MaxResults int
}

// DefaultDuplicationOptions returns sensible defaults.
func DefaultDuplicationOptions() DuplicationOptions {
	return DuplicationOptions{
		MinLines:            5,
		SimilarityThreshold: 0.8,
		IncludeTests:        false,
		MaxResults:          0,
	}
}

// DuplicationFinder finds duplicate code blocks.
//
// # Description
//
// DuplicationFinder uses LSH-based fingerprinting to efficiently find
// duplicate and near-duplicate code. It operates in O(n log n) time
// instead of O(n²) for large codebases.
//
// # Thread Safety
//
// This type is safe for concurrent use.
type DuplicationFinder struct {
	idx           *index.SymbolIndex
	fileReader    *FileReader
	crs           CRSRecorder
	fingerprinter *Fingerprinter
	lshIndex      *LSHIndex
	mu            sync.RWMutex
	indexed       bool
}

// NewDuplicationFinder creates a new duplication finder.
//
// # Description
//
// Creates a finder with pre-configured fingerprinting and LSH index.
// The index is built lazily on first query.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - projectRoot: Project root directory for reading source files.
//
// # Outputs
//
//   - *DuplicationFinder: Configured finder.
func NewDuplicationFinder(idx *index.SymbolIndex, projectRoot string) *DuplicationFinder {
	fpConfig := DefaultFingerprintConfig()
	fingerprinter := NewFingerprinter(fpConfig)

	// 20 bands x 5 rows = 100 signature length, ~80% similarity threshold
	lshIndex := NewLSHIndex(20, 5)

	return &DuplicationFinder{
		idx:           idx,
		fileReader:    NewFileReader(projectRoot),
		crs:           &NopCRSRecorder{},
		fingerprinter: fingerprinter,
		lshIndex:      lshIndex,
		indexed:       false,
	}
}

// NewDuplicationFinderWithReader creates a duplication finder with a shared FileReader.
//
// # Inputs
//
//   - idx: Symbol index for lookups.
//   - reader: Shared cached file reader.
//
// # Outputs
//
//   - *DuplicationFinder: Configured finder.
func NewDuplicationFinderWithReader(idx *index.SymbolIndex, reader *FileReader) *DuplicationFinder {
	fpConfig := DefaultFingerprintConfig()
	fingerprinter := NewFingerprinter(fpConfig)
	lshIndex := NewLSHIndex(20, 5)

	return &DuplicationFinder{
		idx:           idx,
		fileReader:    reader,
		crs:           &NopCRSRecorder{},
		fingerprinter: fingerprinter,
		lshIndex:      lshIndex,
		indexed:       false,
	}
}

// SetCRS configures CRS recording for this finder.
func (d *DuplicationFinder) SetCRS(recorder CRSRecorder) {
	d.crs = recorder
}

// BuildIndex builds the fingerprint index for the codebase.
//
// # Description
//
// Scans all functions/methods in the index, computes fingerprints,
// and adds them to the LSH index. This is called automatically on
// first query but can be called explicitly for better control.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - opts: Options controlling what to index.
//
// # Outputs
//
//   - int: Number of symbols indexed.
//   - error: Non-nil on failure.
func (d *DuplicationFinder) BuildIndex(ctx context.Context, opts *DuplicationOptions) (int, error) {
	if ctx == nil {
		return 0, ErrInvalidInput
	}

	if opts == nil {
		defaults := DefaultDuplicationOptions()
		opts = &defaults
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	count := 0

	// Get all functions and methods
	functions := d.idx.GetByKind(ast.SymbolKindFunction)
	methods := d.idx.GetByKind(ast.SymbolKindMethod)

	allSymbols := append(functions, methods...)

	for _, sym := range allSymbols {
		if ctx.Err() != nil {
			return count, ctx.Err()
		}

		// Skip test files if not requested
		if !opts.IncludeTests && strings.Contains(sym.FilePath, "_test.go") {
			continue
		}

		// Skip symbols below minimum lines
		lineCount := sym.EndLine - sym.StartLine + 1
		if lineCount < opts.MinLines {
			continue
		}

		// Read source code
		code, err := d.fileReader.ReadSymbolCode(sym)
		if err != nil {
			continue // Skip symbols we can't read
		}

		// Create fingerprint
		fp := d.fingerprinter.Fingerprint(sym, code)
		if fp == nil {
			continue
		}

		d.lshIndex.Add(fp)
		count++
	}

	d.indexed = true
	return count, nil
}

// FindDuplication finds duplicate code blocks.
//
// # Description
//
// Finds all pairs of duplicate code blocks in the specified scope.
// Uses LSH for efficient near-duplicate detection.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - scope: Package or file path prefix (empty = all).
//   - opts: Detection options.
//
// # Outputs
//
//   - []Duplication: Found duplications.
//   - error: Non-nil on failure.
//
// # Example
//
//	finder := NewDuplicationFinder(index, "/project")
//	dups, err := finder.FindDuplication(ctx, "pkg/auth", nil)
func (d *DuplicationFinder) FindDuplication(
	ctx context.Context,
	scope string,
	opts *DuplicationOptions,
) ([]Duplication, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startDuplicationSpan(ctx, scope)
	defer span.End()

	if opts == nil {
		defaults := DefaultDuplicationOptions()
		opts = &defaults
	}

	// Build index if not already done
	d.mu.RLock()
	indexed := d.indexed
	d.mu.RUnlock()

	if !indexed {
		if _, err := d.BuildIndex(ctx, opts); err != nil {
			return nil, fmt.Errorf("building index: %w", err)
		}
	}

	// Find all duplicate pairs
	d.mu.RLock()
	pairs := d.lshIndex.FindAllDuplicates(opts.SimilarityThreshold)
	d.mu.RUnlock()

	// Filter by scope and convert to Duplication type
	var results []Duplication

	for _, pair := range pairs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Get fingerprints for additional info
		fp1, ok1 := d.lshIndex.GetFingerprint(pair.SymbolID1)
		fp2, ok2 := d.lshIndex.GetFingerprint(pair.SymbolID2)

		if !ok1 || !ok2 {
			continue
		}

		// Check scope filter
		if scope != "" {
			if !strings.HasPrefix(fp1.FilePath, scope) && !strings.HasPrefix(fp2.FilePath, scope) {
				continue
			}
		}

		// Determine duplication type
		dupType := d.classifyDuplication(fp1, fp2, pair.Similarity)

		// Filter by type if specified
		if opts.Type != "" && string(dupType) != opts.Type {
			continue
		}

		// Create Duplication result
		dup := Duplication{
			Type:       string(dupType),
			Similarity: pair.Similarity,
			Locations: []DupLocation{
				{
					FilePath:  fp1.FilePath,
					LineStart: fp1.LineStart,
					LineEnd:   fp1.LineEnd,
					SymbolID:  fp1.SymbolID,
				},
				{
					FilePath:  fp2.FilePath,
					LineStart: fp2.LineStart,
					LineEnd:   fp2.LineEnd,
					SymbolID:  fp2.SymbolID,
				},
			},
			Suggestion: d.generateSuggestion(fp1, fp2, dupType),
			Confidence: d.calculateConfidence(pair.Similarity, dupType),
		}

		results = append(results, dup)

		// Check max results
		if opts.MaxResults > 0 && len(results) >= opts.MaxResults {
			break
		}
	}

	// Sort by similarity (highest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	dur := time.Since(start)
	setDuplicationSpanResult(span, len(results), nil)
	recordDuplicationMetrics(ctx, dur, len(results), nil)
	d.crs.RecordToolStep(ctx, "find_duplication", len(results), dur, nil)

	return results, nil
}

// FindDuplicatesOf finds code similar to a specific symbol.
//
// # Description
//
// Finds all code blocks similar to the specified symbol.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - symbolID: The symbol to find duplicates of.
//   - opts: Detection options.
//
// # Outputs
//
//   - []Duplication: Found duplications.
//   - error: Non-nil on failure.
func (d *DuplicationFinder) FindDuplicatesOf(
	ctx context.Context,
	symbolID string,
	opts *DuplicationOptions,
) ([]Duplication, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	if opts == nil {
		defaults := DefaultDuplicationOptions()
		opts = &defaults
	}

	// Build index if needed
	d.mu.RLock()
	indexed := d.indexed
	d.mu.RUnlock()

	if !indexed {
		if _, err := d.BuildIndex(ctx, opts); err != nil {
			return nil, fmt.Errorf("building index: %w", err)
		}
	}

	// Get the query fingerprint
	d.mu.RLock()
	queryFP, exists := d.lshIndex.GetFingerprint(symbolID)
	d.mu.RUnlock()

	if !exists {
		// Try to create fingerprint on the fly
		sym, found := d.idx.GetByID(symbolID)
		if !found {
			return nil, ErrPatternNotFound
		}

		code, err := d.fileReader.ReadSymbolCode(sym)
		if err != nil {
			return nil, fmt.Errorf("reading symbol code: %w", err)
		}

		queryFP = d.fingerprinter.Fingerprint(sym, code)
		if queryFP == nil {
			return nil, fmt.Errorf("failed to fingerprint symbol")
		}
	}

	// Query for similar
	d.mu.RLock()
	matches := d.lshIndex.QueryWithThreshold(queryFP, opts.SimilarityThreshold)
	d.mu.RUnlock()

	var results []Duplication

	for _, match := range matches {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		matchFP, ok := d.lshIndex.GetFingerprint(match.SymbolID)
		if !ok {
			continue
		}

		dupType := d.classifyDuplication(queryFP, matchFP, match.Similarity)

		dup := Duplication{
			Type:       string(dupType),
			Similarity: match.Similarity,
			Locations: []DupLocation{
				{
					FilePath:  queryFP.FilePath,
					LineStart: queryFP.LineStart,
					LineEnd:   queryFP.LineEnd,
					SymbolID:  queryFP.SymbolID,
				},
				{
					FilePath:  matchFP.FilePath,
					LineStart: matchFP.LineStart,
					LineEnd:   matchFP.LineEnd,
					SymbolID:  matchFP.SymbolID,
				},
			},
			Suggestion: d.generateSuggestion(queryFP, matchFP, dupType),
			Confidence: d.calculateConfidence(match.Similarity, dupType),
		}

		results = append(results, dup)

		if opts.MaxResults > 0 && len(results) >= opts.MaxResults {
			break
		}
	}

	// Sort by similarity
	sort.Slice(results, func(i, j int) bool {
		return results[i].Similarity > results[j].Similarity
	})

	return results, nil
}

// classifyDuplication determines the type of duplication.
func (d *DuplicationFinder) classifyDuplication(fp1, fp2 *CodeFingerprint, similarity float64) DuplicationType {
	// Exact: very high similarity (>= 0.95)
	if similarity >= 0.95 {
		return DuplicationExact
	}

	// Structural: same AST structure but different tokens
	if fp1.ASTStructure == fp2.ASTStructure && similarity >= 0.6 {
		return DuplicationStructural
	}

	// Near: moderate to high similarity
	return DuplicationNear
}

// generateSuggestion creates a refactoring suggestion.
func (d *DuplicationFinder) generateSuggestion(fp1, fp2 *CodeFingerprint, dupType DuplicationType) string {
	switch dupType {
	case DuplicationExact:
		// Same file - suggest extraction
		if fp1.FilePath == fp2.FilePath {
			return "Extract duplicated code into a shared function"
		}
		return fmt.Sprintf("Consider extracting shared logic into a common package, referenced by %s and %s",
			filepath.Base(fp1.FilePath), filepath.Base(fp2.FilePath))

	case DuplicationNear:
		return "Similar code patterns detected. Consider parameterizing differences"

	case DuplicationStructural:
		return "Same control flow with different identifiers. Consider using generics or interfaces"

	default:
		return "Review for potential refactoring"
	}
}

// calculateConfidence computes confidence score.
func (d *DuplicationFinder) calculateConfidence(similarity float64, dupType DuplicationType) float64 {
	base := similarity

	switch dupType {
	case DuplicationExact:
		base *= 1.0 // High confidence for exact matches
	case DuplicationNear:
		base *= 0.9 // Slightly lower for near duplicates
	case DuplicationStructural:
		base *= 0.8 // Lower for structural
	}

	// Clamp to [0, 1]
	if base > 1.0 {
		return 1.0
	}
	return base
}

// Summary generates a summary of duplication findings.
func (d *DuplicationFinder) Summary(duplications []Duplication) string {
	if len(duplications) == 0 {
		return "No code duplication detected"
	}

	exact := 0
	near := 0
	structural := 0

	for _, dup := range duplications {
		switch DuplicationType(dup.Type) {
		case DuplicationExact:
			exact++
		case DuplicationNear:
			near++
		case DuplicationStructural:
			structural++
		}
	}

	return fmt.Sprintf(
		"Found %d duplication(s): %d exact, %d near, %d structural",
		len(duplications), exact, near, structural,
	)
}

// Stats returns statistics about the duplication finder.
func (d *DuplicationFinder) Stats() DuplicationStats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	lshStats := d.lshIndex.Stats()

	return DuplicationStats{
		Indexed:        d.indexed,
		IndexedSymbols: lshStats.NumFingerprints,
		NumBands:       lshStats.NumBands,
		RowsPerBand:    lshStats.RowsPerBand,
		TotalBuckets:   lshStats.TotalBuckets,
		MaxBucketSize:  lshStats.MaxBucketSize,
	}
}

// DuplicationStats contains statistics about duplication detection.
type DuplicationStats struct {
	// Indexed indicates if the index has been built.
	Indexed bool

	// IndexedSymbols is the number of indexed symbols.
	IndexedSymbols int

	// NumBands is the LSH band count.
	NumBands int

	// RowsPerBand is the LSH rows per band.
	RowsPerBand int

	// TotalBuckets is the total non-empty buckets.
	TotalBuckets int

	// MaxBucketSize is the largest bucket size.
	MaxBucketSize int
}
