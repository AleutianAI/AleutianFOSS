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

// Test data: comprehensive TypeScript example from ticket
const typescriptTestSource = `/**
 * User management module.
 * @module UserModule
 */

import { Injectable } from '@angular/core';
import type { Observable } from 'rxjs';
import * as utils from './utils';
import defaultExport from './defaults';

export interface User {
    readonly id: number;
    name: string;
    email?: string;
}

export type UserID = number | string;

type InternalType = { secret: boolean };

@Injectable()
export class UserService {
    private readonly cache: Map<string, User> = new Map();
    public activeCount = 0;

    async getUser<T extends User>(id: string): Promise<T | null> {
        return null;
    }

    protected updateCache(user: User): void {
        this.cache.set(user.id.toString(), user);
    }

    private internalMethod(): void {}
}

export abstract class BaseEntity {
    abstract getId(): string;
}

export enum UserRole {
    Admin = 'admin',
    User = 'user',
    Guest = 'guest'
}

export const DEFAULT_USER: User = { id: 0, name: 'Guest' };

const internalHelper = () => {};

export default UserService;
`

func TestTypeScriptParser_Parse_EmptyFile(t *testing.T) {
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(""), "empty.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Language != "typescript" {
		t.Errorf("expected language 'typescript', got %q", result.Language)
	}

	if result.FilePath != "empty.ts" {
		t.Errorf("expected file path 'empty.ts', got %q", result.FilePath)
	}
}

func TestTypeScriptParser_Parse_Function(t *testing.T) {
	source := `export function greet(name: string): string {
    return "Hello, " + name;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "greet" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'greet'")
	}

	if !fn.Exported {
		t.Error("expected function to be exported")
	}

	if fn.StartLine != 1 {
		t.Errorf("expected start line 1, got %d", fn.StartLine)
	}

	if !strings.Contains(fn.Signature, "greet") {
		t.Errorf("expected signature to contain 'greet', got %q", fn.Signature)
	}
}

func TestTypeScriptParser_Parse_ArrowFunction(t *testing.T) {
	source := `const add = (a: number, b: number): number => a + b;
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "add" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected arrow function 'add'")
	}

	// Arrow functions assigned to const are treated as functions
	if fn.Kind != SymbolKindFunction {
		t.Errorf("expected kind Function, got %s", fn.Kind)
	}
}

func TestTypeScriptParser_Parse_AsyncFunction(t *testing.T) {
	source := `export async function fetchData(url: string): Promise<string> {
    return "";
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "fetchData" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected async function 'fetchData'")
	}

	if fn.Metadata == nil || !fn.Metadata.IsAsync {
		t.Error("expected function to be marked as async")
	}
}

func TestTypeScriptParser_Parse_Class(t *testing.T) {
	source := `export class MyClass {
    private name: string;

    constructor(name: string) {
        this.name = name;
    }

    getName(): string {
        return this.name;
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "MyClass" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'MyClass'")
	}

	if !class.Exported {
		t.Error("expected class to be exported")
	}

	// Check for field
	var nameField *Symbol
	for _, child := range class.Children {
		if child.Name == "name" && child.Kind == SymbolKindField {
			nameField = child
			break
		}
	}

	if nameField == nil {
		t.Error("expected field 'name' in class")
	}

	// Check for method
	var getNameMethod *Symbol
	for _, child := range class.Children {
		if child.Name == "getName" && child.Kind == SymbolKindMethod {
			getNameMethod = child
			break
		}
	}

	if getNameMethod == nil {
		t.Error("expected method 'getName' in class")
	}
}

func TestTypeScriptParser_Parse_Interface(t *testing.T) {
	source := `export interface User {
    readonly id: number;
    name: string;
    email?: string;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "User" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'User'")
	}

	if !iface.Exported {
		t.Error("expected interface to be exported")
	}

	// Check for properties
	if len(iface.Children) < 3 {
		t.Errorf("expected at least 3 properties, got %d", len(iface.Children))
	}

	propNames := make(map[string]bool)
	for _, child := range iface.Children {
		propNames[child.Name] = true
	}

	for _, name := range []string{"id", "name", "email"} {
		if !propNames[name] {
			t.Errorf("expected property %q in interface", name)
		}
	}
}

func TestTypeScriptParser_Parse_TypeAlias(t *testing.T) {
	source := `export type UserID = number | string;
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var typeAlias *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindType && sym.Name == "UserID" {
			typeAlias = sym
			break
		}
	}

	if typeAlias == nil {
		t.Fatal("expected type alias 'UserID'")
	}

	if !typeAlias.Exported {
		t.Error("expected type alias to be exported")
	}
}

