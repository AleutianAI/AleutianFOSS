// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package dag provides a DAG-based execution framework for Trace pipelines.
//
// The DAG (Directed Acyclic Graph) framework enables:
//   - Parallel execution of independent pipeline nodes
//   - Explicit dependencies between operations
//   - Unified tracing via OpenTelemetry
//   - Checkpointing for resume after failure
//   - Composable pipeline construction
//
// # Thread Safety
//
// All exported types are safe for concurrent use.
//
// # Example
//
//	// Create nodes with dependencies declared internally
//	parseNode := dag.NewFuncNode("PARSE_FILES", nil, parseFn)
//	graphNode := dag.NewFuncNode("BUILD_GRAPH", []string{"PARSE_FILES"}, graphFn)
//	lintNode := dag.NewFuncNode("LINT_CODE", []string{"BUILD_GRAPH"}, lintFn)
//
//	// Build DAG
//	pipeline, err := dag.NewBuilder("my-pipeline").
//	    AddNode(parseNode).
//	    AddNode(graphNode).
//	    AddNode(lintNode).
//	    Build()
//
//	// Execute
//	executor, err := dag.NewExecutor(pipeline, logger)
//	result, err := executor.Run(ctx, input)
package dag
