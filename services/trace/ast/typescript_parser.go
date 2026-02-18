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
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// maxDecoratorArgDepth is the maximum recursion depth for walking decorator arguments.
	maxDecoratorArgDepth = 5
)

// TypeScriptParserOption configures a TypeScriptParser instance.
type TypeScriptParserOption func(*TypeScriptParser)

// WithTypeScriptMaxFileSize sets the maximum file size the parser will accept.
//
// Parameters:
//   - bytes: Maximum file size in bytes. Must be positive.
//
// Example:
//
//	parser := NewTypeScriptParser(WithTypeScriptMaxFileSize(5 * 1024 * 1024)) // 5MB limit
func WithTypeScriptMaxFileSize(bytes int64) TypeScriptParserOption {
	return func(p *TypeScriptParser) {
		if bytes > 0 {
			p.maxFileSize = bytes
		}
	}
}

// WithTypeScriptParseOptions applies the given ParseOptions to the parser.
//
// Parameters:
//   - opts: ParseOptions to apply.
//
// Example:
//
//	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: false}))
func WithTypeScriptParseOptions(opts ParseOptions) TypeScriptParserOption {
	return func(p *TypeScriptParser) {
		p.parseOptions = opts
	}
}

// TypeScriptParser implements the Parser interface for TypeScript source code.
//
// Description:
//
//	TypeScriptParser uses tree-sitter to parse TypeScript source files and extract symbols.
//	It supports concurrent use from multiple goroutines - each Parse call
//	creates its own tree-sitter parser instance internally.
//
// Thread Safety:
//
//	TypeScriptParser instances are safe for concurrent use. Multiple goroutines
//	may call Parse simultaneously on the same TypeScriptParser instance.
//
// Example:
//
//	parser := NewTypeScriptParser()
//	result, err := parser.Parse(ctx, []byte("export function hello(): string { return 'hi'; }"), "main.ts")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, sym := range result.Symbols {
//	    fmt.Printf("%s: %s\n", sym.Kind, sym.Name)
//	}
type TypeScriptParser struct {
	maxFileSize  int64
	parseOptions ParseOptions
}

// NewTypeScriptParser creates a new TypeScriptParser with the given options.
//
// Description:
//
//	Creates a TypeScriptParser configured with sensible defaults. Options can be
//	provided to customize behavior such as maximum file size.
//
// Inputs:
//   - opts: Optional configuration functions (WithTypeScriptMaxFileSize, WithTypeScriptParseOptions)
//
// Outputs:
//   - *TypeScriptParser: Configured parser instance, never nil
//
// Example:
//
//	// Default configuration
//	parser := NewTypeScriptParser()
//
//	// Custom max file size
//	parser := NewTypeScriptParser(WithTypeScriptMaxFileSize(5 * 1024 * 1024))
//
// Thread Safety:
//
//	The returned TypeScriptParser is safe for concurrent use.
func NewTypeScriptParser(opts ...TypeScriptParserOption) *TypeScriptParser {
	p := &TypeScriptParser{
		maxFileSize:  DefaultMaxFileSize,
		parseOptions: DefaultParseOptions(),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Parse extracts symbols from TypeScript source code.
//
// Description:
//
//	Parse uses tree-sitter to parse the provided TypeScript source code and extract
//	all symbols (functions, classes, interfaces, etc.) into a ParseResult.
//	The parser is error-tolerant and will return partial results for syntactically
//	invalid code.
//
// Inputs:
//   - ctx: Context for cancellation. Checked before and after parsing.
//     Note: Tree-sitter parsing itself cannot be interrupted mid-parse.
//   - content: Raw TypeScript source code bytes. Must be valid UTF-8.
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
//	result, err := parser.Parse(ctx, []byte("export const x = 1;"), "main.ts")
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Found %d symbols\n", len(result.Symbols))
//
// Limitations:
//   - Tree-sitter parsing is synchronous and cannot be interrupted mid-parse
//   - Very large files may take significant time to parse
//   - Some edge cases in TypeScript syntax may not be fully handled
//
// Assumptions:
//   - Content is valid UTF-8 (validated internally)
//   - FilePath uses forward slashes as path separator
//   - FilePath does not contain path traversal sequences
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *TypeScriptParser) Parse(ctx context.Context, content []byte, filePath string) (*ParseResult, error) {
	// Start tracing span
	ctx, span := startParseSpan(ctx, "typescript", filePath, len(content))
	defer span.End()

	start := time.Now()

	// Check context before starting
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled before start: %w", err)
	}

	// Validate file size
	if int64(len(content)) > p.maxFileSize {
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
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
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
		return nil, fmt.Errorf("%w: content is not valid UTF-8", ErrInvalidContent)
	}

	// Compute hash before parsing (captures input)
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:])

	// Create tree-sitter parser (new instance per call for thread safety)
	parser := sitter.NewParser()

	// Use TSX grammar for .tsx files, TypeScript grammar otherwise
	if strings.HasSuffix(filePath, ".tsx") {
		parser.SetLanguage(tsx.GetLanguage())
	} else {
		parser.SetLanguage(typescript.GetLanguage())
	}

	// Parse the content
	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
		return nil, fmt.Errorf("tree-sitter parse failed: %w", err)
	}
	defer tree.Close()

	// Check context after parsing
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled after tree-sitter: %w", err)
	}

	// Build result
	result := &ParseResult{
		FilePath:      filePath,
		Language:      "typescript",
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

	// Extract imports
	p.extractImports(rootNode, content, filePath, result)

	// Extract declarations (functions, classes, interfaces, types, enums, variables)
	p.extractDeclarations(ctx, rootNode, content, filePath, result)

	// Validate result before returning
	if err := result.Validate(); err != nil {
		recordParseMetrics(ctx, "typescript", time.Since(start), 0, false)
		return nil, fmt.Errorf("result validation failed: %w", err)
	}

	// Check context one final time
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "typescript", time.Since(start), len(result.Symbols), false)
		return nil, fmt.Errorf("parse canceled after extraction: %w", err)
	}

	// Record successful parse metrics
	setParseSpanResult(span, len(result.Symbols), len(result.Errors))
	recordParseMetrics(ctx, "typescript", time.Since(start), len(result.Symbols), true)

	return result, nil
}

