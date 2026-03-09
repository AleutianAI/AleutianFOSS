// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/graph"
)

// CircularDepType categorizes circular dependencies.
type CircularDepType string

const (
	// CircularDepPackage is a package-level circular dependency.
	CircularDepPackage CircularDepType = "package"

	// CircularDepType represents a type-level circular dependency.
	CircularDepTypeLevel CircularDepType = "type"

	// CircularDepFunction is a function-level circular dependency.
	CircularDepFunction CircularDepType = "function"
)

// CircularDepFinder finds circular dependencies in code.
//
// # Description
//
// CircularDepFinder detects circular dependencies at package, type,
// and function levels. It uses Tarjan's algorithm for efficient
// strongly connected component detection.
//
// # Thread Safety
//
// This type is safe for concurrent use.
type CircularDepFinder struct {
	graph         *graph.Graph
	projectModule string
	crs           CRSRecorder
	packageGraph  *PackageGraph
	mu            sync.RWMutex
	built         bool
}

// NewCircularDepFinder creates a new circular dependency finder.
//
// # Description
//
// Creates a finder that analyzes the code graph for circular dependencies.
// The package graph is built lazily on first query.
//
// # Inputs
//
//   - g: The code graph to analyze.
//   - projectModule: The module path (e.g., "github.com/user/repo").
//
// # Outputs
//
//   - *CircularDepFinder: Configured finder.
func NewCircularDepFinder(g *graph.Graph, projectModule string) *CircularDepFinder {
	return &CircularDepFinder{
		graph:         g,
		projectModule: projectModule,
		crs:           &NopCRSRecorder{},
		built:         false,
	}
}

// SetCRS configures CRS recording for this finder.
func (c *CircularDepFinder) SetCRS(recorder CRSRecorder) {
	c.crs = recorder
}

// BuildPackageGraph builds the package-level dependency graph.
//
// # Description
//
// Derives package dependencies from file imports. Called automatically
// on first query but can be called explicitly for better control.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//
// # Outputs
//
//   - error: Non-nil on failure.
func (c *CircularDepFinder) BuildPackageGraph(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.packageGraph = BuildPackageGraph(c.graph, c.projectModule)
	c.built = true

	return nil
}

// FindCircularDeps finds circular dependencies.
//
// # Description
//
// Finds all circular dependencies in the specified scope.
// Supports package, type, and function level detection.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - scope: Package path prefix to filter (empty = all).
//   - depType: Type of circular dependency to find.
//
// # Outputs
//
//   - []CircularDep: Found circular dependencies.
//   - error: Non-nil on failure.
//
// # Example
//
//	finder := NewCircularDepFinder(graph, "github.com/user/repo")
//	deps, err := finder.FindCircularDeps(ctx, "pkg/", CircularDepPackage)
func (c *CircularDepFinder) FindCircularDeps(
	ctx context.Context,
	scope string,
	depType CircularDepType,
) ([]CircularDep, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}

	start := time.Now()
	ctx, span := startCircularDepsSpan(ctx, scope, depType)
	defer span.End()

	var result []CircularDep
	var err error

	switch depType {
	case CircularDepPackage:
		result, err = c.findPackageCircularDeps(ctx, scope)
	case CircularDepTypeLevel:
		result, err = c.findTypeCircularDeps(ctx, scope)
	case CircularDepFunction:
		result, err = c.findFunctionCircularDeps(ctx, scope)
	default:
		err = fmt.Errorf("unknown circular dependency type: %s", depType)
	}

	dur := time.Since(start)
	setCircularDepsSpanResult(span, len(result), err)
	recordCircularDepsMetrics(ctx, dur, len(result), err)
	c.crs.RecordToolStep(ctx, "find_circular_deps", len(result), dur, err)

	return result, err
}

// findPackageCircularDeps finds package-level circular dependencies.
func (c *CircularDepFinder) findPackageCircularDeps(ctx context.Context, scope string) ([]CircularDep, error) {
	// Build package graph if needed
	c.mu.RLock()
	built := c.built
	c.mu.RUnlock()

	if !built {
		if err := c.BuildPackageGraph(ctx); err != nil {
			return nil, err
		}
	}

	c.mu.RLock()
	cycles := c.packageGraph.FindCycles()
	c.mu.RUnlock()

	var results []CircularDep

	for _, cycle := range cycles {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Filter by scope
		if scope != "" {
			inScope := false
			for _, pkg := range cycle {
				if strings.HasPrefix(pkg, scope) || strings.Contains(pkg, "/"+scope) {
					inScope = true
					break
				}
			}
			if !inScope {
				continue
			}
		}

		// Create circular dependency with cycle going back to start
		fullCycle := append(cycle, cycle[0])

		dep := CircularDep{
			Cycle:       fullCycle,
			Type:        string(CircularDepPackage),
			Severity:    c.calculatePackageSeverity(cycle),
			Suggestion:  c.generatePackageSuggestion(cycle),
			BreakPoints: c.findBreakPoints(cycle),
		}

		results = append(results, dep)
	}

	// Sort by severity
	sort.Slice(results, func(i, j int) bool {
		return severityRank(results[i].Severity) > severityRank(results[j].Severity)
	})

	return results, nil
}

