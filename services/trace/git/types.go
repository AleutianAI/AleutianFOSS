// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package git provides git-aware cache invalidation for Trace.
//
// # Description
//
// This package wraps git command execution to proactively invalidate
// the code graph cache when git operations change the working tree.
// It classifies commands by impact (full/targeted/none) and coordinates
// with the cache system to ensure stale data is never used.
//
// # Thread Safety
//
// GitAwareExecutor is safe for concurrent use.
package git

// InvalidationType indicates how much of the cache should be invalidated.
type InvalidationType int

const (
	// InvalidationNone means the command doesn't affect the working tree.
	// Examples: git status, git diff, git log, git show
	InvalidationNone InvalidationType = iota

	// InvalidationTargeted means specific files changed.
	// Examples: git add <files>, git restore <files>, git checkout -- <files>
	InvalidationTargeted

	// InvalidationFull means the entire working tree may have changed.
	// Examples: git checkout <branch>, git merge, git pull, git reset --hard
	InvalidationFull
)

// String returns a human-readable name for the invalidation type.
func (t InvalidationType) String() string {
	switch t {
	case InvalidationNone:
		return "none"
	case InvalidationTargeted:
		return "targeted"
	case InvalidationFull:
		return "full"
	default:
		return "unknown"
	}
}

// ExecuteResult contains the outcome of a git command execution.
//
// # Description
//
// Returned by GitAwareExecutor.Execute with command output and
// information about cache invalidation performed.
//
// # Fields
//
//   - Output: Combined stdout/stderr from the git command.
//   - ExitCode: Process exit code (0 = success).
//   - InvalidationType: What kind of cache invalidation was performed.
//   - FilesInvalidated: For targeted invalidation, which files were affected.
type ExecuteResult struct {
	Output           string
	ExitCode         int
	InvalidationType InvalidationType
	FilesInvalidated []string
}

// CacheInvalidator is the interface for cache invalidation.
//
// # Description
//
// Implemented by the graph cache to allow git executor to trigger
// invalidation after commands that modify the working tree.
type CacheInvalidator interface {
	// InvalidateAll clears the entire cache.
	// Called after full invalidation commands (checkout, merge, etc.)
	InvalidateAll() error

	// InvalidateFiles invalidates cache entries for specific files.
	// Called after targeted invalidation commands (add, restore).
	InvalidateFiles(paths []string) error

	// WaitForRebuilds waits for any in-flight cache rebuilds to complete.
	// Called before invalidation to avoid race conditions.
	WaitForRebuilds() error
}

// ExecutorConfig configures the GitAwareExecutor behavior.
//
// # Description
//
// Allows customization of working directory and invalidation behavior.
//
// # Fields
//
//   - WorkDir: Git repository root directory.
//   - Cache: Cache invalidator (may be nil to skip invalidation).
//   - InvalidationTimeout: Max time to wait for cache rebuilds before forcing invalidation.
type ExecutorConfig struct {
	WorkDir             string
	Cache               CacheInvalidator
	InvalidationTimeout int // seconds, default 30
}

// DefaultExecutorConfig returns a config with sensible defaults.
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		WorkDir:             ".",
		InvalidationTimeout: 30,
	}
}
