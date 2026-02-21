// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package ast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"go.opentelemetry.io/otel/attribute"
)

// File size constants for security validation.
const (
	// DefaultMaxFileSize is the maximum file size the parser will accept (10MB).
	DefaultMaxFileSize = 10 * 1024 * 1024

	// WarnFileSize is the threshold at which a warning is logged (1MB).
	WarnFileSize = 1 * 1024 * 1024
)

// ErrFileTooLarge is returned when input content exceeds the maximum file size.
var ErrFileTooLarge = errors.New("file exceeds maximum size limit")

// GoParserOption configures a GoParser instance.
type GoParserOption func(*GoParser)

// WithMaxFileSize sets the maximum file size the parser will accept.
//
// Parameters:
//   - bytes: Maximum file size in bytes. Must be positive.
//
// Example:
//
//	parser := NewGoParser(WithMaxFileSize(5 * 1024 * 1024)) // 5MB limit
func WithMaxFileSize(bytes int64) GoParserOption {
	return func(p *GoParser) {
		if bytes > 0 {
			p.maxFileSize = bytes
		}
	}
}

// WithParseOptions applies the given ParseOptions to the parser.
//
// Parameters:
//   - opts: ParseOptions to apply.
//
// Example:
//
//	parser := NewGoParser(WithParseOptions(ParseOptions{IncludePrivate: false}))
func WithParseOptions(opts ParseOptions) GoParserOption {
	return func(p *GoParser) {
		p.parseOptions = opts
	}
}

// GoParser implements the Parser interface for Go source code.
//
// Description:
//
//	GoParser uses tree-sitter to parse Go source files and extract symbols.
//	It supports concurrent use from multiple goroutines - each Parse call
//	creates its own tree-sitter parser instance internally.
//
// Thread Safety:
//
//	GoParser instances are safe for concurrent use. Multiple goroutines
//	may call Parse simultaneously on the same GoParser instance.
//
// Example:
//
//	parser := NewGoParser()
//	result, err := parser.Parse(ctx, []byte("package main\n\nfunc main() {}"), "main.go")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, sym := range result.Symbols {
//	    fmt.Printf("%s: %s\n", sym.Kind, sym.Name)
//	}
type GoParser struct {
	maxFileSize  int64
	parseOptions ParseOptions
}

// NewGoParser creates a new GoParser with the given options.
//
// Description:
//
//	Creates a GoParser configured with sensible defaults. Options can be
//	provided to customize behavior such as maximum file size.
//
// Inputs:
//   - opts: Optional configuration functions (WithMaxFileSize, WithParseOptions)
//
// Outputs:
//   - *GoParser: Configured parser instance, never nil
//
// Example:
//
//	// Default configuration
//	parser := NewGoParser()
//
//	// Custom max file size
//	parser := NewGoParser(WithMaxFileSize(5 * 1024 * 1024))
//
// Thread Safety:
//
//	The returned GoParser is safe for concurrent use.
func NewGoParser(opts ...GoParserOption) *GoParser {
	p := &GoParser{
		maxFileSize:  DefaultMaxFileSize,
		parseOptions: DefaultParseOptions(),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Parse extracts symbols from Go source code.
//
// Description:
//
//	Parse uses tree-sitter to parse the provided Go source code and extract
//	all symbols (functions, methods, types, interfaces, etc.) into a ParseResult.
//	The parser is error-tolerant and will return partial results for syntactically
//	invalid code.
//
// Inputs:
//   - ctx: Context for cancellation. Checked before and after parsing.
//     Note: Tree-sitter parsing itself cannot be interrupted mid-parse.
//   - content: Raw Go source code bytes. Must be valid UTF-8.
//   - filePath: Path to the file (for ID generation and error reporting).
//     Should be relative to project root using forward slashes.
//
// Outputs:
//   - *ParseResult: Extracted symbols and metadata. Never nil on success.
//     May contain partial results with errors for syntactically invalid code.
//   - error: Non-nil for complete failures:
//   - ErrFileTooLarge: Content exceeds maxFileSize
//   - ErrInvalidContent: Content is not valid UTF-8
//   - Context errors: Context was canceled or timed out
//
// Example:
//
//	result, err := parser.Parse(ctx, []byte("package main\n\nfunc Hello() {}"), "main.go")
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Found %d symbols\n", len(result.Symbols))
//
// Limitations:
//   - Tree-sitter parsing is synchronous and cannot be interrupted mid-parse
//   - Very large files may take significant time to parse
//   - Some edge cases in Go syntax may not be fully handled
//
// Assumptions:
//   - Content is valid UTF-8 (validated internally)
//   - FilePath uses forward slashes as path separator
//   - FilePath does not contain path traversal sequences
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *GoParser) Parse(ctx context.Context, content []byte, filePath string) (*ParseResult, error) {
	// Start tracing span
	ctx, span := startParseSpan(ctx, "go", filePath, len(content))
	defer span.End()

	start := time.Now()

	// Check context before starting
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled before start: %w", err)
	}

	// Validate file size
	if int64(len(content)) > p.maxFileSize {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("%w: size %d exceeds limit %d", ErrFileTooLarge, len(content), p.maxFileSize)
	}

	// Log warning for large files
	if len(content) > WarnFileSize {
		slog.Warn("parsing large file",
			slog.String("file", filePath),
			slog.Int("size_bytes", len(content)))
	}

	// Validate UTF-8
	if !utf8.Valid(content) {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("%w: content is not valid UTF-8", ErrInvalidContent)
	}

	// Compute hash before parsing (captures input)
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:])

	// Create tree-sitter parser (new instance per call for thread safety)
	parser := sitter.NewParser()
	parser.SetLanguage(golang.GetLanguage())

	// Parse the content
	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("tree-sitter parse failed: %w", err)
	}
	defer tree.Close()

	// Check context after parsing
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled after tree-sitter: %w", err)
	}

	// Build result
	result := &ParseResult{
		FilePath:      filePath,
		Language:      "go",
		Hash:          hashStr,
		ParsedAtMilli: time.Now().UnixMilli(),
		Symbols:       make([]*Symbol, 0),
		Imports:       make([]Import, 0),
		Errors:        make([]string, 0),
	}

	// Extract symbols from the tree
	rootNode := tree.RootNode()
	if rootNode == nil {
		result.Errors = append(result.Errors, "tree-sitter returned nil root node")
		return result, nil
	}

	// Check for syntax errors in tree
	if rootNode.HasError() {
		result.Errors = append(result.Errors, "source contains syntax errors")
	}

	// Extract package name (returns package name for use in other symbols)
	packageName := p.extractPackage(rootNode, content, filePath, result)

	// Extract imports
	p.extractImports(rootNode, content, filePath, result)

	// Extract functions (GR-41: now extracts call sites too)
	p.extractFunctions(ctx, rootNode, content, filePath, packageName, result)

	// Extract methods (GR-41: now extracts call sites too)
	p.extractMethods(ctx, rootNode, content, filePath, packageName, result)

	// Extract types (structs, interfaces, type aliases)
	p.extractTypes(rootNode, content, filePath, result)

	// Extract top-level variables and constants
	p.extractVariables(rootNode, content, filePath, result)
	p.extractConstants(rootNode, content, filePath, result)

	// Associate methods with their receiver types for interface implementation detection (GR-40)
	p.associateMethodsWithTypes(result)

	// Validate result before returning
	if err := result.Validate(); err != nil {
		recordParseMetrics(ctx, "go", time.Since(start), 0, false)
		return nil, fmt.Errorf("result validation failed: %w", err)
	}

	// Check context one final time
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "go", time.Since(start), len(result.Symbols), false)
		return nil, fmt.Errorf("parse canceled after extraction: %w", err)
	}

	// Record successful parse metrics
	setParseSpanResult(span, len(result.Symbols), len(result.Errors))
	recordParseMetrics(ctx, "go", time.Since(start), len(result.Symbols), true)

	return result, nil
}

