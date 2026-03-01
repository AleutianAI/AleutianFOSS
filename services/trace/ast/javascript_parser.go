package ast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
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

	// IT-04 Phase 2: Post-pass — mark symbols as Exported when they appear in
	// module.exports assignments. buildModuleExportAliases detects patterns like
	// `module.exports = View` but the export alias is discovered before the function
	// symbol is created (top-to-bottom AST traversal). This post-pass retroactively
	// applies the export status.
	if len(exportAliases) > 0 {
		for _, sym := range result.Symbols {
			if !sym.Exported {
				if _, isExported := exportAliases[sym.Name]; isExported {
					sym.Exported = true
				}
			}
		}
	}

	// IT-06b Issue 1: Post-pass — emit synthetic class symbols for module.exports
	// pseudo-classes. In CommonJS codebases (e.g., Express), patterns like:
	//   var app = exports = module.exports = {}
	//   app.init = function init() { ... }
	// create methods with Receiver="Application" but no parent class symbol.
	// This post-pass creates a synthetic SymbolKindClass for each semantic type name,
	// making them discoverable by find_implementations and find_references.
	if len(exportAliases) > 0 {
		p.emitSyntheticClassSymbols(exportAliases, filePath, result)
	}

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
		sym, dynImps := p.extractFunction(ctx, node, content, filePath, exported)
		if sym != nil {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}
		result.Imports = append(result.Imports, dynImps...)

	case jsNodeClassDeclaration:
		sym, dynImps := p.extractClass(ctx, node, content, filePath, exported)
		if sym != nil {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}
		result.Imports = append(result.Imports, dynImps...)

	case jsNodeLexicalDeclaration, jsNodeVariableDeclaration:
		// Check for CommonJS require() first
		p.extractCommonJSImport(node, content, filePath, result)
		// Then extract variables
		syms, dynImps := p.extractVariables(ctx, node, content, filePath, exported)
		for _, sym := range syms {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}
		result.Imports = append(result.Imports, dynImps...)

	case jsNodeExpressionStatement:
		// IT-01 Phase C: Handle prototype method assignments
		// Patterns: proto.handle = function handle() {...}
		//           app.init = function init() {...}
		//           req.get = req.header = function header() {...}
		// IT-03a Phase 16 J-2: Also handles Constructor.prototype.method without export aliases
		syms, dynImps := p.extractPrototypeMethodAssignment(ctx, node, content, filePath, exportAliases)
		for _, sym := range syms {
			if p.options.IncludePrivate || sym.Exported {
				result.Symbols = append(result.Symbols, sym)
			}
		}
		result.Imports = append(result.Imports, dynImps...)

		// IT-03a B-2: Detect prototype chain inheritance patterns
		p.extractPrototypeInheritance(node, content, filePath, result)

		// IT-06e Bug 2: Detect exports.X = require('./m') direct re-export patterns
		p.extractExportsRequireImport(node, content, filePath, result)

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
			sym, dynImps := p.extractFunction(ctx, child, content, filePath, true)
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
			result.Imports = append(result.Imports, dynImps...)

		case jsNodeClassDeclaration:
			sym, dynImps := p.extractClass(ctx, child, content, filePath, true)
			if sym != nil {
				sym.Exported = true
				if docComment != "" && sym.DocComment == "" {
					sym.DocComment = docComment
				}
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}
			result.Imports = append(result.Imports, dynImps...)

		case jsNodeLexicalDeclaration, jsNodeVariableDeclaration:
			syms, dynImps := p.extractVariables(ctx, child, content, filePath, true)
			for _, sym := range syms {
				sym.Exported = true
				if docComment != "" && sym.DocComment == "" {
					sym.DocComment = docComment
				}
				if p.options.IncludePrivate || sym.Exported {
					result.Symbols = append(result.Symbols, sym)
				}
			}
			result.Imports = append(result.Imports, dynImps...)

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

		case "string", "template_string":
			// IT-03a B-3: This is the source module in a re-export:
			// export { Foo } from './bar'
			// export * from './baz'
			// Create an import to track the module dependency
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
func (p *JavaScriptParser) extractFunction(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool) (*Symbol, []Import) {
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
		return nil, nil
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

	// IT-03a B-1: Detect constructor function pattern (PascalCase + this.x = ...)
	isConstructor := false
	if bodyNode != nil && p.isConstructorFunction(name, bodyNode, content) {
		isConstructor = true
		sym.Kind = SymbolKindClass
	}

	if isAsync || isGenerator || isConstructor {
		sym.Metadata = &SymbolMetadata{
			IsAsync:       isAsync,
			IsGenerator:   isGenerator,
			IsConstructor: isConstructor,
		}
	}

	// GR-41: Extract call sites from function body
	var dynImps []Import
	if bodyNode != nil {
		sym.Calls, dynImps = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	return sym, dynImps
}

// extractClass extracts a class declaration.
func (p *JavaScriptParser) extractClass(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool) (*Symbol, []Import) {
	name := ""
	var extends string
	var children []*Symbol
	var dynImps []Import
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeIdentifier:
			name = string(content[child.StartByte():child.EndByte()])
		case jsNodeClassHeritage:
			extends = p.extractClassHeritage(child, content)
		case jsNodeClassBody:
			children, dynImps = p.extractClassBody(ctx, child, content, filePath, name)
		}
	}

	if name == "" {
		return nil, nil
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

	// IT-03a Phase 12 F-3: Collect MethodSignatures from class Children.
	// Ensures Metadata.Methods is populated for graph integrity.
	p.collectJSClassMethods(sym)

	return sym, dynImps
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
func (p *JavaScriptParser) extractClassBody(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) ([]*Symbol, []Import) {
	var members []*Symbol
	var dynImps []Import

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeMethodDefinition:
			mem, imps := p.extractMethod(ctx, child, content, filePath, className)
			if mem != nil {
				members = append(members, mem)
			}
			dynImps = append(dynImps, imps...)
		case jsNodeFieldDefinition:
			// IT-R2d F.1: extractField now extracts call sites from arrow/function initializers.
			mem, imps := p.extractField(ctx, child, content, filePath, className)
			if mem != nil {
				members = append(members, mem)
			}
			dynImps = append(dynImps, imps...)
		}
	}

	return members, dynImps
}

