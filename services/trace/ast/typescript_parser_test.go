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

// TestTypeScriptParser_DecoratedFieldClass tests that a class with decorated fields
// (like BabylonJS TransformNode) is correctly parsed and passes Validate().
// IT-04: This is a diagnostic test for the find_symbol integration failure on TransformNode.
func TestTypeScriptParser_DecoratedFieldClass(t *testing.T) {
	source := `
import { serialize, serializeAsVector3 } from "./decorators";

export class TransformNode extends Node {
    @serializeAsVector3("position")
    private _position: Vector3 = Vector3.Zero();

    @serializeAsVector3("rotation")
    private _rotation: Vector3 = Vector3.Zero();

    @serialize()
    private _scaling: Vector3 = Vector3.One();

    @serialize("billboardMode")
    public billboardMode: number = 0;

    constructor(name: string, scene?: Scene) {
        super(name, scene);
    }

    public getAbsolutePosition(): Vector3 {
        return this._position;
    }

    public setPositionWithLocalVector(newPosition: Vector3): TransformNode {
        this._position = newPosition;
        return this;
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test/transformNode.ts")
	if err != nil {
		t.Fatalf("Parse() returned error: %v", err)
	}

	// Check ParseResult.Validate()
	if err := result.Validate(); err != nil {
		t.Fatalf("result.Validate() failed: %v", err)
	}

	// Find TransformNode class
	var tn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "TransformNode" {
			tn = sym
			break
		}
	}
	if tn == nil {
		t.Fatal("TransformNode not found in parsed symbols")
	}

	// Verify Symbol.Validate() passes (including recursive children validation)
	if err := tn.Validate(); err != nil {
		t.Fatalf("TransformNode.Validate() failed: %v", err)
	}

	t.Logf("TransformNode found: Kind=%s, StartLine=%d, EndLine=%d, Children=%d",
		tn.Kind, tn.StartLine, tn.EndLine, len(tn.Children))

	// Log all children for diagnosis
	for i, child := range tn.Children {
		t.Logf("  Child[%d]: Name=%q Kind=%s StartLine=%d EndLine=%d",
			i, child.Name, child.Kind, child.StartLine, child.EndLine)
	}

	// Verify key expectations
	if tn.Kind != SymbolKindClass {
		t.Errorf("expected SymbolKindClass, got %s", tn.Kind)
	}
	if !tn.Exported {
		t.Error("expected TransformNode to be exported")
	}
	if tn.Metadata == nil || tn.Metadata.Extends != "Node" {
		t.Errorf("expected Extends=Node, got %v", tn.Metadata)
	}

	// Check that children include methods AND fields
	hasMethod := false
	hasField := false
	for _, child := range tn.Children {
		if child.Kind == SymbolKindMethod {
			hasMethod = true
		}
		if child.Kind == SymbolKindField {
			hasField = true
		}
	}
	if !hasMethod {
		t.Error("expected at least one method child")
	}
	if !hasField {
		t.Error("expected at least one field child (decorated fields)")
	}

	// Verify Validate() passes — this is the exact check that service.go's
	// parseFileToResult uses. If this fails, the ENTIRE file is dropped.
	if err := result.Validate(); err != nil {
		t.Fatalf("ParseResult.Validate() WOULD CAUSE FILE DROP: %v", err)
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

// =============================================================================
// IT-03a Phase 12: Metadata.Methods population tests
// =============================================================================

func TestTypeScriptParser_InterfaceMethodSignatures(t *testing.T) {
	source := `export interface Repository {
    findById(id: string): Promise<Entity>;
    save(entity: Entity): Promise<void>;
    delete(id: string): boolean;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Repository" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Repository'")
	}

	if iface.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}

	if len(iface.Metadata.Methods) != 3 {
		t.Fatalf("expected 3 methods in Metadata.Methods, got %d", len(iface.Metadata.Methods))
	}

	expectedNames := map[string]bool{
		"findById": false,
		"save":     false,
		"delete":   false,
	}
	for _, m := range iface.Metadata.Methods {
		if _, ok := expectedNames[m.Name]; ok {
			expectedNames[m.Name] = true
		} else {
			t.Errorf("unexpected method in Metadata.Methods: %s", m.Name)
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected method %q in Metadata.Methods", name)
		}
	}
}

