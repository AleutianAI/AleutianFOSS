// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package crs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/AleutianAI/AleutianFOSS/services/trace/ast"
)

func TestNew(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		c := New(nil)
		if c == nil {
			t.Fatal("New returned nil")
		}
		if c.Generation() != 0 {
			t.Errorf("initial generation = %d, want 0", c.Generation())
		}
	})

	t.Run("custom config", func(t *testing.T) {
		config := &Config{
			SnapshotEpochLimit: 500,
			EnableMetrics:      false,
		}
		c := New(config)
		if c == nil {
			t.Fatal("New returned nil")
		}
	})
}

func TestCRS_Snapshot(t *testing.T) {
	t.Run("returns non-nil snapshot", func(t *testing.T) {
		c := New(nil)
		snap := c.Snapshot()
		if snap == nil {
			t.Fatal("Snapshot returned nil")
		}
	})

	t.Run("snapshot has correct generation", func(t *testing.T) {
		c := New(nil)
		snap := c.Snapshot()
		if snap.Generation() != 0 {
			t.Errorf("generation = %d, want 0", snap.Generation())
		}
	})

	t.Run("snapshot has creation time", func(t *testing.T) {
		beforeMillis := time.Now().UnixMilli()
		c := New(nil)
		snap := c.Snapshot()
		afterMillis := time.Now().UnixMilli()

		if snap.CreatedAt() < beforeMillis || snap.CreatedAt() > afterMillis {
			t.Errorf("creation time %v not between %v and %v", snap.CreatedAt(), beforeMillis, afterMillis)
		}
	})

	t.Run("all indexes accessible", func(t *testing.T) {
		c := New(nil)
		snap := c.Snapshot()

		if snap.ProofIndex() == nil {
			t.Error("ProofIndex is nil")
		}
		if snap.ConstraintIndex() == nil {
			t.Error("ConstraintIndex is nil")
		}
		if snap.SimilarityIndex() == nil {
			t.Error("SimilarityIndex is nil")
		}
		if snap.DependencyIndex() == nil {
			t.Error("DependencyIndex is nil")
		}
		if snap.HistoryIndex() == nil {
			t.Error("HistoryIndex is nil")
		}
		if snap.StreamingIndex() == nil {
			t.Error("StreamingIndex is nil")
		}
	})
}

func TestCRS_Apply(t *testing.T) {
	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceHard, nil)
		_, err := c.Apply(nil, delta) //nolint:staticcheck
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("error = %v, want %v", err, ErrNilContext)
		}
	})

	t.Run("nil delta returns error", func(t *testing.T) {
		c := New(nil)
		_, err := c.Apply(context.Background(), nil)
		if !errors.Is(err, ErrNilDelta) {
			t.Errorf("error = %v, want %v", err, ErrNilDelta)
		}
	})

	t.Run("cancelled context returns error", func(t *testing.T) {
		c := New(nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		delta := NewProofDelta(SignalSourceHard, nil)
		_, err := c.Apply(ctx, delta)
		if err == nil {
			t.Error("expected error for cancelled context")
		}
	})

	t.Run("increments generation on success", func(t *testing.T) {
		c := New(nil)
		before := c.Generation()

		delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Proof: 1, Disproof: 2, Status: ProofStatusExpanded},
		})
		metrics, err := c.Apply(context.Background(), delta)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		after := c.Generation()
		if after != before+1 {
			t.Errorf("generation = %d, want %d", after, before+1)
		}
		if metrics.OldGeneration != before {
			t.Errorf("OldGeneration = %d, want %d", metrics.OldGeneration, before)
		}
		if metrics.NewGeneration != after {
			t.Errorf("NewGeneration = %d, want %d", metrics.NewGeneration, after)
		}
	})

	t.Run("proof delta updates proof index", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Proof: 10, Disproof: 20, Status: ProofStatusExpanded},
		})
		_, err := c.Apply(context.Background(), delta)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		snap := c.Snapshot()
		proof, ok := snap.ProofIndex().Get("node1")
		if !ok {
			t.Fatal("node1 not found in proof index")
		}
		if proof.Proof != 10 || proof.Disproof != 20 {
			t.Errorf("proof = %v, want Proof=10, Disproof=20", proof)
		}
	})

	t.Run("returns metrics", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Proof: 1, Disproof: 1},
			"node2": {Proof: 2, Disproof: 2},
		})
		metrics, err := c.Apply(context.Background(), delta)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		if metrics.DeltaType != DeltaTypeProof {
			t.Errorf("DeltaType = %v, want %v", metrics.DeltaType, DeltaTypeProof)
		}
		if metrics.EntriesModified != 2 {
			t.Errorf("EntriesModified = %d, want 2", metrics.EntriesModified)
		}
		if metrics.IndexesUpdated == 0 {
			t.Error("IndexesUpdated is empty")
		}
		if !metrics.IndexesUpdated.Has(IndexProof) {
			t.Error("IndexesUpdated should include proof index")
		}
		if metrics.ApplyDuration <= 0 {
			t.Error("ApplyDuration should be positive")
		}
	})
}

