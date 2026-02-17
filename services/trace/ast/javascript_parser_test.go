package ast

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJavaScriptParser_Parse_EmptyFile(t *testing.T) {
	parser := NewJavaScriptParser()
	result, err := parser.Parse(context.Background(), []byte(""), "empty.js")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Language != "javascript" {
		t.Errorf("expected language 'javascript', got %q", result.Language)
	}
	if result.FilePath != "empty.js" {
		t.Errorf("expected filePath 'empty.js', got %q", result.FilePath)
	}
	if result.Hash == "" {
		t.Error("expected hash to be set")
	}
}

func TestJavaScriptParser_Parse_FunctionDeclaration(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
function greet(name) {
    return "Hello, " + name;
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "greet.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the function symbol
	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "greet" && sym.Kind == SymbolKindFunction {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected to find function 'greet'")
	}
	if fn.Language != "javascript" {
		t.Errorf("expected language 'javascript', got %q", fn.Language)
	}
	if !strings.Contains(fn.Signature, "greet(name)") {
		t.Errorf("expected signature to contain 'greet(name)', got %q", fn.Signature)
	}
}

func TestJavaScriptParser_Parse_AsyncFunction(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
async function fetchData(url) {
    const response = await fetch(url);
    return response.json();
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "async.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "fetchData" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected to find function 'fetchData'")
	}
	if fn.Metadata == nil || !fn.Metadata.IsAsync {
		t.Error("expected function to be marked as async")
	}
	if !strings.Contains(fn.Signature, "async") {
		t.Errorf("expected signature to contain 'async', got %q", fn.Signature)
	}
}

func TestJavaScriptParser_Parse_GeneratorFunction(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
function* generateIds() {
    let id = 0;
    while (true) yield id++;
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "generator.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "generateIds" {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected to find function 'generateIds'")
	}
	if fn.Metadata == nil || !fn.Metadata.IsGenerator {
		t.Error("expected function to be marked as generator")
	}
}

func TestJavaScriptParser_Parse_ClassDeclaration(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
class UserService {
    constructor(db) {
        this.db = db;
    }

    getUser(id) {
        return this.db.find(id);
    }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "service.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "UserService" && sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected to find class 'UserService'")
	}
	if len(class.Children) < 2 {
		t.Errorf("expected at least 2 children (constructor, getUser), got %d", len(class.Children))
	}

	// Check for constructor
	var constructor *Symbol
	for _, child := range class.Children {
		if child.Name == "constructor" {
			constructor = child
			break
		}
	}
	if constructor == nil {
		t.Error("expected to find constructor method")
	}
}

func TestJavaScriptParser_Parse_ClassExtends(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
class EventEmitter {}

class MyEmitter extends EventEmitter {
    emit(event) {
        console.log(event);
    }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "emitter.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "MyEmitter" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected to find class 'MyEmitter'")
	}
	if class.Metadata == nil || class.Metadata.Extends != "EventEmitter" {
		t.Errorf("expected extends 'EventEmitter', got %v", class.Metadata)
	}
	if !strings.Contains(class.Signature, "extends EventEmitter") {
		t.Errorf("expected signature to contain 'extends EventEmitter', got %q", class.Signature)
	}
}

func TestJavaScriptParser_Parse_PrivateField(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
class Counter {
    #count = 0;
    publicValue = 1;

    increment() {
        this.#count++;
    }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "counter.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Counter" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected to find class 'Counter'")
	}

	var privateField, publicField *Symbol
	for _, child := range class.Children {
		if child.Name == "#count" {
			privateField = child
		}
		if child.Name == "publicValue" {
			publicField = child
		}
	}

	if privateField == nil {
		t.Error("expected to find private field '#count'")
	} else {
		if privateField.Exported {
			t.Error("expected private field to not be exported")
		}
		if privateField.Metadata == nil || privateField.Metadata.AccessModifier != "private" {
			t.Error("expected private field to have 'private' access modifier")
		}
	}

	if publicField == nil {
		t.Error("expected to find public field 'publicValue'")
	} else if !publicField.Exported {
		t.Error("expected public field to be exported")
	}
}

