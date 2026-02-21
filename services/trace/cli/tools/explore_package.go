// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
	"github.com/AleutianAI/AleutianFOSS/services/trace/index"
)

// ============================================================================
// explore_package Tool
// ============================================================================

// explorePackageTool provides package-level exploration.
type explorePackageTool struct {
	graph *graph.Graph
	index *index.SymbolIndex
}

// NewExplorePackageTool creates the explore_package tool.
//
// Description:
//
//	Creates a tool for exploring a specific package, showing its files,
//	public API, dependencies, and inferred purpose. This is the mid-level
//	navigation tool for understanding package responsibilities.
//
// Thread Safety: Safe for concurrent use.
func NewExplorePackageTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &explorePackageTool{
		graph: g,
		index: idx,
	}
}

func (t *explorePackageTool) Name() string {
	return "explore_package"
}

func (t *explorePackageTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *explorePackageTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "explore_package",
		Description: "Explores a specific package showing its files, public API, dependencies, and purpose. Use when asked 'what does pkg/X do?' or 'tell me about the api package'.",
		Parameters: map[string]ParamDef{
			"package": {
				Type:        ParamTypeString,
				Description: "Package name or path to explore (e.g., 'api', 'pkg/handlers', 'main')",
				Required:    true,
			},
			"include_private": {
				Type:        ParamTypeBool,
				Description: "Include unexported (private) symbols",
				Required:    false,
				Default:     false,
			},
			"include_dependencies": {
				Type:        ParamTypeBool,
				Description: "Show packages this package imports",
				Required:    false,
				Default:     true,
			},
			"include_dependents": {
				Type:        ParamTypeBool,
				Description: "Show packages that import this package",
				Required:    false,
				Default:     true,
			},
			"max_symbols": {
				Type:        ParamTypeInt,
				Description: "Maximum symbols to return per category (default: 20)",
				Required:    false,
				Default:     20,
			},
		},
		Category:    CategoryExploration,
		Priority:    95, // High priority for package questions
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     5 * time.Second,
	}
}

// ExplorePackageResult is the output of the explore_package tool.
type ExplorePackageResult struct {
	Name         string         `json:"name"`
	Path         string         `json:"path"`
	Files        []FileOverview `json:"files"`
	PublicAPI    PackageAPI     `json:"public_api"`
	Dependencies []string       `json:"dependencies,omitempty"`
	Dependents   []string       `json:"dependents,omitempty"`
	Summary      string         `json:"summary"`
	Purpose      string         `json:"purpose"`
	TotalSymbols int            `json:"total_symbols"`
	IsMain       bool           `json:"is_main,omitempty"`
	IsTest       bool           `json:"is_test,omitempty"`
}

// FileOverview contains summary information about a file.
type FileOverview struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	LineCount int      `json:"line_count,omitempty"`
	Symbols   []string `json:"symbols"`
	IsTest    bool     `json:"is_test,omitempty"`
}

// PackageAPI represents the public API of a package.
type PackageAPI struct {
	Types      []SymbolSummary `json:"types,omitempty"`
	Functions  []SymbolSummary `json:"functions,omitempty"`
	Interfaces []SymbolSummary `json:"interfaces,omitempty"`
	Constants  []SymbolSummary `json:"constants,omitempty"`
	Variables  []SymbolSummary `json:"variables,omitempty"`
}