func TestTypeScriptParser_Parse_Enum(t *testing.T) {
	source := `export enum UserRole {
    Admin = 'admin',
    User = 'user',
    Guest = 'guest'
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var enum *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindEnum && sym.Name == "UserRole" {
			enum = sym
			break
		}
	}

	if enum == nil {
		t.Fatal("expected enum 'UserRole'")
	}

	if !enum.Exported {
		t.Error("expected enum to be exported")
	}

	// Check for members
	if len(enum.Children) != 3 {
		t.Errorf("expected 3 enum members, got %d", len(enum.Children))
	}

	memberNames := make(map[string]bool)
	for _, child := range enum.Children {
		memberNames[child.Name] = true
		if child.Kind != SymbolKindEnumMember {
			t.Errorf("expected enum member kind, got %s", child.Kind)
		}
	}

	for _, name := range []string{"Admin", "User", "Guest"} {
		if !memberNames[name] {
			t.Errorf("expected enum member %q", name)
		}
	}
}

func TestTypeScriptParser_Parse_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	parser := NewTypeScriptParser()
	_, err := parser.Parse(ctx, []byte("const x = 1;"), "test.ts")

	if err == nil {
		t.Error("expected error from canceled context")
	}

	if !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected canceled error, got: %v", err)
	}
}

func TestTypeScriptParser_Parse_NamedImport(t *testing.T) {
	source := `import { Injectable, Component } from '@angular/core';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if imp.Path != "@angular/core" {
		t.Errorf("expected import path '@angular/core', got %q", imp.Path)
	}

	if len(imp.Names) != 2 {
		t.Errorf("expected 2 names, got %d", len(imp.Names))
	}
}

func TestTypeScriptParser_Parse_DefaultImport(t *testing.T) {
	source := `import React from 'react';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsDefault {
		t.Error("expected default import")
	}

	if imp.Alias != "React" {
		t.Errorf("expected alias 'React', got %q", imp.Alias)
	}
}

func TestTypeScriptParser_Parse_NamespaceImport(t *testing.T) {
	source := `import * as utils from './utils';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsNamespace {
		t.Error("expected namespace import")
	}

	if imp.Alias != "utils" {
		t.Errorf("expected alias 'utils', got %q", imp.Alias)
	}
}

func TestTypeScriptParser_Parse_TypeOnlyImport(t *testing.T) {
	source := `import type { Observable } from 'rxjs';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(result.Imports))
	}

	imp := result.Imports[0]
	if !imp.IsTypeOnly {
		t.Error("expected type-only import")
	}
}

func TestTypeScriptParser_Parse_CommonJSRequire(t *testing.T) {
	source := `const legacy = require('./legacy');
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find CommonJS import
	var cjsImport *Import
	for i := range result.Imports {
		if result.Imports[i].IsCommonJS {
			cjsImport = &result.Imports[i]
			break
		}
	}

	if cjsImport == nil {
		t.Fatal("expected CommonJS import")
	}

	if cjsImport.Path != "./legacy" {
		t.Errorf("expected path './legacy', got %q", cjsImport.Path)
	}

	if cjsImport.Alias != "legacy" {
		t.Errorf("expected alias 'legacy', got %q", cjsImport.Alias)
	}
}

func TestTypeScriptParser_Parse_ExportedFunction(t *testing.T) {
	source := `export function foo(): void {}
function bar(): void {}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foo, bar *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction {
			switch sym.Name {
			case "foo":
				foo = sym
			case "bar":
				bar = sym
			}
		}
	}

	if foo == nil || bar == nil {
		t.Fatal("expected both functions")
	}

	if !foo.Exported {
		t.Error("expected foo to be exported")
	}

	if bar.Exported {
		t.Error("expected bar to NOT be exported")
	}
}

func TestTypeScriptParser_Parse_Generics(t *testing.T) {
	source := `export function identity<T>(value: T): T {
    return value;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "identity" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'identity'")
	}

	if fn.Metadata == nil || len(fn.Metadata.TypeParameters) == 0 {
		t.Fatal("expected type parameters in metadata")
	}

	if !strings.Contains(fn.Metadata.TypeParameters[0], "T") {
		t.Errorf("expected type parameter T, got %v", fn.Metadata.TypeParameters)
	}
}