// Language returns the canonical language name for this parser.
//
// Returns:
//   - "go" for Go source files
func (p *GoParser) Language() string {
	return "go"
}

// Extensions returns the file extensions this parser handles.
//
// Returns:
//   - []string{".go"} for Go source files
func (p *GoParser) Extensions() []string {
	return []string{".go"}
}

// extractPackage extracts the package declaration from the AST.
// Returns the package name string for use in setting Package field on other symbols.
func (p *GoParser) extractPackage(root *sitter.Node, content []byte, filePath string, result *ParseResult) string {
	// Find package_clause node
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "package_clause" {
			// Get package name
			for j := 0; j < int(child.ChildCount()); j++ {
				nameNode := child.Child(j)
				if nameNode.Type() == "package_identifier" {
					name := string(content[nameNode.StartByte():nameNode.EndByte()])

					sym := &Symbol{
						ID:        GenerateID(filePath, int(nameNode.StartPoint().Row+1), name),
						Name:      name,
						Kind:      SymbolKindPackage,
						FilePath:  filePath,
						Language:  "go",
						Exported:  true,
						StartLine: int(nameNode.StartPoint().Row + 1),
						EndLine:   int(nameNode.EndPoint().Row + 1),
						StartCol:  int(nameNode.StartPoint().Column + 1),
						EndCol:    int(nameNode.EndPoint().Column + 1),
					}

					// Look for preceding comment
					sym.DocComment = p.getPrecedingComment(root, child, content)

					result.Symbols = append(result.Symbols, sym)
					return name // Return package name for use in other symbols
				}
			}
		}
	}
	return "" // No package declaration found
}

// extractImports extracts import declarations from the AST.
func (p *GoParser) extractImports(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "import_declaration" {
			p.processImportDecl(child, content, filePath, result)
		}
	}
}

// processImportDecl processes a single import declaration (which may contain multiple imports).
func (p *GoParser) processImportDecl(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)

		switch child.Type() {
		case "import_spec":
			p.processImportSpec(child, content, filePath, result)
		case "import_spec_list":
			// Grouped imports: import ( ... )
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "import_spec" {
					p.processImportSpec(spec, content, filePath, result)
				}
			}
		}
	}
}

// processImportSpec extracts a single import specification.
func (p *GoParser) processImportSpec(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	var alias string
	var path string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "package_identifier", "blank_identifier", "dot":
			alias = string(content[child.StartByte():child.EndByte()])
		case "interpreted_string_literal":
			// Remove quotes from path
			raw := string(content[child.StartByte():child.EndByte()])
			path = strings.Trim(raw, "\"")
		}
	}

	if path == "" {
		return
	}

	startLine := int(node.StartPoint().Row + 1)
	endLine := int(node.EndPoint().Row + 1)
	startCol := int(node.StartPoint().Column + 1)
	endCol := int(node.EndPoint().Column + 1)

	imp := Import{
		Path:  path,
		Alias: alias,
		Location: Location{
			FilePath:  filePath,
			StartLine: startLine,
			EndLine:   endLine,
			StartCol:  startCol,
			EndCol:    endCol,
		},
	}
	result.Imports = append(result.Imports, imp)

	// Also add as symbol for AST completeness
	sym := &Symbol{
		ID:        GenerateID(filePath, startLine, path),
		Name:      path,
		Kind:      SymbolKindImport,
		FilePath:  filePath,
		Language:  "go",
		Exported:  true,
		StartLine: startLine,
		EndLine:   endLine,
		StartCol:  startCol,
		EndCol:    endCol,
	}
	result.Symbols = append(result.Symbols, sym)
}

// extractFunctions extracts function declarations from the AST.
// GR-41: Now accepts context for call site extraction.
func (p *GoParser) extractFunctions(ctx context.Context, root *sitter.Node, content []byte, filePath string, packageName string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "function_declaration" {
			p.processFunctionDecl(ctx, child, content, filePath, packageName, result, root)
		}
	}
}

// processFunctionDecl extracts a single function declaration.
// GR-41: Now accepts context and extracts call sites from function body.
func (p *GoParser) processFunctionDecl(ctx context.Context, node *sitter.Node, content []byte, filePath string, packageName string, result *ParseResult, root *sitter.Node) {
	var name string
	var signature string
	var params string
	var returns string
	var bodyNode *sitter.Node
	var paramListNode *sitter.Node  // IT-06 Bug 9: track for type ref extraction
	var returnTypeNode *sitter.Node // IT-06 Bug 9: track standalone return type node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "parameter_list":
			// First parameter_list is params, subsequent ones are return types
			plist := string(content[child.StartByte():child.EndByte()])
			if params == "" {
				params = plist
				paramListNode = child // IT-06 Bug 9
			} else {
				returns = plist
				returnTypeNode = child // IT-06 Bug 9: return parameter_list
			}
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type", "interface_type", "struct_type", "function_type":
			returns = string(content[child.StartByte():child.EndByte()])
			returnTypeNode = child // IT-06 Bug 9
		case "block":
			// GR-41: Capture function body for call extraction
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	// Build signature
	signature = fmt.Sprintf("func %s%s", name, params)
	if returns != "" {
		signature += " " + returns
	}

	exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

	// Filter unexported if configured
	if !p.parseOptions.IncludePrivate && !exported {
		return
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindFunction,
		FilePath:  filePath,
		Package:   packageName, // GR-17: Set package name for entry point detection
		Language:  "go",
		Exported:  exported,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column + 1),
		EndCol:    int(node.EndPoint().Column + 1),
	}

	// Get doc comment
	sym.DocComment = p.getPrecedingComment(root, node, content)

	// GR-41: Extract call sites from function body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	// IT-06 Bug 9: Extract type references from parameter and return type annotations
	var typeRefs []TypeReference
	if paramListNode != nil {
		typeRefs = append(typeRefs, extractGoTypeRefsFromParamList(paramListNode, content, filePath)...)
	}
	if returnTypeNode != nil {
		if returnTypeNode.Type() == "parameter_list" {
			typeRefs = append(typeRefs, extractGoTypeRefsFromParamList(returnTypeNode, content, filePath)...)
		} else {
			typeRefs = append(typeRefs, extractGoReturnTypeRefs(returnTypeNode, content, filePath)...)
		}
	}
	if len(typeRefs) > 0 {
		if len(typeRefs) > MaxTypeReferencesPerSymbol {
			typeRefs = typeRefs[:MaxTypeReferencesPerSymbol]
		}
		sym.TypeReferences = typeRefs
	}

	result.Symbols = append(result.Symbols, sym)
}

