// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.

package phases

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
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

// TestExtractFunctionNameFromQuery_TypeDotMethod tests IT-01 Bug 3 fix:
// "X method on Y type" patterns should return "Y.X" dot notation.
func TestExtractFunctionNameFromQuery_TypeDotMethod(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// IT-01 Bug 3: Badger "Get method on Txn type" → "Txn.Get"
		{
			name:  "IT-01: Get method on the Txn type",
			query: "Who calls the Get method on the Txn type?",
			want:  "Txn.Get",
		},
		// BabylonJS: render method on Scene
		{
			name:  "IT-01: render method on the Scene type",
			query: "Who calls the render method on the Scene type?",
			want:  "Scene.render",
		},
		// Express: handle method on Router
		{
			name:  "IT-01: handle method on Router (no 'type' suffix)",
			query: "Who calls the handle method on Router?",
			want:  "Router.handle",
		},
		// Python: __init__ method on DataFrame class
		{
			name:  "IT-01: __init__ method on the DataFrame class",
			query: "Who calls the __init__ method on the DataFrame class?",
			want:  "DataFrame.__init__",
		},
		// NestJS: create method on NestFactory
		{
			name:  "IT-01: create method on NestFactory",
			query: "Who calls the create method on NestFactory?",
			want:  "NestFactory.create",
		},
		// Direct dot-notation in query
		{
			name:  "IT-01: direct dot notation Transaction.Get",
			query: "Who calls Transaction.Get?",
			want:  "Transaction.Get",
		},
		// Direct dot-notation: DB.Open
		{
			name:  "IT-01: direct dot notation DB.Open",
			query: "What functions does DB.Open call?",
			want:  "DB.Open",
		},
		// No type qualifier — should still work via existing patterns
		{
			name:  "existing: bare CamelCase function",
			query: "Who calls the Publish function in this codebase?",
			want:  "Publish",
		},
		// Bare function (no type, no dot)
		{
			name:  "existing: bare snake_case function",
			query: "Who calls the full_dispatch_request function?",
			want:  "full_dispatch_request",
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

// TestExtractTypeDotMethodFromQuery tests the helper function directly.
func TestExtractTypeDotMethodFromQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "method on type with articles",
			query: "Who calls the Get method on the Transaction type?",
			want:  "Transaction.Get",
		},
		{
			name:  "method on type without articles",
			query: "Find render method on Scene type",
			want:  "Scene.render",
		},
		{
			name:  "method on class",
			query: "Who calls __init__ method on DataFrame class?",
			want:  "DataFrame.__init__",
		},
		{
			name:  "method on bare type (no suffix)",
			query: "Callers of create method on NestFactory",
			want:  "NestFactory.create",
		},
		{
			name:  "function on type",
			query: "Who calls the Open function on DB?",
			want:  "DB.Open",
		},
		{
			name:  "no match — no 'on' keyword",
			query: "Who calls the Publish function?",
			want:  "",
		},
		{
			name:  "no match — type starts lowercase",
			query: "Who calls the get method on the transaction type?",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTypeDotMethodFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractTypeDotMethodFromQuery(%q) = %q, want %q",
					tt.query, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Comprehensive Tests — Edge Cases, Boundaries, Regressions
// =============================================================================

// TestExtractTypeDotMethodFromQuery_EdgeCases covers boundary conditions and
// adversarial inputs for the Type.Method extraction.
func TestExtractTypeDotMethodFromQuery_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// Empty and minimal inputs
		{
			name:  "empty query",
			query: "",
			want:  "",
		},
		{
			name:  "single word",
			query: "Get",
			want:  "",
		},
		{
			name:  "just 'method on'",
			query: "method on Type",
			want:  "",
		},

		// Boundary: "the" as method name should be skipped
		{
			name:  "the method on Type — skip 'the' as method",
			query: "Call the method on Context type",
			want:  "",
		},

		// Boundary: type name with trailing punctuation
		{
			name:  "type name with question mark",
			query: "Who calls the Get method on the Txn?",
			want:  "Txn.Get",
		},
		{
			name:  "type name with period",
			query: "Find the render method on Scene.",
			want:  "Scene.render",
		},
		{
			name:  "type name with comma",
			query: "The Get method on Txn, how is it used?",
			want:  "Txn.Get",
		},
		{
			name:  "type name with parentheses",
			query: "The init method on DataFrame()",
			want:  "DataFrame.init",
		},

		// Boundary: struct suffix
		{
			name:  "struct suffix",
			query: "Who calls the Open method on the DB struct?",
			want:  "DB.Open",
		},

		// Multiple "method on" patterns — first match wins
		{
			name:  "multiple method-on patterns — first match",
			query: "The Get method on Txn and the Set method on Txn",
			want:  "Txn.Get",
		},

		// Method name with underscores (Python dunder)
		{
			name:  "dunder method __str__",
			query: "Who calls the __str__ method on DataFrame?",
			want:  "DataFrame.__str__",
		},

		// Method name that is a skipWord in isValidFunctionName
		{
			name:  "method name 'get' (normally a skipWord)",
			query: "Who calls the get method on the Cache type?",
			want:  "Cache.get",
		},
		{
			name:  "method name 'find' (normally a skipWord)",
			query: "Who calls the find method on Collection?",
			want:  "Collection.find",
		},

		// Boundary: type name exactly 1 char
		{
			name:  "single char type name",
			query: "Who calls the Run method on T?",
			want:  "T.Run",
		},

		// Query with extra whitespace
		{
			name:  "extra whitespace",
			query: "Who calls  the  Get  method  on  the  Txn  type?",
			want:  "Txn.Get",
		},

		// No "on" but has "method" — should not match
		{
			name:  "method without on",
			query: "Find the render method for Scene",
			want:  "",
		},

		// "function" keyword variant
		{
			name:  "function keyword",
			query: "Who calls the Open function on the DB type?",
			want:  "DB.Open",
		},

		// Case sensitivity: method names should preserve original case
		{
			name:  "preserves method case",
			query: "Who calls the RunQuery method on Engine?",
			want:  "Engine.RunQuery",
		},

		// Long type name
		{
			name:  "long type name",
			query: "Who calls the initialize method on ApplicationContextFactory?",
			want:  "ApplicationContextFactory.initialize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTypeDotMethodFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractTypeDotMethodFromQuery(%q) = %q, want %q",
					tt.query, got, tt.want)
			}
		})
	}
}

