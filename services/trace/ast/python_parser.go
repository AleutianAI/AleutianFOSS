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
	"github.com/smacker/go-tree-sitter/python"
	"go.opentelemetry.io/otel/attribute"
)

// PythonParserOption configures a PythonParser instance.
type PythonParserOption func(*PythonParser)

// WithPythonMaxFileSize sets the maximum file size the parser will accept.
//
// Parameters:
//   - bytes: Maximum file size in bytes. Must be positive.
//
// Example:
//
//	parser := NewPythonParser(WithPythonMaxFileSize(5 * 1024 * 1024)) // 5MB limit
func WithPythonMaxFileSize(bytes int64) PythonParserOption {
	return func(p *PythonParser) {
		if bytes > 0 {
			p.maxFileSize = bytes
		}
	}
}

// WithPythonParseOptions applies the given ParseOptions to the parser.
//
// Parameters:
//   - opts: ParseOptions to apply.
//
// Example:
//
//	parser := NewPythonParser(WithPythonParseOptions(ParseOptions{IncludePrivate: false}))
func WithPythonParseOptions(opts ParseOptions) PythonParserOption {
	return func(p *PythonParser) {
		p.parseOptions = opts
	}
}

// PythonParser implements the Parser interface for Python source code.
//
// Description:
//
//	PythonParser uses tree-sitter to parse Python source files and extract symbols.
//	It supports concurrent use from multiple goroutines - each Parse call
//	creates its own tree-sitter parser instance internally.
//
// Thread Safety:
//
//	PythonParser instances are safe for concurrent use. Multiple goroutines
//	may call Parse simultaneously on the same PythonParser instance.
//
// Example:
//
//	parser := NewPythonParser()
//	result, err := parser.Parse(ctx, []byte("def hello(): pass"), "main.py")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, sym := range result.Symbols {
//	    fmt.Printf("%s: %s\n", sym.Kind, sym.Name)
//	}
type PythonParser struct {
	maxFileSize  int64
	parseOptions ParseOptions
}

// NewPythonParser creates a new PythonParser with the given options.
//
// Description:
//
//	Creates a PythonParser configured with sensible defaults. Options can be
//	provided to customize behavior such as maximum file size.
//
// Inputs:
//   - opts: Optional configuration functions (WithPythonMaxFileSize, WithPythonParseOptions)
//
// Outputs:
//   - *PythonParser: Configured parser instance, never nil
//
// Example:
//
//	// Default configuration
//	parser := NewPythonParser()
//
//	// Custom max file size
//	parser := NewPythonParser(WithPythonMaxFileSize(5 * 1024 * 1024))
//
// Thread Safety:
//
//	The returned PythonParser is safe for concurrent use.
func NewPythonParser(opts ...PythonParserOption) *PythonParser {
	p := &PythonParser{
		maxFileSize:  DefaultMaxFileSize,
		parseOptions: DefaultParseOptions(),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Parse extracts symbols from Python source code.
//
// Description:
//
//	Parse uses tree-sitter to parse the provided Python source code and extract
//	all symbols (functions, classes, methods, etc.) into a ParseResult.
//	The parser is error-tolerant and will return partial results for syntactically
//	invalid code.
//
// Inputs:
//   - ctx: Context for cancellation. Checked before and after parsing.
//     Note: Tree-sitter parsing itself cannot be interrupted mid-parse.
//   - content: Raw Python source code bytes. Must be valid UTF-8.
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
//	result, err := parser.Parse(ctx, []byte("def hello(): pass"), "main.py")
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Found %d symbols\n", len(result.Symbols))
//
// Limitations:
//   - Tree-sitter parsing is synchronous and cannot be interrupted mid-parse
//   - Very large files may take significant time to parse
//   - Some edge cases in Python syntax may not be fully handled
//
// Assumptions:
//   - Content is valid UTF-8 (validated internally)
//   - FilePath uses forward slashes as path separator
//   - FilePath does not contain path traversal sequences
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *PythonParser) Parse(ctx context.Context, content []byte, filePath string) (*ParseResult, error) {
	// Start tracing span
	ctx, span := startParseSpan(ctx, "python", filePath, len(content))
	defer span.End()

	start := time.Now()

	// Check context before starting
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled before start: %w", err)
	}

	// Validate file size
	if int64(len(content)) > p.maxFileSize {
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
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
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
		return nil, fmt.Errorf("%w: content is not valid UTF-8", ErrInvalidContent)
	}

	// Compute hash before parsing (captures input)
	hash := sha256.Sum256(content)
	hashStr := hex.EncodeToString(hash[:])

	// Create tree-sitter parser (new instance per call for thread safety)
	parser := sitter.NewParser()
	parser.SetLanguage(python.GetLanguage())

	// Parse the content
	tree, err := parser.ParseCtx(ctx, nil, content)
	if err != nil {
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
		return nil, fmt.Errorf("tree-sitter parse failed: %w", err)
	}
	defer tree.Close()

	// Check context after parsing
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
		return nil, fmt.Errorf("parse canceled after tree-sitter: %w", err)
	}

	// Build result
	result := &ParseResult{
		FilePath:      filePath,
		Language:      "python",
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

	// Extract module docstring
	p.extractModuleDocstring(rootNode, content, filePath, result)

	// Extract imports
	p.extractImports(rootNode, content, filePath, result)

	// Extract classes and their methods
	p.extractClasses(ctx, rootNode, content, filePath, result)

	// Extract top-level functions
	p.extractFunctions(ctx, rootNode, content, filePath, result, nil)

	// Extract module-level variables
	p.extractModuleVariables(rootNode, content, filePath, result)

	// Validate result before returning
	if err := result.Validate(); err != nil {
		recordParseMetrics(ctx, "python", time.Since(start), 0, false)
		return nil, fmt.Errorf("result validation failed: %w", err)
	}

	// Check context one final time
	if err := ctx.Err(); err != nil {
		recordParseMetrics(ctx, "python", time.Since(start), len(result.Symbols), false)
		return nil, fmt.Errorf("parse canceled after extraction: %w", err)
	}

	// Record successful parse metrics
	setParseSpanResult(span, len(result.Symbols), len(result.Errors))
	recordParseMetrics(ctx, "python", time.Since(start), len(result.Symbols), true)

	return result, nil
}

// Language returns the canonical language name for this parser.
//
// Returns:
//   - "python" for Python source files
func (p *PythonParser) Language() string {
	return "python"
}

// Extensions returns the file extensions this parser handles.
//
// Returns:
//   - []string{".py", ".pyi"} for Python source and stub files
func (p *PythonParser) Extensions() []string {
	return []string{".py", ".pyi"}
}

// extractModuleDocstring extracts the module-level docstring if present.
func (p *PythonParser) extractModuleDocstring(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	// Module docstring is the first expression_statement with a string child
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "expression_statement" {
			// Check if first child is a string
			if child.ChildCount() > 0 {
				strNode := child.Child(0)
				if strNode.Type() == "string" {
					docstring := p.extractStringContent(strNode, content)

					sym := &Symbol{
						ID:         GenerateID(filePath, int(child.StartPoint().Row+1), "__module__"),
						Name:       "__module__",
						Kind:       SymbolKindPackage,
						FilePath:   filePath,
						Language:   "python",
						Exported:   true,
						DocComment: docstring,
						StartLine:  int(child.StartPoint().Row + 1),
						EndLine:    int(child.EndPoint().Row + 1),
						StartCol:   int(child.StartPoint().Column),
						EndCol:     int(child.EndPoint().Column),
					}
					result.Symbols = append(result.Symbols, sym)
					return
				}
			}
		}
		// Stop looking after non-string, non-comment, non-import first statement
		if child.Type() != "comment" && child.Type() != "import_statement" && child.Type() != "import_from_statement" {
			return
		}
	}
}

