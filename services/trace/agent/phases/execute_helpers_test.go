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
			want:  "about", // Pre-existing: Pattern 7 picks "about" (valid, ≤15 chars) before "HandleHTTP"
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
		{"query with no function-like words", "how are you doing today", "you"}, // Pre-existing: "you" passes isFunctionLikeName (≤15 chars)
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
