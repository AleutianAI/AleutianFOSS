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
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func setupTestDispatcher(t *testing.T) (*Dispatcher, *Registry, *Executor) {
	t.Helper()

	registry := NewRegistry()

	// Register test tools
	echoTool := NewMockTool("echo", CategoryExploration)
	echoTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		msg, _ := params.ToMap()["message"].(string)
		return &Result{
			Success:    true,
			OutputText: "Echo: " + msg,
		}, nil
	}
	echoTool.definition.Parameters = map[string]ParamDef{
		"message": {Type: ParamTypeString, Required: true, Description: "Message to echo"},
	}
	registry.Register(echoTool)

	slowTool := NewMockTool("slow_tool", CategoryExploration)
	slowTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return &Result{Success: true, OutputText: "Done"}, nil
		}
	}
	registry.Register(slowTool)

	failingTool := NewMockTool("failing_tool", CategoryExploration)
	failingTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		return nil, errors.New("intentional failure")
	}
	registry.Register(failingTool)

	sideEffectTool := NewMockTool("side_effect_tool", CategoryFile)
	sideEffectTool.definition.SideEffects = true
	sideEffectTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		return &Result{Success: true, OutputText: "Side effect executed"}, nil
	}
	registry.Register(sideEffectTool)

	executor := NewExecutor(registry, nil)
	dispatcher := NewDispatcher(registry, executor)

	return dispatcher, registry, executor
}

func TestDispatcher_Execute_SingleTool(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	calls := []ToolCall{
		{
			ID:     "test-1",
			Name:   "echo",
			Params: json.RawMessage(`{"message": "hello"}`),
		},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("Execute() got %d results, want 1", len(result.Results))
	}

	if !result.Results[0].Result.Success {
		t.Errorf("Execute() result not successful: %s", result.Results[0].Result.Error)
	}

	if !strings.Contains(result.Results[0].Result.OutputText, "Echo: hello") {
		t.Errorf("Execute() output = %q, want to contain 'Echo: hello'", result.Results[0].Result.OutputText)
	}

	if result.FailedCount != 0 {
		t.Errorf("Execute() FailedCount = %d, want 0", result.FailedCount)
	}
}