// extractImports extracts import statements from the AST.
//
// Description:
//
//	R3-P2b-Inline: Walks the ENTIRE AST tree (not just top-level children) to capture
//	inline imports inside function bodies. Python uses inline imports to avoid circular
//	dependencies, and these must be visible to the import name map for call resolution.
func (p *PythonParser) extractImports(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	p.extractImportsRecursive(root, content, filePath, result, 0)
}

// extractImportsRecursive walks the AST tree and extracts all import statements.
func (p *PythonParser) extractImportsRecursive(node *sitter.Node, content []byte, filePath string, result *ParseResult, depth int) {
	if node == nil || depth > MaxCallExpressionDepth {
		return
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "import_statement":
			p.processImportStatement(child, content, filePath, result)
		case "import_from_statement":
			p.processImportFromStatement(child, content, filePath, result)
		default:
			// Recurse into other nodes (function bodies, if blocks, etc.)
			p.extractImportsRecursive(child, content, filePath, result, depth+1)
		}
	}
}

// processImportStatement handles 'import foo' or 'import foo as bar' style imports.
func (p *PythonParser) processImportStatement(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	// import_statement contains dotted_name or aliased_import children
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "dotted_name":
			path := string(content[child.StartByte():child.EndByte()])
			p.addImport(node, path, "", nil, false, false, filePath, result)
		case "aliased_import":
			var path, alias string
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				switch grandchild.Type() {
				case "dotted_name":
					path = string(content[grandchild.StartByte():grandchild.EndByte()])
				case "identifier":
					alias = string(content[grandchild.StartByte():grandchild.EndByte()])
				}
			}
			if path != "" {
				p.addImport(node, path, alias, nil, false, false, filePath, result)
			}
		}
	}
}

// processImportFromStatement handles 'from x import y' style imports.
func (p *PythonParser) processImportFromStatement(node *sitter.Node, content []byte, filePath string, result *ParseResult) {
	var modulePath string
	var names []string
	var isWildcard bool
	var isRelative bool
	var sawImport bool // Track when we've seen the "import" keyword

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "from":
			// Skip the "from" keyword
		case "import":
			// Mark that we've seen import - subsequent dotted_name/identifier are imported names
			sawImport = true
		case "relative_import":
			isRelative = true
			// relative_import contains import_prefix (dots) and optionally dotted_name
			var prefix string
			var name string
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				switch grandchild.Type() {
				case "import_prefix":
					prefix = string(content[grandchild.StartByte():grandchild.EndByte()])
				case "dotted_name":
					name = string(content[grandchild.StartByte():grandchild.EndByte()])
				}
			}
			modulePath = prefix + name
		case "dotted_name":
			nameStr := string(content[child.StartByte():child.EndByte()])
			if !sawImport {
				// Before "import" keyword - this is the module path
				modulePath = nameStr
			} else {
				// After "import" keyword - this is an imported name
				names = append(names, nameStr)
			}
		case "wildcard_import":
			isWildcard = true
		case "aliased_import":
			// from x import y as z
			var importName, alias string
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				switch grandchild.Type() {
				case "identifier":
					if importName == "" {
						importName = string(content[grandchild.StartByte():grandchild.EndByte()])
					} else {
						alias = string(content[grandchild.StartByte():grandchild.EndByte()])
					}
				case "dotted_name":
					if importName == "" {
						importName = string(content[grandchild.StartByte():grandchild.EndByte()])
					}
				}
			}
			if alias != "" {
				names = append(names, importName+" as "+alias)
			} else if importName != "" {
				names = append(names, importName)
			}
		case "identifier":
			if sawImport {
				names = append(names, string(content[child.StartByte():child.EndByte()]))
			}
		}
	}

	if modulePath != "" || isRelative {
		if modulePath == "" && isRelative {
			modulePath = "."
		}
		p.addImport(node, modulePath, "", names, isWildcard, isRelative, filePath, result)
	}
}

// addImport adds an import to the result (both Import struct and Symbol).
func (p *PythonParser) addImport(node *sitter.Node, path, alias string, names []string, isWildcard, isRelative bool, filePath string, result *ParseResult) {
	startLine := int(node.StartPoint().Row + 1)
	endLine := int(node.EndPoint().Row + 1)
	startCol := int(node.StartPoint().Column)
	endCol := int(node.EndPoint().Column)

	imp := Import{
		Path:       path,
		Alias:      alias,
		Names:      names,
		IsWildcard: isWildcard,
		IsRelative: isRelative,
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
		ID:        GenerateID(filePath, startLine, path),
		Name:      path,
		Kind:      SymbolKindImport,
		FilePath:  filePath,
		Language:  "python",
		Exported:  true,
		StartLine: startLine,
		EndLine:   endLine,
		StartCol:  startCol,
		EndCol:    endCol,
	}
	result.Symbols = append(result.Symbols, sym)
}

// extractClasses extracts class definitions from the AST.
func (p *PythonParser) extractClasses(ctx context.Context, root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "class_definition":
			p.processClass(ctx, child, content, filePath, result, nil)
		case "decorated_definition":
			p.processDecoratedDefinition(ctx, child, content, filePath, result, nil)
		}
	}
}

