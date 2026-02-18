// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package ast provides types and interfaces for language-agnostic AST parsing.
//
// This package defines the core data structures used throughout the Code Buddy
// service for representing parsed code symbols, their locations, and relationships.
// All parser implementations (Go, Python, TypeScript, etc.) produce output
// conforming to these types.
//
// Design principles:
//   - Language-agnostic: Types work for any supported language
//   - Timestamps as int64 UnixMilli per project conventions
//   - No map[string]interface{} - concrete types only
//   - Comprehensive documentation on all exported types
package ast

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SymbolKind represents the type of code symbol extracted from source code.
//
// Each kind maps to a common programming construct that exists across
// multiple languages. Language-specific constructs are mapped to the
// closest general kind (e.g., Python's class maps to Struct).
type SymbolKind int

const (
	// SymbolKindUnknown indicates an unrecognized or unparseable symbol.
	SymbolKindUnknown SymbolKind = iota

	// SymbolKindPackage represents a package or module declaration.
	// Examples: Go package, Python module, TypeScript namespace.
	SymbolKindPackage

	// SymbolKindFile represents a source file as a symbol.
	// Used for file-level relationships like imports.
	SymbolKindFile

	// SymbolKindFunction represents a standalone function declaration.
	// Examples: Go func, Python def, TypeScript function.
	SymbolKindFunction

	// SymbolKindMethod represents a function attached to a type/class.
	// Examples: Go method with receiver, Python class method, TypeScript class method.
	SymbolKindMethod

	// SymbolKindInterface represents an interface or protocol definition.
	// Examples: Go interface, Python Protocol (typing), TypeScript interface.
	SymbolKindInterface

	// SymbolKindStruct represents a composite data type.
	// Examples: Go struct, Python class, TypeScript class.
	SymbolKindStruct

	// SymbolKindType represents a type alias or definition.
	// Examples: Go type alias, TypeScript type alias.
	SymbolKindType

	// SymbolKindVariable represents a variable declaration.
	// Examples: Go var, Python variable, TypeScript let/const.
	SymbolKindVariable

	// SymbolKindConstant represents a constant declaration.
	// Examples: Go const, Python CONSTANT (by convention), TypeScript const.
	SymbolKindConstant

	// SymbolKindField represents a field within a struct/class.
	// Examples: Go struct field, Python class attribute, TypeScript class property.
	SymbolKindField

	// SymbolKindImport represents an import statement.
	// Examples: Go import, Python import/from, TypeScript import.
	SymbolKindImport

	// SymbolKindClass represents a class definition (alias for Struct in OOP languages).
	// Used primarily for Python and TypeScript where "class" is the idiomatic term.
	SymbolKindClass

	// SymbolKindDecorator represents a decorator or annotation.
	// Examples: Python @decorator, TypeScript @decorator.
	SymbolKindDecorator

	// SymbolKindEnum represents an enumeration type.
	// Examples: Go const block with iota, TypeScript enum.
	SymbolKindEnum

	// SymbolKindEnumMember represents a member of an enumeration.
	SymbolKindEnumMember

	// SymbolKindParameter represents a function/method parameter.
	SymbolKindParameter

	// SymbolKindProperty represents a property (getter/setter).
	// Examples: Python @property, TypeScript get/set accessors.
	SymbolKindProperty

	// SymbolKindCSSClass represents a CSS class selector.
	SymbolKindCSSClass

	// SymbolKindCSSID represents a CSS ID selector.
	SymbolKindCSSID

	// SymbolKindCSSVariable represents a CSS custom property (variable).
	SymbolKindCSSVariable

	// SymbolKindAnimation represents a CSS animation or keyframes.
	SymbolKindAnimation

	// SymbolKindMediaQuery represents a CSS media query.
	SymbolKindMediaQuery

	// SymbolKindComponent represents a UI component (HTML custom element, React component).
	SymbolKindComponent

	// SymbolKindElement represents an HTML element with an ID.
	SymbolKindElement

	// SymbolKindForm represents an HTML form.
	SymbolKindForm

	// === SQL Symbols ===

	// SymbolKindTable represents a SQL table definition.
	SymbolKindTable

	// SymbolKindColumn represents a SQL column definition.
	SymbolKindColumn

	// SymbolKindView represents a SQL view definition.
	SymbolKindView

	// SymbolKindIndex represents a SQL index definition.
	SymbolKindIndex

	// SymbolKindTrigger represents a SQL trigger definition.
	SymbolKindTrigger

	// SymbolKindProcedure represents a SQL stored procedure.
	SymbolKindProcedure

	// SymbolKindSchema represents a SQL schema/database.
	SymbolKindSchema

	// SymbolKindConstraint represents a SQL constraint (PRIMARY KEY, FOREIGN KEY, etc).
	SymbolKindConstraint

	// === YAML Symbols ===

	// SymbolKindKey represents a YAML key in a mapping.
	SymbolKindKey

	// SymbolKindAnchor represents a YAML anchor (&name).
	SymbolKindAnchor

	// SymbolKindDocument represents a YAML document (separated by ---).
	SymbolKindDocument

	// === Bash/Shell Symbols ===

	// SymbolKindAlias represents a shell alias definition.
	SymbolKindAlias

	// SymbolKindScript represents an executable script.
	SymbolKindScript

	// === Dockerfile Symbols ===

	// SymbolKindStage represents a Docker build stage (FROM ... AS name).
	SymbolKindStage

	// SymbolKindInstruction represents a Dockerfile instruction (FROM, RUN, COPY, etc).
	SymbolKindInstruction

	// SymbolKindPort represents an exposed port (EXPOSE).
	SymbolKindPort

	// SymbolKindVolume represents a Docker volume (VOLUME).
	SymbolKindVolume

	// SymbolKindLabel represents a Docker label (LABEL).
	SymbolKindLabel

	// SymbolKindEnvVar represents an environment variable (ENV).
	SymbolKindEnvVar

	// SymbolKindArg represents a build argument (ARG).
	SymbolKindArg

	// === Markdown Symbols ===

	// SymbolKindHeading represents a markdown heading (# H1, ## H2, etc).
	SymbolKindHeading

	// SymbolKindCodeBlock represents a fenced code block (```language).
	SymbolKindCodeBlock

	// SymbolKindLink represents a markdown link [text](url).
	SymbolKindLink

	// SymbolKindList represents a markdown list (ordered or unordered).
	SymbolKindList

	// SymbolKindBlockquote represents a markdown blockquote (> text).
	SymbolKindBlockquote

	// SymbolKindImage represents a markdown image ![alt](url).
	SymbolKindImage

	// === Graph Building ===

	// SymbolKindExternal represents an external/unresolved symbol.
	// Used for placeholder nodes representing symbols from external packages
	// or unresolved references during graph building.
	SymbolKindExternal
)

