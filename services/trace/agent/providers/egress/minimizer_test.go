// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package egress

import (
	"log/slog"
	"strings"
	"testing"

	agentllm "github.com/AleutianAI/AleutianFOSS/services/trace/agent/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// =============================================================================
// Helpers
// =============================================================================

func testMinimizer(enabled bool) *DataMinimizer {
	return NewDataMinimizer(enabled, 10, slog.Default())
}

func testSystemPrompt() string {
	return `You are a code analysis agent.

## MANDATORY: TOOL-FIRST RESPONSE
You must always use tools before responding.
Never answer from memory.

## QUESTION → TOOL MAPPING
| Question Type | Tool |
|---|---|
| Find callers | find_callers |
| Explore package | explore_package |

## STOPPING CRITERIA (When You Have Enough Information)
Stop when you have found the answer.
Do not over-explore.

## GROUNDING RULES (Prevents Hallucination)
Always cite file paths and line numbers.
Never invent code.

## RESPONSE PATTERN
Follow this pattern for responses.
1. Summary
2. Evidence
3. Recommendation

## AVAILABLE TOOLS
The following tools are available for your use.`
}

func testToolDefs() []tools.ToolDefinition {
	return []tools.ToolDefinition{
		{Name: "find_callers", Description: "Find callers of a function"},
		{Name: "explore_package", Description: "Explore a package"},
		{Name: "Grep", Description: "Search for patterns"},
		{Name: "graph_overview", Description: "Show graph overview"},
	}
}

// =============================================================================
// System Prompt Filter Tests
// =============================================================================

func TestDataMinimizer_FilterSystemPrompt(t *testing.T) {
	m := testMinimizer(true)
	prompt := testSystemPrompt()

	filtered := m.filterSystemPrompt(prompt)

	// Should keep: intro, QUESTION → TOOL MAPPING, AVAILABLE TOOLS
	if !strings.Contains(filtered, "You are a code analysis agent") {
		t.Error("expected intro text to be preserved")
	}
	if !strings.Contains(filtered, "## QUESTION → TOOL MAPPING") {
		t.Error("expected QUESTION → TOOL MAPPING section to be preserved")
	}
	if !strings.Contains(filtered, "## AVAILABLE TOOLS") {
		t.Error("expected AVAILABLE TOOLS section to be preserved")
	}

	// Should strip: MANDATORY, STOPPING CRITERIA, GROUNDING RULES, RESPONSE PATTERN
	if strings.Contains(filtered, "## MANDATORY") {
		t.Error("expected MANDATORY section to be stripped")
	}
	if strings.Contains(filtered, "## STOPPING CRITERIA") {
		t.Error("expected STOPPING CRITERIA section to be stripped")
	}
	if strings.Contains(filtered, "## GROUNDING RULES") {
		t.Error("expected GROUNDING RULES section to be stripped")
	}
	if strings.Contains(filtered, "## RESPONSE PATTERN") {
		t.Error("expected RESPONSE PATTERN section to be stripped")
	}
	if strings.Contains(filtered, "Never answer from memory") {
		t.Error("expected MANDATORY section content to be stripped")
	}
	if strings.Contains(filtered, "Do not over-explore") {
		t.Error("expected STOPPING CRITERIA content to be stripped")
	}
}

func TestDataMinimizer_FilterSystemPrompt_Empty(t *testing.T) {
	m := testMinimizer(true)

	result := m.filterSystemPrompt("")
	if result != "" {
		t.Errorf("expected empty string for empty input, got %q", result)
	}
}

func TestDataMinimizer_FilterSystemPrompt_NoSections(t *testing.T) {
	m := testMinimizer(true)
	prompt := "Simple prompt with no sections."

	result := m.filterSystemPrompt(prompt)
	if result != prompt {
		t.Errorf("expected prompt unchanged, got %q", result)
	}
}

// =============================================================================
// Tool Definition Filter Tests
// =============================================================================

func TestDataMinimizer_FilterToolDefinitions_ForcedTool(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()
	choice := agentllm.ToolChoiceRequired("find_callers")

	filtered := m.filterToolDefinitions(defs, choice)

	if len(filtered) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(filtered))
	}
	if filtered[0].Name != "find_callers" {
		t.Errorf("expected find_callers, got %s", filtered[0].Name)
	}
}