// processClass extracts a class definition.
func (p *PythonParser) processClass(ctx context.Context, node *sitter.Node, content []byte, filePath string, result *ParseResult, decorators []string) *Symbol {
	var name string
	var bases []string
	var bodyNode *sitter.Node
	var docstring string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "argument_list":
			// Extract base classes
			for j := 0; j < int(child.ChildCount()); j++ {
				arg := child.Child(j)
				switch arg.Type() {
				case "identifier":
					bases = append(bases, string(content[arg.StartByte():arg.EndByte()]))
				case "attribute":
					// IT-06b Issue 4: Qualified names like "generic.NDFrame" must be
					// stripped to bare name "NDFrame" for index lookup compatibility.
					// The index stores symbols by bare name, not qualified path.
					fullName := string(content[arg.StartByte():arg.EndByte()])
					if dotIdx := strings.LastIndex(fullName, "."); dotIdx >= 0 {
						bases = append(bases, fullName[dotIdx+1:])
					} else {
						bases = append(bases, fullName)
					}
				case "subscript":
					// IT-03a Phase 15 P-1: Handle Protocol[T], Generic[T], etc.
					// Extract the base name before the bracket (e.g., "Protocol" from "Protocol[T]")
					baseName := extractSubscriptBaseName(arg, content)
					if baseName != "" {
						bases = append(bases, baseName)
					}
				case "keyword_argument":
					// IT-03a Phase 15 P-2: Handle metaclass=ABCMeta
					p.extractKeywordBaseClass(arg, content, &bases)
				}
			}
		case "block":
			bodyNode = child
		}
	}

	if name == "" {
		return nil
	}

	exported := p.isExported(name)
	if !p.parseOptions.IncludePrivate && !exported {
		return nil
	}

	// Extract docstring from body
	if bodyNode != nil {
		docstring = p.extractDocstring(bodyNode, content)
	}

	// GR-40a: Check if this is a Protocol class (structural interface)
	// Check for typing.Protocol first (pure structural interface)
	isTypingProtocol := p.isTypingProtocol(ctx, bases)
	// Check for ABC (requires @abstractmethod check after member extraction)
	isABC := p.isABCClass(ctx, bases)

	kind := SymbolKindClass
	if isTypingProtocol {
		kind = SymbolKindInterface
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       kind,
		FilePath:   filePath,
		Language:   "python",
		Exported:   exported,
		DocComment: docstring,
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
		Children:   make([]*Symbol, 0),
	}

	// Add metadata for decorators and bases
	if len(decorators) > 0 || len(bases) > 0 {
		sym.Metadata = &SymbolMetadata{
			Decorators: decorators,
		}
		if len(bases) > 0 {
			sym.Metadata.Extends = bases[0]
			if len(bases) > 1 {
				sym.Metadata.Implements = bases[1:]
			}
		}
	}

	// Extract methods and class variables from body
	if bodyNode != nil {
		p.extractClassMembers(ctx, bodyNode, content, filePath, sym)
	}

	// GR-40a M-1: For ABC classes, only mark as interface if they have @abstractmethod
	if isABC && !isTypingProtocol {
		if p.hasAbstractMethod(ctx, sym) {
			sym.Kind = SymbolKindInterface
			slog.Debug("GR-40a: ABC class with @abstractmethod marked as interface",
				slog.String("class", name),
			)
		} else {
			slog.Debug("GR-40a: ABC class without @abstractmethod remains a class",
				slog.String("class", name),
			)
		}
	}

	// GR-40a: Collect method signatures for Protocol implementation detection
	p.collectPythonClassMethods(ctx, sym)

	result.Symbols = append(result.Symbols, sym)
	return sym
}

// extractClassMembers extracts methods and class variables from a class body.
func (p *PythonParser) extractClassMembers(ctx context.Context, body *sitter.Node, content []byte, filePath string, classSym *Symbol) {
	for i := 0; i < int(body.ChildCount()); i++ {
		child := body.Child(i)
		switch child.Type() {
		case "function_definition":
			if method := p.processMethod(ctx, child, content, filePath, nil, classSym.Name); method != nil {
				classSym.Children = append(classSym.Children, method)
			}
		case "decorated_definition":
			if method := p.processDecoratedMethod(ctx, child, content, filePath, classSym.Name); method != nil {
				classSym.Children = append(classSym.Children, method)
			}
		case "expression_statement":
			// Class-level variable assignments
			if child.ChildCount() > 0 {
				assign := child.Child(0)
				if assign.Type() == "assignment" || assign.Type() == "augmented_assignment" {
					if field := p.processClassVariable(assign, content, filePath); field != nil {
						classSym.Children = append(classSym.Children, field)
					}
				}
			}
		}
	}
}

// processClassVariable extracts a class variable (field).
func (p *PythonParser) processClassVariable(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var typeStr string
	var typeRefs []TypeReference // IT-06 Bug 9

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = string(content[child.StartByte():child.EndByte()])
			}
		case "type":
			typeStr = string(content[child.StartByte():child.EndByte()])
			// IT-06 Bug 9: Extract type refs from variable annotation
			typeRefs = extractTypeRefsFromAnnotation(child, content, filePath)
		}
	}

	if name == "" {
		return nil
	}

	exported := p.isExported(name)
	if !p.parseOptions.IncludePrivate && !exported {
		return nil
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      SymbolKindField,
		FilePath:  filePath,
		Language:  "python",
		Exported:  exported,
		Signature: typeStr,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}

	// IT-06 Bug 9: Set type annotation references
	if len(typeRefs) > 0 {
		sym.TypeReferences = typeRefs
	}

	return sym
}

// processMethod extracts a method from a class.
func (p *PythonParser) processMethod(ctx context.Context, node *sitter.Node, content []byte, filePath string, decorators []string, className string) *Symbol {
	return p.processFunction(ctx, node, content, filePath, decorators, className)
}

// processDecoratedMethod extracts a decorated method.
func (p *PythonParser) processDecoratedMethod(ctx context.Context, node *sitter.Node, content []byte, filePath string, className string) *Symbol {
	decorators, decoratorArgs := p.extractDecoratorsWithArgs(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "function_definition" {
			sym := p.processMethod(ctx, child, content, filePath, decorators, className)
			// IT-03a A-3: Attach decorator arguments
			if sym != nil && len(decoratorArgs) > 0 {
				if sym.Metadata == nil {
					sym.Metadata = &SymbolMetadata{}
				}
				sym.Metadata.DecoratorArgs = decoratorArgs
			}
			return sym
		}
	}

	return nil
}

// extractFunctions extracts top-level function definitions.
func (p *PythonParser) extractFunctions(ctx context.Context, root *sitter.Node, content []byte, filePath string, result *ParseResult, parent *Symbol) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "function_definition":
			if fn := p.processFunction(ctx, child, content, filePath, nil, ""); fn != nil {
				// Extract nested functions
				p.extractNestedFunctions(ctx, child, content, filePath, fn)
				result.Symbols = append(result.Symbols, fn)
			}
		case "decorated_definition":
			// Check if it's a function (not a class)
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				if grandchild.Type() == "function_definition" {
					decorators, decoratorArgs := p.extractDecoratorsWithArgs(child, content)
					if fn := p.processFunction(ctx, grandchild, content, filePath, decorators, ""); fn != nil {
						// IT-03a A-3: Attach decorator arguments
						if len(decoratorArgs) > 0 {
							if fn.Metadata == nil {
								fn.Metadata = &SymbolMetadata{}
							}
							fn.Metadata.DecoratorArgs = decoratorArgs
						}
						// Extract nested functions
						p.extractNestedFunctions(ctx, grandchild, content, filePath, fn)
						result.Symbols = append(result.Symbols, fn)
					}
					break
				}
			}
		}
	}
}

// extractNestedFunctions extracts nested function definitions.
func (p *PythonParser) extractNestedFunctions(ctx context.Context, funcNode *sitter.Node, content []byte, filePath string, parentFn *Symbol) {
	// Find the block node
	for i := 0; i < int(funcNode.ChildCount()); i++ {
		child := funcNode.Child(i)
		if child.Type() == "block" {
			for j := 0; j < int(child.ChildCount()); j++ {
				stmt := child.Child(j)
				if stmt.Type() == "function_definition" {
					if nested := p.processFunction(ctx, stmt, content, filePath, nil, ""); nested != nil {
						parentFn.Children = append(parentFn.Children, nested)
					}
				} else if stmt.Type() == "decorated_definition" {
					decorators := p.extractDecorators(stmt, content)
					for k := 0; k < int(stmt.ChildCount()); k++ {
						def := stmt.Child(k)
						if def.Type() == "function_definition" {
							if nested := p.processFunction(ctx, def, content, filePath, decorators, ""); nested != nil {
								parentFn.Children = append(parentFn.Children, nested)
							}
							break
						}
					}
				}
			}
			break
		}
	}
}

