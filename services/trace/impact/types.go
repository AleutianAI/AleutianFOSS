// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package impact provides unified change impact analysis for Trace.
//
// # Description
//
// This package orchestrates multiple analysis tools (blast radius, breaking
// changes, test coverage, side effects, data flow) to provide a comprehensive
// "what happens if I change this?" report. It is designed to be the primary
// tool agents use before making changes.
//
// # Thread Safety
//
// All types in this package are safe for concurrent use after initialization.
package impact

import (
	"github.com/AleutianAI/AleutianFOSS/services/trace/analysis"
	"github.com/AleutianAI/AleutianFOSS/services/trace/reason"
)

// RiskLevel indicates the overall risk of a proposed change.
type RiskLevel string

const (
	// RiskCritical indicates very high risk - many callers, breaking changes, no tests.
	RiskCritical RiskLevel = "CRITICAL"

	// RiskHigh indicates high risk - significant impact that needs careful review.
	RiskHigh RiskLevel = "HIGH"

	// RiskMedium indicates moderate risk - some impact but manageable.
	RiskMedium RiskLevel = "MEDIUM"

	// RiskLow indicates low risk - minimal impact, well-tested.
	RiskLow RiskLevel = "LOW"
)

// CoverageLevel indicates how well a symbol is tested.
type CoverageLevel string

const (
	// CoverageGood indicates the symbol has direct test coverage.
	CoverageGood CoverageLevel = "good"

	// CoveragePartial indicates indirect coverage only.
	CoveragePartial CoverageLevel = "partial"

	// CoverageNone indicates no test coverage detected.
	CoverageNone CoverageLevel = "none"
)

// ChangeImpact is the unified result of change impact analysis.
//
// # Description
//
// Combines results from blast radius, breaking change, test coverage,
// side effect, and data flow analysis into a single actionable report.
// Includes an overall risk score and suggested actions.
//
// # Thread Safety
//
// ChangeImpact is immutable after creation.
type ChangeImpact struct {
	// TargetID is the symbol being analyzed.
	TargetID string `json:"target_id"`

	// TargetName is the human-readable name of the target.
	TargetName string `json:"target_name"`

	// ProposedChange describes what change is being analyzed.
	// May be a new signature, description, or empty for general impact.
	ProposedChange string `json:"proposed_change,omitempty"`

	// --- Breaking Change Analysis ---

	// IsBreaking indicates if the proposed change would break callers.
	IsBreaking bool `json:"is_breaking"`

	// BreakingChanges lists specific breaking changes detected.
	BreakingChanges []reason.BreakingChange `json:"breaking_changes,omitempty"`

	// --- Blast Radius ---

	// DirectCallers is the count of functions that directly call the target.
	DirectCallers int `json:"direct_callers"`

	// IndirectCallers is the count of functions that transitively call the target.
	IndirectCallers int `json:"indirect_callers"`

	// TotalImpact is the total number of symbols affected (direct + indirect + implementers).
	TotalImpact int `json:"total_impact"`

	// FilesAffected lists unique files that may need changes.
	FilesAffected []string `json:"files_affected,omitempty"`

	// BlastRiskLevel is the risk level from blast radius analysis.
	BlastRiskLevel analysis.RiskLevel `json:"blast_risk_level"`

	// --- Test Coverage ---

	// TestsCovering is the count of tests that cover this symbol.
	TestsCovering int `json:"tests_covering"`

	// CoverageLevel indicates how well the symbol is tested.
	CoverageLevel CoverageLevel `json:"coverage_level"`

	// TestFiles lists test files that should be run after changes.
	TestFiles []string `json:"test_files,omitempty"`

	// --- Side Effects ---

	// HasSideEffects indicates if the target has external effects.
	HasSideEffects bool `json:"has_side_effects"`

	// SideEffectCount is the total number of side effects.
	SideEffectCount int `json:"side_effect_count"`

	// SideEffectTypes lists the categories of side effects (file_io, network, etc.).
	SideEffectTypes []string `json:"side_effect_types,omitempty"`

	// SideEffects contains detailed side effect information.
	SideEffects []reason.SideEffect `json:"side_effects,omitempty"`

	// --- Data Flow ---

	// DataSinks lists where data from this function ends up (response, DB, file, etc.).
	DataSinks []string `json:"data_sinks,omitempty"`

	// --- Overall Assessment ---

	// RiskLevel is the overall risk assessment.
	RiskLevel RiskLevel `json:"risk_level"`

	// RiskScore is the numeric risk score (0.0 = safe, 1.0 = very risky).
	RiskScore float64 `json:"risk_score"`

	// Confidence is how confident we are in the analysis (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// SuggestedActions lists recommended actions before making the change.
	SuggestedActions []string `json:"suggested_actions"`

	// Warnings contains important warnings about the change.
	Warnings []string `json:"warnings,omitempty"`

	// Limitations lists what we couldn't analyze.
	Limitations []string `json:"limitations,omitempty"`
}

// AnalyzeOptions configures the impact analysis.
type AnalyzeOptions struct {
	// IncludeBreaking enables breaking change analysis (requires proposed change).
	IncludeBreaking bool

	// IncludeBlastRadius enables blast radius analysis.
	IncludeBlastRadius bool

	// IncludeCoverage enables test coverage analysis.
	IncludeCoverage bool

	// IncludeSideEffects enables side effect analysis.
	IncludeSideEffects bool

	// IncludeDataFlow enables data flow analysis.
	IncludeDataFlow bool

	// MaxCallers limits the number of callers to analyze.
	MaxCallers int

	// MaxHops limits the depth of indirect caller analysis.
	MaxHops int

	// IncludeDetailedSideEffects includes full side effect details in response.
	IncludeDetailedSideEffects bool

	// IncludeFileLists includes file lists in response.
	IncludeFileLists bool
}

// DefaultAnalyzeOptions returns options with all analyses enabled.
func DefaultAnalyzeOptions() AnalyzeOptions {
	return AnalyzeOptions{
		IncludeBreaking:            true,
		IncludeBlastRadius:         true,
		IncludeCoverage:            true,
		IncludeSideEffects:         true,
		IncludeDataFlow:            true,
		MaxCallers:                 100,
		MaxHops:                    3,
		IncludeDetailedSideEffects: false, // Keep response compact by default
		IncludeFileLists:           true,
	}
}

// QuickAnalyzeOptions returns options for fast analysis (blast radius + coverage only).
func QuickAnalyzeOptions() AnalyzeOptions {
	return AnalyzeOptions{
		IncludeBreaking:    false,
		IncludeBlastRadius: true,
		IncludeCoverage:    true,
		IncludeSideEffects: false,
		IncludeDataFlow:    false,
		MaxCallers:         50,
		MaxHops:            2,
		IncludeFileLists:   false,
	}
}

// RiskWeights defines the weights for risk score calculation.
type RiskWeights struct {
	Breaking     float64 // Weight for breaking changes (default 0.30)
	BlastRadius  float64 // Weight for caller count (default 0.25)
	TestCoverage float64 // Weight for test coverage (default 0.20)
	SideEffects  float64 // Weight for side effects (default 0.15)
	Exported     float64 // Weight for public API exposure (default 0.10)
}

// DefaultRiskWeights returns the default risk calculation weights.
func DefaultRiskWeights() RiskWeights {
	return RiskWeights{
		Breaking:     0.30,
		BlastRadius:  0.25,
		TestCoverage: 0.20,
		SideEffects:  0.15,
		Exported:     0.10,
	}
}
