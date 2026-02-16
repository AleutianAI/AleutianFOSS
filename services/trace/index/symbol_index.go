// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package index

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Default configuration values.
const (
	// DefaultMaxSymbols is the default maximum number of symbols the index can hold.
	DefaultMaxSymbols = 1_000_000

	// searchCheckInterval is how often Search checks for context cancellation.
	searchCheckInterval = 1000
)

// SymbolIndexOptions configures SymbolIndex behavior and limits.
type SymbolIndexOptions struct {
	// MaxSymbols is the maximum number of symbols the index can hold.
	// Attempting to add more symbols returns ErrMaxSymbolsExceeded.
	// Default: 1,000,000
	MaxSymbols int
}

// DefaultSymbolIndexOptions returns the default options.
func DefaultSymbolIndexOptions() SymbolIndexOptions {
	return SymbolIndexOptions{
		MaxSymbols: DefaultMaxSymbols,
	}
}

// SymbolIndexOption is a functional option for configuring SymbolIndex.
type SymbolIndexOption func(*SymbolIndexOptions)

// WithMaxSymbols sets the maximum number of symbols the index can hold.
func WithMaxSymbols(max int) SymbolIndexOption {
	return func(o *SymbolIndexOptions) {
		o.MaxSymbols = max
	}
}

// IndexStats contains statistics about the symbol index.
type IndexStats struct {
	// TotalSymbols is the total number of symbols in the index.
	TotalSymbols int

	// ByKind maps each SymbolKind to the count of symbols of that kind.
	ByKind map[ast.SymbolKind]int

	// FileCount is the number of unique files with symbols in the index.
	FileCount int

	// MaxSymbols is the configured maximum capacity.
	MaxSymbols int
}

// SymbolIndex provides fast O(1) lookups of symbols by various keys.
//
// The index maintains multiple maps for efficient access patterns:
//   - byID: Primary index for unique symbol lookup
//   - byName: Secondary index for name-based queries (multiple symbols can share a name)
//   - byFile: Secondary index for file-based queries
//   - byKind: Secondary index for kind-based queries
//
// Thread Safety:
//
//	SymbolIndex is safe for concurrent use. Multiple goroutines can call
//	any combination of methods simultaneously.
//
// Ownership:
//
//	The index stores pointers to symbols but does NOT own them.
//	Symbols MUST NOT be mutated after being added to the index.
type SymbolIndex struct {
	mu sync.RWMutex

	// Primary index: ID → Symbol
	byID map[string]*ast.Symbol

	// Secondary indexes: key → []*Symbol
	byName map[string][]*ast.Symbol
	byFile map[string][]*ast.Symbol
	byKind map[ast.SymbolKind][]*ast.Symbol

	// Maintained counters for O(1) stats
	totalCount int
	kindCounts map[ast.SymbolKind]int

	// Configuration
	options SymbolIndexOptions
}

// NewSymbolIndex creates a new empty symbol index with the given options.
//
// Description:
//
//	Creates a concurrent-safe index for storing and querying code symbols.
//	The index is empty upon creation.
//
// Example:
//
//	// Default options (1M max symbols)
//	idx := NewSymbolIndex()
//
//	// Custom options
//	idx := NewSymbolIndex(WithMaxSymbols(100_000))
func NewSymbolIndex(opts ...SymbolIndexOption) *SymbolIndex {
	options := DefaultSymbolIndexOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return &SymbolIndex{
		byID:       make(map[string]*ast.Symbol),
		byName:     make(map[string][]*ast.Symbol),
		byFile:     make(map[string][]*ast.Symbol),
		byKind:     make(map[ast.SymbolKind][]*ast.Symbol),
		kindCounts: make(map[ast.SymbolKind]int),
		options:    options,
	}
}

