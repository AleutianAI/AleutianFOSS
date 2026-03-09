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
	"testing"
	"time"
)

// mockCRSRecorder captures recorded steps for test assertions.
type mockCRSRecorder struct {
	steps []mockStep
}

type mockStep struct {
	toolName    string
	resultCount int
	duration    time.Duration
	err         error
}

func (m *mockCRSRecorder) RecordToolStep(_ context.Context, toolName string, resultCount int, duration time.Duration, err error) {
	m.steps = append(m.steps, mockStep{
		toolName:    toolName,
		resultCount: resultCount,
		duration:    duration,
		err:         err,
	})
}

func TestNopCRSRecorder_NoPanic(t *testing.T) {
	nop := &NopCRSRecorder{}

	// Should not panic with any inputs
	nop.RecordToolStep(context.Background(), "test_tool", 0, 0, nil)
	nop.RecordToolStep(context.Background(), "test_tool", 5, time.Second, fmt.Errorf("test error"))
	nop.RecordToolStep(nil, "", -1, -1, nil)
}

func TestMockCRSRecorder_Records(t *testing.T) {
	mock := &mockCRSRecorder{}

	t.Run("records success step", func(t *testing.T) {
		mock.RecordToolStep(context.Background(), "find_code_smells", 5, 100*time.Millisecond, nil)

		if len(mock.steps) != 1 {
			t.Fatalf("expected 1 step, got %d", len(mock.steps))
		}
		if mock.steps[0].toolName != "find_code_smells" {
			t.Errorf("expected tool name 'find_code_smells', got '%s'", mock.steps[0].toolName)
		}
		if mock.steps[0].resultCount != 5 {
			t.Errorf("expected 5 results, got %d", mock.steps[0].resultCount)
		}
		if mock.steps[0].err != nil {
			t.Errorf("expected nil error, got %v", mock.steps[0].err)
		}
	})

	t.Run("records error step", func(t *testing.T) {
		testErr := fmt.Errorf("test error")
		mock.RecordToolStep(context.Background(), "find_duplication", 0, 50*time.Millisecond, testErr)

		if len(mock.steps) != 2 {
			t.Fatalf("expected 2 steps, got %d", len(mock.steps))
		}
		if mock.steps[1].err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestCRSRecorderInterface_Satisfaction(t *testing.T) {
	// Verify NopCRSRecorder satisfies CRSRecorder interface
	var _ CRSRecorder = &NopCRSRecorder{}

	// Verify mockCRSRecorder satisfies CRSRecorder interface
	var _ CRSRecorder = &mockCRSRecorder{}
}