func TestDispatcher_Execute_MultipleTools(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	calls := []ToolCall{
		{ID: "test-1", Name: "echo", Params: json.RawMessage(`{"message": "first"}`)},
		{ID: "test-2", Name: "echo", Params: json.RawMessage(`{"message": "second"}`)},
		{ID: "test-3", Name: "echo", Params: json.RawMessage(`{"message": "third"}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(result.Results) != 3 {
		t.Fatalf("Execute() got %d results, want 3", len(result.Results))
	}

	for i, r := range result.Results {
		if !r.Result.Success {
			t.Errorf("Execute() result[%d] not successful", i)
		}
	}
}

func TestDispatcher_Execute_UnknownTool(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	calls := []ToolCall{
		{ID: "test-1", Name: "nonexistent_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("Execute() got %d results, want 1", len(result.Results))
	}

	if result.Results[0].Result.Success {
		t.Error("Execute() unknown tool should fail")
	}

	if !strings.Contains(result.Results[0].Result.Error, "unknown tool") {
		t.Errorf("Execute() error = %q, want to contain 'unknown tool'", result.Results[0].Result.Error)
	}

	if result.FailedCount != 1 {
		t.Errorf("Execute() FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestDispatcher_Execute_ToolFailure(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	calls := []ToolCall{
		{ID: "test-1", Name: "failing_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Results[0].Result.Success {
		t.Error("Execute() failing tool should not succeed")
	}

	if result.FailedCount != 1 {
		t.Errorf("Execute() FailedCount = %d, want 1", result.FailedCount)
	}
}

func TestDispatcher_Execute_Timeout(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	// Set short timeout
	dispatcher.timeout = 100 * time.Millisecond

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "slow_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Results[0].Result.Success {
		t.Error("Execute() slow tool should timeout")
	}

	if !strings.Contains(result.Results[0].Result.Error, "timed out") &&
		!strings.Contains(result.Results[0].Result.Error, "timeout") &&
		!strings.Contains(result.Results[0].Result.Error, "deadline") {
		t.Errorf("Execute() error = %q, want to contain 'timeout', 'timed out', or 'deadline'", result.Results[0].Result.Error)
	}
}

func TestDispatcher_Execute_ContextCancellation(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	calls := []ToolCall{
		{ID: "test-1", Name: "echo", Params: json.RawMessage(`{"message": "hello"}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Result should be empty since context was cancelled before execution
	if len(result.Results) > 0 && result.Results[0].Result != nil && result.Results[0].Result.Success {
		t.Error("Execute() with cancelled context should not succeed")
	}
}

func TestDispatcher_Execute_ApprovalRequired(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	approvalCalled := false
	dispatcher.SetApprover(func(tool *ToolDefinition, params map[string]any) (bool, error) {
		approvalCalled = true
		return true, nil
	})

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !approvalCalled {
		t.Error("Execute() approval function not called for side effect tool")
	}

	if !result.Results[0].Result.Success {
		t.Errorf("Execute() approved tool should succeed: %s", result.Results[0].Result.Error)
	}

	if result.ApprovedCount != 1 {
		t.Errorf("Execute() ApprovedCount = %d, want 1", result.ApprovedCount)
	}
}

func TestDispatcher_Execute_ApprovalDeclined(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	dispatcher.SetApprover(func(tool *ToolDefinition, params map[string]any) (bool, error) {
		return false, nil // User declined
	})

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.Results[0].Result.Success {
		t.Error("Execute() declined tool should not succeed")
	}

	if !strings.Contains(result.Results[0].Result.Error, "declined") {
		t.Errorf("Execute() error = %q, want to contain 'declined'", result.Results[0].Result.Error)
	}
}

func TestDispatcher_ExecuteParallel(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	// Create multiple calls that can run in parallel
	calls := []ToolCall{
		{ID: "test-1", Name: "echo", Params: json.RawMessage(`{"message": "one"}`)},
		{ID: "test-2", Name: "echo", Params: json.RawMessage(`{"message": "two"}`)},
		{ID: "test-3", Name: "echo", Params: json.RawMessage(`{"message": "three"}`)},
		{ID: "test-4", Name: "echo", Params: json.RawMessage(`{"message": "four"}`)},
	}

	result, err := dispatcher.ExecuteParallel(ctx, calls, 2)
	if err != nil {
		t.Fatalf("ExecuteParallel() error = %v", err)
	}

	// Results should be in order
	if len(result.Results) != 4 {
		t.Fatalf("ExecuteParallel() got %d results, want 4", len(result.Results))
	}

	// Verify order is preserved
	for i, r := range result.Results {
		if r.Call.ID != calls[i].ID {
			t.Errorf("ExecuteParallel() result[%d].ID = %s, want %s", i, r.Call.ID, calls[i].ID)
		}
	}
}

func TestDispatcher_ExecuteParallel_Concurrency(t *testing.T) {
	registry := NewRegistry()

	// Track concurrent executions
	var current int32
	var maxConcurrent int32

	concurrentTool := NewMockTool("concurrent", CategoryExploration)
	concurrentTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		c := atomic.AddInt32(&current, 1)
		defer atomic.AddInt32(&current, -1)

		// Update max if current is higher
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if c <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxConcurrent, old, c) {
				break
			}
		}

		time.Sleep(50 * time.Millisecond)
		return &Result{Success: true}, nil
	}
	registry.Register(concurrentTool)

	executor := NewExecutor(registry, nil)
	dispatcher := NewDispatcher(registry, executor)

	ctx := context.Background()
	calls := make([]ToolCall, 10)
	for i := range calls {
		calls[i] = ToolCall{ID: string(rune('0' + i)), Name: "concurrent", Params: json.RawMessage(`{}`)}
	}

	// Execute with max 3 concurrent
	_, err := dispatcher.ExecuteParallel(ctx, calls, 3)
	if err != nil {
		t.Fatalf("ExecuteParallel() error = %v", err)
	}

	if maxConcurrent > 3 {
		t.Errorf("ExecuteParallel() max concurrent = %d, want <= 3", maxConcurrent)
	}
}

