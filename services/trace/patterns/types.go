// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package patterns provides design pattern and anti-pattern detection for Trace.
//
// # Description
//
// This package detects design patterns (Singleton, Factory, Builder, etc.),
// code smells (long functions, god objects), structural issues (circular deps),
// and coding conventions. Pattern detection is heuristic-based with confidence
// scores indicating certainty.
//
// # Thread Safety
//
// All detector types are safe for concurrent use.
package patterns

// PatternType identifies the design pattern.
type PatternType string

const (
	// PatternSingleton is a single instance pattern with thread-safety.
	PatternSingleton PatternType = "singleton"

	// PatternFactory creates objects without exposing instantiation logic.
	PatternFactory PatternType = "factory"

	// PatternBuilder constructs complex objects step by step.
	PatternBuilder PatternType = "builder"

	// PatternOptions uses functional options for configuration.
	PatternOptions PatternType = "options"

	// PatternMiddleware chains handlers.
	PatternMiddleware PatternType = "middleware"

	// PatternStrategy defines interchangeable algorithms.
	PatternStrategy PatternType = "strategy"

	// PatternObserver notifies observers of state changes.
	PatternObserver PatternType = "observer"

	// PatternRepository abstracts data access.
	PatternRepository PatternType = "repository"
)

// DetectedPattern represents a design pattern found in code.
type DetectedPattern struct {
	// Type identifies the pattern (singleton, factory, etc.).
	Type PatternType `json:"type"`

	// Location is the package or file where the pattern is found.
	Location string `json:"location"`

	// Components lists the symbols that form the pattern.
	Components []string `json:"components"`

	// Confidence is how certain we are (0.0 - 1.0).
	Confidence float64 `json:"confidence"`

	// Example shows a code snippet illustrating the pattern.
	Example string `json:"example,omitempty"`

	// Idiomatic indicates if this is the recommended implementation.
	Idiomatic bool `json:"idiomatic"`

	// Warnings lists issues with the implementation.
	Warnings []string `json:"warnings,omitempty"`
}

// PatternCandidate is a potential pattern match before validation.
type PatternCandidate struct {
	// SymbolIDs are the symbols that may form the pattern.
	SymbolIDs []string

	// Location is where the pattern was found.
	Location string

	// Metadata contains pattern-specific detection data.
	Metadata map[string]interface{}
}

// SmellType identifies the code smell category.
type SmellType string

const (
	// SmellLongFunction is a function with too many lines.
	SmellLongFunction SmellType = "long_function"

	// SmellLongParameterList is a function with too many parameters.
	SmellLongParameterList SmellType = "long_parameter_list"

	// SmellGodObject is a type with too many methods.
	SmellGodObject SmellType = "god_object"

	// SmellFeatureEnvy is excessive cross-package dependencies.
	SmellFeatureEnvy SmellType = "feature_envy"

	// SmellEmptyInterface is overuse of interface{}.
	SmellEmptyInterface SmellType = "empty_interface"

	// SmellErrorSwallowing is ignoring errors.
	SmellErrorSwallowing SmellType = "error_swallowing"

	// SmellMagicNumber is unexplained numeric literals.
	SmellMagicNumber SmellType = "magic_number"

	// SmellDeepNesting is excessive nesting depth.
	SmellDeepNesting SmellType = "deep_nesting"
)

// Severity indicates the importance of an issue.
type Severity string