func TestTypeScriptParser_Parse_Decorators(t *testing.T) {
	source := `@Injectable()
@Component({})
export class MyService {
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "MyService" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'MyService'")
	}

	if class.Metadata == nil || len(class.Metadata.Decorators) < 2 {
		t.Fatalf("expected 2 decorators, got %v", class.Metadata)
	}

	decorators := class.Metadata.Decorators
	hasInjectable := false
	hasComponent := false
	for _, d := range decorators {
		if d == "Injectable" {
			hasInjectable = true
		}
		if d == "Component" {
			hasComponent = true
		}
	}

	if !hasInjectable || !hasComponent {
		t.Errorf("expected Injectable and Component decorators, got %v", decorators)
	}
}

func TestTypeScriptParser_Parse_AccessModifiers(t *testing.T) {
	source := `export class MyClass {
    public publicField: string;
    private privateField: string;
    protected protectedField: string;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	modifiers := make(map[string]string)
	for _, child := range class.Children {
		if child.Metadata != nil {
			modifiers[child.Name] = child.Metadata.AccessModifier
		}
	}

	if modifiers["publicField"] != "public" {
		t.Errorf("expected public modifier for publicField, got %q", modifiers["publicField"])
	}

	if modifiers["privateField"] != "private" {
		t.Errorf("expected private modifier for privateField, got %q", modifiers["privateField"])
	}

	if modifiers["protectedField"] != "protected" {
		t.Errorf("expected protected modifier for protectedField, got %q", modifiers["protectedField"])
	}
}

func TestTypeScriptParser_Parse_AbstractClass(t *testing.T) {
	source := `export abstract class BaseEntity {
    abstract getId(): string;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "BaseEntity" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'BaseEntity'")
	}

	if class.Metadata == nil || !class.Metadata.IsAbstract {
		t.Error("expected class to be abstract")
	}
}

func TestTypeScriptParser_Parse_JSDoc(t *testing.T) {
	source := `/**
 * Fetch a user by ID.
 * @param id - The user ID
 * @returns Promise resolving to User
 */
export function getUser(id: string): Promise<User> {
    return Promise.resolve(null);
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "getUser" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'getUser'")
	}

	if fn.DocComment == "" {
		t.Error("expected JSDoc comment")
	}

	if !strings.Contains(fn.DocComment, "Fetch a user") {
		t.Errorf("expected JSDoc to contain 'Fetch a user', got %q", fn.DocComment)
	}

	if !strings.Contains(fn.DocComment, "@param") {
		t.Error("expected JSDoc to contain @param tag")
	}
}

func TestTypeScriptParser_Parse_FileTooLarge(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptMaxFileSize(100))

	largeContent := make([]byte, 200)
	for i := range largeContent {
		largeContent[i] = 'x'
	}

	_, err := parser.Parse(context.Background(), largeContent, "large.ts")

	if err == nil {
		t.Error("expected error for file too large")
	}

	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size exceeded error, got: %v", err)
	}
}

func TestTypeScriptParser_Parse_InvalidUTF8(t *testing.T) {
	parser := NewTypeScriptParser()

	invalidContent := []byte{0xff, 0xfe}

	_, err := parser.Parse(context.Background(), invalidContent, "invalid.ts")

	if err == nil {
		t.Error("expected error for invalid UTF-8")
	}

	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("expected UTF-8 error, got: %v", err)
	}
}

func TestTypeScriptParser_Parse_Validation(t *testing.T) {
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(typescriptTestSource), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := result.Validate(); err != nil {
		t.Errorf("validation failed: %v", err)
	}

	for _, sym := range result.Symbols {
		if err := sym.Validate(); err != nil {
			t.Errorf("symbol %s validation failed: %v", sym.Name, err)
		}
	}
}

func TestTypeScriptParser_Parse_Hash(t *testing.T) {
	parser := NewTypeScriptParser()
	content := []byte("const x = 1;")

	result1, err := parser.Parse(context.Background(), content, "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result2, err := parser.Parse(context.Background(), content, "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result1.Hash == "" {
		t.Error("expected non-empty hash")
	}

	if result1.Hash != result2.Hash {
		t.Error("expected deterministic hash for same content")
	}

	result3, err := parser.Parse(context.Background(), []byte("const y = 2;"), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result1.Hash == result3.Hash {
		t.Error("expected different hash for different content")
	}
}

func TestTypeScriptParser_Parse_SyntaxError(t *testing.T) {
	source := `export function broken( {
    // Missing closing paren
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("expected partial result, got error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if !result.HasErrors() {
		t.Error("expected errors for syntax error")
	}
}

func TestTypeScriptParser_Parse_TSX(t *testing.T) {
	source := `import React from 'react';

export const MyComponent: React.FC = () => {
    return <div>Hello</div>;
};
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "component.tsx")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Should have React import
	if len(result.Imports) == 0 {
		t.Error("expected imports")
	}

	// Should have MyComponent
	var component *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "MyComponent" {
			component = sym
			break
		}
	}

	if component == nil {
		t.Error("expected MyComponent")
	}
}

func TestTypeScriptParser_Parse_Concurrent(t *testing.T) {
	parser := NewTypeScriptParser()
	sources := []string{
		`export function func1(): void {}`,
		`export class Class1 {}`,
		`export interface IFace1 {}`,
		`export type Type1 = string;`,
		`export enum Enum1 { A, B }`,
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(sources)*10)

	for i := 0; i < 10; i++ {
		for j, src := range sources {
			wg.Add(1)
			go func(idx int, source string) {
				defer wg.Done()
				_, err := parser.Parse(context.Background(), []byte(source), "test.ts")
				if err != nil {
					errors <- err
				}
			}(j, src)
		}
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent parse error: %v", err)
	}
}

