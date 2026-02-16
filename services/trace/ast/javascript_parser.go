package ast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"log/slog"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
	"go.opentelemetry.io/otel/attribute"
)

// JavaScriptParser extracts symbols from JavaScript source code.
//
// Description:
//
//	JavaScriptParser uses tree-sitter to parse JavaScript source files and extract
//	structured symbol information. It supports all modern JavaScript features including
//	ES6+ modules, classes, async/await, generators, and private fields.
//
// Thread Safety:
//
//	JavaScriptParser is safe for concurrent use. Multiple goroutines can call Parse
//	simultaneously. Each Parse call creates its own tree-sitter parser instance.
//
// Example:
//
//	parser := NewJavaScriptParser()
//	result, err := parser.Parse(ctx, content, "app.js")
//	if err != nil {
//	    return fmt.Errorf("parse: %w", err)
//	}
//	for _, sym := range result.Symbols {
//	    fmt.Printf("%s: %s\n", sym.Kind, sym.Name)
//	}
type JavaScriptParser struct {
	options JavaScriptParserOptions
}

// JavaScriptParserOptions configures JavaScriptParser behavior.
type JavaScriptParserOptions struct {
	// MaxFileSize is the maximum file size in bytes to parse.
	// Files larger than this return ErrFileTooLarge.
	// Default: 10MB
	MaxFileSize int

	// IncludePrivate determines whether to include non-exported symbols.
	// Default: true
	IncludePrivate bool

	// ExtractBodies determines whether to include function body text.
	// Default: false (bodies are expensive and often not needed)
	ExtractBodies bool
}

// DefaultJavaScriptParserOptions returns the default options.
func DefaultJavaScriptParserOptions() JavaScriptParserOptions {
	return JavaScriptParserOptions{
		MaxFileSize:    10 * 1024 * 1024, // 10MB
		IncludePrivate: true,
		ExtractBodies:  false,
	}
}

// JavaScriptParserOption is a functional option for configuring JavaScriptParser.
type JavaScriptParserOption func(*JavaScriptParserOptions)

// WithJSMaxFileSize sets the maximum file size for parsing.
func WithJSMaxFileSize(size int) JavaScriptParserOption {
	return func(o *JavaScriptParserOptions) {
		o.MaxFileSize = size
	}
}

// WithJSIncludePrivate sets whether to include non-exported symbols.
func WithJSIncludePrivate(include bool) JavaScriptParserOption {
	return func(o *JavaScriptParserOptions) {
		o.IncludePrivate = include
	}
}

// WithJSExtractBodies sets whether to include function bodies.
func WithJSExtractBodies(extract bool) JavaScriptParserOption {
	return func(o *JavaScriptParserOptions) {
		o.ExtractBodies = extract
	}
}

// NewJavaScriptParser creates a new JavaScriptParser with the given options.
//
// Description:
//
//	Creates a parser configured for JavaScript source files. The parser can be
//	reused for multiple files and is safe for concurrent use.
//
// Example:
//
//	// Default options
//	parser := NewJavaScriptParser()
//
//	// With custom options
//	parser := NewJavaScriptParser(
//	    WithJSMaxFileSize(5 * 1024 * 1024),
//	    WithJSIncludePrivate(false),
//	)
func NewJavaScriptParser(opts ...JavaScriptParserOption) *JavaScriptParser {
	options := DefaultJavaScriptParserOptions()
	for _, opt := range opts {
		opt(&options)
	}
	return &JavaScriptParser{options: options}
}

// Language returns the language name for this parser.
func (p *JavaScriptParser) Language() string {
	return "javascript"
}

// Extensions returns the file extensions this parser handles.
func (p *JavaScriptParser) Extensions() []string {
	return []string{".js", ".mjs", ".cjs", ".jsx"}
}

// Parse extracts symbols from JavaScript source code.
//
// Description:
//
//	Parses the provided JavaScript content using tree-sitter and extracts all
//	symbols including functions, classes, methods, fields, variables, and imports.
//
// Inputs:
//
//	ctx      - Context for cancellation. Checked before and after parsing.
//	content  - Raw JavaScript source bytes. Must be valid UTF-8.
//	filePath - Path to the file (relative to project root, for ID generation).
//
// Outputs:
//
//	*ParseResult - Extracted symbols and metadata. Never nil on success.
//	error        - Non-nil only for complete failures (invalid UTF-8, too large).
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *JavaScriptParser) Parse(ctx context.Context, content []byte, filePath string) (*ParseResult, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("javascript parse canceled before start: %w", err)
	}

	// Validate file size
	if len(content) > p.options.MaxFileSize {
		return nil, ErrFileTooLarge
	}

	// Validate UTF-8
	if !utf8.Valid(content) {
		return nil, ErrInvalidContent
	}

	// Compute hash before parsing
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:])

	// Create result
	result := &ParseResult{
		FilePath:      filePath,
		Language:      "javascript",
		Hash:          hashStr,
		ParsedAtMilli: time.Now().UnixMilli(),
		Symbols:       make([]*Symbol, 0),
		Imports:       make([]Import, 0),
		Errors:        make([]string, 0),
	}

	// Parse with tree-sitter
	parser := sitter.NewParser()
	parser.SetLanguage(javascript.GetLanguage())

	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse failed: %w", err)
	}
	defer tree.Close()

	// Check context after parsing
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("javascript parse canceled after tree-sitter: %w", err)
	}

	// Extract symbols from AST
	rootNode := tree.RootNode()

	// IT-01 Phase C: Pre-scan for module export aliases (proto, app, req, res patterns)
	// This enables correct receiver resolution for prototype-assigned methods.
	exportAliases := p.buildModuleExportAliases(rootNode, content, filePath)

	p.extractSymbols(ctx, rootNode, content, filePath, result, false, exportAliases)

	// Validate result
	if err := result.Validate(); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("validation error: %v", err))
	}

	return result, nil
}