// extractMethod extracts a method definition from a class.
func (p *JavaScriptParser) extractMethod(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) (*Symbol, []Import) {
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
		return nil, nil
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
	var dynImps []Import
	if bodyNode != nil {
		sym.Calls, dynImps = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	return sym, dynImps
}

// extractField extracts a field definition from a class.
//
// IT-R2d F.1: Now accepts ctx and returns []Import. Detects arrow function or
// function expression initializers in class fields and extracts call sites from
// their bodies.
func (p *JavaScriptParser) extractField(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) (*Symbol, []Import) {
	name := ""
	isStatic := false
	isPrivate := false
	docComment := p.getPrecedingComment(node, content)
	var arrowBodyNode *sitter.Node
	var hasArrowFunction bool

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
		case jsNodeArrowFunction:
			// IT-R2d F.1: Detect arrow function initializer.
			hasArrowFunction = true
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				switch gc.Type() {
				case "statement_block":
					arrowBodyNode = gc
				case "call_expression", "parenthesized_expression", "object", "array",
					"template_string", "binary_expression", "ternary_expression",
					"await_expression", "new_expression":
					if arrowBodyNode == nil {
						arrowBodyNode = gc
					}
				}
			}
		case jsNodeFunctionExpression:
			// IT-R2d F.1: Detect function expression initializer.
			hasArrowFunction = true
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeStatementBlock {
					arrowBodyNode = gc
					break
				}
			}
		}
	}

	if name == "" {
		return nil, nil
	}

	sig := ""
	if isStatic {
		sig += "static "
	}
	sig += name

	kind := SymbolKindField
	if hasArrowFunction {
		kind = SymbolKindProperty
	}

	sym := &Symbol{
		ID:            GenerateID(filePath, int(node.StartPoint().Row)+1, className+"."+name),
		Name:          name,
		Kind:          kind,
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

	// IT-R2d F.1: Extract call sites from arrow/function body.
	var dynImps []Import
	if hasArrowFunction && arrowBodyNode != nil {
		sym.Calls, dynImps = p.extractCallSites(ctx, arrowBodyNode, content, filePath)
	}

	return sym, dynImps
}

// extractVariables extracts variable declarations.
func (p *JavaScriptParser) extractVariables(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool) ([]*Symbol, []Import) {
	var symbols []*Symbol
	var dynImps []Import
	isConst := false
	docComment := p.getPrecedingComment(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case jsNodeConst:
			isConst = true
		case jsNodeVariableDeclarator:
			sym, imps := p.extractVariableDeclarator(ctx, child, content, filePath, exported, isConst, docComment)
			if sym != nil {
				symbols = append(symbols, sym)
			}
			dynImps = append(dynImps, imps...)
		}
	}

	return symbols, dynImps
}

