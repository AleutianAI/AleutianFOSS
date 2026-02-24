// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package agent

import "errors"

// Sentinel errors for the agent package.
var (
	// ErrInvalidTransition indicates an invalid state transition was attempted.
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrSessionNotFound indicates the requested session does not exist.
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionTerminated indicates the session is already in a terminal state.
	ErrSessionTerminated = errors.New("session already terminated")

	// ErrSessionInProgress indicates an operation is already in progress.
	ErrSessionInProgress = errors.New("session operation in progress")

	// ErrMaxStepsExceeded indicates the maximum step limit was reached.
	ErrMaxStepsExceeded = errors.New("maximum steps exceeded")

	// ErrMaxTokensExceeded indicates the token budget was exhausted.
	ErrMaxTokensExceeded = errors.New("token budget exhausted")

	// ErrTimeout indicates an operation timed out.
	ErrTimeout = errors.New("operation timed out")

	// ErrCanceled indicates the operation was canceled via context.
	ErrCanceled = errors.New("operation canceled")

	// ErrInitFailed indicates Trace initialization failed.
	ErrInitFailed = errors.New("code buddy initialization failed")

	// ErrToolNotFound indicates the requested tool does not exist.
	ErrToolNotFound = errors.New("tool not found")

	// ErrToolExecutionFailed indicates a tool execution failed.
	ErrToolExecutionFailed = errors.New("tool execution failed")

	// ErrLLMUnavailable indicates the LLM service is unavailable.
	ErrLLMUnavailable = errors.New("LLM service unavailable")

	// ErrSafetyBlocked indicates a safety check blocked the operation.
	ErrSafetyBlocked = errors.New("operation blocked by safety check")

	// ErrInvalidSession indicates the session configuration is invalid.
	ErrInvalidSession = errors.New("invalid session configuration")

	// ErrEmptyQuery indicates the query is empty.
	ErrEmptyQuery = errors.New("query must not be empty")

	// ErrNotInClarifyState indicates Continue was called but state is not CLARIFY.
	ErrNotInClarifyState = errors.New("session not in CLARIFY state")

	// ErrAwaitingClarification indicates the agent is waiting for user clarification.
	// NOTE: This is used for flow control in the agent loop, not as a true error.
	// The loop returns this when it needs to pause for user input via Continue().
	ErrAwaitingClarification = errors.New("awaiting user clarification")
)