func TestTypeScriptParser_Language(t *testing.T) {
	parser := NewTypeScriptParser()
	if parser.Language() != "typescript" {
		t.Errorf("expected language 'typescript', got %q", parser.Language())
	}
}

func TestTypeScriptParser_Extensions(t *testing.T) {
	parser := NewTypeScriptParser()
	extensions := parser.Extensions()

	expectedExts := map[string]bool{".ts": true, ".tsx": true, ".mts": true, ".cts": true}
	for _, ext := range extensions {
		if !expectedExts[ext] {
			t.Errorf("unexpected extension: %q", ext)
		}
		delete(expectedExts, ext)
	}

	if len(expectedExts) > 0 {
		t.Errorf("missing extensions: %v", expectedExts)
	}
}

func TestTypeScriptParser_Parse_ComprehensiveExample(t *testing.T) {
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(typescriptTestSource), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify imports
	if len(result.Imports) == 0 {
		t.Error("expected imports to be extracted")
	}

	// Find specific imports
	var angularImport, rxjsImport, utilsImport, defaultsImport *Import
	for i := range result.Imports {
		imp := &result.Imports[i]
		switch {
		case imp.Path == "@angular/core":
			angularImport = imp
		case imp.Path == "rxjs":
			rxjsImport = imp
		case imp.Path == "./utils":
			utilsImport = imp
		case imp.Path == "./defaults":
			defaultsImport = imp
		}
	}

	if angularImport == nil {
		t.Error("expected @angular/core import")
	}

	if rxjsImport == nil {
		t.Error("expected rxjs import")
	} else if !rxjsImport.IsTypeOnly {
		t.Error("expected rxjs import to be type-only")
	}

	if utilsImport == nil {
		t.Error("expected ./utils import")
	} else if !utilsImport.IsNamespace {
		t.Error("expected ./utils import to be namespace")
	}

	if defaultsImport == nil {
		t.Error("expected ./defaults import")
	} else if !defaultsImport.IsDefault {
		t.Error("expected ./defaults import to be default")
	}

	// Find User interface
	var userInterface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "User" {
			userInterface = sym
			break
		}
	}

	if userInterface == nil {
		t.Fatal("expected User interface")
	}

	if !userInterface.Exported {
		t.Error("expected User interface to be exported")
	}

	// Find UserService class
	var userService *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "UserService" {
			userService = sym
			break
		}
	}

	if userService == nil {
		t.Fatal("expected UserService class")
	}

	if userService.Metadata == nil || len(userService.Metadata.Decorators) == 0 {
		t.Error("expected UserService to have @Injectable decorator")
	}

	// Check UserService methods
	methodNames := make(map[string]bool)
	for _, child := range userService.Children {
		methodNames[child.Name] = true
	}

	expectedMethods := []string{"cache", "activeCount", "getUser", "updateCache", "internalMethod"}
	for _, name := range expectedMethods {
		if !methodNames[name] {
			t.Errorf("expected member %s in UserService", name)
		}
	}

	// Find UserRole enum
	var userRole *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindEnum && sym.Name == "UserRole" {
			userRole = sym
			break
		}
	}

	if userRole == nil {
		t.Fatal("expected UserRole enum")
	}

	if len(userRole.Children) != 3 {
		t.Errorf("expected 3 enum members, got %d", len(userRole.Children))
	}

	// Find DEFAULT_USER constant
	var defaultUser *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "DEFAULT_USER" {
			defaultUser = sym
			break
		}
	}

	if defaultUser == nil {
		t.Fatal("expected DEFAULT_USER constant")
	}

	if !defaultUser.Exported {
		t.Error("expected DEFAULT_USER to be exported")
	}

	// Find internalHelper (not exported)
	var internalHelper *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "internalHelper" {
			internalHelper = sym
			break
		}
	}

	if internalHelper == nil {
		t.Fatal("expected internalHelper")
	}

	if internalHelper.Exported {
		t.Error("expected internalHelper to NOT be exported")
	}
}

func TestTypeScriptParser_Parse_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond)

	parser := NewTypeScriptParser()
	_, err := parser.Parse(ctx, []byte("const x = 1;"), "test.ts")

	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestTypeScriptParser_Parse_ClassInheritance(t *testing.T) {
	source := `export class Child extends Parent implements IFoo, IBar {
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class")
	}

	if class.Metadata == nil {
		t.Fatal("expected metadata")
	}

	if class.Metadata.Extends != "Parent" {
		t.Errorf("expected extends 'Parent', got %q", class.Metadata.Extends)
	}

	if len(class.Metadata.Implements) != 2 {
		t.Errorf("expected 2 implements, got %d", len(class.Metadata.Implements))
	}
}

func TestTypeScriptParser_Parse_NonExportedType(t *testing.T) {
	source := `type InternalType = { secret: boolean };
export type PublicType = string;
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var internal, public *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindType {
			switch sym.Name {
			case "InternalType":
				internal = sym
			case "PublicType":
				public = sym
			}
		}
	}

	if internal == nil || public == nil {
		t.Fatal("expected both types")
	}

	if internal.Exported {
		t.Error("expected InternalType to NOT be exported")
	}

	if !public.Exported {
		t.Error("expected PublicType to be exported")
	}
}

