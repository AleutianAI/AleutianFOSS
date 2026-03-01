// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

/*
Package eval provides the evaluation framework for Trace components.

# Overview

The eval package is the foundation for proving correctness, measuring performance,
and comparing algorithm implementations in Trace. Every component that participates
in the CRS (Code Reasoning State) system implements the Evaluable interface, enabling:

  - Property-based correctness verification
  - Performance benchmarking with statistical analysis
  - A/B testing between algorithm variants
  - Chaos injection for resilience testing
  - Regression detection for CI/CD gates

# Architecture

	┌─────────────────────────────────────────────────────────────────────────────┐
	│                         EVALUATION FRAMEWORK                                 │
	├─────────────────────────────────────────────────────────────────────────────┤
	│                                                                              │
	│  ┌─────────────────────────────────────────────────────────────────────┐    │
	│  │                         Registry                                     │    │
	│  │  • Stores all Evaluable components                                   │    │
	│  │  • Provides lookup by name                                           │    │
	│  │  • Manages lifecycle                                                 │    │
	│  └─────────────────────────────────────────────────────────────────────┘    │
	│                                    │                                         │
	│         ┌──────────────────────────┼──────────────────────────┐             │
	│         │                          │                          │             │
	│         ▼                          ▼                          ▼             │
	│  ┌─────────────┐           ┌─────────────┐           ┌─────────────┐        │
	│  │ Correctness │           │  Benchmark  │           │  A/B Test   │        │
	│  │  Verifier   │           │   Runner    │           │   Harness   │        │
	│  └─────────────┘           └─────────────┘           └─────────────┘        │
	│         │                          │                          │             │
	│         ▼                          ▼                          ▼             │
	│  ┌─────────────┐           ┌─────────────┐           ┌─────────────┐        │
	│  │  Property   │           │   Metrics   │           │  Statistics │        │
	│  │   Tests     │           │   Export    │           │   Analysis  │        │
	│  └─────────────┘           └─────────────┘           └─────────────┘        │
	│                                                                              │
	└─────────────────────────────────────────────────────────────────────────────┘

# The Evaluable Interface

Every component that can be evaluated implements Evaluable:

	type Evaluable interface {
	    Name() string
	    Properties() []Property
	    Metrics() []MetricDefinition
	    HealthCheck(ctx context.Context) error
	}

# Property-Based Testing

Properties define invariants that must hold for all inputs:

	property := Property{
	    Name: "no_soft_signal_clauses",
	    Description: "CDCL only learns from compiler/test failures",
	    Check: func(input, output any) error {
	        // Verify the invariant
	    },
	    Generator: func() any {
	        // Generate random valid input
	    },
	}

The Verifier runs properties against generated inputs:

	verifier := correctness.NewVerifier(registry)
	result, err := verifier.Verify(ctx, "cdcl", correctness.WithIterations(10000))

# Usage Example

	// Register a component
	registry := eval.NewRegistry()
	registry.Register(myAlgorithm)

	// Verify correctness
	verifier := correctness.NewVerifier(registry)
	result, err := verifier.Verify(ctx, "my_algorithm")
	if !result.Passed {
	    log.Fatalf("Property %s failed: %v", result.FailedProperty, result.FailingInput)
	}

# Thread Safety

All types in this package are safe for concurrent use unless otherwise documented.
The Registry uses read-write locks for concurrent access. Verifiers can run
multiple property checks in parallel.

# Signal Sources

The eval package enforces the hard/soft signal boundary:

	const (
	    SourceCompiler  SignalSource = iota  // Hard signal - can update state
	    SourceTest                            // Hard signal - can update state
	    SourceTypeCheck                       // Hard signal - can update state
	    SourceLinter                          // Hard signal - can update state
	    SourceLLM                             // Soft signal - guidance only
	    SourceHeuristic                       // Soft signal - guidance only
	)

Properties can verify that components respect this boundary.
*/
package eval
