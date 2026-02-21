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
// graph_overview Tool
// ============================================================================

// graphOverviewTool provides project-level structure visualization.
type graphOverviewTool struct {
	graph *graph.Graph
	index *index.SymbolIndex
}

// NewGraphOverviewTool creates the graph_overview tool.
//
// Description:
//
//	Creates a tool for showing high-level project structure including
//	packages, their relationships, and metrics. This is the top-level
//	navigation tool for understanding a codebase.
//
// Thread Safety: Safe for concurrent use.
func NewGraphOverviewTool(g *graph.Graph, idx *index.SymbolIndex) Tool {
	return &graphOverviewTool{
		graph: g,
		index: idx,
	}
}

func (t *graphOverviewTool) Name() string {
	return "graph_overview"
}

func (t *graphOverviewTool) Category() ToolCategory {
	return CategoryExploration
}

func (t *graphOverviewTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "graph_overview",
		Description: "Shows high-level project structure including packages, their relationships, and metrics. Use this FIRST when asked about project organization, package structure, or 'what does this codebase do?'",
		Parameters: map[string]ParamDef{
			"depth": {
				Type:        ParamTypeInt,
				Description: "How deep to traverse (1=packages only, 2=packages+top files, 3=full details)",
				Required:    false,
				Default:     2,
			},
			"include_dependencies": {
				Type:        ParamTypeBool,
				Description: "Include inter-package dependency graph",
				Required:    false,
				Default:     true,
			},
			"include_metrics": {
				Type:        ParamTypeBool,
				Description: "Include symbol counts and file metrics",
				Required:    false,
				Default:     true,
			},
			"filter": {
				Type:        ParamTypeString,
				Description: "Filter to specific package path prefix (e.g., 'pkg/api')",
				Required:    false,
			},
		},
		Category:    CategoryExploration,
		Priority:    100, // Highest priority for overview questions
		Requires:    []string{"graph_initialized"},
		SideEffects: false,
		Timeout:     10 * time.Second,
	}
}

// GraphOverviewResult is the output of the graph_overview tool.
type GraphOverviewResult struct {
	Project         string              `json:"project"`
	Packages        []PackageOverview   `json:"packages"`
	DependencyGraph []PackageDependency `json:"dependency_graph,omitempty"`
	TotalPackages   int                 `json:"total_packages"`
	TotalFiles      int                 `json:"total_files"`
	TotalSymbols    int                 `json:"total_symbols"`
	Languages       []string            `json:"languages"`
}

// PackageOverview contains summary information about a package.
type PackageOverview struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	FileCount   int      `json:"file_count"`
	SymbolCount int      `json:"symbol_count"`
	TypeCount   int      `json:"type_count"`
	FuncCount   int      `json:"func_count"`
	IsMain      bool     `json:"is_main,omitempty"`
	IsTest      bool     `json:"is_test,omitempty"`
	TopFiles    []string `json:"top_files,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	DependedBy  []string `json:"depended_by,omitempty"`
}

// PackageDependency represents a dependency edge between packages.
type PackageDependency struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"` // "import"
}