// extractVariableDeclarator extracts a single variable declarator.
func (p *JavaScriptParser) extractVariableDeclarator(ctx context.Context, node *sitter.Node, content []byte, filePath string, exported bool, isConst bool, docComment string) (*Symbol, []Import) {
	name := ""
	isArrowFunction := false
	isAsync := false
	var params []string
	var arrowBodyNode *sitter.Node
	var initializerNode *sitter.Node // non-arrow initializer for dynamic import scanning

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
				case "statement_block":
					// IT-03a Phase 13 J-1: Capture body for call site extraction
					arrowBodyNode = gc
				case "call_expression", "parenthesized_expression", "object", "array",
					"template_string", "binary_expression", "ternary_expression",
					"await_expression", "new_expression":
					// IT-03a Phase 13 J-1: Expression-body arrow functions
					// e.g., const f = (x) => doSomething(x)
					if arrowBodyNode == nil {
						arrowBodyNode = gc
					}
				}
			}
		default:
			// Capture the initializer node for dynamic import scanning (non-arrow cases).
			// Punctuation tokens (=, ;, ,) have single-char types — skip them.
			if len(child.Type()) > 1 {
				initializerNode = child
			}
		}
	}

	if name == "" {
		return nil, nil
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

	// IT-03a Phase 13 J-1: Extract call sites from arrow function body.
	// IT-06e Bug 4: For non-arrow initializer expressions (e.g., const x = import('pkg')
	// or const X = React.lazy(() => import('./H'))), scan the initializer for dynamic
	// import() calls via scanForDynamicImports.
	var dynImps []Import
	if isArrowFunction && arrowBodyNode != nil {
		sym.Calls, dynImps = p.extractCallSites(ctx, arrowBodyNode, content, filePath)
	} else if !isArrowFunction && initializerNode != nil {
		dynImps = p.scanForDynamicImports(initializerNode, content, filePath)
	}

	return sym, dynImps
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
//
// Description:
//
//	Handles three patterns:
//	1. Simple:       const foo = require('bar')
//	2. Destructured: const { Router, Request } = require('express')  (IT-03a Phase 16 J-3)
//	3. Chained:      const foo = require('./utils').isAbsolute        (IT-06e Bug 1)
//
//	Pattern 3 (chained) sets Import.Names = [propertyName] so that the builder's
//	buildImportNameMap and resolveNamedImportEdges can resolve the specific export.
//
// Inputs:
//   - node: A lexical_declaration or variable_declaration AST node.
//   - content: Raw source bytes for text extraction.
//   - filePath: File path for ID generation and Location.
//   - result: ParseResult to append Import entries and import Symbols to.
//
// Outputs:
//
//	None. Appends to result.Imports and result.Symbols in-place.
//
// Thread Safety: Not safe for concurrent use on the same result.
func (p *JavaScriptParser) extractCommonJSImport(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != jsNodeVariableDeclarator {
			continue
		}
		varName := ""
		var destructuredNames []string
		requirePath := ""
		memberPropName := "" // IT-06e Bug 1: property name from require('./m').propName

		for j := 0; j < int(child.ChildCount()); j++ {
			gc := child.Child(j)
			switch gc.Type() {
			case jsNodeIdentifier:
				varName = string(content[gc.StartByte():gc.EndByte()])
			case "object_pattern":
				// IT-03a Phase 16 J-3: const { Router, Request } = require('express')
				destructuredNames = p.extractDestructuredNames(gc, content)
			case jsNodeCallExpression:
				// Simple: const foo = require('bar')
				requirePath = p.extractRequirePath(gc, content)
			case jsNodeMemberExpression:
				// IT-06e Bug 1: const foo = require('./utils').isAbsolute
				// The RHS is member_expression: object=require(...), property=isAbsolute
				memberObj := gc.ChildByFieldName("object")
				memberProp := gc.ChildByFieldName("property")
				if memberObj != nil && memberObj.Type() == jsNodeCallExpression && memberProp != nil {
					rp := p.extractRequirePath(memberObj, content)
					if rp != "" {
						requirePath = rp
						memberPropName = string(content[memberProp.StartByte():memberProp.EndByte()])
					}
				}
			}
		}

		if requirePath == "" {
			continue
		}

		loc := Location{
			FilePath:  filePath,
			StartLine: int(node.StartPoint().Row) + 1,
			EndLine:   int(node.EndPoint().Row) + 1,
			StartCol:  int(node.StartPoint().Column),
			EndCol:    int(node.EndPoint().Column),
		}

		if varName != "" {
			imp := Import{
				Path:       requirePath,
				Alias:      varName,
				IsCommonJS: true,
				Location:   loc,
			}
			if memberPropName != "" {
				// IT-06e Bug 1: chained — var isAbsolute = require('./utils').isAbsolute
				// Names contains the exported name; Alias is the local variable name.
				imp.Names = []string{memberPropName}
			}
			result.Imports = append(result.Imports, imp)
		} else if len(destructuredNames) > 0 {
			// IT-03a Phase 16 J-3: Destructured: const { Router, Request } = require('express')
			for _, name := range destructuredNames {
				result.Imports = append(result.Imports, Import{
					Path:       requirePath,
					Alias:      name,
					IsCommonJS: true,
					Location:   loc,
				})
			}
		}

		// Add import symbol
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

// extractExportsRequireImport detects the direct exports re-export pattern and emits an Import.
//
// Description:
//
//	IT-06e Bug 2: Handles the CommonJS re-export pattern where a module property assignment
//	uses require() as the RHS:
//
//	  exports.query = require('./middleware/query')       // Express lib/express.js line 79
//	  module.exports.init = require('./middleware/init')
//
//	This pattern is an expression_statement (not a var/let/const declaration), so it is
//	invisible to extractCommonJSImport which only walks lexical_declaration nodes.
//
//	When detected, emits Import{Path: requirePath, Alias: propName, IsCommonJS: true}.
//	The Alias is the LHS property name (e.g., "query"), which represents the export key
//	used by the consuming module. The builder's resolveCommonJSAliasImportEdges post-pass
//	uses this Alias to locate the alias variable symbol and create a REFERENCES edge to
//	the required module's exported class.
//
// Inputs:
//   - node: An expression_statement AST node.
//   - content: Raw source bytes for text extraction.
//   - filePath: File path for Location.
//   - result: ParseResult to append Import entries to.
//
// Outputs:
//
//	None. Appends to result.Imports in-place.
//
// Limitations:
//   - Only matches the static string form of require(). Dynamic require(variable) is ignored.
//   - Only matches "exports.X" and "module.exports.X" LHS patterns.
//
// Assumptions:
//   - node is an "expression_statement" node from tree-sitter JavaScript grammar.
func (p *JavaScriptParser) extractExportsRequireImport(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	if node == nil || node.Type() != jsNodeExpressionStatement {
		return
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil || child.Type() != jsNodeAssignmentExpression {
			continue
		}

		leftNode := child.ChildByFieldName("left")
		rightNode := child.ChildByFieldName("right")
		if leftNode == nil || rightNode == nil {
			continue
		}

		// LHS must be a member_expression (exports.X or module.exports.X)
		if leftNode.Type() != jsNodeMemberExpression {
			continue
		}
		leftText := string(content[leftNode.StartByte():leftNode.EndByte()])
		if !strings.HasPrefix(leftText, "exports.") && !strings.HasPrefix(leftText, "module.exports.") {
			continue
		}

		// Extract the property name (e.g. "query" from "exports.query")
		propNode := leftNode.ChildByFieldName("property")
		if propNode == nil {
			continue
		}
		alias := string(content[propNode.StartByte():propNode.EndByte()])
		if alias == "" {
			continue
		}

		// RHS must be a require(str) call
		if rightNode.Type() != jsNodeCallExpression {
			continue
		}
		requirePath := p.extractRequirePath(rightNode, content)
		if requirePath == "" {
			continue
		}

		result.Imports = append(result.Imports, Import{
			Path:       requirePath,
			Alias:      alias,
			IsCommonJS: true,
			Location: Location{
				FilePath:  filePath,
				StartLine: int(node.StartPoint().Row) + 1,
				EndLine:   int(node.EndPoint().Row) + 1,
				StartCol:  int(node.StartPoint().Column),
				EndCol:    int(node.EndPoint().Column),
			},
		})

		slog.Debug("IT-06e Bug 2: exports.X = require() import extracted",
			slog.String("file", filePath),
			slog.String("alias", alias),
			slog.String("path", requirePath),
		)
	}
}

// scanForDynamicImports performs a depth-limited DFS on a subtree to detect
// dynamic import() calls and returns them as a []Import slice.
//
// Description:
//
//	Used to catch dynamic import() calls in variable initializer expressions that
//	are not arrow function bodies — e.g.:
//	  const x = import('some-package')
//	  const X = React.lazy(() => import('./Heavy'))
//
//	Walks at most MaxCallExpressionDepth levels deep to stay bounded. Emits
//	Import{IsDynamic: true, IsModule: true} for each import(stringLiteral) found.
//
// Inputs:
//   - rootNode: The subtree root to scan. May be nil.
//   - content: Raw source bytes for text extraction.
//   - filePath: File path for Location.
//
// Outputs:
//   - []Import: Any dynamic imports found. Nil if none.
//
// Thread Safety: Safe for concurrent use.
func (p *JavaScriptParser) scanForDynamicImports(rootNode *sitter.Node, content []byte, filePath string) []Import {
	if rootNode == nil {
		return nil
	}

	var dynImports []Import

	type stackEntry struct {
		node  *sitter.Node
		depth int
	}

	stack := make([]stackEntry, 0, 16)
	stack = append(stack, stackEntry{node: rootNode, depth: 0})

	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		node := entry.node
		if node == nil {
			continue
		}
		if entry.depth > MaxCallExpressionDepth {
			continue
		}

		if node.Type() == jsNodeCallExpression {
			funcNode := node.ChildByFieldName("function")
			argsNode := node.ChildByFieldName("arguments")
			if funcNode != nil && funcNode.Type() == jsNodeImport && argsNode != nil {
				for i := 0; i < int(argsNode.ChildCount()); i++ {
					arg := argsNode.Child(i)
					if arg != nil && arg.Type() == jsNodeString {
						importPath := p.extractStringContent(arg, content)
						if importPath != "" {
							dynImports = append(dynImports, Import{
								Path:      importPath,
								IsDynamic: true,
								IsModule:  true,
								Location: Location{
									FilePath:  filePath,
									StartLine: int(node.StartPoint().Row) + 1,
									EndLine:   int(node.EndPoint().Row) + 1,
									StartCol:  int(node.StartPoint().Column),
									EndCol:    int(node.EndPoint().Column),
								},
							})
							slog.Debug("IT-06e Bug 4: dynamic import() in initializer expression",
								slog.String("file", filePath),
								slog.String("path", importPath),
							)
						}
						break
					}
				}
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

	return dynImports
}

// extractRequirePath checks if a call expression is require('...') and returns the module path.
//
// Description:
//
//	Inspects a call_expression node to determine if it is a CommonJS require() call.
//	If so, extracts and returns the string argument (the module path).
//
// Inputs:
//   - callNode: A tree-sitter call_expression node. May be nil (returns "").
//   - content: Raw source bytes for text extraction.
//
// Outputs:
//   - string: The module path from require('path'), or "" if not a require call.
//
// Limitations:
//   - Only handles string literal arguments. Dynamic require (require(variable)) returns "".
//   - Only checks the first string argument in the arguments list.
//
// Assumptions:
//   - callNode is a "call_expression" node from tree-sitter JavaScript grammar.
func (p *JavaScriptParser) extractRequirePath(callNode *sitter.Node, content []byte) string {
	if callNode == nil {
		return ""
	}
	for k := 0; k < int(callNode.ChildCount()); k++ {
		child := callNode.Child(k)
		if child.Type() == jsNodeIdentifier && string(content[child.StartByte():child.EndByte()]) == "require" {
			for l := 0; l < int(callNode.ChildCount()); l++ {
				arg := callNode.Child(l)
				if arg.Type() == jsNodeArguments {
					for m := 0; m < int(arg.ChildCount()); m++ {
						argChild := arg.Child(m)
						if argChild.Type() == jsNodeString {
							return p.extractStringContent(argChild, content)
						}
					}
				}
			}
		}
	}
	return ""
}

// extractDestructuredNames extracts local binding names from an object_pattern node.
//
// Description:
//
//	Handles destructured CommonJS imports like const { Router, Request } = require('express').
//	Supports both shorthand patterns ({ Router }) and aliased patterns ({ Router: MyRouter }).
//	For aliased patterns, only the local binding name (right side) is returned.
//
// Inputs:
//   - node: A tree-sitter object_pattern node. Must not be nil.
//   - content: Raw source bytes for text extraction.
//
// Outputs:
//   - []string: Local binding names extracted from the pattern. May be empty.
//
// Limitations:
//   - Does not handle nested destructuring ({ a: { b, c } }).
//   - Does not handle rest elements ({ ...rest }).
//
// Assumptions:
//   - Node is an "object_pattern" from tree-sitter JavaScript grammar.
//   - Children are "shorthand_property_identifier_pattern" or "pair_pattern".
//
// IT-03a Phase 16 J-3.
func (p *JavaScriptParser) extractDestructuredNames(node *sitter.Node, content []byte) []string {
	var names []string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "shorthand_property_identifier_pattern":
			// { Router } — shorthand, name is the identifier itself
			names = append(names, string(content[child.StartByte():child.EndByte()]))
		case "pair_pattern":
			// { Router: MyRouter } — aliased, extract only the value (right side).
			// pair_pattern has children: identifier("Router"), ":", identifier("MyRouter").
			// We want only the last identifier — the local binding name.
			var lastIdent string
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc.Type() == jsNodeIdentifier {
					lastIdent = string(content[gc.StartByte():gc.EndByte()])
				}
			}
			if lastIdent != "" {
				names = append(names, lastIdent)
			}
		}
	}
	return names
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
func (p *JavaScriptParser) extractCallSites(ctx context.Context, bodyNode *sitter.Node, content []byte, filePath string) ([]CallSite, []Import) {
	if bodyNode == nil {
		return nil, nil
	}

	if ctx.Err() != nil {
		return nil, nil
	}

	ctx, span := tracer.Start(ctx, "JavaScriptParser.extractCallSites")
	defer span.End()

	calls := make([]CallSite, 0, 16)
	dynImports := make([]Import, 0)

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
				return calls, dynImports
			}
		}

		if len(calls) >= MaxCallSitesPerSymbol {
			slog.Warn("GR-41: Max call sites per symbol reached in JavaScript",
				slog.String("file", filePath),
				slog.Int("limit", MaxCallSitesPerSymbol),
			)
			return calls, dynImports
		}

		// JavaScript tree-sitter uses "call_expression" for regular calls.
		if node.Type() == jsNodeCallExpression {
			funcNode := node.ChildByFieldName("function")
			argsNode := node.ChildByFieldName("arguments")
			// IT-06e Bug 4: detect dynamic import() calls inline during the AST walk.
			// import() is a call_expression whose function child has type "import".
			if funcNode != nil && funcNode.Type() == jsNodeImport && argsNode != nil {
				for i := 0; i < int(argsNode.ChildCount()); i++ {
					arg := argsNode.Child(i)
					if arg != nil && arg.Type() == jsNodeString {
						importPath := p.extractStringContent(arg, content)
						if importPath != "" {
							dynImports = append(dynImports, Import{
								Path:      importPath,
								IsDynamic: true,
								IsModule:  true,
								Location: Location{
									FilePath:  filePath,
									StartLine: int(node.StartPoint().Row) + 1,
									EndLine:   int(node.EndPoint().Row) + 1,
									StartCol:  int(node.StartPoint().Column),
									EndCol:    int(node.EndPoint().Column),
								},
							})
							slog.Debug("IT-06e Bug 4: dynamic import() in function body",
								slog.String("file", filePath),
								slog.String("path", importPath),
							)
						}
						break
					}
				}
			} else {
				call := p.extractSingleCallSite(node, content, filePath)
				if call != nil && call.Target != "" {
					calls = append(calls, *call)
				}
			}
		}

		// IT-06d Bug 11: Extract constructor calls: new Route(path), new MongoClient(url).
		// These are new_expression nodes distinct from call_expression and were previously
		// skipped silently. Extracting them as call sites produces incoming EdgeTypeCalls
		// on the constructor symbol, making it visible to find_references.
		if node.Type() == jsNodeNewExpression {
			call := p.extractNewExpressionCallSite(node, content, filePath)
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

	return calls, dynImports
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

	// IT-03a C-1: Extract identifier arguments (callback/HOF references)
	argsNode := node.ChildByFieldName("arguments")
	if argsNode != nil {
		call.FunctionArgs = p.extractCallbackArgIdentifiers(argsNode, content)
	}

	return call
}

// extractNewExpressionCallSite extracts a call site from a JavaScript new_expression node.
//
// Description:
//
//	Parses a new_expression node (e.g., `new Route(path, opts)`) and returns a
//	CallSite with the constructor name as the target. This mirrors extractSingleCallSite
//	but uses the "constructor" named field of new_expression rather than the "function"
//	field of call_expression.
//
//	Handles:
//	  - Simple constructors: new ClassName(args)         → Target: "ClassName"
//	  - Qualified constructors: new module.ClassName()   → Target: "ClassName" (leaf only)
//
// Inputs:
//   - node: A new_expression node from tree-sitter-javascript. Must not be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - *CallSite: The extracted call site, or nil if extraction fails.
//
// Thread Safety: Safe for concurrent use.
func (p *JavaScriptParser) extractNewExpressionCallSite(node *sitter.Node, content []byte, filePath string) *CallSite {
	if node == nil || node.Type() != jsNodeNewExpression {
		return nil
	}

	// new_expression named field: "constructor" holds the class identifier or member expression.
	constructorNode := node.ChildByFieldName("constructor")
	if constructorNode == nil {
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
		IsMethod: false,
	}

	switch constructorNode.Type() {
	case "identifier":
		// new ClassName(args)
		call.Target = string(content[constructorNode.StartByte():constructorNode.EndByte()])

	case jsNodeMemberExpression:
		// new module.ClassName(args) — use the property (leaf) as target, object as receiver.
		propertyNode := constructorNode.ChildByFieldName("property")
		objectNode := constructorNode.ChildByFieldName("object")
		if propertyNode != nil {
			call.Target = string(content[propertyNode.StartByte():propertyNode.EndByte()])
		}
		if objectNode != nil {
			call.Receiver = string(content[objectNode.StartByte():objectNode.EndByte()])
			call.IsMethod = true
		}

	default:
		text := string(content[constructorNode.StartByte():constructorNode.EndByte()])
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

// extractCallbackArgIdentifiers extracts identifier arguments from a call's arguments node.
//
// Description:
//
//	Walks the arguments node and extracts top-level identifiers that likely reference
//	functions (PascalCase or known patterns). This enables callback/HOF tracking.
//	Only extracts identifiers, not string literals, numbers, or inline functions.
//
// IT-03a C-1: Enables EdgeTypeReferences from caller to callback arguments.
func (p *JavaScriptParser) extractCallbackArgIdentifiers(argsNode *sitter.Node, content []byte) []string {
	identifiers := make([]string, 0, argsNode.ChildCount())
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		child := argsNode.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case jsNodeIdentifier:
			name := string(content[child.StartByte():child.EndByte()])
			// Skip common non-function identifiers
			if name != "true" && name != "false" && name != "null" && name != "undefined" &&
				name != "this" && name != "console" && name != "window" && name != "document" {
				identifiers = append(identifiers, name)
			}
		case jsNodeMemberExpression:
			// Extract member expression as potential callback: obj.method
			text := string(content[child.StartByte():child.EndByte()])
			if len(text) <= 50 && !strings.Contains(text, "(") {
				identifiers = append(identifiers, text)
			}
		}
	}
	return identifiers
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

// emitSyntheticClassSymbols creates class symbols for CommonJS module.exports pseudo-classes.
//
// Description:
//
//	IT-06b Issue 1: In codebases like Express.js, module.exports patterns create methods
//	with a Receiver (e.g., "Application") but no parent class symbol exists in the index.
//	This makes the semantic type name invisible to tools like find_implementations.
//
//	This method creates a synthetic SymbolKindClass for each unique semantic type name
//	found in exportAliases, collects all methods with a matching Receiver as Children,
//	and uses the original variable's location for the synthetic symbol.
//
// Inputs:
//   - exportAliases: Map from variable names to semantic type names (e.g., "app" → "Application").
//   - filePath: The source file path.
//   - result: The ParseResult to append synthetic symbols to.
//
// Thread Safety: Not safe for concurrent use (modifies result).
func (p *JavaScriptParser) emitSyntheticClassSymbols(exportAliases map[string]string, filePath string, result *ParseResult) {
	// Deduplicate: multiple variables may alias the same semantic type
	// (e.g., "app" and "exports" both → "Application").
	emitted := make(map[string]bool)

	// Sort variable names for deterministic iteration order.
	// When multiple variables alias the same semantic name (e.g., "app" and "exports"
	// both → "Application"), the first alphabetically determines the location used.
	varNames := make([]string, 0, len(exportAliases))
	for varName := range exportAliases {
		varNames = append(varNames, varName)
	}
	sort.Strings(varNames)

	for _, varName := range varNames {
		semanticName := exportAliases[varName]
		if emitted[semanticName] {
			continue
		}

		// Check if a real class/interface symbol with this name already exists.
		// If so, don't emit a synthetic duplicate (this can happen if the file
		// also contains `class Application { ... }`).
		alreadyExists := false
		for _, sym := range result.Symbols {
			if sym.Name == semanticName && (sym.Kind == SymbolKindClass || sym.Kind == SymbolKindInterface) {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		// Find the original variable symbol to get its location.
		var varSym *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == varName && sym.Kind == SymbolKindVariable {
				varSym = sym
				break
			}
		}

		// Determine location: use the variable symbol if found, otherwise use line 1.
		startLine := 1
		endLine := 1
		startCol := 0
		endCol := 0
		if varSym != nil {
			startLine = varSym.StartLine
			endLine = varSym.EndLine
			startCol = varSym.StartCol
			endCol = varSym.EndCol
		}

		// Collect all methods with Receiver == semanticName as Children.
		var children []*Symbol
		for _, sym := range result.Symbols {
			if sym.Receiver == semanticName && (sym.Kind == SymbolKindMethod || sym.Kind == SymbolKindFunction || sym.Kind == SymbolKindProperty) {
				children = append(children, sym)
			}
		}

		syntheticSym := &Symbol{
			ID:         GenerateID(filePath, startLine, semanticName),
			Name:       semanticName,
			Kind:       SymbolKindClass,
			FilePath:   filePath,
			Language:   "javascript",
			Exported:   true,
			StartLine:  startLine,
			EndLine:    endLine,
			StartCol:   startCol,
			EndCol:     endCol,
			Children:   children,
			DocComment: fmt.Sprintf("Synthetic class derived from module.exports alias '%s'.", varName),
		}

		result.Symbols = append(result.Symbols, syntheticSym)
		emitted[semanticName] = true

		slog.Debug("IT-06b: emitted synthetic class symbol",
			slog.String("name", semanticName),
			slog.String("file", filePath),
			slog.String("alias_of", varName),
			slog.Int("children", len(children)),
		)
	}
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
// isConstructorFunction detects the JavaScript constructor function pattern.
//
// Description:
//
//	A constructor function is identified by two criteria:
//	1. The function name starts with an uppercase letter (PascalCase convention)
//	2. The function body contains at least one "this.x = ..." assignment
//
//	Example: function Router() { this.stack = []; this.params = {}; }
//
// IT-03a B-1: Constructor functions are reclassified as SymbolKindClass.
func (p *JavaScriptParser) isConstructorFunction(name string, bodyNode *sitter.Node, content []byte) bool {
	if name == "" || bodyNode == nil {
		return false
	}

	// Check PascalCase: first character must be uppercase
	firstRune, _ := utf8.DecodeRuneInString(name)
	if !unicode.IsUpper(firstRune) {
		return false
	}

	// Check for this.x = ... assignments in the body
	return p.bodyHasThisAssignment(bodyNode, content, 0)
}

// bodyHasThisAssignment checks if a node tree contains "this.x = ..." assignments.
//
// Description:
//
//	Recursively walks the AST looking for assignment_expression nodes whose
//	left-hand side is a member_expression starting with "this.". Skips nested
//	function declarations to avoid false positives from inner constructors.
//
// Inputs:
//   - node: The AST node to search. May be nil.
//   - content: Raw source bytes for extracting node text.
//   - depth: Current recursion depth. Stops at maxThisAssignmentDepth.
//
// Outputs:
//   - bool: True if a "this.x = ..." pattern was found.
//
// Limitations:
//   - Does not distinguish between constructor and non-constructor this usage.
//   - Only recurses into expression_statement and statement_block nodes.
//
// Assumptions:
//   - content is valid UTF-8 and matches the AST node byte ranges.
func (p *JavaScriptParser) bodyHasThisAssignment(node *sitter.Node, content []byte, depth int) bool {
	if node == nil || depth > maxThisAssignmentDepth {
		return false
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}

		// Look for assignment_expression with this.x on left side
		if child.Type() == jsNodeAssignmentExpression {
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc != nil && gc.Type() == jsNodeMemberExpression {
					if int(gc.EndByte()) > len(content) {
						continue
					}
					text := string(content[gc.StartByte():gc.EndByte()])
					if strings.HasPrefix(text, "this.") {
						return true
					}
				}
			}
		}

		// Recurse into expression_statements and blocks (but not nested functions)
		if child.Type() == jsNodeExpressionStatement || child.Type() == jsNodeStatementBlock {
			if p.bodyHasThisAssignment(child, content, depth+1) {
				return true
			}
		}
	}
	return false
}

// extractPrototypeInheritance detects JavaScript prototype-based inheritance patterns
// and records them as metadata on existing symbols in the parse result.
//
// Description:
//
//	Detects two common patterns:
//	1. util.inherits(Child, Parent) — Node.js style
//	2. Child.prototype = Object.create(Parent.prototype) — ES5 style
//
//	When detected, finds the Child symbol in the result and sets its
//	Metadata.Extends to the Parent name, enabling EdgeTypeEmbeds creation.
//
// IT-03a B-2: Enables prototype chain inheritance edges in the graph.
func (p *JavaScriptParser) extractPrototypeInheritance(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	if node == nil || node.Type() != jsNodeExpressionStatement {
		return
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}

		switch child.Type() {
		case "call_expression":
			// Pattern: util.inherits(Child, Parent) or inherits(Child, Parent)
			p.detectUtilInherits(child, content, result)
			// Pattern: Object.assign(Target.prototype, Source.prototype, ...)
			p.detectObjectAssignMixin(child, content, result)
			// Pattern: mixin(target, Source.prototype, ...)
			p.detectMixinCall(child, content, result)
			// IT-06e Bug 3: Pattern: setPrototypeOf(child, parent)
			p.detectSetPrototypeOfInheritance(child, content, result)
		case jsNodeAssignmentExpression:
			// Pattern: Child.prototype = Object.create(Parent.prototype)
			p.detectObjectCreateInheritance(child, content, result)
		}
	}
}

// detectUtilInherits detects util.inherits(Child, Parent) or inherits(Child, Parent).
//
// Description:
//
//	Checks if a call_expression node calls a function named "inherits" or ending with
//	".inherits" (e.g., util.inherits). If so, extracts the first two identifier
//	arguments as child and parent, and sets Extends on the child symbol.
//
// Inputs:
//   - callNode: A call_expression AST node. Must not be nil.
//   - content: Raw source bytes of the file.
//   - result: The ParseResult to update with extends relationships.
//
// Outputs:
//
//	None. Modifies result in-place via setExtendsOnSymbol.
//
// Limitations:
//   - Only matches calls with exactly "inherits" as the function name or suffix.
//   - Only extracts bare identifier arguments, not member expressions.
//
// Assumptions:
//   - callNode is a "call_expression" node.
//
// IT-03a B-2: Enables Node.js-style prototype chain inheritance detection.
func (p *JavaScriptParser) detectUtilInherits(callNode *sitter.Node, content []byte, result *ParseResult) {
	funcNode := callNode.ChildByFieldName("function")
	argsNode := callNode.ChildByFieldName("arguments")
	if funcNode == nil || argsNode == nil {
		return
	}

	// Check if the function is "inherits" or "*.inherits"
	funcText := string(content[funcNode.StartByte():funcNode.EndByte()])
	if funcText != "inherits" && !strings.HasSuffix(funcText, ".inherits") {
		return
	}

	// Extract the two arguments: Child, Parent
	var args []string
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		arg := argsNode.Child(i)
		if arg != nil && arg.Type() == jsNodeIdentifier {
			args = append(args, string(content[arg.StartByte():arg.EndByte()]))
		}
	}
	if len(args) < 2 {
		return
	}

	childName := args[0]
	parentName := args[1]

	// Find the child symbol and set its Extends
	p.setExtendsOnSymbol(result, childName, parentName)
}