// extractMethods extracts method declarations from the AST.
// GR-41: Now accepts context for call site extraction.
func (p *GoParser) extractMethods(ctx context.Context, root *sitter.Node, content []byte, filePath string, packageName string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "method_declaration" {
			p.processMethodDecl(ctx, child, content, filePath, packageName, result, root)
		}
	}
}

// processMethodDecl extracts a single method declaration.
// GR-41: Now accepts context and extracts call sites from method body.
func (p *GoParser) processMethodDecl(ctx context.Context, node *sitter.Node, content []byte, filePath string, packageName string, result *ParseResult, root *sitter.Node) {
	var name string
	var receiverStr string
	var params string
	var returns string
	var bodyNode *sitter.Node
	var paramListNode *sitter.Node  // IT-06 Bug 9: track params (not receiver) for type ref extraction
	var returnTypeNode *sitter.Node // IT-06 Bug 9: track standalone return type node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "parameter_list":
			// First parameter_list is receiver, second is params
			plist := string(content[child.StartByte():child.EndByte()])
			if receiverStr == "" {
				receiverStr = plist
			} else if params == "" {
				params = plist
				paramListNode = child // IT-06 Bug 9: second parameter_list is params
			} else {
				// Third parameter_list is returns
				returns = plist
				returnTypeNode = child // IT-06 Bug 9: third parameter_list is return types
			}
		case "field_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type":
			returns = string(content[child.StartByte():child.EndByte()])
			returnTypeNode = child // IT-06 Bug 9
		case "block":
			// GR-41: Capture method body for call extraction
			bodyNode = child
		}
	}

	if name == "" {
		return
	}

	// Build signature
	signature := fmt.Sprintf("func %s %s%s", receiverStr, name, params)
	if returns != "" {
		signature += " " + returns
	}

	// Extract receiver type name for method-type association (GR-40)
	receiverTypeName := extractReceiverTypeFromString(receiverStr)

	exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

	// Filter unexported if configured
	if !p.parseOptions.IncludePrivate && !exported {
		return
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindMethod,
		FilePath:  filePath,
		Package:   packageName, // GR-17: Set package name for entry point detection
		Language:  "go",
		Exported:  exported,
		Signature: signature,
		Receiver:  receiverTypeName,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column + 1),
		EndCol:    int(node.EndPoint().Column + 1),
	}

	// Get doc comment
	sym.DocComment = p.getPrecedingComment(root, node, content)

	// GR-41: Extract call sites from method body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	// IT-06 Bug 9: Extract type references from parameter and return type annotations
	var typeRefs []TypeReference
	if paramListNode != nil {
		typeRefs = append(typeRefs, extractGoTypeRefsFromParamList(paramListNode, content, filePath)...)
	}
	if returnTypeNode != nil {
		if returnTypeNode.Type() == "parameter_list" {
			typeRefs = append(typeRefs, extractGoTypeRefsFromParamList(returnTypeNode, content, filePath)...)
		} else {
			typeRefs = append(typeRefs, extractGoReturnTypeRefs(returnTypeNode, content, filePath)...)
		}
	}
	if len(typeRefs) > 0 {
		if len(typeRefs) > MaxTypeReferencesPerSymbol {
			typeRefs = typeRefs[:MaxTypeReferencesPerSymbol]
		}
		sym.TypeReferences = typeRefs
	}

	result.Symbols = append(result.Symbols, sym)
}

// goTypeSkipList contains Go built-in types that should not produce TypeReference entries.
var goTypeSkipList = map[string]bool{
	// Numeric types
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true,
	"complex64": true, "complex128": true,
	// String and byte types
	"string": true, "byte": true, "rune": true,
	// Boolean
	"bool": true,
	// Other builtins
	"error": true, "any": true, "comparable": true,
}

// extractGoTypeRefsFromParamList walks a tree-sitter parameter_list node and extracts
// non-builtin type identifiers as TypeReference entries.
//
// Description:
//
//	Go parameter_list contains parameter_declaration children, each with
//	identifier(s) and a type node (type_identifier, pointer_type, etc.).
//	This walks the type nodes to find type_identifier leaves.
//
// IT-06 Bug 9: Enables graph edges from Go function parameter/return types.
//
// Inputs:
//   - paramListNode: A tree-sitter "parameter_list" node.
//   - content: Source file bytes.
//   - filePath: Relative path for Location.
//
// Outputs:
//   - []TypeReference: Non-builtin type identifiers found in the parameter list.
func extractGoTypeRefsFromParamList(paramListNode *sitter.Node, content []byte, filePath string) []TypeReference {
	if paramListNode == nil {
		return nil
	}

	var refs []TypeReference
	seen := make(map[string]bool)

	var walkType func(node *sitter.Node)
	walkType = func(node *sitter.Node) {
		if node == nil || len(refs) >= MaxTypeReferencesPerSymbol {
			return
		}

		switch node.Type() {
		case "type_identifier":
			name := string(content[node.StartByte():node.EndByte()])
			if !goTypeSkipList[name] && !seen[name] {
				seen[name] = true
				refs = append(refs, TypeReference{
					Name: name,
					Location: Location{
						FilePath:  filePath,
						StartLine: int(node.StartPoint().Row + 1),
						EndLine:   int(node.EndPoint().Row + 1),
						StartCol:  int(node.StartPoint().Column),
						EndCol:    int(node.EndPoint().Column),
					},
				})
			}
			return
		case "qualified_type":
			// For context.Context — extract "Context" (the type_identifier part)
			// but skip since it's external. Walk children to find type_identifier.
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child.Type() == "type_identifier" {
					walkType(child)
				}
			}
			return
		}

		// Recurse into children (pointer_type, slice_type, map_type, etc.)
		for i := 0; i < int(node.ChildCount()); i++ {
			walkType(node.Child(i))
		}
	}

	// Walk each parameter_declaration or variadic_parameter_declaration in the list
	for i := 0; i < int(paramListNode.ChildCount()); i++ {
		child := paramListNode.Child(i)
		if child.Type() == "parameter_declaration" || child.Type() == "variadic_parameter_declaration" {
			// The type is the last non-identifier child. Walk all children that are type nodes.
			for j := 0; j < int(child.ChildCount()); j++ {
				typeChild := child.Child(j)
				switch typeChild.Type() {
				case "type_identifier", "pointer_type", "slice_type", "map_type",
					"channel_type", "qualified_type", "interface_type", "struct_type",
					"function_type", "array_type":
					walkType(typeChild)
				}
			}
		}
	}

	return refs
}

// extractGoReturnTypeRefs extracts TypeReference entries from a standalone return type node.
//
// IT-06 Bug 9: For functions returning a single type (not a parameter_list of returns),
// the return type appears as a direct child of the function_declaration.
//
// Inputs:
//   - typeNode: A tree-sitter type node (type_identifier, pointer_type, etc.).
//   - content: Source file bytes.
//   - filePath: Relative path for Location.
//
// Outputs:
//   - []TypeReference: Non-builtin type identifiers found in the return type.
func extractGoReturnTypeRefs(typeNode *sitter.Node, content []byte, filePath string) []TypeReference {
	if typeNode == nil {
		return nil
	}

	var refs []TypeReference
	seen := make(map[string]bool)

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil || len(refs) >= MaxTypeReferencesPerSymbol {
			return
		}
		if node.Type() == "type_identifier" {
			name := string(content[node.StartByte():node.EndByte()])
			if !goTypeSkipList[name] && !seen[name] {
				seen[name] = true
				refs = append(refs, TypeReference{
					Name: name,
					Location: Location{
						FilePath:  filePath,
						StartLine: int(node.StartPoint().Row + 1),
						EndLine:   int(node.EndPoint().Row + 1),
						StartCol:  int(node.StartPoint().Column),
						EndCol:    int(node.EndPoint().Column),
					},
				})
			}
			return
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}

	walk(typeNode)
	return refs
}