// Benchmark parsing
func BenchmarkTypeScriptParser_Parse(b *testing.B) {
	parser := NewTypeScriptParser()
	content := []byte(typescriptTestSource)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.Parse(context.Background(), content, "test.ts")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTypeScriptParser_Parse_Concurrent(b *testing.B) {
	parser := NewTypeScriptParser()
	content := []byte(typescriptTestSource)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := parser.Parse(context.Background(), content, "test.ts")
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// GR-41: Tests for TypeScript call site extraction

const tsCallsSource = `
export class NestFactory {
    static async create(module: any): Promise<INestApplication> {
        const container = new NestContainer();
        await container.initialize(module);
        const app = this.createNestInstance(container);
        return app;
    }

    private static createNestInstance(container: NestContainer): INestApplication {
        const httpAdapter = container.getHttpAdapter();
        return new NestApplication(httpAdapter);
    }
}

export class AppController {
    constructor(private readonly appService: AppService) {}

    getHello(): string {
        return this.appService.getHello();
    }

    async handleRequest(req: Request): Promise<Response> {
        const validated = this.validate(req);
        const result = await this.appService.process(validated);
        return formatResponse(result);
    }
}

function formatResponse(data: any): Response {
    const serialized = JSON.stringify(data);
    return createResponse(serialized);
}
`

func TestTypeScriptParser_ExtractCallSites_ThisMethodCalls(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(tsCallsSource), "nest.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find AppController.handleRequest method
	var handleMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "AppController" {
			for _, child := range sym.Children {
				if child.Name == "handleRequest" {
					handleMethod = child
					break
				}
			}
		}
	}

	if handleMethod == nil {
		t.Fatal("AppController.handleRequest method not found")
	}

	if len(handleMethod.Calls) == 0 {
		t.Fatal("handleRequest should have call sites extracted")
	}

	callTargets := make(map[string]bool)
	for _, call := range handleMethod.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["validate"] {
		t.Errorf("expected call to validate, got: %v", tsCallTargetNames(handleMethod.Calls))
	}

	// Verify this.method() calls
	for _, call := range handleMethod.Calls {
		if call.Target == "validate" {
			if !call.IsMethod {
				t.Errorf("call to validate should be IsMethod=true")
			}
			if call.Receiver != "this" {
				t.Errorf("call to validate should have Receiver='this', got %q", call.Receiver)
			}
		}
	}
}

func TestTypeScriptParser_ExtractCallSites_StaticMethodCalls(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(tsCallsSource), "nest.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find NestFactory.create method
	var createMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "NestFactory" {
			for _, child := range sym.Children {
				if child.Name == "create" {
					createMethod = child
					break
				}
			}
		}
	}

	if createMethod == nil {
		t.Fatal("NestFactory.create method not found")
	}

	if len(createMethod.Calls) == 0 {
		t.Fatal("create should have call sites extracted")
	}

	callTargets := make(map[string]bool)
	for _, call := range createMethod.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["initialize"] {
		t.Errorf("expected call to initialize, got: %v", tsCallTargetNames(createMethod.Calls))
	}
	if !callTargets["createNestInstance"] {
		t.Errorf("expected call to createNestInstance, got: %v", tsCallTargetNames(createMethod.Calls))
	}
}

func TestTypeScriptParser_ExtractCallSites_SimpleFunctionCalls(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(tsCallsSource), "nest.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var formatFn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "formatResponse" {
			formatFn = sym
			break
		}
	}

	if formatFn == nil {
		t.Fatal("formatResponse function not found")
	}

	if len(formatFn.Calls) < 1 {
		t.Errorf("expected at least 1 call, got %d", len(formatFn.Calls))
	}

	callTargets := make(map[string]bool)
	for _, call := range formatFn.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["createResponse"] {
		t.Errorf("expected call to createResponse, got: %v", tsCallTargetNames(formatFn.Calls))
	}
}

func TestTypeScriptParser_ExtractCallSites_ChainedPropertyCalls(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	result, err := parser.Parse(context.Background(), []byte(tsCallsSource), "nest.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find AppController.handleRequest — has this.appService.process() (chained)
	var handleMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "AppController" {
			for _, child := range sym.Children {
				if child.Name == "handleRequest" {
					handleMethod = child
					break
				}
			}
		}
	}

	if handleMethod == nil {
		t.Fatal("handleRequest not found")
	}

	// Should extract process() call with receiver "this.appService"
	found := false
	for _, call := range handleMethod.Calls {
		if call.Target == "process" {
			found = true
			if !call.IsMethod {
				t.Error("process should be a method call")
			}
		}
	}
	if !found {
		t.Errorf("expected call to process, got: %v", tsCallTargetNames(handleMethod.Calls))
	}
}

