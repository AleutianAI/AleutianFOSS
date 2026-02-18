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
	"strings"
	"sync"
	"testing"
	"time"
)

// Test source code samples (embedded, no file I/O).
const (
	testGoEmpty = ``

	testGoPackageOnly = `package example`

	testGoSimple = `package example

import (
	"context"
	"fmt"

	gin "github.com/gin-gonic/gin"
)

// Handler handles HTTP requests.
type Handler struct {
	db Database
}

// Database defines the data access interface.
type Database interface {
	Get(ctx context.Context, id string) (any, error)
}

// HandleGet handles GET requests.
func (h *Handler) HandleGet(ctx *gin.Context) {
	// implementation
}

// NewHandler creates a new Handler instance.
func NewHandler(db Database) *Handler {
	return &Handler{db: db}
}
`

	testGoFunction = `package example

// Add adds two integers.
func Add(a, b int) int {
	return a + b
}
`

	testGoMethod = `package example

type Calculator struct{}

// Add adds two integers.
func (c *Calculator) Add(a, b int) int {
	return a + b
}
`

	testGoInterface = `package example

// Reader defines read operations.
type Reader interface {
	Read(p []byte) (n int, err error)
	Close() error
}
`

	testGoStruct = `package example

// User represents a system user.
type User struct {
	ID        string
	Name      string
	Email     string
	createdAt int64
}
`

	testGoImports = `package example

import (
	"context"
	"fmt"
	"time"

	gin "github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo"
	_ "github.com/lib/pq"
)
`

	testGoSyntaxError = `package example

func Broken( {
	return
}

func Valid() string {
	return "hello"
}
`

	testGoVariablesConstants = `package example

var GlobalVar = "global"
var (
	MultiVar1 = "one"
	MultiVar2 = "two"
)

const MaxSize = 1024
const (
	StatusPending = "pending"
	StatusActive  = "active"
)
`

	testGoUnexported = `package example

type publicType struct{}
type privateType struct{}

func PublicFunc() {}
func privateFunc() {}

var PublicVar = 1
var privateVar = 2
`

	// Invalid UTF-8 bytes
	testInvalidUTF8 = "\xff\xfe"
)

func TestGoParser_Parse_EmptyFile(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoEmpty), "empty.go")

	if err != nil {
		t.Fatalf("expected no error for empty file, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.FilePath != "empty.go" {
		t.Errorf("expected FilePath 'empty.go', got %q", result.FilePath)
	}
	if result.Language != "go" {
		t.Errorf("expected Language 'go', got %q", result.Language)
	}
	if result.Hash == "" {
		t.Error("expected non-empty hash")
	}
}

func TestGoParser_Parse_PackageOnly(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoPackageOnly), "pkg.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have at least the package symbol
	packageSyms := filterByKind(result.Symbols, SymbolKindPackage)
	if len(packageSyms) != 1 {
		t.Errorf("expected 1 package symbol, got %d", len(packageSyms))
	}
	if len(packageSyms) > 0 && packageSyms[0].Name != "example" {
		t.Errorf("expected package name 'example', got %q", packageSyms[0].Name)
	}
}

func TestGoParser_Parse_Function(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoFunction), "func.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	funcs := filterByKind(result.Symbols, SymbolKindFunction)
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(funcs))
	}

	fn := funcs[0]
	if fn.Name != "Add" {
		t.Errorf("expected function name 'Add', got %q", fn.Name)
	}
	if !fn.Exported {
		t.Error("expected function to be exported")
	}
	if fn.StartLine < 1 {
		t.Errorf("expected StartLine >= 1, got %d", fn.StartLine)
	}
	if fn.Signature == "" {
		t.Error("expected non-empty signature")
	}
}

func TestGoParser_Parse_Method(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoMethod), "method.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	methods := filterByKind(result.Symbols, SymbolKindMethod)
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}

	m := methods[0]
	if m.Name != "Add" {
		t.Errorf("expected method name 'Add', got %q", m.Name)
	}
	if !m.Exported {
		t.Error("expected method to be exported")
	}
	// Signature should contain receiver
	if !strings.Contains(m.Signature, "Calculator") {
		t.Errorf("expected signature to contain receiver, got %q", m.Signature)
	}
}

func TestGoParser_Parse_Interface(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoInterface), "interface.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	interfaces := filterByKind(result.Symbols, SymbolKindInterface)
	if len(interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(interfaces))
	}

	iface := interfaces[0]
	if iface.Name != "Reader" {
		t.Errorf("expected interface name 'Reader', got %q", iface.Name)
	}
	if !iface.Exported {
		t.Error("expected interface to be exported")
	}
	if iface.DocComment == "" {
		t.Error("expected non-empty doc comment")
	}

	// Should have method children
	if len(iface.Children) != 2 {
		t.Errorf("expected 2 interface methods, got %d", len(iface.Children))
	}
}