// extractSymbols recursively extracts symbols from the AST.
// exportAliases maps variable names to their semantic type names for prototype method extraction.
func (p *JavaScriptParser) extractSymbols(ctx context.Context, node *sitter.Node, content []byte, filePath string, result *ParseResult, exported bool, exportAliases map[string]string) {
	if node == nil {
		return
	}

	nodeType := node.Type()

	switch nodeType {
	case jsNodeProgram:
		// Process all children
		for i := 0; i < int(node.ChildCount()); i++ {
			p.extractSymbols(ctx, node.Child(i), content, filePath, result, false, exportAliases)
		}

	case jsNodeImportStatement:
		p.extractImport(node, content, filePath, result)

	case jsNodeExportStatement:
		p.extractExport(ctx, node, content, filePath, result, exportAliases)

	case jsNodeFunctionDeclaration, jsNodeGeneratorFunctionDecl:
		sym := p.extractFunction(ctx, node, content, filePath, exported)
		if sym != nil {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}

	case jsNodeClassDeclaration:
		sym := p.extractClass(ctx, node, content, filePath, exported)
		if sym != nil {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}

	case jsNodeLexicalDeclaration, jsNodeVariableDeclaration:
		// Check for CommonJS require() first
		p.extractCommonJSImport(node, content, filePath, result)
		// Then extract variables
		syms := p.extractVariables(node, content, filePath, exported)
		for _, sym := range syms {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}

	case jsNodeExpressionStatement:
		// IT-01 Phase C: Handle prototype method assignments
		// Patterns: proto.handle = function handle() {...}
		//           app.init = function init() {...}
		//           req.get = req.header = function header() {...}
		if len(exportAliases) > 0 {
			syms := p.extractPrototypeMethodAssignment(ctx, node, content, filePath, exportAliases)
			for _, sym := range syms {
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}
		}

	default:
		// No special handling needed for other node types
	}
}

// extractImport extracts an import statement.
func (p *JavaScriptParser) extractImport(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	imp := &Import{
		IsModule: true,
		Location: Location{
			FilePath:  filePath,
			StartLine: int(node.StartPoint().Row) + 1,
			EndLine:   int(node.EndPoint().Row) + 1,
			StartCol:  int(node.StartPoint().Column),
			EndCol:    int(node.EndPoint().Column),
		},
	}

	// Find the module path (string node)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeString {
			imp.Path = p.extractStringContent(child, content)
		} else if child.Type() == jsNodeImportClause {
			p.extractImportClause(child, content, imp)
		}
	}

	if imp.Path != "" {
		result.Imports = append(result.Imports, *imp)

		// Also add as symbol
		sym := &Symbol{
			ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, imp.Path),
			Name:          imp.Path,
			Kind:          SymbolKindImport,
			FilePath:      filePath,
			StartLine:     int(node.StartPoint().Row) + 1,
			EndLine:       int(node.EndPoint().Row) + 1,
			StartCol:      int(node.StartPoint().Column),
			EndCol:        int(node.EndPoint().Column),
			Language:      "javascript",
			ParsedAtMilli: time.Now().UnixMilli(),
		}
		result.Symbols = append(result.Symbols, sym)
	}
}

// extractImportClause extracts the import clause details.
func (p *JavaScriptParser) extractImportClause(node *sitter.Node, content []byte, imp *Import) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			// Default import
			imp.Alias = string(content[child.StartByte():child.EndByte()])
			imp.IsDefault = true
		case jsNodeNamespaceImport:
			// import * as foo
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeIdentifier {
					imp.Alias = string(content[gc.StartByte():gc.EndByte()])
				}
			}
			imp.IsNamespace = true
		case jsNodeNamedImports:
			// import { foo, bar }
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeImportSpecifier {
					name := p.extractImportSpecifierName(gc, content)
					if name != "" {
						imp.Names = append(imp.Names, name)
					}
				}
			}
		}
	}
}

