// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package phases

import "testing"

// TestSplitCamelCase verifies camelCase and PascalCase boundary splitting.
func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic camelCase
		{"sceneGraphUpdate", "scene Graph Update"},
		{"meshRendering", "mesh Rendering"},
		{"dataFlowAnalysis", "data Flow Analysis"},

		// PascalCase
		{"CreateHemisphericLightMesh", "Create Hemispheric Light Mesh"},
		{"ApplicationHandle", "Application Handle"},

		// Leading underscore (common in JS/TS private methods)
		{"_resyncLightSources", "_resync Light Sources"},
		{"_CreateHemisphericLightMesh", "_Create Hemispheric Light Mesh"},

		// Uppercase acronyms
		{"HTMLParser", "HTML Parser"},
		{"parseJSON", "parse JSON"},
		{"XMLHTTPRequest", "XMLHTTP Request"},

		// Single word / short inputs
		{"scene", "scene"},
		{"Scene", "Scene"},
		{"a", "a"},
		{"", ""},

		// Dot-notation (dots preserved, camelCase still split)
		{"Scene.addMesh", "Scene.add Mesh"},
		{"Application.handleRequest", "Application.handle Request"},

		// Underscores preserved (split happens on camelCase only)
		{"my_variable", "my_variable"},
		{"scene_graph_update", "scene_graph_update"},

		// Mixed
		{"getHTTPResponse", "get HTTP Response"},
		{"loadOBJFile", "load OBJ File"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := splitCamelCase(tc.input)
			if result != tc.expected {
				t.Errorf("splitCamelCase(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}