const (
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

// CodeSmell represents a potential code quality issue.
type CodeSmell struct {
	// Type categorizes the smell.
	Type SmellType `json:"type"`

	// Severity indicates importance.
	Severity Severity `json:"severity"`

	// Location is the file and symbol.
	Location string `json:"location"`

	// Description explains the issue.
	Description string `json:"description"`

	// Suggestion provides a fix recommendation.
	Suggestion string `json:"suggestion"`

	// Code is the relevant code snippet.
	Code string `json:"code,omitempty"`

	// Value is the measured value (e.g., line count).
	Value int `json:"value,omitempty"`

	// Threshold is what triggered the smell.
	Threshold int `json:"threshold,omitempty"`
}

// CircularDep represents a circular dependency.
type CircularDep struct {
	// Cycle is the dependency chain [A, B, C, A].
	Cycle []string `json:"cycle"`

	// Type is package, type, or function.
	Type string `json:"type"`

	// Severity indicates importance.
	Severity Severity `json:"severity"`

	// Suggestion explains how to fix.
	Suggestion string `json:"suggestion"`

	// BreakPoints are recommended places to break the cycle.
	BreakPoints []string `json:"break_points"`
}

// Duplication represents duplicate or similar code.
type Duplication struct {
	// Type is exact, near, or structural.
	Type string `json:"type"`

	// Similarity is the match percentage (0.0 - 1.0).
	Similarity float64 `json:"similarity"`

	// Locations are where the duplicates exist.
	Locations []DupLocation `json:"locations"`

	// Code is a representative sample.
	Code string `json:"code,omitempty"`

	// Suggestion recommends extraction or refactoring.
	Suggestion string `json:"suggestion"`

	// Confidence in the duplication detection.
	Confidence float64 `json:"confidence"`
}

// DupLocation is a location of duplicated code.
type DupLocation struct {
	// FilePath is the source file.
	FilePath string `json:"file_path"`

	// LineStart is the 1-indexed starting line.
	LineStart int `json:"line_start"`

	// LineEnd is the 1-indexed ending line.
	LineEnd int `json:"line_end"`

	// SymbolID is the containing symbol (if applicable).
	SymbolID string `json:"symbol_id,omitempty"`
}

// Convention represents an observed coding convention.
type Convention struct {
	// Type categorizes the convention (naming, error_handling, etc.).
	Type string `json:"type"`

	// Pattern describes the convention pattern.
	Pattern string `json:"pattern"`

	// Examples shows instances of the convention.
	Examples []string `json:"examples"`

	// Frequency is how often the convention is followed (0.0 - 1.0).
	Frequency float64 `json:"frequency"`

	// Description explains the convention.
	Description string `json:"description"`
}

// DeadCode represents unreferenced code.
type DeadCode struct {
	// Type is function, type, variable, or constant.
	Type string `json:"type"`

	// Name is the symbol name.
	Name string `json:"name"`

	// FilePath is the source file.
	FilePath string `json:"file_path"`

	// Line is the 1-indexed line number.
	Line int `json:"line"`

	// Reason explains why it's considered dead.
	Reason string `json:"reason"`

	// Confidence indicates certainty (lower for edge cases).
	Confidence float64 `json:"confidence"`
}

// DetectionOptions configures pattern detection.
type DetectionOptions struct {
	// Patterns limits detection to specific patterns (empty = all).
	Patterns []PatternType

	// MinConfidence filters results below this threshold.
	MinConfidence float64

	// IncludeNonIdiomatic includes non-idiomatic implementations.
	IncludeNonIdiomatic bool
}

// SmellThresholds configures code smell detection.
type SmellThresholds struct {
	// MaxFunctionLines triggers long_function smell.
	MaxFunctionLines int

	// MaxParameters triggers long_parameter_list smell.
	MaxParameters int

	// MaxMethodCount triggers god_object smell.
	MaxMethodCount int

	// MaxNestingDepth triggers deep_nesting smell.
	MaxNestingDepth int
}

// DefaultSmellThresholds returns standard thresholds.
func DefaultSmellThresholds() SmellThresholds {
	return SmellThresholds{
		MaxFunctionLines: 50,
		MaxParameters:    5,
		MaxMethodCount:   20,
		MaxNestingDepth:  4,
	}
}

// Confidence base values for calibration.
const (
	// StructuralMatchBase is confidence when only structure matches.
	StructuralMatchBase = 0.6

	// IdiomaticMatchBase is confidence for idiomatic implementations.
	IdiomaticMatchBase = 0.9

	// HeuristicMatchBase is confidence for threshold-based detection.
	HeuristicMatchBase = 0.5
)

// Confidence adjustments.
const (
	// MultipleExamplesBoost increases confidence for repeated patterns.
	MultipleExamplesBoost = 1.2

	// SingleExamplePenalty decreases confidence for single occurrences.
	SingleExamplePenalty = 0.8

	// PartialMatchPenalty decreases confidence for incomplete matches.
	PartialMatchPenalty = 0.7
)
