// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"testing"
	"time"
)

// mockTool is a minimal tool implementation for testing.
type mockTool struct {
	name       string
	definition ToolDefinition
}

func (t *mockTool) Name() string               { return t.name }
func (t *mockTool) Category() ToolCategory     { return CategoryExploration }
func (t *mockTool) Definition() ToolDefinition { return t.definition }
func (t *mockTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	return &Result{Success: true, OutputText: "ok"}, nil
}

func TestExecutor_CoerceParams(t *testing.T) {
	registry := NewRegistry()
	executor := NewExecutor(registry, nil)

	minVal := 0.0
	maxVal := 10.0

	tool := &mockTool{
		name: "test_tool",
		definition: ToolDefinition{
			Name: "test_tool",
			Parameters: map[string]ParamDef{
				"float_param": {
					Type:    ParamTypeFloat,
					Minimum: &minVal,
					Maximum: &maxVal,
				},
				"int_param": {
					Type: ParamTypeInt,
				},
				"bool_param": {
					Type: ParamTypeBool,
				},
				"string_param": {
					Type: ParamTypeString,
				},
			},
		},
	}

	t.Run("string number to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "1.5",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 1.5 {
			t.Errorf("expected 1.5, got %v", val)
		}
	})

	t.Run("string integer to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "10",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 10.0 {
			t.Errorf("expected 10.0, got %v", val)
		}
	})

	t.Run("semantic high to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "high",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 2.0 {
			t.Errorf("expected 2.0 for 'high', got %v", val)
		}
	})

	t.Run("semantic medium to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "medium",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 1.0 {
			t.Errorf("expected 1.0 for 'medium', got %v", val)
		}
	})

	t.Run("semantic low to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "low",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 0.5 {
			t.Errorf("expected 0.5 for 'low', got %v", val)
		}
	})

	t.Run("semantic maximum to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "maximum",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 2.0 {
			t.Errorf("expected 2.0 for 'maximum', got %v", val)
		}
	})

	t.Run("semantic very_high to float", func(t *testing.T) {
		params := map[string]any{
			"float_param": "very_high",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 3.0 {
			t.Errorf("expected 3.0 for 'very_high', got %v", val)
		}
	})

	t.Run("string to int param", func(t *testing.T) {
		params := map[string]any{
			"int_param": "42",
		}
		executor.coerceParams(tool, params)

		val, ok := params["int_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["int_param"])
		}
		if val != 42.0 {
			t.Errorf("expected 42.0, got %v", val)
		}
	})

	t.Run("string true to bool", func(t *testing.T) {
		params := map[string]any{
			"bool_param": "true",
		}
		executor.coerceParams(tool, params)

		val, ok := params["bool_param"].(bool)
		if !ok {
			t.Errorf("expected bool, got %T", params["bool_param"])
		}
		if !val {
			t.Errorf("expected true, got %v", val)
		}
	})

	t.Run("string false to bool", func(t *testing.T) {
		params := map[string]any{
			"bool_param": "false",
		}
		executor.coerceParams(tool, params)

		val, ok := params["bool_param"].(bool)
		if !ok {
			t.Errorf("expected bool, got %T", params["bool_param"])
		}
		if val {
			t.Errorf("expected false, got %v", val)
		}
	})

	t.Run("string yes to bool", func(t *testing.T) {
		params := map[string]any{
			"bool_param": "yes",
		}
		executor.coerceParams(tool, params)

		val, ok := params["bool_param"].(bool)
		if !ok {
			t.Errorf("expected bool, got %T", params["bool_param"])
		}
		if !val {
			t.Errorf("expected true for 'yes', got %v", val)
		}
	})

	t.Run("does not coerce string params", func(t *testing.T) {
		params := map[string]any{
			"string_param": "123",
		}
		executor.coerceParams(tool, params)

		val, ok := params["string_param"].(string)
		if !ok {
			t.Errorf("expected string to remain string, got %T", params["string_param"])
		}
		if val != "123" {
			t.Errorf("expected '123', got %v", val)
		}
	})

	t.Run("preserves already-correct types", func(t *testing.T) {
		params := map[string]any{
			"float_param": 1.5,
			"int_param":   42,
			"bool_param":  true,
		}
		executor.coerceParams(tool, params)

		if params["float_param"].(float64) != 1.5 {
			t.Errorf("float_param changed unexpectedly")
		}
		if params["int_param"].(int) != 42 {
			t.Errorf("int_param changed unexpectedly")
		}
		if params["bool_param"].(bool) != true {
			t.Errorf("bool_param changed unexpectedly")
		}
	})

	t.Run("handles nil params", func(t *testing.T) {
		// Should not panic
		executor.coerceParams(tool, nil)
	})

	t.Run("handles empty params", func(t *testing.T) {
		params := map[string]any{}
		executor.coerceParams(tool, params)
		if len(params) != 0 {
			t.Errorf("expected empty params to remain empty")
		}
	})

	t.Run("ignores unknown params", func(t *testing.T) {
		params := map[string]any{
			"unknown_param": "high",
		}
		executor.coerceParams(tool, params)

		// Should remain unchanged
		if params["unknown_param"].(string) != "high" {
			t.Errorf("unknown param should not be coerced")
		}
	})

	t.Run("case insensitive semantic values", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected float64
		}{
			{"HIGH", 2.0},
			{"High", 2.0},
			{"MEDIUM", 1.0},
			{"Medium", 1.0},
			{"LOW", 0.5},
			{"Low", 0.5},
		}

		for _, tc := range testCases {
			params := map[string]any{
				"float_param": tc.input,
			}
			executor.coerceParams(tool, params)

			val, ok := params["float_param"].(float64)
			if !ok {
				t.Errorf("expected float64 for %q, got %T", tc.input, params["float_param"])
				continue
			}
			if val != tc.expected {
				t.Errorf("expected %v for %q, got %v", tc.expected, tc.input, val)
			}
		}
	})

	t.Run("whitespace trimming", func(t *testing.T) {
		params := map[string]any{
			"float_param": "  high  ",
		}
		executor.coerceParams(tool, params)

		val, ok := params["float_param"].(float64)
		if !ok {
			t.Errorf("expected float64, got %T", params["float_param"])
		}
		if val != 2.0 {
			t.Errorf("expected 2.0 for '  high  ', got %v", val)
		}
	})

	t.Run("community resolution semantic values", func(t *testing.T) {
		// GR-47: These are values LLMs commonly pass for resolution parameters
		// based on tool descriptions like "granularity: 0.1=large, 1.0=balanced, 5.0=small"
		testCases := []struct {
			input    string
			expected float64
		}{
			// High resolution variants
			{"fine-grained", 2.0},
			{"fine_grained", 2.0},
			{"detailed", 2.0},
			{"granular", 2.0},
			{"fine", 2.0},
			{"small", 2.0},
			// Medium resolution variants
			{"balanced", 1.0},
			{"moderate", 1.0},
			{"standard", 1.0},
			// Low resolution variants
			{"coarse", 0.5},
			{"broad", 0.5},
			{"large", 0.5},
			// Very high/low
			{"very_fine", 3.0},
			{"finest", 3.0},
			{"very_coarse", 0.25},
			{"coarsest", 0.25},
		}

		for _, tc := range testCases {
			params := map[string]any{
				"float_param": tc.input,
			}
			executor.coerceParams(tool, params)

			val, ok := params["float_param"].(float64)
			if !ok {
				t.Errorf("expected float64 for %q, got %T", tc.input, params["float_param"])
				continue
			}
			if val != tc.expected {
				t.Errorf("expected %v for %q, got %v", tc.expected, tc.input, val)
			}
		}
	})
}

