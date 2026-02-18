// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package classifier

import (
	"fmt"
	"log/slog"
	"regexp"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// GR-59 Group C: Pre-compiled regexes for stripping special tokens from LLM-generated tool names.
// Allocated once at package init. Handles cases like:
//   - "find_callees<|channel|>analysis" → "find_callees"
//   - "Grep]" → "Grep"
var (
	specialTokenRe = regexp.MustCompile(`<\|[^|]*\|>`)
	trailingJunkRe = regexp.MustCompile(`[^a-zA-Z0-9_]+$`)
)

// stripSpecialTokens removes LLM special tokens and trailing non-tool characters from a tool name.
//
// Description:
//
//	LLMs sometimes leak special tokens (e.g., <|channel|>) or stray characters
//	(e.g., trailing brackets) into tool names. This function strips them before
//	exact/fuzzy matching to prevent false negatives.
//
// Inputs:
//
//   - name: The raw tool name from LLM output.
//
// Outputs:
//
//   - string: Cleaned tool name with special tokens and trailing junk removed.
//
// Thread Safety: This function is safe for concurrent use.
func stripSpecialTokens(name string) string {
	name = specialTokenRe.ReplaceAllString(name, "")
	name = trailingJunkRe.ReplaceAllString(name, "")
	return name
}

// ToolValidationResult contains the outcome of tool name validation.
type ToolValidationResult struct {
	// Valid indicates if the tool name is valid.
	Valid bool

	// ToolName is the validated/matched tool name.
	// May differ from input if fuzzy matching was used.
	ToolName string

	// FuzzyMatched indicates if fuzzy matching was used.
	FuzzyMatched bool

	// Warning contains any validation warning (e.g., fuzzy match used).
	Warning string

	// Error contains the error if validation failed.
	Error error
}

// ValidateToolName validates the suggested tool against available tools.
//
// Description:
//
//	Validates that the suggested tool name exists in the available tools list.
//	First attempts exact match, then falls back to fuzzy matching using
//	Levenshtein distance if no exact match is found.
//
// Inputs:
//
//	suggested - The tool name suggested by the LLM.
//	available - List of available tool names.
//
// Outputs:
//
//	ToolValidationResult - Contains the matched tool or error.
//
// Example:
//
//	result := ValidateToolName("find_tests", []string{"find_entry_points", "trace_data_flow"})
//	if result.Valid {
//	    // result.ToolName == "find_entry_points" (fuzzy matched)
//	    // result.FuzzyMatched == true
//	}
//
// Thread Safety: This function is safe for concurrent use.
func ValidateToolName(suggested string, available []string) ToolValidationResult {
	if suggested == "" {
		return ToolValidationResult{
			Valid: false,
			Error: fmt.Errorf("empty tool name"),
		}
	}

	if len(available) == 0 {
		return ToolValidationResult{
			Valid: false,
			Error: fmt.Errorf("no available tools"),
		}
	}

	// GR-59 Group C: Strip special tokens that LLMs sometimes leak into tool names.
	sanitized := stripSpecialTokens(suggested)
	if sanitized != suggested {
		slog.Warn("GR-59: special token detected in tool name",
			slog.String("original", suggested),
			slog.String("sanitized", sanitized),
		)
		suggested = sanitized
		// After sanitization, re-check for empty
		if suggested == "" {
			return ToolValidationResult{
				Valid: false,
				Error: fmt.Errorf("tool name empty after sanitization"),
			}
		}
	}

	// Check exact match first
	for _, tool := range available {
		if tool == suggested {
			return ToolValidationResult{
				Valid:    true,
				ToolName: tool,
			}
		}
	}

	// Try fuzzy match (Levenshtein distance < 3)
	const maxDistance = 3
	bestMatch := ""
	bestDistance := maxDistance

	for _, tool := range available {
		dist := levenshteinDistance(suggested, tool)
		if dist < bestDistance {
			bestDistance = dist
			bestMatch = tool
		}
	}

	if bestMatch != "" {
		return ToolValidationResult{
			Valid:        true,
			ToolName:     bestMatch,
			FuzzyMatched: true,
			Warning:      fmt.Sprintf("fuzzy matched %q to %q (distance: %d)", suggested, bestMatch, bestDistance),
		}
	}

	return ToolValidationResult{
		Valid: false,
		Error: fmt.Errorf("tool %q not found in available tools", suggested),
	}
}

// levenshteinDistance calculates the Levenshtein edit distance between two strings.
//
// Description:
//
//	Computes the minimum number of single-character edits (insertions,
//	deletions, or substitutions) required to change one string into another.
//	Uses O(min(m,n)) space optimization instead of O(m*n).
//
// Thread Safety: This function is safe for concurrent use.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Ensure b is the shorter string for space optimization
	if len(a) < len(b) {
		a, b = b, a
	}

	// Use two rows instead of full matrix - O(min(m,n)) space
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)

	// Initialize first row
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	// Fill in row by row
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			curr[j] = min(
				min(prev[j]+1, curr[j-1]+1), // deletion or insertion
				prev[j-1]+cost,              // substitution
			)
		}
		// Swap rows
		prev, curr = curr, prev
	}

	return prev[len(b)]
}

// ParameterValidationResult contains the outcome of parameter validation.
type ParameterValidationResult struct {
	// ValidatedParams contains parameters that passed validation.
	ValidatedParams map[string]any

	// Warnings contains validation warnings for each parameter.
	Warnings []string

	// MissingRequired lists required parameters that are missing.
	MissingRequired []string
}