func TestJavaScriptParser_Parse_StaticMethod(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
class Factory {
    static create() {
        return new Factory();
    }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "factory.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Factory" {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected to find class 'Factory'")
	}

	var staticMethod *Symbol
	for _, child := range class.Children {
		if child.Name == "create" {
			staticMethod = child
			break
		}
	}

	if staticMethod == nil {
		t.Fatal("expected to find static method 'create'")
	}
	if staticMethod.Metadata == nil || !staticMethod.Metadata.IsStatic {
		t.Error("expected method to be marked as static")
	}
	if !strings.Contains(staticMethod.Signature, "static") {
		t.Errorf("expected signature to contain 'static', got %q", staticMethod.Signature)
	}
}

func TestJavaScriptParser_Parse_ArrowFunction(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
const greet = (name) => {
    return "Hello, " + name;
};

const double = x => x * 2;

const asyncFetch = async (url) => {
    return fetch(url);
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "arrows.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find arrow functions
	var greet, double, asyncFetch *Symbol
	for _, sym := range result.Symbols {
		switch sym.Name {
		case "greet":
			greet = sym
		case "double":
			double = sym
		case "asyncFetch":
			asyncFetch = sym
		}
	}

	if greet == nil {
		t.Error("expected to find 'greet' arrow function")
	} else if greet.Kind != SymbolKindFunction {
		t.Errorf("expected greet to be SymbolKindFunction, got %v", greet.Kind)
	}

	if double == nil {
		t.Error("expected to find 'double' arrow function")
	}

	if asyncFetch == nil {
		t.Error("expected to find 'asyncFetch' arrow function")
	} else if asyncFetch.Metadata == nil || !asyncFetch.Metadata.IsAsync {
		t.Error("expected asyncFetch to be marked as async")
	}
}

func TestJavaScriptParser_Parse_NamedImport(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `import { useState, useEffect } from 'react';`

	result, err := parser.Parse(context.Background(), []byte(content), "app.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) == 0 {
		t.Fatal("expected at least one import")
	}

	imp := result.Imports[0]
	if imp.Path != "react" {
		t.Errorf("expected path 'react', got %q", imp.Path)
	}
	if len(imp.Names) != 2 {
		t.Errorf("expected 2 named imports, got %d", len(imp.Names))
	}
	if !imp.IsModule {
		t.Error("expected IsModule to be true")
	}
}

func TestJavaScriptParser_Parse_DefaultImport(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `import React from 'react';`

	result, err := parser.Parse(context.Background(), []byte(content), "app.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) == 0 {
		t.Fatal("expected at least one import")
	}

	imp := result.Imports[0]
	if imp.Path != "react" {
		t.Errorf("expected path 'react', got %q", imp.Path)
	}
	if imp.Alias != "React" {
		t.Errorf("expected alias 'React', got %q", imp.Alias)
	}
	if !imp.IsDefault {
		t.Error("expected IsDefault to be true")
	}
}

func TestJavaScriptParser_Parse_NamespaceImport(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `import * as utils from './utils.js';`

	result, err := parser.Parse(context.Background(), []byte(content), "app.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) == 0 {
		t.Fatal("expected at least one import")
	}

	imp := result.Imports[0]
	if imp.Path != "./utils.js" {
		t.Errorf("expected path './utils.js', got %q", imp.Path)
	}
	if imp.Alias != "utils" {
		t.Errorf("expected alias 'utils', got %q", imp.Alias)
	}
	if !imp.IsNamespace {
		t.Error("expected IsNamespace to be true")
	}
}

func TestJavaScriptParser_Parse_CommonJSRequire(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `const fs = require('fs');`

	result, err := parser.Parse(context.Background(), []byte(content), "app.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Imports) == 0 {
		t.Fatal("expected at least one import")
	}

	imp := result.Imports[0]
	if imp.Path != "fs" {
		t.Errorf("expected path 'fs', got %q", imp.Path)
	}
	if imp.Alias != "fs" {
		t.Errorf("expected alias 'fs', got %q", imp.Alias)
	}
	if !imp.IsCommonJS {
		t.Error("expected IsCommonJS to be true")
	}
}

func TestJavaScriptParser_Parse_ExportedFunction(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
export function greet(name) {
    return "Hello, " + name;
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "greet.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "greet" && sym.Kind == SymbolKindFunction {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected to find function 'greet'")
	}
	if !fn.Exported {
		t.Error("expected function to be exported")
	}
}

func TestJavaScriptParser_Parse_ExportedClass(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
export class UserService {
    getUser(id) {
        return null;
    }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "service.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var class *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "UserService" && sym.Kind == SymbolKindClass {
			class = sym
			break
		}
	}

	if class == nil {
		t.Fatal("expected to find class 'UserService'")
	}
	if !class.Exported {
		t.Error("expected class to be exported")
	}
}