func TestGoParser_Parse_Struct(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoStruct), "struct.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	structs := filterByKind(result.Symbols, SymbolKindStruct)
	if len(structs) != 1 {
		t.Fatalf("expected 1 struct, got %d", len(structs))
	}

	s := structs[0]
	if s.Name != "User" {
		t.Errorf("expected struct name 'User', got %q", s.Name)
	}
	if !s.Exported {
		t.Error("expected struct to be exported")
	}

	// Should have field children (4 total, but createdAt is unexported)
	// With default options (IncludePrivate: true), should have 4
	if len(s.Children) != 4 {
		t.Errorf("expected 4 struct fields, got %d", len(s.Children))
	}

	// Check field names
	fieldNames := make([]string, len(s.Children))
	for i, c := range s.Children {
		fieldNames[i] = c.Name
	}
	expectedFields := []string{"ID", "Name", "Email", "createdAt"}
	for _, expected := range expectedFields {
		found := false
		for _, name := range fieldNames {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected field %q not found in %v", expected, fieldNames)
		}
	}
}

// TestGoParser_Parse_StructEmbedding verifies that the Go parser detects embedded
// (anonymous) fields and sets Metadata.Extends on the parent struct symbol.
// IT-03 H-3: Promoted method resolution depends on this.
func TestGoParser_Parse_StructEmbedding(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	t.Run("single embedded type", func(t *testing.T) {
		content := `package main

type RouterGroup struct {
	basePath string
}

type Engine struct {
	RouterGroup
	pool int
}
`
		result, err := parser.Parse(ctx, []byte(content), "engine.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var engine *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "Engine" && sym.Kind == SymbolKindStruct {
				engine = sym
				break
			}
		}
		if engine == nil {
			t.Fatal("expected to find Engine struct")
		}
		if engine.Metadata == nil {
			t.Fatal("expected Engine to have Metadata")
		}
		if engine.Metadata.Extends != "RouterGroup" {
			t.Errorf("expected Extends='RouterGroup', got %q", engine.Metadata.Extends)
		}
	})

	t.Run("pointer embedded type", func(t *testing.T) {
		content := `package main

type Base struct{}

type Child struct {
	*Base
	Name string
}
`
		result, err := parser.Parse(ctx, []byte(content), "child.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var child *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "Child" && sym.Kind == SymbolKindStruct {
				child = sym
				break
			}
		}
		if child == nil {
			t.Fatal("expected to find Child struct")
		}
		if child.Metadata == nil {
			t.Fatal("expected Child to have Metadata")
		}
		if child.Metadata.Extends != "Base" {
			t.Errorf("expected Extends='Base', got %q", child.Metadata.Extends)
		}
	})

	t.Run("qualified embedded type", func(t *testing.T) {
		content := `package main

import "sync"

type SafeMap struct {
	sync.Mutex
	data map[string]string
}
`
		result, err := parser.Parse(ctx, []byte(content), "safemap.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var safeMap *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "SafeMap" && sym.Kind == SymbolKindStruct {
				safeMap = sym
				break
			}
		}
		if safeMap == nil {
			t.Fatal("expected to find SafeMap struct")
		}
		if safeMap.Metadata == nil {
			t.Fatal("expected SafeMap to have Metadata")
		}
		if safeMap.Metadata.Extends != "Mutex" {
			t.Errorf("expected Extends='Mutex', got %q", safeMap.Metadata.Extends)
		}
	})

	t.Run("no embedded type", func(t *testing.T) {
		content := `package main

type Plain struct {
	Name string
	Age  int
}
`
		result, err := parser.Parse(ctx, []byte(content), "plain.go")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var plain *Symbol
		for _, sym := range result.Symbols {
			if sym.Name == "Plain" && sym.Kind == SymbolKindStruct {
				plain = sym
				break
			}
		}
		if plain == nil {
			t.Fatal("expected to find Plain struct")
		}
		// Plain struct has no embeds — Metadata should be nil or Extends should be empty
		if plain.Metadata != nil && plain.Metadata.Extends != "" {
			t.Errorf("expected no Extends for Plain, got %q", plain.Metadata.Extends)
		}
	})
}

func TestGoParser_Parse_Imports(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoImports), "imports.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 6 imports
	if len(result.Imports) != 6 {
		t.Errorf("expected 6 imports, got %d", len(result.Imports))
	}

	// Check for aliased import
	foundAlias := false
	for _, imp := range result.Imports {
		if imp.Path == "github.com/gin-gonic/gin" && imp.Alias == "gin" {
			foundAlias = true
			break
		}
	}
	if !foundAlias {
		t.Error("expected to find gin alias import")
	}

	// Check for dot import
	foundDot := false
	for _, imp := range result.Imports {
		if imp.Alias == "." {
			foundDot = true
			break
		}
	}
	if !foundDot {
		t.Error("expected to find dot import")
	}

	// Check for blank import
	foundBlank := false
	for _, imp := range result.Imports {
		if imp.Alias == "_" {
			foundBlank = true
			break
		}
	}
	if !foundBlank {
		t.Error("expected to find blank import")
	}
}

func TestGoParser_Parse_SyntaxError(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoSyntaxError), "broken.go")

	// Should NOT return error - should return partial result with errors
	if err != nil {
		t.Fatalf("expected no error for syntax errors (should be in result.Errors), got: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have errors reported
	if len(result.Errors) == 0 {
		t.Error("expected errors to be reported in result.Errors")
	}

	// Should still extract the valid function
	funcs := filterByKind(result.Symbols, SymbolKindFunction)
	validFound := false
	for _, fn := range funcs {
		if fn.Name == "Valid" {
			validFound = true
			break
		}
	}
	if !validFound {
		t.Error("expected to find 'Valid' function despite syntax errors")
	}
}

func TestGoParser_Parse_VariablesConstants(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoVariablesConstants), "vars.go")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	vars := filterByKind(result.Symbols, SymbolKindVariable)
	if len(vars) != 3 {
		t.Errorf("expected 3 variables, got %d", len(vars))
	}

	consts := filterByKind(result.Symbols, SymbolKindConstant)
	if len(consts) != 3 {
		t.Errorf("expected 3 constants, got %d", len(consts))
	}
}