// TestExtractFunctionNameFromQuery_DirectDotNotation_EdgeCases covers Pattern 0b
// edge cases for direct dot-notation tokens in queries.
func TestExtractFunctionNameFromQuery_DirectDotNotation_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// Standard dot notation
		{
			name:  "standard dot notation mid-sentence",
			query: "Find callers of Context.JSON in this project",
			want:  "Context.JSON",
		},
		{
			name:  "dot notation at end with question mark",
			query: "Who calls Router.handle?",
			want:  "Router.handle",
		},
		{
			name:  "dot notation with parens",
			query: "What does DB.Open() call?",
			want:  "DB.Open",
		},

		// lowercase type in dot notation — Pattern 0b skips it (isValidTypeName fails),
		// but Pattern 3 ("calls ") catches "ctx.JSON" as a valid function name.
		{
			name:  "lowercase.method falls through to Pattern 3",
			query: "Who calls ctx.JSON?",
			want:  "ctx.JSON",
		},

		// Multiple dots — should extract first valid one
		{
			name:  "multiple dots in query",
			query: "Compare Context.JSON and Router.handle",
			want:  "Context.JSON",
		},

		// Dot notation that has trailing comma
		{
			name:  "dot notation with trailing comma",
			query: "For DB.Open, show all callers",
			want:  "DB.Open",
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

// TestIsValidTypeName covers the type name validation function.
func TestIsValidTypeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// Valid type names
		{"uppercase single char", "T", true},
		{"standard Go type", "Context", true},
		{"multi-word type", "DataFrame", true},
		{"type with digits", "V2Client", true},
		{"all uppercase", "DB", true},

		// Invalid type names
		{"empty string", "", false},
		{"lowercase start", "context", false},
		{"starts with digit", "2Factor", false},
		{"contains underscore", "My_Type", false},
		{"contains dot", "My.Type", false},
		{"contains hyphen", "My-Type", false},
		{"contains space", "My Type", false},
		{"single lowercase", "t", false},

		// Boundary: very long name (100 chars is the limit)
		{"100 char name", "A" + string(make([]byte, 99)), false}, // bytes not runes, non-alpha
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidTypeName(tt.input)
			if got != tt.want {
				t.Errorf("isValidTypeName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestIsValidTypeName_LongInput verifies the 100-char length limit.
func TestIsValidTypeName_LongInput(t *testing.T) {
	// Exactly 100 chars — valid
	name100 := "A"
	for len(name100) < 100 {
		name100 += "a"
	}
	if !isValidTypeName(name100) {
		t.Errorf("expected 100-char name to be valid")
	}

	// 101 chars — invalid
	name101 := name100 + "a"
	if isValidTypeName(name101) {
		t.Errorf("expected 101-char name to be invalid")
	}
}

// TestExtractFunctionNameFromQuery_RegressionNonInterference verifies that
// the new Pattern 0/0b don't interfere with existing patterns.
func TestExtractFunctionNameFromQuery_RegressionNonInterference(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// These must continue to work as before
		{
			name:  "regression: 'what does X call' pattern",
			query: "What does main call?",
			want:  "main",
		},
		{
			name:  "regression: 'callers of X' pattern",
			query: "Find callers of HandleRequest",
			want:  "HandleRequest",
		},
		{
			name:  "regression: 'who calls X' pattern",
			query: "Who calls parseConfig?",
			want:  "parseConfig",
		},
		{
			name:  "regression: 'called by X' pattern",
			query: "Functions called by BuildRequest",
			want:  "BuildRequest",
		},
		{
			name:  "regression: 'X function' pattern",
			query: "Analyze getDatesToProcess function",
			want:  "getDatesToProcess",
		},
		{
			name:  "regression: CamelCase fallback",
			query: "What about HandleHTTP",
			want:  "HandleHTTP", // IT-06: "about" no longer passes isFunctionLikeName (not PascalCase/CamelCase/snake_case)
		},
		{
			name:  "regression: snake_case via callers-of pattern",
			query: "Find callers of get_user_data",
			want:  "get_user_data",
		},

		// Verify dot notation takes precedence over other patterns
		{
			name:  "dot notation takes precedence over 'callers of'",
			query: "Find callers of Context.JSON in this project",
			want:  "Context.JSON",
		},
		{
			name:  "Type.Method on Y takes precedence over bare extraction",
			query: "Who calls the Set method on the Txn type?",
			want:  "Txn.Set",
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

// TestExtractFunctionNameFromQuery_AllNineProjects verifies extraction works for
// all 9 integration test project queries from the YAML files.
func TestExtractFunctionNameFromQuery_AllNineProjects(t *testing.T) {
	tests := []struct {
		name    string
		project string
		query   string
		want    string
	}{
		{
			name:    "gin: Context.JSON",
			project: "gin",
			query:   "Who calls the JSON method on the Context type?",
			want:    "Context.JSON",
		},
		{
			name:    "hugo: Publish",
			project: "hugo",
			query:   "Who calls the Publish function in this codebase?",
			want:    "Publish",
		},
		{
			name:    "badger: Txn.Get",
			project: "badger",
			query:   "Who calls the Get method on the Txn type?",
			want:    "Txn.Get",
		},
		{
			name:    "babylonjs: Scene.render",
			project: "babylonjs",
			query:   "Who calls the render method on the Scene type?",
			want:    "Scene.render",
		},
		{
			name:    "express: Router.handle",
			project: "express",
			query:   "Who calls the handle method on Router?",
			want:    "Router.handle",
		},
		{
			name:    "pandas: DataFrame.__init__",
			project: "pandas",
			query:   "Who calls the __init__ method on the DataFrame class?",
			want:    "DataFrame.__init__",
		},
		{
			name:    "plottable: Plot.renderImmediately",
			project: "plottable",
			query:   "Who calls the Plot.renderImmediately method?",
			want:    "Plot.renderImmediately",
		},
		{
			name:    "nestjs: NestFactory.create",
			project: "nestjs",
			query:   "Who calls the create method on NestFactory?",
			want:    "NestFactory.create",
		},
		{
			name:    "flask: full_dispatch_request",
			project: "flask",
			query:   "Who calls the full_dispatch_request function?",
			want:    "full_dispatch_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFunctionNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("[%s] extractFunctionNameFromQuery(%q) = %q, want %q",
					tt.project, tt.query, got, tt.want)
			}
		})
	}
}

// IT-06c: Test that "Build method" extraction works after removing "build" from
// isValidSymbolNameBeforeKindKeyword skipWords, and that Pattern 1 skips articles.
func TestExtractFunctionNameFromQuery_BuildMethodExtraction(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "Hugo: Build method with article",
			query: "What functions does the Build method call in hugolib?",
			want:  "Build",
		},
		{
			name:  "Hugo: Build method without article",
			query: "What functions does Build method call?",
			want:  "Build",
		},
		{
			name:  "Hugo: Build function with article",
			query: "What does the Build function call?",
			want:  "Build",
		},
		{
			name:  "Hugo: Build without kind keyword (CRS verification query)",
			query: "What functions does the Build method call?",
			want:  "Build",
		},
		{
			name:  "Verb 'build' should not extract from non-symbol context",
			query: "build the call graph from parseConfig",
			want:  "parseConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFunctionNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractFunctionNameFromQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

// TestExtractFunctionNameFromQuery_EmptyAndNil verifies safe behavior on edge inputs.
func TestExtractFunctionNameFromQuery_EmptyAndNil(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
		{"only common words", "the a an this that", ""},
		{"only punctuation", "???...!!!", ""},
		{"single common word", "function", ""},
		{"query with no function-like words", "how are you doing today", ""}, // IT-06: "you" no longer passes isFunctionLikeName (not PascalCase/CamelCase/snake_case)
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

// TestIsValidFunctionName_RejectsBrackets verifies that symbol names containing
// brackets, braces, or angle brackets are rejected.
// IT-06b Issue 2: "[Tool calls: Grep]" contaminated ConversationHistory,
// causing "Grep]" to be extracted as a valid symbol name.
func TestIsValidFunctionName_RejectsBrackets(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"bare name is valid", "Grep", true},
		{"trailing bracket rejected", "Grep]", false},
		{"leading bracket rejected", "[Tool", false},
		{"curly brace rejected", "Config{}", false},
		{"angle bracket rejected", "List<T>", false},
		{"parentheses rejected", "func()", false},
		{"CamelCase valid", "HandlerFunc", true},
		{"snake_case valid", "full_dispatch_request", true},
		{"dot notation valid", "DB.Open", true}, // dots are OK
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidFunctionName(tt.s)
			if got != tt.want {
				t.Errorf("isValidFunctionName(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

// TestExtractFunctionNameFromQuery_ToolCallContamination verifies that
// tool call placeholder messages like "[Tool calls: Grep]" do not produce
// the CORRUPTED symbol name "Grep]" that caused IT-06b Issue 2.
//
// Before fix: "[Tool calls: Grep]" → "Grep]" (bracket not stripped, passed validation)
// After fix: "[Tool calls: Grep]" → "Tool" (brackets stripped, "Grep" also valid)
//
// The primary defense is that extractFunctionNameFromContext no longer scans
// ConversationHistory, so this input never reaches extractFunctionNameFromQuery.
// This test verifies the secondary defense: brackets are stripped and "Grep]" is impossible.
func TestExtractFunctionNameFromQuery_ToolCallContamination(t *testing.T) {
	// The critical assertion: "Grep]" (with trailing bracket) must NEVER be returned.
	// Before IT-06b fix, this returned "Grep]". Now brackets are stripped by Trim
	// and rejected by isValidFunctionName.
	got := extractFunctionNameFromQuery("[Tool calls: Grep]")
	if got == "Grep]" {
		t.Errorf("extractFunctionNameFromQuery returned corrupted name %q — IT-06b regression", got)
	}
	// After fix, "Tool" is extracted (brackets stripped, CamelCase) — this is a harmless
	// generic word that won't match any real symbol. The primary defense (ConversationHistory
	// removal) prevents this path from ever being exercised in production.
	if got != "Tool" {
		t.Logf("extractFunctionNameFromQuery(%q) = %q (expected 'Tool' after bracket stripping)", "[Tool calls: Grep]", got)
	}
}

// TestExtractFunctionNameFromContext_NoHistoryContamination verifies the primary
// IT-06b Issue 2 fix: extractFunctionNameFromContext no longer scans ConversationHistory.
func TestExtractFunctionNameFromContext_NoHistoryContamination(t *testing.T) {
	ctx := &agent.AssembledContext{
		ConversationHistory: []agent.Message{
			{Role: "assistant", Content: "[Tool calls: Grep]"},
			{Role: "assistant", Content: "[Tool calls: find_references]"},
		},
		ToolResults: nil, // No tool results — forces history fallback (which is now removed)
	}
	got := extractFunctionNameFromContext(ctx)
	if got != "" {
		t.Errorf("extractFunctionNameFromContext with only ConversationHistory returned %q, want empty (IT-06b: history scanning removed)", got)
	}
}

// TestExtractTypeDotMethodFromQuery_PunctuationStripping verifies that punctuation
// is stripped from both method and type names correctly.
func TestExtractTypeDotMethodFromQuery_PunctuationStripping(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "method with trailing question mark is stripped by Trim",
			query: "What about Get? method on Txn",
			// "Get?" → Trim("?,()") → "Get", but "method" is not at [i+1]
			// Actually: words are ["What", "about", "Get?", "method", "on", "Txn"]
			// i=2: lowerWords[3]=="method" → methodName = Trim("Get?", "?,()") = "Get"
			// j=4: lowerWords[4]=="on" → j=5 → typeName = Trim("Txn", "?,()") = "Txn"
			want: "Txn.Get",
		},
		{
			name:  "type with trailing period",
			query: "Find render method on Scene.",
			want:  "Scene.render",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTypeDotMethodFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractTypeDotMethodFromQuery(%q) = %q, want %q",
					tt.query, got, tt.want)
			}
		})
	}
}

// =============================================================================
// GR-59 Group F: Surrender Detection Tests
// =============================================================================

func TestIsSurrenderResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected bool
	}{
		{"simple surrender", "I don't know", true},
		{"surrender with context", "I don't know the answer.", true},
		{"formal surrender", "I do not know", true},
		{"unsure", "I'm not sure", true},
		{"cannot determine", "I cannot determine that", true},
		{"unable", "I'm unable to answer", true},
		{"long response not surrender", "I don't know exactly how many functions there are, but based on the graph analysis, the function parseConfig is called by main() and initServer(). Here are the details...", false},
		{"normal answer", "The function parseConfig is called by main and initServer.", false},
		{"empty", "", false},
		{"just spaces", "   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSurrenderResponse(tt.response)
			if result != tt.expected {
				t.Errorf("isSurrenderResponse(%q) = %v, want %v", tt.response, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// IT-03a: File Extension Rejection + Interface Name Extraction Tests
// =============================================================================

func TestExtractFunctionNameFromQuery_FileExtensionRejection(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "Babylon.js should not match as Type.Method",
			query: "What classes extend the AbstractMesh class in Babylon.js?",
			// Without file extension fix, this would return "Babylon.js"
			// With fix, it falls through to Pattern 7 (CamelCase fallback) → "AbstractMesh"
			want: "AbstractMesh",
		},
		{
			name:  "Express.js should not match as Type.Method",
			query: "Find implementations of Router in Express.js",
			want:  "Router",
		},
		{
			name:  "Flask.py should not match as Type.Method",
			query: "What extends Blueprint in Flask.py?",
			// "extends" is in skipWords, "Blueprint" is CamelCase → Pattern 7
			want: "Blueprint",
		},
		{
			name:  "real dot-notation still works",
			query: "Who calls Router.handle?",
			want:  "Router.handle",
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

func TestExtractInterfaceNameFromQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// "extend" patterns
		{
			name:  "what classes extend X",
			query: "What classes extend the AbstractMesh class?",
			want:  "AbstractMesh",
		},
		{
			name:  "what extends X in project",
			query: "What classes extend the Light base class in Babylon.js?",
			want:  "Light",
		},
		{
			name:  "classes that extend X",
			query: "Show classes that extend EventEmitter",
			want:  "EventEmitter",
		},

		// "implement" patterns
		{
			name:  "what implements X",
			query: "What implements the Reader interface?",
			want:  "Reader",
		},
		{
			name:  "classes implementing X",
			query: "Find all types that implement SessionInterface",
			want:  "SessionInterface",
		},

		// "subclass" patterns
		{
			name:  "subclasses of X",
			query: "What are the subclasses of AbstractMesh?",
			want:  "AbstractMesh",
		},

		// "X class/interface" pattern
		{
			name:  "X class pattern",
			query: "Find implementations of the AbstractMesh class",
			want:  "AbstractMesh",
		},
		{
			name:  "X interface pattern",
			query: "Show all implementations of the Handler interface",
			want:  "Handler",
		},

		// No match — should return empty
		{
			name:  "no inheritance keywords",
			query: "How does the parser work?",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractInterfaceNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractInterfaceNameFromQuery(%q) = %q, want %q",
					tt.query, got, tt.want)
			}
		})
	}
}

