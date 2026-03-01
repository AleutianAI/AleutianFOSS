// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package graph

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

// =============================================================================
// Hierarchy Levels
// =============================================================================

// HierarchyLevel represents a level in the code hierarchy.
const (
	// LevelProject represents the project (root) level.
	LevelProject = 0

	// LevelPackage represents the package level.
	LevelPackage = 1

	// LevelFile represents the file level.
	LevelFile = 2

	// LevelSymbol represents the symbol (function/type/etc) level.
	LevelSymbol = 3
)

// LevelName returns the human-readable name for a hierarchy level.
func LevelName(level int) string {
	switch level {
	case LevelProject:
		return "project"
	case LevelPackage:
		return "package"
	case LevelFile:
		return "file"
	case LevelSymbol:
		return "symbol"
	default:
		return "unknown"
	}
}

// =============================================================================
// Package Info
// =============================================================================

// PackageInfo contains metadata about a package in the graph.
type PackageInfo struct {
	// Name is the package path (e.g., "services/trace/graph").
	Name string

	// Files lists file paths in this package.
	Files []string

	// NodeCount is the total number of symbols in this package.
	NodeCount int

	// ExportedCount is the number of exported (public) symbols.
	ExportedCount int

	// ImportCount is the number of packages this package imports.
	ImportCount int

	// ImportedByCount is the number of packages that import this package.
	ImportedByCount int

	// Types counts types (struct, interface, etc.) in the package.
	Types int

	// Functions counts functions and methods in the package.
	Functions int
}

// =============================================================================
// Coupling Metrics
// =============================================================================

// CouplingMetrics contains package coupling analysis results.
type CouplingMetrics struct {
	// Package is the package being measured.
	Package string

	// Afferent is the number of packages that depend on this package (Ca).
	// Higher = more packages would be affected by changes here.
	Afferent int

	// Efferent is the number of packages this package depends on (Ce).
	// Higher = more dependencies to track.
	Efferent int

	// Instability is Ce / (Ca + Ce), range [0, 1].
	// 0 = maximally stable (many dependents, few dependencies)
	// 1 = maximally unstable (few dependents, many dependencies)
	Instability float64

	// AbstractTypes is the count of interfaces and abstract types.
	AbstractTypes int

	// ConcreteTypes is the count of concrete types.
	ConcreteTypes int

	// Abstractness is AbstractTypes / (AbstractTypes + ConcreteTypes), range [0, 1].
	Abstractness float64
}

// =============================================================================
// HierarchicalGraph
// =============================================================================

// HierarchicalGraph wraps a Graph with hierarchical indexes for efficient
// package-level and file-level queries.
//
// Description:
//
//	HierarchicalGraph embeds the base Graph and adds:
//	- O(1) package lookup via packageIndex
//	- O(1) file lookup via fileIndex
//	- O(1) kind lookup via kindIndex
//	- Cached cross-package edges
//	- Package metadata with counts
//
// Thread Safety:
//
//	After construction, HierarchicalGraph is safe for concurrent reads.
//	Indexes are built during construction and never mutated.
//
// Performance:
//
//	| Operation | Complexity |
//	|-----------|------------|
//	| GetNodesInPackage | O(1) lookup + O(k) copy |
//	| GetNodesInFile | O(1) lookup + O(k) copy |
//	| GetCrossPackageEdges | O(1) cached |
//	| GetPackages | O(1) cached |
type HierarchicalGraph struct {
	// Embedded graph provides all base functionality.
	*Graph

	// packageIndex maps package path to nodes in that package.
	// Built during construction from Symbol.Package field.
	packageIndex map[string][]*Node

	// fileIndex maps file path to nodes defined in that file.
	// Built during construction from Symbol.FilePath field.
	fileIndex map[string][]*Node

	// kindIndex maps symbol kind to nodes of that kind.
	kindIndex map[ast.SymbolKind][]*Node

	// packages contains metadata for each package.
	packages map[string]*PackageInfo

	// packageList is a sorted list of package names for iteration.
	packageList []string

	// crossPackageEdges contains edges that cross package boundaries.
	// Cached during construction for O(1) access.
	crossPackageEdges []*Edge

	// internalEdges contains edges within the same package.
	internalEdges []*Edge

	// fileClassification holds the GR-60 graph-based file classification.
	// When non-nil, IsProductionFile delegates to this classification.
	// When nil, IsProductionFile falls back to heuristic isTestFilePath/isDocFilePath.
	fileClassification *FileClassification
}