func (t *graphOverviewTool) Execute(ctx context.Context, params TypedParams) (*Result, error) {
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
	depth := 2
	if v, ok := m["depth"].(float64); ok {
		depth = int(v)
	} else if v, ok := m["depth"].(int); ok {
		depth = v
	}

	includeDeps := true
	if v, ok := m["include_dependencies"].(bool); ok {
		includeDeps = v
	}

	includeMetrics := true
	if v, ok := m["include_metrics"].(bool); ok {
		includeMetrics = v
	}

	filter := ""
	if v, ok := m["filter"].(string); ok {
		filter = v
	}

	slog.Debug("graph_overview starting",
		slog.Int("depth", depth),
		slog.Bool("include_deps", includeDeps),
		slog.Bool("include_metrics", includeMetrics),
		slog.String("filter", filter),
	)

	// Get all package symbols
	packageSymbols := t.index.GetByKind(ast.SymbolKindPackage)

	// Build package map
	packageMap := make(map[string]*PackageOverview)
	filesByPackage := make(map[string]map[string]bool)
	languageSet := make(map[string]bool)
	totalFiles := 0
	totalSymbols := 0

	for i, pkgSym := range packageSymbols {
		// Check for context cancellation periodically
		if i%100 == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
		}

		pkgName := pkgSym.Name
		if pkgName == "" {
			continue
		}

		// Apply filter
		if filter != "" && !strings.HasPrefix(pkgName, filter) && !strings.HasPrefix(pkgSym.FilePath, filter) {
			continue
		}

		// Track language
		if pkgSym.Language != "" {
			languageSet[pkgSym.Language] = true
		}

		// Initialize package if not exists
		if _, exists := packageMap[pkgName]; !exists {
			isTest := strings.HasSuffix(pkgName, "_test") || strings.HasSuffix(pkgSym.FilePath, "_test.go")
			packageMap[pkgName] = &PackageOverview{
				Name:   pkgName,
				Path:   extractPackagePath(pkgSym.FilePath),
				IsTest: isTest,
			}
			filesByPackage[pkgName] = make(map[string]bool)
		}

		// Track files
		filesByPackage[pkgName][pkgSym.FilePath] = true
	}

	// Count symbols per package and check for main
	for pkgName, files := range filesByPackage {
		pkg := packageMap[pkgName]
		pkg.FileCount = len(files)
		totalFiles += len(files)

		if depth >= 2 {
			// Collect top files (up to 5)
			fileList := make([]string, 0, len(files))
			for f := range files {
				fileList = append(fileList, f)
			}
			sort.Strings(fileList)
			if len(fileList) > 5 {
				fileList = fileList[:5]
			}
			pkg.TopFiles = fileList
		}

		if includeMetrics {
			for filePath := range files {
				symbols := t.index.GetByFile(filePath)
				for _, sym := range symbols {
					if sym.Kind == ast.SymbolKindPackage {
						continue
					}
					pkg.SymbolCount++
					totalSymbols++

					switch sym.Kind {
					case ast.SymbolKindStruct, ast.SymbolKindClass, ast.SymbolKindInterface, ast.SymbolKindType, ast.SymbolKindEnum:
						pkg.TypeCount++
					case ast.SymbolKindFunction, ast.SymbolKindMethod:
						pkg.FuncCount++
						// Check for main function
						if sym.Name == "main" && sym.Kind == ast.SymbolKindFunction {
							pkg.IsMain = true
						}
					}
				}
			}
		}
	}

	// Build dependency graph
	var dependencies []PackageDependency
	if includeDeps {
		dependencies = t.buildDependencyGraph(packageMap, filesByPackage)

		// Populate DependsOn/DependedBy for each package
		dependsOn := make(map[string][]string)
		dependedBy := make(map[string][]string)

		for _, dep := range dependencies {
			dependsOn[dep.From] = append(dependsOn[dep.From], dep.To)
			dependedBy[dep.To] = append(dependedBy[dep.To], dep.From)
		}

		for pkgName, pkg := range packageMap {
			if deps, ok := dependsOn[pkgName]; ok {
				sort.Strings(deps)
				pkg.DependsOn = deps
			}
			if deps, ok := dependedBy[pkgName]; ok {
				sort.Strings(deps)
				pkg.DependedBy = deps
			}
		}
	}

	// Build sorted package list
	packages := make([]PackageOverview, 0, len(packageMap))
	for _, pkg := range packageMap {
		packages = append(packages, *pkg)
	}
	sort.Slice(packages, func(i, j int) bool {
		// Main packages first, then by name
		if packages[i].IsMain != packages[j].IsMain {
			return packages[i].IsMain
		}
		return packages[i].Name < packages[j].Name
	})

	// Build language list
	languages := make([]string, 0, len(languageSet))
	for lang := range languageSet {
		languages = append(languages, lang)
	}
	sort.Strings(languages)

	// Build result
	result := &GraphOverviewResult{
		Project:         t.inferProjectName(packages),
		Packages:        packages,
		DependencyGraph: dependencies,
		TotalPackages:   len(packages),
		TotalFiles:      totalFiles,
		TotalSymbols:    totalSymbols,
		Languages:       languages,
	}

	duration := time.Since(start)

	// Build CRS trace step
	packageNames := make([]string, 0, len(packages))
	for _, pkg := range packages {
		packageNames = append(packageNames, pkg.Name)
	}

	stepBuilder := crs.NewTraceStepBuilder().
		WithAction("graph_overview").
		WithTarget("project").
		WithTool("graph_overview").
		WithDuration(duration).
		WithSymbolsFound(packageNames).
		WithMetadata("navigation_level", "0").
		WithMetadata("total_packages", strconv.Itoa(result.TotalPackages)).
		WithMetadata("total_files", strconv.Itoa(result.TotalFiles)).
		WithMetadata("total_symbols", strconv.Itoa(result.TotalSymbols)).
		WithMetadata("languages", strings.Join(languages, ",")).
		WithMetadata("depth", strconv.Itoa(depth))

	// Report dependencies to CRS
	for _, dep := range dependencies {
		stepBuilder.WithDependency(dep.From, dep.To)
	}

	// Mark project as explored
	stepBuilder.WithProofUpdate(
		"project:overview",
		"expanded",
		fmt.Sprintf("Overview: %d packages, %d files, %d symbols", result.TotalPackages, result.TotalFiles, result.TotalSymbols),
		"soft", // Overview is soft signal (can explore deeper)
	)

	// Mark each package as known
	for _, pkg := range packages {
		stepBuilder.WithProofUpdate(
			"pkg:"+pkg.Name,
			"proven",
			fmt.Sprintf("%d files, %d symbols", pkg.FileCount, pkg.SymbolCount),
			"hard", // Package existence is hard signal
		)
	}

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
			"level":           "overview",
			"navigation_tool": true,
			"packages_found":  result.TotalPackages,
			"files_found":     result.TotalFiles,
			"symbols_found":   result.TotalSymbols,
		},
	}, nil
}