func TestGoParser_Parse_ContextCancellation(t *testing.T) {
	parser := NewGoParser()

	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := parser.Parse(ctx, []byte(testGoSimple), "test.go")

	if err == nil {
		t.Fatal("expected error for canceled context")
	}
	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected 'canceled' in error, got: %v", err)
	}
}

func TestGoParser_Parse_FileTooLarge(t *testing.T) {
	// Create parser with small max size
	parser := NewGoParser(WithMaxFileSize(100))
	ctx := context.Background()

	// testGoSimple is larger than 100 bytes
	_, err := parser.Parse(ctx, []byte(testGoSimple), "large.go")

	if err == nil {
		t.Fatal("expected error for file too large")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected 'exceeds' in error, got: %v", err)
	}
}

func TestGoParser_Parse_InvalidUTF8(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	_, err := parser.Parse(ctx, []byte(testInvalidUTF8), "invalid.go")

	if err == nil {
		t.Fatal("expected error for invalid UTF-8")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("expected 'UTF-8' in error, got: %v", err)
	}
}

func TestGoParser_Parse_HashDeterministic(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	content := []byte(testGoSimple)

	result1, err := parser.Parse(ctx, content, "test.go")
	if err != nil {
		t.Fatalf("first parse failed: %v", err)
	}

	result2, err := parser.Parse(ctx, content, "test.go")
	if err != nil {
		t.Fatalf("second parse failed: %v", err)
	}

	if result1.Hash != result2.Hash {
		t.Errorf("hash not deterministic: %q != %q", result1.Hash, result2.Hash)
	}

	// Hash should be 64 hex characters (SHA256)
	if len(result1.Hash) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(result1.Hash))
	}
}

func TestGoParser_Parse_HashNonEmpty(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoSimple), "test.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if result.Hash == "" {
		t.Error("expected non-empty hash")
	}

	// Verify it's valid hex
	for _, c := range result.Hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hash contains non-hex character: %c", c)
			break
		}
	}
}

func TestGoParser_Parse_ResultValidates(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoSimple), "test.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Result should already be validated, but double-check
	if err := result.Validate(); err != nil {
		t.Errorf("result failed validation: %v", err)
	}

	// All symbols should validate
	for _, sym := range result.Symbols {
		if err := sym.Validate(); err != nil {
			t.Errorf("symbol %q failed validation: %v", sym.Name, err)
		}
	}
}

func TestGoParser_Parse_Concurrent(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	sources := []string{
		testGoFunction,
		testGoMethod,
		testGoStruct,
		testGoInterface,
		testGoImports,
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(sources)*10)

	// Run 10 iterations of each source concurrently
	for i := 0; i < 10; i++ {
		for j, src := range sources {
			wg.Add(1)
			go func(idx int, source string) {
				defer wg.Done()

				result, err := parser.Parse(ctx, []byte(source), "test.go")
				if err != nil {
					errors <- err
					return
				}
				if result == nil {
					errors <- context.DeadlineExceeded // dummy error
					return
				}
			}(j, src)
		}
	}

	wg.Wait()
	close(errors)

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Errorf("concurrent parsing had %d errors: %v", len(errs), errs)
	}
}

func TestGoParser_Parse_FilterUnexported(t *testing.T) {
	// Create parser that excludes unexported symbols
	parser := NewGoParser(WithParseOptions(ParseOptions{
		IncludePrivate: false,
	}))
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoUnexported), "unexported.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Should only have exported symbols
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindPackage || sym.Kind == SymbolKindImport {
			continue // These are always "exported"
		}
		if !sym.Exported {
			t.Errorf("unexported symbol %q should be filtered out", sym.Name)
		}
	}

	// Should have PublicFunc but not privateFunc
	funcs := filterByKind(result.Symbols, SymbolKindFunction)
	for _, fn := range funcs {
		if fn.Name == "privateFunc" {
			t.Error("privateFunc should be filtered out")
		}
	}
}

func TestGoParser_Parse_ComplexFile(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoSimple), "complex.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Verify we extracted expected symbols
	packages := filterByKind(result.Symbols, SymbolKindPackage)
	if len(packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(packages))
	}

	structs := filterByKind(result.Symbols, SymbolKindStruct)
	if len(structs) != 1 {
		t.Errorf("expected 1 struct (Handler), got %d", len(structs))
	}

	interfaces := filterByKind(result.Symbols, SymbolKindInterface)
	if len(interfaces) != 1 {
		t.Errorf("expected 1 interface (Database), got %d", len(interfaces))
	}

	functions := filterByKind(result.Symbols, SymbolKindFunction)
	if len(functions) != 1 {
		t.Errorf("expected 1 function (NewHandler), got %d", len(functions))
	}

	methods := filterByKind(result.Symbols, SymbolKindMethod)
	if len(methods) >= 1 {
		// HandleGet method plus Database.Get interface method
		foundHandleGet := false
		for _, m := range methods {
			if m.Name == "HandleGet" {
				foundHandleGet = true
				break
			}
		}
		if !foundHandleGet {
			t.Error("expected to find HandleGet method")
		}
	}

	// Should have imports
	if len(result.Imports) < 3 {
		t.Errorf("expected at least 3 imports, got %d", len(result.Imports))
	}
}