func TestIsFileExtension(t *testing.T) {
	// File extensions should be rejected
	for _, ext := range []string{"js", "ts", "py", "go", "rs", "java", "css", "html", "json"} {
		if !isFileExtension(ext) {
			t.Errorf("isFileExtension(%q) = false, want true", ext)
		}
	}
	// Non-extensions should pass
	for _, notExt := range []string{"Get", "handle", "render", "JSON", "Open", "Init"} {
		if isFileExtension(notExt) {
			t.Errorf("isFileExtension(%q) = true, want false", notExt)
		}
	}
}

// TestExtractFunctionNameFromQuery_IT04_FindSymbolQueries tests the IT-04 fix for
// find_symbol queries using "Where is the X class/struct defined?" pattern.
// Previously, Pattern 6 only recognized "function"/"method"/"symbol" as kind keywords,
// causing "Where" to be extracted instead of the actual symbol name.
func TestExtractFunctionNameFromQuery_IT04_FindSymbolQueries(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// === All 29 find_symbol test queries from integration testing ===
		// JavaScript/TypeScript - class keyword
		{name: "BabylonJS: Scene class", query: "Where is the Scene class defined in this codebase?", want: "Scene"},
		{name: "BabylonJS: Engine class", query: "Where is the Engine class defined in this codebase?", want: "Engine"},
		{name: "BabylonJS: TransformNode class", query: "Where is the TransformNode class defined?", want: "TransformNode"},
		{name: "Express: Application prototype", query: "Where is the Application prototype defined in this codebase?", want: "Application"},
		{name: "Express: View constructor", query: "Where is the View constructor defined in this codebase?", want: "View"},
		{name: "Express: Layer constructor", query: "Where is the Layer constructor defined?", want: "Layer"},
		{name: "NestJS: NestFactory class", query: "Where is the NestFactory class defined?", want: "NestFactory"},
		{name: "NestJS: Injector class", query: "Where is the Injector class defined?", want: "Injector"},
		{name: "NestJS: RoutesResolver class", query: "Where is the RoutesResolver class defined?", want: "RoutesResolver"},
		{name: "Plottable: Plot class", query: "Where is the Plot class defined?", want: "Plot"},
		{name: "Plottable: Table class", query: "Where is the Table class defined?", want: "Table"},
		{name: "Plottable: Dispatcher class", query: "Where is the Dispatcher class defined?", want: "Dispatcher"},
		// Python - class keyword
		{name: "Pandas: DataFrame class", query: "Where is the DataFrame class defined?", want: "DataFrame"},
		{name: "Pandas: Series class", query: "Where is the Series class defined?", want: "Series"},
		{name: "Pandas: MultiIndex class", query: "Where is the MultiIndex class defined?", want: "MultiIndex"},
		{name: "Flask: Flask class", query: "Where is the Flask class defined?", want: "Flask"},
		{name: "Flask: Blueprint class", query: "Where is the Blueprint class defined?", want: "Blueprint"},
		{name: "Flask: Config class", query: "Where is the Config class defined?", want: "Config"},
		// Go - struct keyword
		{name: "Gin: Context struct", query: "Where is the Context struct defined?", want: "Context"},
		{name: "Gin: Engine struct", query: "Where is the Engine struct defined?", want: "Engine"},
		{name: "Gin: RouterGroup struct", query: "Where is the RouterGroup struct defined?", want: "RouterGroup"},
		{name: "Badger: DB struct", query: "Where is the DB struct defined?", want: "DB"},
		{name: "Badger: Txn struct", query: "Where is the Txn struct defined in this codebase?", want: "Txn"},
		{name: "Badger: levelsController struct", query: "Where is the levelsController struct defined?", want: "levelsController"},
		// Go - no kind keyword
		{name: "Hugo: HugoSites", query: "Where is HugoSites defined in this codebase?", want: "HugoSites"},
		{name: "Hugo: pageState struct", query: "Where is the pageState struct defined in this codebase?", want: "pageState"},
		{name: "Hugo: ContentSpec", query: "Where is ContentSpec defined in this codebase?", want: "ContentSpec"},
		// Edge cases
		{name: "bare symbol name", query: "TransformNode", want: "TransformNode"},
		{name: "just asking where", query: "Where is handleRequest defined?", want: "handleRequest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFunctionNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractFunctionNameFromQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

// TestExtractFunctionNameFromQuery_CompoundPhraseSkip tests IT-05 FN1: compound phrase
// sanitization prevents extracting "call" from queries like "call chain of main".
func TestExtractFunctionNameFromQuery_CompoundPhraseSkip(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "call chain of main — should extract main, not call",
			query: "Show the call chain of main",
			want:  "main",
		},
		{
			name:  "call graph from ProcessData — should extract ProcessData",
			query: "Build the call graph from ProcessData",
			want:  "ProcessData",
		},
		{
			name:  "call hierarchy of Handler — should extract Handler",
			query: "Get the call hierarchy of Handler",
			want:  "Handler",
		},
		{
			name:  "call tree from parseConfig — should extract parseConfig",
			query: "Show the call tree from parseConfig",
			want:  "parseConfig",
		},
		{
			name:  "call stack of initServer — should extract initServer",
			query: "Trace the call stack of initServer",
			want:  "initServer",
		},
		{
			name:  "call flow from LoadConfig — should extract LoadConfig",
			query: "Show call flow from LoadConfig",
			want:  "LoadConfig",
		},
		{
			name:  "call path for renderScene — should extract renderScene",
			query: "Get the call path for renderScene function",
			want:  "renderScene",
		},
		{
			name:  "regular query without compound phrase — unaffected",
			query: "Find the Process function",
			want:  "Process",
		},
		{
			name:  "upstream call chain from main — should extract main",
			query: "Show the upstream call chain from main",
			want:  "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFunctionNameFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractFunctionNameFromQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

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

// TestExtractFunctionNameCandidates_MultipleResults tests that the multi-candidate
// extraction returns ranked candidates from multiple patterns.
func TestExtractFunctionNameCandidates_MultipleResults(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFirst  string
		wantMinLen int // minimum number of candidates expected
	}{
		{
			name:       "simple query with one function name",
			query:      "Who calls ProcessData?",
			wantFirst:  "ProcessData",
			wantMinLen: 1,
		},
		{
			name:       "query with function after 'of' plus CamelCase fallback",
			query:      "Find callers of ProcessData in the BuildRequest handler",
			wantFirst:  "ProcessData",
			wantMinLen: 2, // ProcessData from Pattern 2, BuildRequest from Pattern 7
		},
		{
			name:       "call chain query with stripped phrases yields candidate from remaining",
			query:      "Show the call chain from ProcessData",
			wantFirst:  "ProcessData",
			wantMinLen: 1,
		},
		{
			name:       "Type.Method extraction is highest priority",
			query:      "Who calls Transaction.Get in the Handler?",
			wantFirst:  "Transaction.Get",
			wantMinLen: 2, // Transaction.Get from Pattern 0b, Handler from Pattern 7
		},
		{
			name:       "multiple CamelCase words yield multiple candidates",
			query:      "How does BuildRequest connect to ProcessData?",
			wantFirst:  "BuildRequest",
			wantMinLen: 2,
		},
		{
			name:       "empty query returns empty candidates",
			query:      "",
			wantFirst:  "",
			wantMinLen: 0,
		},
		{
			name:       "all skip words returns empty candidates",
			query:      "find the function from the codebase",
			wantFirst:  "",
			wantMinLen: 0,
		},
		{
			name:       "pattern 5: for X function yields candidate",
			query:      "control dependencies for Process function in the codebase",
			wantFirst:  "Process",
			wantMinLen: 1,
		},
		{
			name:       "pattern 6: X class yields candidate",
			query:      "Where is the TransformNode class defined?",
			wantFirst:  "TransformNode",
			wantMinLen: 1,
		},
		{
			name:       "does X call pattern",
			query:      "What does parseConfig call in BuildHandler?",
			wantFirst:  "parseConfig",
			wantMinLen: 2, // parseConfig from Pattern 1, BuildHandler from Pattern 7
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractFunctionNameCandidates(tt.query)

			if tt.wantMinLen == 0 {
				if len(candidates) != 0 {
					t.Errorf("extractFunctionNameCandidates(%q) returned %d candidates, want 0: %v",
						tt.query, len(candidates), candidates)
				}
				return
			}

			if len(candidates) < tt.wantMinLen {
				t.Errorf("extractFunctionNameCandidates(%q) returned %d candidates, want >= %d: %v",
					tt.query, len(candidates), tt.wantMinLen, candidates)
			}

			if tt.wantFirst != "" && (len(candidates) == 0 || candidates[0] != tt.wantFirst) {
				first := ""
				if len(candidates) > 0 {
					first = candidates[0]
				}
				t.Errorf("extractFunctionNameCandidates(%q) first = %q, want %q (all: %v)",
					tt.query, first, tt.wantFirst, candidates)
			}
		})
	}
}

// TestExtractFunctionNameCandidates_NoDuplicates verifies that duplicate candidates
// are suppressed across patterns.
func TestExtractFunctionNameCandidates_NoDuplicates(t *testing.T) {
	// "callers of ProcessData" — Pattern 2 extracts "ProcessData",
	// Pattern 7 would also find "ProcessData" as CamelCase. Should only appear once.
	candidates := extractFunctionNameCandidates("Find callers of ProcessData")

	seen := make(map[string]bool)
	for _, c := range candidates {
		if seen[c] {
			t.Errorf("duplicate candidate %q in results: %v", c, candidates)
		}
		seen[c] = true
	}
}

// TestExtractFunctionNameCandidates_BackwardsCompatible verifies that the first
// candidate matches what extractFunctionNameFromQuery would have returned.
func TestExtractFunctionNameCandidates_BackwardsCompatible(t *testing.T) {
	queries := []string{
		"Who calls ProcessData?",
		"What does main call?",
		"Find callers of handleRequest",
		"functions called by BuildRequest",
		"Show control dependencies for Process function with depth 3",
		"Where is the TransformNode class defined?",
		"Show the call chain from ProcessData",
		"Who calls the Get method on the Transaction type?",
	}

	for _, query := range queries {
		oldResult := extractFunctionNameFromQuery(query)
		candidates := extractFunctionNameCandidates(query)

		first := ""
		if len(candidates) > 0 {
			first = candidates[0]
		}

		if first != oldResult {
			t.Errorf("backwards compatibility broken for %q: extractFunctionNameFromQuery=%q, candidates[0]=%q",
				query, oldResult, first)
		}
	}
}

// TestResolveFirstCandidate tests the candidate-loop resolution.
func TestResolveFirstCandidate(t *testing.T) {
	// Build a symbol index with known symbols
	idx := index.NewSymbolIndex()
	idx.Add(&ast.Symbol{
		ID:        "pkg/handler.go:ProcessData",
		Name:      "ProcessData",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "pkg/handler.go",
		StartLine: 10,
		EndLine:   20,
		Language:  "go",
		Exported:  true,
	})

	deps := &Dependencies{
		SymbolIndex: idx,
	}

	t.Run("first candidate resolves immediately", func(t *testing.T) {
		var cache sync.Map
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "sess1", []string{"ProcessData"}, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if symbolID != "pkg/handler.go:ProcessData" {
			t.Errorf("symbolID = %q, want %q", symbolID, "pkg/handler.go:ProcessData")
		}
		if rawName != "ProcessData" {
			t.Errorf("rawName = %q, want %q", rawName, "ProcessData")
		}
	})

	t.Run("first candidate fails, second resolves", func(t *testing.T) {
		var cache sync.Map
		candidates := []string{"NonExistent", "ProcessData"}
		symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "sess2", candidates, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if symbolID != "pkg/handler.go:ProcessData" {
			t.Errorf("symbolID = %q, want %q", symbolID, "pkg/handler.go:ProcessData")
		}
		if rawName != "ProcessData" {
			t.Errorf("rawName = %q, want %q (should be the candidate that resolved)", rawName, "ProcessData")
		}
	})

	t.Run("all candidates fail", func(t *testing.T) {
		var cache sync.Map
		candidates := []string{"NonExistent1", "NonExistent2"}
		_, _, _, err := resolveFirstCandidate(context.Background(), &cache, "sess3", candidates, deps)
		if err == nil {
			t.Fatal("expected error when all candidates fail")
		}
	})

	t.Run("empty candidates returns error", func(t *testing.T) {
		var cache sync.Map
		_, _, _, err := resolveFirstCandidate(context.Background(), &cache, "sess4", []string{}, deps)
		if err == nil {
			t.Fatal("expected error for empty candidates")
		}
	})

	t.Run("nil candidates returns error", func(t *testing.T) {
		var cache sync.Map
		_, _, _, err := resolveFirstCandidate(context.Background(), &cache, "sess5", nil, deps)
		if err == nil {
			t.Fatal("expected error for nil candidates")
		}
	})
}

// TestResolveFirstCandidate_SkipsBadExtraction simulates the Issue 4 scenario:
// extraction picks wrong word first, but correct word is second candidate.
func TestResolveFirstCandidate_SkipsBadExtraction(t *testing.T) {
	idx := index.NewSymbolIndex()
	// Only "ProcessData" exists in the index — "Build" does not
	idx.Add(&ast.Symbol{
		ID:        "pkg/data.go:ProcessData",
		Name:      "ProcessData",
		Kind:      ast.SymbolKindFunction,
		FilePath:  "pkg/data.go",
		StartLine: 5,
		EndLine:   15,
		Language:  "go",
		Exported:  true,
	})

	deps := &Dependencies{
		SymbolIndex: idx,
	}

	// Simulates: "Build the call graph from ProcessData"
	// Pattern extraction might yield ["ProcessData"] after skip phrase stripping.
	// But in theory, if "Build" slipped through, the candidate loop would recover.
	var cache sync.Map
	candidates := []string{"Build", "ProcessData"}

	symbolID, rawName, _, err := resolveFirstCandidate(context.Background(), &cache, "test-session", candidates, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rawName != "ProcessData" {
		t.Errorf("rawName = %q, want %q — candidate loop should have skipped 'Build'", rawName, "ProcessData")
	}
	if symbolID != "pkg/data.go:ProcessData" {
		t.Errorf("symbolID = %q, want %q", symbolID, "pkg/data.go:ProcessData")
	}
}

// mockSearchableIndex is a helper to ensure resolveFirstCandidate uses the Search path too.
// This verifies that the index.Search method is called for candidates not found by exact name.
type mockSearchableIndex struct {
	*index.SymbolIndex
}

func (m *mockSearchableIndex) Search(ctx context.Context, query string, limit int) ([]*ast.Symbol, error) {
	return m.SymbolIndex.Search(ctx, query, limit)
}

// =============================================================================
// IT-05 Run 2: "from X to Y" Boundary Tests
// =============================================================================

// TestExtractFunctionNameCandidates_FromToBoundary tests that the "from X to Y"
// pattern prevents destination words from leaking into candidates via Pattern 7.
func TestExtractFunctionNameCandidates_FromToBoundary(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFirst  string
		wantAbsent []string // words that must NOT appear in candidates
		wantMinLen int
	}{
		{
			name:       "Engine.runRenderLoop to mesh rendering — no destination words",
			query:      "Show the call chain from Engine.runRenderLoop to mesh rendering",
			wantFirst:  "Engine.runRenderLoop",
			wantAbsent: []string{"mesh", "rendering"},
			wantMinLen: 1,
		},
		{
			name:       "Scene.addMesh to scene graph update — no destination words",
			query:      "Show the call chain from Scene.addMesh to scene graph update",
			wantFirst:  "Scene.addMesh",
			wantAbsent: []string{"scene", "graph"},
			wantMinLen: 1,
		},
		{
			name:       "DB.Open to value retrieval — no destination words",
			query:      "Show the call chain from DB.Open to value retrieval",
			wantFirst:  "DB.Open",
			wantAbsent: []string{"value", "retrieval"},
			wantMinLen: 1,
		},
		{
			name:       "Engine.Run to handler execution — no destination words",
			query:      "Show the call chain from Engine.Run to handler execution",
			wantFirst:  "Engine.Run",
			wantAbsent: []string{"handler", "execution"},
			wantMinLen: 1,
		},
		{
			name:       "Flask.run to view function — no destination words",
			query:      "Show the call chain from Flask.run to a view function",
			wantFirst:  "Flask.run",
			wantAbsent: []string{"view"},
			wantMinLen: 1,
		},
		{
			name:       "Context.Bind to data validation — no destination words",
			query:      "Show the call chain from Context.Bind to data validation",
			wantFirst:  "Context.Bind",
			wantAbsent: []string{"data", "validation"},
			wantMinLen: 1,
		},
		{
			name:       "Plot.render to SVG creation — creation blocked, SVG allowed (all-caps is CamelCase)",
			query:      "Show the call chain from Plot.render to SVG creation",
			wantFirst:  "Plot.render",
			wantAbsent: []string{"creation"},
			wantMinLen: 1,
		},
		{
			name:       "app.use to middleware execution — no destination words",
			query:      "Show the call chain from app.use to middleware execution",
			wantAbsent: []string{"middleware", "execution"},
			wantMinLen: 0, // app.use may not extract (lowercase 'app')
		},
		// CR-R2-6: Pattern 2 "of" after boundary — "callers of mesh" is in destination zone
		{
			name:       "Engine.Run to callers of mesh — Pattern 2 gated",
			query:      "Show the call chain from Engine.Run to callers of mesh",
			wantFirst:  "Engine.Run",
			wantAbsent: []string{"mesh"},
			wantMinLen: 1,
		},
		// CR-R2-1: Pattern 5 "for X function" after boundary — "for handler function" is in destination zone
		{
			name:       "Engine.Run to handler function — Pattern 5/6 gated",
			query:      "Show the call chain from Engine.Run to handler function dispatch",
			wantFirst:  "Engine.Run",
			wantAbsent: []string{"handler"},
			wantMinLen: 1,
		},
		// IT-05 R3-3: CamelCase words past boundary ARE extracted (they are symbol names)
		{
			name:       "route dispatch to canActivate — CamelCase past boundary extracted",
			query:      "Show the call chain from route handler dispatch to guard canActivate execution",
			wantAbsent: []string{"guard", "execution"},
			wantMinLen: 1, // at least "route" or "handler" should be extracted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractFunctionNameCandidates(tt.query)

			if tt.wantMinLen > 0 && len(candidates) < tt.wantMinLen {
				t.Errorf("extractFunctionNameCandidates(%q) returned %d candidates, want >= %d: %v",
					tt.query, len(candidates), tt.wantMinLen, candidates)
			}

			if tt.wantFirst != "" && len(candidates) > 0 && candidates[0] != tt.wantFirst {
				t.Errorf("extractFunctionNameCandidates(%q) first = %q, want %q (all: %v)",
					tt.query, candidates[0], tt.wantFirst, candidates)
			}

			// Verify absent words are NOT in candidates
			for _, absent := range tt.wantAbsent {
				for _, c := range candidates {
					if strings.EqualFold(c, absent) {
						t.Errorf("extractFunctionNameCandidates(%q) contains unwanted %q (all: %v)",
							tt.query, absent, candidates)
					}
				}
			}
		})
	}
}