// ValidateParameters validates suggested parameters against tool schema.
//
// Description:
//
//	Validates each parameter against the tool's ParamDef schema, checking:
//	- Parameter exists in schema
//	- Type matches expected type
//	- Enum values are in allowed set
//	- Required parameters are present
//
//	Invalid parameters are removed and warnings are generated.
//
// Inputs:
//
//	params - The parameters suggested by the LLM.
//	schema - The tool's parameter schema.
//
// Outputs:
//
//	ParameterValidationResult - Contains validated params and warnings.
//
// Example:
//
//	schema := map[string]tools.ParamDef{
//	    "type": {Type: tools.ParamTypeString, Required: true, Enum: []any{"test", "main"}},
//	}
//	result := ValidateParameters(map[string]any{"type": "test"}, schema)
//
// Thread Safety: This function is safe for concurrent use.
func ValidateParameters(params map[string]any, schema map[string]tools.ParamDef) ParameterValidationResult {
	result := ParameterValidationResult{
		ValidatedParams: make(map[string]any),
	}

	if params == nil {
		params = make(map[string]any)
	}

	// Validate each provided parameter
	for name, value := range params {
		def, exists := schema[name]
		if !exists {
			result.Warnings = append(result.Warnings, fmt.Sprintf("unknown parameter %q removed", name))
			continue
		}

		// Type validation
		if err := validateParamType(value, def.Type); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("parameter %q: %v", name, err))
			continue
		}

		// Enum validation
		if len(def.Enum) > 0 {
			if !containsValue(def.Enum, value) {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("parameter %q value %v not in enum %v", name, value, def.Enum))
				continue
			}
		}

		result.ValidatedParams[name] = value
	}

	// Check required parameters
	for name, def := range schema {
		if def.Required {
			if _, exists := result.ValidatedParams[name]; !exists {
				result.MissingRequired = append(result.MissingRequired, name)
				result.Warnings = append(result.Warnings, fmt.Sprintf("required parameter %q missing", name))
			}
		}
	}

	return result
}

// validateParamType checks if a value matches the expected parameter type.
func validateParamType(value any, expectedType tools.ParamType) error {
	if value == nil {
		return nil // nil is allowed for optional params
	}

	switch expectedType {
	case tools.ParamTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string, got %T", value)
		}
	case tools.ParamTypeInt:
		switch v := value.(type) {
		case int, int64, int32:
			// Valid
		case float64:
			// JSON numbers are float64, check if it's a whole number
			if v != float64(int64(v)) {
				return fmt.Errorf("expected integer, got float %v", v)
			}
		default:
			return fmt.Errorf("expected integer, got %T", value)
		}
	case tools.ParamTypeFloat:
		switch value.(type) {
		case float64, float32, int, int64:
			// Valid
		default:
			return fmt.Errorf("expected number, got %T", value)
		}
	case tools.ParamTypeBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", value)
		}
	case tools.ParamTypeArray:
		switch value.(type) {
		case []any, []string, []int, []float64:
			// Valid
		default:
			return fmt.Errorf("expected array, got %T", value)
		}
	case tools.ParamTypeObject:
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("expected object, got %T", value)
		}
	default:
		return fmt.Errorf("unknown parameter type: %s", expectedType)
	}

	return nil
}

// containsValue checks if a slice contains a value (using equality).
func containsValue(slice []any, value any) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
		// Handle string comparison for enum values
		if s1, ok1 := v.(string); ok1 {
			if s2, ok2 := value.(string); ok2 {
				if s1 == s2 {
					return true
				}
			}
		}
	}
	return false
}

// ValidateClassificationResult validates a complete classification result.
//
// Description:
//
//	Validates the tool name and parameters in a ClassificationResult against
//	the available tools and their schemas. Returns a new result with validated
//	parameters and any warnings.
//
// Inputs:
//
//	result - The classification result to validate.
//	toolDefs - Map of tool name to ToolDefinition.
//	availableTools - List of available tool names.
//
// Outputs:
//
//	*ClassificationResult - Validated result with warnings.
//	bool - True if validation passed (tool found and usable).
//
// Thread Safety: This function is safe for concurrent use.
func ValidateClassificationResult(
	result *ClassificationResult,
	toolDefs map[string]tools.ToolDefinition,
	availableTools []string,
) (*ClassificationResult, bool) {
	if result == nil {
		return nil, false
	}

	// Non-analytical results don't need validation
	if !result.IsAnalytical {
		return result, true
	}

	// Validate tool name
	if result.Tool == "" {
		return result, true // No tool suggested is valid
	}

	toolResult := ValidateToolName(result.Tool, availableTools)
	if !toolResult.Valid {
		// Tool not found - add warning and clear tool
		validated := *result
		validated.ValidationWarnings = append(validated.ValidationWarnings,
			fmt.Sprintf("hallucinated tool %q removed: %v", result.Tool, toolResult.Error))
		validated.Tool = ""
		validated.Parameters = nil
		return &validated, false
	}

	// Update to matched tool name (may have been fuzzy matched)
	validated := *result
	if toolResult.FuzzyMatched {
		validated.Tool = toolResult.ToolName
		validated.ValidationWarnings = append(validated.ValidationWarnings, toolResult.Warning)
	}

	// Validate parameters if tool definition exists
	if toolDef, exists := toolDefs[validated.Tool]; exists {
		paramResult := ValidateParameters(result.Parameters, toolDef.Parameters)
		validated.Parameters = paramResult.ValidatedParams
		validated.ValidationWarnings = append(validated.ValidationWarnings, paramResult.Warnings...)
	}

	return &validated, true
}