// buildDependencyGraph builds the inter-package dependency graph.
func (t *graphOverviewTool) buildDependencyGraph(packageMap map[string]*PackageOverview, filesByPackage map[string]map[string]bool) []PackageDependency {
	// Pre-allocate with estimate (5 deps per package average)
	deps := make([]PackageDependency, 0, len(packageMap)*5)
	seen := make(map[string]bool)

	// Build reverse index: file -> package (O(files) instead of O(imports * packages))
	fileToPackage := make(map[string]string)
	for pkgName, files := range filesByPackage {
		for f := range files {
			fileToPackage[f] = pkgName
		}
	}

	// Get all import symbols
	importSymbols := t.index.GetByKind(ast.SymbolKindImport)

	for _, imp := range importSymbols {
		// Find which package this import is in (O(1) lookup)
		fromPkg := fileToPackage[imp.FilePath]
		if fromPkg == "" {
			continue
		}

		// The import name is the package being imported
		// Try to find if it's one of our packages
		toPkg := imp.Name
		if toPkg == "" {
			continue
		}

		// Check if this is an internal package
		if _, exists := packageMap[toPkg]; !exists {
			// Try to match by path suffix
			for pkgName := range packageMap {
				if strings.HasSuffix(toPkg, "/"+pkgName) || toPkg == pkgName {
					toPkg = pkgName
					break
				}
			}
		}

		// Skip external packages and self-references
		if _, exists := packageMap[toPkg]; !exists || fromPkg == toPkg {
			continue
		}

		// Deduplicate
		key := fromPkg + "->" + toPkg
		if seen[key] {
			continue
		}
		seen[key] = true

		deps = append(deps, PackageDependency{
			From: fromPkg,
			To:   toPkg,
			Type: "import",
		})
	}

	return deps
}

// inferProjectName tries to infer a project name from packages.
func (t *graphOverviewTool) inferProjectName(packages []PackageOverview) string {
	// Look for a main package
	for _, pkg := range packages {
		if pkg.IsMain {
			// Try to get project name from path
			if pkg.Path != "" && pkg.Path != "." {
				parts := strings.Split(pkg.Path, "/")
				if len(parts) > 0 {
					return parts[0]
				}
			}
		}
	}

	// Fall back to first package path
	if len(packages) > 0 && packages[0].Path != "" {
		parts := strings.Split(packages[0].Path, "/")
		if len(parts) > 0 {
			return parts[0]
		}
	}

	return "project"
}

// formatAsText formats the result as human-readable text.
func (t *graphOverviewTool) formatAsText(result *GraphOverviewResult) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Project: %s\n\n", result.Project))
	sb.WriteString(fmt.Sprintf("**Summary:** %d packages, %d files, %d symbols\n", result.TotalPackages, result.TotalFiles, result.TotalSymbols))
	sb.WriteString(fmt.Sprintf("**Languages:** %s\n\n", strings.Join(result.Languages, ", ")))

	sb.WriteString("## Packages\n\n")
	for _, pkg := range result.Packages {
		marker := ""
		if pkg.IsMain {
			marker = " [main]"
		} else if pkg.IsTest {
			marker = " [test]"
		}

		sb.WriteString(fmt.Sprintf("### %s%s\n", pkg.Name, marker))
		sb.WriteString(fmt.Sprintf("- **Path:** %s\n", pkg.Path))
		sb.WriteString(fmt.Sprintf("- **Files:** %d\n", pkg.FileCount))
		sb.WriteString(fmt.Sprintf("- **Symbols:** %d (%d types, %d functions)\n", pkg.SymbolCount, pkg.TypeCount, pkg.FuncCount))

		if len(pkg.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("- **Imports:** %s\n", strings.Join(pkg.DependsOn, ", ")))
		}
		if len(pkg.DependedBy) > 0 {
			sb.WriteString(fmt.Sprintf("- **Used by:** %s\n", strings.Join(pkg.DependedBy, ", ")))
		}
		sb.WriteString("\n")
	}

	if len(result.DependencyGraph) > 0 {
		sb.WriteString("## Dependency Graph\n\n")
		sb.WriteString("```\n")
		for _, dep := range result.DependencyGraph {
			sb.WriteString(fmt.Sprintf("%s → %s\n", dep.From, dep.To))
		}
		sb.WriteString("```\n")
	}

	return sb.String()
}