func TestGoParser_Language(t *testing.T) {
	parser := NewGoParser()

	if parser.Language() != "go" {
		t.Errorf("expected language 'go', got %q", parser.Language())
	}
}

func TestGoParser_Extensions(t *testing.T) {
	parser := NewGoParser()

	exts := parser.Extensions()
	if len(exts) != 1 {
		t.Errorf("expected 1 extension, got %d", len(exts))
	}
	if exts[0] != ".go" {
		t.Errorf("expected extension '.go', got %q", exts[0])
	}
}

func TestGoParser_InterfaceCompliance(t *testing.T) {
	// Compile-time check that GoParser implements Parser
	var _ Parser = (*GoParser)(nil)
}

func TestGoParser_Parse_WithTimeout(t *testing.T) {
	parser := NewGoParser()

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Give time for context to expire
	time.Sleep(1 * time.Millisecond)

	_, err := parser.Parse(ctx, []byte(testGoSimple), "test.go")

	if err == nil {
		t.Fatal("expected error for expired context")
	}
}

// Helper function to filter symbols by kind.
func filterByKind(symbols []*Symbol, kind SymbolKind) []*Symbol {
	result := make([]*Symbol, 0)
	for _, s := range symbols {
		if s.Kind == kind {
			result = append(result, s)
		}
	}
	return result
}

func symbolNames(symbols []*Symbol) []string {
	names := make([]string, len(symbols))
	for i, s := range symbols {
		names[i] = s.Name
	}
	return names
}

// === GR-40: Go Interface Implementation Detection Tests ===

// Test source for interface method collection.
const testGoInterfaceWithMethods = `package example

// Handler defines request handling operations.
type Handler interface {
	Handle(ctx context.Context, req *Request) (*Response, error)
	Close() error
}

// EmptyInterface is an empty interface.
type EmptyInterface interface{}

// Reader defines read operations.
type Reader interface {
	Read(p []byte) (n int, err error)
}
`

// Test source for method-type association.
const testGoTypeWithMethods = `package example

type Handler struct {
	name string
}

func (h *Handler) Handle(ctx context.Context, req *Request) (*Response, error) {
	return nil, nil
}

func (h *Handler) Close() error {
	return nil
}

func (h Handler) String() string {
	return h.name
}

type Reader struct{}

func (r *Reader) Read(p []byte) (n int, err error) {
	return 0, nil
}
`

func TestGoParser_InterfaceMethodCollection(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoInterfaceWithMethods), "interface.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	interfaces := filterByKind(result.Symbols, SymbolKindInterface)
	if len(interfaces) != 3 {
		t.Fatalf("expected 3 interfaces, got %d", len(interfaces))
	}

	t.Run("Handler interface has methods in Metadata", func(t *testing.T) {
		var handler *Symbol
		for _, iface := range interfaces {
			if iface.Name == "Handler" {
				handler = iface
				break
			}
		}
		if handler == nil {
			t.Fatal("Handler interface not found")
		}

		if handler.Metadata == nil {
			t.Fatal("Handler.Metadata is nil")
		}

		if len(handler.Metadata.Methods) != 2 {
			t.Errorf("expected 2 methods in Handler.Metadata.Methods, got %d", len(handler.Metadata.Methods))
		}

		// Verify method names
		methodNames := make(map[string]bool)
		for _, m := range handler.Metadata.Methods {
			methodNames[m.Name] = true
		}
		if !methodNames["Handle"] {
			t.Error("expected Handle method in Handler interface")
		}
		if !methodNames["Close"] {
			t.Error("expected Close method in Handler interface")
		}
	})

	t.Run("Handle method has correct parameter count", func(t *testing.T) {
		var handler *Symbol
		for _, iface := range interfaces {
			if iface.Name == "Handler" {
				handler = iface
				break
			}
		}
		if handler == nil || handler.Metadata == nil {
			t.Skip("Handler not found")
		}

		for _, m := range handler.Metadata.Methods {
			if m.Name == "Handle" {
				if m.ParamCount != 2 {
					t.Errorf("expected Handle to have 2 params, got %d", m.ParamCount)
				}
				if m.ReturnCount != 2 {
					t.Errorf("expected Handle to have 2 returns, got %d", m.ReturnCount)
				}
				return
			}
		}
		t.Error("Handle method not found in Metadata.Methods")
	})

	t.Run("EmptyInterface has no methods", func(t *testing.T) {
		var empty *Symbol
		for _, iface := range interfaces {
			if iface.Name == "EmptyInterface" {
				empty = iface
				break
			}
		}
		if empty == nil {
			t.Fatal("EmptyInterface not found")
		}

		// Empty interfaces should have nil Metadata or empty Methods
		if empty.Metadata != nil && len(empty.Metadata.Methods) > 0 {
			t.Errorf("expected EmptyInterface to have no methods, got %d", len(empty.Metadata.Methods))
		}
	})

	t.Run("Reader interface has Read method", func(t *testing.T) {
		var reader *Symbol
		for _, iface := range interfaces {
			if iface.Name == "Reader" {
				reader = iface
				break
			}
		}
		if reader == nil {
			t.Fatal("Reader interface not found")
		}

		if reader.Metadata == nil || len(reader.Metadata.Methods) != 1 {
			t.Fatalf("expected 1 method in Reader.Metadata.Methods, got %v", reader.Metadata)
		}

		read := reader.Metadata.Methods[0]
		if read.Name != "Read" {
			t.Errorf("expected method name 'Read', got %q", read.Name)
		}
		if read.ParamCount != 1 {
			t.Errorf("expected Read to have 1 param, got %d", read.ParamCount)
		}
		if read.ReturnCount != 2 {
			t.Errorf("expected Read to have 2 returns, got %d", read.ReturnCount)
		}
	})
}