// Add adds a single symbol to the index.
//
// Description:
//
//	Validates the symbol, checks for duplicates and capacity, then adds
//	the symbol to all indexes atomically.
//
// Inputs:
//
//	symbol - The symbol to add. Must pass Symbol.Validate().
//
// Outputs:
//
//	error - Non-nil if validation fails, symbol ID already exists, or
//	        index is at capacity.
//
// Errors:
//
//	ErrInvalidSymbol - Symbol failed validation
//	ErrDuplicateSymbol - Symbol with same ID already exists
//	ErrMaxSymbolsExceeded - Index is at capacity
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) Add(symbol *ast.Symbol) error {
	if symbol == nil {
		return fmt.Errorf("%w: symbol is nil", ErrInvalidSymbol)
	}

	// Validate BEFORE acquiring lock (fail fast)
	if err := symbol.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSymbol, err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Check capacity
	if idx.totalCount >= idx.options.MaxSymbols {
		return ErrMaxSymbolsExceeded
	}

	// Check duplicate
	if _, exists := idx.byID[symbol.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateSymbol, symbol.ID)
	}

	// All-or-nothing add (no failure point after first write)
	idx.addSymbolLocked(symbol)

	return nil
}

// AddBatch adds multiple symbols to the index atomically.
//
// Description:
//
//	Validates all symbols, checks for duplicates (both within the batch
//	and against existing symbols), then adds all symbols atomically.
//	If any validation fails, NO symbols are added.
//
// Inputs:
//
//	symbols - The symbols to add. All must pass validation.
//
// Outputs:
//
//	error - Non-nil if any validation fails, any duplicates exist, or
//	        adding would exceed capacity.
//
// Errors:
//
//	*BatchError - Contains all validation/duplicate errors found
//	ErrMaxSymbolsExceeded - Adding batch would exceed capacity
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) AddBatch(symbols []*ast.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	// Phase 1: Validate ALL symbols before acquiring lock
	var errs []error
	seen := make(map[string]int) // ID → first index seen

	for i, sym := range symbols {
		if sym == nil {
			errs = append(errs, fmt.Errorf("symbol[%d]: %w: symbol is nil", i, ErrInvalidSymbol))
			continue
		}

		if err := sym.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("symbol[%d]: %w: %v", i, ErrInvalidSymbol, err))
			continue
		}

		if firstIdx, exists := seen[sym.ID]; exists {
			errs = append(errs, fmt.Errorf("symbol[%d]: duplicate ID in batch (same as symbol[%d]): %s",
				i, firstIdx, sym.ID))
		} else {
			seen[sym.ID] = i
		}
	}

	if len(errs) > 0 {
		return &BatchError{Errors: errs}
	}

	// Phase 2: Check against existing index
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.totalCount+len(symbols) > idx.options.MaxSymbols {
		return ErrMaxSymbolsExceeded
	}

	for i, sym := range symbols {
		if _, exists := idx.byID[sym.ID]; exists {
			errs = append(errs, fmt.Errorf("symbol[%d]: %w: %s", i, ErrDuplicateSymbol, sym.ID))
		}
	}

	if len(errs) > 0 {
		return &BatchError{Errors: errs}
	}

	// Phase 3: All validated, perform atomic add
	for _, sym := range symbols {
		idx.addSymbolLocked(sym)
	}

	return nil
}

// addSymbolLocked adds a symbol to all indexes. Caller must hold idx.mu.Lock().
func (idx *SymbolIndex) addSymbolLocked(symbol *ast.Symbol) {
	idx.byID[symbol.ID] = symbol
	idx.byName[symbol.Name] = append(idx.byName[symbol.Name], symbol)
	idx.byFile[symbol.FilePath] = append(idx.byFile[symbol.FilePath], symbol)
	idx.byKind[symbol.Kind] = append(idx.byKind[symbol.Kind], symbol)

	idx.totalCount++
	idx.kindCounts[symbol.Kind]++
}