// detectObjectCreateInheritance detects Child.prototype = Object.create(Parent.prototype).
//
// Description:
//
//	Checks if an assignment_expression has a left-hand side of the form X.prototype
//	and a right-hand side that is Object.create(Y.prototype). If so, extracts X as
//	the child and Y as the parent, and sets Extends on the child symbol.
//
// Inputs:
//   - assignNode: An assignment_expression AST node. Must not be nil.
//   - content: Raw source bytes of the file.
//   - result: The ParseResult to update with extends relationships.
//
// Outputs:
//
//	None. Modifies result in-place via setExtendsOnSymbol.
//
// Limitations:
//   - Only matches the exact Object.create pattern, not variations like Object['create'].
//
// Assumptions:
//   - assignNode is an "assignment_expression" node.
//
// IT-03a B-2: Enables ES5-style prototype chain inheritance detection.
func (p *JavaScriptParser) detectObjectCreateInheritance(assignNode *sitter.Node, content []byte, result *ParseResult) {
	leftNode := assignNode.ChildByFieldName("left")
	rightNode := assignNode.ChildByFieldName("right")
	if leftNode == nil || rightNode == nil {
		return
	}

	// LHS must be *.prototype
	if leftNode.Type() != jsNodeMemberExpression {
		return
	}
	lObj := leftNode.ChildByFieldName("object")
	lProp := leftNode.ChildByFieldName("property")
	if lObj == nil || lProp == nil || lObj.Type() != jsNodeIdentifier {
		return
	}
	propText := string(content[lProp.StartByte():lProp.EndByte()])
	if propText != "prototype" {
		return
	}
	childName := string(content[lObj.StartByte():lObj.EndByte()])

	// RHS must be Object.create(Parent.prototype)
	if rightNode.Type() != "call_expression" {
		return
	}
	funcNode := rightNode.ChildByFieldName("function")
	argsNode := rightNode.ChildByFieldName("arguments")
	if funcNode == nil || argsNode == nil {
		return
	}
	funcText := string(content[funcNode.StartByte():funcNode.EndByte()])
	if funcText != "Object.create" {
		return
	}

	// First argument should be Parent.prototype
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		arg := argsNode.Child(i)
		if arg == nil {
			continue
		}
		if arg.Type() == jsNodeMemberExpression {
			argObj := arg.ChildByFieldName("object")
			argProp := arg.ChildByFieldName("property")
			if argObj != nil && argProp != nil && argObj.Type() == jsNodeIdentifier {
				argPropText := string(content[argProp.StartByte():argProp.EndByte()])
				if argPropText == "prototype" {
					parentName := string(content[argObj.StartByte():argObj.EndByte()])
					p.setExtendsOnSymbol(result, childName, parentName)
					return
				}
			}
		}
	}
}