func TestGoParser_MethodTypeAssociation(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoTypeWithMethods), "methods.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	structs := filterByKind(result.Symbols, SymbolKindStruct)
	if len(structs) != 2 {
		t.Fatalf("expected 2 structs, got %d", len(structs))
	}

	t.Run("Handler struct has methods associated", func(t *testing.T) {
		var handler *Symbol
		for _, s := range structs {
			if s.Name == "Handler" {
				handler = s
				break
			}
		}
		if handler == nil {
			t.Fatal("Handler struct not found")
		}

		if handler.Metadata == nil {
			t.Fatal("Handler.Metadata is nil")
		}

		// Handler has 3 methods: Handle, Close, String
		if len(handler.Metadata.Methods) != 3 {
			t.Errorf("expected 3 methods in Handler.Metadata.Methods, got %d", len(handler.Metadata.Methods))
		}

		methodNames := make(map[string]bool)
		for _, m := range handler.Metadata.Methods {
			methodNames[m.Name] = true
		}
		if !methodNames["Handle"] {
			t.Error("expected Handle method associated with Handler")
		}
		if !methodNames["Close"] {
			t.Error("expected Close method associated with Handler")
		}
		if !methodNames["String"] {
			t.Error("expected String method associated with Handler")
		}
	})

	t.Run("Reader struct has Read method associated", func(t *testing.T) {
		var reader *Symbol
		for _, s := range structs {
			if s.Name == "Reader" {
				reader = s
				break
			}
		}
		if reader == nil {
			t.Fatal("Reader struct not found")
		}

		if reader.Metadata == nil {
			t.Fatal("Reader.Metadata is nil")
		}

		if len(reader.Metadata.Methods) != 1 {
			t.Errorf("expected 1 method in Reader.Metadata.Methods, got %d", len(reader.Metadata.Methods))
		}

		if len(reader.Metadata.Methods) > 0 && reader.Metadata.Methods[0].Name != "Read" {
			t.Errorf("expected method name 'Read', got %q", reader.Metadata.Methods[0].Name)
		}
	})
}

func TestGoParser_MethodReceiverField(t *testing.T) {
	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(testGoTypeWithMethods), "methods.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	methods := filterByKind(result.Symbols, SymbolKindMethod)

	t.Run("Method has Receiver field set", func(t *testing.T) {
		var handleMethod *Symbol
		for _, m := range methods {
			if m.Name == "Handle" {
				handleMethod = m
				break
			}
		}
		if handleMethod == nil {
			t.Fatal("Handle method not found")
		}

		if handleMethod.Receiver != "Handler" {
			t.Errorf("expected Receiver 'Handler', got %q", handleMethod.Receiver)
		}
	})

	t.Run("All methods have Receiver field", func(t *testing.T) {
		for _, m := range methods {
			if m.Receiver == "" {
				t.Errorf("method %q has empty Receiver field", m.Name)
			}
		}
	})
}

func TestExtractReceiverTypeName(t *testing.T) {
	tests := []struct {
		name            string
		signature       string
		expectedType    string
		expectedPointer bool
	}{
		{
			name:            "pointer receiver",
			signature:       "func (h *Handler) DoWork(ctx context.Context) error",
			expectedType:    "Handler",
			expectedPointer: true,
		},
		{
			name:            "value receiver",
			signature:       "func (h Handler) String() string",
			expectedType:    "Handler",
			expectedPointer: false,
		},
		{
			name:            "single letter receiver",
			signature:       "func (r *Reader) Read(p []byte) (int, error)",
			expectedType:    "Reader",
			expectedPointer: true,
		},
		{
			name:            "no variable name",
			signature:       "func (*Handler) Handle()",
			expectedType:    "Handler",
			expectedPointer: true,
		},
		{
			name:            "empty signature",
			signature:       "",
			expectedType:    "",
			expectedPointer: false,
		},
		{
			name:            "function not method",
			signature:       "func DoWork(ctx context.Context) error",
			expectedType:    "",
			expectedPointer: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			typeName, isPointer := extractReceiverTypeName(tc.signature)
			if typeName != tc.expectedType {
				t.Errorf("expected type %q, got %q", tc.expectedType, typeName)
			}
			if isPointer != tc.expectedPointer {
				t.Errorf("expected isPointer=%v, got %v", tc.expectedPointer, isPointer)
			}
		})
	}
}