// GetByID retrieves a symbol by its unique ID.
//
// Description:
//
//	Performs O(1) lookup in the primary index.
//
// Inputs:
//
//	id - The symbol ID (format: "file_path:line:name")
//
// Outputs:
//
//	*ast.Symbol - The symbol if found, nil otherwise
//	bool - True if symbol was found
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) GetByID(id string) (*ast.Symbol, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	sym, exists := idx.byID[id]
	return sym, exists
}

// GetByName retrieves all symbols with the given name.
//
// Description:
//
//	Performs O(1) lookup, returns defensive copy of the result slice.
//	Multiple symbols can share the same name (e.g., "Handler" in different packages).
//
// Inputs:
//
//	name - The symbol name to search for
//
// Outputs:
//
//	[]*ast.Symbol - Defensive copy of matching symbols, nil if none found
//
// Thread Safety:
//
//	This method is safe for concurrent use. The returned slice is a copy
//	and can be safely modified by the caller.
func (idx *SymbolIndex) GetByName(name string) []*ast.Symbol {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return idx.copySlice(idx.byName[name])
}

// GetByFile retrieves all symbols in the given file.
//
// Description:
//
//	Performs O(1) lookup, returns defensive copy of the result slice.
//
// Inputs:
//
//	filePath - The file path (relative to project root)
//
// Outputs:
//
//	[]*ast.Symbol - Defensive copy of symbols in that file, nil if none found
//
// Thread Safety:
//
//	This method is safe for concurrent use. The returned slice is a copy
//	and can be safely modified by the caller.
func (idx *SymbolIndex) GetByFile(filePath string) []*ast.Symbol {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return idx.copySlice(idx.byFile[filePath])
}

// GetByKind retrieves all symbols of the given kind.
//
// Description:
//
//	Performs O(1) lookup, returns defensive copy of the result slice.
//
// Inputs:
//
//	kind - The symbol kind (e.g., SymbolKindFunction)
//
// Outputs:
//
//	[]*ast.Symbol - Defensive copy of symbols of that kind, nil if none found
//
// Thread Safety:
//
//	This method is safe for concurrent use. The returned slice is a copy
//	and can be safely modified by the caller.
func (idx *SymbolIndex) GetByKind(kind ast.SymbolKind) []*ast.Symbol {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	return idx.copySlice(idx.byKind[kind])
}

// copySlice returns a defensive copy of the given slice.
func (idx *SymbolIndex) copySlice(src []*ast.Symbol) []*ast.Symbol {
	if len(src) == 0 {
		return nil
	}

	result := make([]*ast.Symbol, len(src))
	copy(result, src)
	return result
}

// Search finds symbols matching the query string.
//
// Description:
//
//	Performs fuzzy search across all symbol names. Results are sorted by
//	relevance: exact matches first, then prefix matches, then substring
//	matches, then fuzzy matches (Levenshtein distance < 3).
//
// Performance:
//
//	O(n) where n is total symbols. For indexes > 50k symbols, consider
//	using GetByName() for exact matches first. The context is checked
//	periodically during search to allow cancellation.
//
// Inputs:
//
//	ctx - Context for cancellation
//	query - Search string (case-insensitive)
//	limit - Maximum number of results to return (0 = no limit)
//
// Outputs:
//
//	[]*ast.Symbol - Matching symbols sorted by relevance
//	error - Non-nil if context was cancelled
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) Search(ctx context.Context, query string, limit int) ([]*ast.Symbol, error) {
	ctx, span := startOperationSpan(ctx, "Search")
	defer span.End()
	start := time.Now()

	if err := ctx.Err(); err != nil {
		setOperationSpanResult(span, 0, false)
		recordOperationMetrics(ctx, "search", time.Since(start), 0, false)
		return nil, err
	}

	if query == "" {
		setOperationSpanResult(span, 0, true)
		recordOperationMetrics(ctx, "search", time.Since(start), 0, true)
		return nil, nil
	}

	queryLower := strings.ToLower(query)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	type scoredSymbol struct {
		symbol    *ast.Symbol
		score     int    // Lower is better - composite score
		matchType string // For debugging: "exact", "prefix", "camelCase", "substring", "fuzzy"
	}

	var results []scoredSymbol
	count := 0

	for _, sym := range idx.byID {
		count++

		// Check context periodically
		if count%searchCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}

		nameLower := strings.ToLower(sym.Name)
		score, matchType := computeMatchScore(query, queryLower, sym.Name, nameLower, sym.Kind)

		if score >= 0 {
			results = append(results, scoredSymbol{
				symbol:    sym,
				score:     score,
				matchType: matchType,
			})
		}
	}

	// Sort by score (lower is better)
	sort.Slice(results, func(i, j int) bool {
		return results[i].score < results[j].score
	})

	// Apply limit
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	// Extract symbols
	symbols := make([]*ast.Symbol, len(results))
	for i, r := range results {
		symbols[i] = r.symbol
	}

	setOperationSpanResult(span, len(symbols), true)
	recordOperationMetrics(ctx, "search", time.Since(start), len(symbols), true)
	recordSearchResults(ctx, len(symbols))

	return symbols, nil
}

