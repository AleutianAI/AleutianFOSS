// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package tdg

import "time"

// =============================================================================
// STATE
// =============================================================================

// State represents a state in the TDG state machine.
type State string

const (
	// StateIdle is the initial state before TDG starts.
	StateIdle State = "idle"

	// StateUnderstand analyzes the bug/feature request.
	StateUnderstand State = "understand"

	// StateWriteTest generates a reproducer test.
	StateWriteTest State = "write_test"

	// StateVerifyFail runs the test to confirm it fails.
	StateVerifyFail State = "verify_fail"

	// StateWriteFix generates the fix.
	StateWriteFix State = "write_fix"

	// StateVerifyPass runs the test to confirm the fix works.
	StateVerifyPass State = "verify_pass"

	// StateRegression runs the full test suite.
	StateRegression State = "regression"

	// StateDone indicates successful completion.
	StateDone State = "done"

	// StateFailed indicates TDG failed (max retries, timeout, etc).
	StateFailed State = "failed"
)

// String returns the string representation of the state.
func (s State) String() string {
	return string(s)
}

// IsTerminal returns true if the state is terminal (done or failed).
func (s State) IsTerminal() bool {
	return s == StateDone || s == StateFailed
}

// IsActive returns true if the state allows continued execution.
func (s State) IsActive() bool {
	switch s {
	case StateUnderstand, StateWriteTest, StateVerifyFail,
		StateWriteFix, StateVerifyPass, StateRegression:
		return true
	default:
		return false
	}
}

// AllStates returns all valid TDG states.
func AllStates() []State {
	return []State{
		StateIdle,
		StateUnderstand,
		StateWriteTest,
		StateVerifyFail,
		StateWriteFix,
		StateVerifyPass,
		StateRegression,
		StateDone,
		StateFailed,
	}
}

// =============================================================================
// REQUEST & RESULT
// =============================================================================

// Request contains the input for a TDG session.
type Request struct {
	// BugDescription describes the bug to fix or feature to implement.
	BugDescription string `json:"bug_description"`

	// ProjectRoot is the root directory of the project.
	ProjectRoot string `json:"project_root"`

	// Language is the programming language (go, python, typescript).
	Language string `json:"language"`

	// GraphID is an optional existing Trace graph ID.
	GraphID string `json:"graph_id,omitempty"`

	// TargetFile is an optional hint for which file contains the bug.
	TargetFile string `json:"target_file,omitempty"`

	// TargetFunction is an optional hint for which function has the bug.
	TargetFunction string `json:"target_function,omitempty"`
}

// Validate checks that the request has required fields.
func (r *Request) Validate() error {
	if r.BugDescription == "" {
		return ErrEmptyRequest
	}
	if r.ProjectRoot == "" {
		return ErrEmptyRequest
	}
	if r.Language == "" {
		return ErrEmptyRequest
	}
	return nil
}

// Result contains the outcome of a TDG session.
type Result struct {
	// Success indicates if TDG completed successfully.
	Success bool `json:"success"`

	// State is the final TDG state.
	State State `json:"final_state"`

	// ReproducerTest is the generated test that reproduces the bug.
	ReproducerTest *TestCase `json:"reproducer_test,omitempty"`

	// AppliedPatches are the code changes that were applied.
	AppliedPatches []*Patch `json:"applied_patches,omitempty"`

	// TestResults is the final test execution result.
	TestResults *TestResult `json:"test_results,omitempty"`

	// RegressionResults is the full suite test result.
	RegressionResults *TestResult `json:"regression_results,omitempty"`

	// Error contains the error message if TDG failed.
	Error string `json:"error,omitempty"`

	// Duration is how long the TDG session took.
	Duration time.Duration `json:"duration"`

	// Metrics contains execution metrics.
	Metrics *Metrics `json:"metrics"`
}

// =============================================================================
// CONTEXT
// =============================================================================

// Context holds the state during a TDG session.
//
// This is the internal working state, not to be confused with context.Context.
type Context struct {
	// SessionID uniquely identifies this TDG session.
	SessionID string

	// State is the current TDG state.
	State State

	// Request is the original request.
	Request *Request

	// ReproducerTest is the generated reproducer test.
	ReproducerTest *TestCase

	// ProposedPatches are the current proposed code changes.
	ProposedPatches []*Patch

	// AppliedPatches are patches that have been applied to disk.
	AppliedPatches []*Patch

	// TestRetries is the number of test generation attempts.
	TestRetries int

	// FixRetries is the number of fix generation attempts.
	FixRetries int

	// RegressionFixes is the number of regression fix attempts.
	RegressionFixes int

	// LastTestOutput is the output from the last test execution.
	LastTestOutput string

	// LastError is the last error encountered.
	LastError error

	// StartTime is when the TDG session started (Unix milliseconds UTC).
	StartTime int64

	// Metrics tracks execution metrics.
	Metrics *Metrics
}