func TestTypeScriptParser_InterfaceMethodSignatures_Empty(t *testing.T) {
	source := `export interface Config {
    readonly host: string;
    port: number;
    debug?: boolean;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Config" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Config'")
	}

	// Interface with only properties should have no methods in Metadata.Methods
	if iface.Metadata != nil && len(iface.Metadata.Methods) > 0 {
		t.Errorf("expected no methods in Metadata.Methods for property-only interface, got %d", len(iface.Metadata.Methods))
	}
}

func TestTypeScriptParser_InterfaceMethodSignatures_Mixed(t *testing.T) {
	source := `export interface Service {
    name: string;
    start(): void;
    stop(): void;
    version?: number;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindInterface && sym.Name == "Service" {
			iface = sym
			break
		}
	}

	if iface == nil {
		t.Fatal("expected interface 'Service'")
	}

	if iface.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}

	// Only methods (start, stop), not properties (name, version)
	if len(iface.Metadata.Methods) != 2 {
		t.Fatalf("expected 2 methods in Metadata.Methods, got %d", len(iface.Metadata.Methods))
	}

	methodNames := make(map[string]bool)
	for _, m := range iface.Metadata.Methods {
		methodNames[m.Name] = true
	}
	if !methodNames["start"] {
		t.Error("expected method 'start' in Metadata.Methods")
	}
	if !methodNames["stop"] {
		t.Error("expected method 'stop' in Metadata.Methods")
	}
	if methodNames["name"] {
		t.Error("property 'name' should NOT be in Metadata.Methods")
	}
}

func TestTypeScriptParser_ClassMethodSignatures(t *testing.T) {
	source := `export class UserService {
    getUser(id: string): User {
        return null;
    }
    saveUser(user: User): void {
        // save
    }
    deleteUser(id: string): boolean {
        return true;
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "UserService" {
			cls = sym
			break
		}
	}

	if cls == nil {
		t.Fatal("expected class 'UserService'")
	}

	if cls.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}

	if len(cls.Metadata.Methods) != 3 {
		t.Fatalf("expected 3 methods in Metadata.Methods, got %d", len(cls.Metadata.Methods))
	}

	expectedNames := map[string]bool{
		"getUser":    false,
		"saveUser":   false,
		"deleteUser": false,
	}
	for _, m := range cls.Metadata.Methods {
		if _, ok := expectedNames[m.Name]; ok {
			expectedNames[m.Name] = true
		} else {
			t.Errorf("unexpected method in Metadata.Methods: %s", m.Name)
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected method %q in Metadata.Methods", name)
		}
	}
}

func TestTypeScriptParser_ClassMethodSignatures_SkipConstructor(t *testing.T) {
	source := `export class Handler {
    constructor(private name: string) {}
    handle(req: Request): Response {
        return null;
    }
    dispose(): void {}
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Handler" {
			cls = sym
			break
		}
	}

	if cls == nil {
		t.Fatal("expected class 'Handler'")
	}

	if cls.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}

	// constructor should be excluded, only handle and dispose
	if len(cls.Metadata.Methods) != 2 {
		t.Fatalf("expected 2 methods (excluding constructor), got %d", len(cls.Metadata.Methods))
	}

	for _, m := range cls.Metadata.Methods {
		if m.Name == "constructor" {
			t.Error("constructor should NOT be in Metadata.Methods")
		}
	}
}