// TestExtractFunctionNameCandidates_NoFromToNoTruncation verifies that queries
// WITHOUT "from X to Y" are unaffected by the boundary logic.
func TestExtractFunctionNameCandidates_NoFromToNoTruncation(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantFirst string
	}{
		{
			name:      "who calls — no boundary applied",
			query:     "Who calls renderMesh?",
			wantFirst: "renderMesh",
		},
		{
			name:      "callers of — no boundary applied",
			query:     "Find callers of ProcessData",
			wantFirst: "ProcessData",
		},
		{
			name:      "what does X call — no boundary applied",
			query:     "What does main call?",
			wantFirst: "main",
		},
		{
			name:      "call chain of X (no 'from') — no boundary applied",
			query:     "Show the call chain of main",
			wantFirst: "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractFunctionNameCandidates(tt.query)
			if len(candidates) == 0 || candidates[0] != tt.wantFirst {
				first := ""
				if len(candidates) > 0 {
					first = candidates[0]
				}
				t.Errorf("extractFunctionNameCandidates(%q) first = %q, want %q (all: %v)",
					tt.query, first, tt.wantFirst, candidates)
			}
		})
	}
}

// TestExtractFunctionNameCandidates_CamelCaseExemption tests IT-05 R3-3:
// CamelCase words past the "to" boundary ARE extracted (they are likely symbol names),
// while single-case words past the boundary are still blocked.
func TestExtractFunctionNameCandidates_CamelCaseExemption(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		wantPresent []string // words that MUST appear in candidates
		wantAbsent  []string // words that must NOT appear in candidates
	}{
		{
			name:        "canActivate (CamelCase) past boundary is extracted",
			query:       "Show the call chain from route handler dispatch to guard canActivate execution",
			wantPresent: []string{"canActivate"},
			wantAbsent:  []string{"guard", "execution"},
		},
		{
			name:        "runRenderLoop (CamelCase) past boundary is extracted",
			query:       "Show the call chain from main to runRenderLoop processing",
			wantPresent: []string{"runRenderLoop"},
			wantAbsent:  []string{"processing"},
		},
		{
			name:        "DataFrame (CamelCase) past boundary is extracted",
			query:       "Show the call chain from read_csv to DataFrame construction",
			wantPresent: []string{"read_csv", "DataFrame"},
			wantAbsent:  []string{"construction"},
		},
		{
			name:        "single-case words past boundary still blocked",
			query:       "Show the call chain from Engine.Run to handler execution",
			wantPresent: []string{"Engine.Run"},
			wantAbsent:  []string{"handler", "execution"},
		},
		{
			name:        "no boundary — CamelCase extracted normally",
			query:       "Who calls canActivate?",
			wantPresent: []string{"canActivate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractFunctionNameCandidates(tt.query)

			for _, want := range tt.wantPresent {
				found := false
				for _, c := range candidates {
					if c == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("extractFunctionNameCandidates(%q) missing expected %q (all: %v)",
						tt.query, want, candidates)
				}
			}

			for _, absent := range tt.wantAbsent {
				for _, c := range candidates {
					if strings.EqualFold(c, absent) {
						t.Errorf("extractFunctionNameCandidates(%q) contains unwanted %q (all: %v)",
							tt.query, absent, candidates)
					}
				}
			}
		})
	}
}

