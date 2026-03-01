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
	"encoding/json"
	"testing"
)

func TestToolCallResponse_ArgumentsString_Object(t *testing.T) {
	tc := ToolCallResponse{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"/foo/bar.go","depth":3}`),
	}

	result := tc.ArgumentsString()
	if result != `{"path":"/foo/bar.go","depth":3}` {
		t.Errorf("ArgumentsString() = %q, want JSON object string", result)
	}
}

func TestToolCallResponse_ArgumentsString_String(t *testing.T) {
	// Some models return arguments as a JSON string
	tc := ToolCallResponse{
		ID:        "call-2",
		Name:      "search",
		Arguments: json.RawMessage(`"{\"query\":\"hello\"}"`),
	}

	result := tc.ArgumentsString()
	if result != `{"query":"hello"}` {
		t.Errorf("ArgumentsString() = %q, want unquoted JSON string", result)
	}
}

func TestToolCallResponse_ArgumentsString_Empty(t *testing.T) {
	tc := ToolCallResponse{
		ID:   "call-3",
		Name: "no_args",
	}

	result := tc.ArgumentsString()
	if result != "{}" {
		t.Errorf("ArgumentsString() = %q, want %q", result, "{}")
	}
}

func TestToolCallResponse_ArgumentsString_NilArguments(t *testing.T) {
	tc := ToolCallResponse{
		ID:        "call-4",
		Name:      "nil_args",
		Arguments: nil,
	}

	result := tc.ArgumentsString()
	if result != "{}" {
		t.Errorf("ArgumentsString() = %q, want %q", result, "{}")
	}
}

func TestToolCallResponse_ArgumentsString_Array(t *testing.T) {
	tc := ToolCallResponse{
		ID:        "call-5",
		Name:      "array_args",
		Arguments: json.RawMessage(`[1,2,3]`),
	}

	result := tc.ArgumentsString()
	if result != `[1,2,3]` {
		t.Errorf("ArgumentsString() = %q, want %q", result, `[1,2,3]`)
	}
}

func TestToolDef_JSONRoundTrip(t *testing.T) {
	def := ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_file",
			Description: "Read a file from the filesystem",
			Parameters: ToolParameters{
				Type: "object",
				Properties: map[string]ToolParamDef{
					"path": {
						Type:        "string",
						Description: "File path to read",
					},
					"depth": {
						Type:        "integer",
						Description: "Read depth",
						Default:     3,
					},
				},
				Required: []string{"path"},
			},
		},
	}

	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ToolDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Function.Name != "read_file" {
		t.Errorf("Name = %q, want %q", decoded.Function.Name, "read_file")
	}
	if len(decoded.Function.Parameters.Properties) != 2 {
		t.Errorf("Properties count = %d, want 2", len(decoded.Function.Parameters.Properties))
	}
	if len(decoded.Function.Parameters.Required) != 1 || decoded.Function.Parameters.Required[0] != "path" {
		t.Errorf("Required = %v, want [path]", decoded.Function.Parameters.Required)
	}
}

func TestChatMessage_JSONRoundTrip(t *testing.T) {
	msg := ChatMessage{
		Role:    "assistant",
		Content: "I'll call a tool",
		ToolCalls: []ToolCallResponse{
			{
				ID:        "tc-1",
				Name:      "search",
				Arguments: json.RawMessage(`{"query":"test"}`),
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ChatMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.Role != "assistant" {
		t.Errorf("Role = %q, want %q", decoded.Role, "assistant")
	}
	if len(decoded.ToolCalls) != 1 {
		t.Fatalf("ToolCalls count = %d, want 1", len(decoded.ToolCalls))
	}
	if decoded.ToolCalls[0].Name != "search" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", decoded.ToolCalls[0].Name, "search")
	}
}

func TestChatMessage_ToolResultFields(t *testing.T) {
	msg := ChatMessage{
		Role:       "tool",
		Content:    "result data",
		ToolCallID: "tc-1",
		ToolName:   "search",
	}

	if msg.ToolCallID != "tc-1" {
		t.Errorf("ToolCallID = %q, want %q", msg.ToolCallID, "tc-1")
	}
	if msg.ToolName != "search" {
		t.Errorf("ToolName = %q, want %q", msg.ToolName, "search")
	}
}