func TestExecutor_Execute_WithCoercion(t *testing.T) {
	registry := NewRegistry()

	minVal := 0.0
	maxVal := 10.0

	tool := &mockTool{
		name: "test_tool",
		definition: ToolDefinition{
			Name: "test_tool",
			Parameters: map[string]ParamDef{
				"resolution": {
					Type:    ParamTypeFloat,
					Minimum: &minVal,
					Maximum: &maxVal,
				},
			},
			Timeout: 5 * time.Second,
		},
	}
	registry.Register(tool)

	executor := NewExecutor(registry, nil)

	t.Run("string number passes validation after coercion", func(t *testing.T) {
		invocation := &Invocation{
			ToolName: "test_tool",
			Parameters: map[string]any{
				"resolution": "1.5",
			},
		}

		result, err := executor.Execute(context.Background(), invocation)
		if err != nil {
			t.Errorf("expected success after coercion, got error: %v", err)
		}
		if result == nil || !result.Success {
			t.Errorf("expected successful result")
		}
	})

	t.Run("semantic value passes validation after coercion", func(t *testing.T) {
		invocation := &Invocation{
			ToolName: "test_tool",
			Parameters: map[string]any{
				"resolution": "high",
			},
		}

		result, err := executor.Execute(context.Background(), invocation)
		if err != nil {
			t.Errorf("expected success after coercion, got error: %v", err)
		}
		if result == nil || !result.Success {
			t.Errorf("expected successful result")
		}
	})
}