// NewContext creates a new TDG context for a request.
func NewContext(sessionID string, req *Request) *Context {
	return &Context{
		SessionID: sessionID,
		State:     StateIdle,
		Request:   req,
		Metrics:   &Metrics{},
		StartTime: time.Now().UnixMilli(),
	}
}

// Elapsed returns the time since the session started.
func (c *Context) Elapsed() time.Duration {
	return time.Duration(time.Now().UnixMilli()-c.StartTime) * time.Millisecond
}

// =============================================================================
// TEST CASE
// =============================================================================

// TestCase represents a generated test.
type TestCase struct {
	// Name is the test function name (e.g., TestValidateToken_NilClaims).
	Name string `json:"name"`

	// FilePath is the path where the test file should be written.
	FilePath string `json:"file_path"`

	// Content is the full test file content.
	Content string `json:"content"`

	// Language is the programming language.
	Language string `json:"language"`

	// TestType is "unit" or "integration".
	TestType string `json:"test_type"`

	// PackagePath is the package/module path for running tests.
	PackagePath string `json:"package_path,omitempty"`
}

// Validate checks that the test case is complete.
func (tc *TestCase) Validate() error {
	if tc.Name == "" || tc.FilePath == "" || tc.Content == "" || tc.Language == "" {
		return ErrInvalidTestCase
	}
	return nil
}

// =============================================================================
// PATCH
// =============================================================================

// Patch represents a code change to apply.
type Patch struct {
	// FilePath is the file to modify.
	FilePath string `json:"file_path"`

	// OldContent is the original file content (for rollback).
	OldContent string `json:"old_content,omitempty"`

	// NewContent is the new file content.
	NewContent string `json:"new_content"`

	// Applied indicates if the patch has been applied to disk.
	Applied bool `json:"applied"`
}

// Validate checks that the patch is complete.
func (p *Patch) Validate() error {
	if p.FilePath == "" || p.NewContent == "" {
		return ErrInvalidPatch
	}
	return nil
}

// =============================================================================
// TEST RESULT
// =============================================================================

// TestResult contains the outcome of test execution.
type TestResult struct {
	// Passed indicates if all tests passed.
	Passed bool `json:"passed"`

	// Output is the captured stdout/stderr.
	Output string `json:"output"`

	// Duration is how long test execution took.
	Duration time.Duration `json:"duration"`

	// ExitCode is the exit code from the test process.
	ExitCode int `json:"exit_code"`

	// FailedTests lists the names of failed tests.
	FailedTests []string `json:"failed_tests,omitempty"`

	// PassedTests lists the names of passed tests.
	PassedTests []string `json:"passed_tests,omitempty"`

	// TotalTests is the total number of tests run.
	TotalTests int `json:"total_tests"`

	// Truncated indicates if output was truncated due to size limits.
	Truncated bool `json:"truncated"`

	// TimedOut indicates if execution was killed due to timeout.
	TimedOut bool `json:"timed_out"`
}

// =============================================================================
// METRICS
// =============================================================================

// Metrics tracks execution statistics for a TDG session.
type Metrics struct {
	// TestAttempts is the number of test generation attempts.
	TestAttempts int `json:"test_attempts"`

	// FixAttempts is the number of fix generation attempts.
	FixAttempts int `json:"fix_attempts"`

	// RegressionFixes is the number of regression fix attempts.
	RegressionFixes int `json:"regression_fixes"`

	// TestsRun is the total number of test executions.
	TestsRun int `json:"tests_run"`

	// LLMCalls is the number of LLM API calls.
	LLMCalls int `json:"llm_calls"`

	// TotalTestDuration is the cumulative time spent running tests.
	TotalTestDuration time.Duration `json:"total_test_duration"`

	// ContextTokens is the total context tokens assembled.
	ContextTokens int `json:"context_tokens"`
}

// =============================================================================
// HISTORY
// =============================================================================

// HistoryEntry records a step in the TDG session.
type HistoryEntry struct {
	// Timestamp is when this entry was recorded (Unix milliseconds UTC).
	Timestamp int64 `json:"timestamp"`

	// State is the TDG state at this step.
	State State `json:"state"`

	// Action describes what happened.
	Action string `json:"action"`

	// Details contains additional information.
	Details string `json:"details,omitempty"`

	// Duration is how long this step took.
	Duration time.Duration `json:"duration,omitempty"`

	// Error contains any error from this step.
	Error string `json:"error,omitempty"`
}
