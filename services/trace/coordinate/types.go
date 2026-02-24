// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package coordinate provides multi-file change coordination for Trace.
//
// # Description
//
// This package enables planning, validating, and previewing coordinated changes
// across multiple files. It is READ-ONLY - it analyzes and generates plans but
// does not modify files. The agent uses Edit tools after approval.
//
// # Thread Safety
//
// All types in this package are safe for concurrent use after initialization.
package coordinate

// ChangeType categorizes the kind of change being made.
type ChangeType string

const (
	// ChangeAddParameter adds a new parameter to a function.
	ChangeAddParameter ChangeType = "add_parameter"

	// ChangeRemoveParameter removes a parameter from a function.
	ChangeRemoveParameter ChangeType = "remove_parameter"

	// ChangeAddReturn adds a new return value.
	ChangeAddReturn ChangeType = "add_return"

	// ChangeRemoveReturn removes a return value.
	ChangeRemoveReturn ChangeType = "remove_return"

	// ChangeAddMethod adds a new method to an interface.
	ChangeAddMethod ChangeType = "add_method"

	// ChangeRenameSymbol renames a function, type, or variable.
	ChangeRenameSymbol ChangeType = "rename_symbol"

	// ChangeChangeType changes a field or parameter type.
	ChangeChangeType ChangeType = "change_type"

	// ChangeMoveSymbol moves a symbol to a different package.
	ChangeMoveSymbol ChangeType = "move_symbol"
)

// FileChangeType categorizes how a file is affected.
type FileChangeType string

const (
	// FileChangePrimary is the file containing the primary change target.
	FileChangePrimary FileChangeType = "primary"

	// FileChangeCallerUpdate updates a caller to match new signature.
	FileChangeCallerUpdate FileChangeType = "caller_update"

	// FileChangeImportUpdate updates import statements.
	FileChangeImportUpdate FileChangeType = "import_update"

	// FileChangeImplementerUpdate updates interface implementations.
	FileChangeImplementerUpdate FileChangeType = "implementer_update"

	// FileChangeReferenceUpdate updates type or variable references.
	FileChangeReferenceUpdate FileChangeType = "reference_update"
)

// RiskLevel indicates the risk of a change plan.
type RiskLevel string

const (
	RiskCritical RiskLevel = "CRITICAL"
	RiskHigh     RiskLevel = "HIGH"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskLow      RiskLevel = "LOW"
)

// ChangeRequest describes a single change the user wants to make.
type ChangeRequest struct {
	// TargetID is the symbol ID to change.
	TargetID string `json:"target_id"`

	// ChangeType categorizes the change.
	ChangeType ChangeType `json:"change_type"`

	// NewSignature is the proposed new signature (for signature changes).
	NewSignature string `json:"new_signature,omitempty"`

	// NewName is the proposed new name (for renames).
	NewName string `json:"new_name,omitempty"`

	// NewPackage is the target package (for moves).
	NewPackage string `json:"new_package,omitempty"`

	// Description explains the change in human terms.
	Description string `json:"description,omitempty"`
}

// ChangeSet is a collection of related changes to coordinate.
type ChangeSet struct {
	// PrimaryChange is the main change being requested.
	PrimaryChange ChangeRequest `json:"primary_change"`

	// Description explains the overall change set.
	Description string `json:"description"`
}

// FileChange describes changes needed for a single file.
type FileChange struct {
	// FilePath is the relative path to the file.
	FilePath string `json:"file_path"`

	// SymbolID is the symbol being changed (if applicable).
	SymbolID string `json:"symbol_id,omitempty"`

	// ChangeType categorizes how this file is affected.
	ChangeType FileChangeType `json:"change_type"`

	// CurrentCode is the current code that will be replaced.
	CurrentCode string `json:"current_code"`

	// ProposedCode is the new code to use.
	ProposedCode string `json:"proposed_code"`

	// StartLine is the 1-indexed starting line of the change.
	StartLine int `json:"start_line"`

	// EndLine is the 1-indexed ending line of the change.
	EndLine int `json:"end_line"`

	// Reason explains why this change is needed.
	Reason string `json:"reason"`
}

