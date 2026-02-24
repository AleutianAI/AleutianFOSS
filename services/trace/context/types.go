// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package context provides the Context Assembler for Trace.
//
// The Context Assembler combines graph traversal, symbol index lookups,
// and optionally library documentation to produce focused, relevant context
// for LLM prompts within a token budget.
//
// Design principles:
//   - Respect token budgets strictly (never exceed)
//   - Prioritize relevance using fuzzy matching and graph distance
//   - Include type definitions with code to provide complete context
//   - Support graceful degradation when library docs are unavailable
package context

import (
	"context"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// Default configuration values.
const (
	// DefaultTokenBudget is the default maximum tokens for assembled context.
	DefaultTokenBudget = 8000

	// DefaultGraphDepth is the default BFS traversal depth.
	DefaultGraphDepth = 2

	// DefaultMaxSymbols is the default maximum symbols to collect.
	DefaultMaxSymbols = 100

	// DefaultTimeout is the default assembly timeout.
	DefaultTimeout = 500 * time.Millisecond

	// MaxQueryLength is the maximum allowed query length in characters.
	MaxQueryLength = 500

	// TokenSafetyBuffer is the percentage of budget reserved as safety margin.
	TokenSafetyBuffer = 0.10

	// CharsPerToken is the approximation ratio for token counting.
	// Conservative estimate: most tokenizers produce ~1 token per 3-4 chars for code.
	CharsPerToken = 3.5

	// MinCodeBudget is the minimum number of tokens reserved for source code context.
	//
	// This constant addresses the "Pinned Budget Context Squeeze" problem:
	// As a session progresses, PinnedInstructions (plan, findings) grows,
	// and without enforcement the code budget can be squeezed to zero.
	//
	// The agent has perfect memory of WHAT it wants to do but can't see
	// the actual code. MinCodeBudget ensures this never happens.
	//
	// The value of 4096 tokens allows approximately:
	//   - 15,000 characters of code (at 3.5 chars/token)
	//   - ~500 lines of typical code
	//   - Enough for a focused function + dependencies
	MinCodeBudget = 4096
)

// BudgetAllocation defines how the token budget is split across content types.
type BudgetAllocation struct {
	// CodePercent is the percentage allocated to primary code (default: 60).
	CodePercent int

	// TypesPercent is the percentage allocated to type signatures (default: 20).
	TypesPercent int

	// LibDocsPercent is the percentage allocated to library docs (default: 20).
	LibDocsPercent int
}

// DefaultBudgetAllocation returns the default 60/20/20 split.
func DefaultBudgetAllocation() BudgetAllocation {
	return BudgetAllocation{
		CodePercent:    60,
		TypesPercent:   20,
		LibDocsPercent: 20,
	}
}

// Validate checks that the allocation percentages sum to 100.
func (b BudgetAllocation) Validate() bool {
	return b.CodePercent+b.TypesPercent+b.LibDocsPercent == 100
}

// AssembleOptions configures the assembly operation.
type AssembleOptions struct {
	// Timeout is the maximum duration for assembly (default: 500ms).
	Timeout time.Duration

	// MaxSymbols is the maximum number of symbols to collect (default: 100).
	MaxSymbols int

	// GraphDepth is the BFS traversal depth (default: 2).
	GraphDepth int

	// BudgetAllocation defines budget distribution (default: 60/20/20).
	BudgetAllocation BudgetAllocation

	// IncludeLibraryDocs enables library documentation lookup (default: true).
	IncludeLibraryDocs bool
}

// DefaultAssembleOptions returns sensible defaults for assembly.
func DefaultAssembleOptions() AssembleOptions {
	return AssembleOptions{
		Timeout:            DefaultTimeout,
		MaxSymbols:         DefaultMaxSymbols,
		GraphDepth:         DefaultGraphDepth,
		BudgetAllocation:   DefaultBudgetAllocation(),
		IncludeLibraryDocs: true,
	}
}

// AssembleOption is a functional option for configuring assembly.
type AssembleOption func(*AssembleOptions)

// WithTimeout sets the assembly timeout.
func WithTimeout(d time.Duration) AssembleOption {
	return func(o *AssembleOptions) {
		if d > 0 {
			o.Timeout = d
		}
	}
}

// WithMaxSymbols sets the maximum symbols to collect.
func WithMaxSymbols(n int) AssembleOption {
	return func(o *AssembleOptions) {
		if n > 0 {
			o.MaxSymbols = n
		}
	}
}

// WithGraphDepth sets the BFS traversal depth.
func WithGraphDepth(d int) AssembleOption {
	return func(o *AssembleOptions) {
		if d >= 0 {
			o.GraphDepth = d
		}
	}
}

// WithBudgetAllocation sets custom budget allocation.
func WithBudgetAllocation(alloc BudgetAllocation) AssembleOption {
	return func(o *AssembleOptions) {
		if alloc.Validate() {
			o.BudgetAllocation = alloc
		}
	}
}

// WithLibraryDocs enables or disables library documentation lookup.
func WithLibraryDocs(enabled bool) AssembleOption {
	return func(o *AssembleOptions) {
		o.IncludeLibraryDocs = enabled
	}
}

// ContextResult contains the assembled context and metadata.
type ContextResult struct {
	// Context is the formatted markdown context for LLM consumption.
	Context string `json:"context"`

	// TokensUsed is the estimated number of tokens used.
	TokensUsed int `json:"tokens_used"`

	// SymbolsIncluded lists the IDs of symbols included in context.
	SymbolsIncluded []string `json:"symbols_included"`

	// LibraryDocsIncluded lists the IDs of library docs included.
	LibraryDocsIncluded []string `json:"library_docs_included"`

	// Suggestions provides "also consider" hints for the user/agent.
	Suggestions []string `json:"suggestions"`

	// AssemblyDurationMs is how long assembly took in milliseconds.
	AssemblyDurationMs int64 `json:"assembly_duration_ms"`

	// Truncated indicates if results were limited by budget or timeout.
	Truncated bool `json:"truncated"`
}

// LibraryDoc represents documentation for an external library symbol.
type LibraryDoc struct {
	// DocID is a unique identifier for this documentation entry.
	DocID string `json:"doc_id"`

	// Library is the library name (e.g., "github.com/gin-gonic/gin").
	Library string `json:"library"`

	// Version is the library version (e.g., "v1.9.1").
	Version string `json:"version"`

	// SymbolPath is the fully qualified symbol path (e.g., "gin.Context.JSON").
	SymbolPath string `json:"symbol_path"`

	// SymbolKind is the type of symbol (function, type, method, etc.).
	SymbolKind string `json:"symbol_kind"`

	// Signature is the type signature (e.g., "func(code int, obj interface{})").
	Signature string `json:"signature"`

	// DocContent is the documentation text.
	DocContent string `json:"doc_content"`

	// Example is an optional usage example.
	Example string `json:"example,omitempty"`
}

// LibraryDocProvider is the interface for fetching library documentation.
//
// Implementations may query Weaviate, local caches, or external APIs.
// The interface allows graceful degradation when the provider is unavailable.
type LibraryDocProvider interface {
	// Search finds library documentation matching the query.
	//
	// Inputs:
	//   ctx - Context for cancellation
	//   query - Search query (library name, symbol name, etc.)
	//   limit - Maximum number of results to return
	//
	// Outputs:
	//   []LibraryDoc - Matching documentation entries
	//   error - Non-nil if search fails
	Search(ctx context.Context, query string, limit int) ([]LibraryDoc, error)
}

// ScoredSymbol represents a symbol with its relevance score.
type ScoredSymbol struct {
	// Symbol is the underlying symbol.
	Symbol *ast.Symbol

	// Score is the relevance score (0.0-1.0, higher is better).
	Score float64

	// Depth is the graph distance from entry points.
	Depth int
}

// SymbolImportance returns an importance weight for ranking.
//
// Functions and methods are most important for understanding code flow.
// Types and interfaces define contracts. Fields are least important alone.
func SymbolImportance(kind ast.SymbolKind) float64 {
	switch kind {
	case ast.SymbolKindFunction:
		return 1.0
	case ast.SymbolKindMethod:
		return 0.95
	case ast.SymbolKindInterface:
		return 0.85
	case ast.SymbolKindStruct, ast.SymbolKindClass:
		return 0.80
	case ast.SymbolKindType:
		return 0.75
	case ast.SymbolKindConstant:
		return 0.60
	case ast.SymbolKindVariable:
		return 0.55
	case ast.SymbolKindField:
		return 0.50
	default:
		return 0.40
	}
}