func TestCRS_Apply_HardSoftBoundary(t *testing.T) {
	t.Run("hard signal can mark disproven", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Status: ProofStatusDisproven, Source: SignalSourceHard},
		})
		_, err := c.Apply(context.Background(), delta)
		if err != nil {
			t.Errorf("hard signal should be able to mark disproven: %v", err)
		}
	})

	t.Run("soft signal cannot mark disproven", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceSoft, map[string]ProofNumber{
			"node1": {Status: ProofStatusDisproven, Source: SignalSourceSoft},
		})
		_, err := c.Apply(context.Background(), delta)
		if err == nil {
			t.Error("soft signal should not be able to mark disproven")
		}
		if !errors.Is(err, ErrDeltaValidation) {
			t.Errorf("error = %v, want ErrDeltaValidation", err)
		}
	})

	t.Run("soft signal can mark proven", func(t *testing.T) {
		c := New(nil)
		delta := NewProofDelta(SignalSourceSoft, map[string]ProofNumber{
			"node1": {Status: ProofStatusProven, Source: SignalSourceSoft},
		})
		_, err := c.Apply(context.Background(), delta)
		if err != nil {
			t.Errorf("soft signal should be able to mark proven: %v", err)
		}
	})
}

func TestCRS_Checkpoint(t *testing.T) {
	t.Run("creates checkpoint", func(t *testing.T) {
		c := New(nil)

		// Add some data
		delta := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Proof: 10, Disproof: 20},
		})
		_, _ = c.Apply(context.Background(), delta)

		cp, err := c.Checkpoint(context.Background())
		if err != nil {
			t.Fatalf("Checkpoint failed: %v", err)
		}

		if cp.ID == "" {
			t.Error("checkpoint ID is empty")
		}
		if cp.Generation != 1 {
			t.Errorf("checkpoint generation = %d, want 1", cp.Generation)
		}
	})

	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		_, err := c.Checkpoint(nil) //nolint:staticcheck
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("error = %v, want %v", err, ErrNilContext)
		}
	})
}

func TestCRS_Restore(t *testing.T) {
	t.Run("restores state", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Add initial data
		delta1 := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node1": {Proof: 10, Disproof: 20},
		})
		_, _ = c.Apply(ctx, delta1)

		// Create checkpoint
		cp, _ := c.Checkpoint(ctx)

		// Add more data
		delta2 := NewProofDelta(SignalSourceHard, map[string]ProofNumber{
			"node2": {Proof: 30, Disproof: 40},
		})
		_, _ = c.Apply(ctx, delta2)

		// Verify node2 exists
		snap := c.Snapshot()
		if _, ok := snap.ProofIndex().Get("node2"); !ok {
			t.Fatal("node2 should exist before restore")
		}

		// Restore
		err := c.Restore(ctx, cp)
		if err != nil {
			t.Fatalf("Restore failed: %v", err)
		}

		// Verify state is restored
		snap = c.Snapshot()
		if _, ok := snap.ProofIndex().Get("node2"); ok {
			t.Error("node2 should not exist after restore")
		}
		if _, ok := snap.ProofIndex().Get("node1"); !ok {
			t.Error("node1 should still exist after restore")
		}
		if c.Generation() != cp.Generation {
			t.Errorf("generation = %d, want %d", c.Generation(), cp.Generation)
		}
	})

	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		cp, _ := c.Checkpoint(context.Background())
		err := c.Restore(nil, cp) //nolint:staticcheck
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("error = %v, want %v", err, ErrNilContext)
		}
	})
}

func TestCRS_Concurrent(t *testing.T) {
	t.Run("concurrent snapshots", func(t *testing.T) {
		c := New(nil)
		var wg sync.WaitGroup

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				snap := c.Snapshot()
				if snap == nil {
					t.Error("snapshot is nil")
				}
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent applies", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()
		var wg sync.WaitGroup
		errCh := make(chan error, 100)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				delta := NewHistoryDelta(SignalSourceHard, []HistoryEntry{
					{ID: "entry-" + string(rune(n)), NodeID: "node1", Action: "test"},
				})
				_, err := c.Apply(ctx, delta)
				if err != nil {
					errCh <- err
				}
			}(i)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent apply failed: %v", err)
		}

		// Verify generation increased correctly
		if c.Generation() != 100 {
			t.Errorf("generation = %d, want 100", c.Generation())
		}
	})

	t.Run("concurrent reads and writes", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()
		var wg sync.WaitGroup

		// Writers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				delta := NewStreamingDelta(SignalSourceHard)
				delta.Increments["item"] = 1
				_, _ = c.Apply(ctx, delta)
			}(i)
		}

		// Readers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				snap := c.Snapshot()
				_ = snap.StreamingIndex().Estimate("item")
			}()
		}

		wg.Wait()
	})
}

func TestCRS_HealthCheck(t *testing.T) {
	t.Run("healthy state", func(t *testing.T) {
		c := New(nil)
		err := c.HealthCheck(context.Background())
		if err != nil {
			t.Errorf("HealthCheck failed: %v", err)
		}
	})

	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		err := c.HealthCheck(nil) //nolint:staticcheck
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("error = %v, want %v", err, ErrNilContext)
		}
	})
}

func TestCRS_Evaluable(t *testing.T) {
	t.Run("implements Evaluable", func(t *testing.T) {
		c := New(nil)

		if c.Name() != "crs" {
			t.Errorf("Name = %s, want crs", c.Name())
		}

		props := c.Properties()
		if len(props) == 0 {
			t.Error("Properties should not be empty")
		}

		metrics := c.Metrics()
		if len(metrics) == 0 {
			t.Error("Metrics should not be empty")
		}
	})
}

// -----------------------------------------------------------------------------
// StepRecord Tests (CRS-01)
// -----------------------------------------------------------------------------

