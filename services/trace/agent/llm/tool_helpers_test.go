// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package llm

import (
	"testing"

	basellm "github.com/AleutianAI/AleutianFOSS/services/llm"
	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// =============================================================================
// convertToolDefs Tests
// =============================================================================

func TestConvertToolDefs_Empty(t *testing.T) {
	result := convertToolDefs(nil)
	if result != nil {
		t.Errorf("convertToolDefs(nil) = %v, want nil", result)
	}

	result = convertToolDefs([]tools.ToolDefinition{})
	if result != nil {
		t.Errorf("convertToolDefs([]) = %v, want nil", result)
	}
}

func TestConvertToolDefs_SingleTool(t *testing.T) {
	defs := []tools.ToolDefinition{
		{
			Name:        "find_hotspots",
			Description: "Find complexity hotspots",
			Parameters: map[string]tools.ParamDef{
				"depth": {
					Type:        tools.ParamTypeInt,
					Description: "Max depth",
					Required:    true,
				},
				"format": {
					Type:        tools.ParamTypeString,
					Description: "Output format",
					Required:    false,
					Enum:        []any{"json", "text"},
					Default:     "text",
				},
			},
		},
	}

	result := convertToolDefs(defs)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}

	td := result[0]
	if td.Type != "function" {
		t.Errorf("Type = %q, want %q", td.Type, "function")
	}
	if td.Function.Name != "find_hotspots" {
		t.Errorf("Name = %q, want %q", td.Function.Name, "find_hotspots")
	}
	if td.Function.Description != "Find complexity hotspots" {
		t.Errorf("Description = %q, want %q", td.Function.Description, "Find complexity hotspots")
	}
	if td.Function.Parameters.Type != "object" {
		t.Errorf("Parameters.Type = %q, want %q", td.Function.Parameters.Type, "object")
	}
	if len(td.Function.Parameters.Properties) != 2 {
		t.Errorf("len(Properties) = %d, want 2", len(td.Function.Parameters.Properties))
	}

	depthParam, ok := td.Function.Parameters.Properties["depth"]
	if !ok {
		t.Fatal("missing 'depth' property")
	}
	if depthParam.Type != "integer" {
		t.Errorf("depth.Type = %q, want %q", depthParam.Type, "integer")
	}

	formatParam, ok := td.Function.Parameters.Properties["format"]
	if !ok {
		t.Fatal("missing 'format' property")
	}
	if len(formatParam.Enum) != 2 {
		t.Errorf("format.Enum len = %d, want 2", len(formatParam.Enum))
	}
	if formatParam.Default != "text" {
		t.Errorf("format.Default = %v, want %q", formatParam.Default, "text")
	}

	// Check required - only "depth" should be required
	foundDepth := false
	for _, r := range td.Function.Parameters.Required {
		if r == "depth" {
			foundDepth = true
		}
		if r == "format" {
			t.Error("'format' should not be in Required (Required=false)")
		}
	}
	if !foundDepth {
		t.Error("'depth' should be in Required")
	}
}

func TestConvertToolDefs_MultipleTtools(t *testing.T) {
	defs := []tools.ToolDefinition{
		{Name: "tool_a", Description: "Tool A"},
		{Name: "tool_b", Description: "Tool B"},
		{Name: "tool_c", Description: "Tool C"},
	}

	result := convertToolDefs(defs)
	if len(result) != 3 {
		t.Fatalf("len(result) = %d, want 3", len(result))
	}

	for i, name := range []string{"tool_a", "tool_b", "tool_c"} {
		if result[i].Function.Name != name {
			t.Errorf("result[%d].Function.Name = %q, want %q", i, result[i].Function.Name, name)
		}
	}
}

// =============================================================================
// convertToChat Tests
// =============================================================================

func TestConvertToChat_NilRequest(t *testing.T) {
	result := convertToChat(nil)
	if result != nil {
		t.Errorf("convertToChat(nil) = %v, want nil", result)
	}
}

func TestConvertToChat_SystemPrompt(t *testing.T) {
	request := &Request{
		SystemPrompt: "You are a code assistant",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result := convertToChat(request)

	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("result[0].Role = %q, want %q", result[0].Role, "system")
	}
	if result[0].Content != "You are a code assistant" {
		t.Errorf("result[0].Content = %q, want %q", result[0].Content, "You are a code assistant")
	}
}