func tsCallTargetNames(calls []CallSite) []string {
	names := make([]string, len(calls))
	for i, call := range calls {
		if call.IsMethod {
			names[i] = call.Receiver + "." + call.Target
		} else {
			names[i] = call.Target
		}
	}
	return names
}

// ============================================================================
// IT-03a A-2: Interface Extends Extraction
// ============================================================================

func TestTypeScriptParser_InterfaceExtends_Single(t *testing.T) {
	source := `export interface Foo extends Bar {
    name: string;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Foo" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Foo'")
	}

	if iface.Metadata == nil {
		t.Fatal("expected metadata on interface Foo")
	}

	if iface.Metadata.Extends != "Bar" {
		t.Errorf("expected Extends='Bar', got %q", iface.Metadata.Extends)
	}

	if len(iface.Metadata.Implements) != 0 {
		t.Errorf("expected no Implements for single extends, got %v", iface.Metadata.Implements)
	}
}

func TestTypeScriptParser_InterfaceExtends_Multiple(t *testing.T) {
	source := `export interface Foo extends Bar, Baz {
    id: number;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Foo" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Foo'")
	}

	if iface.Metadata == nil {
		t.Fatal("expected metadata on interface Foo")
	}

	if iface.Metadata.Extends != "Bar" {
		t.Errorf("expected Extends='Bar', got %q", iface.Metadata.Extends)
	}

	if len(iface.Metadata.Implements) != 1 {
		t.Fatalf("expected 1 Implements entry, got %d: %v", len(iface.Metadata.Implements), iface.Metadata.Implements)
	}

	if iface.Metadata.Implements[0] != "Baz" {
		t.Errorf("expected Implements[0]='Baz', got %q", iface.Metadata.Implements[0])
	}
}

func TestTypeScriptParser_InterfaceExtends_Generic(t *testing.T) {
	source := `export interface Foo extends Bar<T> {
    value: T;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Foo" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Foo'")
	}

	if iface.Metadata == nil {
		t.Fatal("expected metadata on interface Foo")
	}

	if iface.Metadata.Extends != "Bar" {
		t.Errorf("expected Extends='Bar' (without generic params), got %q", iface.Metadata.Extends)
	}
}

func TestTypeScriptParser_InterfaceExtends_None(t *testing.T) {
	source := `export interface Foo {
    name: string;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Foo" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Foo'")
	}

	// With no extends, Metadata may or may not exist, but Extends should be empty
	if iface.Metadata != nil && iface.Metadata.Extends != "" {
		t.Errorf("expected no Extends set, got %q", iface.Metadata.Extends)
	}
}

// ============================================================================
// IT-03a A-3: Decorator Arguments
// ============================================================================

func TestTypeScriptParser_DecoratorArgs_Class(t *testing.T) {
	source := `@Module({providers: [UserService]})
export class AppModule {}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "AppModule" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'AppModule'")
	}

	if class.Metadata == nil {
		t.Fatal("expected metadata on AppModule")
	}

	if class.Metadata.DecoratorArgs == nil {
		t.Fatal("expected DecoratorArgs to be populated")
	}

	moduleArgs, ok := class.Metadata.DecoratorArgs["Module"]
	if !ok {
		t.Fatalf("expected DecoratorArgs to contain key 'Module', got %v", class.Metadata.DecoratorArgs)
	}

	found := false
	for _, arg := range moduleArgs {
		if arg == "UserService" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected DecoratorArgs['Module'] to contain 'UserService', got %v", moduleArgs)
	}
}

func TestTypeScriptParser_DecoratorArgs_SimpleArg(t *testing.T) {
	source := `@UseInterceptors(LoggingInterceptor)
export class Foo {}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Foo" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'Foo'")
	}

	if class.Metadata == nil || class.Metadata.DecoratorArgs == nil {
		t.Fatal("expected DecoratorArgs on class Foo")
	}

	args, ok := class.Metadata.DecoratorArgs["UseInterceptors"]
	if !ok {
		t.Fatalf("expected DecoratorArgs key 'UseInterceptors', got %v", class.Metadata.DecoratorArgs)
	}

	if len(args) != 1 || args[0] != "LoggingInterceptor" {
		t.Errorf("expected ['LoggingInterceptor'], got %v", args)
	}
}

