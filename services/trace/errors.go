// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package trace

import "errors"

// Sentinel errors for the Code Buddy service.
var (
	// ErrGraphNotInitialized indicates no graph has been built for the project.
	ErrGraphNotInitialized = errors.New("graph not initialized")

	// ErrGraphExpired indicates the cached graph has been evicted.
	ErrGraphExpired = errors.New("graph expired")

	// ErrRelativePath indicates the project root was a relative path.
	ErrRelativePath = errors.New("project root must be absolute path")

	// ErrPathTraversal indicates path contains .. traversal sequences.
	ErrPathTraversal = errors.New("path contains traversal sequences")

	// ErrProjectTooLarge indicates the project exceeds size limits.
	ErrProjectTooLarge = errors.New("project exceeds size limits")

	// ErrInitInProgress indicates another init is already running for this project.
	ErrInitInProgress = errors.New("initialization in progress")

	// ErrInitTimeout indicates the init operation timed out.
	ErrInitTimeout = errors.New("initialization timed out")
)
