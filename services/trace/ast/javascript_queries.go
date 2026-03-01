package ast

// JavaScript Tree-sitter Node Types
//
// This file documents the tree-sitter node types used by JavaScriptParser for symbol extraction.
// The parser uses direct node traversal rather than tree-sitter's query language for
// more precise control over symbol extraction.
//
// Reference: https://github.com/tree-sitter/tree-sitter-javascript

// Node type constants for JavaScript AST traversal.
const (
	// Top-level nodes
	jsNodeProgram = "program"

	// Import-related nodes
	jsNodeImportStatement = "import_statement"
	jsNodeImportClause    = "import_clause"
	jsNodeNamespaceImport = "namespace_import"
	jsNodeNamedImports    = "named_imports"
	jsNodeImportSpecifier = "import_specifier"
	jsNodeString          = "string"
	jsNodeStringFragment  = "string_fragment"

	// Export-related nodes
	jsNodeExportStatement = "export_statement"
	jsNodeExportClause    = "export_clause"
	jsNodeExportSpecifier = "export_specifier"

	// Declaration nodes
	jsNodeFunctionDeclaration   = "function_declaration"
	jsNodeGeneratorFunction     = "generator_function"
	jsNodeGeneratorFunctionDecl = "generator_function_declaration"
	jsNodeClassDeclaration      = "class_declaration"
	jsNodeLexicalDeclaration    = "lexical_declaration"
	jsNodeVariableDeclaration   = "variable_declaration"
	jsNodeVariableDeclarator    = "variable_declarator"

	// Class-related nodes
	jsNodeClassBody            = "class_body"
	jsNodeClassHeritage        = "class_heritage"
	jsNodeMethodDefinition     = "method_definition"
	jsNodeFieldDefinition      = "field_definition"
	jsNodePrivatePropertyIdent = "private_property_identifier"
	jsNodePropertyIdentifier   = "property_identifier"
	jsNodeStaticBlock          = "static_block"

	// Function-related nodes
	jsNodeFormalParameters = "formal_parameters"
	jsNodeArrowFunction    = "arrow_function"
	jsNodeRestPattern      = "rest_pattern"

	// Identifier nodes
	jsNodeIdentifier = "identifier"

	// Expression nodes
	jsNodeCallExpression       = "call_expression"
	jsNodeNewExpression        = "new_expression"
	jsNodeMemberExpression     = "member_expression"
	jsNodeArguments            = "arguments"
	jsNodeAssignmentExpression = "assignment_expression"

	// Statement nodes
	jsNodeStatementBlock      = "statement_block"
	jsNodeReturnStatement     = "return_statement"
	jsNodeExpressionStatement = "expression_statement"

	// Function expression nodes (RHS of assignments like proto.handle = function handle() {})
	jsNodeFunctionExpression = "function_expression"

	// Comment nodes
	jsNodeComment = "comment"

	// Keywords
	jsNodeAsync    = "async"
	jsNodeStatic   = "static"
	jsNodeConst    = "const"
	jsNodeLet      = "let"
	jsNodeVar      = "var"
	jsNodeDefault  = "default"
	jsNodeExtends  = "extends"
	jsNodeFunction = "function"
	jsNodeClass    = "class"
	jsNodeExport   = "export"
	jsNodeImport   = "import"
	jsNodeFrom     = "from"
	jsNodeAs       = "as"
	jsNodeYield    = "yield"
	jsNodeAwait    = "await"
)

// Depth limit constants for JavaScript AST traversal.
const (
	// maxThisAssignmentDepth is the maximum recursion depth for bodyHasThisAssignment.
	maxThisAssignmentDepth = 5
)

// JavaScriptNodeTypes maps symbol kinds to the tree-sitter node types that produce them.
var JavaScriptNodeTypes = map[SymbolKind][]string{
	SymbolKindImport:   {jsNodeImportStatement},
	SymbolKindFunction: {jsNodeFunctionDeclaration, jsNodeArrowFunction, jsNodeGeneratorFunctionDecl},
	SymbolKindClass:    {jsNodeClassDeclaration},
	SymbolKindMethod:   {jsNodeMethodDefinition},
	SymbolKindField:    {jsNodeFieldDefinition},
	SymbolKindVariable: {jsNodeLexicalDeclaration, jsNodeVariableDeclaration},
	SymbolKindConstant: {jsNodeLexicalDeclaration}, // when using const
}