// processFunction extracts a function definition.
func (p *PythonParser) processFunction(ctx context.Context, node *sitter.Node, content []byte, filePath string, decorators []string, className string) *Symbol {
	var name string
	var params string
	var returnType string
	var docstring string
	var isAsync bool
	var bodyNode *sitter.Node
	var typeRefs []TypeReference // IT-06 Bug 9: type annotation references

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "async":
			// async keyword is a child of function_definition in tree-sitter-python
			isAsync = true
		case "identifier":
			name = string(content[child.StartByte():child.EndByte()])
		case "parameters":
			params = string(content[child.StartByte():child.EndByte()])
			// IT-06 Bug 9: Extract type refs from parameter annotations
			paramRefs := extractParamTypeRefs(child, content, filePath)
			typeRefs = append(typeRefs, paramRefs...)
		case "type":
			returnType = string(content[child.StartByte():child.EndByte()])
			// IT-06 Bug 9: Extract type refs from return type annotation
			retRefs := extractTypeRefsFromAnnotation(child, content, filePath)
			typeRefs = append(typeRefs, retRefs...)
		case "block":
			bodyNode = child
			docstring = p.extractDocstring(child, content)
		}
	}

	if name == "" {
		return nil
	}

	exported := p.isExported(name)
	if !p.parseOptions.IncludePrivate && !exported {
		return nil
	}

	// Determine kind based on decorators and context
	kind := SymbolKindFunction
	isStatic := false
	if className != "" {
		kind = SymbolKindMethod
	}

	// Check for special decorators
	var isOverload bool
	for _, dec := range decorators {
		switch dec {
		case "property":
			kind = SymbolKindProperty
		case "staticmethod":
			isStatic = true
		case "classmethod":
			isStatic = true
		case "overload":
			// IT-06c H-3: Mark @overload stubs so symbol resolution can prefer
			// the real implementation (which has actual callees).
			isOverload = true
		}
	}

	// Build signature
	var signature string
	if isAsync {
		signature = fmt.Sprintf("async def %s%s", name, params)
	} else {
		signature = fmt.Sprintf("def %s%s", name, params)
	}
	if returnType != "" {
		signature += " -> " + returnType
	}

	sym := &Symbol{
		ID:         GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:       name,
		Kind:       kind,
		FilePath:   filePath,
		Language:   "python",
		Exported:   exported,
		Signature:  signature,
		DocComment: docstring,
		Receiver:   className, // IT-01 Phase C: Set Receiver for graph builder receiver resolution
		StartLine:  int(node.StartPoint().Row + 1),
		EndLine:    int(node.EndPoint().Row + 1),
		StartCol:   int(node.StartPoint().Column),
		EndCol:     int(node.EndPoint().Column),
	}

	// Add metadata
	if len(decorators) > 0 || returnType != "" || isAsync || isStatic || isOverload {
		sym.Metadata = &SymbolMetadata{
			Decorators: decorators,
			ReturnType: returnType,
			IsAsync:    isAsync,
			IsStatic:   isStatic,
			IsOverload: isOverload,
		}
	}

	// GR-41: Extract call sites from function/method body
	if bodyNode != nil {
		sym.Calls = p.extractCallSites(ctx, bodyNode, content, filePath)
	}

	// IT-06 Bug 9: Set type annotation references
	if len(typeRefs) > 0 {
		sym.TypeReferences = typeRefs
	}

	return sym
}

// processDecoratedDefinition handles decorated classes and functions at module level.
func (p *PythonParser) processDecoratedDefinition(ctx context.Context, node *sitter.Node, content []byte, filePath string, result *ParseResult, parent *Symbol) {
	decorators, decoratorArgs := p.extractDecoratorsWithArgs(node, content)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "class_definition":
			if sym := p.processClass(ctx, child, content, filePath, result, decorators); sym != nil {
				// IT-03a A-3: Attach decorator arguments
				if len(decoratorArgs) > 0 {
					if sym.Metadata == nil {
						sym.Metadata = &SymbolMetadata{}
					}
					sym.Metadata.DecoratorArgs = decoratorArgs
				}
			}
		case "function_definition":
			// Handled in extractFunctions
		}
	}
}

// extractDecorators extracts decorator names from a decorated_definition.
func (p *PythonParser) extractDecorators(node *sitter.Node, content []byte) []string {
	decorators := make([]string, 0)

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "decorator" {
			// Get the decorator name (identifier or attribute)
			for j := 0; j < int(child.ChildCount()); j++ {
				grandchild := child.Child(j)
				switch grandchild.Type() {
				case "identifier":
					decorators = append(decorators, string(content[grandchild.StartByte():grandchild.EndByte()]))
				case "attribute":
					decorators = append(decorators, string(content[grandchild.StartByte():grandchild.EndByte()]))
				case "call":
					// Decorator with arguments: @foo(x)
					for k := 0; k < int(grandchild.ChildCount()); k++ {
						ggchild := grandchild.Child(k)
						if ggchild.Type() == "identifier" || ggchild.Type() == "attribute" {
							decorators = append(decorators, string(content[ggchild.StartByte():ggchild.EndByte()]))
							break
						}
					}
				}
			}
		}
	}

	return decorators
}

// pythonTypeSkipList contains Python primitive types and typing constructs that should
// not produce TypeReference entries. These are language-level constructs, not user-defined types.
var pythonTypeSkipList = map[string]bool{
	// Python builtins
	"str": true, "int": true, "float": true, "bool": true, "bytes": true,
	"None": true, "object": true, "type": true, "complex": true,
	// typing module constructs
	"Optional": true, "Union": true, "List": true, "Dict": true,
	"Tuple": true, "Set": true, "FrozenSet": true, "Type": true,
	"Callable": true, "Any": true, "Sequence": true, "Mapping": true,
	"Iterable": true, "Iterator": true, "Generator": true, "Coroutine": true,
	"Awaitable": true, "AsyncIterator": true, "AsyncIterable": true,
	"AsyncGenerator": true, "ClassVar": true, "Final": true,
	"Literal": true, "TypeVar": true, "Generic": true, "Protocol": true,
	"Annotated": true, "TypeAlias": true, "TypeGuard": true,
	"Concatenate": true, "ParamSpec": true, "Self": true,
	"Unpack": true, "Required": true, "NotRequired": true,
	// collections.abc aliases
	"MutableMapping": true, "MutableSequence": true, "MutableSet": true,
	"AbstractSet": true, "Hashable": true, "Sized": true,
	"Collection": true, "Reversible": true, "SupportsInt": true,
	"SupportsFloat": true, "SupportsComplex": true, "SupportsBytes": true,
	"SupportsAbs": true, "SupportsRound": true,
}