// findTypeCircularDeps finds type-level circular dependencies.
func (c *CircularDepFinder) findTypeCircularDeps(ctx context.Context, scope string) ([]CircularDep, error) {
	// Build type dependency graph from the code graph
	typeEdges := make(map[string][]string)

	for _, edge := range c.graph.Edges() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Look for type relationships (implements, embeds, references)
		if edge.Type != graph.EdgeTypeImplements &&
			edge.Type != graph.EdgeTypeEmbeds &&
			edge.Type != graph.EdgeTypeReferences {
			continue
		}

		fromNode, fromOK := c.graph.GetNode(edge.FromID)
		toNode, toOK := c.graph.GetNode(edge.ToID)

		if !fromOK || !toOK {
			continue
		}

		if fromNode.Symbol == nil || toNode.Symbol == nil {
			continue
		}

		// Filter by scope
		if scope != "" && !strings.HasPrefix(fromNode.Symbol.FilePath, scope) {
			continue
		}

		typeEdges[edge.FromID] = append(typeEdges[edge.FromID], edge.ToID)
	}

	// Find cycles using Tarjan's algorithm
	cycles := findCyclesInGraph(typeEdges)

	var results []CircularDep

	for _, cycle := range cycles {
		// Get readable names
		names := make([]string, len(cycle))
		for i, id := range cycle {
			if node, ok := c.graph.GetNode(id); ok && node.Symbol != nil {
				names[i] = node.Symbol.Name
			} else {
				names[i] = id
			}
		}
		names = append(names, names[0]) // Close the cycle

		dep := CircularDep{
			Cycle:       names,
			Type:        string(CircularDepTypeLevel),
			Severity:    SeverityWarning,
			Suggestion:  "Consider breaking the cycle by extracting an interface or moving shared types",
			BreakPoints: cycle,
		}

		results = append(results, dep)
	}

	return results, nil
}

// findFunctionCircularDeps finds function-level circular dependencies.
func (c *CircularDepFinder) findFunctionCircularDeps(ctx context.Context, scope string) ([]CircularDep, error) {
	// Build call graph
	callEdges := make(map[string][]string)

	for _, edge := range c.graph.Edges() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if edge.Type != graph.EdgeTypeCalls {
			continue
		}

		fromNode, fromOK := c.graph.GetNode(edge.FromID)
		if !fromOK || fromNode.Symbol == nil {
			continue
		}

		// Filter by scope
		if scope != "" && !strings.HasPrefix(fromNode.Symbol.FilePath, scope) {
			continue
		}

		callEdges[edge.FromID] = append(callEdges[edge.FromID], edge.ToID)
	}

	// Find cycles
	cycles := findCyclesInGraph(callEdges)

	var results []CircularDep

	for _, cycle := range cycles {
		// Get readable names
		names := make([]string, len(cycle))
		for i, id := range cycle {
			if node, ok := c.graph.GetNode(id); ok && node.Symbol != nil {
				names[i] = node.Symbol.Name
			} else {
				names[i] = id
			}
		}
		names = append(names, names[0]) // Close the cycle

		dep := CircularDep{
			Cycle:      names,
			Type:       string(CircularDepFunction),
			Severity:   c.calculateFunctionSeverity(cycle),
			Suggestion: c.generateFunctionSuggestion(cycle),
		}

		results = append(results, dep)
	}

	return results, nil
}