// extractImportSpecifierName extracts the name from an import specifier.
func (p *JavaScriptParser) extractImportSpecifierName(node *sitter.Node, content []byte) string {
	// import { foo } or import { foo as bar }
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeIdentifier {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	return ""
}

// extractStringContent extracts the string content without quotes.
func (p *JavaScriptParser) extractStringContent(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeStringFragment {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	// Fallback: remove quotes manually
	text := string(content[node.StartByte():node.EndByte()])
	if len(text) >= 2 {
		return text[1 : len(text)-1]
	}
	return text
}

// extractExport extracts an export statement.
func (p *JavaScriptParser) extractExport(ctx context.Context, node *sitter.Node, content []byte, filePath string, result *ParseResult, exportAliases map[string]string) {
	isDefault := false

	// Check for default keyword
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeDefault {
			isDefault = true
			break
		}
	}

	// Get preceding comment for the export
	docComment := p.getPrecedingComment(node, content)

	// Process the declaration inside the export
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeFunctionDeclaration, jsNodeGeneratorFunctionDecl:
			sym := p.extractFunction(ctx, child, content, filePath, true)
			if sym != nil {
				sym.Exported = true
				if isDefault {
					sym.Metadata = ensureMetadata(sym.Metadata)
					// Mark as default export in metadata or name
				}
				if docComment != "" && sym.DocComment == "" {
					sym.DocComment = docComment
				}
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}

		case jsNodeClassDeclaration:
			sym := p.extractClass(ctx, child, content, filePath, true)
			if sym != nil {
				sym.Exported = true
				if docComment != "" && sym.DocComment == "" {
					sym.DocComment = docComment
				}
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}

		case jsNodeLexicalDeclaration, jsNodeVariableDeclaration:
			syms := p.extractVariables(child, content, filePath, true)
			for _, sym := range syms {
				sym.Exported = true
				if docComment != "" && sym.DocComment == "" {
					sym.DocComment = docComment
				}
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}

		case jsNodeIdentifier:
			// export default identifier
			if isDefault {
				name := string(content[child.StartByte():child.EndByte()])
				sym := &Symbol{
					ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, name),
					Name:          name,
					Kind:          SymbolKindVariable,
					FilePath:      filePath,
					StartLine:     int(node.StartPoint().Row) + 1,
					EndLine:       int(node.EndPoint().Row) + 1,
					StartCol:      int(node.StartPoint().Column),
					EndCol:        int(node.EndPoint().Column),
					Exported:      true,
					Language:      "javascript",
					ParsedAtMilli: time.Now().UnixMilli(),
					DocComment:    docComment,
				}
				result.Symbols = append(result.Symbols, sym)
			}

		case jsNodeExportClause:
			// export { foo, bar }
			p.extractExportClause(child, content, filePath, result)
		}
	}
}

// extractExportClause extracts named exports from export clause.
func (p *JavaScriptParser) extractExportClause(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeExportSpecifier {
			name := ""
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeIdentifier {
					name = string(content[gc.StartByte():gc.EndByte()])
					break
				}
			}
			if name != "" {
				sym := &Symbol{
					ID:            GenerateID(filePath, int(child.StartPoint().Row)+1, name),
					Name:          name,
					Kind:          SymbolKindVariable,
					FilePath:      filePath,
					StartLine:     int(child.StartPoint().Row) + 1,
					EndLine:       int(child.EndPoint().Row) + 1,
					StartCol:      int(child.StartPoint().Column),
					EndCol:        int(child.EndPoint().Column),
					Exported:      true,
					Language:      "javascript",
					ParsedAtMilli: time.Now().UnixMilli(),
				}
				result.Symbols = append(result.Symbols, sym)
			}
		}
	}
}

// extractFunction extracts a function declaration.
func (p *JavaScriptParser) extractFunction(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool) *Symbol {
	name := ""
	isAsync := false
	isGenerator := false
	var params []string
	var bodyNode *sitter.Node
	docComment := p.getPrecedingComment(node, content)

	// Check node type for generator
	if node.Type() == jsNodeGeneratorFunctionDecl {
		isGenerator = true
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			name = string(content[child.StartByte():child.EndByte()])
		case jsNodeAsync:
			isAsync = true
		case jsNodeFormalParameters:
			params = p.extractParameters(child, content)
		case "*":
			isGenerator = true
		case jsNodeStatementBlock:
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	// Build signature
	signature := "function"
	if isAsync {
		signature = "async function"
	}
	if isGenerator {
		signature += "*"
	}
	signature += " " + name + "(" + strings.Join(params, ", ") + ")"

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, name),
		Name:          name,
		Kind:          SymbolKindFunction,
		FilePath:      filePath,
		StartLine:     int(node.StartPoint().Row) + 1,
		EndLine:       int(node.EndPoint().Row) + 1,
		StartCol:      int(node.StartPoint().Column),
		EndCol:        int(node.EndPoint().Column),
		Signature:     signature,
		DocComment:    docComment,
		Exported:      exported,
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
	}

	if isAsync || isGenerator {
		sym.Metadata = &SymbolMetadata{
			IsAsync:     isAsync,
			IsGenerator: isGenerator,
		}
	}

	// GR-41: Extract call sites from function body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	return sym
}

// extractClass extracts a class declaration.
func (p *JavaScriptParser) extractClass(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool) *Symbol {
	name := ""
	var extends string
	var children []*Symbol
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			name = string(content[child.StartByte():child.EndByte()])
		case jsNodeClassHeritage:
			extends = p.extractClassHeritage(child, content)
		case jsNodeClassBody:
			children = p.extractClassBody(ctx, child, content, filePath, name)
		}
	}

	if name == "" {
		return nil
	}

	signature := "class " + name
	if extends != "" {
		signature += " extends " + extends
	}

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, name),
		Name:          name,
		Kind:          SymbolKindClass,
		FilePath:      filePath,
		StartLine:     int(node.StartPoint().Row) + 1,
		EndLine:       int(node.EndPoint().Row) + 1,
		StartCol:      int(node.StartPoint().Column),
		EndCol:        int(node.EndPoint().Column),
		Signature:     signature,
		DocComment:    docComment,
		Exported:      exported,
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
		Children:      children,
	}

	if extends != "" {
		sym.Metadata = &SymbolMetadata{
			Extends: extends,
		}
	}

	return sym
}

// extractClassHeritage extracts the extends clause from class heritage.
func (p *JavaScriptParser) extractClassHeritage(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeIdentifier {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	return ""
}

// extractClassBody extracts members from a class body.
func (p *JavaScriptParser) extractClassBody(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) []*Symbol {
	var members []*Symbol

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeMethodDefinition:
			mem := p.extractMethod(ctx, child, content, filePath, className)
			if mem != nil {
				members = append(members, mem)
			}
		case jsNodeFieldDefinition:
			mem := p.extractField(child, content, filePath, className)
			if mem != nil {
				members = append(members, mem)
			}
		}
	}

	return members
}