// symbolKindNames maps SymbolKind values to their string representations.
var symbolKindNames = map[SymbolKind]string{
	SymbolKindUnknown:     "unknown",
	SymbolKindPackage:     "package",
	SymbolKindFile:        "file",
	SymbolKindFunction:    "function",
	SymbolKindMethod:      "method",
	SymbolKindInterface:   "interface",
	SymbolKindStruct:      "struct",
	SymbolKindType:        "type",
	SymbolKindVariable:    "variable",
	SymbolKindConstant:    "constant",
	SymbolKindField:       "field",
	SymbolKindImport:      "import",
	SymbolKindClass:       "class",
	SymbolKindDecorator:   "decorator",
	SymbolKindEnum:        "enum",
	SymbolKindEnumMember:  "enum_member",
	SymbolKindParameter:   "parameter",
	SymbolKindProperty:    "property",
	SymbolKindCSSClass:    "css_class",
	SymbolKindCSSID:       "css_id",
	SymbolKindCSSVariable: "css_variable",
	SymbolKindAnimation:   "animation",
	SymbolKindMediaQuery:  "media_query",
	SymbolKindComponent:   "component",
	SymbolKindElement:     "element",
	SymbolKindForm:        "form",
	// SQL
	SymbolKindTable:      "table",
	SymbolKindColumn:     "column",
	SymbolKindView:       "view",
	SymbolKindIndex:      "index",
	SymbolKindTrigger:    "trigger",
	SymbolKindProcedure:  "procedure",
	SymbolKindSchema:     "schema",
	SymbolKindConstraint: "constraint",
	// YAML
	SymbolKindKey:      "key",
	SymbolKindAnchor:   "anchor",
	SymbolKindDocument: "document",
	// Bash
	SymbolKindAlias:  "alias",
	SymbolKindScript: "script",
	// Dockerfile
	SymbolKindStage:       "stage",
	SymbolKindInstruction: "instruction",
	SymbolKindPort:        "port",
	SymbolKindVolume:      "volume",
	SymbolKindLabel:       "label",
	SymbolKindEnvVar:      "env_var",
	SymbolKindArg:         "arg",
	// Markdown
	SymbolKindHeading:    "heading",
	SymbolKindCodeBlock:  "code_block",
	SymbolKindLink:       "link",
	SymbolKindList:       "list",
	SymbolKindBlockquote: "blockquote",
	SymbolKindImage:      "image",
	// Graph Building
	SymbolKindExternal: "external",
}