// extractTypeRefsFromAnnotation walks a tree-sitter "type" node and extracts
// non-primitive type identifiers as TypeReference entries.
//
// Description:
//
//	Recursively walks the type annotation AST node to find identifier nodes
//	representing user-defined types. Skips Python primitives and typing module
//	constructs (Optional, List, Dict, etc.) since those are language plumbing,
//	not user-defined types that would be meaningful for find_references.
//
// IT-06 Bug 9: This enables graph edges from type annotations.
//
// Inputs:
//   - typeNode: The tree-sitter "type" node from a function return type,
//     parameter annotation, or variable annotation.
//   - content: Source file bytes.
//   - filePath: Relative path for Location.
//
// Outputs:
//   - []TypeReference: Non-primitive type identifiers found in the annotation.
func extractTypeRefsFromAnnotation(typeNode *sitter.Node, content []byte, filePath string) []TypeReference {
	if typeNode == nil {
		return nil
	}

	var refs []TypeReference
	seen := make(map[string]bool) // deduplicate within one annotation

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil || len(refs) >= MaxTypeReferencesPerSymbol {
			return
		}

		switch node.Type() {
		case "identifier":
			name := string(content[node.StartByte():node.EndByte()])
			if !pythonTypeSkipList[name] && !seen[name] {
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
		case "attribute":
			// For typing.List, typing.Optional — extract the leaf attribute name
			// and check if it's in the skip list. If "List", skip it.
			// If "mymodule.MyType", we want "MyType" but it's tricky to get just the leaf.
			// Use the last identifier child as the name.
			for i := int(node.ChildCount()) - 1; i >= 0; i-- {
				child := node.Child(i)
				if child.Type() == "identifier" {
					name := string(content[child.StartByte():child.EndByte()])
					if !pythonTypeSkipList[name] && !seen[name] {
						seen[name] = true
						refs = append(refs, TypeReference{
							Name: name,
							Location: Location{
								FilePath:  filePath,
								StartLine: int(child.StartPoint().Row + 1),
								EndLine:   int(child.EndPoint().Row + 1),
								StartCol:  int(child.StartPoint().Column),
								EndCol:    int(child.EndPoint().Column),
							},
						})
					}
					break // only take the leaf identifier
				}
			}
			return // don't recurse into attribute children — we handled it
		}

		// Recurse into children for subscript (List[X]), binary_operator (X | Y), etc.
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}

	walk(typeNode)
	return refs
}

// extractParamTypeRefs extracts TypeReference entries from function parameter type annotations.
//
// Description:
//
//	Walks the tree-sitter "parameters" node and finds typed_parameter and
//	typed_default_parameter children. For each, extracts the "type" child
//	and runs extractTypeRefsFromAnnotation on it.
//
// IT-06 Bug 9: Enables graph edges from parameter type annotations like "def foo(x: Series)".
//
// Inputs:
//   - paramsNode: The tree-sitter "parameters" node.
//   - content: Source file bytes.
//   - filePath: Relative path for Location.
//
// Outputs:
//   - []TypeReference: Non-primitive type identifiers from all parameter annotations.
func extractParamTypeRefs(paramsNode *sitter.Node, content []byte, filePath string) []TypeReference {
	if paramsNode == nil {
		return nil
	}

	var refs []TypeReference

	for i := 0; i < int(paramsNode.ChildCount()); i++ {
		child := paramsNode.Child(i)

		switch child.Type() {
		case pyNodeTypedParameter, pyNodeTypedDefaultParameter:
			// Look for the "type" child within the typed parameter
			for j := 0; j < int(child.ChildCount()); j++ {
				typeChild := child.Child(j)
				if typeChild.Type() == pyNodeType {
					paramRefs := extractTypeRefsFromAnnotation(typeChild, content, filePath)
					refs = append(refs, paramRefs...)
					break
				}
			}
		}
	}

	return refs
}

// extractDecoratorsWithArgs extracts decorator names and their argument identifiers.
//
// Description:
//
//	Similar to extractDecorators but also captures identifier arguments from decorator calls.
//	For @app.route("/users"), returns names=["app.route"], args={}.
//	For @UseInterceptors(LoggingInterceptor), returns names=["UseInterceptors"],
//	args={"UseInterceptors": ["LoggingInterceptor"]}.
//
// IT-03a A-3: Enables EdgeTypeReferences from decorated symbol to decorator arguments.
func (p *PythonParser) extractDecoratorsWithArgs(node *sitter.Node, content []byte) ([]string, map[string][]string) {
	decorators := make([]string, 0)
	var decoratorArgs map[string][]string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != "decorator" {
			continue
		}
		for j := 0; j < int(child.ChildCount()); j++ {
			grandchild := child.Child(j)
			switch grandchild.Type() {
			case "identifier":
				decorators = append(decorators, string(content[grandchild.StartByte():grandchild.EndByte()]))
			case "attribute":
				decorators = append(decorators, string(content[grandchild.StartByte():grandchild.EndByte()]))
			case "call":
				var name string
				var args []string
				for k := 0; k < int(grandchild.ChildCount()); k++ {
					ggchild := grandchild.Child(k)
					switch ggchild.Type() {
					case "identifier":
						if name == "" {
							name = string(content[ggchild.StartByte():ggchild.EndByte()])
						}
					case "attribute":
						if name == "" {
							name = string(content[ggchild.StartByte():ggchild.EndByte()])
						}
					case "argument_list":
						args = p.extractDecoratorArgIdentifiers(ggchild, content)
					}
				}
				if name != "" {
					decorators = append(decorators, name)
					if len(args) > 0 {
						if decoratorArgs == nil {
							decoratorArgs = make(map[string][]string)
						}
						decoratorArgs[name] = args
					}
				}
			}
		}
	}

	return decorators, decoratorArgs
}

// extractDecoratorArgIdentifiers extracts identifier arguments from a Python decorator's argument list.
//
// Description:
//
//	Walks the argument_list node and extracts top-level identifiers.
//	Skips string literals, numbers, keyword arguments (unless value is an identifier),
//	and other non-identifier arguments. Also recurses into list arguments to find
//	nested identifiers.
//
// Inputs:
//   - argsNode: The argument_list AST node. May be nil (returns nil).
//   - content: Raw source bytes for extracting identifier text.
//
// Outputs:
//   - []string: Extracted identifier names. May be empty but not nil if argsNode is non-nil.
//
// Limitations:
//   - Only extracts identifiers from keyword_argument values and list children.
//   - Does not recurse into nested calls or complex expressions.
//   - Skips Python boolean/None constants (True, False, None).
//
// Assumptions:
//   - content is valid UTF-8 and matches the AST node byte ranges.
func (p *PythonParser) extractDecoratorArgIdentifiers(argsNode *sitter.Node, content []byte) []string {
	if argsNode == nil {
		return nil
	}
	identifiers := make([]string, 0, argsNode.ChildCount())
	for i := 0; i < int(argsNode.ChildCount()); i++ {
		child := argsNode.Child(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "identifier":
			name := string(content[child.StartByte():child.EndByte()])
			if name != "True" && name != "False" && name != "None" {
				identifiers = append(identifiers, name)
			}
		case "keyword_argument":
			// Extract value if it's an identifier: key=SomeClass
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc == nil {
					continue
				}
				if gc.Type() == "identifier" && j > 0 {
					identifiers = append(identifiers, string(content[gc.StartByte():gc.EndByte()]))
				}
			}
		case "list":
			// Recurse into list arguments: [Class1, Class2]
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc == nil {
					continue
				}
				if gc.Type() == "identifier" {
					identifiers = append(identifiers, string(content[gc.StartByte():gc.EndByte()]))
				}
			}
		}
	}
	return identifiers
}

// extractModuleVariables extracts top-level variable assignments.
func (p *PythonParser) extractModuleVariables(root *sitter.Node, content []byte, filePath string, result *ParseResult) {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child.Type() == "expression_statement" {
			if child.ChildCount() > 0 {
				expr := child.Child(0)
				if expr.Type() == "assignment" {
					if variable := p.processModuleVariable(expr, content, filePath); variable != nil {
						result.Symbols = append(result.Symbols, variable)
					}
				}
			}
		}
	}
}

