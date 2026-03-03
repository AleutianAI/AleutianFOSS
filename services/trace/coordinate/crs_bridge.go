// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package coordinate

import (
	"context"
	"time"
)

// CRSRecorder abstracts CRS step recording for coordinate tool implementations.
//
// # Description
//
// CRSRecorder provides a simplified interface for coordinate analysis tools
// to record their executions in the CRS. This interface mirrors
// reason.CRSRecorder and patterns.CRSRecorder — Go duck typing means any
// implementation satisfying one automatically satisfies the others.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type CRSRecorder interface {
	// RecordToolStep records a tool execution step in the CRS.
	RecordToolStep(ctx context.Context, toolName string, resultCount int, duration time.Duration, err error)
}

// NopCRSRecorder is a no-op implementation for tests and standalone usage.
//
// # Thread Safety
//
// This type is safe for concurrent use (stateless).
type NopCRSRecorder struct{}

// RecordToolStep is a no-op.
func (n *NopCRSRecorder) RecordToolStep(_ context.Context, _ string, _ int, _ time.Duration, _ error) {
}