func TestDataMinimizer_FilterToolDefinitions_AutoChoice(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()
	choice := agentllm.ToolChoiceAuto()

	filtered := m.filterToolDefinitions(defs, choice)

	if len(filtered) != len(defs) {
		t.Errorf("expected all %d tools kept for auto choice, got %d", len(defs), len(filtered))
	}
}

func TestDataMinimizer_FilterToolDefinitions_NoneChoice(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()
	choice := agentllm.ToolChoiceNone()

	filtered := m.filterToolDefinitions(defs, choice)

	if filtered != nil {
		t.Errorf("expected nil tools for none choice, got %d tools", len(filtered))
	}
}

func TestDataMinimizer_FilterToolDefinitions_NilChoice(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()

	filtered := m.filterToolDefinitions(defs, nil)

	if len(filtered) != len(defs) {
		t.Errorf("expected all tools kept for nil choice, got %d", len(filtered))
	}
}

func TestDataMinimizer_FilterToolDefinitions_ForcedToolNotFound(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()
	choice := agentllm.ToolChoiceRequired("nonexistent_tool")

	filtered := m.filterToolDefinitions(defs, choice)

	// Should keep all tools when forced tool not found
	if len(filtered) != len(defs) {
		t.Errorf("expected all %d tools when forced tool not found, got %d", len(defs), len(filtered))
	}
}

func TestDataMinimizer_FilterToolDefinitions_AnyChoice(t *testing.T) {
	m := testMinimizer(true)
	defs := testToolDefs()
	choice := agentllm.ToolChoiceAny()

	filtered := m.filterToolDefinitions(defs, choice)

	if len(filtered) != len(defs) {
		t.Errorf("expected all %d tools for any choice, got %d", len(defs), len(filtered))
	}
}

func TestDataMinimizer_FilterToolDefinitions_EmptyDefs(t *testing.T) {
	m := testMinimizer(true)
	choice := agentllm.ToolChoiceRequired("find_callers")

	filtered := m.filterToolDefinitions(nil, choice)

	if len(filtered) != 0 {
		t.Errorf("expected 0 tools for empty input, got %d", len(filtered))
	}
}

// =============================================================================
// Message Minimization Tests
// =============================================================================

func TestDataMinimizer_MinimizeMessages_TruncateLargeToolResults(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      10, // ~40 chars
		HistoryWindow:            20,
		CanReceiveFileSystemInfo: true,
	}

	largeContent := strings.Repeat("x", 200) // ~50 tokens, exceeds limit of 10
	messages := []agentllm.Message{
		{
			Role: "tool",
			ToolResults: []agentllm.ToolCallResult{
				{ToolCallID: "call_1", Content: largeContent},
			},
		},
	}

	result, truncated := m.minimizeMessages(messages, caps)

	if truncated != 1 {
		t.Errorf("expected 1 truncated result, got %d", truncated)
	}
	if !strings.Contains(result[0].ToolResults[0].Content, "[TRUNCATED:") {
		t.Error("expected truncation notice in tool result")
	}
	if len(result[0].ToolResults[0].Content) >= len(largeContent) {
		t.Error("expected tool result to be shorter after truncation")
	}
}

func TestDataMinimizer_MinimizeMessages_SmallToolResultsUnchanged(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      4000,
		HistoryWindow:            20,
		CanReceiveFileSystemInfo: true,
	}

	messages := []agentllm.Message{
		{
			Role: "tool",
			ToolResults: []agentllm.ToolCallResult{
				{ToolCallID: "call_1", Content: "short result"},
			},
		},
	}

	result, truncated := m.minimizeMessages(messages, caps)

	if truncated != 0 {
		t.Errorf("expected 0 truncated results, got %d", truncated)
	}
	if result[0].ToolResults[0].Content != "short result" {
		t.Error("expected small tool result to be unchanged")
	}
}

