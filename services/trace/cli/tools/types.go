// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package tools provides the tool registry and execution framework for the agent.
//
// Tools are the primary mechanism for the agent to interact with the codebase
// and external systems. Each tool is defined by a ToolDefinition that describes
// its parameters and capabilities, and implements the Tool interface for execution.
//
// Thread Safety:
//
//	All types in this package are designed for concurrent use.
package tools

import (
	"context"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
)

// ToolCategory represents the category a tool belongs to.
type ToolCategory string

const (
	// CategoryExploration includes tools for exploring and understanding code.
	CategoryExploration ToolCategory = "exploration"

	// CategoryReasoning includes tools for reasoning about code.
	CategoryReasoning ToolCategory = "reasoning"

	// CategorySafety includes tools for security analysis.
	CategorySafety ToolCategory = "safety"

	// CategoryFile includes tools for file operations.
	CategoryFile ToolCategory = "file"
)

// String returns the string representation of the category.
func (c ToolCategory) String() string {
	return string(c)
}

// ParamType represents the type of a tool parameter.
type ParamType string

const (
	// ParamTypeString is a string parameter.
	ParamTypeString ParamType = "string"

	// ParamTypeInt is an integer parameter.
	ParamTypeInt ParamType = "integer"

	// ParamTypeFloat is a floating-point parameter.
	ParamTypeFloat ParamType = "number"

	// ParamTypeBool is a boolean parameter.
	ParamTypeBool ParamType = "boolean"

	// ParamTypeArray is an array parameter.
	ParamTypeArray ParamType = "array"

	// ParamTypeObject is an object parameter.
	ParamTypeObject ParamType = "object"
)

// ParamDef defines a single parameter for a tool.
//
// NOTE: This struct uses `any` types for Default and Enum to support JSON schema
// flexibility. The default value and enum options can be any JSON-compatible type
// (string, number, boolean, object, array). This is an intentional exception to
// CLAUDE.md Section 4.5 ("no map[string]any") because tool definitions require
// schema flexibility for LLM tool calling. All values are validated at runtime
// when the tool is invoked.
type ParamDef struct {
	// Type is the parameter type.
	Type ParamType `json:"type"`

	// Description explains what the parameter is for.
	Description string `json:"description"`

	// Required indicates if the parameter must be provided.
	Required bool `json:"required"`

	// Default is the default value if not provided.
	// Uses `any` for JSON schema flexibility - can be string, number, boolean, etc.
	Default any `json:"default,omitempty"`

	// Enum restricts values to a set of options.
	// Uses `[]any` for JSON schema flexibility - enum values match the param type.
	Enum []any `json:"enum,omitempty"`

	// MinLength is the minimum string length (for string type).
	MinLength int `json:"minLength,omitempty"`

	// MaxLength is the maximum string length (for string type).
	MaxLength int `json:"maxLength,omitempty"`

	// Minimum is the minimum value (for numeric types).
	Minimum *float64 `json:"minimum,omitempty"`

	// Maximum is the maximum value (for numeric types).
	Maximum *float64 `json:"maximum,omitempty"`

	// Items defines array item type (for array type).
	Items *ParamDef `json:"items,omitempty"`

	// Properties defines object properties (for object type).
	Properties map[string]ParamDef `json:"properties,omitempty"`
}

// ToolDefinition describes a tool's interface for the LLM.
//
// This structure is designed to be serializable to JSON Schema format
// for use with LLM tool calling APIs.
type ToolDefinition struct {
	// Name is the unique identifier for the tool.
	Name string `json:"name"`

	// Description explains what the tool does.
	Description string `json:"description"`

	// Parameters defines the input parameters.
	Parameters map[string]ParamDef `json:"parameters"`

	// Category is the tool category (exploration, reasoning, safety, file).
	Category ToolCategory `json:"category"`

	// Priority influences tool selection (higher = prefer).
	Priority int `json:"priority"`

	// Requires lists dependencies (e.g., "graph_initialized").
	Requires []string `json:"requires,omitempty"`

	// SideEffects indicates if the tool modifies state.
	SideEffects bool `json:"side_effects"`

	// Timeout is the default execution timeout.
	Timeout time.Duration `json:"timeout,omitempty"`

	// Examples provides usage examples.
	Examples []ToolExample `json:"examples,omitempty"`

	// WhenToUse provides structured routing guidance for the tool router.
	// CB-31e: Part of the tool contract - every new tool should define this.
	WhenToUse WhenToUse `json:"when_to_use,omitempty" yaml:"when_to_use,omitempty"`
}

