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
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StalenessCheckSampleSize is the number of files to spot-check per staleness check.
// Checking 50 files keeps the check under 50ms on typical systems.
const StalenessCheckSampleSize = 50

// StalenessCheckCacheTTL is how long a staleness result is cached before rechecking.
const StalenessCheckCacheTTL = 60 * time.Second

// StalenessErrorThreshold is the percentage of changed files that triggers an error
// log suggesting a graph rebuild. Below this, a warning is logged.
const StalenessErrorThreshold = 0.20

// StalenessResult contains the outcome of a staleness check.
//
// Thread Safety: Immutable after construction.
type StalenessResult struct {
	// IsStale is true if any files have changed since graph build.
	IsStale bool

	// ChangedFileCount is the number of files detected as changed.
	ChangedFileCount int

	// TotalFileCount is the total number of files checked.
	TotalFileCount int

	// PercentChanged is the ratio of changed files to total files (0.0-1.0).
	PercentChanged float64

	// CheckedAt is when this result was computed.
	CheckedAt time.Time

	// SampleBased is true if only a subset of files was checked.
	SampleBased bool
}

// StalenessChecker detects whether the code graph is stale relative to the
// current working tree.
//
// Description:
//
//	Records file modification times at build time and spot-checks a sample
//	of files at query time. Results are cached for StalenessCheckCacheTTL
//	(60s) to avoid repeated stat syscalls.
//
// Thread Safety: Safe for concurrent use. Uses sync.RWMutex for cache.
type StalenessChecker struct {
	projectRoot string
	fileMtimes  map[string]int64 // relative path → mtime (Unix seconds)

	mu           sync.RWMutex
	cachedResult *StalenessResult
	cachedAt     time.Time
}

// NewStalenessChecker creates a staleness checker from a graph's recorded file mtimes.
//
// Inputs:
//   - projectRoot: Absolute path to the project root.
//   - fileMtimes: Map of relative file path → mtime (Unix seconds) recorded at build time.
//
// Outputs:
//   - *StalenessChecker: The checker, or nil if fileMtimes is nil/empty.
//
// Thread Safety: Safe for concurrent use after construction.
func NewStalenessChecker(projectRoot string, fileMtimes map[string]int64) *StalenessChecker {
	if len(fileMtimes) == 0 {
		return nil
	}
	return &StalenessChecker{
		projectRoot: projectRoot,
		fileMtimes:  fileMtimes,
	}
}

// Check performs a staleness check, returning a cached result if available.
//
// Description:
//
//	If a cached result exists and is younger than StalenessCheckCacheTTL,
//	returns it immediately. Otherwise, spot-checks up to StalenessCheckSampleSize
//	files from the build-time mtime map against their current mtimes.
//
// Outputs:
//   - StalenessResult: The check outcome.
//
// Thread Safety: Safe for concurrent use.
func (sc *StalenessChecker) Check() StalenessResult {
	// Check cache first
	sc.mu.RLock()
	if sc.cachedResult != nil && time.Since(sc.cachedAt) < StalenessCheckCacheTTL {
		result := *sc.cachedResult
		sc.mu.RUnlock()
		return result
	}
	sc.mu.RUnlock()

	// Perform fresh check
	result := sc.checkNow()

	// Cache the result
	sc.mu.Lock()
	sc.cachedResult = &result
	sc.cachedAt = time.Now()
	sc.mu.Unlock()

	return result
}

// InvalidateCache forces the next Check() to recompute staleness.
//
// Thread Safety: Safe for concurrent use.
func (sc *StalenessChecker) InvalidateCache() {
	sc.mu.Lock()
	sc.cachedResult = nil
	sc.mu.Unlock()
}

// checkNow performs the actual file stat checks.
func (sc *StalenessChecker) checkNow() StalenessResult {
	totalFiles := len(sc.fileMtimes)
	if totalFiles == 0 {
		return StalenessResult{
			IsStale:        false,
			CheckedAt:      time.Now(),
			TotalFileCount: 0,
		}
	}

	// Decide whether to check all files or sample
	filesToCheck := sc.selectFilesToCheck()
	sampleBased := len(filesToCheck) < totalFiles

	changedCount := 0
	for _, relPath := range filesToCheck {
		buildMtime := sc.fileMtimes[relPath]
		absPath := filepath.Join(sc.projectRoot, relPath)

		info, err := os.Stat(absPath)
		if err != nil {
			// File deleted or inaccessible — counts as changed
			changedCount++
			continue
		}

		currentMtime := info.ModTime().Unix()
		if currentMtime != buildMtime {
			changedCount++
		}
	}

	percentChanged := float64(changedCount) / float64(len(filesToCheck))

	result := StalenessResult{
		IsStale:          changedCount > 0,
		ChangedFileCount: changedCount,
		TotalFileCount:   len(filesToCheck),
		PercentChanged:   percentChanged,
		CheckedAt:        time.Now(),
		SampleBased:      sampleBased,
	}

	// Log based on severity
	if changedCount > 0 {
		if percentChanged > StalenessErrorThreshold {
			slog.Error("GRAPH-STALE: Significant divergence detected, suggest graph rebuild",
				slog.Int("changed_files", changedCount),
				slog.Int("checked_files", len(filesToCheck)),
				slog.Float64("percent_changed", percentChanged*100),
				slog.Bool("sample_based", sampleBased),
			)
		} else {
			slog.Warn("GRAPH-STALE: Files changed since graph build",
				slog.Int("changed_files", changedCount),
				slog.Int("checked_files", len(filesToCheck)),
				slog.Float64("percent_changed", percentChanged*100),
				slog.Bool("sample_based", sampleBased),
			)
		}
	}

	return result
}

// selectFilesToCheck returns file paths to check, sampling if the total exceeds
// StalenessCheckSampleSize.
func (sc *StalenessChecker) selectFilesToCheck() []string {
	totalFiles := len(sc.fileMtimes)

	// Collect all file paths
	allFiles := make([]string, 0, totalFiles)
	for path := range sc.fileMtimes {
		allFiles = append(allFiles, path)
	}

	if totalFiles <= StalenessCheckSampleSize {
		return allFiles
	}

	// Random sample without replacement
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(allFiles), func(i, j int) {
		allFiles[i], allFiles[j] = allFiles[j], allFiles[i]
	})

	return allFiles[:StalenessCheckSampleSize]
}

// RecordFileMtimes populates a graph's FileMtimes field by stat'ing all files
// referenced in the graph's nodes.
//
// Description:
//
//	Iterates all nodes in the graph, collects unique file paths, stats each
//	file, and records its modification time. Should be called after Freeze().
//
// Inputs:
//   - g: The graph to populate. Must not be nil.
//   - projectRoot: Absolute path to the project root.
//
// Thread Safety: Must be called during build (before concurrent reads).
func RecordFileMtimes(g *Graph, projectRoot string) {
	if g == nil {
		return
	}

	mtimes := make(map[string]int64)

	// Collect unique file paths from graph nodes
	files := make(map[string]struct{})
	for _, node := range g.Nodes() {
		if node.Symbol != nil && node.Symbol.FilePath != "" {
			files[node.Symbol.FilePath] = struct{}{}
		}
	}

	for relPath := range files {
		absPath := filepath.Join(projectRoot, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			continue // Skip files that can't be stat'd
		}
		mtimes[relPath] = info.ModTime().Unix()
	}

	g.FileMtimes = mtimes

	slog.Debug("CRS-19: Recorded file mtimes for staleness detection",
		slog.Int("files", len(mtimes)),
		slog.String("project_root", projectRoot),
	)
}