func TestDataMinimizer_MinimizeMessages_StripFilePaths(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      4000,
		HistoryWindow:            20,
		CanReceiveFileSystemInfo: false,
	}

	messages := []agentllm.Message{
		{
			Role:    "user",
			Content: "Look at /Users/jin/GolandProjects/AleutianFOSS/services/trace/main.go",
		},
	}

	result, _ := m.minimizeMessages(messages, caps)

	if strings.Contains(result[0].Content, "/Users/jin") {
		t.Error("expected absolute path to be stripped")
	}
	if !strings.Contains(result[0].Content, "./") {
		t.Error("expected relative path prefix after stripping")
	}
}

func TestDataMinimizer_MinimizeMessages_PreservePathsWhenAllowed(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      4000,
		HistoryWindow:            20,
		CanReceiveFileSystemInfo: true,
	}

	messages := []agentllm.Message{
		{
			Role:    "user",
			Content: "Look at /Users/jin/GolandProjects/AleutianFOSS/services/trace/main.go",
		},
	}

	result, _ := m.minimizeMessages(messages, caps)

	if !strings.Contains(result[0].Content, "/Users/jin") {
		t.Error("expected absolute path to be preserved when CanReceiveFileSystemInfo=true")
	}
}

func TestDataMinimizer_MinimizeMessages_HistoryCompression(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      4000,
		HistoryWindow:            3,
		CanReceiveFileSystemInfo: true,
	}

	messages := []agentllm.Message{
		{Role: "user", Content: "First question about architecture"},
		{Role: "assistant", Content: "First response about architecture"},
		{Role: "user", Content: "Second question about testing"},
		{Role: "assistant", Content: "Second response about testing", ToolCalls: []agentllm.ToolCall{{Name: "Grep"}}},
		{Role: "user", Content: "Recent question"},
		{Role: "assistant", Content: "Recent response"},
		{Role: "user", Content: "Latest question"},
	}

	result, _ := m.minimizeMessages(messages, caps)

	if len(result) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(result))
	}

	// First 4 messages (indices 0-3) are outside window of 3, should be compressed
	if !strings.Contains(result[0].Content, "[Previous turn:") {
		t.Errorf("expected message 0 to be compressed, got %q", result[0].Content)
	}
	if !strings.Contains(result[1].Content, "[Previous turn:") {
		t.Errorf("expected message 1 to be compressed, got %q", result[1].Content)
	}
	if !strings.Contains(result[3].Content, "tools: Grep") {
		t.Errorf("expected compressed assistant message to mention tools, got %q", result[3].Content)
	}

	// Last 3 messages (indices 4-6) are within window, should be verbatim
	if result[4].Content != "Recent question" {
		t.Errorf("expected message 4 unchanged, got %q", result[4].Content)
	}
	if result[6].Content != "Latest question" {
		t.Errorf("expected message 6 unchanged, got %q", result[6].Content)
	}
}

func TestDataMinimizer_MinimizeMessages_PathStrippingDoesNotMutateOriginal(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{
		MaxToolResultTokens:      0, // unlimited — skips truncation block
		HistoryWindow:            20,
		CanReceiveFileSystemInfo: false, // triggers path-stripping
	}

	originalContent := "Found at /Users/jin/GolandProjects/AleutianFOSS/services/trace/main.go"
	messages := []agentllm.Message{
		{
			Role: "tool",
			ToolResults: []agentllm.ToolCallResult{
				{ToolCallID: "call_1", Content: originalContent},
			},
		},
	}

	result, _ := m.minimizeMessages(messages, caps)

	// Verify the result was stripped
	if strings.Contains(result[0].ToolResults[0].Content, "/Users/jin") {
		t.Error("expected path to be stripped in result")
	}

	// Verify the ORIGINAL message was NOT mutated
	if messages[0].ToolResults[0].Content != originalContent {
		t.Errorf("original message was mutated: got %q, want %q",
			messages[0].ToolResults[0].Content, originalContent)
	}
}

func TestDataMinimizer_MinimizeMessages_EmptyMessages(t *testing.T) {
	m := testMinimizer(true)
	caps := ProviderCapabilities{HistoryWindow: 20, CanReceiveFileSystemInfo: true}

	result, truncated := m.minimizeMessages(nil, caps)

	if result != nil {
		t.Error("expected nil for nil input")
	}
	if truncated != 0 {
		t.Errorf("expected 0 truncated, got %d", truncated)
	}
}