// ToolExample provides an example invocation for a tool.
type ToolExample struct {
	// Description explains what the example demonstrates.
	Description string `json:"description"`

	// Parameters are the example input parameters.
	Parameters map[string]any `json:"parameters"`

	// ExpectedOutput describes what output to expect.
	ExpectedOutput string `json:"expected_output,omitempty"`
}

// WhenToUse provides structured guidance for tool selection.
//
// Description:
//
//	This is part of the tool contract - every new tool must define this
//	to enable accurate routing by the tool router. Keywords are matched
//	against user queries for fast O(1) lookup.
//
// Thread Safety: WhenToUse is immutable after initialization.
//
// CB-31e: This struct addresses the router inefficiency where BestFor
// was never populated, leading to poor tool selection.
type WhenToUse struct {
	// Keywords are query terms that should trigger this tool.
	// Router matches these against the user's query for fast selection.
	// Example: ["callers", "who calls", "upstream", "incoming calls"]
	Keywords []string `json:"keywords" yaml:"keywords"`

	// UseWhen describes scenarios where this tool is the right choice.
	// Example: "User asks who or what calls a specific function"
	UseWhen string `json:"use_when" yaml:"use_when"`

	// AvoidWhen describes scenarios where this tool should NOT be used.
	// Example: "User asks what a function calls (use find_callees instead)"
	AvoidWhen string `json:"avoid_when,omitempty" yaml:"avoid_when,omitempty"`

	// InsteadOf lists tools this tool should replace in specific scenarios.
	// This helps the router learn substitution patterns.
	InsteadOf []ToolSubstitution `json:"instead_of,omitempty" yaml:"instead_of,omitempty"`
}

// ToolSubstitution describes when one tool should be used instead of another.
//
// Description:
//
//	Captures tool substitution patterns to help the router learn that
//	specialized tools (like find_callers) should be preferred over
//	generic tools (like Grep) for specific query types.
//
// Example:
//
//	{Tool: "Grep", When: "searching for function call patterns"}
type ToolSubstitution struct {
	// Tool is the name of the tool to replace.
	Tool string `json:"tool" yaml:"tool"`

	// When describes the scenario where substitution applies.
	When string `json:"when" yaml:"when"`
}

// RequiredParams returns a list of required parameter names.
func (d *ToolDefinition) RequiredParams() []string {
	var required []string
	for name, param := range d.Parameters {
		if param.Required {
			required = append(required, name)
		}
	}
	return required
}

// TypedParams is the interface for strongly-typed tool parameter structs.
//
// Description:
//
//	Each tool's Params struct (e.g., FindCallersParams) implements this
//	interface to provide type-safe parameter construction with a single
//	conversion point to the map[string]any required by Tool.Execute().
//
// Thread Safety: Implementations must be safe to call ToMap() concurrently.
type TypedParams interface {
	// ToMap converts the typed parameters to the untyped map consumed
	// by Tool.Execute(). This is the ONLY place where typed params
	// become untyped — all upstream code works with concrete types.
	ToMap() map[string]any

	// ToolName returns the name of the tool these parameters belong to.
	// Used for validation and logging.
	ToolName() string
}