// Language returns the canonical language name for this parser.
//
// Returns:
//   - "typescript" for TypeScript source files
func (p *TypeScriptParser) Language() string {
	return "typescript"
}

// Extensions returns the file extensions this parser handles.
//
// Returns:
//   - []string{".ts", ".tsx", ".mts", ".cts"} for TypeScript source files
func (p *TypeScriptParser) Extensions() []string {
	return []string{".ts", ".tsx", ".mts", ".cts"}
}

// extractImports extracts import statements from the AST.
func (p *TypeScriptParser) extractImports(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "import_statement":
			p.processImportStatement(child, content, filePath, result)
		case "lexical_declaration":
			// Check for CommonJS require
			p.processCommonJSRequire(child, content, filePath, result)
		}
	}
}

// processImportStatement handles ES module import statements.
func (p *TypeScriptParser) processImportStatement(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	var modulePath string
	var names []string
	var alias string
	var isDefault bool
	var isNamespace bool
	var isTypeOnly bool

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type":
			// import type { ... }
			isTypeOnly = true
		case "import_clause":
			p.processImportClause(child, content, &names, &alias, &isDefault, &isNamespace)
		case "string":
			modulePath = p.extractStringContent(child, content)
		}
	}

	if modulePath == "" {
		return
	}

	startLine := int(node.StartPoint().Row + 1)
	endLine := int(node.EndPoint().Row + 1)
	startCol := int(node.StartPoint().Column)
	endCol := int(node.EndPoint().Column)

	imp := Import{
		Path:        modulePath,
		Names:       names,
		Alias:       alias,
		IsDefault:   isDefault,
		IsNamespace: isNamespace,
		IsTypeOnly:  isTypeOnly,
		IsModule:    true,
		Location: Location{
			FilePath:  filePath,
			StartLine: startLine,
			EndLine:   endLine,
			StartCol:  startCol,
			EndCol:    endCol,
		},
	}
	result.Imports = append(result.Imports, imp)

	// Also add as symbol
	sym := &Symbol{
		ID:        GenerateID(filePath, startLine, modulePath),
		Name:      modulePath,
		Kind:      SymbolKindImport,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  false,
		StartLine: startLine,
		EndLine:   endLine,
		StartCol:  startCol,
		EndCol:    endCol,
	}
	result.Symbols = append(result.Symbols, sym)
}

// processImportClause extracts import clause details.
func (p *TypeScriptParser) processImportClause(node *sitter.Node, content []byte, names *[]string, alias *string, isDefault *bool, isNamespace *bool) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			// Default import: import foo from 'bar'
			*alias = string(content[child.StartByte():child.EndByte()])
			*isDefault = true
		case "namespace_import":
			// Namespace import: import * as foo from 'bar'
			*isNamespace = true
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "identifier" {
					*alias = string(content[gc.StartByte():gc.EndByte()])
				}
			}
		case "named_imports":
			// Named imports: import { a, b } from 'bar'
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "import_specifier" {
					name := p.extractImportSpecifier(gc, content)
					if name != "" {
						*names = append(*names, name)
					}
				}
			}
		}
	}
}

// extractImportSpecifier extracts a single import specifier.
func (p *TypeScriptParser) extractImportSpecifier(node *sitter.Node, content []byte) string {
	var name, alias string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "identifier" {
			if name == "" {
				name = string(content[child.StartByte():child.EndByte()])
			} else {
				alias = string(content[child.StartByte():child.EndByte()])
			}
		}
	}
	if alias != "" {
		return name + " as " + alias
	}
	return name
}

// processCommonJSRequire handles const foo = require('bar') style imports.
func (p *TypeScriptParser) processCommonJSRequire(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			var name string
			var modulePath string

			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				switch gc.Type() {
				case "identifier":
					name = string(content[gc.StartByte():gc.EndByte()])
				case "call_expression":
					// Check if this is require()
					modulePath = p.extractRequireCall(gc, content)
				}
			}

			if modulePath != "" && name != "" {
				startLine := int(node.StartPoint().Row + 1)
				endLine := int(node.EndPoint().Row + 1)

				imp := Import{
					Path:       modulePath,
					Alias:      name,
					IsCommonJS: true,
					Location: Location{
						FilePath:  filePath,
						StartLine: startLine,
						EndLine:   endLine,
						StartCol:  int(node.StartPoint().Column),
						EndCol:    int(node.EndPoint().Column),
					},
				}
				result.Imports = append(result.Imports, imp)

				sym := &Symbol{
					ID:        GenerateID(filePath, startLine, modulePath),
					Name:      modulePath,
					Kind:      SymbolKindImport,
					FilePath:  filePath,
					Language:  "typescript",
					Exported:  false,
					StartLine: startLine,
					EndLine:   endLine,
					StartCol:  int(node.StartPoint().Column),
					EndCol:    int(node.EndPoint().Column),
				}
				result.Symbols = append(result.Symbols, sym)
			}
		}
	}
}

// extractRequireCall extracts the module path from a require() call.
func (p *TypeScriptParser) extractRequireCall(node *sitter.Node, content []byte) string {
	var funcName string
	var modulePath string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			funcName = string(content[child.StartByte():child.EndByte()])
		case "arguments":
			for j := 0; j < int(child.ChildCount()); j++ {
				arg := child.Child(j)
				if arg.Type() == "string" {
					modulePath = p.extractStringContent(arg, content)
				}
			}
		}
	}

	if funcName == "require" {
		return modulePath
	}
	return ""
}