// WrapGraph creates a HierarchicalGraph from an existing frozen Graph.
//
// Description:
//
//	Wraps the given Graph and builds hierarchical indexes. The graph
//	must be frozen (read-only) before wrapping.
//
// Inputs:
//
//	g - The graph to wrap. Must be frozen (IsFrozen() == true).
//
// Outputs:
//
//	*HierarchicalGraph - The wrapped graph with indexes. Never nil.
//	error - Non-nil if the graph is not frozen.
//
// Thread Safety:
//
//	The returned HierarchicalGraph is safe for concurrent reads.
func WrapGraph(g *Graph) (*HierarchicalGraph, error) {
	if g == nil {
		return nil, ErrNilGraph
	}
	if !g.IsFrozen() {
		return nil, ErrGraphNotFrozen
	}

	hg := &HierarchicalGraph{
		Graph:        g,
		packageIndex: make(map[string][]*Node),
		fileIndex:    make(map[string][]*Node),
		kindIndex:    make(map[ast.SymbolKind][]*Node),
		packages:     make(map[string]*PackageInfo),
	}

	hg.buildIndexes()
	return hg, nil
}

// buildIndexes populates all hierarchical indexes from the graph nodes.
func (hg *HierarchicalGraph) buildIndexes() {
	// Track package imports for coupling metrics
	pkgImports := make(map[string]map[string]bool)    // pkg -> imports
	pkgImportedBy := make(map[string]map[string]bool) // pkg -> imported by

	// CR-3 FIX: Use map for O(1) file tracking instead of O(n) containsString
	pkgFileSet := make(map[string]map[string]bool) // pkg -> files seen

	// First pass: index all nodes
	for _, node := range hg.Graph.Nodes() {
		if node.Symbol == nil {
			continue
		}

		sym := node.Symbol
		pkg := sym.Package
		if pkg == "" {
			// Derive package from file path
			pkg = extractPackage(sym.FilePath)
		}

		// Package index
		hg.packageIndex[pkg] = append(hg.packageIndex[pkg], node)

		// File index
		if sym.FilePath != "" {
			hg.fileIndex[sym.FilePath] = append(hg.fileIndex[sym.FilePath], node)
		}

		// Kind index
		hg.kindIndex[sym.Kind] = append(hg.kindIndex[sym.Kind], node)

		// Initialize package info if needed
		if _, exists := hg.packages[pkg]; !exists {
			hg.packages[pkg] = &PackageInfo{
				Name:  pkg,
				Files: []string{},
			}
			pkgImports[pkg] = make(map[string]bool)
			pkgImportedBy[pkg] = make(map[string]bool)
			pkgFileSet[pkg] = make(map[string]bool)
		}

		// Update package metrics
		info := hg.packages[pkg]
		info.NodeCount++

		if sym.Exported {
			info.ExportedCount++
		}

		switch sym.Kind {
		case ast.SymbolKindFunction, ast.SymbolKindMethod:
			info.Functions++
		case ast.SymbolKindStruct, ast.SymbolKindClass, ast.SymbolKindInterface:
			info.Types++
		}

		// Track files in package using O(1) set lookup
		if sym.FilePath != "" {
			fileSet := pkgFileSet[pkg]
			if !fileSet[sym.FilePath] {
				fileSet[sym.FilePath] = true
				info.Files = append(info.Files, sym.FilePath)
			}
		}
	}

	// Second pass: classify edges and build import tracking
	for _, edge := range hg.Graph.Edges() {
		fromNode, fromOK := hg.Graph.GetNode(edge.FromID)
		toNode, toOK := hg.Graph.GetNode(edge.ToID)

		if !fromOK || !toOK {
			continue
		}

		fromPkg := getNodePackage(fromNode)
		toPkg := getNodePackage(toNode)

		if fromPkg == toPkg {
			hg.internalEdges = append(hg.internalEdges, edge)
		} else {
			hg.crossPackageEdges = append(hg.crossPackageEdges, edge)

			// Track imports for coupling
			if edge.Type == EdgeTypeImports || edge.Type == EdgeTypeCalls || edge.Type == EdgeTypeReferences {
				if _, exists := pkgImports[fromPkg]; exists {
					pkgImports[fromPkg][toPkg] = true
				}
				if _, exists := pkgImportedBy[toPkg]; exists {
					pkgImportedBy[toPkg][fromPkg] = true
				}
			}
		}
	}

	// Update package import counts
	for pkg, info := range hg.packages {
		if imports, ok := pkgImports[pkg]; ok {
			info.ImportCount = len(imports)
		}
		if importedBy, ok := pkgImportedBy[pkg]; ok {
			info.ImportedByCount = len(importedBy)
		}
	}

	// Build sorted package list
	hg.packageList = make([]string, 0, len(hg.packages))
	for pkg := range hg.packages {
		hg.packageList = append(hg.packageList, pkg)
	}
	sort.Strings(hg.packageList)
}