// MapParams wraps a raw map[string]any (from LLM tool calls) as a TypedParams.
//
// Description:
//
//	When the LLM produces tool calls, the parameters arrive as map[string]any
//	from JSON unmarshaling. MapParams wraps these raw maps to satisfy the
//	TypedParams interface, allowing a single Execute(ctx, TypedParams) signature
//	for both the forced-tool path (which produces concrete typed params) and
//	the LLM/executor path (which produces raw maps).
//
// Thread Safety: Safe for concurrent use (value type, map is not mutated).
type MapParams struct {
	// Tool is the name of the tool these parameters belong to.
	Tool string

	// Params is the raw parameter map from JSON unmarshaling.
	Params map[string]any
}

// ToolName returns the tool name.
func (p MapParams) ToolName() string { return p.Tool }

// ToMap returns the underlying parameter map.
func (p MapParams) ToMap() map[string]any { return p.Params }

// EmptyParams represents a tool invocation with no parameters.
//
// Description:
//
//	Used by tools like list_packages, find_entry_points, and
//	find_extractable_regions that require no input parameters.
//
// Thread Safety: Safe for concurrent use (immutable after creation).
type EmptyParams struct {
	// Tool is the name of the tool these empty parameters belong to.
	Tool string
}

// ToolName returns the tool name for empty parameters.
func (p EmptyParams) ToolName() string { return p.Tool }

// ToMap returns an empty parameter map.
func (p EmptyParams) ToMap() map[string]any { return map[string]any{} }

// GrepToolParams contains parameters for the Grep tool when invoked
// via the forced-tool path.
//
// Description:
//
//	The Grep tool is an external (non-graph) tool with its own parameter
//	contract. This struct provides type safety for the forced-tool extraction
//	path in extractToolParameters.
//
// Thread Safety: Safe for concurrent use (value type).
type GrepToolParams struct {
	// Pattern is the search pattern.
	Pattern string

	// OutputMode controls output format ("content", "files_with_matches", "count").
	OutputMode string
}

// ToolName returns "Grep".
func (p GrepToolParams) ToolName() string { return "Grep" }

// ToMap converts GrepToolParams to the map consumed by Grep.Execute().
func (p GrepToolParams) ToMap() map[string]any {
	return map[string]any{
		"pattern":     p.Pattern,
		"output_mode": p.OutputMode,
	}
}

// GraphOverviewParams contains parameters for the graph_overview tool.
//
// Thread Safety: Safe for concurrent use (value type).
type GraphOverviewParams struct {
	// Depth controls traversal depth (1-3).
	Depth int

	// IncludeDependencies includes inter-package dependency graph.
	IncludeDependencies bool

	// IncludeMetrics includes symbol counts and file metrics.
	IncludeMetrics bool
}

// ToolName returns "graph_overview".
func (p GraphOverviewParams) ToolName() string { return "graph_overview" }

// ToMap converts GraphOverviewParams to the map consumed by graph_overview.Execute().
func (p GraphOverviewParams) ToMap() map[string]any {
	return map[string]any{
		"depth":                p.Depth,
		"include_dependencies": p.IncludeDependencies,
		"include_metrics":      p.IncludeMetrics,
	}
}

// ExplorePackageParams contains parameters for the explore_package tool.
//
// Thread Safety: Safe for concurrent use (value type).
type ExplorePackageParams struct {
	// Package is the package name or path to explore.
	Package string

	// IncludeDependencies shows packages this package imports.
	IncludeDependencies bool

	// IncludeDependents shows packages that import this package.
	IncludeDependents bool
}

// ToolName returns "explore_package".
func (p ExplorePackageParams) ToolName() string { return "explore_package" }

// ToMap converts ExplorePackageParams to the map consumed by explore_package.Execute().
func (p ExplorePackageParams) ToMap() map[string]any {
	return map[string]any{
		"package":              p.Package,
		"include_dependencies": p.IncludeDependencies,
		"include_dependents":   p.IncludeDependents,
	}
}