// computeMatchScore calculates a detailed match score for fuzzy search.
//
// Enhanced scoring system (Feb 14, 2026):
//
//	Score = base_score * 10000 +
//	        position_penalty * 100 +
//	        length_penalty * 10 +
//	        kind_penalty
//
// Where lower scores are better. This provides much finer granularity than
// the old 0-3 system, allowing proper ranking of match quality.
//
// Inputs:
//
//	query - Original query string (preserves case).
//	queryLower - Lowercase version of query.
//	name - Symbol name (preserves case).
//	nameLower - Lowercase version of name.
//	kind - Symbol kind (function, type, etc.).
//
// Outputs:
//
//	score - Composite score (lower is better). -1 means no match.
//	matchType - String describing match type for debugging.
//
// Match Types (base_score):
//
//	0 = Exact match (case-insensitive)
//	1 = Prefix match
//	2 = CamelCase word boundary match
//	3 = Substring match
//	4 = Fuzzy match (Levenshtein)
//	-1 = No match
//
// Thread Safety: Safe for concurrent use (stateless function).
func computeMatchScore(query, queryLower, name, nameLower string, kind ast.SymbolKind) (int, string) {
	// Base score determines primary match type
	var baseScore int
	var matchType string
	var matchPos int // Position of match in name

	// 1. Exact match (highest priority)
	if nameLower == queryLower {
		return 0, "exact" // Perfect match, no need for further scoring
	}

	// 2. Prefix match
	if strings.HasPrefix(nameLower, queryLower) {
		baseScore = 1
		matchType = "prefix"
		matchPos = 0
	} else if pos := findCamelCaseWordMatch(name, query); pos >= 0 {
		// 3. CamelCase word boundary match (e.g., "Process" matches "ProcessData" or "getDatesToProcess")
		baseScore = 2
		matchType = "camelCase"
		matchPos = pos
	} else if pos := strings.Index(nameLower, queryLower); pos >= 0 {
		// 4. Substring match
		baseScore = 3
		matchType = "substring"
		matchPos = pos
	} else {
		// 5. Fuzzy match (Levenshtein distance)
		// Scale threshold with query length: allow 30% error rate
		threshold := max(2, len(queryLower)/3)
		distance := levenshteinDistance(nameLower, queryLower)
		if distance <= threshold {
			baseScore = 4
			matchType = "fuzzy"
			matchPos = 0 // Not applicable for fuzzy
		} else {
			return -1, "no_match" // No match found
		}
	}

	// Position penalty: Earlier matches are better (0-99)
	// Scale to 0-99 based on name length
	positionPenalty := 0
	if len(name) > 0 && matchPos > 0 {
		positionPenalty = min(99, (matchPos*100)/len(name))
	}

	// Length penalty: Prefer shorter names (0-99)
	// Difference in length, capped at 99
	lengthDiff := abs(len(name) - len(query))
	lengthPenalty := min(99, lengthDiff)

	// Kind penalty: Prefer functions/methods over types/variables (0-9)
	kindPenalty := getKindPenalty(kind)

	// Composite score: lower is better
	// Format: BMMPPLK where B=base, MM=matchPos, PP=length, L=kind
	score := baseScore*10000 +
		positionPenalty*100 +
		lengthPenalty*10 +
		kindPenalty

	return score, matchType
}

