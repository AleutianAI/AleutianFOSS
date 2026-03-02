// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package phases

import "testing"

// TestValidateToolQuerySemantics_CallChainCorrection tests IT-05 R1: call chain
// queries misrouted to find_callers/find_callees are corrected to get_call_chain.
func TestValidateToolQuerySemantics_CallChainCorrection(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		selectedTool string
		wantTool     string
		wantChanged  bool
	}{
		{
			name:         "call chain misrouted to find_callers",
			query:        "Show the call chain from main",
			selectedTool: "find_callers",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "call graph misrouted to find_callees",
			query:        "Build the call graph from parseConfig",
			selectedTool: "find_callees",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "call hierarchy misrouted to find_callers",
			query:        "Get the call hierarchy of Handler",
			selectedTool: "find_callers",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "call tree misrouted to find_callees",
			query:        "Show the call tree from initServer",
			selectedTool: "find_callees",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "transitive call misrouted to find_callers",
			query:        "Find all transitive callers of Process",
			selectedTool: "find_callers",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "recursive call misrouted to find_callees",
			query:        "Find recursive call paths from main",
			selectedTool: "find_callees",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "full call misrouted to find_callers",
			query:        "Show the full call trace from LoadConfig",
			selectedTool: "find_callers",
			wantTool:     "get_call_chain",
			wantChanged:  true,
		},
		{
			name:         "non-call-chain query stays as find_callers",
			query:        "Who calls parseConfig?",
			selectedTool: "find_callers",
			wantTool:     "find_callers",
			wantChanged:  false,
		},
		{
			name:         "non-call-chain query stays as find_callees",
			query:        "What does main call?",
			selectedTool: "find_callees",
			wantTool:     "find_callees",
			wantChanged:  false,
		},
		{
			name:         "call chain query on non-caller/callee tool — no change",
			query:        "Show the call chain from main",
			selectedTool: "find_symbol",
			wantTool:     "find_symbol",
			wantChanged:  false,
		},
		{
			name:         "call chain query on get_call_chain — no change",
			query:        "Show the call chain from main",
			selectedTool: "get_call_chain",
			wantTool:     "get_call_chain",
			wantChanged:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTool, gotChanged, reason := ValidateToolQuerySemantics(tt.query, tt.selectedTool)
			if gotTool != tt.wantTool {
				t.Errorf("ValidateToolQuerySemantics(%q, %q) tool = %q, want %q (reason: %s)",
					tt.query, tt.selectedTool, gotTool, tt.wantTool, reason)
			}
			if gotChanged != tt.wantChanged {
				t.Errorf("ValidateToolQuerySemantics(%q, %q) changed = %v, want %v (reason: %s)",
					tt.query, tt.selectedTool, gotChanged, tt.wantChanged, reason)
			}
		})
	}
}

// TestValidateToolQuerySemantics_CallersCalleesCorrection tests the existing
// callers/callees semantic correction that predates IT-05.
func TestValidateToolQuerySemantics_CallersCalleesCorrection(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		selectedTool string
		wantTool     string
		wantChanged  bool
	}{
		{
			name:         "what does X call misrouted to find_callers",
			query:        "What does main call?",
			selectedTool: "find_callers",
			wantTool:     "find_callees",
			wantChanged:  true,
		},
		{
			name:         "who calls X misrouted to find_callees",
			query:        "Who calls parseConfig?",
			selectedTool: "find_callees",
			wantTool:     "find_callers",
			wantChanged:  true,
		},
		{
			name:         "callers of X misrouted to find_callees",
			query:        "Show callers of Handler",
			selectedTool: "find_callees",
			wantTool:     "find_callers",
			wantChanged:  true,
		},
		{
			name:         "correctly routed find_callers unchanged",
			query:        "Who calls parseConfig?",
			selectedTool: "find_callers",
			wantTool:     "find_callers",
			wantChanged:  false,
		},
		{
			name:         "correctly routed find_callees unchanged",
			query:        "What does main call?",
			selectedTool: "find_callees",
			wantTool:     "find_callees",
			wantChanged:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTool, gotChanged, _ := ValidateToolQuerySemantics(tt.query, tt.selectedTool)
			if gotTool != tt.wantTool {
				t.Errorf("ValidateToolQuerySemantics(%q, %q) tool = %q, want %q",
					tt.query, tt.selectedTool, gotTool, tt.wantTool)
			}
			if gotChanged != tt.wantChanged {
				t.Errorf("ValidateToolQuerySemantics(%q, %q) changed = %v, want %v",
					tt.query, tt.selectedTool, gotChanged, tt.wantChanged)
			}
		})
	}
}
