// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package phases

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CB-31d: Symbol Resolution Metrics
//
// Description:
//
//	Prometheus metrics for tracking symbol resolution performance and accuracy.
//	Provides observability into resolution strategy effectiveness, cache hit rates,
//	and resolution latency/confidence distributions.
//
// CLAUDE.md Compliance:
//
//	Section 9.4: Metrics naming convention - trace_<metric>_<unit>
//	Section 9.4: Required metrics for every service (counters, histograms)
var (
	// symbolResolutionAttempts tracks total resolution attempts by strategy.
	//
	// Labels:
	//   - strategy: Resolution strategy used (exact, name, name_disambiguated,
	//               name_ambiguous, fuzzy, fuzzy_ambiguous, failed)
	//
	// Use: Track which strategies are being used most often and success rates.
	symbolResolutionAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "trace_symbol_resolution_attempts_total",
		Help: "Total symbol resolution attempts by strategy",
	}, []string{"strategy"})

	// symbolResolutionDuration tracks time spent resolving symbols.
	//
	// Buckets: 1ms, 5ms, 10ms, 50ms, 100ms (target: <10ms P99)
	//
	// Use: Identify performance bottlenecks in symbol resolution.
	symbolResolutionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_symbol_resolution_duration_seconds",
		Help:    "Symbol resolution duration in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1},
	})

	// symbolResolutionConfidence tracks confidence scores.
	//
	// Buckets: 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0
	//
	// Use: Understand resolution quality - higher confidence = more accurate.
	symbolResolutionConfidence = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "trace_symbol_resolution_confidence",
		Help:    "Symbol resolution confidence scores",
		Buckets: []float64{0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 1.0},
	})

	// symbolCacheHits tracks successful cache retrievals.
	//
	// Use: Monitor cache effectiveness (target: >80% hit rate).
	symbolCacheHits = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trace_symbol_cache_hits_total",
		Help: "Total symbol cache hits",
	})

	// symbolCacheMisses tracks cache misses requiring resolution.
	//
	// Use: Complement to cache hits - calculate hit rate.
	symbolCacheMisses = promauto.NewCounter(prometheus.CounterOpts{
		Name: "trace_symbol_cache_misses_total",
		Help: "Total symbol cache misses",
	})
)