// =============================================================================
// Context Window Truncation Tests
// =============================================================================

func TestDataMinimizer_TruncateToContextWindow(t *testing.T) {
	m := testMinimizer(true)

	// Create a request with many messages that exceed a small context window
	messages := make([]agentllm.Message, 20)
	for i := range messages {
		messages[i] = agentllm.Message{
			Role:    "user",
			Content: strings.Repeat("word ", 100), // ~100 tokens each
		}
	}

	request := &agentllm.Request{
		SystemPrompt: "Short system prompt",
		Messages:     messages,
	}

	// Set a small context window that can't fit all messages
	result, dropped := m.truncateToContextWindow(request, 500)

	if dropped == 0 {
		t.Error("expected some messages to be dropped")
	}
	if len(result.Messages) >= len(messages) {
		t.Errorf("expected fewer messages after truncation, got %d (was %d)", len(result.Messages), len(messages))
	}
	// Verify the original request is unchanged
	if len(request.Messages) != len(messages) {
		t.Error("original request should not be mutated")
	}
}

func TestDataMinimizer_TruncateToContextWindow_WithinLimit(t *testing.T) {
	m := testMinimizer(true)

	request := &agentllm.Request{
		SystemPrompt: "Short prompt",
		Messages: []agentllm.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result, dropped := m.truncateToContextWindow(request, 200000)

	if dropped != 0 {
		t.Errorf("expected 0 dropped messages, got %d", dropped)
	}
	if result != request {
		t.Error("expected same request object when within limit")
	}
}

func TestDataMinimizer_TruncateToContextWindow_ZeroMaxTokens(t *testing.T) {
	m := testMinimizer(true)

	request := &agentllm.Request{
		SystemPrompt: "Prompt",
		Messages:     []agentllm.Message{{Role: "user", Content: "Hello"}},
	}

	result, dropped := m.truncateToContextWindow(request, 0)

	if dropped != 0 {
		t.Errorf("expected 0 dropped for unlimited (0) max, got %d", dropped)
	}
	if result != request {
		t.Error("expected same request for unlimited max")
	}
}

// =============================================================================
// Full Pipeline Tests
// =============================================================================

func TestDataMinimizer_Minimize_FullPipeline(t *testing.T) {
	m := testMinimizer(true)
	m.SetCapabilities("anthropic", ProviderCapabilities{
		MaxContextTokens:         200000,
		MaxToolResultTokens:      10, // Very small to trigger truncation
		HistoryWindow:            2,
		CanReceiveFileSystemInfo: true,
	})

	request := &agentllm.Request{
		SystemPrompt: testSystemPrompt(),
		Messages: []agentllm.Message{
			{Role: "user", Content: "Old question 1"},
			{Role: "assistant", Content: "Old answer 1"},
			{Role: "user", Content: "Old question 2"},
			{Role: "assistant", Content: "Old answer 2"},
			{Role: "user", Content: "Recent question"},
			{Role: "tool", ToolResults: []agentllm.ToolCallResult{
				{ToolCallID: "c1", Content: strings.Repeat("result data ", 50)},
			}},
		},
		Tools:      testToolDefs(),
		ToolChoice: agentllm.ToolChoiceRequired("find_callers"),
	}

	result, stats := m.Minimize(request, "anthropic", "claude-sonnet-4-20250514")

	// Verify system prompt was filtered
	if strings.Contains(result.SystemPrompt, "## MANDATORY") {
		t.Error("expected MANDATORY section stripped from system prompt")
	}

	// Verify tools were filtered to only the forced tool
	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool (forced), got %d", len(result.Tools))
	}

	// Verify old messages were compressed
	if !strings.Contains(result.Messages[0].Content, "[Previous turn:") {
		t.Error("expected old messages to be compressed")
	}

	// Verify tool result was truncated
	if stats.TruncatedResults != 1 {
		t.Errorf("expected 1 truncated result, got %d", stats.TruncatedResults)
	}

	// Verify stats are populated
	if stats.OriginalTokens == 0 {
		t.Error("expected non-zero OriginalTokens")
	}
	if stats.MinimizedTokens >= stats.OriginalTokens {
		t.Errorf("expected minimized tokens (%d) < original tokens (%d)",
			stats.MinimizedTokens, stats.OriginalTokens)
	}
	if stats.SystemPromptDelta <= 0 {
		t.Error("expected positive system prompt delta")
	}
	if stats.ToolDefsDelta <= 0 {
		t.Error("expected positive tool defs delta")
	}

	// Verify original request is not mutated
	if len(request.Tools) != 4 {
		t.Error("original request tools should not be mutated")
	}
	if strings.Contains(request.Messages[0].Content, "[Previous turn:") {
		t.Error("original request messages should not be mutated")
	}
}