// extractDeclarations extracts all top-level declarations.
func (p *TypeScriptParser) extractDeclarations(ctx context.Context, root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "export_statement":
			p.processExportStatement(ctx, child, content, filePath, result)
		case "function_declaration":
			if fn := p.processFunction(ctx, child, content, filePath, nil, false); fn != nil {
				result.Symbols = append(result.Symbols, fn)
			}
		case "class_declaration":
			if cls := p.processClass(ctx, child, content, filePath, nil, false); cls != nil {
				result.Symbols = append(result.Symbols, cls)
			}
		case "interface_declaration":
			if iface := p.processInterface(child, content, filePath, false); iface != nil {
				result.Symbols = append(result.Symbols, iface)
			}
		case "type_alias_declaration":
			if ta := p.processTypeAlias(child, content, filePath, false); ta != nil {
				result.Symbols = append(result.Symbols, ta)
			}
		case "enum_declaration":
			if enum := p.processEnum(child, content, filePath, false); enum != nil {
				result.Symbols = append(result.Symbols, enum)
			}
		case "lexical_declaration":
			p.processLexicalDeclaration(child, content, filePath, result, false)
		case "variable_declaration":
			p.processVariableDeclaration(child, content, filePath, result, false)
		}
	}
}

// processExportStatement handles export statements.
func (p *TypeScriptParser) processExportStatement(ctx context.Context, node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	var decorators []string
	var decoratorArgs map[string][]string
	isDefault := false

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "decorator":
			name, args := p.extractDecoratorNameAndArgs(child, content)
			if name != "" {
				decorators = append(decorators, name)
				if len(args) > 0 {
					if decoratorArgs == nil {
						decoratorArgs = make(map[string][]string)
					}
					decoratorArgs[name] = args
				}
			}
		case "default":
			isDefault = true
		case "function_declaration":
			if fn := p.processFunction(ctx, child, content, filePath, decorators, true); fn != nil {
				if isDefault {
					if fn.Metadata == nil {
						fn.Metadata = &SymbolMetadata{}
					}
				}
				// IT-03a A-3: Attach decorator arguments
				if len(decoratorArgs) > 0 {
					if fn.Metadata == nil {
						fn.Metadata = &SymbolMetadata{}
					}
					fn.Metadata.DecoratorArgs = decoratorArgs
				}
				result.Symbols = append(result.Symbols, fn)
			}
		case "class_declaration":
			if cls := p.processClass(ctx, child, content, filePath, decorators, true); cls != nil {
				// IT-03a A-3: Attach decorator arguments
				if len(decoratorArgs) > 0 {
					if cls.Metadata == nil {
						cls.Metadata = &SymbolMetadata{}
					}
					cls.Metadata.DecoratorArgs = decoratorArgs
				}
				result.Symbols = append(result.Symbols, cls)
			}
		case "interface_declaration":
			if iface := p.processInterface(child, content, filePath, true); iface != nil {
				result.Symbols = append(result.Symbols, iface)
			}
		case "type_alias_declaration":
			if ta := p.processTypeAlias(child, content, filePath, true); ta != nil {
				result.Symbols = append(result.Symbols, ta)
			}
		case "enum_declaration":
			if enum := p.processEnum(child, content, filePath, true); enum != nil {
				result.Symbols = append(result.Symbols, enum)
			}
		case "lexical_declaration":
			p.processLexicalDeclaration(child, content, filePath, result, true)
		case "abstract_class_declaration":
			if cls := p.processAbstractClass(ctx, child, content, filePath, decorators, true); cls != nil {
				result.Symbols = append(result.Symbols, cls)
			}
		case "string", "template_string":
			// IT-03a B-3: Re-export source module: export { Foo } from './bar'
			source := p.extractStringContent(child, content)
			if source != "" {
				result.Imports = append(result.Imports, Import{
					Path:       source,
					IsRelative: strings.HasPrefix(source, "."),
					Location: Location{
						FilePath:  filePath,
						StartLine: int(node.StartPoint().Row) + 1,
						EndLine:   int(node.EndPoint().Row) + 1,
						StartCol:  int(node.StartPoint().Column),
						EndCol:    int(node.EndPoint().Column),
					},
				})
			}
		}
	}
}

// processFunction extracts a function declaration.
func (p *TypeScriptParser) processFunction(ctx context.Context, node *sitter.Node, content []byte, filePath string, decorators []string, exported bool) *Symbol {
	var name string
	var typeParams []string
	var params string
	var returnType string
	var docstring string
	var isAsync bool
	var bodyNode *sitter.Node

	// Get preceding comment
	docstring = p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "async":
			isAsync = true
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		case "formal_parameters":
			params = string(content[child.StartByte():child.EndByte()])
		case "type_annotation":
			returnType = p.extractTypeAnnotation(child, content)
		case tsNodeStatementBlock:
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	// Build signature
	signature := "function " + name
	if len(typeParams) > 0 {
		signature += "<" + strings.Join(typeParams, ", ") + ">"
	}
	signature += params
	if returnType != "" {
		signature += ": " + returnType
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       SymbolKindFunction,
		FilePath:   filePath,
		Language:   "typescript",
		Exported:   exported,
		Signature:  signature,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
	}

	if len(decorators) > 0 || len(typeParams) > 0 || returnType != "" || isAsync {
		sym.Metadata = &SymbolMetadata{
			Decorators:     decorators,
			TypeParameters: typeParams,
			ReturnType:     returnType,
			IsAsync:        isAsync,
		}
	}

	// IT-03a C-2: Extract type arguments from return type
	if returnType != "" {
		typeArgs := extractTypeArgumentIdentifiers(returnType)
		if len(typeArgs) > 0 {
			if sym.Metadata == nil {
				sym.Metadata = &SymbolMetadata{}
			}
			sym.Metadata.TypeArguments = typeArgs
		}
	}

	// GR-41: Extract call sites from function body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)

		// IT-03a C-3: Extract type narrowing expressions (instanceof, type predicates)
		narrowings := p.extractTypeNarrowings(bodyNode, content)
		if len(narrowings) > 0 {
			if sym.Metadata == nil {
				sym.Metadata = &SymbolMetadata{}
			}
			sym.Metadata.TypeNarrowings = narrowings
		}
	}

	return sym
}

