// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package routing

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// =============================================================================
// Mock Router for Testing
// =============================================================================

// mockRouter implements ToolRouter for testing.
type mockRouter struct {
	selectFn func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error)
	model    string
	closed   bool
}

func (m *mockRouter) SelectTool(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
	if m.selectFn != nil {
		return m.selectFn(ctx, query, tools, codeCtx)
	}
	return &ToolSelection{Tool: "default", Confidence: 0.9}, nil
}

func (m *mockRouter) Model() string {
	if m.model != "" {
		return m.model
	}
	return "mock-model"
}

func (m *mockRouter) Close() error {
	m.closed = true
	return nil
}

// =============================================================================
// EscalatingRouter Tests
// =============================================================================

func TestEscalatingRouter_HighConfidence_NoEscalation(t *testing.T) {
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{Tool: "find_callers", Confidence: 0.95}, nil
		},
		model: "granite4:micro-h",
	}
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			t.Error("escalation should not be called when primary is confident")
			return nil, nil
		},
		model: "granite3.3:8b",
	}

	router := NewEscalatingRouter(primary, escalation, 0.7, makeTestSpecs(55), 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "who calls parseConfig", makeTestSpecs(10), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_callers" {
		t.Errorf("expected tool find_callers, got %q", sel.Tool)
	}
	if sel.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", sel.Confidence)
	}
}

func TestEscalatingRouter_LowConfidence_Escalation(t *testing.T) {
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{Tool: "find_callers", Confidence: 0.4}, nil
		},
		model: "granite4:micro-h",
	}

	var escalationToolCount int
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			escalationToolCount = len(tools)
			return &ToolSelection{Tool: "find_implementations", Confidence: 0.85}, nil
		},
		model: "granite3.3:8b",
	}

	allSpecs := makeTestSpecs(55)
	router := NewEscalatingRouter(primary, escalation, 0.7, allSpecs, 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "what extends Router", makeTestSpecs(10), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_implementations" {
		t.Errorf("expected escalation tool find_implementations, got %q", sel.Tool)
	}
	if sel.Confidence != 0.85 {
		t.Errorf("expected escalation confidence 0.85, got %f", sel.Confidence)
	}
	if escalationToolCount != 55 {
		t.Errorf("expected escalation to receive all 55 tools, got %d", escalationToolCount)
	}
}

func TestEscalatingRouter_EscalationFails_ReturnsPrimary(t *testing.T) {
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{Tool: "find_callers", Confidence: 0.5}, nil
		},
	}
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return nil, errors.New("model unavailable")
		},
	}

	router := NewEscalatingRouter(primary, escalation, 0.7, makeTestSpecs(55), 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "test query", makeTestSpecs(10), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to primary result
	if sel.Tool != "find_callers" {
		t.Errorf("expected primary tool find_callers on escalation failure, got %q", sel.Tool)
	}
	if sel.Confidence != 0.5 {
		t.Errorf("expected primary confidence 0.5, got %f", sel.Confidence)
	}
}

func TestEscalatingRouter_NilEscalation_Passthrough(t *testing.T) {
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{Tool: "find_callers", Confidence: 0.3}, nil
		},
	}

	// No escalation router (nil) — should always return primary result
	router := NewEscalatingRouter(primary, nil, 0.7, nil, 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "test query", makeTestSpecs(10), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_callers" {
		t.Errorf("expected primary tool find_callers, got %q", sel.Tool)
	}
}

func TestEscalatingRouter_EscalationTimeout(t *testing.T) {
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{Tool: "find_callers", Confidence: 0.3}, nil
		},
	}
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			// Simulate slow escalation — wait for context cancellation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &ToolSelection{Tool: "find_implementations", Confidence: 0.9}, nil
			}
		},
	}

	// Very short escalation timeout
	router := NewEscalatingRouter(primary, escalation, 0.7, makeTestSpecs(55), 50*time.Millisecond, slog.Default())

	sel, err := router.SelectTool(context.Background(), "test query", makeTestSpecs(10), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should fall back to primary result due to timeout
	if sel.Tool != "find_callers" {
		t.Errorf("expected primary tool on timeout, got %q", sel.Tool)
	}
}

func TestEscalatingRouter_Model_Close(t *testing.T) {
	primary := &mockRouter{model: "granite4:micro-h"}
	escalation := &mockRouter{model: "granite3.3:8b"}

	router := NewEscalatingRouter(primary, escalation, 0.7, nil, 3*time.Second, slog.Default())

	// Model() should return primary model
	if router.Model() != "granite4:micro-h" {
		t.Errorf("expected primary model, got %q", router.Model())
	}

	// Close() should close both
	if err := router.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !primary.closed {
		t.Error("expected primary router to be closed")
	}
	if !escalation.closed {
		t.Error("expected escalation router to be closed")
	}
}