// SymbolSummary contains summary information about a symbol.
type SymbolSummary struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Signature string `json:"signature,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Exported  bool   `json:"exported"`
}

func (t *explorePackageTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
	m := params.ToMap()
	// Validate inputs
	if ctx == nil {
		return nil, errors.New("ctx must not be nil")
	}
	if t.index == nil {
		return &Result{
			Success: false,
			Error:   "symbol index not initialized",
		}, nil
	}

	start := time.Now()

	// Parse parameters
	pkgName, ok := m["package"].(string)
	if !ok || pkgName == "" {
		// Record failed attempt in CRS
		stepBuilder := crs.NewTraceStepBuilder().
			WithAction("explore_package").
			WithTarget("unknown").
			WithTool("explore_package").
			WithDuration(time.Since(start)).
			WithError("package parameter is required").
			WithMetadata("navigation_level", "1")
		traceStep := stepBuilder.Build()
		return &Result{
			Success:   false,
			Error:     "package parameter is required",
			TraceStep: &traceStep,
		}, nil
	}

	includePrivate := false
	if v, ok := m["include_private"].(bool); ok {
		includePrivate = v
	}

	includeDeps := true
	if v, ok := m["include_dependencies"].(bool); ok {
		includeDeps = v
	}

	includeDependents := true
	if v, ok := m["include_dependents"].(bool); ok {
		includeDependents = v
	}

	maxSymbols := 20
	if v, ok := m["max_symbols"].(float64); ok {
		maxSymbols = int(v)
	} else if v, ok := m["max_symbols"].(int); ok {
		maxSymbols = v
	}

	slog.Debug("explore_package starting",
		slog.String("package", pkgName),
		slog.Bool("include_private", includePrivate),
		slog.Bool("include_deps", includeDeps),
		slog.Int("max_symbols", maxSymbols),
	)

	// Find the package
	packageSymbols := t.index.GetByKind(ast.SymbolKindPackage)
	var matchedFiles []string

	for i, pkgSym := range packageSymbols {
		// Check for context cancellation periodically
		if i%100 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}
		// Match by exact name or path suffix
		if pkgSym.Name == pkgName ||
			strings.HasSuffix(pkgSym.FilePath, "/"+pkgName+"/") ||
			strings.Contains(pkgSym.FilePath, "/"+pkgName+"/") ||
			strings.HasPrefix(pkgSym.FilePath, pkgName+"/") {
			matchedFiles = append(matchedFiles, pkgSym.FilePath)
		}
	}

	if len(matchedFiles) == 0 {
		// Record failed lookup attempt in CRS for debugging
		stepBuilder := crs.NewTraceStepBuilder().
			WithAction("explore_package").
			WithTarget(pkgName).
			WithTool("explore_package").
			WithDuration(time.Since(start)).
			WithError(fmt.Sprintf("package '%s' not found", pkgName)).
			WithMetadata("navigation_level", "1").
			WithMetadata("packages_searched", strconv.Itoa(len(packageSymbols)))
		traceStep := stepBuilder.Build()

		slog.Debug("explore_package package not found",
			slog.String("package", pkgName),
			slog.Int("packages_searched", len(packageSymbols)),
		)

		return &Result{
			Success:   false,
			Error:     fmt.Sprintf("package '%s' not found", pkgName),
			TraceStep: &traceStep,
		}, nil
	}

	// Remove duplicates and sort
	fileSet := make(map[string]bool)
	for _, f := range matchedFiles {
		fileSet[f] = true
	}
	matchedFiles = make([]string, 0, len(fileSet))
	for f := range fileSet {
		matchedFiles = append(matchedFiles, f)
	}
	sort.Strings(matchedFiles)

	// Build file overviews and collect symbols
	var files []FileOverview
	var allSymbols []*ast.Symbol
	isMain := false
	isTest := strings.HasSuffix(pkgName, "_test")

	for _, filePath := range matchedFiles {
		symbols := t.index.GetByFile(filePath)
		allSymbols = append(allSymbols, symbols...)

		// Get symbol names for this file
		var symbolNames []string
		for _, sym := range symbols {
			if sym.Kind == ast.SymbolKindPackage {
				continue
			}
			if !includePrivate && !sym.Exported {
				continue
			}
			symbolNames = append(symbolNames, sym.Name)
			if sym.Name == "main" && sym.Kind == ast.SymbolKindFunction {
				isMain = true
			}
		}

		// Limit symbol names per file
		if len(symbolNames) > 10 {
			symbolNames = symbolNames[:10]
		}

		files = append(files, FileOverview{
			Name:    extractFileName(filePath),
			Path:    filePath,
			Symbols: symbolNames,
			IsTest:  strings.HasSuffix(filePath, "_test.go"),
		})
	}

	// Build public API
	publicAPI := t.buildPublicAPI(allSymbols, includePrivate, maxSymbols)

	// Find dependencies
	var dependencies []string
	var dependents []string

	if includeDeps || includeDependents {
		dependencies, dependents = t.findDependencies(matchedFiles, pkgName, includeDeps, includeDependents)
	}

	// Infer purpose
	purpose := t.inferPurpose(pkgName, publicAPI, isMain, isTest)

	// Build result
	path := ""
	if len(matchedFiles) > 0 {
		path = extractPackagePath(matchedFiles[0])
	}

	result := &ExplorePackageResult{
		Name:         pkgName,
		Path:         path,
		Files:        files,
		PublicAPI:    publicAPI,
		Dependencies: dependencies,
		Dependents:   dependents,
		Summary:      fmt.Sprintf("Package %s: %d files, %d symbols", pkgName, len(files), len(allSymbols)),
		Purpose:      purpose,
		TotalSymbols: len(allSymbols),
		IsMain:       isMain,
		IsTest:       isTest,
	}

	duration := time.Since(start)

	// Build CRS trace step
	symbolNames := t.collectSymbolNames(publicAPI)

	stepBuilder := crs.NewTraceStepBuilder().
		WithAction("explore_package").
		WithTarget(pkgName).
		WithTool("explore_package").
		WithDuration(duration).
		WithSymbolsFound(symbolNames).
		WithMetadata("navigation_level", "1").
		WithMetadata("files_found", strconv.Itoa(len(files))).
		WithMetadata("total_symbols", strconv.Itoa(result.TotalSymbols)).
		WithMetadata("types_count", strconv.Itoa(len(publicAPI.Types))).
		WithMetadata("functions_count", strconv.Itoa(len(publicAPI.Functions))).
		WithMetadata("interfaces_count", strconv.Itoa(len(publicAPI.Interfaces))).
		WithMetadata("purpose", purpose)

	// Report dependencies to CRS
	for _, dep := range dependencies {
		stepBuilder.WithDependency(pkgName, dep)
	}
	for _, dep := range dependents {
		stepBuilder.WithDependency(dep, pkgName)
	}

	// Mark package as expanded
	stepBuilder.WithProofUpdate(
		"pkg:"+pkgName,
		"expanded",
		fmt.Sprintf("Explored: %d files, %d symbols, purpose: %s", len(files), result.TotalSymbols, purpose),
		"soft", // Exploration is soft signal (deeper analysis possible)
	)

	traceStep := stepBuilder.Build()

	// Format output
	output, _ := json.Marshal(result)

	return &Result{
		Success:    true,
		Output:     result,
		OutputText: t.formatAsText(result),
		TokensUsed: estimateTokens(string(output)),
		Duration:   duration,
		TraceStep:  &traceStep,
		Metadata: map[string]any{
			"level":           "package",
			"navigation_tool": true,
			"package":         pkgName,
			"files":           len(files),
			"symbols":         result.TotalSymbols,
		},
	}, nil
}

// buildPublicAPI categorizes symbols into the public API structure.
func (t *explorePackageTool) buildPublicAPI(symbols []*ast.Symbol, includePrivate bool, maxSymbols int) PackageAPI {
	api := PackageAPI{}

	for _, sym := range symbols {
		if sym.Kind == ast.SymbolKindPackage || sym.Kind == ast.SymbolKindImport {
			continue
		}
		if !includePrivate && !sym.Exported {
			continue
		}

		summary := SymbolSummary{
			Name:      sym.Name,
			Kind:      kindToString(sym.Kind),
			Signature: sym.Signature,
			File:      extractFileName(sym.FilePath),
			Line:      sym.StartLine,
			Exported:  sym.Exported,
		}

		switch sym.Kind {
		case ast.SymbolKindInterface:
			if len(api.Interfaces) < maxSymbols {
				api.Interfaces = append(api.Interfaces, summary)
			}
		case ast.SymbolKindStruct, ast.SymbolKindClass, ast.SymbolKindType, ast.SymbolKindEnum:
			if len(api.Types) < maxSymbols {
				api.Types = append(api.Types, summary)
			}
		case ast.SymbolKindFunction, ast.SymbolKindMethod:
			if len(api.Functions) < maxSymbols {
				api.Functions = append(api.Functions, summary)
			}
		case ast.SymbolKindConstant:
			if len(api.Constants) < maxSymbols {
				api.Constants = append(api.Constants, summary)
			}
		case ast.SymbolKindVariable:
			if len(api.Variables) < maxSymbols {
				api.Variables = append(api.Variables, summary)
			}
		}
	}

	return api
}

// findDependencies finds imports and reverse imports for a package.
func (t *explorePackageTool) findDependencies(files []string, pkgName string, includeDeps, includeDependents bool) ([]string, []string) {
	var deps, dependents []string
	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	// Get all imports
	importSymbols := t.index.GetByKind(ast.SymbolKindImport)
	depsSet := make(map[string]bool)
	dependentsSet := make(map[string]bool)

	for _, imp := range importSymbols {
		if fileSet[imp.FilePath] && includeDeps {
			// This file imports something
			if imp.Name != "" && imp.Name != pkgName {
				depsSet[imp.Name] = true
			}
		}

		// Check if this import is importing our package
		if includeDependents {
			if imp.Name == pkgName || strings.HasSuffix(imp.Name, "/"+pkgName) {
				// Find which package this import is in
				// This is approximate - we'd need more context for accuracy
				importerPkg := extractPackageName(imp.FilePath)
				if importerPkg != "" && importerPkg != pkgName {
					dependentsSet[importerPkg] = true
				}
			}
		}
	}

	for d := range depsSet {
		deps = append(deps, d)
	}
	sort.Strings(deps)

	for d := range dependentsSet {
		dependents = append(dependents, d)
	}
	sort.Strings(dependents)

	return deps, dependents
}

// inferPurpose tries to infer the package's purpose from its contents.
func (t *explorePackageTool) inferPurpose(pkgName string, api PackageAPI, isMain, isTest bool) string {
	if isMain {
		return "Application entry point"
	}
	if isTest {
		return "Test package"
	}

	// Look for common patterns in type/function names
	typeNames := make([]string, 0)
	funcNames := make([]string, 0)

	for _, typ := range api.Types {
		typeNames = append(typeNames, strings.ToLower(typ.Name))
	}
	for _, fn := range api.Functions {
		funcNames = append(funcNames, strings.ToLower(fn.Name))
	}
	for _, iface := range api.Interfaces {
		typeNames = append(typeNames, strings.ToLower(iface.Name))
	}

	allNames := append(typeNames, funcNames...)
	nameStr := strings.Join(allNames, " ")

	// Pattern matching for common purposes
	switch {
	case containsAny(nameStr, "handler", "controller", "route", "endpoint"):
		return "HTTP request handling"
	case containsAny(nameStr, "model", "entity", "schema"):
		return "Data models and structures"
	case containsAny(nameStr, "service", "provider"):
		return "Business logic services"
	case containsAny(nameStr, "repository", "store", "database", "db"):
		return "Data persistence layer"
	case containsAny(nameStr, "client", "api", "request", "response"):
		return "API client/communication"
	case containsAny(nameStr, "config", "settings", "options"):
		return "Configuration management"
	case containsAny(nameStr, "util", "helper", "common"):
		return "Utility functions"
	case containsAny(nameStr, "auth", "token", "session", "permission"):
		return "Authentication/authorization"
	case containsAny(nameStr, "log", "metric", "trace", "monitor"):
		return "Observability/logging"
	case containsAny(nameStr, "parse", "encode", "decode", "marshal"):
		return "Data parsing/encoding"
	case containsAny(pkgName, "middleware"):
		return "Request middleware"
	default:
		return fmt.Sprintf("Package providing %d types and %d functions", len(api.Types)+len(api.Interfaces), len(api.Functions))
	}
}

// collectSymbolNames collects all symbol names from the API.
func (t *explorePackageTool) collectSymbolNames(api PackageAPI) []string {
	var names []string
	for _, s := range api.Types {
		names = append(names, s.Name)
	}
	for _, s := range api.Functions {
		names = append(names, s.Name)
	}
	for _, s := range api.Interfaces {
		names = append(names, s.Name)
	}
	for _, s := range api.Constants {
		names = append(names, s.Name)
	}
	for _, s := range api.Variables {
		names = append(names, s.Name)
	}
	return names
}

// formatAsText formats the result as human-readable text.
func (t *explorePackageTool) formatAsText(result *ExplorePackageResult) string {
	var sb strings.Builder

	marker := ""
	if result.IsMain {
		marker = " [main]"
	} else if result.IsTest {
		marker = " [test]"
	}

	sb.WriteString(fmt.Sprintf("# Package: %s%s\n\n", result.Name, marker))
	sb.WriteString(fmt.Sprintf("**Path:** %s\n", result.Path))
	sb.WriteString(fmt.Sprintf("**Purpose:** %s\n", result.Purpose))
	sb.WriteString(fmt.Sprintf("**Summary:** %s\n\n", result.Summary))

	// Files
	sb.WriteString("## Files\n\n")
	for _, f := range result.Files {
		testMarker := ""
		if f.IsTest {
			testMarker = " (test)"
		}
		sb.WriteString(fmt.Sprintf("- **%s**%s: %s\n", f.Name, testMarker, strings.Join(f.Symbols, ", ")))
	}
	sb.WriteString("\n")

	// Public API
	sb.WriteString("## Public API\n\n")

	if len(result.PublicAPI.Interfaces) > 0 {
		sb.WriteString("### Interfaces\n")
		for _, s := range result.PublicAPI.Interfaces {
			sb.WriteString(fmt.Sprintf("- `%s` (%s:%d)\n", s.Name, s.File, s.Line))
		}
		sb.WriteString("\n")
	}

	if len(result.PublicAPI.Types) > 0 {
		sb.WriteString("### Types\n")
		for _, s := range result.PublicAPI.Types {
			sb.WriteString(fmt.Sprintf("- `%s` [%s] (%s:%d)\n", s.Name, s.Kind, s.File, s.Line))
		}
		sb.WriteString("\n")
	}

	if len(result.PublicAPI.Functions) > 0 {
		sb.WriteString("### Functions\n")
		for _, s := range result.PublicAPI.Functions {
			sig := s.Name
			if s.Signature != "" {
				sig = s.Signature
			}
			sb.WriteString(fmt.Sprintf("- `%s` (%s:%d)\n", sig, s.File, s.Line))
		}
		sb.WriteString("\n")
	}

	// Dependencies
	if len(result.Dependencies) > 0 {
		sb.WriteString("## Imports\n\n")
		sb.WriteString(strings.Join(result.Dependencies, ", "))
		sb.WriteString("\n\n")
	}

	if len(result.Dependents) > 0 {
		sb.WriteString("## Used By\n\n")
		sb.WriteString(strings.Join(result.Dependents, ", "))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// Helper functions

func extractFileName(filePath string) string {
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash >= 0 {
		return filePath[lastSlash+1:]
	}
	return filePath
}

func extractPackageName(filePath string) string {
	// Try to extract package name from path
	// e.g., "pkg/handlers/user.go" -> "handlers"
	dir := extractPackagePath(filePath)
	lastSlash := strings.LastIndex(dir, "/")
	if lastSlash >= 0 {
		return dir[lastSlash+1:]
	}
	return dir
}

func kindToString(kind ast.SymbolKind) string {
	switch kind {
	case ast.SymbolKindFunction:
		return "function"
	case ast.SymbolKindMethod:
		return "method"
	case ast.SymbolKindStruct:
		return "struct"
	case ast.SymbolKindClass:
		return "class"
	case ast.SymbolKindInterface:
		return "interface"
	case ast.SymbolKindType:
		return "type"
	case ast.SymbolKindEnum:
		return "enum"
	case ast.SymbolKindConstant:
		return "const"
	case ast.SymbolKindVariable:
		return "var"
	default:
		return "symbol"
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