// processClass extracts a class declaration.
func (p *TypeScriptParser) processClass(ctx context.Context, node *sitter.Node, content []byte, filePath string, decorators []string, exported bool) *Symbol {
	var name string
	var typeParams []string
	var extends string
	var implements []string
	var bodyNode *sitter.Node
	var docstring string

	docstring = p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		case "class_heritage":
			extends, implements = p.extractClassHeritage(child, content)
		case "class_body":
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       SymbolKindClass,
		FilePath:   filePath,
		Language:   "typescript",
		Exported:   exported,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
		Children:   make([]*Symbol, 0),
	}

	if len(decorators) > 0 || len(typeParams) > 0 || extends != "" || len(implements) > 0 {
		sym.Metadata = &SymbolMetadata{
			Decorators:     decorators,
			TypeParameters: typeParams,
			Extends:        extends,
			Implements:     implements,
		}
	}

	// Extract class members
	if bodyNode != nil {
		p.extractClassMembers(ctx, bodyNode, content, filePath, sym)
	}

	return sym
}

// processAbstractClass extracts an abstract class declaration.
func (p *TypeScriptParser) processAbstractClass(ctx context.Context, node *sitter.Node, content []byte, filePath string, decorators []string, exported bool) *Symbol {
	sym := p.processClass(ctx, node, content, filePath, decorators, exported)
	if sym != nil {
		if sym.Metadata == nil {
			sym.Metadata = &SymbolMetadata{}
		}
		sym.Metadata.IsAbstract = true
	}
	return sym
}

// extractClassMembers extracts methods and fields from a class body.
func (p *TypeScriptParser) extractClassMembers(ctx context.Context, body *sitter.Node, content []byte, filePath string, classSym *Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		switch child.Type() {
		case "method_definition":
			if method := p.processMethod(ctx, child, content, filePath); method != nil {
				// IT-01 Phase C: Set Receiver to class name for graph builder receiver resolution
				method.Receiver = classSym.Name
				classSym.Children = append(classSym.Children, method)
			}
		case "public_field_definition":
			if field := p.processField(child, content, filePath); field != nil {
				classSym.Children = append(classSym.Children, field)
			}
		}
	}
}

// processMethod extracts a method definition.
func (p *TypeScriptParser) processMethod(ctx context.Context, node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var typeParams []string
	var params string
	var returnType string
	var accessModifier string
	var isAsync bool
	var isStatic bool
	var isAbstract bool
	var bodyNode *sitter.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "accessibility_modifier":
			accessModifier = string(content[child.StartByte():child.EndByte()])
		case "static":
			isStatic = true
		case "async":
			isAsync = true
		case "abstract":
			isAbstract = true
		case "property_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		case "formal_parameters":
			params = string(content[child.StartByte():child.EndByte()])
		case "type_annotation":
			returnType = p.extractTypeAnnotation(child, content)
		case tsNodeStatementBlock:
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	// Determine visibility
	exported := accessModifier != "private"

	// Build signature
	signature := name + params
	if returnType != "" {
		signature += ": " + returnType
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindMethod,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  exported,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
		Metadata: &SymbolMetadata{
			TypeParameters: typeParams,
			ReturnType:     returnType,
			IsAsync:        isAsync,
			IsStatic:       isStatic,
			IsAbstract:     isAbstract,
			AccessModifier: accessModifier,
		},
	}

	// GR-41: Extract call sites from method body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	return sym
}

// processField extracts a field definition.
func (p *TypeScriptParser) processField(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var typeStr string
	var accessModifier string
	var isReadonly bool
	var isStatic bool

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "accessibility_modifier":
			accessModifier = string(content[child.StartByte():child.EndByte()])
		case "readonly":
			isReadonly = true
		case "static":
			isStatic = true
		case "property_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_annotation":
			typeStr = p.extractTypeAnnotation(child, content)
		}
	}

	if name == "" {
		return nil
	}

	exported := accessModifier != "private"

	signature := name
	if typeStr != "" {
		signature += ": " + typeStr
	}
	if isReadonly {
		signature = "readonly " + signature
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindField,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  exported,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
		Metadata: &SymbolMetadata{
			IsStatic:       isStatic,
			AccessModifier: accessModifier,
		},
	}

	return sym
}

// processInterface extracts an interface declaration.
func (p *TypeScriptParser) processInterface(node *sitter.Node, content []byte, filePath string, exported bool) *Symbol {
	var name string
	var typeParams []string
	var bodyNode *sitter.Node
	var docstring string
	var extendsInterfaces []string

	docstring = p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		case "interface_body", "object_type":
			bodyNode = child
		case "extends_type_clause":
			// IT-03a A-2: Extract interface inheritance (interface Foo extends Bar, Baz)
			extendsInterfaces = p.extractInterfaceHeritage(child, content)
		}
	}

	if name == "" {
		return nil
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       SymbolKindInterface,
		FilePath:   filePath,
		Language:   "typescript",
		Exported:   exported,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
		Children:   make([]*Symbol, 0),
	}

	// Build metadata from type params and heritage
	hasMetadata := len(typeParams) > 0 || len(extendsInterfaces) > 0
	if hasMetadata {
		sym.Metadata = &SymbolMetadata{
			TypeParameters: typeParams,
		}
	}

	// IT-03a A-2: Populate Extends (first parent) and Implements (additional parents)
	// Same convention as Python multi-base: Extends=first, Implements=rest.
	if len(extendsInterfaces) > 0 {
		if sym.Metadata == nil {
			sym.Metadata = &SymbolMetadata{}
		}
		sym.Metadata.Extends = extendsInterfaces[0]
		if len(extendsInterfaces) > 1 {
			sym.Metadata.Implements = extendsInterfaces[1:]
		}
	}

	// Extract interface members
	if bodyNode != nil {
		p.extractInterfaceMembers(bodyNode, content, filePath, sym)
	}

	return sym
}

