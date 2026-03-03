// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package patterns

import (
	"context"
	"fmt"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/agent/mcts/crs"
)

// CRSRecorder abstracts CRS step recording for tool implementations.
//
// # Description
//
// CRSRecorder provides a simplified interface for pattern analysis tools
// to record their executions in the CRS. Tool executions are recorded as
// system-level actions using ActorSystem.
//
// # Thread Safety
//
// Implementations must be safe for concurrent use.
type CRSRecorder interface {
	// RecordToolStep records a tool execution step in the CRS.
	RecordToolStep(ctx context.Context, toolName string, resultCount int, duration time.Duration, err error)
}

// CRSBridge implements CRSRecorder using a real CRS instance.
//
// # Description
//
// CRSBridge wraps a CRS instance to record tool executions and generate
// CDCL clauses on error or degraded results. It uses ActorSystem since
// tool executions are system-level actions.
//
// # Thread Safety
//
// This type is safe for concurrent use (delegates to thread-safe CRS).
type CRSBridge struct {
	crsInstance crs.CRS
	sessionID   string
}

// NewCRSBridge creates a CRS bridge for tool step recording.
//
// # Inputs
//
//   - crsInstance: The CRS to record steps in.
//   - sessionID: Session ID for step attribution.
//
// # Outputs
//
//   - *CRSBridge: Configured bridge.
func NewCRSBridge(crsInstance crs.CRS, sessionID string) *CRSBridge {
	return &CRSBridge{
		crsInstance: crsInstance,
		sessionID:   sessionID,
	}
}

// RecordToolStep records a tool execution step and generates clauses as needed.
//
// # Description
//
// Records the step in the CRS step history. On error, generates a
// session-scoped CDCL clause with FailureType=ToolError. On empty results,
// generates a soft signal clause indicating the parameters may be wrong.
//
// # Inputs
//
//   - ctx: Context for cancellation.
//   - toolName: Name of the tool that executed.
//   - resultCount: Number of results returned.
//   - duration: Execution duration.
//   - err: Error from tool execution (nil on success).
func (b *CRSBridge) RecordToolStep(ctx context.Context, toolName string, resultCount int, duration time.Duration, err error) {
	outcome := crs.OutcomeSuccess
	var errMsg string
	var errCat crs.ErrorCategory

	if err != nil {
		outcome = crs.OutcomeFailure
		errMsg = err.Error()
		errCat = crs.ErrorCategoryInternal
	}

	step := crs.StepRecord{
		Timestamp:     time.Now().UnixMilli(),
		SessionID:     b.sessionID,
		Actor:         crs.ActorSystem,
		Decision:      crs.DecisionExecuteTool,
		Tool:          toolName,
		Outcome:       outcome,
		ErrorMessage:  errMsg,
		ErrorCategory: errCat,
		DurationMs:    duration.Milliseconds(),
		ResultSummary: fmt.Sprintf("%d results", resultCount),
	}

	_ = b.crsInstance.RecordStep(ctx, step)

	// Generate CDCL clause on error
	if err != nil {
		clause := &crs.Clause{
			ID: fmt.Sprintf("tool_error_%s_%d", toolName, time.Now().UnixMilli()),
			Literals: []crs.Literal{
				{Variable: fmt.Sprintf("tool:%s", toolName), Negated: true},
				{Variable: fmt.Sprintf("error:%s", errCat), Negated: true},
			},
			Source:      crs.SignalSourceHard,
			LearnedAt:   time.Now().UnixMilli(),
			FailureType: crs.FailureTypeToolError,
			SessionID:   b.sessionID,
		}
		_ = b.crsInstance.AddClause(ctx, clause)
	}

	// Generate soft signal clause on empty results (may indicate wrong parameters)
	if err == nil && resultCount == 0 {
		clause := &crs.Clause{
			ID: fmt.Sprintf("empty_result_%s_%d", toolName, time.Now().UnixMilli()),
			Literals: []crs.Literal{
				{Variable: fmt.Sprintf("tool:%s", toolName), Negated: false},
				{Variable: "outcome:empty", Negated: true},
			},
			Source:      crs.SignalSourceSoft,
			LearnedAt:   time.Now().UnixMilli(),
			FailureType: crs.FailureTypeInvalidOutput,
			SessionID:   b.sessionID,
		}
		_ = b.crsInstance.AddClause(ctx, clause)
	}
}

// NopCRSRecorder is a no-op implementation for tests and standalone usage.
//
// # Description
//
// NopCRSRecorder discards all step recordings without error. Use this
// when CRS integration is not needed (tests, standalone tool usage).
//
// # Thread Safety
//
// This type is safe for concurrent use (stateless).
type NopCRSRecorder struct{}

// RecordToolStep is a no-op.
func (n *NopCRSRecorder) RecordToolStep(_ context.Context, _ string, _ int, _ time.Duration, _ error) {
}