func TestDispatcher_Dispatch(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	input := `I'll help you with that.
<tool_call>
<name>echo</name>
<params>{"message": "hello world"}</params>
</tool_call>
Done!`

	result, err := dispatcher.Dispatch(ctx, input)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("Dispatch() got %d results, want 1", len(result.Results))
	}

	if !result.Results[0].Result.Success {
		t.Errorf("Dispatch() result not successful: %s", result.Results[0].Result.Error)
	}

	if result.RemainingText != "I'll help you with that.\n\nDone!" {
		t.Errorf("Dispatch() remaining = %q", result.RemainingText)
	}
}

func TestDispatcher_Dispatch_NoToolCalls(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	input := "Just some text without any tool calls."

	result, err := dispatcher.Dispatch(ctx, input)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if len(result.Results) != 0 {
		t.Errorf("Dispatch() got %d results, want 0", len(result.Results))
	}

	if result.RemainingText != input {
		t.Errorf("Dispatch() remaining = %q, want %q", result.RemainingText, input)
	}
}

func TestDispatcher_TooManyToolCalls(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	// Create more than MaxToolCallsPerRequest calls
	calls := make([]ToolCall, MaxToolCallsPerRequest+1)
	for i := range calls {
		calls[i] = ToolCall{Name: "echo", Params: json.RawMessage(`{"message": "test"}`)}
	}

	_, err := dispatcher.Execute(ctx, calls)
	if err == nil {
		t.Error("Execute() should error with too many calls")
	}

	if !strings.Contains(err.Error(), "too many tool calls") {
		t.Errorf("Execute() error = %q, want to contain 'too many tool calls'", err.Error())
	}
}

func TestDispatcher_NilContext(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	calls := []ToolCall{
		{Name: "echo", Params: json.RawMessage(`{"message": "test"}`)},
	}

	_, err := dispatcher.Execute(nil, calls)
	if err == nil {
		t.Error("Execute() should error with nil context")
	}
}