// processModuleVariable extracts a module-level variable.
func (p *PythonParser) processModuleVariable(node *sitter.Node, content []byte, filePath string) *Symbol {
	var name string
	var typeStr string
	var typeRefs []TypeReference // IT-06 Bug 9

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if name == "" {
				name = string(content[child.StartByte():child.EndByte()])
			}
		case "type":
			typeStr = string(content[child.StartByte():child.EndByte()])
			// IT-06 Bug 9: Extract type refs from variable annotation
			typeRefs = extractTypeRefsFromAnnotation(child, content, filePath)
		}
	}

	if name == "" {
		return nil
	}

	// Skip internal variables like __all__, __version__, etc. for symbol extraction
	// but don't skip them entirely - they're useful
	exported := p.isExported(name)
	if !p.parseOptions.IncludePrivate && !exported {
		return nil
	}

	// Determine kind: CONSTANT if all caps, otherwise variable
	kind := SymbolKindVariable
	if isAllCaps(name) {
		kind = SymbolKindConstant
	}

	sym := &Symbol{
		ID:        GenerateID(filePath, int(node.StartPoint().Row+1), name),
		Name:      name,
		Kind:      kind,
		FilePath:  filePath,
		Language:  "python",
		Exported:  exported,
		Signature: typeStr,
		StartLine: int(node.StartPoint().Row + 1),
		EndLine:   int(node.EndPoint().Row + 1),
		StartCol:  int(node.StartPoint().Column),
		EndCol:    int(node.EndPoint().Column),
	}

	// IT-06 Bug 9: Set type annotation references
	if len(typeRefs) > 0 {
		sym.TypeReferences = typeRefs
	}

	return sym
}

// extractDocstring extracts the docstring from a block node.
func (p *PythonParser) extractDocstring(block *sitter.Node, content []byte) string {
	// First statement in block might be docstring
	if block.ChildCount() > 0 {
		first := block.Child(0)
		if first.Type() == "expression_statement" && first.ChildCount() > 0 {
			strNode := first.Child(0)
			if strNode.Type() == "string" {
				return p.extractStringContent(strNode, content)
			}
		}
	}
	return ""
}

// extractStringContent extracts the content from a string node, removing quotes.
func (p *PythonParser) extractStringContent(node *sitter.Node, content []byte) string {
	raw := string(content[node.StartByte():node.EndByte()])

	// Handle triple-quoted strings
	if strings.HasPrefix(raw, `"""`) || strings.HasPrefix(raw, `'''`) {
		return strings.Trim(raw, `"'`)
	}

	// Handle single-quoted strings
	return strings.Trim(raw, `"'`)
}

// isExported determines if a Python name is exported.
//
// Python visibility rules:
//   - Names starting with _ (single underscore) are conventionally private
//   - Names starting with __ but not ending with __ are name-mangled (private)
//   - Dunder names (__init__, __str__, etc.) are special/public
//   - All other names are public
func (p *PythonParser) isExported(name string) bool {
	if name == "" {
		return false
	}

	// Dunder methods (__xxx__) are exported
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return true
	}

	// Name-mangled (__xxx) is not exported
	if strings.HasPrefix(name, "__") {
		return false
	}

	// Single underscore prefix is not exported
	if strings.HasPrefix(name, "_") {
		return false
	}

	return true
}

// isAllCaps returns true if the name is all uppercase (with underscores allowed).
func isAllCaps(name string) bool {
	for _, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return len(name) > 0
}

// === GR-40a: Python Protocol Implementation Detection ===

// extractSubscriptBaseName extracts the base identifier from a subscript node.
//
// Description:
//
//	For tree-sitter subscript nodes like "Protocol[T]" or "Generic[T, U]",
//	extracts the base name ("Protocol", "Generic") by finding the first
//	identifier or attribute child.
//
// Inputs:
//   - node: A tree-sitter subscript node. Must not be nil.
//   - content: Raw source bytes for text extraction.
//
// Outputs:
//   - string: The base name before the bracket, or "" if not found.
//
// Limitations:
//   - Returns the first identifier/attribute child. For complex expressions
//     like module.sub.Protocol[T], returns "module.sub.Protocol" (full attribute text).
//
// Assumptions:
//   - Node is a "subscript" from tree-sitter Python grammar.
//
// IT-03a Phase 15 P-1: Enables detection of Protocol[T] as an interface.
func extractSubscriptBaseName(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			return string(content[child.StartByte():child.EndByte()])
		case "attribute":
			// IT-06b: Strip qualified prefixes (e.g., "typing.Protocol" → "Protocol")
			// for index lookup compatibility, same as base class extraction.
			fullName := string(content[child.StartByte():child.EndByte()])
			if dotIdx := strings.LastIndex(fullName, "."); dotIdx >= 0 {
				return fullName[dotIdx+1:]
			}
			return fullName
		}
	}
	return ""
}

// extractKeywordBaseClass handles keyword arguments in class base lists.
//
// Description:
//
//	Detects metaclass=ABCMeta and metaclass=abc.ABCMeta patterns in class
//	definitions. When found, adds "ABCMeta" or "abc.ABCMeta" to the bases
//	list so that isABCClass can detect it. Ignores non-metaclass keywords
//	(e.g., bar=True, slots=True).
//
// Inputs:
//   - node: A tree-sitter keyword_argument node. Must not be nil.
//   - content: Raw source bytes for text extraction.
//   - bases: Pointer to the base class name slice. Mutated in place.
//
// Outputs:
//   - None. Mutates *bases by appending when metaclass=ABCMeta is found.
//
// Limitations:
//   - Only detects "ABCMeta" and "abc.ABCMeta" as metaclass values.
//     Custom ABCMeta subclasses (e.g., MyMeta) are not detected.
//   - Uses positional identifier matching (first=key, second=value) based
//     on tree-sitter's keyword_argument node structure.
//
// Assumptions:
//   - Node is a "keyword_argument" from tree-sitter Python grammar.
//   - keyword_argument has structure: identifier("metaclass"), "=", identifier/attribute("ABCMeta").
//
// IT-03a Phase 15 P-2: Enables detection of ABCMeta metaclass.
func (p *PythonParser) extractKeywordBaseClass(node *sitter.Node, content []byte, bases *[]string) {
	var key, value string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier":
			if key == "" {
				key = string(content[child.StartByte():child.EndByte()])
			} else {
				value = string(content[child.StartByte():child.EndByte()])
			}
		case "attribute":
			value = string(content[child.StartByte():child.EndByte()])
		}
	}
	if key == "metaclass" && (value == "ABCMeta" || value == "abc.ABCMeta") {
		*bases = append(*bases, value)
	}
}

// isTypingProtocol checks if a class is a typing.Protocol (structural interface).
//
// Description:
//
//	Detects Protocol classes by checking if any base class is "Protocol" or
//	"typing.Protocol". Protocol classes define structural interfaces similar
//	to Go's implicit interface satisfaction.
//
// Inputs:
//   - ctx: Context for tracing. Must not be nil.
//   - bases: List of base class names from the class definition.
//
// Outputs:
//   - bool: true if this is a typing.Protocol class.
//
// Assumptions:
//   - Base class names are extracted from the class definition AST.
//   - "Protocol" and "typing.Protocol" are the standard ways to define Protocols.
//
// Thread Safety:
//
//	This method is safe for concurrent use.
func (p *PythonParser) isTypingProtocol(ctx context.Context, bases []string) bool {
	for _, base := range bases {
		if base == "Protocol" || base == "typing.Protocol" {
			slog.Debug("GR-40a: Protocol class detected",
				slog.String("base", base),
			)
			return true
		}
	}
	return false
}

