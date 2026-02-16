// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package phases

import (
	"testing"
)

// TestExtractFunctionNameFromQuery_P0Fix tests the P0 fix for parameter extraction
// that was failing to extract "Process" from "control dependencies for Process function".
func TestExtractFunctionNameFromQuery_P0Fix(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "P0: control dependencies for X function",
			query: "Show control dependencies for Process function with depth 3",
			want:  "Process",
		},
		{
			name:  "P0: dependencies of X",
			query: "Find control dependencies of Handler",
			want:  "Handler",
		},
		{
			name:  "P0: dominates X method",
			query: "What dominates Middleware method",
			want:  "Middleware",
		},
		{
			name:  "P0: X function pattern",
			query: "Analyze getDatesToProcess function",
			want:  "getDatesToProcess",
		},
		{
			name:  "P0: common dependency for X and Y",
			query: "Find common dependency for Parser and Writer",
			want:  "Parser", // Should extract first symbol
		},
		{
			name:  "P0: should not extract 'control'",
			query: "control dependencies for HandleRequest function",
			want:  "HandleRequest", // NOT "control"!
		},
		{
			name:  "P0: should not extract 'dependencies'",
			query: "dependencies of ProcessData",
			want:  "ProcessData", // NOT "dependencies"!
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFunctionNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractFunctionNameFromQuery(%q) = %q, want %q",
					tt.query, got, tt.want)
			}
		})
	}
}