func TestCountParamString(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		expected int
	}{
		{"empty", "", 0},
		{"single param", "ctx context.Context", 1},
		{"two params", "a int, b int", 2},
		{"two params combined type", "a, b int", 2},
		{"three params", "ctx context.Context, req *Request, opts ...Option", 3},
		{"nested func type", "handler func(int, int) error", 1},
		{"nested map type", "m map[string]interface{}", 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := countParamString(tc.params)
			if got != tc.expected {
				t.Errorf("countParamString(%q) = %d, want %d", tc.params, got, tc.expected)
			}
		})
	}
}

func TestCountReturnString(t *testing.T) {
	tests := []struct {
		name     string
		returns  string
		expected int
	}{
		{"empty", "", 0},
		{"single type", "error", 1},
		{"single pointer", "*Handler", 1},
		{"two returns", "(int, error)", 2},
		{"three returns", "(*Response, bool, error)", 3},
		{"named returns", "(n int, err error)", 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := countReturnString(tc.returns)
			if got != tc.expected {
				t.Errorf("countReturnString(%q) = %d, want %d", tc.returns, got, tc.expected)
			}
		})
	}
}

// =============================================================================
// GR-41: Call Extraction Tests
// =============================================================================

// testGoWithCalls is a test fixture for call extraction.
const testGoWithCalls = `package main

import (
	"fmt"
	"config"
)

func Setup() {
	LoadConfig()
	config.Initialize()
	db.Connect()
}

func LoadConfig() {
	fmt.Println("loading")
}

func ProcessData(data []byte) error {
	result := transform(data)
	err := validate(result)
	if err != nil {
		return logError(err)
	}
	return nil
}

type Server struct {
	db Database
}

func (s *Server) Start() {
	s.db.Connect()
	s.listen()
}

func (s *Server) listen() {
	fmt.Println("listening")
}
`

func TestGoParser_ExtractCallSites_BasicFunction(t *testing.T) {
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(testGoWithCalls), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Find the Setup function
	var setupSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Setup" && sym.Kind == SymbolKindFunction {
			setupSym = sym
			break
		}
	}

	if setupSym == nil {
		t.Fatal("Setup function not found")
	}

	if len(setupSym.Calls) == 0 {
		t.Error("Setup should have call sites extracted")
	}

	// Check that we found the expected calls
	callTargets := make(map[string]bool)
	for _, call := range setupSym.Calls {
		callTargets[call.Target] = true
	}

	expectedCalls := []string{"LoadConfig", "Initialize", "Connect"}
	for _, expected := range expectedCalls {
		if !callTargets[expected] {
			t.Errorf("Expected call to %s not found in Setup", expected)
		}
	}
}

func TestGoParser_ExtractCallSites_MethodCall(t *testing.T) {
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(testGoWithCalls), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Find the Start method
	var startSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Start" && sym.Kind == SymbolKindMethod {
			startSym = sym
			break
		}
	}

	if startSym == nil {
		t.Fatal("Start method not found")
	}

	if len(startSym.Calls) == 0 {
		t.Error("Start method should have call sites extracted")
	}

	// Check that we found method calls with receivers
	hasMethodCall := false
	for _, call := range startSym.Calls {
		if call.IsMethod && call.Receiver != "" {
			hasMethodCall = true
			break
		}
	}

	if !hasMethodCall {
		t.Error("Start method should have method calls with receivers")
	}
}

func TestGoParser_ExtractCallSites_NestedCalls(t *testing.T) {
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(testGoWithCalls), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Find the ProcessData function
	var processDataSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "ProcessData" && sym.Kind == SymbolKindFunction {
			processDataSym = sym
			break
		}
	}

	if processDataSym == nil {
		t.Fatal("ProcessData function not found")
	}

	// Should have calls to transform, validate, and logError
	if len(processDataSym.Calls) < 3 {
		t.Errorf("ProcessData should have at least 3 calls, got %d", len(processDataSym.Calls))
	}

	callTargets := make(map[string]bool)
	for _, call := range processDataSym.Calls {
		callTargets[call.Target] = true
	}

	expectedCalls := []string{"transform", "validate", "logError"}
	for _, expected := range expectedCalls {
		if !callTargets[expected] {
			t.Errorf("Expected call to %s not found in ProcessData", expected)
		}
	}
}

func TestGoParser_ExtractCallSites_ContextCancellation(t *testing.T) {
	parser := NewGoParser()

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should still parse but may have fewer/no calls extracted
	_, err := parser.Parse(ctx, []byte(testGoWithCalls), "test.go")
	if err == nil {
		// If it didn't error, that's ok - context cancellation is checked periodically
		return
	}

	// Error should be context-related
	if !strings.Contains(err.Error(), "cancel") {
		t.Errorf("Expected context cancellation error, got: %v", err)
	}
}

