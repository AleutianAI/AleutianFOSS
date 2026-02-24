// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package benchmark provides performance benchmarking for evaluable components.
//
// # Overview
//
// The benchmark package enables systematic performance measurement of Trace's
// algorithms and components. It integrates with the eval framework to provide
// standardized benchmarking with statistical rigor.
//
// # Architecture
//
//	┌─────────────────────────────────────────────────────────────────────────┐
//	│                         Benchmark Framework                              │
//	├─────────────────────────────────────────────────────────────────────────┤
//	│                                                                          │
//	│  ┌──────────────┐      ┌──────────────┐      ┌──────────────┐          │
//	│  │   Runner     │──────│   Metrics    │──────│   Reporter   │          │
//	│  │              │      │   Collector  │      │              │          │
//	│  │ • Warmup     │      │ • Latency    │      │ • JSON       │          │
//	│  │ • Iterations │      │ • Throughput │      │ • Console    │          │
//	│  │ • Cooldown   │      │ • Memory     │      │ • Prometheus │          │
//	│  └──────────────┘      └──────────────┘      └──────────────┘          │
//	│         │                     │                     │                   │
//	│         └─────────────────────┴─────────────────────┘                   │
//	│                               │                                         │
//	│                               ▼                                         │
//	│                    ┌──────────────────────┐                            │
//	│                    │    BenchmarkResult    │                            │
//	│                    │                       │                            │
//	│                    │ • Name                │                            │
//	│                    │ • Iterations          │                            │
//	│                    │ • Duration            │                            │
//	│                    │ • LatencyStats        │                            │
//	│                    │ • MemoryStats         │                            │
//	│                    │ • ThroughputStats     │                            │
//	│                    └──────────────────────┘                            │
//	│                                                                          │
//	└─────────────────────────────────────────────────────────────────────────┘
//
// # Usage
//
// Basic benchmarking:
//
//	runner := benchmark.NewRunner(registry)
//	result, err := runner.Run(ctx, "cdcl",
//	    benchmark.WithIterations(1000),
//	    benchmark.WithWarmup(100),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("P99 latency: %v\n", result.Latency.P99)
//
// With custom input generator:
//
//	result, err := runner.Run(ctx, "cdcl",
//	    benchmark.WithInputGenerator(func() any {
//	        return generateRandomClauses(100)
//	    }),
//	)
//
// Comparing algorithms:
//
//	comparison, err := runner.Compare(ctx,
//	    []string{"cdcl_v1", "cdcl_v2"},
//	    benchmark.WithIterations(10000),
//	)
//	if comparison.Winner != "" {
//	    fmt.Printf("%s is %.2fx faster\n", comparison.Winner, comparison.Speedup)
//	}
//
// # Statistical Rigor
//
// The benchmark package provides:
//   - Percentile calculations (P50, P90, P95, P99, P999)
//   - Standard deviation and variance
//   - Confidence intervals
//   - Statistical significance tests for comparisons
//   - Outlier detection and removal
//
// # Thread Safety
//
// All types in this package are safe for concurrent use unless documented otherwise.
package benchmark