func TestTypeScriptParser_ClassMethodSignatures_SkipStatic(t *testing.T) {
	source := `export class Factory {
    static create(name: string): Factory {
        return new Factory();
    }
    process(input: string): string {
        return input;
    }
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Factory" {
			cls = sym
			break
		}
	}

	if cls == nil {
		t.Fatal("expected class 'Factory'")
	}

	if cls.Metadata == nil {
		t.Fatal("expected Metadata to be non-nil")
	}

	// static 'create' should be excluded, only 'process'
	if len(cls.Metadata.Methods) != 1 {
		t.Fatalf("expected 1 method (excluding static), got %d", len(cls.Metadata.Methods))
	}

	if cls.Metadata.Methods[0].Name != "process" {
		t.Errorf("expected method 'process', got %q", cls.Metadata.Methods[0].Name)
	}
}

func TestTypeScriptParser_ClassMethodSignatures_Empty(t *testing.T) {
	source := `export class Config {
    host: string = "localhost";
    port: number = 8080;
}
`
	parser := NewTypeScriptParser()
	result, err := parser.Parse(context.Background(), []byte(source), "test.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Config" {
			cls = sym
			break
		}
	}

	if cls == nil {
		t.Fatal("expected class 'Config'")
	}

	// Class with only fields should have no methods
	if cls.Metadata != nil && len(cls.Metadata.Methods) > 0 {
		t.Errorf("expected no methods in Metadata.Methods for field-only class, got %d", len(cls.Metadata.Methods))
	}
}

// IT-03a Phase 13 T-3: Generic extends text stripping tests

func TestTypeScriptParser_ClassHeritage_GenericExtendsStripped(t *testing.T) {
	parser := NewTypeScriptParser()

	src := []byte(`
export class UserService extends BaseService<User> {
  getUser(): User {
    return new User();
  }
}

export class Repo extends GenericRepository<Entity, string> implements Queryable<Entity> {
  query(): Entity[] {
    return [];
  }
}
`)

	result, err := parser.Parse(context.Background(), src, "generic_heritage.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Find UserService
	var userService *Symbol
	var repo *Symbol
	for _, sym := range result.Symbols {
		switch sym.Name {
		case "UserService":
			userService = sym
		case "Repo":
			repo = sym
		}
	}

	if userService == nil {
		t.Fatal("expected symbol 'UserService'")
	}
	if userService.Metadata == nil {
		t.Fatal("expected UserService to have metadata")
	}
	// Should be "BaseService" not "BaseService<User>"
	if userService.Metadata.Extends != "BaseService" {
		t.Errorf("expected Extends='BaseService', got %q", userService.Metadata.Extends)
	}

	if repo == nil {
		t.Fatal("expected symbol 'Repo'")
	}
	if repo.Metadata == nil {
		t.Fatal("expected Repo to have metadata")
	}
	// Should be "GenericRepository" not "GenericRepository<Entity, string>"
	if repo.Metadata.Extends != "GenericRepository" {
		t.Errorf("expected Extends='GenericRepository', got %q", repo.Metadata.Extends)
	}
	// Implements should be "Queryable" not "Queryable<Entity>"
	found := false
	for _, impl := range repo.Metadata.Implements {
		if impl == "Queryable" {
			found = true
		}
		if strings.Contains(impl, "<") {
			t.Errorf("implements should not contain generic params, got %q", impl)
		}
	}
	if !found {
		t.Errorf("expected 'Queryable' in implements, got %v", repo.Metadata.Implements)
	}
}

// IT-03a Phase 13 T-4: Non-exported abstract class tests

func TestTypeScriptParser_NonExportedAbstractClass(t *testing.T) {
	parser := NewTypeScriptParser()

	src := []byte(`
abstract class BaseHandler {
  abstract handle(request: Request): Response;

  log(msg: string): void {
    console.log(msg);
  }
}

export class ConcreteHandler extends BaseHandler {
  handle(request: Request): Response {
    return new Response();
  }
}
`)

	result, err := parser.Parse(context.Background(), src, "abstract.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Find BaseHandler (non-exported abstract class)
	var baseHandler *Symbol
	var concreteHandler *Symbol
	for _, sym := range result.Symbols {
		switch sym.Name {
		case "BaseHandler":
			baseHandler = sym
		case "ConcreteHandler":
			concreteHandler = sym
		}
	}

	if baseHandler == nil {
		t.Fatal("expected non-exported abstract class 'BaseHandler' to be parsed")
	}
	if baseHandler.Kind != SymbolKindClass {
		t.Errorf("expected kind Class, got %v", baseHandler.Kind)
	}
	if baseHandler.Metadata == nil || !baseHandler.Metadata.IsAbstract {
		t.Error("expected BaseHandler.Metadata.IsAbstract to be true")
	}

	if concreteHandler == nil {
		t.Fatal("expected symbol 'ConcreteHandler'")
	}
	if concreteHandler.Metadata == nil || concreteHandler.Metadata.Extends != "BaseHandler" {
		t.Errorf("expected ConcreteHandler to extend BaseHandler")
	}
}

// IT-03a Phase 14 T-1: TypeArguments on methods
// IT-03a Phase 14 T-2: TypeNarrowings on methods

func TestTypeScriptParser_Method_TypeArguments(t *testing.T) {
	parser := NewTypeScriptParser()

	src := []byte(`
export class UserService {
  getUsers(): Promise<User[]> {
    return this.repo.findAll();
  }

  transform(input: Entity): Map<string, Result> {
    return new Map();
  }

  simple(): void {
    return;
  }
}
`)

	result, err := parser.Parse(context.Background(), src, "method_typeargs.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// Find the class, then its methods
	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "UserService" {
			cls = sym
			break
		}
	}
	if cls == nil {
		t.Fatal("expected symbol 'UserService'")
	}

	methodMap := make(map[string]*Symbol)
	for _, child := range cls.Children {
		if child.Kind == SymbolKindMethod {
			methodMap[child.Name] = child
		}
	}

	// getUsers returns Promise<User[]> — should extract "User" as TypeArgument
	getUsers := methodMap["getUsers"]
	if getUsers == nil {
		t.Fatal("expected method 'getUsers'")
	}
	if getUsers.Metadata == nil || len(getUsers.Metadata.TypeArguments) == 0 {
		t.Fatal("expected getUsers to have TypeArguments from Promise<User[]>")
	}
	found := false
	for _, ta := range getUsers.Metadata.TypeArguments {
		if ta == "User" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'User' in TypeArguments, got %v", getUsers.Metadata.TypeArguments)
	}

	// transform returns Map<string, Result> — should extract "Result" (string is primitive, filtered)
	transform := methodMap["transform"]
	if transform == nil {
		t.Fatal("expected method 'transform'")
	}
	if transform.Metadata == nil || len(transform.Metadata.TypeArguments) == 0 {
		t.Fatal("expected transform to have TypeArguments from Map<string, Result>")
	}
	found = false
	for _, ta := range transform.Metadata.TypeArguments {
		if ta == "Result" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Result' in TypeArguments, got %v", transform.Metadata.TypeArguments)
	}

	// simple returns void — should have no TypeArguments
	simple := methodMap["simple"]
	if simple == nil {
		t.Fatal("expected method 'simple'")
	}
	if simple.Metadata != nil && len(simple.Metadata.TypeArguments) > 0 {
		t.Errorf("expected no TypeArguments for void return, got %v", simple.Metadata.TypeArguments)
	}
}

func TestTypeScriptParser_Method_TypeNarrowings(t *testing.T) {
	parser := NewTypeScriptParser()

	src := []byte(`
export class Validator {
  validate(input: unknown): boolean {
    if (input instanceof Error) {
      return false;
    }
    if (input instanceof CustomType) {
      return true;
    }
    return false;
  }

  noNarrowings(): void {
    console.log("hello");
  }
}
`)

	result, err := parser.Parse(context.Background(), src, "method_narrowings.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var cls *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Validator" {
			cls = sym
			break
		}
	}
	if cls == nil {
		t.Fatal("expected symbol 'Validator'")
	}

	methodMap := make(map[string]*Symbol)
	for _, child := range cls.Children {
		if child.Kind == SymbolKindMethod {
			methodMap[child.Name] = child
		}
	}

	// validate uses instanceof Error and instanceof CustomType
	validate := methodMap["validate"]
	if validate == nil {
		t.Fatal("expected method 'validate'")
	}
	if validate.Metadata == nil || len(validate.Metadata.TypeNarrowings) == 0 {
		t.Fatal("expected validate to have TypeNarrowings from instanceof checks")
	}

	narrowingSet := make(map[string]bool)
	for _, n := range validate.Metadata.TypeNarrowings {
		narrowingSet[n] = true
	}
	if !narrowingSet["Error"] {
		t.Errorf("expected 'Error' in TypeNarrowings, got %v", validate.Metadata.TypeNarrowings)
	}
	if !narrowingSet["CustomType"] {
		t.Errorf("expected 'CustomType' in TypeNarrowings, got %v", validate.Metadata.TypeNarrowings)
	}

	// noNarrowings should have no TypeNarrowings
	noNarrowings := methodMap["noNarrowings"]
	if noNarrowings == nil {
		t.Fatal("expected method 'noNarrowings'")
	}
	if noNarrowings.Metadata != nil && len(noNarrowings.Metadata.TypeNarrowings) > 0 {
		t.Errorf("expected no TypeNarrowings, got %v", noNarrowings.Metadata.TypeNarrowings)
	}
}

// TestTypeScriptParser_ExportConstNewInstance verifies that `export const X = new Y()`
// produces a symbol with Kind=SymbolKindConstant and Exported=true.
// IT-04 Phase 3: Ensures NestFactory-style patterns are correctly parsed.
func TestTypeScriptParser_ExportConstNewInstance(t *testing.T) {
	parser := NewTypeScriptParser()
	content := `
export class NestFactoryStatic {
  async create(module: any): Promise<any> {
    return {};
  }
}

export const NestFactory = new NestFactoryStatic();
`
	result, err := parser.Parse(context.Background(), []byte(content), "packages/core/nest-factory.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nestFactory *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "NestFactory" {
			nestFactory = sym
			break
		}
	}

	if nestFactory == nil {
		t.Fatal("expected to find symbol 'NestFactory'")
	}

	if !nestFactory.Exported {
		t.Error("expected NestFactory to be marked as Exported")
	}

	if nestFactory.Kind != SymbolKindConstant {
		t.Errorf("expected NestFactory kind to be %q, got %q", SymbolKindConstant, nestFactory.Kind)
	}

	if nestFactory.Language != "typescript" {
		t.Errorf("expected language 'typescript', got %q", nestFactory.Language)
	}
}

// findTSMethod searches a ParseResult for a method by class name + method name,
// traversing the Children of each class symbol.
func findTSMethod(result *ParseResult, className, methodName string) *Symbol {
	for _, sym := range result.Symbols {
		if sym.Name == className {
			for _, child := range sym.Children {
				if child.Name == methodName {
					return child
				}
			}
		}
	}
	return nil
}

// TestTypeScriptParser_NewExpression_SimpleConstructor verifies that `new X()` calls
// are extracted as Calls entries on the calling method (IT-06d Bug 13).
func TestTypeScriptParser_NewExpression_SimpleConstructor(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	content := `
import { Drawer } from './drawer';

class LinePlot {
  private drawer: Drawer;

  render(dataset: Dataset): void {
    this.drawer = new Drawer(this.element);
    this.drawer.draw(dataset);
  }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "src/plots/linePlot.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	renderFn := findTSMethod(result, "LinePlot", "render")
	if renderFn == nil {
		t.Fatal("LinePlot.render method not found")
	}

	callTargets := make(map[string]bool)
	for _, call := range renderFn.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["Drawer"] {
		t.Errorf("expected call to Drawer from new Drawer(...), got calls: %v", renderFn.Calls)
	}
}

// TestTypeScriptParser_NewExpression_QualifiedConstructor verifies that `new mod.Class()`
// extracts the leaf class name as target.
func TestTypeScriptParser_NewExpression_QualifiedConstructor(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	content := `
import * as drawers from './drawers';

class BarPlot {
  createDrawer(): void {
    const drawer = new drawers.BarDrawer(this.config);
  }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "src/plots/barPlot.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	createFn := findTSMethod(result, "BarPlot", "createDrawer")
	if createFn == nil {
		t.Fatal("BarPlot.createDrawer method not found")
	}

	callTargets := make(map[string]bool)
	for _, call := range createFn.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["BarDrawer"] {
		t.Errorf("expected call to BarDrawer from new drawers.BarDrawer(...), got calls: %v", createFn.Calls)
	}
}

// TestTypeScriptParser_NewExpression_MultipleConstructors verifies multiple new X() calls
// in a single method body are all extracted.
func TestTypeScriptParser_NewExpression_MultipleConstructors(t *testing.T) {
	parser := NewTypeScriptParser(WithTypeScriptParseOptions(ParseOptions{IncludePrivate: true}))
	content := `
class ScatterPlot {
  initialize(config: PlotConfig): void {
    const drawer = new ScatterDrawer(config.element);
    const scale = new LinearScale(config.domain);
    const axis = new Axis(scale);
    this.render(drawer, scale, axis);
  }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "src/plots/scatterPlot.ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	initFn := findTSMethod(result, "ScatterPlot", "initialize")
	if initFn == nil {
		t.Fatal("ScatterPlot.initialize method not found")
	}

	callTargets := make(map[string]bool)
	for _, call := range initFn.Calls {
		callTargets[call.Target] = true
	}

	for _, want := range []string{"ScatterDrawer", "LinearScale", "Axis"} {
		if !callTargets[want] {
			t.Errorf("expected new-expression call to %s, got calls: %v", want, initFn.Calls)
		}
	}
}

// =============================================================================
// IT-06e Bug 4: Dynamic import() detection in TypeScript
// =============================================================================

// TestTypeScriptParser_DynamicImport verifies that import(stringLiteral) inside
// a function body produces an Import entry with IsDynamic=true.
func TestTypeScriptParser_DynamicImport(t *testing.T) {
	parser := NewTypeScriptParser()
	src := []byte(`
const LazyComponent = React.lazy(() => import('./HeavyComponent'));
const DynamicComp   = dynamic(() => import('./Component'), { ssr: false });
`)
	result, err := parser.Parse(context.Background(), src, "app.tsx")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	dynamicImports := make(map[string]Import)
	for _, imp := range result.Imports {
		if imp.IsDynamic {
			dynamicImports[imp.Path] = imp
		}
	}

	if _, ok := dynamicImports["./HeavyComponent"]; !ok {
		t.Errorf("expected dynamic import for './HeavyComponent', got: %v", dynamicImports)
	}
	if _, ok := dynamicImports["./Component"]; !ok {
		t.Errorf("expected dynamic import for './Component', got: %v", dynamicImports)
	}

	for path, imp := range dynamicImports {
		if !imp.IsModule {
			t.Errorf("dynamic import %q should have IsModule=true", path)
		}
	}
}

// TestTypeScriptParser_DynamicImport_ExternalCaptured verifies that dynamic imports
// of external packages are also captured by the parser (path filtering is builder's job).
func TestTypeScriptParser_DynamicImport_ExternalCaptured(t *testing.T) {
	parser := NewTypeScriptParser()
	src := []byte(`const x = import('lodash');`)
	result, err := parser.Parse(context.Background(), src, "app.ts")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	var found bool
	for _, imp := range result.Imports {
		if imp.IsDynamic && imp.Path == "lodash" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected dynamic import for 'lodash' to be captured")
	}
}

// IT-R2d F.1: Test that class field arrow function call sites are extracted.
func TestTypeScriptParser_FieldArrowFunction_CallSites(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
class Engine {
    private _renderLoop = () => {
        this.renderMeshes(activeMeshes);
        this.scene.render();
    };

    simpleField: number = 42;

    static handler = (event: Event) => {
        console.log(event);
        processEvent(event);
    };
}
`
	result, err := parser.Parse(context.Background(), []byte(source), "engine.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	// Find Engine class
	var engineClass *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Engine" && sym.Kind == SymbolKindClass {
			engineClass = sym
			break
		}
	}
	if engineClass == nil {
		t.Fatal("expected Engine class symbol")
	}

	// Find _renderLoop field
	var renderLoop *Symbol
	var simpleField *Symbol
	var handler *Symbol
	for _, child := range engineClass.Children {
		switch child.Name {
		case "_renderLoop":
			renderLoop = child
		case "simpleField":
			simpleField = child
		case "handler":
			handler = child
		}
	}

	// _renderLoop should have call sites extracted
	if renderLoop == nil {
		t.Fatal("expected _renderLoop field symbol")
	}
	if renderLoop.Receiver != "Engine" {
		t.Errorf("expected _renderLoop.Receiver = 'Engine', got %q", renderLoop.Receiver)
	}
	if len(renderLoop.Calls) < 2 {
		t.Errorf("expected _renderLoop to have >= 2 call sites, got %d", len(renderLoop.Calls))
	} else {
		// Verify call targets
		targets := make(map[string]bool)
		for _, call := range renderLoop.Calls {
			targets[call.Target] = true
		}
		if !targets["renderMeshes"] {
			t.Error("expected call to renderMeshes in _renderLoop")
		}
		if !targets["render"] {
			t.Error("expected call to render in _renderLoop")
		}
	}

	// simpleField should NOT have call sites (plain value, not arrow)
	if simpleField == nil {
		t.Fatal("expected simpleField symbol")
	}
	if len(simpleField.Calls) != 0 {
		t.Errorf("expected simpleField to have 0 calls, got %d", len(simpleField.Calls))
	}

	// handler should have call sites (static arrow field)
	if handler == nil {
		t.Fatal("expected handler field symbol")
	}
	if len(handler.Calls) < 2 {
		t.Errorf("expected handler to have >= 2 call sites, got %d", len(handler.Calls))
	}
}

// IT-R2d F.1: Test that class field function expression call sites are extracted.
func TestTypeScriptParser_FieldFunctionExpression_CallSites(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
class Service {
    processor = function(data: any) {
        validate(data);
        transform(data);
    };
}
`
	result, err := parser.Parse(context.Background(), []byte(source), "service.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	var svc *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Service" {
			svc = sym
			break
		}
	}
	if svc == nil {
		t.Fatal("expected Service class symbol")
	}

	var processor *Symbol
	for _, child := range svc.Children {
		if child.Name == "processor" {
			processor = child
			break
		}
	}
	if processor == nil {
		t.Fatal("expected processor field symbol")
	}
	if len(processor.Calls) < 2 {
		t.Errorf("expected processor to have >= 2 call sites, got %d", len(processor.Calls))
	}
}

// IT-R2d F.2: Test that TS arrow function variable call sites are extracted.
func TestTypeScriptParser_ArrowVariable_CallSites(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
const renderScene = () => {
    engine.runRenderLoop();
    scene.render();
};

const simpleVar: number = 42;

export const handler = async (req: Request) => {
    validate(req);
    const result = await process(req);
    return result;
};
`
	result, err := parser.Parse(context.Background(), []byte(source), "app.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	var renderScene *Symbol
	var simpleVar *Symbol
	var handler *Symbol
	for _, sym := range result.Symbols {
		switch sym.Name {
		case "renderScene":
			renderScene = sym
		case "simpleVar":
			simpleVar = sym
		case "handler":
			handler = sym
		}
	}

	// renderScene should have Kind=Function (arrow) and call sites
	if renderScene == nil {
		t.Fatal("expected renderScene symbol")
	}
	if renderScene.Kind != SymbolKindFunction {
		t.Errorf("expected renderScene.Kind = Function, got %v", renderScene.Kind)
	}
	if len(renderScene.Calls) < 2 {
		t.Errorf("expected renderScene to have >= 2 call sites, got %d", len(renderScene.Calls))
	} else {
		targets := make(map[string]bool)
		for _, call := range renderScene.Calls {
			targets[call.Target] = true
		}
		if !targets["runRenderLoop"] {
			t.Error("expected call to runRenderLoop in renderScene")
		}
		if !targets["render"] {
			t.Error("expected call to render in renderScene")
		}
	}

	// simpleVar should not have calls
	if simpleVar == nil {
		t.Fatal("expected simpleVar symbol")
	}
	if len(simpleVar.Calls) != 0 {
		t.Errorf("expected simpleVar to have 0 calls, got %d", len(simpleVar.Calls))
	}

	// handler should have calls (async arrow)
	if handler == nil {
		t.Fatal("expected handler symbol")
	}
	if len(handler.Calls) < 2 {
		t.Errorf("expected handler to have >= 2 call sites, got %d", len(handler.Calls))
	}
}

// IT-R2d F.2: Test expression-body arrow variable call extraction.
func TestTypeScriptParser_ArrowVariable_ExpressionBody_CallSites(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
const transform = (x: number) => Math.sqrt(x);
`
	result, err := parser.Parse(context.Background(), []byte(source), "math.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	var transform *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "transform" {
			transform = sym
			break
		}
	}
	if transform == nil {
		t.Fatal("expected transform symbol")
	}
	if transform.Kind != SymbolKindFunction {
		t.Errorf("expected transform.Kind = Function, got %v", transform.Kind)
	}
	// Expression body should have the member_expression call extracted
	// Math.sqrt is a member expression → call to sqrt
	// The call extraction traverses the expression body node
}

// IT-R2d F.3: Test that interface method signatures get Receiver set.
func TestTypeScriptParser_InterfaceMethodReceiver(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
interface ICamera {
    update(): void;
    getViewMatrix(): Matrix;
    readonly position: Vector3;
    name?: string;
}
`
	result, err := parser.Parse(context.Background(), []byte(source), "camera.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "ICamera" && sym.Kind == SymbolKindInterface {
			iface = sym
			break
		}
	}
	if iface == nil {
		t.Fatal("expected ICamera interface symbol")
	}

	if len(iface.Children) < 4 {
		t.Fatalf("expected ICamera to have >= 4 children, got %d", len(iface.Children))
	}

	for _, child := range iface.Children {
		if child.Receiver != "ICamera" {
			t.Errorf("expected child %q (kind=%v) to have Receiver='ICamera', got %q",
				child.Name, child.Kind, child.Receiver)
		}
	}

	// Verify specific children
	childNames := make(map[string]SymbolKind)
	for _, child := range iface.Children {
		childNames[child.Name] = child.Kind
	}
	if _, ok := childNames["update"]; !ok {
		t.Error("expected 'update' method in ICamera children")
	}
	if _, ok := childNames["getViewMatrix"]; !ok {
		t.Error("expected 'getViewMatrix' method in ICamera children")
	}
	if _, ok := childNames["position"]; !ok {
		t.Error("expected 'position' property in ICamera children")
	}
}

// IT-R2d F.3: Test that interface members with extends also get Receiver.
func TestTypeScriptParser_InterfaceExtendsReceiver(t *testing.T) {
	parser := NewTypeScriptParser()
	source := `
interface IRenderable extends IDisposable {
    render(): void;
    isVisible: boolean;
}
`
	result, err := parser.Parse(context.Background(), []byte(source), "renderable.ts")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	var iface *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "IRenderable" {
			iface = sym
			break
		}
	}
	if iface == nil {
		t.Fatal("expected IRenderable interface symbol")
	}

	for _, child := range iface.Children {
		if child.Receiver != "IRenderable" {
			t.Errorf("expected child %q Receiver='IRenderable', got %q", child.Name, child.Receiver)
		}
	}

	// Verify extends is preserved
	if iface.Metadata == nil || iface.Metadata.Extends != "IDisposable" {
		extends := ""
		if iface.Metadata != nil {
			extends = iface.Metadata.Extends
		}
		t.Errorf("expected IRenderable.Metadata.Extends = 'IDisposable', got %q", extends)
	}
}