// extractInterfaceHeritage extracts parent interface names from an extends_type_clause.
//
// Description:
//
//	Handles TS interface inheritance: interface Foo extends Bar, Baz { }
//	Returns the list of parent interface names in declaration order.
//
// Inputs:
//   - node: An extends_type_clause node from tree-sitter.
//   - content: Source file bytes.
//
// Outputs:
//   - []string: Parent interface names. Empty if none found.
func (p *TypeScriptParser) extractInterfaceHeritage(node *sitter.Node, content []byte) []string {
	var parents []string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			parents = append(parents, string(content[child.StartByte():child.EndByte()]))
		case "generic_type":
			// interface Foo extends Bar<T> — extract the base name
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "type_identifier" {
					parents = append(parents, string(content[gc.StartByte():gc.EndByte()]))
					break
				}
			}
		}
	}
	return parents
}

// extractInterfaceMembers extracts properties and methods from an interface body.
func (p *TypeScriptParser) extractInterfaceMembers(body *sitter.Node, content []byte, filePath string, ifaceSym *Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		switch child.Type() {
		case "property_signature":
			if prop := p.processPropertySignature(child, content, filePath); prop != nil {
				ifaceSym.Children = append(ifaceSym.Children, prop)
			}
		case "method_signature":
			if method := p.processMethodSignature(child, content, filePath); method != nil {
				ifaceSym.Children = append(ifaceSym.Children, method)
			}
		}
	}
}

// processPropertySignature extracts an interface property.
func (p *TypeScriptParser) processPropertySignature(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var typeStr string
	var isReadonly bool
	var isOptional bool

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "readonly":
			isReadonly = true
		case "property_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "?":
			isOptional = true
		case "type_annotation":
			typeStr = p.extractTypeAnnotation(child, content)
		}
	}

	if name == "" {
		return nil
	}

	signature := name
	if isOptional {
		signature += "?"
	}
	if typeStr != "" {
		signature += ": " + typeStr
	}
	if isReadonly {
		signature = "readonly " + signature
	}

	return &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindField,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  true,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}
}

// processMethodSignature extracts an interface method signature.
func (p *TypeScriptParser) processMethodSignature(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var params string
	var returnType string
	var typeParams []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "property_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		case "formal_parameters":
			params = string(content[child.StartByte():child.EndByte()])
		case "type_annotation":
			returnType = p.extractTypeAnnotation(child, content)
		}
	}

	if name == "" {
		return nil
	}

	signature := name + params
	if returnType != "" {
		signature += ": " + returnType
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindMethod,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  true,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}

	if len(typeParams) > 0 || returnType != "" {
		sym.Metadata = &SymbolMetadata{
			TypeParameters: typeParams,
			ReturnType:     returnType,
		}
	}

	return sym
}

// processTypeAlias extracts a type alias declaration.
func (p *TypeScriptParser) processTypeAlias(node *sitter.Node, content []byte, filePath string, exported bool) *Symbol {
	var name string
	var typeParams []string
	var typeDef string
	var docstring string

	docstring = p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "type_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_parameters":
			typeParams = p.extractTypeParameters(child, content)
		default:
			// Capture type definition (after =)
			if child.Type() != "type" && child.Type() != "=" && child.Type() != ";" && typeDef == "" && name != "" {
				typeDef = string(content[child.StartByte():child.EndByte()])
			}
		}
	}

	if name == "" {
		return nil
	}

	signature := "type " + name
	if len(typeParams) > 0 {
		signature += "<" + strings.Join(typeParams, ", ") + ">"
	}
	if typeDef != "" {
		signature += " = " + typeDef
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       SymbolKindType,
		FilePath:   filePath,
		Language:   "typescript",
		Exported:   exported,
		Signature:  signature,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
	}

	if len(typeParams) > 0 {
		sym.Metadata = &SymbolMetadata{
			TypeParameters: typeParams,
		}
	}

	return sym
}

// processEnum extracts an enum declaration.
func (p *TypeScriptParser) processEnum(node *sitter.Node, content []byte, filePath string, exported bool) *Symbol {
	var name string
	var bodyNode *sitter.Node
	var docstring string

	docstring = p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "enum_body":
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       SymbolKindEnum,
		FilePath:   filePath,
		Language:   "typescript",
		Exported:   exported,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
		Children:   make([]*Symbol, 0),
	}

	// Extract enum members
	if bodyNode != nil {
		p.extractEnumMembers(bodyNode, content, filePath, sym)
	}

	return sym
}

// extractEnumMembers extracts members from an enum body.
func (p *TypeScriptParser) extractEnumMembers(body *sitter.Node, content []byte, filePath string, enumSym *Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		switch child.Type() {
		case "enum_assignment":
			if member := p.processEnumMember(child, content, filePath); member != nil {
				enumSym.Children = append(enumSym.Children, member)
			}
		case "property_identifier":
			// Simple enum member without value
			name := string(content[child.StartByte():child.EndByte()])
			member := &Symbol{
				ID:        GenerateID(filePath, int(child.StartPoint().Row+1), name),
				Name:      name,
				Kind:      SymbolKindEnumMember,
				FilePath:  filePath,
				Language:  "typescript",
				Exported:  true,
				StartLine: int(child.StartPoint().Row + 1),
				EndLine:   int(child.EndPoint().Row + 1),
				StartCol:  int(child.StartPoint().Column),
				EndCol:    int(child.EndPoint().Column),
			}
			enumSym.Children = append(enumSym.Children, member)
		}
	}
}