// extractReceiverTypeFromString extracts the type name from a receiver string.
// Input: "(h *Handler)" or "(s Service)"
// Output: "Handler" or "Service" (without pointer/variable)
func extractReceiverTypeFromString(receiver string) string {
	// Remove parentheses
	receiver = strings.TrimPrefix(receiver, "(")
	receiver = strings.TrimSuffix(receiver, ")")
	receiver = strings.TrimSpace(receiver)

	if receiver == "" {
		return ""
	}

	// Split into parts: variable name and type
	parts := strings.Fields(receiver)
	if len(parts) == 0 {
		return ""
	}

	// Last part is the type (potentially with *)
	typePart := parts[len(parts)-1]

	// Remove pointer prefix
	typePart = strings.TrimPrefix(typePart, "*")

	return typePart
}

// extractTypes extracts type declarations (struct, interface, alias) from the AST.
func (p *GoParser) extractTypes(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "type_declaration" {
			p.processTypeDecl(child, content, filePath, result, root)
		}
	}
}

// processTypeDecl extracts type declarations.
func (p *GoParser) processTypeDecl(node *sitter.Node, content []byte, filePath string, result *ParseResult, root *sitter.Node) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_spec" {
			p.processTypeSpec(child, content, filePath, result, root, node)
		}
	}
}

// processTypeSpec extracts a single type specification.
func (p *GoParser) processTypeSpec(node *sitter.Node, content []byte, filePath string, result *ParseResult, root *sitter.Node, parentDecl *sitter.Node) {
	var name string
	var kind SymbolKind
	var typeNode *sitter.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			// Phase 20-C: Only set name from the FIRST type_identifier child.
			// For type aliases like `type nopPage int`, tree-sitter produces two
			// type_identifier children: "nopPage" (the name) and "int" (the aliased type).
			// Without this guard, "int" overwrites "nopPage" and method association fails.
			if name == "" {
				name = string(content[child.StartByte():child.EndByte()])
			}
		case "struct_type":
			kind = SymbolKindStruct
			typeNode = child
		case "interface_type":
			kind = SymbolKindInterface
			typeNode = child
		default:
			if kind == 0 && name != "" {
				kind = SymbolKindType // Type alias
			}
		}
	}

	if name == "" {
		return
	}

	if kind == 0 {
		kind = SymbolKindType
	}

	exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

	// Filter unexported if configured
	if !p.parseOptions.IncludePrivate && !exported {
		return
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      kind,
		FilePath:  filePath,
		Language:  "go",
		Exported:  exported,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column + 1),
		EndCol:    int(node.EndPoint().Column + 1),
	}

	// Get doc comment (from type_declaration, not type_spec)
	sym.DocComment = p.getPrecedingComment(root, parentDecl, content)

	// Extract children (fields for struct, methods for interface)
	if typeNode != nil {
		sym.Children = p.extractTypeChildren(typeNode, content, filePath, kind)

		// For interfaces, also collect method signatures for implementation detection (GR-40)
		if kind == SymbolKindInterface {
			methods := p.collectInterfaceMethods(typeNode, content)
			if len(methods) > 0 {
				if sym.Metadata == nil {
					sym.Metadata = &SymbolMetadata{}
				}
				sym.Metadata.Methods = methods
			}

			// Phase 18: Extract embedded interface names for transitive resolution.
			// Interfaces can embed other interfaces (e.g., ReadWriter embeds Reader + Writer).
			// Store them in Extends/Implements like struct embeds so the builder's
			// extractEmbedsEdges() creates EMBEDS edges for transitive method resolution.
			embeddedIfaces := p.extractInterfaceEmbeds(typeNode, content)
			if len(embeddedIfaces) > 0 {
				if sym.Metadata == nil {
					sym.Metadata = &SymbolMetadata{}
				}
				sym.Metadata.Extends = embeddedIfaces[0]
				if len(embeddedIfaces) > 1 {
					sym.Metadata.Implements = embeddedIfaces[1:]
				}
			}
		}

		// IT-03 H-3: For structs, detect embedded (anonymous) fields and set Extends.
		// This enables the builder to create EMBEDS edges for promoted method resolution.
		if kind == SymbolKindStruct {
			embeddedTypes := p.extractEmbeddedTypes(typeNode, content)
			if len(embeddedTypes) > 0 {
				if sym.Metadata == nil {
					sym.Metadata = &SymbolMetadata{}
				}
				sym.Metadata.Extends = embeddedTypes[0]
				// IT-03a Phase 16 G-1: Store additional embeds in Implements.
				// NOTE: For Go structs, Metadata.Implements holds additional embedded types,
				// NOT interface implementations. Go structs never use explicit "implements"
				// syntax. The builder's extractEmbedsEdges creates EMBEDS edges (not IMPLEMENTS)
				// for these entries when sym.Kind == SymbolKindStruct. The builder also calls
				// extractImplementsEdges, but validateEdgeType prevents incorrect IMPLEMENTS
				// edges when the target is not an interface.
				if len(embeddedTypes) > 1 {
					sym.Metadata.Implements = embeddedTypes[1:]
				}
			}
		}
	}

	result.Symbols = append(result.Symbols, sym)
}

// extractTypeChildren extracts fields from struct or method signatures from interface.
func (p *GoParser) extractTypeChildren(node *sitter.Node, content []byte, filePath string, parentKind SymbolKind) []*Symbol {
	children := make([]*Symbol, 0)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "field_declaration_list":
			// Struct fields
			for j := 0; j < int(child.ChildCount()); j++ {
				field := child.Child(j)
				if field.Type() == "field_declaration" {
					children = append(children, p.extractField(field, content, filePath)...)
				}
			}
		case "method_elem":
			// Interface method (tree-sitter uses method_elem, not method_spec)
			if sym := p.extractMethodSpec(child, content, filePath); sym != nil {
				children = append(children, sym)
			}
		}
	}

	return children
}

// extractField extracts struct fields.
func (p *GoParser) extractField(node *sitter.Node, content []byte, filePath string) []*Symbol {
	fields := make([]*Symbol, 0)
	var fieldType string
	var names []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "field_identifier":
			names = append(names, string(content[child.StartByte():child.EndByte()]))
		default:
			// Assume other nodes are the type
			if len(names) > 0 && fieldType == "" {
				fieldType = string(content[child.StartByte():child.EndByte()])
			}
		}
	}

	for _, name := range names {
		exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

		if !p.parseOptions.IncludePrivate && !exported {
			continue
		}

		sym := &Symbol{
			ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
			Name:      name,
			Kind:      SymbolKindField,
			FilePath:  filePath,
			Language:  "go",
			Exported:  exported,
			Signature: fieldType,
			StartLine: int(node.StartPoint().Row + 1),
			EndLine:   int(node.EndPoint().Row + 1),
			StartCol:  int(node.StartPoint().Column + 1),
			EndCol:    int(node.EndPoint().Column + 1),
		}
		fields = append(fields, sym)
	}

	return fields
}