// extractMethod extracts a method definition from a class.
func (p *JavaScriptParser) extractMethod(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) *Symbol {
	name := ""
	isAsync := false
	isStatic := false
	isGenerator := false
	isPrivate := false
	var params []string
	var bodyNode *sitter.Node
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodePropertyIdentifier:
			name = string(content[child.StartByte():child.EndByte()])
		case jsNodePrivatePropertyIdent:
			name = string(content[child.StartByte():child.EndByte()])
			isPrivate = true
		case jsNodeAsync:
			isAsync = true
		case jsNodeStatic:
			isStatic = true
		case "*":
			isGenerator = true
		case jsNodeFormalParameters:
			params = p.extractParameters(child, content)
		case jsNodeStatementBlock:
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	// Build signature
	sig := ""
	if isStatic {
		sig += "static "
	}
	if isAsync {
		sig += "async "
	}
	sig += name
	if isGenerator {
		sig += "*"
	}
	sig += "(" + strings.Join(params, ", ") + ")"

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, className+"."+name),
		Name:          name,
		Kind:          SymbolKindMethod,
		FilePath:      filePath,
		StartLine:     int(node.StartPoint().Row) + 1,
		EndLine:       int(node.EndPoint().Row) + 1,
		StartCol:      int(node.StartPoint().Column),
		EndCol:        int(node.EndPoint().Column),
		Signature:     sig,
		DocComment:    docComment,
		Receiver:      className,
		Exported:      !isPrivate,
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
	}

	if isAsync || isGenerator || isStatic || isPrivate {
		sym.Metadata = &SymbolMetadata{
			IsAsync:     isAsync,
			IsGenerator: isGenerator,
			IsStatic:    isStatic,
		}
		if isPrivate {
			sym.Metadata.AccessModifier = "private"
		}
	}

	// GR-41: Extract call sites from method body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	return sym
}

// extractField extracts a field definition from a class.
func (p *JavaScriptParser) extractField(node *sitter.Node, content []byte, filePath string, className string) *Symbol {
	name := ""
	isStatic := false
	isPrivate := false
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodePropertyIdentifier:
			name = string(content[child.StartByte():child.EndByte()])
		case jsNodePrivatePropertyIdent:
			name = string(content[child.StartByte():child.EndByte()])
			isPrivate = true
		case jsNodeStatic:
			isStatic = true
		}
	}

	if name == "" {
		return nil
	}

	sig := ""
	if isStatic {
		sig += "static "
	}
	sig += name

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, className+"."+name),
		Name:          name,
		Kind:          SymbolKindField,
		FilePath:      filePath,
		StartLine:     int(node.StartPoint().Row) + 1,
		EndLine:       int(node.EndPoint().Row) + 1,
		StartCol:      int(node.StartPoint().Column),
		EndCol:        int(node.EndPoint().Column),
		Signature:     sig,
		DocComment:    docComment,
		Receiver:      className,
		Exported:      !isPrivate,
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
	}

	if isStatic || isPrivate {
		sym.Metadata = &SymbolMetadata{
			IsStatic: isStatic,
		}
		if isPrivate {
			sym.Metadata.AccessModifier = "private"
		}
	}

	return sym
}

// extractVariables extracts variable declarations.
func (p *JavaScriptParser) extractVariables(node *sitter.Node, content []byte, filePath string, exported bool) []*Symbol {
	var symbols []*Symbol
	isConst := false
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeConst:
			isConst = true
		case jsNodeVariableDeclarator:
			sym := p.extractVariableDeclarator(child, content, filePath, exported, isConst, docComment)
			if sym != nil {
				symbols = append(symbols, sym)
			}
		}
	}

	return symbols
}

// extractVariableDeclarator extracts a single variable declarator.
func (p *JavaScriptParser) extractVariableDeclarator(node *sitter.Node, content []byte, filePath string, exported bool, isConst bool, docComment string) *Symbol {
	name := ""
	isArrowFunction := false
	isAsync := false
	var params []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			if name == "" { // First identifier is the variable name
				name = string(content[child.StartByte():child.EndByte()])
			}
		case jsNodeArrowFunction:
			isArrowFunction = true
			// Extract arrow function details
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				switch gc.Type() {
				case jsNodeAsync:
					isAsync = true
				case jsNodeFormalParameters:
					params = p.extractParameters(gc, content)
				case jsNodeIdentifier:
					// Single parameter without parens
					params = []string{string(content[gc.StartByte():gc.EndByte()])}
				}
			}
		}
	}

	if name == "" {
		return nil
	}

	kind := SymbolKindVariable
	if isConst {
		kind = SymbolKindConstant
	}
	if isArrowFunction {
		kind = SymbolKindFunction
	}

	sig := ""
	if isConst {
		sig = "const "
	} else {
		sig = "let "
	}
	sig += name
	if isArrowFunction {
		if isAsync {
			sig = "const " + name + " = async (" + strings.Join(params, ", ") + ") => {}"
		} else {
			sig = "const " + name + " = (" + strings.Join(params, ", ") + ") => {}"
		}
	}

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, name),
		Name:          name,
		Kind:          kind,
		FilePath:      filePath,
		StartLine:     int(node.StartPoint().Row) + 1,
		EndLine:       int(node.EndPoint().Row) + 1,
		StartCol:      int(node.StartPoint().Column),
		EndCol:        int(node.EndPoint().Column),
		Signature:     sig,
		DocComment:    docComment,
		Exported:      exported,
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
	}

	if isArrowFunction && isAsync {
		sym.Metadata = &SymbolMetadata{
			IsAsync: true,
		}
	}

	return sym
}