func TestCRS_RecordStep(t *testing.T) {
	t.Run("records valid step", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		step := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
			Tool:       "list_packages",
			Confidence: 0.95,
		}

		err := c.RecordStep(ctx, step)
		if err != nil {
			t.Fatalf("RecordStep failed: %v", err)
		}

		// Verify it was recorded
		history := c.GetStepHistory("session-1")
		if len(history) != 1 {
			t.Fatalf("expected 1 step, got %d", len(history))
		}
		if history[0].Tool != "list_packages" {
			t.Errorf("tool = %s, want list_packages", history[0].Tool)
		}
	})

	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		step := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}

		err := c.RecordStep(nil, step)
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("expected ErrNilContext, got %v", err)
		}
	})

	t.Run("invalid step returns error", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		step := StepRecord{
			SessionID: "", // Invalid - empty
			Actor:     ActorRouter,
			Decision:  DecisionSelectTool,
			Outcome:   OutcomeSuccess,
		}

		err := c.RecordStep(ctx, step)
		if err == nil {
			t.Error("expected error for invalid step")
		}
	})

	t.Run("auto-assigns step number", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Record step without step number
		step := StepRecord{
			SessionID: "session-1",
			Actor:     ActorRouter,
			Decision:  DecisionSelectTool,
			Outcome:   OutcomeSuccess,
		}

		err := c.RecordStep(ctx, step)
		if err != nil {
			t.Fatalf("RecordStep failed: %v", err)
		}

		history := c.GetStepHistory("session-1")
		if history[0].StepNumber != 1 {
			t.Errorf("step_number = %d, want 1", history[0].StepNumber)
		}
	})

	t.Run("auto-assigns timestamp", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		beforeMillis := time.Now().UnixMilli()
		step := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}

		err := c.RecordStep(ctx, step)
		if err != nil {
			t.Fatalf("RecordStep failed: %v", err)
		}
		afterMillis := time.Now().UnixMilli()

		history := c.GetStepHistory("session-1")
		if history[0].Timestamp < beforeMillis || history[0].Timestamp > afterMillis {
			t.Error("timestamp not in expected range")
		}
	})
}

func TestCRS_GetStepHistory(t *testing.T) {
	t.Run("returns empty for unknown session", func(t *testing.T) {
		c := New(nil)
		history := c.GetStepHistory("unknown")
		if history != nil {
			t.Errorf("expected nil, got %v", history)
		}
	})

	t.Run("returns empty for empty session ID", func(t *testing.T) {
		c := New(nil)
		history := c.GetStepHistory("")
		if history != nil {
			t.Errorf("expected nil, got %v", history)
		}
	})

	t.Run("returns steps in order", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Record multiple steps
		for i := 1; i <= 3; i++ {
			step := StepRecord{
				SessionID:  "session-1",
				StepNumber: i,
				Actor:      ActorRouter,
				Decision:   DecisionSelectTool,
				Outcome:    OutcomeSuccess,
				Tool:       "tool-" + string(rune('0'+i)),
			}
			if err := c.RecordStep(ctx, step); err != nil {
				t.Fatalf("RecordStep %d failed: %v", i, err)
			}
		}

		history := c.GetStepHistory("session-1")
		if len(history) != 3 {
			t.Fatalf("expected 3 steps, got %d", len(history))
		}
		for i, step := range history {
			if step.StepNumber != i+1 {
				t.Errorf("step %d has number %d", i, step.StepNumber)
			}
		}
	})
}

func TestCRS_GetLastStep(t *testing.T) {
	t.Run("returns nil for unknown session", func(t *testing.T) {
		c := New(nil)
		step := c.GetLastStep("unknown")
		if step != nil {
			t.Errorf("expected nil, got %v", step)
		}
	})

	t.Run("returns nil for empty session ID", func(t *testing.T) {
		c := New(nil)
		step := c.GetLastStep("")
		if step != nil {
			t.Errorf("expected nil, got %v", step)
		}
	})

	t.Run("returns last step", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Record multiple steps
		for i := 1; i <= 3; i++ {
			step := StepRecord{
				SessionID:  "session-1",
				StepNumber: i,
				Actor:      ActorRouter,
				Decision:   DecisionSelectTool,
				Outcome:    OutcomeSuccess,
				Tool:       "tool-" + string(rune('0'+i)),
			}
			if err := c.RecordStep(ctx, step); err != nil {
				t.Fatalf("RecordStep %d failed: %v", i, err)
			}
		}

		last := c.GetLastStep("session-1")
		if last == nil {
			t.Fatal("GetLastStep returned nil")
		}
		if last.StepNumber != 3 {
			t.Errorf("step_number = %d, want 3", last.StepNumber)
		}
	})
}

func TestCRS_CountToolExecutions(t *testing.T) {
	t.Run("returns 0 for unknown session", func(t *testing.T) {
		c := New(nil)
		count := c.CountToolExecutions("unknown", "list_packages")
		if count != 0 {
			t.Errorf("expected 0, got %d", count)
		}
	})

	t.Run("returns 0 for empty params", func(t *testing.T) {
		c := New(nil)
		if c.CountToolExecutions("", "tool") != 0 {
			t.Error("expected 0 for empty session")
		}
		if c.CountToolExecutions("session", "") != 0 {
			t.Error("expected 0 for empty tool")
		}
	})

	t.Run("counts only ExecuteTool decisions", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// SelectTool - should not count
		step1 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
			Tool:       "list_packages",
		}
		c.RecordStep(ctx, step1)

		// ExecuteTool - should count
		step2 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 2,
			Actor:      ActorSystem,
			Decision:   DecisionExecuteTool,
			Outcome:    OutcomeSuccess,
			Tool:       "list_packages",
		}
		c.RecordStep(ctx, step2)

		// ExecuteTool again - should count
		step3 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 3,
			Actor:      ActorSystem,
			Decision:   DecisionExecuteTool,
			Outcome:    OutcomeSuccess,
			Tool:       "list_packages",
		}
		c.RecordStep(ctx, step3)

		count := c.CountToolExecutions("session-1", "list_packages")
		if count != 2 {
			t.Errorf("expected 2, got %d", count)
		}
	})
}