func TestJavaScriptParser_Parse_ExportDefault(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
class UserService {}
export default UserService;
`
	result, err := parser.Parse(context.Background(), []byte(content), "service.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have both the class and the default export
	exportedCount := 0
	for _, sym := range result.Symbols {
		if sym.Name == "UserService" && sym.Exported {
			exportedCount++
		}
	}

	if exportedCount == 0 {
		t.Error("expected at least one exported UserService symbol")
	}
}

func TestJavaScriptParser_Parse_ExportConst(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `export const DEFAULT_TIMEOUT = 5000;`

	result, err := parser.Parse(context.Background(), []byte(content), "config.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var constant *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "DEFAULT_TIMEOUT" {
			constant = sym
			break
		}
	}

	if constant == nil {
		t.Fatal("expected to find constant 'DEFAULT_TIMEOUT'")
	}
	if !constant.Exported {
		t.Error("expected constant to be exported")
	}
	if constant.Kind != SymbolKindConstant {
		t.Errorf("expected kind Constant, got %v", constant.Kind)
	}
}

func TestJavaScriptParser_Parse_JSDoc(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
/**
 * Greet a user by name.
 * @param {string} name - The user's name
 * @returns {string} The greeting
 */
export function greet(name) {
    return "Hello, " + name;
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "greet.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var fn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "greet" && sym.Kind == SymbolKindFunction {
			fn = sym
			break
		}
	}

	if fn == nil {
		t.Fatal("expected to find function 'greet'")
	}
	if fn.DocComment == "" {
		t.Error("expected DocComment to be populated")
	}
	if !strings.Contains(fn.DocComment, "@param") {
		t.Errorf("expected DocComment to contain @param, got %q", fn.DocComment)
	}
}

func TestJavaScriptParser_Parse_FileTooLarge(t *testing.T) {
	parser := NewJavaScriptParser(WithJSMaxFileSize(100))
	content := make([]byte, 200)
	for i := range content {
		content[i] = ' '
	}

	_, err := parser.Parse(context.Background(), content, "large.js")
	if err != ErrFileTooLarge {
		t.Errorf("expected ErrFileTooLarge, got %v", err)
	}
}

func TestJavaScriptParser_Parse_InvalidUTF8(t *testing.T) {
	parser := NewJavaScriptParser()
	// Invalid UTF-8 byte sequence
	content := []byte{0xff, 0xfe, 0x00, 0x01}

	_, err := parser.Parse(context.Background(), content, "invalid.js")
	if err != ErrInvalidContent {
		t.Errorf("expected ErrInvalidContent, got %v", err)
	}
}

func TestJavaScriptParser_Parse_ContextCancellation(t *testing.T) {
	parser := NewJavaScriptParser()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := parser.Parse(ctx, []byte("function test() {}"), "test.js")
	if err == nil {
		t.Error("expected error due to cancelled context")
	}
}

func TestJavaScriptParser_Parse_Hash(t *testing.T) {
	parser := NewJavaScriptParser()
	content := []byte("const x = 1;")

	result1, _ := parser.Parse(context.Background(), content, "test.js")
	result2, _ := parser.Parse(context.Background(), content, "test.js")

	if result1.Hash == "" {
		t.Error("expected hash to be set")
	}
	if result1.Hash != result2.Hash {
		t.Error("expected same content to produce same hash")
	}

	// Different content should produce different hash
	result3, _ := parser.Parse(context.Background(), []byte("const y = 2;"), "test.js")
	if result1.Hash == result3.Hash {
		t.Error("expected different content to produce different hash")
	}
}

func TestJavaScriptParser_Parse_Concurrent(t *testing.T) {
	parser := NewJavaScriptParser()
	contents := []string{
		"function a() {}",
		"function b() {}",
		"function c() {}",
		"class X {}",
		"class Y {}",
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(contents))

	for i, content := range contents {
		wg.Add(1)
		go func(idx int, c string) {
			defer wg.Done()
			_, err := parser.Parse(context.Background(), []byte(c), "test.js")
			if err != nil {
				errors <- err
			}
		}(i, content)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent parse error: %v", err)
	}
}

func TestJavaScriptParser_Parse_Timeout(t *testing.T) {
	parser := NewJavaScriptParser()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Small content that should parse quickly
	content := []byte("const x = 1;")

	// This might or might not timeout depending on timing
	// We're mainly testing that the timeout mechanism doesn't panic
	_, _ = parser.Parse(ctx, content, "test.js")
}

func TestJavaScriptParser_Language(t *testing.T) {
	parser := NewJavaScriptParser()
	if parser.Language() != "javascript" {
		t.Errorf("expected 'javascript', got %q", parser.Language())
	}
}

func TestJavaScriptParser_Extensions(t *testing.T) {
	parser := NewJavaScriptParser()
	extensions := parser.Extensions()

	expected := map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".jsx": true}
	for _, ext := range extensions {
		if !expected[ext] {
			t.Errorf("unexpected extension: %q", ext)
		}
		delete(expected, ext)
	}
	for ext := range expected {
		t.Errorf("missing extension: %q", ext)
	}
}

func TestJavaScriptParser_Parse_ComprehensiveExample(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
/**
 * User service for managing users.
 * @module UserService
 */
import { EventEmitter } from 'events';
import config from './config.js';
const legacy = require('./legacy');

/**
 * User service class.
 * @class
 */
export class UserService extends EventEmitter {
    #privateCache = new Map();
    publicCount = 0;

    constructor(db) {
        super();
        this.db = db;
    }

    /**
     * Get user by ID.
     * @param {number} id - User ID
     * @returns {Promise<User>}
     */
    async getUser(id) {
        return this.db.findById(id);
    }

    static createInstance(db) {
        return new UserService(db);
    }

    *generateIds() {
        let id = 0;
        while (true) yield id++;
    }
}

export const DEFAULT_TIMEOUT = 5000;
const internalHelper = () => {};
export default UserService;
`
	result, err := parser.Parse(context.Background(), []byte(content), "user-service.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check imports
	if len(result.Imports) < 3 {
		t.Errorf("expected at least 3 imports, got %d", len(result.Imports))
	}

	// Check for UserService class
	var userService *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "UserService" && sym.Kind == SymbolKindClass {
			userService = sym
			break
		}
	}

	if userService == nil {
		t.Fatal("expected to find class 'UserService'")
	}
	if !userService.Exported {
		t.Error("expected UserService to be exported")
	}
	if userService.Metadata == nil || userService.Metadata.Extends != "EventEmitter" {
		t.Error("expected UserService to extend EventEmitter")
	}

	// Check class has expected children
	if len(userService.Children) < 5 {
		t.Errorf("expected at least 5 children, got %d", len(userService.Children))
	}

	// Find specific members
	memberNames := make(map[string]bool)
	for _, child := range userService.Children {
		memberNames[child.Name] = true
	}

	expectedMembers := []string{"#privateCache", "publicCount", "constructor", "getUser", "createInstance", "generateIds"}
	for _, name := range expectedMembers {
		if !memberNames[name] {
			t.Errorf("expected to find member %q", name)
		}
	}

	// Check async method
	for _, child := range userService.Children {
		if child.Name == "getUser" {
			if child.Metadata == nil || !child.Metadata.IsAsync {
				t.Error("expected getUser to be async")
			}
			if child.DocComment == "" {
				t.Error("expected getUser to have JSDoc comment")
			}
		}
		if child.Name == "generateIds" {
			if child.Metadata == nil || !child.Metadata.IsGenerator {
				t.Error("expected generateIds to be a generator")
			}
		}
		if child.Name == "createInstance" {
			if child.Metadata == nil || !child.Metadata.IsStatic {
				t.Error("expected createInstance to be static")
			}
		}
	}

	// Check DEFAULT_TIMEOUT
	var timeout *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "DEFAULT_TIMEOUT" {
			timeout = sym
			break
		}
	}
	if timeout == nil {
		t.Error("expected to find constant 'DEFAULT_TIMEOUT'")
	} else if !timeout.Exported {
		t.Error("expected DEFAULT_TIMEOUT to be exported")
	}

	// Check internalHelper is not exported
	for _, sym := range result.Symbols {
		if sym.Name == "internalHelper" {
			if sym.Exported {
				t.Error("expected internalHelper to not be exported")
			}
		}
	}
}

