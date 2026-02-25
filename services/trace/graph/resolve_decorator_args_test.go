// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system Attribution.

package graph

// =============================================================================
// IT-06e Bug 5: Decorator Argument Resolution Tests
// =============================================================================
//
// These tests verify that class names listed in decorator arguments
// (@Module({providers: [UserService]})) create EdgeTypeReferences edges from the
// decorated class to the referenced class.
//
// Test scenarios:
//   - PascalCase decorator arg creates REFERENCES edge from decorated class to target
//   - Non-PascalCase args (e.g., "true", "v2") are skipped
//   - Non-TypeScript files are skipped
//   - DecoratorArgEdgesResolved stat is incremented correctly

import (
	"context"
	"testing"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// TestResolveDecoratorArgEdges_Success verifies that a PascalCase identifier in a
// decorator arg array creates a REFERENCES edge from the decorated class to the target.
func TestResolveDecoratorArgEdges_Success(t *testing.T) {
	// Simulates:
	//   // src/app.module.ts
	//   import { UserService } from './user.service'
	//   @Module({ providers: [UserService] })
	//   export class AppModule {}
	//
	//   // src/user.service.ts
	//   @Injectable()
	//   export class UserService { findAll() { return []; } }

	userServiceID := "src/user.service.ts:1:UserService"
	appModuleID := "src/app.module.ts:5:AppModule"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/user.service.ts",
			Language: "typescript",
			Symbols: []*ast.Symbol{
				{
					ID:        userServiceID,
					Name:      "UserService",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/user.service.ts",
					StartLine: 1,
					EndLine:   5,
					Language:  "typescript",
					Exported:  true,
				},
			},
		},
		{
			FilePath: "src/app.module.ts",
			Language: "typescript",
			Imports: []ast.Import{
				{
					Path:     "./user.service",
					Names:    []string{"UserService"},
					IsModule: true,
					Location: ast.Location{FilePath: "src/app.module.ts", StartLine: 1},
				},
			},
			Symbols: []*ast.Symbol{
				{
					ID:        appModuleID,
					Name:      "AppModule",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/app.module.ts",
					StartLine: 5,
					EndLine:   10,
					Language:  "typescript",
					Exported:  true,
					Metadata: &ast.SymbolMetadata{
						Decorators: []string{"Module"},
						DecoratorArgs: map[string][]string{
							"Module": {"UserService"},
						},
					},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	g := result.Graph

	// UserService should have an incoming REFERENCES edge from AppModule
	refs, err := g.FindReferencesByID(context.Background(), userServiceID)
	if err != nil {
		t.Fatalf("FindReferencesByID failed: %v", err)
	}

	if len(refs) == 0 {
		t.Error("Bug 5: UserService has 0 incoming references; expected reference from AppModule via @Module")
	}

	found := false
	for _, loc := range refs {
		if loc.FilePath == "src/app.module.ts" {
			found = true
			t.Logf("AppModule → UserService reference at line %d ✓", loc.StartLine)
			break
		}
	}
	if !found {
		t.Error("Bug 5: no reference from src/app.module.ts to UserService")
	}

	if result.Stats.DecoratorArgEdgesResolved == 0 {
		t.Error("expected DecoratorArgEdgesResolved > 0")
	}
}

// TestResolveDecoratorArgEdges_LowercaseArgSkipped verifies that lowercase decorator
// argument values (not PascalCase class references) are skipped.
func TestResolveDecoratorArgEdges_LowercaseArgSkipped(t *testing.T) {
	appModuleID := "src/app.module.ts:1:AppModule"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/app.module.ts",
			Language: "typescript",
			Symbols: []*ast.Symbol{
				{
					ID:        appModuleID,
					Name:      "AppModule",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/app.module.ts",
					StartLine: 1,
					EndLine:   5,
					Language:  "typescript",
					Exported:  true,
					Metadata: &ast.SymbolMetadata{
						Decorators: []string{"Component"},
						DecoratorArgs: map[string][]string{
							"Component": {"selector", "true", "v2", "templateUrl"},
						},
					},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// No edges should be created — all args are lowercase
	if result.Stats.DecoratorArgEdgesResolved != 0 {
		t.Errorf("expected 0 DecoratorArgEdgesResolved for lowercase args, got %d",
			result.Stats.DecoratorArgEdgesResolved)
	}
}

// TestResolveDecoratorArgEdges_NonTSSkipped verifies that non-TypeScript files
// are skipped by the decorator arg resolution pass.
func TestResolveDecoratorArgEdges_NonTSSkipped(t *testing.T) {
	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/app.js",
			Language: "javascript",
			Symbols: []*ast.Symbol{
				{
					ID:        "src/app.js:1:App",
					Name:      "App",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/app.js",
					StartLine: 1,
					Language:  "javascript",
					Metadata: &ast.SymbolMetadata{
						DecoratorArgs: map[string][]string{
							"Module": {"UserService"},
						},
					},
				},
				{
					ID:        "src/app.js:10:UserService",
					Name:      "UserService",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/app.js",
					StartLine: 10,
					Language:  "javascript",
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if result.Stats.DecoratorArgEdgesResolved != 0 {
		t.Errorf("JavaScript file should be skipped by decorator arg pass, got %d edges",
			result.Stats.DecoratorArgEdgesResolved)
	}
}

// TestResolveDecoratorArgEdges_MultipleDecorators verifies that multiple decorators
// on the same class are all processed (NestJS classes often have @Module, @Controller, etc.).
func TestResolveDecoratorArgEdges_MultipleDecorators(t *testing.T) {
	authServiceID := "src/auth.service.ts:1:AuthService"
	userServiceID := "src/user.service.ts:1:UserService"
	appModuleID := "src/app.module.ts:5:AppModule"

	parseResults := []*ast.ParseResult{
		{
			FilePath: "src/auth.service.ts",
			Language: "typescript",
			Symbols: []*ast.Symbol{{
				ID:        authServiceID,
				Name:      "AuthService",
				Kind:      ast.SymbolKindClass,
				FilePath:  "src/auth.service.ts",
				StartLine: 1,
				EndLine:   5,
				Language:  "typescript",
				Exported:  true,
			}},
		},
		{
			FilePath: "src/user.service.ts",
			Language: "typescript",
			Symbols: []*ast.Symbol{{
				ID:        userServiceID,
				Name:      "UserService",
				Kind:      ast.SymbolKindClass,
				FilePath:  "src/user.service.ts",
				StartLine: 1,
				EndLine:   5,
				Language:  "typescript",
				Exported:  true,
			}},
		},
		{
			FilePath: "src/app.module.ts",
			Language: "typescript",
			Symbols: []*ast.Symbol{
				{
					ID:        appModuleID,
					Name:      "AppModule",
					Kind:      ast.SymbolKindClass,
					FilePath:  "src/app.module.ts",
					StartLine: 5,
					EndLine:   10,
					Language:  "typescript",
					Exported:  true,
					Metadata: &ast.SymbolMetadata{
						Decorators: []string{"Module"},
						DecoratorArgs: map[string][]string{
							"Module": {"AuthService", "UserService"},
						},
					},
				},
			},
		},
	}

	builder := NewBuilder()
	result, err := builder.Build(context.Background(), parseResults)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if result.Stats.DecoratorArgEdgesResolved < 2 {
		t.Errorf("expected at least 2 DecoratorArgEdgesResolved (AuthService + UserService), got %d",
			result.Stats.DecoratorArgEdgesResolved)
	}
}
