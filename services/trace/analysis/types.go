// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package analysis provides code analysis tools for Trace.
//
// # Description
//
// This package implements analysis tools for understanding code impact,
// including blast radius analysis which calculates the effect of changing
// a function or type on the rest of the codebase.
//
// # Thread Safety
//
// All analyzer types are safe for concurrent use.
package analysis

import "time"

// RiskLevel indicates the risk associated with a change.
type RiskLevel string

const (
	// RiskCritical means the change affects many callers or interfaces.
	// Examples: 20+ direct callers, changing an interface, shared utility.
	RiskCritical RiskLevel = "CRITICAL"

	// RiskHigh means significant impact but manageable.
	// Examples: 10-19 direct callers, exported public API.
	RiskHigh RiskLevel = "HIGH"

	// RiskMedium means moderate impact.
	// Examples: 4-9 direct callers, same package.
	RiskMedium RiskLevel = "MEDIUM"

	// RiskLow means minimal impact.
	// Examples: 0-3 direct callers, unexported.
	RiskLow RiskLevel = "LOW"
)

// BlastRadius contains the analysis results for a potential change.
//
// # Description
//
// Provides comprehensive information about what would be affected
// if the target symbol is modified. Used by agents to make informed
// decisions before generating patches.
//
// # Fields
//
//   - Target: The symbol being analyzed (function, type, etc.).
//   - RiskLevel: Overall risk assessment.
//   - DirectCallers: Functions that directly call the target.
//   - IndirectCallers: Functions that call functions that call the target.
//   - Implementers: Types implementing an interface (if target is interface).
//   - SharedDeps: Dependencies shared by target and its callers.
//   - FilesAffected: Unique files that may need changes.
//   - TestFiles: Test files that should be run.
//   - Summary: Human-readable summary.
//   - Recommendation: Actionable advice.
type BlastRadius struct {
	Target          string        `json:"target"`
	RiskLevel       RiskLevel     `json:"risk_level"`
	DirectCallers   []Caller      `json:"direct_callers"`
	IndirectCallers []Caller      `json:"indirect_callers"`
	Implementers    []Implementer `json:"implementers,omitempty"`
	SharedDeps      []SharedDep   `json:"shared_deps,omitempty"`
	FilesAffected   []string      `json:"files_affected"`
	TestFiles       []string      `json:"test_files"`
	Summary         string        `json:"summary"`
	Recommendation  string        `json:"recommendation"`
	Truncated       bool          `json:"truncated,omitempty"`
	TruncatedReason string        `json:"truncated_reason,omitempty"`
}

// Caller represents a function that calls the target.
//
// # Fields
//
//   - ID: Unique symbol identifier.
//   - Name: Human-readable function name.
//   - FilePath: File containing the caller.
//   - Line: Line number of the call.
//   - Hops: Distance from target (1 = direct, 2+ = indirect).
type Caller struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Hops     int    `json:"hops"`
}

// Implementer represents a type that implements an interface.
//
// # Fields
//
//   - TypeID: Unique identifier of the implementing type.
//   - TypeName: Human-readable type name.
//   - MethodID: If target is interface method, the implementing method.
//   - MethodName: Human-readable method name.
//   - FilePath: File containing the implementation.
//   - Line: Line number of the implementation.
type Implementer struct {
	TypeID     string `json:"type_id"`
	TypeName   string `json:"type_name"`
	MethodID   string `json:"method_id,omitempty"`
	MethodName string `json:"method_name,omitempty"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
}

// SharedDep represents a dependency shared between target and callers.
//
// # Description
//
// A shared dependency creates a "diamond" pattern where both the target
// and its callers depend on the same symbol. Changes to shared deps
// can have cascading effects.
//
// # Fields
//
//   - ID: Unique symbol identifier.
//   - Name: Human-readable name.
//   - UsedBy: Callers that also use this dependency.
//   - UsageType: Type of shared usage ("diamond", "common_utility").
//   - Warning: Human-readable warning message.
type SharedDep struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	UsedBy    []string `json:"used_by"`
	UsageType string   `json:"usage_type"`
	Warning   string   `json:"warning"`
}

// AnalyzeOptions configures the blast radius analysis.
//
// # Fields
//
//   - MaxDirectCallers: Stop expanding direct callers after this limit.
//   - MaxIndirectCallers: Total indirect caller limit.
//   - MaxHops: How far to trace indirect callers (default 3).
//   - Timeout: Maximum analysis time.
//   - TestPatterns: Glob patterns for test files (default ["*_test.go"]).
//   - TestDirs: Additional directories to search for tests.
type AnalyzeOptions struct {
	MaxDirectCallers   int           `json:"max_direct_callers"`
	MaxIndirectCallers int           `json:"max_indirect_callers"`
	MaxHops            int           `json:"max_hops"`
	Timeout            time.Duration `json:"timeout"`
	TestPatterns       []string      `json:"test_patterns"`
	TestDirs           []string      `json:"test_dirs"`
}

// DefaultAnalyzeOptions returns options with sensible defaults.
func DefaultAnalyzeOptions() AnalyzeOptions {
	return AnalyzeOptions{
		MaxDirectCallers:   100,
		MaxIndirectCallers: 500,
		MaxHops:            3,
		Timeout:            500 * time.Millisecond,
		TestPatterns:       []string{"*_test.go"},
		TestDirs:           nil,
	}
}

// RiskConfig allows customizing risk level thresholds.
//
// # Description
//
// Default thresholds work for typical projects but can be adjusted
// for utility libraries where functions commonly have many callers.
//
// # Fields
//
//   - CriticalThreshold: Direct callers >= this is CRITICAL (default 20).
//   - HighThreshold: Direct callers >= this is HIGH (default 10).
//   - MediumThreshold: Direct callers >= this is MEDIUM (default 4).
type RiskConfig struct {
	CriticalThreshold int `json:"critical_threshold"`
	HighThreshold     int `json:"high_threshold"`
	MediumThreshold   int `json:"medium_threshold"`
}

// DefaultRiskConfig returns risk thresholds with sensible defaults.
func DefaultRiskConfig() RiskConfig {
	return RiskConfig{
		CriticalThreshold: 20,
		HighThreshold:     10,
		MediumThreshold:   4,
	}
}