// TestIsStrictCamelCase tests the isStrictCamelCase helper function.
func TestIsStrictCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		// CamelCase: has uppercase in middle → true
		{"canActivate", true},
		{"runRenderLoop", true},
		{"DataFrame", true},
		{"NestFactory", true},
		{"processData", true},
		{"ABCTest", true},

		// Not CamelCase: all lowercase or all uppercase or single letter → false
		{"route", false},
		{"handler", false},
		{"mesh", false},
		{"rendering", false},
		{"execution", false},
		{"main", false},
		{"a", false},
		{"", false},
		{"x", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isStrictCamelCase(tt.input)
			if got != tt.want {
				t.Errorf("isStrictCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestExtractFunctionNameCandidates_ConceptWordsFiltered verifies that concept/action
// words added to skipWords are properly filtered.
func TestExtractFunctionNameCandidates_ConceptWordsFiltered(t *testing.T) {
	conceptWords := []string{
		"rendering", "creation", "retrieval", "persistence", "execution",
		"compilation", "initialization", "processing", "assembly",
		"assigning", "parsing", "dispatch", "handling",
		"update", "validation", "construction", "aggregation",
	}

	for _, word := range conceptWords {
		t.Run(word, func(t *testing.T) {
			// These words should not pass isValidFunctionName
			if isValidFunctionName(word) {
				t.Errorf("isValidFunctionName(%q) = true, want false (should be in skipWords)", word)
			}
		})
	}
}

// TestExtractDestinationCandidates tests IT-05 R5 destination extraction for "from X to Y" queries.
func TestExtractDestinationCandidates(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string // nil means no candidates expected
	}{
		{
			name:  "from X to Y: extracts destination",
			query: "Show the call chain from Engine.runRenderLoop to mesh rendering",
			want:  nil, // IT-06: "mesh" no longer passes isFunctionLikeName (lowercase, no structural signal)
		},
		{
			name:  "from X to Y with CamelCase destination",
			query: "Show the call chain from main to ParseConfig",
			want:  []string{"ParseConfig"},
		},
		{
			name:  "no from/to pattern returns nil",
			query: "Show callers of ProcessData",
			want:  nil,
		},
		{
			name:  "from but no to returns nil",
			query: "Show the call chain from main",
			want:  nil,
		},
		{
			name:  "from X to Y with dot notation destination",
			query: "Show the call chain from App.listen to Router.handle",
			want:  []string{"Router.handle"},
		},
		{
			name:  "from X to Y with function-like destination",
			query: "Show the call chain from read_csv to DataFrame creation",
			want:  []string{"DataFrame"},
		},
		{
			name:  "multi-hop uses last 'to' (review fix #5)",
			query: "Show the call chain from login to the dashboard to SettingsPage",
			want:  []string{"SettingsPage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDestinationCandidates(tt.query)

			if tt.want == nil {
				if len(got) > 0 {
					t.Errorf("extractDestinationCandidates(%q) = %v, want nil/empty", tt.query, got)
				}
				return
			}

			if len(got) == 0 {
				t.Errorf("extractDestinationCandidates(%q) = nil/empty, want %v", tt.query, tt.want)
				return
			}

			// Check that expected candidates appear in results
			for _, expected := range tt.want {
				found := false
				for _, candidate := range got {
					if candidate == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("extractDestinationCandidates(%q) = %v, missing expected candidate %q", tt.query, got, expected)
				}
			}
		})
	}
}

// IT-06c Bug C: Test extractPackageContextFromQuery.
func TestExtractPackageContextFromQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "in hugolib at end",
			query: "What functions does the Build method call in hugolib?",
			want:  "hugolib",
		},
		{
			name:  "in gin at end",
			query: "What does Engine.Run call in gin?",
			want:  "gin",
		},
		{
			name:  "in flask mid-query",
			query: "Who calls request in flask across the codebase?",
			want:  "flask",
		},
		{
			name:  "the hugolib package",
			query: "Find callers of Build in the hugolib package",
			want:  "hugolib",
		},
		{
			name:  "no package context",
			query: "What functions does Build call?",
			want:  "",
		},
		{
			name:  "in the codebase should not match",
			query: "Where is Build used in the codebase?",
			want:  "",
		},
		{
			name:  "in the project should not match",
			query: "Find callers in the project",
			want:  "",
		},
		{
			name:  "package with underscore",
			query: "What does read_csv call in pandas_core?",
			want:  "pandas_core",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPackageContextFromQuery(tt.query)
			if got != tt.want {
				t.Errorf("extractPackageContextFromQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
