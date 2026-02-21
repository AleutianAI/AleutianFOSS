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
	"sync"
	"testing"
)

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	t.Run("register single tool", func(t *testing.T) {
		tool := NewMockTool("test_tool", CategoryExploration)
		registry.Register(tool)

		got, ok := registry.Get("test_tool")
		if !ok {
			t.Fatal("expected tool to be registered")
		}
		if got.Name() != "test_tool" {
			t.Errorf("expected name test_tool, got %s", got.Name())
		}
	})

	t.Run("register nil tool", func(t *testing.T) {
		count := registry.Count()
		registry.Register(nil)
		if registry.Count() != count {
			t.Error("nil tool should not be registered")
		}
	})

	t.Run("replace existing tool", func(t *testing.T) {
		tool1 := NewMockTool("replace_me", CategoryExploration)
		tool2 := NewMockTool("replace_me", CategoryReasoning)

		registry.Register(tool1)
		registry.Register(tool2)

		got, ok := registry.Get("replace_me")
		if !ok {
			t.Fatal("expected tool to be registered")
		}
		if got.Category() != CategoryReasoning {
			t.Errorf("expected category to be updated to reasoning")
		}
	})
}

func TestRegistry_GetByCategory(t *testing.T) {
	registry := NewRegistry()

	registry.Register(NewMockTool("explore1", CategoryExploration))
	registry.Register(NewMockTool("explore2", CategoryExploration))
	registry.Register(NewMockTool("reason1", CategoryReasoning))

	t.Run("get exploration tools", func(t *testing.T) {
		tools := registry.GetByCategory(CategoryExploration)
		if len(tools) != 2 {
			t.Errorf("expected 2 exploration tools, got %d", len(tools))
		}
	})

	t.Run("get reasoning tools", func(t *testing.T) {
		tools := registry.GetByCategory(CategoryReasoning)
		if len(tools) != 1 {
			t.Errorf("expected 1 reasoning tool, got %d", len(tools))
		}
	})

	t.Run("get non-existent category", func(t *testing.T) {
		tools := registry.GetByCategory(CategoryFile)
		if len(tools) != 0 {
			t.Errorf("expected 0 file tools, got %d", len(tools))
		}
	})
}

func TestRegistry_GetEnabled(t *testing.T) {
	registry := NewRegistry()

	// Register tools with different priorities
	t1 := NewMockTool("low_priority", CategoryExploration)
	t1.definition.Priority = 10
	registry.Register(t1)

	t2 := NewMockTool("high_priority", CategoryExploration)
	t2.definition.Priority = 90
	registry.Register(t2)

	t3 := NewMockTool("disabled_one", CategoryExploration)
	t3.definition.Priority = 50
	registry.Register(t3)

	t.Run("filter by category", func(t *testing.T) {
		tools := registry.GetEnabled([]string{"exploration"}, nil)
		if len(tools) != 3 {
			t.Errorf("expected 3 tools, got %d", len(tools))
		}
	})

	t.Run("exclude specific tool", func(t *testing.T) {
		tools := registry.GetEnabled(nil, []string{"disabled_one"})
		if len(tools) != 2 {
			t.Errorf("expected 2 tools, got %d", len(tools))
		}
		for _, tool := range tools {
			if tool.Name() == "disabled_one" {
				t.Error("disabled tool should not be included")
			}
		}
	})

	t.Run("sorted by priority", func(t *testing.T) {
		tools := registry.GetEnabled(nil, nil)
		if len(tools) < 2 {
			t.Skip("need at least 2 tools")
		}
		for i := 0; i < len(tools)-1; i++ {
			if tools[i].Definition().Priority < tools[i+1].Definition().Priority {
				t.Error("tools should be sorted by priority descending")
			}
		}
	})
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewRegistry()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent registrations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tool := NewMockTool("concurrent_"+string(rune('A'+n)), CategoryExploration)
			registry.Register(tool)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = registry.GetAll()
			_ = registry.GetByCategory(CategoryExploration)
			_ = registry.Names()
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry()

	registry.Register(NewMockTool("to_remove", CategoryExploration))
	registry.Register(NewMockTool("to_keep", CategoryExploration))

	t.Run("unregister existing", func(t *testing.T) {
		if !registry.Unregister("to_remove") {
			t.Error("expected true for existing tool")
		}

		_, ok := registry.Get("to_remove")
		if ok {
			t.Error("tool should be removed")
		}
	})

	t.Run("unregister non-existent", func(t *testing.T) {
		if registry.Unregister("does_not_exist") {
			t.Error("expected false for non-existent tool")
		}
	})
}

func TestRegistry_GetDefinitions(t *testing.T) {
	registry := NewRegistry()

	t1 := NewMockTool("tool_a", CategoryExploration)
	t1.definition.Priority = 10
	registry.Register(t1)

	t2 := NewMockTool("tool_b", CategoryExploration)
	t2.definition.Priority = 90
	registry.Register(t2)

	definitions := registry.GetDefinitions()
	if len(definitions) != 2 {
		t.Errorf("expected 2 definitions, got %d", len(definitions))
	}

	// Should be sorted by priority
	if definitions[0].Priority < definitions[1].Priority {
		t.Error("definitions should be sorted by priority descending")
	}
}