func TestDataMinimizer_SkipWhenSmall(t *testing.T) {
	m := NewDataMinimizer(true, 10000, slog.Default()) // High threshold

	request := &agentllm.Request{
		SystemPrompt: "Short",
		Messages:     []agentllm.Message{{Role: "user", Content: "Hello"}},
	}

	result, stats := m.Minimize(request, "anthropic", "claude-sonnet-4-20250514")

	if result != request {
		t.Error("expected same request when below minTokens threshold")
	}
	if stats.OriginalTokens == 0 {
		t.Error("expected OriginalTokens to be populated even when skipped")
	}
	if stats.MinimizedTokens != stats.OriginalTokens {
		t.Error("expected MinimizedTokens == OriginalTokens when skipped")
	}
}

func TestDataMinimizer_Disabled(t *testing.T) {
	m := testMinimizer(false) // disabled

	request := &agentllm.Request{
		SystemPrompt: testSystemPrompt(),
		Messages:     []agentllm.Message{{Role: "user", Content: "Hello"}},
		Tools:        testToolDefs(),
	}

	result, stats := m.Minimize(request, "anthropic", "claude-sonnet-4-20250514")

	if result != request {
		t.Error("expected same request when minimizer disabled")
	}
	if stats.OriginalTokens != 0 {
		t.Errorf("expected 0 OriginalTokens when disabled, got %d", stats.OriginalTokens)
	}
}

func TestDataMinimizer_OllamaSkipped(t *testing.T) {
	m := testMinimizer(true)

	request := &agentllm.Request{
		SystemPrompt: testSystemPrompt(),
		Messages:     []agentllm.Message{{Role: "user", Content: "Hello"}},
		Tools:        testToolDefs(),
	}

	result, stats := m.Minimize(request, "ollama", "llama3")

	if result != request {
		t.Error("expected same request for ollama (local provider)")
	}
	if stats.OriginalTokens != 0 {
		t.Errorf("expected 0 OriginalTokens for ollama, got %d", stats.OriginalTokens)
	}
}

// =============================================================================
// MinimizationStats Tests
// =============================================================================