// extractParameters extracts parameter names from formal_parameters.
func (p *JavaScriptParser) extractParameters(node *sitter.Node, content []byte) []string {
	var params []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			params = append(params, string(content[child.StartByte():child.EndByte()]))
		case jsNodeRestPattern:
			// ...args
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeIdentifier {
					params = append(params, "..."+string(content[gc.StartByte():gc.EndByte()]))
				}
			}
		case jsNodeAssignmentExpression:
			// param = defaultValue
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeIdentifier {
					params = append(params, string(content[gc.StartByte():gc.EndByte()]))
					break
				}
			}
		}
	}

	return params
}

// extractCommonJSImport extracts CommonJS require() imports.
func (p *JavaScriptParser) extractCommonJSImport(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	// Look for: const foo = require('bar')
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == jsNodeVariableDeclarator {
			varName := ""
			requirePath := ""

			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				switch gc.Type() {
				case jsNodeIdentifier:
					varName = string(content[gc.StartByte():gc.EndByte()])
				case jsNodeCallExpression:
					// Check if it's require()
					for k := 0; k < int(gc.ChildCount()); k++ {
						ggc := gc.Child(k)
						if ggc.Type() == jsNodeIdentifier && string(content[ggc.StartByte():ggc.EndByte()]) == "require" {
							// Found require, get the argument
							for l := 0; l < int(gc.ChildCount()); l++ {
								arg := gc.Child(l)
								if arg.Type() == jsNodeArguments {
									for m := 0; m < int(arg.ChildCount()); m++ {
										argChild := arg.Child(m)
										if argChild.Type() == jsNodeString {
											requirePath = p.extractStringContent(argChild, content)
										}
									}
								}
							}
						}
					}
				}
			}

			if requirePath != "" && varName != "" {
				imp := &Import{
					Path:       requirePath,
					Alias:      varName,
					IsCommonJS: true,
					Location: Location{
						FilePath:  filePath,
						StartLine: int(node.StartPoint().Row) + 1,
						EndLine:   int(node.EndPoint().Row) + 1,
						StartCol:  int(node.StartPoint().Column),
						EndCol:    int(node.EndPoint().Column),
					},
				}
				result.Imports = append(result.Imports, *imp)

				// Also add as symbol
				sym := &Symbol{
					ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, requirePath),
					Name:          requirePath,
					Kind:          SymbolKindImport,
					FilePath:      filePath,
					StartLine:     int(node.StartPoint().Row) + 1,
					EndLine:       int(node.EndPoint().Row) + 1,
					StartCol:      int(node.StartPoint().Column),
					EndCol:        int(node.EndPoint().Column),
					Language:      "javascript",
					ParsedAtMilli: time.Now().UnixMilli(),
				}
				result.Symbols = append(result.Symbols, sym)
			}
		}
	}
}

// getPrecedingComment extracts JSDoc or comment before a node.
func (p *JavaScriptParser) getPrecedingComment(node *sitter.Node, content []byte) string {
	if node == nil {
		return ""
	}

	// Look for comment node immediately before this one
	prev := node.PrevSibling()
	if prev != nil && prev.Type() == jsNodeComment {
		comment := string(content[prev.StartByte():prev.EndByte()])
		// Check if it's a JSDoc comment
		if strings.HasPrefix(comment, "/**") {
			return comment
		}
	}

	// If this node is inside an export_statement, check parent's previous sibling
	parent := node.Parent()
	if parent != nil && parent.Type() == jsNodeExportStatement {
		parentPrev := parent.PrevSibling()
		if parentPrev != nil && parentPrev.Type() == jsNodeComment {
			comment := string(content[parentPrev.StartByte():parentPrev.EndByte()])
			if strings.HasPrefix(comment, "/**") {
				return comment
			}
		}
	}

	return ""
}