// findCamelCaseWordMatch finds if query matches a word boundary in camelCase/PascalCase name.
//
// Examples:
//
//	"Process" matches "ProcessData" at position 0
//	"Process" matches "getDatesToProcess" at position 11
//	"Data" matches "ProcessData" at position 7
//	"process" does not match "Unprocessed" (not a word boundary)
//
// Returns: Position of match, or -1 if no word boundary match.
func findCamelCaseWordMatch(name, query string) int {
	if len(query) == 0 || len(name) == 0 {
		return -1
	}

	// Find all word boundaries (uppercase letters or start of string)
	queryLower := strings.ToLower(query)

	for i := 0; i < len(name); i++ {
		// Check if this is a word boundary
		isWordBoundary := i == 0 || (i > 0 && isUpper(name[i]) && !isUpper(name[i-1]))

		if isWordBoundary {
			// Check if query matches starting here (case-insensitive)
			if i+len(query) <= len(name) {
				if strings.ToLower(name[i:i+len(query)]) == queryLower {
					// Verify next char is either end of string or uppercase (true word boundary)
					if i+len(query) == len(name) || isUpper(name[i+len(query)]) || !isLetter(name[i+len(query)]) {
						return i
					}
				}
			}
		}
	}

	return -1
}

// getKindPenalty returns a penalty based on symbol kind.
// Lower penalty for more important symbol types.
func getKindPenalty(kind ast.SymbolKind) int {
	switch kind {
	case ast.SymbolKindFunction, ast.SymbolKindMethod:
		return 0 // Functions and methods are most important
	case ast.SymbolKindType, ast.SymbolKindInterface, ast.SymbolKindStruct:
		return 1 // Types are important but less than functions
	case ast.SymbolKindVariable, ast.SymbolKindConstant:
		return 2 // Variables/constants are least important
	case ast.SymbolKindField, ast.SymbolKindParameter:
		return 3 // Fields and parameters even less
	default:
		return 5 // Unknown kinds get low priority
	}
}

// Helper functions for character classification
func isUpper(c byte) bool {
	return c >= 'A' && c <= 'Z'
}

func isLetter(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Note: min() and max() are Go 1.21+ built-ins, no need to define them

// levenshteinDistance calculates the edit distance between two strings.
// This is a simple implementation for fuzzy matching.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Use two rows instead of full matrix for memory efficiency
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// RemoveByFile removes all symbols from the given file.
//
// Description:
//
//	Removes symbols from all indexes atomically. Use this before
//	AddBatch when updating symbols for a file.
//
// Inputs:
//
//	filePath - The file path whose symbols should be removed
//
// Outputs:
//
//	int - Number of symbols removed
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) RemoveByFile(filePath string) int {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	symbols := idx.byFile[filePath]
	if len(symbols) == 0 {
		return 0
	}

	// Remove from all indexes
	for _, sym := range symbols {
		// Remove from byID
		delete(idx.byID, sym.ID)

		// Remove from byName
		idx.byName[sym.Name] = removeFromSlice(idx.byName[sym.Name], sym)
		if len(idx.byName[sym.Name]) == 0 {
			delete(idx.byName, sym.Name)
		}

		// Remove from byKind
		idx.byKind[sym.Kind] = removeFromSlice(idx.byKind[sym.Kind], sym)
		if len(idx.byKind[sym.Kind]) == 0 {
			delete(idx.byKind, sym.Kind)
		}

		// Update counters
		idx.totalCount--
		idx.kindCounts[sym.Kind]--
		if idx.kindCounts[sym.Kind] == 0 {
			delete(idx.kindCounts, sym.Kind)
		}
	}

	// Remove from byFile
	removed := len(symbols)
	delete(idx.byFile, filePath)

	return removed
}