// String returns the string representation of the SymbolKind.
//
// Returns "unknown" for unrecognized values.
func (k SymbolKind) String() string {
	if name, ok := symbolKindNames[k]; ok {
		return name
	}
	return "unknown"
}

// MarshalJSON implements json.Marshaler for SymbolKind.
//
// Serializes the kind as a JSON string (e.g., "function") rather than
// a number for better readability and forward compatibility.
func (k SymbolKind) MarshalJSON() ([]byte, error) {
	return json.Marshal(k.String())
}

// UnmarshalJSON implements json.Unmarshaler for SymbolKind.
//
// Accepts both string values (e.g., "function") and numeric values
// for backward compatibility.
func (k *SymbolKind) UnmarshalJSON(data []byte) error {
	// Try string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*k = ParseSymbolKind(s)
		return nil
	}

	// Fall back to int for backward compatibility
	var i int
	if err := json.Unmarshal(data, &i); err != nil {
		return fmt.Errorf("SymbolKind must be string or int: %w", err)
	}
	*k = SymbolKind(i)
	return nil
}

// ParseSymbolKind converts a string to a SymbolKind.
//
// Returns SymbolKindUnknown if the string is not recognized.
func ParseSymbolKind(s string) SymbolKind {
	for kind, name := range symbolKindNames {
		if name == s {
			return kind
		}
	}
	return SymbolKindUnknown
}

// Location represents a position range within a source file.
//
// Line numbers are 1-indexed (first line is 1).
// Column numbers are 0-indexed (first column is 0).
// This matches the convention used by most editors and LSP.
type Location struct {
	// FilePath is the path to the source file, relative to project root.
	FilePath string `json:"file_path"`

	// StartLine is the 1-indexed line number where the symbol starts.
	StartLine int `json:"start_line"`

	// EndLine is the 1-indexed line number where the symbol ends.
	EndLine int `json:"end_line"`

	// StartCol is the 0-indexed column where the symbol starts on StartLine.
	StartCol int `json:"start_col"`

	// EndCol is the 0-indexed column where the symbol ends on EndLine.
	EndCol int `json:"end_col"`
}

// String returns a human-readable representation of the location.
//
// Format: "file_path:start_line:start_col"
func (l Location) String() string {
	return fmt.Sprintf("%s:%d:%d", l.FilePath, l.StartLine, l.StartCol)
}