// GR-41: Tests for JavaScript call site extraction

const jsCallsSource = `
class Router {
    handle(req, res) {
        const route = this.matchRoute(req.path);
        const result = route.handler(req, res);
        this.sendResponse(res, result);
    }

    matchRoute(path) {
        return this.routes.find(r => r.matches(path));
    }
}

function processRequest(req) {
    const data = parseBody(req);
    const validated = validate(data);
    return formatResponse(validated);
}

class EventEmitter {
    emit(event, data) {
        const listeners = this.getListeners(event);
        listeners.forEach(listener => listener(data));
        logger.debug("emitted", event);
    }
}
`

func TestJavaScriptParser_ExtractCallSites_ThisMethodCalls(t *testing.T) {
	parser := NewJavaScriptParser()
	result, err := parser.Parse(context.Background(), []byte(jsCallsSource), "router.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find Router.handle method
	var handleMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "Router" {
			for _, child := range sym.Children {
				if child.Name == "handle" {
					handleMethod = child
					break
				}
			}
		}
	}

	if handleMethod == nil {
		t.Fatal("Router.handle method not found")
	}

	if len(handleMethod.Calls) == 0 {
		t.Fatal("Router.handle should have call sites extracted")
	}

	callTargets := make(map[string]bool)
	for _, call := range handleMethod.Calls {
		callTargets[call.Target] = true
	}

	if !callTargets["matchRoute"] {
		t.Error("expected call to matchRoute")
	}
	if !callTargets["sendResponse"] {
		t.Error("expected call to sendResponse")
	}

	// Verify this.method() calls have IsMethod=true and Receiver="this"
	for _, call := range handleMethod.Calls {
		if call.Target == "matchRoute" || call.Target == "sendResponse" {
			if !call.IsMethod {
				t.Errorf("call to %s should be IsMethod=true", call.Target)
			}
			if call.Receiver != "this" {
				t.Errorf("call to %s should have Receiver='this', got %q", call.Target, call.Receiver)
			}
		}
	}
}