// detectSetPrototypeOfInheritance detects setPrototypeOf(child, parent) inheritance.
//
// Description:
//
//	IT-06e Bug 3: Handles the modern ES2015 prototype assignment pattern used by Express:
//
//	  setPrototypeOf(router, proto)                           // lib/router/index.js line 51
//	  setPrototypeOf(this.request, parent.request)           // lib/application.js line 105
//
//	setPrototypeOf(child, parent) is semantically equivalent to util.inherits(child, parent)
//	for establishing prototype chains, but is not handled by the three existing detectors
//	(detectUtilInherits, detectObjectCreateInheritance, detectObjectAssignMixin).
//
//	When detected, calls setExtendsOnSymbol to record the inheritance relationship,
//	enabling EdgeTypeEmbeds creation during the graph build phase.
//
// Inputs:
//   - callNode: A call_expression AST node. Must not be nil.
//   - content: Raw source bytes of the file.
//   - result: The ParseResult to update with extends relationships.
//
// Outputs:
//
//	None. Modifies result in-place via setExtendsOnSymbol.
//
// Limitations:
//   - Only extracts bare identifier arguments. Member expression arguments like
//     this.request and parent.request are not currently extracted.
//   - Requires exactly 2 arguments (ignores calls with more or fewer).
//
// Assumptions:
//   - callNode is a "call_expression" node from tree-sitter JavaScript grammar.
func (p *JavaScriptParser) detectSetPrototypeOfInheritance(callNode *sitter.Node, content []byte, result *ParseResult) {
	funcNode := callNode.ChildByFieldName("function")
	argsNode := callNode.ChildByFieldName("arguments")
	if funcNode == nil || argsNode == nil {
		return
	}

	// Only match the bare name "setPrototypeOf" (not Object.setPrototypeOf variants,
	// which are different from the npm setprototypeof package used by Express)
	funcText := string(content[funcNode.StartByte():funcNode.EndByte()])
	if funcText != "setPrototypeOf" {
		return
	}

	// Extract bare identifier arguments: setPrototypeOf(child, parent)
	var args []string
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		arg := argsNode.Child(i)
		if arg != nil && arg.Type() == jsNodeIdentifier {
			args = append(args, string(content[arg.StartByte():arg.EndByte()]))
		}
	}
	if len(args) < 2 {
		return
	}

	childName := args[0]
	parentName := args[1]
	p.setExtendsOnSymbol(result, childName, parentName)
}