// Tool defines the interface for executable tools.
//
// Implementations must be safe for concurrent use.
type Tool interface {
	// Name returns the unique tool name.
	Name() string

	// Category returns the tool category.
	Category() ToolCategory

	// Definition returns the tool's parameter schema.
	Definition() ToolDefinition

	// Execute runs the tool with the given parameters.
	//
	// Inputs:
	//   ctx - Context for cancellation and timeout
	//   params - Typed parameters (use params.ToMap() to access raw map)
	//
	// Outputs:
	//   *Result - Execution result
	//   error - Non-nil if execution failed
	Execute(ctx context.Context, params TypedParams) (*Result, error)
}

// Result contains the outcome of a tool execution.
type Result struct {
	// Success indicates if the tool succeeded.
	Success bool `json:"success"`

	// Output is the tool's output data.
	Output any `json:"output"`

	// OutputText is a text representation of the output.
	OutputText string `json:"output_text"`

	// Error contains any error message.
	Error string `json:"error,omitempty"`

	// Duration is how long execution took.
	Duration time.Duration `json:"duration"`

	// TokensUsed estimates the output token count.
	TokensUsed int `json:"tokens_used"`

	// Cached indicates if result came from cache.
	Cached bool `json:"cached"`

	// Truncated indicates if output was truncated.
	Truncated bool `json:"truncated"`

	// ModifiedFiles lists files written or modified by this tool.
	// Used by DirtyTracker to trigger incremental graph refresh.
	// Tools that write to the file system should populate this.
	ModifiedFiles []string `json:"modified_files,omitempty"`

	// Metadata contains additional result metadata.
	Metadata map[string]any `json:"metadata,omitempty"`

	// TraceStep is the CRS trace step for this tool execution.
	// Tools that want full CRS integration should populate this.
	// The dispatcher/execute phase will record it to the session.
	TraceStep *crs.TraceStep `json:"trace_step,omitempty"`
}

// Invocation represents a pending or completed tool call.
type Invocation struct {
	// ID is a unique identifier for this invocation.
	ID string `json:"id"`

	// ToolName is the tool to invoke.
	ToolName string `json:"tool_name"`

	// Parameters are the input parameters.
	Parameters map[string]any `json:"parameters"`

	// Reason explains why the agent chose this tool.
	Reason string `json:"reason,omitempty"`

	// StepNumber is the agent step when this was invoked.
	StepNumber int `json:"step_number"`

	// StartedAt is when execution started (Unix milliseconds UTC).
	StartedAt int64 `json:"started_at,omitempty"`

	// CompletedAt is when execution completed (Unix milliseconds UTC).
	CompletedAt int64 `json:"completed_at,omitempty"`

	// Result contains the execution result (after completion).
	Result *Result `json:"result,omitempty"`
}

// ExecutorOptions configures the tool executor.
type ExecutorOptions struct {
	// DefaultTimeout is the default execution timeout.
	DefaultTimeout time.Duration

	// MaxOutputTokens limits output size (estimated).
	MaxOutputTokens int

	// EnableCaching enables result caching.
	EnableCaching bool

	// CacheTTL is how long cached results are valid.
	CacheTTL time.Duration
}

// DefaultExecutorOptions returns sensible defaults.
func DefaultExecutorOptions() ExecutorOptions {
	return ExecutorOptions{
		DefaultTimeout:  30 * time.Second,
		MaxOutputTokens: 4000,
		EnableCaching:   true,
		CacheTTL:        5 * time.Minute,
	}
}

// ValidationError represents a parameter validation error.
type ValidationError struct {
	// Parameter is the parameter name that failed validation.
	Parameter string `json:"parameter"`

	// Message describes the validation failure.
	Message string `json:"message"`

	// Expected describes what was expected.
	Expected string `json:"expected,omitempty"`

	// Actual describes what was received.
	Actual string `json:"actual,omitempty"`
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Expected != "" && e.Actual != "" {
		return e.Parameter + ": " + e.Message + " (expected " + e.Expected + ", got " + e.Actual + ")"
	}
	return e.Parameter + ": " + e.Message
}
