// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package nodes provides concrete DAG node implementations for Trace pipelines.
//
// # Overview
//
// This package contains production-ready nodes that wrap the core Trace
// packages (ast, graph, cache, lint, lsp, patterns, impact, safety). Each node
// implements the dag.Node interface and can be composed into execution pipelines.
//
// # Node Categories
//
// Foundation Nodes (sequential):
//   - ParseFilesNode: Wraps ast.ParserRegistry to parse source files
//   - LoadCacheNode: Wraps cache.GraphCache to load/build cached graphs
//   - BuildGraphNode: Wraps graph.Builder to construct code graphs
//
// Verification Nodes (parallel):
//   - LSPSpawnNode: Spawns language servers via lsp.Manager
//   - LSPTypeCheckNode: Performs type checking via lsp.Operations
//   - LintAnalyzeNode: Runs linters via lint.LintRunner
//   - LintCheckNode: Validates lint results against policies
//   - PatternScanNode: Detects design patterns via patterns.PatternDetector
//
// Analysis Nodes:
//   - BlastRadiusNode: Analyzes change impact via impact.ChangeImpactAnalyzer
//   - SafetyScanNode: Performs security scanning via safety interfaces
//
// Control Flow Nodes:
//   - GateNode: Conditional execution based on previous outputs
//   - TDGNode: Wraps Test-Driven Generation as a sub-DAG
//
// # Usage Example
//
//	// Create foundation nodes
//	parseNode := nodes.NewParseFilesNode(registry)
//	cacheNode := nodes.NewLoadCacheNode(graphCache)
//	graphNode := nodes.NewBuildGraphNode(builder)
//
//	// Create verification nodes (these run in parallel)
//	lspNode := nodes.NewLSPSpawnNode(lspManager)
//	lintNode := nodes.NewLintAnalyzeNode(lintRunner)
//	patternNode := nodes.NewPatternScanNode(detector)
//
//	// Build pipeline
//	pipeline, err := dag.NewBuilder("code-analysis").
//	    AddNode(parseNode).
//	    AddNode(cacheNode).
//	    AddNode(graphNode).
//	    AddNode(lspNode).
//	    AddNode(lintNode).
//	    AddNode(patternNode).
//	    Build()
//
// # Thread Safety
//
// All nodes are safe for concurrent use. Multiple DAG executions can share
// the same node instances. Each Execute() call receives independent input
// and produces independent output.
//
// # Error Handling
//
// Nodes follow Trace error handling conventions:
//   - Return wrapped errors with context (never panic)
//   - Partial results may be returned alongside errors
//   - Context cancellation is always respected
package nodes