// extractEmbeddedTypes detects anonymous (embedded) fields in a Go struct type node.
//
// Description:
//
//	Walks the field_declaration_list of a struct_type node looking for
//	field_declaration nodes that have no field_identifier child. These are
//	Go embedded (anonymous) fields. Returns the type names of all embedded types.
//
// Inputs:
//   - structNode: A tree-sitter struct_type node. Must not be nil.
//   - content: Raw source bytes for extracting text.
//
// Outputs:
//   - []string: Names of embedded types (e.g., ["RouterGroup", "Mutex"]).
//     Empty if no embedded fields found.
//
// Limitations:
//   - Qualified embeds (e.g., sync.Mutex) return only the type name ("Mutex"), not the package.
//   - Interface embeds within structs are included (they are valid Go embeds).
//
// Assumptions:
//   - structNode is a valid struct_type node from tree-sitter.
func (p *GoParser) extractEmbeddedTypes(structNode *sitter.Node, content []byte) []string {
	var embedded []string

	for i := 0; i < int(structNode.ChildCount()); i++ {
		child := structNode.Child(i)
		if child.Type() != "field_declaration_list" {
			continue
		}

		for j := 0; j < int(child.ChildCount()); j++ {
			field := child.Child(j)
			if field.Type() != "field_declaration" {
				continue
			}

			// Check if this field_declaration has a field_identifier child.
			// If it does, it's a named field, not an embedded type.
			hasFieldIdentifier := false
			for k := 0; k < int(field.ChildCount()); k++ {
				if field.Child(k).Type() == "field_identifier" {
					hasFieldIdentifier = true
					break
				}
			}

			if hasFieldIdentifier {
				continue
			}

			// No field_identifier — this is an embedded (anonymous) field.
			// Extract the type name from type_identifier, pointer_type, or qualified_type.
			typeName := p.extractEmbeddedTypeName(field, content)
			if typeName != "" {
				embedded = append(embedded, typeName)
			}
		}
	}

	return embedded
}

// extractInterfaceEmbeds extracts embedded interface names from an interface_type node.
//
// Description:
//
//	Walks the children of an interface_type AST node and collects embedded interface
//	names. In Go, interface children are either method_elem (method declarations) or
//	type_identifier/qualified_type (embedded interfaces). This function collects the
//	latter, analogous to extractEmbeddedTypes for struct nodes.
//
// Inputs:
//   - ifaceNode: A tree-sitter node of type "interface_type". Must not be nil.
//   - content: Raw source bytes for extracting text from AST nodes.
//
// Outputs:
//   - []string: Names of embedded interfaces (e.g., ["Reader", "Writer", "Stringer"]).
//     Empty slice if no embedded interfaces found.
//
// Example:
//
//	For `type ReadWriter interface { Reader; Writer; Close() error }`,
//	returns ["Reader", "Writer"]. Close() is a method_elem, not an embed.
//
// Limitations:
//   - Single-file analysis: cannot resolve cross-package interface methods.
//     The builder handles transitive method resolution via EMBEDS edges.
//
// Assumptions:
//   - ifaceNode is a valid interface_type node from tree-sitter.
//
// Thread Safety: This method is safe for concurrent use.
func (p *GoParser) extractInterfaceEmbeds(ifaceNode *sitter.Node, content []byte) []string {
	if ifaceNode == nil || ifaceNode.Type() != "interface_type" {
		return nil
	}

	var embedded []string

	contentLen := len(content)

	for i := 0; i < int(ifaceNode.ChildCount()); i++ {
		child := ifaceNode.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "type_elem":
			// tree-sitter wraps embedded interfaces in a type_elem node.
			// Walk its children for type_identifier, qualified_type, or pointer_type.
			for j := 0; j < int(child.ChildCount()); j++ {
				inner := child.Child(j)
				if inner == nil {
					continue
				}
				switch inner.Type() {
				case "type_identifier":
					// CR-20-2: Bounds check prevents panic if AST node range exceeds content.
					if int(inner.EndByte()) > contentLen {
						continue
					}
					name := string(content[inner.StartByte():inner.EndByte()])
					if name != "" {
						embedded = append(embedded, name)
					}
				case "qualified_type":
					name := p.extractQualifiedTypeName(inner, content)
					if name != "" {
						embedded = append(embedded, name)
					}
				case "pointer_type":
					name := p.unwrapPointerType(inner, content)
					if name != "" {
						embedded = append(embedded, name)
					}
				}
			}
		case "type_identifier":
			// Fallback: some tree-sitter versions may place embeds directly.
			// CR-20-2: Bounds check prevents panic if AST node range exceeds content.
			if int(child.EndByte()) > contentLen {
				continue
			}
			name := string(content[child.StartByte():child.EndByte()])
			if name != "" {
				embedded = append(embedded, name)
			}
		case "qualified_type":
			name := p.extractQualifiedTypeName(child, content)
			if name != "" {
				embedded = append(embedded, name)
			}
		case "pointer_type":
			name := p.unwrapPointerType(child, content)
			if name != "" {
				embedded = append(embedded, name)
			}
		}
		// Skip method_elem, "{", "}", "\n", comment nodes — they are not embeds
	}

	return embedded
}

// extractEmbeddedTypeName extracts the type name from an embedded field declaration.
//
// Description:
//
//	Handles plain types (RouterGroup), pointer types (*RouterGroup),
//	and qualified types (sync.Mutex, *sync.Mutex).
//
// Inputs:
//   - fieldNode: A field_declaration node with no field_identifier.
//   - content: Raw source bytes.
//
// Outputs:
//   - string: The type name (e.g., "RouterGroup", "Mutex"). Empty if not found.
func (p *GoParser) extractEmbeddedTypeName(fieldNode *sitter.Node, content []byte) string {
	for i := 0; i < int(fieldNode.ChildCount()); i++ {
		child := fieldNode.Child(i)
		switch child.Type() {
		case "type_identifier":
			return string(content[child.StartByte():child.EndByte()])
		case "pointer_type":
			// *SomeType — unwrap the pointer to get the type name
			return p.unwrapPointerType(child, content)
		case "qualified_type":
			// pkg.Type — extract just the type name (last identifier)
			return p.extractQualifiedTypeName(child, content)
		}
	}
	return ""
}

// unwrapPointerType extracts the type name from a pointer_type node (*T or *pkg.T).
func (p *GoParser) unwrapPointerType(node *sitter.Node, content []byte) string {
	contentLen := len(content)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "type_identifier":
			// CR-20-2 R-5: Bounds check on content slicing.
			if int(child.EndByte()) > contentLen {
				continue
			}
			return string(content[child.StartByte():child.EndByte()])
		case "qualified_type":
			return p.extractQualifiedTypeName(child, content)
		}
	}
	return ""
}