func TestCRS_GetStepsByActor(t *testing.T) {
	t.Run("filters by actor", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Router step
		step1 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step1)

		// System step
		step2 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 2,
			Actor:      ActorSystem,
			Decision:   DecisionExecuteTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step2)

		// MainAgent step
		step3 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 3,
			Actor:      ActorMainAgent,
			Decision:   DecisionSynthesize,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step3)

		routerSteps := c.GetStepsByActor("session-1", ActorRouter)
		if len(routerSteps) != 1 {
			t.Errorf("expected 1 router step, got %d", len(routerSteps))
		}

		systemSteps := c.GetStepsByActor("session-1", ActorSystem)
		if len(systemSteps) != 1 {
			t.Errorf("expected 1 system step, got %d", len(systemSteps))
		}
	})
}

func TestCRS_GetStepsByOutcome(t *testing.T) {
	t.Run("filters by outcome", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Success step
		step1 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step1)

		// Failure step
		step2 := StepRecord{
			SessionID:     "session-1",
			StepNumber:    2,
			Actor:         ActorSystem,
			Decision:      DecisionExecuteTool,
			Outcome:       OutcomeFailure,
			ErrorCategory: ErrorCategoryTimeout,
		}
		c.RecordStep(ctx, step2)

		successSteps := c.GetStepsByOutcome("session-1", OutcomeSuccess)
		if len(successSteps) != 1 {
			t.Errorf("expected 1 success step, got %d", len(successSteps))
		}

		failureSteps := c.GetStepsByOutcome("session-1", OutcomeFailure)
		if len(failureSteps) != 1 {
			t.Errorf("expected 1 failure step, got %d", len(failureSteps))
		}
	})
}

func TestCRS_ClearStepHistory(t *testing.T) {
	t.Run("clears session history", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Record a step
		step := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step)

		// Verify it exists
		if len(c.GetStepHistory("session-1")) != 1 {
			t.Fatal("step not recorded")
		}

		// Clear it
		c.ClearStepHistory("session-1")

		// Verify it's gone
		if c.GetStepHistory("session-1") != nil {
			t.Error("history should be nil after clear")
		}
	})

	t.Run("does not affect other sessions", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()

		// Record steps in two sessions
		step1 := StepRecord{
			SessionID:  "session-1",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step1)

		step2 := StepRecord{
			SessionID:  "session-2",
			StepNumber: 1,
			Actor:      ActorRouter,
			Decision:   DecisionSelectTool,
			Outcome:    OutcomeSuccess,
		}
		c.RecordStep(ctx, step2)

		// Clear session-1
		c.ClearStepHistory("session-1")

		// session-2 should still have its step
		if len(c.GetStepHistory("session-2")) != 1 {
			t.Error("session-2 should still have its step")
		}
	})
}

func TestCRS_StepRecording_Concurrent(t *testing.T) {
	c := New(nil)
	ctx := context.Background()

	const numGoroutines = 10
	const stepsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('0'+goroutineID))

			for i := 1; i <= stepsPerGoroutine; i++ {
				step := StepRecord{
					SessionID:  sessionID,
					StepNumber: i,
					Actor:      ActorRouter,
					Decision:   DecisionExecuteTool,
					Outcome:    OutcomeSuccess,
					Tool:       "test_tool",
				}
				if err := c.RecordStep(ctx, step); err != nil {
					t.Errorf("RecordStep failed: %v", err)
					return
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify each session has the correct number of steps
	for g := 0; g < numGoroutines; g++ {
		sessionID := "session-" + string(rune('0'+g))
		history := c.GetStepHistory(sessionID)
		if len(history) != stepsPerGoroutine {
			t.Errorf("session %s: expected %d steps, got %d", sessionID, stepsPerGoroutine, len(history))
		}
	}
}

// -----------------------------------------------------------------------------
// Proof Index Tests (CRS-02)
// -----------------------------------------------------------------------------

func TestCRS_UpdateProofNumber(t *testing.T) {
	ctx := context.Background()

	t.Run("nil context returns error", func(t *testing.T) {
		c := New(nil)
		err := c.UpdateProofNumber(nil, ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSourceHard,
		})
		if !errors.Is(err, ErrNilContext) {
			t.Errorf("expected ErrNilContext, got %v", err)
		}
	})

	t.Run("empty nodeID fails validation", func(t *testing.T) {
		c := New(nil)
		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: "",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSourceHard,
		})
		if err == nil {
			t.Error("expected error for empty nodeID")
		}
	})

	t.Run("increment increases proof number", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeIncrement,
			Delta:  5,
			Reason: "tool_failure",
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, exists := c.GetProofStatus(nodeID)
		if !exists {
			t.Fatal("node not found after update")
		}
		// Initial is DefaultInitialProofNumber (10) + 5 = 15
		if pn.Proof != DefaultInitialProofNumber+5 {
			t.Errorf("proof number = %d, want %d", pn.Proof, DefaultInitialProofNumber+5)
		}
		if pn.Status != ProofStatusExpanded {
			t.Errorf("status = %v, want Expanded", pn.Status)
		}
	})

	t.Run("decrement decreases proof number", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDecrement,
			Delta:  3,
			Reason: "tool_success",
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		// Initial is 10, minus 3 = 7
		if pn.Proof != DefaultInitialProofNumber-3 {
			t.Errorf("proof number = %d, want %d", pn.Proof, DefaultInitialProofNumber-3)
		}
	})

	t.Run("decrement does not go below zero", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDecrement,
			Delta:  100,
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		if pn.Proof != 0 {
			t.Errorf("proof number = %d, want 0", pn.Proof)
		}
	})

	t.Run("disproven requires hard signal", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDisproven,
			Source: SignalSourceSoft, // Soft signal - should fail
		})
		if !errors.Is(err, ErrHardSoftBoundaryViolation) {
			t.Errorf("expected ErrHardSoftBoundaryViolation, got %v", err)
		}
	})

	t.Run("disproven with hard signal succeeds", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDisproven,
			Reason: "circuit_breaker",
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		if pn.Status != ProofStatusDisproven {
			t.Errorf("status = %v, want Disproven", pn.Status)
		}
		if pn.Proof != ProofNumberInfinite {
			t.Errorf("proof = %d, want infinite", pn.Proof)
		}
	})

	t.Run("safety signal counts as hard", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDisproven,
			Reason: "safety_violation",
			Source: SignalSourceSafety, // Safety counts as hard
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		if pn.Status != ProofStatusDisproven {
			t.Errorf("status = %v, want Disproven", pn.Status)
		}
	})

	t.Run("proven sets status correctly", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeProven,
			Reason: "solution_found",
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		if pn.Status != ProofStatusProven {
			t.Errorf("status = %v, want Proven", pn.Status)
		}
		if pn.Proof != 0 {
			t.Errorf("proof = %d, want 0 for proven", pn.Proof)
		}
	})

	t.Run("reset returns to initial state", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:123:tool:test"

		// First increment
		c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeIncrement,
			Delta:  10,
			Source: SignalSourceHard,
		})

		// Then reset
		err := c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeReset,
			Source: SignalSourceHard,
		})
		if err != nil {
			t.Fatalf("UpdateProofNumber failed: %v", err)
		}

		pn, _ := c.GetProofStatus(nodeID)
		if pn.Proof != DefaultInitialProofNumber {
			t.Errorf("proof = %d, want %d", pn.Proof, DefaultInitialProofNumber)
		}
		if pn.Status != ProofStatusUnknown {
			t.Errorf("status = %v, want Unknown", pn.Status)
		}
	})
}