// Symbol represents a code symbol extracted from AST parsing.
//
// A symbol is any named code construct: function, type, variable, etc.
// Symbols form the nodes in the code graph, with edges representing
// relationships like "calls", "implements", or "imports".
//
// Note: All timestamps are int64 UnixMilli per project conventions.
type Symbol struct {
	// ID is a unique identifier for this symbol.
	// Format: "file_path:start_line:name"
	// Example: "handlers/agent.go:27:HandleAgent"
	ID string `json:"id"`

	// Name is the symbol's identifier as it appears in source code.
	// Example: "HandleAgent", "UserService", "MAX_RETRIES"
	Name string `json:"name"`

	// Kind indicates what type of symbol this is (function, struct, etc.).
	Kind SymbolKind `json:"kind"`

	// FilePath is the path to the containing file, relative to project root.
	FilePath string `json:"file_path"`

	// StartLine is the 1-indexed line number where the symbol definition starts.
	StartLine int `json:"start_line"`

	// EndLine is the 1-indexed line number where the symbol definition ends.
	EndLine int `json:"end_line"`

	// StartCol is the 0-indexed column where the symbol starts on StartLine.
	StartCol int `json:"start_col"`

	// EndCol is the 0-indexed column where the symbol ends on EndLine.
	EndCol int `json:"end_col"`

	// Signature is the type signature or declaration.
	// Example: "func(ctx context.Context) error" for a Go function.
	Signature string `json:"signature"`

	// DocComment is the documentation comment associated with the symbol.
	// Extracted from preceding comment blocks (GoDoc, docstrings, JSDoc, etc.).
	DocComment string `json:"doc_comment"`

	// Receiver is the receiver type name for methods.
	// Empty for non-method symbols.
	// Example: "UserService" for method "func (s *UserService) Create(...)"
	Receiver string `json:"receiver"`

	// Package is the package or module name containing this symbol.
	// Example: "handlers" for Go, "myapp.services" for Python.
	Package string `json:"package"`

	// Exported indicates whether the symbol is publicly visible.
	// In Go: starts with uppercase. In Python: not prefixed with underscore.
	// In TypeScript: has "export" keyword.
	Exported bool `json:"exported"`

	// Language is the programming language of the source file.
	// Example: "go", "python", "typescript"
	Language string `json:"language"`

	// ParsedAtMilli is the Unix timestamp in milliseconds when this symbol was parsed.
	// Used for cache invalidation and staleness detection.
	ParsedAtMilli int64 `json:"parsed_at_milli"`

	// Children contains nested symbols (e.g., methods within a class).
	// May be nil if the symbol has no children.
	Children []*Symbol `json:"children,omitempty"`

	// Metadata contains language-specific additional information.
	// Examples: decorators for Python, generics for TypeScript.
	// Keys are well-defined strings, not arbitrary data.
	Metadata *SymbolMetadata `json:"metadata,omitempty"`

	// Calls contains function/method calls made within this symbol's body.
	// Only populated for functions and methods during AST parsing.
	// Used by the graph builder to create EdgeTypeCalls edges.
	// See GR-41: Call Edge Extraction for find_callers/find_callees.
	Calls []CallSite `json:"calls,omitempty"`
}

// MethodSignature represents a method's signature for interface implementation detection.
//
// This struct captures the essential signature information needed to determine
// if a type implements an interface via method-set matching. It focuses on
// structural compatibility rather than exact type matching.
//
// For Go interface implementation detection, we compare method names and
// parameter/return counts. Full type checking would require go/types which
// needs the complete compilation context.
type MethodSignature struct {
	// Name is the method name.
	// Example: "Read", "Write", "Close"
	Name string `json:"name"`

	// Params is the normalized parameter list (types only, no names).
	// Example: "[]byte" for func Read(p []byte) (int, error)
	// Used for Phase 2 signature matching.
	Params string `json:"params,omitempty"`

	// Returns is the normalized return type list.
	// Example: "int, error" for func Read(p []byte) (int, error)
	// Used for Phase 2 signature matching.
	Returns string `json:"returns,omitempty"`

	// ParamCount is the number of parameters.
	// Used for quick filtering before detailed signature comparison.
	ParamCount int `json:"param_count"`

	// ReturnCount is the number of return values.
	// Used for quick filtering before detailed signature comparison.
	ReturnCount int `json:"return_count"`

	// ReceiverType is the receiver type for methods (e.g., "*Handler", "Handler").
	// Empty for interface method declarations.
	// Used to distinguish value vs pointer receivers.
	ReceiverType string `json:"receiver_type,omitempty"`
}

// CallSite represents a function or method call within a symbol's body.
//
// Description:
//
//	CallSite captures the essential information about a function or method
//	call expression found during AST parsing. This enables the graph builder
//	to create EdgeTypeCalls edges for the find_callers and find_callees tools.
//
// Used by:
//   - GoParser.extractCallSites() to collect calls from function bodies
//   - Builder.extractCallEdges() to create EdgeTypeCalls edges
//   - find_callers/find_callees CRS tools for call graph queries
//
// Thread Safety: CallSite is immutable after creation and safe for concurrent read.
type CallSite struct {
	// Target is the name of the called function or method.
	//
	// For simple function calls: "FunctionName"
	// For qualified calls: "package.Function" or "receiver.Method"
	// For method calls: "Method" (receiver tracked separately)
	//
	// Example values:
	//   - "Setup" (local function)
	//   - "config.Load" (imported package function)
	//   - "Connect" (method call, Receiver="db")
	Target string `json:"target"`

	// Location is where the call expression occurs in the source file.
	// Points to the start of the call expression.
	Location Location `json:"location"`

	// IsMethod indicates if this is a method call (has a receiver).
	// True for: obj.Method(), s.Initialize()
	// False for: Function(), pkg.Function()
	IsMethod bool `json:"is_method,omitempty"`

	// Receiver is the receiver expression text for method calls.
	// Empty for plain function calls.
	//
	// Examples:
	//   - "db" for db.Connect()
	//   - "s" for s.Initialize()
	//   - "ctx" for ctx.Done()
	Receiver string `json:"receiver,omitempty"`

	// FunctionArgs lists identifiers passed as callback/HOF arguments.
	// Only populated when a call passes identifiers that may reference other functions.
	// IT-03a C-1: Enables EdgeTypeReferences from caller to callback arguments.
	//
	// Examples:
	//   - ["middleware"] for app.use(middleware)
	//   - ["LoggingInterceptor"] for UseInterceptors(LoggingInterceptor)
	FunctionArgs []string `json:"function_args,omitempty"`
}