// processEnumMember extracts an enum member with assignment.
func (p *TypeScriptParser) processEnumMember(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var value string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "property_identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "string", "number":
			value = string(content[child.StartByte():child.EndByte()])
		}
	}

	if name == "" {
		return nil
	}

	signature := name
	if value != "" {
		signature += " = " + value
	}

	return &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindEnumMember,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  true,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}
}

// processLexicalDeclaration handles const/let declarations.
func (p *TypeScriptParser) processLexicalDeclaration(node *sitter.Node, content []byte, filePath string, result *ParseResult, exported bool) {
	var declKind string // const or let

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "const", "let":
			declKind = child.Type()
		case "variable_declarator":
			if variable := p.processVariableDeclarator(child, content, filePath, declKind, exported); variable != nil {
				result.Symbols = append(result.Symbols, variable)
			}
		}
	}
}

// processVariableDeclaration handles var declarations.
func (p *TypeScriptParser) processVariableDeclaration(node *sitter.Node, content []byte, filePath string, result *ParseResult, exported bool) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "variable_declarator" {
			if variable := p.processVariableDeclarator(child, content, filePath, "var", exported); variable != nil {
				result.Symbols = append(result.Symbols, variable)
			}
		}
	}
}

// processVariableDeclarator extracts a variable declarator.
func (p *TypeScriptParser) processVariableDeclarator(node *sitter.Node, content []byte, filePath string, declKind string, exported bool) *Symbol {
	var name string
	var typeStr string
	var hasArrowFunction bool

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "type_annotation":
			typeStr = p.extractTypeAnnotation(child, content)
		case "arrow_function":
			hasArrowFunction = true
		}
	}

	if name == "" {
		return nil
	}

	kind := SymbolKindVariable
	if declKind == "const" {
		kind = SymbolKindConstant
	}
	if hasArrowFunction {
		kind = SymbolKindFunction
	}

	signature := declKind + " " + name
	if typeStr != "" {
		signature += ": " + typeStr
	}

	return &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      kind,
		FilePath:  filePath,
		Language:  "typescript",
		Exported:  exported,
		Signature: signature,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}
}

// extractTypeParameters extracts type parameters from a type_parameters node.
func (p *TypeScriptParser) extractTypeParameters(node *sitter.Node, content []byte) []string {
	params := make([]string, 0)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "type_parameter" {
			param := string(content[child.StartByte():child.EndByte()])
			params = append(params, param)
		}
	}

	return params
}

// extractTypeAnnotation extracts the type from a type annotation.
func (p *TypeScriptParser) extractTypeAnnotation(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != ":" {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	return ""
}

// extractClassHeritage extracts extends and implements from class heritage.
func (p *TypeScriptParser) extractClassHeritage(node *sitter.Node, content []byte) (extends string, implements []string) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "extends_clause":
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				// Tree-sitter uses "identifier" for simple class names, "type_identifier" for type references
				if gc.Type() == "identifier" || gc.Type() == "type_identifier" || gc.Type() == "generic_type" {
					extends = string(content[gc.StartByte():gc.EndByte()])
				}
			}
		case "implements_clause":
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == "type_identifier" || gc.Type() == "generic_type" {
					implements = append(implements, string(content[gc.StartByte():gc.EndByte()]))
				}
			}
		}
	}
	return
}

// extractDecoratorName extracts the name from a decorator node.
func (p *TypeScriptParser) extractDecoratorName(node *sitter.Node, content []byte) string {
	name, _ := p.extractDecoratorNameAndArgs(node, content)
	return name
}

// extractDecoratorNameAndArgs extracts the name and argument identifiers from a decorator node.
//
// Description:
//
//	For simple decorators like @Injectable, returns ("Injectable", nil).
//	For call decorators like @UseInterceptors(LoggingInterceptor, AuthGuard),
//	returns ("UseInterceptors", ["LoggingInterceptor", "AuthGuard"]).
//	Only identifier arguments are extracted — string literals and complex expressions are skipped.
//
// IT-03a A-3: Enables EdgeTypeReferences from decorated symbol to decorator arguments.
func (p *TypeScriptParser) extractDecoratorNameAndArgs(node *sitter.Node, content []byte) (string, []string) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "identifier":
			return string(content[child.StartByte():child.EndByte()]), nil
		case "call_expression":
			var name string
			var args []string
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc == nil {
					continue
				}
				switch gc.Type() {
				case "identifier":
					if name == "" {
						name = string(content[gc.StartByte():gc.EndByte()])
					}
				case "member_expression":
					if name == "" {
						name = string(content[gc.StartByte():gc.EndByte()])
					}
				case "arguments":
					args = p.extractDecoratorArgIdentifiers(gc, content)
				}
			}
			return name, args
		}
	}
	return "", nil
}

// extractDecoratorArgIdentifiers extracts identifier arguments from a decorator's arguments node.
//
// Description:
//
//	Walks the arguments node and extracts top-level identifiers and member expressions.
//	Skips string literals, numbers, objects, arrays, and other non-identifier arguments.
//	Example: @Module({providers: [UserService]}) extracts ["UserService"].
func (p *TypeScriptParser) extractDecoratorArgIdentifiers(argsNode *sitter.Node, content []byte) []string {
	identifiers := make([]string, 0, argsNode.ChildCount())
	p.walkDecoratorArgs(argsNode, content, &identifiers, 0)
	return identifiers
}