// ChangePlan is the coordinated plan for all files.
type ChangePlan struct {
	// ID is a unique identifier for this plan.
	ID string `json:"plan_id"`

	// GraphID is the ID of the graph this plan was created for.
	GraphID string `json:"graph_id,omitempty"`

	// Description explains the overall change.
	Description string `json:"description"`

	// PrimaryChange is the original change request.
	PrimaryChange ChangeRequest `json:"primary_change"`

	// FileChanges lists all changes needed across files.
	FileChanges []FileChange `json:"file_changes"`

	// Order lists file paths in the order changes should be applied.
	// Target file first, then dependent files.
	Order []string `json:"order"`

	// TotalFiles is the number of files affected.
	TotalFiles int `json:"total_files"`

	// TotalChanges is the total number of individual changes.
	TotalChanges int `json:"total_changes"`

	// RiskLevel is the overall risk assessment.
	RiskLevel RiskLevel `json:"risk_level"`

	// Confidence is how confident we are in the plan (0.0-1.0).
	Confidence float64 `json:"confidence"`

	// Warnings contains important warnings about the plan.
	Warnings []string `json:"warnings,omitempty"`

	// Limitations lists what we couldn't analyze.
	Limitations []string `json:"limitations,omitempty"`

	// CreatedAt is when the plan was created (Unix milliseconds UTC).
	CreatedAt int64 `json:"created_at"`
}

// GetID returns the plan's unique identifier.
//
// Description:
//
//	Returns the plan ID for storage and retrieval.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *ChangePlan) GetID() string {
	return p.ID
}

// GetGraphID returns the graph ID this plan was created for.
//
// Description:
//
//	Returns the graph ID for plan retrieval and validation.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *ChangePlan) GetGraphID() string {
	return p.GraphID
}

// Hunk represents a section of changed lines in a diff.
type Hunk struct {
	// StartLine is the 1-indexed starting line in the old file.
	StartLine int `json:"start_line"`

	// OldLines are the lines being removed (with - prefix).
	OldLines []string `json:"old_lines"`

	// NewLines are the lines being added (with + prefix).
	NewLines []string `json:"new_lines"`

	// Context contains context lines around the change.
	Context []string `json:"context,omitempty"`
}

// FileDiff represents a unified diff for a single file.
type FileDiff struct {
	// FilePath is the relative path to the file.
	FilePath string `json:"file_path"`

	// Hunks contains the diff sections.
	Hunks []Hunk `json:"hunks"`

	// LinesAdded is the total lines added.
	LinesAdded int `json:"lines_added"`

	// LinesRemoved is the total lines removed.
	LinesRemoved int `json:"lines_removed"`

	// ChangeType is how this file is affected.
	ChangeType FileChangeType `json:"change_type"`

	// Reason explains why this file is changing.
	Reason string `json:"reason"`
}

// ValidationError represents a validation failure.
type ValidationError struct {
	// FilePath is the file containing the error.
	FilePath string `json:"file_path"`

	// Line is the 1-indexed line number.
	Line int `json:"line"`

	// Message describes the error.
	Message string `json:"message"`

	// Severity indicates how serious the error is.
	Severity string `json:"severity"` // "error", "warning"
}

// ValidationResult is the result of validating a change plan.
type ValidationResult struct {
	// Valid indicates if the plan is valid.
	Valid bool `json:"valid"`

	// SyntaxErrors contains parsing errors.
	SyntaxErrors []ValidationError `json:"syntax_errors,omitempty"`

	// TypeErrors contains type-related errors.
	TypeErrors []ValidationError `json:"type_errors,omitempty"`

	// ImportErrors contains import resolution errors.
	ImportErrors []ValidationError `json:"import_errors,omitempty"`

	// Warnings contains non-fatal issues.
	Warnings []string `json:"warnings,omitempty"`
}

// PlanOptions configures change plan generation.
type PlanOptions struct {
	// MaxCallers limits how many callers to update.
	MaxCallers int

	// MaxHops limits indirect caller depth.
	MaxHops int

	// IncludeTests includes test file updates.
	IncludeTests bool

	// GenerateStubs generates stub implementations for interface changes.
	GenerateStubs bool

	// ContextLines is the number of context lines in diffs.
	ContextLines int
}

// DefaultPlanOptions returns sensible defaults.
func DefaultPlanOptions() PlanOptions {
	return PlanOptions{
		MaxCallers:    50,
		MaxHops:       2,
		IncludeTests:  true,
		GenerateStubs: true,
		ContextLines:  3,
	}
}