// Validate checks if the CallSite has valid field values.
//
// Returns nil if valid, or a ValidationError describing the issue.
//
// Validates:
//   - Target is non-empty
//   - Location.StartLine is positive
func (c *CallSite) Validate() error {
	if c.Target == "" {
		return ValidationError{Field: "Target", Message: "must not be empty"}
	}
	if c.Location.StartLine < 1 {
		return ValidationError{Field: "Location.StartLine", Message: "must be >= 1"}
	}
	return nil
}

// MaxCallSitesPerSymbol is the maximum number of call sites extracted per symbol.
// This prevents memory exhaustion from pathologically large functions.
const MaxCallSitesPerSymbol = 1000

// MaxCallExpressionDepth is the maximum nesting depth for call expression traversal.
// This prevents stack overflow from deeply nested expressions like f(g(h(i(...)))).
const MaxCallExpressionDepth = 50

// SymbolMetadata contains optional language-specific metadata for a symbol.
//
// This struct provides type-safe storage for language-specific information
// that doesn't fit in the core Symbol fields. All fields are optional.
type SymbolMetadata struct {
	// Decorators lists decorator/annotation names applied to the symbol.
	// Example: ["staticmethod", "cache"] for Python.
	Decorators []string `json:"decorators,omitempty"`

	// DecoratorArgs maps decorator names to their argument identifiers.
	// Only populated when a decorator has arguments that reference symbols.
	// Example: {"UseInterceptors": ["LoggingInterceptor"], "Module": ["UserService"]}
	// IT-03a A-3: Enables EdgeTypeReferences from decorated symbol to decorator arguments.
	DecoratorArgs map[string][]string `json:"decorator_args,omitempty"`

	// TypeParameters lists generic type parameter names.
	// Example: ["T", "U"] for TypeScript generic function.
	TypeParameters []string `json:"type_parameters,omitempty"`

	// TypeArguments lists type identifiers used as generic arguments in the symbol's type annotations.
	// IT-03a C-2: Tracks type arguments like User in Promise<User>, Handler in Map<string, Handler>.
	// Only populated for non-primitive type names (skips string, number, boolean, etc.).
	TypeArguments []string `json:"type_arguments,omitempty"`

	// ReturnType is the declared return type (if available).
	// Example: "Promise<User>" for TypeScript async function.
	ReturnType string `json:"return_type,omitempty"`

	// IsAsync indicates if the function/method is asynchronous.
	IsAsync bool `json:"is_async,omitempty"`

	// IsGenerator indicates if the function is a generator.
	IsGenerator bool `json:"is_generator,omitempty"`

	// IsAbstract indicates if the class/method is abstract.
	IsAbstract bool `json:"is_abstract,omitempty"`

	// IsStatic indicates if the method is static.
	IsStatic bool `json:"is_static,omitempty"`

	// AccessModifier is the access level (public, private, protected).
	// Empty string means default/public access.
	AccessModifier string `json:"access_modifier,omitempty"`

	// Implements lists interface names that a class/struct implements.
	// For languages with explicit implements (TypeScript, Java), this is populated
	// by the parser. For Go, this is populated by the graph builder via
	// method-set matching (see GR-40).
	Implements []string `json:"implements,omitempty"`

	// Extends is the parent class name for inheritance.
	Extends string `json:"extends,omitempty"`

	// Methods contains method signatures for interfaces and types.
	// For interfaces: lists required method signatures.
	// For structs/types: lists methods with receivers matching this type.
	// Used for Go interface implementation detection via method-set matching (GR-40).
	Methods []MethodSignature `json:"methods,omitempty"`

	// IsConstructor indicates a JavaScript constructor function (uses this.x = ...).
	// IT-03a B-1: Constructor functions are reclassified as SymbolKindClass for graph visibility.
	IsConstructor bool `json:"is_constructor,omitempty"`

	// TypeNarrowings lists type identifiers used in type narrowing expressions.
	// IT-03a C-3: Tracks types referenced via instanceof, typeof, and type predicates.
	// Example: ["Router", "Response"] for code containing `x instanceof Router`.
	TypeNarrowings []string `json:"type_narrowings,omitempty"`

	// PrototypeOf indicates this symbol was assigned via Foo.prototype.method = ...
	// IT-03a B-2: Links prototype method assignments to their constructor function.
	PrototypeOf string `json:"prototype_of,omitempty"`

	// CSSSelector is the full CSS selector for CSS symbols.
	CSSSelector string `json:"css_selector,omitempty"`

	// ParentName is the parent symbol name (e.g., table name for columns).
	ParentName string `json:"parent_name,omitempty"`

	// SQLConstraints lists SQL constraints for columns (PRIMARY KEY, UNIQUE, etc.).
	SQLConstraints []string `json:"sql_constraints,omitempty"`

	// HeadingLevel is the heading level (1-6) for Markdown headings.
	HeadingLevel int `json:"heading_level,omitempty"`

	// CodeLanguage is the language identifier for fenced code blocks.
	// Example: "go", "python", "javascript"
	CodeLanguage string `json:"code_language,omitempty"`

	// ListType is the type of list: "ordered" or "unordered".
	ListType string `json:"list_type,omitempty"`

	// ListItems is the number of items in a list.
	ListItems int `json:"list_items,omitempty"`

	// LinkURL is the destination URL for link reference definitions.
	LinkURL string `json:"link_url,omitempty"`

	// LinkTitle is the optional title for link reference definitions.
	LinkTitle string `json:"link_title,omitempty"`
}

