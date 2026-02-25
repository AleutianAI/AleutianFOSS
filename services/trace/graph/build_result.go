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

import "fmt"

// FileError represents a failure to process a single file during graph building.
type FileError struct {
	// FilePath is the path to the file that failed.
	FilePath string

	// Err is the underlying error.
	Err error
}

// Error implements the error interface.
func (e FileError) Error() string {
	return fmt.Sprintf("file %s: %v", e.FilePath, e.Err)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e FileError) Unwrap() error {
	return e.Err
}

// EdgeError represents a failure to create a single edge during graph building.
type EdgeError struct {
	// FromID is the source node ID.
	FromID string

	// ToID is the target node ID.
	ToID string

	// EdgeType is the type of edge that failed to create.
	EdgeType EdgeType

	// Err is the underlying error.
	Err error
}

// Error implements the error interface.
func (e EdgeError) Error() string {
	return fmt.Sprintf("edge %s -[%s]-> %s: %v", e.FromID, e.EdgeType, e.ToID, e.Err)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e EdgeError) Unwrap() error {
	return e.Err
}

// BuildStats contains statistics about a build operation.
type BuildStats struct {
	// FilesProcessed is the number of files successfully processed.
	FilesProcessed int

	// FilesFailed is the number of files that failed processing.
	FilesFailed int

	// NodesCreated is the number of nodes added to the graph.
	NodesCreated int

	// EdgesCreated is the number of edges added to the graph.
	EdgesCreated int

	// PlaceholderNodes is the number of placeholder nodes created for
	// external/unresolved symbols.
	PlaceholderNodes int

	// AmbiguousResolves is the number of call resolutions that matched
	// multiple symbols (over-approximated by creating edges to all).
	AmbiguousResolves int

	// GoInterfaceEdges is the number of EdgeTypeImplements edges created
	// via Go interface implementation detection (method-set matching).
	// See GR-40 for details.
	GoInterfaceEdges int

	// CallEdgesResolved is the number of EdgeTypeCalls edges where the
	// target was successfully resolved to an existing symbol.
	// See GR-41 for details.
	CallEdgesResolved int

	// CallEdgesUnresolved is the number of EdgeTypeCalls edges where the
	// target could not be resolved and a placeholder was created.
	// See GR-41 for details.
	CallEdgesUnresolved int

	// NamedImportEdgesResolved is the number of EdgeTypeReferences edges
	// created by the named import resolution pass (GR-62). Each represents
	// a "from X import Y" statement where Y was resolved to an in-project
	// symbol node.
	NamedImportEdgesResolved int

	// CommonJSImportEdgesResolved is the number of EdgeTypeReferences edges
	// created by the CommonJS import alias resolution pass (IT-06d Bug D).
	// Each represents a "var X = require('./module')" statement where the
	// imported module's exported class was found and linked.
	CommonJSImportEdgesResolved int

	// DynamicImportEdgesResolved is the number of EdgeTypeReferences edges
	// created by the dynamic import() resolution pass (IT-06e Bug 4).
	// Each represents an import('./module') call where the dynamically loaded
	// module's exported class was found and linked to the importing file.
	DynamicImportEdgesResolved int

	// DecoratorArgEdgesResolved is the number of EdgeTypeReferences edges
	// created by the decorator argument resolution pass (IT-06e Bug 5).
	// Each represents a class name in a @Module({providers: [X]}) or
	// @NgModule({imports: [X]}) decorator array that resolved to an in-project symbol.
	DecoratorArgEdgesResolved int

	// DurationMilli is the total build time in milliseconds.
	// NOTE: For fast builds (< 1ms), this rounds to 0. Use DurationMicro for precision.
	DurationMilli int64

	// DurationMicro is the total build time in microseconds (for sub-millisecond precision).
	DurationMicro int64
}

// BuildResult contains the result of a graph build operation.
//
// Build operations are designed to be resilient: individual file failures
// do not fail the entire build. Instead, partial results are returned
// along with error information.
type BuildResult struct {
	// Graph is the constructed graph. May be partial if errors occurred
	// or the build was cancelled.
	Graph *Graph

	// FileErrors contains errors for files that failed processing.
	// Files in this list are not represented in the graph.
	FileErrors []FileError

	// EdgeErrors contains errors for edges that couldn't be created.
	// The graph may still contain valid edges despite these errors.
	EdgeErrors []EdgeError

	// Stats contains build statistics.
	Stats BuildStats

	// Incomplete is true if the build was cancelled (via context) or
	// stopped due to memory limits. When true, the graph contains
	// partial results.
	Incomplete bool
}

// HasErrors returns true if any file or edge errors occurred.
func (r *BuildResult) HasErrors() bool {
	return len(r.FileErrors) > 0 || len(r.EdgeErrors) > 0
}

// TotalErrors returns the total number of errors (file + edge).
func (r *BuildResult) TotalErrors() int {
	return len(r.FileErrors) + len(r.EdgeErrors)
}

// Success returns true if the build completed without errors and is complete.
func (r *BuildResult) Success() bool {
	return !r.Incomplete && !r.HasErrors()
}