// extractQualifiedTypeName extracts the type name from a qualified_type node (pkg.Type).
func (p *GoParser) extractQualifiedTypeName(node *sitter.Node, content []byte) string {
	// qualified_type has children: package_identifier "." type_identifier
	// Return the type_identifier (last part).
	contentLen := len(content)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "type_identifier" {
			// CR-20-2 R-5: Bounds check on content slicing.
			if int(child.EndByte()) > contentLen {
				continue
			}
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	return ""
}

// extractMethodSpec extracts interface method specifications.
func (p *GoParser) extractMethodSpec(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var params string
	var returns string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "field_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "parameter_list":
			if params == "" {
				params = string(content[child.StartByte():child.EndByte()])
			} else {
				returns = string(content[child.StartByte():child.EndByte()])
			}
		case "type_identifier", "pointer_type", "slice_type", "map_type":
			returns = string(content[child.StartByte():child.EndByte()])
		}
	}

	if name == "" {
		return nil
	}

	signature := fmt.Sprintf("%s%s", name, params)
	if returns != "" {
		signature += " " + returns
	}

	exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

	if !p.parseOptions.IncludePrivate && !exported {
		return nil
	}

	return &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindMethod,
		FilePath:  filePath,
		Language:  "go",
		Exported:  exported,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column + 1),
		EndCol:    int(node.EndPoint().Column + 1),
	}
}

// collectInterfaceMethods extracts method signatures from an interface type node.
//
// Description:
//
//	Parses an interface_type AST node and extracts method signature information
//	for use in interface implementation detection (GR-40). This collects method
//	names, parameter counts, and return counts for method-set matching.
//
// Inputs:
//   - node: A tree-sitter node of type "interface_type"
//   - content: The source file content as bytes
//
// Outputs:
//   - []MethodSignature: Slice of method signatures for the interface.
//     Empty slice if interface has no methods (empty interface).
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *GoParser) collectInterfaceMethods(node *sitter.Node, content []byte) []MethodSignature {
	if node == nil || node.Type() != "interface_type" {
		return nil
	}

	methods := make([]MethodSignature, 0)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "method_elem" {
			if sig := p.extractMethodSignature(child, content); sig != nil {
				methods = append(methods, *sig)
			}
		}
	}

	return methods
}

// extractMethodSignature extracts a MethodSignature from a method_elem node.
//
// Description:
//
//	Parses a method specification from an interface and extracts the method
//	name, parameter types, return types, and counts for interface implementation
//	detection.
//
// Inputs:
//   - node: A tree-sitter node of type "method_elem"
//   - content: The source file content as bytes
//
// Outputs:
//   - *MethodSignature: The extracted signature, or nil if extraction fails
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *GoParser) extractMethodSignature(node *sitter.Node, content []byte) *MethodSignature {
	var name string
	var params string
	var returns string
	paramCount := 0
	returnCount := 0

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "field_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "parameter_list":
			plist := string(content[child.StartByte():child.EndByte()])
			if params == "" {
				params = plist
				// Count parameters (excluding empty parens)
				paramCount = countParameters(child)
			} else {
				returns = plist
				returnCount = countParameters(child)
			}
		case "type_identifier", "pointer_type", "slice_type", "map_type", "qualified_type", "interface_type", "struct_type", "function_type", "channel_type":
			// Single return type
			returns = string(content[child.StartByte():child.EndByte()])
			returnCount = 1
		}
	}

	if name == "" {
		return nil
	}

	return &MethodSignature{
		Name:        name,
		Params:      normalizeParams(params),
		Returns:     normalizeParams(returns),
		ParamCount:  paramCount,
		ReturnCount: returnCount,
	}
}

// countParameters counts the number of parameters in a parameter_list node.
//
// Thread Safety: This function is safe for concurrent use.
func countParameters(paramList *sitter.Node) int {
	if paramList == nil {
		return 0
	}

	count := 0
	for i := 0; i < int(paramList.ChildCount()); i++ {
		child := paramList.Child(i)
		if child == nil {
			continue
		}
		// Each parameter_declaration or variadic_parameter_declaration is one parameter
		switch child.Type() {
		case "parameter_declaration", "variadic_parameter_declaration":
			// Count identifiers in the declaration (handles "a, b int" case)
			identCount := 0
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				if grandchild == nil {
					continue
				}
				if grandchild.Type() == "identifier" {
					identCount++
				}
			}
			// If no identifiers, it's an unnamed parameter (just type)
			if identCount == 0 {
				count++
			} else {
				count += identCount
			}
		}
	}
	return count
}

// normalizeParams removes parameter names, leaving only types.
//
// Description:
//
//	Currently returns the parameter list with outer parentheses removed.
//	Full normalization (extracting only type names) is deferred to Phase 2.
//
// Inputs:
//   - params: Parameter list string, e.g., "(ctx context.Context, req *Request)"
//
// Outputs:
//   - string: Normalized parameters, e.g., "ctx context.Context, req *Request"
//
// TODO(GR-40-phase2): Add proper type-only normalization.
//
//	Input: "(ctx context.Context, req *Request)"
//	Expected Output: "context.Context, *Request"
//
// Thread Safety: This function is safe for concurrent use.
func normalizeParams(params string) string {
	// Remove outer parentheses
	params = strings.TrimPrefix(params, "(")
	params = strings.TrimSuffix(params, ")")
	params = strings.TrimSpace(params)

	if params == "" {
		return ""
	}

	// For Phase 1, we keep the full parameter list
	// TODO(GR-40-phase2): Extract only type names for signature matching
	return params
}

// associateMethodsWithTypes populates Metadata.Methods on type symbols with their methods.
//
// Description:
//
//	After all symbols are extracted, this function associates methods with their
//	receiver types by matching receiver type names. This enables interface
//	implementation detection via method-set matching (GR-40).
//
// Inputs:
//   - result: The ParseResult with all extracted symbols
//
// Thread Safety:
//
//	This method modifies result.Symbols in place. Not safe for concurrent use
//	on the same ParseResult, but safe when each goroutine has its own result.
func (p *GoParser) associateMethodsWithTypes(result *ParseResult) {
	if result == nil || len(result.Symbols) == 0 {
		return
	}

	// Build map of type names to their symbols (for structs and type aliases)
	typesByName := make(map[string]*Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindStruct || sym.Kind == SymbolKindType {
			typesByName[sym.Name] = sym
		}
	}

	if len(typesByName) == 0 {
		return
	}

	// Group methods by their receiver type
	methodsByType := make(map[string][]MethodSignature)

	for _, sym := range result.Symbols {
		if sym.Kind != SymbolKindMethod {
			continue
		}

		// Extract receiver type name from signature
		// Signature format: "func (r *Type) Name(params) returns"
		receiverType, isPointer := extractReceiverTypeName(sym.Signature)
		if receiverType == "" {
			continue
		}

		// Parse the method signature for parameter/return counts
		sig := parseMethodSignatureFromSymbol(sym, isPointer)
		if sig == nil {
			continue
		}

		methodsByType[receiverType] = append(methodsByType[receiverType], *sig)
	}

	// Associate methods with their types
	for typeName, methods := range methodsByType {
		if typeSym, ok := typesByName[typeName]; ok {
			if typeSym.Metadata == nil {
				typeSym.Metadata = &SymbolMetadata{}
			}
			typeSym.Metadata.Methods = methods
		}
	}
}