func TestGoParser_ExtractCallSites_EmptyFunction(t *testing.T) {
	code := `package main

func Empty() {
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(code), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var emptySym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Empty" && sym.Kind == SymbolKindFunction {
			emptySym = sym
			break
		}
	}

	if emptySym == nil {
		t.Fatal("Empty function not found")
	}

	if len(emptySym.Calls) != 0 {
		t.Errorf("Empty function should have no calls, got %d", len(emptySym.Calls))
	}
}

func TestGoParser_ExtractCallSites_CallLocation(t *testing.T) {
	code := `package main

func Caller() {
	Target()
}
`
	parser := NewGoParser()
	result, err := parser.Parse(context.Background(), []byte(code), "test.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	var callerSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Caller" && sym.Kind == SymbolKindFunction {
			callerSym = sym
			break
		}
	}

	if callerSym == nil {
		t.Fatal("Caller function not found")
	}

	if len(callerSym.Calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(callerSym.Calls))
	}

	call := callerSym.Calls[0]
	if call.Target != "Target" {
		t.Errorf("Expected call to Target, got %s", call.Target)
	}

	// Verify location is set
	if call.Location.StartLine <= 0 {
		t.Error("Call location StartLine should be positive")
	}
	if call.Location.FilePath != "test.go" {
		t.Errorf("Call location FilePath should be test.go, got %s", call.Location.FilePath)
	}
}

func TestCallSite_Validate(t *testing.T) {
	tests := []struct {
		name    string
		call    CallSite
		wantErr bool
	}{
		{
			name: "valid call",
			call: CallSite{
				Target:   "DoWork",
				Location: Location{FilePath: "test.go", StartLine: 10},
			},
			wantErr: false,
		},
		{
			name: "empty target",
			call: CallSite{
				Target:   "",
				Location: Location{FilePath: "test.go", StartLine: 10},
			},
			wantErr: true,
		},
		{
			name: "zero line",
			call: CallSite{
				Target:   "DoWork",
				Location: Location{FilePath: "test.go", StartLine: 0},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// IT-03a Phase 16 G-1: Multiple struct embed tests

func TestGoParser_MultipleStructEmbeds(t *testing.T) {
	parser := NewGoParser()

	src := []byte(`package main

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Writer interface {
	Write(p []byte) (n int, err error)
}

type Closer interface {
	Close() error
}

type ReadWriteCloser struct {
	Reader
	Writer
	Closer
}
`)

	result, err := parser.Parse(context.Background(), src, "multi_embed.go")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var rwc *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "ReadWriteCloser" {
			rwc = sym
			break
		}
	}
	if rwc == nil {
		t.Fatal("expected symbol 'ReadWriteCloser'")
	}
	if rwc.Kind != SymbolKindStruct {
		t.Errorf("expected kind Struct, got %v", rwc.Kind)
	}
	if rwc.Metadata == nil {
		t.Fatal("expected ReadWriteCloser to have metadata")
	}

	// First embed in Extends
	if rwc.Metadata.Extends != "Reader" {
		t.Errorf("expected Extends='Reader', got %q", rwc.Metadata.Extends)
	}

	// Additional embeds in Implements
	if len(rwc.Metadata.Implements) != 2 {
		t.Fatalf("expected 2 additional embeds in Implements, got %d: %v", len(rwc.Metadata.Implements), rwc.Metadata.Implements)
	}
	implSet := make(map[string]bool)
	for _, impl := range rwc.Metadata.Implements {
		implSet[impl] = true
	}
	if !implSet["Writer"] {
		t.Error("expected 'Writer' in additional embeds")
	}
	if !implSet["Closer"] {
		t.Error("expected 'Closer' in additional embeds")
	}
}

// Phase 18: Test that the parser extracts embedded interface names into Metadata.Extends/Implements.
func TestGoParser_InterfaceEmbeddedInterfaces(t *testing.T) {
	src := `package example

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Writer interface {
	Write(p []byte) (n int, err error)
}

type ReadWriter interface {
	Reader
	Writer
}

type ReadCloser interface {
	Reader
	Close() error
}
`

	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(src), "iface_embed.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	interfaces := filterByKind(result.Symbols, SymbolKindInterface)
	if len(interfaces) != 4 {
		t.Fatalf("expected 4 interfaces, got %d", len(interfaces))
	}

	findIface := func(name string) *Symbol {
		for _, sym := range interfaces {
			if sym.Name == name {
				return sym
			}
		}
		t.Fatalf("interface %q not found", name)
		return nil
	}

	t.Run("Reader has methods but no embeds", func(t *testing.T) {
		reader := findIface("Reader")
		if reader.Metadata == nil {
			t.Fatal("Reader.Metadata is nil")
		}
		if len(reader.Metadata.Methods) != 1 || reader.Metadata.Methods[0].Name != "Read" {
			t.Errorf("expected [{Read}], got %v", reader.Metadata.Methods)
		}
		if reader.Metadata.Extends != "" {
			t.Errorf("expected Extends='', got %q", reader.Metadata.Extends)
		}
	})

	t.Run("Writer has methods but no embeds", func(t *testing.T) {
		writer := findIface("Writer")
		if writer.Metadata == nil {
			t.Fatal("Writer.Metadata is nil")
		}
		if len(writer.Metadata.Methods) != 1 || writer.Metadata.Methods[0].Name != "Write" {
			t.Errorf("expected [{Write}], got %v", writer.Metadata.Methods)
		}
		if writer.Metadata.Extends != "" {
			t.Errorf("expected Extends='', got %q", writer.Metadata.Extends)
		}
	})

	t.Run("ReadWriter has no direct methods but embeds Reader and Writer", func(t *testing.T) {
		rw := findIface("ReadWriter")
		if rw.Metadata == nil {
			t.Fatal("ReadWriter.Metadata is nil")
		}
		if len(rw.Metadata.Methods) != 0 {
			t.Errorf("expected no direct methods, got %v", rw.Metadata.Methods)
		}
		if rw.Metadata.Extends != "Reader" {
			t.Errorf("expected Extends='Reader', got %q", rw.Metadata.Extends)
		}
		if len(rw.Metadata.Implements) != 1 || rw.Metadata.Implements[0] != "Writer" {
			t.Errorf("expected Implements=['Writer'], got %v", rw.Metadata.Implements)
		}
	})

	t.Run("ReadCloser has Close method and embeds Reader", func(t *testing.T) {
		rc := findIface("ReadCloser")
		if rc.Metadata == nil {
			t.Fatal("ReadCloser.Metadata is nil")
		}
		if len(rc.Metadata.Methods) != 1 || rc.Metadata.Methods[0].Name != "Close" {
			t.Errorf("expected [{Close}], got %v", rc.Metadata.Methods)
		}
		if rc.Metadata.Extends != "Reader" {
			t.Errorf("expected Extends='Reader', got %q", rc.Metadata.Extends)
		}
	})
}

// Phase 18: Test that cross-package embedded interfaces are extracted correctly.
func TestGoParser_InterfaceEmbeddedQualifiedType(t *testing.T) {
	src := `package example

import "fmt"

type Stringer interface {
	fmt.Stringer
	MyMethod() string
}
`

	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(src), "qualified_embed.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	interfaces := filterByKind(result.Symbols, SymbolKindInterface)
	if len(interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(interfaces))
	}

	stringer := interfaces[0]
	if stringer.Name != "Stringer" {
		t.Fatalf("expected 'Stringer', got %q", stringer.Name)
	}
	if stringer.Metadata == nil {
		t.Fatal("Stringer.Metadata is nil")
	}

	// Should have MyMethod as a direct method
	if len(stringer.Metadata.Methods) != 1 || stringer.Metadata.Methods[0].Name != "MyMethod" {
		t.Errorf("expected [{MyMethod}], got %v", stringer.Metadata.Methods)
	}

	// Should have "Stringer" (from fmt.Stringer qualified_type) as an embed
	if stringer.Metadata.Extends != "Stringer" {
		t.Errorf("expected Extends='Stringer' (from fmt.Stringer), got %q", stringer.Metadata.Extends)
	}
}

// Phase 20-C: Test that type aliases preserve the declared name, not the aliased type.
// Bug: `type nopPage int` was producing a symbol named "int" instead of "nopPage"
// because the second type_identifier child overwrote the first.
func TestGoParser_TypeAlias(t *testing.T) {
	src := `package example

type MyInt int

type MyString string

type Duration int64
`

	parser := NewGoParser()
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(src), "type_alias.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	types := filterByKind(result.Symbols, SymbolKindType)
	if len(types) != 3 {
		t.Fatalf("expected 3 type symbols, got %d: %v", len(types), symbolNames(types))
	}

	expected := map[string]bool{"MyInt": false, "MyString": false, "Duration": false}
	for _, sym := range types {
		if _, ok := expected[sym.Name]; !ok {
			t.Errorf("unexpected type symbol %q (should not be 'int', 'string', or 'int64')", sym.Name)
		} else {
			expected[sym.Name] = true
		}
		if sym.Kind != SymbolKindType {
			t.Errorf("expected SymbolKindType for %q, got %v", sym.Name, sym.Kind)
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing expected type symbol %q", name)
		}
	}
}

// Phase 20-C: Test that methods on type aliases are correctly associated.
func TestGoParser_TypeAliasWithMethods(t *testing.T) {
	src := `package example

type nopPage int

func (p nopPage) Title() string {
	return ""
}

func (p nopPage) IsNode() {}
`

	parser := NewGoParser(WithParseOptions(ParseOptions{IncludePrivate: true}))
	ctx := context.Background()

	result, err := parser.Parse(ctx, []byte(src), "nop_page.go")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Verify the type symbol has the correct name
	types := filterByKind(result.Symbols, SymbolKindType)
	if len(types) != 1 {
		t.Fatalf("expected 1 type symbol, got %d: %v", len(types), symbolNames(types))
	}
	if types[0].Name != "nopPage" {
		t.Errorf("expected type name 'nopPage', got %q", types[0].Name)
	}

	// Verify methods are extracted (Go parser uses SymbolKindMethod for methods)
	methods := filterByKind(result.Symbols, SymbolKindMethod)
	if len(methods) != 2 {
		t.Fatalf("expected 2 method symbols, got %d: %v", len(methods), symbolNames(methods))
	}

	methodNames := make(map[string]bool)
	for _, m := range methods {
		methodNames[m.Name] = true
		if m.Receiver != "nopPage" {
			t.Errorf("method %q should have receiver 'nopPage', got %q",
				m.Name, m.Receiver)
		}
	}
	if !methodNames["Title"] {
		t.Error("missing method 'Title'")
	}
	if !methodNames["IsNode"] {
		t.Error("missing method 'IsNode'")
	}
}