func TestJavaScriptParser_ExtractCallSites_SimpleFunctionCalls(t *testing.T) {
	parser := NewJavaScriptParser()
	result, err := parser.Parse(context.Background(), []byte(jsCallsSource), "router.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var processFn *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "processRequest" {
			processFn = sym
			break
		}
	}

	if processFn == nil {
		t.Fatal("processRequest function not found")
	}

	if len(processFn.Calls) < 3 {
		t.Errorf("expected at least 3 calls, got %d", len(processFn.Calls))
	}

	callTargets := make(map[string]bool)
	for _, call := range processFn.Calls {
		callTargets[call.Target] = true
	}

	for _, expected := range []string{"parseBody", "validate", "formatResponse"} {
		if !callTargets[expected] {
			t.Errorf("expected call to %s", expected)
		}
	}

	// Simple function calls should NOT be method calls
	for _, call := range processFn.Calls {
		if call.IsMethod {
			t.Errorf("call to %s should not be IsMethod", call.Target)
		}
	}
}

func TestJavaScriptParser_ExtractCallSites_MixedCalls(t *testing.T) {
	parser := NewJavaScriptParser()
	result, err := parser.Parse(context.Background(), []byte(jsCallsSource), "router.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find EventEmitter.emit — has this.method(), obj.method(), and simple calls
	var emitMethod *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "EventEmitter" {
			for _, child := range sym.Children {
				if child.Name == "emit" {
					emitMethod = child
					break
				}
			}
		}
	}

	if emitMethod == nil {
		t.Fatal("EventEmitter.emit method not found")
	}

	callMap := make(map[string]*CallSite)
	for i := range emitMethod.Calls {
		callMap[emitMethod.Calls[i].Target] = &emitMethod.Calls[i]
	}

	if call, ok := callMap["getListeners"]; ok {
		if !call.IsMethod || call.Receiver != "this" {
			t.Errorf("getListeners should be this.getListeners, got IsMethod=%v, Receiver=%q", call.IsMethod, call.Receiver)
		}
	} else {
		t.Error("expected call to getListeners")
	}

	if call, ok := callMap["debug"]; ok {
		if !call.IsMethod || call.Receiver != "logger" {
			t.Errorf("debug should be logger.debug, got IsMethod=%v, Receiver=%q", call.IsMethod, call.Receiver)
		}
	} else {
		t.Error("expected call to debug")
	}
}