// removeFromSlice removes the given symbol from the slice by pointer equality.
func removeFromSlice(slice []*ast.Symbol, sym *ast.Symbol) []*ast.Symbol {
	for i, s := range slice {
		if s == sym {
			// Remove by swapping with last element
			slice[i] = slice[len(slice)-1]
			return slice[:len(slice)-1]
		}
	}
	return slice
}

// Clear removes all symbols from the index.
//
// Description:
//
//	Resets the index to empty state. All counters are reset to zero.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.byID = make(map[string]*ast.Symbol)
	idx.byName = make(map[string][]*ast.Symbol)
	idx.byFile = make(map[string][]*ast.Symbol)
	idx.byKind = make(map[ast.SymbolKind][]*ast.Symbol)
	idx.kindCounts = make(map[ast.SymbolKind]int)
	idx.totalCount = 0
}

// Stats returns statistics about the index.
//
// Description:
//
//	Returns counts using O(1) maintained counters, not map traversal.
//
// Outputs:
//
//	IndexStats - Current index statistics
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (idx *SymbolIndex) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Copy kind counts
	byKind := make(map[ast.SymbolKind]int, len(idx.kindCounts))
	for k, v := range idx.kindCounts {
		byKind[k] = v
	}

	return IndexStats{
		TotalSymbols: idx.totalCount,
		ByKind:       byKind,
		FileCount:    len(idx.byFile),
		MaxSymbols:   idx.options.MaxSymbols,
	}
}

// Clone creates a deep copy of the symbol index.
//
// Description:
//
//	Creates an independent copy of the index that can be modified without
//	affecting the original. Used for copy-on-write incremental updates.
//
// Outputs:
//
//	*SymbolIndex - A deep copy of the index.
//
// Behavior:
//
//   - All maps are deep copied
//   - Symbol pointers are shared (symbols are immutable after add)
//   - Counters are copied
//   - Options are copied
//
// Thread Safety:
//
//	The returned index is independent and can be modified without synchronization.
//	This method is safe to call concurrently on the source index.
func (idx *SymbolIndex) Clone() *SymbolIndex {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	clone := &SymbolIndex{
		byID:       make(map[string]*ast.Symbol, len(idx.byID)),
		byName:     make(map[string][]*ast.Symbol, len(idx.byName)),
		byFile:     make(map[string][]*ast.Symbol, len(idx.byFile)),
		byKind:     make(map[ast.SymbolKind][]*ast.Symbol, len(idx.byKind)),
		kindCounts: make(map[ast.SymbolKind]int, len(idx.kindCounts)),
		totalCount: idx.totalCount,
		options:    idx.options,
	}

	// Copy byID (primary index)
	for id, sym := range idx.byID {
		clone.byID[id] = sym
	}

	// Copy byName (secondary index)
	for name, symbols := range idx.byName {
		cloned := make([]*ast.Symbol, len(symbols))
		copy(cloned, symbols)
		clone.byName[name] = cloned
	}

	// Copy byFile (secondary index)
	for file, symbols := range idx.byFile {
		cloned := make([]*ast.Symbol, len(symbols))
		copy(cloned, symbols)
		clone.byFile[file] = cloned
	}

	// Copy byKind (secondary index)
	for kind, symbols := range idx.byKind {
		cloned := make([]*ast.Symbol, len(symbols))
		copy(cloned, symbols)
		clone.byKind[kind] = cloned
	}

	// Copy kind counts
	for kind, count := range idx.kindCounts {
		clone.kindCounts[kind] = count
	}

	return clone
}