func TestMinimizationStats_Reduction(t *testing.T) {
	tests := []struct {
		name     string
		stats    MinimizationStats
		expected float64
	}{
		{
			name:     "50% reduction",
			stats:    MinimizationStats{OriginalTokens: 1000, MinimizedTokens: 500},
			expected: 50.0,
		},
		{
			name:     "no reduction",
			stats:    MinimizationStats{OriginalTokens: 1000, MinimizedTokens: 1000},
			expected: 0.0,
		},
		{
			name:     "zero original",
			stats:    MinimizationStats{OriginalTokens: 0, MinimizedTokens: 0},
			expected: 0.0,
		},
		{
			name:     "full reduction",
			stats:    MinimizationStats{OriginalTokens: 1000, MinimizedTokens: 0},
			expected: 100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.Reduction()
			if got != tt.expected {
				t.Errorf("expected %.1f%%, got %.1f%%", tt.expected, got)
			}
		})
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestCopyRequest(t *testing.T) {
	original := &agentllm.Request{
		SystemPrompt: "prompt",
		Messages: []agentllm.Message{
			{Role: "user", Content: "hello"},
		},
		Tools: []tools.ToolDefinition{
			{Name: "tool1"},
		},
		ToolChoice:    agentllm.ToolChoiceAuto(),
		StopSequences: []string{"stop"},
	}

	cp := copyRequest(original)

	// Verify independence
	cp.SystemPrompt = "modified"
	if original.SystemPrompt == "modified" {
		t.Error("modifying copy should not affect original SystemPrompt")
	}

	cp.Messages[0].Content = "modified"
	if original.Messages[0].Content == "modified" {
		t.Error("modifying copy messages should not affect original")
	}

	cp.Tools[0].Name = "modified"
	if original.Tools[0].Name == "modified" {
		t.Error("modifying copy tools should not affect original")
	}

	cp.ToolChoice.Type = "none"
	if original.ToolChoice.Type == "none" {
		t.Error("modifying copy ToolChoice should not affect original")
	}
}

func TestEstimateTokens(t *testing.T) {
	if estimateTokens("") != 0 {
		t.Error("empty string should estimate 0 tokens")
	}

	// "ab" is 2 chars, 2/4 = 0, so should be clamped to 1
	if estimateTokens("ab") != 1 {
		t.Errorf("short string should estimate at least 1 token, got %d", estimateTokens("ab"))
	}

	// 400 chars = ~100 tokens
	if estimateTokens(strings.Repeat("x", 400)) != 100 {
		t.Errorf("expected ~100 tokens for 400 chars, got %d", estimateTokens(strings.Repeat("x", 400)))
	}
}

func TestStripAbsolutePaths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
		excludes string
	}{
		{
			name:     "Users path",
			input:    "File at /Users/jin/GolandProjects/AleutianFOSS/services/trace/main.go",
			contains: "./",
			excludes: "/Users/jin",
		},
		{
			name:     "home path",
			input:    "See /home/user/project/src/main.go",
			contains: "./",
			excludes: "/home/user",
		},
		{
			name:     "no absolute path",
			input:    "Just a regular string",
			contains: "Just a regular string",
			excludes: "./",
		},
		{
			name:     "empty string",
			input:    "",
			contains: "",
			excludes: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripAbsolutePaths(tt.input)
			if tt.contains != "" && !strings.Contains(result, tt.contains) {
				t.Errorf("expected result to contain %q, got %q", tt.contains, result)
			}
			if tt.excludes != "" && strings.Contains(result, tt.excludes) {
				t.Errorf("expected result to not contain %q, got %q", tt.excludes, result)
			}
		})
	}
}

func TestCleanBlankLines(t *testing.T) {
	input := "line1\n\n\n\n\nline2\n\nline3"
	result := cleanBlankLines(input)

	// Should collapse 4+ blank lines but not produce more than 2 blank lines
	// (2 blank lines = 3 newlines between content lines)
	if strings.Contains(result, "\n\n\n\n") {
		t.Error("expected runs of 3+ blank lines to be collapsed")
	}
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line2") {
		t.Error("expected content lines to be preserved")
	}
	// The pair of blank lines between line2 and line3 should be preserved
	if !strings.Contains(result, "line2\n\nline3") {
		t.Error("expected double blank line between line2 and line3 to be preserved")
	}
}

func TestCompressTurn(t *testing.T) {
	t.Run("user message", func(t *testing.T) {
		msg := agentllm.Message{Role: "user", Content: "What does this function do?"}
		compressed := compressTurn(msg)
		if !strings.Contains(compressed.Content, "[Previous turn:") {
			t.Error("expected compressed format")
		}
		if compressed.Role != "user" {
			t.Error("expected role preserved")
		}
	})

	t.Run("assistant with tool calls", func(t *testing.T) {
		msg := agentllm.Message{
			Role:      "assistant",
			Content:   "Let me check",
			ToolCalls: []agentllm.ToolCall{{Name: "Grep"}, {Name: "find_callers"}},
		}
		compressed := compressTurn(msg)
		if !strings.Contains(compressed.Content, "tools: Grep, find_callers") {
			t.Errorf("expected tool names in compressed, got %q", compressed.Content)
		}
	})

	t.Run("tool result", func(t *testing.T) {
		msg := agentllm.Message{
			Role: "tool",
			ToolResults: []agentllm.ToolCallResult{
				{ToolCallID: "call_123", Content: "result data"},
			},
		}
		compressed := compressTurn(msg)
		if !strings.Contains(compressed.Content, "call_123") {
			t.Errorf("expected tool call ID in compressed, got %q", compressed.Content)
		}
	})
}