// =============================================================================
// Core Query Methods
// =============================================================================

// GetPackages returns metadata for all packages in the graph.
//
// Description:
//
//	Returns a sorted list of PackageInfo for all packages, including
//	node counts, file counts, and coupling metrics.
//
// Outputs:
//
//	[]PackageInfo - Package metadata, sorted by package name.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackages() []PackageInfo {
	result := make([]PackageInfo, 0, len(hg.packageList))
	for _, pkg := range hg.packageList {
		if info, ok := hg.packages[pkg]; ok {
			result = append(result, *info)
		}
	}
	return result
}

// GetPackageNames returns a sorted list of all package names.
//
// Outputs:
//
//	[]string - Sorted package names.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackageNames() []string {
	result := make([]string, len(hg.packageList))
	copy(result, hg.packageList)
	return result
}

// GetPackageInfo returns metadata for a specific package.
//
// Inputs:
//
//	pkg - The package path to look up.
//
// Outputs:
//
//	*PackageInfo - Package metadata, or nil if not found.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackageInfo(pkg string) *PackageInfo {
	info, ok := hg.packages[pkg]
	if !ok {
		return nil
	}
	// CR-4 FIX: Return a deep copy to prevent mutation (including Files slice)
	result := *info
	result.Files = make([]string, len(info.Files))
	copy(result.Files, info.Files)
	return &result
}

// GetNodesInPackage returns all nodes in a package.
//
// Description:
//
//	O(1) lookup via packageIndex, returns defensive copy of node slice.
//
// Inputs:
//
//	pkg - The package path to look up.
//
// Outputs:
//
//	[]*Node - Nodes in the package, empty slice if not found.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetNodesInPackage(pkg string) []*Node {
	nodes := hg.packageIndex[pkg]
	if nodes == nil {
		return []*Node{}
	}
	result := make([]*Node, len(nodes))
	copy(result, nodes)
	return result
}

// GetNodesInFile returns all nodes defined in a file.
//
// Description:
//
//	O(1) lookup via fileIndex, returns defensive copy of node slice.
//
// Inputs:
//
//	filePath - The file path to look up.
//
// Outputs:
//
//	[]*Node - Nodes in the file, empty slice if not found.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetNodesInFile(filePath string) []*Node {
	nodes := hg.fileIndex[filePath]
	if nodes == nil {
		return []*Node{}
	}
	result := make([]*Node, len(nodes))
	copy(result, nodes)
	return result
}

// GetNodesByKind returns all nodes of a specific kind.
//
// Description:
//
//	O(1) lookup via kindIndex, returns defensive copy of node slice.
//
// Inputs:
//
//	kind - The symbol kind to filter by.
//
// Outputs:
//
//	[]*Node - Nodes of that kind, empty slice if none found.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetNodesByKind(kind ast.SymbolKind) []*Node {
	nodes := hg.kindIndex[kind]
	if nodes == nil {
		return []*Node{}
	}
	result := make([]*Node, len(nodes))
	copy(result, nodes)
	return result
}

// GetCrossPackageEdges returns all edges that cross package boundaries.
//
// Description:
//
//	O(1) cached access, returns defensive copy of edge slice.
//
// Outputs:
//
//	[]*Edge - Edges where source and target are in different packages.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetCrossPackageEdges() []*Edge {
	result := make([]*Edge, len(hg.crossPackageEdges))
	copy(result, hg.crossPackageEdges)
	return result
}