// walkDecoratorArgs recursively walks decorator argument nodes to find identifiers.
//
// Description:
//
//	Traverses the AST of a decorator's arguments, recursing into containers
//	(arrays, objects, key-value pairs) to extract all identifier references.
//	Skips common JS keyword identifiers (true, false, null, undefined).
//
// Inputs:
//   - node: The current AST node to walk. May be nil.
//   - content: Raw source bytes of the file.
//   - identifiers: Accumulator for found identifier names. Must not be nil.
//   - depth: Current recursion depth. Stops at maxDecoratorArgDepth.
//
// Outputs:
//
//	None. Appends found identifiers to the identifiers slice.
//
// Limitations:
//   - Does not extract member expressions or complex expressions.
//   - Depth limited to maxDecoratorArgDepth (5) levels.
//
// Assumptions:
//   - identifiers pointer is non-nil.
func (p *TypeScriptParser) walkDecoratorArgs(node *sitter.Node, content []byte, identifiers *[]string, depth int) {
	if depth > maxDecoratorArgDepth || node == nil {
		return
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "identifier":
			name := string(content[child.StartByte():child.EndByte()])
			// Skip common non-symbol identifiers (JS keywords used as property names, etc.)
			if name != "true" && name != "false" && name != "null" && name != "undefined" {
				*identifiers = append(*identifiers, name)
			}
		case "array", "object", "pair":
			// Recurse into containers to find nested identifiers
			p.walkDecoratorArgs(child, content, identifiers, depth+1)
		}
	}
}

// extractStringContent extracts the content from a string node.
func (p *TypeScriptParser) extractStringContent(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string_fragment" {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	// Fallback: strip quotes from raw content
	raw := string(content[node.StartByte():node.EndByte()])
	return strings.Trim(raw, `"'`)
}

// getPrecedingComment extracts JSDoc or comment before a node.
func (p *TypeScriptParser) getPrecedingComment(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}

	// Look for comment node immediately before this one
	prev := node.PrevSibling()
	if prev != nil && prev.Type() == "comment" {
		comment := string(content[prev.StartByte():prev.EndByte()])
		// Check if it's a JSDoc comment
		if strings.HasPrefix(comment, "/**") {
			return comment
		}
	}

	// If this node is inside an export_statement, the comment may be
	// a sibling of the export_statement, not this declaration node.
	// Check parent's previous sibling.
	parent := node.Parent()
	if parent != nil && parent.Type() == "export_statement" {
		parentPrev := parent.PrevSibling()
		if parentPrev != nil && parentPrev.Type() == "comment" {
			comment := string(content[parentPrev.StartByte():parentPrev.EndByte()])
			if strings.HasPrefix(comment, "/**") {
				return comment
			}
		}
	}

	return ""
}

// extractCallSites extracts all function and method calls from a TypeScript function body.
//
// Description:
//
//	Traverses the AST of a TypeScript function or method body to find all
//	call_expression nodes. For each call, it extracts the target name, location,
//	and whether it's a method call (e.g., this.method(), obj.func()). This enables
//	the graph builder to create EdgeTypeCalls edges for find_callers/find_callees.
//
// Inputs:
//   - ctx: Context for cancellation. Checked every 100 nodes.
//   - bodyNode: The statement_block node representing the function body. May be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - []CallSite: Extracted call sites. Limited to MaxCallSitesPerSymbol (1000).
//
// Thread Safety: Safe for concurrent use.
func (p *TypeScriptParser) extractCallSites(ctx context.Context, bodyNode *sitter.Node, content []byte, filePath string) []CallSite {
	if bodyNode == nil {
		return nil
	}

	if ctx.Err() != nil {
		return nil
	}

	ctx, span := tracer.Start(ctx, "TypeScriptParser.extractCallSites")
	defer span.End()

	calls := make([]CallSite, 0, 16)

	type stackEntry struct {
		node  *sitter.Node
		depth int
	}

	stack := make([]stackEntry, 0, 64)
	stack = append(stack, stackEntry{node: bodyNode, depth: 0})

	nodeCount := 0
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		node := entry.node
		if node == nil {
			continue
		}

		if entry.depth > MaxCallExpressionDepth {
			slog.Debug("GR-41: Max call expression depth reached in TypeScript",
				slog.String("file", filePath),
				slog.Int("depth", entry.depth),
			)
			continue
		}

		nodeCount++
		if nodeCount%100 == 0 {
			if ctx.Err() != nil {
				slog.Debug("GR-41: Context cancelled during TypeScript call extraction",
					slog.String("file", filePath),
					slog.Int("calls_found", len(calls)),
				)
				return calls
			}
		}

		if len(calls) >= MaxCallSitesPerSymbol {
			slog.Warn("GR-41: Max call sites per symbol reached in TypeScript",
				slog.String("file", filePath),
				slog.Int("limit", MaxCallSitesPerSymbol),
			)
			return calls
		}

		// TypeScript tree-sitter uses "call_expression" for calls
		if node.Type() == tsNodeCallExpression {
			call := p.extractSingleCallSite(node, content, filePath)
			if call != nil && call.Target != "" {
				calls = append(calls, *call)
			}
		}

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

	span.SetAttributes(
		attribute.String("file", filePath),
		attribute.Int("calls_found", len(calls)),
		attribute.Int("nodes_traversed", nodeCount),
	)

	return calls
}