func TestExecutor_Execute(t *testing.T) {
	registry := NewRegistry()

	mockTool := NewMockTool("test_execute", CategoryExploration)
	mockTool.definition.Parameters = map[string]ParamDef{
		"required_param": {
			Type:     ParamTypeString,
			Required: true,
		},
		"optional_param": {
			Type:     ParamTypeInt,
			Required: false,
		},
	}
	mockTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		return &Result{
			Success:    true,
			OutputText: "execution succeeded",
		}, nil
	}
	registry.Register(mockTool)

	executor := NewExecutor(registry, nil)

	t.Run("execute with valid params", func(t *testing.T) {
		invocation := &Invocation{
			ToolName: "test_execute",
			Parameters: map[string]any{
				"required_param": "value",
			},
		}

		result, err := executor.Execute(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if !result.Success {
			t.Error("expected success")
		}
	})

	t.Run("execute missing required param", func(t *testing.T) {
		invocation := &Invocation{
			ToolName:   "test_execute",
			Parameters: map[string]any{},
		}

		_, err := executor.Execute(context.Background(), invocation)
		if err == nil {
			t.Error("expected error for missing required param")
		}
	})

	t.Run("execute non-existent tool", func(t *testing.T) {
		invocation := &Invocation{
			ToolName:   "does_not_exist",
			Parameters: map[string]any{},
		}

		_, err := executor.Execute(context.Background(), invocation)
		if err == nil {
			t.Error("expected error for non-existent tool")
		}
	})
}

func TestExecutor_Requirements(t *testing.T) {
	registry := NewRegistry()

	tool := NewMockTool("requires_graph", CategoryExploration)
	tool.definition.Requires = []string{"graph_initialized"}
	registry.Register(tool)

	executor := NewExecutor(registry, nil)

	t.Run("requirement not met", func(t *testing.T) {
		invocation := &Invocation{
			ToolName:   "requires_graph",
			Parameters: map[string]any{},
		}

		_, err := executor.Execute(context.Background(), invocation)
		if err == nil {
			t.Error("expected error when requirement not met")
		}
	})

	t.Run("requirement satisfied", func(t *testing.T) {
		executor.SatisfyRequirement("graph_initialized")

		invocation := &Invocation{
			ToolName:   "requires_graph",
			Parameters: map[string]any{},
		}

		result, err := executor.Execute(context.Background(), invocation)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if !result.Success {
			t.Error("expected success after satisfying requirement")
		}
	})

	t.Run("unsatisfy requirement", func(t *testing.T) {
		executor.UnsatisfyRequirement("graph_initialized")

		if executor.IsRequirementSatisfied("graph_initialized") {
			t.Error("requirement should be unsatisfied")
		}
	})
}

func TestExecutor_Caching(t *testing.T) {
	registry := NewRegistry()

	callCount := 0
	tool := NewMockTool("cacheable", CategoryExploration)
	tool.definition.SideEffects = false
	tool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		callCount++
		return &Result{
			Success:    true,
			OutputText: "cached result",
		}, nil
	}
	registry.Register(tool)

	opts := DefaultExecutorOptions()
	opts.EnableCaching = true
	executor := NewExecutor(registry, &opts)

	invocation := &Invocation{
		ToolName:   "cacheable",
		Parameters: map[string]any{},
	}

	// First call
	result1, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}
	if result1.Cached {
		t.Error("first call should not be cached")
	}

	// Second call - should be cached
	result2, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("second execute failed: %v", err)
	}
	if !result2.Cached {
		t.Error("second call should be cached")
	}

	if callCount != 1 {
		t.Errorf("expected 1 actual call, got %d", callCount)
	}

	// Clear cache
	executor.ClearCache()

	// Third call after clear - should not be cached
	result3, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("third execute failed: %v", err)
	}
	if result3.Cached {
		t.Error("third call after clear should not be cached")
	}
}

func TestParamValidation(t *testing.T) {
	registry := NewRegistry()

	tool := NewMockTool("validation_test", CategoryExploration)
	minVal := 0.0
	maxVal := 100.0
	tool.definition.Parameters = map[string]ParamDef{
		"string_param": {
			Type:      ParamTypeString,
			Required:  true,
			MinLength: 1,
			MaxLength: 10,
		},
		"int_param": {
			Type:    ParamTypeInt,
			Minimum: &minVal,
			Maximum: &maxVal,
		},
		"enum_param": {
			Type: ParamTypeString,
			Enum: []any{"option1", "option2"},
		},
		"bool_param": {
			Type: ParamTypeBool,
		},
	}
	registry.Register(tool)

	executor := NewExecutor(registry, nil)

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "valid params",
			params:  map[string]any{"string_param": "hello"},
			wantErr: false,
		},
		{
			name:    "string too long",
			params:  map[string]any{"string_param": "this is way too long"},
			wantErr: true,
		},
		{
			name:    "int out of range",
			params:  map[string]any{"string_param": "ok", "int_param": 200},
			wantErr: true,
		},
		{
			name:    "invalid enum value",
			params:  map[string]any{"string_param": "ok", "enum_param": "invalid"},
			wantErr: true,
		},
		{
			name:    "wrong type",
			params:  map[string]any{"string_param": 123}, // int instead of string
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation := &Invocation{
				ToolName:   "validation_test",
				Parameters: tt.params,
			}

			_, err := executor.Execute(context.Background(), invocation)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