// GenerateID creates a unique identifier for a symbol based on its location and name.
//
// Format: "file_path:start_line:name"
//
// This format ensures uniqueness within a project while remaining human-readable
// and useful for debugging. Two symbols at the same location with the same name
// are considered identical.
//
// SECURITY: Callers MUST validate that filePath is within the project boundary
// before calling this function. This function does NOT perform path validation
// to avoid redundant checks when called in bulk. Use Symbol.Validate() or
// ParseResult.Validate() to verify paths don't contain traversal sequences.
//
// Parameters:
//   - filePath: Path relative to project root. Must not contain ".." sequences.
//   - startLine: 1-indexed line number where the symbol starts.
//   - name: The symbol's identifier name.
func GenerateID(filePath string, startLine int, name string) string {
	return fmt.Sprintf("%s:%d:%s", filePath, startLine, name)
}

// Location returns the symbol's location as a Location struct.
func (s *Symbol) Location() Location {
	return Location{
		FilePath:  s.FilePath,
		StartLine: s.StartLine,
		EndLine:   s.EndLine,
		StartCol:  s.StartCol,
		EndCol:    s.EndCol,
	}
}

// SetParsedAt sets the ParsedAtMilli field to the current time.
func (s *Symbol) SetParsedAt() {
	s.ParsedAtMilli = time.Now().UnixMilli()
}

// ValidationError represents a validation failure with field context.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Validate checks if the Symbol has valid field values.
//
// Returns nil if valid, or a ValidationError describing the first invalid field.
//
// Validates:
//   - Name is non-empty
//   - FilePath is non-empty and doesn't contain path traversal
//   - StartLine is positive (1-indexed)
//   - EndLine >= StartLine
//   - StartCol is non-negative (0-indexed)
//   - EndCol >= 0
//   - Language is non-empty
func (s *Symbol) Validate() error {
	if s.Name == "" {
		return ValidationError{Field: "Name", Message: "must not be empty"}
	}

	if s.FilePath == "" {
		return ValidationError{Field: "FilePath", Message: "must not be empty"}
	}

	// Check for path traversal attempts
	if strings.Contains(s.FilePath, "..") {
		return ValidationError{Field: "FilePath", Message: "must not contain path traversal (..)"}
	}

	if s.StartLine < 1 {
		return ValidationError{Field: "StartLine", Message: "must be >= 1 (1-indexed)"}
	}

	if s.EndLine < s.StartLine {
		return ValidationError{Field: "EndLine", Message: "must be >= StartLine"}
	}

	if s.StartCol < 0 {
		return ValidationError{Field: "StartCol", Message: "must be >= 0 (0-indexed)"}
	}

	if s.EndCol < 0 {
		return ValidationError{Field: "EndCol", Message: "must be >= 0"}
	}

	if s.Language == "" {
		return ValidationError{Field: "Language", Message: "must not be empty"}
	}

	// Recursively validate children
	for i, child := range s.Children {
		if err := child.Validate(); err != nil {
			return ValidationError{
				Field:   fmt.Sprintf("Children[%d]", i),
				Message: err.Error(),
			}
		}
	}

	return nil
}