func TestTypeScriptParser_DecoratorArgs_NoArgs(t *testing.T) {
	source := `@Injectable()
export class Foo {}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Foo" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'Foo'")
	}

	// @Injectable() has no arguments, so DecoratorArgs should be nil or empty
	if class.Metadata != nil && class.Metadata.DecoratorArgs != nil {
		if args, ok := class.Metadata.DecoratorArgs["Injectable"]; ok && len(args) > 0 {
			t.Errorf("expected no DecoratorArgs for @Injectable(), got %v", args)
		}
	}
}

func TestTypeScriptParser_DecoratorArgs_PlainDecorator(t *testing.T) {
	source := `@Controller
export class Foo {}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Foo" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected class 'Foo'")
	}

	// Plain decorator (not a call) should have no DecoratorArgs
	if class.Metadata != nil && class.Metadata.DecoratorArgs != nil {
		if args, ok := class.Metadata.DecoratorArgs["Controller"]; ok && len(args) > 0 {
			t.Errorf("expected no DecoratorArgs for plain @Controller, got %v", args)
		}
	}
}

// ============================================================================
// IT-03a B-3: Re-export Module Resolution
// ============================================================================

func TestTypeScriptParser_ReExport_NamedFromModule(t *testing.T) {
	source := `export { Foo } from './bar';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found bool
	for _, imp := range result.Imports {
		if imp.Path == "./bar" {
			found = true
			if imp.Location.StartLine < 1 {
				t.Errorf("expected re-export Import to have valid Location, got StartLine=%d", imp.Location.StartLine)
			}
			break
		}
	}

	if !found {
		t.Errorf("expected import with Path='./bar' from re-export, got imports: %+v", result.Imports)
	}
}

func TestTypeScriptParser_ReExport_TypeExport(t *testing.T) {
	source := `export type { Foo } from './bar';
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var found bool
	for _, imp := range result.Imports {
		if imp.Path == "./bar" {
			found = true
			if imp.Location.StartLine < 1 {
				t.Errorf("expected re-export Import to have valid Location, got StartLine=%d", imp.Location.StartLine)
			}
			break
		}
	}

	if !found {
		t.Errorf("expected import with Path='./bar' from type re-export, got imports: %+v", result.Imports)
	}
}

// ============================================================================
// IT-03a C-1: Callback Argument Tracking
// ============================================================================

func TestTypeScriptParser_CallbackArgs(t *testing.T) {
	source := `export function setup(app: Application): void {
    app.use(middleware);
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "setup" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'setup'")
	}

	if len(fn.Calls) == 0 {
		t.Fatal("expected call sites in setup function")
	}

	// Find the app.use() call
	var useCall *CallSite
	for i := range fn.Calls {
		if fn.Calls[i].Target == "use" {
			useCall = &fn.Calls[i]
			break
		}
	}

	if useCall == nil {
		t.Fatalf("expected call to 'use', got: %v", tsCallTargetNames(fn.Calls))
	}

	found := false
	for _, arg := range useCall.FunctionArgs {
		if arg == "middleware" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected FunctionArgs to contain 'middleware', got %v", useCall.FunctionArgs)
	}
}

func TestTypeScriptParser_CallbackArgs_SkipsKeywords(t *testing.T) {
	source := `export function doStuff(): void {
    foo(true, null, undefined);
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "doStuff" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'doStuff'")
	}

	// Find the foo() call
	var fooCall *CallSite
	for i := range fn.Calls {
		if fn.Calls[i].Target == "foo" {
			fooCall = &fn.Calls[i]
			break
		}
	}

	if fooCall == nil {
		t.Fatalf("expected call to 'foo', got: %v", tsCallTargetNames(fn.Calls))
	}

	// true, null, undefined should all be skipped
	if len(fooCall.FunctionArgs) != 0 {
		t.Errorf("expected no FunctionArgs (keywords should be skipped), got %v", fooCall.FunctionArgs)
	}
}

// ============================================================================
// IT-03a C-2: Generic Type Argument Tracking
// ============================================================================