// extractCallSites extracts all function and method calls from a JavaScript function body.
//
// Description:
//
//	Traverses the AST of a JavaScript function or method body to find all
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
func (p *JavaScriptParser) extractCallSites(ctx context.Context, bodyNode *sitter.Node, content []byte, filePath string) []CallSite {
	if bodyNode == nil {
		return nil
	}

	if ctx.Err() != nil {
		return nil
	}

	ctx, span := tracer.Start(ctx, "JavaScriptParser.extractCallSites")
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
			slog.Debug("GR-41: Max call expression depth reached in JavaScript",
				slog.String("file", filePath),
				slog.Int("depth", entry.depth),
			)
			continue
		}

		nodeCount++
		if nodeCount%100 == 0 {
			if ctx.Err() != nil {
				slog.Debug("GR-41: Context cancelled during JavaScript call extraction",
					slog.String("file", filePath),
					slog.Int("calls_found", len(calls)),
				)
				return calls
			}
		}

		if len(calls) >= MaxCallSitesPerSymbol {
			slog.Warn("GR-41: Max call sites per symbol reached in JavaScript",
				slog.String("file", filePath),
				slog.Int("limit", MaxCallSitesPerSymbol),
			)
			return calls
		}

		// JavaScript tree-sitter uses "call_expression" for calls
		if node.Type() == jsNodeCallExpression {
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

// extractSingleCallSite extracts call information from a JavaScript call_expression node.
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
//   - node: A call_expression node from tree-sitter-javascript. Must not be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - *CallSite: The extracted call site, or nil if extraction fails.
//
// Thread Safety: Safe for concurrent use.
func (p *JavaScriptParser) extractSingleCallSite(node *sitter.Node, content []byte, filePath string) *CallSite {
	if node == nil || node.Type() != jsNodeCallExpression {
		return nil
	}

	// call_expression: function_node, arguments
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

	case jsNodeMemberExpression:
		// Method call: obj.method(args) or this.method(args)
		// member_expression has: object, property
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

	return call
}

// =============================================================================
// IT-01 Phase C: Module Export Alias Resolution
// =============================================================================

// buildModuleExportAliases scans top-level statements to build a map from variable names
// to their semantic type names, for use in prototype method extraction.
//
// Description:
//
//	Pre-ES6 JavaScript libraries use patterns like:
//	  var proto = module.exports = function(options) {...}
//	  var app = exports = module.exports = {}
//	  var req = Object.create(http.IncomingMessage.prototype)
//	followed by: module.exports = req
//
//	In all these patterns, the variable IS the module's public API, and methods assigned
//	to it (proto.handle = function handle() {...}) are the module's methods. The semantic
//	type name is derived from the file path: lib/router/index.js → "Router".
//
//	This function detects these patterns and builds aliasMap[varName] = "SemanticTypeName".
//
// Inputs:
//
//	rootNode - The program root node.
//	content - Source file content bytes.
//	filePath - File path for semantic type derivation.
//
// Outputs:
//
//	map[string]string - Variable name → semantic type name. Empty if no patterns found.
//
// Limitations:
//
//	Only detects patterns at the top level of the file (not nested in functions).
//	Does not trace variable reassignments beyond the initial declaration/assignment.
//
// Assumptions:
//
//	File path uses forward slashes. Content is valid UTF-8.
func (p *JavaScriptParser) buildModuleExportAliases(rootNode *sitter.Node, content []byte, filePath string) map[string]string {
	if rootNode == nil || rootNode.Type() != jsNodeProgram {
		return nil
	}

	aliases := make(map[string]string)
	semanticName := deriveSemanticTypeName(filePath)
	if semanticName == "" {
		return nil
	}

	// Track which variable names are assigned to module.exports or exports
	// Also track module.exports = varName assignments
	var moduleExportVarNames []string

	for i := 0; i < int(rootNode.ChildCount()); i++ {
		child := rootNode.Child(i)
		if child == nil {
			continue
		}

		switch child.Type() {
		case jsNodeVariableDeclaration, jsNodeLexicalDeclaration:
			// Check for: var proto = module.exports = ... or var app = exports = module.exports = {}
			names := p.findModuleExportVarDecl(child, content)
			moduleExportVarNames = append(moduleExportVarNames, names...)

		case jsNodeExpressionStatement:
			// Check for: module.exports = varName
			varName := p.findModuleExportsAssignment(child, content)
			if varName != "" {
				moduleExportVarNames = append(moduleExportVarNames, varName)
			}
		}
	}

	// Map all discovered variable names to the semantic type name
	for _, name := range moduleExportVarNames {
		aliases[name] = semanticName
	}

	if len(aliases) > 0 {
		slog.Debug("IT-01 Phase C: module export aliases detected",
			slog.String("file", filePath),
			slog.String("semantic_type", semanticName),
			slog.Int("alias_count", len(aliases)),
		)
	}

	return aliases
}

// findModuleExportVarDecl finds variable names assigned to module.exports in a var/let/const declaration.
//
// Handles patterns:
//
//	var proto = module.exports = function() {}
//	var app = exports = module.exports = {}
//	var req = Object.create(...)  (when followed by module.exports = req)
//
// For chained assignments like `var app = exports = module.exports = {}`,
// the tree-sitter AST has: variable_declarator → identifier "app", assignment_expression chain.
func (p *JavaScriptParser) findModuleExportVarDecl(node *sitter.Node, content []byte) []string {
	var names []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil || child.Type() != jsNodeVariableDeclarator {
			continue
		}

		varName := ""
		hasModuleExports := false

		for j := 0; j < int(child.ChildCount()); j++ {
			gc := child.Child(j)
			if gc == nil {
				continue
			}

			switch gc.Type() {
			case jsNodeIdentifier:
				if varName == "" {
					varName = string(content[gc.StartByte():gc.EndByte()])
				}
			case jsNodeAssignmentExpression:
				// Check if the assignment chain contains module.exports or exports
				if p.assignmentChainContainsModuleExports(gc, content) {
					hasModuleExports = true
				}
			}
		}

		if varName != "" && hasModuleExports {
			names = append(names, varName)
		}
	}

	return names
}

// assignmentChainContainsModuleExports checks if an assignment expression chain
// contains module.exports or exports as a target.
//
// Handles: exports = module.exports = function() {}
// AST: assignment_expression(left=identifier "exports", right=assignment_expression(left=member "module.exports", right=...))
func (p *JavaScriptParser) assignmentChainContainsModuleExports(node *sitter.Node, content []byte) bool {
	if node == nil {
		return false
	}

	if node.Type() == jsNodeMemberExpression {
		text := string(content[node.StartByte():node.EndByte()])
		if text == "module.exports" {
			return true
		}
	}

	if node.Type() == jsNodeIdentifier {
		text := string(content[node.StartByte():node.EndByte()])
		if text == "exports" {
			return true
		}
	}

	// Check all children recursively (handles chained assignments)
	for i := 0; i < int(node.ChildCount()); i++ {
		if p.assignmentChainContainsModuleExports(node.Child(i), content) {
			return true
		}
	}

	return false
}