// findCyclesInGraph finds all cycles using Tarjan's SCC algorithm.
func findCyclesInGraph(edges map[string][]string) [][]string {
	index := 0
	stack := make([]string, 0)
	onStack := make(map[string]bool)
	indices := make(map[string]int)
	lowlinks := make(map[string]int)
	var sccs [][]string

	var strongConnect func(v string)
	strongConnect = func(v string) {
		indices[v] = index
		lowlinks[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range edges[v] {
			if _, visited := indices[w]; !visited {
				strongConnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}

		if lowlinks[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				sccs = append(sccs, scc)
			}
		}
	}

	for v := range edges {
		if _, visited := indices[v]; !visited {
			strongConnect(v)
		}
	}

	return sccs
}

// calculatePackageSeverity determines severity based on cycle characteristics.
func (c *CircularDepFinder) calculatePackageSeverity(cycle []string) Severity {
	// Longer cycles are worse
	if len(cycle) > 5 {
		return SeverityError
	}

	// Check if internal packages are involved
	hasInternal := false
	for _, pkg := range cycle {
		if strings.Contains(pkg, "/internal/") {
			hasInternal = true
			break
		}
	}

	if hasInternal {
		return SeverityWarning
	}

	if len(cycle) > 3 {
		return SeverityWarning
	}

	return SeverityInfo
}

// generatePackageSuggestion creates a suggestion for breaking package cycles.
func (c *CircularDepFinder) generatePackageSuggestion(cycle []string) string {
	if len(cycle) == 2 {
		return fmt.Sprintf(
			"Consider extracting shared types from %s and %s into a common package",
			shortPkgName(cycle[0]), shortPkgName(cycle[1]),
		)
	}

	return fmt.Sprintf(
		"Consider introducing an interface in one of the packages to break the cycle, "+
			"or merge the tightly coupled packages. The cycle involves %d packages.",
		len(cycle),
	)
}

// findBreakPoints identifies the best places to break a cycle.
func (c *CircularDepFinder) findBreakPoints(cycle []string) []string {
	if len(cycle) < 2 {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.packageGraph == nil {
		return nil
	}

	// Find the edge with the fewest dependencies to suggest breaking
	type edgeCount struct {
		from  string
		to    string
		count int
	}

	var edges []edgeCount

	for i := 0; i < len(cycle); i++ {
		from := cycle[i]
		to := cycle[(i+1)%len(cycle)]

		fromNode, exists := c.packageGraph.GetPackage(from)
		if !exists {
			continue
		}

		// Count how many imports go from -> to
		count := 0
		for _, imp := range fromNode.Imports {
			if imp == to {
				count++
			}
		}

		edges = append(edges, edgeCount{from: from, to: to, count: count})
	}

	// Sort by count (ascending) - easier to break edges with fewer imports
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].count < edges[j].count
	})

	if len(edges) > 0 {
		return []string{
			fmt.Sprintf("Remove dependency: %s → %s", shortPkgName(edges[0].from), shortPkgName(edges[0].to)),
		}
	}

	return nil
}

// calculateFunctionSeverity determines severity for function cycles.
func (c *CircularDepFinder) calculateFunctionSeverity(cycle []string) Severity {
	// Direct recursion (self-cycle) is common and usually intentional
	if len(cycle) == 1 {
		return SeverityInfo
	}

	// Mutual recursion (2 functions) can be intentional
	if len(cycle) == 2 {
		return SeverityInfo
	}

	// Larger cycles are more concerning
	if len(cycle) > 5 {
		return SeverityWarning
	}

	return SeverityInfo
}

// generateFunctionSuggestion creates a suggestion for function cycles.
func (c *CircularDepFinder) generateFunctionSuggestion(cycle []string) string {
	if len(cycle) <= 2 {
		return "Mutual recursion detected. This may be intentional; verify it terminates properly"
	}

	return fmt.Sprintf(
		"Complex call cycle involving %d functions. Consider refactoring to reduce coupling",
		len(cycle),
	)
}

// shortPkgName extracts the short name from a package path.
func shortPkgName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// severityRank returns a numeric rank for sorting.
func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// Summary generates a summary of circular dependency findings.
func (c *CircularDepFinder) Summary(deps []CircularDep) string {
	if len(deps) == 0 {
		return "No circular dependencies detected"
	}

	pkgCount := 0
	typeCount := 0
	funcCount := 0

	for _, dep := range deps {
		switch CircularDepType(dep.Type) {
		case CircularDepPackage:
			pkgCount++
		case CircularDepTypeLevel:
			typeCount++
		case CircularDepFunction:
			funcCount++
		}
	}

	parts := make([]string, 0)
	if pkgCount > 0 {
		parts = append(parts, fmt.Sprintf("%d package", pkgCount))
	}
	if typeCount > 0 {
		parts = append(parts, fmt.Sprintf("%d type", typeCount))
	}
	if funcCount > 0 {
		parts = append(parts, fmt.Sprintf("%d function", funcCount))
	}

	return fmt.Sprintf("Found %d circular dependency cycles: %s",
		len(deps), strings.Join(parts, ", "))
}

// GetPackageGraph returns the underlying package graph.
func (c *CircularDepFinder) GetPackageGraph() *PackageGraph {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.packageGraph
}