// =============================================================================
// IT-01 Phase C: Prototype Method Extraction Tests
// =============================================================================

func TestJavaScriptParser_DeriveSemanticTypeName(t *testing.T) {
	tests := []struct {
		filePath string
		expected string
	}{
		{"lib/router/index.js", "Router"},
		{"lib/application.js", "Application"},
		{"lib/request.js", "Request"},
		{"lib/response.js", "Response"},
		{"src/utils/helper.js", "Helper"},
		{"index.js", ""}, // root index.js with "." parent → empty
		{"lib/middleware/cors.js", "Cors"},
		{"server.mjs", "Server"},
	}

	for _, tt := range tests {
		t.Run(tt.filePath, func(t *testing.T) {
			result := deriveSemanticTypeName(tt.filePath)
			if result != tt.expected {
				t.Errorf("deriveSemanticTypeName(%q) = %q, want %q", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestJavaScriptParser_PrototypeMethodAssignment_Express(t *testing.T) {
	// Express router pattern: var proto = module.exports = function() {}; proto.handle = function handle() {}
	parser := NewJavaScriptParser()
	content := `
var proto = module.exports = function(options) {
  return function router(req, res, next) {
    router.handle(req, res, next);
  };
};

proto.handle = function handle(req, res, out) {
  var self = this;
  var done = out;
};

proto.route = function route(path) {
  var route = new Route(path);
  return route;
};

proto.use = function use(fn) {
  var offset = 0;
  return this;
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/router/index.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Build a map of method names to symbols
	methodMap := make(map[string]*Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod {
			methodMap[sym.Name] = sym
		}
	}

	// Should find handle, route, use as methods with Receiver = "Router"
	expectedMethods := []string{"handle", "route", "use"}
	for _, name := range expectedMethods {
		sym, ok := methodMap[name]
		if !ok {
			t.Errorf("expected method %q to be extracted", name)
			continue
		}
		if sym.Receiver != "Router" {
			t.Errorf("method %q: expected Receiver=%q, got %q", name, "Router", sym.Receiver)
		}
		if sym.Kind != SymbolKindMethod {
			t.Errorf("method %q: expected Kind=Method, got %v", name, sym.Kind)
		}
		if !sym.Exported {
			t.Errorf("method %q: expected Exported=true", name)
		}
	}

	// handle should have call sites extracted from its body
	if handleSym, ok := methodMap["handle"]; ok {
		if len(handleSym.Calls) == 0 {
			t.Log("Note: handle has 0 calls — body may be too simple for call extraction")
		}
	}
}

func TestJavaScriptParser_PrototypeMethodAssignment_ApplicationPattern(t *testing.T) {
	// Express application.js pattern: var app = exports = module.exports = {}; app.init = function init() {}
	parser := NewJavaScriptParser()
	content := `
var app = exports = module.exports = {};

app.init = function init() {
  this.cache = {};
  this.engines = {};
};

app.handle = function handle(req, res, callback) {
  var router = this._router;
  done(req, res);
};

app.use = function use(fn) {
  var offset = 0;
  var path = '/';
  return this;
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/application.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	methodMap := make(map[string]*Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod {
			methodMap[sym.Name] = sym
		}
	}

	expectedMethods := []string{"init", "handle", "use"}
	for _, name := range expectedMethods {
		sym, ok := methodMap[name]
		if !ok {
			t.Errorf("expected method %q to be extracted", name)
			continue
		}
		if sym.Receiver != "Application" {
			t.Errorf("method %q: expected Receiver=%q, got %q", name, "Application", sym.Receiver)
		}
	}
}

func TestJavaScriptParser_PrototypeMethodAssignment_RequestPattern(t *testing.T) {
	// Express request.js pattern: var req = Object.create(...); module.exports = req; req.get = function() {}
	parser := NewJavaScriptParser()
	content := `
var req = Object.create(http.IncomingMessage.prototype);

module.exports = req;

req.get = function header(name) {
  return this.headers[name.toLowerCase()];
};

req.accepts = function() {
  var accept = accepts(this);
  return accept.types.apply(accept, arguments);
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/request.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	methodMap := make(map[string]*Symbol)
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod {
			methodMap[sym.Name] = sym
		}
	}

	// "get" method: property name "get" takes priority over function name "header"
	// The public API is req.get(), so the method should be named "get"
	for _, name := range []string{"get", "accepts"} {
		sym, ok := methodMap[name]
		if !ok {
			t.Errorf("expected method %q to be extracted", name)
			continue
		}
		if sym.Receiver != "Request" {
			t.Errorf("method %q: expected Receiver=%q, got %q", name, "Request", sym.Receiver)
		}
	}
}

func TestJavaScriptParser_PrototypeMethod_NonAlias_Ignored(t *testing.T) {
	// If a variable is NOT a module export alias, its assignments should not create methods
	parser := NewJavaScriptParser()
	content := `
var helper = {};

helper.doWork = function doWork() {
  return 42;
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/utils.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod && sym.Name == "doWork" {
			t.Error("did not expect doWork to be extracted as a method (helper is not a module export)")
		}
	}
}

func TestJavaScriptParser_PrototypeMethod_WithCalls(t *testing.T) {
	// Verify that call sites are extracted from prototype method bodies
	parser := NewJavaScriptParser()
	content := `
var proto = module.exports = function() {};

proto.handle = function handle(req, res, out) {
  this.process(req, res);
  done(out);
  logger.debug("handled");
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/router/index.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var handleSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod && sym.Name == "handle" {
			handleSym = sym
			break
		}
	}

	if handleSym == nil {
		t.Fatal("expected handle method to be extracted")
	}

	if handleSym.Receiver != "Router" {
		t.Errorf("expected Receiver=%q, got %q", "Router", handleSym.Receiver)
	}

	// Should have calls extracted from the body
	if len(handleSym.Calls) == 0 {
		t.Error("expected call sites to be extracted from handle body")
	}

	callMap := make(map[string]CallSite)
	for _, call := range handleSym.Calls {
		callMap[call.Target] = call
	}

	// Check for this.process(), done(), logger.debug()
	if _, ok := callMap["process"]; !ok {
		t.Error("expected call to process (this.process)")
	}
	if _, ok := callMap["done"]; !ok {
		t.Error("expected call to done")
	}
	if _, ok := callMap["debug"]; !ok {
		t.Error("expected call to debug (logger.debug)")
	}
}

func TestJavaScriptParser_PrototypeMethod_IndexFile_UsesDirectory(t *testing.T) {
	// For index.js, semantic type should come from parent directory
	parser := NewJavaScriptParser()
	content := `
var proto = module.exports = function() {};
proto.render = function render() {};
`
	result, err := parser.Parse(context.Background(), []byte(content), "components/widget/index.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindMethod && sym.Name == "render" {
			if sym.Receiver != "Widget" {
				t.Errorf("expected Receiver=%q (from directory), got %q", "Widget", sym.Receiver)
			}
			return
		}
	}
	t.Error("expected render method to be extracted")
}

func TestJavaScriptParser_PrototypeMethod_ClassSyntaxUnaffected(t *testing.T) {
	// ES6 class syntax should still work as before (methods are in Children, not top-level)
	parser := NewJavaScriptParser()
	content := `
class Router {
  constructor(options) {
    this.options = options;
  }

  handle(req, res) {
    return this.dispatch(req, res);
  }
}
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/router.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the Router class
	var routerClass *Symbol
	for _, sym := range result.Symbols {
		if sym.Kind == SymbolKindClass && sym.Name == "Router" {
			routerClass = sym
			break
		}
	}

	if routerClass == nil {
		t.Fatal("expected Router class")
	}

	// Methods are stored as children of the class
	var handleSym *Symbol
	for _, child := range routerClass.Children {
		if child.Kind == SymbolKindMethod && child.Name == "handle" {
			handleSym = child
			break
		}
	}

	if handleSym == nil {
		t.Fatal("expected handle method in class children")
	}

	if handleSym.Receiver != "Router" {
		t.Errorf("expected Receiver=%q, got %q", "Router", handleSym.Receiver)
	}
}

func TestJavaScriptParser_Parse_PrototypeDotMethodAssignment(t *testing.T) {
	parser := NewJavaScriptParser()
	content := `
module.exports = Route;

function Route(path) {
    this.path = path;
    this.methods = {};
}

Route.prototype.dispatch = function dispatch(req, res, done) {
    var method = req.method.toLowerCase();
    done();
};

Route.prototype._handles_method = function _handles_method(method) {
    return Boolean(this.methods[method.toLowerCase()]);
};
`
	result, err := parser.Parse(context.Background(), []byte(content), "lib/route.js")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the dispatch method — it should be associated with Route
	var dispatchSym *Symbol
	var handlesMethodSym *Symbol
	for _, sym := range result.Symbols {
		if sym.Name == "dispatch" && sym.Kind == SymbolKindMethod {
			dispatchSym = sym
		}
		if sym.Name == "_handles_method" && sym.Kind == SymbolKindMethod {
			handlesMethodSym = sym
		}
	}

	if dispatchSym == nil {
		// Also check class children
		for _, sym := range result.Symbols {
			if sym.Kind == SymbolKindClass && sym.Name == "Route" {
				for _, child := range sym.Children {
					if child.Name == "dispatch" {
						dispatchSym = child
					}
					if child.Name == "_handles_method" {
						handlesMethodSym = child
					}
				}
			}
		}
	}

	if dispatchSym == nil {
		t.Fatal("expected dispatch method symbol from Route.prototype.dispatch pattern")
	}
	if dispatchSym.Receiver != "Route" {
		t.Errorf("expected Receiver=%q, got %q", "Route", dispatchSym.Receiver)
	}

	if handlesMethodSym == nil {
		t.Fatal("expected _handles_method symbol from Route.prototype._handles_method pattern")
	}
	if handlesMethodSym.Receiver != "Route" {
		t.Errorf("expected Receiver=%q, got %q", "Route", handlesMethodSym.Receiver)
	}
}