// findModuleExportsAssignment extracts the variable name from `module.exports = varName` statements.
//
// Handles:
//
//	module.exports = req
//	module.exports = proto
//
// Returns the variable name on the RHS, or empty string if not a simple identifier assignment.
func (p *JavaScriptParser) findModuleExportsAssignment(node *sitter.Node, content []byte) string {
	if node == nil || node.Type() != jsNodeExpressionStatement {
		return ""
	}

	// expression_statement → assignment_expression
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil || child.Type() != jsNodeAssignmentExpression {
			continue
		}

		// Check: left is module.exports, right is an identifier
		leftNode := child.ChildByFieldName("left")
		rightNode := child.ChildByFieldName("right")

		if leftNode == nil || rightNode == nil {
			continue
		}

		// Check if left is "module.exports"
		if leftNode.Type() == jsNodeMemberExpression {
			leftText := string(content[leftNode.StartByte():leftNode.EndByte()])
			if leftText == "module.exports" && rightNode.Type() == jsNodeIdentifier {
				return string(content[rightNode.StartByte():rightNode.EndByte()])
			}
		}
	}

	return ""
}

// extractPrototypeMethodAssignment extracts method symbols from prototype-style assignments.
//
// Description:
//
//	Handles the pre-ES6 pattern where methods are assigned to an object variable:
//	  proto.handle = function handle(req, res, out) {...}
//	  app.init = function init() {...}
//	  res.set = res.header = function header(field, val) {...}
//
//	When the variable name (e.g., "proto") is a known module export alias, the method
//	is created with the semantic type name as receiver (e.g., "Router" instead of "proto").
//
// Inputs:
//
//	ctx - Context for cancellation.
//	node - An expression_statement node.
//	content - Source file content bytes.
//	filePath - File path for symbol ID generation.
//	exportAliases - Map from variable name to semantic type name.
//
// Outputs:
//
//	[]*Symbol - Extracted method symbols. May be empty if the statement doesn't match.
//
// Thread Safety: Safe for concurrent use.
func (p *JavaScriptParser) extractPrototypeMethodAssignment(ctx context.Context, node *sitter.Node, content []byte, filePath string, exportAliases map[string]string) []*Symbol {
	if node == nil || node.Type() != jsNodeExpressionStatement {
		return nil
	}

	// Find the assignment expression inside
	var assignNode *sitter.Node
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil && child.Type() == jsNodeAssignmentExpression {
			assignNode = child
			break
		}
	}
	if assignNode == nil {
		return nil
	}

	// Walk the assignment chain to find the function on the RHS and all member expressions on the LHS.
	// Handles: proto.handle = function handle() {...}
	// Handles: res.set = res.header = function header() {...}  (aliased methods)
	return p.extractMethodsFromAssignmentChain(ctx, assignNode, content, filePath, exportAliases)
}