// isABCClass checks if a class inherits from abc.ABC.
//
// Description:
//
//	Detects ABC (Abstract Base Class) by checking if any base class is "ABC"
//	or "abc.ABC". Note: ABC classes only define interfaces if they have
//	at least one @abstractmethod decorated method.
//
// Inputs:
//   - ctx: Context for tracing. Must not be nil.
//   - bases: List of base class names from the class definition.
//
// Outputs:
//   - bool: true if this class inherits from ABC.
//
// Thread Safety: This method is safe for concurrent use.
func (p *PythonParser) isABCClass(ctx context.Context, bases []string) bool {
	for _, base := range bases {
		if base == "ABC" || base == "abc.ABC" || base == "ABCMeta" || base == "abc.ABCMeta" {
			return true
		}
	}
	return false
}

// isProtocolClass checks if a class is a Protocol or ABC with abstract methods.
//
// Description:
//
//	Legacy wrapper that checks for either typing.Protocol or abc.ABC.
//	For ABC classes, use hasAbstractMethod() to verify they define an interface.
//
// Deprecated: Use isTypingProtocol() and isABCClass() separately for better control.
//
// Thread Safety: This method is safe for concurrent use.
func (p *PythonParser) isProtocolClass(ctx context.Context, bases []string) bool {
	return p.isTypingProtocol(ctx, bases) || p.isABCClass(ctx, bases)
}

// hasAbstractMethod checks if a class has at least one @abstractmethod decorated method.
//
// Description:
//
//	GR-40a M-1 fix: ABC classes only define interfaces if they have abstract methods.
//	This function checks the class's children for any method with @abstractmethod
//	or @abc.abstractmethod decorator.
//
// Inputs:
//   - ctx: Context for tracing. Must not be nil.
//   - classSym: The class symbol with Children already populated.
//
// Outputs:
//   - bool: true if at least one method has @abstractmethod decorator.
//
// Thread Safety: This method is safe for concurrent use (read-only).
func (p *PythonParser) hasAbstractMethod(ctx context.Context, classSym *Symbol) bool {
	if classSym == nil {
		return false
	}

	for _, child := range classSym.Children {
		if child.Kind != SymbolKindMethod {
			continue
		}
		if child.Metadata == nil {
			continue
		}

		for _, dec := range child.Metadata.Decorators {
			if dec == "abstractmethod" || dec == "abc.abstractmethod" {
				slog.Debug("GR-40a: Found @abstractmethod",
					slog.String("class", classSym.Name),
					slog.String("method", child.Name),
				)
				return true
			}
		}
	}
	return false
}

// collectPythonClassMethods populates Metadata.Methods with method signatures.
//
// Description:
//
//	Collects method signatures from a class's children and stores them in
//	Metadata.Methods for use in Protocol implementation detection (GR-40a).
//	This enables the graph builder to create EdgeTypeImplements edges
//	when a class's method set is a superset of a Protocol's method set.
//
// Inputs:
//   - ctx: Context for tracing. Must not be nil.
//   - classSym: The class symbol with Children already populated.
//
// Limitations:
//   - Only checks method names, not parameter/return types (Phase 1).
//   - Dunder methods are filtered except for common Protocol methods.
//
// Assumptions:
//   - classSym.Children are already populated from class body extraction.
//   - Method signatures are in the format "def name(params) -> return".
//
// Thread Safety:
//
//	This method modifies classSym in place. Not safe for concurrent use
//	on the same symbol.
func (p *PythonParser) collectPythonClassMethods(ctx context.Context, classSym *Symbol) {
	if classSym == nil || len(classSym.Children) == 0 {
		return
	}

	methods := make([]MethodSignature, 0)
	skippedDunders := 0

	for _, child := range classSym.Children {
		if child.Kind != SymbolKindMethod {
			continue
		}

		// Skip dunder methods for interface matching (except Protocol-relevant ones)
		// GR-40a M-3: Expanded dunder list to include comparison and hashing protocols
		if strings.HasPrefix(child.Name, "__") && strings.HasSuffix(child.Name, "__") {
			if !isProtocolDunderMethod(child.Name) {
				skippedDunders++
				continue
			}
		}

		sig := p.extractPythonMethodSignature(ctx, child)
		methods = append(methods, sig)
	}

	if len(methods) > 0 {
		if classSym.Metadata == nil {
			classSym.Metadata = &SymbolMetadata{}
		}
		classSym.Metadata.Methods = methods

		// GR-40a M-4: Structured logging for Protocol method collection
		slog.Debug("GR-40a: collected class methods",
			slog.String("class", classSym.Name),
			slog.Int("method_count", len(methods)),
			slog.Int("skipped_dunders", skippedDunders),
			slog.Bool("is_protocol", classSym.Kind == SymbolKindInterface),
		)
	}
}

// extractPythonMethodSignature creates a MethodSignature from a method symbol.
//
// Description:
//
//	Parses a Python method symbol to extract signature information for
//	Protocol implementation detection (GR-40a). Extracts parameter count
//	(excluding self/cls) and return type information.
//
// Inputs:
//   - ctx: Context for tracing. Must not be nil.
//   - method: The method symbol with Signature populated. May be nil.
//
// Outputs:
//   - MethodSignature: Extracted signature info. Returns empty signature for nil input.
//
// Limitations:
//   - Params field is empty (Phase 1 - name-only matching).
//   - Does not validate parameter types, only counts them.
//   - Tuple return types count commas, may be imprecise for nested types.
//
// Assumptions:
//   - Signature format: "def name(params) -> ReturnType" or "async def name(...)".
//   - First parameter is self/cls for instance/class methods.
//
// Thread Safety: This method is safe for concurrent use.
func (p *PythonParser) extractPythonMethodSignature(ctx context.Context, method *Symbol) MethodSignature {
	// Validate input (GR-40a post-implementation review fix H-4)
	if method == nil {
		return MethodSignature{}
	}

	// Parse signature to extract param/return counts
	// Signature format: "def name(self, a, b) -> ReturnType" or "async def name(...)"
	signature := method.Signature

	// Handle empty signature gracefully
	if signature == "" {
		return MethodSignature{Name: method.Name}
	}

	paramCount := 0
	returnCount := 0

	// Find parameter list
	parenStart := strings.Index(signature, "(")
	parenEnd := strings.LastIndex(signature, ")")
	if parenStart != -1 && parenEnd > parenStart {
		params := signature[parenStart+1 : parenEnd]
		if params != "" {
			// Count commas + 1 for parameter count
			// But subtract 1 if 'self' or 'cls' is the first param
			paramCount = strings.Count(params, ",") + 1

			// Check for self/cls as first param
			firstParam := strings.TrimSpace(strings.Split(params, ",")[0])
			if firstParam == "self" || firstParam == "cls" ||
				strings.HasPrefix(firstParam, "self:") || strings.HasPrefix(firstParam, "cls:") {
				paramCount--
			}
		}
	}

	// Check for return type annotation
	arrowIdx := strings.Index(signature, "->")
	returnType := ""
	if arrowIdx != -1 {
		returnType = strings.TrimSpace(signature[arrowIdx+2:])
		if returnType != "" && returnType != "None" {
			returnCount = 1
			// Check for tuple return (multiple values)
			if strings.HasPrefix(returnType, "Tuple[") || strings.HasPrefix(returnType, "tuple[") {
				// Count commas in the tuple
				returnCount = strings.Count(returnType, ",") + 1
			}
		}
	}

	return MethodSignature{
		Name:        method.Name,
		Params:      "", // Python doesn't need normalized params for Phase 1
		Returns:     returnType,
		ParamCount:  paramCount,
		ReturnCount: returnCount,
	}
}

