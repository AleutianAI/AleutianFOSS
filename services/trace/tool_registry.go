// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

// ToolParam represents a parameter in a tool definition.
type ToolParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Default     string   `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ToolDefinition represents a tool available to the agent.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Category    string      `json:"category"`
	Parameters  []ToolParam `json:"parameters"`
	Returns     string      `json:"returns"`
	Performance string      `json:"performance"`
}

// ToolRegistry provides tool definitions for agent discovery.
//
// Thread Safety:
//
//	ToolRegistry is immutable after initialization and safe for concurrent use.
type ToolRegistry struct {
	tools []ToolDefinition
}

// NewToolRegistry creates a registry with all available tools.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: allToolDefinitions(),
	}
}

// GetTools returns all available tool definitions.
func (r *ToolRegistry) GetTools() []ToolDefinition {
	return r.tools
}

// GetToolsByCategory returns tools filtered by category.
func (r *ToolRegistry) GetToolsByCategory(category string) []ToolDefinition {
	var result []ToolDefinition
	for _, t := range r.tools {
		if t.Category == category {
			result = append(result, t)
		}
	}
	return result
}

// allToolDefinitions returns all 24 tool definitions.
func allToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		// ==================== EXPLORATION TOOLS ====================
		{
			Name:        "find_entry_points",
			Description: "Find where code execution starts: main functions, HTTP handlers, CLI commands, test functions, Lambda handlers, gRPC services. Essential for understanding codebase architecture.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "type", Type: "string", Description: "Filter by type: main, handler, command, test, lambda, grpc, all", Required: false, Default: "all", Enum: []string{"main", "handler", "command", "test", "lambda", "grpc", "all"}},
				{Name: "package", Type: "string", Description: "Filter by package prefix", Required: false},
				{Name: "limit", Type: "integer", Description: "Maximum results to return", Required: false, Default: "100"},
				{Name: "include_tests", Type: "boolean", Description: "Include test entry points", Required: false, Default: "false"},
			},
			Returns:     "List of entry points with type, framework, location, and signature",
			Performance: "<100ms",
		},
		{
			Name:        "trace_data_flow",
			Description: "Trace how data flows from sources (HTTP input, env vars, files) to sinks (database, response, logs). Identifies transformations along the path. Critical for understanding dependencies.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "source_id", Type: "string", Description: "Symbol ID to trace from", Required: true},
				{Name: "max_hops", Type: "integer", Description: "Maximum call depth to trace", Required: false, Default: "5"},
				{Name: "include_code", Type: "boolean", Description: "Include code snippets", Required: false, Default: "true"},
			},
			Returns:     "Data flow with sources, transforms, sinks, and path",
			Performance: "<200ms",
		},
		{
			Name:        "trace_error_flow",
			Description: "Trace error propagation from origin through handlers to potential escape points. Identifies unhandled error paths.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "scope", Type: "string", Description: "Package or file to analyze", Required: true},
				{Name: "max_hops", Type: "integer", Description: "Maximum call depth", Required: false, Default: "5"},
			},
			Returns:     "Error flow with origins, handlers, and escape points",
			Performance: "<200ms",
		},
		{
			Name:        "find_config_usage",
			Description: "Find all usages of configuration values (env vars, config files, feature flags) across the codebase.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "config_key", Type: "string", Description: "Config key or pattern to search", Required: true},
				{Name: "include_defaults", Type: "boolean", Description: "Include default value detection", Required: false, Default: "true"},
			},
			Returns:     "Configuration usages with locations and default values",
			Performance: "<150ms",
		},
		{
			Name:        "find_similar_code",
			Description: "Find code structurally similar to a given function or type. Uses AST fingerprinting for fast similarity matching.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to find similar code for", Required: true},
				{Name: "min_similarity", Type: "number", Description: "Minimum similarity threshold (0.0-1.0)", Required: false, Default: "0.7"},
				{Name: "limit", Type: "integer", Description: "Maximum results", Required: false, Default: "10"},
			},
			Returns:     "Similar code matches with similarity scores and matched traits",
			Performance: "<500ms",
		},
		{
			Name:        "build_minimal_context",
			Description: "Build the minimum code context needed to understand a function: target code, required types, implemented interfaces, and key dependencies. Token-efficient for LLM prompts.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to build context for", Required: true},
				{Name: "token_budget", Type: "integer", Description: "Maximum tokens to include", Required: false, Default: "4000"},
				{Name: "include_callees", Type: "boolean", Description: "Include key dependencies", Required: false, Default: "true"},
			},
			Returns:     "Minimal context with target, types, interfaces, and key callees",
			Performance: "<150ms",
		},
		{
			Name:        "summarize_file",
			Description: "Generate a structured summary of a source file: package, imports, types, functions, and inferred purpose.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "file_path", Type: "string", Description: "Relative path to the file", Required: true},
			},
			Returns:     "File summary with types, functions, imports, and purpose",
			Performance: "<50ms",
		},
		{
			Name:        "summarize_package",
			Description: "Generate a public API summary of a package: exported types, functions, constants, and variables.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "package", Type: "string", Description: "Package path to summarize", Required: true},
			},
			Returns:     "Package API with exported types, functions, and constants",
			Performance: "<100ms",
		},
		{
			Name:        "analyze_change_impact",
			Description: "Analyze the blast radius of a proposed change: affected callers, dependent packages, and risk assessment.",
			Category:    "explore",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to analyze impact for", Required: true},
				{Name: "change_type", Type: "string", Description: "Type of change planned", Required: false, Default: "modify", Enum: []string{"modify", "delete", "rename"}},
			},
			Returns:     "Impact analysis with affected callers, risk level, and recommendations",
			Performance: "<200ms",
		},

		// ==================== REASONING TOOLS ====================
		{
			Name:        "check_breaking_changes",
			Description: "Detect if a proposed signature change would break callers. Identifies all affected call sites and suggests auto-fixes. CRITICAL for safe refactoring.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Function/method to analyze", Required: true},
				{Name: "proposed_signature", Type: "string", Description: "The new signature to check", Required: true},
			},
			Returns:     "Breaking analysis with affected callers, severity, and suggested fixes",
			Performance: "<100ms",
		},
		{
			Name:        "simulate_change",
			Description: "Simulate applying a change and identify all code locations that would need updates. Does NOT modify code.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to change", Required: true},
				{Name: "change_type", Type: "string", Description: "Type of change", Required: true, Enum: []string{"add_parameter", "remove_parameter", "change_type", "rename"}},
				{Name: "change_details", Type: "object", Description: "Change-specific details", Required: true},
			},
			Returns:     "Simulation result with all update locations",
			Performance: "<200ms",
		},
		{
			Name:        "validate_change",
			Description: "Validate proposed code syntactically. Checks for parse errors before applying changes.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "code", Type: "string", Description: "Code to validate", Required: true},
				{Name: "language", Type: "string", Description: "Programming language", Required: true, Enum: []string{"go", "python", "typescript", "javascript"}},
			},
			Returns:     "Validation result with syntax errors if any",
			Performance: "<50ms",
		},
		{
			Name:        "find_test_coverage",
			Description: "Find tests that cover a specific function or code path. Helps ensure changes have test coverage.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to find tests for", Required: true},
				{Name: "include_indirect", Type: "boolean", Description: "Include tests that call through other functions", Required: false, Default: "true"},
			},
			Returns:     "Test coverage with direct and indirect tests",
			Performance: "<150ms",
		},
		{
			Name:        "detect_side_effects",
			Description: "Detect side effects of a function: I/O operations, state mutations, network calls, database access.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Function to analyze", Required: true},
				{Name: "transitive", Type: "boolean", Description: "Include effects from called functions", Required: false, Default: "true"},
			},
			Returns:     "Side effects categorized by type (I/O, state, network, etc.)",
			Performance: "<200ms",
		},
		{
			Name:        "suggest_refactor",
			Description: "Suggest refactoring improvements based on code metrics: complexity, coupling, cohesion.",
			Category:    "reason",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "symbol_id", Type: "string", Description: "Symbol to analyze", Required: true},
			},
			Returns:     "Refactoring suggestions with priority and expected improvement",
			Performance: "<100ms",
		},

		// ==================== COORDINATION TOOLS ====================
		{
			Name:        "plan_multi_file_change",
			Description: "Generate a coordinated change plan for modifications that affect multiple files. Identifies all files needing updates and the correct order to apply changes.",
			Category:    "coordinate",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "target_id", Type: "string", Description: "Primary symbol to change", Required: true},
				{Name: "change_type", Type: "string", Description: "Type of change", Required: true, Enum: []string{"add_parameter", "remove_parameter", "add_return", "remove_return", "rename_symbol", "change_type"}},
				{Name: "new_signature", Type: "string", Description: "New signature for signature changes", Required: false},
				{Name: "new_name", Type: "string", Description: "New name for renames", Required: false},
				{Name: "include_tests", Type: "boolean", Description: "Include test file updates", Required: false, Default: "true"},
			},
			Returns:     "Change plan with ordered file changes, risk level, and confidence",
			Performance: "<300ms",
		},
		{
			Name:        "validate_plan",
			Description: "Validate a change plan before execution. Checks for syntax errors, type mismatches, and import issues.",
			Category:    "coordinate",
			Parameters: []ToolParam{
				{Name: "plan_id", Type: "string", Description: "Plan ID from plan_multi_file_change", Required: true},
			},
			Returns:     "Validation result with syntax, type, and import errors",
			Performance: "<200ms",
		},
		{
			Name:        "preview_changes",
			Description: "Generate unified diffs for all changes in a plan. Shows exactly what will change in each file.",
			Category:    "coordinate",
			Parameters: []ToolParam{
				{Name: "plan_id", Type: "string", Description: "Plan ID from plan_multi_file_change", Required: true},
				{Name: "context_lines", Type: "integer", Description: "Context lines in diff", Required: false, Default: "3"},
			},
			Returns:     "File diffs with hunks showing additions and removals",
			Performance: "<100ms",
		},

		// ==================== PATTERN TOOLS ====================
		{
			Name:        "detect_patterns",
			Description: "Detect design patterns in the codebase: Singleton, Factory, Builder, Options, Middleware, Strategy, Observer, Repository.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "scope", Type: "string", Description: "Package or file to scan", Required: false, Default: ""},
				{Name: "patterns", Type: "array", Description: "Specific patterns to detect (empty = all)", Required: false},
				{Name: "min_confidence", Type: "number", Description: "Minimum confidence threshold", Required: false, Default: "0.6"},
			},
			Returns:     "Detected patterns with components, confidence, and idiomaticity",
			Performance: "<200ms",
		},
		{
			Name:        "find_code_smells",
			Description: "Find code quality issues: long functions, god objects, error swallowing, magic numbers, deep nesting, empty interfaces.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "scope", Type: "string", Description: "Package or file to scan", Required: false, Default: ""},
				{Name: "min_severity", Type: "string", Description: "Minimum severity: INFO, WARNING, ERROR", Required: false, Default: "WARNING", Enum: []string{"INFO", "WARNING", "ERROR"}},
				{Name: "include_tests", Type: "boolean", Description: "Include test files", Required: false, Default: "false"},
			},
			Returns:     "Code smells with severity, location, and suggestions",
			Performance: "<100ms",
		},
		{
			Name:        "find_duplication",
			Description: "Find duplicate or near-duplicate code using LSH-based similarity matching. O(n log n) performance.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "scope", Type: "string", Description: "Package or file to scan", Required: false, Default: ""},
				{Name: "min_similarity", Type: "number", Description: "Minimum similarity (0.0-1.0)", Required: false, Default: "0.8"},
				{Name: "type", Type: "string", Description: "Type: exact, near, structural, all", Required: false, Default: "all", Enum: []string{"exact", "near", "structural", "all"}},
			},
			Returns:     "Duplications with locations, similarity, and refactoring suggestions",
			Performance: "<500ms",
		},
		{
			Name:        "find_circular_deps",
			Description: "Find circular dependencies at package, type, or function level using Tarjan's SCC algorithm.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "level", Type: "string", Description: "Granularity: package, type, function", Required: false, Default: "package", Enum: []string{"package", "type", "function"}},
			},
			Returns:     "Circular dependencies with cycles and break point suggestions",
			Performance: "<300ms",
		},
		{
			Name:        "extract_conventions",
			Description: "Extract coding conventions from the codebase: naming patterns, error handling, file organization, testing patterns.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "types", Type: "array", Description: "Convention types to extract: naming, error_handling, file_organization, testing, documentation, imports", Required: false},
			},
			Returns:     "Conventions with patterns, examples, and adherence frequency",
			Performance: "<200ms",
		},
		{
			Name:        "find_dead_code",
			Description: "Find unreferenced code (functions, types, variables). Conservative detection with exclusions for entry points, exported symbols, and reflection patterns.",
			Category:    "patterns",
			Parameters: []ToolParam{
				{Name: "graph_id", Type: "string", Description: "The graph ID from /init", Required: true},
				{Name: "scope", Type: "string", Description: "Package or file to scan", Required: false, Default: ""},
				{Name: "include_exported", Type: "boolean", Description: "Include exported symbols (less conservative)", Required: false, Default: "false"},
			},
			Returns:     "Dead code with type, location, and confidence",
			Performance: "<200ms",
		},
	}
}