// extractMethodsFromAssignmentChain recursively walks an assignment chain to extract
// prototype method symbols.
//
// For `res.set = res.header = function header(field, val) {...}`:
// - assignNode is assignment_expression(left=member(res.set), right=assignment_expression(...))
// - Recurse into right to find the function and additional member expressions
func (p *JavaScriptParser) extractMethodsFromAssignmentChain(ctx context.Context, assignNode *sitter.Node, content []byte, filePath string, exportAliases map[string]string) []*Symbol {
	if assignNode == nil || assignNode.Type() != jsNodeAssignmentExpression {
		return nil
	}

	leftNode := assignNode.ChildByFieldName("left")
	rightNode := assignNode.ChildByFieldName("right")

	if leftNode == nil || rightNode == nil {
		return nil
	}

	// Collect all member expression targets from this chain
	type memberTarget struct {
		objectName   string
		propertyName string
		line         int
	}
	var targets []memberTarget

	// Extract the LHS member expression: obj.method
	if leftNode.Type() == jsNodeMemberExpression {
		objNode := leftNode.ChildByFieldName("object")
		propNode := leftNode.ChildByFieldName("property")
		if objNode != nil && propNode != nil && objNode.Type() == jsNodeIdentifier {
			objName := string(content[objNode.StartByte():objNode.EndByte()])
			propName := string(content[propNode.StartByte():propNode.EndByte()])
			targets = append(targets, memberTarget{
				objectName:   objName,
				propertyName: propName,
				line:         int(leftNode.StartPoint().Row) + 1,
			})
		}
	}

	// If the RHS is another assignment expression, recurse to collect more targets and the function
	var funcNode *sitter.Node
	if rightNode.Type() == jsNodeAssignmentExpression {
		// Recurse to get more method targets from the chain
		chainSyms := p.extractMethodsFromAssignmentChain(ctx, rightNode, content, filePath, exportAliases)
		// The recursive call already created symbols; we just need to create our own
		// But we also need the function info. Extract from the deepest RHS.
		funcNode = p.findDeepestRHSFunction(rightNode)
		if funcNode == nil {
			// No function found at end of chain — just return what the recursion found
			// plus create our target if it's an alias
			for _, tgt := range targets {
				semanticType, isAlias := exportAliases[tgt.objectName]
				if !isAlias {
					continue
				}
				// Create a symbol that aliases the one from the chain
				for _, chainSym := range chainSyms {
					if chainSym != nil {
						aliasSym := p.createPrototypeMethodSymbol(
							tgt.propertyName, semanticType, chainSym.Signature,
							chainSym.Calls, chainSym.Metadata,
							filePath, tgt.line, int(assignNode.EndPoint().Row)+1,
							chainSym.DocComment,
						)
						chainSyms = append(chainSyms, aliasSym)
						break
					}
				}
			}
			return chainSyms
		}
	} else {
		// RHS is the function itself (or something else)
		funcNode = rightNode
	}

	// Check if funcNode is a function expression
	if funcNode == nil {
		return nil
	}
	if funcNode.Type() != jsNodeFunctionExpression && funcNode.Type() != jsNodeArrowFunction {
		return nil
	}

	// Extract function details from the RHS
	var funcName string
	var params []string
	var bodyNode *sitter.Node
	isAsync := false
	isGenerator := false

	for i := 0; i < int(funcNode.ChildCount()); i++ {
		child := funcNode.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case jsNodeIdentifier:
			funcName = string(content[child.StartByte():child.EndByte()])
		case jsNodeAsync:
			isAsync = true
		case "*":
			isGenerator = true
		case jsNodeFormalParameters:
			params = p.extractParameters(child, content)
		case jsNodeStatementBlock:
			bodyNode = child
		}
	}

	// Extract calls from function body
	var calls []CallSite
	if bodyNode != nil {
		calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	// Get doc comment from the parent expression_statement (or the assignment itself)
	parentNode := assignNode.Parent()
	docComment := ""
	if parentNode != nil {
		docComment = p.getPrecedingComment(parentNode, content)
	}
	if docComment == "" {
		docComment = p.getPrecedingComment(assignNode, content)
	}

	// Build metadata
	var metadata *SymbolMetadata
	if isAsync || isGenerator {
		metadata = &SymbolMetadata{
			IsAsync:     isAsync,
			IsGenerator: isGenerator,
		}
	}

	// Build signature
	sig := ""
	if isAsync {
		sig += "async "
	}
	if funcName != "" {
		sig += funcName
	}
	if isGenerator {
		sig += "*"
	}
	sig += "(" + strings.Join(params, ", ") + ")"

	// Create symbols for each target that is a known alias
	var symbols []*Symbol
	for _, tgt := range targets {
		semanticType, isAlias := exportAliases[tgt.objectName]
		if !isAlias {
			continue
		}

		// The property name (e.g., "get" in req.get = function header() {}) is the
		// public API name and takes priority over the function expression name.
		methodName := tgt.propertyName

		sym := p.createPrototypeMethodSymbol(
			methodName, semanticType, sig, calls, metadata,
			filePath, tgt.line, int(assignNode.EndPoint().Row)+1,
			docComment,
		)
		symbols = append(symbols, sym)
	}

	return symbols
}

// findDeepestRHSFunction walks the RHS of chained assignments to find the function expression.
func (p *JavaScriptParser) findDeepestRHSFunction(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	if node.Type() == jsNodeFunctionExpression || node.Type() == jsNodeArrowFunction {
		return node
	}
	if node.Type() == jsNodeAssignmentExpression {
		rightNode := node.ChildByFieldName("right")
		return p.findDeepestRHSFunction(rightNode)
	}
	return nil
}

// createPrototypeMethodSymbol creates a Symbol for a prototype-assigned method.
func (p *JavaScriptParser) createPrototypeMethodSymbol(
	methodName string,
	semanticType string,
	signature string,
	calls []CallSite,
	metadata *SymbolMetadata,
	filePath string,
	startLine int,
	endLine int,
	docComment string,
) *Symbol {
	return &Symbol{
		ID:            GenerateID(filePath, startLine, semanticType+"."+methodName),
		Name:          methodName,
		Kind:          SymbolKindMethod,
		FilePath:      filePath,
		StartLine:     startLine,
		EndLine:       endLine,
		StartCol:      0,
		EndCol:        0,
		Signature:     signature,
		DocComment:    docComment,
		Receiver:      semanticType,
		Exported:      true, // module export methods are always public
		Language:      "javascript",
		ParsedAtMilli: time.Now().UnixMilli(),
		Calls:         calls,
		Metadata:      metadata,
	}
}

// deriveSemanticTypeName derives a PascalCase type name from a file path.
//
// Description:
//
//	Converts a file path into a semantic type name following JavaScript conventions:
//	  lib/router/index.js → "Router" (index.js uses parent directory)
//	  lib/application.js  → "Application"
//	  lib/request.js      → "Request"
//	  lib/response.js     → "Response"
//	  src/utils/helper.js → "Helper"
//
// Inputs:
//
//	filePath - Relative file path with forward slashes.
//
// Outputs:
//
//	string - PascalCase type name, or empty string if derivation fails.
func deriveSemanticTypeName(filePath string) string {
	if filePath == "" {
		return ""
	}

	base := filepath.Base(filePath)

	// Remove extension
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// For index files, use the parent directory name
	if name == "index" {
		dir := filepath.Dir(filePath)
		name = filepath.Base(dir)
		// If dir is "." or empty, we can't derive a name
		if name == "." || name == "" {
			return ""
		}
	}

	// Skip if the name is too generic
	if name == "main" || name == "app" || name == "lib" || name == "src" {
		// Still derive — "App", "Main" etc. are valid type names
	}

	// Capitalize first letter (PascalCase)
	if len(name) == 0 {
		return ""
	}

	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// ensureMetadata ensures the metadata object exists.
func ensureMetadata(m *SymbolMetadata) *SymbolMetadata {
	if m == nil {
		return &SymbolMetadata{}
	}
	return m
}
