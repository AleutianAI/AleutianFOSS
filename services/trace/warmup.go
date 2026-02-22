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

import (
	"sync/atomic"
)

// warmupStatus tracks whether the main LLM model has completed warming up.
// 0 = not complete, 1 = complete.
// This is a package-level variable that can be set from cmd/trace/main.go
// and checked from handlers.go.
var warmupStatus atomic.Int32

// IsWarmupComplete returns true if the main model warmup has finished.
//
// Description:
//
//	Checks the global warmup status. This is used by the /ready endpoint
//	to return 503 Service Unavailable until warmup completes.
//
// Thread Safety: This function is safe for concurrent use.
func IsWarmupComplete() bool {
	return warmupStatus.Load() == 1
}

// MarkWarmupComplete marks the warmup as complete.
//
// Description:
//
//	Called from cmd/trace/main.go after model warmup completes (success or failure).
//	After this is called, the /ready endpoint will return 200 OK.
//
// Thread Safety: This function is safe for concurrent use.
func MarkWarmupComplete() {
	warmupStatus.Store(1)
}

// ResetWarmupStatus resets the warmup status to incomplete.
//
// Description:
//
//	Used for testing to reset the warmup state between tests.
//
// Thread Safety: This function is safe for concurrent use.
func ResetWarmupStatus() {
	warmupStatus.Store(0)
}