func TestConvertToChat_NoSystemPrompt(t *testing.T) {
	request := &Request{
		Messages: []Message{
			{Role: "user", Content: "Hi"},
		},
	}

	result := convertToChat(request)
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("result[0].Role = %q, want %q", result[0].Role, "user")
	}
}

func TestConvertToChat_AssistantWithToolCalls(t *testing.T) {
	request := &Request{
		Messages: []Message{
			{
				Role:    "assistant",
				Content: "Let me search for that.",
				ToolCalls: []ToolCall{
					{
						ID:        "call-1",
						Name:      "find_hotspots",
						Arguments: `{"depth": 3}`,
					},
					{
						ID:        "call-2",
						Name:      "find_dead_code",
						Arguments: `{}`,
					},
				},
			},
		},
	}

	result := convertToChat(request)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}

	msg := result[0]
	if msg.Role != "assistant" {
		t.Errorf("Role = %q, want %q", msg.Role, "assistant")
	}
	if msg.Content != "Let me search for that." {
		t.Errorf("Content = %q, want %q", msg.Content, "Let me search for that.")
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call-1" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", msg.ToolCalls[0].ID, "call-1")
	}
	if msg.ToolCalls[0].Name != "find_hotspots" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", msg.ToolCalls[0].Name, "find_hotspots")
	}
	if string(msg.ToolCalls[0].Arguments) != `{"depth": 3}` {
		t.Errorf("ToolCalls[0].Arguments = %q, want %q", string(msg.ToolCalls[0].Arguments), `{"depth": 3}`)
	}
}

func TestConvertToChat_ToolResults(t *testing.T) {
	request := &Request{
		Messages: []Message{
			{
				Role: "tool",
				ToolResults: []ToolCallResult{
					{
						ToolCallID: "call-1",
						Content:    "Found 5 hotspots",
					},
				},
			},
		},
	}

	result := convertToChat(request)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}

	msg := result[0]
	if msg.Role != "tool" {
		t.Errorf("Role = %q, want %q", msg.Role, "tool")
	}
	if msg.ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q, want %q", msg.ToolCallID, "call-1")
	}
	if msg.Content != "Found 5 hotspots" {
		t.Errorf("Content = %q, want %q", msg.Content, "Found 5 hotspots")
	}
	if msg.ToolName != "call-1" {
		t.Errorf("ToolName = %q, want %q (should match ToolCallID)", msg.ToolName, "call-1")
	}
}

func TestConvertToChat_FullConversation(t *testing.T) {
	request := &Request{
		SystemPrompt: "System",
		Messages: []Message{
			{Role: "user", Content: "Find hotspots"},
			{
				Role:    "assistant",
				Content: "I'll find them.",
				ToolCalls: []ToolCall{
					{ID: "tc-1", Name: "find_hotspots", Arguments: `{"depth": 2}`},
				},
			},
			{
				Role: "tool",
				ToolResults: []ToolCallResult{
					{ToolCallID: "tc-1", Content: "Results here"},
				},
			},
			{Role: "assistant", Content: "Here are the results."},
		},
	}

	result := convertToChat(request)

	// system + 4 messages = 5
	if len(result) != 5 {
		t.Fatalf("len(result) = %d, want 5", len(result))
	}

	if result[0].Role != "system" {
		t.Errorf("result[0].Role = %q, want system", result[0].Role)
	}
	if result[1].Role != "user" {
		t.Errorf("result[1].Role = %q, want user", result[1].Role)
	}
	if result[2].Role != "assistant" && len(result[2].ToolCalls) != 1 {
		t.Error("result[2] should be assistant with tool call")
	}
	if result[3].Role != "tool" {
		t.Errorf("result[3].Role = %q, want tool", result[3].Role)
	}
	if result[4].Role != "assistant" {
		t.Errorf("result[4].Role = %q, want assistant", result[4].Role)
	}
}

// =============================================================================
// estimateInputTokensChat Tests
// =============================================================================

func TestEstimateInputTokensChat(t *testing.T) {
	messages := []basellm.ChatMessage{
		{Role: "system", Content: "1234567890123456"}, // 16 chars = 4 tokens
		{Role: "user", Content: "12345678"},           // 8 chars = 2 tokens
	}

	result := estimateInputTokensChat(messages)
	if result != 6 {
		t.Errorf("estimateInputTokensChat = %d, want 6", result)
	}
}

func TestEstimateInputTokensChat_Empty(t *testing.T) {
	result := estimateInputTokensChat(nil)
	if result != 0 {
		t.Errorf("estimateInputTokensChat(nil) = %d, want 0", result)
	}
}