// extractSingleCallSite extracts call information from a TypeScript call_expression node.
//
// Description:
//
//	Parses a single call_expression node to extract the function/method name,
//	location, and receiver information. Handles:
//	  - Simple calls: func(args)
//	  - Method calls: this.method(args), obj.method(args)
//	  - Chained calls: obj.method1().method2(args)
//
// Inputs:
//   - node: A call_expression node from tree-sitter-typescript. Must not be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - *CallSite: The extracted call site, or nil if extraction fails.
//
// Thread Safety: Safe for concurrent use.
func (p *TypeScriptParser) extractSingleCallSite(node *sitter.Node, content []byte, filePath string) *CallSite {
	if node == nil || node.Type() != tsNodeCallExpression {
		return nil
	}

	funcNode := node.ChildByFieldName("function")
	if funcNode == nil && node.ChildCount() > 0 {
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
		// Simple function call: functionName(args)
		call.Target = string(content[funcNode.StartByte():funcNode.EndByte()])
		call.IsMethod = false

	case "member_expression":
		// Method call: obj.method(args) or this.method(args)
		objectNode := funcNode.ChildByFieldName("object")
		propertyNode := funcNode.ChildByFieldName("property")

		if propertyNode != nil {
			call.Target = string(content[propertyNode.StartByte():propertyNode.EndByte()])
		}

		if objectNode != nil {
			receiver := string(content[objectNode.StartByte():objectNode.EndByte()])
			call.Receiver = receiver
			call.IsMethod = true
		}

	default:
		text := string(content[funcNode.StartByte():funcNode.EndByte()])
		if len(text) > 100 {
			text = text[:100]
		}
		call.Target = text
	}

	if call.Target == "" {
		return nil
	}

	// IT-03a C-1: Extract identifier arguments (callback/HOF references)
	argsNode := node.ChildByFieldName("arguments")
	if argsNode != nil {
		call.FunctionArgs = p.extractCallbackArgIdentifiers(argsNode, content)
	}

	return call
}

// extractCallbackArgIdentifiers extracts identifier arguments from a TS call's arguments node.
//
// Description:
//
//	Walks the arguments node and extracts top-level identifiers that likely reference
//	functions or classes. This enables callback/HOF tracking in the graph.
//
// IT-03a C-1: Enables EdgeTypeReferences from caller to callback arguments.
func (p *TypeScriptParser) extractCallbackArgIdentifiers(argsNode *sitter.Node, content []byte) []string {
	identifiers := make([]string, 0, argsNode.ChildCount())
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		child := argsNode.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "identifier":
			name := string(content[child.StartByte():child.EndByte()])
			if name != "true" && name != "false" && name != "null" && name != "undefined" && name != "this" {
				identifiers = append(identifiers, name)
			}
		case "member_expression":
			text := string(content[child.StartByte():child.EndByte()])
			if len(text) <= 50 && !strings.Contains(text, "(") {
				identifiers = append(identifiers, text)
			}
		}
	}
	return identifiers
}

// extractTypeNarrowings extracts type identifiers from instanceof and type predicate expressions.
//
// Description:
//
//	Walks a function body to find "instanceof" binary expressions and extracts
//	the type identifier on the right side. For `x instanceof Router`, extracts "Router".
//	This enables the graph builder to create REFERENCES edges for type narrowing.
//
// IT-03a C-3: Tracks types referenced via instanceof for graph visibility.
func (p *TypeScriptParser) extractTypeNarrowings(bodyNode *sitter.Node, content []byte) []string {
	if bodyNode == nil {
		return nil
	}

	var narrowings []string
	seen := make(map[string]bool)

	type stackEntry struct {
		node  *sitter.Node
		depth int
	}

	stack := []stackEntry{{node: bodyNode, depth: 0}}
	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		node := entry.node
		if node == nil || entry.depth > 20 {
			continue
		}

		// Look for binary expressions with "instanceof" operator
		if node.Type() == "binary_expression" {
			operatorFound := false
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child == nil {
					continue
				}
				text := string(content[child.StartByte():child.EndByte()])
				if text == "instanceof" {
					operatorFound = true
				} else if operatorFound && (child.Type() == "identifier" || child.Type() == "type_identifier") {
					typeName := text
					if !seen[typeName] {
						seen[typeName] = true
						narrowings = append(narrowings, typeName)
					}
					break
				}
			}
		}

		// Recurse into children (but not into nested function bodies)
		childCount := int(node.ChildCount())
		for i := childCount - 1; i >= 0; i-- {
			child := node.Child(i)
			if child != nil && child.Type() != "function_declaration" &&
				child.Type() != "arrow_function" && child.Type() != "function" {
				stack = append(stack, stackEntry{node: child, depth: entry.depth + 1})
			}
		}
	}

	return narrowings
}

// extractTypeArgumentIdentifiers extracts non-primitive type identifiers from a type expression string.
//
// Description:
//
//	Parses type strings like "Promise<User>", "Map<string, Handler>", "Observable<Event[]>"
//	and extracts user-defined type names (skipping primitives like string, number, boolean, etc.).
//
// IT-03a C-2: Enables graph edges from symbols to their generic type arguments.
func extractTypeArgumentIdentifiers(typeExpr string) []string {
	if !strings.Contains(typeExpr, "<") {
		return nil
	}

	primitives := map[string]bool{
		"string": true, "number": true, "boolean": true, "void": true,
		"any": true, "unknown": true, "never": true, "null": true,
		"undefined": true, "object": true, "symbol": true, "bigint": true,
	}

	var identifiers []string
	// Extract identifiers between < > brackets
	depth := 0
	current := strings.Builder{}
	for _, ch := range typeExpr {
		switch ch {
		case '<':
			depth++
			current.Reset()
		case '>':
			if depth > 0 {
				name := strings.TrimSpace(current.String())
				// Handle array types: User[]
				name = strings.TrimSuffix(name, "[]")
				if name != "" && !primitives[strings.ToLower(name)] {
					identifiers = append(identifiers, name)
				}
				current.Reset()
			}
			depth--
		case ',':
			if depth > 0 {
				name := strings.TrimSpace(current.String())
				name = strings.TrimSuffix(name, "[]")
				if name != "" && !primitives[strings.ToLower(name)] {
					identifiers = append(identifiers, name)
				}
				current.Reset()
			}
		default:
			if depth > 0 {
				current.WriteRune(ch)
			}
		}
	}

	return identifiers
}

// Compile-time interface compliance check.
var _ Parser = (*TypeScriptParser)(nil)