func TestDispatcher_FormattedOutput(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	calls := []ToolCall{
		{ID: "test-1", Name: "echo", Params: json.RawMessage(`{"message": "hello"}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check formatted output contains expected XML structure
	if !strings.Contains(result.FormattedOutput, "<tool_result>") {
		t.Error("FormattedOutput should contain <tool_result>")
	}
	if !strings.Contains(result.FormattedOutput, "<name>echo</name>") {
		t.Error("FormattedOutput should contain tool name")
	}
	if !strings.Contains(result.FormattedOutput, "<success>true</success>") {
		t.Error("FormattedOutput should contain success status")
	}
}

func TestSanitizeParams(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		params   json.RawMessage
		wantKey  string
		wantVal  string
	}{
		{
			name:     "bash command redacted",
			toolName: "bash_execute",
			params:   json.RawMessage(`{"command": "secret_command"}`),
			wantKey:  "command",
			wantVal:  "[REDACTED]",
		},
		{
			name:     "write content redacted",
			toolName: "write_file",
			params:   json.RawMessage(`{"path": "/foo", "content": "secret data"}`),
			wantKey:  "content",
			wantVal:  "[REDACTED]",
		},
		{
			name:     "non-sensitive tool unchanged",
			toolName: "read_file",
			params:   json.RawMessage(`{"path": "/foo/bar.go"}`),
			wantKey:  "path",
			wantVal:  "/foo/bar.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeParams(tt.toolName, tt.params)

			var data map[string]any
			if err := json.Unmarshal(result, &data); err != nil {
				t.Fatalf("SanitizeParams() result not valid JSON: %v", err)
			}

			if val, ok := data[tt.wantKey]; !ok {
				t.Errorf("SanitizeParams() missing key %q", tt.wantKey)
			} else if val != tt.wantVal {
				t.Errorf("SanitizeParams() [%s] = %q, want %q", tt.wantKey, val, tt.wantVal)
			}
		})
	}
}

func TestDispatcher_GenerateToolsPrompt(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	prompt := dispatcher.GenerateToolsPrompt(nil, nil)

	if !strings.Contains(prompt, "echo") {
		t.Error("GenerateToolsPrompt() should contain registered tools")
	}
	if !strings.Contains(prompt, "<tool_call>") {
		t.Error("GenerateToolsPrompt() should contain usage instructions")
	}
}

func TestDispatcher_GroupToolCallsByCategory(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	calls := []ToolCall{
		{Name: "echo", Params: json.RawMessage(`{}`)},
		{Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
		{Name: "unknown", Params: json.RawMessage(`{}`)},
	}

	grouped := dispatcher.GroupToolCallsByCategory(calls)

	if len(grouped[CategoryExploration]) != 1 {
		t.Errorf("GroupToolCallsByCategory() exploration = %d, want 1", len(grouped[CategoryExploration]))
	}
	if len(grouped[CategoryFile]) != 1 {
		t.Errorf("GroupToolCallsByCategory() file = %d, want 1", len(grouped[CategoryFile]))
	}
}

func TestMergeResults(t *testing.T) {
	r1 := &DispatchResult{
		Results: []ExecutionResult{
			{Call: ToolCall{Name: "tool1"}},
		},
		FormattedOutput: "output1",
		FailedCount:     1,
		TotalDuration:   time.Second,
	}

	r2 := &DispatchResult{
		Results: []ExecutionResult{
			{Call: ToolCall{Name: "tool2"}},
			{Call: ToolCall{Name: "tool3"}},
		},
		FormattedOutput: "output2",
		ApprovedCount:   2,
		TotalDuration:   2 * time.Second,
	}

	merged := MergeResults(r1, r2)

	if len(merged.Results) != 3 {
		t.Errorf("MergeResults() got %d results, want 3", len(merged.Results))
	}
	if merged.FailedCount != 1 {
		t.Errorf("MergeResults() FailedCount = %d, want 1", merged.FailedCount)
	}
	if merged.ApprovedCount != 2 {
		t.Errorf("MergeResults() ApprovedCount = %d, want 2", merged.ApprovedCount)
	}
	if merged.TotalDuration != 3*time.Second {
		t.Errorf("MergeResults() TotalDuration = %v, want 3s", merged.TotalDuration)
	}
}

func TestFilterToolCalls(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file"},
		{Name: "write_file"},
		{Name: "read_config"},
	}

	filtered := FilterToolCalls(calls, func(tc ToolCall) bool {
		return strings.HasPrefix(tc.Name, "read_")
	})

	if len(filtered) != 2 {
		t.Errorf("FilterToolCalls() got %d, want 2", len(filtered))
	}
}

func TestDispatcher_DispatchWithRetry(t *testing.T) {
	registry := NewRegistry()

	// Create a tool that fails on first attempt but succeeds on retry
	var attemptCount int32
	retryTool := NewMockTool("retry_tool", CategoryExploration)
	retryTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count == 1 {
			// Return a result with error that looks like a timeout (retryable)
			// Note: We return a Result with error message since executeSingle
			// converts executor errors to Result.Error
			return &Result{
				Success: false,
				Error:   "timeout: operation took too long",
			}, nil
		}
		return &Result{Success: true, OutputText: "Success on retry"}, nil
	}
	registry.Register(retryTool)

	executor := NewExecutor(registry, nil)
	dispatcher := NewDispatcher(registry, executor)

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "retry_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.DispatchWithRetry(ctx, calls, 3)
	if err != nil {
		t.Fatalf("DispatchWithRetry() error = %v", err)
	}

	if len(result.Results) != 1 {
		t.Fatalf("DispatchWithRetry() got %d results, want 1", len(result.Results))
	}

	if !result.Results[0].Result.Success {
		t.Errorf("DispatchWithRetry() should succeed after retry: %s", result.Results[0].Result.Error)
	}

	if atomic.LoadInt32(&attemptCount) < 2 {
		t.Errorf("DispatchWithRetry() should have retried, attemptCount = %d", attemptCount)
	}
}

func TestDispatcher_DispatchWithRetry_NonRetryable(t *testing.T) {
	registry := NewRegistry()

	// Create a tool that always fails with non-retryable error
	nonRetryTool := NewMockTool("non_retry_tool", CategoryExploration)
	attemptCount := 0
	nonRetryTool.ExecuteFunc = func(ctx context.Context, params TypedParams) (*Result, error) {
		attemptCount++
		return nil, errors.New("validation failed")
	}
	registry.Register(nonRetryTool)

	executor := NewExecutor(registry, nil)
	dispatcher := NewDispatcher(registry, executor)

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "non_retry_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.DispatchWithRetry(ctx, calls, 3)
	if err != nil {
		t.Fatalf("DispatchWithRetry() error = %v", err)
	}

	if result.Results[0].Result.Success {
		t.Error("DispatchWithRetry() non-retryable error should not succeed")
	}

	// Should not retry for non-retryable errors
	if attemptCount > 1 {
		t.Errorf("DispatchWithRetry() should not retry non-retryable errors, attemptCount = %d", attemptCount)
	}
}

func TestDispatcher_ConcurrentSetApprover(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)
	ctx := context.Background()

	// Run SetApprover and Execute concurrently to verify thread safety
	done := make(chan bool)

	// Start goroutine that continuously sets approvers
	go func() {
		for i := 0; i < 100; i++ {
			dispatcher.SetApprover(func(tool *ToolDefinition, params map[string]any) (bool, error) {
				return true, nil
			})
		}
		done <- true
	}()

	// Run executions concurrently
	go func() {
		for i := 0; i < 100; i++ {
			calls := []ToolCall{
				{ID: "test", Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
			}
			_, _ = dispatcher.Execute(ctx, calls)
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// If we get here without a race condition, the test passes
}

func TestDispatcher_DeclinedCount(t *testing.T) {
	dispatcher, _, _ := setupTestDispatcher(t)

	dispatcher.SetApprover(func(tool *ToolDefinition, params map[string]any) (bool, error) {
		return false, nil // Always decline
	})

	ctx := context.Background()
	calls := []ToolCall{
		{ID: "test-1", Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
		{ID: "test-2", Name: "side_effect_tool", Params: json.RawMessage(`{}`)},
	}

	result, err := dispatcher.Execute(ctx, calls)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.DeclinedCount != 2 {
		t.Errorf("Execute() DeclinedCount = %d, want 2", result.DeclinedCount)
	}

	// Verify the Declined field is set on individual results
	for i, r := range result.Results {
		if !r.Declined {
			t.Errorf("Execute() result[%d].Declined = false, want true", i)
		}
	}
}

func TestNewDispatcher_NilRegistry(t *testing.T) {
	executor := NewExecutor(NewRegistry(), nil)

	dispatcher := NewDispatcher(nil, executor)
	if dispatcher != nil {
		t.Error("NewDispatcher() with nil registry should return nil")
	}
}

func TestNewDispatcher_NilExecutor(t *testing.T) {
	registry := NewRegistry()

	dispatcher := NewDispatcher(registry, nil)
	if dispatcher != nil {
		t.Error("NewDispatcher() with nil executor should return nil")
	}
}