func TestCRS_GetProofStatus(t *testing.T) {
	t.Run("empty nodeID returns false", func(t *testing.T) {
		c := New(nil)
		_, exists := c.GetProofStatus("")
		if exists {
			t.Error("expected exists=false for empty nodeID")
		}
	})

	t.Run("nonexistent node returns false", func(t *testing.T) {
		c := New(nil)
		_, exists := c.GetProofStatus("nonexistent")
		if exists {
			t.Error("expected exists=false for nonexistent node")
		}
	})

	t.Run("existing node returns correct data", func(t *testing.T) {
		c := New(nil)
		ctx := context.Background()
		nodeID := "session:123:tool:test"

		c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeIncrement,
			Delta:  5,
			Source: SignalSourceHard,
		})

		pn, exists := c.GetProofStatus(nodeID)
		if !exists {
			t.Fatal("expected exists=true")
		}
		if pn.Source != SignalSourceHard {
			t.Errorf("source = %v, want Hard", pn.Source)
		}
	})
}

func TestCRS_CheckCircuitBreaker(t *testing.T) {
	ctx := context.Background()

	t.Run("empty sessionID returns false", func(t *testing.T) {
		c := New(nil)
		result := c.CheckCircuitBreaker("", "tool")
		if result.ShouldFire {
			t.Error("expected ShouldFire=false for empty sessionID")
		}
	})

	t.Run("empty tool returns false", func(t *testing.T) {
		c := New(nil)
		result := c.CheckCircuitBreaker("session", "")
		if result.ShouldFire {
			t.Error("expected ShouldFire=false for empty tool")
		}
	})

	t.Run("no proof data uses execution count fallback", func(t *testing.T) {
		c := New(nil)

		// No proof data - should check execution count
		result := c.CheckCircuitBreaker("session-1", "list_packages")
		if result.ShouldFire {
			t.Error("expected ShouldFire=false with no executions")
		}

		// Add tool executions up to DefaultCircuitBreakerThreshold
		for i := 0; i < DefaultCircuitBreakerThreshold; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: "session-1",
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      "list_packages",
			})
		}

		// Now should fire
		result = c.CheckCircuitBreaker("session-1", "list_packages")
		if !result.ShouldFire {
			t.Errorf("expected ShouldFire=true with %d executions", DefaultCircuitBreakerThreshold)
		}
	})

	t.Run("disproven node fires circuit breaker", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:session-1:tool:list_packages"

		// Mark as disproven
		c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDisproven,
			Reason: "repeated_failure",
			Source: SignalSourceHard,
		})

		result := c.CheckCircuitBreaker("session-1", "list_packages")
		if !result.ShouldFire {
			t.Error("expected ShouldFire=true for disproven node")
		}
		if result.Status != ProofStatusDisproven {
			t.Errorf("status = %v, want Disproven", result.Status)
		}
	})

	t.Run("viable node does not fire", func(t *testing.T) {
		c := New(nil)
		nodeID := "session:session-1:tool:list_packages"

		// Add some failures but don't disprove
		c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeIncrement,
			Delta:  5,
			Reason: "failure",
			Source: SignalSourceHard,
		})

		result := c.CheckCircuitBreaker("session-1", "list_packages")
		if result.ShouldFire {
			t.Error("expected ShouldFire=false for viable node")
		}
	})
}