// GetInternalEdges returns all edges within the same package.
//
// Description:
//
//	O(1) cached access, returns defensive copy of edge slice.
//
// Outputs:
//
//	[]*Edge - Edges where source and target are in the same package.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetInternalEdges() []*Edge {
	result := make([]*Edge, len(hg.internalEdges))
	copy(result, hg.internalEdges)
	return result
}

// GetFilesInPackage returns all file paths in a package.
//
// Inputs:
//
//	pkg - The package path to look up.
//
// Outputs:
//
//	[]string - File paths in the package, empty slice if not found.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetFilesInPackage(pkg string) []string {
	info := hg.packages[pkg]
	if info == nil {
		return []string{}
	}
	result := make([]string, len(info.Files))
	copy(result, info.Files)
	return result
}

// =============================================================================
// Navigation Methods
// =============================================================================

// DrillDown returns children at the next hierarchy level.
//
// Description:
//
//	Starting from an entity at the given level, returns the entities
//	one level deeper in the hierarchy.
//
//	Level 0 (Project) → Level 1 (Packages): Returns all packages
//	Level 1 (Package) → Level 2 (Files): Returns files in the package
//	Level 2 (File) → Level 3 (Symbols): Returns symbols in the file
//	Level 3 (Symbol): No children, returns empty
//
// Inputs:
//
//	level - The current hierarchy level (0-3).
//	id - The entity ID (empty for project, pkg path, file path, or node ID).
//
// Outputs:
//
//	[]*Node - Child nodes at the next level. For levels 0-1, returns
//	          representative nodes. For level 2, returns actual symbol nodes.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) DrillDown(level int, id string) []*Node {
	switch level {
	case LevelProject:
		// Project → Packages: return first node from each package
		result := make([]*Node, 0, len(hg.packageList))
		for _, pkg := range hg.packageList {
			if nodes := hg.packageIndex[pkg]; len(nodes) > 0 {
				result = append(result, nodes[0])
			}
		}
		return result

	case LevelPackage:
		// Package → Files: return first node from each file in package
		info := hg.packages[id]
		if info == nil {
			return []*Node{}
		}
		result := make([]*Node, 0, len(info.Files))
		for _, file := range info.Files {
			if nodes := hg.fileIndex[file]; len(nodes) > 0 {
				result = append(result, nodes[0])
			}
		}
		return result

	case LevelFile:
		// File → Symbols: return all nodes in file
		return hg.GetNodesInFile(id)

	case LevelSymbol:
		// Symbols have no children
		return []*Node{}

	default:
		return []*Node{}
	}
}

// RollUp returns the parent entity ID at the previous hierarchy level.
//
// Description:
//
//	Given a node ID, returns the parent entity:
//	Symbol → File path
//	File → Package path
//	Package → "" (project root)
//	Project → "" (no parent)
//
// Inputs:
//
//	nodeID - The node ID to get the parent for.
//
// Outputs:
//
//	string - The parent entity ID, or empty string if at root.
//	int - The parent's hierarchy level.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) RollUp(nodeID string) (string, int) {
	node, ok := hg.Graph.GetNode(nodeID)
	if !ok || node.Symbol == nil {
		return "", LevelProject
	}

	// Symbol → File
	if node.Symbol.FilePath != "" {
		return node.Symbol.FilePath, LevelFile
	}

	// Fall back to package
	pkg := getNodePackage(node)
	if pkg != "" {
		return pkg, LevelPackage
	}

	return "", LevelProject
}

// GetSiblings returns nodes at the same hierarchy level as the given node.
//
// Description:
//
//	Returns other nodes that share the same parent. For a symbol, returns
//	other symbols in the same file. For a file (identified by any symbol
//	in it), returns other files in the same package.
//
// Inputs:
//
//	nodeID - The node ID to find siblings for.
//
// Outputs:
//
//	[]*Node - Sibling nodes (not including the input node itself).
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetSiblings(nodeID string) []*Node {
	node, ok := hg.Graph.GetNode(nodeID)
	if !ok || node.Symbol == nil {
		return []*Node{}
	}

	// Get siblings in same file
	siblings := hg.fileIndex[node.Symbol.FilePath]
	result := make([]*Node, 0, len(siblings))
	for _, sibling := range siblings {
		if sibling.ID != nodeID {
			result = append(result, sibling)
		}
	}
	return result
}