// JavaScript AST Structure Reference
//
// program
// ├── import_statement
// │   ├── import
// │   ├── import_clause
// │   │   ├── identifier                 // default import
// │   │   ├── namespace_import           // * as foo
// │   │   │   ├── *
// │   │   │   ├── as
// │   │   │   └── identifier
// │   │   └── named_imports              // { foo, bar }
// │   │       └── import_specifier+
// │   │           ├── identifier         // imported name
// │   │           └── identifier?        // local alias
// │   ├── from
// │   └── string                         // module path
// │       └── string_fragment
// │
// ├── export_statement
// │   ├── export
// │   ├── default?
// │   └── <declaration>                  // function, class, etc.
// │
// ├── function_declaration
// │   ├── async?
// │   ├── function
// │   ├── *?                             // generator
// │   ├── identifier                     // name
// │   ├── formal_parameters
// │   │   └── identifier | rest_pattern
// │   └── statement_block
// │
// ├── class_declaration
// │   ├── class
// │   ├── identifier                     // name
// │   ├── class_heritage?
// │   │   └── extends
// │   │       └── identifier
// │   └── class_body
// │       ├── method_definition
// │       │   ├── static?
// │       │   ├── async?
// │       │   ├── *?                     // generator
// │       │   ├── property_identifier    // name or 'constructor'
// │       │   ├── formal_parameters
// │       │   └── statement_block
// │       └── field_definition
// │           ├── static?
// │           ├── property_identifier | private_property_identifier
// │           ├── =?
// │           └── <initializer>?
// │
// └── lexical_declaration
//     ├── const | let
//     └── variable_declarator
//         ├── identifier                 // name
//         ├── =?
//         └── <initializer>?
//             └── arrow_function | ...

// JavaScript Import Types
//
// JavaScript/ES6 supports several import forms:
//
// 1. Named imports:
//    import { foo, bar } from 'module'
//    import { foo as f } from 'module'
//
// 2. Default imports:
//    import foo from 'module'
//
// 3. Namespace imports:
//    import * as foo from 'module'
//
// 4. Side-effect imports:
//    import 'module'
//
// 5. CommonJS (Node.js):
//    const foo = require('module')
//    const { a, b } = require('module')
//
// 6. Dynamic imports:
//    const module = await import('module')
//
// The parser captures:
// - IsDefault: true for default imports
// - IsNamespace: true for namespace imports
// - IsCommonJS: true for require() calls
// - IsModule: true for ES module imports

// JavaScript Export Types
//
// JavaScript supports several export forms:
//
// 1. Named exports:
//    export { foo, bar }
//    export { foo as f }
//
// 2. Default exports:
//    export default foo
//    export default class Foo {}
//    export default function foo() {}
//
// 3. Declaration exports:
//    export function foo() {}
//    export class Foo {}
//    export const foo = 1
//    export let bar = 2
//
// 4. Re-exports:
//    export { foo } from 'module'
//    export * from 'module'
//
// The parser marks exported symbols with Exported: true

// JavaScript Class Features
//
// ES2022+ class features supported:
//
// - Public fields: class { field = value }
// - Private fields: class { #field = value }
// - Static fields: class { static field = value }
// - Static private fields: class { static #field = value }
// - Private methods: class { #method() {} }
// - Static blocks: class { static { ... } }
// - Getters/Setters: class { get prop() {}, set prop(v) {} }
//
// The parser captures these in Metadata.AccessModifier and Exported flag.

// JavaScript Async/Generator Functions
//
// Function types supported:
//
// - Regular: function foo() {}
// - Async: async function foo() {}
// - Generator: function* foo() {}
// - Async Generator: async function* foo() {}
// - Arrow: const foo = () => {}
// - Async Arrow: const foo = async () => {}
//
// The parser sets Metadata.IsAsync and Metadata.IsGenerator accordingly.