// setExtendsOnSymbol finds a symbol by name in the result and sets its Metadata.Extends.
//
// Description:
//
//	Scans all symbols in the ParseResult for one matching childName and sets
//	its Metadata.Extends field to parentName. Creates Metadata if nil. Stops
//	after the first matching symbol.
//
// Inputs:
//   - result: The ParseResult containing symbols to search. Must not be nil.
//   - childName: The name of the child symbol to find.
//   - parentName: The parent name to set as Extends.
//
// Outputs:
//
//	None. Modifies the matching symbol's Metadata in-place.
//
// Limitations:
//   - Only updates the first symbol with a matching name.
//   - Overwrites any existing Extends value.
//
// Assumptions:
//   - result and result.Symbols are non-nil.
func (p *JavaScriptParser) setExtendsOnSymbol(result *ParseResult, childName, parentName string) {
	for _, sym := range result.Symbols {
		if sym != nil && sym.Name == childName {
			if sym.Metadata == nil {
				sym.Metadata = &SymbolMetadata{}
			}
			sym.Metadata.Extends = parentName
			return
		}
	}
}

// detectObjectAssignMixin detects Object.assign(Target.prototype, Source.prototype, ...) mixin patterns.
//
// Description:
//
//	Detects calls to Object.assign where the first argument is X.prototype (the target)
//	and subsequent arguments are source prototypes or objects. For each source, sets
//	Extends on the target symbol via setExtendsOnSymbol.
//
// Inputs:
//   - callNode: A call_expression AST node. Must not be nil.
//   - content: Raw source bytes of the file.
//   - result: The ParseResult to update with extends relationships.
//
// Outputs:
//
//	None. Modifies result in-place via setExtendsOnSymbol.
//
// Limitations:
//   - Only detects Object.assign where the first argument ends with .prototype.
//   - Sets Extends to the last source found (overwrites previous sources).
//
// Assumptions:
//   - callNode is a "call_expression" node.
//
// IT-03a B-2: Extends prototype inheritance to cover Object.assign mixin patterns.
func (p *JavaScriptParser) detectObjectAssignMixin(callNode *sitter.Node, content []byte, result *ParseResult) {
	funcNode := callNode.ChildByFieldName("function")
	argsNode := callNode.ChildByFieldName("arguments")
	if funcNode == nil || argsNode == nil {
		return
	}

	// Check function text is "Object.assign"
	if int(funcNode.EndByte()) > len(content) || int(funcNode.StartByte()) > len(content) {
		return
	}
	funcText := string(content[funcNode.StartByte():funcNode.EndByte()])
	if funcText != "Object.assign" {
		return
	}

	// Collect non-punctuation arguments
	var args []*sitter.Node
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		arg := argsNode.Child(i)
		if arg == nil {
			continue
		}
		// Skip punctuation nodes (parentheses, commas)
		if arg.Type() == "(" || arg.Type() == ")" || arg.Type() == "," {
			continue
		}
		args = append(args, arg)
	}
	if len(args) < 2 {
		return
	}

	// First argument must be X.prototype (the target)
	firstArg := args[0]
	if firstArg.Type() != jsNodeMemberExpression {
		return
	}
	targetObj := firstArg.ChildByFieldName("object")
	targetProp := firstArg.ChildByFieldName("property")
	if targetObj == nil || targetProp == nil || targetObj.Type() != jsNodeIdentifier {
		return
	}
	if string(content[targetProp.StartByte():targetProp.EndByte()]) != "prototype" {
		return
	}
	targetName := string(content[targetObj.StartByte():targetObj.EndByte()])

	// Remaining arguments are sources
	for _, arg := range args[1:] {
		var sourceName string
		switch arg.Type() {
		case jsNodeMemberExpression:
			// Source.prototype → extract "Source"
			srcObj := arg.ChildByFieldName("object")
			srcProp := arg.ChildByFieldName("property")
			if srcObj != nil && srcProp != nil && srcObj.Type() == jsNodeIdentifier {
				if string(content[srcProp.StartByte():srcProp.EndByte()]) == "prototype" {
					sourceName = string(content[srcObj.StartByte():srcObj.EndByte()])
				}
			}
		case jsNodeIdentifier:
			// Bare identifier as source
			sourceName = string(content[arg.StartByte():arg.EndByte()])
		}
		if sourceName != "" {
			p.setExtendsOnSymbol(result, targetName, sourceName)
		}
	}
}