// extractReceiverTypeName extracts the type name from a method signature.
// Input: "func (r *Handler) DoWork(ctx context.Context) error"
// Output: ("Handler", true) - true indicates pointer receiver
func extractReceiverTypeName(signature string) (typeName string, isPointer bool) {
	if signature == "" || !strings.HasPrefix(signature, "func ") {
		return "", false
	}

	// Remove "func " prefix
	rest := strings.TrimPrefix(signature, "func ")

	// Method signatures have receiver immediately after "func "
	// Format: "(receiver) MethodName(params)"
	// Function signatures have name immediately after "func "
	// Format: "FuncName(params)"
	if !strings.HasPrefix(rest, "(") {
		// This is a function, not a method
		return "", false
	}

	// Find the receiver part: "(r *Handler) ..."
	end := strings.Index(rest, ")")
	if end == -1 {
		return "", false
	}

	receiver := rest[1:end]
	// receiver is now like "r *Handler" or "h Handler" or "*Handler"

	// Remove the variable name by finding the type
	parts := strings.Fields(receiver)
	if len(parts) == 0 {
		return "", false
	}

	// Last part should be the type (or *Type)
	typeStr := parts[len(parts)-1]

	// Check for pointer
	isPointer = strings.HasPrefix(typeStr, "*")
	typeName = strings.TrimPrefix(typeStr, "*")

	return typeName, isPointer
}

// parseMethodSignatureFromSymbol creates a MethodSignature from a Method symbol.
func parseMethodSignatureFromSymbol(sym *Symbol, isPointer bool) *MethodSignature {
	if sym == nil || sym.Kind != SymbolKindMethod {
		return nil
	}

	// Parse params and returns from the signature
	// Signature format: "func (r *Type) Name(params) returns"
	signature := sym.Signature

	// Find the method name's parameter list (after the receiver)
	// First, skip past the receiver
	afterReceiver := strings.Index(signature, ")")
	if afterReceiver == -1 {
		return nil
	}

	rest := signature[afterReceiver+1:]

	// Find the method's parameter list
	paramStart := strings.Index(rest, "(")
	if paramStart == -1 {
		return nil
	}

	// Count parentheses to find matching close
	depth := 0
	paramEnd := -1
	for i := paramStart; i < len(rest); i++ {
		if rest[i] == '(' {
			depth++
		} else if rest[i] == ')' {
			depth--
			if depth == 0 {
				paramEnd = i
				break
			}
		}
	}

	if paramEnd == -1 {
		return nil
	}

	params := rest[paramStart+1 : paramEnd]
	returns := strings.TrimSpace(rest[paramEnd+1:])

	// Count parameters
	paramCount := countParamString(params)
	returnCount := countReturnString(returns)

	receiverType := ""
	if isPointer {
		receiverType = "*" + sym.Receiver
	} else {
		receiverType = sym.Receiver
	}

	// Extract receiver type from signature if not in Receiver field
	if sym.Receiver == "" {
		typeName, ptr := extractReceiverTypeName(signature)
		if ptr {
			receiverType = "*" + typeName
		} else {
			receiverType = typeName
		}
	}

	return &MethodSignature{
		Name:         sym.Name,
		Params:       normalizeParams("(" + params + ")"),
		Returns:      returns,
		ParamCount:   paramCount,
		ReturnCount:  returnCount,
		ReceiverType: receiverType,
	}
}

// countParamString counts parameters from a parameter string (without parentheses).
//
// Description:
//
//	Counts the number of parameters in a Go parameter string by counting
//	comma separators at depth 0 (handling nested types like func(int, int)).
//
// Inputs:
//   - params: Parameter string without outer parentheses, e.g., "a int, b string"
//
// Outputs:
//   - int: Number of parameters. Returns 0 for empty string.
//
// Examples:
//   - "" → 0
//   - "a int" → 1
//   - "a, b int" → 2
//   - "fn func(int, int)" → 1 (nested parens ignored)
//
// Thread Safety: This function is safe for concurrent use.
func countParamString(params string) int {
	params = strings.TrimSpace(params)
	if params == "" {
		return 0
	}

	// Split by comma, but account for nested types like func(int, int)
	count := 0
	depth := 0
	for _, r := range params {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				count++
			}
		}
	}
	// Number of parameters is number of commas + 1
	return count + 1
}

// countReturnString counts return values from a return string.
//
// Description:
//
//	Counts the number of return values in a Go return type string.
//	Handles both single returns and multiple returns wrapped in parentheses.
//
// Inputs:
//   - returns: Return type string, e.g., "error" or "(int, error)"
//
// Outputs:
//   - int: Number of return values. Returns 0 for empty string.
//
// Examples:
//   - "" → 0
//   - "error" → 1
//   - "(int, error)" → 2
//   - "(a, b, c int)" → 3
//
// Thread Safety: This function is safe for concurrent use.
func countReturnString(returns string) int {
	returns = strings.TrimSpace(returns)
	if returns == "" {
		return 0
	}

	// If wrapped in parentheses, it's multiple returns
	if strings.HasPrefix(returns, "(") && strings.HasSuffix(returns, ")") {
		inner := returns[1 : len(returns)-1]
		return countParamString(inner)
	}

	// Single return value
	return 1
}

// extractVariables extracts top-level variable declarations.
func (p *GoParser) extractVariables(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "var_declaration" {
			p.processVarDecl(child, content, filePath, result, root, SymbolKindVariable)
		}
	}
}

// extractConstants extracts top-level constant declarations.
func (p *GoParser) extractConstants(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "const_declaration" {
			p.processVarDecl(child, content, filePath, result, root, SymbolKindConstant)
		}
	}
}

// processVarDecl processes variable or constant declarations.
func (p *GoParser) processVarDecl(node *sitter.Node, content []byte, filePath string, result *ParseResult, root *sitter.Node, kind SymbolKind) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "var_spec", "const_spec":
			p.processVarSpec(child, content, filePath, result, root, kind, node)
		case "var_spec_list", "const_spec_list":
			for j := 0; j < int(child.ChildCount()); j++ {
				spec := child.Child(j)
				if spec.Type() == "var_spec" || spec.Type() == "const_spec" {
					p.processVarSpec(spec, content, filePath, result, root, kind, node)
				}
			}
		}
	}
}

// processVarSpec processes a single variable or constant specification.
func (p *GoParser) processVarSpec(node *sitter.Node, content []byte, filePath string, result *ParseResult, root *sitter.Node, kind SymbolKind, parentDecl *sitter.Node) {
	var names []string
	var typeStr string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			names = append(names, string(content[child.StartByte():child.EndByte()]))
		case "type_identifier", "pointer_type", "slice_type", "map_type", "channel_type", "qualified_type":
			typeStr = string(content[child.StartByte():child.EndByte()])
		}
	}

	for _, name := range names {
		exported := len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'

		if !p.parseOptions.IncludePrivate && !exported {
			continue
		}

		sym := &Symbol{
			ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
			Name:      name,
			Kind:      kind,
			FilePath:  filePath,
			Language:  "go",
			Exported:  exported,
			Signature: typeStr,
			StartLine: int(node.StartPoint().Row + 1),
			EndLine:   int(node.EndPoint().Row + 1),
			StartCol:  int(node.StartPoint().Column + 1),
			EndCol:    int(node.EndPoint().Column + 1),
		}

		// Get doc comment
		sym.DocComment = p.getPrecedingComment(root, parentDecl, content)

		result.Symbols = append(result.Symbols, sym)
	}
}