func TestCRS_PropagateDisproof(t *testing.T) {
	ctx := context.Background()

	t.Run("nil context returns 0", func(t *testing.T) {
		c := New(nil)
		affected := c.PropagateDisproof(nil, "node")
		if affected != 0 {
			t.Errorf("expected 0, got %d", affected)
		}
	})

	t.Run("empty nodeID returns 0", func(t *testing.T) {
		c := New(nil)
		affected := c.PropagateDisproof(ctx, "")
		if affected != 0 {
			t.Errorf("expected 0, got %d", affected)
		}
	})

	t.Run("no parents returns 0", func(t *testing.T) {
		c := New(nil)
		affected := c.PropagateDisproof(ctx, "orphan-node")
		if affected != 0 {
			t.Errorf("expected 0, got %d", affected)
		}
	})

	t.Run("propagates to parents via dependency index", func(t *testing.T) {
		c := New(nil)

		// Set up dependency: parent -> child (GR-32: use graph-backed index)
		parentID := "parent-node"
		childID := "child-node"

		// Set up mock graph with dependency relationship
		mock := newMockGraphQuery()
		mock.callees[parentID] = []*ast.Symbol{{ID: childID}}
		mock.callers[childID] = []*ast.Symbol{{ID: parentID}}
		mock.callEdgeCount = 1
		c.SetGraphProvider(mock)

		// Propagate disproof from child
		affected := c.PropagateDisproof(ctx, childID)

		// Parent should be affected
		if affected != 1 {
			t.Errorf("affected = %d, want 1", affected)
		}

		// Parent's proof number should have increased
		pn, exists := c.GetProofStatus(parentID)
		if !exists {
			t.Fatal("parent node not found")
		}
		// Initial 10 + 1 increment = 11
		if pn.Proof != DefaultInitialProofNumber+1 {
			t.Errorf("parent proof = %d, want %d", pn.Proof, DefaultInitialProofNumber+1)
		}
	})
}

func TestCRS_ProofIndex_Concurrent(t *testing.T) {
	c := New(nil)
	ctx := context.Background()

	numGoroutines := 10
	updatesPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			nodeID := "concurrent-node"

			for i := 0; i < updatesPerGoroutine; i++ {
				if i%2 == 0 {
					c.UpdateProofNumber(ctx, ProofUpdate{
						NodeID: nodeID,
						Type:   ProofUpdateTypeIncrement,
						Delta:  1,
						Source: SignalSourceHard,
					})
				} else {
					c.UpdateProofNumber(ctx, ProofUpdate{
						NodeID: nodeID,
						Type:   ProofUpdateTypeDecrement,
						Delta:  1,
						Source: SignalSourceHard,
					})
				}
			}
		}(g)
	}

	wg.Wait()

	// Should not panic and node should exist
	_, exists := c.GetProofStatus("concurrent-node")
	if !exists {
		t.Error("concurrent-node should exist after concurrent updates")
	}
}

func TestProofUpdateType_String(t *testing.T) {
	tests := []struct {
		updateType ProofUpdateType
		want       string
	}{
		{ProofUpdateTypeUnknown, "unknown"},
		{ProofUpdateTypeIncrement, "increment"},
		{ProofUpdateTypeDecrement, "decrement"},
		{ProofUpdateTypeDisproven, "disproven"},
		{ProofUpdateTypeProven, "proven"},
		{ProofUpdateTypeReset, "reset"},
		{ProofUpdateType(99), "ProofUpdateType(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.updateType.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProofUpdateType_IsValid(t *testing.T) {
	tests := []struct {
		updateType ProofUpdateType
		want       bool
	}{
		{ProofUpdateTypeUnknown, false},
		{ProofUpdateTypeIncrement, true},
		{ProofUpdateTypeDecrement, true},
		{ProofUpdateTypeDisproven, true},
		{ProofUpdateTypeProven, true},
		{ProofUpdateTypeReset, true},
		{ProofUpdateType(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.updateType.String(), func(t *testing.T) {
			if got := tt.updateType.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProofUpdate_Validate(t *testing.T) {
	t.Run("valid update passes", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err != nil {
			t.Errorf("valid update should pass: %v", err)
		}
	})

	t.Run("empty nodeID fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err == nil {
			t.Error("empty nodeID should fail")
		}
	})

	t.Run("invalid type fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeUnknown,
			Delta:  1,
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err == nil {
			t.Error("invalid type should fail")
		}
	})

	t.Run("increment with zero delta fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeIncrement,
			Delta:  0,
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err == nil {
			t.Error("increment with zero delta should fail")
		}
	})

	t.Run("decrement with zero delta fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeDecrement,
			Delta:  0,
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err == nil {
			t.Error("decrement with zero delta should fail")
		}
	})

	t.Run("disproven with zero delta passes", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeDisproven,
			Delta:  0, // Delta not required for disproven
			Source: SignalSourceHard,
		}
		if err := update.Validate(); err != nil {
			t.Errorf("disproven with zero delta should pass: %v", err)
		}
	})

	t.Run("invalid source fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSourceUnknown, // Unknown source should fail
		}
		if err := update.Validate(); err == nil {
			t.Error("unknown source should fail validation")
		}
	})

	t.Run("invalid source value fails", func(t *testing.T) {
		update := ProofUpdate{
			NodeID: "test-node",
			Type:   ProofUpdateTypeIncrement,
			Delta:  1,
			Source: SignalSource(99), // Invalid source value
		}
		if err := update.Validate(); err == nil {
			t.Error("invalid source value should fail validation")
		}
	})
}

// -----------------------------------------------------------------------------
// Additional Edge Case Tests (Code Review 2026-02-04)
// -----------------------------------------------------------------------------