// detectMixinCall detects mixin(target, Source.prototype, ...) patterns.
//
// Description:
//
//	Detects calls to functions whose name ends with "mixin" (handles bare mixin,
//	_.mixin, utils.mixin, etc.). The first argument is the target identifier,
//	and the second argument is the source (may be X.prototype or a bare identifier).
//
// Inputs:
//   - callNode: A call_expression AST node. Must not be nil.
//   - content: Raw source bytes of the file.
//   - result: The ParseResult to update with extends relationships.
//
// Outputs:
//
//	None. Modifies result in-place via setExtendsOnSymbol.
//
// Limitations:
//   - Only matches functions ending with "mixin" — does not detect arbitrary mixing functions.
//   - Ignores arguments beyond the second (e.g., boolean flags).
//
// Assumptions:
//   - callNode is a "call_expression" node.
//
// IT-03a B-2: Extends prototype inheritance to cover mixin call patterns.
func (p *JavaScriptParser) detectMixinCall(callNode *sitter.Node, content []byte, result *ParseResult) {
	funcNode := callNode.ChildByFieldName("function")
	argsNode := callNode.ChildByFieldName("arguments")
	if funcNode == nil || argsNode == nil {
		return
	}

	// Check function name ends with "mixin"
	if int(funcNode.EndByte()) > len(content) || int(funcNode.StartByte()) > len(content) {
		return
	}
	funcText := string(content[funcNode.StartByte():funcNode.EndByte()])
	if !strings.HasSuffix(funcText, "mixin") {
		return
	}

	// Collect non-punctuation arguments
	var args []*sitter.Node
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		arg := argsNode.Child(i)
		if arg == nil {
			continue
		}
		if arg.Type() == "(" || arg.Type() == ")" || arg.Type() == "," {
			continue
		}
		args = append(args, arg)
	}
	if len(args) < 2 {
		return
	}

	// First argument is the target identifier
	firstArg := args[0]
	var targetName string
	switch firstArg.Type() {
	case jsNodeIdentifier:
		targetName = string(content[firstArg.StartByte():firstArg.EndByte()])
	case jsNodeMemberExpression:
		// Handle target.prototype as well
		targetObj := firstArg.ChildByFieldName("object")
		if targetObj != nil && targetObj.Type() == jsNodeIdentifier {
			targetName = string(content[targetObj.StartByte():targetObj.EndByte()])
		}
	}
	if targetName == "" {
		return
	}

	// Second argument is the source
	secondArg := args[1]
	var sourceName string
	switch secondArg.Type() {
	case jsNodeMemberExpression:
		srcObj := secondArg.ChildByFieldName("object")
		srcProp := secondArg.ChildByFieldName("property")
		if srcObj != nil && srcProp != nil && srcObj.Type() == jsNodeIdentifier {
			if string(content[srcProp.StartByte():srcProp.EndByte()]) == "prototype" {
				sourceName = string(content[srcObj.StartByte():srcObj.EndByte()])
			} else {
				// member expression but not .prototype — use full text
				sourceName = string(content[secondArg.StartByte():secondArg.EndByte()])
			}
		}
	case jsNodeIdentifier:
		sourceName = string(content[secondArg.StartByte():secondArg.EndByte()])
	}
	if sourceName != "" {
		p.setExtendsOnSymbol(result, targetName, sourceName)
	}
}

