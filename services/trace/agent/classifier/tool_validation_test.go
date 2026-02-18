// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package classifier

import (
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

func TestValidateToolName(t *testing.T) {
	available := []string{"find_entry_points", "trace_data_flow", "check_security"}

	t.Run("exact match", func(t *testing.T) {
		result := ValidateToolName("find_entry_points", available)
		if !result.Valid {
			t.Error("expected valid")
		}
		if result.ToolName != "find_entry_points" {
			t.Errorf("expected find_entry_points, got %s", result.ToolName)
		}
		if result.FuzzyMatched {
			t.Error("should not be fuzzy matched for exact match")
		}
	})

	t.Run("fuzzy match - minor typo", func(t *testing.T) {
		result := ValidateToolName("find_entry_point", available) // missing 's'
		if !result.Valid {
			t.Error("expected valid with fuzzy match")
		}
		if result.ToolName != "find_entry_points" {
			t.Errorf("expected find_entry_points, got %s", result.ToolName)
		}
		if !result.FuzzyMatched {
			t.Error("should be fuzzy matched")
		}
		if result.Warning == "" {
			t.Error("should have warning for fuzzy match")
		}
	})

	t.Run("no match", func(t *testing.T) {
		result := ValidateToolName("completely_different_tool", available)
		if result.Valid {
			t.Error("expected invalid for non-matching tool")
		}
		if result.Error == nil {
			t.Error("expected error")
		}
	})

	t.Run("empty tool name", func(t *testing.T) {
		result := ValidateToolName("", available)
		if result.Valid {
			t.Error("expected invalid for empty tool name")
		}
	})

	t.Run("empty available tools", func(t *testing.T) {
		result := ValidateToolName("find_entry_points", []string{})
		if result.Valid {
			t.Error("expected invalid when no tools available")
		}
	})
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "b", 1},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "adc", 1},
		{"abc", "abcd", 1},
		{"kitten", "sitting", 3},
		{"find_entry_points", "find_entry_point", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			result := levenshteinDistance(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestValidateParameters(t *testing.T) {
	schema := map[string]tools.ParamDef{
		"type": {
			Type:     tools.ParamTypeString,
			Required: true,
			Enum:     []any{"test", "main", "api"},
		},
		"limit": {
			Type:     tools.ParamTypeInt,
			Required: false,
		},
		"enabled": {
			Type:     tools.ParamTypeBool,
			Required: false,
		},
	}

	t.Run("valid parameters", func(t *testing.T) {
		params := map[string]any{
			"type":  "test",
			"limit": float64(10), // JSON numbers come as float64
		}

		result := ValidateParameters(params, schema)
		if len(result.Warnings) > 0 {
			t.Errorf("unexpected warnings: %v", result.Warnings)
		}
		if _, ok := result.ValidatedParams["type"]; !ok {
			t.Error("expected type in validated params")
		}
		if _, ok := result.ValidatedParams["limit"]; !ok {
			t.Error("expected limit in validated params")
		}
	})

	t.Run("unknown parameter removed", func(t *testing.T) {
		params := map[string]any{
			"type":    "test",
			"unknown": "value",
		}

		result := ValidateParameters(params, schema)
		if _, ok := result.ValidatedParams["unknown"]; ok {
			t.Error("unknown parameter should be removed")
		}
		if len(result.Warnings) == 0 {
			t.Error("expected warning for unknown parameter")
		}
	})

	t.Run("invalid enum value", func(t *testing.T) {
		params := map[string]any{
			"type": "invalid",
		}

		result := ValidateParameters(params, schema)
		if _, ok := result.ValidatedParams["type"]; ok {
			t.Error("invalid enum value should be removed")
		}
		if len(result.Warnings) == 0 {
			t.Error("expected warning for invalid enum")
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		params := map[string]any{
			"type":  123,   // should be string
			"limit": "ten", // should be int
		}

		result := ValidateParameters(params, schema)
		if _, ok := result.ValidatedParams["type"]; ok {
			t.Error("wrong type should be removed")
		}
		if _, ok := result.ValidatedParams["limit"]; ok {
			t.Error("wrong type should be removed")
		}
	})

	t.Run("missing required parameter", func(t *testing.T) {
		params := map[string]any{
			"limit": float64(10),
		}

		result := ValidateParameters(params, schema)
		if len(result.MissingRequired) == 0 {
			t.Error("expected missing required parameter")
		}
		found := false
		for _, missing := range result.MissingRequired {
			if missing == "type" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected 'type' in missing required")
		}
	})

	t.Run("nil parameters", func(t *testing.T) {
		result := ValidateParameters(nil, schema)
		if result.ValidatedParams == nil {
			t.Error("ValidatedParams should be initialized")
		}
	})
}

func TestValidateClassificationResult(t *testing.T) {
	toolDefs := map[string]tools.ToolDefinition{
		"find_entry_points": {
			Name: "find_entry_points",
			Parameters: map[string]tools.ParamDef{
				"type": {
					Type: tools.ParamTypeString,
					Enum: []any{"test", "main"},
				},
			},
		},
	}
	available := []string{"find_entry_points", "trace_data_flow"}

	t.Run("valid result", func(t *testing.T) {
		result := &ClassificationResult{
			IsAnalytical: true,
			Tool:         "find_entry_points",
			Parameters:   map[string]any{"type": "test"},
		}

		validated, ok := ValidateClassificationResult(result, toolDefs, available)
		if !ok {
			t.Error("expected valid result")
		}
		if validated.Tool != "find_entry_points" {
			t.Errorf("expected find_entry_points, got %s", validated.Tool)
		}
	})

	t.Run("non-analytical", func(t *testing.T) {
		result := &ClassificationResult{
			IsAnalytical: false,
		}

		validated, ok := ValidateClassificationResult(result, toolDefs, available)
		if !ok {
			t.Error("expected valid for non-analytical")
		}
		if validated.IsAnalytical {
			t.Error("should remain non-analytical")
		}
	})

	t.Run("hallucinated tool", func(t *testing.T) {
		result := &ClassificationResult{
			IsAnalytical: true,
			Tool:         "nonexistent_tool",
		}

		validated, ok := ValidateClassificationResult(result, toolDefs, available)
		if ok {
			t.Error("expected invalid for hallucinated tool")
		}
		if validated.Tool != "" {
			t.Error("hallucinated tool should be cleared")
		}
		if len(validated.ValidationWarnings) == 0 {
			t.Error("expected warning for hallucinated tool")
		}
	})

	t.Run("nil result", func(t *testing.T) {
		validated, ok := ValidateClassificationResult(nil, toolDefs, available)
		if ok {
			t.Error("expected invalid for nil result")
		}
		if validated != nil {
			t.Error("expected nil for nil input")
		}
	})

	t.Run("no tool suggested", func(t *testing.T) {
		result := &ClassificationResult{
			IsAnalytical: true,
			Tool:         "",
		}

		_, ok := ValidateClassificationResult(result, toolDefs, available)
		if !ok {
			t.Error("no tool suggested is valid")
		}
	})
}

func TestValidateParamType(t *testing.T) {
	t.Run("string type", func(t *testing.T) {
		if err := validateParamType("hello", tools.ParamTypeString); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType(123, tools.ParamTypeString); err == nil {
			t.Error("expected error for wrong type")
		}
	})

	t.Run("int type", func(t *testing.T) {
		if err := validateParamType(42, tools.ParamTypeInt); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType(float64(42), tools.ParamTypeInt); err != nil {
			t.Errorf("should accept whole number float64: %v", err)
		}
		if err := validateParamType(42.5, tools.ParamTypeInt); err == nil {
			t.Error("expected error for non-integer float")
		}
		if err := validateParamType("42", tools.ParamTypeInt); err == nil {
			t.Error("expected error for string")
		}
	})

	t.Run("float type", func(t *testing.T) {
		if err := validateParamType(42.5, tools.ParamTypeFloat); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType(42, tools.ParamTypeFloat); err != nil {
			t.Errorf("int should be valid for float: %v", err)
		}
	})

	t.Run("bool type", func(t *testing.T) {
		if err := validateParamType(true, tools.ParamTypeBool); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType("true", tools.ParamTypeBool); err == nil {
			t.Error("expected error for string")
		}
	})

	t.Run("array type", func(t *testing.T) {
		if err := validateParamType([]any{"a", "b"}, tools.ParamTypeArray); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType("not an array", tools.ParamTypeArray); err == nil {
			t.Error("expected error for non-array")
		}
	})

	t.Run("object type", func(t *testing.T) {
		if err := validateParamType(map[string]any{"key": "value"}, tools.ParamTypeObject); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if err := validateParamType("not an object", tools.ParamTypeObject); err == nil {
			t.Error("expected error for non-object")
		}
	})

	t.Run("nil is valid", func(t *testing.T) {
		if err := validateParamType(nil, tools.ParamTypeString); err != nil {
			t.Errorf("nil should be valid: %v", err)
		}
	})
}

// GR-59 Group C: Tests for special token stripping in tool name validation.

func TestStripSpecialTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no tokens", "find_callers", "find_callers"},
		{"channel token", "find_callees<|channel|>analysis", "find_calleesanalysis"},
		{"trailing bracket", "Grep]", "Grep"},
		{"trailing parens", "find_path()", "find_path"},
		{"multiple tokens", "tool<|a|>name<|b|>", "toolname"},
		{"only token", "<|token|>", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripSpecialTokens(tt.input)
			if result != tt.expected {
				t.Errorf("stripSpecialTokens(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestValidateToolName_SpecialTokens(t *testing.T) {
	available := []string{"find_callees", "find_callers", "Grep"}

	t.Run("special token stripped then exact match", func(t *testing.T) {
		result := ValidateToolName("Grep]", available)
		if !result.Valid {
			t.Error("expected valid after stripping trailing bracket")
		}
		if result.ToolName != "Grep" {
			t.Errorf("expected Grep, got %s", result.ToolName)
		}
	})

	t.Run("channel token stripped then fuzzy match", func(t *testing.T) {
		// "find_calleesanalysis" after stripping → fuzzy match to "find_callees" (distance 8, won't match)
		// But "find_callees<|channel|>" → "find_callees" → exact match
		result := ValidateToolName("find_callees<|channel|>", available)
		if !result.Valid {
			t.Error("expected valid after stripping channel token")
		}
		if result.ToolName != "find_callees" {
			t.Errorf("expected find_callees, got %s", result.ToolName)
		}
	})

	t.Run("empty after sanitization", func(t *testing.T) {
		result := ValidateToolName("<|token|>", available)
		if result.Valid {
			t.Error("expected invalid for name that becomes empty after sanitization")
		}
	})
}

func TestContainsValue(t *testing.T) {
	t.Run("string in slice", func(t *testing.T) {
		if !containsValue([]any{"a", "b", "c"}, "b") {
			t.Error("expected to find 'b'")
		}
	})

	t.Run("int in slice", func(t *testing.T) {
		if !containsValue([]any{1, 2, 3}, 2) {
			t.Error("expected to find 2")
		}
	})

	t.Run("not in slice", func(t *testing.T) {
		if containsValue([]any{"a", "b", "c"}, "d") {
			t.Error("should not find 'd'")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if containsValue([]any{}, "a") {
			t.Error("should not find in empty slice")
		}
	})
}