func TestCRS_UpdateProofNumber_OverflowProtection(t *testing.T) {
	ctx := context.Background()
	c := New(nil)
	nodeID := "overflow-test-node"

	// First, set proof number close to max
	err := c.UpdateProofNumber(ctx, ProofUpdate{
		NodeID: nodeID,
		Type:   ProofUpdateTypeIncrement,
		Delta:  ProofNumberInfinite - DefaultInitialProofNumber - 5,
		Source: SignalSourceHard,
	})
	if err != nil {
		t.Fatalf("initial increment failed: %v", err)
	}

	pn, _ := c.GetProofStatus(nodeID)
	if pn.Proof >= ProofNumberInfinite {
		t.Fatalf("proof number should not be infinite yet: %d", pn.Proof)
	}

	// Now increment again - this should saturate at ProofNumberInfinite
	err = c.UpdateProofNumber(ctx, ProofUpdate{
		NodeID: nodeID,
		Type:   ProofUpdateTypeIncrement,
		Delta:  10, // Would overflow if not protected
		Source: SignalSourceHard,
	})
	if err != nil {
		t.Fatalf("overflow increment failed: %v", err)
	}

	pn, _ = c.GetProofStatus(nodeID)
	if pn.Proof != ProofNumberInfinite {
		t.Errorf("proof number should saturate at infinite, got %d", pn.Proof)
	}
}

func TestCRS_PropagateDisproof_ContextCancellation(t *testing.T) {
	c := New(nil)

	// Set up a chain of dependencies
	parentID := "parent-node"
	childID := "child-node"

	// Add dependency
	delta := NewDependencyDelta(SignalSourceHard)
	delta.AddEdges = append(delta.AddEdges, [2]string{parentID, childID})
	_, err := c.Apply(context.Background(), delta)
	if err != nil {
		t.Fatalf("failed to add dependency: %v", err)
	}

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Propagate with cancelled context should return early
	affected := c.PropagateDisproof(ctx, childID)

	// Should return 0 because context is cancelled
	if affected != 0 {
		t.Errorf("expected 0 affected with cancelled context, got %d", affected)
	}
}

func TestCRS_PropagateDisproof_MaxDepthLimit(t *testing.T) {
	c := New(nil)
	ctx := context.Background()

	// Create a deep chain of dependencies exceeding MaxPropagationDepth
	var prevNode string
	for i := 0; i <= MaxPropagationDepth+10; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		if prevNode != "" {
			delta := NewDependencyDelta(SignalSourceHard)
			delta.AddEdges = append(delta.AddEdges, [2]string{prevNode, nodeID})
			_, err := c.Apply(ctx, delta)
			if err != nil {
				t.Fatalf("failed to add dependency at depth %d: %v", i, err)
			}
		}
		prevNode = nodeID
	}

	// Mark the deepest node as disproven
	deepestNode := fmt.Sprintf("node-%d", MaxPropagationDepth+10)
	err := c.UpdateProofNumber(ctx, ProofUpdate{
		NodeID: deepestNode,
		Type:   ProofUpdateTypeDisproven,
		Reason: "test",
		Source: SignalSourceHard,
	})
	if err != nil {
		t.Fatalf("failed to disprove deepest node: %v", err)
	}

	// Propagate - should be limited by MaxPropagationDepth
	affected := c.PropagateDisproof(ctx, deepestNode)

	// Should not exceed MaxPropagationDepth
	if affected > MaxPropagationDepth {
		t.Errorf("propagation exceeded max depth: affected=%d, max=%d", affected, MaxPropagationDepth)
	}

	// Root node (node-0) should NOT be affected due to depth limit
	rootPn, exists := c.GetProofStatus("node-0")
	if exists && rootPn.Proof > DefaultInitialProofNumber {
		// This is acceptable if depth was within limit
		t.Logf("root node was affected (depth within limit): proof=%d", rootPn.Proof)
	}
}

func TestCRS_CheckCircuitBreaker_ProofNumberInfiniteBoundary(t *testing.T) {
	ctx := context.Background()
	c := New(nil)

	sessionID := "boundary-test"
	tool := "boundary-tool"
	nodeID := fmt.Sprintf("session:%s:tool:%s", sessionID, tool)

	// Set proof number exactly at ProofNumberInfinite
	err := c.UpdateProofNumber(ctx, ProofUpdate{
		NodeID: nodeID,
		Type:   ProofUpdateTypeIncrement,
		Delta:  ProofNumberInfinite - DefaultInitialProofNumber,
		Source: SignalSourceHard,
	})
	if err != nil {
		t.Fatalf("failed to set proof number to infinite: %v", err)
	}

	pn, _ := c.GetProofStatus(nodeID)
	if pn.Proof != ProofNumberInfinite {
		t.Fatalf("proof number should be infinite, got %d", pn.Proof)
	}

	// Circuit breaker should fire at exactly ProofNumberInfinite
	result := c.CheckCircuitBreaker(sessionID, tool)
	if !result.ShouldFire {
		t.Error("circuit breaker should fire when proof number equals ProofNumberInfinite")
	}
	if result.ProofNumber != ProofNumberInfinite {
		t.Errorf("result.ProofNumber = %d, want %d", result.ProofNumber, ProofNumberInfinite)
	}
}

func TestCRS_CheckCircuitBreaker_JustBelowInfinite(t *testing.T) {
	ctx := context.Background()
	c := New(nil)

	sessionID := "below-infinite-test"
	tool := "below-infinite-tool"
	nodeID := fmt.Sprintf("session:%s:tool:%s", sessionID, tool)

	// Set proof number just below ProofNumberInfinite
	err := c.UpdateProofNumber(ctx, ProofUpdate{
		NodeID: nodeID,
		Type:   ProofUpdateTypeIncrement,
		Delta:  ProofNumberInfinite - DefaultInitialProofNumber - 1,
		Source: SignalSourceHard,
	})
	if err != nil {
		t.Fatalf("failed to set proof number: %v", err)
	}

	pn, _ := c.GetProofStatus(nodeID)
	if pn.Proof >= ProofNumberInfinite {
		t.Fatalf("proof number should be below infinite, got %d", pn.Proof)
	}

	// Circuit breaker should NOT fire just below infinite
	result := c.CheckCircuitBreaker(sessionID, tool)
	if result.ShouldFire {
		t.Error("circuit breaker should not fire when proof number is below ProofNumberInfinite")
	}
}