// Thread Safety: Safe for concurrent use.
func (p *JavaScriptParser) extractPrototypeMethodAssignment(ctx context.Context, node *sitter.Node, content []byte, filePath string, exportAliases map[string]string) ([]*Symbol, []Import) {
	if node == nil || node.Type() != jsNodeExpressionStatement {
		return nil, nil
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
		return nil, nil
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
func (p *JavaScriptParser) extractMethodsFromAssignmentChain(ctx context.Context, assignNode *sitter.Node, content []byte, filePath string, exportAliases map[string]string) ([]*Symbol, []Import) {
	if assignNode == nil || assignNode.Type() != jsNodeAssignmentExpression {
		return nil, nil
	}

	leftNode := assignNode.ChildByFieldName("left")
	rightNode := assignNode.ChildByFieldName("right")

	if leftNode == nil || rightNode == nil {
		return nil, nil
	}

	// Collect all member expression targets from this chain
	type memberTarget struct {
		objectName   string
		propertyName string
		line         int
		isPrototype  bool // IT-03a Phase 16 J-2: true when from Constructor.prototype.method
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
		} else if objNode != nil && propNode != nil && objNode.Type() == jsNodeMemberExpression {
			// Handle Constructor.prototype.method pattern
			innerObj := objNode.ChildByFieldName("object")
			innerProp := objNode.ChildByFieldName("property")
			if innerObj != nil && innerProp != nil && innerObj.Type() == jsNodeIdentifier {
				innerPropName := string(content[innerProp.StartByte():innerProp.EndByte()])
				if innerPropName == "prototype" {
					constructorName := string(content[innerObj.StartByte():innerObj.EndByte()])
					propName := string(content[propNode.StartByte():propNode.EndByte()])
					targets = append(targets, memberTarget{
						objectName:   constructorName,
						propertyName: propName,
						line:         int(leftNode.StartPoint().Row) + 1,
						isPrototype:  true,
					})
				}
			}
		}
	}

	// If the RHS is another assignment expression, recurse to collect more targets and the function
	var funcNode *sitter.Node
	var chainDynImps []Import
	if rightNode.Type() == jsNodeAssignmentExpression {
		// Recurse to get more method targets from the chain
		chainSyms, chainImps := p.extractMethodsFromAssignmentChain(ctx, rightNode, content, filePath, exportAliases)
		chainDynImps = chainImps
		// The recursive call already created symbols; we just need to create our own
		// But we also need the function info. Extract from the deepest RHS.
		funcNode = p.findDeepestRHSFunction(rightNode)
		if funcNode == nil {
			// No function found at end of chain — just return what the recursion found
			// plus create our target if it's an alias or prototype pattern
			for _, tgt := range targets {
				var semanticType string
				if tgt.isPrototype {
					semanticType = tgt.objectName
				} else if alias, isAlias := exportAliases[tgt.objectName]; isAlias {
					semanticType = alias
				} else {
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
			return chainSyms, chainDynImps
		}
	} else {
		// RHS is the function itself (or something else)
		funcNode = rightNode
	}

	// Check if funcNode is a function expression
	if funcNode == nil {
		return nil, chainDynImps
	}
	if funcNode.Type() != jsNodeFunctionExpression && funcNode.Type() != jsNodeArrowFunction {
		return nil, chainDynImps
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
	var localDynImps []Import
	if bodyNode != nil {
		calls, localDynImps = p.extractCallSites(ctx, bodyNode, content, filePath)
	}
	allDynImps := append(chainDynImps, localDynImps...)

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

	// Create symbols for each target that is a known alias or prototype pattern
	var symbols []*Symbol
	for _, tgt := range targets {
		var semanticType string
		if tgt.isPrototype {
			// IT-03a Phase 16 J-2: Constructor.prototype.method — use constructor name directly
			semanticType = tgt.objectName
		} else if alias, isAlias := exportAliases[tgt.objectName]; isAlias {
			semanticType = alias
		} else {
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

	return symbols, allDynImps
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

// collectJSClassMethods converts a class's Children of kind SymbolKindMethod
// into MethodSignature entries in Metadata.Methods.
//
// Description:
//
//	Populates Metadata.Methods for JavaScript ES6 classes, ensuring graph integrity.
//	Skips constructors since they are not part of behavioral contracts.
//	JavaScript has no interface concept, so this doesn't affect implicit matching,
//	but ensures the capability matrix claim of .Methods for JS classes is accurate.
//
// Inputs:
//   - classSym: The class Symbol whose Children have been populated.
//     Must not be nil.
//
// Outputs:
//   - None. Mutates classSym.Metadata.Methods in place.
//
// Limitations:
//   - Only collects instance methods from class body Children.
//   - Prototype methods attached externally are separate symbols, not Children.
//
// Assumptions:
//   - Children have already been populated by extractClassBody.
func (p *JavaScriptParser) collectJSClassMethods(classSym *Symbol) {
	if classSym == nil || len(classSym.Children) == 0 {
		return
	}

	methods := make([]MethodSignature, 0, len(classSym.Children))
	for _, child := range classSym.Children {
		if child.Kind != SymbolKindMethod {
			continue
		}
		// Skip constructor — not a behavioral contract method.
		if child.Name == "constructor" {
			continue
		}
		sig := MethodSignature{
			Name: child.Name,
		}
		methods = append(methods, sig)
	}

	if len(methods) > 0 {
		if classSym.Metadata == nil {
			classSym.Metadata = &SymbolMetadata{}
		}
		classSym.Metadata.Methods = methods
	}
}

// ensureMetadata ensures the metadata object exists.
func ensureMetadata(m *SymbolMetadata) *SymbolMetadata {
	if m == nil {
		return &SymbolMetadata{}
	}
	return m
}