func TestEscalatingRouter_PrimaryError_PropagatesDirectly(t *testing.T) {
	expectedErr := errors.New("primary connection failed")
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return nil, expectedErr
		},
	}
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			t.Error("escalation should not be called when primary errors")
			return nil, nil
		},
	}

	router := NewEscalatingRouter(primary, escalation, 0.7, makeTestSpecs(55), 3*time.Second, slog.Default())

	_, err := router.SelectTool(context.Background(), "test", makeTestSpecs(10), nil)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected primary error to propagate, got %v", err)
	}
}

func TestEscalatingRouter_DefaultThreshold(t *testing.T) {
	router := NewEscalatingRouter(&mockRouter{}, nil, 0, nil, 0, nil)
	if router.threshold != 0.7 {
		t.Errorf("expected default threshold 0.7, got %f", router.threshold)
	}
	if router.escalationTimeout != 3*time.Second {
		t.Errorf("expected default timeout 3s, got %v", router.escalationTimeout)
	}
}

// =============================================================================
// CB-62 Rev 2: Prefilter Miss Recovery Tests
// =============================================================================

func TestEscalatingRouter_PrefilterMiss_DirectUse(t *testing.T) {
	// Primary returns a tool NOT in the candidate set, but with high confidence.
	// EscalatingRouter should use it directly (0ms overhead).
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{
				Tool:          "find_implementations",
				Confidence:    0.95,
				PrefilterMiss: true,
				RawModelPick:  "find_implementations",
			}, nil
		},
	}
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			t.Error("escalation should not be called for high-confidence prefilter miss")
			return nil, nil
		},
	}

	allSpecs := makeTestSpecs(55) // includes find_implementations
	router := NewEscalatingRouter(primary, escalation, 0.7, allSpecs, 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "what classes extend Light", makeTestSpecs(3), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_implementations" {
		t.Errorf("expected tool find_implementations, got %q", sel.Tool)
	}
	if sel.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", sel.Confidence)
	}
	if !sel.PrefilterMiss {
		t.Error("expected PrefilterMiss=true on direct use")
	}
}

func TestEscalatingRouter_PrefilterMiss_Escalation(t *testing.T) {
	// Primary returns a tool NOT in the candidate set, with LOW confidence.
	// EscalatingRouter should escalate to the larger model.
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{
				Tool:          "find_implementations",
				Confidence:    0.60,
				PrefilterMiss: true,
				RawModelPick:  "find_implementations",
			}, nil
		},
	}

	var escalationToolCount int
	escalation := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			escalationToolCount = len(tools)
			return &ToolSelection{Tool: "find_implementations", Confidence: 0.90}, nil
		},
	}

	allSpecs := makeTestSpecs(55)
	router := NewEscalatingRouter(primary, escalation, 0.7, allSpecs, 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "what classes extend Light", makeTestSpecs(3), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_implementations" {
		t.Errorf("expected escalation tool find_implementations, got %q", sel.Tool)
	}
	if escalationToolCount != 55 {
		t.Errorf("expected escalation to receive all 55 tools, got %d", escalationToolCount)
	}
}

func TestEscalatingRouter_PrefilterMiss_Hallucination(t *testing.T) {
	// Primary returns a tool that doesn't exist anywhere — hallucination.
	// EscalatingRouter should return an error.
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{
				Tool:          "nonexistent_magic_tool",
				Confidence:    0.95,
				PrefilterMiss: true,
				RawModelPick:  "nonexistent_magic_tool",
			}, nil
		},
	}

	allSpecs := makeTestSpecs(55)
	router := NewEscalatingRouter(primary, nil, 0.7, allSpecs, 3*time.Second, slog.Default())

	_, err := router.SelectTool(context.Background(), "test query", makeTestSpecs(3), nil)
	if err == nil {
		t.Fatal("expected error for hallucinated tool name")
	}
}

func TestEscalatingRouter_PrefilterMiss_NoEscalation(t *testing.T) {
	// Primary returns a prefilter miss with low confidence, but no escalation
	// router is configured. Should return primary pick (best effort).
	primary := &mockRouter{
		selectFn: func(ctx context.Context, query string, tools []ToolSpec, codeCtx *CodeContext) (*ToolSelection, error) {
			return &ToolSelection{
				Tool:          "find_implementations",
				Confidence:    0.60,
				PrefilterMiss: true,
				RawModelPick:  "find_implementations",
			}, nil
		},
	}

	allSpecs := makeTestSpecs(55)
	// No escalation router (nil)
	router := NewEscalatingRouter(primary, nil, 0.7, allSpecs, 3*time.Second, slog.Default())

	sel, err := router.SelectTool(context.Background(), "what extends Light", makeTestSpecs(3), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Tool != "find_implementations" {
		t.Errorf("expected primary tool find_implementations (best effort), got %q", sel.Tool)
	}
	if sel.Confidence != 0.60 {
		t.Errorf("expected primary confidence 0.60, got %f", sel.Confidence)
	}
}