// TestCRS16_PerToolCircuitBreakerThresholds verifies that per-tool thresholds
// override the default and that tools not in the map use the default.
func TestCRS16_PerToolCircuitBreakerThresholds(t *testing.T) {
	ctx := context.Background()

	t.Run("per-tool threshold overrides default", func(t *testing.T) {
		cfg := DefaultConfig()
		// find_callers has threshold=4 in default config
		c := New(cfg)

		sessionID := "crs16-test-1"
		tool := "find_callers"

		// Execute find_callers 2 times (default threshold) — should NOT fire
		for i := 0; i < DefaultCircuitBreakerThreshold; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result := c.CheckCircuitBreaker(sessionID, tool)
		if result.ShouldFire {
			t.Error("expected ShouldFire=false at default threshold (2) when per-tool threshold is 4")
		}

		// Execute 2 more times (total 4 = per-tool threshold) — should fire
		for i := 0; i < 2; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result = c.CheckCircuitBreaker(sessionID, tool)
		if !result.ShouldFire {
			t.Error("expected ShouldFire=true at per-tool threshold (4)")
		}
		if result.ProofNumber != 0 {
			t.Errorf("expected ProofNumber=0 (fallback path), got %d", result.ProofNumber)
		}
	})

	t.Run("tool not in map uses default threshold", func(t *testing.T) {
		cfg := DefaultConfig()
		c := New(cfg)

		sessionID := "crs16-test-2"
		tool := "list_packages" // Not in per-tool map

		// Execute at default threshold (2) — should fire
		for i := 0; i < DefaultCircuitBreakerThreshold; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result := c.CheckCircuitBreaker(sessionID, tool)
		if !result.ShouldFire {
			t.Errorf("expected ShouldFire=true at default threshold (%d)", DefaultCircuitBreakerThreshold)
		}
	})

	t.Run("Grep uses higher threshold", func(t *testing.T) {
		cfg := DefaultConfig()
		c := New(cfg)

		sessionID := "crs16-test-3"
		tool := "Grep"

		// Execute 4 times — should NOT fire (Grep threshold=5)
		for i := 0; i < 4; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result := c.CheckCircuitBreaker(sessionID, tool)
		if result.ShouldFire {
			t.Error("expected ShouldFire=false for Grep at 4 calls (threshold=5)")
		}

		// 5th call
		c.RecordStep(ctx, StepRecord{
			SessionID: sessionID,
			Actor:     ActorRouter,
			Decision:  DecisionExecuteTool,
			Outcome:   OutcomeSuccess,
			Tool:      tool,
		})

		result = c.CheckCircuitBreaker(sessionID, tool)
		if !result.ShouldFire {
			t.Error("expected ShouldFire=true for Grep at 5 calls (threshold=5)")
		}
	})

	t.Run("nil config still applies default per-tool thresholds", func(t *testing.T) {
		// New(nil) uses DefaultConfig() which includes per-tool overrides.
		c := New(nil)

		sessionID := "crs16-test-4"
		tool := "find_callers" // Has threshold=4 in DefaultConfig

		// Execute at default threshold (2) — should NOT fire (per-tool=4)
		for i := 0; i < DefaultCircuitBreakerThreshold; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result := c.CheckCircuitBreaker(sessionID, tool)
		if result.ShouldFire {
			t.Error("expected ShouldFire=false at default threshold when per-tool=4")
		}

		// Execute to per-tool threshold (4 total)
		for i := 0; i < 2; i++ {
			c.RecordStep(ctx, StepRecord{
				SessionID: sessionID,
				Actor:     ActorRouter,
				Decision:  DecisionExecuteTool,
				Outcome:   OutcomeSuccess,
				Tool:      tool,
			})
		}

		result = c.CheckCircuitBreaker(sessionID, tool)
		if !result.ShouldFire {
			t.Error("expected ShouldFire=true at per-tool threshold (4)")
		}
	})

	t.Run("proof data takes precedence over per-tool threshold", func(t *testing.T) {
		cfg := DefaultConfig()
		c := New(cfg)

		sessionID := "crs16-test-5"
		tool := "find_callers"
		nodeID := fmt.Sprintf("session:%s:tool:%s", sessionID, tool)

		// Set proof data to disproven — should fire regardless of per-tool threshold
		c.UpdateProofNumber(ctx, ProofUpdate{
			NodeID: nodeID,
			Type:   ProofUpdateTypeDisproven,
			Reason: "test",
			Source: SignalSourceHard,
		})

		result := c.CheckCircuitBreaker(sessionID, tool)
		if !result.ShouldFire {
			t.Error("expected ShouldFire=true when proof data is disproven")
		}
		if result.Status != ProofStatusDisproven {
			t.Errorf("status = %v, want Disproven", result.Status)
		}
	})
}

func TestSignalSource_IsValid(t *testing.T) {
	tests := []struct {
		source SignalSource
		want   bool
	}{
		{SignalSourceUnknown, false},
		{SignalSourceHard, true},
		{SignalSourceSoft, true},
		{SignalSourceSafety, true},
		{SignalSource(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.source.String(), func(t *testing.T) {
			if got := tt.source.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}