// GetPackageSiblings returns other files in the same package as the node.
//
// Inputs:
//
//	nodeID - The node ID to find package siblings for.
//
// Outputs:
//
//	[]string - File paths of sibling files (not including the node's file).
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackageSiblings(nodeID string) []string {
	node, ok := hg.Graph.GetNode(nodeID)
	if !ok || node.Symbol == nil {
		return []string{}
	}

	pkg := getNodePackage(node)
	info := hg.packages[pkg]
	if info == nil {
		return []string{}
	}

	result := make([]string, 0, len(info.Files))
	for _, file := range info.Files {
		if file != node.Symbol.FilePath {
			result = append(result, file)
		}
	}
	return result
}

// =============================================================================
// Package Dependency Methods
// =============================================================================

// GetPackageDependencies returns packages that the given package imports.
//
// Description:
//
//	Finds all packages that have IMPORTS edges from the given package,
//	or CALLS/REFERENCES edges to symbols in other packages.
//
// Inputs:
//
//	pkg - The package path to analyze.
//
// Outputs:
//
//	[]string - Package paths that this package depends on.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackageDependencies(pkg string) []string {
	deps := make(map[string]bool)

	for _, edge := range hg.crossPackageEdges {
		fromNode, ok := hg.Graph.GetNode(edge.FromID)
		if !ok {
			continue
		}

		if getNodePackage(fromNode) == pkg {
			toNode, ok := hg.Graph.GetNode(edge.ToID)
			if ok {
				targetPkg := getNodePackage(toNode)
				if targetPkg != pkg && targetPkg != "" {
					deps[targetPkg] = true
				}
			}
		}
	}

	result := make([]string, 0, len(deps))
	for dep := range deps {
		result = append(result, dep)
	}
	sort.Strings(result)
	return result
}

// GetPackageDependents returns packages that import the given package.
//
// Description:
//
//	Finds all packages that have incoming edges from the given package
//	(reverse of GetPackageDependencies).
//
// Inputs:
//
//	pkg - The package path to analyze.
//
// Outputs:
//
//	[]string - Package paths that depend on this package.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackageDependents(pkg string) []string {
	dependents := make(map[string]bool)

	for _, edge := range hg.crossPackageEdges {
		toNode, ok := hg.Graph.GetNode(edge.ToID)
		if !ok {
			continue
		}

		if getNodePackage(toNode) == pkg {
			fromNode, ok := hg.Graph.GetNode(edge.FromID)
			if ok {
				sourcePkg := getNodePackage(fromNode)
				if sourcePkg != pkg && sourcePkg != "" {
					dependents[sourcePkg] = true
				}
			}
		}
	}

	result := make([]string, 0, len(dependents))
	for dep := range dependents {
		result = append(result, dep)
	}
	sort.Strings(result)
	return result
}

// =============================================================================
// CRS Integration Methods
// =============================================================================

// GetNodesInPackageWithCRS returns nodes in a package and a TraceStep.
//
// Description:
//
//	Same as GetNodesInPackage but also returns a TraceStep for CRS recording.
//
// Inputs:
//
//	ctx - Context for cancellation (checked but not currently used).
//	pkg - The package path to look up.
//
// Outputs:
//
//	[]*Node - Nodes in the package.
//	crs.TraceStep - TraceStep with query metadata.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetNodesInPackageWithCRS(ctx context.Context, pkg string) ([]*Node, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("graph_query").
			WithTarget("pkg:" + pkg).
			WithTool("GetNodesInPackage").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []*Node{}, step
	}

	nodes := hg.GetNodesInPackage(pkg)

	step := crs.NewTraceStepBuilder().
		WithAction("graph_query").
		WithTarget("pkg:"+pkg).
		WithTool("GetNodesInPackage").
		WithDuration(time.Since(start)).
		WithMetadata("navigation_level", "1").
		WithMetadata("nodes_found", itoa(len(nodes))).
		WithMetadata("package", pkg).
		Build()

	return nodes, step
}

// GetPackagesWithCRS returns package info and a TraceStep.
//
// Inputs:
//
//	ctx - Context for cancellation.
//
// Outputs:
//
//	[]PackageInfo - Package metadata.
//	crs.TraceStep - TraceStep with query metadata.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) GetPackagesWithCRS(ctx context.Context) ([]PackageInfo, crs.TraceStep) {
	start := time.Now()

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("graph_query").
			WithTarget("project").
			WithTool("GetPackages").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []PackageInfo{}, step
	}

	packages := hg.GetPackages()

	step := crs.NewTraceStepBuilder().
		WithAction("graph_query").
		WithTarget("project").
		WithTool("GetPackages").
		WithDuration(time.Since(start)).
		WithMetadata("navigation_level", "0").
		WithMetadata("packages_found", itoa(len(packages))).
		Build()

	return packages, step
}