// ParseResult contains the output of parsing a single source file.
//
// This struct is returned by Parser.Parse() and contains all symbols
// extracted from the file along with metadata about the parse operation.
//
// # Import Handling
//
// Imports are stored in two places by design:
//   - Imports field: Structured data optimized for dependency resolution and
//     library seeding (path, alias, specific names imported)
//   - Symbols field: Import symbols with Kind=SymbolKindImport for graph building
//     and location tracking (consistent with other symbol types)
//
// This duplication is intentional: the Imports field provides rich import
// metadata (aliases, selective imports, wildcards) while the Symbols field
// maintains a uniform representation for graph construction. Consumers should
// use Imports for dependency analysis and Symbols for code navigation.
type ParseResult struct {
	// FilePath is the path to the parsed file, relative to project root.
	FilePath string `json:"file_path"`

	// Language is the detected or specified language of the file.
	// Example: "go", "python", "typescript"
	Language string `json:"language"`

	// Symbols contains all symbols extracted from the file.
	// Symbols are in source order (top to bottom).
	// Note: Import statements appear here as SymbolKindImport entries.
	Symbols []*Symbol `json:"symbols"`

	// Imports lists all import statements with structured metadata.
	// Use this field for dependency resolution and library documentation seeding.
	// See Import type for details on the structured import data.
	Imports []Import `json:"imports"`

	// Package is the package/module name declared in the file.
	// May be empty for languages without package declarations.
	Package string `json:"package"`

	// ParsedAtMilli is the Unix timestamp in milliseconds when parsing completed.
	ParsedAtMilli int64 `json:"parsed_at_milli"`

	// ParseDurationMs is how long parsing took in milliseconds.
	ParseDurationMs int64 `json:"parse_duration_ms"`

	// Errors contains non-fatal parse errors encountered.
	// The parse may still produce partial results despite errors.
	Errors []string `json:"errors,omitempty"`

	// Hash is the SHA256 hash of the file content at parse time.
	// Used for cache invalidation and staleness detection.
	Hash string `json:"hash"`
}

// Import represents an import statement in source code.
//
// Import statements are tracked separately for building the dependency graph
// and for seeding library documentation.
type Import struct {
	// Path is the import path or module name.
	// Example: "github.com/gin-gonic/gin" for Go, "os.path" for Python.
	Path string `json:"path"`

	// Alias is the local alias if the import is aliased.
	// Example: "gin" for 'import "github.com/gin-gonic/gin"' (Go default).
	// Example: "pd" for 'import pandas as pd' (Python).
	Alias string `json:"alias,omitempty"`

	// Names lists specific names imported (for selective imports).
	// Example: ["useState", "useEffect"] for 'import { useState, useEffect } from "react"'.
	Names []string `json:"names,omitempty"`

	// IsWildcard indicates if this is a wildcard import.
	// Example: 'from module import *' in Python.
	IsWildcard bool `json:"is_wildcard,omitempty"`

	// IsRelative indicates if this is a relative import.
	// Example: 'from . import foo' or 'from ..utils import bar' in Python.
	// For absolute imports, this is false.
	IsRelative bool `json:"is_relative,omitempty"`

	// IsDefault indicates if this is a default import.
	// Example: 'import foo from "bar"' in JavaScript/TypeScript.
	IsDefault bool `json:"is_default,omitempty"`

	// IsNamespace indicates if this is a namespace import.
	// Example: 'import * as foo from "bar"' in JavaScript/TypeScript.
	IsNamespace bool `json:"is_namespace,omitempty"`

	// IsTypeOnly indicates if this is a type-only import.
	// Example: 'import type { Foo } from "bar"' in TypeScript.
	IsTypeOnly bool `json:"is_type_only,omitempty"`

	// IsCommonJS indicates if this is a CommonJS require() import.
	// Example: 'const foo = require("bar")' in JavaScript.
	IsCommonJS bool `json:"is_commonjs,omitempty"`

	// IsScript indicates this is a script import (HTML <script src>).
	IsScript bool `json:"is_script,omitempty"`

	// IsStylesheet indicates this is a stylesheet import (HTML <link href> or CSS @import).
	IsStylesheet bool `json:"is_stylesheet,omitempty"`

	// IsModule indicates this is an ES module import.
	// For HTML: <script type="module">
	// For JS: import statement (vs require)
	IsModule bool `json:"is_module,omitempty"`

	// MediaQuery contains the media query for conditional imports.
	// Example: @import 'print.css' print → MediaQuery: "print"
	MediaQuery string `json:"media_query,omitempty"`

	// Location is where the import statement appears in the file.
	Location Location `json:"location"`
}