// getPrecedingComment extracts the doc comment immediately before a node.
func (p *GoParser) getPrecedingComment(root *sitter.Node, node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}

	nodeStartLine := int(node.StartPoint().Row)

	// Search siblings for comment immediately preceding this node
	for i := 0; i < int(root.ChildCount()); i++ {
		sibling := root.Child(i)
		if sibling.Type() == "comment" {
			commentEndLine := int(sibling.EndPoint().Row)
			// Comment must be on line immediately before node
			if commentEndLine == nodeStartLine-1 || commentEndLine == nodeStartLine {
				return strings.TrimSpace(string(content[sibling.StartByte():sibling.EndByte()]))
			}
		}
	}

	return ""
}

// extractCallSites extracts all function and method calls from a function body.
//
// Description:
//
//	Traverses the AST of a function or method body to find all call_expression
//	nodes. For each call, it extracts the target name, location, and whether
//	it's a method call. This enables the graph builder to create EdgeTypeCalls
//	edges for the find_callers and find_callees CRS tools.
//
// Inputs:
//   - ctx: Context for cancellation. Checked every 100 nodes for responsiveness.
//   - bodyNode: The block node representing the function body. May be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - []CallSite: Extracted call sites. Empty slice if bodyNode is nil or no calls found.
//     Limited to MaxCallSitesPerSymbol (1000) to prevent memory exhaustion.
//
// Limitations:
//   - Does not resolve call targets to symbol IDs (that's the graph builder's job)
//   - Cannot detect calls made through function pointers or interface values
//   - Limited to MaxCallExpressionDepth (50) nesting depth
//
// Thread Safety:
//
//	This method is safe for concurrent use. Each call operates on its own
//	node traversal state.
//
// Example:
//
//	calls := p.extractCallSites(ctx, bodyNode, content, "main.go")
//	for _, call := range calls {
//	    fmt.Printf("Call to %s at line %d\n", call.Target, call.Location.StartLine)
//	}
func (p *GoParser) extractCallSites(ctx context.Context, bodyNode *sitter.Node, content []byte, filePath string) []CallSite {
	if bodyNode == nil {
		return nil
	}

	// Check context early
	if ctx.Err() != nil {
		return nil
	}

	// GR-41: OTel tracing for observability
	ctx, span := tracer.Start(ctx, "GoParser.extractCallSites")
	defer span.End()

	calls := make([]CallSite, 0, 16) // Pre-allocate for typical function

	// Iterative traversal with depth limiting
	type stackEntry struct {
		node  *sitter.Node
		depth int
	}

	stack := make([]stackEntry, 0, 64)
	stack = append(stack, stackEntry{node: bodyNode, depth: 0})

	nodeCount := 0
	for len(stack) > 0 {
		// Pop from stack
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		node := entry.node
		if node == nil {
			continue
		}

		// Check depth limit
		if entry.depth > MaxCallExpressionDepth {
			slog.Debug("GR-41: Max call expression depth reached",
				slog.String("file", filePath),
				slog.Int("depth", entry.depth),
			)
			continue
		}

		// Check context periodically for cancellation
		nodeCount++
		if nodeCount%100 == 0 {
			if ctx.Err() != nil {
				slog.Debug("GR-41: Context cancelled during call extraction",
					slog.String("file", filePath),
					slog.Int("calls_found", len(calls)),
				)
				return calls
			}
		}

		// Check call limit
		if len(calls) >= MaxCallSitesPerSymbol {
			slog.Warn("GR-41: Max call sites per symbol reached",
				slog.String("file", filePath),
				slog.Int("limit", MaxCallSitesPerSymbol),
			)
			return calls
		}

		// Process call expressions
		if node.Type() == "call_expression" {
			call := p.extractSingleCallSite(node, content, filePath)
			if call != nil && call.Target != "" {
				calls = append(calls, *call)
			}
		}

		// Add children to stack (in reverse order to process left-to-right)
		childCount := int(node.ChildCount())
		for i := childCount - 1; i >= 0; i-- {
			child := node.Child(i)
			if child != nil {
				stack = append(stack, stackEntry{
					node:  child,
					depth: entry.depth + 1,
				})
			}
		}
	}

	// GR-41: Record span attributes for observability
	span.SetAttributes(
		attribute.String("file", filePath),
		attribute.Int("calls_found", len(calls)),
		attribute.Int("nodes_traversed", nodeCount),
	)

	return calls
}

// extractSingleCallSite extracts call information from a call_expression node.
//
// Description:
//
//	Parses a single call_expression node to extract the function/method name,
//	location, and receiver information. Handles both simple function calls
//	(e.g., "DoWork()") and method calls (e.g., "obj.Method()").
//
// Inputs:
//   - node: A call_expression node from tree-sitter. Must not be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - *CallSite: The extracted call site, or nil if extraction fails.
//
// Thread Safety: This function is safe for concurrent use.
func (p *GoParser) extractSingleCallSite(node *sitter.Node, content []byte, filePath string) *CallSite {
	if node == nil || node.Type() != "call_expression" {
		return nil
	}

	// The function/method being called is typically the first child
	// call_expression has children: function_node, argument_list
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil && node.ChildCount() > 0 {
		// Fallback: first child might be the function
		funcNode = node.Child(0)
	}

	if funcNode == nil {
		return nil
	}

	call := &CallSite{
		Location: Location{
			FilePath:  filePath,
			StartLine: int(node.StartPoint().Row) + 1,
			EndLine:   int(node.EndPoint().Row) + 1,
			StartCol:  int(node.StartPoint().Column),
			EndCol:    int(node.EndPoint().Column),
		},
	}

	switch funcNode.Type() {
	case "identifier":
		// Simple function call: FunctionName(args)
		call.Target = string(content[funcNode.StartByte():funcNode.EndByte()])
		call.IsMethod = false

	case "selector_expression":
		// Method call or qualified call: receiver.Method(args) or pkg.Function(args)
		// selector_expression has: operand, field
		operand := funcNode.ChildByFieldName("operand")
		field := funcNode.ChildByFieldName("field")

		if field != nil {
			call.Target = string(content[field.StartByte():field.EndByte()])
		}

		if operand != nil {
			receiver := string(content[operand.StartByte():operand.EndByte()])
			call.Receiver = receiver
			// It's a method call if the receiver looks like a variable (starts with lowercase)
			// or is a complex expression. Package references typically use CamelCase.
			// We'll mark it as a method call and let the graph builder resolve it.
			call.IsMethod = true
		}

	case "parenthesized_expression":
		// Call on a parenthesized expression: (getFunc())(args)
		// Extract what we can
		text := string(content[funcNode.StartByte():funcNode.EndByte()])
		call.Target = text
		call.IsMethod = false

	default:
		// Other cases: type assertions, index expressions, etc.
		// Just extract the text as target
		call.Target = string(content[funcNode.StartByte():funcNode.EndByte()])
	}

	// Validate the extracted call
	if call.Target == "" {
		return nil
	}

	return call
}

// Compile-time interface compliance check.
var _ Parser = (*GoParser)(nil)