// DrillDownWithCRS returns children and a TraceStep.
//
// Inputs:
//
//	ctx - Context for cancellation.
//	level - The current hierarchy level.
//	id - The entity ID.
//
// Outputs:
//
//	[]*Node - Child nodes at the next level.
//	crs.TraceStep - TraceStep with navigation metadata.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) DrillDownWithCRS(ctx context.Context, level int, id string) ([]*Node, crs.TraceStep) {
	start := time.Now()

	target := id
	if target == "" {
		target = "project"
	}

	// CR-5 FIX: Check context cancellation
	if ctx != nil && ctx.Err() != nil {
		step := crs.NewTraceStepBuilder().
			WithAction("drill_down").
			WithTarget(target).
			WithTool("DrillDown").
			WithDuration(time.Since(start)).
			WithError(ctx.Err().Error()).
			Build()
		return []*Node{}, step
	}

	nodes := hg.DrillDown(level, id)

	step := crs.NewTraceStepBuilder().
		WithAction("drill_down").
		WithTarget(target).
		WithTool("DrillDown").
		WithDuration(time.Since(start)).
		WithMetadata("from_level", itoa(level)).
		WithMetadata("to_level", itoa(level+1)).
		WithMetadata("from_level_name", LevelName(level)).
		WithMetadata("to_level_name", LevelName(level+1)).
		WithMetadata("children_found", itoa(len(nodes))).
		Build()

	return nodes, step
}

// =============================================================================
// GR-60: File Classification Methods
// =============================================================================

// SetFileClassification stores a computed file classification on the graph.
//
// Description:
//
//	Called after ClassifyFiles() to attach the classification to the graph.
//	Subsequent calls to IsProductionFile() will use this classification
//	instead of the heuristic fallback.
//
// Inputs:
//
//	fc - The file classification to store. May be nil (clears classification).
//
// Thread Safety: NOT safe for concurrent use with IsProductionFile reads.
// Must be called during initialization (RegisterExploreTools) before any
// tool execution begins. After this call, IsProductionFile is safe for
// concurrent reads.
func (hg *HierarchicalGraph) SetFileClassification(fc *FileClassification) {
	hg.fileClassification = fc
}

// IsProductionFile returns true if the file is classified as production code.
//
// Description:
//
//	If a FileClassification has been set (via SetFileClassification), delegates
//	to fc.IsProduction(filePath). If no classification is set, falls back to
//	heuristic checks: !isTestFilePath(filePath) && !isDocFilePath(filePath).
//	This preserves existing behavior when the classification feature is not used.
//
// Inputs:
//
//	filePath - The file path to check.
//
// Outputs:
//
//	bool - True if the file is production code.
//
// Thread Safety: Safe for concurrent use.
func (hg *HierarchicalGraph) IsProductionFile(filePath string) bool {
	if hg.fileClassification != nil {
		return hg.fileClassification.IsProduction(filePath)
	}
	// Fallback: heuristic-based classification
	return !isTestFilePath(filePath) && !isDocFilePath(filePath)
}

// =============================================================================
// Helper Functions
// =============================================================================

// extractPackage derives package path from file path.
func extractPackage(filePath string) string {
	if filePath == "" {
		return ""
	}
	dir := filepath.Dir(filePath)
	if dir == "." {
		return ""
	}
	return dir
}

// getNodePackage gets the package for a node, deriving from file if needed.
func getNodePackage(node *Node) string {
	if node == nil || node.Symbol == nil {
		return ""
	}
	if node.Symbol.Package != "" {
		return node.Symbol.Package
	}
	return extractPackage(node.Symbol.FilePath)
}

// containsString checks if a slice contains a string.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// itoa converts int to string without importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var b strings.Builder
	var digits []byte
	for i > 0 {
		digits = append(digits, byte('0'+i%10))
		i /= 10
	}
	for j := len(digits) - 1; j >= 0; j-- {
		b.WriteByte(digits[j])
	}
	return b.String()
}