// MaxSymbolDepth is the maximum nesting depth for symbol traversal.
// This prevents stack overflow from maliciously crafted input.
const MaxSymbolDepth = 100

// SymbolCount returns the total number of symbols in the parse result,
// including nested children up to MaxSymbolDepth levels.
//
// Uses an iterative approach with explicit stack to prevent stack overflow
// from deeply nested symbol hierarchies.
func (r *ParseResult) SymbolCount() int {
	return r.SymbolCountWithDepth(MaxSymbolDepth)
}

// SymbolCountWithDepth returns the total number of symbols up to the specified depth.
//
// Parameters:
//   - maxDepth: Maximum nesting depth to traverse. 0 means only top-level symbols.
//     Use MaxSymbolDepth constant for default safe limit.
//
// Uses an iterative approach with explicit stack to prevent stack overflow.
func (r *ParseResult) SymbolCountWithDepth(maxDepth int) int {
	if r.Symbols == nil {
		return 0
	}

	type stackEntry struct {
		symbols []*Symbol
		depth   int
	}

	count := 0
	stack := []stackEntry{{symbols: r.Symbols, depth: 0}}

	for len(stack) > 0 {
		// Pop from stack
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		for _, s := range entry.symbols {
			count++
			// Only traverse children if within depth limit
			if s.Children != nil && entry.depth < maxDepth {
				stack = append(stack, stackEntry{
					symbols: s.Children,
					depth:   entry.depth + 1,
				})
			}
		}
	}

	return count
}

// HasErrors returns true if the parse result contains any errors.
func (r *ParseResult) HasErrors() bool {
	return len(r.Errors) > 0
}

// SetParsedAt sets the ParsedAtMilli field to the current time.
func (r *ParseResult) SetParsedAt() {
	r.ParsedAtMilli = time.Now().UnixMilli()
}

// Validate checks if the ParseResult has valid field values.
//
// Returns nil if valid, or a ValidationError describing the first invalid field.
//
// Validates:
//   - FilePath is non-empty and doesn't contain path traversal
//   - Language is non-empty
//   - All Symbols are valid (via Symbol.Validate())
//   - All Imports have valid locations
func (r *ParseResult) Validate() error {
	if r.FilePath == "" {
		return ValidationError{Field: "FilePath", Message: "must not be empty"}
	}

	// Check for path traversal attempts
	if strings.Contains(r.FilePath, "..") {
		return ValidationError{Field: "FilePath", Message: "must not contain path traversal (..)"}
	}

	if r.Language == "" {
		return ValidationError{Field: "Language", Message: "must not be empty"}
	}

	// Validate all symbols
	for i, sym := range r.Symbols {
		if err := sym.Validate(); err != nil {
			return ValidationError{
				Field:   fmt.Sprintf("Symbols[%d]", i),
				Message: err.Error(),
			}
		}
	}

	// Validate import locations
	for i, imp := range r.Imports {
		if imp.Path == "" {
			return ValidationError{
				Field:   fmt.Sprintf("Imports[%d].Path", i),
				Message: "must not be empty",
			}
		}
		if imp.Location.StartLine < 1 {
			return ValidationError{
				Field:   fmt.Sprintf("Imports[%d].Location.StartLine", i),
				Message: "must be >= 1",
			}
		}
	}

	return nil
}