func TestTypeScriptParser_TypeArguments_ReturnType(t *testing.T) {
	source := `export function foo(): Promise<User> {
    return Promise.resolve(null);
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	if fn.Metadata == nil {
		t.Fatal("expected metadata on function 'foo'")
	}

	found := false
	for _, ta := range fn.Metadata.TypeArguments {
		if ta == "User" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected TypeArguments to contain 'User', got %v", fn.Metadata.TypeArguments)
	}
}

func TestTypeScriptParser_TypeArguments_Complex(t *testing.T) {
	source := `export function foo(): Map<string, Handler> {
    return new Map();
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	if fn.Metadata == nil {
		t.Fatal("expected metadata on function 'foo'")
	}

	// Should contain "Handler" but not "string" (primitive)
	hasHandler := false
	hasString := false
	for _, ta := range fn.Metadata.TypeArguments {
		if ta == "Handler" {
			hasHandler = true
		}
		if ta == "string" {
			hasString = true
		}
	}

	if !hasHandler {
		t.Errorf("expected TypeArguments to contain 'Handler', got %v", fn.Metadata.TypeArguments)
	}

	if hasString {
		t.Errorf("expected TypeArguments to NOT contain 'string' (primitive), got %v", fn.Metadata.TypeArguments)
	}
}

func TestTypeScriptParser_TypeArguments_Array(t *testing.T) {
	source := `export function foo(): Observable<Event[]> {
    return null;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	if fn.Metadata == nil {
		t.Fatal("expected metadata on function 'foo'")
	}

	// Should contain "Event" (with [] stripped)
	found := false
	for _, ta := range fn.Metadata.TypeArguments {
		if ta == "Event" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected TypeArguments to contain 'Event' ([] stripped), got %v", fn.Metadata.TypeArguments)
	}
}

func TestTypeScriptParser_TypeArguments_NoPrimitives(t *testing.T) {
	source := `export function foo(): Map<string, number> {
    return new Map();
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "foo" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'foo'")
	}

	// When all type args are primitives, TypeArguments should be empty/nil
	if fn.Metadata != nil && len(fn.Metadata.TypeArguments) > 0 {
		t.Errorf("expected no TypeArguments (all primitives), got %v", fn.Metadata.TypeArguments)
	}
}

func TestExtractTypeArgumentIdentifiers_Unit(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "no generics",
			input:    "string",
			expected: nil,
		},
		{
			name:     "simple generic",
			input:    "Promise<User>",
			expected: []string{"User"},
		},
		{
			name:     "primitive generic",
			input:    "Promise<string>",
			expected: nil,
		},
		{
			name:     "multiple type args",
			input:    "Map<string, Handler>",
			expected: []string{"Handler"},
		},
		{
			name:     "array type argument",
			input:    "Observable<Event[]>",
			expected: []string{"Event"},
		},
		{
			name:     "all primitives",
			input:    "Map<string, number>",
			expected: nil,
		},
		{
			name:     "multiple non-primitives",
			input:    "Either<User, Error>",
			expected: []string{"User", "Error"},
		},
		{
			name:     "nested generics",
			input:    "Promise<Array<User>>",
			expected: []string{"User"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTypeArgumentIdentifiers(tt.input)

			if tt.expected == nil {
				if len(result) != 0 {
					t.Errorf("expected nil/empty result, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d identifiers, got %d: %v", len(tt.expected), len(result), result)
			}

			for i, exp := range tt.expected {
				if result[i] != exp {
					t.Errorf("expected result[%d]=%q, got %q", i, exp, result[i])
				}
			}
		})
	}
}

// ============================================================================
// IT-03a C-3: Type Narrowing Detection
// ============================================================================

func TestTypeScriptParser_TypeNarrowing_Instanceof(t *testing.T) {
	source := `export function handle(x: unknown): void {
    if (x instanceof Router) {
        x.route();
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "handle" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'handle'")
	}

	if fn.Metadata == nil {
		t.Fatal("expected metadata on function 'handle'")
	}

	found := false
	for _, tn := range fn.Metadata.TypeNarrowings {
		if tn == "Router" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected TypeNarrowings to contain 'Router', got %v", fn.Metadata.TypeNarrowings)
	}
}

func TestTypeScriptParser_TypeNarrowing_MultipleInstanceof(t *testing.T) {
	source := `export function handle(a: unknown, b: unknown): void {
    if (a instanceof Foo && b instanceof Bar) {
        return;
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "handle" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'handle'")
	}

	if fn.Metadata == nil {
		t.Fatal("expected metadata on function 'handle'")
	}

	narrowingSet := make(map[string]bool)
	for _, tn := range fn.Metadata.TypeNarrowings {
		narrowingSet[tn] = true
	}

	if !narrowingSet["Foo"] {
		t.Errorf("expected TypeNarrowings to contain 'Foo', got %v", fn.Metadata.TypeNarrowings)
	}

	if !narrowingSet["Bar"] {
		t.Errorf("expected TypeNarrowings to contain 'Bar', got %v", fn.Metadata.TypeNarrowings)
	}
}

func TestTypeScriptParser_TypeNarrowing_NonePresent(t *testing.T) {
	source := `export function greet(name: string): string {
    return "Hello, " + name;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "greet" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'greet'")
	}

	if fn.Metadata != nil && len(fn.Metadata.TypeNarrowings) > 0 {
		t.Errorf("expected no TypeNarrowings, got %v", fn.Metadata.TypeNarrowings)
	}
}

func TestTypeScriptParser_TypeNarrowing_NestedFunction(t *testing.T) {
	source := `export function outer(x: unknown): void {
    const inner = (y: unknown) => {
        if (y instanceof NestedType) {
            return;
        }
    };
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindFunction && sym.Name == "outer" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected function 'outer'")
	}

	// The instanceof in the nested arrow function should NOT be extracted
	// into the outer function's TypeNarrowings
	if fn.Metadata != nil {
		for _, tn := range fn.Metadata.TypeNarrowings {
			if tn == "NestedType" {
				t.Errorf("expected 'NestedType' to NOT appear in outer function's TypeNarrowings (it is in a nested arrow function)")
			}
		}
	}
}