// protocolDunderMethods is the set of dunder methods commonly used in Protocols.
// GR-40a M-3: Expanded list to include comparison, hashing, and numeric protocols.
var protocolDunderMethods = map[string]bool{
	// Object lifecycle
	"__init__": true,
	"__call__": true,
	"__del__":  true,

	// Iterator protocol
	"__iter__": true,
	"__next__": true,

	// Context manager protocol
	"__enter__": true,
	"__exit__":  true,

	// Async context manager protocol
	"__aenter__": true,
	"__aexit__":  true,

	// Async iterator protocol
	"__aiter__": true,
	"__anext__": true,

	// Container protocol
	"__getitem__":  true,
	"__setitem__":  true,
	"__delitem__":  true,
	"__len__":      true,
	"__contains__": true,

	// Comparison protocol (Comparable, Hashable)
	"__eq__":   true,
	"__ne__":   true,
	"__lt__":   true,
	"__le__":   true,
	"__gt__":   true,
	"__ge__":   true,
	"__hash__": true,

	// String representation
	"__str__":  true,
	"__repr__": true,

	// Numeric protocol
	"__add__":      true,
	"__sub__":      true,
	"__mul__":      true,
	"__truediv__":  true,
	"__floordiv__": true,
	"__mod__":      true,
	"__neg__":      true,
	"__pos__":      true,
	"__abs__":      true,

	// Boolean
	"__bool__": true,

	// Attribute access
	"__getattr__":      true,
	"__setattr__":      true,
	"__delattr__":      true,
	"__getattribute__": true,

	// Descriptor protocol
	"__get__":    true,
	"__set__":    true,
	"__delete__": true,

	// Awaitable protocol
	"__await__": true,
}

// isProtocolDunderMethod returns true if the method name is a Protocol-relevant dunder.
//
// Description:
//
//	Checks if a dunder method should be included in Protocol method matching.
//	This includes methods from common Protocols like Iterator, ContextManager,
//	Container, Comparable, Hashable, and numeric protocols.
//
// Inputs:
//   - name: The method name to check. Should be a dunder (starts and ends with __).
//
// Outputs:
//   - bool: True if the method is Protocol-relevant and should be included.
//
// Thread Safety: Safe for concurrent use (reads from immutable map).
func isProtocolDunderMethod(name string) bool {
	return protocolDunderMethods[name]
}

// extractCallSites extracts all function and method calls from a Python function body.
//
// Description:
//
//	Traverses the AST of a Python function or method body to find all call
//	nodes. For each call, it extracts the target name, location, and whether
//	it's a method call (e.g., self.method(), obj.func()). This enables the
//	graph builder to create EdgeTypeCalls edges for find_callers/find_callees.
//
// Inputs:
//   - ctx: Context for cancellation. Checked every 100 nodes.
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
//   - Cannot detect calls through dynamic dispatch or metaprogramming
//   - Limited to MaxCallExpressionDepth (50) nesting depth
//
// Thread Safety: Safe for concurrent use.
func (p *PythonParser) extractCallSites(ctx context.Context, bodyNode *sitter.Node, content []byte, filePath string) []CallSite {
	if bodyNode == nil {
		return nil
	}

	if ctx.Err() != nil {
		return nil
	}

	ctx, span := tracer.Start(ctx, "PythonParser.extractCallSites")
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
			slog.Debug("GR-41: Max call expression depth reached in Python",
				slog.String("file", filePath),
				slog.Int("depth", entry.depth),
			)
			continue
		}

		nodeCount++
		if nodeCount%100 == 0 {
			if ctx.Err() != nil {
				slog.Debug("GR-41: Context cancelled during Python call extraction",
					slog.String("file", filePath),
					slog.Int("calls_found", len(calls)),
				)
				return calls
			}
		}

		if len(calls) >= MaxCallSitesPerSymbol {
			slog.Warn("GR-41: Max call sites per symbol reached in Python",
				slog.String("file", filePath),
				slog.Int("limit", MaxCallSitesPerSymbol),
			)
			return calls
		}

		// Python tree-sitter uses "call" (not "call_expression" like Go)
		if node.Type() == "call" {
			call := p.extractSingleCallSite(node, content, filePath)
			if call != nil && call.Target != "" {
				calls = append(calls, *call)
			}
		}

		// Add children to stack in reverse order for left-to-right processing
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

// extractSingleCallSite extracts call information from a Python call node.
//
// Description:
//
//	Parses a single Python call node to extract the function/method name,
//	location, and receiver information. Handles:
//	  - Simple function calls: func(args)
//	  - Method calls: self.method(args), obj.method(args)
//	  - Chained calls: obj.method1().method2(args)
//
// Inputs:
//   - node: A "call" node from tree-sitter-python. Must not be nil.
//   - content: The source file content bytes.
//   - filePath: Path to the source file for location data.
//
// Outputs:
//   - *CallSite: The extracted call site, or nil if extraction fails.
//
// Thread Safety: Safe for concurrent use.
func (p *PythonParser) extractSingleCallSite(node *sitter.Node, content []byte, filePath string) *CallSite {
	if node == nil || node.Type() != "call" {
		return nil
	}

	// Python call node structure:
	//   call { function: <expr>, arguments: argument_list }
	// The function child can be:
	//   - identifier: simple call like func()
	//   - attribute: method call like self.method() or obj.func()
	//   - call: chained call like func()()
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
		// Simple function call: function_name(args)
		call.Target = string(content[funcNode.StartByte():funcNode.EndByte()])
		call.IsMethod = false

	case "attribute":
		// Method call: obj.method(args) or self.method(args)
		// Python attribute node has: object, attribute
		objectNode := funcNode.ChildByFieldName("object")
		attrNode := funcNode.ChildByFieldName("attribute")

		if attrNode != nil {
			call.Target = string(content[attrNode.StartByte():attrNode.EndByte()])
		}

		if objectNode != nil {
			receiver := string(content[objectNode.StartByte():objectNode.EndByte()])

			// GR-62a P-3: Normalize super() calls. When objectNode is a call to super(),
			// the receiver text is "super()" — normalize to "super" so the builder's
			// resolveSuperCall strategy can match it.
			if objectNode.Type() == "call" && (receiver == "super()" || receiver == "super") {
				receiver = "super"
			}

			call.Receiver = receiver
			call.IsMethod = true
		}

	default:
		// Other cases: subscript calls, call chains, etc.
		// Extract text as target
		text := string(content[funcNode.StartByte():funcNode.EndByte()])
		if len(text) > 100 {
			// Truncate very long expressions
			text = text[:100]
		}
		call.Target = text
	}

	if call.Target == "" {
		return nil
	}

	return call
}

// Compile-time interface compliance check.
var _ Parser = (*PythonParser)(nil)
